# Web UI 对话框设计文档

> 本文档覆盖 CCX Web 管理界面中所有对话框、弹窗、面板类组件的布局、交互、状态流转与后端调用关系。

## 0. 组件清单

扫描 `frontend/src/components/` 与 `frontend/src/views/`，命中的对话框/弹窗/面板类组件如下（无 Drawer 后缀组件；全部基于 Vuetify `v-dialog`）。

| 组件 | 路径 | 类型 |
|------|------|------|
| AddChannelModal | `frontend/src/components/AddChannelModal.vue` | Modal（v-dialog） |
| EditChannelModal | `frontend/src/components/EditChannelModal.vue` | Modal（v-dialog，大型分区表单） |
| CapabilityTestDialog | `frontend/src/components/CapabilityTestDialog.vue` | Dialog |
| ChannelLogsDialog | `frontend/src/components/ChannelLogsDialog.vue` | Dialog |
| SchedulerDiagnoseDialog | `frontend/src/components/SchedulerDiagnoseDialog.vue` | Dialog |
| AutopilotTraceDetailDialog | `frontend/src/components/AutopilotTraceDetailDialog.vue` | Dialog |
| UpdateDialog | `frontend/src/components/UpdateDialog.vue` | Dialog |
| UserGuideDialog | `frontend/src/components/UserGuideDialog.vue` | Dialog（多步引导） |
| NewApiQuickAddDialog | `frontend/src/components/subscriptions/NewApiQuickAddDialog.vue` | Dialog（包裹表单） |
| AutopilotModePanel | `frontend/src/components/AutopilotModePanel.vue` | Panel（内嵌卡片，非弹窗） |
| AutopilotDiagnosePanel | `frontend/src/components/AutopilotDiagnosePanel.vue` | Panel（内嵌卡片，非弹窗） |
| NewApiAccountPanel | `frontend/src/components/edit-channel/NewApiAccountPanel.vue` | Panel（EditChannelModal 内区块） |

以 `Form` 结尾但充当弹窗内容的：`NewApiSubscriptionForm.vue`、`QuickAddChannelForm.vue`。

匿名内联弹窗（未拆成独立组件）：
- 熔断器配置对话框 — `App.vue:399`
- 添加 API 密钥对话框 — `App.vue:610`
- 通用确认对话框 — `App.vue:636`
- 认证登录对话框 + 自动认证 overlay — `App.vue:18` / `App.vue:4`
- 分组模型策略 / Key 倍率对话框 — `ApiKeyManagementSection.vue:1290` / `:1342`
- 计费条款编辑 / 订阅关联渠道 / 同步结果对话框 — `SubscriptionsView.vue:49` / `:64` / `:102`

> 载入点：`AddChannelModal`、`EditChannelModal`、`UpdateDialog`、`UserGuideDialog` 均在 `App.vue` 挂载并由 `useAppController.ts` 驱动；`ChannelLogsDialog`、`SchedulerDiagnoseDialog` 挂载在 `ChannelOrchestration.vue`；`AutopilotTraceDetailDialog` 同时挂载在 `AutopilotView.vue` 与 `ChannelLogsDialog.vue`。

> `CapabilityTestDialog` 已接线（`App.vue` 挂载 + `ChannelOrchestration` 行操作触发，见 §16.1）。

## 1. AddChannelModal（快速/标准添加渠道）

- 路径：`frontend/src/components/AddChannelModal.vue`
- 用途：新建自定义渠道，两种模式：标准（textarea 粘贴解析 baseURL+key）与快速（选 provider 模板 + 输 key）。
- 触发入口：`App.vue:317` 主操作栏「添加渠道」按钮 → `openAddChannelModal` → `dialogStore.openAddChannelModal()`；空状态页 `ChannelsView.vue:25` 「立即添加」也调用同一 store 方法。
- 关键 props / emits：
  - props：`show: boolean`、`channelType?: ChannelType`（默认 `messages`）
  - emits：`update:show`、`save(channel, options?)`、`error(message)`、`autoAdded(channelId)`
