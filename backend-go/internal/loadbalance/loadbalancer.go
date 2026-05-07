package loadbalance

import (
	"context"
	"time"
)

// LoadBalancer 是 channel 排序入口；持有一组 strategy 实例。
//
// 用法（PR2 Phase 3 接入）：
//
//	lb := loadbalance.New(provider,
//	    loadbalance.NewTraceAware(provider),
//	    loadbalance.NewErrorAware(provider),
//	    loadbalance.NewWeightRR(provider),
//	    loadbalance.NewLatencyAware(provider),
//	    loadbalance.NewRateLimitAware(provider),
//	    loadbalance.NewPromotion(provider),
//	)
//	sorted := lb.Sort(ctx, candidates, model, stream)
//	channelToTry := sorted[0]
//
// PR2 Phase 1 阶段仅有空 strategy 列表也是合法的：所有 channel 拿到 0 分，
// 按 OrderingWeight + ID 降序排出可复现序列，便于单测验证容器逻辑。
type LoadBalancer struct {
	strategies []LoadBalanceStrategy
	provider   ChannelMetricsProvider
}

// New 构造 LoadBalancer；strategies 顺序仅影响 DecisionLog 的 StrategyScores
// 排列，不影响最终总分。
func New(provider ChannelMetricsProvider, strategies ...LoadBalanceStrategy) *LoadBalancer {
	if provider == nil {
		provider = NewNoopMetricsProvider()
	}
	return &LoadBalancer{
		strategies: strategies,
		provider:   provider,
	}
}

// Strategies 返回当前注册的 strategy 列表（只读快照）。
func (lb *LoadBalancer) Strategies() []LoadBalanceStrategy {
	out := make([]LoadBalanceStrategy, len(lb.strategies))
	copy(out, lb.strategies)
	return out
}

// Provider 返回构造时注入的 metrics provider。
func (lb *LoadBalancer) Provider() ChannelMetricsProvider { return lb.provider }

// Sort 是生产路径：对 candidates 做 top-K 排序，仅返回前 K 个。
//
//	candidates  待选 channel 列表（已经过 model/stream 兼容性过滤、circuit-open
//	            channel 剔除等"硬过滤"步骤；本函数不做硬过滤）。
//	model       请求 model；本函数不直接用，由 strategy 通过 ctx 取（也允许
//	            strategy 直接读 channel）。
//	stream      请求是否流式；同上。
//	topK        期望返回的前 K 个；<=0 视为返回全部。
//
// 返回值：按总分降序排列的 channel 切片；不修改原 candidates。
func (lb *LoadBalancer) Sort(ctx context.Context, candidates []*Channel, model string, stream bool, topK int) []*Channel {
	if len(candidates) == 0 {
		return nil
	}
	ctx = ContextWithRequestedModel(ctx, model)
	ctx = ContextWithRequestStream(ctx, stream)

	if topK <= 0 || topK > len(candidates) {
		topK = len(candidates)
	}

	// 计算每个 channel 的总分，记录到临时切片。
	decisions := make([]ChannelDecision, len(candidates))
	for i, ch := range candidates {
		var total float64
		for _, s := range lb.strategies {
			total += s.Score(ctx, ch)
		}
		decisions[i] = ChannelDecision{Channel: ch, TotalScore: total}
	}

	partialSortTopK(decisions, topK, decisionLess)

	out := make([]*Channel, topK)
	for i := 0; i < topK; i++ {
		out[i] = decisions[i].Channel
	}
	return out
}

// SortWithDebug 是带决策日志的排序；除返回排序后切片外，还返回 DecisionLog。
//
// 与 Sort 的区别：每个 strategy 用 ScoreWithDebug 求分以填充 StrategyScores
// 与 Duration；总耗时记录到 DecisionLog.TotalDuration。
//
// 适用于：开关打开时的可观测决策、单测验证策略权重平衡、e2e 调试。
func (lb *LoadBalancer) SortWithDebug(ctx context.Context, candidates []*Channel, model string, stream bool, topK int) ([]*Channel, DecisionLog) {
	now := time.Now()

	if len(candidates) == 0 {
		return nil, DecisionLog{Timestamp: now}
	}
	ctx = ContextWithRequestedModel(ctx, model)
	ctx = ContextWithRequestStream(ctx, stream)

	if topK <= 0 || topK > len(candidates) {
		topK = len(candidates)
	}

	decisions := make([]ChannelDecision, len(candidates))
	for i, ch := range candidates {
		scores := make([]StrategyScore, 0, len(lb.strategies))
		var total float64
		for _, s := range lb.strategies {
			score, ss := s.ScoreWithDebug(ctx, ch)
			total += score
			scores = append(scores, ss)
		}
		decisions[i] = ChannelDecision{
			Channel:        ch,
			TotalScore:     total,
			StrategyScores: scores,
		}
	}

	partialSortTopK(decisions, topK, decisionLess)
	for i := range decisions {
		decisions[i].FinalRank = i + 1
	}

	out := make([]*Channel, topK)
	for i := 0; i < topK; i++ {
		out[i] = decisions[i].Channel
	}
	return out, DecisionLog{
		Timestamp:     now,
		ChannelCount:  len(candidates),
		TotalDuration: time.Since(now),
		Channels:      decisions,
	}
}
