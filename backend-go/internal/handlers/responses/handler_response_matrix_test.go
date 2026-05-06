package responses

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/gin-gonic/gin"
)

func setupResponsesTestConfigManager(t *testing.T, upstream []config.UpstreamConfig) *config.ConfigManager {
	t.Helper()
	cfg := config.Config{ResponsesUpstream: upstream}
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
	t.Cleanup(func() {
		if err := cm.Close(); err != nil {
			t.Logf("close config manager: %v", err)
		}
	})
	return cm
}

func newResponsesTestRouter(t *testing.T, upstream config.UpstreamConfig, sessionManager *session.SessionManager) *gin.Engine {
	t.Helper()
	r, _ := newResponsesTestRouterWithMetrics(t, upstream, sessionManager)
	return r
}

func newResponsesTestRouterWithMetrics(t *testing.T, upstream config.UpstreamConfig, sessionManager *session.SessionManager) (*gin.Engine, *metrics.MetricsManager) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfgManager := setupResponsesTestConfigManager(t, []config.UpstreamConfig{upstream})
	responsesMetrics := metrics.NewMetricsManager()
	channelScheduler := scheduler.NewChannelScheduler(
		cfgManager,
		metrics.NewMetricsManager(),
		responsesMetrics,
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
	r.POST("/v1/responses", Handler(envCfg, cfgManager, sessionManager, channelScheduler))
	return r, responsesMetrics
}

