# PR2 Phase 1 + Phase 2（6/6 strategy）进度

## 已完成

### 新增包：`backend-go/internal/loadbalance/`

`strategy.go` —— `LoadBalanceStrategy` interface（Score / ScoreWithDebug / Name）+ `Channel` 数据载体（ID / Name / Priority / OrderingWeight / Tags）+ `StrategyScore` / `ChannelDecision` / `DecisionLog` 类型

`metrics_provider.go` —— `ChannelMetricsProvider` interface（13 方法对应 6 strategy 数据维度）+ `RateLimitState` 类型 + `NewNoopMetricsProvider` 单测桩

`context.go` —— `ContextWithRequestedModel` / `RequestedModelFromContext` / `ContextWithRequestStream` / `RequestStreamFromContext` / `ContextWithTraceID` / `TraceIDFromContext`（私有 ctx key 类型，避免外部冲突）

`partial_sort.go` —— 自实现 generic `partialSortTopK[T]`（不引入 `viterin/partial`）+ `decisionLess`（TotalScore 降序 → OrderingWeight 降序 → ID 升序保 stable）

`loadbalancer.go` —— `LoadBalancer` 容器 + `New(provider, strategies...)` + `Sort(ctx, candidates, model, stream, topK)` 生产路径 + `SortWithDebug(...)` 决策日志路径 + `Strategies()` / `Provider()` 访问器

`loadbalancer_test.go` —— 12 个测试覆盖：
- 容器排序（无 strategy / 固定打分求和 / topK 截断 / topK<=0 全返 / 空输入 / 输入不被修改）
- DecisionLog 完整字段填充 + FinalRank 1-based
- partial sort 全排序、top-K 子集、空/单元素/k=0/k<0 边界
- ctx helpers 往返（含空字符串不污染）
- noop provider 13 方法全零值

### 测试结果

```
go vet ./...                       # 全过
go test ./... -count=1             # 23 包全过（新增 internal/loadbalance）
```

PR1 hard constraint 仍然保持：4 个 handler.go 与所有 *_test.go 一字未改。

## 待办（PR2 Phase 2 剩余 + Phase 3）

### Phase 2 已完成（6/6 strategy）

- ✅ `strategy_promotion.go` —— PromotionStrategy（boost 0/800，ccx 独有）
- ✅ `strategy_trace.go` —— TraceAwareStrategy（hit 0/1000，从 ctx 取 traceID）
- ✅ `strategy_weight_rr.go` —— WeightRoundRobinStrategy（10-150，`maxScore × exp(-effective/scaling)`，effective = capped/weightFactor × decayMultiplier）
- ✅ `strategy_error.go` —— ErrorAwareStrategy（0-200，`maxScore - failures×30×ratio - 40×ratio`，ratio 按 5min 线性衰减）
- ✅ `strategy_latency.go` —— LatencyAwareStrategy（0-80，stream: 0.7×FTTL + 0.3×TPS；non-stream: E2E）
- ✅ `strategy_ratelimit.go` —— RateLimitAwareStrategy（-10000~100，限流 cooldown 期硬负分 + ActiveConnections 衰减）
- 配套：`strategy_test.go`（10 测试 Promotion+Trace+集成）+ `strategy_wrr_error_test.go`（13 测试 WRR+Error+4-strategy 集成）+ `strategy_latency_ratelimit_test.go`（13 测试 Latency+RateLimit+6-strategy 联动）

### Phase 2 不依赖具体 metrics 数据

LatencyAware / RateLimitAware 仅依赖 `ChannelMetricsProvider` 接口（已就位），单测用 `fakeMetricsProvider` 注入数据。具体 metrics 字段（FTTL / TPS / ActiveConnections）的数据来源是 Phase 3 实现 provider 时填充。

### Phase 3：合并到 PR3 处理（不在 PR2 范围）

**决定时间**：2026-05-07，本轮收尾时与 skip 对齐。

