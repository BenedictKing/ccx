package common

import (
	"net/http"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

// 真实上游报错样本：Codex 打到不支持 developer role 的 Chat 上游。
const developerRoleErrorBody = `{"error":{"message":"Failed to deserialize the JSON body into the target type: messages[0].role: unknown variant ` + "`developer`" + `, expected one of ` + "`system`" + `, ` + "`user`" + `, ` + "`assistant`" + `, ` + "`tool`" + `, ` + "`latest_reminder`" + ` at line 1 column 18015","type":"invalid_request_error","param":null,"code":"invalid_request_error"}}`

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
			name:       "请求未带 developer role 时不学习",
			statusCode: http.StatusBadRequest,
			body:       developerRoleErrorBody,
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
			name:       "500 不参与兼容性学习",
			statusCode: http.StatusInternalServerError,
			body:       developerRoleErrorBody,
			ctx:        CompatSignalContext{HasDeveloperRole: true},
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
