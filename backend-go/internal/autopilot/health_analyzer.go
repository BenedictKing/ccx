package autopilot

import (
	"math"
	"time"
)

// ── 死亡类型 ──

// DeathType 细分 dead 状态的根因类型（设计 §6.3）。
type DeathType string

const (
	DeathTypeHard    DeathType = "hard"    // DNS/TLS/认证失败
	DeathTypeSoft    DeathType = "soft"    // 429/quota/临时错误
	DeathTypeModel   DeathType = "model"   // 模型不可用（L1 或 L3）
	DeathTypeQuality DeathType = "quality" // 空响应/断流
	DeathTypeUnknown DeathType = "unknown" // 无法分类
)

// ── EndpointSignals 输入结构体 ──

// EndpointSignals 是调用方从 MetricsManager 被动采集的 endpoint 级诊断信号。
// 全部字段由 worker 填充，HealthAnalyzer 不主动抓取任何数据。
type EndpointSignals struct {
	// ── 基础计数（滑动窗口）──
	TotalRequests1h  int // 最近 1 小时总请求数
	SuccessCount1h   int // 最近 1 小时成功请求数
	FailureCount1h   int // 最近 1 小时失败请求数
	TotalRequests24h int // 最近 24 小时总请求数
	SuccessCount24h  int // 最近 24 小时成功请求数
	FailureCount24h  int // 最近 24 小时失败请求数
	// 按 S 型时间衰减加权的 24h 有效计数：越是久远的记录权重越低
	// （w(age)=1/(1+exp((age-4h)/1h))），用于软性判据，避免陈旧故障无限期维持判死。
	EffectiveSuccessCount24h float64
	EffectiveFailureCount24h float64
	TotalRequests15m         int     // 最近 15 分钟总请求数
	SuccessRate15m           float64 // 最近 15 分钟成功率（直接从 MetricsManager 取）
	SuccessRate1h            float64 // 最近 1 小时成功率（直接从 MetricsManager 取）
	ConsecutiveFail          int     // 连续失败次数
	LastSuccessAt            *time.Time
	LastFailureAt            *time.Time
	Now                      time.Time // 当前时间（方便测试注入）

	// ── 错误分类计数（最近 1 小时）──
	AuthFailureCount   int // 401/403 非重试失败（FailureClass=non_retryable）
	DNSFailureCount    int // DNS/TLS/连接失败（error 含 dial tcp/tls/certificate）
	QuotaFailureCount  int // 402/insufficient_balance/insufficient_quota
	OverloadedCount    int // FailureClass=overloaded（429）
	RetryAfterCount    int // 最近 5 分钟 Retry-After header 出现次数
	NotFoundCount      int // 404 次数
	ProtocolErrorCount int // 501/505 次数
	StreamBreakCount   int // 断流次数（streaming→非 completed）
	EmptyResponseCount int // 空响应次数（usage 全零但无报错）

	// ── 熔断器 ──
	CircuitBreakerOpen bool // 熔断器是否处于 open 状态

	// ── Key 统计 ──
	TotalKeys    int // 总 Key 数量
	DisabledKeys int // 已禁用 Key 数量
}

// ── 诊断结果 ──

// DiagnosisResult 是 HealthAnalyzer.Diagnose 的输出。
type DiagnosisResult struct {
	State      HealthState
	DeathType  DeathType // 仅 State==dead 时有意义
	Confidence float64   // 诊断置信度 0.0-1.0
	Reason     string    // 中文诊断原因
}

// 软死有效计数门槛：加权失败证据低于 failureFloor 视为证据已衰减殆尽；
// 加权成功证据高于 successFloor 即视为“窗口内有成功”，均不判死。
const (
	softDeathEffectiveFailureFloor = 1.0
	softDeathEffectiveSuccessFloor = 0.5
)

// ── HealthAnalyzer ──

// HealthAnalyzer 实现 L1 被动健康诊断。
// 按设计 §6.2 规则顺序判定：Dead → Limited → Misconfigured → Degraded → Healthy → Unknown。
type HealthAnalyzer struct{}

// NewHealthAnalyzer 创建一个 HealthAnalyzer 实例。
func NewHealthAnalyzer() *HealthAnalyzer {
	return &HealthAnalyzer{}
}

