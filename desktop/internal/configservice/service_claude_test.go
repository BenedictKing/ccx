package configservice

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNew(t *testing.T) {
	svc := newTestService(t)
	if svc.homeDir == "" {
		t.Error("homeDir should not be empty")
	}
	if svc.stateDir == "" {
		t.Error("stateDir should not be empty")
	}
	if _, err := os.Stat(svc.stateDir); os.IsNotExist(err) {
		t.Error("stateDir should be created")
	}
}

func TestNewWithDefaultDataDir(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)
	if runtime.GOOS == "linux" {
		t.Setenv("XDG_STATE_HOME", "")
	}
	svc, err := New("")
	if err != nil {
		t.Fatalf("New('') failed: %v", err)
	}
	expected := filepath.Join(tmpHome, ".config", "ccx-desktop", "agent-config-state")
	if runtime.GOOS == "linux" {
		expected = filepath.Join(tmpHome, ".local", "state", "ccx", "agent-config-state")
	}
	if svc.stateDir != expected {
		t.Errorf("stateDir = %q, want %q", svc.stateDir, expected)
	}
}

func TestGetStatusClaude_NoConfig(t *testing.T) {
	svc := newTestService(t)
	status, err := svc.GetStatus(PlatformClaude, 3688)
	if err != nil {
		t.Fatalf("GetStatus failed: %v", err)
	}
	if status.Configured {
		t.Error("should not be configured when no settings.json exists")
	}
	// 空 base_url 时 detectClaudeProvider 返回 ""
	if status.Provider != "" {
		t.Errorf("provider = %q, want empty", status.Provider)
	}
}

func TestGetStatusClaude_CCXProvider(t *testing.T) {
	svc := newTestService(t)
	settingsPath := filepath.Join(svc.homeDir, ".claude", "settings.json")
	os.MkdirAll(filepath.Dir(settingsPath), 0o755)
	data := map[string]any{
		"env": map[string]any{
			"ANTHROPIC_BASE_URL": "http://127.0.0.1:3688",
		},
	}
	writeJSON(settingsPath, data)

	status, err := svc.GetStatus(PlatformClaude, 3688)
	if err != nil {
		t.Fatalf("GetStatus failed: %v", err)
	}
	if status.Provider != ProviderCCX {
		t.Errorf("provider = %q, want %q", status.Provider, ProviderCCX)
	}
	if !status.MatchesCurrentPort {
		t.Error("should match current port")
	}
	if status.NeedsUpdate {
		t.Error("should not need update when port matches")
	}
}

func TestGetStatusClaude_DeepSeekProvider(t *testing.T) {
	svc := newTestService(t)
	settingsPath := filepath.Join(svc.homeDir, ".claude", "settings.json")
	os.MkdirAll(filepath.Dir(settingsPath), 0o755)
	data := map[string]any{
		"env": map[string]any{
			"ANTHROPIC_BASE_URL": "https://api.deepseek.com/anthropic",
		},
	}
	writeJSON(settingsPath, data)

	status, err := svc.GetStatus(PlatformClaude, 3688)
	if err != nil {
		t.Fatalf("GetStatus failed: %v", err)
	}
	if status.Provider != ProviderDeepSeek {
		t.Errorf("provider = %q, want %q", status.Provider, ProviderDeepSeek)
	}
	if !status.Configured {
		t.Error("should be configured for DeepSeek")
	}
}

func TestApplyAndRestoreClaudeXFyunProvider(t *testing.T) {
	svc := newTestService(t)
	settingsPath := filepath.Join(svc.homeDir, ".claude", "settings.json")
	os.MkdirAll(filepath.Dir(settingsPath), 0o755)
	original := map[string]any{
		"env": map[string]any{
			"ANTHROPIC_MODEL":            "original-model",
			"ANTHROPIC_SMALL_FAST_MODEL": "original-small",
		},
	}
	writeJSON(settingsPath, original)

	err := svc.Apply(ApplyAgentConfigRequest{Platform: PlatformClaude, Provider: ProviderXFyun, APIKey: "xf-test-key"}, 3688, "proxy-key")
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	var after map[string]any
	readJSON(settingsPath, &after)
	env := after["env"].(map[string]any)
	if env["ANTHROPIC_BASE_URL"] != xfyunClaudeBaseURL {
		t.Errorf("base_url = %v, want %s", env["ANTHROPIC_BASE_URL"], xfyunClaudeBaseURL)
	}
	if env["ANTHROPIC_AUTH_TOKEN"] != "xf-test-key" {
		t.Errorf("auth_token = %v", env["ANTHROPIC_AUTH_TOKEN"])
	}
	if env["ANTHROPIC_MODEL"] != "astron-code-latest" {
		t.Errorf("model = %v", env["ANTHROPIC_MODEL"])
	}
	if env["ANTHROPIC_SMALL_FAST_MODEL"] != "astron-code-latest" {
		t.Errorf("small_fast_model = %v", env["ANTHROPIC_SMALL_FAST_MODEL"])
	}

	err = svc.Restore(PlatformClaude)
	if err != nil {
		t.Fatalf("Restore failed: %v", err)
	}

	var restored map[string]any
	readJSON(settingsPath, &restored)
	env = restored["env"].(map[string]any)
	if env["ANTHROPIC_MODEL"] != "original-model" {
		t.Errorf("restored model = %v", env["ANTHROPIC_MODEL"])
	}
	if env["ANTHROPIC_SMALL_FAST_MODEL"] != "original-small" {
		t.Errorf("restored small_fast_model = %v", env["ANTHROPIC_SMALL_FAST_MODEL"])
	}
	if _, ok := env["ANTHROPIC_BASE_URL"]; ok {
		t.Errorf("base_url should be removed after restore, got %v", env["ANTHROPIC_BASE_URL"])
	}
}

