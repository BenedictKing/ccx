# New-API 集成设计文档

## 1. 集成入口与接口定义

### 1.1 路由注册（后端装配）
`backend-go/main.go:1454-1476` 在 autopilot 就绪后装配三组路由，全部挂在 `apiGroup`（`/api`）下：
- `autopilot.RegisterSubscriptionRoutes` — 订阅中心 CRUD + link/unlink + refresh
- `autopilot.RegisterNewApiSubscriptionRoutes` — new-api verify/provision
- `autopilot.RegisterSubscriptionAccountRoutes` — 多账号 + 主账号凭证更新

共享依赖 `NewApiSubscriptionSyncService`（`main.go:1454`），并在启动时 `newApiSyncService.SyncAllNewAPIAsync(context.Background())` 对所有 `provider=new_api` 订阅异步做一次同步。

### 1.2 核心端点（`internal/autopilot/handlers_newapi.go`）
`RegisterNewApiSubscriptionRoutes`（行 53-61）：
- `POST /api/subscriptions/newapi/verify` → `handleNewApiVerify`（行 310）— 校验令牌 + 预览账户/分组/模型，**不落库**
- `POST /api/subscriptions/newapi/provision` → `handleNewApiProvision`（行 362）— 建 profile + 建 key + 建/并渠道 + 触发 Discovery

依赖结构 `NewApiRouteDeps`（行 38-43）：`Store *SubscriptionStore`、`CfgManager *config.ConfigManager`、`Runner *AutoDiscoveryRunner`、`SyncService *NewApiSubscriptionSyncService`。provision 用 `newAPIProvisionMu sync.Mutex`（行 47）串行化避免并发建同名远端 key。

### 1.3 请求/响应结构（`handlers_newapi.go`）
- `NewApiVerifyRequest`（行 66）：`baseUrl, accessToken, userId, authTokenMode, displayName, subscriptionUid`
- `NewApiVerifyResponse`（行 76）：`username, userId, quota, usedQuota, groups(map[string]float64), groupFetchError, availableModels, suggestedOriginType/Tier, accessTokenMasked`
- `NewApiProvisionRequest`（行 91）：`subscriptionUid, displayName, baseUrl, accessToken, userId, authTokenMode, channelKind(必填), channelName, provisionKeyName, provisionGroup, provisionAllEligibleGroups, provisionModels, maxGroupMultiplier(*float64), notes`
- `NewApiProvisionResponse`（行 112）：`subscription, channelUid, channelIndex, channelName, mergedChannel, provisionedKey(仅此次返回明文), provisionedTokenId, reused, provisionedKeys[], discoveryStarted`

### 1.4 上游适配器（`internal/autopilot/newapi_adapter.go`）
`NewApiAdapter`（行 98），零值可用（默认 15s client），封装 new-api family 面板接口。`doRequest`（行 121）统一注入认证并解析 `{success,data,message}` 信封（`newApiEnvelope` 行 29）。认证头 `buildAuthHeader`（行 110）：`bearer`（默认 `Authorization: Bearer <token>`）/ `raw`/`raw_auth`（裸 token）；同时下发 `New-API-User` 与 `User-id`（fork 兼容，行 142-146）。

调用的 new-api 接口子集与方法：
- `Verify`（行 198）→ `GET /api/user/self`（`NewApiUserSelf`：id/username/quota/used_quota）
- `VerifyWithFallback`（行 209）— userID 为空时回退 `userId="1"` 重试（`isNewApiUserHeaderError` 行 237）
- `FetchGroups`（行 259）→ `GET /api/user/self/groups`（返回 `{group: ratio}`）
- `FetchModels`（行 272）→ `GET /api/user/models`
- `ListTokens`（行 281）/ `FindTokenByName`（行 309）→ `GET /api/token/?p=&size=`（分页遍历，maxPages=1000 防重复建 key）
- `ProvisionKey`（行 363）→ `POST /api/token/`（`NewApiCreateTokenRequest` 行 75，默认 `unlimited_quota=true, expired_time=-1`）。查重复用，明文经 `normalizeNewApiPlaintextKey`（行 352）补 `sk-` 前缀（否则上游 401）。分组不匹配时返回 `NewApiProvisionKeyConflictError`。
- `DeleteToken`（行 441）→ `DELETE /api/token/{id}`（失败补偿回收）
- `FetchBalance`（行 249）— 复用 Verify，返回 `quota` 原值 + `currency="quota"`

