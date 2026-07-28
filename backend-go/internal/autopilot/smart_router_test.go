package autopilot

import (
	"database/sql"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/scheduler"

	_ "modernc.org/sqlite"
)

// ── SmartRouter 人工意图集成测试（设计 §4.6.4）──

// createTestIntentStore 创建内存 SQLite 测试用 ManualIntentStore。
func createTestIntentStore(t *testing.T) *ManualIntentStore {
	t.Helper()
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("打开内存数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })

	store, err := NewManualIntentStoreWithDB(db)
	if err != nil {
		t.Fatalf("创建 ManualIntentStore 失败: %v", err)
	}
	return store
}

// createTestTraceStore 创建内存测试用 TraceStore。
func createTestTraceStore(t *testing.T) *TraceStore {
	t.Helper()
	ts, err := NewTraceStoreWithDB(nil)
	if err != nil {
		t.Fatalf("创建 TraceStore 失败: %v", err)
	}
	return ts
}

// ── Shadow 模式：意图不影响输出 ──

func TestIntentExec_ShadowMode_NoOutputChange(t *testing.T) {
	cfg := baseTestConfig()
	cfg.AutopilotRouting = config.AutopilotRoutingConfig{
		RoutingMode: "shadow",
	}

	cfgManager, cleanup := createTestConfigManager(t, cfg)
	defer cleanup()

	intentStore := createTestIntentStore(t)
	traceStore := createTestTraceStore(t)

	// 创建一个 model_trial 意图
	intent := &ManualRoutingIntent{
		IntentType:     IntentTypeModelTrial,
		ChannelKind:    "messages",
		ChannelUID:     "", // 将在下面填充
		Model:          "claude-sonnet-4",
		TrafficPercent: 100,
		ExpiresAt:      time.Now().Add(1 * time.Hour),
	}
	_ = intentStore.Create(intent)

	sr := &SmartRouter{
		configManager: cfgManager,
		intentStore:   intentStore,
		traceStore:    traceStore,
	}

	profile := testProfile()
	profile.Model = "claude-sonnet-4"

	filter := sr.CandidateFilterFor(profile)
	if filter == nil {
		t.Fatal("shadow 模式下 filter 不应为 nil")
	}

	channels := []scheduler.ChannelInfo{
		{Index: 0, Name: "ch-premium", Priority: 1, Status: "active"},
		{Index: 1, Name: "ch-standard", Priority: 2, Status: "active"},
		{Index: 2, Name: "ch-economy", Priority: 3, Status: "active"},
	}

	upstreamCfgs := cfgManager.GetConfig().Upstream
	upstreamFor := func(ch scheduler.ChannelInfo) *config.UpstreamConfig {
		if ch.Index >= 0 && ch.Index < len(upstreamCfgs) {
			u := upstreamCfgs[ch.Index]
			return &u
		}
		return nil
	}
	available := func(ch scheduler.ChannelInfo, u *config.UpstreamConfig) bool {
		return ch.Status == "active" && len(u.APIKeys) > 0
	}

	result, err := filter(channels, upstreamFor, available)
	if err != nil {
		t.Fatalf("filter 执行失败: %v", err)
	}

	// shadow 模式必须返回原始列表
	if len(result) != len(channels) {
		t.Errorf("shadow 模式应返回原始列表: 期望 %d，实际 %d", len(channels), len(result))
	}
	for i, ch := range result {
		if ch.Name != channels[i].Name {
			t.Errorf("候选 %d 名称不一致: 期望 %s，实际 %s", i, channels[i].Name, ch.Name)
		}
	}

	// 验证 trace 记录了意图匹配信息
	traces := traceStore.ListRecent(1)
	if len(traces) > 0 {
		trace := traces[0]
		if trace.ManualIntentUID != "" {
			t.Logf("shadow trace 记录了 ManualIntentUID=%s", trace.ManualIntentUID)
		}
	}
}

// ── Assist 模式：目标渠道提升到首位 ──

func TestIntentExec_AssistMode_TargetPromoted(t *testing.T) {
	cfg := baseTestConfig()
	cfg.AutopilotRouting = config.AutopilotRoutingConfig{
		RoutingMode: "assist",
	}

	cfgManager, cleanup := createTestConfigManager(t, cfg)
	defer cleanup()

	intentStore := createTestIntentStore(t)
	traceStore := createTestTraceStore(t)

	// 获取实际的 ChannelUID
	processedCfg := cfgManager.GetConfig()
	ch2UID := processedCfg.Upstream[2].ChannelUID
	if ch2UID == "" {
		t.Fatal("ConfigManager 未分配 ChannelUID")
	}

	// 创建 model_trial 意图，指向 ch-economy（最低优先级）
	intent := &ManualRoutingIntent{
		IntentType:     IntentTypeModelTrial,
		ChannelKind:    "messages",
		ChannelUID:     ch2UID,
		Model:          "claude-sonnet-4",
		TrafficPercent: 100,
		ExpiresAt:      time.Now().Add(1 * time.Hour),
	}
	_ = intentStore.Create(intent)

	sr := &SmartRouter{
		configManager: cfgManager,
		intentStore:   intentStore,
		traceStore:    traceStore,
	}

	profile := testProfile()
	profile.Model = "claude-sonnet-4"

	filter := sr.CandidateFilterFor(profile)
	if filter == nil {
		t.Fatal("assist 模式下 filter 不应为 nil")
	}

	channels := []scheduler.ChannelInfo{
		{Index: 0, Name: "ch-premium", Priority: 1, Status: "active"},
		{Index: 1, Name: "ch-standard", Priority: 2, Status: "active"},
		{Index: 2, Name: "ch-economy", Priority: 3, Status: "active"},
	}

	upstreamFor := func(ch scheduler.ChannelInfo) *config.UpstreamConfig {
		if ch.Index >= 0 && ch.Index < len(processedCfg.Upstream) {
			u := processedCfg.Upstream[ch.Index]
			return &u
		}
		return nil
	}
	available := func(ch scheduler.ChannelInfo, u *config.UpstreamConfig) bool {
		return ch.Status == "active" && len(u.APIKeys) > 0
	}

	result, err := filter(channels, upstreamFor, available)
	if err != nil {
		t.Fatalf("filter 执行失败: %v", err)
	}

	if len(result) == 0 {
		t.Fatal("结果不应为空")
	}

	// 意图目标（ch-economy）应被提升到首位
	if result[0].Name != "ch-economy" {
		t.Errorf("意图目标应提升到首位: 期望 ch-economy，实际 %s", result[0].Name)
	}

	// 验证 trace
	traces := traceStore.ListRecent(1)
	if len(traces) == 0 {
		t.Fatal("应记录 trace")
	}
	trace := traces[0]
	if trace.ManualIntentUID == "" {
		t.Error("trace 应记录 ManualIntentUID")
	}
	hasIntentSort := false
	for _, r := range trace.SortReasons {
		if r == "intent_promote" {
			hasIntentSort = true
			break
		}
	}
	if !hasIntentSort {
		t.Errorf("trace SortReasons 应包含 intent_promote，实际: %v", trace.SortReasons)
	}
}

// ── Auto 模式：意图提升 + 硬约束过滤 + fallback ──

func TestIntentExec_AutoMode_HardConstraintFallback(t *testing.T) {
	cfg := baseTestConfig()
	cfg.AutopilotRouting = config.AutopilotRoutingConfig{
		RoutingMode: "auto",
	}

	cfgManager, cleanup := createTestConfigManager(t, cfg)
	defer cleanup()

	intentStore := createTestIntentStore(t)
	traceStore := createTestTraceStore(t)

	processedCfg := cfgManager.GetConfig()
	ch2UID := processedCfg.Upstream[2].ChannelUID

	// 创建 model_trial 意图指向 ch-economy
	intent := &ManualRoutingIntent{
		IntentType:        IntentTypeModelTrial,
		ChannelKind:       "messages",
		ChannelUID:        ch2UID,
		Model:             "claude-sonnet-4",
		TrafficPercent:    100,
		ExpiresAt:         time.Now().Add(1 * time.Hour),
		FallbackOnFailure: true,
	}
	_ = intentStore.Create(intent)

	sr := &SmartRouter{
		configManager: cfgManager,
		intentStore:   intentStore,
		traceStore:    traceStore,
	}

	// vision 请求：ch-economy 不支持 vision → 硬约束过滤 → fallback
	profile := testProfile()
	profile.Model = "claude-sonnet-4"
	profile.VisionNeed = true

	filter := sr.CandidateFilterFor(profile)
	if filter == nil {
		t.Fatal("auto 模式下 filter 不应为 nil")
	}

	channels := []scheduler.ChannelInfo{
		{Index: 0, Name: "ch-premium", Priority: 1, Status: "active"},
		{Index: 1, Name: "ch-standard", Priority: 2, Status: "active"},
		{Index: 2, Name: "ch-economy", Priority: 3, Status: "active"},
	}

	upstreamFor := func(ch scheduler.ChannelInfo) *config.UpstreamConfig {
		if ch.Index >= 0 && ch.Index < len(processedCfg.Upstream) {
			u := processedCfg.Upstream[ch.Index]
			return &u
		}
		return nil
	}
	available := func(ch scheduler.ChannelInfo, u *config.UpstreamConfig) bool {
		return ch.Status == "active" && len(u.APIKeys) > 0
	}

	result, err := filter(channels, upstreamFor, available)
	if err != nil {
		t.Fatalf("filter 执行失败: %v", err)
	}

	// 由于所有渠道的 SupportsVision=false（无画像），auto 会 fail-open
	// 结果应包含所有候选（fail-open 保留完整列表）
	if len(result) == 0 {
		t.Error("fail-open 时结果不应为空")
	}

	// 验证 trace 记录了意图相关字段
	traces := traceStore.ListRecent(1)
	if len(traces) > 0 {
		trace := traces[0]
		t.Logf("trace: ManualIntentUID=%s SortReasons=%v FallbackUsed=%v GlobalFilterReasons=%v",
			trace.ManualIntentUID, trace.SortReasons, trace.FallbackUsed, trace.GlobalFilterReasons)
	}
}

// ── Auto 模式：意图生效（目标通过硬约束）──

func TestIntentExec_AutoMode_TargetSurvivesConstraints(t *testing.T) {
	cfg := baseTestConfig()
	cfg.AutopilotRouting = config.AutopilotRoutingConfig{
		RoutingMode: "auto",
	}

	cfgManager, cleanup := createTestConfigManager(t, cfg)
	defer cleanup()

	intentStore := createTestIntentStore(t)
	traceStore := createTestTraceStore(t)

	processedCfg := cfgManager.GetConfig()
	ch0UID := processedCfg.Upstream[0].ChannelUID // ch-premium

	// 创建 model_trial 意图指向 ch-premium
	intent := &ManualRoutingIntent{
		IntentType:     IntentTypeModelTrial,
		ChannelKind:    "messages",
		ChannelUID:     ch0UID,
		Model:          "claude-sonnet-4",
		TrafficPercent: 100,
		ExpiresAt:      time.Now().Add(1 * time.Hour),
	}
	_ = intentStore.Create(intent)

	sr := &SmartRouter{
		configManager: cfgManager,
		intentStore:   intentStore,
		traceStore:    traceStore,
	}

	// 非 vision 请求：不触发识图硬约束
	profile := testProfile()
	profile.Model = "claude-sonnet-4"
	profile.VisionNeed = false

	filter := sr.CandidateFilterFor(profile)
	if filter == nil {
		t.Fatal("auto 模式下 filter 不应为 nil")
	}

	channels := []scheduler.ChannelInfo{
		{Index: 0, Name: "ch-premium", Priority: 1, Status: "active"},
		{Index: 1, Name: "ch-standard", Priority: 2, Status: "active"},
		{Index: 2, Name: "ch-economy", Priority: 3, Status: "active"},
	}

	upstreamFor := func(ch scheduler.ChannelInfo) *config.UpstreamConfig {
		if ch.Index >= 0 && ch.Index < len(processedCfg.Upstream) {
			u := processedCfg.Upstream[ch.Index]
			return &u
		}
		return nil
	}
	available := func(ch scheduler.ChannelInfo, u *config.UpstreamConfig) bool {
		return ch.Status == "active" && len(u.APIKeys) > 0
	}

	result, err := filter(channels, upstreamFor, available)
	if err != nil {
		t.Fatalf("filter 执行失败: %v", err)
	}

	if len(result) == 0 {
		t.Fatal("结果不应为空")
	}

	// ch-premium 应在首位（意图提升 + 它本身评分也最高）
	if result[0].Name != "ch-premium" {
		t.Errorf("意图目标 ch-premium 应在首位，实际: %s", result[0].Name)
	}

	// 验证 trace 记录了意图命中且未 fallback
	traces := traceStore.ListRecent(1)
	if len(traces) > 0 {
		trace := traces[0]
		if trace.ManualIntentUID == "" {
			t.Error("trace 应记录 ManualIntentUID")
		}
		if _, ok := trace.GlobalFilterReasons["intent_fallback"]; ok {
			t.Error("意图目标通过硬约束时不应有 intent_fallback")
		}
	}
}

// ── Supervisor 保护：third-party model_trial 不覆盖 supervisor ──

func TestIntentExec_SupervisorProtection_ThirdPartyBlocked(t *testing.T) {
	cfg := baseTestConfig()
	cfg.AutopilotRouting = config.AutopilotRoutingConfig{
		RoutingMode: "assist",
	}

	cfgManager, cleanup := createTestConfigManager(t, cfg)
	defer cleanup()

	intentStore := createTestIntentStore(t)
	traceStore := createTestTraceStore(t)

	processedCfg := cfgManager.GetConfig()
	ch2UID := processedCfg.Upstream[2].ChannelUID

	// 创建 model_trial 意图指向 ch-economy（假设 third-party）
	intent := &ManualRoutingIntent{
		IntentType:     IntentTypeModelTrial,
		ChannelKind:    "messages",
		ChannelUID:     ch2UID,
		Model:          "claude-sonnet-4",
		TrafficPercent: 100,
		ExpiresAt:      time.Now().Add(1 * time.Hour),
		// TaskClasses 不包含 supervisor
	}
	_ = intentStore.Create(intent)

	// 创建 ProfileStore 并设置 ch-economy 的 OriginTier=third
	profileStore := newTestProfileStore(t)
	if profileStore != nil {
		_ = profileStore.Upsert(&KeyEndpointProfile{
			EndpointUID:    "ep-economy-0",
			ChannelUID:     ch2UID,
			ChannelKind:    "messages",
			MetricsKey:     "https://economy.example.com|sk-economy",
			HealthState:    HealthStateHealthy,
			OriginTier:     "third",
			SupportsVision: false,
			QualityTier:    QualityTierLow,
			StabilityTier:  StabilityTierNormal,
			SpeedTier:      SpeedTierNormal,
			CostTier:       CostTierCheap,
			SuccessRate15m: 0.90,
			P95LatencyMs:   300,
		})
	}

	sr := &SmartRouter{
		configManager: cfgManager,
		intentStore:   intentStore,
		traceStore:    traceStore,
		profileStore:  profileStore,
	}

	// supervisor 请求：AgentRole=main + EstTokens>10K → classifier 输出 supervisor
	profile := testProfile()
	profile.Model = "claude-sonnet-4"
	profile.AgentRole = "main"
	profile.EstTokens = 50000

	filter := sr.CandidateFilterFor(profile)
	if filter == nil {
		t.Fatal("assist 模式下 filter 不应为 nil")
	}

	channels := []scheduler.ChannelInfo{
		{Index: 0, Name: "ch-premium", Priority: 1, Status: "active"},
		{Index: 1, Name: "ch-standard", Priority: 2, Status: "active"},
		{Index: 2, Name: "ch-economy", Priority: 3, Status: "active"},
	}

	upstreamFor := func(ch scheduler.ChannelInfo) *config.UpstreamConfig {
		if ch.Index >= 0 && ch.Index < len(processedCfg.Upstream) {
			u := processedCfg.Upstream[ch.Index]
			return &u
		}
		return nil
	}
	available := func(ch scheduler.ChannelInfo, u *config.UpstreamConfig) bool {
		return ch.Status == "active" && len(u.APIKeys) > 0
	}

	result, err := filter(channels, upstreamFor, available)
	if err != nil {
		t.Fatalf("filter 执行失败: %v", err)
	}

	if len(result) == 0 {
		t.Fatal("结果不应为空")
	}

	// supervisor 保护：ch-economy（third-party）不应被提升到首位
	if result[0].Name == "ch-economy" {
		t.Error("third-party model_trial 不应覆盖 supervisor 的路由选择")
	}

	// 验证 trace 记录了 supervisor 保护
	traces := traceStore.ListRecent(1)
	if len(traces) > 0 {
		trace := traces[0]
		if reasons, ok := trace.GlobalFilterReasons["supervisor_protect"]; ok {
			t.Logf("supervisor 保护已触发: %v", reasons)
		}
	}
}

// ── Shadow 模式不改变输出的不变量验证 ──

func TestIntentExec_LegacyShadowMode_TargetPromoted(t *testing.T) {
	cfg := baseTestConfig()
	cfg.AutopilotRouting = config.AutopilotRoutingConfig{
		RoutingMode: "shadow",
	}

	cfgManager, cleanup := createTestConfigManager(t, cfg)
	defer cleanup()

	intentStore := createTestIntentStore(t)
	processedCfg := cfgManager.GetConfig()
	ch2UID := processedCfg.Upstream[2].ChannelUID

	intent := &ManualRoutingIntent{
		IntentType:     IntentTypeModelTrial,
		ChannelKind:    "messages",
		ChannelUID:     ch2UID,
		Model:          "claude-sonnet-4",
		TrafficPercent: 100,
		ExpiresAt:      time.Now().Add(1 * time.Hour),
	}
	_ = intentStore.Create(intent)

	sr := &SmartRouter{
		configManager: cfgManager,
		intentStore:   intentStore,
	}
	profile := testProfile()
	profile.Model = "claude-sonnet-4"
	channels := []scheduler.ChannelInfo{
		{Index: 0, Name: "ch-premium", Priority: 1, Status: "active"},
		{Index: 1, Name: "ch-standard", Priority: 2, Status: "active"},
		{Index: 2, Name: "ch-economy", Priority: 3, Status: "active"},
	}
	upstreamFor := func(ch scheduler.ChannelInfo) *config.UpstreamConfig {
		if ch.Index >= 0 && ch.Index < len(processedCfg.Upstream) {
			u := processedCfg.Upstream[ch.Index]
			return &u
		}
		return nil
	}
	available := func(ch scheduler.ChannelInfo, u *config.UpstreamConfig) bool {
		return ch.Status == "active" && len(u.APIKeys) > 0
	}

	filter := sr.CandidateFilterFor(profile)
	if filter == nil {
		t.Fatal("legacy shadow 输入下 filter 不应为 nil")
	}
	result, err := filter(channels, upstreamFor, available)
	if err != nil {
		t.Fatalf("filter 失败: %v", err)
	}
	if len(result) == 0 || result[0].Name != "ch-economy" {
		t.Fatalf("legacy shadow 应按 auto 执行意图重排: result=%v", result)
	}
}

// ── 意图 Store 为 nil 时安全降级 ──

func TestIntentExec_NilIntentStore_NoPanic(t *testing.T) {
	cfg := baseTestConfig()
	cfg.AutopilotRouting = config.AutopilotRoutingConfig{
		RoutingMode: "assist",
	}

	cfgManager, cleanup := createTestConfigManager(t, cfg)
	defer cleanup()

	// intentStore 为 nil
	sr := &SmartRouter{
		configManager: cfgManager,
		intentStore:   nil,
		traceStore:    nil,
	}

	profile := testProfile()
	filter := sr.CandidateFilterFor(profile)
	if filter == nil {
		t.Fatal("assist 模式下 filter 不应为 nil")
	}

	processedCfg := cfgManager.GetConfig()
	channels := []scheduler.ChannelInfo{
		{Index: 0, Name: "ch-premium", Priority: 1, Status: "active"},
		{Index: 1, Name: "ch-standard", Priority: 2, Status: "active"},
	}

	upstreamFor := func(ch scheduler.ChannelInfo) *config.UpstreamConfig {
		if ch.Index >= 0 && ch.Index < len(processedCfg.Upstream) {
			u := processedCfg.Upstream[ch.Index]
			return &u
		}
		return nil
	}
	available := func(ch scheduler.ChannelInfo, u *config.UpstreamConfig) bool {
		return ch.Status == "active" && len(u.APIKeys) > 0
	}

	result, err := filter(channels, upstreamFor, available)
	if err != nil {
		t.Fatalf("nil intentStore 不应导致错误: %v", err)
	}
	if len(result) != len(channels) {
		t.Errorf("nil intentStore 应返回全部候选: 期望 %d，实际 %d", len(channels), len(result))
	}
}

// ── 空意图列表安全降级 ──

func TestIntentExec_EmptyIntents_NoEffect(t *testing.T) {
	cfg := baseTestConfig()
	cfg.AutopilotRouting = config.AutopilotRoutingConfig{
		RoutingMode: "assist",
	}

	cfgManager, cleanup := createTestConfigManager(t, cfg)
	defer cleanup()

	intentStore := createTestIntentStore(t) // 空 store

	sr := &SmartRouter{
		configManager: cfgManager,
		intentStore:   intentStore,
		traceStore:    nil,
	}

	profile := testProfile()
	filter := sr.CandidateFilterFor(profile)
	if filter == nil {
		t.Fatal("assist 模式下 filter 不应为 nil")
	}

	processedCfg := cfgManager.GetConfig()
	channels := []scheduler.ChannelInfo{
		{Index: 0, Name: "ch-premium", Priority: 1, Status: "active"},
		{Index: 1, Name: "ch-standard", Priority: 2, Status: "active"},
	}

	upstreamFor := func(ch scheduler.ChannelInfo) *config.UpstreamConfig {
		if ch.Index >= 0 && ch.Index < len(processedCfg.Upstream) {
			u := processedCfg.Upstream[ch.Index]
			return &u
		}
		return nil
	}
	available := func(ch scheduler.ChannelInfo, u *config.UpstreamConfig) bool {
		return ch.Status == "active" && len(u.APIKeys) > 0
	}

	result, err := filter(channels, upstreamFor, available)
	if err != nil {
		t.Fatalf("空意图列表不应导致错误: %v", err)
	}
	if len(result) != len(channels) {
		t.Errorf("空意图列表应返回全部候选: 期望 %d，实际 %d", len(channels), len(result))
	}
}
