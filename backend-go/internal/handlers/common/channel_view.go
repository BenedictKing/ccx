package common

import (
	"strings"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/gin-gonic/gin"
)

func BuildChannelView(up config.UpstreamConfig, index int) gin.H {
	status := config.GetChannelStatus(&up)
	priority := config.GetChannelPriority(&up, index)
	view := gin.H{
		"index":                         index,
		"name":                          up.Name,
		"accountUid":                    up.AccountUID,
		"channelUid":                    up.ChannelUID,
		"providerId":                    up.ProviderID,
		"serviceType":                   up.ServiceType,
		"authHeader":                    up.AuthHeader,
		"baseUrl":                       up.BaseURL,
		"baseUrls":                      up.BaseURLs,
		"apiKeys":                       up.APIKeys,
		"apiKeyConfigs":                 config.NormalizeAPIKeyConfigsForView(up),
		"description":                   up.Description,
		"remark":                        up.Remark,
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
		"normalizeNonstandardChatRoles": up.IsNormalizeNonstandardChatRolesEnabled(),
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
		"proxyPreferDirect":             up.ProxyPreferDirect,
		"supportedModels":               up.SupportedModels,
		"routePrefix":                   up.RoutePrefix,
		"disabledApiKeys":               up.DisabledAPIKeys,
		"autoManaged":                   up.AutoManaged,
		"autoManagedAt":                 up.AutoManagedAt,
		"autoManagedKind":               up.AutoManagedKind,
		"originType":                    up.OriginType,
		"originTier":                    up.OriginTier,
		"autoBlacklistBalance":          up.IsAutoBlacklistBalanceEnabled(),
		"normalizeMetadataUserId":       up.IsNormalizeMetadataUserIDEnabled(),
		"stripBillingHeader":            up.IsStripBillingHeaderEnabled(),
		"codexNativeToolPassthrough":    up.IsCodexNativeToolPassthroughEnabled(),
		"codexToolCompat":               up.IsCodexToolCompatEnabled(),
		"stripImageGenerationTool":      up.IsStripImageGenerationToolEnabled(),
		"convertImageUrlToB64Json":      up.ConvertImageURLToB64JSON,
		"noVision":                      up.NoVision,
		"noVisionModels":                up.NoVisionModels,
		"visionFallbackModel":           up.VisionFallbackModel,
		"historicalImageTurnLimit":      up.HistoricalImageTurnLimit,
		"passbackReasoningContent":      up.IsPassbackReasoningContentEnabled(),
		"passbackThinkingBlocks":        up.IsPassbackThinkingBlocksEnabled(),
		"stripEmptyTextBlocks":          up.IsStripEmptyTextBlocksEnabled(),
		"normalizeSystemRoleToTopLevel": up.NormalizeSystemRoleToTopLevel,
		"injectDummyThoughtSignature":   up.InjectDummyThoughtSignature,
		"stripThoughtSignature":         up.StripThoughtSignature,
		"requestTimeoutMs":              up.RequestTimeoutMs,
		"responseHeaderTimeoutMs":       up.ResponseHeaderTimeoutMs,
		"streamFirstContentTimeoutMs":   up.StreamFirstContentTimeoutMs,
		"streamInactivityTimeoutMs":     up.StreamInactivityTimeoutMs,
		"streamToolCallIdleTimeoutMs":   up.StreamToolCallIdleTimeoutMs,
		"rateLimitRpm":                  up.RateLimitRPM,
		"rateLimitWindowMinutes":        up.RateLimitWindowMinutes,
		"rateLimitBurst":                up.RateLimitBurst,
		"rateLimitMaxConcurrent":        up.RateLimitMaxConcurrent,
		"rateLimitAutoFromHeaders":      up.IsRateLimitAutoFromHeadersEnabled(),
		"logicalChannelUid":             up.LogicalChannelUID,
		"logicalName":                   up.LogicalName,
	}
	for _, keyConfig := range up.APIKeyConfigs {
		if uid := strings.TrimSpace(keyConfig.SourceSubscriptionUID); uid != "" {
			view["subscriptionUid"] = uid
			break
		}
	}
	return view
}
