package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/errutil"
)

// newBalanceEscalationManager 构造带单渠道的 ConfigManager，渠道持有两把 Key。
func newBalanceEscalationManager(t *testing.T, supportedModels []string) *ConfigManager {
	t.Helper()
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	cfg := `{
		"upstream": [{
			"name": "balance-channel",
			"baseUrl": "https://example.com",
			"apiKeys": ["sk-a", "sk-b"],
			"serviceType": "claude"
		}]
	}`
	if err := os.WriteFile(configPath, []byte(cfg), 0644); err != nil {
		t.Fatalf("写入初始配置失败: %v", err)
	}
	cm, err := NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	t.Cleanup(func() { errutil.IgnoreDeferred(cm.Close) })
	if len(supportedModels) > 0 {
		cm.mu.Lock()
		cm.config.Upstream[0].SupportedModels = supportedModels
		cm.mu.Unlock()
	}
	return cm
}

func TestShouldEscalateBalanceKeyBlacklist(t *testing.T) {
	tests := []struct {
		name            string
		supportedModels []string
		restricted      []string // 写入组合限制的模型列表（sk-a）
		want            bool
	}{
		{
			name:       "无限制时不升级",
			restricted: nil,
			want:       false,
		},
		{
			name:       "空白名单两个模型不足阈值",
			restricted: []string{"m1", "m2"},
			want:       false,
		},
		{
			name:       "空白名单三个不同模型达到阈值",
			restricted: []string{"m1", "m2", "m3"},
			want:       true,
		},
		{
			name:       "同模型重复限制只计一次",
			restricted: []string{"m1", "M1", "m2"},
			want:       false,
		},
		{
			name:            "精确白名单全覆盖即升级",
			supportedModels: []string{"m1", "m2"},
			restricted:      []string{"m1", "m2"},
			want:            true,
		},
		{
			name:            "精确白名单未覆盖不升级",
			supportedModels: []string{"m1", "m2", "m3"},
			restricted:      []string{"m1", "m2"},
			want:            false,
		},
		{
			name:            "通配白名单回落阈值规则",
			supportedModels: []string{"m*"},
			restricted:      []string{"m1", "m2"},
			want:            false,
		},
		{
			name:            "排除条目使白名单不可精确判定",
			supportedModels: []string{"m1", "!m2"},
			restricted:      []string{"m1"},
			want:            false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm := newBalanceEscalationManager(t, tt.supportedModels)
			for _, model := range tt.restricted {
				if err := cm.DisableKeyModel("Messages", 0, "sk-a", model, "insufficient_balance", "402"); err != nil {
					t.Fatalf("DisableKeyModel(%s) error = %v", model, err)
				}
			}
			if got := cm.ShouldEscalateBalanceKeyBlacklist("Messages", 0, "sk-a"); got != tt.want {
				t.Fatalf("ShouldEscalateBalanceKeyBlacklist() = %v, want %v", got, tt.want)
			}
			// 其他 Key 的限制不参与判定
			if err := cm.DisableKeyModel("Messages", 0, "sk-b", "m1", "insufficient_balance", "402"); err != nil {
				t.Fatalf("DisableKeyModel(sk-b) error = %v", err)
			}
			if got := cm.ShouldEscalateBalanceKeyBlacklist("Messages", 0, "sk-a"); got != tt.want {
				t.Fatalf("混入其他 Key 的限制后判定漂移: got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestShouldEscalateBalanceKeyBlacklistIgnoresExpired(t *testing.T) {
	cm := newBalanceEscalationManager(t, nil)
	now := time.Now()
	cm.mu.Lock()
	cm.config.Upstream[0].DisabledKeyModels = []DisabledKeyModelInfo{
		{Key: "sk-a", Model: "m1", Reason: "insufficient_balance", RecoverAt: now.Add(-time.Minute).Format(time.RFC3339)},
		{Key: "sk-a", Model: "m2", Reason: "insufficient_balance", RecoverAt: now.Add(-time.Minute).Format(time.RFC3339)},
		{Key: "sk-a", Model: "m3", Reason: "insufficient_balance", RecoverAt: now.Add(-time.Minute).Format(time.RFC3339)},
	}
	cm.mu.Unlock()

	if cm.ShouldEscalateBalanceKeyBlacklist("Messages", 0, "sk-a") {
		t.Fatal("过期限制不应计入升级判定")
	}
}
