package handlers

import (
	"strconv"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/gin-gonic/gin"
)

// GetChannelLogs 获取渠道请求日志
// 路由参数 :id 仍为渠道在配置中的数组下标（与前端保持一致），
// 后端按该渠道的所有 baseURL × (APIKeys ∪ HistoricalAPIKeys) 反查合并日志桶。
func GetChannelLogs(
	channelLogStore *metrics.ChannelLogStore,
	cfgManager *config.ConfigManager,
	kind scheduler.ChannelKind,
	metricsManager *metrics.MetricsManager,
) gin.HandlerFunc {
	return func(c *gin.Context) {
		idStr := c.Param("id")
		channelIndex, err := strconv.Atoi(idStr)
		if err != nil {
			c.JSON(400, gin.H{"error": "Invalid channel ID"})
			return
		}

		upstream := getUpstreamByKindAndIndex(cfgManager, kind, channelIndex)
		if upstream == nil {
			c.JSON(200, gin.H{
				"channelIndex": channelIndex,
				"logs":         make([]*metrics.ChannelLog, 0),
			})
			return
		}

		normalizedServiceType := scheduler.NormalizedMetricsServiceType(kind, upstream.ServiceType)
		metricsKeys := channelMetricsKeys(upstream, normalizedServiceType)
		sharedMetricsKeys := sharedChannelMetricsKeys(cfgManager, kind)

		logs := channelLogStore.GetMergedFiltered(metricsKeys, func(logEntry *metrics.ChannelLog) bool {
			if logEntry == nil {
				return false
			}
			// 日志带创建时渠道名时按名称归属：可避免删除/重命名/重排后日志串台。
			if logEntry.ChannelName != "" && upstream.Name != "" {
				return logEntry.ChannelName == upstream.Name
			}
			// 旧日志无渠道名：共享 metricsKey 按 index 比对，独占 metricsKey 直接放行。
			if sharedMetricsKeys[logEntry.MetricsKey] {
				return logEntry.ChannelIndex == channelIndex
			}
			return true
		})
		if logs == nil {
			logs = make([]*metrics.ChannelLog, 0)
		}

		body := gin.H{
			"channelIndex": channelIndex,
			"channelName":  upstream.Name,
			"logs":         logs,
		}
		// 日志为空但渠道处于熔断态时补上成因依据：渠道日志是内存态，重启即清空，
		// 而 breaker 状态持久化恢复，否则界面会呈现"无日志却熔断"的黑盒。
		if len(logs) == 0 && metricsManager != nil {
			if evidence := metricsManager.GetBreakerEvidenceByMetricsKeys(metricsKeys); evidence != nil &&
				evidence.CircuitState != "closed" {
				body["breakerEvidence"] = evidence
			}
		}

		c.JSON(200, body)
	}
}

func getUpstreamByKindAndIndex(cfgManager *config.ConfigManager, kind scheduler.ChannelKind, index int) *config.UpstreamConfig {
	cfg := cfgManager.GetConfig()

	var upstreams []config.UpstreamConfig
	switch kind {
	case scheduler.ChannelKindResponses:
		upstreams = cfg.ResponsesUpstream
	case scheduler.ChannelKindGemini:
		upstreams = cfg.GeminiUpstream
	case scheduler.ChannelKindChat:
		upstreams = cfg.ChatUpstream
	case scheduler.ChannelKindImages:
		upstreams = cfg.ImagesUpstream
	case scheduler.ChannelKindVectors:
		upstreams = cfg.VectorsUpstream
	default:
		upstreams = cfg.Upstream
	}

	if index < 0 || index >= len(upstreams) {
		return nil
	}
	upstream := upstreams[index]
	return &upstream
}

// channelMetricsKeys 枚举渠道所有 baseURL × (APIKeys ∪ HistoricalAPIKeys) 对应的 metricsKeys，
// 与统计查询口径一致（含 legacy baseURL 等价变体）。
func channelMetricsKeys(upstream *config.UpstreamConfig, normalizedServiceType string) []string {
	if upstream == nil {
		return nil
	}
	seen := make(map[string]struct{})
	var keys []string
	add := func(metricsKey string) {
		if metricsKey == "" {
			return
		}
		if _, exists := seen[metricsKey]; exists {
			return
		}
		seen[metricsKey] = struct{}{}
		keys = append(keys, metricsKey)
	}

	allAPIKeys := channelStatsAPIKeys(*upstream)

	for _, baseURL := range upstream.GetAllBaseURLs() {
		for _, apiKey := range allAPIKeys {
			for _, metricsKey := range metrics.GenerateMetricsLookupKeys(baseURL, apiKey, normalizedServiceType) {
				add(metricsKey)
			}
		}
	}
	return keys
}

func sharedChannelMetricsKeys(cfgManager *config.ConfigManager, kind scheduler.ChannelKind) map[string]bool {
	cfg := cfgManager.GetConfig()

	var upstreams []config.UpstreamConfig
	switch kind {
	case scheduler.ChannelKindResponses:
		upstreams = cfg.ResponsesUpstream
	case scheduler.ChannelKindGemini:
		upstreams = cfg.GeminiUpstream
	case scheduler.ChannelKindChat:
		upstreams = cfg.ChatUpstream
	case scheduler.ChannelKindImages:
		upstreams = cfg.ImagesUpstream
	case scheduler.ChannelKindVectors:
		upstreams = cfg.VectorsUpstream
	default:
		upstreams = cfg.Upstream
	}

	counts := make(map[string]int)
	for i := range upstreams {
		upstream := &upstreams[i]
		normalizedServiceType := scheduler.NormalizedMetricsServiceType(kind, upstream.ServiceType)
		for _, metricsKey := range channelMetricsKeys(upstream, normalizedServiceType) {
			counts[metricsKey]++
		}
	}

	shared := make(map[string]bool)
	for metricsKey, count := range counts {
		if count > 1 {
			shared[metricsKey] = true
		}
	}
	return shared
}
