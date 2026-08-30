# New-API 集成设计文档

## 1. 集成入口与接口定义

### 1.1 路由注册（后端装配）
`backend-go/main.go:1511-1531` 在 autopilot 就绪后装配三组路由，全部挂在 `apiGroup`（`/api`）下：
- `autopilot.RegisterSubscriptionRoutes` — 订阅中心 CRUD + link/unlink + refresh
- `autopilot.RegisterNewApiSubscriptionRoutes` — new-api verify/provision
- `autopilot.RegisterSubscriptionAccountRoutes` — 多账号 + 主账号凭证更新

共享依赖 `NewApiSubscriptionSyncService`（`main.go:1504`）。启动时先调用 `newApiSyncService.SyncAllNewAPIAsync(context.Background())` 对所有 `provider=new_api` 订阅做一次全量同步（`newapi_subscription_sync_service.go`），随后 `newApiSyncService.Start(ctx)` 启动周期为 30 分钟的后台同步循环。路由注册位置与同步服务生命周期以 `main.go` 中符号为准，避免行号再次漂移。

### 1.2 核心端点（`internal/autopilot/handlers_newapi.go`）
`RegisterNewApiSubscriptionRoutes`（行 110-114）：
- `POST /api/subscriptions/newapi/verify` → `handleNewApiVerify`（行 375）— 校验令牌 + 预览账户/分组/模型，**不落库**
- `POST /api/subscriptions/newapi/provision` → `handleNewApiProvision`（行 427）— 建 profile + 建 key + 建/并渠道 + 触发 Discovery

依赖结构 `NewApiRouteDeps`（行 40）：`Store *SubscriptionStore`、`CfgManager *config.ConfigManager`、`Runner *AutoDiscoveryRunner`、`SyncService *NewApiSubscriptionSyncService`。provision 采用**站点级锁 + 订阅级锁**串行化远端 key 查重/创建与同订阅画像写入，避免并发建同名远端 key。

### 1.3 请求/响应结构（`handlers_newapi.go`）
- `NewApiVerifyRequest`（行 127）：`baseUrl, accessToken, userId, authTokenMode, displayName, subscriptionUid`
- `NewApiVerifyResponse`（行 137）：`username, userId, quota, usedQuota, groups(map[string]float64), groupFetchError, availableModels, suggestedOriginType/Tier, accessTokenMasked`
- `NewApiProvisionRequest`（行 152）：`subscriptionUid, displayName, baseUrl, accessToken, userId, authTokenMode, channelKind(必填), channelName, provisionKeyName, provisionGroup, provisionAllEligibleGroups, provisionModels, maxGroupMultiplier(*float64), notes`
- `NewApiProvisionResponse`（行 173）：`subscription, channelUid, channelIndex, channelName, mergedChannel, provisionedKey(仅此次返回明文), provisionedTokenId, reused, provisionedKeys[], discoveryStarted`

### 1.4 上游适配器（`internal/autopilot/newapi_adapter.go`）
`NewApiAdapter`（行 98），零值可用（默认 15s client），封装 new-api family 面板接口。`doRequest`（行 121）统一注入认证并解析 `{success,data,message}` 信封（`newApiEnvelope` 行 29）。认证头 `buildAuthHeader`（行 110）：`bearer`（默认 `Authorization: Bearer <token>`）/ `raw`/`raw_auth`（裸 token）；同时下发 `New-API-User` 与 `User-id`（fork 兼容，行 142-146）。

调用的 new-api 接口子集与方法：
- `Verify`（行 198）→ `GET /api/user/self`（`NewApiUserSelf`：id/username/quota/used_quota）
- `VerifyWithFallback`（行 209）— userID 为空时回退 `userId="1"` 重试（`isNewApiUserHeaderError` 行 237）。注意：标准 new-api 的 access token 认证强制要求 `New-Api-User` 头（缺失时报 `New-Api-User header not provided`），空 userID 仅在 fork 不校验该头或账号恰为 ID=1 管理员时可用，因此 **userId 实际必填**；前端各入口表单（2026-08-30）均已改为必填，后端保留空值兜底仅为兼容直接 API 调用与宽松 fork
- `FetchGroups`（行 259）→ `GET /api/user/self/groups`（返回 `{group: ratio}`）
- `FetchModels`（行 272）→ `GET /api/user/models`
- `ListTokens`（行 281）/ `FindTokenByName`（行 309）→ `GET /api/token/?p=&size=`（分页遍历，maxPages=1000 防重复建 key）
- `ProvisionKey`（行 363）→ `POST /api/token/`（`NewApiCreateTokenRequest` 行 75，默认 `unlimited_quota=true, expired_time=-1`）。查重复用，明文经 `normalizeNewApiPlaintextKey`（行 352）补 `sk-` 前缀（否则上游 401）。分组不匹配时返回 `NewApiProvisionKeyConflictError`。
- `DeleteToken`（行 441）→ `DELETE /api/token/{id}`（失败补偿回收）
- `FetchBalance`（行 249）— 复用 Verify，返回 `quota` 原值 + `currency="quota"`

## 2. 多账号建 key、主账号凭证管理（前后端位置）

