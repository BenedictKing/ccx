package autopilot

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/errutil"
)

const (
	routingWindowDuration        = 15 * time.Minute
	autoReadinessLookback        = 24 * time.Hour
	autoReadinessMinSamples      = int64(500)
	autoReadinessMinSuccessRate  = 0.95
	autoReadinessMaxFallbackRate = 0.10
	autoReadinessMaxFailOpenRate = 0.02
	autoReadinessRecentWindow    = time.Hour
	autoReadinessMinBaseline     = int64(100)
	autoReadinessMinRecent       = int64(20)
	autoReadinessMaxP95Ratio     = 1.50

	autoRollbackWindows          = 3
	autoRollbackMinWindowSamples = int64(20)
	autoRollbackBaselineLookback = 7 * 24 * time.Hour
)

var routingLatencyBounds = [...]int64{500, 1000, 2000, 5000, 10000, 30000, 60000}

// RoutingOutcome 是一次渠道选择 trace 的请求结果，不包含 prompt、密钥或响应正文。
type RoutingOutcome struct {
	Terminal           bool
	Success            bool
	ChannelFallback    bool
	StatusCode         int
	RequestDurationMs  int64
	FirstByteLatencyMs int64
	Outcome            string
	CompletedAt        time.Time
}

// RoutingWindowSummary 是一组 15 分钟窗口的无偏聚合结果。
type RoutingWindowSummary struct {
	FirstWindowAt         time.Time `json:"firstWindowAt,omitempty"`
	LastWindowAt          time.Time `json:"lastWindowAt,omitempty"`
	AttemptCount          int64     `json:"attemptCount"`
	RequestCount          int64     `json:"requestCount"`
	SuccessCount          int64     `json:"successCount"`
	FailureCount          int64     `json:"failureCount"`
	CancelledCount        int64     `json:"cancelledCount"`
	AttemptFailureCount   int64     `json:"attemptFailureCount"`
	ChannelFallbackCount  int64     `json:"channelFallbackCount"`
	FailOpenCount         int64     `json:"failOpenCount"`
	LatencySampleCount    int64     `json:"latencySampleCount"`
	FirstByteSampleCount  int64     `json:"firstByteSampleCount"`
	SuccessRate           float64   `json:"successRate"`
	FallbackRate          float64   `json:"fallbackRate"`
	FailOpenRate          float64   `json:"failOpenRate"`
	P95LatencyMs          int64     `json:"p95LatencyMs"`
	P95FirstByteLatencyMs int64     `json:"p95FirstByteLatencyMs"`
	latencyBuckets        [8]int64
	firstByteBuckets      [8]int64
}

// AutoReadinessReport 描述 auto 模式是否达到最小上线门槛。
type AutoReadinessReport struct {
	Ready                    bool                 `json:"ready"`
	RecoveryPending          bool                 `json:"recoveryPending"`
	RequiredSamples          int64                `json:"requiredSamples"`
	RequiredObservationHours int                  `json:"requiredObservationHours"`
	ObservationHours         float64              `json:"observationHours"`
	BlockingReasons          []string             `json:"blockingReasons"`
	SafeModeMetrics          RoutingWindowSummary `json:"safeModeMetrics"`
	RecentMetrics            RoutingWindowSummary `json:"recentMetrics"`
	BaselineMetrics          RoutingWindowSummary `json:"baselineMetrics"`
	LastRollback             *AutoSafetyEvent     `json:"lastRollback,omitempty"`
}

// AutoSafetyEvent 记录 auto 被系统降回 assist 的原因。
type AutoSafetyEvent struct {
	EventUID  string               `json:"eventUid"`
	FromMode  string               `json:"fromMode"`
	ToMode    string               `json:"toMode"`
	Reasons   []string             `json:"reasons"`
	Observed  RoutingWindowSummary `json:"observed"`
	Baseline  RoutingWindowSummary `json:"baseline"`
	CreatedAt time.Time            `json:"createdAt"`
}

