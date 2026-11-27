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
	ChannelUID            string                             `json:"channelUid,omitempty"` // 观测身份，重排/改名时保持不变
	BaseURL               string                             `json:"baseUrl"`
	BaseURLs              []string                           `json:"baseUrls,omitempty"` // 多 BaseURL 支持（failover 模式）
	APIKeys               []string                           `json:"apiKeys"`
	SiteAccountUID        string                             `json:"siteAccountUid,omitempty"` // 托管归属独立于用户可编辑的渠道名称
	SiteGroup             string                             `json:"siteGroup,omitempty"`
	GroupMultiplier       *float64                           `json:"groupMultiplier,omitempty"`     // 渠道计费倍率：nil 按 1，0 表示免费
	APIKeyConfigs         []APIKeyConfig                     `json:"apiKeyConfigs,omitempty"`       // API Key 附加配置（限速、权重、配额组等），通过 Key 关联 APIKeys
	HistoricalAPIKeys     []string                           `json:"historicalApiKeys,omitempty"`   // 历史 API Key（用于统计聚合，换 Key 后保留旧 Key 的统计数据）
	DisabledAPIKeys       []DisabledKeyInfo                  `json:"disabledApiKeys,omitempty"`     // 被拉黑的 API Key（持久化，需手动恢复）
	DisabledKeyModels     []DisabledKeyModelInfo             `json:"disabledKeyModels,omitempty"`   // 被限制的 (Key,模型) 组合（持久化，定时自动恢复）
	DisabledGroupModels   []DisabledGroupModelInfo           `json:"disabledGroupModels,omitempty"` // 人工禁用的 (配额组,模型) 组合（持久化，需手动恢复）
	ServiceType           string                             `json:"serviceType"`                   // gemini, openai, claude
	AuthHeader            string                             `json:"authHeader,omitempty"`          // 认证头覆盖：auto(空)/bearer/x-api-key
	Name                  string                             `json:"name,omitempty"`
	Description           string                             `json:"description,omitempty"`
	Website               string                             `json:"website,omitempty"`
	InsecureSkipVerify    bool                               `json:"insecureSkipVerify,omitempty"`
	ModelMapping          map[string]string                  `json:"modelMapping,omitempty"`
	SubagentModel         string                             `json:"subagentModel,omitempty"`     // 子代理请求模型大类或具体模型名，为空沿用请求模型
	ModelCapabilities     map[string]UpstreamModelCapability `json:"modelCapabilities,omitempty"` // 实际模型能力覆盖，key 支持模型通配符
	EmbeddingCapabilities map[string]EmbeddingCapability     `json:"embeddingCapabilities,omitempty"`
	DefaultCapability     UpstreamModelCapability            `json:"defaultCapability,omitempty"`   // 渠道默认实际模型能力
	AllowUnknownContext   bool                               `json:"allowUnknownContext,omitempty"` // 大上下文请求是否允许落到未知能力渠道
	ReasoningMapping      map[string]string                  `json:"reasoningMapping,omitempty"`
	ReasoningParamStyle   string                             `json:"reasoningParamStyle,omitempty"`
	TextVerbosity         string                             `json:"textVerbosity,omitempty"`
	FastMode              bool                               `json:"fastMode,omitempty"`
	// OpenAI Chat 上游配置：启用后将非标准 Chat role 改写为 user（默认 false）
	NormalizeNonstandardChatRoles bool `json:"normalizeNonstandardChatRoles,omitempty"`
	// Codex 工具兼容开关（默认 false）。
	// 透传分支中将 Codex 原生工具转换为 OpenAI function 格式（默认 false）。
	CodexNativeToolPassthrough bool  `json:"codexNativeToolPassthrough,omitempty"`
	ResponsesReasoningFold     bool  `json:"responsesReasoningFold,omitempty"` // 显式启用 Responses 推理续轮折叠（默认 false）
	CodexToolCompat            *bool `json:"codexToolCompat,omitempty"`
	// Deprecated: 使用 codexToolCompat；保留旧字段仅用于配置读取和旧前端写入兼容。
	StripCodexClientTools bool `json:"stripCodexClientTools,omitempty"`
	// Responses/Chat 工具兼容：移除 image_generation 工具（兼容未开通图片生成权限的上游）
	StripImageGenerationTool bool `json:"stripImageGenerationTool,omitempty"`
	// Images 响应兼容：当客户端请求 b64_json 而上游只返回 URL 时，下载并转换为 b64_json（默认 false）
	ConvertImageURLToB64JSON bool `json:"convertImageUrlToB64Json,omitempty"`
	// 多渠道调度相关字段
	Priority       int        `json:"priority"`                 // 渠道优先级（数字越小优先级越高，默认按索引）
	Status         string     `json:"status"`                   // 渠道状态：active（正常）, suspended（暂停）, disabled（备用池）
	PromotionUntil *time.Time `json:"promotionUntil,omitempty"` // 促销期截止时间，在此期间内优先使用此渠道（忽略trace亲和）
	LowQuality     bool       `json:"lowQuality,omitempty"`     // 低质量渠道标记：启用后强制本地估算 token，偏差>5%时使用本地值
	// 自动拉黑开关
	AutoBlacklistBalance   *bool `json:"autoBlacklistBalance,omitempty"`   // 余额不足时自动拉黑 Key（默认 true）
	CircuitBreakerDisabled bool  `json:"circuitBreakerDisabled,omitempty"` // 禁用此渠道的自动熔断；不影响鉴权/余额拉黑与限速 cooldown
	// Claude Messages metadata.user_id 规范化开关
	NormalizeMetadataUserID *bool `json:"normalizeMetadataUserId,omitempty"` // 规范化 metadata.user_id（默认 false）
	// Messages 渠道级移除计费头：转发前从 system 数组移除 cch=xxx; 计费参数（默认关闭）
	StripBillingHeader *bool `json:"stripBillingHeader,omitempty"`
	// Claude 协议空文本兼容
	StripEmptyTextBlocks bool `json:"stripEmptyTextBlocks,omitempty"` // 转发前移除裸空 text content block（兼容严格校验的第三方 Claude 上游）
	// Claude 协议 system 角色兼容
	NormalizeSystemRoleToTopLevel bool `json:"normalizeSystemRoleToTopLevel,omitempty"` // 将 messages 中的 system 角色抽取回顶层 system 字段（针对 Opus 4.8 / Fable 5 等新客户端将 system 作为消息 role 发送，兼容仅支持 user/assistant role 的旧 Claude 上游）
	// Gemini 特定配置
	InjectDummyThoughtSignature bool `json:"injectDummyThoughtSignature,omitempty"` // 给空 thought_signature 注入 dummy 值（兼容 x666.me 等要求必须有该字段的 API）
	StripThoughtSignature       bool `json:"stripThoughtSignature,omitempty"`       // 移除 thought_signature 字段（兼容旧版 Gemini API）
	// Claude 协议 thinking 回传配置
	PassbackReasoningContent bool `json:"passbackReasoningContent,omitempty"` // 将 thinking 块转为 reasoning_content 回传（兼容 mimo 等要求 OpenAI 风格 reasoning_content 的 Claude 协议上游）
	PassbackThinkingBlocks   bool `json:"passbackThinkingBlocks,omitempty"`   // 将真实 reasoning_content 回传为 content[].thinking（兼容 DeepSeek/GLM 等严格 Claude thinking 上游）
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
	RateLimitRPM             int   `json:"rateLimitRpm,omitempty"`             // 每个 Key 的默认请求数上限（0=不限）
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
}

