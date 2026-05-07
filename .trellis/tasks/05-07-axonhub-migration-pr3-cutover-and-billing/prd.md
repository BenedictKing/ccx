# PR3: traffic cutover, pricing, NDJSON usage store, channel dashboard

> 父任务: [05-07-axonhub-forwarding-migration](../05-07-axonhub-forwarding-migration/prd.md)
> 依赖: [PR1 pipeline skeleton](../05-07-axonhub-migration-pr1-pipeline-skeleton/prd.md), [PR2 load balancer](../05-07-axonhub-migration-pr2-loadbalancer/prd.md)

## Goal

把 4 个 handler 切到新 pipeline + 新 LoadBalancer；同时引入价格计算 + NDJSON usage 持久化 + 前端 channel dashboard 6 项指标 + cost 展示。

## Why

- PR1/PR2 已铺好骨架，本 PR 是"接通"步骤
- 切流量 + 计费 + UI 是用户可见的核心价值
- 一次 PR 完成完整功能闭环，验收明确

## Requirements

### 1. handler 切流量

```
backend-go/internal/handlers/chat/handler.go
backend-go/internal/handlers/messages/handler.go
backend-go/internal/handlers/responses/handler.go
backend-go/internal/handlers/gemini/handler.go
```

每个 handler 改造：
- 删除 `TryUpstreamWithAllKeys` 调用
- 改用 `pipeline.Factory().Pipeline(inboundAdapter, outboundAdapter, opts...).Process(ctx, req)`
- LoadBalancer 注入到 outboundAdapter 的 `NextChannel` 实现
- ccx key 级机制 (BlacklistKey / MarkKeyAsFailed / MatchPauseRule) 通过 `RawResponseMiddleware` 注入

### 2. ccx key 级 middleware

```
backend-go/internal/pipeline/middleware/
  ccx_key_failure.go            # 实现 RawResponse hook
  ccx_pause_rule.go             # 实现 RawResponse hook
```

逻辑迁移自 `internal/handlers/common/upstream_failover.go` 第 354-482 行（ShouldRetryWithNextKey / matchChannelFailoverRule / MatchPauseRule / BlacklistKey）。

### 3. 价格计算

```
backend-go/internal/pricing/
  price.go                      # ModelPrice / Pricing / PriceTier (搬 axonhub objects/price.go)
  cost.go                       # CostItem / TierCost (搬 axonhub objects/cost.go)
  calculator.go                 # Calculate(usage, price) (totalCost, []CostItem)
  loader.go                     # 加载 prices.json + 文件热重载
  prices.json                   # 内置主流模型价格

  price_test.go
  calculator_test.go
```

支持 3 种计费模式：flat_fee / usage_per_unit / usage_tiered。

prices.json 初版覆盖：
- gpt-4o / gpt-4o-mini / gpt-4-turbo
- claude-3-5-sonnet / claude-3-5-haiku / claude-3-opus
- gemini-2.0-flash / gemini-1.5-pro / gemini-1.5-flash
- o1 / o1-mini / o3-mini

### 4. UsageStore（NDJSON）

```
backend-go/internal/usage/
  store.go                      # UsageStore interface
  record.go                     # UsageRecord schema
  ndjson_store.go               # NDJSON impl: 按日切分 + 保留期清理 + buffered writer
  ndjson_store_test.go

  config.go                     # 路径 / 保留期 / flush 间隔
```

UsageRecord 字段（参考 axonhub usage_log 简化）：
```go
type UsageRecord struct {
    RequestID    string
    Timestamp    time.Time
    ChannelID    int
    ChannelName  string
    APIKeyMasked string
    ModelID      string
    Format       string

    InputTokens              int64
    OutputTokens             int64
    CacheReadInputTokens     int64
    CacheCreationInputTokens int64
    TotalTokens              int64

    TotalCost      *decimal.Decimal
    CostItems      []pricing.CostItem
    PriceVersion   string

    DurationMs int64
    Success    bool
    ErrorCode  string  // optional
}
```

NDJSON 文件命名：`logs/usage/usage-2025-05-07.ndjson`

并发安全：`sync.Mutex + bufio.Writer`，每秒 flush。

保留期：默认 30 天，启动时清理；可通过环境变量 `CCX_USAGE_RETENTION_DAYS` 覆盖。

### 5. metrics 扩展

```
backend-go/internal/metrics/channel_metrics.go
  - 新增 AggregatedMetrics.TotalCost decimal.Decimal
  - 新增 AggregatedMetrics.CacheReadTokensTotal / CacheCreationTokensTotal
  - 现有 RecordRequestFinalizeSuccess(usage) 改成同时写 UsageStore
```

### 6. 后端 API

