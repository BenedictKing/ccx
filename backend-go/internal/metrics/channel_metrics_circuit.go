package metrics

import (
	"log"
	"time"

	"github.com/BenedictKing/ccx/internal/statelog"
)

func (m *MetricsManager) refreshBreakerWindowsLocked(metrics *KeyMetrics, now time.Time) {
	if metrics == nil {
		return
	}
	cutoff := now.Add(-defaultBreakerHealthWindow)

	recentRecords := make([]bool, 0, m.windowSize)
	breakerRecords := make([]bool, 0, m.windowSize)
	pendingIndexes := make(map[int]struct{}, len(metrics.pendingHistoryIdx))
	for _, idx := range metrics.pendingHistoryIdx {
		pendingIndexes[idx] = struct{}{}
	}
	var consecutiveRetryable int64
	failureModels := make(map[string]struct{})
	for idx, record := range metrics.requestHistory {
		if _, pending := pendingIndexes[idx]; pending {
			continue
		}
		if !record.Timestamp.Before(cutoff) {
			recentRecords = append(recentRecords, record.Success)
			if isBreakerRelevantFailure(record.Success, record.FailureClass) {
				breakerRecords = append(breakerRecords, record.Success)
				if !record.Success {
					failureModels[recordRouteModel(&record)] = struct{}{}
				}
			}
			if record.Success {
				consecutiveRetryable = 0
			} else if normalizeFailureClass(false, record.FailureClass).IsBreakerRelevant() {
				consecutiveRetryable++
			}
		}
	}

	if len(recentRecords) > m.windowSize {
		recentRecords = recentRecords[len(recentRecords)-m.windowSize:]
	}
	if len(breakerRecords) > m.windowSize {
		breakerRecords = breakerRecords[len(breakerRecords)-m.windowSize:]
	}

	metrics.recentResults = append(metrics.recentResults[:0], recentRecords...)
	metrics.breakerResults = append(metrics.breakerResults[:0], breakerRecords...)
	metrics.breakerFailureModels = failureModels
	metrics.ConsecutiveFailures = consecutiveRetryable
}

func (m *MetricsManager) calculateKeyBreakerFailureRateInternal(metrics *KeyMetrics) float64 {
	if len(metrics.breakerResults) == 0 {
		return 0
	}
	failures := 0
	for _, success := range metrics.breakerResults {
		if !success {
			failures++
		}
	}
	return float64(failures) / float64(len(metrics.breakerResults))
}

func (m *MetricsManager) nextBackoffDuration(level int) time.Duration {
	if level < 0 {
		level = 0
	}
	delay := m.circuitBackoffBase
	for i := 0; i < level; i++ {
		delay *= 2
		if delay >= m.circuitBackoffMax {
			return m.circuitBackoffMax
		}
	}
	if delay > m.circuitBackoffMax {
		return m.circuitBackoffMax
	}
	return delay
}

func (m *MetricsManager) persistCircuitStateLocked(metrics *KeyMetrics) {
	if m.store == nil || metrics == nil {
		return
	}
	var circuitOpenedAt *time.Time
	if metrics.CircuitBrokenAt != nil {
		t := *metrics.CircuitBrokenAt
		circuitOpenedAt = &t
	}
	var halfOpenAt *time.Time
	if metrics.HalfOpenAt != nil {
		t := *metrics.HalfOpenAt
		halfOpenAt = &t
	}
	var nextRetryAt *time.Time
	if metrics.NextRetryAt != nil {
		t := *metrics.NextRetryAt
		nextRetryAt = &t
	}
	if err := m.store.UpsertCircuitState(PersistentCircuitState{
		MetricsKey:          metrics.MetricsKey,
		BaseURL:             metrics.BaseURL,
		KeyMask:             metrics.KeyMask,
		APIType:             m.apiType,
		CircuitState:        metrics.CircuitState.String(),
		CircuitOpenedAt:     circuitOpenedAt,
		HalfOpenAt:          halfOpenAt,
		NextRetryAt:         nextRetryAt,
		BackoffLevel:        metrics.BackoffLevel,
		HalfOpenSuccesses:   metrics.HalfOpenSuccesses,
		ConsecutiveFailures: metrics.ConsecutiveFailures,
	}); err != nil {
		log.Printf("[Metrics-Circuit] 警告: 持久化熔断状态失败 (key=%s, state=%s): %v", metrics.KeyMask, metrics.CircuitState.String(), err)
	}
}

func (m *MetricsManager) resetCircuitStateLocked(metrics *KeyMetrics, clearBreakerWindow bool) {
	metrics.CircuitState = CircuitStateClosed
	metrics.CircuitBrokenAt = nil
	metrics.HalfOpenAt = nil
	metrics.NextRetryAt = nil
	metrics.BackoffLevel = 0
	metrics.HalfOpenSuccesses = 0
	metrics.ProbeInFlight = false
	metrics.ConsecutiveFailures = 0
	if clearBreakerWindow {
		metrics.breakerResults = make([]bool, 0, m.windowSize)
	}
	m.persistCircuitStateLocked(metrics)
}

