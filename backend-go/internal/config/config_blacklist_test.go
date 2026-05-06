package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestGetAdminAPIKeyPrefersActiveKey(t *testing.T) {
	cm := &ConfigManager{}
	upstream := &UpstreamConfig{
		Name:    "test-channel",
		APIKeys: []string{"sk-active"},
		DisabledAPIKeys: []DisabledKeyInfo{{
			Key: "sk-disabled",
		}},
	}

	got, fallback, err := cm.GetAdminAPIKey(upstream, nil, "Messages")
	if err != nil {
		t.Fatalf("GetAdminAPIKey() error = %v", err)
	}
	if fallback {
		t.Fatal("fallback = true, want false")
	}
	if got != "sk-active" {
		t.Fatalf("apiKey = %q, want sk-active", got)
	}
}

func TestGetAdminAPIKeyDoesNotFallbackToDisabledKey(t *testing.T) {
	cm := &ConfigManager{}
	upstream := &UpstreamConfig{
		Name:    "test-channel",
		APIKeys: nil,
		DisabledAPIKeys: []DisabledKeyInfo{{
			Key: "sk-disabled",
		}},
	}

	got, fallback, err := cm.GetAdminAPIKey(upstream, nil, "Messages")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if fallback {
		t.Fatal("fallback = true, want false")
	}
	if got != "" {
		t.Fatalf("apiKey = %q, want empty", got)
	}
}

func TestGetAdminAPIKeySkipsDisabledKeyEvenIfStillActive(t *testing.T) {
	cm := &ConfigManager{}
	upstream := &UpstreamConfig{
		Name:    "test-channel",
		APIKeys: []string{"sk-disabled", "sk-active"},
		DisabledAPIKeys: []DisabledKeyInfo{{
			Key: "sk-disabled",
		}},
	}

	got, fallback, err := cm.GetAdminAPIKey(upstream, nil, "Messages")
	if err != nil {
		t.Fatalf("GetAdminAPIKey() error = %v", err)
	}
	if fallback {
		t.Fatal("fallback = true, want false")
	}
	if got != "sk-active" {
		t.Fatalf("apiKey = %q, want sk-active", got)
	}
}