### 2.1 后端（`internal/autopilot/handlers_subscription_accounts.go`）
`RegisterSubscriptionAccountRoutes`（行 810）：
- `PATCH /api/subscriptions/:uid/newapi-credentials` → `handleUpdateNewApiCredentials`（行 703）— 主账号凭证更新，指针字段区分“不改”与显式提交（`NewApiCredentialsUpdateRequest`，含 `proxyUrl`/`proxyPreferDirect` 订阅级代理设置），落库前 `VerifyWithFallback` 按生效代理校验，支持 `expectedVersion` 乐观锁，落库后触发 `SyncNow`
- `POST /api/subscriptions/:uid/accounts` → `handleAddSubscriptionAccount`（行 89）— **先 verify 拉账号/分组/模型，再按倍率阈值为全部合格分组自动建 key**（`namePrefix = accountUID + “-”` 避免同站同名复用冲突），失败回滚远端 key
- `GET /api/subscriptions/:uid/accounts` → `handleListSubscriptionAccounts`（行 332，脱敏；仅返回额外账号，主账号经 `GET /subscriptions/:uid` 单独拉取、前端组装进列表首行）
- `DELETE /api/subscriptions/:uid/accounts/primary` → `handleDeletePrimaryAccount`（行 373）— 账号平权：删除主账号＝清空订阅级凭证（AccessToken/UserID/Username/AuthTokenMode）与 ProvisionedKeys、剔除其自动接入 key（best-effort 回收远端），订阅本体（baseUrl/分组/计费/渠道关联）保留；无主账号时 409
- `DELETE /api/subscriptions/:uid/accounts/:accountUid` → `handleDeleteSubscriptionAccount`（行 434）— 先从渠道剔除 key，再 best-effort 回收远端 key，再移除账号
- `POST /api/subscriptions/:uid/accounts/:accountUid/refresh` → `handleRefreshSubscriptionAccount`（行 493）
- `PATCH /api/subscriptions/:uid/accounts/:accountUid/credentials` → `handleUpdateSubscriptionAccountCredentials`（行 576，62d2d46d）— 子账号独立更新凭证（accessToken 留空=保持不变），支持 userId/authTokenMode。**账号平权后（2026-08-28）编辑面板已不再调用**（换凭证统一为删除+重新添加），端点保留供外部调用

**账号平权模型（2026-08-28）**：主账号与子账号在 UI 平权——主账号行同样可删除；换主凭证的完整路径=删除主账号 → 「添加账号」输入新令牌。`handleAddSubscriptionAccount` 在订阅无主凭证时（行 235）自动把新账号提升为主账号（回填凭证/用户名/余额并从 Accounts 移除该条目，避免既主又子）；`SyncNow` 无主凭证时（`newapi_subscription_sync_service.go:291` 起）跳过站点级同步、仅同步子账号，不视为失败。

多账号建 key 复用主流程 `provisionNewApiGroupKeys`（`handlers_newapi.go:227`），核心复用逻辑集中在此。

### 2.2 前端
- 主/多账号面板：`frontend/src/components/edit-channel/NewApiAccountPanel.vue`
  - 订阅 UID 解析 `effectiveSubscriptionUid`（行 382，787db651）：`props.subscriptionUid || localSubscriptionUid`，仍为空且非 generic、`autoManagedKind==='new_api'` 时按约定兜底 `newapi-${channelUid}`——渠道 key 配置 `sourceSubscriptionUid` 丢失（编辑换 key 切断关联、`BuildChannelView` 不再暴露 `subscriptionUid`）时面板仍可直连订阅
  - `bindNewApi`（行 457）：generic 渠道绑定 new-api，`subscriptionUid: newapi-${channelUid}`；**先 verify，再按上游 `groups + availableModels` 调 `provisionAllEligibleGroups=true`**
  - `refreshPrimaryAccount`（行 523）→ `api.refreshSubscription`；`deletePrimaryAccount`（行 540）→ `api.deleteSubscriptionPrimaryAccount`（账号平权：主账号可删，换凭证=删除+重新添加，后端自动提升新主账号）
  - `handleAddAccount`（行 570）：主账号订阅未就绪时置 `addError`（`subscriptionUnavailable`，787db651）而非静默返回；就绪时同样**先 verify，再显式传 `provisionAllEligibleGroups` / `maxGroupMultiplier` / `availableModels`**；`refreshAccount` / `deleteAccount`（行 616/628）为子账号行操作
  - UI 结构（40d8b990）：主账号默认作为账号列表首行（「主账号」徽章，展开=纯详情，行操作刷新/删除），与子账号行同构；详见 `web-ui-dialogs.md` §5
  - `authTokenModeOptions`：Bearer/Raw（行 420）
- 首次接入表单：`frontend/src/components/NewApiSubscriptionForm.vue`（两步 verify→provision）
- 订阅中心入口：`frontend/src/views/SubscriptionsView.vue`（`selectedProvider==='new-api'` 行 23）+ `SubscriptionProviderGrid.vue`（行 116-122）
- 快速添加入口：`frontend/src/components/QuickAddChannelForm.vue`（`NEW_API_PROVIDER_VALUE='__new_api__'` 行 264）+ `subscriptions/NewApiQuickAddDialog.vue`

## 3. 数据模型：账号/key/凭证/渠道映射

### 3.1 订阅画像 `SubscriptionProfile`（`internal/autopilot/subscription_profile.go:15`）
new-api 专用字段（行 62-94）：`Provider="new_api"`、`BaseURL`、`AccessToken`（敏感，明文序列化进 `profile_json`，API 响应脱敏）、`UserID`、`AuthTokenMode`、`ProxyURL`、`ProxyPreferDirect`（订阅级代理：绑定/同步经代理访问，直连优先开启时先直连、失败回退代理）、`ProvisionKeyName`、`ProvisionGroup`、`ProvisionGroupRatio`、`MaxGroupMultiplier`、`ProvisionModels`、`ProvisionedTokenID`、`ProvisionedKeys []NewApiProvisionedKey`、`AvailableModels`、`GroupMultipliers map[string]float64`、`Accounts []NewApiAccount`（账号级 `ProxyURL/ProxyPreferDirect` 为空时继承订阅级）。计费条款为四字段币种/金额模型 `PaymentAmount/PaymentUnit/CreditAmount/CreditUnit`（行 37-40；旧 `RechargeMultiplier` 单字段已移除，`a96098da`，旧 JSON 键加载时自动忽略）。

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
`KeyUID = StableKeyUID(subscriptionUID, tokenID)`（`newapi_subscription_sync_service.go:775`，sha256 前 8 字节，前缀 `kuid_`）。tokenID 站点级唯一，主账号与多账号 key 共存不撞号。

