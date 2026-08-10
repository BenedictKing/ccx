# Healthcheck 设计文档

## 1. 目录结构与文件职责

`backend-go/internal/healthcheck/` — 保活验证核心包（后台调度 + L1/L2 探针 + 结果处置）

| 文件 | 职责 |
| --- | --- |
| `manager.go` | 调度器 `Manager`：扫描循环、worker 池、任务去重、单渠道全 key 编排、L1/L2 分派 |
| `check.go` | L1 探针实现 `checkKeyL1`、到期判定 `channelDue`、jitter、响应归一化、错误摘要、模型 ID 提取 |
| `l2.go` | L2 真实调用验活 `checkKeyL2` / `probeOneModel` / 稀疏探测 `checkKeyL2Sparse`、最便宜模型选择 |
| `model_select.go` | 稀疏 L2 的模型选择器 `selectL2ProbeModels`、AFP/USD 成本估算、`l2:<model>` check_kind 编解码 |
| `handlers.go` | 管理 API：`ChannelHealthHandler`（GET 策略+记录）、`TriggerChannelCheckHandler`（POST 立即触发） |
| `check_test.go` / `l2_test.go` / `manager_test.go` / `model_select_test.go` / `per_key_test.go` | 测试 |

相关支撑包/文件（在 healthcheck 之外）：
- `backend-go/internal/config/health_check.go` — 保活策略配置类型与解析 `ResolveHealthCheckPolicy`
- `backend-go/internal/upstreamprobe/volcengine.go` — 火山 Plan 数据面共享探针（与 autopilot 共用）
- `backend-go/internal/metrics/sqlite_store_key_health.go` — `key_health` 表持久化（`KeyHealthRecord`）
- `backend-go/internal/metrics/model_circuit.go` — 模型级熔断追踪器 `ModelCircuitTracker`
- `backend-go/main.go:133-204` — L1Fetcher 接线、火山探针分流、拉黑/喂熔断回调注入
- `backend-go/internal/scheduler/recovery.go` + `internal/config/config.go` — 渠道/Key 恢复
- `backend-go/internal/autopilot/volcengine_coding_plan.go` — 火山管控面签名客户端（用量/套餐识别，凭证回填）

关键架构约束：healthcheck 不依赖 autopilot（反向包循环禁止）；火山探针提取到中性包 `upstreamprobe`，供 healthcheck 保活与 autopilot 新渠道验证共享，避免请求特征漂移。

## 2. L1（Plan/账号级）与 L2（模型级）探针设计

### 2.1 验证级别常量（`manager.go:16-28`）
- `CheckKindL1 = "l1"`、`CheckKindL2 = "l2"`、`CheckKindL2ModelPrefix = "l2:"`
- 状态：`StatusOK = "ok"`、`StatusAuthFailed = "auth_failed"`、`StatusError = "error"`
- 到期判定只看 L1 记录（`groupL1Records` 只保留 `CheckKind==l1`）。

### 2.2 L1 探针（`check.go:checkKeyL1`, line 135）
- 目的：带单个 key 拉取上游模型列表，验证账号/凭证可达性。六类渠道通用（`messages/chat/responses/gemini/images/vectors`）。
- 输入 `L1Request`（`manager.go:41`）：`BaseURL/APIKey/ServiceType/AuthHeader/CustomHeaders/ProxyURL/InsecureSkipVerify`。
- 输出 `L1Response`（`manager.go:52`）：`StatusCode/Body/RealCallVerified/Model`。`RealCallVerified` 标记该次 L1 已发起过真实推理调用（火山探针置位，通用 `/v1/models` 不置位）。
- 遍历 `baseURLs` 逐个尝试：
  - 200 → `succeeded`，`countModels` 统计模型数，`extractModelIDs` 抽取模型 ID（OpenAI `data[].id` 或 Gemini `models[].name`），`break`。
  - 401/403 → `authFailed`，立即停止遍历（不再试其他 BaseURL）。
  - 其他/超时/5xx → `error`。
