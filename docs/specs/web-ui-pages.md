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
黑名单 = ['/conversations', '/health', '/autopilot', '/cost-report', '/subscriptions', '/cockpit']
```

| 全局区块 | 显示条件 | 出现路由 | 隐藏路由 |
|---|---|---|---|
| 全局统计可折叠卡片 `GlobalStatsChart` | `isAuthenticated && !黑名单` | channels/* | conversations、health、autopilot、cost-report、subscriptions、cockpit |
| 三张统计卡片（总渠道/活动渠道/系统状态） | `!黑名单` | 同上 | 同上 |
| 操作栏（添加渠道 / 刷新 / 熔断器 TB） | `!黑名单` | 同上 | 同上 |

> 已在 2026-08-09 修复（commit `d876784d`）：`/subscriptions` 与 `/cockpit` 补进黑名单，两页不再显示渠道统计卡与"添加渠道"操作栏。

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

9 个 `<router-link>` 平铺，`/` 分隔，顺序与移动端一致：

```
渠道 / Images / Vectors / 驾驶舱 / 健康中心 / 订阅中心 / 总览 / Autopilot / 成本报表   API Proxy - CCX
```

i18n key 依次为 `app.tabs.{channels,images,vectors,conversations,healthCenter,subscriptions,cockpitOverview,autopilot,costReport}`。品牌文案 `API Proxy - CCX`（`d-none d-md-inline`）。

> 已在 2026-08-09 修复（commit `d876784d`）：桌面导航末项"成本报表"改为 `t('app.tabs.costReport')`，不再硬编码中文。

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

- **文件**：`views/ChannelsView.vue`（~102 行，极薄壳）
- **标题/用途**：无自带标题，标题由 App.vue 顶部提供（渠道/Images/Vectors）。统一渠道编排列表（多 LLM 协议已合并，`:type` 实际区分为 messages / images / vectors 三类）。
- **数据流**：
  - 渠道数据：`channelStore.currentChannelsData`（依赖 `activeTab`，由路由 `props.type` 驱动）+ `currentDashboardMetrics/Stats/RecentActivity`。
  - 健康徽标：自带 `loadHealthData()` → `api.getHealthCenterChannels()`，构建 `healthMap`（`channelUid` 主键 + `kind:id` 兜底双写）。Phase B.3 起 `useEventStream` 订阅熔断/Key/渠道状态事件（400ms 去抖）即时刷新，`useGlobalTick(30_000)` 轮询降级为兜底。
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

布局示意图（页面内容区 = ChannelOrchestration；App 层对渠道路由另渲染流量统计卡/统计三卡/操作栏，此处从略）：

```
┌────────────────────────────────────────────────────────────────┐
│ ⇅ 渠道编排 〔多渠道模式 chip〕     [routes 诊断] [🔍 搜索渠道... ◌]│
├────────────────────────────────────────────────────────────────┤
│ ▶ 故障转移序列 〔N chip〕   拖拽调整优先级,自动保存(保存中 ◌)     │
│ ┌ 可拖拽行(点击行展开图表;行背景=150 根成功率渐变波形柱)───────┐│
│ │ ⠿ 1 │●active+健康点│ **渠道名**(点击编辑·备注tooltip)〔促销🚀〕││
│ │     │ 〔协议胶囊〕〔来源/池标签〕〔用户标签〕[eye-off] 描述  ⇕ ││
│ │     │ ✔98.2% · 1.2k 请求 · 缓存 67%(tooltip 三组统计)         ││
│ │     │ RPM n/TPM n │ ⏱长延迟 chip │ 🔑3·暂停1·拉黑1            ││
│ │     │            [⏸/▶] [🕘日志] [⋮更多]     ← 点击展开:        ││
│ │     │   ┌ KeyTrendChart(异步,整行宽) ────────────┐            ││
│ └───────────────────────────────────────────────────────┘      │
│ (列表空态: 暂无活跃渠道 · 从下方备用池启用)                      │
├────────────────────────────────────────────────────────────────┤
│ 🗃 备用资源池 〔N chip〕     启用后将追加到活跃序列末尾           │
│ ┌ 行: **渠道名**+协议胶囊+eye-off / 描述 │ 🔑chips               ││
│ │                                  [▶ 启用] [⋮更多] ─────────┘ │
│ (空态: 所有渠道都处于活跃状态 / 搜索时无匹配的备用渠道)          │
└────────────────────────────────────────────────────────────────┘
  行「更多」菜单: 编辑✎ / 复制配置 / 能力测试(LLLM 类) / 抢优先级🚀 /
  置顶⤒ 置底⤓ / 恢复(重置指标) / 移至备用池 / 密钥统计 / 删除🗑(至少保留一个)
  整页空态(无渠道): 圆形头像+🚀 + 暂无渠道配置 + [添加第一个渠道]
