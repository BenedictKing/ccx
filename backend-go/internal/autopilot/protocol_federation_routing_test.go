package autopilot

import (
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/scheduler"
)

// federationProfileStore 为 messages 原生渠道与 chat sibling 分别登记画像，
// 使评分只依赖显式档位，不依赖生成的 model registry 分数。
func federationProfileStore(t *testing.T) *ProfileStore {
	t.Helper()
	return &ProfileStore{
		cache: map[string]*KeyEndpointProfile{
			"ep_native": {
				EndpointUID: "ep_native", ChannelUID: "ch_native", ChannelKind: "messages",
				MetricsKey: "metrics_native", KeyMask: "sk-***nat", HealthState: HealthStateHealthy,
				QualityTier: QualityTierHigh, StabilityTier: StabilityTierStable,
				SpeedTier: SpeedTierNormal, CostTier: CostTierNormal,
				SupportsToolCalls: true, SupportsReasoning: true,
			},
			"ep_k3": {
				EndpointUID: "ep_k3", ChannelUID: "ch_k3_chat", ChannelKind: "chat",
				MetricsKey: "metrics_k3", KeyMask: "sk-***k3", HealthState: HealthStateHealthy,
				QualityTier: QualityTierPremium, StabilityTier: StabilityTierStable,
				SpeedTier: SpeedTierNormal, CostTier: CostTierNormal,
				SupportsToolCalls: true, SupportsReasoning: true,
			},
		},
		dirtyKeys: make(map[string]struct{}),
	}
}

func federationRouterConfig() config.Config {
	return config.Config{
		AutopilotRouting: config.DefaultAutopilotRoutingConfig(),
		Upstream: []config.UpstreamConfig{{
			Name: "native", ChannelUID: "ch_native", AccountUID: "acct-a", AutoManaged: true,
			BaseURL: "https://messages.example.com", APIKeys: []string{"sk-native"}, Status: "active",
		}},
		ChatUpstream: []config.UpstreamConfig{{
			Name: "sol-chat", ChannelUID: "ch_k3_chat", AccountUID: "acct-a", AutoManaged: true,
			BaseURL: "https://api.openai.com", APIKeys: []string{"sk-k3"}, Status: "active",
		}},
	}
}

func federationChannels(penalty float64) []scheduler.ChannelInfo {
	return []scheduler.ChannelInfo{
		{
			Route:            scheduler.ChannelRouteRef{Kind: "messages", Index: 0, ChannelUID: "ch_native"},
			Index:            0,
			Name:             "native",
			Status:           "active",
			ProtocolFidelity: "native",
		},
		{
			Route:             scheduler.ChannelRouteRef{Kind: "chat", Index: 0, ChannelUID: "ch_k3_chat"},
			Index:             0,
			Name:              "sol-chat",
			Status:            "active",
			ActualModel:       "gpt-5.6-sol",
			ProtocolFidelity:  "converted",
			ConversionPenalty: penalty,
		},
	}
}

func federationUpstreamFor(cfg config.Config) func(scheduler.ChannelInfo) *config.UpstreamConfig {
	return func(ch scheduler.ChannelInfo) *config.UpstreamConfig {
		switch ch.Route.Kind {
		case "chat":
			u := cfg.ChatUpstream[ch.Index]
			return &u
		default:
			u := cfg.Upstream[ch.Index]
			return &u
		}
	}
}

func runFederationFilter(t *testing.T, profile *RequestProfile, penalty float64) ([]scheduler.ChannelInfo, *RoutingDecisionTrace) {
	t.Helper()
	cfg := federationRouterConfig()
	cfgManager, cleanup := createTestConfigManager(t, cfg)
	t.Cleanup(cleanup)
	traceStore := createTestTraceStore(t)
	router := NewSmartRouter(federationProfileStore(t), nil, traceStore, cfgManager)

	filter := router.CandidateFilterFor(profile)
	if filter == nil {
		t.Fatal("CandidateFilterFor returned nil")
	}
	processed := cfgManager.GetConfig()
	result, err := filter(
		federationChannels(penalty),
		federationUpstreamFor(processed),
		func(_ scheduler.ChannelInfo, u *config.UpstreamConfig) bool { return u != nil && len(u.APIKeys) > 0 },
	)
	if err != nil {
		t.Fatalf("filter error: %v", err)
	}
	traces := traceStore.ListRecent(1)
	if len(traces) == 0 {
		t.Fatal("no routing trace recorded")
	}
	return result, traces[0]
}

func federationWorkerProfile() *RequestProfile {
	return &RequestProfile{
		Model: "claude-sonnet-5", ChannelKind: "messages", Operation: "completion",
		AgentRole: "main", TaskClass: TaskClassWorker, Complexity: TaskComplexityRoutine,
		QualityNeed: QualityTierHigh, EstTokens: 4000, ToolUseNeed: true,
	}
}