- `normalizeWrappedResponse`（`check.go:269`）把各渠道 `GetChannelModels` handler 的包装响应归一化：200 透传；400+`{statusCode,details}` 还原为上游 401；502/504 归 0（网络错误）；其他透传。
- 结果处置（`check.go:202-225`）：
  - 成功 → `LastStatus=ok`，`ConsecutiveFailures=0`。
  - 认证失败 → `LastStatus=auth_failed`，`ConsecutiveFailures=prev+1`，若 `common.ShouldBlacklistKey` 判定应拉黑则调 `blacklist` 回调。
  - 其他错误 → `LastStatus=error`，喂 `recordFailure` 回调（归因到 `lastBaseURL`）。
  - 每次都 `UpsertKeyHealth`。

### 2.3 L2 探针（`l2.go`）
- `supportsL2`（`l2.go:19`）：仅 `messages/chat/responses/gemini` 支持 L2；images/vectors 直接跳过不写记录。
- `checkKeyL2`（`l2.go:30`）：对 L1 成功且 `RealCallVerified=false` 的 key 做一次真实调用验活。选模型：显式 `VerifyModel` 优先，否则 `selectCheapestModel` 从 L1 模型列表按定价选 input+output 单价最低者；全部无定价且未指定 → 跳过不写记录（避免污染状态）。
- `probeOneModel`（`l2.go:59`）：核心 L2 执行。
  - 按 key 裁剪渠道副本 `probeChannel`：`APIKeys=[apiKey]`、`DisabledAPIKeys=nil`、覆盖 `BaseURL/BaseURLs` 为该 key 绑定端点（`keyBaseURLs`），避免把其他凭证/跨套餐地址带入探针。
  - 复用能力测试路径：`handlers.BuildHealthCheckL2Request`（= `buildTestRequestWithModel`）构建请求，`handlers.SendHealthCheckL2Stream`（= `sendAndCheckStream`）发送并做流式预检（`PreflightStreamEventsWithOptions`，`TreatThinkingAsContent`）。请求特征见 `capability_test_request.go:218`（Claude Code system 指纹、Codex `Originator`、思考参数等）。
  - 处置与 L1 同构：成功 `ok`（`detail="model=<model>"`）；401/403 `auth_failed`+拉黑；其他 `error`+喂熔断（归因到 `keyBaseURLs[0]`）。`consecutive_failures` 基于该 `(key, check_kind)` 上次记录。

### 2.4 请求构建入口（`healthcheck_probe.go`）
`handlers/healthcheck_probe.go` 是 L2 复用能力测试路径的薄适配：`BuildHealthCheckL2Request` / `SendHealthCheckL2Stream`。

## 3. 动态频率、稀疏模型级探针、共享探针与去重机制

### 3.1 频率策略与分档（`config/health_check.go`）
- `GlobalHealthCheckConfig`（全局，line 10）与 `ChannelHealthCheckConfig`（渠道级，line 29）合并为 `ResolvedHealthCheckPolicy`（line 46）。
- `MinHealthCheckInterval = 30 * time.Minute`（硬下限，clamp）。
- OriginTier 分档默认（`healthCheckTierDefaults`, line 64）：
  - `third`（公益站）：30min + 默认开 L2（`VerifyRealCall=true`）
  - `second`（中转站）：2h + L2 关
  - `first/local/unknown`（官方等）：6h + L2 关
- 覆盖优先级：渠道级字段 > 全局字段 > OriginTier 分档默认（`ResolveHealthCheckPolicy`, line 77）。
- 新增稀疏 L2 相关默认字段：`SparseL2MaxModels=3`、`SparseL2MaxCostAFP=6.0`、`L2ModelQuietPeriod`（默认继承 Interval）。
- 扫描间隔为固定 `defaultScanInterval=5min`（`manager.go:34`），但每渠道有效到期时间通过 `jitteredInterval` 施加基于 `(channelType,channelID)` FNV hash 的 ±10% 确定性抖动（`check.go:117`），避免整点齐发。所谓“自适应/动态频率”即分档 Interval + 抖动 + `channelDue` 判定的组合，而非改扫描 tick。

