# CCX 渠道自动托管 (Channel Autopilot) 设计方案

## 1. 设计目标

> 用户添加渠道只需 baseURL + apiKey，系统自动完成协议发现、模型映射、能力画像、健康诊断、智能调度。
> 高级用户可覆盖任何自动决策，但默认不需要碰。

### 1.1 核心用户故事

| # | 作为… | 我希望… | 这样我就能… |
|---|-------|---------|------------|
| U1 | 有 30-40 个中转站的用户 | 添加渠道时只填 URL + Key | 不用手动配 modelMapping / supportedModels / 兼容开关 |
| U2 | 想用 Opus 监工 + 白嫖子代理的用户 | 主代理自动走高智商稳定渠道，子代理自动走便宜/快速渠道 | 不用手动给每个渠道调优先级 |
| U3 | 渠道会死/会限流的用户 | 一眼看到哪些渠道死了、限流了、配置错了 | 快速清理或修复，不用逐个 ping |
| U4 | 有临时薅羊毛渠道的用户 | 临时池优先消耗，用完自动切常规池 | 不浪费免费额度 |
| U5 | 同时使用官方 API/token plan、中转站、公益站的用户 | 渠道中心按信任等级展示，但实时质量独立评估 | 知道渠道隐私/治理风险，同时允许低信任上游在状态好时被选中 |
| U6 | 想临时试新模型/新公益站的用户 | 给模型或渠道设置短期试用意图 | 不改长期策略，也能让真实流量帮忙验证 |
| U7 | 本地有 Ollama/LM Studio/llama-server 的用户 | 用本地模型承担低风险任务和路由辅助 | 减少重要付费模型调用，同时不让弱模型误判强模型边界 |

### 1.2 设计原则

- **SOLID**：Analyzer、Profiler、ModelResolver、SmartRouter 职责单一，接口隔离
- **KISS**：先用可解释规则，不做复杂 AI 打分
- **DRY**：复用现有 MetricsManager、能力测试、模型注册表、CandidateFilter
- **YAGNI**：Phase 1 只做自动画像 + 健康诊断，Phase 2 做智能调度，Phase 3 做自愈
- **信任等级 ≠ 服务质量**：官方 API/token plan 是一等信任来源，中转站是二等，公益站是三等；等级表示隐私、治理和可控性，不代表实时服务质量
- **AI 路由判定只走可信执行面**：会读取用户内容的 AI 判定只能使用一等官方 API/token plan 或本地模型；中转站和公益站的信任等级不足以承担隐私敏感判定
- **人工意图优先但有边界**：用户可以短期试模型/渠道；系统必须限制 TTL、预算和作用范围，并保留硬约束与自动回退
- **本地模型只做有边界的辅助**：本地弱模型可以处理低风险任务和生成路由 hint，但不能作为“哪些任务只有 Fable 能做”的通用裁判
- **Images/Vectors 原生调度优先**：生图和 embedding 作为一等调度类型接入上游原生端点；MVP 不做 chat prompt → image API 的协议转换

---

## 2. 现有基础（可直接复用）

### 2.1 调度器筛选链

`backend-go/internal/scheduler/select.go` 中 `SelectChannelWithOptions` 已有完整筛选链：

```text
Active+Model过滤 → RoutePrefix过滤 → 上下文过滤 → CandidateFilter回调
→ Channel Pinning → Manual Override → Promotion优先 → Trace亲和
→ Priority遍历(含健康检查/熔断/限速/视觉保护) → Soft-skip回退 → Degraded兜底
```

**关键扩展点**：`SelectionOptions.CandidateFilter` 是注入式回调，autopilot 可在此注入标签/画像过滤逻辑。

### 2.2 指标系统

`backend-go/internal/metrics/` 提供：

| 能力 | 接口 | 说明 |
|------|------|------|
| 健康判断 | `IsChannelHealthyMultiURL` | 多 URL 聚合 |
| 失败率 | `CalculateChannelFailureRateMultiURL` | 滑动窗口 |
| 聚合指标 | `GetChannelAggregatedMetrics` | 15m 成功率/请求数/缓存率 |
| 熔断状态 | `GetChannelCircuitStateMultiURL` | Closed/Open/HalfOpen |
| 失败分类 | `FailureClass` | retryable/overloaded/non_retryable/quota |
| 请求日志 | `ChannelLog` | 含 AgentRole、模型、延迟、流健康 |

### 2.3 能力测试与模型发现

`backend-go/internal/handlers/channel_discovery.go` 已实现：

- 协议自动探测：对 messages/responses/chat/gemini 四协议并发探测
- 模型自动发现：拉 `/v1/models`，失败时用内置候选模型回退
- 模型映射推荐：根据探测结果自动生成 modelMapping
- 能力探测：工具调用、视觉、thinking passback 测试

`capability_test_runner.go` 提供完整的多模型轮询测试框架。

Images / vectors 需要单独处理：
- `/v1/images/*` 和 `/v1/embeddings` 进入 Autopilot 调度，但只做原生端点选择、健康画像、成本画像和限速保护。
- 不复用 chat/messages 的复杂能力测试；MVP 只做最小探测：鉴权、协议路径、模型列表/默认模型、一次小尺寸生图或短文本 embedding（受探测预算限制）。
- 不做 chat 画图协议转换，也不把“文本对话请求里让模型画图”的 prompt 自动改写为 images API 请求。
- vectors 的 embedding 维度、模型名和协议字段仍由现有 handler 硬约束过滤；Autopilot 只负责在满足硬约束后排序 endpoint。

### 2.4 模型注册表

`backend-go/internal/config/generated_model_registry.go` + `model_registry.go`：

- `ResolveUpstreamCapability`：四层解析（channel → global → builtin → default）
- `ResolveAgentModelProfile`：下游代理模型上下文窗口
- 覆盖 Claude/GPT/Gemini/DeepSeek/Kimi/GLM 等主流模型
- 每个模型有 ContextWindowTokens、MaxOutputTokens、Capabilities、Pricing

### 2.5 角色识别

`backend-go/internal/utils/headers.go` 的 `ExtractAgentContext`：

- Codex 子代理：`client_metadata.x-openai-subagent` 精确识别
- Claude Code 子代理：`X-Claude-Code-Agent-Id` header 精确识别
- 启发式识别：消息数 + 工具调用模式
- 已用于 trace 亲和隔离（`:subagent` 后缀）

## 3. 数据模型

### 3.1 粒度模型：三层架构

**⚠️ 关键设计决策：画像粒度是 `(baseURL, apiKey)` 对，不是 channel**

原因：
1. 同一 channel 的不同 baseURL 可能走不同 CDN，延迟/超时/截断行为不同
2. 同一 channel 的不同 API Key 可能属于不同分组/租户，提供不同的模型列表、协议、价格倍率
3. Key 可能随时换分组，之前探测的模型列表会失效，需要重新检测
4. 现有 `MetricsManager` 的 identity 已经是 endpoint 粒度：`GenerateMetricsIdentityKey(baseURL, apiKey, serviceType)`

```text
Channel（渠道，稳定身份 ChannelUID）
  └── KeyEndpoint（baseURL + apiKey 对）  ← 画像的最小单元
        └── ModelProfile（该 endpoint 上的每个实际模型）
```

ChannelProfile 是 KeyEndpoint 画像的聚合视图，用于 UI 展示和调度粗筛。

### 3.1.1 稳定身份与 Metrics Identity

**channel index 不能作为持久身份。** 当前管理 API 和调度器大量使用数组 index 作为 `channelId`，但用户重排、删除、插入渠道后 index 会变化。如果画像表以 index 为主键，历史画像、健康证据、模型快照会串到另一个渠道。

落地时必须先引入稳定身份：

```go
type UpstreamConfig struct {
    // ... 现有字段 ...
    ChannelUID string `json:"channelUid,omitempty"` // 新增：渠道稳定 ID，创建后不因重排/改名/API Key 变更而改变
}
```

规则：
- `ChannelUID` 在新增渠道时生成；读取旧配置时由 ConfigManager 补齐并持久化一次。
- `ChannelID` 继续表示当前数组 index，仅用于兼容旧 API 和 UI 展示，不参与新画像表主键。
- `ChannelUID` 不从 name/baseURL/apiKey 推导，避免用户改名或换 key 后画像断裂。
- 删除渠道后，以 `channel_uid + channel_kind` 清理画像；重排渠道不触发画像迁移。

Metrics identity 也必须与现有实现保持一致：

```text
identityBaseURL = utils.MetricsIdentityBaseURL(baseURL, serviceType)
metricsKey      = metrics.GenerateMetricsIdentityKey(baseURL, apiKey, serviceType)
lookupKeys      = metrics.GenerateMetricsLookupKeys(baseURL, apiKey, serviceType)
```

注意：
- `serviceType` 影响 `identityBaseURL` 的默认版本前缀，必须写入画像记录。
- `lookupKeys` 只用于兼容旧历史数据，ProfileStore 主键使用当前规范 `metricsKey`。
- 画像表不保存明文 API Key，只保存 `keyMask` 和 `metricsKey`；如需按 key 展示名称，读取 `APIKeyConfig.Name`。

### 3.2 KeyEndpoint 画像 (KeyEndpointProfile)

**画像的最小单元**，对应一个具体的 `baseURL + apiKey` 组合：

```go
// backend-go/internal/autopilot/key_endpoint_profile.go

type KeyEndpointProfile struct {
    // ── 身份 ──
    ChannelUID     string `json:"channelUid"`     // 稳定渠道 ID，用于持久化主键
    ChannelID      int    `json:"channelId"`      // 当前配置数组 index，仅用于展示/兼容
    ChannelKind    string `json:"channelKind"`
    OriginType     string `json:"originType"`     // official_api | official_token_plan | relay | community | unknown
    OriginTier     string `json:"originTier"`     // first | second | third | local | unknown；信任/隐私等级，不代表实时质量
    ServiceType    string `json:"serviceType"`    // metrics identity 依赖 serviceType
    BaseURL        string `json:"baseUrl"`        // 原始配置 URL，用于展示
    IdentityBaseURL string `json:"identityBaseUrl"` // MetricsIdentityBaseURL(baseURL, serviceType)
    KeyMask        string `json:"keyMask"`        // 掩码后的 key，如 sk-***abc
    MetricsKey     string `json:"metricsKey"`     // GenerateMetricsIdentityKey(baseURL, apiKey, serviceType)
    UpdatedAt      time.Time `json:"updatedAt"`

    // ── 自动推导维度 ──
    HealthState      HealthState    `json:"healthState"`
    HealthConfidence float64        `json:"healthConfidence"`
    QualityTier      QualityTier    `json:"qualityTier"`
    StabilityTier    StabilityTier  `json:"stabilityTier"`
    SpeedTier        SpeedTier      `json:"speedTier"`
    CostTier         CostTier       `json:"costTier"`
    CostProfile      CostProfile    `json:"costProfile,omitempty"`

    // ── 能力标签（该 endpoint 特有）──
    SupportsVision     bool `json:"supportsVision"`
    SupportsToolCalls  bool `json:"supportsToolCalls"`
    SupportsReasoning  bool `json:"supportsReasoning"`
    SupportsLongCtx    bool `json:"supportsLongCtx"`

    // ── 该 endpoint 的可用模型列表 ──
    AvailableModels    []string `json:"availableModels"`    // 探测到的实际模型列表
    ModelMapping       map[string]string `json:"modelMapping"` // 该 endpoint 的模型映射

    // ── 运行时指标（来自 MetricsManager）──
    SuccessRate15m   float64    `json:"successRate15m"`
    P95LatencyMs     int64      `json:"p95LatencyMs"`
    ConsecutiveFail  int        `json:"consecutiveFail"`
    LastSuccessAt    *time.Time `json:"lastSuccessAt,omitempty"`
    LastFailureAt    *time.Time `json:"lastFailureAt,omitempty"`

    // ── 自动限速画像（仅在未显式配置时用于运行态 limiter）──
    DiscoveredRPM          int     `json:"discoveredRpm,omitempty"`
    DiscoveredMaxConcurrent int    `json:"discoveredMaxConcurrent,omitempty"`
    RateLimitSource        string  `json:"rateLimitSource,omitempty"` // manual | header | passive_aimd | unknown
    RateLimitConfidence    float64 `json:"rateLimitConfidence,omitempty"`

    // ── 分组感知 ──
    DetectedGroup    string    `json:"detectedGroup,omitempty"`   // 检测到的 key 分组标识
    GroupChangedAt   *time.Time `json:"groupChangedAt,omitempty"` // 分组变更时间
    ModelListHash    string    `json:"modelListHash,omitempty"`   // 模型列表哈希，用于检测变更

    // ── 诊断 ──
    HealthEvidence   []string        `json:"healthEvidence"`
    SuggestedAction  SuggestedAction `json:"suggestedAction"`

    // ── 元数据 ──
    Source           string  `json:"source"`
    Confidence       float64 `json:"confidence"`
}
```

```go
// CostProfile 描述该 endpoint 的实际成本倍率。
// 目标是把模型注册表里的公开 USD 价格换算成用户真实付费成本。
type CostProfile struct {
    // 分组倍率：不同上游账号组/模型组常有不同计费倍率。
    // key 为模型组或通配符，例如 "*"、"claude-opus"、"gpt-5"、"gemini"。
    GroupMultipliers map[string]float64 `json:"groupMultipliers,omitempty"`

    // 充值倍率：充值赠送或折扣换算。1.0=无折扣；2.0=付 1 得 2，真实成本减半。
    RechargeMultiplier float64 `json:"rechargeMultiplier,omitempty"`

    // 最终成本倍率，按请求模型解析后计算：
    // effective = groupMultiplier / rechargeMultiplier
    EffectiveCostMultiplier float64 `json:"effectiveCostMultiplier,omitempty"`

    // 基于模型注册表 Pricing × EffectiveCostMultiplier 得到的估算价格。
    EffectiveInputCostPerMTok  float64 `json:"effectiveInputCostPerMTok,omitempty"`
    EffectiveOutputCostPerMTok float64 `json:"effectiveOutputCostPerMTok,omitempty"`
    EffectiveEmbeddingCostPerMTok float64 `json:"effectiveEmbeddingCostPerMTok,omitempty"`
    EffectiveImageUnitCost        float64 `json:"effectiveImageUnitCost,omitempty"`

    Source     string  `json:"source"`     // manual | default | inferred
    Confidence float64 `json:"confidence"` // 手动配置为 1.0
}
```

成本规则：
- 文本类请求使用 input/output token 价格。
- vectors 使用 embedding token 价格；如果模型注册表缺少 embedding 价格，只做 shadow 展示，不参与强排序。
- images 使用按张/尺寸/质量/operation 的 unit price；不要用 chat token 价格估算生图成本。
- `EffectiveImageUnitCost` 可以是按当前请求参数计算后的请求级值；ProfileStore 只缓存默认尺寸/质量的代表值。

```go
// SubscriptionProfile 描述渠道背后的套餐/余额/价格来源。
// 它由订阅中心维护，被渠道中心和智能路由读取。
type SubscriptionProfile struct {
    SubscriptionUID string `json:"subscriptionUid"`
    DisplayName     string `json:"displayName"`
    Provider        string `json:"provider"` // openai | anthropic | google | relay_x | community_x | custom
    OriginType      string `json:"originType"`
    OriginTier      string `json:"originTier"`

    BillingMode string `json:"billingMode"` // official_api | token_plan | prepaid_credit | shared_free | unknown
    Currency    string `json:"currency,omitempty"`
    Balance     float64 `json:"balance,omitempty"`
    BalanceUpdatedAt *time.Time `json:"balanceUpdatedAt,omitempty"`

    // 套餐默认成本倍率；channel/key 可继续覆盖。
    GroupMultipliers  map[string]float64 `json:"groupMultipliers,omitempty"`
    RechargeMultiplier float64 `json:"rechargeMultiplier,omitempty"`

    LinkedChannelUIDs []string `json:"linkedChannelUids,omitempty"`
    Source     string  `json:"source"`     // manual | imported | inferred
    Confidence float64 `json:"confidence"`
}
```

```go
// ChannelOriginType 描述渠道来源。它属于渠道中心的信任/隐私治理维度，不属于质量画像。
type ChannelOriginType string

const (
    OriginOfficialAPI       ChannelOriginType = "official_api"        // 官方 API key
    OriginOfficialTokenPlan ChannelOriginType = "official_token_plan" // 官方 token/subscription plan
    OriginRelay             ChannelOriginType = "relay"               // 付费/商业中转站
    OriginCommunity         ChannelOriginType = "community"           // 公益站/白嫖站/临时共享站
    OriginLocalRuntime      ChannelOriginType = "local_runtime"       // 本地 Ollama/LM Studio/llama-server
    OriginUnknown           ChannelOriginType = "unknown"
)

type ChannelOriginTier string

const (
    OriginTierFirst   ChannelOriginTier = "first"   // 官方 API / 官方 token plan，可用于隐私敏感 advisor
    OriginTierSecond  ChannelOriginTier = "second"  // 中转站，不用于隐私敏感 advisor
    OriginTierThird   ChannelOriginTier = "third"   // 公益站，不用于隐私敏感 advisor
    OriginTierLocal   ChannelOriginTier = "local"   // 用户自托管本地运行时，可用于隐私敏感 advisor
    OriginTierUnknown ChannelOriginTier = "unknown"
)
```

### 3.2.1 信任与隐私等级 (OriginTier)

`OriginTier` 是渠道中心的一等维度，用来表达隐私信任、来源可控性、账单可解释性和长期可维护性：

| 来源 | OriginType | OriginTier | 语义 |
|------|------------|------------|------|
| 官方 API | `official_api` | `first` | 直接使用模型厂商 API key，账单和限额最清楚 |
| 官方 token plan | `official_token_plan` | `first` | 官方订阅/token 计划，虽然计费形态不同，但信任/隐私等级等同官方 API |
| 中转站 | `relay` | `second` | 第三方商业代理或聚合站，价格/模型/限额可能有二次包装 |
| 公益站 | `community` | `third` | 公益、共享、临时或白嫖站，波动和不可控性更高 |
| 本地运行时 | `local_runtime` | `local` | 用户自托管 Ollama/LM Studio/llama-server，治理可控但质量由本地模型决定 |
| 未知 | `unknown` | `unknown` | 未标注来源，只做观察，不自动假设等级 |

约束：
- `OriginTier` **不得参与 QualityTier 推导**，也不得把 `first` 自动视为高质量、把 `third` 自动视为低质量。
- `OriginTier` 可以影响渠道中心默认分组、风险提示、成本解释和同分调度 tie-breaker。
- 当三等来源在短时间内拥有更高成功率、更低延迟或更稳定流式表现时，SmartRouter 必须允许它在满足硬约束后胜出。
- `OriginTier=local` 不自动等同高质量或低质量；本地模型质量必须由 LocalModelRuntimeProfile 和运行画像决定。
- 凡是需要读取用户 prompt、系统提示、上下文摘要或敏感 metadata 的 AI 判定，只允许 `OriginTier=first` 或 `OriginTier=local`。
- `OriginTier=second|third|unknown` 可以作为候选执行上游，但不能作为 routing advisor / classifier / privacy-sensitive evaluator。
- 如果一个 channel 混入不同来源的 baseURL/key，UI 标记为「来源混杂」，并建议拆成多个 channel；MVP 不做 endpoint 级来源 override。

### 3.2.2 订阅与渠道链接

`SubscriptionProfile` 是订阅中心的最小业务对象，`UpstreamConfig.SubscriptionUID` 是渠道到订阅/套餐的链接：

```text
SubscriptionProfile（官方 API / token plan / 中转套餐 / 公益来源）
  └── Channel（一个订阅可挂多个协议渠道）
        └── KeyEndpoint（baseURL + apiKey）
```

规则：
- 官方 API key 和官方 token plan 都归入 `OriginTier=first`，但 `BillingMode` 不同，价格计算要走各自套餐逻辑。
- 中转站归入 `OriginTier=second`，其 `GroupMultipliers`、`RechargeMultiplier` 优先从订阅套餐继承，再被 channel/key 覆盖。
- 公益站归入 `OriginTier=third`，允许 `BillingMode=shared_free`，但仍要记录 RPM、稳定性和质量趋势。
- 订阅中心负责维护余额、套餐倍率、续费周期和备注；渠道中心只展示链接结果和运行状态，不重复维护同一套价格字段。
- 未链接订阅的历史渠道允许继续运行，UI 显示为「未归档来源」，不会阻塞调度。

### 3.2.3 同订阅多 Key：能力共享与配额负载均衡

**问题**：官方订阅 plan（Claude Max、Codex plan 等）允许用户持有多个 key，或购买多份相同套餐。若每个 endpoint 独立探测与配置，探测预算被浪费、配置重复；请求量大时还需要在同 plan 的多个 key 之间按官方 RPM 限速自动负载均衡，而不是打满第一个 key 才溢出到下一个。

**能力检测去重（同一套餐只需一套能力检测和配置）**：

```text
SubscriptionUID 相同的 endpoint 共享一份「订阅级能力画像」：
  模型列表 / vision / tool / reasoning 支持 / 协议兼容开关 → 只探测一次，
  存在 SubscriptionProfile.SharedCapability，endpoint 画像以引用方式继承。

仍然 per-endpoint 维护的：健康状态、延迟、熔断、用量窗口
  （key 可能被单独封禁/限速，能力相同不代表运行态相同）

继承失效条件：某 key 实测模型列表哈希与订阅级不一致（GroupChangeDetector 信号）
  → 该 key 脱离继承、独立探测，并在 UI 标记「与套餐能力不一致」警告。
```

**配额负载均衡（Phase 1 shadow 展示，Phase 2 生效）**：

```text
每 key 维护配额余量视图：
  remainingRPM = officialRPM − 当前窗口已用
  officialRPM 来源优先级：内置 manifest > 用户配置 > RateLimitDiscoverer 建议

同订阅多 key 时按「剩余余量加权」分配请求（余量大者优先、
窗口最早重置者兜底），替代顺序打满：
  仅当渠道 autoManaged 且同一 SubscriptionUID 下 key 数 > 1 时启用；
  否则保持现有 sequential/random key 顺序策略不变。

某 key 的窗口配额耗尽 → 临时降权并切到同订阅其他 key，
不熔断整个渠道（这是配额耗尽，不是故障）。
```

### 3.2.4 订阅用量窗口 (Usage Windows)

部分官方 plan 提供 5h / 日 / 周 / 月等窗口的用量查询与限制（如 Claude 订阅的 5h 滚动窗口 + 周配额）。在 key 的位置展示这些窗口，帮助用户掌握使用情况：

```go
type UsageWindow struct {
    Window    string    `json:"window"`    // "5h" | "day" | "week" | "month"
    Used      float64   `json:"used"`      // 已用量
    Limit     float64   `json:"limit"`     // 上限，0 = 未知
    Unit      string    `json:"unit"`      // requests | tokens | credits | percent
    ResetsAt  time.Time `json:"resetsAt"`  // 窗口重置时间
    Source    string    `json:"source"`    // official_api | response_header | local_metering
    FetchedAt time.Time `json:"fetchedAt"`
}
// KeyEndpointProfile 与 SubscriptionProfile 均增加 UsageWindows []UsageWindow
```

数据来源优先级：

```text
1. 官方用量 API：有 provider adapter 的 plan 主动拉取（§Phase 4 订阅 adapter 的可提前子集）
2. 响应头：anthropic-ratelimit-* / x-ratelimit-* 等（复用 RateLimitDiscoverer 的信号 tap）
3. 本地计量兜底：CCX 按 key 累计请求数/token（误差来自窗口起点对齐，标注 Source=local_metering）
```

UI 要求：key 行内显示主窗口（5h）用量进度条，悬浮展开全部窗口；用量 >80% 黄色预警、超限红色并显示预计重置时间。调度联动（Phase 2）：窗口余量进入配额负载均衡权重，窗口耗尽的 key 临时降权而非熔断。

### 3.3 Channel 画像 (ChannelProfile) — 聚合视图

ChannelProfile 不再存储原始数据，而是从 KeyEndpoint 画像聚合而来：

```go
// backend-go/internal/autopilot/channel_profile.go

type ChannelProfile struct {
    ChannelUID  string `json:"channelUid"`
    ChannelID   int    `json:"channelId"` // 当前配置数组 index，仅用于展示/兼容
    ChannelKind string `json:"channelKind"`
    OriginType  string `json:"originType"`
    OriginTier  string `json:"originTier"` // 信任/隐私等级，不参与质量聚合
    UpdatedAt   time.Time `json:"updatedAt"`

    // ── 聚合维度（取所有 endpoint 的"最差可用"或"最佳代表"）──
    HealthState      HealthState    `json:"healthState"`     // 取最差：任一 endpoint dead → degraded
    QualityTier      QualityTier    `json:"qualityTier"`     // 取最佳 endpoint 的质量
    StabilityTier    StabilityTier  `json:"stabilityTier"`   // 取中位数
    SpeedTier        SpeedTier      `json:"speedTier"`       // 取中位数
    CostTier         CostTier       `json:"costTier"`        // 取最佳 endpoint 的成本

    // ── 能力标签（取并集，但标注不一致）──
    SupportsVision     bool `json:"supportsVision"`
    SupportsToolCalls  bool `json:"supportsToolCalls"`
    SupportsReasoning  bool `json:"supportsReasoning"`
    SupportsLongCtx    bool `json:"supportsLongCtx"`

    // ── 聚合指标 ──
    TotalEndpoints     int     `json:"totalEndpoints"`
    HealthyEndpoints   int     `json:"healthyEndpoints"`
    TotalModels        int     `json:"totalModels"`       // 去重后的模型总数
    SuccessRate15m     float64 `json:"successRate15m"`
    P95LatencyMs       int64   `json:"p95LatencyMs"`

    // ── 能力不一致警告 ──
    EndpointInconsistencies []EndpointInconsistency `json:"endpointInconsistencies,omitempty"`

    Source     string  `json:"source"`
    Confidence float64 `json:"confidence"`
}

// EndpointInconsistency 记录同一 channel 内不同 endpoint 的能力差异
type EndpointInconsistency struct {
    Dimension  string `json:"dimension"`  // "quality" | "vision" | "models" | "latency"
    Detail     string `json:"detail"`     // 例如 "endpoint-1: premium, endpoint-2: normal"
    Severity   string `json:"severity"`   // "info" | "warning"
}
```

**聚合策略**：

```text
维度        │ 策略              │ 原因
────────────┼───────────────────┼──────────────────────────────
HealthState │ 取最差            │ 任一 endpoint 死了，整个渠道至少 degraded
QualityTier │ 取最佳            │ 调度时选最佳 endpoint 的质量档
Stability   │ 取中位数          │ 避免单个坏 endpoint 拉低整个渠道
Speed       │ 取中位数          │ 同上
Cost        │ 取最佳            │ 便宜的 endpoint 存在就有价值
Vision等    │ 取并集            │ 只要有一个 endpoint 支持就算支持
OriginTier  │ 渠道级字段        │ 信任等级是隐私/治理属性，不从 endpoint 运行质量聚合
```

### 3.4 模型画像 (ModelProfile)

每个 `(KeyEndpoint + 模型)` 组合的画像：

```go
// backend-go/internal/autopilot/model_profile.go

type ModelProfile struct {
    // ── 锚定到 KeyEndpoint ──
    ChannelUID  string `json:"channelUid"`
    ChannelID   int    `json:"channelId"`   // 当前配置数组 index，仅用于展示/兼容
    ChannelKind string `json:"channelKind"`
    ServiceType string `json:"serviceType"`
    MetricsKey  string `json:"metricsKey"`  // 精确到 identityBaseURL+key+serviceType
    ModelID     string `json:"modelId"`     // 该 endpoint 内的实际模型名
    UpdatedAt   time.Time `json:"updatedAt"`

    // ── 能力 ──
    ModelFamily    ModelFamily `json:"modelFamily"`   // 派系：claude/openai/gemini/deepseek/…，从注册表推导
    QualityTier   QualityTier `json:"qualityTier"`    // 基于模型族的质量档（premium/high/normal/low）
    SpeedTier     SpeedTier   `json:"speedTier"`
    ContextTokens int         `json:"contextTokens"`
    SupportsVision    bool    `json:"supportsVision"`
    SupportsToolCalls bool    `json:"supportsToolCalls"`
    SupportsReasoning bool    `json:"supportsReasoning"`

    // ── 上游供应商质量（同模型在不同上游的质量差异）──
    // 解决问题：同一 claude-opus-4-8，官方 Anthropic vs AWS Bedrock vs kiro 中转的质量可能差异很大
    // 与 QualityTier 的区别：QualityTier 基于模型族（opus=premium），ProviderQuality 区分同族内的上游差异
    ProviderQualityScore float64 `json:"providerQualityScore,omitempty"` // 0.0-1.0，该endpoint对该模型的实际实现质量
    ProviderQualitySource string `json:"providerQualitySource,omitempty"` // probe | user_feedback | inferred | default
    ProviderQualityConfidence float64 `json:"providerQualityConfidence,omitempty"` // 置信度

    // ── 任务域优势（不同模型的强项任务不同）──
    // 解决问题：opus/fable/gemini/glm 的审美明显好于 gpt，但 gpt 的代码审核堪比 fable
    // 缺省时回退到 ModelFamily 级种子矩阵（§5.7），0.5 = 中性
    TaskDomainStrengths map[TaskDomain]float64 `json:"taskDomainStrengths,omitempty"`

    // ── 思考等级（同模型不同思考档位的智商差异，§5.8）──
    SupportsEffortControl bool          `json:"supportsEffortControl,omitempty"` // 上游是否可控思考等级
    SupportedEffortLevels []EffortLevel `json:"supportedEffortLevels,omitempty"` // 可用档位（按派系映射）

    // ── 探测结果 ──
    ProbeSuccess    bool      `json:"probeSuccess"`
    LastProbeAt     time.Time `json:"lastProbeAt"`
    ProbeLatencyMs  int64     `json:"probeLatencyMs"`
    ProbeConfidence float64   `json:"probeConfidence"`

    // ── 来源 ──
    Source string `json:"source"` // builtin_registry | auto_probe | capability_test | manual
}
```

### 3.5 请求画像 (RequestProfile)

每次请求在进入调度器前生成，不持久化：