func initRoutingSafetySchema(db *sql.DB) error {
	if db == nil {
		return nil
	}
	_, err := db.Exec(`
CREATE TABLE IF NOT EXISTS autopilot_routing_windows (
    window_start TEXT NOT NULL,
    release_id TEXT NOT NULL DEFAULT 'legacy',
    policy_fingerprint TEXT NOT NULL DEFAULT '',
    cohort TEXT NOT NULL DEFAULT 'treatment',
    mode TEXT NOT NULL,
    request_kind TEXT NOT NULL,
    task_class TEXT NOT NULL,
    attempt_count INTEGER NOT NULL DEFAULT 0,
    request_count INTEGER NOT NULL DEFAULT 0,
    success_count INTEGER NOT NULL DEFAULT 0,
    failure_count INTEGER NOT NULL DEFAULT 0,
    cancelled_count INTEGER NOT NULL DEFAULT 0,
    attempt_failure_count INTEGER NOT NULL DEFAULT 0,
    channel_fallback_count INTEGER NOT NULL DEFAULT 0,
    failopen_count INTEGER NOT NULL DEFAULT 0,
    compared_count INTEGER NOT NULL DEFAULT 0,
    matched_count INTEGER NOT NULL DEFAULT 0,
    mismatch_count INTEGER NOT NULL DEFAULT 0,
    uncompared_count INTEGER NOT NULL DEFAULT 0,
    latency_samples INTEGER NOT NULL DEFAULT 0,
    latency_b0 INTEGER NOT NULL DEFAULT 0,
    latency_b1 INTEGER NOT NULL DEFAULT 0,
    latency_b2 INTEGER NOT NULL DEFAULT 0,
    latency_b3 INTEGER NOT NULL DEFAULT 0,
    latency_b4 INTEGER NOT NULL DEFAULT 0,
    latency_b5 INTEGER NOT NULL DEFAULT 0,
    latency_b6 INTEGER NOT NULL DEFAULT 0,
    latency_b7 INTEGER NOT NULL DEFAULT 0,
    first_byte_samples INTEGER NOT NULL DEFAULT 0,
    first_byte_b0 INTEGER NOT NULL DEFAULT 0,
    first_byte_b1 INTEGER NOT NULL DEFAULT 0,
    first_byte_b2 INTEGER NOT NULL DEFAULT 0,
    first_byte_b3 INTEGER NOT NULL DEFAULT 0,
    first_byte_b4 INTEGER NOT NULL DEFAULT 0,
    first_byte_b5 INTEGER NOT NULL DEFAULT 0,
    first_byte_b6 INTEGER NOT NULL DEFAULT 0,
    first_byte_b7 INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (window_start, release_id, policy_fingerprint, cohort, mode, request_kind, task_class)
);
CREATE INDEX IF NOT EXISTS idx_routing_windows_mode_start
    ON autopilot_routing_windows(mode, window_start);
CREATE INDEX IF NOT EXISTS idx_routing_windows_release_start
    ON autopilot_routing_windows(release_id, window_start);
CREATE TABLE IF NOT EXISTS autopilot_auto_safety_events (
    event_uid TEXT PRIMARY KEY,
    from_mode TEXT NOT NULL,
    to_mode TEXT NOT NULL,
    release_id TEXT NOT NULL DEFAULT '',
    policy_fingerprint TEXT NOT NULL DEFAULT '',
    reasons TEXT NOT NULL DEFAULT '[]',
    observed_json TEXT NOT NULL DEFAULT '{}',
    baseline_json TEXT NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_auto_safety_events_created
    ON autopilot_auto_safety_events(created_at);
`)
	return err
}

// RecordOutcome 回填详细 trace，并把终态写入无偏窗口统计。
func (s *TraceStore) RecordOutcome(traceUID string, outcome RoutingOutcome) error {
	if s == nil || traceUID == "" {
		return nil
	}
	if outcome.CompletedAt.IsZero() {
		outcome.CompletedAt = time.Now().UTC()
	}
	if outcome.Outcome == "" {
		switch {
		case !outcome.Terminal:
			outcome.Outcome = "attempt_failed"
		case outcome.Success:
			outcome.Outcome = "success"
		default:
			outcome.Outcome = "upstream_error"
		}
	}

	var snapshot RoutingDecisionTrace
	found := false
	s.mu.Lock()
	for i := len(s.records) - 1; i >= 0; i-- {
		trace := s.records[i]
		if trace.TraceUID != traceUID {
			continue
		}
		if trace.OutcomeRecorded {
			s.mu.Unlock()
			return nil
		}
		trace.OutcomeRecorded = true
		trace.Outcome = outcome.Outcome
		trace.Success = outcome.Success
		trace.ChannelFallback = outcome.ChannelFallback
		trace.StatusCode = outcome.StatusCode
		trace.RequestDurationMs = outcome.RequestDurationMs
		trace.FirstByteLatencyMs = outcome.FirstByteLatencyMs
		completedAt := outcome.CompletedAt.UTC()
		trace.CompletedAt = &completedAt
		snapshot = *trace
		found = true
		break
	}
	s.mu.Unlock()
	if !found {
		return nil
	}

	if s.db != nil {
		// 判断是否需要从 in-flight 索引提升为持久化
		isException := !outcome.Success || outcome.ChannelFallback ||
			(!snapshot.Match && snapshot.ActualChannelUID != "" && snapshot.ShadowChannelUID != "")
		if isException {
			// 异常样本：先从 in-flight 索引提升（若存在），再 UPDATE
			s.promoteInflight(traceUID)
		} else {
			// 非异常：从 in-flight 索引移除
			s.removeInflight(traceUID)
		}

		if _, err := s.db.Exec(`
UPDATE autopilot_routing_traces
SET outcome_recorded = 1, outcome = ?, success = ?, channel_fallback = ?,
    status_code = ?, request_duration_ms = ?, first_byte_latency_ms = ?, completed_at = ?
WHERE trace_uid = ?`,
			outcome.Outcome, boolInt(outcome.Success), boolInt(outcome.ChannelFallback),
			outcome.StatusCode, outcome.RequestDurationMs, outcome.FirstByteLatencyMs,
			outcome.CompletedAt.UTC().Format(time.RFC3339), traceUID,
		); err != nil {
			return fmt.Errorf("[TraceStore-Outcome] 回填 trace 失败 uid=%s: %w", traceUID, err)
		}
		if err := s.recordRoutingWindow(snapshot, outcome); err != nil {
			return err
		}
	}

	// 终态后清除 attempt 序号计数器和 in-flight 索引，避免内存泄漏
	if outcome.Terminal {
		clearAttemptCounter(traceUID)
	}
	return nil
}

