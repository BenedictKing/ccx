package autopilot

import "testing"

func TestClassifyModelRoutingIntent(t *testing.T) {
	tests := []struct {
		name         string
		channelKind  string
		requestModel string
		want         ModelRoutingIntent
	}{
		{name: "Claude alias", channelKind: "messages", requestModel: "sonnet", want: ModelRoutingIntentClaudeAdaptive},
		{name: "Claude Fable full model", channelKind: "messages", requestModel: "claude-fable-5", want: ModelRoutingIntentClaudeAdaptive},
		{name: "Claude dated full model", channelKind: "messages", requestModel: "CLAUDE-OPUS-4-8-20260713", want: ModelRoutingIntentClaudeAdaptive},
		{name: "Claude legacy full model", channelKind: "messages", requestModel: "claude-3-5-sonnet-latest", want: ModelRoutingIntentClaudeAdaptive},
		{name: "GPT 5.6 series", channelKind: "responses", requestModel: "gpt-5.6-terra", want: ModelRoutingIntentResponsesAdaptive},
		{name: "GPT 5.5 exact", channelKind: "responses", requestModel: " gpt-5.5 ", want: ModelRoutingIntentResponsesAdaptive},
		{name: "GPT 5.4 series", channelKind: "responses", requestModel: "gpt-5.4-mini", want: ModelRoutingIntentResponsesAdaptive},
		{name: "Codex review alias", channelKind: "responses", requestModel: "codex-auto-review", want: ModelRoutingIntentResponsesAdaptive},
		{name: "Explicit DeepSeek", channelKind: "messages", requestModel: "deepseek-chat", want: ModelRoutingIntentExactOnly},
		{name: "Explicit GLM", channelKind: "responses", requestModel: "glm-5.2", want: ModelRoutingIntentExactOnly},
		{name: "Explicit Qwen", channelKind: "chat", requestModel: "qwen3-coder", want: ModelRoutingIntentExactOnly},
		{name: "Claude on non-native protocol", channelKind: "chat", requestModel: "claude-sonnet-5", want: ModelRoutingIntentExactOnly},
		{name: "GPT on non-native protocol", channelKind: "messages", requestModel: "gpt-5.6-sol", want: ModelRoutingIntentExactOnly},
		{name: "Unlisted Claude tier", channelKind: "messages", requestModel: "claude-mythos-5", want: ModelRoutingIntentExactOnly},
		{name: "Unlisted GPT derivative", channelKind: "responses", requestModel: "gpt-5.5-codex", want: ModelRoutingIntentExactOnly},
		{name: "Unknown model", channelKind: "responses", requestModel: "vendor-model-x", want: ModelRoutingIntentExactOnly},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClassifyModelRoutingIntent(tt.channelKind, tt.requestModel); got != tt.want {
				t.Fatalf("ClassifyModelRoutingIntent(%q, %q) = %q, want %q",
					tt.channelKind, tt.requestModel, got, tt.want)
			}
		})
	}
}

func TestModelResolver_ExactOnlyRejectsCrossModelMapping(t *testing.T) {
	tests := []struct {
		name           string
		channelKind    string
		requestModel   string
		candidateModel string
		family         ModelFamily
	}{
		{name: "DeepSeek same family", channelKind: "messages", requestModel: "deepseek-chat", candidateModel: "deepseek-reasoner", family: ModelFamilyDeepSeek},
		{name: "GLM same family", channelKind: "messages", requestModel: "glm-5.2", candidateModel: "glm-5.1", family: ModelFamilyGLM},
		{name: "Qwen same family", channelKind: "responses", requestModel: "qwen3-coder", candidateModel: "qwen3-max", family: ModelFamilyQwen},
		{name: "Claude wrong protocol", channelKind: "chat", requestModel: "claude-sonnet-5", candidateModel: "glm-5.2", family: ModelFamilyGLM},
		{name: "Unlisted GPT derivative", channelKind: "responses", requestModel: "gpt-5.5-codex", candidateModel: "gpt-5.5", family: ModelFamilyOpenAI},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profile := routingPolicyProfile(tt.channelKind, tt.candidateModel, tt.family)
			resolver := newTestResolver(t, []ModelProfile{profile})

			target, resolved, reason := resolver.ResolveModel(
				tt.requestModel, "ch_test", tt.channelKind, "metrics_test", CapabilityFloor{})
			if resolved || target.Model != tt.requestModel || reason != "exact_model_required" {
				t.Fatalf("ResolveModel() = (%q, %v, %q), want (%q, false, exact_model_required)",
					target.Model, resolved, reason, tt.requestModel)
			}

			target, found, reason := resolver.ResolveModelAnyEndpoint(
				tt.requestModel, "ch_test", tt.channelKind)
			if found || target.Model != tt.requestModel || reason != "exact_model_required" {
				t.Fatalf("ResolveModelAnyEndpoint() = (%q, %v, %q), want (%q, false, exact_model_required)",
					target.Model, found, reason, tt.requestModel)
			}
		})
	}
}

