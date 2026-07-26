package autopilot

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
)

func newRoutingReadinessTestStore(t *testing.T) *TraceStore {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("sql.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	store, err := NewTraceStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewTraceStoreWithDB() error = %v", err)
	}
	return store
}

func recordReadinessOutcome(
	t *testing.T,
	store *TraceStore,
	uid string,
	mode RoutingMode,
	completedAt time.Time,
	success bool,
	channelFallback bool,
	failOpen bool,
	durationMs int64,
	firstByteMs int64,
) {
	t.Helper()
	trace := &RoutingDecisionTrace{
		TraceUID:     uid,
		RequestKind:  "messages",
		TaskClass:    TaskClassLightweight,
		Mode:         mode,
		FallbackUsed: failOpen,
		CreatedAt:    completedAt.Add(-time.Second),
	}
	store.Record(trace)
	err := store.RecordOutcome(uid, RoutingOutcome{
		Terminal:           true,
		Success:            success,
		ChannelFallback:    channelFallback,
		StatusCode:         map[bool]int{true: 200, false: 502}[success],
		RequestDurationMs:  durationMs,
		FirstByteLatencyMs: firstByteMs,
		CompletedAt:        completedAt,
	})
	if err != nil {
		t.Fatalf("RecordOutcome(%s) error = %v", uid, err)
	}
}

func TestRecordOutcomeWritesUnbiasedWindowOnce(t *testing.T) {
	store := newRoutingReadinessTestStore(t)
	now := time.Date(2026, 7, 17, 8, 7, 0, 0, time.UTC)
	recordReadinessOutcome(t, store, "rt_once", RoutingModeAssist, now, true, true, true, 750, 300)

	// 重复回填必须幂等，不能把窗口计数翻倍。
	if err := store.RecordOutcome("rt_once", RoutingOutcome{Terminal: true, Success: true, CompletedAt: now}); err != nil {
		t.Fatalf("duplicate RecordOutcome() error = %v", err)
	}

	summary, err := store.aggregateRoutingWindows(now.Add(-time.Hour), now.Add(time.Hour), RoutingModeAssist)
	if err != nil {
		t.Fatalf("aggregateRoutingWindows() error = %v", err)
	}
	if summary.RequestCount != 1 || summary.SuccessCount != 1 || summary.ChannelFallbackCount != 1 || summary.FailOpenCount != 1 {
		t.Fatalf("unexpected window summary: %+v", summary)
	}
	if summary.P95LatencyMs != 1000 || summary.P95FirstByteLatencyMs != 500 {
		t.Fatalf("p95 = %d/%dms, want 1000/500", summary.P95LatencyMs, summary.P95FirstByteLatencyMs)
	}
}

func TestEvaluateAutoReadinessRequiresSamplesAndObservation(t *testing.T) {
	store := newRoutingReadinessTestStore(t)
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	report := store.EvaluateAutoReadiness(now)
	if report.Ready {
		t.Fatal("empty telemetry must not be ready")
	}
	if !containsString(report.BlockingReasons, "insufficient_samples") ||
		!containsString(report.BlockingReasons, "insufficient_observation_time") {
		t.Fatalf("unexpected blocking reasons: %v", report.BlockingReasons)
	}
}

func TestEvaluateAutoReadinessPassesHealthyBurnIn(t *testing.T) {
	store := newRoutingReadinessTestStore(t)
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	start := now.Add(-24 * time.Hour)
	for i := 0; i < 500; i++ {
		completedAt := start.Add(time.Duration(i%96)*routingWindowDuration + time.Minute)
		recordReadinessOutcome(
			t, store, fmt.Sprintf("rt_safe_%03d", i), RoutingModeAssist, completedAt,
			true, i%20 == 0, i%100 == 0, 800, 400,
		)
	}
	report := store.EvaluateAutoReadiness(now)
	if !report.Ready {
		t.Fatalf("healthy burn-in should be ready, reasons=%v report=%+v", report.BlockingReasons, report)
	}
	if report.SafeModeMetrics.RequestCount != 500 || report.ObservationHours != 24 {
		t.Fatalf("unexpected readiness totals: %+v", report)
	}
}

