# Channel 数据模型 v2 设计文档

> 目标：把 CCX 的渠道粒度从"协议入口 = 渠道"重构为"物理站点/账号 = 渠道"，让一个渠道下自然表达
> 「多个 key，每个 key 多个 (baseURL, 协议) endpoint，每个 endpoint 有具体模型清单」，
> 并让 new-api 账号、分组共享的协议+模型认知成为一等公民。

## 1. 背景与问题

CCX 权威配置是六个并列数组：`upstream`（messages）、`chatUpstream`、`responsesUpstream`、`geminiUpstream`、`imagesUpstream`、`vectorsUpstream`。每个元素是一个 `UpstreamConfig`，只承载**一种协议**（单一 `ServiceType`）、一组 `BaseURLs`、一组 `APIKeys`。

后果：同一物理站点/账号（如火山 `ark.cn-beijing.volces.com/api/coding` 同时兼容 Claude/OpenAI/Responses）被拆成多个 `UpstreamConfig`，导致：

- key、模型清单在多个协议副本间重复；
- 健康检查、熔断、拉黑、RPM 配额各自独立，"一个上游坏了多个渠道一起报"；
- 用户看到多张卡，实则同一账号。

详见 `logical-channel.md` §17。

## 2. 核心概念与边界

- **Channel（渠道）**：一个物理站点/账号。聚合同账号/同逻辑渠道的多协议物理渠道。
- **ChannelKey（凭证）**：渠道下的一个 key。凭证状态（auth 有效性、健康、拉黑、配额）按 key/账号隔离。
- **KeyEndpointBinding**：key 到某个 `(baseURL, 协议)` endpoint 的绑定。**不复制模型清单**，只引用共享能力。
- **EndpointCapability（能力认知）**：`(SiteIdentity, GroupIdentity, IdentityBaseURL, Protocol)` 唯一确定的协议+模型认知。**跨账号共享**。
- **ProtocolFacade**：CCX 入口协议（messages/chat/responses/gemini/images/vectors），决定请求从哪个路由进入。
- **NewApiAccount**：new-api 渠道的账号侧信息（订阅、accessToken、余额、归属 key）。

两条边界必须分离：

| 边界 | 归属键 | 跨账号共享 |
|---|---|---|
| 能力认知（协议+模型） | `(SiteIdentity, GroupIdentity, IdentityBaseURL, Protocol)` | 共享 |
| 凭证状态（auth/健康/拉黑/配额/余额/token） | `KeyHash` / `AccountUID` | 隔离 |

**已确认设计假设**：`GroupIdentity = 归一化(GroupName)`（trim + 小写），同站点同名分组视为同能力，无需能力指纹二次校验。

## 3. 数据模型

Phase 1 以**只读视图**落地（`backend-go/internal/config/channel_model.go`），不改 JSON schema：

```text
ChannelView（物理站点/账号聚合）
├── ChannelUID / AccountUID / ProviderID / Name / SiteIdentity / BaseURLs / Status
├── Protocols []ProtocolFacadeView   // 每个 kind 一个物理渠道 facade
├── Keys      []ChannelKeyView
│     ├── KeyUID / CredentialUID / KeyMask / KeyHash / AccountUID
│     ├── QuotaGroup / GroupIdentity / Enabled / Weight / RateLimit*
│     └── Endpoints []KeyEndpointBindingView
│            └── CapabilityUID → EndpointCapability（引用，不复制）
├── Account   *NewApiAccountView       // 仅 new-api 渠道
└── MemberRoutes []ChannelRouteRefView  // 组成该视图的物理路由 (kind,index,channelUID)

EndpointCapability（跨账号共享，按 CapabilityUID 索引）
├── CapabilityUID = "cap_" + sha256(site|group|identityBaseURL|protocol)[:16]
├── SiteIdentity / GroupIdentity / GroupName / BaseURL / IdentityBaseURL
├── Protocol / ServiceType
└── Models []string  // Phase1 取 SupportedModels，后续接入探测画像
```

