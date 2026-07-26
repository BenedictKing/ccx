package common

import (
	"testing"

	"github.com/BenedictKing/ccx/internal/autopilot"
	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/tidwall/gjson"
)

// TestEffortInjectionStyle 断言注入形态由渠道种类/ServiceType 决定，而非模型名匹配。
func TestEffortInjectionStyle(t *testing.T) {
	tests := []struct {
		name     string
		kind     scheduler.ChannelKind
		upstream *config.UpstreamConfig
		want     string
	}{
		{
			name:     "gemini 渠道走 Gemini 原生形态",
			kind:     scheduler.ChannelKindGemini,
			upstream: &config.UpstreamConfig{},
			want:     config.ReasoningParamStyleGemini,
		},
		{
			name:     "gemini 渠道忽略配置里的 thinking 形态",
			kind:     scheduler.ChannelKindGemini,
			upstream: &config.UpstreamConfig{ReasoningParamStyle: "thinking"},
			want:     config.ReasoningParamStyleGemini,
		},
		{
			name:     "非 gemini 渠道但 ServiceType=gemini 也走 Gemini 形态",
			kind:     scheduler.ChannelKindChat,
			upstream: &config.UpstreamConfig{ServiceType: "gemini"},
			want:     config.ReasoningParamStyleGemini,
		},
		{
			name:     "messages 渠道沿用 thinking 形态",
			kind:     scheduler.ChannelKindMessages,
			upstream: &config.UpstreamConfig{ReasoningParamStyle: "thinking"},
			want:     "thinking",
		},
		{
			name:     "chat 渠道沿用 reasoning_effort 形态",
			kind:     scheduler.ChannelKindChat,
			upstream: &config.UpstreamConfig{ReasoningParamStyle: "reasoning_effort"},
			want:     "reasoning_effort",
		},
		{
			name:     "未配置形态时回落到 Responses 的 reasoning 对象",
			kind:     scheduler.ChannelKindResponses,
			upstream: &config.UpstreamConfig{},
			want:     "reasoning",
		},
		// 自动托管渠道会被 RuntimeUpstreamForAutoManagedProvider 清空 ReasoningParamStyle，
		// 此时必须按渠道类型推导原生形态，否则 effort 会写到上游不识别的字段被静默丢弃。
		{
			name:     "messages 渠道未配置形态时推导为 thinking",
			kind:     scheduler.ChannelKindMessages,
			upstream: &config.UpstreamConfig{},
			want:     "thinking",
		},
		{
			name:     "chat 渠道未配置形态时推导为 reasoning_effort",
			kind:     scheduler.ChannelKindChat,
			upstream: &config.UpstreamConfig{},
			want:     "reasoning_effort",
		},
		// images / vectors 不接受思考参数，返回空串表示不注入
		{
			name:     "images 渠道不注入",
			kind:     scheduler.ChannelKindImages,
			upstream: &config.UpstreamConfig{ReasoningParamStyle: "thinking"},
			want:     "",
		},
		{
			name:     "vectors 渠道不注入",
			kind:     scheduler.ChannelKindVectors,
			upstream: &config.UpstreamConfig{ReasoningParamStyle: "reasoning_effort"},
			want:     "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := effortInjectionStyle(tt.kind, tt.upstream); got != tt.want {
				t.Errorf("effortInjectionStyle() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestAtomicModelEffortRewrite_ByChannelKind 断言各渠道的最终注入形态。
func TestAtomicModelEffortRewrite_ByChannelKind(t *testing.T) {
	tests := []struct {
		name     string
		kind     scheduler.ChannelKind
		upstream *config.UpstreamConfig
		effort   autopilot.EffortLevel
		body     string
		// wantPaths: gjson 路径 -> 期望字符串值（数字比较用 gjson Raw）
		wantPaths map[string]string
		// wantAbsentPaths: 必须不存在的路径
		wantAbsentPaths []string
	}{
		{
			name:     "gemini 渠道写 thinkingLevel",
			kind:     scheduler.ChannelKindGemini,
			upstream: &config.UpstreamConfig{},
			effort:   autopilot.EffortHigh,
			body:     `{"model":"old","contents":[]}`,
			wantPaths: map[string]string{
				"model": "gemini-3.5-flash",
				"generationConfig.thinkingConfig.thinkingLevel": "high",
			},
			wantAbsentPaths: []string{"thinking", "reasoning", "reasoning_effort", "generationConfig.thinkingConfig.thinkingBudget"},
		},
		{
			name:     "gemini 渠道 off 用 thinkingBudget=0 关闭",
			kind:     scheduler.ChannelKindGemini,
			upstream: &config.UpstreamConfig{},
			effort:   autopilot.EffortOff,
			body:     `{"model":"old","contents":[]}`,
			wantPaths: map[string]string{
				"generationConfig.thinkingConfig.thinkingBudget": "0",
			},
			wantAbsentPaths: []string{"generationConfig.thinkingConfig.thinkingLevel"},
		},
		{
			name:     "gemini 渠道 max 收敛到 high",
			kind:     scheduler.ChannelKindGemini,
			upstream: &config.UpstreamConfig{},
			effort:   autopilot.EffortMax,
			body:     `{"model":"old","contents":[]}`,
			wantPaths: map[string]string{
				"generationConfig.thinkingConfig.thinkingLevel": "high",
			},
		},
		{
			name:      "gemini 渠道无法映射的档位只改写 model",
			kind:      scheduler.ChannelKindGemini,
			upstream:  &config.UpstreamConfig{},
			effort:    autopilot.EffortLevel("turbo"),
			body:      `{"model":"old","contents":[]}`,
			wantPaths: map[string]string{"model": "gemini-3.5-flash"},
			wantAbsentPaths: []string{
				"generationConfig.thinkingConfig.thinkingLevel",
				"generationConfig.thinkingConfig.thinkingBudget",
			},
		},
		{
			name:      "messages 渠道保持 thinking.effort 形态",
			kind:      scheduler.ChannelKindMessages,
			upstream:  &config.UpstreamConfig{ReasoningParamStyle: "thinking"},
			effort:    autopilot.EffortHigh,
			body:      `{"model":"old","messages":[]}`,
			wantPaths: map[string]string{"thinking.type": "enabled", "thinking.effort": "high"},
			wantAbsentPaths: []string{
				"generationConfig.thinkingConfig.thinkingLevel",
			},
		},
		{
			name:            "chat 渠道保持 reasoning_effort 形态",
			kind:            scheduler.ChannelKindChat,
			upstream:        &config.UpstreamConfig{ReasoningParamStyle: "reasoning_effort"},
			effort:          autopilot.EffortLow,
			body:            `{"model":"old","messages":[]}`,
			wantPaths:       map[string]string{"reasoning_effort": "low"},
			wantAbsentPaths: []string{"generationConfig.thinkingConfig.thinkingLevel"},
		},
		{
			name:            "responses 渠道保持 reasoning.effort 形态",
			kind:            scheduler.ChannelKindResponses,
			upstream:        &config.UpstreamConfig{},
			effort:          autopilot.EffortMedium,
			body:            `{"model":"old","input":[]}`,
			wantPaths:       map[string]string{"reasoning.effort": "medium"},
			wantAbsentPaths: []string{"generationConfig.thinkingConfig.thinkingLevel"},
		},
		{
			name:      "images 渠道不注入任何思考参数",
			kind:      scheduler.ChannelKindImages,
			upstream:  &config.UpstreamConfig{},
			effort:    autopilot.EffortHigh,
			body:      `{"model":"old","prompt":"cat"}`,
			wantPaths: map[string]string{"model": "gemini-3.5-flash"},
			wantAbsentPaths: []string{
				"reasoning", "reasoning_effort", "thinking",
				"generationConfig.thinkingConfig.thinkingLevel",
			},
		},
		{
			name:      "vectors 渠道不注入任何思考参数",
			kind:      scheduler.ChannelKindVectors,
			upstream:  &config.UpstreamConfig{},
			effort:    autopilot.EffortHigh,
			body:      `{"model":"old","input":"hello"}`,
			wantPaths: map[string]string{"model": "gemini-3.5-flash"},
			wantAbsentPaths: []string{
				"reasoning", "reasoning_effort", "thinking",
				"generationConfig.thinkingConfig.thinkingLevel",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := &autopilot.ResolvedRouteTarget{
				Model:         "gemini-3.5-flash",
				Effort:        tt.effort,
				EffortDecided: true,
			}
			got, ok := atomicModelEffortRewrite([]byte(tt.body), target, tt.upstream, tt.kind)
			if !ok {
				t.Fatal("atomicModelEffortRewrite() ok = false, want true")
			}
			if model := gjson.GetBytes(got, "model").String(); model != "gemini-3.5-flash" {
				t.Errorf("model = %q, want gemini-3.5-flash", model)
			}
			for path, want := range tt.wantPaths {
				value := gjson.GetBytes(got, path)
				if !value.Exists() {
					t.Errorf("path %q missing, body=%s", path, got)
					continue
				}
				if value.String() != want {
					t.Errorf("path %q = %q, want %q", path, value.String(), want)
				}
			}
			for _, path := range tt.wantAbsentPaths {
				if gjson.GetBytes(got, path).Exists() {
					t.Errorf("path %q must be absent, body=%s", path, got)
				}
			}
		})
	}
}
