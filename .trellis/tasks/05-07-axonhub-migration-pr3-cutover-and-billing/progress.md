# PR3 完成（cutover + billing）

> 父任务: [05-07-axonhub-forwarding-migration](../05-07-axonhub-forwarding-migration/prd.md)
> 状态: ✅ done（开工 2026-05-07，完工 2026-05-07，30 个 commit）

## 完成的工作单元

| 单元 | 范围 | commit |
|------|------|--------|
| T1 | internal/metrics 扩 FTTL/TPS/ActiveConnections | 4b846e1 |
| T2 | stream.go 首事件 RecordFirstToken hook + channelKey 单一来源 | 190adcd |
| T3 | scheduler/lb_metrics_provider.go 桥接 12 方法 | 09a85d4 |
| T4 | channel_scheduler.go 拆解（候选过滤 + LB.Sort） | 357cf82 |
| T5 | pipeline/middleware/{ccx_key_failure,ccx_pause_rule}.go | a37325e |
| T6 | internal/pricing 包（embed.FS + 12 模型） | 0da9e66 |
| T7 | internal/usage NDJSON UsageStore | 74a6642 |
| PR1-fix | pipeline.go cancel + close body + drain fan-out before retry | feebbb6 |
| T8a-A | internal/handlers/wire 包（LBOutboundAdapter + Finalize） | 8ce1e78 |
| T8a-B1 | messages outbound cross-format dispatch | 8f38698 |
| T8a-B2-1 | wire per-key RecordSuccess/Failure | 04ee34f |
| T8a-B2-2 | messages handler 切 pipeline.Process | 44956c8 |
| T8a-B2-3 | messages outbound token normalization | 3a359bd |
| T8a-B2-4 | pipeline SSE event:error detection middleware | 7b3b1c3 |
| T8a-B2-5 | wire ChannelRetryable.NextKey | 5aa3970 |
| T8b | chat handler 切流量 + outbound cross-format | 0d5f005 |
| T8c | responses handler 切流量 + outbound cross-format | f2b1bd3 |
| T8d | gemini handler 切流量 + outbound cross-format | 4187939 |
| T8e-msg | messages dead helpers cleanup | 46e1736 |
| T8e-chat | chat dead helpers cleanup | c4fa18b |
| T8e-gem | gemini dead helpers cleanup | 70722ed |
| T9 | metrics + dashboard 扩 cost + cache 字段 | 2aca4a1 |
| T10 | frontend ChannelDashboardCard.vue | eb5c0e6 |
| docs | spec/pricing.md + spec/usage-store.md（首次同步） | a6aa80e |
| docs | trellis kickoff + research notes | d02c5ba |
| docs | round 2 progress mark | 1c02475 |

## Acceptance Criteria 状态

- [x] 现有 messages / chat / responses / gemini handler_test / matrix_test / failover_test 全部通过
- [x] 现有 BlacklistKey / MarkKeyAsFailedWithDuration / MatchPauseRule 测试不回归
- [x] AxonHub-half.md 第 82-90 行已迁移契约一条不退（PR1-fix feebbb6 解决最后一项 stream cancel-on-retry）
- [x] 价格计算 3 种模式各有单测，覆盖 5+ 主流模型（pricing 包 84.2% 覆盖）
- [x] UsageStore NDJSON：并发写（200×10 = 2000 行 100% 落盘）、按日切分、保留期清理、崩溃恢复（mock clock + retention sweep 测试）
- [x] 前端 ChannelDashboardCard 单测覆盖 6 项指标 + cost + 缺数据 fallback（vitest 32 cases）
- [x] 端到端：发起一次请求 → metrics 更新 → NDJSON 落账 → API 查询 → UI 显示
- [x] go test ./... 全部通过
- [x] git diff --check 通过
- [x] frontend bun run build clean

## Definition of Done 状态

- [x] `.trellis/spec/backend/pricing.md` 新增（a6aa80e）
- [x] `.trellis/spec/backend/usage-store.md` 新增（a6aa80e）
- [x] `.trellis/spec/frontend/channel-dashboard.md` 新增（本 commit）
- [x] AxonHub-half.md 关闭收尾，标注本次完整迁移完成（本 commit）
- [ ] backend-go/CLAUDE.md 新增 pipeline / loadbalance / pricing / usage 模块说明（追加在收尾 commit）
- [ ] .trellis/spec/backend/quality-guidelines.md 加 NDJSON 落账契约（追加在收尾 commit）
- [x] TryUpstreamWithAllKeys：保留（images handler 仍在用，不在 PR3 范围）

## 残留（不阻塞 PR3 验收）

- `internal/handlers/images/handler.go` 仍用 TryUpstreamWithAllKeys（不在 PR3 范围）
- responses/handler.go 的 handleSuccess 系列因 handler_session_test.go 直接调用而保留
- gemini/stream.go::handleStreamSuccess lint hint 未处理（不阻塞编译）
- ChannelDashboardCard 集成到 Channels.vue / ChannelOrchestration.vue 视图层（PR4 范围）

## 关键决策记录

### 1. wire 包路径偏离 PRD（common → wire）
PRD 原计划 `internal/handlers/common/pipeline_wire.go`，但 `pipeline/middleware` → `handlers/common` 已有引用，反向 import 循环。改放 `internal/handlers/wire/` 子包破环。

### 2. channelKey 单一来源
T2/T3 各自定义 channelKey 函数（`Name|BaseURL` vs `kind:name`），通过 alignment sub-agent 统一到 `metrics.BuildLBChannelKey(kind, name)`，metrics 包成为唯一来源。

### 3. AttemptInfo ctx 指针 holder
T5 ccx key middleware 用 `*middleware.AttemptInfo` 通过 ctx 传递，outbound `NextChannel` 原地改写 channel/apiKey，middleware 下次 RawResponse 自动看到新值。**不改 pipeline.Middleware 9-hook 接口**。

### 4. pipeline cleanup-on-retry（PR1 oversight 修补）
PR1 锁定的契约 #1（cancel + close body + wait fan-out）只在 BindRawStreamFanout 层做了，pipeline.Process 主循环没在 retry 之前调用，导致 stream attempt retry 时 goroutine 阻塞。feebbb6 通过 `cleanupAttemptStreamResources` LIFO + `withAttemptState(ctx)` 注入解决。

### 5. SSE event:error 检测 + ChannelRetryable.NextKey
- `pipeline/middleware/sse_error_event.go` 检测 HTTP 200 + body 含 `event:error` 帧返回 sentinel
- `wire.LBOutboundAdapter` 实现 `pipeline.ChannelRetryable`，让 SSE error 触发 same-channel key rotation 而非 channel rotation

### 6. wire/responses cleanup 保留
responses 旧 helper 因测试直接调用而保留（handler_session_test.go:54）。属于"测试与新路径双轨"中间态，待测试重写后再清理。

### 7. 503 风波（10 次 sub-agent 中断）
PR3 期间累计 10+ 次 API 503 中断 sub-agent。通过：
- 极简 prompt（直接给完整代码片段）
- 小范围拆分（≤ 2 文件 / 单元）
- 工作树 rollback + 重派
- 中间 commit 标 known-gap

完成总耗时被显著拉长，但最终全部 acceptance criteria 通过。

## Spec 索引（PR3 输出）

- [pipeline-architecture.md](../../spec/backend/pipeline-architecture.md)（PR1 已落）
- [pricing.md](../../spec/backend/pricing.md)
- [usage-store.md](../../spec/backend/usage-store.md)
- [channel-dashboard.md](../../spec/frontend/channel-dashboard.md)
