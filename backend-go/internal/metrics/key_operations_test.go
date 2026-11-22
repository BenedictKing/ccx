package metrics

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
)

func TestKeyOperationsPaginationAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.db")
	db, err := NewSQLiteStore(&SQLiteStoreConfig{DBPath: path, RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewKeyOperationStore(db, 14)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		store.Record(config.KeyOperationEvent{APIType: "chat", ChannelUID: "channel", KeyID: RoutingKeyID("a"), OccurredAt: time.Now(), Action: "pause"})
	}
	store.Record(config.KeyOperationEvent{APIType: "chat", ChannelUID: "other", KeyID: RoutingKeyID("b"), OccurredAt: time.Now()})
	store.Close()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewSQLiteStore(&SQLiteStoreConfig{DBPath: path, RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	recovered, err := NewKeyOperationStore(reopened, 14)
	if err != nil {
		t.Fatal(err)
	}
	defer recovered.Close()
	seen := map[int64]bool{}
	before := int64(0)
	for {
		page, more, coverage, err := recovered.Query("chat", "channel", RoutingKeyID("a"), before, 3)
		if err != nil || !coverage.Persistent || coverage.DroppedWrites != 0 {
			t.Fatalf("page coverage=%+v err=%v", coverage, err)
		}
		for _, event := range page {
			if seen[event.Sequence] || event.ChannelUID != "channel" {
				t.Fatal("duplicate or wrong channel")
			}
			seen[event.Sequence] = true
		}
		if !more {
			break
		}
		before = page[len(page)-1].Sequence
		// 翻页期间插入新记录，不能挤入已开始的分页。
		recovered.Record(config.KeyOperationEvent{APIType: "chat", ChannelUID: "channel", KeyID: RoutingKeyID("a"), OccurredAt: time.Now()})
	}
	if len(seen) != 8 {
		t.Fatalf("saw %d original events", len(seen))
	}
	page, _, _, err := recovered.Query("chat", "channel", RoutingKeyID("b"), 0, 20)
	if err != nil || len(page) != 0 {
		t.Fatal("channel and Key conditions crossed events")
	}
}

func TestKeyOperationMemoryIsBoundedAndPersistenceFailureVisible(t *testing.T) {
	store, err := NewKeyOperationStore(nil, 14)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for i := 0; i < maxKeyOperationMemory+10; i++ {
		store.Record(config.KeyOperationEvent{APIType: "chat", ChannelUID: "channel", OccurredAt: time.Now()})
	}
	_, _, coverage, err := store.Query("chat", "channel", "", 0, 20)
	if err != nil || len(store.events) != maxKeyOperationMemory || coverage.Persistent || !coverage.Limited {
		t.Fatalf("invalid memory coverage: %+v %v", coverage, err)
	}

	db, err := NewSQLiteStore(&SQLiteStoreConfig{DBPath: filepath.Join(t.TempDir(), "metrics.db"), RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	broken, err := NewKeyOperationStore(db, 14)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec("DROP TABLE key_operations_v1"); err != nil {
		t.Fatal(err)
	}
	broken.Record(config.KeyOperationEvent{APIType: "chat", ChannelUID: "channel", OccurredAt: time.Now()})
	broken.Close()
	_, _, failedCoverage, err := broken.Query("chat", "channel", "", 0, 20)
	if err == nil || failedCoverage.DroppedWrites != 1 {
		t.Fatalf("failure invisible: %+v %v", failedCoverage, err)
	}
}

func TestLatestKeyFailuresIncludeRetriesAndMatchTheSameAttempt(t *testing.T) {
	store, _ := NewRoutingStoreWithRetention(nil, 14)
	defer store.Close()
	store.StartRequest(RoutingRequest{GatewayRequestID: "retry", APIType: "chat", ReceivedAt: time.Now()})
	for _, item := range []struct{ id, channel, key, status string }{
		{"failed", "wanted", "a", RoutingStatusFailed},
		{"success", "wanted", "b", RoutingStatusSuccess},
		{"other", "other", "b", RoutingStatusFailed},
	} {
		store.StartAttempt("retry", RoutingAttempt{AttemptID: item.id, ChannelUID: item.channel})
		store.SetAttemptKey("retry", item.id, RoutingKeyID(item.key), "same-mask")
		store.FinishAttempt("retry", item.id, item.status, 429, &RoutingAttemptDetails{ErrorSummary: "rate limited"})
	}
	store.FinishRequest("retry", RoutingStatusSuccess, "")
	failures, _, err := store.LatestKeyFailures("chat", "wanted")
	if err != nil || len(failures) != 1 || failures[RoutingKeyID("a")].AttemptID != "failed" {
		t.Fatalf("failures=%+v err=%v", failures, err)
	}
}
