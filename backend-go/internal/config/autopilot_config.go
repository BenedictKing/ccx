package config

import (
	"fmt"
	"log"
	"os"
	"strings"
	"time"
)

// ============== 智能路由全局配置（§9.1） ==============
//
// 定义在 config 包避免循环依赖，autopilot 包通过 ConfigManager.GetAutopilotRouting() 引用。
// 结构体覆盖设计文档 §9.1 smartRouting 的全部子节点。

// AutopilotRoutingConfig 智能路由全局配置。
// 对应 config.json 的 "autopilot" 顶层字段。
// 热重载生效：文件变更后自动 reload，autopilot Manager 通过 RegisterOnConfigChange 回调感知。
type AutopilotRoutingConfig struct {
	// SchemaVersion 用于旧配置的幂等升级。加载器只迁移低于当前版本的配置，
	// 避免 omitempty 字段在后续重载时被重复解释为“缺失”。
	SchemaVersion int `json:"schemaVersion,omitempty"`

	// routingMode 是智能路由运行模式。
	// "off"    — 完全关闭，SmartRouter 不计算不记录。
	// "shadow" — 只计算 + 记录 routing trace，不影响真实调度（默认）。
	// "assist" — 保留全部候选并按评分重排，影响真实调度优先级。
	// "auto"   — 应用硬约束过滤并按评分重排；候选全被过滤时 fail-open。
	RoutingMode string `json:"mode,omitempty"`

	// killSwitch 全局急停开关。
	// true 时无条件回退到 "off"；AUTOPILOT_KILL_SWITCH 可在运行态强制启用急停。
	KillSwitch bool `json:"killSwitch,omitempty"`

	// RolloutPercent 灰度放量百分比 0-100。
	// shadow/off 强制为 0，active 强制为 100，assist/auto 有效范围为 1-100。
	RolloutPercent int `json:"rolloutPercent,omitempty"`

	// ControlPercent 固定对照组百分比，首版固定为 1。
	// 仅在 active 作用于未受保护的可路由请求，作为观测对照。
	ControlPercent int `json:"controlPercent,omitempty"`

	// ReleaseID 是当前发布批次的不可预测标识符（内部管理，不通过 API 暴露写入）。
	// 每次从 off 开始的新策略发布或语义变更时重新生成。
	ReleaseID string `json:"releaseId,omitempty"`

	// RolloutSeed 是稳定分桶种子（内部管理，不通过 API 暴露）。
	RolloutSeed string `json:"-"`

	// disabledTaskClasses 命中的 TaskClass 请求，SmartRouter 完全不介入（等同 off），
	// 回退到调度器默认行为。用于按任务类型临时下线 autopilot 影响，
	// 不同于 TrustedRoutingAdvisorConfig.NeverDemoteTaskClasses（那是"禁止降级"保护名单，
	// 语义是"autopilot 仍生效但不能把它调差"；这里是"autopilot 对它完全不生效"）。
	DisabledTaskClasses []string `json:"disabledTaskClasses,omitempty"`

	// disabledChannelUids 命中的渠道永远不会被 autopilot 推荐/选中。
	// 不等同于系统级禁用渠道——不改渠道本身的 status 字段，不影响手动
	// override/X-Channel/promotion 等非 autopilot 路径对该渠道的可选性。
	DisabledChannelUIDs []string `json:"disabledChannelUids,omitempty"`

	// costPreference 用户价格偏向（§5.6）。
	CostPreference CostPreferenceConfig `json:"costPreference,omitempty"`

	// scenario 任务场景预设（日常开发/难题攻坚/后台自动化/批量省钱）。
	// 非 auto 时覆盖复杂度→质量目标的推导、effort 展开范围与默认价格偏向。
	Scenario ScenarioRoutingConfig `json:"scenario,omitempty"`

	// modelFamilyPreference 模型派系偏好（§5.5.3）。
	ModelFamilyPreference ModelFamilyPreferenceConfig `json:"modelFamilyPreference,omitempty"`

	// weightOverrides 可选的评分权重覆盖，key 为权重名，value 为覆盖值。
	// 未列出的权重使用 SmartRouter 内部默认值。
	WeightOverrides map[string]float64 `json:"weightOverrides,omitempty"`

	// ── 以下为 §9.1 完整子节点，Phase 2 后续迭代逐步实现 ──

	// healthCheck 被动/主动健康检测配置。
	HealthCheck HealthCheckConfig `json:"healthCheck,omitempty"`

	// fastDecay 快速衰减配置（free/cheap 渠道）。
	FastDecay FastDecayRoutingConfig `json:"fastDecay,omitempty"`

	// rateLimitDiscovery 自动限速发现配置。
	RateLimitDiscovery RateLimitDiscoveryConfig `json:"rateLimitDiscovery,omitempty"`

	// modelMapping 自动模型映射配置。
	ModelMapping ModelMappingRoutingConfig `json:"modelMapping,omitempty"`

	// costOptimization 成本优化配置。
	CostOptimization CostOptimizationConfig `json:"costOptimization,omitempty"`

	// originPolicy 来源信任策略配置。
	OriginPolicy OriginPolicyConfig `json:"originPolicy,omitempty"`

	// taskDomainStrength 任务域优势配置。
	TaskDomainStrength TaskDomainStrengthConfig `json:"taskDomainStrength,omitempty"`

	// reasoningEffort 推理档位展开配置。
	ReasoningEffort ReasoningEffortConfig `json:"reasoningEffort,omitempty"`

	// manualIntent 人工路由意图配置。
	ManualIntent ManualIntentConfig `json:"manualIntent,omitempty"`

	// trustedRoutingAdvisor 可信路由顾问配置。
	TrustedRoutingAdvisor TrustedRoutingAdvisorConfig `json:"trustedRoutingAdvisor,omitempty"`

	// localModelRouting 本地模型路由配置。
	LocalModelRouting LocalModelRoutingConfig `json:"localModelRouting,omitempty"`

	// sloRollback SLO regression 自动回滚配置。
	// 仅作用于 TrustedRoutingAdvisor 已手动晋升到 active 的渠道。
	SLORollback SLORollbackConfig `json:"sloRollback,omitempty"`

	// subscriptionAutoRefresh 订阅余额自动刷新配置。
	// 仅对显式填写了 BillingAPIKey 的订阅生效，且必须双门控（开关打开 + BillingAPIKey 非空）。
	SubscriptionAutoRefresh SubscriptionAutoRefreshConfig `json:"subscriptionAutoRefresh,omitempty"`

	// abTest A/B 测试（低比例统计抽样双发）配置。
	// 主请求路径完全不变，影子请求在主响应返回后异步发起。
	ABTest ABTestConfig `json:"abTest,omitempty"`

	// ProtocolFederation 允许逻辑 Messages/Responses 请求在 Autopilot 默认路径中使用同一托管账号的兼容物理路由。
	// 支持 messages -> chat/responses 与 responses -> chat/responses；显式路由、手动覆盖和促销路径不受影响。
	ProtocolFederation ProtocolFederationConfig `json:"protocolFederation,omitempty"`

	// FrontierRoutingEnabled 已废弃，保留字段仅为 JSON 兼容。
	// Frontier/Ladder 模型级选优默认启用，成本证据不足时 fail-open 回退既有比较链。
	FrontierRoutingEnabled bool `json:"frontierRoutingEnabled,omitempty"`

	// AutoSafetyDowngrade 控制 auto 模式是否因 SLO 回归自动降级到 assist。
	// 默认 false：检测到回归时仅记录警告日志，不切换模式。
	// 设为 true 时恢复原有自动降级行为（连续 3 个窗口回归 → assist）。
	AutoSafetyDowngrade bool `json:"autoSafetyDowngrade,omitempty"`

	// AFPCostRoutingEnabled 已废弃，保留字段仅为 JSON 兼容。
	// AFP 成本路由默认开启，在 assist/auto 模式下始终生效。
	// 仅对火山 Agent Plan 渠道生效；scope 非可比时自动回退 USD。
	AFPCostRoutingEnabled bool `json:"afpCostRoutingEnabled,omitempty"`

	// LogicalChannelIdentityEnabled 控制 SmartRouter 是否在候选/trace/dry-run 中
	// 透传 LogicalChannelUID / LogicalChannelName。Phase A.1 仅用于回滚开关，
	// 不影响评分。默认 true；设为 false 时旧消费者可继续忽略 logical channel 字段。
	LogicalChannelIdentityEnabled *bool `json:"logicalChannelIdentityEnabled,omitempty"`

	// LogicalChannelScoringEnabled 控制 SmartRouter 在物理渠道画像缺失时，是否
	// fallback 到同一 LogicalChannel 下兄弟物理渠道的聚合画像作为评分输入（Phase A.2）。
	// 默认 false（opt-in）：物理画像存在时永不被覆盖；旧配置行为保持不变。
	LogicalChannelScoringEnabled *bool `json:"logicalChannelScoringEnabled,omitempty"`
}

