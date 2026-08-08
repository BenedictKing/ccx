# Benchmark Chart 设计文档

> 当前结论：`frontend/src/` 中**没有**真正的 benchmark chart 页面或 Vue 组件。你提到的“图例 hover 高亮、模型点/轨迹弱化、筛选/排序交互”实际实现在离线脚本 `scripts/generate-benchmark-chart.mjs` 中，而非 Web 管理台。

## 1. 现状与范围

### 1.1 前端图表组件（非 benchmark）

`frontend/src/components/` 中现有图表均为监控/运营图表：

| 组件 | 用途 |
|------|------|
| `GlobalStatsChart.vue` | 全局流量/Token/成本趋势，可按模型堆叠 |
| `KeyTrendChart.vue` | 单渠道下按 key 或 key/model 维度的趋势图 |
| `ModelStatsChart.vue` | 按模型聚合的短周期统计图（可用但未挂载） |
| `ChannelMetricsChart.vue` | 旧版单渠道双图实现（请求量/成功率，未实际引用） |

这些组件统一使用 `apexcharts` + `vue3-apexcharts`，但**均未实现**：
- 图例 hover 高亮
- 系列 dim/highlight 自定义逻辑
- benchmark 专属排序/筛选

### 1.2 真正的 benchmark chart 位置

- `scripts/generate-benchmark-chart.mjs` — 离线 HTML 生成脚本
- `scripts/benchmark-sources/visualization.mjs` — 可视化辅助

该脚本实现了完整的 benchmark 对比交互，但产物是静态 HTML，不是 Web 管理台页面。

## 2. 数据链路

### 2.1 权威数据源

- `shared/model-registry/ccx_model_registry.json` — 权威模型注册表

### 2.2 同步到后端

- `backend-go/internal/presetstore/embedded/model-registry.json` — embedded shard
- `backend-go/internal/config/model_registry.go` — 运行时 snapshot
- `backend-go/internal/presetstore/store.go` / `updater.go` / `embedded.go` — 缓存/更新/原子替换

### 2.3 前端运行时加载

- `frontend/src/services/api.ts` → `getPresets()` → `/api/presets`
- `frontend/src/composables/useRuntimePresets.ts` → 解析 `benchmarkProfiles`

### 2.4 `ModelBenchmarkProfile` 结构

`frontend/src/services/api-types.ts`：

| 字段 | 含义 |
|------|------|
| `canonicalModel` | 规范模型名 |
| `overallScore` | 综合得分 |
| `categoryScores` | 分类得分 |
| `benchmarkEvidence` | 证据列表 |
| `sources` | 数据来源 |
| `verifiedAt` | 验证时间 |
| `lane` | `provisional` / `verified` |
| `sharedResults` | 共享结果 |
| `comparableCategories` | 可比较分类 |
| `totalCategories` | 总分类数 |

### 2.5 数据链路状态

- 数据链路已通：`/api/presets` → `useRuntimePresets` → `effectiveBenchmarkProfiles`
- UI 渲染链路未落地：`frontend/src` 没有任何组件真正渲染 benchmark profile

## 3. 与 Autopilot 的共享与使用

### 3.1 后端使用位置

benchmark 数据在 autopilot 中的使用：

- `backend-go/internal/autopilot/task_domain.go` — `ResolveDomainStrengthForEffort`
- `backend-go/internal/autopilot/model_resolver.go` — `buildRankedCandidates`
- `backend-go/internal/autopilot/model_frontier_scoring.go` — `frontierQualityHalfWidth`
- `backend-go/internal/autopilot/routing_trace.go` — trace 输出 `DomainEvidence`

### 3.2 关键用途

1. **Domain Strength**：规范 benchmark category 映射到任务域，形成 `DomainStrengthEvidence`
2. **Candidate 评分**：写入 `benchmarkKnown/benchmarkScore/benchmarkModel/benchmarkLane`
3. **置信区间调整**：`benchmarkLane == "provisional"` 时扩大 quality interval
4. **Trace 输出**：`RoutingCandidate.DomainEvidence` 可返回给前端

### 3.3 前端展示缺口

虽然前端类型已定义：
- `DomainStrengthEvidence`
- `RoutingCandidate`
- `benchmarkLane`
- `benchmarkSources`
- `benchmarkVerifiedAt`