- 主要字段：模式切换 `v-btn-toggle`（`quickAddMode`）；标准模式 `quickInput` textarea + **代理 URL 输入框（可选，`standardProxyUrl`，占位 `http://127.0.0.1:7890 或 socks5://…`，带 `mdi-vpn` 前置图标，随 `discoverFast`/`autoAddChannel` 透传 `proxyUrl`；876eaf7e 起另有 `standardProxyPreferDirect` 直连优先开关一并透传）** + 探测状态卡（检测到的 baseUrls/apiKeys/自动生成渠道名）；故障转移位置开关 `placement`（front/back）。
- 操作按钮：取消（Esc）、创建渠道（⌘/Ctrl+Enter）。提交按 `handleSubmitByMode` 分派：快速模式委托子表单 `quickAddFormRef.handleSubmit()`，标准模式走 `handleQuickSubmit`。
- 校验/状态：`isQuickFormValid`（copilot 仅需 baseUrls；其余需 baseUrls+apiKeys）；`standardSubmitting` 加载态、`standardSubmitError` 内联错误；重复渠道 `duplicateChannel` 提示。
- 后端调用：`discoverFast`（快速协议探测）→ `autoAddChannel`（`autopilot-api.ts`）；copilot 走 `emit('save', …)` 交由 store。
- 联动：内嵌 `QuickAddChannelForm`（`v-model:placement`，`@added→onQuickAddSuccess`）。

布局示意图：

```
┌───────────────────────────────────────────┐
│ ⊕  创建渠道 / 快速接入                     │  ← 头部（主色/暗色自适应）
├───────────────────────────────────────────┤
│         [ 标准模式 | 快速模式 ]  (toggle)   │
│ ── 标准 ──────────────  或  ── 快速 ─────── │
│ ┌ textarea 粘贴 baseURL+key ┐  │ <QuickAddChannelForm/> │
│ │                           │  │  provider下拉         │
│ └───────────────────────────┘  │  baseURL / apiKeys    │
│ ┌ 代理 URL (可选) ──────────┐  │  代理 URL (可选,自定义)│
│ ┌ 检测状态卡 ───────────────┐  │  故障转移开关          │
│ │ ✔ BaseURL  期望请求        │                          │
│ │ 渠道名(自动) | API Keys    │                          │
│ │ 故障转移位置 [switch]      │                          │
│ └───────────────────────────┘                          │
│ [!] 重复/错误 alert                                     │
├───────────────────────────────────────────┤
│                     [取消 Esc] [创建 ⌘Enter]│
└───────────────────────────────────────────┘  max-width 800
```

## 2. EditChannelModal（渠道编辑器，最复杂）

- 路径：`frontend/src/components/EditChannelModal.vue`（模板+装配），逻辑在 `frontend/src/composables/useEditChannelModal.ts`
- 用途：编辑既有渠道 / 托管账号；左侧分区导航 + 右侧滚动表单。既服务于 create 也服务于 edit（`dialogMode`）。
- 触发入口：`App.vue:369` 挂载，`v-model:show=dialogStore.showEditChannelModal`；`ChannelOrchestration.vue` 渠道名点击 / 菜单「编辑」`$emit('edit', channel)` → `editChannel` → `dialogStore.openEditChannelModal(channel)`。
- 关键 props / emits：
  - props：`show`、`channel?: Channel|null`、`channelType?`
  - emits：`update:show`、`save(channel, options?, onComplete?)`、`error`、`success`、`updated`、`update:api-key-configs`
- 分区（`useEditChannelSectionNav.ts`，侧导航固定三项：basic/auth/custom）：
  1. basic（基础信息）— `BasicInfoSection`（多行 baseUrls（条件可编辑，见下）、官网 website + 快捷按钮、渠道备注输入（12e52cf9 恢复，≤10 字符，与渠道名称解耦））+ `ProtocolModelAvailability`（协议模型清单/重新发现）
  2. auth（认证管理）— `ApiKeyManagementSection`（密钥增删、**拖拽排序与置顶/置底**、复制、暂停/恢复、拉黑恢复、**每 Key 模型数 chip**、分组模型策略、Key 倍率、provider 凭证如 volcengine/kimi/mimo/compshare/minimax、copilot OAuth）
  3. custom（自定义参数）— 代理服务器 `form.proxyUrl`（v-text-field，clearable，`mdi-vpn` 前置图标）+ **代理直连优先开关 `form.proxyPreferDirect`**（876eaf7e，仅填写代理后有意义）+ `CustomHeadersSection` + **渠道计费四字段**（充值币种/充值金额/渠道币种/到账金额）
  - accounts 区（仅 new-api / generic 托管，`EditChannelModal.vue:107` 的 `v-if`）仍在 DOM 中渲染 `NewApiAccountPanel`，但**不进侧导航**
  - redirect / advanced 分区已随 09c4996d「白名单字段精简」删除：`ModelMappingSection`、`ModelCapabilitySection`、`EmbeddingCompatibilitySection`、`SupportedModelsFilter`、`AdvancedOptionsSection`、`TransportConfigGroup`、`StreamTimeoutSection`、`RateLimitGroup` 共 8 个子组件整体移除
