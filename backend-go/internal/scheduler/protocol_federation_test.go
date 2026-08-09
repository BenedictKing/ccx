package scheduler

import (
	"context"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

func federationTestConfig() config.Config {
	return config.Config{
		AutopilotRouting: config.DefaultAutopilotRoutingConfig(),
		Upstream: []config.UpstreamConfig{{
			Name: "native", ChannelUID: "msg", AccountUID: "acct-a", AutoManaged: true,
			BaseURL: "https://messages.example.com", APIKeys: []string{"msg-key"}, Status: "active",
		}},
		ChatUpstream: []config.UpstreamConfig{
			{Name: "k3-chat", ChannelUID: "chat-k3", AccountUID: "acct-a", AutoManaged: true, BaseURL: "https://api.moonshot.cn", APIKeys: []string{"chat-key"}, Status: "active"},
			{Name: "other-account", ChannelUID: "chat-other", AccountUID: "acct-b", AutoManaged: true, BaseURL: "https://other.example.com", APIKeys: []string{"other-key"}, Status: "active"},
			{Name: "suspended", ChannelUID: "chat-suspended", AccountUID: "acct-a", AutoManaged: true, BaseURL: "https://suspended.example.com", APIKeys: []string{"suspended-key"}, Status: "suspended"},
			{Name: "disabled", ChannelUID: "chat-disabled", AccountUID: "acct-a", AutoManaged: true, BaseURL: "https://disabled.example.com", APIKeys: []string{"disabled-key"}, Status: "disabled"},
			{Name: "keyless", ChannelUID: "chat-keyless", AccountUID: "acct-a", AutoManaged: true, BaseURL: "https://keyless.example.com", Status: "active"},
		},
		ResponsesUpstream: []config.UpstreamConfig{{
			Name: "responses", ChannelUID: "responses-sib", AccountUID: "acct-a", AutoManaged: true,
			BaseURL: "https://responses.example.com", APIKeys: []string{"responses-key"}, Status: "active",
		}},
		GeminiUpstream: []config.UpstreamConfig{{
			Name: "gemini", ChannelUID: "gemini-sib", AccountUID: "acct-a", AutoManaged: true,
			BaseURL: "https://gemini.example.com", APIKeys: []string{"gemini-key"}, Status: "active",
		}},
	}
}

// federationTestConfigWithManual 在 federationTestConfig 基础上追加一个显式手动渠道。
// 显式 autoManaged=false 通过 JSON 保留，用于验证 protocol federation 不联邦化手动渠道。
func federationTestConfigWithManual() config.Config {
	cfg := federationTestConfig()
	// manual 渠道的 autoManaged=false 需经 JSON 显式落盘才能被识别为"显式手动"，
	// 此处先不放入 ChatUpstream，由 createTestSchedulerWithRawJSON 注入。
	return cfg
}

// federationTestRawJSON 构造包含显式 autoManaged:false 手动渠道的 JSON。
// 其余渠道与 federationTestConfig 保持一致。
func federationTestRawJSON() string {
	return `{
  "autopilot": {},
  "upstream": [
    {"name":"native","channelUid":"msg","accountUid":"acct-a","autoManaged":true,"baseUrl":"https://messages.example.com","apiKeys":["msg-key"],"status":"active"}
  ],
  "chatUpstream": [
    {"name":"k3-chat","channelUid":"chat-k3","accountUid":"acct-a","autoManaged":true,"baseUrl":"https://api.moonshot.cn","apiKeys":["chat-key"],"status":"active"},
    {"name":"other-account","channelUid":"chat-other","accountUid":"acct-b","autoManaged":true,"baseUrl":"https://other.example.com","apiKeys":["other-key"],"status":"active"},
    {"name":"suspended","channelUid":"chat-suspended","accountUid":"acct-a","autoManaged":true,"baseUrl":"https://suspended.example.com","apiKeys":["suspended-key"],"status":"suspended"},
    {"name":"disabled","channelUid":"chat-disabled","accountUid":"acct-a","autoManaged":true,"baseUrl":"https://disabled.example.com","apiKeys":["disabled-key"],"status":"disabled"},
    {"name":"keyless","channelUid":"chat-keyless","accountUid":"acct-a","autoManaged":true,"baseUrl":"https://keyless.example.com","status":"active"},
    {"name":"manual","channelUid":"chat-manual","accountUid":"acct-a","autoManaged":false,"baseUrl":"https://manual.example.com","apiKeys":["manual-key"],"status":"active"}
  ],
  "responsesUpstream": [
    {"name":"responses","channelUid":"responses-sib","accountUid":"acct-a","autoManaged":true,"baseUrl":"https://responses.example.com","apiKeys":["responses-key"],"status":"active"}
  ],
  "geminiUpstream": [
    {"name":"gemini","channelUid":"gemini-sib","accountUid":"acct-a","autoManaged":true,"baseUrl":"https://gemini.example.com","apiKeys":["gemini-key"],"status":"active"}
  ],
  "imagesUpstream": [],
  "vectorsUpstream": []
}`
}

func federationSiblingUIDs(t *testing.T, s *ChannelScheduler, accountUID string) map[string]string {
	t.Helper()
	got := make(map[string]string)
	for _, ch := range s.protocolFederationSiblings(accountUID) {
		upstream := s.getUpstreamByRoute(ch.Route)
		if upstream == nil {
			t.Fatalf("sibling route has no upstream: %#v", ch.Route)
		}
		got[upstream.ChannelUID] = ch.Route.Kind
	}
	return got
}

func TestProtocolFederationIncludesOnlyEligibleSiblings(t *testing.T) {
	s, cleanup := createTestSchedulerWithRawJSON(t, federationTestRawJSON())
	defer cleanup()

	got := federationSiblingUIDs(t, s, "acct-a")
	if got["chat-k3"] != string(ChannelKindChat) {
		t.Fatalf("same-account AutoManaged chat K3 not federated: %#v", got)
	}
	if got["responses-sib"] != string(ChannelKindResponses) {
		t.Fatalf("responses sibling not federated: %#v", got)
	}
	for _, excluded := range []string{"chat-other", "chat-suspended", "chat-disabled", "chat-keyless", "chat-manual", "gemini-sib"} {
		if _, ok := got[excluded]; ok {
			t.Fatalf("%s must stay excluded: %#v", excluded, got)
		}
	}
	if len(got) != 2 {
		t.Fatalf("federated siblings = %#v, want exactly chat-k3 + responses-sib", got)
	}
}

func TestProtocolFederationMarksConvertedFidelityAndPenalty(t *testing.T) {
	cfg := federationTestConfig()
	cfg.AutopilotRouting.ProtocolFederation.ConversionPenalty = 0.5
	s, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	for _, ch := range s.protocolFederationSiblings("acct-a") {
		if ch.ProtocolFidelity != "converted" || ch.ConversionPenalty != 0.5 {
			t.Fatalf("sibling missing conversion metadata: %#v", ch)
		}
	}
}

func TestProtocolFederationFeatureOffKeepsMessagesOnly(t *testing.T) {
	cfg := federationTestConfig()
	cfg.AutopilotRouting.ProtocolFederation.Enabled = false
	s, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	if got := s.protocolFederationSiblings("acct-a"); len(got) != 0 {
		t.Fatalf("feature-off siblings = %#v, want none", got)
	}
	native := s.federateDefaultCandidates(context.Background(), []ChannelInfo{{
		Route: channelRouteRef(ChannelKindMessages, 0, &config.UpstreamConfig{ChannelUID: "msg"}), Index: 0, Status: "active",
	}}, "claude-sonnet-5", nil, newSelectionTrace(SelectionOptions{Kind: ChannelKindMessages}))
	if len(native) != 1 || native[0].Route.Kind != string(ChannelKindMessages) {
		t.Fatalf("feature-off candidates = %#v, want messages-only", native)
	}
}

func newFederationResolver() ModelSupportResolverFunc {
	return func(_ context.Context, kind ChannelKind, upstream *config.UpstreamConfig, model string) (bool, string, string, string) {
		switch {
		case kind == ChannelKindChat && upstream.ChannelUID == "chat-k3":
			return true, "kimi-k3", "test", ""
		case kind == ChannelKindResponses:
			return true, "gpt-5.4", "test", ""
		default:
			return true, model, "test", ""
		}
	}
}

func pinKindFilter(kind ChannelKind) func(context.Context, []ChannelInfo) []ChannelInfo {
	return func(_ context.Context, channels []ChannelInfo) []ChannelInfo {
		for _, ch := range channels {
			if ch.Route.Kind == string(kind) {
				return []ChannelInfo{ch}
			}
		}
		return nil
	}
}

func TestProtocolFederationSelectsChatSiblingWithK3Model(t *testing.T) {
	s, cleanup := createTestScheduler(t, federationTestConfig())
	defer cleanup()
	s.SetModelSupportResolverProvider(newFederationResolver())

	result, err := s.SelectChannelWithOptions(context.Background(), SelectionOptions{
		Kind: ChannelKindMessages, Model: "claude-sonnet-5",
		SmartFilter: pinKindFilter(ChannelKindChat),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Route.Kind != string(ChannelKindChat) || result.Upstream.ChannelUID != "chat-k3" {
		t.Fatalf("selected route = %#v, want chat-k3 physical route", result.Route)
	}
	if result.CandidateCount != 1 {
		t.Fatalf("CandidateCount = %d, want filtered physical candidate count 1", result.CandidateCount)
	}
}

func TestProtocolFederationSelectsResponsesSibling(t *testing.T) {
	s, cleanup := createTestScheduler(t, federationTestConfig())
	defer cleanup()
	s.SetModelSupportResolverProvider(newFederationResolver())

	result, err := s.SelectChannelWithOptions(context.Background(), SelectionOptions{
		Kind: ChannelKindMessages, Model: "claude-sonnet-5",
		SmartFilter: pinKindFilter(ChannelKindResponses),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Route.Kind != string(ChannelKindResponses) || result.Upstream.ChannelUID != "responses-sib" {
		t.Fatalf("selected route = %#v, want responses sibling", result.Route)
	}
}

func TestProtocolFederationFailsOverAcrossKindsWithoutIndexCollision(t *testing.T) {
	s, cleanup := createTestSchedulerWithRawJSON(t, federationTestRawJSON())
	defer cleanup()
	s.SetModelSupportResolverProvider(newFederationResolver())

	keepMessagesAndChat := func(_ context.Context, channels []ChannelInfo) []ChannelInfo {
		filtered := make([]ChannelInfo, 0, 2)
		for _, ch := range channels {
			if ch.Route.Kind == string(ChannelKindChat) || ch.Route.Kind == string(ChannelKindMessages) {
				filtered = append(filtered, ch)
			}
		}
		return filtered
	}
	first, err := s.SelectChannelWithOptions(context.Background(), SelectionOptions{
		Kind: ChannelKindMessages, Model: "claude-sonnet-5", SmartFilter: keepMessagesAndChat,
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.CandidateCount != 2 {
		t.Fatalf("CandidateCount = %d, want 2 deduplicated physical candidates", first.CandidateCount)
	}

	// messages[0] 失败后同 index 的 chat[0] 仍必须可选，证明跨 kind 无裸 index 冲突。
	failed := map[ChannelRouteKey]bool{first.Route.Key(): true}
	second, err := s.SelectChannelWithOptions(context.Background(), SelectionOptions{
		Kind: ChannelKindMessages, Model: "claude-sonnet-5", SmartFilter: keepMessagesAndChat,
		FailedRoutes: failed,
	})
	if err != nil {
		t.Fatal(err)
	}
	if second.Route.Key() == first.Route.Key() {
		t.Fatalf("second attempt reused failed route: %#v", second.Route)
	}
	if second.Route.Index != first.Route.Index {
		t.Fatalf("expected same bare index across kinds, got %#v vs %#v", first.Route, second.Route)
	}
}

func TestProtocolFederationExcludesSiblingWithoutModelSupport(t *testing.T) {
	s, cleanup := createTestScheduler(t, federationTestConfig())
	defer cleanup()
	s.SetModelSupportResolverProvider(func(_ context.Context, kind ChannelKind, _ *config.UpstreamConfig, model string) (bool, string, string, string) {
		if kind == ChannelKindMessages {
			return true, model, "test", ""
		}
		return false, "", "test", "model unavailable on sibling protocol"
	})

	federated := s.federateDefaultCandidates(context.Background(), []ChannelInfo{{
		Route: channelRouteRef(ChannelKindMessages, 0, &config.UpstreamConfig{ChannelUID: "msg"}), Index: 0, Status: "active",
	}}, "claude-sonnet-5", nil, newSelectionTrace(SelectionOptions{Kind: ChannelKindMessages}))
	if len(federated) != 1 {
		t.Fatalf("unsupported siblings must be excluded before scoring: %#v", federated)
	}
}

func TestProtocolFederationPreservesExplicitChannelPin(t *testing.T) {
	s, cleanup := createTestScheduler(t, federationTestConfig())
	defer cleanup()
	s.SetModelSupportResolverProvider(newFederationResolver())

	result, err := s.SelectChannelWithOptions(context.Background(), SelectionOptions{
		Kind: ChannelKindMessages, Model: "claude-sonnet-5", ChannelName: "native",
		SmartFilter: pinKindFilter(ChannelKindChat),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Reason != "channel_pin" || result.Route.Kind != string(ChannelKindMessages) {
		t.Fatalf("explicit pin must win over federation: %#v", result)
	}
}
