# LogicalChannel 设计文档

> 同站多协议合一（LogicalChannel）把同一上游站点在不同协议（messages/chat/responses/gemini/images/vectors）下的物理渠道收敛为单一逻辑卡。运行时仍以六个 Upstream* 数组为权威存储；LogicalChannel 是稳定身份与跨协议管理视图。

## 1. 背景与目标

- 同一上游站点可能同时提供 Claude Messages、OpenAI Chat、Responses、Gemini 等多种协议入口。
- 传统模式下每个协议是一个独立渠道，导致列表冗长、配置重复、状态分散。
- LogicalChannel 把同账号/同站/同 provider 的物理渠道收敛为单一逻辑卡，统一展示、统一管理；调度时仍落到具体物理渠道。

## 2. 核心数据模型

### 2.1 `LogicalChannel`

文件：`backend-go/internal/config/logical_channel.go`（行 38-55）

| 字段 | JSON 名 | 含义 |
|---|---|---|
| `LogicalChannelUID` | `logicalChannelUid` | 稳定 ULID 风格 UID，`lc_` 前缀 |
| `AccountUID` | `accountUid` | 可选托管账号身份 |
| `ProviderID` | `providerId` | 可选来源 provider 模板 ID |
| `Name` | `name` | 用户可见名称 |
| `Description` | `description` | 描述 |
| `Website` | `website` | 站点主页 |
| `Kind` | `kind` | `llm` / `embeddings` / `images` |
| `BaseURLs` | `baseUrls` | 站点地址池（归一化后，去重保序） |
| `SiteIdentity` | `siteIdentity` | 主 URL 的归一化站点身份 |
| `Protocols` | `protocols` | 多协议物理路由引用 |
| `Tags` | `tags` | 用户标签 |
| `CreatedAt` / `UpdatedAt` | `createdAt` / `updatedAt` | 时间戳 |

### 2.2 `LogicalChannelProtocol`

文件：`backend-go/internal/config/logical_channel.go`（行 27-36）

| 字段 | 含义 |
|---|---|
| `Kind` | `messages` / `chat` / `responses` / `gemini` / `images` / `vectors` |
| `ChannelUID` | 物理渠道 `UpstreamConfig.ChannelUID` |
| `ServiceType` | `claude` / `openai` / `responses` / `gemini` |
| `Enabled` | 用户可见启停，与物理 `Status` 同步 |
| `Status` | 运行时状态：`active` / `suspended` / `disabled` |
| `Priority` | 与物理 route 同步 |
| `RoutePrefix` | 物理 route 的 `RoutePrefix`（仅展示） |

### 2.3 物理渠道上的反向引用

文件：`backend-go/internal/config/config.go`（行 140-146）

- `UpstreamConfig.LogicalChannelUID`：物理渠道所属逻辑渠道稳定身份。
- `UpstreamConfig.LogicalName`：物理渠道所属逻辑渠道的用户可见名称。

二者为非权威指针，用于加载旧配置回填、CRUD 同步、前端统一视图。

### 2.4 配置根结构

文件：`backend-go/internal/config/config.go`（行 1255-1261）

- `Config.LogicalChannels []LogicalChannel`
- `Config.LogicalChannelSchemaVersion int`（当前版本 `1`）

## 3. 归组算法 `RebuildLogicalChannels`

文件：`backend-go/internal/config/logical_channel.go`（行 132-234）

### 3.1 输入与前提

- 锁内调用，纯函数转换，不写盘。
- 收集全部六个物理数组：`Upstream`、`ChatUpstream`、`ResponsesUpstream`、`GeminiUpstream`、`ImagesUpstream`、`VectorsUpstream`。
- 已有 `LogicalChannelUID` 的物理渠道优先归到对应 logical（用户通过 API 创建的不会被拆散）。

### 3.2 归组键 `logicalChannelGroupKey`

文件：`backend-go/internal/config/logical_channel.go`（行 57-79）

键字段：