func (s *TraceStore) recordRoutingWindow(trace RoutingDecisionTrace, outcome RoutingOutcome) error {
	windowStart := outcome.CompletedAt.UTC().Truncate(routingWindowDuration).Format(time.RFC3339)
	requestInc := int64(0)
	successInc := int64(0)
	failureInc := int64(0)
	cancelledInc := int64(0)
	attemptFailureInc := int64(0)
	fallbackInc := int64(0)
	failOpenInc := int64(0)
	comparedInc := int64(0)
	matchedInc := int64(0)
	mismatchInc := int64(0)
	uncomparedInc := int64(0)
	latencyInc, latencyBuckets := histogramIncrement(0)
	firstByteInc, firstByteBuckets := histogramIncrement(0)

	if !outcome.Terminal {
		attemptFailureInc = 1
	} else if outcome.Outcome == "cancelled" {
		cancelledInc = 1
	} else {
		requestInc = 1
		if outcome.Success {
			successInc = 1
		} else {
			failureInc = 1
		}
		if outcome.ChannelFallback {
			fallbackInc = 1
		}
		if trace.FallbackUsed {
			failOpenInc = 1
		}
		latencyInc, latencyBuckets = histogramIncrement(outcome.RequestDurationMs)
		firstByteInc, firstByteBuckets = histogramIncrement(outcome.FirstByteLatencyMs)
	}

	// v6 comparison 计数
	comparison := ComputeComparisonStatus(trace.ShadowChannelUID, trace.ActualChannelUID, trace.Match)
	switch comparison {
	case ComparisonMatched:
		comparedInc = 1
		matchedInc = 1
	case ComparisonMismatched:
		comparedInc = 1
		mismatchInc = 1
	case ComparisonUncompared:
		uncomparedInc = 1
	}

	releaseID := trace.ReleaseID
	if releaseID == "" {
		releaseID = "legacy"
	}
	cohort := string(trace.Cohort)
	if cohort == "" {
		cohort = "treatment"
	}

	args := []any{
		windowStart, releaseID, trace.PolicyFingerprint, cohort,
		string(trace.Mode), trace.RequestKind, string(trace.TaskClass),
		int64(1), requestInc, successInc, failureInc, cancelledInc, attemptFailureInc,
		fallbackInc, failOpenInc, comparedInc, matchedInc, mismatchInc, uncomparedInc,
		latencyInc,
	}
	for _, value := range latencyBuckets {
		args = append(args, value)
	}
	args = append(args, firstByteInc)
	for _, value := range firstByteBuckets {
		args = append(args, value)
	}

	_, err := s.db.Exec(routingWindowUpsertSQL, args...)
	if err != nil {
		return fmt.Errorf("[TraceStore-Window] 写入窗口失败: %w", err)
	}
	return nil
}

