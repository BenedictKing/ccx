# PR3 新会话开启提示词

> 把下方"提示词正文"整段复制到新 Claude Code 会话的第一条消息。

---

## 会话背景

当前仓库 `C:\Users\Blue\Desktop\learn-ai\ccx`，分支 `axonhub`（已领先 origin/main 90+ commit）。PR1（#28 pipeline + adapters）和 PR2（#29 loadbalancer，depends on #28）已开。**本会话只做 PR3**：把 4 个 handler 切到新 pipeline + LoadBalancer，加 ccx key 级 middleware、pricing、UsageStore、channel dashboard。PR3 PRD 已吸收 PR2 Phase 3（scheduler 改造 + metrics 扩展）。

PR3 ~6000 LOC，单会话主对话顺序写完不现实，**必须**用 trellis sub-agent 并行派发。

---

## 提示词正文（复制以下整段）

```
你正在推进 trellis 任务 05-07-axonhub-migration-pr3-cutover-and-billing。

【启动动作】
1. git status / git log -10 看 axonhub 分支当前状态。
2. 读这 4 份文件，吃透上下文：
   - .trellis/tasks/05-07-axonhub-migration-pr3-cutover-and-billing/prd.md（含 Section 0 PR2 Phase 3 已并入清单）
   - .trellis/tasks/05-07-axonhub-migration-pr1-pipeline-skeleton/progress.md（PR1 已落地结构与 hard constraints 已打破点）
   - .trellis/tasks/05-07-axonhub-migration-pr2-loadbalancer/progress.md（PR2 已落地 loadbalance 包结构）
   - .trellis/spec/backend/pipeline-architecture.md（pipeline + adapter + fan-out + 空流检测契约）
3. python3 ./.trellis/scripts/task.py start 05-07-axonhub-migration-pr3-cutover-and-billing
   并把 task.json 的 status 从 planning 改为 in_progress。

【工作风格强制要求】
- 全程通过 Task 工具派 trellis-implement / trellis-check / trellis-research sub-agent 完成代码工作
- 主会话只做：读 PRD、拆任务、派 sub-agent、串结果、最终 trellis-update-spec
- 主会话禁止直接 Edit/Write 业务代码（trellis 元数据、PRD、progress.md 例外）
- 每个 sub-agent 任务范围 ≤ 1 个独立模块或 ≤ 2 个 handler
- sub-agent 完成后立刻派 trellis-check 验证，pass 才推下一个

【可并行 sub-agent 工作单元（按依赖度排）】

T1 — metrics 扩展（前置）
  输入：现有 internal/metrics/channel_metrics.go
  产出：新增 FTTL / TPS / ActiveConnections 字段 + Record 方法 + 单测
  约束：现有 channel_metrics 测试不回归
  依赖：无；其他工作的前置

T2 — stream.go 首事件计时 hook
  输入：internal/handlers/common/stream.go（PR1 期间未动）
  产出：第一个 SSE event 时调 metrics.RecordFirstToken
  依赖：T1

T3 — scheduler.lb_metrics_provider.go（PR2 Phase 3）
  输入：现有 internal/scheduler/channel_scheduler.go + internal/loadbalance/ 接口
  产出：把 (baseURL, apiKey, serviceType) tuple 模型 ↔ int channelID 桥接
        实现 loadbalance.ChannelMetricsProvider 13 方法
  依赖：T1

T4 — scheduler 拆解
  输入：T3 完成
  产出：channel_scheduler.go 拆出"候选过滤"+"LB.Sort 排序"两段
        现有 SelectChannel 暂保留（handler 切完后再删）
  依赖：T3

T5 — ccx key 级 middleware
  输入：internal/handlers/common/upstream_failover.go 第 354-595 行
        internal/pipeline/middleware.go 9-hook 接口
  产出：internal/pipeline/middleware/ 下 ccx_key_failure.go + ccx_pause_rule.go
        实现 RawResponse hook，迁移 BlacklistKey / MarkKeyAsFailed / MatchPauseRule
  依赖：无（独立）

T6 — pricing 包（PR3 §3）
  输入：axonhub/internal/objects/price.go + cost.go（直接搬）
  产出：internal/pricing/{price,cost,calculator,loader,prices.json,*_test}.go
        支持 flat_fee / usage_per_unit / usage_tiered 三种计费
  依赖：无（独立，可与 T1-T5 并行）

T7 — UsageStore NDJSON（PR3 §4）
  输入：PRD 第 79-101 行 UsageRecord schema
  产出：internal/usage/{store,record,ndjson_store,config,*_test}.go
        sync.Mutex + bufio.Writer + 按日切分 + 30 天保留期清理
  依赖：T6（UsageRecord 引用 pricing.CostItem）

T8 — handler 切流量（4 个 handler，硬核区！）
  输入：T1-T7 全部完成；4 个 handler.go
  产出：每个 handler 删除 TryUpstreamWithAllKeys 调用
        改用 pipeline.Factory.Pipeline(in, out, ...).Process(ctx, req)
        LoadBalancer 注入到 outboundAdapter 的 NextChannel 实现
        ccx key 级 middleware 通过 WithMiddlewares 注入
  约束：现有 handler_test / matrix_test / failover_test 全部通过
        AxonHub-half.md 第 82-90 行已迁移契约一条不退（自动化测试覆盖）
  依赖：T1-T7

T9 — 后端 dashboard API（PR3 §6）
  输入：T7 完成；现有 channel_dashboard_handler.go
  产出：扩展 GET /api/{type}/channels/:id/dashboard 返回总请求/可用率/各 token/cost
  依赖：T7

T10 — 前端 ChannelDashboardCard
  输入：PRD 第 130-148 行 UI 样式
  产出：frontend/src/features/channels/components/ChannelDashboardCard.{tsx,test.tsx}
        显示 6 项核心指标 + cost；K/M 数字格式化；缺数据 "—" fallback
  依赖：T9（API 契约）

【并行/串行调度建议】
第 1 轮（并行派 4 个 sub-agent）：T1, T5, T6, T10 (用 mock API 数据)
第 2 轮（T1 完成后并行）：T2, T3
第 3 轮：T4
第 4 轮：T7
第 5 轮：T8（最关键，单个 sub-agent 处理 1-2 个 handler，分批派）
第 6 轮：T9（连接前端 mock 到真实 API）

【硬约束（任何 sub-agent 必须遵守）】
- AxonHub-half.md 第 82-142 行已锁定契约一条不退：
  - raw passthrough fan-out 是 attempt 级状态
  - retry/failover 前必须 cancel + close body + wait fan-out goroutine
  - User-Agent passthrough 独立于 body/response passthrough
  - custom headers 必须在 final selected auth 之前应用
  - sensitive inbound header 统一剥离
- 现有 BlacklistKey / MarkKeyAsFailedWithDuration / MatchPauseRule 行为不回归
- 价格计算 3 种模式各有单测，覆盖 5+ 主流模型
- UsageStore 在 1000 req/sec 压力下不丢账（mutex + buffered writer 验证）
- 端到端：发起一次请求 → metrics 更新 → NDJSON 落账 → API 查询 → UI 显示

【测试与提交节奏】
- 每个 sub-agent 完成后：trellis-check 验证 → 通过才 commit
- commit 粒度：T1-T10 各自一个 commit；T8 按 handler 拆 4 个 commit
- 全部 sub-agent 完成后：go vet ./... + go test ./... -count=1 -race（cgo 不可用时去 -race） 全过
- frontend pnpm test 全过
- 最终 trellis-update-spec 同步 spec：
  - .trellis/spec/backend/pricing.md（新增）
  - .trellis/spec/backend/usage-store.md（新增）
  - .trellis/spec/backend/quality-guidelines.md（更新，加计费 NDJSON 落账契约）
  - .trellis/spec/frontend/channel-dashboard.md（新增）
  - backend-go/CLAUDE.md（加 pipeline / loadbalance / pricing / usage 模块说明）
  - AxonHub-half.md（标注本次迁移完成）

【完成定义】
- PR3 全部 acceptance criteria 通过（PRD 第 152-161 行）
- 删除 TryUpstreamWithAllKeys 及其专属测试中已被新测试覆盖的部分
- 旧 SelectChannel 改造为 LoadBalancer 包装
- 不留 deprecated 标记（用户拒绝 feature flag，直接替换）
- 准备 push axonhub-pr3 分支 + 在 BenedictKing/ccx 上开 PR3，依赖 #28 #29

开工前先派 1 个 trellis-research sub-agent 通读 axonhub/llm/middleware/ + axonhub/internal/objects/{price,cost}.go + ccx 现有 upstream_failover.go 第 354-595 行，把"哪些行为可直接搬、哪些需要 ccx 适配"列成一份 research 笔记到 .trellis/tasks/05-07-axonhub-migration-pr3-cutover-and-billing/research/ 下。这份笔记是 T5/T6 的输入。
```

---

## 用法说明

1. 把上面 ``` 包裹的整段提示词复制
2. 新 Claude Code 会话第一条消息粘贴
3. 让它跑

提示词包含：
- 启动动作（git/PRD/spec 阅读 + 任务标记 in_progress）
- 强制 sub-agent 派发（拒绝主对话写代码）
- 10 个独立 sub-agent 工作单元（T1-T10）+ 输入/产出/约束/依赖
- 6 轮调度顺序（第 1 轮 4 个并行起步）
- 硬约束（AxonHub-half.md 契约 + 行为不回归）
- 测试 + commit 粒度 + spec 同步要求
- "开工前先派 trellis-research"作为引子，让会话自动进入 sub-agent 模式
