# axonhub forwarding migration with billing and channel metrics

## Goal

将 AxonHub（`axonhub/` 目录下项目）的流量转发逻辑**逐字节地**迁移到 ccx 主项目（`backend-go/`），并在此基础上叠加：
1. **价格计算**（usage → cost，支持多模型价格表）
2. **每渠道 dashboard 指标**（总请求 / 可用率 / 输入 token / 输出 token / 总 token / 缓存读写等）

同时保留 ccx 现有的"拉黑（BlacklistKey）+ 冷却（MarkKeyAsFailedWithDuration）+ 暂停规则（MatchPauseRule）"机制，不引入 AxonHub 的 model-level circuit breaker / channel rate limiter。

## What I already know

### CCX 现状

- 入口层：`internal/handlers/{chat,messages,responses,gemini}/handler.go` 各一个 Gin handler。
- 转发核心：`internal/handlers/common/upstream_failover.go` 的 `TryUpstreamWithAllKeys` 按 BaseURL × Key 二维循环做 failover。
- 请求构造：`internal/forwarding/builder.go` 的 `Build()`。
- 透传决策：`internal/passthrough/passthrough.go` 的 `Decide()`，仅以 inbound/outbound API format 一致性决定是否 raw passthrough。
- 调度：`internal/scheduler/` 多渠道（promotion / priority / trace affinity / circuit filter）。
- 指标：`internal/metrics/channel_metrics.go` 内存聚合，已支持 cache token 拆分。
- 存储：JSON/YAML 配置热重载，**无数据库**。
- 拉黑冷却：`MarkKeyAsFailed` / `MarkKeyAsFailedWithDuration` / `BlacklistKey` / `MatchPauseRule` 一套 key 级机制。

### AxonHub 现状

- 核心在 `internal/server/orchestrator/`（27341 行）：完整 inbound → unified → outbound pipeline。
- 7 种 LB 策略 + partial sort 打分：trace-aware / error-aware / RR / conn-aware / weight / model-CB / random。
- 配额预扣：`candidates_quota.go` + `provider_quota_status` 表。
- usage 持久化：`internal/ent/schema/usage_log.go`（Ent ORM + SQLite）。
- 依赖注入：FX 框架。
- pipeline 内：`llm/pipeline/pipeline.go` + `executor.go` + `stream/` + `middleware.go`。
- 转发决策：`orchestrator/pass_through.go` 等。
- `AxonHub-half.md` 记录了上一轮已经从 AxonHub 迁移过来的**子集**契约（passthrough / raw stream fan-out / header override / user-agent / sensitive header strip / cached-token metrics）。

### 已有迁移子集（不要重复做）

`AxonHub-half.md` 第 82-90 行、100-286 行列出上一轮已落地的内容：
- 协议一致性自动 passthrough
- raw stream fan-out / attempt cleanup
- header override 顺序契约
- User-Agent passthrough 策略
- sensitive inbound header stripping
- Chat/Gemini/Responses same-format raw passthrough 的 cached token metrics 旁路

### 关键冲突点

1. **存储模型**：AxonHub 用 Ent + SQLite 持久化每个 RequestExecution，ccx 无数据库（都在内存 metrics）。
2. **熔断粒度**：AxonHub 是 model 级 circuit breaker + channel rate limiter；ccx 是 key 级 BlacklistKey / MarkKeyAsFailed。
3. **DI 框架**：AxonHub 是 FX + 多 provider 注册；ccx 是 Gin handler 直接组合。
4. **RequestExecution 持久化**：AxonHub pipeline 会把每次 attempt 落 DB；ccx 目前只有内存 metrics + channel log。
5. **LB 策略**：AxonHub 是 partial sort 7 策略打分；ccx 是 priority + affinity + warmup 延迟排序。

## Assumptions (temporary)

- **A1**：~~用户说"100% 逻辑一样"指的是**流量转发行为**~~ ✅ **已确认（Q1=A）**：行为层面对齐，只搬 pipeline/transformer/LB 算法核心为纯 Go 函数和接口，不引入 FX/Ent/SQLite。保留 ccx 现有 Gin handler + 内存配置架构。
- **A2**：~~用户保留 ccx 的拉黑 / 冷却 / 暂停规则 == 显式拒绝 AxonHub 的 model-CB / channel limiter~~ ✅ **已确认（Q2=B）**：两者并存。AxonHub model-CB / channel limiter 管 channel 级 model 健康（选 channel 时跳过），ccx key 级管单 key 冷却拉黑暂停。两层独立，叠加生效。
- **A3**：价格计算不需要多租户配额预扣（AxonHub 的 quota 体系），只要"usage → cost，UI 能展示成本"。
- **A4**：渠道 dashboard 指标的数据源可以是 ccx 现有的 `channel_metrics.go` 内存聚合，不强制要求持久化。

