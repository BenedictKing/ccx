package metrics

import (
	"time"

	"github.com/BenedictKing/ccx/internal/types"
)

// RecordSuccess 记录成功请求（新方法，使用 baseURL + apiKey）
func (m *MetricsManager) RecordSuccess(baseURL, apiKey, serviceType string) {
	m.RecordSuccessWithUsage(baseURL, apiKey, serviceType, nil)
}

// RecordSuccessWithUsage 记录成功请求（带 Usage 数据）
func (m *MetricsManager) RecordSuccessWithUsage(baseURL, apiKey, serviceType string, usage *types.Usage) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.recordSuccessWithUsageLocked(baseURL, apiKey, serviceType, usage, time.Now())
}

func (m *MetricsManager) recordSuccessWithUsageLocked(baseURL, apiKey, serviceType string, usage *types.Usage, now time.Time) {
	metrics := m.getWritableMetricsLocked(baseURL, apiKey, serviceType)
	metrics.RequestCount++
	metrics.SuccessCount++
	metrics.LastSuccessAt = &now

	inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens := extractUsageTokens(usage)

	m.appendToHistoryKeyWithUsage(metrics, now, true, FailureClassNone, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens)
	m.handleBreakerSuccessLocked(metrics, now)

	if m.store != nil {
		m.store.AddRecord(PersistentRecord{
			ChannelUID:          "",
			RouteModel:          "",
			MetricsKey:          metrics.MetricsKey,
			BaseURL:             metrics.BaseURL,
			KeyMask:             metrics.KeyMask,
			Timestamp:           now,
			Success:             true,
			FailureClass:        FailureClassNone,
			InputTokens:         inputTokens,
			OutputTokens:        outputTokens,
			CacheCreationTokens: cacheCreationTokens,
			CacheReadTokens:     cacheReadTokens,
			APIType:             m.apiType,
		})
	}
}

// RecordFailure 记录失败请求（新方法，使用 baseURL + apiKey）
func (m *MetricsManager) RecordFailure(baseURL, apiKey, serviceType string) {
	m.RecordFailureWithClass(baseURL, apiKey, serviceType, FailureClassRetryable)
}

// RecordFailureWithClass 记录失败请求并指定失败分类。
func (m *MetricsManager) RecordFailureWithClass(baseURL, apiKey, serviceType string, failureClass FailureClass) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.recordFailureLocked(baseURL, apiKey, serviceType, normalizeFailureClass(false, failureClass), time.Now())
}

func (m *MetricsManager) recordFailureLocked(baseURL, apiKey, serviceType string, failureClass FailureClass, now time.Time) {
	metrics := m.getWritableMetricsLocked(baseURL, apiKey, serviceType)
	metrics.RequestCount++
	metrics.FailureCount++
	metrics.LastFailureAt = &now

	m.appendToHistoryKey(metrics, now, false, normalizeFailureClass(false, failureClass))
	m.handleBreakerFailureLocked(metrics, failureClass, now)

	if m.store != nil {
		m.store.AddRecord(PersistentRecord{
			ChannelUID:          "",
			RouteModel:          "",
			MetricsKey:          metrics.MetricsKey,
			BaseURL:             metrics.BaseURL,
			KeyMask:             metrics.KeyMask,
			Timestamp:           now,
			Success:             false,
			FailureClass:        normalizeFailureClass(false, failureClass),
			InputTokens:         0,
			OutputTokens:        0,
			CacheCreationTokens: 0,
			CacheReadTokens:     0,
			APIType:             m.apiType,
		})
	}
}

// RecordRequestConnected 记录”开始发起上游请求（TCP 建连阶段）”的请求（用于更实时的活跃度统计）。
// 返回 requestID，用于后续在请求结束时回写成功/失败与 token。
func (m *MetricsManager) RecordRequestConnected(baseURL, apiKey, serviceType string, model string) uint64 {
	return m.RecordRequestConnectedAt(baseURL, apiKey, serviceType, model, time.Now())
}

