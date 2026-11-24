package messages

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

func TestNativeMessagesResponseCompressionNegotiation(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, encoding := range []string{"", "gzip, br", "identity", " Identity "} {
			t.Run(encoding+map[bool]string{false: "/http", true: "/sse"}[stream], func(t *testing.T) {
				wantEncoding := "gzip"
				if stream {
					wantEncoding = ""
				}
				if strings.EqualFold(strings.TrimSpace(encoding), "identity") {
					wantEncoding = "identity"
				}
				response := `{"id":"msg1","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":10,"output_tokens":2}}`
				if stream {
					response = "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if got := r.Header.Get("Accept-Encoding"); got != wantEncoding {
						t.Errorf("upstream Accept-Encoding = %q, want %q", got, wantEncoding)
					}
					w.Header().Set("Content-Type", map[bool]string{false: "application/json", true: "text/event-stream"}[stream])
					if r.Header.Get("Accept-Encoding") == "gzip" {
						w.Header().Set("Content-Encoding", "gzip")
						compressed := gzip.NewWriter(w)
						_, _ = io.WriteString(compressed, response)
						_ = compressed.Close()
						return
					}
					_, _ = io.WriteString(w, response)
				}))
				defer server.Close()
				router := newMessagesTestRouter(t, config.UpstreamConfig{BaseURL: server.URL, APIKeys: []string{"key"}, ServiceType: "claude"})
				body := `{"model":"native","stream":` + map[bool]string{false: "false", true: "true"}[stream] + `,"messages":[{"role":"user","content":"hello"}]}`
				req := httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(body))
				req.Header.Set("Content-Type", "application/json")
				req.Header.Set("x-api-key", "secret-key")
				req.Header.Set("Accept-Encoding", encoding)
				w := httptest.NewRecorder()
				router.ServeHTTP(w, req)
				if got := req.Header.Get("Accept-Encoding"); got != encoding {
					t.Fatalf("client header mutated: %q, want %q", got, encoding)
				}
				if w.Code != http.StatusOK || w.Body.String() != response {
					t.Fatalf("client response = %d %q, want %q", w.Code, w.Body.String(), response)
				}
			})
		}
	}
}

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
