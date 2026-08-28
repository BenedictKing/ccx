# Autopilot 设计文档

## 1. 总体架构

Autopilot 是 CCX 的智能路由与渠道托管子系统，由 Manager + SmartRouter + EndpointPolicy + Trace 四层构成。

```text
[Client Request]
      │
      ▼
┌─────────────────┐
│  Protocol Handler │  /v1/messages /v1/chat/completions ...
└────────┬────────┘
         │ 提取脱敏特征
         ▼
┌─────────────────┐
│ RequestProfile  │  Model, Kind, HasImage, HasDocument, EstTokens,
│   Builder       │  QualityNeed, ContextNeed, Effort(off..ultra), TaskClass, Domain
└────────┬────────┘
         │ context 传递
         ▼
┌─────────────────┐     ┌─────────────────┐
│   SmartRouter   │◄────│  Model Registry │ (capability, effort, context)
│  Channel Filter │     └─────────────────┘
└────────┬────────┘
         │ 返回排序渠道列表
         ▼
┌─────────────────┐     ┌─────────────────┐
│    Scheduler    │◄────│   Healthcheck   │ (L1/L2 probe state)
│ channel picker  │     └─────────────────┘
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│  EndpointPolicy │  URL/Key 级排序与过滤
│ (per-attempt)   │
└────────┬────────┘
         │
         ▼
   [Upstream Call]
         │
         ▼
┌─────────────────┐
│  Trace Store    │  记录 RoutingDecisionTrace
│ + Learning Loop │  失败/成功反馈更新 profile
└─────────────────┘
```

## 2. 目录结构与文件职责

`backend-go/internal/autopilot/` 共 225 个 `.go` 文件，约 85,205 行代码（2026-08-26 实测）。按职责分组如下。

### 2.1 核心管理/生命周期

| 文件 | 职责 |
|---|---|
| `manager.go` | `Manager` 总控；启动 worker、组件持有、与 scheduler/main.go 接线 |
| `profile_store.go` | `ProfileStore`：endpoint 画像的内存缓存 + SQLite 异步持久化 |
| `schema_migration.go` | SQLite 画像库 schema 版本管理 |
| `async_writer.go` | 有界异步 writer，供 TraceStore 等批量落盘；指标计数（dropped/writeErrors 等）为 `atomic.Int64`，enqueue/flush/读取三方无锁并发安全 |

### 2.2 请求画像与分类

| 文件 | 职责 |
|---|---|
| `request_profile.go` | `RequestProfile`、`ClassifierInput`、`IntentEffortPin` 定义 |
| `request_profile_builder.go` | `BuildRequestProfile`、`ResolveQualityTarget` |
| `request_profile_context.go` | context key，请求画像跨层传递 |
| `request_correlation.go` | 请求 correlation ID 载体 |
| `task_classifier.go` | 确定性 `Classify`，产出 `TaskClass` |
| `task_complexity.go` | `InferTaskComplexity`：从 prompt 信号提取难度 |
| `task_domain.go` | `InferTaskDomain`、域关键词表、域强度证据 |
| `effort_normalize.go` | `EffortLevel` 8 档归一化与档位距离计算 |

### 2.3 画像推导与健康

| 文件 | 职责 |
|---|---|
| `profiler.go` | `Profiler.DeriveEndpointProfile`：把指标快照转成 `KeyEndpointProfile` |
| `channel_profile.go` | `AggregateChannelProfile`：多 endpoint 聚合为渠道级视图 |
| `key_endpoint_profile.go` | `KeyEndpointProfile` 全字段、origin/cost 解析 |
| `health_analyzer.go` | `HealthAnalyzer.Diagnose`：Dead/Limited/Misconfigured/Degraded/Healthy |
| `fast_decay.go` | `FastDecayScorer`：白嫖/临时渠道快速衰减评分 |
| `stability_hysteresis.go` | 稳定性晋降级滞后窗口 |
| `carry_forward.go` | 跨轮画像字段 carry-forward 规则 |
| `profile_change_detector.go` | 画像变更事件检测 |
| `profile_changelog.go` | 变更日志存储 |
| `event_hub.go` | 画像变更事件广播 |
| `time_series.go` | 时间桶指标聚合 |
| `usage_metering.go` | `UsageMeter`：按 endpoint 的请求计数窗口 |
| `usage_pattern_accumulator.go` | 渠道推荐用的用量画像累积 |

### 2.4 探测与自动发现

| 文件 | 职责 |
|---|---|
| `probe_worker.go` | L2 主动探测 worker（优先级队列、每日预算） |
| `verify_endpoint.go` | endpoint 保活/协议探测请求构造 |
| `auto_discovery.go` | `AutoDiscoveryRunner`：拉 `/v1/models`、写模型清单 |
| `auto_discovery_routes.go` | 发现结果到 channel 配置的路由应用 |
| `discovery_task_store.go` | 发现任务 checkpoint 持久化与断点续传 |
| `protocol_discovery.go` | 协议探测（messages/chat/responses 等同 URL 的协议联邦） |
| `provider_quality_probe.go` | L3 固定 canary 的 provider 质量探测 |
| `provider_quality_protocol.go` | provider quality 协议/评分维度 |
| `provider_quality_scoring.go` | provider quality 分数合成 |

### 2.5 限速学习

| 文件 | 职责 |
|---|---|
| `rate_limit_discovery.go` | `RateLimitDiscoverer`：header/429/TTFB 信号学习 RPM、MaxConcurrent |
| `rate_limit_applier.go` | `RateLimitApplier`：把学习结果写入 `ratelimit.Manager` |
| `response_header_timeout.go` | 基于 TTFB 画像的自适应响应头超时建议 |

### 2.6 模型解析与自动映射

| 文件 | 职责 |
|---|---|
| `model_resolver.go` | `ModelResolver`：手动映射 → 能力下界过滤 → 模型×effort 排序（8 档规范轴） |
| `model_profile.go` | `ModelProfile`、质量档（benchmark 优先 + 动态边界，见 §5.6）、能力字段、`EffortLevel` 8 档规范枚举 |
| `model_profile_store.go` | `(channel, kind, metricsKey, model)` 维度的模型画像存储 |
| `capability_floor.go` | `CapabilityFloor` 与 `MinQualityTierReasons` |
| `model_frontier.go` | `ComputeFrontierForest`：Pareto 分层与微簇；同成本 tie-break 补模型名/effort/CandidateID |
| `model_frontier_scoring.go` | 质量-成本点合成、三倾向车道选择（车道加权质量分与动态溢价帽，见 §5.10） |
| `benchmark_report.go` | 渠道无关的 benchmark 场景选型报告生成与渲染（供 benchmark-report CLI，见 §2.13 与 §5.7） |
| `model_routing_policy.go` | 模型替换意图策略 |
| `origin_tiebreaker.go` | 渠道来源信任等级兜底 |

### 2.7 智能路由

| 文件 | 职责 |
|---|---|
| `smart_router.go` | `SmartRouter`：渠道级候选收集、评分、硬约束过滤、意图提升 |
| `scoring.go` | 九项评分公式、权重不变量校验 |
| `endpoint_policy.go` | `EndpointAttemptPolicy`：URL/Key 级排序、模型映射、FastDecay 过滤 |
| `route_target.go` | `ResolvedRouteTarget`：原子 model+effort 决策结果 |
| `routing_trace.go` | `RoutingDecisionTrace`、`TraceStore`：路由决策追踪与持久化 |
| `routing_readiness.go` | 路由结果窗口聚合与 SLO 统计 |
| `local_candidate_provider.go` | 本地 runtime 候选收集 |
| `local_model_runtime.go` | 本地 runtime 画像 |
| `recommendation.go` | 基于用量画像的渠道域推荐 |
| `profile_coverage.go` | 画像覆盖率诊断 |

### 2.8 自学习记忆

| 文件 | 职责 |
|---|---|
| `context_limit_memory.go` | 读取共享兼容性记忆中“渠道-模型”实测上下文下限 |
| `document_capability_memory.go` | 读取共享兼容性记忆中“渠道-模型”document 不支持结论 |

### 2.9 人工意图与 Advisor