// RecordRequestConnectedWithProxyKeyMask 记录请求开始并关联代理 Key 掩码（用于成本报表按用户分组）。
// 保持与 RecordRequestConnected 相同的行为，额外将 proxyKeyMask 存入 pending 记录，
// 后续 RecordRequestFinalizeOutcome 会将其写入 PersistentRecord 持久化到 SQLite。
func (m *MetricsManager) RecordRequestConnectedWithProxyKeyMask(baseURL, apiKey, serviceType, model, proxyKeyMask string) uint64 {
	return m.recordRequestConnectedInternal(baseURL, apiKey, serviceType, "", model, model, proxyKeyMask, time.Now())
}

// RecordRequestConnectedWithContext 记录请求开始，携带完整 breaker 身份。
//
// channelUID 与 routeModel 是 breaker scope 三元组的两个维度，keyHash 在调用方计算。
// actualModel 是实际发给上游的模型（可被 autopilot 映射改写），继续写入 RequestRecord.Model
// 供成本报表与真实模型统计；routeModel 写入 RequestRecord.RouteModel 供 breaker 聚合窗口。
func (m *MetricsManager) RecordRequestConnectedWithContext(baseURL, apiKey, serviceType, channelUID, actualModel, routeModel, proxyKeyMask string) uint64 {
	return m.recordRequestConnectedInternal(baseURL, apiKey, serviceType, channelUID, actualModel, routeModel, proxyKeyMask, time.Now())
}

// RecordRequestConnectedAt 与 RecordRequestConnected 相同，但允许注入时间戳（用于测试）。
func (m *MetricsManager) RecordRequestConnectedAt(baseURL, apiKey, serviceType string, model string, timestamp time.Time) uint64 {
	return m.recordRequestConnectedInternal(baseURL, apiKey, serviceType, "", model, model, "", timestamp)
}

// RecordRequestConnectionLatency 记录从发起上游请求到取得连接（httptrace.GotConn）的耗时。
// 连接复用同样会产生样本；同一请求仅保留第一次连接事件。
func (m *MetricsManager) RecordRequestConnectionLatency(baseURL, apiKey, serviceType string, requestID uint64, latency time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	metrics := m.findPendingRequestMetricsLocked(baseURL, apiKey, serviceType, requestID)
	if metrics == nil {
		return
	}
	idx, ok := metrics.pendingHistoryIdx[requestID]
	if !ok || idx < 0 || idx >= len(metrics.requestHistory) {
		return
	}
	record := &metrics.requestHistory[idx]
	if record.ConnectLatencyMs > 0 {
		return
	}
	latencyMs := latency.Milliseconds()
	if latencyMs < 1 {
		latencyMs = 1
	}
	record.ConnectLatencyMs = latencyMs
}

// RecordRequestFirstByte 记录从发起上游请求到收到首个响应字节的耗时。
// 该值只写入当前进程的滑动历史；未收到响应头的超时/取消请求不产生样本。
func (m *MetricsManager) RecordRequestFirstByte(baseURL, apiKey, serviceType string, requestID uint64, latency time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()

	metrics := m.findPendingRequestMetricsLocked(baseURL, apiKey, serviceType, requestID)
	if metrics == nil {
		return
	}
	idx, ok := metrics.pendingHistoryIdx[requestID]
	if !ok || idx < 0 || idx >= len(metrics.requestHistory) {
		return
	}
	record := &metrics.requestHistory[idx]
	if record.FirstByteLatencyMs > 0 {
		return
	}
	latencyMs := latency.Milliseconds()
	if latencyMs < 1 {
		latencyMs = 1
	}
	record.FirstByteLatencyMs = latencyMs
}

