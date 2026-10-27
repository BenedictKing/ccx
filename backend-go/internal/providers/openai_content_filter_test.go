package providers

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/types"
)

// content_filter 表示输出被安全策略拦截，应映射为 Claude 的 refusal，而不是 end_turn。
func TestOpenAIProvider_ConvertToClaudeResponse_ContentFilterIsRefusal(t *testing.T) {
	provider := &OpenAIProvider{}
	providerResp := &types.ProviderResponse{
		StatusCode: http.StatusOK,
		Headers:    map[string][]string{"Content-Type": {"application/json"}},
		Body:       []byte(`{"choices":[{"message":{"role":"assistant","content":"I can't"},"finish_reason":"content_filter"}]}`),
	}

	claudeResp, err := provider.ConvertToClaudeResponse(providerResp)
	if err != nil {
		t.Fatalf("ConvertToClaudeResponse() err = %v", err)
	}
	if claudeResp.StopReason != "refusal" {
		t.Fatalf("stop_reason = %q, want \"refusal\"", claudeResp.StopReason)
	}
}

func TestOpenAIProvider_HandleStreamResponse_ContentFilterIsRefusal(t *testing.T) {
	body := strings.Join([]string{
		`data: {"id":"chatcmpl-6","choices":[{"index":0,"delta":{"content":"I can't"},"finish_reason":null}],"model":"gpt-5"}`,
		`data: {"id":"chatcmpl-6","choices":[{"index":0,"delta":{},"finish_reason":"content_filter"}],"model":"gpt-5"}`,
		`data: [DONE]`,
		"",
	}, "\n")

	provider := &OpenAIProvider{}
	eventChan, _, err := provider.HandleStreamResponse(io.NopCloser(strings.NewReader(body)))
	if err != nil {
		t.Fatalf("HandleStreamResponse returned error: %v", err)
	}

	messageDelta := extractMessageDelta(t, collectStreamEvents(eventChan))
	delta, _ := messageDelta["delta"].(map[string]interface{})
	stopReason, _ := delta["stop_reason"].(string)
	if stopReason != "refusal" {
		t.Fatalf("stop_reason = %q, want \"refusal\"", stopReason)
	}
}
