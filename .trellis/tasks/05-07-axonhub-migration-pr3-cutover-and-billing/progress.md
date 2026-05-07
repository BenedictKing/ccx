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
| T1 | internal/metrics 扩 FTTL/TPS/ActiveConnections | pending | — | — |
| T2 | stream.go 首事件 RecordFirstToken hook | pending | T1 | — |
| T3 | scheduler/lb_metrics_provider.go 桥接 13 方法 | pending | T1 | — |
| T4 | channel_scheduler.go 拆解（候选过滤 + LB.Sort） | pending | T3 | — |
| T5 | pipeline/middleware/{ccx_key_failure,ccx_pause_rule}.go | pending | — | — |
| T6 | internal/pricing 包（price/cost/calculator/loader/prices.json） | pending | — | — |
| T7 | internal/usage NDJSON store | pending | T6 | — |
| T8 | 4 handler 切流量到新 pipeline | pending | T1-T7 | — |
| T9 | channel_dashboard_handler 扩展 cost 字段 | pending | T7 | — |
| T10 | frontend ChannelDashboardCard | pending | T9 | — |

## 第 1 轮（并行）：T1 + T5 + T6

实际派发：trellis-implement × 3（metrics / pipeline-middleware / pricing 三个独立包）
