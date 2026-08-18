package autopilot

import (
	"math"
	"path/filepath"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/errutil"
	"github.com/BenedictKing/ccx/internal/scheduler"
)

func TestBuildChannelEntryUsesRegistryCapabilities(t *testing.T) {
	tests := []struct {
		name          string
		model         string
		upstream      config.UpstreamConfig
		wantVision    bool
		wantTools     bool
		wantReasoning bool
	}{
		{
			name: "多模态模型使用内置能力", model: "mimo-v2.5",
			wantVision: true, wantTools: true, wantReasoning: true,
		},
		{
			name: "文本模型不会误报视觉", model: "mimo-v2.5-pro",
			wantTools: true, wantReasoning: true,
		},
		{
			name: "NoVision 强制覆盖注册表", model: "mimo-v2.5",
			upstream:  config.UpstreamConfig{NoVision: true},
			wantTools: true, wantReasoning: true,
		},
		{
			name: "映射后的 NoVisionModels 强制覆盖注册表", model: "alias-model",
			upstream: config.UpstreamConfig{
				ModelMapping:   map[string]string{"alias-model": "mimo-v2.5"},
				NoVisionModels: []string{"mimo-v2.5"},
			},
			wantTools: true, wantReasoning: true,
		},
		{
			name: "渠道能力覆盖可提供正向能力", model: "custom-model",
			upstream: config.UpstreamConfig{ModelCapabilities: map[string]config.UpstreamModelCapability{
				"custom-model": {
					ThinkingMode: "thinking",
					Capabilities: map[string]bool{"vision": true, "toolCalls": true},
				},
			}},
			wantVision: true, wantTools: true, wantReasoning: true,
		},
	}

	router := NewSmartRouter(nil, nil, nil, nil)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := tt.upstream
			upstream.ChannelUID = "ch_registry"
			entry := router.buildChannelEntry(
				scheduler.ChannelInfo{Index: 0, Name: "registry", Status: "active"},
				&upstream, "messages", tt.model, nil,
			)
			if entry.SupportsVision != tt.wantVision || entry.SupportsToolCalls != tt.wantTools ||
				entry.SupportsReasoning != tt.wantReasoning {
				t.Fatalf("capabilities vision=%v tools=%v reasoning=%v, want %v/%v/%v",
					entry.SupportsVision, entry.SupportsToolCalls, entry.SupportsReasoning,
					tt.wantVision, tt.wantTools, tt.wantReasoning)
			}
		})
	}
}

func TestBuildChannelEntryUsesMappedModelQualityTier(t *testing.T) {
	modelStore := newModelPreviewStore(t,
		ModelProfile{
			ChannelUID: "ch_kimi", ChannelKind: "messages", MetricsKey: "k3-endpoint",
			ModelID: "k3", ModelFamily: ModelFamilyUnknown, QualityTier: QualityTierLow,
			Source:       "auto_discovery",
			ProbeSuccess: true,
		},
		ModelProfile{
			ChannelUID: "ch_kimi", ChannelKind: "messages", MetricsKey: "coding-endpoint",
			ModelID: "kimi-for-coding", ModelFamily: ModelFamilyKimi, QualityTier: QualityTierNormal,
			Source:       "auto_discovery",
			ProbeSuccess: true,
		},
	)
	router := NewSmartRouter(nil, nil, nil, nil)
	router.SetModelProfileStore(modelStore)
	upstream := &config.UpstreamConfig{ChannelUID: "ch_kimi"}
	channel := scheduler.ChannelInfo{Index: 0, Name: "kimi", Status: "active"}

	k3Entry := router.buildChannelEntry(channel, upstream, "messages", "k3", nil)
	codingEntry := router.buildChannelEntry(channel, upstream, "messages", "kimi-for-coding", nil)
	if k3Entry.ScoringCandidate.QualityTier != QualityTierPremium {
		t.Fatalf("K3 quality tier = %q, want premium", k3Entry.ScoringCandidate.QualityTier)
	}
	if codingEntry.ScoringCandidate.QualityTier != QualityTierNormal {
		t.Fatalf("kimi-for-coding quality tier = %q, want normal", codingEntry.ScoringCandidate.QualityTier)
	}
}

