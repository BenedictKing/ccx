package handlers

import (
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/gin-gonic/gin"
)

// GetChannelMetricsWithConfig 获取渠道指标（需要配置管理器来获取 baseURL 和 keys）
func GetChannelMetricsWithConfig(metricsManager *metrics.MetricsManager, cfgManager *config.ConfigManager, isResponses bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		cfg := cfgManager.GetConfig()
		var upstreams []config.UpstreamConfig
		if isResponses {
			upstreams = cfg.ResponsesUpstream
		} else {
			upstreams = cfg.Upstream
		}

		result := make([]gin.H, 0, len(upstreams))
		for i, upstream := range upstreams {
			// 使用多 URL 聚合方法获取渠道指标（支持 failover 多端点场景）
			resp := metricsManager.ToResponseMultiURL(i, upstream.GetAllBaseURLs(), upstream.APIKeys, 0, upstream.HistoricalAPIKeys)

			item := gin.H{
				"channelIndex":        i,
				"channelName":         upstream.Name,
				"requestCount":        resp.RequestCount,
				"successCount":        resp.SuccessCount,
				"failureCount":        resp.FailureCount,
				"successRate":         resp.SuccessRate,
				"errorRate":           resp.ErrorRate,
				"consecutiveFailures": resp.ConsecutiveFailures,
				"latency":             resp.Latency,
				"circuitState":        resp.CircuitState,
				"halfOpenSuccesses":   resp.HalfOpenSuccesses,
				"breakerFailureRate":  resp.BreakerFailureRate,
				"keyMetrics":          resp.KeyMetrics,  // 各 Key 的详细指标
				"timeWindows":         resp.TimeWindows, // 分时段统计 (15m, 1h, 6h, 24h)
			}

			if resp.LastSuccessAt != nil {
				item["lastSuccessAt"] = *resp.LastSuccessAt
			}
			if resp.LastFailureAt != nil {
				item["lastFailureAt"] = *resp.LastFailureAt
			}
			if resp.CircuitBrokenAt != nil {
				item["circuitBrokenAt"] = *resp.CircuitBrokenAt
			}
			if resp.NextRetryAt != nil {
				item["nextRetryAt"] = *resp.NextRetryAt
			}

			result = append(result, item)
		}

		c.JSON(200, result)
	}
}

// GetAllKeyMetrics 获取所有 Key 的原始指标
func GetAllKeyMetrics(metricsManager *metrics.MetricsManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		allMetrics := metricsManager.GetAllKeyMetrics()

		result := make([]gin.H, 0, len(allMetrics))
		for _, m := range allMetrics {
			if m == nil {
				continue
			}

			successRate := float64(100)
			if m.RequestCount > 0 {
				successRate = float64(m.SuccessCount) / float64(m.RequestCount) * 100
			}

			item := gin.H{
				"metricsKey":          m.MetricsKey,
				"baseUrl":             m.BaseURL,
				"keyMask":             m.KeyMask,
				"requestCount":        m.RequestCount,
				"successCount":        m.SuccessCount,
				"failureCount":        m.FailureCount,
				"successRate":         successRate,
				"consecutiveFailures": m.ConsecutiveFailures,
				"circuitState":        m.CircuitState.String(),
				"halfOpenSuccesses":   m.HalfOpenSuccesses,
			}

			if m.LastSuccessAt != nil {
				item["lastSuccessAt"] = m.LastSuccessAt.Format("2006-01-02T15:04:05Z07:00")
			}
			if m.LastFailureAt != nil {
				item["lastFailureAt"] = m.LastFailureAt.Format("2006-01-02T15:04:05Z07:00")
			}
			if m.CircuitBrokenAt != nil {
				item["circuitBrokenAt"] = m.CircuitBrokenAt.Format("2006-01-02T15:04:05Z07:00")
			}
			if m.NextRetryAt != nil {
				item["nextRetryAt"] = m.NextRetryAt.Format("2006-01-02T15:04:05Z07:00")
			}

			result = append(result, item)
		}

		c.JSON(200, result)
	}
}

// GetChannelMetrics 获取渠道指标（兼容旧 API，返回空数据）
// Deprecated: 使用 GetChannelMetricsWithConfig 代替
func GetChannelMetrics(metricsManager *metrics.MetricsManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 返回所有 Key 的指标
		allMetrics := metricsManager.GetAllKeyMetrics()

		result := make([]gin.H, 0, len(allMetrics))
		for _, m := range allMetrics {
			if m == nil {
				continue
			}

			successRate := float64(100)
			if m.RequestCount > 0 {
				successRate = float64(m.SuccessCount) / float64(m.RequestCount) * 100
			}

			item := gin.H{
				"metricsKey":          m.MetricsKey,
				"baseUrl":             m.BaseURL,
				"keyMask":             m.KeyMask,
				"requestCount":        m.RequestCount,
				"successCount":        m.SuccessCount,
				"failureCount":        m.FailureCount,
				"successRate":         successRate,
				"consecutiveFailures": m.ConsecutiveFailures,
				"circuitState":        m.CircuitState.String(),
				"halfOpenSuccesses":   m.HalfOpenSuccesses,
			}

			if m.LastSuccessAt != nil {
				item["lastSuccessAt"] = m.LastSuccessAt.Format("2006-01-02T15:04:05Z07:00")
			}
			if m.LastFailureAt != nil {
				item["lastFailureAt"] = m.LastFailureAt.Format("2006-01-02T15:04:05Z07:00")
			}
			if m.CircuitBrokenAt != nil {
				item["circuitBrokenAt"] = m.CircuitBrokenAt.Format("2006-01-02T15:04:05Z07:00")
			}
			if m.NextRetryAt != nil {
				item["nextRetryAt"] = m.NextRetryAt.Format("2006-01-02T15:04:05Z07:00")
			}

			result = append(result, item)
		}

		c.JSON(200, result)
	}
}

// GetResponsesChannelMetrics 获取 Responses 渠道指标
// Deprecated: 使用 GetChannelMetricsWithConfig 代替
func GetResponsesChannelMetrics(metricsManager *metrics.MetricsManager) gin.HandlerFunc {
	return GetChannelMetrics(metricsManager)
}