以上 4 条需要 Blocking 确认。

## Open Questions

（已全部解决，进入实施）

## Requirements (evolving)

### MVP 范围（已确认）

✅ 核心需求 3 项：流量转发行为对齐 + 价格计算 + channel dashboard 6 项指标
✅ 预留 `UsageStore` interface（当前 NDJSON 实现，未来可切 SQLite）
✅ NDJSON 边界处理：按日切分 + 过期自动清理 + 并发写 buffered writer
❌ 不加 feature flag 回退（一次性切换）

### 已分类
- 迁移 AxonHub 流量转发，行为层面 100% 一致
- 增加价格计算（内置 JSON 价格表，缺失模型 cost=nil）
- 增加每渠道 dashboard 展示：总请求、可用率、输入 token、输出 token、总 token、缓存 R/W、cost
- 保留 ccx 现有的 key 级 blacklist / cooldown / pause rule
- 保留 AxonHub 的 model-CB / channel limiter（与 ccx key 级并存）
- 不破坏 `AxonHub-half.md` 里已落地的迁移子集
- 不引入 axonhub 的 FX / Ent / 配额预扣 / model access control / GraphQL

### 待分类
- 迁移 AxonHub 流量转发，行为层面 100% 一致
- 增加价格计算
- 增加每渠道 dashboard 展示：总请求、可用率、输入 token、输出 token、总 token、缓存 R/W 等
- 保留 ccx 现有的 key 级 blacklist / cooldown / pause rule
- 不破坏 `AxonHub-half.md` 里已落地的迁移子集

## Acceptance Criteria (evolving)

- [ ] 迁移后流量转发的所有单测（chat/messages/responses/gemini 的 handler_test、matrix_test、failover_test）继续通过
- [ ] 新增价格计算单测，覆盖主流模型（GPT/Claude/Gemini）的 usage → cost 换算
- [ ] 渠道 dashboard UI 能显示 6 项核心指标（总请求 / 可用率 / 输入 token / 输出 token / 总 token / 缓存 R/W）
- [ ] ccx 的 BlacklistKey / MarkKeyAsFailedWithDuration / MatchPauseRule 测试全部继续通过
- [ ] AxonHub-half.md 第 82-90 行列出的已迁移契约一条不回退
- [ ] `go vet ./...` / `go test ./...` / `git diff --check` 通过

## Definition of Done

- 所有 handler 覆盖 raw passthrough + cross-format 两条路径的单测
- metrics 新增字段（cost）有单测
- 前端渠道 dashboard 有组件测试 / E2E（如果已有 E2E 体系）
- `.trellis/spec/backend/*.md` 同步新契约
- 迁移前后行为差异有明确文档（逐步回退策略）

## Out of Scope (explicit)

- **明确排除**：完整 FX DI 框架迁移（侵入性太大）
- **明确排除**：完整 Ent ORM + SQLite 依赖（除非 Q4 选 b）
- **明确排除**：多租户配额预扣 / API key profile / model access control（除非 Q6 明确要）
- **明确排除**：AxonHub 的 GraphQL 管理面（ccx 保持自己的 REST）
- **明确排除**：RequestExecution 持久化（每条请求落 DB）

## Technical Notes

### 参考文件

**CCX 侧**：
- `backend-go/internal/handlers/common/upstream_failover.go`（failover 核心）
- `backend-go/internal/handlers/chat/handler.go`
- `backend-go/internal/handlers/messages/handler.go`
- `backend-go/internal/handlers/responses/handler.go`
- `backend-go/internal/handlers/gemini/handler.go`
- `backend-go/internal/forwarding/builder.go`
- `backend-go/internal/passthrough/passthrough.go`
- `backend-go/internal/metrics/channel_metrics.go`
- `backend-go/internal/scheduler/`
- `AxonHub-half.md`（上一轮迁移契约）

