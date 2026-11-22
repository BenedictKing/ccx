package metrics

import (
	"math"
	"path/filepath"
	"testing"
	"time"
)

func TestFirstContentLatencySampleLifecycleAndWindows(t *testing.T) {
	manager := NewMetricsManagerWithConfig(10, 0.5)
	defer manager.Stop()
	baseURL, apiKey, serviceType := "https://content.example", "sk-test", "openai"
	for _, sample := range []struct {
		latency time.Duration
		success bool
	}{{0, true}, {100 * time.Millisecond, true}, {300 * time.Millisecond, false}} {
		id := manager.RecordRequestConnected(baseURL, apiKey, serviceType, "model")
		manager.RecordRequestFirstContent(baseURL, apiKey, serviceType, id, sample.latency)
		// 重复观察不能替换首个内容的时间。
		manager.RecordRequestFirstContent(baseURL, apiKey, serviceType, id, time.Second)
		manager.RecordRequestFinalizeOutcome(baseURL, apiKey, serviceType, id, sample.success, FailureClassRetryable, nil)
		manager.RecordRequestFirstContent(baseURL, apiKey, serviceType, id, 2*time.Second)
	}
	for _, outcome := range []string{"unobserved", "negative", "pending", "cancelled", "ignored"} {
		id := manager.RecordRequestConnected(baseURL, apiKey, serviceType, "model")
		switch outcome {
		case "unobserved":
			manager.RecordRequestFirstByte(baseURL, apiKey, serviceType, id, 25*time.Millisecond)
			manager.RecordRequestFinalizeSuccess(baseURL, apiKey, serviceType, id, nil)
		case "negative":
			manager.RecordRequestFirstContent(baseURL, apiKey, serviceType, id, -time.Millisecond)
			manager.RecordRequestFinalizeSuccess(baseURL, apiKey, serviceType, id, nil)
		case "pending":
			manager.RecordRequestFirstContent(baseURL, apiKey, serviceType, id, 5*time.Second)
		case "cancelled":
			manager.RecordRequestFirstContent(baseURL, apiKey, serviceType, id, 5*time.Second)
			manager.RecordRequestFinalizeClientCancel(baseURL, apiKey, serviceType, id)
		case "ignored":
			manager.RecordRequestFirstContent(baseURL, apiKey, serviceType, id, 5*time.Second)
			manager.RecordRequestFinalizeIgnored(baseURL, apiKey, serviceType, id)
		}
	}
	old := manager.RecordRequestConnectedAt(baseURL, apiKey, serviceType, "model", time.Now().Add(-2*time.Hour))
	manager.RecordRequestFirstContent(baseURL, apiKey, serviceType, old, time.Second)
	manager.RecordRequestFinalizeSuccess(baseURL, apiKey, serviceType, old, nil)

	for name, stats := range map[string]TimeWindowStats{
		"key":        manager.GetTimeWindowStatsForKey(baseURL, apiKey, serviceType, time.Hour),
		"key 15m":    manager.GetAllTimeWindowStatsForKey(baseURL, apiKey, serviceType)["15m"],
		"single URL": manager.calculateAggregatedTimeWindowsInternal(baseURL, []string{apiKey}, serviceType)["1h"],
		"multi URL":  manager.calculateAggregatedTimeWindowsMultiURL([]string{baseURL, baseURL}, []string{apiKey}, serviceType)["1h"],
	} {
		if stats.FirstContentLatencySamples != 3 || stats.FirstContentLatencyP50Ms != 100 || stats.FirstContentLatencyP95Ms != 280 {
			t.Errorf("%s: unexpected first-content stats: %+v", name, stats)
		}
		if stats.FirstByteLatencySamples != 1 || stats.FirstByteLatencyP95Ms != 25 {
			t.Errorf("%s: first-byte samples changed: %+v", name, stats)
		}
	}
	for _, window := range []string{"6h", "24h"} {
		stats := manager.calculateAggregatedTimeWindowsMultiURL([]string{baseURL}, []string{apiKey}, serviceType)[window]
		if stats.FirstContentLatencySamples != 4 || stats.FirstContentLatencyP50Ms != 200 || math.Abs(stats.FirstContentLatencyP95Ms-895) > 0.001 {
			t.Errorf("%s: lost older sample: %+v", window, stats)
		}
	}
}

func TestFirstContentLatencyPersistsZeroAndMissingAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "metrics.db")
	store, err := NewSQLiteStore(&SQLiteStoreConfig{DBPath: path, RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	manager := NewMetricsManagerWithPersistence(10, 0.5, store, "responses")
	closed := false
	t.Cleanup(func() {
		if !closed {
			manager.Stop()
			_ = store.Close()
		}
	})
	baseURL, apiKey, serviceType := "https://content.example", "sk-test", "openai-response"
	for i := 0; i < 3; i++ {
		id := manager.RecordRequestConnected(baseURL, apiKey, serviceType, "model")
		if i < 2 {
			manager.RecordRequestFirstContent(baseURL, apiKey, serviceType, id, time.Duration(i)*125500*time.Microsecond)
		}
		manager.RecordRequestFinalizeOutcome(baseURL, apiKey, serviceType, id, i != 1, FailureClassRetryable, nil)
	}
	closed = true
	manager.Stop()
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := NewSQLiteStore(&SQLiteStoreConfig{DBPath: path, RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	recovered := NewMetricsManagerWithPersistence(10, 0.5, reopened, "responses")
	t.Cleanup(func() { recovered.Stop(); _ = reopened.Close() })
	records, err := reopened.LoadRecords(time.Now().Add(-time.Hour), "responses")
	if err != nil || len(records) != 3 {
		t.Fatalf("loaded records=%+v, err=%v", records, err)
	}
	var missing, observedZero int
	for _, record := range records {
		if record.FirstContentLatencyMs == nil {
			missing++
		} else if *record.FirstContentLatencyMs == 0 {
			observedZero++
		}
	}
	if missing != 1 || observedZero != 1 {
		t.Fatalf("missing=%d zero=%d, want 1 each", missing, observedZero)
	}
	stats := recovered.calculateAggregatedTimeWindowsMultiURL([]string{baseURL}, []string{apiKey}, serviceType)["15m"]
	if stats.FirstContentLatencySamples != 2 || stats.FirstContentLatencyP50Ms != 62.75 || math.Abs(stats.FirstContentLatencyP95Ms-119.225) > 0.001 {
		t.Fatalf("recovered first-content stats=%+v", stats)
	}
}

func BenchmarkRequestLatencyHistograms(b *testing.B) {
	latency := 125.5
	record := RequestRecord{FirstByteLatencyMs: 25, FirstContentLatencyMs: &latency}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var histograms requestLatencyHistograms
		for sample := 0; sample < 100000; sample++ {
			histograms.add(record)
		}
		var stats TimeWindowStats
		histograms.populate(&stats)
		if stats.FirstContentLatencySamples != 100000 || stats.FirstByteLatencySamples != 100000 {
			b.Fatal("lost latency samples")
		}
	}
}
