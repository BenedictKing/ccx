package config

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/presetstore"
)

func TestResolveAgentModelProfile_CodexBuiltins(t *testing.T) {
	profile := ResolveAgentModelProfile("gpt-5.4", nil)
	if !profile.Known {
		t.Fatal("expected built-in gpt-5.4 profile")
	}
	if profile.Profile.ContextWindowTokens != 272000 {
		t.Fatalf("ContextWindowTokens = %d, want 272000", profile.Profile.ContextWindowTokens)
	}
	if profile.Profile.MaxContextWindowTokens != 1050000 {
		t.Fatalf("MaxContextWindowTokens = %d, want 1050000", profile.Profile.MaxContextWindowTokens)
	}
	if profile.Profile.TruncationMode != "tokens" {
		t.Fatalf("TruncationMode = %q, want tokens", profile.Profile.TruncationMode)
	}
}

func TestResolveAgentModelProfile_GPT56BedrockBuiltins(t *testing.T) {
	for _, model := range []string{"gpt-5.6", "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna"} {
		t.Run(model, func(t *testing.T) {
			profile := ResolveAgentModelProfile(model, nil)
			if !profile.Known {
				t.Fatalf("expected built-in %s profile", model)
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
		})
	}
}

func TestResolveAgentModelProfile_GPT55UsesLiteLLMMaximumContext(t *testing.T) {
	profile := ResolveAgentModelProfile("gpt-5.5", nil)
	if !profile.Known {
		t.Fatal("expected built-in gpt-5.5 profile")
	}
	if profile.Profile.ContextWindowTokens != 272000 {
		t.Fatalf("ContextWindowTokens = %d, want conservative routing minimum 272000", profile.Profile.ContextWindowTokens)
	}
	if profile.Profile.MaxContextWindowTokens != 1050000 {
		t.Fatalf("MaxContextWindowTokens = %d, want 1050000", profile.Profile.MaxContextWindowTokens)
	}
}

func TestResolveAgentModelProfile_LiteLLMGPTVariants(t *testing.T) {
	tests := []struct {
		model      string
		context    int
		maxContext int
		maxOutput  int
		xhigh      bool
		none       bool
	}{
		{"gpt-5.2-2025-12-11", 272000, 272000, 128000, true, true},
		{"gpt-5.2-chat-latest", 128000, 128000, 16384, false, false},
		{"gpt-5.2-pro-2025-12-11", 272000, 272000, 128000, true, false},
		{"gpt-5.2-codex", 272000, 272000, 128000, true, false},
		{"gpt-5.3-codex", 272000, 272000, 128000, false, false},
		{"gpt-5.3-chat-latest", 128000, 128000, 16384, false, false},
		{"gpt-5.4-2026-03-05", 272000, 1050000, 128000, true, true},
		{"gpt-5.4-pro-2026-03-05", 272000, 1050000, 128000, true, false},
		{"gpt-5.4-mini-2026-03-17", 272000, 272000, 128000, true, true},
		{"gpt-5.4-nano-2026-03-17", 272000, 272000, 128000, true, true},
		{"gpt-5.5-2026-04-23", 272000, 1050000, 128000, true, true},
		{"gpt-5.5-pro-2026-04-23", 272000, 1050000, 128000, true, false},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			resolved := ResolveAgentModelProfile(tt.model, nil)
			if !resolved.Known || resolved.Source != "builtin" {
				t.Fatalf("resolved = %+v, want builtin profile", resolved)
			}
			profile := resolved.Profile
			if profile.ContextWindowTokens != tt.context || profile.MaxContextWindowTokens != tt.maxContext || profile.MaxOutputTokens != tt.maxOutput {
				t.Fatalf("profile = %+v, want context=%d maxContext=%d maxOutput=%d", profile, tt.context, tt.maxContext, tt.maxOutput)
			}
			if got := containsString(profile.ReasoningEfforts, "xhigh"); got != tt.xhigh {
				t.Fatalf("xhigh = %v, want %v; efforts=%v", got, tt.xhigh, profile.ReasoningEfforts)
			}
			if got := containsString(profile.ReasoningEfforts, "none"); got != tt.none {
				t.Fatalf("none = %v, want %v; efforts=%v", got, tt.none, profile.ReasoningEfforts)
			}
		})
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

func TestResolveAgentModelProfile_KimiCodeBuiltins(t *testing.T) {
	tests := []struct {
		model      string
		context    int
		maxContext int
		efforts    []string
	}{
		{model: "k3", context: 262144, maxContext: 1048576, efforts: []string{"low", "high", "max"}},
		{model: "k3[1m]", context: 1048576, maxContext: 1048576, efforts: []string{"low", "high", "max"}},
		{model: "kimi-for-coding", context: 262144, maxContext: 0},
		{model: "kimi-for-coding-highspeed", context: 262144, maxContext: 0},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			resolved := ResolveAgentModelProfile(tt.model, nil)
			if !resolved.Known || resolved.Source != "builtin" {
				t.Fatalf("resolved = %+v, want builtin profile", resolved)
			}
			if resolved.Profile.ContextWindowTokens != tt.context || resolved.Profile.MaxContextWindowTokens != tt.maxContext {
				t.Fatalf("profile = %+v, want context=%d maxContext=%d", resolved.Profile, tt.context, tt.maxContext)
			}
			if !slices.Equal(resolved.Profile.ReasoningEfforts, tt.efforts) {
				t.Fatalf("ReasoningEfforts = %v, want %v", resolved.Profile.ReasoningEfforts, tt.efforts)
			}
		})
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

func TestResolveUpstreamCapability_KimiCodeModels(t *testing.T) {
	tests := []struct {
		model        string
		context      int
		maxOutput    int
		reasoning    []string
		displayName  string
		wantThinking string
	}{
		{model: "k3", context: 1048576, reasoning: []string{"low", "high", "max"}, displayName: "Kimi K3", wantThinking: "thinking"},
		{model: "k3[1m]", context: 1048576, reasoning: []string{"low", "high", "max"}, displayName: "Kimi K3 (1M)", wantThinking: "thinking"},
		// kimi-k3 是官方 API 模型 ID（非 Kimi Code CLI 的 k3/k3[1m] 别名），文档确认为 1M 上下文
		// （platform.kimi.ai/docs/overview 等多处来源一致）；不是"256K 版 k3"的同义词。
		{model: "kimi-k3", context: 1048576, reasoning: []string{"low", "high", "max"}, displayName: "Kimi K3 (1M)", wantThinking: "thinking"},
		{model: "kimi-for-coding", context: 262144, maxOutput: 32768, reasoning: []string{"high"}, displayName: "Kimi K2.7 Code (Kimi Code 会员)", wantThinking: "thinking"},
		{model: "kimi-for-coding-highspeed", context: 262144, maxOutput: 32768, reasoning: []string{"high"}, displayName: "Kimi K2.7 Code HighSpeed", wantThinking: "thinking"},
	}
	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			resolved := ResolveUpstreamCapability(tt.model, nil, nil)
			if !resolved.Known || resolved.Source != "builtin" {
				t.Fatalf("resolved = %+v, want builtin capability", resolved)
			}
			if resolved.Capability.ContextWindowTokens != tt.context {
				t.Fatalf("ContextWindowTokens = %d, want %d", resolved.Capability.ContextWindowTokens, tt.context)
			}
			if resolved.Capability.MaxOutputTokens != tt.maxOutput {
				t.Fatalf("MaxOutputTokens = %d, want %d", resolved.Capability.MaxOutputTokens, tt.maxOutput)
			}
			if resolved.Capability.DisplayName != tt.displayName {
				t.Fatalf("DisplayName = %q, want %q", resolved.Capability.DisplayName, tt.displayName)
			}
			if resolved.Capability.ThinkingMode != tt.wantThinking {
				t.Fatalf("ThinkingMode = %q, want %q", resolved.Capability.ThinkingMode, tt.wantThinking)
			}
			if len(resolved.Capability.ReasoningEfforts) != len(tt.reasoning) {
				t.Fatalf("ReasoningEfforts = %v, want %v", resolved.Capability.ReasoningEfforts, tt.reasoning)
			}
			for i, effort := range tt.reasoning {
				if resolved.Capability.ReasoningEfforts[i] != effort {
					t.Fatalf("ReasoningEfforts = %v, want %v", resolved.Capability.ReasoningEfforts, tt.reasoning)
				}
			}
		})
	}
}

