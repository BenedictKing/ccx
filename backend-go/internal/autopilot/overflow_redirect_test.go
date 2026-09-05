package autopilot

import (
	"context"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/scheduler"
)

// TestContextProbeEligible 验证 resolver 地板层的试探放宽准入。
func TestContextProbeEligible(t *testing.T) {
	originalDeclared := learnedDeclaredContextLimitLookup
	learnedDeclaredContextLimitLookup = func(channelUID, model string) (int, bool) {
		if channelUID == "ch_declared" {
			return 200_000, true
		}
		return 0, false
	}
	t.Cleanup(func() { learnedDeclaredContextLimitLookup = originalDeclared })

	// gpt-5.6-sol 在当前内置注册表带 [272K, 372K, 1.05M] 阶梯
	sameModel := &ModelProfile{ChannelUID: "ch_a", ChannelKind: "responses", ModelID: "gpt-5.6-sol", ContextTokens: 272_000}
	if !contextProbeEligible(sameModel, 274_081, "gpt-5.6-sol", 272_000) {
		t.Fatal("同名模型 + 阶梯覆盖 + 无收紧矛盾 → 应可试探")
	}
	if contextProbeEligible(sameModel, 1_100_000, "gpt-5.6-sol", 272_000) {
		t.Fatal("超出阶梯上限 → 不可试探")
	}
	declared := &ModelProfile{ChannelUID: "ch_declared", ChannelKind: "responses", ModelID: "gpt-5.6-sol", ContextTokens: 272_000}
	if contextProbeEligible(declared, 274_081, "gpt-5.6-sol", 272_000) {
		t.Fatal("实测收紧矛盾 → 不可试探")
	}
	other := &ModelProfile{ChannelUID: "ch_a", ChannelKind: "responses", ModelID: "other-model", ContextTokens: 272_000}
	if contextProbeEligible(other, 274_081, "gpt-5.6-sol", 272_000) {
		t.Fatal("非请求同名模型（替代模型）→ 必须真实满足窗口，不可试探")
	}
}

// TestFilterByCapabilityFloorProbeRelaxation 同名模型窗口不足时经试探放宽保留。
func TestFilterByCapabilityFloorProbeRelaxation(t *testing.T) {
	profiles := []ModelProfile{
		{ChannelUID: "ch_a", ChannelKind: "responses", ModelID: "gpt-5.6-sol", ContextTokens: 272_000, ProbeSuccess: true},
		{ChannelUID: "ch_a", ChannelKind: "responses", ModelID: "small-model", ContextTokens: 128_000, ProbeSuccess: true},
	}
	floor := CapabilityFloor{MinContextTokens: 274_081}
	eligible := filterByCapabilityFloor(profiles, floor, "gpt-5.6-sol")
	if len(eligible) != 1 || eligible[0].ModelID != "gpt-5.6-sol" {
		t.Fatalf("eligible = %v, want 仅试探保留的 gpt-5.6-sol", eligible)
	}
}

