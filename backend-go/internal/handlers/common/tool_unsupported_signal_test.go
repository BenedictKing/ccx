package common

import (
	"net/http"
	"testing"
)

func TestToolUnsupportedFromError(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		hasTools   bool
		wantNil    bool
	}{
		// 强信号：错误文案点名 tools / tool use / function calling
		{
			name:       "强信号-tools not supported",
			statusCode: http.StatusBadRequest,
			body:       `{"error":{"type":"invalid_request_error","message":"tools are not supported by this model"}}`,
			hasTools:   true,
		},
		{
			name:       "强信号-tool use 未开启",
			statusCode: http.StatusBadRequest,
			body:       `{"error":{"message":"Tool use is not enabled for this model"}}`,
			hasTools:   true,
		},
		{
			name:       "强信号-否定词前置 tools",
			statusCode: http.StatusUnprocessableEntity,
			body:       `{"error":{"message":"unsupported parameter: tools"}}`,
			hasTools:   true,
		},
		{
			name:       "强信号-function calling 不支持",
			statusCode: http.StatusBadRequest,
			body:       `{"error":{"message":"function calling is disabled for this deployment"}}`,
			hasTools:   true,
		},
		{
			name:       "强信号-does not support tools",
			statusCode: http.StatusBadRequest,
			body:       `{"error":{"message":"this model does not support tools"}}`,
			hasTools:   true,
		},
		// 不学习：错误不含工具所指
		{
			name:       "不学习-具体参数名报错",
			statusCode: http.StatusBadRequest,
			body:       `{"error":{"message":"messages.3.content: temperature must be between 0 and 1"}}`,
			hasTools:   true,
			wantNil:    true,
		},
		{
			name:       "不学习-通用 invalid_request（无弱信号归因）",
			statusCode: http.StatusBadRequest,
			body:       `{"error":{"type":"invalid_request_error","message":"Invalid request Error"}}`,
			hasTools:   true,
			wantNil:    true,
		},
		{
			name:       "不学习-5xx 容量问题",
			statusCode: http.StatusServiceUnavailable,
			body:       `{"error":{"message":"tools are not supported"}}`,
			hasTools:   true,
			wantNil:    true,
		},
		{
			name:       "不学习-请求未携带 tools",
			statusCode: http.StatusBadRequest,
			body:       `{"error":{"message":"tools are not supported"}}`,
			hasTools:   false,
			wantNil:    true,
		},
		{
			name:       "不学习-空响应体",
			statusCode: http.StatusBadRequest,
			body:       ``,
			hasTools:   true,
			wantNil:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ToolUnsupportedFromError(tt.statusCode, []byte(tt.body), tt.hasTools)
			if tt.wantNil && got != nil {
				t.Fatalf("ToolUnsupportedFromError() = %+v, want nil", got)
			}
			if !tt.wantNil && got == nil {
				t.Fatal("ToolUnsupportedFromError() = nil, want signal")
			}
		})
	}
}

func TestForcedToolChoiceInBody(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{"messages-指定工具", `{"tool_choice":{"type":"tool","name":"ccx_probe"},"tools":[{}]}`, true},
		{"messages-任一工具", `{"tool_choice":{"type":"any"}}`, true},
		{"chat-function", `{"tool_choice":{"type":"function","function":{"name":"ccx_probe"}}}`, true},
		{"chat-legacy name 对象", `{"tool_choice":{"name":"ccx_probe"}}`, true},
		{"chat-required 字符串", `{"tool_choice":"required"}`, true},
		{"responses-custom", `{"tool_choice":{"type":"custom","name":"ccx_probe"}}`, true},
		{"gemini-ANY", `{"tool_config":{"function_calling_config":{"mode":"ANY"}}}`, true},
		{"auto-非强制", `{"tool_choice":{"type":"auto"}}`, false},
		{"none-非强制", `{"tool_choice":"none"}`, false},
		{"未声明", `{"messages":[]}`, false},
		{"空对象", `{}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ForcedToolChoiceInBody([]byte(tt.body)); got != tt.want {
				t.Fatalf("ForcedToolChoiceInBody() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBodyHasTools(t *testing.T) {
	tests := []struct {
		name string
		body string
		want bool
	}{
		{"messages 带工具", `{"tools":[{"name":"a"}]}`, true},
		{"gemini 带工具", `{"tools":[{"functionDeclarations":[{}]}]}`, true},
		{"空工具数组", `{"tools":[]}`, false},
		{"缺失字段", `{"messages":[]}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := BodyHasTools([]byte(tt.body)); got != tt.want {
				t.Fatalf("BodyHasTools() = %v, want %v", got, tt.want)
			}
		})
	}
}
