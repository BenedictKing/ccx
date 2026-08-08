package autopilot

import (
	"log"
	"math"
	"sync"
	"time"
)

// ── 信号来源类型 ──

// RateLimitSignalSource 表示限速信号的来源类型。
type RateLimitSignalSource string

const (
	SignalSourceHeader  RateLimitSignalSource = "header"  // 从上游响应头解析
	SignalSource429     RateLimitSignalSource = "429"     // 429 响应反推
	SignalSourceSuccess RateLimitSignalSource = "success" // 成功响应（用于 AIMD 上调）
)

const (
	latencyWarmupSamples              = 5
	latencySlowStreakThreshold        = 3
	latencyHealthyStreakTarget        = 10
	latencySlowRatio                  = 3.0
	latencySlowFloorMs          int64 = 15_000
	latencyAbsoluteSlowMs       int64 = 60_000
	latencyBaselineAlpha              = 0.10
	latencyCongestionConfidence       = 0.70
)

// ── 429 信号原因 ──

// RateLimitSignalReason 区分普通 429 与已确认账号级限流。
// 精确原因可提高 AIMD 置信度；普通无 header 429 不能被一并提升为高置信信号。
type RateLimitSignalReason string

const (
	RateLimitReasonUnknown                  RateLimitSignalReason = "unknown"
	RateLimitReasonAccountRateLimitExceeded RateLimitSignalReason = "account_rate_limit_exceeded"
)

// ── 输入信号 ──

// RateLimitSignal 是一次上游响应携带的限速相关信号。
type RateLimitSignal struct {
	// Source 信号来源。
	Source RateLimitSignalSource `json:"source"`

	// Reason 429 信号的细分原因（仅 Source=429 时有意义）。
	Reason RateLimitSignalReason `json:"reason,omitempty"`

	// ── header 显式值 ──
	// Limit 为 x-ratelimit-limit-requests 或 anthropic-ratelimit-limit-requests 解析值。
	// 表示窗口内请求上限（非 RPM，需结合 Window 换算）。
	Limit int `json:"limit,omitempty"`
	// Remaining 为 x-ratelimit-remaining-requests 或 anthropic-ratelimit-requests-remaining 解析值。
	Remaining int `json:"remaining,omitempty"`
	// ResetSeconds 为 x-ratelimit-reset-requests 解析的重置窗口秒数。
	ResetSeconds float64 `json:"resetSeconds,omitempty"`
	// WindowSeconds 为 header 中指示的限速窗口秒数（如 60s、3600s）。
	// 若 ResetSeconds 已知，可从 ResetSeconds 推算；若 header 明确给出窗口大小则直接填入。
	WindowSeconds float64 `json:"windowSeconds,omitempty"`

	// ── 429 信号 ──
	// HasRetryAfter 是否携带 Retry-After header。
	HasRetryAfter bool `json:"hasRetryAfter,omitempty"`
	// RetryAfterSeconds Retry-After header 解析的秒数。
	RetryAfterSeconds float64 `json:"retryAfterSeconds,omitempty"`

	// ── 成功信号 ──
	// IsStreaming 是否为流式请求（影响并发学习）。
	IsStreaming bool `json:"isStreaming,omitempty"`
	// LatencyMs 响应延迟毫秒（用于判断是否"低延迟稳定"）。
	LatencyMs int64 `json:"latencyMs,omitempty"`

	// Timestamp 信号产生的时间戳。
	Timestamp time.Time `json:"timestamp"`
}

// ── 学习状态 ──

