package config

import (
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/BenedictKing/ccx/internal/statelog"
	"github.com/BenedictKing/ccx/internal/utils"
	"github.com/fsnotify/fsnotify"
)

// ============== 核心类型定义 ==============

// UpstreamConfig 上游配置
type UpstreamConfig struct {
	// AccountUID 自动托管账号的稳定身份。一个 provider 账号下的多协议渠道共享该值。
	AccountUID string `json:"accountUid,omitempty"`
	// ChannelUID 渠道稳定身份标识，创建后不因渠道重排、改名、API Key 变更而改变。
	// 用于画像表主键、健康证据归档等需要跨配置变更持久追踪的场景。
	// 加载旧配置时由 ConfigManager 自动补齐并持久化，格式为 "ch_" + 12 位 hex。
	ChannelUID            string                             `json:"channelUid,omitempty"`
	BaseURL               string                             `json:"baseUrl"`
	BaseURLs              []string                           `json:"baseUrls,omitempty"` // 多 BaseURL 支持（failover 模式）
	APIKeys               []string                           `json:"apiKeys"`
	APIKeyConfigs         []APIKeyConfig                     `json:"apiKeyConfigs,omitempty"`     // API Key 附加配置（限速、权重、配额组等），通过 Key 关联 APIKeys
	HistoricalAPIKeys     []string                           `json:"historicalApiKeys,omitempty"` // 历史 API Key（用于统计聚合，换 Key 后保留旧 Key 的统计数据）
	DisabledAPIKeys       []DisabledKeyInfo                  `json:"disabledApiKeys,omitempty"`   // 被禁用的 API Key（持久化；额度类可定时恢复）
	DisabledKeyModels     []DisabledKeyModelInfo             `json:"disabledKeyModels,omitempty"` // 被限制的 (Key,模型) 组合（持久化，定时自动恢复）
	ServiceType           string                             `json:"serviceType"`                 // gemini, openai, claude
	AuthHeader            string                             `json:"authHeader,omitempty"`        // 认证头覆盖：auto(空)/bearer/x-api-key
	ProviderID            string                             `json:"providerId,omitempty"`        // 来源 provider 模板 ID（如 mimo/deepseek），模板化添加时写入，用于回溯与预设引用
	Name                  string                             `json:"name,omitempty"`
	Description           string                             `json:"description,omitempty"`
	Website               string                             `json:"website,omitempty"`
	InsecureSkipVerify    bool                               `json:"insecureSkipVerify,omitempty"`
	ModelMapping          map[string]string                  `json:"modelMapping,omitempty"`
	ModelCapabilities     map[string]UpstreamModelCapability `json:"modelCapabilities,omitempty"` // 实际模型能力覆盖，key 支持模型通配符
	EmbeddingCapabilities map[string]EmbeddingCapability     `json:"embeddingCapabilities,omitempty"`
	DefaultCapability     UpstreamModelCapability            `json:"defaultCapability,omitempty"`   // 渠道默认实际模型能力
	AllowUnknownContext   bool                               `json:"allowUnknownContext,omitempty"` // 大上下文请求是否允许落到未知能力渠道
	ReasoningMapping      map[string]string                  `json:"reasoningMapping,omitempty"`
	ReasoningParamStyle   string                             `json:"reasoningParamStyle,omitempty"`
	TextVerbosity         string                             `json:"textVerbosity,omitempty"`
	FastMode              bool                               `json:"fastMode,omitempty"`
	// OpenAI Chat 上游配置：启用后将非标准 Chat role 改写为 user。
	// nil 表示用户未设置，可由运行时兼容性学习决定；非 nil 时用户配置优先。
	NormalizeNonstandardChatRoles *bool `json:"normalizeNonstandardChatRoles,omitempty"`
	// Codex 工具兼容开关。
	// 透传分支中将 Codex 原生工具转换为 OpenAI function 格式。
	CodexNativeToolPassthrough *bool `json:"codexNativeToolPassthrough,omitempty"`
	CodexToolCompat            *bool `json:"codexToolCompat,omitempty"`
	// Deprecated: 使用 codexToolCompat；保留旧字段仅用于配置读取和旧前端写入兼容。
	StripCodexClientTools bool `json:"stripCodexClientTools,omitempty"`
	// Responses/Chat 工具兼容：移除 image_generation 与 Codex image_gen 工具（兼容未开通图片生成权限的上游）
	StripImageGenerationTool *bool `json:"stripImageGenerationTool,omitempty"`
	// Images 响应兼容：当客户端请求 b64_json 而上游只返回 URL 时，下载并转换为 b64_json（默认 false）
	ConvertImageURLToB64JSON bool `json:"convertImageUrlToB64Json,omitempty"`
	// 多渠道调度相关字段
	Priority       int        `json:"priority"`                 // 渠道优先级（数字越小优先级越高，默认按索引）
	Status         string     `json:"status"`                   // 渠道状态：active（正常）, suspended（暂停）, disabled（备用池）
	PromotionUntil *time.Time `json:"promotionUntil,omitempty"` // 促销期截止时间，在此期间内优先使用此渠道（忽略trace亲和）
	LowQuality     bool       `json:"lowQuality,omitempty"`     // 低质量渠道标记：启用后强制本地估算 token，偏差>5%时使用本地值
	// 自动拉黑开关
	AutoBlacklistBalance *bool `json:"autoBlacklistBalance,omitempty"` // 余额不足时自动拉黑 Key（默认 true）
	// Claude Messages metadata.user_id 规范化开关
	NormalizeMetadataUserID *bool `json:"normalizeMetadataUserId,omitempty"` // 规范化 metadata.user_id（默认 true）
	// Messages 渠道级移除 billing header：转发前从 system 数组移除 x-anthropic-billing-header block（默认按域名推断）
	StripBillingHeader *bool `json:"stripBillingHeader,omitempty"`
	// Claude 协议空文本兼容
	StripEmptyTextBlocks *bool `json:"stripEmptyTextBlocks,omitempty"` // 转发前移除裸空 text content block（兼容严格校验的第三方 Claude 上游）
	// Claude 协议 system 角色兼容
	NormalizeSystemRoleToTopLevel bool `json:"normalizeSystemRoleToTopLevel,omitempty"` // 将 messages 中的 system 角色抽取回顶层 system 字段（针对 Opus 4.8 / Fable 5 等新客户端将 system 作为消息 role 发送，兼容仅支持 user/assistant role 的旧 Claude 上游）
	// Gemini 特定配置
	InjectDummyThoughtSignature bool `json:"injectDummyThoughtSignature,omitempty"` // 给空 thought_signature 注入 dummy 值（兼容 x666.me 等要求必须有该字段的 API）
	StripThoughtSignature       bool `json:"stripThoughtSignature,omitempty"`       // 移除 thought_signature 字段（兼容旧版 Gemini API）
	// Claude 协议 thinking 回传配置
	PassbackReasoningContent *bool `json:"passbackReasoningContent,omitempty"` // 将 thinking 块转为 reasoning_content 回传（兼容 mimo 等要求 OpenAI 风格 reasoning_content 的 Claude 协议上游）
	PassbackThinkingBlocks   *bool `json:"passbackThinkingBlocks,omitempty"`   // 将真实 reasoning_content 回传为 content[].thinking（兼容 DeepSeek/GLM 等严格 Claude thinking 上游）
	// LearnedDowngradeDeveloperRole 运行时字段：本次请求是否应用"developer role 降级为 system"。
	// 由 failover 在发送前按 渠道-Key-模型 的学习结论注入，不持久化到 config.json，
	// 也不暴露给前端配置（用户侧对应能力是 NormalizeNonstandardChatRoles）。
	LearnedDowngradeDeveloperRole bool `json:"-"`
	// 自定义请求头
	CustomHeaders map[string]string `json:"customHeaders,omitempty"` // 自定义请求头（覆盖或添加到上游请求）
	// 渠道级代理
	ProxyURL string `json:"proxyUrl,omitempty"` // HTTP/HTTPS/SOCKS5 代理地址
	// 渠道级请求超时
	RequestTimeoutMs        int `json:"requestTimeoutMs,omitempty"`        // 非流式上游请求超时时间（毫秒，0=继承全局 REQUEST_TIMEOUT）
	ResponseHeaderTimeoutMs int `json:"responseHeaderTimeoutMs,omitempty"` // 等待上游 HTTP 响应头超时（毫秒，0=继承全局 RESPONSE_HEADER_TIMEOUT）
	// 流式健康检测渠道覆盖（0=继承全局，-1=禁用，正数=覆盖全局）
	StreamFirstContentTimeoutMs int `json:"streamFirstContentTimeoutMs,omitempty"` // HTTP 200 后首个有效内容等待超时
	StreamInactivityTimeoutMs   int `json:"streamInactivityTimeoutMs,omitempty"`   // 首字后连续性确认窗口
	StreamToolCallIdleTimeoutMs int `json:"streamToolCallIdleTimeoutMs,omitempty"` // 工具调用空闲超时
	// 模型白名单
	SupportedModels []string `json:"supportedModels,omitempty"` // 支持的模型白名单（空=全部）；支持精确匹配，以及 prefix* / *suffix / *contains* 形式的包含与排除规则（排除用 ! 前缀）
	// 路由前缀
	RoutePrefix string `json:"routePrefix,omitempty"` // 路由前缀（如 "kimi"），客户端可通过 /:routePrefix/v1/messages 访问
	// 主动限速配置（渠道级，默认 0=不限）
	RateLimitRPM             int   `json:"rateLimitRpm,omitempty"`             // 每分钟请求数上限（0=不限）
	RateLimitWindowMinutes   int   `json:"rateLimitWindowMinutes,omitempty"`   // 滑动窗口时长（秒，0=默认60秒；JSON 字段名保留为兼容旧配置）
	RateLimitBurst           int   `json:"rateLimitBurst,omitempty"`           // 已废弃，保留仅为兼容性
	RateLimitMaxConcurrent   int   `json:"rateLimitMaxConcurrent,omitempty"`   // 最大并发上游请求数（0=不限）
	RateLimitAutoFromHeaders *bool `json:"rateLimitAutoFromHeaders,omitempty"` // 自动从上游响应头解析限流信息并动态调速（默认 false）

	// Vision 能力配置
	NoVision            bool     `json:"noVision,omitempty"`            // 整个渠道不支持图片输入
	NoVisionModels      []string `json:"noVisionModels,omitempty"`      // 不支持图片输入的模型列表（匹配 modelMapping 后的实际模型名）
	VisionFallbackModel string   `json:"visionFallbackModel,omitempty"` // 含图请求命中 noVisionModels 时使用的替代模型
	// 历史图片轮次限制
	HistoricalImageTurnLimit int `json:"historicalImageTurnLimit,omitempty"` // 超过此轮次的历史图片替换为占位符（0=不限制，2-10=限制轮次）
	// Compact 专用模型配置
	CompactModel string `json:"compactModel,omitempty"` // 本地 compact 时使用的上游模型名（不经过 modelMapping，为空则使用原始请求的模型）
	// AutoManaged 自动托管标记
	AutoManaged   bool       `json:"autoManaged,omitempty"`   // 渠道是否由自动托管流程创建
	AutoManagedAt *time.Time `json:"autoManagedAt,omitempty"` // 自动托管设置时间
	// Autopilot 来源信任分类（设计 §3.2.1，仅用于隐私/治理展示和同分 tie-breaker，不参与质量推导）
	// OriginType: official_api | official_token_plan | relay | community | local_runtime | unknown
	// OriginTier: first | second | third | local | unknown
	// 加载旧配置时由 ConfigManager 自动补齐为 "unknown"（不做任何基于 URL/名称的猜测推断）。
	OriginType string `json:"originType,omitempty"`
	OriginTier string `json:"originTier,omitempty"`
	// 用户自定义标签（自由文本，与受限枚举 PoolTag 完全独立）。
	// Tags 只做用户侧组织/筛选，不接入任何调度逻辑。
	Tags []string `json:"tags,omitempty"`
	// 渠道级保活验证配置（可选，nil 时继承全局与 OriginTier 分档默认）
	HealthCheck *ChannelHealthCheckConfig `json:"healthCheck,omitempty"`
}

