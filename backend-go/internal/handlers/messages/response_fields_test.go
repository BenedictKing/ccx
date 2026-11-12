package messages

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

func TestMessagesHandlerPreservesClaudeResponseFields(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(`{"id":"msg_test","type":"message","role":"assistant","model":"claude-sonnet-5","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","stop_sequence":null,"container":{"id":"c_test"},"safeguard_results":{"count":9007199254740993},"usage":{"input_tokens":1,"output_tokens":2}}`))
	}))
	defer upstream.Close()
	router := newMessagesTestRouter(t, config.UpstreamConfig{Name: "claude", BaseURL: upstream.URL, APIKeys: []string{"test-key"}, ServiceType: "claude"})
	w := performMessagesHandlerRequest(t, router, `{"model":"claude-sonnet-5","messages":[{"role":"user","content":"hello"}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", w.Code, w.Body.String())
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"model": `"claude-sonnet-5"`, "stop_sequence": "null", "container": `{"id":"c_test"}`, "safeguard_results": `{"count":9007199254740993}`} {
		if got := string(body[key]); got != want {
			t.Errorf("%s = %s, want %s", key, got, want)
		}
	}
	if got := w.Header().Get("Content-Type"); got != "application/json; charset=utf-8" {
		t.Errorf("Content-Type = %q", got)
	}
}