## 2. 多账号建 key、主账号凭证管理（前后端位置）

### 2.1 后端（`internal/autopilot/handlers_subscription_accounts.go`）
`RegisterSubscriptionAccountRoutes`（行 489）：
- `PATCH /api/subscriptions/:uid/newapi-credentials` → `handleUpdateNewApiCredentials`（行 397）— 主账号凭证更新，指针字段区分“不改”与显式提交（`NewApiCredentialsUpdateRequest` 行 27），落库前 `VerifyWithFallback` 校验，支持 `expectedVersion` 乐观锁，落库后触发 `SyncNow`
- `POST /api/subscriptions/:uid/accounts` → `handleAddSubscriptionAccount`（行 55）— 加账号并为其合格分组自动建 key（`namePrefix = accountUID + "-"` 行 121 避免同站同名复用冲突），失败回滚远端 key
- `GET /api/subscriptions/:uid/accounts` → `handleListSubscriptionAccounts`（行 228，脱敏）
- `DELETE /api/subscriptions/:uid/accounts/:accountUid` → `handleDeleteSubscriptionAccount`（行 264）— 先从渠道剔除 key，再 best-effort 回收远端 key，再移除账号
- `POST /api/subscriptions/:uid/accounts/:accountUid/refresh` → `handleRefreshSubscriptionAccount`（行 323）

多账号建 key 复用主流程 `provisionNewApiGroupKeys`（`handlers_newapi.go:166`），核心复用逻辑集中在此。

### 2.2 前端
- 主/多账号面板：`frontend/src/components/edit-channel/NewApiAccountPanel.vue`
  - `bindNewApi`（行 350）：generic 渠道绑定 new-api，`subscriptionUid: newapi-${channelUid}`（行 356）
  - `savePrimaryCredentials`（行 393）→ `api.updateNewApiCredentials`（带 `expectedVersion`）
  - `refreshPrimaryAccount`（行 415）→ `api.refreshSubscription`
  - `handleAddAccount` / `refreshAccount` / `deleteAccount`（行 444/465/477）
  - `authTokenModeOptions`：Bearer/Raw（行 309）
- 首次接入表单：`frontend/src/components/NewApiSubscriptionForm.vue`（两步 verify→provision）
- 订阅中心入口：`frontend/src/views/SubscriptionsView.vue`（`selectedProvider==='new-api'` 行 23）+ `SubscriptionProviderGrid.vue`（行 116-122）
- 快速添加入口：`frontend/src/components/QuickAddChannelForm.vue`（`NEW_API_PROVIDER_VALUE='__new_api__'` 行 264）+ `subscriptions/NewApiQuickAddDialog.vue`

## 3. 数据模型：账号/key/凭证/渠道映射

### 3.1 订阅画像 `SubscriptionProfile`（`internal/autopilot/subscription_profile.go:15`）
new-api 专用字段（行 62-94）：`Provider="new_api"`、`BaseURL`、`AccessToken`（敏感，明文序列化进 `profile_json`，API 响应脱敏）、`UserID`、`AuthTokenMode`、`ProvisionKeyName`、`ProvisionGroup`、`ProvisionGroupRatio`、`MaxGroupMultiplier`、`ProvisionModels`、`ProvisionedTokenID`、`ProvisionedKeys []NewApiProvisionedKey`、`AvailableModels`、`GroupMultipliers map[string]float64`、`Accounts []NewApiAccount`。

- `NewApiProvisionedKey`（行 104）：`Name, Group, GroupMultiplier, TokenID, KeyUID`（**无明文 key**）
- `NewApiAccount`（行 114）：`AccountUID, AccessToken, UserID, AuthTokenMode, DisplayName, Balance, Status, ProvisionedKeys[], LastSyncError, LastCheckedAt, CreatedAt`

持久化：`SubscriptionStore`（行 159），SQLite 表 `autopilot_subscriptions`（`subscription_uid` 主键 + `profile_json` JSON 列，行 214），内存 cache + `Patch` 乐观锁（行 317，`Version` 字段）。与 `ProfileStore` 共享同一 `*sql.DB`（`manager.go:187` `NewSubscriptionStoreWithDB`）。