```go
// backend-go/internal/autopilot/request_profile.go

type RequestProfile struct {
    // ── 来自请求 ──
    Model       string // 请求的目标模型
    ChannelKind string // messages | chat | responses | gemini | images | vectors
    Operation   string // completion | image_generation | image_edit | image_variation | embedding
    AgentRole   string // "main" | "subagent"
    AgentType   string // "codex_subagent" | "claude_code_subagent"
    HasImage    bool   // 是否包含图片
    EstTokens   int    // 估算输入 token 数

    // ── 来自模型注册表 ──
    QualityNeed   QualityTier   // 该模型对应的质量需求
    ContextNeed   int           // 最小上下文窗口
    VisionNeed    bool          // 是否需要识图
    ImageGenNeed  bool          // 是否需要原生生图端点
    EmbeddingNeed bool          // 是否需要原生 embedding 端点
    ToolUseNeed   bool          // 是否需要工具调用
    ReasoningNeed bool          // 是否需要推理
    EmbeddingDimension int      // vectors handler 的硬约束；未知时为 0

    // ── 任务分类 ──
    TaskClass TaskClass // supervisor | worker | lightweight | vision | long_context | image_generation | embedding
}

type TaskClass string
const (
    TaskClassSupervisor   TaskClass = "supervisor"    // 主代理/监工
    TaskClassWorker       TaskClass = "worker"         // 子代理/干活
    TaskClassLightweight  TaskClass = "lightweight"    // 轻任务（摘要/标题）
    TaskClassVision       TaskClass = "vision"         // 识图任务
    TaskClassLongContext  TaskClass = "long_context"   // 长上下文任务
    TaskClassImageGen     TaskClass = "image_generation" // 原生生图任务
    TaskClassEmbedding    TaskClass = "embedding"      // 原生向量/embedding 任务
)
```

### 3.6 本地模型运行时 (LocalModelRuntimeProfile)

本地模型既可以作为普通候选上游，也可以作为路由辅助模型。支持目标：
- Ollama：本地模型服务，可通过 native API 或 OpenAI-compatible API 接入。
- LM Studio：通常以 OpenAI-compatible `/v1` 暴露本地模型。
- llama-server / llama.cpp server：通常以 OpenAI-compatible `/v1` 暴露本地模型。
- 其他本地 OpenAI-compatible 服务：按 `runtimeType=openai_compatible` 处理。

```go
// LocalModelRuntimeProfile 描述一个本地运行时中的一个模型。
type LocalModelRuntimeProfile struct {
    RuntimeUID string `json:"runtimeUid"`
    Name       string `json:"name,omitempty"`

    // ollama | lmstudio | llama_server | openai_compatible
    RuntimeType string `json:"runtimeType"`
    BaseURL     string `json:"baseUrl"` // 例如 http://localhost:11434 或 http://localhost:1234/v1
    Model       string `json:"model"`

    // candidate_model: 可直接承接请求；routing_advisor: 只产出路由 hint
    Roles []string `json:"roles"`

    OriginType string `json:"originType"` // local_runtime
    OriginTier string `json:"originTier"` // local

    ContextTokens int  `json:"contextTokens,omitempty"`
    SupportsTools bool `json:"supportsTools,omitempty"`
    SupportsVision bool `json:"supportsVision,omitempty"`
    SupportsReasoning bool `json:"supportsReasoning,omitempty"`

    // 本地资源画像
    P95LatencyMs int64 `json:"p95LatencyMs,omitempty"`
    TokensPerSecond float64 `json:"tokensPerSecond,omitempty"`
    MaxPromptTokensForAdvisor int `json:"maxPromptTokensForAdvisor,omitempty"`
    TimeoutMs int `json:"timeoutMs,omitempty"`

    // 路由辅助可靠度，只能通过 shadow 评估提升
    AdvisorConfidence float64 `json:"advisorConfidence,omitempty"`
    AdvisorShadowAccuracy float64 `json:"advisorShadowAccuracy,omitempty"`
    LastProbeAt *time.Time `json:"lastProbeAt,omitempty"`
    Status string `json:"status"` // healthy | slow | unavailable | unknown
}
```

本地运行时规则：
- 作为 `candidate_model` 时，它和普通 channel 一样进入 ChannelProfile/ModelProfile，但默认只承接 lightweight/worker 的低风险任务。
- 作为 `routing_advisor` 时，它只输出结构化 `RoutingHint`，不直接决定最终路由。
- 本地模型默认有效成本接近 0，但不能因此覆盖质量下界；它的真实成本可展示为本机资源成本或用户自定义电费/硬件摊销。
- 本地 runtime 作为 advisor 时必须设置超时；超时或不可用时直接 fail-open，回到默认 SmartRouter。
- 不自动把远端强模型任务切分给本地模型处理；只有用户配置的任务模板允许本地预处理或摘要。

### 3.7 人工路由意图 (ManualRoutingIntent)

用户需要适度干预系统选择，而不是长期关闭 Autopilot。典型场景：
- 新模型试用：用户暂时想测试 `fable-5`，即使模型注册表和画像还不完整。
- 新公益站试用：用户希望某个公益站先接一部分 worker/lightweight 流量，用真实请求快速收集画像。
- 单会话排障：用户只想让当前会话固定走某个 channel/endpoint，验证兼容性。

```go
// ManualRoutingIntent 是用户显式表达的短期路由意图。
// 它比 SmartRouter 默认排序优先，但不能绕过协议、鉴权、上下文、vision/tool 等硬约束。
type ManualRoutingIntent struct {
    IntentUID string `json:"intentUid"`
    Name      string `json:"name,omitempty"`

    // model_trial | channel_trial | endpoint_trial | session_pin
    IntentType string `json:"intentType"`

    ChannelKind string `json:"channelKind"`          // messages/chat/responses/gemini/images/vectors
    ChannelUID  string `json:"channelUid,omitempty"` // 可选：指定渠道
    MetricsKey  string `json:"metricsKey,omitempty"` // 可选：精确到 baseURL+key endpoint
    Model       string `json:"model,omitempty"`      // 请求模型，例如 fable-5
    MappedModel string `json:"mappedModel,omitempty"` // 可选：上游实际模型

    // 作用范围
    AgentRoles []string `json:"agentRoles,omitempty"` // main/subagent；为空表示全部
    TaskClasses []TaskClass `json:"taskClasses,omitempty"`
    SessionID string `json:"sessionId,omitempty"` // 可选：只影响当前会话
    TrafficPercent int `json:"trafficPercent,omitempty"` // 1-100；默认 100

    // 安全边界
    ExpiresAt time.Time `json:"expiresAt"`
    MaxRequests int `json:"maxRequests,omitempty"`
    MaxEstimatedCost float64 `json:"maxEstimatedCost,omitempty"`
    FallbackOnFailure bool `json:"fallbackOnFailure"` // true: 失败后回到 Autopilot 默认计划
    RequireHardConstraints bool `json:"requireHardConstraints"` // 默认 true

    // 观测
    CreatedBy string `json:"createdBy,omitempty"`
    CreatedAt time.Time `json:"createdAt"`
    Reason    string `json:"reason,omitempty"`
    Status    string `json:"status"` // active | expired | exhausted | disabled
}
```

规则：
- `ManualRoutingIntent` 是**临时意图**，默认 TTL 不超过 24 小时；超过需要用户重新确认。
- `model_trial` 允许未知模型进入指定 channel/endpoint 的探测流量，但结果只写入 trial 画像，不自动写 `modelMapping`。
- `channel_trial` / `endpoint_trial` 只改变候选优先级，不把公益站永久提升为高优先级。
- `session_pin` 只影响指定会话，不改变全局调度。
- 即使用户显式试用，也不能绕过鉴权、协议兼容、上下文窗口、vision/tool/reasoning 等硬约束；如用户选择“仍要尝试未知能力”，UI 必须显示风险并限制在 session scope。
- trial 的成功率、延迟、断流、成本和错误会进入画像，但标记 `Source=manual_trial`，避免污染长期自动推导。

### 3.8 存储方案

| 数据 | 存储 | TTL |
|------|------|-----|
| KeyEndpointProfile | SQLite `key_endpoint_profiles` 表 + 内存缓存 | 持久化，运行时 5min 刷新 |
| ChannelProfile | 不持久化，运行时从 KeyEndpoint 聚合 | 内存计算 |
| ModelProfile | SQLite `model_profiles` 表 + 内存缓存 | 持久化，运行时 10min 刷新 |
| RequestProfile | 内存 | 请求级，不持久化 |
| LocalModelRuntimeProfile | SQLite `local_model_runtimes` 表 + 内存缓存 | 持久化，运行时 5min 刷新 |
| ManualRoutingIntent | SQLite `manual_routing_intents` 表 + 内存缓存 | 到期后保留结果 7 天 |
| TimeBucketMetrics | SQLite `time_bucket_metrics` 表 + 内存聚合桶 | 7 天滚动清理 |
| 健康证据 | SQLite `health_evidence` 表 | 7 天滚动清理 |
| 模型列表快照 | SQLite `model_list_snapshots` 表 | 30 天滚动清理，用于检测分组变更 |
| 画像变更日志 | SQLite `profile_changelog` 表 | 30 天滚动清理 |

```sql
-- 画像最小单元：baseURL + apiKey 对
CREATE TABLE key_endpoint_profiles (
    channel_uid       TEXT    NOT NULL,
    channel_id        INTEGER NOT NULL,      -- 当前配置数组 index，仅作展示快照
    channel_kind      TEXT    NOT NULL,
    service_type      TEXT    NOT NULL,
    metrics_key       TEXT    NOT NULL,      -- GenerateMetricsIdentityKey(baseURL, apiKey, serviceType)
    identity_base_url TEXT    NOT NULL,      -- MetricsIdentityBaseURL(baseURL, serviceType)
    base_url          TEXT    NOT NULL,      -- 原始配置 URL
    key_mask          TEXT    NOT NULL,      -- 掩码后的 key
    profile_json      TEXT    NOT NULL,
    updated_at        TEXT    NOT NULL,
    PRIMARY KEY (channel_uid, channel_kind, metrics_key)
);
CREATE INDEX idx_key_endpoint_profiles_kind_index ON key_endpoint_profiles(channel_kind, channel_id);

-- 模型画像锚定到 endpoint
CREATE TABLE model_profiles (
    channel_uid  TEXT    NOT NULL,
    channel_id   INTEGER NOT NULL,          -- 当前配置数组 index，仅作展示快照
    channel_kind TEXT    NOT NULL,
    service_type TEXT    NOT NULL,
    metrics_key  TEXT    NOT NULL,          -- 精确到 endpoint
    model_id     TEXT    NOT NULL,
    profile_json TEXT    NOT NULL,
    updated_at   TEXT    NOT NULL,
    PRIMARY KEY (channel_uid, channel_kind, metrics_key, model_id)
);
CREATE INDEX idx_model_profiles_kind_index ON model_profiles(channel_kind, channel_id);

CREATE TABLE local_model_runtimes (
    runtime_uid  TEXT PRIMARY KEY,
    runtime_type TEXT    NOT NULL,
    base_url     TEXT    NOT NULL,
    model        TEXT    NOT NULL,
    profile_json TEXT    NOT NULL,
    status       TEXT    NOT NULL,
    updated_at   TEXT    NOT NULL
);
CREATE INDEX idx_local_model_runtimes_type ON local_model_runtimes(runtime_type, status);

-- 短期人工试用意图
CREATE TABLE manual_routing_intents (
    intent_uid   TEXT PRIMARY KEY,
    intent_type  TEXT    NOT NULL,
    channel_kind TEXT    NOT NULL,
    intent_json  TEXT    NOT NULL,
    status       TEXT    NOT NULL,
    expires_at   TEXT    NOT NULL,
    created_at   TEXT    NOT NULL,
    updated_at   TEXT    NOT NULL
);
CREATE INDEX idx_manual_routing_intents_active
    ON manual_routing_intents(channel_kind, status, expires_at);

-- 模型列表快照，用于检测 key 换分组
CREATE TABLE model_list_snapshots (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    channel_uid  TEXT    NOT NULL,
    channel_kind TEXT    NOT NULL,
    metrics_key  TEXT    NOT NULL,
    model_list   TEXT    NOT NULL,          -- JSON array of model IDs
    list_hash    TEXT    NOT NULL,          -- SHA256 of sorted model list
    detected_at  TEXT    NOT NULL
);
CREATE INDEX idx_snapshots_channel ON model_list_snapshots(channel_uid, channel_kind, detected_at);

CREATE TABLE health_evidence (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    channel_uid TEXT    NOT NULL,
    channel_id  INTEGER NOT NULL,           -- 当前配置数组 index，仅作展示快照
    channel_kind TEXT   NOT NULL,
    metrics_key TEXT    NOT NULL DEFAULT '', -- 精确到 endpoint
    kind        TEXT    NOT NULL,
    evidence    TEXT    NOT NULL,
    severity    TEXT    NOT NULL,
    created_at  TEXT    NOT NULL
);
CREATE INDEX idx_health_evidence_channel ON health_evidence(channel_uid, channel_kind, created_at);
CREATE INDEX idx_health_evidence_endpoint ON health_evidence(channel_uid, channel_kind, metrics_key, created_at);
```

配置同步规则：
- 服务启动和配置热重载时，按 `channel_uid + channel_kind` 对齐当前 index，刷新画像中的 `channel_id` 展示快照。
- 配置中不存在的 `channel_uid` 标记为 `orphaned`，延迟清理；立即删除可能影响正在展示的历史诊断。
- API 返回时同时带 `channelUid` 和 `channelId`；所有写操作使用 `channelUid`，只读兼容接口可继续接收当前 index。

### 3.9 Key 分组变更检测

当 key 换了分组，之前的模型发现结果会失效。检测机制：

```go
// backend-go/internal/autopilot/group_change_detector.go

type GroupChangeDetector struct {
    profileStore *ProfileStore
}

// CheckGroupChange 对比本次探测的模型列表与上次快照
// 返回 (changed bool, oldHash string, newHash string, addedModels []string, removedModels []string)
func (d *GroupChangeDetector) CheckGroupChange(
    channelUID string,
    channelKind string,
    metricsKey string,
    currentModels []string,
) (bool, GroupChangeResult) {
    // 1. 取上次快照
    lastSnapshot := d.profileStore.GetLatestModelListSnapshot(channelUID, channelKind, metricsKey)
    if lastSnapshot == nil {
        // 首次，记录快照
        d.profileStore.SaveModelListSnapshot(channelUID, channelKind, metricsKey, currentModels)
        return false, GroupChangeResult{}
    }

    // 2. 计算当前模型列表哈希
    currentHash := hashModelList(currentModels)

    // 3. 对比
    if currentHash == lastSnapshot.ListHash {
        return false, GroupChangeResult{} // 无变化
    }

    // 4. 分组变更！计算差异
    added, removed := diffModelLists(lastSnapshot.ModelList, currentModels)

    // 5. 记录变更
    now := time.Now()
    d.profileStore.SaveModelListSnapshot(channelUID, channelKind, metricsKey, currentModels)
    d.profileStore.UpdateGroupChangedAt(channelUID, channelKind, metricsKey, now)

    return true, GroupChangeResult{
        OldHash:      lastSnapshot.ListHash,
        NewHash:      currentHash,
        AddedModels:  added,
        RemovedModels: removed,
        ChangedAt:    now,
    }
}
```

**触发时机**（与 L1/L2/L3 信号结合）：

```text
场景                          │ 触发方式              │ 动作
──────────────────────────────┼───────────────────────┼──────────────────────────────
L1 被动：请求返回 model_not_found │ 真实请求失败         │ 触发该 endpoint 的 L2 重探测
L2 探测：定期轻量探测           │ 每 2h 一次            │ 拉 /models 对比快照
新 key 添加                   │ 配置变更事件          │ 立即触发 L2 探测
用户手动「重新探测」            │ UI 按钮              │ 立即触发 L3 深度探测
```

**分组变更后的动作**：

```text
1. 标记该 endpoint 的 ModelProfile 为 stale
2. 重新触发 L2 模型发现（拉 /models）
3. 重新生成 ModelProfile
4. 如果模型列表变化导致能力标签变化 → 更新 KeyEndpointProfile → 重新聚合 ChannelProfile
5. 通知前端：endpoint 模型列表已变更
```

### 3.10 时序感知 — 快照到持续评估

**⚠️ 画像不是"探测一次定终身"。上游同一分组在忙时和闲时的质量差异可能很大。**

一个 endpoint 在凌晨 3 点测试通过，不代表早高峰 9 点仍然可用。画像系统必须从"静态快照"升级为"持续滚动评估"。

#### 时间桶指标 (TimeBucketMetrics)

```go
// backend-go/internal/autopilot/time_series.go

// TimeBucketMetrics 按固定时间桶记录 endpoint 的质量快照
type TimeBucketMetrics struct {
    ChannelID   int    `json:"channelId"`
    MetricsKey  string `json:"metricsKey"`
    BucketStart time.Time `json:"bucketStart"` // 桶起始时间（UTC，15 分钟对齐）
    BucketSize  time.Duration `json:"bucketSize"` // 桶大小，默认 15 分钟

    // ── 该桶内的聚合指标 ──
    RequestCount    int     `json:"requestCount"`
    SuccessCount    int     `json:"successCount"`
    FailureCount    int     `json:"failureCount"`
    OverloadedCount int     `json:"overloadedCount"` // 429
    StreamBreakCount int    `json:"streamBreakCount"`
    EmptyResponseCount int  `json:"emptyResponseCount"`

    P50LatencyMs  int64   `json:"p50LatencyMs"`
    P95LatencyMs  int64   `json:"p95LatencyMs"`
    P99LatencyMs  int64   `json:"p99LatencyMs"`

    SuccessRate   float64 `json:"successRate"`
    AvgInputTokens  int   `json:"avgInputTokens"`
    AvgOutputTokens int   `json:"avgOutputTokens"`
}
```

#### 质量趋势 (QualityTrend)

```go
// QualityTrend 描述 endpoint 质量的变化方向和幅度
type QualityTrend struct {
    MetricsKey  string    `json:"metricsKey"`
    DetectedAt  time.Time `json:"detectedAt"`

    // ── 趋势方向 ──
    Direction   TrendDirection `json:"direction"` // improving | stable | degrading | volatile

    // ── 对比基准 ──
    BaselineWindow   string  `json:"baselineWindow"`   // "7d" | "24h" | "1h"
    BaselineSuccessRate float64 `json:"baselineSuccessRate"`
    CurrentSuccessRate  float64 `json:"currentSuccessRate"`
    DeltaPercent        float64 `json:"deltaPercent"`   // 当前 vs 基准的变化百分比

    // ── 时段模式 ──
    HourlyPattern []HourlyQuality `json:"hourlyPattern,omitempty"` // 24 小时质量热力图
    PeakHours     []int           `json:"peakHours,omitempty"`     // 质量低谷的小时列表
    OffPeakQuality QualityTier    `json:"offPeakQuality,omitempty"`

    // ── 触发动作 ──
    ShouldReevaluate bool   `json:"shouldReevaluate"` // 是否需要重新评估画像
    ReevalReason     string `json:"reevalReason,omitempty"`
}

type TrendDirection string
const (
    TrendImproving TrendDirection = "improving"
    TrendStable    TrendDirection = "stable"
    TrendDegrading TrendDirection = "degrading"
    TrendVolatile  TrendDirection = "volatile" // 忙闲交替剧烈
)

// HourlyQuality 某个小时的平均质量（UTC）
type HourlyQuality struct {
    Hour         int     `json:"hour"`         // 0-23 UTC
    AvgSuccessRate float64 `json:"avgSuccessRate"`
    AvgP95Latency  int64   `json:"avgP95Latency"`
    SampleCount    int     `json:"sampleCount"` // 该小时总请求数（跨多天）
}
```

#### 趋势检测逻辑

```go
// backend-go/internal/autopilot/quality_trend_detector.go

type QualityTrendDetector struct {
    profileStore *ProfileStore
}

// DetectTrend 分析 endpoint 的质量趋势
func (d *QualityTrendDetector) DetectTrend(
    channelID int,
    metricsKey string,
    currentTime time.Time,
) QualityTrend {
    // 1. 取最近 7 天的时间桶数据
    buckets := d.profileStore.GetTimeBuckets(channelID, metricsKey, currentTime.Add(-7*24*time.Hour))

    // 2. 构建三个窗口的基准
    current1h  := aggregateBuckets(buckets, currentTime.Add(-1*time.Hour), currentTime)  // 最近 1 小时
    baseline24h := aggregateBuckets(buckets, currentTime.Add(-24*time.Hour), currentTime.Add(-1*time.Hour)) // 前 24 小时
    baseline7d  := aggregateBuckets(buckets, currentTime.Add(-7*24*time.Hour), currentTime.Add(-24*time.Hour)) // 前 7 天

    // 3. 判断趋势方向
    //    degrading：当前 1h 成功率比 24h 基准下降 > 15%，且比 7d 基准下降 > 10%
    //    improving：当前 1h 成功率比 24h 基准上升 > 10%
    //    volatile：最近 24h 内，小时成功率的标准差 > 20%
    //    stable：其他情况

    // 4. 构建 24 小时质量热力图（UTC 小时 × 平均成功率）
    hourlyPattern := buildHourlyPattern(buckets)
    peakHours := findQualityTroughs(hourlyPattern, threshold: 0.70)

    // 5. 判断是否需要重新评估
    shouldReevaluate := false
    var reason string
    switch {
    case current1h.SuccessRate < baseline7d.SuccessRate * 0.75:
        shouldReevaluate = true
        reason = fmt.Sprintf("成功率从 %.0f%% 降至 %.0f%%", baseline7d.SuccessRate*100, current1h.SuccessRate*100)
    case current1h.P95Latency > baseline7d.P95Latency * 2:
        shouldReevaluate = true
        reason = fmt.Sprintf("p95 延迟从 %dms 升至 %dms", baseline7d.P95Latency, current1h.P95Latency)
    case len(peakHours) > 0 && currentHourInList(currentTime, peakHours):
        // 当前正处于已知质量低谷
        reason = fmt.Sprintf("当前处于已知低谷时段 %v", peakHours)
        // 不触发重评估，但标记为时段性降级
    }

    return QualityTrend{
        MetricsKey:          metricsKey,
        DetectedAt:          currentTime,
        Direction:           trendDir,
        BaselineWindow:      "7d",
        BaselineSuccessRate: baseline7d.SuccessRate,
        CurrentSuccessRate:  current1h.SuccessRate,
        DeltaPercent:        (current1h.SuccessRate - baseline7d.SuccessRate) / baseline7d.SuccessRate * 100,
        HourlyPattern:       hourlyPattern,
        PeakHours:           peakHours,
        ShouldReevaluate:    shouldReevaluate,
        ReevalReason:        reason,
    }
}
```

#### 存储

```sql
-- 时间桶指标，7 天滚动
CREATE TABLE time_bucket_metrics (
    channel_uid  TEXT    NOT NULL,
    channel_kind TEXT    NOT NULL,
    channel_id   INTEGER NOT NULL,     -- 当前配置数组 index，仅作展示快照
    metrics_key  TEXT    NOT NULL,
    bucket_start TEXT    NOT NULL,     -- ISO 8601，15 分钟对齐
    bucket_json  TEXT    NOT NULL,
    PRIMARY KEY (channel_uid, channel_kind, metrics_key, bucket_start)
);
CREATE INDEX idx_time_bucket_metrics_kind_index ON time_bucket_metrics(channel_kind, channel_id, bucket_start);
-- 自动清理：DELETE WHERE bucket_start < datetime('now', '-7 days')
```

#### 对调度的影响

```text
场景                              │ 调度行为
──────────────────────────────────┼──────────────────────────────────────────
trend=stable                      │ 正常，使用当前画像
trend=improving                   │ 正常，画像质量档可能即将升级
trend=degrading + shouldReevaluate│ 降权，同时触发画像重评估
trend=volatile                    │ FastDecay 系数降低（衰减更快，回升更慢）
当前处于 peakHours 低谷           │ 该 endpoint 的 SpeedTier 临时降一档，不标记 dead
忙时质量低 + 闲时质量高           │ SmartRouter 在忙时自动倾向非低谷 endpoint
```

#### 与 HealthAnalyzer 的集成

趋势检测是 HealthAnalyzer 的输入信号之一，不是独立判定：

```text
HealthState 判定 = L1 被动指标 + 趋势信号 + L2/L3 探测（如需要）

具体：
  - L1 成功率 < 50% → 直接 dead（不看趋势）
  - L1 成功率 50-80% + trend=degrading → degraded，标记 shouldReevaluate
  - L1 成功率 80-95% + trend=degrading + 连续 3 个桶恶化 → degraded
  - L1 成功率 > 95% + trend=degrading → 保持 healthy，但在 UI 显示"质量下降趋势"
  - L1 成功率正常 + 当前处于 peakHours → 保持 healthy，但 SpeedTier 临时降档
```

#### 更新频率

```text
时间桶写入：每次请求后增量更新 15 分钟桶（内存聚合，桶结束时刷 SQLite）
趋势计算：每 15 分钟（桶结束时触发），不额外查询
小时热力图：每小时更新一次（24 个 UTC 小时 × 过去 7 天的数据）
```

## 4. 核心组件设计

### 4.1 组件总览

```text
┌──────────────────────────────────────────────────────────────┐
│                       Channel Autopilot                       │
│                                                              │
│  ┌───────────┐  ┌───────────┐  ┌──────────────────────────┐ │
│  │ Discovery  │  │ Profiler  │  │ HealthAnalyzer           │ │
│  │ (协议发现  │→│ (画像生成  │→│ (健康诊断/分层)           │ │
│  │  模型发现) │  │  能力推导) │  │                          │ │
│  └───────────┘  └───────────┘  └──────────┬───────────────┘ │
│        │              │                    │                 │
│        │              │         ┌──────────▼───────────┐     │
│        │              │         │ QualityTrendMonitor  │     │
│        │              │         │ (时序趋势/忙闲检测)  │     │
│        │              │         └──────────┬───────────┘     │
│        │              │                    │                 │
│        └──────────────┴────────────────────┘                 │
│                       ▼                                      │
│              ┌─────────────────┐                             │
│              │  ProfileStore   │ ← SQLite + 内存缓存         │
│              │  (endpoint 级)  │   + TimeBucket 指标         │
│              └─────────────────┘                             │
│                       ▼                                      │
│              ┌─────────────────┐                             │
│              │  SmartRouter    │ ← 注入 CandidateFilter      │
│              │ (任务分类→标签   │                              │
│              │  匹配→排序)     │                              │
│              └─────────────────┘                             │
│                       ▼                                      │
│              ┌─────────────────┐                             │
│              │ Scheduler       │ (现有，不修改核心链路)       │
│              │ SelectChannel   │                              │
│              └─────────────────┘                             │
└──────────────────────────────────────────────────────────────┘
```

### 4.2 Discovery — 协议与模型发现

**职责**：对每个 KeyEndpoint 独立探测协议、发现模型、推荐映射。

**⚠️ 关键：探测粒度是 endpoint 级，不是 channel 级**

同一 channel 的不同 key 可能属于不同分组，返回不同模型列表。必须逐 endpoint 探测。

**触发时机**：
1. 渠道首次添加（`autoManaged: true` 时）
2. 手动触发「重新发现」
3. 定时刷新（每天一次，但逐 endpoint 轮转，不并发）
4. **分组变更检测触发**（model_not_found 或模型列表哈希变化）

**流程**：

```text
添加渠道(baseURL + key[])
  │
  ├─ 遍历每个 (baseURL, apiKey) 对：
  │   │
  │   ├─ 1. 协议探测：对单个 endpoint 探测 messages/responses/chat/gemini
  │   │     └─ 复用 capability_test_runner.executeModelTest
  │   │
  │   ├─ 2. 模型发现：GET /v1/models（用该 key）
  │   │     └─ 复用 channel_discovery.discoverTransientModels
  │   │     └─ 失败时用内置候选模型列表回退
  │   │
  │   ├─ 3. 分组变更检测：对比模型列表哈希
  │   │     └─ GroupChangeDetector.CheckGroupChange
  │   │     └─ 如果变更 → 标记 stale，重新生成 ModelProfile
  │   │
  │   ├─ 4. 模型选择：从发现的模型中选 Strong/Primary/Fast 三档
  │   │     └─ 复用 channel_discovery.selectDiscoveryModels
  │   │
  │   ├─ 5. 能力探测：对选中模型做硬失败检测（HTTP 错误/解析失败）
  │   │     └─ 复用 channel_discovery.runDiscoveryToolCallProbe
  │   │     └─ 复用 channel_discovery.runDiscoveryVisionProbe
  │   │
  │   ├─ 6. 映射推荐：根据协议类型生成该 endpoint 的 modelMapping
  │   │
  │   └─ 7. 生成 KeyEndpointProfile + ModelProfile[]
  │
  └─ 8. 聚合：从所有 endpoint 画像生成 ChannelProfile
```

**endpoint 间差异处理**：

```text
场景：同一 channel 的 key-A 返回 [opus, sonnet, haiku]，key-B 返回 [gpt-5.5, gpt-5.4-mini]
  │
  ├─ KeyEndpointProfile-A: qualityTier=premium, models=[opus, sonnet, haiku]
  ├─ KeyEndpointProfile-B: qualityTier=premium, models=[gpt-5.5, gpt-5.4-mini]
  │
  └─ ChannelProfile:
       - qualityTier = premium（取最佳 endpoint）
       - EndpointInconsistencies = [{"models", "key-A: Claude系列, key-B: GPT系列"}]
       - UI 显示：⚠️ 不同 Key 提供的模型系列不同
```

**与现有 Channel Discovery 的关系**：

现有 `POST /channel-discovery` 是一个"预览"接口，返回推荐但不自动应用。Autopilot 复用其核心逻辑，但：
- **逐 endpoint 探测**，而非整个 channel 一次
- 自动写入每个 endpoint 的 `modelMapping`、`supportedModels`、兼容开关
- 自动生成 `KeyEndpointProfile` + `ModelProfile` 记录
- 对 `autoManaged` 渠道静默执行，对非 auto 渠道提供「建议应用」按钮

#### 4.2.1 内置模型清单 (Builtin Models Manifest)

**问题**：部分官方订阅 plan 的入口没有正确的 models 接口——要么不存在（404）、要么需要额外权限、要么返回与订阅实际可用模型不符的列表（典型：Claude 订阅 OAuth 入口、Codex plan 入口、部分官方企业网关）。对这些 baseURL 做模型发现会失败或产出错误画像。

**方案**：CCX 内置「已知官方 baseURL → 模型清单」映射表，命中时直接返回内置内容，完全跳过上游 models 接口：

```go
// backend-go/internal/config/builtin_models_manifest.go（生成式，随模型注册表更新）
type BuiltinModelsManifest struct {
    BaseURLPattern string   `json:"baseUrlPattern"` // 精确 host 或前缀匹配，如 "api.anthropic.com"
    ServiceType    string   `json:"serviceType"`    // messages/responses/…
    PlanHint       string   `json:"planHint,omitempty"` // 订阅类型提示（如 "claude_subscription"）
    ModelIDs       []string `json:"modelIds"`       // 该入口实际可用的模型清单
    DisableProbe   bool     `json:"disableProbe"`   // true 时 Discovery 跳过 GET /v1/models
}
```

**两个生效位置**：

```text
1. Discovery（§4.2 流程第 2 步之前）：
   baseURL 命中 manifest 且 DisableProbe=true
     → 跳过 GET /v1/models，直接用 ModelIDs 生成 ModelProfile[]
     → ProfileSource = "builtin_manifest"，不再因 models 接口失败而误判 misconfigured

2. /v1/models 代理响应（现有 Models API）：
   客户端向 CCX 请求 /v1/models 时，聚合结果中该渠道的贡献
   直接取内置清单，不向上游发起 models 请求
   （渠道显式配置了 supportedModels 时仍以用户配置优先）
```