// Diagnose 根据被动信号判定 HealthState、DeathType 和诊断原因。
// 规则顺序参照设计 §6.2：高置信度 Dead 优先，依次降级。
func (h *HealthAnalyzer) Diagnose(s EndpointSignals) DiagnosisResult {
	// 规范化 Now
	if s.Now.IsZero() {
		s.Now = time.Now()
	}

	// 1. Dead — 高置信度死亡（优先级最高）
	if r, ok := h.checkDead(s); ok {
		return r
	}

	// 2. Limited — 限流中
	if r, ok := h.checkLimited(s); ok {
		return r
	}

	// 3. Misconfigured — 配置疑似错误
	if r, ok := h.checkMisconfigured(s); ok {
		return r
	}

	// 4. Degraded — 可用但质量差
	if r, ok := h.checkDegraded(s); ok {
		return r
	}

	// 5. Healthy — 证据充足且一切正常
	if r, ok := h.checkHealthy(s); ok {
		return r
	}

	// 6. Unknown — 证据不足
	return DiagnosisResult{
		State:      HealthStateUnknown,
		DeathType:  DeathTypeUnknown,
		Confidence: 0.3,
		Reason:     "证据不足：最近请求量过低，无法判定健康状态",
	}
}

// ── Dead 判定（设计 §6.2）──

// checkDead 检查硬死和软死条件。
func (h *HealthAnalyzer) checkDead(s EndpointSignals) (DiagnosisResult, bool) {
	total1h := s.TotalRequests1h

	// ── 硬死（confidence >= 0.95）──

	// 全部 Key 返回 401/403（最近 1 小时，FailureClass=non_retryable）
	if total1h > 0 && s.AuthFailureCount == total1h {
		return DiagnosisResult{
			State:      HealthStateDead,
			DeathType:  DeathTypeHard,
			Confidence: 0.98,
			Reason:     "硬死：全部请求返回 401/403 认证失败",
		}, true
	}
	// 有认证失败且无成功
	if total1h >= 3 && s.AuthFailureCount > 0 && s.SuccessCount1h == 0 {
		return DiagnosisResult{
			State:      HealthStateDead,
			DeathType:  DeathTypeHard,
			Confidence: 0.95,
			Reason:     "硬死：最近 1 小时内有认证失败且无成功请求",
		}, true
	}

	// DNS/TLS 连接失败
	if total1h > 0 && s.DNSFailureCount == total1h {
		return DiagnosisResult{
			State:      HealthStateDead,
			DeathType:  DeathTypeHard,
			Confidence: 0.97,
			Reason:     "硬死：全部请求 DNS/TLS/连接失败",
		}, true
	}
	if s.DNSFailureCount >= 3 && s.SuccessCount1h == 0 {
		return DiagnosisResult{
			State:      HealthStateDead,
			DeathType:  DeathTypeHard,
			Confidence: 0.95,
			Reason:     "硬死：多次 DNS/TLS/连接失败且无成功请求",
		}, true
	}

	// 连续失败 >= 15
	if s.ConsecutiveFail >= 15 {
		return DiagnosisResult{
			State:      HealthStateDead,
			DeathType:  DeathTypeUnknown,
			Confidence: 0.95,
			Reason:     "硬死：连续失败次数达到 " + itoa(s.ConsecutiveFail) + " 次",
		}, true
	}

	// ── 软死（confidence >= 0.80）──

	// 最近 24 小时无成功请求，且有失败记录。
	// 判据使用 S 型时间衰减后的有效计数：陈旧失败（如数小时前的一次本机网络抖动）
	// 证据权重随时间衰减，不足以维持判死时渠道回落到 unknown，由后续流量重新验证。
	effSuccess, effFailure := s.EffectiveSuccessCount24h, s.EffectiveFailureCount24h
	if effSuccess == 0 && effFailure == 0 && s.FailureCount24h > 0 {
		// 信号来源未填充衰减计数时退回原始计数（衰减字段对旧调用方可选）
		effSuccess, effFailure = float64(s.SuccessCount24h), float64(s.FailureCount24h)
	}
	if s.TotalRequests24h > 0 && effSuccess < softDeathEffectiveSuccessFloor && effFailure >= softDeathEffectiveFailureFloor {
		return DiagnosisResult{
			State:      HealthStateDead,
			DeathType:  DeathTypeSoft,
			Confidence: 0.85,
			Reason: "软死：最近 24 小时无成功请求（有效失败证据 " + formatFixed1(effFailure) +
				"，共 " + itoa(s.TotalRequests24h) + " 次尝试）",
		}, true
	}

	// 熔断器 open 且 lastSuccessAt > 6 小时前
	if s.CircuitBreakerOpen && s.LastSuccessAt != nil && s.Now.Sub(*s.LastSuccessAt) > 6*time.Hour {
		return DiagnosisResult{
			State:      HealthStateDead,
			DeathType:  DeathTypeSoft,
			Confidence: 0.82,
			Reason:     "软死：熔断器已打开且最近成功时间超过 6 小时",
		}, true
	}

	// 成功率 < 10%（最近 1 小时，且请求样本 >= 5）
	if total1h >= 5 && s.SuccessRate1h < 0.10 {
		return DiagnosisResult{
			State:      HealthStateDead,
			DeathType:  DeathTypeSoft,
			Confidence: 0.80,
			Reason:     "软死：最近 1 小时成功率仅 " + formatPercent(s.SuccessRate1h) + "（样本 " + itoa(total1h) + " 次）",
		}, true
	}

	return DiagnosisResult{}, false
}

