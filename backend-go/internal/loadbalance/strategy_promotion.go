package loadbalance

import (
	"context"
	"time"
)

// PromotionStrategy 是 ccx 独有的促销期渠道加分策略。
//
// 当 ChannelMetricsProvider.IsPromotionActive(channelID) 返回 true 时，给该
// channel 加 boostScore（默认 800）。在 6 个策略的总分里，该值介于
// TraceAware（1000）与 ErrorAware（200）之间，含义是："促销期渠道在没有
// trace 亲和的情况下应当显著优先，但 trace 亲和的渠道仍然优先"。
//
// 与 axonhub 5 个策略的不同点：axonhub 没有"促销"概念；ccx 把现有 scheduler
// 的 promotion 优先级语义保留下来，作为加权打分体系里的一项。
//
// 设计要点：
//   - 仅依赖 1 个 provider 方法（IsPromotionActive），实现最简、零状态；
//   - boostScore 通过构造函数注入，便于仿真测试调权；
//   - ScoreWithDebug 与 Score 共享判断，确保两条路径行为一致。
type PromotionStrategy struct {
	provider   ChannelMetricsProvider
	boostScore float64
}

// DefaultPromotionBoost 是 PRD 第 142 行约定的默认 boost 分。
const DefaultPromotionBoost = 800.0

// NewPromotionStrategy 构造促销策略；provider 不可为 nil；boost <= 0 时回退
// 到 DefaultPromotionBoost。
func NewPromotionStrategy(provider ChannelMetricsProvider, boost float64) *PromotionStrategy {
	if provider == nil {
		provider = NewNoopMetricsProvider()
	}
	if boost <= 0 {
		boost = DefaultPromotionBoost
	}
	return &PromotionStrategy{provider: provider, boostScore: boost}
}

// Name 返回稳定字符串，用于 DecisionLog 索引。
func (s *PromotionStrategy) Name() string { return "promotion" }

// Score 返回 0 或 boostScore；nil channel 安全返回 0。
func (s *PromotionStrategy) Score(_ context.Context, ch *Channel) float64 {
	if ch == nil {
		return 0
	}
	if s.provider.IsPromotionActive(ch.ID) {
		return s.boostScore
	}
	return 0
}

// ScoreWithDebug 与 Score 同逻辑，附带 StrategyScore 用于 DecisionLog。
func (s *PromotionStrategy) ScoreWithDebug(ctx context.Context, ch *Channel) (float64, StrategyScore) {
	start := time.Now()
	score := s.Score(ctx, ch)
	details := map[string]any{
		"boost":   s.boostScore,
		"applied": score > 0,
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