func federationComplexProfile() *RequestProfile {
	return &RequestProfile{
		Model: "claude-sonnet-5", ChannelKind: "messages", Operation: "completion",
		AgentRole: "main", TaskClass: TaskClassWorker, Complexity: TaskComplexityComplex,
		QualityNeed: QualityTierPremium, EstTokens: 90_000, ToolUseNeed: true, ReasoningNeed: true,
	}
}

func TestFederationTraceRecordsRequestAndExecutionKind(t *testing.T) {
	_, trace := runFederationFilter(t, federationWorkerProfile(), 0.35)

	if trace.RequestKind != "messages" {
		t.Fatalf("RequestKind = %q, want messages", trace.RequestKind)
	}
	var sibling *RoutingCandidate
	for i := range trace.Candidates {
		if trace.Candidates[i].ChannelUID == "ch_k3_chat" {
			sibling = &trace.Candidates[i]
		}
	}
	if sibling == nil {
		t.Fatalf("sibling candidate missing from trace: %#v", trace.Candidates)
	}
	if sibling.ExecutionKind != "chat" {
		t.Fatalf("ExecutionKind = %q, want chat", sibling.ExecutionKind)
	}
	if sibling.ProtocolFidelity != "converted" || sibling.ConversionPenalty != 0.35 {
		t.Fatalf("fidelity/penalty not traced: %#v", sibling)
	}
	if sibling.MappedModel != "gpt-5.6-sol" || sibling.MappingSource == "" {
		t.Fatalf("sibling execution model attribution missing: %#v", sibling)
	}
	for i := range trace.Candidates {
		if trace.Candidates[i].ChannelUID == "ch_native" {
			if trace.Candidates[i].ExecutionKind != "messages" || trace.Candidates[i].ProtocolFidelity != "native" {
				t.Fatalf("native candidate attribution wrong: %#v", trace.Candidates[i])
			}
			if trace.Candidates[i].ConversionPenalty != 0 {
				t.Fatalf("native candidate must not carry conversion penalty: %#v", trace.Candidates[i])
			}
		}
	}
}

func TestFederationQualityBenefitCapBlocksPremiumUpgradeForRoutineWork(t *testing.T) {
	result, trace := runFederationFilter(t, federationWorkerProfile(), 0.35)
	if len(result) == 0 {
		t.Fatal("no candidates returned")
	}
	if result[0].Route.Kind != "messages" || result[0].Route.ChannelUID != "ch_native" {
		t.Fatalf("routine worker request upgraded to premium sibling: %#v", result[0].Route)
	}
	if trace.SelectedChannelUID != "ch_native" {
		t.Fatalf("trace selection = %q, want ch_native", trace.SelectedChannelUID)
	}

	// 惩罚为 0 时仍不得升级：证明拦截来自 QualityBenefitCap 而非转换惩罚。
	noPenalty, _ := runFederationFilter(t, federationWorkerProfile(), 0)
	if len(noPenalty) == 0 || noPenalty[0].Route.ChannelUID != "ch_native" {
		t.Fatalf("QualityBenefitCap did not hold without conversion penalty: %#v", noPenalty)
	}
}

func TestFederationAllowsPremiumSiblingForComplexWorkWithoutPenaltyBlock(t *testing.T) {
	result, _ := runFederationFilter(t, federationComplexProfile(), 0)
	if len(result) == 0 {
		t.Fatal("no candidates returned")
	}
	if result[0].Route.Kind != "chat" || result[0].Route.ChannelUID != "ch_k3_chat" {
		t.Fatalf("complex request did not select premium sibling: %#v", result[0].Route)
	}
}

func TestFederationConversionPenaltyCanOutweighQualityGain(t *testing.T) {
	result, _ := runFederationFilter(t, federationComplexProfile(), 5)
	if len(result) == 0 {
		t.Fatal("no candidates returned")
	}
	if result[0].Route.Kind != "messages" {
		t.Fatalf("large conversion penalty ignored: %#v", result[0].Route)
	}
}

func TestFederationEntryUsesExecutionKindForProfileLookup(t *testing.T) {
	cfg := federationRouterConfig()
	cfgManager, cleanup := createTestConfigManager(t, cfg)
	defer cleanup()
	router := NewSmartRouter(federationProfileStore(t), nil, nil, cfgManager)

	processed := cfgManager.GetConfig()
	sibling := federationChannels(0.35)[1]
	upstream := processed.ChatUpstream[0]
	entry := router.buildChannelEntry(sibling, &upstream, sibling.Route.Kind, "gpt-5.6-sol", processed.UpstreamModelCapabilities)

	if entry.ChannelKind != "chat" {
		t.Fatalf("ChannelKind = %q, want chat", entry.ChannelKind)
	}
	if entry.MetricsKey != "metrics_k3" || entry.KeyMask != "sk-***k3" {
		t.Fatalf("metrics/key attribution wrong: %#v", entry)
	}
	if entry.Route.Key() != sibling.Route.Key() {
		t.Fatalf("entry route identity = %#v, want %#v", entry.Route, sibling.Route)
	}
}
