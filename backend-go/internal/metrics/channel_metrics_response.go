package metrics

import (
	"time"
)

// ============ 兼容旧 API 的方法（基于 channelIndex，需要调用方提供 baseURL 和 keys）============

// MetricsResponse API 响应结构
// 使用 omitempty 减少 JSON 体积，0 值字段不输出
// 注意：successRate/errorRate 不使用 omitempty，因为 0% 是有意义的值
type MetricsResponse struct {
	ChannelIndex        int                        `json:"channelIndex"`
	RequestCount        int64                      `json:"requestCount,omitempty"`
	SuccessCount        int64                      `json:"successCount,omitempty"`
	FailureCount        int64                      `json:"failureCount,omitempty"`
	SuccessRate         float64                    `json:"successRate"`
	ErrorRate           float64                    `json:"errorRate"`
	ConsecutiveFailures int64                      `json:"consecutiveFailures,omitempty"`
	ActiveRequests      int64                      `json:"activeRequests,omitempty"` // 进行中请求数
	Latency             int64                      `json:"latency,omitempty"`
	LastSuccessAt       *string                    `json:"lastSuccessAt,omitempty"`
	LastFailureAt       *string                    `json:"lastFailureAt,omitempty"`
	CircuitBrokenAt     *string                    `json:"circuitBrokenAt,omitempty"`
	CircuitState        string                     `json:"circuitState,omitempty"`
	NextRetryAt         *string                    `json:"nextRetryAt,omitempty"`
	HalfOpenSuccesses   int                        `json:"halfOpenSuccesses,omitempty"`
	BreakerFailureRate  float64                    `json:"breakerFailureRate,omitempty"`
	TimeWindows         map[string]TimeWindowStats `json:"timeWindows,omitempty"`
	KeyMetrics          []*KeyMetricsResponse      `json:"keyMetrics,omitempty"` // 各 Key 的详细指标
}

// KeyMetricsResponse 单个 Key 的 API 响应
// 使用 omitempty 减少 JSON 体积，0 值字段不输出
// 注意：successRate 不使用 omitempty，因为 0% 是有意义的值
type KeyMetricsResponse struct {
	KeyMask             string  `json:"keyMask"`
	RequestCount        int64   `json:"requestCount,omitempty"`
	SuccessCount        int64   `json:"successCount,omitempty"`
	FailureCount        int64   `json:"failureCount,omitempty"`
	SuccessRate         float64 `json:"successRate"`
	ConsecutiveFailures int64   `json:"consecutiveFailures,omitempty"`
	CircuitBroken       bool    `json:"circuitBroken,omitempty"`
	CircuitState        string  `json:"circuitState,omitempty"`
	NextRetryAt         *string `json:"nextRetryAt,omitempty"`
	HalfOpenSuccesses   int     `json:"halfOpenSuccesses,omitempty"`
	BreakerFailureRate  float64 `json:"breakerFailureRate,omitempty"`
}

