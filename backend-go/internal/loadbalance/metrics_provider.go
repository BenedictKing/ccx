package loadbalance

import (
	"context"
	"time"
)

// RateLimitState 表示一个 channel 的限流状态。
//
// strategy_ratelimit 根据本结构在 -10000 到 100 之间打分：
//   - 完全无限流 → +100；
//   - 接近上限（90% 配额已用）→ 急剧扣分；
//   - 已限流（cooldown 中）→ -10000（实际等同硬过滤）。
type RateLimitState struct {
	// Limited 表示当前是否已被上游标记为 rate-limited。
	Limited bool
	// CooldownUntil 表示限流恢复时间；零值代表未在限流状态。
	CooldownUntil time.Time
	// QuotaRemaining 是剩余配额（如果上游 header 暴露）；负数表示未知。
	QuotaRemaining int64
	// QuotaTotal 是总配额；负数表示未知。
	QuotaTotal int64
}

// ChannelMetricsProvider 是 LoadBalancer 与 ccx metrics 模块之间的接口契约。
//
// 13 个方法对应 6 个 strategy 各自需要的数据维度：
//
//	TraceAware      LastSuccessfulChannelByTrace
//	ErrorAware      ConsecutiveFailures / LastFailureTime
//	WeightRR        RequestCount / OrderingWeight
//	LatencyAware    FTTLP95 / TPSP50 / E2ELatencyP95
//	RateLimitAware  RateLimitState / ActiveConnections / MaxConcurrent
//	Promotion       IsPromotionActive   （ccx 独有）
//
// 实现位置（PR2 Phase 3）：`internal/scheduler/lb_metrics_provider.go` 把现有
// `metrics.AggregatedMetrics` / `traceAffinity` / config promotion 聚合为
// 实现。Phase 1 阶段提供 noop 默认实现用于单测。
type ChannelMetricsProvider interface {
	// LastSuccessfulChannelByTrace 返回与 traceID 关联的上次成功 channel。
	// ok=false 表示无亲和性记录。
	LastSuccessfulChannelByTrace(ctx context.Context, traceID string) (channelID int, ok bool)

	// ConsecutiveFailures 返回某 channel 当前连续失败次数；0 表示健康。
	ConsecutiveFailures(channelID int) int
	// LastFailureTime 返回某 channel 最近一次失败的时间；零值表示无失败记录。
	LastFailureTime(channelID int) time.Time

	// RequestCount 返回某 channel 在指定时间窗内的请求数；用于 weight RR。
	RequestCount(channelID int, since time.Duration) int
	// OrderingWeight 返回某 channel 的加权轮询权重（来自 config）。
	OrderingWeight(channelID int) int

	// FTTLP95 返回某 channel × model 在最近窗口的首 token 时间 P95。
	// 0 表示无样本。
	FTTLP95(channelID int, model string) time.Duration
	// TPSP50 返回某 channel × model 在最近窗口的 tokens/sec P50。
	// 0 表示无样本。
	TPSP50(channelID int, model string) float64
	// E2ELatencyP95 返回某 channel × model 在最近窗口的端到端时延 P95。
	E2ELatencyP95(channelID int, model string) time.Duration

	// RateLimitState 返回某 channel 当前限流状态。
	RateLimitState(channelID int) RateLimitState
	// ActiveConnections 返回某 channel 当前并发连接数。
	ActiveConnections(channelID int) int
	// MaxConcurrent 返回某 channel 的并发上限（来自 config）；0 表示不限。
	MaxConcurrent(channelID int) int

	// IsPromotionActive 返回某 channel 是否处于促销期；ccx 独有。
	IsPromotionActive(channelID int) bool
}

// noopMetricsProvider 是单测用零值默认实现，所有方法返回零值。
//
// 既用于本包的骨架测试，也允许 scheduler 改造期间渐进接入（先全 noop，
// 再逐个 strategy 替换）。
type noopMetricsProvider struct{}

// NewNoopMetricsProvider 返回一个 13 方法全部返回零值的 metrics provider。
//
// 单测里直接使用；生产代码请用 PR2 Phase 3 的真实 provider。
func NewNoopMetricsProvider() ChannelMetricsProvider { return noopMetricsProvider{} }

func (noopMetricsProvider) LastSuccessfulChannelByTrace(_ context.Context, _ string) (int, bool) {
	return 0, false
}
func (noopMetricsProvider) ConsecutiveFailures(int) int               { return 0 }
func (noopMetricsProvider) LastFailureTime(int) time.Time             { return time.Time{} }
func (noopMetricsProvider) RequestCount(int, time.Duration) int       { return 0 }
func (noopMetricsProvider) OrderingWeight(int) int                    { return 0 }
func (noopMetricsProvider) FTTLP95(int, string) time.Duration         { return 0 }
func (noopMetricsProvider) TPSP50(int, string) float64                { return 0 }
func (noopMetricsProvider) E2ELatencyP95(int, string) time.Duration   { return 0 }
func (noopMetricsProvider) RateLimitState(int) RateLimitState         { return RateLimitState{} }
func (noopMetricsProvider) ActiveConnections(int) int                 { return 0 }
func (noopMetricsProvider) MaxConcurrent(int) int                     { return 0 }
func (noopMetricsProvider) IsPromotionActive(int) bool                { return false }
