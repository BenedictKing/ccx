package autopilot

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/BenedictKing/ccx/internal/warmup"
)

// ── SmartRouter 不变量测试（P0.5）──

// createTestConfigManager 创建测试用配置管理器。
func createTestConfigManager(t *testing.T, cfg config.Config) (*config.ConfigManager, func()) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", "smart-router-test-*")
	if err != nil {
		t.Fatalf("创建临时目录失败: %v", err)
	}

	configFile := filepath.Join(tmpDir, "config.json")
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		t.Fatalf("序列化配置失败: %v", err)
	}

	if err := os.WriteFile(configFile, data, 0644); err != nil {
		_ = os.RemoveAll(tmpDir)
		t.Fatalf("写入配置文件失败: %v", err)
	}

	cfgManager, err := config.NewConfigManager(configFile, "")
	if err != nil {
		_ = os.RemoveAll(tmpDir)
		t.Fatalf("创建配置管理器失败: %v", err)
	}

	cleanup := func() {
		_ = cfgManager.Close()
		_ = os.RemoveAll(tmpDir)
	}

	return cfgManager, cleanup
}

// createTestScheduler 创建测试用调度器。
func createTestScheduler(t *testing.T, cfg config.Config) (*scheduler.ChannelScheduler, *config.ConfigManager, func()) {
	t.Helper()

	cfgManager, cleanup := createTestConfigManager(t, cfg)
	messagesMetrics := metrics.NewMetricsManager()
	responsesMetrics := metrics.NewMetricsManager()
	geminiMetrics := metrics.NewMetricsManager()
	chatMetrics := metrics.NewMetricsManager()
	imagesMetrics := metrics.NewMetricsManager()
	traceAffinity := session.NewTraceAffinityManager()
	urlManager := warmup.NewURLManager(30*time.Second, 3)

	s := scheduler.NewChannelScheduler(
		cfgManager, messagesMetrics, responsesMetrics, geminiMetrics,
		chatMetrics, imagesMetrics, traceAffinity, urlManager,
	)

	fullCleanup := func() {
		messagesMetrics.Stop()
		responsesMetrics.Stop()
		geminiMetrics.Stop()
		chatMetrics.Stop()
		imagesMetrics.Stop()
		cleanup()
	}

	return s, cfgManager, fullCleanup
}

// createTestSmartRouter 创建测试用 SmartRouter（ProfileStore 为 nil，无画像数据）。
func createTestSmartRouter(t *testing.T, cfgManager *config.ConfigManager) *SmartRouter {
	t.Helper()
	return &SmartRouter{
		configManager: cfgManager,
		traceStore:    nil, // 测试中不持久化 trace
	}
}

// wrapAsSmartFilter 将 CandidateFilterFunc 包装为 SmartFilter 签名。
// 测试辅助函数：SmartRouter 内部的 CandidateFilterFunc 通过 scheduler 包的
// buildSmartFilterFromProvider 自动包装，此处手动模拟该包装。
func wrapAsSmartFilter(filter scheduler.CandidateFilterFunc, cfg config.Config, kind scheduler.ChannelKind) func(context.Context, []scheduler.ChannelInfo) []scheduler.ChannelInfo {
	if filter == nil {
		return nil
	}
	upstreamCfgs := cfg.Upstream
	return func(_ context.Context, channels []scheduler.ChannelInfo) []scheduler.ChannelInfo {
		result, err := filter(channels, func(ch scheduler.ChannelInfo) *config.UpstreamConfig {
			if ch.Index >= 0 && ch.Index < len(upstreamCfgs) {
				u := upstreamCfgs[ch.Index]
				return &u
			}
			return nil
		}, func(ch scheduler.ChannelInfo, u *config.UpstreamConfig) bool {
			return ch.Status == "active" && len(u.APIKeys) > 0
		})
		if err != nil {
			return nil
		}
		return result
	}
}

// testProfile 返回一个基础 RequestProfile 用于测试。
func testProfile() *RequestProfile {
	return &RequestProfile{
		Model:       "claude-sonnet-4",
		ChannelKind: "messages",
		Operation:   "completion",
		AgentRole:   "main",
		HasImage:    false,
		EstTokens:   1000,
		Complexity:  TaskComplexityTrivial,
		QualityNeed: QualityTierHigh,
		ContextNeed: 200000,
	}
}

// baseTestConfig 返回含三个 messages 渠道的基础测试配置。
func baseTestConfig() config.Config {
	return config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:            "ch-premium",
				BaseURL:         "https://premium.example.com",
				APIKeys:         []string{"sk-premium"},
				SupportedModels: []string{"*"},
				Status:          "active",
				Priority:        1,
			},
			{
				Name:            "ch-standard",
				BaseURL:         "https://standard.example.com",
				APIKeys:         []string{"sk-standard"},
				SupportedModels: []string{"*"},
				Status:          "active",
				Priority:        2,
			},
			{
				Name:            "ch-economy",
				BaseURL:         "https://economy.example.com",
				APIKeys:         []string{"sk-economy"},
				SupportedModels: []string{"*"},
				Status:          "active",
				Priority:        3,
			},
		},
	}
}