func (m *MetricsManager) recordRequestConnectedInternal(baseURL, apiKey, serviceType, channelUID, actualModel, routeModel, proxyKeyMask string, timestamp time.Time) uint64 {
	m.mu.Lock()
	defer m.mu.Unlock()

	metrics := m.getWritableMetricsLocked(baseURL, apiKey, serviceType)
	m.advanceCircuitStateIfDueLocked(metrics, timestamp)

	m.nextRequestID++
	requestID := m.nextRequestID

	if metrics.pendingHistoryIdx == nil {
		metrics.pendingHistoryIdx = make(map[uint64]int)
	}

	metrics.requestHistory = append(metrics.requestHistory, RequestRecord{
		ChannelUID:   channelUID,
		RouteModel:   routeModel,
		Model:        actualModel,
		Timestamp:    timestamp,
		Success:      true, // 先按成功计数；结束时会回写真实结果
		FailureClass: FailureClassNone,
		ProxyKeyMask: proxyKeyMask,
	})
	metrics.pendingHistoryIdx[requestID] = len(metrics.requestHistory) - 1

	m.cleanupHistoryLocked(metrics)

	return requestID
}

// RecordRequestFinalizeSuccess 回写成功结果与 token（requestID 来自 RecordRequestConnected）。
func (m *MetricsManager) RecordRequestFinalizeSuccess(baseURL, apiKey, serviceType string, requestID uint64, usage *types.Usage) {
	m.RecordRequestFinalizeOutcome(baseURL, apiKey, serviceType, requestID, true, FailureClassNone, usage)
}

// RecordRequestFinalizeFailure 回写失败结果（requestID 来自 RecordRequestConnected）。
func (m *MetricsManager) RecordRequestFinalizeFailure(baseURL, apiKey, serviceType string, requestID uint64) {
	m.RecordRequestFinalizeFailureWithClass(baseURL, apiKey, serviceType, requestID, FailureClassRetryable)
}

// RecordRequestFinalizeFailureWithClass 回写失败结果并显式指定失败分类。
func (m *MetricsManager) RecordRequestFinalizeFailureWithClass(baseURL, apiKey, serviceType string, requestID uint64, failureClass FailureClass) {
	m.RecordRequestFinalizeOutcome(baseURL, apiKey, serviceType, requestID, false, failureClass, nil)
}