// APIKeyConfig 描述单个 API Key 的附加调度配置。
type APIKeyConfig struct {
	Key                      string   `json:"key"`
	Name                     string   `json:"name,omitempty"`
	Enabled                  *bool    `json:"enabled,omitempty"`
	QuotaGroup               string   `json:"quotaGroup,omitempty"`
	SourceAccountUID         string   `json:"sourceAccountUid,omitempty"`
	SourceRemoteTokenID      int      `json:"sourceRemoteTokenId,omitempty"`
	RemoteStatus             string   `json:"remoteStatus,omitempty"` // 远端明确停用/过期/分组缺失；空表示未发现限制
	RateLimitRPM             int      `json:"rateLimitRpm,omitempty"`
	RateLimitWindowMinutes   int      `json:"rateLimitWindowMinutes,omitempty"` // 滑动窗口时长（秒；JSON 字段名保留为兼容旧配置）
	RateLimitMaxConcurrent   int      `json:"rateLimitMaxConcurrent,omitempty"`
	RateLimitAutoFromHeaders *bool    `json:"rateLimitAutoFromHeaders,omitempty"`
	Weight                   int      `json:"weight,omitempty"`
	Models                   []string `json:"models,omitempty"`
}

// DisabledKeyInfo 被拉黑的 API Key 信息
type DisabledKeyInfo struct {
	Key        string        `json:"key"`
	Reason     string        `json:"reason"`              // "authentication_error" / "permission_error" / "insufficient_balance"
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
	Reason     string `json:"reason"`     // "model_not_found"
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
	if cfg.RateLimitRPM > 0 || cfg.RateLimitWindowMinutes > 0 || cfg.RateLimitMaxConcurrent > 0 {
		return true
	}
	if cfg.RateLimitAutoFromHeaders != nil {
		return true
	}
	if cfg.Weight > 0 || len(cfg.Models) > 0 || cfg.RemoteStatus != "" {
		return true
	}
	return false
}