**维护与覆盖顺序**：

```text
渠道显式 supportedModels（用户手动） > 内置 manifest > 上游 models 接口实探 > 内置候选模型回退
```

manifest 与模型注册表同源生成（`generated_model_registry.go` 同级），新模型发布时随注册表一起更新；HealthAnalyzer 对命中 manifest 的 endpoint 不将「models 接口 404/403」计入 misconfigured 信号。

### 4.3 Profiler — 画像生成器

**职责**：综合模型注册表、探测结果、运行时指标，生成 ChannelProfile 和 ModelProfile。

**推导规则**：

#### QualityTier 推导

```text
优先级 1：模型注册表 BuiltinUpstreamModelCapabilities 中的模型族
  claude-opus-* / gpt-5.5 / gpt-5.4     → premium
  claude-sonnet-* / gpt-5.3-codex        → high
  claude-haiku-* / gpt-5.4-mini          → normal
  其他                                    → low

优先级 2：渠道级 LowQuality 标记
  lowQuality: true → 最高 normal

优先级 3：capability-test 探测质量
  探测响应长度 > 100 tokens 且无截断 → 保持注册表推导
  探测失败或空响应                   → 降一档
```

#### StabilityTier 推导

```text
基于最近 1 小时指标：
  成功率 >= 95% 且 429 率 < 5%    → stable
  成功率 >= 80% 且 429 率 < 20%   → normal
  其他                            → unstable

额外信号（任一命中则降级）：
  连续失败 >= 5 次                → 最高 normal
  熔断器 open                     → unstable
  最近成功 > 6 小时前             → unstable
```

#### SpeedTier 推导

```text
基于最近 100 次请求的 p95 首 token 延迟：
  < 500ms   → fast
  < 2000ms  → normal
  >= 2000ms → slow

冷启动：无足够数据时用 capability-test 的 ProbeLatencyMs
  < 800ms   → fast
  < 3000ms  → normal
  >= 3000ms → slow
```

#### CostTier 推导

```text
优先级 1：用户手动成本画像
  APIKeyConfig.GroupMultipliers + RechargeMultiplier → 计算真实有效成本
  costHint                                           → 仅作为无价格数据时的粗粒度 fallback

优先级 2：模型注册表中的 Pricing 字段
  Input/Output 都是 0             → free
  EffectiveInput < $1/M 且 EffectiveOutput < $5/M   → cheap
  EffectiveInput < $10/M 且 EffectiveOutput < $30/M → normal
  其他                            → expensive

优先级 3：运行时行为启发（低置信度）
  频繁 429 且无 Retry-After      → 可能是免费/低配额，标记 cheap
  频繁 402/余额不足              → 有成本，标记 normal
```

#### EffectiveCost 推导

只用 `CostTier` 不足以做省钱调度。Autopilot 需要在满足能力和质量下界后，按 endpoint 的真实有效成本排序：

```text
baseInputCost  = ModelPricing.InputCacheMissPrice
baseOutputCost = ModelPricing.OutputPrice

groupMultiplier = matchGroupMultiplier(requestModel, APIKeyConfig.GroupMultipliers, default=1.0)
rechargeMultiplier = APIKeyConfig.RechargeMultiplier
if rechargeMultiplier <= 0:
    rechargeMultiplier = 1.0

effectiveMultiplier = groupMultiplier / rechargeMultiplier
effectiveInputCost  = baseInputCost  * effectiveMultiplier
effectiveOutputCost = baseOutputCost * effectiveMultiplier
```

例子：

```text
claude-opus 官价 input=$15/M, output=$75/M
key-A: groupMultiplier=0.8, rechargeMultiplier=1.0 → effective=0.8x
key-B: groupMultiplier=1.0, rechargeMultiplier=2.0 → effective=0.5x

在二者健康、上下文、能力和质量都满足时，优先 key-B。
```

约束：
- 成本优化只能在 `CapabilityFloor`、`QualityTier`、上下文窗口、vision/tool/reasoning 需求全部满足之后执行。
- 不允许为了省钱把 supervisor / premium 请求降级到不满足 `MinQualityTier` 的模型或 endpoint。
- 对低置信度成本画像，只能作为 tie-breaker，不能覆盖显式质量策略。

#### 能力标签推导

**⚠️ 原则：只做硬失败检测，不判定软质量问题**

识图/工具/reasoning 的"虚标"（渠道声称支持但实际返回垃圾）可靠判定很难。策略是：

- **硬失败**（可自动检测）：调用报错、格式错误、HTTP 错误码、解析失败
- **软质量问题**（留给人工）：答非所问、内容质量低、thinking 输出无意义

```text
SupportsVision：
  ── 硬条件（自动判定）──
  1. 注册表 Capabilities["vision"] == true
  2. 且 NoVision != true
  3. 且 (NoVisionModels 不含该模型 || VisionFallbackModel 已设置)
  ── 可选验证（L3 探测）──
  4. vision probe 返回 HTTP 200 且响应可解析（不要求内容质量）
  5. 如果 probe 返回 400/415/unsupported → 明确标记 SupportsVision=false

SupportsToolCalls：
  ── 硬条件（自动判定）──
  1. 注册表 Capabilities["toolCalls"] == true
  ── 可选验证（L3 探测）──
  2. tool_call probe 返回 HTTP 200 且响应含合法 tool_use block
  3. 如果 probe 返回 400/tool_not_found → 明确标记 SupportsToolCalls=false

SupportsReasoning：
  ── 硬条件（自动判定）──
  1. 注册表 ThinkingMode 非空
  ── 可选验证（L3 探测）──
  2. reasoning probe 返回 HTTP 200 且响应含 thinking/reasoning block
  3. 如果 probe 返回 400/thinking_not_supported → 明确标记 SupportsReasoning=false

SupportsLongCtx：
  1. ContextWindowTokens >= 200_000（来自注册表，无需探测）
  2. 或注册表 Supports1M == true
```

**虚标处理**：如果 L1 被动信号显示某渠道的 vision/tool/reasoning 请求**成功但用户标记为"结果差"**，系统不自动关闭标签，而是在 UI 上显示「⚠️ 用户反馈能力可能不准确」，允许人工 override。这避免了系统在"质量差"和"不支持"之间误判。

#### 识图能力分层与向现有代码的过渡

现有代码中识图能力由渠道级手动配置控制（`NoVision`、`NoVisionModels`、`VisionFallbackModel`），Autopilot 逐步引入自动画像，两者按优先级共存：

```text
Layer 0 — 手动显式配置（现有，始终最高优先级）
  NoVision=true          → SupportsVision 强制为 false，自动画像不覆盖
  NoVisionModels=[...]   → 指定模型不支持视觉，其余模型仍可走自动画像
  VisionFallbackModel    → 主模型无视觉时的回退，Phase 1 继续生效

Layer 1 — 自动画像 shadow（Phase 1，只展示不调度）
  endpoint 级 SupportsVision 由 L3 probe 生成，写入 KeyEndpointProfile
  NoVision=nil 且注册表 Capabilities["vision"]=true → shadow 展示建议值
  调度仍走现有 shouldReserveVisionChannelForText watermark 逻辑

Layer 2 — 自动画像影响 endpoint 排序（Phase 2）
  EndpointAttemptPolicy 使用 KeyEndpointProfile.SupportsVision 过滤候选
  NoVision=nil 的渠道：优先使用画像值；画像置信度 < 0.7 时回退 Layer 0 规则
  shouldReserveVisionChannelForText 继续保护视觉渠道不被文本请求耗尽

Layer 3 — 完全自动（Phase 3）
  SupportsVision 由持续 L1+L3 信号维护，NoVision 降级为人工 override 入口
  VisionFallbackModel 可由 ModelResolver 自动选取满足视觉下界的备选模型
```

兼容规则（落地时必须测试）：

```text
NoVision=true                → SupportsVision 永远 false，忽略所有画像
NoVision=false（显式关闭）   → SupportsVision 信任用户，画像不覆盖
NoVision=nil（未配置）       → Phase 1 用注册表+probe；Phase 2+ 用画像
NoVisionModels 包含请求模型  → 该模型 SupportsVision=false，不影响其他模型
画像置信度 < 0.7             → 回退到注册表 Capabilities["vision"] 判断
probe 返回 400/415           → 标记 SupportsVision=false，置信度=0.9
probe 成功但内容不可解析      → 不更新画像，保持现有状态
```

### 4.4 HealthAnalyzer — 健康诊断器

**职责**：持续分析渠道健康，生成 HealthState 和证据。

**⚠️ 核心原则：被动优先 (Passive-First)**

30-40 渠道 × 多模型的主动探测有 quota 成本，且白嫖渠道本身就抖。诊断信号分三层，成本递增：

| 层级 | 信号来源 | 成本 | 频率 | 适用场景 |
|------|---------|------|------|---------|
| L1 被动信号 | 真实请求的 MetricsManager | 零 | 实时/每次请求 | **默认层**，所有健康判定的主要依据 |
| L2 轻量探测 | 单模型 ping（最小 prompt） | 极低 | cooldown 复测 | L1 无数据（新渠道/长时间无请求） |
| L3 深度探测 | capability-test（多模型多协议） | 中 | 手动/每天 | 新渠道首次画像、用户主动触发、misconfigured 修复后 |

**分析周期**：
- L1 被动：每次请求后增量更新（复用 MetricsManager 已有的 RecordSuccess/RecordFailure）
- L1 聚合：每 5 分钟做一次滑动窗口聚合，更新 ChannelProfile
- L2 探测：仅在以下条件触发：
  - 渠道状态为 `unknown` 且添加超过 10 分钟
  - `limited`/`dead` 的 cooldown 到期
  - L1 数据不足（最近 1 小时请求数 < 3）
- L3 深度：仅在以下条件触发：
  - 用户手动点击「重新探测」
  - 新渠道 `autoManaged` 首次添加
  - `misconfigured` 状态修复后

**被动信号指标**（全部来自 MetricsManager 现有数据，无需额外请求）：

```text
成功/失败率     → MetricsManager.CalculateChannelFailureRateMultiURL
429 率         → FailureClass=overloaded 计数 / 总请求数
断流率         → ChannelLog.Status="streaming" 但无 "completed" 的比率
空响应率       → ChannelLog 中 DurationMs > 0 但 usage.InputTokens=0 的比率
p95 延迟       → ChannelLog.DurationMs 的 p95 分位
连续失败       → MetricsManager 滑动窗口 consecutiveFail
最后成功时间   → MetricsManager.GetChannelAggregatedMetrics.LastSuccessAt
熔断器状态     → MetricsManager.GetChannelCircuitStateMultiURL
Key 健康       → DisabledAPIKeys 数量 vs 总 Key 数量
```

**诊断逻辑**（见第 6 章详细设计）。

### 4.5 RateLimitDiscoverer — 上游限额自动发现

**职责**：在用户未显式配置 `rateLimitRpm` / key 级 `rateLimitRpm` 等限额时，为 endpoint 推导保守的运行态 RPM/TPM/RPD/并发限额，减少 429 和上游账号池冷却。

核心原则：
- **显式配置永远优先**：只要 channel 或 `APIKeyConfig` 设置了 `RateLimitRPM`，自动发现只做展示，不覆盖 limiter。
- **被动优先**：优先从真实响应头学习，不主动压测上游。
- **保守收敛**：无明确 header 时使用 AIMD（additive increase, multiplicative decrease）估算，只下调快、上调慢。
- **endpoint 粒度**：学习结果绑定 `metricsKey`，必要时映射到 key/quota scope limiter；不能只写 channel 级限速。

#### 4.5.1 信号来源

```text
优先级 │ 来源                         │ 动作
───────┼──────────────────────────────┼────────────────────────────────────
1      │ 用户显式 RateLimitRPM        │ 直接使用，自动发现不覆盖
2      │ x-ratelimit-limit-requests   │ 计算窗口内请求上限，换算 RPM/RPD
3      │ x-ratelimit-limit-tokens     │ 计算窗口内 token 上限，换算 TPM
4      │ x-ratelimit-remaining/reset  │ 估算当前消耗速度与重置窗口
5      │ anthropic-ratelimit-*        │ 同上，按 reset 时间换算 request/token limit
6      │ 并发/队列/断流信号            │ 降低 MaxConcurrent，再观察 RPM/TPM
7      │ Retry-After + 429/5xx        │ 进入 cooldown，并降低估算 limit
8      │ 无 header 的 429 比率        │ AIMD 降低估算 limit
9      │ 长时间成功且低延迟           │ AIMD 缓慢提高估算 limit
```

现有 `ratelimit.ChannelLimiter.ApplyUpstreamHints` 已能解析 `Retry-After`、Anthropic remaining/reset、OpenAI remaining/reset 并施加 cooldown。Autopilot 在此基础上新增“可解释的运行限额推导”，不要复制现有 cooldown 逻辑。

#### 4.5.2 学习状态

```go
// backend-go/internal/autopilot/rate_limit_discovery.go

type RateLimitProfile struct {
    ChannelUID   string    `json:"channelUid"`
    ChannelKind  string    `json:"channelKind"`
    MetricsKey   string    `json:"metricsKey"`
    Scope        string    `json:"scope"` // channel | key:<id> | quota:<id>
    EstimatedRPM int       `json:"estimatedRpm"`
    EstimatedTPM int       `json:"estimatedTpm,omitempty"`
    EstimatedRPD int       `json:"estimatedRpd,omitempty"`
    WindowSeconds int      `json:"windowSeconds"`
    MaxConcurrent int      `json:"maxConcurrent,omitempty"`
    Source       string    `json:"source"` // manual | header | passive_aimd
    Confidence   float64   `json:"confidence"`
    Last429At    *time.Time `json:"last429At,omitempty"`
    UpdatedAt    time.Time `json:"updatedAt"`
}
```

存储：
- 可放入 `KeyEndpointProfile.profile_json`；如果后续需要历史趋势，再独立建 `rate_limit_profiles` 表。
- 不写明文 key，只通过 `metricsKey` 和 limiter scope 关联。

#### 4.5.3 推导规则

```text
header 明确给 limit:
  estimatedRPM = normalize(limit, reset/window)
  confidence = 0.9

只有 remaining/reset:
  observedRate = requests_since_last_reset / elapsed
  estimatedRPM = min(current_estimate, inferred_window_capacity)
  confidence 逐次成功解析后提升，最高 0.75

429 + Retry-After:
  cooldownUntil = now + Retry-After
  estimatedRPM = max(minRPM, floor(current_estimate * 0.5))
  confidence = max(confidence, 0.7)

429 无 Retry-After:
  estimatedRPM = max(minRPM, floor(current_estimate * 0.7))
  confidence = max(confidence, 0.5)

连续稳定成功:
  每 10 分钟最多 +10%，且需要最近 15 分钟无 429
```

默认边界：
- `minRPM=1`，防止估算为 0 后永久不可用。
- `maxAutoRPM` 默认不超过 120，除非 header 明确给出更高 limit；避免自动学习把免费站打爆。
- `maxAutoTPM`、`maxAutoRPD`、`maxAutoConcurrent` 也是硬上限；没有明确 header 时不能超过配置上限。
- 对流式请求同时学习 `MaxConcurrent`：若出现频繁排队、断流或 429，先降并发，再降 RPM。

#### 4.5.4 应用到运行态 limiter

```text
if manual channel/key RateLimitRPM/TPM/RPD/MaxConcurrent > 0:
    使用手动配置；profile 只展示 header/passive 发现值
else if discovered EstimatedRPM > 0 and confidence >= threshold:
    将 discovered RPM/TPM/RPD/MaxConcurrent 作为 runtime limiter 配置
else:
    保持现有默认；只依赖 Retry-After cooldown 和负载软跳过
```

落地时不要自动写入 `config.json`。UI 可以显示“建议设置 RPM=xx”，由用户一键采纳；运行态 limiter 可以使用 profile 中的估算值，服务重启后从 ProfileStore 恢复。

### 4.6 SmartRouter — 智能路由注入

**职责**：根据请求画像 + 渠道画像生成一次请求的路由计划。路由计划分两层：

1. **Channel 层**：通过 `CandidateFilter` 过滤/重排 channel。
2. **Endpoint 层**：通过 `EndpointAttemptPolicy` 过滤/重排同一 channel 内的 baseURL + key，并提供 endpoint 级模型覆盖。

只靠 `CandidateFilter` 不足以实现 endpoint 级画像，因为现有 `CandidateFilterFunc` 只能返回 `[]scheduler.ChannelInfo`，而实际 baseURL/key 选择发生在 `common.TryUpstreamWithAllKeys` 内部。

#### 4.6.1 请求路由计划

```go
// backend-go/internal/autopilot/smart_router.go

type RequestRoutingPlan struct {
    RequestProfile *RequestProfile
    CandidateFilter scheduler.CandidateFilterFunc
    EndpointPolicy  *EndpointAttemptPolicy
}

type SmartRouter struct {
    profileStore  *ProfileStore
    modelResolver *ModelResolver
    intentStore   *ManualRoutingIntentStore
    config        *SmartRoutingConfig
}

// BuildPlan 为每次请求构建完整路由计划。
func (r *SmartRouter) BuildPlan(profile *RequestProfile) *RequestRoutingPlan {
    return &RequestRoutingPlan{
        RequestProfile:  profile,
        CandidateFilter: r.buildCandidateFilter(profile),
        EndpointPolicy:  r.buildEndpointPolicy(profile),
    }
}
```

#### 4.6.2 EndpointAttemptPolicy

```go
// backend-go/internal/autopilot/endpoint_policy.go

type EndpointCandidate struct {
    ChannelUID          string
    ChannelKind         string
    MetricsKey          string
    BaseURL             string
    KeyMask             string
    MappedModel         string
    MappedEffort        EffortLevel  // 自动映射的 effort 级别（空=未决定）
    MappedEffortDecided bool         // effort 是否由 Autopilot 决定
    EndpointUID         string       // 稳定 endpoint ID
    Score               float64
    Reason              string
}

type EndpointAttemptPolicy struct {
    RequestModel string
    ByChannelUID map[string][]EndpointCandidate // 已按优先级排序
    FailOpen     bool                           // true: 无画像时回退现有 key/baseURL 轮转

    // ResolvedModelByEndpointUID（旧 API）：仅返回模型名，向后兼容。
    // ResolvedTargetByEndpointUID（新 API）：返回 model + effort 原子决策。
    // 两者从同一份 targetByUID 派生，避免两个真值源。
    ResolvedModelByEndpointUID  func(endpointUID string) string
    ResolvedTargetByEndpointUID func(endpointUID string) *ResolvedRouteTarget
}
```

执行契约：
- `CandidateFilter` 只负责 channel 级顺序，不直接选择 key/baseURL。
- `EndpointAttemptPolicy` 通过新的 `common.TryUpstreamOption` 注入 `TryUpstreamWithAllKeys`。
- `TryUpstreamWithAllKeys` 在进入 baseURL/key 双层循环前，先按 policy 过滤和重排 `urlResults`。
- `selectAttemptAPIKey` 需要新增 policy-aware 分支：在 `keypool.CandidatesForModel` 结果上应用 endpoint 候选顺序、健康状态、FastDecay 分数和模型下界。
- 若 policy 对当前 channel 没有候选且 `FailOpen=true`，保持现有 failover 行为；若 `FailOpen=false`，跳过该 channel。

建议的最小接口：

```go
// backend-go/internal/handlers/common/upstream_failover.go

func WithEndpointAttemptPolicy(policy *autopilot.EndpointAttemptPolicy) TryUpstreamOption
```

这样可以复用现有 `TryUpstreamWithAllKeys` 的熔断、限流、拉黑、日志、URL warmup 行为，只在候选排序和模型覆盖点插入 autopilot。

#### 4.6.2a TryUpstreamWithAllKeys 改造细节

**改造目标**：最小侵入地将 `EndpointAttemptPolicy` 注入现有双层循环，不破坏熔断/限流/拉黑/warmup 等现有保护行为。

改造后的执行顺序（baseURL × key 双层循环内）：

```text
原始 urlResults（按配置顺序）
  │
  ├─ 1. [NEW] policy.FilterURLs(urlResults)
  │       → 移除画像中 HealthState=dead 的 baseURL
  │       → FailOpen=true 时：过滤后为空则回退全量 urlResults
  │
  ├─ 2. [NEW] policy.SortURLs(filtered)
  │       → 按 EndpointCandidate.Score 降序排列 baseURL
  │
  └─ 遍历每个 baseURL：
       │
       ├─ 3. [EXISTING] URL warmup 检查（不变）
       ├─ 4. [EXISTING] keypool.CandidatesForModel(baseURL, model)
       │
       ├─ 5. [NEW] policy.FilterKeys(baseURL, candidates)
       │       → 移除画像中 FastDecay.EffectiveScore < threshold 的 key
       │       → 移除处于 cooldown 中的 key（已有逻辑，policy 可追加条件）
       │       → FailOpen=true 时：过滤后为空则回退全量 candidates
       │
       ├─ 6. [NEW] policy.SortKeys(baseURL, filtered)
       │       → 按 EndpointCandidate.Score 降序排列 key
       │       → 同分时：成本低的优先，信任等级高的作为 tie-breaker
       │
       └─ 遍历每个 key：
            ├─ 7. [EXISTING] 熔断、限流、拉黑检查（不变）
            ├─ 8. [EXISTING] 发起上游请求（不变）
            ├─ 9. [NEW] 请求成功 → NotifySuccess(baseURL, key) → 更新亲和
            └─ 10.[NEW] 请求失败 → NotifyFailure(baseURL, key) → 更新 FastDecay
```

`MappedModel` 的注入点在步骤 8 之前：

```go
// 如果 policy 对当前 (baseURL, key) 提供了 MappedModel，替换请求体中的 model 字段
if candidate := policy.GetCandidate(baseURL, key); candidate != nil && candidate.MappedModel != "" {
    requestBody.Model = candidate.MappedModel
    // ChannelLog 中记录 originalModel 和 mappedModel
}
```

**FailOpen 保证**：任何 policy 操作出错（panic recover、nil policy、store 读取失败），立即退出 policy 路径，回到原有顺序遍历，不中断请求。

#### 4.6.3 与现有手动控制的优先级

SmartRouter 不能破坏用户显式控制：

```text
最高优先级：ManualRoutingIntent（model_trial/channel_trial/endpoint_trial/session_pin）
显式控制：X-Channel 指定渠道、手动序列 override、promotion
中间层级：SmartRouter channel 重排/过滤
底层约束：协议/鉴权/上下文/vision/tool/reasoning、熔断、限速、key/baseURL failover
```

当前 `CandidateFilter` 在 `SelectChannelWithOptions` 中执行得早于 X-Channel、manual override、promotion。Phase 2 落地时必须二选一：

1. 调整 `SelectChannelWithOptions` 的阶段顺序，让显式控制先定位候选，再让 SmartRouter 只处理剩余默认调度。
2. 保持现有顺序，但给 `CandidateFilter` 传入 `ProtectedChannels`，SmartRouter 对受保护 channel 只能降权，不能过滤。

推荐方案 1，行为更符合“显式用户意图优先”。

#### 4.6.4 人工意图执行语义

`ManualRoutingIntent` 不是普通 priority，也不是永久 modelMapping。它是短期路由补丁：

```text
请求进入
→ 解析 request/session/model/agent role
→ 查找匹配的 active ManualRoutingIntent
→ 若命中：
    - 构建 protected channel/endpoint/model candidate
    - 仍执行硬约束校验
    - 按 TTL/MaxRequests/MaxEstimatedCost 扣减预算
    - 请求失败且 FallbackOnFailure=true 时回到默认 Autopilot plan
→ 若未命中：走默认 SmartRouter
```

场景规则：
- **测试 `fable-5`**：创建 `model_trial`，指定 `model=fable-5`，可选指定 `channelUid/metricsKey`；模型未知时只允许 request-scoped 透传或 mappedModel 试用，不自动写入全局 `modelMapping`。
- **先用某个公益站**：创建 `channel_trial` 或 `endpoint_trial`，限制 `taskClasses=[worker, lightweight]`、`trafficPercent`、`expiresAt` 和 `MaxRequests`；成功后用户可手动推广为常规策略。
- **会话级排障**：创建 `session_pin`，只影响 `sessionId`，用于确认某个渠道是否兼容当前客户端。
- **主代理保护**：默认不允许 third-tier 公益站 trial 覆盖 supervisor，除非用户在 UI 明确选择主代理试用并设置短 TTL。

观测要求：
- 路由 trace 必须标注 `intentUid`、命中原因、预算剩余、是否 fallback。
- trial 产生的画像单独标注 `manual_trial`，驾驶舱显示试用结果摘要。
- 试用结束后不自动改变长期策略；只给出「保存为 modelMapping」「提升渠道权重」「加入常规池」等显式操作。

#### 4.6.5 与现有 CandidateFilter 的兼容

现有 handler 自有 filter（例如 vectors 的 embedding 维度过滤）必须与 SmartRouter 组合，而不是互相覆盖：

```text
active/model/context 过滤
→ protected manual controls
→ SmartRouter CandidateFilter（channel 粗筛/重排）
→ handler CandidateFilter（协议/业务硬约束）
→ priority/affinity/promotion fallback
→ TryUpstreamWithAllKeys + EndpointAttemptPolicy（endpoint 细选）
```

如果某个 handler 已经传入 `CandidateFilter`，公共 failover 外壳需要提供 `ComposeCandidateFilters`，按顺序合并多个 filter。

### 4.7 TrustedRoutingAdvisor — 可信模型路由辅助

**职责**：使用一等官方 API/token plan 或本地模型为 SmartRouter 提供隐私边界内的路由 hint。它不是最终裁判。

允许的 advisor 执行面：
- `OriginTier=first`：官方 API / 官方 token plan，可用于隐私敏感的路由判定。
- `OriginTier=local`：用户自托管本地运行时，可用于隐私敏感的路由判定。

禁止的 advisor 执行面：
- `OriginTier=second`：中转站可以执行用户明确发起的模型请求，但不能接收额外上下文做路由判定。
- `OriginTier=third`：公益站可以作为低风险候选执行上游，但不能作为 routing advisor。
- `OriginTier=unknown`：未知来源只允许观察，不参与 AI 判定。

支持的本地运行时：
- `ollama`：优先走 OpenAI-compatible 接口；不支持时用 native adapter。
- `lmstudio`：按 OpenAI-compatible `/v1` adapter 接入。
- `llama_server`：按 OpenAI-compatible `/v1` adapter 接入。
- `openai_compatible`：用户自定义本地 baseURL。

#### 4.7.1 讨论结论转成设计约束

用户贴出的讨论里，核心争议是：用较弱模型判断“哪些子任务只有 Fable 做得到”本身接近强模型能力，弱模型容易不知道自己不知道什么；而 Fable 的优势常来自全局 context 和长链推理，这类任务通常不适合自动切分再合并。

因此本方案不做“弱模型动态拆任务省 Fable”的通用机制。务实策略是：
- 人凭经验预先定义任务类别和路由规则，工具负责执行路由。
- advisor 只判断**已定义分类**，例如“日志分类/标题/格式化/简单摘要/低风险 worker”。
- advisor 只能在高置信、低风险、可回退场景做 demotion 建议；不让它判定强模型“不需要”。
- 对架构设计、跨文件重构、长上下文综合、模糊需求澄清、关键业务决策，默认保持 high/premium 路由。
- 任何自动切分-合并必须来自用户显式模板；没有模板时不自动把全局任务拆给本地模型。

#### 4.7.2 RoutingHint 契约

```go
type TrustedRoutingHint struct {
    TaskClass TaskClass `json:"taskClass"`
    ComplexityTier string `json:"complexityTier"` // trivial | routine | complex | unknown
    SuggestedMinQualityTier QualityTier `json:"suggestedMinQualityTier"`
    AllowLocalCandidate bool `json:"allowLocalCandidate"`
    AllowCheapRemote bool `json:"allowCheapRemote"`
    NeverDemote bool `json:"neverDemote"`
    Confidence float64 `json:"confidence"`
    Reasons []string `json:"reasons"`
}
```

执行规则：
- `NeverDemote=true` 时，SmartRouter 不能因为成本或 advisor 建议降低质量下界。
- `AllowLocalCandidate=true` 只表示本地模型可进入候选集，仍需满足 context/tool/vision/reasoning 硬约束。
- `Confidence < threshold` 或 advisor 超时，忽略 hint，回到默认 SmartRouter。
- advisor 的 hint 先 shadow 记录，达到最小样本量和准确率后才能影响真实路由。
- advisor 执行面必须满足 `OriginTier in {first, local}`；否则拒绝创建或自动降级为 disabled。
- advisor 不能覆盖 `ManualRoutingIntent`、X-Channel、session pin、显式 modelMapping。

#### 4.7.3 Fable/高价模型用量优化

目标不是让 advisor 识别“只有 Fable 能做”，而是让系统可靠识别“不需要 Fable 的已知低风险任务”：

```text
固定高优先级保留给 Fable / premium：
  - 全局架构设计、复杂规划、跨模块一致性推理
  - 长上下文综合、需求不明确但风险高的任务
  - 失败代价高的代码修改、迁移、发布决策

可由本地/便宜模型优先尝试：
  - 日志分类、标题生成、格式转换、短摘要
  - 渠道健康证据归类、错误原因初筛
  - 明确模板化的配置解释、成本估算、简单路由标签

不确定：
  - 直接升级到 high/premium，而不是让本地模型猜
```

这样 advisor 优化的是”明显不用强模型”的部分，而不是把强模型任务拆碎后试图合并。

### 4.8 Endpoint 级亲和保持机制

**背景**：现有 `TraceAffinityManager` 实现了渠道级亲和（同一 trace 固定到同一 channel index）。Autopilot 引入 endpoint 级后，需要在 channel 内进一步稳定 `(baseURL, key)` 对的选择，避免每次请求都随机轮转 key。

#### 4.8.1 亲和粒度层级

```text
层级 1：Channel 亲和（现有，已实现）
  key: traceAffinityKey(kind, userID, contextBucket)
  value: channelIndex
  由 session.TraceAffinityManager 管理，含超时续期

层级 2：Endpoint 亲和（新增，Phase 2）
  key: endpointAffinityKey(kind, userID, contextBucket)
  value: metricsKey（= GenerateMetricsIdentityKey(baseURL, apiKey, serviceType)）
  在 TryUpstreamWithAllKeys 成功后由 autopilot 写入，失败后清除

两层独立管理：channel 亲和失效不自动清 endpoint 亲和；endpoint 亲和失效只在
同 channel 内重新选 key，不影响 channel 亲和。
```

#### 4.8.2 亲和选取与保持规则

```text
请求进入 TryUpstreamWithAllKeys：
  1. 查 endpoint 亲和 → 得到 preferred metricsKey
  2. 检查 preferred endpoint 的当前状态：
     ├─ 健康且未被 policy 过滤 → 提升到候选列表首位（不跳过其他候选）
     ├─ 处于 cooldown/熔断/拉黑 → 忽略亲和，走 policy 正常排序
     └─ key 被禁用 / 画像 HealthState=dead → 清除亲和记录，走正常排序

请求成功（任意 endpoint）：
  → SetEndpointAffinity(kind, userID, contextBucket, metricsKey)
  → UpdateTraceAffinity(kind, userID)（channel 层续期）

请求失败（该 endpoint）：
  → 不清除亲和，仍允许下次优先尝试（避免偶发失败导致频繁漂移）
  → 连续失败 ≥ 3 次 → ClearEndpointAffinity，下次走正常排序
```