// APIKeyConfig 描述单个 API Key 的附加调度配置。
type APIKeyConfig struct {
	Key           string `json:"key,omitempty"`
	CredentialUID string `json:"credentialUid,omitempty"`
	Name          string `json:"name,omitempty"`
	// BaseURL 该 Key 绑定的上游端点。非空时 failover 遍历仅在此 baseURL 上尝试该 Key，
	// 避免 provider 模板化添加场景下不同 plan 的 Key（如 MiMo sk-/tp-）与多 baseURL 产生无效笛卡尔积组合。
	// 为空时保持原有笛卡尔积行为（向后兼容，历史手填渠道不受影响）。
	BaseURL    string `json:"baseUrl,omitempty"`
	Enabled    *bool  `json:"enabled,omitempty"`
	QuotaGroup string `json:"quotaGroup,omitempty"`
	// GroupMultiplier 和 MaxGroupMultiplier 是自动接入的成本安全闸门。
	// 两者同时存在时，调度只会使用倍率不超过上限的 Key；任一字段缺失则保持历史 Key 的兼容行为。
	GroupMultiplier          *float64 `json:"groupMultiplier,omitempty"`
	MaxGroupMultiplier       *float64 `json:"maxGroupMultiplier,omitempty"`
	RateLimitRPM             int      `json:"rateLimitRpm,omitempty"`
	RateLimitWindowMinutes   int      `json:"rateLimitWindowMinutes,omitempty"` // 滑动窗口时长（秒；JSON 字段名保留为兼容旧配置）
	RateLimitMaxConcurrent   int      `json:"rateLimitMaxConcurrent,omitempty"`
	RateLimitAutoFromHeaders *bool    `json:"rateLimitAutoFromHeaders,omitempty"`
	Weight                   int      `json:"weight,omitempty"`
	Models                   []string `json:"models,omitempty"`
}

// ManagedAccountConfig 是自动托管 provider 的账号级持久化根。
type ManagedAccountConfig struct {
	AccountUID  string                     `json:"accountUid"`
	ProviderID  string                     `json:"providerId"`
	Name        string                     `json:"name,omitempty"`
	Credentials []ManagedAccountCredential `json:"credentials"`
}

type ManagedAccountCredential struct {
	CredentialUID       string                      `json:"credentialUid"`
	APIKey              string                      `json:"apiKey"`
	VolcengineAccessKey *VolcengineAccessKeyPair    `json:"volcengineAccessKey,omitempty"`
	MiMoConsole         *MiMoConsoleCredential      `json:"mimoConsole,omitempty"`
	CompshareConsole    *CompshareConsoleCredential `json:"compshareConsole,omitempty"`
	KimiConsole         *KimiConsoleCredential      `json:"kimiConsole,omitempty"`
}

// KimiConsoleCredential 保存 Kimi Web 会话令牌与最近一次 Kimi Code 套餐快照。
// AccessToken 属于敏感凭证，只能持久化，管理 API 不得回显。
type KimiConsoleCredential struct {
	AccessToken string                `json:"accessToken"`
	Usage       KimiCodeUsageSnapshot `json:"usage"`
}

// KimiCodeUsageSnapshot 汇总 Kimi Code 额度和会员订阅统计。
type KimiCodeUsageSnapshot struct {
	WeeklyUsage         KimiCodeQuotaWindow  `json:"weeklyUsage"`
	TotalQuota          KimiCodeQuotaWindow  `json:"totalQuota"`
	RateLimits          []KimiCodeRateLimit  `json:"rateLimits,omitempty"`
	CodeFiveHour        *KimiCodeRatioWindow `json:"codeFiveHour,omitempty"`
	CodeSevenDay        *KimiCodeRatioWindow `json:"codeSevenDay,omitempty"`
	SubscriptionBalance *KimiCodeBalance     `json:"subscriptionBalance,omitempty"`
	GiftBalances        []KimiCodeBalance    `json:"giftBalances,omitempty"`
	BoosterWallets      []KimiBoosterWallet  `json:"boosterWallets,omitempty"`
	ValidatedAt         time.Time            `json:"validatedAt"`
}

// KimiCodeQuotaWindow 是 GetUsages 返回的精确额度窗口。
type KimiCodeQuotaWindow struct {
	Used      int64  `json:"used"`
	Limit     int64  `json:"limit"`
	Remaining int64  `json:"remaining"`
	ResetTime string `json:"resetTime,omitempty"`
}

type KimiCodeRateLimit struct {
	WindowSeconds int64               `json:"windowSeconds"`
	Usage         KimiCodeQuotaWindow `json:"usage"`
}

// KimiCodeRatioWindow 是 GetSubscriptionStats 返回的比例窗口，Ratio 表示已用比例。
type KimiCodeRatioWindow struct {
	Ratio     float64 `json:"ratio"`
	Enabled   bool    `json:"enabled"`
	ResetTime string  `json:"resetTime,omitempty"`
}

type KimiCodeBalance struct {
	Feature           string  `json:"feature,omitempty"`
	Type              string  `json:"type,omitempty"`
	Unit              string  `json:"unit,omitempty"`
	AmountUsedRatio   float64 `json:"amountUsedRatio"`
	KimiCodeUsedRatio float64 `json:"kimiCodeUsedRatio"`
	ExpireTime        string  `json:"expireTime,omitempty"`
}

// KimiCodeMoney 是 GetSubscriptionStats 返回的金额字段，单位为分。
type KimiCodeMoney struct {
	Currency     string `json:"currency,omitempty"`
	PriceInCents int64  `json:"priceInCents"`
}

// KimiBoosterWallet 是 Kimi 额度加油包（按量计费余额钱包）快照。
type KimiBoosterWallet struct {
	ID                 string        `json:"id,omitempty"`
	Status             string        `json:"status,omitempty"`
	AllowTopup         bool          `json:"allowTopup"`
	MoneyLeft          KimiCodeMoney `json:"moneyLeft"`
	MoneyTotal         KimiCodeMoney `json:"moneyTotal"`
	MonthlyChargeLimit KimiCodeMoney `json:"monthlyChargeLimit"`
	MonthlyUsed        KimiCodeMoney `json:"monthlyUsed"`
}

// VolcengineAccessKeyPair 是火山云管控面签名凭证，用于 Agent/Coding Plan 识别与模型发现。
// SecretAccessKey 与推理 API Key 一样只持久化在 0600 配置文件中，管理 API 不得回显。
type VolcengineAccessKeyPair struct {
	AccessKeyID     string               `json:"accessKeyId"`
	SecretAccessKey string               `json:"secretAccessKey"`
	Plan            string               `json:"plan,omitempty"`
	PlanTier        string               `json:"planTier,omitempty"`
	PlanStatus      string               `json:"planStatus,omitempty"`
	Usage           *VolcenginePlanUsage `json:"usage,omitempty"`
}

// VolcenginePlanUsageWindow 描述火山套餐单个时间窗口的用量。
// Agent Plan 返回 Quota+Used（可算余量）；Coding Plan 仅返回 UsedPercent。
type VolcenginePlanUsageWindow struct {
	Quota       float64  `json:"quota,omitempty"`
	Used        float64  `json:"used"`
	UsedPercent *float64 `json:"usedPercent,omitempty"`
	ResetTime   int64    `json:"resetTime,omitempty"`
}

// VolcenginePlanUsage 是火山套餐用量快照。
// Agent Plan 填充 FiveHour/Daily/Weekly/Monthly（含 Quota）；
// Coding Plan 填充 FiveHour(=session)/Weekly/Monthly（仅 UsedPercent）。
type VolcenginePlanUsage struct {
	FiveHour  *VolcenginePlanUsageWindow `json:"fiveHour,omitempty"`
	Daily     *VolcenginePlanUsageWindow `json:"daily,omitempty"`
	Weekly    *VolcenginePlanUsageWindow `json:"weekly,omitempty"`
	Monthly   *VolcenginePlanUsageWindow `json:"monthly,omitempty"`
	FetchedAt time.Time                  `json:"fetchedAt"`
	Error     string                     `json:"error,omitempty"`
}

// MiMoConsoleCredential 保存 MiMo 控制台登录态与最近一次套餐用量快照。
// Cookie 属于敏感凭证，只能持久化，管理 API 不得回显。
type MiMoConsoleCredential struct {
	Cookie           string                  `json:"cookie"`
	PlanCode         string                  `json:"planCode,omitempty"`
	PlanName         string                  `json:"planName,omitempty"`
	CurrentPeriodEnd string                  `json:"currentPeriodEnd,omitempty"`
	Expired          bool                    `json:"expired,omitempty"`
	MonthUsage       MiMoTokenPlanUsageQuota `json:"monthUsage"`
	CurrentUsage     MiMoTokenPlanUsageQuota `json:"currentUsage"`
	ValidatedAt      time.Time               `json:"validatedAt"`
}

type MiMoTokenPlanUsageQuota struct {
	Used        int64   `json:"used"`
	Limit       int64   `json:"limit"`
	UsedPercent float64 `json:"usedPercent"`
}

// CompshareConsoleCredential 保存优云智算控制台登录态与最近一次套餐用量快照。
// Cookie 属于敏感凭证，只能持久化，管理 API 不得回显。
type CompshareConsoleCredential struct {
	Cookie           string                   `json:"cookie"`
	UserPlanCode     string                   `json:"userPlanCode,omitempty"`
	PlanCode         string                   `json:"planCode,omitempty"`
	PlanName         string                   `json:"planName,omitempty"`
	DisplayName      string                   `json:"displayName,omitempty"`
	Status           int                      `json:"status"`
	ConcurrencyLimit int64                    `json:"concurrencyLimit,omitempty"`
	IsTeam           bool                     `json:"isTeam,omitempty"`
	ExpireAt         int64                    `json:"expireAt,omitempty"`
	FiveHourUsage    CompsharePlanUsageWindow `json:"fiveHourUsage"`
	WeeklyUsage      CompsharePlanUsageWindow `json:"weeklyUsage"`
	MonthlyUsage     CompsharePlanUsageWindow `json:"monthlyUsage"`
	ValidatedAt      time.Time                `json:"validatedAt"`
}

type CompsharePlanUsageWindow struct {
	Used        int64 `json:"used"`
	Limit       int64 `json:"limit"`
	UpdatedAt   int64 `json:"updatedAt,omitempty"`
	NextResetAt int64 `json:"nextResetAt,omitempty"`
}

// CredentialUIDForKey 返回账号内 API Key 的稳定凭证身份。
func (u *UpstreamConfig) CredentialUIDForKey(apiKey string) string {
	for _, keyConfig := range u.APIKeyConfigs {
		if keyConfig.Key == apiKey && keyConfig.CredentialUID != "" {
			return keyConfig.CredentialUID
		}
	}
	if u.AccountUID == "" || apiKey == "" {
		return ""
	}
	return GenerateCredentialUID(u.AccountUID, apiKey)
}

// DisabledKeyInfo 被拉黑的 API Key 信息
type DisabledKeyInfo struct {
	Key        string        `json:"key"`
	Reason     string        `json:"reason"`              // "authentication_error" / "permission_error" / "insufficient_balance" / "insufficient_quota"
	Message    string        `json:"message"`             // 原始错误信息
	DisabledAt string        `json:"disabledAt"`          // ISO8601 时间戳
	RecoverAt  string        `json:"recoverAt,omitempty"` // 自动恢复时间（可选）
	Config     *APIKeyConfig `json:"config,omitempty"`    // 拉黑前的 key 配置快照，restore 时恢复
}

// DisabledKeyModelInfo 被限制的 (Key, 模型) 组合信息。
// 用于 model_not_found 等"该 Key 在此渠道下缺少特定模型"的场景：
// 仅限制该 Key 对该模型的路由，不影响该 Key 的其他模型，也不阻断 failover。
type DisabledKeyModelInfo struct {
	Key        string `json:"key"`
	Model      string `json:"model"`      // 触发限制的模型（redirectedModel）
	Reason     string `json:"reason"`     // "model_not_found" / "image_generation_not_enabled"
	Message    string `json:"message"`    // 原始错误信息摘要
	DisabledAt string `json:"disabledAt"` // RFC3339 时间戳
	RecoverAt  string `json:"recoverAt"`  // RFC3339 自动恢复时间（默认 +1h）
}

// RateLimitWindowSeconds 返回限速器实际使用的窗口秒数。
// JSON 字段名仍为 rateLimitWindowMinutes，仅用于兼容既有 API 和配置文件。
func RateLimitWindowSeconds(value int) int {
	if value > 0 {
		return value
	}
	return 0
}