- 编辑副标题按渠道来源三选一：官方直连 `managed.editSubtitle`（"{provider} 官方渠道 · 管理账号凭证"）、provider 模板 `providerEditSubtitle`、自定义托管 `customEditSubtitle`
- baseUrls 可编辑条件（70249bbf）：仅「自定义手填地址托管渠道」（`isEditableBaseUrlsChannel = !providerId && !isOfficialProviderChannel`，`useEditChannelModal.ts:95`）可编辑地址池；provider 模板托管（火山/Kimi 等）与官方直连渠道隐藏地址输入（`hide-base-url`），保存时沿用渠道当前值。前端已不再区分手动/托管渠道分支（7adf46a2 清理遗留代码）
- 渠道计费四字段（4ab0b99e → 49f28b3e → 5e976904 最终形态）：`channelPaymentCurrency`（充值币种，如 LDC/CNY/USD）、`channelPaymentAmount`（充值金额）、`channelCreditCurrency`（渠道币种，如 USD）、`channelCreditAmount`（到账金额）；空值/非正数归 0（不参与计算），后端按全局汇率图计算 `EffectiveMultiplier = (充值金额×充值币价)/(到账金额×渠道币价)` 并复用 `ResolveEffectiveCostUSD`
- 主要状态流转：
  - `watch(props.show)`：打开时 `dialogMode = channel ? 'edit' : 'create'`，编辑走 `loadChannelData(channel)`，新建走 `resetForm()`；`nextTick(attachScrollListener)` 绑定滚动高亮。
  - 编辑打开且渠道有任何 Key（含禁用 Key）即 `nextTick(fetchTargetModels())` 预拉上游模型（eee81e0b，恢复每 Key 模型数 chip 展示）；分组模型对话框打开时经 `ensure-models-loaded` 懒加载兜底。
  - `baseUrlsText` watch → `syncBaseUrlsFormState` 去重 + `extractChannelNamePrefix` 自动派生渠道名。去重语义经 6cda4596/d300b2bb 修正：转义路径保留原样，带 `#` 的完整路径 URL 不与域名根条目去重合并（`utils/base-url-semantics.test.ts`）。
  - `handleSubmit`：`formRef.validate()` → `buildSubmitPayload` → `emit('save', …, onComplete)`。
- 校验规则：`isFormValid` 只综合三项——serviceType 非空、baseUrls（仅 `isEditableBaseUrlsChannel` 时要求非空且逐行合法 URL）、apiKeys（copilot 免除）；模型能力错误不再参与。
- 加载态：`submitting`、`fetchingModels`、`managedModelsLoading`、`keyModelsStatus`（每 Key 模型数探测状态 Map）。
- 后端调用：`getManagedAccounts`、`useTargetModelFetch` 拉取上游模型、`useDisabledApiKeys` 系列。保存走 `useAppController.saveChannel` → `channel store.saveChannel`。
- 保存白名单语义（09c4996d）：`buildSubmitPayload` 对其余全部元数据字段「沿用渠道当前值」，仅 `customHeaders/proxyUrl/proxyPreferDirect/remark/计费四字段/website/baseUrls(条件)` 取表单值——这也是 proxyUrl/customHeaders/remark 保存丢失 bug 的修复方式。
- 联动：`ProtocolModelAvailability @refreshed` → 重新拉账号模型 + `refreshEditingChannelAfterRediscovery`；`NewApiAccountPanel @updated → emit('updated')`；`ApiKeyManagementSection @update:api-key-configs` 同步重排后的 Key 配置项。
- `routePrefix` 已不可在编辑弹窗修改（TransportConfigGroup 已删），保存时仅沿用渠道当前值；其调度语义（默认路由 vs 前缀路由）见 §8 SchedulerDiagnoseDialog，仅调度诊断对话框可模拟。


布局示意图：

