package ratelimit

import (
	"fmt"
	"log"
	"strings"
	"sync"
	"time"
)

// Manager 按 (apiType, channelIndex) 管理所有渠道的 ChannelLimiter 实例。
type Manager struct {
	mu          sync.RWMutex
	limiters    map[string]*ChannelLimiter
	channelUIDs map[string]string
	stopCh      chan struct{}
}

// ChannelConfig 是一个配置渠道在 limiter Manager 中的稳定身份及限速配置。
// ChannelUID 用于识别索引重排或渠道替换，避免旧 limiter 状态污染新渠道。
type ChannelConfig struct {
	APIType      string
	ChannelIndex int
	ChannelUID   string
	Config       Config
}

// NewManager 创建一个空的限速器管理器。
func NewManager() *Manager {
	m := &Manager{
		limiters:    make(map[string]*ChannelLimiter),
		channelUIDs: make(map[string]string),
		stopCh:      make(chan struct{}),
	}
	go m.cleanupStaleLimiters()
	return m
}

// ReconcileChannelConfigs 以当前完整渠道快照更新 channel limiter。
// 若索引消失或同一索引的 ChannelUID 变化，会先移除该索引的 channel/scoped
// limiter，再按新配置创建，避免已发现 RPM、cooldown 或 key scope 泄漏到新渠道。
func (m *Manager) ReconcileChannelConfigs(entries []ChannelConfig) {
	if m == nil {
		return
	}

	desired := make(map[string]ChannelConfig, len(entries))
	for _, entry := range entries {
		if entry.APIType == "" || entry.ChannelIndex < 0 {
			continue
		}
		desired[limiterKey(entry.APIType, entry.ChannelIndex)] = entry
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	for key, previousUID := range m.channelUIDs {
		entry, exists := desired[key]
		if exists && entry.ChannelUID == previousUID {
			continue
		}
		m.removeChannelLocked(key)
	}

	for key, entry := range desired {
		if limiter := m.limiters[key]; limiter != nil {
			limiter.UpdateConfig(entry.Config)
		} else {
			m.limiters[key] = NewChannelLimiter(entry.Config, time.Now())
		}
	}

	m.channelUIDs = make(map[string]string, len(desired))
	for key, entry := range desired {
		m.channelUIDs[key] = entry.ChannelUID
	}
}

// Stop 停止后台清理协程。
func (m *Manager) Stop() {
	close(m.stopCh)
}

func limiterKey(apiType string, channelIndex int) string {
	return fmt.Sprintf("%s:%d", apiType, channelIndex)
}

func scopedLimiterKey(apiType string, channelIndex int, scope string) string {
	return fmt.Sprintf("%s:%d:%s", apiType, channelIndex, scope)
}

// GetOrCreate 获取或创建指定渠道的 limiter。如果已存在则更新配置。
func (m *Manager) GetOrCreate(apiType string, channelIndex int, cfg Config) *ChannelLimiter {
	key := limiterKey(apiType, channelIndex)

	m.mu.RLock()
	if l, ok := m.limiters[key]; ok {
		m.mu.RUnlock()
		l.UpdateConfig(cfg)
		return l
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()

	// 双重检查
	if l, ok := m.limiters[key]; ok {
		l.UpdateConfig(cfg)
		return l
	}

	now := time.Now()
	l := NewChannelLimiter(cfg, now)
	m.limiters[key] = l

	if cfg.RPM > 0 || cfg.MaxConcurrent > 0 {
		log.Printf("[RateLimit-Manager] 创建渠道限速器: %s [%d] (RPM=%d, burst=%d, concurrent=%d, autoHeaders=%v)",
			apiType, channelIndex, cfg.RPM, cfg.Burst, cfg.MaxConcurrent, cfg.AutoFromHeaders)
	}

	return l
}

// Get 获取指定渠道的 limiter。不存在返回 nil。
func (m *Manager) Get(apiType string, channelIndex int) *ChannelLimiter {
	key := limiterKey(apiType, channelIndex)
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.limiters[key]
}

// GetOrCreateScoped 获取或创建指定渠道下某个 scope 的 limiter。scope 应使用 credential ID/hash 或 quota group，不能使用明文 API Key。
func (m *Manager) GetOrCreateScoped(apiType string, channelIndex int, scope string, cfg Config) *ChannelLimiter {
	if scope == "" {
		return m.GetOrCreate(apiType, channelIndex, cfg)
	}
	key := scopedLimiterKey(apiType, channelIndex, scope)

	m.mu.RLock()
	if l, ok := m.limiters[key]; ok {
		m.mu.RUnlock()
		l.UpdateConfig(cfg)
		return l
	}
	m.mu.RUnlock()

	m.mu.Lock()
	defer m.mu.Unlock()
	if l, ok := m.limiters[key]; ok {
		l.UpdateConfig(cfg)
		return l
	}

	l := NewChannelLimiter(cfg, time.Now())
	m.limiters[key] = l
	if cfg.RPM > 0 || cfg.MaxConcurrent > 0 {
		log.Printf("[RateLimit-Manager] 创建 scoped 限速器: %s [%d] scope=%s (RPM=%d, burst=%d, concurrent=%d, autoHeaders=%v)",
			apiType, channelIndex, scope, cfg.RPM, cfg.Burst, cfg.MaxConcurrent, cfg.AutoFromHeaders)
	}
	return l
}

// GetScoped 获取指定渠道下某个 scope 的 limiter。不存在返回 nil。
func (m *Manager) GetScoped(apiType string, channelIndex int, scope string) *ChannelLimiter {
	if scope == "" {
		return m.Get(apiType, channelIndex)
	}
	key := scopedLimiterKey(apiType, channelIndex, scope)
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.limiters[key]
}

// SetCooldownScoped 将指定 scoped limiter 置入短期冷却。
func (m *Manager) SetCooldownScoped(apiType string, channelIndex int, scope string, duration time.Duration, now time.Time) {
	if m == nil || duration <= 0 {
		return
	}
	m.GetOrCreateScoped(apiType, channelIndex, scope, Config{}).SetCooldown(now.Add(duration))
}

// SetCooldown 将指定渠道置入短期冷却；不存在 limiter 时创建一个不限速 limiter 承载运行态冷却。
func (m *Manager) SetCooldown(apiType string, channelIndex int, duration time.Duration, now time.Time) {
	if m == nil || duration <= 0 {
		return
	}
	if l := m.Get(apiType, channelIndex); l != nil {
		l.SetCooldown(now.Add(duration))
		return
	}
	m.GetOrCreate(apiType, channelIndex, Config{}).SetCooldown(now.Add(duration))
}

// Remove 移除指定渠道的 limiter。
// Remove 移除指定渠道的 limiter，包括 channel 级和该 channel 下所有 scoped limiter（key/quota 级）。
// 渠道删除或 key 全部轮换时调用，避免 limiter map 累积无效条目。
func (m *Manager) Remove(apiType string, channelIndex int) {
	channelKey := limiterKey(apiType, channelIndex)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.removeChannelLocked(channelKey)
	delete(m.channelUIDs, channelKey)
}

func (m *Manager) removeChannelLocked(channelKey string) {
	scopedPrefix := channelKey + ":"
	delete(m.limiters, channelKey)
	for k := range m.limiters {
		if strings.HasPrefix(k, scopedPrefix) {
			delete(m.limiters, k)
		}
	}
}

// UpdateAll 通过回调函数批量更新所有 limiter 配置。
// fetcher 返回 (apiType, channelIndex) 对应的新配置，ok=false 表示该渠道不需要限速。
// UpdateAll 通过回调函数批量更新所有 limiter 配置。
// fetcher 返回 (apiType, channelIndex) 对应的新配置，ok=false 表示该渠道不需要限速。
// 注意：scoped limiter（key/quota 级）和 channel 级共享同一 (apiType, channelIndex) 配置；
// 解析 key 时丢弃 scope 部分，但用原始 key 从 map 中取 limiter，确保 scoped limiter 也被更新。
func (m *Manager) UpdateAll(fetcher func(apiType string, channelIndex int) (cfg Config, ok bool)) {
	m.mu.RLock()
	keys := make([]string, 0, len(m.limiters))
	for k := range m.limiters {
		keys = append(keys, k)
	}
	m.mu.RUnlock()

	for _, key := range keys {
		apiType, idx := parseKey(key)
		if cfg, ok := fetcher(apiType, idx); ok {
			m.mu.RLock()
			l := m.limiters[key]
			m.mu.RUnlock()
			if l != nil {
				l.UpdateConfig(cfg)
			}
		}
	}
}

// GetStatus 返回所有活跃 limiter 的状态快照。
func (m *Manager) GetStatus(now time.Time) map[string]LimiterStatus {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]LimiterStatus, len(m.limiters))
	for key, l := range m.limiters {
		result[key] = l.Status(now)
	}
	return result
}