| 文件 | 职责 |
|---|---|
| `manual_routing_intent.go` | `ManualRoutingIntent` 类型、校验、生命周期 |
| `manual_intent_store.go` | 意图 SQLite 存储 |
| `intent_matcher.go` | `MatchIntent`：请求与活跃意图匹配 |
| `trusted_routing_advisor.go` | `TrustedRoutingAdvisor`：启发式 hint 生成 |
| `advisor_hint_policy.go` | `ResolveAdvisorHintEffect`：hint 到硬约束/本地候选的转换 |
| `advisor_decision_store.go` | advisor 决策记录 |

### 2.10 A/B 测试与发布

| 文件 | 职责 |
|---|---|
| `ab_test_sampler.go` | A/B 测试采样器 |
| `ab_test_store.go` | A/B 测试结果存储 |

### 2.11 订阅与自动托管

| 文件 | 职责 |
|---|---|
| `subscription_profile.go` | `SubscriptionProfile`、new-api 账号/Key |
| `subscription_store.go` | 订阅画像存储 |
| `subscription_capability.go` | 订阅级共享能力与 drift 检测 |
| `subscription_refresh_worker.go` | 订阅余额自动刷新 |
| `subscription_balance_fetcher.go` | 多提供商余额拉取接口 |
| `newapi_adapter.go` | new-api 面板适配（校验、余额、分组、建 key；876eaf7e 起校验/provision/追加账号均支持透传 `proxyUrl`/`proxyPreferDirect` 出站代理） |
| `newapi_subscription_sync_service.go` | new-api 订阅倍率/Key 同步与对账 |
| `newapi_group_guard.go` | new-api 分组并发保护 |
| `handlers_newapi.go` | new-api 订阅 provision 接口 |
| `handlers_subscription.go` | 订阅 CRUD 接口 |
| `handlers_subscription_accounts.go` | new-api 账号管理接口 |
| `handlers_auto_managed.go` | 通用自动托管渠道的账号/凭证/发现接口（最大文件，2994 行） |
| `handlers_billing_terms.go` | 订阅计费条款接口 |
| `handlers_exchange_rates.go` | 多跳汇率接口 |
| `handlers_key_multiplier.go` | Key 倍率管理接口 |
| `custom_managed_routes.go` | 自定义托管路由常量 |
| `deepseek_account.go` | DeepSeek 额度抓取 |
| `kimi_console.go` | Kimi 控制台额度/用量抓取 |
| `minimax_token_plan.go` | MiniMax token plan 用量 |
| `mimo_console.go` | MiMo 控制台套餐抓取 |
| `compshare_console.go` | Compshare 优云智算套餐抓取 |
| `volcengine_coding_plan.go` | 火山方舟 coding plan 额度；158d9c12 起另提供 `FetchVolcenginePlanModelsForChannel`：从管控面 AK/SK 签名接口拉取套餐模型清单，供火山套餐渠道的 `GetChannelModels` 使用 |

### 2.12 HTTP 接口

| 文件 | 职责 |
|---|---|
| `handlers.go` | 健康中心 `/health-center/*` 只读 API |
| `handlers_dryrun.go` | SmartRouter dry-run 诊断接口 |
| `handlers_cockpit.go` | 驾驶舱聚合接口 |
| `handlers_manual_intent.go` | 人工意图接口 |
| `handlers_advisor.go` | advisor 决策查询接口 |
| `handlers_profile_coverage.go` | 画像覆盖率接口 |
| `handlers_recommendations.go` | 推荐接口 |
| `handlers_routing_config.go` | 路由配置接口 |
| `handlers_local_runtime.go` | 本地 runtime 接口 |
| `handlers_task_template.go` | 本地任务模板接口 |
| `handlers_trace.go` | Trace 查询接口 |
| `handlers_events.go` | 事件订阅接口 |
| `handlers_provider_quality.go` | provider quality 接口 |

### 2.13 命令行工具（`backend-go/cmd/`）

| 工具 | 用途 |
|---|---|
| `benchmark-report` | 独立的渠道无关 benchmark 场景选型报告 CLI，不依赖运行中的后端（详见 §5.7） |
| `cc-sim` | 请求模拟 |
| `stream_verify` | 流式响应验证 |

## 3. 核心结构体

### 3.1 请求画像 `RequestProfile`

`RequestProfile`（`request_profile.go:7`）是路由决策的输入载体。

| 字段 | 含义 |
|---|---|
| `Model` | 请求目标模型 |
| `ChannelKind` | 入口协议：`messages/chat/responses/gemini/images/vectors` |
| `Operation` | `completion/count_tokens/image_generation/embedding` 等 |
| `AgentRole/AgentType` | `main/subagent`、codex/claude_code 等 |
| `HasImage/HasDocument` | 是否含图片/文档附件 |
| `EstTokens` | 字符级估算输入 token 保守上界 |
| `Complexity` | `TaskComplexity`（trivial/routine/complex/unknown） |
| `QualityNeed/QualityTarget` | 模型本身档、结合任务难度后的目标档 |
| `ContextNeed` | 估算输入 token 数，作为上下文硬约束 |
| `VisionNeed/DocumentNeed/ImageGenNeed/EmbeddingNeed/ToolUseNeed/ReasoningNeed` | 能力硬约束 |
| `ClientEffort/ClientEffortExplicit` | 客户端显式声明的思考档位（`off/minimal/low/medium/high/xhigh/max/ultra`） |
| `TaskClass/TaskDomain` | 分类与域推导结果 |
| `SessionID/PromptHash` | 意图/session 匹配、确定性流量哈希 |
| `IntentEffortPin` | 与 `EndpointPolicy` 共享的 effort 覆盖指针 |
| `AFPProfile` | 火山 Agent Plan 成本扩展 |

`ClassifierInput`（`request_profile.go:52`）是 `RequestProfile` 的脱敏子集，用于确定性分类。

### 3.2 能力下界 `CapabilityFloor`

`CapabilityFloor`（`model_resolver.go:19`）定义模型替换的最低能力要求。

| 字段 | 含义 |
|---|---|
| `MinContextTokens` | 最低上下文窗口 |
| `NeedsReasoning/Vision/Document/ToolCalls` | 能力布尔硬约束 |
| `MinQualityTier` | 最低质量档 |
| `QualityBenefitCap` | 简单/常规任务的质量收益软上限 |
| `TaskClass/TaskDomain` | 任务类别/域 |
| `EffortFloor` | 最低思考档位 |
| `PinnedEffort` | 手动意图或客户端锁定的档位 |

### 3.3 端点画像 `KeyEndpointProfile`

`KeyEndpointProfile`（`key_endpoint_profile.go`）是渠道-端点-Key 维度的运行时画像。

| 字段组 | 关键字段 |
|---|---|
| 身份 | `EndpointUID/ChannelUID/ChannelID/ChannelKind/BaseURL/KeyMask/KeyHash/MetricsKey/ServiceType` |
| 来源 | `OriginType/OriginTier/AccountUID/CredentialUID` |
| 健康 | `HealthState/HealthConfidence/HealthEvidence/SuggestedAction/ConsecutiveFail/LastSuccessAt/LastFailureAt` |
| 质量 | `QualityTier/StabilityTier/SpeedTier/CostTier/EffectiveStabilityTier` |
| 能力 | `SupportsVision/ToolCalls/Reasoning/LongCtx/AvailableModels/ProtocolModels` |
| 限速 | `DiscoveredRPM/DiscoveredMaxConcurrent/RateLimitSource/RateLimitConfidence/SuggestedRPMTPM/RPD` |
| 探测 | `ProbeSuccess/LastProbeAt/ProbeLatencyMs/ProbeConfidence/ConsecutiveProbeSuccess` |
| 延迟 | `P95LatencyMs/P95ConnectLatencyMs/P95FirstByteLatencyMs` 及样本数 |
| 成本 | `CostProfile/EffectiveCostMultiplier` |
| 订阅 | `UsageWindows/InheritedFromSubscription/MiniMaxTokenPlanUsage` |

`StabilityTier` 推导注意样本下限：`DeriveStabilityTier`（`profiler.go:161`）在 `stats.RequestCount < 5` 时直接返回 `StabilityTierNormal`（避免新渠道因样本不足被误判 Unstable 而评分归零）；有样本时按 `classifyStabilityByRates`：stable 需 ≥95% 成功率且 429 率 <5%，normal 需 ≥80% 且 429 率 <20%。

### 3.4 渠道聚合画像 `ChannelProfile`

`ChannelProfile`（`channel_profile.go:21`）由多 endpoint 聚合而成。

