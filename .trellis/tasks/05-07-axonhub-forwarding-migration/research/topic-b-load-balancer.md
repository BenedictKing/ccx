# Research Topic B: AxonHub Load Balancer 7 策略

## 文件
- `axonhub/internal/server/orchestrator/load_balancer.go`
- `axonhub/internal/server/orchestrator/load-balancing.md`
- `axonhub/internal/server/orchestrator/lb_strategy_*.go`

## 核心抽象

```go
type LoadBalanceStrategy interface {
    Score(ctx, channel) float64                                  // 0-1000
    ScoreWithDebug(ctx, channel) (float64, StrategyScore)        // 同 Score + 调试细节
    Name() string
}
```

LoadBalancer = 多策略加权打分 + partial sort（top-k 选择，避免全排序开销）。

## 7 策略 × 分数范围

| 优先级 | 策略 | 分数范围 | 用途 |
|------|-----|---------|------|
| 1 | TraceAware | 0 / 1000 | 同 trace 上次成功的 channel 得满分 |
| 2 | ErrorAware | 0-200 | 连续失败 -30/次，最近失败 -40，按冷却期线性衰减 |
| 3 | WeightRoundRobin | 10-150 | 历史请求计数指数衰减（roundrobin）+ 管理员权重 |
| 4 | LatencyAware | 0-80 | 流式 FTTL/TPS、非流式 e2e latency |
| 5 | RateLimitAware | -10000-100 | 限流命中 -10000，并发饱和 -10000 |

⚠️ axonhub 文档中 README 说有 7 种，实际 doc 列出的是上述 5 种 + ConnectionAware（已 deprecated 合并入 RateLimitAware）+ Random（tie-break）。

## 默认组合（DefaultChannelSelector）

```go
NewLoadBalancer(
    NewTraceAwareStrategy(requestService),
    NewErrorAwareStrategy(channelService),
    NewWeightRoundRobinStrategy(channelService),
    NewLatencyAwareStrategy(channelService),
    NewRateLimitAwareStrategy(rateLimitTracker, connectionTracker),
)
```

总分范围：~-9790-1530。Trace 在场景里几乎主导（1000 分上下）。

## 与 ccx 现状对比

ccx 现有调度（`internal/scheduler/`）：
- 促销渠道优先（axonhub 没有等价物）
- Priority 字段排序（≈ axonhub WeightStrategy 静态部分）
- Trace 亲和性（≈ TraceAwareStrategy 但 ccx 是布尔过滤而非加分）
- 熔断状态过滤（≈ axonhub ErrorAwareStrategy 但 ccx 是直接跳过 Open 状态，不是减分）
- BaseURL 延迟排序（warmup，≈ axonhub LatencyAware）

差异：
- ccx 用**多级硬过滤**（promotion → priority → affinity → circuit），axonhub 用**加权打分**
- ccx 是 BaseURL × Key 二维 failover；axonhub 是 channel × candidate 选择
- ccx 没有 RateLimit/Connection 维度
- ccx 没有 partial sort 优化

## 行为对齐迁移点

### 必须迁移
1. **LoadBalanceStrategy 接口 + LoadBalancer 容器**
2. **5 个 strategy 实现**（TraceAware / ErrorAware / WeightRoundRobin / LatencyAware / RateLimitAware）
3. **partial sort（top-k）**：用 `github.com/viterin/partial` 或自实现

### 与 ccx 现有 scheduler 的整合
- ccx 的 promotion 概念 axonhub 没有 → 加一个 `NewPromotionStrategy`（boost: 800 分），优先级介于 Trace 和 Error 之间
- ccx 的 trace 亲和（布尔）改造成 TraceAwareStrategy（1000 分加权）
- ccx 的 BaseURL warmup 与 axonhub LatencyAware 重叠 → 把 warmup 数据接入 LatencyAware
- ccx 的 circuit breaker（key 级）保留独立，**LB 阶段不做 key 级过滤**，只在 attempt 内做（保留 ccx 现有逻辑）

### 数据需求
为支持 5 个 strategy，channel metrics 必须能提供：
- `LastSuccessfulChannelByTrace(traceID)` → channel ID
- `ConsecutiveFailures(channelID)` / `LastFailureTime` / `FailureWindow`
- `RequestCount(channelID, since)` / `OrderingWeight`
- `FTTL` / `TPS` / `E2ELatency`（按 channel + model）
- `RateLimitState(channelID)` / `ActiveConnections(channelID)`

ccx 的 `metrics.AggregatedMetrics` **大部分都已有**（参见 `channel_metrics.go`），缺的是：
- FTTL（首 token 时间）和 TPS（tokens/sec）—— ccx 已有 latency 但未细分
- ActiveConnections —— ccx 没显式跟踪

## 风险

- **5 个 strategy 都迁移**工作量很大（每个 100-300 行），可分阶段：
  - **MVP**：先迁 TraceAware + ErrorAware + WeightRoundRobin（保留 ccx 现有逻辑作为 fallback）
  - **后续**：再迁 LatencyAware + RateLimitAware
- **Promotion 是 ccx 独有概念**，axonhub 没有；要决定加进 LB 还是保留为前置过滤
- **LoadBalancer 阶段不做 key 级**，避免与 ccx 现有 BlacklistKey 冲突