#### 4.8.3 亲和失效条件

| 条件 | 行为 |
|------|------|
| key 被显式禁用 | 立即清除亲和，failover 到同 channel 其他 key |
| endpoint 进入 cooldown（429） | 忽略亲和，不清除；cooldown 结束后恢复优先 |
| 熔断器 open | 忽略亲和，不清除；半开后恢复优先 |
| 连续失败 ≥ 3 次（L1 被动） | 清除亲和，重新选 endpoint |
| channel 亲和漂移（failover 换 channel） | 清除旧 channel 的 endpoint 亲和 |
| 亲和 TTL 超时（默认 30min） | 自动清除，下次重新建立 |
| `session_pin` ManualRoutingIntent | 覆盖自动亲和，直到 intent 到期 |

## 5. 智能调度策略

### 5.1 任务分类 (TaskClassifier)

请求进入时自动分类，决定调度策略：

```go
// backend-go/internal/autopilot/task_classifier.go

func ClassifyRequest(profile *RequestProfile) TaskClass {
    // 1. 原生生图端点优先判定；不做 chat → images 协议转换
    if profile.ChannelKind == "images" || profile.ImageGenNeed {
        return TaskClassImageGen
    }

    // 2. 原生 embedding / vectors 端点优先判定
    if profile.ChannelKind == "vectors" || profile.EmbeddingNeed {
        return TaskClassEmbedding
    }

    // 3. 识图理解任务
    if profile.HasImage && profile.VisionNeed {
        return TaskClassVision
    }

    // 4. 长上下文任务
    if profile.ContextNeed > 200_000 {
        return TaskClassLongContext
    }

    // 5. 明确的低风险轻任务
    if isLightweightRequest(profile) {
        return TaskClassLightweight
    }

    // 6. 主代理/监工
    if profile.AgentRole == "main" || profile.AgentRole == "" {
        // 主代理默认走 Supervisor 策略
        return TaskClassSupervisor
    }

    // 7. 子代理
    if profile.AgentRole == "subagent" {
        // 子代理默认走 Worker 策略
        return TaskClassWorker
    }

    return TaskClassWorker // 兜底
}
```

`isLightweightRequest` 必须是确定性规则，不能让 advisor 自己发明分类：
- `Operation=count_tokens`、标题/分类/格式转换等已知模板。
- 上下文 < 10K、无图片、无工具调用、无 reasoning 需求。
- 模型名包含 `haiku` / `mini` / `flash` 只能作为弱信号，不能单独决定 lightweight。

### 5.2 调度策略矩阵

每个 TaskClass 对应一组优先级规则：

#### Supervisor（主代理/监工）

```text
优先级 1：qualityTier=high|premium + stabilityTier=stable + 长上下文
优先级 2：qualityTier=high + stabilityTier=normal
优先级 3：qualityTier=normal + stabilityTier=stable
同档排序：优先 stability，再按 estimatedRequestCost 从低到高
降级    ：qualityTier=high + stabilityTier=degraded（仅当无稳定高智商渠道时）
禁止    ：stabilityTier=unstable, costTier=free, qualityTier=low
```

#### Worker（子代理）

```text
硬约束  ：满足 CapabilityFloor + MinQualityTier，不允许质量降级省钱
优先级 1：estimatedRequestCost 最低 + stabilityTier>=normal
优先级 2：costTier=free|cheap + qualityTier=normal|high（临时池/白嫖池）
优先级 3：costTier=cheap + speedTier=fast
优先级 4：speedTier=fast + qualityTier=low|normal
优先级 5：qualityTier=normal + stabilityTier=stable（常规池）
默认跳过：costTier=expensive, qualityTier=premium
```

#### Lightweight（轻任务）

```text
优先级 1：estimatedRequestCost 最低 + speedTier=fast
优先级 2：speedTier=fast + costTier=free|cheap
优先级 3：costTier=free
优先级 4：speedTier=fast
禁止    ：qualityTier=premium, 视觉池, 长上下文池
```

#### Vision（识图任务）

```text
硬过滤  ：SupportsVision=true 且 SupportsToolCalls=true（如需要）
优先级 1：qualityTier=high|premium + vision
优先级 2：qualityTier=normal + vision
同档排序：estimatedRequestCost 从低到高，但不牺牲 vision/tool 硬约束
降级    ：当所有 vision 渠道不可用时，尝试 visionFallbackModel
禁止    ：SupportsVision=false 的渠道
```

#### ImageGeneration（原生生图任务）

```text
硬过滤  ：channelKind=images，支持请求的 operation（generation/edit/variation）和 model
优先级 1：healthState=healthy + stabilityTier>=normal + estimatedRequestCost 最低
优先级 2：speedTier=fast + costTier=cheap|normal
优先级 3：qualityTier=normal|high + 最近 15m 成功率高
参数处理：透传 images API 原生参数；只做必要校验和脱敏日志
禁止    ：把 chat/messages/responses 文本请求自动转换为 images 请求
```

MVP 不做“聊天模型画图”协议转换。用户如果要用某个上游的生图服务，应把它作为 `images` 类型渠道添加，Autopilot 负责发现、健康画像、限速和 endpoint 排序。

#### Embedding（原生向量任务）

```text
硬过滤  ：channelKind=vectors，支持请求模型和 handler 要求的 embedding 维度/格式
优先级 1：healthState=healthy + estimatedRequestCost 最低
优先级 2：stabilityTier=stable + p95LatencyMs 低
优先级 3：同模型/同维度历史成功率高
参数处理：透传 embeddings API 原生参数；不改写 input，不跨模型猜测维度
禁止    ：把 chat/messages/responses 文本请求自动转换为 embeddings 请求
```

#### LongContext（长上下文）

```text
硬过滤  ：ContextWindowTokens >= 请求需要的最小窗口
优先级 1：qualityTier=high|premium + longContext + stable
优先级 2：qualityTier=normal + longContext
同档排序：estimatedRequestCost 从低到高，但不牺牲上下文窗口
禁止    ：ContextWindowTokens < 需求 或 SupportsLongCtx=false
```

### 5.3 CandidateFilter 实现

```go
func (r *SmartRouter) filterByTaskStrategy(
    channels []scheduler.ChannelInfo,
    profiles map[int]*ChannelProfile,
    strategy taskStrategy,
) []scheduler.ChannelInfo {

    // 1. 硬过滤：排除不满足硬约束的渠道
    filtered := hardFilter(channels, profiles, strategy)

    // 2. 标签评分：每个渠道按策略规则打分
    scored := scoreChannels(filtered, profiles, strategy)

    // 3. 按分数降序排列
    sort.Slice(scored, func(i, j int) bool {
        return scored[i].Score > scored[j].Score
    })

    // 4. 返回重排后的 ChannelInfo 列表
    return scored.ToChannelInfoList()
}
```

**评分公式**：

```text
Score = w_quality * qualityScore
      + w_stability * stabilityScore
      + w_speed * speedScore
      + w_cost * costScore
      + w_savings * savingsScore
      + w_tier_match * tierMatchBonus
      + w_family * familyPreferenceScore   // 新增：模型派系偏好软约束
      + w_provider_quality * providerQualityScore  // 新增：同模型上游供应商质量差异
      + w_domain * domainStrengthScore     // 新增：任务域优势（审美/代码审核等强项差异）
      - penalty

其中：
  qualityScore:          low=1, normal=2, high=3, premium=4
  stabilityScore:        unstable=0, normal=1, stable=2
  speedScore:            slow=0, normal=1, fast=2
  costScore:             expensive=0, normal=1, cheap=2, free=3
  savingsScore:          normalizeCheapest(estimatedRequestCost)，越便宜越高，仅在满足硬约束后参与
  familyPreferenceScore: 见下方"模型派系偏好"章节
  providerQualityScore:  该endpoint对该模型的实现质量，0.0-1.0（1.0=最优实现）
  domainStrengthScore:   模型在当前任务域的优势分，0.0-1.0（0.5=中性），见 §5.7

  tierMatchBonus: 渠道画像标签匹配策略优先标签时 +10
  penalty:        healthState=degraded 时 -5, limited 时 -20；assist 下 dead/misconfigured 额外 -100 并保留在队尾

  权重根据 TaskClass 不同（w_family 统一为 0.2，w_provider_quality 统一为 1.0）：
  Supervisor:      w_quality=3, w_stability=2, w_speed=1, w_cost=0, w_savings=0.5, w_family=0.2, w_provider_quality=1.0
  Worker:          w_quality=1, w_stability=1, w_speed=2, w_cost=2, w_savings=3,   w_family=0.2, w_provider_quality=0.8
  Lightweight:     w_quality=0, w_stability=1, w_speed=3, w_cost=2, w_savings=3,   w_family=0.1, w_provider_quality=0.5
  Vision:          w_quality=2, w_stability=2, w_speed=1, w_cost=1, w_savings=1,   w_family=0.2, w_provider_quality=1.0
  ImageGeneration: w_quality=1, w_stability=2, w_speed=1, w_cost=2, w_savings=2,   w_family=0.1, w_provider_quality=0.3
  Embedding:       w_quality=0, w_stability=2, w_speed=2, w_cost=3, w_savings=3,   w_family=0.0, w_provider_quality=0.0
  LongContext:     w_quality=2, w_stability=2, w_speed=1, w_cost=0, w_savings=1,   w_family=0.2, w_provider_quality=1.0
```

**w_family 权重约束验证**：`w_stability × (stable=2 − unstable=0) = 2.0`，而 `w_family × max(familyPreferenceScore) ≤ 0.2 × 3 = 0.6`。因此 stable 的非偏好派系（+2.0）始终胜过 unstable 的偏好派系（+0.6），派系偏好只在同等稳定性下才决定顺序。

**w_provider_quality 权重约束验证**：`w_provider_quality × (1.0 − 0.0) = 1.0`，而 `w_stability × 2.0 = 2.0`。因此 stable 的低供应商质量实现仍优于 unstable 的高质量实现，供应商质量只在同等稳定性内打破平局。

**w_domain 权重约束验证**：`w_domain` 统一取 0.5（embedding/image_generation 取 0），`w_domain × (1.0 − 0.0) = 0.5 < w_stability × 2.0`。任务域优势只在同等稳定性/健康度内影响排序，不会让"审美好但不稳定"的渠道胜出。TaskDomain 无法细分时进入 `general`；有规范 Knowledge 基准则使用其软评分，缺少直接证据时仍取 0.5 中性值。

**ProviderQualityScore 推导规则**：

```text
L3 深度探测（默认来源）：
  探测 prompt：同一测试用例（固定长度、固定语言、固定复杂度）
  评分维度：
    响应完整性  40%：输出 token 数 vs 预期（完整回答 vs 截断）
    语义一致性  30%：与基线响应的语义相似度（cosine similarity 或 LLM-as-judge）
    格式正确性  15%：tool_use block 是否合法、reasoning block 是否存在
    首token延迟 15%：响应开始速度
  providerQualityScore = weighted_average(各维度得分)

用户反馈（高优先级 override）：
  用户在 UI 标记某个 endpoint 的某个模型"质量好"或"质量差"
  providerQualityScore = clamp(userScore, 0.0, 1.0)
  ProviderQualitySource = "user_feedback"，confidence = 0.9

启发式推导（低置信度兜底）：
  基于 OriginTier + 历史成功响应的平均长度/完整度
  official_api 默认 0.95，relay 默认 0.7，community 默认 0.5，unknown 默认 0.6
  ProviderQualitySource = "inferred"，confidence = 0.4

约束：
  - 置信度 < 0.5 时 providerQualityScore 不参与评分（视为0分）
  - 用户反馈覆盖探测结果，但用户反馈 TTL 为 30 天，过期后回退探测
  - 不同模型族的 ProviderQualityScore 独立评估（opus 在kiro的质量不等于sonnet在kiro的质量）
```

**手动 L3 第一版状态（2026-07-15）**：已实现 `pq-v1-20260715` 固定 JSON canary，覆盖
Messages、Chat Completions、Responses 与 Gemini 上游协议。调用方只能指定 `endpointUid`、
`modelId` 和 1～3 次重复数，不能注入 prompt。评分仍按 40% 完整性、30% 语义正确性、
15% 严格 JSON 格式、15% 延迟执行；第一版使用非流式完整响应延迟作为首 token 延迟代理，
后续引入流式采样时再替换该维度。单次有效样本 confidence=0.6，最多三次提升到 0.8；
失败样本按 0 分计入均值，全部失败不写画像。

安全边界：仅管理 API 手动触发，不启动自动 worker；当前进程按 UTC 日设置 12 次硬预算，
请求前原子预留，单次 90 秒超时、响应体上限 64 KiB。同一 endpoint×model 不并发执行；
单次请求最多允许 2048 个输出 token，以容纳推理模型的隐藏思考并限制最坏额度消耗。
对声明可控推理的模型固定采用最低可用 effort；Claude 兼容端点若只暴露
`reasoning_content` 而未声明 thinking 模式，则与 capability-test 一致显式关闭 thinking。
原始模型输出和明文 Key 既不落盘也不返回，只保存分数、canary 版本、置信度和脱敏判定。
`user_feedback` 仍高于 `probe`，自动发现重建 ModelProfile 时必须保留既有质量证据。
跨进程共享预算与 LLM-as-judge/语义向量评分仍属于后续阶段。

### 5.5 模型派系偏好 (Model Family Preference)

用户在不同派系的模型（Claude / GPT / Gemini / DeepSeek 等）之间有主观偏好，且偏好本身与渠道质量无关。本章定义派系偏好的数据模型、推导逻辑和排序语义。

#### 5.5.1 ModelFamily 枚举与推导

```go
// backend-go/internal/autopilot/model_family.go

type ModelFamily string

const (
    // ── 国际主流 ──
    ModelFamilyClaude    ModelFamily = "claude"     // claude-*，Anthropic
    ModelFamilyOpenAI    ModelFamily = "openai"     // gpt-*, o*, codex-*，OpenAI / Amazon Bedrock
    ModelFamilyGemini    ModelFamily = "gemini"     // gemini-*，Google
    ModelFamilyMistral   ModelFamily = "mistral"    // mistral-*, mixtral-*，Mistral AI

    // ── 国产主流 ──
    ModelFamilyDeepSeek  ModelFamily = "deepseek"   // DeepSeek V3/V4，DeepSeek
    ModelFamilyQwen      ModelFamily = "qwen"       // qwen3-*，通义千问，DashScope
    ModelFamilyGLM       ModelFamily = "glm"        // glm-5-*，智谱 AI
    ModelFamilyKimi      ModelFamily = "kimi"       // kimi-k2-*，月之暗面 Moonshot
    ModelFamilyMiMo      ModelFamily = "mimo"       // mimo-v2-*，小米
    ModelFamilyERNIE     ModelFamily = "ernie"      // ernie-4.5，百度
    ModelFamilyDoubao    ModelFamily = "doubao"     // doubao-seed-*，字节豆包 Volcengine
    ModelFamilyMiniMax   ModelFamily = "minimax"    // minimax-m*，MiniMax
    ModelFamilyYi        ModelFamily = "yi"         // yi-*，零一万物 01.ai
    ModelFamilyBaichuan  ModelFamily = "baichuan"   // baichuan-m*，百川智能
    ModelFamilyStep      ModelFamily = "step"       // step-3.*，阶跃星辰 StepFun
    ModelFamilySenseNova ModelFamily = "sensenova"  // sensenova-6.*，商汤 SenseTime
    ModelFamilyAgnes     ModelFamily = "agnes"      // agnes-2.*，Xiaomi 独立系列
    ModelFamilyLongCat   ModelFamily = "longcat"    // longcat-2.*，京东

    // ── 特殊 ──
    ModelFamilyLocal     ModelFamily = "local"      // ollama/lmstudio/llama-server 本地运行时
    ModelFamilyUnknown   ModelFamily = "unknown"
)

// InferModelFamily 从模型注册表 Provider 字段推导派系，注册表显式标注优先
// 同时按生成的 ModelProfile.ModelFamily 写入画像
func InferModelFamily(modelID string, registryCapabilities map[string]UpstreamModelCapability) ModelFamily {
    // 优先从 BuiltinUpstreamModelCapabilities 的 Provider 字段精确映射
    // Provider→ModelFamily 映射表（从 generated_model_registry.go 提取）：
    //   anthropic → claude
    //   openai / amazon-bedrock → openai
    //   dashscope → qwen
    //   volcengine → doubao
    //   xiaomi → mimo 或 agnes（按前缀细分）
    //   baidu → ernie
    //   01-ai → yi
    //   moonshot → kimi
    //   zai → glm
    //   其余 Provider 直接映射同名 family
    // 回退到模型 ID 前缀匹配（兜底未知模型）
}
```

**⚠️ 派系偏好不覆盖版本差异**：同一个 ModelFamily 内不同模型版本的 QualityTier 和能力可能完全不同。

```text
示例：familyPreferenceOrder = ["claude", "openai"]

Claude Opus 4.8 (claude, premium) vs Claude Haiku 4.5 (claude, normal)
  → 同一派系，但QualityTier 不同 → QualityTier 决定优先级，与派系偏好无关

Claude Opus 4.8 (claude, premium) vs GPT-5.4 (openai, premium)
  → 不同派系，同 QualityTier → familyPreferenceScore 决定，claude 得分更高

Claude Haiku 4.5 (claude, normal) vs GPT-5.4 (openai, premium)
  → 不同派系，不同 QualityTier → qualityScore 差距 > familyPreferenceScore → GPT-5.4 胜出
```

因此 `w_family × max_score ≤ 0.6`，而 `w_quality × (premium − normal) = 3.0`，版本间的质量差异总是压倒派系偏好。派系偏好只在同版本档次（同QualityTier）内打破平局。

#### 5.5.2 familyPreferenceScore 计算

```go
// 用户配置的派系偏好顺序，例如 ["claude", "openai", "gemini"]
// familyPreferenceScore：越靠前分越高，不在列表中得 0 分
func CalcFamilyPreferenceScore(family ModelFamily, prefs []ModelFamily) float64 {
    n := len(prefs)
    for i, f := range prefs {
        if f == family {
            return float64(n - i) // 第1位得n分，第2位得n-1分，…最后1位得1分
        }
    }
    return 0
}
```

**示例**：偏好顺序 `["claude", "openai", "deepseek"]`（n=3）

| 派系 | familyPreferenceScore | × w_family=0.2 | 对总分影响 |
|------|----------------------|----------------|-----------|
| claude | 3 | +0.6 | 同等条件下最优先 |
| openai | 2 | +0.4 | 次优先 |
| deepseek | 1 | +0.2 | 再次 |
| qwen / kimi / glm / doubao / mimo / er 等其余 13 个派系 | 0 | +0.0 | 无偏好加成 |

当两个 stable 的 premium 渠道并列，claude 比 openai 多 +0.2，决定顺序；但 stable openai 比 unstable claude 多 2.0 - 0.6 = +1.4，稳定性压倒派系偏好。

#### 5.5.3 按 TaskClass 独立配置

不同场景对派系的感知不同。推荐全局配置 + 任务级 override：

```json
"modelFamilyPreference": {
  "enabled": true,
  "weight": 0.2,
  "globalOrder": ["claude", "openai", "gemini"],
  "perTaskClass": {
    "supervisor":  ["claude", "openai"],
    "worker":      ["openai", "claude", "gemini", "deepseek"],
    "lightweight": ["openai", "gemini", "claude"],
    "vision":      ["claude", "openai", "gemini"]
  }
}
```

规则：
- `perTaskClass` 中存在的 TaskClass 使用其专属顺序；否则回退 `globalOrder`。
- `enabled=false` 时 `familyPreferenceScore` 恒为 0，评分退化为原有公式。
- `weight` 覆盖全局 `w_family`；可在管理面板调整（建议范围 0.1 ~ 0.5）。
- Embedding / ImageGeneration 默认 `w_family=0.0`（上游原生端点选择优先技术兼容性，派系偏好意义不大）。

### 5.6 用户价格偏向 (Cost Preference)

同一模型在不同供应商的价格差异显著（官方 opus vs AWS Bedrock vs kiro 中转），且质量与价格通常正相关。系统不应替用户决定"质量优先还是省钱优先"——这是用户的权利。

#### 5.6.1 三档预设 + 自定义

```json
"costPreference": {
  "mode": "balanced",
  "perTaskClass": {
    "supervisor": "quality_first",
    "worker": "cost_first",
    "lightweight": "cost_first"
  },
  "custom": {
    "savingsMultiplier": 1.0,
    "providerQualityMultiplier": 1.0
  }
}
```

| 模式 | savingsMultiplier | providerQualityMultiplier | 语义 |
|------|-------------------|---------------------------|------|
| `quality_first` | 0.3 | 1.5 | 质量优先：官方 opus 胜过便宜的 kiro，即使贵 3 倍 |
| `balanced` | 1.0 | 1.0 | 默认：按各 TaskClass 原始权重 |
| `cost_first` | 2.0 | 0.5 | 省钱优先：kiro 的 opus 在满足硬约束后优先胜出 |
| `custom` | 用户自定义 | 用户自定义 | 高级用户微调，范围 0.0 ~ 3.0 |

生效方式是权重乘数，不改公式结构：

```text
effective_w_savings          = w_savings          × savingsMultiplier
effective_w_provider_quality = w_provider_quality × providerQualityMultiplier
```

#### 5.6.2 不可突破的边界

价格偏向是软偏好，以下边界任何模式都不能突破：

```text
1. CapabilityFloor / MinQualityTier：cost_first 也不能把 supervisor 降到不满足质量下界的模型
2. 稳定性优先：cost_first 下 stable 贵渠道仍胜过 unstable 便宜渠道
   （w_stability × 2.0 > effective_w_savings × normalizeCheapest 的分差上限需在实现时校验钳制）
3. vision/tool/reasoning/上下文硬约束：不因省钱跳过
4. 成本置信度 < requireCostConfidence 时，savingsMultiplier 不生效（无价格数据不能假装省钱）
5. quality_first 不豁免限速/熔断：官方渠道被限流时仍然 failover 到次选
```

#### 5.6.3 典型场景

```text
场景：用户有官方 Anthropic（$15/$75，providerQuality=0.95）、
      AWS Bedrock（$15/$75，providerQuality=0.85）、
      kiro 中转（0.4x 折扣，providerQuality=0.70），三者都 stable

quality_first（supervisor 默认）：
  官方 > Bedrock > kiro —— 质量差距被放大 1.5 倍，价格几乎不影响

balanced：
  官方 > kiro ≈ Bedrock —— kiro 靠 0.4x 价格追平 Bedrock 的质量优势

cost_first（worker 默认）：
  kiro > 官方 ≈ Bedrock —— 省钱权重放大 2 倍，质量差距缩小一半；
  但若 kiro 变 unstable，立即让位给官方/Bedrock
```

UI 要求：驾驶舱和渠道中心显示当前生效模式；当 quality_first 用户的请求因限流 failover 到低质量供应商时，显示「已临时降级到 kiro（原因：官方限流中）」。

文本类请求的 `estimatedRequestCost` 使用请求级 token 估算：

```text
estimatedRequestCost =
  estimatedInputTokens  * effectiveInputCostPerMTok  / 1_000_000 +
  estimatedOutputTokens * effectiveOutputCostPerMTok / 1_000_000
```

当输出 token 不可预估时：
- supervisor/long_context 使用模型推荐输出上限的保守比例；
- worker/lightweight 使用最近同类请求的 p50 输出 token；
- 如果仍无数据，只使用输入成本做 tie-breaker，不做强排序。

Images / vectors 使用各自原生计价，不强行换算成 chat token：

```text
image_generation:
  estimatedRequestCost =
    imageUnitPrice(model, size, quality, operation) * n * effectiveCostMultiplier

embedding:
  estimatedRequestCost =
    estimatedInputTokens * effectiveEmbeddingCostPerMTok / 1_000_000
```

如果上游没有公开 image/vector 价格：
- 用户手动配置的 `GroupMultipliers` / `RechargeMultiplier` 仍生效。
- 成本置信度低于阈值时只做展示，不参与强排序。
- vectors 可用最近同模型/同维度请求均价作为 shadow 估算；images 不用 chat token 价格替代生图价格。

### 5.7 任务域优势矩阵 (Task-Domain Strength)

模型质量不是标量：不同模型的强项任务不同。典型例子：opus / fable / gemini / glm 的审美（前端 UI、视觉设计）明显好于 gpt，但 gpt 的代码审核能力堪比 fable。单一 QualityTier 无法表达这种"按任务域各有胜负"的差异，本章引入 TaskDomain 维度。

#### 5.7.1 TaskDomain 与 TaskClass 的区别

```text
TaskClass  回答"谁在干活/资源档位"：supervisor / worker / lightweight / vision / …
TaskDomain 回答"干的是什么活/内容领域"：审美、代码审核、算法、写作、翻译…

两者正交：一个 worker（TaskClass）可能在写前端 UI（aesthetics_ui 域），
也可能在做代码审核（code_review 域），最优模型完全不同。
```

```go
type TaskDomain string

const (
    TaskDomainAestheticsUI TaskDomain = "aesthetics_ui" // 前端 UI/视觉设计/审美
    TaskDomainCodeReview   TaskDomain = "code_review"   // 代码审核/找 bug
    TaskDomainCoding       TaskDomain = "coding"        // 通用编码实现
    TaskDomainReasoning    TaskDomain = "reasoning"     // 算法/数学/复杂推理
    TaskDomainWriting      TaskDomain = "writing"       // 文案/长文写作
    TaskDomainTranslation  TaskDomain = "translation"   // 翻译
    TaskDomainAgentic      TaskDomain = "agentic"       // 多步工具调用/agent 编排
    TaskDomainGeneral      TaskDomain = "general"       // 无法细分的通用任务；缺少基准证据时中性
)
```

#### 5.7.2 域推导（确定性规则）

与 P0.3 确定性 TaskClassifier 相同约束：域推导必须是确定性规则，不引入 LLM 判断，同一请求永远推导出同一 TaskDomain。

```text
信号来源（按优先级）：
1. 请求头显式声明：X-Task-Domain（客户端/上层 agent 直接指定，最高优先级）
2. system prompt 关键词匹配：固定关键词表（"code review"/"审查代码"→code_review；
   "UI"/"设计"/"样式"/"Tailwind"→aesthetics_ui；"翻译"→translation…）
3. 工具集特征：请求携带的 tools 列表特征（如仅有 read/grep 类只读工具 + diff 上下文→code_review）
4. 消息内容特征：首条 user 消息的文件扩展名分布（.vue/.css→aesthetics_ui）

任何信号都不足时 → general；有规范 Knowledge 基准时使用其软评分，缺少直接证据时取 0.5 中性。
误判代价有界：w_domain=0.5 上限远低于稳定性/质量权重，误判最多影响同档候选的相对顺序。
```

#### 5.7.3 种子矩阵与覆盖

内置 ModelFamily 级种子矩阵（出厂默认，随版本更新），ModelProfile 级 `TaskDomainStrengths` 可覆盖单个模型：

```text
              aesthetics_ui  code_review  coding  reasoning  writing
claude(fable)      0.90          0.90      0.85     0.90       0.85
claude(opus)       0.90          0.85      0.85     0.85       0.85
openai(gpt)        0.60          0.90      0.80     0.85       0.70
gemini             0.85          0.75      0.75     0.80       0.75
glm                0.80          0.70      0.75     0.70       0.75
deepseek           0.55          0.75      0.80     0.85       0.65
（未列出的族/域一律 0.5 中性；数值为种子先验，允许用户在配置中整体替换）
```

```text
domainStrengthScore = ModelProfile.TaskDomainStrengths[domain]
                      ?? canonicalBenchmark[model][domain] * providerQualityFactor
                      ?? seedMatrix[family][domain]
                      ?? 0.5

来源优先级：用户配置覆盖 > ModelProfile 探测/反馈值 > 规范模型基准 > 种子矩阵 > 0.5
用户反馈通路与 ProviderQualityScore 相同：UI 标记"这个模型做 X 类任务很好/很差"，
写入 ModelProfile.TaskDomainStrengths，TTL 90 天（域优势比供应商质量稳定，衰减更慢）。
```

同版本迭代说明：同族不同版本的域优势也会漂移（如 gpt 新版审美追上），因此种子矩阵按 `family + 主版本` 建键（如 `openai/gpt-5.x`），注册表更新时同步刷新种子值。

#### 5.7.4 规范模型能力基准

`shared/model-registry/ccx_model_registry.json` 的 `benchmarkProfiles` 独立保存规范模型能力上界，不与 `upstreamCapabilities` 混合。后者仍是 context、vision、tool calls 等硬能力的事实源；benchmark 只参与 Autopilot 软评分和 trace 解释，绝不改变 `supportedModels` 或 CapabilityFloor。

```text
EffectiveCapability[domain]
  = CanonicalCeiling[model, domain]
  × ProviderQualityFactor[endpoint]

ProviderQualityFactor = 1 - confidence × (1 - providerQualityScore)
无渠道质量证据或 confidence<0.5 时 factor=1；可信证据只能从规范模型上界向下折算。
```

基准数据必须保留领域向量、来源、核验日期、lane、共享结果数和可比类别覆盖率，不能只保存站点 Overall。当前 BenchLM 接入为 `provisional`：Claude Opus 4.8、GPT-5.6 Terra、GPT-5.6 Sol 各有 30 个共享结果，8 类中 5 类可比，核验日期为 2026-07-13。

领域映射：Knowledge → `general`，Math → `reasoning`，Coding → `coding` / `code_review`，Agentic → `agentic`，Multimodal → `aesthetics_ui`（仅弱代理，解释置信度减半）。Writing / Translation 没有直接类别证据时继续回退家族种子或 0.5。BenchLM 的 cohort 归一化、缺失项插补和外部校准决定了 Overall 不是绝对能力值，因此路由只消费可映射的原始类别分。

### 5.8 思考等级差异 (Reasoning Effort Levels)

同一个模型在不同思考能力等级（thinking budget / reasoning effort）下的有效智商差异显著，代价是少量额外的 think 输出 token 开销。调度不应只在"选哪个模型"上做文章——"同一个模型开多大思考"是一个更便宜的质量杠杆：把 medium 提到 high 的成本增量通常远小于换到更贵一档的模型。

#### 5.8.1 EffortLevel 档位与派系映射

```go
type EffortLevel string

const (
    EffortOff     EffortLevel = "off"     // 不开思考
    EffortMinimal EffortLevel = "minimal"
    EffortLow     EffortLevel = "low"
    EffortMedium  EffortLevel = "medium"
    EffortHigh    EffortLevel = "high"
    EffortMax     EffortLevel = "max"
)
```

统一档位到各派系原生参数的映射（发请求时翻译，画像/评分只用统一档位）：