// TestOverflowRedirectCandidates 完全跨协议溢出重定向：
// 四类协议池内按「有效窗口 ≥ 输入 + 探测成功」筛选，同协议优先、质量档降序。
func TestOverflowRedirectCandidates(t *testing.T) {
	db := newTestDB(t)
	store, err := NewProfileStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewProfileStoreWithDB: %v", err)
	}
	cfgManager, cleanup := createTestConfigManager(t, config.Config{
		ResponsesUpstream: []config.UpstreamConfig{
			{ChannelUID: "ch_resp", Name: "resp", BaseURL: "https://resp.example.com", APIKeys: []string{"sk-x"}, Status: "active"},
		},
		ChatUpstream: []config.UpstreamConfig{
			{ChannelUID: "ch_chat", Name: "chat", BaseURL: "https://chat.example.com", APIKeys: []string{"sk-y"}, Status: "active"},
		},
	})
	t.Cleanup(cleanup)

	mgr, err := NewManager(store, NewMetricsAdapterManager(nil), cfgManager, ManagerConfig{QuietLogs: true})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	// 标记活跃 binding（生产环境由 endpoint 画像 reconcile 维护）
	bindings := map[string]struct{}{
		modelProfileBindingKey("ch_resp", "responses", "mk"): {},
		modelProfileBindingKey("ch_chat", "chat", "mk"):      {},
	}
	mgr.modelProfileStore.ReplaceActiveBindings(bindings)

	now := time.Now()
	seed := []*ModelProfile{
		{ChannelUID: "ch_resp", ChannelID: 0, ChannelKind: "responses", ServiceType: "responses", MetricsKey: "mk", ModelID: "gpt-5.6-sol", ContextTokens: 272_000, QualityTier: QualityTierPremium, ProbeSuccess: true, UpdatedAt: now},
		{ChannelUID: "ch_resp", ChannelID: 0, ChannelKind: "responses", ServiceType: "responses", MetricsKey: "mk", ModelID: "resp-native-big", ContextTokens: 900_000, QualityTier: QualityTierHigh, ProbeSuccess: true, UpdatedAt: now},
		{ChannelUID: "ch_chat", ChannelID: 0, ChannelKind: "chat", ServiceType: "openai", MetricsKey: "mk", ModelID: "chat-premium-big", ContextTokens: 1_000_000, QualityTier: QualityTierPremium, ProbeSuccess: true, UpdatedAt: now},
		{ChannelUID: "ch_chat", ChannelID: 0, ChannelKind: "chat", ServiceType: "openai", MetricsKey: "mk", ModelID: "small-chat", ContextTokens: 128_000, QualityTier: QualityTierPremium, ProbeSuccess: true, UpdatedAt: now},
		{ChannelUID: "ch_chat", ChannelID: 0, ChannelKind: "chat", ServiceType: "openai", MetricsKey: "mk", ModelID: "unprobed-big", ContextTokens: 2_000_000, QualityTier: QualityTierPremium, UpdatedAt: now},
	}
	for _, p := range seed {
		if err := mgr.modelProfileStore.Upsert(p); err != nil {
			t.Fatalf("seed %s: %v", p.ModelID, err)
		}
	}

	candidates := mgr.OverflowRedirectCandidates(context.Background(), scheduler.ChannelKindResponses, "gpt-5.6-sol", 600_000)
	if len(candidates) != 2 {
		t.Fatalf("candidates = %v, want [chat-premium-big(resp 外唯二可承载)]", candidates)
	}
	// 同协议优先：resp-native-big（high, 900K ≥ 600K）排在 chat-premium-big（premium）之前
	if candidates[0].ActualModel != "resp-native-big" || candidates[0].Route.Kind != "responses" {
		t.Fatalf("首位候选 = %+v, want resp-native-big(responses)", candidates[0])
	}
	if candidates[1].ActualModel != "chat-premium-big" || candidates[1].Route.Kind != "chat" {
		t.Fatalf("次位候选 = %+v, want chat-premium-big(chat 跨协议)", candidates[1])
	}
	if !candidates[0].OverflowRedirect || candidates[0].Priority != 0 || candidates[1].Priority != 1 {
		t.Fatalf("注入候选应带 OverflowRedirect 标记并按名次排优先级: %+v", candidates)
	}

	// 900K：仅 chat-premium-big 可承载 → 跨协议候选
	candidates = mgr.OverflowRedirectCandidates(context.Background(), scheduler.ChannelKindResponses, "gpt-5.6-sol", 950_000)
	if len(candidates) != 1 || candidates[0].ActualModel != "chat-premium-big" {
		t.Fatalf("candidates = %v, want 仅 chat-premium-big", candidates)
	}

	// 1.5M：全池无人能承载 → 无重定向
	if got := mgr.OverflowRedirectCandidates(context.Background(), scheduler.ChannelKindResponses, "gpt-5.6-sol", 1_500_000); len(got) != 0 {
		t.Fatalf("超出全池窗口应返回空, got %v", got)
	}
}