- `HealthState`：取最差但保守降级（mixed 场景降到 degraded）
- `QualityTier`：取最佳
- `StabilityTier/SpeedTier`：取中位数
- `CostTier`：取最便宜
- 能力标签取并集
- `EndpointInconsistencies`：同渠道 endpoint 能力不一致警告

### 3.5 模型画像 `ModelProfile`

`ModelProfile`（`model_profile.go:583`）锚定 `(ChannelUID, ChannelKind, MetricsKey, ModelID)`。

- `ModelFamily/QualityTier/SpeedTier/ContextTokens`
- `QualityTier` 不再单纯按模型族推导：`ModelProfileQualityTier`（`model_profile.go:562`）优先按 benchmark 归一化能力分评定，无 benchmark 证据才回退 `ModelProfileQualityTierFromFamily`（模型族推导，见 §5.6 的动态边界算法）
- 能力：`SupportsVision/Document/ToolCalls/Reasoning`
- `ProviderQualityScore/Confidence/Source/ProbeVersion`
- `TaskDomainStrengths`
- `SupportsEffortControl/SupportedEffortLevels`
- `ProbeSuccess/LastProbeAt/ProbeLatencyMs`

### 3.6 路由决策结果 `ResolvedRouteTarget`

`ResolvedRouteTarget`（`route_target.go:5`）是模型解析的最终输出。

| 字段 | 含义 |
|---|---|
| `Model` | 上游实际发送的模型 |
| `Effort` | 目标思考档位 |
| `EffortDecided` | Autopilot 是否真正决定了 effort（true）还是透传（false） |
| `Reason` | 决策原因 |

### 3.7 评分相关

`ScoringWeights`（`scoring.go:11`）：
- `WQuality/WStability/WSpeed/WCost/WSavings/WTierMatch/WFamily/WProviderQuality/WDomain`

`ScoringCandidate` / `ScoredCandidate`（`scoring.go:119/170`）：
- 输入候选的各维度分 + 输出总分、分项明细
- `ScoringCandidate` 另含 `QualityBenchmarkKnown/QualityBenchmarkScore`：仅当候选最终判定为 premium 且有实测 benchmark 分时由 `applyPremiumBenchmarkEvidence`（`smart_router.go:1729`）填充，供同档内 tie-break，不跨档、不影响 premium 之外的排序

### 3.8 Trace

`RoutingDecisionTrace`（`routing_trace.go:88`）记录一次完整路由决策。

- `TraceUID/SchemaVersion/RequestCorrelationId`
- `TaskClass/TaskDomain/RequestedModel/AgentRole`
- `Candidates`（含 `CandidateKey`（`channelUID|model`，见 §5.8）、`CandidateScore` 明细、`FilterReasons`、`DomainEvidence`、AFP 成本）
- `GlobalFilterReasons/SortReasons`
- `SelectedChannelUID/SelectedMetricsKey/SelectedOriginTier`
- `FallbackUsed/ShadowChannelUID/ActualChannelUID`
- `SchedulerDecision`：类型为 `SchedulerDecisionSummary`（`trace_contract.go:133`），含被滤渠道明细 `SkippedCandidates`（ChannelIndex/ChannelName/Stage/Reason/Details，见 §5.9）
- `EndpointAttempts/AttemptsTotal/AttemptsByResult`

### 3.9 限速学习

`RateLimitSignal`（`rate_limit_discovery.go:46`）：
- `Source`：`header/429/success`
- `Limit/Remaining/ResetSeconds/WindowSeconds`
- `HasRetryAfter/RetryAfterSeconds`
- `IsStreaming/LatencyMs`

`endpointLearnState`（`rate_limit_discovery.go:84`）：
- `EstimatedRPM/TPM/RPD/MaxConcurrent`
- `LatencyBaselineMs/LatencySampleCount`
- `ConsecutiveSlowLatency/ConsecutiveHealthyLatency`
- `LastConcurrencyAdjustmentAt/lastConcurrencyReason`
- `No429Since/ConsecutiveSuccessesSince429`

`SuggestedLimitResult`（`rate_limit_discovery.go:228`）：
- `RPM/TPM/RPD/MaxConcurrent/Confidence/ConcurrentConfidence/Source`

### 3.10 人工意图

`ManualRoutingIntent`（`manual_routing_intent.go:75`）：
- `IntentType`：`model_trial/channel_trial/endpoint_trial/session_pin`
- `ChannelKind/ChannelUID/MetricsKey/Model/MappedModel/Effort`
- `AgentRoles/TaskClasses/SessionID/TrafficPercent`
- `ExpiresAt/MaxRequests/MaxEstimatedCost`
- `FallbackOnFailure/RequireHardConstraints`
- `TrialResult`

### 3.11 成本证据

`CostEvidence`（`afp_cost_evidence.go:35`）：
- `Unit`：`usd/afp/compshare`
- `ScopeID`：AFP 必须同 scope 才可比
- `Estimated/Actual/Confidence/Source`

`AFPRequestProfile`（`afp_cost_evidence.go:102`）：
- `PricingSnapshot`（输入/输出 token 估算）
- `EstOutputTokens`
- `AgentPlanScope`

### 3.12 探测

`ProbeResult` / `ProbeRequest` / `ProbeQueue` / `ProbeBudget` / `ProbeWorker`（`probe_worker.go`）：
- 优先级队列：`dead > degraded > unknown > low`
- 每日预算、冷却期、连续成功恢复阈值

## 4. 关键流程

### 4.1 请求进入 → 画像构造

1. 协议层 handler 提取脱敏特征：`RequestProfileFeatures`（`request_profile_builder.go:5`）
2. `BuildRequestProfile`：
   - 合并 `ContextNeed` 与 `EstTokens`
   - 根据模型族推导 `QualityNeed`
   - 调用 `ClassifyAndFill` 得到 `TaskClass` 与 `TaskDomain`
   - `ResolveQualityTarget` 收敛为目标档
   - 归一化客户端 effort
   - 初始化空的 `IntentEffortPin`
3. `RequestProfile` 通过 context 传递，供 `SmartRouter` 与 `EndpointPolicy` 共享

### 4.2 任务分类

`Classify`（`task_classifier.go:27`）优先级：

1. `images` / `ImageGenNeed` → `image_generation`
2. `vectors` / `EmbeddingNeed` → `embedding`
3. `HasImage && VisionNeed` → `vision`
4. `ContextNeed > 200_000` → `long_context`
5. `Complexity == complex && AgentRole != subagent` → `supervisor`
6. 轻任务白名单 → `lightweight`
7. `subagent` 或 `routine` → `worker`
8. `main` 或未知 → `supervisor`
9. 兜底 → `worker`

### 4.3 域推导

`InferTaskDomain`（`task_domain.go:112`）优先级：

1. 显式 `X-Task-Domain` header
2. system prompt 关键词
3. 工具集特征（只读工具 + diff → `code_review`）
4. 文件扩展名（`.vue/.css/...` → `aesthetics_ui`）
5. 回退 `general`

### 4.4 调度器集成：渠道级过滤

入口：`main.go:791` 通过 `channelScheduler.SetCandidateFilterProvider` 注册。

```
Scheduler 选渠链路：
ContextFilter → CandidateFilter(SmartRouter) → X-Channel/ManualOverride/Promotion → SmartFilter → PrioritySort
```

`SmartRouter.CandidateFilterForWithActual` → `candidateFilterFor` → `executeFilter`

`executeFilter` 流程（`smart_router.go:591`）：

1. 构建 `RoutingDecisionTrace`
2. 遍历 scheduler 传入的候选渠道
   - 三个预过滤跳过点（missing_upstream/disabled_channel/candidate_unavailable）不再静默，写入 `trace.GlobalFilterReasons["candidate_pre_filter"]`
   - 联邦路由：`federatedRoute(ch, profile.ChannelKind)`；`ch.ActualModel != ""` 时协议联邦短路为单候选行（MappingSource="protocol_federation"）
   - 解析模型映射：`resolveChannelModels`（复数）——候选粒度为 **(渠道, 模型)** 行，单渠道上限 `routingCandidateFanoutLimit = 8`（见 §5.8）
   - 每行独立构建 `channelScoreEntry` 并独立做硬约束判定；`applyModelQualityTier`（同名承接行 `MappedModel` 保持空，防映射质量档折算误判）
