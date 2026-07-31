package config

import (
	"path/filepath"
	"testing"
	"time"
)

func TestAllAdministrativeUpdatePathsRecordManualSuspension(t *testing.T) {
	status := "SUSPENDED"
	promotion := time.Now().Add(time.Hour)
	tests := []struct {
		name   string
		seed   func(*ConfigManager)
		update func(*ConfigManager, UpstreamUpdate) error
		get    func(*ConfigManager) UpstreamConfig
	}{
		{"messages", func(cm *ConfigManager) { cm.config.Upstream = []UpstreamConfig{{Name: "m"}} }, func(cm *ConfigManager, update UpstreamUpdate) error {
			_, err := cm.UpdateUpstream(0, update)
			return err
		}, func(cm *ConfigManager) UpstreamConfig { return cm.config.Upstream[0] }},
		{"responses", func(cm *ConfigManager) { cm.config.ResponsesUpstream = []UpstreamConfig{{Name: "r"}} }, func(cm *ConfigManager, update UpstreamUpdate) error {
			_, err := cm.UpdateResponsesUpstream(0, update)
			return err
		}, func(cm *ConfigManager) UpstreamConfig { return cm.config.ResponsesUpstream[0] }},
		{"chat", func(cm *ConfigManager) { cm.config.ChatUpstream = []UpstreamConfig{{Name: "c"}} }, func(cm *ConfigManager, update UpstreamUpdate) error {
			_, err := cm.UpdateChatUpstream(0, update)
			return err
		}, func(cm *ConfigManager) UpstreamConfig { return cm.config.ChatUpstream[0] }},
		{"gemini", func(cm *ConfigManager) { cm.config.GeminiUpstream = []UpstreamConfig{{Name: "g"}} }, func(cm *ConfigManager, update UpstreamUpdate) error {
			_, err := cm.UpdateGeminiUpstream(0, update)
			return err
		}, func(cm *ConfigManager) UpstreamConfig { return cm.config.GeminiUpstream[0] }},
		{"images", func(cm *ConfigManager) {
			cm.config.ImagesUpstream = []UpstreamConfig{{Name: "i", ServiceType: "openai"}}
		}, func(cm *ConfigManager, update UpstreamUpdate) error {
			_, err := cm.UpdateImagesUpstream(0, update)
			return err
		}, func(cm *ConfigManager) UpstreamConfig { return cm.config.ImagesUpstream[0] }},
		{"vectors", func(cm *ConfigManager) {
			cm.config.VectorsUpstream = []UpstreamConfig{{Name: "v", ServiceType: "openai"}}
		}, func(cm *ConfigManager, update UpstreamUpdate) error {
			_, err := cm.UpdateVectorsUpstream(0, update)
			return err
		}, func(cm *ConfigManager) UpstreamConfig { return cm.config.VectorsUpstream[0] }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			cm := &ConfigManager{configFile: filepath.Join(dir, "config.json"), backupDir: dir}
			tt.seed(cm)
			if err := tt.update(cm, UpstreamUpdate{Status: &status, PromotionUntil: &promotion}); err != nil {
				t.Fatalf("更新状态失败: %v", err)
			}
			got := tt.get(cm)
			if got.Status != "suspended" || got.SuspensionSource != SuspensionSourceManual || got.PromotionUntil != nil {
				t.Fatalf("状态迁移结果 = status:%q source:%q promotion:%v", got.Status, got.SuspensionSource, got.PromotionUntil)
			}
		})
	}
}