### 3.2 到期判定（`check.go:channelDue`, line 95）
1. 无 L1 记录 → 立即到期（启动首扫）。
2. 存在无记录的 eligible key（新增 key）→ 到期。
3. `maxLast + jitteredInterval` 已过 → 到期。

### 3.3 稀疏模型级 L2（`l2.go:checkKeyL2Sparse` + `model_select.go`）
- 触发条件：火山套餐 L1 已 `RealCallVerified=true`（L1 已消耗一次最便宜模型的真实调用），其余模型改走预算受限的稀疏探测（`manager.go:389-390`）。
- 每个模型独立落 `check_kind="l2:<model>"`（`l2ModelCheckKind`），避免模型状态互相覆盖，无需 schema 迁移。
- `selectL2ProbeModels`（`model_select.go:56`）排序规则：
  1. 最近失败模型最优先（验证恢复），可突破成本预算。
  2. 无近期成功记录的次之。
  3. 剩余按成本升序（火山用 `ResolveVolcengineAFPCost` AFP 成本，非火山用 USD 等价 `pricingCost`）。
- 预算：`SparseL2MaxModels` 数量上限 + `SparseL2MaxCostAFP` AFP 成本上限；最近失败模型不受成本预算限制。
- 别名去重：`ResolveVolcengineAFPCost` 返回 `IsAlias/AliasOf`（如 `glm-latest`→`glm-5.2`），规范模型在列表内时跳过别名。
- 静默期 `L2ModelQuietPeriod`：近期成功模型在该期内降级（不重复浪费）。
- 内存熔断信号：注入 `modelCircuitLookup`（`manager.go:153`），`circuit.IsModelCircuitOpen` 提供更快的失败信号，把熔断中模型标记为 `RecentlyFailed` 优先探测恢复。

### 3.4 共享火山探针（`upstreamprobe/volcengine.go`）
- `IsVolcenginePlanBaseURL`（line 54）：精确匹配官方 host `ark.cn-beijing.volces.com` + path 前缀 `/api/plan`|`/api/coding`，不用裸 `Contains` 防误命中中转站。
- `ProbeVolcenginePlanWithModels`（line 167）：按 serviceType 选 Claude(`/messages`) 或 OpenAI(`/chat/completions`) 请求；模型选择 `volcenginePlanProbeModel`（line 77）——优先 `deepseek-v4-flash`，否则 Agent Plan 按 AFP 选最便宜、Coding Plan 回退首个候选。
- 严格策略（`postJSONProbe`, line 207）：只接受真实 2xx；401/403 标记 `AuthFailed`；其他 4xx/5xx/网络错误保留原状态不推断 key 无效。
- `VolcenginePlanL1Probe`（line 190）：成功后用 `config.LookupBuiltinManifest` 内置 manifest 模型清单生成标准 OpenAI models 列表；命中官方入口但无 manifest 时返回空 `data` 列表（不臆造模型）。
- Claude Code 请求特征来自 `utils/claude_code_probe.go`（UA `claude-cli/…`、`X-App`、`anthropic-beta`、system 身份、`metadata.user_id` session）。

### 3.5 去重机制
- **任务级去重**：`Manager.submit`（`manager.go:289`）用 `inFlight` map 按 `channelType/channelID` 去重，队列满则丢弃（下轮重试），`taskQueueSize=256`。
- **渠道内 key 串行**：`checkChannel`（`manager.go:328`）对渠道内 key 串行执行，避免对同一上游并发打。
- **L1/L2 同周期去重**：`RealCallVerified=true` 时跳过等价 L2（火山 L1 已是真实调用），改走稀疏探测；`RealCallVerified=false`（通用 `/v1/models`）L1 成功后仍执行 L2（非火山不回归）。

### 3.6 L1Fetcher 接线与火山分流（`main.go:137 healthCheckL1Fetcher`）
- 命中 `IsVolcenginePlanBaseURL` → 走 `VolcenginePlanL1Probe`，用内置 manifest 候选动态选模型，返回 `RealCallVerified=true`。
- 否则走 `channelModelsHandlerFetcher`（各渠道 `GetChannelModels` handler 的 httptest 包装，`main.go:101`），`RealCallVerified=false`。