```text
claude:   thinking.budget_tokens（off=不带 thinking 块；minimal≈1k/low≈4k/medium≈10k/high≈32k/max=模型上限）
openai:   reasoning_effort = minimal|low|medium|high（无 max 的模型 high 封顶）
gemini:   thinkingConfig.thinkingBudget（同 claude 按 token 预算映射）
deepseek/glm/qwen…: 各自 thinking 开关/预算参数，映射表放模型注册表，缺省 SupportsEffortControl=false
```

#### 5.8.2 候选变体展开与评分影响

对 `SupportsEffortControl=true` 的候选，SmartRouter 按 TaskClass 策略展开思考档位变体 `(endpoint, model, effort)`，同一模型的不同档位作为不同候选参与同一套评分：

```text
effort 不是新的权重项，它修改既有两个输入：
  qualityScore(variant)         = baseQualityScore + effortQualityBonus(effort)
                                  （off=0 / minimal=+0.2 / low=+0.4 / medium=+0.6 / high=+0.9 / max=+1.0，
                                   上限钳制在 premium=4，避免"低档模型开满思考"虚标超过高档模型）
  estimatedRequestCost(variant) += estimatedThinkTokens(effort) × effectiveOutputCostPerMTok / 1e6
                                  （think token 按档位经验均值估算，来源于历史同档位请求的 p50）

净效果：quality_first/supervisor 自然偏向高思考档；cost_first/lightweight 自然偏向 off/minimal——
不需要独立的 effort 策略引擎，成本偏好（§5.6）的乘数直接作用于 think 开销。
```

每 TaskClass 的展开范围（避免候选爆炸，每模型最多展开 2 个档位）：

```text
supervisor:   [high, max]        worker:      [medium]
lightweight:  [off, minimal]     vision:      [medium]
long_context: [medium, high]     image_generation/embedding: 不展开
```

#### 5.8.3 不可突破的边界

```text
1. 客户端显式 thinking 配置永远优先：请求体已带 thinking/reasoning_effort 参数时，
   CCX 忠实转发，不展开变体、不改档（代理诚实性原则）
2. 只对 autoManaged 且渠道允许注入思考参数的 endpoint 展开；
   现有渠道级思考配置（如 PassbackReasoningContent）语义不变
3. 升档受成本边界约束：estimatedRequestCost 含 think 开销后仍需通过 §5.6 的排序，
   不允许"为了质量分虚增思考预算"绕过 cost_first
4. effortQualityBonus 不改变 CapabilityFloor 判定：质量下界按 baseQualityTier 评估，
   "低档模型 + max 思考"不能顶替下界要求的高档模型
5. 上游不支持该档位时就近降档（max→high），但绝不低于客户端等效请求的档位
```

#### 5.8.4 联合决策的现状实现（model + effort 原子化）

> 状态说明（2026-07-26）：本节描述的联合决策链路已实现并接入真实请求路径（关联计划 [2026-07-26-autopilot-model-effort-joint-routing.md](../superpowers/plans/2026-07-26-autopilot-model-effort-joint-routing.md) 的 Task 1~7）。**渠道级手工 `modelMapping` / `reasoningMapping` 表目前仍然存在且仍然生效**，退役是该计划的 Task 8，尚未执行；手工表当前的优先级关系见下文。

**决策对象。** 自动路由的返回值不再是裸模型名，而是 `autopilot.ResolvedRouteTarget`（`backend-go/internal/autopilot/route_target.go`）：

```go
type ResolvedRouteTarget struct {
    Model         string      // 目标模型 ID
    Effort        EffortLevel // 目标思考档位；空串表示不改写
    EffortDecided bool        // true 表示 effort 由 Autopilot 决定；false 为 passthrough
    Reason        string      // 决策原因，用于 Trace 与日志
}
```

`EffortDecided=false` 且 `Effort` 为空是合法的 fail-open 结果：模型可能被改写，但思考参数保持原样。

**决策发生的位置。** `autopilot/endpoint_policy.go` 的 `resolveMappedModel()` 是统一入口，内部分两路：`resolveManualMapping()` 处理渠道级显式 `ModelMapping`（仍是当前优先级更高的一路，返回的 `ResolvedRouteTarget.EffortDecided` 恒为 `false`，即手工映射只改模型不改档位）；`resolveAutoModel()` 调用 `ModelResolver` 做自动模型解析，并在 `model_resolver.go` 的 `rankEligibleModels()` 中按 `SupportedEffortLevels` × `PerTaskClass` 展开 `(model, effort)` 候选、应用 `EffortQualityBonus` 与 anti-effort-inflation 反膨胀 tiebreak（同质量档优先选低 effort）、并按 `EffortFloor` 过滤掉低于任务下界的候选。产出的 `ResolvedRouteTarget` 经 `buildResolvedTargetLookup()` 挂到 `EndpointAttemptPolicy.ResolvedTargetByEndpointUID`，与旧的 `ResolvedModelByEndpointUID`（仅模型，向后兼容）并列。

**决策如何到达上游请求。** `handlers/common/upstream_failover.go` 的 `TryUpstreamWithAllKeys` 在为每个 endpoint 尝试构建请求前，通过 `ResolvedTargetByEndpointUID` 取得 `*ResolvedRouteTarget`，交给 `atomicModelEffortRewrite()` 做一次原子改写：先用 `sjson` 改写 `model` 字段；若 `EffortDecided=true` 且 `Effort` 非空，再检查目标模型的 `ThinkingMode`——`adaptive_only` 的模型跳过思考参数注入（模型自身决定思考深度，守卫逻辑对齐 `providers/claude.go` 的 `applyClaudeReasoningEffort`），否则调用 `config.ApplyReasoningParamStyle()` 写入。模型改写与档位改写共用一次函数调用，任一步 `sjson`/`json` 处理失败即整体放弃、保持原始 body，不会出现"模型改了、档位没改"的中间态。

**各协议的注入形态。** `config.ApplyReasoningParamStyle(req, style, effort)`（`config/config_utils.go`）按 `upstream.ReasoningParamStyle` 三态分支：

```text
style="thinking"          → Claude 派系：写 thinking.type=enabled + thinking.effort=<effort>；
                             effort 为 off/none 时写 thinking.type=disabled；同时清理互斥字段
                             reasoning / reasoning_effort / thinking.budget_tokens
style="reasoning_effort"   → OpenAI Chat 派系：写顶层 reasoning_effort 字段，清理 reasoning
style="" / 默认            → Responses 派系：写 reasoning.effort 对象形态
```

**Gemini 派系是尚未接通的缺口。** 请求侧提取（`extractGeminiEffortExplicit()`，`handlers/common/reasoning_log.go`）已经能读取 `generationConfig.thinkingConfig.thinkingLevel` 并识别显式 `none`（`thinkingBudget=0`），这条读路径用于日志与 `RequestProfile.ClientEffort` 提取。但写路径——把联合决策的 `target.Effort` 写回 Gemini 请求体的 `thinkingConfig.thinkingLevel`——目前代码中不存在：`atomicModelEffortRewrite()` 唯一的写出口是 `ApplyReasoningParamStyle()`，它只覆盖 Claude/Chat/Responses 三种形态，没有 Gemini 分支；`handlers/gemini/handler.go` 的 `buildProviderRequest()` 也没有调用任何 effort 注入函数。也就是说，Gemini 渠道当前仍会经过 endpoint 级模型改写（`atomicModelEffortRewrite` 会改写 `model` 字段本身），但联合决策选中的思考档位不会落到 Gemini 请求体上，效果等同于 fail-open passthrough。此项在原计划里归属 Task 5 的"Gemini 派系注入路径"，尚未实现。

**可观测性字段。** 原子改写点会向 `gin.Context` 写入两个字段供 ChannelLog 使用：`effortDecisionSource`（`autopilot` 表示由联合决策产出，`passthrough` 表示未改写或手工映射路径）、`effortClampedByClient`（当客户端显式声明的档位低于 Autopilot 选择的档位时置 `true`，由 `isEffortClampedByClient()` 基于 `EffortLevelOrdinal` 比较得出）。

**手工映射表的现状。** `resolveMappedModel()` 当前的分支顺序是"显式 `ModelMapping` 优先，自动决策兜底"——与原计划 Task 7 描述的"反转优先级"目标相反的方向仍是默认值；反转开关已作为可配置项落地（提交 `d15e776e`），但默认状态未切换，`ModelMapping` / `ReasoningMapping` 两张表本身尚未删除，`config.ResolveReasoningEffort()`（`config/config_utils.go`）与 provider 层（如 `providers/claude.go`）仍会读取 `ReasoningMapping` 独立生效。表的整体退役是 Task 8 的范围，未开始。

### 5.4 模型自动映射 (ModelResolver)

当请求的模型在某个渠道的 `supportedModels` 中不存在时，自动寻找最佳映射。

**请求意图边界（ModelRoutingPolicy）**

跨模型替代不是所有模型的默认能力，只对白名单客户端入口开放：

```text
messages:
  fable / opus / sonnet / haiku 及对应 claude-* 完整模型名 → adaptive
responses:
  gpt-5.6* / gpt-5.5 / gpt-5.4* / codex-auto-review → adaptive
其他协议或模型：
  exact-only，只能在探测到相同规范化模型 ID 的 endpoint 之间调度
```

`adaptive` 允许在满足 Capability Floor 后选择 GLM、MiMo 等其他模型族；`exact-only` 禁止跨模型替代。用户显式配置的 `modelMapping` 仍优先于该策略。调度预览和 endpoint 实际解析必须调用同一分类函数，避免诊断结果与真实请求分叉。

**⚠️ 核心约束：能力下界 (Capability Floor)**

模型映射最大的风险是**语义降级**：用户以为在用 opus 级能力，实际被路由到白嫖模型，输出质量下降但无信号。因此映射必须满足能力下界约束：

```go
// backend-go/internal/autopilot/model_resolver.go

type CapabilityFloor struct {
    MinContextTokens   int    // 请求模型的 AgentModelProfile.ContextWindowTokens
    NeedsReasoning     bool   // 请求模型的 ThinkingMode 非空
    NeedsVision        bool   // 请求包含图片
    NeedsToolCalls     bool   // 请求包含工具定义
    MinQualityTier     QualityTier // 请求模型对应的质量档
}

type ModelResolver struct {
    profileStore *ModelProfileStore
}

// ResolveModel 将请求模型映射到渠道实际支持的模型
// 返回 (mappedModel, resolved, reason)
// resolved=false 表示该渠道无满足下界约束的模型，应跳过此渠道
func (r *ModelResolver) ResolveModel(
    requestModel string,
    channelUID string,
    channelKind string,
    metricsKey string,
    floor CapabilityFloor,
) (string, bool, string) {
    // 1. 查现有 modelMapping（精确匹配 → 模糊匹配）
    //    复用 config.RedirectModel
    //    如果有显式映射，信任用户配置，不做下界检查
    //    （用户手动设的映射视为已知正确）

    // 2. autoManaged 渠道：查 ModelProfile 表
    candidates := r.profileStore.GetModelProfiles(channelUID, channelKind, metricsKey)

    // 3. 硬过滤：只保留满足能力下界的模型
    eligible := filterByCapabilityFloor(candidates, floor)
    if len(eligible) == 0 {
        return "", false, "no model meets capability floor"
    }

    // 4. 在满足下界的模型中选优
    //    上下文窗口只负责准入，不对更大的窗口扣分。
    //    排序优先级：
    //    a. 模型质量档越高越优先
    //    b. provider 已确认的同档模型能力顺序
    //    c. 带置信度折算的供应商实测质量
    //    d. 探测延迟
    //    e. 同渠道/provider 的模型相对消耗倍率
    //       倍率相同或缺失时再比较公开模型成本
    //    f. 同模型族与 model ID 仅作为确定性兜底
    best := rankEligibleModels(eligible, requestModel)

    return best.ModelID, true, fmt.Sprintf("mapped %s→%s (family:%s, quality:%s)",
        requestModel, best.ModelID, best.Family, best.QualityTier)
}

// filterByCapabilityFloor 只保留满足所有下界约束的模型
func filterByCapabilityFloor(profiles []ModelProfile, floor CapabilityFloor) []ModelProfile {
    var eligible []ModelProfile
    for _, p := range profiles {
        if !p.ProbeSuccess {
            continue // 未验证通过的模型不参与自动映射
        }
        if p.ContextTokens < floor.MinContextTokens {
            continue
        }
        if floor.NeedsReasoning && !p.SupportsReasoning {
            continue
        }
        if floor.NeedsVision && !p.SupportsVision {
            continue
        }
        if floor.NeedsToolCalls && !p.SupportsToolCalls {
            continue
        }
        if qualityTierRank(p.QualityTier) < qualityTierRank(floor.MinQualityTier) {
            continue
        }
        eligible = append(eligible, p)
    }
    return eligible
}
```

同渠道套餐倍率与公开 USD/Token 价格是不同单位，禁止直接混算。模型解析器先比较 provider
提供的相对消耗倍率；只有双方倍率相同，或双方均无倍率时，才回退到统一输入/输出规模下的
公开价格。优云智算当前内置倍率如下（数字越小越省，`glm-5.2` 的 2 次为限时值）：

| 模型 | 每次调用套餐消耗 |
|------|-----------------:|
| `glm-5.2` | 2 |
| `glm-5.1` | 6 |
| `MiniMax-M2.7` | 2 |
| `kimi-k2.6` | 5 |
| `deepseek-ai/DeepSeek-V3.2` | 1 |
| `deepseek-v4-flash` | 1 |

provider 可以额外声明同一质量档内已确认的模型能力顺序，但只有该档全部候选都命中该元数据
时才比较，避免把未知模型误判为弱模型，也保证排序不依赖候选输入顺序。优云模板当前记录
`glm-5.1 > kimi-k2.6`，因此前者即使消耗 6 次，也会在后者的 5 次成本证据之前胜出。
`glm-5.2` 即使消耗 2 次，也仍会在质量档高于 DeepSeek 候选时优先；V3.2 与 V4 Flash
同为 1 次时，再由公开价格及最终确定性规则区分。静态 `modelMapping` 仅保留为用户显式覆盖，
不承担自动替代策略。

**映射示例**：

| 请求模型 | 能力下界 | 渠道实际模型 | 映射依据 | 是否通过 |
|----------|---------|-------------|---------|---------|
| `claude-opus-4-8` | context:1M, reasoning, quality:premium | `claude-opus-4-7` | 同 opus 族，满足全部下界 | ✓ |
| `claude-opus-4-8` | context:1M, reasoning, quality:premium | `claude-haiku-4-5` | haiku 不满足 quality:premium | ✗ 跳过渠道 |
| `gpt-5.5` | quality:premium, reasoning | `gpt-5.4` | 同 premium 档，满足下界 | ✓ |
| `claude-sonnet-5` | quality:high | `claude-sonnet-4-6` | 同 sonnet 族，满足下界 | ✓ |
| 请求含图片 | vision:true | 某渠道无 vision 模型 | 不满足 vision 下界 | ✗ 跳过渠道 |

#### 5.4.1 与现有模型过滤链路的关系

当前调度器的 active model filter 会先调用 `upstream.ExplainModelSupport(model)`，之后才进入 `CandidateFilter`。因此自动映射不能只放在 SmartRouter 后半段，否则 channel 会在映射前被剔除。

Phase 3 落地 ModelResolver 时必须调整为以下顺序：

```text
1. 构建 RequestProfile + CapabilityFloor
2. 收集候选 channel：
   - 显式 supportedModels 支持请求模型 → 保留
   - 显式 modelMapping 可把请求模型映射到上游模型 → 保留
   - autoManaged 且存在满足 CapabilityFloor 的 ModelProfile → 保留
   - autoManaged 的空 supportedModels 不表示支持全部，必须由 endpoint ModelProfile 裁决
   - 其他 → 过滤
3. 对每个候选 channel/endpoint 计算 request-scoped mappedModel
4. context filter 使用 mappedModel 的实际能力做窗口校验
5. TryUpstreamWithAllKeys 使用 EndpointAttemptPolicy 写入请求体中的实际 model
6. 响应 header/ChannelLog 回显 originalModel 与 mappedModel
```

实现上建议新增 `ModelSupportResolver`，替代 active model filter 中对 `ExplainModelSupport` 的直接调用：

```go
type ModelSupportResolution struct {
    Supported    bool
    ActualModel  string
    Source       string // supported_models | explicit_mapping | auto_profile
    Reason       string
}
```

这能避免“自动映射尚未运行，channel 已被 supportedModels 过滤掉”的死路。

当 ModelResolver 已正常初始化且路由处于 `assist/auto + autoResolve=true` 时，autoManaged 渠道的画像拒绝是权威结果，调度器不得再 fail-open 到空 `supportedModels` 的兼容语义。Resolver 不可用、开关关闭或 `off/shadow` 时仍保留原有 fail-open，保证急停和降级路径不依赖画像库。

**映射结果回显**：

这是调试的关键。映射发生时，必须在响应中标注真实使用的模型：

```text
方案 A（推荐）：在 response header 中回显
  X-CCX-Mapped-Model: claude-opus-4-7
  X-CCX-Original-Model: claude-opus-4-8
  X-CCX-Mapping-Source: auto_resolved

方案 B：在 Claude Messages 响应 body 的 model 字段用真实模型
  {"model": "claude-opus-4-7", ...}  // 而非请求的 claude-opus-4-8

方案 C：两者都做（最利于调试）
```

**安全边界**：
- 仅 `autoManaged: true` 的渠道触发自动映射
- 显式 `modelMapping` 始终优先，不经过下界检查（信任用户配置）
- 请求路径中的自动映射默认是 request-scoped，不直接写回 `modelMapping`
- Discovery 产生的映射建议可持久化；用户也可在 UI 将 request-scoped 映射保存为显式 `modelMapping`
- 映射日志记录每次决策（requestModel → mappedModel → floor → reason），写入 ChannelLog
- **禁止链式映射**：A→B 后不再 B→C，避免不可预测的降级链

## 6. 健康诊断系统

### 6.1 HealthState 状态机

```text
                    ┌──────────┐
          添加渠道 →│ unknown  │
                    └────┬─────┘
                         │ L2 探测成功 或 首次真实请求成功
                         ▼
                    ┌──────────┐
         ┌────────→│ healthy  │←────────┐
         │         └──┬───┬───┘         │
         │ L1被动信号  │   │ L1被动信号  │ L2 探测成功
         │ 成功率↓    │   │ 连续失败≥3  │ 或 真实请求成功
         │ 429增多    │   │             │
         │         ┌──▼┐  │        ┌────┴─────┐
         │         │deg│  │        │ limited  │
         │         │rad│  │        │(429/quota)│
         │         └─┬─┘  │        └────┬─────┘
         │           │    │             │
         │  L1连续≥10│    │ L1连续≥5   │ cooldown 到期
         │  或成功率  │    │ 且全部key   │ L2 探测失败
         │  <50%     │    │ 认证失败    │
         │         ┌─▼──┐ │        ┌────▼─────┐
         │         │dead│◄┘        │ dead     │
         │         └─┬──┘          └──────────┘
         │           │
         │  L2恢复   │  L1/L3 检测到配置错误
         │  探测成功  │
         │           │         ┌──────────────┐
         └───────────┘         │ misconfigured│← 用户修复后 L3 重测
                               └──────────────┘
```

**关键：所有正常状态转换基于 L1 被动信号，不需要额外请求。**

### 6.2 诊断规则

#### Dead（高置信度死亡）

```text
── 硬死（confidence >= 0.95，全部来自 L1 被动信号）──
  - 全部 Key 返回 401/403（最近 1 小时内的真实请求，FailureClass=non_retryable）
  - DNS/TLS 连接失败（ChannelLog 中 error 含 "dial tcp"/"tls"/"certificate"）
  - 连续失败 >= 15 次（MetricsManager 滑动窗口）

── 软死（confidence >= 0.80，L1 被动信号）──
  - 最近 24 小时无成功请求，且有失败记录
  - 熔断器 open 且 lastSuccessAt > 6 小时前
  - 成功率 < 10%（最近 1 小时，且请求样本 >= 5）

── 确认（仅在 L1 不足时触发 L2）──
  - L1 数据不足（请求数 < 5）但 L2 探测连续失败 >= 3 次
```

#### Degraded（可用但质量差）

```text
── 全部来自 L1 被动信号 ──
  - 成功率 50%-80%（最近 1 小时，请求样本 >= 10）
  - p95 延迟 > 5000ms（最近 1 小时）
  - 断流率 > 20%（ChannelLog 中 streaming→非completed 的比率，最近 30 分钟）
  - 空响应率 > 10%（usage 全零但无报错，最近 30 分钟）
```

#### Limited（限流中）

```text
── 全部来自 L1 被动信号 ──
  - FailureClass=overloaded 占比 > 30%（最近 15 分钟）
  - Retry-After header 出现在最近 5 分钟内的 ChannelLog
  - FailureClass=quota（402/insufficient_balance/insufficient_quota）
  - 熔断器 open 但 lastSuccessAt < 6 小时前（区别于 dead）
```

#### Misconfigured（配置疑似错误）

```text
── L1 被动信号 ──
  - 全部请求返回 404（modelMapping 指向不存在的模型）
  - 501/505（协议不支持）
  - capability-test 中仅部分协议成功，但 serviceType 配的是失败协议

── L3 深度探测确认（可选，用户手动触发）──
  - chat 协议成功但 serviceType 配为 claude
  - authHeader 类型与响应不匹配
```

#### Unknown（证据不足）

```text
  - 新添加的渠道，无历史数据
  - 最近 24 小时内请求数 < 5 且未运行 L2 探测
  - L3 capability-test 未运行或已过期（> 7 天）
```

### 6.3 死亡类型细分

```go
type DeathType string
const (
    DeathTypeHard       DeathType = "hard"        // DNS/TLS/认证（L1 被动即可判定）
    DeathTypeSoft       DeathType = "soft"        // 429/quota/临时错误（L1 被动即可判定）
    DeathTypeModel      DeathType = "model"       // 模型不可用（L1 或 L3）
    DeathTypeQuality    DeathType = "quality"     // 空响应/断流（L1 被动即可判定）
    DeathTypeUnknown    DeathType = "unknown"     // 无法分类
)
```

**注意：Quality 死亡只检测"硬失败"**（空响应、断流、格式错误），不检测"答非所问"等软质量问题。软质量问题留给人工标签 override。

### 6.4 健康诊断对调度的影响

```text
HealthState      │ 调度行为                    │ UI 表现
─────────────────┼─────────────────────────────┼──────────────
healthy          │ 正常参与调度                │ 绿色
degraded         │ 降权，只在同池不足时使用     │ 黄色
limited          │ cooldown 内跳过，到期 L2 复测│ 橙色
misconfigured    │ 不参与自动调度，提示修复     │ 紫色
dead             │ 默认移出调度，建议清理       │ 红色
unknown          │ 低风险请求小流量试探         │ 灰色
```

**自动恢复机制**：
- `limited` 渠道：cooldown 到期后触发 L2 轻量探测（单模型、最小 prompt），成功则回到 `healthy`
- `dead` 软死渠道：每 30 分钟检查一次 L1 被动信号，如果有真实请求成功则回到 `healthy`；无真实请求时每 2 小时触发 L2 探测，连续 3 次成功则恢复
- `dead` 硬死渠道：每 6 小时触发 L2 探测，连续 3 次成功则回到 `unknown`（不是直接 `healthy`，需真实请求验证）
- `misconfigured` 渠道：用户修复配置后手动触发 L3 深度探测

**L2 探测成本控制**：
- 每次 L2 探测只用 1 个模型、1 个 prompt、max_tokens=50
- 所有渠道的 L2 探测串行执行，间隔 >= 5 秒
- 每天 L2 探测总次数上限：`渠道数 × 12`（每 2 小时最多一次）
- 白嫖渠道的 L2 探测频率自动降低：如果连续 3 次 L2 探测失败，间隔翻倍（2h→4h→8h→24h）

### 6.5 白嫖池快速衰减机制

白嫖/临时池渠道的可用性是移动靶，不能靠滑动窗口慢慢反应。需要独立的衰减机制：

```go
// backend-go/internal/autopilot/fast_decay.go

// FastDecayScore 实时衰减评分，用于白嫖/临时池渠道
type FastDecayScore struct {
    ChannelID       int
    BaseScore       float64   // 基于 ChannelProfile 的基础分
    DecayFactor     float64   // 衰减系数 0.0-1.0
    LastUpdate      time.Time
    ConsecutiveFail int
}

// OnSuccess 请求成功时
func (s *FastDecayScore) OnSuccess() {
    s.DecayFactor = math.Min(1.0, s.DecayFactor+0.15) // 快速回升 +15%
    s.ConsecutiveFail = 0
}

// OnFailure 请求失败时
func (s *FastDecayScore) OnFailure() {
    s.ConsecutiveFail++
    // 指数衰减：连续失败越多，衰减越快
    // 1次失败: ×0.85, 2次: ×0.72, 3次: ×0.61, 5次: ×0.44, 10次: ×0.20
    s.DecayFactor *= math.Pow(0.85, float64(s.ConsecutiveFail))
}

// OnStreamBreak 断流时（比普通失败更严重）
func (s *FastDecayScore) OnStreamBreak() {
    s.ConsecutiveFail++
    s.DecayFactor *= math.Pow(0.70, float64(s.ConsecutiveFail)) // 更激进衰减
}

// EffectiveScore = BaseScore × DecayFactor
func (s *FastDecayScore) EffectiveScore() float64 {
    return s.BaseScore * s.DecayFactor
}
```

**触发条件**：`costTier=free|cheap` 或 `poolTag=temp` 的渠道自动启用 FastDecay。

**调度效果**：
- 一个白嫖渠道连续断流 3 次，EffectiveScore 从 1.0 降到 0.61，自动让位给下一个渠道
- 连续失败 10 次，降到 0.20，几乎不会被选中
- 成功一次立即回升 15%，避免"一朝被蛇咬"的永久惩罚
- 这比滑动窗口快得多：滑动窗口需要窗口滚动才反映变化，FastDecay 是请求级即时反应

## 7. API 设计

### 7.1 新增 API 端点

#### 渠道画像

```text
GET  /api/{kind}/channels/profiles          → 获取所有渠道画像
GET  /api/{kind}/channels/{id}/profile      → 获取单个渠道画像
POST /api/{kind}/channels/{id}/profile/refresh → 手动刷新画像
```

#### 模型画像

```text
GET  /api/{kind}/channels/{id}/model-profiles → 获取渠道下所有模型画像
```

#### 本地模型运行时

```text
GET  /api/local-models/runtimes              → 获取本地运行时列表
POST /api/local-models/runtimes              → 添加 Ollama/LM Studio/llama-server
POST /api/local-models/runtimes/{uid}/probe  → 探测本地模型能力/延迟
POST /api/local-models/runtimes/{uid}/benchmark → 本地 runtime 性能与能力基准
POST /api/smart-routing/trusted-advisor/benchmark → 对一等官方或本地 advisor 做 shadow 评估
GET  /api/smart-routing/trusted-advisor/summary   → 查看可信 advisor 准确率、成本与命中情况
```

#### 健康中心

```text
GET  /api/health-center/overview            → 全局健康概览（跨所有 kind）
GET  /api/health-center/channels            → 渠道健康列表（支持过滤/排序）
POST /api/health-center/batch-action        → 批量操作（refresh/probe/pause）
POST /api/health-center/channels/{id}/probe → 手动深度探测
GET  /api/health-center/provider-quality/budget → 查看本进程手动 L3 每日预算
POST /api/health-center/provider-quality/probe  → 对指定 endpoint×model 执行固定质量 canary
```

#### 订阅中心

```text
GET  /api/subscriptions                     → 获取订阅/套餐列表
POST /api/subscriptions                     → 创建订阅/套餐
PUT  /api/subscriptions/{uid}               → 更新订阅/套餐
POST /api/subscriptions/{uid}/link-channel  → 绑定渠道
POST /api/subscriptions/{uid}/refresh       → 手动刷新余额/套餐状态（可选 provider adapter）
```

#### 自动托管

```text
POST /api/{kind}/channels/auto-add          → 自动添加渠道（仅需 URL+Key）
POST /api/{kind}/channels/{id}/auto-discover → 重新触发自动发现
GET  /api/{kind}/channels/{id}/auto-status   → 自动托管状态
```

#### 智能路由（诊断用）

```text
POST /api/smart-routing/diagnose            → 智能路由诊断（dry-run）
GET  /api/smart-routing/config              → 获取自动路由配置
PUT  /api/smart-routing/config              → 更新自动路由配置
GET  /api/smart-routing/intents             → 获取人工路由意图列表
POST /api/smart-routing/intents             → 创建模型/渠道/endpoint 试用意图
PUT  /api/smart-routing/intents/{uid}       → 禁用或延长试用意图
GET  /api/smart-routing/intents/{uid}/result → 查看试用结果摘要
```

### 7.2 与现有 API 的关系

| 现有端点 | 变更 |
|---------|------|
| `POST /channel-discovery` | 不变，autopilot 内部复用其逻辑 |
| `POST /{kind}/channels` | 新增 `autoManaged` 字段，为 true 时自动触发 Discovery |
| `POST /{kind}/channels/{id}/capability-test` | 不变，autopilot 结果写入 ModelProfile |
| `POST /{kind}/channels/scheduler/diagnose` | 增加智能路由 trace 输出 |
| `GET /{kind}/channels/dashboard` | 增加 `healthState`、`qualityTier`、`originTier`、`subscriptionUid` 字段 |
| `X-Channel` / manual override / promotion | 保留；新增 `ManualRoutingIntent` 在产品层表达短期试用，不替代底层显式控制 |
| 本地 OpenAI-compatible runtime | 可作为普通 channel，也可注册为 `LocalModelRuntimeProfile`；如角色包含 `routing_advisor`，可被 `TrustedRoutingAdvisor` 选择 |
| `/v1/images/*` / `/v1/embeddings` | 纳入原生 endpoint 调度；MVP 不做 chat 画图转换，只做健康/成本/RPM/endpoint 排序 |

### 7.3 WebSocket 推送

新增 `ws://api/autopilot/events` 通道，推送：

```json
{
  "type": "profile_updated",
  "channelId": 5,
  "channelKind": "messages",
  "healthState": "dead",
  "suggestedAction": "delete",
  "evidence": ["5/5 keys returned 401"]
}
```

事件类型：`profile_updated` / `health_changed` / `discovery_completed` / `auto_mapping_applied`

### 7.4 安全与权限边界

新增端点必须按管理接口处理：