func TestModelResolver_ExactOnlyFindsSameNormalizedModel(t *testing.T) {
	exact := routingPolicyProfile("chat", "DeepSeek-Chat", ModelFamilyDeepSeek)
	alternative := routingPolicyProfile("chat", "deepseek-reasoner", ModelFamilyDeepSeek)
	alternative.ProbeLatencyMs = 1
	resolver := newTestResolver(t, []ModelProfile{alternative, exact})

	target, resolved, reason := resolver.ResolveModel(
		" deepseek-chat ", "ch_test", "chat", "metrics_test", CapabilityFloor{})
	if !resolved || target.Model != "DeepSeek-Chat" || reason != "found_exact_model_in_profile" {
		t.Fatalf("ResolveModel() = (%q, %v, %q), want exact DeepSeek model", target.Model, resolved, reason)
	}

	target, found, reason := resolver.ResolveModelAnyEndpoint("deepseek-chat", "ch_test", "chat")
	if !found || target.Model != "DeepSeek-Chat" || reason != "found_exact_model_in_profile" {
		t.Fatalf("ResolveModelAnyEndpoint() = (%q, %v, %q), want exact DeepSeek model", target.Model, found, reason)
	}
}

func TestModelResolver_ExactOnlyAcceptsDocumentedCompatibilityAlias(t *testing.T) {
	flash := routingPolicyProfile("chat", "deepseek-v4-flash", ModelFamilyDeepSeek)
	pro := routingPolicyProfile("chat", "deepseek-v4-pro", ModelFamilyDeepSeek)
	pro.ProbeLatencyMs = 1
	resolver := newTestResolver(t, []ModelProfile{pro, flash})

	target, resolved, reason := resolver.ResolveModel(
		"deepseek-chat", "ch_test", "chat", "metrics_test", CapabilityFloor{})
	if !resolved || target.Model != "deepseek-v4-flash" || reason != "found_equivalent_model_in_profile" {
		t.Fatalf("ResolveModel() = (%q, %v, %q), want documented DeepSeek compatibility alias", target.Model, resolved, reason)
	}

	target, found, reason := resolver.ResolveModelAnyEndpoint("deepseek-chat", "ch_test", "chat")
	if !found || target.Model != "deepseek-v4-flash" || reason != "found_equivalent_model_in_profile" {
		t.Fatalf("ResolveModelAnyEndpoint() = (%q, %v, %q), want documented DeepSeek compatibility alias", target.Model, found, reason)
	}
}

func TestModelResolver_AdaptiveEntrypointsAllowSubstitution(t *testing.T) {
	tests := []struct {
		name           string
		channelKind    string
		requestModel   string
		candidateModel string
		family         ModelFamily
	}{
		{name: "Claude alias", channelKind: "messages", requestModel: "opus", candidateModel: "mimo-v2.5-pro", family: ModelFamilyMiMo},
		{name: "Claude full model", channelKind: "messages", requestModel: "claude-sonnet-5", candidateModel: "glm-5.2", family: ModelFamilyGLM},
		{name: "GPT 5.6", channelKind: "responses", requestModel: "gpt-5.6-sol", candidateModel: "mimo-v2.5-pro", family: ModelFamilyMiMo},
		{name: "GPT 5.4", channelKind: "responses", requestModel: "gpt-5.4-mini", candidateModel: "deepseek-chat", family: ModelFamilyDeepSeek},
		{name: "Codex auto review", channelKind: "responses", requestModel: "codex-auto-review", candidateModel: "glm-5.2", family: ModelFamilyGLM},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			profile := routingPolicyProfile(tt.channelKind, tt.candidateModel, tt.family)
			resolver := newTestResolver(t, []ModelProfile{profile})

			target, resolved, reason := resolver.ResolveModel(
				tt.requestModel, "ch_test", tt.channelKind, "metrics_test", CapabilityFloor{})
			if !resolved || target.Model != tt.candidateModel || reason == "" {
				t.Fatalf("ResolveModel() = (%q, %v, %q), want adaptive mapping to %q",
					target.Model, resolved, reason, tt.candidateModel)
			}

			target, found, reason := resolver.ResolveModelAnyEndpoint(
				tt.requestModel, "ch_test", tt.channelKind)
			if !found || target.Model != tt.candidateModel || reason == "" {
				t.Fatalf("ResolveModelAnyEndpoint() = (%q, %v, %q), want adaptive mapping to %q",
					target.Model, found, reason, tt.candidateModel)
			}
		})
	}
}

func routingPolicyProfile(channelKind, modelID string, family ModelFamily) ModelProfile {
	profile := makeModelProfile(modelID, family, QualityTierHigh, 1_000_000,
		true, true, true, true, 50)
	profile.ChannelKind = channelKind
	return profile
}
