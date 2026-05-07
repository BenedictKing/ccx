package loadbalance

import (
	"context"
	"time"
)

// LatencyAwareStrategy 基于"延迟指标"的体感打分策略。
//
// 算法（参照 axonhub/orchestrator/lb_strategy_latency.go）：
//
//	streaming（请求带 stream=true）：
//	  firstTokenScore = clampInverse(FTTL_p95_ms, 3000ms)
//	  throughputScore = clampNorm(TPS_p50, 5, 100)   // 默认 0.5
//	  score = maxScore × (0.7 × firstTokenScore + 0.3 × throughputScore)
//
//	non-streaming：
//	  e2eScore = clampInverse(E2E_p95_ms, 3000ms)
//	  score    = maxScore × e2eScore
//
//	signal 缺失（FTTL/TPS/E2E 全 0）：返回 maxScore/2（中位分作为安全兜底）
//
// 设计直觉：
//   - 越快的 channel 拿越高的分，鼓励"用户体感更好"的 channel 优先
//   - 流式请求侧重首 token 时延 + 吐字速率
//   - 非流式请求看端到端时延
//   - 数据缺失时不偏袒任何 channel
type LatencyAwareStrategy struct {
	provider     ChannelMetricsProvider
	maxScore     float64
	fttlTarget   time.Duration // 流式首 token 目标时延（达到 0 ⇒ 满分；达到 target ⇒ 0 分）
	e2eTarget    time.Duration // 非流式 E2E 目标时延
	tpsMin       float64       // 流式吐字最小可用 TPS
	tpsMax       float64       // 流式吐字最大目标 TPS
	fttlWeight   float64       // 流式首 token 权重
	throughputW  float64       // 流式吞吐权重
}

// 默认参数（与 axonhub 对齐，PRD 第 32 行 score 范围 0-80）。
const (
	DefaultLatencyMaxScore           = 80.0
	DefaultLatencyFTTLTarget         = 3 * time.Second
	DefaultLatencyE2ETarget          = 3 * time.Second
	DefaultLatencyTPSMin             = 5.0
	DefaultLatencyTPSMax             = 100.0
	DefaultLatencyFTTLWeight         = 0.7
	DefaultLatencyThroughputWeight   = 0.3
)

// NewLatencyAwareStrategy 构造延迟感知策略；provider 为 nil 时回退 noop。
func NewLatencyAwareStrategy(provider ChannelMetricsProvider) *LatencyAwareStrategy {
	if provider == nil {
		provider = NewNoopMetricsProvider()
	}
	return &LatencyAwareStrategy{
		provider:    provider,
		maxScore:    DefaultLatencyMaxScore,
		fttlTarget:  DefaultLatencyFTTLTarget,
		e2eTarget:   DefaultLatencyE2ETarget,
		tpsMin:      DefaultLatencyTPSMin,
		tpsMax:      DefaultLatencyTPSMax,
		fttlWeight:  DefaultLatencyFTTLWeight,
		throughputW: DefaultLatencyThroughputWeight,
	}
}

// Name 返回稳定字符串。
func (s *LatencyAwareStrategy) Name() string { return "latency_aware" }

// Score 生产路径。
func (s *LatencyAwareStrategy) Score(ctx context.Context, ch *Channel) float64 {
	if ch == nil {
		return s.maxScore / 2
	}
	score, hasSignal := s.compute(ctx, ch)
	if !hasSignal {
		return s.maxScore / 2
	}
	return score
}

// ScoreWithDebug 与 Score 同逻辑，附带详细 details。
func (s *LatencyAwareStrategy) ScoreWithDebug(ctx context.Context, ch *Channel) (float64, StrategyScore) {
	start := time.Now()
	if ch == nil {
		neutral := s.maxScore / 2
		return neutral, StrategyScore{
			StrategyName: s.Name(),
			Score:        neutral,
			Details:      map[string]any{"reason": "nil_channel"},
			Duration:     time.Since(start),
		}
	}
	model := RequestedModelFromContext(ctx)
	stream := RequestStreamFromContext(ctx)
	fttl := s.provider.FTTLP95(ch.ID, model)
	tps := s.provider.TPSP50(ch.ID, model)
	e2e := s.provider.E2ELatencyP95(ch.ID, model)

	score, hasSignal := s.compute(ctx, ch)
	details := map[string]any{
		"channel_id":  ch.ID,
		"stream":      stream,
		"model":       model,
		"fttl_p95_ms": fttl.Milliseconds(),
		"tps_p50":     tps,
		"e2e_p95_ms":  e2e.Milliseconds(),
		"max_score":   s.maxScore,
	}
	if !hasSignal {
		details["reason"] = "no_latency_signal"
		score = s.maxScore / 2
	}
	return score, StrategyScore{
		StrategyName: s.Name(),
		Score:        score,
		Details:      details,
		Duration:     time.Since(start),
	}
}

// compute 返回 (score, hasSignal)。
func (s *LatencyAwareStrategy) compute(ctx context.Context, ch *Channel) (float64, bool) {
	model := RequestedModelFromContext(ctx)
	stream := RequestStreamFromContext(ctx)

	if stream {
		fttl := s.provider.FTTLP95(ch.ID, model)
		tps := s.provider.TPSP50(ch.ID, model)
		if fttl == 0 && tps == 0 {
			return 0, false
		}
		fttlScore := clampInverseDuration(fttl, s.fttlTarget) // 时延越大分越低
		throughputScore := 0.5
		if tps > 0 {
			throughputScore = clampNorm(tps, s.tpsMin, s.tpsMax)
		}
		score := s.maxScore * (s.fttlWeight*fttlScore + s.throughputW*throughputScore)
		return clamp(score, 0, s.maxScore), true
	}

	e2e := s.provider.E2ELatencyP95(ch.ID, model)
	if e2e == 0 {
		return 0, false
	}
	e2eScore := clampInverseDuration(e2e, s.e2eTarget)
	return clamp(s.maxScore*e2eScore, 0, s.maxScore), true
}

// clamp 把 v 限制在 [lo, hi]。
func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// clampNorm 把 v 线性归一化到 [0,1]：v <= min ⇒ 0；v >= max ⇒ 1。
func clampNorm(v, min, max float64) float64 {
	if max <= min {
		return 0
	}
	r := (v - min) / (max - min)
	return clamp(r, 0, 1)
}

// clampInverseDuration 把"越大越差"的 duration 反向归一化到 [0,1]：
//
//	0 ⇒ 1（极速，满分）
//	target ⇒ 0（达到目标延迟，0 分）
//	> target ⇒ 0
func clampInverseDuration(v, target time.Duration) float64 {
	if target <= 0 {
		return 0
	}
	if v <= 0 {
		return 1
	}
	r := 1.0 - float64(v)/float64(target)
	return clamp(r, 0, 1)
}

// 编译期接口断言。
var _ LoadBalanceStrategy = (*LatencyAwareStrategy)(nil)
