# PR3 进度（cutover + billing）

> 父任务: [05-07-axonhub-forwarding-migration](../05-07-axonhub-forwarding-migration/prd.md)
> 状态: in_progress（开工 2026-05-07）

## 调研笔记

- `research/01-axonhub-middleware-mapping.md` —— T5 输入：ccx 自家 upstream_failover.go:354-595 → 9-hook RawResponse 映射
- `research/02-pricing-cost-types.md` —— T6 输入：axonhub price.go/cost.go/cost_calc.go 三文件原文 copy 方案 + 12 模型初版价
- `research/03-usage-log-fields.md` —— T7 输入：UsageRecord 字段对照 + NDJSON snake_case + decimal-as-string

## 工作单元状态

| 单元 | 范围 | 状态 | 依赖 | commit |
|------|------|------|------|--------|
| T1 | internal/metrics 扩 FTTL/TPS/ActiveConnections | ✅ done | — | 4b846e1 |
| T2 | stream.go 首事件 RecordFirstToken hook | ✅ done | T1 | 190adcd |
| T3 | scheduler/lb_metrics_provider.go 桥接 12 方法 | ✅ done | T1 | 09a85d4 |
| T4 | channel_scheduler.go 拆解（候选过滤 + LB.Sort） | ✅ done | T3 | 357cf82 |
| T5 | pipeline/middleware/{ccx_key_failure,ccx_pause_rule}.go | ✅ done | — | a37325e |
| T6 | internal/pricing 包 | ✅ done | — | 0da9e66 |
| T7 | internal/usage NDJSON store | ✅ done | T6 | 74a6642 |
| T8a-A | internal/handlers/wire 包（LBOutboundAdapter + Finalize） | ✅ done | T1-T7 | 8ce1e78 |
| T8a-B1 | messages outbound cross-format dispatch | ✅ done | T8a-A | 8f38698 |
| T8a-B2 | messages handler.go 切 pipeline.Process | 🟡 blocked-by-PR1-bug | T8a-B1 | — |
| PR1-fix | pipeline.go retry 路径加 stream cancel + close body + wait fan-out | pending | — | — |
| T8b | chat handler 切流量 + chat outbound cross-format | pending | T8a | — |
| T8c | responses handler 切流量 + responses outbound cross-format | pending | T8a | — |
| T8d | gemini handler 切流量 + gemini outbound cross-format | pending | T8a | — |
| T8e | 删 TryUpstreamWithAllKeys + scheduler 旧 SelectChannel 残留 | pending | T8a-d | — |
| T9 | channel_dashboard_handler 扩展 cost 字段 | pending | T7 | — |
| T10 | frontend ChannelDashboardCard | pending | T9 | — |

## 第 1 轮（已完成）：T1 + T5 + T6
trellis-implement × 3 全部 PASS。trellis-check：T1 PASS；T5/T6 因 503 失败，由 `go vet + go test` 全绿替代验证。

## 第 2 轮（已完成）：T2 + T3 + T7
trellis-implement × 3 全部 PASS。**对齐修复**：T2/T3 各自定义了 channelKey 函数（`Name|BaseURL` vs `kind:name`），通过 sub-agent 把唯一来源统一到 `metrics.BuildLBChannelKey(kind, name)`。

## 第 3 轮（已完成）：T4

T4：channel_scheduler.go 拆解。按 kind 隔离 LB 实例、`priorityToOrderingWeight` 把 priority 注入 LB 作次级 tiebreaker、`inferSelectionReason` 还原 reason 字段、SelectChannel 签名 0 变更。删除了旧的 priority-覆盖-affinity 硬规则。

## 第 4 轮（进行中）：T8 handler 切流量

### T8a 已完成部分
- **Stage A** (8ce1e78): `internal/handlers/wire/` 包，含 `LBOutboundAdapter` + `BuildPipelineOpts`。9 个测试通过
  - 注：路径偏离了 PRD 原计划的 `common/pipeline_wire.go`，因 `pipeline/middleware` → `handlers/common` 已有引用，反向 import 会循环
- **Stage B1** (8f38698): messages outbound 补全 cross-format dispatch（claude same-format raw / openai-gemini-responses 经 provider.ConvertToClaudeResponse 转换）。4 上游 matrix 测试通过

### T8a B2 阻塞详情（PR1 oversight）

**症状**：第一个 sub-agent (afebaab40229932c5) 实际完成了 handler.go + main.go 切换，cross-format 4 上游 matrix 全过，但 `TestMessagesHandler_StreamRawPassthroughCancelsFirstAttemptBeforeFailover` 永久 HANG。

**根因**：`internal/pipeline/pipeline.go` Process 主循环的 retry 路径在切换 channel 前**没有** cancel 当前 attempt 的 stream context + 关闭 `*http.Response.Body` + 等 fan-out goroutine 退出。这违反 AxonHub-half.md 契约 #1（retry/failover 前必须 cancel + close body + wait fan-out）。

**契约现状**：
- `internal/handlers/common/pipeline_attempt.go::BindRawStreamFanout` 已实现 cleanup（cancel ctx → drain & wait done → state.Reset）
- 但 PR1 的 `pipeline.go` 主循环没在 retry 之前调用这个 cleanup，导致 attempt 1 的 goroutine 永远卡在 `bufio.Reader.fill` / `chunkedReader.Read`

**修复路径**：在 `pipeline.go` 的每次 attempt 失败 retry 之前，按当前 attempt 的 `AttemptState` 状态调 cleanup hook（`AttemptState.RawStreamCancel()` + drain `RawStreamCh` + wait）。

### 503 风波
连续 3 次 sub-agent 在 T8a B2 + PR1 修补范围内被 API 503 中断，浪费约 80 分钟工作时长。已决定先封存元数据，再派窄范围 sub-agent 仅做 PR1 cancel 修补（独立模块），最后才做 B2 切 handler。

## 已落 commit 清单

```
8f38698  feat(messages): cross-format response dispatch (T8a B1)
8ce1e78  feat(handlers/wire): pipeline wiring helper (T8a A)
357cf82  feat(scheduler): wire LoadBalancer.Sort into SelectChannel (T4)
1c02475  docs(trellis): mark PR3 round 2 complete
74a6642  feat(usage): NDJSON usage store (T7)
0da9e66  feat(pricing): pricing package (T6)
09a85d4  feat(scheduler): bridge ccx tuple to loadbalance provider (T3)
190adcd  feat(stream): record first SSE token latency (T2)
a37325e  feat(pipeline/middleware): port ccx key/pause rules (T5)
4b846e1  feat(metrics): add LB data plane (T1)
d02c5ba  docs(trellis): kickoff PR3 with research notes
```
