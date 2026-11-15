package scheduler

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/conversation"
	"github.com/BenedictKing/ccx/internal/session"
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

func TestEncryptedResponsesAffinitySurvivesContextBucketChange(t *testing.T) {
	cfg := config.Config{ResponsesUpstream: []config.UpstreamConfig{
		{Name: "primary", BaseURL: "https://primary.example", APIKeys: []string{"primary"}, ServiceType: "responses", Status: "active", Priority: 1},
		{Name: "original", BaseURL: "https://original.example", APIKeys: []string{"original"}, ServiceType: "responses", Status: "active", Priority: 4},
	}}
	s, cleanup := createTestScheduler(t, cfg)
	defer cleanup()
	s.SetTraceAffinityForRequirement("user", 1, ChannelKindResponses, &ContextRequirement{InputTokens: 268126})
	for _, encrypted := range []bool{true, false} {
		got, err := s.SelectChannelWithOptions(context.Background(), SelectionOptions{
			UserID: "user", Kind: ChannelKindResponses, Model: "gpt-6.1-sol",
			ContextRequirement: &ContextRequirement{InputTokens: 278408}, PreserveTraceAffinity: encrypted,
		})
		if err != nil {
			t.Fatal(err)
		}
		want := 0
		if encrypted {
			want = 1
		}
		if got.ChannelIndex != want {
			t.Fatalf("encrypted=%v 跨上下文桶选择渠道 %d，期望 %d", encrypted, got.ChannelIndex, want)
		}
	}
}

func TestEncryptedResponsesAffinityRestoresSuccessfulChannel(t *testing.T) {
	for _, tc := range []struct {
		name    string
		prepare func(*config.Config, *SelectionOptions)
		want    int
	}{
		{"expired_cache", nil, 1},
		{"ordinary_request", func(_ *config.Config, o *SelectionOptions) { o.PreserveTraceAffinity = false }, 0},
		{"failed_source", func(_ *config.Config, o *SelectionOptions) { o.FailedChannels = map[int]bool{1: true} }, 0},
		{"unsupported_source", func(c *config.Config, _ *SelectionOptions) {
			c.ResponsesUpstream[1].SupportedModels = []string{"other"}
		}, 0},
		{"suspended_source", func(c *config.Config, _ *SelectionOptions) { c.ResponsesUpstream[1].Status = "suspended" }, 0},
		{"explicit_pin", func(_ *config.Config, o *SelectionOptions) { o.ChannelName = "primary" }, 0},
		{"reordered_source", func(c *config.Config, _ *SelectionOptions) {
			c.ResponsesUpstream[0], c.ResponsesUpstream[1] = c.ResponsesUpstream[1], c.ResponsesUpstream[0]
		}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Config{ResponsesUpstream: []config.UpstreamConfig{
				{Name: "primary", BaseURL: "https://primary.example", APIKeys: []string{"primary"}, ServiceType: "responses", Status: "active", Priority: 1},
				{Name: "original", BaseURL: "https://original.example", APIKeys: []string{"original"}, ServiceType: "responses", Status: "active", Priority: 4},
			}}
			opts := SelectionOptions{UserID: "user", Kind: ChannelKindResponses, Model: "gpt-6.1-sol", PreserveTraceAffinity: true, ContextRequirement: &ContextRequirement{InputTokens: 278408}}
			if tc.prepare != nil {
				tc.prepare(&cfg, &opts)
			}
			s, cleanup := createTestScheduler(t, cfg)
			defer cleanup()
			// 原始缓存已经过期，且上下文从 268k 跨过了 272k 桶边界。
			s.traceAffinity.Stop()
			s.traceAffinity = session.NewTraceAffinityManagerWithTTL(time.Nanosecond)
			defer s.traceAffinity.Stop()
			s.SetTraceAffinityForRequirement("user", 1, ChannelKindResponses, &ContextRequirement{InputTokens: 268126})
			time.Sleep(time.Millisecond)
			s.traceAffinity.Cleanup()
			if s.traceAffinity.Size() != 0 {
				t.Fatal("亲和缓存应已过期")
			}
			path := filepath.Join(t.TempDir(), "conversations.json")
			tracker := conversation.NewConversationTracker(time.Hour, 24*time.Hour, path)
			tracker.Track("responses", "user", "gpt-6.1-sol", 1, "original", "", "continue", 1, "main")
			// 失败或尚未完成的其他渠道请求不能改写来源。
			tracker.TrackWithStatus("responses", "user", "gpt-6.1-sol", 0, "primary", "", "continue", 1, "main", "streaming")
			tracker.Stop()
			restored := conversation.NewConversationTracker(time.Hour, 24*time.Hour, path)
			defer restored.Stop()
			s.SetConversationComponents(restored, nil)
			got, err := s.SelectChannelWithOptions(context.Background(), opts)
			if err != nil {
				t.Fatal(err)
			}
			if got.ChannelIndex != tc.want {
				t.Fatalf("恢复渠道 %d/%s，期望 %d", got.ChannelIndex, got.Reason, tc.want)
			}
		})
	}
}

func TestEncryptedResponsesAffinitySeparatesAgentRoles(t *testing.T) {
	cfg := config.Config{ResponsesUpstream: []config.UpstreamConfig{
		{Name: "primary", BaseURL: "https://primary.example", APIKeys: []string{"primary"}, ServiceType: "responses", Status: "active", Priority: 1},
		{Name: "subagent", BaseURL: "https://subagent.example", APIKeys: []string{"subagent"}, ServiceType: "responses", Status: "active", Priority: 4},
	}}
	s, cleanup := createTestScheduler(t, cfg)
	defer cleanup()
	tracker := conversation.NewConversationTracker(time.Hour, 24*time.Hour)
	defer tracker.Stop()
	tracker.Track("responses", "user", "gpt-6.1-sol", 0, "primary", "", "main", 1, "main")
	tracker.Track("responses", "user", "gpt-6.1-sol", 1, "subagent", "", "child", 1, "subagent")
	s.SetConversationComponents(tracker, nil)
	for _, role := range []string{"", "main", "subagent"} {
		got, err := s.SelectChannelWithOptions(context.Background(), SelectionOptions{
			UserID: "user", Kind: ChannelKindResponses, Model: "gpt-6.1-sol", PreserveTraceAffinity: true, AgentRole: role,
		})
		if err != nil {
			t.Fatal(err)
		}
		want := 0
		if role == "subagent" {
			want = 1
		}
		if got.ChannelIndex != want {
			t.Fatalf("role=%q 选择渠道 %d，期望 %d", role, got.ChannelIndex, want)
		}
	}
}
