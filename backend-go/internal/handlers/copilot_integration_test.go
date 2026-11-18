package handlers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/copilot"
	"github.com/BenedictKing/ccx/internal/handlers/chat"
	"github.com/BenedictKing/ccx/internal/handlers/gemini"
	"github.com/BenedictKing/ccx/internal/handlers/messages"
	"github.com/BenedictKing/ccx/internal/handlers/responses"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

func TestCopilotModelEndpointHandlerMatrix(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, target := range []struct{ protocol, endpoint, body, stream string }{
		{"responses", "/responses", `{"id":"resp_1","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}`, "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\",\"output_index\":0,\"content_index\":0}\n\ndata: {\"type\":\"response.completed\",\"response\":{\"id\":\"resp_1\",\"status\":\"completed\",\"output\":[{\"type\":\"message\",\"role\":\"assistant\",\"content\":[{\"type\":\"output_text\",\"text\":\"hello\"}]}],\"usage\":{\"input_tokens\":1,\"output_tokens\":1,\"total_tokens\":2}}}\n\n"},
		{"chat", "/chat/completions", `{"id":"chat_1","model":"actual","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`, "data: {\"id\":\"chat_1\",\"model\":\"actual\",\"choices\":[{\"index\":0,\"delta\":{\"role\":\"assistant\",\"content\":\"hello\"},\"finish_reason\":null}]}\n\ndata: {\"id\":\"chat_1\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1,\"total_tokens\":2}}\n\ndata: [DONE]\n\n"},
		{"messages", "/v1/messages", `{"id":"msg_1","type":"message","role":"assistant","model":"actual","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"model\":\"actual\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\nevent: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\nevent: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello\"}}\n\nevent: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\nevent: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\nevent: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"},
	} {
		for _, entry := range []struct{ protocol, path, body, textPath, terminal string }{
			{"responses", "/v1/responses", `{"model":"alias","store":false,"stream":%t,"input":"hello"}`, "output.0.content.0.text", "response.completed"},
			{"chat", "/v1/chat/completions", `{"model":"alias","stream":%t,"messages":[{"role":"user","content":"hello"}]}`, "choices.0.message.content", "[DONE]"},
			{"messages", "/v1/messages", `{"model":"alias","stream":%t,"max_tokens":100,"messages":[{"role":"user","content":"hello"}]}`, "content.0.text", "message_stop"},
			{"gemini", "/v1beta/models/alias:generateContent", `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`, "candidates.0.content.parts.0.text", "finishReason"},
		} {
			for _, stream := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s_to_%s_stream%t", entry.protocol, target.protocol, stream), func(t *testing.T) {
					var inferenceCalls atomic.Int32
					upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.Header.Get("Authorization") != "Bearer runtime" || r.Header.Get("Copilot-Integration-Id") == "" || r.Header.Get("x-api-key") != "" {
							t.Errorf("Copilot 认证错误: %v", r.Header)
						}
						if r.URL.Path == "/models" {
							_, _ = fmt.Fprintf(w, `{"data":[{"id":"actual","supported_endpoints":[%q]}]}`, target.endpoint)
							return
						}
						inferenceCalls.Add(1)
						body, _ := io.ReadAll(r.Body)
						if r.URL.Path != target.endpoint || gjson.GetBytes(body, "model").String() != "actual" {
							t.Errorf("协议路径/模型错误: %s %s", r.URL, body)
						}
						w.Header().Set("Content-Type", "application/json")
						response := target.body
						if stream {
							w.Header().Set("Content-Type", "text/event-stream")
							response = target.stream
						}
						_, _ = io.WriteString(w, response)
					}))
					defer upstream.Close()
					resolver := copilot.NewModelResolver(upstream.Client(), func(context.Context, string, string) (string, string, error) { return "runtime", upstream.URL, nil })
					channel := config.UpstreamConfig{Name: "copilot", ServiceType: "copilot", BaseURL: upstream.URL, APIKeys: []string{"oauth"}, ModelMapping: map[string]string{"alias": "actual"}, Status: "active"}
					cfg := config.Config{Upstream: []config.UpstreamConfig{channel}, ChatUpstream: []config.UpstreamConfig{channel}, ResponsesUpstream: []config.UpstreamConfig{channel}, GeminiUpstream: []config.UpstreamConfig{channel}}
					data, err := json.Marshal(cfg)
					if err != nil {
						t.Fatal(err)
					}
					path := filepath.Join(t.TempDir(), "config.json")
					if err := os.WriteFile(path, data, 0600); err != nil {
						t.Fatal(err)
					}
					cm, err := config.NewConfigManager(path, "")
					if err != nil {
						t.Fatal(err)
					}
					defer func() { _ = cm.Close() }()
					s := scheduler.NewChannelScheduler(cm, metrics.NewMetricsManager(), metrics.NewMetricsManager(), metrics.NewMetricsManager(), metrics.NewMetricsManager(), metrics.NewMetricsManager(), session.NewTraceAffinityManager(), nil)
					env := &config.EnvConfig{ProxyAccessKey: "secret", MaxRequestBodySize: 1 << 20}
					router := gin.New()
					router.POST("/v1/messages", messages.Handler(env, cm, s))
					router.POST("/v1/responses", responses.Handler(env, cm, session.NewSessionManager(time.Hour, 100, 100000), s))
					router.POST("/v1/chat/completions", chat.Handler(env, cm, s))
					router.POST("/v1beta/models/*modelAction", gemini.Handler(env, cm, s))
					body := entry.body
					requestPath := entry.path
					if entry.protocol == "gemini" {
						if stream {
							requestPath = strings.ReplaceAll(requestPath, ":generateContent", ":streamGenerateContent") + "?alt=sse"
						}
					} else {
						body = fmt.Sprintf(body, stream)
					}
					req := httptest.NewRequest(http.MethodPost, requestPath, strings.NewReader(body)).WithContext(copilot.WithModelResolver(context.Background(), resolver))
					req.Header.Set("Authorization", "Bearer secret")
					req.Header.Set("Content-Type", "application/json")
					w := httptest.NewRecorder()
					router.ServeHTTP(w, req)
					if w.Code != http.StatusOK || inferenceCalls.Load() != 1 {
						t.Fatalf("status=%d calls=%d body=%s", w.Code, inferenceCalls.Load(), w.Body.String())
					}
					if stream {
						if !strings.Contains(w.Body.String(), "hello") || !strings.Contains(w.Body.String(), entry.terminal) {
							t.Fatalf("协议流内容/终态错误: %s", w.Body.String())
						}
					} else if gjson.Get(w.Body.String(), entry.textPath).String() != "hello" {
						t.Fatalf("协议响应错误: %s", w.Body.String())
					}
				})
			}
		}
	}
}
