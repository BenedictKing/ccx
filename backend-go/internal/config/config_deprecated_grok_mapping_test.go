package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestLoadConfigMigratesDeprecatedGrokModelMapping 端到端验证：
// 真实加载包含历史遗留 grok 精确映射（含 vectors 渠道）的配置文件后，
// 迁移会清除该映射并落盘，同时保留用户自定义的其他映射。
func TestLoadConfigMigratesDeprecatedGrokModelMapping(t *testing.T) {
	root := map[string]any{
		"upstream": []any{
			map[string]any{
				"name":        "messages-legacy",
				"baseUrl":     "https://example.com",
				"serviceType": "claude",
				"modelMapping": map[string]any{
					"grok-4.1": "grok-4.1-thinking",
					"grok-4.2": "grok-4.20-beta",
				},
			},
		},
		"vectorsUpstream": []any{
			map[string]any{
				"name":        "vectors-legacy",
				"baseUrl":     "https://example.com",
				"serviceType": "openai",
				"modelMapping": map[string]any{
					"grok-4.2": "grok-4.20-beta",
					"grok-4.1": "my-custom-target",
				},
			},
		},
	}

	data, err := json.Marshal(root)
	if err != nil {
		t.Fatalf("序列化测试配置失败: %v", err)
	}
	configPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatalf("写入测试配置失败: %v", err)
	}

	cm, err := NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	cm.CloseWatcher()

	cfg := cm.GetConfig()
	if len(cfg.Upstream) != 1 {
		t.Fatalf("expected 1 messages upstream, got %d", len(cfg.Upstream))
	}
	if _, ok := cfg.Upstream[0].ModelMapping["grok-4.1"]; ok {
		t.Fatalf("expected legacy grok-4.1 mapping removed from messages, got %v", cfg.Upstream[0].ModelMapping)
	}
	if _, ok := cfg.Upstream[0].ModelMapping["grok-4.2"]; ok {
		t.Fatalf("expected legacy grok-4.2 mapping removed from messages, got %v", cfg.Upstream[0].ModelMapping)
	}

	if len(cfg.VectorsUpstream) != 1 {
		t.Fatalf("expected 1 vectors upstream, got %d", len(cfg.VectorsUpstream))
	}
	vectorsMapping := cfg.VectorsUpstream[0].ModelMapping
	if _, ok := vectorsMapping["grok-4.2"]; ok {
		t.Fatalf("expected legacy grok-4.2 mapping removed from vectors, got %v", vectorsMapping)
	}
	if vectorsMapping["grok-4.1"] != "my-custom-target" {
		t.Fatalf("expected custom grok-4.1 mapping preserved on vectors, got %v", vectorsMapping)
	}

	// 校验迁移已经落盘：重新读取磁盘上的配置文件，而不是内存快照。
	persisted, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("读取持久化配置失败: %v", err)
	}
	var onDisk Config
	if err := json.Unmarshal(persisted, &onDisk); err != nil {
		t.Fatalf("解析持久化配置失败: %v", err)
	}
	if _, ok := onDisk.Upstream[0].ModelMapping["grok-4.1"]; ok {
		t.Fatalf("expected legacy grok-4.1 mapping removed on disk, got %v", onDisk.Upstream[0].ModelMapping)
	}
	if onDisk.VectorsUpstream[0].ModelMapping["grok-4.1"] != "my-custom-target" {
		t.Fatalf("expected custom grok-4.1 mapping preserved on disk, got %v", onDisk.VectorsUpstream[0].ModelMapping)
	}

	// 二次加载应保持幂等，不再产生额外的迁移写盘。
	reloaded, err := NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("重新加载迁移后的配置失败: %v", err)
	}
	reloaded.CloseWatcher()
	reloadedCfg := reloaded.GetConfig()
	if len(reloadedCfg.Upstream[0].ModelMapping) != 0 {
		t.Fatalf("expected messages mapping to stay empty after reload, got %v", reloadedCfg.Upstream[0].ModelMapping)
	}
	if reloadedCfg.VectorsUpstream[0].ModelMapping["grok-4.1"] != "my-custom-target" {
		t.Fatalf("expected custom mapping to persist after reload, got %v", reloadedCfg.VectorsUpstream[0].ModelMapping)
	}
}
