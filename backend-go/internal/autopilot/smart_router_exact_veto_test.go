package autopilot

import (
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

// ── 精确模型运行期否决 → 替代模型行展开 专项测试 ──
//
// 覆盖场景：精确模型在 ModelProfileStore 有画像，但被运行期负信号
// （endpoint binding 判死 / 模型熔断）否决时，自适应意图渠道不短路、展开替代模型行。

func vetoExactProfile() ModelProfile {
	return ModelProfile{
		ChannelUID: "ch_veto", ChannelKind: "messages", MetricsKey: "m1",
		ModelID: "claude-opus-4-8", ModelFamily: ModelFamilyClaude, QualityTier: QualityTierPremium,
		ContextTokens: 1_000_000, SupportsToolCalls: true, SupportsReasoning: true, ProbeSuccess: true,
	}
}

func vetoSubstituteProfile() ModelProfile {
	return ModelProfile{
		ChannelUID: "ch_veto", ChannelKind: "messages", MetricsKey: "m2",
		ModelID: "claude-sonnet-5", ModelFamily: ModelFamilyClaude, QualityTier: QualityTierPremium,
		ContextTokens: 1_000_000, SupportsToolCalls: true, SupportsReasoning: true, ProbeSuccess: true,
	}
}

// vetoTestConfig 构建一个 AutoManaged 渠道（单 Key 单 BaseURL）与空模型画像 store。
func vetoTestConfig() (config.Config, *ModelProfileStore) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{{
			Name:            "veto-auto",
			ChannelUID:      "ch_veto",
			BaseURL:         "https://veto.example.com",
			APIKeys:         []string{"sk-veto"},
			Status:          "active",
			AutoManaged:     true,
			SupportedModels: []string{"*"},
		}},
		AutopilotRouting: config.AutopilotRoutingConfig{
			RoutingMode: "auto",
			ModelMapping: config.ModelMappingRoutingConfig{
				AutoResolve:            true,
				CapabilityFloorEnabled: true,
			},
		},
	}
	return cfg, &ModelProfileStore{cache: map[string]*ModelProfile{}, dirtyKeys: map[string]struct{}{}}
}

// upsertBindingHealth 写入指定健康状态的 endpoint binding 画像（EndpointUID 按运行时身份重建规则生成）。
func upsertBindingHealth(t *testing.T, store *ProfileStore, health HealthState) {
	t.Helper()
	p := &KeyEndpointProfile{
		EndpointUID: GenerateEndpointUID("ch_veto", "https://veto.example.com", KeyHashFromAPIKey("sk-veto")),
		ChannelUID:  "ch_veto",
		BaseURL:     "https://veto.example.com",
		HealthState: health,
	}
	if err := store.Upsert(p); err != nil {
		t.Fatalf("Upsert binding profile 失败: %v", err)
	}
}

func vetoRequestProfile(model string) *RequestProfile {
	profile := BuildRequestProfile(RequestProfileFeatures{
		Model: model, ChannelKind: "messages", Operation: "completion", EstTokens: 1000,
	})
	return &profile
}

// 精确模型画像命中但 binding 全部 dead → 否决短路，只产替代模型行。
func TestExactVetoExpandsSubstituteRows(t *testing.T) {
	cfg, modelStore := vetoTestConfig()
	upsertProfiles(t, modelStore, vetoExactProfile(), vetoSubstituteProfile())
	profileStore := newTestProfileStore(t)
	upsertBindingHealth(t, profileStore, HealthStateDead)

	cfgManager, cleanup := createTestConfigManager(t, cfg)
	defer cleanup()
	router := NewSmartRouter(profileStore, nil, nil, cfgManager)
	router.SetModelResolver(NewModelResolver(modelStore, cfgManager))

	profile := vetoRequestProfile("claude-opus-4-8")
	up := cfgManager.GetConfig().Upstream[0]
	resolutions := router.resolveChannelModels(profile, &up, cfgManager.GetConfig().UpstreamModelCapabilities)

	if len(resolutions) != 1 {
		t.Fatalf("否决后展开行数 = %d, want 1（仅替代模型）: %+v", len(resolutions), resolutions)
	}
	res := resolutions[0]
	if res.ActualModel != "claude-sonnet-5" {
		t.Errorf("替代行 ActualModel = %q, want claude-sonnet-5", res.ActualModel)
	}
	if res.MappingSource != "auto_resolve" || !strings.HasPrefix(res.MappingReason, "exact_vetoed:") {
		t.Errorf("替代行 MappingSource/Reason = %q/%q, want auto_resolve + exact_vetoed: 前缀",
			res.MappingSource, res.MappingReason)
	}
}