**理由**：
- Phase 3 的 scheduler 改造 + metrics 扩展涉及 ccx 现有 `(baseURL, apiKey, serviceType)` tuple 模型与 axonhub `int channelID` 模型之间的桥接，本身就是大改造
- 若 PR2 内做 Phase 3，则期间 SelectChannel 必须同时维护"旧路径 + 新路径"两条分支（用户已拒绝 feature flag），中间状态价值低
- PR3 本来就要"删除 TryUpstreamWithAllKeys"（PR3 PRD 第 211 行），与 scheduler 拆解强相关
- 合并到 PR3 一次性完成"scheduler 改造 + metrics 扩展 + handler 切流量 + key 级 middleware + pricing + UsageStore + dashboard"，verification 路径更清晰

**迁移到 PR3 的项**：
- `internal/scheduler/lb_metrics_provider.go` —— ChannelMetricsProvider 实现
- `internal/scheduler/channel_scheduler.go` 拆解：候选过滤 + `LoadBalancer.Sort` 排序
- `internal/metrics/channel_metrics.go` 字段扩展（FTTL / TPS / ActiveConnections）
- `internal/handlers/common/stream.go` 首 SSE event 计时 hook

PR3 PRD 已在 Out of Scope 上方追加备注（见 PR3 PRD 末尾）。

### 不在 PR2 范围内（PR3 处理）

- handler 切流量
- ccx key 级 BlacklistKey/MarkKeyAsFailed/MatchPauseRule middleware
- 价格计算 + UsageStore + dashboard
- **新增**：scheduler 改造（候选过滤 + LB.Sort 接入）
- **新增**：metrics 字段扩展（FTTL / TPS / ActiveConnections）+ stream.go 首事件计时

## 关键设计决策记录

### 1. `Channel` 不直接持有 `*config.UpstreamConfig`

理由：
- config 字段过多，strategy 仅需 ID / Name / Priority / OrderingWeight / Tags 几个维度
- scheduler 改造（Phase 3）会把 `*config.UpstreamConfig` 投影到 `Channel`，避免 strategy 与配置耦合，便于单测构造

### 2. partial sort 自实现，不引入 `viterin/partial`

按 PRD 风险条目第 145 行的备选方案。算法选择：当 K << N 时（典型 LB 决策场景：N = 数十个，K = 1-3），简单 K 轮 selection sort（O(N·K)）的实现绝对常数低、易于正确性验证。N 增大到 1000+ 时若需要再换 quickselect，接口不变。

### 3. `decisionLess` 用三级排序键保 stable

主键 TotalScore 降序，副键 OrderingWeight 降序，末键 ID 升序。这样：
- 总分相同时优先选权重大的（与 ccx 现状对齐）
- 权重也相同时按 ID 给确定性结果，便于单测复现
- 避免 Go `sort` 不稳定带来的间歇性测试 flake

### 4. `Sort` 不修改输入 candidates 切片

把候选放到内部 `[]ChannelDecision` 临时切片做排序；调用方传入的 `[]*Channel` 顺序保持不变。这是 axonhub 的做法。

### 5. noop metrics provider 是默认填充

`New(nil, ...)` 自动注入 `NewNoopMetricsProvider()`，让骨架测试与 Phase 2 渐进集成都不需要 mock 完整 13 方法。

### 6. `time.Since` 在 Windows 下可能为 0

DecisionLog.TotalDuration 在 16ms 时钟分辨率下对单次微秒级排序可能取到 0；测试只断言 `>= 0`。

## 下一步

进入 PR2 Phase 2：实现 6 个 strategy。建议按依赖度从低到高顺序：
1. PromotionStrategy（仅依赖 IsPromotionActive，最简单）
2. TraceAware（仅依赖 LastSuccessfulChannelByTrace + traceID ctx）
3. WeightRR（依赖 OrderingWeight + RequestCount）
4. ErrorAware（依赖 ConsecutiveFailures + LastFailureTime + 时间衰减算法）
5. LatencyAware（依赖 FTTL/TPS/E2E，需要 metrics 扩展）
6. RateLimitAware（依赖 RateLimitState + ActiveConnections，需要 metrics 扩展）