// IsLogicalChannelIdentityEnabled 返回 LogicalChannel 身份透传是否启用。
// 未配置时默认启用。
func (c AutopilotRoutingConfig) IsLogicalChannelIdentityEnabled() bool {
	if c.LogicalChannelIdentityEnabled == nil {
		return true
	}
	return *c.LogicalChannelIdentityEnabled
}

// IsLogicalChannelScoringEnabled 返回 LogicalChannel 兄弟渠道画像 fallback 是否启用。
// 未配置时默认关闭（opt-in）。
func (c AutopilotRoutingConfig) IsLogicalChannelScoringEnabled() bool {
	if c.LogicalChannelScoringEnabled == nil {
		return false
	}
	return *c.LogicalChannelScoringEnabled
}

// IsAFPCostRoutingEnabled 返回 AFP 成本适配器是否参与可比树。
// 始终开启。仅对火山 Agent Plan 渠道有效；
// scope 非可比（非 Running/非托管）时自动回退 USD，无需额外开关。
func (c AutopilotRoutingConfig) IsAFPCostRoutingEnabled() bool {
	return true
}

// ── §9.1 子配置类型 ──

// CostPreferenceConfig 用户价格偏向配置（§5.6）。
// 三档预设：quality_first / balanced / cost_first。
// ProtocolFederationConfig 定义 Autopilot 的窄协议联邦边界。
// RequestKinds/ExecutionKinds 是白名单，避免未来新增协议时意外扩面。
// 默认 RequestKinds 包含 messages 与 responses，ExecutionKinds 包含 chat 与 responses。
type ProtocolFederationConfig struct {
	Enabled            bool     `json:"enabled"`
	RequestKinds       []string `json:"requestKinds,omitempty"`
	ExecutionKinds     []string `json:"executionKinds,omitempty"`
	RequireSameAccount bool     `json:"requireSameAccount"`
	RequireAutoManaged bool     `json:"requireAutoManaged"`
	ConversionPenalty  float64  `json:"conversionPenalty,omitempty"`
}

type CostPreferenceConfig struct {
	// Mode 全局价格偏向模式："quality_first" | "balanced" | "cost_first"。
	// 默认 "balanced"。
	Mode string `json:"mode,omitempty"`

	// PerTaskClass 按任务类别覆盖价格偏向模式。
	// key 为 TaskClass 字符串（supervisor/worker/lightweight/vision/long_context/image_generation/embedding）。
	// value 为该任务类别的价格偏向模式。
	PerTaskClass map[string]string `json:"perTaskClass,omitempty"`
}

// ScenarioRoutingConfig 任务场景预设配置。
// 场景预设把「质量下限 + 价格偏好 + effort 档范围」打包成用户可一键切换的组合，
// 生效链：请求头 X-Routing-Scenario > 此处 Mode > 现有复杂度/TaskClass 自动推断。
type ScenarioRoutingConfig struct {
	// Mode 当前生效场景："auto"（默认，自动推断）|
	// "daily_dev"（日常开发）| "hard_problem"（难题攻坚）|
	// "background"（后台自动化）| "batch_cheap"（批量省钱）。
	Mode string `json:"mode,omitempty"`

	// Overrides 按场景 key 覆盖内置预设参数（可选；key 同 Mode 枚举除 auto 外）。
	Overrides map[string]ScenarioPresetOverride `json:"overrides,omitempty"`

	// HeaderOverrideEnabled 是否允许请求头 X-Routing-Scenario / X-Cost-Preference
	// 按请求覆盖全局设置（默认 true）。
	HeaderOverrideEnabled *bool `json:"headerOverrideEnabled,omitempty"`
}

// ScenarioPresetOverride 场景预设参数覆盖项。
// 全部可选；未填字段沿用内置默认值。字段语义与 autopilot 内置预设一致。
type ScenarioPresetOverride struct {
	// MinQualityTier 场景质量档下限："low"|"normal"|"high"|"premium"。
	MinQualityTier string `json:"minQualityTier,omitempty"`

	// CostPreference 场景默认价格偏向："quality_first"|"balanced"|"cost_first"。
	CostPreference string `json:"costPreference,omitempty"`

	// EffortFloor / EffortCeil effort 档展开范围（含端点）：
	// "low"|"medium"|"high"|"xhigh"|"max"|"ultra"。
	EffortFloor string `json:"effortFloor,omitempty"`
	EffortCeil  string `json:"effortCeil,omitempty"`

	// QualityBenefitCap 质量收益帽：超过该档后质量不再加分；空串 = 无帽。
	QualityBenefitCap string `json:"qualityBenefitCap,omitempty"`
}

// ModelFamilyPreferenceConfig 模型派系偏好配置（§5.5.3）。
// 控制 SmartRouter 对不同模型派系的排序偏好。
type ModelFamilyPreferenceConfig struct {
	// Enabled 是否启用派系偏好（默认 true）。
	Enabled bool `json:"enabled,omitempty"`

	// Weight 派系偏好权重，覆盖全局 w_family，建议范围 0.1~0.5（默认 0.2）。
	Weight float64 `json:"weight,omitempty"`

	// GlobalOrder 全局派系偏好顺序，从高到低。
	// 回退默认值：未在 PerTaskClass 中列出的 TaskClass 使用此顺序。
	GlobalOrder []string `json:"globalOrder,omitempty"`

	// PerTaskClass 按任务类别覆盖派系偏好顺序。
	// key 为 TaskClass 字符串，value 为该任务类别的派系偏好顺序。
	// 存在的 TaskClass 使用其专属顺序，否则回退 GlobalOrder。
	PerTaskClass map[string][]string `json:"perTaskClass,omitempty"`
}

// HealthCheckConfig 被动/主动健康检测配置。
type HealthCheckConfig struct {
	Enabled                 bool    `json:"enabled,omitempty"`
	PassiveSignalsOnly      bool    `json:"passiveSignalsOnly,omitempty"`
	L2ProbeEnabled          bool    `json:"l2ProbeEnabled,omitempty"`
	L2ProbeIntervalMinutes  int     `json:"l2ProbeIntervalMinutes,omitempty"`
	L2ProbeMaxPerDay        int     `json:"l2ProbeMaxPerDay,omitempty"`
	DeadProbeIntervalHours  int     `json:"deadProbeIntervalHours,omitempty"`
	DeadConfidenceThreshold float64 `json:"deadConfidenceThreshold,omitempty"`
	AutoExcludeDead         bool    `json:"autoExcludeDead,omitempty"`
	// ProbeRecoveryThreshold degraded/limited→healthy 所需的连续探测成功次数。
	// 默认 2：避免单次探测噪声导致状态在 degraded 和 healthy 之间抖动（flapping）。
	ProbeRecoveryThreshold int `json:"probeRecoveryThreshold,omitempty"`
	// StabilityHysteresisWindows StabilityTier 晋降级所需的连续窗口数。
	// 默认 2：避免单轮噪声导致 StabilityTier 在 stable/normal/unstable 之间抖动。
	StabilityHysteresisWindows int `json:"stabilityHysteresisWindows,omitempty"`
}

// FastDecayRoutingConfig 快速衰减路由配置。
type FastDecayRoutingConfig struct {
	Enabled              bool     `json:"enabled,omitempty"`
	ApplyToCostTiers     []string `json:"applyToCostTiers,omitempty"`
	ApplyToPoolTags      []string `json:"applyToPoolTags,omitempty"`
	RecoveryRate         float64  `json:"recoveryRate,omitempty"`
	DecayBase            float64  `json:"decayBase,omitempty"`
	StreamBreakDecayBase float64  `json:"streamBreakDecayBase,omitempty"`
}

