package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestAuthoritativeLogicalMetadataRoundTrip 验证 C 阶段的单一持久化真源：
// LogicalChannel 元数据与 Settings 写入 ChannelsV3，磁盘不再重复写 logicalChannels，
// 纯 V3 重载后管理 API 仍能得到完整逻辑渠道视图。
func TestAuthoritativeLogicalMetadataRoundTrip(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	cm, err := NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("创建配置管理器失败: %v", err)
	}

	multiplier := 0.75
	route := UpstreamConfig{
		ChannelUID:        "ch_c_logical",
		LogicalChannelUID: "lc_c_logical",
		LogicalName:       "C 渠道",
		Name:              "C 渠道",
		BaseURL:           "https://c.example.com/v1",
		ServiceType:       "openai",
		Status:            "active",
		APIKeys:           []string{"sk-c"},
		CostMultiplier:    &multiplier,
		ProxyURL:          "http://proxy.example.com:8080",
		CustomHeaders:     map[string]string{"X-Trace": "c"},
	}
	settings := LogicalChannelSettings{
		CostMultiplier: &multiplier,
		ProxyURL:       route.ProxyURL,
		CustomHeaders:  map[string]string{"X-Trace": "c"},
	}
	cm.config.Upstream = []UpstreamConfig{route}
	cm.config.LogicalChannels = []LogicalChannel{{
		LogicalChannelUID: "lc_c_logical",
		Name:              "C 渠道",
		Remark:            "统一保存",
		Description:       "单一权威记录",
		Website:           "https://c.example.com",
		Kind:              LogicalChannelKindLLM,
		BaseURLs:          []string{route.BaseURL},
		SiteIdentity:      SiteIdentityForBaseURL(route.BaseURL),
		Tags:              []string{"phase-c"},
		Settings:          &settings,
	}}
	if err := cm.SaveConfig(); err != nil {
		cm.Close()
		t.Fatalf("保存配置失败: %v", err)
	}
	if err := cm.Close(); err != nil {
		t.Fatalf("关闭配置管理器失败: %v", err)
	}

	raw, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("读取配置失败: %v", err)
	}
	var persisted map[string]json.RawMessage
	if err := json.Unmarshal(raw, &persisted); err != nil {
		t.Fatalf("解析配置失败: %v", err)
	}
	if _, ok := persisted["logicalChannels"]; ok {
		t.Fatal("C 阶段不应重复落盘 logicalChannels")
	}
	var channels []ChannelV3
	if err := json.Unmarshal(persisted["channelsV3"], &channels); err != nil {
		t.Fatalf("解析 channelsV3 失败: %v", err)
	}
	if len(channels) != 1 || channels[0].Remark != "统一保存" || channels[0].Settings == nil {
		t.Fatalf("逻辑元数据未写入 ChannelsV3: %+v", channels)
	}

	cm2, err := NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("纯 V3 重载失败: %v", err)
	}
	defer cm2.Close()
	lc := cm2.GetLogicalChannel("lc_c_logical")
	if lc == nil {
		t.Fatal("纯 V3 重载后应恢复 LogicalChannel")
	}
	if lc.Remark != "统一保存" || lc.Description != "单一权威记录" || lc.Website != "https://c.example.com" {
		t.Fatalf("逻辑渠道元数据重载不完整: %+v", lc)
	}
	if lc.Settings == nil || lc.Settings.ProxyURL != route.ProxyURL || lc.Settings.CostMultiplier == nil || *lc.Settings.CostMultiplier != multiplier {
		t.Fatalf("逻辑渠道 Settings 重载不完整: %+v", lc.Settings)
	}
}
