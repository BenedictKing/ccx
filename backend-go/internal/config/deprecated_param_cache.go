package config

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// DeprecatedParamStatePath 弃用参数记忆的默认落盘位置。
// 属于内部运行时状态（与 scheduled_recovery_state.json 同级），不进 config.json，
// 用户无需感知或配置。
const DeprecatedParamStatePath = ".config/deprecated_params.json"

// deprecatedParamTTL 弃用参数记忆的有效期。
// 与 SystemHeaderFilterCache 保持一致：上游模型能力变化后自动重新探测。
const deprecatedParamTTL = 24 * time.Hour

// DeprecatedParamEntry 记录单个渠道-key-模型已探测到的弃用参数集合。
type DeprecatedParamEntry struct {
	Params     map[string]time.Time `json:"params"`      // 参数名 -> 首次探测时间
	DetectedAt time.Time            `json:"detected_at"` // 最近一次探测/命中时间
	StripCount int                  `json:"strip_count"` // 命中记忆并主动剥离的次数
}

// DeprecatedParamCache 按渠道-key-模型记忆上游拒绝的弃用请求参数。
// 首次遇到 "X is deprecated" 类 400 后写入，后续同组合请求在发送前主动剥离，
// 避免每次都消耗一次失败往返。
type DeprecatedParamCache struct {
	cache map[string]*DeprecatedParamEntry
	mu    sync.RWMutex
	// path 为落盘路径；为空表示纯内存模式（测试与未启用持久化时）。
	path string
	// dirty 标记自上次落盘后是否有新增记忆，避免无变化时重复写盘。
	dirty bool
}

// NewDeprecatedParamCache 创建纯内存缓存实例（不落盘）。
func NewDeprecatedParamCache() *DeprecatedParamCache {
	return &DeprecatedParamCache{
		cache: make(map[string]*DeprecatedParamEntry),
	}
}

// NewDeprecatedParamCacheWithPersistence 创建带落盘的缓存实例，并立即加载已有记忆。
// 加载失败（文件缺失/损坏）时退化为空缓存并继续运行：记忆丢失只意味着重新探测一次，
// 不应阻断代理服务启动。
func NewDeprecatedParamCacheWithPersistence(path string) *DeprecatedParamCache {
	c := &DeprecatedParamCache{
		cache: make(map[string]*DeprecatedParamEntry),
		path:  path,
	}
	if err := c.load(); err != nil {
		log.Printf("[DeprecatedParam-Load] 加载弃用参数记忆失败，从空状态开始: %v", err)
	} else if n := c.Size(); n > 0 {
		log.Printf("[DeprecatedParam-Load] 已加载 %d 条弃用参数记忆", n)
	}
	return c
}

// load 从磁盘读取记忆，跳过已过期条目。
func (c *DeprecatedParamCache) load() error {
	if c.path == "" {
		return nil
	}
	data, err := os.ReadFile(c.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(data) == 0 {
		return nil
	}

	var stored map[string]*DeprecatedParamEntry
	if err := json.Unmarshal(data, &stored); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	for key, entry := range stored {
		if entry == nil || entry.Params == nil {
			continue
		}
		// 过期条目不加载，等价于重新探测
		if time.Since(entry.DetectedAt) > deprecatedParamTTL {
			continue
		}
		c.cache[key] = entry
	}
	return nil
}

// Flush 将当前记忆原子落盘（tmp + rename）。无变化时为空操作。
func (c *DeprecatedParamCache) Flush() error {
	c.mu.Lock()
	if c.path == "" || !c.dirty {
		c.mu.Unlock()
		return nil
	}
	// 仅序列化未过期条目，顺带完成落盘时的清理
	snapshot := make(map[string]*DeprecatedParamEntry, len(c.cache))
	for key, entry := range c.cache {
		if time.Since(entry.DetectedAt) <= deprecatedParamTTL {
			snapshot[key] = entry
		}
	}
	path := c.path
	c.dirty = false
	data, err := json.MarshalIndent(snapshot, "", "  ")
	c.mu.Unlock()
	if err != nil {
		return err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Record 记录一个探测到的弃用参数。返回该参数是否为新增（此前未记录）。
// 新增时立即落盘：单次探测代价是一条上游 400，值得同步持久化。
func (c *DeprecatedParamCache) Record(channelUID, keyHash, model, param string) bool {
	if param == "" {
		return false
	}

	c.mu.Lock()
	key := GenerateCacheKey(channelUID, keyHash, model)
	entry, ok := c.cache[key]
	if !ok || time.Since(entry.DetectedAt) > deprecatedParamTTL {
		entry = &DeprecatedParamEntry{Params: make(map[string]time.Time)}
		c.cache[key] = entry
	}
	entry.DetectedAt = time.Now()
	_, exists := entry.Params[param]
	if !exists {
		entry.Params[param] = time.Now()
		c.dirty = true
	}
	c.mu.Unlock()

	if exists {
		return false
	}
	// 锁外做 IO，避免阻塞并发请求路径
	if err := c.Flush(); err != nil {
		log.Printf("[DeprecatedParam-Flush] 落盘弃用参数记忆失败: %v", err)
	}
	return true
}

// Params 返回该组合已记忆的弃用参数列表（按字母序，便于日志稳定输出）。
// 条目过期或不存在时返回 nil。
func (c *DeprecatedParamCache) Params(channelUID, keyHash, model string) []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.cache[GenerateCacheKey(channelUID, keyHash, model)]
	if !ok || time.Since(entry.DetectedAt) > deprecatedParamTTL {
		return nil
	}

	params := make([]string, 0, len(entry.Params))
	for param := range entry.Params {
		params = append(params, param)
	}
	sort.Strings(params)
	return params
}

// MarkStripped 记录一次基于记忆的主动剥离，并刷新有效期。
func (c *DeprecatedParamCache) MarkStripped(channelUID, keyHash, model string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if entry, ok := c.cache[GenerateCacheKey(channelUID, keyHash, model)]; ok {
		entry.StripCount++
		entry.DetectedAt = time.Now()
	}
}

// Clear 清除所有缓存。
func (c *DeprecatedParamCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cache = make(map[string]*DeprecatedParamEntry)
}

// Size 返回缓存条目数量。
func (c *DeprecatedParamCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return len(c.cache)
}
