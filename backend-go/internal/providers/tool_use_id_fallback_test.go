package providers

import (
	"net/http"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/types"
)

func TestOpenAIConvertToClaudeResponseToolUseIDFallback(t *testing.T) {
	provider := &OpenAIProvider{}
	providerResp := &types.ProviderResponse{
		StatusCode: http.StatusOK,
		Headers:    map[string][]string{"Content-Type": {"application/json"}},
		Body: []byte(`{
			"choices":[
				{
					"message":{
						"role":"assistant",
						"tool_calls":[
							{"type":"function","function":{"name":"Read","arguments":"{\"file_path\":\"/tmp/x\"}"}},
							{"id":"call_keep","type":"function","function":{"name":"Grep","arguments":"{\"q\":\"x\"}"}}
						]
					}
				}
			]
		}`),
	}

	claudeResp, err := provider.ConvertToClaudeResponse(providerResp)
	if err != nil {
		t.Fatalf("ConvertToClaudeResponse() err = %v", err)
	}
	if len(claudeResp.Content) != 2 {
		t.Fatalf("len(Content) = %d, want 2", len(claudeResp.Content))
	}
	fallback, preserved := claudeResp.Content[0], claudeResp.Content[1]
	if fallback.Type != "tool_use" || fallback.Name != "Read" {
		t.Fatalf("content[0] = %#v, want tool_use Read", fallback)
	}
	if !strings.HasPrefix(fallback.ID, "toolu_") {
		t.Fatalf("content[0].ID = %q, want toolu_ prefix for upstream-omitted id", fallback.ID)
	}
	if preserved.ID != "call_keep" {
		t.Fatalf("content[1].ID = %q, want upstream id preserved", preserved.ID)
	}
}

func TestResponsesConvertToClaudeResponseToolUseIDFallback(t *testing.T) {
	provider := &ResponsesProvider{}
	providerResp := &types.ProviderResponse{
		StatusCode: http.StatusOK,
		Headers:    map[string][]string{"Content-Type": {"application/json"}},
		Body: []byte(`{
			"output":[
				{"type":"function_call","name":"Read","arguments":"{\"file_path\":\"/tmp/x\"}"},
				{"type":"function_call","call_id":"call_keep","name":"Grep","arguments":"{\"q\":\"x\"}"}
			]
		}`),
	}

	claudeResp, err := provider.ConvertToClaudeResponse(providerResp)
	if err != nil {
		t.Fatalf("ConvertToClaudeResponse() err = %v", err)
	}
	if len(claudeResp.Content) != 2 {
		t.Fatalf("len(Content) = %d, want 2", len(claudeResp.Content))
	}
	fallback, preserved := claudeResp.Content[0], claudeResp.Content[1]
	if fallback.Type != "tool_use" || fallback.Name != "Read" {
		t.Fatalf("content[0] = %#v, want tool_use Read", fallback)
	}
	if !strings.HasPrefix(fallback.ID, "toolu_") {
		t.Fatalf("content[0].ID = %q, want toolu_ prefix for upstream-omitted call_id", fallback.ID)
	}
	if preserved.ID != "call_keep" {
		t.Fatalf("content[1].ID = %q, want upstream call_id preserved", preserved.ID)
	}
}
