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

## 7. 待补充项详解

### 7.1 是否需要在 Web 管理台新增 benchmark chart 页面

**当前实现**

- 路由集中在 `frontend/src/router/index.ts`：静态路由数组，9 条路由（`/channels/:type`、`/conversations`、`/health`、`/subscriptions`、`/cockpit`、`/autopilot`、`/cost-report`），全部懒加载 + `meta: { requiresAuth: true }`。新增一条路由是纯增量、零副作用的改动。
- 顶部导航是**两套手写清单**，新增页面必须同步两处：
  1. `frontend/src/App.vue:100-135` 桌面端平铺 `<router-link>`（硬编码，如 `/cost-report` 甚至标签直接写死中文"成本报表"未走 i18n）。
  2. `frontend/src/composables/useAppController.ts:59-68` 的 `apiTabOptions` 数组（移动端下拉）。注意此数组当前**缺 `/cost-report` 与 `/autopilot` 的部分项**，说明两套清单已有漂移。
- 视图目录 `frontend/src/views/` 现有 8 个 View，结构一致（`CostReportView.vue` 是最接近的先例：独立数据页、无渠道统计头）。
- **无设计稿**：`docs/specs/benchmark-chart.md` §6.2 给出的是文字版"目标状态"布局示意，非视觉设计稿。

**缺口分析**

- 路由/视图结构**非常容易扩展**——照抄 `CostReportView.vue` 模式即可。
- 真正的负担在导航双清单同步，且清单已存在漂移。新增页面若沿用现状会加剧这个问题。
- 三份 i18n（`zh-CN.json`/`en.json`/`id.json`）需要同步新增 key；当前无任何 `benchmark.*` key。
- 没有需求文档/设计稿意味着交互范围（是否复刻离线脚本全部功能）需要产品先拍板。

**建议方案**

1. 新增 `frontend/src/views/BenchmarkView.vue` + `router/index.ts` 一条 `/benchmark` 路由，`requiresAuth: true`，懒加载。数据源直接用 `useRuntimePresets().effectiveBenchmarkProfiles`。
2. 导航先做**收敛再扩展**：把 `App.vue` 硬编码链接改为遍历 `translatedApiTabOptions`，补齐 `apiTabOptions` 缺失项与图标，再加 benchmark 项；同时把 benchmark 页加入 `App.vue` 全局统计卡片的路径黑名单。
3. i18n 补 `benchmark.*` key 到三份 locale（保持行对齐），顺手把 `cost-report` 的硬编码中文也国际化。
4. 落地前先产出一页需求（至少确认：展示 overallScore/categoryScores 雷达/表格，还是复刻能力-成本散点+Pareto），否则范围无界。

### 7.2 图表库选择（apexcharts 能否满足）

**当前实现**

- 前端唯一图表库：`apexcharts@^5.15.2` + `vue3-apexcharts@^1.11.1`。
- 现有 4 个图表组件**全部只用 `type="area"`/`type="line"` 时间序列**，没有任何 scatter/bubble/radar 用法。
- 已用到的 apex 能力：`updateSeries` 就地更新、`annotations.xaxis` 背景带、`tooltip.custom`、`legend` 配置、渐变填充。**未使用**图例 hover 联动（`legend.onItemHover.highlightDataSeries`）、`highlightSeries`/`toggleSeries` 等实例方法。
- apexcharts 类型定义证实其**具备**：`'scatter'` 图表类型、`legend.onItemHover.highlightDataSeries`、实例方法 `highlightSeries`/`toggleSeries`、`markers.discrete`、point/xaxis annotations。
- 离线脚本 `generate-benchmark-chart.mjs` 的交互**不是用任何图表库实现的**——它是**手写 SVG**：`renderTrajectories/renderPoints/renderLabels` 直接 `document.createElementNS`，`applyModelHighlight(model)` 对所有 `[data-model]` 节点统一打 `.is-dim`/`.is-highlight` CSS 类，图例 hover 通过 `item.addEventListener('mouseenter', ...)` 联动。这套精细的"轨迹弱化 + 点弱化 + 图例项弱化"三级联动在 apexcharts 里没有直接等价物。

**缺口分析**

- **基础散点/Pareto 前沿**：apexcharts 的 scatter + 一条 line series 叠加可实现，成本不高。
- **图例 hover 高亮**：apexcharts 有 `legend.onItemHover.highlightDataSeries`，但它的高亮粒度是"整个 series"，而离线脚本按 **model** 聚合（同一 model 跨 source 多条轨迹 + 多个点作为一个高亮单元）。要在 apex 里复现"hover 一个 model 名 → 弱化其它所有 model 的轨迹和点"需要把每个 model 组织成独立 series 并自行封装联动逻辑，能做但要写不少胶水。
- **轨迹（同 model 不同 effort 连线）+ 散点 + 标签避让 + Pareto 折线**同图叠加，外加成本口径/成本范围/来源筛选切换——这套组合更接近 D3/手写 SVG 的自由度，用 apex 会到处打补丁。
- 引入 ECharts/D3 会**增加打包体积**，而 `frontend/CLAUDE.md` 明确强调按需导入、首屏 JS 已优化约 60%，新增一个大图表库与该目标冲突。

