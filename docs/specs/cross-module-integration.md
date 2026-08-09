# 跨模块集成设计文档

> 本文档描述 Autopilot、LogicalChannel、New-API 集成、Healthcheck 四个子系统之间的交互边界、事件传播与状态一致性。

## 1. 总体关系图

```text
[Client Request]
      │
      ▼
┌─────────────────┐
│  Protocol Handler │  /v1/messages /v1/chat/completions /v1/responses ...
└────────┬────────┘
         │
         ▼
┌─────────────────┐     ┌─────────────────┐
│  LogicalChannel │◄────│  Model Registry │ (capability, effort, context, benchmark)
│  (归组/聚合层)   │     └─────────────────┘
└────────┬────────┘
         │
         ▼
┌─────────────────┐     ┌─────────────────┐
│    Autopilot    │◄────│   Healthcheck   │ (L1/L2 probe state, circuit)
│  SmartRouter    │     └─────────────────┘
│  EndpointPolicy │
└────────┬────────┘
         │
         ▼
┌─────────────────┐     ┌─────────────────┐
│    Scheduler    │◄────│   New-API Sync  │ (group multiplier, key ownership)
│ channel picker  │     └─────────────────┘
└────────┬────────┘
         │
    ┌────┴────┐
    ▼         ▼
[New-API]  [Direct Providers]
Channels   (Claude/OpenAI/Gemini/...)
    │
    ▼
[Upstream Response]
    │
    ▼
[Learning Loop]  ← FastDecay / RateLimit / Document / Context / Healthcheck
```

## 2. 模块间依赖关系

| 模块 | 依赖 | 被依赖 |
|------|------|--------|
| LogicalChannel | config, handlers | frontend, scheduler |
| Autopilot | config, scheduler, metrics, ratelimit, keypool, handlers/common | main.go |
| New-API Integration | autopilot, config, handlers | main.go, frontend |
| Healthcheck | config, metrics, scheduler, upstreamprobe, handlers/common | main.go |

## 3. 关键交互边界

### 3.1 LogicalChannel 与 Scheduler

- LogicalChannel 在**展示层**聚合，Scheduler 在**执行层**选择物理渠道
- SmartRouter 收集候选渠道时，先经过 LogicalChannel 归组，再展开为物理渠道
- 健康状态在逻辑渠道层聚合，在物理渠道层执行熔断

### 3.2 Autopilot 与 Scheduler

| 交互点 | 位置 | 方向 |
|--------|------|------|
| `SetCandidateFilterProvider` | `main.go:741` | autopilot → scheduler |
| `SetModelSupportResolverProvider` | `main.go:884` | autopilot → scheduler |
| `SetEndpointPolicyProviderHook` | `main.go:780` | autopilot → handlers/common |
| `SetNotifyEndpointResultHook` | `main.go:804` | handlers/common → autopilot |
| `SetRoutingOutcomeRecorderHook` | `main.go:768` | handlers/common → autopilot |
| `SetAttemptRecorderHook` | `main.go:774` | handlers/common → autopilot |

### 3.3 Autopilot 与 Healthcheck

- **无直接包依赖**（反向包循环禁止）
- 通过共享 `upstreamprobe` 包间接交互
- Healthcheck 探针失败通过 `recordFailure` 回调喂给 scheduler 熔断器
- Autopilot 的 `HealthAnalyzer` 读取 scheduler 熔断状态作为诊断信号

### 3.4 New-API Integration 与 Autopilot

- New-API 是 autopilot 的订阅 provider 之一
- `NewApiSubscriptionSyncService` 与 `ProfileStore` 共享同一 `*sql.DB`
- provision 后的渠道通过 `TriggerDiscovery` 纳入 autopilot 画像体系
- key 的 `GroupMultiplier` 参与 SmartRouter 的成本评分

## 4. 事件传播链

### 4.1 请求路由决策链

```text
[Request]
   │
   ▼
[BuildRequestProfile]
   │
   ▼
[SmartRouter.CandidateFilterForWithActual]
   │
   ├─ 收集候选渠道（LogicalChannel 层）
   ├─ 解析模型映射
   ├─ 构建评分 entry
   ├─ Advisor hint / 人工意图
   ├─ 硬约束过滤
   └─ 返回排序渠道
   │
   ▼
[Scheduler.SelectChannelWithOptions]
   │
   ├─ X-Channel / ManualOverride / Promotion
   ├─ ProtocolFederation
   ├─ SmartFilter
   ├─ ModelCircuit 过滤
   ├─ Trace 亲和
   ├─ PrioritySort
   └─ Fallback
   │
   ▼
[EndpointPolicy.BuildEndpointPolicy]
   │
   ├─ FilterURLs / SortURLs
   ├─ FilterKeys / SortKeys
   ├─ FilterKeyBindings / SortKeyBindings
   └─ ResolvedTargetForBinding
   │
   ▼
[Upstream Call]
```

