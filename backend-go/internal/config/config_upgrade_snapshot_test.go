package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// 仅在指定私有快照时运行；始终在测试目录中加载，避免改写来源文件。
func TestLegacyConfigSnapshotUpgrade(t *testing.T) {
	sourcePath := os.Getenv("CCX_UPGRADE_SNAPSHOT_CONFIG")
	if sourcePath == "" {
		t.Skip("未指定 2.9 配置快照")
	}
	source, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatalf("读取配置快照失败: %v", err)
	}
	var before Config
	if err := json.Unmarshal(source, &before); err != nil {
		t.Fatalf("解析配置快照失败: %v", err)
	}

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, source, 0600); err != nil {
		t.Fatalf("复制配置快照失败: %v", err)
	}
	open := func() *ConfigManager {
		t.Helper()
		cm, err := NewConfigManager(configPath, filepath.Join(dir, "backups"))
		if err != nil {
			t.Fatalf("加载隔离配置失败: %v", err)
		}
		return cm
	}
	check := func(stage string, after Config) {
		t.Helper()
		for _, pair := range []struct {
			kind   string
			before []UpstreamConfig
			after  []UpstreamConfig
		}{
			{"messages", before.Upstream, after.Upstream},
			{"responses", before.ResponsesUpstream, after.ResponsesUpstream},
			{"chat", before.ChatUpstream, after.ChatUpstream},
			{"gemini", before.GeminiUpstream, after.GeminiUpstream},
			{"images", before.ImagesUpstream, after.ImagesUpstream},
			{"vectors", before.VectorsUpstream, after.VectorsUpstream},
		} {
			if len(pair.before) != len(pair.after) {
				t.Errorf("%s: %s 渠道数变化: %d -> %d", stage, pair.kind, len(pair.before), len(pair.after))
				continue
			}
			for i, old := range pair.before {
				current := pair.after[i]
				for _, field := range []struct {
					name string
					old  any
					new  any
				}{
					{"name", old.Name, current.Name},
					{"baseUrl", old.BaseURL, current.BaseURL},
					{"baseUrls", old.BaseURLs, current.BaseURLs},
					{"apiKeys", old.APIKeys, current.APIKeys},
					{"apiKeyConfigs", old.APIKeyConfigs, current.APIKeyConfigs},
					{"disabledApiKeys", old.DisabledAPIKeys, current.DisabledAPIKeys},
					{"historicalApiKeys", old.HistoricalAPIKeys, current.HistoricalAPIKeys},
					{"priority", old.Priority, current.Priority},
					{"supportedModels", old.SupportedModels, current.SupportedModels},
					{"routePrefix", old.RoutePrefix, current.RoutePrefix},
					{"customHeaders", old.CustomHeaders, current.CustomHeaders},
					{"proxyUrl", old.ProxyURL, current.ProxyURL},
				} {
					if !reflect.DeepEqual(field.old, field.new) {
						t.Errorf("%s: %s[%d].%s 发生变化", stage, pair.kind, i, field.name)
					}
				}
				if old.Status != "" && (old.Status != "active" || len(old.APIKeys) > 0) && old.Status != current.Status {
					t.Errorf("%s: %s[%d].status 发生变化", stage, pair.kind, i)
				}
				if old.ServiceType != "" && old.ServiceType != current.ServiceType {
					t.Errorf("%s: %s[%d].serviceType 发生变化", stage, pair.kind, i)
				}
				for model, target := range old.ModelMapping {
					if current.ModelMapping[model] != target {
						t.Errorf("%s: %s[%d].modelMapping 中已有映射发生变化", stage, pair.kind, i)
					}
				}
			}
		}
	}

	cm := open()
	check("首次加载", cm.GetConfig())
	if err := cm.SaveConfig(); err != nil {
		t.Fatalf("保存隔离配置失败: %v", err)
	}
	saved, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	checkLegacyJSONFields(t, source, saved)
	if err := cm.Close(); err != nil {
		t.Fatal(err)
	}
	cm = open()
	check("保存后重启", cm.GetConfig())
	if err := cm.Close(); err != nil {
		t.Fatal(err)
	}
	backups, err := os.ReadDir(filepath.Join(dir, "backups"))
	if err != nil || len(backups) == 0 {
		t.Fatalf("升级保存后未生成配置备份: count=%d err=%v", len(backups), err)
	}
	if err := os.WriteFile(configPath, source, 0600); err != nil {
		t.Fatalf("回放原始配置失败: %v", err)
	}
	cm = open()
	check("回放原始配置", cm.GetConfig())
	if err := cm.Close(); err != nil {
		t.Fatal(err)
	}
	unchanged, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(source, unchanged) {
		t.Fatal("源配置快照被改写")
	}
}

