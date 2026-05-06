package config

import (
	"os"
	"path/filepath"
	"testing"
)

func newConfigManagerFromJSON(t *testing.T, rawConfig string) *ConfigManager {
	t.Helper()

	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	if err := os.WriteFile(configPath, []byte(rawConfig), 0644); err != nil {
		t.Fatalf("写入测试配置失败: %v", err)
	}

	cm, err := NewConfigManager(configPath)
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}

	return cm
}

func TestMessagesCurrentUpstreamRequiresActiveChannel(t *testing.T) {
	cm := newConfigManagerFromJSON(t, `{
		"upstream": [{
			"name": "messages-suspended",
			"status": "suspended",
			"apiKeys": ["test-key"],
			"serviceType": "claude"
		}]
	}`)
	defer func() { _ = cm.Close() }()

	upstream, err := cm.GetCurrentUpstream()
	if err == nil {
		t.Fatal("GetCurrentUpstream() expected error when no active channel exists")
	}
	if upstream != nil {
		t.Fatalf("GetCurrentUpstream() returned upstream %q on error", upstream.Name)
	}

	upstream, index, err := cm.GetCurrentUpstreamWithIndex()
	if err == nil {
		t.Fatal("GetCurrentUpstreamWithIndex() expected error when no active channel exists")
	}
	if upstream != nil {
		t.Fatalf("GetCurrentUpstreamWithIndex() returned upstream %q on error", upstream.Name)
	}
	if index != -1 {
		t.Fatalf("GetCurrentUpstreamWithIndex() index = %d, want -1", index)
	}
}

func TestResponsesCurrentUpstreamRequiresActiveChannel(t *testing.T) {
	cm := newConfigManagerFromJSON(t, `{
		"responsesUpstream": [{
			"name": "responses-disabled",
			"status": "disabled",
			"apiKeys": ["test-key"],
			"serviceType": "claude"
		}]
	}`)
	defer func() { _ = cm.Close() }()

	upstream, err := cm.GetCurrentResponsesUpstream()
	if err == nil {
		t.Fatal("GetCurrentResponsesUpstream() expected error when no active channel exists")
	}
	if upstream != nil {
		t.Fatalf("GetCurrentResponsesUpstream() returned upstream %q on error", upstream.Name)
	}

	upstream, index, err := cm.GetCurrentResponsesUpstreamWithIndex()
	if err == nil {
		t.Fatal("GetCurrentResponsesUpstreamWithIndex() expected error when no active channel exists")
	}
	if upstream != nil {
		t.Fatalf("GetCurrentResponsesUpstreamWithIndex() returned upstream %q on error", upstream.Name)
	}
	if index != -1 {
		t.Fatalf("GetCurrentResponsesUpstreamWithIndex() index = %d, want -1", index)
	}
}

func TestUpdateUpstreamRejectsDuplicateNames(t *testing.T) {
	cm := newConfigManagerFromJSON(t, `{
		"upstream": [
			{
				"name": "messages-a",
				"status": "active",
				"apiKeys": ["key-a"],
				"serviceType": "claude"
			},
			{
				"name": "messages-b",
				"status": "active",
				"apiKeys": ["key-b"],
				"serviceType": "claude"
			}
		]
	}`)
	defer func() { _ = cm.Close() }()

	_, err := cm.UpdateUpstream(0, UpstreamUpdate{Name: strPtr("messages-b")})
	if err == nil {
		t.Fatal("UpdateUpstream() expected duplicate name error")
	}

	cfg := cm.GetConfig()
	if cfg.Upstream[0].Name != "messages-a" {
		t.Fatalf("UpdateUpstream() unexpectedly changed name to %q", cfg.Upstream[0].Name)
	}
}

func TestUpdateResponsesUpstreamRejectsDuplicateNames(t *testing.T) {
	cm := newConfigManagerFromJSON(t, `{
		"responsesUpstream": [
			{
				"name": "responses-a",
				"status": "active",
				"apiKeys": ["key-a"],
				"serviceType": "claude"
			},
			{
				"name": "responses-b",
				"status": "active",
				"apiKeys": ["key-b"],
				"serviceType": "claude"
			}
		]
	}`)
	defer func() { _ = cm.Close() }()

	_, err := cm.UpdateResponsesUpstream(0, UpstreamUpdate{Name: strPtr("responses-b")})
	if err == nil {
		t.Fatal("UpdateResponsesUpstream() expected duplicate name error")
	}

	cfg := cm.GetConfig()
	if cfg.ResponsesUpstream[0].Name != "responses-a" {
		t.Fatalf("UpdateResponsesUpstream() unexpectedly changed name to %q", cfg.ResponsesUpstream[0].Name)
	}
}
