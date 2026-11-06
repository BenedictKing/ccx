package handlers

import (
	"log"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/gin-gonic/gin"
)

// GetGlobalStatsHistory 获取全局统计历史数据
// GET /api/{messages|responses}/global/stats/history?duration={1h|6h|24h|today|7d|30d}
func GetGlobalStatsHistory(metricsManager *metrics.MetricsManager, cfgManager *config.ConfigManager, kind scheduler.ChannelKind) gin.HandlerFunc {
	return func(c *gin.Context) {
		calculateCost := globalCostCalculator(cfgManager.GetConfig(), kind)
		durationStr := c.DefaultQuery("duration", "24h")
		duration, err := parseExtendedDuration(durationStr)
		if err != nil || duration < time.Minute {
			duration = 6 * time.Hour // 回退到默认值
		}
		maxDuration := 366 * 24 * time.Hour
		if duration > maxDuration {
			duration = maxDuration
		}

		interval := selectIntervalForDuration(c.Query("interval"), duration)

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

			buckets, err := store.QueryAggregatedHistory(apiType, since, intervalSec, "", "")
			if err != nil {
				log.Printf("[GlobalStats-History] SQLite 查询失败: %v", err)
				c.JSON(500, gin.H{"error": "查询历史数据失败"})
				return
			}
			modelBuckets, err := store.QueryModelAggregatedHistory(apiType, since, intervalSec, "", "")
			if err != nil {
				log.Printf("[GlobalStats-History] SQLite 模型聚合查询失败: %v", err)
				c.JSON(500, gin.H{"error": "查询模型历史数据失败"})
				return
			}

			// 构建与内存查询兼容的响应格式
			dataPoints := make([]metrics.GlobalHistoryDataPoint, 0, len(buckets))
			pointIndexByUnix := make(map[int64]int, len(buckets))
			var totalReqs, totalSuccess, totalInput, totalOutput, totalCacheCreate, totalCacheRead int64
			var totalCostUSD float64
			for _, b := range buckets {
				var successRate float64
				if b.TotalRequests > 0 {
					successRate = float64(b.SuccessCount) / float64(b.TotalRequests) * 100
				}
				pointIndexByUnix[b.Timestamp.Unix()] = len(dataPoints)
				dataPoints = append(dataPoints, metrics.GlobalHistoryDataPoint{
					Timestamp:           b.Timestamp,
					RequestCount:        b.TotalRequests,
					SuccessCount:        b.SuccessCount,
					FailureCount:        b.TotalRequests - b.SuccessCount,
					SuccessRate:         successRate,
					InputTokens:         b.InputTokens,
					OutputTokens:        b.OutputTokens,
					CacheCreationTokens: b.CacheCreationTokens,
					CacheReadTokens:     b.CacheReadTokens,
				})
				totalReqs += b.TotalRequests
				totalSuccess += b.SuccessCount
				totalInput += b.InputTokens
				totalOutput += b.OutputTokens
				totalCacheCreate += b.CacheCreationTokens
				totalCacheRead += b.CacheReadTokens
			}

			modelDataPoints := buildModelDataPointsFromAggregatedBuckets(modelBuckets, pointIndexByUnix, dataPoints, calculateCost)
			for _, points := range modelDataPoints {
				for i, point := range points {
					dataPoints[i].CostUSD += point.CostUSD
					totalCostUSD += point.CostUSD
				}
			}

			var overallRate float64
			if totalReqs > 0 {
				overallRate = float64(totalSuccess) / float64(totalReqs) * 100
			}

			result := metrics.GlobalStatsHistoryResponse{
				Summary: metrics.GlobalStatsSummary{
					Duration:                 durationStr,
					IntervalSeconds:          intervalSec,
					TotalRequests:            totalReqs,
					TotalSuccess:             totalSuccess,
					TotalFailure:             totalReqs - totalSuccess,
					AvgSuccessRate:           overallRate,
					TotalInputTokens:         totalInput,
					TotalOutputTokens:        totalOutput,
					TotalCacheCreationTokens: totalCacheCreate,
					TotalCacheReadTokens:     totalCacheRead,
					TotalCostUSD:             totalCostUSD,
				},
				DataPoints:      dataPoints,
				ModelDataPoints: modelDataPoints,
			}

			c.JSON(200, result)
			return
		}

		// <=24h 走内存
		result := metricsManager.GetGlobalHistoricalStatsWithTokens(duration, interval, func(metricsKey string, record metrics.RequestRecord) float64 {
			return calculateCost(metricsKey, record.Model, record.InputTokens, record.OutputTokens, record.CacheCreationInputTokens, record.CacheReadInputTokens)
		})
		result.Summary.IntervalSeconds = int64(interval.Seconds())
		if durationStr == "today" {
			result.Summary.Duration = "today"
		} else if durationStr == "thisyear" {
			result.Summary.Duration = "thisyear"
		}

		c.JSON(200, result)
	}
}