// TestOverflowRedirectCandidatesMultiChannelSameModel 验证按物理候选去重（O2）：
// 同一模型在多个物理渠道上各保留一条——第一个渠道窗口不足时第二个仍可入选，
// 两个都可用时同时保留，最终由 fanout 上限裁剪。
func TestOverflowRedirectCandidatesMultiChannelSameModel(t *testing.T) {
	db := newTestDB(t)
	store, err := NewProfileStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewProfileStoreWithDB: %v", err)
	}
	cfgManager, cleanup := createTestConfigManager(t, config.Config{
		ResponsesUpstream: []config.UpstreamConfig{
			{ChannelUID: "ch_resp_small", Name: "resp-small", BaseURL: "https://small.example.com", APIKeys: []string{"sk-x"}, Status: "active"},
			{ChannelUID: "ch_resp_big", Name: "resp-big", BaseURL: "https://big.example.com", APIKeys: []string{"sk-y"}, Status: "active"},
		},
	})
	t.Cleanup(cleanup)

	mgr, err := NewManager(store, NewMetricsAdapterManager(nil), cfgManager, ManagerConfig{QuietLogs: true})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	bindings := map[string]struct{}{
		modelProfileBindingKey("ch_resp_small", "responses", "mk"): {},
		modelProfileBindingKey("ch_resp_big", "responses", "mk"):   {},
	}
	mgr.modelProfileStore.ReplaceActiveBindings(bindings)

	now := time.Now()
	seed := []*ModelProfile{
		{ChannelUID: "ch_resp_small", ChannelID: 0, ChannelKind: "responses", ServiceType: "responses", MetricsKey: "mk", ModelID: "big-model", ContextTokens: 128_000, QualityTier: QualityTierPremium, ProbeSuccess: true, UpdatedAt: now},
		{ChannelUID: "ch_resp_big", ChannelID: 1, ChannelKind: "responses", ServiceType: "responses", MetricsKey: "mk", ModelID: "big-model", ContextTokens: 900_000, QualityTier: QualityTierPremium, ProbeSuccess: true, UpdatedAt: now},
	}
	for _, p := range seed {
		if err := mgr.modelProfileStore.Upsert(p); err != nil {
			t.Fatalf("seed %s: %v", p.ModelID, err)
		}
	}

	// 600K：第一个渠道（128K）装不下，第二个（900K）仍须入选——
	// 旧实现按 kind|model 去重且在窗口检查前写 seen，谁先遍历谁占坑，
	// 第一个渠道的同名模型会把第二个渠道挡在门外。
	candidates := mgr.OverflowRedirectCandidates(context.Background(), scheduler.ChannelKindResponses, "gpt-5.6-sol", 600_000)
	if len(candidates) != 1 || candidates[0].Route.ChannelUID != "ch_resp_big" {
		t.Fatalf("candidates = %+v, want 仅 ch_resp_big 的 big-model", candidates)
	}

	// 100K：两个渠道都装得下 → 两条物理候选都保留（可用性独立，failover 兜底）
	candidates = mgr.OverflowRedirectCandidates(context.Background(), scheduler.ChannelKindResponses, "gpt-5.6-sol", 100_000)
	if len(candidates) != 2 {
		t.Fatalf("candidates = %+v, want 同模型的两个物理渠道都保留", candidates)
	}
}

