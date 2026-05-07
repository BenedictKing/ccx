// Package loadbalance 实现 ccx 的加权打分式 channel 负载均衡。
//
// 设计参考 axonhub/internal/server/orchestrator/load_balancer.go：
// 把"channel 选择"抽象为多个 LoadBalanceStrategy 各自打分、求和、排序的过程；
// 单个 strategy 关注一个维度（错误率 / 延迟 / 限流 / trace 亲和性 / 加权轮询 /
// 促销期），互不依赖；LoadBalancer 负责调度与决策日志。
//
// 与 ccx 现有 internal/scheduler 的关系（PR2 Phase 1 阶段）：
//   - 本包仅提供骨架 + 接口契约 + Decision 类型 + partial sort；
//   - 6 个具体 strategy 留给 Phase 2 实现；
//   - scheduler 改造（接入 LoadBalancer.Sort）留给 Phase 3；
//   - PR2 全部完成前 handler 仍走旧 SelectChannel 路径，risk = 0。
//
// 与 ccx key 级机制的边界（PRD 第 79-83 行）：
//   - LoadBalancer 仅做 channel 排序，**不做 key 级判定**；
//   - BlacklistKey / MarkKeyAsFailedWithDuration / MatchPauseRule 留在 attempt
//     内的 RawResponse middleware（PR3 实现）。
package loadbalance

import (
	"context"
	"time"
)

// Channel 是 LB 看到的 channel 抽象数据载体。
//
// 这里**不直接持有 *config.UpstreamConfig**，原因：
//  1. config.UpstreamConfig 字段过多，strategy 不需要全部；
//  2. scheduler 改造（PR2 Phase 3）会把 *config.UpstreamConfig 投影到本类型，
//     避免 strategy 与配置耦合，便于单测构造。
//
// 字段语义见各 strategy 的注释；OrderingWeight 越大越优先；Tags / Priority
// 用于 strategy_weight_rr 等的分组打分。
type Channel struct {
	// ID 是 channel 在 scheduler 维度的全局唯一 ID（即 ccx 现有的"渠道索引"）。
	ID int
	// Name 用于决策日志。
	Name string
	// Priority 来自 config.UpstreamConfig.Priority；越小越优先（与 ccx 现状对齐）。
	Priority int
	// OrderingWeight 来自 config.UpstreamConfig.OrderingWeight；越大越优先。
	// strategy_weight_rr 使用此值做加权轮询打分。
	OrderingWeight int
	// Tags 预留给 axonhub 风格的 tag-aware 候选过滤；PR2 Phase 1 阶段不消费。
	Tags []string
}

// LoadBalanceStrategy 定义单个打分策略的契约。
//
// 实现需满足：
//   - Score 返回 0-1000 的相对分数（具体范围取决于策略本身）；
//   - ScoreWithDebug 必须与 Score 保持等价的打分逻辑，仅多产出 StrategyScore；
//   - Name 返回稳定字符串，用于 DecisionLog。
//
// 实现可以是无状态的，也可以持有 ChannelMetricsProvider 等依赖；具体由
// 构造函数注入。
type LoadBalanceStrategy interface {
	Score(ctx context.Context, channel *Channel) float64
	ScoreWithDebug(ctx context.Context, channel *Channel) (float64, StrategyScore)
	Name() string
}

// StrategyScore 持有单个 strategy 在某次决策中的打分细节。
type StrategyScore struct {
	StrategyName string
	Score        float64
	// Details 由具体 strategy 填写，例如 ErrorAware 会写入 ConsecutiveFailures。
	Details  map[string]any
	Duration time.Duration
}

// ChannelDecision 持有单个 channel 在某次决策中的总分与各 strategy 细节。
//
// 排序规则：TotalScore 越大越靠前；TotalScore 相同时由 OrderingWeight 决定
// （在 partial_sort.go 内实现 stable secondary key）。
type ChannelDecision struct {
	Channel        *Channel
	TotalScore     float64
	StrategyScores []StrategyScore
	// FinalRank 在 LoadBalancer.Sort 完成后由调用方填充（1 = 最高优先级）。
	FinalRank int
}

// DecisionLog 是一次完整 LB 决策的可观测结构。
//
// 仅 sortWithDebug 路径产出；生产路径（sortProduction）为性能保留 minimal 输出。
// 决策日志写入 slog 或文件由调用方决定，本包不直接 IO。
type DecisionLog struct {
	Timestamp     time.Time
	ChannelCount  int
	TotalDuration time.Duration
	Channels      []ChannelDecision
}