### 3.2 渠道侧凭证元数据 `config.APIKeyConfig`（`internal/config/config.go:217`）
key 明文只存在渠道配置，new-api 相关字段：`QuotaGroup`、`GroupMultiplier *float64`、`MaxGroupMultiplier *float64`、`MultiplierSource("new_api")`、`MultiplierUpdatedAt/ExpiresAt`、`MultiplierSyncStatus`、`MultiplierSyncError`、`SourceSubscriptionUID`、`SourceRemoteTokenID`、`KeyUID`。

### 3.3 渠道 `config.UpstreamConfig`（`config.go:19`）
new-api 渠道标记：`AutoManaged=true`、`AutoManagedKind="new_api"`（行 128，取值 `"" | "generic" | "new_api"`）、`OriginType="relay"`、`OriginTier="second"`。

### 3.4 映射关系
```
SubscriptionProfile (subscriptionUid, provider=new_api, accessToken, accounts[])
  ├── GroupMultipliers {group: ratio}          ← FetchGroups 快照
  ├── ProvisionedKeys[] (主账号, tokenId→group)
  ├── Accounts[].ProvisionedKeys[] (每账号, name 加 accountUID 前缀)
  └── LinkedChannelUIDs[] ──────────────────┐
                                            ▼
UpstreamConfig (channelUid, autoManagedKind=new_api)
  └── APIKeyConfigs[] (key 明文 + KeyUID
        + SourceSubscriptionUID = subscriptionUid
        + SourceRemoteTokenId  = tokenId          ← ownership 绑定
        + QuotaGroup / GroupMultiplier / MaxGroupMultiplier
        + MultiplierSource=new_api / SyncStatus / ExpiresAt)
```
`KeyUID = StableKeyUID(subscriptionUID, tokenID)`（`newapi_subscription_sync_service.go:585`，sha256 前 8 字节，前缀 `kuid_`）。tokenID 站点级唯一，主账号与多账号 key 共存不撞号。

### 3.5 同步服务（`internal/autopilot/newapi_subscription_sync_service.go`）
`NewApiSubscriptionSyncService`（行 59），per-uid 锁（`lockForUID` 行 90）。
- `SyncNow`（行 101）：verify → FetchGroups（校验非负有限，`finiteNonNegative` 行 590）→ FetchModels → `Patch` 回写余额/分组/模型/KeyUID/ratio → `reconcileChannels`（行 430）把 desired key 元数据合并进关联渠道的 `APIKeyConfigs`（ownership 冲突→`relink_required`）→ 模型哈希变化触发 Discovery
- `reconcileNewApiConfigs`（行 455）：按 `SourceRemoteTokenID`/`KeyUID` 匹配，跨订阅 ownership 冲突返回 conflict
- `buildDesiredForKeys`（行 402）：计算每 key 的 syncStatus（`fresh`/`over_limit`/`remote_group_missing`）+ TTL（`newApiSyncTTL=15m` 行 24）
- `injectProvisionedKeys`（行 609）/`ReconcileProvisioned`（行 649）/`ReconcileAccountProvisioned`（行 668）：provision/加账号后把明文 key 注入渠道
- `RemoveAccountKeysFromChannels`（行 326）：删账号时剔除渠道 key
- 状态常量（行 17-24）：`fresh/over_limit/sync_error/relink_required/stale/remote_group_missing`

## 4. 与 autopilot/scheduler 的集成点

### 4.1 渠道纳入调度（provision 落地）
`handleNewApiProvision`（`handlers_newapi.go:362`）第 5 步：
- 同站点合并 `findNewApiMergeTarget`（行 252，规范化 baseURL 去尾 `/`、忽略大小写、跳过带 `providerId` 的渠道、优先 active），合并时保留已有纯 key、去重追加新 key、补 `AutoManagedKind="new_api"`
- 否则新建，`kindToDefaultServiceType`（`handlers_auto_managed.go:2769`）推导 serviceType，`GenerateChannelUID` 生成稳定 UID
- 第 6 步 `Store.LinkChannel`，第 7 步 `deps.Runner.TriggerDiscovery`（`auto_discovery.go:279`）

### 4.2 分组安全闸门 `resolveNewApiProvisionGroups`（`newapi_group_guard.go:24`）
`DefaultNewApiMaxGroupMultiplier=1.0`（行 12）。只为 `ratio <= maxMultiplier` 的合格分组建 key，一个 key 固定绑一个上游分组；`defaultNewApiProvisionKeyNameForGroup`（行 96）给 key 名加分组后缀。