// endpointLearnState 持有单个 endpoint 的限速学习状态。
type endpointLearnState struct {
	// estimatedRPM 当前估算的 RPM。
	EstimatedRPM int `json:"estimatedRpm"`
	// estimatedTPM 当前估算的 TPM（token 每分钟，Phase 1 暂不精细推导）。
	EstimatedTPM int `json:"estimatedTpm,omitempty"`
	// estimatedRPD 当前估算的 RPD。
	EstimatedRPD int `json:"estimatedRpd,omitempty"`
	// windowSeconds 当前学习到的限速窗口秒数。
	WindowSeconds int `json:"windowSeconds"`
	// maxConcurrent 估算的最大并发。
	MaxConcurrent int `json:"maxConcurrent,omitempty"`
	// concurrentConfidence 并发建议的独立置信度，避免与 RPM header 置信度混用。
	ConcurrentConfidence float64 `json:"concurrentConfidence,omitempty"`
	// latencyBaselineMs 流式请求响应头 TTFB 的平滑基线。
	LatencyBaselineMs float64 `json:"latencyBaselineMs,omitempty"`
	// latencySampleCount 已纳入并发学习的流式成功样本数。
	LatencySampleCount int `json:"latencySampleCount,omitempty"`
	// consecutiveSlowLatency 连续显著慢于基线的样本数。
	ConsecutiveSlowLatency int `json:"consecutiveSlowLatency,omitempty"`
	// consecutiveHealthyLatency 并发受限后连续健康样本数。
	ConsecutiveHealthyLatency int `json:"consecutiveHealthyLatency,omitempty"`
	// lastConcurrencyAdjustmentAt 最近一次并发 AIMD 调整时间。
	LastConcurrencyAdjustmentAt *time.Time `json:"lastConcurrencyAdjustmentAt,omitempty"`
	// lastConcurrencyReason 最近一次并发调整的原因（用于去抖动）。
	lastConcurrencyReason string `json:"-"`
	// source 最近一次更新信号的来源。
	Source RateLimitSource `json:"source"`
	// confidence 当前置信度，0.0-1.0。
	Confidence float64 `json:"confidence"`
	// last429At 最近一次 429 信号的时间。
	Last429At *time.Time `json:"last429At,omitempty"`
	// lastSuccessAt 最近一次成功信号的时间。
	LastSuccessAt *time.Time `json:"lastSuccessAt,omitempty"`
	// updatedAt 最近一次状态更新时间。
	UpdatedAt time.Time `json:"updatedAt"`
	// observeCount 累计观测次数。
	ObserveCount int `json:"observeCount"`
	// headerSuccessCount header 信号连续成功解析计数（用于提升 confidence）。
	HeaderSuccessCount int `json:"headerSuccessCount"`
	// lastAIMDIncreaseAt 最近一次 AIMD 上调时间。
	LastAIMDIncreaseAt *time.Time `json:"lastAIMDIncreaseAt,omitempty"`
	// no429Since 最近一次 429 后连续无 429 的起始时间。
	No429Since *time.Time `json:"no429Since,omitempty"`
	// consecutiveSuccessesSince429 自最近 429 后连续成功次数。
	ConsecutiveSuccessesSince429 int `json:"consecutiveSuccessesSince429,omitempty"`
}

// ── 配置 ──

// RateLimitDiscovererConfig 可调参数。
type RateLimitDiscovererConfig struct {
	// PassiveAimdEnabled 控制 429 AIMD 降速与成功上调是否执行。
	// false 时仍展示显式 header 信号，但不执行 429 降速和成功上调。
	PassiveAimdEnabled bool `json:"passiveAimdEnabled"`
	// MinRPM 估算下限，防止降为 0 后永久不可用。默认 1。
	MinRPM int `json:"minRpm"`
	// MaxAutoRPM 无明确 header 时的自动估算上限。默认 120。
	MaxAutoRPM int `json:"maxAutoRpm"`
	// MaxAutoConcurrent 自动并发建议上限。默认 8。
	MaxAutoConcurrent int `json:"maxAutoConcurrent"`
	// ConfidenceThreshold 建议被采纳的最低置信度阈值。默认 0.3。
	ConfidenceThreshold float64 `json:"confidenceThreshold"`
	// AIMDIncreaseInterval AIMD 上调最短间隔。默认 10 分钟。
	AIMDIncreaseInterval time.Duration `json:"aimdIncreaseInterval"`
	// AIMDIncreasePercent AIMD 上调幅度百分比。默认 10。
	AIMDIncreasePercent float64 `json:"aimdIncreasePercent"`
	// AIMDNo429Grace AIMD 上调要求最近无 429 的窗口。默认 15 分钟。
	AIMDNo429Grace time.Duration `json:"aimdNo429Grace"`
	// HeaderConfidenceMax header 来源置信度上限。默认 0.9。
	HeaderConfidenceMax float64 `json:"headerConfidenceMax"`
	// RemainingConfidenceMax remaining/reset 推导置信度上限。默认 0.75。
	RemainingConfidenceMax float64 `json:"remainingConfidenceMax"`
	// QuietLogs 是否静默日志。
	QuietLogs bool `json:"quietLogs"`
}

