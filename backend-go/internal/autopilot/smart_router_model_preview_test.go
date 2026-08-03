package autopilot

import (
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/scheduler"
)

func newModelPreviewStore(t *testing.T, profiles ...ModelProfile) *ModelProfileStore {
	t.Helper()
	store := &ModelProfileStore{
		cache:     make(map[string]*ModelProfile),
		dirtyKeys: make(map[string]struct{}),
	}
	for i := range profiles {
		profile := profiles[i]
		if err := store.Upsert(&profile); err != nil {
			t.Fatalf("ModelProfileStore.Upsert() error = %v", err)
		}
	}
	return store
}

func modelPreviewConfig(mode string) config.Config {
	return config.Config{
		Upstream: []config.UpstreamConfig{{
			Name:            "glm-auto",
			ChannelUID:      "ch_glm_auto",
			BaseURL:         "https://glm.example.com",
			APIKeys:         []string{"sk-glm"},
			Status:          "active",
			AutoManaged:     true,
			SupportedModels: []string{"glm-5.2"},
		}},
		AutopilotRouting: config.AutopilotRoutingConfig{
			RoutingMode: mode,
			ModelMapping: config.ModelMappingRoutingConfig{
				AutoResolve:            true,
				CapabilityFloorEnabled: true,
			},
		},
	}
}

func glmPreviewProfile() ModelProfile {
	return ModelProfile{
		ChannelUID: "ch_glm_auto", ChannelKind: "messages", MetricsKey: "metrics_glm",
		ModelID: "glm-5.2", ModelFamily: ModelFamilyGLM, QualityTier: QualityTierHigh,
		ContextTokens: 1_048_576, SupportsToolCalls: true, SupportsReasoning: true,
		ProbeSuccess: true,
	}
}

func TestBuildPlanAutoManagedIgnoresStaleExplicitMapping(t *testing.T) {
	cfg := modelPreviewConfig("shadow")
	cfg.Upstream[0].SupportedModels = nil
	cfg.Upstream[0].ModelMapping = map[string]string{"claude-sonnet-5": "legacy-manual-target"}
	cfg.Upstream[0].ReasoningMapping = map[string]string{"claude-sonnet-5": "high"}
	cfgManager, cleanup := createTestConfigManager(t, cfg)
	defer cleanup()
	store := newModelPreviewStore(t, glmPreviewProfile())
	resolver := NewModelResolver(store, cfgManager)
	router := NewSmartRouter(nil, nil, nil, cfgManager)
	router.SetModelResolver(resolver)

	profile := BuildRequestProfile(RequestProfileFeatures{
		Model: "claude-sonnet-5", ChannelKind: "messages", Operation: "completion",
		EstTokens: 20_000, ToolUseNeed: true, ReasoningNeed: true,
	})
	plan := router.BuildPlan(&profile)
	if len(plan.Candidates) != 1 {
		t.Fatalf("candidate count = %d, want 1: %+v", len(plan.Candidates), plan.Candidates)
	}
	candidate := plan.Candidates[0]
	if candidate.MappedModel != "glm-5.2" || candidate.MappingSource != "auto_resolve_preview" {
		t.Fatalf("candidate mapping = %+v, want auto-resolved glm-5.2", candidate)
	}
	if candidate.MappedModel == "legacy-manual-target" {
		t.Fatalf("stale explicit mapping should not win for auto-managed channel: %+v", candidate)
	}
	if plan.SelectedModel != "glm-5.2" {
		t.Fatalf("selected model = %q, want glm-5.2", plan.SelectedModel)
	}
}

