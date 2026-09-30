package common

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/warmup"
	"github.com/gin-gonic/gin"
)

// 回归：预检识别的请求侧拒绝（UpstreamRequestRejectedError）终态化后，
// handled=true 的契约是「已向客户端写回响应」。改前该分支只记账直接
// return，两级调用方（多渠道编排/单渠道 handler）都按 handled=true 直接
// 返回且无人补写，客户端拿到 200 + 零字节——正是该提交要修的空 200 模式。
// 用真实 TryUpstreamWithAllKeys 驱动执行器分支，断言错误体确实写出。
func TestRequestRejectedWritesClientResponse(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name         string
		kind         scheduler.ChannelKind
		apiType      string
		wantBodyPart string
	}{
		{
			name:         "chat 入口按 OpenAI 风格回包",
			kind:         scheduler.ChannelKindChat,
			apiType:      "Chat",
			wantBodyPart: `"type":"invalid_request_error"`,
		},
		{
			name:         "gemini 入口按 Google 风格回包",
			kind:         scheduler.ChannelKindGemini,
			apiType:      "Gemini",
			wantBodyPart: `"status":"INVALID_ARGUMENT"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("data: {\"error\":{\"type\":\"invalid_request_error\",\"message\":\"The request body is invalid\"}}\n\n"))
			}))
			t.Cleanup(server.Close)

			cfgManager, channelScheduler, metricsManager, cleanup := newTestFailoverDependencies(t, config.UpstreamConfig{
				Name:        "request-rejected-upstream",
				ChannelUID:  "ch-request-rejected",
				BaseURL:     server.URL,
				APIKeys:     []string{"rejected-test-1"},
				Status:      "active",
				ServiceType: "openai",
			})
			t.Cleanup(cleanup)

			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			reqBody := `{"model":"test-model","messages":[{"role":"user","content":"hi"}]}`
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(reqBody))

			cfg := cfgManager.GetConfig()
			upstream := &cfg.Upstream[0]

			nextKey := cfgManager.GetNextChatAPIKey
			if tc.kind == scheduler.ChannelKindGemini {
				nextKey = cfgManager.GetNextGeminiAPIKey
			}

			handled, _, _, failoverErr, _, lastErr := TryUpstreamWithAllKeys(
				c,
				config.NewEnvConfig(),
				cfgManager,
				channelScheduler,
				tc.kind,
				tc.apiType,
				metricsManager,
				upstream,
				[]warmup.URLLatencyResult{{URL: server.URL, OriginalIdx: 0}},
				[]byte(reqBody),
				nil,
				true,
				nextKey,
				func(c *gin.Context, upstreamCopy *config.UpstreamConfig, apiKey string) (*http.Request, error) {
					return http.NewRequestWithContext(c.Request.Context(), http.MethodPost, upstreamCopy.BaseURL+"/v1/chat/completions", strings.NewReader(reqBody))
				},
				func(apiKey string) {},
				func(url string) {},
				func(url string) {},
				func(c *gin.Context, resp *http.Response, upstreamCopy *config.UpstreamConfig, apiKey string, actualRequestBody []byte) (*types.Usage, error) {
					_ = resp.Body.Close()
					// 模拟 chat/gemini 预检已识别的请求侧拒绝：真实预检链路
					// （RequestSideStreamError → ClassifyPreflightStreamError）
					// 由 stream_data_error_test.go 锁定，这里直接复用同一解析入口。
					return nil, RequestSideStreamError(`{"error":{"type":"invalid_request_error","message":"The request body is invalid"}}`)
				},
				"test-model",
				"",
				0,
				channelScheduler.GetChannelLogStore(tc.kind),
			)

			if !handled {
				t.Fatalf("请求侧拒绝应终态 handled=true, failoverErr=%#v", failoverErr)
			}
			var rejected *UpstreamRequestRejectedError
			if !errors.As(lastErr, &rejected) {
				t.Fatalf("lastErr 应为 UpstreamRequestRejectedError, got %#v", lastErr)
			}
			if w.Code != http.StatusBadRequest {
				t.Fatalf("客户端状态码 = %d, want 400（改前为空 200）", w.Code)
			}
			if !strings.Contains(w.Body.String(), tc.wantBodyPart) {
				t.Fatalf("错误体应包含 %s, got %q", tc.wantBodyPart, w.Body.String())
			}
			if !strings.Contains(w.Body.String(), "The request body is invalid") {
				t.Fatalf("错误体应包含上游原始 message, got %q", w.Body.String())
			}
		})
	}
}

// RespondUpstreamRequestRejected 的字段兜底：type/code 缺失时逐级回退，
// message 缺失时回退到 Error() 摘要，保证错误体永远可解析且非空。
func TestRespondUpstreamRequestRejectedFallbacks(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name         string
		rejected     *UpstreamRequestRejectedError
		wantBodyPart []string
	}{
		{
			name:         "type 与 code 均缺失回退 invalid_request_error",
			rejected:     &UpstreamRequestRejectedError{Message: "boom"},
			wantBodyPart: []string{`"type":"invalid_request_error"`, "boom"},
		},
		{
			name:         "type 缺失用 code 回填",
			rejected:     &UpstreamRequestRejectedError{Code: "request_too_large", Message: "too big"},
			wantBodyPart: []string{`"type":"request_too_large"`, `"code":"request_too_large"`, "too big"},
		},
		{
			name:         "message 缺失回退 Error() 摘要",
			rejected:     &UpstreamRequestRejectedError{Type: "invalid_request_error"},
			wantBodyPart: []string{`"type":"invalid_request_error"`, "upstream rejected the request"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			RespondUpstreamRequestRejected(c, "Chat", tc.rejected)
			if w.Code != http.StatusBadRequest {
				t.Fatalf("状态码 = %d, want 400", w.Code)
			}
			for _, part := range tc.wantBodyPart {
				if !strings.Contains(w.Body.String(), part) {
					t.Fatalf("错误体应包含 %s, got %q", part, w.Body.String())
				}
			}
		})
	}
}
