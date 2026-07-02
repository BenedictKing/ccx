package handlers

import (
	"encoding/json"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

func TestBuildChatProbeBody_ReasoningEffortUsesProviderCompatibleValue(t *testing.T) {
	bodyBytes := buildChatProbeBody("test-model", nil)

	var body map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		t.Fatalf("unmarshal body failed: %v", err)
	}
	if body["reasoning_effort"] != "low" {
		t.Fatalf("reasoning_effort=%v, want low", body["reasoning_effort"])
	}
}

func TestBuildProbeBodiesUseExpandedTokenBudget(t *testing.T) {
	tests := []struct {
		name string
		body []byte
		get  func(map[string]interface{}) interface{}
	}{
		{
			name: "messages",
			body: buildMessagesProbeBody("test-model", nil),
			get:  func(body map[string]interface{}) interface{} { return body["max_tokens"] },
		},
		{
			name: "chat",
			body: buildChatProbeBody("test-model", nil),
			get:  func(body map[string]interface{}) interface{} { return body["max_tokens"] },
		},
		{
			name: "responses",
			body: buildResponsesProbeBody("test-model", nil),
			get:  func(body map[string]interface{}) interface{} { return body["max_output_tokens"] },
		},
		{
			name: "gemini",
			body: buildGeminiProbeBody("test-model", nil),
			get: func(body map[string]interface{}) interface{} {
				generationConfig, _ := body["generationConfig"].(map[string]interface{})
				return generationConfig["maxOutputTokens"]
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body map[string]interface{}
			if err := json.Unmarshal(tt.body, &body); err != nil {
				t.Fatalf("unmarshal body failed: %v", err)
			}
			if got := tt.get(body); got != float64(capabilityProbeMaxTokens) {
				t.Fatalf("max tokens=%v, want %d", got, capabilityProbeMaxTokens)
			}
		})
	}
}

func TestBuildChatProbeBody_KimiK27CodeUsesRequiredReasoningEffort(t *testing.T) {
	bodyBytes := buildChatProbeBody("kimi-k2.7-code", nil)

	var body map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		t.Fatalf("unmarshal body failed: %v", err)
	}
	if body["reasoning_effort"] != "high" {
		t.Fatalf("reasoning_effort=%v, want high", body["reasoning_effort"])
	}
}

func TestBuildChatProbeBody_KimiK27CodeMappingUsesRequiredReasoningEffort(t *testing.T) {
	channel := &config.UpstreamConfig{
		ModelMapping: map[string]string{
			"agent": "kimi-k2.7-code",
		},
	}
	bodyBytes := buildChatProbeBody("agent", nil, channel)

	var body map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		t.Fatalf("unmarshal body failed: %v", err)
	}
	if body["reasoning_effort"] != "high" {
		t.Fatalf("reasoning_effort=%v, want high", body["reasoning_effort"])
	}
}

func TestBuildMessagesProbeBody_ClaudeOpus48UsesSystemMessage(t *testing.T) {
	bodyBytes := buildMessagesProbeBody(capabilityProbeModelClaudeOpus48, nil)

	var body map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		t.Fatalf("unmarshal body failed: %v", err)
	}
	messages, ok := body["messages"].([]interface{})
	if !ok {
		t.Fatalf("messages=%T, want []interface{}", body["messages"])
	}
	if len(messages) != 4 {
		t.Fatalf("messages len=%d, want 4", len(messages))
	}
	middle, ok := messages[2].(map[string]interface{})
	if !ok {
		t.Fatalf("middle message=%T, want map[string]interface{}", messages[2])
	}
	if middle["role"] != "system" {
		t.Fatalf("middle role=%v, want system", middle["role"])
	}
	if _, ok := middle["content"].(string); !ok {
		t.Fatalf("middle content=%T, want string", middle["content"])
	}
}

func TestBuildMessagesProbeBody_LegacyModelsKeepTopLevelSystem(t *testing.T) {
	bodyBytes := buildMessagesProbeBody("claude-opus-4-7", nil)

	var body map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		t.Fatalf("unmarshal body failed: %v", err)
	}
	messages, ok := body["messages"].([]interface{})
	if !ok {
		t.Fatalf("messages=%T, want []interface{}", body["messages"])
	}
	for _, raw := range messages {
		msg, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		if msg["role"] == "system" {
			t.Fatalf("legacy probe should not include system message: %#v", messages)
		}
	}
	system, ok := body["system"].([]interface{})
	if !ok {
		t.Fatalf("system=%T, want []interface{}", body["system"])
	}
	if len(system) != 2 {
		t.Fatalf("system len=%d, want 2", len(system))
	}
}

func TestBuildMessagesProbeBody_KimiK27CodeEnablesRequiredThinking(t *testing.T) {
	bodyBytes := buildMessagesProbeBody("kimi-k2.7-code", nil)

	var body map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &body); err != nil {
		t.Fatalf("unmarshal body failed: %v", err)
	}
	thinking, ok := body["thinking"].(map[string]interface{})
	if !ok {
		t.Fatalf("thinking=%T, want map[string]interface{}", body["thinking"])
	}
	if thinking["type"] != "enabled" {
		t.Fatalf("thinking.type=%v, want enabled", thinking["type"])
	}
	if thinking["effort"] != "high" {
		t.Fatalf("thinking.effort=%v, want high", thinking["effort"])
	}
}