// ResumeChannel 恢复熔断渠道（重置熔断状态、恢复拉黑 Key，保留历史统计）
// isResponses 参数指定是 Messages 渠道还是 Responses 渠道
func ResumeChannel(sch *scheduler.ChannelScheduler, cfgManager *config.ConfigManager, isResponses bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		idStr := c.Param("id")
		id, err := strconv.Atoi(idStr)
		if err != nil {
			c.JSON(400, gin.H{"error": "Invalid channel ID"})
			return
		}

		apiType := "Messages"
		kind := scheduler.ChannelKindMessages
		if isResponses {
			apiType = "Responses"
			kind = scheduler.ChannelKindResponses
		}

		// 先恢复被拉黑的 Key，再重置渠道所有 Key 的熔断状态，确保恢复出来的 Key 也被重置
		restoredCount, err := cfgManager.RestoreAllKeys(apiType, id)
		if err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		sch.ResetChannelMetrics(id, kind)

		message := "渠道已恢复，熔断状态已重置（历史统计保留）"
		if restoredCount > 0 {
			message = fmt.Sprintf("渠道已恢复，熔断状态已重置，同时恢复了 %d 个被拉黑的 Key", restoredCount)
		}

		c.JSON(200, gin.H{
			"success":      true,
			"message":      message,
			"restoredKeys": restoredCount,
		})
	}
}

// GetSchedulerStats 获取调度器统计信息
func GetSchedulerStats(sch *scheduler.ChannelScheduler) gin.HandlerFunc {
	return func(c *gin.Context) {
		queryType := strings.ToLower(c.Query("type"))

		var kind scheduler.ChannelKind
		var metricsManager *metrics.MetricsManager

		switch queryType {
		case "responses":
			kind = scheduler.ChannelKindResponses
			metricsManager = sch.GetResponsesMetricsManager()
		case "chat":
			kind = scheduler.ChannelKindChat
			metricsManager = sch.GetChatMetricsManager()
		default:
			kind = scheduler.ChannelKindMessages
			metricsManager = sch.GetMessagesMetricsManager()
		}

		stats := gin.H{
			"multiChannelMode":                      sch.IsMultiChannelMode(kind),
			"activeChannelCount":                    sch.GetActiveChannelCount(kind),
			"traceAffinityCount":                    sch.GetTraceAffinityManager().Size(),
			"traceAffinityTTL":                      sch.GetTraceAffinityManager().GetTTL().String(),
			"failureThreshold":                      metricsManager.GetFailureThreshold() * 100,
			"windowSize":                            metricsManager.GetWindowSize(),
			"circuitRecoveryTime":                   metricsManager.GetCircuitRecoveryTime().String(),
			"consecutiveRetryableFailuresThreshold": metricsManager.GetConsecutiveRetryableFailuresThreshold(),
			"halfOpenSuccessTarget":                 metricsManager.GetHalfOpenSuccessTarget(),
			"circuitBackoffBase":                    metricsManager.GetCircuitBackoffBase().String(),
			"circuitBackoffMax":                     metricsManager.GetCircuitBackoffMax().String(),
		}

		c.JSON(200, stats)
	}
}

// SetChannelPromotion 设置渠道促销期
// 促销期内的渠道会被优先选择，忽略 trace 亲和性
func SetChannelPromotion(cfgManager ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		idStr := c.Param("id")
		id, err := strconv.Atoi(idStr)
		if err != nil {
			c.JSON(400, gin.H{"error": "无效的渠道 ID"})
			return
		}

		var req struct {
			Duration int `json:"duration"` // 促销期时长（秒），0 表示清除
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "无效的请求参数"})
			return
		}

		// 调用配置管理器设置促销期
		duration := time.Duration(req.Duration) * time.Second
		if err := cfgManager.SetChannelPromotion(id, duration); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}

		if req.Duration <= 0 {
			c.JSON(200, gin.H{
				"success": true,
				"message": "渠道促销期已清除",
			})
		} else {
			c.JSON(200, gin.H{
				"success":  true,
				"message":  "渠道促销期已设置",
				"duration": req.Duration,
			})
		}
	}
}

// SetResponsesChannelPromotion 设置 Responses 渠道促销期
func SetResponsesChannelPromotion(cfgManager ResponsesConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		idStr := c.Param("id")
		id, err := strconv.Atoi(idStr)
		if err != nil {
			c.JSON(400, gin.H{"error": "无效的渠道 ID"})
			return
		}

		var req struct {
			Duration int `json:"duration"` // 促销期时长（秒），0 表示清除
		}
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(400, gin.H{"error": "无效的请求参数"})
			return
		}

		duration := time.Duration(req.Duration) * time.Second
		if err := cfgManager.SetResponsesChannelPromotion(id, duration); err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}

		if req.Duration <= 0 {
			c.JSON(200, gin.H{
				"success": true,
				"message": "Responses 渠道促销期已清除",
			})
		} else {
			c.JSON(200, gin.H{
				"success":  true,
				"message":  "Responses 渠道促销期已设置",
				"duration": req.Duration,
			})
		}
	}
}

// ConfigManager 促销期配置管理接口
type ConfigManager interface {
	SetChannelPromotion(index int, duration time.Duration) error
}

// ResponsesConfigManager Responses 渠道促销期配置管理接口
type ResponsesConfigManager interface {
	SetResponsesChannelPromotion(index int, duration time.Duration) error
}

// MetricsHistoryResponse 历史指标响应
type MetricsHistoryResponse struct {
	ChannelIndex int                        `json:"channelIndex"`
	ChannelName  string                     `json:"channelName"`
	DataPoints   []metrics.HistoryDataPoint `json:"dataPoints"`
}

