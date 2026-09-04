package metrics

import (
	"log"
	"math"
	"sort"
	"time"

	"github.com/BenedictKing/ccx/internal/utils"
)

// IsKeyHealthy 判断单个 Key 是否健康
func (m *MetricsManager) IsKeyHealthy(baseURL, apiKey, serviceType string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	metrics := m.getFirstMatchingMetricsLocked(baseURL, apiKey, serviceType)
	if metrics == nil {
		return true // 没有记录，默认健康
	}
	now := time.Now()
	m.advanceCircuitStateIfDueLocked(metrics, now)
	m.refreshBreakerWindowsLocked(metrics, now)
	if metrics.CircuitState == CircuitStateOpen {
		return false
	}
	if len(metrics.breakerResults) == 0 {
		return true
	}

	return m.calculateKeyBreakerFailureRateInternal(metrics) < m.failureThreshold
}

// IsChannelHealthy 判断渠道是否健康（基于当前活跃 Keys 聚合计算）
// activeKeys: 当前渠道配置的所有活跃 API Keys
func (m *MetricsManager) IsChannelHealthyWithKeys(baseURL string, activeKeys []string, serviceType string) bool {
	if len(activeKeys) == 0 {
		return false // 没有 Key，不健康
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	var totalResults []bool
	hasOpenOnly := false
	hasAvailableCandidate := false
	now := time.Now()
	for _, apiKey := range activeKeys {
		variants := m.getMetricsVariantsLocked(baseURL, apiKey, serviceType)
		if len(variants) == 0 {
			hasAvailableCandidate = true
			continue
		}
		for _, metrics := range variants {
			m.advanceCircuitStateIfDueLocked(metrics, now)
			m.refreshBreakerWindowsLocked(metrics, now)
			if metrics.CircuitState == CircuitStateOpen {
				hasOpenOnly = true
				continue
			}
			hasAvailableCandidate = true
			totalResults = append(totalResults, metrics.breakerResults...)
		}
	}

	if len(totalResults) == 0 {
		if hasOpenOnly && !hasAvailableCandidate {
			return false
		}
		return true
	}

	minRequests := max(3, m.windowSize/2)
	if len(totalResults) < minRequests {
		return true
	}

	failures := 0
	for _, success := range totalResults {
		if !success {
			failures++
		}
	}
	failureRate := float64(failures) / float64(len(totalResults))

	return failureRate < m.failureThreshold
}

// channelBreakerRecord 是渠道级熔断判定所需的带身份时间序列样本。
// 单个 Key 的 breaker 仍按各自的 requestHistory 维护；这里仅用于识别多个身份同时失败。
type channelBreakerRecord struct {
	timestamp time.Time
	success   bool
	relevant  bool
	identity  string
}

type channelBreakerSnapshot struct {
	results                         []bool
	failureIdentityCount            int
	consecutiveFailures             int64
	consecutiveFailureIdentityCount int
	hasOpenState                    bool
	hasAvailableCandidate           bool
}

func (s channelBreakerSnapshot) failureRate() float64 {
	if len(s.results) == 0 {
		return 0
	}
	failures := 0
	for _, success := range s.results {
		if !success {
			failures++
		}
	}
	return float64(failures) / float64(len(s.results))
}

// channelBreakerSnapshotLocked 聚合当前渠道所有可用身份的健康窗口。
// 非 breaker 相关的失败会打断“连续失败”序列，但不会进入失败率样本。
func (m *MetricsManager) channelBreakerSnapshotLocked(baseURLs, activeKeys []string, serviceType, model string, now time.Time) channelBreakerSnapshot {
	snapshot := channelBreakerSnapshot{
		hasAvailableCandidate: m.hasAvailableIdentityCandidateLocked(baseURLs, activeKeys, serviceType),
	}
	cutoff := now.Add(-defaultBreakerHealthWindow)
	records := make([]channelBreakerRecord, 0)

	for _, metrics := range m.getIdentityMetricsByMultiURLAndKeysLocked(baseURLs, activeKeys, serviceType) {
		legacyBreakerResults := append([]bool(nil), metrics.breakerResults...)
		m.advanceCircuitStateIfDueLocked(metrics, now)
		m.refreshBreakerWindowsLocked(metrics, now)
		if metrics.CircuitState == CircuitStateOpen {
			snapshot.hasOpenState = true
			continue
		}
		snapshot.hasAvailableCandidate = true

		pendingIndexes := make(map[int]struct{}, len(metrics.pendingHistoryIdx))
		for _, idx := range metrics.pendingHistoryIdx {
			pendingIndexes[idx] = struct{}{}
		}
		for idx, record := range metrics.requestHistory {
			if _, pending := pendingIndexes[idx]; pending || record.Timestamp.Before(cutoff) {
				continue
			}
			// 模型作用域过滤：只统计当前 routeModel 的记录。
			// 成功也过滤，避免其他模型成功稀释当前模型失败率。
			if model != "" && recordRouteModel(&record) != model {
				continue
			}
			normalizedClass := normalizeFailureClass(record.Success, record.FailureClass)
			records = append(records, channelBreakerRecord{
				timestamp: record.Timestamp,
				success:   record.Success,
				relevant:  record.Success || normalizedClass.IsBreakerRelevant(),
				identity:  metrics.MetricsKey,
			})
		}

		// 仅有派生窗口、没有历史时间戳的旧内存指标只参与空模型聚合，
		// 不注入具体模型窗口——旧记录没有 routeModel 可以归因。
		if len(metrics.requestHistory) == 0 && model == "" {
			for _, success := range legacyBreakerResults {
				records = append(records, channelBreakerRecord{
					timestamp: now,
					success:   success,
					relevant:  true,
					identity:  metrics.MetricsKey,
				})
			}
		}
	}

	sort.SliceStable(records, func(i, j int) bool {
		if records[i].timestamp.Equal(records[j].timestamp) {
			return records[i].identity < records[j].identity
		}
		return records[i].timestamp.Before(records[j].timestamp)
	})

	failureIdentities := make(map[string]struct{})
	streakIdentities := make(map[string]struct{})
	for _, record := range records {
		if !record.relevant {
			streakIdentities = make(map[string]struct{})
			snapshot.consecutiveFailures = 0
			continue
		}
		snapshot.results = append(snapshot.results, record.success)
		if record.success {
			streakIdentities = make(map[string]struct{})
			snapshot.consecutiveFailures = 0
			continue
		}

		failureIdentities[record.identity] = struct{}{}
		streakIdentities[record.identity] = struct{}{}
		snapshot.consecutiveFailures++
	}
	snapshot.failureIdentityCount = len(failureIdentities)
	snapshot.consecutiveFailureIdentityCount = len(streakIdentities)
	return snapshot
}

// isCombinedChannelFailureLocked 判断是否存在跨 Key/BaseURL 的共同故障。
// 至少两个独立身份同时失败，避免单个坏端点触发整个渠道的组合熔断。
func (m *MetricsManager) isCombinedChannelFailureLocked(snapshot channelBreakerSnapshot) bool {
	if snapshot.failureIdentityCount < 2 {
		return false
	}
	if snapshot.consecutiveFailures >= m.consecutiveFailuresThreshold && snapshot.consecutiveFailureIdentityCount >= 2 {
		return true
	}
	minRequests := max(3, m.windowSize/2)
	return len(snapshot.results) >= minRequests && snapshot.failureRate() >= m.failureThreshold
}

func (m *MetricsManager) isChannelBreakerHealthyLocked(snapshot channelBreakerSnapshot) bool {
	if m.isCombinedChannelFailureLocked(snapshot) {
		return false
	}
	if len(snapshot.results) == 0 {
		return !snapshot.hasOpenState || snapshot.hasAvailableCandidate
	}
	minRequests := max(3, m.windowSize/2)
	if len(snapshot.results) < minRequests {
		return true
	}
	return snapshot.failureRate() < m.failureThreshold
}

// IsChannelHealthyMultiURL 判断多 BaseURL 聚合渠道是否健康。
func (m *MetricsManager) IsChannelHealthyMultiURL(baseURLs []string, activeKeys []string, serviceType, model string) bool {
	if len(baseURLs) == 0 {
		return false
	}
	if len(activeKeys) == 0 {
		return false
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	snapshot := m.channelBreakerSnapshotLocked(baseURLs, activeKeys, serviceType, model, time.Now())
	return m.isChannelBreakerHealthyLocked(snapshot)
}

// CalculateChannelFailureRateMultiURL 计算多 BaseURL 聚合 breaker 失败率。
func (m *MetricsManager) CalculateChannelFailureRateMultiURL(baseURLs []string, activeKeys []string, serviceType, model string) float64 {
	if len(baseURLs) == 0 || len(activeKeys) == 0 {
		return 0
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	snapshot := m.channelBreakerSnapshotLocked(baseURLs, activeKeys, serviceType, model, time.Now())
	if len(snapshot.results) == 0 {
		if snapshot.hasOpenState && !snapshot.hasAvailableCandidate {
			return 1
		}
		return 0
	}
	return snapshot.failureRate()
}

// CalculateKeyFailureRate 计算单个 Key 的 breaker 失败率
func (m *MetricsManager) CalculateKeyFailureRate(baseURL, apiKey, serviceType string) float64 {
	m.mu.Lock()
	defer m.mu.Unlock()

	metrics := m.getFirstMatchingMetricsLocked(baseURL, apiKey, serviceType)
	if metrics == nil {
		return 0
	}
	now := time.Now()
	m.advanceCircuitStateIfDueLocked(metrics, now)
	m.refreshBreakerWindowsLocked(metrics, now)
	if metrics.CircuitState == CircuitStateOpen && len(metrics.breakerResults) == 0 {
		return 1
	}

	return m.calculateKeyBreakerFailureRateInternal(metrics)
}

// CalculateChannelFailureRate 计算渠道聚合 breaker 失败率
func (m *MetricsManager) CalculateChannelFailureRate(baseURL string, activeKeys []string, serviceType string) float64 {
	if len(activeKeys) == 0 {
		return 0
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	var totalResults []bool
	hasOpenOnly := false
	hasAvailableCandidate := false
	now := time.Now()
	for _, apiKey := range activeKeys {
		variants := m.getMetricsVariantsLocked(baseURL, apiKey, serviceType)
		if len(variants) == 0 {
			hasAvailableCandidate = true
			continue
		}
		for _, metrics := range variants {
			m.advanceCircuitStateIfDueLocked(metrics, now)
			m.refreshBreakerWindowsLocked(metrics, now)
			if metrics.CircuitState == CircuitStateOpen {
				hasOpenOnly = true
				continue
			}
			hasAvailableCandidate = true
			totalResults = append(totalResults, metrics.breakerResults...)
		}
	}

	if len(totalResults) == 0 {
		if hasOpenOnly && !hasAvailableCandidate {
			return 1
		}
		return 0
	}

	failures := 0
	for _, success := range totalResults {
		if !success {
			failures++
		}
	}

	return float64(failures) / float64(len(totalResults))
}

// GetKeyMetrics 获取单个 Key 的指标
func (m *MetricsManager) GetKeyMetrics(baseURL, apiKey, serviceType string) *KeyMetrics {
	m.mu.Lock()
	defer m.mu.Unlock()

	metrics := m.getFirstMatchingMetricsLocked(baseURL, apiKey, serviceType)
	if metrics != nil {
		now := time.Now()
		m.advanceCircuitStateIfDueLocked(metrics, now)
		m.refreshBreakerWindowsLocked(metrics, now)
		return &KeyMetrics{
			MetricsKey:          metrics.MetricsKey,
			BaseURL:             metrics.BaseURL,
			KeyMask:             metrics.KeyMask,
			RequestCount:        metrics.RequestCount,
			SuccessCount:        metrics.SuccessCount,
			FailureCount:        metrics.FailureCount,
			ConsecutiveFailures: metrics.ConsecutiveFailures,
			ActiveRequests:      metrics.ActiveRequests,
			LastSuccessAt:       metrics.LastSuccessAt,
			LastFailureAt:       metrics.LastFailureAt,
			CircuitBrokenAt:     metrics.CircuitBrokenAt,
			CircuitState:        metrics.CircuitState,
			HalfOpenAt:          metrics.HalfOpenAt,
			NextRetryAt:         metrics.NextRetryAt,
			BackoffLevel:        metrics.BackoffLevel,
			HalfOpenSuccesses:   metrics.HalfOpenSuccesses,
		}
	}
	return nil
}

// GetChannelAggregatedMetrics 获取渠道聚合指标（基于活跃 Keys）
func (m *MetricsManager) GetChannelAggregatedMetrics(channelIndex int, baseURL string, activeKeys []string, serviceType string) *ChannelMetrics {
	m.mu.Lock()
	defer m.mu.Unlock()

	aggregated := &ChannelMetrics{
		ChannelIndex: channelIndex,
		CircuitState: CircuitStateClosed,
	}

	var latestSuccess, latestFailure, latestCircuitBroken, latestNextRetry *time.Time
	var maxConsecutiveFailures int64
	var maxHalfOpenSuccesses int
	channelState := CircuitStateClosed
	now := time.Now()

	for _, apiKey := range activeKeys {
		for _, metrics := range m.getMetricsVariantsLocked(baseURL, apiKey, serviceType) {
			m.advanceCircuitStateIfDueLocked(metrics, now)
			m.refreshBreakerWindowsLocked(metrics, now)
			aggregated.RequestCount += metrics.RequestCount
			aggregated.SuccessCount += metrics.SuccessCount
			aggregated.FailureCount += metrics.FailureCount
			if metrics.ConsecutiveFailures > maxConsecutiveFailures {
				maxConsecutiveFailures = metrics.ConsecutiveFailures
			}
			if metrics.HalfOpenSuccesses > maxHalfOpenSuccesses {
				maxHalfOpenSuccesses = metrics.HalfOpenSuccesses
			}
			aggregated.recentResults = append(aggregated.recentResults, metrics.recentResults...)
			aggregated.breakerResults = append(aggregated.breakerResults, metrics.breakerResults...)
			aggregated.requestHistory = append(aggregated.requestHistory, metrics.requestHistory...)

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
			if metrics.CircuitState > channelState {
				channelState = metrics.CircuitState
			}
		}
	}

	aggregated.LastSuccessAt = latestSuccess
	aggregated.LastFailureAt = latestFailure
	aggregated.CircuitBrokenAt = latestCircuitBroken
	aggregated.NextRetryAt = latestNextRetry
	aggregated.CircuitState = channelState
	aggregated.HalfOpenSuccesses = maxHalfOpenSuccesses
	aggregated.ConsecutiveFailures = maxConsecutiveFailures

	return aggregated
}

// KeyUsageInfo Key 使用信息（用于排序筛选）
type KeyUsageInfo struct {
	APIKey       string
	KeyMask      string
	RequestCount int64
	LastUsedAt   *time.Time
}

// GetChannelKeyUsageInfo 获取渠道下所有 Key 的使用信息（用于排序筛选）
// 返回的 keys 已按最近使用时间排序
func (m *MetricsManager) GetChannelKeyUsageInfo(baseURL string, apiKeys []string, serviceType string) []KeyUsageInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	infos := make([]KeyUsageInfo, 0, len(apiKeys))

	for _, apiKey := range apiKeys {
		metrics := m.getIdentityMetricsLocked(baseURL, apiKey, serviceType)
		if metrics == nil {
			infos = append(infos, KeyUsageInfo{
				APIKey:       apiKey,
				KeyMask:      utils.MaskAPIKey(apiKey),
				RequestCount: 0,
				LastUsedAt:   nil,
			})
			continue
		}
		usedAt := metrics.LastSuccessAt
		if usedAt == nil {
			usedAt = metrics.LastFailureAt
		}
		infos = append(infos, KeyUsageInfo{
			APIKey:       apiKey,
			KeyMask:      metrics.KeyMask,
			RequestCount: metrics.RequestCount,
			LastUsedAt:   usedAt,
		})
	}

	// 按最近使用时间排序（最近的在前面）
	sort.Slice(infos, func(i, j int) bool {
		if infos[i].LastUsedAt == nil && infos[j].LastUsedAt == nil {
			return infos[i].RequestCount > infos[j].RequestCount // 都未使用时，按访问量排序
		}
		if infos[i].LastUsedAt == nil {
			return false // i 未使用，排后面
		}
		if infos[j].LastUsedAt == nil {
			return true // j 未使用，i 排前面
		}
		return infos[i].LastUsedAt.After(*infos[j].LastUsedAt)
	})

	return infos
}

// GetChannelKeyUsageInfoMultiURL 获取渠道 Key 使用信息（支持多 URL 聚合）
func (m *MetricsManager) GetChannelKeyUsageInfoMultiURL(baseURLs []string, apiKeys []string, serviceType string) []KeyUsageInfo {
	if len(baseURLs) == 0 {
		return []KeyUsageInfo{}
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	infos := make([]KeyUsageInfo, 0, len(apiKeys))

	for _, apiKey := range apiKeys {
		var keyMask string
		var requestCount int64
		var lastUsedAt *time.Time

		for _, metrics := range m.getIdentityMetricsByMultiURLLocked(baseURLs, apiKey, serviceType) {
			if keyMask == "" {
				keyMask = metrics.KeyMask
			}
			requestCount += metrics.RequestCount
			usedAt := metrics.LastSuccessAt
			if usedAt == nil {
				usedAt = metrics.LastFailureAt
			}
			if usedAt != nil && (lastUsedAt == nil || usedAt.After(*lastUsedAt)) {
				lastUsedAt = usedAt
			}
		}

		if keyMask == "" {
			keyMask = utils.MaskAPIKey(apiKey)
		}

		infos = append(infos, KeyUsageInfo{
			APIKey:       apiKey,
			KeyMask:      keyMask,
			RequestCount: requestCount,
			LastUsedAt:   lastUsedAt,
		})
	}

	// 按最近使用时间排序（最近的在前面）
	sort.Slice(infos, func(i, j int) bool {
		if infos[i].LastUsedAt == nil && infos[j].LastUsedAt == nil {
			return infos[i].RequestCount > infos[j].RequestCount // 都未使用时，按访问量排序
		}
		if infos[i].LastUsedAt == nil {
			return false // i 未使用，排后面
		}
		if infos[j].LastUsedAt == nil {
			return true // j 未使用，i 排前面
		}
		return infos[i].LastUsedAt.After(*infos[j].LastUsedAt)
	})

	return infos
}

// SelectTopKeys 筛选展示的 Key
// 策略：先取最近使用的 5 个，再从其他 Key 中按访问量补全到 10 个
func SelectTopKeys(infos []KeyUsageInfo, maxDisplay int) []KeyUsageInfo {
	if len(infos) <= maxDisplay {
		return infos
	}

	// 分离：最近使用的和未使用的
	var recentKeys []KeyUsageInfo
	var otherKeys []KeyUsageInfo

	for i, info := range infos {
		if i < 5 {
			recentKeys = append(recentKeys, info)
		} else {
			otherKeys = append(otherKeys, info)
		}
	}

	// 其他 Key 按访问量排序（降序）
	sort.Slice(otherKeys, func(i, j int) bool {
		return otherKeys[i].RequestCount > otherKeys[j].RequestCount
	})

	// 补全到 maxDisplay 个
	result := make([]KeyUsageInfo, 0, maxDisplay)
	result = append(result, recentKeys...)

	needCount := maxDisplay - len(recentKeys)
	if needCount > 0 && len(otherKeys) > 0 {
		if len(otherKeys) > needCount {
			otherKeys = otherKeys[:needCount]
		}
		result = append(result, otherKeys...)
	}

	return result
}

// GetAllKeyMetrics 获取所有 Key 的指标
func (m *MetricsManager) GetAllKeyMetrics() []*KeyMetrics {
	m.mu.Lock()
	defer m.mu.Unlock()

	result := make([]*KeyMetrics, 0, len(m.keyMetrics))
	now := time.Now()
	for _, metrics := range m.keyMetrics {
		m.advanceCircuitStateIfDueLocked(metrics, now)
		m.refreshBreakerWindowsLocked(metrics, now)
		result = append(result, &KeyMetrics{
			MetricsKey:          metrics.MetricsKey,
			BaseURL:             metrics.BaseURL,
			KeyMask:             metrics.KeyMask,
			RequestCount:        metrics.RequestCount,
			SuccessCount:        metrics.SuccessCount,
			FailureCount:        metrics.FailureCount,
			ConsecutiveFailures: metrics.ConsecutiveFailures,
			ActiveRequests:      metrics.ActiveRequests,
			LastSuccessAt:       metrics.LastSuccessAt,
			LastFailureAt:       metrics.LastFailureAt,
			CircuitBrokenAt:     metrics.CircuitBrokenAt,
			CircuitState:        metrics.CircuitState,
			HalfOpenAt:          metrics.HalfOpenAt,
			NextRetryAt:         metrics.NextRetryAt,
			BackoffLevel:        metrics.BackoffLevel,
			HalfOpenSuccesses:   metrics.HalfOpenSuccesses,
		})
	}
	return result
}

// GetTimeWindowStatsForKey 获取指定 Key 在时间窗口内的统计
func (m *MetricsManager) GetTimeWindowStatsForKey(baseURL, apiKey, serviceType string, duration time.Duration) TimeWindowStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cutoff := time.Now().Add(-duration)
	var requestCount, successCount, failureCount int64
	connectLatencies := make([]int64, 0)
	firstByteLatencies := make([]int64, 0)

	metrics := m.getIdentityMetricsLocked(baseURL, apiKey, serviceType)
	if metrics != nil {
		for _, record := range metrics.requestHistory {
			if record.Timestamp.After(cutoff) {
				requestCount++
				if record.Success {
					successCount++
				} else {
					failureCount++
				}
				if record.Success && record.FirstByteLatencyMs > 0 {
					firstByteLatencies = append(firstByteLatencies, record.FirstByteLatencyMs)
				}
				if record.Success && record.ConnectLatencyMs > 0 {
					connectLatencies = append(connectLatencies, record.ConnectLatencyMs)
				}
			}
		}
	}

	if requestCount == 0 {
		return TimeWindowStats{SuccessRate: 100}
	}

	successRate := float64(successCount) / float64(requestCount) * 100
	sort.Slice(connectLatencies, func(i, j int) bool { return connectLatencies[i] < connectLatencies[j] })
	sort.Slice(firstByteLatencies, func(i, j int) bool { return firstByteLatencies[i] < firstByteLatencies[j] })

	return TimeWindowStats{
		RequestCount:          requestCount,
		SuccessCount:          successCount,
		FailureCount:          failureCount,
		SuccessRate:           successRate,
		ConnectSampleCount:    int64(len(connectLatencies)),
		P95ConnectLatencyMs:   nearestRankPercentile(connectLatencies, 95),
		FirstByteSampleCount:  int64(len(firstByteLatencies)),
		P95FirstByteLatencyMs: nearestRankPercentile(firstByteLatencies, 95),
	}
}

func nearestRankPercentile(sortedValues []int64, percentile int) int64 {
	if len(sortedValues) == 0 || percentile <= 0 {
		return 0
	}
	if percentile >= 100 {
		return sortedValues[len(sortedValues)-1]
	}
	index := (len(sortedValues)*percentile + 99) / 100
	if index < 1 {
		index = 1
	}
	return sortedValues[index-1]
}

// ── S 型时间衰减统计 ──

// 衰减参数：w(age) = 1 / (1 + exp((age - t50) / k))。
// t50=4h 表示记录发生 4 小时后证据权重衰减到 0.5；k=1h 控制 S 型过渡带的陡峭程度。
const (
	recencyDecayT50 = 4 * time.Hour
	recencyDecayK   = 1 * time.Hour
)

// RecencyWeight 返回一条距今 age 的历史记录对当前健康判定的证据权重（0-1，S 型衰减）。
// 越是久远的记录（尤其是失败）对当前判定的贡献越低。
func RecencyWeight(age time.Duration) float64 {
	if age <= 0 {
		return 1
	}
	x := float64(age-recencyDecayT50) / float64(recencyDecayK)
	// 防止 exp 上溢（age 极大时直接归零）
	if x > 40 {
		return 0
	}
	return 1 / (1 + math.Exp(x))
}

// DecayedStats 是按 S 型时间衰减加权后的有效计数。
type DecayedStats struct {
	SuccessCount float64
	FailureCount float64
}

// GetDecayedStatsForKey 获取指定 Key 在时间窗口内按 S 型衰减加权的有效成功/失败计数。
// 供健康判定使用：一次陈旧故障（如数小时前的本机 DNS 抖动）不会无限期维持判死，
// 证据随时间自然衰减，过期后渠道回到 unknown 由后续流量重新验证。
func (m *MetricsManager) GetDecayedStatsForKey(baseURL, apiKey, serviceType string, duration time.Duration) DecayedStats {
	m.mu.RLock()
	defer m.mu.RUnlock()

	now := time.Now()
	cutoff := now.Add(-duration)
	var stats DecayedStats

	metrics := m.getIdentityMetricsLocked(baseURL, apiKey, serviceType)
	if metrics == nil {
		return stats
	}
	for _, record := range metrics.requestHistory {
		if !record.Timestamp.After(cutoff) {
			continue
		}
		w := RecencyWeight(now.Sub(record.Timestamp))
		if record.Success {
			stats.SuccessCount += w
		} else {
			stats.FailureCount += w
		}
	}
	return stats
}

// GetAllTimeWindowStatsForKey 获取单个 Key 所有时间窗口的统计
func (m *MetricsManager) GetAllTimeWindowStatsForKey(baseURL, apiKey, serviceType string) map[string]TimeWindowStats {
	return map[string]TimeWindowStats{
		"15m": m.GetTimeWindowStatsForKey(baseURL, apiKey, serviceType, 15*time.Minute),
		"1h":  m.GetTimeWindowStatsForKey(baseURL, apiKey, serviceType, 1*time.Hour),
		"6h":  m.GetTimeWindowStatsForKey(baseURL, apiKey, serviceType, 6*time.Hour),
		"24h": m.GetTimeWindowStatsForKey(baseURL, apiKey, serviceType, 24*time.Hour),
	}
}

// MoveKeyToHalfOpen 强制将指定 Key 切换到 half-open 探测状态。
func (m *MetricsManager) MoveKeyToHalfOpen(baseURL, apiKey, serviceType string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	metrics := m.getOrCreateKey(baseURL, apiKey, serviceType)
	m.moveCircuitToHalfOpenLocked(metrics, time.Now())
	log.Printf("[Metrics-Circuit] Key [%s] (%s) 已切换到 half-open", metrics.KeyMask, metrics.BaseURL)
}

// ResetKeyFailureState 重置单个 Key 的熔断/失败状态（保留历史统计与总量计数）。
// 用于“恢复熔断”场景：清零连续失败、清空 breaker 滑动窗口、解除熔断标记。
func (m *MetricsManager) ResetKeyFailureState(baseURL, apiKey, serviceType string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	metrics := m.getFirstMatchingMetricsLocked(baseURL, apiKey, serviceType)
	if metrics != nil {
		metrics.recentResults = make([]bool, 0, m.windowSize)
		m.resetCircuitStateLocked(metrics, true)
		log.Printf("[Metrics-Reset] Key [%s] (%s) 熔断状态已重置（保留历史统计）", metrics.KeyMask, metrics.BaseURL)
	}
}

// ResetKey 重置单个 Key 的指标
func (m *MetricsManager) ResetKey(baseURL, apiKey, serviceType string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	metrics := m.getFirstMatchingMetricsLocked(baseURL, apiKey, serviceType)
	if metrics != nil {
		metrics.RequestCount = 0
		metrics.SuccessCount = 0
		metrics.FailureCount = 0
		metrics.ActiveRequests = 0
		metrics.LastSuccessAt = nil
		metrics.LastFailureAt = nil
		metrics.recentResults = make([]bool, 0, m.windowSize)
		metrics.breakerResults = make([]bool, 0, m.windowSize)
		metrics.requestHistory = nil
		m.resetCircuitStateLocked(metrics, true)
		if metrics.pendingHistoryIdx != nil {
			for id := range metrics.pendingHistoryIdx {
				delete(metrics.pendingHistoryIdx, id)
			}
		}
		log.Printf("[Metrics-Reset] Key [%s] (%s) 指标已完全重置", metrics.KeyMask, metrics.BaseURL)
	}
}

// ResetAll 重置所有指标
func (m *MetricsManager) ResetAll() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.keyMetrics = make(map[string]*KeyMetrics)
}

// Stop 停止后台清理任务
func (m *MetricsManager) Stop() {
	close(m.stopCh)
}

// DeleteKeysForChannel 删除指定渠道的所有内存指标
// baseURLs: 渠道的所有 BaseURL（支持多端点 failover）
// apiKeys: 渠道的所有 API Key
// 返回所有可能的 metricsKey 列表（无论内存中是否存在，用于后续清理持久化数据）
func (m *MetricsManager) DeleteKeysForChannel(baseURLs, apiKeys []string, serviceType string) []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	seenKeys := make(map[string]struct{})
	allKeys := make([]string, 0, len(baseURLs)*max(1, len(apiKeys)))
	var deletedFromMemory int

	for _, baseURL := range baseURLs {
		for _, apiKey := range apiKeys {
			for _, metricsKey := range m.metricsLookupKeys(baseURL, apiKey, serviceType) {
				if _, exists := seenKeys[metricsKey]; exists {
					continue
				}
				seenKeys[metricsKey] = struct{}{}
				allKeys = append(allKeys, metricsKey)
				if _, exists := m.keyMetrics[metricsKey]; exists {
					delete(m.keyMetrics, metricsKey)
					deletedFromMemory++
				}
			}
		}
	}

	if deletedFromMemory > 0 {
		log.Printf("[Metrics-Delete] 已删除 %d 个内存指标记录", deletedFromMemory)
	}

	return allKeys
}

// DeleteChannelMetrics 删除渠道的所有指标数据（内存 + 持久化）
// baseURLs: 渠道的所有 BaseURL（支持多端点 failover）
// apiKeys: 渠道的所有 API Key
// 返回被删除的持久化记录数
func (m *MetricsManager) DeleteChannelMetrics(baseURLs, apiKeys []string, serviceType string) int64 {
	deletedKeys := m.DeleteKeysForChannel(baseURLs, apiKeys, serviceType)

	if m.store != nil && len(deletedKeys) > 0 {
		deleted, err := m.store.DeleteRecordsByMetricsKeys(deletedKeys, m.apiType)
		if err != nil {
			log.Printf("[Metrics-Delete] 警告: 删除持久化指标记录失败: %v", err)
			return 0
		}
		if _, err := m.store.DeleteCircuitStatesByMetricsKeys(deletedKeys, m.apiType); err != nil {
			log.Printf("[Metrics-Delete] 警告: 删除持久化熔断状态失败: %v", err)
		}
		if deleted > 0 {
			log.Printf("[Metrics-Delete] 已删除 %d 条 %s 持久化指标记录", deleted, m.apiType)
		}
		return deleted
	}

	return 0
}

// DeleteByMetricsKeys 按 metricsKey 列表直接删除指标数据（内存 + 持久化）
// 用于精确删除特定的 (BaseURL, APIKey) 组合，避免笛卡尔积误删
//
// 返回值语义：
//   - 如果配置了持久化存储：返回从持久化存储中删除的记录数
//   - 如果未配置持久化存储或删除失败：返回 0
//   - 注意：内存中的删除数量通过日志输出，不影响返回值
func (m *MetricsManager) DeleteByMetricsKeys(metricsKeys []string) int64 {
	if len(metricsKeys) == 0 {
		return 0
	}

	m.mu.Lock()
	var deletedFromMemory int
	for _, metricsKey := range metricsKeys {
		if _, exists := m.keyMetrics[metricsKey]; exists {
			delete(m.keyMetrics, metricsKey)
			deletedFromMemory++
		}
	}
	m.mu.Unlock()

	if deletedFromMemory > 0 {
		log.Printf("[Metrics-Delete] 已删除 %d 个内存指标记录", deletedFromMemory)
	}

	if m.store != nil {
		deleted, err := m.store.DeleteRecordsByMetricsKeys(metricsKeys, m.apiType)
		if err != nil {
			log.Printf("[Metrics-Delete] 警告: 删除持久化指标记录失败: %v", err)
			return 0
		}
		if _, err := m.store.DeleteCircuitStatesByMetricsKeys(metricsKeys, m.apiType); err != nil {
			log.Printf("[Metrics-Delete] 警告: 删除持久化熔断状态失败: %v", err)
		}
		if deleted > 0 {
			log.Printf("[Metrics-Delete] 已删除 %d 条 %s 持久化指标记录", deleted, m.apiType)
		}
		return deleted
	}

	return 0
}

// ============ 废弃的旧方法（保留签名以便编译，但标记为废弃）============

// Deprecated: 使用 IsChannelHealthyWithKeys 代替
// IsChannelHealthy 判断渠道是否健康（旧方法，不再使用 channelIndex）
// 此方法保留是为了兼容，但始终返回 true，调用方应迁移到新方法
func (m *MetricsManager) IsChannelHealthy(channelIndex int) bool {
	log.Printf("[Metrics-Deprecated] 警告: 调用了废弃的 IsChannelHealthy(channelIndex=%d)，请迁移到 IsChannelHealthyWithKeys", channelIndex)
	return true // 默认健康，避免影响现有逻辑
}

// Deprecated: 使用 CalculateChannelFailureRate 代替
func (m *MetricsManager) CalculateFailureRate(channelIndex int) float64 {
	return 0
}

// Deprecated: 使用 CalculateChannelFailureRate 代替
func (m *MetricsManager) CalculateSuccessRate(channelIndex int) float64 {
	return 1
}

// Deprecated: 使用 ResetKey 代替
func (m *MetricsManager) Reset(channelIndex int) {
	log.Printf("[Metrics-Deprecated] 警告: 调用了废弃的 Reset(channelIndex=%d)，请迁移到 ResetKey", channelIndex)
}

// Deprecated: 使用 GetChannelAggregatedMetrics 代替
func (m *MetricsManager) GetMetrics(channelIndex int) *ChannelMetrics {
	return nil
}

// Deprecated: 使用 GetAllKeyMetrics 代替
func (m *MetricsManager) GetAllMetrics() []*ChannelMetrics {
	return nil
}

// Deprecated: 使用 GetTimeWindowStatsForKey 代替
func (m *MetricsManager) GetTimeWindowStats(channelIndex int, duration time.Duration) TimeWindowStats {
	return TimeWindowStats{SuccessRate: 100}
}

// Deprecated: 使用 GetAllTimeWindowStatsForKey 代替
func (m *MetricsManager) GetAllTimeWindowStats(channelIndex int) map[string]TimeWindowStats {
	return map[string]TimeWindowStats{
		"15m": {SuccessRate: 100},
		"1h":  {SuccessRate: 100},
		"6h":  {SuccessRate: 100},
		"24h": {SuccessRate: 100},
	}
}

// Deprecated: 使用新的 ShouldSuspendKey 代替
func (m *MetricsManager) ShouldSuspend(channelIndex int) bool {
	return false
}

// GetKeyCircuitState 获取单个 Key 当前的 breaker 状态。
func (m *MetricsManager) GetKeyCircuitState(baseURL, apiKey, serviceType string) CircuitState {
	m.mu.Lock()
	defer m.mu.Unlock()

	metrics := m.getFirstMatchingMetricsLocked(baseURL, apiKey, serviceType)
	if metrics == nil {
		return CircuitStateClosed
	}
	now := time.Now()
	m.advanceCircuitStateIfDueLocked(metrics, now)
	m.refreshBreakerWindowsLocked(metrics, now)
	return metrics.CircuitState
}

// TryAcquireProbe 尝试占用 half-open 探针资格。
func (m *MetricsManager) TryAcquireProbe(baseURL, apiKey, serviceType string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	metrics := m.getFirstMatchingMetricsLocked(baseURL, apiKey, serviceType)
	if metrics == nil {
		return false
	}
	now := time.Now()
	m.advanceCircuitStateIfDueLocked(metrics, now)
	m.refreshBreakerWindowsLocked(metrics, now)
	if metrics.CircuitState != CircuitStateHalfOpen || metrics.ProbeInFlight {
		return false
	}
	metrics.ProbeInFlight = true
	return true
}

// ReleaseProbe 释放 half-open 探针占用。
func (m *MetricsManager) ReleaseProbe(baseURL, apiKey, serviceType string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	metrics := m.getFirstMatchingMetricsLocked(baseURL, apiKey, serviceType)
	if metrics != nil {
		metrics.ProbeInFlight = false
	}
}

// GetChannelCircuitStateMultiURL 获取多 BaseURL 聚合后的 channel breaker 状态。
func (m *MetricsManager) GetChannelCircuitStateMultiURL(baseURLs []string, activeKeys []string, serviceType, model string) CircuitState {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.channelCircuitStateMultiURLLocked(baseURLs, activeKeys, serviceType, model, time.Now())
}

func (m *MetricsManager) channelCircuitStateMultiURLLocked(baseURLs []string, activeKeys []string, serviceType, model string, now time.Time) CircuitState {
	snapshot := m.channelBreakerSnapshotLocked(baseURLs, activeKeys, serviceType, model, now)
	if !m.isChannelBreakerHealthyLocked(snapshot) {
		// 组合失败或聚合失败率达到阈值时，渠道级状态必须与健康判定一致，
		// 否则调度器会在 fallback 阶段重新选回同一故障渠道。
		return CircuitStateOpen
	}

	hasHalfOpen := false
	for _, baseURL := range baseURLs {
		for _, apiKey := range activeKeys {
			metrics := m.getIdentityMetricsLocked(baseURL, apiKey, serviceType)
			if metrics == nil {
				return CircuitStateClosed
			}
			m.advanceCircuitStateIfDueLocked(metrics, now)
			m.refreshBreakerWindowsLocked(metrics, now)
			if metrics.CircuitState == CircuitStateClosed {
				return CircuitStateClosed
			}
			if metrics.CircuitState == CircuitStateHalfOpen {
				hasHalfOpen = true
			}
		}
	}
	if hasHalfOpen {
		return CircuitStateHalfOpen
	}
	return CircuitStateOpen
}

// HasProbeCandidateMultiURL 判断渠道是否存在可用的 half-open 探针候选。
func (m *MetricsManager) HasProbeCandidateMultiURL(baseURLs []string, activeKeys []string, serviceType string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	for _, apiKey := range activeKeys {
		for _, metrics := range m.getIdentityMetricsByMultiURLLocked(baseURLs, apiKey, serviceType) {
			m.advanceCircuitStateIfDueLocked(metrics, now)
			m.refreshBreakerWindowsLocked(metrics, now)
			if metrics.CircuitState == CircuitStateHalfOpen && !metrics.ProbeInFlight {
				return true
			}
		}
	}
	return false
}

// ShouldSuspendKey 判断单个 Key 是否应该熔断
func (m *MetricsManager) ShouldSuspendKey(baseURL, apiKey, serviceType string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()

	metrics := m.getFirstMatchingMetricsLocked(baseURL, apiKey, serviceType)
	if metrics == nil {
		return false
	}
	now := time.Now()
	m.advanceCircuitStateIfDueLocked(metrics, now)
	m.refreshBreakerWindowsLocked(metrics, now)
	return metrics.CircuitState == CircuitStateOpen
}