// defaultDiscovererConfig 返回默认配置。
func defaultDiscovererConfig() RateLimitDiscovererConfig {
	return RateLimitDiscovererConfig{
		PassiveAimdEnabled:     true,
		MinRPM:                 1,
		MaxAutoRPM:             120,
		MaxAutoConcurrent:      8,
		ConfidenceThreshold:    0.3,
		AIMDIncreaseInterval:   10 * time.Minute,
		AIMDIncreasePercent:    10,
		AIMDNo429Grace:         15 * time.Minute,
		HeaderConfidenceMax:    0.9,
		RemainingConfidenceMax: 0.75,
		QuietLogs:              false,
	}
}

// ── Discoverer 主体 ──

// RateLimitDiscoverer 在 shadow/read-only 模式下推导 endpoint 的限速建议。
// Phase 1: 只输出建议（SuggestedLimit），不写任何 limiter。
// 并发安全，状态可 JSON 序列化。
type RateLimitDiscoverer struct {
	mu      sync.RWMutex
	states  map[string]*endpointLearnState // key: endpointUID
	cfg     RateLimitDiscovererConfig
	nowFunc func() time.Time // 可注入，测试用
}

// NewRateLimitDiscoverer 创建 RateLimitDiscoverer。
func NewRateLimitDiscoverer(cfg RateLimitDiscovererConfig) *RateLimitDiscoverer {
	if cfg.MinRPM <= 0 {
		cfg.MinRPM = defaultDiscovererConfig().MinRPM
	}
	if cfg.MaxAutoRPM <= 0 {
		cfg.MaxAutoRPM = defaultDiscovererConfig().MaxAutoRPM
	}
	if cfg.MaxAutoConcurrent <= 0 {
		cfg.MaxAutoConcurrent = defaultDiscovererConfig().MaxAutoConcurrent
	}
	if cfg.ConfidenceThreshold <= 0 {
		cfg.ConfidenceThreshold = defaultDiscovererConfig().ConfidenceThreshold
	}
	if cfg.AIMDIncreaseInterval <= 0 {
		cfg.AIMDIncreaseInterval = defaultDiscovererConfig().AIMDIncreaseInterval
	}
	if cfg.AIMDIncreasePercent <= 0 {
		cfg.AIMDIncreasePercent = defaultDiscovererConfig().AIMDIncreasePercent
	}
	if cfg.AIMDNo429Grace <= 0 {
		cfg.AIMDNo429Grace = defaultDiscovererConfig().AIMDNo429Grace
	}
	if cfg.HeaderConfidenceMax <= 0 {
		cfg.HeaderConfidenceMax = defaultDiscovererConfig().HeaderConfidenceMax
	}
	if cfg.RemainingConfidenceMax <= 0 {
		cfg.RemainingConfidenceMax = defaultDiscovererConfig().RemainingConfidenceMax
	}
	return &RateLimitDiscoverer{
		states:  make(map[string]*endpointLearnState),
		cfg:     cfg,
		nowFunc: time.Now,
	}
}

// ── SuggestedLimit 输出结构 ──

// SuggestedLimitResult 限速建议结果。
type SuggestedLimitResult struct {
	RPM                  int             `json:"rpm"`
	TPM                  int             `json:"tpm,omitempty"`
	RPD                  int             `json:"rpd,omitempty"`
	MaxConcurrent        int             `json:"maxConcurrent,omitempty"`
	Confidence           float64         `json:"confidence"`
	ConcurrentConfidence float64         `json:"concurrentConfidence,omitempty"`
	Source               RateLimitSource `json:"source"`
}

// ── 公开方法 ──