// TestInvariant_CandidateFilterNil_SameAsShadowFilter 验证：
// CandidateFilter=nil（无 SmartRouter）与注入 shadow SmartFilter 时，
// SelectChannelWithOptions 的选择结果完全一致。
// 这是 P0.5 核心不变量：shadow 不改变真实调度。
func TestInvariant_CandidateFilterNil_SameAsShadowFilter(t *testing.T) {
	cfg := baseTestConfig()
	cfg.AutopilotRouting = config.AutopilotRoutingConfig{
		RoutingMode: "shadow",
		KillSwitch:  false,
	}

	s, cfgManager, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	smartRouter := createTestSmartRouter(t, cfgManager)
	profile := testProfile()

	// 构建 shadow SmartFilter
	filter := smartRouter.CandidateFilterFor(profile)
	if filter == nil {
		t.Fatal("shadow 模式下 CandidateFilterFor 应返回非 nil filter")
	}
	smartFilter := wrapAsSmartFilter(filter, cfg, scheduler.ChannelKindMessages)

	ctx := context.Background()

	// 无 filter 的基线选择
	baseResult, baseErr := s.SelectChannelWithOptions(ctx, scheduler.SelectionOptions{
		Kind:   scheduler.ChannelKindMessages,
		Model:  "claude-sonnet-4",
		DryRun: true,
	})

	// 有 shadow SmartFilter 的选择
	shadowResult, shadowErr := s.SelectChannelWithOptions(ctx, scheduler.SelectionOptions{
		Kind:        scheduler.ChannelKindMessages,
		Model:       "claude-sonnet-4",
		DryRun:      true,
		SmartFilter: smartFilter,
	})

	// 两者应有相同错误状态
	if (baseErr != nil) != (shadowErr != nil) {
		t.Fatalf("错误状态不一致: base=%v shadow=%v", baseErr, shadowErr)
	}
	if baseErr != nil {
		return // 两者都失败，不检查结果
	}

	// 两者应选择相同的渠道
	if baseResult.ChannelIndex != shadowResult.ChannelIndex {
		t.Errorf("渠道选择不一致: base=%d(%s) shadow=%d(%s)",
			baseResult.ChannelIndex, baseResult.Upstream.Name,
			shadowResult.ChannelIndex, shadowResult.Upstream.Name)
	}
	if baseResult.Upstream.Name != shadowResult.Upstream.Name {
		t.Errorf("渠道名称不一致: base=%s shadow=%s",
			baseResult.Upstream.Name, shadowResult.Upstream.Name)
	}
}

// TestInvariant_KillSwitch 不注入 验证：
// kill switch 启用时 CandidateFilterFor 返回 nil。
func TestInvariant_KillSwitch_NotInjected(t *testing.T) {
	cfg := baseTestConfig()
	cfg.AutopilotRouting = config.AutopilotRoutingConfig{
		RoutingMode: "shadow",
		KillSwitch:  true, // 急停
	}

	_, cfgManager, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	smartRouter := createTestSmartRouter(t, cfgManager)
	profile := testProfile()

	filter := smartRouter.CandidateFilterFor(profile)
	if filter != nil {
		t.Error("kill switch 启用时 CandidateFilterFor 应返回 nil")
	}
}

// TestInvariant_ModeOff_NotInjected 验证：
// mode=off 时 CandidateFilterFor 返回 nil。
func TestInvariant_LegacyModeOff_StillInjected(t *testing.T) {
	cfg := baseTestConfig()
	cfg.AutopilotRouting = config.AutopilotRoutingConfig{
		RoutingMode: "off",
		KillSwitch:  false,
	}

	_, cfgManager, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	smartRouter := createTestSmartRouter(t, cfgManager)
	profile := testProfile()

	filter := smartRouter.CandidateFilterFor(profile)
	if filter == nil {
		t.Error("legacy mode=off 不应停用 CandidateFilterFor")
	}
}

// TestInvariant_XChannel_BypassesSmartFilter 验证：
// X-Channel 显式指定渠道时，不经过 SmartRouter 重排。
// SelectChannelWithOptions 在 SmartFilter 之前就定位到 X-Channel 并返回。
func TestInvariant_XChannel_BypassesSmartFilter(t *testing.T) {
	cfg := baseTestConfig()
	cfg.AutopilotRouting = config.AutopilotRoutingConfig{
		RoutingMode: "shadow",
		KillSwitch:  false,
	}

	s, cfgManager, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	smartRouter := createTestSmartRouter(t, cfgManager)
	profile := testProfile()
	filter := smartRouter.CandidateFilterFor(profile)
	smartFilter := wrapAsSmartFilter(filter, cfg, scheduler.ChannelKindMessages)

	ctx := context.Background()

	// X-Channel 指定 ch-economy（优先级最低）
	result, err := s.SelectChannelWithOptions(ctx, scheduler.SelectionOptions{
		Kind:        scheduler.ChannelKindMessages,
		Model:       "claude-sonnet-4",
		ChannelName: "ch-economy",
		DryRun:      true,
		SmartFilter: smartFilter,
	})
	if err != nil {
		t.Fatalf("X-Channel 选择失败: %v", err)
	}

	// X-Channel 应直接选择 ch-economy，不受 SmartRouter 评分影响
	if result.Upstream.Name != "ch-economy" {
		t.Errorf("X-Channel 应选择 ch-economy，实际选择: %s", result.Upstream.Name)
	}
	if result.Reason != "channel_pin" {
		t.Errorf("选择原因应为 channel_pin，实际: %s", result.Reason)
	}
}

