package common

import (
	"net/http"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

// 真实上游报错样本：Codex 打到不支持 developer role 的 Chat 上游。
const developerRoleErrorBody = `{"error":{"message":"Failed to deserialize the JSON body into the target type: messages[0].role: unknown variant ` + "`developer`" + `, expected one of ` + "`system`" + `, ` + "`user`" + `, ` + "`assistant`" + `, ` + "`tool`" + `, ` + "`latest_reminder`" + ` at line 1 column 18015","type":"invalid_request_error","param":null,"code":"invalid_request_error"}}`

// Kimi Coding Chat 接口拒绝 developer role 时的真实报错样本。
const kimiDeveloperRoleErrorBody = `{"error":{"message":"Invalid request: role 'developer' is not allowed","type":"invalid_request_error"}}`

// tokenrhythm-studio 拒绝 1M context beta header 时的真实报错样本。
const betaHeaderRejectErrorBody = `{"type":"error","error":{"type":"invalid_request_error","message":"尚未验证或不支持的 anthropic-beta：context-1m-2025-08-07"},"request_id":"trace_xxx"}`

// 英文变体（上游可能给出更结构化文案）。
const betaHeaderRejectErrorBodyEn = `{"error":{"message":"anthropic-beta ` + "`" + `context-1m-2025-08-07` + "`" + ` is not enabled for this API key"}}`

func TestCompatTraitFromError(t *testing.T) {
	tests := []struct {
		name       string
		statusCode int
		body       string
		ctx        CompatSignalContext
		wantTrait  config.CompatTrait
		wantHit    bool
	}{
		{
			name:       "developer role 真实样本",
			statusCode: http.StatusBadRequest,
			body:       developerRoleErrorBody,
			ctx:        CompatSignalContext{HasDeveloperRole: true},
			wantTrait:  config.TraitDowngradeDeveloperRole,
			wantHit:    true,
		},
		{
			name:       "developer role 422 同样识别",
			statusCode: http.StatusUnprocessableEntity,
			body:       developerRoleErrorBody,
			ctx:        CompatSignalContext{HasDeveloperRole: true},
			wantTrait:  config.TraitDowngradeDeveloperRole,
			wantHit:    true,
		},
		{
			name:       "Kimi developer role not allowed 真实样本",
			statusCode: http.StatusBadRequest,
			body:       kimiDeveloperRoleErrorBody,
			ctx:        CompatSignalContext{HasDeveloperRole: true},
			wantTrait:  config.TraitDowngradeDeveloperRole,
			wantHit:    true,
		},
		{
			name:       "请求未带 developer role 时不学习",
			statusCode: http.StatusBadRequest,
			body:       kimiDeveloperRoleErrorBody,
			ctx:        CompatSignalContext{HasDeveloperRole: false},
			wantHit:    false,
		},
		{
			name:       "含 developer 字样但非 role 枚举错误",
			statusCode: http.StatusBadRequest,
			body:       `{"error":{"message":"your developer account has insufficient balance"}}`,
			ctx:        CompatSignalContext{HasDeveloperRole: true},
			wantHit:    false,
		},
		{
			name:       "Codex 工具报错且请求确实带 Codex 工具",
			statusCode: http.StatusBadRequest,
			body:       `{"error":{"message":"unknown tool: apply_patch"}}`,
			ctx:        CompatSignalContext{HasCodexClientTools: true},
			wantTrait:  config.TraitStripCodexClientTools,
			wantHit:    true,
		},
		{
			name:       "Codex 工具报错但请求未带 Codex 工具时不学习",
			statusCode: http.StatusBadRequest,
			body:       `{"error":{"message":"unknown tool: apply_patch"}}`,
			ctx:        CompatSignalContext{HasCodexClientTools: false},
			wantHit:    false,
		},
		{
			name:       "历史 thinking 块被拒",
			statusCode: http.StatusBadRequest,
			body:       `{"error":{"message":"messages: final assistant content cannot end with trailing whitespace; thinking block expected"}}`,
			ctx:        CompatSignalContext{HasHistoricalThinking: true},
			wantTrait:  config.TraitPassbackThinkingBlocks,
			wantHit:    true,
		},
		{
			name:       "非 JSON 错误体不崩溃且不学习",
			statusCode: http.StatusBadRequest,
			body:       `<html>502 Bad Gateway</html>`,
			ctx:        CompatSignalContext{HasDeveloperRole: true},
			wantHit:    false,
		},
		{
			name:       "429 不参与兼容性学习",
			statusCode: http.StatusTooManyRequests,
			body:       developerRoleErrorBody,
			ctx:        CompatSignalContext{HasDeveloperRole: true},
			wantHit:    false,
		},
		{
			name:       "anthropic-beta token 被拒绝（中文真实样本）",
			statusCode: http.StatusBadRequest,
			body:       betaHeaderRejectErrorBody,
			ctx:        CompatSignalContext{HasAnthropicBetaHeader: true},
			wantTrait:  config.TraitUnsupportedBetaHeader,
			wantHit:    true,
		},
		{
			name:       "anthropic-beta token 被拒绝（英文变体）",
			statusCode: http.StatusBadRequest,
			body:       betaHeaderRejectErrorBodyEn,
			ctx:        CompatSignalContext{HasAnthropicBetaHeader: true},
			wantTrait:  config.TraitUnsupportedBetaHeader,
			wantHit:    true,
		},
		{
			name:       "未带 anthropic-beta header 时不学习",
			statusCode: http.StatusBadRequest,
			body:       betaHeaderRejectErrorBody,
			ctx:        CompatSignalContext{HasAnthropicBetaHeader: false},
			wantHit:    false,
		},
		{
			name:       "协证词缺失时不学习（仅含 anthropic-beta 关键词）",
			statusCode: http.StatusBadRequest,
			body:       `{"error":{"message":"client should include anthropic-beta header for newer features"}}`,
			ctx:        CompatSignalContext{HasAnthropicBetaHeader: true},
			wantHit:    false,
		},
		{
			name:       "协证词命中但 Evidence 无 token 名时不学习",
			statusCode: http.StatusBadRequest,
			body:       `{"error":{"message":"unsupported anthropic-beta configuration"}}`,
			ctx:        CompatSignalContext{HasAnthropicBetaHeader: true},
			wantHit:    false,
		},
		{
			name:       "429 状态码不参与 anthropic-beta 学习",
			statusCode: http.StatusTooManyRequests,
			body:       betaHeaderRejectErrorBody,
			ctx:        CompatSignalContext{HasAnthropicBetaHeader: true},
			wantHit:    false,
		},
		{
			name:       "422 状态码参与 anthropic-beta 学习",
			statusCode: http.StatusUnprocessableEntity,
			body:       betaHeaderRejectErrorBody,
			ctx:        CompatSignalContext{HasAnthropicBetaHeader: true},
			wantTrait:  config.TraitUnsupportedBetaHeader,
			wantHit:    true,
		},
		{
			name:       "非 JSON 错误体不崩溃且不学习",
			statusCode: http.StatusBadRequest,
			body:       `<html>502 Bad Gateway</html>`,
			ctx:        CompatSignalContext{HasAnthropicBetaHeader: true},
			wantHit:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CompatTraitFromError(tt.statusCode, []byte(tt.body), tt.ctx)
			if tt.wantHit {
				if got == nil {
					t.Fatalf("期望命中 %s，实际未命中", tt.wantTrait)
				}
				if got.Trait != tt.wantTrait {
					t.Fatalf("trait = %s, want %s", got.Trait, tt.wantTrait)
				}
				if !got.Enabled {
					t.Error("错误驱动信号的结论应为启用兼容改写")
				}
				if got.Evidence == "" {
					t.Error("证据不应为空")
				}
				return
			}
			if got != nil {
				t.Fatalf("期望不命中，实际命中 %s (evidence=%q)", got.Trait, got.Evidence)
			}
		})
	}
}