渠道编辑保存时 `mergeAPIKeyConfig`（`config.go:785`，787db651）对 `MultiplierSource=new_api` 的既有 key 回填 `SourceSubscriptionUID`/`SourceRemoteTokenID`（客户端表单不携带这两个托管身份字段）——此前不回填，编辑换 key 会切断渠道与订阅的关联，且 `BuildChannelView` 的 `subscriptionUid` 正是从 `APIKeyConfigs[].SourceSubscriptionUID` 暴露的，关联一断前端账号面板即拿不到订阅 UID。非托管 key 的显式清空（`handlers_key_multiplier.go` 脱离同步管理场景）不受回填影响。

### 3.5 同步服务（`internal/autopilot/newapi_subscription_sync_service.go`）

`NewApiSubscriptionSyncService`（行 59）负责 new-api 订阅的周期性余额、分组倍率与可用模型同步，使用 per-uid 锁（`lockForUID`）。

- 后台循环：`Start`（行 159）启动 30 分钟周期 ticker（`newApiSyncDefaultInterval` 行 93），tick 到达时调用 `SweepAll` 并发刷新所有 new-api 订阅。`Stop` 优雅停止循环。初始启动时还会通过 `SyncAllNewAPIAsync` 先做一次性全量同步。
- `SyncNow`（行 271）：verify → FetchGroups（校验非负有限，`finiteNonNegative`）→ FetchModels → `Patch` 回写余额/分组/模型/KeyUID/ratio → `reconcileChannels`（行 620）把 desired key 元数据合并进关联渠道的 `APIKeyConfigs`（ownership 冲突→`relink_required`）→ 模型哈希变化触发 Discovery；主账号凭证为空时（行 291，账号平权）跳过站点级同步仅同步子账号
- `reconcileNewApiConfigs`（行 645）：按 `SourceRemoteTokenID`/`KeyUID` 匹配，跨订阅 ownership 冲突返回 conflict。匹配维度只有这两个字段——渠道 key 被误删后常规 reconcile 无法找回，由 `healMissingProvisionedKeys`（行 867）自愈：SyncNow/syncOneAccount 在 reconcile 后检查关联渠道是否缺失 desired key，缺失时按 tokenID 分页拉远端 token 列表（`ListTokens`），掩码 key 经揭示端点（`GetTokenKey`）换回明文并规范 `sk-` 前缀，再走 `injectProvisionedKeys` 重建 config；远端 token 也已删除的项跳过，绝不注入空 key（测试 fake 未实现 `newApiTokenHealer` 接口时自愈自动跳过）
- `buildDesiredForKeys`：计算每 key 的 syncStatus（`fresh`/`over_limit`/`remote_group_missing`）+ TTL（`newApiSyncTTL=35m` 行 26）
- `injectProvisionedKeys`（行 799）/`ReconcileProvisioned`（行 972）/`ReconcileAccountProvisioned`（行 991）：provision/加账号/自愈后把明文 key 注入渠道；注入的明文同时并入渠道 `APIKeys`（调度与 keypool 候选只遍历 `APIKeys`，仅写 configs 的 key 不参与调用）
- `RemoveAccountKeysFromChannels`（行 516）：删账号/删主账号时剔除渠道 key
- 状态常量（行 17-24）：`fresh/over_limit/sync_error/relink_required/stale/remote_group_missing`

## 4. 与 autopilot/scheduler 的集成点

### 4.1 渠道纳入调度（provision 落地）
`handleNewApiProvision`（`handlers_newapi.go:427`）第 5 步：
- 同站点合并 `findNewApiMergeTarget`（行 317，规范化 baseURL 去尾 `/`、忽略大小写、跳过带 `providerId` 的渠道、优先 active），合并时保留已有纯 key、去重追加新 key、补 `AutoManagedKind="new_api"`
- 否则新建，`kindToDefaultServiceType`（`handlers_auto_managed.go`）推导 serviceType，`GenerateChannelUID` 生成稳定 UID
- 第 6 步 `Store.LinkChannel`，第 7 步 `deps.Runner.TriggerDiscovery`（`auto_discovery.go:297`）

### 4.2 分组安全闸门 `resolveNewApiProvisionGroups`（`newapi_group_guard.go:24`）
`DefaultNewApiMaxGroupMultiplier=1.0`（行 12）。只为 `ratio <= maxMultiplier` 的合格分组建 key，一个 key 固定绑一个上游分组；`defaultNewApiProvisionKeyNameForGroup`（行 96）给 key 名加分组后缀。

**当前统一语义**：
- 所有入口都先 verify，获取上游 `groups` 与 `availableModels`。
- 空分组请求不再猜 `default`；统一按“全部合格分组”路径处理。
- 只有调用方**显式**传 `provisionGroup` 时才进入单分组模式。
- `groupFetchError`、无合格分组、倍率阈值非法时均阻断，不创建任何部分 key。

### 4.3 调度期成本闸门 `IsAPIKeyGroupMultiplierAllowed`（`internal/config/key_group_multiplier.go:79`）
`GetNextAPIKey`（`config.go:1447/1459`）与 keypool 选 key 时调用 `EvaluateAPIKeyMultiplierEligibility`（行 27）：`new_api` 源需 `SourceSubscriptionUID`+`SourceRemoteTokenID` 有效、status=`fresh` 且未过期，否则 `stale/over_limit/relink_required` 一律不参与调度。这是把 new-api 分组倍率纳入调度的核心闸门。

### 4.4 SmartRouter 候选过滤/成本评分
`main.go:788` `SetCandidateFilterProvider` 注入 `SmartRouter.CandidateFilterForWithActual`。`buildChannelEntry` 在有汇率图 + 到账规则时，用带 `SourceSubscriptionUID` 且 `GroupMultiplier` 的 key 配置调用 `ResolveEffectiveCostUSD`（`key_endpoint_profile.go:360`）算真实 effective USD 成本，替代标价。`EffectiveMultiplier = GroupMultiplier × TimeMultiplier × PaymentAmount×PaymentUSDPrice / (CreditAmount×CreditUSDPrice)`（行 416）。到账规则有两条入口：**订阅级** billing-terms（`PaymentAmount/CreditAmount` 四字段）与**渠道级**计费四字段（`UpstreamConfig.ChannelPayment*/ChannelCredit*`，`49f28b3e`），后者使无订阅 billing-terms 的渠道也能算 effective cost。