func performResponsesHandlerRequest(t *testing.T, router *gin.Engine, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "secret-key")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestResponsesHandler_InvalidJSONReturns400(t *testing.T) {
	sessionManager := session.NewSessionManager(time.Hour, 100, 100000)
	router := newResponsesTestRouter(t, config.UpstreamConfig{
		Name:        "responses-upstream",
		ServiceType: "responses",
		BaseURL:     "https://api.example.com",
		APIKeys:     []string{"sk-test"},
	}, sessionManager)

	w := performResponsesHandlerRequest(t, router, `{"model":"gpt-5","input":[`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if got := w.Body.String(); !bytes.Contains([]byte(got), []byte("Invalid request body")) {
		t.Fatalf("body = %s, want invalid request body error", got)
	}
}

func TestResponsesHandler_NonStreamRawPassthroughPreservesUnknownFieldsAndRecordsMetrics(t *testing.T) {
	sessionManager := session.NewSessionManager(time.Hour, 100, 100000)
	rawBody := `{"id":"resp_raw","model":"gpt-5","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"raw hi"}]}],"usage":{"input_tokens":23,"output_tokens":11,"total_tokens":34},"vendor_trace":{"id":"trace_1","nested":{"kept":true}},"unknown_top":"keep me"}`

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(rawBody))
	}))
	defer upstream.Close()

	router, responsesMetrics := newResponsesTestRouterWithMetrics(t, config.UpstreamConfig{
		Name:        "responses-raw-passthrough",
		BaseURL:     upstream.URL,
		APIKeys:     []string{"sk-test"},
		ServiceType: "responses",
		Status:      "active",
	}, sessionManager)

	w := performResponsesHandlerRequest(t, router, `{"model":"gpt-5","input":"hello"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if got := w.Body.String(); got != rawBody {
		t.Fatalf("raw passthrough body changed:\ngot  %s\nwant %s", got, rawBody)
	}

	var resp map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal raw response: %v", err)
	}
	if resp["unknown_top"] != "keep me" {
		t.Fatalf("unknown_top = %#v, want keep me", resp["unknown_top"])
	}
	vendorTrace, ok := resp["vendor_trace"].(map[string]interface{})
	if !ok {
		t.Fatalf("vendor_trace missing or wrong type: %#v", resp["vendor_trace"])
	}
	nested, ok := vendorTrace["nested"].(map[string]interface{})
	if !ok || nested["kept"] != true {
		t.Fatalf("vendor_trace.nested.kept not preserved: %#v", vendorTrace["nested"])
	}

	points := responsesMetrics.GetKeyHistoricalStats(upstream.URL, "sk-test", "responses", time.Hour, time.Minute)
	var successCount, inputTokens, outputTokens int64
	for _, point := range points {
		successCount += point.SuccessCount
		inputTokens += point.InputTokens
		outputTokens += point.OutputTokens
	}
	if successCount != 1 {
		t.Fatalf("successCount = %d, want 1", successCount)
	}
	if inputTokens != 23 || outputTokens != 11 {
		t.Fatalf("metrics tokens = input:%d output:%d, want input:23 output:11", inputTokens, outputTokens)
	}
}

func TestResponsesHandler_StreamRawPassthroughPreservesSSEBytes(t *testing.T) {
	sessionManager := session.NewSessionManager(time.Hour, 100, 100000)
	rawBody := strings.Join([]string{
		":keepalive",
		"id:evt-1",
		"event:response.output_text.delta",
		"retry:1500",
		`data:{"type":"response.output_text.delta","delta":"hi"}`,
		"",
		`data:{"type":"response.completed","response":{"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}}`,
		"",
	}, "\n")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(rawBody))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer upstream.Close()

	router, responsesMetrics := newResponsesTestRouterWithMetrics(t, config.UpstreamConfig{
		Name:        "responses-stream-raw-passthrough",
		BaseURL:     upstream.URL,
		APIKeys:     []string{"sk-test"},
		ServiceType: "responses",
		Status:      "active",
	}, sessionManager)

	w := performResponsesHandlerRequest(t, router, `{"model":"gpt-5","input":"hello","stream":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if got := w.Body.String(); got != rawBody {
		t.Fatalf("raw stream passthrough body changed:\ngot  %q\nwant %q", got, rawBody)
	}

	points := responsesMetrics.GetKeyHistoricalStats(upstream.URL, "sk-test", "responses", time.Hour, time.Minute)
	var inputTokens, outputTokens int64
	for _, point := range points {
		inputTokens += point.InputTokens
		outputTokens += point.OutputTokens
	}
	if inputTokens != 2 || outputTokens != 1 {
		t.Fatalf("metrics tokens = input:%d output:%d, want input:2 output:1", inputTokens, outputTokens)
	}
}

func TestResponsesHandler_StreamRawPassthroughRecordsUsageAfterLargePrefix(t *testing.T) {
	sessionManager := session.NewSessionManager(time.Hour, 100, 100000)
	firstEvent := strings.Join([]string{
		`data:{"type":"response.output_text.delta","delta":"hi"}`,
		"",
	}, "\n")
	padding := strings.Repeat(": padding\n\n", 120000)
	usageEvent := `data:{"type":"response.completed","response":{"usage":{"input_tokens":13,"output_tokens":5,"total_tokens":18}}}` + "\n\n"
	rawBody := firstEvent + padding + usageEvent

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(rawBody))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer upstream.Close()

	router, responsesMetrics := newResponsesTestRouterWithMetrics(t, config.UpstreamConfig{
		Name:        "responses-stream-raw-large-prefix",
		BaseURL:     upstream.URL,
		APIKeys:     []string{"sk-large-prefix"},
		ServiceType: "responses",
		Status:      "active",
	}, sessionManager)

	w := performResponsesHandlerRequest(t, router, `{"model":"gpt-5","input":"hello","stream":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if got := w.Body.String(); got != rawBody {
		t.Fatalf("raw stream passthrough body changed after large prefix: got len=%d want len=%d", len(got), len(rawBody))
	}

	points := responsesMetrics.GetKeyHistoricalStats(upstream.URL, "sk-large-prefix", "responses", time.Hour, time.Minute)
	var inputTokens, outputTokens int64
	for _, point := range points {
		inputTokens += point.InputTokens
		outputTokens += point.OutputTokens
	}
	if inputTokens != 13 || outputTokens != 5 {
		t.Fatalf("metrics tokens = input:%d output:%d, want input:13 output:5", inputTokens, outputTokens)
	}
}

func TestResponsesHandler_StreamRawPassthroughEmptyStreamFailsOverBeforeHeaders(t *testing.T) {
	sessionManager := session.NewSessionManager(time.Hour, 100, 100000)
	validBody := strings.Join([]string{
		`data:{"type":"response.output_text.delta","delta":"fallback"}`,
		"",
		`data:{"type":"response.completed","response":{"usage":{"input_tokens":7,"output_tokens":3,"total_tokens":10}}}`,
		"",
	}, "\n")
	attempts := 0

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if strings.Contains(r.Header.Get("Authorization"), "sk-empty") {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("X-First-Attempt", "must-not-reach-client")
			w.WriteHeader(http.StatusOK)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(validBody))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer upstream.Close()

	router := newResponsesTestRouter(t, config.UpstreamConfig{
		Name:        "responses-stream-empty-failover",
		BaseURL:     upstream.URL,
		APIKeys:     []string{"sk-empty", "sk-ok"},
		ServiceType: "responses",
		Status:      "active",
	}, sessionManager)

	w := performResponsesHandlerRequest(t, router, `{"model":"gpt-5","input":"hello","stream":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if got := w.Header().Get("X-First-Attempt"); got != "" {
		t.Fatalf("first attempt header reached client: %q", got)
	}
	if got := w.Body.String(); got != validBody {
		t.Fatalf("raw fallback stream changed:\ngot  %q\nwant %q", got, validBody)
	}
}

func TestResponsesHandler_StreamRawPassthroughInvalidBodyFailsOverBeforeHeaders(t *testing.T) {
	sessionManager := session.NewSessionManager(time.Hour, 100, 100000)
	validBody := strings.Join([]string{
		`data:{"type":"response.output_text.delta","delta":"fallback"}`,
		"",
		`data:{"type":"response.completed","response":{"usage":{"input_tokens":9,"output_tokens":4,"total_tokens":13}}}`,
		"",
	}, "\n")
	attempts := 0

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if strings.Contains(r.Header.Get("Authorization"), "sk-invalid") {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("X-First-Attempt", "must-not-reach-client")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("<html>upstream error</html>"))
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(validBody))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer upstream.Close()

	router := newResponsesTestRouter(t, config.UpstreamConfig{
		Name:        "responses-stream-invalid-failover",
		BaseURL:     upstream.URL,
		APIKeys:     []string{"sk-invalid", "sk-ok"},
		ServiceType: "responses",
		Status:      "active",
	}, sessionManager)

	w := performResponsesHandlerRequest(t, router, `{"model":"gpt-5","input":"hello","stream":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if got := w.Header().Get("X-First-Attempt"); got != "" {
		t.Fatalf("first attempt header reached client: %q", got)
	}
	if got := w.Body.String(); got != validBody {
		t.Fatalf("raw fallback stream changed:\ngot  %q\nwant %q", got, validBody)
	}
}

func TestResponsesHandler_StreamRawPassthroughBlacklistPreflightFailsOverBeforeHeaders(t *testing.T) {
	sessionManager := session.NewSessionManager(time.Hour, 100, 100000)
	errorBody := strings.Join([]string{
		"event:error",
		`data:{"type":"error","error":{"type":"authentication_error","message":"bad key"}}`,
		"",
	}, "\n")
	validBody := strings.Join([]string{
		`data:{"type":"response.output_text.delta","delta":"ok"}`,
		"",
		`data:{"type":"response.completed","response":{"usage":{"input_tokens":5,"output_tokens":2,"total_tokens":7}}}`,
		"",
	}, "\n")
	attempts := 0

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		w.Header().Set("Content-Type", "text/event-stream")
		if strings.Contains(r.Header.Get("Authorization"), "sk-bad") {
			w.Header().Set("X-First-Attempt", "must-not-reach-client")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(errorBody))
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(validBody))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer upstream.Close()

	router := newResponsesTestRouter(t, config.UpstreamConfig{
		Name:        "responses-stream-blacklist-failover",
		BaseURL:     upstream.URL,
		APIKeys:     []string{"sk-bad", "sk-ok"},
		ServiceType: "responses",
		Status:      "active",
	}, sessionManager)

	w := performResponsesHandlerRequest(t, router, `{"model":"gpt-5","input":"hello","stream":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if attempts != 2 {
		t.Fatalf("attempts = %d, want 2", attempts)
	}
	if got := w.Header().Get("X-First-Attempt"); got != "" {
		t.Fatalf("first attempt header reached client: %q", got)
	}
	if got := w.Body.String(); got != validBody {
		t.Fatalf("raw fallback stream changed:\ngot  %q\nwant %q", got, validBody)
	}
}

func TestResponsesHandler_StreamSameFormatAlwaysUsesRawPassthrough(t *testing.T) {
	sessionManager := session.NewSessionManager(time.Hour, 100, 100000)
	rawBody := strings.Join([]string{
		":keepalive",
		"id:evt-1",
		"event:response.output_text.delta",
		"retry:1500",
		`data:{"type":"response.output_text.delta","delta":"hi"}`,
		"",
		`data:{"type":"response.completed","response":{"usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}}`,
		"",
	}, "\n")

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(rawBody))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
	}))
	defer upstream.Close()

	router := newResponsesTestRouter(t, config.UpstreamConfig{
		Name:        "responses-stream-processing",
		BaseURL:     upstream.URL,
		APIKeys:     []string{"sk-test"},
		ServiceType: "responses",
		Status:      "active",
	}, sessionManager)

	w := performResponsesHandlerRequest(t, router, `{"model":"gpt-5","input":"hello","stream":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	got := w.Body.String()
	if got != rawBody {
		t.Fatalf("same-format responses stream should stay raw:\ngot  %q\nwant %q", got, rawBody)
	}
}

func TestResponsesHandler_NonStreamMatrix_AllFourUpstreams(t *testing.T) {
	sessionManager := session.NewSessionManager(time.Hour, 100, 100000)

	tests := []struct {
		name              string
		serviceType       string
		responseBody      string
		expectedText      string
		expectedStatus    string
		expectedInputTok  int
		expectedOutputTok int
	}{
		{
			name:              "responses_handler_to_responses",
			serviceType:       "responses",
			responseBody:      `{"id":"resp_native","model":"gpt-5","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}],"usage":{"input_tokens":11,"output_tokens":7,"total_tokens":18}}`,
			expectedText:      "hi",
			expectedStatus:    "completed",
			expectedInputTok:  11,
			expectedOutputTok: 7,
		},
		{
			name:              "responses_handler_to_claude",
			serviceType:       "claude",
			responseBody:      `{"id":"msg_1","model":"claude-3-5-sonnet","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":{"input_tokens":13,"output_tokens":5}}`,
			expectedText:      "hi",
			expectedStatus:    "completed",
			expectedInputTok:  13,
			expectedOutputTok: 5,
		},
		{
			name:              "responses_handler_to_openai",
			serviceType:       "openai",
			responseBody:      `{"id":"chatcmpl_1","model":"gpt-4o","choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":17,"completion_tokens":3,"total_tokens":20}}`,
			expectedText:      "hi",
			expectedStatus:    "completed",
			expectedInputTok:  17,
			expectedOutputTok: 3,
		},
		{
			name:              "responses_handler_to_gemini",
			serviceType:       "gemini",
			responseBody:      `{"candidates":[{"content":{"role":"model","parts":[{"text":"hi"}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":19,"candidatesTokenCount":9,"totalTokenCount":28}}`,
			expectedText:      "hi",
			expectedStatus:    "completed",
			expectedInputTok:  19,
			expectedOutputTok: 9,
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

			router := newResponsesTestRouter(t, config.UpstreamConfig{
				Name:        tt.name,
				BaseURL:     upstream.URL,
				APIKeys:     []string{"sk-test"},
				ServiceType: tt.serviceType,
				Status:      "active",
			}, sessionManager)

			w := performResponsesHandlerRequest(t, router, `{"model":"gpt-5","input":"hello"}`)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
			}

			var resp struct {
				ID     string `json:"id"`
				Status string `json:"status"`
				Output []struct {
					Type    string      `json:"type"`
					Role    string      `json:"role"`
					Content interface{} `json:"content"`
				} `json:"output"`
				Usage struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("unmarshal response: %v, body=%s", err, w.Body.String())
			}

			if resp.Status != tt.expectedStatus {
				t.Fatalf("status = %q, want %q", resp.Status, tt.expectedStatus)
			}
			if len(resp.Output) == 0 {
				t.Fatalf("output empty: %s", w.Body.String())
			}
			if got := fmt.Sprint(resp.Output[0].Content); got == "<nil>" {
				t.Fatalf("first output content is nil: %#v", resp.Output)
			}
			if resp.Usage.InputTokens != tt.expectedInputTok {
				t.Fatalf("input_tokens = %d, want %d", resp.Usage.InputTokens, tt.expectedInputTok)
			}
			if resp.Usage.OutputTokens != tt.expectedOutputTok {
				t.Fatalf("output_tokens = %d, want %d", resp.Usage.OutputTokens, tt.expectedOutputTok)
			}

			bodyText := w.Body.String()
			if !bytes.Contains([]byte(bodyText), []byte(tt.expectedText)) {
				t.Fatalf("response body %q does not contain expected text %q", bodyText, tt.expectedText)
			}
		})
	}
}