func TestBuildPlanPremiumBenchmarkTieBreak(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{Name: "gpt54-channel", ChannelUID: "ch_gpt54", BaseURL: "https://example.com", APIKeys: []string{"sk-54"}, Status: "active"},
			{Name: "gpt56sol-channel", ChannelUID: "ch_gpt56sol", BaseURL: "https://example.com", APIKeys: []string{"sk-56"}, Status: "active"},
		},
		AutopilotRouting: config.AutopilotRoutingConfig{RoutingMode: "shadow"},
	}
	cfgManager, cleanup := createTestConfigManager(t, cfg)
	defer cleanup()
	router := NewSmartRouter(nil, nil, nil, cfgManager)

	profile := BuildRequestProfile(RequestProfileFeatures{Model: "claude-fable-5", ChannelKind: "messages", Operation: "completion", EstTokens: 20_000})
	entries := []channelScoreEntry{
		router.buildChannelEntry(scheduler.ChannelInfo{Index: 0, Name: "gpt54-channel"}, &cfg.Upstream[0], "messages", "gpt-5.4", nil),
		router.buildChannelEntry(scheduler.ChannelInfo{Index: 1, Name: "gpt56sol-channel"}, &cfg.Upstream[1], "messages", "gpt-5.6-sol", nil),
	}
	ctx := ScoringContext{TaskClass: profile.TaskClass, TaskDomain: profile.TaskDomain, TargetQualityTier: requestQualityTarget(&profile), QualityBenefitCap: requestQualityBenefitCap(&profile), Weights: DefaultTaskWeights()[profile.TaskClass]}
	applyDomainStrength(&entries[0], ctx.TaskDomain)
	applyDomainStrength(&entries[1], ctx.TaskDomain)
	scored54 := ScoreCandidate(entries[0].ScoringCandidate, ctx)
	scored56 := ScoreCandidate(entries[1].ScoringCandidate, ctx)
	if scored56.Score <= scored54.Score {
		t.Fatalf("premium benchmark tie-break should prefer gpt-5.6-sol over gpt-5.4: sol=%f 54=%f", scored56.Score, scored54.Score)
	}
}

func TestBuildPlanPreviewsAutoResolvedModel(t *testing.T) {
	cfgManager, cleanup := createTestConfigManager(t, modelPreviewConfig("shadow"))
	defer cleanup()
	store := newModelPreviewStore(t, glmPreviewProfile())
	resolver := NewModelResolver(store, cfgManager)
	router := NewSmartRouter(nil, nil, nil, cfgManager)
	router.SetModelResolver(resolver)

	profile := BuildRequestProfile(RequestProfileFeatures{
		Model: "claude-sonnet-5", ChannelKind: "messages", Operation: "completion",
		EstTokens: 20_000, ToolUseNeed: true, ReasoningNeed: true,
	})
	plan := router.BuildPlan(&profile)
	if len(plan.Candidates) != 1 {
		t.Fatalf("candidate count = %d, want 1: %+v", len(plan.Candidates), plan.Candidates)
	}
	candidate := plan.Candidates[0]
	if candidate.ChannelUID != "ch_glm_auto" || candidate.MappedModel != "glm-5.2" {
		t.Fatalf("candidate = %+v, want ch_glm_auto mapped to glm-5.2", candidate)
	}
	if candidate.MappingSource != "auto_resolve_preview" || candidate.MappingReason == "" {
		t.Fatalf("mapping metadata = %q/%q", candidate.MappingSource, candidate.MappingReason)
	}
	if plan.SelectedChannelUID != "ch_glm_auto" || plan.SelectedModel != "glm-5.2" {
		t.Fatalf("selection = %q/%q, want ch_glm_auto/glm-5.2",
			plan.SelectedChannelUID, plan.SelectedModel)
	}
	if !containsString(plan.SortReasons, "dryrun_auto_resolve_preview") {
		t.Fatalf("sort reasons = %v, want auto-resolve preview marker", plan.SortReasons)
	}
}