func histogramIncrement(value int64) (int64, [8]int64) {
	var buckets [8]int64
	if value <= 0 {
		return 0, buckets
	}
	index := len(routingLatencyBounds)
	for i, bound := range routingLatencyBounds {
		if value <= bound {
			index = i
			break
		}
	}
	buckets[index] = 1
	return 1, buckets
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

const routingWindowUpsertSQL = `
INSERT INTO autopilot_routing_windows (
    window_start, release_id, policy_fingerprint, cohort,
    mode, request_kind, task_class,
    attempt_count, request_count, success_count, failure_count, cancelled_count,
    attempt_failure_count, channel_fallback_count, failopen_count,
    compared_count, matched_count, mismatch_count, uncompared_count,
    latency_samples, latency_b0, latency_b1, latency_b2, latency_b3, latency_b4, latency_b5, latency_b6, latency_b7,
    first_byte_samples, first_byte_b0, first_byte_b1, first_byte_b2, first_byte_b3, first_byte_b4, first_byte_b5, first_byte_b6, first_byte_b7)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(window_start, release_id, policy_fingerprint, cohort, mode, request_kind, task_class) DO UPDATE SET
    attempt_count = attempt_count + excluded.attempt_count,
    request_count = request_count + excluded.request_count,
    success_count = success_count + excluded.success_count,
    failure_count = failure_count + excluded.failure_count,
    cancelled_count = cancelled_count + excluded.cancelled_count,
    attempt_failure_count = attempt_failure_count + excluded.attempt_failure_count,
    channel_fallback_count = channel_fallback_count + excluded.channel_fallback_count,
    failopen_count = failopen_count + excluded.failopen_count,
    compared_count = compared_count + excluded.compared_count,
    matched_count = matched_count + excluded.matched_count,
    mismatch_count = mismatch_count + excluded.mismatch_count,
    uncompared_count = uncompared_count + excluded.uncompared_count,
    latency_samples = latency_samples + excluded.latency_samples,
    latency_b0 = latency_b0 + excluded.latency_b0,
    latency_b1 = latency_b1 + excluded.latency_b1,
    latency_b2 = latency_b2 + excluded.latency_b2,
    latency_b3 = latency_b3 + excluded.latency_b3,
    latency_b4 = latency_b4 + excluded.latency_b4,
    latency_b5 = latency_b5 + excluded.latency_b5,
    latency_b6 = latency_b6 + excluded.latency_b6,
    latency_b7 = latency_b7 + excluded.latency_b7,
    first_byte_samples = first_byte_samples + excluded.first_byte_samples,
    first_byte_b0 = first_byte_b0 + excluded.first_byte_b0,
    first_byte_b1 = first_byte_b1 + excluded.first_byte_b1,
    first_byte_b2 = first_byte_b2 + excluded.first_byte_b2,
    first_byte_b3 = first_byte_b3 + excluded.first_byte_b3,
    first_byte_b4 = first_byte_b4 + excluded.first_byte_b4,
    first_byte_b5 = first_byte_b5 + excluded.first_byte_b5,
    first_byte_b6 = first_byte_b6 + excluded.first_byte_b6,
    first_byte_b7 = first_byte_b7 + excluded.first_byte_b7
`

func finalizeWindowSummary(summary *RoutingWindowSummary) {
	if summary.RequestCount > 0 {
		summary.SuccessRate = float64(summary.SuccessCount) / float64(summary.RequestCount)
		summary.FallbackRate = float64(summary.ChannelFallbackCount) / float64(summary.RequestCount)
		summary.FailOpenRate = float64(summary.FailOpenCount) / float64(summary.RequestCount)
	}
	summary.P95LatencyMs = histogramPercentile(summary.latencyBuckets, summary.LatencySampleCount, 0.95)
	summary.P95FirstByteLatencyMs = histogramPercentile(summary.firstByteBuckets, summary.FirstByteSampleCount, 0.95)
}

func histogramPercentile(buckets [8]int64, samples int64, percentile float64) int64 {
	if samples <= 0 {
		return 0
	}
	target := int64(math.Ceil(float64(samples) * percentile))
	var cumulative int64
	for index, count := range buckets {
		cumulative += count
		if cumulative < target {
			continue
		}
		if index < len(routingLatencyBounds) {
			return routingLatencyBounds[index]
		}
		return routingLatencyBounds[len(routingLatencyBounds)-1] * 2
	}
	return routingLatencyBounds[len(routingLatencyBounds)-1] * 2
}

func modePlaceholders(modes []RoutingMode) (string, []any) {
	placeholders := make([]string, 0, len(modes))
	args := make([]any, 0, len(modes))
	for _, mode := range modes {
		placeholders = append(placeholders, "?")
		args = append(args, string(mode))
	}
	return strings.Join(placeholders, ","), args
}

type rowScanner interface {
	Scan(dest ...any) error
}

func (s *TraceStore) aggregateRoutingWindows(start, end time.Time, modes ...RoutingMode) (RoutingWindowSummary, error) {
	var summary RoutingWindowSummary
	if s == nil || s.db == nil || len(modes) == 0 {
		return summary, nil
	}
	placeholders, modeArgs := modePlaceholders(modes)
	query := fmt.Sprintf(`
SELECT MIN(window_start), MAX(window_start),
       COALESCE(SUM(attempt_count), 0), COALESCE(SUM(request_count), 0),
       COALESCE(SUM(success_count), 0), COALESCE(SUM(failure_count), 0),
       COALESCE(SUM(cancelled_count), 0), COALESCE(SUM(attempt_failure_count), 0),
       COALESCE(SUM(channel_fallback_count), 0), COALESCE(SUM(failopen_count), 0),
       COALESCE(SUM(latency_samples), 0),
       COALESCE(SUM(latency_b0), 0), COALESCE(SUM(latency_b1), 0),
       COALESCE(SUM(latency_b2), 0), COALESCE(SUM(latency_b3), 0),
       COALESCE(SUM(latency_b4), 0), COALESCE(SUM(latency_b5), 0),
       COALESCE(SUM(latency_b6), 0), COALESCE(SUM(latency_b7), 0),
       COALESCE(SUM(first_byte_samples), 0),
       COALESCE(SUM(first_byte_b0), 0), COALESCE(SUM(first_byte_b1), 0),
       COALESCE(SUM(first_byte_b2), 0), COALESCE(SUM(first_byte_b3), 0),
       COALESCE(SUM(first_byte_b4), 0), COALESCE(SUM(first_byte_b5), 0),
       COALESCE(SUM(first_byte_b6), 0), COALESCE(SUM(first_byte_b7), 0)
FROM autopilot_routing_windows
WHERE window_start >= ? AND window_start < ? AND mode IN (%s)`, placeholders)
	args := []any{start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339)}
	args = append(args, modeArgs...)
	if err := scanRoutingWindowSummary(s.db.QueryRow(query, args...), &summary); err != nil {
		return RoutingWindowSummary{}, err
	}
	return summary, nil
}