### 4.3 调度期成本闸门 `IsAPIKeyGroupMultiplierAllowed`（`internal/config/key_group_multiplier.go:79`）
`GetNextAPIKey`（`config.go:1447/1459`）与 keypool 选 key 时调用 `EvaluateAPIKeyMultiplierEligibility`（行 27）：`new_api` 源需 `SourceSubscriptionUID`+`SourceRemoteTokenID` 有效、status=`fresh` 且未过期，否则 `stale/over_limit/relink_required` 一律不参与调度。这是把 new-api 分组倍率纳入调度的核心闸门。

### 4.4 SmartRouter 候选过滤/成本评分
`main.go:743` `SetCandidateFilterProvider` 注入 `SmartRouter.CandidateFilterForWithActual`（`smart_router.go:375`）。`buildChannelEntry`（行 1162）在有汇率图 + 订阅到账规则时，用带 `SourceSubscriptionUID` 且 `GroupMultiplier` 的 key 配置调用 `ResolveEffectiveCostUSD`（`key_endpoint_profile.go:361`）算真实 effective USD 成本，替代标价。`EffectiveMultiplier = GroupMultiplier × TimeMultiplier × PaymentAmount×PaymentUSDPrice / (CreditAmount×CreditUSDPrice)`（行 404）。

### 4.5 Discovery/画像链路
`TriggerDiscovery`→`runDiscovery`（`auto_discovery.go:342`）→`discoverEndpoints`（行 499）/`probeEndpoint`（行 835）拉 `/v1/models`，`parseModelsDeclaredEndpointTypes`（行 1061）解析 new-api 的 `supported_endpoint_types`（`protocolForEndpointType` 行 1096：openai→chat / anthropic→messages / gemini / openai-response→responses；仅作探测排序提示不做过滤），`discoverEndpointProtocols`（`protocol_discovery.go:92`）逐模型多协议实测，`writeProfileForEndpoint`（`auto_discovery.go:1182`）写 `KeyEndpointProfile`。`buildEndpointInventory`（`endpoint_inventory.go:53`）建 endpoint 清单供画像与限速。

### 4.6 余额刷新路径
`handleRefreshSubscription`（`handlers_subscription.go:383`）对 `provider=="new_api"`（行 401）走 `syncService.SyncNow`，其他 provider 走 `SubscriptionRefreshWorker`（`subscription_refresh_worker.go`）。注意 `IsAutoRefreshSupported`（`subscription_balance_fetcher.go:244`）白名单只含 openai/anthropic/google，**不含 new_api**（见第 6 节）。

## 5. 前端编辑弹窗字段/校验/API 调用链路

### 5.1 弹窗接入位置
`frontend/src/components/EditChannelModal.vue:107-124` 在 `isNewApiChannel || isGenericAutoManagedChannel`（行 663-671，判断逻辑：`autoManagedKind==='new_api'` 或 `originType==='relay' && autoManaged && !providerId`；generic 为 `autoManaged && !providerId && autoManagedKind!=='new_api'`）时渲染 `NewApiAccountPanel`，传 `subscription-uid / channel-name / base-url / channel-uid / channel-kind / is-generic / auto-managed-kind`。

### 5.2 字段与校验
- 首次接入 `NewApiSubscriptionForm.vue`：
  - 表单字段：`baseUrl, accessToken(password), userId, authTokenMode, displayName`；provision 步 `subscriptionUid, channelKind, channelName, maxGroupMultiplier(number,min=0), notes`
  - 校验：`canVerify`（baseUrl+accessToken 非空，行 263）、`canProvision`（subscriptionUid+channelKind + `maxGroupMultiplierValid` + `eligibleGroupItems.length>0`，行 264）
  - 合格分组过滤：`eligibleNewApiGroups`（`utils/newApiGroups.ts:12`）与 `isValidNewApiGroupMultiplier`（行 8，非负有限），前端与后端 `resolveNewApiProvisionGroups` 语义一致
  - provision 固定发 `provisionAllEligibleGroups=true`（行 318）