**建议方案**

- **不引入新库**。两条路线择一：
  - **路线 A（推荐，低风险）**：直接把 `generate-benchmark-chart.mjs` 里已经验证过的手写 SVG 渲染逻辑移植成一个 Vue 组件。它零依赖、交互已完整，避免 apex 建模不匹配的胶水成本，也不增加体积。
  - **路线 B（若只要简版）**：用现有 apexcharts 做 scatter + Pareto line + 内置 `legend.onItemHover`，接受"高亮按 series 而非按 model 聚合"的降级，快速交付但交互精度低于离线脚本。
- 明确不选 ECharts/D3：收益不抵体积与新依赖成本，且路线 A 已能满足全部交互需求。

### 7.3 离线脚本与管理台页面的功能对齐策略

**当前实现**

- `scripts/generate-benchmark-chart.mjs`（751 行）结构：`renderBenchmarkChart(rows, comparisons)` 返回一整个 HTML 字符串，内联 `<style>` + `<script>`。核心交互逻辑是纯 DOM/SVG，**不依赖 Vue、不依赖任何库**：
  - 状态：`const state = { metric:'mean_cost', range:'focus', source:'all' }` + `comparisonState`。
  - 数据处理纯函数：`quantile`、`paretoFrontier`、`niceMax`、`ticks`、`currentRows`、`linePath`。
  - 渲染：`setGeometry`/`renderAxes`/`renderTrajectories`/`renderPoints`/`renderLabels`/`renderLegend`/`renderTable` + 第二张"多来源比较"图 `updateComparison`。
  - 交互：`applyModelHighlight`（三级弱化）、`bindSegmented`、source `<select>`、comparison category `<select>`、`ResizeObserver` 响应式重绘。
- 数据入口：脚本从 `/tmp/benchmark-viz-data.json` 读 `{data, comparisons}`。该文件由 `scripts/update-benchmark-data.mjs` 调 `scripts/benchmark-sources/visualization.mjs::buildBenchmarkVisualizationData` 生成。
- **关键错位**：离线脚本的 `data`（能力-成本散点，含 pass_rate/cost）**只存在于离线管线**，其 `mean_cost/median_cost` 来自 DeepSWE live leaderboard 和 CodexRadar 成本明细，这些**不落地** `ccx_model_registry.json`（`update-benchmark-data.mjs:337` 明确 `delete profile.costData`）。因此 `/api/presets` 下发的 `benchmarkProfiles` **不含 pass_rate/cost 散点数据**，只有 `overallScore/categoryScores/benchmarkEvidence`。

**缺口分析**

- 交互逻辑复用**可行**：SVG 渲染函数不依赖 Node/浏览器专属 API，可整体搬进 `<script setup>`，用 `ref` 替换 `state`、`watch`/`computed` 替换手动 `update()` 调用、模板替换手写 DOM 骨架。
- 数据链路**存在硬缺口**：管理台通过 `/api/presets` 拿不到能力-成本散点所需的 `pass_rate` 和 `cost`。这意味着：
  - 若要在管理台复刻**能力-成本散点图**，必须先决定是否把成本/pass_rate 数据也纳入 `benchmarkProfiles`（改 `presetstore/preset.go`、`config.go` 结构体、`convertRuntimeBenchmarkProfiles`、前端 `ModelBenchmarkProfile` 类型），或另开端点。这是一个跨前后端的 schema 扩展，非纯前端工作。
  - 若管理台只复刻**多来源比较图**（`comparisons`：category × score），这部分能从 `benchmarkProfiles` 的 `categoryScores` + `benchmarkEvidence` 在前端重建，无需后端改动。

**建议方案**

1. **分层对齐，不追求一次性全复刻**：
   - **第一步（无后端改动）**：管理台先做"多来源能力比较"图——数据从 `effectiveBenchmarkProfiles` 的 `categoryScores`/`benchmarkEvidence` 前端重建 comparison rows。
   - **第二步（需后端 schema 决策）**：若产品要能力-成本散点，需先决定成本/pass_rate 数据的下发方式（扩展 `benchmarkProfiles` 或新端点），再移植散点+Pareto 渲染。