func buildModelDataPointsFromAggregatedBuckets(buckets []metrics.ModelAggregatedBucket, pointIndexByUnix map[int64]int, dataPoints []metrics.GlobalHistoryDataPoint, calculateCost func(string, string, int64, int64, int64, int64) float64) map[string][]metrics.ModelHistoryDataPoint {
	if len(buckets) == 0 || len(dataPoints) == 0 {
		return nil
	}

	modelDataPoints := make(map[string][]metrics.ModelHistoryDataPoint)
	for _, bucket := range buckets {
		idx, ok := pointIndexByUnix[bucket.Timestamp.Unix()]
		if !ok || idx < 0 || idx >= len(dataPoints) {
			continue
		}
		points, ok := modelDataPoints[bucket.Model]
		if !ok {
			points = make([]metrics.ModelHistoryDataPoint, len(dataPoints))
			for i, dp := range dataPoints {
				points[i].Timestamp = dp.Timestamp
			}
			modelDataPoints[bucket.Model] = points
		}
		point := &points[idx]
		point.RequestCount += bucket.TotalRequests
		point.SuccessCount += bucket.SuccessCount
		point.FailureCount += bucket.TotalRequests - bucket.SuccessCount
		point.InputTokens += bucket.InputTokens
		point.OutputTokens += bucket.OutputTokens
		point.CacheCreationTokens += bucket.CacheCreationTokens
		point.CacheReadTokens += bucket.CacheReadTokens
		point.CostUSD += calculateCost(bucket.MetricsKey, bucket.Model, bucket.InputTokens, bucket.OutputTokens, bucket.CacheCreationTokens, bucket.CacheReadTokens)
	}

	return modelDataPoints
}

func globalCostCalculator(cfg config.Config, kind scheduler.ChannelKind) func(string, string, int64, int64, int64, int64) float64 {
	upstreams := channelUpstreamsByKind(cfg, kind)
	byMetricsKey := make(map[string]*config.UpstreamConfig)
	for i := range upstreams {
		upstream := &upstreams[i]
		serviceType := scheduler.NormalizedMetricsServiceType(kind, upstream.ServiceType)
		for _, baseURL := range upstream.GetAllBaseURLs() {
			for _, key := range channelStatsAPIKeys(*upstream) {
				for _, identity := range metrics.GenerateMetricsLookupKeys(baseURL, key, serviceType) {
					if previous, exists := byMetricsKey[identity]; exists && previous != upstream {
						byMetricsKey[identity] = nil // 同一指标身份无法区分两个渠道价格。
					} else if !exists {
						byMetricsKey[identity] = upstream
					}
				}
			}
		}
	}
	return func(metricsKey, model string, input, output, cacheCreate, cacheRead int64) float64 {
		return metrics.CalculateTokenCostUSDWithConfig(model, byMetricsKey[metricsKey], cfg.UpstreamModelCapabilities, input, output, cacheCreate, cacheRead)
	}
}