func TestResponsesHandler_NonStreamMatrix_FunctionCall(t *testing.T) {
	sessionManager := session.NewSessionManager(time.Hour, 100, 100000)

	tests := []struct {
		name         string
		serviceType  string
		responseBody string
		expectCall   string
		expectName   string
	}{
		{
			name:         "responses_handler_function_call_from_claude",
			serviceType:  "claude",
			responseBody: `{"id":"msg_tool","model":"claude-3-5-sonnet","content":[{"type":"tool_use","id":"call_claude","name":"Read","input":{"file_path":"/tmp/a"}}],"stop_reason":"tool_use","usage":{"input_tokens":1,"output_tokens":1}}`,
			expectCall:   "call_claude",
			expectName:   "Read",
		},
		{
			name:         "responses_handler_function_call_from_openai",
			serviceType:  "openai",
			responseBody: `{"id":"chat_tool","model":"gpt-4o","choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call_openai","type":"function","function":{"name":"Read","arguments":"{\"file_path\":\"/tmp/b\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
			expectCall:   "call_openai",
			expectName:   "Read",
		},
		{
			name:         "responses_handler_function_call_from_gemini",
			serviceType:  "gemini",
			responseBody: `{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"search_docs","args":{"query":"responses"}}}]},"finishReason":"STOP","index":0}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`,
			expectCall:   "search_docs",
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

			router := newResponsesTestRouter(t, config.UpstreamConfig{
				Name:        tt.name,
				BaseURL:     upstream.URL,
				APIKeys:     []string{"sk-test"},
				ServiceType: tt.serviceType,
				Status:      "active",
			}, sessionManager)

			w := performResponsesHandlerRequest(t, router, `{"model":"gpt-5","input":"hello"}`)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
			}
			if !bytes.Contains(w.Body.Bytes(), []byte(`"type":"function_call"`)) {
				t.Fatalf("expected function_call in response body, got %s", w.Body.String())
			}
			if !bytes.Contains(w.Body.Bytes(), []byte(tt.expectCall)) {
				t.Fatalf("expected call id %q in response body, got %s", tt.expectCall, w.Body.String())
			}
			if !bytes.Contains(w.Body.Bytes(), []byte(tt.expectName)) {
				t.Fatalf("expected tool/function name %q in response body, got %s", tt.expectName, w.Body.String())
			}
		})
	}
}