- key 倍率编辑 `edit-channel/ApiKeyManagementSection.vue`：`openMultiplierEditor`（行 1585）→ `patchKeyMultiplier`（行 1604）；`new_api` 源 key 的 `groupMultiplier` 后端拒绝手改（`handlers_key_multiplier.go:114`），仅允许改 `maxGroupMultiplier`（行 1602 前端也据此限制）；状态色 `multiplierStatusColor`（行 1583）/`multiplierStatusLabel`（`utils/subscriptionBilling.ts:19`）

### 5.3 API 服务层（`frontend/src/services/api.ts`）
`verifyNewApiSubscription`(1422) / `provisionNewApiSubscription`(1430) / `updateNewApiCredentials`(1439) / `getSubscriptionAccounts`(1446) / `addSubscriptionAccount`(1451) / `deleteSubscriptionAccount`(1459) / `refreshSubscriptionAccount`(1466) / `refreshSubscription`(1385) / `patchKeyMultiplier`(1409) / `linkSubscriptionChannel`(1371) / `unlinkSubscriptionChannel`(1378)。类型定义在 `api-types.ts`：`NewApiVerifyRequest/Response`(1354/1363)、`NewApiProvisionRequest/Response`(1376/1404)、`NewApiProvisionedKey(Info)`(1393/1400)、`NewApiAccountItem`(1434)、`NewApiCredentialsUpdateRequest`(1427)、`NewApiKeyStatus`(1222)、`NewApiSyncResult`(1235)、`APIKeyConfig`(114)。

### 5.4 调用链路（provision 为例）
`NewApiSubscriptionForm.handleProvision` → `api.provisionNewApiSubscription` → `POST /api/subscriptions/newapi/provision` → `handleNewApiProvision`（verify→FetchGroups→resolveGroups→provisionNewApiGroupKeys→建 profile→建/并渠道→LinkChannel→TriggerDiscovery→SyncService.ReconcileProvisioned）→ 返回 `NewApiProvisionResponse` → `emit('created')` → `SubscriptionsView.handleNewApiCreated` 刷新列表。

### 5.5 i18n
locale 键在 `frontend/src/locales/{zh-CN,en,id}.json`，前缀 `subscription.newApi.*`（zh-CN 行 1168-1224）与 `subscription.keyMultiplier.*`（行 1422-1427），`autopilot.quickAdd.provider.newApi`（行 1394）。

## 6. 可能缺失的边界处理与文档

1. **new_api 无周期性自动余额刷新**：`SubscriptionRefreshWorker.refreshAll`（`subscription_refresh_worker.go:220`）按 `IsAutoRefreshSupported` 白名单跳过 non-whitelisted provider，`builtinAutoRefreshProviders`（`subscription_balance_fetcher.go:235`）与预置 `shared/subscription-preset/subscription-preset.json:13` 的 `autoRefreshProviders` 均**不含 new_api**。new_api 仅靠启动 `SyncAllNewAPIAsync`（一次性）+ 手动刷新。这与设计文档 §8.5.1「定时刷新复用 SubscriptionAutoRefresh」（`docs/design/channel-autopilot.md:3384`）不一致——目前没有 new_api 的定时后台同步 worker。

2. **AccessToken 明文落库**：`subscription_profile.go:66-69` 注释明确「允许序列化进 profile_json」，`persist`（行 448）直接 JSON 落 SQLite，无加密。设计文档 §8.5.1 反复强调「加密存储」（`channel-autopilot.md:3283/3310/3323/3390`）。这是与设计的实现级偏差（仅做了脱敏展示 `maskAccessToken`，未做静态加密）。

3. **专项 spec 文档缺失**：`docs/specs/README.md:12` 引用 `./new-api-integration.md`，但该文件在 `docs/specs/` 下**不存在**（仅有 README.md）。文档索引存在悬空链接。

4. **link/unlink UI 缺失**：后端有 `POST /api/subscriptions/:uid/link|unlink`（`handlers_subscription.go:133-134`）与前端 `api.linkSubscriptionChannel/unlinkSubscriptionChannel`，但前端组件/视图无任何调用点（grep 仅命中定义）。手动关联/解绑渠道无 UI 入口。

5. **`quota` 货币换算依赖手工汇率**：new-api `quota` 非 USD，effective 成本需 `ExchangeRateQuotes` + 订阅 `PaymentAmount/CreditAmount`（`smart_router.go:1249-1270`）齐备才生效；缺任一项则回退标价 USD 成本，new-api quota 无法参与真实成本排序（静默降级，无用户提示）。

