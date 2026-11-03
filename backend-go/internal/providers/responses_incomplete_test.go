package providers

import (
	"net/http"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/types"
)

// incomplete 的原因决定 stop_reason：max_output_tokens 是 max_tokens，content_filter 是 refusal。
func TestResponsesProvider_ConvertToClaudeResponse_IncompleteReason(t *testing.T) {
	tests := []struct {
		name    string
		details string
		want    string
	}{
		{"max_output_tokens", `{"reason":"max_output_tokens"}`, "max_tokens"},
		{"content_filter", `{"reason":"content_filter"}`, "refusal"},
		{"no details", `null`, "max_tokens"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := &ResponsesProvider{}
			providerResp := &types.ProviderResponse{
				StatusCode: http.StatusOK,
				Headers:    map[string][]string{"Content-Type": {"application/json"}},
				Body: []byte(`{"id":"resp_1","status":"incomplete","incomplete_details":` + tt.details + `,
					"output":[{"type":"message","content":[{"type":"output_text","text":"partial"}]}]}`),
			}

			claudeResp, err := provider.ConvertToClaudeResponse(providerResp)
			if err != nil {
				t.Fatalf("ConvertToClaudeResponse() err = %v", err)
			}
			if claudeResp.StopReason != tt.want {
				t.Fatalf("stop_reason = %q, want %q", claudeResp.StopReason, tt.want)
			}
		})
	}
}

func TestResponsesProvider_HandleStreamResponse_IncompleteContentFilterMapsToRefusal(t *testing.T) {
	body := `event: response.output_text.delta
data: {"type":"response.output_text.delta","delta":"partial"}

event: response.incomplete
data: {"type":"response.incomplete","response":{"status":"incomplete","incomplete_details":{"reason":"content_filter"},"usage":{"input_tokens":3,"output_tokens":1}}}

`
	events := runResponsesStream(t, body)

	if !strings.Contains(strings.Join(events, ""), `"stop_reason":"refusal"`) {
		t.Fatalf("message_delta should carry stop_reason=refusal, got: %v", events)
	}
}