**AxonHub 侧**：
- `axonhub/internal/server/orchestrator/orchestrator.go`
- `axonhub/internal/server/orchestrator/pass_through.go`
- `axonhub/internal/server/orchestrator/inbound.go`
- `axonhub/internal/server/orchestrator/outbound.go`
- `axonhub/internal/server/orchestrator/request_execution.go`
- `axonhub/internal/server/orchestrator/state.go`
- `axonhub/internal/server/orchestrator/retry.go`
- `axonhub/internal/server/orchestrator/load_balancer.go`
- `axonhub/internal/server/orchestrator/lb_strategy_*.go`（7 个策略）
- `axonhub/internal/server/orchestrator/candidates*.go`
- `axonhub/internal/server/orchestrator/model_circuit_breaker.go`
- `axonhub/llm/pipeline/pipeline.go`
- `axonhub/llm/pipeline/stream.go`
- `axonhub/llm/pipeline/executor.go`
- `axonhub/llm/pipeline/middleware.go`
- `axonhub/internal/ent/schema/usage_log.go`
- `axonhub/internal/server/biz/channel_metrics.go`

### 初步判断

如果 A1-A4 成立，推荐路径是：
1. 以 ccx 现有 handler 骨架为底
2. 把 AxonHub 的 pipeline/transformer/LB 策略**抽取成纯函数/接口**，不带 FX / Ent
3. 插入到 ccx 的 `TryUpstreamWithAllKeys` 之内或之上
4. 价格表用 JSON 文件（`config/prices.json` 或内嵌）
5. dashboard 指标从现有 `channel_metrics.go` 扩展字段
6. 拉黑冷却逻辑 100% 保持在 ccx，不引入 model-CB

## Research References

- [`research/topic-a-pipeline-architecture.md`](research/topic-a-pipeline-architecture.md) — AxonHub pipeline 核心 = Inbound/Outbound transformer + 7 类 middleware + 双层 retry（cross-channel + same-channel）
- [`research/topic-b-load-balancer.md`](research/topic-b-load-balancer.md) — 5 个 LB 策略（TraceAware / ErrorAware / WeightRR / LatencyAware / RateLimitAware），加权打分 + partial sort
- [`research/topic-c-pricing-and-billing.md`](research/topic-c-pricing-and-billing.md) — ModelPrice 数据模型（flat_fee / usage_per_unit / usage_tiered）+ decimal 库 + UsageStore NDJSON impl

## Feasible Approaches

### Approach A：渐进式重构（推荐）

**分 3 个 PR 叠加交付，每个 PR 独立可合并、可回滚。**

```
PR1: 引入 pipeline 骨架（不改现有 handler 行为）
  - 新增 internal/pipeline/（transformer/middleware/pipeline/state）
  - 新增 internal/llm/（中间请求格式）
  - 新增 4 个 Outbound adapter（现有 buildProviderRequest 包装）
  - 新增 1 个 Inbound adapter per handler
  - handler 先保持旧路径，新 pipeline 有完整单测但不接入流量
  - 验证：全部 handler_test / matrix_test 通过

PR2: LB 策略 + scheduler 整合
  - 新增 internal/loadbalance/（LoadBalancer + 5 strategies + partial sort）
  - 改造 scheduler：把 promotion / priority / affinity / circuit 改成 strategies
  - 保留 ccx key 级 BlacklistKey/MarkKeyAsFailed/MatchPauseRule 独立（不迁进 LB）
  - 验证：multi_channel_failover_test + scheduler test 通过

PR3: 切流量 + 价格计算 + dashboard
  - handler 切到新 pipeline（删除 TryUpstreamWithAllKeys 或改成私有）
  - 新增 internal/pricing/（price.go / cost.go / calculator.go / prices.json）
  - 新增 internal/usage/（UsageStore + NDJSON impl + 日切分 + 保留期清理）
  - metrics 层加 cost 字段
  - 前端新增 channel dashboard 组件（6 指标 + cost）
  - 验证：端到端 + 前端 E2E
```

**优点**：
- 每个 PR 可独立 review / 合并 / 回滚
- PR1 合并后新 pipeline 有完整测试但没切流量，风险零
- PR3 一把切换，前后行为对齐有完整测试保证

**缺点**：
- PR1 会引入看似"死代码"（新 pipeline 存在但没流量）
- 3 个 PR 至少 2-3 周

### Approach B：一次性大迁移

一个 PR 包含所有改动：pipeline + LB + pricing + usage + dashboard。

**优点**：
- 一次到位，无中间状态

**缺点**：
- 单 PR 预计 3000+ 行，难以 review
- 回滚只能全退
- 任何一处出 bug 都会阻塞全部

