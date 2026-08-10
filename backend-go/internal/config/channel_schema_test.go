package config

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestRebuildChannels_MirrorAndArraysIntact 验证 RebuildChannels 生成镜像且不改动六数组。
func TestRebuildChannels_MirrorAndArraysIntact(t *testing.T) {
	cfg := &Config{
		Upstream: []UpstreamConfig{
			makeKeyChannel("messages", "ch_claude", "acct_volc", "https://ark.example.com/api/coding", "claude", "sk-secret-aaa", "vip", []string{"claude-x"}),
		},
		ChatUpstream: []UpstreamConfig{
			makeKeyChannel("chat", "ch_chat", "acct_volc", "https://ark.example.com/api/coding", "openai", "sk-secret-aaa", "vip", []string{"gpt-x"}),
		},
	}
	beforeUpstream := len(cfg.Upstream)
	beforeChat := len(cfg.ChatUpstream)
	beforeKey := cfg.Upstream[0].APIKeys[0]

	RebuildChannels(cfg)

	if cfg.ChannelSchemaVersion != ChannelSchemaVersion {
		t.Fatalf("ChannelSchemaVersion = %d, 期望 %d", cfg.ChannelSchemaVersion, ChannelSchemaVersion)
	}
	if len(cfg.Channels) != 1 {
		t.Fatalf("同账号多协议应镜像为 1 个 Channel，实际 %d", len(cfg.Channels))
	}
	if len(cfg.ChannelCapabilities) != 2 {
		t.Fatalf("应有 2 份能力（messages/chat），实际 %d", len(cfg.ChannelCapabilities))
	}
	// 能力按 CapabilityUID 排序（确定性）
	if cfg.ChannelCapabilities[0].CapabilityUID > cfg.ChannelCapabilities[1].CapabilityUID {
		t.Fatal("ChannelCapabilities 应按 CapabilityUID 升序")
	}
	// 六数组不受影响
	if len(cfg.Upstream) != beforeUpstream || len(cfg.ChatUpstream) != beforeChat {
		t.Fatal("RebuildChannels 不应改动六个 Upstream 数组长度")
	}
	if cfg.Upstream[0].APIKeys[0] != beforeKey {
		t.Fatal("RebuildChannels 不应改动物理渠道明文 key")
	}
}

// TestRebuildChannels_JSONRoundTripNoPlaintextKey 验证镜像可 JSON 往返且不含明文 key。
func TestRebuildChannels_JSONRoundTripNoPlaintextKey(t *testing.T) {
	cfg := &Config{
		ChatUpstream: []UpstreamConfig{
			makeKeyChannel("chat", "ch_a", "acct_a", "https://relay.example.com", "openai", "sk-secret-xyz", "vip", []string{"gpt-x"}),
		},
	}
	RebuildChannels(cfg)

	// 只序列化 Channels 镜像本身：它绝不能携带明文 key（仅 KeyMask）。
	// 注意六个 Upstream 数组仍按原样保存明文（config.json 为 0600），不在本断言范围内。
	data, err := json.Marshal(struct {
		Channels     []ChannelView        `json:"channels"`
		Capabilities []EndpointCapability `json:"channelCapabilities"`
	}{cfg.Channels, cfg.ChannelCapabilities})
	if err != nil {
		t.Fatalf("marshal 失败: %v", err)
	}
	if strings.Contains(string(data), "sk-secret-xyz") {
		t.Fatal("Channels 镜像不得包含明文 key")
	}
	if !strings.Contains(string(data), "\"channels\"") {
		t.Fatal("序列化应包含 channels 字段")
	}

	var back struct {
		Channels []ChannelView `json:"channels"`
	}
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatalf("unmarshal 失败: %v", err)
	}
	if len(back.Channels) != 1 || len(back.Channels[0].Keys) != 1 {
		t.Fatalf("往返后应保留 1 channel 1 key，实际 channels=%d", len(back.Channels))
	}
	if len(back.Channels[0].Keys[0].Endpoints) != 1 {
		t.Fatal("往返后应保留 key 的 endpoint 绑定")
	}
	if back.Channels[0].Keys[0].KeyMask == "" {
		t.Fatal("往返后应保留 KeyMask")
	}
}

// TestGetChannelViews_RealtimeSynthesis 验证 ConfigManager.GetChannelViews 实时合成。
func TestGetChannelViews_RealtimeSynthesis(t *testing.T) {
	cm := &ConfigManager{config: Config{
		ChatUpstream: []UpstreamConfig{
			makeKeyChannel("chat", "ch_a", "acct_a", "https://relay.example.com", "openai", "sk-aaa", "vip", []string{"gpt-x"}),
		},
	}}
	views, caps := cm.GetChannelViews()
	if len(views) != 1 {
		t.Fatalf("应实时合成 1 个渠道视图，实际 %d", len(views))
	}
	if len(caps) != 1 {
		t.Fatalf("应有 1 份能力，实际 %d", len(caps))
	}
	if len(views[0].Keys) != 1 || len(views[0].Keys[0].Endpoints) != 1 {
		t.Fatal("视图应含 1 key 1 endpoint")
	}
}
