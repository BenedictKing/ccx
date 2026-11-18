package providers

import (
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/types"
)

func TestConvertedStopReasonsMatchStreamAndNonStream(t *testing.T) {
	for _, tc := range []struct {
		name, body, stream, want string
		provider                 Provider
	}{
		{"chat-filter", `{"choices":[{"message":{"content":"blocked"},"finish_reason":"content_filter"}]}`, "data: {\"choices\":[{\"delta\":{\"content\":\"blocked\"},\"finish_reason\":\"content_filter\"}]}\n\ndata: [DONE]\n\n", "refusal", &OpenAIProvider{}},
		{"chat-tool-stop", `{"choices":[{"message":{"tool_calls":[{"id":"call_1","type":"function","function":{"name":"exec","arguments":"{}"}}]},"finish_reason":"stop"}]}`, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"exec\",\"arguments\":\"{}\"}}]},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", "tool_use", &OpenAIProvider{}},
	} {
		t.Run(tc.name, func(t *testing.T) { assertProviderStopReason(t, tc.provider, tc.body, tc.stream, tc.want) })
	}
	for _, tc := range []struct{ reason, want string }{{"STOP", "end_turn"}, {"MAX_TOKENS", "max_tokens"}, {"SAFETY", "end_turn"}, {"RECITATION", "end_turn"}, {"OTHER", "end_turn"}} {
		t.Run("gemini-"+tc.reason, func(t *testing.T) {
			body := fmt.Sprintf(`{"candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]},"finishReason":%q}]}`, tc.reason)
			assertProviderStopReason(t, &GeminiProvider{}, body, "data: "+body+"\n\n", tc.want)
		})
	}
	for _, tc := range []struct{ reason, want string }{{"content_filter", "refusal"}, {"max_output_tokens", "max_tokens"}} {
		t.Run("responses-"+tc.reason, func(t *testing.T) {
			body := fmt.Sprintf(`{"id":"resp_1","status":"incomplete","incomplete_details":{"reason":%q},"output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}]}`, tc.reason)
			stream := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\ndata: {\"type\":\"response.incomplete\",\"response\":" + body + "}\n\n"
			assertProviderStopReason(t, &ResponsesProvider{}, body, stream, tc.want)
		})
	}
}

func assertProviderStopReason(t *testing.T, provider Provider, body, stream, want string) {
	t.Helper()
	resp, err := provider.ConvertToClaudeResponse(&types.ProviderResponse{Body: []byte(body)})
	if err != nil {
		t.Fatal(err)
	}
	if resp.StopReason != want {
		t.Errorf("非流式 stop_reason=%q, want %q", resp.StopReason, want)
	}
	events, errors, err := provider.HandleStreamResponse(io.NopCloser(strings.NewReader(stream)))
	if err != nil {
		t.Fatal(err)
	}
	result := collectStreamEvents(events)
	select {
	case err := <-errors:
		if err != nil {
			t.Fatal(err)
		}
	default:
	}
	delta := extractMessageDelta(t, result)["delta"].(map[string]interface{})
	if delta["stop_reason"] != want {
		t.Errorf("流式 stop_reason=%v, want %q", delta["stop_reason"], want)
	}
}
