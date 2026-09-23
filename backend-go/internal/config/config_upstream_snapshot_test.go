package config

import "testing"

func TestGetUpstreamByIndexReturnsIndependentClone(t *testing.T) {
	cm := &ConfigManager{config: Config{
		ResponsesUpstream: []UpstreamConfig{{
			Name:              "responses",
			APIKeys:           []string{"sk-original"},
			SupportedModels:   []string{"requested"},
			ModelCapabilities: map[string]UpstreamModelCapability{"requested": {ContextWindowTokens: 200000}},
		}},
	}}

	first := cm.GetUpstreamByIndex("Responses", 0)
	if first == nil {
		t.Fatal("GetUpstreamByIndex() returned nil")
	}
	first.Name = "mutated"
	first.APIKeys[0] = "sk-mutated"
	first.SupportedModels[0] = "mutated"
	first.ModelCapabilities["requested"] = UpstreamModelCapability{ContextWindowTokens: 1}

	second := cm.GetUpstreamByIndex("Responses", 0)
	if second == nil {
		t.Fatal("second GetUpstreamByIndex() returned nil")
	}
	if second.Name != "responses" || second.APIKeys[0] != "sk-original" ||
		second.SupportedModels[0] != "requested" || second.ModelCapabilities["requested"].ContextWindowTokens != 200000 {
		t.Fatalf("stored upstream was mutated through snapshot: %+v", second)
	}
}

func TestGetUpstreamByIndexRejectsInvalidLookup(t *testing.T) {
	cm := &ConfigManager{config: Config{Upstream: []UpstreamConfig{{Name: "messages"}}}}
	for _, test := range []struct {
		apiType string
		index   int
	}{
		{apiType: "Unknown", index: 0},
		{apiType: "Messages", index: -1},
		{apiType: "Messages", index: 1},
	} {
		if got := cm.GetUpstreamByIndex(test.apiType, test.index); got != nil {
			t.Fatalf("GetUpstreamByIndex(%q, %d) = %+v, want nil", test.apiType, test.index, got)
		}
	}
}