```
┌──────────────────────────────────────────────────────────┐
│ [蓝底白字渠道身份块] v-avatar 图标 + identityLabel/       │
│                      identityName（1af4ffd3/ed1b55fd；   │
│                      托管徽章已移除）                     │
├────────────┬─────────────────────────────────────────────┤
│ 侧栏导航    │ (右侧可滚动内容区 .content-area)              │
│ • 基本信息  │  [basic]   BaseInfo(条件baseUrls/官网/备注)   │
│            │            + ProtocolModelAvailability       │
│ • 认证管理  │  [auth]    ApiKeyManagementSection           │
│            │            (拖拽排序/模型数chip/倍率/OAuth)   │
│ • 自定义    │  [custom]  代理(+直连优先) / CustomHeaders /  │
│            │            计费四字段                          │
│            │  [accounts] NewApiAccountPanel(不进侧导航)   │
├────────────┴─────────────────────────────────────────────┤
│                                   [取消 Esc] [保存 ⌘Enter] │
└──────────────────────────────────────────────────────────┘  max-width 1030（9b187f8b 收窄）, scrollable
```

### 2.1 ApiKeyManagementSection 关键交互

- **Key 列表拖拽排序与置顶/置底**（85f362ba，`vuedragnable`）：活跃 Key 多于 1 个（`canReorderKeys`）时，列表用 `<draggable>` 包裹，仅行首 `mdi-drag-vertical` 把手可拖；被拉黑（disabled）Key 不可拖、固定展示。每行另有置顶/置底按钮（`moveKeyToTop/moveKeyToBottom`，首/末位置禁用）。顺序即 `APIKeys`/`APIKeyConfigs` 的 slice 顺序（无显式 sort 字段），重排后同步 emit `update:apiKeys` 与 `update:apiKeyConfigs` 保证 Key 与配置项一一对应。
- **每 Key 模型数 chip**（eee81e0b）：每个活跃 Key 行标题区、Key 掩码右侧三个互斥状态 chip——加载中 / 成功（「models {statusCode} ({count} 个)」）/ 失败（「models {code}」+ error tooltip），数据源 `useTargetModelFetch` 的 `keyModelsStatus` Map。
- **分组模型策略 / Key 倍率**：见 §13 内联对话框。new-api key 的 groupMultiplier 由远端同步，手动修改被后端 409 拒绝。

## 3. QuickAddChannelForm（AddChannelModal 快速模式子表单）

- 路径：`frontend/src/components/QuickAddChannelForm.vue`
- 用途：模板化添加（选 provider + 输 key，系统判 plan/baseURL）；也支持自定义（手填 baseURL）。
- 触发入口：由 `AddChannelModal` 在 `quickAddMode=true` 时渲染。
- props / emits：props `channelType`、`existingChannels?`、`placement?`；emits `added(channelId)`、`close`、`update:placement`。`defineExpose({ handleSubmit, resetForm, isFormValid, submitting })` 供父组件调用。
- 主要字段：provider `v-select`（赞助商 volcengine/compshare/runapi 置顶，末尾「new-api 通用接入」与「自定义」；**不按当前协议 tab 过滤**——任意 tab 下拉均显示全部 provider，fc7cc04f）、多 baseURL 输入（`recognizedBaseUrls` 识别提示）、多 apiKey 输入（显隐切换）、自定义模式**代理 URL 输入（可选，`mdi-vpn` 前置图标，随 `discoverFast`/`autoAddChannel` 透传，provider 模式不传）**、故障转移开关、自动生成渠道名预览、重复渠道 alert、提交错误 alert、创建中进度卡。
- 校验/状态：`isQuickFormValid`（provider 模式仅需 key；自定义需 baseUrls+key）；`submitting`、`submitError`、`providerTemplatesLoading`。
- 后端调用：`getProviderTemplates`、自定义模式 `discoverFast` → `autoAddChannel`；provider 模式直接 `autoAddChannel({providerId, apiKeys, kind: provider.channelKind})`（按 provider 自身声明的 channelKind 提交，自动创建该 provider 支持的全部渠道，避免 tab 与 provider 能力不匹配导致 400）。
- 联动：选中 `__new_api__` → 打开 `NewApiQuickAddDialog`，其 `@created` → `emit('added', channelIndex)`。

## 4. NewApiSubscriptionForm + NewApiQuickAddDialog（new-api 两步接入）