关键函数（同文件）：`BuildChannelViews(cfg)`、`GenerateCapabilityUID`、`NormalizeGroupIdentity`、`ChannelKeyHash`、`SiteIdentityForBaseURL`。

## 4. 身份与共享规则

- **归组**：`LogicalChannelUID` > `AccountUID` > `SiteIdentity` > 物理 `ChannelUID`。
- **凭证合并**：同一 `KeyHash` 在组内跨协议合并为一个 `ChannelKeyView`，其 `Endpoints` 汇总各协议 `(baseURL, protocol)`。
- **能力共享**：`(SiteIdentity, GroupIdentity, IdentityBaseURL, Protocol)` 相同即同一 `CapabilityUID`。不同账号在同站点买同名分组，其 key 绑定同一能力，协议探测只需一次。
- **能力隔离**：`GroupIdentity` 不同（如 vip vs default）或站点/协议不同，则能力独立。

## 5. 运行时状态归属

| 状态 | 归属键 | 说明 |
|---|---|---|
| 能力认知 / 协议探测 | `CapabilityUID` | 跨账号共享，探测去重 |
| L1 auth 健康 | `KeyHash` | 401/403 是 key 级事实，隔离 |
| Circuit breaker | `(KeyHash, CapabilityUID, model)` | key 熔断不影响同分组他账号 |
| Blacklist / disabled | `KeyUID` / `KeyHash` | new-api 账号注销级联禁用其 key |
| RPM / concurrency | `(AccountUID, KeyHash)` | 配额账号私有 |
| Model 质量画像 | `CapabilityUID + ModelID` | 认知共享，provider 质量证据可细分 key |

## 6. 三步迁移路线

- **Phase 1（已落地）**：只读 `ChannelView` + `EndpointCapability` 注册表；能力/凭证边界成型；不改 schema，可回退。
- **Phase 2（进行中）**：
  - **2a（已落地）**：`Config` 新增 `Channels []ChannelView` + `ChannelCapabilities []EndpointCapability` + `ChannelSchemaVersion`，作为**非权威镜像**，落盘前由 `RebuildChannels` 从六数组合成（与 `RebuildLogicalChannels` 同一 save 路径）。六数组仍是运行时权威；镜像不含明文 key。前端可直接消费该新粒度。
  - **2b（进行中）**：只读 API `GET /api/channels`、`GET /api/channels/:uid` 暴露实时合成的渠道视图与共享能力（`internal/handlers/channels`，基于 `ConfigManager.GetChannelViews`）；前端读逐步切换。写路径仍走既有 upstream/logical-channels 接口。
- **Phase 3**：移除六个 `Upstream` 数组，仅保留 `Channels` + `ProtocolFacade`；scheduler/handlers/autopilot 全量改用 `Channel`。
  - **3a（已落地）**：无损权威形态 `ChannelV3`（每协议成员携带完整 `UpstreamConfig`）+ 双向投影 `BuildAuthoritativeChannels` / `ApplyAuthoritativeChannels`（`channel_authoritative.go`）。round-trip 逐字段无损、按 Index 恢复数组顺序，测试通过。这是"Channels 权威"的安全核心机制。
  - **3b（已落地）**：把 `ChannelV3` 作为持久化权威（save 写 / load 时从它重建六数组），schema 版本门控，旧配置零影响。
  - **3c 波 1（已落地，app 已验证）**：运行时权威反转——加载后以 `ChannelsV3` 为唯一权威重建运行时六数组（无开关门控）；严格模式 `CCX_CHANNEL_AUTHORITATIVE_STRICT` 对账失败拒绝启动，非严格以 V3 覆盖；加载期迁移落盘当次跳过翻转（避免旧 V3 快照撤销迁移）。
  - **3c 波 2（评估结论：免改）**：消费者无需逐文件切换——`GetConfig()` 返回的六数组已是 V3 运行时投影，读取语义不变；如需显式化可后续加 `UpstreamsForKind` 访问器。
  - **3c 波 3（已落地：读保留、写停）**：save 不再落盘六数组（置 nil + `omitempty`，文件只含 `channelsV3` 权威形态）；读侧兼容不变——旧双写/仅六数组文件照常读入并在下次 save 自动转纯 V3。纯 V3 文件加载时在入口提前投影 V3→六数组（迁移/中途落盘直接作用于投影，避免空数组重建出空 V3 与托管凭证丢失），跳过后置翻转；旧双写格式保持"迁移后翻转/对账 + savedDuringLoad 豁免"语义。**回滚约束**：纯 V3 文件不能被波 1 之前的旧二进制读取（旧二进制只认六数组），回滚不得低于波 1 版本，且须先从 `.config/backups/` 恢复双写格式备份；`ChannelV3SchemaVersion` 保持 1 不 bump（旧文件双写同代，平滑升级）。