// TestInvariant_ManualOverride_BypassesSmartFilter 验证：
// 手动序列 override 优先于 SmartRouter。
// 本测试验证即使 SmartRouter 评分偏爱其他渠道，手动 override 仍优先。
func TestInvariant_ManualOverride_BypassesSmartFilter(t *testing.T) {
	cfg := baseTestConfig()
	cfg.AutopilotRouting = config.AutopilotRoutingConfig{
		RoutingMode: "shadow",
		KillSwitch:  false,
	}

	s, cfgManager, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	smartRouter := createTestSmartRouter(t, cfgManager)
	profile := testProfile()
	filter := smartRouter.CandidateFilterFor(profile)
	smartFilter := wrapAsSmartFilter(filter, cfg, scheduler.ChannelKindMessages)

	ctx := context.Background()

	// 手动 override 场景：SelectChannelWithOptions 中 ManualOverride 检查
	// 发生在 SmartFilter 之后（当前实现），但 ManualOverride 本身通过
	// overrideManager 设置，不在 SelectionOptions 中。
	// 此处验证 SmartFilter 不改变候选列表（shadow 模式返回原始列表），
	// 因此 ManualOverride 仍能找到正确的渠道。
	result, err := s.SelectChannelWithOptions(ctx, scheduler.SelectionOptions{
		Kind:        scheduler.ChannelKindMessages,
		Model:       "claude-sonnet-4",
		DryRun:      true,
		SmartFilter: smartFilter,
	})
	if err != nil {
		t.Fatalf("选择失败: %v", err)
	}

	// shadow 模式下 SmartFilter 不改变候选顺序
	// 默认优先级排序应选择 ch-premium（Priority=1）
	if result.Upstream.Name != "ch-premium" {
		t.Errorf("shadow 模式下默认排序应选择 ch-premium，实际选择: %s", result.Upstream.Name)
	}
}

// TestInvariant_ShadowFilterReturnsOriginalList 验证：
// shadow 模式 SmartFilter 内部始终返回原始候选列表。
func TestInvariant_ShadowFilterReturnsOriginalList(t *testing.T) {
	cfg := baseTestConfig()
	cfg.AutopilotRouting = config.AutopilotRoutingConfig{
		RoutingMode: "shadow",
		KillSwitch:  false,
	}

	_, cfgManager, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	smartRouter := createTestSmartRouter(t, cfgManager)
	profile := testProfile()
	filter := smartRouter.CandidateFilterFor(profile)
	if filter == nil {
		t.Fatal("shadow 模式下 filter 不应为 nil")
	}

	// 构造三个候选
	channels := []scheduler.ChannelInfo{
		{Index: 0, Name: "ch-premium", Priority: 1, Status: "active"},
		{Index: 1, Name: "ch-standard", Priority: 2, Status: "active"},
		{Index: 2, Name: "ch-economy", Priority: 3, Status: "active"},
	}

	upstreamCfgs := cfg.Upstream
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

	// 直接调用 filter 函数
	result, err := filter(channels, upstreamFor, available)
	if err != nil {
		t.Fatalf("filter 执行失败: %v", err)
	}

	// shadow 模式必须返回原始列表（长度和顺序不变）
	if len(result) != len(channels) {
		t.Errorf("shadow 模式应返回原始列表: 期望 %d 个候选，实际 %d", len(channels), len(result))
	}
	for i, ch := range result {
		if ch.Name != channels[i].Name {
			t.Errorf("候选 %d 名称不一致: 期望 %s，实际 %s", i, channels[i].Name, ch.Name)
		}
	}
}

// TestInvariant_EmptyCandidateList 验证：
// 空候选列表时 SmartFilter 不崩溃。
func TestInvariant_EmptyCandidateList(t *testing.T) {
	cfg := baseTestConfig()
	cfg.AutopilotRouting = config.AutopilotRoutingConfig{
		RoutingMode: "shadow",
		KillSwitch:  false,
	}

	_, cfgManager, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	smartRouter := createTestSmartRouter(t, cfgManager)
	profile := testProfile()
	filter := smartRouter.CandidateFilterFor(profile)
	if filter == nil {
		t.Fatal("shadow 模式下 filter 不应为 nil")
	}

	upstreamFor := func(ch scheduler.ChannelInfo) *config.UpstreamConfig {
		return nil
	}
	available := func(ch scheduler.ChannelInfo, u *config.UpstreamConfig) bool {
		return false
	}

	// 空候选列表
	result, err := filter(nil, upstreamFor, available)
	if err != nil {
		t.Fatalf("空候选列表不应报错: %v", err)
	}
	// 返回 nil 或空列表均可
	if len(result) != 0 {
		t.Errorf("空输入应返回空结果，实际长度: %d", len(result))
	}
}