// aggregateRoutingWindowsByRelease 按 release/policy/cohort 隔离的窗口聚合。
// 用于 readiness/regression 按发布批次和策略隔离样本。
func (s *TraceStore) aggregateRoutingWindowsByRelease(
	start, end time.Time,
	releaseID, policyFingerprint string,
	cohort string,
	modes ...RoutingMode,
) (RoutingWindowSummary, error) {
	var summary RoutingWindowSummary
	if s == nil || s.db == nil || len(modes) == 0 {
		return summary, nil
	}
	placeholders, modeArgs := modePlaceholders(modes)
	query := fmt.Sprintf(`
SELECT MIN(window_start), MAX(window_start),
       COALESCE(SUM(attempt_count), 0), COALESCE(SUM(request_count), 0),
       COALESCE(SUM(success_count), 0), COALESCE(SUM(failure_count), 0),
       COALESCE(SUM(cancelled_count), 0), COALESCE(SUM(attempt_failure_count), 0),
       COALESCE(SUM(channel_fallback_count), 0), COALESCE(SUM(failopen_count), 0),
       COALESCE(SUM(compared_count), 0), COALESCE(SUM(matched_count), 0),
       COALESCE(SUM(mismatch_count), 0), COALESCE(SUM(uncompared_count), 0),
       COALESCE(SUM(latency_samples), 0),
       COALESCE(SUM(latency_b0), 0), COALESCE(SUM(latency_b1), 0),
       COALESCE(SUM(latency_b2), 0), COALESCE(SUM(latency_b3), 0),
       COALESCE(SUM(latency_b4), 0), COALESCE(SUM(latency_b5), 0),
       COALESCE(SUM(latency_b6), 0), COALESCE(SUM(latency_b7), 0),
       COALESCE(SUM(first_byte_samples), 0),
       COALESCE(SUM(first_byte_b0), 0), COALESCE(SUM(first_byte_b1), 0),
       COALESCE(SUM(first_byte_b2), 0), COALESCE(SUM(first_byte_b3), 0),
       COALESCE(SUM(first_byte_b4), 0), COALESCE(SUM(first_byte_b5), 0),
       COALESCE(SUM(first_byte_b6), 0), COALESCE(SUM(first_byte_b7), 0)
FROM autopilot_routing_windows
WHERE window_start >= ? AND window_start < ? AND mode IN (%s)`, placeholders)

	args := []any{start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339)}
	args = append(args, modeArgs...)
	if releaseID != "" {
		query += " AND release_id = ?"
		args = append(args, releaseID)
	}
	if policyFingerprint != "" {
		query += " AND policy_fingerprint = ?"
		args = append(args, policyFingerprint)
	}
	if cohort != "" {
		query += " AND cohort = ?"
		args = append(args, cohort)
	}

	if err := scanRoutingWindowSummary(s.db.QueryRow(query, args...), &summary); err != nil {
		return RoutingWindowSummary{}, err
	}
	return summary, nil
}

// ComparedWindowSummary 是包含三态比较计数的窗口聚合。
type ComparedWindowSummary struct {
	RoutingWindowSummary
	ComparedCount   int64
	MatchedCount    int64
	MismatchCount   int64
	UncomparedCount int64
}

// AggregateComparisonStats 按发布批次聚合三态比较统计。
func (s *TraceStore) AggregateComparisonStats(start, end time.Time, releaseID string) (*ComparedWindowSummary, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	var summary ComparedWindowSummary
	err := s.db.QueryRow(`
SELECT COALESCE(SUM(compared_count), 0), COALESCE(SUM(matched_count), 0),
       COALESCE(SUM(mismatch_count), 0), COALESCE(SUM(uncompared_count), 0)
FROM autopilot_routing_windows
WHERE window_start >= ? AND window_start < ?`,
		start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339),
	).Scan(&summary.ComparedCount, &summary.MatchedCount, &summary.MismatchCount, &summary.UncomparedCount)
	if err != nil {
		return nil, err
	}
	return &summary, nil
}

func scanRoutingWindowSummary(row rowScanner, summary *RoutingWindowSummary) error {
	var firstWindow, lastWindow sql.NullString
	err := row.Scan(
		&firstWindow, &lastWindow,
		&summary.AttemptCount, &summary.RequestCount,
		&summary.SuccessCount, &summary.FailureCount,
		&summary.CancelledCount, &summary.AttemptFailureCount,
		&summary.ChannelFallbackCount, &summary.FailOpenCount,
		&summary.LatencySampleCount,
		&summary.latencyBuckets[0], &summary.latencyBuckets[1],
		&summary.latencyBuckets[2], &summary.latencyBuckets[3],
		&summary.latencyBuckets[4], &summary.latencyBuckets[5],
		&summary.latencyBuckets[6], &summary.latencyBuckets[7],
		&summary.FirstByteSampleCount,
		&summary.firstByteBuckets[0], &summary.firstByteBuckets[1],
		&summary.firstByteBuckets[2], &summary.firstByteBuckets[3],
		&summary.firstByteBuckets[4], &summary.firstByteBuckets[5],
		&summary.firstByteBuckets[6], &summary.firstByteBuckets[7],
	)
	if err != nil {
		return err
	}
	if firstWindow.Valid {
		summary.FirstWindowAt, _ = time.Parse(time.RFC3339, firstWindow.String)
	}
	if lastWindow.Valid {
		summary.LastWindowAt, _ = time.Parse(time.RFC3339, lastWindow.String)
	}
	finalizeWindowSummary(summary)
	return nil
}

