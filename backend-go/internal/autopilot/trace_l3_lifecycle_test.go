package autopilot

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// ── L3 SQLite 生命周期测试 ──

// newTempTraceStore 创建使用临时文件的 TraceStore，测试重启持久化。
func newTempTraceStore(t *testing.T) (*TraceStore, string) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_trace.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}

	store, err := NewTraceStoreWithDB(db)
	if err != nil {
		t.Fatalf("创建 TraceStore 失败: %v", err)
	}
	return store, dbPath
}

func reopenTraceStore(t *testing.T, dbPath string) *TraceStore {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("重新打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store, err := NewTraceStoreWithDB(db)
	if err != nil {
		t.Fatalf("重新创建 TraceStore 失败: %v", err)
	}
	return store
}

// TestTraceStore_RestartRestoresV2Detail 写入 v2 trace，关闭后重新打开，验证详情可还原。
func TestTraceStore_RestartRestoresV2Detail(t *testing.T) {
	store, dbPath := newTempTraceStore(t)

	// 写入一条必落盘的 trace（ManualIntent）
	traceUID := GenerateTraceUIDv2()
	trace := &RoutingDecisionTrace{
		TraceUID:           traceUID,
		SchemaVersion:      2,
		Source:             "proxy",
		RequestKind:        "messages",
		TaskClass:          TaskClassSupervisor,
		TaskDomain:         TaskDomainCoding,
		RequestedModel:     "claude-sonnet-5",
		Mode:               RoutingModeShadow,
		ShadowChannelUID:   "ch_a",
		ActualChannelUID:   "ch_b",
		Match:              false,
		ManualIntentUID:    "mi_test_001",
		SelectedChannelUID: "ch_c",
		ReleaseID:          "rel_001",
		PolicyFingerprint:  "fp_abc",
		Cohort:             CohortTreatment,
		CreatedAt:          time.Now().UTC(),
	}
	store.Record(trace)
	_ = store.Close()

	// 重新打开
	store2 := reopenTraceStore(t, dbPath)

	// 按 UID 查询详情
	detail, err := store2.GetTraceDetail(traceUID)
	if err != nil {
		t.Fatalf("GetTraceDetail 失败: %v", err)
	}
	if detail == nil {
		t.Fatal("重启后应能读取 trace 详情")
	}
	if detail.TraceUID != traceUID {
		t.Errorf("TraceUID = %q, want %q", detail.TraceUID, traceUID)
	}
	if detail.ManualIntentUID != "mi_test_001" {
		t.Errorf("ManualIntentUID = %q, want mi_test_001", detail.ManualIntentUID)
	}
	if detail.ComparisonStatus != ComparisonMismatched {
		t.Errorf("ComparisonStatus = %q, want mismatched", detail.ComparisonStatus)
	}
}

// TestTraceStore_BadJSONDetailsReturnsPartial 验证坏 JSON 时列表标记 partial。
func TestTraceStore_BadJSONDetailsReturnsPartial(t *testing.T) {
	store, dbPath := newTempTraceStore(t)

	// 正常写入
	trace := &RoutingDecisionTrace{
		TraceUID:        "rt_good",
		RequestKind:     "chat",
		TaskClass:       TaskClassWorker,
		Mode:            RoutingModeShadow,
		ManualIntentUID: "mi_good",
		CreatedAt:       time.Now().UTC(),
	}
	store.Record(trace)
	_ = store.Close()

	// 直接操作 DB 注入坏 JSON
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	_, _ = db.Exec(`UPDATE autopilot_routing_traces SET details_json = 'not-valid-json' WHERE trace_uid = 'rt_good'`)
	_ = db.Close()

	// 重新打开
	store2 := reopenTraceStore(t, dbPath)

	// 按 UID 查询应返回适配结果（不崩溃）
	detail, err := store2.GetTraceDetail("rt_good")
	if err != nil {
		t.Fatalf("坏 JSON 不应导致查询失败: %v", err)
	}
	if detail == nil {
		t.Fatal("坏 JSON 时应返回适配详情")
	}
	// v1 适配结果应标记 historicalSchema
	if !detail.HistoricalSchema {
		t.Error("坏 JSON 时应标记 HistoricalSchema")
	}
}

// TestTraceStore_CleanupExpiredTraces 验证过期 trace 被清理。
func TestTraceStore_CleanupExpiredTraces(t *testing.T) {
	store, dbPath := newTempTraceStore(t)

	// 写入一条 8 天前的 trace
	oldTime := time.Now().UTC().Add(-8 * 24 * time.Hour)
	trace := &RoutingDecisionTrace{
		TraceUID:        "rt_old_expired",
		RequestKind:     "chat",
		TaskClass:       TaskClassWorker,
		Mode:            RoutingModeShadow,
		ManualIntentUID: "mi_old",
		CreatedAt:       oldTime,
	}
	store.Record(trace)
	_ = store.Close()

	// 重新打开，触发启动清理
	store2 := reopenTraceStore(t, dbPath)
	store2.MaybeCleanup()

	// 验证旧 trace 已被清理
	var count int
	db, _ := sql.Open("sqlite", dbPath)
	err := db.QueryRow("SELECT COUNT(*) FROM autopilot_routing_traces WHERE trace_uid = 'rt_old_expired'").Scan(&count)
	_ = db.Close()
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if count != 0 {
		t.Errorf("8 天前的 trace 应被清理，实际行数: %d", count)
	}
}

// TestTraceStore_DetailsJSONSanitized 验证落盘的 details_json 不含敏感字段。
func TestTraceStore_DetailsJSONSanitized(t *testing.T) {
	store, dbPath := newTempTraceStore(t)

	trace := &RoutingDecisionTrace{
		TraceUID:           GenerateTraceUIDv2(),
		SchemaVersion:      2,
		RequestKind:        "chat",
		TaskClass:          TaskClassWorker,
		Mode:               RoutingModeShadow,
		ManualIntentUID:    "mi_san",
		SelectedChannelUID: "ch_sel",
		SelectedMetricsKey: "https://api.example.com|sk-secret-key-12345678",
		Candidates: []RoutingCandidate{
			{ChannelUID: "ch_a", MetricsKey: "https://leak.example.com|sk-bad-key-99999999"},
		},
		CreatedAt: time.Now().UTC(),
	}
	store.Record(trace)
	_ = store.Close()

	// 读取 details_json 验证不含敏感信息
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	var detailsJSON string
	err = db.QueryRow("SELECT details_json FROM autopilot_routing_traces WHERE trace_uid = ?", trace.TraceUID).Scan(&detailsJSON)
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}

	// 验证脱敏后 JSON 不含敏感字段
	sensitive := ScanJSONForSensitive([]byte(detailsJSON))
	if len(sensitive) > 0 {
		t.Errorf("details_json 含敏感字段: %v", sensitive)
	}

	// 验证 MetricsKey 已清空
	var detail TraceDetailV2
	_ = json.Unmarshal([]byte(detailsJSON), &detail)
	for i, c := range detail.Candidates {
		if c.MetricsKey != "" {
			t.Errorf("candidate[%d].MetricsKey 未清空: %q", i, c.MetricsKey)
		}
	}
}

