# 跨模块集成设计文档

> 本文档描述 Autopilot、LogicalChannel、New-API 集成、Healthcheck、Benchmark Chart 五个子系统之间的交互边界、事件传播与状态一致性。

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
| Benchmark Chart | presetstore, config, frontend | (无) |

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

### 3.5 Benchmark Chart 与 Autopilot

- 共享 `model-registry` 数据源
- benchmark profile 通过 `/api/presets` 分发给前端和 autopilot
- autopilot 在 `model_frontier_scoring.go` 中使用 benchmark lane 调整置信区间
- 前端目前**未消费** benchmark 数据进行可视化

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
2. **Benchmark Chart 前端缺失**：数据链路已通，但无 UI 消费
3. **New-API 无周期性自动余额刷新**：仅启动时同步 + 手动刷新
4. **AccessToken 明文落库**：与设计文档要求的加密存储不符
5. **跨模块事件总线缺失**：画像变更、健康状态变更等事件通过回调/hook 传播，无统一事件总线
6. **状态版本一致性**：LogicalChannel 重建与物理渠道变更之间的竞态未完全处理

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

## 8. 待补充

- LogicalChannel 归组算法的详细规则
- 跨模块事件总线设计
- 状态版本与竞态处理策略
- 统一配置变更传播机制
