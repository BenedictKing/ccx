package config

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestRemoveAPIKeyDeletesBlacklistedEntry(t *testing.T) {
	tests := []struct {
		name       string
		apiType    string
		remove     func(*ConfigManager, int, string) error
		getChannel func(*Config) UpstreamConfig
	}{
		{name: "Messages", apiType: "Messages", remove: (*ConfigManager).RemoveAPIKey, getChannel: func(c *Config) UpstreamConfig { return c.Upstream[0] }},
		{name: "Responses", apiType: "Responses", remove: (*ConfigManager).RemoveResponsesAPIKey, getChannel: func(c *Config) UpstreamConfig { return c.ResponsesUpstream[0] }},
		{name: "Gemini", apiType: "Gemini", remove: (*ConfigManager).RemoveGeminiAPIKey, getChannel: func(c *Config) UpstreamConfig { return c.GeminiUpstream[0] }},
		{name: "Chat", apiType: "Chat", remove: (*ConfigManager).RemoveChatAPIKey, getChannel: func(c *Config) UpstreamConfig { return c.ChatUpstream[0] }},
		{name: "Images", apiType: "Images", remove: (*ConfigManager).RemoveImagesAPIKey, getChannel: func(c *Config) UpstreamConfig { return c.ImagesUpstream[0] }},
		{name: "Vectors", apiType: "Vectors", remove: (*ConfigManager).RemoveVectorsAPIKey, getChannel: func(c *Config) UpstreamConfig { return c.VectorsUpstream[0] }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm := &ConfigManager{
				configFile:      filepath.Join(t.TempDir(), "config.json"),
				failedKeysCache: make(map[string]*FailedKey),
			}
			upstreams := cm.getUpstreamSliceLocked(tt.apiType)
			*upstreams = []UpstreamConfig{{
				DisabledAPIKeys:   []DisabledKeyInfo{{Key: "sk-disabled"}},
				DisabledKeyModels: []DisabledKeyModelInfo{{Key: "sk-disabled", Model: "model-a"}},
				DisabledGroupModels: []DisabledGroupModelInfo{
					{Key: "sk-disabled", Model: "model-a"},
					{QuotaGroup: "vip", Model: "model-a"},
				},
			}}
			cm.failedKeysCache[failedKeyCacheKey(tt.apiType, "sk-disabled")] = &FailedKey{}

			if err := tt.remove(cm, 0, "sk-disabled"); err != nil {
				t.Fatalf("删除拉黑 Key 失败: %v", err)
			}
			got := tt.getChannel(&cm.config)
			if len(got.DisabledAPIKeys) != 0 || len(got.DisabledKeyModels) != 0 {
				t.Fatalf("拉黑及模型限制未清理: %+v", got)
			}
			if len(got.DisabledGroupModels) != 1 || got.DisabledGroupModels[0].QuotaGroup != "vip" {
				t.Fatalf("单 Key 策略未清理或整组策略被误删: %+v", got.DisabledGroupModels)
			}
			if !slices.Contains(got.HistoricalAPIKeys, "sk-disabled") {
				t.Fatal("删除后应保留历史统计 Key")
			}
			if _, exists := cm.failedKeysCache[failedKeyCacheKey(tt.apiType, "sk-disabled")]; exists {
				t.Fatal("删除后仍残留失败冷却记录")
			}
		})
	}
}
