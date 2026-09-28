package config

import (
	"reflect"
	"testing"
)

func TestApplyReasoningParamStylePreservesNativeReasoningMetadata(t *testing.T) {
	reasoning := map[string]interface{}{
		"effort":           "low",
		"summary":          "auto",
		"generate_summary": "concise",
	}
	req := map[string]interface{}{
		"reasoning":        reasoning,
		"thinking":         map[string]interface{}{"type": "enabled"},
		"reasoning_effort": "low",
	}

	ApplyReasoningParamStyle(req, "reasoning", "high")

	got, ok := req["reasoning"].(map[string]interface{})
	if !ok {
		t.Fatalf("reasoning = %T, want map", req["reasoning"])
	}
	if got["effort"] != "high" || got["summary"] != "auto" || got["generate_summary"] != "concise" {
		t.Fatalf("reasoning = %#v, want merged effort and preserved metadata", got)
	}
	if _, ok := req["thinking"]; ok {
		t.Fatal("thinking should be removed for native reasoning style")
	}
	if _, ok := req["reasoning_effort"]; ok {
		t.Fatal("reasoning_effort should be removed for native reasoning style")
	}
}

func TestApplyReasoningParamStyleUnknownStylePreservesConvertedRequest(t *testing.T) {
	thinking := map[string]interface{}{"type": "enabled", "budget_tokens": float64(4096)}
	req := map[string]interface{}{
		"reasoning_effort": "high",
		"thinking":         thinking,
	}

	ApplyReasoningParamStyle(req, "vendor_custom", "high")

	if req["reasoning_effort"] != "high" {
		t.Fatalf("reasoning_effort = %#v, want preserved high", req["reasoning_effort"])
	}
	if !reflect.DeepEqual(req["thinking"], thinking) {
		t.Fatalf("thinking = %#v, want original map", req["thinking"])
	}
	if _, ok := req["reasoning"]; ok {
		t.Fatalf("unknown style must not inject reasoning: %#v", req["reasoning"])
	}
}
