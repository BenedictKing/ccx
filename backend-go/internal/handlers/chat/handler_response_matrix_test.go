package chat

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

func setupChatTestConfigManager(t *testing.T, upstream []config.UpstreamConfig) *config.ConfigManager {
	t.Helper()
	cfg := config.Config{ChatUpstream: upstream}
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

func newChatTestRouter(t *testing.T, upstream config.UpstreamConfig) *gin.Engine {
	t.Helper()
	r, _ := newChatTestRouterWithMetrics(t, upstream)
	return r
}

func newChatTestRouterWithMetrics(t *testing.T, upstream config.UpstreamConfig) (*gin.Engine, *metrics.MetricsManager) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	cfgManager := setupChatTestConfigManager(t, []config.UpstreamConfig{upstream})
	chatMetrics := metrics.NewMetricsManager()
	channelScheduler := scheduler.NewChannelScheduler(
		cfgManager,
		metrics.NewMetricsManager(),
		metrics.NewMetricsManager(),
		metrics.NewMetricsManager(),
		chatMetrics,
		session.NewTraceAffinityManager(),
		nil,
	)
	envCfg := &config.EnvConfig{
		ProxyAccessKey:     "secret-key",
		MaxRequestBodySize: 1024 * 1024,
	}

	r := gin.New()
	r.POST("/v1/chat/completions", Handler(envCfg, cfgManager, channelScheduler))
	return r, chatMetrics
}

func performChatHandlerRequest(t *testing.T, router *gin.Engine, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", bytes.NewBufferString(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer secret-key")
	w := httptest.NewRecorder()
	router.ServeHTTP(w, req)
	return w
}

