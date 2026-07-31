package metrics

import "time"

// processStartedAt 记录进程启动时间。渠道日志是内存态（ChannelLogStore 无持久化），
// 而 breaker 状态走 SQLite 持久化并在启动时由 LoadCircuitStates 恢复，两者生命周期不一致：
// 重启后已熔断渠道会呈现"无日志却熔断"。该时间戳用于判定熔断依据是否早于本次启动，
// 从而让前端能明确交代"日志已随重启清空"，而不是把熔断显示成无来由的黑盒。
var processStartedAt = time.Now()

// BreakerEvidence 描述某渠道当前熔断态的成因依据，供管理界面解释熔断来源。
type BreakerEvidence struct {
	CircuitState        string     `json:"circuitState"`
	LastFailureAt       *time.Time `json:"lastFailureAt,omitempty"`
	CircuitBrokenAt     *time.Time `json:"circuitBrokenAt,omitempty"`
	NextRetryAt         *time.Time `json:"nextRetryAt,omitempty"`
	BackoffLevel        int        `json:"backoffLevel"`
	ConsecutiveFailures int64      `json:"consecutiveFailures"`
	// PredatesRestart 为 true 表示熔断依据的最近失败发生在本次进程启动之前，
	// 对应日志已被重启清空的情形。
	PredatesRestart  bool      `json:"predatesRestart"`
	ProcessStartedAt time.Time `json:"processStartedAt"`
}

// GetBreakerEvidenceByMetricsKeys 汇总给定 metricsKeys 中最"坏"的熔断态作为渠道级依据。
// 一个渠道可对应多个 baseURL × key 的 metricsKey，取最严重状态（open > half_open > closed）
// 并在同状态下取最近一次失败时间，与界面上的渠道级熔断展示口径一致。
// 未命中任何 metricsKey 时返回 nil。
func (m *MetricsManager) GetBreakerEvidenceByMetricsKeys(metricsKeys []string) *BreakerEvidence {
	if len(metricsKeys) == 0 {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	var best *KeyMetrics
	var bestState CircuitState

	severity := func(state CircuitState) int {
		switch state {
		case CircuitStateOpen:
			return 2
		case CircuitStateHalfOpen:
			return 1
		default:
			return 0
		}
	}

	for _, metricsKey := range metricsKeys {
		keyMetrics, ok := m.keyMetrics[metricsKey]
		if !ok || keyMetrics == nil {
			continue
		}
		m.advanceCircuitStateIfDueLocked(keyMetrics, now)
		m.refreshBreakerWindowsLocked(keyMetrics, now)
		state := keyMetrics.CircuitState

		if best == nil || severity(state) > severity(bestState) {
			best, bestState = keyMetrics, state
			continue
		}
		// 同严重度下取最近失败者，避免多 key 渠道展示过期依据
		if severity(state) == severity(bestState) && keyMetrics.LastFailureAt != nil &&
			(best.LastFailureAt == nil || keyMetrics.LastFailureAt.After(*best.LastFailureAt)) {
			best, bestState = keyMetrics, state
		}
	}

	if best == nil {
		return nil
	}

	evidence := &BreakerEvidence{
		CircuitState:        bestState.String(),
		LastFailureAt:       best.LastFailureAt,
		CircuitBrokenAt:     best.CircuitBrokenAt,
		NextRetryAt:         best.NextRetryAt,
		BackoffLevel:        best.BackoffLevel,
		ConsecutiveFailures: best.ConsecutiveFailures,
		ProcessStartedAt:    processStartedAt,
	}
	// 仅非 closed 态才需要解释成因：closed 渠道日志为空是正常的（没请求过）
	if bestState != CircuitStateClosed && best.LastFailureAt != nil {
		evidence.PredatesRestart = best.LastFailureAt.Before(processStartedAt)
	}
	return evidence
}