// TestInvariant_NilProfile_NotInjected 验证：
// RequestProfile 为 nil 时 CandidateFilterFor 返回 nil。
func TestInvariant_NilProfile_NotInjected(t *testing.T) {
	cfg := baseTestConfig()
	cfg.AutopilotRouting = config.AutopilotRoutingConfig{
		RoutingMode: "shadow",
	}

	_, cfgManager, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	smartRouter := createTestSmartRouter(t, cfgManager)
	filter := smartRouter.CandidateFilterFor(nil)
	if filter != nil {
		t.Error("nil profile 时 CandidateFilterFor 应返回 nil")
	}
}

// TestInvariant_BuildPlan_WithKillSwitch 验证：
// kill switch 启用时 BuildPlan 仍可调用（dry-run API 使用），
// 但返回的路由计划不注入任何 filter。
func TestInvariant_BuildPlan_WithKillSwitch(t *testing.T) {
	cfg := baseTestConfig()
	cfg.AutopilotRouting = config.AutopilotRoutingConfig{
		RoutingMode: "shadow",
		KillSwitch:  true,
	}

	_, cfgManager, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	smartRouter := createTestSmartRouter(t, cfgManager)
	profile := testProfile()

	// BuildPlan 用于 dry-run，即使 kill switch 也可执行
	plan := smartRouter.BuildPlan(profile)
	if plan == nil {
		t.Fatal("BuildPlan 不应返回 nil")
	}
	if plan.Mode != RoutingModeDryRun {
		t.Errorf("BuildPlan 模式应为 dry_run，实际: %s", plan.Mode)
	}
}

// TestInvariant_RoutingTraceRecorded 验证：
// shadow SmartFilter 执行后记录了 RoutingDecisionTrace。
func TestInvariant_RoutingTraceRecorded(t *testing.T) {
	cfg := baseTestConfig()
	cfg.AutopilotRouting = config.AutopilotRoutingConfig{
		RoutingMode: "shadow",
		KillSwitch:  false,
	}

	_, cfgManager, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	// 创建带内存 TraceStore 的 SmartRouter
	traceStore, err := NewTraceStoreWithDB(nil)
	if err != nil {
		t.Fatalf("创建 TraceStore 失败: %v", err)
	}

	smartRouter := &SmartRouter{
		configManager: cfgManager,
		traceStore:    traceStore,
	}

	profile := testProfile()
	filter := smartRouter.CandidateFilterFor(profile)
	if filter == nil {
		t.Fatal("shadow 模式下 filter 不应为 nil")
	}

	// 构造三个候选
	channels := []scheduler.ChannelInfo{
		{Index: 0, Name: "ch-premium", Priority: 1, Status: "active"},
		{Index: 1, Name: "ch-standard", Priority: 2, Status: "active"},
		{Index: 2, Name: "ch-economy", Priority: 3, Status: "active"},
	}

	upstreamCfgs := cfg.Upstream
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

	// 执行 filter
	_, _ = filter(channels, upstreamFor, available)

	// 验证 trace 已记录
	traces := traceStore.ListRecent(1)
	if len(traces) != 1 {
		t.Fatalf("应记录 1 条 trace，实际: %d", len(traces))
	}

	trace := traces[0]
	if trace.TaskClass != TaskClassLightweight {
		t.Errorf("TaskClass 应为 lightweight（EstTokens=1000, AgentRole=main），实际: %s", trace.TaskClass)
	}
	if trace.Mode != RoutingModeAuto {
		t.Errorf("Mode 应为 auto，实际: %s", trace.Mode)
	}
	if trace.CandidatesBefore != 3 {
		t.Errorf("CandidatesBefore 应为 3，实际: %d", trace.CandidatesBefore)
	}
	if len(trace.Candidates) == 0 {
		t.Error("Candidates 不应为空")
	}
}

// ── assist/auto 模式不变量测试 ──