- 路径：`frontend/src/components/NewApiSubscriptionForm.vue`、`frontend/src/components/subscriptions/NewApiQuickAddDialog.vue`
- 用途：验证 new-api 实例 → 接入订阅并落地渠道。两步流程（step1 验证、step2 接入）。
- 触发入口：
  - Dialog 版：`QuickAddChannelForm` 选 new-api 时 `openDialog()`。
  - 内联版：`SubscriptionsView.vue:25` provider 选择区直接内嵌 `NewApiSubscriptionForm`。
- NewApiQuickAddDialog props/emits：emits `created(result)`、`error(message)`；内部 `dialogVisible`，`handleCreated` 关闭对话框并上抛。
- NewApiSubscriptionForm 字段：
  - step1 验证 `verifyForm`：baseUrl、accessToken(password)、userId、authTokenMode(bearer/raw)、displayName、`proxyUrl`（可选）+ `proxyPreferDirect` 开关（876eaf7e，verify/provision 均透传）；验证后展示账户预览（username/quota/usedQuota/可用模型数/分组倍率 chips）。
  - step2 接入 `provisionForm`：subscriptionUid、channelKind(messages/chat/…)、channelName、`maxGroupMultiplier`(number)、notes；分组资格 alert（blockedGroupCount / eligibleGroupItems / groupFetchError / noEligibleGroups）。
- 校验：`canVerify`（baseUrl+accessToken 非空）；`canProvision`（subscriptionUid + channelKind + `maxGroupMultiplierValid` + `eligibleGroupItems.length>0`）。
- 加载态：`verifying`、`provisioning`。
- 后端调用：`api.verifyNewApiSubscription`（step1），`api.provisionNewApiSubscription`（step2）。验证成功后自动预填 step2 表单。
- 联动：`created` → 上游 `QuickAddChannelForm.onNewApiCreated`（emit added）或 `SubscriptionsView.handleNewApiCreated`（刷新订阅列表）。

布局示意图：

```
┌───────────────────────────────────┐
│ 🖧 接入 new-api                     │
├───────────────────────────────────┤
│ <NewApiSubscriptionForm>            │
│  Step1 验证: baseUrl/token/userId…  │
│   [验证]                            │
│  [账户预览卡: quota/groups chips]   │
│  ── divider ──                      │
│  Step2 接入: uid/kind/name/倍率…    │
│   [资格 alert] [接入]               │
├───────────────────────────────────┤
│                            [取消]   │
└───────────────────────────────────┘  max-width 680, persistent
```

## 5. NewApiAccountPanel（EditChannelModal accounts 区）

- 路径：`frontend/src/components/edit-channel/NewApiAccountPanel.vue`
- 用途：管理 new-api 主账号凭证 + 多子账号（余额、密钥掩码、分组倍率 chips）；generic 未绑定时提供绑定表单。
- 触发入口：`EditChannelModal` 在 `isNewApiChannel || isGenericAutoManagedChannel` 时渲染。
- props：`subscriptionUid`、`channelName?`、`baseUrl?`、`channelUid?`、`channelKind?`、`isGeneric?`、`autoManagedKind?`、`channelProxyUrl?`、`channelProxyPreferDirect?`（代理以渠道「代理通道」为唯一事实源，面板仅继承展示 `channelProxyHint`，表单不再单独配置代理）；emit `updated`。
- 订阅 UID 解析：`effectiveSubscriptionUid`（行 470）= `props.subscriptionUid || localSubscriptionUid || 兜底`；兜底仅在非 generic 且 `autoManagedKind==='new_api'` 时按约定推导 `newapi-${channelUid}`（行 467，787db651）——key 配置 `sourceSubscriptionUid` 丢失（编辑换 key 切断关联）时面板不瘫痪。watch 依赖 `[subscriptionUid, channelUid, autoManagedKind, isGeneric]` 重置并重拉。
- 分支视图：
  - generic 未绑定：`bindForm`（accessToken/userId/authTokenMode），`canBindNewApi` 校验；**先 `verifyNewApiSubscription` 获取 `groups + availableModels`，再按统一倍率阈值提交 `provisionAllEligibleGroups=true`**。
  - 已绑定（40d8b990 起主账号并入列表）：账号列表首行=主账号行（站点用户名 +「主账号」徽章 + 余额/脱敏 token/Key 数；点击展开详情 grid：用户名/用户 ID/余额/已用/最近刷新/令牌模式/站点/脱敏 token + 自动接入 Key chips + 最近刷新错误 alert + 更新凭证表单 `primaryForm`→`savePrimaryCredentials`；行上刷新按钮 `refreshingPrimary`→`refreshPrimaryAccount`；主账号不可删除故无删除按钮），其后为子账号行 v-for（`accounts`，62d2d46d 起同样支持展开详情 + 独立更新凭证 `accountForms(accountUid)`→`saveAccountCredentials`，行操作刷新/删除），再后为展开面板「添加账号」`addForm`；**追加账号同样先 verify，再显式传 `provisionModels: verified.availableModels`**。
  - 空态/错误（787db651）：列表上方 `primaryError`（GET 订阅 404 时显示 `subscriptionNotFound` 文案）或 `primaryAccountUnavailable` 提示；`handleAddAccount` 在主账号订阅未就绪时置 `addError`（`subscriptionUnavailable`）而非静默返回。