// Observe 累积一个限速信号。并发安全。返回值表示运行态建议是否发生变化。
// 推导规则按设计 §4.5.3：header 显式值优先 → 429 反推 → 被动 AIMD 收敛。
func (d *RateLimitDiscoverer) Observe(endpointUID string, sig RateLimitSignal) bool {
	if endpointUID == "" {
		return false
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	state, ok := d.states[endpointUID]
	if !ok {
		state = &endpointLearnState{
			WindowSeconds: 60, // 默认 1 分钟窗口
			UpdatedAt:     d.nowFunc(),
		}
		d.states[endpointUID] = state
	}

	now := d.nowFunc()
	state.ObserveCount++
	state.UpdatedAt = now
	previous := suggestedLimitFromState(state)

	if (sig.Source == SignalSourceSuccess || sig.Source == SignalSourceHeader) && sig.IsStreaming && sig.LatencyMs > 0 {
		d.observeStreamingLatency(state, sig.LatencyMs, now)
	}

	switch sig.Source {
	case SignalSourceHeader:
		d.observeHeader(state, sig, now)
	case SignalSource429:
		d.observe429(state, sig, now)
	case SignalSourceSuccess:
		d.observeSuccess(state, sig, now)
	}

	current := suggestedLimitFromState(state)
	return previous.RPM != current.RPM ||
		previous.MaxConcurrent != current.MaxConcurrent ||
		previous.Confidence != current.Confidence ||
		previous.ConcurrentConfidence != current.ConcurrentConfidence ||
		previous.Source != current.Source
}

// SeedProbeEstimate 用添加渠道时的主动探测结果初始化 endpoint 限速画像。
// 未遇到 429 的 30 RPM 只表示探测过程安全，不足以直接驱动限速；遇到 429
// 后的降速结果置信度更高，但仍允许后续显式响应头覆盖。
func (d *RateLimitDiscoverer) SeedProbeEstimate(endpointUID string, rpm int, rateLimited bool) SuggestedLimitResult {
	if d == nil || endpointUID == "" || rpm <= 0 {
		return SuggestedLimitResult{Source: RateLimitSourceUnknown}
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	now := d.nowFunc()
	seedRPM := d.clampRPM(rpm)
	seedConfidence := 0.2
	if rateLimited {
		seedConfidence = 0.7
	}

	state, ok := d.states[endpointUID]
	if !ok {
		state = &endpointLearnState{WindowSeconds: 60}
		d.states[endpointUID] = state
	}
	// 已解析到明确上游响应头，或已有更高置信结论时，不用探测提示降级。
	if state.Source == RateLimitSourceHeader || state.Confidence > seedConfidence {
		return suggestedLimitFromState(state)
	}
	// 普通探测基线不覆盖已有估算；429 探测只允许收紧，不会放宽已有值。
	if state.EstimatedRPM > 0 {
		if !rateLimited || state.EstimatedRPM <= seedRPM {
			return suggestedLimitFromState(state)
		}
	}

	state.EstimatedRPM = seedRPM
	state.Source = RateLimitSourcePassiveAIMD
	state.Confidence = seedConfidence
	state.UpdatedAt = now
	state.ObserveCount++
	if rateLimited {
		state.Last429At = &now
		state.No429Since = nil
		state.ConsecutiveSuccessesSince429 = 0
	} else if state.No429Since == nil {
		state.No429Since = &now
	}
	return suggestedLimitFromState(state)
}

func suggestedLimitFromState(state *endpointLearnState) SuggestedLimitResult {
	if state == nil || state.ObserveCount == 0 {
		return SuggestedLimitResult{Source: RateLimitSourceUnknown}
	}
	return SuggestedLimitResult{
		RPM:                  state.EstimatedRPM,
		TPM:                  state.EstimatedTPM,
		RPD:                  state.EstimatedRPD,
		MaxConcurrent:        state.MaxConcurrent,
		Confidence:           state.Confidence,
		ConcurrentConfidence: state.ConcurrentConfidence,
		Source:               state.Source,
	}
}

// SuggestedLimit 返回 endpoint 的限速建议。并发安全。
// 返回 zero value（rpm=0, confidence=0, source=unknown）表示信号不足，无建议。
func (d *RateLimitDiscoverer) SuggestedLimit(endpointUID string) SuggestedLimitResult {
	d.mu.RLock()
	defer d.mu.RUnlock()

	state, ok := d.states[endpointUID]
	if !ok {
		return SuggestedLimitResult{Source: RateLimitSourceUnknown}
	}
	return suggestedLimitFromState(state)
}

// AllSuggestedLimits 返回所有已观测 endpoint 的限速建议。并发安全。
func (d *RateLimitDiscoverer) AllSuggestedLimits() map[string]SuggestedLimitResult {
	d.mu.RLock()
	defer d.mu.RUnlock()

	result := make(map[string]SuggestedLimitResult, len(d.states))
	for uid, state := range d.states {
		if state.ObserveCount == 0 {
			continue
		}
		result[uid] = suggestedLimitFromState(state)
	}
	return result
}

// GetState 返回指定 endpoint 的学习状态快照（深拷贝），供序列化/调试。
// 返回 nil 表示该 endpoint 无观测记录。
func (d *RateLimitDiscoverer) GetState(endpointUID string) *endpointLearnState {
	d.mu.RLock()
	defer d.mu.RUnlock()

	state, ok := d.states[endpointUID]
	if !ok {
		return nil
	}
	cp := *state
	return &cp
}

// StateCount 返回当前跟踪的 endpoint 数量。
func (d *RateLimitDiscoverer) StateCount() int {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return len(d.states)
}

// ── 内部推导逻辑（§4.5.3）──

// observeHeader 处理 header 显式 limit 信号。
// 规则：estimatedRPM = normalize(limit, reset/window)，confidence = 0.9。
func (d *RateLimitDiscoverer) observeHeader(state *endpointLearnState, sig RateLimitSignal, now time.Time) {
	if sig.Limit > 0 {
		// header 明确给 limit：换算为 RPM
		window := sig.WindowSeconds
		if window <= 0 && sig.ResetSeconds > 0 {
			window = sig.ResetSeconds
		}
		if window <= 0 {
			window = 60 // 默认 1 分钟窗口
		}
		state.WindowSeconds = int(window)
		rpm := normalizeToRPM(sig.Limit, window)
		rpm = d.clampRPM(rpm)
		state.EstimatedRPM = rpm
		state.Source = RateLimitSourceHeader
		state.Confidence = d.cfg.HeaderConfidenceMax

		if !d.cfg.QuietLogs {
			log.Printf("[RateLimitDiscover-Header] endpoint=%s limit=%d window=%.0fs -> rpm=%d confidence=%.2f",
				"", sig.Limit, window, rpm, state.Confidence)
		}
		return
	}

	// 只有 remaining/reset：估算当前消耗速度
	if sig.Remaining >= 0 && sig.ResetSeconds > 0 {
		state.WindowSeconds = int(sig.ResetSeconds)
		// observedRate = 已消耗量 / 已过去时间，但这里只有 remaining 和 reset，
		// 用 capacity = remaining + consumed 近似。
		// 更保守地：如果 remaining 很低，说明 capacity 接近 remaining + 已知消耗。
		// 这里简化为：inferred_capacity = remaining + (剩余窗口内的预估消耗)
		// 保守策略：以 reset 窗口的 remaining 推算 RPM 上限
		if sig.ResetSeconds > 0 {
			// remaining 是 reset 窗口内剩余配额，reset 是距重置的时间
			// 推算：窗口总容量 >= remaining，假设已消耗的量 = elapsed 内的消耗
			// 这里无法精确得知窗口总容量，保守估计 remaining 就是上限残余
			// 最安全的推算：RPM <= remaining / (resetSeconds / 60)
			resetMinutes := sig.ResetSeconds / 60.0
			if resetMinutes > 0 {
				inferredRPM := int(float64(sig.Remaining) / resetMinutes)
				if inferredRPM > 0 {
					// 取 min(当前估算, 推算值) —— 只降不升
					if state.EstimatedRPM == 0 || inferredRPM < state.EstimatedRPM {
						state.EstimatedRPM = d.clampRPM(inferredRPM)
					}
					state.Source = RateLimitSourceHeader
				}
			}
		}

		// 逐次成功解析后提升 confidence，最高 RemainingConfidenceMax
		state.HeaderSuccessCount++
		newConf := math.Min(
			float64(state.HeaderSuccessCount)*0.15,
			d.cfg.RemainingConfidenceMax,
		)
		if newConf > state.Confidence {
			state.Confidence = newConf
		}
	}
}

// observe429 处理 429 信号。三档规则：
//   - 有 Retry-After：×0.5，confidence >= 0.7
//   - 无 Retry-After 且 Reason=AccountRateLimitExceeded：×0.7，confidence >= 0.6
//   - 其他无 header 429：×0.7，confidence >= 0.5
//
// 已有 header 高置信基线时，429 只降低 RPM，不降低既有 confidence。
// PassiveAimdEnabled=false 时只更新 429 时间戳，不降速。
func (d *RateLimitDiscoverer) observe429(state *endpointLearnState, sig RateLimitSignal, now time.Time) {
	state.Last429At = &now
	state.ConsecutiveSuccessesSince429 = 0
	state.No429Since = nil

	// PassiveAimdEnabled=false：仅记录画像时间戳，不执行 AIMD 降速
	if !d.cfg.PassiveAimdEnabled {
		return
	}
	currentRPM := state.EstimatedRPM
	if currentRPM <= 0 {
		// 还没有基线估算，用 MaxAutoRPM 的一半作为起点
		currentRPM = d.cfg.MaxAutoRPM / 2
	}

	var newRPM int
	var minConf float64
	if sig.HasRetryAfter && sig.RetryAfterSeconds > 0 {
		// 429 + Retry-After: 估算 = floor(current * 0.5)，confidence >= 0.7
		newRPM = int(math.Floor(float64(currentRPM) * 0.5))
		minConf = 0.7
	} else if sig.Reason == RateLimitReasonAccountRateLimitExceeded {
		// 无 Retry-After 且确认账号级限流：×0.7，confidence >= 0.6
		newRPM = int(math.Floor(float64(currentRPM) * 0.7))
		minConf = 0.6
	} else {
		// 其他无 header 429: 估算 = floor(current * 0.7)，confidence >= 0.5
		newRPM = int(math.Floor(float64(currentRPM) * 0.7))
		minConf = 0.5
	}
	if sig.IsStreaming {
		d.reduceConcurrentOnCongestion(state, now, minConf, "429")
	}
	if newRPM < d.cfg.MinRPM {
		newRPM = d.cfg.MinRPM
	}
	state.EstimatedRPM = newRPM
	state.Source = RateLimitSourcePassiveAIMD
	// 只升不降：已有更高 confidence（如 header 0.9）时保留，429 只降 RPM
	if state.Confidence < minConf {
		state.Confidence = minConf
	}

	if !d.cfg.QuietLogs {
		log.Printf("[RateLimitDiscover-429] endpoint=unknown 429 reason=%s retryAfter=%v rpm: %d -> %d confidence=%.2f",
			sig.Reason, sig.HasRetryAfter, currentRPM, newRPM, state.Confidence)
	}
}

// observeStreamingLatency 以流式响应头 TTFB 作为排队拥塞信号。
// 只在相对基线连续显著变慢时降并发，避免把单次长上下文请求当作故障。
func (d *RateLimitDiscoverer) observeStreamingLatency(state *endpointLearnState, latencyMs int64, now time.Time) {
	if latencyMs <= 0 {
		return
	}

	state.LatencySampleCount++
	if state.LatencyBaselineMs <= 0 {
		state.LatencyBaselineMs = float64(latencyMs)
	} else if state.LatencySampleCount <= latencyWarmupSamples {
		count := float64(state.LatencySampleCount)
		state.LatencyBaselineMs = (state.LatencyBaselineMs*(count-1) + float64(latencyMs)) / count
	}

	if state.LatencySampleCount < latencyWarmupSamples || state.LatencyBaselineMs <= 0 {
		return
	}

	threshold := math.Max(float64(latencySlowFloorMs), state.LatencyBaselineMs*latencySlowRatio)
	slow := float64(latencyMs) >= threshold || latencyMs >= latencyAbsoluteSlowMs
	if slow {
		state.ConsecutiveSlowLatency++
		state.ConsecutiveHealthyLatency = 0
		if state.ConsecutiveSlowLatency >= latencySlowStreakThreshold {
			d.reduceConcurrentOnCongestion(state, now, latencyCongestionConfidence, "slow_ttfb")
			state.ConsecutiveSlowLatency = 0
		}
		return
	}

	state.ConsecutiveSlowLatency = 0
	if latencyMs < int64(state.LatencyBaselineMs) {
		state.LatencyBaselineMs = state.LatencyBaselineMs*(1-latencyBaselineAlpha) + float64(latencyMs)*latencyBaselineAlpha
	} else {
		// 正常样本只缓慢抬高基线，避免持续拥塞被快速吸收。
		state.LatencyBaselineMs = state.LatencyBaselineMs*0.98 + float64(latencyMs)*0.02
	}
	if state.MaxConcurrent <= 0 {
		return
	}
	state.ConsecutiveHealthyLatency++
	if state.ConsecutiveHealthyLatency < latencyHealthyStreakTarget || state.LastConcurrencyAdjustmentAt == nil {
		return
	}
	if now.Sub(*state.LastConcurrencyAdjustmentAt) < d.cfg.AIMDIncreaseInterval {
		return
	}
	if state.MaxConcurrent < d.cfg.MaxAutoConcurrent {
		state.MaxConcurrent++
		state.ConcurrentConfidence = math.Max(state.ConcurrentConfidence, latencyCongestionConfidence)
		state.LastConcurrencyAdjustmentAt = &now
		state.ConsecutiveHealthyLatency = 0
		if !d.cfg.QuietLogs {
			log.Printf("[RateLimitDiscover-Latency] endpoint=unknown healthy recovery concurrent -> %d", state.MaxConcurrent)
		}
	}
}

func (d *RateLimitDiscoverer) reduceConcurrentOnCongestion(state *endpointLearnState, now time.Time, confidence float64, reason string) {
	current := state.MaxConcurrent
	if current <= 0 {
		current = d.cfg.MaxAutoConcurrent
	}
	if current <= 1 {
		state.MaxConcurrent = 1
		if state.ConcurrentConfidence < confidence {
			state.ConcurrentConfidence = confidence
		}
		return
	}
	// 同因连续降并发需要间隔保护，避免抖动；429 与慢 TTFB 互为独立信号，应即时响应。
	if state.LastConcurrencyAdjustmentAt != nil &&
		now.Sub(*state.LastConcurrencyAdjustmentAt) < d.cfg.AIMDIncreaseInterval/2 &&
		state.lastConcurrencyReason == reason {
		return
	}

	newConcurrent := int(math.Floor(float64(current) * 0.5))
	if newConcurrent >= current {
		newConcurrent = current - 1
	}
	if newConcurrent < 1 {
		newConcurrent = 1
	}
	state.MaxConcurrent = newConcurrent
	state.ConcurrentConfidence = math.Max(state.ConcurrentConfidence, confidence)
	state.LastConcurrencyAdjustmentAt = &now
	state.lastConcurrencyReason = reason
	state.ConsecutiveHealthyLatency = 0
	if state.Source == "" || state.Source == RateLimitSourceUnknown {
		state.Source = RateLimitSourcePassiveAIMD
	}
	if !d.cfg.QuietLogs {
		log.Printf("[RateLimitDiscover-Latency] endpoint=unknown congestion=%s concurrent: %d -> %d confidence=%.2f", reason, current, newConcurrent, state.ConcurrentConfidence)
	}
}

// observeSuccess 处理成功信号，用于 AIMD 缓慢上调。
// 规则：每 10 分钟最多 +10%，且需要最近 15 分钟无 429。
// PassiveAimdEnabled=false 时只更新成功时间戳，不上调 RPM。
func (d *RateLimitDiscoverer) observeSuccess(state *endpointLearnState, sig RateLimitSignal, now time.Time) {
	if sig.LatencyMs > 0 {
		state.LastSuccessAt = &now
	}

	// 更新 No429Since
	if state.Last429At != nil {
		state.ConsecutiveSuccessesSince429++
		if state.No429Since == nil {
			// 从最近 429 时间开始算起
			since := *state.Last429At
			state.No429Since = &since
		}
	} else {
		// 从未见过 429
		if state.No429Since == nil {
			state.No429Since = &now
		}
	}

	// PassiveAimdEnabled=false：不执行 AIMD 上调
	if !d.cfg.PassiveAimdEnabled {
		return
	}

	// AIMD 上调条件：
	// 1. 有基线估算
	// 2. 距上次上调 >= AIMDIncreaseInterval
	// 3. 最近 AIMDNo429Grace 内无 429
	// 4. 估算未超过 MaxAutoRPM
	if state.EstimatedRPM <= 0 {
		return
	}
	if state.EstimatedRPM >= d.cfg.MaxAutoRPM {
		return
	}

	// 检查上调间隔
	if state.LastAIMDIncreaseAt != nil {
		if now.Sub(*state.LastAIMDIncreaseAt) < d.cfg.AIMDIncreaseInterval {
			return
		}
	}

	// 检查无 429 窗口
	if state.No429Since != nil {
		if now.Sub(*state.No429Since) < d.cfg.AIMDNo429Grace {
			return
		}
	}

	// 满足上调条件：+10%
	increase := math.Max(1, float64(state.EstimatedRPM)*d.cfg.AIMDIncreasePercent/100.0)
	newRPM := state.EstimatedRPM + int(increase)
	newRPM = d.clampRPM(newRPM)

	if newRPM > state.EstimatedRPM {
		state.EstimatedRPM = newRPM
		state.Source = RateLimitSourcePassiveAIMD
		state.LastAIMDIncreaseAt = &now

		if !d.cfg.QuietLogs {
			log.Printf("[RateLimitDiscover-AIMD] endpoint=unknown AIMD increase rpm: %d -> %d",
				state.EstimatedRPM-int(increase), newRPM)
		}
	}
}

// UpdateConfig 热重载发现器参数（并发安全）。
// 对 0 值字段保留默认，避免热重载把 NewRateLimitDiscoverer 已填的默认覆盖为 0。
// PassiveAimdEnabled 直接采用传入值（配置默认 true 由 config 层保证）；
// 从 true→false 时不会清除已写入的画像，只是停止后续 AIMD 调整。
func (d *RateLimitDiscoverer) UpdateConfig(cfg RateLimitDiscovererConfig) {
	if d == nil {
		return
	}
	def := defaultDiscovererConfig()
	if cfg.MinRPM <= 0 {
		cfg.MinRPM = def.MinRPM
	}
	if cfg.MaxAutoRPM <= 0 {
		cfg.MaxAutoRPM = def.MaxAutoRPM
	}
	if cfg.MaxAutoConcurrent <= 0 {
		cfg.MaxAutoConcurrent = def.MaxAutoConcurrent
	}
	if cfg.ConfidenceThreshold <= 0 {
		cfg.ConfidenceThreshold = def.ConfidenceThreshold
	}
	if cfg.AIMDIncreaseInterval <= 0 {
		cfg.AIMDIncreaseInterval = def.AIMDIncreaseInterval
	}
	if cfg.AIMDIncreasePercent <= 0 {
		cfg.AIMDIncreasePercent = def.AIMDIncreasePercent
	}
	if cfg.AIMDNo429Grace <= 0 {
		cfg.AIMDNo429Grace = def.AIMDNo429Grace
	}
	if cfg.HeaderConfidenceMax <= 0 {
		cfg.HeaderConfidenceMax = def.HeaderConfidenceMax
	}
	if cfg.RemainingConfidenceMax <= 0 {
		cfg.RemainingConfidenceMax = def.RemainingConfidenceMax
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	// 保留 nowFunc，仅替换可调参数
	d.cfg = cfg
	for _, state := range d.states {
		if state.MaxConcurrent > cfg.MaxAutoConcurrent {
			state.MaxConcurrent = cfg.MaxAutoConcurrent
		}
	}
}

// ── 辅助函数 ──

// normalizeToRPM 将窗口内 limit 值换算为 RPM。
func normalizeToRPM(limit int, windowSeconds float64) int {
	if windowSeconds <= 0 {
		return limit
	}
	// 换算：limit / (window / 60)
	minutes := windowSeconds / 60.0
	return int(math.Round(float64(limit) / minutes))
}

// clampRPM 将 RPM 限制在 [MinRPM, MaxAutoRPM] 范围内。
func (d *RateLimitDiscoverer) clampRPM(rpm int) int {
	if rpm < d.cfg.MinRPM {
		return d.cfg.MinRPM
	}
	if rpm > d.cfg.MaxAutoRPM {
		return d.cfg.MaxAutoRPM
	}
	return rpm
}
