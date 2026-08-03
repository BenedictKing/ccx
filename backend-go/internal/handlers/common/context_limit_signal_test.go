package common

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/BenedictKing/ccx/internal/autopilot"
	"github.com/BenedictKing/ccx/internal/config"
	"github.com/gin-gonic/gin"
)

func TestContextLimitFromError(t *testing.T) {
	tests := []struct {
		name          string
		statusCode    int
		body          string
		estimated     int
		wantNil       bool
		wantMaxTokens int
		wantSource    string
	}{
		{
			name:          "上游声明具体窗口值",
			statusCode:    http.StatusBadRequest,
			body:          `{"error":{"message":"This model's maximum context length is 272000 tokens, however you requested 1050000 tokens"}}`,
			estimated:     1_050_000,
			wantMaxTokens: 272_000,
			wantSource:    config.CompatSourceUpstreamDeclared,
		},
		{
			name:          "只说超限时按被拒量反推保守上界",
			statusCode:    http.StatusBadRequest,
			body:          `{"error":{"code":"context_length_exceeded","message":"context_length_exceeded: input tokens too many"}}`,
			estimated:     80_000,
			wantMaxTokens: 70_000, // 80000 * 7/8
			wantSource:    config.CompatSourceRejectedEstimate,
		},
		{
			name:       "无估算且上游未声明数值时不学习",
			statusCode: http.StatusBadRequest,
			body:       `{"error":{"message":"context_too_large"}}`,
			estimated:  0,
			wantNil:    true,
		},
		{
			name:       "输出上限报错不得当成输入上下文上限",
			statusCode: http.StatusBadRequest,
			body:       `{"error":{"message":"max_tokens is too large: maximum context length is 8192 tokens"}}`,
			estimated:  50_000,
			wantNil:    true,
		},
		{
			name:       "请求体字节过大不是上下文问题",
			statusCode: http.StatusBadRequest,
			body:       `{"error":{"message":"request body too large, payload too large"}}`,
			estimated:  50_000,
			wantNil:    true,
		},
		{
			name:       "配额超限不参与上下文学习",
			statusCode: http.StatusBadRequest,
			body:       `{"error":{"message":"quota exceeded, too many tokens used this month"}}`,
			estimated:  50_000,
			wantNil:    true,
		},
		{
			name:       "429 不参与能力学习",
			statusCode: http.StatusTooManyRequests,
			body:       `{"error":{"message":"context_length_exceeded"}}`,
			estimated:  50_000,
			wantNil:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ContextLimitFromError(tt.statusCode, []byte(tt.body), tt.estimated)
			if tt.wantNil {
				if got != nil {
					t.Fatalf("ContextLimitFromError() = %+v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatal("ContextLimitFromError() = nil, want signal")
			}
			if got.MaxInputTokens != tt.wantMaxTokens {
				t.Errorf("MaxInputTokens = %d, want %d", got.MaxInputTokens, tt.wantMaxTokens)
			}
			if got.Source != tt.wantSource {
				t.Errorf("Source = %q, want %q", got.Source, tt.wantSource)
			}
		})
	}
}

// TestDeclaredContextLimitIgnoresRequestedAmount 覆盖最容易出错的一步：
// 报错里同时有「上限」和「你请求了多少」两个数字，绝不能把后者当成窗口学进去，
// 否则学到的上限比真实窗口大，硬约束依然形同虚设。
func TestDeclaredContextLimitIgnoresRequestedAmount(t *testing.T) {
	msg := "this model's maximum context length is 272000 tokens, however you requested 1050000 tokens"

	if got := declaredContextLimit(msg, 1_050_000); got != 272_000 {
		t.Errorf("declaredContextLimit() = %d, want 272000", got)
	}
}

func TestDeclaredContextLimitRejectsValueAboveRejectedAmount(t *testing.T) {
	// 唯一出现的数字就等于被拒量：它不可能是真实上限（真实上限一定更小），必须放弃
	msg := "you requested 1050000 tokens which exceeds the context window of 1050000"

	if got := declaredContextLimit(msg, 1_050_000); got != 0 {
		t.Errorf("declaredContextLimit() = %d, want 0（不可信应放弃提取）", got)
	}
}

func TestEstimatedInputTokensForContextLimitPrefersInputEstimate(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	profile := autopilot.RequestProfile{EstTokens: 80_000, ContextNeed: 96_000}
	req = req.WithContext(autopilot.ContextWithRequestProfile(req.Context(), profile))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = req

	if got := estimatedInputTokensForContextLimit(c, []byte(`{"messages":[]}`)); got != profile.EstTokens {
		t.Fatalf("estimatedInputTokensForContextLimit() = %d, want %d", got, profile.EstTokens)
	}
}
