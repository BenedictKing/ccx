package config

import (
	"path/filepath"
	"testing"
	"time"
)

func TestAllChannelUpdatePathsRecordManualSuspension(t *testing.T) {
	type updateCase struct {
		name   string
		seed   func(*ConfigManager, UpstreamConfig)
		update func(*ConfigManager, UpstreamUpdate) error
		get    func(*ConfigManager) *UpstreamConfig
	}
	cases := []updateCase{
		{"messages", func(cm *ConfigManager, c UpstreamConfig) { cm.config.Upstream = []UpstreamConfig{c} }, func(cm *ConfigManager, u UpstreamUpdate) error { _, err := cm.UpdateUpstream(0, u); return err }, func(cm *ConfigManager) *UpstreamConfig { return &cm.config.Upstream[0] }},
		{"responses", func(cm *ConfigManager, c UpstreamConfig) { cm.config.ResponsesUpstream = []UpstreamConfig{c} }, func(cm *ConfigManager, u UpstreamUpdate) error {
			_, err := cm.UpdateResponsesUpstream(0, u)
			return err
		}, func(cm *ConfigManager) *UpstreamConfig { return &cm.config.ResponsesUpstream[0] }},
		{"chat", func(cm *ConfigManager, c UpstreamConfig) { cm.config.ChatUpstream = []UpstreamConfig{c} }, func(cm *ConfigManager, u UpstreamUpdate) error { _, err := cm.UpdateChatUpstream(0, u); return err }, func(cm *ConfigManager) *UpstreamConfig { return &cm.config.ChatUpstream[0] }},
		{"gemini", func(cm *ConfigManager, c UpstreamConfig) { cm.config.GeminiUpstream = []UpstreamConfig{c} }, func(cm *ConfigManager, u UpstreamUpdate) error { _, err := cm.UpdateGeminiUpstream(0, u); return err }, func(cm *ConfigManager) *UpstreamConfig { return &cm.config.GeminiUpstream[0] }},
		{"images", func(cm *ConfigManager, c UpstreamConfig) { cm.config.ImagesUpstream = []UpstreamConfig{c} }, func(cm *ConfigManager, u UpstreamUpdate) error { _, err := cm.UpdateImagesUpstream(0, u); return err }, func(cm *ConfigManager) *UpstreamConfig { return &cm.config.ImagesUpstream[0] }},
		{"vectors", func(cm *ConfigManager, c UpstreamConfig) { cm.config.VectorsUpstream = []UpstreamConfig{c} }, func(cm *ConfigManager, u UpstreamUpdate) error { _, err := cm.UpdateVectorsUpstream(0, u); return err }, func(cm *ConfigManager) *UpstreamConfig { return &cm.config.VectorsUpstream[0] }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			promotion := time.Now().Add(time.Hour)
			cm := &ConfigManager{configFile: filepath.Join(dir, "config.json"), backupDir: dir}
			tc.seed(cm, UpstreamConfig{Name: tc.name, Status: "active", APIKeys: []string{"key"}, PromotionUntil: &promotion})
			status := "suspended"
			if err := tc.update(cm, UpstreamUpdate{Status: &status}); err != nil {
				t.Fatal(err)
			}
			got := tc.get(cm)
			if got.Status != "suspended" || got.SuspensionSource != SuspensionSourceManual || got.PromotionUntil != nil {
				t.Fatalf("暂停结果 = status:%q source:%q promotion:%v", got.Status, got.SuspensionSource, got.PromotionUntil)
			}
			status = "active"
			if err := tc.update(cm, UpstreamUpdate{Status: &status}); err != nil {
				t.Fatal(err)
			}
			if got := tc.get(cm); got.SuspensionSource != "" {
				t.Fatalf("激活后暂停来源未清除: %q", got.SuspensionSource)
			}
		})
	}
}

func TestAllChannelUpdatePathsRecoverAutoNoKeysOnFirstKey(t *testing.T) {
	type updateCase struct {
		name   string
		seed   func(*ConfigManager, UpstreamConfig)
		update func(*ConfigManager, UpstreamUpdate) error
		get    func(*ConfigManager) *UpstreamConfig
	}
	cases := []updateCase{
		{"messages", func(cm *ConfigManager, c UpstreamConfig) { cm.config.Upstream = []UpstreamConfig{c} }, func(cm *ConfigManager, u UpstreamUpdate) error { _, err := cm.UpdateUpstream(0, u); return err }, func(cm *ConfigManager) *UpstreamConfig { return &cm.config.Upstream[0] }},
		{"responses", func(cm *ConfigManager, c UpstreamConfig) { cm.config.ResponsesUpstream = []UpstreamConfig{c} }, func(cm *ConfigManager, u UpstreamUpdate) error {
			_, err := cm.UpdateResponsesUpstream(0, u)
			return err
		}, func(cm *ConfigManager) *UpstreamConfig { return &cm.config.ResponsesUpstream[0] }},
		{"chat", func(cm *ConfigManager, c UpstreamConfig) { cm.config.ChatUpstream = []UpstreamConfig{c} }, func(cm *ConfigManager, u UpstreamUpdate) error { _, err := cm.UpdateChatUpstream(0, u); return err }, func(cm *ConfigManager) *UpstreamConfig { return &cm.config.ChatUpstream[0] }},
		{"gemini", func(cm *ConfigManager, c UpstreamConfig) { cm.config.GeminiUpstream = []UpstreamConfig{c} }, func(cm *ConfigManager, u UpstreamUpdate) error { _, err := cm.UpdateGeminiUpstream(0, u); return err }, func(cm *ConfigManager) *UpstreamConfig { return &cm.config.GeminiUpstream[0] }},
		{"images", func(cm *ConfigManager, c UpstreamConfig) { cm.config.ImagesUpstream = []UpstreamConfig{c} }, func(cm *ConfigManager, u UpstreamUpdate) error { _, err := cm.UpdateImagesUpstream(0, u); return err }, func(cm *ConfigManager) *UpstreamConfig { return &cm.config.ImagesUpstream[0] }},
		{"vectors", func(cm *ConfigManager, c UpstreamConfig) { cm.config.VectorsUpstream = []UpstreamConfig{c} }, func(cm *ConfigManager, u UpstreamUpdate) error { _, err := cm.UpdateVectorsUpstream(0, u); return err }, func(cm *ConfigManager) *UpstreamConfig { return &cm.config.VectorsUpstream[0] }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			cm := &ConfigManager{configFile: filepath.Join(dir, "config.json"), backupDir: dir}
			tc.seed(cm, UpstreamConfig{Name: tc.name, Status: "suspended", SuspensionSource: SuspensionSourceAutoNoKeys})
			keys := []string{"first-key"}
			if err := tc.update(cm, UpstreamUpdate{APIKeys: keys}); err != nil {
				t.Fatal(err)
			}
			got := tc.get(cm)
			if got.Status != "active" || got.SuspensionSource != "" || len(got.APIKeys) != 1 {
				t.Fatalf("0->N 自动恢复失败: status=%q source=%q keys=%v", got.Status, got.SuspensionSource, got.APIKeys)
			}
		})
	}
}
