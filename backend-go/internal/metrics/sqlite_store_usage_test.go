package metrics

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestQueryUsageBucketsUsesLocalMonthAndDST(t *testing.T) {
	store, err := NewSQLiteStore(&SQLiteStoreConfig{DBPath: filepath.Join(t.TempDir(), "metrics.db"), RetentionDays: 366})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	for _, timestamp := range []time.Time{
		time.Date(2026, 3, 1, 4, 59, 0, 0, time.UTC), // 纽约仍是 2 月
		time.Date(2026, 3, 1, 5, 0, 0, 0, time.UTC),
		time.Date(2026, 3, 8, 6, 59, 0, 0, time.UTC), // 01:59 EST
		time.Date(2026, 3, 8, 7, 0, 0, 0, time.UTC),  // 03:00 EDT
		time.Date(2026, 4, 1, 4, 0, 0, 0, time.UTC),  // 已是 4 月
	} {
		store.AddRecord(PersistentRecord{MetricsKey: "key", BaseURL: "https://example.com", APIType: "messages",
			Timestamp: timestamp, Success: true, InputTokens: 10, Model: "test-model"})
	}
	location, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 3, 1, 0, 0, 0, 0, location)
	buckets, err := store.QueryUsageBuckets(context.Background(), start, start.AddDate(0, 1, 0), location, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 3 {
		t.Fatalf("buckets = %+v, want 3 local hours", buckets)
	}
	if buckets[0].Timestamp.Format(time.RFC3339) != "2026-03-01T00:00:00-05:00" ||
		buckets[1].Timestamp.Format(time.RFC3339) != "2026-03-08T01:00:00-05:00" ||
		buckets[2].Timestamp.Format(time.RFC3339) != "2026-03-08T03:00:00-04:00" {
		t.Fatalf("unexpected local buckets: %+v", buckets)
	}
	for _, bucket := range buckets {
		if bucket.Requests != 1 || bucket.InputTokens != 10 {
			t.Fatalf("unexpected totals: %+v", bucket)
		}
	}
}

func TestQueryUsageBucketsAlignsNonWholeHourTimezone(t *testing.T) {
	store, err := NewSQLiteStore(&SQLiteStoreConfig{DBPath: filepath.Join(t.TempDir(), "metrics.db"), RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	store.AddRecord(PersistentRecord{MetricsKey: "key", BaseURL: "https://example.com", APIType: "chat",
		Timestamp: time.Date(2026, 10, 1, 18, 45, 0, 0, time.UTC), Success: true})
	location, err := time.LoadLocation("Asia/Kathmandu")
	if err != nil {
		t.Fatal(err)
	}
	start := time.Date(2026, 10, 1, 0, 0, 0, 0, location)
	buckets, err := store.QueryUsageBuckets(context.Background(), start, start.AddDate(0, 1, 0), location, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(buckets) != 1 || buckets[0].Timestamp.Format(time.RFC3339) != "2026-10-02T00:00:00+05:45" {
		t.Fatalf("buckets = %+v, want local hour boundary", buckets)
	}
}