## 4. 探针结果如何影响 scheduler 的选择/熔断/恢复

### 4.1 结果落库
`KeyHealthRecord`（`metrics/sqlite_store_key_health.go:9`）：`ChannelType/ChannelID/KeyMask/CheckKind/LastCheckAt/LastStatus/ConsecutiveFailures/LatencyMs/ModelCount/Detail`。表 `key_health` 主键 `(channel_type, channel_id, key_mask, check_kind)`，UPSERT 语义。该表**仅供保活自身到期判定和管理 API 展示**，不直接被 scheduler 读取。

### 4.2 探针失败 → 熔断（间接）
探针失败通过 `main.go:939` 注入的 `recordFailure` 回调，喂 `channelScheduler.RecordFailure(baseURL, apiKey, serviceType, kind)`（`delegates.go:24`）→ `MetricsManager.RecordFailure` 写入 Key 级熔断滑动窗口；同时写渠道日志（`RecordChannelLogWithSource`，`RequestSource=healthcheck`，`metrics/channel_log.go:65`）。

scheduler 选渠道读的是运行时熔断/健康度，探针失败与真实请求失败共同累积到同一 breaker：
- `channelCircuitState` → `GetChannelCircuitStateMultiURL`（`select.go:806`）
- `channelIsHealthy` → `IsChannelHealthyMultiURL`
- `channelFailureRate` → `CalculateChannelFailureRateMultiURL`

调度优先级链路（`select.go:SelectChannelWithOptions`, line 217）：X-Channel pin → ManualOverride → Promotion（绕过健康检查）→ ProtocolFederation → SmartFilter → **模型级熔断过滤 `filterChannelsByModelCircuit`** → Trace 亲和 → 优先级遍历（跳过 circuit_open/unhealthy/cooldown）→ fallback（失败率最低）。

### 4.3 模型级熔断（`metrics/model_circuit.go`）
- 权威 store，三元组 `(channelUID, keyHash, model)`，`model=""` 为全局桶。
- 双阈值：快速 5min/2 次、慢速 30min/3 次；退避 60s 起翻倍上限 30min；恢复后再次失败按退避递增（`recordModelFailureAt:207`）。
- `ChannelModelCircuitOpen`（line 316）：全部候选 key 都熔断才升级为渠道级排除（`select.go:filterChannelsByModelCircuit`，fail-open）。
- `IsAvailable`（line 376）：keypool 选 key 时的查询入口（global+exact 双桶）。
- 稀疏 L2 探针通过 `modelCircuitLookup` **读取**该 tracker 判断哪些模型需优先探测恢复；真实请求路径通过 `recordModelCircuitFailure/Success`（`upstream_failover.go:1768/1785`）**写入**。保活探针本身通过 `recordFailure` 喂的是 Key 级 breaker，不直接写模型级 tracker。

### 4.4 恢复（`scheduler/recovery.go` + `config/config.go`）
- 定时恢复：`RunScheduledRecoveries`（UTC 0/8/16 点，`main.go` goroutine 驱动）与 `RunDueRecoveries`（到 `RecoverAt` 即恢复）。
- `restoreKeysForKind`（`recovery.go:187`）：只恢复 `IsAutoRecoverableDisabledReason` 原因的 key（余额/额度类），`RestoreDisabledKeys` 移回 APIKeys，`transitions.RestoreDisabledKeysAndActivate` 编排 `MoveKeyToHalfOpen` + 激活渠道；同时 `RestoreExpiredKeyModels` 清理到期的 `(Key,模型)` 限制。
- 手动恢复：`ResumeChannelWithKind`（`handlers/channel_metrics_handler.go:975`）→ `transitions.RestoreAllAndReset` = `RestoreAllKeys`（恢复全部禁用+手动暂停 key）+ `ResetChannelMetrics`。
- 认证失败（`authentication_error`）无 `RecoverAt`，必须手动恢复，不参与定时自动恢复。

## 5. per-key 路由、凭证回填、失败诚实态等边界

