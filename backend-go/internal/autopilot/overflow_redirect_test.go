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