// RateLimitDiscoveryConfig 自动限速发现配置。
type RateLimitDiscoveryConfig struct {
	Enabled                 bool    `json:"enabled,omitempty"`
	ApplyOnlyWhenUnset      bool    `json:"applyOnlyWhenUnset,omitempty"`
	PreferHeaders           bool    `json:"preferHeaders,omitempty"`
	PassiveAimdEnabled      bool    `json:"passiveAimdEnabled,omitempty"`
	MinRpm                  int     `json:"minRpm,omitempty"`
	MaxAutoRpm              int     `json:"maxAutoRpm,omitempty"`
	MaxAutoTpm              int     `json:"maxAutoTpm,omitempty"`
	MaxAutoRpd              int     `json:"maxAutoRpd,omitempty"`
	MaxAutoConcurrent       int     `json:"maxAutoConcurrent,omitempty"`
	ConfidenceThreshold     float64 `json:"confidenceThreshold,omitempty"`
	IncreaseIntervalMinutes int     `json:"increaseIntervalMinutes,omitempty"`
	IncreaseStepPercent     int     `json:"increaseStepPercent,omitempty"`
	DecreaseOn429Percent    int     `json:"decreaseOn429Percent,omitempty"`
}

// ModelMappingRoutingConfig 自动模型映射配置。
type ModelMappingRoutingConfig struct {
	AutoResolve            bool `json:"autoResolve,omitempty"`
	CapabilityFloorEnabled bool `json:"capabilityFloorEnabled,omitempty"`
	EchoMappedModel        bool `json:"echoMappedModel,omitempty"`
	ForbidChainMapping     bool `json:"forbidChainMapping,omitempty"`

	// PreferAutoOverManual 控制手动映射表退役期间的优先级反转。
	// false（默认）：与历史行为一致，显式 profile.ModelMapping 优先，ModelResolver 自动决策仅作兜底。
	// true：ModelResolver 自动决策优先；仅当自动决策 fail-open（未命中）时才回退到显式手动映射。
	// 必须显式开启，可在不改代码的情况下随时回滚。
	PreferAutoOverManual bool `json:"preferAutoOverManual,omitempty"`
}

// CostOptimizationConfig 成本优化配置。
type CostOptimizationConfig struct {
	Enabled                  bool    `json:"enabled,omitempty"`
	ApplyAfterQualityFloor   bool    `json:"applyAfterQualityFloor,omitempty"`
	RequireCostConfidence    float64 `json:"requireCostConfidence,omitempty"`
	IncludeCachePricing      bool    `json:"includeCachePricing,omitempty"`
	IncludeImageUnitPricing  bool    `json:"includeImageUnitPricing,omitempty"`
	IncludeEmbeddingPricing  bool    `json:"includeEmbeddingPricing,omitempty"`
	Currency                 string  `json:"currency,omitempty"`
	ExchangeRateSource       string  `json:"exchangeRateSource,omitempty"`
	PreferLowerEffectiveCost bool    `json:"preferLowerEffectiveCost,omitempty"`
	SupervisorSavingsWeight  float64 `json:"supervisorSavingsWeight,omitempty"`
	WorkerSavingsWeight      float64 `json:"workerSavingsWeight,omitempty"`
	// ProviderTimePricing 描述官方 provider 的按时段计价规则，key 为 providerId。
	// effectiveFrom 为空或尚未到达时不加价；基础模型价格始终保留在模型注册表中。
	ProviderTimePricing          map[string]ProviderTimePricingConfig `json:"providerTimePricing,omitempty"`
	ExchangeRateQuotes           []ExchangeRateQuote                  `json:"exchangeRateQuotes,omitempty"`
	ExchangeRateQuotesConfigured bool                                 `json:"-"`
	ExchangeRateSnapshot         *ExchangeRateSnapshot                `json:"exchangeRateSnapshot,omitempty"`
}

// ExchangeRateQuote 描述人工汇率报价：SourceAmount SourceUnit = TargetAmount TargetUnit。
type ExchangeRateQuote struct {
	SourceAmount float64   `json:"sourceAmount"`
	SourceUnit   string    `json:"sourceUnit"`
	TargetAmount float64   `json:"targetAmount"`
	TargetUnit   string    `json:"targetUnit"`
	UpdatedAt    time.Time `json:"updatedAt"`
	Note         string    `json:"note,omitempty"`
}

// ExchangeRateSnapshot 是从人工报价图解析出的 USD 单价原子快照。
type ExchangeRateSnapshot struct {
	Version       uint64             `json:"version"`
	USDUnitPrices map[string]float64 `json:"usdUnitPrices"`
	BuiltAt       time.Time          `json:"builtAt"`
}

func defaultExchangeRateQuotes() []ExchangeRateQuote {
	now := time.Now().UTC()
	return []ExchangeRateQuote{
		{SourceAmount: 1, SourceUnit: "USD", TargetAmount: 7, TargetUnit: "CNY", UpdatedAt: now, Note: "default"},
		{SourceAmount: 500, SourceUnit: "LDC", TargetAmount: 10, TargetUnit: "CNY", UpdatedAt: now, Note: "default"},
	}
}

// ProviderTimePricingConfig 描述一个 provider 的生效时间、时区和每日高峰窗口。
type ProviderTimePricingConfig struct {
	EffectiveFrom  string               `json:"effectiveFrom,omitempty"`
	TimeZone       string               `json:"timeZone,omitempty"`
	PeakMultiplier float64              `json:"peakMultiplier,omitempty"`
	PeakWindows    []DailyPricingWindow `json:"peakWindows,omitempty"`
}

// DailyPricingWindow 使用 provider 所在时区的 HH:MM 半开区间 [start, end)。
type DailyPricingWindow struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

// OriginPolicyConfig 来源信任策略配置。
type OriginPolicyConfig struct {
	UnknownOriginPolicy                string  `json:"unknownOriginPolicy,omitempty"`
	PreferHigherOriginTierAsTieBreaker bool    `json:"preferHigherOriginTierAsTieBreaker,omitempty"`
	OriginTieBreakerWeight             float64 `json:"originTieBreakerWeight,omitempty"`
	ShowLowerTierOutperforming         bool    `json:"showLowerTierOutperforming,omitempty"`
	WarnMixedOriginChannel             bool    `json:"warnMixedOriginChannel,omitempty"`
}

// TaskDomainStrengthConfig 任务域优势配置。
type TaskDomainStrengthConfig struct {
	Enabled             bool                          `json:"enabled,omitempty"`
	Weight              float64                       `json:"weight,omitempty"`
	SeedMatrixOverrides map[string]map[string]float64 `json:"seedMatrixOverrides,omitempty"`
}

// ReasoningEffortConfig 推理档位展开配置。
type ReasoningEffortConfig struct {
	Enabled               bool                `json:"enabled,omitempty"`
	ExpandVariants        bool                `json:"expandVariants,omitempty"`
	PerTaskClass          map[string][]string `json:"perTaskClass,omitempty"`
	RespectClientThinking bool                `json:"respectClientThinking,omitempty"`
	// QualityTierShadowEnabled 仅记录模型×effort 质量档及影子总分，不改变实际排序。
	// nil 默认开启；指针用于区分旧配置缺失与显式 false。
	QualityTierShadowEnabled *bool `json:"qualityTierShadowEnabled,omitempty"`
	// QualityTierActiveEnabled 将模型×effort 质量档与未知证据折扣用于实际排序
	// （2026-09 由 shadow 转正）。nil 默认开启；显式 false 回退为仅影子观测，
	// 排序回到模型基础档口径。
	QualityTierActiveEnabled *bool `json:"qualityTierActiveEnabled,omitempty"`
}

// IsQualityTierShadowEnabled 返回 effort-aware 质量档 shadow 是否开启。
func (c ReasoningEffortConfig) IsQualityTierShadowEnabled() bool {
	return c.QualityTierShadowEnabled == nil || *c.QualityTierShadowEnabled
}

// IsQualityTierActiveEnabled 返回 effort-aware 质量档是否参与实际排序。
func (c ReasoningEffortConfig) IsQualityTierActiveEnabled() bool {
	return c.QualityTierActiveEnabled == nil || *c.QualityTierActiveEnabled
}

func boolPointer(value bool) *bool { return &value }

// ManualIntentConfig 人工路由意图配置。
type ManualIntentConfig struct {
	Enabled                       bool `json:"enabled,omitempty"`
	DefaultTTLMinutes             int  `json:"defaultTtlMinutes,omitempty"`
	MaxTTLHours                   int  `json:"maxTtlHours,omitempty"`
	DefaultMaxRequests            int  `json:"defaultMaxRequests,omitempty"`
	MaxTrafficPercentForThirdTier int  `json:"maxTrafficPercentForThirdTier,omitempty"`
	RequireConfirmForSupervisor   bool `json:"requireConfirmForSupervisor,omitempty"`
	FallbackOnFailureDefault      bool `json:"fallbackOnFailureDefault,omitempty"`
}

