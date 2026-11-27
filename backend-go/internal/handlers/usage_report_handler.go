package handlers

import (
	"encoding/csv"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/gin-gonic/gin"
)

type usageReportChannel struct {
	ID          string
	Name        string
	APIType     string
	ServiceType string
	Upstream    *config.UpstreamConfig
}

func usageReportChannels(cfg config.Config) []usageReportChannel {
	var result []usageReportChannel
	for _, kind := range []scheduler.ChannelKind{
		scheduler.ChannelKindMessages, scheduler.ChannelKindChat, scheduler.ChannelKindResponses,
		scheduler.ChannelKindGemini, scheduler.ChannelKindImages, scheduler.ChannelKindVectors,
	} {
		upstreams := channelUpstreamsByKind(cfg, kind)
		for i := range upstreams {
			upstream := &upstreams[i]
			result = append(result, usageReportChannel{
				ID: fmt.Sprintf("%s:%d", kind, i), Name: upstream.Name,
				APIType:     string(kind),
				ServiceType: scheduler.NormalizedMetricsServiceType(kind, upstream.ServiceType),
				Upstream:    upstream,
			})
		}
	}
	return result
}

func usageReportAttribution(channels []usageReportChannel) map[string]*usageReportChannel {
	result := make(map[string]*usageReportChannel)
	for i := range channels {
		channel := &channels[i]
		for _, baseURL := range channel.Upstream.GetAllBaseURLs() {
			for _, apiKey := range channelStatsAPIKeys(*channel.Upstream) {
				for _, metricsKey := range metrics.GenerateMetricsLookupKeys(baseURL, apiKey, channel.ServiceType) {
					key := channel.APIType + ":" + metricsKey
					if previous, exists := result[key]; exists && previous != channel {
						result[key] = nil
					} else if !exists {
						result[key] = channel
					}
				}
			}
		}
	}
	return result
}

// GetUsageReportChannels 返回可选择的渠道，不包含凭证和上游地址。
func GetUsageReportChannels(cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		channels := usageReportChannels(cfgManager.GetConfig())
		result := make([]gin.H, 0, len(channels))
		for _, channel := range channels {
			result = append(result, gin.H{"id": channel.ID, "name": channel.Name,
				"apiType": channel.APIType, "serviceType": channel.ServiceType})
		}
		c.JSON(http.StatusOK, result)
	}
}

