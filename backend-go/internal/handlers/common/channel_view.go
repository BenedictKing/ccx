package common

import (
	"github.com/BenedictKing/ccx/internal/config"
	"github.com/gin-gonic/gin"
)

func BuildChannelView(up config.UpstreamConfig, index int) gin.H {
	status := config.GetChannelStatus(&up)
	priority := config.GetChannelPriority(&up, index)
	return gin.H{
		"index":                   index,
		"name":                    up.Name,
		"serviceType":             up.ServiceType,
		"baseUrl":                 up.BaseURL,
		"baseUrls":                up.BaseURLs,
		"apiKeys":                 up.APIKeys,
		"description":             up.Description,
		"website":                 up.Website,
		"insecureSkipVerify":      up.InsecureSkipVerify,
		"modelMapping":            up.ModelMapping,
		"reasoningMapping":        up.ReasoningMapping,
		"textVerbosity":           up.TextVerbosity,
		"fastMode":                up.FastMode,
		"latency":                 nil,
		"status":                  status,
		"adminState":              config.GetChannelAdminState(&up),
		"effectiveState":          config.GetChannelEffectiveState(&up),
		"runtimeState":            config.GetChannelRuntimeState(&up),
		"priority":                priority,
		"promotionUntil":          up.PromotionUntil,
		"lowQuality":              up.LowQuality,
		"customHeaders":           up.CustomHeaders,
		"proxyUrl":                up.ProxyURL,
		"supportedModels":         up.SupportedModels,
		"routePrefix":             up.RoutePrefix,
		"disabledApiKeys":         up.DisabledAPIKeys,
		"autoBlacklistBalance":    up.IsAutoBlacklistBalanceEnabled(),
		"normalizeMetadataUserId": up.IsNormalizeMetadataUserIDEnabled(),
	}
}