// AgentModelProfile 描述下游 agent 模型的上下文管理语义。
type AgentModelProfile struct {
	ContextWindowTokens    int      `json:"contextWindowTokens,omitempty"`
	MaxContextWindowTokens int      `json:"maxContextWindowTokens,omitempty"`
	EffectiveContextRatio  float64  `json:"effectiveContextRatio,omitempty"`
	AutoCompactRatio       float64  `json:"autoCompactRatio,omitempty"`
	AutoCompactThreshold   int      `json:"autoCompactThreshold,omitempty"`
	MaxOutputTokens        int      `json:"maxOutputTokens,omitempty"`
	TruncationMode         string   `json:"truncationMode,omitempty"`
	TruncationLimit        int      `json:"truncationLimit,omitempty"`
	ReasoningEfforts       []string `json:"reasoningEfforts,omitempty"`
	SupportsPriorityTier   bool     `json:"supportsPriorityTier,omitempty"`
	DisplayName            string   `json:"displayName,omitempty"`
}

// UpstreamModelCapability 描述实际发送给上游的模型能力。
type UpstreamModelCapability struct {
	ContextWindowTokens     int             `json:"contextWindowTokens,omitempty"` // CCX 路由使用的可承载输入窗口；若来源是总上下文窗口，应在能力数据层预先扣除输出预留
	MaxOutputTokens         int             `json:"maxOutputTokens,omitempty"`
	DefaultOutputTokens     int             `json:"defaultOutputTokens,omitempty"`
	RecommendedOutputTokens int             `json:"recommendedOutputTokens,omitempty"`
	ThinkingMode            string          `json:"thinkingMode,omitempty"`
	ReasoningEfforts        []string        `json:"reasoningEfforts,omitempty"`
	Provider                string          `json:"provider,omitempty"`
	DisplayName             string          `json:"displayName,omitempty"`
	Description             string          `json:"description,omitempty"`
	Capabilities            map[string]bool `json:"capabilities,omitempty"`
	Pricing                 *ModelPricing   `json:"pricing,omitempty"`
	Sources                 []string        `json:"sources,omitempty"`
}

// ModelBenchmarkProfile 描述规范模型在独立基准中的能力上界证据。
// 该结构只用于 Autopilot 软评分，不参与模型支持、上下文或协议能力等硬过滤。
type ModelBenchmarkProfile struct {
	CanonicalModel       string                   `json:"canonicalModel"`
	OverallScore         float64                  `json:"overallScore,omitempty"`   // 0-100，仅用于展示
	CategoryScores       map[string]float64       `json:"categoryScores,omitempty"` // 0-100，保留来源领域向量
	BenchmarkEvidence    []ModelBenchmarkEvidence `json:"benchmarkEvidence,omitempty"`
	Sources              []string                 `json:"sources,omitempty"`
	VerifiedAt           string                   `json:"verifiedAt,omitempty"` // YYYY-MM-DD
	Lane                 string                   `json:"lane,omitempty"`       // provisional | verified
	SharedResults        int                      `json:"sharedResults,omitempty"`
	ComparableCategories int                      `json:"comparableCategories,omitempty"`
	TotalCategories      int                      `json:"totalCategories,omitempty"`
}

// ModelBenchmarkEvidence 保存单个基准观测，避免将特定 harness 的原始指标误作通用能力分。
// CohortPercentile 在固定 cohort 内从原始指标派生，供 Autopilot 做有界的相对软修正。
type ModelBenchmarkEvidence struct {
	Benchmark        string  `json:"benchmark"`
	BenchmarkVersion string  `json:"benchmarkVersion"`
	SourceModel      string  `json:"sourceModel"`
	Domain           string  `json:"domain"`
	Metric           string  `json:"metric"`
	RawValue         float64 `json:"rawValue"`
	Uncertainty      float64 `json:"uncertainty,omitempty"`
	CohortPercentile float64 `json:"cohortPercentile"`
	TaskCount        int     `json:"taskCount"`
	CohortSize       int     `json:"cohortSize"`
	Effort           string  `json:"effort"`
	SelectionBasis   string  `json:"selectionBasis"`
	SourceURL        string  `json:"sourceUrl"`
	CapturedAt       string  `json:"capturedAt"`
}

type EmbeddingCapability struct {
	EmbeddingSpaceID    string `json:"embeddingSpaceId,omitempty"`
	Dimensions          int    `json:"dimensions,omitempty"`
	SupportedDimensions []int  `json:"supportedDimensions,omitempty"`
	Normalized          *bool  `json:"normalized,omitempty"`
}

// ModelPricing 描述模型公开计费信息，单位与币种由 unit/currency 标明。
type ModelPricing struct {
	Unit                string             `json:"unit,omitempty"`
	Currency            string             `json:"currency,omitempty"`
	InputCacheHitPrice  *float64           `json:"inputCacheHitPrice,omitempty"`
	InputCacheMissPrice *float64           `json:"inputCacheMissPrice,omitempty"`
	OutputPrice         *float64           `json:"outputPrice,omitempty"`
	Tiers               []ModelPricingTier `json:"tiers,omitempty"`
}

// ModelPricingTier 描述按输入 token 规模区分的模型阶梯计费。
type ModelPricingTier struct {
	Label               string   `json:"label,omitempty"`
	InputTokensAbove    int      `json:"inputTokensAbove,omitempty"`
	InputTokensUpTo     int      `json:"inputTokensUpTo,omitempty"`
	InputCacheHitPrice  *float64 `json:"inputCacheHitPrice,omitempty"`
	InputCacheMissPrice *float64 `json:"inputCacheMissPrice,omitempty"`
	OutputPrice         *float64 `json:"outputPrice,omitempty"`
}

// ContextRoutingConfig 控制上下文路由过滤。
type ContextRoutingConfig struct {
	Enabled                    *bool `json:"enabled,omitempty"`
	DefaultOutputReserveTokens int   `json:"defaultOutputReserveTokens,omitempty"`
	UnknownSafeWindowTokens    int   `json:"unknownSafeWindowTokens,omitempty"`
}

const (
	ThinkingCacheDefaultTTLHours = 48
	ThinkingCacheMinTTLHours     = 1
	ThinkingCacheMaxTTLHours     = 24 * 30
)

// ThinkingCacheConfig 控制 Claude thinking 回填缓存。
type ThinkingCacheConfig struct {
	TTLHours int `json:"ttlHours,omitempty"`
}

func NormalizeThinkingCacheTTLHours(value int) int {
	if value == 0 {
		return ThinkingCacheDefaultTTLHours
	}
	if value < ThinkingCacheMinTTLHours {
		return ThinkingCacheMinTTLHours
	}
	if value > ThinkingCacheMaxTTLHours {
		return ThinkingCacheMaxTTLHours
	}
	return value
}

func (c ThinkingCacheConfig) EffectiveTTLHours() int {
	return NormalizeThinkingCacheTTLHours(c.TTLHours)
}

func (c ThinkingCacheConfig) EffectiveTTL() time.Duration {
	return time.Duration(c.EffectiveTTLHours()) * time.Hour
}

// IsAutoRecoverableDisabledReason 判断是否属于可自动恢复的拉黑原因。
func normalizeAPIKeyConfigs(apiKeys []string, configs []APIKeyConfig) []APIKeyConfig {
	keys := deduplicateStrings(apiKeys)
	if len(keys) == 0 && len(configs) == 0 {
		return nil
	}

	byKey := make(map[string]APIKeyConfig, len(configs))
	for _, cfg := range configs {
		key := strings.TrimSpace(cfg.Key)
		if key == "" {
			continue
		}
		cfg.Key = key
		cfg.Name = strings.TrimSpace(cfg.Name)
		cfg.QuotaGroup = strings.TrimSpace(cfg.QuotaGroup)
		byKey[key] = cfg
	}

	normalized := make([]APIKeyConfig, 0, len(keys))
	for _, key := range keys {
		if cfg, ok := byKey[key]; ok {
			normalized = append(normalized, cfg)
		} else {
			normalized = append(normalized, APIKeyConfig{Key: key})
		}
		delete(byKey, key)
	}
	// 保留已知但不在 active APIKeys 中的 key 配置，避免 blacklist/restore 丢失 quota/rpm 语义。
	for _, cfg := range byKey {
		normalized = append(normalized, cfg)
	}
	return normalized
}

// NormalizeAPIKeyConfigsForView 按 apiKeys 顺序返回规范化后的 Key 附加配置。
// 注意：返回切片可能共享 upstream.APIKeyConfigs 的底层存储；调用方不应在可能并发修改上游配置的场景下使用。
func NormalizeAPIKeyConfigsForView(upstream UpstreamConfig) []APIKeyConfig {
	full := normalizeAPIKeyConfigs(upstream.APIKeys, upstream.APIKeyConfigs)
	out := make([]APIKeyConfig, 0, len(full))
	for _, cfg := range full {
		if IsAPIKeyConfigEffective(cfg) {
			out = append(out, cfg)
		}
	}
	return out
}

// IsAPIKeyConfigEffective 判断 key 配置是否包含有效运行时语义，避免 view 层把默认空白配置一并返回。
func IsAPIKeyConfigEffective(cfg APIKeyConfig) bool {
	if cfg.Enabled != nil {
		return true
	}
	if strings.TrimSpace(cfg.Name) != "" || strings.TrimSpace(cfg.QuotaGroup) != "" {
		return true
	}
	if cfg.GroupMultiplier != nil || cfg.MaxGroupMultiplier != nil {
		return true
	}
	if cfg.RateLimitRPM > 0 || cfg.RateLimitWindowMinutes > 0 || cfg.RateLimitMaxConcurrent > 0 {
		return true
	}
	if cfg.RateLimitAutoFromHeaders != nil {
		return true
	}
	if cfg.Weight > 0 || len(cfg.Models) > 0 {
		return true
	}
	return false
}

// restoreAPIKeyConfig 将已保存的 key 配置合并回 configs 列表，或用默认值补齐。
func restoreAPIKeyConfig(configs []APIKeyConfig, key string, saved *APIKeyConfig) []APIKeyConfig {
	if saved != nil {
		found := false
		for i, cfg := range configs {
			if cfg.Key == key {
				configs[i] = *saved
				configs[i].Key = key
				found = true
				break
			}
		}
		if !found {
			copyCfg := *saved
			copyCfg.Key = key
			configs = append(configs, copyCfg)
		}
		return configs
	}
	return normalizeAPIKeyConfigs([]string{key}, configs)
}

func IsAutoRecoverableDisabledReason(reason string) bool {
	reason = strings.ToLower(strings.TrimSpace(reason))
	switch reason {
	case "insufficient_balance", "insufficient_quota", "billing_error", "quota":
		return true
	default:
		return false
	}
}

// IsAutoBlacklistBalanceEnabled 检查余额不足自动拉黑是否启用（默认 true）
func (u *UpstreamConfig) IsAutoBlacklistBalanceEnabled() bool {
	if u.AutoBlacklistBalance == nil {
		return true
	}
	return *u.AutoBlacklistBalance
}

// IsKeyDisabledNow 判断 API Key 当前是否处于整 Key 禁用期内。
// RecoverAt 为空表示必须手动恢复；已到期的自动恢复记录不再阻断请求。
func (u *UpstreamConfig) IsKeyDisabledNow(apiKey string, now time.Time) bool {
	if apiKey == "" || len(u.DisabledAPIKeys) == 0 {
		return false
	}
	for _, disabled := range u.DisabledAPIKeys {
		if disabled.Key != apiKey {
			continue
		}
		if disabled.RecoverAt == "" {
			return true
		}
		recoverAt, err := time.Parse(time.RFC3339, disabled.RecoverAt)
		if err != nil {
			return true
		}
		if now.Before(recoverAt) {
			return true
		}
	}
	return false
}

// IsKeyModelDisabledNow 判断 (apiKey, model) 组合当前是否处于限制期内。
// 纯只读方法，供 keypool 候选过滤与 ConfigManager 复用，避免反向依赖。
// RecoverAt 为空或已到期视为不再限制。model 比较大小写不敏感。
func (u *UpstreamConfig) IsKeyModelDisabledNow(apiKey, model string, now time.Time) bool {
	if apiKey == "" || model == "" || len(u.DisabledKeyModels) == 0 {
		return false
	}
	model = strings.ToLower(strings.TrimSpace(model))
	for _, dm := range u.DisabledKeyModels {
		if dm.Key != apiKey || strings.ToLower(strings.TrimSpace(dm.Model)) != model {
			continue
		}
		if dm.RecoverAt == "" {
			return true
		}
		recoverAt, err := time.Parse(time.RFC3339, dm.RecoverAt)
		if err != nil {
			return true // 无法解析恢复时间时保守视为仍受限
		}
		return now.Before(recoverAt)
	}
	return false
}