```

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

布局示意图（页面内容区 = ConversationDashboard；数据 3s + 时钟 1s 双轮询）：

```
┌──────────────────────────────────────────────────────────────┐
│ [ALL][MESSAGES][CHAT][IMAGES][VECTORS][RESPONSES][GEMINI]     │ ← <400px 降级 v-select
│                                 [🔍 搜索...] [空闲自动恢复 30min ▼]│
├──────────────────────────────────────────────────────────────┤
│ (初次加载 ◌ / 空态卡: 暂无活跃会话… / 筛选无匹配提示)          │
│ ┌── ● 工作中 (n) ──────────┐ ┌── ● 空闲 (n) ─────────────┐    │
│ │ ConversationCard（点击整卡展开）:                              │
│ │ ●LED·[MSG]·标题(tooltip)·N×轮次·时长·[SA n]                   │
│ │ (关系行: 主对话← / 父线程 id / 子代理 n 跳转)                  │
│ │ 摘要: 模型 · 渠道名                                            │
│ │ 折叠态渠道 chips: [✓当前][NEXT绿/TRIPPED红][+N]  ← 点击快速覆盖│
│ │ ┌ 展开 ─────────────────────────────┐                        │
│ │ │ 主对话多轮文本(中段省略,可点全文)   │                        │
│ │ │ Recap 块 · 明细 grid(轮次/时长)     │                        │
│ │ │ (⚠ 覆盖告警: {time}后自动恢复/永不  │                        │
│ │ │  + 恢复默认顺序钮)                  │                        │
│ │ │ 主对话渠道序列: 01→名称[CURRENT/NEXT│                        │
│ │ │ /TRIPPED/PAUSED] 点名称置顶,⬇沉底   │                        │
│ │ │ 子代理列表(+N more) · 子代理渠道序列 │                        │
│ │ │ 底部: 对话 ID 等宽字 [⧉复制]        │                        │
│ └──────────────────────────┘ └───────────────────────────┘    │
└──────────────────────────────────────────────────────────────┘
```

## 4. HealthCenterView（`/health`）

- **文件**：`views/HealthCenterView.vue`（~210 行）
- **标题**：`mdi-stethoscope` + `healthCenter.title` + 刷新按钮。
- **数据流**：`fetchAll()` = `Promise.all([api.getHealthCenterOverview(), api.getHealthCenterChannels()])`。Phase B.3 起改为**事件驱动**：`onMounted` 首次加载 + `useEventStream` 订阅熔断/Key/渠道状态/`upstream_changed` 事件（400ms 去抖）即时刷新，保留手动刷新按钮。
- **子组件树**：
  ```
  HealthCenterView
  ├─ HealthCenterStats (overview 总览)
  ├─ 概要行 (totalChannels/totalEndpoints)
  ├─ drift alerts (manifest_drift / capability_drift 警告条)
  ├─ ProfileChangelogTimeline (Phase 3A 变更时间线)
  └─ HealthChannelTable (channels 表格)
  ```
- **空态**：`EmptyState` 组件（`HealthCenterView.vue:59-66`），无 overview 且非 loading 时显示，文案走 `healthCenter.empty.*` i18n key。
- **Drift alerts**：订阅 `manifest_drift` / `capability_drift` 事件，在表格上方以 `v-alert` 形式展示最近最多 5 条漂移告警，支持关闭。告警标题/消息均使用 `healthCenter.drift.*` i18n key。
- **加载态**：`loading && !overview` 时居中 `v-progress-circular`。
- 在黑名单 → 隐藏全局三块。

布局示意图（事件驱动刷新 + 手动刷新按钮兜底）：

```
┌──────────────────────────────────────────────────────────────┐
│ 🩺 渠道健康中心                                       [⟳ 刷新] │
├──────────────────────────────────────────────────────────────┤
│ │♥健康│⚠降级│限流│配置错│死亡│未知│  ← 6 统计卡(md=2,tonal,图标+数值+标签)│
│ 渠道总数: N · Endpoint 总数: N                                 │
│ (⚠ 漂移告警 ×≤5, closable: 模型清单漂移:{subject} /            │
│   能力探测漂移:{model}({protocol}) + 相对时间 + 正文)           │
│ ┌ 🕘 最近变更 〔实时/连接中… chip〕────────────────────┐       │
│ │ (断连: ⚠ 实时更新已断开,正在重连…)                    │       │
│ │ ◦事件图标 类型标签—channelUid · summary    相对时间    │       │
│ │ (空态: 暂无变更记录)                                   │       │
│ └───────────────────────────────────────────────┘              │
│ ┌ 渠道健康表 v-table(行点击展开) ────────────────────────┐      │
│ │ │状态│类型│渠道│Endpoints│成功率│详情(chevron)│          │      │
│ │ │●ok chip│〔llm〕│名称+健康分布 chips│12│98%(着色)│▾│      │      │
│ │   展开(colspan=6) HealthChannelDetail:                   │      │
│ │   Endpoints 子表(BaseURL/Key/置信度/质量/稳定性/速度/     │      │
│ │   成功率15m/P95延迟/P95首字节/连续失败)+证据/建议折叠面板  │      │
│ └──────────────────────────────────────────────────┘             │
│ (表内空态: 暂无渠道健康数据 / 页面级空态 EmptyState+刷新钮)      │
└──────────────────────────────────────────────────────────────┘
```

## 5. SubscriptionsView（`/subscriptions`）

- **文件**：`views/SubscriptionsView.vue`（~254 行，内联弹窗与轻函数）
- **标题/用途**：导航 label `app.tabs.subscriptions`（订阅中心）。订阅提供商接入 + 订阅计划管理 + 汇率管理。
- **数据流**：`api.getSubscriptions()`（`onMounted` + 手动刷新）；提供商模板 `getProviderTemplates()`（来自 `services/autopilot-api`）；**provider 一键添加走 `channelStore.quickAddFromTemplate(providerId, keys, {kind, placement:'front', displayName})`**（`735b8703`，不再直接调 `autoAddChannel`——store 封装创建 + `refreshChannels` + 按 kind 置顶 + 5 分钟促销期，与渠道列表快速添加行为对齐）；计费条款 `api.patchSubscriptionBillingTerms()`（四字段 `paymentAmount/paymentUnit/creditAmount/creditUnit`）；同步 `api.refreshSubscription()`；删除 `api.deleteSubscription()`（用 `dialogStore.confirm`）。无轮询。
- **子组件树**：
  ```
  SubscriptionsView
  ├─ SubscriptionProviderGrid (@select/@add)
  ├─ [内联] addProvider 卡 (v-expand-transition, apiKey 输入)
  ├─ [内联] selectedProvider 卡 (github-copilot=ComingSoon / new-api=NewApiSubscriptionForm)
  ├─ SubscriptionPlanTable (@edit/@refresh/@delete/@link)
  ├─ ExchangeRateManager
  └─ [内联弹窗] 计费条款编辑 / 订阅关联渠道(linkDialog) / new-api 同步结果
  ```
- **空态**：`EmptyState` 组件，无订阅且非 loading 时由 `SubscriptionPlanTable` 外部兜底显示，文案走 `subscription.empty.*`。
- **加载态**：`loading` 仅驱动刷新按钮 loading，无整页 spinner。
- **不在** App 黑名单 → 顶部会多余显示渠道统计卡与"添加渠道"操作栏（P4 已修复，见 §0.2）。
- **提示/确认体系**：已定义 `success` / `error` emits，通过父级 `App.vue` 全局 toast 提示；删除确认改用 `dialogStore.confirm`，不再保留本地 `v-snackbar`（P6/P7 已修复）。

布局示意图（纵向堆叠：provider 网格 → 订阅管理 → 汇率管理）：

```
┌────────────────────────────────────────────────────────────────┐
│ ┌赞助卡(双倍宽)────────┐┌赞助卡────┐┌赞助卡────┐┌普通卡×N────┐  │
│ │logo 名称〔赞助商 chip〕││  …      ││  …      ││[domain] 名称 │  │
│ │描述(截断)             ││         ││         ││描述          │  │
│ │[添加][官网↗][控制台]   ││         ││         ││[添加]…       │  │
│ ┌Copilot 选择卡────────┐┌new-api 选择卡────────────────┐        │
│ │[github] GitHub Copilot││[server-network] 接入 new-api  │        │
│ │ (点击选中,高亮边框)    ││ (选中→展开内嵌两步表单)        │        │
│ (展开)添加 provider 卡: [API Key 密码框] (error) [取消][添加]   │
│ (展开)选中 provider 卡: copilot→(i)ComingSoon / new-api→表单   │
├────────────────────────────────────────────────────────────────┤
│ 📋 订阅管理                                             [⟳刷新] │
│ (loadError 时 error alert)                                     │
│ ┌ 计划表 v-table compact hover ─────────────────────────┐      │
│ │ │来源│名称+分组摘要│余额│到账规则│版本│绑定渠道│来源│自动刷新││
│ │ │chip│ name        │50k │ 预览  │ 75 │〔uid≤3〕+N│auto│●3m ││
│ │ │    │             │    │       │    │ 未绑定    │    │历史 ││
│ │ 行操作(右对齐): [⟳条件][🔗绑定][✎编辑=到账规则][🗑删除]    ││
│ (空态 EmptyState: 暂无订阅 + 描述)                              │
├────────────────────────────────────────────────────────────────┤
│ 💱 汇率管理                                             [⟳刷新] │
│ 快照版本 v · 构建于 t · USD 派生单价: 1 X = …                   │
│ │来源数量│来源单位│目标数量│目标单位│ [×删除] …          │       │
│                                        [添加报价] [保存 primary]│
│ 叠加对话框: 到账规则(560) / 绑定渠道(560) / 同步结果(760) → dialogs §13│
└────────────────────────────────────────────────────────────────┘
```

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

布局示意图（onMounted 一次性拉取三接口，无轮询；统计卡为 6 列网格）：

```
┌──────────────────────────────────────────────────────────────┐
│ ▦ 驾驶舱总览                                          [⟳ 刷新] │
├──────────────────────────────────────────────────────────────┤
│ ♥ 健康: │渠道数│Endpoint 数│ + │健康│降级│限流│配置错│死亡│未知│ │
│ 💰 订阅: │订阅总数│ 〔余额分布卡 × 币种: CODE/金额〕            │
│          计费模式 chips(按量付费: n…) · 来源等级 chips(一等: n…)│
│ 🖥 本地 Runtime: │Runtime 数│发现模型数│ + statusCounts 着色卡   │
│ 👤 人工意图: │活跃意图│意图总数│                                 │
│ 🧪 活跃试用意图(3 列卡,deep-purple): 〔类型 chip〕〔状态 chip〕  │
│    模型/渠道 UID code │ 剩余 {time} │ 命中·成功率·回退          │
│    [■ 结束 error](loading)   (空: 暂无活跃试用意图。)           │
│ 💡 渠道推荐(3 列卡,success): 〔domain chip〕近期使用 n 次        │
│    当前主用: code(分数) → 推荐尝试: code(分数) │ 综合分差 +δ     │
│    (空: 暂无推荐…)                                              │
│ ⚠ 待办事项 v-table: │Channel│Kind chip│Endpoint│Health chip│Action│
│    (空: 暂无待办事项。;列头硬编码英文,未 i18n)                   │
│ (加载 ◌ / 页面空态: 暂无数据,请先配置渠道和订阅。)               │
└──────────────────────────────────────────────────────────────┘
```

## 7. AutopilotView（`/autopilot`）

- **文件**：`views/AutopilotView.vue`（~152 行）
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

布局示意图（面板堆叠；onMounted + 手动刷新，无轮询）：

```
┌──────────────────────────────────────────────┐
│ ◎ 智能路由 Autopilot                  [⟳刷新] │
├──────────────────────────────────────────────┤
│ 1. AutopilotModePanel     全局策略 (dialogs §12)│
│ 2. AutopilotDiagnosePanel 路由诊断 (dialogs §12)│
│ 3. AutopilotTraceStats   trace 聚合 (v-if 有数据)│
│ 4. AutopilotTraceTable   trace 列表             │
│    ([⟳刷新]; 行 @select → 详情对话框)            │
│ 叠加: AutopilotTraceDetailDialog (dialogs §9)   │
│ (配置加载失败空态: 智能路由未配置 + [刷新])      │
└──────────────────────────────────────────────┘
```

## 8. CostReportView（`/cost-report`）

- **文件**：`views/CostReportView.vue`（~350 行，已全量 i18n）
- **标题**：`mdi-cash-multiple` + `t('costReport.title')` + 刷新/导出 CSV 按钮。
- **数据流**：`api.getCostReport(groupBy, duration, apiType)`，`onMounted` + 每次筛选变更即重新拉取，无轮询。`exportCSV()` 前端拼 CSV Blob 下载（含 BOM）。
- **主内容区块**（无独立区块组件，全内联）：
  ```
  CostReportView
  ├─ 筛选栏: groupBy chips + duration chips + apiType v-select
  ├─ 4 汇总卡: 总请求/成功率/总输入Token/官方定价成本
  └─ 数据表: 按 groupKey 分组的请求/成功率/Token/缓存/成本 + 成本构成列
  ```
- **成本构成列**（`47d6c205`）：每组渲染四类 chip——已确认零成本（`zeroCostCount`）/ 配置倍率（`configuredMultiplierCount`）/ 订阅到账（`subscriptionCostCount`）/ 无定价（`unpricedCostCount`），区分「已确认零成本」与「成本证据缺失」；CSV 导出含对应字段。
- **空态**：`!loading && rows.length===0` 大图标 + `t('costReport.emptyTitle')` / `t('costReport.emptyHint')`。
- **加载态**：`loading && rows.length===0` 居中 spinner。
- 在黑名单 → 隐藏全局三块。
- **i18n**：已新增 43 个 `costReport.*` key（`zh-CN.json:1444-1506`），覆盖标题、按钮、筛选标签、表头、空态、CSV 表头、pricingHint 等，三 locale 对齐。

布局示意图（筛选变更即重拉，无轮询）：

```
┌──────────────────────────────────────────────────────────────┐
│ 💲 成本报表                            [⟳刷新] [⬇ 导出 CSV]   │
├──────────────────────────────────────────────────────────────┤
│ ┌ 筛选卡（横向 flex-wrap）────────────────────────────┐      │
│ │ 分组维度: [用户][模型][Key]（选中 flat primary）     │      │
│ │ 时间范围: [24h][7d][30d][90d][365d]                  │      │
│ │ API 类型 [Messages/Responses/Chat/Gemini/Images/Vectors ▼]│ │
│ ├ 汇总卡 ×4（cols6/sm3,outlined）──────────────────┤         │
│ │ │总请求数│成功率%│总输入 Token│官方定价成本 $│           │      │
│ │  定价不完整→warning 色+"+"后缀+「含未配置定价模型」hint  │      │
│ ├ 数据表（outlined 卡内 v-table hover,中间列右对齐）──┤         │
│ │ │{分组维度 chip}│请求数│成功率│输入│输出│缓存创建│缓存读取│   │
│ │ │官方成本(USD)(粗体·缺定价⚠tooltip)│成本构成:             │   │
│ │ │ 〔零成本 success〕〔配置倍率 primary〕〔订阅到账 info〕    │   │
│ │ │ 〔证据缺失 warning〕×计数 chips                          │   │
│ (加载 ◌ / 空态: 暂无成本数据 · 需启用 SQLite 持久化并有请求记录) │
└──────────────────────────────────────────────────────────────┘
```

## 9. 已知导航 / 信息架构问题汇总

截至最近一次回填，§0–§8 中所有问题已随代码修复同步到正文，以下汇总表保留仅作历史索引。当前剩余可观测/体验类待办已在各专项文档中跟踪，不再重复。

| # | 问题 | 位置 | 状态 |
|---|---|---|---|
| P1 | Tab 清单漂移：移动端 8 项 vs 桌面 9 项，cost-report 仅桌面可达 | `useAppController.apiTabOptions` | ✅ 已修复 |
| P2 | 硬编码中文未国际化 | `App.vue`、`CostReportView.vue`、`locales/*` | ✅ 已修复 |
| P3 | 导航 icon 重复：conversations 与 cockpit 同图标 | `apiTabOptions`、`App.vue` | ✅ 已修复 |
| P4 | 全局统计卡/操作栏路径黑名单不对称 | `App.vue` L240/263/310 | ✅ 已修复 |
| P5 | 健康数据轮询不一致 | `ChannelsView` vs `HealthCenterView` | ✅ 已修复 |
| P6 | 确认体系分裂：SubscriptionsView 用原生 `window.confirm` | `SubscriptionsView.vue` | ✅ 已修复 |
| P7 | 提示体系分裂：SubscriptionsView 自带本地 `v-snackbar` | `SubscriptionsView.vue` | ✅ 已修复 |
| P8 | 路由守卫空转 | `router/index.ts` | ✅ 已修复 |
| P9 | 空态覆盖不均 | `EmptyState.vue`、`HealthCenterView.vue`、`SubscriptionsView.vue`、`AutopilotView.vue`、`locales/*` | ✅ 已修复 |
| P10 | i18n 命名重叠易混：conversations 与 cockpitOverview 曾同为"驾驶舱" | locale JSON | ✅ 已修复 |

### 已修复记录

- **P10（2026-08-09, commit c3b9d161）**：`/cockpit` 的 `app.tabs.cockpitOverview` 由"驾驶舱/Cockpit/Kokpit"改为"总览/Overview/Ikhtisar"，`/conversations` 的 `app.tabs.conversations` 保留"驾驶舱/Cockpit"，两个 tab 不再重名。
- **P1 / P2 / P4（2026-08-09, commit d876784d）**：
  - P1 移动端 cost-report 可达（`apiTabOptions` 补项 + `mdi-finance` 图标注册）。
  - P2 成本报表国际化（`app.tabs.costReport` + 43 个 `costReport.*` key ×3 locale，`CostReportView` 与导航改用 `t()`）。
  - P4 黑名单补 `/subscriptions` `/cockpit`。
- **P5 / P9（2026-08-09，Phase B.3 及后续）**：ChannelsView 与 HealthCenterView 统一接入 `useEventStream` 事件驱动刷新；新增 `EmptyState` 补齐 Health/Subscriptions/Autopilot 三页 View 级空态。
- **P6 / P7（2026-08-09）**：`SubscriptionsView` 删除改用 `dialogStore.confirm`；`success`/`error` emits 统一走 App 全局 toast，移除本地 `v-snackbar`。
- **P8（2026-08-09）**：移除对所有路由一律 `next()` 的空转 `beforeEach`，鉴权由 `App.vue` 持久认证对话框承担。
- **CapabilityTestDialog 接线（2026-08-09, commit d876784d）**：见 [web-ui-dialogs.md §16.1](./web-ui-dialogs.md)。

### 关键文件路径清单

- 路由：`frontend/src/router/index.ts`
- 布局/导航/黑名单：`frontend/src/App.vue`
- 控制器/tab 源：`frontend/src/composables/useAppController.ts`
- store：`frontend/src/stores/{channel,auth,system,dialog,preferences}.ts`
- Views：`frontend/src/views/{ChannelsView,ConversationsView,HealthCenterView,SubscriptionsView,CockpitView,AutopilotView,CostReportView}.vue`
- 页面级区块组件：`frontend/src/components/{ChannelOrchestration,ConversationDashboard,ConversationCard,GlobalStatsChart,HealthCenterStats,HealthChannelTable,ProfileChangelogTimeline,AutopilotModePanel,AutopilotDiagnosePanel,AutopilotTraceStats,AutopilotTraceTable,SubscriptionPlanTable,NewApiSubscriptionForm}.vue`、`frontend/src/components/subscriptions/{SubscriptionProviderGrid,ExchangeRateManager}.vue`
- locale：`frontend/src/locales/{en,id,zh-CN}.json`