## 7. 当前实现状态

- ✅ `channel_model.go`：读模型类型 + 身份工具。
- ✅ `channel_view.go`：`BuildChannelViews` 合成 + 共享能力注册表。
- ✅ `channel_view_test.go`：多协议收敛、跨账号同分组共享、分组隔离、禁用 key 四项测试。
- ✅ `endpoint_capability.go`：`EndpointCapabilityRegistry`（按 CapabilityUID 查询）+ `CapabilityProbeLedger`（每周期跨账号探测去重）+ `KeyEndpointCapabilityUIDs`。
- ✅ Phase 2a：`Config.Channels` / `ChannelCapabilities` / `ChannelSchemaVersion` 非权威镜像，落盘前 `RebuildChannels` 合成（`config_loader.go` saveConfigLocked）；round-trip 与"不含明文 key/不改六数组"测试通过。
- ✅ Phase 2b（后端读）：`GET /api/channels`、`GET /api/channels/:uid` 实时返回渠道视图 + 共享能力（`ConfigManager.GetChannelViews`）。
- ✅ Phase 3a（权威投影核心）：`channel_authoritative.go` 无损 `ChannelV3` + 双向投影 + round-trip/顺序恢复测试。
- ✅ Phase 3b（权威落盘）：save 落盘 `channelsV3`（脱敏后从数组合成）+ `channelAuthoritativeVersion`；load 时 `reconcileAuthoritativeChannels` 对账告警（非破坏，仍信任六数组）。
- ✅ #8（拉黑跨协议）：`BlacklistKeyWithRecoverAt` 级联拉黑同 `AccountUID`/`LogicalChannelUID` 下持有相同明文 key 的其它协议渠道；不同账号同名 key 不误伤。测试覆盖级联与隔离。
- ⏳ #8 熔断层：model-circuit 跨协议共享（metrics 层改键，需 app 验证）。
- ✅ `CapabilityProbeLedger` 已接入 autopilot 协议探测与 healthcheck L2（healthcheck 侧：同能力 key 每周期只真实探测一次，成功结论复用、失败各自再探；L1 因 auth 绑定不去重，稀疏 L2 因 per-key 预算口径不去重）。
- ✅ Phase 3c 波 1（运行时权威反转）：加载后始终以 `ChannelsV3` 重建运行时六数组；托管 Key 经 `syncManagedAccountCredentialsFromChannels` + `hydrateManagedAccountCredentials` 闭环；对账忽略易变字段与 Key；加载期迁移落盘当次跳过翻转（回归测试覆盖）。app 回归通过（`make run` 实测 3399：297 渠道重建、管理 API/调度/保活正常）。
- ❌ Phase 3c 波 2（消费者切换）：评估后免改（六数组已是 V3 投影）。
- ✅ Phase 3c 波 3（六数组停落盘）：save 只写 `channelsV3`（六数组置 nil + `omitempty`）；读侧兼容旧双写/仅六数组文件并在下次 save 自动转纯 V3；纯 V3 加载入口提前投影 V3→六数组（修复中途落盘以空数组重建空 V3、清托管凭证的回归）；专项测试覆盖落盘格式/重载不清文件/旧格式升级。回滚约束见 §6。