// GetChannelMetricsHistory 获取渠道指标历史数据（用于时间序列图表）
// Query params:
//   - duration: 时间范围 (1h, 6h, 24h)，默认 24h
//   - interval: 时间间隔 (5m, 15m, 1h)，默认根据 duration 自动选择
func GetChannelMetricsHistory(metricsManager *metrics.MetricsManager, cfgManager *config.ConfigManager, isResponses bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 解析 duration 参数（支持 1h, 6h, 24h, 7d, 30d）
		durationStr := c.DefaultQuery("duration", "24h")
		duration, err := parseExtendedDuration(durationStr)
		if err != nil || duration <= 0 {
			c.JSON(400, gin.H{"error": "Invalid duration parameter"})
			return
		}

		// 限制最大查询范围
		maxDuration := 30 * 24 * time.Hour
		if duration > maxDuration {
			duration = maxDuration
		}

		// 解析或自动选择 interval
		interval := selectIntervalForDuration(c.Query("interval"), duration)

		cfg := cfgManager.GetConfig()
		var upstreams []config.UpstreamConfig
		if isResponses {
			upstreams = cfg.ResponsesUpstream
		} else {
			upstreams = cfg.Upstream
		}

		// >24h 走 SQLite 聚合查询
		if duration > 24*time.Hour {
			store := metricsManager.GetPersistenceStore()
			if store == nil {
				c.JSON(400, gin.H{"error": "长时间范围查询需要启用 SQLite 持久化存储"})
				return
			}
			apiType := metricsManager.GetAPIType()
			since := time.Now().Add(-duration)
			intervalSec := int64(interval.Seconds())

			result := make([]MetricsHistoryResponse, 0, len(upstreams))
			for i, upstream := range upstreams {
				// 过滤属于此渠道的数据（按 baseURL 匹配）
				allURLs := upstream.GetAllBaseURLs()
				channelBuckets := filterBucketsByURLs(store, apiType, since, intervalSec, allURLs, upstream.APIKeys)

				dataPoints := convertBucketsToDataPoints(channelBuckets)
				result = append(result, MetricsHistoryResponse{
					ChannelIndex: i,
					ChannelName:  upstream.Name,
					DataPoints:   dataPoints,
				})
			}
			c.JSON(200, result)
			return
		}

		// <=24h 走内存
		result := make([]MetricsHistoryResponse, 0, len(upstreams))
		for i, upstream := range upstreams {
			dataPoints := metricsManager.GetHistoricalStatsMultiURL(upstream.GetAllBaseURLs(), upstream.APIKeys, duration, interval)

			result = append(result, MetricsHistoryResponse{
				ChannelIndex: i,
				ChannelName:  upstream.Name,
				DataPoints:   dataPoints,
			})
		}

		c.JSON(200, result)
	}
}

// ChannelKeyMetricsHistoryResponse Key 级别历史指标响应
type ChannelKeyMetricsHistoryResponse struct {
	ChannelIndex int                       `json:"channelIndex"`
	ChannelName  string                    `json:"channelName"`
	Keys         []KeyMetricsHistoryResult `json:"keys"`
}

// KeyMetricsHistoryResult 单个 Key+Model 组合的历史数据
type KeyMetricsHistoryResult struct {
	KeyMask    string                        `json:"keyMask"`
	Model      string                        `json:"model,omitempty"` // 模型名（空表示聚合所有模型）
	Color      string                        `json:"color"`
	DataPoints []metrics.KeyHistoryDataPoint `json:"dataPoints"`
}

// Key 颜色配置（与前端一致）
var keyColors = []string{
	"#3b82f6", // Blue - Primary
	"#f97316", // Orange - Backup 1
	"#10b981", // Emerald - Backup 2
	"#8b5cf6", // Violet - Fallback
	"#ec4899", // Pink - Canary
}