func checkLegacyJSONFields(t *testing.T, source, saved []byte) {
	t.Helper()
	var oldRoot, newRoot map[string]json.RawMessage
	if err := json.Unmarshal(source, &oldRoot); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(saved, &newRoot); err != nil {
		t.Fatal(err)
	}
	for field, oldRaw := range oldRoot {
		switch field {
		case "upstream", "responsesUpstream", "chatUpstream", "geminiUpstream", "imagesUpstream", "vectorsUpstream", "currentUpstream", "currentResponsesUpstream":
			continue
		}
		var oldValue, newValue any
		if err := json.Unmarshal(oldRaw, &oldValue); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(newRoot[field], &newValue); err != nil || !reflect.DeepEqual(oldValue, newValue) {
			t.Errorf("全局设置 %s 在升级后发生变化", field)
		}
	}
	for _, kind := range []string{"upstream", "responsesUpstream", "chatUpstream", "geminiUpstream", "imagesUpstream", "vectorsUpstream"} {
		var oldChannels, newChannels []map[string]json.RawMessage
		if raw := oldRoot[kind]; len(raw) > 0 {
			if err := json.Unmarshal(raw, &oldChannels); err != nil {
				t.Fatal(err)
			}
		}
		if raw := newRoot[kind]; len(raw) > 0 {
			if err := json.Unmarshal(raw, &newChannels); err != nil {
				t.Fatal(err)
			}
		}
		if len(oldChannels) != len(newChannels) {
			t.Errorf("%s 渠道数变化", kind)
			continue
		}
		for i, old := range oldChannels {
			for field, oldRaw := range old {
				var oldValue, newValue any
				if err := json.Unmarshal(oldRaw, &oldValue); err != nil {
					t.Fatal(err)
				}
				if oldValue == nil || reflect.DeepEqual(oldValue, false) || reflect.DeepEqual(oldValue, float64(0)) || reflect.DeepEqual(oldValue, "") {
					continue
				}
				if oldMap, ok := oldValue.(map[string]any); ok && len(oldMap) == 0 {
					continue
				}
				if oldList, ok := oldValue.([]any); ok && len(oldList) == 0 {
					continue
				}
				if field == "status" && oldValue == "active" {
					var keys []string
					_ = json.Unmarshal(old["apiKeys"], &keys)
					if len(keys) == 0 {
						continue // 2.9 的既有自检会暂停无 Key 的活跃渠道。
					}
				}
				if err := json.Unmarshal(newChannels[i][field], &newValue); err != nil {
					t.Errorf("%s[%d].%s 在升级后缺失", kind, i, field)
					continue
				}
				if field == "modelMapping" || field == "reasoningMapping" {
					newMap, ok := newValue.(map[string]any)
					if !ok {
						t.Errorf("%s[%d].%s 在升级后不再是映射", kind, i, field)
						continue
					}
					for key, value := range oldValue.(map[string]any) {
						if newMap[key] != value {
							t.Errorf("%s[%d].%s 中已有映射发生变化", kind, i, field)
						}
					}
					continue
				}
				if !reflect.DeepEqual(oldValue, newValue) {
					t.Errorf("%s[%d].%s 在升级后发生变化", kind, i, field)
				}
			}
		}
	}
}
