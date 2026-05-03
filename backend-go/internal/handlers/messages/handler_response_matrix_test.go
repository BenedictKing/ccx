package messages

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/session"
	"github.com/gin-gonic/gin"
)

func setupMessagesTestConfigManager(t *testing.T, upstream []config.UpstreamConfig) *config.ConfigManager {
	t.Helper()
	cfg := config.Config{Upstream: upstream}
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
	t.Cleanup(func() { cm.Close() })
	return cm
}

func newMessagesTestRouter(t *testing.T, upstream config.UpstreamConfig) *gin.Engine {
	t.Helper()
	r, _ := newMessagesTestRouterWithMetrics(t, upstream)
	return r
}

func newMessagesTestRouterWithMetrics(t *testing.T, upstream config.UpstreamConfig) (*gin.Engine, *metrics.MetricsManager) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfgManager := setupMessagesTestConfigManager(t, []config.UpstreamConfig{upstream})
	messagesMetrics := metrics.NewMetricsManager()
	channelScheduler := scheduler.NewChannelScheduler(
		cfgManager,
		messagesMetrics,
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
	r.POST("/v1/messages", Handler(envCfg, cfgManager, channelScheduler))
	return r, messagesMetrics
}

func performMessagesHandlerRequest(t *testing.T, router *gin.Engine, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", "secret-key")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestMessagesHandler_InvalidJSONReturns400(t *testing.T) {
	router := newMessagesTestRouter(t, config.UpstreamConfig{
		Name:        "messages-upstream",
		ServiceType: "claude",
		BaseURL:     "https://api.example.com",
		APIKeys:     []string{"sk-test"},
	})

	w := performMessagesHandlerRequest(t, router, `{"model":"claude-3-7-sonnet","messages":[`)
	if w.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusBadRequest, w.Body.String())
	}
	if got := w.Body.String(); !bytes.Contains([]byte(got), []byte("Invalid request body")) {
		t.Fatalf("body = %s, want invalid request body error", got)
	}
}