// ToResponseMultiURL 转换为 API 响应格式（支持多 BaseURL 聚合）
// baseURLs: 渠道配置的所有 BaseURL（用于多端点 failover 场景）
// historicalKeys: 历史 API Key（用于统计聚合，只计入总数不显示在 KeyMetrics 中）
func (m *MetricsManager) ToResponseMultiURL(channelIndex int, baseURLs []string, activeKeys []string, serviceType string, latency int64, historicalKeys ...[]string) *MetricsResponse {
	if len(baseURLs) == 0 {
		return &MetricsResponse{
			ChannelIndex: channelIndex,
			Latency:      latency,
			SuccessRate:  100,
			ErrorRate:    0,
		}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	resp := &MetricsResponse{
		ChannelIndex: channelIndex,
		Latency:      latency,
	}

	statsKeys := append([]string{}, activeKeys...)
	if len(historicalKeys) > 0 {
		statsKeys = append(statsKeys, historicalKeys[0]...)
	}
	if len(statsKeys) == 0 {
		resp.SuccessRate = 100
		resp.ErrorRate = 0
		return resp
	}

	type keyAggregation struct {
		keyMask             string
		requestCount        int64
		successCount        int64
		failureCount        int64
		consecutiveFailures int64
		circuitBroken       bool
		circuitState        CircuitState
		nextRetryAt         *time.Time
		halfOpenSuccesses   int
		breakerFailureRate  float64
	}
	keyAggMap := make(map[string]*keyAggregation)
	seenMetrics := make(map[string]*KeyMetrics)
	metricsToAPIKey := make(map[string]string)

	for _, apiKey := range activeKeys {
		for _, metrics := range m.getIdentityMetricsByMultiURLLocked(baseURLs, apiKey, serviceType) {
			seenMetrics[metrics.MetricsKey] = metrics
			if _, exists := metricsToAPIKey[metrics.MetricsKey]; !exists {
				metricsToAPIKey[metrics.MetricsKey] = apiKey
			}
		}
	}

	now := time.Now()
	var latestSuccess, latestFailure, latestCircuitBroken, latestNextRetry *time.Time
	var totalResults []bool
	var maxConsecutiveFailures int64
	var maxHalfOpenSuccesses int
	channelState := m.channelCircuitStateMultiURLLocked(baseURLs, activeKeys, serviceType, now)

	for _, metrics := range seenMetrics {
		m.advanceCircuitStateIfDueLocked(metrics, now)
		m.refreshBreakerWindowsLocked(metrics, now)
		resp.RequestCount += metrics.RequestCount
		resp.SuccessCount += metrics.SuccessCount
		resp.FailureCount += metrics.FailureCount
		resp.ActiveRequests += metrics.ActiveRequests
		if metrics.ConsecutiveFailures > maxConsecutiveFailures {
			maxConsecutiveFailures = metrics.ConsecutiveFailures
		}
		if metrics.HalfOpenSuccesses > maxHalfOpenSuccesses {
			maxHalfOpenSuccesses = metrics.HalfOpenSuccesses
		}
		totalResults = append(totalResults, metrics.breakerResults...)

		if metrics.LastSuccessAt != nil && (latestSuccess == nil || metrics.LastSuccessAt.After(*latestSuccess)) {
			latestSuccess = metrics.LastSuccessAt
		}
		if metrics.LastFailureAt != nil && (latestFailure == nil || metrics.LastFailureAt.After(*latestFailure)) {
			latestFailure = metrics.LastFailureAt
		}
		if metrics.CircuitBrokenAt != nil && (latestCircuitBroken == nil || metrics.CircuitBrokenAt.After(*latestCircuitBroken)) {
			latestCircuitBroken = metrics.CircuitBrokenAt
		}
		if metrics.NextRetryAt != nil && (latestNextRetry == nil || metrics.NextRetryAt.After(*latestNextRetry)) {
			latestNextRetry = metrics.NextRetryAt
		}

		breakerFailureRate := m.calculateKeyBreakerFailureRateInternal(metrics) * 100
		apiKey := metricsToAPIKey[metrics.MetricsKey]
		if agg, ok := keyAggMap[apiKey]; ok {
			agg.requestCount += metrics.RequestCount
			agg.successCount += metrics.SuccessCount
			agg.failureCount += metrics.FailureCount
			if metrics.ConsecutiveFailures > agg.consecutiveFailures {
				agg.consecutiveFailures = metrics.ConsecutiveFailures
			}
			if metrics.CircuitBrokenAt != nil {
				agg.circuitBroken = true
			}
			if metrics.CircuitState > agg.circuitState {
				agg.circuitState = metrics.CircuitState
			}
			if metrics.NextRetryAt != nil && (agg.nextRetryAt == nil || metrics.NextRetryAt.After(*agg.nextRetryAt)) {
				t := *metrics.NextRetryAt
				agg.nextRetryAt = &t
			}
			if metrics.HalfOpenSuccesses > agg.halfOpenSuccesses {
				agg.halfOpenSuccesses = metrics.HalfOpenSuccesses
			}
			if breakerFailureRate > agg.breakerFailureRate {
				agg.breakerFailureRate = breakerFailureRate
			}
		} else {
			var nextRetryCopy *time.Time
			if metrics.NextRetryAt != nil {
				t := *metrics.NextRetryAt
				nextRetryCopy = &t
			}
			keyAggMap[apiKey] = &keyAggregation{
				keyMask:             metrics.KeyMask,
				requestCount:        metrics.RequestCount,
				successCount:        metrics.SuccessCount,
				failureCount:        metrics.FailureCount,
				consecutiveFailures: metrics.ConsecutiveFailures,
				circuitBroken:       metrics.CircuitBrokenAt != nil,
				circuitState:        metrics.CircuitState,
				nextRetryAt:         nextRetryCopy,
				halfOpenSuccesses:   metrics.HalfOpenSuccesses,
				breakerFailureRate:  breakerFailureRate,
			}
		}
	}

	if len(historicalKeys) > 0 && len(historicalKeys[0]) > 0 {
		seenHistorical := make(map[string]struct{})
		for _, apiKey := range historicalKeys[0] {
			for _, metrics := range m.getIdentityMetricsByMultiURLLocked(baseURLs, apiKey, serviceType) {
				if _, ok := seenHistorical[metrics.MetricsKey]; ok {
					continue
				}
				seenHistorical[metrics.MetricsKey] = struct{}{}
				m.refreshBreakerWindowsLocked(metrics, now)
				resp.RequestCount += metrics.RequestCount
				resp.SuccessCount += metrics.SuccessCount
				resp.FailureCount += metrics.FailureCount
				if metrics.LastSuccessAt != nil && (latestSuccess == nil || metrics.LastSuccessAt.After(*latestSuccess)) {
					latestSuccess = metrics.LastSuccessAt
				}
				if metrics.LastFailureAt != nil && (latestFailure == nil || metrics.LastFailureAt.After(*latestFailure)) {
					latestFailure = metrics.LastFailureAt
				}
			}
		}
	}

	var keyResponses []*KeyMetricsResponse
	for _, apiKey := range activeKeys {
		if agg, ok := keyAggMap[apiKey]; ok {
			keySuccessRate := float64(100)
			if agg.requestCount > 0 {
				keySuccessRate = float64(agg.successCount) / float64(agg.requestCount) * 100
			}
			var nextRetryText *string
			if agg.nextRetryAt != nil {
				t := agg.nextRetryAt.Format(time.RFC3339)
				nextRetryText = &t
			}
			keyResponses = append(keyResponses, &KeyMetricsResponse{
				KeyMask:             agg.keyMask,
				RequestCount:        agg.requestCount,
				SuccessCount:        agg.successCount,
				FailureCount:        agg.failureCount,
				SuccessRate:         keySuccessRate,
				ConsecutiveFailures: agg.consecutiveFailures,
				CircuitBroken:       agg.circuitBroken,
				CircuitState:        agg.circuitState.String(),
				NextRetryAt:         nextRetryText,
				HalfOpenSuccesses:   agg.halfOpenSuccesses,
				BreakerFailureRate:  agg.breakerFailureRate,
			})
		}
	}

	resp.ConsecutiveFailures = maxConsecutiveFailures
	resp.HalfOpenSuccesses = maxHalfOpenSuccesses
	resp.CircuitState = channelState.String()

	if resp.RequestCount > 0 {
		resp.SuccessRate = float64(resp.SuccessCount) / float64(resp.RequestCount) * 100
		resp.ErrorRate = float64(resp.FailureCount) / float64(resp.RequestCount) * 100
	} else {
		resp.SuccessRate = 100
		resp.ErrorRate = 0
	}

	if len(totalResults) > 0 {
		failures := 0
		for _, success := range totalResults {
			if !success {
				failures++
			}
		}
		failureRate := float64(failures) / float64(len(totalResults))
		resp.BreakerFailureRate = failureRate * 100
	} else {
		resp.BreakerFailureRate = 0
	}

	if latestSuccess != nil {
		t := latestSuccess.Format(time.RFC3339)
		resp.LastSuccessAt = &t
	}
	if latestFailure != nil {
		t := latestFailure.Format(time.RFC3339)
		resp.LastFailureAt = &t
	}
	if latestCircuitBroken != nil {
		t := latestCircuitBroken.Format(time.RFC3339)
		resp.CircuitBrokenAt = &t
	}
	if latestNextRetry != nil {
		t := latestNextRetry.Format(time.RFC3339)
		resp.NextRetryAt = &t
	}

	resp.KeyMetrics = keyResponses
	resp.TimeWindows = m.calculateAggregatedTimeWindowsMultiURL(baseURLs, statsKeys, serviceType)

	return resp
}

// ToResponse 转换为 API 响应格式（需要提供 baseURL 和 activeKeys）
func (m *MetricsManager) ToResponse(channelIndex int, baseURL string, activeKeys []string, serviceType string, latency int64) *MetricsResponse {
	return m.ToResponseMultiURL(channelIndex, []string{baseURL}, activeKeys, serviceType, latency)
}

// calculateAggregatedTimeWindowsInternal 计算聚合的时间窗口统计（内部方法，调用前需持有锁）
func (m *MetricsManager) calculateAggregatedTimeWindowsInternal(baseURL string, activeKeys []string, serviceType string) map[string]TimeWindowStats {
	windows := map[string]time.Duration{
		"15m": 15 * time.Minute,
		"1h":  1 * time.Hour,
		"6h":  6 * time.Hour,
		"24h": 24 * time.Hour,
	}

	result := make(map[string]TimeWindowStats)
	now := time.Now()

	for label, duration := range windows {
		cutoff := now.Add(-duration)
		var requestCount, successCount, failureCount int64
		var inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens int64

		for _, apiKey := range activeKeys {
			for _, metrics := range m.getMetricsVariantsLocked(baseURL, apiKey, serviceType) {
				for _, record := range metrics.requestHistory {
					if record.Timestamp.After(cutoff) {
						requestCount++
						if record.Success {
							successCount++
						} else {
							failureCount++
						}
						inputTokens += record.InputTokens
						outputTokens += record.OutputTokens
						cacheCreationTokens += record.CacheCreationInputTokens
						cacheReadTokens += record.CacheReadInputTokens
					}
				}
			}
		}

		successRate := float64(100)
		if requestCount > 0 {
			successRate = float64(successCount) / float64(requestCount) * 100
		}

		cacheHitRate := float64(0)
		denom := cacheReadTokens + inputTokens
		if denom > 0 {
			cacheHitRate = float64(cacheReadTokens) / float64(denom) * 100
		}

		result[label] = TimeWindowStats{
			RequestCount:        requestCount,
			SuccessCount:        successCount,
			FailureCount:        failureCount,
			SuccessRate:         successRate,
			InputTokens:         inputTokens,
			OutputTokens:        outputTokens,
			CacheCreationTokens: cacheCreationTokens,
			CacheReadTokens:     cacheReadTokens,
			CacheHitRate:        cacheHitRate,
		}
	}

	return result
}

// calculateAggregatedTimeWindowsMultiURL 计算聚合的时间窗口统计（多 URL 版本，内部方法，调用前需持有锁）
func (m *MetricsManager) calculateAggregatedTimeWindowsMultiURL(baseURLs []string, activeKeys []string, serviceType string) map[string]TimeWindowStats {
	windows := map[string]time.Duration{
		"15m": 15 * time.Minute,
		"1h":  1 * time.Hour,
		"6h":  6 * time.Hour,
		"24h": 24 * time.Hour,
	}

	result := make(map[string]TimeWindowStats)
	now := time.Now()

	for label, duration := range windows {
		cutoff := now.Add(-duration)
		var requestCount, successCount, failureCount int64
		var inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens int64

		// 遍历所有 BaseURL 和 Key 的组合
		for _, metrics := range m.getIdentityMetricsByMultiURLAndKeysLocked(baseURLs, activeKeys, serviceType) {
			for _, record := range metrics.requestHistory {
				if record.Timestamp.After(cutoff) {
					requestCount++
					if record.Success {
						successCount++
					} else {
						failureCount++
					}
					inputTokens += record.InputTokens
					outputTokens += record.OutputTokens
					cacheCreationTokens += record.CacheCreationInputTokens
					cacheReadTokens += record.CacheReadInputTokens
				}
			}
		}

		successRate := float64(100)
		if requestCount > 0 {
			successRate = float64(successCount) / float64(requestCount) * 100
		}

		cacheHitRate := float64(0)
		denom := cacheReadTokens + inputTokens
		if denom > 0 {
			cacheHitRate = float64(cacheReadTokens) / float64(denom) * 100
		}

		result[label] = TimeWindowStats{
			RequestCount:        requestCount,
			SuccessCount:        successCount,
			FailureCount:        failureCount,
			SuccessRate:         successRate,
			InputTokens:         inputTokens,
			OutputTokens:        outputTokens,
			CacheCreationTokens: cacheCreationTokens,
			CacheReadTokens:     cacheReadTokens,
			CacheHitRate:        cacheHitRate,
		}
	}

	return result
}

// ============ 历史数据查询方法（用于图表可视化）============

// HistoryDataPoint 历史数据点（用于时间序列图表）
type HistoryDataPoint struct {
	Timestamp           time.Time `json:"timestamp"`
	RequestCount        int64     `json:"requestCount"`
	SuccessCount        int64     `json:"successCount"`
	FailureCount        int64     `json:"failureCount"`
	SuccessRate         float64   `json:"successRate"`
	InputTokens         int64     `json:"inputTokens"`
	OutputTokens        int64     `json:"outputTokens"`
	CacheCreationTokens int64     `json:"cacheCreationTokens"`
	CacheReadTokens     int64     `json:"cacheReadTokens"`
}

// KeyHistoryDataPoint Key 级别历史数据点（包含 Token 和 Cache 数据）
type KeyHistoryDataPoint struct {
	Timestamp                time.Time `json:"timestamp"`
	RequestCount             int64     `json:"requestCount"`
	SuccessCount             int64     `json:"successCount"`
	FailureCount             int64     `json:"failureCount"`
	SuccessRate              float64   `json:"successRate"`
	InputTokens              int64     `json:"inputTokens"`
	OutputTokens             int64     `json:"outputTokens"`
	CacheCreationInputTokens int64     `json:"cacheCreationTokens"`
	CacheReadInputTokens     int64     `json:"cacheReadTokens"`
	CostUSD                  float64   `json:"costUSD"`
}

// GetHistoricalStats 获取历史统计数据（按时间间隔聚合）
// duration: 查询时间范围 (如 1h, 6h, 24h)
// interval: 聚合间隔 (如 5m, 15m, 1h)
func (m *MetricsManager) GetHistoricalStats(baseURL string, activeKeys []string, serviceType string, duration, interval time.Duration) []HistoryDataPoint {
	// 参数验证
	if interval <= 0 || duration <= 0 {
		return []HistoryDataPoint{}
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	now := time.Now()
	// 时间对齐到 interval 边界
	startTime := now.Add(-duration).Truncate(interval)
	// endTime 延伸一个 interval，确保当前时间段的请求也被包含
	endTime := now.Truncate(interval).Add(interval)

	// 计算需要多少个数据点（+1 用于包含延伸的当前时间段）
	numPoints := int(duration / interval)
	if numPoints <= 0 {
		numPoints = 1
	}
	numPoints++ // 额外的一个桶用于当前时间段

	// 使用 map 按时间分桶，优化性能：O(records) 而不是 O(records * numPoints)
	buckets := make(map[int64]*bucketData)
	for i := 0; i < numPoints; i++ {
		buckets[int64(i)] = &bucketData{}
	}

	// 收集所有相关 Key 的请求历史并放入对应桶
	for _, apiKey := range activeKeys {
		metrics := m.getIdentityMetricsLocked(baseURL, apiKey, serviceType)
		if metrics == nil {
			continue
		}
		for _, record := range metrics.requestHistory {
			if !record.Timestamp.Before(startTime) && record.Timestamp.Before(endTime) {
				offset := int64(record.Timestamp.Sub(startTime) / interval)
				if offset >= 0 && offset < int64(numPoints) {
					b := buckets[offset]
					b.requestCount++
					if record.Success {
						b.successCount++
					} else {
						b.failureCount++
					}
					b.inputTokens += record.InputTokens
					b.outputTokens += record.OutputTokens
					b.cacheCreationTokens += record.CacheCreationInputTokens
					b.cacheReadTokens += record.CacheReadInputTokens
				}
			}
		}
	}

	// 构建结果
	result := make([]HistoryDataPoint, numPoints)
	for i := 0; i < numPoints; i++ {
		b := buckets[int64(i)]
		// 空桶成功率默认为 0，避免误导（100% 暗示完美成功）
		successRate := float64(0)
		if b.requestCount > 0 {
			successRate = float64(b.successCount) / float64(b.requestCount) * 100
		}
		result[i] = HistoryDataPoint{
			Timestamp:           startTime.Add(time.Duration(i) * interval),
			RequestCount:        b.requestCount,
			SuccessCount:        b.successCount,
			FailureCount:        b.failureCount,
			SuccessRate:         successRate,
			InputTokens:         b.inputTokens,
			OutputTokens:        b.outputTokens,
			CacheCreationTokens: b.cacheCreationTokens,
			CacheReadTokens:     b.cacheReadTokens,
		}
	}

	return result
}

// GetHistoricalStatsMultiURL 获取多 URL 聚合的历史统计数据
func (m *MetricsManager) GetHistoricalStatsMultiURL(baseURLs []string, activeKeys []string, serviceType string, duration, interval time.Duration) []HistoryDataPoint {
	// 参数验证
	if interval <= 0 || duration <= 0 || len(baseURLs) == 0 {
		return []HistoryDataPoint{}
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	now := time.Now()
	// 时间对齐到 interval 边界
	startTime := now.Add(-duration).Truncate(interval)
	// endTime 延伸一个 interval，确保当前时间段的请求也被包含
	endTime := now.Truncate(interval).Add(interval)

	// 计算需要多少个数据点（+1 用于包含延伸的当前时间段）
	numPoints := int(duration / interval)
	if numPoints <= 0 {
		numPoints = 1
	}
	numPoints++ // 额外的一个桶用于当前时间段

	// 使用 map 按时间分桶，优化性能：O(records) 而不是 O(records * numPoints)
	buckets := make(map[int64]*bucketData)
	for i := 0; i < numPoints; i++ {
		buckets[int64(i)] = &bucketData{}
	}

	// 收集所有 BaseURL 和 Key 组合的请求历史并放入对应桶
	for _, metrics := range m.getIdentityMetricsByMultiURLAndKeysLocked(baseURLs, activeKeys, serviceType) {
		for _, record := range metrics.requestHistory {
			if !record.Timestamp.Before(startTime) && record.Timestamp.Before(endTime) {
				offset := int64(record.Timestamp.Sub(startTime) / interval)
				if offset >= 0 && offset < int64(numPoints) {
					b := buckets[offset]
					b.requestCount++
					if record.Success {
						b.successCount++
					} else {
						b.failureCount++
					}
					b.inputTokens += record.InputTokens
					b.outputTokens += record.OutputTokens
					b.cacheCreationTokens += record.CacheCreationInputTokens
					b.cacheReadTokens += record.CacheReadInputTokens
				}
			}
		}
	}

	// 构建结果
	result := make([]HistoryDataPoint, numPoints)
	for i := 0; i < numPoints; i++ {
		b := buckets[int64(i)]
		// 空桶成功率默认为 0，避免误导（100% 暗示完美成功）
		successRate := float64(0)
		if b.requestCount > 0 {
			successRate = float64(b.successCount) / float64(b.requestCount) * 100
		}
		result[i] = HistoryDataPoint{
			Timestamp:           startTime.Add(time.Duration(i) * interval),
			RequestCount:        b.requestCount,
			SuccessCount:        b.successCount,
			FailureCount:        b.failureCount,
			SuccessRate:         successRate,
			InputTokens:         b.inputTokens,
			OutputTokens:        b.outputTokens,
			CacheCreationTokens: b.cacheCreationTokens,
			CacheReadTokens:     b.cacheReadTokens,
		}
	}

	return result
}

// bucketData 用于时间分桶的辅助结构
type bucketData struct {
	requestCount        int64
	successCount        int64
	failureCount        int64
	inputTokens         int64
	outputTokens        int64
	cacheCreationTokens int64
	cacheReadTokens     int64
}

func (m *MetricsManager) GetAllKeysHistoricalStats(duration, interval time.Duration) []HistoryDataPoint {
	// 参数验证
	if interval <= 0 || duration <= 0 {
		return []HistoryDataPoint{}
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	now := time.Now()
	// 时间对齐到 interval 边界
	startTime := now.Add(-duration).Truncate(interval)
	// endTime 延伸一个 interval，确保当前时间段的请求也被包含
	endTime := now.Truncate(interval).Add(interval)

	numPoints := int(duration / interval)
	if numPoints <= 0 {
		numPoints = 1
	}
	numPoints++ // 额外的一个桶用于当前时间段

	// 使用 map 按时间分桶，优化性能
	buckets := make(map[int64]*bucketData)
	for i := 0; i < numPoints; i++ {
		buckets[int64(i)] = &bucketData{}
	}

	// 收集所有 Key 的请求历史并放入对应桶
	for _, metrics := range m.keyMetrics {
		for _, record := range metrics.requestHistory {
			// 使用 [startTime, endTime) 的区间，避免 endTime 处 offset 越界
			if !record.Timestamp.Before(startTime) && record.Timestamp.Before(endTime) {
				offset := int64(record.Timestamp.Sub(startTime) / interval)
				if offset >= 0 && offset < int64(numPoints) {
					b := buckets[offset]
					b.requestCount++
					if record.Success {
						b.successCount++
					} else {
						b.failureCount++
					}
					b.inputTokens += record.InputTokens
					b.outputTokens += record.OutputTokens
					b.cacheCreationTokens += record.CacheCreationInputTokens
					b.cacheReadTokens += record.CacheReadInputTokens
				}
			}
		}
	}

	// 构建结果
	result := make([]HistoryDataPoint, numPoints)
	for i := 0; i < numPoints; i++ {
		b := buckets[int64(i)]
		// 空桶成功率默认为 0，避免误导（100% 暗示完美成功）
		successRate := float64(0)
		if b.requestCount > 0 {
			successRate = float64(b.successCount) / float64(b.requestCount) * 100
		}
		result[i] = HistoryDataPoint{
			Timestamp:           startTime.Add(time.Duration(i) * interval),
			RequestCount:        b.requestCount,
			SuccessCount:        b.successCount,
			FailureCount:        b.failureCount,
			SuccessRate:         successRate,
			InputTokens:         b.inputTokens,
			OutputTokens:        b.outputTokens,
			CacheCreationTokens: b.cacheCreationTokens,
			CacheReadTokens:     b.cacheReadTokens,
		}
	}

	return result
}

// GetKeyHistoricalStats 获取单个 Key 的历史统计数据（包含 Token 和 Cache 数据）
func (m *MetricsManager) GetKeyHistoricalStats(baseURL, apiKey, serviceType string, duration, interval time.Duration) []KeyHistoryDataPoint {
	// 参数验证
	if interval <= 0 || duration <= 0 {
		return []KeyHistoryDataPoint{}
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	now := time.Now()
	// 时间对齐到 interval 边界
	startTime := now.Add(-duration).Truncate(interval)
	// endTime 延伸一个 interval，确保当前时间段的请求也被包含
	endTime := now.Truncate(interval).Add(interval)

	numPoints := int(duration / interval)
	if numPoints <= 0 {
		numPoints = 1
	}
	numPoints++ // 额外的一个桶用于当前时间段

	// 使用 map 按时间分桶
	buckets := make(map[int64]*keyBucketData)
	for i := 0; i < numPoints; i++ {
		buckets[int64(i)] = &keyBucketData{}
	}

	metrics := m.getIdentityMetricsLocked(baseURL, apiKey, serviceType)
	if metrics == nil {
		// Key 不存在，返回空数据点
		result := make([]KeyHistoryDataPoint, numPoints)
		for i := 0; i < numPoints; i++ {
			result[i] = KeyHistoryDataPoint{
				Timestamp: startTime.Add(time.Duration(i+1) * interval),
			}
		}
		return result
	}

	// 收集该 Key 的请求历史并放入对应桶
	for _, record := range metrics.requestHistory {
		if record.Timestamp.After(startTime) && record.Timestamp.Before(endTime) {
			offset := int64(record.Timestamp.Sub(startTime) / interval)
			if offset >= 0 && offset < int64(numPoints) {
				b := buckets[offset]
				b.requestCount++
				if record.Success {
					b.successCount++
				} else {
					b.failureCount++
				}
				b.inputTokens += record.InputTokens
				b.outputTokens += record.OutputTokens
				b.cacheCreationTokens += record.CacheCreationInputTokens
				b.cacheReadTokens += record.CacheReadInputTokens
			}
		}
	}

	// 构建结果
	result := make([]KeyHistoryDataPoint, numPoints)
	for i := 0; i < numPoints; i++ {
		b := buckets[int64(i)]
		// 空桶成功率默认为 0，避免误导（100% 暗示完美成功）
		successRate := float64(0)
		if b.requestCount > 0 {
			successRate = float64(b.successCount) / float64(b.requestCount) * 100
		}
		result[i] = KeyHistoryDataPoint{
			Timestamp:                startTime.Add(time.Duration(i+1) * interval),
			RequestCount:             b.requestCount,
			SuccessCount:             b.successCount,
			FailureCount:             b.failureCount,
			SuccessRate:              successRate,
			InputTokens:              b.inputTokens,
			OutputTokens:             b.outputTokens,
			CacheCreationInputTokens: b.cacheCreationTokens,
			CacheReadInputTokens:     b.cacheReadTokens,
		}
	}

	return result
}

// GetKeyHistoricalStatsMultiURL 获取单个 Key 的多 URL 聚合历史统计
func (m *MetricsManager) GetKeyHistoricalStatsMultiURL(baseURLs []string, apiKey, serviceType string, duration, interval time.Duration) []KeyHistoryDataPoint {
	// 参数验证
	if interval <= 0 || duration <= 0 || len(baseURLs) == 0 {
		return []KeyHistoryDataPoint{}
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	now := time.Now()
	// 时间对齐到 interval 边界
	startTime := now.Add(-duration).Truncate(interval)
	// endTime 延伸一个 interval，确保当前时间段的请求也被包含
	endTime := now.Truncate(interval).Add(interval)

	numPoints := int(duration / interval)
	if numPoints <= 0 {
		numPoints = 1
	}
	numPoints++ // 额外的一个桶用于当前时间段

	// 使用 map 按时间分桶
	buckets := make(map[int64]*keyBucketData)
	for i := 0; i < numPoints; i++ {
		buckets[int64(i)] = &keyBucketData{}
	}

	// 遍历所有 BaseURL 聚合同一 Key 的历史数据
	hasData := false
	for _, metrics := range m.getIdentityMetricsByMultiURLLocked(baseURLs, apiKey, serviceType) {
		hasData = true

		for _, record := range metrics.requestHistory {
			if record.Timestamp.After(startTime) && record.Timestamp.Before(endTime) {
				offset := int64(record.Timestamp.Sub(startTime) / interval)
				if offset >= 0 && offset < int64(numPoints) {
					b := buckets[offset]
					b.requestCount++
					if record.Success {
						b.successCount++
					} else {
						b.failureCount++
					}
					b.inputTokens += record.InputTokens
					b.outputTokens += record.OutputTokens
					b.cacheCreationTokens += record.CacheCreationInputTokens
					b.cacheReadTokens += record.CacheReadInputTokens
				}
			}
		}
	}

	// 如果没有任何数据，返回空数据点
	if !hasData {
		result := make([]KeyHistoryDataPoint, numPoints)
		for i := 0; i < numPoints; i++ {
			result[i] = KeyHistoryDataPoint{
				Timestamp: startTime.Add(time.Duration(i+1) * interval),
			}
		}
		return result
	}

	// 构建结果
	result := make([]KeyHistoryDataPoint, numPoints)
	for i := 0; i < numPoints; i++ {
		b := buckets[int64(i)]
		// 空桶成功率默认为 0，避免误导（100% 暗示完美成功）
		successRate := float64(0)
		if b.requestCount > 0 {
			successRate = float64(b.successCount) / float64(b.requestCount) * 100
		}
		result[i] = KeyHistoryDataPoint{
			Timestamp:                startTime.Add(time.Duration(i+1) * interval),
			RequestCount:             b.requestCount,
			SuccessCount:             b.successCount,
			FailureCount:             b.failureCount,
			SuccessRate:              successRate,
			InputTokens:              b.inputTokens,
			OutputTokens:             b.outputTokens,
			CacheCreationInputTokens: b.cacheCreationTokens,
			CacheReadInputTokens:     b.cacheReadTokens,
		}
	}

	return result
}

// KeyModelHistoryDataPoint Key+Model 组合的历史数据点
type KeyModelHistoryDataPoint struct {
	Timestamp                time.Time `json:"timestamp"`
	RequestCount             int64     `json:"requestCount"`
	SuccessCount             int64     `json:"successCount"`
	FailureCount             int64     `json:"failureCount"`
	InputTokens              int64     `json:"inputTokens"`
	OutputTokens             int64     `json:"outputTokens"`
	CacheCreationInputTokens int64     `json:"cacheCreationTokens"`
	CacheReadInputTokens     int64     `json:"cacheReadTokens"`
	CostUSD                  float64   `json:"costUSD"`
}

// GetKeyModelHistoricalStatsMultiURL 获取单个 Key 按模型分组的历史数据
func (m *MetricsManager) GetKeyModelHistoricalStatsMultiURL(baseURLs []string, apiKey, serviceType string, duration, interval time.Duration) map[string][]KeyModelHistoryDataPoint {
	if interval <= 0 || duration <= 0 || len(baseURLs) == 0 {
		return nil
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	now := time.Now()
	startTime := now.Add(-duration).Truncate(interval)
	endTime := now.Truncate(interval).Add(interval)
	numPoints := int(duration/interval) + 1

	// 按模型分组的桶: model -> bucketIndex -> data
	modelBuckets := make(map[string]map[int64]*keyBucketData)

	for _, metrics := range m.getIdentityMetricsByMultiURLLocked(baseURLs, apiKey, serviceType) {
		for _, record := range metrics.requestHistory {
			if record.Timestamp.After(startTime) && record.Timestamp.Before(endTime) {
				offset := int64(record.Timestamp.Sub(startTime) / interval)
				if offset >= 0 && offset < int64(numPoints) {
					model := record.Model
					if model == "" {
						model = "unknown"
					}
					if modelBuckets[model] == nil {
						modelBuckets[model] = make(map[int64]*keyBucketData)
						for i := 0; i < numPoints; i++ {
							modelBuckets[model][int64(i)] = &keyBucketData{}
						}
					}
					b := modelBuckets[model][offset]
					b.requestCount++
					if record.Success {
						b.successCount++
					} else {
						b.failureCount++
					}
					b.inputTokens += record.InputTokens
					b.outputTokens += record.OutputTokens
					b.cacheCreationTokens += record.CacheCreationInputTokens
					b.cacheReadTokens += record.CacheReadInputTokens
				}
			}
		}
	}

	// 构建结果
	result := make(map[string][]KeyModelHistoryDataPoint)
	for model, buckets := range modelBuckets {
		points := make([]KeyModelHistoryDataPoint, numPoints)
		for i := 0; i < numPoints; i++ {
			b := buckets[int64(i)]
			points[i] = KeyModelHistoryDataPoint{
				Timestamp:                startTime.Add(time.Duration(i+1) * interval),
				RequestCount:             b.requestCount,
				SuccessCount:             b.successCount,
				FailureCount:             b.failureCount,
				InputTokens:              b.inputTokens,
				OutputTokens:             b.outputTokens,
				CacheCreationInputTokens: b.cacheCreationTokens,
				CacheReadInputTokens:     b.cacheReadTokens,
				CostUSD:                  CalculateTokenCostUSD(model, b.inputTokens, b.outputTokens, b.cacheCreationTokens, b.cacheReadTokens),
			}
		}
		result[model] = points
	}

	return result
}

// keyBucketData Key 级别时间分桶的辅助结构（包含 Token 数据）
type keyBucketData struct {
	requestCount        int64
	successCount        int64
	failureCount        int64
	inputTokens         int64
	outputTokens        int64
	cacheCreationTokens int64
	cacheReadTokens     int64
}

// ============ 全局统计数据结构和方法（用于全局流量统计图表）============

// GlobalHistoryDataPoint 全局历史数据点（含 Token 数据）
type GlobalHistoryDataPoint struct {
	Timestamp           time.Time `json:"timestamp"`
	RequestCount        int64     `json:"requestCount"`
	SuccessCount        int64     `json:"successCount"`
	FailureCount        int64     `json:"failureCount"`
	SuccessRate         float64   `json:"successRate"`
	InputTokens         int64     `json:"inputTokens"`
	OutputTokens        int64     `json:"outputTokens"`
	CacheCreationTokens int64     `json:"cacheCreationTokens"`
	CacheReadTokens     int64     `json:"cacheReadTokens"`
	CostUSD             float64   `json:"costUSD"`
}

// GlobalStatsSummary 全局统计汇总
type GlobalStatsSummary struct {
	TotalRequests            int64   `json:"totalRequests"`
	TotalSuccess             int64   `json:"totalSuccess"`
	TotalFailure             int64   `json:"totalFailure"`
	TotalInputTokens         int64   `json:"totalInputTokens"`
	TotalOutputTokens        int64   `json:"totalOutputTokens"`
	TotalCacheCreationTokens int64   `json:"totalCacheCreationTokens"`
	TotalCacheReadTokens     int64   `json:"totalCacheReadTokens"`
	TotalCostUSD             float64 `json:"totalCostUSD"`
	AvgSuccessRate           float64 `json:"avgSuccessRate"`
	Duration                 string  `json:"duration"`
	IntervalSeconds          int64   `json:"intervalSeconds,omitempty"`
}

// GlobalStatsHistoryResponse 全局统计响应
type GlobalStatsHistoryResponse struct {
	DataPoints      []GlobalHistoryDataPoint           `json:"dataPoints"`
	Summary         GlobalStatsSummary                 `json:"summary"`
	ModelDataPoints map[string][]ModelHistoryDataPoint `json:"modelDataPoints,omitempty"`
}

// GetGlobalHistoricalStatsWithTokens 获取全局历史统计（包含 Token 数据）
// 聚合所有 Key 的数据，按时间间隔分桶
func (m *MetricsManager) GetGlobalHistoricalStatsWithTokens(duration, interval time.Duration) GlobalStatsHistoryResponse {
	// 参数验证
	if interval <= 0 || duration <= 0 {
		return GlobalStatsHistoryResponse{
			DataPoints: []GlobalHistoryDataPoint{},
			Summary:    GlobalStatsSummary{Duration: duration.String()},
		}
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	now := time.Now()
	// 时间对齐到 interval 边界
	startTime := now.Add(-duration).Truncate(interval)
	// endTime 延伸一个 interval，确保当前时间段的请求也被包含
	endTime := now.Truncate(interval).Add(interval)

	numPoints := int(duration / interval)
	if numPoints <= 0 {
		numPoints = 1
	}
	numPoints++ // 额外的一个桶用于当前时间段

	// 使用 map 按时间分桶
	buckets := make(map[int64]*globalBucketData)
	for i := 0; i < numPoints; i++ {
		buckets[int64(i)] = &globalBucketData{}
	}

	// 汇总统计
	var totalRequests, totalSuccess, totalFailure int64
	var totalInputTokens, totalOutputTokens, totalCacheCreation, totalCacheRead int64

	// 按模型分桶（复用 modelBucket 结构）
	type modelBucket struct {
		requestCount        int64
		successCount        int64
		failureCount        int64
		inputTokens         int64
		outputTokens        int64
		cacheCreationTokens int64
		cacheReadTokens     int64
	}
	modelBuckets := make(map[string][]modelBucket)

	// 遍历所有 Key 的请求历史
	for _, metrics := range m.keyMetrics {
		for _, record := range metrics.requestHistory {
			// 使用 Before(endTime) 排除恰好落在 endTime 的记录，避免 offset 越界
			if record.Timestamp.After(startTime) && record.Timestamp.Before(endTime) {
				offset := int64(record.Timestamp.Sub(startTime) / interval)
				if offset >= 0 && offset < int64(numPoints) {
					b := buckets[offset]
					b.requestCount++
					if record.Success {
						b.successCount++
					} else {
						b.failureCount++
					}
					b.inputTokens += record.InputTokens
					b.outputTokens += record.OutputTokens
					b.cacheCreationTokens += record.CacheCreationInputTokens
					b.cacheReadTokens += record.CacheReadInputTokens

					// 累加汇总
					totalRequests++
					if record.Success {
						totalSuccess++
					} else {
						totalFailure++
					}
					totalInputTokens += record.InputTokens
					totalOutputTokens += record.OutputTokens
					totalCacheCreation += record.CacheCreationInputTokens
					totalCacheRead += record.CacheReadInputTokens

					// 同时按模型分桶（跳过无模型信息的记录）
					if model := record.Model; model != "" {
						if _, ok := modelBuckets[model]; !ok {
							modelBuckets[model] = make([]modelBucket, numPoints)
						}
						mb := &modelBuckets[model][offset]
						mb.requestCount++
						if record.Success {
							mb.successCount++
						} else {
							mb.failureCount++
						}
						mb.inputTokens += record.InputTokens
						mb.outputTokens += record.OutputTokens
						mb.cacheCreationTokens += record.CacheCreationInputTokens
						mb.cacheReadTokens += record.CacheReadInputTokens
					}
				}
			}
		}
	}

	// 构建数据点结果
	dataPoints := make([]GlobalHistoryDataPoint, numPoints)
	for i := 0; i < numPoints; i++ {
		b := buckets[int64(i)]
		successRate := float64(0)
		if b.requestCount > 0 {
			successRate = float64(b.successCount) / float64(b.requestCount) * 100
		}
		dataPoints[i] = GlobalHistoryDataPoint{
			Timestamp:           startTime.Add(time.Duration(i+1) * interval),
			RequestCount:        b.requestCount,
			SuccessCount:        b.successCount,
			FailureCount:        b.failureCount,
			SuccessRate:         successRate,
			InputTokens:         b.inputTokens,
			OutputTokens:        b.outputTokens,
			CacheCreationTokens: b.cacheCreationTokens,
			CacheReadTokens:     b.cacheReadTokens,
		}
	}

	// 计算平均成功率
	avgSuccessRate := float64(0)
	if totalRequests > 0 {
		avgSuccessRate = float64(totalSuccess) / float64(totalRequests) * 100
	}

	summary := GlobalStatsSummary{
		TotalRequests:            totalRequests,
		TotalSuccess:             totalSuccess,
		TotalFailure:             totalFailure,
		TotalInputTokens:         totalInputTokens,
		TotalOutputTokens:        totalOutputTokens,
		TotalCacheCreationTokens: totalCacheCreation,
		TotalCacheReadTokens:     totalCacheRead,
		AvgSuccessRate:           avgSuccessRate,
		Duration:                 duration.String(),
	}

	// 构建模型维度数据点，并把模型成本汇总回全局桶。
	var modelDataPoints map[string][]ModelHistoryDataPoint
	if len(modelBuckets) > 0 {
		modelDataPoints = make(map[string][]ModelHistoryDataPoint, len(modelBuckets))
		for model, buckets := range modelBuckets {
			points := make([]ModelHistoryDataPoint, numPoints)
			for i := 0; i < numPoints; i++ {
				costUSD := CalculateTokenCostUSD(model, buckets[i].inputTokens, buckets[i].outputTokens, buckets[i].cacheCreationTokens, buckets[i].cacheReadTokens)
				points[i] = ModelHistoryDataPoint{
					Timestamp:           startTime.Add(time.Duration(i+1) * interval),
					RequestCount:        buckets[i].requestCount,
					SuccessCount:        buckets[i].successCount,
					FailureCount:        buckets[i].failureCount,
					InputTokens:         buckets[i].inputTokens,
					OutputTokens:        buckets[i].outputTokens,
					CacheCreationTokens: buckets[i].cacheCreationTokens,
					CacheReadTokens:     buckets[i].cacheReadTokens,
					CostUSD:             costUSD,
				}
				dataPoints[i].CostUSD += costUSD
				summary.TotalCostUSD += costUSD
			}
			modelDataPoints[model] = points
		}
	}

	return GlobalStatsHistoryResponse{
		DataPoints:      dataPoints,
		Summary:         summary,
		ModelDataPoints: modelDataPoints,
	}
}

// globalBucketData 全局统计时间分桶的辅助结构
type globalBucketData struct {
	requestCount        int64
	successCount        int64
	failureCount        int64
	inputTokens         int64
	outputTokens        int64
	cacheCreationTokens int64
	cacheReadTokens     int64
}

// CalculateTodayDuration 计算"今日"时间范围（从今天 0 点到现在）
func CalculateTodayDuration() time.Duration {
	now := time.Now()
	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	return now.Sub(startOfDay)
}

// ============ 渠道实时活跃度数据（用于渐变背景显示）============

// ActivitySegment 活跃度分段数据（每 6 秒一段）
// 使用 omitempty 减少 JSON 体积，0 值字段不输出
type ActivitySegment struct {
	RequestCount int64 `json:"requestCount,omitempty"`
	SuccessCount int64 `json:"successCount,omitempty"`
	FailureCount int64 `json:"failureCount,omitempty"`
	InputTokens  int64 `json:"inputTokens,omitempty"`
	OutputTokens int64 `json:"outputTokens,omitempty"`
}

// ChannelRecentActivity 渠道最近活跃度数据
// 使用稀疏 Map 格式存储 segments，只返回有数据的段
type ChannelRecentActivity struct {
	ChannelIndex int                      `json:"channelIndex"`
	Segments     map[int]*ActivitySegment `json:"segments,omitempty"` // 稀疏表示：key=段索引(0-149)，只包含有请求的段
	TotalSegs    int                      `json:"totalSegs"`          // 总段数（固定 150），前端用于展开稀疏数组
	RPM          float64                  `json:"rpm,omitempty"`      // 15分钟平均 RPM
	TPM          float64                  `json:"tpm,omitempty"`      // 15分钟平均 TPM
}

// GetRecentActivityMultiURL 获取渠道最近活跃度数据（支持多 URL 和多 Key 聚合）
// 参数：
//   - channelIndex: 渠道索引
//   - baseURLs: 渠道的所有故障转移 URL（支持多个）
//   - activeKeys: 渠道的所有活跃 API Key（支持多个）
//
// 返回：
//   - 稀疏 Map 格式的活跃度数据（只包含有请求的段，减少 JSON 体积）
//   - 自动聚合所有 URL × Key 组合的请求数据
//   - RPM/TPM 为 15 分钟平均值
func (m *MetricsManager) GetRecentActivityMultiURL(channelIndex int, baseURLs []string, activeKeys []string, serviceType string) *ChannelRecentActivity {
	// 150 段，每段 6 秒 = 900 秒 = 15 分钟
	const numSegments = 150
	const segmentDuration = 6 * time.Second

	if len(baseURLs) == 0 || len(activeKeys) == 0 {
		return &ChannelRecentActivity{
			ChannelIndex: channelIndex,
			TotalSegs:    numSegments,
		}
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	now := time.Now()

	// 时间边界对齐：将 endTime 向上对齐到下一个 segmentDuration 边界
	// 这样每次请求的分段边界都是固定的，不会因为 now 的微小变化而导致数据跳动
	// 例如：segmentDuration=6s，now=12:34:57，则 endTime=12:35:00（包含当前正在进行的段）
	endTimeUnix := now.Unix()
	segmentSeconds := int64(segmentDuration.Seconds())
	alignedEndUnix := ((endTimeUnix / segmentSeconds) + 1) * segmentSeconds
	endTime := time.Unix(alignedEndUnix, 0)
	startTime := endTime.Add(-time.Duration(numSegments) * segmentDuration)

	// 使用稀疏 Map 存储有数据的分段
	sparseSegments := make(map[int]*ActivitySegment)

	// 汇总统计
	var totalRequests, totalInputTokens, totalOutputTokens int64

	for _, metrics := range m.getIdentityMetricsByMultiURLAndKeysLocked(baseURLs, activeKeys, serviceType) {
		// 遍历该 Key 的请求历史，放入对应分段
		for _, record := range metrics.requestHistory {
			// 检查是否在 [startTime, endTime) 范围内
			if record.Timestamp.Before(startTime) || !record.Timestamp.Before(endTime) {
				continue
			}

			// 计算属于哪个分段
			offset := int(record.Timestamp.Sub(startTime) / segmentDuration)
			if offset < 0 || offset >= numSegments {
				continue
			}

			// 按需创建稀疏 segment
			seg, exists := sparseSegments[offset]
			if !exists {
				seg = &ActivitySegment{}
				sparseSegments[offset] = seg
			}

			seg.RequestCount++
			if record.Success {
				seg.SuccessCount++
			} else {
				seg.FailureCount++
			}
			seg.InputTokens += record.InputTokens
			seg.OutputTokens += record.OutputTokens

			// 累加汇总
			totalRequests++
			totalInputTokens += record.InputTokens
			totalOutputTokens += record.OutputTokens
		}
	}

	// 计算 RPM 和 TPM（基于实际窗口时长）
	// TPM 只计算输出 tokens（包含思考），不包含输入 tokens 和缓存 tokens
	windowMinutes := float64(numSegments) * segmentDuration.Minutes()
	rpm := float64(totalRequests) / windowMinutes
	tpm := float64(totalOutputTokens) / windowMinutes

	return &ChannelRecentActivity{
		ChannelIndex: channelIndex,
		Segments:     sparseSegments,
		TotalSegs:    numSegments,
		RPM:          rpm,
		TPM:          tpm,
	}
}

// ModelHistoryDataPoint 模型级别历史数据点
type ModelHistoryDataPoint struct {
	Timestamp           time.Time `json:"timestamp"`
	RequestCount        int64     `json:"requestCount"`
	SuccessCount        int64     `json:"successCount"`
	FailureCount        int64     `json:"failureCount"`
	InputTokens         int64     `json:"inputTokens"`
	OutputTokens        int64     `json:"outputTokens"`
	CacheCreationTokens int64     `json:"cacheCreationTokens"`
	CacheReadTokens     int64     `json:"cacheReadTokens"`
	CostUSD             float64   `json:"costUSD"`
}

// GetModelStatsHistory 获取按模型分组的历史统计
func (m *MetricsManager) GetModelStatsHistory(duration, interval time.Duration) map[string][]ModelHistoryDataPoint {
	if interval <= 0 || duration <= 0 {
		return map[string][]ModelHistoryDataPoint{}
	}

	now := time.Now()
	startTime := now.Add(-duration).Truncate(interval)
	endTime := now.Truncate(interval).Add(interval)
	numPoints := int(duration/interval) + 1

	// 快速拷贝 requestHistory 引用，缩短持锁时间
	type historyRef struct {
		history []RequestRecord
	}
	var historyRefs []historyRef

	m.mu.RLock()
	for _, metrics := range m.keyMetrics {
		// 拷贝 slice 引用（底层数组共享，但遍历时不会修改）
		historyRefs = append(historyRefs, historyRef{history: metrics.requestHistory})
	}
	m.mu.RUnlock()

	// 解锁后进行聚合计算
	// 按模型分组收集记录
	type modelBucket struct {
		requestCount        int64
		successCount        int64
		failureCount        int64
		inputTokens         int64
		outputTokens        int64
		cacheCreationTokens int64
		cacheReadTokens     int64
	}
	// model -> bucketIndex -> data
	modelBuckets := make(map[string][]modelBucket)

	for _, ref := range historyRefs {
		for _, record := range ref.history {
			if record.Timestamp.Before(startTime) || !record.Timestamp.Before(endTime) {
				continue
			}
			model := record.Model
			if model == "" {
				continue // 跳过没有模型信息的记录
			}
			offset := int(record.Timestamp.Sub(startTime) / interval)
			if offset < 0 || offset >= numPoints {
				continue
			}
			if _, ok := modelBuckets[model]; !ok {
				modelBuckets[model] = make([]modelBucket, numPoints)
			}
			b := &modelBuckets[model][offset]
			b.requestCount++
			if record.Success {
				b.successCount++
			} else {
				b.failureCount++
			}
			b.inputTokens += record.InputTokens
			b.outputTokens += record.OutputTokens
			b.cacheCreationTokens += record.CacheCreationInputTokens
			b.cacheReadTokens += record.CacheReadInputTokens
		}
	}

	// 构建结果
	result := make(map[string][]ModelHistoryDataPoint, len(modelBuckets))
	for model, buckets := range modelBuckets {
		points := make([]ModelHistoryDataPoint, numPoints)
		for i := 0; i < numPoints; i++ {
			points[i] = ModelHistoryDataPoint{
				Timestamp:           startTime.Add(time.Duration(i) * interval),
				RequestCount:        buckets[i].requestCount,
				SuccessCount:        buckets[i].successCount,
				FailureCount:        buckets[i].failureCount,
				InputTokens:         buckets[i].inputTokens,
				OutputTokens:        buckets[i].outputTokens,
				CacheCreationTokens: buckets[i].cacheCreationTokens,
				CacheReadTokens:     buckets[i].cacheReadTokens,
				CostUSD:             CalculateTokenCostUSD(model, buckets[i].inputTokens, buckets[i].outputTokens, buckets[i].cacheCreationTokens, buckets[i].cacheReadTokens),
			}
		}
		result[model] = points
	}

	return result
}
