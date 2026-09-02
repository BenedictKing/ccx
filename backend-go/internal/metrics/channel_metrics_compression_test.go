package metrics

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/types"
)

// P2 压缩遥测持久化：RecordRequestCompression 附加到 pending 记录后，
// 成功与失败两条 finalize 路径都应把压缩字段写入 SQLite（request_records 压缩列）。
func TestRecordRequestCompression_PersistedOnSuccessAndFailure(t *testing.T) {
	store, err := NewSQLiteStore(&SQLiteStoreConfig{
		DBPath:        filepath.Join(t.TempDir(), "metrics.db"),
		RetentionDays: 7,
	})
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer func() { _ = store.Close() }()

	m := NewMetricsManagerWithPersistence(100, 0.5, store, "messages")
	defer m.Stop()

	stats := CompressionStats{
		Compressed:       true,
		OriginalTokens:   1000,
		CompressedTokens: 400,
		SavingsPercent:   60,
		Technique:        "rtk_filter",
	}

	// 成功路径
	reqID1 := m.RecordRequestConnectedWithCostContext("https://upstream.example", "sk-ok", "messages", "ch-1", "m", "m", "sk-***", RequestCostContext{})
	m.RecordRequestCompression("https://upstream.example", "sk-ok", "messages", reqID1, stats)
	m.RecordRequestFinalizeSuccess("https://upstream.example", "sk-ok", "messages", reqID1, &types.Usage{InputTokens: 10, OutputTokens: 5})

	// 失败路径（内部重试同样携带压缩观测）
	reqID2 := m.RecordRequestConnectedWithCostContext("https://upstream.example", "sk-bad", "messages", "ch-1", "m", "m", "sk-***", RequestCostContext{})
	m.RecordRequestCompression("https://upstream.example", "sk-bad", "messages", reqID2, stats)
	m.RecordRequestFinalizeFailureWithClass("https://upstream.example", "sk-bad", "messages", reqID2, FailureClassRetryable)

	store.flush()

	records, err := store.LoadRecords(time.Now().Add(-time.Minute), "messages")
	if err != nil {
		t.Fatalf("LoadRecords() err = %v", err)
	}
	if len(records) != 2 {
		t.Fatalf("记录数 = %d, want 2（成功+失败各一条）", len(records))
	}

	for _, r := range records {
		if !r.Compressed {
			t.Errorf("key=%s Compressed = false, want true", r.KeyMask)
		}
		if r.OriginalTokens != 1000 {
			t.Errorf("key=%s OriginalTokens = %d, want 1000", r.KeyMask, r.OriginalTokens)
		}
		if r.CompressedTokens != 400 {
			t.Errorf("key=%s CompressedTokens = %d, want 400", r.KeyMask, r.CompressedTokens)
		}
		if r.CompressionSavingsPct != 60 {
			t.Errorf("key=%s CompressionSavingsPct = %v, want 60", r.KeyMask, r.CompressionSavingsPct)
		}
		if r.CompressionTechnique != "rtk_filter" {
			t.Errorf("key=%s CompressionTechnique = %q, want rtk_filter", r.KeyMask, r.CompressionTechnique)
		}
	}

	// 成功与失败各占一条
	var success, failure int
	for _, r := range records {
		if r.Success {
			success++
		} else {
			failure++
		}
	}
	if success != 1 || failure != 1 {
		t.Fatalf("success=%d failure=%d, want 1/1", success, failure)
	}
}

// 回退（未压缩）路径：Compressed=false 但 FallbackReason 保留，供成本报表统计回退数。
func TestRecordRequestCompression_FallbackRecorded(t *testing.T) {
	store, err := NewSQLiteStore(&SQLiteStoreConfig{
		DBPath:        filepath.Join(t.TempDir(), "metrics.db"),
		RetentionDays: 7,
	})
	if err != nil {
		t.Fatalf("NewSQLiteStore() err = %v", err)
	}
	defer func() { _ = store.Close() }()

	m := NewMetricsManagerWithPersistence(100, 0.5, store, "messages")
	defer m.Stop()

	reqID := m.RecordRequestConnectedWithCostContext("https://upstream.example", "sk-fb", "messages", "ch-1", "m", "m", "sk-***", RequestCostContext{})
	m.RecordRequestCompression("https://upstream.example", "sk-fb", "messages", reqID, CompressionStats{
		Compressed:     false,
		OriginalTokens: 500,
		FallbackReason: "no_tool_results",
	})
	m.RecordRequestFinalizeSuccess("https://upstream.example", "sk-fb", "messages", reqID, nil)

	store.flush()

	records, err := store.LoadRecords(time.Now().Add(-time.Minute), "messages")
	if err != nil {
		t.Fatalf("LoadRecords() err = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("记录数 = %d, want 1", len(records))
	}
	r := records[0]
	if r.Compressed {
		t.Error("Compressed = true, want false")
	}
	if r.CompressionFallbackReason != "no_tool_results" {
		t.Errorf("CompressionFallbackReason = %q, want no_tool_results", r.CompressionFallbackReason)
	}
	if r.OriginalTokens != 500 {
		t.Errorf("OriginalTokens = %d, want 500", r.OriginalTokens)
	}
}

// 未调用 RecordRequestCompression 的请求：压缩字段保持零值（旧行为兼容）。
// 找不到 pending 记录时静默跳过（fail-open）。
func TestRecordRequestCompression_MissingPendingNoop(t *testing.T) {
	m := NewMetricsManagerWithPersistence(100, 0.5, nil, "messages")
	defer m.Stop()

	// requestID 不存在，不应 panic
	m.RecordRequestCompression("https://upstream.example", "sk-x", "messages", 99999, CompressionStats{Compressed: true})
}