func TestBuildPlanAutoResolvePreviewRespectsCapabilityFloor(t *testing.T) {
	cfgManager, cleanup := createTestConfigManager(t, modelPreviewConfig("shadow"))
	defer cleanup()
	profile := glmPreviewProfile()
	profile.SupportsToolCalls = false
	store := newModelPreviewStore(t, profile)
	router := NewSmartRouter(nil, nil, nil, cfgManager)
	router.SetModelResolver(NewModelResolver(store, cfgManager))

	requestProfile := BuildRequestProfile(RequestProfileFeatures{
		Model: "claude-sonnet-5", ChannelKind: "messages", Operation: "completion",
		EstTokens: 20_000, ToolUseNeed: true,
	})
	plan := router.BuildPlan(&requestProfile)
	if len(plan.Candidates) != 0 {
		t.Fatalf("ineligible auto-resolve candidates = %+v, want none", plan.Candidates)
	}
}

func TestSmartRouterUsesAutoResolvedModelForHardConstraints(t *testing.T) {
	cfgManager, cleanup := createTestConfigManager(t, modelPreviewConfig("auto"))
	defer cleanup()
	modelStore := newModelPreviewStore(t, glmPreviewProfile())
	traceStore := createTestTraceStore(t)
	profileStore := &ProfileStore{
		cache: map[string]*KeyEndpointProfile{
			"ep_glm": {
				EndpointUID:       "ep_glm",
				ChannelUID:        "ch_glm_auto",
				ChannelKind:       "messages",
				MetricsKey:        "metrics_glm",
				HealthState:       HealthStateHealthy,
				QualityTier:       QualityTierHigh,
				StabilityTier:     StabilityTierStable,
				SpeedTier:         SpeedTierNormal,
				CostTier:          CostTierNormal,
				SupportsToolCalls: false,
				SupportsReasoning: false,
			},
		},
		dirtyKeys: make(map[string]struct{}),
	}
	router := NewSmartRouter(profileStore, nil, traceStore, cfgManager)
	router.SetModelResolver(NewModelResolver(modelStore, cfgManager))

	profile := BuildRequestProfile(RequestProfileFeatures{
		Model: "claude-opus-4-8", ChannelKind: "messages", Operation: "completion",
		EstTokens: 17_906, ToolUseNeed: true, ReasoningNeed: true,
	})
	cfg := cfgManager.GetConfig()
	upstream := cfg.Upstream[0]
	resolution := router.resolveChannelModel(&profile, &upstream, cfg.UpstreamModelCapabilities)
	if !resolution.Supported || resolution.ActualModel != "glm-5.2" || resolution.MappedModel != "glm-5.2" {
		t.Fatalf("model resolution = %+v, want supported glm-5.2 mapping", resolution)
	}
	if resolution.MappingSource != "auto_resolve" || strings.HasPrefix(resolution.MappingReason, "quality_fallback:") {
		t.Fatalf("mapping metadata = %q/%q, want task-aware high-tier auto-resolve",
			resolution.MappingSource, resolution.MappingReason)
	}

	filter := router.CandidateFilterFor(&profile)
	if filter == nil {
		t.Fatal("auto mode filter should not be nil")
	}
	channels := []scheduler.ChannelInfo{{Index: 0, Name: upstream.Name, Status: "active"}}
	result, err := filter(
		channels,
		func(scheduler.ChannelInfo) *config.UpstreamConfig { return &upstream },
		func(scheduler.ChannelInfo, *config.UpstreamConfig) bool { return true },
	)
	if err != nil {
		t.Fatalf("CandidateFilterFor() error = %v", err)
	}
	if len(result) != 1 || result[0].Index != 0 {
		t.Fatalf("filtered channels = %+v, want GLM channel retained", result)
	}

	traces := traceStore.ListRecent(1)
	if len(traces) != 1 || len(traces[0].Candidates) != 1 {
		t.Fatalf("routing traces = %+v, want one candidate", traces)
	}
	candidate := traces[0].Candidates[0]
	if !candidate.Selected {
		t.Fatalf("candidate filtered unexpectedly: %+v", candidate.FilterReasons)
	}
	if containsString(candidate.FilterReasons, "工具调用能力不满足") {
		t.Fatalf("mapped GLM capability was evaluated as original Opus: %+v", candidate.FilterReasons)
	}
}