3. 应用 AFP 成本（若启用）
4. 归一化 `SavingsScore`：cost/savings 按 `CandidateKey`（`channelUID|model`）归一化，空则回退 ChannelUID
5. `ScoreCandidate` 对每个候选行评分
6. **Advisor hint**：
   - 仅 `lightweight/worker` 允许生效
   - `MinQualityTier` 作为硬约束
   - `AllowLocalCandidate` 注入本地 runtime 候选
7. **人工意图匹配**：
   - `MatchIntent` 按 channel/model/taskClass/agentRole/session/traffic 匹配
   - supervisor 保护：third-party 渠道的 `model_trial` 不覆盖 supervisor
   - 命中后提升到首位
8. 硬约束过滤 + fail-open
   - 全部过滤后回退到重排
   - 结果按路由键做**渠道级去重**（`seenRouteKeys`）：同渠道多模型行只贡献一个 failover 槽位，取首个（最高分）存活模型行
9. 持久化 trace
10. 返回排序后的 `[]scheduler.ChannelInfo`

### 4.5 模型解析与 effort 决策

`ModelResolver.ResolveModel`（`model_resolver.go:125`）：

1. **显式 modelMapping** 优先（非自动托管渠道）
2. 无 `ModelProfileStore` → fail-open
3. 查询 `ModelProfileStore.GetModelProfiles(channelUID, kind, metricsKey)`
4. 能力过滤：
   - 上下文、reasoning、vision、document、toolCalls 硬约束
   - 质量档首选，更高质量不存在时允许降档
5. 精确/等价模型命中 → 直接返回，并应用 effort 决策
6. 不可替换意图 → 返回原模型
7. 否则 `rankEligibleModels`

`rankEligibleModels`：

1. `buildRankedCandidates` 展开 `model × effort`
2. **Frontier 选型**（默认启用，先保留所有满足硬能力的候选）：
   - 按连续 benchmark、置信区间与可比成本生成动态 `F0...Fn`，簇数量由自然断点决定（相邻点断点阈值钳制在 `[minClusterGap 0.08, maxClusterGap 0.12]`，按模型去重后估计，见 `model_frontier.go:228-252`），不设固定上限
   - `QualityBenefitCap` 只把兼容性的 `low/normal/high/premium` 请求目标投影到当前 `F0...Fn`，不写回模型永久等级
3. 成本证据不足时 fail-open，才应用固定 `QualityTier` 分带并进入旧字典序链（`betterRankedModel`，`model_resolver.go:966`）：
   - qualityRank → sameFamily → provider quality priority（双方可比时）→ 【cost_first 车道先比 cost】→ benchmark（`compareModelBenchmark`）→ version → measuredQuality → 【balanced 车道比 cost】→ latency → 【quality_first 车道比 cost】→ anti-effort-inflation → model ID
   - benchmark 比较已提前于版本比较；成本比较位置随车道后移/前移

`resolveEffortVariants`（`model_resolver.go:420`）：

- 手动意图锁定 effort 优先
- 全局 `ReasoningEffort.Enabled` 门控
- `PerTaskClass` 配置与模型支持档位的交集
- `ExpandVariants` 控制是否展开多档

### 4.6 Endpoint 级策略

`EndpointPolicy` 由 `main.go:832` 的 hook 注入 `handlers/common`。

`BuildEndpointPolicy`（`endpoint_policy.go:267`）：

- `dry_run` → `buildShadowPolicy`：只记录 trace，不修改排序
- 否则 `buildActivePolicy`：过滤+排序

`EndpointAttemptPolicy` 四步插入点：

1. `FilterURLs`：URL 不硬过滤，统一在 binding 层过滤
2. `SortURLs`：按 endpoint 评分排序
3. `FilterKeys`：FastDecay 低于阈值过滤
4. `SortKeys`：按 endpoint 评分排序
5. `FilterKeyBindings`：模型兼容性 + 健康/衰减硬过滤
6. `SortKeyBindings`：评分排序

评分链路（`endpoint_policy.go:504`）：

```
health(40) > fastDecay(25) > successRate(20) > latency(10) > cost(5)
```

乘以健康惩罚乘数：`dead=0.05/limited=0.30/misconfigured=0.40/degraded=0.70`

### 4.7 健康诊断与画像刷新

`Manager.runWorker`（`manager.go:843`）每 5 分钟执行 `collectAll`（`manager.go:892`）：

1. `refreshEndpointInventory`：刷新当前配置可达 endpoint 清单
2. `buildEndpointLimiterMappings` + `RateLimitApplier.Apply`
3. 对每个渠道每个 key：
   - `Profiler.DeriveEndpointProfile`：从 metrics 生成画像
   - carry-forward：发现字段、探测字段、连接/TTFB 统计
   - `HealthAnalyzer.Diagnose`：状态诊断
   - `RateLimitDiscoverer.SuggestedLimit`：写入限速建议
   - `QualityTrendDetector.DetectTrend`：质量趋势
   - `GroupChangeDetector.CheckGroupChange`：模型清单分组变更
   - `UsageMeter.ComputeWindows`：用量窗口
   - `applyStabilityHysteresis`：稳定性滞后
   - `emitProfileChangeEvents`：变更事件
   - `ProfileStore.Upsert`
4. `ProfileStore.Flush`
5. `updateSubscriptionCapabilities`：订阅级能力推导与 drift 检测
6. SLO 回滚检查

### 4.8 失败后学习/熔断/重试

#### FastDecay
- `FastDecayScorer.RecordResult(endpointUID, success)`（`main.go:857` hook）
- 普通失败：`DecayFactor = pow(0.85, consecutiveFail)`
- 断流：`pow(0.70, consecutiveFail)`
- 成功：`+0.15` 恢复
- 仅对 `PoolTagTemp` 生效

#### 熔断
- `metrics.MetricsManager` 维护 per-key 熔断器
- `HealthAnalyzer` 读取 `CircuitBreakerOpen` 作为诊断信号
- `EndpointPolicy.FilterKeys` 中 FastDecay 分数 `< 0.15` 过滤

#### 限速学习
- `Manager.ObserveRateLimitSignal` 读取响应头/429/TTFB
- `RateLimitDiscoverer.Observe` 更新 `endpointLearnState`
- header 显式值优先 → 429 反推 RPM/并发折半 → 成功 AIMD 上调
- TTFB 拥塞：连续显著慢于基线降低 `MaxConcurrent`
- `RateLimitApplier.Apply` 写入 `ratelimit.Manager`
- 显式 RPM/MaxConcurrent 配置永远优先

#### document 失败学习
- 上游 400/422 且文案含 document/pdf/input_file 时
- `handlers/common` 写入 `config.SharedChannelCompatCache`
- `SmartRouter.buildChannelEntry` 通过 `learnedDocumentUnsupported` 读取
- `routingHardConstraintReasons` 增加 `document_unsupported`

#### 上下文失败学习
- 类似 document，写入共享兼容性记忆
- `learnedContextLimit` 返回保守最小值

#### 输出上限失败学习
- 见 §5.16（`dd57a6d7`）：400/422 自报输出上限 → 渠道-Key-模型 级记忆 + 发送前钳制 + 同 Key 钳制重试

### 4.9 重试

- 调度器原生 failover 逻辑在 `handlers/common`
- `EndpointPolicy` 提供的 `FilterKeyBindings` 在重试前已过滤 dead/misconfigured 和不兼容模型
- `ResolvedTargetForBinding` 按最终 channel+URL+Key 解析 model+effort
- `ResponseHeaderTimeoutForEndpoint` 对轻任务可缩短响应头超时

## 5. 最近新增功能

### 5.1 流式 TTFB 拥塞学习与自适应并发

提交：`77bed070`

- 新增 `observeStreamingLatency`：按流式响应头 TTFB 建立平滑基线
- 连续显著变慢（`> 3` 倍基线或 > 15s）触发 `reduceConcurrentOnCongestion`
- 429 同步触发并发折半，与 RPM AIMD 解耦
- `ratelimit.Manager` 支持动态 `activeRequests` 计数，运行中安全升降并发上限
- `RateLimitApplier` 分维度应用 RPM/MaxConcurrent，显式配置独立优先
- `manager.go` 信号变化即时触发 `RequestRateLimitApply`

### 5.2 document 失败学习

提交：`ddf5b657`