func TestResolveUpstreamCapability_NewAugust2026Models(t *testing.T) {
	tests := []struct {
		model       string
		provider    string
		context     int
		maxOutput   int
		vision      bool
		toolCalls   bool
		inputPrice  float64
		outputPrice float64
	}{
		{model: "grok-4.6", provider: "xai", context: 500000, vision: true, toolCalls: true, inputPrice: 2, outputPrice: 6},
		{model: "xai/grok-4.6", provider: "xai", context: 500000, vision: true, toolCalls: true, inputPrice: 2, outputPrice: 6},
		{model: "glm-5.3", provider: "zai", context: 1000000, maxOutput: 131072, toolCalls: true},
		{model: "glm-5.3[1m]", provider: "zai", context: 1000000, maxOutput: 131072, toolCalls: true},
		{model: "gemini-3.7-flash", provider: "google", context: 1048576, maxOutput: 65536, vision: true, toolCalls: true, inputPrice: 0.75, outputPrice: 3.75},
		{model: "qwen3.8-max", provider: "dashscope", context: 1000000, maxOutput: 131072, vision: true, toolCalls: true, inputPrice: 12, outputPrice: 36},
		{model: "qwen3.8-max-preview", provider: "dashscope", context: 1000000, maxOutput: 131072, vision: true, toolCalls: true, inputPrice: 12, outputPrice: 36},
		{model: "doubao-seed-2.1-turbo", provider: "volcengine", context: 256000, maxOutput: 256000, vision: true, toolCalls: true},
		{model: "doubao-seed-evolving", provider: "volcengine", context: 1024000, maxOutput: 256000, toolCalls: true},
		{model: "doubao-seed-2.0-mini", provider: "volcengine", context: 256000, maxOutput: 128000, vision: true, toolCalls: true},
		{model: "glm-5.2-200k", provider: "atomgit", context: 200000, maxOutput: 131072, toolCalls: true},
		{model: "atomgit/glm-5.2-200k", provider: "atomgit", context: 200000, maxOutput: 131072, toolCalls: true},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			resolved := ResolveUpstreamCapability(tt.model, nil, nil)
			if !resolved.Known || resolved.Source != "builtin" {
				t.Fatalf("resolved = %+v, want builtin capability", resolved)
			}
			capability := resolved.Capability
			if capability.Provider != tt.provider || capability.ContextWindowTokens != tt.context || capability.MaxOutputTokens != tt.maxOutput {
				t.Fatalf("capability = %+v, want provider=%s context=%d maxOutput=%d", capability, tt.provider, tt.context, tt.maxOutput)
			}
			if capability.Capabilities["vision"] != tt.vision || capability.Capabilities["toolCalls"] != tt.toolCalls {
				t.Fatalf("Capabilities = %v, want vision=%v toolCalls=%v", capability.Capabilities, tt.vision, tt.toolCalls)
			}
			if tt.inputPrice > 0 {
				if capability.Pricing == nil {
					t.Fatal("Pricing = nil")
				}
				assertFloatPointerValue(t, capability.Pricing.InputCacheMissPrice, tt.inputPrice, "Pricing.InputCacheMissPrice")
				assertFloatPointerValue(t, capability.Pricing.OutputPrice, tt.outputPrice, "Pricing.OutputPrice")
			}
		})
	}
}

