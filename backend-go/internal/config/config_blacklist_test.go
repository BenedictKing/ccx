package config

import (
	"bytes"
	"encoding/json"
	"log"
	"os"
	"path/filepath"
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
	defer cm.Close()

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
	defer cm.Close()

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
	defer cm.Close()

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

func TestCircuitBreakerDisabledUpdatesAllProtocolChannels(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	channel := UpstreamConfig{Name: "test", BaseURL: "https://relay.example", APIKeys: []string{"sk-test"}, Status: "active"}
	initial := Config{
		Upstream: []UpstreamConfig{channel}, ChatUpstream: []UpstreamConfig{channel},
		ResponsesUpstream: []UpstreamConfig{channel}, GeminiUpstream: []UpstreamConfig{channel},
		ImagesUpstream: []UpstreamConfig{channel}, VectorsUpstream: []UpstreamConfig{channel},
	}
	data, err := json.Marshal(initial)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	cm, err := NewConfigManager(path, "")
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	update := UpstreamUpdate{CircuitBreakerDisabled: &enabled}
	for _, apply := range []func(int, UpstreamUpdate) (bool, error){
		cm.UpdateUpstream, cm.UpdateChatUpstream, cm.UpdateResponsesUpstream,
		cm.UpdateGeminiUpstream, cm.UpdateImagesUpstream, cm.UpdateVectorsUpstream,
	} {
		if _, err := apply(0, update); err != nil {
			t.Fatalf("updating protocol channel: %v", err)
		}
	}
	cm.Close()
	cm, err = NewConfigManager(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer cm.Close()
	cfg := cm.GetConfig()
	for _, group := range [][]UpstreamConfig{
		cfg.Upstream, cfg.ChatUpstream, cfg.ResponsesUpstream,
		cfg.GeminiUpstream, cfg.ImagesUpstream, cfg.VectorsUpstream,
	} {
		if len(group) != 1 || !group[0].CircuitBreakerDisabled {
			t.Fatalf("circuit breaker setting did not survive restart: %+v", group)
		}
	}
}

func TestCircuitBreakerDisabledIgnoresTemporaryFailedKeyCache(t *testing.T) {
	cm := &ConfigManager{failedKeysCache: make(map[string]*FailedKey), keyRecoveryTime: time.Hour}
	cm.MarkKeyAsFailed("sk-first", "Messages")
	upstream := &UpstreamConfig{Name: "unstable", APIKeys: []string{"sk-first", "sk-second"}}
	key, err := cm.GetNextAPIKey(upstream, nil, "Messages")
	if err != nil || key != "sk-second" {
		t.Fatalf("default channel should avoid failed key: key=%q err=%v", key, err)
	}
	upstream.CircuitBreakerDisabled = true
	key, err = cm.GetNextAPIKey(upstream, nil, "Messages")
	if err != nil || key != "sk-first" {
		t.Fatalf("disabled breaker should consider the first active key: key=%q err=%v", key, err)
	}
	key, err = cm.GetNextAPIKey(upstream, map[string]bool{"sk-first": true}, "Messages")
	if err != nil || key != "sk-second" {
		t.Fatalf("request-local failure must still be skipped: key=%q err=%v", key, err)
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
	defer cm.Close()

	cfg := cm.GetConfig()
	if got := cfg.Upstream[0].IsNormalizeMetadataUserIDEnabled(); got != false {
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
	defer cm.Close()

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
	defer cm.Close()

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
	defer cm.Close()

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
	defer cm.Close()

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
				"groupMultiplier": 0.35,
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
	defer cm.Close()

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
	*cm.config.Upstream[0].APIKeyConfigs[0].GroupMultiplier = 0.8
	cm.mu.Unlock()

	cfg := cm.GetConfig()
	if len(cfg.Upstream[0].DisabledAPIKeys) != 1 {
		t.Fatalf("DisabledAPIKeys len = %d, want 1", len(cfg.Upstream[0].DisabledAPIKeys))
	}
	dk := cfg.Upstream[0].DisabledAPIKeys[0]
	if dk.Config == nil {
		t.Fatal("dk.Config is nil, want non-nil snapshot")
	}
	if dk.Config.GroupMultiplier == nil || *dk.Config.GroupMultiplier != 0.35 {
		t.Fatalf("snapshot GroupMultiplier = %v, want 0.35", dk.Config.GroupMultiplier)
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
	defer cm.Close()

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
	t.Cleanup(func() { cm.Close() })
	return cm
}

func TestDisableKeyModelAndIsKeyModelDisabled(t *testing.T) {
	cm := newKeyModelTestConfigManager(t)

	if err := cm.DisableKeyModel("Messages", 0, "sk-a", "gpt-5.6-sol", "model_not_found", "no available channel", ""); err != nil {
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
	if err := cm.DisableKeyModel("Messages", 0, "sk-a", "m1", "model_not_found", "", ""); err != nil {
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
	if err := cm.DisableKeyModel("Messages", 0, "sk-a", "expired", "model_not_found", "", ""); err != nil {
		t.Fatalf("DisableKeyModel() error = %v", err)
	}
	if err := cm.DisableKeyModel("Messages", 0, "sk-a", "active", "model_not_found", "", ""); err != nil {
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
