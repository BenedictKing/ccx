package loadbalance

import (
	"context"
	"time"
)

// TraceAwareStrategy 把"上一次成功 channel"的亲和性映射为加权分数。
//
// 取值：
//   - ctx 上有 traceID + provider.LastSuccessfulChannelByTrace 命中当前 channel
//     ⇒ 返回 hitScore（默认 1000）
//   - 其他 ⇒ 返回 0
//
// 与 ccx 现有 traceAffinity 的差异：现状是"硬绑定"（命中即直接返回该 channel），
// LB 体系下改为加权打分，让 ErrorAware / RateLimitAware 仍有机会通过显著
// 负分把出问题的亲和 channel 打到后面。这是 axonhub 的设计哲学：避免硬绑定
// 造成"亲和 channel 故障时无法快速 failover"。
type TraceAwareStrategy struct {
	provider ChannelMetricsProvider
	hitScore float64
}

// DefaultTraceAwareHit 是 PRD 第 28 行约定的默认 hit 分。
const DefaultTraceAwareHit = 1000.0

// NewTraceAwareStrategy 构造 trace 亲和策略。
func NewTraceAwareStrategy(provider ChannelMetricsProvider, hit float64) *TraceAwareStrategy {
	if provider == nil {
		provider = NewNoopMetricsProvider()
	}
	if hit <= 0 {
		hit = DefaultTraceAwareHit
	}
	return &TraceAwareStrategy{provider: provider, hitScore: hit}
}

// Name 返回稳定字符串。
func (s *TraceAwareStrategy) Name() string { return "trace_aware" }

// Score 仅在 traceID 非空且 provider 命中时返回 hitScore。
func (s *TraceAwareStrategy) Score(ctx context.Context, ch *Channel) float64 {
	if ch == nil {
		return 0
	}
	traceID := TraceIDFromContext(ctx)
	if traceID == "" {
		return 0
	}
	preferred, ok := s.provider.LastSuccessfulChannelByTrace(ctx, traceID)
	if !ok {
		return 0
	}
	if preferred == ch.ID {
		return s.hitScore
	}
	return 0
}

// ScoreWithDebug 与 Score 共享判断，附带 StrategyScore。
func (s *TraceAwareStrategy) ScoreWithDebug(ctx context.Context, ch *Channel) (float64, StrategyScore) {
	start := time.Now()
	score := s.Score(ctx, ch)
	details := map[string]any{
		"hit_score": s.hitScore,
		"trace_id":  TraceIDFromContext(ctx),
		"applied":   score > 0,
	}
	if ch != nil {
		details["channel_id"] = ch.ID
	}
	return score, StrategyScore{
		StrategyName: s.Name(),
		Score:        score,
		Details:      details,
		Duration:     time.Since(start),
	}
}

// 编译期接口断言。
var (
	_ LoadBalanceStrategy = (*PromotionStrategy)(nil)
	_ LoadBalanceStrategy = (*TraceAwareStrategy)(nil)
)
