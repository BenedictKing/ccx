package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImagesConfigLoadsAndInitializes(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	initialConfig := `{
		"upstream": [],
		"responsesUpstream": [],
		"geminiUpstream": [],
		"chatUpstream": [],
		"imagesUpstream": [{
			"name": "images-primary",
			"baseUrl": "https://api.openai.com/v1",
			"baseUrls": ["https://api.openai.com/v1/", "https://images.example.com/v1"],
			"apiKeys": ["sk-img", "sk-img"],
			"serviceType": ""
		}]
	}`
	if err := os.WriteFile(configPath, []byte(initialConfig), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cm, err := NewConfigManager(configPath)
	if err != nil {
		t.Fatalf("NewConfigManager: %v", err)
	}
	defer func() { _ = cm.Close() }()

	upstream, index, err := cm.GetCurrentImagesUpstreamWithIndex()
	if err != nil {
		t.Fatalf("GetCurrentImagesUpstreamWithIndex: %v", err)
	}
	if index != 0 {
		t.Fatalf("index = %d, want 0", index)
	}
	if upstream.ServiceType != "openai" {
		t.Fatalf("ServiceType = %q, want openai", upstream.ServiceType)
	}
	if upstream.BaseURL != "https://api.openai.com/v1" {
		t.Fatalf("stored BaseURL = %q, want original load value", upstream.BaseURL)
	}
	if got := upstream.GetEffectiveBaseURL(); got != "https://api.openai.com" {
		t.Fatalf("effective BaseURL = %q, want canonical base", got)
	}
	if len(upstream.APIKeys) != 2 {
		t.Fatalf("loaded APIKeys length = %d, want 2 before explicit update", len(upstream.APIKeys))
	}

	if err := cm.AddImagesUpstream(UpstreamConfig{
		Name:        "images-secondary",
		BaseURL:     "https://alt.example.com/v1",
		BaseURLs:    []string{"https://alt.example.com/v1/", "https://alt.example.com"},
		APIKeys:     []string{"sk-alt", "sk-alt"},
		ServiceType: "openai",
	}); err != nil {
		t.Fatalf("AddImagesUpstream: %v", err)
	}

	cfg := cm.GetConfig()
	added := cfg.ImagesUpstream[1]
	if added.BaseURL != "https://alt.example.com" {
		t.Fatalf("added BaseURL = %q, want canonical", added.BaseURL)
	}
	if len(added.BaseURLs) != 1 || added.BaseURLs[0] != "https://alt.example.com" {
		t.Fatalf("added BaseURLs = %#v, want single canonical URL", added.BaseURLs)
	}
	if len(added.APIKeys) != 1 {
		t.Fatalf("added APIKeys length = %d, want deduplicated 1", len(added.APIKeys))
	}
}

func TestConfigSaveDropsLegacyRPMField(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	initialConfig := `{
		"upstream": [{
			"name": "legacy-rpm",
			"baseUrl": "https://api.example.com",
			"apiKeys": ["sk-test"],
			"serviceType": "claude",
			"rpm": 42
		}]
	}`
	if err := os.WriteFile(configPath, []byte(initialConfig), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cm, err := NewConfigManager(configPath)
	if err != nil {
		t.Fatalf("NewConfigManager: %v", err)
	}
	defer func() { _ = cm.Close() }()

	if err := cm.SaveConfig(); err != nil {
		t.Fatalf("SaveConfig: %v", err)
	}
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("read config: %v", err)
	}
	if strings.Contains(string(data), `"rpm"`) {
		t.Fatalf("saved config still contains legacy rpm field: %s", string(data))
	}
}

func TestImagesCurrentUpstreamRequiresActiveChannel(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	initialConfig := `{
		"upstream": [],
		"imagesUpstream": [
			{
				"name": "images-disabled",
				"baseUrl": "https://disabled.example.com",
				"apiKeys": ["sk-disabled"],
				"serviceType": "openai",
				"status": "disabled"
			},
			{
				"name": "images-suspended",
				"baseUrl": "https://suspended.example.com",
				"apiKeys": ["sk-suspended"],
				"serviceType": "openai",
				"status": "suspended"
			}
		]
	}`
	if err := os.WriteFile(configPath, []byte(initialConfig), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cm, err := NewConfigManager(configPath)
	if err != nil {
		t.Fatalf("NewConfigManager: %v", err)
	}
	defer func() { _ = cm.Close() }()

	if upstream, err := cm.GetCurrentImagesUpstream(); err == nil || upstream != nil {
		t.Fatalf("GetCurrentImagesUpstream returned upstream=%v err=%v, want no active channel error", upstream, err)
	}
	if upstream, index, err := cm.GetCurrentImagesUpstreamWithIndex(); err == nil || upstream != nil || index != -1 {
		t.Fatalf("GetCurrentImagesUpstreamWithIndex returned upstream=%v index=%d err=%v, want no active channel error", upstream, index, err)
	}
}

func TestUpdateImagesUpstreamRejectsDuplicateName(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	initialConfig := `{
		"upstream": [],
		"imagesUpstream": [
			{
				"name": "images-a",
				"baseUrl": "https://a.example.com",
				"apiKeys": ["sk-a"],
				"serviceType": "openai"
			},
			{
				"name": "images-b",
				"baseUrl": "https://b.example.com",
				"apiKeys": ["sk-b"],
				"serviceType": "openai"
			}
		]
	}`
	if err := os.WriteFile(configPath, []byte(initialConfig), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cm, err := NewConfigManager(configPath)
	if err != nil {
		t.Fatalf("NewConfigManager: %v", err)
	}
	defer func() { _ = cm.Close() }()

	duplicateName := "images-a"
	if _, err := cm.UpdateImagesUpstream(1, UpstreamUpdate{Name: &duplicateName}); err == nil {
		t.Fatal("UpdateImagesUpstream duplicate name succeeded, want error")
	}

	cfg := cm.GetConfig()
	if cfg.ImagesUpstream[1].Name != "images-b" {
		t.Fatalf("duplicate rename mutated channel name to %q", cfg.ImagesUpstream[1].Name)
	}
}