```
backend-go/internal/handlers/channel_dashboard_handler.go (扩展)
  GET /api/{type}/channels/:id/dashboard
    返回：总请求 / 成功率 / InputTokens / OutputTokens / TotalTokens / CacheRead / CacheWrite / TotalCost
```

### 7. 前端 dashboard 组件

```
frontend/src/features/channels/components/ChannelDashboardCard.tsx
  - 显示 6 项核心指标 + cost
  - 数字格式化（K / M）
  - cost 显示美元符号 + 2 位小数
  - 缺数据时显示 "—"

frontend/src/features/channels/components/ChannelDashboardCard.test.tsx
```

UI 参考用户提供的样式：
```
总请求 18
可用率 100.0%
输入 Token 1.2K
输出 Token 8.0K
总 Token 9.2K
缓存 R/W 读 689.9K 写 589.4K
成本 $0.12
```

## Acceptance Criteria

- [ ] 现有 `internal/handlers/{chat,messages,responses,gemini}` 的 handler_test / matrix_test / failover_test **全部通过**
- [ ] 现有 `BlacklistKey` / `MarkKeyAsFailedWithDuration` / `MatchPauseRule` 测试不回归
- [ ] AxonHub-half.md 第 82-90 行已迁移契约**一条不退**（自动化测试覆盖）
- [ ] 价格计算 3 种模式各有单测，覆盖 5+ 主流模型
- [ ] UsageStore NDJSON 测试：并发写、按日切分、保留期清理、崩溃恢复
- [ ] 前端 ChannelDashboardCard 单测覆盖 6 项指标 + cost + 缺数据 fallback
- [ ] 端到端：发起一次请求 → metrics 更新 → NDJSON 落账 → API 查询 → UI 显示
- [ ] `go test ./... -count=1 -race` 全部通过
- [ ] `git diff --check` 通过
- [ ] frontend `pnpm test` 通过

## Definition of Done

- spec 同步：
  - `.trellis/spec/backend/pricing.md` 新增
  - `.trellis/spec/backend/usage-store.md` 新增
  - `.trellis/spec/backend/quality-guidelines.md` 更新（计费 NDJSON 落账契约）
  - `.trellis/spec/frontend/channel-dashboard.md` 新增
- 文档更新：
  - `backend-go/CLAUDE.md` 新增 pipeline / loadbalance / pricing / usage 模块说明
  - `AxonHub-half.md` 关闭收尾，标注本次完整迁移完成
- 旧代码标记 deprecated 或删除：
  - `TryUpstreamWithAllKeys` 删除（无残留调用）
  - 现有 scheduler `selectChannel` 改造为 LoadBalancer 包装

## Out of Scope

- ❌ feature flag 回退（用户拒绝）
- ❌ SQLite 持久化（接口预留，本 PR 仅 NDJSON impl）
- ❌ channel × model × key 二维明细 dashboard（用户 Q5=A 只要 channel 维度）
- ❌ 配额预扣 / 限流 / model access control（用户 Q6=A 全部不要）
- ❌ 外部价格源同步（用户 Q3=A 内置 JSON）

## Technical Notes

### 关键参考

- PR1 / PR2 输出（pipeline / loadbalance 包）
- `axonhub/internal/objects/price.go` / `cost.go`（直接搬）
- `axonhub/internal/ent/schema/usage_log.go`（字段参考但不搬 ent）
- ccx `backend-go/internal/handlers/common/upstream_failover.go` 第 354-595 行（key 级机制提取到 middleware）
- ccx `backend-go/internal/handlers/{chat,messages,responses,gemini}/handler*_test.go`（验收基线）

### 新增依赖

- `github.com/shopspring/decimal`

### 风险

- handler 切流量是**不可回退**操作，必须有完整 e2e 测试 + 灰度发布
- NDJSON 文件高并发场景下需要压测（1000 req/sec 是否扛得住 mutex）
- 前端组件依赖后端新 API 字段，要协调 schema 一致
- 价格表静态，新模型上线需手动加 prices.json（接受）

### 切流量步骤

1. 实现新 pipeline 适配（PR1 已完成）
2. 实现新 LB（PR2 已完成）
3. 在 handler 中加 `if envCfg.UseAxonHubPipeline { newPath } else { oldPath }` ⚠️ 用户拒绝 feature flag，所以**直接替换**
4. 删除 TryUpstreamWithAllKeys 及其测试中已被新测试覆盖的部分
5. 全量回归测试

### Spec 同步

完工时务必更新：
- `AxonHub-half.md` 标注：本次迁移完整闭环，passthrough/billing/dashboard 全套契约已迁移
- 后端 quality-guidelines.md 增加新 pipeline 行为契约