func TestBuildPlanAutoManagedEmptyModelsUsesProfilePolicy(t *testing.T) {
	cfg := modelPreviewConfig("assist")
	cfg.Upstream[0].SupportedModels = nil
	cfgManager, cleanup := createTestConfigManager(t, cfg)
	defer cleanup()
	resolver := NewModelResolver(newModelPreviewStore(t, glmPreviewProfile()), cfgManager)
	router := NewSmartRouter(nil, nil, nil, cfgManager)
	router.SetModelResolver(resolver)

	adaptive := BuildRequestProfile(RequestProfileFeatures{
		Model: "claude-sonnet-5", ChannelKind: "messages", Operation: "completion", EstTokens: 2_000,
	})
	plan := router.BuildPlan(&adaptive)
	if len(plan.Candidates) != 1 || plan.Candidates[0].MappedModel != "glm-5.2" {
		t.Fatalf("adaptive candidates = %+v, want GLM profile mapping", plan.Candidates)
	}

	exactOnly := BuildRequestProfile(RequestProfileFeatures{
		Model: "deepseek-chat", ChannelKind: "messages", Operation: "completion", EstTokens: 2_000,
	})
	plan = router.BuildPlan(&exactOnly)
	if len(plan.Candidates) != 0 {
		t.Fatalf("exact-only candidates = %+v, want none without exact model profile", plan.Candidates)
	}
}

func TestResolveModelSupportAutoManagedEmptyModelsUsesProfilePolicy(t *testing.T) {
	cfg := modelPreviewConfig("assist")
	cfg.Upstream[0].SupportedModels = nil
	cfgManager, cleanup := createTestConfigManager(t, cfg)
	defer cleanup()
	resolver := NewModelResolver(newModelPreviewStore(t, glmPreviewProfile()), cfgManager)
	manager := &Manager{cfgManager: cfgManager, modelResolver: resolver}
	upstream := cfgManager.GetConfig().Upstream[0]

	supported, mapped, source, reason := manager.ResolveModelSupport("messages", &upstream, "claude-sonnet-5")
	if !supported || mapped != "glm-5.2" || source != "auto_resolve" || reason == "" {
		t.Fatalf("adaptive support = %v mapped=%q source=%q reason=%q",
			supported, mapped, source, reason)
	}

	supported, mapped, source, reason = manager.ResolveModelSupport("messages", &upstream, "deepseek-chat")
	if supported || mapped != "" || source != scheduler.ModelSupportSourceAuthoritativeDeny || reason != "exact_model_required" {
		t.Fatalf("exact-only support = %v mapped=%q source=%q reason=%q",
			supported, mapped, source, reason)
	}
}

func TestResolveModelSupportWithFloorFallsBackBelowQualityTarget(t *testing.T) {
	cfg := modelPreviewConfig("assist")
	cfg.Upstream[0].SupportedModels = nil
	cfgManager, cleanup := createTestConfigManager(t, cfg)
	defer cleanup()
	resolver := NewModelResolver(newModelPreviewStore(t, glmPreviewProfile()), cfgManager)
	manager := &Manager{cfgManager: cfgManager, modelResolver: resolver}
	upstream := cfgManager.GetConfig().Upstream[0]

	supported, mapped, source, reason := manager.ResolveModelSupportWithFloor(
		"messages",
		&upstream,
		"claude-opus-4-8",
		CapabilityFloor{MinQualityTier: QualityTierPremium},
	)
	if !supported || mapped != "glm-5.2" || source != "auto_resolve" || !strings.HasPrefix(reason, "quality_fallback:") {
		t.Fatalf("premium floor support = %v mapped=%q source=%q reason=%q",
			supported, mapped, source, reason)
	}
}

