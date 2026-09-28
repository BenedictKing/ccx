package metrics

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/types"
)

// 请求开始时固化的标价快照（ListPricing）应在 finalize 回写 token 后参与
// ListCostUSD 计算：渠道级/全局级价格覆盖与调度侧读同一份价格，而不是落入
// 仅内置注册表的全局口径。
func TestRecordListCostUsesFrozenPricing(t *testing.T) {
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

	// 专用模型名确保内置注册表无价格，验证成本完全来自固化的 pricing
	const model = "test-frozen-pricing-model"
	in := 1.0  // $1 / 1M input tokens
	out := 2.0 // $2 / 1M output tokens
	pricing := &config.ModelPricing{
		Unit:                "per_million_tokens",
		Currency:            "USD",
		InputCacheMissPrice: &in,
		OutputPrice:         &out,
	}

	reqID := m.RecordRequestConnectedWithCostContext(
		"https://upstream.example", "sk-a", "messages", "ch-1", model, model, "sk-***",
		RequestCostContext{ListPricing: pricing},
	)
	m.RecordRequestFinalizeSuccess("https://upstream.example", "sk-a", "messages", reqID,
		&types.Usage{InputTokens: 1_000_000, OutputTokens: 1_000_000})
	store.flush()

	records, err := store.LoadRecords(time.Now().Add(-time.Minute), "messages")
	if err != nil {
		t.Fatalf("LoadRecords() err = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("记录数 = %d, want 1", len(records))
	}
	// 1M input × $1 + 1M output × $2 = $3
	if records[0].ListCostUSD < 2.99 || records[0].ListCostUSD > 3.01 {
		t.Fatalf("ListCostUSD = %v, want ~3.0 (frozen pricing)", records[0].ListCostUSD)
	}
}

// 无固化 pricing 时维持兜底行为：按内置注册表全局口径计算
// （本用例模型无内置价格 → 成本 0），不回归。
func TestRecordListCostFallbackWithoutFrozenPricing(t *testing.T) {
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

	const model = "test-frozen-pricing-model"
	reqID := m.RecordRequestConnectedWithCostContext(
		"https://upstream.example", "sk-a", "messages", "ch-1", model, model, "sk-***",
		RequestCostContext{},
	)
	m.RecordRequestFinalizeSuccess("https://upstream.example", "sk-a", "messages", reqID,
		&types.Usage{InputTokens: 1_000, OutputTokens: 1_000})
	store.flush()

	records, err := store.LoadRecords(time.Now().Add(-time.Minute), "messages")
	if err != nil {
		t.Fatalf("LoadRecords() err = %v", err)
	}
	if len(records) != 1 {
		t.Fatalf("记录数 = %d, want 1", len(records))
	}
	if records[0].ListCostUSD != 0 {
		t.Fatalf("ListCostUSD = %v, want 0 (no pricing anywhere)", records[0].ListCostUSD)
	}
}