func (m *MetricsManager) moveCircuitToOpenLocked(metrics *KeyMetrics, now time.Time, escalate bool) {
	if escalate {
		metrics.BackoffLevel++
	}
	delay := m.nextBackoffDuration(metrics.BackoffLevel)
	nextRetryAt := now.Add(delay)
	metrics.CircuitState = CircuitStateOpen
	metrics.CircuitBrokenAt = &now
	metrics.HalfOpenAt = nil
	metrics.NextRetryAt = &nextRetryAt
	metrics.HalfOpenSuccesses = 0
	metrics.ProbeInFlight = false
	m.persistCircuitStateLocked(metrics)
}

func (m *MetricsManager) moveCircuitToHalfOpenLocked(metrics *KeyMetrics, now time.Time) {
	metrics.CircuitState = CircuitStateHalfOpen
	metrics.HalfOpenAt = &now
	metrics.NextRetryAt = nil
	metrics.HalfOpenSuccesses = 0
	metrics.ProbeInFlight = false
	m.persistCircuitStateLocked(metrics)
}

func (m *MetricsManager) advanceCircuitStateIfDueLocked(metrics *KeyMetrics, now time.Time) {
	if metrics == nil {
		return
	}
	if metrics.CircuitState == CircuitStateOpen && metrics.NextRetryAt != nil && !now.Before(*metrics.NextRetryAt) {
		m.moveCircuitToHalfOpenLocked(metrics, now)
	}
}

func (m *MetricsManager) handleBreakerSuccessLocked(metrics *KeyMetrics, now time.Time) {
	m.advanceCircuitStateIfDueLocked(metrics, now)
	m.refreshBreakerWindowsLocked(metrics, now)
	metrics.ConsecutiveFailures = 0

	switch metrics.CircuitState {
	case CircuitStateHalfOpen:
		metrics.HalfOpenSuccesses++
		if metrics.HalfOpenSuccesses >= m.halfOpenSuccessTarget {
			m.resetCircuitStateLocked(metrics, true)
			log.Printf("[Metrics-Circuit] Key [%s] (%s) half-open 探针成功，恢复 closed", metrics.KeyMask, metrics.BaseURL)
			statelog.LogStateTransition("Metrics-Circuit", "key", metrics.KeyMask, "half_open", "closed", "probe_success", "baseURL="+metrics.BaseURL)
		} else {
			m.persistCircuitStateLocked(metrics)
		}
	case CircuitStateOpen:
		m.resetCircuitStateLocked(metrics, true)
		log.Printf("[Metrics-Circuit] Key [%s] (%s) 因请求成功退出熔断状态", metrics.KeyMask, metrics.BaseURL)
	default:
		// closed 状态成功仅更新内存统计，不需要同步持久化熔断状态
	}
}

func (m *MetricsManager) handleBreakerFailureLocked(metrics *KeyMetrics, failureClass FailureClass, now time.Time) {
	failureClass = normalizeFailureClass(false, failureClass)
	m.advanceCircuitStateIfDueLocked(metrics, now)
	m.refreshBreakerWindowsLocked(metrics, now)
	if !failureClass.IsBreakerRelevant() {
		// 非 breaker 相关失败仅更新观测统计，不需要同步持久化熔断状态
		return
	}

	switch metrics.CircuitState {
	case CircuitStateHalfOpen:
		m.moveCircuitToOpenLocked(metrics, now, true)
		log.Printf("[Metrics-Circuit] Key [%s] (%s) half-open 探针失败，重新进入 open（失败率: %.1f%%）", metrics.KeyMask, metrics.BaseURL, m.calculateKeyBreakerFailureRateInternal(metrics)*100)
	case CircuitStateClosed:
		// 模型多样性门槛：失败能明确归因到单一模型时不熔断整个 Key，
		// 避免单个模型故障（如某模型 503）殃及同 Key 下其他健康模型。
		// 无法归因（模型名缺失）时不拦截，回退到原始阈值判定。
		//
		// 这里只负责"豁免 Key 级熔断"，被豁免掉的那一份惩罚由 ModelCircuitTracker
		// （model_circuit.go）以 (channelUID, keyHash, model) 粒度承担——否则失败越
		// 集中在单一模型，约束反而越彻底消失，调度器会无止境地反复首选故障组合。
		// 模型级记账由 handler 侧完成：那里手上已有 channelUID/keyHash/attemptModel
		// 三个键，无需从 metrics 内部反查。
		if m.isSingleModelFailureLocked(metrics) {
			return
		}
		if failureClass.OpensCircuitImmediately() {
			m.moveCircuitToOpenLocked(metrics, now, false)
			log.Printf("[Metrics-Circuit] Key [%s] (%s) 因上游临时过载进入熔断状态（失败率: %.1f%%）", metrics.KeyMask, metrics.BaseURL, m.calculateKeyBreakerFailureRateInternal(metrics)*100)
			statelog.LogStateTransition("Metrics-Circuit", "key", metrics.KeyMask, "closed", "open", "overloaded", "baseURL="+metrics.BaseURL)
			return
		}
		if m.isKeyCircuitBroken(metrics) {
			m.moveCircuitToOpenLocked(metrics, now, false)
			log.Printf("[Metrics-Circuit] Key [%s] (%s) 进入熔断状态（失败率: %.1f%%）", metrics.KeyMask, metrics.BaseURL, m.calculateKeyBreakerFailureRateInternal(metrics)*100)
			statelog.LogStateTransition("Metrics-Circuit", "key", metrics.KeyMask, "closed", "open", "breaker_threshold", "baseURL="+metrics.BaseURL)
		}
	default:
		// open 状态下继续记录内存统计，持久化仅在状态迁移时发生
	}
}