### 4.5 Discovery/画像链路
`TriggerDiscovery`→`runDiscovery`（`auto_discovery.go:360`）→`discoverEndpoints`（行 527）/`probeEndpoint`（行 899）拉 `/v1/models`（统一走 `utils.FetchUpstreamModels`：Anthropic 风格端点首发即带 Claude Code 探针头；其余裸发、命中客户端指纹风控特征时带探针头重试一次并回写 `LearnedClientFingerprint`，见 `cross-module-integration.md` §3.6），`parseModelsDeclaredEndpointTypes`（行 1119）解析 new-api 的 `supported_endpoint_types`（`protocolForEndpointType` 行 1154：openai→chat / anthropic→messages / gemini / openai-response→responses；仅作探测排序提示不做过滤），`discoverEndpointProtocols`（`protocol_discovery.go`）逐模型多协议实测，`writeProfileForEndpoint`（`auto_discovery.go:1237`）写 `KeyEndpointProfile`。`buildEndpointInventory`（`endpoint_inventory.go`）建 endpoint 清单供画像与限速。

### 4.6 余额刷新路径
`handleRefreshSubscription`（`handlers_subscription.go:376`）对 `provider=="new_api"` 走 `syncService.SyncNow`，其他 provider 走 `SubscriptionRefreshWorker`（`subscription_refresh_worker.go`）。注意 `IsAutoRefreshSupported`（`subscription_balance_fetcher.go`）白名单只含 openai/anthropic/google，**不含 new_api**（见第 6 节）。

## 5. 前端编辑弹窗字段/校验/API 调用链路

### 5.1 弹窗接入位置
`frontend/src/components/EditChannelModal.vue:118-136` 在 `isNewApiChannel || isGenericAutoManagedChannel`（行 443-451，判断逻辑：`autoManagedKind==='new_api'` 或 `originType==='relay' && autoManaged && !providerId`；generic 为 `autoManaged && !providerId && autoManagedKind!=='new_api'`）时渲染 `NewApiAccountPanel`，传 `subscription-uid / channel-name / base-url / channel-uid / channel-kind / is-generic / auto-managed-kind / channel-proxy-url / channel-proxy-prefer-direct`（后两项让面板的绑定/校验/同步复用渠道「代理通道」，面板自身不配置代理）。

### 5.2 字段与校验
- 首次接入 `NewApiSubscriptionForm.vue`：
  - 表单字段：`baseUrl, accessToken(password), userId(必填，标准 new-api 强制 New-Api-User 头), authTokenMode, proxyUrl, proxyPreferDirect, displayName`；provision 步 `subscriptionUid, channelKind, channelName, maxGroupMultiplier(number,min=0), notes`（代理设置在 verify 步录入，provision 预填继承并写入所建渠道）
  - 校验：`canVerify`（baseUrl+accessToken+userId 非空，行 287；userId 自 2026-08-30 起必填）、`canProvision`（subscriptionUid+channelKind + `maxGroupMultiplierValid` + `eligibleGroupItems.length>0`，行 288）
  - 合格分组过滤：`eligibleNewApiGroups`（`utils/newApiGroups.ts:12`）与 `isValidNewApiGroupMultiplier`（行 8，非负有限），前端与后端 `resolveNewApiProvisionGroups` 语义一致
  - provision 固定发 `provisionAllEligibleGroups=true`（行 318）
- 已有渠道绑定 / 追加账号（`NewApiAccountPanel.vue`）：
  - userId 同样必填（2026-08-30）：`canBindNewApi` 与添加账号按钮均要求 userId 非空
  - **同样先 verify，再使用 verify 返回的 `groups` + `availableModels` 计算全部合格分组**
  - 绑定主账号与追加账号都显式传 `provisionAllEligibleGroups=true`、`maxGroupMultiplier`（当前默认 1.0）与 `availableModels`
  - `groupFetchError`、无合格组、verify 失败时均阻断提交，不允许 fallback 到 `default`
- key 倍率编辑 `edit-channel/ApiKeyManagementSection.vue`：`openMultiplierEditor` → `patchKeyMultiplier`；`new_api` 源 key 的 `groupMultiplier` 后端拒绝手改（`handlers_key_multiplier.go:158`，409 Conflict「new_api key 的 groupMultiplier 由远端同步，不能手动修改」），仅允许改 `maxGroupMultiplier`（前端也据此限制）；状态色 `multiplierStatusColor`/`multiplierStatusLabel`（`utils/subscriptionBilling.ts`）。定位符为 `KeyUID`，非 new-api key（托管账号手工 key 等）无 `KeyUID` 时后端兜底匹配 `CredentialUID`、前端行数据以 `keyUid ?? credentialUid` 回填，倍率编辑入口对全部可编辑 key 可见（`bb736eef` 及后续兜底修复）。

### 5.3 API 服务层（`frontend/src/services/api.ts`）
`verifyNewApiSubscription`(1473) / `provisionNewApiSubscription`(1481) / `updateNewApiCredentials`(1490) / `getSubscriptionAccounts`(1497) / `addSubscriptionAccount`(1502) / `deleteSubscriptionAccount`(1510) / `deleteSubscriptionPrimaryAccount`(1517，账号平权) / `refreshSubscriptionAccount`(1524) / `updateSubscriptionAccountCredentials`(1531，62d2d46d，面板已不再调用) / `refreshSubscription`(1436) / `patchKeyMultiplier`(1460) / `linkSubscriptionChannel`(1422) / `unlinkSubscriptionChannel`(1429)。类型定义在 `api-types.ts`：`NewApiVerifyRequest/Response`、`NewApiProvisionRequest/Response`、`NewApiProvisionedKey(Info)`、`NewApiAccountItem`、`NewApiCredentialsUpdateRequest`、`NewApiKeyStatus`、`NewApiSyncResult`、`APIKeyConfig`。

