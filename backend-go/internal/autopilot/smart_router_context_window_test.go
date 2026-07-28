package autopilot

import (
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/scheduler"
)

func TestBuildChannelEntryResolvesContextWindow(t *testing.T) {
	tests := []struct {
		name       string
		model      string
		upstream   config.UpstreamConfig
		global     map[string]config.UpstreamModelCapability
		wantTokens int
	}{
		{
			name:  "渠道模型能力覆盖优先",
			model: "alias-model",
			upstream: config.UpstreamConfig{
				ModelMapping: map[string]string{"alias-model": "actual-model"},
				ModelCapabilities: map[string]config.UpstreamModelCapability{
					"actual-model": {ContextWindowTokens: 4096},
				},
			},
			global: map[string]config.UpstreamModelCapability{
				"actual-model": {ContextWindowTokens: 8192},
			},
			wantTokens: 4096,
		},
		{
			name:  "映射后命中全局能力",
			model: "alias-model",
			upstream: config.UpstreamConfig{
				ModelMapping: map[string]string{"alias-model": "actual-model"},
			},
			global: map[string]config.UpstreamModelCapability{
				"actual-model": {ContextWindowTokens: 8192},
			},
			wantTokens: 8192,
		},
		{
			name:       "命中内置模型注册表",
			model:      "mimo-v2.5-pro",
			wantTokens: 1_048_576,
		},
		{
			name:  "动态映射后只使用实际模型能力",
			model: "gpt-5.6-sol",
			upstream: config.UpstreamConfig{
				ModelCapabilities: map[string]config.UpstreamModelCapability{
					"claude-fable-5": {ContextWindowTokens: 1_000_000},
					"gpt-5.6-sol":    {ContextWindowTokens: 272_000},
				},
			},
			wantTokens: 272_000,
		},
		{
			name:       "未知模型保持 fail-open",
			model:      "unknown-model-without-capability",
			wantTokens: 0,
		},
	}

	router := NewSmartRouter(nil, nil, nil, nil)
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := tt.upstream
			upstream.ChannelUID = "ch_context"
			entry := router.buildChannelEntry(
				scheduler.ChannelInfo{Index: 0, Name: "context", Status: "active"},
				&upstream,
				"messages",
				tt.model,
				tt.global,
			)
			if entry.ContextWindowTokens != tt.wantTokens {
				t.Fatalf("ContextWindowTokens = %d, want %d", entry.ContextWindowTokens, tt.wantTokens)
			}
		})
	}
}

func TestResolvedContextWindowFeedsAutoHardConstraint(t *testing.T) {
	router := NewSmartRouter(nil, nil, nil, nil)
	upstream := &config.UpstreamConfig{
		ChannelUID: "ch_short",
		ModelCapabilities: map[string]config.UpstreamModelCapability{
			"short-model": {ContextWindowTokens: 4096},
		},
	}
	entry := router.buildChannelEntry(
		scheduler.ChannelInfo{Index: 0, Name: "short", Status: "active"},
		upstream,
		"messages",
		"short-model",
		nil,
	)

	reasons := routingHardConstraintReasons(&RequestProfile{ContextNeed: 4097}, &entry)
	if len(reasons) != 1 || reasons[0] != "上下文窗口不满足" {
		t.Fatalf("routingHardConstraintReasons() = %v, want [上下文窗口不满足]", reasons)
	}
}

func TestAutoFiltersContextHardConstraintCandidates(t *testing.T) {
	const (
		model       = "auto-context-model"
		shortUID    = "ch_shadow_short"
		longUID     = "ch_shadow_long"
		contextNeed = 8192
	)

	cfg := baseTestConfig()
	cfg.Upstream = cfg.Upstream[:2]
	cfg.Upstream[0].ChannelUID = shortUID
	cfg.Upstream[0].ModelCapabilities = map[string]config.UpstreamModelCapability{
		model: {ContextWindowTokens: 4096},
	}
	cfg.Upstream[1].ChannelUID = longUID
	cfg.Upstream[1].ModelCapabilities = map[string]config.UpstreamModelCapability{
		model: {ContextWindowTokens: 16384},
	}
	cfg.AutopilotRouting = config.AutopilotRoutingConfig{RoutingMode: "auto"}

	cfgManager, cleanup := createTestConfigManager(t, cfg)
	defer cleanup()
	traceStore, err := NewTraceStoreWithDB(nil)
	if err != nil {
		t.Fatalf("NewTraceStoreWithDB() error = %v", err)
	}
	router := NewSmartRouter(nil, nil, traceStore, cfgManager)
	filter, observeActual := router.CandidateFilterForWithActual(&RequestProfile{
		Model: model, ChannelKind: "messages", Operation: "completion",
		EstTokens: 1000, ContextNeed: contextNeed,
	})
	if filter == nil || observeActual == nil {
		t.Fatal("auto mode should return filter and actual-channel observer")
	}

	processed := cfgManager.GetConfig()
	channels := []scheduler.ChannelInfo{
		{Index: 0, Name: processed.Upstream[0].Name, Status: "active"},
		{Index: 1, Name: processed.Upstream[1].Name, Status: "active"},
	}
	result, err := filter(
		channels,
		func(ch scheduler.ChannelInfo) *config.UpstreamConfig { return &processed.Upstream[ch.Index] },
		func(_ scheduler.ChannelInfo, upstream *config.UpstreamConfig) bool { return upstream != nil },
	)
	if err != nil {
		t.Fatalf("auto filter error = %v", err)
	}
	if len(result) != 1 || result[0].Name != "ch-standard" {
		t.Fatalf("auto context filter result = %v, want only ch-standard", result)
	}

	traces := traceStore.ListRecent(1)
	if len(traces) != 1 {
		t.Fatalf("trace count = %d, want 1", len(traces))
	}
	trace := traces[0]
	if trace.SelectedChannelUID != longUID {
		t.Fatalf("selected channel = %q, want %q", trace.SelectedChannelUID, longUID)
	}
	if trace.Mode != RoutingModeAuto {
		t.Fatalf("trace mode = %q, want %q", trace.Mode, RoutingModeAuto)
	}
	if !containsString(trace.SortReasons, "auto_filter_and_reorder") {
		t.Fatalf("sort reasons = %v, want auto_filter_and_reorder", trace.SortReasons)
	}
	if len(trace.Candidates) != 2 {
		t.Fatalf("candidate count = %d, want 2", len(trace.Candidates))
	}
	if trace.Candidates[0].ChannelUID != shortUID || trace.Candidates[0].Selected ||
		len(trace.Candidates[0].FilterReasons) != 1 || trace.Candidates[0].FilterReasons[0] != "上下文窗口不满足" {
		t.Fatalf("short candidate trace = %+v", trace.Candidates[0])
	}
	if trace.Candidates[1].ChannelUID != longUID || !trace.Candidates[1].Selected {
		t.Fatalf("long candidate trace = %+v", trace.Candidates[1])
	}

	observeActual(longUID)
	trace = traceStore.ListRecent(1)[0]
	if trace.ActualChannelUID != longUID || trace.Match {
		t.Fatalf("auto trace actual=%q match=%v, want actual=%q without shadow comparison", trace.ActualChannelUID, trace.Match, longUID)
	}
}
