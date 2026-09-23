package scheduler

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/conversation"
)

func TestSelectChannelFiltersByContextWindowStableOrder(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "cheap-200k",
				BaseURL:  "https://cheap.example.com",
				APIKeys:  []string{"sk-cheap"},
				Status:   "active",
				Priority: 1,
				ModelCapabilities: map[string]config.UpstreamModelCapability{
					"agent": {ContextWindowTokens: 200000},
				},
			},
			{
				Name:     "mid-272k",
				BaseURL:  "https://mid.example.com",
				APIKeys:  []string{"sk-mid"},
				Status:   "active",
				Priority: 2,
				ModelCapabilities: map[string]config.UpstreamModelCapability{
					"agent": {ContextWindowTokens: 272000},
				},
			},
			{
				Name:     "premium-1m",
				BaseURL:  "https://premium.example.com",
				APIKeys:  []string{"sk-premium"},
				Status:   "active",
				Priority: 3,
				ModelCapabilities: map[string]config.UpstreamModelCapability{
					"agent": {ContextWindowTokens: 1000000},
				},
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	tests := []struct {
		name     string
		required int
		wantName string
	}{
		{name: "50k keeps first 200k channel", required: 50000, wantName: "cheap-200k"},
		{name: "230k skips 200k and keeps 272k first", required: 230000, wantName: "mid-272k"},
		{name: "500k skips 200k and 272k", required: 500000, wantName: "premium-1m"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := scheduler.SelectChannelWithOptions(context.Background(), SelectionOptions{
				UserID:         "user-context",
				FailedChannels: map[int]bool{},
				Kind:           ChannelKindMessages,
				Model:          "agent",
				ContextRequirement: &ContextRequirement{
					InputTokens:    tt.required - 8192,
					OutputTokens:   8192,
					RequiredTokens: tt.required,
				},
			})
			if err != nil {
				t.Fatalf("SelectChannelWithOptions() error = %v", err)
			}
			if result.Upstream.Name != tt.wantName {
				t.Fatalf("selected channel = %q, want %q", result.Upstream.Name, tt.wantName)
			}
		})
	}
}

func TestSelectChannelFiltersContextWindowByInputOnlyWithoutExtraOutputReserve(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "cheap-200k",
				BaseURL:  "https://cheap.example.com",
				APIKeys:  []string{"sk-cheap"},
				Status:   "active",
				Priority: 1,
				ModelCapabilities: map[string]config.UpstreamModelCapability{
					"agent": {ContextWindowTokens: 200000},
				},
			},
			{
				Name:     "premium-1m",
				BaseURL:  "https://premium.example.com",
				APIKeys:  []string{"sk-premium"},
				Status:   "active",
				Priority: 2,
				ModelCapabilities: map[string]config.UpstreamModelCapability{
					"agent": {ContextWindowTokens: 1000000},
				},
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	result, err := scheduler.SelectChannelWithOptions(context.Background(), SelectionOptions{
		UserID:         "user-input-only",
		FailedChannels: map[int]bool{},
		Kind:           ChannelKindMessages,
		Model:          "agent",
		ContextRequirement: &ContextRequirement{
			InputTokens:       196000,
			OutputTokens:      8192,
			RequiredTokens:    204192,
			ExplicitOutputMax: true,
		},
	})
	if err != nil {
		t.Fatalf("SelectChannelWithOptions() error = %v", err)
	}
	if result.Upstream.Name != "cheap-200k" {
		t.Fatalf("selected channel = %q, want cheap-200k", result.Upstream.Name)
	}
}

