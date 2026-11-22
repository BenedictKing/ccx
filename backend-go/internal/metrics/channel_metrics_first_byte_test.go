package metrics

import (
	"database/sql"
	"fmt"
	"math"
	"path/filepath"
	"sort"
	"testing"
	"time"
)

func TestFirstByteLatencyWindowPercentilesExcludeUnobservedRequests(t *testing.T) {
	manager := NewMetricsManagerWithConfig(10, 0.5)
	defer manager.Stop()

	baseURL, apiKey, serviceType := "https://upstream.example", "sk-test", "openai"
	startedAt := time.Now()
	for _, sample := range []struct {
		latency time.Duration
		status  string
	}{
		{latency: 100 * time.Millisecond, status: "success"},
		{latency: 200 * time.Millisecond, status: "failure"},
		{status: "failure"},
	} {
		requestID := manager.RecordRequestConnectedAt(baseURL, apiKey, serviceType, "gpt-test", startedAt)
		if sample.latency > 0 {
			manager.RecordRequestFirstByte(baseURL, apiKey, serviceType, requestID, sample.latency)
		}
		manager.RecordRequestFinalizeOutcome(baseURL, apiKey, serviceType, requestID, sample.status == "success", FailureClassRetryable, nil)
	}

	stats := manager.GetAllTimeWindowStatsForKey(baseURL, apiKey, serviceType)["15m"]
	if stats.RequestCount != 3 || stats.FirstByteLatencySamples != 2 {
		t.Fatalf("request/sample counts = %d/%d, want 3/2", stats.RequestCount, stats.FirstByteLatencySamples)
	}
	if stats.FirstByteLatencyP50Ms != 150 || stats.FirstByteLatencyP95Ms != 195 {
		t.Fatalf("first-byte percentiles = %.2f/%.2f, want 150/195", stats.FirstByteLatencyP50Ms, stats.FirstByteLatencyP95Ms)
	}
}

func TestFirstByteLatencyExcludesPendingAndExpiredRequests(t *testing.T) {
	manager := NewMetricsManagerWithConfig(10, 0.5)
	defer manager.Stop()
	baseURL, apiKey, serviceType := "https://upstream.example", "sk-test", "openai"
	requestID := manager.RecordRequestConnected(baseURL, apiKey, serviceType, "gpt-test")
	manager.RecordRequestFirstByte(baseURL, apiKey, serviceType, requestID, 75*time.Millisecond)
	manager.RecordRequestFirstByte(baseURL, apiKey, serviceType, requestID, 300*time.Millisecond)
	if stats := manager.GetTimeWindowStatsForKey(baseURL, apiKey, serviceType, time.Hour); stats.FirstByteLatencySamples != 0 {
		t.Fatalf("pending request counted as latency sample: %+v", stats)
	}
	manager.RecordRequestFinalizeSuccess(baseURL, apiKey, serviceType, requestID, nil)
	expiredID := manager.RecordRequestConnectedAt(baseURL, apiKey, serviceType, "gpt-test", time.Now().Add(-2*time.Hour))
	manager.RecordRequestFirstByte(baseURL, apiKey, serviceType, expiredID, time.Second)
	manager.RecordRequestFinalizeSuccess(baseURL, apiKey, serviceType, expiredID, nil)
	for name, stats := range map[string]TimeWindowStats{
		"key":           manager.GetTimeWindowStatsForKey(baseURL, apiKey, serviceType, time.Hour),
		"single URL":    manager.calculateAggregatedTimeWindowsInternal(baseURL, []string{apiKey}, serviceType)["1h"],
		"multiple URLs": manager.calculateAggregatedTimeWindowsMultiURL([]string{baseURL}, []string{apiKey}, serviceType)["1h"],
	} {
		if stats.FirstByteLatencySamples != 1 || stats.FirstByteLatencyP95Ms != 75 {
			t.Errorf("%s: expected first observation only, got %+v", name, stats)
		}
	}
}