// TestOverflowRedirectCandidatesProtocolProfileMismatch 验证协议画像匹配：
// 候选的物理路由来自配置协议池，画像协议必须与路由协议一致——渠道仅在
// responses 池注册时，其 chat 画像不得作为 responses 路由的候选（窗口口径
// 与执行协议错配）；渠道同时注册在 chat 池时，chat 画像经 chat 路由入选。
func TestOverflowRedirectCandidatesProtocolProfileMismatch(t *testing.T) {
	newManagerWith := func(t *testing.T, withChat bool) *Manager {
		t.Helper()
		db := newTestDB(t)
		store, err := NewProfileStoreWithDB(db)
		if err != nil {
			t.Fatalf("NewProfileStoreWithDB: %v", err)
		}
		cfg := config.Config{
			ResponsesUpstream: []config.UpstreamConfig{
				{ChannelUID: "ch_multi", Name: "multi", BaseURL: "https://multi.example.com", APIKeys: []string{"sk-x"}, Status: "active"},
			},
		}
		if withChat {
			cfg.ChatUpstream = []config.UpstreamConfig{
				{ChannelUID: "ch_multi", Name: "multi", BaseURL: "https://multi.example.com", APIKeys: []string{"sk-x"}, Status: "active", ServiceType: "openai"},
			}
		}
		cfgManager, cleanup := createTestConfigManager(t, cfg)
		t.Cleanup(cleanup)

		mgr, err := NewManager(store, NewMetricsAdapterManager(nil), cfgManager, ManagerConfig{QuietLogs: true})
		if err != nil {
			t.Fatalf("NewManager: %v", err)
		}
		bindings := map[string]struct{}{
			modelProfileBindingKey("ch_multi", "responses", "mk"): {},
			modelProfileBindingKey("ch_multi", "chat", "mk"):      {},
		}
		mgr.modelProfileStore.ReplaceActiveBindings(bindings)
		now := time.Now()
		if err := mgr.modelProfileStore.Upsert(&ModelProfile{
			ChannelUID: "ch_multi", ChannelID: 0, ChannelKind: "chat", ServiceType: "openai", MetricsKey: "mk",
			ModelID: "chat-only-model", ContextTokens: 900_000, QualityTier: QualityTierPremium, ProbeSuccess: true, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
		return mgr
	}

	// 渠道同时注册在 chat 池：chat 画像经 chat 路由成为跨协议候选
	mgr := newManagerWith(t, true)
	candidates := mgr.OverflowRedirectCandidates(context.Background(), scheduler.ChannelKindResponses, "gpt-5.6-sol", 600_000)
	if len(candidates) != 1 || candidates[0].Route.Kind != "chat" || candidates[0].ActualModel != "chat-only-model" {
		t.Fatalf("candidates = %+v, want chat 路由的 chat-only-model", candidates)
	}

	// 渠道仅在 responses 池注册：chat 画像不得作为 responses 路由的候选（协议错配）
	mgr = newManagerWith(t, false)
	if candidates := mgr.OverflowRedirectCandidates(context.Background(), scheduler.ChannelKindResponses, "gpt-5.6-sol", 600_000); len(candidates) != 0 {
		t.Fatalf("协议错配的画像不应成为候选, got %+v", candidates)
	}

	// 同名模型排除：请求模型 == 画像模型时跳过（即便协议不同）
	mgr = newManagerWith(t, true)
	if candidates := mgr.OverflowRedirectCandidates(context.Background(), scheduler.ChannelKindResponses, "chat-only-model", 600_000); len(candidates) != 0 {
		t.Fatalf("同名模型不应成为重定向目标, got %+v", candidates)
	}
}

// TestOverflowRedirectCandidatesDeterministicOrder 验证重复运行结果顺序一致（O2）：
// 同档同名的候选按路由身份排序，不受 map 遍历顺序影响。
func TestOverflowRedirectCandidatesDeterministicOrder(t *testing.T) {
	db := newTestDB(t)
	store, err := NewProfileStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewProfileStoreWithDB: %v", err)
	}
	cfgManager, cleanup := createTestConfigManager(t, config.Config{
		ResponsesUpstream: []config.UpstreamConfig{
			{ChannelUID: "ch_a", Name: "same-name", BaseURL: "https://a.example.com", APIKeys: []string{"sk-a"}, Status: "active"},
			{ChannelUID: "ch_b", Name: "same-name", BaseURL: "https://b.example.com", APIKeys: []string{"sk-b"}, Status: "active"},
			{ChannelUID: "ch_c", Name: "same-name", BaseURL: "https://c.example.com", APIKeys: []string{"sk-c"}, Status: "active"},
			{ChannelUID: "ch_d", Name: "same-name", BaseURL: "https://d.example.com", APIKeys: []string{"sk-d"}, Status: "active"},
		},
	})
	t.Cleanup(cleanup)

	mgr, err := NewManager(store, NewMetricsAdapterManager(nil), cfgManager, ManagerConfig{QuietLogs: true})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	bindings := map[string]struct{}{
		modelProfileBindingKey("ch_a", "responses", "mk"): {},
		modelProfileBindingKey("ch_b", "responses", "mk"): {},
		modelProfileBindingKey("ch_c", "responses", "mk"): {},
		modelProfileBindingKey("ch_d", "responses", "mk"): {},
	}
	mgr.modelProfileStore.ReplaceActiveBindings(bindings)

	now := time.Now()
	for _, uid := range []string{"ch_a", "ch_b", "ch_c", "ch_d"} {
		if err := mgr.modelProfileStore.Upsert(&ModelProfile{
			ChannelUID: uid, ChannelID: 0, ChannelKind: "responses", ServiceType: "responses", MetricsKey: "mk",
			ModelID: "model-" + uid, ContextTokens: 900_000, QualityTier: QualityTierHigh, ProbeSuccess: true, UpdatedAt: now,
		}); err != nil {
			t.Fatalf("seed %s: %v", uid, err)
		}
	}

	var first []scheduler.ChannelInfo
	for run := 0; run < 10; run++ {
		candidates := mgr.OverflowRedirectCandidates(context.Background(), scheduler.ChannelKindResponses, "gpt-5.6-sol", 600_000)
		if len(candidates) != 4 {
			t.Fatalf("run %d: candidates = %d, want 4", run, len(candidates))
		}
		if run == 0 {
			first = candidates
			continue
		}
		for i := range candidates {
			if candidates[i].Route.ChannelUID != first[i].Route.ChannelUID {
				t.Fatalf("run %d: 候选顺序不稳定: [%d]=%s, want %s（首轮顺序 %+v）",
					run, i, candidates[i].Route.ChannelUID, first[i].Route.ChannelUID, first)
			}
		}
	}
}