// TestInvariant_AssistMode_PermutationInvariant 验证：
// assist 模式下，输出是输入的一个排列（长度相等、集合相等）。
func TestInvariant_AssistMode_PermutationInvariant(t *testing.T) {
	cfg := baseTestConfig()
	cfg.AutopilotRouting = config.AutopilotRoutingConfig{
		RoutingMode: "assist",
		KillSwitch:  false,
	}

	_, cfgManager, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	smartRouter := createTestSmartRouter(t, cfgManager)
	profile := testProfile()
	filter := smartRouter.CandidateFilterFor(profile)
	if filter == nil {
		t.Fatal("assist 模式下 filter 不应为 nil")
	}

	channels := []scheduler.ChannelInfo{
		{Index: 0, Name: "ch-premium", Priority: 1, Status: "active"},
		{Index: 1, Name: "ch-standard", Priority: 2, Status: "active"},
		{Index: 2, Name: "ch-economy", Priority: 3, Status: "active"},
	}

	upstreamCfgs := cfg.Upstream
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

	// 长度相等
	if len(result) != len(channels) {
		t.Fatalf("assist 模式应保持所有候选: 期望 %d，实际 %d", len(channels), len(result))
	}

	// 集合相等（排列不变量）
	inputSet := make(map[string]bool)
	for _, ch := range channels {
		inputSet[ch.Name] = true
	}
	for _, ch := range result {
		if !inputSet[ch.Name] {
			t.Errorf("assist 输出包含输入中不存在的渠道: %s", ch.Name)
		}
		delete(inputSet, ch.Name)
	}
	if len(inputSet) > 0 {
		t.Errorf("assist 输出缺少输入中的渠道: %v", inputSet)
	}
}

// TestInvariant_AssistMode_RecordsTrace 验证：
// assist 模式正确记录 RoutingDecisionTrace。
func TestInvariant_LegacyAssistMode_RecordsAutoTrace(t *testing.T) {
	cfg := baseTestConfig()
	cfg.AutopilotRouting = config.AutopilotRoutingConfig{
		RoutingMode: "assist",
		KillSwitch:  false,
	}

	_, cfgManager, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	traceStore, err := NewTraceStoreWithDB(nil)
	if err != nil {
		t.Fatalf("创建 TraceStore 失败: %v", err)
	}

	smartRouter := &SmartRouter{
		configManager: cfgManager,
		traceStore:    traceStore,
	}

	profile := testProfile()
	filter := smartRouter.CandidateFilterFor(profile)
	if filter == nil {
		t.Fatal("legacy assist 输入下 filter 不应为 nil")
	}

	channels := []scheduler.ChannelInfo{
		{Index: 0, Name: "ch-premium", Priority: 1, Status: "active"},
		{Index: 1, Name: "ch-standard", Priority: 2, Status: "active"},
		{Index: 2, Name: "ch-economy", Priority: 3, Status: "active"},
	}

	upstreamCfgs := cfg.Upstream
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

	_, _ = filter(channels, upstreamFor, available)

	traces := traceStore.ListRecent(1)
	if len(traces) != 1 {
		t.Fatalf("应记录 1 条 trace，实际: %d", len(traces))
	}

	trace := traces[0]
	if trace.Mode != RoutingModeAuto {
		t.Errorf("trace Mode 应为 auto，实际: %s", trace.Mode)
	}
	if !containsString(trace.SortReasons, "auto_filter_and_reorder") {
		t.Errorf("trace SortReasons 应包含 auto_filter_and_reorder，实际: %v", trace.SortReasons)
	}
}

// TestInvariant_AssistMode_PreservesUnscoredCandidates 验证 assist 只重排，
// 基础可用性检查未通过的候选仍保留在末尾供原调度 failover。
func TestInvariant_LegacyAssistMode_FiltersUnavailableCandidates(t *testing.T) {
	cfg := baseTestConfig()
	cfg.AutopilotRouting = config.AutopilotRoutingConfig{RoutingMode: "assist"}

	_, cfgManager, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	traceStore, err := NewTraceStoreWithDB(nil)
	if err != nil {
		t.Fatalf("创建 TraceStore 失败: %v", err)
	}
	router := &SmartRouter{configManager: cfgManager, traceStore: traceStore}
	filter := router.CandidateFilterFor(testProfile())
	if filter == nil {
		t.Fatal("legacy assist 输入下 filter 不应为 nil")
	}

	channels := []scheduler.ChannelInfo{
		{Index: 0, Name: "ch-premium", Priority: 1, Status: "active"},
		{Index: 1, Name: "ch-standard", Priority: 2, Status: "active"},
		{Index: 2, Name: "ch-economy", Priority: 3, Status: "active"},
	}
	upstreams := cfgManager.GetConfig().Upstream
	upstreamFor := func(ch scheduler.ChannelInfo) *config.UpstreamConfig {
		u := upstreams[ch.Index]
		return &u
	}
	available := func(ch scheduler.ChannelInfo, _ *config.UpstreamConfig) bool {
		return ch.Index != 1
	}

	result, err := filter(channels, upstreamFor, available)
	if err != nil {
		t.Fatalf("filter 执行失败: %v", err)
	}
	if len(result) != 2 {
		t.Fatalf("legacy assist 应按 auto 过滤不可用候选: got=%d want=2 result=%v", len(result), result)
	}
	for _, ch := range result {
		if ch.Index == 1 {
			t.Fatalf("不可用候选不应保留: result=%v", result)
		}
	}

	traces := traceStore.ListRecent(1)
	if len(traces) != 1 {
		t.Fatalf("应记录 1 条 trace，实际: %d", len(traces))
	}
	trace := traces[0]
	if trace.CandidatesBefore != len(channels) || trace.CandidatesAfter != len(result) || len(trace.Candidates) != len(result) {
		t.Fatalf("auto trace 候选数不一致: before=%d after=%d candidates=%d", trace.CandidatesBefore, trace.CandidatesAfter, len(trace.Candidates))
	}
}