### 4.2 失败学习传播链

```text
[Upstream Failure]
   │
   ├─ 400/422 document → SharedChannelCompatCache
   ├─ 400/422 context → SharedChannelCompatCache
   ├─ 429/5xx → MetricsManager.RecordFailure
   ├─ auth/permission → ShouldBlacklistKey → BlacklistKeyWithRecoverAt
   └─ TTFB 拥塞 → RateLimitDiscoverer.Observe
   │
   ▼
[SmartRouter.buildChannelEntry]
   │
   ├─ learnedDocumentUnsupported → document_unsupported
   ├─ learnedContextLimit → context_limit
   └─ RateLimitDiscoverer.SuggestedLimit → rate_limit
   │
   ▼
[HealthAnalyzer.Diagnose]
   │
   └─ CircuitBreakerOpen / FastDecay / ConsecutiveFail
   │
   ▼
[ChannelProfile/KeyEndpointProfile 更新]
```

### 4.3 New-API 同步传播链

```text
[NewApiSubscriptionSyncService.SyncNow]
   │
   ├─ Verify → 余额/账号信息
   ├─ FetchGroups → GroupMultipliers
   ├─ FetchModels → AvailableModels
   │
   ▼
[Patch SubscriptionProfile]
   │
   ▼
[reconcileChannels]
   │
   ├─ 按 SourceRemoteTokenID / KeyUID 匹配
   ├─ ownership 冲突 → relink_required
   └─ 合并 desired key 元数据进 APIKeyConfigs
   │
   ▼
[TriggerDiscovery]
   │
   ▼
[AutoDiscoveryRunner]
   │
   ├─ discoverEndpoints
   ├─ probeEndpoint
   ├─ discoverEndpointProtocols
   └─ writeProfileForEndpoint
   │
   ▼
[KeyEndpointProfile 更新]
```

## 5. 状态一致性边界

### 5.1 健康状态一致性

| 层级 | 状态来源 | 更新频率 |
|------|----------|----------|
| Physical Channel | MetricsManager CircuitBreaker | 实时 |
| Endpoint | HealthAnalyzer.Diagnose | 5min |
| Channel | AggregateChannelProfile | 5min |
| LogicalChannel | 聚合物理渠道 | 重建时 |
| Key Health | Healthcheck Probe | 按策略分档 |

### 5.2 成本状态一致性

| 层级 | 状态来源 | 更新频率 |
|------|----------|----------|
| Key Multiplier | New-API Sync | 15min TTL |
| Endpoint Cost | Profiler.DeriveEndpointProfile | 5min |
| Channel Cost | AggregateChannelProfile | 5min |
| Model Cost | ModelRegistry / AFP Pricing | 启动时 + 定期 |

### 5.3 模型能力一致性

| 层级 | 状态来源 | 更新频率 |
|------|----------|----------|
| Registry | shared/model-registry/ccx_model_registry.json | 手动/定期 |
| Runtime | PresetStore → ModelRegistry | 启动时 + 后台更新 |
| Channel | AutoDiscovery → KeyEndpointProfile | Discovery 触发时 |
| Model | ModelProfileStore | 探测/请求反馈时 |

## 6. 已知缺口与风险

1. **LogicalChannel 归组逻辑未完全文档化**：分组键、冲突解决策略需补充
2. **New-API 无周期性自动余额刷新**：仅启动时同步 + 手动刷新
3. **AccessToken 明文落库**：与设计文档要求的加密存储不符
4. **跨模块事件总线缺失**：画像变更、健康状态变更等事件通过回调/hook 传播，无统一事件总线
5. **状态版本一致性**：LogicalChannel 重建与物理渠道变更之间的竞态未完全处理

## 7. 布局示意图

### 7.1 模块交互时序

```text
[Client]    [Handler]   [LogicalChannel]  [Autopilot]   [Scheduler]   [Upstream]
   │            │              │              │              │            │
   │  Request   │              │              │              │            │
   │───────────>│              │              │              │            │
   │            │ BuildProfile │              │              │            │
   │            │─────────────>│              │              │            │
   │            │              │ 候选渠道      │              │            │
   │            │              │─────────────>│              │            │
   │            │              │              │ 评分/过滤     │            │
   │            │              │              │─────────────>│            │
   │            │              │              │              │ 选择渠道    │
   │            │              │              │              │───────────>│
   │            │              │              │              │            │
   │            │              │              │              │  Response  │
   │            │              │              │              │<───────────│
   │            │              │              │ 学习反馈      │            │
   │            │              │              │<─────────────│            │
   │            │              │              │              │            │
   │            │              │              │ 更新 Profile  │            │
   │            │              │              │─────────────>│            │
```