// 精确模型画像命中且 binding 健康 → 维持精确短路（回归保护）。
func TestExactHitHealthyBindingKeepsShortCircuit(t *testing.T) {
	cfg, modelStore := vetoTestConfig()
	upsertProfiles(t, modelStore, vetoExactProfile(), vetoSubstituteProfile())
	profileStore := newTestProfileStore(t)
	upsertBindingHealth(t, profileStore, HealthStateHealthy)

	cfgManager, cleanup := createTestConfigManager(t, cfg)
	defer cleanup()
	router := NewSmartRouter(profileStore, nil, nil, cfgManager)
	router.SetModelResolver(NewModelResolver(modelStore, cfgManager))

	profile := vetoRequestProfile("claude-opus-4-8")
	up := cfgManager.GetConfig().Upstream[0]
	resolutions := router.resolveChannelModels(profile, &up, cfgManager.GetConfig().UpstreamModelCapabilities)

	if len(resolutions) != 1 {
		t.Fatalf("健康精确命中行数 = %d, want 1: %+v", len(resolutions), resolutions)
	}
	res := resolutions[0]
	if res.ActualModel != "claude-opus-4-8" || res.MappedModel != "" {
		t.Errorf("精确短路行 = %+v, want ActualModel=claude-opus-4-8 且无 MappedModel", res)
	}
}

// 非自适应意图（exact_only）即使被否决也不展开，维持精确短路。
func TestExactVetoNonAdaptiveIntentNoExpansion(t *testing.T) {
	cfg, modelStore := vetoTestConfig()
	upsertProfiles(t, modelStore, ModelProfile{
		ChannelUID: "ch_veto", ChannelKind: "messages", MetricsKey: "m1",
		ModelID: "gpt-5.2", ModelFamily: ModelFamilyOpenAI, QualityTier: QualityTierPremium,
		ContextTokens: 1_000_000, SupportsToolCalls: true, ProbeSuccess: true,
	}, vetoSubstituteProfile())
	profileStore := newTestProfileStore(t)
	upsertBindingHealth(t, profileStore, HealthStateDead)

	cfgManager, cleanup := createTestConfigManager(t, cfg)
	defer cleanup()
	router := NewSmartRouter(profileStore, nil, nil, cfgManager)
	router.SetModelResolver(NewModelResolver(modelStore, cfgManager))

	profile := vetoRequestProfile("gpt-5.2")
	up := cfgManager.GetConfig().Upstream[0]
	resolutions := router.resolveChannelModels(profile, &up, cfgManager.GetConfig().UpstreamModelCapabilities)

	if len(resolutions) != 1 || resolutions[0].ActualModel != "gpt-5.2" {
		t.Fatalf("非自适应意图被否决时不应展开, got %+v", resolutions)
	}
}

// 模型熔断探针全部熔断 → keypool 候选为空 → 同样否决精确模型并展开替代行。
func TestExactVetoViaModelCircuitProbe(t *testing.T) {
	cfg, modelStore := vetoTestConfig()
	upsertProfiles(t, modelStore, vetoExactProfile(), vetoSubstituteProfile())
	profileStore := newTestProfileStore(t)
	upsertBindingHealth(t, profileStore, HealthStateHealthy)

	cfgManager, cleanup := createTestConfigManager(t, cfg)
	defer cleanup()
	router := NewSmartRouter(profileStore, nil, nil, cfgManager)
	router.SetModelResolver(NewModelResolver(modelStore, cfgManager))
	router.SetModelCircuitProbe(func(channelKind, channelUID, apiKey, model string) bool { return true })

	profile := vetoRequestProfile("claude-opus-4-8")
	up := cfgManager.GetConfig().Upstream[0]
	resolutions := router.resolveChannelModels(profile, &up, cfgManager.GetConfig().UpstreamModelCapabilities)

	if len(resolutions) != 1 || resolutions[0].ActualModel != "claude-sonnet-5" {
		t.Fatalf("熔断否决后展开 = %+v, want 仅 claude-sonnet-5", resolutions)
	}
}