func TestMessagesHandler_Sub2APIPassthroughLowQualityRecordsNormalizedMetrics(t *testing.T) {
	enabled := true
	disabled := false
	upstreamServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"this response is long enough to estimate more than one output token"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`))
	}))
	defer upstreamServer.Close()

	router, messagesMetrics := newMessagesTestRouterWithMetrics(t, config.UpstreamConfig{
		Name:                            "sub2api-low-quality",
		BaseURL:                         upstreamServer.URL,
		APIKeys:                         []string{"sk-ant-test"},
		ServiceType:                     "claude",
		Status:                          "active",
		LowQuality:                      true,
		Sub2APIPassthroughEnabled:       &enabled,
		StreamPassthroughEnabled:        &disabled,
		StrictRequestPassthroughEnabled: &enabled,
	})

	body := `{"model":"claude-3-5-sonnet","messages":[{"role":"user","content":[{"type":"text","text":"this request body is intentionally long enough to estimate more than one input token for low quality usage metrics"}]}]}`
	w := performMessagesHandlerRequest(t, router, body)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte(`"input_tokens":1`)) || !bytes.Contains(w.Body.Bytes(), []byte(`"output_tokens":1`)) {
		t.Fatalf("passthrough response should remain unchanged, body=%s", w.Body.String())
	}

	points := messagesMetrics.GetKeyHistoricalStats(upstreamServer.URL, "sk-ant-test", "claude", time.Hour, time.Minute)
	var successCount, inputTokens, outputTokens int64
	for _, point := range points {
		successCount += point.SuccessCount
		inputTokens += point.InputTokens
		outputTokens += point.OutputTokens
	}
	if successCount != 1 {
		t.Fatalf("successCount = %d, want 1", successCount)
	}
	if inputTokens <= 1 || outputTokens <= 1 {
		t.Fatalf("metrics tokens = input:%d output:%d, want lowQuality-normalized values > 1", inputTokens, outputTokens)
	}
}

func TestMessagesHandler_NonStreamMatrix_AllFourUpstreams(t *testing.T) {
	tests := []struct {
		name              string
		serviceType       string
		responseBody      string
		expectedText      string
		expectedStop      string
		expectedInputTok  int
		expectedOutputTok int
	}{
		{
			name:              "messages_handler_to_claude",
			serviceType:       "claude",
			responseBody:      `{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":{"input_tokens":11,"output_tokens":7}}`,
			expectedText:      "hi",
			expectedStop:      "end_turn",
			expectedInputTok:  11,
			expectedOutputTok: 7,
		},
		{
			name:              "messages_handler_to_openai",
			serviceType:       "openai",
			responseBody:      `{"id":"chatcmpl_1","choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":13,"completion_tokens":5,"total_tokens":18}}`,
			expectedText:      "hi",
			expectedStop:      "end_turn",
			expectedInputTok:  13,
			expectedOutputTok: 5,
		},
		{
			name:              "messages_handler_to_gemini",
			serviceType:       "gemini",
			responseBody:      `{"candidates":[{"content":{"role":"model","parts":[{"text":"hi"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":17,"candidatesTokenCount":3,"totalTokenCount":20}}`,
			expectedText:      "hi",
			expectedStop:      "end_turn",
			expectedInputTok:  17,
			expectedOutputTok: 3,
		},
		{
			name:              "messages_handler_to_responses",
			serviceType:       "responses",
			responseBody:      `{"id":"resp_1","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}],"usage":{"input_tokens":19,"output_tokens":9,"total_tokens":28}}`,
			expectedText:      "hi",
			expectedStop:      "end_turn",
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

			router := newMessagesTestRouter(t, config.UpstreamConfig{
				Name:        tt.name,
				BaseURL:     upstream.URL,
				APIKeys:     []string{"sk-test"},
				ServiceType: tt.serviceType,
				Status:      "active",
			})

			w := performMessagesHandlerRequest(t, router, `{"model":"claude-3-5-sonnet","messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}`)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
			}

			var resp struct {
				ID         string `json:"id"`
				Type       string `json:"type"`
				Role       string `json:"role"`
				StopReason string `json:"stop_reason"`
				Content    []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"content"`
				Usage *struct {
					InputTokens  int `json:"input_tokens"`
					OutputTokens int `json:"output_tokens"`
				} `json:"usage"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("unmarshal response: %v, body=%s", err, w.Body.String())
			}

			if resp.StopReason != tt.expectedStop {
				t.Fatalf("stop_reason = %q, want %q", resp.StopReason, tt.expectedStop)
			}
			if len(resp.Content) == 0 || resp.Content[0].Type != "text" {
				t.Fatalf("content[0].type = %q, want text", func() string {
					if len(resp.Content) == 0 {
						return "<empty>"
					}
					return resp.Content[0].Type
				}())
			}
			if resp.Content[0].Text != tt.expectedText {
				t.Fatalf("content[0].text = %q, want %q", resp.Content[0].Text, tt.expectedText)
			}
			if resp.Usage == nil {
				t.Fatalf("usage is nil")
			}
			if resp.Usage.InputTokens != tt.expectedInputTok {
				t.Fatalf("input_tokens = %d, want %d", resp.Usage.InputTokens, tt.expectedInputTok)
			}
			if resp.Usage.OutputTokens != tt.expectedOutputTok {
				t.Fatalf("output_tokens = %d, want %d", resp.Usage.OutputTokens, tt.expectedOutputTok)
			}
		})
	}
}

func TestMessagesHandler_NonStreamMatrix_ToolUse(t *testing.T) {
	tests := []struct {
		name         string
		serviceType  string
		responseBody string
		expectTool   string
	}{
		{
			name:         "messages_handler_tool_from_openai",
			serviceType:  "openai",
			responseBody: `{"id":"chat_tool","choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"Read","arguments":"{\"file_path\":\"/tmp/x\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`,
			expectTool:   "Read",
		},
		{
			name:         "messages_handler_tool_from_gemini",
			serviceType:  "gemini",
			responseBody: `{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"search_docs","args":{"query":"go"}}}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":1,"candidatesTokenCount":1,"totalTokenCount":2}}`,
			expectTool:   "search_docs",
		},
		{
			name:         "messages_handler_tool_from_responses",
			serviceType:  "responses",
			responseBody: `{"id":"resp_tool","status":"completed","output":[{"type":"function_call","call_id":"call_r","name":"Read","arguments":"{\"file_path\":\"/tmp/y\"}"}],"usage":{"input_tokens":1,"output_tokens":1}}`,
			expectTool:   "Read",
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

			router := newMessagesTestRouter(t, config.UpstreamConfig{
				Name:        tt.name,
				BaseURL:     upstream.URL,
				APIKeys:     []string{"sk-test"},
				ServiceType: tt.serviceType,
				Status:      "active",
			})

			w := performMessagesHandlerRequest(t, router, `{"model":"claude-3-5-sonnet","messages":[{"role":"user","content":[{"type":"text","text":"read file"}]}]}`)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
			}

			var resp struct {
				Content []struct {
					Type string `json:"type"`
					Name string `json:"name"`
				} `json:"content"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}

			var found bool
			for _, c := range resp.Content {
				if c.Type == "tool_use" && c.Name == tt.expectTool {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("expected tool_use name=%q in response, got %#v", tt.expectTool, resp.Content)
			}
		})
	}
}
