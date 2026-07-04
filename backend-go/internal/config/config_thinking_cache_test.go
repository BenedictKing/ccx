package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestThinkingCacheDefaultConfig(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	cm, err := NewConfigManager(configPath, filepath.Join(tempDir, "backups"))
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	defer cm.Close()

	cfg := cm.GetConfig()
	if got := cfg.ThinkingCache.EffectiveTTLHours(); got != ThinkingCacheDefaultTTLHours {
		t.Fatalf("thinking cache ttl = %d, want %d", got, ThinkingCacheDefaultTTLHours)
	}
	if cfg.ThinkingCache.TTLHours != ThinkingCacheDefaultTTLHours {
		t.Fatalf("persisted ttlHours = %d, want %d", cfg.ThinkingCache.TTLHours, ThinkingCacheDefaultTTLHours)
	}
}

func TestThinkingCacheTTLClampOnLoad(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	raw := []byte(`{
		"upstream": [],
		"responsesUpstream": [],
		"geminiUpstream": [],
		"fuzzyModeEnabled": true,
		"thinkingCache": {"ttlHours": 9999}
	}`)
	if err := os.WriteFile(configPath, raw, 0600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cm, err := NewConfigManager(configPath, filepath.Join(tempDir, "backups"))
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	defer cm.Close()

	cfg := cm.GetConfig()
	if got := cfg.ThinkingCache.TTLHours; got != ThinkingCacheMaxTTLHours {
		t.Fatalf("ttlHours = %d, want %d", got, ThinkingCacheMaxTTLHours)
	}
}