// IsAPIKeyEnabled 判断 Key 是否允许参与调度，未配置 Enabled 时默认启用。
func (u *UpstreamConfig) IsAPIKeyEnabled(apiKey string) bool {
	for _, cfg := range u.APIKeyConfigs {
		if cfg.Key == apiKey {
			return cfg.Enabled == nil || *cfg.Enabled
		}
	}
	return true
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

// IsNormalizeMetadataUserIDEnabled 检查 metadata.user_id 规范化是否启用（默认 false）
func (u *UpstreamConfig) IsNormalizeMetadataUserIDEnabled() bool {
	if u.NormalizeMetadataUserID == nil {
		return false
	}
	return *u.NormalizeMetadataUserID
}

// IsStripBillingHeaderEnabled 检查是否移除 cch= 计费参数（默认 false）。
func (u *UpstreamConfig) IsStripBillingHeaderEnabled() bool {
	if u.StripBillingHeader == nil {
		return false
	}
	return *u.StripBillingHeader
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

// IsStripImageGenerationToolEnabled 检查是否移除 image_generation 工具（默认 false）。
func (u *UpstreamConfig) IsStripImageGenerationToolEnabled() bool {
	return u.StripImageGenerationTool
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
	ExpectedSiteSnapshot          *UpstreamConfig                    `json:"-"` // 后台同步的乐观并发保护，不由外部 API 赋值
	SiteAccountUID                *string                            `json:"siteAccountUid"`
	SiteGroup                     *string                            `json:"siteGroup"`
	GroupMultiplier               OptionalMultiplier                 `json:"groupMultiplier"`
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
	SubagentModel                 *string                            `json:"subagentModel"`
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
	ResponsesReasoningFold        *bool                              `json:"responsesReasoningFold"`
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
	CircuitBreakerDisabled  *bool      `json:"circuitBreakerDisabled"`
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
	ModelFallbackPolicies map[string]ModelFallbackPolicy `json:"modelFallbackPolicies,omitempty"`
	Upstream              []UpstreamConfig               `json:"upstream"`
	CurrentUpstream       int                            `json:"currentUpstream,omitempty"` // 已废弃：旧格式兼容用

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

	// 各文本协议的独立子代理默认模型与渠道序列。
	SubagentRouting map[string]SubagentRoutingPolicy `json:"subagentRouting,omitempty"`

	// Claude thinking 回填缓存配置
	ThinkingCache ThinkingCacheConfig `json:"thinkingCache,omitempty"`

	// 熔断器运行时配置（可选，nil 使用环境变量或代码默认值）
	CircuitBreaker *CircuitBreakerConfig `json:"circuitBreaker,omitempty"`
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
	keyOperationObserver  func(KeyOperationEvent)
	// selfWriteSum 由 mu 保护，记录最后一次自身保存的文件摘要。
	selfWriteSum string
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
	if cm.config.ModelFallbackPolicies != nil {
		cloned.ModelFallbackPolicies = make(map[string]ModelFallbackPolicy, len(cm.config.ModelFallbackPolicies))
		for k, v := range cm.config.ModelFallbackPolicies {
			v.Models = append([]string(nil), v.Models...)
			cloned.ModelFallbackPolicies[k] = v
		}
	}
	if cm.config.SubagentRouting != nil {
		cloned.SubagentRouting = make(map[string]SubagentRoutingPolicy, len(cm.config.SubagentRouting))
		for k, v := range cm.config.SubagentRouting {
			v.Channels = append([]SubagentRoutingChannel(nil), v.Channels...)
			cloned.SubagentRouting[k] = v
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

	return cloned
}

// GetNextAPIKey 获取下一个 API 密钥（纯 failover 模式）
// apiType: 接口类型（Messages/Responses/Gemini），用于日志标签前缀
func (cm *ConfigManager) GetNextAPIKey(upstream *UpstreamConfig, failedKeys map[string]bool, apiType string) (string, error) {
	filtered := *upstream
	filtered.APIKeys = make([]string, 0, len(upstream.APIKeys))
	for _, key := range upstream.APIKeys {
		if upstream.IsAPIKeyEnabled(key) && upstream.IsAPIKeyRemoteAvailable(key) {
			filtered.APIKeys = append(filtered.APIKeys, key)
		}
	}
	upstream = &filtered
	if len(upstream.APIKeys) == 0 {
		return "", fmt.Errorf("上游 %s 没有可用的API密钥", upstream.Name)
	}

	// 单 Key 直接返回
	if len(upstream.APIKeys) == 1 {
		return upstream.APIKeys[0], nil
	}

	// 筛选可用密钥：排除临时失败密钥和内存中的失败密钥
	availableKeys := []string{}
	for _, key := range upstream.APIKeys {
		if !failedKeys[key] && (upstream.CircuitBreakerDisabled || !cm.isKeyFailed(key, apiType)) {
			availableKeys = append(availableKeys, key)
		}
	}

	if len(availableKeys) == 0 {
		// 如果所有密钥都失效，尝试选择失败时间最早的密钥（恢复尝试）
		var oldestFailedKey string
		oldestTime := time.Now()

		cm.mu.RLock()
		for _, key := range upstream.APIKeys {
			if !failedKeys[key] { // 排除本次请求已经尝试过的密钥
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
	upstream.DisabledKeyModels = slices.DeleteFunc(upstream.DisabledKeyModels, func(dm DisabledKeyModelInfo) bool {
		return dm.Key == apiKey
	})
	upstream.DisabledGroupModels = slices.DeleteFunc(upstream.DisabledGroupModels, func(dm DisabledGroupModelInfo) bool {
		return dm.QuotaGroup == "" && dm.Key == apiKey
	})
}

// removeDisabledKeyEntryLocked 从拉黑列表移除指定 Key 及其模型限制与内存失败记录。
// 用于直接删除已拉黑（不在活跃列表中）的 Key。调用方需持有锁。
func (cm *ConfigManager) removeDisabledKeyEntryLocked(upstream *UpstreamConfig, apiType, apiKey string) bool {
	for i, dk := range upstream.DisabledAPIKeys {
		if dk.Key == apiKey {
			upstream.DisabledAPIKeys = append(upstream.DisabledAPIKeys[:i], upstream.DisabledAPIKeys[i+1:]...)
			removeDisabledKeyModelsForKey(upstream, apiKey)
			delete(cm.failedKeysCache, failedKeyCacheKey(apiType, apiKey))
			return true
		}
	}
	return false
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

// ============== API Key 拉黑相关方法 ==============

// BlacklistKey 将指定 Key 从活跃列表移到拉黑列表（持久化）
// apiType: Messages/Responses/Gemini/Chat，用于定位 upstream slice
// channelIndex: 渠道在 upstream slice 中的索引
func (cm *ConfigManager) BlacklistKey(apiType string, channelIndex int, apiKey string, reason string, message string) (operationErr error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	defer cm.observeKeyOperationLocked(apiType, channelIndex, apiKey, "blacklist", "automatic", reason)(&operationErr)

	upstreams := cm.getUpstreamSliceLocked(apiType)
	if upstreams == nil || channelIndex < 0 || channelIndex >= len(*upstreams) {
		return fmt.Errorf("无效的渠道索引: %s[%d]", apiType, channelIndex)
	}

	upstream := &(*upstreams)[channelIndex]

	// 检查 key 是否在活跃列表中
	keyIdx := -1
	for i, k := range upstream.APIKeys {
		if k == apiKey {
			keyIdx = i
			break
		}
	}
	if keyIdx == -1 {
		return nil // key 不在活跃列表，可能已被拉黑，忽略
	}

	// 从 APIKeys 中移除
	upstream.APIKeys = append(upstream.APIKeys[:keyIdx], upstream.APIKeys[keyIdx+1:]...)
	upstream.APIKeyConfigs = normalizeAPIKeyConfigs(upstream.APIKeys, upstream.APIKeyConfigs)

	// 添加到 DisabledAPIKeys
	disabledAt := time.Now().Format(time.RFC3339)
	recoverAt := ""
	if IsAutoRecoverableDisabledReason(reason) {
		recoverAt = time.Now().Add(time.Hour).Format(time.RFC3339)
	}
	var disabledCfg *APIKeyConfig
	for _, cfg := range upstream.APIKeyConfigs {
		if cfg.Key == apiKey {
			copyCfg := cloneAPIKeyConfig(cfg)
			disabledCfg = &copyCfg
			break
		}
	}
	upstream.DisabledAPIKeys = append(upstream.DisabledAPIKeys, DisabledKeyInfo{
		Key:        apiKey,
		Reason:     reason,
		Message:    message,
		DisabledAt: disabledAt,
		RecoverAt:  recoverAt,
		Config:     disabledCfg,
	})

	// 同时添加到 HistoricalAPIKeys（保留统计数据）
	if !slices.Contains(upstream.HistoricalAPIKeys, apiKey) {
		upstream.HistoricalAPIKeys = append(upstream.HistoricalAPIKeys, apiKey)
	}

	log.Printf("[%s-Blacklist] Key %s 已被拉黑 (原因: %s, 渠道: %s, 剩余Key: %d)",
		apiType, utils.MaskAPIKey(apiKey), reason, upstream.Name, len(upstream.APIKeys))
	statelog.LogStateTransition(apiType+"-Blacklist", "key", utils.MaskAPIKey(apiKey), "active", "disabled", reason, "channel="+upstream.Name)

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
func (cm *ConfigManager) RestoreKey(apiType string, channelIndex int, apiKey string) (operationErr error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	defer cm.observeKeyOperationLocked(apiType, channelIndex, apiKey, "restore", "manual", "")(&operationErr)

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
		return fmt.Errorf("Key %s 不在拉黑列表中", utils.MaskAPIKey(apiKey))
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
func (cm *ConfigManager) SuspendKey(apiType string, channelIndex int, apiKey string) (operationErr error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	defer cm.observeKeyOperationLocked(apiType, channelIndex, apiKey, "pause", "manual", "")(&operationErr)

	upstreams := cm.getUpstreamSliceLocked(apiType)
	if upstreams == nil || channelIndex < 0 || channelIndex >= len(*upstreams) {
		return fmt.Errorf("无效的渠道索引: %s[%d]", apiType, channelIndex)
	}

	upstream := &(*upstreams)[channelIndex]
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
func (cm *ConfigManager) ResumeKey(apiType string, channelIndex int, apiKey string) (operationErr error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()
	defer cm.observeKeyOperationLocked(apiType, channelIndex, apiKey, "resume", "manual", "")(&operationErr)

	upstreams := cm.getUpstreamSliceLocked(apiType)
	if upstreams == nil || channelIndex < 0 || channelIndex >= len(*upstreams) {
		return fmt.Errorf("无效的渠道索引: %s[%d]", apiType, channelIndex)
	}

	upstream := &(*upstreams)[channelIndex]
	if !slices.Contains(upstream.APIKeys, apiKey) {
		return fmt.Errorf("key %s 不在活跃列表中", utils.MaskAPIKey(apiKey))
	}

	upstream.APIKeyConfigs = normalizeAPIKeyConfigs(upstream.APIKeys, upstream.APIKeyConfigs)
	for i, cfg := range upstream.APIKeyConfigs {
		if cfg.Key == apiKey {
			upstream.APIKeyConfigs[i].Enabled = nil
			break
		}
	}

	log.Printf("[%s-Suspend] Key %s 已手动恢复 (渠道: %s)", apiType, utils.MaskAPIKey(apiKey), upstream.Name)
	statelog.LogStateTransition(apiType+"-Suspend", "key", utils.MaskAPIKey(apiKey), "suspended", "active", "manual_resume", "channel="+upstream.Name)
	return cm.saveConfigLocked(cm.config)
}

// RestoreAllKeys 恢复指定渠道所有不可用的 Key（被拉黑或手动暂停，持久化）。
// 返回实际恢复的唯一 Key 数量。
func (cm *ConfigManager) RestoreAllKeys(apiType string, channelIndex int) (restoredCount int, operationErr error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	upstreams := cm.getUpstreamSliceLocked(apiType)
	if upstreams == nil || channelIndex < 0 || channelIndex >= len(*upstreams) {
		return 0, fmt.Errorf("无效的渠道索引: %s[%d]", apiType, channelIndex)
	}

	upstream := &(*upstreams)[channelIndex]
	disabledCount := len(upstream.DisabledAPIKeys)
	observedKeys := make(map[string]bool)
	for _, key := range slices.Concat(upstream.APIKeys, disabledKeys(upstream.DisabledAPIKeys)) {
		if keyOperationState(upstream, key) != "active" && !observedKeys[key] {
			observedKeys[key] = true
			defer cm.observeKeyOperationLocked(apiType, channelIndex, key, "restore", "manual", "")(&operationErr)
		}
	}
	restoredKeys := make(map[string]struct{}, disabledCount)
	savedConfigs := make(map[string]*APIKeyConfig, disabledCount)
	for _, dk := range upstream.DisabledAPIKeys {
		restoredKeys[dk.Key] = struct{}{}
		if !slices.Contains(upstream.APIKeys, dk.Key) {
			upstream.APIKeys = append(upstream.APIKeys, dk.Key)
		}
		if dk.Config != nil {
			copyCfg := *dk.Config
			savedConfigs[dk.Key] = &copyCfg
		}
		upstream.HistoricalAPIKeys = slices.DeleteFunc(upstream.HistoricalAPIKeys, func(k string) bool {
			return k == dk.Key
		})
		delete(cm.failedKeysCache, failedKeyCacheKey(apiType, dk.Key))
	}

	if disabledCount > 0 {
		for _, cfg := range savedConfigs {
			upstream.APIKeyConfigs = restoreAPIKeyConfig(upstream.APIKeyConfigs, cfg.Key, cfg)
		}
		upstream.APIKeyConfigs = normalizeAPIKeyConfigs(upstream.APIKeys, upstream.APIKeyConfigs)
		upstream.DisabledAPIKeys = nil
	}

	activeKeys := make(map[string]struct{}, len(upstream.APIKeys))
	for _, key := range upstream.APIKeys {
		activeKeys[key] = struct{}{}
	}
	resumedCount := 0
	for i := range upstream.APIKeyConfigs {
		cfg := &upstream.APIKeyConfigs[i]
		if _, active := activeKeys[cfg.Key]; !active || cfg.Enabled == nil || *cfg.Enabled {
			continue
		}
		cfg.Enabled = nil
		restoredKeys[cfg.Key] = struct{}{}
		resumedCount++
	}

	if len(restoredKeys) == 0 {
		return 0, nil
	}
	if disabledCount > 0 {
		log.Printf("[%s-Blacklist] 渠道 [%d] %s 的 %d 个被拉黑 Key 已全部恢复", apiType, channelIndex, upstream.Name, disabledCount)
	}
	if resumedCount > 0 {
		log.Printf("[%s-Suspend] 渠道 [%d] %s 的 %d 个手动暂停 Key 已全部恢复", apiType, channelIndex, upstream.Name, resumedCount)
	}
	return len(restoredKeys), cm.saveConfigLocked(cm.config)
}

// RestoreDisabledKeys 恢复指定渠道中命中的被拉黑 Key，并返回实际恢复的 key 列表。
func (cm *ConfigManager) RestoreDisabledKeys(apiType string, channelIndex int, keys []string) (restoredKeys []string, operationErr error) {
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
	for key := range keySet {
		defer cm.observeKeyOperationLocked(apiType, channelIndex, key, "restore", "automatic", "")(&operationErr)
	}
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
// recoverAt 为上游明确给出的恢复时间（RFC3339）；为空、无效或已过期时沿用 1 小时兜底，
// 与 applyKeyBlacklistLocked 的 Key 级拉黑语义保持一致。
func (cm *ConfigManager) DisableKeyModel(apiType string, channelIndex int, apiKey, model, reason, message, recoverAt string) error {
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
	resolvedRecoverAt := now.Add(time.Hour).Format(time.RFC3339)
	if explicit, err := time.Parse(time.RFC3339, strings.TrimSpace(recoverAt)); err == nil && explicit.After(now) {
		resolvedRecoverAt = explicit.Format(time.RFC3339)
	}
	recoverAt = resolvedRecoverAt

	// 去重：同 (key, model) 组合已存在则刷新时间戳与恢复时间
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
