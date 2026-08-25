package common

import (
	"testing"

	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/tidwall/gjson"
)

func TestMaxOutputTokensInBody(t *testing.T) {
	tests := []struct {
		name string
		kind scheduler.ChannelKind
		body string
		want int
	}{
		{name: "messages max_tokens", kind: scheduler.ChannelKindMessages, body: `{"max_tokens":64000}`, want: 64000},
		{name: "chat 取优先级最高的字段", kind: scheduler.ChannelKindChat, body: `{"max_completion_tokens":32000,"max_tokens":64000}`, want: 32000},
		{name: "gemini 嵌套字段", kind: scheduler.ChannelKindGemini, body: `{"generationConfig":{"maxOutputTokens":8192}}`, want: 8192},
		{name: "字段缺失返回 0", kind: scheduler.ChannelKindMessages, body: `{"model":"kimi-k2.6"}`, want: 0},
		{name: "非法 JSON 返回 0", kind: scheduler.ChannelKindMessages, body: `{`, want: 0},
		{name: "images 无输出字段返回 0", kind: scheduler.ChannelKindImages, body: `{"max_tokens":64000}`, want: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := maxOutputTokensInBody([]byte(tt.body), tt.kind); got != tt.want {
				t.Fatalf("maxOutputTokensInBody = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestClampMaxTokensInBody(t *testing.T) {
	tests := []struct {
		name      string
		kind      scheduler.ChannelKind
		body      string
		field     string
		want      int64
		wantClamp bool
	}{
		{
			name:      "messages max_tokens",
			kind:      scheduler.ChannelKindMessages,
			body:      `{"model":"kimi-k2.7-code","max_tokens":64000}`,
			field:     "max_tokens",
			want:      32768,
			wantClamp: true,
		},
		{
			name:      "chat max_completion_tokens priority",
			kind:      scheduler.ChannelKindChat,
			body:      `{"max_completion_tokens":64000,"max_tokens":64000}`,
			field:     "max_completion_tokens",
			want:      32768,
			wantClamp: true,
		},
		{
			name:      "chat clamps all aliases",
			kind:      scheduler.ChannelKindChat,
			body:      `{"max_completion_tokens":64000,"max_tokens":64000}`,
			field:     "max_tokens",
			want:      32768,
			wantClamp: true,
		},
		{
			name:      "responses max_output_tokens",
			kind:      scheduler.ChannelKindResponses,
			body:      `{"max_output_tokens":64000}`,
			field:     "max_output_tokens",
			want:      32768,
			wantClamp: true,
		},
		{
			name:      "gemini nested maxOutputTokens",
			kind:      scheduler.ChannelKindGemini,
			body:      `{"generationConfig":{"maxOutputTokens":64000}}`,
			field:     "generationConfig.maxOutputTokens",
			want:      32768,
			wantClamp: true,
		},
		{
			name:      "below limit unchanged",
			kind:      scheduler.ChannelKindMessages,
			body:      `{"max_tokens":100}`,
			field:     "max_tokens",
			want:      100,
			wantClamp: false,
		},
		{
			name:      "missing field unchanged",
			kind:      scheduler.ChannelKindMessages,
			body:      `{"model":"kimi-k2.7-code"}`,
			field:     "max_tokens",
			want:      0,
			wantClamp: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, clamped := clampMaxTokensInBody([]byte(tt.body), tt.kind, 32768)
			if clamped != tt.wantClamp {
				t.Fatalf("clamped=%v, want %v", clamped, tt.wantClamp)
			}
			if !gjson.GetBytes(got, tt.field).Exists() {
				if tt.want != 0 {
					t.Fatalf("field %s missing", tt.field)
				}
				return
			}
			if value := gjson.GetBytes(got, tt.field).Int(); value != tt.want {
				t.Fatalf("%s=%d, want %d", tt.field, value, tt.want)
			}
		})
	}
}