func TestResolveUpstreamCapability_Grok46PricingTiers(t *testing.T) {
	resolved := ResolveUpstreamCapability("grok-4.6", nil, nil)
	if !resolved.Known || resolved.Source != "builtin" {
		t.Fatalf("resolved = %+v, want builtin capability", resolved)
	}
	capability := resolved.Capability
	if capability.MaxOutputTokens != 0 {
		t.Fatalf("MaxOutputTokens = %d, want 0 because xAI documents no text output limit", capability.MaxOutputTokens)
	}
	if capability.ThinkingMode != "adaptive" || !containsString(capability.ReasoningEfforts, "xhigh") {
		t.Fatalf("thinkingMode=%q reasoningEfforts=%v, want adaptive with xhigh", capability.ThinkingMode, capability.ReasoningEfforts)
	}
	if !capability.Capabilities["structuredOutput"] || !capability.Capabilities["webSearch"] || !capability.Capabilities["codeExecution"] {
		t.Fatalf("Capabilities = %v, want structured output, web search, and code execution", capability.Capabilities)
	}
	pricing := capability.Pricing
	if pricing == nil || len(pricing.Tiers) != 2 {
		t.Fatalf("Pricing = %+v, want two context tiers", pricing)
	}
	short := pricing.Tiers[0]
	if short.InputTokensAbove != 0 || short.InputTokensUpTo != 199999 {
		t.Fatalf("short tier bounds = (%d, %d), want (0, 199999)", short.InputTokensAbove, short.InputTokensUpTo)
	}
	assertFloatPointerValue(t, short.InputCacheHitPrice, 0.5, "Pricing.Tiers[0].InputCacheHitPrice")
	assertFloatPointerValue(t, short.InputCacheMissPrice, 2, "Pricing.Tiers[0].InputCacheMissPrice")
	assertFloatPointerValue(t, short.OutputPrice, 6, "Pricing.Tiers[0].OutputPrice")
	long := pricing.Tiers[1]
	if long.InputTokensAbove != 199999 || long.InputTokensUpTo != 500000 {
		t.Fatalf("long tier bounds = (%d, %d), want (199999, 500000)", long.InputTokensAbove, long.InputTokensUpTo)
	}
	assertFloatPointerValue(t, long.InputCacheHitPrice, 1, "Pricing.Tiers[1].InputCacheHitPrice")
	assertFloatPointerValue(t, long.InputCacheMissPrice, 4, "Pricing.Tiers[1].InputCacheMissPrice")
	assertFloatPointerValue(t, long.OutputPrice, 12, "Pricing.Tiers[1].OutputPrice")
}

func TestResolveUpstreamCapability_GLM52RuntimeBuiltin(t *testing.T) {
	resolved := ResolveUpstreamCapability("glm-5.2", nil, nil)
	if !resolved.Known || resolved.Source != "builtin" {
		t.Fatalf("resolved = %+v, want builtin known", resolved)
	}
	if resolved.Capability.Provider != "zai" {
		t.Fatalf("Provider = %q, want zai", resolved.Capability.Provider)
	}
	if resolved.Capability.ContextWindowTokens != 1048576 {
		t.Fatalf("ContextWindowTokens = %d, want 1048576", resolved.Capability.ContextWindowTokens)
	}
	if resolved.Capability.MaxOutputTokens != 131072 {
		t.Fatalf("MaxOutputTokens = %d, want 131072", resolved.Capability.MaxOutputTokens)
	}
	if !containsString(resolved.Capability.ReasoningEfforts, "minimal") {
		t.Fatalf("ReasoningEfforts = %v, want minimal", resolved.Capability.ReasoningEfforts)
	}
	if !resolved.Capability.Capabilities["streamingToolCalls"] {
		t.Fatalf("Capabilities = %v, want streamingToolCalls", resolved.Capability.Capabilities)
	}
}

func TestResolveUpstreamCapability_GLM52AtomGit200k(t *testing.T) {
	// AtomGit 托管的 glm-5.2-200k 是 GLM-5.2 家族的 200K 上下文变体，provider 为 atomgit，
	// 区别于 zai 官方 1M 条目；裸名、命名空间前缀与日期后缀变体都必须归一到该条目。
	tests := []string{
		"glm-5.2-200k",
		"atomgit/glm-5.2-200k",
		"glm-5.2-200k-0813",
		"glm-5.2-200k-2026-08-13",
		"glm-5.2-200k-260813",
		"atomgit/GLM-5.2-200K-0813",
	}
	for _, model := range tests {
		t.Run(model, func(t *testing.T) {
			resolved := ResolveUpstreamCapability(model, nil, nil)
			if !resolved.Known || resolved.Source != "builtin" {
				t.Fatalf("resolved = %+v, want builtin known", resolved)
			}
			capability := resolved.Capability
			if capability.Provider != "atomgit" {
				t.Fatalf("Provider = %q, want atomgit", capability.Provider)
			}
			if capability.ContextWindowTokens != 200000 {
				t.Fatalf("ContextWindowTokens = %d, want 200000", capability.ContextWindowTokens)
			}
			if capability.MaxOutputTokens != 131072 {
				t.Fatalf("MaxOutputTokens = %d, want 131072", capability.MaxOutputTokens)
			}
			if !capability.Capabilities["reasoning"] || !capability.Capabilities["toolCalls"] {
				t.Fatalf("Capabilities = %v, want reasoning and toolCalls", capability.Capabilities)
			}
		})
	}
}

