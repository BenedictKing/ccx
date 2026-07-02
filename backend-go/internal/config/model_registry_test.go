package config

import "testing"

func TestResolveAgentModelProfile_CodexBuiltins(t *testing.T) {
	profile := ResolveAgentModelProfile("gpt-5.4", nil)
	if !profile.Known {
		t.Fatal("expected built-in gpt-5.4 profile")
	}
	if profile.Profile.ContextWindowTokens != 272000 {
		t.Fatalf("ContextWindowTokens = %d, want 272000", profile.Profile.ContextWindowTokens)
	}
	if profile.Profile.MaxContextWindowTokens != 1000000 {
		t.Fatalf("MaxContextWindowTokens = %d, want 1000000", profile.Profile.MaxContextWindowTokens)
	}
	if profile.Profile.TruncationMode != "tokens" {
		t.Fatalf("TruncationMode = %q, want tokens", profile.Profile.TruncationMode)
	}
}

func TestResolveAgentModelProfile_GPT56BedrockBuiltins(t *testing.T) {
	profile := ResolveAgentModelProfile("gpt-5.6-sol", nil)
	if !profile.Known {
		t.Fatal("expected built-in gpt-5.6-sol profile")
	}
	if profile.Profile.ContextWindowTokens != 272000 {
		t.Fatalf("ContextWindowTokens = %d, want 272000", profile.Profile.ContextWindowTokens)
	}
	if profile.Profile.MaxContextWindowTokens != 272000 {
		t.Fatalf("MaxContextWindowTokens = %d, want 272000", profile.Profile.MaxContextWindowTokens)
	}
	if !containsString(profile.Profile.ReasoningEfforts, "max") {
		t.Fatalf("ReasoningEfforts = %v, want max", profile.Profile.ReasoningEfforts)
	}
}

func TestResolveAgentModelProfile_ClaudeBuiltins(t *testing.T) {
	profile := ResolveAgentModelProfile("claude-sonnet-4-6", nil)
	if !profile.Known {
		t.Fatal("expected built-in claude-sonnet-4-6 profile")
	}
	if profile.Profile.ContextWindowTokens != 1000000 {
		t.Fatalf("ContextWindowTokens = %d, want 1000000", profile.Profile.ContextWindowTokens)
	}
	if profile.Profile.MaxOutputTokens != 64000 {
		t.Fatalf("MaxOutputTokens = %d, want 64000", profile.Profile.MaxOutputTokens)
	}

	alias := ResolveAgentModelProfile("sonnet", nil)
	if !alias.Known {
		t.Fatal("expected built-in sonnet alias profile")
	}
	if alias.Profile.ContextWindowTokens != 1000000 {
		t.Fatalf("alias ContextWindowTokens = %d, want 1000000", alias.Profile.ContextWindowTokens)
	}
}

func TestResolveAgentModelProfile_GlobalOverrideWins(t *testing.T) {
	profile := ResolveAgentModelProfile("gpt-5.4", map[string]AgentModelProfile{
		"gpt-5.4": {ContextWindowTokens: 512000},
	})
	if !profile.Known || profile.Source != "global" {
		t.Fatalf("profile source = %q known=%v, want global known", profile.Source, profile.Known)
	}
	if profile.Profile.ContextWindowTokens != 512000 {
		t.Fatalf("ContextWindowTokens = %d, want 512000", profile.Profile.ContextWindowTokens)
	}
}

func TestResolveUpstreamCapability_UsesActualModelAfterMapping(t *testing.T) {
	upstream := &UpstreamConfig{
		ModelMapping: map[string]string{
			"agent": "claude-sonnet-4-6",
		},
	}

	resolved := ResolveUpstreamCapability("agent", upstream, nil)
	if !resolved.Known {
		t.Fatal("expected built-in upstream capability")
	}
	if resolved.ActualModel != "claude-sonnet-4-6" {
		t.Fatalf("ActualModel = %q, want claude-sonnet-4-6", resolved.ActualModel)
	}
	if resolved.Capability.ContextWindowTokens != 1000000 {
		t.Fatalf("ContextWindowTokens = %d, want 1000000", resolved.Capability.ContextWindowTokens)
	}
	if resolved.Capability.MaxOutputTokens != 64000 {
		t.Fatalf("MaxOutputTokens = %d, want 64000", resolved.Capability.MaxOutputTokens)
	}
}

func TestResolveUpstreamCapability_ChannelOverrideWins(t *testing.T) {
	upstream := &UpstreamConfig{
		ModelMapping: map[string]string{"agent": "claude-sonnet-4-6"},
		ModelCapabilities: map[string]UpstreamModelCapability{
			"claude-sonnet-4-6": {ContextWindowTokens: 200000, MaxOutputTokens: 32000},
		},
	}

	resolved := ResolveUpstreamCapability("agent", upstream, map[string]UpstreamModelCapability{
		"claude-sonnet-4-6": {ContextWindowTokens: 500000},
	})
	if resolved.Source != "channel" {
		t.Fatalf("source = %q, want channel", resolved.Source)
	}
	if resolved.Capability.ContextWindowTokens != 200000 {
		t.Fatalf("ContextWindowTokens = %d, want 200000", resolved.Capability.ContextWindowTokens)
	}
}