2. **代码组织**：把 `generate-benchmark-chart.mjs` 里的纯计算函数（`quantile/paretoFrontier/niceMax/ticks/linePath`）抽到 `shared/` 或前端 util，离线脚本与 Vue 组件共享，避免两处维护发散。渲染层离线保留字符串拼接、管理台用 Vue 模板 + `:d`/`:cx` 绑定各自实现。
3. 离线脚本**保留**作为 CI/本地生成静态产物的手段，不废弃；管理台页面是它的在线补充。

### 7.4 benchmark 数据更新频率与缓存策略

**当前实现**

**数据生产（离线，手动/CI 触发）**：
- `make benchmark-update` → `scripts/update-benchmark-data.mjs`：抓 5 源（deepswe/benchlm/dradar/artificial-analysis/litellm）→ 合并进 `shared/model-registry/ccx_model_registry.json` → 生成 `docs/public/presets/*` 与 `backend-go/internal/presetstore/embedded/*`。
- **无定时任务**：`.github/workflows/` 无 benchmark 相关 workflow，无 cron。更新完全靠人工跑 `make benchmark-update`。
- 抓取层缓存：`scripts/benchmark-sources/http-cache.mjs` + benchlm 的 ETag/generatedAt 双层变更检测；数据未变时跳过 merge、不刷 `verifiedAt`，避免无效 diff。当前 registry 24 个 `benchmarkProfiles`，全部 `lane: provisional`（0 个 verified），`imageArenaProfiles: []`。

**数据分发（后端→前端）**：
- 后端 `presetstore.PresetUpdater`：默认从 `https://benedictking.github.io/ccx/presets/index.json` 拉取；`PRESET_UPDATE_INTERVAL_MINUTES` 默认 **360 分钟（6h）**，范围 30–10080；`PRESET_UPDATE_ENABLED` 默认 true。按 `dataVersion` 单调递增比较，SHA256 校验，原子 `Swap`。
- 前端下发：`GET /api/presets` 带 **ETag** + `Cache-Control: no-cache`，支持 `If-None-Match` → 304。
- 前端缓存：`useRuntimePresets.ts` 模块级 `state` ref + `loaded` 标志 + `inflight` 去重 + `latestRequestToken` 防旧响应覆盖。`ensureRuntimePresetsLoaded(force=false)`：**一旦 loaded 就永不重取**（除非 `force=true`）。当前**唯一调用点**是 `useEditChannelPresets.ts:32`（编辑渠道时），`main.ts` 启动时不加载。

**缺口分析**

- **生产频率无自动化**：benchmark 数据靠人工 `make benchmark-update` + 发版分发，没有 cron，`verifiedAt` 会随人工更新滞后。对 benchmark chart 而言，数据"新鲜度"取决于运营者节奏，页面上应展示 `verifiedAt`/`dataVersion` 让用户知情。
- **前端缓存策略与"页面"场景不匹配**：`useRuntimePresets` 的 `loaded` 是**进程生命周期级永久缓存**，无 TTL、无基于 `dataVersion` 的失效。benchmark chart 页面若长时间开着、后端 6h 后拉到新 preset，前端不会自动刷新。
  - 对比：后端 `/api/presets` 已实现 ETag/304，但前端 `api.getPresets()` 是裸 `this.request('/presets')`，**未发送 `If-None-Match`、未消费 ETag**——304 优化在前端侧未启用。
- `/api/presets/status`（后端已实现，返回 source/dataVersion/lastCheckAt/lastError）**前端零消费**——benchmark 页面本可用它显示数据来源与新鲜度，但目前无对应前端类型或调用。
- provisional/verified：全部 provisional，前端无 lane 徽章。若 chart 要区分 provisional（数据可信度低）需前端自行读 `profile.lane`。

**建议方案**

1. **生产频率**：若希望 benchmark 数据自动新鲜，新增一个 GitHub Actions cron workflow 跑 `make benchmark-update` 并提交/发布 preset（需 `ARTIFICIAL_ANALYSIS_API_KEY` secret，缺失时自动跳过 AA）。否则明确接受"人工 + 发版"节奏，并在 chart 页显示 `verifiedAt`/`dataVersion`。
2. **前端缓存**：benchmark chart 页面挂载时调 `ensureRuntimePresetsLoaded()`（复用现有共享缓存，避免重复请求）；若需页面级刷新按钮，调 `ensureRuntimePresetsLoaded(true)`。不必为 benchmark 单独建缓存层——`effectiveBenchmarkProfiles` 已就绪。
3. **可选增强**：让 `api.getPresets()` 携带 `If-None-Match` 并缓存 ETag，真正用上后端已实现的 304，减少大 bundle 重复传输；或页面用 `/api/presets/status` 的 `dataVersion` 做轻量轮询判断是否需要 `force` 重取。
4. chart 页读 `profile.lane` 对 provisional 数据加视觉标注（当前全部 provisional，这个提示实际意义明确）。
