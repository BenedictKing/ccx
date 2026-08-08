package autopilot

import (
	"log"
	"sync"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/ratelimit"
)

// RateLimitApplier 将 RateLimitDiscoverer 的高置信建议应用到运行态 limiter。
//
// 门控（设计 §7）：RateLimitDiscovery.Enabled && AutopilotRouting.IsAutopilotActive() && !KillSwitch。
// shadow/off/kill switch 时只学习与展示画像，不写运行态 limiter；并按 lastApplied
// 中保存的 limiterKey 全量清理已注入的 discovered RPM/MaxConcurrent。
//
// 显式配置永远优先：RPM 和 MaxConcurrent 分维度独立判断。
//
// 并发安全：mapping 更新、Apply、Clear 由内部 mutex 串行保护，可在 worker
// 节奏与请求 goroutine 触发的通知之间安全调用。
type RateLimitApplier struct {
	discoverer   *RateLimitDiscoverer
	limiterMgr   *ratelimit.Manager
	configGetter func() config.AutopilotRoutingConfig

	mu sync.Mutex

	// mappings 维护 endpointUID → limiter binding。由集成层在 worker 轮询时
	// 通过 SetEndpointMappings 替换整个映射表。
	mappings map[string]EndpointLimiterMapping

	// lastApplied 快照上次应用的发现限额，按 limiterKey 去重。
	// 多 endpoint 映射到同一 limiter 时只保存一条，保证清理时按 limiter 反查。
	lastApplied map[string]appliedLimits

	quietLogs bool
}

// NewRateLimitApplier 创建 RateLimitApplier。
// discoverer / limiterMgr / configGetter 任一为 nil 时 Apply 为 no-op。
func NewRateLimitApplier(
	discoverer *RateLimitDiscoverer,
	limiterMgr *ratelimit.Manager,
	configGetter func() config.AutopilotRoutingConfig,
	quietLogs bool,
) *RateLimitApplier {
	return &RateLimitApplier{
		discoverer:   discoverer,
		limiterMgr:   limiterMgr,
		configGetter: configGetter,
		mappings:     make(map[string]EndpointLimiterMapping),
		lastApplied:  make(map[string]appliedLimits),
		quietLogs:    quietLogs,
	}
}

// EndpointLimiterMapping 表示一个 endpoint 到 limiter 的绑定关系。
type EndpointLimiterMapping struct {
	// EndpointUID autopilot 的 endpoint 唯一标识。
	EndpointUID string
	// LimiterKey ratelimit.Manager 中的 limiter key
	//（apiType:channelIndex 或 apiType:channelIndex:scope）。
	LimiterKey string
	// LimiterConfig scoped limiter 不存在时按此配置创建。
	LimiterConfig ratelimit.Config
	// ExplicitRPM 标记该 endpoint 的 RPM 是否来自用户显式配置。
	// true 时跳过发现 RPM 注入（显式配置永远优先）。
	ExplicitRPM bool
	// ExplicitMaxConcurrent 标记最大并发是否来自用户显式配置。
	ExplicitMaxConcurrent bool
}

type appliedLimits struct {
	rpm           int
	maxConcurrent int
}

// SetEndpointMappings 批量设置 endpoint → limiter 映射（并发安全）。
// 每次 worker 轮询时由集成层调用，替换整个映射表。
func (a *RateLimitApplier) SetEndpointMappings(mappings []EndpointLimiterMapping) {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.mappings = make(map[string]EndpointLimiterMapping, len(mappings))
	for _, m := range mappings {
		if m.EndpointUID != "" && m.LimiterKey != "" {
			a.mappings[m.EndpointUID] = m
		}
	}
}

