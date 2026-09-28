package providers

import (
	"net/http/httptest"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/gin-gonic/gin"
)

func responsesReasoningTestContext() *gin.Context {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	return c
}

func TestResponsesProviderPassthroughPreservesReasoningMetadata(t *testing.T) {
	provider := &ResponsesProvider{}
	body := []byte(`{
		"model":"gpt-5-codex",
		"input":"hello",
		"reasoning":{"effort":"high","summary":"auto","generate_summary":"concise"}
	}`)

	providerReq, _, err := provider.buildProviderRequestBody(
		responsesReasoningTestContext(),
		"/v1/responses",
		body,
		&config.UpstreamConfig{ServiceType: "responses"},
	)
	if err != nil {
		t.Fatalf("buildProviderRequestBody() error = %v", err)
	}
	reqMap, ok := providerReq.(map[string]interface{})
	if !ok {
		t.Fatalf("provider request = %T, want map", providerReq)
	}
	reasoning, ok := reqMap["reasoning"].(map[string]interface{})
	if !ok {
		t.Fatalf("reasoning = %T, want map", reqMap["reasoning"])
	}
	if reasoning["effort"] != "high" || reasoning["summary"] != "auto" || reasoning["generate_summary"] != "concise" {
		t.Fatalf("reasoning = %#v, want original metadata preserved", reasoning)
	}
}

func TestResponsesProviderUnknownReasoningStyleDoesNotInventProtocolField(t *testing.T) {
	provider := &ResponsesProvider{}
	body := []byte(`{
		"model":"gpt-5-codex",
		"input":[{"role":"user","content":[{"type":"input_text","text":"hello"}]}],
		"reasoning":{"effort":"high"}
	}`)

	providerReq, _, err := provider.buildProviderRequestBody(
		responsesReasoningTestContext(),
		"/v1/responses",
		body,
		&config.UpstreamConfig{ServiceType: "custom_chat", ReasoningParamStyle: "vendor_custom"},
	)
	if err != nil {
		t.Fatalf("buildProviderRequestBody() error = %v", err)
	}
	reqMap, ok := providerReq.(map[string]interface{})
	if !ok {
		t.Fatalf("provider request = %T, want map", providerReq)
	}
	if _, ok := reqMap["reasoning_effort"]; ok {
		t.Fatalf("unknown style must not invent reasoning_effort: %#v", reqMap["reasoning_effort"])
	}
	if _, ok := reqMap["reasoning"]; ok {
		t.Fatalf("unknown style must not invent reasoning: %#v", reqMap["reasoning"])
	}
}