// ── Limited 判定 ──

// checkLimited 检查限流/配额耗尽条件（设计 §6.2 Limited 规则）。
func (h *HealthAnalyzer) checkLimited(s EndpointSignals) (DiagnosisResult, bool) {
	total15m := s.TotalRequests15m

	// FailureClass=overloaded 占比 > 30%（最近 15 分钟）
	if total15m > 0 && float64(s.OverloadedCount)/float64(total15m) > 0.30 {
		return DiagnosisResult{
			State:      HealthStateLimited,
			DeathType:  DeathTypeSoft,
			Confidence: 0.90,
			Reason:     "限流：最近 15 分钟 429 过载占比 " + formatPercent(float64(s.OverloadedCount)/float64(total15m)),
		}, true
	}

	// Retry-After header 出现在最近 5 分钟内
	if s.RetryAfterCount > 0 {
		return DiagnosisResult{
			State:      HealthStateLimited,
			DeathType:  DeathTypeSoft,
			Confidence: 0.88,
			Reason:     "限流：最近 5 分钟收到 Retry-After 响应头",
		}, true
	}

	// FailureClass=quota（402/insufficient_balance/insufficient_quota）
	if s.QuotaFailureCount > 0 {
		return DiagnosisResult{
			State:      HealthStateLimited,
			DeathType:  DeathTypeSoft,
			Confidence: 0.85,
			Reason:     "限流：检测到配额耗尽（quota 错误 " + itoa(s.QuotaFailureCount) + " 次）",
		}, true
	}

	// 熔断器 open 但 lastSuccessAt < 6 小时前（区别于 dead）
	if s.CircuitBreakerOpen {
		recentSuccess := s.LastSuccessAt != nil && s.Now.Sub(*s.LastSuccessAt) <= 6*time.Hour
		if recentSuccess {
			return DiagnosisResult{
				State:      HealthStateLimited,
				DeathType:  DeathTypeSoft,
				Confidence: 0.80,
				Reason:     "限流：熔断器已打开，但最近 6 小时内有成功记录",
			}, true
		}
		// 熔断器 open 且无最近成功 → 已在 checkDead 中处理
	}

	return DiagnosisResult{}, false
}

// ── Misconfigured 判定 ──

// checkMisconfigured 检查配置错误条件（设计 §6.2 Misconfigured 规则）。
func (h *HealthAnalyzer) checkMisconfigured(s EndpointSignals) (DiagnosisResult, bool) {
	total1h := s.TotalRequests1h

	// 全部请求返回 404（modelMapping 指向不存在的模型）
	if total1h > 0 && s.NotFoundCount == total1h {
		return DiagnosisResult{
			State:      HealthStateMisconfigured,
			DeathType:  DeathTypeModel,
			Confidence: 0.92,
			Reason:     "配置错误：全部请求返回 404，模型映射可能指向不存在的模型",
		}, true
	}

	// 501/505（协议不支持）
	if s.ProtocolErrorCount > 0 && s.SuccessCount1h == 0 {
		return DiagnosisResult{
			State:      HealthStateMisconfigured,
			DeathType:  DeathTypeHard,
			Confidence: 0.90,
			Reason:     "配置错误：收到 501/505 协议不支持错误",
		}, true
	}

	return DiagnosisResult{}, false
}

// ── Degraded 判定 ──

