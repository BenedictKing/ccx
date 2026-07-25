package autopilot

import (
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/BenedictKing/ccx/internal/presetstore"
)

// ── 健康状态 ──

// HealthState 表示 endpoint/channel 的健康状态。
// 状态机：unknown → healthy → degraded/limited → dead，另有 misconfigured。
type HealthState string

const (
	HealthStateUnknown       HealthState = "unknown"       // 新渠道或证据不足
	HealthStateHealthy       HealthState = "healthy"       // 正常参与调度
	HealthStateDegraded      HealthState = "degraded"      // 可用但质量差，降权
	HealthStateLimited       HealthState = "limited"       // 限流中（429/quota），cooldown 内跳过
	HealthStateMisconfigured HealthState = "misconfigured" // 配置疑似错误，不参与自动调度
	HealthStateDead          HealthState = "dead"          // 死亡，移出调度
)

// ── 来源类型与信任等级 ──

// ChannelOriginType 描述渠道来源类型。
type ChannelOriginType string

const (
	OriginOfficialAPI       ChannelOriginType = "official_api"        // 官方 API key
	OriginOfficialTokenPlan ChannelOriginType = "official_token_plan" // 官方 token/subscription plan
	OriginRelay             ChannelOriginType = "relay"               // 付费/商业中转站
	OriginCommunity         ChannelOriginType = "community"           // 公益站/共享站
	OriginLocalRuntime      ChannelOriginType = "local_runtime"       // 本地 Ollama/LM Studio/llama-server
	OriginUnknown           ChannelOriginType = "unknown"
)

// ChannelOriginTier 描述渠道信任/隐私等级。不参与 QualityTier 推导。
type ChannelOriginTier string

const (
	OriginTierFirst   ChannelOriginTier = "first"  // 官方 API / 官方 token plan
	OriginTierSecond  ChannelOriginTier = "second" // 中转站
	OriginTierThird   ChannelOriginTier = "third"  // 公益站
	OriginTierLocal   ChannelOriginTier = "local"  // 本地运行时
	OriginTierUnknown ChannelOriginTier = "unknown"
)

// InferOriginTier 从 OriginType 推导 OriginTier。
//
// 优先查 presetstore（可远程更新的来源分类预置）；预置未命中该来源类型时，
// 回退到下方编译期 switch 兜底，保证离线/首启与旧枚举行为不变。
func InferOriginTier(originType ChannelOriginType) ChannelOriginTier {
	sub := presetstore.Default().Subscription()
	// 经别名归一化后在预置里查等级；命中且非 unknown 直接采用。
	if tier := sub.TierFor(string(originType)); tier != "" && tier != string(OriginTierUnknown) {
		return ChannelOriginTier(tier)
	}
	return inferOriginTierBuiltin(originType)
}

// inferOriginTierBuiltin 是编译期兜底映射，预置缺失时使用。
func inferOriginTierBuiltin(originType ChannelOriginType) ChannelOriginTier {
	switch originType {
	case OriginOfficialAPI, OriginOfficialTokenPlan:
		return OriginTierFirst
	case OriginRelay:
		return OriginTierSecond
	case OriginCommunity:
		return OriginTierThird
	case OriginLocalRuntime:
		return OriginTierLocal
	default:
		return OriginTierUnknown
	}
}

// ── 建议动作 ──

// SuggestedAction 是 autopilot 根据画像给出的建议动作。
type SuggestedAction string

const (
	ActionNone       SuggestedAction = "none"        // 无需操作
	ActionPause      SuggestedAction = "pause"       // 建议暂停
	ActionResume     SuggestedAction = "resume"      // 建议恢复
	ActionDelete     SuggestedAction = "delete"      // 建议删除
	ActionFix        SuggestedAction = "fix"         // 建议修复配置
	ActionRefreshKey SuggestedAction = "refresh_key" // 建议刷新 API Key
	ActionProbe      SuggestedAction = "probe"       // 建议手动探测
)

// ── 池标签 ──

// PoolTag 标记渠道/endpoint 所属的资源池。
type PoolTag string

const (
	PoolTagTemp    PoolTag = "temp"    // 临时池
	PoolTagRegular PoolTag = "regular" // 常规池
	PoolTagPremium PoolTag = "premium" // 高级池
)

