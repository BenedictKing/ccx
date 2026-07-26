package common

import (
	"testing"

	"github.com/tidwall/gjson"
)

// TestDeprecatedParamFromError 覆盖上游弃用参数错误的识别
func TestDeprecatedParamFromError(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		// 真实案例：api.aijws.com 对 claude-opus-4-8 返回的 400
		{
			name: "反引号包裹 temperature deprecated",
			body: `{"error":{"message":"` + "`temperature`" + ` is deprecated for this model.","type":"server_error"}}`,
			want: "temperature",
		},
		{
			name: "双引号包裹 top_p deprecated",
			body: `{"error":{"message":"\"top_p\" is deprecated","type":"invalid_request_error"}}`,
			want: "top_p",
		},
		{
			name: "裸词 no longer supported",
			body: `{"error":{"message":"temperature is no longer supported"}}`,
			want: "temperature",
		},
		{
			name: "not supported for this model",
			body: `{"error":{"message":"top_k is not supported for this model"}}`,
			want: "top_k",
		},
		{
			name: "嵌套 upstream_error",
			body: `{"error":{"message":"upstream failed","upstream_error":{"message":"` + "`presence_penalty`" + ` is deprecated"}}}`,
			want: "presence_penalty",
		},
		{
			name: "顶层 message",
			body: `{"message":"` + "`frequency_penalty`" + ` is deprecated for this model"}`,
			want: "frequency_penalty",
		},
		// 白名单外的参数不应自动剥离
		{
			name: "非白名单参数 messages 不剥离",
			body: `{"error":{"message":"` + "`messages`" + ` is deprecated"}}`,
			want: "",
		},
		{
			name: "非白名单参数 tools 不剥离",
			body: `{"error":{"message":"tools is no longer supported"}}`,
			want: "",
		},
		// 非弃用类错误
		{
			name: "普通 schema 校验错误",
			body: `{"error":{"message":"messages.0.content: field required"}}`,
			want: "",
		},
		{
			name: "限流错误",
			body: `{"error":{"message":"rate limit exceeded"}}`,
			want: "",
		},
		{"空 body", ``, ""},
		{"非 JSON", `not json at all`, ""},
		{"空 JSON 对象", `{}`, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DeprecatedParamFromError([]byte(tt.body)); got != tt.want {
				t.Errorf("DeprecatedParamFromError() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestStripDeprecatedParams 覆盖请求体参数剥离
func TestStripDeprecatedParams(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		params      []string
		wantChanged bool
		gone        []string // 剥离后应不存在的路径
		kept        []string // 剥离后应保留的路径
	}{
		{
			name:        "剥离顶层 temperature",
			body:        `{"model":"claude-opus-4-8","temperature":0.7,"messages":[]}`,
			params:      []string{"temperature"},
			wantChanged: true,
			gone:        []string{"temperature"},
			kept:        []string{"model", "messages"},
		},
		{
			name:        "剥离 Gemini generationConfig 变体",
			body:        `{"generationConfig":{"temperature":0.5,"maxOutputTokens":100}}`,
			params:      []string{"temperature"},
			wantChanged: true,
			gone:        []string{"generationConfig.temperature"},
			kept:        []string{"generationConfig.maxOutputTokens"},
		},
		{
			name:        "同时剥离多个参数",
			body:        `{"temperature":0.7,"top_p":0.9,"model":"x"}`,
			params:      []string{"temperature", "top_p"},
			wantChanged: true,
			gone:        []string{"temperature", "top_p"},
			kept:        []string{"model"},
		},
		{
			name:        "参数不存在时不改写",
			body:        `{"model":"x","messages":[]}`,
			params:      []string{"temperature"},
			wantChanged: false,
			kept:        []string{"model", "messages"},
		},
		{
			name:        "temperature 为 0 也应剥离",
			body:        `{"temperature":0,"model":"x"}`,
			params:      []string{"temperature"},
			wantChanged: true,
			gone:        []string{"temperature"},
			kept:        []string{"model"},
		},
		{
			name:        "无效 JSON 原样返回",
			body:        `not json`,
			params:      []string{"temperature"},
			wantChanged: false,
		},
		{
			name:        "空参数列表不改写",
			body:        `{"temperature":0.7}`,
			params:      nil,
			wantChanged: false,
			kept:        []string{"temperature"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, changed := StripDeprecatedParams([]byte(tt.body), tt.params)
			if changed != tt.wantChanged {
				t.Errorf("changed = %v, want %v", changed, tt.wantChanged)
			}
			for _, path := range tt.gone {
				if gjson.GetBytes(got, path).Exists() {
					t.Errorf("path %q 应已被剥离，实际仍存在", path)
				}
			}
			for _, path := range tt.kept {
				if !gjson.GetBytes(got, path).Exists() {
					t.Errorf("path %q 应保留，实际被删除", path)
				}
			}
		})
	}
}