// GetChannelKeyMetricsHistory 获取渠道下各 Key 的历史数据（用于 Key 趋势图表）
// GET /api/channels/:id/keys/metrics/history?duration=6h
func GetChannelKeyMetricsHistory(metricsManager *metrics.MetricsManager, cfgManager *config.ConfigManager, isResponses bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 解析 duration 参数
		durationStr := c.DefaultQuery("duration", "6h")

		var duration time.Duration
		var err error

		// 特殊处理 "today" 参数
		if durationStr == "today" {
			duration = metrics.CalculateTodayDuration()
			// 如果刚过零点，duration 可能非常小，设置最小值
			if duration < time.Minute {
				duration = time.Minute
			}
		} else {
			duration, err = parseExtendedDuration(durationStr)
			if err != nil {
				c.JSON(400, gin.H{"error": "Invalid duration parameter. Use: 1h, 6h, 24h, today, 7d, or 30d"})
				return
			}
		}

		// 限制最大查询范围为 24 小时（Key 级历史数据仅保留在内存中）
		// 注意：全局统计支持 30 天是因为使用 SQLite 持久化，但 Key 级数据未持久化
		if duration > 24*time.Hour {
			duration = 24 * time.Hour
		}

		// 解析或自动选择 interval
		intervalStr := c.Query("interval")
		var interval time.Duration
		if intervalStr != "" {
			interval, err = time.ParseDuration(intervalStr)
			if err != nil {
				c.JSON(400, gin.H{"error": "Invalid interval parameter"})
				return
			}
			// 限制 interval 最小值为 1 分钟，防止生成过多 bucket
			if interval < time.Minute {
				interval = time.Minute
			}
		} else {
			// 根据 duration 自动选择合适的聚合粒度
			// 目标：每个时间段约 60-100 个数据点，保持图表清晰
			// 1h = 60 points (1m interval)
			// 6h = 72 points (5m interval)
			// 24h = 96 points (15m interval)
			// 注意：Key 级历史数据最大支持 24h（内存限制）
			switch {
			case duration <= time.Hour:
				interval = time.Minute
			case duration <= 6*time.Hour:
				interval = 5 * time.Minute
			default:
				interval = 15 * time.Minute
			}
		}

		// 解析 channel ID
		channelIDStr := c.Param("id")
		channelID, err := strconv.Atoi(channelIDStr)
		if err != nil {
			c.JSON(400, gin.H{"error": "Invalid channel ID"})
			return
		}

		cfg := cfgManager.GetConfig()
		var upstreams []config.UpstreamConfig
		if isResponses {
			upstreams = cfg.ResponsesUpstream
		} else {
			upstreams = cfg.Upstream
		}

		// 检查 channel ID 是否有效
		if channelID < 0 || channelID >= len(upstreams) {
			c.JSON(400, gin.H{"error": "Channel not found"})
			return
		}

		upstream := upstreams[channelID]

		// 获取所有 Key 的使用信息并筛选（最多显示 10 个）
		const maxDisplayKeys = 10
		// 使用多 URL 聚合方法获取 Key 使用信息（支持 failover 多端点场景）
		allKeyInfos := metricsManager.GetChannelKeyUsageInfoMultiURL(upstream.GetAllBaseURLs(), upstream.APIKeys)
		displayKeys := metrics.SelectTopKeys(allKeyInfos, maxDisplayKeys)

		// 构建响应
		result := ChannelKeyMetricsHistoryResponse{
			ChannelIndex: channelID,
			ChannelName:  upstream.Name,
			Keys:         make([]KeyMetricsHistoryResult, 0),
		}

		// 为筛选后的 Key 获取历史数据
		// 同时返回按 Key 聚合的完整数据（含 token）和按模型拆分的流量数据
		colorIndex := 0
		for _, keyInfo := range displayKeys {
			keyMask := truncateKeyMask(keyInfo.KeyMask, 8)
			// 获取完整的 Key 级别数据（含 token/cache，用于 tokens/cache 视图）
			fullDataPoints := metricsManager.GetKeyHistoricalStatsMultiURL(upstream.GetAllBaseURLs(), keyInfo.APIKey, duration, interval)

			// 获取按模型拆分的流量数据
			modelData := metricsManager.GetKeyModelHistoricalStatsMultiURL(upstream.GetAllBaseURLs(), keyInfo.APIKey, duration, interval)

			if len(modelData) <= 1 {
				// 只有一个或没有模型时，直接返回聚合数据
				result.Keys = append(result.Keys, KeyMetricsHistoryResult{
					KeyMask:    keyMask,
					Color:      keyColors[colorIndex%len(keyColors)],
					DataPoints: fullDataPoints,
				})
				colorIndex++
			} else {
				// 多个模型时，按模型拆分返回（仅流量数据，token 数据从聚合数据获取）
				// 对模型名排序以保证颜色稳定
				models := make([]string, 0, len(modelData))
				for model := range modelData {
					models = append(models, model)
				}
				sort.Strings(models)

				for _, model := range models {
					points := modelData[model]
					dataPoints := make([]metrics.KeyHistoryDataPoint, len(points))
					for i, p := range points {
						dataPoints[i] = metrics.KeyHistoryDataPoint{
							Timestamp:                p.Timestamp,
							RequestCount:             p.RequestCount,
							SuccessCount:             p.SuccessCount,
							FailureCount:             p.FailureCount,
							InputTokens:              p.InputTokens,
							OutputTokens:             p.OutputTokens,
							CacheCreationInputTokens: p.CacheCreationInputTokens,
							CacheReadInputTokens:     p.CacheReadInputTokens,
						}
					}
					result.Keys = append(result.Keys, KeyMetricsHistoryResult{
						KeyMask:    keyMask,
						Model:      model,
						Color:      keyColors[colorIndex%len(keyColors)],
						DataPoints: dataPoints,
					})
					colorIndex++
				}
			}
		}

		c.JSON(200, result)
	}
}

// truncateKeyMask 截取 keyMask 的前 N 个字符
func truncateKeyMask(keyMask string, maxLen int) string {
	if len(keyMask) <= maxLen {
		return keyMask
	}
	return keyMask[:maxLen]
}