func TestResolveUpstreamCapability_KimiK27Builtin(t *testing.T) {
	upstream := &UpstreamConfig{
		ModelMapping: map[string]string{
			"agent": "Kimi-K2.7-Code-HighSpeed",
		},
	}

	resolved := ResolveUpstreamCapability("agent", upstream, nil)
	if !resolved.Known || resolved.Source != "builtin" {
		t.Fatalf("source = %q known=%v, want builtin known", resolved.Source, resolved.Known)
	}
	if resolved.Capability.ContextWindowTokens != 262144 {
		t.Fatalf("ContextWindowTokens = %d, want 262144", resolved.Capability.ContextWindowTokens)
	}
	if resolved.Capability.MaxOutputTokens != 32768 {
		t.Fatalf("MaxOutputTokens = %d, want 32768", resolved.Capability.MaxOutputTokens)
	}
	if resolved.Capability.DefaultOutputTokens != 32768 {
		t.Fatalf("DefaultOutputTokens = %d, want 32768", resolved.Capability.DefaultOutputTokens)
	}
	if resolved.Capability.RecommendedOutputTokens != 32768 {
		t.Fatalf("RecommendedOutputTokens = %d, want 32768", resolved.Capability.RecommendedOutputTokens)
	}
	if resolved.Capability.ThinkingMode != "thinking" {
		t.Fatalf("ThinkingMode = %q, want thinking", resolved.Capability.ThinkingMode)
	}
	if resolved.Capability.Pricing == nil || resolved.Capability.Pricing.OutputPrice == nil || *resolved.Capability.Pricing.OutputPrice != 4 {
		t.Fatalf("Pricing.OutputPrice = %#v, want 4", resolved.Capability.Pricing)
	}
}

func TestResolveUpstreamCapability_Qwen37MaxBuiltin(t *testing.T) {
	upstream := &UpstreamConfig{
		ModelMapping: map[string]string{
			"agent": "qwen3.7-max-2026-05-20",
		},
	}

	resolved := ResolveUpstreamCapability("agent", upstream, nil)
	if !resolved.Known || resolved.Source != "builtin" {
		t.Fatalf("source = %q known=%v, want builtin known", resolved.Source, resolved.Known)
	}
	if resolved.Capability.ContextWindowTokens != 991808 {
		t.Fatalf("ContextWindowTokens = %d, want 991808", resolved.Capability.ContextWindowTokens)
	}
	if resolved.Capability.MaxOutputTokens != 65536 {
		t.Fatalf("MaxOutputTokens = %d, want 65536", resolved.Capability.MaxOutputTokens)
	}
	if resolved.Capability.ThinkingMode != "thinking" {
		t.Fatalf("ThinkingMode = %q, want thinking", resolved.Capability.ThinkingMode)
	}
	pricing := resolved.Capability.Pricing
	if pricing == nil {
		t.Fatal("Pricing = nil, want qwen3.7-max pricing")
	}
	assertFloatPointerValue(t, pricing.InputCacheHitPrice, 2.4, "Pricing.InputCacheHitPrice")
	assertFloatPointerValue(t, pricing.InputCacheMissPrice, 12, "Pricing.InputCacheMissPrice")
	assertFloatPointerValue(t, pricing.OutputPrice, 36, "Pricing.OutputPrice")
}

func TestResolveUpstreamCapability_LongCat20Builtin(t *testing.T) {
	upstream := &UpstreamConfig{
		ModelMapping: map[string]string{
			"agent": "LongCat-2.0",
		},
	}

	resolved := ResolveUpstreamCapability("agent", upstream, nil)
	if !resolved.Known || resolved.Source != "builtin" {
		t.Fatalf("source = %q known=%v, want builtin known", resolved.Source, resolved.Known)
	}
	if resolved.Capability.Provider != "longcat" {
		t.Fatalf("Provider = %q, want longcat", resolved.Capability.Provider)
	}
	pricing := resolved.Capability.Pricing
	if pricing == nil {
		t.Fatal("Pricing = nil, want LongCat-2.0 pricing")
	}
	if pricing.Currency != "CNY" {
		t.Fatalf("Pricing.Currency = %q, want CNY", pricing.Currency)
	}
	assertFloatPointerValue(t, pricing.InputCacheHitPrice, 0.04, "Pricing.InputCacheHitPrice")
	assertFloatPointerValue(t, pricing.InputCacheMissPrice, 2, "Pricing.InputCacheMissPrice")
	assertFloatPointerValue(t, pricing.OutputPrice, 8, "Pricing.OutputPrice")
}