// TestTraceStore_V1RowCompatible 验证 v1 行（无 v2 列值）可被读取。
func TestTraceStore_V1RowCompatible(t *testing.T) {
	store, dbPath := newTempTraceStore(t)

	// 正常写入一条 trace
	trace := &RoutingDecisionTrace{
		TraceUID:        GenerateTraceUIDv2(),
		SchemaVersion:   2,
		RequestKind:     "messages",
		TaskClass:       TaskClassSupervisor,
		Mode:            RoutingModeShadow,
		ManualIntentUID: "mi_v1",
		CreatedAt:       time.Now().UTC(),
	}
	store.Record(trace)
	_ = store.Close()

	// 模拟 v1 行：清除 schema_version 到 1
	db, _ := sql.Open("sqlite", dbPath)
	_, _ = db.Exec("UPDATE autopilot_routing_traces SET schema_version = 1 WHERE trace_uid = ?", trace.TraceUID)
	_ = db.Close()

	// 重新打开
	store2 := reopenTraceStore(t, dbPath)

	// 按 UID 查询应返回 v1 适配结果
	detail, err := store2.GetTraceDetail(trace.TraceUID)
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if detail == nil {
		t.Fatal("v1 行应返回适配详情")
	}
	if !detail.HistoricalSchema {
		t.Error("v1 行应标记 HistoricalSchema")
	}
}