// RecordRequestFinalizeOutcome 根据最终结果统一回写请求指标与 breaker 状态。
func (m *MetricsManager) RecordRequestFinalizeOutcome(baseURL, apiKey, serviceType string, requestID uint64, success bool, failureClass FailureClass, usage *types.Usage) {
	m.mu.Lock()
	defer m.mu.Unlock()

	metrics := m.findPendingRequestMetricsLocked(baseURL, apiKey, serviceType, requestID)
	if metrics == nil {
		metrics = m.getFirstMatchingMetricsLocked(baseURL, apiKey, serviceType)
	}
	if metrics != nil && metrics.MetricsKey != m.metricsIdentityKey(baseURL, apiKey, serviceType) {
		metrics = m.getOrCreateKey(baseURL, apiKey, serviceType)
	}
	if metrics == nil {
		if success {
			m.recordSuccessWithUsageLocked(baseURL, apiKey, serviceType, usage, time.Now())
		} else {
			m.recordFailureLocked(baseURL, apiKey, serviceType, normalizeFailureClass(false, failureClass), time.Now())
		}
		return
	}

	idx, ok := metrics.pendingHistoryIdx[requestID]
	if !ok || idx < 0 || idx >= len(metrics.requestHistory) {
		if success {
			m.recordSuccessWithUsageLocked(baseURL, apiKey, serviceType, usage, time.Now())
		} else {
			m.recordFailureLocked(baseURL, apiKey, serviceType, normalizeFailureClass(false, failureClass), time.Now())
		}
		return
	}
	delete(metrics.pendingHistoryIdx, requestID)

	now := time.Now()
	metrics.RequestCount++
	record := &metrics.requestHistory[idx]
	record.Success = success
	record.FailureClass = normalizeFailureClass(success, failureClass)

	if success {
		metrics.SuccessCount++
		metrics.LastSuccessAt = &now
		m.handleBreakerSuccessLocked(metrics, now)

		inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens := extractUsageTokens(usage)
		record.InputTokens = inputTokens
		record.OutputTokens = outputTokens
		record.CacheCreationInputTokens = cacheCreationTokens
		record.CacheReadInputTokens = cacheReadTokens

		if m.store != nil {
			m.store.AddRecord(PersistentRecord{
				ChannelUID:          record.ChannelUID,
				RouteModel:          record.RouteModel,
				MetricsKey:          metrics.MetricsKey,
				BaseURL:             metrics.BaseURL,
				KeyMask:             metrics.KeyMask,
				Timestamp:           record.Timestamp,
				Success:             true,
				FailureClass:        FailureClassNone,
				InputTokens:         inputTokens,
				OutputTokens:        outputTokens,
				CacheCreationTokens: cacheCreationTokens,
				CacheReadTokens:     cacheReadTokens,
				APIType:             m.apiType,
				Model:               record.Model,
				ProxyKeyMask:        record.ProxyKeyMask,
			})
		}
		return
	}

	failureClass = normalizeFailureClass(false, failureClass)
	metrics.FailureCount++
	metrics.LastFailureAt = &now
	m.handleBreakerFailureLocked(metrics, failureClass, now)
	record.InputTokens = 0
	record.OutputTokens = 0
	record.CacheCreationInputTokens = 0
	record.CacheReadInputTokens = 0

	if m.store != nil {
		m.store.AddRecord(PersistentRecord{
			MetricsKey:          metrics.MetricsKey,
			BaseURL:             metrics.BaseURL,
			KeyMask:             metrics.KeyMask,
			Timestamp:           record.Timestamp,
			Success:             false,
			FailureClass:        failureClass,
			InputTokens:         0,
			OutputTokens:        0,
			CacheCreationTokens: 0,
			CacheReadTokens:     0,
			APIType:             m.apiType,
			Model:               record.Model,
			ProxyKeyMask:        record.ProxyKeyMask,
		})
	}
}

// RecordRequestFinalizeClientCancel 记录客户端取消的请求（计入总请求数但不计入失败）
func (m *MetricsManager) RecordRequestFinalizeClientCancel(baseURL, apiKey, serviceType string, requestID uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	metrics := m.findPendingRequestMetricsLocked(baseURL, apiKey, serviceType, requestID)
	if metrics == nil {
		metrics = m.getFirstMatchingMetricsLocked(baseURL, apiKey, serviceType)
	}
	if metrics != nil && metrics.MetricsKey != m.metricsIdentityKey(baseURL, apiKey, serviceType) {
		metrics = m.getOrCreateKey(baseURL, apiKey, serviceType)
	}
	if metrics == nil {
		return
	}

	if !m.removePendingRequestRecordLocked(metrics, requestID) {
		return
	}

	// 仅计入总请求数，不计入失败数
	metrics.RequestCount++
	// 注意：不重置 ConsecutiveFailures，客户端取消不应影响连续失败计数
}

// RecordRequestFinalizeIgnored 丢弃内部重试产生的 pending 记录。
// 用于 Header 未写回客户端前的内部重试，不计入请求数、失败率或熔断状态。
func (m *MetricsManager) RecordRequestFinalizeIgnored(baseURL, apiKey, serviceType string, requestID uint64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	metrics := m.findPendingRequestMetricsLocked(baseURL, apiKey, serviceType, requestID)
	if metrics == nil {
		metrics = m.getFirstMatchingMetricsLocked(baseURL, apiKey, serviceType)
	}
	if metrics != nil && metrics.MetricsKey != m.metricsIdentityKey(baseURL, apiKey, serviceType) {
		metrics = m.getOrCreateKey(baseURL, apiKey, serviceType)
	}
	if metrics == nil {
		return
	}

	m.removePendingRequestRecordLocked(metrics, requestID)
}