func TestSelectChannelDoesNotUseAgentProfileAsHardMinimumWindow(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "legacy-200k",
				BaseURL:  "https://legacy.example.com",
				APIKeys:  []string{"sk-legacy"},
				Status:   "active",
				Priority: 1,
				ModelCapabilities: map[string]config.UpstreamModelCapability{
					"sonnet": {ContextWindowTokens: 200000},
					"haiku":  {ContextWindowTokens: 200000},
				},
			},
			{
				Name:     "modern-1m",
				BaseURL:  "https://modern.example.com",
				APIKeys:  []string{"sk-modern"},
				Status:   "active",
				Priority: 2,
				ModelCapabilities: map[string]config.UpstreamModelCapability{
					"sonnet": {ContextWindowTokens: 1000000},
					"haiku":  {ContextWindowTokens: 200000},
				},
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	sonnetProfile := config.ResolveAgentModelProfile("sonnet", nil)
	if !sonnetProfile.Known {
		t.Fatal("expected sonnet agent profile")
	}
	sonnet, err := scheduler.SelectChannelWithOptions(context.Background(), SelectionOptions{
		UserID:         "user-sonnet-profile",
		FailedChannels: map[int]bool{},
		Kind:           ChannelKindMessages,
		Model:          "sonnet",
		ContextRequirement: &ContextRequirement{
			InputTokens:                41808,
			OutputTokens:               8192,
			RequiredTokens:             50000,
			MinimumContextWindowTokens: sonnetProfile.Profile.ContextWindowTokens,
		},
	})
	if err != nil {
		t.Fatalf("sonnet SelectChannelWithOptions() error = %v", err)
	}
	if sonnet.Upstream.Name != "legacy-200k" {
		t.Fatalf("sonnet selected channel = %q, want legacy-200k", sonnet.Upstream.Name)
	}

	haikuProfile := config.ResolveAgentModelProfile("haiku", nil)
	if !haikuProfile.Known {
		t.Fatal("expected haiku agent profile")
	}
	haiku, err := scheduler.SelectChannelWithOptions(context.Background(), SelectionOptions{
		UserID:         "user-haiku-profile",
		FailedChannels: map[int]bool{},
		Kind:           ChannelKindMessages,
		Model:          "haiku",
		ContextRequirement: &ContextRequirement{
			InputTokens:                41808,
			OutputTokens:               8192,
			RequiredTokens:             50000,
			MinimumContextWindowTokens: haikuProfile.Profile.ContextWindowTokens,
		},
	})
	if err != nil {
		t.Fatalf("haiku SelectChannelWithOptions() error = %v", err)
	}
	if haiku.Upstream.Name != "legacy-200k" {
		t.Fatalf("haiku selected channel = %q, want legacy-200k", haiku.Upstream.Name)
	}
}

func TestSelectChannelFiltersExplicitOutputLimit(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "sonnet-64k-output",
				BaseURL:  "https://sonnet.example.com",
				APIKeys:  []string{"sk-sonnet"},
				Status:   "active",
				Priority: 1,
				ModelCapabilities: map[string]config.UpstreamModelCapability{
					"agent": {ContextWindowTokens: 1000000, MaxOutputTokens: 64000},
				},
			},
			{
				Name:     "opus-128k-output",
				BaseURL:  "https://opus.example.com",
				APIKeys:  []string{"sk-opus"},
				Status:   "active",
				Priority: 2,
				ModelCapabilities: map[string]config.UpstreamModelCapability{
					"agent": {ContextWindowTokens: 1000000, MaxOutputTokens: 128000},
				},
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	result, err := scheduler.SelectChannelWithOptions(context.Background(), SelectionOptions{
		UserID:         "user-output",
		FailedChannels: map[int]bool{},
		Kind:           ChannelKindMessages,
		Model:          "agent",
		ContextRequirement: &ContextRequirement{
			InputTokens:       1000,
			OutputTokens:      128000,
			RequiredTokens:    129000,
			ExplicitOutputMax: true,
		},
	})
	if err != nil {
		t.Fatalf("SelectChannelWithOptions() error = %v", err)
	}
	if result.Upstream.Name != "opus-128k-output" {
		t.Fatalf("selected channel = %q, want opus-128k-output", result.Upstream.Name)
	}
}

func TestSelectChannelOutputLimitFallsBackToClampCandidateAfterFailure(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "opus-128k-output",
				BaseURL:  "https://opus.example.com",
				APIKeys:  []string{"sk-opus"},
				Status:   "active",
				Priority: 1,
				ModelCapabilities: map[string]config.UpstreamModelCapability{
					"agent": {ContextWindowTokens: 1000000, MaxOutputTokens: 128000},
				},
			},
			{
				Name:     "sonnet-64k-output",
				BaseURL:  "https://sonnet.example.com",
				APIKeys:  []string{"sk-sonnet"},
				Status:   "active",
				Priority: 2,
				ModelCapabilities: map[string]config.UpstreamModelCapability{
					"agent": {ContextWindowTokens: 1000000, MaxOutputTokens: 64000},
				},
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	result, err := scheduler.SelectChannelWithOptions(context.Background(), SelectionOptions{
		UserID: "user-output-failover",
		FailedChannels: map[int]bool{
			0: true,
		},
		Kind:  ChannelKindMessages,
		Model: "agent",
		ContextRequirement: &ContextRequirement{
			InputTokens:       1000,
			OutputTokens:      128000,
			RequiredTokens:    129000,
			ExplicitOutputMax: true,
		},
	})
	if err != nil {
		t.Fatalf("SelectChannelWithOptions() error = %v", err)
	}
	if result.Upstream.Name != "sonnet-64k-output" {
		t.Fatalf("selected channel = %q, want sonnet-64k-output", result.Upstream.Name)
	}
}

