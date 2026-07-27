package common

import (
	"testing"

	"github.com/tidwall/gjson"
)

func TestApplyKnownParamConstraints(t *testing.T) {
	tests := []struct {
		name        string
		model       string
		body        string
		wantApplied bool
		// check 在改写后的请求体上做断言
		check func(t *testing.T, body []byte)
	}{
		{
			name:        "kimi-k3 剥离固定值采样参数",
			model:       "kimi-k3",
			body:        `{"model":"kimi-k3","temperature":0.7,"top_p":0.8,"n":2,"presence_penalty":0.5,"frequency_penalty":0.5,"messages":[]}`,
			wantApplied: true,
			check: func(t *testing.T, body []byte) {
				for _, param := range []string{"temperature", "top_p", "n", "presence_penalty", "frequency_penalty"} {
					if gjson.GetBytes(body, param).Exists() {
						t.Errorf("参数 %s 应已被剥离", param)
					}
				}
				if !gjson.GetBytes(body, "messages").Exists() {
					t.Error("messages 不应被影响")
				}
			},
		},
		{
			name:        "k2.7-code 非法 thinking 归一化为 keep all",
			model:       "kimi-k2.7-code-highspeed",
			body:        `{"model":"kimi-k2.7-code-highspeed","thinking":{"type":"disabled"}}`,
			wantApplied: true,
			check: func(t *testing.T, body []byte) {
				if got := gjson.GetBytes(body, "thinking.type").String(); got != "enabled" {
					t.Errorf("thinking.type = %q, want enabled", got)
				}
				if got := gjson.GetBytes(body, "thinking.keep").String(); got != "all" {
					t.Errorf("thinking.keep = %q, want all", got)
				}
			},
		},
		{
			name:        "k3 支持 required 不降级",
			model:       "kimi-k3",
			body:        `{"model":"kimi-k3","tool_choice":"required"}`,
			wantApplied: false,
			check: func(t *testing.T, body []byte) {
				if got := gjson.GetBytes(body, "tool_choice").String(); got != "required" {
					t.Errorf("tool_choice = %q, want required", got)
				}
			},
		},
		{
			name:        "moonshot-v1 可改参数不剥离",
			model:       "moonshot-v1-8k",
			body:        `{"model":"moonshot-v1-8k","temperature":0.7,"presence_penalty":0.5}`,
			wantApplied: false,
			check: func(t *testing.T, body []byte) {
				if !gjson.GetBytes(body, "temperature").Exists() {
					t.Error("moonshot-v1 的 temperature 可修改，不应被剥离")
				}
			},
		},
		{
			name:        "k2.6 的 tool_choice required 降级为 auto",
			model:       "kimi-k2.6",
			body:        `{"model":"kimi-k2.6","tool_choice":"required","tools":[{"type":"function"}]}`,
			wantApplied: true,
			check: func(t *testing.T, body []byte) {
				if got := gjson.GetBytes(body, "tool_choice").String(); got != "auto" {
					t.Errorf("tool_choice = %q, want auto", got)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, applied := ApplyKnownParamConstraints([]byte(tt.body), tt.model)
			if tt.wantApplied && len(applied) == 0 {
				t.Fatal("期望发生改写，实际未改写")
			}
			if !tt.wantApplied && len(applied) > 0 {
				t.Fatalf("期望不改写，实际改写 %v", applied)
			}
			if tt.check != nil {
				tt.check(t, got)
			}
		})
	}
}
