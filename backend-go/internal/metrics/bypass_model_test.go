package metrics

import (
	"github.com/BenedictKing/ccx/internal/types"
	"path/filepath"
	"testing"
	"time"
)

func TestBypassRecordsPreserveModelInMemoryAndPersistence(t *testing.T) {
	store, err := NewSQLiteStore(&SQLiteStoreConfig{DBPath: filepath.Join(t.TempDir(), "metrics.db"), RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	m := NewMetricsManagerWithPersistence(10, 0.5, store, "responses")
	defer m.Stop()
	const url, key, model = "https://example.com", "test-key", "actual-model"
	m.RecordSuccessWithUsage(url, key, "responses", &types.Usage{InputTokens: 12, OutputTokens: 4}, model)
	m.RecordFailure(url, key, "responses", model)
	stats := m.GetGlobalHistoricalStatsWithTokens(time.Hour, time.Minute)
	if len(stats.ModelDataPoints[model]) == 0 {
		t.Fatalf("actual model missing from memory stats: %v", stats.ModelDataPoints)
	}
	buckets, err := store.QueryModelAggregatedHistory("responses", time.Now().Add(-time.Hour), 60, "", "")
	if err != nil {
		t.Fatal(err)
	}
	total := int64(0)
	for _, bucket := range buckets {
		if bucket.Model != model {
			t.Fatalf("persisted model=%q", bucket.Model)
		}
		total += bucket.TotalRequests
	}
	if total != 2 {
		t.Fatalf("persisted requests=%d,want 2", total)
	}
}
