package gemini

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/gin-gonic/gin"
)

func setupGeminiTestConfigManager(t *testing.T, upstream []config.UpstreamConfig) *config.ConfigManager {
	t.Helper()
	cfg := config.Config{GeminiUpstream: upstream}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		t.Fatalf("serialize config: %v", err)
	}
	tmpFile := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(tmpFile, data, 0644); err != nil {
		t.Fatalf("write config file: %v", err)
	}
	cm, err := config.NewConfigManager(tmpFile)
	if err != nil {
		t.Fatalf("NewConfigManager() err = %v", err)
	}
	t.Cleanup(func() { _ = cm.Close() })
	return cm
}

func newGeminiTestRouter(t *testing.T, upstream config.UpstreamConfig) *gin.Engine {
	t.Helper()
	r, _ := newGeminiTestRouterWithMetrics(t, upstream)
	return r
}

func newGeminiTestRouterWithMetrics(t *testing.T, upstream config.UpstreamConfig) (*gin.Engine, *metrics.MetricsManager) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfgManager := setupGeminiTestConfigManager(t, []config.UpstreamConfig{upstream})
	geminiMetrics := metrics.NewMetricsManager()
	channelScheduler := scheduler.NewChannelScheduler(
		cfgManager,
		metrics.NewMetricsManager(),
		metrics.NewMetricsManager(),
		geminiMetrics,
		metrics.NewMetricsManager(),
		session.NewTraceAffinityManager(),
		nil,
	)
	envCfg := &config.EnvConfig{
		ProxyAccessKey:     "secret-key",
		MaxRequestBodySize: 1024 * 1024,
	}

	r := gin.New()
	r.POST("/v1beta/models/*modelAction", Handler(envCfg, cfgManager, channelScheduler))
	return r, geminiMetrics
}