// ── 限速来源 ──

// RateLimitSource 表示限速配置的来源。
type RateLimitSource string

const (
	RateLimitSourceManual      RateLimitSource = "manual"       // 手动配置
	RateLimitSourceHeader      RateLimitSource = "header"       // 从上游响应头解析
	RateLimitSourcePassiveAIMD RateLimitSource = "passive_aimd" // 被动 AIMD 探测
	RateLimitSourceUnknown     RateLimitSource = "unknown"
)

// ── EndpointUID 推导 ──

// GenerateEndpointUID 根据 ChannelUID + baseURL + keyHash 生成稳定的 endpoint 唯一标识。
// 与设计 §3.1.1 一致：EndpointUID = sha256(channelUID + "|" + baseURL + "|" + keyHash) 前 16 位十六进制。
func GenerateEndpointUID(channelUID, baseURL, keyHash string) string {
	h := sha256.New()
	h.Write([]byte(channelUID + "|" + baseURL + "|" + keyHash))
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// KeyHashFromAPIKey 对 API Key 做 sha256 摘要，用于画像存储（不保存明文 Key）。
func KeyHashFromAPIKey(apiKey string) string {
	h := sha256.New()
	h.Write([]byte(apiKey))
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// ── 用量窗口（设计 §3.2.4）──

// UsageWindow 描述一个时间窗口内的用量统计。
// 数据来源优先级：official_api > response_header > local_metering。
type UsageWindow struct {
	Window    string    `json:"window"`    // "5h" | "day" | "week" | "month"
	Used      float64   `json:"used"`      // 已用量
	Limit     float64   `json:"limit"`     // 上限，0 = 未知
	Unit      string    `json:"unit"`      // requests | tokens | credits | percent
	ResetsAt  time.Time `json:"resetsAt"`  // 窗口重置时间
	Source    string    `json:"source"`    // official_api | response_header | local_metering
	FetchedAt time.Time `json:"fetchedAt"` // 最近一次数据获取时间
}

// ── 成本画像 ──

// CostProfile 描述 endpoint 的实际成本倍率。
// 目标是把模型注册表里的公开 USD 价格换算成用户真实付费成本。
type CostProfile struct {
	// 分组倍率：key 为模型组或通配符，例如 "*"、"claude-opus"、"gpt-5"、"gemini"。
	GroupMultipliers map[string]float64 `json:"groupMultipliers,omitempty"`

	// 充值倍率：1.0=无折扣；2.0=付 1 得 2，真实成本减半。
	RechargeMultiplier float64 `json:"rechargeMultiplier,omitempty"`

	// 最终成本倍率 = groupMultiplier / rechargeMultiplier
	EffectiveCostMultiplier float64 `json:"effectiveCostMultiplier,omitempty"`

	// 基于模型注册表 Pricing x EffectiveCostMultiplier 的估算价格（每百万 token USD）。
	EffectiveInputCostPerMTok  float64 `json:"effectiveInputCostPerMTok,omitempty"`
	EffectiveOutputCostPerMTok float64 `json:"effectiveOutputCostPerMTok,omitempty"`

	Source     string  `json:"source"`     // manual | default | inferred
	Confidence float64 `json:"confidence"` // 手动配置为 1.0
}

// ── KeyEndpointProfile ──

// KeyEndpointProfile 是画像的最小单元，对应一个具体的 baseURL + apiKey 组合。
type KeyEndpointProfile struct {
	// ── 身份 ──
	AccountUID      string    `json:"accountUid"`      // 自动托管账号稳定 ID
	ChannelUID      string    `json:"channelUid"`      // 稳定渠道 ID，持久化主键
	ChannelID       int       `json:"channelId"`       // 当前配置数组 index，仅用于展示/兼容
	ChannelKind     string    `json:"channelKind"`     // messages | chat | responses | gemini | images | vectors
	EndpointUID     string    `json:"endpointUid"`     // 稳定 endpoint ID = sha256(channelUID + baseURL + keyHash)
	OriginType      string    `json:"originType"`      // official_api | official_token_plan | relay | community | local_runtime | unknown
	OriginTier      string    `json:"originTier"`      // first | second | third | local | unknown
	ServiceType     string    `json:"serviceType"`     // metrics identity 依赖 serviceType
	BaseURL         string    `json:"baseUrl"`         // 原始配置 URL
	IdentityBaseURL string    `json:"identityBaseUrl"` // MetricsIdentityBaseURL(baseURL, serviceType)
	KeyMask         string    `json:"keyMask"`         // 掩码后的 key，如 sk-***abc
	KeyHash         string    `json:"keyHash"`         // API Key 单向哈希，用于账号内凭证关联
	CredentialUID   string    `json:"credentialUid"`   // 账号内稳定凭证 ID
	MetricsKey      string    `json:"metricsKey"`      // GenerateMetricsIdentityKey(baseURL, apiKey, serviceType)
	UpdatedAt       time.Time `json:"updatedAt"`

	// ── 自动推导维度 ──
	HealthState      HealthState   `json:"healthState"`
	HealthConfidence float64       `json:"healthConfidence"` // 0.0-1.0
	QualityTier      QualityTier   `json:"qualityTier"`
	StabilityTier    StabilityTier `json:"stabilityTier"`
	SpeedTier        SpeedTier     `json:"speedTier"`
	CostTier         CostTier      `json:"costTier"`
	CostProfile      CostProfile   `json:"costProfile,omitempty"`

	// ── StabilityTier 晋降级滞后（Phase 3B-3）──
	EffectiveStabilityTier StabilityTier `json:"effectiveStabilityTier,omitempty"` // 滞后后供评分使用的稳定档；零值时调用方回退到 StabilityTier
	StabilityPendingTier   StabilityTier `json:"stabilityPendingTier,omitempty"`   // 当前正在累积连续窗口数的候选档
	StabilityPendingStreak int           `json:"stabilityPendingStreak,omitempty"` // 候选档已连续出现的轮数

	// ── 能力标签（该 endpoint 特有）──
	SupportsVision    bool `json:"supportsVision"`
	SupportsToolCalls bool `json:"supportsToolCalls"`
	SupportsReasoning bool `json:"supportsReasoning"`
	SupportsLongCtx   bool `json:"supportsLongCtx"`

	// ── 该 endpoint 的可用模型列表 ──
	AvailableModels          []string             `json:"availableModels"`                    // 当前配置协议探测到的实际模型列表
	ProtocolModels           map[string][]string  `json:"protocolModels,omitempty"`           // 上游原生协议 -> 模型列表
	ProtocolModelsHash       map[string]string    `json:"protocolModelsHash,omitempty"`       // 上游原生协议 -> 模型列表哈希
	ProtocolDiscoveredAt     map[string]time.Time `json:"protocolDiscoveredAt,omitempty"`     // 上游原生协议 -> 最近发现时间
	ProtocolDiscoverySource  map[string]string    `json:"protocolDiscoverySource,omitempty"`  // 上游原生协议 -> 清单来源
	ProtocolDiscoveryMessage map[string]string    `json:"protocolDiscoveryMessage,omitempty"` // 上游原生协议 -> 发现说明
	ProtocolDiscoveryError   map[string]string    `json:"protocolDiscoveryError,omitempty"`   // 上游原生协议 -> 最近失败原因
	ModelMapping             map[string]string    `json:"modelMapping"`                       // 该 endpoint 的模型映射

	// ── 运行时指标（来自 MetricsManager）──
	SuccessRate15m          float64    `json:"successRate15m"`
	P95LatencyMs            int64      `json:"p95LatencyMs"`
	ConnectSampleCount      int64      `json:"connectSampleCount,omitempty"`
	P95ConnectLatencyMs     int64      `json:"p95ConnectLatencyMs,omitempty"`
	ConnectStatsUpdatedAt   *time.Time `json:"connectStatsUpdatedAt,omitempty"`
	FirstByteSampleCount    int64      `json:"firstByteSampleCount,omitempty"`
	P95FirstByteLatencyMs   int64      `json:"p95FirstByteLatencyMs,omitempty"`
	FirstByteStatsUpdatedAt *time.Time `json:"firstByteStatsUpdatedAt,omitempty"`
	ConsecutiveFail         int        `json:"consecutiveFail"`
	LastSuccessAt           *time.Time `json:"lastSuccessAt,omitempty"`
	LastFailureAt           *time.Time `json:"lastFailureAt,omitempty"`

	// ── 自动限速画像 ──
	DiscoveredRPM           int     `json:"discoveredRpm,omitempty"`
	DiscoveredMaxConcurrent int     `json:"discoveredMaxConcurrent,omitempty"`
	RateLimitSource         string  `json:"rateLimitSource,omitempty"` // manual | header | passive_aimd | unknown
	RateLimitConfidence     float64 `json:"rateLimitConfidence,omitempty"`

	// ── 分组感知 ──
	DetectedGroup  string     `json:"detectedGroup,omitempty"`  // 检测到的 key 分组标识
	GroupChangedAt *time.Time `json:"groupChangedAt,omitempty"` // 分组变更时间
	ModelListHash  string     `json:"modelListHash,omitempty"`  // 模型列表哈希，用于检测变更

	// ── 模型清单发现元数据 ──
	// 与 UpdatedAt 分离：UpdatedAt 会被 L1 健康画像刷新覆盖，不能代表模型清单时间。
	ModelDiscoverySource  string     `json:"modelDiscoverySource,omitempty"`  // control_plane | models_api | builtin_manifest | builtin_fallback
	ModelDiscoveryMessage string     `json:"modelDiscoveryMessage,omitempty"` // 发现/回退说明
	ModelsDiscoveredAt    *time.Time `json:"modelsDiscoveredAt,omitempty"`    // 最近一次模型清单发现时间

	// ── 质量趋势（Phase 1 shadow）──
	QualityTrend *QualityTrend `json:"qualityTrend,omitempty"` // 当前质量趋势检测结果

	// ── 限速发现器附加字段（Phase 1 shadow）──
	SuggestedRPMSource string `json:"suggestedRpmSource,omitempty"` // 限速建议来源（header | passive_aimd | unknown）
	SuggestedRPMTPM    int    `json:"suggestedRpmTpm,omitempty"`    // 限速建议 TPM
	SuggestedRPMRPD    int    `json:"suggestedRpmRpd,omitempty"`    // 限速建议 RPD

	// ── 分组变更详情（Phase 1 shadow）──
	LastGroupChange *GroupChangeResult `json:"lastGroupChange,omitempty"` // 最近一次分组变更详情

	// ── 订阅级能力继承（§3.2.3）──
	InheritedFromSubscription bool `json:"inheritedFromSubscription,omitempty"` // 是否从订阅级能力画像继承

	// ── 用量窗口（§3.2.4）──
	UsageWindows []UsageWindow `json:"usageWindows,omitempty"` // 该 endpoint 的用量窗口列表

	// ── Provider 套餐用量 ──
	MiniMaxTokenPlanUsage      *MiniMaxTokenPlanUsage `json:"miniMaxTokenPlanUsage,omitempty"`
	MiniMaxTokenPlanUsageError string                 `json:"miniMaxTokenPlanUsageError,omitempty"`

	// ── L2 探测结果 ──
	LastProbeAt             *time.Time `json:"lastProbeAt,omitempty"`             // 最近一次 L2 探测时间
	ProbeSuccess            bool       `json:"probeSuccess"`                      // 最近一次 L2 探测是否成功
	ProbeLatencyMs          int64      `json:"probeLatencyMs"`                    // 最近一次 L2 探测延迟（ms）
	ProbeConfidence         float64    `json:"probeConfidence,omitempty"`         // 探测置信度 0.0-1.0
	ProbeStatusCode         int        `json:"probeStatusCode,omitempty"`         // 最近一次 L2 探测 HTTP 状态码
	ConsecutiveProbeSuccess int        `json:"consecutiveProbeSuccess,omitempty"` // 连续探测成功次数，失败清零；用于 degraded/limited→healthy 恢复判定（防抖动）

	// ── 诊断 ──
	HealthEvidence          []string                `json:"healthEvidence"` // 诊断证据列表
	SuggestedAction         SuggestedAction         `json:"suggestedAction"`
	EndpointInconsistencies []EndpointInconsistency `json:"endpointInconsistencies,omitempty"` // 能力漂移诊断

	// ── 元数据 ──
	Source     string  `json:"source"`     // l1_passive | l2_probe | capability_test | manual
	Confidence float64 `json:"confidence"` // 画像整体置信度
}