| 字段 | 来源 |
|---|---|
| `accountUID` | `UpstreamConfig.AccountUID`（trim） |
| `providerID` | `UpstreamConfig.ProviderID`（trim） |
| `siteIdent` | 主 URL 经 `utils.BaseURLSiteIdentities` 取第一个身份 |
| `hasAccount` / `hasProvider` | 标识位 |

### 3.3 BaseURL 规范化

由 `utils.BaseURLSiteIdentities` 实现。

文件：`backend-go/internal/utils/url_utils.go`（行 124-161）

规则：

- scheme、host 转小写。
- 忽略尾部 `/`。
- 默认版本前缀（`/v1`、Gemini 的 `/v1beta`）视为等价。
- 保留路径（含 tenant path）、端口、查询参数、`#` 语义。
- 空 scheme/host 返回空身份。

因此：
- `https://api.example.com/v1` 与 `https://api.example.com` 同站。
- `https://api.openai.com/v1` 与 `https://api.openai.com/tenantA/v1` 不同站。

### 3.4 可合并判定 `shouldGroupLogical`

文件：`backend-go/internal/config/logical_channel.go`（行 92-111）

优先级顺序：

1. 同一 `AccountUID` 必合并（即使 site 不同也被视为同一托管账号身份）。
2. 站点身份必须一致。
3. 同一 `ProviderID` + 同一站点合并。
4. 手工渠道（无 provider / 无 account）+ 同一站点合并。
5. 不同 account / 不同 provider / 不同 site 不合并。

### 3.5 同账号强制收敛（历史缺陷修复）

文件：`backend-go/internal/config/logical_channel.go`（行 463-499）

- 历史一次性缺陷曾给同账号不同协议渠道分别回填不同 `LogicalChannelUID`。
- `convergeLogicalByAccount` 以 `AccountUID` 为身份真相，把同账号物理渠道归并到首个遇到的 logical（canonical），其余清空 protocols，物理渠道的 `LogicalChannelUID` / `LogicalName` 强制重指。
- 遍历顺序固定为 messages → chat → responses → gemini → images → vectors，因此 canonical 选择幂等稳定。

### 3.6 协议去重规则

文件：`backend-go/internal/config/logical_channel.go`（行 262-310、430-447）

- 同 slice（协议 kind）在组内只保留一个物理渠道。
- `collectProtocolsFromEntries` 按固定顺序输出：`messages` → `chat` → `responses` → `gemini` → `images` → `vectors`。
- 若出现未知 kind，按字母序追加。
- `dedupProtocols` 按 `Kind` 去重，后出现的覆盖先出现的。

## 4. 冲突解决：同组多个物理渠道时如何保留/排序

### 4.1 协议级冲突

- 同一 logical 内每个 kind 只保留一个 protocol entry。
- 来源顺序为六个数组的固定遍历顺序，因此先遇到的物理渠道优先保留。
- `appendProtocolToLogical` 同 kind 覆盖。

### 4.2 名称推导

文件：`backend-go/internal/config/logical_channel.go`（行 355-377、574-599）

- 优先取组内非自动派生的 `UpstreamConfig.Name`。
- 自动派生格式判定：`... - chat`、`- codex`、`- gemini`、`- claude`、`- responses`。
- 无合适名称时，由主 URL host 与 provider/account 信息派生，格式如 `providerId · host` 或 `host`。

### 4.3 BaseURL 池

文件：`backend-go/internal/config/logical_channel.go`（行 312-330）

- 收集组内所有 `GetAllBaseURLs()`，trim 后去重，保留首次出现顺序。

### 4.4 Kind 推断

文件：`backend-go/internal/config/logical_channel.go`（行 332-353）

- 仅 images → `images`
- 仅 vectors → `embeddings`
- 其他（含 images+vectors 混合，或仅文本协议）→ `llm`

## 5. UID 生成规则

文件：`backend-go/internal/config/logical_channel.go`（行 113-123、416-428）