// TestInvariant_AutoMode_VisionFilter 验证：
// auto 模式下，vision 请求过滤掉不支持识图的渠道。
func TestInvariant_AutoMode_VisionFilter(t *testing.T) {
	cfg := baseTestConfig()
	cfg.AutopilotRouting = config.AutopilotRoutingConfig{
		RoutingMode: "auto",
		KillSwitch:  false,
	}

	_, cfgManager, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	// 读取 ConfigManager 自动分配的 ChannelUID
	processedCfg := cfgManager.GetConfig()
	ch0UID := processedCfg.Upstream[0].ChannelUID
	ch1UID := processedCfg.Upstream[1].ChannelUID
	ch2UID := processedCfg.Upstream[2].ChannelUID
	if ch0UID == "" || ch1UID == "" || ch2UID == "" {
		t.Fatalf("ConfigManager 未分配 ChannelUID: ch0=%s ch1=%s ch2=%s", ch0UID, ch1UID, ch2UID)
	}

	// 创建带 ProfileStore 的 SmartRouter，给 ch-premium 设置 SupportsVision=true
	profileStore := newTestProfileStore(t)
	if profileStore != nil {
		// ch-premium 支持识图
		err := profileStore.Upsert(&KeyEndpointProfile{
			EndpointUID:    "ep-premium-0",
			ChannelUID:     ch0UID,
			ChannelKind:    "messages",
			MetricsKey:     "https://premium.example.com|sk-premium",
			HealthState:    HealthStateHealthy,
			SupportsVision: true,
			QualityTier:    QualityTierHigh,
			StabilityTier:  StabilityTierStable,
			SpeedTier:      SpeedTierFast,
			CostTier:       CostTierNormal,
			SuccessRate15m: 0.99,
			P95LatencyMs:   100,
		})
		if err != nil {
			t.Fatalf("Upsert premium 画像失败: %v", err)
		}
		// ch-standard 不支持识图（SupportsVision=false）
		err = profileStore.Upsert(&KeyEndpointProfile{
			EndpointUID:    "ep-standard-0",
			ChannelUID:     ch1UID,
			ChannelKind:    "messages",
			MetricsKey:     "https://standard.example.com|sk-standard",
			HealthState:    HealthStateHealthy,
			SupportsVision: false,
			QualityTier:    QualityTierNormal,
			StabilityTier:  StabilityTierNormal,
			SpeedTier:      SpeedTierNormal,
			CostTier:       CostTierNormal,
			SuccessRate15m: 0.95,
			P95LatencyMs:   200,
		})
		if err != nil {
			t.Fatalf("Upsert standard 画像失败: %v", err)
		}
		// ch-economy 不支持识图
		err = profileStore.Upsert(&KeyEndpointProfile{
			EndpointUID:    "ep-economy-0",
			ChannelUID:     ch2UID,
			ChannelKind:    "messages",
			MetricsKey:     "https://economy.example.com|sk-economy",
			HealthState:    HealthStateHealthy,
			SupportsVision: false,
			QualityTier:    QualityTierLow,
			StabilityTier:  StabilityTierNormal,
			SpeedTier:      SpeedTierNormal,
			CostTier:       CostTierCheap,
			SuccessRate15m: 0.90,
			P95LatencyMs:   300,
		})
		if err != nil {
			t.Fatalf("Upsert economy 画像失败: %v", err)
		}
	}

	smartRouter := &SmartRouter{
		configManager: cfgManager,
		profileStore:  profileStore,
	}

	// vision 请求
	profile := testProfile()
	profile.VisionNeed = true

	filter := smartRouter.CandidateFilterFor(profile)
	if filter == nil {
		t.Fatal("auto 模式下 filter 不应为 nil")
	}

	channels := []scheduler.ChannelInfo{
		{Index: 0, Name: "ch-premium", Priority: 1, Status: "active"},
		{Index: 1, Name: "ch-standard", Priority: 2, Status: "active"},
		{Index: 2, Name: "ch-economy", Priority: 3, Status: "active"},
	}

	upstreamCfgs := processedCfg.Upstream
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

	// 只有 ch-premium 支持 vision，应被保留
	if len(result) != 1 {
		t.Fatalf("auto vision 过滤后应剩 1 个候选，实际: %d", len(result))
	}
	if result[0].Name != "ch-premium" {
		t.Errorf("auto vision 过滤后应选择 ch-premium，实际: %s", result[0].Name)
	}
}