### 7.2 状态同步矩阵

```text
                LogicalChannel   Autopilot   Scheduler   Healthcheck   New-API
LogicalChannel       -             读          读           -             -
Autopilot            写             -          写           读            读
Scheduler            读             读          -           读            读
Healthcheck          -             写          写           -             -
New-API              -             写          读           -             -
```

## 8. 配置变更传播机制

### 8.1 配置变更入口

| 配置类型 | 变更入口 | 影响模块 |
|----------|----------|----------|
| 物理渠道增删改 | `/api/{kind}/channels/*` | LogicalChannel, Scheduler, Healthcheck |
| 逻辑渠道增删改 | `/api/logical-channels/*` | LogicalChannel, Frontend |
| 订阅/账号变更 | `/api/subscriptions/*` | New-API, Autopilot |
| 模型注册表 | `shared/model-registry/` | Autopilot |
| 路由配置 | `/api/autopilot/routing-config` | Autopilot |
| 健康检查策略 | `/api/health-check/*` | Healthcheck |

### 8.2 传播路径

```text
[Config Change]
      │
      ├─ 物理渠道变更 ──→ RebuildLogicalChannels ──→ Frontend 刷新
      │                      │
      │                      └─→ Scheduler 候选列表更新
      │                      │
      │                      └─→ Healthcheck 扫描列表更新
      │
      ├─ 订阅变更 ──→ NewApiSubscriptionSyncService.SyncNow
      │                      │
      │                      └─→ reconcileChannels
      │                      │
      │                      └─→ TriggerDiscovery
      │                      │
      │                      └─→ KeyEndpointProfile 更新
      │
      ├─ 模型注册表变更 ──→ PresetStore.Swap
      │                      │
      │                      └─→ ModelRegistry 重建
      │                      │
      │                      └─→ Autopilot 评分参数更新
      │
      └─ 路由配置变更 ──→ AutopilotRoutingConfig 热更新
                             │
                             └─→ SmartRouter 评分权重/开关更新
```

### 8.3 热更新与重启边界

- `.config/config.json` 修改后自动热重载（文件监听）
- `.env` 修改后需要重启服务
- `PresetStore` 支持后台原子替换，无需重启
- `LogicalChannel` 重建是内存操作，立即生效

## 9. 状态版本与竞态处理策略

### 9.1 版本控制点

| 状态 | 版本字段 | 并发控制 |
|------|----------|----------|
| SubscriptionProfile | `Version` | `Patch` 乐观锁 |
| APIKeyConfig | `MultiplierUpdatedAt` | 时间戳比较 |
| KeyHealthRecord | `LastCheckAt` | UPSERT 覆盖 |
| ModelProfile | `ProbeVersion` | 探测版本比较 |
| LogicalChannel | 无显式版本 | 重建时全量替换 |

### 9.2 竞态场景与处理

1. **LogicalChannel 重建 vs 物理渠道变更**
   - 场景：RebuildLogicalChannels 执行期间，物理渠道被增删
   - 当前处理：重建是内存操作，基于当前 config snapshot；若变更发生在重建后，需下次重建生效
   - 风险：短期展示不一致
   - 建议：重建后触发事件通知前端刷新

2. **New-API 同步 vs 渠道编辑**
   - 场景：SyncService 正在 reconcileChannels，用户同时编辑渠道 key
   - 当前处理：`newAPIProvisionMu` 串行化 provision；reconcile 基于 config snapshot
   - 风险：reconcile 结果覆盖用户手动修改
   - 建议：reconcile 前检查 `MultiplierUpdatedAt`，冲突时标记 `relink_required`

3. **Healthcheck 探针 vs 真实请求**
   - 场景：探针和真实请求同时失败，重复喂熔断器
   - 当前处理：熔断器本身有滑动窗口去重
   - 风险：无，熔断器设计允许重复失败计数

4. **AutoDiscovery vs 手动渠道配置**
   - 场景：Discovery 发现新模型，用户同时手动修改渠道配置
   - 当前处理：Discovery 结果写入 `KeyEndpointProfile`，不直接改 `UpstreamConfig`
   - 风险：无，Discovery 是画像层，不直接改配置层

### 9.3 一致性保证级别

