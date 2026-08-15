package metrics

import (
	"github.com/BenedictKing/ccx/internal/errutil"
	"path/filepath"
	"testing"
	"time"
)

func TestQueryAggregatedHistoryWaitsForFlushAndFlushesBuffer(t *testing.T) {
	store, err := NewSQLiteStore(&SQLiteStoreConfig{
		DBPath:        filepath.Join(t.TempDir(), "metrics.db"),
		RetentionDays: 30,
	})
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer errutil.IgnoreDeferred(store.Close)

	record := PersistentRecord{
		MetricsKey:  GenerateMetricsKey("https://example.com", "sk-test"),
		BaseURL:     "https://example.com",
		KeyMask:     "sk-***",
		Timestamp:   time.Now(),
		Success:     true,
		APIType:     "messages",
		InputTokens: 10,
	}

	store.bufferMu.Lock()
	store.writeBuffer = append(store.writeBuffer, record)
	store.bufferMu.Unlock()

	store.flushMu.Lock()
	defer store.flushMu.Unlock()

	resultCh := make(chan []AggregatedBucket, 1)
	errCh := make(chan error, 1)
	go func() {
		buckets, err := store.QueryAggregatedHistory("messages", time.Now().Add(-time.Hour), 60, record.MetricsKey, "")
		if err != nil {
			errCh <- err
			return
		}
		resultCh <- buckets
	}()

	select {
	case <-resultCh:
		t.Fatal("QueryAggregatedHistory() should wait for flushMu, but returned early")
	case err := <-errCh:
		t.Fatalf("QueryAggregatedHistory() unexpected error: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	store.flushMu.Unlock()

	select {
	case err := <-errCh:
		t.Fatalf("QueryAggregatedHistory() error = %v", err)
	case buckets := <-resultCh:
		if len(buckets) != 1 {
			t.Fatalf("len(buckets) = %d, want 1", len(buckets))
		}
		if buckets[0].TotalRequests != 1 {
			t.Fatalf("TotalRequests = %d, want 1", buckets[0].TotalRequests)
		}
		if buckets[0].SuccessCount != 1 {
			t.Fatalf("SuccessCount = %d, want 1", buckets[0].SuccessCount)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("QueryAggregatedHistory() did not finish after flushMu released")
	}

	store.flushMu.Lock()
}

func TestLoadRecords_RoundTripsConsumptionPolicy(t *testing.T) {
	store, err := NewSQLiteStore(&SQLiteStoreConfig{
		DBPath:        filepath.Join(t.TempDir(), "metrics.db"),
		RetentionDays: 7,
	})
	if err != nil {
		t.Fatalf("NewSQLiteStore() error = %v", err)
	}
	defer errutil.IgnoreDeferred(store.Close)

	now := time.Now()
	records := []PersistentRecord{
		{
			MetricsKey:        GenerateMetricsKey("https://example.com", "sk-public"),
			BaseURL:           "https://example.com",
			KeyMask:           "sk-***blic",
			Timestamp:         now,
			Success:           true,
			APIType:           "messages",
			ConsumptionPolicy: "opportunistic",
		},
		{
			MetricsKey:        GenerateMetricsKey("https://example.com", "sk-private"),
			BaseURL:           "https://example.com",
			KeyMask:           "sk-***vate",
			Timestamp:         now,
			Success:           true,
			APIType:           "messages",
			ConsumptionPolicy: "normal",
		},
	}
	for _, r := range records {
		store.AddRecord(r)
	}
	store.flush()

	loaded, err := store.LoadRecords(now.Add(-time.Hour), "messages")
	if err != nil {
		t.Fatalf("LoadRecords() error = %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("len(loaded) = %d, want 2", len(loaded))
	}
	byKey := make(map[string]string)
	for _, r := range loaded {
		byKey[r.KeyMask] = r.ConsumptionPolicy
	}
	if byKey["sk-***blic"] != "opportunistic" {
		t.Errorf("public key consumptionPolicy = %q, want opportunistic", byKey["sk-***blic"])
	}
	if byKey["sk-***vate"] != "normal" {
		t.Errorf("private key consumptionPolicy = %q, want normal", byKey["sk-***vate"])
	}
}