- `document_capability_memory.go`：读取 `config.SharedChannelCompatCache`
- 强/弱信号分类器识别 document 不支持错误
- failover 只记录不重试
- `SmartRouter` 增加 `document_unsupported` 硬约束

### 5.3 通用自动托管迁移

提交：`69bc0535`

- 为 baseURL 与 key 齐全的历史渠道补齐 `generic` 托管身份
- 编辑渠道支持填写 new-api token 并绑定套餐
- 暴露订阅关联与 `autoManagedKind`

### 5.4 DocumentNeed 硬约束

提交：`02d1d178`

- `RequestProfile` 增加 `HasDocument/DocumentNeed`
- `CapabilityFloor.NeedsDocument` 与 `ModelProfile.SupportsDocument` 对齐
- `smart_router.go` 增加 `document_unsupported` 硬约束
- registry 增加 document 能力字段

### 5.5 模型 × effort 联合路由

提交：`afd94b8e`、`0a1a5c30`

- `ModelResolver.rankEligibleModels` 默认走 `ComputeFrontierForest`
- `buildRankedCandidates` 展开 `model × effort` 候选；规范轴统一为 8 档：`off/minimal/low/medium/high/xhigh/max/ultra`
- `xhigh/max/ultra` 不再压平，按厂商投入/成本序递增：`ultra > max > xhigh`
- 质量分以 benchmark 为主锚，带置信区间；`task_domain.go:effortBonusTable` 体现效果序（非投入序）：
  - `off=0, minimal=+0.2, low=+0.4, medium=+0.6, high=+0.9, xhigh=+1.0, max=+0.95, ultra=+0.9`
  - 雷达站 `gpt-5.6-sol` 实测：xhigh 0.75 > max 0.714 > ultra 0.693，xhigh 为效果最优档
- 成本轴乘 effort 成本系数防止档位膨胀；系数随投入递增，但可被实测 cost 校准（见 §5.5.1）
- 三车道（车道参数详见 §5.10）：
  - `balanced`：膝点 + 每 0.01 质量增益的成本溢价帽（基础 2%，按 TaskClass 质量权重动态缩放 0.9~1.2 倍）
  - `cost_first`：最低成本簇（溢价帽系数 1.0）
  - `quality_first`：并列池逐级决胜（benchmark 已知/未知不对称时已知者直接胜出 → benchmark 差 ≥`premiumFrontierBenchmarkMinDelta`(5.0) 直接兑现 → qualityRank → sameFamily 粘性（仅限 `frontierFamilyCostPremiumTolerance`=25% 溢价内）→ 成本兜底，见 `model_frontier_scoring.go:450-505`）
- 成本证据不足时 fail-open 回退旧链

#### 5.5.1 性价比 effort 选择

提交：`0a1a5c30`（接入 codexradar 实测 cost）；一致性门控见后续修复

- **全有或全无门控（前提）**：`frontierUseMeasuredCost` 仅当**所有**可比候选（公开价已知）都带有效实测 cost 时才返回 true，整批启用实测校准；任一候选缺失实测 cost 则整体回退推测系数 `frontierEffortCostFactor`。这保证同一 Pareto 成本轴绝不混用两种尺度——实测 cost 是"每任务实际 USD"（量级 ~$1），公开价是"1M 输入+1M 输出假想价 × effort 系数"（量级 ~$10+），若只对部分候选用实测，轴上量级差 ~5×，会破坏支配/聚类并反转 anti-effort-inflation 防护。语义与 `frontierUseProviderMultiplier` 的轴内一致性门控一致。
- **启用后**：`frontierCostFactorFor` 用候选 effort 的实测 USD cost 计算 `factor = measuredCostUSD / normalizedPublicCostUSD`，使成本轴值退化为实测 cost 本身
- 当 `benchmark.Profile.BenchmarkEvidence` 中存在该 effort 的有效 `CostUSD` 时参与校准；同一 effort 多条证据取最小 cost（保守估计），避免异常高成本扭曲 frontier
- **超注册表档位回退**：`measuredCostForEffort` 优先按候选 effort 精确命中实测 cost；未命中（如 codexradar 测了 ultra 但注册表该模型仅声明到 max）时回退该模型已测档位的最小成本作下界，避免校准静默失效
- 实测 cost 来源在 `CostEvidence.Source` 中标记为 `registry_pricing_x_measured_effort_cost`（或乘 provider 倍率后的变体），仅在门控启用时设置
- 效果：frontier 成本轴从"按档位系数猜测"演进为"以 codexradar 实测 cost 校准的性价比轴"，使 xhigh 等高效果档在成本相近时获得真实优势

### 5.6 QualityTier 按 benchmark 分数分布动态评定

提交：`231cbf5d`、`39027645`

- **归一化能力分** `normalizedCapabilityScoreWithEvidenceClass`（2026-08-24 起统一到常规 effort 口径）：直测证据只接受 domain=coding、metric=pass_at_1、benchmark∈{deepswe, codexradar}，且按 effort 档折算到 medium/default 口径——比率来自 deepswe 同模型 effort 曲线统计（low=0.686、high=1.413、xhigh=1.627、max=1.975）。证据按来源（deepswe/codexradar）分组后按证据等级分层合成：有常规口径实测直接用（跨源取最大，与旧合并语义一致）→ 无直测但曲线跨 medium（如 low+high）时按 effort 序数线性插值（模型自身曲线是中档水平的最好估计，源间取最小）→ 全部在一侧时跨源合并按全局比率折算取最小值（保守）；仅剩单一非常规档证据时 `singleEffortOnly=true`，档位封顶 high（折算完全依赖全局平均曲线，不足以支撑 premium）。分层升级只作用于缺 medium 直测的模型——实测档位评定从「全局平均比率的最坏折算」升级为「模型自身曲线插值」后，kimi-k3/glm-5.3 等跨档模型不再被系统性低估。动机：只存最佳档会把"开满思考强度"的成绩当模型基础能力（gpt-5.6-terra max=69.6 而 medium=35.1、luna max=67.2 而 medium=11.3）。为此 update 脚本的 deepswe/dradar 提取保留全部 effort 档 evidence（`selectionBasis=per_effort`）。无直测时用 artificial_analysis coding_index 线性校准：`deepswe ≈ 2.391 × aa_coding − 116.007`（系数来自重叠模型最小二乘拟合）。
- **边界算法** `computeQualityTierBoundariesFromRegistry`（`model_profile.go:491`）：默认边界 premium≥75 / high≥61 / normal≥55；注册表直测分数（按 CanonicalModel 去重）不足 4 个时用默认；否则排序后自顶向下找最大间隙中点——premium 边界取最高分下方 25% 量表区间（两端 ≥ 0.75×最高分）内的最大间隙中点，high 在低于 premium 的分段（floor=premiumMin×0.5）、normal 在低于 high 的分段（floor=highMin×0.4）依次找最大间隙。间隙两端都必须落在目标区域内（只查上端会让低段跨区域间隙被误当顶部断层）。2026-08-23 修复：原 60% 锚假设分布铺满量表，池分数集中在 40-77 时 60% 锚把中段空隙包进顶部区域，premiumMin 一度塌到 49 使 53 分模型全部升入 premium、选型翻转。
- **世代缓存**（`39027645`）：`benchmarkTierBoundariesCache`（`atomic.Pointer`，`model_profile.go:461`）以 `config.BuiltinSnapshotGeneration()` 为缓存键——内置快照每次重建发布时世代 +1，命中才免重算。热路径单次调用从 ~830µs/428 allocs 降至 ~15µs/8 allocs。
- **消费方**：`Profiler.DeriveQualityTier`（benchmark 优先 → lowQuality 降级）、`BuildRequestProfile` 的 QualityNeed、auto_discovery、provider_quality_probe、`SmartRouter.applyModelQualityTier`。

### 5.7 benchmark-report 独立 CLI

提交：`a4bf60be`、`2fb72449`、`3d58615a`、`a3429e8f`、`179c9580`