// checkDegraded 检查质量降级条件（设计 §6.2 Degraded 规则）。
func (h *HealthAnalyzer) checkDegraded(s EndpointSignals) (DiagnosisResult, bool) {
	total1h := s.TotalRequests1h

	// 成功率 50%-80%（最近 1 小时，请求样本 >= 10）
	if total1h >= 10 && s.SuccessRate1h >= 0.50 && s.SuccessRate1h < 0.80 {
		return DiagnosisResult{
			State:      HealthStateDegraded,
			DeathType:  DeathTypeUnknown,
			Confidence: 0.75,
			Reason:     "质量降级：最近 1 小时成功率 " + formatPercent(s.SuccessRate1h) + "（样本 " + itoa(total1h) + " 次）",
		}, true
	}

	// p95 延迟 > 5000ms 由调用方通过 SuccessRate15m 以外的渠道传入；
	// 这里用断流率和空响应率来辅助判定。

	// 断流率 > 20%（最近 30 分钟）
	streamTotal := s.SuccessCount1h + s.FailureCount1h // 粗略估计
	if streamTotal > 0 && s.StreamBreakCount > 0 {
		breakRate := float64(s.StreamBreakCount) / float64(streamTotal)
		if breakRate > 0.20 {
			return DiagnosisResult{
				State:      HealthStateDegraded,
				DeathType:  DeathTypeQuality,
				Confidence: 0.78,
				Reason:     "质量降级：断流率 " + formatPercent(breakRate) + "（" + itoa(s.StreamBreakCount) + "/" + itoa(streamTotal) + "）",
			}, true
		}
	}

	// 空响应率 > 10%（usage 全零但无报错）
	if streamTotal > 0 && s.EmptyResponseCount > 0 {
		emptyRate := float64(s.EmptyResponseCount) / float64(streamTotal)
		if emptyRate > 0.10 {
			return DiagnosisResult{
				State:      HealthStateDegraded,
				DeathType:  DeathTypeQuality,
				Confidence: 0.72,
				Reason:     "质量降级：空响应率 " + formatPercent(emptyRate) + "（" + itoa(s.EmptyResponseCount) + "/" + itoa(streamTotal) + "）",
			}, true
		}
	}

	return DiagnosisResult{}, false
}

// ── Healthy 判定 ──

// checkHealthy 判定证据充足且各项指标正常的渠道为 healthy。
func (h *HealthAnalyzer) checkHealthy(s EndpointSignals) (DiagnosisResult, bool) {
	total1h := s.TotalRequests1h

	// 最近 1 小时请求数 >= 5 且成功率 >= 80%
	if total1h >= 5 && s.SuccessRate1h >= 0.80 {
		return DiagnosisResult{
			State:      HealthStateHealthy,
			DeathType:  DeathTypeUnknown,
			Confidence: 0.85,
			Reason:     "健康：最近 1 小时成功率 " + formatPercent(s.SuccessRate1h) + "（样本 " + itoa(total1h) + " 次）",
		}, true
	}

	// 有成功请求但样本不足（1 <= 请求 < 5）
	if s.SuccessCount1h > 0 && total1h < 5 {
		return DiagnosisResult{
			State:      HealthStateHealthy,
			DeathType:  DeathTypeUnknown,
			Confidence: 0.60,
			Reason:     "健康（低置信度）：有成功请求但样本不足（" + itoa(total1h) + " 次）",
		}, true
	}

	return DiagnosisResult{}, false
}

// ── 辅助函数 ──

// itoa 将 int 转换为字符串（避免引入 strconv）。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	isNeg := false
	if n < 0 {
		isNeg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if isNeg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

// formatFixed1 将非负浮点数格式化为一位小数字符串（如 "2.4"）。
func formatFixed1(f float64) string {
	if f < 0 {
		f = 0
	}
	tenths := int(math.Round(f * 10))
	return itoa(tenths/10) + "." + itoa(tenths%10)
}

// formatPercent 将 0.0-1.0 的浮点数格式化为百分比字符串（如 "73.5%"）。
// 使用 math.Round 解决浮点精度问题（如 0.735*100=73.4999...）。
func formatPercent(f float64) string {
	// 四舍五入到一位小数：先乘 1000 四舍五入，再除以 10
	pct := math.Round(f*1000) / 10
	whole := int(pct)
	frac := int(math.Abs(pct-float64(whole)) * 10)
	return itoa(whole) + "." + itoa(frac) + "%"
}

// AggregateHealthState 从多个 endpoint DiagnosisResult 聚合出 channel 级 HealthState。
// 聚合策略与 channel_profile.go 的 AggregateChannelProfile 一致：
// 只要仍有 healthy/unknown endpoint，就不让单个 dead/misconfigured endpoint 直接把整条
// 渠道判成终态不可用；mixed 场景统一降到 degraded。
func AggregateHealthState(results []DiagnosisResult) HealthState {
	if len(results) == 0 {
		return HealthStateUnknown
	}
	bestRank := healthStateRank(HealthStateDead)
	worstRank := healthStateRank(HealthStateHealthy)
	hasHealthyLike := false
	for _, r := range results {
		rank := healthStateRank(r.State)
		if rank < bestRank {
			bestRank = rank
		}
		if rank > worstRank {
			worstRank = rank
		}
		if r.State == HealthStateHealthy || r.State == HealthStateUnknown {
			hasHealthyLike = true
		}
	}
	if hasHealthyLike {
		if worstRank > healthStateRank(HealthStateHealthy) {
			return HealthStateDegraded
		}
		return rankToHealthState(bestRank)
	}
	return rankToHealthState(worstRank)
}