// EvaluateAutoReadiness 使用最近 24 小时 shadow/assist 真实结果判断是否允许切换 auto。
func (s *TraceStore) EvaluateAutoReadiness(now time.Time) AutoReadinessReport {
	now = now.UTC()
	report := AutoReadinessReport{
		RequiredSamples:          autoReadinessMinSamples,
		RequiredObservationHours: int(autoReadinessLookback.Hours()),
		BlockingReasons:          make([]string, 0, 8),
	}
	if s == nil || s.db == nil {
		report.BlockingReasons = append(report.BlockingReasons, "telemetry_unavailable")
		return report
	}
	lastEvent, _ := s.LastAutoSafetyEvent()
	report.RecoveryPending = isPendingAutoRecovery(lastEvent)
	report.LastRollback, _ = s.LastAutoRollbackEvent()

	safeModes := []RoutingMode{RoutingModeShadow, RoutingModeAssist}
	lookbackStart := now.Add(-autoReadinessLookback).Truncate(routingWindowDuration)
	safe, err := s.aggregateRoutingWindows(lookbackStart, now.Add(time.Nanosecond), safeModes...)
	if err != nil {
		report.BlockingReasons = append(report.BlockingReasons, "telemetry_query_failed")
		return report
	}
	report.SafeModeMetrics = safe
	if !safe.FirstWindowAt.IsZero() && !safe.LastWindowAt.IsZero() {
		report.ObservationHours = (safe.LastWindowAt.Sub(safe.FirstWindowAt) + routingWindowDuration).Hours()
	}
	if safe.RequestCount < autoReadinessMinSamples {
		report.BlockingReasons = append(report.BlockingReasons, "insufficient_samples")
	}
	if report.ObservationHours < autoReadinessLookback.Hours()-routingWindowDuration.Hours() {
		report.BlockingReasons = append(report.BlockingReasons, "insufficient_observation_time")
	}
	if safe.RequestCount > 0 && safe.SuccessRate < autoReadinessMinSuccessRate {
		report.BlockingReasons = append(report.BlockingReasons, "success_rate_below_threshold")
	}
	if safe.RequestCount > 0 && safe.FallbackRate > autoReadinessMaxFallbackRate {
		report.BlockingReasons = append(report.BlockingReasons, "fallback_rate_above_threshold")
	}
	if safe.RequestCount > 0 && safe.FailOpenRate > autoReadinessMaxFailOpenRate {
		report.BlockingReasons = append(report.BlockingReasons, "failopen_rate_above_threshold")
	}

	recentStart := now.Add(-autoReadinessRecentWindow).Truncate(routingWindowDuration)
	baseline, baselineErr := s.aggregateRoutingWindows(lookbackStart, recentStart, safeModes...)
	recent, recentErr := s.aggregateRoutingWindows(recentStart, now.Add(time.Nanosecond), safeModes...)
	report.BaselineMetrics = baseline
	report.RecentMetrics = recent
	if baselineErr != nil || recentErr != nil {
		report.BlockingReasons = appendUnique(report.BlockingReasons, "telemetry_query_failed")
	} else {
		if baseline.RequestCount < autoReadinessMinBaseline || recent.RequestCount < autoReadinessMinRecent {
			report.BlockingReasons = append(report.BlockingReasons, "insufficient_comparison_samples")
		} else {
			if recent.SuccessRate < baseline.SuccessRate-0.05 {
				report.BlockingReasons = append(report.BlockingReasons, "recent_success_regression")
			}
			if recent.FallbackRate > baseline.FallbackRate+0.05 {
				report.BlockingReasons = append(report.BlockingReasons, "recent_fallback_regression")
			}
			if latencyRegressed(recent.P95LatencyMs, baseline.P95LatencyMs) ||
				latencyRegressed(recent.P95FirstByteLatencyMs, baseline.P95FirstByteLatencyMs) {
				report.BlockingReasons = append(report.BlockingReasons, "recent_latency_regression")
			}
		}
	}
	report.Ready = len(report.BlockingReasons) == 0
	return report
}