- 格式：`lc_` + 12 字节随机 hex，共 22 字符。
- 使用 `crypto/rand.Read`；若失败则回退到 `lc_` + 当前纳秒时间低 48 bit。
- `pickFreshUID` 在 `usedUIDs` 集合中碰撞时最多重试 16 次。

## 6. 聚合规则

当前 LogicalChannel 本身不直接存储“健康/质量/成本/能力标签”字段，聚合由前端和后端 dashboard 共同完成。

### 6.1 后端 Dashboard 聚合

文件：`backend-go/internal/handlers/logicalchannels/logical_channels.go`（行 119-193）

- 选择主物理渠道：`primaryPhysicalChannel` 按 `messages` → `chat` → `responses` → `gemini` → `images` → `vectors` 顺序挑选第一个存在的 protocol。
- 用 `common.BuildChannelView` 生成物理视图后，覆盖 `name`、`logicalChannelUid`、`logicalName`、`baseUrl`、`baseUrls`、`accountUid`、`providerId`、`tags`、`protocolRoutes`、`protocolCapsules`。
- metrics 使用主物理渠道在对应 kind 的 `MetricsManager` 中查询。

### 6.2 活跃状态聚合

文件：`backend-go/internal/handlers/logicalchannels/logical_channels.go`（行 197-208）

- `countActiveLogical`：任一 protocol 的 `Status` 为 `active`，该 logical 即视为活跃。

### 6.3 前端状态聚合

文件：`frontend/src/utils/unifiedChannels.ts`（行 307-315）

- `normalizeChannelStatus('healthy')` → `'active'`。
- `resolveGroupStatus`：组内所有物理协议状态一致则取该状态，否则为 `'partial'`。

### 6.4 凭证聚合

文件：`frontend/src/utils/unifiedChannels.ts`（行 286-305）

- `mergeAccountCredentials`：把同 logical 下各协议的 `apiKeys`、`apiKeyConfigs`、`disabledApiKeys` 去合并集。

## 7. CRUD 原子性

文件：`backend-go/internal/config/logical_channel_crud.go`

### 7.1 创建 `CreateLogicalChannel`

文件：`backend-go/internal/config/logical_channel_crud.go`（行 47-132）

- 入参校验：name 非空、baseUrls 至少一个、protocols 至少一个。
- kind 与 protocols 一致性校验：
  - `images` 只允许 `images` protocol。
  - `embeddings` 只允许 `vectors` protocol。
- 重名与同 site 冲突检查。
- 锁内创建 logical + 各 protocol 对应物理渠道。
- 任一 protocol 创建失败即 `rollbackCreatedChannelsLocked`，从六个数组中按 `ChannelUID` 移除已写入的物理渠道。
- 落盘失败同样回滚，并从 `LogicalChannels` 切片移除刚追加的 logical。

### 7.2 更新 `UpdateLogicalChannel`

文件：`backend-go/internal/config/logical_channel_crud.go`（行 301-471）

- 锁内操作。
- 备份原始 logical 副本、六个 Upstream 切片副本、`ManagedAccounts` 副本。
- 通用字段（Common）原子更新到 logical，并同步到每个物理渠道：
  - Name → `UpstreamConfig.Name`、`LogicalName`
  - Website、Description
  - Tags
  - BaseURLs → 重写物理渠道的 `BaseURLs` 并把首个设为 `BaseURL`
- Removals：删除指定 kind 的 protocol，同时删除对应物理渠道；拒绝删到 0 个 protocol。
- Protocols：已存在则更新物理渠道并替换 logical protocol entry；不存在则新增物理渠道。
- 任意失败则恢复备份，保证事务性。

### 7.3 删除 `DeleteLogicalChannel`

文件：`backend-go/internal/config/logical_channel_crud.go`（行 473-500）

- 锁内按 UID 找到 logical。
- 遍历 `logical.Protocols`，按 `kind + ChannelUID` 从六个物理数组中移除对应元素，并清空其 `LogicalChannelUID` / `LogicalName`。
- 从 `LogicalChannels` 切片移除 logical。
- 返回已删除的物理渠道列表，供上层清理 metrics/log。
- 落盘失败返回错误，不提交。

