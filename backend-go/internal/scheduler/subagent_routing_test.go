package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/conversation"
)

func TestSubagentRoutingUsesIndependentUIDOrderAndStatus(t *testing.T) {
	cfg := config.Config{ChatUpstream: []config.UpstreamConfig{
		{Name: "ordinary-disabled", ChannelUID: "uid-a", BaseURL: "https://a.example", APIKeys: []string{"a"}, Status: "disabled", Priority: 0, ModelMapping: map[string]string{"sol": "actual-a"}},
		{Name: "subagent-first", ChannelUID: "uid-b", BaseURL: "https://b.example", APIKeys: []string{"b"}, Status: "active", Priority: 2, ModelMapping: map[string]string{"sol": "actual-b"}},
		{Name: "main-first", ChannelUID: "uid-c", BaseURL: "https://c.example", APIKeys: []string{"c"}, Status: "active", Priority: 1},
	}}
	s, cleanup := createTestScheduler(t, cfg)
	defer cleanup()
	policy := &config.SubagentRoutingPolicy{Model: "sol", Channels: []config.SubagentRoutingChannel{
		{ChannelUID: "uid-b", Enabled: true}, {ChannelUID: "uid-a", Enabled: true}, {ChannelUID: "uid-c", Enabled: false},
	}}
	result, err := s.SelectChannelWithOptions(context.Background(), SelectionOptions{
		Kind: ChannelKindChat, Model: "astra", AgentRole: "subagent", AgentConfidence: "exact",
		SubagentRouting: policy, ApplySubagentRouting: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ChannelIndex != 1 || config.RedirectModel("astra", result.Upstream) != "actual-b" {
		t.Fatalf("子代理应使用独立顺序和映射，实际 channel=%d upstream=%+v", result.ChannelIndex, result.Upstream)
	}
	result, err = s.SelectChannelWithOptions(context.Background(), SelectionOptions{
		Kind: ChannelKindChat, Model: "astra", AgentRole: "subagent", AgentConfidence: "exact",
		SubagentRouting: policy, ApplySubagentRouting: true, FailedChannels: map[int]bool{1: true},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ChannelIndex != 0 || result.Upstream.Status != "disabled" {
		t.Fatalf("普通 disabled 不应排除子代理渠道，实际 %+v", result)
	}
	main, err := s.SelectChannelWithOptions(context.Background(), SelectionOptions{
		Kind: ChannelKindChat, Model: "astra", AgentRole: "main", SubagentRouting: policy,
	})
	if err != nil {
		t.Fatal(err)
	}
	if main.ChannelIndex != 2 {
		t.Fatalf("主代理应继续使用普通渠道，实际 %d", main.ChannelIndex)
	}
}

func TestSubagentRoutingRequiresExactRole(t *testing.T) {
	cfg := config.Config{
		ChatUpstream:    []config.UpstreamConfig{{Name: "main", ChannelUID: "uid-a", BaseURL: "https://a.example", APIKeys: []string{"a"}, Status: "active", SubagentModel: "legacy"}},
		SubagentRouting: map[string]config.SubagentRoutingPolicy{"chat": {Model: "sol", Channels: []config.SubagentRoutingChannel{{ChannelUID: "uid-a", Enabled: true}}}},
	}
	s, cleanup := createTestScheduler(t, cfg)
	defer cleanup()
	policy, apply := s.ResolveSubagentRouting(ChannelKindChat, "subagent", "heuristic")
	if policy == nil || apply {
		t.Fatal("启发式角色不得应用独立模型策略")
	}
	result, err := s.SelectChannelWithOptions(context.Background(), SelectionOptions{
		Kind: ChannelKindChat, Model: "astra", AgentRole: "subagent", AgentConfidence: "heuristic", SubagentRouting: policy, ApplySubagentRouting: apply,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Upstream.SubagentModel != "" || config.RedirectModel("astra", result.Upstream) != "astra" {
		t.Fatalf("激活策略后启发式请求不应再读取旧模型覆盖: %+v", result.Upstream)
	}
}

func TestSubagentRoutingRejectsEmptyPool(t *testing.T) {
	cfg := config.Config{ChatUpstream: []config.UpstreamConfig{{Name: "main", ChannelUID: "uid-a", BaseURL: "https://a.example", APIKeys: []string{"a"}, Status: "active"}}}
	s, cleanup := createTestScheduler(t, cfg)
	defer cleanup()
	_, err := s.SelectChannelWithOptions(context.Background(), SelectionOptions{
		Kind: ChannelKindChat, Model: "astra", AgentRole: "subagent", AgentConfidence: "exact",
		SubagentRouting: &config.SubagentRoutingPolicy{Model: "sol", Channels: []config.SubagentRoutingChannel{{ChannelUID: "uid-a", Enabled: false}}}, ApplySubagentRouting: true,
	})
	if err == nil {
		t.Fatal("全部子代理渠道关闭时应明确失败")
	}
}

func TestEncryptedContinuationKeepsRequestModelOnSuccessfulChannel(t *testing.T) {
	cfg := config.Config{ResponsesUpstream: []config.UpstreamConfig{
		{Name: "new", ChannelUID: "uid-a", BaseURL: "https://a.example", APIKeys: []string{"a"}, Status: "active", Priority: 0},
		{Name: "source", ChannelUID: "uid-b", BaseURL: "https://b.example", APIKeys: []string{"b"}, Status: "active", Priority: 1, ModelMapping: map[string]string{"astra": "original-actual", "sol": "new-actual"}},
	}}
	s, cleanup := createTestScheduler(t, cfg)
	defer cleanup()
	tracker := conversation.NewConversationTracker(time.Hour, 24*time.Hour)
	defer tracker.Stop()
	tracker.Track("responses", "user", "astra", 1, "source", "", "child", 1, "subagent")
	s.SetConversationComponents(tracker, nil)
	policy := &config.SubagentRoutingPolicy{Model: "sol", Channels: []config.SubagentRoutingChannel{{ChannelUID: "uid-a", Enabled: true}, {ChannelUID: "uid-b", Enabled: true}}}
	result, err := s.SelectChannelWithOptions(context.Background(), SelectionOptions{
		UserID: "user", Kind: ChannelKindResponses, Model: "astra", AgentRole: "subagent", AgentConfidence: "exact",
		SubagentRouting: policy, ApplySubagentRouting: true, PreserveTraceAffinity: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.ChannelIndex != 1 || config.RedirectModel("astra", result.Upstream) != "original-actual" {
		t.Fatalf("加密续聊应保持成功来源和原模型，实际 channel=%d model=%s", result.ChannelIndex, config.RedirectModel("astra", result.Upstream))
	}
}
