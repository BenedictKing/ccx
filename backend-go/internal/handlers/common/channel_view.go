package common

import (
	"github.com/BenedictKing/ccx/internal/config"
	"github.com/gin-gonic/gin"
)

func BuildChannelView(up config.UpstreamConfig, index int) gin.H {
	status := config.GetChannelStatus(&up)
	priority := config.GetChannelPriority(&up, index)
	return gin.H{
		"index":                         index,
		"name":                          up.Name,
		"serviceType":                   up.ServiceType,
		"authHeader":                    up.AuthHeader,
		"baseUrl":                       up.BaseURL,
		"baseUrls":                      up.BaseURLs,
		"apiKeys":                       up.APIKeys,
		"apiKeyConfigs":                 config.NormalizeAPIKeyConfigsForView(up),
		"description":                   up.Description,
		"website":                       up.Website,
		"insecureSkipVerify":            up.InsecureSkipVerify,
		"modelMapping":                  up.ModelMapping,
		"modelCapabilities":             up.ModelCapabilities,
		"embeddingCapabilities":         up.EmbeddingCapabilities,
		"defaultCapability":             up.DefaultCapability,
		"allowUnknownContext":           up.AllowUnknownContext,
		"reasoningMapping":              up.ReasoningMapping,
		"reasoningParamStyle":           up.ReasoningParamStyle,
		"textVerbosity":                 up.TextVerbosity,
		"fastMode":                      up.FastMode,
		"normalizeNonstandardChatRoles": up.NormalizeNonstandardChatRoles,
		"stripCodexClientTools":         up.IsCodexToolCompatEnabled(),
		"latency":                       nil,
		"status":                        status,
		"adminState":                    config.GetChannelAdminState(&up),
		"effectiveState":                config.GetChannelEffectiveState(&up),
		"runtimeState":                  config.GetChannelRuntimeState(&up),
		"priority":                      priority,
		"promotionUntil":                up.PromotionUntil,
		"lowQuality":                    up.LowQuality,
		"customHeaders":                 up.CustomHeaders,
		"proxyUrl":                      up.ProxyURL,
		"supportedModels":               up.SupportedModels,
		"routePrefix":                   up.RoutePrefix,
		"disabledApiKeys":               up.DisabledAPIKeys,
		"autoBlacklistBalance":          up.IsAutoBlacklistBalanceEnabled(),
		"normalizeMetadataUserId":       up.IsNormalizeMetadataUserIDEnabled(),
		"stripBillingHeader":            up.IsStripBillingHeaderEnabled(),
		"codexNativeToolPassthrough":    up.CodexNativeToolPassthrough,
		"codexToolCompat":               up.IsCodexToolCompatEnabled(),
		"stripImageGenerationTool":      up.IsStripImageGenerationToolEnabled(),
		"convertImageUrlToB64Json":      up.ConvertImageURLToB64JSON,
		"noVision":                      up.NoVision,
		"noVisionModels":                up.NoVisionModels,
		"visionFallbackModel":           up.VisionFallbackModel,
		"historicalImageTurnLimit":      up.HistoricalImageTurnLimit,
		// Claude 协议兼容开关
		"passbackReasoningContent":      up.PassbackReasoningContent,
		"passbackThinkingBlocks":        up.PassbackThinkingBlocks,
		"stripEmptyTextBlocks":          up.StripEmptyTextBlocks,
		"normalizeSystemRoleToTopLevel": up.NormalizeSystemRoleToTopLevel,
		// Gemini 特定开关
		"injectDummyThoughtSignature": up.InjectDummyThoughtSignature,
		"stripThoughtSignature":       up.StripThoughtSignature,
		// 超时配置
		"requestTimeoutMs":            up.RequestTimeoutMs,
		"responseHeaderTimeoutMs":     up.ResponseHeaderTimeoutMs,
		"streamFirstContentTimeoutMs": up.StreamFirstContentTimeoutMs,
		"streamInactivityTimeoutMs":   up.StreamInactivityTimeoutMs,
		"streamToolCallIdleTimeoutMs": up.StreamToolCallIdleTimeoutMs,
		// Rate Limit（渠道级限速）
		"rateLimitRpm":             up.RateLimitRPM,
		"rateLimitWindowMinutes":   up.RateLimitWindowMinutes,
		"rateLimitBurst":           up.RateLimitBurst,
		"rateLimitMaxConcurrent":   up.RateLimitMaxConcurrent,
		"rateLimitAutoFromHeaders": up.IsRateLimitAutoFromHeadersEnabled(),
	}
}
