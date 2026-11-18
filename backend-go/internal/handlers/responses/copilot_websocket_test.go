package responses

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/copilot"
	"github.com/gorilla/websocket"
	"github.com/tidwall/gjson"
)

func TestCopilotWebSocketKeepsNativeContinuation(t *testing.T) {
	const output = `{"type":"response.completed","response":{"id":"resp_1","status":"completed"},"future":9007199254740993}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer runtime" {
			t.Errorf("错误的 Copilot 认证: %v", r.Header)
		}
		if r.URL.Path == "/models" {
			_, _ = fmt.Fprint(w, `{"data":[{"id":"native","supported_endpoints":["/responses","ws:/responses"]}]}`)
			return
		}
		if r.URL.Path != "/responses" || r.Header.Get("OpenAI-Beta") != responsesWebSocketV2BetaHeaderValue {
			t.Errorf("错误的 WebSocket 端点或头: %s %v", r.URL, r.Header)
		}
		conn, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = conn.Close() }()
		for range 2 {
			_, body, err := conn.ReadMessage()
			if err != nil {
				t.Error(err)
				return
			}
			if gjson.GetBytes(body, "previous_response_id").String() != "previous" || gjson.GetBytes(body, "input.0.encrypted_content").String() != "opaque" || gjson.GetBytes(body, "future").Raw != "9007199254740993" {
				t.Errorf("续轮被修改: %s", body)
			}
			if err := conn.WriteMessage(websocket.TextMessage, []byte(output)); err != nil {
				t.Error(err)
				return
			}
		}
	}))
	defer upstream.Close()
	resolver := copilot.NewModelResolver(upstream.Client(), func(context.Context, string, string) (string, string, error) { return "runtime", upstream.URL, nil })
	router := newResponsesWebSocketTestRouter(t, config.UpstreamConfig{Name: "copilot", ServiceType: "copilot", BaseURL: upstream.URL, APIKeys: []string{"oauth"}, Status: "active"})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		router.ServeHTTP(w, r.WithContext(copilot.WithModelResolver(r.Context(), resolver)))
	}))
	defer server.Close()
	header := http.Header{"Authorization": []string{"Bearer secret-key"}}
	conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/v1/responses", header)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := conn.WriteMessage(websocket.TextMessage, []byte(`{"type":"response.create","model":"native","previous_response_id":"previous","input":[{"type":"reasoning","encrypted_content":"opaque"}],"future":9007199254740993}`)); err != nil {
			t.Fatal(err)
		}
		_, got, err := conn.ReadMessage()
		if err != nil || string(got) != output {
			t.Fatalf("原生响应被修改: %s err=%v", got, err)
		}
	}
}

func TestCopilotExplicitReasoningFoldKeepsProtectedAuthentication(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		var calls atomic.Int32
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Header.Get("Authorization") != "Bearer runtime" || r.Header.Get("Copilot-Integration-Id") != "vscode-chat" {
				t.Errorf("续轮认证被覆盖: %v", r.Header)
			}
			if r.URL.Path == "/models" {
				_, _ = fmt.Fprint(w, `{"data":[{"id":"native","supported_endpoints":["/responses"]}]}`)
				return
			}
			if r.URL.Path != "/responses" {
				t.Errorf("续轮端点错误: %s", r.URL)
			}
			w.Header().Set("Content-Type", "text/event-stream")
			if calls.Add(1) == 1 {
				_, _ = fmt.Fprint(w, responsesFoldReasoningRound("r1", responsesFoldStep-2, "FIRST", true))
			} else {
				_, _ = fmt.Fprint(w, responsesFoldReasoningRound("r2", 20, "FINAL", true))
			}
		}))
		resolver := copilot.NewModelResolver(upstream.Client(), func(context.Context, string, string) (string, string, error) { return "runtime", upstream.URL, nil })
		router := newResponsesTestRouter(t, config.UpstreamConfig{ServiceType: "copilot", BaseURL: upstream.URL, APIKeys: []string{"oauth"}, ResponsesReasoningFold: enabled, CustomHeaders: map[string]string{"Authorization": "Bearer wrong", "Copilot-Integration-Id": "wrong"}}, nil)
		req := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"native","stream":true,"store":false,"input":"hello"}`)).WithContext(copilot.WithModelResolver(context.Background(), resolver))
		req.Header.Set("Authorization", "Bearer secret-key")
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		upstream.Close()
		want := int32(1)
		if enabled {
			want = 2
		}
		if w.Code != 200 || calls.Load() != want {
			t.Fatalf("enabled=%t status=%d calls=%d body=%s", enabled, w.Code, calls.Load(), w.Body.String())
		}
	}
}
