package handlers

import (
	"reflect"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

func TestCapabilityModelsForChannelUsesConfiguredModels(t *testing.T) {
	channel := &config.UpstreamConfig{
		SupportedModels:   []string{"private-model", "other-*", "!blocked"},
		ModelMapping:      map[string]string{"codex-auto-review": "private-model", "prefix-*": "private-model"},
		ModelCapabilities: map[string]config.UpstreamModelCapability{"internal-model": {}, "wild-*": {}},
	}
	got, err := capabilityModelsForChannel("responses", channel)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"internal-model", "private-model"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("configured probe models = %v, want %v", got, want)
	}
	configuredOnly := &config.UpstreamConfig{SupportedModels: []string{"private-model"}}
	if got, err := capabilityModelsForChannel("responses", configuredOnly); err != nil || !reflect.DeepEqual(got, []string{"private-model"}) {
		t.Fatalf("private channel should not probe unrelated built-in models: %v, err=%v", got, err)
	}
}

func TestCapabilityDefaultModelCacheKeyChangesWithConfiguredModels(t *testing.T) {
	protocols := []string{"responses"}
	first := capabilityDefaultModelCacheKey(protocols, &config.UpstreamConfig{SupportedModels: []string{"private-a"}})
	second := capabilityDefaultModelCacheKey(protocols, &config.UpstreamConfig{SupportedModels: []string{"private-b"}})
	if reflect.DeepEqual(first, second) || len(first) != 1 || first[0] != "responses:private-a" {
		t.Fatalf("default cache identity must follow channel models: first=%v second=%v", first, second)
	}
}

func TestGetProbeModelsForCapabilityProtocol_UsesSourceProtocol(t *testing.T) {
	got, err := getProbeModelsForCapabilityProtocol("messages->chat")
	if err != nil {
		t.Fatalf("getProbeModelsForCapabilityProtocol() error = %v", err)
	}
	want, err := getCapabilityProbeModels("messages")
	if err != nil {
		t.Fatalf("getCapabilityProbeModels() error = %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("models len=%d, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("models[%d]=%q, want %q", i, got[i], want[i])
		}
	}
}

func TestShouldRunRedirectVerification(t *testing.T) {
	tests := []struct {
		name               string
		protocols          []string
		sourceTab          string
		channelServiceType string
		want               bool
	}{
		{
			name:               "cross protocol runs redirect verification even for unrelated base protocol",
			protocols:          []string{"messages"},
			sourceTab:          "responses",
			channelServiceType: "chat",
			want:               true,
		},
		{
			name:               "cross protocol runs redirect verification for source protocol",
			protocols:          []string{"responses"},
			sourceTab:          "responses",
			channelServiceType: "chat",
			want:               true,
		},
		{
			name:               "explicit virtual protocol runs redirect verification",
			protocols:          []string{"responses->chat"},
			sourceTab:          "responses",
			channelServiceType: "chat",
			want:               true,
		},
		{
			name:               "cross protocol runs redirect verification even when protocols contain other virtual protocol",
			protocols:          []string{"messages->chat"},
			sourceTab:          "responses",
			channelServiceType: "chat",
			want:               true,
		},
		{
			name:               "all base protocols run redirect verification for cross protocol channel",
			protocols:          []string{"messages", "responses", "chat", "gemini"},
			sourceTab:          "responses",
			channelServiceType: "chat",
			want:               true,
		},
		{
			name:               "same source and channel protocol does not run redirect verification implicitly",
			protocols:          []string{"responses"},
			sourceTab:          "responses",
			channelServiceType: "responses",
			want:               false,
		},
		{
			name:               "same source unrelated virtual protocol does not run redirect verification",
			protocols:          []string{"messages->chat"},
			sourceTab:          "responses",
			channelServiceType: "responses",
			want:               false,
		},
		{
			name:               "same source explicit virtual protocol runs redirect verification",
			protocols:          []string{"responses->responses"},
			sourceTab:          "responses",
			channelServiceType: "responses",
			want:               true,
		},
		{
			name:               "empty source tab does not run redirect verification",
			protocols:          []string{"responses"},
			sourceTab:          "",
			channelServiceType: "chat",
			want:               false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := shouldRunRedirectVerification(tt.protocols, tt.sourceTab, tt.channelServiceType)
			if got != tt.want {
				t.Fatalf("shouldRunRedirectVerification(%v, %q, %q) = %v, want %v", tt.protocols, tt.sourceTab, tt.channelServiceType, got, tt.want)
			}
		})
	}
}
