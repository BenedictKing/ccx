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
| T4 | channel_scheduler.go 拆解（候选过滤 + LB.Sort） | in-progress | T3 | — |
| T5 | pipeline/middleware/{ccx_key_failure,ccx_pause_rule}.go | ✅ done | — | a37325e |
| T6 | internal/pricing 包（price/cost/calculator/loader/prices.json） | ✅ done | — | 0da9e66 |
| T7 | internal/usage NDJSON store | ✅ done | T6 | 74a6642 |
| T8 | 4 handler 切流量到新 pipeline | pending | T1-T7 | — |
| T9 | channel_dashboard_handler 扩展 cost 字段 | pending | T7 | — |
| T10 | frontend ChannelDashboardCard | pending | T9 | — |

## 第 1 轮（已完成）：T1 + T5 + T6
trellis-implement × 3 全部 PASS。trellis-check：T1 PASS；T5/T6 因 503 失败，由 `go vet + go test` 全绿替代验证。

## 第 2 轮（已完成）：T2 + T3 + T7
trellis-implement × 3 全部 PASS。**对齐修复**：T2/T3 各自定义了 channelKey 函数（`Name|BaseURL` vs `kind:name`），通过 sub-agent 把唯一来源统一到 `metrics.BuildLBChannelKey(kind, name)`。

## 第 3 轮（进行中）：T4

T4：channel_scheduler.go 拆解（候选过滤 + LB.Sort）。T3 报告给的 hook 点：
- 354-447 行 promotion → LB strategy_promotion
- 449-482 行 trace 亲和性 → LB strategy_traceaware
- 484-523 行 priority + 健康过滤 → LB strategy_errorAware + strategy_weightrr
- 526 行 fallback → LB 兜底