- 校验/状态：`canBindNewApi`、`primaryCredentialsChanged`、`accountCredentialsChanged`；loading：`binding`/`savingPrimary`/`refreshingPrimary`/`adding`/`refreshing`/`deleting`/`loadingPrimary`/`savingAccount`；错误：`bindError`/`primaryError`/`addError`/`accountErrors`；展开态：`expandedPrimary`/`expandedAccountUid`。`groupFetchError`、无合格组、verify 失败时阻断提交。
- 后端调用：`verifyNewApiSubscription`、`provisionNewApiSubscription`、`getSubscription`、`updateNewApiCredentials`、`refreshSubscription`、`getSubscriptionAccounts`、`addSubscriptionAccount`、`refreshSubscriptionAccount`、`deleteSubscriptionAccount`、`updateSubscriptionAccountCredentials`。

## 6. ChannelLogsDialog（渠道请求日志）

- 路径：`frontend/src/components/ChannelLogsDialog.vue`
- 用途：查看单渠道最近 50 条请求日志（状态码、协议、reasoning effort、时延、熔断依据），3s 轮询。
- 触发入口：`ChannelOrchestration.vue:457` 行操作「历史」按钮 → `openLogsDialog(channel)`。
- props：`modelValue`、`channelIndex`、`channelName`、`channelType`、`protocolRoutes?`；emit `update:modelValue`。
- 主要内容：加载态 spinner、空态（含熔断依据 alert）、日志列表（状态码 chip、请求状态、interfaceType、agentRole、operation、requestSource、模型映射、reasoning、keyMask、baseUrl、时延分解、可展开 errorInfo、复制单条、autopilotTrace chip）。
- 状态流转：`watch(modelValue)` 打开时清空并 `fetchLogs` + 开启轮询（`useGlobalTick(3000)`）；切换 channel/type/routes 重新拉取；关闭停止轮询。
- 后端调用：`api.getChannelLogs(kind, index)`（对每条 protocolRoute `Promise.allSettled`，合并去重取前 50）。
- 联动：日志 autopilotTrace chip → `openAutopilotTrace` 打开内嵌 `AutopilotTraceDetailDialog`。

## 7. CapabilityTestDialog（能力测试结果）

- 路径：`frontend/src/components/CapabilityTestDialog.vue`；管理器 `frontend/src/composables/useCapabilityTestManager.ts`
- 用途：展示渠道多协议能力测试（messages/chat/responses/gemini + 复合协议 `a->b`）；移动端卡片 / 桌面端表格双布局，含 RPM 调节、协议级重测、单模型重试、复制到其他协议 tab。
- 触发入口：已在 `App.vue` 挂载并由 `ChannelOrchestration` 行操作菜单触发；管理器暴露 `testChannelCapability(target)`。
- props：`modelValue`、`channelName`、`currentTab`、`capabilityJob: CapabilityTestJob|null`、`capabilityRpm`；emits：`update:modelValue`、`update:capabilityRpm`、`copyToTab(target, service?)`、`cancel`、`retryModel(protocol, model)`、`testProtocol(protocol)`。
- 状态机：`initializing/idle/pending/running/completed/cancelled/error`。
- 子组件：`CapabilityModelResults`（模型徽章 + tooltip，点击重试）。
- 后端调用（管理器）：`startChannelCapabilityTest`、`getChannelCapabilitySnapshot`、`getChannelCapabilityTestStatus`（轮询）、`cancelCapabilityTest`、`retryCapabilityTestModel`。

## 8. SchedulerDiagnoseDialog（调度诊断）

