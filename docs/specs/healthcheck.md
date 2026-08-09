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

**缺口分析**

- **代码常量副本与 JSON 副本未声明主从关系**：`builtin_models_manifest.go` 注释称“清单来源：火山方舟 Agent/Coding Plan 套餐概览(2026-07)”，但无版本戳、无生成时间、无与 `shared/builtin-models-manifest/builtin-models-manifest.json` 的一致性校验。
- **没有自动从火山侧拉取并回填清单的机制**：清单更新依赖人工维护 `shared/builtin-models-manifest/builtin-models-manifest.json`，再运行 `make generate-preset-manifest`；火山新增/下线模型时无法自动感知。
- **DisableProbe=true 导致未绑定 AK 的渠道长期依赖静态兜底**：若静态清单滞后，未绑定 AK 的火山渠道会暴露已不存在或缺失新模型的错误可用性。
- ~~**缺少 drift 告警**~~ ✅ 已实现（2026-08-10，提交 `22d4a11c`）：`AutoDiscoveryRunner.publishManifestDriftIfNeeded`（`auto_discovery.go`）在火山管控面 `FetchModels` 成功后，将线上清单与 `LookupBuiltinManifest` 内置兜底清单做 `diffModelLists`，有增删时发布 `manifest_drift` 事件（Phase B eventbus，Scope=config，Payload 带 `added`/`removed`）并打 `[AutoDiscovery-ManifestDrift]` 日志。纯观测信号：非阻塞、bus 为 nil 时空操作、不改调度。**当前无下游消费者**（仅日志 + 事件流可查），自动回填内置清单仍是后续项。

**建议方案**

1. **统一主从关系**：将 `shared/builtin-models-manifest/builtin-models-manifest.json` 设为唯一真相源；`builtin_models_manifest.go` 在测试构建期通过 `go:generate` 从 JSON 生成或仅保留最小 fallback，避免双副本。
2. **增加清单版本元数据**：在 `BuiltinModelsManifest` 中新增 `SourceVersion` / `UpdatedAt` / `SourceURL` 字段，并在 `PresetIndex` 或 `/api/presets/status` 中暴露。
3. **自动化清单刷新任务**：在 autopilot 的火山 plan 同步逻辑中，周期性对“已绑定 AK 且套餐状态 Running”的账号调用 `FetchModels`，将结果聚合后写入共享 JSON 或更新远程 preset shard，再触发 `generate-preset-manifest`。
4. **Drift 检测与告警**：新增 `/api/presets/manifest-drift` 或监控指标，对同一 plan 对比内置清单与最近 `FetchModels` 结果，记录新增/缺失模型；超过阈值时打日志并推送到管理界面。
5. **未绑定 AK 渠道的兜底置信度**：在 channel discovery 结果中标注 `ModelDiscoverySourceBuiltinFallback`，让前端或调度器在火山侧模型变化频繁时降低其优先级。

### 9.2 L2 请求特征与 capability test 的 drift 检测

> **实现状态（2026-08-10，提交 `18f71d05`）**：probe schema 版本化与观测性 drift 检测已落地（建议方案第 2、3 条核心路径）。新增 `capabilityProbeSchemaVersion` 常量（`capability_probe_models.go`），`CapabilityTestResponse`/`CapabilityTestJob` 携带该版本；capability cache key 与 execution lookup key 纳入版本号，probe 参数升级后旧缓存自然失效。`executeModelTest` 完成后将探测结果与 `config.ResolveUpstreamCapability` 声明能力比对，命中"registry 声明支持但探测失败"等偏差时打 `[Capability-Drift]` 日志（纯观测，不改调度/探测结论）。**未覆盖**：`shared/capability-probe-schema.json` 统一定义（前后端仍双副本硬编码模型列表）、drift 事件写入 eventbus/指标、CI nightly 全协议回归。以下为改造前基线。

**当前实现**