func TestChatHandler_StreamRawPassthroughPreservesOpenAIUpstreamSSEBytesAndMetrics(t *testing.T) {
	rawStream := "" +
		":keep-alive\n\n" +
		"id:chat-1\n" +
		"event:chunk\n" +
		"data:{\"id\":\"chatcmpl_1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"hi\"}}]}\n\n" +
		"retry:1500\n" +
		"data:{\"id\":\"chatcmpl_1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":5,\"completion_tokens\":2,\"total_tokens\":7}}\n\n" +
		"data:{\"id\":\"chatcmpl_1\",\"object\":\"chat.completion.chunk\",\"choices\":[],\"usage\":{\"prompt_tokens_details\":{\"cached_tokens\":3}}}\n\n" +
		"data:[DONE]\n\n"

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(rawStream))
	}))
	defer upstream.Close()

	router, chatMetrics := newChatTestRouterWithMetrics(t, config.UpstreamConfig{
		Name:        "chat-openai-raw-stream",
		BaseURL:     upstream.URL,
		APIKeys:     []string{"sk-chat-stream"},
		ServiceType: "openai",
		Status:      "active",
	})

	w := performChatHandlerRequest(t, router, `{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if got := w.Body.String(); got != rawStream {
		t.Fatalf("raw stream body mismatch\n got: %q\nwant: %q", got, rawStream)
	}

	points := chatMetrics.GetKeyHistoricalStats(upstream.URL, "sk-chat-stream", "openai", time.Hour, time.Minute)
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
	if inputTokens != 2 || outputTokens != 2 || cacheReadTokens != 3 {
		t.Fatalf("metrics tokens = input:%d output:%d cache_read:%d, want input:2 output:2 cache_read:3", inputTokens, outputTokens, cacheReadTokens)
	}
}

func TestChatHandler_NonStreamRawPassthroughRecordsCachedTokens(t *testing.T) {
	rawBody := `{"id":"chatcmpl_1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12,"prompt_tokens_details":{"cached_tokens":7}},"vendor_ext":{"kept":true}}`

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(rawBody))
	}))
	defer upstream.Close()

	router, chatMetrics := newChatTestRouterWithMetrics(t, config.UpstreamConfig{
		Name:        "chat-openai-raw-nonstream",
		BaseURL:     upstream.URL,
		APIKeys:     []string{"sk-chat-nonstream"},
		ServiceType: "openai",
		Status:      "active",
	})

	w := performChatHandlerRequest(t, router, `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if got := w.Body.String(); got != rawBody {
		t.Fatalf("raw non-stream body mismatch\n got: %q\nwant: %q", got, rawBody)
	}

	points := chatMetrics.GetKeyHistoricalStats(upstream.URL, "sk-chat-nonstream", "openai", time.Hour, time.Minute)
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
	if inputTokens != 3 || outputTokens != 2 || cacheReadTokens != 7 {
		t.Fatalf("metrics tokens = input:%d output:%d cache_read:%d, want input:3 output:2 cache_read:7", inputTokens, outputTokens, cacheReadTokens)
	}
}

func TestChatHandler_StreamRawPassthroughCancelsFirstAttemptBeforeFailover(t *testing.T) {
	firstClosed := make(chan struct{})
	var secondSawFirstClosed atomic.Bool
	successStream := "" +
		"data:{\"id\":\"chatcmpl_2\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"ok\"}}]}\n\n" +
		"data:{\"id\":\"chatcmpl_2\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}],\"usage\":{\"prompt_tokens\":4,\"completion_tokens\":2,\"total_tokens\":6}}\n\n" +
		"data:[DONE]\n\n"

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		auth := r.Header.Get("Authorization")
		switch {
		case strings.Contains(auth, "sk-first"):
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("event: error\ndata:{\"error\":{\"type\":\"rate_limit_error\",\"message\":\"slow down\"}}\n\n"))
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			<-r.Context().Done()
			close(firstClosed)
		case strings.Contains(auth, "sk-second"):
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

	router := newChatTestRouter(t, config.UpstreamConfig{
		Name:        "chat-openai-raw-failover",
		BaseURL:     upstream.URL,
		APIKeys:     []string{"sk-first", "sk-second"},
		ServiceType: "openai",
		Status:      "active",
	})

	w := performChatHandlerRequest(t, router, `{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
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

func TestChatHandler_CrossFormatStreamDoesNotUseRawPassthrough(t *testing.T) {
	upstreamRaw := "" +
		"event:raw_should_not_pass\n" +
		"data: {\"type\":\"content_block_delta\",\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n" +
		"event:message_delta\n" +
		"data: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":3,\"output_tokens\":2}}\n\n"

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(upstreamRaw))
	}))
	defer upstream.Close()

	router := newChatTestRouter(t, config.UpstreamConfig{
		Name:        "chat-claude-stream-conversion",
		BaseURL:     upstream.URL,
		APIKeys:     []string{"sk-claude-stream"},
		ServiceType: "claude",
		Status:      "active",
	})

	w := performChatHandlerRequest(t, router, `{"model":"gpt-4o","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
	}
	if got := w.Body.String(); got == upstreamRaw {
		t.Fatalf("cross-format stream was raw-passthroughed unexpectedly")
	}
	if bytes.Contains(w.Body.Bytes(), []byte("event:raw_should_not_pass")) {
		t.Fatalf("cross-format response leaked raw upstream event: %s", w.Body.String())
	}
	if !bytes.Contains(w.Body.Bytes(), []byte("chat.completion.chunk")) {
		t.Fatalf("cross-format response did not include converted Chat chunks: %s", w.Body.String())
	}
}

// TestChatHandler_NonStreamMatrix_OpenAIFormat 验证 Chat 入口在 openai/claude 上游下
// 返回正确的 OpenAI Chat Completions 格式响应
func TestChatHandler_NonStreamMatrix_OpenAIFormat(t *testing.T) {
	tests := []struct {
		name                string
		serviceType         string
		responseBody        string
		expectedText        string
		expectedFinish      string
		expectedPromptTok   int
		expectedCompleteTok int
	}{
		{
			name:                "chat_handler_to_openai",
			serviceType:         "openai",
			responseBody:        `{"id":"chatcmpl_1","object":"chat.completion","model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}],"usage":{"prompt_tokens":13,"completion_tokens":5,"total_tokens":18}}`,
			expectedText:        "hi",
			expectedFinish:      "stop",
			expectedPromptTok:   13,
			expectedCompleteTok: 5,
		},
		{
			name:                "chat_handler_to_claude",
			serviceType:         "claude",
			responseBody:        `{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"hi"}],"stop_reason":"end_turn","usage":{"input_tokens":11,"output_tokens":7}}`,
			expectedText:        "hi",
			expectedFinish:      "stop",
			expectedPromptTok:   11,
			expectedCompleteTok: 7,
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

			router := newChatTestRouter(t, config.UpstreamConfig{
				Name:        tt.name,
				BaseURL:     upstream.URL,
				APIKeys:     []string{"sk-test"},
				ServiceType: tt.serviceType,
				Status:      "active",
			})

			w := performChatHandlerRequest(t, router, `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
			}

			var resp struct {
				ID      string `json:"id"`
				Object  string `json:"object"`
				Choices []struct {
					Index   int `json:"index"`
					Message struct {
						Role    string `json:"role"`
						Content string `json:"content"`
					} `json:"message"`
					FinishReason string `json:"finish_reason"`
				} `json:"choices"`
				Usage *struct {
					PromptTokens     int `json:"prompt_tokens"`
					CompletionTokens int `json:"completion_tokens"`
					TotalTokens      int `json:"total_tokens"`
				} `json:"usage"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
				t.Fatalf("unmarshal response: %v, body=%s", err, w.Body.String())
			}

			if len(resp.Choices) == 0 {
				t.Fatalf("choices empty: %s", w.Body.String())
			}
			if resp.Choices[0].Message.Content != tt.expectedText {
				t.Fatalf("content = %q, want %q", resp.Choices[0].Message.Content, tt.expectedText)
			}
			if resp.Choices[0].FinishReason != tt.expectedFinish {
				t.Fatalf("finish_reason = %q, want %q", resp.Choices[0].FinishReason, tt.expectedFinish)
			}
			if resp.Usage == nil {
				t.Fatalf("usage is nil")
			}
			if resp.Usage.PromptTokens != tt.expectedPromptTok {
				t.Fatalf("prompt_tokens = %d, want %d", resp.Usage.PromptTokens, tt.expectedPromptTok)
			}
			if resp.Usage.CompletionTokens != tt.expectedCompleteTok {
				t.Fatalf("completion_tokens = %d, want %d", resp.Usage.CompletionTokens, tt.expectedCompleteTok)
			}
		})
	}
}

