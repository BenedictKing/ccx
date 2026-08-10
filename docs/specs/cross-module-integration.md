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

1. **LogicalChannel 归组逻辑未完全文档化**：分组键、冲突解决策略需补充。
2. **状态版本一致性**：LogicalChannel 重建与物理渠道变更之间的竞态未完全处理（`saveConfigLocked` 内统一重建已缓解主路径，见 `logical-channel.md` §16.2）。
3. **观测事件无自动消费**：`manifest_drift` / `capability_drift` 仅暴露信号，无自动回填或告警下游（见 §10.1 未覆盖）。

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

统一事件总线 `internal/eventbus` 是叶子包（仅依赖标准库，可被任意 internal 包 import 而不引入包循环），承载跨模块状态变更的发布/订阅。

**核心契约**

- 统一 `Event` envelope：`UID / Type / Scope / Subject / ChannelKind / From / To / Cause / Payload / CreatedAt`，只携带脱敏字段（key 经 `utils.MaskAPIKey`）。
- `Bus`：非阻塞发布、缓冲 64、慢订阅者丢弃；按 `Type` 过滤订阅。
- 可选依赖：各 Manager 经 `SetEventBus` 注入（`atomic.Pointer`，nil-safe），**未注入时 publish 为 no-op，系统行为不变**。
- 事件仅通知、非真相源：调度仍以 `GetConfig()` / `GetKeyCircuitState()` 为权威，前端保留轮询兜底。

**事件来源与落点**

| 事件 Type | 发布位置 | 触发 |
|-----------|----------|------|
| `circuit_breaker_state_changed` | `metrics/channel_metrics_circuit.go` 三迁移点 | 熔断 open/half/closed 迁移 |
| `key_blacklisted` / `key_restored` / `key_model_disabled` / `key_model_restored` | `config.go` Key 方法 | Key 拉黑/恢复/模型级禁用 |
| `upstream_changed` / `config_reloaded` / `logical_channel_rebuilt` | `config_loader.go`（`saveConfigLocked` diff 6 类渠道 / `loadConfig` / `RebuildLogicalChannelsAndPublish`） | 配置写入与热重载 |
| `preset_bundle_swapped` | `presetstore.Swap` | 预置数据原子替换 |
| `manifest_drift` | `autopilot/auto_discovery.go` | 火山管控面清单与内置兜底 diff 有增删 |

**持久化与消费**

- `StateEventStore`（环形内存 + SQLite `state_events` 表 + 30 天清理，复用 `ProfileChangelogStore` 模式）持久化关键事件；`GET /api/health-center/state-events`（REST 历史）+ `GET /api/health-center/state-events/stream`（WebSocket 实时）。
- 前端 `composables/useEventStream.ts`（`mitt`）维护单例 WS 连接，按 Type 分发、引用计数管理生命周期、指数退避重连；`ChannelsView`/`HealthCenterView` 事件驱动即时刷新（400ms 去抖），`useGlobalTick` 轮询降级为兜底。
- `RegisterOnConfigChange` 回调兼容层完整保留（5 个 reconcile 仍走原锁外异步投递路径，与事件订阅等价）。

**未覆盖（待排期）**

- 事件族未补齐：限速/学习（`rate_limit_*`）、请求/路由（`attempt_*`）、订阅/New-API（`subscription_*`）。
- `manifest_drift` 自动回填内置清单的消费者仍未实现（事件化 + 前端告警已落地）；`capability_drift` 已事件化并上总线、前端健康中心展示。
- 多实例总线（NATS / Redis Streams）：当前单进程架构下内存总线 + SQLite 已足够。
- 事件 schema 版本化。

**应覆盖的事件类型**

> 已实现（eventbus 常量，2026-08）：`circuit_breaker_state_changed`、`key_blacklisted`、`key_restored`、`key_model_disabled`、`key_model_restored`、`config_reloaded`、`upstream_changed`、`channel_status_changed`、`logical_channel_rebuilt`、`preset_bundle_swapped`、`manifest_drift`。以下为完整目标清单（未标注即待实现）。

- 画像变更：`profile_updated`、`health_changed`、`discovery_completed`、`auto_mapping_applied`
- 健康/熔断：`circuit_breaker_state_changed`✅、`key_blacklisted`✅、`key_restored`✅、`healthcheck_probe_failed`、`probe_recovered`
- 配置变更：`config_reloaded`✅、`upstream_changed`✅、`logical_channel_rebuilt`✅、`preset_bundle_swapped`✅、`manifest_drift`✅
- 限速/学习：`rate_limit_discovered`、`rate_limit_applied`、`rate_limit_cooldown_triggered`
- 请求/路由：`routing_decision_made`、`attempt_started`、`attempt_completed`、`attempt_failed`
- 订阅/New-API：`subscription_synced`、`subscription_balance_changed`、`managed_account_keys_changed`
