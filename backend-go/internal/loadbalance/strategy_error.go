package loadbalance

import (
	"context"
	"time"
)

// ErrorAwareStrategy 基于"近期错误"的健康度打分策略。
//
// 算法（参照 axonhub/orchestrator/lb_strategy_bp.go）：
//
//	cooldownRatio = max(0, 1 - timeSinceLastFailure / errorCooldown)
//	score = maxScore - consecutiveFailures × penaltyPerFailure × cooldownRatio
//	                 - basePenalty × cooldownRatio
//	      ∈ [0, maxScore]
//
// 边界规则：
//   - LastFailureTime 为零值 + ConsecutiveFailures > 0 ⇒ 视为"刚刚失败"，
//     cooldownRatio = 1.0；
//   - cooldownRatio == 0 ⇒ 全部 penalty 衰减殆尽，score = maxScore（健康）；
//   - score 被 clamp 到 [0, maxScore]，防止"上次失败 + 100 次连续失败"产生
//     -∞ 的极端负分破坏总分排序。
type ErrorAwareStrategy struct {
	provider                     ChannelMetricsProvider
	maxScore                     float64
	basePenalty                  float64
	penaltyPerConsecutiveFailure float64
	errorCooldown                time.Duration
}

// 默认参数（与 axonhub 对齐，PRD 第 31 行 score 范围 0-200）。
const (
	DefaultErrorMaxScore                     = 200.0
	DefaultErrorBasePenalty                  = 40.0
	DefaultErrorPenaltyPerConsecutiveFailure = 30.0
	DefaultErrorCooldown                     = 5 * time.Minute
)

// NewErrorAwareStrategy 构造错误感知策略；provider 为 nil 时回退 noop。
func NewErrorAwareStrategy(provider ChannelMetricsProvider) *ErrorAwareStrategy {
	if provider == nil {
		provider = NewNoopMetricsProvider()
	}
	return &ErrorAwareStrategy{
		provider:                     provider,
		maxScore:                     DefaultErrorMaxScore,
		basePenalty:                  DefaultErrorBasePenalty,
		penaltyPerConsecutiveFailure: DefaultErrorPenaltyPerConsecutiveFailure,
		errorCooldown:                DefaultErrorCooldown,
	}
}

// Name 返回稳定字符串。
func (s *ErrorAwareStrategy) Name() string { return "error_aware" }

// Score 生产路径。
func (s *ErrorAwareStrategy) Score(_ context.Context, ch *Channel) float64 {
	if ch == nil {
		return s.maxScore / 2
	}
	score, _, _ := s.compute(ch)
	return score
}

// ScoreWithDebug 与 Score 同逻辑，附带详细 details。
func (s *ErrorAwareStrategy) ScoreWithDebug(ctx context.Context, ch *Channel) (float64, StrategyScore) {
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
	score, failures, ratio := s.compute(ch)
	return score, StrategyScore{
		StrategyName: s.Name(),
		Score:        score,
		Details: map[string]any{
			"channel_id":           ch.ID,
			"max_score":            s.maxScore,
			"consecutive_failures": failures,
			"cooldown_ratio":       ratio,
			"error_cooldown":       s.errorCooldown.String(),
		},
		Duration: time.Since(start),
	}
}

// compute 返回 (score, consecutiveFailures, cooldownRatio)。
func (s *ErrorAwareStrategy) compute(ch *Channel) (float64, int, float64) {
	failures := s.provider.ConsecutiveFailures(ch.ID)
	last := s.provider.LastFailureTime(ch.ID)

	cooldownRatio := 0.0
	if !last.IsZero() {
		elapsed := time.Since(last)
		if elapsed < s.errorCooldown && elapsed >= 0 {
			cooldownRatio = 1.0 - elapsed.Seconds()/s.errorCooldown.Seconds()
		}
	} else if failures > 0 {
		// 没有时间戳但有连续失败 → 视为"刚刚发生"
		cooldownRatio = 1.0
	}

	score := s.maxScore
	if cooldownRatio > 0 {
		penalty := float64(failures)*s.penaltyPerConsecutiveFailure*cooldownRatio + s.basePenalty*cooldownRatio
		score -= penalty
	}
	if score < 0 {
		score = 0
	}
	if score > s.maxScore {
		score = s.maxScore
	}
	return score, failures, cooldownRatio
}

// 编译期接口断言。
var _ LoadBalanceStrategy = (*ErrorAwareStrategy)(nil)
