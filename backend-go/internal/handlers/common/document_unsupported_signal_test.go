package common

import (
	"net/http"
	"testing"
)

func TestDocumentUnsupportedFromError(t *testing.T) {
	tests := []struct {
		name        string
		statusCode  int
		body        string
		hasDocument bool
		wantNil     bool
		wantStrong  bool
	}{
		// 强信号：错误文案直接点名 document / pdf / input_file
		{
			name:        "强信号-Anthropic系Input tag document原文",
			statusCode:  http.StatusBadRequest,
			body:        `{"error":{"type":"invalid_request_error","message":"messages.8.content.4: Input tag 'document' found using 'type' does not match any of the expected tags: 'image', 'text', 'tool_use', 'thinking', 'tool_result', 'web_search_tool_result'"},"type":"error"}`,
			hasDocument: true,
			wantStrong:  true,
		},
		{
			name:        "强信号-Responses系input_file",
			statusCode:  http.StatusBadRequest,
			body:        `{"error":{"type":"invalid_request_error","message":"input_file content type is not supported"}}`,
			hasDocument: true,
			wantStrong:  true,
		},
		{
			name:        "强信号-application/pdf",
			statusCode:  http.StatusBadRequest,
			body:        `{"error":{"type":"invalid_request_error","message":"media type application/pdf is not allowed"}}`,
			hasDocument: true,
			wantStrong:  true,
		},
		{
			name:        "强信号-document邻近否定词",
			statusCode:  http.StatusBadRequest,
			body:        `{"error":{"type":"invalid_request_error","message":"document blocks are not supported by this model"}}`,
			hasDocument: true,
			wantStrong:  true,
		},
		{
			name:        "强信号-否定词前置document",
			statusCode:  http.StatusUnprocessableEntity,
			body:        `{"error":{"type":"invalid_request_error","message":"unsupported document type in content"}}`,
			hasDocument: true,
			wantStrong:  true,
		},
		// 弱信号：通用 invalid_request 且请求带 document（kimi 实际案例）
		{
			name:        "弱信号-kimi通用Invalid request Error",
			statusCode:  http.StatusBadRequest,
			body:        `{"error":{"type":"invalid_request_error","message":"Invalid request Error"},"type":"error"}`,
			hasDocument: true,
			wantStrong:  false,
		},
		{
			name:        "弱信号-422顶层type形态",
			statusCode:  http.StatusUnprocessableEntity,
			body:        `{"type":"invalid_request_error","message":"bad request"}`,
			hasDocument: true,
			wantStrong:  false,
		},
		{
			name:        "弱信号-顶层message字段",
			statusCode:  http.StatusBadRequest,
			body:        `{"type":"invalid_request_error","message":"Invalid request"}`,
			hasDocument: true,
			wantStrong:  false,
		},
		// 负例
		{
			name:        "负例-请求无document即使强信号也不学",
			statusCode:  http.StatusBadRequest,
			body:        `{"error":{"type":"invalid_request_error","message":"Input tag 'document' does not match any of the expected tags"}}`,
			hasDocument: false,
			wantNil:     true,
		},
		{
			name:        "负例-带参数名的400不学",
			statusCode:  http.StatusBadRequest,
			body:        `{"error":{"type":"invalid_request_error","message":"temperature is not a valid parameter"}}`,
			hasDocument: true,
			wantNil:     true,
		},
		{
			name:        "负例-通用type混入具体message不学",
			statusCode:  http.StatusBadRequest,
			body:        `{"error":{"type":"invalid_request_error","message":"Invalid request","upstream_error":{"message":"max_tokens exceeds limit"}}}`,
			hasDocument: true,
			wantNil:     true,
		},
		{
			name:        "负例-quota类错误不学",
			statusCode:  http.StatusBadRequest,
			body:        `{"error":{"type":"insufficient_quota","message":"insufficient quota"}}`,
			hasDocument: true,
			wantNil:     true,
		},
		{
			name:        "负例-401不参与学习",
			statusCode:  http.StatusUnauthorized,
			body:        `{"error":{"type":"authentication_error","message":"invalid api key"}}`,
			hasDocument: true,
			wantNil:     true,
		},
		{
			name:        "负例-500不参与学习",
			statusCode:  http.StatusInternalServerError,
			body:        `{"error":{"type":"invalid_request_error","message":"Input tag 'document' does not match any of the expected tags"}}`,
			hasDocument: true,
			wantNil:     true,
		},
		{
			name:        "负例-429不参与学习",
			statusCode:  http.StatusTooManyRequests,
			body:        `{"error":{"type":"invalid_request_error","message":"Invalid request"}}`,
			hasDocument: true,
			wantNil:     true,
		},
		{
			name:        "负例-空body",
			statusCode:  http.StatusBadRequest,
			body:        ``,
			hasDocument: true,
			wantNil:     true,
		},
		{
			name:        "负例-非JSONbody",
			statusCode:  http.StatusBadRequest,
			body:        `Invalid request`,
			hasDocument: true,
			wantNil:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			signal := DocumentUnsupportedFromError(tt.statusCode, []byte(tt.body), tt.hasDocument)
			if tt.wantNil {
				if signal != nil {
					t.Fatalf("期望 nil，实际 %+v", signal)
				}
				return
			}
			if signal == nil {
				t.Fatal("期望识别出信号，实际 nil")
			}
			if signal.Strong != tt.wantStrong {
				t.Errorf("Strong = %v，期望 %v", signal.Strong, tt.wantStrong)
			}
			if signal.Evidence == "" {
				t.Error("Evidence 不应为空")
			}
		})
	}
}
