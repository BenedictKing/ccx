package config

import (
	"sort"
	"sync"
	"time"
)

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
}

// NewDeprecatedParamCache 创建新的缓存实例。
func NewDeprecatedParamCache() *DeprecatedParamCache {
	return &DeprecatedParamCache{
		cache: make(map[string]*DeprecatedParamEntry),
	}
}

// Record 记录一个探测到的弃用参数。返回该参数是否为新增（此前未记录）。
func (c *DeprecatedParamCache) Record(channelUID, keyHash, model, param string) bool {
	if param == "" {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	key := GenerateCacheKey(channelUID, keyHash, model)
	entry, ok := c.cache[key]
	if !ok || time.Since(entry.DetectedAt) > deprecatedParamTTL {
		entry = &DeprecatedParamEntry{Params: make(map[string]time.Time)}
		c.cache[key] = entry
	}

	entry.DetectedAt = time.Now()
	if _, exists := entry.Params[param]; exists {
		return false
	}
	entry.Params[param] = time.Now()
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
