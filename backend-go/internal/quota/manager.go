package quota

import (
	"net/http"
	"sync"
	"time"
)

// ── 配额管理器 ──
//
// Manager 是配额系统的对外入口，协调：
//   - ChannelState 存储（按渠道维度）
//   - BucketManager 饱和桶
//   - 响应头解析
//
// Fail-open 原则：任何内部错误都不影响现有调度路径。

// Manager 是配额系统的线程安全管理器。
type Manager struct {
	mu      sync.RWMutex
	states  map[string]*ChannelState // channelUID → state
	buckets *BucketManager
}

// NewManager 创建新的配额管理器。
func NewManager() *Manager {
	return &Manager{
		states:  make(map[string]*ChannelState),
		buckets: NewBucketManager(),
	}
}

// GetChannelState 获取指定渠道的配额状态。
// 如果不存在，返回 unknown 状态（fail-open）。
func (m *Manager) GetChannelState(channelUID string) *ChannelState {
	if m == nil || channelUID == "" {
		return NewChannelState("")
	}

	m.mu.RLock()
	state, ok := m.states[channelUID]
	m.mu.RUnlock()

	if !ok {
		return NewChannelState(channelUID)
	}
	return state
}

// GetChannelHeadroom 获取指定渠道的综合 headroom（0.0-1.0）。
// 无数据时返回 0.5（中性分）。
// 这是 SmartRouter 评分因子的主要消费点。
func (m *Manager) GetChannelHeadroom(channelUID string) float64 {
	state := m.GetChannelState(channelUID)
	return state.OverallHeadroom()
}

// GetChannelTruth 获取指定渠道的真相等级。
func (m *Manager) GetChannelTruth(channelUID string) TruthLevel {
	state := m.GetChannelState(channelUID)
	return state.Status
}

// UpdateChannelProviderAPI 更新渠道的 provider_api 级配额数据。
// 用于 SubscriptionBalanceFetcher 等官方 API 返回的数据。
func (m *Manager) UpdateChannelProviderAPI(channelUID, accountUID string, values []Value, fetchErr error) {
	if m == nil || channelUID == "" {
		return
	}

	m.mu.Lock()
	state, ok := m.states[channelUID]
	if !ok {
		state = NewChannelState(channelUID)
		m.states[channelUID] = state
	}
	m.mu.Unlock()

	state.Supported = true
	state.AccountUID = accountUID
	state.FetchedAtMs = time.Now().UnixMilli()
	if fetchErr != nil {
		state.Error = fetchErr.Error()
	} else {
		state.Error = ""
	}

	// 将来源标记为 provider_api
	apiValues := make([]Value, len(values))
	copy(apiValues, values)
	for i := range apiValues {
		apiValues[i].Source = SourceProviderAPI
	}

	state.MergeValues(apiValues)

	// 更新饱和桶
	if accountUID != "" {
		nowMs := time.Now().UnixMilli()
		m.buckets.UpdateFromValues(accountUID, apiValues, nowMs)
	}
}

// UpdateChannelResponseHeaders 从响应头更新渠道配额数据（response_headers 级）。
// 挂点：与 ratelimit/rate_limit_applier.go 同一位置（上游响应返回后）。
func (m *Manager) UpdateChannelResponseHeaders(channelUID, accountUID, provider string, headers http.Header) {
	if m == nil || channelUID == "" || provider == "" {
		return
	}

	values := ParseResponseHeaders(provider, headers)
	if len(values) == 0 {
		return
	}

	m.mu.Lock()
	state, ok := m.states[channelUID]
	if !ok {
		state = NewChannelState(channelUID)
		m.states[channelUID] = state
	}
	m.mu.Unlock()

	state.FetchedAtMs = time.Now().UnixMilli()
	state.AccountUID = accountUID
	state.MergeValues(values)

	// 更新饱和桶
	if accountUID != "" {
		nowMs := time.Now().UnixMilli()
		m.buckets.UpdateFromValues(accountUID, values, nowMs)
	}
}

// UpdateChannelConfigured 更新渠道的 configured 级配额数据。
// 用于静态配置的配额（如 newapi multiplier、手动设置的额度）。
func (m *Manager) UpdateChannelConfigured(channelUID, accountUID string, values []Value) {
	if m == nil || channelUID == "" {
		return
	}

	m.mu.Lock()
	state, ok := m.states[channelUID]
	if !ok {
		state = NewChannelState(channelUID)
		m.states[channelUID] = state
	}
	m.mu.Unlock()

	// 将来源标记为 configured
	cfgValues := make([]Value, len(values))
	copy(cfgValues, values)
	for i := range cfgValues {
		cfgValues[i].Source = SourceConfigured
	}

	state.MergeValues(cfgValues)

	if accountUID != "" {
		nowMs := time.Now().UnixMilli()
		m.buckets.UpdateFromValues(accountUID, cfgValues, nowMs)
	}
}

// Buckets 返回饱和桶管理器（供 scheduler 使用）。
func (m *Manager) Buckets() *BucketManager {
	if m == nil {
		return nil
	}
	return m.buckets
}

// IsChannelSaturated 判断渠道是否处于配额紧张状态。
// 供 scheduler 底层选择时使用（沉底排序用，不剔除）。
// approaching_limit 和 exhausted 都返回 true；桶的懒重置只对 exhausted 状态生效。
func (m *Manager) IsChannelSaturated(channelUID string, nowMs int64) bool {
	if m == nil {
		return false
	}

	state := m.GetChannelState(channelUID)
	switch state.Status {
	case TruthExhausted:
		// exhausted 状态需要通过桶的懒重置来判断是否已恢复
		accountUID := state.AccountUID
		if accountUID == "" {
			return true
		}
		for dim := range state.Values {
			if m.buckets.IsSaturated(accountUID, string(dim), nowMs) {
				return true
			}
		}
		// 如果桶都已重置（窗口过了），返回 false
		return false
	case TruthApproachingLimit:
		// approaching_limit 直接返回 true，不依赖桶（桶阈值是 100%）
		return true
	default:
		return false
	}
}

// ChannelSaturationRank 返回渠道的饱和排序权重。
// 值越大表示越饱和，沉底排序时用。
// 返回 0 表示正常，1 表示接近上限，2 表示已耗尽，-1 表示无数据。
func (m *Manager) ChannelSaturationRank(channelUID string, nowMs int64) int {
	if m == nil {
		return -1
	}

	state := m.GetChannelState(channelUID)
	switch state.Status {
	case TruthExhausted:
		return 2
	case TruthApproachingLimit:
		return 1
	case TruthHealthy:
		return 0
	default:
		// unknown / unavailable → -1（不参与饱和排序，保持原位置）
		return -1
	}
}

// ── 测试辅助 ──

// Reset 清空所有状态（测试用，生产代码禁止调用）。
func (m *Manager) Reset() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states = make(map[string]*ChannelState)
	m.buckets = NewBucketManager()
}