// IsNormalizeMetadataUserIDEnabled 检查 metadata.user_id 规范化是否启用（默认 true）
func (u *UpstreamConfig) IsNormalizeMetadataUserIDEnabled() bool {
	if u.NormalizeMetadataUserID == nil {
		return true
	}
	return *u.NormalizeMetadataUserID
}

// IsStripBillingHeaderEnabled 检查是否移除 billing header block（默认：按域名推断）。
func (u *UpstreamConfig) IsStripBillingHeaderEnabled() bool {
	if u.StripBillingHeader != nil {
		return *u.StripBillingHeader
	}
	return shouldStripBillingHeaderByDefault(u)
}

// requiresAnthropicBillingHeader 判断 base URL 是否需要 x-anthropic-billing-header。
// Anthropic 官方 API（api.anthropic.com）需要这行 header 进行计费归属，返回 true；
// 第三方代理、自建网关、本地模型等不需要，返回 false（且 cch= 每次变化会打穿上游 prompt 缓存前缀）。
// 只精确匹配主域名，子域名（如 proxy.api.anthropic.com）视为第三方；端口号不影响判断。
func requiresAnthropicBillingHeader(baseURL string) bool {
	s := strings.ToLower(strings.TrimSpace(baseURL))
	if idx := strings.Index(s, "://"); idx >= 0 {
		s = s[idx+3:]
	}
	s = strings.TrimRight(s, "/")
	host := s
	if i := strings.Index(s, "/"); i >= 0 {
		host = s[:i]
	}
	// 去掉端口号（如 api.anthropic.com:443），避免官方域名被误判为第三方
	if i := strings.LastIndex(host, ":"); i >= 0 {
		host = host[:i]
	}
	return host == "api.anthropic.com"
}

// shouldStripBillingHeaderByDefault 判断该渠道是否应默认剔除 Claude Code billing header。
// 遍历渠道所有 BaseURL：只有全部均需要 billing header 时才保留；
// 只要有一个 URL 不需要（第三方域名），则默认剔除。
// 用户显式设置 StripBillingHeader 时（非 nil），以用户配置为准，本函数不被调用。
func shouldStripBillingHeaderByDefault(upstream *UpstreamConfig) bool {
	if upstream == nil {
		return true
	}
	urls := []string{upstream.BaseURL}
	urls = append(urls, upstream.BaseURLs...)
	for _, u := range urls {
		if !requiresAnthropicBillingHeader(u) {
			return true
		}
	}
	return false
}

// IsCodexToolCompatEnabled 检查 Codex 工具兼容是否启用（默认 false）。
func (u *UpstreamConfig) IsCodexToolCompatEnabled() bool {
	if u.CodexToolCompat != nil {
		return *u.CodexToolCompat
	}
	return u.StripCodexClientTools
}

// IsRateLimitAutoFromHeadersEnabled 检查是否自动从上游响应头解析限流信息（默认 true）。
func (u *UpstreamConfig) IsRateLimitAutoFromHeadersEnabled() bool {
	if u.RateLimitAutoFromHeaders != nil {
		return *u.RateLimitAutoFromHeaders
	}
	return true
}

// 兼容性开关的三态合成。
//
// 这些开关原为静态 bool，需要用户手工勾选；现改为 *bool 三态，语义为：
//
//	非 nil  -> 用户显式设置，最高优先级（既是强制覆盖也是逃生阀）
//	nil    -> 未设置，交由运行时兼容性学习结论决定
//	学习也没有 -> 回落静态默认（多数为 false，个别按域名/关键词推断）
//
// 裸 bool 无法区分"用户显式关闭"与"用户没设置"，因此三态是自动学习能生效的前提。
// learned 传 nil 表示当前无学习结论（调用方未接入缓存或缓存未命中）。

// IsDowngradeDeveloperRoleEnabled 检查是否需要把 developer role 降级为 system。
// 该开关没有对应的用户配置字段：它完全由运行时兼容性学习驱动（用户想手工处理非标准 role 时用
// NormalizeNonstandardChatRoles）。failover 在发送前把学习结论写入 LearnedDowngradeDeveloperRole。
func (u *UpstreamConfig) IsDowngradeDeveloperRoleEnabled() bool {
	return u.LearnedDowngradeDeveloperRole
}

// BoolPtr 返回指向给定布尔值的指针，用于显式设置三态开关。
func BoolPtr(v bool) *bool {
	return &v
}

// resolveCompatSwitch 按 用户配置 > 学习结论 > 静态默认 合成最终生效值。
func resolveCompatSwitch(userSet *bool, learned *bool, staticDefault bool) bool {
	if userSet != nil {
		return *userSet
	}
	if learned != nil {
		return *learned
	}
	return staticDefault
}

// IsStripImageGenerationToolEnabled 检查是否移除 image_generation 与 Codex image_gen 工具。
func (u *UpstreamConfig) IsStripImageGenerationToolEnabled() bool {
	return u.IsStripImageGenerationToolEnabledWith(nil)
}

// IsStripImageGenerationToolEnabledWith 带学习结论的版本（默认 false）。
func (u *UpstreamConfig) IsStripImageGenerationToolEnabledWith(learned *bool) bool {
	return resolveCompatSwitch(u.StripImageGenerationTool, learned, false)
}

// IsNormalizeNonstandardChatRolesEnabled 检查是否将非标准 Chat role 改写为 user（默认 false）。
func (u *UpstreamConfig) IsNormalizeNonstandardChatRolesEnabled() bool {
	return resolveCompatSwitch(u.NormalizeNonstandardChatRoles, nil, false)
}

// IsCodexNativeToolPassthroughEnabled 检查是否将 Codex 原生工具转为 OpenAI function（默认 false）。
func (u *UpstreamConfig) IsCodexNativeToolPassthroughEnabled() bool {
	return resolveCompatSwitch(u.CodexNativeToolPassthrough, nil, false)
}

// IsStripEmptyTextBlocksEnabledWith 检查是否剥离空 text content block（默认 false）。
func (u *UpstreamConfig) IsStripEmptyTextBlocksEnabledWith(learned *bool) bool {
	return resolveCompatSwitch(u.StripEmptyTextBlocks, learned, false)
}

// IsStripEmptyTextBlocksEnabled 无学习结论的版本。
func (u *UpstreamConfig) IsStripEmptyTextBlocksEnabled() bool {
	return u.IsStripEmptyTextBlocksEnabledWith(nil)
}

// IsPassbackReasoningContentEnabledWith 检查是否将 thinking 转为 reasoning_content 回传（默认 false）。
func (u *UpstreamConfig) IsPassbackReasoningContentEnabledWith(learned *bool) bool {
	return resolveCompatSwitch(u.PassbackReasoningContent, learned, false)
}

// IsPassbackReasoningContentEnabled 无学习结论的版本。
func (u *UpstreamConfig) IsPassbackReasoningContentEnabled() bool {
	return u.IsPassbackReasoningContentEnabledWith(nil)
}

// IsPassbackThinkingBlocksEnabledWith 检查是否回传 content[].thinking（默认 false）。
func (u *UpstreamConfig) IsPassbackThinkingBlocksEnabledWith(learned *bool) bool {
	return resolveCompatSwitch(u.PassbackThinkingBlocks, learned, false)
}

// IsPassbackThinkingBlocksEnabled 无学习结论的版本。
func (u *UpstreamConfig) IsPassbackThinkingBlocksEnabled() bool {
	return u.IsPassbackThinkingBlocksEnabledWith(nil)
}

// GetEffectiveRequestTimeoutMs 返回渠道生效的非流式上游请求超时时间（毫秒）。
func (u *UpstreamConfig) GetEffectiveRequestTimeoutMs(fallbackMs int) int {
	if u.RequestTimeoutMs > 0 {
		if u.RequestTimeoutMs > MaxRequestTimeoutMs {
			return MaxRequestTimeoutMs
		}
		return u.RequestTimeoutMs
	}
	return fallbackMs
}

// GetEffectiveResponseHeaderTimeoutMs 返回渠道生效的等待上游响应头超时时间（毫秒）。
func (u *UpstreamConfig) GetEffectiveResponseHeaderTimeoutMs(fallbackMs int) int {
	if u.ResponseHeaderTimeoutMs > 0 {
		if u.ResponseHeaderTimeoutMs > MaxResponseHeaderTimeoutMs {
			return MaxResponseHeaderTimeoutMs
		}
		return u.ResponseHeaderTimeoutMs
	}
	return fallbackMs
}

// UpstreamUpdate 用于部分更新 UpstreamConfig
type UpstreamUpdate struct {
	Name                          *string                            `json:"name"`
	ServiceType                   *string                            `json:"serviceType"`
	AuthHeader                    *string                            `json:"authHeader"`
	BaseURL                       *string                            `json:"baseUrl"`
	BaseURLs                      []string                           `json:"baseUrls"`
	APIKeys                       []string                           `json:"apiKeys"`
	APIKeyConfigs                 []APIKeyConfig                     `json:"apiKeyConfigs"`
	Description                   *string                            `json:"description"`
	Website                       *string                            `json:"website"`
	InsecureSkipVerify            *bool                              `json:"insecureSkipVerify"`
	ModelMapping                  map[string]string                  `json:"modelMapping"`
	ModelCapabilities             map[string]UpstreamModelCapability `json:"modelCapabilities"`
	EmbeddingCapabilities         map[string]EmbeddingCapability     `json:"embeddingCapabilities"`
	DefaultCapability             *UpstreamModelCapability           `json:"defaultCapability"`
	AllowUnknownContext           *bool                              `json:"allowUnknownContext"`
	ReasoningMapping              map[string]string                  `json:"reasoningMapping"`
	ReasoningParamStyle           *string                            `json:"reasoningParamStyle"`
	TextVerbosity                 *string                            `json:"textVerbosity"`
	FastMode                      *bool                              `json:"fastMode"`
	NormalizeNonstandardChatRoles *bool                              `json:"normalizeNonstandardChatRoles"`
	CodexNativeToolPassthrough    *bool                              `json:"codexNativeToolPassthrough"`
	CodexToolCompat               *bool                              `json:"codexToolCompat"`
	StripCodexClientTools         *bool                              `json:"stripCodexClientTools"`
	StripImageGenerationTool      *bool                              `json:"stripImageGenerationTool"`
	ConvertImageURLToB64JSON      *bool                              `json:"convertImageUrlToB64Json"`
	// 多渠道调度相关字段
	Priority                *int       `json:"priority"`
	Status                  *string    `json:"status"`
	PromotionUntil          *time.Time `json:"promotionUntil"`
	LowQuality              *bool      `json:"lowQuality"`
	AutoBlacklistBalance    *bool      `json:"autoBlacklistBalance"`
	NormalizeMetadataUserID *bool      `json:"normalizeMetadataUserId"`
	StripBillingHeader      *bool      `json:"stripBillingHeader"`
	// Claude 协议空文本兼容
	StripEmptyTextBlocks *bool `json:"stripEmptyTextBlocks"`
	// Claude 协议 system 角色兼容
	NormalizeSystemRoleToTopLevel *bool `json:"normalizeSystemRoleToTopLevel"`
	// Gemini 特定配置
	InjectDummyThoughtSignature *bool `json:"injectDummyThoughtSignature"`
	StripThoughtSignature       *bool `json:"stripThoughtSignature"`
	PassbackReasoningContent    *bool `json:"passbackReasoningContent"`
	PassbackThinkingBlocks      *bool `json:"passbackThinkingBlocks"`
	// 自定义请求头
	CustomHeaders map[string]string `json:"customHeaders"`
	// 渠道级代理
	ProxyURL *string `json:"proxyUrl"`
	// 渠道级请求超时
	RequestTimeoutMs        *int `json:"requestTimeoutMs"`
	ResponseHeaderTimeoutMs *int `json:"responseHeaderTimeoutMs"`
	// 流式健康检测渠道覆盖（0=继承全局，-1=禁用，正数=覆盖全局）
	StreamFirstContentTimeoutMs *int `json:"streamFirstContentTimeoutMs"`
	StreamInactivityTimeoutMs   *int `json:"streamInactivityTimeoutMs"`
	StreamToolCallIdleTimeoutMs *int `json:"streamToolCallIdleTimeoutMs"`
	// 模型白名单
	SupportedModels []string `json:"supportedModels"` // 支持的模型白名单（空=全部）；支持精确匹配，以及 prefix* / *suffix / *contains* 形式的包含与排除规则（排除用 ! 前缀）
	// 路由前缀
	RoutePrefix *string `json:"routePrefix"` // 路由前缀（如 "kimi"）
	// 主动限速配置（渠道级，默认 0=不限）
	RateLimitRPM             *int  `json:"rateLimitRpm"`
	RateLimitWindowMinutes   *int  `json:"rateLimitWindowMinutes"`
	RateLimitBurst           *int  `json:"rateLimitBurst"`
	RateLimitMaxConcurrent   *int  `json:"rateLimitMaxConcurrent"`
	RateLimitAutoFromHeaders *bool `json:"rateLimitAutoFromHeaders"`

	// Vision 能力配置
	NoVision            *bool    `json:"noVision"`
	NoVisionModels      []string `json:"noVisionModels"`
	VisionFallbackModel *string  `json:"visionFallbackModel"`
	// 历史图片轮次限制（0=不限制，2-10=限制轮次）
	HistoricalImageTurnLimit *int `json:"historicalImageTurnLimit"`
	// 自动托管字段
	AutoManaged   *bool      `json:"autoManaged"`
	AutoManagedAt *time.Time `json:"autoManagedAt"`
	// 用户自定义标签（nil=不修改，空切片=清空标签）
	Tags []string `json:"tags"`
}

