package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
)

func TestEncryptedResponsesAffinityRespectsAvailabilityAndFilters(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(*config.Config, *SelectionOptions)
		want    int
		reason  string
	}{
		{"encrypted_continuation", nil, 1, "trace_affinity"},
		{"ordinary_request", func(_ *config.Config, o *SelectionOptions) { o.PreserveTraceAffinity = false }, 0, "priority_order"},
		{"no_affinity", func(_ *config.Config, o *SelectionOptions) { o.UserID = "new-user" }, 0, "priority_order"},
		{"failed_in_request", func(_ *config.Config, o *SelectionOptions) { o.FailedChannels = map[int]bool{1: true} }, 0, "priority_order"},
		{"cooldown", nil, 0, "priority_order"},
		{"unsupported_model", func(c *config.Config, _ *SelectionOptions) {
			c.ResponsesUpstream[1].SupportedModels = []string{"other"}
		}, 0, "priority_order"},
		{"suspended", func(c *config.Config, _ *SelectionOptions) { c.ResponsesUpstream[1].Status = "suspended" }, 0, "priority_order"},
		{"explicit_pin", func(_ *config.Config, o *SelectionOptions) { o.ChannelName = "primary" }, 0, "channel_pin"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Config{ResponsesUpstream: []config.UpstreamConfig{
				{Name: "primary", BaseURL: "https://primary.example", APIKeys: []string{"primary"}, ServiceType: "responses", Status: "active", Priority: 1},
				{Name: "affinity", BaseURL: "https://affinity.example", APIKeys: []string{"affinity"}, ServiceType: "responses", Status: "active", Priority: 4},
			}}
			o := SelectionOptions{UserID: "user", Kind: ChannelKindResponses, Model: "gpt-6.1-sol", PreserveTraceAffinity: true}
			if tc.prepare != nil {
				tc.prepare(&cfg, &o)
			}
			s, cleanup := createTestScheduler(t, cfg)
			defer cleanup()
			if tc.name == "cooldown" {
				s.MarkChannelCooldown(ChannelKindResponses, 1, time.Minute)
			}
			s.SetTraceAffinity("user", 1, ChannelKindResponses)
			got, err := s.SelectChannelWithOptions(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			if got.ChannelIndex != tc.want || got.Reason != tc.reason {
				t.Fatalf("选择渠道 %d/%s，期望 %d/%s", got.ChannelIndex, got.Reason, tc.want, tc.reason)
			}
		})
	}
}