func (m *MetricsManager) removePendingRequestRecordLocked(metrics *KeyMetrics, requestID uint64) bool {
	idx, ok := metrics.pendingHistoryIdx[requestID]
	if !ok || idx < 0 || idx >= len(metrics.requestHistory) {
		return false
	}
	delete(metrics.pendingHistoryIdx, requestID)

	// 不更新滑动窗口（不影响失败率计算）
	// 不检查熔断状态（客户端取消不应触发熔断）

	// 从历史记录中移除（客户端取消 / 内部重试不记录）
	metrics.requestHistory = append(metrics.requestHistory[:idx], metrics.requestHistory[idx+1:]...)
	// 更新后续索引
	for rid, ridx := range metrics.pendingHistoryIdx {
		if ridx > idx {
			metrics.pendingHistoryIdx[rid] = ridx - 1
		}
	}
	return true
}

// RecordRequestStart 记录请求开始（增加进行中计数）
func (m *MetricsManager) RecordRequestStart(baseURL, apiKey, serviceType string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	metrics := m.getWritableMetricsLocked(baseURL, apiKey, serviceType)
	metrics.ActiveRequests++
}

// RecordRequestEnd 记录请求结束（减少进行中计数）
func (m *MetricsManager) RecordRequestEnd(baseURL, apiKey, serviceType string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	metrics := m.getFirstMatchingMetricsLocked(baseURL, apiKey, serviceType)
	if metrics != nil {
		if metrics.ActiveRequests > 0 {
			metrics.ActiveRequests--
		}
	}
}

// appendToHistoryKey 向 Key 历史记录添加请求（保留24小时）
func (m *MetricsManager) appendToHistoryKey(metrics *KeyMetrics, timestamp time.Time, success bool, failureClass FailureClass) {
	m.appendToHistoryKeyWithUsage(metrics, timestamp, success, failureClass, 0, 0, 0, 0)
}

// cleanupHistoryLocked 清理超过 24 小时的历史记录，并同步修正 pendingHistoryIdx 索引。
// 注意：调用方需要持有写锁。
func (m *MetricsManager) cleanupHistoryLocked(metrics *KeyMetrics) {
	if metrics == nil || len(metrics.requestHistory) == 0 {
		return
	}

	cutoff := time.Now().Add(-24 * time.Hour)

	newStart := -1
	for i, record := range metrics.requestHistory {
		if record.Timestamp.After(cutoff) {
			newStart = i
			break
		}
	}

	if newStart > 0 {
		metrics.requestHistory = metrics.requestHistory[newStart:]
		// 索引平移：老数据被切走后，pending 索引需要整体减去 newStart
		if len(metrics.pendingHistoryIdx) > 0 {
			for id, idx := range metrics.pendingHistoryIdx {
				if idx < newStart {
					delete(metrics.pendingHistoryIdx, id)
					continue
				}
				metrics.pendingHistoryIdx[id] = idx - newStart
			}
		}
		return
	}

	if newStart == -1 {
		// 所有记录都过期，清空切片
		metrics.requestHistory = metrics.requestHistory[:0]
		if metrics.pendingHistoryIdx != nil {
			for id := range metrics.pendingHistoryIdx {
				delete(metrics.pendingHistoryIdx, id)
			}
		}
	}
}

// appendToHistoryKeyWithUsage 向 Key 历史记录添加请求（带 Usage 数据）
func (m *MetricsManager) appendToHistoryKeyWithUsage(metrics *KeyMetrics, timestamp time.Time, success bool, failureClass FailureClass, inputTokens, outputTokens, cacheCreationTokens, cacheReadTokens int64) {
	metrics.requestHistory = append(metrics.requestHistory, RequestRecord{
		Timestamp:                timestamp,
		Success:                  success,
		FailureClass:             normalizeFailureClass(success, failureClass),
		InputTokens:              inputTokens,
		OutputTokens:             outputTokens,
		CacheCreationInputTokens: cacheCreationTokens,
		CacheReadInputTokens:     cacheReadTokens,
	})

	// 清理超过 24 小时的记录
	m.cleanupHistoryLocked(metrics)
}