func TestGetAdminAPIKeyReturnsErrorWhenNoKeysAvailable(t *testing.T) {
	cm := &ConfigManager{}
	upstream := &UpstreamConfig{Name: "test-channel"}

	_, _, err := cm.GetAdminAPIKey(upstream, nil, "Messages")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestValidateAdminProbeKeyRejectsDisabledAndCooldownKeys(t *testing.T) {
	cm := newConfigManagerForTestJSON(t, `{
		"upstream": [{
			"name": "test-channel",
			"baseUrl": "https://example.com",
			"apiKeys": ["sk-cooldown", "sk-active"],
			"disabledApiKeys": [{"key": "sk-disabled"}],
			"serviceType": "claude"
		}]
	}`)
	cm.MarkKeyAsFailedWithDuration("sk-cooldown", "Messages", time.Hour)

	if err := cm.ValidateAdminProbeKey("Messages", 0, "sk-disabled"); err == nil {
		t.Fatal("disabled key should be rejected")
	}
	if err := cm.ValidateAdminProbeKey("Messages", 0, "sk-cooldown"); err == nil {
		t.Fatal("cooldown key should be rejected")
	}
	if err := cm.ValidateAdminProbeKey("Messages", 0, "sk-active"); err != nil {
		t.Fatalf("active key should be accepted: %v", err)
	}
	if err := cm.ValidateAdminProbeKey("Messages", 0, "sk-new"); err != nil {
		t.Fatalf("unknown temporary key should be accepted: %v", err)
	}
}

func TestGetUsableAPIKeyForChannelSkipsDisabledAndCooldownKeys(t *testing.T) {
	cm := newConfigManagerForTestJSON(t, `{
		"upstream": [{
			"name": "test-channel",
			"baseUrl": "https://example.com",
			"apiKeys": ["sk-disabled", "sk-cooldown", "sk-active"],
			"disabledApiKeys": [{"key": "sk-disabled"}],
			"serviceType": "claude"
		}]
	}`)
	cm.MarkKeyAsFailedWithDuration("sk-cooldown", "Messages", time.Hour)

	got, err := cm.GetUsableAPIKeyForChannel("Messages", 0)
	if err != nil {
		t.Fatalf("GetUsableAPIKeyForChannel() error = %v", err)
	}
	if got != "sk-active" {
		t.Fatalf("apiKey = %q, want sk-active", got)
	}
}

func TestGetNextAPIKeySkipsSingleCooldownKey(t *testing.T) {
	cm := &ConfigManager{
		failedKeysCache: map[string]*FailedKey{
			failedKeyCacheKey("Messages", "sk-only"): {
				Timestamp:    time.Now(),
				FailureCount: 1,
			},
		},
		keyBackoffDurations: []time.Duration{time.Minute},
	}
	upstream := &UpstreamConfig{
		Name:    "test-channel",
		APIKeys: []string{"sk-only"},
	}

	got, err := cm.GetNextAPIKey(upstream, nil, "Messages")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got != "" {
		t.Fatalf("apiKey = %q, want empty", got)
	}
}

func TestGetNextAPIKeyDoesNotReuseOldestCooldownKey(t *testing.T) {
	now := time.Now()
	cm := &ConfigManager{
		failedKeysCache: map[string]*FailedKey{
			failedKeyCacheKey("Messages", "sk-old"): {
				Timestamp:    now.Add(-30 * time.Second),
				FailureCount: 1,
			},
			failedKeyCacheKey("Messages", "sk-new"): {
				Timestamp:    now,
				FailureCount: 1,
			},
		},
		keyBackoffDurations: []time.Duration{time.Minute},
	}
	upstream := &UpstreamConfig{
		Name:    "test-channel",
		APIKeys: []string{"sk-old", "sk-new"},
	}

	got, err := cm.GetNextAPIKey(upstream, nil, "Messages")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got != "" {
		t.Fatalf("apiKey = %q, want empty", got)
	}
}

func TestAddAPIKeyRemovesDisabledEntryAndRestoreAvoidsDuplicate(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	initialConfig := `{
		"upstream": [{
			"name": "test-channel",
			"baseUrl": "https://example.com",
			"apiKeys": ["sk-active"],
			"disabledApiKeys": [{
				"key": "sk-disabled",
				"reason": "authentication_error",
				"message": "invalid key",
				"disabledAt": "2026-04-04T00:00:00Z"
			}],
			"serviceType": "claude"
		}]
	}`
	if err := os.WriteFile(configPath, []byte(initialConfig), 0644); err != nil {
		t.Fatalf("写入初始配置失败: %v", err)
	}

	cm, err := NewConfigManager(configPath)
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	defer cm.Close()

	if err := cm.AddAPIKey(0, "sk-disabled"); err != nil {
		t.Fatalf("AddAPIKey() error = %v", err)
	}

	cfg := cm.GetConfig()
	if len(cfg.Upstream[0].DisabledAPIKeys) != 0 {
		t.Fatalf("DisabledAPIKeys = %+v, want empty after AddAPIKey", cfg.Upstream[0].DisabledAPIKeys)
	}

	cm.mu.Lock()
	cm.config.Upstream[0].DisabledAPIKeys = append(cm.config.Upstream[0].DisabledAPIKeys, DisabledKeyInfo{
		Key:        "sk-disabled",
		Reason:     "authentication_error",
		Message:    "invalid key",
		DisabledAt: "2026-04-04T00:00:00Z",
	})
	cm.mu.Unlock()

	if err := cm.RestoreKey("Messages", 0, "sk-disabled"); err != nil {
		t.Fatalf("RestoreKey() error = %v", err)
	}

	finalCfg := cm.GetConfig()
	count := 0
	for _, key := range finalCfg.Upstream[0].APIKeys {
		if key == "sk-disabled" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("restored key count = %d, want 1; keys=%v", count, finalCfg.Upstream[0].APIKeys)
	}
}

func TestUpdateUpstreamCanSetAutoBlacklistBalance(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	initialConfig := `{
		"upstream": [{
			"name": "test-channel",
			"baseUrl": "https://example.com",
			"apiKeys": ["sk-active"],
			"serviceType": "claude"
		}]
	}`
	if err := os.WriteFile(configPath, []byte(initialConfig), 0644); err != nil {
		t.Fatalf("写入初始配置失败: %v", err)
	}

	cm, err := NewConfigManager(configPath)
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	defer cm.Close()

	disabled := false
	if _, err := cm.UpdateUpstream(0, UpstreamUpdate{AutoBlacklistBalance: &disabled}); err != nil {
		t.Fatalf("UpdateUpstream() error = %v", err)
	}

	cfg := cm.GetConfig()
	if cfg.Upstream[0].AutoBlacklistBalance == nil || *cfg.Upstream[0].AutoBlacklistBalance != false {
		t.Fatalf("AutoBlacklistBalance = %v, want false", cfg.Upstream[0].AutoBlacklistBalance)
	}
}