// Apply 执行一次发现限额的应用周期（并发安全）。
//
// 行为：
//  1. 任一依赖为 nil → no-op
//  2. 门控不满足（Enabled=false / 非 active / KillSwitch）→ 全量清理
//  3. 构造 desiredByLimiterKey：RPM/MaxConcurrent 分别过滤低置信和显式配置；
//     多 endpoint 映射到同一 limiter 时各维度取最小值
//  4. scoped limiter 不存在时用 mapping 携带的配置 GetOrCreateScoped 创建
//  5. 将发现值写入 limiter，并清理不再建议的单个维度
//  6. 本轮不再需要的旧 limiterKey 必须 ClearDiscoveredRPM 并从快照移除
func (a *RateLimitApplier) Apply() {
	if a == nil || a.discoverer == nil || a.limiterMgr == nil || a.configGetter == nil {
		return
	}

	a.mu.Lock()
	defer a.mu.Unlock()

	cfg := a.configGetter()
	rlCfg := cfg.RateLimitDiscovery
	active := rlCfg.Enabled && cfg.IsAutopilotActive() && !cfg.KillSwitch

	if !active {
		a.clearAllLocked()
		return
	}

	confidenceThreshold := rlCfg.ConfidenceThreshold
	if confidenceThreshold <= 0 {
		confidenceThreshold = 0.7 // 安全回退
	}

	suggestions := a.discoverer.AllSuggestedLimits()

	// 构造 desiredByLimiterKey：多 endpoint → 同 limiter 取确定性最小 RPM
	type desired struct {
		rpm           int
		maxConcurrent int
		cfg           ratelimit.Config
	}
	desiredByKey := make(map[string]desired, len(suggestions))
	for endpointUID, s := range suggestions {
		m, ok := a.mappings[endpointUID]
		if !ok || m.LimiterKey == "" {
			continue
		}
		d, exists := desiredByKey[m.LimiterKey]
		if !exists {
			d.cfg = m.LimiterConfig
		}
		if !m.ExplicitRPM && s.RPM > 0 && s.Confidence >= confidenceThreshold && (d.rpm == 0 || s.RPM < d.rpm) {
			d.rpm = s.RPM
		}
		if !m.ExplicitMaxConcurrent && s.MaxConcurrent > 0 && s.ConcurrentConfidence >= confidenceThreshold &&
			(d.maxConcurrent == 0 || s.MaxConcurrent < d.maxConcurrent) {
			d.maxConcurrent = s.MaxConcurrent
		}
		if d.rpm > 0 || d.maxConcurrent > 0 {
			desiredByKey[m.LimiterKey] = d
		}
	}

	// 写 limiter + 更新 lastApplied
	applied := 0
	newApplied := make(map[string]appliedLimits, len(desiredByKey))
	for limiterKey, d := range desiredByKey {
		apiType, channelIndex, scope := parseLimiterKey(limiterKey)
		if apiType == "" {
			continue
		}
		var l *ratelimit.ChannelLimiter
		if scope != "" {
			l = a.limiterMgr.GetOrCreateScoped(apiType, channelIndex, scope, d.cfg)
		} else {
			l = a.limiterMgr.GetOrCreate(apiType, channelIndex, d.cfg)
		}
		if l == nil {
			continue
		}
		changed := false
		if d.rpm > 0 {
			changed = l.SetDiscoveredRPM(d.rpm)
		} else if l.HasDiscoveredRPM() {
			l.ClearDiscoveredRPM()
			changed = true
		}
		if d.maxConcurrent > 0 {
			if l.SetDiscoveredMaxConcurrent(d.maxConcurrent) {
				changed = true
			}
		} else if l.HasDiscoveredMaxConcurrent() {
			l.ClearDiscoveredMaxConcurrent()
			changed = true
		}
		if changed {
			applied++
		}
		state := appliedLimits{}
		if d.rpm > 0 && l.HasDiscoveredRPM() {
			state.rpm = d.rpm
		}
		if d.maxConcurrent > 0 && l.HasDiscoveredMaxConcurrent() {
			state.maxConcurrent = d.maxConcurrent
		}
		if state.rpm > 0 || state.maxConcurrent > 0 {
			newApplied[limiterKey] = state
		}
	}

	// 清理本轮不再需要的旧 limiterKey
	for limiterKey := range a.lastApplied {
		if _, ok := newApplied[limiterKey]; ok {
			continue
		}
		a.clearLimiterLocked(limiterKey)
	}

	a.lastApplied = newApplied

	if applied > 0 && !a.quietLogs {
		log.Printf("[RateLimitApplier-Apply] 应用发现限额: %d 个 limiter", applied)
	}
}

// Clear 主动清除所有已注入的发现 RPM（并发安全）。
// 供 kill switch、配置变更或模式切换时调用，确保不遗留运行态覆盖。
func (a *RateLimitApplier) Clear() {
	if a == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.clearAllLocked()
}

// clearLimiterLocked 清除单个 limiterKey 的发现 RPM（需持有 a.mu）。
func (a *RateLimitApplier) clearLimiterLocked(limiterKey string) {
	apiType, channelIndex, scope := parseLimiterKey(limiterKey)
	if apiType == "" {
		return
	}
	var l *ratelimit.ChannelLimiter
	if scope != "" {
		l = a.limiterMgr.GetScoped(apiType, channelIndex, scope)
	} else {
		l = a.limiterMgr.Get(apiType, channelIndex)
	}
	if l != nil {
		l.ClearDiscoveredRPM()
		l.ClearDiscoveredMaxConcurrent()
	}
}

// clearAllLocked 全量清除已注入的发现 RPM（需持有 a.mu）。
// 按 lastApplied 中保存的 limiterKey 反查清理，不依赖当前 endpoint mapping，
// 因此 mapping 被替换为空或 endpoint 消失时仍能清理旧值。
func (a *RateLimitApplier) clearAllLocked() {
	if len(a.lastApplied) == 0 {
		return
	}
	cleared := 0
	for limiterKey := range a.lastApplied {
		a.clearLimiterLocked(limiterKey)
		cleared++
	}
	a.lastApplied = make(map[string]appliedLimits)
	if cleared > 0 && !a.quietLogs {
		log.Printf("[RateLimitApplier-Clear] 已清除 %d 个发现限额（开关关闭/kill switch/非 active）", cleared)
	}
}

// parseLimiterKey 解析 ratelimit.Manager 的 key 格式。
// 支持 "apiType:channelIndex" 和 "apiType:channelIndex:scope" 两种格式。
func parseLimiterKey(key string) (apiType string, channelIndex int, scope string) {
	colon1 := -1
	colon2 := -1
	for i := 0; i < len(key); i++ {
		if key[i] == ':' {
			if colon1 == -1 {
				colon1 = i
			} else {
				colon2 = i
				break
			}
		}
	}
	if colon1 == -1 {
		return key, 0, ""
	}

	apiType = key[:colon1]
	idx := 0
	for i := colon1 + 1; i < len(key) && key[i] >= '0' && key[i] <= '9'; i++ {
		idx = idx*10 + int(key[i]-'0')
	}

	if colon2 > colon1 {
		scope = key[colon2+1:]
	}

	return apiType, idx, scope
}