### 5.4 调用链路（统一组计划）
1. `verify`：输入 `baseUrl/accessToken/userId(必填)/authTokenMode`，后端拉 `GET /api/user/self`、`GET /api/user/self/groups`、`GET /api/user/models`，返回 `groups + availableModels`。
2. `plan`：前端按统一倍率阈值（默认 1.0）用 `eligibleNewApiGroups` 计算全部合格分组；若 `groupFetchError`、无合格组或阈值非法，则直接阻断。
3. `provision / add-account`：
   - 新增订阅：`NewApiSubscriptionForm.handleProvision` → `api.provisionNewApiSubscription`
   - generic 渠道绑定：`NewApiAccountPanel.bindNewApi` → 先 verify，再 `api.provisionNewApiSubscription`
   - 追加账号：`NewApiAccountPanel.handleAddAccount` / Desktop 对应入口 → 先 verify，再 `api.addSubscriptionAccount`
4. 所有入口都显式发送：`provisionAllEligibleGroups=true`、`maxGroupMultiplier`、`availableModels`；空请求不再由后端猜 `default`。
5. 后端统一执行：`verify→FetchGroups→resolveGroups→provisionNewApiGroupKeys→建 profile/账号→建/并渠道→LinkChannel→TriggerDiscovery→SyncService.ReconcileProvisioned`，并返回实际接入的 key/group 列表。

### 5.5 i18n
locale 键在 `frontend/src/locales/{zh-CN,en,id}.json`，前缀 `subscription.newApi.*`（zh-CN 行 1201-1275，含 787db651 新增的 `primaryBadge`/`primaryAccountUnavailable`/`subscriptionNotFound`/`subscriptionUnavailable` 与账号平权新增的 `deletePrimaryAccount`/`primaryAccountRemoved`）与 `subscription.keyMultiplier.*`（行 1494-1569），`autopilot.quickAdd.provider.newApi`（行 1466）。

## 6. 可能缺失的边界处理与文档

1. **new_api 无周期性自动余额刷新**~~（旧声明，已修正）~~：`NewApiSubscriptionSyncService` 已提供 30 分钟周期的后台同步循环。启动时 `SyncAllNewAPIAsync` 做一次全量同步，随后 `Start` 启动 30 分钟 ticker 并调用 `SweepAll` 并发刷新每个 `provider=new_api` 订阅。`SubscriptionRefreshWorker.refreshAll` 仍按 `IsAutoRefreshSupported` 白名单跳过 new_api，但 new-api 的定期同步由专用 sync service 承担。

2. ~~**AccessToken 明文落库**~~ ✅ **已修复**（2026-08-09，提交 `d876784d`）：`SubscriptionProfile.AccessToken` 与 `Accounts[].AccessToken` 落库前经 AES-256-GCM 加密（`encryptProfile`/`decryptProfile`），读库透明解密并向后兼容旧明文（无加密标记前缀时原样返回）。密钥优先读环境变量 `CCX_SECRET_KEY`，未设置则退回机器派生（hostname/GOOS/GOARCH/salt）。**注意**：机器派生密钥下若迁移数据目录到另一台机器且未设 `CCX_SECRET_KEY`，旧加密 token 将无法解密——生产部署应显式配置 `CCX_SECRET_KEY`。`BillingAPIKey` 暂未加密（可复用同一机制扩展）。

3. ~~**专项 spec 文档缺失**~~ ✅ **已修正**：`docs/specs/new-api-integration.md` 已存在，`docs/specs/README.md:12` 索引链接有效，不再悬空。

4. ~~`link/unlink UI 缺失`~~ ✅ **已修复**：订阅中心已提供绑定/解绑入口，调用 `POST /api/subscriptions/:uid/link|unlink` 与前端 `api.linkSubscriptionChannel/unlinkSubscriptionChannel`。

5. **`quota` 货币换算依赖手工汇率**：new-api `quota` 非 USD，effective 成本需 `ExchangeRateQuotes` + 订阅 `PaymentAmount/CreditAmount`（`smart_router.go:1356-1378`）齐备才生效；缺任一项则回退标价 USD 成本，new-api quota 无法参与真实成本排序（静默降级，无用户提示）。

6. **`NewApiAccountItem.usedQuota` 前端类型有、后端不填**：`api-types.ts:1446` 定义了 `usedQuota`，但后端 `handlers_subscription_accounts.go` 构造 `NewApiAccountItem` 时（行 184/244/391）从不设置该字段——per-account 已用额度不可见。

7. **合并渠道的 kind 冲突**：`findNewApiMergeTarget` 只按 baseURL+kind 匹配，若同站点用户先建了纯 key 渠道且 serviceType 与 new-api 推导不一致，合并后 `serviceType` 沿用旧渠道（不校验），可能与 new-api 分组语义错配（无显式告警）。

8. ~~**new-api 占位模型 503 误判端点不可用**~~ ✅ **已修复**（2026-08-19，提交 `cd6f93d7`）：key 验证（`verify_endpoint.go` `verifyJSONPostEndpointWithPolicy`）在 `acceptValidationError` 分支除 400/422 外新增识别 503 + `{"error":{"code":"model_not_found"}}`（或 message 含 `no available channel for model`），判定鉴权通过——new-api/one-api 对无渠道占位模型返回 503，此前被误判「端点不可用」导致有效 key 无法添加。

