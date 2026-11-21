package responses

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/conversation"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
	"github.com/tidwall/gjson"
)

const subagentMetadata = `"client_metadata":{"x-openai-subagent":"review","x-codex-parent-thread-id":"parent"}`

func saveContinuationPolicy(t *testing.T, cm *config.ConfigManager, model string, closeSource bool) {
	t.Helper()
	view, err := cm.GetSubagentRouting("responses")
	if err != nil {
		t.Fatal(err)
	}
	upstreams := cm.GetConfig().ResponsesUpstream
	view.Policy.Model = model
	view.Policy.Channels = []config.SubagentRoutingChannel{
		{ChannelUID: upstreams[1].ChannelUID, Enabled: true},
		{ChannelUID: upstreams[0].ChannelUID, Enabled: !closeSource},
	}
	if model == "sol" {
		view.Policy.Channels[0], view.Policy.Channels[1] = view.Policy.Channels[1], view.Policy.Channels[0]
	}
	if _, err := cm.UpdateSubagentRouting("responses", view.Revision, view.Policy); err != nil {
		t.Fatal(err)
	}
}

func TestSubagentResponsesHTTPContinuationRestoresActualModelAndRejectsClosedSource(t *testing.T) {
	gin.SetMode(gin.TestMode)
	models := make(chan string, 4)
	var alternateCalls atomic.Int32
	const response = `{"id":"resp_ok","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}],"usage":{"input_tokens":1,"output_tokens":1}}`
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		models <- gjson.GetBytes(body, "model").String()
		if sealed := gjson.GetBytes(body, "input.0.encrypted_content"); sealed.Exists() && sealed.String() != "sealed+/==" {
			t.Error("密文被修改")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, response)
	}))
	defer source.Close()
	alternate := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		alternateCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, response)
	}))
	defer alternate.Close()
	cm, sch := newEncryptedAffinityTestScheduler(t, []config.UpstreamConfig{
		{Name: "source", BaseURL: source.URL, APIKeys: []string{"a"}, ServiceType: "responses", Status: "active", ModelMapping: map[string]string{"sol": "cheap-original", "astra": "strong"}},
		{Name: "alternate", BaseURL: alternate.URL, APIKeys: []string{"b"}, ServiceType: "responses", Status: "active"},
	})
	tracker := conversation.NewConversationTracker(time.Hour, 24*time.Hour)
	defer tracker.Stop()
	sch.SetConversationTracker(tracker)
	saveContinuationPolicy(t, cm, "sol", false)
	router := gin.New()
	router.POST("/v1/responses", Handler(&config.EnvConfig{ProxyAccessKey: "secret", MaxRequestBodySize: 1 << 20}, cm, session.NewSessionManager(time.Hour, 100, 100000), sch))
	request := func(encrypted bool, want int) {
		t.Helper()
		input := `[{"role":"user","content":"hello"}]`
		if encrypted {
			input = `[{"type":"reasoning","encrypted_content":"sealed+/=="},{"role":"user","content":"continue"}]`
		}
		r := httptest.NewRequest(http.MethodPost, "/v1/responses", strings.NewReader(`{"model":"astra","input":`+input+`,`+subagentMetadata+`}`))
		r.Header.Set("x-api-key", "secret")
		r.Header.Set("Session_id", "http-child")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
		}
	}
	request(false, 200)
	if model := <-models; model != "cheap-original" {
		t.Fatal(model)
	}
	if saved, ok := tracker.GetSuccessfulChannel("responses", "http-child", "subagent"); !ok || saved.Model != "cheap-original" || saved.RequestModel != "sol" {
		t.Fatalf("HTTP 成功模型未正确记录: %+v, conversations=%+v", saved, tracker.GetActiveConversations("responses"))
	}
	mapping := map[string]string{"sol": "cheap-new", "luna": "new-model", "astra": "strong"}
	if _, err := cm.UpdateResponsesUpstream(0, config.UpstreamUpdate{ModelMapping: mapping}); err != nil {
		t.Fatal(err)
	}
	saveContinuationPolicy(t, cm, "luna", false)
	request(true, 200)
	if model := <-models; model != "cheap-original" {
		t.Fatalf("续轮改模: %s", model)
	}
	saveContinuationPolicy(t, cm, "luna", true)
	request(true, 503)
	if alternateCalls.Load() != 0 {
		t.Fatal("密文被发到备用渠道")
	}
	request(false, 200)
	if alternateCalls.Load() != 1 {
		t.Fatal("新任务未采用新策略")
	}
}

func TestSubagentResponsesNativeWebSocketReconnectRestoresSuccessfulSource(t *testing.T) {
	gin.SetMode(gin.TestMode)
	models := make(chan string, 2)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		_, body, err := conn.ReadMessage()
		if err != nil {
			t.Error(err)
			return
		}
		models <- gjson.GetBytes(body, "model").String()
		if gjson.GetBytes(body, "previous_response_id").Exists() && gjson.GetBytes(body, "previous_response_id").String() != "resp_ws" {
			t.Error("previous_response_id 被修改")
		}
		_ = conn.WriteJSON(map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_ws", "status": "completed"}})
	}))
	defer upstream.Close()
	cm, sch := newEncryptedAffinityTestScheduler(t, []config.UpstreamConfig{
		{Name: "source", BaseURL: upstream.URL, APIKeys: []string{"a"}, ServiceType: "responses", Status: "active", ModelMapping: map[string]string{"sol": "cheap-ws", "astra": "strong"}},
		{Name: "alternate", BaseURL: "http://127.0.0.1:1", APIKeys: []string{"b"}, ServiceType: "responses", Status: "active"},
	})
	tracker := conversation.NewConversationTracker(time.Hour, 24*time.Hour)
	defer tracker.Stop()
	sch.SetConversationTracker(tracker)
	saveContinuationPolicy(t, cm, "sol", false)
	router := gin.New()
	router.GET("/v1/responses", WebSocketHandler(&config.EnvConfig{ProxyAccessKey: "secret", MaxRequestBodySize: 1 << 20}, cm, session.NewSessionManager(time.Hour, 100, 100000), sch))
	server := httptest.NewServer(router)
	defer server.Close()
	request := func(previous string, want string) {
		t.Helper()
		conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+"/v1/responses", http.Header{"x-api-key": []string{"secret"}, "Session_id": []string{"ws-child"}})
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		payload := fmt.Sprintf(`{"type":"response.create","model":"astra","input":"hello",%s}`, subagentMetadata)
		var body map[string]any
		_ = json.Unmarshal([]byte(payload), &body)
		if previous != "" {
			body["previous_response_id"] = previous
		}
		if err := conn.WriteJSON(body); err != nil {
			t.Fatal(err)
		}
		_ = readWebSocketJSONUntilType(t, conn, want)
	}
	request("", "response.completed")
	deadline := time.Now().Add(time.Second)
	for {
		source, ok := tracker.GetSuccessfulChannel("responses", "ws-child", "subagent")
		if ok && source.Model == "cheap-ws" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("WS 成功来源未保存: %+v", source)
		}
		time.Sleep(time.Millisecond)
	}
	saveContinuationPolicy(t, cm, "luna", false)
	request("resp_ws", "response.completed")
	for i := 0; i < 2; i++ {
		if got := <-models; got != "cheap-ws" {
			t.Fatalf("WS 改模: %s", got)
		}
	}
	saveContinuationPolicy(t, cm, "luna", true)
	request("resp_ws", "error")
}