func TestResolveUpstreamCapability_Step37FlashBuiltin(t *testing.T) {
	upstream := &UpstreamConfig{
		ModelMapping: map[string]string{
			"agent": "step-3.7-flash",
		},
	}

	resolved := ResolveUpstreamCapability("agent", upstream, nil)
	if !resolved.Known || resolved.Source != "builtin" {
		t.Fatalf("source = %q known=%v, want builtin known", resolved.Source, resolved.Known)
	}
	if resolved.Capability.Provider != "stepfun" {
		t.Fatalf("Provider = %q, want stepfun", resolved.Capability.Provider)
	}
	if resolved.Capability.ContextWindowTokens != 262144 {
		t.Fatalf("ContextWindowTokens = %d, want 262144", resolved.Capability.ContextWindowTokens)
	}
	if resolved.Capability.ThinkingMode != "thinking" {
		t.Fatalf("ThinkingMode = %q, want thinking", resolved.Capability.ThinkingMode)
	}
	if !containsString(resolved.Capability.ReasoningEfforts, "medium") {
		t.Fatalf("ReasoningEfforts = %v, want medium", resolved.Capability.ReasoningEfforts)
	}
	if !resolved.Capability.Capabilities["vision"] {
		t.Fatal("step-3.7-flash should advertise vision")
	}
	if !resolved.Capability.Capabilities["videoInput"] {
		t.Fatal("step-3.7-flash should advertise videoInput")
	}
	if !resolved.Capability.Capabilities["toolCalls"] {
		t.Fatal("step-3.7-flash should advertise toolCalls")
	}
	pricing := resolved.Capability.Pricing
	if pricing == nil {
		t.Fatal("Pricing = nil, want step-3.7-flash pricing")
	}
	assertFloatPointerValue(t, pricing.InputCacheHitPrice, 0.04, "Pricing.InputCacheHitPrice")
	assertFloatPointerValue(t, pricing.InputCacheMissPrice, 0.2, "Pricing.InputCacheMissPrice")
	assertFloatPointerValue(t, pricing.OutputPrice, 1.15, "Pricing.OutputPrice")
}

func TestResolveUpstreamCapability_GPT56BedrockBuiltin(t *testing.T) {
	upstream := &UpstreamConfig{
		ModelMapping: map[string]string{
			"agent": "gpt-5.6-terra",
		},
	}

	resolved := ResolveUpstreamCapability("agent", upstream, nil)
	if !resolved.Known || resolved.Source != "builtin" {
		t.Fatalf("source = %q known=%v, want builtin known", resolved.Source, resolved.Known)
	}
	if resolved.Capability.Provider != "amazon-bedrock" {
		t.Fatalf("Provider = %q, want amazon-bedrock", resolved.Capability.Provider)
	}
	if resolved.Capability.ContextWindowTokens != 272000 {
		t.Fatalf("ContextWindowTokens = %d, want 272000", resolved.Capability.ContextWindowTokens)
	}
	if resolved.Capability.MaxOutputTokens != 128000 {
		t.Fatalf("MaxOutputTokens = %d, want 128000", resolved.Capability.MaxOutputTokens)
	}
	if !containsString(resolved.Capability.ReasoningEfforts, "max") {
		t.Fatalf("ReasoningEfforts = %v, want max", resolved.Capability.ReasoningEfforts)
	}
}