// GetChannelDashboard 获取渠道仪表盘数据（合并 channels + metrics + stats）
// GET /api/channels/dashboard?type=messages|responses|chat|gemini
// 将原本需要 3 个请求的数据合并为 1 个请求，减少网络开销
func GetChannelDashboard(cfgManager *config.ConfigManager, sch *scheduler.ChannelScheduler) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 获取 type 参数，默认为 messages
		channelType := strings.ToLower(c.Query("type"))
		if channelType == "" {
			channelType = "messages"
		}

		cfg := cfgManager.GetConfig()
		var upstreams []config.UpstreamConfig
		var metricsManager *metrics.MetricsManager
		var kind scheduler.ChannelKind
		apiType := "Messages"

		switch channelType {
		case "responses":
			upstreams = cfg.ResponsesUpstream
			metricsManager = sch.GetResponsesMetricsManager()
			kind = scheduler.ChannelKindResponses
			apiType = "Responses"
		case "chat":
			upstreams = cfg.ChatUpstream
			metricsManager = sch.GetChatMetricsManager()
			kind = scheduler.ChannelKindChat
			apiType = "Chat"
		case "gemini":
			upstreams = cfg.GeminiUpstream
			metricsManager = sch.GetGeminiMetricsManager()
			kind = scheduler.ChannelKindGemini
			apiType = "Gemini"
		default: // messages
			upstreams = cfg.Upstream
			metricsManager = sch.GetMessagesMetricsManager()
			kind = scheduler.ChannelKindMessages
			apiType = "Messages"
		}

		// 1. 构建 channels 数据
		channels := make([]gin.H, len(upstreams))
		for i, up := range upstreams {
			status := config.GetChannelStatus(&up)
			priority := config.GetChannelPriority(&up, i)

			channel := gin.H{
				"index":                           i,
				"name":                            up.Name,
				"serviceType":                     up.ServiceType,
				"baseUrl":                         up.BaseURL,
				"baseUrls":                        up.BaseURLs,
				"apiKeys":                         up.APIKeys,
				"description":                     up.Description,
				"website":                         up.Website,
				"insecureSkipVerify":              up.InsecureSkipVerify,
				"modelMapping":                    up.ModelMapping,
				"reasoningMapping":                up.ReasoningMapping,
				"textVerbosity":                   up.TextVerbosity,
				"fastMode":                        up.FastMode,
				"customHeaders":                   up.CustomHeaders,
				"proxyUrl":                        up.ProxyURL,
				"supportedModels":                 up.SupportedModels,
				"routePrefix":                     up.RoutePrefix,
				"disabledApiKeys":                 up.DisabledAPIKeys,
				"cooldownApiKeys":                 cfgManager.GetCooldownKeys(apiType, i),
				"autoBlacklistBalance":            up.IsAutoBlacklistBalanceEnabled(),
				"normalizeMetadataUserId":         up.IsNormalizeMetadataUserIDEnabled(),
				"streamPassthroughEnabled":        up.IsStreamPassthroughEnabled(),
				"strictRequestPassthroughEnabled": up.IsStrictRequestPassthroughEnabled(),
				"failoverRules":                   up.GetEffectiveFailoverRules(),
				"latency":                         nil,
				"status":                          status,
				"priority":                        priority,
				"promotionUntil":                  up.PromotionUntil,
				"lowQuality":                      up.LowQuality,
				"rpm":                             up.RPM,
			}

			// Gemini 特有字段
			if channelType == "gemini" {
				channel["injectDummyThoughtSignature"] = up.InjectDummyThoughtSignature
				channel["stripThoughtSignature"] = up.StripThoughtSignature
			}

			channels[i] = channel
		}

		// 2. 构建 metrics 数据
		metricsResult := make([]gin.H, 0, len(upstreams))
		for i, upstream := range upstreams {
			resp := metricsManager.ToResponseMultiURL(i, upstream.GetAllBaseURLs(), upstream.APIKeys, 0, upstream.HistoricalAPIKeys)

			item := gin.H{
				"channelIndex":        i,
				"channelName":         upstream.Name,
				"requestCount":        resp.RequestCount,
				"successCount":        resp.SuccessCount,
				"failureCount":        resp.FailureCount,
				"successRate":         resp.SuccessRate,
				"errorRate":           resp.ErrorRate,
				"consecutiveFailures": resp.ConsecutiveFailures,
				"latency":             resp.Latency,
				"circuitState":        resp.CircuitState,
				"halfOpenSuccesses":   resp.HalfOpenSuccesses,
				"breakerFailureRate":  resp.BreakerFailureRate,
				"keyMetrics":          resp.KeyMetrics,
				"timeWindows":         resp.TimeWindows,
			}

			if resp.LastSuccessAt != nil {
				item["lastSuccessAt"] = *resp.LastSuccessAt
			}
			if resp.LastFailureAt != nil {
				item["lastFailureAt"] = *resp.LastFailureAt
			}
			if resp.CircuitBrokenAt != nil {
				item["circuitBrokenAt"] = *resp.CircuitBrokenAt
			}
			if resp.NextRetryAt != nil {
				item["nextRetryAt"] = *resp.NextRetryAt
			}

			metricsResult = append(metricsResult, item)
		}

		// 3. 构建 stats 数据
		stats := gin.H{
			"multiChannelMode":                      sch.IsMultiChannelMode(kind),
			"activeChannelCount":                    sch.GetActiveChannelCount(kind),
			"traceAffinityCount":                    sch.GetTraceAffinityManager().Size(),
			"traceAffinityTTL":                      sch.GetTraceAffinityManager().GetTTL().String(),
			"failureThreshold":                      metricsManager.GetFailureThreshold() * 100,
			"windowSize":                            metricsManager.GetWindowSize(),
			"circuitRecoveryTime":                   metricsManager.GetCircuitRecoveryTime().String(),
			"consecutiveRetryableFailuresThreshold": metricsManager.GetConsecutiveRetryableFailuresThreshold(),
			"halfOpenSuccessTarget":                 metricsManager.GetHalfOpenSuccessTarget(),
			"circuitBackoffBase":                    metricsManager.GetCircuitBackoffBase().String(),
			"circuitBackoffMax":                     metricsManager.GetCircuitBackoffMax().String(),
		}

		// 4. 构建 recentActivity 数据（最近 15 分钟分段活跃度）
		recentActivity := make([]*metrics.ChannelRecentActivity, len(upstreams))
		for i, upstream := range upstreams {
			recentActivity[i] = metricsManager.GetRecentActivityMultiURL(i, upstream.GetAllBaseURLs(), upstream.APIKeys)
		}

		// 返回合并数据
		c.JSON(200, gin.H{
			"channels":       channels,
			"metrics":        metricsResult,
			"stats":          stats,
			"recentActivity": recentActivity,
		})
	}
}

// GetGeminiChannelMetricsHistory 获取 Gemini 渠道指标历史数据（用于时间序列图表）
// Query params:
//   - duration: 时间范围 (1h, 6h, 24h)，默认 24h
//   - interval: 时间间隔 (5m, 15m, 1h)，默认根据 duration 自动选择
func GetGeminiChannelMetricsHistory(metricsManager *metrics.MetricsManager, cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		durationStr := c.DefaultQuery("duration", "24h")
		duration, err := parseExtendedDuration(durationStr)
		if err != nil || duration <= 0 {
			c.JSON(400, gin.H{"error": "Invalid duration parameter"})
			return
		}
		maxDuration := 30 * 24 * time.Hour
		if duration > maxDuration {
			duration = maxDuration
		}
		interval := selectIntervalForDuration(c.Query("interval"), duration)

		cfg := cfgManager.GetConfig()
		upstreams := cfg.GeminiUpstream

		if duration > 24*time.Hour {
			store := metricsManager.GetPersistenceStore()
			if store == nil {
				c.JSON(400, gin.H{"error": "长时间范围查询需要启用 SQLite 持久化存储"})
				return
			}
			apiType := metricsManager.GetAPIType()
			since := time.Now().Add(-duration)
			intervalSec := int64(interval.Seconds())
			result := make([]MetricsHistoryResponse, 0, len(upstreams))
			for i, upstream := range upstreams {
				channelBuckets := filterBucketsByURLs(store, apiType, since, intervalSec, upstream.GetAllBaseURLs(), upstream.APIKeys)
				result = append(result, MetricsHistoryResponse{
					ChannelIndex: i,
					ChannelName:  upstream.Name,
					DataPoints:   convertBucketsToDataPoints(channelBuckets),
				})
			}
			c.JSON(200, result)
			return
		}

		result := make([]MetricsHistoryResponse, 0, len(upstreams))
		for i, upstream := range upstreams {
			dataPoints := metricsManager.GetHistoricalStatsMultiURL(upstream.GetAllBaseURLs(), upstream.APIKeys, duration, interval)
			result = append(result, MetricsHistoryResponse{
				ChannelIndex: i,
				ChannelName:  upstream.Name,
				DataPoints:   dataPoints,
			})
		}

		c.JSON(200, result)
	}
}

