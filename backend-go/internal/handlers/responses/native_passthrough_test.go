package responses

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

func TestNativeResponsesHTTPPreservesRequestAndResponse(t *testing.T) {
	const request = `{ "model":"native", "store":false, "input":[{"type":"additional_tools","tools":[{"type":"custom","name":"exec"}]},{"type":"function_call_output","call_id":"outside","output":"ok","status":"completed"}], "future":9007199254740993 }`
	const response = `{ "id":"resp_native", "object":"response", "status":"completed", "output":[{"type":"future_output","value":9007199254740993}], "future":{"x":true}, "usage":{"input_tokens":1,"output_tokens":0,"total_tokens":1,"future_tokens":123} }`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if string(body) != request {
			t.Errorf("request changed: %s", body)
		}
		w.Header().Set("Content-Type", "application/json; profile=native")
		w.WriteHeader(http.StatusCreated)
		_, _ = io.WriteString(w, response)
	}))
	defer server.Close()
	router := newResponsesTestRouter(t, config.UpstreamConfig{BaseURL: server.URL, APIKeys: []string{"key"}, ServiceType: "responses"}, nil)
	w := performResponsesHandlerRequest(t, router, request)
	if w.Code != http.StatusCreated || w.Body.String() != response || w.Header().Get("Content-Type") != "application/json; profile=native" {
		t.Fatalf("native response changed: status=%d headers=%v body=%s", w.Code, w.Header(), w.Body.String())
	}
}

func TestResponsesReasoningFoldRequiresExplicitOptIn(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		var calls atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			if calls.Add(1) == 1 {
				_, _ = io.WriteString(w, responsesFoldReasoningRound("rs1", responsesFoldStep-2, "FIRST", true))
			} else {
				_, _ = io.WriteString(w, responsesFoldReasoningRound("rs2", 20, "FINAL", true))
			}
		}))
		router := newResponsesTestRouter(t, config.UpstreamConfig{BaseURL: server.URL, APIKeys: []string{"key"}, ServiceType: "responses", CodexNativeToolPassthrough: true, ResponsesReasoningFold: enabled}, nil)
		w := performResponsesHandlerRequest(t, router, `{"model":"native","store":false,"stream":true,"input":"hello"}`)
		server.Close()
		wantCalls := int32(1)
		if enabled {
			wantCalls = 2
		}
		if w.Code != http.StatusOK || calls.Load() != wantCalls || (enabled && !strings.Contains(w.Body.String(), "FINAL")) {
			t.Fatalf("enabled=%t status=%d calls=%d body=%s", enabled, w.Code, calls.Load(), w.Body.String())
		}
	}
}

func TestNativeResponsesStreamPreservesEventsWithoutContinuation(t *testing.T) {
	const stream = ": upstream comment\r\nevent:response.created\r\ndata:{\"type\":\"response.created\",\"sequence_number\":71,\"response\":{\"id\":\"resp_native\"}}\r\n\r\n" +
		"event: response.output_item.added\r\ndata: {\"type\":\"response.output_item.added\",\"output_index\":3,\"item\":{\"id\":\"rs1\",\"type\":\"reasoning\",\"encrypted_content\":\"sealed\"}}\r\n\r\n" +
		"data: {\"type\":\"response.completed\",\"sequence_number\":99,\"response\":{\"id\":\"resp_native\",\"status\":\"completed\",\"output\":[],\"future\":9007199254740993,\"usage\":{\"input_tokens\":0,\"output_tokens\":516,\"output_tokens_details\":{\"reasoning_tokens\":516}}}}\r\n\r\n"
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, stream)
	}))
	defer server.Close()
	router := newResponsesTestRouter(t, config.UpstreamConfig{BaseURL: server.URL, APIKeys: []string{"key"}, ServiceType: "responses"}, nil)
	w := performResponsesHandlerRequest(t, router, `{"model":"native","store":false,"stream":true,"input":"hello"}`)
	if w.Code != http.StatusOK || calls.Load() != 1 || w.Body.String() != stream {
		t.Fatalf("native stream changed: status=%d calls=%d body=%q", w.Code, calls.Load(), w.Body.String())
	}
}