func TestResolveUpstreamCapability_Qwen37PlusTieredPricing(t *testing.T) {
	upstream := &UpstreamConfig{
		ModelMapping: map[string]string{
			"agent": "qwen3.7-plus-2026-05-26",
		},
	}

	resolved := ResolveUpstreamCapability("agent", upstream, nil)
	if !resolved.Known || resolved.Source != "builtin" {
		t.Fatalf("source = %q known=%v, want builtin known", resolved.Source, resolved.Known)
	}
	if resolved.Capability.ContextWindowTokens != 991808 {
		t.Fatalf("ContextWindowTokens = %d, want 991808", resolved.Capability.ContextWindowTokens)
	}
	if resolved.Capability.MaxOutputTokens != 65536 {
		t.Fatalf("MaxOutputTokens = %d, want 65536", resolved.Capability.MaxOutputTokens)
	}
	if !resolved.Capability.Capabilities["vision"] {
		t.Fatalf("Capabilities[vision] = false, want true")
	}
	pricing := resolved.Capability.Pricing
	if pricing == nil {
		t.Fatal("Pricing = nil, want qwen3.7-plus pricing")
	}
	if pricing.Currency != "CNY" {
		t.Fatalf("Pricing.Currency = %q, want CNY", pricing.Currency)
	}
	assertFloatPointerValue(t, pricing.InputCacheHitPrice, 0.4, "Pricing.InputCacheHitPrice")
	assertFloatPointerValue(t, pricing.InputCacheMissPrice, 2, "Pricing.InputCacheMissPrice")
	assertFloatPointerValue(t, pricing.OutputPrice, 8, "Pricing.OutputPrice")
	if len(pricing.Tiers) != 2 {
		t.Fatalf("len(Pricing.Tiers) = %d, want 2", len(pricing.Tiers))
	}

	firstTier := pricing.Tiers[0]
	if firstTier.InputTokensAbove != 0 || firstTier.InputTokensUpTo != 262144 {
		t.Fatalf("first tier bounds = (%d, %d), want (0, 262144)", firstTier.InputTokensAbove, firstTier.InputTokensUpTo)
	}
	assertFloatPointerValue(t, firstTier.InputCacheHitPrice, 0.4, "Pricing.Tiers[0].InputCacheHitPrice")
	assertFloatPointerValue(t, firstTier.InputCacheMissPrice, 2, "Pricing.Tiers[0].InputCacheMissPrice")
	assertFloatPointerValue(t, firstTier.OutputPrice, 8, "Pricing.Tiers[0].OutputPrice")

	secondTier := pricing.Tiers[1]
	if secondTier.InputTokensAbove != 262144 || secondTier.InputTokensUpTo != 1048576 {
		t.Fatalf("second tier bounds = (%d, %d), want (262144, 1048576)", secondTier.InputTokensAbove, secondTier.InputTokensUpTo)
	}
	assertFloatPointerValue(t, secondTier.InputCacheHitPrice, 1.2, "Pricing.Tiers[1].InputCacheHitPrice")
	assertFloatPointerValue(t, secondTier.InputCacheMissPrice, 6, "Pricing.Tiers[1].InputCacheMissPrice")
	assertFloatPointerValue(t, secondTier.OutputPrice, 24, "Pricing.Tiers[1].OutputPrice")
}

func TestResolveUpstreamCapability_MimoVisionCapabilities(t *testing.T) {
	upstream := &UpstreamConfig{}

	pro := ResolveUpstreamCapability("mimo-v2.5-pro", upstream, nil)
	if !pro.Known || pro.Source != "builtin" {
		t.Fatalf("pro source = %q known=%v, want builtin known", pro.Source, pro.Known)
	}
	if pro.Capability.ContextWindowTokens != 1048576 {
		t.Fatalf("pro ContextWindowTokens = %d, want 1048576", pro.Capability.ContextWindowTokens)
	}
	if pro.Capability.MaxOutputTokens != 131072 {
		t.Fatalf("pro MaxOutputTokens = %d, want 131072", pro.Capability.MaxOutputTokens)
	}
	if pro.Capability.Capabilities["vision"] {
		t.Fatal("mimo-v2.5-pro should not advertise vision")
	}

	multimodal := ResolveUpstreamCapability("mimo-v2.5", upstream, nil)
	if !multimodal.Known || multimodal.Source != "builtin" {
		t.Fatalf("multimodal source = %q known=%v, want builtin known", multimodal.Source, multimodal.Known)
	}
	if !multimodal.Capability.Capabilities["vision"] {
		t.Fatal("mimo-v2.5 should advertise vision")
	}
	if !multimodal.Capability.Capabilities["videoInput"] {
		t.Fatal("mimo-v2.5 should advertise videoInput")
	}
	if !multimodal.Capability.Capabilities["audioInput"] {
		t.Fatal("mimo-v2.5 should advertise audioInput")
	}
}

func TestResolveUpstreamCapability_RequestModelFallback(t *testing.T) {
	upstream := &UpstreamConfig{
		ModelMapping: map[string]string{"agent-1m": "vendor-hidden-model"},
	}

	resolved := ResolveUpstreamCapability("agent-1m", upstream, map[string]UpstreamModelCapability{
		"agent-*": {ContextWindowTokens: 1000000},
	})
	if !resolved.Known || resolved.Source != "global" {
		t.Fatalf("source = %q known=%v, want global known", resolved.Source, resolved.Known)
	}
	if resolved.MatchedPattern != "agent-*" {
		t.Fatalf("MatchedPattern = %q, want agent-*", resolved.MatchedPattern)
	}
	if resolved.Capability.ContextWindowTokens != 1000000 {
		t.Fatalf("ContextWindowTokens = %d, want 1000000", resolved.Capability.ContextWindowTokens)
	}
}

func assertFloatPointerValue(t *testing.T, got *float64, want float64, name string) {
	t.Helper()
	if got == nil {
		t.Fatalf("%s = nil, want %v", name, want)
	}
	if *got != want {
		t.Fatalf("%s = %v, want %v", name, *got, want)
	}
}

func containsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