func parseKey(key string) (apiType string, channelIndex int) {
	for i := 0; i < len(key); i++ {
		if key[i] == ':' {
			idx := 0
			_, _ = fmt.Sscanf(key[i+1:], "%d", &idx)
			return key[:i], idx
		}
	}
	return key, 0
}

// cleanupStaleLimiters 后台任务：每小时清理长期无活动的 scoped limiter 条目。
func (m *Manager) cleanupStaleLimiters() {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			m.removeStaleLimiters()
		case <-m.stopCh:
			return
		}
	}
}

func (m *Manager) removeStaleLimiters() {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now()
	staleThreshold := 48 * time.Hour
	var removed []string
	for key, l := range m.limiters {
		// 只清理 scoped limiter（key/quota 级），保留 channel 级 limiter
		if !strings.Contains(key, ":") || strings.Count(key, ":") < 2 {
			continue
		}
		if l.LastActivity().IsZero() || now.Sub(l.LastActivity()) <= staleThreshold {
			continue
		}
		// 仍在 cooldown 的 limiter 不应被清理，避免绕过上游要求的冷却窗口
		if inCooldown, _ := l.InCooldown(now); inCooldown {
			continue
		}
		delete(m.limiters, key)
		removed = append(removed, key)
	}
	if len(removed) > 0 {
		log.Printf("[RateLimit-Manager] 清理 %d 个过期 scoped 限速器: %v", len(removed), removed)
	}
}
