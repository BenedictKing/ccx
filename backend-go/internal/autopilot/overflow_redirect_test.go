package autopilot

import (
	"context"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
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

// TestOverflowRedirectModel 全池按质量档选择可承载的替代模型。
func TestOverflowRedirectModel(t *testing.T) {
	db := newTestDB(t)
	store, err := NewProfileStoreWithDB(db)
	if err != nil {
		t.Fatalf("NewProfileStoreWithDB: %v", err)
	}
	cfgManager, cleanup := createTestConfigManager(t, config.Config{
		ResponsesUpstream: []config.UpstreamConfig{
			{ChannelUID: "ch_big", Name: "big", BaseURL: "https://big.example.com", APIKeys: []string{"sk-x"}, Status: "active"},
		},
	})
	t.Cleanup(cleanup)

	mgr, err := NewManager(store, NewMetricsAdapterManager(nil), cfgManager, ManagerConfig{QuietLogs: true})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	now := time.Now()
	seed := []*ModelProfile{
		{ChannelUID: "ch_big", ChannelID: 0, ChannelKind: "responses", ServiceType: "responses", MetricsKey: "mk", ModelID: "gpt-5.6-sol", ContextTokens: 272_000, QualityTier: QualityTierPremium, ProbeSuccess: true, UpdatedAt: now},
		{ChannelUID: "ch_big", ChannelID: 0, ChannelKind: "responses", ServiceType: "responses", MetricsKey: "mk", ModelID: "gpt-5.5", ContextTokens: 1_050_000, QualityTier: QualityTierHigh, ProbeSuccess: true, UpdatedAt: now},
		{ChannelUID: "ch_big", ChannelID: 0, ChannelKind: "responses", ServiceType: "responses", MetricsKey: "mk", ModelID: "gemini-3.8-pro", ContextTokens: 1_000_000, QualityTier: QualityTierPremium, ProbeSuccess: true, UpdatedAt: now},
		{ChannelUID: "ch_big", ChannelID: 0, ChannelKind: "responses", ServiceType: "responses", MetricsKey: "mk", ModelID: "small-chat", ContextTokens: 128_000, QualityTier: QualityTierPremium, ProbeSuccess: true, UpdatedAt: now},
		{ChannelUID: "ch_big", ChannelID: 0, ChannelKind: "responses", ServiceType: "responses", MetricsKey: "mk", ModelID: "unprobed-big", ContextTokens: 2_000_000, QualityTier: QualityTierPremium, UpdatedAt: now},
	}
	// 标记 ch_big|responses|mk 为活跃 binding（生产环境由 endpoint 画像 reconcile 维护）
	mgr.modelProfileStore.ReplaceActiveBindings(map[string]struct{}{
		modelProfileBindingKey("ch_big", "responses", "mk"): {},
	})

	for _, p := range seed {
		if err := mgr.modelProfileStore.Upsert(p); err != nil {
			t.Fatalf("seed %s: %v", p.ModelID, err)
		}
	}

	// 274K 输入：窗口足够的候选里 gemini-3.8-pro（premium）优于 gpt-5.5（high）；
	// small-chat 窗口不足、unprobed-big 未探测、gpt-5.6-sol 是请求模型本身，均排除。
	target, ok := mgr.OverflowRedirectModel(context.Background(), "responses", "gpt-5.6-sol", 274_081)
	if !ok {
		t.Fatal("应找到重定向目标")
	}
	if target != "gemini-3.8-pro" {
		t.Fatalf("target = %q, want gemini-3.8-pro（最高质量档且窗口足够）", target)
	}

	// 1.2M 输入：无人能承载 → 无重定向
	if _, ok := mgr.OverflowRedirectModel(context.Background(), "responses", "gpt-5.6-sol", 1_200_000); ok {
		t.Fatal("超出全池窗口应返回无重定向")
	}
}
