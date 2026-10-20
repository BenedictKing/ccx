package providers

import (
	"testing"

	"github.com/BenedictKing/ccx/internal/types"
)

func TestGeminiConvertToClaudeResponse_StopReason(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "STOP with text",
			body: `{"candidates":[{"content":{"parts":[{"text":"hi"}]},"finishReason":"STOP"}]}`,
			want: "end_turn",
		},
		{
			name: "STOP with function call",
			body: `{"candidates":[{"content":{"parts":[{"functionCall":{"name":"lookup","args":{}}}]},"finishReason":"STOP"}]}`,
			want: "tool_use",
		},
		{
			name: "MAX_TOKENS",
			body: `{"candidates":[{"content":{"parts":[{"text":"cut off"}]},"finishReason":"MAX_TOKENS"}]}`,
			want: "max_tokens",
		},
		{
			name: "SAFETY",
			body: `{"candidates":[{"content":{"parts":[{"text":"blocked"}]},"finishReason":"SAFETY"}]}`,
			want: "end_turn",
		},
		{
			name: "RECITATION",
			body: `{"candidates":[{"content":{"parts":[{"text":"quoted"}]},"finishReason":"RECITATION"}]}`,
			want: "end_turn",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &GeminiProvider{}
			resp, err := provider.ConvertToClaudeResponse(&types.ProviderResponse{Body: []byte(tt.body)})
			if err != nil {
				t.Fatalf("ConvertToClaudeResponse returned error: %v", err)
			}
			if resp.StopReason != tt.want {
				t.Fatalf("expected stop_reason=%q, got %q", tt.want, resp.StopReason)
			}
		})
	}
}
