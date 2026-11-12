package config

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestResolveUpstreamCapabilityClaudePointVersions(t *testing.T) {
	for _, tt := range []struct {
		model, display string
		input, output  float64
	}{
		{"claude-opus-5.5", "Claude Opus 5.5", 4, 20},
		{"claude-sonnet-5.5", "Claude Sonnet 5.5", 2, 10},
		{"claude-haiku-5.5", "Claude Haiku 5.5", 0.1, 0.5},
		{"claude-fable-5.1", "Claude Fable 5.1", 10, 50},
		{"claude-mythos-5.1", "Claude Mythos 5.1", 10, 50},
	} {
		for _, model := range []string{tt.model, strings.ReplaceAll(tt.model, ".", "-")} {
			for _, variant := range []string{model, model + "-20261112", "anthropic/" + strings.ToUpper(model) + "-2026-11-12@custom"} {
				t.Run(variant, func(t *testing.T) {
					resolved := ResolveUpstreamCapability(variant, nil, nil)
					capability := resolved.Capability
					if !resolved.Known || capability.DisplayName != tt.display {
						t.Fatalf("resolved = %+v, want %s", resolved, tt.display)
					}
					if capability.ContextWindowTokens != 1000000 || capability.MaxOutputTokens != 128000 {
						t.Fatalf("unexpected token limits: %+v", capability)
					}
					if !capability.Capabilities["toolCalls"] || !capability.Capabilities["vision"] {
						t.Errorf("missing capabilities: %v", capability.Capabilities)
					}
					if capability.Pricing == nil {
						t.Fatal("missing point-version pricing")
					}
					assertFloatPointerValue(t, capability.Pricing.InputCacheMissPrice, tt.input, "input price")
					assertFloatPointerValue(t, capability.Pricing.OutputPrice, tt.output, "output price")
				})
			}
		}
	}
	for _, model := range []string{"claude-opus-5.50", "notclaude-opus-5.5", "claude-opus-5.5-extra"} {
		if resolved := ResolveUpstreamCapability(model, nil, nil); resolved.Known {
			t.Errorf("unexpected partial match for %q: %s", model, resolved.MatchedPattern)
		}
	}
}

func TestResolveUpstreamCapabilityMergedFamiliesAndLegacyAliases(t *testing.T) {
	for _, tt := range []struct {
		model           string
		context, output int
	}{
		{"claude-opus-4.8", 1000000, 128000},
		{"claude-sonnet-4.6-thinking", 1000000, 64000},
		{"claude-haiku-5", 1000000, 128000},
		{"gemini-3-flash", 1048576, 65535},
		{"gemini-3.1-pro", 1048576, 65536},
		{"gemini-3.5-flash", 1048576, 65535},
		{"gemini-3.5-flash-lite", 1048576, 65536},
		{"gemini-3.6-flash", 1048576, 65536},
		{"gemini-3.7-flash", 1048576, 65536},
		{"gemini-3.8-flash", 1048576, 65536},
		{"gpt-6-astra", 1050000, 128000},
		{"deepseek-v4-flash-20261112", 1000000, 384000},
		{"deepseek-v4-flash-vision-exp", 1000000, 384000},
		{"deepseek-v4-pro", 1000000, 384000},
		{"doubao-seed-2.1-turbo", 256000, 256000},
		{"doubao-seed-2-1-turbo", 256000, 256000},
		{"kimi-k3.1-pro-highspeed", 1048576, 1048576},
		{"glm-5p3", 1048576, 131072},
		{"glm-5.3-20261112[1m]", 1048576, 131072},
		{"glm-5.3-flashx-legal[1m]", 1048576, 131072},
		{"hy4", 1048576, 65536},
		{"hy4-preview", 1048576, 64000},
		{"mimo-v2-flash", 262144, 16384},
	} {
		t.Run(tt.model, func(t *testing.T) {
			resolved := ResolveUpstreamCapability("mapped", &UpstreamConfig{ModelMapping: map[string]string{"mapped": tt.model}}, nil)
			if !resolved.Known || resolved.ActualModel != tt.model {
				t.Fatalf("model did not resolve after mapping: %+v", resolved)
			}
			if got := resolved.Capability; got.ContextWindowTokens != tt.context || got.MaxOutputTokens != tt.output {
				t.Errorf("limits = %d/%d, want %d/%d", got.ContextWindowTokens, got.MaxOutputTokens, tt.context, tt.output)
			}
		})
	}
}

func TestResolveUpstreamCapabilityLegacyMetadataFilled(t *testing.T) {
	for _, model := range []string{"claude-haiku-4.5", "claude-sonnet-4.5", "claude-opus-4.6", "claude-opus-5", "claude-sonnet-5", "gpt-6-astra"} {
		resolved := ResolveUpstreamCapability(model, nil, nil)
		capability := resolved.Capability
		if !resolved.Known || !capability.Capabilities["vision"] || !capability.Capabilities["toolCalls"] {
			t.Errorf("%s missing capabilities: %v", model, capability.Capabilities)
		}
		if capability.Pricing == nil || capability.Pricing.InputCacheMissPrice == nil || capability.Pricing.OutputPrice == nil {
			t.Errorf("%s missing pricing: %+v", model, capability.Pricing)
		}
	}
}

func TestModelRegistrySourceHasUniquePatterns(t *testing.T) {
	data, err := os.ReadFile("../../../shared/model-registry/ccx_model_registry.json")
	if err != nil {
		t.Fatal(err)
	}
	var source struct {
		Entries []struct {
			Patterns    []string `json:"patterns"`
			DisplayName string   `json:"displayName"`
		} `json:"upstreamCapabilities"`
	}
	if err := json.Unmarshal(data, &source); err != nil {
		t.Fatal(err)
	}
	seen := make(map[string]string)
	for _, entry := range source.Entries {
		for _, pattern := range entry.Patterns {
			if previous, exists := seen[pattern]; exists {
				t.Errorf("pattern %q repeated in %s and %s", pattern, previous, entry.DisplayName)
			}
			seen[pattern] = entry.DisplayName
		}
	}
	if len(seen) != len(BuiltinUpstreamModelCapabilities()) {
		t.Error("generated registry does not contain all source patterns")
	}
}