- 所有 `/api/health-center/*`、`/api/smart-routing/*`、`/api/{kind}/channels/*/auto-*` 端点必须要求 `ADMIN_ACCESS_KEY` 或等价管理权限。
- 创建 `ManualRoutingIntent` 必须要求管理权限；如果影响 main/supervisor 或 unknown model，UI 必须显示 TTL、预算和 fallback 状态。
- 添加本地运行时只允许 loopback/private network baseURL，除非用户显式开启远程本地运行时；避免 SSRF 风险。
- routing advisor 会读取用户 prompt/metadata，因此只能调用 `OriginTier=first` 或 `OriginTier=local`；严禁把额外判定上下文发送给中转站、公益站或未知来源。
- advisor 输入必须走最小上下文：只传分类所需的 prompt 摘要、任务 metadata 和能力需求，不传 API Key、Authorization、自定义敏感 header 或完整历史，除非用户显式允许。
- `batch-action` 默认只允许 `refresh`、`probe`、`pause`，不默认开放删除；删除必须走现有单渠道删除流程并二次确认。
- WebSocket 必须鉴权，事件中禁止携带明文 API Key、Authorization header、自定义敏感 header、multipart 内容。
- 手动深度探测会请求真实上游，UI 必须显示本次操作会消耗上游额度；后台自动 L2 探测遵守全局预算。
- `auto-add` 写入配置前只保存用户提交的必要字段；探测日志只记录 key mask 和 metricsKey。

---

## 8. 前端设计

### 8.0 信息架构联动

Autopilot 会影响四个一线界面，必须按职责拆清楚，避免所有信息都堆到渠道列表：

| 页面 | 职责 | 主要数据 | 不负责 |
|------|------|----------|--------|
| 渠道中心 | 管上游 endpoint 的可用性、能力、信任等级和调度状态 | ChannelProfile、KeyEndpointProfile、HealthState、OriginTier、SubscriptionUID | 不维护套餐余额，不计算全局花费预算 |
| 订阅中心 | 管官方 API/token plan、中转套餐、公益来源、余额与倍率 | SubscriptionProfile、余额、续费周期、GroupMultipliers、RechargeMultiplier | 不展示每个 endpoint 的实时健康细节 |
| 管理面板 | 管全局策略、权限、安全开关、本地模型和 Autopilot 模式 | smartRouting 配置、探测预算、自动 RPM、可信 advisor、成本优化开关 | 不承载日常运营决策 |
| 驾驶舱 | 给出当前系统是否健康、是否省钱、是否需要人工处理 | 跨渠道聚合指标、成本趋势、异常队列、调度 dry-run 摘要、ManualRoutingIntent 摘要 | 不编辑复杂渠道配置 |

核心数据流：

```text
订阅中心：套餐/倍率/余额/信任等级
    ↓ SubscriptionUID
渠道中心：endpoint 健康/能力/质量/成本画像
    ↓ ChannelProfile + CostProfile
SmartRouter：按任务类型、硬约束、实时质量和有效成本排序
    ↓ 运行指标
驾驶舱：总览、异常、节省金额、人工待办
```

界面原则：
- 信任等级始终单独展示为 `官方 / 官方 token plan / 中转 / 公益 / 本地 / 未知`，不和质量 badge 合并。
- 服务质量始终来自运行画像：成功率、流式完整性、p95 延迟、HealthState、QualityTrend。
- 当低信任等级渠道短时间服务更好时，驾驶舱和渠道中心都应显示「低信任来源当前表现更优」，但仍禁止它承担隐私敏感的 AI 判定。
- 所有价格、余额、充值倍率优先从订阅中心读；渠道/key 只保留必要 override。

#### 8.0.1 实现现状与演进方向（2026-07-09 核实）

四界面在 **Web 前端（`frontend/src`）已大面积落地**，不是纯文档：

| 界面 | Web 实现 | Desktop 实现 |
|------|----------|--------------|
| 健康中心 | ✅ `HealthCenterView.vue` + `HealthCenterStats`/`HealthChannelTable`/`HealthChannelDetail`/`ProfileChangelogTimeline`，路由 `/health` | ❌ 无 |
| 订阅中心 | ✅ `SubscriptionsView.vue` + `SubscriptionPlanTable`，CRUD + 刷新余额，路由 `/subscriptions` | ⚠️ 独立旧实现 `subscriptions/SubscriptionTab.vue`（与 Web 两套代码） |
| 渠道中心 | ✅ `ChannelOrchestration.vue`（六态 `ChannelHealthBadge`、来源 chips、`autoManaged` 图标齐备） | ❌ 未同步 |
| 管理面板 | ⚠️ `AutopilotModePanel.vue` 仅暴露 mode/killSwitch/costPreference | ❌ 无 |
| 驾驶舱 | ✅ `CockpitView.vue`（观测型总览：健康统计+余额汇总+渠道推荐） | ⚠️ `cockpit` tab 渲染的是会话 Dashboard，职责不同 |

**与本设计意图的关系**（对齐用户「自动调度为主、废旧手动驾驶舱」的诉求）：
- **旧的「手动调度驾驶舱」在 Web 端已不存在**。`CockpitView` 已是 §8.7 定义的观测型总览，只回答健康/省钱/风险/待办四问题，不做渠道配置编辑，符合「程序自动选、人只观测+有限干预」的方向。无需再废除，只需继续收敛为纯观测。
- **统一提交入口已存在**（§8.4 快速添加 + §8.5 订阅中心），new-api 集成（§8.5.1）在此之上把「填 baseURL+key」进一步自动化。

**前端演进待办**（本设计新增/明确）：
1. **补 `ManualRoutingIntent` 前端**：这是用户要的「有限偏向性干预」，后端已有、两个前端均未实现（§8.3 试用意图入口尚缺）。
2. **Desktop 前端对齐 Web**：健康中心/渠道自动托管/管理面板/观测型驾驶舱在 Desktop 几乎空白，需同步或复用 Web 组件，避免长期维护两套。
3. **管理面板补字段**：暴露 `l2ProbeEnabled`、探测预算、new-api/多来源订阅 adapter 开关（§8.5.2）等，当前只有 3 个字段。
4. **订阅中心接 §8.5.1/§8.5.2**：新增「new-api」订阅类型与 Provider Adapter 能力矩阵驱动的动态表单。

### 8.1 健康中心视图

新增 `HealthCenter.vue` 页面，作为渠道中心的高级视图。

#### 布局

```text
┌──────────────────────────────────────────────────────────┐
│ 渠道健康中心                    [批量复测] [批量暂停] [筛选] │
├──────────────────────────────────────────────────────────┤
│ ┌─ 统计卡片 ────────────────────────────────────────────┐│
│ │ 🟢 12 健康  🟡 3 降级  🟠 5 限流  🔴 4 死亡  ⚪ 2 新  ││
│ └───────────────────────────────────────────────────────┘│
│                                                          │
│ ┌─ 分组标签 ────────────────────────────────────────────┐│
│ │ [官方(5)] [Token Plan(3)] [中转(18)] [公益(6)]        ││
│ │ [建议清理(4)] [需要修复(3)] [限流恢复中(5)]           ││
│ │ [质量较差(2)] [观察池(2)] [当前表现优于等级(3)]       ││
│ └───────────────────────────────────────────────────────┘│
│                                                          │
│ ┌─ 渠道表格 ────────────────────────────────────────────┐│
│ │ 状态 │ 来源 │ 渠道 │ 订阅 │ 协议 │ 模型数 │成功率│p95│ │
│ │ 🔴   │ 中转 │ xxx  │ A套餐│ chat │ 3/5    │ 2%   │ - │ │
│ │ 🟢   │ 公益 │ yyy  │ 免费 │ msgs │ 7/7    │ 99%  │1s │ │
│ └───────────────────────────────────────────────────────┘│
│                                                          │
│ ┌─ 渠道详情侧栏（点击展开）─────────────────────────────┐│
│ │ 健康状态：🔴 Dead (confidence: 96%)                    ││
│ │ 死亡类型：硬死 - 全部 Key 认证失败                     ││
│ │ 证据：                                                 ││
│ │   • 5/5 keys returned 401                              ││
│ │   • capability-test failed 3 consecutive times         ││
│ │   • no successful request in 72h                       ││
│ │ 建议操作：[替换 Key] [删除渠道] [标记观察]             ││
│ │ 来源：中转 second，订阅：A 套餐                         ││
│ │ 画像：quality=high, stability=unstable, speed=-        ││
│ │ 可用模型：gpt-5.4(✓), gpt-5.5(✗), ...                ││
│ └───────────────────────────────────────────────────────┘│
└──────────────────────────────────────────────────────────┘
```

#### 标签系统

每个渠道显示标签 chip：

| 标签 | 颜色 | 条件 |
|------|------|------|
| 官方 API | 蓝 | originType=official_api |
| 官方 Token Plan | 蓝 | originType=official_token_plan |
| 中转站 | 靛 | originType=relay |
| 公益站 | 绿 | originType=community |
| 来源未知 | 灰 | originType=unknown |
| 高智商稳定 | 蓝 | qualityTier=high\|premium + stabilityTier=stable |
| 白嫖池 | 绿 | costTier=free |
| 临时池 | 橙 | 画像来源=auto_probe 且 confidence < 0.7 |
| 当前表现优于等级 | 青 | originTier=second\|third 且 successRate15m/p95/streamHealth 优于一等来源中位数 |
| 仅子代理 | 灰 | qualityTier=low + costTier=free\|cheap |
| 可识图 | 紫 | supportsVision=true |
| 长上下文 | 青 | supportsLongCtx=true |
| 全部 Key 失效 | 红 | evidence 含 "all keys failed" |
| 限流中 | 黄 | healthState=limited |
| 疑似配置错 | 紫 | healthState=misconfigured |

### 8.2 渠道卡片增强

在现有 `ChannelOrchestration.vue` 的每行中增加：

1. **健康状态 badge**：替换现有简单状态，使用 HealthState 六态 badge
2. **来源 badge**：显示官方 API / 官方 Token Plan / 中转 / 公益 / 未知，并支持跳转到订阅中心
3. **质量/稳定性/速度/成本标签**：在渠道名下方显示小 chip，和来源 badge 分开
4. **自动托管图标**：`autoManaged` 渠道显示机器人图标，hover 提示「自动托管中」
5. **一键操作**：死渠道显示「清理」快捷按钮
6. **有效成本预览**：在 key 详情中显示 `groupMultiplier / rechargeMultiplier = effectiveMultiplier` 和按模型估算的每百万 token 成本
7. **信任等级质量对比**：当低信任来源短时间优于一等来源中位数时，显示「当前表现更好」状态，但不改变其信任等级
8. **试用入口**：对新公益站/新 endpoint 显示「试用」按钮，打开 ManualRoutingIntent 创建面板

### 8.3 人工干预入口

人工干预不放在全局优先级里，而是作为短期试用意图创建：

```text
┌─ 创建试用意图 ─────────────────────────┐
│ 类型：[模型试用 ▾]                      │
│ 模型：[fable-5____________________]     │
│ 渠道：[自动选择 / 指定渠道 / 指定endpoint] │
│ 范围：[当前会话] [子代理] [轻任务]       │
│ 流量：[10%───]  上限：[100 请求]         │
│ 有效期：[2 小时 ▾]  失败回退：[开]       │
│ 风险：未知模型不会写入全局 modelMapping  │
│                         [创建试用]      │
└────────────────────────────────────────┘
```

入口：
- 渠道中心：在渠道/endpoint 行上创建 `channel_trial` 或 `endpoint_trial`，适合“公益站先用起来”。
- 模型画像：在未知或新发现模型上创建 `model_trial`，适合测试 `fable-5`。
- 驾驶舱：显示活跃试用、预算消耗、成功率、fallback 次数和一键结束。
- 会话详情：创建 `session_pin`，只影响当前会话排障。

默认值：
- `model_trial` 默认 2 小时或 100 请求，`FallbackOnFailure=true`。
- `channel_trial` 默认只作用于 worker/lightweight，不覆盖 supervisor。
- third-tier 公益站试用默认 `trafficPercent<=25`，除非用户显式提高。
- 试用结束只生成建议，不自动保存长期映射或优先级。

### 8.4 添加渠道流程简化

当用户选择「快速添加」模式：

```text
┌─ 快速添加渠道 ──────────────────────────┐
│                                          │
│ 名称：[________________] (可选，自动生成) │
│ 来源：[官方API ▾]  订阅：[新建/选择____] │
│ 地址：[https://xxx/v1________________]   │
│      [+ 添加 BaseURL]                    │
│ Key ：[sk-__________________________]    │
│      [+ 添加 Key]                        │
│                                          │
│ Key 成本（可选，展开编辑）                │
│ ┌ key mask │ 分组倍率 │ 充值倍率 │ 预估成本 ││
│ │ sk-***a  │ *:1.0   │ 1.0     │ 1.0x    ││
│ │ sk-***b  │ opus:1 │ 2.0     │ 0.5x    ││
│ └──────────────────────────────────────┘│
│ 信任说明：信任等级只表示隐私/治理风险，不代表实时质量 │
│                                          │
│ [x] 自动托管（推荐）                     │
│     系统将自动探测协议、发现模型、        │
│     生成映射、持续监控健康               │
│                                          │
│          [添加并探测]                    │
└──────────────────────────────────────────┘
```

点击「添加并探测」后：
1. 创建渠道（status=unknown）
2. 自动触发 Discovery + 能力测试
3. 显示进度条和实时探测日志
4. 完成后生成映射建议；仅当所有 endpoint 结果一致时自动写入 channel 级 modelMapping / supportedModels / 兼容开关
5. 生成初始 ChannelProfile
6. 如果用户选择/创建了订阅，自动建立 `SubscriptionUID → ChannelUID` 链接

### 8.5 订阅中心

新增 `SubscriptionsView.vue`，统一管理官方 API、官方 token plan、中转套餐和公益来源。它不是支付系统，只是本地账单和来源画像的事实源。

```text
┌──────────────────────────────────────────────────────────┐
│ 订阅中心                              [新增订阅] [导入] │
├──────────────────────────────────────────────────────────┤
│ 类型筛选：[全部] [官方API] [Token Plan] [中转] [公益]    │
│                                                          │
│ ┌─ 套餐表 ──────────────────────────────────────────────┐│
│ │ 类型 │ 名称 │ 余额 │ 倍率 │ 绑定渠道 │ 最近使用 │ 状态 ││
│ │ 官方 │ OpenAI Prod │ $42 │ *:1.0/1.0 │ 3       │ 2m前 │正常││
│ │ 中转 │ A站充值组   │ ¥88 │ opus:1/2  │ 8       │ 1m前 │正常││
│ │ 公益 │ 临时公益池  │ -   │ free      │ 4       │ 5m前 │波动││
│ └───────────────────────────────────────────────────────┘│
│                                                          │
│ ┌─ 详情 ────────────────────────────────────────────────┐│
│ │ 价格公式：effective = groupMultiplier / recharge      ││
│ │ 继承链：subscription → channel → key                  ││
│ │ 绑定渠道：[messages/0] [responses/2] [chat/1]         ││
│ │ 操作：[刷新余额] [绑定渠道] [编辑倍率] [归档]          ││
│ └───────────────────────────────────────────────────────┘│
└──────────────────────────────────────────────────────────┘
```

订阅中心规则：
- `official_api` 和 `official_token_plan` 默认是 `OriginTier=first`，但 UI 必须显示具体计费模式。
- 中转站套餐必须支持 key 级分组倍率和充值倍率批量编辑，写入 `SubscriptionProfile` 后由 key 继承。
- 公益站允许没有余额和价格，仍可维护来源、备注、RPM 建议和风险说明。
- 一个订阅可绑定多个渠道；一个渠道默认只绑定一个订阅。需要多个来源时建议拆 channel。
- 余额刷新 adapter 是可选能力；MVP 允许手动录入余额和更新时间。

### 8.5.1 new-api 订阅集成（统一账号接入）

**目标**：用户在订阅中心手填一个 new-api（及其 fork）账号的「系统访问令牌」即可，CCX 自动用该令牌创建代理专用 key、拉取分组倍率、查询额度余额，并把「站点 baseURL + 新建 key」自动落成一个上游渠道，交给 Autopilot 探测与调度。

**范围约束**：
- 只使用 new-api 用户在「个人设置」生成的 **系统访问令牌（access_token）** 一种凭据；令牌用于 API 调用的身份验证，属敏感数据，须妥善加密存储。
- **不做** cookie 登录 + 2FA + 导出后台渠道明文 key 的管理员迁移路径（all-api-hub 的 newApiSession 能力），本设计不涉及。
- new-api 归入 `OriginType=relay`、`OriginTier=second`（中转站），信任等级不因自动化提升；实时质量仍由运行画像决定。
- 认证令牌像其他订阅凭据一样由用户手动录入，CCX 不代替用户在 new-api 侧注册或登录。

**认证与凭据模型**：

new-api family 的管理接口鉴权（依据 all-api-hub 传输层实现）：

```text
Authorization: Bearer <access_token>     # 系统访问令牌；raw 模式可去掉 Bearer 前缀
New-API-User: <userId>                    # 当前用户 ID（必带）
User-id: <userId>                         # fork 兼容回退（Veloera/voapi/Super-API 等）
Content-Type: application/json
```

统一响应信封：`{ "success": bool, "data": T, "message": string }`；解析时先判 `success`，再取 `data`。

**SubscriptionProfile 扩展**：

在 §3.2.2 的 `SubscriptionProfile` 基础上，为可自动接入的订阅新增 provider 集成字段（沿用「默认关闭 + 显式 opt-in」守则）：

```go
// SubscriptionProfile 新增字段（复用 Phase 4 Item 6 的 SubscriptionAutoRefresh 开关）
type SubscriptionIntegration struct {
    Kind        string `json:"kind"`         // "new_api" | "openai" | "anthropic" | "google" | ...
    BaseURL     string `json:"baseUrl"`      // new-api 站点地址（同时作为新建渠道的上游 baseURL）
    AccessToken string `json:"-"`            // 系统访问令牌；加密存储，不出 API 响应
    UserID      string `json:"userId"`       // New-API-User 头所需
    AuthTokenMode string `json:"authTokenMode,omitempty"` // "bearer"(默认) | "raw"
    // 自动建 key 的模板
    ProvisionKeyName   string   `json:"provisionKeyName,omitempty"`   // 默认 "ccx-autopilot"
    ProvisionGroup     string   `json:"provisionGroup,omitempty"`     // 建 key 时指定分组，空=默认分组
    ProvisionModels    []string `json:"provisionModels,omitempty"`    // model_limits，空=不限制
    // 自动同步产物（回填，只读展示）
    ProvisionedTokenID int    `json:"provisionedTokenId,omitempty"`
    LinkedChannelUID   string `json:"linkedChannelUid,omitempty"`
}
```

`AccessToken` 必须与现有敏感字段一样走加密存储，序列化到前端时脱敏（仅显示尾部若干位）。

**new-api 接口清单**（本集成实际调用的子集，路径以主线 new-api 为准，字段名保持与上游一致）：

| 用途 | 方法 | 路径 | 请求体 | 响应关键字段 |
|------|------|------|--------|--------------|
| 校验令牌 + 查用户/余额 | GET | `/api/user/self` | — | `data.id`、`data.username`、`data.quota`（剩余额度）、`data.used_quota` |
| 拉分组倍率 | GET | `/api/user/self/groups` | — | `data: { <group>: { desc, ratio } }` |
| 站点全部分组 | GET | `/api/group` | — | `data: string[]` |
| 账号可用模型 | GET | `/api/user/models` | — | `data: string[]` |
| 创建代理 key | POST | `/api/token/` | `CreateTokenRequest`（见下） | `data.key` / `success` |
| key 列表 | GET | `/api/token/?p={p}&size={n}` | — | `data.items[]`（含 `id/key/name/status/remain_quota`） |
| 更新 key | PUT | `/api/token/` | `{ ...token, id }` | `success` |
| 删除 key | DELETE | `/api/token/{id}` | — | `success` |

`CreateTokenRequest`：

```json
{
  "name": "ccx-autopilot",
  "remain_quota": 0,
  "expired_time": -1,
  "unlimited_quota": true,
  "model_limits_enabled": false,
  "model_limits": "",
  "allow_ips": "",
  "group": ""
}
```

`quota` 是 new-api 内部额度单位（非美元），换算比例读 `GET /api/status` 的 `price` / `PaymentUSDRate`（无鉴权），或允许用户在订阅中手填换算率；MVP 直接以 quota 原值展示 + 站点换算率估算余额。

**接入流程**（订阅中心「新增订阅 → 类型选 new-api」）：

```text
1. 用户输入：站点 baseURL + 系统访问令牌(access_token) + userId
   （userId 可选自动获取：先用令牌调 /api/user/self 拿 data.id 回填）
2. 校验：GET /api/user/self
   ├─ success=false / 401 → 令牌无效或 userId 不匹配，报错停止
   └─ 成功 → 记录 username、quota，创建 SubscriptionProfile(provider=new_api, originTier=second)
3. 拉分组：GET /api/user/self/groups → 写入 GroupMultipliers（{group: ratio}）
          GET /api/user/models       → 记录账号可用模型，供渠道 supportedModels 参考
4. 建 key：POST /api/token/（按 ProvisionKeyName/Group/Models 模板）
   ├─ 已存在同名 ccx-autopilot key → 复用其 id，不重复创建（列表比对 name）
   └─ 新建成功 → 回填 ProvisionedTokenID，取 data.key 作为渠道 apiKey
5. 自动建渠道：用 baseURL + 新建 key 创建一个上游渠道
   ├─ channelKind 由用户选择或按站点探测（messages/chat/responses/gemini）
   ├─ status=unknown，autoManaged=true，OriginType=relay
   ├─ 建立 SubscriptionUID → ChannelUID 链接（回填 LinkedChannelUID）
   └─ 触发 §8.4 的 Discovery + 能力测试标准流程
6. 完成：订阅卡片显示余额、分组倍率、已绑定渠道、探测进度
```

关键点：new-api 集成**不额外造探测逻辑**，第 5 步之后完全复用现有 §8.4「添加并探测」链路；集成层只负责「令牌 → key + 分组 + 余额 + baseURL」的自动填充，把手填 baseURL/key 的动作自动化掉。

**刷新与同步**（复用 `POST /api/subscriptions/{uid}/refresh`）：

| 触发 | 动作 |
|------|------|
| 手动刷新余额 | `GET /api/user/self` 更新 `Balance` + `BalanceUpdatedAt` |
| 定时刷新（可选，opt-in） | 复用 Phase 4 Item 6 `SubscriptionAutoRefresh`；new-api 作为 `relay` provider adapter 纳入，默认关闭 |
| 分组倍率变更 | 刷新时对比 `/api/user/self/groups`，`GroupMultipliers` 变化 → 更新并提示「套餐倍率已变」 |
| key 失效检测 | 渠道侧探测到该 key 全 401 → 订阅卡片提示「key 可能已删除/禁用」，提供「重新建 key」一键动作（重跑第 4-5 步，复用旧渠道，只换 apiKey） |
| 可用模型变更 | `/api/user/models` 结果哈希变化 → 触发对应渠道重新 Discovery |

**安全与边界**：
- `access_token` 权限等同 new-api 账户，须加密存储、脱敏展示、禁止写入日志；刷新失败连续 N 次自动停用该订阅的自动刷新，只保留手动。
- 自动建 key 默认 `unlimited_quota=true` 但可由用户改为限额模板；建 key 是有副作用的写操作，须在接入前明确告知用户「将在 new-api 侧创建一个名为 ccx-autopilot 的令牌」。
- new-api 是 `OriginTier=second`，**不得**用作隐私敏感的 routing advisor / classifier（遵守 §3.2.1 约束）。
- fork 兼容：请求同时带 `New-API-User` 与 `User-id` 头；响应信封字段名（`success/data/message`）在部分 fork 可能不同，adapter 需容错解析，失败时回退手动录入。
- 一个 new-api 订阅默认只自动建/绑定一个渠道；用户如需该站多协议渠道，可在同订阅下手动增建，共用同一 key。

### 8.5.2 订阅 Provider Adapter 框架（多来源余额与接入）

§8.5.1 的 new-api 集成不是特例，而是「订阅 Provider Adapter」框架的一个实现。Phase 4 Item 6 已落地 openai/anthropic/google 的余额刷新 adapter，本框架把它抽象成统一接口，让 new-api 及后续来源（GitHub Copilot、Codex、Nvidia、MiniMax、DeepSeek 等）按相同契约接入，避免每加一个来源就散落一套逻辑（DRY）。

**统一接口**：

```go
// backend-go/internal/autopilot/subscription/adapter.go
type ProviderAdapter interface {
    Kind() string // "new_api" | "openai" | "anthropic" | "google" | "copilot" | "deepseek" | ...

    // 校验凭据，返回账号身份（用户名/账户标识）；失败即凭据无效
    Verify(ctx context.Context, cred Credential) (AccountInfo, error)
    // 查余额/额度；不支持返回 ErrUnsupported，由 UI 降级为手动录入
    FetchBalance(ctx context.Context, cred Credential) (Balance, error)
    // 拉分组倍率；仅 new-api 类中转有，官方直连返回 ErrUnsupported
    FetchGroups(ctx context.Context, cred Credential) (map[string]GroupRatio, error)
    // 自动创建代理 key；官方直连通常不支持（key 即用户 API key），返回 ErrUnsupported
    ProvisionKey(ctx context.Context, cred Credential, tmpl KeyTemplate) (ProvisionedKey, error)
    // 可用模型列表；用于渠道 supportedModels 参考
    FetchModels(ctx context.Context, cred Credential) ([]string, error)
}
```

每个能力独立、可选：adapter 只实现自己支持的部分，不支持的统一返回 `ErrUnsupported`（接口隔离原则），UI 据此隐藏/降级对应入口。所有 adapter 共用 `SubscriptionAutoRefresh` 的 opt-in 开关与 `Validate()` 兜底，与 Phase 4 现有防御模式一致。

**能力矩阵**（✅ 支持 / ⚠️ 部分或需确认 / ❌ 不支持，走手动）：

| 来源 | Kind | 验证 | 查余额 | 拉分组 | 自动建 key | OriginTier |
|------|------|:----:|:------:|:------:|:----------:|------------|
| new-api family | `new_api` | ✅ | ✅ quota | ✅ | ✅ | second |
| OpenAI 官方 | `openai` | ✅ | ⚠️ 计费 API | ❌ | ❌ | first |
| Anthropic 官方 | `anthropic` | ✅ | ⚠️ 计费 API | ❌ | ❌ | first |
| Google 官方 | `google` | ✅ | ⚠️ | ❌ | ❌ | first |
| GitHub Copilot | `copilot` | ⚠️ token | ❌ 无额度概念 | ❌ | ❌ | first |
| Codex plan | `codex` | ⚠️ | ⚠️ 用量窗口 | ❌ | ❌ | first |
| Nvidia NIM | `nvidia` | ✅ | ⚠️ credits | ❌ | ❌ | first |
| MiniMax | `minimax` | ✅ | ⚠️ | ❌ | ❌ | first |
| DeepSeek | `deepseek` | ✅ | ✅ balance API | ❌ | ❌ | first |

说明：官方直连来源普遍**不支持自动建 key**（用户的 API key 本身就是凭据，直接作为渠道 apiKey 录入即可）、也**没有分组倍率**（价格按官方定价表）。它们的 adapter 价值集中在「验证 + 查余额/用量窗口」，把 §3.2.4 UsageWindow 的官方用量 API 来源坐实。DeepSeek 有独立余额查询端点，可完整支持余额。Copilot 属订阅制、无 token 余额概念，只做验证 + 用量提示。

**接入分层**（YAGNI：不强求一次做全，按价值排期）：

```text
P0（本轮）：new_api（完整：验证+余额+分组+建key+自动建渠道）
            —— 覆盖用户最主要的中转站场景
P1：deepseek（验证+余额）、官方三家余额已在 Phase 4 完成 → 补 UsageWindow 对齐
P2：nvidia / minimax（验证+credits/余额）
P3：copilot / codex（验证+用量窗口提示，无余额）
```

每个来源的具体端点在实现时于 `adapter.go` 各 provider 文件内落地，本设计只固定接口契约与能力矩阵，避免文档与上游 API 变动强耦合。新增来源 = 实现 `ProviderAdapter` + 在能力矩阵登记，不改框架。

**能力缺失与降级**：
- adapter 任一能力返回 `ErrUnsupported` → 订阅中心对应字段回退为手动录入（余额手填、倍率手填），不报错、不阻塞接入。
- adapter 验证失败/超时 → 订阅可仍以「手动模式」创建，仅失去自动刷新与自动建 key，其余流程（手填 baseURL+key 建渠道）不受影响。
- 官方来源无「自动建 key」时，接入流程退化为 §8.4 标准快速添加：用户填 API key，adapter 只补验证与余额。
- 所有自动刷新遵守 `SubscriptionAutoRefresh` 默认关闭；连续失败自动停用自动刷新并在驾驶舱「人工待办」提示。

### 8.6 管理面板

管理面板新增 Autopilot 策略页，承载全局开关，不进入日常渠道运营路径：

| 区块 | 控件 | 默认 |
|------|------|------|
| 自动托管 | `defaultAutoManaged`、`autoDiscoveryOnAdd` | 开启 |
| 可信 advisor | 一等官方 API、本地 Ollama/LM Studio/llama-server、shadow 准确率门槛 | 只 shadow |
| 信任策略 | 未知来源处理、信任等级 tie-breaker 权重、来源混杂提醒 | 未知只观察 |
| 质量保护 | `capabilityFloorEnabled`、`protectVisionChannels`、`protectLongContextChannels` | 开启 |
| 成本优化 | `costOptimization.enabled`、最低置信度、省钱权重 | 开启但在质量下界后生效 |
| RPM 发现 | header 优先、AIMD、maxAutoRPM、手动配置优先 | 开启 shadow，Phase 2 生效 |
| 探测预算 | L2 每日预算、公益站降频、并发限制 | 保守 |

管理面板只保存全局策略。单个渠道来源、订阅、key 倍率仍从渠道中心或订阅中心编辑，避免入口重复。

本地模型设置：
- 支持快速添加 Ollama、LM Studio、llama-server、OpenAI-compatible runtime。
- 每个 runtime 可选择角色：`候选模型`、`路由辅助`、`两者都启用`。
- advisor 选择器只列出 `OriginTier=first` 的官方上游和 `OriginTier=local` 的本地运行时。
- 官方 API/token plan 作为 advisor 时需要展示预计额外 token 成本；本地 runtime 作为 advisor 时展示本机延迟和 tokens/sec。
- 中转站、公益站、未知来源在 UI 中显示为“不可用于隐私判定”，但仍可作为候选执行上游。
- 路由辅助必须先进入 shadow 模式，展示与真实路由结果的差异、误判样例和平均耗时。
- advisor 超时、不可用或准确率低于门槛时自动关闭真实影响，只保留诊断展示。

### 8.7 驾驶舱

驾驶舱是运营总览，不替代渠道中心。它需要回答四个问题：

```text
1. 当前请求是否健康：成功率、断流、限流、死亡渠道数
2. 当前是否在省钱：实际有效成本、节省估算、低价 endpoint 命中率
3. 当前是否有质量风险：主代理是否降级、vision/long-context 是否缺可用渠道
4. 当前是否需要人工：余额低、来源混杂、成本配置缺失、RPM 未发现
```