// Config 配置结构
// CircuitBreakerConfig 熔断器运行时配置（所有字段可选，nil 使用默认值）
type CircuitBreakerConfig struct {
	WindowSize                   *int     `json:"windowSize,omitempty"`
	FailureThreshold             *float64 `json:"failureThreshold,omitempty"`
	ConsecutiveFailuresThreshold *int     `json:"consecutiveFailuresThreshold,omitempty"`
	// 上游请求生命周期全局默认参数
	RequestTimeoutMs        *int `json:"requestTimeoutMs,omitempty"`        // 非流式上游请求超时（ms，范围 1000-300000）
	ResponseHeaderTimeoutMs *int `json:"responseHeaderTimeoutMs,omitempty"` // 等待上游 HTTP 响应头超时（ms，范围 1000-300000）
	// 流式健康检测全局默认参数
	StreamFirstContentTimeoutMs *int `json:"streamFirstContentTimeoutMs,omitempty"` // HTTP 200 后首个有效内容等待超时（ms，范围 5000-300000）
	StreamInactivityTimeoutMs   *int `json:"streamInactivityTimeoutMs,omitempty"`   // 首字后连续性确认窗口（ms，范围 1000-180000）
	StreamToolCallIdleTimeoutMs *int `json:"streamToolCallIdleTimeoutMs,omitempty"` // 工具调用空闲超时（ms，范围 30000-300000）
}

type Config struct {
	ManagedAccounts []ManagedAccountConfig `json:"managedAccounts,omitempty"`
	Upstream        []UpstreamConfig       `json:"upstream"`
	CurrentUpstream int                    `json:"currentUpstream,omitempty"` // 已废弃：旧格式兼容用

	// Responses 接口专用配置（独立于 /v1/messages）
	ResponsesUpstream        []UpstreamConfig `json:"responsesUpstream"`
	CurrentResponsesUpstream int              `json:"currentResponsesUpstream,omitempty"` // 已废弃：旧格式兼容用

	// Gemini 接口专用配置（独立于 /v1/messages 和 /v1/responses）
	GeminiUpstream []UpstreamConfig `json:"geminiUpstream"`

	// Chat Completions 接口专用配置（OpenAI /v1/chat/completions 兼容）
	ChatUpstream []UpstreamConfig `json:"chatUpstream,omitempty"`

	// Images 接口专用配置（OpenAI /v1/images/generations 兼容）
	ImagesUpstream []UpstreamConfig `json:"imagesUpstream,omitempty"`

	// Vectors 接口专用配置（OpenAI /v1/embeddings 兼容）
	VectorsUpstream []UpstreamConfig `json:"vectorsUpstream,omitempty"`

	// 上下文路由配置与全局能力覆盖。
	ContextRouting            ContextRoutingConfig               `json:"contextRouting,omitempty"`
	AgentModelProfiles        map[string]AgentModelProfile       `json:"agentModelProfiles,omitempty"`
	UpstreamModelCapabilities map[string]UpstreamModelCapability `json:"upstreamModelCapabilities,omitempty"`

	// Fuzzy 模式：启用时模糊处理错误，所有非 2xx 错误都尝试 failover
	FuzzyModeEnabled bool `json:"fuzzyModeEnabled"`

	// 移除计费头中的 cch= 参数：兼容旧全局配置读取；新语义已下沉到渠道级字段
	StripBillingHeader bool `json:"stripBillingHeader,omitempty"`

	// 驾驶舱 override 默认有效期（分钟，1-1440；0 或未设置时使用环境变量 OVERRIDE_TTL_MINUTES）
	OverrideTTLMinutes int `json:"overrideTtlMinutes,omitempty"`

	// Claude thinking 回填缓存配置
	ThinkingCache ThinkingCacheConfig `json:"thinkingCache,omitempty"`

	// AutopilotRouting 智能路由全局配置（§9.1）。
	// 对应 config.json 的 "autopilot" 字段，缺失时使用 DefaultAutopilotRoutingConfig()。
	// 热重载生效：文件变更后自动 reload，autopilot Manager 通过 RegisterOnConfigChange 回调感知。
	AutopilotRouting AutopilotRoutingConfig `json:"autopilot,omitempty"`

	// 熔断器运行时配置（可选，nil 使用环境变量或代码默认值）
	CircuitBreaker *CircuitBreakerConfig `json:"circuitBreaker,omitempty"`

	// 渠道保活验证全局配置（可选，nil 使用默认值）
	HealthCheck *GlobalHealthCheckConfig `json:"healthCheck,omitempty"`
}

// FailedKey 失败密钥记录
type FailedKey struct {
	Timestamp    time.Time
	FailureCount int
}

// ConfigManager 配置管理器
type ConfigManager struct {
	mu                    sync.RWMutex
	config                Config
	configFile            string
	backupDir             string
	watcher               *fsnotify.Watcher
	failedKeysCache       map[string]*FailedKey
	keyRecoveryTime       time.Duration
	maxFailureCount       int
	stopChan              chan struct{} // 用于通知 goroutine 停止
	reloadCh              chan struct{} // 配置文件变化信号，由 watcher 发送、单独 goroutine 消费
	closeOnce             sync.Once     // 确保 Close 只执行一次
	backgroundWG          sync.WaitGroup
	configChangeCallbacks []func(Config)
}

// failedKeyCacheKey 构造 FailedKeysCache 的复合键（apiType:apiKey）
func failedKeyCacheKey(apiType, apiKey string) string {
	return apiType + ":" + apiKey
}

// ============== 核心共享方法 ==============

// GetConfig 获取配置（返回深拷贝，确保并发安全）
func (cm *ConfigManager) GetConfig() Config {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	// 深拷贝整个 Config 结构体
	cloned := cm.config
	if cm.config.ManagedAccounts != nil {
		cloned.ManagedAccounts = make([]ManagedAccountConfig, len(cm.config.ManagedAccounts))
		for i, account := range cm.config.ManagedAccounts {
			cloned.ManagedAccounts[i] = account
			cloned.ManagedAccounts[i].Credentials = make([]ManagedAccountCredential, len(account.Credentials))
			for j, credential := range account.Credentials {
				cloned.ManagedAccounts[i].Credentials[j] = credential
				if credential.VolcengineAccessKey != nil {
					pair := *credential.VolcengineAccessKey
					cloned.ManagedAccounts[i].Credentials[j].VolcengineAccessKey = &pair
				}
				if credential.MiMoConsole != nil {
					console := *credential.MiMoConsole
					cloned.ManagedAccounts[i].Credentials[j].MiMoConsole = &console
				}
				if credential.CompshareConsole != nil {
					console := *credential.CompshareConsole
					cloned.ManagedAccounts[i].Credentials[j].CompshareConsole = &console
				}
				if credential.KimiConsole != nil {
					cloned.ManagedAccounts[i].Credentials[j].KimiConsole = cloneKimiConsoleCredential(credential.KimiConsole)
				}
			}
		}
	}

	// 深拷贝 Upstream slice
	if cm.config.Upstream != nil {
		cloned.Upstream = make([]UpstreamConfig, len(cm.config.Upstream))
		for i := range cm.config.Upstream {
			cloned.Upstream[i] = *cm.config.Upstream[i].Clone()
		}
	}

	// 深拷贝 ResponsesUpstream slice
	if cm.config.ResponsesUpstream != nil {
		cloned.ResponsesUpstream = make([]UpstreamConfig, len(cm.config.ResponsesUpstream))
		for i := range cm.config.ResponsesUpstream {
			cloned.ResponsesUpstream[i] = *cm.config.ResponsesUpstream[i].Clone()
		}
	}

	// 深拷贝 GeminiUpstream slice
	if cm.config.GeminiUpstream != nil {
		cloned.GeminiUpstream = make([]UpstreamConfig, len(cm.config.GeminiUpstream))
		for i := range cm.config.GeminiUpstream {
			cloned.GeminiUpstream[i] = *cm.config.GeminiUpstream[i].Clone()
		}
	}

	// 深拷贝 ChatUpstream slice
	if len(cm.config.ChatUpstream) > 0 {
		cloned.ChatUpstream = make([]UpstreamConfig, len(cm.config.ChatUpstream))
		for i := range cm.config.ChatUpstream {
			cloned.ChatUpstream[i] = *cm.config.ChatUpstream[i].Clone()
		}
	}

	// 深拷贝 ImagesUpstream slice
	if len(cm.config.ImagesUpstream) > 0 {
		cloned.ImagesUpstream = make([]UpstreamConfig, len(cm.config.ImagesUpstream))
		for i := range cm.config.ImagesUpstream {
			cloned.ImagesUpstream[i] = *cm.config.ImagesUpstream[i].Clone()
		}
	}

	// 深拷贝 VectorsUpstream slice
	if len(cm.config.VectorsUpstream) > 0 {
		cloned.VectorsUpstream = make([]UpstreamConfig, len(cm.config.VectorsUpstream))
		for i := range cm.config.VectorsUpstream {
			cloned.VectorsUpstream[i] = *cm.config.VectorsUpstream[i].Clone()
		}
	}

	if cm.config.ContextRouting.Enabled != nil {
		v := *cm.config.ContextRouting.Enabled
		cloned.ContextRouting.Enabled = &v
	}
	if cm.config.AgentModelProfiles != nil {
		cloned.AgentModelProfiles = make(map[string]AgentModelProfile, len(cm.config.AgentModelProfiles))
		for k, v := range cm.config.AgentModelProfiles {
			cloned.AgentModelProfiles[k] = cloneAgentModelProfile(v)
		}
	}
	if cm.config.UpstreamModelCapabilities != nil {
		cloned.UpstreamModelCapabilities = make(map[string]UpstreamModelCapability, len(cm.config.UpstreamModelCapabilities))
		for k, v := range cm.config.UpstreamModelCapabilities {
			cloned.UpstreamModelCapabilities[k] = cloneUpstreamModelCapability(v)
		}
	}

	// 深拷贝 CircuitBreaker 指针字段
	if cm.config.CircuitBreaker != nil {
		cb := *cm.config.CircuitBreaker
		cloned.CircuitBreaker = &cb
	}

	// 深拷贝 AutopilotRouting（map 字段需要独立分配）
	cloned.AutopilotRouting = cm.config.AutopilotRouting.deepCopy()
	applyAutopilotEnvOverrides(&cloned.AutopilotRouting)

	return cloned
}

// GetUpstreamByIndex 返回指定协议与索引对应的渠道深拷贝。
// 相比 GetConfig，它只克隆一个 UpstreamConfig，适合调度热路径中的单渠道读取。
func (cm *ConfigManager) GetUpstreamByIndex(apiType string, index int) *UpstreamConfig {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	upstreams := cm.getUpstreamSliceLocked(apiType)
	if upstreams == nil || index < 0 || index >= len(*upstreams) {
		return nil
	}
	return (*upstreams)[index].Clone()
}