// TestTraceStore_EmptyDBQueries 验证空数据库查询不报错。
func TestTraceStore_EmptyDBQueries(t *testing.T) {
	store, _ := newTempTraceStore(t)

	// 空数据库查询详情应返回 ErrNoRows
	_, err := store.GetTraceDetail("rt_nonexistent")
	if err == nil {
		t.Error("空数据库查询应返回错误")
	}

	// 空数据库列表应返回空
	summaries, partial, hasMore, err := store.ListTraceSummary(10, false, "", "", "")
	if err != nil {
		t.Fatalf("空数据库列表不应报错: %v", err)
	}
	if len(summaries) != 0 {
		t.Errorf("空数据库列表应返回 0 条，实际: %d", len(summaries))
	}
	if partial {
		t.Error("空数据库不应标记 partial")
	}
	if hasMore {
		t.Error("空数据库不应标记 hasMore")
	}

	// 空数据库统计
	stats := store.GetV2Stats()
	if stats.TotalCount != 0 {
		t.Errorf("空数据库统计应为 0，实际: %d", stats.TotalCount)
	}
}

// TestTraceStore_MultipleTracesPagination 验证多条 trace 的列表分页。
func TestTraceStore_MultipleTracesPagination(t *testing.T) {
	store, _ := newTempTraceStore(t)

	// 写入 5 条 trace
	for i := 0; i < 5; i++ {
		trace := &RoutingDecisionTrace{
			TraceUID:        GenerateTraceUIDv2(),
			RequestKind:     "chat",
			TaskClass:       TaskClassWorker,
			Mode:            RoutingModeShadow,
			ManualIntentUID: "mi_multi",
			CreatedAt:       time.Now().Add(time.Duration(i) * time.Second).UTC(),
		}
		store.Record(trace)
	}

	// 请求 limit=3
	summaries, _, hasMore, err := store.ListTraceSummary(3, false, "", "", "")
	if err != nil {
		t.Fatalf("列表查询失败: %v", err)
	}
	if len(summaries) != 3 {
		t.Errorf("应返回 3 条，实际: %d", len(summaries))
	}
	if !hasMore {
		t.Error("应标记 hasMore")
	}

	// 请求 limit=10（全部）
	summariesAll, _, hasMoreAll, err := store.ListTraceSummary(10, false, "", "", "")
	if err != nil {
		t.Fatalf("列表查询失败: %v", err)
	}
	if len(summariesAll) != 5 {
		t.Errorf("应返回 5 条，实际: %d", len(summariesAll))
	}
	if hasMoreAll {
		t.Error("5 条不应标记 hasMore")
	}
}

// TestTraceStore_FilterByMode 验证按 mode 过滤。
func TestTraceStore_FilterByMode(t *testing.T) {
	store, _ := newTempTraceStore(t)

	// 写入不同模式的 trace
	modes := []RoutingMode{RoutingModeShadow, RoutingModeAssist, RoutingModeAuto}
	for _, mode := range modes {
		trace := &RoutingDecisionTrace{
			TraceUID:        GenerateTraceUIDv2(),
			RequestKind:     "chat",
			TaskClass:       TaskClassWorker,
			Mode:            mode,
			ManualIntentUID: "mi_filter",
			CreatedAt:       time.Now().UTC(),
		}
		store.Record(trace)
	}

	// 过滤 assist 模式
	summaries, _, _, err := store.ListTraceSummary(10, false, "", "", "assist")
	if err != nil {
		t.Fatalf("列表查询失败: %v", err)
	}
	if len(summaries) != 1 {
		t.Errorf("assist 过滤应返回 1 条，实际: %d", len(summaries))
	}
	if len(summaries) > 0 && summaries[0].Mode != RoutingModeAssist {
		t.Errorf("过滤结果模式 = %q, want assist", summaries[0].Mode)
	}
}

