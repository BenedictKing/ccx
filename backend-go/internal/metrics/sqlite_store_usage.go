package metrics

import (
	"context"
	"fmt"
	"sort"
	"time"
)

// UsageBucket 是指定日历月内按协议、指标身份、模型和本地时间桶汇总的请求记录。
type UsageBucket struct {
	APIType             string
	MetricsKey          string
	Model               string
	Timestamp           time.Time
	Requests            int64
	Successes           int64
	InputTokens         int64
	OutputTokens        int64
	CacheCreationTokens int64
	CacheReadTokens     int64
}

// QueryUsageBuckets 使用指定时区划分日历时间桶，正确处理跨月边界和夏令时。
func (s *SQLiteStore) QueryUsageBuckets(ctx context.Context, start, end time.Time, location *time.Location, hourly bool) ([]UsageBucket, error) {
	s.flushBuffer()
	type bucketKey struct {
		apiType, metricsKey, model string
		timestamp                  int64
	}
	grouped := make(map[bucketKey]*UsageBucket)
	for _, apiType := range []string{"messages", "chat", "responses", "gemini", "images", "vectors"} {
		rows, err := s.db.QueryContext(ctx, `
			SELECT metrics_key, model, timestamp, success, input_tokens, output_tokens,
				cache_creation_tokens, cache_read_tokens
			FROM request_records
			WHERE api_type = ? AND timestamp >= ? AND timestamp < ?`, apiType, start.Unix(), end.Unix())
		if err != nil {
			return nil, fmt.Errorf("查询月度使用记录失败: %w", err)
		}
		for rows.Next() {
			var metricsKey, model string
			var timestamp, success, input, output, cacheCreate, cacheRead int64
			if err := rows.Scan(&metricsKey, &model, &timestamp, &success, &input, &output, &cacheCreate, &cacheRead); err != nil {
				rows.Close()
				return nil, fmt.Errorf("扫描月度使用记录失败: %w", err)
			}
			localTime := time.Unix(timestamp, 0).In(location)
			var bucketTime time.Time
			if hourly {
				bucketTime = localTime.Add(-time.Duration(localTime.Minute())*time.Minute - time.Duration(localTime.Second())*time.Second)
			} else {
				bucketTime = time.Date(localTime.Year(), localTime.Month(), localTime.Day(), 0, 0, 0, 0, location)
			}
			key := bucketKey{apiType, metricsKey, model, bucketTime.Unix()}
			bucket := grouped[key]
			if bucket == nil {
				bucket = &UsageBucket{APIType: apiType, MetricsKey: metricsKey, Model: model, Timestamp: bucketTime}
				grouped[key] = bucket
			}
			bucket.Requests++
			bucket.Successes += success
			bucket.InputTokens += input
			bucket.OutputTokens += output
			bucket.CacheCreationTokens += cacheCreate
			bucket.CacheReadTokens += cacheRead
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return nil, fmt.Errorf("读取月度使用记录失败: %w", err)
		}
	}
	result := make([]UsageBucket, 0, len(grouped))
	for _, bucket := range grouped {
		result = append(result, *bucket)
	}
	sort.Slice(result, func(i, j int) bool {
		a, b := result[i], result[j]
		if !a.Timestamp.Equal(b.Timestamp) {
			return a.Timestamp.Before(b.Timestamp)
		}
		if a.APIType != b.APIType {
			return a.APIType < b.APIType
		}
		if a.MetricsKey != b.MetricsKey {
			return a.MetricsKey < b.MetricsKey
		}
		return a.Model < b.Model
	})
	return result, nil
}
