package config

import (
	"bytes"
	"github.com/BenedictKing/ccx/internal/errutil"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
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

func TestGetAdminAPIKeyFallsBackToDisabledKey(t *testing.T) {
	cm := &ConfigManager{}
	upstream := &UpstreamConfig{
		Name:    "test-channel",
		APIKeys: nil,
		DisabledAPIKeys: []DisabledKeyInfo{{
			Key: "sk-disabled",
		}},
	}

	got, fallback, err := cm.GetAdminAPIKey(upstream, nil, "Messages")
	if err != nil {
		t.Fatalf("GetAdminAPIKey() error = %v", err)
	}
	if !fallback {
		t.Fatal("fallback = false, want false")
	}
	if got != "sk-disabled" {
		t.Fatalf("apiKey = %q, want sk-disabled", got)
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

func TestGetNextAPIKeySkipsDisabledManagedCredential(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	recoverAt := time.Now().Add(time.Hour).Format(time.RFC3339)
	initialConfig := `{
		"managedAccounts": [{
			"accountUid": "acct-glm",
			"providerId": "glm",
			"name": "glm",
			"credentials": [{"credentialUid": "cred-glm", "apiKey": "glm-managed-key"}]
		}],
		"upstream": [{
			"accountUid": "acct-glm",
			"channelUid": "ch-glm-messages",
			"providerId": "glm",
			"name": "glm-claude",
			"autoManaged": true,
			"status": "active",
			"baseUrl": "https://glm.example/api/anthropic",
			"serviceType": "claude",
			"apiKeyConfigs": [{"credentialUid": "cred-glm", "baseUrl": "https://glm.example/api/anthropic"}],
			"disabledApiKeys": [{
				"key": "glm-managed-key",
				"reason": "insufficient_balance",
				"message": "disabled for test",
				"disabledAt": "2026-07-16T12:00:00Z",
				"recoverAt": "` + recoverAt + `"
			}]
		}]
	}`
	if err := os.WriteFile(configPath, []byte(initialConfig), 0600); err != nil {
		t.Fatalf("写入初始配置失败: %v", err)
	}

	cm, err := NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	defer errutil.IgnoreDeferred(cm.Close)

	cfg := cm.GetConfig()
	upstream := &cfg.Upstream[0]
	if len(upstream.APIKeys) != 1 || upstream.APIKeys[0] != "glm-managed-key" {
		t.Fatalf("自动托管凭据未注入测试渠道: %+v", upstream.APIKeys)
	}
	if _, err := cm.GetNextAPIKey(upstream, nil, "Messages"); err == nil {
		t.Fatal("GetNextAPIKey() 选中了 DisabledAPIKeys 中的自动托管凭据")
	}
}

func TestBlacklistKeyRefreshesExistingRecordWithoutDuplicate(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	initialConfig := `{
		"upstream": [{
			"name": "test-channel",
			"baseUrl": "https://example.com",
			"apiKeys": ["sk-active"],
			"serviceType": "claude",
			"disabledApiKeys": [{
				"key": "sk-active",
				"reason": "insufficient_balance",
				"message": "old message",
				"disabledAt": "2026-07-16T12:00:00Z",
				"recoverAt": "2026-07-16T13:00:00Z"
			}]
		}]
	}`
	if err := os.WriteFile(configPath, []byte(initialConfig), 0600); err != nil {
		t.Fatalf("写入初始配置失败: %v", err)
	}

	cm, err := NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	defer errutil.IgnoreDeferred(cm.Close)

	if err := cm.BlacklistKey("Messages", 0, "sk-active", "insufficient_balance", "new message"); err != nil {
		t.Fatalf("BlacklistKey() error = %v", err)
	}

	upstream := cm.GetConfig().Upstream[0]
	if len(upstream.DisabledAPIKeys) != 1 {
		t.Fatalf("len(DisabledAPIKeys) = %d, want 1", len(upstream.DisabledAPIKeys))
	}
	if got := upstream.DisabledAPIKeys[0].Message; got != "new message" {
		t.Fatalf("禁用记录未刷新，message = %q", got)
	}
	if slices.Contains(upstream.APIKeys, "sk-active") {
		t.Fatal("重复拉黑后 Key 仍在活跃列表")
	}
}

func TestBlacklistKeyWithRecoverAtPrefersFutureUpstreamReset(t *testing.T) {
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
	if err := os.WriteFile(configPath, []byte(initialConfig), 0600); err != nil {
		t.Fatalf("写入初始配置失败: %v", err)
	}

	cm, err := NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	defer errutil.IgnoreDeferred(cm.Close)

	wantRecoverAt := time.Now().Add(6 * time.Hour).Truncate(time.Second).Format(time.RFC3339)
	if err := cm.BlacklistKeyWithRecoverAt("Messages", 0, "sk-active", "insufficient_quota", "monthly quota exhausted", wantRecoverAt); err != nil {
		t.Fatalf("BlacklistKeyWithRecoverAt() error = %v", err)
	}

	disabled := cm.GetConfig().Upstream[0].DisabledAPIKeys
	if len(disabled) != 1 {
		t.Fatalf("len(DisabledAPIKeys) = %d, want 1", len(disabled))
	}
	if got := disabled[0].RecoverAt; got != wantRecoverAt {
		t.Fatalf("RecoverAt = %q, want %q", got, wantRecoverAt)
	}
}

func TestMigrateDisabledKeyRecoveryTimesUsesPersistedQuotaMessage(t *testing.T) {
	now := time.Date(2026, 7, 17, 18, 0, 0, 0, time.FixedZone("CST", 8*60*60))
	cm := &ConfigManager{config: Config{
		Upstream: []UpstreamConfig{{
			Name: "volcengine-claude",
			DisabledAPIKeys: []DisabledKeyInfo{{
				Key:        "ark-test",
				Reason:     "insufficient_balance",
				Message:    "You have exceeded the monthly usage quota. It will reset at 2026-07-17 23:59:59 +0800 CST.",
				DisabledAt: now.Add(-time.Hour).Format(time.RFC3339),
				RecoverAt:  now.Add(time.Hour).Format(time.RFC3339),
			}},
		}},
	}}

	if !cm.migrateDisabledKeyRecoveryTimes(now) {
		t.Fatal("migrateDisabledKeyRecoveryTimes() = false, want true")
	}
	if got, want := cm.config.Upstream[0].DisabledAPIKeys[0].RecoverAt, "2026-07-17T23:59:59+08:00"; got != want {
		t.Fatalf("RecoverAt = %q, want %q", got, want)
	}
	if got := cm.config.Upstream[0].DisabledAPIKeys[0].Reason; got != "insufficient_quota" {
		t.Fatalf("Reason = %q, want insufficient_quota", got)
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

	cm, err := NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	defer errutil.IgnoreDeferred(cm.Close)

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

func TestMarkKeyAsFailedCoolingWindowAndRecoveryLog(t *testing.T) {
	cm := &ConfigManager{
		failedKeysCache: make(map[string]*FailedKey),
		keyRecoveryTime: 50 * time.Millisecond,
		maxFailureCount: 2,
	}

	cm.MarkKeyAsFailed("sk-test", "Messages")
	if !cm.IsKeyFailed("sk-test", "Messages") {
		t.Fatal("IsKeyFailed() = false, want false immediately after failure")
	}

	cacheKey := failedKeyCacheKey("Messages", "sk-test")
	cm.mu.Lock()
	cm.failedKeysCache[cacheKey].Timestamp = time.Now().Add(-100 * time.Millisecond)
	cm.mu.Unlock()

	if cm.IsKeyFailed("sk-test", "Messages") {
		t.Fatal("IsKeyFailed() = true, want false after recovery window elapsed")
	}

	var buf bytes.Buffer
	origWriter := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(origWriter)

	cm.mu.Lock()
	cm.failedKeysCache[cacheKey] = &FailedKey{Timestamp: time.Now().Add(-100 * time.Millisecond), FailureCount: 1}
	cm.mu.Unlock()

	cm.mu.Lock()
	now := time.Now()
	for key, failure := range cm.failedKeysCache {
		recoveryTime := cm.keyRecoveryTime
		if failure.FailureCount > cm.maxFailureCount {
			recoveryTime = cm.keyRecoveryTime * 2
		}
		if now.Sub(failure.Timestamp) > recoveryTime {
			delete(cm.failedKeysCache, key)
			log.Printf("[Config-Key] API密钥 %s 已从失败列表中恢复", key)
		}
	}
	cm.mu.Unlock()

	if _, exists := cm.failedKeysCache[cacheKey]; exists {
		t.Fatal("failed key cache entry still exists after simulated cleanup")
	}
	if !strings.Contains(buf.String(), "已从失败列表中恢复") {
		t.Fatalf("expected recovery log, got %q", buf.String())
	}
}

func TestBlacklistAndRestoreLogsIncludeTransitionFields(t *testing.T) {
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

	cm, err := NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	defer errutil.IgnoreDeferred(cm.Close)

	var buf bytes.Buffer
	origWriter := log.Writer()
	log.SetOutput(&buf)
	defer log.SetOutput(origWriter)

	if err := cm.BlacklistKey("Messages", 0, "sk-active", "insufficient_balance", "no balance"); err != nil {
		t.Fatalf("BlacklistKey() error = %v", err)
	}
	if err := cm.RestoreKey("Messages", 0, "sk-active"); err != nil {
		t.Fatalf("RestoreKey() error = %v", err)
	}

	output := buf.String()
	if !strings.Contains(output, "from=active") || !strings.Contains(output, "to=disabled") || !strings.Contains(output, "cause=insufficient_balance") {
		t.Fatalf("blacklist transition fields missing: %q", output)
	}
	if !strings.Contains(output, "from=disabled") || !strings.Contains(output, "to=active") || !strings.Contains(output, "cause=manual_restore") {
		t.Fatalf("restore transition fields missing: %q", output)
	}
}

func TestBlacklistRestorePreservesKeyMetadata(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	initialConfig := `{
		"upstream": [{
			"name": "test-channel",
			"baseUrl": "https://example.com",
			"apiKeys": ["sk-a", "sk-b"],
			"serviceType": "claude",
			"apiKeyConfigs": [
				{"key": "sk-a", "name": "primary", "quotaGroup": "account-1", "rateLimitRpm": 20, "rateLimitMaxConcurrent": 2, "weight": 3},
				{"key": "sk-b", "name": "backup", "quotaGroup": "account-1", "rateLimitRpm": 10}
			]
		}]
	}`
	if err := os.WriteFile(configPath, []byte(initialConfig), 0644); err != nil {
		t.Fatalf("写入初始配置失败: %v", err)
	}

	cm, err := NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	cm.CloseWatcher()
	defer errutil.IgnoreDeferred(cm.Close)

	// 拉黑 sk-a
	if err := cm.BlacklistKey("Messages", 0, "sk-a", "authentication_error", "auth failed"); err != nil {
		t.Fatalf("BlacklistKey() error = %v", err)
	}

	cfg := cm.GetConfig()
	up := cfg.Upstream[0]

	// sk-a 应在 DisabledAPIKeys 中，且 Config 快照完整
	if len(up.DisabledAPIKeys) != 1 {
		t.Fatalf("len(DisabledAPIKeys) = %d, want 1", len(up.DisabledAPIKeys))
	}
	dk := up.DisabledAPIKeys[0]
	if dk.Config == nil {
		t.Fatal("DisabledKeyInfo.Config is nil")
	}
	if dk.Config.Name != "primary" {
		t.Fatalf("Config.Name = %q, want primary", dk.Config.Name)
	}
	if dk.Config.QuotaGroup != "account-1" {
		t.Fatalf("Config.QuotaGroup = %q, want account-1", dk.Config.QuotaGroup)
	}
	if dk.Config.RateLimitRPM != 20 {
		t.Fatalf("Config.RateLimitRPM = %d, want 20", dk.Config.RateLimitRPM)
	}
	if dk.Config.RateLimitMaxConcurrent != 2 {
		t.Fatalf("Config.RateLimitMaxConcurrent = %d, want 2", dk.Config.RateLimitMaxConcurrent)
	}
	if dk.Config.Weight != 3 {
		t.Fatalf("Config.Weight = %d, want 3", dk.Config.Weight)
	}

	// 恢复 sk-a
	if err := cm.RestoreKey("Messages", 0, "sk-a"); err != nil {
		t.Fatalf("RestoreKey() error = %v", err)
	}

	cfg = cm.GetConfig()
	up = cfg.Upstream[0]
	if len(up.DisabledAPIKeys) != 0 {
		t.Fatalf("len(DisabledAPIKeys) = %d after restore, want 0", len(up.DisabledAPIKeys))
	}

	// 恢复后 apiKeyConfigs 中 sk-a 的 metadata 应完整
	var restoredCfg *APIKeyConfig
	for _, c := range up.APIKeyConfigs {
		if c.Key == "sk-a" {
			copy := c
			restoredCfg = &copy
			break
		}
	}
	if restoredCfg == nil {
		t.Fatal("sk-a not found in APIKeyConfigs after restore")
	}
	if restoredCfg.Name != "primary" {
		t.Fatalf("restored Name = %q, want primary", restoredCfg.Name)
	}
	if restoredCfg.QuotaGroup != "account-1" {
		t.Fatalf("restored QuotaGroup = %q, want account-1", restoredCfg.QuotaGroup)
	}
	if restoredCfg.RateLimitRPM != 20 {
		t.Fatalf("restored RateLimitRPM = %d, want 20", restoredCfg.RateLimitRPM)
	}
	if restoredCfg.Weight != 3 {
		t.Fatalf("restored Weight = %d, want 3", restoredCfg.Weight)
	}
}

func TestValidateChannelKeysSuspendsChatChannelWithoutKeys(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	initialConfig := `{
		"chatUpstream": [{
			"name": "chat-channel",
			"baseUrl": "https://example.com",
			"apiKeys": [],
			"status": "active",
			"serviceType": "openai"
		}]
	}`
	if err := os.WriteFile(configPath, []byte(initialConfig), 0644); err != nil {
		t.Fatalf("写入初始配置失败: %v", err)
	}

	cm, err := NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	defer errutil.IgnoreDeferred(cm.Close)

	cfg := cm.GetConfig()
	if len(cfg.ChatUpstream) != 1 {
		t.Fatalf("len(ChatUpstream) = %d, want 1", len(cfg.ChatUpstream))
	}
	if got := cfg.ChatUpstream[0].Status; got != "suspended" {
		t.Fatalf("Chat status = %s, want suspended", got)
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

	cm, err := NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	defer errutil.IgnoreDeferred(cm.Close)

	disabled := false
	if _, err := cm.UpdateUpstream(0, UpstreamUpdate{AutoBlacklistBalance: &disabled}); err != nil {
		t.Fatalf("UpdateUpstream() error = %v", err)
	}

	cfg := cm.GetConfig()
	if cfg.Upstream[0].AutoBlacklistBalance == nil || *cfg.Upstream[0].AutoBlacklistBalance != false {
		t.Fatalf("AutoBlacklistBalance = %v, want false", cfg.Upstream[0].AutoBlacklistBalance)
	}
}

func TestNormalizeMetadataUserIDDefaultsAndUpdate(t *testing.T) {
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

	cm, err := NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	defer errutil.IgnoreDeferred(cm.Close)

	cfg := cm.GetConfig()
	if got := cfg.Upstream[0].IsNormalizeMetadataUserIDEnabled(); got != true {
		t.Fatalf("default IsNormalizeMetadataUserIDEnabled() = %v, want false", got)
	}

	disabled := false
	if _, err := cm.UpdateUpstream(0, UpstreamUpdate{NormalizeMetadataUserID: &disabled}); err != nil {
		t.Fatalf("UpdateUpstream() error = %v", err)
	}

	cfg = cm.GetConfig()
	if cfg.Upstream[0].NormalizeMetadataUserID == nil || *cfg.Upstream[0].NormalizeMetadataUserID != false {
		t.Fatalf("NormalizeMetadataUserID = %v, want false", cfg.Upstream[0].NormalizeMetadataUserID)
	}
	if got := cfg.Upstream[0].IsNormalizeMetadataUserIDEnabled(); got != false {
		t.Fatalf("IsNormalizeMetadataUserIDEnabled() = %v, want false", got)
	}

	cloned := cfg.Upstream[0].Clone()
	if cloned.NormalizeMetadataUserID == nil || *cloned.NormalizeMetadataUserID != false {
		t.Fatalf("cloned NormalizeMetadataUserID = %v, want false", cloned.NormalizeMetadataUserID)
	}
	if cloned.NormalizeMetadataUserID == cfg.Upstream[0].NormalizeMetadataUserID {
		t.Fatal("NormalizeMetadataUserID pointer should be deep-copied")
	}
}

func TestCodexToolCompatDefaultsAndUpdate(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	initialConfig := `{
		"responsesUpstream": [{
			"name": "test-channel",
			"baseUrl": "https://example.com",
			"apiKeys": ["sk-active"],
			"serviceType": "openai"
		}]
	}`
	if err := os.WriteFile(configPath, []byte(initialConfig), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cm, err := NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	defer errutil.IgnoreDeferred(cm.Close)

	cfg := cm.GetConfig()
	if got := cfg.ResponsesUpstream[0].IsCodexToolCompatEnabled(); got != false {
		t.Fatalf("default IsCodexToolCompatEnabled() = %v, want false", got)
	}

	disabled := false
	if _, err := cm.UpdateResponsesUpstream(0, UpstreamUpdate{CodexToolCompat: &disabled}); err != nil {
		t.Fatalf("UpdateResponsesUpstream() error = %v", err)
	}

	cfg = cm.GetConfig()
	if cfg.ResponsesUpstream[0].CodexToolCompat == nil || *cfg.ResponsesUpstream[0].CodexToolCompat != false {
		t.Fatalf("CodexToolCompat = %v, want false", cfg.ResponsesUpstream[0].CodexToolCompat)
	}
	if got := cfg.ResponsesUpstream[0].IsCodexToolCompatEnabled(); got != false {
		t.Fatalf("IsCodexToolCompatEnabled() = %v, want false", got)
	}

	cloned := cfg.ResponsesUpstream[0].Clone()
	if cloned.CodexToolCompat == nil || *cloned.CodexToolCompat != false {
		t.Fatalf("cloned CodexToolCompat = %v, want false", cloned.CodexToolCompat)
	}
	if cloned.CodexToolCompat == cfg.ResponsesUpstream[0].CodexToolCompat {
		t.Fatal("CodexToolCompat pointer should be deep-copied")
	}
}

func TestStripImageGenerationToolDefaultsAndUpdate(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	initialConfig := `{
		"responsesUpstream": [{
			"name": "test-channel",
			"baseUrl": "https://example.com",
			"apiKeys": ["sk-active"],
			"serviceType": "openai"
		}]
	}`
	if err := os.WriteFile(configPath, []byte(initialConfig), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cm, err := NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	defer errutil.IgnoreDeferred(cm.Close)

	cfg := cm.GetConfig()
	if got := cfg.ResponsesUpstream[0].IsStripImageGenerationToolEnabled(); got != false {
		t.Fatalf("default IsStripImageGenerationToolEnabled() = %v, want false", got)
	}

	enabled := true
	if _, err := cm.UpdateResponsesUpstream(0, UpstreamUpdate{StripImageGenerationTool: &enabled}); err != nil {
		t.Fatalf("UpdateResponsesUpstream() error = %v", err)
	}

	cfg = cm.GetConfig()
	if got := cfg.ResponsesUpstream[0].IsStripImageGenerationToolEnabled(); got != true {
		t.Fatalf("IsStripImageGenerationToolEnabled() = %v, want true", got)
	}

	cloned := cfg.ResponsesUpstream[0].Clone()
	if !cloned.StripImageGenerationTool {
		t.Fatalf("cloned StripImageGenerationTool = %v, want true", cloned.StripImageGenerationTool)
	}

	disabled := false
	if _, err := cm.UpdateResponsesUpstream(0, UpstreamUpdate{StripImageGenerationTool: &disabled}); err != nil {
		t.Fatalf("UpdateResponsesUpstream() error = %v", err)
	}
	cfg = cm.GetConfig()
	if got := cfg.ResponsesUpstream[0].IsStripImageGenerationToolEnabled(); got != false {
		t.Fatalf("after disable IsStripImageGenerationToolEnabled() = %v, want false", got)
	}
}

func TestConvertImageURLToB64JSONDefaultsAndImagesUpdate(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	initialConfig := `{
		"imagesUpstream": [{
			"name": "images-channel",
			"baseUrl": "https://example.com",
			"apiKeys": ["sk-active"],
			"serviceType": "openai"
		}]
	}`
	if err := os.WriteFile(configPath, []byte(initialConfig), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cm, err := NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	defer errutil.IgnoreDeferred(cm.Close)

	cfg := cm.GetConfig()
	if cfg.ImagesUpstream[0].ConvertImageURLToB64JSON {
		t.Fatal("default ConvertImageURLToB64JSON = true, want false")
	}

	enabled := true
	if _, err := cm.UpdateImagesUpstream(0, UpstreamUpdate{ConvertImageURLToB64JSON: &enabled}); err != nil {
		t.Fatalf("UpdateImagesUpstream() error = %v", err)
	}

	cfg = cm.GetConfig()
	if !cfg.ImagesUpstream[0].ConvertImageURLToB64JSON {
		t.Fatal("ConvertImageURLToB64JSON = false, want true")
	}

	cloned := cfg.ImagesUpstream[0].Clone()
	if !cloned.ConvertImageURLToB64JSON {
		t.Fatal("cloned ConvertImageURLToB64JSON = false, want true")
	}

	disabled := false
	if _, err := cm.UpdateImagesUpstream(0, UpstreamUpdate{ConvertImageURLToB64JSON: &disabled}); err != nil {
		t.Fatalf("UpdateImagesUpstream() disable error = %v", err)
	}
	cfg = cm.GetConfig()
	if cfg.ImagesUpstream[0].ConvertImageURLToB64JSON {
		t.Fatal("after disable ConvertImageURLToB64JSON = true, want false")
	}
}

func TestStripBillingHeaderDefaultsUpdateAndMigration(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	initialConfig := `{
		"stripBillingHeader": true,
		"upstream": [{
			"name": "msg-ch",
			"baseUrl": "https://example.com",
			"apiKeys": ["sk-active"],
			"serviceType": "claude"
		}]
	}`
	if err := os.WriteFile(configPath, []byte(initialConfig), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cm, err := NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	defer errutil.IgnoreDeferred(cm.Close)

	cfg := cm.GetConfig()
	if cfg.Upstream[0].StripBillingHeader == nil || *cfg.Upstream[0].StripBillingHeader != true {
		t.Fatalf("StripBillingHeader migration = %v, want true", cfg.Upstream[0].StripBillingHeader)
	}
	if got := cfg.Upstream[0].IsStripBillingHeaderEnabled(); got != true {
		t.Fatalf("default IsStripBillingHeaderEnabled() = %v, want true", got)
	}

	disabled := false
	if _, err := cm.UpdateUpstream(0, UpstreamUpdate{StripBillingHeader: &disabled}); err != nil {
		t.Fatalf("UpdateUpstream() error = %v", err)
	}

	cfg = cm.GetConfig()
	if cfg.Upstream[0].StripBillingHeader == nil || *cfg.Upstream[0].StripBillingHeader != false {
		t.Fatalf("StripBillingHeader = %v, want false", cfg.Upstream[0].StripBillingHeader)
	}
	if got := cfg.Upstream[0].IsStripBillingHeaderEnabled(); got != false {
		t.Fatalf("IsStripBillingHeaderEnabled() = %v, want false", got)
	}

	cloned := cfg.Upstream[0].Clone()
	if cloned.StripBillingHeader == nil || *cloned.StripBillingHeader != false {
		t.Fatalf("cloned StripBillingHeader = %v, want false", cloned.StripBillingHeader)
	}
	if cloned.StripBillingHeader == cfg.Upstream[0].StripBillingHeader {
		t.Fatal("StripBillingHeader pointer should be deep-copied")
	}
}

func TestRequiresAnthropicBillingHeader(t *testing.T) {
	tests := []struct {
		name     string
		baseURL  string
		expected bool
	}{
		{name: "official host", baseURL: "api.anthropic.com", expected: true},
		{name: "official host with scheme", baseURL: "https://api.anthropic.com", expected: true},
		{name: "official host with path", baseURL: "http://api.anthropic.com/v1", expected: true},
		{name: "official host with trailing slash", baseURL: "https://api.anthropic.com/", expected: true},
		{name: "official host with explicit port", baseURL: "https://api.anthropic.com:443/v1", expected: true},
		{name: "official host with port no path", baseURL: "https://api.anthropic.com:8443", expected: true},
		{name: "subdomain is not official", baseURL: "https://proxy.api.anthropic.com", expected: false},
		{name: "subdomain with port is not official", baseURL: "https://proxy.api.anthropic.com:443", expected: false},
		{name: "third-party domain", baseURL: "https://api.deepseek.com", expected: false},
		{name: "empty string", baseURL: "", expected: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := requiresAnthropicBillingHeader(tt.baseURL); got != tt.expected {
				t.Errorf("requiresAnthropicBillingHeader(%q) = %v, want %v", tt.baseURL, got, tt.expected)
			}
		})
	}
}

func TestShouldStripBillingHeaderByDefault(t *testing.T) {
	tests := []struct {
		name     string
		upstream *UpstreamConfig
		expected bool
	}{
		{
			name:     "nil upstream",
			upstream: nil,
			expected: true,
		},
		{
			name:     "all urls official",
			upstream: &UpstreamConfig{BaseURL: "https://api.anthropic.com", BaseURLs: []string{"https://api.anthropic.com/v1"}},
			expected: false,
		},
		{
			name:     "primary official, secondary third-party",
			upstream: &UpstreamConfig{BaseURL: "https://api.anthropic.com", BaseURLs: []string{"https://api.deepseek.com/anthropic"}},
			expected: true,
		},
		{
			name:     "primary third-party",
			upstream: &UpstreamConfig{BaseURL: "https://api.deepseek.com/anthropic"},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldStripBillingHeaderByDefault(tt.upstream); got != tt.expected {
				t.Errorf("shouldStripBillingHeaderByDefault(%+v) = %v, want %v", tt.upstream, got, tt.expected)
			}
		})
	}
}

func TestIsStripBillingHeaderEnabledDefaultByDomain(t *testing.T) {
	trueVal := true
	falseVal := false

	official := &UpstreamConfig{BaseURL: "https://api.anthropic.com"}
	thirdParty := &UpstreamConfig{BaseURL: "https://api.deepseek.com/anthropic"}

	if got := official.IsStripBillingHeaderEnabled(); got != false {
		t.Errorf("official endpoint default IsStripBillingHeaderEnabled() = %v, want false", got)
	}
	if got := thirdParty.IsStripBillingHeaderEnabled(); got != true {
		t.Errorf("third-party endpoint default IsStripBillingHeaderEnabled() = %v, want true", got)
	}

	thirdParty.StripBillingHeader = &falseVal
	if got := thirdParty.IsStripBillingHeaderEnabled(); got != false {
		t.Errorf("explicit false should override domain default, got %v", got)
	}

	official.StripBillingHeader = &trueVal
	if got := official.IsStripBillingHeaderEnabled(); got != true {
		t.Errorf("explicit true should override domain default, got %v", got)
	}
}

// TestBlacklistKeyConfigSnapshotDeepCopy 验证 BlacklistKey 深拷贝 APIKeyConfig 快照，
// 拉黑后修改源 config 不影响快照。
func TestBlacklistKeyConfigSnapshotDeepCopy(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	initialConfig := `{
		"upstream": [{
			"name": "test-channel",
			"baseUrl": "https://example.com",
			"apiKeys": ["sk-1"],
			"apiKeyConfigs": [{
				"key": "sk-1",
				"name": "test-key",
				"enabled": true,
				"rateLimitAutoFromHeaders": true,
				"weight": 5,
				"models": ["claude-sonnet"]
			}],
			"serviceType": "claude"
		}]
	}`
	if err := os.WriteFile(configPath, []byte(initialConfig), 0644); err != nil {
		t.Fatalf("写入初始配置失败: %v", err)
	}
	cm, err := NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	defer errutil.IgnoreDeferred(cm.Close)

	if err := cm.BlacklistKey("Messages", 0, "sk-1", "authentication_error", "test"); err != nil {
		t.Fatalf("BlacklistKey() error = %v", err)
	}

	cm.mu.Lock()
	falseVal := false
	cm.config.Upstream[0].APIKeyConfigs[0].Name = "mutated"
	cm.config.Upstream[0].APIKeyConfigs[0].Weight = 9
	cm.config.Upstream[0].APIKeyConfigs[0].Models[0] = "mutated-model"
	cm.config.Upstream[0].APIKeyConfigs[0].Enabled = &falseVal
	cm.config.Upstream[0].APIKeyConfigs[0].RateLimitAutoFromHeaders = &falseVal
	cm.mu.Unlock()

	cfg := cm.GetConfig()
	if len(cfg.Upstream[0].DisabledAPIKeys) != 1 {
		t.Fatalf("DisabledAPIKeys len = %d, want 1", len(cfg.Upstream[0].DisabledAPIKeys))
	}
	dk := cfg.Upstream[0].DisabledAPIKeys[0]
	if dk.Config == nil {
		t.Fatal("dk.Config is nil, want non-nil snapshot")
	}
	if dk.Config.Name != "test-key" {
		t.Fatalf("snapshot Name = %q, want test-key", dk.Config.Name)
	}
	if dk.Config.Enabled == nil || !*dk.Config.Enabled {
		t.Fatalf("snapshot Enabled = %v, want true", dk.Config.Enabled)
	}
	if dk.Config.RateLimitAutoFromHeaders == nil || !*dk.Config.RateLimitAutoFromHeaders {
		t.Fatalf("snapshot RateLimitAutoFromHeaders = %v, want true", dk.Config.RateLimitAutoFromHeaders)
	}
	if dk.Config.Weight != 5 {
		t.Errorf("snapshot Weight = %d, want 5", dk.Config.Weight)
	}
	snapshotModels := dk.Config.Models
	if len(snapshotModels) != 1 || snapshotModels[0] != "claude-sonnet" {
		t.Errorf("snapshot Models mutated to %v, want [claude-sonnet]", snapshotModels)
	}
}

// TestRestoreDisabledKeysRestoresConfig 验证 RestoreDisabledKeys（自动恢复路径）
// 将 DisabledKeyInfo.Config 中的快照恢复到 APIKeyConfigs，quota/weight 不丢失。
func TestRestoreDisabledKeysRestoresConfig(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	// 初始：key 活跃且有完整配置
	initialConfig := `{
		"upstream": [{
			"name": "test-channel",
			"baseUrl": "https://example.com",
			"apiKeys": ["sk-1"],
			"apiKeyConfigs": [{
				"key": "sk-1",
				"name": "primary-key",
				"quotaGroup": "group-a",
				"weight": 3,
				"rateLimitRpm": 100
			}],
			"serviceType": "claude"
		}]
	}`
	if err := os.WriteFile(configPath, []byte(initialConfig), 0644); err != nil {
		t.Fatalf("写入初始配置失败: %v", err)
	}
	cm, err := NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	defer errutil.IgnoreDeferred(cm.Close)

	// 拉黑（带自动恢复时间）
	if err := cm.BlacklistKey("Messages", 0, "sk-1", "authentication_error", "test"); err != nil {
		t.Fatalf("BlacklistKey() error = %v", err)
	}

	// 确认配置快照存在
	cfg := cm.GetConfig()
	if len(cfg.Upstream[0].DisabledAPIKeys) != 1 {
		t.Fatalf("DisabledAPIKeys len = %d, want 1", len(cfg.Upstream[0].DisabledAPIKeys))
	}
	dk := cfg.Upstream[0].DisabledAPIKeys[0]
	if dk.Config == nil {
		t.Fatal("dk.Config is nil after BlacklistKey")
	}
	if dk.Config.QuotaGroup != "group-a" {
		t.Fatalf("snapshot QuotaGroup = %q, want group-a", dk.Config.QuotaGroup)
	}

	// 模拟用户编辑渠道（触发 config 保存，移除该 key 的 APIKeyConfig 条目）
	cm.mu.Lock()
	cm.config.Upstream[0].APIKeyConfigs = []APIKeyConfig{
		{Key: "sk-new", Name: "new-key"},
	}
	cm.mu.Unlock()

	// 自动恢复
	restored, err := cm.RestoreDisabledKeys("Messages", 0, []string{"sk-1"})
	if err != nil {
		t.Fatalf("RestoreDisabledKeys() error = %v", err)
	}
	if len(restored) != 1 || restored[0] != "sk-1" {
		t.Fatalf("restored = %v, want [sk-1]", restored)
	}

	// 验证恢复后 key 配置保留了快照中的字段
	cfg = cm.GetConfig()
	var restoredConfig *APIKeyConfig
	for _, cfgEntry := range cfg.Upstream[0].APIKeyConfigs {
		if cfgEntry.Key == "sk-1" {
			cfgCopy := cfgEntry
			restoredConfig = &cfgCopy
			break
		}
	}
	if restoredConfig == nil {
		t.Fatal("restored config not found in APIKeyConfigs")
	}
	if restoredConfig.QuotaGroup != "group-a" {
		t.Errorf("restored QuotaGroup = %q, want group-a", restoredConfig.QuotaGroup)
	}
	if restoredConfig.Weight != 3 {
		t.Errorf("restored Weight = %d, want 3", restoredConfig.Weight)
	}
	if restoredConfig.RateLimitRPM != 100 {
		t.Errorf("restored RateLimitRPM = %d, want 100", restoredConfig.RateLimitRPM)
	}
	if restoredConfig.Name != "primary-key" {
		t.Errorf("restored Name = %q, want primary-key", restoredConfig.Name)
	}
}

func newKeyModelTestConfigManager(t *testing.T) *ConfigManager {
	t.Helper()
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	initialConfig := `{
		"upstream": [{
			"name": "test-channel",
			"baseUrl": "https://example.com",
			"apiKeys": ["sk-a", "sk-b"],
			"serviceType": "claude"
		}]
	}`
	if err := os.WriteFile(configPath, []byte(initialConfig), 0644); err != nil {
		t.Fatalf("写入初始配置失败: %v", err)
	}
	cm, err := NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	t.Cleanup(func() { _ = cm.Close() })
	return cm
}

func TestDisableKeyModelAndIsKeyModelDisabled(t *testing.T) {
	cm := newKeyModelTestConfigManager(t)

	if err := cm.DisableKeyModel("Messages", 0, "sk-a", "gpt-5.6-sol", "model_not_found", "no available channel"); err != nil {
		t.Fatalf("DisableKeyModel() error = %v", err)
	}

	// 命中组合应受限
	if !cm.IsKeyModelDisabled("Messages", 0, "sk-a", "gpt-5.6-sol") {
		t.Fatal("(sk-a, gpt-5.6-sol) 应处于限制期内")
	}
	// 大小写不敏感
	if !cm.IsKeyModelDisabled("Messages", 0, "sk-a", "GPT-5.6-SOL") {
		t.Fatal("模型比较应大小写不敏感")
	}
	// 同 Key 其他模型不受影响
	if cm.IsKeyModelDisabled("Messages", 0, "sk-a", "gpt-4o") {
		t.Fatal("同 Key 其他模型不应受限")
	}
	// 其他 Key 同模型不受影响
	if cm.IsKeyModelDisabled("Messages", 0, "sk-b", "gpt-5.6-sol") {
		t.Fatal("其他 Key 同模型不应受限")
	}
}

func TestDisableKeyModelDoesNotRewriteActiveRestriction(t *testing.T) {
	cm := newKeyModelTestConfigManager(t)
	if err := cm.DisableKeyModel("Messages", 0, "sk-a", "m1", "model_not_found", "first"); err != nil {
		t.Fatalf("first DisableKeyModel() error = %v", err)
	}
	before := cm.GetConfig().Upstream[0].DisabledKeyModels[0]

	if err := cm.DisableKeyModel("Messages", 0, "sk-a", "m1", "different_reason", "second"); err != nil {
		t.Fatalf("second DisableKeyModel() error = %v", err)
	}
	after := cm.GetConfig().Upstream[0].DisabledKeyModels[0]
	if after != before {
		t.Fatalf("生效中的重复限制不应被刷新: before=%+v after=%+v", before, after)
	}
}

func TestIsKeyModelDisabledNowRespectsRecoverAt(t *testing.T) {
	now := time.Now()
	upstream := &UpstreamConfig{
		Name: "c",
		DisabledKeyModels: []DisabledKeyModelInfo{
			{Key: "sk-a", Model: "m1", RecoverAt: now.Add(30 * time.Minute).Format(time.RFC3339)},
			{Key: "sk-a", Model: "m2", RecoverAt: now.Add(-time.Minute).Format(time.RFC3339)},
		},
	}
	if !upstream.IsKeyModelDisabledNow("sk-a", "m1", now) {
		t.Fatal("未到期组合应受限")
	}
	if upstream.IsKeyModelDisabledNow("sk-a", "m2", now) {
		t.Fatal("已到期组合不应受限")
	}
}

func TestRestoreKeyModel(t *testing.T) {
	cm := newKeyModelTestConfigManager(t)
	if err := cm.DisableKeyModel("Messages", 0, "sk-a", "m1", "model_not_found", ""); err != nil {
		t.Fatalf("DisableKeyModel() error = %v", err)
	}
	if err := cm.RestoreKeyModel("Messages", 0, "sk-a", "m1"); err != nil {
		t.Fatalf("RestoreKeyModel() error = %v", err)
	}
	if cm.IsKeyModelDisabled("Messages", 0, "sk-a", "m1") {
		t.Fatal("手动恢复后组合不应再受限")
	}
	if err := cm.RestoreKeyModel("Messages", 0, "sk-a", "m1"); err == nil {
		t.Fatal("恢复不存在的组合应返回错误")
	}
}

func TestRestoreExpiredKeyModels(t *testing.T) {
	cm := newKeyModelTestConfigManager(t)
	if err := cm.DisableKeyModel("Messages", 0, "sk-a", "expired", "model_not_found", ""); err != nil {
		t.Fatalf("DisableKeyModel() error = %v", err)
	}
	if err := cm.DisableKeyModel("Messages", 0, "sk-a", "active", "model_not_found", ""); err != nil {
		t.Fatalf("DisableKeyModel() error = %v", err)
	}

	// 手动把其中一个改为已到期
	cm.mu.Lock()
	cm.config.Upstream[0].DisabledKeyModels[0].RecoverAt = time.Now().Add(-time.Hour).Format(time.RFC3339)
	cm.mu.Unlock()

	restored, err := cm.RestoreExpiredKeyModels("Messages", 0, time.Now())
	if err != nil {
		t.Fatalf("RestoreExpiredKeyModels() error = %v", err)
	}
	if len(restored) != 1 {
		t.Fatalf("恢复数量 = %d, want 1", len(restored))
	}
	if cm.IsKeyModelDisabled("Messages", 0, "sk-a", "expired") {
		t.Fatal("到期组合应被清理")
	}
	if !cm.IsKeyModelDisabled("Messages", 0, "sk-a", "active") {
		t.Fatal("未到期组合应保留")
	}
}
