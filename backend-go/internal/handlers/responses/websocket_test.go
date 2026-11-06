package responses

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func TestNormalizeWebSocketResponseCreatePayload(t *testing.T) {
	body, warmup, err := normalizeWebSocketResponseCreatePayload([]byte(`{
		"type":"response.create",
		"model":"gpt-5",
		"input":"hello",
		"stream":false,
		"generate":false,
		"client_metadata":{"x-codex-installation-id":"install"}
	}`))
	if err != nil {
		t.Fatalf("normalizeWebSocketResponseCreatePayload() err = %v", err)
	}
	if !warmup {
		t.Fatal("warmup = false, want true")
	}

	var got map[string]interface{}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal normalized body: %v", err)
	}
	for _, key := range []string{"type", "generate", "client_metadata"} {
		if _, ok := got[key]; ok {
			t.Fatalf("normalized body still contains %q: %s", key, string(body))
		}
	}
	if got["stream"] != true {
		t.Fatalf("stream = %v, want true", got["stream"])
	}
}

func TestNormalizeNativeWebSocketResponseCreatePayload_PreservesV2Fields(t *testing.T) {
	body, err := normalizeNativeWebSocketResponseCreatePayload([]byte(`{
		"type":"response.create",
		"model":"gpt-5",
		"input":"hello",
		"stream":false,
		"generate":false,
		"previous_response_id":"resp-1",
		"client_metadata":{"x-codex-installation-id":"install"}
	}`))
	if err != nil {
		t.Fatalf("normalizeNativeWebSocketResponseCreatePayload() err = %v", err)
	}

	var got map[string]interface{}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("unmarshal native body: %v", err)
	}
	for _, key := range []string{"type", "generate", "previous_response_id", "client_metadata"} {
		if _, ok := got[key]; !ok {
			t.Fatalf("native body missing %q: %s", key, string(body))
		}
	}
	if got["stream"] != true {
		t.Fatalf("stream = %v, want true", got["stream"])
	}
}

func TestResponsesWebSocketHandler_ResponsesUpstreamKeepsNativeV2Continuation(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstreamBody := make(chan map[string]interface{}, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/responses" {
			t.Errorf("upstream path = %s, want /v1/responses", r.URL.Path)
		}
		if got := r.Header.Get(responsesWebSocketV2BetaHeader); got != responsesWebSocketV2BetaHeaderValue {
			t.Errorf("OpenAI-Beta = %q, want WebSocket v2", got)
		}
		upgrader := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade upstream: %v", err)
			return
		}
		defer conn.Close()
		for i := 0; i < 2; i++ {
			var req map[string]interface{}
			if err := conn.ReadJSON(&req); err != nil {
				t.Errorf("read upstream request: %v", err)
				return
			}
			upstreamBody <- req
			if err := conn.WriteJSON(map[string]interface{}{"type": "response.completed", "response": map[string]interface{}{"id": "resp-1", "status": "completed"}}); err != nil {
				t.Errorf("write upstream response: %v", err)
				return
			}
		}
	}))
	defer upstream.Close()

	router := newResponsesWebSocketTestRouter(t, config.UpstreamConfig{
		Name:        "responses-ws-test",
		BaseURL:     upstream.URL,
		APIKeys:     []string{"sk-test"},
		ServiceType: "responses",
		Status:      "active",
	})
	server := httptest.NewServer(router)
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/v1/responses", http.Header{
		"x-api-key": []string{"secret-key"},
	})
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()

	err = conn.WriteJSON(map[string]interface{}{
		"type":            "response.create",
		"model":           "gpt-5",
		"input":           "hello",
		"stream":          false,
		"generate":        true,
		"client_metadata": map[string]interface{}{"x-codex-installation-id": "install"},
	})
	if err != nil {
		t.Fatalf("write websocket request: %v", err)
	}

	completed := readWebSocketJSONUntilType(t, conn, "response.completed")
	if completed["type"] != "response.completed" {
		t.Fatalf("terminal event type = %v, want response.completed", completed["type"])
	}
	if err := conn.WriteJSON(map[string]interface{}{
		"type": "response.create", "model": "gpt-5", "previous_response_id": "resp-1",
		"input": []interface{}{map[string]interface{}{"type": "function_call_output", "call_id": "call-1", "output": "Fri"}},
	}); err != nil {
		t.Fatalf("write continuation: %v", err)
	}
	_ = readWebSocketJSONUntilType(t, conn, "response.completed")

	first := <-upstreamBody
	second := <-upstreamBody
	if first["type"] != "response.create" || second["previous_response_id"] != "resp-1" {
		t.Fatalf("native continuation was not forwarded: first=%v second=%v", first, second)
	}
}