// TestChatHandler_NonStreamMatrix_Passthrough 验证 Chat 入口对 gemini/responses 上游
// 走透传路径：上游响应原样返回给客户端（不做格式转换）
func TestChatHandler_NonStreamMatrix_Passthrough(t *testing.T) {
	tests := []struct {
		name         string
		serviceType  string
		responseBody string
	}{
		{
			name:         "chat_handler_to_gemini_passthrough",
			serviceType:  "gemini",
			responseBody: `{"candidates":[{"content":{"role":"model","parts":[{"text":"hi"}]},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":17,"candidatesTokenCount":3,"totalTokenCount":20}}`,
		},
		{
			name:         "chat_handler_to_responses_passthrough",
			serviceType:  "responses",
			responseBody: `{"id":"resp_1","status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"hi"}]}],"usage":{"input_tokens":19,"output_tokens":9,"total_tokens":28}}`,
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

			router := newChatTestRouter(t, config.UpstreamConfig{
				Name:        tt.name,
				BaseURL:     upstream.URL,
				APIKeys:     []string{"sk-test"},
				ServiceType: tt.serviceType,
				Status:      "active",
			})

			w := performChatHandlerRequest(t, router, `{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}`)
			if w.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
			}

			// 透传路径：响应体应与上游原始输出一致
			if !bytes.Contains(w.Body.Bytes(), []byte(tt.responseBody[:50])) {
				t.Fatalf("response body does not contain upstream output prefix, got %s", w.Body.String())
			}
		})
	}
}

func TestChatHandler_NonStreamMatrix_ToolCalls(t *testing.T) {
	// Claude 上游走 convertClaudeResponseToChat 转换，能正确输出 OpenAI tool_calls 格式
	t.Run("chat_handler_tool_from_claude", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"msg_tool","type":"message","role":"assistant","content":[{"type":"tool_use","id":"call_1","name":"Read","input":{"file_path":"/tmp/a"}}],"stop_reason":"tool_use","usage":{"input_tokens":1,"output_tokens":1}}`))
		}))
		defer upstream.Close()

		router := newChatTestRouter(t, config.UpstreamConfig{
			Name:        "chat_claude_tool",
			BaseURL:     upstream.URL,
			APIKeys:     []string{"sk-test"},
			ServiceType: "claude",
			Status:      "active",
		})

		w := performChatHandlerRequest(t, router, `{"model":"claude-3-5-sonnet","messages":[{"role":"user","content":"read file"}]}`)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
		}

		var resp struct {
			Choices []struct {
				Message struct {
					ToolCalls []struct {
						Function struct {
							Name string `json:"name"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"message"`
				FinishReason string `json:"finish_reason"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(resp.Choices) == 0 || len(resp.Choices[0].Message.ToolCalls) == 0 {
			t.Fatalf("expected tool_calls in response, got %s", w.Body.String())
		}
		if resp.Choices[0].Message.ToolCalls[0].Function.Name != "Read" {
			t.Fatalf("tool name = %q, want Read", resp.Choices[0].Message.ToolCalls[0].Function.Name)
		}
		if resp.Choices[0].FinishReason != "tool_calls" {
			t.Fatalf("finish_reason = %q, want tool_calls", resp.Choices[0].FinishReason)
		}
	})

	// openai 上游透传，上游已是 OpenAI tool_calls 格式
	t.Run("chat_handler_tool_from_openai", func(t *testing.T) {
		upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"id":"chat_tool","model":"gpt-4o","choices":[{"message":{"role":"assistant","tool_calls":[{"id":"call_o","type":"function","function":{"name":"Read","arguments":"{\"file_path\":\"/tmp/b\"}"}}]},"finish_reason":"tool_calls"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2}}`))
		}))
		defer upstream.Close()

		router := newChatTestRouter(t, config.UpstreamConfig{
			Name:        "chat_openai_tool",
			BaseURL:     upstream.URL,
			APIKeys:     []string{"sk-test"},
			ServiceType: "openai",
			Status:      "active",
		})

		w := performChatHandlerRequest(t, router, `{"model":"gpt-4o","messages":[{"role":"user","content":"read file"}]}`)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, want %d, body=%s", w.Code, http.StatusOK, w.Body.String())
		}

		var resp struct {
			Choices []struct {
				Message struct {
					ToolCalls []struct {
						Function struct {
							Name string `json:"name"`
						} `json:"function"`
					} `json:"tool_calls"`
				} `json:"message"`
			} `json:"choices"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if len(resp.Choices) == 0 || len(resp.Choices[0].Message.ToolCalls) == 0 {
			t.Fatalf("expected tool_calls in response, got %s", w.Body.String())
		}
		if resp.Choices[0].Message.ToolCalls[0].Function.Name != "Read" {
			t.Fatalf("tool name = %q, want Read", resp.Choices[0].Message.ToolCalls[0].Function.Name)
		}
	})
}
