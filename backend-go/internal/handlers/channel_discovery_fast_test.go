package handlers

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func channelDiscoveryFastForTest() gin.HandlerFunc {
	return ChannelDiscoveryFast(nil)
}

// TestChannelDiscoveryFastRecommendsChatFromRealNIMModels 验证：
// 上游返回真实 NIM 模型清单 + chat 协议成功 → primaryKind=chat，testedModel 为真实模型名，
// testedKeyHash 非空，不返回明文 key。
func TestChannelDiscoveryFastRecommendsChatFromRealNIMModels(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"meta/llama-3.1-8b-instruct"},{"id":"nim-main"}]}`))
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"content":"ok"}}]}` + "\n\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	router := gin.New()
	router.POST("/api/channel-discovery-fast", channelDiscoveryFastForTest())

	body := []byte(`{"baseUrls":["` + upstream.URL + `"],"apiKeys":["sk-test-nim"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/channel-discovery-fast", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var resp ChannelDiscoveryFastResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.PrimaryKind != "chat" {
		t.Fatalf("primaryKind=%q, want chat", resp.PrimaryKind)
	}
	if resp.TestedModel == "" || !strings.HasPrefix(resp.TestedModel, "meta/") && resp.TestedModel != "nim-main" {
		t.Fatalf("testedModel=%q, want a real NIM model", resp.TestedModel)
	}
	if resp.TestedKeyHash == "" {
		t.Fatal("testedKeyHash is empty")
	}
	if strings.Contains(recorder.Body.String(), "sk-test-nim") {
		t.Fatalf("response leaks plaintext api key: %s", recorder.Body.String())
	}
}

// TestChannelDiscoveryFastNoModelsNoManifestReturnsError 验证：
// models 端点失败且无已知 manifest 兜底时返回 4xx，不建渠道，不泄露 key。
func TestChannelDiscoveryFastNoModelsNoManifestReturnsError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer upstream.Close()

	router := gin.New()
	router.POST("/api/channel-discovery-fast", channelDiscoveryFastForTest())

	body := []byte(`{"baseUrls":["` + upstream.URL + `"],"apiKeys":["sk-leak"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/channel-discovery-fast", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code == http.StatusOK {
		t.Fatalf("expected non-200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
	if strings.Contains(recorder.Body.String(), "sk-leak") {
		t.Fatalf("error response leaks plaintext api key: %s", recorder.Body.String())
	}
}

// TestChannelDiscoveryFastSecondKeySucceedsWhenFirstFails 验证：
// 第一组凭证 models 失败（401）但第二组成功时，使用第二组完成探测，不固定只用 apiKeys[0]。
func TestChannelDiscoveryFastSecondKeySucceedsWhenFirstFails(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		switch r.URL.Path {
		case "/v1/models":
			if auth == "Bearer sk-bad" {
				http.Error(w, `{"error":{"message":"invalid key"}}`, http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"nim-main"}]}`))
		case "/v1/chat/completions":
			if auth == "Bearer sk-bad" {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"content":"ok"}}]}` + "\n\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	router := gin.New()
	router.POST("/api/channel-discovery-fast", channelDiscoveryFastForTest())

	body := []byte(`{"baseUrls":["` + upstream.URL + `"],"apiKeys":["sk-bad","sk-good"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/channel-discovery-fast", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var resp ChannelDiscoveryFastResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.PrimaryKind != "chat" {
		t.Fatalf("primaryKind=%q, want chat", resp.PrimaryKind)
	}
}

// TestChannelDiscoveryFastDualProtocolRecommendsResponses 验证：
// responses 与 chat 同时成功时，推荐逻辑优先返回 responses（而非协议数组顺序首个）。
func TestChannelDiscoveryFastDualProtocolRecommendsResponses(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"nim-main"}]}`))
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"content":"ok"}}]}` + "\n\n"))
		case "/v1/responses":
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte("event: response.output_text.delta\ndata: {\"type\":\"response.output_text.delta\",\"delta\":\"ok\"}\n\n"))
			_, _ = w.Write([]byte("event: response.completed\ndata: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	router := gin.New()
	router.POST("/api/channel-discovery-fast", channelDiscoveryFastForTest())

	body := []byte(`{"baseUrls":["` + upstream.URL + `"],"apiKeys":["sk-test"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/channel-discovery-fast", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var resp ChannelDiscoveryFastResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.PrimaryKind != "responses" {
		t.Fatalf("primaryKind=%q, want responses", resp.PrimaryKind)
	}
}

// TestChannelDiscoveryFastFallsBackToSecondModel 验证：
// 第一个模型全部协议失败时，自动尝试下一个候选模型，第二个模型成功即返回 OK。
func TestChannelDiscoveryFastFallsBackToSecondModel(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			// sonnet 排在 Primary 位（含 "sonnet" 关键词），haiku 排在 Fast 位（含 "haiku" 关键词）
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"claude-sonnet-4-6"},{"id":"claude-haiku-4-5"}]}`))
		case "/v1/chat/completions":
			// 根据请求体判断模型：sonnet 返回错误，haiku 返回成功
			var body struct {
				Model string `json:"model"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if strings.Contains(body.Model, "sonnet") {
				http.Error(w, `{"error":{"message":"No available accounts","type":"api_error"}}`, http.StatusServiceUnavailable)
				return
			}
			w.Header().Set("Content-Type", "text/event-stream")
			_, _ = w.Write([]byte(`data: {"choices":[{"delta":{"content":"ok"}}]}` + "\n\n"))
		default:
			// messages/responses/gemini 对 sonnet 也返回错误
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	router := gin.New()
	router.POST("/api/channel-discovery-fast", channelDiscoveryFastForTest())

	body := []byte(`{"baseUrls":["` + upstream.URL + `"],"apiKeys":["sk-test"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/channel-discovery-fast", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var resp ChannelDiscoveryFastResponse
	if err := json.Unmarshal(recorder.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if resp.PrimaryKind != "chat" {
		t.Fatalf("primaryKind=%q, want chat", resp.PrimaryKind)
	}
	if !strings.Contains(resp.TestedModel, "haiku") {
		t.Fatalf("testedModel=%q, want haiku (second model fallback)", resp.TestedModel)
	}
}

// TestChannelDiscoveryFastAllProtocolsFailReturnsError 验证：
// 模型可获取但所有协议探测均失败时返回 4xx，不建渠道。
func TestChannelDiscoveryFastAllProtocolsFailReturnsError(t *testing.T) {
	gin.SetMode(gin.TestMode)

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/models":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"object":"list","data":[{"id":"nim-main"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer upstream.Close()

	router := gin.New()
	router.POST("/api/channel-discovery-fast", channelDiscoveryFastForTest())

	body := []byte(`{"baseUrls":["` + upstream.URL + `"],"apiKeys":["sk-test"]}`)
	req := httptest.NewRequest(http.MethodPost, "/api/channel-discovery-fast", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, req)

	if recorder.Code == http.StatusOK {
		t.Fatalf("expected non-200, got %d body=%s", recorder.Code, recorder.Body.String())
	}
}
