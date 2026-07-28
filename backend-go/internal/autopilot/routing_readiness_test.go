package autopilot

import (
	"database/sql"
	"testing"
	"time"
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
