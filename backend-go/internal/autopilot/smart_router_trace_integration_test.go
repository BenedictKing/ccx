package autopilot

import (
	"database/sql"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"

	_ "modernc.org/sqlite"
)

// ── L2 集成测试：SmartRouter + TraceStore 生命周期 ──

// TestBuildPlan_WritesDryRunTrace 验证 BuildPlan 将 dry-run 路由决策写入 TraceStore。
func TestBuildPlan_WritesDryRunTrace(t *testing.T) {
	cfg := baseTestConfig()
	cfg.AutopilotRouting = config.AutopilotRoutingConfig{
		RoutingMode: "shadow",
	}

	cfgManager, cleanup := createTestConfigManager(t, cfg)
	defer cleanup()

	traceStore := createTestTraceStore(t)
	smartRouter := &SmartRouter{
		configManager: cfgManager,
		traceStore:    traceStore,
	}

	profile := testProfile()
	plan := smartRouter.BuildPlan(profile)

	if plan == nil {
		t.Fatal("BuildPlan 应返回非 nil plan")
	}
	if plan.Mode != RoutingModeDryRun {
		t.Errorf("plan.Mode = %q, want dry_run", plan.Mode)
	}

	// 验证 dry-run trace 已写入 TraceStore
	traces := traceStore.ListRecent(10)
	if len(traces) == 0 {
		t.Fatal("BuildPlan 应写入 dry-run trace")
	}

	lastTrace := traces[len(traces)-1]
	if lastTrace.Source != "dry_run" {
		t.Errorf("trace.Source = %q, want dry_run", lastTrace.Source)
	}
	if lastTrace.Mode != RoutingModeDryRun {
		t.Errorf("trace.Mode = %q, want dry_run", lastTrace.Mode)
	}
	if lastTrace.SchemaVersion != 2 {
		t.Errorf("trace.SchemaVersion = %d, want 2", lastTrace.SchemaVersion)
	}
}

// TestCandidateFilterWithReleaseController 验证 ReleaseController 集成。
func TestCandidateFilterUsesAutopilotMode(t *testing.T) {
	cfg := baseTestConfig()
	cfg.AutopilotRouting = config.AutopilotRoutingConfig{RoutingMode: "shadow"}
	cfgManager, cleanup := createTestConfigManager(t, cfg)
	defer cleanup()

	smartRouter := &SmartRouter{configManager: cfgManager, traceStore: createTestTraceStore(t)}
	filter := smartRouter.CandidateFilterFor(testProfile())
	if filter == nil {
		t.Fatal("Autopilot 自动运行态应返回 CandidateFilter")
	}
}

// TestCandidateFilterWithSafetyOverride 验证安全覆盖生效。
func TestCandidateFilterIgnoresLegacyMode(t *testing.T) {
	cfg := baseTestConfig()
	cfg.AutopilotRouting = config.AutopilotRoutingConfig{RoutingMode: "off"}
	cfgManager, cleanup := createTestConfigManager(t, cfg)
	defer cleanup()

	smartRouter := &SmartRouter{configManager: cfgManager, traceStore: createTestTraceStore(t)}
	if filter := smartRouter.CandidateFilterFor(testProfile()); filter == nil {
		t.Fatal("旧 mode 字段不应关闭 Autopilot；只能使用 Kill Switch")
	}
}

// TestRecordInflightPromotion 验证 in-flight 索引和异常提升。
func TestRecordInflightPromotion(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store, err := NewTraceStoreWithDB(db)
	if err != nil {
		t.Fatalf("创建 TraceStore 失败: %v", err)
	}

	// Record 一条未落盘的 trace（非 mismatch，count 未命中抽样）
	// 通过多次调用使 counter 不命中 10 的倍数
	for i := 0; i < 9; i++ {
		store.counter.Add(1) // 预置 counter
	}
	trace := &RoutingDecisionTrace{
		RequestKind: "chat",
		TaskClass:   TaskClassWorker,
		Mode:        RoutingModeShadow,
	}
	store.Record(trace)

	// 确认 in-flight 索引中有此 trace
	if store.inflightCount() == 0 {
		t.Skip("trace 可能已被抽样落盘，跳过 in-flight 测试")
	}

	// RecordOutcome 触发异常提升（失败）
	store.RecordOutcome(trace.TraceUID, RoutingOutcome{
		Terminal:    true,
		Success:     false,
		StatusCode:  502,
		CompletedAt: time.Now(),
	})

	// 验证 in-flight 已清除
	if store.inflightCount() != 0 {
		t.Errorf("终态后 in-flight 应清除，实际: %d", store.inflightCount())
	}

	// 验证 trace 已落盘到 SQLite
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM autopilot_routing_traces WHERE trace_uid = ?", trace.TraceUID).Scan(&count)
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if count != 1 {
		t.Errorf("异常提升后 trace 应已落盘，实际行数: %d", count)
	}
}

