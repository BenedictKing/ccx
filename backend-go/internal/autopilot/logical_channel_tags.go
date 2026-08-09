package autopilot

import (
	"sync"

	"github.com/BenedictKing/ccx/internal/config"
)

// logicalChannelTagState 缓存最近一次成功派生出的标签，用于避免无意义的 Rebuild。
type logicalChannelTagState struct {
	mu    sync.RWMutex
	cache map[string]logicalChannelTags // key = logicalChannelUID
}

type logicalChannelTags struct {
	health       string
	quality      string
	cost         string
	capabilities []string
}

// newLogicalChannelTagState 创建空缓存。
func newLogicalChannelTagState() *logicalChannelTagState {
	return &logicalChannelTagState{cache: make(map[string]logicalChannelTags)}
}

func (s *logicalChannelTagState) get(uid string) (logicalChannelTags, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.cache[uid]
	return t, ok
}

func (s *logicalChannelTagState) set(uid string, t logicalChannelTags) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cache[uid] = t
}

// RegisterLogicalChannelTagDeriver 在 config 包注册逻辑渠道标签推导函数。
// 由 Manager 初始化时调用一次。
func RegisterLogicalChannelTagDeriver(m *Manager) {
	if m == nil {
		return
	}
	state := newLogicalChannelTagState()
	config.RegisterLogicalChannelTagDeriver(func(siblingChannelUIDs []string) (health, quality, cost string, capabilities []string, ok bool) {
		return deriveLogicalChannelTags(m, state, siblingChannelUIDs)
	})
}

// deriveLogicalChannelTags 聚合兄弟物理渠道画像，输出展示标签。
// 当无有效画像时返回 ok=false，config 侧保持标签为空（向后兼容）。
func deriveLogicalChannelTags(m *Manager, state *logicalChannelTagState, siblingChannelUIDs []string) (health, quality, cost string, capabilities []string, ok bool) {
	if m == nil || m.store == nil || len(siblingChannelUIDs) == 0 {
		return "", "", "", nil, false
	}

	// 收集所有兄弟渠道的 active endpoint 画像；所有 channelUID 自身都在 siblings 中。
	var endpoints []KeyEndpointProfile
	for _, uid := range siblingChannelUIDs {
		for _, p := range m.store.ListActiveByChannel(uid) {
			if p != nil {
				endpoints = append(endpoints, *p)
			}
		}
	}
	if len(endpoints) == 0 {
		return "", "", "", nil, false
	}

	profile := AggregateChannelProfile("", 0, "", endpoints)

	health = string(profile.HealthState)
	quality = string(profile.QualityTier)
	cost = string(profile.CostTier)

	if profile.SupportsVision {
		capabilities = append(capabilities, "vision")
	}
	if profile.SupportsToolCalls {
		capabilities = append(capabilities, "tools")
	}
	if profile.SupportsReasoning {
		capabilities = append(capabilities, "reasoning")
	}
	if profile.SupportsLongCtx {
		capabilities = append(capabilities, "long-context")
	}

	return health, quality, cost, capabilities, true
}

// tagsEqual 比较新旧标签是否完全一致。
func tagsEqual(a, b logicalChannelTags) bool {
	if a.health != b.health || a.quality != b.quality || a.cost != b.cost {
		return false
	}
	if len(a.capabilities) != len(b.capabilities) {
		return false
	}
	for i := range a.capabilities {
		if a.capabilities[i] != b.capabilities[i] {
			return false
		}
	}
	return true
}

// tagsFromLogicalChannel 从 config.LogicalChannel 读取当前标签。
func tagsFromLogicalChannel(lc *config.LogicalChannel) logicalChannelTags {
	if lc == nil {
		return logicalChannelTags{}
	}
	return logicalChannelTags{
		health:       lc.HealthTag,
		quality:      lc.QualityTag,
		cost:         lc.CostTag,
		capabilities: copyStringSlice(lc.CapabilityTags),
	}
}

func copyStringSlice(in []string) []string {
	if in == nil {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

// deriveAllLogicalChannelTags 遍历当前配置的全部 LogicalChannel，
// 仅当任一 channel 的标签相对缓存/当前值发生变化时才触发 ConfigManager.RebuildLogicalChannelsAndPublish。
// 该函数应在画像变更事件处理后调用，确保标签与画像最终一致。
func (m *Manager) deriveAllLogicalChannelTags() {
	if m == nil || m.cfgManager == nil || m.store == nil {
		return
	}
	cfg := m.cfgManager.GetConfig()
	if len(cfg.LogicalChannels) == 0 {
		return
	}

	state := m.logicalChannelTagState()
	changed := false
	for i := range cfg.LogicalChannels {
		lc := &cfg.LogicalChannels[i]
		uids := lc.SiblingChannelUIDs()
		if len(uids) == 0 {
			continue
		}
		health, quality, cost, caps, ok := deriveLogicalChannelTags(m, state, uids)
		if !ok {
			// 无画像时若之前缓存非空，说明画像已被清空，应触发重建把标签置空。
			if prev, has := state.get(lc.LogicalChannelUID); has && (prev.health != "" || prev.quality != "" || prev.cost != "" || len(prev.capabilities) > 0) {
				changed = true
			}
			continue
		}
		newTags := logicalChannelTags{health: health, quality: quality, cost: cost, capabilities: caps}
		oldTags := tagsFromLogicalChannel(lc)
		if !tagsEqual(newTags, oldTags) {
			changed = true
		}
		// 始终更新缓存为最新派生结果，便于后续对比。
		state.set(lc.LogicalChannelUID, newTags)
	}

	if changed {
		m.cfgManager.RebuildLogicalChannelsAndPublish()
	}
}

// logicalChannelTagState 返回 Manager 持有的标签缓存；延迟初始化。
func (m *Manager) logicalChannelTagState() *logicalChannelTagState {
	if m.tagState != nil {
		return m.tagState
	}
	return newLogicalChannelTagState()
}

// initLogicalChannelTagState 应在 Manager 构造后调用，把缓存挂到 Manager 上。
func (m *Manager) initLogicalChannelTagState() {
	if m.tagState == nil {
		m.tagState = newLogicalChannelTagState()
	}
}
