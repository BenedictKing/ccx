# Channel Dashboard 契约（PR3 T9 + T10 落地）

后端 GET `/api/{type}/channels/:id/dashboard` 返回 per-channel 聚合指标，前端 `ChannelDashboardCard.vue` 渲染。

## 后端契约

`backend-go/internal/handlers/channel_metrics_handler.go::buildChannelMetricsResult` 通过 `metrics.BuildLBChannelKey(string(kind), upstream.Name)` 查 `lbMetricsEntry`，返回 JSON：

```jsonc
{
  "channelId": "<int>",
  "channelName": "<string>",
  "requestCount": "<int>",          // 总请求
  "successRate": "<float>",         // 0.0-1.0，前端格式化为百分比
  "inputTokens": "<int>",           // 累计 prompt tokens
  "outputTokens": "<int>",          // 累计 completion tokens
  "totalTokens": "<int>",           // input + output
  "cacheReadInputTokens": "<int>",  // cache 命中
  "cacheCreationInputTokens": "<int>", // cache 写入
  "totalCost": "<string>"           // decimal as string，跨语言无损
}
```

数据来源：
- `requestCount / successRate` —— 现有 metrics aggregated counters
- `inputTokens / outputTokens / totalTokens / cacheReadInputTokens / cacheCreationInputTokens / totalCost` —— `lbMetricsEntry` 通过 `wire.LBOutboundAdapter.Finalize → MetricsManager.RecordCost` 累加

`totalCost` 用 string 序列化保留 `shopspring/decimal` 精度（与 NDJSON usage 一致）。

## 前端契约

`frontend/src/services/api.ts` 已扩 `ChannelMetrics` interface：
```ts
interface ChannelMetrics {
  // ... existing fields ...
  inputTokens?: number
  outputTokens?: number
  totalTokens?: number
  cacheReadInputTokens?: number
  cacheCreationInputTokens?: number
  totalCost?: string  // decimal as string，前端 parseFloat 显示
}
```

字段全部 optional，缺数据时 UI 显示 em-dash `—`。

## ChannelDashboardCard.vue 渲染规则

- Props: `metrics?: ChannelMetrics`
- 7 行展示（PRD 第 149-158 行 UI 文案）：
  - `总请求 18`
  - `可用率 100.0%`
  - `输入 Token 1.2K`
  - `输出 Token 8.0K`
  - `总 Token 9.2K`
  - `缓存 R/W 读 689.9K 写 589.4K`
  - `成本 $0.12`

格式化函数（导出供测试）：
- `formatNumber(n?: number)`: `< 1000` 原值；`1000-999999` → `1.2K`；`>= 1000000` → `1.2M`；undefined → `—`
- `formatPercent(rate?: number)`: 1 位小数 + `%`；undefined → `—`
- `formatCost(s?: string)`: `parseFloat(s).toFixed(2)` → `$0.12`；undefined / NaN → `—`
- `formatCacheReadWrite(read?, write?)`: 双字段都缺 → `—`；任一存在 → `读 X 写 Y`

## Vuetify 组件

仅用基础 Vuetify primitives（已在 `frontend/src/plugins/vuetify.ts` 注册）：
- `VCard / VCardText / VList / VListItem`

无图标使用，无 `@mdi/js` / `iconMap` 改动。

## 测试覆盖

`frontend/src/components/ChannelDashboardCard.test.ts`（vitest 4 + `it.each` 表驱动 32 cases）：
- 数值格式化边界（< 1K / 1.2K / 1.2M）
- 缺 `totalCost` → `—`
- 缺所有 cache 字段 → `—`
- decimal cost rounding（`"0.12345"` → `$0.12`）
- NaN / 单边 cache miss

## 集成提示

PR3 仅落组件 + 测试，未接入到现有 channel 列表页。后续 PR 把 `ChannelDashboardCard` 接入到：
- `frontend/src/views/Channels.vue` channel detail 视图，或
- `frontend/src/views/ChannelOrchestration.vue` orchestration 详情面板

## 相关文件

- `backend-go/internal/handlers/channel_metrics_handler.go`
- `backend-go/internal/metrics/channel_metrics_lb.go`（lbMetricsEntry totalCost / token totals）
- `backend-go/internal/handlers/wire/wire.go::Finalize`（RecordCost 调用点）
- `frontend/src/services/api.ts`（ChannelMetrics interface）
- `frontend/src/components/ChannelDashboardCard.{vue,test.ts}`