### 5.1 per-key BaseURL 路由（`config/config_utils.go`）
- `BoundBaseURLForKey`（line 1147）：从 `APIKeyConfigs[].BaseURL` 查该 key 绑定端点（归一化）。
- `BaseURLsForKey`（line 1166）：绑定则只返回该端点（单元素），未绑定回退 `GetAllBaseURLs`（历史笛卡尔积）。
- 保活在 `checkChannel`（`manager.go:380`）对每个 key 单独 `u.BaseURLsForKey(apiKey)`，绑定的 Agent Plan key 不会误打到 Coding Plan `/api/coding` 入口。L2 副本同步覆盖绑定端点，`recordFailure` 归因到实际绑定 BaseURL（`l2.go:120`）。
- eligible key 过滤（`check.go:eligibleKeys`, line 54）：跳过 `IsKeyDisabledNow` 禁用期内与 `APIKeyConfigs.Enabled=false` 的 key。

### 5.2 凭证回填 / 用量恢复（`config/config_accounts.go`）
- `TryRestoreDisabledKeysByUsage`（line 1280）：套餐 provider 用量刷新后按余量恢复因余额/限额被禁用的 key，支持 Kimi/MiMo/Compshare/Volcengine 四类；采 AND 语义（所有窗口有余量才恢复）。
- 火山：`VolcenginePlanUsageWindow`（Agent Plan 有 Quota+Used，Coding Plan 仅 UsedPercent+ResetTime），耗尽且重置未到不恢复。
- 触发点：`autopilot/handlers_auto_managed.go:1213`（`handleRefreshVolcenginePlanUsage` 用量刷新成功后）等多处。
- 火山管控面签名客户端 `volcenginePlanClient`（`autopilot/volcengine_coding_plan.go`）：HMAC-SHA256 签名，`GetPersonalPlan`/`FetchModels`/`FetchUsage`（`GetAFPUsage`/`GetCodingPlanUsage`）。AK/SK 只用于管控面识别与用量，**不作为推理 key 数据面可用性证明**（推理 key 可用性仍靠数据面探针）。

### 5.3 失败诚实态（保守边界，`upstreamprobe/volcengine.go` + `check.go`）
- 火山探针只接受 2xx 为成功；401/403 才 `AuthFailed`；400/404/5xx/网络错误记 `error` 不喂认证黑名单（`postJSONProbe:230`）。
- 拉黑唯一入口 `common.ShouldBlacklistKey`（`common/failover.go:1181`）：区分 `authentication_error`/`permission_error`/`insufficient_balance`/`insufficient_quota`，可从 message 提取 `RecoverAt`；rate_limit 类不拉黑（交熔断）。
- L2 请求构建失败（本地配置问题）只打日志不写记录，不污染健康态（`l2.go:87`）。
- fail-open 原则：全渠道模型熔断时保留原列表让真实错误返回（`select.go:filterChannelsByModelCircuit:752`）。

## 6. 与 providers/volcengine 的交互

healthcheck 不直接依赖 provider 适配层，而是通过 `internal/upstreamprobe`（中性共享包）与火山交互：

- **数据面探针**（推理 key 可用性）：`upstreamprobe.VolcenginePlanL1Probe` / `ProbeVolcenginePlan`。healthcheck L1（`main.go:143`）与 autopilot 新渠道验证（`autopilot/verify_endpoint.go:324 verifyVolcenginePlanEndpoint`）共用同一实现，避免请求特征漂移。请求走真实 `/messages` 或 `/chat/completions`，非管控面。
- **管控面**（套餐识别/模型发现/用量，凭证回填）：`autopilot/volcengine_coding_plan.go volcenginePlanClient`（HMAC 签名），供用量刷新→`TryRestoreDisabledKeysByUsage` 恢复。
- **内置 manifest**（`config/builtin_models_manifest.go`）：火山 Agent/Coding Plan 条目 `DisableProbe=true`（普通 key 无法用 `/v1/models` 探测），保活 L1 探针成功后用 `volcengineAgentPlanModelIDs`/`volcengineCodingPlanModelIDs` 生成模型清单。
- **AFP 定价**（`config/volcengine_afp_pricing.go`）：`ResolveVolcengineAFPCost` 供稀疏 L2 选模型按成本排序，含输入分段（≤32k×0.67 / ≤128k×1 / >128k×2）与活动倍率、别名解析。

