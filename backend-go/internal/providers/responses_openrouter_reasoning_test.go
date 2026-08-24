package providers

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/types"
)

// OpenRouter 非流式响应把思考文本放在非标准 content[reasoning_text]，
// ConvertToResponsesResponse 应先归一化为规范 summary[summary_text] 再透出。
func TestResponsesProvider_ConvertToResponsesResponse_NormalizesOpenRouterBody(t *testing.T) {
	provider := &ResponsesProvider{}
	resp, err := provider.ConvertToResponsesResponse(&types.ProviderResponse{
		Body: []byte(`{
			"id": "resp_or_1",
			"model": "test-model",
			"status": "completed",
			"output": [
				{
					"id": "rs_1",
					"type": "reasoning",
					"status": "completed",
					"summary": [],
					"content": [{"type": "reasoning_text", "text": "17*23=391"}]
				},
				{
					"id": "msg_1",
					"type": "message",
					"role": "assistant",
					"content": [{"type": "output_text", "text": "**391**"}]
				}
			],
			"usage": {"input_tokens": 10, "output_tokens": 5, "total_tokens": 15}
		}`),
	}, "responses", "")
	if err != nil {
		t.Fatalf("ConvertToResponsesResponse() err = %v", err)
	}

	raw, _ := json.Marshal(resp.Output)
	if strings.Contains(string(raw), "reasoning_text") {
		t.Fatalf("non-standard reasoning_text part leaked: %s", raw)
	}
	reasoning := resp.Output[0]
	if reasoning.Summary == nil {
		t.Fatalf("summary should be populated: %#v", reasoning)
	}
	summaryJSON, _ := json.Marshal(reasoning.Summary)
	if !strings.Contains(string(summaryJSON), "17*23=391") || !strings.Contains(string(summaryJSON), "summary_text") {
		t.Fatalf("unexpected summary: %s", summaryJSON)
	}
	if reasoning.Content != nil {
		t.Fatalf("content should be removed after normalization: %#v", reasoning.Content)
	}
}
