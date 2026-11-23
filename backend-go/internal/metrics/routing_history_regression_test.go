package metrics

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

func TestRoutingHistoryFiltersUseLatestSnapshotWithoutLosingPages(t *testing.T) {
	db, err := NewSQLiteStore(&SQLiteStoreConfig{DBPath: filepath.Join(t.TempDir(), "metrics.db"), RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	seed, err := NewRoutingStoreWithRetention(db, 14)
	if err != nil {
		t.Fatal(err)
	}
	seed.Close()
	now := time.Now()
	var current RoutingRequest
	for i, id := range []string{"updated", "older", "oldest"} {
		request := RoutingRequest{
			GatewayRequestID: id, APIType: "chat", RequestedModel: "old-model", Status: RoutingStatusInProgress,
			ReceivedAt: now.Add(-time.Duration(i+1) * time.Minute),
			Attempts:   []RoutingAttempt{{AttemptID: id, SentModel: "old-target", KeyID: RoutingKeyID("old-key"), Details: &RoutingAttemptDetails{ErrorSummary: "outdated error"}}},
		}
		if err := seed.persist(request); err != nil {
			t.Fatal(err)
		}
		if i == 0 {
			current = cloneRoutingRequest(request)
			current.Status, current.RequestedModel = RoutingStatusSuccess, "new-model"
			current.Attempts[0].SentModel, current.Attempts[0].KeyID = "new-target", RoutingKeyID("new-key")
			current.Attempts[0].Details.ErrorSummary = "current error"
		}
	}
	// 固定异步落盘尚未追上内存的窗口，避免测试依赖 goroutine 调度。
	store := &RoutingStore{db: db.db, retentionDays: 14, startedAt: now, requests: map[string]RoutingRequest{current.GatewayRequestID: current}}
	filters := []RoutingHistoryFilter{
		{Status: RoutingStatusInProgress},
		{RoutingFilter: RoutingFilter{RequestedModel: "old-model"}},
		{RoutingFilter: RoutingFilter{SentModel: "old-target"}},
		{KeyID: RoutingKeyID("old-key")},
		{Search: "outdated"},
	}
	for i, filter := range filters {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			page, more, _, err := store.QueryRequests("chat", now.Add(-time.Hour), now, filter, time.Time{}, "", 1)
			if err != nil || len(page) != 1 || page[0].GatewayRequestID != "older" || !more {
				t.Fatalf("first page=%+v more=%v err=%v", page, more, err)
			}
			page, more, _, err = store.QueryRequests("chat", now.Add(-time.Hour), now, filter, page[0].ReceivedAt, page[0].GatewayRequestID, 1)
			if err != nil || len(page) != 1 || page[0].GatewayRequestID != "oldest" || more {
				t.Fatalf("next page=%+v more=%v err=%v", page, more, err)
			}
		})
	}
	page, _, _, err := store.QueryRequests("chat", now.Add(-time.Hour), now, RoutingHistoryFilter{}, time.Time{}, "", 1)
	if err != nil || len(page) != 1 || page[0].RequestedModel != "new-model" || page[0].Attempts[0].Details.ErrorSummary != "current error" {
		t.Fatalf("latest snapshot lost: page=%+v err=%v", page, err)
	}
}

func TestRoutingRestartRecoversHistoryBeyondMemoryWindowInBatches(t *testing.T) {
	db, err := NewSQLiteStore(&SQLiteStoreConfig{DBPath: filepath.Join(t.TempDir(), "metrics.db"), RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	seed, err := NewRoutingStoreWithRetention(db, 14)
	if err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-48 * time.Hour)
	for i := range 300 {
		id := fmt.Sprintf("interrupted-%03d", i)
		seed.StartRequest(RoutingRequest{GatewayRequestID: id, APIType: "chat", ReceivedAt: old})
		seed.StartAttempt(id, RoutingAttempt{AttemptID: "finished"})
		seed.FinishAttempt(id, "finished", RoutingStatusFailed, 429, &RoutingAttemptDetails{ErrorSummary: "rate limited"})
		seed.StartAttempt(id, RoutingAttempt{AttemptID: "interrupted"})
		seed.RecordAttemptFirstByte(id, "interrupted", time.Millisecond)
	}
	seed.StartRequest(RoutingRequest{GatewayRequestID: "completed", APIType: "chat", ReceivedAt: old})
	seed.FinishRequest("completed", RoutingStatusSuccess, "")
	seed.Close()
	restarted, err := NewRoutingStoreWithRetention(db, 14)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	page, more, coverage, err := restarted.QueryRequests("chat", old.Add(-time.Hour), time.Now(), RoutingHistoryFilter{Status: RoutingStatusUnknown}, time.Time{}, "", 301)
	if err != nil || more || len(page) != 300 || coverage.UnknownRequests != 300 {
		t.Fatalf("recovery count=%d more=%v coverage=%+v err=%v", len(page), more, coverage, err)
	}
	for _, request := range page {
		finished, interrupted := request.Attempts[0], request.Attempts[1]
		if request.CompletedAt == nil || finished.Status != RoutingStatusFailed || finished.StatusCode != 429 || finished.Details.DurationMs == nil || finished.Details.ErrorSummary != "rate limited" {
			t.Fatalf("completed attempt changed: %+v", request)
		}
		if interrupted.Status != RoutingStatusUnknown || interrupted.CompletedAt == nil || interrupted.Details.DurationMs != nil || *interrupted.Details.FirstByteLatencyMs != 1 {
			t.Fatalf("interrupted attempt not recovered correctly: %+v", interrupted)
		}
	}
	page, _, _, err = restarted.QueryRequests("chat", old.Add(-time.Hour), time.Now(), RoutingHistoryFilter{Status: RoutingStatusSuccess}, time.Time{}, "", 1)
	if err != nil || len(page) != 1 || page[0].GatewayRequestID != "completed" {
		t.Fatalf("completed request changed: page=%+v err=%v", page, err)
	}
}

func TestRoutingHistoryCleanupPreservesCursorGrace(t *testing.T) {
	db, err := NewSQLiteStore(&SQLiteStoreConfig{DBPath: filepath.Join(t.TempDir(), "metrics.db"), RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	store, err := NewRoutingStoreWithRetention(db, 14)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Now()
	for _, item := range []struct {
		id  string
		age time.Duration
	}{{"cursor-edge", 14*24*time.Hour + 10*time.Minute}, {"expired", 14*24*time.Hour + RoutingSnapshotMaxAge + time.Minute}} {
		if err := store.persist(RoutingRequest{GatewayRequestID: item.id, APIType: "chat", ReceivedAt: now.Add(-item.age), Status: RoutingStatusSuccess}); err != nil {
			t.Fatal(err)
		}
	}
	if err := store.cleanupHistory(now); err != nil {
		t.Fatal(err)
	}
	page, _, _, err := store.QueryRequests("chat", now.Add(-store.MaxHistoryAge()-RoutingSnapshotMaxAge), now, RoutingHistoryFilter{}, time.Time{}, "", 10)
	if err != nil || len(page) != 1 || page[0].GatewayRequestID != "cursor-edge" {
		t.Fatalf("cursor grace lost: page=%+v err=%v", page, err)
	}
}