func TestResponsesWebSocketHandler_NonResponsesUpstreamKeepsHTTPBridge(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstreamBody := make(chan map[string]interface{}, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("read upstream request: %v", err)
		}
		var req map[string]interface{}
		if err := json.Unmarshal(body, &req); err != nil {
			t.Fatalf("unmarshal upstream request: %v", err)
		}
		upstreamBody <- req

		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"created\":123,\"model\":\"gpt-5\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"\"},\"finish_reason\":null}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"created\":123,\"model\":\"gpt-5\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"},\"finish_reason\":null}]}\n\n"))
		_, _ = w.Write([]byte("data: {\"id\":\"chatcmpl-1\",\"object\":\"chat.completion.chunk\",\"created\":123,\"model\":\"gpt-5\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\n"))
		_, _ = w.Write([]byte("data: [DONE]\n\n"))
		if flusher != nil {
			flusher.Flush()
		}
	}))
	defer upstream.Close()

	router := newResponsesWebSocketTestRouter(t, config.UpstreamConfig{
		Name:        "responses-ws-bridge-test",
		BaseURL:     upstream.URL,
		APIKeys:     []string{"sk-test"},
		ServiceType: "openai",
		Status:      "active",
	})
	server := httptest.NewServer(router)
	defer server.Close()

	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/v1/responses", http.Header{
		"x-api-key": []string{"secret-key"},
	})
	if err != nil {
		t.Fatalf("dial websocket: %v", err)
	}
	defer conn.Close()

	if err := conn.WriteJSON(map[string]interface{}{
		"type":            "response.create",
		"model":           "gpt-5",
		"input":           "hello",
		"stream":          false,
		"generate":        true,
		"client_metadata": map[string]interface{}{"x-codex-installation-id": "install"},
	}); err != nil {
		t.Fatalf("write websocket request: %v", err)
	}

	_ = readWebSocketJSONUntilType(t, conn, "response.output_text.delta")
	completed := readWebSocketJSONUntilType(t, conn, "response.completed")
	if completed["type"] != "response.completed" {
		t.Fatalf("terminal event type = %v, want response.completed", completed["type"])
	}

	select {
	case req := <-upstreamBody:
		for _, key := range []string{"type", "generate", "client_metadata"} {
			if _, ok := req[key]; ok {
				t.Fatalf("bridge upstream request still contains %q: %#v", key, req)
			}
		}
		if req["stream"] != true {
			t.Fatalf("bridge upstream stream = %v, want true", req["stream"])
		}
	case <-time.After(time.Second):
		t.Fatal("HTTP bridge upstream did not receive request")
	}
}

func newResponsesWebSocketTestRouter(t *testing.T, upstream config.UpstreamConfig) *gin.Engine {
	t.Helper()
	cfgManager := setupResponsesTestConfigManager(t, []config.UpstreamConfig{upstream})
	channelScheduler := scheduler.NewChannelScheduler(
		cfgManager,
		metrics.NewMetricsManager(),
		metrics.NewMetricsManager(),
		metrics.NewMetricsManager(),
		metrics.NewMetricsManager(),
		metrics.NewMetricsManager(),
		session.NewTraceAffinityManager(),
		nil,
	)
	envCfg := &config.EnvConfig{
		ProxyAccessKey:     "secret-key",
		MaxRequestBodySize: 1024 * 1024,
	}

	r := gin.New()
	r.GET("/v1/responses", WebSocketHandler(envCfg, cfgManager, session.NewSessionManager(time.Hour, 100, 100000), channelScheduler))
	return r
}

func readWebSocketJSON(t *testing.T, conn *websocket.Conn) map[string]interface{} {
	t.Helper()
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	var event map[string]interface{}
	if err := conn.ReadJSON(&event); err != nil {
		t.Fatalf("read websocket json: %v", err)
	}
	return event
}

func readWebSocketJSONUntilType(t *testing.T, conn *websocket.Conn, wantType string) map[string]interface{} {
	t.Helper()
	for i := 0; i < 10; i++ {
		event := readWebSocketJSON(t, conn)
		if event["type"] == wantType {
			return event
		}
	}
	t.Fatalf("websocket event type %q not received", wantType)
	return nil
}