- L2 真实调用与 capability test 共用同一套请求构建路径：
  - 请求构建：`backend-go/internal/handlers/capability_test_request.go` 中的 `buildTestRequestWithModel`。
  - 对外封装：`backend-go/internal/handlers/healthcheck_probe.go` 提供 `BuildHealthCheckL2Request` 与 `SendHealthCheckL2Stream`。
- 探测模型列表硬编码在 `backend-go/internal/handlers/capability_probe_models.go` 中，前端副本在 `frontend/src/composables/useCapabilityTestManager.ts` 的 `capabilityPlaceholderModels`，双方注释互相提醒需同步。
- 请求特征（提示词、max_tokens、stream、reasoning_effort、system instruction）全部在代码中写死：
  - messages: 调用 `buildMessagesProbeBody` + `applyRequiredThinkingToCapabilityProbeBody`。
  - chat / responses / gemini: 内联 JSON。
- 没有显式版本字段或 schema 来描述“当前 L2 probe 特征集合”。

**缺口分析**

- **前后端硬编码模型列表同步风险高**：两个文件、两种语言，新增模型时容易遗漏前端或后端，导致首屏占位与真实探测不一致。
- **请求特征无版本化**：上游对特定提示词/参数组合的行为可能变化（如 reasoning_effort 取值、system 角色要求、Gemini thinkingConfig），当前无法在 capability 结果或 key_health 记录中标记“本次探测使用的是哪一版 probe schema”。
- **缺少与上游文档/行为的 drift 检测**：
  - 没有将探测结果与已知模型能力（来自 presetstore model registry）做回归比对。
  - 没有检测“某模型突然不再支持 streaming”或“某模型返回空流”的自动化告警。
- **capability cache 只按 key+protocol+models+mappingHash 缓存**：未包含 probe schema 版本，若 probe 参数变化，旧缓存可能误导结果。

**建议方案**

1. **统一 probe schema 定义**：将模型列表、提示词、参数约束、协议头抽取到 `shared/capability-probe-schema.json`，由前后端与 L2 共同读取；前端在构建时生成 TypeScript 常量，后端通过 `go:embed` 读取。
2. **引入 probe schema 版本**：在 `CapabilityTestResponse` / `KeyHealthRecord` / capability cache key 中加入 `probeSchemaVersion` 字段，确保参数升级后旧缓存失效。
3. **建立 drift 检测器**：
   - 在 capability test runner 中，将每个模型的探测结果（是否成功、是否支持 streaming、延迟、返回 token 数）与 model registry 中声明的 `Capabilities` / `ReasoningEfforts` 做比对。
   - 若某模型连续 N 次出现“registry 声明支持 streaming 但探测失败”或“reasoning 参数被拒绝”，生成 `capability_drift` 事件写入日志/指标。
4. **定期回归测试**：在 CI 或 nightly job 中对 mock 上游或沙箱 key 跑全协议 capability test，校验 `capabilityProbeModels` 中所有模型仍符合预期。
5. **capability cache 失效策略**：cache key 加入 schema version 与模型 registry dataVersion，保证 preset 更新后自动重新探测。

### 9.3 稀疏 L2 预算在大盘紧张时的动态调整策略

> **实现状态（2026-08-10，提交 `27dc64d2`）**：动态调整已落地（建议方案第 1、2 条核心路径）。`model_select.go` 新增 `effectiveSparseBudget(policy, modelCount, recentlyFailedCount, loadRatio)`，在 `selectL2ProbeModels` 顶部（`SparseL2MaxModels<=0` 门控后）按模型数/近期失败数/负载比动态放宽数量与成本上限：数量上界 = policy 值 + min(max(0, modelCount/3-1), 5)，成本上界 = 2×policy；`loadRatio>1` 时按 `1/loadRatio` 收缩，默认 0 为 no-op。只在静态 clamp 内放宽、recentlyFailed 模型仍不受成本限制，保证恢复探测。原始配置字段保留作上限，未新增配置面。**未覆盖**：AFP 余额联动、成本单位拆分（USD/AFP 仍共用 `CostAFP`）、分时段策略。以下为改造前基线。

