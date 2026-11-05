package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// 固定 v2.9.37 六协议配置形态，防止后续改动把旧渠道、Key 或映射静默丢弃。
func TestV2937ConfigSurvivesLoadSaveAndRestart(t *testing.T) {
	fixture, err := os.ReadFile("testdata/v2_9_37_config.json")
	if err != nil {
		t.Fatal(err)
	}
	var want Config
	if err := json.Unmarshal(fixture, &want); err != nil {
		t.Fatal(err)
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, fixture, 0600); err != nil {
		t.Fatal(err)
	}
	open := func() *ConfigManager {
		t.Helper()
		cm, err := NewConfigManager(path, filepath.Join(dir, "backups"))
		if err != nil {
			t.Fatalf("加载 2.9 配置失败: %v", err)
		}
		return cm
	}
	check := func(got Config) {
		t.Helper()
		pairs := []struct {
			name string
			got  []UpstreamConfig
			want []UpstreamConfig
		}{
			{"messages", got.Upstream, want.Upstream},
			{"responses", got.ResponsesUpstream, want.ResponsesUpstream},
			{"chat", got.ChatUpstream, want.ChatUpstream},
			{"gemini", got.GeminiUpstream, want.GeminiUpstream},
			{"images", got.ImagesUpstream, want.ImagesUpstream},
			{"vectors", got.VectorsUpstream, want.VectorsUpstream},
		}
		for _, pair := range pairs {
			if !reflect.DeepEqual(pair.got, pair.want) {
				t.Errorf("%s 渠道变化: got=%+v want=%+v", pair.name, pair.got, pair.want)
			}
		}
		if got.FuzzyModeEnabled != want.FuzzyModeEnabled || got.ThinkingCache != want.ThinkingCache {
			t.Errorf("全局设置变化: fuzzy=%v thinking=%+v", got.FuzzyModeEnabled, got.ThinkingCache)
		}
	}

	cm := open()
	check(cm.GetConfig())
	if err := cm.SaveConfig(); err != nil {
		t.Fatalf("保存 2.9 配置失败: %v", err)
	}
	if err := cm.Close(); err != nil {
		t.Fatal(err)
	}

	cm = open()
	defer cm.Close()
	check(cm.GetConfig())
}

func TestV3ConfigIsRejectedWithoutChangingFile(t *testing.T) {
	for _, marker := range []string{
		`"channelsV3": [{"uid": "old-channel"}]`,
		`"channelAuthoritativeVersion": 1`,
	} {
		t.Run(marker, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.json")
			original := []byte(`{"upstream": [], "responsesUpstream": [], ` + marker + `}`)
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			cm, err := NewConfigManager(path, filepath.Join(dir, "backups"))
			if cm != nil || err == nil || !strings.Contains(err.Error(), "3.0") {
				t.Fatalf("应拒绝 3.0 配置: manager=%v err=%v", cm, err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, original) {
				t.Fatal("拒绝加载后原配置被改写")
			}
		})
	}
}