func TestResolveUpstreamCapability_GLM52BareNotAtomGit(t *testing.T) {
	// 裸 glm-5.2（无 -200k 后缀）仍归 zai 官方 1M 条目，不能被 atomgit 200K 变体误吞。
	resolved := ResolveUpstreamCapability("glm-5.2", nil, nil)
	if !resolved.Known || resolved.Capability.Provider != "zai" {
		t.Fatalf("resolved = %+v, want zai glm-5.2", resolved)
	}
	if resolved.Capability.ContextWindowTokens != 1048576 {
		t.Fatalf("ContextWindowTokens = %d, want 1048576 for bare glm-5.2", resolved.Capability.ContextWindowTokens)
	}
}

func TestResolveUpstreamCapability_DeepSeekV4DatedSuffixes(t *testing.T) {
	// 部分渠道以日期后缀形式发布新模型（DeepSeek 惯用 -MMDD，如 deepseek-v3-0324），
	// 这些变体必须归一到同一内置能力条目，否则定价/上下文窗口会丢失。
	tests := []struct {
		name                string
		models              []string
		inputCacheMissPrice float64
		outputPrice         float64
	}{
		{
			name: "flash",
			models: []string{
				"deepseek-v4-flash",
				"deepseek-v4-flash-0731",
				"deepseek-v4-flash-2026-07-31",
				"deepseek-v4-flash-250731",
				"deepseek-v4-flash-20260731",
				"deepseek-ai/deepseek-v4-flash-0731",
			},
			inputCacheMissPrice: 1,
			outputPrice:         2,
		},
		{
			name: "pro",
			models: []string{
				"deepseek-v4-pro",
				"DeepSeek-V4-Pro-0813",
				"deepseek-v4-pro-2026-08-13",
				"deepseek-v4-pro-260813",
				"deepseek-v4-pro-20260813",
				"deepseek-ai/DeepSeek-V4-Pro-0813",
			},
			inputCacheMissPrice: 3,
			outputPrice:         6,
		},
	}
	for _, tt := range tests {
		for _, model := range tt.models {
			resolved := ResolveUpstreamCapability(model, nil, nil)
			if !resolved.Known || resolved.Source != "builtin" {
				t.Fatalf("%s: resolved = %+v, want builtin known", model, resolved)
			}
			if resolved.Capability.Provider != "deepseek" {
				t.Fatalf("%s: Provider = %q, want deepseek", model, resolved.Capability.Provider)
			}
			if resolved.Capability.ContextWindowTokens != 1000000 {
				t.Fatalf("%s: ContextWindowTokens = %d, want 1000000", model, resolved.Capability.ContextWindowTokens)
			}
			pricing := resolved.Capability.Pricing
			if pricing == nil {
				t.Fatalf("%s: Pricing = nil, want deepseek-v4-%s pricing", model, tt.name)
			}
			assertFloatPointerValue(t, pricing.InputCacheMissPrice, tt.inputCacheMissPrice, model+" Pricing.InputCacheMissPrice")
			assertFloatPointerValue(t, pricing.OutputPrice, tt.outputPrice, model+" Pricing.OutputPrice")
		}
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
	for _, capability := range []string{"vision", "toolCalls"} {
		if !resolved.Capability.Capabilities[capability] {
			t.Fatalf("Capabilities[%q] = false, want true", capability)
		}
	}
}

func TestResolveUpstreamCapability_ClaudeOpusAliasesAndOpus5(t *testing.T) {
	tests := []struct {
		model       string
		displayName string
	}{
		{model: "claude-opus-4-8", displayName: "Claude Opus 4.8"},
		{model: "claude-opus-4.8", displayName: "Claude Opus 4.8"},
		{model: "claude-opus-5", displayName: "Claude Opus 5"},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			resolved := ResolveUpstreamCapability(tt.model, nil, nil)
			if !resolved.Known || resolved.Source != "builtin" {
				t.Fatalf("resolved = %+v, want builtin capability", resolved)
			}
			capability := resolved.Capability
			if capability.DisplayName != tt.displayName || capability.ContextWindowTokens != 1_000_000 ||
				capability.MaxOutputTokens != 128_000 || !capability.Capabilities["reasoning"] ||
				!capability.Capabilities["vision"] || !capability.Capabilities["toolCalls"] {
				t.Fatalf("capability = %+v", capability)
			}
		})
	}
}

func TestResolveUpstreamCapability_MultimodalAgentModels(t *testing.T) {
	for _, model := range []string{
		"k3",
		"minimax-m3",
		"mimo-v2.5",
		"gpt-5.4",
		"gpt-5.5",
		"gpt-5.6",
		"gpt-5.6-sol",
		"gpt-5.6-terra",
		"gpt-5.6-luna",
	} {
		t.Run(model, func(t *testing.T) {
			resolved := ResolveUpstreamCapability(model, nil, nil)
			if !resolved.Known || resolved.Source != "builtin" {
				t.Fatalf("resolved = %+v, want builtin capability", resolved)
			}
			for _, capability := range []string{"vision", "toolCalls"} {
				if !resolved.Capability.Capabilities[capability] {
					t.Fatalf("Capabilities[%q] = false, want true", capability)
				}
			}
		})
	}
}