// GetGeminiChannelKeyMetricsHistory 获取 Gemini 渠道下各 Key 的历史数据（用于 Key 趋势图表）
// GET /api/gemini/channels/:id/keys/metrics/history?duration=6h
func GetGeminiChannelKeyMetricsHistory(metricsManager *metrics.MetricsManager, cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		// 解析 duration 参数
		durationStr := c.DefaultQuery("duration", "6h")

		var duration time.Duration
		var err error

		// 特殊处理 "today" 参数
		if durationStr == "today" {
			duration = metrics.CalculateTodayDuration()
			// 如果刚过零点，duration 可能非常小，设置最小值
			if duration < time.Minute {
				duration = time.Minute
			}
		} else {
			duration, err = parseExtendedDuration(durationStr)
			if err != nil {
				c.JSON(400, gin.H{"error": "Invalid duration parameter. Use: 1h, 6h, 24h, today, 7d, or 30d"})
				return
			}
		}

		// 限制最大查询范围为 24 小时（Key 级历史数据仅保留在内存中）
		// 注意：全局统计支持 30 天是因为使用 SQLite 持久化，但 Key 级数据未持久化
		if duration > 24*time.Hour {
			duration = 24 * time.Hour
		}

		// 解析或自动选择 interval
		intervalStr := c.Query("interval")
		var interval time.Duration
		if intervalStr != "" {
			interval, err = time.ParseDuration(intervalStr)
			if err != nil {
				c.JSON(400, gin.H{"error": "Invalid interval parameter"})
				return
			}
			// 限制 interval 最小值为 1 分钟，防止生成过多 bucket
			if interval < time.Minute {
				interval = time.Minute
			}
		} else {
			// 根据 duration 自动选择合适的聚合粒度
			switch {
			case duration <= time.Hour:
				interval = time.Minute
			case duration <= 6*time.Hour:
				interval = 5 * time.Minute
			default:
				interval = 15 * time.Minute
			}
		}

		// 解析 channel ID
		channelIDStr := c.Param("id")
		channelID, err := strconv.Atoi(channelIDStr)
		if err != nil {
			c.JSON(400, gin.H{"error": "Invalid channel ID"})
			return
		}

		cfg := cfgManager.GetConfig()
		upstreams := cfg.GeminiUpstream

		// 检查 channel ID 是否有效
		if channelID < 0 || channelID >= len(upstreams) {
			c.JSON(400, gin.H{"error": "Channel not found"})
			return
		}

		upstream := upstreams[channelID]

		// 获取所有 Key 的使用信息并筛选（最多显示 10 个）
		const maxDisplayKeys = 10
		// 使用多 URL 聚合方法获取 Key 使用信息（支持 failover 多端点场景）
		allKeyInfos := metricsManager.GetChannelKeyUsageInfoMultiURL(upstream.GetAllBaseURLs(), upstream.APIKeys)
		displayKeys := metrics.SelectTopKeys(allKeyInfos, maxDisplayKeys)

		// 构建响应
		result := ChannelKeyMetricsHistoryResponse{
			ChannelIndex: channelID,
			ChannelName:  upstream.Name,
			Keys:         make([]KeyMetricsHistoryResult, 0, len(displayKeys)),
		}

		// 为筛选后的 Key 获取历史数据
		for i, keyInfo := range displayKeys {
			// 使用多 URL 聚合方法获取单个 Key 的历史数据（支持 failover 多端点场景）
			dataPoints := metricsManager.GetKeyHistoricalStatsMultiURL(upstream.GetAllBaseURLs(), keyInfo.APIKey, duration, interval)

			// 获取 Key 的颜色
			color := keyColors[i%len(keyColors)]

			// 获取 Key 的脱敏显示（只取前 8 个字符）
			keyMask := truncateKeyMask(keyInfo.KeyMask, 8)

			result.Keys = append(result.Keys, KeyMetricsHistoryResult{
				KeyMask:    keyMask,
				Color:      color,
				DataPoints: dataPoints,
			})
		}

		c.JSON(200, result)
	}
}

// GetGeminiChannelMetrics 获取 Gemini 渠道指标
func GetGeminiChannelMetrics(metricsManager *metrics.MetricsManager, cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		cfg := cfgManager.GetConfig()
		upstreams := cfg.GeminiUpstream

		result := make([]gin.H, 0, len(upstreams))
		for i, upstream := range upstreams {
			// 使用多 URL 聚合方法获取渠道指标（支持 failover 多端点场景）
			resp := metricsManager.ToResponseMultiURL(i, upstream.GetAllBaseURLs(), upstream.APIKeys, 0, upstream.HistoricalAPIKeys)

			item := gin.H{
				"channelIndex":        i,
				"channelName":         upstream.Name,
				"requestCount":        resp.RequestCount,
				"successCount":        resp.SuccessCount,
				"failureCount":        resp.FailureCount,
				"successRate":         resp.SuccessRate,
				"errorRate":           resp.ErrorRate,
				"consecutiveFailures": resp.ConsecutiveFailures,
				"latency":             resp.Latency,
				"circuitState":        resp.CircuitState,
				"halfOpenSuccesses":   resp.HalfOpenSuccesses,
				"breakerFailureRate":  resp.BreakerFailureRate,
				"keyMetrics":          resp.KeyMetrics,  // 各 Key 的详细指标
				"timeWindows":         resp.TimeWindows, // 分时段统计 (15m, 1h, 6h, 24h)
			}

			if resp.LastSuccessAt != nil {
				item["lastSuccessAt"] = *resp.LastSuccessAt
			}
			if resp.LastFailureAt != nil {
				item["lastFailureAt"] = *resp.LastFailureAt
			}
			if resp.CircuitBrokenAt != nil {
				item["circuitBrokenAt"] = *resp.CircuitBrokenAt
			}
			if resp.NextRetryAt != nil {
				item["nextRetryAt"] = *resp.NextRetryAt
			}

			result = append(result, item)
		}

		c.JSON(200, result)
	}
}