但 UI 现状：
- `AutopilotTraceDetailDialog.vue` 只显示 channel / origin tier / totalScore / selected / filterReasons
- `AutopilotDiagnosePanel.vue` 只显示 candidate.score / mappedModel / mappingSource / filterReasons

**均未展开显示 benchmark lane / canonical benchmark 证据**。

## 4. 离线脚本 benchmark chart 交互

### 4.1 图例 hover 高亮

`generate-benchmark-chart.mjs`：

- `renderLegend(rows)` 动态生成图例 DOM
- `item.addEventListener('mouseenter', () => applyModelHighlight(model))`
- `item.addEventListener('mouseleave', () => applyModelHighlight(null))`

高亮逻辑：
- `applyModelHighlight(model)` 对所有 `[data-model]` 元素统一打类
- 非目标模型：`.is-dim`
- 目标轨迹：`.is-highlight`

样式：
- `.trajectory.is-dim { opacity: .07 }`
- `.trajectory.is-highlight { opacity: .9; stroke-width: 2.5 }`
- `.point.is-dim { opacity: .12 }`
- `.legend-item.is-dim { opacity: .35 }`

### 4.2 筛选与排序

- source filter
- 成本口径切换：`mean_cost` / `median_cost`
- 成本范围切换：`focus` / `full`
- comparison category 切换
- table 中按 `pass_rate desc, cost asc` 排序
- Pareto frontier 计算

### 4.3 产物形式

生成静态 HTML 文件，用于离线查看，不嵌入 Web 管理台。

## 5. 模型注册与 provisional lane

### 5.1 provisional lane 数据层

- 前端类型：`lane?: 'provisional' | 'verified'`
- 后端校验：`presetstore/validate.go` 只允许 `provisional` / `verified`
- 后端评分：`model_frontier_scoring.go` provisional 扩大 quality interval

### 5.2 muse-spark 模型注册

- 已存在于 `backend-go/internal/presetstore/embedded/model-registry.json`
- 最近 commit 涉及 `muse-spark-1.1/1.2` 基准数据刷新与预注册

### 5.3 前端展示缺口

- 无 badge/legend/UI lane 渲染
- 无 provisional 模型的特殊视觉提示

## 6. 布局示意图

### 6.1 当前数据流

```text
[shared/model-registry/ccx_model_registry.json]
              │
              ▼
[backend-go/internal/presetstore/embedded/model-registry.json]
              │
              ▼
[backend-go /api/presets]
              │
              ├──────────────────────┐
              ▼                      ▼
    [frontend useRuntimePresets]  [autopilot ModelRegistry]
              │                      │
              ▼                      ▼
    [effectiveBenchmarkProfiles] [DomainStrength/Frontier Scoring]
              │                      │
              ▼                      ▼
    [无 UI 组件消费]              [Trace 输出 DomainEvidence]
              │                      │
              ▼                      ▼
    [AutopilotTraceDetailDialog] [AutopilotDiagnosePanel]
    (未展示 benchmark 详情)      (未展示 benchmark 详情)
```

### 6.2 目标状态（建议）

```text
[shared/model-registry/ccx_model_registry.json]
              │
              ▼
[backend-go /api/presets]
              │
              ├──────────────────────┐
              ▼                      ▼
    [frontend useRuntimePresets]  [autopilot ModelRegistry]
              │                      │
              ▼                      ▼
    [BenchmarkChart.vue]         [DomainStrength/Frontier Scoring]
    (新页面/组件)                   │
              │                      ▼
              ├─ 图例 hover 高亮    [Trace 输出]
              ├─ 模型点/轨迹弱化         │
              ├─ 筛选/排序              ▼
              └─ provisional lane    [AutopilotTraceDetailDialog]
                     │              (展示 benchmark 详情)
                     ▼
              [离线脚本 generate-benchmark-chart.mjs]
              (保留作为离线产物)
```

## 7. 待补充

- 是否需要在 Web 管理台新增 benchmark chart 页面
- 若需要，是复用 apexcharts 还是引入更专门的图表库
- 离线脚本与管理台页面的功能对齐策略
- benchmark 数据更新频率与缓存策略
