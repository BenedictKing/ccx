package loadbalance

import (
	"context"
	"time"
)

// RateLimitAwareStrategy 基于"限流 / 配额 / 并发"的健康度打分策略。
//
// 算法（参照 axonhub/orchestrator/lb_strategy_rate_limit.go，PRD 第 33 行
// 范围 -10000 ~ 100）：
//
//	if Limited 且 cooldown 未过期 ⇒ hardPenalty（-10000，等同硬过滤）
//	if MaxConcurrent > 0 且 ActiveConnections >= MaxConcurrent ⇒ hardPenalty
//	otherwise:
//	    quotaRatio = QuotaRemaining / QuotaTotal      （未知则 1.0）
//	    connRatio  = ActiveConnections / MaxConcurrent（未知则 0）
//	    score      = maxScore × quotaRatio × (1 - connRatio)
//	    clamp [0, maxScore]
//
// 设计直觉：
//   - 已限流的 channel 用极大负分排到队尾，让总分排序兜底（不直接 panic）；
//   - 仅有配额数据时按配额剩余百分比给分；
//   - 仅有并发数据时按"还有多少余量"给分；
//   - 数据全无 ⇒ 满分（视为完全可用），与"安全偏向"一致。
type RateLimitAwareStrategy struct {
	provider    ChannelMetricsProvider
	maxScore    float64
	hardPenalty float64
}

// 默认参数（与 axonhub 对齐）。
const (
	DefaultRateLimitMaxScore    = 100.0
	DefaultRateLimitHardPenalty = -10000.0
)

// NewRateLimitAwareStrategy 构造限流感知策略；provider 为 nil 时回退 noop。
func NewRateLimitAwareStrategy(provider ChannelMetricsProvider) *RateLimitAwareStrategy {
	if provider == nil {
		provider = NewNoopMetricsProvider()
	}
	return &RateLimitAwareStrategy{
		provider:    provider,
		maxScore:    DefaultRateLimitMaxScore,
		hardPenalty: DefaultRateLimitHardPenalty,
	}
}

// Name 返回稳定字符串。
func (s *RateLimitAwareStrategy) Name() string { return "rate_limit_aware" }

// Score 生产路径。
func (s *RateLimitAwareStrategy) Score(_ context.Context, ch *Channel) float64 {
	if ch == nil {
		return s.maxScore
	}
	score, _, _ := s.compute(ch)
	return score
}

// ScoreWithDebug 与 Score 同逻辑，附带详细 details。
func (s *RateLimitAwareStrategy) ScoreWithDebug(ctx context.Context, ch *Channel) (float64, StrategyScore) {
	start := time.Now()
	if ch == nil {
		return s.maxScore, StrategyScore{
			StrategyName: s.Name(),
			Score:        s.maxScore,
			Details:      map[string]any{"reason": "nil_channel"},
			Duration:     time.Since(start),
		}
	}
	score, reason, snapshot := s.compute(ch)
	details := map[string]any{
		"channel_id":         ch.ID,
		"max_score":          s.maxScore,
		"hard_penalty":       s.hardPenalty,
		"limited":            snapshot.limited,
		"cooldown_remaining": snapshot.cooldownRemaining.String(),
		"active_connections": snapshot.active,
		"max_concurrent":     snapshot.maxConcurrent,
		"quota_remaining":    snapshot.quotaRemaining,
		"quota_total":        snapshot.quotaTotal,
	}
	if reason != "" {
		details["reason"] = reason
	}
	return score, StrategyScore{
		StrategyName: s.Name(),
		Score:        score,
		Details:      details,
		Duration:     time.Since(start),
	}
}

// rateLimitSnapshot 记录单次 compute 的输入快照，用于 debug details。
type rateLimitSnapshot struct {
	limited           bool
	cooldownRemaining time.Duration
	active            int
	maxConcurrent     int
	quotaRemaining    int64
	quotaTotal        int64
}

// compute 返回 (score, reason, snapshot)。reason 仅在硬负分场景非空。
func (s *RateLimitAwareStrategy) compute(ch *Channel) (float64, string, rateLimitSnapshot) {
	rl := s.provider.RateLimitState(ch.ID)
	active := s.provider.ActiveConnections(ch.ID)
	maxConn := s.provider.MaxConcurrent(ch.ID)

	snap := rateLimitSnapshot{
		limited:        rl.Limited,
		active:         active,
		maxConcurrent:  maxConn,
		quotaRemaining: rl.QuotaRemaining,
		quotaTotal:     rl.QuotaTotal,
	}
	if !rl.CooldownUntil.IsZero() {
		remaining := time.Until(rl.CooldownUntil)
		if remaining < 0 {
			remaining = 0
		}
		snap.cooldownRemaining = remaining
	}

	// 1) 已限流且尚在 cooldown 期 → 硬负分。
	if rl.Limited && snap.cooldownRemaining > 0 {
		return s.hardPenalty, "in_cooldown", snap
	}

	// 2) 并发到顶 → 硬负分。
	if maxConn > 0 && active >= maxConn {
		return s.hardPenalty, "concurrent_full", snap
	}

	// 3) 加权得分。
	quotaRatio := 1.0
	if rl.QuotaTotal > 0 {
		quotaRatio = float64(rl.QuotaRemaining) / float64(rl.QuotaTotal)
		quotaRatio = clamp(quotaRatio, 0, 1)
	}
	connRatio := 0.0
	if maxConn > 0 {
		connRatio = float64(active) / float64(maxConn)
		connRatio = clamp(connRatio, 0, 1)
	}
	score := s.maxScore * quotaRatio * (1 - connRatio)
	score = clamp(score, 0, s.maxScore)
	return score, "", snap
}

// 编译期接口断言。
var _ LoadBalanceStrategy = (*RateLimitAwareStrategy)(nil)