9. ~~**编辑换 key 切断渠道与订阅的关联**~~ ✅ **已修复**（2026-08-27，提交 `787db651`；2026-08-28 补自愈）：前端编辑保存时表单提交的 `apiKeyConfigs` 不含 `sourceSubscriptionUid`（且无 `keyUid` 的身份骨架条目会被 `channelPayload.ts` 的 filter 剔除），后端合并后旧 config 的 `SourceSubscriptionUID`/`SourceRemoteTokenID` 全部丢失 → `BuildChannelView` 不再暴露 `subscriptionUid` → 编辑渠道对话框主账号面板空白、添加账号静默无响应；且 `reconcileNewApiConfigs` 只按 tokenID/KeyUID 匹配，常规同步无法自愈。修复：`mergeAPIKeyConfig` 对 `MultiplierSource=new_api` 的既有 key 回填托管身份字段（止血）；前端按 `newapi-${channelUid}` 兜底推导订阅 UID（恢复）；订阅 404/未就绪给出提示而非静默。后续补齐：渠道侧误删的自动接入 key 由 `healMissingProvisionedKeys` 在每次同步时按 tokenID 从远端取回明文自愈重建；`injectProvisionedKeys` 注入的明文同时并入渠道 `APIKeys`（此前仅写 configs、新 key 实际不可调度）。

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
  ├── AccessToken (主账号；账号平权：DELETE accounts/primary 清空，
  │                无主凭证时添加账号自动提升为新主账号)
  ├── GroupMultipliers {group: ratio}
  ├── ProvisionedKeys[]
  │     ├── Name, Group, TokenID, KeyUID
  │     └── ...
  └── Accounts[]  (子账号；提升为主账号的条目会从列表移除)
        ├── AccountUID
        ├── AccessToken
        ├── ProvisionedKeys[]
        │     ├── Name (accountUID + "-group")
        │     └── TokenID, KeyUID
        └── ...

UpstreamConfig (channelUid, autoManagedKind=new_api)
  ├── APIKeys[]  (调度池；注入的明文 key 一并并入)
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
    ├─ 主账号凭证为空（账号平权，已删除）→ 仅 syncAccounts，不视为失败
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
[healMissingProvisionedKeys]
    ├─ 渠道缺失 desired key → 按 tokenID 拉远端列表
    │   └─ 掩码经揭示端点换明文 → injectProvisionedKeys 重建
    └─ 远端 token 也已删除 → 跳过，不注入空 key
    │
    ▼
[状态计算]
    ├─ fresh: 正常
    ├─ over_limit: ratio > maxGroupMultiplier
    ├─ stale: 超过 TTL 未同步
    ├─ relink_required: ownership 冲突
    └─ remote_group_missing: 远端分组已删除