// TrustedRoutingAdvisorConfig 可信路由顾问配置。
type TrustedRoutingAdvisorConfig struct {
	Enabled                         bool     `json:"enabled,omitempty"`
	Mode                            string   `json:"mode,omitempty"`
	AllowedAdvisorOriginTiers       []string `json:"allowedAdvisorOriginTiers,omitempty"`
	ForbidAdvisorOnRelayOrCommunity bool     `json:"forbidAdvisorOnRelayOrCommunity,omitempty"`
	AdvisorRuntimeUID               string   `json:"advisorRuntimeUid,omitempty"`
	AdvisorChannelUID               string   `json:"advisorChannelUid,omitempty"`
	AdvisorTimeoutMs                int      `json:"advisorTimeoutMs,omitempty"`
	MaxAdvisorPromptTokens          int      `json:"maxAdvisorPromptTokens,omitempty"`
	MinAdvisorConfidence            float64  `json:"minAdvisorConfidence,omitempty"`
	MinShadowSamples                int      `json:"minShadowSamples,omitempty"`
	MinShadowAccuracy               float64  `json:"minShadowAccuracy,omitempty"`
	MaxCriticalMisrouteRate         float64  `json:"maxCriticalMisrouteRate,omitempty"`
	MaxFalseDemotionRate            float64  `json:"maxFalseDemotionRate,omitempty"`
	PromotionMode                   string   `json:"promotionMode,omitempty"`
	NeverDemoteTaskClasses          []string `json:"neverDemoteTaskClasses,omitempty"`
	ForbidAutoDecomposeAndMerge     bool     `json:"forbidAutoDecomposeAndMerge,omitempty"`
	RedactSensitiveMetadata         bool     `json:"redactSensitiveMetadata,omitempty"`
	RecordOnlyHashedPrompt          bool     `json:"recordOnlyHashedPrompt,omitempty"`
	RetainDecisionRecordsDays       int      `json:"retainDecisionRecordsDays,omitempty"`
	AutoRollbackOnSloRegression     bool     `json:"autoRollbackOnSloRegression,omitempty"`
	FailOpenOnAdvisorError          bool     `json:"failOpenOnAdvisorError,omitempty"`
}

// LocalModelRoutingConfig 本地模型路由配置。
type LocalModelRoutingConfig struct {
	Enabled                     bool     `json:"enabled,omitempty"`
	Mode                        string   `json:"mode,omitempty"`
	AllowLocalForTaskClasses    []string `json:"allowLocalForTaskClasses,omitempty"`
	NeverDemoteTaskClasses      []string `json:"neverDemoteTaskClasses,omitempty"`
	ForbidAutoDecomposeAndMerge bool     `json:"forbidAutoDecomposeAndMerge,omitempty"`
}

// SubscriptionAutoRefreshConfig 订阅余额自动刷新配置。
// 仅对 Provider 为 openai/anthropic/google 且显式填写了 BillingAPIKey 的订阅生效。
// 中转/公益渠道（relay_x/community_x）不支持自动刷新，继续走手动维护。
type SubscriptionAutoRefreshConfig struct {
	// Enabled 是否启用订阅余额自动刷新。
	// 默认 false，需显式 opt-in（Phase 4 Item 6 安全守则）。
	Enabled bool `json:"enabled,omitempty"`

	// RefreshIntervalHours 每张卡的刷新间隔（小时）。
	// 默认 24，即每 24 小时刷新一次。
	RefreshIntervalHours int `json:"refreshIntervalHours,omitempty"`

	// DailyBudget 每日刷新调用总预算。
	// 默认 100，防止触发 provider 侧限流。
	DailyBudget int `json:"dailyBudget,omitempty"`

	// RequestTimeoutSeconds 单次刷新请求超时（秒）。
	// 默认 15。
	RequestTimeoutSeconds int `json:"requestTimeoutSeconds,omitempty"`
}

// SLORollbackConfig SLO regression 自动回滚配置。
// 仅作用于 TrustedRoutingAdvisor 里已被运维手动晋升到 active 状态的渠道。
// 不做自动恢复：rolled_back 后只能运维手动重新晋升，避免自动升降级来回震荡。
type SLORollbackConfig struct {
	// Enabled 是否启用 SLO regression 自动回滚。
	// 默认 false，需显式 opt-in（Phase 3B+ 安全守则）。
	Enabled bool `json:"enabled,omitempty"`

	// ConsecutiveWindows 连续 degrading 窗口数达到此阈值时触发回滚。
	// 默认 3，防止单轮抖动误触发。
	ConsecutiveWindows int `json:"consecutiveWindows,omitempty"`
}

// ABTestConfig A/B 测试（低比例统计抽样双发）配置。
// 主请求路径完全不变：影子请求在主响应返回后异步发起，不影响主请求延迟或结果。
// 默认关闭，需显式 opt-in（Phase 4 Item 8 安全守则）。
type ABTestConfig struct {
	// Enabled 是否启用 A/B 测试抽样双发。
	// 默认 false，需显式 opt-in。
	Enabled bool `json:"enabled,omitempty"`

	// SampleRatio 抽样比例，0.0~1.0。
	// 默认 0.01（1% 流量），防止配置失误导致成本失控。
	SampleRatio float64 `json:"sampleRatio,omitempty"`

	// MaxShadowRequestsPerHour 每小时影子请求硬预算上限。
	// 仿 L2 探测每日 200 次预算先例，防止配置失误导致成本失控。
	// 默认 60（约每分钟 1 次）。
	MaxShadowRequestsPerHour int `json:"maxShadowRequestsPerHour,omitempty"`

	// ShadowCandidateCount 影子候选数量。
	// 默认 1，即只对比排名第二的候选渠道，不做多渠道混战。
	ShadowCandidateCount int `json:"shadowCandidateCount,omitempty"`
}

// ── 模式常量 ──

const (
	currentAutopilotConfigSchemaVersion = 4

	// AutopilotModeAuto 全自动模式：应用硬约束过滤并按评分重排，支持 fail-open。
	AutopilotModeAuto = "auto"
)

const (
	// autopilotKillSwitchEnv 是全局急停环境变量名。
	autopilotKillSwitchEnv = "AUTOPILOT_KILL_SWITCH"
)

// ── 默认值 ──

