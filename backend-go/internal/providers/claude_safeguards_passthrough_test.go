package providers

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/types"
)

// 非流式 Claude 上游响应中的未知顶层字段（如 auto mode safeguard_results）
// 必须原样透传给客户端，网关不得丢键（gateway compatibility guide）。
func TestClaudeProvider_ConvertToClaudeResponse_PreservesUnknownTopLevelFields(t *testing.T) {
	provider := &ClaudeProvider{}
	claudeResp, err := provider.ConvertToClaudeResponse(&types.ProviderResponse{
		Body: []byte(`{
			"id": "msg_01",
			"type": "message",
			"role": "assistant",
			"model": "claude-sonnet-5",
			"content": [{"type": "text", "text": "ok"}],
			"stop_reason": "end_turn",
			"stop_sequence": null,
			"usage": {"input_tokens": 1, "output_tokens": 2},
			"safeguard_results": {"decision": "allow"}
		}`),
	})
	if err != nil {
		t.Fatalf("ConvertToClaudeResponse() error = %v", err)
	}

	out, err := json.Marshal(claudeResp)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(out), `"safeguard_results"`) {
		t.Fatalf("marshaled response dropped safeguard_results: %s", out)
	}
	if !strings.Contains(string(out), `"stop_sequence":null`) {
		t.Fatalf("marshaled response dropped stop_sequence: %s", out)
	}
}