func TestResolveModelSupportWithFloorRejectsMissingHardCapability(t *testing.T) {
	cfg := modelPreviewConfig("assist")
	cfg.Upstream[0].SupportedModels = nil
	cfgManager, cleanup := createTestConfigManager(t, cfg)
	defer cleanup()
	profile := glmPreviewProfile()
	profile.SupportsToolCalls = false
	resolver := NewModelResolver(newModelPreviewStore(t, profile), cfgManager)
	manager := &Manager{cfgManager: cfgManager, modelResolver: resolver}
	upstream := cfgManager.GetConfig().Upstream[0]

	supported, mapped, source, reason := manager.ResolveModelSupportWithFloor(
		"messages",
		&upstream,
		"claude-opus-4-8",
		CapabilityFloor{MinQualityTier: QualityTierPremium, NeedsToolCalls: true},
	)
	if supported || mapped != "" || source != scheduler.ModelSupportSourceAuthoritativeDeny || reason != "no_capable_model" {
		t.Fatalf("hard capability support = %v mapped=%q source=%q reason=%q",
			supported, mapped, source, reason)
	}
}

func TestResolveModelSupportWithFloorRefreshesLegacyKimiK3Profile(t *testing.T) {
	cfg := modelPreviewConfig("auto")
	cfg.Upstream[0].Name = "kimi-auto"
	cfg.Upstream[0].ChannelUID = "ch_kimi_auto"
	cfg.Upstream[0].ProviderID = "kimi"
	cfg.Upstream[0].ServiceType = "claude"
	cfg.Upstream[0].SupportedModels = nil
	cfgManager, cleanup := createTestConfigManager(t, cfg)
	defer cleanup()

	legacyProfile := ModelProfile{
		ChannelUID: "ch_kimi_auto", ChannelKind: "messages", MetricsKey: "metrics_kimi",
		ModelID: "k3", ModelFamily: ModelFamilyUnknown, QualityTier: QualityTierLow,
		ProbeSuccess: true, Source: "auto_discovery",
	}
	store := newModelPreviewStore(t, legacyProfile)
	resolver := NewModelResolver(store, cfgManager)
	manager := &Manager{cfgManager: cfgManager, modelResolver: resolver}
	upstream := cfgManager.GetConfig().Upstream[0]

	supported, mapped, source, reason := manager.ResolveModelSupportWithFloor(
		"messages",
		&upstream,
		"claude-opus-4-8",
		CapabilityFloor{
			MinQualityTier: QualityTierPremium,
			NeedsReasoning: true,
			NeedsToolCalls: true,
		},
	)
	if !supported || mapped != "k3" || source != "auto_resolve" || reason == "" {
		t.Fatalf("Kimi K3 support = %v mapped=%q source=%q reason=%q",
			supported, mapped, source, reason)
	}
}

func TestResolveModelSupportUsesAutoResolveInAutopilot(t *testing.T) {
	cfgManager, cleanup := createTestConfigManager(t, modelPreviewConfig("shadow"))
	defer cleanup()
	resolver := NewModelResolver(newModelPreviewStore(t, glmPreviewProfile()), cfgManager)
	manager := &Manager{cfgManager: cfgManager, modelResolver: resolver}
	upstream := cfgManager.GetConfig().Upstream[0]

	supported, mapped, source, reason := manager.ResolveModelSupport("messages", &upstream, "claude-sonnet-5")
	if !supported || mapped != "glm-5.2" || source != "auto_resolve" || reason == "" {
		t.Fatalf("Autopilot support = %v mapped=%q source=%q reason=%q", supported, mapped, source, reason)
	}
}

func TestWireSmartRouterInjectsDryRunModelResolver(t *testing.T) {
	resolver := &ModelResolver{}
	router := &SmartRouter{}
	manager := &Manager{smartRouter: router, modelResolver: resolver}
	manager.WireSmartRouter()
	if router.modelResolver != resolver {
		t.Fatal("WireSmartRouter() did not inject ModelResolver")
	}
}