// DefaultAutopilotRoutingConfig 返回智能路由默认配置。
// 适用于 config.json 中缺失 "autopilot" 块时的回退值。
func DefaultAutopilotRoutingConfig() AutopilotRoutingConfig {
	return AutopilotRoutingConfig{
		SchemaVersion: currentAutopilotConfigSchemaVersion,
		RoutingMode:   AutopilotModeAuto,
		KillSwitch:    false,

		CostPreference: CostPreferenceConfig{
			Mode: "balanced",
		},

		ProtocolFederation: ProtocolFederationConfig{
			Enabled:            true,
			RequestKinds:       []string{"messages", "responses"},
			ExecutionKinds:     []string{"chat", "responses"},
			RequireSameAccount: true,
			RequireAutoManaged: true,
			ConversionPenalty:  0.35,
		},

		ModelFamilyPreference: ModelFamilyPreferenceConfig{
			Enabled: true,
			Weight:  0.2,
			GlobalOrder: []string{
				"claude", "openai", "deepseek", "gemini", "qwen", "kimi", "glm",
			},
		},

		HealthCheck: HealthCheckConfig{
			Enabled:                    true,
			L2ProbeIntervalMinutes:     120,
			L2ProbeMaxPerDay:           12,
			DeadProbeIntervalHours:     6,
			DeadConfidenceThreshold:    0.80,
			AutoExcludeDead:            true,
			ProbeRecoveryThreshold:     2,
			StabilityHysteresisWindows: 2,
		},

		FastDecay: FastDecayRoutingConfig{
			Enabled:              true,
			ApplyToCostTiers:     []string{"free", "cheap"},
			ApplyToPoolTags:      []string{"temp"},
			RecoveryRate:         0.15,
			DecayBase:            0.85,
			StreamBreakDecayBase: 0.70,
		},

		RateLimitDiscovery: RateLimitDiscoveryConfig{
			Enabled:                 true,
			ApplyOnlyWhenUnset:      true,
			PreferHeaders:           true,
			PassiveAimdEnabled:      true,
			MinRpm:                  1,
			MaxAutoRpm:              120,
			MaxAutoTpm:              200000,
			MaxAutoRpd:              5000,
			MaxAutoConcurrent:       8,
			ConfidenceThreshold:     0.6,
			IncreaseIntervalMinutes: 10,
			IncreaseStepPercent:     10,
			DecreaseOn429Percent:    50,
		},

		ModelMapping: ModelMappingRoutingConfig{
			AutoResolve:            false, // 新开关默认关闭，需显式 opt-in（Phase 3B-2 安全守则）
			CapabilityFloorEnabled: true,
			EchoMappedModel:        true,
			ForbidChainMapping:     true,
		},

		CostOptimization: CostOptimizationConfig{
			Enabled:                  true,
			ApplyAfterQualityFloor:   true,
			RequireCostConfidence:    0.6,
			IncludeCachePricing:      true,
			IncludeImageUnitPricing:  true,
			IncludeEmbeddingPricing:  true,
			Currency:                 "USD",
			ExchangeRateSource:       "manual",
			PreferLowerEffectiveCost: true,
			SupervisorSavingsWeight:  0.5,
			WorkerSavingsWeight:      3,
			ExchangeRateQuotes:       defaultExchangeRateQuotes(),
			ProviderTimePricing: map[string]ProviderTimePricingConfig{
				"deepseek": {
					EffectiveFrom:  "2026-07-20T00:00:00+08:00",
					TimeZone:       "Asia/Shanghai",
					PeakMultiplier: 2,
					PeakWindows: []DailyPricingWindow{
						{Start: "09:00", End: "12:00"},
						{Start: "14:00", End: "18:00"},
					},
				},
			},
		},

		OriginPolicy: OriginPolicyConfig{
			UnknownOriginPolicy:                "observe",
			PreferHigherOriginTierAsTieBreaker: true,
			OriginTieBreakerWeight:             0.2,
			ShowLowerTierOutperforming:         true,
			WarnMixedOriginChannel:             true,
		},

		TaskDomainStrength: TaskDomainStrengthConfig{
			Enabled: true,
			Weight:  0.5,
		},

		ReasoningEffort: ReasoningEffortConfig{
			Enabled:                  true,
			ExpandVariants:           true,
			RespectClientThinking:    true,
			QualityTierShadowEnabled: boolPointer(true),
			QualityTierActiveEnabled: boolPointer(true),
			PerTaskClass: map[string][]string{
				"supervisor":   {"high", "max"},
				"worker":       {"medium"},
				"lightweight":  {"off", "minimal"},
				"long_context": {"medium", "high"},
			},
		},

		ManualIntent: ManualIntentConfig{
			Enabled:                       true,
			DefaultTTLMinutes:             120,
			MaxTTLHours:                   24,
			DefaultMaxRequests:            100,
			MaxTrafficPercentForThirdTier: 25,
			RequireConfirmForSupervisor:   true,
			FallbackOnFailureDefault:      true,
		},

		TrustedRoutingAdvisor: TrustedRoutingAdvisorConfig{
			Enabled:                         true,
			Mode:                            "shadow",
			AllowedAdvisorOriginTiers:       []string{"first", "local"},
			ForbidAdvisorOnRelayOrCommunity: true,
			AdvisorTimeoutMs:                1200,
			MaxAdvisorPromptTokens:          1200,
			MinAdvisorConfidence:            0.85,
			MinShadowSamples:                500,
			MinShadowAccuracy:               0.90,
			MaxCriticalMisrouteRate:         0.01,
			MaxFalseDemotionRate:            0.03,
			PromotionMode:                   "manual",
			NeverDemoteTaskClasses:          []string{"supervisor", "vision", "long_context"},
			ForbidAutoDecomposeAndMerge:     true,
			RedactSensitiveMetadata:         true,
			RecordOnlyHashedPrompt:          true,
			RetainDecisionRecordsDays:       7,
			AutoRollbackOnSloRegression:     true,
			FailOpenOnAdvisorError:          true,
		},

		LocalModelRouting: LocalModelRoutingConfig{
			Enabled:                     true,
			Mode:                        "shadow",
			AllowLocalForTaskClasses:    []string{"lightweight", "worker"},
			NeverDemoteTaskClasses:      []string{"supervisor", "vision", "long_context"},
			ForbidAutoDecomposeAndMerge: true,
		},

		SLORollback: SLORollbackConfig{
			Enabled:            false, // 默认关闭，需显式 opt-in（Phase 4 Item 3 安全守则）
			ConsecutiveWindows: 3,
		},

		SubscriptionAutoRefresh: SubscriptionAutoRefreshConfig{
			Enabled:               false, // 默认关闭，需显式 opt-in（Phase 4 Item 6 安全守则）
			RefreshIntervalHours:  24,
			DailyBudget:           100,
			RequestTimeoutSeconds: 15,
		},

		ABTest: ABTestConfig{
			Enabled:                  false, // 默认关闭，需显式 opt-in（Phase 4 Item 8 安全守则）
			SampleRatio:              0.01,  // 默认 1% 抽样
			MaxShadowRequestsPerHour: 60,    // 默认每小时 60 次上限
			ShadowCandidateCount:     1,     // 默认只对比排名第二的候选
		},

		// LogicalChannelIdentityEnabled 默认 nil，IsLogicalChannelIdentityEnabled() 返回 true。
		// 不显式设值，避免在默认配置中多出一个布尔字段。
	}
}

// ── 校验与归一化 ──

// validateRollout 将放量比例固定为全量，并补齐追踪标识。
func (c *AutopilotRoutingConfig) validateRollout() {
	c.RolloutPercent = 100
	c.ControlPercent = 1
	if c.RolloutSeed == "" {
		c.RolloutSeed = fmt.Sprintf("seed_%d", time.Now().UnixNano())
	}
	if c.ReleaseID == "" {
		c.ReleaseID = fmt.Sprintf("rel_%d", time.Now().Unix())
	}
}

// Validate 校验并归一化 AutopilotRoutingConfig。
// 非法值回退到安全默认，不返回 error（fail-open 策略）。
func (c *AutopilotRoutingConfig) Validate() {
	// 1. 模式校验
	c.RoutingMode = AutopilotModeAuto

	// 2. 灰度比例校验
	c.validateRollout()

	// 3. 成本偏好校验
	c.CostPreference.validate()
	c.Scenario.validate()
	if !c.CostOptimization.ExchangeRateQuotesConfigured && c.CostOptimization.ExchangeRateQuotes == nil {
		c.CostOptimization.ExchangeRateQuotes = defaultExchangeRateQuotes()
	}
	c.CostOptimization.validateProviderTimePricing()

	// 3. 派系偏好校验
	c.ModelFamilyPreference.validate()

	// 4. 可选权重覆盖：移除 NaN
	for k, v := range c.WeightOverrides {
		if v != v { // NaN 检测
			delete(c.WeightOverrides, k)
		}
	}

	// 5. 禁用名单和协议联邦白名单：去空白项、去重，避免无意义的重复配置。
	c.DisabledTaskClasses = dedupeNonEmptyStrings(c.DisabledTaskClasses)
	c.DisabledChannelUIDs = dedupeNonEmptyStrings(c.DisabledChannelUIDs)
	c.ProtocolFederation.RequestKinds = dedupeNonEmptyStrings(c.ProtocolFederation.RequestKinds)
	c.ProtocolFederation.ExecutionKinds = dedupeNonEmptyStrings(c.ProtocolFederation.ExecutionKinds)
	if c.ProtocolFederation.ConversionPenalty < 0 || c.ProtocolFederation.ConversionPenalty != c.ProtocolFederation.ConversionPenalty {
		c.ProtocolFederation.ConversionPenalty = 0.35
	}

	// 6. 健康检测：连续探测恢复阈值兜底（旧配置文件可能没有该字段，反序列化后为 0）
	if c.HealthCheck.ProbeRecoveryThreshold <= 0 {
		c.HealthCheck.ProbeRecoveryThreshold = 2
	}
	// 7. StabilityTier 晋降级滞后窗口兜底
	if c.HealthCheck.StabilityHysteresisWindows <= 0 {
		c.HealthCheck.StabilityHysteresisWindows = 2
	}

	// 8. SLO regression 自动回滚：连续窗口阈值兜底
	if c.SLORollback.ConsecutiveWindows <= 0 {
		c.SLORollback.ConsecutiveWindows = 3
	}

	// 9. 订阅余额自动刷新：参数兜底
	if c.SubscriptionAutoRefresh.RefreshIntervalHours <= 0 {
		c.SubscriptionAutoRefresh.RefreshIntervalHours = 24
	}
	if c.SubscriptionAutoRefresh.DailyBudget <= 0 {
		c.SubscriptionAutoRefresh.DailyBudget = 100
	}
	if c.SubscriptionAutoRefresh.RequestTimeoutSeconds <= 0 {
		c.SubscriptionAutoRefresh.RequestTimeoutSeconds = 15
	}

	// 10. A/B 测试：参数兜底（含 0，与 SLORollback/SubscriptionAutoRefresh 的兜底模式一致；
	// 0 与"字段缺失反序列化后的零值"不可区分，回退默认避免旧配置迁移后静默停止抽样）
	if c.ABTest.SampleRatio <= 0 || c.ABTest.SampleRatio > 1 {
		c.ABTest.SampleRatio = 0.01
	}
	if c.ABTest.MaxShadowRequestsPerHour <= 0 {
		c.ABTest.MaxShadowRequestsPerHour = 60
	}
	if c.ABTest.ShadowCandidateCount <= 0 {
		c.ABTest.ShadowCandidateCount = 1
	}
}

