package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSuspendAndResumeKey(t *testing.T) {
	apiTypes := []string{"Messages", "Responses", "Gemini", "Chat", "Images", "Vectors"}
	for _, apiType := range apiTypes {
		t.Run(apiType, func(t *testing.T) {
			cm := &ConfigManager{configFile: filepath.Join(t.TempDir(), "config.json")}
			upstreams := cm.getUpstreamSliceLocked(apiType)
			*upstreams = []UpstreamConfig{{
				Name:    "test-channel",
				APIKeys: []string{"sk-test"},
				APIKeyConfigs: []APIKeyConfig{{
					Key:  "sk-test",
					Name: "primary",
				}},
			}}

			if err := cm.SuspendKey(apiType, 0, "sk-test"); err != nil {
				t.Fatalf("SuspendKey() error = %v", err)
			}
			got := (*upstreams)[0]
			if len(got.APIKeys) != 1 || got.APIKeys[0] != "sk-test" {
				t.Fatalf("暂停后 apiKeys = %v, 应保留原 Key", got.APIKeys)
			}
			if got.APIKeyConfigs[0].Enabled == nil || *got.APIKeyConfigs[0].Enabled {
				t.Fatalf("暂停后 Enabled = %v, want false", got.APIKeyConfigs[0].Enabled)
			}
			if _, err := cm.GetNextAPIKey(&got, nil, apiType); err == nil {
				t.Fatal("暂停后的 Key 不应再被故障转移路径选中")
			}

			data, err := os.ReadFile(cm.configFile)
			if err != nil {
				t.Fatalf("读取持久化配置失败: %v", err)
			}
			var persisted Config
			if err := json.Unmarshal(data, &persisted); err != nil {
				t.Fatalf("解析持久化配置失败: %v", err)
			}
			persistedUpstream := (*(&ConfigManager{config: persisted}).getUpstreamSliceLocked(apiType))[0]
			if persistedUpstream.APIKeyConfigs[0].Enabled == nil || *persistedUpstream.APIKeyConfigs[0].Enabled {
				t.Fatal("暂停状态未持久化")
			}

			if err := cm.ResumeKey(apiType, 0, "sk-test"); err != nil {
				t.Fatalf("ResumeKey() error = %v", err)
			}
			got = (*upstreams)[0]
			if got.APIKeyConfigs[0].Enabled != nil {
				t.Fatalf("恢复后 Enabled = %v, want nil", *got.APIKeyConfigs[0].Enabled)
			}
			if got.APIKeyConfigs[0].Name != "primary" {
				t.Fatalf("恢复后其他 Key 配置丢失: %+v", got.APIKeyConfigs[0])
			}
		})
	}
}

func TestRestoreAllKeysAlsoResumesManuallyPausedKeys(t *testing.T) {
	cm := &ConfigManager{configFile: filepath.Join(t.TempDir(), "config.json")}
	disabled := false
	cm.config.Upstream = []UpstreamConfig{{
		APIKeys: []string{"sk-paused"},
		APIKeyConfigs: []APIKeyConfig{{
			Key:     "sk-paused",
			Enabled: &disabled,
			Name:    "primary",
		}},
	}}

	restored, err := cm.RestoreAllKeys("Messages", 0)
	if err != nil {
		t.Fatalf("RestoreAllKeys() error = %v", err)
	}
	if restored != 1 {
		t.Fatalf("restored = %d, want 1", restored)
	}
	cfg := cm.config.Upstream[0].APIKeyConfigs[0]
	if cfg.Enabled != nil || cfg.Name != "primary" {
		t.Fatalf("恢复结果 = %+v, 应解除暂停并保留其他配置", cfg)
	}
	restored, err = cm.RestoreAllKeys("Messages", 0)
	if err != nil || restored != 0 {
		t.Fatalf("重复恢复 restored = %d, err = %v, want 0, nil", restored, err)
	}
}

func TestSuspendKeyRejectsUnknownKey(t *testing.T) {
	cm := &ConfigManager{configFile: filepath.Join(t.TempDir(), "config.json")}
	cm.config.Upstream = []UpstreamConfig{{APIKeys: []string{"sk-test"}}}
	if err := cm.SuspendKey("Messages", 0, "sk-unknown"); err == nil {
		t.Fatal("暂停不存在的 Key 应返回错误")
	}
}