func TestResolveUpstreamCapability_GPT55LiteLLMDefaults(t *testing.T) {
	resolved := ResolveUpstreamCapability("gpt-5.5", nil, nil)
	if !resolved.Known || resolved.Source != "builtin" {
		t.Fatalf("resolved = %+v, want builtin capability", resolved)
	}
	if resolved.Capability.ContextWindowTokens != 1050000 {
		t.Fatalf("ContextWindowTokens = %d, want 1050000", resolved.Capability.ContextWindowTokens)
	}
	if resolved.Capability.MaxOutputTokens != 128000 {
		t.Fatalf("MaxOutputTokens = %d, want 128000", resolved.Capability.MaxOutputTokens)
	}
	for _, capability := range []string{"vision", "toolCalls", "jsonMode"} {
		if !resolved.Capability.Capabilities[capability] {
			t.Fatalf("Capabilities[%q] = false, want true", capability)
		}
	}
}

func TestResolveUpstreamCapability_LiteLLMGPTVariants(t *testing.T) {
	tests := []struct {
		model     string
		context   int
		maxOutput int
		xhigh     bool
		none      bool
		jsonMode  bool
	}{
		{"gpt-5.2-2025-12-11", 272000, 128000, true, true, true},
		{"gpt-5.2-chat-latest", 128000, 16384, false, false, true},
		{"gpt-5.2-pro-2025-12-11", 272000, 128000, true, false, true},
		{"gpt-5.2-codex", 272000, 128000, true, false, true},
		{"gpt-5.3-codex", 272000, 128000, false, false, true},
		{"gpt-5.3-chat-latest", 128000, 16384, false, false, true},
		{"gpt-5.4-2026-03-05", 1050000, 128000, true, true, true},
		{"gpt-5.4-pro-2026-03-05", 1050000, 128000, true, false, false},
		{"gpt-5.4-mini-2026-03-17", 272000, 128000, true, true, true},
		{"gpt-5.4-nano-2026-03-17", 272000, 128000, true, true, true},
		{"gpt-5.5-2026-04-23", 1050000, 128000, true, true, true},
		{"gpt-5.5-pro-2026-04-23", 1050000, 128000, true, false, false},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			resolved := ResolveUpstreamCapability(tt.model, nil, nil)
			if !resolved.Known || resolved.Source != "builtin" {
				t.Fatalf("resolved = %+v, want builtin capability", resolved)
			}
			capability := resolved.Capability
			if capability.ContextWindowTokens != tt.context || capability.MaxOutputTokens != tt.maxOutput {
				t.Fatalf("capability = %+v, want context=%d maxOutput=%d", capability, tt.context, tt.maxOutput)
			}
			if got := containsString(capability.ReasoningEfforts, "xhigh"); got != tt.xhigh {
				t.Fatalf("xhigh = %v, want %v; efforts=%v", got, tt.xhigh, capability.ReasoningEfforts)
			}
			if got := containsString(capability.ReasoningEfforts, "none"); got != tt.none {
				t.Fatalf("none = %v, want %v; efforts=%v", got, tt.none, capability.ReasoningEfforts)
			}
			if got, exists := capability.Capabilities["jsonMode"]; !exists || got != tt.jsonMode {
				t.Fatalf("jsonMode = %v exists=%v, want %v", got, exists, tt.jsonMode)
			}
		})
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