// GetUsageReportCSV 导出指定本地日历月的使用报表。
func GetUsageReportCSV(store *metrics.SQLiteStore, cfgManager *config.ConfigManager) gin.HandlerFunc {
	return func(c *gin.Context) {
		if store == nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "使用报表需要启用 SQLite 指标持久化"})
			return
		}
		zone := c.DefaultQuery("timezone", "UTC")
		location, err := time.LoadLocation(zone)
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "无效的时区"})
			return
		}
		month := c.DefaultQuery("month", time.Now().In(location).Format("2006-01"))
		parsed, err := time.Parse("2006-01", month)
		if err != nil || parsed.Format("2006-01") != month {
			c.JSON(http.StatusBadRequest, gin.H{"error": "月份应为 YYYY-MM"})
			return
		}
		granularity := c.DefaultQuery("granularity", "hour")
		if granularity != "hour" && granularity != "day" {
			c.JSON(http.StatusBadRequest, gin.H{"error": "时间粒度只能是 hour 或 day"})
			return
		}
		start := time.Date(parsed.Year(), parsed.Month(), 1, 0, 0, 0, 0, location)
		end := start.AddDate(0, 1, 0)
		if start.After(time.Now()) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "不能导出未来月份"})
			return
		}
		cfg := cfgManager.GetConfig()
		channels := usageReportChannels(cfg)
		selectedID := c.DefaultQuery("channel", "all")
		if selectedID != "all" {
			found := false
			for _, channel := range channels {
				if channel.ID == selectedID {
					found = true
					break
				}
			}
			if !found {
				c.JSON(http.StatusBadRequest, gin.H{"error": "渠道不存在"})
				return
			}
		}
		buckets, err := store.QueryUsageBuckets(c.Request.Context(), start, end, location, granularity == "hour")
		if err != nil {
			log.Printf("[Usage-Report] SQLite 查询失败: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "查询使用报表失败"})
			return
		}
		c.Header("Content-Type", "text/csv; charset=utf-8")
		c.Header("Content-Disposition", fmt.Sprintf(`attachment; filename="ccx-usage-%s.csv"`, month))
		c.Writer.WriteHeader(http.StatusOK)
		_, _ = c.Writer.Write([]byte("\xef\xbb\xbf"))
		writer := csv.NewWriter(c.Writer)
		_ = writer.Write([]string{"month", "bucket_start", "timezone", "protocol", "channel", "service_type", "model", "requests", "successes", "failures", "success_rate_pct", "input_tokens", "output_tokens", "cache_creation_tokens", "cache_read_tokens", "avg_rpm", "avg_tpm", "estimated_cost_usd", "attribution"})
		attribution := usageReportAttribution(channels)
		costCalculators := make(map[string]tokenCostCalculator)
		for _, channel := range channels {
			if costCalculators[channel.APIType] == nil {
				costCalculators[channel.APIType] = globalCostCalculator(cfg, scheduler.ChannelKind(channel.APIType))
			}
		}
		for _, bucket := range buckets {
			channel, known := attribution[bucket.APIType+":"+bucket.MetricsKey]
			if selectedID != "all" && (channel == nil || channel.ID != selectedID) {
				continue
			}
			name, serviceType, status := "", "", "unattributed"
			var upstream *config.UpstreamConfig
			if known && channel == nil {
				status = "shared"
			} else if channel != nil {
				name, serviceType, status, upstream = channel.Name, channel.ServiceType, "matched", channel.Upstream
			}
			minutes := 60.0
			if granularity == "day" {
				next := bucket.Timestamp.AddDate(0, 0, 1)
				minutes = next.Sub(bucket.Timestamp).Minutes()
			}
			successRate := 100 * float64(bucket.Successes) / float64(bucket.Requests)
			var cost float64
			if calculateCost := costCalculators[bucket.APIType]; calculateCost != nil {
				cost = calculateCost(bucket.MetricsKey, bucket.Model, bucket.InputTokens, bucket.OutputTokens, bucket.CacheCreationTokens, bucket.CacheReadTokens)
			} else {
				cost = metrics.CalculateTokenCostUSDForChannel(bucket.Model, upstream, cfg.UpstreamModelCapabilities,
					bucket.InputTokens, bucket.OutputTokens, bucket.CacheCreationTokens, bucket.CacheReadTokens)
			}
			row := []string{month, bucket.Timestamp.Format(time.RFC3339), zone, bucket.APIType,
				csvSafeText(name), csvSafeText(serviceType), csvSafeText(bucket.Model),
				strconv.FormatInt(bucket.Requests, 10), strconv.FormatInt(bucket.Successes, 10),
				strconv.FormatInt(bucket.Requests-bucket.Successes, 10), fmt.Sprintf("%.2f", successRate),
				strconv.FormatInt(bucket.InputTokens, 10), strconv.FormatInt(bucket.OutputTokens, 10),
				strconv.FormatInt(bucket.CacheCreationTokens, 10), strconv.FormatInt(bucket.CacheReadTokens, 10),
				fmt.Sprintf("%.4f", float64(bucket.Requests)/minutes),
				fmt.Sprintf("%.4f", float64(bucket.InputTokens+bucket.OutputTokens)/minutes),
				fmt.Sprintf("%.6f", cost), status}
			if err := writer.Write(row); err != nil {
				log.Printf("[Usage-Report] CSV 写入失败: %v", err)
				return
			}
		}
		writer.Flush()
		if err := writer.Error(); err != nil {
			log.Printf("[Usage-Report] CSV 输出失败: %v", err)
		}
	}
}

func csvSafeText(value string) string {
	trimmed := strings.TrimLeft(value, " \t\r\n")
	if len(trimmed) > 0 && strings.ContainsRune("=+-@", rune(trimmed[0])) {
		return "'" + value
	}
	return value
}