| 级别 | 适用场景 | 实现方式 |
|------|----------|----------|
| 强一致 | 配置写入 | 单线程 config manager + 文件锁 |
| 最终一致 | 画像/健康状态 | 异步 worker + 定期刷新 |
| 读写一致 | 调度决策 | 基于当前 snapshot，允许短期不一致 |
| 展示一致 | 前端 UI | 事件驱动刷新 + 轮询兜底 |

## 10. 待补充项详解

### 10.1 跨模块事件总线设计

> **实现状态（2026-08-09，Phase B 已落地）**：下方"当前实现/缺口分析"为改造前基线。统一事件总线已按"建议方案"第 1/2/3/5 条核心路径实现，提交 `267f82d6`（B.1）/ `10b99f1c`（B.2）/ `1891c7d2`（B.3）。
>
> - **B.1 总线基础 + 熔断/Key 事件**：新增叶子包 `internal/eventbus`（统一 `Event` envelope + 非阻塞 `Bus`，缓冲 64、慢订阅者丢弃）。`MetricsManager`/`ConfigManager` 经 `SetEventBus` 可选注入（`atomic.Pointer`，nil-safe）。熔断三迁移点（`channel_metrics_circuit.go`）发 `circuit_breaker_state_changed`；`config.go` Key 方法发 `key_blacklisted`/`key_restored`/`key_model_disabled`/`key_model_restored`（key 用 `utils.MaskAPIKey` 脱敏）。新增 `StateEventStore`（环形内存 + SQLite `state_events` + 30 天清理，复用 `ProfileChangelogStore` 模式）与 `GET /api/health-center/state-events`（REST）+ `GET /api/health-center/state-events/stream`（WS）。
> - **B.2 配置/preset 细粒度事件**：`saveConfigLocked` 比对 6 类渠道 slice 发 `upstream_changed`；`loadConfig` 末尾发 `config_reloaded`；`RebuildLogicalChannelsAndPublish` 发 `logical_channel_rebuilt`；`presetstore.Swap` 发 `preset_bundle_swapped`。`RegisterOnConfigChange` 回调兼容层完整保留（5 个 reconcile 未迁移，因其已是锁外异步投递，与事件订阅等价，迁移无功能收益仅增风险）。
> - **B.3 前端事件总线**：引入 `mitt`；`composables/useEventStream.ts` 维护单例 WS 连接 `/state-events/stream`，按 Type 分发、引用计数管理生命周期、指数退避重连。`ChannelsView`/`HealthCenterView` 熔断/Key/渠道状态事件驱动即时刷新（400ms 去抖），`useGlobalTick` 轮询降级为兜底（未移除）。
> - **后续增补事件（2026-08-10）**：`manifest_drift`（提交 `22d4a11c`，火山管控面清单与内置兜底 diff，`auto_discovery.go` 发布，Scope=config，Payload `added`/`removed`）。此外 capability probe 观测性漂移走 `[Capability-Drift]` 日志（提交 `18f71d05`，尚未上事件总线）。
>
> **守住的红线**：总线为可选依赖（未注入则 publish no-op，系统行为不变）；非阻塞发布不阻塞热路径；事件仅通知、非真相源——调度仍以 `GetConfig()`/`GetKeyCircuitState()` 为权威，前端保留轮询兜底。
>
> **未覆盖（留待后续）**：限速/学习事件（`rate_limit_*`）、请求/路由事件（`attempt_*`）、订阅/New-API 事件（`subscription_*`）、多实例总线（NATS/Redis Streams）、事件 schema 版本化；`manifest_drift` 与 `capability_drift` 目前仅观测（无下游自动消费/回填）。

**当前实现（改造前基线）**

后端事件传播机制分散，无统一总线：

| 机制 | 位置 | 作用 | 局限 |
|------|------|------|------|
| 配置变更回调 | `config.ConfigManager.RegisterOnConfigChange` | 配置文件热重载后广播 Config 快照 | 粗粒度，接收方需自行 diff |
| 预置数据变更回调 | `presetstore.PresetStore.RegisterOnChange` | PresetBundle 原子替换后通知 | 仅 model registry 消费 |
| 上游信号回调 | `ratelimit.SetUpstreamSignalCallback` | 429/header 信号喂给 Autopilot | 单一回调点 |
| Endpoint policy hook | `handlers/common.SetEndpointPolicyProviderHook` | Autopilot 注入每请求策略 | 运行时依赖注入 |
| Endpoint 结果通知 hook | `handlers/common.SetNotifyEndpointResultHook` | 更新 FastDecayScorer | 单向通知 |
| 画像变更 EventHub | `autopilot/event_hub.go` | ProfileChangeEvent fan-out | 仅 autopilot 内部，仅向前端广播 |
| 状态转换日志 | `statelog/logging.go` | 统一日志格式 | 只写不广播 |
| 前端事件 | 原生 `window.addEventListener` | 主题、快捷键、桌面端 auth | 无统一总线 |