// dedupeNonEmptyStrings 去除空白项（trim 后为空则丢弃）并去重，保持首次出现的顺序。
// nil 输入返回 nil（不强制分配空 slice，避免 JSON 序列化时把 omitempty 字段变成 []）。
func dedupeNonEmptyStrings(items []string) []string {
	if items == nil {
		return nil
	}
	seen := make(map[string]struct{}, len(items))
	result := make([]string, 0, len(items))
	for _, item := range items {
		trimmed := strings.TrimSpace(item)
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		result = append(result, trimmed)
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// validate 归一化 CostPreferenceConfig。
func (c *CostPreferenceConfig) validate() {
	c.Mode = normalizeCostPreferenceMode(c.Mode)

	// 按 TaskClass 归一化
	for k, v := range c.PerTaskClass {
		normalized := normalizeCostPreferenceMode(v)
		if normalized != v {
			c.PerTaskClass[k] = normalized
		}
	}
}

// normalizeCostPreferenceMode 归一化价格偏向模式。
// 非法值回退到 "balanced"。
func normalizeCostPreferenceMode(mode string) string {
	switch normalized := strings.ToLower(strings.TrimSpace(mode)); normalized {
	case "quality_first", "balanced", "cost_first":
		return normalized
	default:
		return "balanced"
	}
}

// ValidScenarioModes 场景模式合法枚举（供 API 校验与前端展示复用）。
var ValidScenarioModes = []string{"auto", "daily_dev", "hard_problem", "background", "batch_cheap"}

// NormalizeScenarioMode 归一化场景模式。非法值回退到 "auto"。
func NormalizeScenarioMode(mode string) string {
	normalized := strings.ToLower(strings.TrimSpace(mode))
	for _, valid := range ValidScenarioModes {
		if normalized == valid {
			return normalized
		}
	}
	return "auto"
}

// NormalizeCostPreferenceMode 归一化价格偏向模式。非法值回退到 "balanced"。
func NormalizeCostPreferenceMode(mode string) string {
	return normalizeCostPreferenceMode(mode)
}

// validate 归一化 ScenarioRoutingConfig。
func (c *ScenarioRoutingConfig) validate() {
	c.Mode = NormalizeScenarioMode(c.Mode)
	if len(c.Overrides) == 0 {
		return
	}
	normalized := make(map[string]ScenarioPresetOverride, len(c.Overrides))
	for k, v := range c.Overrides {
		key := NormalizeScenarioMode(k)
		if key == "auto" {
			continue
		}
		v.MinQualityTier = strings.ToLower(strings.TrimSpace(v.MinQualityTier))
		v.CostPreference = normalizeCostPreferenceMode(v.CostPreference)
		v.EffortFloor = strings.ToLower(strings.TrimSpace(v.EffortFloor))
		v.EffortCeil = strings.ToLower(strings.TrimSpace(v.EffortCeil))
		v.QualityBenefitCap = strings.ToLower(strings.TrimSpace(v.QualityBenefitCap))
		normalized[key] = v
	}
	c.Overrides = normalized
}

func (c *CostOptimizationConfig) validateProviderTimePricing() {
	if c.ProviderTimePricing == nil {
		return
	}
	normalized := make(map[string]ProviderTimePricingConfig, len(c.ProviderTimePricing))
	for providerID, rule := range c.ProviderTimePricing {
		providerID = strings.ToLower(strings.TrimSpace(providerID))
		if providerID == "" {
			continue
		}
		rule.EffectiveFrom = strings.TrimSpace(rule.EffectiveFrom)
		if rule.EffectiveFrom != "" {
			if _, err := time.Parse(time.RFC3339, rule.EffectiveFrom); err != nil {
				rule.EffectiveFrom = ""
			}
		}
		rule.TimeZone = strings.TrimSpace(rule.TimeZone)
		if rule.TimeZone != "" {
			if _, err := time.LoadLocation(rule.TimeZone); err != nil {
				rule.TimeZone = ""
			}
		}
		if rule.PeakMultiplier <= 0 || rule.PeakMultiplier != rule.PeakMultiplier {
			rule.PeakMultiplier = 1
		} else if rule.PeakMultiplier > 10 {
			rule.PeakMultiplier = 10
		}
		windows := make([]DailyPricingWindow, 0, len(rule.PeakWindows))
		for _, window := range rule.PeakWindows {
			window.Start = strings.TrimSpace(window.Start)
			window.End = strings.TrimSpace(window.End)
			start, startOK := parseDailyClock(window.Start)
			end, endOK := parseDailyClock(window.End)
			if startOK && endOK && start != end {
				windows = append(windows, window)
			}
		}
		rule.PeakWindows = windows
		normalized[providerID] = rule
	}
	c.ProviderTimePricing = normalized
}

// ProviderTimePricingMultiplier 返回请求时刻的 provider 计价倍率。
// 未配置、未到 effectiveFrom、配置无效或不在高峰窗口时均安全回退到 1.0。
func (c CostOptimizationConfig) ProviderTimePricingMultiplier(providerID string, at time.Time) float64 {
	rule, ok := c.ProviderTimePricing[strings.ToLower(strings.TrimSpace(providerID))]
	if !ok || strings.TrimSpace(rule.EffectiveFrom) == "" || strings.TrimSpace(rule.TimeZone) == "" {
		return 1
	}
	effectiveFrom, err := time.Parse(time.RFC3339, rule.EffectiveFrom)
	if err != nil || at.Before(effectiveFrom) {
		return 1
	}
	location, err := time.LoadLocation(rule.TimeZone)
	if err != nil || rule.PeakMultiplier <= 0 {
		return 1
	}
	local := at.In(location)
	minute := local.Hour()*60 + local.Minute()
	for _, window := range rule.PeakWindows {
		start, startOK := parseDailyClock(window.Start)
		end, endOK := parseDailyClock(window.End)
		if !startOK || !endOK || start == end {
			continue
		}
		if (start < end && minute >= start && minute < end) ||
			(start > end && (minute >= start || minute < end)) {
			return rule.PeakMultiplier
		}
	}
	return 1
}

func parseDailyClock(value string) (int, bool) {
	parsed, err := time.Parse("15:04", strings.TrimSpace(value))
	if err != nil {
		return 0, false
	}
	return parsed.Hour()*60 + parsed.Minute(), true
}

// validate 归一化 ModelFamilyPreferenceConfig。
func (c *ModelFamilyPreferenceConfig) validate() {
	// 移除空值
	c.GlobalOrder = removeEmptyStrings(c.GlobalOrder)
	for k, v := range c.PerTaskClass {
		cleaned := removeEmptyStrings(v)
		if len(cleaned) == 0 {
			delete(c.PerTaskClass, k)
		} else {
			c.PerTaskClass[k] = cleaned
		}
	}

	// 权重钳制
	if c.Weight < 0 {
		c.Weight = 0
	} else if c.Weight > 1.0 {
		c.Weight = 1.0
	}
}

// removeEmptyStrings 从字符串切片中移除空白条目。
// 输入 nil 返回 nil（保持零值语义）。
func removeEmptyStrings(ss []string) []string {
	if ss == nil {
		return nil
	}
	result := make([]string, 0, len(ss))
	for _, s := range ss {
		if trimmed := strings.TrimSpace(s); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}

// ── ConfigManager 访问方法 ──

// GetAutopilotRouting 获取智能路由配置（返回深拷贝）。
// 如果配置文件中缺失 "autopilot" 块，返回 DefaultAutopilotRoutingConfig()。
// 环境变量 AUTOPILOT_KILL_SWITCH 仅叠加到返回值，不污染可持久化配置。
func (cm *ConfigManager) GetAutopilotRouting() AutopilotRoutingConfig {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	cfg := cm.config.AutopilotRouting.deepCopy()
	applyAutopilotEnvOverrides(&cfg)
	return cfg
}

// SetCostPreferenceMode 更新全局价格偏向模式并持久化。
// 只修改 Mode，保留已有的 PerTaskClass 覆盖。
func (cm *ConfigManager) SetCostPreferenceMode(mode string) error {
	normalized := normalizeCostPreferenceMode(mode)
	cm.mu.Lock()
	cm.config.AutopilotRouting.CostPreference.Mode = normalized
	if err := cm.saveConfigLocked(cm.config); err != nil {
		cm.mu.Unlock()
		return err
	}
	log.Printf("[Config-Autopilot] 价格偏向已更新: %s", normalized)
	cm.fireConfigChangeCallbacks()
	return nil
}

// SetScenarioMode 更新任务场景预设模式并持久化。
// 只修改 Mode，保留已有的 Overrides 覆盖项。
func (cm *ConfigManager) SetScenarioMode(mode string) error {
	normalized := NormalizeScenarioMode(mode)
	cm.mu.Lock()
	cm.config.AutopilotRouting.Scenario.Mode = normalized
	if err := cm.saveConfigLocked(cm.config); err != nil {
		cm.mu.Unlock()
		return err
	}
	log.Printf("[Config-Autopilot] 场景预设已更新: %s", normalized)
	cm.fireConfigChangeCallbacks()
	return nil
}

// SetAutopilotRolloutPercent 更新灰度放量比例并持久化。
// 仅在 assist/auto 模式下有效，自动校验范围 1-100。
func (cm *ConfigManager) SetAutopilotRolloutPercent(pct int) error {
	if pct < 1 || pct > 100 {
		return fmt.Errorf("rolloutPercent 必须在 1-100 之间")
	}
	cm.mu.Lock()
	cm.config.AutopilotRouting.RolloutPercent = pct
	if err := cm.saveConfigLocked(cm.config); err != nil {
		cm.mu.Unlock()
		return err
	}
	log.Printf("[Config-Autopilot] 灰度比例已更新: %d%%", pct)
	cm.fireConfigChangeCallbacks()
	return nil
}

// SetABTestEnabled 更新 A/B 测试开关并持久化。
// enabled=false 时立即停止所有影子请求采样。
func (cm *ConfigManager) SetABTestEnabled(enabled bool) error {
	cm.mu.Lock()
	cm.config.AutopilotRouting.ABTest.Enabled = enabled
	if err := cm.saveConfigLocked(cm.config); err != nil {
		cm.mu.Unlock()
		return err
	}
	log.Printf("[Config-Autopilot] A/B 测试开关已更新: enabled=%v", enabled)
	cm.fireConfigChangeCallbacks()
	return nil
}

type ExchangeRateUpdateResult struct {
	Quotes   []ExchangeRateQuote
	Snapshot *ExchangeRateSnapshot
	Source   string
	Version  uint64
}

func (cm *ConfigManager) ApplyExchangeRateUpdate(quotes []ExchangeRateQuote, snapshot ExchangeRateSnapshot, explicitEmpty bool) (ExchangeRateUpdateResult, error) {
	cm.mu.Lock()
	previous := cm.config.AutopilotRouting.CostOptimization
	cm.config.AutopilotRouting.CostOptimization.ExchangeRateQuotes = append([]ExchangeRateQuote(nil), quotes...)
	cm.config.AutopilotRouting.CostOptimization.ExchangeRateQuotesConfigured = explicitEmpty || quotes != nil
	snapshotCopy := snapshot
	if snapshot.USDUnitPrices != nil {
		snapshotCopy.USDUnitPrices = make(map[string]float64, len(snapshot.USDUnitPrices))
		for unit, price := range snapshot.USDUnitPrices {
			snapshotCopy.USDUnitPrices[unit] = price
		}
	}
	cm.config.AutopilotRouting.CostOptimization.ExchangeRateSnapshot = &snapshotCopy
	if err := cm.saveConfigLocked(cm.config); err != nil {
		cm.config.AutopilotRouting.CostOptimization = previous
		cm.mu.Unlock()
		return ExchangeRateUpdateResult{}, err
	}
	cm.fireConfigChangeCallbacks()
	resultSnapshot := snapshotCopy
	return ExchangeRateUpdateResult{
		Quotes:   append([]ExchangeRateQuote(nil), cm.config.AutopilotRouting.CostOptimization.ExchangeRateQuotes...),
		Snapshot: &resultSnapshot,
		Source:   strings.TrimSpace(cm.config.AutopilotRouting.CostOptimization.ExchangeRateSource),
		Version:  snapshotCopy.Version,
	}, nil
}

// GetEffectiveRoutingMode 获取智能路由生效模式。
// KillSwitch=true 时无条件返回 "off"。
func (cm *ConfigManager) GetEffectiveRoutingMode() string {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	cfg := cm.config.AutopilotRouting
	applyAutopilotEnvOverrides(&cfg)
	return cfg.EffectiveRoutingMode()
}

// EffectiveRoutingMode 返回智能路由生效模式（无锁版本，供内部使用）。
// KillSwitch=true 时无条件返回 "off"。
func (c AutopilotRoutingConfig) EffectiveRoutingMode() string {
	if c.KillSwitch {
		return "off"
	}
	return AutopilotModeAuto
}

// IsAutopilotActive 判断智能路由是否处于影响真实调度的模式。
func (c AutopilotRoutingConfig) IsAutopilotActive() bool {
	return !c.KillSwitch
}

// applyAutopilotEnvOverrides 将环境变量覆盖应用到 AutopilotRoutingConfig。
// 仅处理 AUTOPILOT_KILL_SWITCH（bool-like: true/1/yes/on）。
// 调用方必须传入运行态副本，环境变量覆盖不得写回持久配置。
func applyAutopilotEnvOverrides(c *AutopilotRoutingConfig) {
	if envVal := os.Getenv(autopilotKillSwitchEnv); isTruthyEnv(envVal) {
		c.KillSwitch = true
	}
}

// isTruthyEnv 判断环境变量值是否为真值。
// 支持: true, 1, yes, on（不区分大小写）。
func isTruthyEnv(val string) bool {
	switch strings.ToLower(strings.TrimSpace(val)) {
	case "true", "1", "yes", "on":
		return true
	default:
		return false
	}
}

// ── CostPreference 辅助方法 ──

// GetEffectiveCostPreferenceMode 获取指定 TaskClass 的生效价格偏向模式。
// PerTaskClass 优先于全局 Mode。
func (c CostPreferenceConfig) GetEffectiveCostPreferenceMode(taskClass string) string {
	if taskClass != "" {
		if mode, ok := c.PerTaskClass[taskClass]; ok {
			return normalizeCostPreferenceMode(mode)
		}
	}
	return c.Mode
}

// GetEffectiveMultipliers 获取指定 TaskClass 的生效乘数。
// 返回 (savingsMultiplier, providerQualityMultiplier)。
func (c CostPreferenceConfig) GetEffectiveMultipliers(taskClass string) (float64, float64) {
	mode := c.GetEffectiveCostPreferenceMode(taskClass)
	switch mode {
	case "quality_first":
		return 0.3, 1.5
	case "balanced":
		return 1.0, 1.0
	case "cost_first":
		return 2.0, 0.5
	default:
		return 1.0, 1.0
	}
}

// ── ModelFamilyPreference 辅助方法 ──

// GetEffectiveOrder 获取指定 TaskClass 的生效派系偏好顺序。
// PerTaskClass 优先于 GlobalOrder。
func (c ModelFamilyPreferenceConfig) GetEffectiveOrder(taskClass string) []string {
	if taskClass != "" {
		if order, ok := c.PerTaskClass[taskClass]; ok && len(order) > 0 {
			return order
		}
	}
	return c.GlobalOrder
}

// FamilyRank 返回指定派系在偏好顺序中的排名（0-based）。
// 未找到返回 -1。排名越小越优先。
func (c ModelFamilyPreferenceConfig) FamilyRank(taskClass string, family string) int {
	order := c.GetEffectiveOrder(taskClass)
	familyLower := strings.ToLower(strings.TrimSpace(family))
	for i, f := range order {
		if strings.ToLower(strings.TrimSpace(f)) == familyLower {
			return i
		}
	}
	return -1
}

// ── 深拷贝 ──

// deepCopy 创建 AutopilotRoutingConfig 的深拷贝，确保并发安全。
// map / slice 字段独立分配，避免调用方修改影响原始配置。
func (c AutopilotRoutingConfig) deepCopy() AutopilotRoutingConfig {
	cp := c

	// DisabledTaskClasses
	if c.DisabledTaskClasses != nil {
		cp.DisabledTaskClasses = make([]string, len(c.DisabledTaskClasses))
		copy(cp.DisabledTaskClasses, c.DisabledTaskClasses)
	}

	// DisabledChannelUIDs
	if c.DisabledChannelUIDs != nil {
		cp.DisabledChannelUIDs = make([]string, len(c.DisabledChannelUIDs))
		copy(cp.DisabledChannelUIDs, c.DisabledChannelUIDs)
	}

	// ProtocolFederation 白名单：切片必须独立，否则 GetAutopilotRouting 的调用方
	// 修改返回值会写穿共享配置。
	if c.ProtocolFederation.RequestKinds != nil {
		cp.ProtocolFederation.RequestKinds = append([]string(nil), c.ProtocolFederation.RequestKinds...)
	}
	if c.ProtocolFederation.ExecutionKinds != nil {
		cp.ProtocolFederation.ExecutionKinds = append([]string(nil), c.ProtocolFederation.ExecutionKinds...)
	}

	// CostPreference.PerTaskClass
	if c.CostPreference.PerTaskClass != nil {
		cp.CostPreference.PerTaskClass = make(map[string]string, len(c.CostPreference.PerTaskClass))
		for k, v := range c.CostPreference.PerTaskClass {
			cp.CostPreference.PerTaskClass[k] = v
		}
	}

	// Scenario.Overrides
	if c.Scenario.Overrides != nil {
		cp.Scenario.Overrides = make(map[string]ScenarioPresetOverride, len(c.Scenario.Overrides))
		for k, v := range c.Scenario.Overrides {
			cp.Scenario.Overrides[k] = v
		}
	}

	// CostOptimization.ProviderTimePricing
	if c.CostOptimization.ProviderTimePricing != nil {
		cp.CostOptimization.ProviderTimePricing = make(map[string]ProviderTimePricingConfig, len(c.CostOptimization.ProviderTimePricing))
		for providerID, rule := range c.CostOptimization.ProviderTimePricing {
			if rule.PeakWindows != nil {
				rule.PeakWindows = append([]DailyPricingWindow(nil), rule.PeakWindows...)
			}
			cp.CostOptimization.ProviderTimePricing[providerID] = rule
		}
	}

	if c.CostOptimization.ExchangeRateQuotes != nil {
		cp.CostOptimization.ExchangeRateQuotes = append([]ExchangeRateQuote(nil), c.CostOptimization.ExchangeRateQuotes...)
	}
	if c.CostOptimization.ExchangeRateSnapshot != nil {
		snapshot := *c.CostOptimization.ExchangeRateSnapshot
		if snapshot.USDUnitPrices != nil {
			snapshot.USDUnitPrices = make(map[string]float64, len(snapshot.USDUnitPrices))
			for unit, price := range c.CostOptimization.ExchangeRateSnapshot.USDUnitPrices {
				snapshot.USDUnitPrices[unit] = price
			}
		}
		cp.CostOptimization.ExchangeRateSnapshot = &snapshot
	}

	// ModelFamilyPreference.GlobalOrder
	if c.ModelFamilyPreference.GlobalOrder != nil {
		cp.ModelFamilyPreference.GlobalOrder = make([]string, len(c.ModelFamilyPreference.GlobalOrder))
		copy(cp.ModelFamilyPreference.GlobalOrder, c.ModelFamilyPreference.GlobalOrder)
	}

	// ModelFamilyPreference.PerTaskClass
	if c.ModelFamilyPreference.PerTaskClass != nil {
		cp.ModelFamilyPreference.PerTaskClass = make(map[string][]string, len(c.ModelFamilyPreference.PerTaskClass))
		for k, v := range c.ModelFamilyPreference.PerTaskClass {
			orderCopy := make([]string, len(v))
			copy(orderCopy, v)
			cp.ModelFamilyPreference.PerTaskClass[k] = orderCopy
		}
	}

	// WeightOverrides
	if c.WeightOverrides != nil {
		cp.WeightOverrides = make(map[string]float64, len(c.WeightOverrides))
		for k, v := range c.WeightOverrides {
			cp.WeightOverrides[k] = v
		}
	}

	// HealthCheck — 值类型，无需额外处理

	// FastDecay.ApplyToCostTiers / ApplyToPoolTags
	if c.FastDecay.ApplyToCostTiers != nil {
		cp.FastDecay.ApplyToCostTiers = make([]string, len(c.FastDecay.ApplyToCostTiers))
		copy(cp.FastDecay.ApplyToCostTiers, c.FastDecay.ApplyToCostTiers)
	}
	if c.FastDecay.ApplyToPoolTags != nil {
		cp.FastDecay.ApplyToPoolTags = make([]string, len(c.FastDecay.ApplyToPoolTags))
		copy(cp.FastDecay.ApplyToPoolTags, c.FastDecay.ApplyToPoolTags)
	}

	// TaskDomainStrength.SeedMatrixOverrides
	if c.TaskDomainStrength.SeedMatrixOverrides != nil {
		cp.TaskDomainStrength.SeedMatrixOverrides = make(map[string]map[string]float64, len(c.TaskDomainStrength.SeedMatrixOverrides))
		for k, v := range c.TaskDomainStrength.SeedMatrixOverrides {
			inner := make(map[string]float64, len(v))
			for ik, iv := range v {
				inner[ik] = iv
			}
			cp.TaskDomainStrength.SeedMatrixOverrides[k] = inner
		}
	}

	// ReasoningEffort.PerTaskClass
	if c.ReasoningEffort.PerTaskClass != nil {
		cp.ReasoningEffort.PerTaskClass = make(map[string][]string, len(c.ReasoningEffort.PerTaskClass))
		for k, v := range c.ReasoningEffort.PerTaskClass {
			effortsCopy := make([]string, len(v))
			copy(effortsCopy, v)
			cp.ReasoningEffort.PerTaskClass[k] = effortsCopy
		}
	}
	if c.ReasoningEffort.QualityTierShadowEnabled != nil {
		value := *c.ReasoningEffort.QualityTierShadowEnabled
		cp.ReasoningEffort.QualityTierShadowEnabled = &value
	}
	if c.ReasoningEffort.QualityTierActiveEnabled != nil {
		value := *c.ReasoningEffort.QualityTierActiveEnabled
		cp.ReasoningEffort.QualityTierActiveEnabled = &value
	}

	// TrustedRoutingAdvisor slice 字段
	if c.TrustedRoutingAdvisor.AllowedAdvisorOriginTiers != nil {
		cp.TrustedRoutingAdvisor.AllowedAdvisorOriginTiers = make([]string, len(c.TrustedRoutingAdvisor.AllowedAdvisorOriginTiers))
		copy(cp.TrustedRoutingAdvisor.AllowedAdvisorOriginTiers, c.TrustedRoutingAdvisor.AllowedAdvisorOriginTiers)
	}
	if c.TrustedRoutingAdvisor.NeverDemoteTaskClasses != nil {
		cp.TrustedRoutingAdvisor.NeverDemoteTaskClasses = make([]string, len(c.TrustedRoutingAdvisor.NeverDemoteTaskClasses))
		copy(cp.TrustedRoutingAdvisor.NeverDemoteTaskClasses, c.TrustedRoutingAdvisor.NeverDemoteTaskClasses)
	}

	// LocalModelRouting slice 字段
	if c.LocalModelRouting.AllowLocalForTaskClasses != nil {
		cp.LocalModelRouting.AllowLocalForTaskClasses = make([]string, len(c.LocalModelRouting.AllowLocalForTaskClasses))
		copy(cp.LocalModelRouting.AllowLocalForTaskClasses, c.LocalModelRouting.AllowLocalForTaskClasses)
	}
	if c.LocalModelRouting.NeverDemoteTaskClasses != nil {
		cp.LocalModelRouting.NeverDemoteTaskClasses = make([]string, len(c.LocalModelRouting.NeverDemoteTaskClasses))
		copy(cp.LocalModelRouting.NeverDemoteTaskClasses, c.LocalModelRouting.NeverDemoteTaskClasses)
	}

	return cp
}
