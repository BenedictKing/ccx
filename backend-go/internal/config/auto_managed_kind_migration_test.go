package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/BenedictKing/ccx/internal/errutil"
)

func TestAutoManagedKindMigration(t *testing.T) {
	tests := []struct {
		name        string
		channelJSON string
		wantManaged bool
		wantKind    string
	}{
		{
			name:        "baseUrl 与 apiKeys 升级为 generic",
			channelJSON: `{"baseUrl":"https://relay.example.com","apiKeys":["sk-old"],"name":"legacy"}`,
			wantManaged: true,
			wantKind:    "generic",
		},
		{
			name:        "apiKeyConfigs 中的凭证也可触发升级",
			channelJSON: `{"baseUrl":"https://relay.example.com","apiKeyConfigs":[{"key":"sk-config"}],"name":"legacy-config"}`,
			wantManaged: true,
			wantKind:    "generic",
		},
		{
			name:        "disabledApiKeys 中的凭证同样触发升级",
			channelJSON: `{"baseUrl":"https://relay.example.com","disabledApiKeys":[{"key":"sk-disabled"}],"name":"legacy-disabled"}`,
			wantManaged: true,
			wantKind:    "generic",
		},
		{
			name:        "已有 provider 不参与 generic 迁移",
			channelJSON: `{"baseUrl":"https://api.example.com","apiKeys":["sk-provider"],"providerId":"deepseek","name":"provider"}`,
			wantManaged: false,
		},
		{
			name:        "缺少 key 不升级",
			channelJSON: `{"baseUrl":"https://relay.example.com","name":"no-key"}`,
			wantManaged: false,
		},
		{
			name:        "旧版 relay 托管渠道回填 new_api",
			channelJSON: `{"baseUrl":"https://newapi.example.com","apiKeys":["sk-managed"],"autoManaged":true,"originType":"relay","name":"new-api"}`,
			wantManaged: true,
			wantKind:    "new_api",
		},
		{
			name:        "显式 autoManaged=false 不升级为托管",
			channelJSON: `{"baseUrl":"https://relay.example.com","apiKeys":["sk-old"],"autoManaged":false,"name":"manual"}`,
			wantManaged: false,
			wantKind:    "",
		},
		{
			name:        "已有显式 kind 保持不变",
			channelJSON: `{"baseUrl":"https://relay.example.com","apiKeys":["sk-managed"],"autoManaged":true,"autoManagedKind":"custom","name":"explicit"}`,
			wantManaged: true,
			wantKind:    "custom",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			cfgFile := filepath.Join(dir, "config.json")
			initial := `{"upstream":[` + tt.channelJSON + `],"responsesUpstream":[],"geminiUpstream":[],"chatUpstream":[],"imagesUpstream":[],"vectorsUpstream":[]}`
			if err := os.WriteFile(cfgFile, []byte(initial), 0600); err != nil {
				t.Fatalf("写入初始配置失败: %v", err)
			}

			cm, err := NewConfigManager(cfgFile, "")
			if err != nil {
				t.Fatalf("NewConfigManager 失败: %v", err)
			}
			defer errutil.IgnoreDeferred(cm.Close)

			got := cm.GetConfig().Upstream[0]
			if got.AutoManaged != tt.wantManaged || got.AutoManagedKind != tt.wantKind {
				t.Fatalf("迁移结果 autoManaged=%v kind=%q，期望 %v/%q", got.AutoManaged, got.AutoManagedKind, tt.wantManaged, tt.wantKind)
			}
			if tt.wantKind == "generic" && got.AutoManagedAt == nil {
				t.Fatal("generic 自动升级后应记录 autoManagedAt")
			}

			persisted, err := os.ReadFile(cfgFile)
			if err != nil {
				t.Fatalf("读取迁移后配置失败: %v", err)
			}
			var cfg Config
			if err := json.Unmarshal(persisted, &cfg); err != nil {
				t.Fatalf("解析迁移后配置失败: %v", err)
			}
			if cfg.Upstream[0].AutoManagedKind != tt.wantKind {
				t.Fatalf("迁移结果未持久化: kind=%q", cfg.Upstream[0].AutoManagedKind)
			}
		})
	}
}
