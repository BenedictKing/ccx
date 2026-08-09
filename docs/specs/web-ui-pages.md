# Web UI 页面层设计文档

> 范围：页面/tab 层（View 层）。对话框层已在 [web-ui-dialogs.md](./web-ui-dialogs.md) 覆盖，此处不重复。
> 技术栈：Vue 3 `<script setup>` + Vuetify 3（按需注册）+ Pinia + vue-router（HTML5 history）+ 自研 i18n（扁平 dotted-key JSON，`src/locales/{en,id,zh-CN}.json`）。

## 目录

- [0. 全局架构总览](#0-全局架构总览)
- [1. 顶部导航（两套）](#1-顶部导航两套)
- [2. ChannelsView](#2-channelsview-channelstype)
- [3. ConversationsView](#3-conversationsview-conversations)
- [4. HealthCenterView](#4-healthcenterview-health)
- [5. SubscriptionsView](#5-subscriptionsview-subscriptions)
- [6. CockpitView](#6-cockpitview-cockpit)
- [7. AutopilotView](#7-autopilotview-autopilot)
- [8. CostReportView](#8-costreportview-cost-report)
- [9. 已知导航 / 信息架构问题汇总](#9-已知导航--信息架构问题汇总)

<!-- 各章节逐节补充 -->

## 0. 全局架构总览

### 0.1 路由表（`frontend/src/router/index.ts`）

| 路径 | View | name | 说明 |
|---|---|---|---|
| `/` | — | — | redirect → `/channels/messages` |
| `/channels/chat` `/channels/responses` `/channels/gemini` | — | — | 全部 redirect → `/channels/messages`（多 LLM 协议已合并为统一渠道列表） |
| `/channels/:type` | `ChannelsView.vue` | — | 动态段匹配 `messages/images/vectors`；`props:true`，懒加载 |
| `/conversations` | `ConversationsView.vue` | — | 会话驾驶舱（对话雷达） |
| `/health` | `HealthCenterView.vue` | — | 渠道健康中心 |
| `/subscriptions` | `SubscriptionsView.vue` | — | 订阅中心 |
| `/cockpit` | `CockpitView.vue` | — | 运营总览 |
| `/autopilot` | `AutopilotView.vue` | `autopilot` | 智能路由 |
| `/cost-report` | `CostReportView.vue` | `cost-report` | 成本报表 |

- 所有路由 `meta.requiresAuth:true`，但 `beforeEach` 守卫是空操作（`next()` 无条件放行）；真正的认证门在 `App.vue` 的认证 overlay/对话框。
- 无 404 / catch-all 路由；无嵌套子路由。

### 0.2 顶层布局（`App.vue`）与"路径黑名单"

`App.vue` 的 `v-main` 内，三块全局 UI 通过同一个**路径黑名单**显式隐藏：

```
黑名单 = ['/conversations', '/health', '/autopilot', '/cost-report']
```

| 全局区块 | 显示条件 | 出现路由 | 隐藏路由 |
|---|---|---|---|
| 全局统计可折叠卡片 `GlobalStatsChart` | `isAuthenticated && !黑名单` | channels/*、subscriptions、cockpit | conversations、health、autopilot、cost-report |
| 三张统计卡片（总渠道/活动渠道/系统状态） | `!黑名单` | 同上 | 同上 |
| 操作栏（添加渠道 / 刷新 / 熔断器 TB） | `!黑名单` | 同上 | 同上 |

> **ego-browser 实测确认**（2026-08-09）：`/subscriptions` 与 `/cockpit` **不在**黑名单，确实显示渠道统计卡和"添加渠道"操作栏——语义上突兀（见 §9 问题 P4）。`/cost-report` 在黑名单内，本身渠道无关，正确。

### 0.3 ASCII 全局布局

```
┌────────────────────────────────────────────────────────────┐
│ v-app-bar (毛玻璃, 高 56/72)                                │
│  [Logo] [导航: 移动<1000px 下拉 / 桌面平铺]      [版本徽章]  │
│         ... spacer ... [语言][帮助][暗色][注销]             │
├────────────────────────────────────────────────────────────┤
│ v-main > v-container                                        │
│  ┌ 全局统计卡 GlobalStatsChart (可折叠) ┐  ← 黑名单隐藏      │
│  ┌ [总渠道][活动渠道][系统状态] 三卡   ┐  ← 黑名单隐藏      │
│  ┌ 操作栏 [+添加渠道][刷新]...[TB]    ┐  ← 黑名单隐藏      │
│  ┌ <router-view/>  ← 当前 View                         ┐  │
│  └ 对话框层（略，见 web-ui-dialogs.md）+ Toast 栈        ┘  │
└────────────────────────────────────────────────────────────┘
```

### 0.4 全局数据流 / 轮询

- `useAppController.ts`（App.vue 唯一组合式控制器）持有所有 store：`auth / channel / preferences / dialog / system`。
- **渠道轮询**：`channelStore.startAutoRefresh()` 经 `registerGlobalTick(5000ms)`（`useGlobalTick`，多组件共享单一 `setInterval`，页面 hidden 自动暂停）。仅认证后启动，登出停止。
- **系统状态**：watch `channelStore.lastRefreshSuccess` → `systemStore.setSystemStatus(running|error)`，驱动第三张统计卡。
- **Tab 同步**：`channelStore.activeTab` 由 `currentChannelType`（读 `route.params.type`）watch 同步；`watch(activeTab)` 触发 `refreshChannels()`。
- **版本检查**：`checkVersion()`（非桌面端）经 `fetchHealth()` + `versionService`，结果写入 `systemStore.versionInfo`，驱动版本徽章；点击徽章开 UpdateDialog。

## 1. 顶部导航（两套）

### 1.1 数据源 `apiTabOptions`（`useAppController.ts` L59–68）

8 项，供**移动端下拉**使用（`translatedApiTabOptions` 实时翻译）：

| value | i18n labelKey | route | icon |
|---|---|---|---|
| messages | `app.tabs.channels` | /channels/messages | mdi-server-network |
| images | `app.tabs.images` | /channels/images | mdi-image-outline |
| vectors | `app.tabs.vectors` | /channels/vectors | mdi-vector-point |
| conversations | `app.tabs.conversations` | /conversations | mdi-view-dashboard-outline |
| health | `app.tabs.healthCenter` | /health | mdi-stethoscope |
| subscriptions | `app.tabs.subscriptions` | /subscriptions | mdi-cash-multiple |
| cockpit | `app.tabs.cockpitOverview` | /cockpit | mdi-view-dashboard-outline |
| autopilot | `app.tabs.autopilot` | /autopilot | mdi-steering |

### 1.2 桌面端平铺（`App.vue` L100–137，`display.width >= 1000`）

硬编码 9 个 `<router-link>`，`/` 分隔，顺序与移动端一致，**外加第 9 项 cost-report**：

```
渠道 / Images / Vectors / 驾驶舱 / 健康中心 / 订阅中心 / 总览 / Autopilot / 成本报表   API Proxy - CCX
```

i18n key 依次为 `app.tabs.{channels,images,vectors,conversations,healthCenter,subscriptions,cockpitOverview,autopilot}` + 末项**硬编码中文 `成本报表`**。品牌文案 `API Proxy - CCX`（`d-none d-md-inline`）。

> **ego-browser 实测**：桌面端导航 DOM 顺序为 `Channels / Images / Vectors / Cockpit(/conversations) / Health Center / Subscriptions / Overview(/cockpit) / Autopilot / 成本报表`，与代码一致；`成本报表` 确认为硬编码中文。

### 1.3 移动端下拉（`App.vue` L76–97，`display.width < 1000`）

`v-menu` + activator 按钮（显示当前路由 label），列表项 = `translatedApiTabOptions`（8 项）。**不含 cost-report**。

> **ego-browser 实测**（480px 宽度）：桌面平铺链接全部隐藏（`visibleDesktopLinks: []`），头部出现 activator 按钮（如 `CHANNELS`，显示当前路由 label）；点击展开下拉。**移动端无法进入 `/cost-report`**。

### 1.4 头部右侧操作（与页面无关，全局）

版本徽章（<500px 隐藏；桌面 WebUI 隐藏）、语言切换（en/id/zh-CN）、新用户指引（mdi-help-circle）、暗色切换、注销（仅认证后）。

### 1.5 导航层问题

- **tab 清单漂移（P1）**：移动端 8 项 vs 桌面 9 项，**cost-report 仅桌面可达**；移动端无法进入 `/cost-report`。
- **硬编码中文（P2）**：桌面导航"成本报表"未走 i18n（`app.tabs.costReport` key 在 en/zh-CN 两个 locale 均**缺失**）。
- **图标重复（P3）**：conversations 与 cockpit 同用 `mdi-view-dashboard-outline`，移动端下拉辨识度低。

## 2. ChannelsView（`/channels/:type`）

- **文件**：`views/ChannelsView.vue`（86 行，极薄壳）
- **标题/用途**：无自带标题，标题由 App.vue 顶部提供（渠道/Images/Vectors）。统一渠道编排列表（多 LLM 协议已合并，`:type` 实际区分为 messages / images / vectors 三类）。
- **数据流**：
  - 渠道数据：`channelStore.currentChannelsData`（依赖 `activeTab`，由路由 `props.type` 驱动）+ `currentDashboardMetrics/Stats/RecentActivity`。
  - 健康徽标：自带 `loadHealthData()` → `api.getHealthCenterChannels()`，构建 `healthMap`（`channelUid` 主键 + `kind:id` 兜底双写），用 `useGlobalTick(30_000)` 每 30s 轮询。
  - 渠道列表本身由 App 级 5s 轮询刷新（见 §0.4）。
- **主内容区块 / 子组件树**：
  ```
  ChannelsView
  └─ ChannelOrchestration (有渠道时)
       ├─ ChannelStatusBadge / ChannelHealthBadge
       ├─ KeyTrendChart (defineAsyncComponent)
       ├─ ChannelLogsDialog (对话框,略)
       └─ SchedulerDiagnoseDialog (对话框,略)
  └─ 空态 v-card (无渠道时)
  ```
- **空态**：大图标 `mdi-rocket-launch` + `channels.empty.title/description/button`，按钮 `dialogStore.openAddChannelModal()`。
- **加载态**：无独立 spinner（依赖 App 层 overlay；空态在 `channels` 为空时即显示，含"尚未加载"窗口期）。

## 3. ConversationsView（`/conversations`）

- **文件**：`views/ConversationsView.vue`（13 行，纯转发壳）
- **标题/用途**：顶部导航 label `app.tabs.conversations`（zh"驾驶舱"）。实时会话雷达 + 渠道顺序 override。
- **数据流**：核心在 `ConversationDashboard` —— `api.getChannelDashboard(kind)`、`api.getConversations()`、`api.setConversationOverride()`、`api.removeConversationOverride()`。**双轮询**：`useGlobalTick(3000)`（数据 3s）+ `useGlobalTick(1000)`（时钟 1s，驱动相对时间）。
- **子组件树**：
  ```
  ConversationsView
  └─ ConversationDashboard (@success/@error 冒泡到 App toast)
       └─ ConversationCard (会话卡: override/expand/subagent/navigate)
  ```
- **空态**：在 `ConversationDashboard` 内（`cockpit.empty` / `cockpit.noMatches`）。
- **加载态**：无 View 级 spinner（轮询增量更新）。
- 在 App 黑名单 → 隐藏全局统计卡/操作栏。

## 4. HealthCenterView（`/health`）

- **文件**：`views/HealthCenterView.vue`（72 行）
- **标题**：`mdi-stethoscope` + `healthCenter.title` + 刷新按钮。
- **数据流**：`fetchAll()` = `Promise.all([api.getHealthCenterOverview(), api.getHealthCenterChannels()])`，仅 `onMounted` + 手动刷新按钮，**无自动轮询**（对比 ChannelsView 的 health 数据却 30s 轮询，不一致，见 P5）。
- **子组件树**：
  ```
  HealthCenterView
  ├─ HealthCenterStats (overview 总览)
  ├─ 概要行 (totalChannels/totalEndpoints)
  ├─ ProfileChangelogTimeline (Phase 3A 变更时间线)
  └─ HealthChannelTable (channels 表格)
  ```
- **空态**：无专属空态（`overview` 为空时只剩概要/时间线不渲染）。
- **加载态**：`loading && !overview` 时居中 `v-progress-circular`。
- 在黑名单 → 隐藏全局三块。

## 5. SubscriptionsView（`/subscriptions`）

- **文件**：`views/SubscriptionsView.vue`（110 行，含内联弹窗与密集单行函数）
- **标题/用途**：导航 label `app.tabs.subscriptions`（订阅中心）。订阅提供商接入 + 订阅计划管理 + 汇率管理。
- **数据流**：`api.getSubscriptions()`（`onMounted` + 手动刷新）；提供商模板 `getProviderTemplates()`、自动加渠道 `autoAddChannel()`（来自 `services/autopilot-api`）；计费条款 `api.patchSubscriptionBillingTerms()`；同步 `api.refreshSubscription()`；删除 `api.deleteSubscription()`（用 `window.confirm`，见 P6）。无轮询。
- **子组件树**：
  ```
  SubscriptionsView
  ├─ SubscriptionProviderGrid (@select/@add)
  ├─ [内联] addProvider 卡 (v-expand-transition, apiKey 输入)
  ├─ [内联] selectedProvider 卡 (github-copilot=ComingSoon / new-api=NewApiSubscriptionForm)
  ├─ SubscriptionPlanTable (@edit/@refresh/@delete)
  ├─ ExchangeRateManager
  └─ [内联弹窗] 计费条款编辑 / new-api 同步结果 (对话框层,略) + 本地 v-snackbar
  ```
- **空态**：无专属空态（表格空由 SubscriptionPlanTable 处理）。
- **加载态**：`loading` 仅驱动刷新按钮 loading，无整页 spinner。
- **不在** App 黑名单 → 顶部会多余显示渠道统计卡与"添加渠道"操作栏（P4）。
- 持有独立 `v-snackbar`（与 App 全局 toast 并存，提示体系分裂，P7）。

## 6. CockpitView（`/cockpit`）

- **文件**：`views/CockpitView.vue`（435 行，最大的 View，纯卡片网格，无独立区块组件）
- **标题**：`mdi-view-dashboard-outline` + `cockpitOverview.title` + 刷新按钮（一次刷三个 fetch）。
- **数据流**（全部 `onMounted` + 手动刷新，**无轮询**）：
  - `api.getCockpitOverview()` → health / subscriptions / localRuntimes / manualIntents / todoItems 五大汇总。
  - `api.getRecommendations()` → 渠道推荐卡。
  - `api.getManualIntents({all:false})` → 进行中试用意图。
  - `api.deleteManualIntent(uid)` → 结束试用。
- **主内容区块**（全部内联 v-card，无子组件）：
  ```
  CockpitView
  ├─ [健康] 渠道/端点总数 + 6 状态卡 (healthy/degraded/limited/misconfigured/dead/unknown)
  ├─ [订阅] 总数 + balanceByCode 卡 + countByMode/countByTier chips
  ├─ [本地运行时] 总数/模型数 + statusCounts 卡
  ├─ [手动意图] active/total 卡
  ├─ [进行中试用] trial 卡 (可结束) / noActiveTrials
  ├─ [渠道推荐] recommendation 卡 / noRecommendations
  └─ [待办] todoItems 表格 / noTodoItems
  ```
- **空态**：`!loading && !overview` 时大图标 + `cockpitOverview.empty`；各分区另有局部空文案。
- **加载态**：`loading && !overview` 居中 spinner。
- 不在黑名单 → 顶部多余显示渠道统计三块（P4）。

## 7. AutopilotView（`/autopilot`）

- **文件**：`views/AutopilotView.vue`（141 行）
- **标题**：`mdi-steering` + `autopilot.title` + 刷新按钮。
- **数据流**：`fetchAll()` = `Promise.all([api.getSmartRoutingConfig(), api.getAutopilotTraceStats(), api.getAutopilotTraces({limit:50})])`；`api.updateSmartRoutingConfig()` 保存；traces 可单独 `fetchTraces()` 刷新。仅 `onMounted` + 手动，**无轮询**。
- **子组件树**：
  ```
  AutopilotView
  ├─ AutopilotModePanel (全局策略, @update:config)
  ├─ AutopilotDiagnosePanel (路由 dry-run 诊断; 内部并行拉 6 类渠道列表)
  ├─ AutopilotTraceStats (trace 聚合, v-if traceStats)
  ├─ AutopilotTraceTable (trace 列表, @refresh/@select)
  └─ AutopilotTraceDetailDialog (详情, 对话框层,略)
  ```
- **空态**：无专属空态（traces 空由 TraceTable 处理）。
- **加载态**：`loading && !config` 居中 spinner。
- 在黑名单 → 隐藏全局三块。

## 8. CostReportView（`/cost-report`）

- **文件**：`views/CostReportView.vue`（289 行，**全硬编码中文，零 i18n**）
- **标题**：`mdi-cash-multiple` + 硬编码"成本报表" + 刷新/导出 CSV 按钮。
- **数据流**：`api.getCostReport(groupBy, duration, apiType)`，`onMounted` + 每次筛选变更即重新拉取，无轮询。`exportCSV()` 前端拼 CSV Blob 下载（含 BOM）。
- **主内容区块**（无独立区块组件，全内联）：
  ```
  CostReportView
  ├─ 筛选栏: groupBy chips(用户/模型/Key) + duration chips(24h~365d) + apiType v-select
  ├─ 4 汇总卡: 总请求/成功率/总输入Token/官方定价成本(定价不完整警示)
  └─ 数据表: 按 groupKey 分组的请求/成功率/Token/缓存/成本 (含未定价模型 tooltip)
  ```
- **空态**：`!loading && rows.length===0` 大图标 + 硬编码"暂无成本数据 / 需要启用 SQLite 持久化…"。
- **加载态**：`loading && rows.length===0` 居中 spinner。
- 在黑名单 → 隐藏全局三块。
- 硬编码中文（P2 重灾区）：标题、按钮、筛选标签、表头、空态、CSV 表头、pricingHint 全部中文裸写。

## 9. 已知导航 / 信息架构问题汇总

| # | 问题 | 位置 | 影响 |
|---|---|---|---|
| P1 | **Tab 清单漂移**：移动端下拉 8 项、桌面平铺 9 项；`/cost-report` 移动端不可达 | `useAppController.apiTabOptions`(8) vs `App.vue` 桌面(9) | 移动端用户无法进入成本报表 |
| P2 | **硬编码中文未国际化**：① 桌面导航"成本报表"（`app.tabs.costReport` key 在 en/zh-CN 均缺失）；② `CostReportView` 全页中文 | `App.vue` L134；`CostReportView.vue` 全文 | 切英文/印尼语时界面仍中文 |
| P3 | **导航 icon 重复**：conversations 与 cockpit 同用 `mdi-view-dashboard-outline` | `apiTabOptions` | 下拉辨识度低 |
| P4 | **全局统计卡/操作栏路径黑名单不对称**：`/subscriptions`、`/cockpit` 未列入黑名单，却显示"添加渠道"按钮与渠道统计卡，语义不符（**ego-browser 实测确认**） | `App.vue` L240/263/310 | 非渠道页出现渠道操作 |
| P5 | **健康数据轮询不一致**：ChannelsView 的 healthMap 30s 轮询，HealthCenterView 仅手动刷新 | `ChannelsView` vs `HealthCenterView` | 健康中心数据易过期 |
| P6 | **确认体系分裂**：SubscriptionsView 用原生 `window.confirm`，而全站已有 `dialogStore.confirm`（Wails 兼容） | `SubscriptionsView.vue` `deleteItem` | 桌面端 iframe 下 confirm 可能失效 |
| P7 | **提示体系分裂**：SubscriptionsView 自带本地 `v-snackbar`，与 App 全局 toast 并存 | `SubscriptionsView.vue` | 通知样式/位置不统一 |
| P8 | **路由守卫空转**：`beforeEach` 对所有路由 `next()`，`requiresAuth` 实际由 App.vue 处理，存在语义冗余/误导 | `router/index.ts` L67 | 维护者易误以为有路由级鉴权 |
| P9 | **空态覆盖不均**：Channels/CostReport/Cockpit 有专属空态；Health/Subscriptions/Autopilot 依赖子组件，无 View 级空态 | 各 View | 体验不一致 |
| P10 | **i18n 命名重叠易混**：`app.tabs.conversations` 与 `app.tabs.cockpitOverview` 曾同为"驾驶舱"，指向不同页（**已于 2026-08-09 修复**：cockpitOverview 改为 Overview/总览/Ikhtisar） | locale JSON | 用户认知混淆（已解决） |

### 已修复记录

- **P10（2026-08-09, commit c3b9d161）**：`/cockpit` 的 `app.tabs.cockpitOverview` 由"驾驶舱/Cockpit/Kokpit"改为"总览/Overview/Ikhtisar"，`/conversations` 的 `app.tabs.conversations` 保留"驾驶舱/Cockpit"，两个 tab 不再重名。已经 ego-browser 在运行实例（5688）验证生效。

### 关键文件路径清单

- 路由：`frontend/src/router/index.ts`
- 布局/导航/黑名单：`frontend/src/App.vue`
- 控制器/tab 源：`frontend/src/composables/useAppController.ts`
- store：`frontend/src/stores/{channel,auth,system,dialog,preferences}.ts`
- Views：`frontend/src/views/{ChannelsView,ConversationsView,HealthCenterView,SubscriptionsView,CockpitView,AutopilotView,CostReportView}.vue`
- 页面级区块组件：`frontend/src/components/{ChannelOrchestration,ConversationDashboard,ConversationCard,GlobalStatsChart,HealthCenterStats,HealthChannelTable,ProfileChangelogTimeline,AutopilotModePanel,AutopilotDiagnosePanel,AutopilotTraceStats,AutopilotTraceTable,SubscriptionPlanTable,NewApiSubscriptionForm}.vue`、`frontend/src/components/subscriptions/{SubscriptionProviderGrid,ExchangeRateManager}.vue`
- locale：`frontend/src/locales/{en,id,zh-CN}.json`