// GetNextAPIKey 获取下一个 API 密钥（纯 failover 模式）
// apiType: 接口类型（Messages/Responses/Gemini），用于日志标签前缀
func (cm *ConfigManager) GetNextAPIKey(upstream *UpstreamConfig, failedKeys map[string]bool, apiType string) (string, error) {
	if len(upstream.APIKeys) == 0 {
		return "", fmt.Errorf("上游 %s 没有可用的API密钥", upstream.Name)
	}

	// 筛选可用密钥：排除持久禁用、本次请求失败和内存冷却中的密钥。
	now := time.Now()
	availableKeys := []string{}
	for _, key := range upstream.APIKeys {
		if !failedKeys[key] && !upstream.IsKeyDisabledNow(key, now) && upstream.IsAPIKeyGroupMultiplierAllowed(key) && !cm.isKeyFailed(key, apiType) {
			availableKeys = append(availableKeys, key)
		}
	}

	if len(availableKeys) == 0 {
		// 如果所有密钥都失效，尝试选择失败时间最早的密钥（恢复尝试）
		var oldestFailedKey string
		oldestTime := now

		cm.mu.RLock()
		for _, key := range upstream.APIKeys {
			if !failedKeys[key] && !upstream.IsKeyDisabledNow(key, now) && upstream.IsAPIKeyGroupMultiplierAllowed(key) { // 排除本次请求失败、持久禁用或超过成本上限的密钥
				cacheKey := failedKeyCacheKey(apiType, key)
				if failure, exists := cm.failedKeysCache[cacheKey]; exists {
					if failure.Timestamp.Before(oldestTime) {
						oldestTime = failure.Timestamp
						oldestFailedKey = key
					}
				}
			}
		}
		cm.mu.RUnlock()

		if oldestFailedKey != "" {
			log.Printf("[%s-Key] 警告: 所有密钥都失效，尝试最早失败的密钥: %s", apiType, utils.MaskAPIKey(oldestFailedKey))
			return oldestFailedKey, nil
		}

		return "", fmt.Errorf("上游 %s 的所有API密钥都暂时不可用", upstream.Name)
	}

	// 纯 failover：按优先级顺序选择第一个可用密钥
	selectedKey := availableKeys[0]
	// 获取该密钥在原始列表中的索引
	keyIndex := 0
	for i, key := range upstream.APIKeys {
		if key == selectedKey {
			keyIndex = i + 1
			break
		}
	}
	log.Printf("[%s-Key] 故障转移选择密钥 %s (%d/%d)", apiType, utils.MaskAPIKey(selectedKey), keyIndex, len(upstream.APIKeys))
	return selectedKey, nil
}

// GetAdminAPIKey 获取管理/探测场景下的 API 密钥。
// 优先使用活跃 APIKeys；若活跃密钥不可用，则临时借用 DisabledAPIKeys 中的密钥。
// 返回值 fallback=true 表示本次借用了已拉黑密钥。
func (cm *ConfigManager) GetAdminAPIKey(upstream *UpstreamConfig, failedKeys map[string]bool, apiType string) (apiKey string, fallback bool, err error) {
	apiKey, err = cm.GetNextAPIKey(upstream, failedKeys, apiType)
	if err == nil {
		return apiKey, false, nil
	}

	for _, disabledKey := range upstream.DisabledAPIKeys {
		if failedKeys[disabledKey.Key] {
			continue
		}
		if disabledKey.Config != nil && !IsAPIKeyConfigGroupMultiplierAllowed(*disabledKey.Config) {
			continue
		}
		log.Printf("[%s-Key] 警告: 活跃密钥不可用，临时借用已拉黑密钥用于管理操作: %s", apiType, utils.MaskAPIKey(disabledKey.Key))
		return disabledKey.Key, true, nil
	}

	return "", false, err
}

// MarkKeyAsFailed 标记密钥失败
// apiType: 接口类型（Messages/Responses/Gemini/Chat），用于日志标签前缀和缓存键隔离
func (cm *ConfigManager) MarkKeyAsFailed(apiKey string, apiType string) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cacheKey := failedKeyCacheKey(apiType, apiKey)
	if failure, exists := cm.failedKeysCache[cacheKey]; exists {
		failure.FailureCount++
		failure.Timestamp = time.Now()
	} else {
		cm.failedKeysCache[cacheKey] = &FailedKey{
			Timestamp:    time.Now(),
			FailureCount: 1,
		}
	}

	failure := cm.failedKeysCache[cacheKey]
	recoveryTime := cm.keyRecoveryTime
	if failure.FailureCount > cm.maxFailureCount {
		recoveryTime = cm.keyRecoveryTime * 2
	}

	log.Printf("[%s-Key] 标记API密钥失败: %s (失败次数: %d, 恢复时间: %v)",
		apiType, utils.MaskAPIKey(apiKey), failure.FailureCount, recoveryTime)
}

// isKeyFailed 检查密钥是否失败
func (cm *ConfigManager) isKeyFailed(apiKey, apiType string) bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	cacheKey := failedKeyCacheKey(apiType, apiKey)
	failure, exists := cm.failedKeysCache[cacheKey]
	if !exists {
		return false
	}

	recoveryTime := cm.keyRecoveryTime
	if failure.FailureCount > cm.maxFailureCount {
		recoveryTime = cm.keyRecoveryTime * 2
	}

	return time.Since(failure.Timestamp) < recoveryTime
}

// IsKeyFailed 检查 Key 是否在冷却期（公开方法）
func (cm *ConfigManager) IsKeyFailed(apiKey, apiType string) bool {
	return cm.isKeyFailed(apiKey, apiType)
}

// clearFailedKeysForUpstream 清理指定渠道的所有失败 key 记录
// 当渠道被删除时调用，避免内存泄漏和冷却状态残留
// apiType: 接口类型（Messages/Responses/Gemini/Chat），用于日志标签前缀和缓存键隔离
func (cm *ConfigManager) clearFailedKeysForUpstream(upstream *UpstreamConfig, apiType string) {
	for _, key := range upstream.APIKeys {
		cacheKey := failedKeyCacheKey(apiType, key)
		if _, exists := cm.failedKeysCache[cacheKey]; exists {
			delete(cm.failedKeysCache, cacheKey)
			log.Printf("[%s-Key] 已清理被删除渠道 %s 的失败密钥记录: %s", apiType, upstream.Name, utils.MaskAPIKey(key))
		}
	}
}

// removeDisabledKeyModelsForKey 移除指定 Key 的所有 (Key,模型) 组合限制。
// 用于 Key 重新加入渠道时清理其历史模型限制。调用方需持有锁。
func removeDisabledKeyModelsForKey(upstream *UpstreamConfig, apiKey string) {
	if len(upstream.DisabledKeyModels) == 0 {
		return
	}
	newList := make([]DisabledKeyModelInfo, 0, len(upstream.DisabledKeyModels))
	for _, dm := range upstream.DisabledKeyModels {
		if dm.Key != apiKey {
			newList = append(newList, dm)
		}
	}
	upstream.DisabledKeyModels = newList
}

// cleanupExpiredFailures 清理过期的失败记录
func (cm *ConfigManager) cleanupExpiredFailures() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-cm.stopChan:
			return
		case <-ticker.C:
			cm.mu.Lock()
			now := time.Now()
			for key, failure := range cm.failedKeysCache {
				recoveryTime := cm.keyRecoveryTime
				if failure.FailureCount > cm.maxFailureCount {
					recoveryTime = cm.keyRecoveryTime * 2
				}

				if now.Sub(failure.Timestamp) > recoveryTime {
					delete(cm.failedKeysCache, key)
					log.Printf("[Config-Key] API密钥 %s 已从失败列表中恢复", utils.MaskAPIKey(key))
				}
			}
			cm.mu.Unlock()
		}
	}
}

// ============== Fuzzy 模式相关方法 ==============

// GetFuzzyModeEnabled 获取 Fuzzy 模式状态
func (cm *ConfigManager) GetFuzzyModeEnabled() bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.config.FuzzyModeEnabled
}

// SetFuzzyModeEnabled 设置 Fuzzy 模式状态
func (cm *ConfigManager) SetFuzzyModeEnabled(enabled bool) error {
	cm.mu.Lock()

	cm.config.FuzzyModeEnabled = enabled

	if err := cm.saveConfigLocked(cm.config); err != nil {
		cm.mu.Unlock()
		return err
	}

	status := "关闭"
	if enabled {
		status = "启用"
	}
	log.Printf("[Config-FuzzyMode] Fuzzy 模式已%s", status)

	cm.fireConfigChangeCallbacks()
	return nil
}

// ============== HistoricalImageTurnLimit 相关方法 ==============

// 历史图片轮次限制约束：
//   - 0 表示不限制
//   - 大于 0 时有效值范围为 2-10
const (
	HistoricalImageTurnLimitMin = 2
	HistoricalImageTurnLimitMax = 10
)

// NormalizeChannelHistoricalImageTurnLimit 归一化渠道级历史图片轮次限制。
//   - limit <= 0 → 0（表示该渠道不裁剪历史图片）
//   - 0 < limit < 2 → 最低值 2
//   - 2 <= limit <= 10 → 原值
//   - limit > 10 → 最高值 10
func NormalizeChannelHistoricalImageTurnLimit(limit int) int {
	if limit <= 0 {
		return 0
	}
	if limit < HistoricalImageTurnLimitMin {
		return HistoricalImageTurnLimitMin
	}
	if limit > HistoricalImageTurnLimitMax {
		return HistoricalImageTurnLimitMax
	}
	return limit
}

// SetOverrideTTLMinutes 设置驾驶舱 override 默认有效期（分钟）
func (cm *ConfigManager) SetOverrideTTLMinutes(minutes int) error {
	cm.mu.Lock()

	// 标准化为界面提供的固定选项，不合适的值使用默认 30 分钟
	// 有效选项：-1（永不恢复）, 15, 30, 60, 120, 240, 480, 720, 1440 分钟
	validOptions := []int{-1, 15, 30, 60, 120, 240, 480, 720, 1440}
	normalized := 30 // 默认 30 分钟
	for _, option := range validOptions {
		if minutes == option {
			normalized = minutes
			break
		}
	}

	cm.config.OverrideTTLMinutes = normalized

	if err := cm.saveConfigLocked(cm.config); err != nil {
		cm.mu.Unlock()
		return err
	}

	if normalized == -1 {
		log.Printf("[Config-OverrideTTL] 驾驶舱 override 默认有效期已设置为永不恢复")
	} else {
		if minutes != normalized {
			log.Printf("[Config-OverrideTTL] 驾驶舱 override 默认有效期已标准化为 %d 分钟（原值: %d）", normalized, minutes)
		} else {
			log.Printf("[Config-OverrideTTL] 驾驶舱 override 默认有效期已设置为 %d 分钟", normalized)
		}
	}

	cm.fireConfigChangeCallbacks()
	return nil
}

// ============== API Key 拉黑相关方法 ==============

// BlacklistKey 将指定 Key 从活跃列表移到拉黑列表（持久化）
// apiType: Messages/Responses/Gemini/Chat，用于定位 upstream slice
// channelIndex: 渠道在 upstream slice 中的索引
func (cm *ConfigManager) BlacklistKey(apiType string, channelIndex int, apiKey string, reason string, message string) error {
	return cm.BlacklistKeyWithRecoverAt(apiType, channelIndex, apiKey, reason, message, "")
}