```

## 8. 待补充项详解

### 8.1 与 LogicalChannel 归组逻辑的交互

> **更新（2026-08-09，提交 `d876784d`）**：本节"主缺口①（逻辑卡视图不刷新）"已修复——`saveConfigLocked` 现在统一调用 `RebuildLogicalChannels`，provision/add-account 落盘后逻辑卡即时刷新，无需进程重启。下文缺口①保留作为背景记录，缺口②（AccountUID 空）仍待处理。

**当前实现**

New-api provision 建渠道时（`handlers_newapi.go:591-605`），构造的 `UpstreamConfig` **只设置** `AutoManaged=true`、`AutoManagedKind="new_api"`、`ChannelUID`、`BaseURL/BaseURLs`，**未设置 `AccountUID`，也未设置 `LogicalChannelUID`/`LogicalName`**。合并路径（`updateChannelForKind` → `UpstreamUpdate`）同样只更新 `APIKeys/APIKeyConfigs/AutoManaged*`，不碰账号/逻辑身份字段。

归组的真相来源是 `RebuildLogicalChannels`：除加载时的 `ensureLogicalBackfill` 外，现已在 `saveConfigLocked` 统一收口（见更新说明），运行期物理渠道变更后逻辑卡即时同步。`AccountUID` 回填仍是独立缺口。

`AccountUID` 与 `LogicalChannelUID` 的关系（`logical_channel.go:66-111、467-499`）：
- `logicalChannelGroupKeyFrom` 用 `(accountUID, providerID, siteIdentity)` 三元组归组。
- `shouldGroupLogical`：① 同 `accountUID` 直接合并（最高优先，`convergeLogicalByAccount` 还会强制把同账号收敛到单一 canonical 卡）；② 同 providerID + 同站点合并；③ 无 provider 无 account 的手工渠道 + 同站点合并。
- New-api 渠道 `ProviderID` 为空、`AccountUID` 为空，只能靠**规则③（同站点手工渠道合并）**归组。而 provision 的 `findNewApiMergeTarget` 恰好用相同的“同站点 + 无 providerID”语义在物理层面提前合并，二者语义一致但**各算各的**。

**缺口分析**

1. **新建渠道后逻辑卡视图不刷新（主缺口）。** provision/add-account 只改物理数组，`LogicalChannels` 聚合列表与物理渠道的 `LogicalChannelUID` 字段直到进程重启才由 `ensureLogicalBackfill` 回填。运行期 `GET /api/logical-channels` 会返回陈旧列表——新接入的 new-api 渠道要么不出现在逻辑卡里，要么 `logicalChannelUid` 为空。

2. **AccountUID 空导致多账号 new-api 无法归到同一逻辑卡。** 同一 new-api 订阅下 add-account 把多个账号的 key 并入同一 `LinkedChannelUIDs` 渠道，但**渠道 `AccountUID` 始终为空**。若同一订阅将不同 kind（messages/chat/responses）建到不同站点渠道，规则③（同站点）可能把它们拆成多张逻辑卡，而没有 `AccountUID` 作为跨站点/跨协议的强合并键——不像自动托管 provider 那样能靠 `AccountUID` 收敛。

3. **合并目标选择口径与归组口径存在潜在分叉。** `findNewApiMergeTarget` 跳过任何带 `ProviderID` 的渠道，而 `shouldGroupLogical` 规则②允许同 provider 同站点合并。若同站点既有 new-api 渠道又有带 providerId 的模板渠道，provision 会新建独立渠道，但逻辑归组阶段规则可能有不同判断（虽然 new-api 渠道无 providerId 走规则③，不会与规则②渠道合并，当前行为一致，但两处口径独立维护，易随一方演进而漂移）。

**建议方案**

- **在物理渠道增删改后触发逻辑卡重建。** 最小改动：在 new-api provision/merge/add-account 完成、`LinkChannel` 之后，显式调用一次 `RebuildLogicalChannels`（需在 `ConfigManager` 暴露一个加锁的公开方法，如 `RebuildLogicalChannelsAndSave()`）。或在 `AddUpstream`/`UpdateXxxUpstream`/`AddChatUpstream` 等 `saveConfigLocked` 前统一插入 `RebuildLogicalChannels(&cm.config)`。
- **给 new-api 渠道分配稳定 `AccountUID`。** provision 时按 `subscriptionUID`（或 `subscriptionUID + baseURL 站点`）派生一个稳定 `AccountUID` 写入 `UpstreamConfig.AccountUID`，让同订阅多协议/多站点渠道靠 `shouldGroupLogical` 规则①和 `convergeLogicalByAccount` 强制收敛到单张逻辑卡，与自动托管 provider 语义对齐。注意 `syncManagedAccountsFromChannels` 要求 `AutoManaged && AccountUID != "" && ProviderID != ""` 才建 ManagedAccount，new-api 渠道 `ProviderID` 为空不会误入托管账号池，是安全的。
- **统一合并口径。** 将 `findNewApiMergeTarget` 的“同站点 + 无 providerId”判定抽取为与 `logicalChannelGroupKeyFrom`/`shouldGroupLogical` 共享的辅助函数，避免两处独立维护站点身份归一化逻辑（当前 `normalizeNewApiChannelURL` 仅做 `TrimRight("/")`，而归组用 `utils.BaseURLSiteIdentities`，归一化强度不同，是漂移隐患）。

### 8.2 多账号并发 provision 锁粒度

**当前实现**

`newAPIProvisionMu` 是**进程级全局互斥锁**（`sync.Mutex`，非 per-subscription）。`handleNewApiProvision` 和 `handleAddSubscriptionAccount` 都对它 `Lock/defer Unlock`，且在**整个 handler 生命周期**持锁——包括 `VerifyWithFallback`、`FetchGroups`、`FetchModels`、`provisionNewApiGroupKeys`（对远端 new-api 站点建 key）、`Store.Create/AddAccount`、`AddUpstream`、`ReconcileProvisioned`，直至 handler 返回。ctx 超时为 30s。

注释说明设计意图：串行化避免同一时刻重复创建同名远端 Key，配置管理器仍负责与其他来源的渠道名冲突检测。

对比 sync service 用的是 per-UID 锁（`lockForUID`），设计上更细粒度。

**缺口分析**

1. **全局锁把所有账号 provision 串行化，含慢速网络 I/O。** 持锁跨越多个对远端 new-api 站点的 HTTP 往返（verify/groups/models/逐分组建 key）。两个**不同订阅、不同站点**的 provision 也会互相阻塞，最坏情况下第二个请求要等第一个的完整 30s 网络周期。多账号（add-account）与主 provision 共用同一把锁，批量接入多账号时吞吐受限。

2. **锁粒度与 sync service 不一致。** 同一进程里 sync 用 per-UID 锁、provision 用全局锁，两套并发模型并存。provision 持全局锁期间，sync service 对**同一订阅**的 `SyncNow`（走 per-UID 锁）可与 provision 交错执行——provision 尚未 `Store.Create` 完成落库时 sync 读到的中间态、或 provision 已建远端 key 但未 reconcile 到渠道时 sync 的 reconcile，两者对同一渠道 `APIKeyConfigs` 的读改写不受同一把锁保护（provision 用 `newAPIProvisionMu`，sync 用 `lockForUID`，互不感知），存在竞态窗口。

3. **锁未保护配置写入的读-改-写序列。** provision 内先 `findNewApiMergeTarget`（读 config）→ 后 `updateChannelForKind`（改 config）。`ConfigManager` 自身的 `cm.mu` 只保护单次 Add/Update 调用，跨调用的“查合并目标 + 写”序列靠 `newAPIProvisionMu` 全局串行化来保证——一旦未来放宽这把锁的粒度，`mergeIndex` 可能失效（代码有 `mergeIndex >= len(channels)` 的防御，但那只是越界检查，不能防止 index 指向了被重排后的另一个渠道）。

**建议方案**

- **改为 per-subscription（per-UID）锁，与 sync service 统一。** provision 的 key 冲突边界本质是“同一 new-api 站点 + 同一 key 名称”。可将锁键设为 `baseURL 站点身份`（而非 subscriptionUID，因为跨订阅但同站点才会撞远端同名 key），用与 sync 同款的 `map[string]*sync.Mutex` + 保护该 map 的 `sync.Mutex`。这样不同站点的 provision 可并发。若嫌站点键复杂，退而求其次用 subscriptionUID 作键（add-account 用其所属 subscriptionUID），可解决“不同订阅互相阻塞”，但保留同站点跨订阅撞名的小概率风险（此风险由远端 `FindTokenByName` 查重 + `provisionNewApiGroupKeys` 的 `newApiProvisionConflictError` 兜底）。
- **让 provision 与 sync 共享同一把 per-UID 锁。** 把 `newAPIProvisionMu` 换成 `SyncService.lockForUID` 暴露的同一锁实例（或让 provision 也走 sync service 的锁），消除“同一订阅 provision 与 sync 并发”的竞态。
- **缩小持锁范围。** 只在“查合并目标 + 建渠道/并渠道 + LinkChannel”这段配置读-改-写序列持锁；远端 HTTP（verify/groups/models）可移出锁外（这些是幂等只读或由远端查重兜底的建 key）。注意 `provisionNewApiGroupKeys` 建 key 部分仍需在锁内以防同名并发，需权衡。

### 8.3 汇率图缺失时的降级策略

**当前实现**

SmartRouter 成本计算的三段降级（`smart_router.go:1326-1380`）：
1. 基础标价为 `listCost`，默认成本 `-1`（表示无成本证据）。
2. 若配置有 `ExchangeRateQuotes`，构建 `graph`（构建失败时通过 `logExchangeRateBuildErrorOnce` 按 `ExchangeRateSnapshot.Version` 去重记录日志）；默认 `entry.EstimatedCost = listCost * timeMultiplier * groupMultiplier`，其中 `groupMultiplier` 取 `APIKeyConfigs` 中的 `GroupMultiplier`，缺失按 `1.0`。
3. 若 `graph != nil && r.subscriptionStore != nil` 且订阅有 `PaymentAmount/CreditAmount`，调 `ResolveEffectiveCostUSD` 算 effective USD，仅当 `resolvedCost.Available` 才覆盖；否则通过 `logEffectiveCostMissingOnce` 按 `subscriptionUID + Reason` 去重记录不可用原因。

**汇率图缺失/构建失败时的降级：**
- 配置默认**永远有 quotes**：`defaultExchangeRateQuotes`（USD↔CNY、LDC↔CNY）在 `Validate` 里对未配置的情况兜底填充，所以 `len(ExchangeRateQuotes) > 0` 几乎总成立。
- `NewExchangeRateGraph` 构建失败时 error 不再静默丢弃，而是由 `logExchangeRateBuildErrorOnce` 按 snapshot version 只记录一次，并回退到 `listCost * timeMultiplier * groupMultiplier`。
- `ResolveExchangeTerms` 对 graph nil、单位不在图中、版本不匹配、单价非有限正数等都返回 `OK=false` + 具体 `Reason` 字符串，`Available=false`，调用方回退标价。
- `EstimatedCost` 最终进 `NormalizeSavingsScore`；`c < 0`（无成本证据）得中性 0.5 分，不惩罚。

**请求期路径（`upstream_failover.go:291` `buildRequestCostContext`）**（`49f28b3e` 后已支持渠道级 effective cost）：
1. 列表成本按渠道计价币种折 USD（`ResolveUpstreamCapability` + `CalculateTokenCostUSDWithPricing`），只固化 `ListCostUSD` + `ExchangeSnapshotVersion`。
2. 构建全局汇率图（quotes 来自 `CostOptimization.ExchangeRateQuotes`）。
3. 渠道级「充值币种/金额 + 渠道币种/到账金额」四字段齐备且金额>0 时，调 `autopilot.ResolveEffectiveCostUSD` 计算 `EffectiveCostMultiplier = (Payment×充值币价)/(Credit×渠道币价)`，置 `EffectiveCostAvailable=true`、`EffectiveCostReason="channel payment/credit conversion"`。
4. 四字段缺配但渠道配了 `CostMultiplier` 时走简化路径直取该倍率（reason `"channel cost multiplier"`）。
5. 订阅级 billing-terms 仍未在请求期解析（reason 以 `"subscription payment/credit snapshot unavailable"` 起步）——订阅级补齐仍是路由期（§4.4）专属。

**缺口分析**

1. **new-api 无 billing-terms 且渠道未配四字段时 effective USD 不可用。** effective USD 计算要求订阅同时有 `PaymentAmount` 和 `CreditAmount`，或渠道配齐计费四字段。new-api provision 建 profile 时设 `Currency:"quota"`、`Balance`，**从不设置 `PaymentAmount/CreditAmount`**（只能通过 `PATCH /subscriptions/:uid/billing-terms` 手工填，四字段 `paymentAmount/paymentUnit/creditAmount/creditUnit`，充值倍率单字段模型已移除 `a96098da`）。未补齐的订阅/渠道走 `listCost * timeMultiplier * groupMultiplier`；分组倍率已能影响 SavingsScore，但真实到账 USD 成本仍无法计算。

2. **`quote` 单位与 key 计价单位可能对不上。** new-api key 的 `GroupMultiplier` 是相对倍率，effective 计算用 `PaymentUnit/CreditUnit`（如 CNY/LDC）查图。若用户 billing-terms 填的单位不在默认图（USD/CNY/LDC）中，`ResolveExchangeTerms` 返回 `"exchange rate unit not in graph"`，静默回退——用户没有任何反馈知道要去补一条 quote。

**建议方案**

- **引导用户补 billing-terms。** 在订阅详情/编辑处明确提示：只有填写 `PaymentAmount/CreditAmount/PaymentUnit/CreditUnit` 并保证单位在汇率图内，effective USD 成本路由才会生效。
- **暴露成本降级的可观测指标。** 请求期 `buildRequestCostContext` 已有 `EffectiveCostReason` 字段并持久化到 SQLite，`cost_report_handler.go` 也统计 `EffectiveUnavailableCount`。建议把 SmartRouter 路由期解析出的 `Reason` 也接入同一诊断链路，让 `/api/reports/cost` 能展示“多少 new-api 请求因汇率/billing-terms 缺失而无法算 effective cost”。
- **provision 时预填默认 billing-terms 或返回 warning。** 若 new-api quota 与某法币有确定换算（如平台按 CNY 充值得 quota），provision 可预填 `PaymentAmount/CreditAmount/PaymentUnit/CreditUnit`，或在 provision 响应里返回一个 warning 提示用户补 billing-terms + 对应汇率 quote，才能启用 effective cost 路由。

### 8.4 补充发现

- **`ExchangeRateQuotesConfigured` 语义**：区分“用户显式清空 quotes”与“旧配置没配 quotes”的机制。若用户显式传空数组清空 quotes，则 `len(ExchangeRateQuotes) == 0`，不建图，所有 key 回退标价——这是**唯一能真正让图缺失的路径**，且同样无日志。
- **图版本一致性检查**依赖 `ResolveUSDPrice` 返回的 version 全等。构图时 version 取自 `ExchangeRateSnapshot.Version`（缺省 1），若 snapshot 与 quotes 不同步（如 quotes 更新但 snapshot 未重算），version 仍能自洽（同一次 `NewExchangeRateGraph` 内所有单位同 version），不会误判——此处设计正确，无缺口。