// 单数版 resolveChannelModel：精确命中 + 被否决 → 映射到最佳非精确候选。
func TestResolveChannelModelSingularExactVeto(t *testing.T) {
	cfg, modelStore := vetoTestConfig()
	upsertProfiles(t, modelStore, vetoExactProfile(), vetoSubstituteProfile())
	profileStore := newTestProfileStore(t)
	upsertBindingHealth(t, profileStore, HealthStateDead)

	cfgManager, cleanup := createTestConfigManager(t, cfg)
	defer cleanup()
	router := NewSmartRouter(profileStore, nil, nil, cfgManager)
	router.SetModelResolver(NewModelResolver(modelStore, cfgManager))

	profile := vetoRequestProfile("claude-opus-4-8")
	up := cfgManager.GetConfig().Upstream[0]
	resolution := router.resolveChannelModel(profile, &up, cfgManager.GetConfig().UpstreamModelCapabilities)

	if !resolution.Supported {
		t.Fatalf("单数版否决后仍应 supported: %+v", resolution)
	}
	if resolution.ActualModel != "claude-sonnet-5" || resolution.MappedModel != "claude-sonnet-5" {
		t.Errorf("单数版否决映射 = %+v, want ActualModel/MappedModel=claude-sonnet-5", resolution)
	}
	if !strings.HasPrefix(resolution.MappingReason, "exact_vetoed:") {
		t.Errorf("单数版 MappingReason = %q, want exact_vetoed: 前缀", resolution.MappingReason)
	}
}

// 单数版回归保护：精确命中且健康 → 不映射。
func TestResolveChannelModelSingularHealthyExact(t *testing.T) {
	cfg, modelStore := vetoTestConfig()
	upsertProfiles(t, modelStore, vetoExactProfile(), vetoSubstituteProfile())
	profileStore := newTestProfileStore(t)
	upsertBindingHealth(t, profileStore, HealthStateHealthy)

	cfgManager, cleanup := createTestConfigManager(t, cfg)
	defer cleanup()
	router := NewSmartRouter(profileStore, nil, nil, cfgManager)
	router.SetModelResolver(NewModelResolver(modelStore, cfgManager))

	profile := vetoRequestProfile("claude-opus-4-8")
	up := cfgManager.GetConfig().Upstream[0]
	resolution := router.resolveChannelModel(profile, &up, cfgManager.GetConfig().UpstreamModelCapabilities)

	if !resolution.Supported || resolution.ActualModel != "claude-opus-4-8" || resolution.MappedModel != "" {
		t.Errorf("单数版健康精确命中 = %+v, want claude-opus-4-8 无映射", resolution)
	}
}

// 全灭路径聚合原因：dead + 模型不兼容混合时能正确分类计数。
func TestFilterKeyBindingsAllFilteredReasonSummary(t *testing.T) {
	store := newTestProfileStore(t)
	_ = store.Upsert(&KeyEndpointProfile{
		EndpointUID: GenerateEndpointUID("ch1", "https://a.com", KeyHashFromAPIKey("sk-dead")),
		ChannelUID:  "ch1", BaseURL: "https://a.com", HealthState: HealthStateDead,
	})
	_ = store.Upsert(&KeyEndpointProfile{
		EndpointUID: GenerateEndpointUID("ch1", "https://a.com", KeyHashFromAPIKey("sk-incompat")),
		ChannelUID:  "ch1", BaseURL: "https://a.com", HealthState: HealthStateHealthy,
		AvailableModels: []string{"other-model"},
	})

	deps := EndpointPolicyDeps{ProfileStore: store}
	req := &RequestProfile{Model: "m1", ChannelKind: "messages"}
	filtered, decisions := filterKeyBindingsWithDecisions(deps, req, "ch1", "https://a.com",
		[]string{"sk-dead", "sk-incompat"}, true)
	if len(filtered) != 0 {
		t.Fatalf("应全灭: got %v", filtered)
	}
	summary := summarizeKeyBindingFilterReasons(decisions, true)
	if !strings.Contains(summary, "health_ineligible=1") || !strings.Contains(summary, "model_ineligible=1") {
		t.Errorf("聚合原因 = %q, want 含 health_ineligible=1 和 model_ineligible=1", summary)
	}
}