- 路径：`frontend/src/components/SchedulerDiagnoseDialog.vue`
- 用途：手工构造请求画像，dry-run 调度器选路，展示 selected/stages/candidates 表。
- 触发入口：`ChannelOrchestration.vue:54` 标题栏 mdi-routes 图标按钮。
- props：`modelValue`、`channelType`；emit `update:modelValue`。
- 字段：model、userId、routePrefix、channelName、failedChannels、agentRole、inputTokens/outputTokens/requiredTokens、hasImageContent/explicitOutputMax/skipWindowValidation。
- `routePrefix` 字段语义：
  - 留空表示模拟默认路由请求，即只看 `RoutePrefix == ""` 的渠道候选集。
  - 填写 `foo` 这类裸前缀值表示模拟 `/:routePrefix/...` 入口请求，只看 `RoutePrefix == "foo"` 的渠道候选集。
  - 诊断 trace 中的 `default_route_filter` / `route_prefix_filter` 分别对应上述两类过滤。
- 操作：运行（`runDiagnose`）、清除（`clearResult`）。加载态 `isRunning`。
- 后端调用：`api.diagnoseSchedulerSelection(channelType, payload)`。

## 9. AutopilotTraceDetailDialog（路由 Trace 详情）

- 路径：`frontend/src/components/AutopilotTraceDetailDialog.vue`
- 用途：展示单条 autopilot trace（身份/策略快照、请求画像、候选与决策、scheduler 裁决、endpoint 尝试、终态）。
- 触发入口：`AutopilotView.vue:55`（`AutopilotTraceTable @select`）；`ChannelLogsDialog.vue:220`（日志 trace chip）。
- props：`modelValue`、`traceUid`；emit `update:modelValue`。
- 状态：`loading`、`notFound`（404）、`fetchError`（可重试），`detail: TraceDetailV2`。
- 候选表含 Model 列（78ed757f 路由候选按 (渠道, 模型) 展开，同名承接行经 CandidateKey 回退解析模型名）；scheduler 裁决区含**被滤渠道明细表**（16dc0fda：ChannelIndex/ChannelName/Stage/Reason/Details，来自 `schedulerDecision.skippedCandidates`）。
- 后端调用：`api.getAutopilotTraceDetail(traceUid)`。

## 10. UpdateDialog（OTA 版本检查）

- 路径：`frontend/src/components/UpdateDialog.vue`
- 触发入口：`App.vue:378`（`v-model=systemStore.updateDialogOpen`）；版本徽标点击 `handleVersionClick`。
- props：`modelValue`；emit `update:modelValue`。
- 字段/内容：当前版本、最新版本 chip、状态 alert（error/hasUpdate/upToDate）。
- 操作：检查更新（`handleCheck` 派发 `ccx-check-version` 事件）、下载（`releaseUrl` 外链）。

## 11. UserGuideDialog（新用户 4 步引导）

- 路径：`frontend/src/components/UserGuideDialog.vue`
- 触发入口：`App.vue:381`（`v-model=showGuide`）；`App.vue:210` 帮助按钮；`useAppController.ts:277` 首次认证成功且非嵌入自动弹出一次。
- props：`modelValue`；emit `update:modelValue`。
- 内容：4 步（欢迎/协议切换示意/添加渠道示意/渠道列表示意）。
- 状态：`step`，`watch(modelValue)` 打开归零；键盘 Esc 关闭、Enter 下一步/完成。

## 12. AutopilotModePanel / AutopilotDiagnosePanel（内嵌面板，非弹窗）

- 路径：`frontend/src/components/AutopilotModePanel.vue`、`AutopilotDiagnosePanel.vue`
- 触发入口：均由 `AutopilotView.vue` 直接内嵌渲染。
- AutopilotModePanel：props `config: SmartRoutingConfig`、`saving`；emit `update:config`。字段：killSwitch(只读开关+警告 alert)、costPreference(select)。
- AutopilotDiagnosePanel：无 props；本地 `form`（model/channelKind/agentRole/estTokens/toolUseNeed/reasoningNeed/hasImage）。结果：mode/taskClass/candidates 表（候选行为 (渠道, 模型) 粒度并展示 CandidateKey/模型名，78ed757f）。

## 13. 内联对话框（App.vue / 子区块 / 视图）

