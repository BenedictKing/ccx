package common

import (
	"encoding/json"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/gin-gonic/gin"
)

// TestHandleAllChannelsFailed_ContextCapacity 上下文容量选路错误必须以
// 400 context_length_exceeded 透传，而不是被吞成 503 service_unavailable——
// Codex 等客户端只有拿到这个语义才会压缩上下文而不是无限重试。
func TestHandleAllChannelsFailed_ContextCapacity(t *testing.T) {
	gin.SetMode(gin.TestMode)

	cases := []struct {
		name          string
		lastError     error
		wantStatus    int
		wantCode      string
		wantMessageEq string
	}{
		{
			name: "带已知最大窗口",
			lastError: newSelectionTraceErrForTest(&scheduler.ContextCapacityError{
				InputTokens:    274081,
				TotalBudget:    282273,
				MaxKnownWindow: 272000,
				Detail:         "[responses:3]x actual=gpt-5.6-sol input=274081>272000",
			}),
			wantStatus:    400,
			wantCode:      "context_length_exceeded",
			wantMessageEq: "This model's maximum context length is 272000 tokens. However, you requested 282273 tokens (274081 in the messages, 8192 in the completion). Please reduce the length of the messages or completion.",
		},
		{
			name: "全未知窗口",
			lastError: newSelectionTraceErrForTest(&scheduler.ContextCapacityError{
				InputTokens: 274081,
				TotalBudget: 282273,
			}),
			wantStatus: 400,
			wantCode:   "context_length_exceeded",
		},
		{
			name:       "普通选路错误维持 503",
			lastError:  errors.New("没有具有可用 API Key 的 Responses 渠道"),
			wantStatus: 503,
			wantCode:   "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest("POST", "/v1/responses", strings.NewReader("{}"))

			HandleAllChannelsFailed(c, nil, tc.lastError, "Responses")

			if recorder.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", recorder.Code, tc.wantStatus, recorder.Body.String())
			}
			var body struct {
				Error struct {
					Message string `json:"message"`
					Type    string `json:"type"`
					Code    string `json:"code"`
				} `json:"error"`
			}
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("响应体不是 JSON: %v; body=%s", err, recorder.Body.String())
			}
			if body.Error.Code != tc.wantCode {
				t.Fatalf("code = %q, want %q", body.Error.Code, tc.wantCode)
			}
			if tc.wantMessageEq != "" && body.Error.Message != tc.wantMessageEq {
				t.Fatalf("message = %q, want %q", body.Error.Message, tc.wantMessageEq)
			}
		})
	}
}

// newSelectionTraceErrForTest 模拟选路错误被 SelectionTraceError 包装、
// 再被外层继续包装后的真实形态，验证 errors.As 穿透。
func newSelectionTraceErrForTest(err error) error {
	return errors.Join(errors.New("select: "), &scheduler.SelectionTraceError{Err: err})
}