func TestEvaluateAutoRegressionRequiresThreeDegradingWindows(t *testing.T) {
	store := newRoutingReadinessTestStore(t)
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	baselineStart := now.Add(-48 * time.Hour)
	for i := 0; i < 500; i++ {
		completedAt := baselineStart.Add(time.Duration(i%96)*routingWindowDuration + time.Minute)
		recordReadinessOutcome(t, store, fmt.Sprintf("rt_base_%03d", i), RoutingModeAssist, completedAt, true, false, false, 800, 400)
	}

	completedEnd := now.Truncate(routingWindowDuration)
	for window := 0; window < autoRollbackWindows; window++ {
		windowStart := completedEnd.Add(-time.Duration(autoRollbackWindows-window) * routingWindowDuration)
		for sample := 0; sample < int(autoRollbackMinWindowSamples); sample++ {
			success := sample < 15 // 75%，显著低于安全基线。
			recordReadinessOutcome(
				t, store, fmt.Sprintf("rt_auto_%d_%d", window, sample), RoutingModeAuto,
				windowStart.Add(time.Duration(sample)*time.Second), success, !success, false, 5000, 3000,
			)
		}
	}

	report, err := store.EvaluateAutoRegression(now)
	if err != nil {
		t.Fatalf("EvaluateAutoRegression() error = %v", err)
	}
	if !report.ShouldRollback || !containsString(report.Reasons, "success_rate_regression") {
		t.Fatalf("expected rollback, got %+v", report)
	}
}

func TestManagerAutoSafetyDowngradesToAssist(t *testing.T) {
	store := newRoutingReadinessTestStore(t)
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	baselineStart := now.Add(-48 * time.Hour)
	for i := 0; i < 500; i++ {
		recordReadinessOutcome(t, store, fmt.Sprintf("rt_mgr_base_%03d", i), RoutingModeAssist,
			baselineStart.Add(time.Duration(i%96)*routingWindowDuration+time.Minute), true, false, false, 800, 400)
	}
	completedEnd := now.Truncate(routingWindowDuration)
	for window := 0; window < autoRollbackWindows; window++ {
		windowStart := completedEnd.Add(-time.Duration(autoRollbackWindows-window) * routingWindowDuration)
		for sample := 0; sample < int(autoRollbackMinWindowSamples); sample++ {
			recordReadinessOutcome(t, store, fmt.Sprintf("rt_mgr_auto_%d_%d", window, sample), RoutingModeAuto,
				windowStart.Add(time.Duration(sample)*time.Second), false, true, true, 10000, 5000)
		}
	}

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"autopilot":{"autoSafetyDowngrade":true}}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfgManager, err := config.NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	if err := cfgManager.SetAutopilotRoutingMode(config.AutopilotModeAuto); err != nil {
		t.Fatalf("SetAutopilotRoutingMode(auto) error = %v", err)
	}
	manager := &Manager{traceStore: store, cfgManager: cfgManager}
	manager.evaluateAutoSafety(now)
	if got := cfgManager.GetEffectiveRoutingMode(); got != config.AutopilotModeAssist {
		t.Fatalf("effective mode = %q, want assist", got)
	}
	lastEvent, err := store.LastAutoSafetyEvent()
	if err != nil || lastEvent == nil || lastEvent.FromMode != "auto" || lastEvent.ToMode != "assist" {
		t.Fatalf("last safety event = %+v, err=%v", lastEvent, err)
	}
}

func TestManagerAutoSafetyMonitorOnlyWhenDowngradeDisabled(t *testing.T) {
	store := newRoutingReadinessTestStore(t)
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)
	baselineStart := now.Add(-48 * time.Hour)
	for i := 0; i < 500; i++ {
		recordReadinessOutcome(t, store, fmt.Sprintf("rt_mgr_mon_%03d", i), RoutingModeAssist,
			baselineStart.Add(time.Duration(i%96)*routingWindowDuration+time.Minute), true, false, false, 800, 400)
	}
	completedEnd := now.Truncate(routingWindowDuration)
	for window := 0; window < autoRollbackWindows; window++ {
		windowStart := completedEnd.Add(-time.Duration(autoRollbackWindows-window) * routingWindowDuration)
		for sample := 0; sample < int(autoRollbackMinWindowSamples); sample++ {
			recordReadinessOutcome(t, store, fmt.Sprintf("rt_mgr_mon_auto_%d_%d", window, sample), RoutingModeAuto,
				windowStart.Add(time.Duration(sample)*time.Second), false, true, true, 10000, 5000)
		}
	}

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{}`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfgManager, err := config.NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	if err := cfgManager.SetAutopilotRoutingMode(config.AutopilotModeAuto); err != nil {
		t.Fatalf("SetAutopilotRoutingMode(auto) error = %v", err)
	}
	manager := &Manager{traceStore: store, cfgManager: cfgManager}
	manager.evaluateAutoSafety(now)
	// 默认不降级，模式应保持 auto
	if got := cfgManager.GetEffectiveRoutingMode(); got != config.AutopilotModeAuto {
		t.Fatalf("effective mode = %q, want auto (monitor-only)", got)
	}
	// 安全事件应以 monitor_only 落盘
	lastEvent, err := store.LastAutoSafetyEvent()
	if err != nil || lastEvent == nil || lastEvent.ToMode != "monitor_only" {
		t.Fatalf("expected monitor_only event, got %+v, err=%v", lastEvent, err)
	}
}