- 位置 `backend-go/cmd/benchmark-report/main.go`，用法 `benchmark-report -config <config.json>`（默认 `.config/config.json`）。不依赖运行中的后端：in-memory SQLite 作 ModelProfileStore + 合成渠道 `ch_synthetic_registry`。
- 候选池由 presetstore 默认快照的 `BenchmarkProfiles` 按 CanonicalModel 去重合成（`Source="synthetic_registry"`，不注入 upstreamCapabilities 的正则 patterns 防正则串泄漏），再用 `config.BuiltinUpstreamModelCapabilities` 补齐上下文/effort/能力布尔。
- 6 个固定场景（`benchmark_report.go:73`）：worker/balanced、supervisor/quality_first、lightweight/cost_first、long_context ctx≥200k、long_context ctx≥1M、worker/balanced coding。每场景跑一次与真实路由一致的 frontier 选型，失败回退 `betterRankedModel` 链并标注 `fallback:`。
- 输出：`[BenchmarkSelectionReport]` 每场景一行 + Top 5 ASCII 表（`# / model / effort / bench / cost / tier`）。无实测证据时 bench 列渲染 `-` 且选中行带 `bench_evidence=none`。
- Top 5 口径（`a3429e8f`，`scenarioCandidateOrder`）：选中项固定第 1，其余按 frontier 阶梯（Preferred+Overflow）排，阶梯未覆盖的按 `betterRankedModel` 排尾部，同模型去重，顺序与输入顺序无关。
- 「更优」判定 `findBetterOptions`：quality_first 仅质量差 ≥0.05 算更优；其他车道为「质量高 ≥0.05 且 cost ≤ 1.25×」或「省 >$0.01 且质量 ≥95%」；只含有实测分的模型，上限 5 条。
- 主程序不再输出该报告（`179c9580`）：manager/main 均无 `BenchmarkSelectionReport` 引用，避免 CLI 用正则抓输出被污染；数据刷新脚本 `scripts/update-benchmark-data.mjs` 的 `runBenchmarkReport()` 在刷新后现编译 CLI 并透传 stdout。

### 5.8 路由候选按 (渠道, 模型) 展开

提交：`78ed757f`

- 候选粒度从渠道级变为 (渠道, 模型) 级：`RoutingCandidate`/`RoutingPlanCandidate`/`channelScoreEntry` 新增 `CandidateKey = channelUID + "|" + normalizeRoutingModelID(model)`（`smart_router.go:1318`）；单渠道候选行上限 `routingCandidateFanoutLimit = 8`。
- AutoManaged 渠道走 `ResolveModelsAnyEndpointWithFloor`（能力过滤 + `betterRankedModel` 排序 + 截断 top8）；精确/等价命中只产该行；无精确命中且意图不允许替代（`AllowsSubstitution()` 为 false）→ 渠道不产候选行（保持 exact_model_required 不变量）。
- 显式/白名单渠道从 `ModelMapping` 值 + redirect 目标枚举，逐个过 `ExplainModelSupport`；枚举为空但单数版认为 supported 时回退单模型行（fail-open）。
- 同名承接（映射后模型名 == 请求模型名）的行 `MappedModel` 保持空——`applyModelQualityTier` 依赖 MappedModel 判空做映射质量档折算；模型名展示由前端经 CandidateKey 回退解析（`AutopilotTraceDetailDialog.vue` 加 Model 列）。
- 返回 scheduler 的结果仍按路由键渠道级去重：同渠道一个模型行通过即保留该渠道（取最高分行），不重复占用 failover 槽位。`RoutingPlan`/`BuildPlan`（dry-run 诊断面板）同样消费 CandidateKey。

### 5.9 scheduler 过滤明细接入路由 trace

提交：`16dc0fda`

- `SchedulerDecisionSummary.SkippedCandidates`（`trace_contract.go:136`）：ChannelIndex/ChannelName/Stage/Reason/Details，记录被调度器滤掉的渠道。
- `NormalizeSelectionTrace`（`trace_lifecycle.go:19`）从 `scheduler.SelectionTrace.Candidates` 填充；Reason 必须过 `isSafeSkipReason` 白名单（约 40 个代码枚举如 unsupported_model/circuit_open/context_window_insufficient/route_prefix_mismatch），白名单外整条丢弃。
- `AttachSchedulerDecision`（`trace_lifecycle.go:67`）：已落盘记录同步 UPDATE details_json；未落盘（1/10 抽样中）只更新内存，终态落盘自然携带。
- `ToTraceDetailV2` 补上映 `schedulerDecision` 字段（此前 DTO 有字段但从不映射，前端区块永不显示）；前端 `AutopilotTraceDetailDialog.vue` 渲染被滤渠道明细表。
- SmartRouter 侧三个候选预过滤跳过点（missing_upstream/disabled_channel/candidate_unavailable）写入 `trace.GlobalFilterReasons["candidate_pre_filter"]`。

### 5.10 SmartRouter 评分修复与车道参数化

提交：`455dc044`

- **动态溢价帽**：`maxCostPremiumPerQuality`（`model_frontier_scoring.go:259`）按车道取基础值 quality_first=6.0、balanced=2.0、cost_first=1.0；再按 TaskClass 权重微调：`ratio=(WQuality+1)/(WCost+1)` 钳制 [0.5,2.0]，乘数 0.9~1.2。
- **quality_first 并列池**改逐级决胜（`pickFrontierQualityFirstPoint`，`model_frontier_scoring.go:450`）：benchmark 证据已知/未知不对称时已知者直接胜出（1468fa08，防零证据候选凭档位先验+低成本挤掉强证据模型）→ benchmark 差 ≥5.0 直接兑现 → qualityRank → sameFamily 粘性（仅限 25% 成本溢价内，`13c70e1c`，防同族溢价无上限膨胀）→ 成本兜底；不再「并列取最低成本」。
- **质量分按车道加权** `frontierQualityWeights`：quality_first {Benchmark 0.6, TierPrior 0.25, Measured 0.15}、balanced {0.5, 0.30, 0.20}、cost_first {0.4, 0.35, 0.25}；benchmark 缺失时权重让位给 tierPrior+measured。
- **置信区间半宽随车道变化**：quality_first ×0.85、cost_first ×1.1；接近本批最高分（delta<5）再 ×0.75。
- **SmartRouter 与 ModelResolver 车道一致**：两处均改用 `GetEffectiveCostPreferenceMode(taskClass)`（PerTaskClass 优先于全局 Mode）；`GetEffectiveMultipliers`：quality_first (savings 0.3, providerQuality 1.5)、balanced (1,1)、cost_first (2.0, 0.5)。
- **稳定性评分偏差修复**：`DeriveStabilityTier` 样本 <5 时返回 Normal（见 §3.3），避免新渠道被误判 Unstable 打 0 分。
- **premium 档内 benchmark tie-break**：`applyPremiumBenchmarkEvidence` 仅对最终判定 premium 且 BenchmarkKnown 的候选填充 `QualityBenchmarkKnown/QualityBenchmarkScore` 供同档决胜。

### 5.11 effort-aware benchmark 分

提交：`5e1b4bd9`

- `effortAwareBenchmarkScore`（`model_resolver.go:794`）：`BenchmarkEvidence` 存在 domain=overall 且 effort 精确命中的实测 RawValue 时，用 `ratio = effortRaw / defaultRaw` 缩放 OverallScore（反映不同思考档位的真实智商差异）；effort 为空/default 或无 default 基准时直接返回 OverallScore（不惩罚缺数据）。
- `buildRankedCandidates` 用它填充每个 model×effort 候选的 `benchmarkScore/benchmarkKnown`；域证据同步按候选 effort 精确取（`ResolveDomainStrengthForEffort`），不再恒走 domain-only 回退。

### 5.12 Key 级模型黑名单在自动映射渠道生效

提交：`a1eb2743`

- 原缺陷：`DisableKeyModel` 以 autopilot 映射后的实际模型名写入黑名单，而 keypool 选 Key 阶段（`CandidatesForModelFiltered`）用映射前的请求模型名查询，自动映射渠道上持久化黑名单永不命中。
- 修复：发送前（实际发往上游的模型确定后）复查 `IsKeyModelDisabledNow(apiKey, actualAttemptModel, now)`（`handlers/common/upstream_failover.go:750` 附近），命中则跳过本 Key 走正常 failover；不重复计熔断、不写新黑名单。
- 两层关系：选 Key 阶段覆盖手动 RedirectModel 场景，autopilot 映射目标由发送前复查兜底。

### 5.13 key 验证兼容 new-api 占位模型 503

提交：`cd6f93d7`