func (m *MetricsManager) isKeyCircuitBroken(metrics *KeyMetrics) bool {
	if metrics == nil {
		return false
	}
	m.refreshBreakerWindowsLocked(metrics, time.Now())
	if metrics.ConsecutiveFailures >= m.consecutiveFailuresThreshold {
		return true
	}
	minRequests := max(3, m.windowSize/2)
	if len(metrics.breakerResults) < minRequests {
		return false
	}
	return m.calculateKeyBreakerFailureRateInternal(metrics) >= m.failureThreshold
}

// isSingleModelFailureLocked 判断 breaker 窗口内的失败是否能明确归因到单一模型。
// 用于模型多样性门槛：当失败全部来自同一个已知模型时返回 true（不熔断 Key），
// 避免单一模型故障（如某模型临时 503）导致同 Key 下其他健康模型不可用。
// 模型名缺失（空桶）视为无法归因，返回 false，使熔断回退到原始阈值判定。
func (m *MetricsManager) isSingleModelFailureLocked(metrics *KeyMetrics) bool {
	if metrics == nil || len(metrics.breakerFailureModels) != 1 {
		return false
	}
	for model := range metrics.breakerFailureModels {
		return model != ""
	}
	return false
}

// calculateKeyFailureRateInternal 计算 Key 综合失败率（内部方法，调用前需持有锁）

// cleanupCircuitBreakers 后台任务：推进到期的熔断状态并清理过期指标
func (m *MetricsManager) cleanupCircuitBreakers() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	cleanupTicker := time.NewTicker(1 * time.Hour)
	defer cleanupTicker.Stop()

	for {
		select {
		case <-ticker.C:
			m.recoverExpiredCircuitBreakers()
		case <-cleanupTicker.C:
			m.cleanupStaleKeys()
			m.modelCircuit.Cleanup()
		case <-m.stopCh:
			return
		}
	}
}

// recoverExpiredCircuitBreakers 推进超时的熔断 Key（open -> half_open）。
func (m *MetricsManager) recoverExpiredCircuitBreakers() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	for _, metrics := range m.keyMetrics {
		m.advanceCircuitStateIfDueLocked(metrics, now)
	}
}

// cleanupStaleKeys 清理过期的 Key 指标（超过 48 小时无活动）
func (m *MetricsManager) cleanupStaleKeys() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	staleThreshold := 48 * time.Hour
	var removedMetricsKeys []string
	var removed []string

	for key, metrics := range m.keyMetrics {
		var lastActivity time.Time
		if metrics.LastSuccessAt != nil {
			lastActivity = *metrics.LastSuccessAt
		}
		if metrics.LastFailureAt != nil && metrics.LastFailureAt.After(lastActivity) {
			lastActivity = *metrics.LastFailureAt
		}

		if lastActivity.IsZero() || now.Sub(lastActivity) > staleThreshold {
			delete(m.keyMetrics, key)
			removedMetricsKeys = append(removedMetricsKeys, key)
			removed = append(removed, metrics.KeyMask)
		}
	}

	if m.store != nil && len(removedMetricsKeys) > 0 {
		if _, err := m.store.DeleteCircuitStatesByMetricsKeys(removedMetricsKeys, m.apiType); err != nil {
			log.Printf("[Metrics-Cleanup] 警告: 删除过期熔断状态失败: %v", err)
		}
	}

	if len(removed) > 0 {
		log.Printf("[Metrics-Cleanup] 清理了 %d 个过期 Key 指标: %v", len(removed), removed)
	}
}

