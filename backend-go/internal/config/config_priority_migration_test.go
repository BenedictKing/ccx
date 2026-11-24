package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestLoadConfigNormalizesChannelPriorities(t *testing.T) {
	protocols := []string{"upstream", "responsesUpstream", "chatUpstream", "geminiUpstream", "imagesUpstream", "vectorsUpstream"}
	for _, tt := range []struct {
		name string
		in   []int
		want []int
	}{
		{"mixed", []int{0, 4, 0, 2}, []int{1, 4, 2, 2}},
		{"legacy", []int{0, 0, 0}, []int{1, 2, 3}},
		{"explicit", []int{7, 2}, []int{7, 2}},
		{"nonpositive", []int{-1, 2}, []int{1, 2}},
		{"unconfigured before larger explicit", []int{10, 0, 0, 2}, []int{10, 1, 2, 2}},
		{"trailing unconfigured", []int{3, 1, 0}, []int{3, 1, 2}},
		{"empty", []int{}, []int{}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			raw := make(map[string][]UpstreamConfig)
			for _, protocol := range protocols {
				for _, priority := range tt.in {
					raw[protocol] = append(raw[protocol], UpstreamConfig{
						Name: "legacy", BaseURL: "https://example.com", APIKeys: []string{"test-key"}, Priority: priority,
					})
				}
			}
			data, err := json.Marshal(raw)
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, data, 0600); err != nil {
				t.Fatal(err)
			}
			cm := &ConfigManager{configFile: path}
			if err := cm.loadConfig(); err != nil {
				t.Fatal(err)
			}
			persisted, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			// 配置含布尔默认值，只解码需要断言的六类渠道。
			var fields map[string]json.RawMessage
			if err := json.Unmarshal(persisted, &fields); err != nil {
				t.Fatal(err)
			}
			for _, protocol := range protocols {
				var channels []UpstreamConfig
				if field := fields[protocol]; len(field) != 0 {
					if err := json.Unmarshal(field, &channels); err != nil {
						t.Fatal(err)
					}
				}
				got := make([]int, 0, len(channels))
				for _, channel := range channels {
					got = append(got, channel.Priority)
				}
				if !reflect.DeepEqual(got, tt.want) {
					t.Errorf("%s priorities = %v, want %v", protocol, got, tt.want)
				}
				// 补写只把“未配置回退为数组下标”落成显式值，补写前后的调度顺序必须一致。
				legacy := make([]UpstreamConfig, len(tt.in))
				for i, priority := range tt.in {
					legacy[i].Priority = priority
				}
				loaded := make([]UpstreamConfig, len(channels))
				for i, channel := range channels {
					loaded[i].Priority = channel.Priority
				}
				if order := effectiveChannelOrder(loaded); !reflect.DeepEqual(order, effectiveChannelOrder(legacy)) {
					t.Errorf("%s 顺序在补写后变化: %v -> %v", protocol, effectiveChannelOrder(legacy), order)
				}
			}
			before, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			restarted := &ConfigManager{configFile: path}
			if err := restarted.loadConfig(); err != nil {
				t.Fatal(err)
			}
			after, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if !before.ModTime().Equal(after.ModTime()) {
				t.Error("重启不应再次写入已迁移的配置")
			}
			if restarted.normalizeChannelPriorities() {
				t.Error("优先级迁移应幂等")
			}
			if got, _ := os.ReadFile(path); !bytes.Equal(got, persisted) {
				t.Error("重启后配置内容变化")
			}
		})
	}
}