- `verifyJSONPostEndpointWithPolicy`（`verify_endpoint.go:213`）的 `acceptValidationError` 分支除 400/422 外，新增识别 503 + `modelNotFoundErrorBody`（`error.code` equals-fold `model_not_found`，或 message 含 `no available channel for model`），判定鉴权通过（OK=true，「服务可达（探测模型无可用渠道，鉴权通过）」）。
- 背景：new-api/one-api 对无渠道占位模型返回 503，此前被误判「端点不可用」导致有效 key 无法添加。

### 5.14 兼容性 trait 自学习：unsupported_beta_header

提交：`205fe29d`

- 动机：Claude Code 2.x 携带的 `anthropic-beta: context-1m-2025-08-07` 等 beta header 透传到部分 new-api 风格上游会被 400 拒绝。
- 链路（跨模块）：`handlers/common/compat_signal.go` 检测请求带 `anthropic-beta` header 且上游拒绝 → `ExtractRejectedBetaTokens` 提取被拒 token → 以 trait `unsupported_beta_header`（`config/channel_compat_cache.go:44` 的 `CompatTrait` 枚举）写入 `UpstreamConfig.LearnedRejectedBetaTokens`（`config/config.go:87`，运行时字段不落盘）→ 下次请求 `upstream_failover.go:721` 注入 learnedTraits，`providers/claude.go:220` `stripUnsupportedBetaHeaderTokens` 按 token 粒度剥离（token 名必含 `-` 防误判）。
- 与 §2.8 的 context_limit / document_capability 记忆同属共享兼容性缓存体系，但 trait 化后由 config 层统一承载。

### 5.15 其他近期功能

- **new-api raw_auth 认证模式**：`2020fc4f`
- **有效 USD 成本链路**：`2755fd90`
- **倍率资格与多跳汇率**：`f39fc6fb`
- **new-api provision 同站点并入已有渠道**：`08fd5c7e`
- **同账号 Messages 跨协议调度**：`13ffd67b`
- **上下文上限自学习**：`ef458440`
- **手动意图支持锁定思考档位**：`00ff7454`
- **Kimi 额度快照以 `GetSubscriptionStats` 为主数据源**：`59b6aa02`（`GetUsages` 已从 Kimi Web 下线，仅当仍返回 `FEATURE_CODING` 时作可选增强补充 WeeklyUsage/TotalQuota/RateLimits；恢复判定用其 `CodeFiveHour/CodeSevenDay` 比例窗口，绑定令牌不再因缺 `FEATURE_CODING` 失败）
- **Kimi 控制台令牌解析兼容**：`87267d6f`（`normalizeKimiConsoleToken` 支持 Cookie `access_token=`、localStorage JSON、成对引号、URL 编码）

### 5.16 输出上限自学习与 max_tokens 钳制

提交：`dd57a6d7`（镜像 §5.15 的上下文上限自学习机制）

- **信号提取**：`OutputLimitFromError`（`handlers/common/output_limit_signal.go:101`）从上游 400/422 错误体（Anthropic/OpenAI 两种格式的正则）提取上游自报的输出 token 上限。
- **记忆**：`ChannelCompatCache.RecordOutputLimit`（`config/channel_compat_cache.go:485`）按「渠道-Key-模型」粒度记忆 `OutputLimitState`，24h TTL（`channelCompatTTL`）；下界 `minLearnableOutputLimit = 256`，低于此不采信（宁小勿大口径的防误判护栏）。
- **钳制**：`clampMaxTokensInBody`（`handlers/common/body_clamp.go:47`）按入口协议钳制 `max_tokens`/`max_completion_tokens`/`max_output_tokens` 三种字段；发送前在 `upstream_failover.go` 主动钳制，学到上限后同 Key 以钳制值立即重试一次，后续请求发送前直接钳制。
- 与 §2.8 的 context_limit / document_capability 记忆同属共享兼容性缓存体系，写入方在 `handlers/common`，autopilot 包本身不读取该状态。

### 5.17 基准图表的常规 effort 等效分与质量档色带

提交：`23baf19c`

- `scripts/benchmark-sources/visualization.mjs:15-21` 把 §5.6 的 effort 折算比率（low=0.686/high=1.413/xhigh=1.627/max=1.975）引入图表生成，散点图按自动质量档渲染 Low/Normal/High/Premium 色带；`scripts/generate-benchmark-chart.mjs` 增加 `qualityTiers` 有效性校验。
- 注意两侧口径的细微差异：scripts 版比率表含 `ultra` 键（=1.975），后端 `model_profile.go` 的 `effortQualityRatio` 无 `ultra`。

### 5.18 新增 key 验证的鉴权/非鉴权失败分类与降级放行

提交：`36387ca6`

- 动机：部分上游（如签到送额度的中转站）对占位模型 `probe` 的推理探测请求挂起直至 12s 超时，此前 `verifyChannelKey` 任一候选失败即整体 400，且不区分 401/403 与超时/网络/5xx，导致有效 key 无法添加。
- `verifyChannelKey` 改为任一候选 OK 即通过（对齐 `verifyProviderRouteKeys` 的首个命中语义）；全部失败时返回结构化 `KeyVerifyError{AuthFailed, Probe, Diagnostics}`——所有候选均 401/403 才标记 `AuthFailed=true`。
- `verifyAddedKeysForUpdates` / `verifyNewKeysForChannels` 返回 `(warnings, error)`：`AuthFailed=false` 的失败降级为 warning（key 正常保存，响应带 `warnings` 字段，保活 L1/L2 后续把关）；鉴权失败与未知错误仍硬阻断。auto-add 两条追加路径（`appendCredentialsToCustomAccount` / `appendCredentialsToLegacyChannels`）同步降级。
- `updateAccountRequest` 新增 `skipVerify`；账号更新类 400 错误经 `respondAccountUpdateError` 附加 `verifyFailure`/`authFailed` 标识，前端据此弹「仍要保存」确认框后带 `skipVerify` 重发。
- 探测方式随错误/警告消息透传（接口路径 + 占位模型 + max_tokens），解决验证过程不透明问题。

### 5.19 倍率编辑对托管账号手工 key 可用（credentialUid 兜底）

提交：`e205d9c6`（入口可见性修复见 `bb736eef`）

- 原缺陷：`KeyUID` 仅 new-api 订阅同步路径（`StableKeyUID`）生成，托管账号手工 key 只有 `CredentialUID`，导致 `findAPIKeyConfigByKeyUID` 无法定位、前端 `ApiKeyManagementSection` 的倍率编辑按钮又因嵌在「已设置倍率才渲染」的 subtitle 块内而无首次配置入口（鸡生蛋）。
- 修复：`findAPIKeyConfigByKeyUID` 兜底匹配 `CredentialUID`（托管渠道加载期 `ensureCredentialUIDs` 必回填）；前端 `buildChannelApiKeyRows` 以 `keyUid ?? credentialUid` 回填行 `keyUid`；倍率编辑按钮对可编辑 key（有 keyUid/channelUid/channelKind）始终可见，未设置倍率时不渲染空 chips。
- 效果：免费/签到类渠道的 key 可在编辑弹窗一键「标记为公开/临时 Key」（`groupMultiplier=0` 显式零成本 `manual_zero_cost` + `opportunistic` 消耗策略），成本报表归入「已确认零成本」，调度零成本优先。

## 6. 与其他模块的交互点

### 6.1 scheduler

| 交互 | 位置 |
|---|---|
| `SetCandidateFilterProvider` 注册 SmartRouter | `main.go:791` |
| `SetModelSupportResolverProvider` 注册 AutoManaged 模型支持解析 | `main.go:938` |
| `CandidateFilterFunc` 类型契约 | `internal/scheduler/scheduler.go:55` |
| `ChannelScheduler.channelAvailableForCandidateFilter` | `internal/scheduler/scheduler.go:674` |
| `buildSmartFilterFromProvider` 包装 SmartFilter | `internal/scheduler/scheduler.go:1535` |

### 6.2 providers

- 极少直接依赖：仅 `providers` 导入 1 次
- 主要通过 `config.UpstreamModelCapability` 消费注册表能力

### 6.3 session

- `session` 导入 1 次
- `RequestProfile.SessionID` 来自 handler 层 session 管理，用于 `session_pin` 意图匹配

### 6.4 keypool / ratelimit

- `keypool` 导入 2 次：scoped limiter 配置
- `ratelimit` 导入 3 次：`RateLimitApplier` 调用 `ratelimit.Manager` 的 `SetDiscoveredRPM/SetDiscoveredMaxConcurrent`

