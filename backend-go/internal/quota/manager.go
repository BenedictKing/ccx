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

// GetChannelState 获取指定渠道的配额状态快照（深拷贝）。
// 如果不存在，返回 unknown 状态（fail-open）。
// 返回值与 Manager 内部状态完全隔离：修改快照不影响内部数据。
func (m *Manager) GetChannelState(channelUID string) *ChannelState {
	if m == nil || channelUID == "" {
		return NewChannelState("")
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	state, ok := m.states[channelUID]
	if !ok {
		return NewChannelState(channelUID)
	}
	return state.DeepCopy()
}

// GetChannelHeadroom 获取指定渠道的综合 headroom（0.0-1.0）。
// 无数据时返回 0.5（中性分）。
// 这是 SmartRouter 评分因子的主要消费点。
func (m *Manager) GetChannelHeadroom(channelUID string) float64 {
	if m == nil || channelUID == "" {
		return 0.5
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	state, ok := m.states[channelUID]
	if !ok {
		return 0.5
	}
	return state.OverallHeadroom()
}

// GetChannelTruth 获取指定渠道的真相等级。
func (m *Manager) GetChannelTruth(channelUID string) TruthLevel {
	if m == nil || channelUID == "" {
		return TruthUnknown
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	state, ok := m.states[channelUID]
	if !ok {
		return TruthUnknown
	}
	return state.Status
}

// UpdateChannelProviderAPI 更新渠道的 provider_api 级配额数据。
// 用于 SubscriptionBalanceFetcher 等官方 API 返回的数据。
// 状态修改在 Manager 写锁内完成；查询失败不以空值清除最后一次成功数据，
// 只更新 Error/FetchedAtMs。
func (m *Manager) UpdateChannelProviderAPI(channelUID, accountUID string, values []Value, fetchErr error) {
	if m == nil || channelUID == "" {
		return
	}

	// 复制并标记来源，避免调用方后续修改切片影响 Manager 内部状态。
	apiValues := make([]Value, len(values))
	copy(apiValues, values)
	for i := range apiValues {
		apiValues[i].Source = SourceProviderAPI
	}

	nowMs := time.Now().UnixMilli()
	m.mu.Lock()
	state, ok := m.states[channelUID]
	if !ok {
		state = NewChannelState(channelUID)
		m.states[channelUID] = state
	}
	state.Supported = true
	state.AccountUID = accountUID
	state.FetchedAtMs = nowMs
	if fetchErr != nil {
		state.Error = fetchErr.Error()
	} else {
		state.Error = ""
	}
	state.MergeValues(apiValues)
	m.mu.Unlock()

	// 饱和桶有自己的锁，更新放在 Manager 锁外避免扩大临界区
	//（锁顺序恒为 Manager.mu → buckets.mu，无反转风险）。
	if accountUID != "" {
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

	nowMs := time.Now().UnixMilli()
	m.mu.Lock()
	state, ok := m.states[channelUID]
	if !ok {
		state = NewChannelState(channelUID)
		m.states[channelUID] = state
	}
	state.FetchedAtMs = nowMs
	state.AccountUID = accountUID
	state.MergeValues(values)
	m.mu.Unlock()

	if accountUID != "" {
		m.buckets.UpdateFromValues(accountUID, values, nowMs)
	}
}

// serviceTypeQuotaProviders 将渠道协议族（handlers/common.ChannelAPIType 取值）
// 映射到已确认的响应头 provider 映射。未列出的协议族无显式确认的头映射，
// 跳过解析（fail-open，不猜测头名归属）。
var serviceTypeQuotaProviders = map[string]string{
	"Messages":  "anthropic",
	"Chat":      "openai",
	"Responses": "openai",
}

// UpdateFromUpstreamSignal 是 response_headers 级数据的生产接线入口，
// 供 ratelimit 上游信号回调（与限速发现器共享的同一挂点）调用：
// 由 serviceType 推导 provider 头映射后更新渠道配额状态。
// accountUID 传 endpointUID（channelUID+baseURL+keyHash 摘要），饱和桶按 endpoint 粒度聚合。
func (m *Manager) UpdateFromUpstreamSignal(channelUID, accountUID, serviceType string, headers http.Header) {
	if m == nil || channelUID == "" || headers == nil {
		return
	}
	provider, ok := serviceTypeQuotaProviders[serviceType]
	if !ok {
		return
	}
	m.UpdateChannelResponseHeaders(channelUID, accountUID, provider, headers)
}

// UpdateChannelConfigured 更新渠道的 configured 级配额数据。
// 用于静态配置的配额（如 newapi multiplier、手动设置的额度）。
func (m *Manager) UpdateChannelConfigured(channelUID, accountUID string, values []Value) {
	if m == nil || channelUID == "" {
		return
	}

	// 将来源标记为 configured（复制，隔离调用方切片）
	cfgValues := make([]Value, len(values))
	copy(cfgValues, values)
	for i := range cfgValues {
		cfgValues[i].Source = SourceConfigured
	}

	m.mu.Lock()
	state, ok := m.states[channelUID]
	if !ok {
		state = NewChannelState(channelUID)
		m.states[channelUID] = state
	}
	state.MergeValues(cfgValues)
	m.mu.Unlock()

	if accountUID != "" {
		m.buckets.UpdateFromValues(accountUID, cfgValues, time.Now().UnixMilli())
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
// 在 Manager 读锁内读取一致快照；饱和桶有自己的锁，锁顺序恒为
// Manager.mu → buckets.mu（写侧先释放 Manager.mu 再更新桶）。
func (m *Manager) IsChannelSaturated(channelUID string, nowMs int64) bool {
	if m == nil {
		return false
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	state, ok := m.states[channelUID]
	if !ok {
		return false
	}
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

	m.mu.RLock()
	defer m.mu.RUnlock()

	state, ok := m.states[channelUID]
	if !ok {
		return -1
	}
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