// BlacklistKeyWithRecoverAt 将指定 Key 禁用到上游明确给出的恢复时间。
// recoverAt 必须为未来的 RFC3339 时间；为空、无效或已过期时沿用原因级默认策略。
func (cm *ConfigManager) BlacklistKeyWithRecoverAt(apiType string, channelIndex int, apiKey string, reason string, message string, recoverAt string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	upstreams := cm.getUpstreamSliceLocked(apiType)
	if upstreams == nil || channelIndex < 0 || channelIndex >= len(*upstreams) {
		return fmt.Errorf("无效的渠道索引: %s[%d]", apiType, channelIndex)
	}

	upstream := &(*upstreams)[channelIndex]

	wasActive := slices.Contains(upstream.APIKeys, apiKey)
	wasDisabled := slices.ContainsFunc(upstream.DisabledAPIKeys, func(disabled DisabledKeyInfo) bool {
		return disabled.Key == apiKey
	})
	if !wasActive && !wasDisabled {
		return nil
	}

	// 在移除活跃 Key 前保存附加配置；重复拉黑时沿用已有快照。
	var disabledCfg *APIKeyConfig
	for _, cfg := range upstream.APIKeyConfigs {
		if cfg.Key == apiKey {
			copyCfg := cloneAPIKeyConfig(cfg)
			disabledCfg = &copyCfg
			break
		}
	}
	if disabledCfg == nil {
		for _, disabled := range upstream.DisabledAPIKeys {
			if disabled.Key == apiKey && disabled.Config != nil {
				copyCfg := cloneAPIKeyConfig(*disabled.Config)
				disabledCfg = &copyCfg
				break
			}
		}
	}

	upstream.APIKeys = slices.DeleteFunc(upstream.APIKeys, func(key string) bool {
		return key == apiKey
	})
	upstream.APIKeyConfigs = normalizeAPIKeyConfigs(upstream.APIKeys, upstream.APIKeyConfigs)

	// 同一 Key 只保留一条禁用记录；再次命中时刷新原因和恢复时间。
	now := time.Now()
	disabledAt := now.Format(time.RFC3339)
	resolvedRecoverAt := ""
	if IsAutoRecoverableDisabledReason(reason) {
		resolvedRecoverAt = now.Add(time.Hour).Format(time.RFC3339)
		if strings.TrimSpace(recoverAt) == "" {
			recoverAt = utils.ExtractQuotaRecoverAt(message)
		}
		if explicit, err := time.Parse(time.RFC3339, strings.TrimSpace(recoverAt)); err == nil && explicit.After(now) {
			resolvedRecoverAt = explicit.Format(time.RFC3339)
		}
	}
	refreshed := DisabledKeyInfo{
		Key:        apiKey,
		Reason:     reason,
		Message:    message,
		DisabledAt: disabledAt,
		RecoverAt:  resolvedRecoverAt,
		Config:     disabledCfg,
	}
	disabledKeys := make([]DisabledKeyInfo, 0, len(upstream.DisabledAPIKeys)+1)
	disabledKeys = append(disabledKeys, refreshed)
	for _, disabled := range upstream.DisabledAPIKeys {
		if disabled.Key != apiKey {
			disabledKeys = append(disabledKeys, disabled)
		}
	}
	upstream.DisabledAPIKeys = disabledKeys

	// 同时添加到 HistoricalAPIKeys（保留统计数据）
	if !slices.Contains(upstream.HistoricalAPIKeys, apiKey) {
		upstream.HistoricalAPIKeys = append(upstream.HistoricalAPIKeys, apiKey)
	}

	fromState := "active"
	if !wasActive {
		fromState = "disabled"
	}
	log.Printf("[%s-Blacklist] Key %s 禁用记录已更新 (原因: %s, 渠道: %s, 剩余Key: %d)",
		apiType, utils.MaskAPIKey(apiKey), reason, upstream.Name, len(upstream.APIKeys))
	statelog.LogStateTransition(apiType+"-Blacklist", "key", utils.MaskAPIKey(apiKey), fromState, "disabled", reason, "channel="+upstream.Name)

	if len(upstream.APIKeys) == 0 {
		log.Printf("[%s-Blacklist] 警告: 渠道 %s 的所有 Key 都已被拉黑！", apiType, upstream.Name)
	}

	return cm.saveConfigLocked(cm.config)
}

// ============== 熔断器运行时配置 ==============

// GetCircuitBreakerConfig 获取熔断器运行时配置
func (cm *ConfigManager) GetCircuitBreakerConfig() CircuitBreakerConfig {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	if cm.config.CircuitBreaker == nil {
		return CircuitBreakerConfig{}
	}
	return *cm.config.CircuitBreaker
}

// SetCircuitBreakerConfig 更新熔断器运行时配置（partial update，nil 字段不覆盖）
func (cm *ConfigManager) SetCircuitBreakerConfig(update CircuitBreakerConfig) error {
	cm.mu.Lock()

	if cm.config.CircuitBreaker == nil {
		cm.config.CircuitBreaker = &CircuitBreakerConfig{}
	}
	cb := cm.config.CircuitBreaker
	if update.WindowSize != nil {
		v := *update.WindowSize
		if v < 3 {
			v = 3
		} else if v > 100 {
			v = 100
		}
		cb.WindowSize = &v
	}
	if update.FailureThreshold != nil {
		v := *update.FailureThreshold
		if v < 0.01 {
			v = 0.01
		} else if v > 1.0 {
			v = 1.0
		}
		cb.FailureThreshold = &v
	}
	if update.ConsecutiveFailuresThreshold != nil {
		v := *update.ConsecutiveFailuresThreshold
		if v < 1 {
			v = 1
		} else if v > 100 {
			v = 100
		}
		cb.ConsecutiveFailuresThreshold = &v
	}
	if update.RequestTimeoutMs != nil {
		v := clampInt(*update.RequestTimeoutMs, MinRequestTimeoutMs, MaxRequestTimeoutMs)
		cb.RequestTimeoutMs = &v
	}
	if update.ResponseHeaderTimeoutMs != nil {
		v := clampInt(*update.ResponseHeaderTimeoutMs, MinResponseHeaderTimeoutMs, MaxResponseHeaderTimeoutMs)
		cb.ResponseHeaderTimeoutMs = &v
	}
	if update.StreamFirstContentTimeoutMs != nil {
		v := *update.StreamFirstContentTimeoutMs
		if v < 5000 {
			v = 5000
		} else if v > 300000 {
			v = 300000
		}
		cb.StreamFirstContentTimeoutMs = &v
	}
	if update.StreamInactivityTimeoutMs != nil {
		v := *update.StreamInactivityTimeoutMs
		if v < 1000 {
			v = 1000
		} else if v > 180000 {
			v = 180000
		}
		cb.StreamInactivityTimeoutMs = &v
	}
	if update.StreamToolCallIdleTimeoutMs != nil {
		v := *update.StreamToolCallIdleTimeoutMs
		if v < 30000 {
			v = 30000
		} else if v > 300000 {
			v = 300000
		}
		cb.StreamToolCallIdleTimeoutMs = &v
	}

	if err := cm.saveConfigLocked(cm.config); err != nil {
		cm.mu.Unlock()
		return err
	}

	log.Printf("[Config-CircuitBreaker] 熔断器配置已更新")
	cm.fireConfigChangeCallbacks()
	return nil
}

// RegisterOnConfigChange 注册配置变更回调（在 loadConfig / Set 成功后触发）
func (cm *ConfigManager) RegisterOnConfigChange(fn func(Config)) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	cm.configChangeCallbacks = append(cm.configChangeCallbacks, fn)
}

// fireConfigChangeCallbacks 在锁外通知所有已注册的回调
// 调用方需已持有 cm.mu，本方法会在内部释放锁
// fireConfigChangeCallbacks 在锁外异步通知所有已注册的回调
// 调用方需已持有 cm.mu，本方法会在内部释放锁
func (cm *ConfigManager) fireConfigChangeCallbacks() {
	snapshot := cm.config.deepCopy()
	applyAutopilotEnvOverrides(&snapshot.AutopilotRouting)
	callbacks := cm.configChangeCallbacks
	cm.mu.Unlock()
	// 异步执行回调，避免回调中触发配置写操作导致重入死锁
	if len(callbacks) > 0 {
		go func() {
			for _, fn := range callbacks {
				fn(snapshot)
			}
		}()
	}
}

// RestoreKey 将指定 Key 从拉黑列表恢复到活跃列表（持久化）
func (cm *ConfigManager) RestoreKey(apiType string, channelIndex int, apiKey string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	upstreams := cm.getUpstreamSliceLocked(apiType)
	if upstreams == nil || channelIndex < 0 || channelIndex >= len(*upstreams) {
		return fmt.Errorf("无效的渠道索引: %s[%d]", apiType, channelIndex)
	}

	upstream := &(*upstreams)[channelIndex]

	// 查找并移除
	disabledIdx := -1
	for i, dk := range upstream.DisabledAPIKeys {
		if dk.Key == apiKey {
			disabledIdx = i
			break
		}
	}
	if disabledIdx == -1 {
		return fmt.Errorf("key %s 不在拉黑列表中", utils.MaskAPIKey(apiKey))
	}

	savedCfg := upstream.DisabledAPIKeys[disabledIdx].Config
	upstream.DisabledAPIKeys = append(upstream.DisabledAPIKeys[:disabledIdx], upstream.DisabledAPIKeys[disabledIdx+1:]...)
	if !slices.Contains(upstream.APIKeys, apiKey) {
		upstream.APIKeys = append(upstream.APIKeys, apiKey)
	}
	upstream.APIKeyConfigs = restoreAPIKeyConfig(upstream.APIKeyConfigs, apiKey, savedCfg)

	// 从 HistoricalAPIKeys 移除，避免 active∩historical 重复导致统计重复计数
	upstream.HistoricalAPIKeys = slices.DeleteFunc(upstream.HistoricalAPIKeys, func(k string) bool {
		return k == apiKey
	})

	// 清除内存中的失败记录
	cacheKey := failedKeyCacheKey(apiType, apiKey)
	delete(cm.failedKeysCache, cacheKey)

	log.Printf("[%s-Blacklist] Key %s 已恢复 (渠道: %s)", apiType, utils.MaskAPIKey(apiKey), upstream.Name)
	statelog.LogStateTransition(apiType+"-Blacklist", "key", utils.MaskAPIKey(apiKey), "disabled", "active", "manual_restore", "channel="+upstream.Name)

	return cm.saveConfigLocked(cm.config)
}

// SuspendKey 手动暂停指定 API Key（设置 Enabled=false，不移出 APIKeys 列表）
// apiType: Messages/Responses/Gemini/Chat/Images/Vectors
// channelIndex: 渠道在 upstream slice 中的索引
func (cm *ConfigManager) SuspendKey(apiType string, channelIndex int, apiKey string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	upstreams := cm.getUpstreamSliceLocked(apiType)
	if upstreams == nil || channelIndex < 0 || channelIndex >= len(*upstreams) {
		return fmt.Errorf("无效的渠道索引: %s[%d]", apiType, channelIndex)
	}

	upstream := &(*upstreams)[channelIndex]

	// Key 必须在活跃列表中
	if !slices.Contains(upstream.APIKeys, apiKey) {
		return fmt.Errorf("key %s 不在活跃列表中", utils.MaskAPIKey(apiKey))
	}

	disabled := false
	upstream.APIKeyConfigs = normalizeAPIKeyConfigs(upstream.APIKeys, upstream.APIKeyConfigs)
	for i, cfg := range upstream.APIKeyConfigs {
		if cfg.Key == apiKey {
			upstream.APIKeyConfigs[i].Enabled = &disabled
			break
		}
	}

	log.Printf("[%s-Suspend] Key %s 已手动暂停 (渠道: %s)", apiType, utils.MaskAPIKey(apiKey), upstream.Name)
	statelog.LogStateTransition(apiType+"-Suspend", "key", utils.MaskAPIKey(apiKey), "active", "suspended", "manual_suspend", "channel="+upstream.Name)

	return cm.saveConfigLocked(cm.config)
}

// ResumeKey 手动恢复指定 API Key（移除 Enabled 限制，恢复为活跃状态）
// apiType: Messages/Responses/Gemini/Chat/Images/Vectors
// channelIndex: 渠道在 upstream slice 中的索引
func (cm *ConfigManager) ResumeKey(apiType string, channelIndex int, apiKey string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	upstreams := cm.getUpstreamSliceLocked(apiType)
	if upstreams == nil || channelIndex < 0 || channelIndex >= len(*upstreams) {
		return fmt.Errorf("无效的渠道索引: %s[%d]", apiType, channelIndex)
	}

	upstream := &(*upstreams)[channelIndex]

	// Key 必须在活跃列表中
	if !slices.Contains(upstream.APIKeys, apiKey) {
		return fmt.Errorf("key %s 不在活跃列表中", utils.MaskAPIKey(apiKey))
	}

	upstream.APIKeyConfigs = normalizeAPIKeyConfigs(upstream.APIKeys, upstream.APIKeyConfigs)
	found := false
	for i, cfg := range upstream.APIKeyConfigs {
		if cfg.Key == apiKey {
			// 移除 Enabled 限制，恢复默认（活跃）行为
			upstream.APIKeyConfigs[i].Enabled = nil
			// 如果移除 Enabled 后配置无其他有效字段，清除整个配置项
			if !IsAPIKeyConfigEffective(upstream.APIKeyConfigs[i]) {
				upstream.APIKeyConfigs = append(upstream.APIKeyConfigs[:i], upstream.APIKeyConfigs[i+1:]...)
			}
			found = true
			break
		}
	}
	if !found {
		// Key 没有配置项，也无需恢复——已经是活跃状态
	}

	log.Printf("[%s-Suspend] Key %s 已手动恢复 (渠道: %s)", apiType, utils.MaskAPIKey(apiKey), upstream.Name)
	statelog.LogStateTransition(apiType+"-Suspend", "key", utils.MaskAPIKey(apiKey), "suspended", "active", "manual_resume", "channel="+upstream.Name)

	return cm.saveConfigLocked(cm.config)
}

