package common

import (
	"net/http"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

func TestStreamRequirementFromError(t *testing.T) {
	tests := []struct {
		name            string
		statusCode      int
		body            string
		requestIsStream bool
		wantNil         bool
	}{
		{
			name:       "实测文案-英文",
			statusCode: http.StatusBadRequest,
			body:       `{"error":{"message":"streaming is required: this endpoint only accepts \"stream\": true"}}`,
		},
		{
			name:       "无空格变体",
			statusCode: http.StatusBadRequest,
			body:       `{"error":{"message":"this endpoint only accepts \"stream\":true"}}`,
		},
		{
			name:       "中文文案",
			statusCode: http.StatusBadRequest,
			body:       `{"error":{"message":"该分组仅支持流式调用"}}`,
		},
		{
			name:       "嵌套 upstream_error",
			statusCode: http.StatusBadRequest,
			body:       `{"error":{"upstream_error":{"message":"streaming is required"}}}`,
		},
		{
			name:       "实测文案-中文禁止非流",
			statusCode: http.StatusBadRequest,
			body:       `{"error":{"type":"stream_required","message":"本渠道禁止非流请求 (request id: 202609230554293194136808268d9d65HVnJS5H)"},"type":"error"}`,
		},
		{
			name:       "错误码强信号-文案无关",
			statusCode: http.StatusBadRequest,
			body:       `{"error":{"type":"stream_required","message":"invalid payload"}}`,
		},
		{
			name:       "顶层 code 强信号",
			statusCode: http.StatusBadRequest,
			body:       `{"code":"stream_required","message":"x"}`,
		},
		{
			name:       "中文文案-无错误码",
			statusCode: http.StatusBadRequest,
			body:       `{"error":{"message":"本渠道禁止非流请求"}}`,
		},
		{
			name:       "英文文案-non-stream not supported",
			statusCode: http.StatusBadRequest,
			body:       `{"error":{"message":"non-streaming requests are not supported by this model"}}`,
		},
		{
			name:       "反义错误码不误判",
			statusCode: http.StatusBadRequest,
			body:       `{"error":{"type":"not_stream_required","message":"invalid payload"}}`,
			wantNil:    true,
		},
		{
			name:            "流式请求不学习",
			statusCode:      http.StatusBadRequest,
			body:            `{"error":{"message":"streaming is required"}}`,
			requestIsStream: true,
			wantNil:         true,
		},
		{
			name:       "非400422不学习",
			statusCode: http.StatusInternalServerError,
			body:       `{"error":{"message":"streaming is required"}}`,
			wantNil:    true,
		},
		{
			name:       "非JSON不学习",
			statusCode: http.StatusBadRequest,
			body:       `<html>bad gateway</html>`,
			wantNil:    true,
		},
		{
			name:       "无关文案不学习",
			statusCode: http.StatusBadRequest,
			body:       `{"error":{"message":"messages[0].role: unknown variant ` + "`developer`" + `"}}`,
			wantNil:    true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			signal := StreamRequirementFromError(tt.statusCode, []byte(tt.body), tt.requestIsStream)
			if tt.wantNil {
				if signal != nil {
					t.Fatalf("不应识别出流式要求信号, got %+v", signal)
				}
				return
			}
			if signal == nil {
				t.Fatal("应识别出流式要求信号")
			}
			if signal.Trait != config.TraitRequiresStream {
				t.Fatalf("Trait = %q, want %q", signal.Trait, config.TraitRequiresStream)
			}
			if !signal.Enabled {
				t.Fatal("Enabled = false, want true")
			}
			if signal.Evidence == "" {
				t.Fatal("Evidence 不应为空")
			}
		})
	}
}

func TestIsStreamRequirementError(t *testing.T) {
	tests := []struct {
		name   string
		errObj map[string]interface{}
		want   bool
	}{
		{
			name:   "error.message 命中",
			errObj: map[string]interface{}{"message": `streaming is required: this endpoint only accepts "stream": true`},
			want:   true,
		},
		{
			name:   "type 强信号命中",
			errObj: map[string]interface{}{"type": "stream_required"},
			want:   true,
		},
		{
			name:   "反义 code 不命中",
			errObj: map[string]interface{}{"type": "not_stream_required"},
			want:   false,
		},
		{
			name:   "嵌套 upstream_error.message 命中",
			errObj: map[string]interface{}{"upstream_error": map[string]interface{}{"message": "only accepts 'stream': true"}},
			want:   true,
		},
		{
			name:   "无关 is required 文案不命中",
			errObj: map[string]interface{}{"message": "max_tokens is required"},
			want:   false,
		},
		{
			name:   "普通 schema 错误不命中",
			errObj: map[string]interface{}{"message": "messages[0].role: unknown variant"},
			want:   false,
		},
		{
			name:   "空对象不命中",
			errObj: map[string]interface{}{},
			want:   false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isStreamRequirementError(tt.errObj); got != tt.want {
				t.Fatalf("isStreamRequirementError = %v, want %v", got, tt.want)
			}
		})
	}
}

// 放行回归：该文案含 "is required"，修复前会被判成 schema 校验类不可重试 400，
// 既不学习也不 failover。修复后应放行 failover；无关的 "is required" 文案仍被拦截。
func TestShouldRetryWithNextKeyStreamRequirement(t *testing.T) {
	streamRequiredBody := []byte(`{"error":{"message":"streaming is required: this endpoint only accepts \"stream\": true"}}`)
	if shouldFailover, _ := ShouldRetryWithNextKey(http.StatusBadRequest, streamRequiredBody, "Chat"); !shouldFailover {
		t.Fatal("仅接受流式类 400 应放行 failover")
	}

	otherRequiredBody := []byte(`{"error":{"message":"max_tokens is required"}}`)
	if shouldFailover, _ := ShouldRetryWithNextKey(http.StatusBadRequest, otherRequiredBody, "Chat"); shouldFailover {
		t.Fatal("无关的 is required 文案应保持 schema 校验拦截语义")
	}
}