**缺口分析**

1. **无统一事件总线**：新增跨模块响应需改多处 `main.go` 接线，易遗漏。
2. **画像事件仅限前端展示**：后端调度/熔断/限速模块无法消费画像变更。
3. **熔断状态迁移不可订阅**：前端健康中心无法实时展示熔断 open/closed，需轮询。
4. **配置变更回调粗粒度**：接收方拿到整个 Config，需自行 diff，效率低且易错。
5. **Key 拉黑/恢复无事件**：Scheduler/Healthcheck/前端无法实时感知 key 可用性变化。
6. **PresetStore 变更通知范围窄**：仅 model registry 消费，scheduler/autopilot 未订阅。
7. **事件无 schema/版本**：未来事件扩展易破坏前后端契约。
8. **前端无事件总线**：组件间状态同步依赖 props/store，跨分支通信困难。

**建议方案**

1. **短期：扩展现有 `EventHub` 为后端统一总线**
   - 将 `autopilot.EventHub` 升级为 `internal/eventbus`，支持泛型或统一 `Event` envelope。
   - 保留现有 `ProfileChangeEvent` 兼容，新增 `CircuitStateEvent`、`ConfigChangeEvent`、`KeyStateEvent` 等。
   - 订阅者可按 `EventType` 过滤，发布非阻塞、慢消费者丢弃。
   - 关键状态事件（熔断、Key 拉黑）同时落盘到 `ProfileChangelogStore` 或新建 `StateTransitionStore`。

2. **中期：统一配置变更传播**
   - 将 `ConfigManager.RegisterOnConfigChange` 改造为基于事件总线：
     - 发布 `ConfigReloadedEvent`（全量快照）+ 细粒度 `UpstreamChangedEvent` / `ChannelStatusChangedEvent`。
     - 现有回调可保留为兼容层，内部转为订阅者。
   - `PresetStore.RegisterOnChange` 同样接入总线，发布 `PresetBundleSwappedEvent`。

3. **中期：熔断/Key 状态事件化**
   - 在 `metrics/channel_metrics_circuit.go` 状态迁移点发布 `CircuitBreakerStateChangedEvent`。
   - 在 `config.go` `BlacklistKey` / `RestoreKey` / `DisableKeyModel` 发布 `KeyStateChangedEvent`。
   - 前端健康中心 WebSocket 不仅推送画像事件，也推送熔断/key 状态事件。

4. **长期：事件 schema 与持久化**
   - 定义统一 `Event` 结构：eventUID、eventType、subject、scope、from、to、cause、payload、createdAt。
   - 引入轻量级 SQLite 事件日志表（类似 `profile_changelog`），支持按 subject/channel 查询历史。
   - 如需高可用/多实例，可替换为 NATS / Redis Streams，但当前单进程架构下内存总线+SQLite 足够。

5. **前端事件总线**
   - 引入轻量 `mitt` 或基于 Vue 的 `provide/inject` 事件总线，统一桌面端 auth、版本检查、渠道刷新等跨组件通信。
   - 后端 WebSocket 事件接入一个中央 `EventSource` composable，按类型分发给订阅组件。

**应覆盖的事件类型**

> 已实现（eventbus 常量，2026-08）：`circuit_breaker_state_changed`、`key_blacklisted`、`key_restored`、`key_model_disabled`、`key_model_restored`、`config_reloaded`、`upstream_changed`、`channel_status_changed`、`logical_channel_rebuilt`、`preset_bundle_swapped`、`manifest_drift`。以下为完整目标清单（未标注即待实现）。

- 画像变更：`profile_updated`、`health_changed`、`discovery_completed`、`auto_mapping_applied`
- 健康/熔断：`circuit_breaker_state_changed`✅、`key_blacklisted`✅、`key_restored`✅、`healthcheck_probe_failed`、`probe_recovered`
- 配置变更：`config_reloaded`✅、`upstream_changed`✅、`logical_channel_rebuilt`✅、`preset_bundle_swapped`✅、`manifest_drift`✅
- 限速/学习：`rate_limit_discovered`、`rate_limit_applied`、`rate_limit_cooldown_triggered`
- 请求/路由：`routing_decision_made`、`attempt_started`、`attempt_completed`、`attempt_failed`
- 订阅/New-API：`subscription_synced`、`subscription_balance_changed`、`managed_account_keys_changed`