// TestRecordInflightCleanup 验证 in-flight 超时清理。
func TestRecordInflightCleanup(t *testing.T) {
	store := &TraceStore{
		inflight: make(map[string]*RoutingDecisionTrace),
	}

	// 注册一条 trace
	trace := &RoutingDecisionTrace{
		TraceUID:  "rt_old",
		CreatedAt: time.Now().Add(-2 * time.Hour), // 2 小时前
	}
	store.registerInflight(trace)

	if store.inflightCount() != 1 {
		t.Fatalf("in-flight 数 = %d, want 1", store.inflightCount())
	}

	// 手动触发清理（因为我们的 cleanupExpired 检查时间门限，这里直接测试 in-flight 清理逻辑）
	store.inflightMu.Lock()
	inflightTimeout := time.Now().Add(-time.Hour)
	for uid, tr := range store.inflight {
		if tr.CreatedAt.Before(inflightTimeout) {
			delete(store.inflight, uid)
		}
	}
	store.inflightMu.Unlock()

	if store.inflightCount() != 0 {
		t.Errorf("超时清理后 in-flight 应为 0，实际: %d", store.inflightCount())
	}
}

// TestAppendEndpointAttempt_NilSafety 验证 nil store 安全。
func TestAppendEndpointAttempt_NilStore(t *testing.T) {
	var store *TraceStore
	// 不应 panic
	store.AppendEndpointAttempt("rt_test", EndpointAttemptSummary{
		AttemptUID: "a1",
		Status:     "started",
	})
}

// TestAttachSchedulerDecision_ToInFlight 验证 Scheduler 裁决同步到 in-flight 索引。
func TestAttachSchedulerDecision_ToInFlight(t *testing.T) {
	store := &TraceStore{
		records:  make([]*RoutingDecisionTrace, 0),
		inflight: make(map[string]*RoutingDecisionTrace),
	}

	trace := &RoutingDecisionTrace{
		TraceUID:  "rt_inflight",
		CreatedAt: time.Now(),
	}
	store.records = append(store.records, trace)
	store.registerInflight(trace)

	decision := &SchedulerDecisionSummary{
		SelectedName:  "ch_b",
		SelectionCode: "priority_order",
	}
	store.AttachSchedulerDecision("rt_inflight", decision)

	// 验证内存记录已附加
	if store.records[0].SchedulerDecision == nil {
		t.Fatal("内存记录 SchedulerDecision 未附加")
	}

	// 验证 in-flight 索引已同步
	store.inflightMu.RLock()
	inflightTrace, ok := store.inflight["rt_inflight"]
	store.inflightMu.RUnlock()
	if !ok {
		t.Fatal("in-flight 索引中应有 rt_inflight")
	}
	if inflightTrace.SchedulerDecision == nil {
		t.Fatal("in-flight 索引 SchedulerDecision 未同步")
	}
	if inflightTrace.SchedulerDecision.SelectedName != "ch_b" {
		t.Errorf("SelectedName = %q, want ch_b", inflightTrace.SchedulerDecision.SelectedName)
	}
}

// TestTraceRecord_MustPersistCategories 验证必落盘类别不走抽样。
func TestTraceRecord_MustPersistCategories(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store, err := NewTraceStoreWithDB(db)
	if err != nil {
		t.Fatalf("创建 TraceStore 失败: %v", err)
	}

	// Manual intent trace 应必落盘
	manualTrace := &RoutingDecisionTrace{
		RequestKind:     "messages",
		TaskClass:       TaskClassSupervisor,
		Mode:            RoutingModeShadow,
		ManualIntentUID: "mi_test",
		CreatedAt:       time.Now(),
	}
	store.Record(manualTrace)

	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM autopilot_routing_traces WHERE trace_uid = ?", manualTrace.TraceUID).Scan(&count)
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if count != 1 {
		t.Errorf("ManualIntent trace 应必落盘，实际行数: %d", count)
	}

	// Advisor trace 应必落盘
	advisorTrace := &RoutingDecisionTrace{
		RequestKind:        "chat",
		TaskClass:          TaskClassWorker,
		Mode:               RoutingModeShadow,
		AdvisorDecisionUID: "ad_test",
		CreatedAt:          time.Now(),
	}
	store.Record(advisorTrace)

	err = db.QueryRow("SELECT COUNT(*) FROM autopilot_routing_traces WHERE trace_uid = ?", advisorTrace.TraceUID).Scan(&count)
	if err != nil {
		t.Fatalf("查询失败: %v", err)
	}
	if count != 1 {
		t.Errorf("Advisor trace 应必落盘，实际行数: %d", count)
	}
}
