package messages

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

func TestNativeMessagesHTTPPreservesRequestAndResponse(t *testing.T) {
	const request = `{ "model":"native", "metadata":{"user_id":"{\"session_id\":\"s1\"}"}, "messages":[{"role":"assistant","content":[{"type":"thinking","thinking":"history","signature":""},{"type":"text","text":""}]},{"role":"user","content":"hello"}], "future":9007199254740993 }`
	const response = `{ "id":"msg_native", "type":"message", "role":"assistant", "content":[{"type":"text","text":"hi"}], "future":9007199254740993, "usage":{"input_tokens":1,"output_tokens":0} }`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if string(body) != request {
			t.Errorf("request changed: %s", body)
		}
		w.Header().Set("Content-Type", "application/json; profile=native")
		_, _ = io.WriteString(w, response)
	}))
	defer server.Close()
	router := newMessagesTestRouter(t, config.UpstreamConfig{BaseURL: server.URL, APIKeys: []string{"key"}, ServiceType: "claude"})
	w := performMessagesHandlerRequest(t, router, request)
	if w.Code != 200 || w.Body.String() != response {
		t.Fatalf("response changed: %d %s", w.Code, w.Body.String())
	}
}

func TestNativeMessagesStreamPreservesWireEvents(t *testing.T) {
	const stream = ": upstream\r\nevent: message_start\r\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg1\",\"usage\":{\"input_tokens\":1}}}\r\n\r\n" +
		"event: content_block_delta\r\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"},\"future\":9007199254740993}\r\n\r\n" +
		"event: message_stop\r\ndata: {\"type\":\"message_stop\"}\r\n\r\n"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, stream)
	}))
	defer server.Close()
	router := newMessagesTestRouter(t, config.UpstreamConfig{BaseURL: server.URL, APIKeys: []string{"key"}, ServiceType: "claude"})
	w := performMessagesHandlerRequest(t, router, `{"model":"native","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
	if w.Code != 200 || w.Body.String() != stream {
		t.Fatalf("stream changed: %d %q", w.Code, w.Body.String())
	}
}