func latencyRegressed(current, baseline int64) bool {
	return baseline > 0 && current > int64(math.Ceil(float64(baseline)*autoReadinessMaxP95Ratio))
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

// AutoRegressionReport 是连续窗口回归检测结果。
type AutoRegressionReport struct {
	ShouldRollback bool
	Reasons        []string
	Observed       RoutingWindowSummary
	Baseline       RoutingWindowSummary
}

// AutoRecoveryReport 是自动安全降级后的恢复检测结果。
type AutoRecoveryReport struct {
	ShouldRecover bool
	Reasons       []string
	Observed      RoutingWindowSummary
	Baseline      RoutingWindowSummary
}

// EvaluateAutoRegression 检查最近三个已完成 auto 窗口是否全部相对安全模式基线恶化。
func (s *TraceStore) EvaluateAutoRegression(now time.Time) (AutoRegressionReport, error) {
	var report AutoRegressionReport
	if s == nil || s.db == nil {
		return report, nil
	}
	completedEnd := now.UTC().Truncate(routingWindowDuration)
	baseline, err := s.aggregateRoutingWindows(
		completedEnd.Add(-autoRollbackBaselineLookback), completedEnd,
		RoutingModeShadow, RoutingModeAssist,
	)
	if err != nil {
		return report, err
	}
	report.Baseline = baseline
	if baseline.RequestCount < autoReadinessMinSamples {
		return report, nil
	}
	windows, err := s.listModeWindows(
		RoutingModeAuto,
		completedEnd.Add(-time.Duration(autoRollbackWindows)*routingWindowDuration),
		completedEnd,
	)
	if err != nil || len(windows) != autoRollbackWindows {
		return report, err
	}

	var observed RoutingWindowSummary
	for index, window := range windows {
		expected := completedEnd.Add(-time.Duration(autoRollbackWindows-index) * routingWindowDuration)
		if !window.FirstWindowAt.Equal(expected) || window.RequestCount < autoRollbackMinWindowSamples {
			return report, nil
		}
		reasons := regressionReasons(window, baseline)
		if len(reasons) == 0 {
			return report, nil
		}
		for _, reason := range reasons {
			report.Reasons = appendUnique(report.Reasons, reason)
		}
		mergeWindowSummary(&observed, window)
	}
	finalizeWindowSummary(&observed)
	report.Observed = observed
	report.ShouldRollback = true
	return report, nil
}

// EvaluateAutoRecovery 检查最近一次系统降级是否已满足自动恢复条件。
// 除完整 readiness 门槛外，最近三个已完成 assist 窗口必须逐个不劣于降级时基线，
// 避免单个短暂好窗口造成 auto/assist 来回震荡。
func (s *TraceStore) EvaluateAutoRecovery(now time.Time) (AutoRecoveryReport, error) {
	var report AutoRecoveryReport
	if s == nil || s.db == nil {
		return report, nil
	}
	lastEvent, err := s.LastAutoSafetyEvent()
	if err != nil || !isPendingAutoRecovery(lastEvent) {
		return report, err
	}

	readiness := s.EvaluateAutoReadiness(now)
	report.Baseline = lastEvent.Baseline
	if report.Baseline.RequestCount == 0 {
		report.Baseline = readiness.BaselineMetrics
	}
	if !readiness.Ready {
		report.Reasons = append(report.Reasons, readiness.BlockingReasons...)
		return report, nil
	}

	completedEnd := now.UTC().Truncate(routingWindowDuration)
	recoveryStart := completedEnd.Add(-time.Duration(autoRollbackWindows) * routingWindowDuration)
	if !lastEvent.CreatedAt.IsZero() && lastEvent.CreatedAt.UTC().After(recoveryStart) {
		report.Reasons = append(report.Reasons, "insufficient_recovery_observation")
		return report, nil
	}
	windows, err := s.listModeWindows(RoutingModeAssist, recoveryStart, completedEnd)
	if err != nil {
		return report, err
	}
	if len(windows) != autoRollbackWindows {
		report.Reasons = append(report.Reasons, "insufficient_recovery_observation")
		return report, nil
	}

	for index, window := range windows {
		expected := recoveryStart.Add(time.Duration(index) * routingWindowDuration)
		if !window.FirstWindowAt.Equal(expected) || window.RequestCount < autoRollbackMinWindowSamples {
			report.Reasons = append(report.Reasons, "insufficient_recovery_observation")
			return report, nil
		}
		if reasons := regressionReasons(window, report.Baseline); len(reasons) > 0 {
			report.Reasons = append(report.Reasons, reasons...)
			return report, nil
		}
		mergeWindowSummary(&report.Observed, window)
	}
	finalizeWindowSummary(&report.Observed)
	report.ShouldRecover = true
	return report, nil
}

func regressionReasons(current, baseline RoutingWindowSummary) []string {
	var reasons []string
	if current.SuccessRate < baseline.SuccessRate-0.05 {
		reasons = append(reasons, "success_rate_regression")
	}
	if current.FallbackRate > baseline.FallbackRate+0.05 {
		reasons = append(reasons, "fallback_rate_regression")
	}
	if current.FailOpenRate > math.Max(0.05, baseline.FailOpenRate+0.03) {
		reasons = append(reasons, "failopen_rate_regression")
	}
	if latencyRegressed(current.P95LatencyMs, baseline.P95LatencyMs) ||
		latencyRegressed(current.P95FirstByteLatencyMs, baseline.P95FirstByteLatencyMs) {
		reasons = append(reasons, "latency_regression")
	}
	return reasons
}

func (s *TraceStore) listModeWindows(mode RoutingMode, start, end time.Time) ([]RoutingWindowSummary, error) {
	rows, err := s.db.Query(`
SELECT window_start, window_start,
       SUM(attempt_count), SUM(request_count), SUM(success_count), SUM(failure_count),
       SUM(cancelled_count), SUM(attempt_failure_count), SUM(channel_fallback_count), SUM(failopen_count),
       SUM(latency_samples),
       SUM(latency_b0), SUM(latency_b1), SUM(latency_b2), SUM(latency_b3),
       SUM(latency_b4), SUM(latency_b5), SUM(latency_b6), SUM(latency_b7),
       SUM(first_byte_samples),
       SUM(first_byte_b0), SUM(first_byte_b1), SUM(first_byte_b2), SUM(first_byte_b3),
       SUM(first_byte_b4), SUM(first_byte_b5), SUM(first_byte_b6), SUM(first_byte_b7)
FROM autopilot_routing_windows
WHERE mode = ? AND window_start >= ? AND window_start < ?
GROUP BY window_start
ORDER BY window_start`,
		string(mode), start.UTC().Format(time.RFC3339), end.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return nil, err
	}
	defer errutil.IgnoreDeferred(rows.Close)
	windows := make([]RoutingWindowSummary, 0, autoRollbackWindows)
	for rows.Next() {
		var summary RoutingWindowSummary
		if err := scanRoutingWindowSummary(rows, &summary); err != nil {
			return nil, err
		}
		windows = append(windows, summary)
	}
	return windows, rows.Err()
}

func mergeWindowSummary(target *RoutingWindowSummary, source RoutingWindowSummary) {
	if target.FirstWindowAt.IsZero() || source.FirstWindowAt.Before(target.FirstWindowAt) {
		target.FirstWindowAt = source.FirstWindowAt
	}
	if source.LastWindowAt.After(target.LastWindowAt) {
		target.LastWindowAt = source.LastWindowAt
	}
	target.AttemptCount += source.AttemptCount
	target.RequestCount += source.RequestCount
	target.SuccessCount += source.SuccessCount
	target.FailureCount += source.FailureCount
	target.CancelledCount += source.CancelledCount
	target.AttemptFailureCount += source.AttemptFailureCount
	target.ChannelFallbackCount += source.ChannelFallbackCount
	target.FailOpenCount += source.FailOpenCount
	target.LatencySampleCount += source.LatencySampleCount
	target.FirstByteSampleCount += source.FirstByteSampleCount
	for index := range target.latencyBuckets {
		target.latencyBuckets[index] += source.latencyBuckets[index]
		target.firstByteBuckets[index] += source.firstByteBuckets[index]
	}
}

func (s *TraceStore) RecordAutoSafetyEvent(event AutoSafetyEvent) error {
	if s == nil || s.db == nil {
		return nil
	}
	if event.CreatedAt.IsZero() {
		event.CreatedAt = time.Now().UTC()
	}
	if event.EventUID == "" {
		event.EventUID = fmt.Sprintf("as_%d", event.CreatedAt.UnixNano())
	}
	reasons, _ := json.Marshal(event.Reasons)
	observed, _ := json.Marshal(event.Observed)
	baseline, _ := json.Marshal(event.Baseline)
	_, err := s.db.Exec(`
INSERT INTO autopilot_auto_safety_events
    (event_uid, from_mode, to_mode, reasons, observed_json, baseline_json, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		event.EventUID, event.FromMode, event.ToMode, string(reasons), string(observed), string(baseline),
		event.CreatedAt.UTC().Format(time.RFC3339),
	)
	return err
}

func (s *TraceStore) LastAutoSafetyEvent() (*AutoSafetyEvent, error) {
	return s.queryLastAutoSafetyEvent(false)
}

func (s *TraceStore) LastAutoRollbackEvent() (*AutoSafetyEvent, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	return s.queryLastAutoSafetyEvent(true)
}

func (s *TraceStore) queryLastAutoSafetyEvent(rollbackOnly bool) (*AutoSafetyEvent, error) {
	if s == nil || s.db == nil {
		return nil, nil
	}
	query := `
SELECT event_uid, from_mode, to_mode, reasons, observed_json, baseline_json, created_at
FROM autopilot_auto_safety_events ORDER BY created_at DESC LIMIT 1`
	if rollbackOnly {
		query = `
SELECT event_uid, from_mode, to_mode, reasons, observed_json, baseline_json, created_at
FROM autopilot_auto_safety_events
WHERE from_mode = 'auto' AND to_mode = 'assist'
ORDER BY created_at DESC LIMIT 1`
	}
	var event AutoSafetyEvent
	var reasonsJSON, observedJSON, baselineJSON, createdAt string
	err := s.db.QueryRow(query).Scan(
		&event.EventUID, &event.FromMode, &event.ToMode, &reasonsJSON,
		&observedJSON, &baselineJSON, &createdAt,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	_ = json.Unmarshal([]byte(reasonsJSON), &event.Reasons)
	_ = json.Unmarshal([]byte(observedJSON), &event.Observed)
	_ = json.Unmarshal([]byte(baselineJSON), &event.Baseline)
	event.CreatedAt, _ = time.Parse(time.RFC3339, createdAt)
	return &event, nil
}

func isPendingAutoRecovery(event *AutoSafetyEvent) bool {
	return event != nil && event.FromMode == config.AutopilotModeAuto && event.ToMode == config.AutopilotModeAssist
}