## 7. 关键状态流转

**保活周期**：Manager 启动 → `loop` 首扫 + 每 5min tick → `scan` 遍历六类渠道，读全量 `key_health` 分组，对 active+enabled+有 eligible key 且 `channelDue` 的渠道 `submit` 任务（inFlight 去重）→ worker 取任务 `checkChannel` → 渠道内 key 串行：`checkKeyL1`（按 `BaseURLsForKey` 探测）→ 成功且需 L2 时，`RealCallVerified` 分流：火山走 `checkKeyL2Sparse`（预算受限模型级），其他走 `checkKeyL2`（最便宜单模型）→ 各自 `UpsertKeyHealth`。

**失败传播**：探针 error → `recordFailure` 回调 → scheduler Key 级 breaker + 渠道日志（source=healthcheck）；认证失败 → `ShouldBlacklistKey` → `BlacklistKeyWithRecoverAt` 写 `DisabledAPIKeys`（额度类带 RecoverAt，认证类需手动）。

**恢复**：额度类 key 到 RecoverAt 或用量刷新有余量 → `RestoreDisabledKeys`/`TryRestoreDisabledKeysByUsage` → `MoveKeyToHalfOpen` + 激活渠道；模型级熔断到 openUntil 自动放行，恢复后再失败按退避递增。

## 8. 布局示意图

### 8.1 保活调度内部结构

```text
[Manager]
   │
   ├─ loop (首扫 + 5min tick)
   │     │
   │     ▼
   │   scan()
   │     ├─ 读取六类渠道
   │     ├─ 读取 key_health 全量记录
   │     ├─ 分组 groupL1Records
   │     └─ 对 active+enabled+有 eligible key+channelDue 的渠道 submit
   │
   ├─ inFlight map[(type,id)]struct{}  ← 任务级去重
   │
   ├─ taskQueue (256) ← worker pool
   │     │
   │     ▼
   │   checkChannel(type, id, channel)
   │     ├─ 按 key 串行
   │     │     │
   │     │     ▼
   │     │   checkKeyL1(key, L1Request)
   │     │     ├─ BaseURLsForKey(apiKey)
   │     │     ├─ 火山? VolcenginePlanL1Probe : channelModelsHandlerFetcher
   │     │     └─ 结果 → UpsertKeyHealth
   │     │
   │     │   (L1 ok && RealCallVerified=false)
   │     │     │
   │     │     ▼
   │     │   checkKeyL2(key, model)
   │     │     └─ probeOneModel()
   │     │
   │     │   (L1 ok && RealCallVerified=true)
   │     │     │
   │     │     ▼
   │     │   checkKeyL2Sparse(key)
   │     │     └─ selectL2ProbeModels(...)
   │     │         ├─ RecentlyFailed 优先
   │     │         ├─ NoRecentSuccess 次之
   │     │         └─ 按 AFP/USD 成本升序
   │     │
   │     └─ 结果处置
   │           ├─ ok → UpsertKeyHealth
   │           ├─ auth_failed → UpsertKeyHealth + blacklist()
   │           └─ error → UpsertKeyHealth + recordFailure()
   │
   └─ blacklist / recordFailure / modelCircuitLookup (注入回调)
```

### 8.2 探针结果与调度器联动

```text
[Healthcheck Probe]
        │
        ▼
   UpsertKeyHealth (key_health 表)
        │
        ├─ 保活自身到期判定 channelDue
        ├─ 管理 API 展示
        └─ 不直接被 scheduler 读取
        │
        ▼
   recordFailure / blacklist
        │
        ├─→ scheduler Key 级 breaker (RecordFailure)
        │   → channelCircuitState / channelIsHealthy
        │   → SelectChannelWithOptions 跳过
        │
        └─→ ModelCircuitTracker (真实请求路径)
            → filterChannelsByModelCircuit
```

## 9. 待补充项详解