## 8. 物理渠道增删时逻辑渠道的自动重建策略

### 8.1 触发时机

文件：`backend-go/internal/config/config_loader.go`（行 186-194）

- 配置加载时：在所有迁移、校验完成后调用 `ensureLogicalBackfill`。
- `ensureLogicalBackfill` 在以下任一条件触发 `RebuildLogicalChannels`：
  - `LogicalChannelSchemaVersion != 1`
  - 任一物理渠道缺少 `LogicalChannelUID`
  - 检测到同 `AccountUID` 对应多个不同 `LogicalChannelUID`（`hasAccountUIDDivergence`）

### 8.2 非 CRUD 物理渠道变更

- 当前代码未在每次物理渠道增删后显式调用 `RebuildLogicalChannels`。
- 物理渠道变更后若 `LogicalChannelUID` 已回填且未发生账号分歧，不会自动重建；建议在相关 handler 完成后显式调用 `RebuildLogicalChannels` 或触发配置 reload。
- `ReloadFromMemory` 方法（仅供测试/手动）会重新回填。

## 9. 与 new-api 多账号的交互

### 9.1 账号身份合并

文件：`backend-go/internal/config/config_accounts.go`（行 459-757）

- `mergeManagedProviderAccounts` 在配置加载时按 BaseURL 站点把同一 provider 的历史渠道归并到同一 `AccountUID`。
- 使用并查集按 `utils.BaseURLSiteIdentities` 找同站账号并合并。
- 合并后同一账号在不同协议下的渠道共享 `AccountUID`、`ProviderID`，并按 kind 加后缀命名（如 `-claude`、`-chat`）。

### 9.2 LogicalChannel 对多账号的处理

文件：`backend-go/internal/config/logical_channel.go`（行 92-111）

- 归组规则把同一 `AccountUID` 的物理渠道强制合并到单一 logical。
- 不同 `AccountUID` 即使 site 完全一致也不合并。
- 这保证 new-api 多账号场景下，每个账号得到独立 logical 卡。

### 9.3 自动托管子类型

文件：`backend-go/internal/config/config.go`（行 126-128）

- `UpstreamConfig.AutoManagedKind`：`""` | `"generic"` | `"new_api"`
- 旧版 relay 托管渠道加载时回填为 `"new_api"`（`config_loader.go` 行 937-953）。

## 10. 与 healthcheck 探针的联动

### 10.1 配置结构

文件：`backend-go/internal/config/health_check.go`

- `GlobalHealthCheckConfig`：全局保活验证配置。
- `ChannelHealthCheckConfig`：渠道级配置，覆盖全局。
- `ResolvedHealthCheckPolicy`：合并后的最终策略。

### 10.2 解析入口

文件：`backend-go/internal/config/health_check.go`（行 67-146）

- `Config.ResolveHealthCheckPolicy(u *UpstreamConfig)` 解析单个物理渠道策略。
- 最小间隔硬下限 `MinHealthCheckInterval = 30 * time.Minute`。

### 10.3 与 LogicalChannel 的关系

- 当前 healthcheck 在物理渠道级执行（按 `MetricsIdentityBaseURL`、API key、service type 定位）。
- LogicalChannel dashboard 仅展示主物理渠道的聚合指标（`successRate`、`errorRate`、`consecutiveFailures`、`circuitState`、`latency` 等）。
- 逻辑渠道本身不单独被探测；其“健康状态”由主物理渠道 metrics 间接代表，或前端按各 protocol 状态聚合为 `partial`。

## 11. REST API

文件：`backend-go/internal/handlers/logicalchannels/logical_channels.go`