func TestResolveUpstreamCapability_RuntimeRegistryOverride(t *testing.T) {
	store := presetstore.Default()
	original := store.Get()
	store.Swap(&presetstore.PresetBundle{
		SchemaVersion: original.SchemaVersion,
		DataVersion:   "runtime-test-1",
		Subscription:  original.Subscription,
		ModelRegistry: &presetstore.ModelRegistryPreset{
			SchemaVersion: 1,
			UpstreamCapabilities: []presetstore.ModelRegistryCapabilityPreset{{
				Patterns:            []string{`(?:^|[-/])qwen3\.7-plus(?:-\d{4}-\d{2}-\d{2}|-\d{6,8})?(?=$|@)`},
				ContextWindowTokens: 123456,
				Pricing: &presetstore.ModelPricingPreset{Tiers: []presetstore.ModelPricingTierPreset{{
					InputTokensUpTo: 42,
				}}},
			}},
		},
	})
	defer store.Swap(original)

	resolved := ResolveUpstreamCapability("qwen3.7-plus-2026-05-26", nil, nil)
	if !resolved.Known || resolved.Source != "builtin" {
		t.Fatalf("source = %q known=%v, want builtin known", resolved.Source, resolved.Known)
	}
	if resolved.Capability.ContextWindowTokens != 123456 {
		t.Fatalf("ContextWindowTokens = %d, want 123456", resolved.Capability.ContextWindowTokens)
	}
	if resolved.Capability.Pricing == nil || len(resolved.Capability.Pricing.Tiers) != 1 {
		t.Fatalf("Pricing.Tiers len = %d, want 1", len(resolved.Capability.Pricing.Tiers))
	}
	if resolved.Capability.Pricing.Tiers[0].InputTokensUpTo != 42 {
		t.Fatalf("InputTokensUpTo = %d, want 42", resolved.Capability.Pricing.Tiers[0].InputTokensUpTo)
	}
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

func TestResolveMappedUpstreamCapability_DoesNotFallbackToRequestModel(t *testing.T) {
	upstream := &UpstreamConfig{
		ModelCapabilities: map[string]UpstreamModelCapability{
			"claude-fable-5": {ContextWindowTokens: 1_000_000},
			"gpt-5.6-sol":    {ContextWindowTokens: 272_000},
		},
	}

	resolved := ResolveMappedUpstreamCapability("claude-fable-5", "gpt-5.6-sol", upstream, nil)
	if !resolved.Known || resolved.ActualModel != "gpt-5.6-sol" {
		t.Fatalf("resolved = %+v, want mapped gpt-5.6-sol capability", resolved)
	}
	if resolved.Capability.ContextWindowTokens != 272_000 {
		t.Fatalf("ContextWindowTokens = %d, want 272000", resolved.Capability.ContextWindowTokens)
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

func TestBuiltinUpstreamModelCapabilities_ReturnsDeepCopy(t *testing.T) {
	caps := BuiltinUpstreamModelCapabilities()
	entry := caps[`(?:^|[-/])qwen3\.7-plus(?:-\d{4}-\d{2}-\d{2}|-\d{6,8})?(?=$|@)`]
	if entry.Pricing == nil || len(entry.Pricing.Tiers) == 0 {
		t.Fatal("expected qwen3.7-plus pricing tiers")
	}
	entry.Pricing.Tiers[0].InputTokensUpTo = 1
	caps[`(?:^|[-/])qwen3\.7-plus(?:-\d{4}-\d{2}-\d{2}|-\d{6,8})?(?=$|@)`] = entry

	again := BuiltinUpstreamModelCapabilities()
	againEntry := again[`(?:^|[-/])qwen3\.7-plus(?:-\d{4}-\d{2}-\d{2}|-\d{6,8})?(?=$|@)`]
	if againEntry.Pricing.Tiers[0].InputTokensUpTo != 262144 {
		t.Fatalf("InputTokensUpTo = %d, want 262144", againEntry.Pricing.Tiers[0].InputTokensUpTo)
	}
}

func TestResolveUpstreamCapability_BuiltinResultIsDeepCopy(t *testing.T) {
	resolved := ResolveUpstreamCapability("mimo-v2.5", nil, nil)
	if !resolved.Known || !resolved.Capability.Capabilities["vision"] {
		t.Fatalf("resolved = %+v, want vision capability", resolved)
	}
	resolved.Capability.Capabilities["vision"] = false
	resolved.Capability.ReasoningEfforts[0] = "mutated"

	again := ResolveUpstreamCapability("mimo-v2.5", nil, nil)
	if !again.Capability.Capabilities["vision"] {
		t.Fatal("修改解析结果不应污染内置能力快照")
	}
	if len(again.Capability.ReasoningEfforts) == 0 || again.Capability.ReasoningEfforts[0] == "mutated" {
		t.Fatalf("ReasoningEfforts 未深拷贝: %v", again.Capability.ReasoningEfforts)
	}
}

func TestCurrentBuiltinSnapshot_RebuildsAfterSetDefault(t *testing.T) {
	original := presetstore.Default()
	defer presetstore.SetDefault(original)

	first := presetstore.NewPresetStore(presetstore.EmbeddedBundle())
	presetstore.SetDefault(first)
	_ = BuiltinUpstreamModelCapabilities()

	secondBundle := presetstore.EmbeddedBundle()
	secondBundle.DataVersion = "same"
	secondBundle.ModelRegistry = &presetstore.ModelRegistryPreset{
		SchemaVersion: 1,
		UpstreamCapabilities: []presetstore.ModelRegistryCapabilityPreset{{
			Patterns:            []string{`(?:^|[-/])custom-runtime-model(?=$|@)`},
			ContextWindowTokens: 777,
		}},
	}
	second := presetstore.NewPresetStore(secondBundle)
	presetstore.SetDefault(second)

	resolved := ResolveUpstreamCapability("custom-runtime-model", nil, nil)
	if !resolved.Known || resolved.Capability.ContextWindowTokens != 777 {
		t.Fatalf("resolved = %+v, want runtime rebuilt capability", resolved)
	}
}

func TestCurrentBuiltinSnapshot_IgnoresOlderCacheMissingK3(t *testing.T) {
	original := presetstore.Default()
	defer func() {
		presetstore.SetDefault(original)
		_ = BuiltinUpstreamModelCapabilities()
	}()

	stale := presetstore.EmbeddedBundle()
	stale.DataVersion = "v0.0.1+19700101"
	stale.ModelRegistry.UpstreamCapabilities = slices.DeleteFunc(
		stale.ModelRegistry.UpstreamCapabilities,
		func(entry presetstore.ModelRegistryCapabilityPreset) bool {
			return slices.ContainsFunc(entry.Patterns, func(pattern string) bool {
				return strings.Contains(strings.ToLower(pattern), "k3")
			})
		},
	)
	cacheDir := t.TempDir()
	if err := presetstore.SaveCache(cacheDir, stale); err != nil {
		t.Fatalf("SaveCache() error = %v", err)
	}

	store := presetstore.NewPresetStore(nil)
	updater := presetstore.NewPresetUpdater(store, presetstore.UpdaterConfig{CacheDir: cacheDir})
	if err := updater.LoadCacheAtStartup(); err != nil {
		t.Fatalf("LoadCacheAtStartup() error = %v", err)
	}
	presetstore.SetDefault(store)

	resolved := ResolveUpstreamCapability("k3", nil, nil)
	if !resolved.Known || resolved.Source != "builtin" ||
		!resolved.Capability.Capabilities["vision"] ||
		!resolved.Capability.Capabilities["toolCalls"] ||
		resolved.Capability.ContextWindowTokens != 1048576 {
		t.Fatalf("resolved = %+v, want current embedded K3 capabilities", resolved)
	}
}

func TestResolveModelBenchmarkProfile_DistinguishesGPT56Variants(t *testing.T) {
	tests := []struct {
		model     string
		canonical string
	}{
		{model: "claude-opus-4-8-20260713", canonical: "claude-opus-4-8"},
		{model: "gpt-5.6-terra", canonical: "gpt-5.6-terra"},
		{model: "gpt-5.6-sol", canonical: "gpt-5.6-sol"},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			resolved := ResolveModelBenchmarkProfile(tt.model)
			if !resolved.Known || resolved.Source != "builtin" {
				t.Fatalf("resolved = %+v, want builtin benchmark", resolved)
			}
			if resolved.Profile.CanonicalModel != tt.canonical {
				t.Fatalf("CanonicalModel = %q, want %q", resolved.Profile.CanonicalModel, tt.canonical)
			}
			for _, category := range []string{"coding", "math"} {
				score, ok := resolved.Profile.CategoryScores[category]
				if !ok {
					t.Fatalf("CategoryScores 缺少 %q: %+v", category, resolved.Profile.CategoryScores)
				}
				if score <= 0 || score > 100 {
					t.Fatalf("%s score = %v, want (0,100]", category, score)
				}
			}
			// 评测内容和采集日期会随 registry 刷新，只验证元数据完整性。
			if resolved.Profile.Lane != "provisional" || resolved.Profile.VerifiedAt == "" {
				t.Fatalf("evidence metadata = lane %q date %q", resolved.Profile.Lane, resolved.Profile.VerifiedAt)
			}
			if len(resolved.Profile.BenchmarkEvidence) == 0 {
				t.Fatal("BenchmarkEvidence 为空")
			}
			for _, ev := range resolved.Profile.BenchmarkEvidence {
				if ev.Benchmark == "" || ev.Domain == "" || ev.Metric == "" || ev.SourceURL == "" || ev.CapturedAt == "" {
					t.Fatalf("benchmark evidence 元数据不完整: %+v", ev)
				}
			}
		})
	}

	luna := ResolveModelBenchmarkProfile("gpt-5.6-luna")
	if !luna.Known || luna.Profile.CanonicalModel != "gpt-5.6-luna" {
		t.Fatalf("Luna 应有独立基准证据: %+v", luna)
	}
	// 证据顺序随 registry 刷新脚本变动，不能假设固定下标；按 benchmark 名查找并校验元数据与取值范围。
	deepswe := findLunaEvidence(luna.Profile.BenchmarkEvidence, "deepswe")
	codexradar := findLunaEvidence(luna.Profile.BenchmarkEvidence, "codexradar")
	if deepswe == nil || codexradar == nil {
		t.Fatalf("Luna 缺少 deepswe/codexradar 证据: %+v", luna.Profile.BenchmarkEvidence)
	}
	for _, ev := range []ModelBenchmarkEvidence{*deepswe, *codexradar} {
		if ev.Benchmark == "" || ev.Domain == "" || ev.Metric == "" || ev.SourceURL == "" || ev.CapturedAt == "" {
			t.Fatalf("Luna %q 证据元数据不完整: %+v", ev.Benchmark, ev)
		}
		if ev.RawValue <= 0 || ev.RawValue > 1 {
			t.Fatalf("Luna %q pass_at_1 = %v, want (0,1]", ev.Benchmark, ev.RawValue)
		}
	}
}

// findLunaEvidence 按 benchmark 名查找证据，返回 nil 表示不存在。
func findLunaEvidence(evs []ModelBenchmarkEvidence, benchmark string) *ModelBenchmarkEvidence {
	for i := range evs {
		if evs[i].Benchmark == benchmark {
			return &evs[i]
		}
	}
	return nil
}

func TestResolveModelBenchmarkProfile_RuntimeRegistryOverride(t *testing.T) {
	store := presetstore.Default()
	original := store.Get()
	store.Swap(&presetstore.PresetBundle{
		SchemaVersion: original.SchemaVersion,
		DataVersion:   "runtime-benchmark-test",
		Subscription:  original.Subscription,
		ModelRegistry: &presetstore.ModelRegistryPreset{
			SchemaVersion: 1,
			BenchmarkProfiles: []presetstore.ModelBenchmarkProfilePreset{{
				Patterns:             []string{`(?:^|[-/])runtime-benchmark(?=$|@)`},
				CanonicalModel:       "runtime-benchmark",
				OverallScore:         90,
				CategoryScores:       map[string]float64{"coding": 91},
				Sources:              []string{"https://example.test/benchmark"},
				VerifiedAt:           "2026-07-14",
				Lane:                 "verified",
				SharedResults:        1,
				ComparableCategories: 1,
				TotalCategories:      1,
			}},
		},
	})
	defer store.Swap(original)

	resolved := ResolveModelBenchmarkProfile("runtime-benchmark")
	if !resolved.Known || resolved.Profile.CategoryScores["coding"] != 91 {
		t.Fatalf("resolved = %+v, want runtime benchmark", resolved)
	}
	if fallback := ResolveModelBenchmarkProfile("gpt-5.6-sol"); fallback.Known {
		t.Fatal("运行时基准存在时应由运行时列表整体接管")
	}
}

func TestBuiltinModelBenchmarkProfiles_ReturnsDeepCopy(t *testing.T) {
	profiles := BuiltinModelBenchmarkProfiles()
	var foundPattern string
	for pattern, profile := range profiles {
		if profile.CanonicalModel != "gpt-5.6-sol" {
			continue
		}
		foundPattern = pattern
		profile.CategoryScores["coding"] = 1
		profile.Sources[0] = "mutated"
		profile.BenchmarkEvidence[0].SourceURL = "https://mutated.example/"
		if profile.BenchmarkEvidence[0].CostUSD != nil {
			*profile.BenchmarkEvidence[0].CostUSD = 9.99
		} else {
			v := 9.99
			profile.BenchmarkEvidence[0].CostUSD = &v
		}
		profiles[pattern] = profile
		break
	}
	if foundPattern == "" {
		t.Fatal("未找到 gpt-5.6-sol benchmark profile")
	}

	resolved := ResolveModelBenchmarkProfile("gpt-5.6-sol")
	if got := resolved.Profile.CategoryScores["coding"]; got == 1 {
		t.Fatal("CategoryScores 未深拷贝")
	}
	if len(resolved.Profile.Sources) == 0 || resolved.Profile.Sources[0] == "mutated" {
		t.Fatalf("Sources 未深拷贝: %v", resolved.Profile.Sources)
	}
	if len(resolved.Profile.BenchmarkEvidence) == 0 || resolved.Profile.BenchmarkEvidence[0].SourceURL == "https://mutated.example/" {
		t.Fatalf("BenchmarkEvidence 未深拷贝: %v", resolved.Profile.BenchmarkEvidence)
	}
	if len(resolved.Profile.BenchmarkEvidence) > 0 {
		if resolved.Profile.BenchmarkEvidence[0].CostUSD != nil && *resolved.Profile.BenchmarkEvidence[0].CostUSD == 9.99 {
			t.Fatalf("CostUSD 指针未深拷贝: %v", *resolved.Profile.BenchmarkEvidence[0].CostUSD)
		}
	}
}

func TestConvertRuntimeBenchmarkProfiles_CopiesCostUSD(t *testing.T) {
	cost := 0.0042
	preset := &presetstore.ModelRegistryPreset{
		SchemaVersion: 1,
		BenchmarkProfiles: []presetstore.ModelBenchmarkProfilePreset{{
			Patterns:       []string{"gpt-test"},
			CanonicalModel: "gpt-test",
			BenchmarkEvidence: []presetstore.ModelBenchmarkEvidencePreset{{
				Benchmark:        "codexradar",
				BenchmarkVersion: "v1",
				SourceModel:      "gpt-test",
				Domain:           "coding",
				Metric:           "pass_at_1",
				RawValue:         0.75,
				CohortPercentile: 0.8,
				TaskCount:        100,
				CohortSize:       10,
				Effort:           "high",
				SelectionBasis:   "best_available_effort",
				SourceURL:        "https://deng.codexradar.com/",
				CapturedAt:       "2026-08-10",
				CostUSD:          &cost,
			}},
		}},
	}

	profiles := convertRuntimeBenchmarkProfiles(preset)
	profile, ok := profiles["gpt-test"]
	if !ok {
		t.Fatal("未找到转换后的 benchmark profile")
	}
	if len(profile.BenchmarkEvidence) != 1 {
		t.Fatalf("证据数量 = %d, want 1", len(profile.BenchmarkEvidence))
	}
	ev := profile.BenchmarkEvidence[0]
	if ev.CostUSD == nil {
		t.Fatal("CostUSD 未拷贝")
	}
	if *ev.CostUSD != cost {
		t.Fatalf("CostUSD = %v, want %v", *ev.CostUSD, cost)
	}
	// 验证深拷贝：修改 preset 不影响运行时
	cost = 0.9999
	if *profile.BenchmarkEvidence[0].CostUSD != 0.0042 {
		t.Fatalf("CostUSD 指针未隔离: %v", *profile.BenchmarkEvidence[0].CostUSD)
	}
}

func TestModelBenchmarkEvidence_JSONDeserialization(t *testing.T) {
	jsonIn := `{
		"benchmark":"codexradar",
		"benchmarkVersion":"v1",
		"sourceModel":"gpt-test",
		"domain":"coding",
		"metric":"pass_at_1",
		"rawValue":0.75,
		"cohortPercentile":0.8,
		"taskCount":100,
		"cohortSize":10,
		"effort":"high",
		"selectionBasis":"best_available_effort",
		"sourceUrl":"https://deng.codexradar.com/",
		"capturedAt":"2026-08-10",
		"costUsd":0.0042
	}`
	var ev ModelBenchmarkEvidence
	if err := json.Unmarshal([]byte(jsonIn), &ev); err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}
	if ev.CostUSD == nil || *ev.CostUSD != 0.0042 {
		t.Fatalf("costUsd 反序列化 = %v, want 0.0042", ev.CostUSD)
	}

	// 无 cost 字段时保持 nil
	jsonNoCost := `{"benchmark":"x","benchmarkVersion":"v1","sourceModel":"m","domain":"coding","metric":"pass_at_1","rawValue":0.5,"cohortPercentile":0.5,"taskCount":1,"cohortSize":1,"effort":"medium","selectionBasis":"best","sourceUrl":"https://example.test/","capturedAt":"2026-08-10"}`
	var ev2 ModelBenchmarkEvidence
	if err := json.Unmarshal([]byte(jsonNoCost), &ev2); err != nil {
		t.Fatalf("反序列化失败: %v", err)
	}
	if ev2.CostUSD != nil {
		t.Fatalf("无 cost 时期望 nil, 得到 %v", *ev2.CostUSD)
	}
}
