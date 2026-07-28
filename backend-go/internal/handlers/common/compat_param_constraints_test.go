package common

import (
	"testing"

	"github.com/tidwall/gjson"

	"github.com/BenedictKing/ccx/internal/config"
)

func TestApplyKnownParamConstraints(t *testing.T) {
	kimiFixedConstraints := &config.ModelParamConstraints{
		FixedParams: []string{"temperature", "top_p", "n", "presence_penalty", "frequency_penalty"},
	}
	thinkingFixedConstraints := &config.ModelParamConstraints{
		ThinkingFixedValue: map[string]interface{}{"type": "enabled", "keep": "all"},
	}
	toolChoiceConstraints := &config.ModelParamConstraints{
		ToolChoiceRequiredUnsupported: true,
	}

	tests := []struct {
		name        string
		constraints *config.ModelParamConstraints
		body        string
		wantApplied bool
		// check 在改写后的请求体上做断言
		check func(t *testing.T, body []byte)
	}{
		{
			name:        "kimi-k3 剥离固定值采样参数",
			constraints: kimiFixedConstraints,
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
			constraints: thinkingFixedConstraints,
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
			name:        "thinking 已等于固定值时不产生改写",
			constraints: thinkingFixedConstraints,
			body:        `{"model":"kimi-k2.7-code","thinking":{"type":"enabled","keep":"all"}}`,
			wantApplied: false,
		},
		{
			name:        "k3 未配置 tool_choice 约束不降级",
			constraints: nil,
			body:        `{"model":"kimi-k3","tool_choice":"required"}`,
			wantApplied: false,
			check: func(t *testing.T, body []byte) {
				if got := gjson.GetBytes(body, "tool_choice").String(); got != "required" {
					t.Errorf("tool_choice = %q, want required", got)
				}
			},
		},
		{
			name:        "moonshot-v1 未收录时不剥离",
			constraints: nil,
			body:        `{"model":"moonshot-v1-8k","temperature":0.7,"presence_penalty":0.5}`,
			wantApplied: false,
			check: func(t *testing.T, body []byte) {
				if !gjson.GetBytes(body, "temperature").Exists() {
					t.Error("未配置约束时不应剥离 temperature")
				}
			},
		},
		{
			name:        "k2.6 的 tool_choice required 降级为 auto",
			constraints: toolChoiceConstraints,
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
			got, applied := ApplyKnownParamConstraints([]byte(tt.body), tt.constraints)
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

// 端到端验证约束数据真的从 model-registry 里读出来，而不是本地硬编码表：
// 这是本次迁移的核心目的——运营者更新 shared/model-registry/ccx_model_registry.json
// 后，无需重新编译发版即可让线上实例应用新的参数约束。
func TestApplyKnownParamConstraintsFromRegistry(t *testing.T) {
	tests := []struct {
		name   string
		model  string
		body   string
		verify func(t *testing.T, body []byte)
	}{
		{
			name:  "kimi-k3 从注册表读固定值参数并剥离",
			model: "kimi-k3",
			body:  `{"model":"kimi-k3","temperature":0.7,"messages":[]}`,
			verify: func(t *testing.T, body []byte) {
				if gjson.GetBytes(body, "temperature").Exists() {
					t.Error("temperature 应已被剥离")
				}
			},
		},
		{
			name:  "kimi-k2.6 从注册表读 tool_choice 约束并降级",
			model: "kimi-k2.6",
			body:  `{"model":"kimi-k2.6","tool_choice":"required"}`,
			verify: func(t *testing.T, body []byte) {
				if got := gjson.GetBytes(body, "tool_choice").String(); got != "auto" {
					t.Errorf("tool_choice = %q, want auto", got)
				}
			},
		},
		{
			name:  "kimi-k2.7-code 从注册表读 thinking 固定值并归一化",
			model: "kimi-k2.7-code",
			body:  `{"model":"kimi-k2.7-code","thinking":{"type":"disabled"}}`,
			verify: func(t *testing.T, body []byte) {
				if got := gjson.GetBytes(body, "thinking.type").String(); got != "enabled" {
					t.Errorf("thinking.type = %q, want enabled", got)
				}
			},
		},
		{
			name:  "moonshot-v1 未收录约束，不应改写",
			model: "moonshot-v1-8k",
			body:  `{"model":"moonshot-v1-8k","temperature":0.7}`,
			verify: func(t *testing.T, body []byte) {
				if !gjson.GetBytes(body, "temperature").Exists() {
					t.Error("moonshot-v1 未配置约束，temperature 不应被剥离")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resolved := config.ResolveUpstreamCapability(tt.model, nil, nil)
			got, _ := ApplyKnownParamConstraints([]byte(tt.body), resolved.Capability.ParamConstraints)
			tt.verify(t, got)
		})
	}
}