### Approach C：仅迁 pipeline，保留 scheduler

只做 PR1 + PR3 的 pricing/dashboard 部分，**不动 scheduler / LB**。ccx 的 scheduler 继续管 channel 选择，pipeline 只管 "channel 内的 attempt 生命周期"。

**优点**：
- 工作量最小，最快交付
- 风险最低

**缺点**：
- 你说的"axonhub 流量转发 100% 一致"中 LB 部分没做到，仅 pipeline 对齐
- 未来还要再做一次 LB 迁移

## Decision (ADR-lite)

**Context**: 用户希望把 axonhub 流量转发逻辑迁移到 ccx 主项目，并叠加价格计算和每渠道 dashboard 指标。axonhub 完整架构含 FX/Ent/SQLite/RequestExecution，工作量极大；ccx 现有 key 级 BlacklistKey/MarkKeyAsFailed/MatchPauseRule 必须保留。

**Decision**: Approach A 渐进式 3 PR：
- **PR1**：引入 pipeline 骨架（transformer/middleware/state），新 pipeline 完整单测但不切流量
- **PR2**：LB 策略 + scheduler 整合（5 个 axonhub strategy + 保留 ccx promotion）
- **PR3**：切流量 + 价格计算 + UsageStore（NDJSON）+ dashboard 6 指标

**Consequences**:
- 优点：每 PR 独立 review/合并/回滚；2-3 周可交付完整功能；前后行为对齐有完整测试保证
- 风险：PR1 引入"死代码"（新 pipeline 暂时不接流量）；PR3 切换瞬间需要全量回归测试
- 不可回退：PR3 合并后删除 `TryUpstreamWithAllKeys`，无法回到旧路径（用户拒绝 feature flag）

## Technical Notes

### 参考文件

**CCX 侧**：
- `backend-go/internal/handlers/common/upstream_failover.go`（failover 核心）
- `backend-go/internal/handlers/chat/handler.go`
- `backend-go/internal/handlers/messages/handler.go`
- `backend-go/internal/handlers/responses/handler.go`
- `backend-go/internal/handlers/gemini/handler.go`
- `backend-go/internal/forwarding/builder.go`
- `backend-go/internal/passthrough/passthrough.go`
- `backend-go/internal/metrics/channel_metrics.go`
- `backend-go/internal/scheduler/`
- `AxonHub-half.md`（上一轮迁移契约）

**AxonHub 侧**：
- `axonhub/internal/server/orchestrator/orchestrator.go`
- `axonhub/internal/server/orchestrator/pass_through.go`
- `axonhub/internal/server/orchestrator/inbound.go`
- `axonhub/internal/server/orchestrator/outbound.go`
- `axonhub/internal/server/orchestrator/request_execution.go`
- `axonhub/internal/server/orchestrator/state.go`
- `axonhub/internal/server/orchestrator/retry.go`
- `axonhub/internal/server/orchestrator/load_balancer.go`
- `axonhub/internal/server/orchestrator/lb_strategy_*.go`（7 个策略）
- `axonhub/internal/server/orchestrator/candidates*.go`
- `axonhub/internal/server/orchestrator/model_circuit_breaker.go`
- `axonhub/llm/pipeline/pipeline.go`
- `axonhub/llm/pipeline/stream.go`
- `axonhub/llm/pipeline/executor.go`
- `axonhub/llm/pipeline/middleware.go`
- `axonhub/internal/ent/schema/usage_log.go`
- `axonhub/internal/objects/price.go`
- `axonhub/internal/objects/cost.go`

### 新增依赖

- `github.com/shopspring/decimal` —— 价格计算精度
- （可选）`github.com/viterin/partial` —— LB partial sort，可自实现替代

### 初步判断

推荐 Approach A（渐进式重构）：
1. 以 ccx 现有 handler 骨架为底
2. 把 AxonHub 的 pipeline/transformer/LB 策略**抽取成纯函数/接口**，不带 FX / Ent
3. 插入到 ccx 的 `TryUpstreamWithAllKeys` 之内或之上
4. 价格表用 JSON 文件（`config/prices.json` 或内嵌）
5. dashboard 指标从现有 `channel_metrics.go` 扩展字段
6. 拉黑冷却逻辑 100% 保持在 ccx，不引入 model-CB

### 当前临时假设有效，无需用户回复
（已无遗留假设——A1-A4 均已通过 Q1-Q6 转为确认事实）