驾驶舱卡片：
- **来源分布**：一等/二等/三等/本地/未知来源数量、流量占比、成功率对比。
- **实时最佳上游**：按任务类型展示当前胜出的 endpoint，说明胜出原因：质量、稳定、低成本、低延迟。
- **低信任高表现提醒**：公益/中转在最近 15m 表现优于官方来源时展示，但标注这是短期运行质量，不改变信任等级。
- **活跃试用**：列出 `ManualRoutingIntent`，显示命中请求数、预算剩余、成功率和 fallback 次数。
- **本地模型与可信 advisor**：展示本地 candidate 命中量、估算节省、advisor shadow 准确率、额外判定成本和误判样例。
- **成本节省**：按模型和任务类型展示 `official baseline` vs `effective actual`。
- **人工待办**：余额低、倍率缺失、来源未知、endpoint 能力不一致、自动 RPM 置信度低。

驾驶舱所有「修复」动作跳转到对应页面：
- 余额/倍率 → 订阅中心
- endpoint 健康/能力 → 渠道中心
- 全局策略 → 管理面板
- 调度解释 → SmartRouter dry-run

---

## 9. 配置设计

### 9.1 全局智能路由配置

在 `config.json` 新增顶层字段：

```json
{
  "smartRouting": {
    "enabled": true,
    "mode": "auto",
    "killSwitch": false,

    "defaultAutoManaged": true,
    "autoDiscoveryOnAdd": true,

    "subagentUseCheapPool": true,
    "unknownChannelPolicy": "observe",
    "premiumFallbackForSubagent": false,
    "protectVisionChannels": true,
    "protectLongContextChannels": true,

    "healthCheck": {
      "enabled": true,
      "passiveSignalsOnly": false,
      "l2ProbeEnabled": true,
      "l2ProbeIntervalMinutes": 120,
      "l2ProbeMaxPerDay": 12,
      "deadProbeIntervalHours": 6,
      "deadConfidenceThreshold": 0.80,
      "autoExcludeDead": true
    },

    "fastDecay": {
      "enabled": true,
      "applyToCostTiers": ["free", "cheap"],
      "applyToPoolTags": ["temp"],
      "recoveryRate": 0.15,
      "decayBase": 0.85,
      "streamBreakDecayBase": 0.70
    },

    "rateLimitDiscovery": {
      "enabled": true,
      "applyOnlyWhenUnset": true,
      "preferHeaders": true,
      "passiveAimdEnabled": true,
      "minRpm": 1,
      "maxAutoRpm": 120,
      "maxAutoTpm": 200000,
      "maxAutoRpd": 5000,
      "maxAutoConcurrent": 8,
      "confidenceThreshold": 0.6,
      "increaseIntervalMinutes": 10,
      "increaseStepPercent": 10,
      "decreaseOn429Percent": 50
    },

    "modelMapping": {
      "autoResolve": true,
      "capabilityFloorEnabled": true,
      "echoMappedModel": true,
      "forbidChainMapping": true
    },

    "costOptimization": {
      "enabled": true,
      "applyAfterQualityFloor": true,
      "requireCostConfidence": 0.6,
      "includeCachePricing": true,
      "includeImageUnitPricing": true,
      "includeEmbeddingPricing": true,
      "currency": "USD",
      "exchangeRateSource": "manual",
      "preferLowerEffectiveCost": true,
      "supervisorSavingsWeight": 0.5,
      "workerSavingsWeight": 3,
      "providerTimePricing": {
        "deepseek": {
          "effectiveFrom": "2026-07-20T00:00:00+08:00",
          "timeZone": "Asia/Shanghai",
          "peakMultiplier": 2,
          "peakWindows": [
            { "start": "09:00", "end": "12:00" },
            { "start": "14:00", "end": "18:00" }
          ]
        }
      }
    },

    "originPolicy": {
      "unknownOriginPolicy": "observe",
      "preferHigherOriginTierAsTieBreaker": true,
      "originTieBreakerWeight": 0.2,
      "showLowerTierOutperforming": true,
      "warnMixedOriginChannel": true
    },

    "modelFamilyPreference": {
      "enabled": true,
      "weight": 0.2,
      "globalOrder": ["claude", "openai", "deepseek", "gemini", "qwen", "kimi", "glm"],
      "perTaskClass": {
        "supervisor":      ["claude", "openai"],
        "worker":          ["openai", "deepseek", "claude", "qwen", "kimi", "glm"],
        "lightweight":     ["openai", "deepseek", "qwen", "gemini", "claude"],
        "vision":          ["claude", "openai", "gemini"],
        "long_context":    ["claude", "openai", "deepseek", "qwen"],
        "image_generation": ["openai", "agnes", "volcengine"],
        "embedding":       ["openai", "qwen"]
      }
    },

    "costPreference": {
      "mode": "balanced",
      "perTaskClass": {
        "supervisor": "quality_first",
        "worker": "cost_first",
        "lightweight": "cost_first"
      },
      "custom": {
        "savingsMultiplier": 1.0,
        "providerQualityMultiplier": 1.0
      }
    },

    "taskDomainStrength": {
      "enabled": true,
      "weight": 0.5,
      "seedMatrixOverrides": {
        "openai/gpt-5.x": { "aesthetics_ui": 0.6, "code_review": 0.9 }
      }
    },

    "reasoningEffort": {
      "enabled": true,
      "expandVariants": true,
      "perTaskClass": {
        "supervisor": ["high", "max"],
        "worker": ["medium"],
        "lightweight": ["off", "minimal"],
        "long_context": ["medium", "high"]
      },
      "respectClientThinking": true
    },

    "manualIntent": {
      "enabled": true,
      "defaultTtlMinutes": 120,
      "maxTtlHours": 24,
      "defaultMaxRequests": 100,
      "maxTrafficPercentForThirdTier": 25,
      "requireConfirmForSupervisor": true,
      "fallbackOnFailureDefault": true
    },

    "trustedRoutingAdvisor": {
      "enabled": true,
      "mode": "shadow",
      "allowedAdvisorOriginTiers": ["first", "local"],
      "forbidAdvisorOnRelayOrCommunity": true,
      "advisorRuntimeUid": "",
      "advisorChannelUid": "",
      "advisorTimeoutMs": 1200,
      "maxAdvisorPromptTokens": 1200,
      "minAdvisorConfidence": 0.85,
      "minShadowSamples": 500,
      "minShadowAccuracy": 0.90,
      "maxCriticalMisrouteRate": 0.01,
      "maxFalseDemotionRate": 0.03,
      "promotionMode": "manual",
      "neverDemoteTaskClasses": ["supervisor", "vision", "long_context"],
      "forbidAutoDecomposeAndMerge": true,
      "redactSensitiveMetadata": true,
      "recordOnlyHashedPrompt": true,
      "retainDecisionRecordsDays": 7,
      "autoRollbackOnSloRegression": true,
      "failOpenOnAdvisorError": true
    },

    "localModelRouting": {
      "enabled": true,
      "mode": "shadow",
      "allowLocalForTaskClasses": ["lightweight", "worker"],
      "neverDemoteTaskClasses": ["supervisor", "vision", "long_context"],
      "forbidAutoDecomposeAndMerge": true,
      "maxLocalCandidateLatencyMs": 3000,
      "maxLocalConcurrent": 2,
      "maxLocalPromptTokens": 32000,
      "remoteLocalRuntimeAllowed": false,
      "requireLocalRuntimeAuth": true
    },

    "taskStrategies": {
      "supervisor": {
        "preferQuality": ["high", "premium"],
        "requireStability": ["stable", "normal"],
        "excludeTags": ["unstable", "free"]
      },
      "worker": {
        "preferCost": ["free", "cheap"],
        "preferSpeed": ["fast"],
        "excludeQuality": ["premium"]
      },
      "image_generation": {
        "preferHealth": ["healthy"],
        "preferCost": ["cheap", "normal"],
        "requireNativeImagesEndpoint": true,
        "forbidChatToImageConversion": true
      },
      "embedding": {
        "preferCost": ["free", "cheap"],
        "preferSpeed": ["fast"],
        "requireNativeEmbeddingsEndpoint": true,
        "preserveEmbeddingDimension": true
      }
    }
  }
}
```

`providerTimePricing.effectiveFrom` 使用 RFC3339；修改该值即可调整激活时间，设为空字符串则停用对应规则。

### 9.2 渠道级配置

现有 `UpstreamConfig` 新增字段：

```go
type UpstreamConfig struct {
    // ... 现有字段 ...

    // ── 稳定身份 ──
    ChannelUID string `json:"channelUid,omitempty"` // 渠道稳定 ID，ConfigManager 负责补齐旧配置
    SubscriptionUID string `json:"subscriptionUid,omitempty"` // 关联订阅/套餐，由订阅中心维护

    // ── 自动托管 ──
    AutoManaged       bool   `json:"autoManaged,omitempty"`       // 启用自动托管
    AutoManagedAt     *time.Time `json:"autoManagedAt,omitempty"` // 开始托管时间
    OriginType        string `json:"originType,omitempty"`         // official_api/official_token_plan/relay/community/local_runtime/unknown
    OriginTier        string `json:"originTier,omitempty"`         // first/second/third/local/unknown，信任/隐私等级
    BillingMode       string `json:"billingMode,omitempty"`        // official_api/token_plan/prepaid_credit/shared_free/unknown
    CostHint          string `json:"costHint,omitempty"`          // 用户成本提示：free/cheap/normal/expensive
    GroupMultipliers  map[string]float64 `json:"groupMultipliers,omitempty"` // channel 默认分组倍率，key 可覆盖
    RechargeMultiplier float64 `json:"rechargeMultiplier,omitempty"` // channel 默认充值倍率，key 可覆盖
    QualityHint       string `json:"qualityHint,omitempty"`       // 用户质量提示（override 自动推导）
    PoolTag           string `json:"poolTag,omitempty"`           // 池标签：temp/regular/premium
    RoutingPriority   string `json:"routingPriority,omitempty"`   // 路由优先级 hint
}
```

**用户覆盖规则**：
- 用户手动设置的字段（QualityHint/CostHint/PoolTag）优先级高于自动推导
- 但自动推导的运行时指标（健康状态/熔断）始终生效，不受 override 影响
- 用户可通过 `autoManaged: false` 退出自动托管，回到手动模式

### 9.3 Key 级成本倍率配置

用户添加 `baseUrls[]` 和 `apiKeys[]` 后，必须能给每个 key 标注成本倍率。成本倍率属于 key 级语义，因为同一 baseURL 下不同 key 可能属于不同分组、租户或充值批次。

扩展 `APIKeyConfig`：

```go
type APIKeyConfig struct {
    // ... 现有字段 ...

    // 成本分组名称，仅用于 UI 展示和批量编辑；不要与 QuotaGroup 混用。
    CostGroup string `json:"costGroup,omitempty"`

    // 分组倍率：key 为模型组或通配符，例如 "*"、"claude-opus"、"gpt-5"、"gemini"。
    // 未命中时回退 channel.GroupMultipliers，再回退 1.0。
    GroupMultipliers map[string]float64 `json:"groupMultipliers,omitempty"`

    // 充值倍率：1.0=无折扣；2.0=付 1 得 2，真实成本减半。
    // 未设置时回退 channel.RechargeMultiplier，再回退 1.0。
    RechargeMultiplier float64 `json:"rechargeMultiplier,omitempty"`

    // 可选：用户直接给出的成本备注，例如 "8折企业组/双倍充值"。
    CostNote string `json:"costNote,omitempty"`
}
```

配置示例：

```json
{
  "baseUrls": ["https://proxy-a.example/v1", "https://proxy-b.example/v1"],
  "apiKeys": ["sk-key-a", "sk-key-b"],
  "apiKeyConfigs": [
    {
      "key": "sk-key-a",
      "name": "A 组",
      "costGroup": "vip-a",
      "groupMultipliers": { "*": 1.0, "claude-opus": 1.2 },
      "rechargeMultiplier": 1.0
    },
    {
      "key": "sk-key-b",
      "name": "充值活动组",
      "costGroup": "promo-b",
      "groupMultipliers": { "*": 1.0, "claude-opus": 1.0 },
      "rechargeMultiplier": 2.0
    }
  ]
}
```

解释：
- `sk-key-a` 的 Opus 成本是官价 `1.2x`。
- `sk-key-b` 的 Opus 成本是官价 `1.0 / 2.0 = 0.5x`。
- 如果两者质量、健康、上下文和能力都满足，worker/lightweight 请求优先 `sk-key-b`。

`baseUrls[] × apiKeys[]` 会生成多个 endpoint，但同一个 key 的成本配置默认应用到所有 baseURL。如果同一个 key 在不同 baseURL 上有不同价格，优先建议拆成两个 channel；不要在 MVP 中加入 baseURL 级成本 override，避免配置复杂度失控。

### 9.4 endpoint 级自动结果的落点

现有 `UpstreamConfig.ModelMapping`、`SupportedModels`、兼容开关都是 channel 级字段，而本方案的发现结果是 endpoint 级。落地规则：

```text
所有 endpoint 一致：
  可安全写入 channel 级 ModelMapping / SupportedModels / 兼容开关

endpoint 间不一致：
  写入 ProfileStore 的 KeyEndpointProfile / ModelProfile
  UI 显示不一致警告
  请求路径通过 EndpointAttemptPolicy 做 request-scoped 决策
  不自动覆盖 channel 级 ModelMapping / SupportedModels
```

这避免一个 channel 内 key-A 支持 Claude 系列、key-B 支持 GPT 系列时，自动配置把 channel 级模型白名单写成错误并集。

### 9.5 后台任务生命周期

Autopilot 需要一个后台 worker，但必须是保守、可停止、可观测的：

```text
启动：
  - 加载 ProfileStore
  - 为旧 UpstreamConfig 补齐 ChannelUID
  - 同步当前配置 index 快照
  - 启动 L1 聚合、L2 探测队列、过期数据清理

配置热重载：
  - 新 ChannelUID：创建 unknown profile
  - baseURL/apiKey/serviceType 变化：重算 endpoint 集合，新增 unknown，旧 endpoint 标记 orphaned
  - channel 重排：只更新 channel_id 展示快照，不迁移画像主键
  - channel 删除：标记 orphaned，延迟清理画像/证据/快照

关闭：
  - 停止探测队列
  - flush 内存时间桶
  - 不中断正在处理的真实用户请求
```

多实例部署先按“best effort”处理：Phase 1 不做跨进程锁；Phase 2 若要自动 L2/L3 探测，需要在 SQLite 增加 `autopilot_jobs(locked_until, owner)`，避免多个进程重复烧 quota。

---

## 10. 分阶段落地计划

### Phase 1：自动画像 + 健康诊断（MVP）

**目标**：用户能看到渠道健康状态（精确到 endpoint 级），系统自动推导画像，但不改变真实调度结果。Phase 1 是 shadow/read-only 阶段。

**范围**：
- [ ] `ChannelUID` 稳定身份补齐 + 配置热重载同步
- [ ] KeyEndpointProfile / ChannelProfile(聚合) / ModelProfile 数据模型 + SQLite 存储
- [ ] SubscriptionProfile 数据模型 + 渠道 `SubscriptionUID` 关联（仅手动维护，不做余额自动抓取）
- [ ] LocalModelRuntimeProfile 数据模型 + Ollama/LM Studio/llama-server 手动注册和探测
- [ ] ManualRoutingIntent 数据模型 + 试用结果存储（shadow/read-only，不改变真实调度）
- [ ] Profiler 画像推导（基于现有 MetricsManager + 模型注册表，L1 被动信号为主，endpoint 级粒度）
- [ ] CostProfile shadow 计算（key 级分组倍率/充值倍率 → endpoint 有效成本，仅展示）
- [ ] Images/Vectors 原生画像：仅记录健康、延迟、模型/维度/operation、成本和 RPM，不做 chat 协议转换
- [ ] HealthAnalyzer 健康诊断（被动优先：L1 为主，endpoint 级判定 + channel 级聚合）
- [ ] FastDecay shadow score（只展示/诊断，不参与调度）
- [ ] RateLimitDiscoverer shadow profile（解析响应头和 429 模式，只展示建议 RPM，不改 limiter）
- [ ] GroupChangeDetector 快照对比（仅手动刷新或已有 discovery/capability-test 后更新，不自动重探测）
- [ ] 健康中心 API（`/api/health-center/*`，支持 endpoint 级展开）
- [ ] 前端健康中心视图
- [ ] 订阅中心基础视图（套餐/来源/倍率/绑定渠道）
- [ ] 驾驶舱只读总览（来源分布、待办、成本 shadow 估算）
- [ ] TrustedRoutingAdvisor shadow 评估（不影响真实调度）
- [ ] 渠道卡片健康 badge 增强（显示 endpoint 级不一致警告）
- [ ] 标签系统（官方/中转/公益、白嫖池/临时池/高智商稳定等）

**不做的事**：
- 不修改调度器核心链路
- 不让 FastDecay / HealthState 影响真实调度
- 不让 CostProfile 影响真实调度
- 不让自动发现的 RPM 影响真实 limiter
- 不自动触发 L2/L3 探测
- 不自动写入 modelMapping
- 不做模型自动映射

**预估工期**：2-3 周

### Phase 2：自动发现 + 智能调度

**目标**：添加渠道时自动发现，调度时能按 channel + endpoint 画像选择，但仍不做动态模型自动映射。

**范围**：
- [ ] `autoManaged` 字段 + 快速添加流程
- [ ] Discovery 自动触发（复用现有 channel_discovery 逻辑）
- [ ] 自动写入一致的 channel 级 modelMapping / supportedModels / 兼容开关；不一致结果保留在 ProfileStore
- [ ] SmartRouter + CandidateFilter 注入（保留 X-Channel/manual override/promotion 优先级）
- [ ] EndpointAttemptPolicy 注入 `TryUpstreamWithAllKeys`，支持 endpoint 级 baseURL/key 排序
- [ ] Cost-aware routing：满足质量/能力下界后，按 `estimatedRequestCost` 降低总费用
- [ ] Origin-aware tie-breaker：仅在同质量/同健康/同成本档时使用信任等级，不把信任等级当质量
- [ ] FastDecay 从 shadow score 切换为 endpoint 调度降权
- [ ] RateLimitDiscoverer 对未显式设置 RPM 的 endpoint 应用 runtime limiter
- [ ] L2 轻量探测 worker + 每日预算 + 探测队列
- [ ] TaskClassifier + 七种任务策略（supervisor/worker/lightweight/vision/long_context/image_generation/embedding）
- [ ] 智能路由诊断 API（dry-run）
- [ ] ManualRoutingIntent 执行：model_trial/channel_trial/endpoint_trial/session_pin
- [ ] TrustedRoutingAdvisor 只对 lightweight/worker 低风险请求提供真实路由 hint
- [ ] 本地 candidate_model 可进入 endpoint 候选，但必须满足硬约束和 shadow 门槛
- [ ] `/v1/images/*` 和 `/v1/embeddings` 接入 endpoint 排序；只选择原生上游，不做 chat 画图转换
- [ ] 前端自动托管指示器 + 快速添加 UI

**预估工期**：3-4 周

### Phase 3：动态画像 + 自愈

**目标**：画像实时更新，渠道自动恢复。

**Phase 3A 状态（2026-07-09）**：低风险、不改真实路由语义的子集已完成并接入真实链路——`Manager.collectAll()` 新旧画像对比产出变更事件（`DetectProfileChanges`）、`ProfileChangelogStore`（环形内存 + SQLite，30 天机会性清理）、`EventHub` 内存 pub/sub、`GET /api/health-center/changelog`（历史）+ `GET /api/health-center/events`（WebSocket 实时推送）、`AutoDiscoveryRunner` 发布 `discovery_completed`/`auto_mapping_applied`、前端 `ProfileChangelogTimeline.vue` 接入健康中心页并支持断线自动重连。均为只读展示，未修改 `SmartRouter`/`EndpointAttemptPolicy` 调度逻辑。落地时顺带发现并修复一个真实缺口：浏览器原生 WebSocket 无法设自定义 header，为此给 `middleware.getAPIKey` 增加了 `Sec-WebSocket-Protocol` 鉴权回退（业界标准做法，仅新增一个 key 来源，不降低现有校验）。

**Phase 3B（已全部完成，2026-07-09）**：模型自动映射、自动恢复探测、晋升/降级机制会直接改变真实调度决策，风险明显高于 3A，四个子项已分批全部完成。§12.2 P1.5 的 SQLite schema version / 版本化 migration / `OriginType`&`OriginTier` 旧配置 backfill / `killSwitch`+按 task class/channel disable **已于 2026-07-09 补强完成**（详见 §12.2 P1.5）。**Phase 3B-1（自动恢复探测补全）已于 2026-07-09 完成**：修复了 L2 探测子系统里此前从未打通的两个真实 bug——① `collectAll()` 每轮把新构造的画像整行 Upsert，会把 `ProbeWorker` 刚写入的 `LastProbeAt`/`ProbeSuccess`/`ConsecutiveProbeSuccess` 等字段清零，导致 6 小时探测冷却期形同虚设（修复：`carryForwardProbeFields` 在 Upsert 前搬运 Probe* 字段，不影响 L1 诊断字段的现有覆盖逻辑）；② `extractRawAPIKey` 是返回 `KeyMask` 掩码值的占位 stub，探测请求实际带的是假 key（修复：新增 `APIKeyResolver` 回调 + `Manager.ResolveAPIKey`，通过遍历配置里对应渠道的 `APIKeys` 逐个计算 `KeyHashFromAPIKey` 比对来还原明文；resolver 未注入或未命中时 fail-open 跳过探测且不消耗预算）。同时补齐了 `degraded/limited → healthy` 的连续成功恢复路径（新增 `KeyEndpointProfile.ConsecutiveProbeSuccess` 计数器 + `HealthCheckConfig.ProbeRecoveryThreshold`，默认连续 2 次探测成功才恢复，避免单次探测噪声导致状态抖动；探测失败计数器清零）。**Phase 3B-3（StabilityTier 晋降级滞后）已于 2026-07-09 完成**（提交 664d3fff）：在原本每轮从瞬时 1h 窗口重算的无记忆 `StabilityTier` 上加一层连续窗口滞后——新增 `KeyEndpointProfile.EffectiveStabilityTier`/`StabilityPendingTier`/`StabilityPendingStreak` 三字段 + 纯函数 `applyStabilityHysteresis`（对称阈值：连续 N 轮同值才切换生效档，`HealthCheckConfig.StabilityHysteresisWindows` 默认 2；首次画像直接采用，不等待阈值，避免新渠道卡在零值），`AggregateChannelProfile` 改读 `EffectiveStabilityTier`（空则回退 `StabilityTier`）。晋升/降级机制的数据模型仅限于 StabilityTier，不改变 QualityTier/SpeedTier/CostTier 语义。**Phase 3B-2（模型自动映射闭环）已于 2026-07-09 完成**（提交 0f02e988，随后修复一处 critical 集成 bug，提交 856372c4）：新建 `ModelProfileStore`（SQLite，`model_profiles` 表）+ 扩展 `AutoDiscoveryRunner` 为每个发现模型写入 `ModelProfile`（仅 `AutoManaged` 渠道）；新建 `ModelResolver` 实现 §5.4 五步逻辑（手动 `RedirectModel` 短路 → `GetModelProfiles` → `filterByCapabilityFloor` → `rankBySimilarity`）；`scheduler` 包新增 `ModelSupportResolverFunc` hook（fail-open，未注入或返回不支持时严格回退到原 `ExplainModelSupport`）；`endpoint_policy.go` 的 `resolveMappedModel`（此前是永不触发的死代码——`profile.ModelMapping` 从未被写入）现调用 `ModelResolver`；`handlers/common` 在 `RedirectModel` 之后应用 `MappedModel` 覆盖请求体，并按 `EchoMappedModel` 配置回显 `X-CCX-Mapped-Model`/`X-CCX-Original-Model`/`X-CCX-Mapping-Source` 响应头。三条件安全门控：`ModelMapping.AutoResolve==true`（**默认由死配置的 true 改为 false**）AND `RoutingMode ∈ {assist, auto}` AND 无 `KillSwitch`，三者任一不满足则所有新增代码路径行为与今天字节级一致；且仅 `AutoManaged` 渠道触发，手动配置渠道完全不受影响。对抗性复核发现并修复一个真实 critical bug：`scoreEndpointForKey` 用空 `channelUID` 计算 `EndpointUID` 并写入 `modelByUID`，而 `handlers` 层用真实 `channelUID` 查 `modelByUID`，两者哈希永不相等导致 `MappedModel` 在 key 级调度路径上完全查不到（提交 856372c4 修复：命中画像后改用 `profile.EndpointUID`，新增回归测试锁定该不变量）。至此 Phase 3B 四个子项（自动恢复探测、StabilityTier 滞后、模型自动映射、active filter 前置判定）全部完成；SLO regression 自动回滚仍是独立子系统，留待后续单独立项。

**范围**：
- [x] 运行时指标驱动画像实时更新（Phase 3A：`collectAll()` 每轮 diff + changelog + WebSocket 推送）
- [x] 模型自动映射（ModelResolver + ModelSupportResolver + request-scoped mappedModel）—— Phase 3B-2（2026-07-09 完成，提交 0f02e988 + 856372c4）
- [x] active model filter / context filter 支持自动映射前置判定 —— Phase 3B-2：`scheduler.ModelSupportResolverFunc` fail-open 接入 `select.go`
- [x] 自动恢复探测（limited/dead → healthy）—— Phase 3B-1（2026-07-09 完成）：`APIKeyResolver` + `carryForwardProbeFields` + `ConsecutiveProbeSuccess`/`ProbeRecoveryThreshold`
- [x] 晋升/降级机制（连续成功→升级，连续失败→降级）—— Phase 3B-3（2026-07-09 完成，提交 664d3fff）：`StabilityTier` 连续窗口滞后（`EffectiveStabilityTier`），暂不含 QualityTier/SpeedTier/CostTier
- [x] WebSocket 推送画像变更事件（Phase 3A：`GET /api/health-center/events`）
- [x] 前端画像变更历史/时间线（Phase 3A：`ProfileChangelogTimeline.vue`）

**预估工期**：2-3 周（3A、3B-1、3B-2、3B-3 均已完成；advisor 与全局 auto 的 SLO 回滚均已补齐）

> 状态校正（2026-07-17）：上方 Phase 3B 历史说明中的“SLO regression 自动回滚待单独立项”已完成；advisor 回滚与全局 auto 上线闸门/自动降级是两个独立保护层。

### Phase 4：高级特性

**Phase 4 状态（2026-07-09，全部完成）**：8 项全部实现并接入真实链路，均遵循"默认关闭 + 显式 opt-in"安全守则。

**范围**：
- [x] 多维度标签系统扩展（用户自定义标签）——Item 1
- [x] 成本报表：按用户/模型/渠道/key 统计真实有效成本——Item 2（新增 `proxyKeyMask` 身份贯穿链路：`middleware.GetProxyKeyMask` → `ChannelLog`/`RequestRecord` → SQLite `proxy_key_mask` 列，供 Item 4 复用）
- [x] Trusted advisor SLO regression 自动回滚——Item 3（默认关闭；`SLORollbackConfig`，连续 degrading 窗口数默认 3，不做自动恢复以避免震荡）
- [x] 渠道推荐：根据使用模式推荐新渠道——Item 4
- [x] 批量渠道管理（导入/导出/模板）——Item 5
- [x] 订阅中心 provider adapter：可选自动刷新余额/套餐状态——Item 6（默认关闭；仅 openai/anthropic/google 且显式填写 BillingAPIKey 的订阅生效，中转/公益渠道继续手动维护）
- [x] 本地任务模板：用户显式配置可本地预处理/摘要的固定模板——Item 7
- [x] A/B 测试：低比例统计抽样双发——Item 8（默认关闭，默认 1% 抽样 + 每小时 60 次硬预算；主请求路径不受影响，影子请求在主响应返回后异步发起）

**关键实现说明**：
- Item 3/6/8 均在 `AutopilotRoutingConfig` 新增独立配置块（`SLORollback`/`SubscriptionAutoRefresh`/`ABTest`），`Validate()` 统一做负值/越界兜底，保持与既有字段一致的防御模式。
- Item 4 直接复用 Item 2 的 `proxyKeyMask` 身份标记，未重新发明身份识别机制。
- **全局 auto 安全闸门（2026-07-17）**：请求终态按 15 分钟窗口无偏聚合，详细 trace 继续抽样；管理 API 只在 shadow/assist 已积累至少 500 个有效请求、24 小时连续观测且成功率/fallback/fail-open/p95 均达标时允许切换 `auto`。后台连续检测三个已完成 auto 窗口，相对最近七天安全模式基线持续恶化时，持久化降级到 `assist` 并记录 `autopilot_auto_safety_events`，不自动恢复。

---

## 11. 关键代码锚点

### 11.1 需要扩展的文件

| 文件 | 行号 | 扩展内容 |
|------|------|---------|
| `config/config.go` | `UpstreamConfig` / `APIKeyConfig` | 新增 ChannelUID/SubscriptionUID/OriginType/OriginTier/BillingMode/AutoManaged/CostHint/QualityHint/PoolTag，以及 key 级 GroupMultipliers/RechargeMultiplier |
| `config/config_loader.go` | 配置加载/保存 | 为旧配置补齐 ChannelUID，并保证重排不改变 UID |
| `scheduler/select.go` | `SelectChannelWithOptions` | 调整人工意图/显式控制与 CandidateFilter 顺序；支持 ModelSupportResolver 前置判断 |
| `scheduler/recovery.go` | `SelectionOptions` | 必要时携带 protected channels / routing hints |
| `handlers/common/multi_channel_failover.go` | `HandleMultiChannelFailover*` | 构建 RequestProfile，传入 SmartRouter plan |
| `handlers/common/upstream_failover.go` | `TryUpstreamWithAllKeys` | 新增 WithEndpointAttemptPolicy，按 endpoint 画像排序 baseURL/key |
| `keypool/keyconfig.go` | `CandidatesForModel` | 支持 endpoint policy 过滤/排序后的 key 候选 |
| `ratelimit/hints.go` | `ApplyUpstreamHints` | 复用现有限流响应头解析；补充 limit/window 信号输出给 RateLimitDiscoverer |
| `ratelimit/limiter.go` | `ChannelLimiter` | 支持从 discovered runtime config 更新 RPM，但不覆盖手动配置 |
| `metrics/channel_metrics.go` | `MetricsManager` | 新增画像相关查询方法 |
| `metrics/cost.go` | 成本估算 | 支持 CostProfile 的 effective multiplier、embedding token price 和 image unit price，不只使用模型官价 |
| `providers/` | 本地 provider adapter | 增加 Ollama native、LM Studio、llama-server/OpenAI-compatible adapter |
| `handlers/channel_discovery.go` | 全文 | Profiler 复用其探测逻辑 |
| `handlers/capability_test_runner.go` | `runCapabilityTestJob` | 测试结果写入 ModelProfile |
| `handlers/images_*` / `handlers/vectors_*` | 原生 images/vectors handlers | 构建 RequestProfile，保留 handler 硬约束，接入 SmartRouter endpoint 排序 |
| `services/api.ts` / `api-types.ts` | 前端 API 类型 | 增加 SubscriptionProfile、origin 字段、dashboard 聚合字段 |
| `router/index.ts` / `App.vue` | 前端导航 | 增加订阅中心、管理面板、驾驶舱入口 |