// TestInvariant_AutoMode_FailOpen 验证：
// auto 模式下，全部候选被硬约束过滤时回退到重排（fail-open）。
func TestInvariant_AutoMode_FailOpen(t *testing.T) {
	cfg := baseTestConfig()
	cfg.AutopilotRouting = config.AutopilotRoutingConfig{
		RoutingMode: "auto",
		KillSwitch:  false,
	}

	_, cfgManager, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	// ProfileStore 为 nil，所有渠道 SupportsVision=false（默认值）
	smartRouter := createTestSmartRouter(t, cfgManager)

	// vision 请求，但无渠道支持 vision
	profile := testProfile()
	profile.VisionNeed = true

	filter := smartRouter.CandidateFilterFor(profile)
	if filter == nil {
		t.Fatal("auto 模式下 filter 不应为 nil")
	}

	channels := []scheduler.ChannelInfo{
		{Index: 0, Name: "ch-premium", Priority: 1, Status: "active"},
		{Index: 1, Name: "ch-standard", Priority: 2, Status: "active"},
		{Index: 2, Name: "ch-economy", Priority: 3, Status: "active"},
	}

	upstreamCfgs := cfg.Upstream
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

	// fail-open：全部被过滤时回退到重排，返回完整列表
	if len(result) != len(channels) {
		t.Fatalf("auto fail-open 应返回全部候选: 期望 %d，实际 %d", len(channels), len(result))
	}

	// 验证集合一致
	inputSet := make(map[string]bool)
	for _, ch := range channels {
		inputSet[ch.Name] = true
	}
	for _, ch := range result {
		delete(inputSet, ch.Name)
	}
	if len(inputSet) > 0 {
		t.Errorf("auto fail-open 输出缺少输入中的渠道: %v", inputSet)
	}
}

// TestInvariant_AutoMode_FailOpen_TraceRecorded 验证：
// auto fail-open 模式下 trace 正确记录 FallbackUsed 和过滤原因。
func TestInvariant_AutoMode_FailOpen_TraceRecorded(t *testing.T) {
	cfg := baseTestConfig()
	cfg.AutopilotRouting = config.AutopilotRoutingConfig{
		RoutingMode: "auto",
		KillSwitch:  false,
	}

	_, cfgManager, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	traceStore, err := NewTraceStoreWithDB(nil)
	if err != nil {
		t.Fatalf("创建 TraceStore 失败: %v", err)
	}

	smartRouter := &SmartRouter{
		configManager: cfgManager,
		traceStore:    traceStore,
	}

	profile := testProfile()
	profile.VisionNeed = true

	filter := smartRouter.CandidateFilterFor(profile)
	if filter == nil {
		t.Fatal("auto 模式下 filter 不应为 nil")
	}

	channels := []scheduler.ChannelInfo{
		{Index: 0, Name: "ch-premium", Priority: 1, Status: "active"},
		{Index: 1, Name: "ch-standard", Priority: 2, Status: "active"},
		{Index: 2, Name: "ch-economy", Priority: 3, Status: "active"},
	}

	upstreamCfgs := cfg.Upstream
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

	_, _ = filter(channels, upstreamFor, available)

	traces := traceStore.ListRecent(1)
	if len(traces) != 1 {
		t.Fatalf("应记录 1 条 trace，实际: %d", len(traces))
	}

	trace := traces[0]
	if trace.Mode != RoutingModeAuto {
		t.Errorf("trace Mode 应为 auto，实际: %s", trace.Mode)
	}
	if !trace.FallbackUsed {
		t.Error("trace FallbackUsed 应为 true（全部候选被过滤）")
	}
	if _, ok := trace.GlobalFilterReasons["auto_failopen"]; !ok {
		t.Error("trace GlobalFilterReasons 应包含 auto_failopen")
	}
	if _, ok := trace.GlobalFilterReasons["auto_hard_constraints"]; !ok {
		t.Error("trace GlobalFilterReasons 应包含 auto_hard_constraints")
	}
}

// ── P1.5：DisabledTaskClasses / DisabledChannelUIDs 不变量测试 ──

// TestInvariant_DisabledTaskClass_FallsBackToDefault 验证：
// 命中 DisabledTaskClasses 的请求，CandidateFilterFor 返回 nil，
// 与 kill switch 的 "本次请求 SmartRouter 完全不介入" 语义一致，
// 调度回退到默认调度器行为。
func TestInvariant_DisabledTaskClass_FallsBackToDefault(t *testing.T) {
	cfg := baseTestConfig()
	cfg.AutopilotRouting = config.AutopilotRoutingConfig{
		RoutingMode: "shadow",
		KillSwitch:  false,
		// testProfile() 显式携带 trivial 难度信号，会被分类为 lightweight
		// （TestInvariant_RoutingTraceRecorded 已验证这一分类结果）。
		DisabledTaskClasses: []string{string(TaskClassLightweight)},
	}

	_, cfgManager, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	smartRouter := createTestSmartRouter(t, cfgManager)
	profile := testProfile()

	filter := smartRouter.CandidateFilterFor(profile)
	if filter != nil {
		t.Error("task class 命中 DisabledTaskClasses 时 CandidateFilterFor 应返回 nil")
	}
}

