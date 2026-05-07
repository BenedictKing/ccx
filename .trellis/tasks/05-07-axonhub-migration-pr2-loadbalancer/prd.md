# PR2: load balancer with 5 strategies and scheduler integration

> 父任务: [05-07-axonhub-forwarding-migration](../05-07-axonhub-forwarding-migration/prd.md)
> 依赖: [PR1 pipeline skeleton](../05-07-axonhub-migration-pr1-pipeline-skeleton/prd.md)（不强阻塞，可并行启动）

## Goal

把 axonhub 的 5 个 LoadBalanceStrategy 迁移到 ccx，加上 ccx 独有的 PromotionStrategy 共 6 个策略；改造现有 scheduler 为新 LoadBalancer 提供数据；保持 ccx key 级 BlacklistKey/MarkKeyAsFailed/MatchPauseRule 独立运行。

## Why

- ccx 现有 scheduler 是多级硬过滤（promotion → priority → affinity → circuit），axonhub 是加权打分；行为差异需要消除
- 新 LB 不立即接入 handler（PR3 才接），本 PR 仅完成结构 + 单测
- 引入 5 个 strategy 是大改动，独立成 PR 便于 review

## Requirements

### 新增包

```
backend-go/internal/loadbalance/
  loadbalancer.go               # LoadBalancer 容器 + Sort()
  strategy.go                   # LoadBalanceStrategy interface
  decision.go                   # ChannelDecision / StrategyScore / DecisionLog
  partial_sort.go               # top-k partial sort (avoid full sort)
  context.go                    # contextWithRequestedModel / requestedModelFromContext

  strategy_trace.go             # TraceAware (0 / 1000)
  strategy_error.go             # ErrorAware (0-200)
  strategy_weight_rr.go         # WeightRoundRobin (10-150)
  strategy_latency.go           # LatencyAware (0-80)
  strategy_ratelimit.go         # RateLimitAware (-10000-100)
  strategy_promotion.go         # ccx 独有：促销渠道 (0 / 800)

  strategy_test.go              # 每个 strategy 单测
  loadbalancer_test.go          # 整合测试 + 决策日志测试
```

### LoadBalanceStrategy 接口（沿用 axonhub）

```go
type LoadBalanceStrategy interface {
    Score(ctx, channel) float64
    ScoreWithDebug(ctx, channel) (float64, StrategyScore)
    Name() string
}
```

### 数据接口（ChannelMetricsProvider）

```go
type ChannelMetricsProvider interface {
    LastSuccessfulChannelByTrace(ctx, traceID string) (channelID int, ok bool)
    ConsecutiveFailures(channelID int) int
    LastFailureTime(channelID int) time.Time
    RequestCount(channelID int, since time.Duration) int
    OrderingWeight(channelID int) int
    FTTLP95(channelID int, model string) time.Duration       // 首 token 时间
    TPSP50(channelID int, model string) float64              // 平均 tokens/sec
    E2ELatencyP95(channelID int, model string) time.Duration
    RateLimitState(channelID int) RateLimitState
    ActiveConnections(channelID int) int
    MaxConcurrent(channelID int) int

    // ccx 独有
    IsPromotionActive(channelID int) bool
}
```

### scheduler 改造

ccx 现有 `internal/scheduler/`：
- 把现有 selectChannel 拆解为：(a) 候选过滤（model 兼容性、stream 策略、circuit Open 跳过）+ (b) LoadBalancer.Sort 排序
- 实现 `ChannelMetricsProvider`（大部分字段已存在于 `metrics.AggregatedMetrics`）
- 新增字段（FTTL / TPS / ActiveConnections）按需扩展 `metrics/channel_metrics.go`

### 与 ccx key 级机制的边界

⚠️ 严格分离：
- **LoadBalancer 阶段**只做 channel 排序，**不做 key 级判定**
- key 级机制（BlacklistKey / MarkKeyAsFailed / MatchPauseRule）保留在 attempt 内（`TryUpstreamWithAllKeys` 内或 PR3 的 RawResponse middleware）
- model-CB（PR2 引入，作为 LB 一部分）在 channel 选择前生效；key 级在选完 channel 之后

## Acceptance Criteria

- [ ] `go test ./internal/loadbalance/... -count=1 -race` 通过
- [ ] 6 个 strategy 各自单测覆盖率 ≥ 85%
- [ ] LB 整合测试：模拟 3 channel 场景验证排序符合 `axonhub/internal/server/orchestrator/load-balancing.md` 表 1（Channel A/B/C 总分排序）
- [ ] partial sort 性能测试：1000 channel 场景下比 full sort 快 ≥ 30%
- [ ] DecisionLog 输出能复现 axonhub log-balancing.md 第 168-201 行结构
- [ ] `internal/scheduler/` 现有测试不回归
- [ ] 现有 `internal/metrics/channel_metrics.go` 测试通过（字段扩展不破坏现有 API）

## Definition of Done

- spec：`.trellis/spec/backend/load-balancer.md` 新增（接口 + 6 策略 + 数据需求）
- LoadBalancer 不接入 handler（PR3 接），handler 仍走旧 scheduler 路径
- LB 单元测试 + 仿真测试（参考 axonhub `lb_simulation_*_test.go`）

## Out of Scope

- ❌ 不切 handler 流量
- ❌ 不实现 model-level circuit breaker（独立组件，本 PR 暂不引入）
- ❌ 不实现 channel limiter（axonhub `channel_limiter.go` 不迁，用户 Q6=A）
- ❌ 不动现有 BlacklistKey / MarkKeyAsFailed / MatchPauseRule

## Technical Notes

### 关键参考

- `axonhub/internal/server/orchestrator/load_balancer.go`
- `axonhub/internal/server/orchestrator/load-balancing.md`（5 策略详细算法）
- `axonhub/internal/server/orchestrator/lb_strategy_*.go`（每个策略实现）
- `axonhub/internal/server/orchestrator/lb_simulation_*_test.go`（仿真测试参考）
- ccx `backend-go/internal/scheduler/`
- ccx `backend-go/internal/metrics/channel_metrics.go`

### 数据缺口

ccx metrics **缺**这些字段（PR2 内补齐）：
- FTTL（首 token 时间）—— 需要在 `internal/handlers/common/stream.go` 第一个 SSE event 时记录
- TPS（tokens/sec）—— 总 token / 总时间
- ActiveConnections —— 在 SendRequest 前后增减
- TraceID 关联的 last successful channel —— 现有 trace affinity 已隐含此信息，提取出来即可

### Promotion 策略（ccx 独有）

```go
type PromotionStrategy struct {
    metricsProvider ChannelMetricsProvider
    boostScore      float64  // default 800
}

func (s *PromotionStrategy) Score(ctx, ch *Channel) float64 {
    if s.metricsProvider.IsPromotionActive(ch.ID) {
        return s.boostScore
    }
    return 0
}
```

优先级介于 TraceAware（1000）和 ErrorAware（200）之间，体现 ccx 业务直觉。

### 风险

- partial sort 库 `github.com/viterin/partial` 是 ccx 新增依赖，可自实现 quickselect 替代
- 6 个策略的权重平衡需要仿真测试验证（参考 axonhub lb_simulation 测试）
- scheduler 改造可能影响现有 promotion / affinity 行为，必须保证现有 e2e 测试通过