### 6.5 metrics

- `metrics` 导入 4 次
- `MetricsProvider` 接口在 `manager.go` 通过 `metricsManagerAdapter` / `metricsAdapterManager` 适配
- `Manager.collectSignals` 从 `MetricsManager` 取 1h/24h/15m 窗口统计与熔断快照

### 6.6 config

- 最大外部依赖：77 次导入
- 读取 `AutopilotRoutingConfig`、上游配置、模型注册表、兼容性缓存、AFP 配置

### 6.7 handlers/common

通过 hook 实现反向依赖：

| Hook | 位置 | 用途 |
|---|---|---|
| `SetEndpointPolicyProviderHook` | `main.go:832` | 注入 `EndpointAttemptPolicy` |
| `SetNotifyEndpointResultHook` | `main.go:857` | 通知 FastDecay |
| `SetRoutingOutcomeRecorderHook` | `main.go:816` | 记录请求终态到 Trace |
| `SetAttemptRecorderHook` | `main.go:822` | 记录每次 endpoint 尝试 |
| `SetUsagePatternRecorderHook` | `main.go:888` | 记录用量画像 |

### 6.8 兼容性记忆

- `context_limit_memory.go` / `document_capability_memory.go`
- 通过 `config.SharedChannelCompatCache()` 读取
- 写入方在 `internal/handlers/common`

## 7. 布局示意图

### 7.1 请求路由决策链

```text
[Client Request]
      │
      ▼
┌─────────────────┐
│ RequestProfile  │  Model, Kind, HasImage, HasDocument, EstTokens,
│   Builder       │  QualityNeed, ContextNeed, Effort(off..ultra), TaskClass, Domain
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│   SmartRouter   │  1. 收集候选渠道
│                 │  2. 解析模型映射
│                 │  3. 构建评分 entry
│                 │  4. AFP 成本应用
│                 │  5. ScoreCandidate
│                 │  6. Advisor hint (lightweight/worker)
│                 │  7. 人工意图匹配
│                 │  8. 硬约束过滤 + fail-open
│                 │  9. 持久化 trace
│                 │ 10. 返回排序渠道
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│    Scheduler    │  X-Channel → ManualOverride → Promotion →
│                 │  ProtocolFederation → SmartFilter →
│                 │  ModelCircuit → Trace 亲和 → PrioritySort → Fallback
└────────┬────────┘
         │
         ▼
┌─────────────────┐
│  EndpointPolicy │  FilterURLs → SortURLs → FilterKeys → SortKeys →
│                 │  FilterKeyBindings → SortKeyBindings
└────────┬────────┘
         │
         ▼
   [Upstream Call]
         │
         ▼
┌─────────────────┐
│  Trace Store    │  RoutingDecisionTrace v2
│ + Learning Loop │  FastDecay / RateLimit / Document / Context / OutputLimit
└─────────────────┘
```

### 7.2 模型解析与 Frontier 选型

```text
[ModelResolver.ResolveModel]
      │
      ├─ 显式 modelMapping? ──→ 直接返回
      │
      ├─ 无 ModelProfileStore? ──→ fail-open
      │
      ▼
[GetModelProfiles(channelUID, kind, metricsKey)]
      │
      ▼
[能力过滤 CapabilityFloor]
      ├─ MinContextTokens
      ├─ NeedsReasoning/Vision/Document/ToolCalls
      ├─ MinQualityTier (无更高档时可降档)
      └─ PinnedEffort
      │
      ▼
[精确/等价模型命中?]
      ├─ 是 ──→ 直接返回 + effort 决策
      └─ 否
      ▼
[rankEligibleModels]
      │
      ├─ buildRankedCandidates ──→ model × effort 展开
      │
      ▼
[Frontier 选型 ComputeFrontierForest]
      ├─ balanced: 膝点 + 动态溢价帽（基础 2%，按 TaskClass 缩放 0.9~1.2x）
      ├─ cost_first: 最低成本簇
      └─ quality_first: 并列池逐级决胜（benchmark 证据优先/qualityRank/同族粘性限溢价，成本兜底）
      │
      ▼
[成本证据不足?]
      ├─ 是 ──→ fail-open 回退旧字典序链
      └─ 否 ──→ 返回最优候选
```

### 7.3 限速学习状态机

```text
[RateLimitDiscoverer.Observe]
      │
      ├─ Header 显式值? ──→ 直接采用
      │
      ├─ 429? ──→ RPM/并发折半
      │     └─ No429Since 重置
      │
      ├─ 成功? ──→ AIMD 上调 RPM
      │     └─ ConsecutiveSuccessesSince429++
      │
      └─ TTFB 信号
            │
            ▼
      [observeStreamingLatency]
            │
            ├─ 建立 LatencyBaselineMs
            │
            ├─ 连续 >3× 基线或 >15s?
            │     └─ reduceConcurrentOnCongestion
            │
            └─ 连续健康?
                  └─ 逐步恢复并发
```

## 8. 边界与缺口

### 8.1 已知缺口

1. **用量画像的域推导缺信号**
   - `RecordUsagePattern` 注释说明：`channelKind/model` 未参与域推导，因为 `InferTaskDomain` 依赖 system prompt/工具集/文件扩展名，而代理层请求完成钩子上不可得
   - 文件：`manager.go:615/624`

2. **细粒度错误分类未逐条统计**
   - `collectSignals` 中 `AuthFailureCount/DNSFailureCount/QuotaFailureCount` 等多数为 0，仅 `OverloadedCount` 来自熔断器
   - 文件：`manager.go:1306`

3. **AFP 成本系数已替换为实测校准**
   - `frontierEffortCostFactor` 仍为 fallback 推测系数；`frontierUseMeasuredCost` 门控通过（所有可比候选均带实测 cost）时，`frontierCostFactorFor` 用 `measuredCostUSD / normalizedPublicCostUSD` 替换推测值，否则整体回退（轴内尺度一致，不混用）
   - 文件：`model_frontier_scoring.go:47`、`model_frontier_scoring.go:62`

### 8.2 边界与保守策略

1. **document 不支持学习**
   - 同渠道任一 key 已知拒绝即视为不支持
   - 原因：路由决策发生在选定具体 key 之前，保守规避

2. **上下文下限学习**
   - 同渠道-模型取所有已知 key 的最小上下文上限
   - 原因：不同 key 套餐窗口不同，宁可放过大窗口 key

3. **输出上限学习**
   - 渠道-Key-模型 粒度、24h TTL、下界 256 不采信（见 §5.16）
   - 学到后同 Key 以钳制值立即重试，后续请求发送前直接钳制

4. **质量档降档**
   - `filterByCapabilityFloorWithQualityFallback`：无更高档候选时才降档

5. **ChannelProfile 健康聚合**
   - 只要仍有 healthy/unknown endpoint，mixed 场景统一降到 `degraded`，不会直接判死

6. **Advisor hint 生效范围**
   - 仅 `lightweight/worker` 允许真实生效
   - `supervisor/vision/long_context` 受 `NeverDemoteTaskClasses` 保护

### 8.3 运行态安全

1. **panic 恢复**
   - `EndpointPolicy.SortURLs/SortKeys` 带 panic recover，异常时回退原列表

2. **Kill Switch**
   - `BuildEndpointPolicy` 在 kill switch 时返回 nil
   - `RateLimitApplier.Apply` 非 active 时清理所有发现限速

3. **RateLimitApplier 分维度独立**
   - 显式 RPM / 显式 MaxConcurrent 分别独立判断是否采用发现值

### 8.4 配置/向后兼容

1. **FrontierRoutingEnabled 废弃**
   - 仅 JSON 兼容保留，实际无条件启用

2. **ResolvedModelByEndpointUID 与 ResolvedTargetByEndpointUID 共存**
   - 旧 hook 读取 model，新 hook 读取完整 target，reason 字段兼容

3. **EndpointUID 兼容**
   - `scoreEndpointForKey` 中命中画像后改用 `profile.EndpointUID`，否则 handlers 层查询 key 与 modelByUID key 不一致

## 9. 待补充

- LogicalChannel 归组逻辑与 SmartRouter 候选收集的交互
- 多协议联邦路由的边界条件
- Trace 脱敏边界与隐私合规
- 本地 runtime 候选的画像完整性