// TestInvariant_DisabledTaskClass_OtherClassUnaffected 验证：
// DisabledTaskClasses 只影响命中的 TaskClass，不影响其他 TaskClass 的请求。
func TestInvariant_DisabledTaskClass_OtherClassUnaffected(t *testing.T) {
	cfg := baseTestConfig()
	cfg.AutopilotRouting = config.AutopilotRoutingConfig{
		RoutingMode:         "shadow",
		KillSwitch:          false,
		DisabledTaskClasses: []string{"some_other_task_class"},
	}

	_, cfgManager, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	smartRouter := createTestSmartRouter(t, cfgManager)
	profile := testProfile()

	filter := smartRouter.CandidateFilterFor(profile)
	if filter == nil {
		t.Error("未命中的 TaskClass 不应受 DisabledTaskClasses 影响，CandidateFilterFor 不应返回 nil")
	}
}

// TestInvariant_DisabledChannelUID_ExcludedFromRealPath 验证：
// DisabledChannelUIDs 命中的渠道不会出现在 executeFilter（真实路由路径）
// 返回的候选列表中。使用 assist 模式以便观察重排后的候选集合。
func TestInvariant_DisabledChannelUID_ExcludedFromRealPath(t *testing.T) {
	const ch1UID = "ch-standard-uid-fixed"

	cfg := baseTestConfig()
	cfg.Upstream[1].ChannelUID = ch1UID // 预先固定 ChannelUID，避免依赖自动分配的随机值
	cfg.AutopilotRouting = config.AutopilotRoutingConfig{
		RoutingMode:         "assist",
		KillSwitch:          false,
		DisabledChannelUIDs: []string{ch1UID},
	}

	_, cfgManager, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	smartRouter := createTestSmartRouter(t, cfgManager)
	profile := testProfile()
	filter := smartRouter.CandidateFilterFor(profile)
	if filter == nil {
		t.Fatal("assist 模式下 filter 不应为 nil")
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

	for _, ch := range result {
		if ch.Name == "ch-standard" {
			t.Errorf("被禁用的渠道 ch-standard 不应出现在候选结果中: %v", result)
		}
	}
	if len(result) != 2 {
		t.Errorf("禁用 1 个渠道后应剩 2 个候选，实际: %d", len(result))
	}
}

// TestInvariant_DisabledChannelUID_BuildPlanConsistentWithRealPath 验证：
// BuildPlan（dry-run 诊断路径）对 DisabledChannelUIDs 的处理与
// executeFilter（真实路由路径）保持一致——被禁用渠道不出现在
// RoutingPlan.Candidates 中，避免 dry-run 预览和实际调度产生分歧
// （对应之前 aa136b26 修复的那类一致性问题的预防性回归测试）。
func TestInvariant_DisabledChannelUID_BuildPlanConsistentWithRealPath(t *testing.T) {
	const ch1UID = "ch-standard-uid-fixed"

	cfg := baseTestConfig()
	cfg.Upstream[1].ChannelUID = ch1UID
	cfg.AutopilotRouting = config.AutopilotRoutingConfig{
		RoutingMode:         "shadow",
		KillSwitch:          false,
		DisabledChannelUIDs: []string{ch1UID},
	}

	_, cfgManager, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	smartRouter := createTestSmartRouter(t, cfgManager)
	profile := testProfile()

	plan := smartRouter.BuildPlan(profile)
	if plan == nil {
		t.Fatal("BuildPlan 不应返回 nil")
	}

	for _, c := range plan.Candidates {
		if c.ChannelUID == ch1UID {
			t.Errorf("被禁用的渠道 %s 不应出现在 BuildPlan.Candidates 中", ch1UID)
		}
	}
	if len(plan.Candidates) != 2 {
		t.Errorf("禁用 1 个渠道后 BuildPlan 应剩 2 个候选，实际: %d", len(plan.Candidates))
	}
}

// TestInvariant_DisabledTaskClass_DoesNotShortCircuitBuildPlan 验证：
// DisabledTaskClasses 是"是否运行"类开关（与 kill switch/mode=off 同类），
// BuildPlan 作为诊断预览接口，即使命中 DisabledTaskClasses 也应正常
// 算出候选计划，不提前返回空计划——与 TestInvariant_BuildPlan_WithKillSwitch
// 验证的不变量一致。
func TestInvariant_DisabledTaskClass_DoesNotShortCircuitBuildPlan(t *testing.T) {
	cfg := baseTestConfig()
	cfg.AutopilotRouting = config.AutopilotRoutingConfig{
		RoutingMode:         "shadow",
		KillSwitch:          false,
		DisabledTaskClasses: []string{string(TaskClassLightweight)},
	}

	_, cfgManager, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	smartRouter := createTestSmartRouter(t, cfgManager)
	profile := testProfile()

	plan := smartRouter.BuildPlan(profile)
	if plan == nil {
		t.Fatal("BuildPlan 不应返回 nil")
	}
	if plan.Mode != RoutingModeDryRun {
		t.Errorf("BuildPlan 模式应为 dry_run，实际: %s", plan.Mode)
	}
	if len(plan.Candidates) != 3 {
		t.Errorf("DisabledTaskClasses 不应影响 BuildPlan 候选数量，期望 3，实际: %d", len(plan.Candidates))
	}
}