路由注册：`backend-go/main.go`（行 1426）

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/api/logical-channels?kind=llm` | 列表 / 按 kind 过滤 |
| GET | `/api/logical-channels/dashboard?kind=llm` | 聚合仪表盘 |
| GET | `/api/logical-channels/:uid` | 单条详情 |
| POST | `/api/logical-channels` | 创建 |
| PUT | `/api/logical-channels/:uid` | 更新 |
| DELETE | `/api/logical-channels/:uid` | 删除 |

### 11.1 Dashboard 详情

文件：`backend-go/internal/handlers/logicalchannels/logical_channels.go`（行 119-193）

- 返回 `channels`、`current`、`metrics`、`stats`、`recentActivity`。
- `stats.activeChannelCount` 使用 `countActiveLogical`。
- metrics 按主物理渠道查对应 kind 的 `MetricsManager`：
  - `ToResponseMultiURL`
  - `GetRecentActivityMultiURL`
- API key 集合包含当前 keys、historical keys、disabled keys，避免历史 metrics 丢失。

### 11.2 删除后的 metrics/log 清理

文件：`backend-go/internal/handlers/logicalchannels/logical_channels.go`（行 477-580）

- `Delete` 返回 `removedChannels` 后调用 `cleanupRemovedChannels`。
- 按 `ServiceType` 推断 kind，调用 `scheduler.DeleteChannelLogs` 和 `scheduler.DeleteChannelMetrics`。
- images/vectors 不经过 `ChannelScheduler` 记 metrics，无需处理。

## 12. 前端展示细节

### 12.1 类型定义

文件：`frontend/src/services/api-types.ts`

- `Channel` 字段：`logicalChannelUid`、`logicalName`、`protocolCapsules`、`protocolRoutes`（行 226-233）。
- `LogicalChannel`、`LogicalChannelProtocol`、`LogicalChannelKind`（行 2312-2343）。
- `ChannelProtocolCapsule`、`ChannelProtocolRoute`（行 312-351）。

### 12.2 统一渠道数据构建

文件：`frontend/src/utils/unifiedChannels.ts`

核心函数：

- `buildUnifiedChannelsData`：把 messages/chat/responses/gemini 四类响应合并成统一 LLM 视图。
- `logicalGroupKey`：决定分组 key，优先级：
  1. 后端回填的 `logicalChannelUid`。
  2. `accountUid`（账号托管渠道）。
  3. 非自动托管/无 provider 的手工渠道：按 `kind:index:channelUid` 独立成组。
  4. provider 自动托管：按 `providerId:name:apiKeyFingerprint` 分组，避免同 provider 多账号误合并。
- `annotateChannel`：为每个物理渠道标记 `routeKind`、`routeIndex`、`displayKey`。
- `selectPrimary`：按 `messages` → `chat` → `responses` → `gemini` 选主渠道。
- `buildProtocolCapsules`：按主顺序去重 serviceType，生成展示胶囊。
- `buildProtocolRoutes`：生成完整路由信息，含 index、name、serviceType、status、apiKeys 等。
- `buildDisplayChannel`：聚合 credentials、priority、status、protocolRoutes、protocolCapsules。

### 12.3 状态变更路由解析

文件：`frontend/src/utils/unifiedChannels.ts`（行 70-136）

- `resolveChannelStatusMutationRoutes`：状态变更时按 `protocolRoutes` 拆分为对物理渠道的请求；disabled 状态操作全部路由，否则跳过已 disabled 路由。
- `resolveChannelRecoveryRoutes`：恢复时同样按 `protocolRoutes` 拆分。

### 12.4 最近活动聚合

文件：`frontend/src/utils/unifiedChannels.ts`（行 372-430）

- `buildUnifiedRecentActivity`：按 `routeKind:channelIndex` 查找 activity，跨 protocol 聚合 segment、rpm、tpm。

### 12.5 拖拽排序载荷

文件：`frontend/src/utils/unifiedChannels.ts`（行 443-457）

- `buildUnifiedReorderPayloads`：把统一列表的全局位次拆回各 kind 的 `order` 与 `priorities`，避免各协议数组尺度不一导致刷新后顺序回弹。

### 12.6 Store 使用

文件：`frontend/src/stores/channel.ts`（行 147-162、198-219）

- `unifiedLlmChannelsData`：合并四类 LLM 渠道。
- `currentDashboardMetrics` / `currentDashboardRecentActivity`：对 LLM tab 做 routeKind 标注与活动聚合。
- `activeChannelCount` / `failoverChannelCount`：基于 `protocolRoutes` 计算。

## 13. 与 Scheduler 的交互

- 当前 `internal/scheduler` 不直接识别 `LogicalChannel`。
- 调度器仍以物理渠道为候选，以 `(MetricsIdentityBaseURL, APIKey, serviceType)` 为 metrics key。
- LogicalChannel 仅用于管理面聚合；运行时选择物理渠道后，其 metrics/log 清理通过 `ChannelScheduler.DeleteChannelMetrics` / `DeleteChannelLogs` 完成。

## 14. 关键测试覆盖

文件：`backend-go/internal/config/logical_channel_test.go`

- 同 `AccountUID` 合并。
- 同 `ProviderID` + 同 site 合并。
- 手工同站合并。
- 不同 provider / 不同 AccountUID / 不同 tenant path 不合并。
- 创建原子性与失败回滚。
- 删除一并移除物理渠道。
- 更新时增删 protocol。
- 拒绝删空 protocol。
- 旧配置加载时回填 `LogicalChannelUID`。
- 六类数组均回填。

文件：`backend-go/internal/config/logical_channel_converge_test.go`

- 同账号历史 UID 分歧收敛到 canonical logical。

## 15. 布局示意图

### 15.1 归组算法流程

```text
[RebuildLogicalChannels]
           │
           ▼
   [收集六个物理数组]
           │
           ▼
   [按 groupKey 分组]
           │
           ├─ accountUID 相同? ──→ 强制合并
           ├─ siteIdent 相同? ──→ 检查 provider/account
           └─ 其他 ──→ 不合并
           │
           ▼
   [convergeLogicalByAccount]
           │
           ▼
   [协议去重 dedupProtocols]
           │
           ▼
   [生成 LogicalChannel]
           │
           ├─ UID: lc_ + 12字节hex
           ├─ Name: 非自动派生名称优先
           ├─ BaseURLs: 去重保序
           └─ Kind: images/embeddings/llm