// GetCircuitRecoveryTime 获取基础熔断冷却时间（兼容旧接口）
func (m *MetricsManager) GetCircuitRecoveryTime() time.Duration {
	return m.circuitRecoveryTime
}

// GetCircuitBackoffBase 获取 breaker 基础退避时间。
func (m *MetricsManager) GetCircuitBackoffBase() time.Duration {
	return m.circuitBackoffBase
}

// GetCircuitBackoffMax 获取 breaker 最大退避时间。
func (m *MetricsManager) GetCircuitBackoffMax() time.Duration {
	return m.circuitBackoffMax
}

// GetHalfOpenSuccessTarget 获取 half-open 恢复所需连续成功次数。
func (m *MetricsManager) GetHalfOpenSuccessTarget() int {
	return m.halfOpenSuccessTarget
}

// GetConsecutiveRetryableFailuresThreshold 获取连续可重试失败触发阈值。
// GetConsecutiveRetryableFailuresThreshold 获取连续可重试失败触发阈值。
func (m *MetricsManager) GetConsecutiveRetryableFailuresThreshold() int64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.consecutiveFailuresThreshold
}

// GetFailureThreshold 获取失败率阈值
// GetFailureThreshold 获取失败率阈值
func (m *MetricsManager) GetFailureThreshold() float64 {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.failureThreshold
}

// GetWindowSize 获取滑动窗口大小
// GetWindowSize 获取滑动窗口大小
func (m *MetricsManager) GetWindowSize() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.windowSize
}

// CircuitBreakerParams 熔断器可配置参数
type CircuitBreakerParams struct {
	WindowSize                   int     `json:"windowSize"`
	FailureThreshold             float64 `json:"failureThreshold"`
	ConsecutiveFailuresThreshold int64   `json:"consecutiveFailuresThreshold"`
	// 流式健康检测参数
	StreamFirstContentTimeoutMs int `json:"streamFirstContentTimeoutMs"` // HTTP 200 后首个有效内容等待超时（ms，5000-300000）
	StreamInactivityTimeoutMs   int `json:"streamInactivityTimeoutMs"`   // 首字后连续性确认窗口（ms，1000-180000）
	StreamToolCallIdleTimeoutMs int `json:"streamToolCallIdleTimeoutMs"` // 工具调用空闲超时（ms，30000-300000）
}

// GetCircuitBreakerConfig 获取当前运行时生效的熔断器配置
func (m *MetricsManager) GetCircuitBreakerConfig() CircuitBreakerParams {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return CircuitBreakerParams{
		WindowSize:                   m.windowSize,
		FailureThreshold:             m.failureThreshold,
		ConsecutiveFailuresThreshold: m.consecutiveFailuresThreshold,
		StreamFirstContentTimeoutMs:  m.streamFirstContentTimeoutMs,
		StreamInactivityTimeoutMs:    m.streamInactivityTimeoutMs,
		StreamToolCallIdleTimeoutMs:  m.streamToolCallIdleTimeoutMs,
	}
}

// UpdateCircuitBreakerConfig 原子更新熔断器配置
func (m *MetricsManager) UpdateCircuitBreakerConfig(params CircuitBreakerParams) {
	if params.WindowSize < 3 {
		params.WindowSize = 3
	}
	if params.FailureThreshold <= 0 || params.FailureThreshold > 1 {
		params.FailureThreshold = 0.5
	}
	if params.ConsecutiveFailuresThreshold < 1 {
		params.ConsecutiveFailuresThreshold = defaultConsecutiveRetryableFailuresThreshold
	}
	if params.StreamFirstContentTimeoutMs < 5000 {
		params.StreamFirstContentTimeoutMs = 5000
	} else if params.StreamFirstContentTimeoutMs > 300000 {
		params.StreamFirstContentTimeoutMs = 300000
	}
	if params.StreamInactivityTimeoutMs < 1000 {
		params.StreamInactivityTimeoutMs = 1000
	} else if params.StreamInactivityTimeoutMs > 180000 {
		params.StreamInactivityTimeoutMs = 180000
	}
	if params.StreamToolCallIdleTimeoutMs < 30000 {
		params.StreamToolCallIdleTimeoutMs = 30000
	} else if params.StreamToolCallIdleTimeoutMs > 300000 {
		params.StreamToolCallIdleTimeoutMs = 300000
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.windowSize = params.WindowSize
	m.failureThreshold = params.FailureThreshold
	m.consecutiveFailuresThreshold = params.ConsecutiveFailuresThreshold
	m.streamFirstContentTimeoutMs = params.StreamFirstContentTimeoutMs
	m.streamInactivityTimeoutMs = params.StreamInactivityTimeoutMs
	m.streamToolCallIdleTimeoutMs = params.StreamToolCallIdleTimeoutMs
}