func TestApplyClaudeSenseNovaProvider(t *testing.T) {
	svc := newTestService(t)
	settingsPath := filepath.Join(svc.homeDir, ".claude", "settings.json")
	os.MkdirAll(filepath.Dir(settingsPath), 0o755)

	err := svc.Apply(ApplyAgentConfigRequest{Platform: PlatformClaude, Provider: ProviderSenseNova, APIKey: "sensenova-key"}, 3688, "proxy-key")
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	var after map[string]any
	readJSON(settingsPath, &after)
	env := after["env"].(map[string]any)
	if env["ANTHROPIC_BASE_URL"] != sensenovaClaudeBaseURL {
		t.Errorf("base_url = %v, want %s", env["ANTHROPIC_BASE_URL"], sensenovaClaudeBaseURL)
	}
	if env["ANTHROPIC_AUTH_TOKEN"] != "sensenova-key" {
		t.Errorf("auth_token = %v", env["ANTHROPIC_AUTH_TOKEN"])
	}
	if env["ANTHROPIC_MODEL"] != "sensenova-6.7-flash-lite" {
		t.Errorf("model = %v", env["ANTHROPIC_MODEL"])
	}
	if env["ANTHROPIC_SMALL_FAST_MODEL"] != "sensenova-6.7-flash-lite" {
		t.Errorf("small_fast_model = %v", env["ANTHROPIC_SMALL_FAST_MODEL"])
	}

	status, err := svc.GetStatus(PlatformClaude, 3688)
	if err != nil {
		t.Fatalf("GetStatus failed: %v", err)
	}
	if status.Provider != ProviderSenseNova {
		t.Errorf("provider = %q, want %q", status.Provider, ProviderSenseNova)
	}
	if !status.Configured {
		t.Error("SenseNova Claude provider should be configured")
	}
}

func TestApplyAndRestoreClaude(t *testing.T) {
	svc := newTestService(t)
	settingsPath := filepath.Join(svc.homeDir, ".claude", "settings.json")
	configPath := filepath.Join(svc.homeDir, ".claude", "config.json")
	os.MkdirAll(filepath.Dir(settingsPath), 0o755)
	original := map[string]any{
		"env": map[string]any{
			"ANTHROPIC_BASE_URL": "https://original.example.com",
		},
	}
	writeJSON(settingsPath, original)
	writeJSON(configPath, map[string]any{"primaryApiKey": "sk-original"})

	err := svc.Apply(ApplyAgentConfigRequest{Platform: PlatformClaude}, 3688, "test-key")
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	// 验证注入后配置
	var after map[string]any
	readJSON(settingsPath, &after)
	env := after["env"].(map[string]any)
	if env["ANTHROPIC_BASE_URL"] != "http://127.0.0.1:3688" {
		t.Errorf("base_url = %v", env["ANTHROPIC_BASE_URL"])
	}
	if env["ANTHROPIC_AUTH_TOKEN"] != "test-key" {
		t.Errorf("auth_token = %v", env["ANTHROPIC_AUTH_TOKEN"])
	}
	var configAfter map[string]any
	readJSON(configPath, &configAfter)
	if configAfter["primaryApiKey"] != "sk-original" {
		t.Errorf("primaryApiKey should be preserved, got %v", configAfter["primaryApiKey"])
	}

	// Restore
	err = svc.Restore(PlatformClaude)
	if err != nil {
		t.Fatalf("Restore failed: %v", err)
	}
	var restored map[string]any
	readJSON(settingsPath, &restored)
	env = restored["env"].(map[string]any)
	if env["ANTHROPIC_BASE_URL"] != "https://original.example.com" {
		t.Errorf("restored base_url = %v", env["ANTHROPIC_BASE_URL"])
	}
}