// GetChatChannelMetrics 获取 Chat 渠道指标
func GetChatChannelMetrics(metricsManager *metrics.MetricsManager, cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		cfg := cfgManager.GetConfig()
		result := make([]gin.H, 0, len(cfg.ChatUpstream))
		for i, upstream := range cfg.ChatUpstream {
			resp := metricsManager.ToResponseMultiURL(i, upstream.GetAllBaseURLs(), upstream.APIKeys, 0, upstream.HistoricalAPIKeys)
			item := gin.H{
				"channelIndex":        i,
				"channelName":         upstream.Name,
				"requestCount":        resp.RequestCount,
				"successCount":        resp.SuccessCount,
				"failureCount":        resp.FailureCount,
				"successRate":         resp.SuccessRate,
				"errorRate":           resp.ErrorRate,
				"consecutiveFailures": resp.ConsecutiveFailures,
				"latency":             resp.Latency,
				"circuitState":        resp.CircuitState,
				"halfOpenSuccesses":   resp.HalfOpenSuccesses,
				"breakerFailureRate":  resp.BreakerFailureRate,
				"keyMetrics":          resp.KeyMetrics,
				"timeWindows":         resp.TimeWindows,
			}
			if resp.LastSuccessAt != nil {
				item["lastSuccessAt"] = *resp.LastSuccessAt
			}
			if resp.LastFailureAt != nil {
				item["lastFailureAt"] = *resp.LastFailureAt
			}
			if resp.CircuitBrokenAt != nil {
				item["circuitBrokenAt"] = *resp.CircuitBrokenAt
			}
			if resp.NextRetryAt != nil {
				item["nextRetryAt"] = *resp.NextRetryAt
			}
			result = append(result, item)
		}
		c.JSON(200, result)
	}
}

// GetChatChannelMetricsHistory 获取 Chat 渠道指标历史数据
func GetChatChannelMetricsHistory(metricsManager *metrics.MetricsManager, cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		duration, interval := parseHistoryDuration(c)
		cfg := cfgManager.GetConfig()

		if duration > 24*time.Hour {
			store := metricsManager.GetPersistenceStore()
			if store == nil {
				c.JSON(400, gin.H{"error": "长时间范围查询需要启用 SQLite 持久化存储"})
				return
			}
			apiType := metricsManager.GetAPIType()
			since := time.Now().Add(-duration)
			intervalSec := int64(interval.Seconds())
			result := make([]MetricsHistoryResponse, 0, len(cfg.ChatUpstream))
			for i, upstream := range cfg.ChatUpstream {
				channelBuckets := filterBucketsByURLs(store, apiType, since, intervalSec, upstream.GetAllBaseURLs(), upstream.APIKeys)
				result = append(result, MetricsHistoryResponse{ChannelIndex: i, ChannelName: upstream.Name, DataPoints: convertBucketsToDataPoints(channelBuckets)})
			}
			c.JSON(200, result)
			return
		}

		result := make([]MetricsHistoryResponse, 0, len(cfg.ChatUpstream))
		for i, upstream := range cfg.ChatUpstream {
			dataPoints := metricsManager.GetHistoricalStatsMultiURL(upstream.GetAllBaseURLs(), upstream.APIKeys, duration, interval)
			result = append(result, MetricsHistoryResponse{ChannelIndex: i, ChannelName: upstream.Name, DataPoints: dataPoints})
		}
		c.JSON(200, result)
	}
}

// GetChatChannelKeyMetricsHistory 获取 Chat 渠道下各 Key 的历史数据
func GetChatChannelKeyMetricsHistory(metricsManager *metrics.MetricsManager, cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		duration, interval := parseKeyHistoryDuration(c)
		channelID, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(400, gin.H{"error": "Invalid channel ID"})
			return
		}
		cfg := cfgManager.GetConfig()
		if channelID < 0 || channelID >= len(cfg.ChatUpstream) {
			c.JSON(400, gin.H{"error": "Channel not found"})
			return
		}
		upstream := cfg.ChatUpstream[channelID]
		allKeyInfos := metricsManager.GetChannelKeyUsageInfoMultiURL(upstream.GetAllBaseURLs(), upstream.APIKeys)
		displayKeys := metrics.SelectTopKeys(allKeyInfos, 10)
		result := ChannelKeyMetricsHistoryResponse{ChannelIndex: channelID, ChannelName: upstream.Name, Keys: make([]KeyMetricsHistoryResult, 0, len(displayKeys))}
		for i, keyInfo := range displayKeys {
			dataPoints := metricsManager.GetKeyHistoricalStatsMultiURL(upstream.GetAllBaseURLs(), keyInfo.APIKey, duration, interval)
			result.Keys = append(result.Keys, KeyMetricsHistoryResult{KeyMask: truncateKeyMask(keyInfo.KeyMask, 8), Color: keyColors[i%len(keyColors)], DataPoints: dataPoints})
		}
		c.JSON(200, result)
	}
}