### 9.1 火山 Coding Plan 与 Agent Plan 的模型清单 manifest 更新机制

**当前实现**

- 火山方舟 `/api/plan`（Agent Plan）与 `/api/coding`（Coding Plan）的内置清单存在两个同源副本：
  - 代码常量：`backend-go/internal/config/builtin_models_manifest.go` 中的 `volcengineAgentPlanModelIDs()` 与 `volcengineCodingPlanModelIDs()`。
  - 预置 JSON：`shared/builtin-models-manifest/builtin-models-manifest.json`，通过 `scripts/generate-preset-manifest.mjs` 生成到 `backend-go/internal/presetstore/embedded/builtin-manifest.json`，并参与 `presetstore` 远程/缓存/embedded 三层更新。
- 运行时 `config.LookupBuiltinManifest` 优先读取 `presetstore.Default().Get()` 中的运行时清单，fallback 到代码常量。
- 当用户绑定火山 Access Key 后，自动发现流程通过 `autopilot/volcengine_coding_plan.go` 的 `FetchModels` 调用火山管控面 `ListArkAgentPlanModel` / `ListArkCodingPlanModel` 获取真实清单，覆盖内置兜底清单。
- 预置更新链路：`PresetUpdater` 每 30 分钟拉取 `PresetUpdateURL` 的 index + shards，校验 schemaVersion、dataVersion、SHA256 后 Swap；`/api/presets/status` 暴露 source / dataVersion / lastSuccessAt。

- 运行时 `config.LookupBuiltinManifest` 优先读取 `presetstore.Default().Get()` 中的运行时清单，fallback 到代码常量。
- 当用户绑定火山 Access Key 后，自动发现流程通过 `autopilot/volcengine_coding_plan.go` 的 `FetchModels` 调用火山管控面 `ListArkAgentPlanModel` / `ListArkCodingPlanModel` 获取真实清单，覆盖内置兜底清单。
- **Drift 观测**：`AutoDiscoveryRunner.publishManifestDriftIfNeeded`（`auto_discovery.go`）在 `FetchModels` 成功后将线上清单与 `LookupBuiltinManifest` 内置兜底清单 `diffModelLists`，有增删时发布 `manifest_drift` 事件（eventbus，Scope=config，Payload `added`/`removed`）并打 `[AutoDiscovery-ManifestDrift]` 日志。纯观测：非阻塞、bus 为 nil 时空操作、不改调度。
- 预置更新链路：`PresetUpdater` 每 30 分钟拉取 `PresetUpdateURL` 的 index + shards，校验 schemaVersion、dataVersion、SHA256 后 Swap；`/api/presets/status` 暴露 source / dataVersion / lastSuccessAt。

**待排期缺口**

- **代码常量副本与 JSON 副本未声明主从关系**：`builtin_models_manifest.go` 与 `shared/builtin-models-manifest/builtin-models-manifest.json` 双副本，无版本戳/生成时间/一致性校验。建议将 JSON 设为唯一真相源，代码侧 `go:generate` 或仅保留最小 fallback；并在 `BuiltinModelsManifest` 增 `SourceVersion`/`UpdatedAt`/`SourceURL` 元数据。
- **manifest_drift 部分消费已落地**：事件化 + StateEventStore 持久化 + 前端健康中心告警已实现；自动回填内置清单（周期性 `FetchModels` 聚合回写共享 JSON）仍未实现。
- **未绑定 AK 渠道的兜底置信度**：可在 discovery 结果标注 `ModelDiscoverySourceBuiltinFallback`，让调度在火山侧模型频繁变化时降低其优先级。

### 9.2 L2 请求特征与 capability test 的 drift 检测

**当前设计**

- L2 真实调用与 capability test 共用同一套请求构建路径：
  - 请求构建：`backend-go/internal/handlers/capability_test_request.go` 中的 `buildTestRequestWithModel`。
  - 对外封装：`backend-go/internal/handlers/healthcheck_probe.go` 提供 `BuildHealthCheckL2Request` 与 `SendHealthCheckL2Stream`。
