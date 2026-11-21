package scheduler

import (
	"context"
	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/conversation"
	"testing"
	"time"
)

func subagentContinuationConfig() config.Config {
	return config.Config{ResponsesUpstream: []config.UpstreamConfig{
		{Name: "A", ChannelUID: "uid-a", BaseURL: "https://a.example", APIKeys: []string{"a"}, Status: "active", ModelMapping: map[string]string{"astra": "strong-actual", "sol": "cheap-actual"}},
		{Name: "B", ChannelUID: "uid-b", BaseURL: "https://b.example", APIKeys: []string{"b"}, Status: "active", ModelMapping: map[string]string{"astra": "strong-b", "sol": "cheap-b"}},
	}}
}
func TestSubagentRoutingEncryptedSecondTurnKeepsActualModel(t *testing.T) {
	s, close := createTestScheduler(t, subagentContinuationConfig())
	defer close()
	tracker := conversation.NewConversationTracker(time.Hour, 24*time.Hour)
	defer tracker.Stop()
	s.SetConversationTracker(tracker)
	p := &config.SubagentRoutingPolicy{Model: "sol", Channels: []config.SubagentRoutingChannel{{ChannelUID: "uid-a", Enabled: true}, {ChannelUID: "uid-b", Enabled: true}}}
	opts := SelectionOptions{UserID: "child", Kind: ChannelKindResponses, Model: "astra", AgentRole: "subagent", AgentConfidence: "exact", SubagentRouting: p, ApplySubagentRouting: true}
	first, err := s.SelectChannelWithOptions(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	firstModel := config.RedirectModel("astra", first.Upstream)
	tracker.Track("responses", "child", "astra", first.ChannelIndex, first.Upstream.Name, "", "hello", 1, "subagent")
	s.RecordSuccessfulResponse("child", "astra", "subagent", first.ChannelIndex, first.Upstream, firstModel, first.Upstream.BaseURL)
	opts.PreserveTraceAffinity = true
	second, err := s.SelectChannelWithOptions(context.Background(), opts)
	if err != nil {
		t.Fatal(err)
	}
	secondModel := config.RedirectModel("astra", second.Upstream)
	t.Logf("first=%s second=%s reason=%s", firstModel, secondModel, second.Reason)
	if secondModel != firstModel {
		t.Fatalf("encrypted continuation changed model: %s -> %s", firstModel, secondModel)
	}
}
func TestSubagentRoutingClosedEncryptedSourceFails(t *testing.T) {
	s, close := createTestScheduler(t, subagentContinuationConfig())
	defer close()
	tracker := conversation.NewConversationTracker(time.Hour, 24*time.Hour)
	defer tracker.Stop()
	tracker.Track("responses", "child", "astra", 0, "A", "", "hello", 1, "subagent")
	s.SetConversationTracker(tracker)
	source := s.ConfigSnapshot().ResponsesUpstream[0].Clone()
	source.SubagentModel = "sol"
	s.RecordSuccessfulResponse("child", "astra", "subagent", 0, source, "cheap-actual", source.BaseURL)
	_, err := s.SelectChannelWithOptions(context.Background(), SelectionOptions{UserID: "child", Kind: ChannelKindResponses, Model: "astra", AgentRole: "subagent", AgentConfidence: "exact", SubagentRouting: &config.SubagentRoutingPolicy{Model: "sol", Channels: []config.SubagentRoutingChannel{{ChannelUID: "uid-a", Enabled: false}, {ChannelUID: "uid-b", Enabled: true}}}, ApplySubagentRouting: true, PreserveTraceAffinity: true})
	if err == nil {
		t.Fatal("closed encrypted source silently failed over to B")
	}
}
func TestSubagentRoutingDeletionDuringSelectionUsesSnapshot(t *testing.T) {
	cfg := subagentContinuationConfig()
	cfg.ChatUpstream = cfg.ResponsesUpstream
	cfg.ChatUpstream[1].Status = "disabled"
	s, close := createTestScheduler(t, cfg)
	defer close()
	result, err := s.SelectChannelWithOptions(context.Background(), SelectionOptions{Kind: ChannelKindChat, Model: "astra", AgentRole: "subagent", AgentConfidence: "exact", SubagentRouting: &config.SubagentRoutingPolicy{Model: "sol", Channels: []config.SubagentRoutingChannel{{ChannelUID: "uid-a", Enabled: true}, {ChannelUID: "uid-b", Enabled: false}}}, ApplySubagentRouting: true, CandidateFilter: func(ch []ChannelInfo, _ func(ChannelInfo) *config.UpstreamConfig, _ func(ChannelInfo, *config.UpstreamConfig) bool) ([]ChannelInfo, error) {
		_, e := s.configManager.RemoveChatUpstream(0)
		return ch, e
	}})
	if err != nil {
		return
	}
	if result.Upstream.ChannelUID != "uid-a" {
		t.Fatalf("selected policy-disabled UID=%s name=%s", result.Upstream.ChannelUID, result.Upstream.Name)
	}
}
func TestSubagentRoutingFuzzyMappingMatchesExecution(t *testing.T) {
	s, close := createTestScheduler(t, subagentContinuationConfig())
	defer close()
	result, err := s.SelectChannelWithOptions(context.Background(), SelectionOptions{Kind: ChannelKindResponses, Model: "astra", AgentRole: "subagent", AgentConfidence: "exact", SubagentRouting: &config.SubagentRoutingPolicy{Model: "gpt-sol-2026", Channels: []config.SubagentRoutingChannel{{ChannelUID: "uid-a", Enabled: true}}}, ApplySubagentRouting: true})
	if err != nil {
		t.Fatal(err)
	}
	actual := config.RedirectModel("astra", result.Upstream)
	if actual != "cheap-actual" {
		t.Fatalf("preview/RedirectModel resolves cheap-actual, execution=%s", actual)
	}
}
