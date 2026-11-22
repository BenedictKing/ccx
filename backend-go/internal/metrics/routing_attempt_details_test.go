package metrics

import (
	"path/filepath"
	"testing"
	"time"
)

func TestRoutingDetailsAndKeyFiltersSurviveRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.db")
	db, err := NewSQLiteStore(&SQLiteStoreConfig{DBPath: path, RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewRoutingStoreWithRetention(db, 14)
	if err != nil {
		t.Fatal(err)
	}
	base := time.Now().Add(-48 * time.Hour)
	keyA, keyB := RoutingKeyID("sk-private-key-a"), RoutingKeyID("sk-private-key-b")
	store.StartRequest(RoutingRequest{GatewayRequestID: "retry-request", APIType: "chat", ReceivedAt: base})
	for i, item := range []struct{ id, channel, key string }{{"unique-upstream-attempt", "channel-a", keyA}, {"b", "channel-b", keyB}} {
		store.StartAttempt("retry-request", RoutingAttempt{AttemptID: item.id, ChannelUID: item.channel, SentModel: "model"})
		store.SetAttemptKey("retry-request", item.id, item.key, "same-mask")
		store.RecordAttemptFirstByte("retry-request", item.id, time.Duration(i+1)*100*time.Millisecond)
		firstContent := 50.0
		store.FinishAttempt("retry-request", item.id, RoutingStatusFailed, 429, &RoutingAttemptDetails{
			ErrorSummary: "rate limited", UsageObserved: true, InputTokens: 200, OutputTokens: 25, CacheReadTokens: 100,
			FirstContentLatencyMs: &firstContent,
		})
	}
	store.FinishRequest("retry-request", RoutingStatusFailed, "upstream")
	copy, _ := store.Get("chat", "retry-request")
	*copy.Attempts[0].Details.FirstByteLatencyMs = 999
	current, _ := store.Get("chat", "retry-request")
	if *current.Attempts[0].Details.FirstByteLatencyMs != 100 {
		t.Fatal("query result mutated stored details")
	}
	check := func(s *RoutingStore) {
		for _, keyword := range []string{"rate limited", "unique-upstream-attempt"} {
			page, _, _, err := s.QueryRequests("chat", base.Add(-time.Hour), time.Now(), RoutingHistoryFilter{Search: keyword}, time.Time{}, "", 20)
			if err != nil || len(page) != 1 {
				t.Fatalf("detail/attempt search=%q page=%+v err=%v", keyword, page, err)
			}
		}
		for _, item := range []struct {
			channel, key string
			count        int
		}{
			{"", keyA, 1}, {"channel-a", keyA, 1}, {"channel-a", keyB, 0}, {"channel-b", keyB, 1},
		} {
			filter := RoutingHistoryFilter{RoutingFilter: RoutingFilter{Basis: "all_attempts", ChannelUID: item.channel}, KeyID: item.key}
			page, more, _, err := s.QueryRequests("chat", base.Add(-time.Hour), time.Now(), filter, time.Time{}, "", 20)
			if err != nil || more || len(page) != item.count {
				t.Fatalf("channel/key filter %+v: page=%+v err=%v", item, page, err)
			}
			if len(page) > 0 && (page[0].Attempts[0].Details.InputTokens != 200 || *page[0].Attempts[0].Details.FirstByteLatencyMs != 100) {
				t.Fatal("request detail lost")
			}
		}
	}
	check(store)
	store.Close()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewSQLiteStore(&SQLiteStoreConfig{DBPath: path, RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	recovered, err := NewRoutingStoreWithRetention(reopened, 14)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	check(recovered)
}

func TestRoutingHistoryRetentionDoesNotDeleteAggregateRecords(t *testing.T) {
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
	old := now.Add(-20 * 24 * time.Hour)
	if err := db.batchInsertRecords([]PersistentRecord{{MetricsKey: "old", BaseURL: "https://example", KeyMask: "masked", Timestamp: old, APIType: "chat"}}); err != nil {
		t.Fatal(err)
	}
	if err := store.persist(RoutingRequest{GatewayRequestID: "old", APIType: "chat", ReceivedAt: old}); err != nil {
		t.Fatal(err)
	}
	if err := store.persist(RoutingRequest{GatewayRequestID: "recent", APIType: "chat", ReceivedAt: now}); err != nil {
		t.Fatal(err)
	}
	if err := store.cleanupHistory(now); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.db.QueryRow("SELECT COUNT(*) FROM routing_records_v1").Scan(&count); err != nil || count != 1 {
		t.Fatalf("history cleanup count=%d err=%v", count, err)
	}
	records, err := db.LoadRecords(old.Add(-time.Hour), "chat")
	if err != nil || len(records) != 1 || store.MaxHistoryAge() != 14*24*time.Hour {
		t.Fatalf("aggregate retention changed: records=%+v err=%v", records, err)
	}
}

func TestInterruptedRoutingAttemptDoesNotInventTotalLatency(t *testing.T) {
	store, err := NewRoutingStore(nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	store.StartRequest(RoutingRequest{GatewayRequestID: "interrupted", APIType: "chat", ReceivedAt: time.Now()})
	store.StartAttempt("interrupted", RoutingAttempt{AttemptID: "attempt"})
	store.RecordAttemptFirstByte("interrupted", "attempt", time.Millisecond)
	store.FinishRequest("interrupted", RoutingStatusUnknown, "")
	request, _ := store.Get("chat", "interrupted")
	if request.Attempts[0].Details.DurationMs != nil {
		t.Fatal("interrupted request has fabricated total latency")
	}
}