func performGeminiHandlerRequest(t *testing.T, router *gin.Engine, model string, body string) *httptest.ResponseRecorder {
	t.Helper()
	url := "/v1beta/models/" + model + ":generateContent"
	req := httptest.NewRequest(http.MethodPost, url, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "secret-key")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func performGeminiStreamHandlerRequest(t *testing.T, router *gin.Engine, model string, body string) *httptest.ResponseRecorder {
	t.Helper()
	url := "/v1beta/models/" + model + ":streamGenerateContent"
	req := httptest.NewRequest(http.MethodPost, url, bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "secret-key")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestGeminiHandler_StreamRawPassthroughPreservesNativeSSEBytesAndMetrics(t *testing.T) {
	rawStream := "" +
		":keep-alive\r\n\r\n" +
		"id:gem-1\r\n" +
		"event:model\r\n" +
		"data:{\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"hi\"}]}}]}\r\n\r\n" +
		"retry:1500\r\n" +
		"data:{\"candidates\":[{\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":9,\"candidatesTokenCount\":4,\"totalTokenCount\":13}}\r\n\r\n" +
		"data:{\"usageMetadata\":{\"cachedContentTokenCount\":2}}\r\n\r\n"

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(rawStream))
	}))
	defer upstream.Close()

	router, geminiMetrics := newGeminiTestRouterWithMetrics(t, config.UpstreamConfig{
		Name:        "gemini-native-raw-stream",
		BaseURL:     upstream.URL,
		APIKeys:     []string{"gemini-stream-key"},
		ServiceType: "gemini",
		Status:      "active",
	})

	w := performGeminiStreamHandlerRequest(t, router, "gemini-2.0-flash", `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if got := w.Body.String(); got != rawStream {
		t.Fatalf("raw stream body mismatch\n got: %q\nwant: %q", got, rawStream)
	}

	points := geminiMetrics.GetKeyHistoricalStats(upstream.URL, "gemini-stream-key", "gemini", time.Hour, time.Minute)
	var successCount, inputTokens, outputTokens, cacheReadTokens int64
	for _, point := range points {
		successCount += point.SuccessCount
		inputTokens += point.InputTokens
		outputTokens += point.OutputTokens
		cacheReadTokens += point.CacheReadInputTokens
	}
	if successCount != 1 {
		t.Fatalf("successCount = %d, want 1", successCount)
	}
	if inputTokens != 7 || outputTokens != 4 || cacheReadTokens != 2 {
		t.Fatalf("metrics tokens = input:%d output:%d cache_read:%d, want input:7 output:4 cache_read:2", inputTokens, outputTokens, cacheReadTokens)
	}
}

func TestGeminiHandler_NonStreamRawPassthroughRecordsCachedContentTokens(t *testing.T) {
	rawBody := `{"candidates":[{"content":{"role":"model","parts":[{"text":"hi"}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":9,"cachedContentTokenCount":12,"candidatesTokenCount":4,"totalTokenCount":13},"vendorExt":{"kept":true}}`

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(rawBody))
	}))
	defer upstream.Close()

	router, geminiMetrics := newGeminiTestRouterWithMetrics(t, config.UpstreamConfig{
		Name:        "gemini-native-raw-nonstream",
		BaseURL:     upstream.URL,
		APIKeys:     []string{"gemini-nonstream-key"},
		ServiceType: "gemini",
		Status:      "active",
	})

	w := performGeminiHandlerRequest(t, router, "gemini-2.0-flash", `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if got := w.Body.String(); got != rawBody {
		t.Fatalf("raw non-stream body mismatch\n got: %q\nwant: %q", got, rawBody)
	}

	points := geminiMetrics.GetKeyHistoricalStats(upstream.URL, "gemini-nonstream-key", "gemini", time.Hour, time.Minute)
	var successCount, inputTokens, outputTokens, cacheReadTokens int64
	for _, point := range points {
		successCount += point.SuccessCount
		inputTokens += point.InputTokens
		outputTokens += point.OutputTokens
		cacheReadTokens += point.CacheReadInputTokens
	}
	if successCount != 1 {
		t.Fatalf("successCount = %d, want 1", successCount)
	}
	if inputTokens != 0 || outputTokens != 4 || cacheReadTokens != 12 {
		t.Fatalf("metrics tokens = input:%d output:%d cache_read:%d, want input:0 output:4 cache_read:12", inputTokens, outputTokens, cacheReadTokens)
	}
}

func TestGeminiHandler_StreamRawPassthroughCancelsFirstAttemptBeforeFailover(t *testing.T) {
	firstClosed := make(chan struct{})
	var secondSawFirstClosed atomic.Bool
	successStream := "" +
		"data:{\"candidates\":[{\"content\":{\"role\":\"model\",\"parts\":[{\"text\":\"ok\"}]}}]}\n\n" +
		"data:{\"candidates\":[{\"finishReason\":\"STOP\"}],\"usageMetadata\":{\"promptTokenCount\":4,\"candidatesTokenCount\":2,\"totalTokenCount\":6}}\n\n"

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		key := r.Header.Get("x-goog-api-key")
		switch {
		case strings.Contains(key, "gemini-first"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("event: error\ndata:{\"error\":{\"type\":\"rate_limit_error\",\"message\":\"slow down\"}}\n\n"))
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			<-r.Context().Done()
			close(firstClosed)
		case strings.Contains(key, "gemini-second"):
			select {
			case <-firstClosed:
				secondSawFirstClosed.Store(true)
			case <-time.After(2 * time.Second):
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(successStream))
		default:
			http.Error(w, "unexpected key", http.StatusInternalServerError)
		}
	}))
	defer upstream.Close()

	router := newGeminiTestRouter(t, config.UpstreamConfig{
		Name:        "gemini-native-raw-failover",
		BaseURL:     upstream.URL,
		APIKeys:     []string{"gemini-first", "gemini-second"},
		ServiceType: "gemini",
		Status:      "active",
	})

	w := performGeminiStreamHandlerRequest(t, router, "gemini-2.0-flash", `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if got := w.Body.String(); got != successStream {
		t.Fatalf("body = %q, want successful second attempt stream %q", got, successStream)
	}
	if !secondSawFirstClosed.Load() {
		t.Fatalf("second attempt started before first attempt body/fan-out was released")
	}
}

func TestGeminiHandler_CrossFormatStreamDoesNotUseRawPassthrough(t *testing.T) {
	upstreamRaw := "" +
		"event:raw_should_not_pass\n" +
		"data: {\"id\":\"chatcmpl_1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\n" +
		"data: {\"choices\":[{\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":3,\"completion_tokens\":2,\"total_tokens\":5}}\n\n"

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(upstreamRaw))
	}))
	defer upstream.Close()

	router := newGeminiTestRouter(t, config.UpstreamConfig{
		Name:        "gemini-openai-stream-conversion",
		BaseURL:     upstream.URL,
		APIKeys:     []string{"sk-openai-stream"},
		ServiceType: "openai",
		Status:      "active",
	})

	w := performGeminiStreamHandlerRequest(t, router, "gemini-2.0-flash", `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if got := w.Body.String(); got == upstreamRaw {
		t.Fatalf("cross-format stream was raw-passthroughed unexpectedly")
	}
	if bytes.Contains(w.Body.Bytes(), []byte("event:raw_should_not_pass")) {
		t.Fatalf("cross-format response leaked raw upstream event: %s", w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("candidates")) {
		t.Fatalf("cross-format response did not include converted Gemini chunks: %s", w.Body.String())
	}
}

func TestGeminiHandler_NonStreamMatrix_AllFourUpstreams(t *testing.T) {
	tests := []struct {
		name                  string
		serviceType           string
		responseBody          string
		expectedText          string
		expectedFinishReason  string
		expectedPromptTok     int
		expectedCandidatesTok int
	}{
		{
			name:                  "gemini_handler_to_gemini",
			serviceType:           "gemini",
			responseBody:          `{"candidates":[{"content":{"role":"model","parts":[{"text":"hi"}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":17,"candidatesTokenCount":3,"totalTokenCount":20}}`,
			expectedText:          "hi",
			expectedFinishReason:  "STOP",
			expectedPromptTok:     17,
			expectedCandidatesTok: 3,
		},
		{
			name:                  "gemini_handler_to_claude",
			serviceType:           "claude",
			responseBody:          `{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":{"input_tokens":11,"output_tokens":7}}`,
			expectedText:          "hi",
			expectedFinishReason:  "STOP",
			expectedPromptTok:     11,
			expectedCandidatesTok: 7,
		},
		{
			name:                  "gemini_handler_to_openai",
			serviceType:           "openai",
			responseBody:          `{"id":"chatcmpl_1","model":"gpt-4o","choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":13,"completion_tokens":5,"total_tokens":18}}`,
			expectedText:          "hi",
			expectedFinishReason:  "STOP",
			expectedPromptTok:     13,
			expectedCandidatesTok: 5,
		},
		{
			name:                  "gemini_handler_to_responses",
			serviceType:           "responses",
			responseBody:          `{"id":"resp_1","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}],"usage":{"input_tokens":19,"output_tokens":9,"total_tokens":28}}`,
			expectedText:          "hi",
			expectedFinishReason:  "STOP",
			expectedPromptTok:     19,
			expectedCandidatesTok: 9,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(tt.responseBody))
			}))
			defer upstream.Close()

			router := newGeminiTestRouter(t, config.UpstreamConfig{
				Name:        tt.name,
				BaseURL:     upstream.URL,
				APIKeys:     []string{"sk-test"},
				ServiceType: tt.serviceType,
				Status:      "active",
			})

			w := performGeminiHandlerRequest(t, router, "gemini-2.0-flash", `{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}`)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
			}

			var resp struct {
				Candidates []struct {
					Content *struct {
						Parts []struct {
							Text string `json:"text"`
						} `json:"parts"`
					} `json:"content"`
					FinishReason string `json:"finishReason"`
				} `json:"candidates"`
				UsageMetadata *struct {
					PromptTokenCount     int `json:"promptTokenCount"`
					CandidatesTokenCount int `json:"candidatesTokenCount"`
				} `json:"usageMetadata"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("unmarshal response: %v, body=%s", err, w.Body.String())
			}

			if len(resp.Candidates) == 0 || resp.Candidates[0].Content == nil {
				t.Fatalf("candidates empty: %s", w.Body.String())
			}
			if len(resp.Candidates[0].Content.Parts) == 0 {
				t.Fatalf("parts empty")
			}
			if resp.Candidates[0].Content.Parts[0].Text != tt.expectedText {
				t.Fatalf("text = %q, want %q", resp.Candidates[0].Content.Parts[0].Text, tt.expectedText)
			}
			if resp.Candidates[0].FinishReason != tt.expectedFinishReason {
				t.Fatalf("finishReason = %q, want %q", resp.Candidates[0].FinishReason, tt.expectedFinishReason)
			}
			if resp.UsageMetadata == nil {
				t.Fatalf("usageMetadata is nil")
			}
			if resp.UsageMetadata.PromptTokenCount != tt.expectedPromptTok {
				t.Fatalf("promptTokenCount = %d, want %d", resp.UsageMetadata.PromptTokenCount, tt.expectedPromptTok)
			}
			if resp.UsageMetadata.CandidatesTokenCount != tt.expectedCandidatesTok {
				t.Fatalf("candidatesTokenCount = %d, want %d", resp.UsageMetadata.CandidatesTokenCount, tt.expectedCandidatesTok)
			}
		})
	}
}

func TestGeminiHandler_NonStreamMatrix_FunctionCall(t *testing.T) {
	tests := []struct {
		name         string
		serviceType  string
		responseBody string
		expectName   string
	}{
		{
			name:         "gemini_handler_function_from_claude",
			serviceType:  "claude",
			responseBody: `{"id":"msg_tool","type":"message","role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"Read","input":{"file_path":"/tmp/a"}}],"stop_reason":"tool_use","usage":{"input_tokens":1,"output_tokens":1}}`,
			expectName:   "Read",
		},
		{
			name:         "gemini_handler_function_from_openai",
			serviceType:  "openai",
			responseBody: `{"id":"chat_tool","model":"gpt-4o","choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call_o","type":"function","function":{"name":"Read","arguments":"{\"file_path\":\"/tmp/b\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
			expectName:   "Read",
		},
		{
			name:         "gemini_handler_function_from_responses",
			serviceType:  "responses",
			responseBody: `{"id":"resp_tool","status":"completed","output":[{"type":"function_call","call_id":"call_r","name":"search_docs","arguments":"{\"query\":\"go\"}"}],"usage":{"input_tokens":1,"output_tokens":1}}`,
			expectName:   "search_docs",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte(tt.responseBody))
			}))
			defer upstream.Close()

			router := newGeminiTestRouter(t, config.UpstreamConfig{
				Name:        tt.name,
				BaseURL:     upstream.URL,
				APIKeys:     []string{"sk-test"},
				ServiceType: tt.serviceType,
				Status:      "active",
			})

			w := performGeminiHandlerRequest(t, router, "gemini-2.0-flash", `{"contents":[{"role":"user","parts":[{"text":"read file"}]}]}`)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
			}

			if !bytes.Contains(w.Body.Bytes(), []byte(`"functionCall"`)) {
				t.Fatalf("expected functionCall in response body, got %s", w.Body.String())
			}
			if !bytes.Contains(w.Body.Bytes(), []byte(tt.expectName)) {
				t.Fatalf("expected function name %q in response body, got %s", tt.expectName, w.Body.String())
			}
		})
	}
}