```

### 15.2 前端统一视图构建

```text
[四类 LLM 渠道响应]
           │
           ▼
   [buildUnifiedChannelsData]
           │
           ├─ logicalGroupKey 分组
           │     ├─ 后端回填 logicalChannelUid
           │     ├─ accountUid
           │     ├─ 手工渠道独立成组
           │     └─ provider 自动托管按 providerId:name:keyFingerprint
           │
           ▼
   [annotateChannel]
           │
           ▼
   [selectPrimary]
           │
           ▼
   [buildDisplayChannel]
           │
           ├─ mergeAccountCredentials
           ├─ resolveGroupStatus
           ├─ buildProtocolCapsules
           └─ buildProtocolRoutes
           │
           ▼
   [统一渠道列表展示]
```

### 15.3 CRUD 原子性

```text
[Create/Update/Delete LogicalChannel]
           │
           ▼
   [锁内操作]
           │
           ├─ 备份原始状态
           │
           ▼
   [执行变更]
           │
           ├─ 成功 → 落盘
           │
           └─ 失败 → 恢复备份
           │
           ▼
   [返回结果]
```

## 16. 待补充项详解

### 16.1 健康/质量/成本/能力标签字段持久化

**状态**：待实现（低优先级）。

**当前实现**

- `LogicalChannel` 结构体仅有通用 `Tags []string` 字段，没有区分健康、质量、成本、能力的专用标签字段。
- 后端 dashboard 接口 `/api/logical-channels/dashboard` 通过 `primaryPhysicalChannel` 选取主物理路由，再调用 `common.BuildChannelView` 和 metrics manager 聚合指标，最后把 `tags` 原样返回。
- 前端类型定义同样只有 `tags?: string[]`，没有健康/质量/成本/能力相关字段。
- Autopilot 内部有完整的 `HealthState`、`QualityTier`、`CostTier`、`StabilityTier`、`SpeedTier` 等枚举，但这些是运行时画像/评分类型，未与 `LogicalChannel` 持久化字段打通。

**缺口分析**

- 缺少逻辑渠道级的“健康/质量/成本/能力”聚合标签持久化，导致：
  - 前端无法在统一列表中直接展示逻辑渠道的综合健康/质量/成本/能力标签。
  - dashboard 目前只能透传物理渠道的实时 metrics（successRate、circuitState 等），无法给出一个稳定的逻辑渠道级标签。
  - Autopilot 评分时使用的是物理渠道/模型级画像，没有逻辑渠道级的归一化标签输入。

**建议方案**

1. 在 `LogicalChannel` 中新增专用字段：`HealthTag` / `QualityTag` / `CostTag` / `CapabilityTags`。
2. 在 `RebuildLogicalChannels` 中根据组内物理渠道状态推导默认值。
3. 前端 `admin-api.ts` 补齐对应字段，dashboard 视图直接渲染标签胶囊。

### 16.2 物理渠道非 CRUD 变更后的自动重建触发机制

**状态**：✅ **已修复**（2026-08-09，提交 `d876784d`）。

**修复方案**

在 `saveConfigLocked`（`config_loader.go:1155`）内、`deepCopy` 之前统一调用 `RebuildLogicalChannels(&config)`，确保任何持久化写盘后 logical 视图与物理视图一致。同时增强 `RebuildLogicalChannels`：
1. 已有 logical 先复制副本并清空 protocols，避免陈旧数据。
2. `convergeLogicalByAccount` 把同一托管账号的物理渠道强制收敛到单一 canonical logical。
3. `hasAccountUIDDivergence` 检测同账号多 UID 分歧。
4. 归组时检查物理渠道字段变更后是否仍属于旧 logical，若不再匹配则移出并重建。
5. 写回逻辑改为强制刷新 UID/Name，而非仅在空时回填。

**测试**：`TestPhysicalChannelChange_RebuildsLogicalChannels` 验证物理渠道 Add/Update/Remove 后 LogicalChannels 与 LogicalChannelUID/LogicalName 同步刷新。

### 16.3 与 Autopilot SmartRouter 的候选渠道收集联动

**状态**：待实现（中优先级，架构级改动）。

**当前实现**

- `SmartRouter.collectChannelEntries` 直接从六个物理数组遍历，按 `Status` 和 `APIKeys` 过滤。
- `buildChannelEntry` 基于单个 `UpstreamConfig` 构建 `channelScoreEntry`，评分维度全部来自模型画像、endpoint 画像、价格注册表，**未读取 `LogicalChannel` 的任何字段**。
- 整个 `internal/autopilot` 包没有任何代码引用 `LogicalChannel` 或 `LogicalChannelUID`。

**缺口分析**

- SmartRouter 完全不感知 LogicalChannel，候选渠道收集发生在物理层。
- 同一 LogicalChannel 下的多个协议/多个 BaseURL/多个 Key 会被当作独立候选分别评分。
- 用户从产品语义上认为是一张“渠道卡片”，但 Autopilot 的决策 trace 和候选排序仍以物理渠道为单位，前后端语义不一致。

**建议方案**

1. 在 `collectChannelEntries` 和 `executeFilter` 中为每个 `UpstreamConfig` 查找其所属 `LogicalChannel`。
2. 在 `channelScoreEntry` / `RoutingCandidate` 中新增 `LogicalChannelUID`、`LogicalChannelName` 字段。
3. 将 LogicalChannel 的健康/质量/成本/能力标签作为 `ScoringCandidate` 的补充输入或 fallback。
4. dry-run / BuildPlan 场景可在 LogicalChannel 维度聚合展示候选。
5. 注意兼容性：`LogicalChannel` 为空表示旧配置，SmartRouter 应回退到现有物理层行为。
