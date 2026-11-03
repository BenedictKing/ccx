package responses

import (
	"bufio"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/session"
)

// 上游等待客户端收到正文后才结束；若代理整轮缓存，此测试会超时。
func TestResponsesHandlerStreamsTextBeforeUpstreamCompletes(t *testing.T) {
	release := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, formatResponsesFoldSSE(map[string]interface{}{
			"type": "response.output_item.added", "output_index": 0,
			"item": map[string]interface{}{"type": "message", "id": "msg_1", "role": "assistant"},
		}))
		for _, text := range []string{"雨", "天"} {
			_, _ = io.WriteString(w, formatResponsesFoldSSE(map[string]interface{}{
				"type": "response.output_text.delta", "output_index": 0, "delta": text,
			}))
		}
		w.(http.Flusher).Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		_, _ = io.WriteString(w, formatResponsesFoldSSE(map[string]interface{}{
			"type": "response.completed", "response": map[string]interface{}{
				"id": "resp_1", "status": "completed", "output": []interface{}{},
				"usage": map[string]interface{}{"input_tokens": 10, "output_tokens": 2, "total_tokens": 12},
			},
		}))
	}))
	defer upstream.Close()
	router := newResponsesTestRouter(t, config.UpstreamConfig{
		BaseURL: upstream.URL, APIKeys: []string{"test-key"}, ServiceType: "responses",
	}, session.NewSessionManager(time.Hour, 100, 100000))
	proxy := httptest.NewServer(router)
	defer proxy.Close()
	defer close(release)
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest(http.MethodPost, proxy.URL+"/v1/responses", strings.NewReader(`{"model":"gpt-6.1-sol","stream":true,"input":"你好"}`))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "secret-key")
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		var event map[string]interface{}
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data:")), &event) == nil && event["type"] == "response.output_text.delta" {
			return
		}
	}
	t.Fatalf("上游结束前未收到正文 delta: %v", scanner.Err())
}

func TestResponsesHandlerFoldingRequiresExplicitOptIn(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		name := "默认实时转发"
		if enabled {
			name = "显式开启续接"
		}
		t.Run(name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, responsesFoldReasoningRound("rs_1", 404, "正文", true))
			}))
			defer upstream.Close()
			router := newResponsesTestRouter(t, config.UpstreamConfig{
				BaseURL: upstream.URL, APIKeys: []string{"test-key"}, ServiceType: "responses",
				ResponsesReasoningFold: enabled, Status: "active", Priority: 1,
			}, session.NewSessionManager(time.Hour, 100, 100000))
			w := performResponsesHandlerRequest(t, router, `{"model":"gpt-6.1-sol","stream":true,"input":"你好"}`)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d: %s", w.Code, w.Body.String())
			}
			if got := strings.Contains(w.Body.String(), "proxy_rounds"); got != enabled {
				t.Fatalf("folding metadata = %v, want %v", got, enabled)
			}
			if !strings.Contains(w.Body.String(), "正文") {
				t.Fatal("正文丢失")
			}
		})
	}
}

func TestResponsesStreamToolRemapperPreservesTextAndRestoresTools(t *testing.T) {
	r := newResponsesStreamToolRemapper(foldCodexTestContext())
	send := func(event map[string]interface{}) []string {
		t.Helper()
		data, _ := json.Marshal(event)
		return r.processLine("data: " + string(data))
	}
	added := map[string]interface{}{"id": "fc_1", "type": "function_call", "name": "functions__exec", "call_id": "call_1", "arguments": ""}
	if got := send(map[string]interface{}{"type": "response.output_item.added", "output_index": 1, "item": added}); len(got) != 0 {
		t.Fatalf("工具完成前已输出: %v", got)
	}
	if got := send(map[string]interface{}{"type": "response.function_call_arguments.delta", "output_index": 1, "delta": `{"input":"console.log(1)"}`}); len(got) != 0 {
		t.Fatalf("工具参数未聚合: %v", got)
	}
	text := send(map[string]interface{}{"type": "response.output_text.delta", "output_index": 2, "delta": "正文"})
	if len(text) != 1 || !strings.Contains(text[0], "正文") {
		t.Fatalf("正文被工具缓存阻塞: %v", text)
	}
	done := map[string]interface{}{"id": "fc_1", "type": "function_call", "name": "functions__exec", "call_id": "call_1", "arguments": `{"input":"console.log(1)"}`, "status": "completed"}
	got := strings.Join(send(map[string]interface{}{"type": "response.output_item.done", "output_index": 1, "item": done}), "")
	if strings.Count(got, "event: response.custom_tool_call_input.delta\n") != 1 || strings.Contains(got, "functions__exec") || !strings.Contains(got, "console.log(1)") {
		t.Fatalf("工具还原错误或重复: %s", got)
	}
	terminal := strings.Join(send(map[string]interface{}{"type": "response.completed", "response": map[string]interface{}{"output": []interface{}{done}}}), "")
	if strings.Contains(terminal, "functions__exec") || !strings.Contains(terminal, `"type":"custom_tool_call"`) {
		t.Fatalf("最终 output 未还原: %s", terminal)
	}
}