func TestBuildChannelEntryAppliesProviderTimePricingAfterActivation(t *testing.T) {
	manager, cleanup := createTestConfigManager(t, config.Config{AutopilotRouting: config.DefaultAutopilotRoutingConfig()})
	defer cleanup()
	router := NewSmartRouter(nil, nil, nil, manager)
	upstream := &config.UpstreamConfig{ChannelUID: "ch_deepseek", ProviderID: "deepseek"}
	channel := scheduler.ChannelInfo{Index: 0, Name: "deepseek", Status: "active"}

	router.now = func() time.Time {
		return time.Date(2026, 7, 19, 10, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	}
	before := router.buildChannelEntry(channel, upstream, "messages", "deepseek-v4-pro", nil)
	router.now = func() time.Time {
		return time.Date(2026, 7, 20, 10, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	}
	peak := router.buildChannelEntry(channel, upstream, "messages", "deepseek-v4-pro", nil)
	if before.EstimatedCost <= 0 || math.Abs(peak.EstimatedCost-before.EstimatedCost*2) > 1e-9 {
		t.Fatalf("estimated cost before=%v peak=%v", before.EstimatedCost, peak.EstimatedCost)
	}

	upstream.ProviderID = ""
	upstream.BaseURL = "https://api.deepseek.com/anthropic"
	manualOfficial := router.buildChannelEntry(channel, upstream, "messages", "deepseek-v4-pro", nil)
	if math.Abs(manualOfficial.EstimatedCost-peak.EstimatedCost) > 1e-9 {
		t.Fatalf("手动官方 DeepSeek 渠道未应用峰值倍率: managed=%v manual=%v", peak.EstimatedCost, manualOfficial.EstimatedCost)
	}
	upstream.BaseURL = "https://relay.example/v1"
	relay := router.buildChannelEntry(channel, upstream, "messages", "deepseek-v4-pro", nil)
	if math.Abs(relay.EstimatedCost-before.EstimatedCost) > 1e-9 {
		t.Fatalf("第三方 relay 不应应用 DeepSeek 官方倍率: before=%v relay=%v", before.EstimatedCost, relay.EstimatedCost)
	}
}

func TestBuildChannelEntryGroupMultiplierFallback(t *testing.T) {
	manager, cleanup := createTestConfigManager(t, config.Config{AutopilotRouting: config.DefaultAutopilotRoutingConfig()})
	defer cleanup()
	router := NewSmartRouter(nil, nil, nil, manager)

	gm := 1.8
	upstream := &config.UpstreamConfig{
		ChannelUID: "ch_newapi",
		ProviderID: "openai",
		APIKeys:    []string{"sk-test"},
		APIKeyConfigs: []config.APIKeyConfig{{
			Key:                "sk-test",
			GroupMultiplier:    &gm,
			MaxGroupMultiplier: &gm,
		}},
	}
	channel := scheduler.ChannelInfo{Index: 0, Name: "newapi", Status: "active"}

	// 使用当前 embedded registry 中实际存在的 OpenAI 模型（gpt-4o 已移除）。
	modelID := "gpt-5.2"
	entry := router.buildChannelEntry(channel, upstream, "messages", modelID, nil)
	if entry.EstimatedCost <= 0 {
		t.Fatalf("estimated cost 应大于 0，got=%v", entry.EstimatedCost)
	}

	// 构造一个无 APIKeyConfig 的 upstream，验证 groupMultiplier=1 的默认行为。
	upstreamNoMultiplier := &config.UpstreamConfig{
		ChannelUID: "ch_newapi_no_mult",
		ProviderID: "openai",
		APIKeys:    []string{"sk-test2"},
	}
	entryNoMult := router.buildChannelEntry(channel, upstreamNoMultiplier, "messages", modelID, nil)
	if entryNoMult.EstimatedCost <= 0 {
		t.Fatalf("无 groupMultiplier 时 estimated cost 应大于 0，got=%v", entryNoMult.EstimatedCost)
	}
	if entry.EstimatedCost <= entryNoMult.EstimatedCost {
		t.Fatalf("groupMultiplier=1.8 的 estimated cost(%v) 应大于无倍率的(%v)", entry.EstimatedCost, entryNoMult.EstimatedCost)
	}

	// 精确校验：标价 × 分组倍率 = 计算值。
	want := entryNoMult.EstimatedCost * gm
	if math.Abs(entry.EstimatedCost-want) > 1e-9 {
		t.Fatalf("groupMultiplier=1.8 的 estimated cost=%v，want=%v (base=%v × %v)", entry.EstimatedCost, want, entryNoMult.EstimatedCost, gm)
	}
}

func TestSmartRouterAppliesCanonicalBenchmarkToDomainScore(t *testing.T) {
	// 期望值由当前 registry 推导，避免评测数据刷新导致断言失效。
	want := canonicalDomainCeiling(t, "gpt-5.6-sol", TaskDomainReasoning)
	router := NewSmartRouter(nil, nil, nil, nil)
	entry := router.buildChannelEntry(
		scheduler.ChannelInfo{Index: 0, Name: "sol", Status: "active"},
		&config.UpstreamConfig{ChannelUID: "ch_sol"}, "responses", "gpt-5.6-sol", nil,
	)
	applyDomainStrength(&entry, TaskDomainReasoning)

	if math.Abs(entry.ScoringCandidate.DomainStrengthScore-want) > 1e-9 {
		t.Fatalf("DomainStrengthScore = %v, want %v", entry.ScoringCandidate.DomainStrengthScore, want)
	}
	if entry.ScoringCandidate.DomainEvidence == nil ||
		entry.ScoringCandidate.DomainEvidence.Source != "canonical_benchmark" ||
		entry.ScoringCandidate.DomainEvidence.CanonicalModel != "gpt-5.6-sol" {
		t.Fatalf("DomainEvidence = %+v", entry.ScoringCandidate.DomainEvidence)
	}

	scored := ScoreCandidate(entry.ScoringCandidate, ScoringContext{Weights: DefaultTaskWeights()[TaskClassWorker]})
	if scored.DomainEvidence == nil || math.Abs(scored.DomainEvidence.CanonicalCeiling-want) > 1e-9 {
		t.Fatalf("scored DomainEvidence = %+v", scored.DomainEvidence)
	}
}

func TestSmartRouterPrefersEndpointDomainOverrideAndAppliesProviderFactor(t *testing.T) {
	profile := &ModelProfile{
		ChannelUID:                "ch_sol",
		ChannelKind:               "responses",
		MetricsKey:                "endpoint-a",
		ModelID:                   "gpt-5.6-sol",
		ModelFamily:               ModelFamilyOpenAI,
		ProviderQualityScore:      0.8,
		ProviderQualityConfidence: 0.75,
	}
	store := &ModelProfileStore{cache: map[string]*ModelProfile{"sol": profile}}
	router := NewSmartRouter(nil, nil, nil, nil)
	router.SetModelProfileStore(store)

	entry := router.buildChannelEntry(
		scheduler.ChannelInfo{Index: 0, Name: "sol", Status: "active"},
		&config.UpstreamConfig{ChannelUID: "ch_sol"}, "responses", "gpt-5.6-sol", nil,
	)
	applyDomainStrength(&entry, TaskDomainReasoning)
	// factor = 1 - 0.75 * (1 - 0.8) = 0.85；上界随 registry 刷新动态推导。
	if want := canonicalDomainCeiling(t, "gpt-5.6-sol", TaskDomainReasoning) * 0.85; math.Abs(entry.ScoringCandidate.DomainStrengthScore-want) > 1e-9 {
		t.Fatalf("quality-adjusted DomainStrengthScore = %v, want %v", entry.ScoringCandidate.DomainStrengthScore, want)
	}

	profile.TaskDomainStrengths = map[TaskDomain]float64{TaskDomainReasoning: 0.97}
	entry = router.buildChannelEntry(
		scheduler.ChannelInfo{Index: 0, Name: "sol", Status: "active"},
		&config.UpstreamConfig{ChannelUID: "ch_sol"}, "responses", "gpt-5.6-sol", nil,
	)
	applyDomainStrength(&entry, TaskDomainReasoning)
	if entry.ScoringCandidate.DomainStrengthScore != 0.97 ||
		entry.ScoringCandidate.DomainEvidence.Source != "endpoint_override" {
		t.Fatalf("endpoint override evidence = %+v", entry.ScoringCandidate.DomainEvidence)
	}
}

func TestBuildChannelEntryMergesRegistryAndEndpointCapabilities(t *testing.T) {
	store, err := NewProfileStore(filepath.Join(t.TempDir(), "profiles.db"))
	if err != nil {
		t.Fatalf("创建 ProfileStore 失败: %v", err)
	}
	defer errutil.IgnoreDeferred(store.Close)

	profiles := []*KeyEndpointProfile{
		{
			EndpointUID: "ep_registry", ChannelUID: "ch_registry", ChannelKind: "messages",
			HealthState: HealthStateUnknown, QualityTier: QualityTierNormal,
			StabilityTier: StabilityTierNormal, SpeedTier: SpeedTierNormal, CostTier: CostTierNormal,
		},
		{
			EndpointUID: "ep_profile", ChannelUID: "ch_profile", ChannelKind: "messages",
			HealthState: HealthStateUnknown, QualityTier: QualityTierNormal,
			StabilityTier: StabilityTierNormal, SpeedTier: SpeedTierNormal, CostTier: CostTierNormal,
			SupportsVision: true, SupportsToolCalls: true, SupportsReasoning: true,
		},
	}
	for _, profile := range profiles {
		if err := store.Upsert(profile); err != nil {
			t.Fatalf("写入 profile 失败: %v", err)
		}
	}

	router := NewSmartRouter(store, nil, nil, nil)
	registryEntry := router.buildChannelEntry(
		scheduler.ChannelInfo{Index: 0, Name: "registry", Status: "active"},
		&config.UpstreamConfig{ChannelUID: "ch_registry"}, "messages", "glm-5.2", nil,
	)
	if !registryEntry.SupportsToolCalls || !registryEntry.SupportsReasoning {
		t.Fatalf("空画像不应抹掉注册表能力: %+v", registryEntry)
	}
	if reasons := routingHardConstraintReasons(&RequestProfile{ToolUseNeed: true, ReasoningNeed: true}, &registryEntry); len(reasons) != 0 {
		t.Fatalf("注册表已知能力不应触发硬约束: %v", reasons)
	}

	profileEntry := router.buildChannelEntry(
		scheduler.ChannelInfo{Index: 1, Name: "profile", Status: "active"},
		&config.UpstreamConfig{ChannelUID: "ch_profile"}, "messages", "unknown-model", nil,
	)
	if !profileEntry.SupportsVision || !profileEntry.SupportsToolCalls || !profileEntry.SupportsReasoning {
		t.Fatalf("画像正向能力未合并: %+v", profileEntry)
	}
}