func TestApplyClaudeUsesHTTPSWhenEnabled(t *testing.T) {
	svc := newTestService(t)
	if err := os.WriteFile(filepath.Join(filepath.Dir(svc.stateDir), ".env"), []byte("ENABLE_HTTPS=true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(svc.homeDir, ".claude", "settings.json")

	err := svc.Apply(ApplyAgentConfigRequest{Platform: PlatformClaude}, 8443, "test-key")
	if err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	var after map[string]any
	readJSON(settingsPath, &after)
	env := after["env"].(map[string]any)
	if env["ANTHROPIC_BASE_URL"] != "https://127.0.0.1:8443" {
		t.Errorf("base_url = %v", env["ANTHROPIC_BASE_URL"])
	}
}

func TestGetStatusClaudeMatchesHTTPSLocalBaseURL(t *testing.T) {
	svc := newTestService(t)
	if err := os.WriteFile(filepath.Join(filepath.Dir(svc.stateDir), ".env"), []byte("ENABLE_HTTPS=true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	settingsPath := filepath.Join(svc.homeDir, ".claude", "settings.json")
	writeJSON(settingsPath, map[string]any{
		"env": map[string]any{
			"ANTHROPIC_BASE_URL": "https://127.0.0.1:8443",
		},
	})

	status, err := svc.GetStatus(PlatformClaude, 8443)
	if err != nil {
		t.Fatalf("GetStatus failed: %v", err)
	}
	if !status.MatchesCurrentPort {
		t.Error("HTTPS local base URL should match current port")
	}
	if status.TargetBaseURL != "https://127.0.0.1:8443" {
		t.Errorf("TargetBaseURL = %q", status.TargetBaseURL)
	}
}

func TestPreviewApplyClaudeDoesNotTouchPrimaryAPIKey(t *testing.T) {
	svc := newTestService(t)
	settingsPath := filepath.Join(svc.homeDir, ".claude", "settings.json")
	configPath := filepath.Join(svc.homeDir, ".claude", "config.json")
	writeJSON(settingsPath, map[string]any{"env": map[string]any{}})
	writeJSON(configPath, map[string]any{"primaryApiKey": "sk-original"})

	diff, err := svc.PreviewApply(ApplyAgentConfigRequest{Platform: PlatformClaude}, 3688, "test-key")
	if err != nil {
		t.Fatalf("PreviewApply failed: %v", err)
	}
	for _, file := range diff.Files {
		if file.Path == configPath {
			t.Fatalf("preview should not include config.json diff: %+v", file)
		}
	}
}

func TestSaveAndLoadProviderKeys(t *testing.T) {
	svc := newTestService(t)

	err := svc.SaveProviderKeyAsset(ProviderKeyAsset{
		Provider: ProviderDeepSeek,
		APIKey:   "sk-test-key",
		BaseURL:  "https://api.deepseek.com",
	})
	if err != nil {
		t.Fatalf("SaveProviderKeyAsset failed: %v", err)
	}
	err = svc.SaveProviderKeyAsset(ProviderKeyAsset{
		Provider: ProviderMiMo,
		APIKey:   "mimo-test-key",
		BaseURL:  "https://api.xiaomimimo.com/anthropic",
	})
	if err != nil {
		t.Fatalf("SaveProviderKeyAsset failed: %v", err)
	}
	err = svc.SaveProviderKeyAsset(ProviderKeyAsset{
		Provider: ProviderSenseNova,
		APIKey:   "sensenova-test-key",
		BaseURL:  "https://token.sensenova.cn",
	})
	if err != nil {
		t.Fatalf("SaveProviderKeyAsset failed: %v", err)
	}

	keys := svc.GetSavedProviderKeys()
	if keys["channel:"+ProviderDeepSeek] != "sk-test-key" {
		t.Errorf("channel key = %q", keys["channel:"+ProviderDeepSeek])
	}
	if keys[PlatformClaude+":"+ProviderDeepSeek] != "sk-test-key" {
		t.Errorf("claude key = %q", keys[PlatformClaude+":"+ProviderDeepSeek])
	}
	if keys["channel:"+ProviderMiMo] != "mimo-test-key" {
		t.Errorf("mimo channel key = %q", keys["channel:"+ProviderMiMo])
	}
	if keys[PlatformClaude+":"+ProviderMiMo] != "mimo-test-key" {
		t.Errorf("mimo claude key = %q", keys[PlatformClaude+":"+ProviderMiMo])
	}
	if keys["channel:"+ProviderSenseNova] != "sensenova-test-key" {
		t.Errorf("sensenova channel key = %q", keys["channel:"+ProviderSenseNova])
	}
	if keys[PlatformClaude+":"+ProviderSenseNova] != "sensenova-test-key" {
		t.Errorf("sensenova claude key = %q", keys[PlatformClaude+":"+ProviderSenseNova])
	}

	assets := svc.GetProviderKeyAssets()
	foundDeepSeek := false
	foundMiMo := false
	foundSenseNova := false
	for _, a := range assets {
		switch a.Provider {
		case ProviderDeepSeek:
			foundDeepSeek = true
			if a.APIKey != "sk-test-key" {
				t.Errorf("asset APIKey = %q", a.APIKey)
			}
		case ProviderMiMo:
			foundMiMo = true
			if a.APIKey != "mimo-test-key" {
				t.Errorf("mimo asset APIKey = %q", a.APIKey)
			}
		case ProviderSenseNova:
			foundSenseNova = true
			if a.APIKey != "sensenova-test-key" {
				t.Errorf("sensenova asset APIKey = %q", a.APIKey)
			}
		}
	}
	if !foundDeepSeek {
		t.Error("DeepSeek asset not found")
	}
	if !foundMiMo {
		t.Error("MiMo asset not found")
	}
	if !foundSenseNova {
		t.Error("SenseNova asset not found")
	}
}

func TestProviderKeyAssetsKeepPlanScopedKeys(t *testing.T) {
	svc := newTestService(t)

	if err := svc.SaveProviderKeyAsset(ProviderKeyAsset{
		Provider: ProviderMiMo,
		APIKey:   "tp-openai-key",
		PlanID:   "openai-chat",
	}); err != nil {
		t.Fatalf("SaveProviderKeyAsset openai-chat failed: %v", err)
	}
	if err := svc.SaveProviderKeyAsset(ProviderKeyAsset{
		Provider: ProviderMiMo,
		APIKey:   "tp-anthropic-key",
		PlanID:   "anthropic",
	}); err != nil {
		t.Fatalf("SaveProviderKeyAsset anthropic failed: %v", err)
	}

	assets := svc.GetProviderKeyAssets()
	if len(assets) != 2 {
		t.Fatalf("assets len = %d", len(assets))
	}
	if assets[0].PlanID != "anthropic" || assets[0].APIKey != "tp-anthropic-key" {
		t.Fatalf("first asset = %+v", assets[0])
	}
	if assets[1].PlanID != "openai-chat" || assets[1].APIKey != "tp-openai-key" {
		t.Fatalf("second asset = %+v", assets[1])
	}

	keys := svc.GetSavedProviderKeys()
	if keys[PlatformClaude+":"+ProviderMiMo+":anthropic"] != "tp-anthropic-key" {
		t.Fatalf("anthropic saved key = %q", keys[PlatformClaude+":"+ProviderMiMo+":anthropic"])
	}
	if keys[PlatformClaude+":"+ProviderMiMo+":openai-chat"] != "tp-openai-key" {
		t.Fatalf("openai-chat saved key = %q", keys[PlatformClaude+":"+ProviderMiMo+":openai-chat"])
	}
	if keys["channel:"+ProviderMiMo] != "" {
		t.Fatalf("plan-scoped save should not overwrite provider channel key, got %q", keys["channel:"+ProviderMiMo])
	}
}

func TestProviderKeyAssetPersistsProxyURL(t *testing.T) {
	svc := newTestService(t)

	if err := svc.SaveProviderKeyAsset(ProviderKeyAsset{
		Provider: "github-copilot",
		APIKey:   "gho_test",
		BaseURL:  "https://api.githubcopilot.com",
		ProxyURL: " socks5://127.0.0.1:1080 ",
		Usages:   []string{"responses-channel"},
	}); err != nil {
		t.Fatalf("SaveProviderKeyAsset failed: %v", err)
	}

	assets := svc.GetProviderKeyAssets()
	if len(assets) != 1 {
		t.Fatalf("assets len = %d", len(assets))
	}
	if assets[0].ProxyURL != "socks5://127.0.0.1:1080" {
		t.Fatalf("ProxyURL = %q, want socks5://127.0.0.1:1080", assets[0].ProxyURL)
	}
	if assets[0].APIKey != "gho_test" {
		t.Fatalf("APIKey = %q, want gho_test", assets[0].APIKey)
	}
}

// ── P1 回归: Codex 第三方 provider 状态识别 ──