**当前实现**

- 预算字段定义在 `backend-go/internal/config/health_check.go`：
  - `SparseL2MaxModels`：每 key 每周期最多探测模型数，默认 3。
  - `SparseL2MaxCostAFP`：每 key 每周期 AFP 成本预算上限，默认 6.0。
  - 支持全局配置与渠道级配置覆盖。
- 模型选择逻辑在 `backend-go/internal/healthcheck/model_select.go`：
  - 优先探测“最近失败”模型（不受成本预算限制）。
  - 其次探测“无近期成功记录”模型。
  - 最后按成本升序填充，直到 `maxModels` 或 `maxCostAFP`。
  - 火山渠道使用 `ResolveVolcengineAFPCost(now, "agent_plan", ...)` 估算 AFP 成本；非火山渠道使用 USD 定价相对成本。
- 调度并发控制：healthcheck `Manager` 使用 worker 池，`MaxConcurrency` 默认 4，按渠道去重；队列满时丢弃任务。
- 静默期：`L2ModelQuietPeriod` 默认等于 Interval，近期成功模型跳过。

**缺口分析**

- **预算是静态配置**：`SparseL2MaxModels` / `SparseL2MaxCostAFP` 只在配置解析时确定，不随系统负载、上游错误率、可用余额或时间窗口变化。
- **缺乏大盘负载感知**：
  - 不读取 scheduler/model circuit 状态来决定是否收紧/放宽探测。
  - 不感知火山套餐 AFP 余额（`VolcenginePlanUsage` 已Fetched，但 healthcheck 未使用）。
  - 不感知当前 healthcheck worker 队列深度或整体并发压力。
- **没有分时段/分套餐策略**：高峰时段仍按默认预算探测，可能加剧上游限流；低谷时段也没有自动扩容利用空闲窗口。
- **成本单位不一致**：非火山渠道用 USD 相对成本与火山 AFP 共用 `CostAFP` 字段，命名有误导性，且 USD 成本未按真实汇率/AFP 换算，预算比较不具备物理意义。

**建议方案**

1. **引入动态预算调节器**：
   - 在 `healthcheck.Manager` 中新增 `BudgetController`，根据以下信号实时调整有效预算：
     - 当前 worker 队列深度 / 在飞任务数；
     - 最近 N 分钟内 L2 失败率；
     - 火山套餐 AFP 剩余余额（对 `IsVolcengineProvider` 渠道）；
     - 时间窗口（如业务高峰自动降预算、低谷自动升预算）。
2. **将动态预算写入 `ResolvedHealthCheckPolicy` 扩展字段**：新增 `EffectiveSparseL2MaxModels` / `EffectiveSparseL2MaxCostAFP`，由 `selectL2ProbeModels` 使用；原始字段保留作为上限。
3. **AFP 余额联动**：对火山渠道，在 `checkKeyL2Sparse` 前查询该 key 对应套餐的 `VolcenginePlanUsage`，将 `SparseL2MaxCostAFP` 限制为剩余 AFP 的一定比例（如 5%），避免探测耗尽生产额度。
4. **负载熔断式降级**：当整体 L2 失败率超过阈值或队列持续满载时，临时将 `SparseL2MaxModels` 降到 1 或关闭非失败模型的稀疏探测，仅保留“最近失败”恢复探测。
5. **统一成本语义或拆分字段**：将非火山渠道的预算字段改名为 `SparseL2MaxCostUSDRelative` 或引入归一化成本指数；火山渠道保留 AFP，避免混合比较。
6. **可观测性**：在 `KeyHealthRecord` 或日志中记录本次实际预算（`effectiveMaxModels` / `effectiveMaxCost`）及触发原因，便于复盘大盘紧张时的探测行为。