- 熔断器配置（`App.vue:399`）：三组滑块 + 预设 gentle/balanced/aggressive/custom。后端 `getCircuitBreaker`/`setCircuitBreaker`。
- 添加 API 密钥（`App.vue:610`）：`newApiKey` 输入，Enter 添加。
- 通用确认对话框（`App.vue:636`）：`dialogStore.confirm({message,confirmText,cancelText,color})` 返回 Promise。
- 认证登录（`App.vue:18`）+ 自动认证 overlay（`App.vue:4`）：`showAuthDialog` computed。
- 分组模型策略 / Key 倍率（`ApiKeyManagementSection.vue:1290`/`:1342`）：`openGroupModelEditor`/`submitGroupModelDisable`；`openMultiplierEditor`/`saveMultiplier`。
- 计费条款 / 订阅关联渠道 / 同步结果（`SubscriptionsView.vue:49`/`:64`/`:102`）：`billingDialog`（四字段 paymentAmount/paymentUnit/creditAmount/creditUnit，a96098da 统一币种/金额模型）、`linkDialog`（v-select 选 `linkableChannels` + 已关联 channelUid chips 逐个解绑 `unlinkChannel`，入口 SubscriptionPlanTable 行操作）、`syncDialog`。

## 14. 对话框跳转/联动关系图

```
App.vue ─┬─ openAddChannelModal ─▶ AddChannelModal
         │        └─(quick模式)─▶ QuickAddChannelForm ─(选new-api)─▶ NewApiQuickAddDialog ─▶ NewApiSubscriptionForm
         │                                                                      │ created
         │                                                                      ▼ 刷新渠道
         ├─ editChannel ─▶ EditChannelModal ─┬─ ApiKeyManagementSection ─▶ [分组模型/Key倍率对话框]
         │                                   ├─ NewApiAccountPanel (accounts区,不进侧导航)
         │                                   └─ ProtocolModelAvailability @refreshed ▶ 刷新账号模型+替换editingChannel快照
         ├─ UpdateDialog（版本徽标/检查）
         └─ UserGuideDialog（帮助/首登自动）

ChannelOrchestration ─┬─「历史」▶ ChannelLogsDialog ─(trace chip)─▶ AutopilotTraceDetailDialog
                      └─ mdi-routes ▶ SchedulerDiagnoseDialog

AutopilotView ─ AutopilotTraceTable @select ─▶ AutopilotTraceDetailDialog
             └ 内嵌 AutopilotModePanel / AutopilotDiagnosePanel

SubscriptionsView ─ 内嵌 NewApiSubscriptionForm；billingDialog / syncDialog
```

## 15. 通用约定

- 状态管理：`dialogStore`（`stores/dialog.ts`）持有 `showAddChannelModal`/`showEditChannelModal`/`editingChannel`/`showAddKeyModal`/确认对话框状态 + `confirm()` Promise 化封装。
- 快捷键：对话框普遍 Esc 关闭、⌘/Ctrl+Enter 提交。
- 后端交互统一经 `services/api.ts` 与 `services/autopilot-api.ts`。
- 校验模式：本地 computed 校验 + Vuetify `formRef.validate()` 规则 + 内联 error alert。

## 16. 待补充项详解

### 16.1 CapabilityTestDialog 接线

**状态**：✅ **已修复**（2026-08-09，提交 `d876784d`）。

**修复方案**

- `App.vue` 引入 `CapabilityTestDialog` 与 `useCapabilityTestManager`，装配 manager 并挂载对话框，绑定 `model-value`、`channelName`、`capabilityJob`、`capabilityRpm` 等 props；`@capability-test` 从 router-view 透传给 `manager.testChannelCapability`。
- `ChannelOrchestration.vue` 渠道行操作菜单新增「能力测试」项（仅对 messages/chat/responses/gemini 可测协议显示），点击 `$emit('capability-test', element)`。
- 保留原有行为：打开能力测试会关闭 Add/Edit 弹窗（`useCapabilityTestManager.ts:515-520`）。

**遗留观察项**

- `ChannelOrchestration` 为获取 `isCapabilityChannelKind` 而实例化 `useCapabilityTestManager`，传入了空的 `showToast`/store stub；不影响功能，但 manager 副作用增强时需审视。
- 接线已通过 type-check 与单元测试，但未做真实后端能力测试端到端验证。

### 16.2 其他待补充

- 移动端适配细节：部分对话框在移动端可能需要全屏或底部弹出
- 国际化覆盖：部分硬编码文案需提取到 locale 文件
- 无障碍访问：对话框的 focus trap、aria-label 需完善