### 11.2 需要新增的文件

```text
backend-go/internal/autopilot/
├── key_endpoint_profile.go    # KeyEndpointProfile 类型（画像最小单元）
├── channel_profile.go         # ChannelProfile 聚合视图
├── model_profile.go           # ModelProfile 类型
├── request_profile.go         # RequestProfile + TaskClassifier
├── profile_store.go           # SQLite 持久化 + 内存缓存（endpoint 级）
├── group_change_detector.go   # Key 分组变更检测
├── time_series.go             # TimeBucketMetrics + 时序存储
├── quality_trend_detector.go  # QualityTrend 检测（忙闲/趋势/时段模式）
├── profiler.go                # 画像推导逻辑（L1 被动信号为主）
├── health_analyzer.go         # 健康诊断逻辑（被动优先 + 趋势信号 + L2 探测）
├── cost_resolver.go           # key 级分组倍率/充值倍率 → endpoint 有效成本
├── subscription_profile.go    # 订阅/套餐/来源画像
├── local_model_runtime.go     # Ollama/LM Studio/llama-server 运行时画像
├── trusted_routing_advisor.go # 一等官方/本地模型结构化 RoutingHint 生成与 shadow 评估
├── advisor_decision_store.go  # AdvisorDecisionRecord、准入/回滚统计和隐私保留策略
├── routing_trace.go           # RoutingDecisionTrace、过滤原因和脱敏输出
├── origin_policy.go           # 信任等级解析、来源混杂检测、tie-breaker 策略
├── manual_routing_intent.go   # 模型/渠道/endpoint/session 短期试用意图
├── manual_intent_store.go     # TTL、预算、命中结果存储
├── fast_decay.go              # 白嫖/临时池快速衰减评分
├── rate_limit_discovery.go    # 上游 RPM/并发限制自动发现（header + passive AIMD）
├── smart_router.go            # SmartRouter + CandidateFilter 构建
├── endpoint_policy.go         # EndpointAttemptPolicy + endpoint 候选排序
├── model_support_resolver.go  # active model filter 前置的自动支持判断
├── model_resolver.go          # 模型自动映射 + CapabilityFloor 约束
├── worker.go                  # L1 聚合 / L2 探测 / 清理后台任务
└── handlers.go                # API handlers

backend-go/internal/autopilot/
├── autopilot_test.go          # 画像推导测试
├── health_analyzer_test.go    # 健康诊断测试
├── cost_resolver_test.go      # 倍率继承、模型组匹配、有效成本计算测试
├── native_media_routing_test.go # images/vectors 原生调度、禁止 chat 协议转换测试
├── origin_policy_test.go      # 信任等级不参与质量推导、仅 tie-breaker 测试
├── subscription_profile_test.go # 套餐继承、渠道绑定、倍率回退测试
├── local_model_runtime_test.go # 本地 runtime 探测、OpenAI-compatible adapter 测试
├── trusted_routing_advisor_test.go # shadow 准确率、超时、不可降级约束测试
├── advisor_privacy_test.go    # AdvisorInput 白名单、低信任上游禁止、trace 脱敏测试
├── routing_priority_test.go   # kill switch、manual override、advisor、成本和信任 tie-breaker 顺序测试
├── manual_routing_intent_test.go # TTL、预算、fallback、硬约束测试
├── fast_decay_test.go         # 快速衰减测试
├── rate_limit_discovery_test.go # RPM header 解析与 AIMD 收敛测试
├── group_change_test.go       # 分组变更检测测试
├── quality_trend_test.go      # 趋势检测测试（忙闲模式识别）
├── endpoint_policy_test.go    # endpoint 排序与 fail-open/fail-closed 测试
├── model_support_resolver_test.go # supportedModels 前置兼容测试
└── smart_router_test.go       # 路由策略测试

frontend/src/components/
├── HealthCenter.vue        # 健康中心主视图
├── HealthCenterStats.vue   # 统计卡片
├── HealthChannelTable.vue  # 渠道健康表格
├── HealthChannelDetail.vue # 渠道详情侧栏
├── SubscriptionsView.vue   # 订阅中心
├── SubscriptionPlanTable.vue # 套餐/来源列表
├── AdminAutopilotPanel.vue # 管理面板中的 Autopilot 策略页
├── OperationsCockpit.vue   # 驾驶舱总览
├── LocalModelSettings.vue  # Ollama/LM Studio/llama-server 设置
├── TrustedAdvisorTrace.vue # 一等官方/本地 advisor shadow trace 和误判样例
├── ManualRoutingIntentDialog.vue # 模型/渠道试用创建面板
├── ManualIntentSummary.vue  # 活跃试用与结果摘要
├── QuickAddChannel.vue     # 快速添加渠道
└── ChannelHealthBadge.vue  # 健康状态 badge（增强现有）
```

### 11.3 与现有代码的接口契约

| 接口 | 方向 | 说明 |
|------|------|------|
| `CandidateFilterFunc` | autopilot → scheduler | SmartRouter 用于 channel 级粗筛/重排 |
| `EndpointAttemptPolicy` | autopilot → common failover | SmartRouter 用于 endpoint 级 baseURL/key 细选 |
| `ModelSupportResolver` | autopilot → scheduler | 在 active model filter 阶段判断 autoManaged 渠道是否可 request-scoped 映射 |
| `RateLimitDiscoverer` | autopilot → ratelimit | 未显式配置 RPM 时提供 endpoint/key/quota scope 的 runtime limiter 建议 |
| `CostResolver` | autopilot → scheduler/common failover | 在满足质量和能力下界后，为 endpoint 排序提供 `estimatedRequestCost` |
| `NativeEndpointPolicy` | autopilot → images/vectors handlers | 对原生生图和 embedding 请求只做 endpoint 排序，不改写请求协议或业务参数 |
| `OriginPolicy` | autopilot → scheduler/frontend | 信任等级只用于隐私/治理展示和同分 tie-breaker，不参与 QualityTier |
| `SubscriptionStore` | autopilot/frontend → config | 订阅中心维护套餐、余额、倍率和渠道绑定 |
| `TrustedRoutingAdvisor` | autopilot → scheduler | 一等官方 API 或本地模型只产出 bounded RoutingHint，不直接决定最终路由 |
| `LocalModelRuntimeStore` | autopilot/frontend → providers | 管理本地 runtime、模型能力、延迟和 advisor shadow 结果 |
| `ManualRoutingIntentStore` | autopilot/frontend → scheduler | 存储短期人工意图、TTL、预算、试用结果 |
| `MetricsManager.GetChannelAggregatedMetrics` | autopilot ← metrics | 画像推导读取运行时指标（入参是 baseURL + activeKeys + serviceType） |
| `MetricsManager.GetTimeWindowStatsForKey` | autopilot ← metrics | **endpoint 级**指标查询 |
| `config.ResolveUpstreamCapability` | autopilot ← config | 画像推导读取模型能力 |
| `config.ResolveAgentModelProfile` | autopilot ← config | RequestProfile 推导质量需求 |
| `channelDiscovery.*` | autopilot ← handlers | 复用探测逻辑（需抽取为可复用函数，支持单 endpoint 调用） |
| `PersistenceStore` | autopilot → metrics | 新表复用同一 SQLite 连接 |

---

## 12. P0/P1/P2 落地契约与验收标准

本节是实现前的阻断清单。P0 未完成时不得让 Autopilot 影响真实调度；P1 未完成时不得默认开启自动模式；P2 可作为体验增强分批交付。

### 12.1 P0：必须先锁死

#### P0.1 Trusted advisor 准入、退出与回滚

`TrustedRoutingAdvisor` 必须先 shadow，再进入候选影响，最后才允许真实影响路由：

```text
disabled
  → shadow：只记录 hint，不影响路由
  → candidate：满足样本/准确率门槛，仍需用户或配置显式允许
  → active：只对 lightweight/worker 低风险请求生效
  → rolled_back：SLO 恶化、误判超阈值、advisor 不可用或用户关闭
```

准入门槛：
- `minShadowSamples >= 500`。
- `minShadowAccuracy >= 0.90`。
- `maxCriticalMisrouteRate <= 0.01`。
- `maxFalseDemotionRate <= 0.03`。
- `advisor p95 latency <= advisorTimeoutMs`，超时按 fail-open 处理。
- 只允许 `OriginTier=first|local`；配置错误时直接降为 `disabled`。

退出条件：
- 任意 15 分钟窗口内 critical misroute 超阈值。
- active 后成功率、fallback 率、p95 latency 或用户 override 率相对 shadow baseline 明显恶化。
- advisor 连续超时、返回无效 JSON 或违反 `NeverDemote`。
- 全局 `smartRouting.killSwitch=true`。

```go
type AdvisorDecisionRecord struct {
    DecisionUID string `json:"decisionUid"`
    RequestUID  string `json:"requestUid,omitempty"`

    AdvisorUID        string `json:"advisorUid"`
    AdvisorOriginTier string `json:"advisorOriginTier"` // first | local
    Mode              string `json:"mode"`              // shadow | candidate | active

    TaskClass TaskClass `json:"taskClass"`
    PromptHash string `json:"promptHash,omitempty"` // 不存明文 prompt
    InputTokenBucket string `json:"inputTokenBucket"` // <1k | 1-10k | 10-50k | 50k+

    Hint TrustedRoutingHint `json:"hint"`
    DefaultPlanHash string `json:"defaultPlanHash"`
    Applied bool `json:"applied"`

    Outcome string `json:"outcome"` // matched | fallback | user_override | upstream_error | timeout
    MisrouteSeverity string `json:"misrouteSeverity,omitempty"` // none | minor | major | critical
    LatencyMs int64 `json:"latencyMs"`
    EstimatedAdvisorCost float64 `json:"estimatedAdvisorCost,omitempty"`
    CreatedAt time.Time `json:"createdAt"`
}
```

记录保留：
- `AdvisorDecisionRecord` 默认保留 7 天。
- 只保存 hash、bucket、结构化特征和结果，不保存 prompt 明文、文件内容、Authorization 或 API Key。
- 用户导出诊断时仍要二次脱敏。

#### P0.2 Advisor 输入与隐私白名单

`AdvisorInput` 必须是显式白名单，而不是把请求对象整体序列化：

```go
type AdvisorInput struct {
    RequestKind string `json:"requestKind"` // messages | chat | responses | gemini
    Operation string `json:"operation,omitempty"`
    RequestedModel string `json:"requestedModel,omitempty"`
    AgentRole string `json:"agentRole,omitempty"`

    InputTokenBucket string `json:"inputTokenBucket"`
    HasImage bool `json:"hasImage"`
    NeedsToolUse bool `json:"needsToolUse"`
    NeedsReasoning bool `json:"needsReasoning"`
    NeedsLongContext bool `json:"needsLongContext"`

    RedactedTaskSummary string `json:"redactedTaskSummary,omitempty"`
    CandidateTaskClasses []TaskClass `json:"candidateTaskClasses"`
}
```

隐私规则：
- `RedactedTaskSummary` 只能由本地模型、一等官方 advisor 或确定性本地摘要器生成。
- 禁止发送完整历史、文件全文、API Key、Authorization、自定义敏感 header、multipart 内容。
- `second|third|unknown` 上游永远不能接收 `AdvisorInput`。
- 如果摘要无法在可信执行面生成，advisor 直接跳过，SmartRouter 使用默认规则。

#### P0.3 确定性 TaskClassifier

`TaskClassifier` 必须先用确定性规则生成 task class。advisor 只能在既定枚举内提供 hint，不能发明新分类。

硬规则：
- `images` → `image_generation`。
- `vectors` → `embedding`。
- `HasImage && VisionNeed` → `vision`。
- `ContextNeed > threshold` → `long_context`。
- `Operation` 命中标题、分类、格式转换、`count_tokens` 等白名单，且无图片/工具/reasoning/长上下文 → `lightweight`。
- `AgentRole=main` 或未知 → `supervisor`。
- `AgentRole=subagent` → `worker`。
- 不确定时升级到更保守分类，不降级到 lightweight。

#### P0.4 最终调度优先级矩阵

真实请求必须按以下顺序裁决：

| 顺序 | 层级 | 规则 |
|------|------|------|
| 0 | 全局开关 | `killSwitch=true` 时只走现有调度，不读取 advisor hint |
| 1 | 安全硬约束 | 鉴权、协议、上下文、vision/tool/reasoning、image/vector 原生能力、embedding 维度 |
| 2 | 用户显式意图 | `ManualRoutingIntent`、`X-Channel`、session pin、manual override；仍不能绕过硬约束 |
| 3 | 显式模型配置 | 用户 `modelMapping` / `supportedModels` 优先，自动结果不得覆盖 |
| 4 | 运行可用性 | 熔断、cooldown、手动限流、runtime RPM/TPM/RPD/concurrency limiter |
| 5 | advisor hint | 只对允许 task class 生效；不能覆盖 `NeverDemote` 或用户显式意图 |
| 6 | 质量与能力排序 | `CapabilityFloor`、`MinQualityTier`、HealthState、QualityTrend |
| 7 | 成本排序 | 只在质量/能力下界之后使用 `estimatedRequestCost` |
| 8 | 信任等级 tie-breaker | 只在同质量/同健康/同成本档使用，不代表服务质量 |
| 9 | endpoint failover | `TryUpstreamWithAllKeys` 按 EndpointAttemptPolicy 细选 baseURL/key |

#### P0.5 验收不变量

实现必须覆盖以下测试：
- 低信任上游不会收到 `AdvisorInput`、prompt 摘要或隐私 metadata。
- `killSwitch=true` 时 Autopilot 不影响真实调度。
- 手动 `RateLimitRPM` / key 级限流不会被自动发现覆盖。
- 成本排序永远发生在能力、质量、上下文和协议硬约束之后。
- `ManualRoutingIntent` 不能绕过鉴权、模型能力、embedding 维度、images operation 或上下文限制。
- advisor 不能把 `supervisor`、`vision`、`long_context` 降级。
- `/v1/images/*` 只走原生 images endpoint，不从 chat/messages/responses 自动转换。
- `/v1/embeddings` 不跨模型猜测维度，不改写 input。
- 路由 trace 不包含明文 API Key、Authorization、prompt 全文或 multipart 内容。

### 12.2 P1：默认自动模式前必须完成

#### P1.1 成本模型覆盖面

CostResolver 必须区分：
- 文本 input/output token。
- prompt cache read/write 价格。
- embedding token 价格。
- image unit price：按 model、size、quality、operation、n 计算。
- token plan / prepaid credit / shared free 的余额与倍率。
- 币种换算：MVP 使用手动汇率；自动汇率更新属于 Phase 4。

缺价格时：
- `Confidence < requireCostConfidence`。
- 只展示 shadow 估算，不参与强排序。
- 不用 chat token 价格替代 image unit price 或 embedding price。

#### P1.2 限流模型覆盖面

RateLimitDiscoverer 必须同时表达：
- RPM：requests per minute。
- TPM：tokens per minute。
- RPD：requests per day。
- MaxConcurrent：并发连接/流式请求占用。
- Retry-After / reset window / cooldown。

应用规则：
- 手动值永远优先。
- header 明确值优先于 AIMD。
- 无 header 时只保守收敛，不能主动压测。
- 流式请求先降并发，再降 RPM/TPM，避免把长连接误判成请求速率过高。

#### P1.3 本地 runtime 资源与安全边界

本地运行时必须有资源保护：
- 默认只允许 loopback/private network；远程 local runtime 需要显式开启和鉴权。
- 禁止通过代理绕过 loopback/private 检查；需要防 DNS rebinding。
- `maxLocalConcurrent`、`maxLocalPromptTokens`、`maxLocalCandidateLatencyMs` 必须生效。
- 本地 candidate 超时或慢于阈值时 fail-open，不阻塞高优先级请求。
- 不自动 unload/load 模型；模型生命周期只做探测和提示，避免破坏用户本地环境。

#### P1.4 Routing trace schema

所有真实或 dry-run 路由都输出结构化 trace：

```go
type RoutingDecisionTrace struct {
    TraceUID string `json:"traceUid"`
    TaskClass TaskClass `json:"taskClass"`
    RequestKind string `json:"requestKind"`

    ManualIntentUID string `json:"manualIntentUid,omitempty"`
    AdvisorDecisionUID string `json:"advisorDecisionUid,omitempty"`

    CandidatesBefore int `json:"candidatesBefore"`
    CandidatesAfter int `json:"candidatesAfter"`
    FilterReasons map[string][]string `json:"filterReasons,omitempty"`
    SortReasons []string `json:"sortReasons,omitempty"`

    SelectedChannelUID string `json:"selectedChannelUid,omitempty"`
    SelectedMetricsKey string `json:"selectedMetricsKey,omitempty"`
    SelectedOriginTier string `json:"selectedOriginTier,omitempty"`
    EstimatedRequestCost float64 `json:"estimatedRequestCost,omitempty"`
    CostConfidence float64 `json:"costConfidence,omitempty"`

    FallbackUsed bool `json:"fallbackUsed"`
    PromptHash string `json:"promptHash,omitempty"`
}
```

trace 只记录解释性字段，不记录明文 prompt、密钥、敏感 header 或 multipart。

#### P1.5 迁移与回滚

落地必须包含：
- ~~SQLite schema version 和幂等 migration。~~ **已完成**：`ensureSchemaVersion` 基于 `PRAGMA user_version`，当前 7 张表定版为 v1 基线；库版本高于代码版本时返回 error（fail-closed，不做任何写操作），交给下游 fail-open 分支接管。
- ~~旧配置 backfill：补 `ChannelUID`、`OriginType`、`OriginTier` 时不改变原调度。~~ **已完成**：`ensureOriginBackfill` 只补 `"unknown"`，不做任何基于 URL/名称的猜测推断；已接线到 `Profiler.DeriveEndpointProfile`，`BreakTieByOriginTier` 现在能吃到真实数据（此前一直是 fallback 的 `unknown`）。
- ~~profile 损坏或 migration 失败时 fail-open：禁用 Autopilot，保留现有调度。~~ **已确认满足并测试锁定**：`main.go` 现有结构本就是 fail-open（`NewProfileStore`/`NewManager` 出错只打日志、`autopilotManager` 保持 `nil`，下游全部有 `nil` 守卫），本次新增 `schema_migration_test.go` 用测试锁定这条不变量。
- ~~全局 `killSwitch`、按 task class disable、按 channel disable。~~ **已完成**：新增 `AutopilotRoutingConfig.DisabledTaskClasses`/`DisabledChannelUIDs`；`CandidateFilterFor`/`executeFilter`/`BuildPlan` 三处一致接入，`smart_router_invariant_test.go` 覆盖"task class 禁用后回退默认调度""channel 禁用后不出现在候选里""dry-run 与真实路径行为一致"三条不变量。
- active 后的 SLO regression 自动 rollback 到 shadow，并在驾驶舱显示原因。**部分完成**：`ReleaseController.EvaluateAndApplyRegression` 已实现三窗口回归检测和 `SafetyOverride` 降级机制；`RoutingModeActive` 已加入配置枚举和 `IsAutopilotActive`。

#### P1.6 Trace v2 契约与灰度发布

> 对应实施计划：`docs/superpowers/plans/2026-07-23-autopilot-trace-rollout.md`

**Trace v2 核心变更：**

- `RoutingDecisionTrace` 增加 `SchemaVersion`、`TraceRevision`、`RequestCorrelationId`、`ReleaseID`、`PolicyFingerprint`、`TargetMode`/`EffectiveMode`/`Cohort`、`SchedulerDecision`、`EndpointAttempts` 字段。
- 新增 `TraceDetailV2`、`TraceSummary`、`SchedulerDecisionSummary`、`EndpointAttemptSummary` DTO；使用显式 DTO 落盘而非直接 JSON 化运行时结构。
- `ComparisonStatus` 三态枚举（`matched`/`mismatched`/`uncompared`）替代裸 `bool`。
- `SanitizeForPersistence` 与 `SanitizeForResponse` 双边界脱敏；`GenerateTraceUIDv2` 使用 `crypto/rand` 碰撞安全生成。
- `RoutingReleaseSnapshot` 不可变发布快照，入口冻结后续只读。

**SQLite v6 迁移：**

- trace 表新增列：`schema_version`、`trace_revision`、`request_correlation_id`、`release_id`、`policy_fingerprint`、`persistence_class`、`details_json`。
- 窗口表重建主键：增加 `release_id`/`policy_fingerprint`/`cohort` 维度，新增 `compared_count`/`matched_count`/`mismatch_count`/`uncompared_count` 全量比较计数。
- safety event 表增加 `release_id`/`policy_fingerprint` 字段。

**灰度发布状态机：**

```text
off --管理员启用--> shadow --门槛满足--> assist --门槛满足--> auto --100% 稳定--> active
```

| 状态 | 行为 | 放量规则 |
|---|---|---|
| `off` | 不调用 Autopilot | rolloutPercent=0 |
| `shadow` | 计算+记录，不影响调度 | rolloutPercent=0，安全基线 |
| `assist` | 重排候选，不删除 | rolloutPercent 1→100 |
| `auto` | 硬约束过滤+重排 | rolloutPercent 1→100 |
| `active` | 与 auto 语义相同 | rolloutPercent=100，1% shadow control |

**ReleaseController**（`release_controller.go`）：

- 集中处理相邻状态迁移校验（逐级晋升，降级随时允许）。
- `ComputeCohort` 使用 session ID / request correlation ID 与 `rolloutSeed` 做稳定哈希分桶。
- `EvaluateAndApplyRegression` 检查三窗口回归并应用 `SafetyOverride`。
- `AllowedTransition` 禁止跳级，`modeRank` 确定顺序。

**只读 Trace API：**

| 接口 | 返回 | 约束 |
|---|---|---|
| `GET /api/autopilot/traces` | `TraceSummary[]` | 游标分页、release/cohort/mode 过滤、partial 标记 |
| `GET /api/autopilot/traces/:traceUid` | `TraceDetailV2` | 404=未找到/已过期/未采样；503=存储不可用 |
| `GET /api/autopilot/traces/stats` | `TraceStatsResponse` | 三态比较统计、Mode/TaskClass 分布 |

### 12.3 P2：体验与运营增强

P2 不阻塞核心落地，但应进入后续 backlog：
- images/vectors 在渠道中心独立显示 operation、尺寸/质量、embedding 维度、原生端点健康。
- 前端异常态：advisor 被隐私策略禁用、本地 runtime 慢/不可用、成本置信度低、来源混杂、RPM/TPM 未发现。
- 导入/导出时保留订阅、倍率、信任等级和本地 runtime 绑定，但不导出密钥明文。
- 管理面板增加“解释模式”：只开启 trace/dry-run，不改真实调度。
- 运营报表按 task class 展示节省、fallback、advisor 命中、手动 override 和 SLO 变化。

---

## 13. 风险与缓解

| 风险 | 影响 | 缓解 |
|------|------|------|
| ~~自动模型映射语义降级~~ | ~~用户以为用 opus 实际用 haiku~~ | **已缓解**：CapabilityFloor 硬约束 + 不满足则跳过渠道而非降级映射 + response header 回显真实模型 |
| ~~健康诊断烧 quota~~ | ~~30-40 渠道主动探测成本高~~ | **已缓解**：L1 被动优先（零成本），L2 仅在数据不足时触发，每天总次数上限 `渠道数×12` |
| ~~白嫖池状态抖动~~ | ~~渠道反复断流导致调度震荡~~ | **已缓解**：FastDecay 请求级即时衰减 + 成功快速回升 + 断流比普通失败衰减更快 |
| ~~能力虚标误判~~ | ~~系统误关 vision/tool 标签~~ | **已缓解**：只做硬失败检测（HTTP 错误/解析失败），软质量问题留给人工 override |
| channel index 漂移 | 重排/删除后画像串到其他渠道 | 新增 `ChannelUID` 作为画像主键；`channelId` 仅作当前 index 展示快照 |
| endpoint 决策无法落到真实请求 | SmartRouter 只重排 channel，无法选择具体 key/baseURL | 新增 `EndpointAttemptPolicy` 注入 `TryUpstreamWithAllKeys`，在 failover 层过滤/排序 endpoint |
| 自动映射被 supportedModels 前置过滤挡掉 | ModelResolver 没机会运行，autoManaged 渠道被提前剔除 | 新增 `ModelSupportResolver` 接管 active model filter 的支持判断 |
| 信任等级被误当成服务质量 | 官方渠道被无条件优先，公益/中转短时高质量时无法被利用 | `OriginTier` 独立于 `QualityTier`；调度中只做同分 tie-breaker；UI 同时展示信任等级和实时质量 |
| 订阅中心与渠道中心重复维护价格 | 同一倍率在多处编辑导致成本计算不一致 | `SubscriptionProfile` 是套餐/余额/倍率事实源；继承链固定为 subscription → channel → key |
| 界面职责混乱 | 渠道中心、订阅中心、管理面板、驾驶舱都塞满配置项 | 明确四页职责：渠道中心管 endpoint，订阅中心管套餐，管理面板管策略，驾驶舱管运营总览 |
| 人工试用变成永久偏置 | 用户临时测试的 fable-5 或公益站长期压过 Autopilot | `ManualRoutingIntent` 必须有 TTL、请求/成本预算、fallback；到期只生成建议，不自动写长期策略 |
| 未知模型污染全局映射 | 测试新模型时把错误映射写入所有请求 | `model_trial` 只做 request-scoped 透传/映射；结果标记 `manual_trial`，用户显式保存后才进入 `modelMapping` |
| 隐私内容被发给低信任上游做判定 | 中转站/公益站接触额外 prompt、系统提示或 metadata | routing advisor 只允许 `OriginTier=first|local`；second/third/unknown 只可作为候选执行上游，不可做隐私敏感 classifier/evaluator |
| advisor 误判后持续影响真实调度 | 低风险任务被系统性降级，用户质量下降 | shadow 样本/准确率/critical misroute 门槛；active 后 SLO regression 自动 rollback；全局 kill switch |
| trace 或 advisor 记录泄露隐私 | prompt、文件内容或密钥进入 SQLite/API 响应 | `AdvisorInput` 白名单、prompt hash、短 TTL、导出二次脱敏；禁止记录 Authorization/API Key/multipart |
| 成本倍率配置错误 | 用户看到的具体费用不准，调度可能选错 key | 倍率必须显式展示来源和公式；低置信度成本只做 tie-breaker；UI 提供按 key 的有效价格预览 |
| 生图成本被误按 chat token 估算 | images 调度选错上游或显示错误节省 | image_generation 使用 image unit price；缺价格时只 shadow 展示，不用 chat token 价格替代 |
| chat 画图被误转成 images 请求 | 用户原本想走对话模型，系统改写协议导致行为变化 | MVP 禁止 chat/messages/responses → images 自动转换；只有显式 `/v1/images/*` 请求进入 image_generation 调度 |
| embedding 维度/格式被自动改写 | 向量库写入不兼容或召回质量异常 | vectors handler 的维度/格式是硬约束；Autopilot 只排序满足约束的 endpoint，不跨模型猜测维度 |
| 为省钱牺牲质量 | supervisor/vision/long-context 被路由到低质量或能力不足 endpoint | 成本排序只在 `CapabilityFloor`、`MinQualityTier`、上下文和能力硬约束通过后执行 |
| 自动 RPM 发现过于激进 | 误把免费/低配额上游打到 429 或封禁 | 只在未显式设置时启用；优先 header；无 header 用保守 AIMD；不主动压测；`maxAutoRPM` 封顶 |
| 自动 RPM 覆盖用户意图 | 用户手动设置被 runtime 学习值覆盖 | 手动 channel/key `RateLimitRPM` 永远优先；自动值只展示或用于未设置场景的 runtime limiter |
| TPM/RPD/并发限制缺失 | 只按 RPM 控制仍可能触发 token 限额或长连接拥塞 | RateLimitProfile 同时记录 RPM/TPM/RPD/MaxConcurrent；流式先降并发再降 RPM/TPM |
| endpoint 级探测成本倍增 | 5 key × 3 baseURL = 15 endpoint，探测量是 channel 级的 15 倍 | endpoint 级探测轮转执行，不并发；L2 探测每日总量仍有上限；channel 级聚合结果缓存避免重复计算 |
| Key 换分组后模型列表突变 | 调度时才发现模型不可用 | GroupChangeDetector 在 L2 探测和 model_not_found 时自动检测；分组变更立即标记 stale 并重探测 |
| endpoint 间能力不一致导致配置污染 | 同一 channel 有的 endpoint 支持 vision 有的不支持，自动写 channel 级配置会误导调度 | 只有所有 endpoint 一致时才写 channel 级配置；不一致结果保留 ProfileStore 并通过 EndpointAttemptPolicy request-scoped 生效 |
| SmartRouter 增加调度延迟 | 请求耗时增加 | 画像缓存在内存，CandidateFilter 只做内存操作（< 1ms）；聚合视图预计算，非实时聚合 |
| 与现有 X-Channel/manual override/promotion 冲突 | 用户显式选择被自动调度过滤 | 调整调度阶段或传入 ProtectedChannels；显式控制优先，SmartRouter 不过滤受保护渠道 |
| Phase 1 无智能调度时画像价值不明显 | 用户感知弱 | 明确 Phase 1 为 shadow/read-only；健康中心 + dry-run 诊断提前展示启用后效果 |
| 自动 modelMapping 覆盖用户手动配置 | 用户设置被意外覆盖 | 显式 modelMapping 始终优先；请求路径自动映射默认 request-scoped，不直接写配置 |
| 被动信号在低流量渠道不足 | 新渠道/冷渠道无法诊断 | 低流量时自动降级为 L2 探测，探测频率随请求量动态调整 |
| 上游忙闲时段质量差异大 | 闲时探测通过但忙时不可用 | TimeBucketMetrics 15 分钟桶 + QualityTrend 时段模式识别；忙时自动降档而非标记 dead；SmartRouter 在忙时倾向非低谷 endpoint |
| 趋势检测误判（短期波动 vs 真实恶化） | 频繁触发重评估浪费资源 | degrading 需要同时满足 24h 和 7d 双基准下降才触发重评估；volatile 状态只调 FastDecay 系数，不触发重评估 |
| 多实例重复探测 | 多进程同时 L2/L3 探测导致 quota 消耗翻倍 | Phase 1 不自动探测；Phase 2 引入 `autopilot_jobs(locked_until, owner)` 后再启用自动 worker |
| 管理 API 误操作 | 批量删除/暂停影响生产渠道 | 新端点要求管理权限；批量默认仅支持 refresh/probe/pause，删除走现有确认流程 |
| 本地 runtime 被当成远端代理入口 | SSRF、内网探测或远程未鉴权 runtime 泄露请求 | 默认只允许 loopback/private network；远程 local runtime 必须显式开启和鉴权；防代理绕过和 DNS rebinding |
| migration 失败影响生产流量 | schema/backfill 错误导致调度不可用 | migration 幂等；失败时 fail-open 禁用 Autopilot；保留现有调度和 kill switch |
