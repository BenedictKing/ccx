package handlers

import (
	"log"
	"time"

	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/gin-gonic/gin"
)

// GetGlobalStatsHistory 获取全局统计历史数据
// GET /api/{messages|responses}/global/stats/history?duration={1h|6h|24h|today|7d|30d}
func GetGlobalStatsHistory(metricsManager *metrics.MetricsManager) gin.HandlerFunc {
	return func(c *gin.Context) {
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

			modelDataPoints := buildModelDataPointsFromAggregatedBuckets(modelBuckets, pointIndexByUnix, dataPoints)
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
		result := metricsManager.GetGlobalHistoricalStatsWithTokens(duration, interval)
		result.Summary.IntervalSeconds = int64(interval.Seconds())
		if durationStr == "today" {
			result.Summary.Duration = "today"
		} else if durationStr == "thisyear" {
			result.Summary.Duration = "thisyear"
		}

		c.JSON(200, result)
	}
}

func buildModelDataPointsFromAggregatedBuckets(buckets []metrics.ModelAggregatedBucket, pointIndexByUnix map[int64]int, dataPoints []metrics.GlobalHistoryDataPoint) map[string][]metrics.ModelHistoryDataPoint {
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
		points[idx] = metrics.ModelHistoryDataPoint{
			Timestamp:           dataPoints[idx].Timestamp,
			RequestCount:        bucket.TotalRequests,
			SuccessCount:        bucket.SuccessCount,
			FailureCount:        bucket.TotalRequests - bucket.SuccessCount,
			InputTokens:         bucket.InputTokens,
			OutputTokens:        bucket.OutputTokens,
			CacheCreationTokens: bucket.CacheCreationTokens,
			CacheReadTokens:     bucket.CacheReadTokens,
			CostUSD:             metrics.CalculateTokenCostUSD(bucket.Model, bucket.InputTokens, bucket.OutputTokens, bucket.CacheCreationTokens, bucket.CacheReadTokens),
		}
	}

	return modelDataPoints
}