func TestSelectChannelCompactionSkipsWindowButKeepsOutputLimit(t *testing.T) {
	cfg := config.Config{
		ResponsesUpstream: []config.UpstreamConfig{
			{
				Name:     "sonnet-200k",
				BaseURL:  "https://sonnet.example.com",
				APIKeys:  []string{"sk-sonnet"},
				Status:   "active",
				Priority: 1,
				ModelCapabilities: map[string]config.UpstreamModelCapability{
					"agent": {ContextWindowTokens: 200000, MaxOutputTokens: 64000},
				},
			},
			{
				Name:     "opus-128k-output",
				BaseURL:  "https://opus.example.com",
				APIKeys:  []string{"sk-opus"},
				Status:   "active",
				Priority: 2,
				ModelCapabilities: map[string]config.UpstreamModelCapability{
					"agent": {ContextWindowTokens: 1000000, MaxOutputTokens: 128000},
				},
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	result, err := scheduler.SelectChannelWithOptions(context.Background(), SelectionOptions{
		UserID:         "user-compact",
		FailedChannels: map[int]bool{},
		Kind:           ChannelKindResponses,
		Model:          "agent",
		ContextRequirement: &ContextRequirement{
			InputTokens:          491808,
			OutputTokens:         8192,
			RequiredTokens:       500000,
			SkipWindowValidation: true,
		},
	})
	if err != nil {
		t.Fatalf("SelectChannelWithOptions() error = %v", err)
	}
	if result.Upstream.Name != "sonnet-200k" {
		t.Fatalf("selected channel = %q, want sonnet-200k", result.Upstream.Name)
	}

	result, err = scheduler.SelectChannelWithOptions(context.Background(), SelectionOptions{
		UserID:         "user-compact-output",
		FailedChannels: map[int]bool{},
		Kind:           ChannelKindResponses,
		Model:          "agent",
		ContextRequirement: &ContextRequirement{
			InputTokens:          1000,
			OutputTokens:         128000,
			RequiredTokens:       129000,
			ExplicitOutputMax:    true,
			SkipWindowValidation: true,
		},
	})
	if err != nil {
		t.Fatalf("SelectChannelWithOptions() output error = %v", err)
	}
	if result.Upstream.Name != "opus-128k-output" {
		t.Fatalf("selected output channel = %q, want opus-128k-output", result.Upstream.Name)
	}
}

func TestSelectChannelPinnedChannelMustSatisfyContext(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "cheap-200k",
				BaseURL:  "https://cheap.example.com",
				APIKeys:  []string{"sk-cheap"},
				Status:   "active",
				Priority: 1,
				ModelCapabilities: map[string]config.UpstreamModelCapability{
					"agent": {ContextWindowTokens: 200000},
				},
			},
			{
				Name:     "premium-1m",
				BaseURL:  "https://premium.example.com",
				APIKeys:  []string{"sk-premium"},
				Status:   "active",
				Priority: 2,
				ModelCapabilities: map[string]config.UpstreamModelCapability{
					"agent": {ContextWindowTokens: 1000000},
				},
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	_, err := scheduler.SelectChannelWithOptions(context.Background(), SelectionOptions{
		UserID:         "user-pin",
		FailedChannels: map[int]bool{},
		Kind:           ChannelKindMessages,
		Model:          "agent",
		ChannelName:    "cheap-200k",
		ContextRequirement: &ContextRequirement{
			InputTokens:    491808,
			OutputTokens:   8192,
			RequiredTokens: 500000,
		},
	})
	if err == nil {
		t.Fatal("SelectChannelWithOptions() error = nil, want pinned context rejection")
	}
	if !strings.Contains(err.Error(), "指定渠道") {
		t.Fatalf("error = %q, want pinned channel rejection", err.Error())
	}
}

func TestSelectChannelUnknownContextPolicy(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "unknown",
				BaseURL:  "https://unknown.example.com",
				APIKeys:  []string{"sk-unknown"},
				Status:   "active",
				Priority: 1,
			},
			{
				Name:     "premium-1m",
				BaseURL:  "https://premium.example.com",
				APIKeys:  []string{"sk-premium"},
				Status:   "active",
				Priority: 2,
				ModelCapabilities: map[string]config.UpstreamModelCapability{
					"agent": {ContextWindowTokens: 1000000},
				},
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	small, err := scheduler.SelectChannelWithOptions(context.Background(), SelectionOptions{
		UserID:         "user-small",
		FailedChannels: map[int]bool{},
		Kind:           ChannelKindMessages,
		Model:          "agent",
		ContextRequirement: &ContextRequirement{
			InputTokens:    41808,
			OutputTokens:   8192,
			RequiredTokens: 50000,
		},
	})
	if err != nil {
		t.Fatalf("small SelectChannelWithOptions() error = %v", err)
	}
	if small.Upstream.Name != "unknown" {
		t.Fatalf("small selected channel = %q, want unknown", small.Upstream.Name)
	}

	large, err := scheduler.SelectChannelWithOptions(context.Background(), SelectionOptions{
		UserID:         "user-large",
		FailedChannels: map[int]bool{},
		Kind:           ChannelKindMessages,
		Model:          "agent",
		ContextRequirement: &ContextRequirement{
			InputTokens:    491808,
			OutputTokens:   8192,
			RequiredTokens: 500000,
		},
	})
	if err != nil {
		t.Fatalf("large SelectChannelWithOptions() error = %v", err)
	}
	if large.Upstream.Name != "premium-1m" {
		t.Fatalf("large selected channel = %q, want premium-1m", large.Upstream.Name)
	}
}

func TestSelectChannelManualOverridePreservedWhenContextFiltersChannel(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "manual-cheap-200k",
				BaseURL:  "https://cheap.example.com",
				APIKeys:  []string{"sk-cheap"},
				Status:   "active",
				Priority: 1,
				ModelCapabilities: map[string]config.UpstreamModelCapability{
					"agent": {ContextWindowTokens: 200000},
				},
			},
			{
				Name:     "fallback-1m",
				BaseURL:  "https://premium.example.com",
				APIKeys:  []string{"sk-premium"},
				Status:   "active",
				Priority: 2,
				ModelCapabilities: map[string]config.UpstreamModelCapability{
					"agent": {ContextWindowTokens: 1000000},
				},
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	overrideMgr := conversation.NewOverrideManager(time.Hour)
	defer overrideMgr.Stop()
	if err := overrideMgr.SetOverride("conv-1", string(ChannelKindMessages), "user-override", []conversation.ChannelEntry{
		{ChannelIndex: 0, ChannelName: "manual-cheap-200k"},
	}, time.Hour); err != nil {
		t.Fatalf("SetOverride() error = %v", err)
	}
	scheduler.SetConversationComponents(nil, overrideMgr)

	result, err := scheduler.SelectChannelWithOptions(context.Background(), SelectionOptions{
		UserID:         "user-override",
		FailedChannels: map[int]bool{},
		Kind:           ChannelKindMessages,
		Model:          "agent",
		ContextRequirement: &ContextRequirement{
			InputTokens:    491808,
			OutputTokens:   8192,
			RequiredTokens: 500000,
		},
	})
	if err != nil {
		t.Fatalf("SelectChannelWithOptions() error = %v", err)
	}
	if result.Upstream.Name != "fallback-1m" {
		t.Fatalf("selected channel = %q, want fallback-1m", result.Upstream.Name)
	}
	if _, ok := overrideMgr.GetOverrideForUser(string(ChannelKindMessages), "user-override"); !ok {
		t.Fatal("manual override was cleared; want preserved")
	}
}

func TestTraceAffinityUsesContextBuckets(t *testing.T) {
	cfg := config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:     "cheap-200k",
				BaseURL:  "https://cheap.example.com",
				APIKeys:  []string{"sk-cheap"},
				Status:   "active",
				Priority: 1,
				ModelCapabilities: map[string]config.UpstreamModelCapability{
					"agent": {ContextWindowTokens: 200000},
				},
			},
			{
				Name:     "premium-1m",
				BaseURL:  "https://premium.example.com",
				APIKeys:  []string{"sk-premium"},
				Status:   "active",
				Priority: 2,
				ModelCapabilities: map[string]config.UpstreamModelCapability{
					"agent": {ContextWindowTokens: 1000000},
				},
			},
		},
	}

	scheduler, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	largeReq := &ContextRequirement{InputTokens: 491808, OutputTokens: 8192, RequiredTokens: 500000}
	smallReq := &ContextRequirement{InputTokens: 41808, OutputTokens: 8192, RequiredTokens: 50000}
	scheduler.SetTraceAffinityForRequirement("user-bucket", 1, ChannelKindMessages, largeReq)

	result, err := scheduler.SelectChannelWithOptions(context.Background(), SelectionOptions{
		UserID:             "user-bucket",
		FailedChannels:     map[int]bool{},
		Kind:               ChannelKindMessages,
		Model:              "agent",
		ContextRequirement: smallReq,
	})
	if err != nil {
		t.Fatalf("SelectChannelWithOptions() error = %v", err)
	}
	if result.Upstream.Name != "cheap-200k" {
		t.Fatalf("small context selected channel = %q, want cheap-200k", result.Upstream.Name)
	}
}