- 请求特征（提示词、max_tokens、stream、reasoning_effort、system instruction）在代码中定义（messages 走 `buildMessagesProbeBody` + `applyRequiredThinkingToCapabilityProbeBody`，其余协议内联 JSON）。
- **Probe schema 版本化**：`capability_probe_models.go` 定义 `capabilityProbeSchemaVersion` 常量（探针特征集版本，模型列表/提示词/参数约束/协议头任一变更时递增）。`CapabilityTestResponse` / `CapabilityTestJob` 携带该版本；cache key 与 execution lookup key 纳入版本号，probe 参数升级后旧缓存自然失效。
- **前后端统一 schema**：`shared/capability-probe-schema.json` 作为单一真相源，承载 `schemaVersion` / `baseProtocols` / `probeModels` / `frontendPlaceholderModels`。后端 `capability_probe_models.go` 经 `go:embed` 读取嵌入副本，前端 `useCapabilityTestManager.ts` 与 `CapabilityTestDialog.vue` 直接 import 该 JSON，消除前后端模型列表与基础协议双副本。
- **Drift 观测与事件化**：`executeModelTest` 完成后将探测结果与 `config.ResolveUpstreamCapability` 声明能力比对，命中"registry 声明支持但探测失败"等偏差时打 `[Capability-Drift]` 日志，并经注入的共享 eventbus 发布 `capability_drift` 事件（Scope=config，Payload 含模型/声明能力/实际结果）。事件经 StateEventStore 持久化并由健康中心以告警展示；纯观测，不改调度/探测结论。

**待排期缺口**

- **无定期回归**：CI/nightly 未对 mock 上游或沙箱 key 跑全协议 capability test 校验 `capabilityProbeModels`。

### 9.3 稀疏 L2 预算在大盘紧张时的动态调整策略

**当前设计**

- 预算字段定义在 `backend-go/internal/config/health_check.go`：`SparseL2MaxModels`（每 key 每周期最多探测模型数，默认 3）、`SparseL2MaxCostAFP`（每 key 每周期 AFP 成本预算上限，默认 6.0），支持全局与渠道级覆盖。这两个静态值作为动态调整的**下界/上限 clamp**。
- **动态放宽**：`model_select.go` 的 `effectiveSparseBudget(policy, modelCount, recentlyFailedCount, loadRatio)` 在 `selectL2ProbeModels` 顶部（`SparseL2MaxModels<=0` 门控后）按模型数/近期失败数/负载比动态调整有效上限——数量上界 = policy 值 + `min(max(0, modelCount/3-1), 5)`，成本上界 = 2×policy；`loadRatio>1` 时按 `1/loadRatio` 收缩，默认 0 为 no-op。只在 clamp 内放宽，`recentlyFailed` 模型仍不受成本限制以保证恢复探测。
- 模型选择顺序：最近失败（不受成本限制）→ 无近期成功记录 → 成本升序填充至有效 `maxModels`/`maxCostAFP`。
- **成本语义拆分**：候选模型成本以 `CostValue`+`CostUnit`（`probeCostUnit`：AFP/USD）表示，火山渠道标 AFP（`ResolveVolcengineAFPCost`），非火山渠道标 USD 相对成本。排序与预算累加仅同单位比较/累计，杜绝 AFP 与 USD 混加；配置字段 `SparseL2MaxCostAFP` 保持不变（向后兼容）。
- **AFP 余额联动**：`Manager` 经 `SetProbeUsageResolver` 注入余额查询器（`ProbeUsageResolver` 接口由 ConfigManager 实现，避免 healthcheck 直接 import autopilot）。`selectL2ProbeModels` 对火山渠道按剩余 AFP（各窗口 `Quota-Used` 最小值）的 5% 收紧成本上限；无 resolver / 快照为 nil / 无 Quota 时保持原上限不放大，余额为零时关闭非失败模型的 AFP 探测预算。
- 调度并发：healthcheck `Manager` worker 池，`MaxConcurrency` 默认 4，按渠道去重，队列满丢弃。静默期 `L2ModelQuietPeriod` 默认等于 Interval。

**待排期缺口**

- **无分时段策略**：高峰/低谷未按时间窗口自动降/升预算。