func TestFirstByteLatencyHistogramAccuracy(t *testing.T) {
	var histogram firstByteLatencyHistogram
	for _, invalid := range []float64{0, -1, math.NaN(), math.Inf(1)} {
		histogram.add(invalid)
	}
	if count, _, _ := histogram.summary(); count != 0 {
		t.Fatalf("invalid latencies counted: %d", count)
	}
	samples := make([]float64, 100000)
	for i := range samples {
		// 对数分布，覆盖亚毫秒请求、长尾与重复值。
		samples[i] = math.Exp(float64((i*7919)%10000)/1000) / 10
		histogram.add(samples[i])
	}
	sort.Float64s(samples)
	for _, percentile := range []float64{0.50, 0.95} {
		position := percentile * float64(len(samples)-1)
		lower := int(position)
		expected := samples[lower] + (samples[lower+1]-samples[lower])*(position-float64(lower))
		actual := histogram.percentile(percentile)
		if math.Abs(actual-expected)/expected > 0.022 {
			t.Errorf("p%.0f = %f, expected %f within 2.2%%", percentile*100, actual, expected)
		}
	}
	if histogram.count != int64(len(samples)) {
		t.Fatalf("sample count = %d, want %d", histogram.count, len(samples))
	}
}

func BenchmarkFirstByteLatencyHistogram(b *testing.B) {
	for _, count := range []int{1000, 100000} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				var histogram firstByteLatencyHistogram
				for j := 0; j < count; j++ {
					histogram.add(float64(j%10000 + 1))
				}
				histogram.summary()
			}
		})
	}
}

func TestFirstByteLatencyPersistsAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.db")
	store, err := NewSQLiteStore(&SQLiteStoreConfig{DBPath: path, RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	manager := NewMetricsManagerWithPersistence(10, 0.5, store, "chat")
	storeClosed, managerStopped := false, false
	t.Cleanup(func() {
		if !managerStopped {
			manager.Stop()
		}
		if !storeClosed {
			_ = store.Close()
		}
	})

	baseURL, apiKey, serviceType := "https://upstream.example", "sk-test", "openai"
	requestID := manager.RecordRequestConnected(baseURL, apiKey, serviceType, "gpt-test")
	manager.RecordRequestFirstByte(baseURL, apiKey, serviceType, requestID, 125*time.Millisecond)
	manager.RecordRequestFinalizeSuccess(baseURL, apiKey, serviceType, requestID, nil)
	manager.Stop()
	managerStopped = true
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	storeClosed = true

	reopened, err := NewSQLiteStore(&SQLiteStoreConfig{DBPath: path, RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	recovered := NewMetricsManagerWithPersistence(10, 0.5, reopened, "chat")
	t.Cleanup(func() {
		recovered.Stop()
		_ = reopened.Close()
	})

	stats := recovered.calculateAggregatedTimeWindowsMultiURL([]string{baseURL}, []string{apiKey}, serviceType)["15m"]
	if stats.FirstByteLatencySamples != 1 || stats.FirstByteLatencyP50Ms != 125 || stats.FirstByteLatencyP95Ms != 125 {
		t.Fatalf("recovered first-byte stats = %+v", stats)
	}
}

func TestFirstByteLatencyColumnUpgradePreservesSchemaVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE request_records (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		metrics_key TEXT NOT NULL,
		base_url TEXT NOT NULL,
		key_mask TEXT NOT NULL,
		timestamp INTEGER NOT NULL,
		success INTEGER NOT NULL,
		failure_class TEXT NOT NULL DEFAULT '',
		input_tokens INTEGER DEFAULT 0,
		output_tokens INTEGER DEFAULT 0,
		cache_creation_tokens INTEGER DEFAULT 0,
		cache_read_tokens INTEGER DEFAULT 0,
		api_type TEXT NOT NULL DEFAULT 'messages',
		model TEXT DEFAULT ''
	);
	PRAGMA user_version = 3;`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO request_records (metrics_key, base_url, key_mask, timestamp, success)
		VALUES ('legacy', 'https://old.example', 'masked', ?, 1)`, time.Now().Unix()); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	store, err := NewSQLiteStore(&SQLiteStoreConfig{DBPath: path, RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()

	version, err := store.schemaVersion()
	if err != nil || version != 3 {
		t.Fatalf("schema version = %d, err = %v, want 3", version, err)
	}
	var columnCount int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM pragma_table_info('request_records') WHERE name = 'first_byte_latency_ms'").Scan(&columnCount); err != nil || columnCount != 1 {
		t.Fatalf("first-byte latency column count = %d, err = %v", columnCount, err)
	}
	records, err := store.LoadRecords(time.Now().Add(-time.Hour), "messages")
	if err != nil || len(records) != 1 || records[0].FirstByteLatencyMs != 0 {
		t.Fatalf("legacy request should have no latency sample: %+v, err=%v", records, err)
	}
}