// RestoreAllKeys 恢复指定渠道所有被拉黑的 Key（持久化）
// 返回恢复的 Key 数量
func (cm *ConfigManager) RestoreAllKeys(apiType string, channelIndex int) (int, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	upstreams := cm.getUpstreamSliceLocked(apiType)
	if upstreams == nil || channelIndex < 0 || channelIndex >= len(*upstreams) {
		return 0, fmt.Errorf("无效的渠道索引: %s[%d]", apiType, channelIndex)
	}

	upstream := &(*upstreams)[channelIndex]
	restoredCount := len(upstream.DisabledAPIKeys)
	if restoredCount == 0 {
		return 0, nil
	}

	// 将所有被拉黑的 Key 移回活跃列表
	savedConfigs := make(map[string]*APIKeyConfig, restoredCount)
	for _, dk := range upstream.DisabledAPIKeys {
		if !slices.Contains(upstream.APIKeys, dk.Key) {
			upstream.APIKeys = append(upstream.APIKeys, dk.Key)
		}
		if dk.Config != nil {
			copyCfg := *dk.Config
			savedConfigs[dk.Key] = &copyCfg
		}
		// 从 HistoricalAPIKeys 移除，避免 active∩historical 重复
		upstream.HistoricalAPIKeys = slices.DeleteFunc(upstream.HistoricalAPIKeys, func(k string) bool {
			return k == dk.Key
		})
		// 清除内存中的失败记录
		cacheKey := failedKeyCacheKey(apiType, dk.Key)
		delete(cm.failedKeysCache, cacheKey)
	}

	for _, cfg := range savedConfigs {
		upstream.APIKeyConfigs = restoreAPIKeyConfig(upstream.APIKeyConfigs, cfg.Key, cfg)
	}
	upstream.APIKeyConfigs = normalizeAPIKeyConfigs(upstream.APIKeys, upstream.APIKeyConfigs)

	log.Printf("[%s-Blacklist] 渠道 [%d] %s 的 %d 个 Key 已全部恢复", apiType, channelIndex, upstream.Name, restoredCount)
	upstream.DisabledAPIKeys = nil

	return restoredCount, cm.saveConfigLocked(cm.config)
}

// RestoreDisabledKeys 恢复指定渠道中命中的被拉黑 Key，并返回实际恢复的 key 列表。
func (cm *ConfigManager) RestoreDisabledKeys(apiType string, channelIndex int, keys []string) ([]string, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	upstreams := cm.getUpstreamSliceLocked(apiType)
	if upstreams == nil || channelIndex < 0 || channelIndex >= len(*upstreams) {
		return nil, fmt.Errorf("无效的渠道索引: %s[%d]", apiType, channelIndex)
	}
	if len(keys) == 0 {
		return nil, nil
	}

	keySet := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		if key == "" {
			continue
		}
		keySet[key] = struct{}{}
	}
	if len(keySet) == 0 {
		return nil, nil
	}

	upstream := &(*upstreams)[channelIndex]
	restored := make([]string, 0, len(keySet))
	newDisabled := make([]DisabledKeyInfo, 0, len(upstream.DisabledAPIKeys))
	savedConfigs := make(map[string]*APIKeyConfig)
	for _, dk := range upstream.DisabledAPIKeys {
		if _, ok := keySet[dk.Key]; !ok {
			newDisabled = append(newDisabled, dk)
			continue
		}
		if !slices.Contains(upstream.APIKeys, dk.Key) {
			upstream.APIKeys = append(upstream.APIKeys, dk.Key)
		}
		upstream.HistoricalAPIKeys = slices.DeleteFunc(upstream.HistoricalAPIKeys, func(k string) bool {
			return k == dk.Key
		})
		delete(cm.failedKeysCache, failedKeyCacheKey(apiType, dk.Key))
		if dk.Config != nil {
			copyCfg := *dk.Config
			savedConfigs[dk.Key] = &copyCfg
		}
		restored = append(restored, dk.Key)
	}

	if len(restored) == 0 {
		return nil, nil
	}

	upstream.DisabledAPIKeys = newDisabled
	upstream.APIKeyConfigs = normalizeAPIKeyConfigs(upstream.APIKeys, upstream.APIKeyConfigs)
	for key, cfg := range savedConfigs {
		upstream.APIKeyConfigs = restoreAPIKeyConfig(upstream.APIKeyConfigs, key, cfg)
	}
	log.Printf("[%s-Blacklist] 渠道 [%d] %s 自动恢复了 %d 个 Key", apiType, channelIndex, upstream.Name, len(restored))
	if err := cm.saveConfigLocked(cm.config); err != nil {
		return nil, err
	}
	return restored, nil
}

// DisableKeyModel 将 (apiKey, model) 组合加入限制列表（持久化，默认 1 小时后自动恢复）。
// 仅限制该 Key 对该模型的路由，不影响该 Key 的其他模型，也不从 APIKeys 中移除。
func (cm *ConfigManager) DisableKeyModel(apiType string, channelIndex int, apiKey, model, reason, message string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	upstreams := cm.getUpstreamSliceLocked(apiType)
	if upstreams == nil || channelIndex < 0 || channelIndex >= len(*upstreams) {
		return fmt.Errorf("无效的渠道索引: %s[%d]", apiType, channelIndex)
	}
	apiKey = strings.TrimSpace(apiKey)
	model = strings.TrimSpace(model)
	if apiKey == "" || model == "" {
		return nil
	}

	upstream := &(*upstreams)[channelIndex]
	now := time.Now()
	recoverAt := now.Add(time.Hour).Format(time.RFC3339)

	// 去重：限制仍生效时不刷新、不写盘，避免并发失败造成热路径磁盘抖动。
	if upstream.IsKeyModelDisabledNow(apiKey, model, now) {
		log.Printf("[%s-KeyModel] (Key %s, 模型 %s) 已处于限制期，跳过重复写入 (渠道: %s)",
			apiType, utils.MaskAPIKey(apiKey), model, upstream.Name)
		return nil
	}

	// 同 (key, model) 组合已过期则复用原记录并刷新恢复时间。
	for i := range upstream.DisabledKeyModels {
		dm := &upstream.DisabledKeyModels[i]
		if dm.Key == apiKey && strings.EqualFold(strings.TrimSpace(dm.Model), model) {
			dm.Reason = reason
			dm.Message = message
			dm.DisabledAt = now.Format(time.RFC3339)
			dm.RecoverAt = recoverAt
			log.Printf("[%s-KeyModel] 刷新 (Key %s, 模型 %s) 限制 (原因: %s, 渠道: %s)",
				apiType, utils.MaskAPIKey(apiKey), model, reason, upstream.Name)
			return cm.saveConfigLocked(cm.config)
		}
	}

	upstream.DisabledKeyModels = append(upstream.DisabledKeyModels, DisabledKeyModelInfo{
		Key:        apiKey,
		Model:      model,
		Reason:     reason,
		Message:    message,
		DisabledAt: now.Format(time.RFC3339),
		RecoverAt:  recoverAt,
	})
	log.Printf("[%s-KeyModel] (Key %s, 模型 %s) 已限制 (原因: %s, 渠道: %s, 恢复时间: %s)",
		apiType, utils.MaskAPIKey(apiKey), model, reason, upstream.Name, recoverAt)
	statelog.LogStateTransition(apiType+"-KeyModel", "key_model", utils.MaskAPIKey(apiKey)+"|"+model, "active", "disabled", reason, "channel="+upstream.Name)

	return cm.saveConfigLocked(cm.config)
}

// IsKeyModelDisabled 判断指定渠道下 (apiKey, model) 组合是否处于限制期内。
func (cm *ConfigManager) IsKeyModelDisabled(apiType string, channelIndex int, apiKey, model string) bool {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	upstreams := cm.getUpstreamSliceLocked(apiType)
	if upstreams == nil || channelIndex < 0 || channelIndex >= len(*upstreams) {
		return false
	}
	return (*upstreams)[channelIndex].IsKeyModelDisabledNow(apiKey, model, time.Now())
}

// RestoreKeyModel 手动移除指定渠道下 (apiKey, model) 组合的限制（持久化）。
func (cm *ConfigManager) RestoreKeyModel(apiType string, channelIndex int, apiKey, model string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	upstreams := cm.getUpstreamSliceLocked(apiType)
	if upstreams == nil || channelIndex < 0 || channelIndex >= len(*upstreams) {
		return fmt.Errorf("无效的渠道索引: %s[%d]", apiType, channelIndex)
	}
	upstream := &(*upstreams)[channelIndex]

	newList := make([]DisabledKeyModelInfo, 0, len(upstream.DisabledKeyModels))
	removed := false
	for _, dm := range upstream.DisabledKeyModels {
		if dm.Key == apiKey && strings.EqualFold(strings.TrimSpace(dm.Model), strings.TrimSpace(model)) {
			removed = true
			continue
		}
		newList = append(newList, dm)
	}
	if !removed {
		return fmt.Errorf("(Key %s, 模型 %s) 不在限制列表中", utils.MaskAPIKey(apiKey), model)
	}

	upstream.DisabledKeyModels = newList
	log.Printf("[%s-KeyModel] (Key %s, 模型 %s) 限制已移除 (渠道: %s)", apiType, utils.MaskAPIKey(apiKey), model, upstream.Name)
	statelog.LogStateTransition(apiType+"-KeyModel", "key_model", utils.MaskAPIKey(apiKey)+"|"+model, "disabled", "active", "manual_restore", "channel="+upstream.Name)

	return cm.saveConfigLocked(cm.config)
}

// RestoreExpiredKeyModels 清理指定渠道下所有 RecoverAt 已到期的 (Key,模型) 限制条目。
// 返回被恢复的组合描述（"maskedKey|model"），供定时恢复调度器汇总日志。
func (cm *ConfigManager) RestoreExpiredKeyModels(apiType string, channelIndex int, now time.Time) ([]string, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	upstreams := cm.getUpstreamSliceLocked(apiType)
	if upstreams == nil || channelIndex < 0 || channelIndex >= len(*upstreams) {
		return nil, fmt.Errorf("无效的渠道索引: %s[%d]", apiType, channelIndex)
	}
	upstream := &(*upstreams)[channelIndex]
	if len(upstream.DisabledKeyModels) == 0 {
		return nil, nil
	}

	newList := make([]DisabledKeyModelInfo, 0, len(upstream.DisabledKeyModels))
	restored := make([]string, 0)
	for _, dm := range upstream.DisabledKeyModels {
		expired := false
		if dm.RecoverAt != "" {
			if recoverAt, err := time.Parse(time.RFC3339, dm.RecoverAt); err == nil {
				expired = !now.Before(recoverAt)
			}
		}
		if expired {
			restored = append(restored, utils.MaskAPIKey(dm.Key)+"|"+dm.Model)
			continue
		}
		newList = append(newList, dm)
	}
	if len(restored) == 0 {
		return nil, nil
	}

	upstream.DisabledKeyModels = newList
	log.Printf("[%s-KeyModel] 渠道 [%d] %s 自动恢复了 %d 个 (Key,模型) 限制", apiType, channelIndex, upstream.Name, len(restored))
	if err := cm.saveConfigLocked(cm.config); err != nil {
		return nil, err
	}
	return restored, nil
}

// getUpstreamSliceLocked 根据 apiType 获取对应的 upstream slice 指针（调用方需持有锁）
func (cm *ConfigManager) getUpstreamSliceLocked(apiType string) *[]UpstreamConfig {
	switch apiType {
	case "Messages":
		return &cm.config.Upstream
	case "Responses":
		return &cm.config.ResponsesUpstream
	case "Gemini":
		return &cm.config.GeminiUpstream
	case "Chat":
		return &cm.config.ChatUpstream
	case "Images":
		return &cm.config.ImagesUpstream
	case "Vectors":
		return &cm.config.VectorsUpstream
	default:
		return nil
	}
}
