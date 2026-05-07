package loadbalance

import (
	"context"
	"math"
	"time"
)

// WeightRoundRobinStrategy 基于"加权轮询"的负载均衡策略。
//
// 算法（参照 axonhub/orchestrator/lb_strategy_rr.go）：
//
//	score = maxScore × exp(-effectiveCount / scalingFactor)
//
// 其中：
//   - effectiveCount = (requestCount / max(1, weight/100)) × decayMultiplier
//   - decayMultiplier = exp(-inactivitySeconds / inactivityDecay)
//   - requestCount 上限由 requestCountCap 截断（默认 1000）
//   - score 最终被 clamp 到 [minScore, maxScore]
//
// 语义直觉：
//   - 请求数为 0 → 拿到 maxScore（新 channel 优先被试）
//   - 请求数与 weight 正比 → 各 channel 分数趋同（权重越大耐受请求越多）
//   - 长时间未活动 → effectiveCount 衰减，分数恢复（避免"曾经火过的 channel"
//     长时间得低分）
//   - metrics 缺失 → 取 (max+min)/2 中位分作为安全兜底
type WeightRoundRobinStrategy struct {
	provider        ChannelMetricsProvider
	maxScore        float64
	minScore        float64
	requestCountCap int
	scalingFactor   float64
	inactivityDecay time.Duration
	since           time.Duration
}

// 默认参数（与 axonhub 对齐，PRD 第 30 行 score 范围 10-150）。
const (
	DefaultWRRMaxScore      = 150.0
	DefaultWRRMinScore      = 10.0
	DefaultWRRRequestCap    = 1000
	DefaultWRRScalingFactor = 150.0
	DefaultWRRInactivity    = 5 * time.Minute
	DefaultWRRRequestSince  = 1 * time.Hour
)

// NewWeightRoundRobinStrategy 构造 WRR 策略；provider 为 nil 时回退 noop。
func NewWeightRoundRobinStrategy(provider ChannelMetricsProvider) *WeightRoundRobinStrategy {
	if provider == nil {
		provider = NewNoopMetricsProvider()
	}
	return &WeightRoundRobinStrategy{
		provider:        provider,
		maxScore:        DefaultWRRMaxScore,
		minScore:        DefaultWRRMinScore,
		requestCountCap: DefaultWRRRequestCap,
		scalingFactor:   DefaultWRRScalingFactor,
		inactivityDecay: DefaultWRRInactivity,
		since:           DefaultWRRRequestSince,
	}
}

// Name 返回稳定字符串。
func (s *WeightRoundRobinStrategy) Name() string { return "weight_rr" }

// Score 生产路径。
func (s *WeightRoundRobinStrategy) Score(_ context.Context, ch *Channel) float64 {
	if ch == nil {
		return (s.maxScore + s.minScore) / 2
	}
	return s.compute(ch)
}

// ScoreWithDebug 与 Score 同逻辑，附带详细 details。
func (s *WeightRoundRobinStrategy) ScoreWithDebug(ctx context.Context, ch *Channel) (float64, StrategyScore) {
	start := time.Now()
	score := s.Score(ctx, ch)
	details := map[string]any{
		"max_score":        s.maxScore,
		"min_score":        s.minScore,
		"scaling_factor":   s.scalingFactor,
		"inactivity_decay": s.inactivityDecay.String(),
	}
	if ch != nil {
		rc := s.provider.RequestCount(ch.ID, s.since)
		w := s.provider.OrderingWeight(ch.ID)
		if w == 0 && ch.OrderingWeight > 0 {
			w = ch.OrderingWeight
		}
		details["channel_id"] = ch.ID
		details["request_count"] = rc
		details["weight"] = w
	}
	return score, StrategyScore{
		StrategyName: s.Name(),
		Score:        score,
		Details:      details,
		Duration:     time.Since(start),
	}
}

// compute 计算单个 channel 的分数。
func (s *WeightRoundRobinStrategy) compute(ch *Channel) float64 {
	requestCount := s.provider.RequestCount(ch.ID, s.since)
	weight := s.provider.OrderingWeight(ch.ID)
	if weight <= 0 {
		// fallback：优先使用 channel 上的 OrderingWeight（来自 config 静态值），
		// 都没有则视为标准权重 100。
		if ch.OrderingWeight > 0 {
			weight = ch.OrderingWeight
		} else {
			weight = 100
		}
	}
	if requestCount <= 0 {
		return s.maxScore
	}

	// cap requestCount
	capped := requestCount
	if s.requestCountCap > 0 && capped > s.requestCountCap {
		capped = s.requestCountCap
	}

	// inactivity decay
	decayMul := 1.0
	last := s.provider.LastFailureTime(ch.ID)
	if !last.IsZero() && s.inactivityDecay > 0 {
		inact := time.Since(last).Seconds()
		if inact > 0 {
			decayMul = math.Exp(-inact / s.inactivityDecay.Seconds())
		}
	}

	weightFactor := float64(weight) / 100.0
	if weightFactor < 0.01 {
		weightFactor = 0.01
	}
	effective := (float64(capped) / weightFactor) * decayMul

	score := s.maxScore * math.Exp(-effective/s.scalingFactor)
	if score < s.minScore {
		score = s.minScore
	}
	if score > s.maxScore {
		score = s.maxScore
	}
	return score
}

// 编译期接口断言。
var _ LoadBalanceStrategy = (*WeightRoundRobinStrategy)(nil)
