package handlers_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/handlers/chat"
	"github.com/BenedictKing/ccx/internal/handlers/common"
	"github.com/BenedictKing/ccx/internal/handlers/gemini"
	"github.com/BenedictKing/ccx/internal/handlers/images"
	"github.com/BenedictKing/ccx/internal/handlers/messages"
	"github.com/BenedictKing/ccx/internal/handlers/responses"
	"github.com/BenedictKing/ccx/internal/handlers/vectors"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/gin-gonic/gin"
	"github.com/tidwall/gjson"
)

func TestRoutingObservationRealProxyRequests(t *testing.T) {
	for _, tc := range []struct{ kind, path, body, response, service string }{
		{"messages", "/v1/messages", `{"model":"client","max_tokens":20,"messages":[{"role":"user","content":"hi"}]}`, `{"id":"msg_1","type":"message","role":"assistant","model":"actual","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`, "claude"},
		{"chat", "/v1/chat/completions", `{"model":"client","messages":[{"role":"user","content":"hi"}]}`, `{"id":"chat_1","object":"chat.completion","model":"actual","choices":[{"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`, "openai"},
		{"responses", "/v1/responses", `{"model":"client","input":"hi"}`, `{"id":"resp_1","object":"response","status":"completed","model":"actual","output":[{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"output_text","text":"hello"}]}],"usage":{"input_tokens":1,"output_tokens":1}}`, "responses"},
		{"gemini", "/v1beta/models/client:generateContent", `{"contents":[{"role":"user","parts":[{"text":"hi"}]}]}`, `{"candidates":[{"content":{"role":"model","parts":[{"text":"hello"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1}}`, "gemini"},
		{"images", "/v1/images/generations", `{"model":"client","prompt":"test","n":1}`, `{"created":1,"data":[{"b64_json":"aGVsbG8="}]}`, "openai"},
		{"vectors", "/v1/embeddings", `{"model":"client","input":"hello"}`, `{"object":"list","data":[{"object":"embedding","index":0,"embedding":[0.1,0.2]}],"model":"actual","usage":{"prompt_tokens":1,"total_tokens":1}}`, "openai"},
	} {
		t.Run(tc.kind, func(t *testing.T) {
			var mu sync.Mutex
			var received []string
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				model := gjson.GetBytes(body, "model").String()
				if tc.kind == "gemini" {
					index := strings.LastIndex(r.URL.Path, "/models/")
					if index >= 0 {
						model = strings.SplitN(r.URL.Path[index+8:], ":", 2)[0]
					}
				}
				mu.Lock()
				received = append(received, model)
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				if r.Header.Get("Authorization") == "Bearer bad-key" || r.Header.Get("x-api-key") == "bad-key" || r.Header.Get("x-goog-api-key") == "bad-key" || r.URL.Query().Get("key") == "bad-key" {
					w.WriteHeader(503)
					_, _ = w.Write([]byte(`{"error":{"message":"temporary error"}}`))
					return
				}
				_, _ = w.Write([]byte(tc.response))
			}))
			defer upstream.Close()
			channel := config.UpstreamConfig{Name: "test", BaseURL: upstream.URL, ServiceType: tc.service, Status: "active", CircuitBreakerDisabled: true, APIKeys: []string{"bad-key", "good-key"}, ModelMapping: map[string]string{"client": "actual"}}
			cfg := config.Config{FuzzyModeEnabled: true}
			switch tc.kind {
			case "messages":
				cfg.Upstream = []config.UpstreamConfig{channel}
			case "chat":
				cfg.ChatUpstream = []config.UpstreamConfig{channel}
			case "responses":
				cfg.ResponsesUpstream = []config.UpstreamConfig{channel}
			case "gemini":
				cfg.GeminiUpstream = []config.UpstreamConfig{channel}
			case "images":
				cfg.ImagesUpstream = []config.UpstreamConfig{channel}
			case "vectors":
				cfg.VectorsUpstream = []config.UpstreamConfig{channel}
			}
			path := filepath.Join(t.TempDir(), "config.json")
			payload, _ := json.Marshal(cfg)
			if err := os.WriteFile(path, payload, 0600); err != nil {
				t.Fatal(err)
			}
			cm, err := config.NewConfigManager(path, "")
			if err != nil {
				t.Fatal(err)
			}
			defer cm.Close()
			manager := metrics.NewMetricsManager()
			defer manager.Stop()
			sch := scheduler.NewChannelScheduler(cm, manager, manager, manager, manager, manager, session.NewTraceAffinityManager(), nil, manager)
			env := config.NewEnvConfig()
			env.ProxyAccessKey = "proxy-test"
			env.EnableRequestLogs = false
			sessions := session.NewSessionManager(time.Hour, 100, 10000)
			var handler gin.HandlerFunc
			switch tc.kind {
			case "messages":
				handler = messages.Handler(env, cm, sch)
			case "chat":
				handler = chat.Handler(env, cm, sch)
			case "responses":
				handler = responses.Handler(env, cm, sessions, sch)
			case "gemini":
				handler = gemini.Handler(env, cm, sch)
			case "images":
				handler = images.Handler(env, cm, sch)
			case "vectors":
				handler = vectors.Handler(env, cm, sch)
			}
			store, _ := metrics.NewRoutingStore(nil)
			defer store.Close()
			router := gin.New()
			router.Use(common.RoutingStoreMiddleware(store))
			route := tc.path
			if tc.kind == "gemini" {
				route = "/v1beta/models/*modelAction"
			}
			router.POST(route, handler)
			req := httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer proxy-test")
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, req)
			if recorder.Code != 200 {
				t.Fatalf("proxy=%d %s", recorder.Code, recorder.Body.String())
			}
			requests, _ := store.Snapshot(tc.kind, time.Now().Add(-time.Hour), time.Now().Add(time.Second))
			if len(requests) != 1 {
				t.Fatalf("requests=%+v", requests)
			}
			request := requests[0]
			mu.Lock()
			count := len(received)
			mu.Unlock()
			if count != 2 || len(request.Attempts) != count || request.Status != metrics.RoutingStatusSuccess || request.RequestedModel != "client" {
				t.Fatalf("request=%+v upstream count=%d", request, count)
			}
			if request.Attempts[0].Status != metrics.RoutingStatusFailed || request.Attempts[1].Status != metrics.RoutingStatusSuccess {
				t.Fatalf("attempts=%+v", request.Attempts)
			}
			for i, a := range request.Attempts {
				if a.SentModel != "actual" || a.ChannelUID == "" || a.MappingOutcome != metrics.MappingOutcomeChanged {
					t.Fatalf("attempt %d=%+v", i, a)
				}
			}
			distribution, _ := metrics.AggregateRouting(requests, metrics.RoutingFilter{Basis: "successful_requests"})
			if distribution.Summary.Requests != 1 || distribution.Summary.Attempts != 2 || distribution.Summary.MatchedCount != 1 {
				t.Fatalf("summary=%+v", distribution.Summary)
			}
			// 鉴权失败不能混入业务请求计数。
			denied := httptest.NewRecorder()
			router.ServeHTTP(denied, httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body)))
			requests, _ = store.Snapshot(tc.kind, time.Now().Add(-time.Hour), time.Now().Add(time.Second))
			if len(requests) != 1 {
				t.Fatal("unauthorized request counted")
			}
		})
	}
}
