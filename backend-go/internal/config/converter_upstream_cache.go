package config

import (
	"encoding/json"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ConverterUpstreamStatePath 转换层上游指纹记忆的默认落盘位置。
// 与 channel_compat.json 同级，属于内部运行时状态，不进 config.json，用户无需感知或配置。
const ConverterUpstreamStatePath = ".config/converter_upstreams.json"

// converterUpstreamTTL 指纹记忆的有效期。
// 上游站点可能更换实现（如从 new-api 换成原生网关），过期后按响应头重新学习；
// 活跃渠道每次命中指纹都会刷新时间戳，实际上不会过期。
const converterUpstreamTTL = 7 * 24 * time.Hour

// ConverterUpstreamEntry 记录某个渠道被识别为"转换层上游"的事实。
// 转换层上游指 new-api/one-api 系中转：Anthropic Messages 请求会被转换为
// OpenAI 兼容格式再发给后端模型，遇到 messages 数组中间的 system 角色时
// 会截断上下文（模型表现为只看到最后一个 system 之后的内容）。
type ConverterUpstreamEntry struct {
	// Source 识别依据，目前为命中的响应头名（如 "x-new-api-version"）
	Source string `json:"source"`
	// UpdatedAt 最近一次命中指纹的时间，用于 TTL 判定
	UpdatedAt time.Time `json:"updated_at"`
}

// ConverterUpstreamCache 按渠道（ChannelUID）记忆"该上游是转换层"的事实。
//
// 为什么按渠道而不是 渠道-Key-模型：是否转换层是站点级事实，与用哪个 Key、
// 跑哪个模型无关；按组合学习会在每个 Key×模型 上重复消耗首次请求的损耗。
//
// 与 ChannelCompatCache 的区别：compat cache 的学习信号来自上游明确报错（400/422），
// 而转换层指纹来自任意响应的响应头（X-New-Api-Version 等），错误响应同样携带，
// 因此学习时机更早、也不依赖错误解析。
type ConverterUpstreamCache struct {
	cache map[string]*ConverterUpstreamEntry
	mu    sync.RWMutex
	// path 为空表示纯内存模式（测试与未启用持久化时）。
	path string
	// dirty 标记自上次落盘后是否有新增记忆，避免无变化时重复写盘。
	dirty bool
}

// NewConverterUpstreamCache 创建纯内存缓存实例（不落盘）。
func NewConverterUpstreamCache() *ConverterUpstreamCache {
	return &ConverterUpstreamCache{cache: make(map[string]*ConverterUpstreamEntry)}
}

// NewConverterUpstreamCacheWithPersistence 创建带落盘的缓存实例，并立即加载已有记忆。
// 加载失败（文件缺失/损坏）时退化为空缓存并继续运行：记忆丢失只意味着重新学习一次，
// 不应阻断代理服务启动。
func NewConverterUpstreamCacheWithPersistence(path string) *ConverterUpstreamCache {
	c := &ConverterUpstreamCache{
		cache: make(map[string]*ConverterUpstreamEntry),
		path:  path,
	}
	if err := c.load(); err != nil {
		log.Printf("[ConverterUpstream-Load] 加载转换层指纹记忆失败，从空状态开始: %v", err)
	} else if n := c.Size(); n > 0 {
		log.Printf("[ConverterUpstream-Load] 已加载 %d 条转换层指纹记忆", n)
	}
	return c
}

// load 从磁盘读取记忆，跳过已过期条目。
func (c *ConverterUpstreamCache) load() error {
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

	var stored map[string]*ConverterUpstreamEntry
	if err := json.Unmarshal(data, &stored); err != nil {
		return err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	for key, entry := range stored {
		if entry == nil {
			continue
		}
		// 过期条目不加载，等价于重新学习
		if time.Since(entry.UpdatedAt) > converterUpstreamTTL {
			continue
		}
		c.cache[key] = entry
	}
	return nil
}

// Flush 将当前记忆原子落盘（tmp + rename）。无变化时为空操作。
func (c *ConverterUpstreamCache) Flush() error {
	c.mu.Lock()
	if c.path == "" || !c.dirty {
		c.mu.Unlock()
		return nil
	}
	// 仅序列化未过期条目，顺带完成落盘时的清理
	snapshot := make(map[string]*ConverterUpstreamEntry, len(c.cache))
	for key, entry := range c.cache {
		if time.Since(entry.UpdatedAt) <= converterUpstreamTTL {
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

// Mark 记录某渠道命中转换层指纹。返回是否为首次新增（调用方据此决定是否打日志）。
// 过期条目视为新增（等价于重新学习）；已有有效记忆时仅刷新时间戳（续期），不重复落盘。
func (c *ConverterUpstreamCache) Mark(channelUID, source string) bool {
	if channelUID == "" {
		return false
	}

	c.mu.Lock()
	entry, ok := c.cache[channelUID]
	isNew := !ok || time.Since(entry.UpdatedAt) > converterUpstreamTTL
	if isNew {
		entry = &ConverterUpstreamEntry{Source: source}
		c.cache[channelUID] = entry
		c.dirty = true
	}
	entry.UpdatedAt = time.Now()
	c.mu.Unlock()

	if !isNew {
		return false
	}
	// 锁外做 IO，避免阻塞并发请求路径
	if err := c.Flush(); err != nil {
		log.Printf("[ConverterUpstream-Flush] 落盘转换层指纹记忆失败: %v", err)
	}
	return true
}

// IsConverter 返回该渠道是否已被识别为转换层上游。过期或未学习过时返回 false（fail-open）。
func (c *ConverterUpstreamCache) IsConverter(channelUID string) bool {
	if channelUID == "" {
		return false
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, ok := c.cache[channelUID]
	if !ok {
		return false
	}
	return time.Since(entry.UpdatedAt) <= converterUpstreamTTL
}

// Clear 清除所有缓存。
func (c *ConverterUpstreamCache) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.cache = make(map[string]*ConverterUpstreamEntry)
}

// Size 返回缓存条目数量。
func (c *ConverterUpstreamCache) Size() int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	return len(c.cache)
}