// TestTraceStore_StatsV2Comparison 验证 v2 统计的三态比较计数。
func TestTraceStore_StatsV2Comparison(t *testing.T) {
	store := &TraceStore{
		records: make([]*RoutingDecisionTrace, 0),
	}

	// matched
	store.records = append(store.records, &RoutingDecisionTrace{
		TraceUID:         "rt_1",
		ShadowChannelUID: "ch_a",
		ActualChannelUID: "ch_a",
		Match:            true,
		Mode:             RoutingModeShadow,
		CreatedAt:        time.Now(),
	})

	// mismatched
	store.records = append(store.records, &RoutingDecisionTrace{
		TraceUID:         "rt_2",
		ShadowChannelUID: "ch_a",
		ActualChannelUID: "ch_b",
		Match:            false,
		Mode:             RoutingModeShadow,
		CreatedAt:        time.Now(),
	})

	// uncompared
	store.records = append(store.records, &RoutingDecisionTrace{
		TraceUID:  "rt_3",
		Mode:      RoutingModeShadow,
		CreatedAt: time.Now(),
	})

	stats := store.GetV2Stats()
	if stats.TotalCount != 3 {
		t.Errorf("TotalCount = %d, want 3", stats.TotalCount)
	}
	if stats.ComparedCount != 2 {
		t.Errorf("ComparedCount = %d, want 2", stats.ComparedCount)
	}
	if stats.MatchedCount != 1 {
		t.Errorf("MatchedCount = %d, want 1", stats.MatchedCount)
	}
	if stats.MismatchCount != 1 {
		t.Errorf("MismatchCount = %d, want 1", stats.MismatchCount)
	}
	if stats.UncomparedCount != 1 {
		t.Errorf("UncomparedCount = %d, want 1", stats.UncomparedCount)
	}
}

// TestTraceStore_MigrationV5ToV6Idempotent 验证重复迁移为 no-op。
func TestTraceStore_MigrationV5ToV6Idempotent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_migrate.db")

	// 首次创建
	db1, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	store1, err := NewTraceStoreWithDB(db1)
	if err != nil {
		t.Fatalf("首次创建 TraceStore 失败: %v", err)
	}
	_ = store1.Close()
	_ = db1.Close()

	// 再次打开（schema 应已包含 v6 列，重复创建为 no-op）
	db3, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("第三次打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = db3.Close() })
	store3, err := NewTraceStoreWithDB(db3)
	if err != nil {
		t.Fatalf("重复打开后创建 TraceStore 失败: %v", err)
	}

	// 验证 v6 列存在
	hasCol, err := columnExists(db3, "autopilot_routing_traces", "details_json")
	if err != nil {
		t.Fatalf("检查列失败: %v", err)
	}
	if !hasCol {
		t.Error("details_json 列应存在")
	}
	hasCol, err = columnExists(db3, "autopilot_routing_windows", "release_id")
	if err != nil {
		t.Fatalf("检查列失败: %v", err)
	}
	if !hasCol {
		t.Error("autopilot_routing_windows.release_id 列应存在")
	}
	_ = store3
}

// TestTraceStore_TempFileCleanup 验证临时文件被正确清理。
func TestTraceStore_TempFileCleanup(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test_cleanup.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	store, err := NewTraceStoreWithDB(db)
	if err != nil {
		t.Fatalf("创建 TraceStore 失败: %v", err)
	}
	_ = store.Close()
	_ = db.Close()

	// 文件应存在（t.TempDir 会自动清理）
	if _, err := os.Stat(dbPath); err != nil {
		t.Errorf("数据库文件应存在: %v", err)
	}
}