// ResumeChannelWithKind 恢复指定类型的熔断渠道（重置熔断状态、恢复拉黑 Key，保留历史统计）
func ResumeChannelWithKind(sch *scheduler.ChannelScheduler, cfgManager *config.ConfigManager, kind scheduler.ChannelKind) gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := strconv.Atoi(c.Param("id"))
		if err != nil {
			c.JSON(400, gin.H{"error": "Invalid channel ID"})
			return
		}

		apiType := "Messages"
		switch kind {
		case scheduler.ChannelKindResponses:
			apiType = "Responses"
		case scheduler.ChannelKindGemini:
			apiType = "Gemini"
		case scheduler.ChannelKindChat:
			apiType = "Chat"
		}

		// 先恢复被拉黑的 Key，再重置渠道所有 Key 的熔断状态，确保恢复出来的 Key 也被重置
		restoredCount, err := cfgManager.RestoreAllKeys(apiType, id)
		if err != nil {
			c.JSON(400, gin.H{"error": err.Error()})
			return
		}
		sch.ResetChannelMetrics(id, kind)

		message := "渠道已恢复，熔断状态已重置（历史统计保留）"
		if restoredCount > 0 {
			message = fmt.Sprintf("渠道已恢复，熔断状态已重置，同时恢复了 %d 个被拉黑的 Key", restoredCount)
		}

		c.JSON(200, gin.H{"success": true, "message": message, "restoredKeys": restoredCount})
	}
}

// parseHistoryDuration 解析历史数据查询参数
func parseHistoryDuration(c *gin.Context) (time.Duration, time.Duration) {
	durationStr := c.DefaultQuery("duration", "24h")
	duration, err := parseExtendedDuration(durationStr)
	if err != nil || duration <= 0 {
		duration = 24 * time.Hour
	}
	maxDuration := 30 * 24 * time.Hour
	if duration > maxDuration {
		duration = maxDuration
	}
	return duration, selectIntervalForDuration(c.Query("interval"), duration)
}

// parseKeyHistoryDuration 解析 Key 历史数据查询参数（支持 today）
func parseKeyHistoryDuration(c *gin.Context) (time.Duration, time.Duration) {
	durationStr := c.DefaultQuery("duration", "6h")
	duration, err := parseExtendedDuration(durationStr)
	if err != nil || duration < time.Minute {
		duration = 6 * time.Hour // 回退到默认值
	}
	maxDuration := 30 * 24 * time.Hour
	if duration > maxDuration {
		duration = maxDuration
	}
	return duration, selectIntervalForDuration(c.Query("interval"), duration)
}

// selectIntervalForDuration 解析或自动选择 interval
func selectIntervalForDuration(intervalStr string, duration time.Duration) time.Duration {
	if intervalStr != "" {
		interval, err := time.ParseDuration(intervalStr)
		if err == nil && interval >= time.Minute {
			return interval
		}
	}
	switch {
	case duration <= time.Hour:
		return time.Minute
	case duration <= 6*time.Hour:
		return 5 * time.Minute
	case duration <= 24*time.Hour:
		return 15 * time.Minute
	case duration <= 7*24*time.Hour:
		return time.Hour
	default:
		return 4 * time.Hour
	}
}

// parseExtendedDuration 解析扩展的时间范围字符串
// 支持标准 Go duration (1h, 6h, 24h) 和扩展格式 (7d, 30d, today)
func parseExtendedDuration(s string) (time.Duration, error) {
	if s == "today" {
		d := metrics.CalculateTodayDuration()
		if d < time.Minute {
			d = time.Minute
		}
		return d, nil
	}
	// 尝试天数格式: 7d, 30d
	if strings.HasSuffix(s, "d") {
		dayStr := strings.TrimSuffix(s, "d")
		days, err := strconv.Atoi(dayStr)
		if err != nil {
			return 0, err
		}
		if days <= 0 {
			return 0, fmt.Errorf("invalid duration: days must be positive, got %d", days)
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}
	// 标准 Go duration
	return time.ParseDuration(s)
}

// filterBucketsByURLs 按渠道的 URL 和 Key 过滤 SQLite 聚合数据
func filterBucketsByURLs(store metrics.PersistenceStore, apiType string, since time.Time, intervalSec int64, baseURLs []string, apiKeys []string) []metrics.AggregatedBucket {
	// SQLite 里的聚合记录是按 metrics_key(baseURL + apiKey) 归属的。
	// 因此这里必须按当前渠道的 URL+Key 组合逐个查询并汇总，
	// 不能只按 baseURL 过滤，否则多个共用 baseURL 的渠道会串数据。
	bucketMap := make(map[int64]*metrics.AggregatedBucket)

	for _, baseURL := range baseURLs {
		for _, apiKey := range apiKeys {
			metricsKey := metrics.GenerateMetricsKey(baseURL, apiKey)
			buckets, err := store.QueryAggregatedHistory(apiType, since, intervalSec, metricsKey, "")
			if err != nil {
				log.Printf("[Metrics-History] 查询 metricsKey %s 失败(baseURL=%s): %v", metricsKey, baseURL, err)
				continue
			}
			for _, b := range buckets {
				ts := b.Timestamp.Unix()
				if existing, ok := bucketMap[ts]; ok {
					existing.TotalRequests += b.TotalRequests
					existing.SuccessCount += b.SuccessCount
					existing.InputTokens += b.InputTokens
					existing.OutputTokens += b.OutputTokens
					existing.CacheCreationTokens += b.CacheCreationTokens
					existing.CacheReadTokens += b.CacheReadTokens
				} else {
					copy := b
					bucketMap[ts] = &copy
				}
			}
		}
	}

	// 转为有序 slice
	result := make([]metrics.AggregatedBucket, 0, len(bucketMap))
	for _, b := range bucketMap {
		result = append(result, *b)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Timestamp.Before(result[j].Timestamp)
	})
	return result
}

// convertBucketsToDataPoints 将 SQLite 聚合桶转为 HistoryDataPoint 格式
func convertBucketsToDataPoints(buckets []metrics.AggregatedBucket) []metrics.HistoryDataPoint {
	points := make([]metrics.HistoryDataPoint, 0, len(buckets))
	for _, b := range buckets {
		var successRate float64
		if b.TotalRequests > 0 {
			successRate = float64(b.SuccessCount) / float64(b.TotalRequests) * 100
		}
		points = append(points, metrics.HistoryDataPoint{
			Timestamp:    b.Timestamp,
			RequestCount: b.TotalRequests,
			SuccessCount: b.SuccessCount,
			FailureCount: b.TotalRequests - b.SuccessCount,
			SuccessRate:  successRate,
		})
	}
	return points
}