6. **`NewApiAccountItem.usedQuota` 前端类型有、后端不填**：`api-types.ts:1446` 定义了 `usedQuota`，但后端 `handlers_subscription_accounts.go` 构造 `NewApiAccountItem` 时（行 184/244/391）从不设置该字段——per-account 已用额度不可见。

7. **合并渠道的 kind 冲突**：`findNewApiMergeTarget` 只按 baseURL+kind 匹配，若同站点用户先建了纯 key 渠道且 serviceType 与 new-api 推导不一致，合并后 `serviceType` 沿用旧渠道（不校验），可能与 new-api 分组语义错配（无显式告警）。

## 7. 布局示意图

### 7.1 整体数据流

```text
[用户输入 BaseURL + AccessToken]
           │
           ▼
   POST /api/subscriptions/newapi/verify
           │
           ▼
   ┌─────────────────┐
   │  NewApiAdapter  │ ──→ GET /api/user/self
   │   (Verify)      │ ──→ GET /api/user/self/groups
   └─────────────────┘ ──→ GET /api/user/models
           │
           ▼
   返回账号/分组/模型预览（不落库）
           │
           ▼
   POST /api/subscriptions/newapi/provision
           │
           ▼
   ┌─────────────────┐
   │ resolveNewApi   │  过滤 ratio <= maxGroupMultiplier 的合格分组
   │ ProvisionGroups │
   └─────────────────┘
           │
           ▼
   ┌─────────────────┐
   │ provisionNewApi │  为每个合格分组建 key（或复用同名 key）
   │    GroupKeys    │  namePrefix = accountUID + "-"
   └─────────────────┘
           │
           ▼
   ┌─────────────────┐     ┌─────────────────┐
   │ 建 Subscription │────→│ 建/并 Upstream  │
   │    Profile      │     │     Config      │
   └─────────────────┘     └─────────────────┘
           │                       │
           ▼                       ▼
   ┌─────────────────┐     ┌─────────────────┐
   │  LinkChannel    │────→│ TriggerDiscovery │
   └─────────────────┘     └─────────────────┘
           │                       │
           ▼                       ▼
   ┌─────────────────┐     ┌─────────────────┐
   │ Reconcile       │────→│  AutoDiscovery  │
   │ ProvisionedKeys │     │     Runner      │
   └─────────────────┘     └─────────────────┘
```

### 7.2 账号-Key-渠道映射

```text
SubscriptionProfile (subscriptionUid)
  ├── AccessToken (主账号)
  ├── GroupMultipliers {group: ratio}
  ├── ProvisionedKeys[]
  │     ├── Name, Group, TokenID, KeyUID
  │     └── ...
  └── Accounts[]
        ├── AccountUID
        ├── AccessToken
        ├── ProvisionedKeys[]
        │     ├── Name (accountUID + "-group")
        │     └── TokenID, KeyUID
        └── ...

UpstreamConfig (channelUid, autoManagedKind=new_api)
  └── APIKeyConfigs[]
        ├── Key (明文)
        ├── KeyUID = StableKeyUID(subscriptionUID, tokenID)
        ├── SourceSubscriptionUID = subscriptionUid
        ├── SourceRemoteTokenID = tokenID
        ├── QuotaGroup / GroupMultiplier / MaxGroupMultiplier
        └── MultiplierSource = new_api / SyncStatus / ExpiresAt
```

### 7.3 同步服务状态机

```text
[SyncNow]
    │
    ├─ Verify ──→ 余额/账号信息
    ├─ FetchGroups ──→ GroupMultipliers
    ├─ FetchModels ──→ AvailableModels
    │
    ▼
[reconcileChannels]
    │
    ├─ 按 SourceRemoteTokenID / KeyUID 匹配
    ├─ ownership 冲突 → relink_required
    └─ 合并 desired key 元数据进 APIKeyConfigs
    │
    ▼
[状态计算]
    ├─ fresh: 正常
    ├─ over_limit: ratio > maxGroupMultiplier
    ├─ stale: 超过 TTL 未同步
    ├─ relink_required: ownership 冲突
    └─ remote_group_missing: 远端分组已删除
```

## 8. 待补充

- 与 LogicalChannel 的归组逻辑如何交互
- 多账号并发 provision 的锁粒度
- 汇率图缺失时的降级策略
