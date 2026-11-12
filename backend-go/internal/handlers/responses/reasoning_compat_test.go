package responses

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/converters"
	"github.com/BenedictKing/ccx/internal/handlers/common"
)

func TestReasoningDeltasAreSemanticContentAndFilteredFromCompact(t *testing.T) {
	for _, eventType := range []string{"response.reasoning_text.delta", "response.reasoning_text.done", "response.reasoning_summary_text.delta"} {
		event := `data: {"type":"` + eventType + `","delta":"think"}`
		if !common.HasResponsesSemanticContent(event) {
			t.Errorf("not semantic: %s", eventType)
		}
		if !shouldSkipCompactStreamEvent(converters.NormalizeOpenRouterReasoningSSELine(event)) {
			t.Errorf("reasoning would leak into compact: %s", eventType)
		}
	}
}

func TestNativeReasoningHTTPPreservesUpstreamForm(t *testing.T) {
	for _, stream := range []bool{false, true} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if stream {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = w.Write([]byte("event: response.reasoning_text.delta\ndata: {\"type\":\"response.reasoning_text.delta\",\"output_index\":0,\"content_index\":0,\"delta\":\"think\"}\n\nevent: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp1\",\"status\":\"completed\",\"output\":[{\"type\":\"reasoning\",\"summary\":[],\"content\":[{\"type\":\"reasoning_text\",\"text\":\"think\"}]}],\"usage\":{\"input_tokens\":2,\"output_tokens\":2}}}\n\n"))
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"id":"resp1","status":"completed","output":[{"type":"reasoning","summary":[],"content":[{"type":"reasoning_text","text":"think"}]}],"usage":{"input_tokens":2,"output_tokens":2}}`))
		}))
		router := newResponsesTestRouter(t, config.UpstreamConfig{Name: "reasoning", BaseURL: server.URL, ServiceType: "responses", APIKeys: []string{"key"}}, nil)
		body := `{"model":"test","store":false,"input":"hello","stream":false}`
		if stream {
			body = strings.Replace(body, `"stream":false`, `"stream":true`, 1)
		}
		w := performResponsesHandlerRequest(t, router, body)
		server.Close()
		if w.Code != 200 || strings.Contains(w.Body.String(), "summary_text") || !strings.Contains(w.Body.String(), `"type":"reasoning_text"`) {
			t.Fatalf("stream=%t response=%d %s", stream, w.Code, w.Body.String())
		}
		if stream && !strings.Contains(w.Body.String(), "event: response.reasoning_text.delta") {
			t.Fatalf("upstream event name changed: %s", w.Body.String())
		}
	}
}
