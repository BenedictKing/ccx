package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// TestBlacklistKey_CascadesToSiblingProtocols 验证同账号跨协议的相同 key 被级联拉黑。
func TestBlacklistKey_CascadesToSiblingProtocols(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	cfg := Config{
		Upstream: []UpstreamConfig{{
			ChannelUID: "ch_claude", AccountUID: "acct_volc", Name: "volc-claude",
			BaseURL: "https://ark.example.com/api/coding", ServiceType: "claude",
			Status: "active", APIKeys: []string{"sk-shared", "sk-other"},
		}},
		ChatUpstream: []UpstreamConfig{{
			ChannelUID: "ch_chat", AccountUID: "acct_volc", Name: "volc-chat",
			BaseURL: "https://ark.example.com/api/coding", ServiceType: "openai",
			Status: "active", APIKeys: []string{"sk-shared"},
		}},
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}

	cm, err := NewConfigManager(configPath, filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatalf("NewConfigManager: %v", err)
	}

	if err := cm.BlacklistKey("Messages", 0, "sk-shared", "auth_error", "invalid key"); err != nil {
		t.Fatalf("BlacklistKey: %v", err)
	}

	got := cm.GetConfig()
	// messages 渠道：sk-shared 被移出活跃、进入禁用
	if slices.Contains(got.Upstream[0].APIKeys, "sk-shared") {
		t.Fatal("messages 渠道的 sk-shared 应已被移出活跃列表")
	}
	if !slices.Contains(got.Upstream[0].APIKeys, "sk-other") {
		t.Fatal("messages 渠道的 sk-other 不应受影响")
	}
	// chat 渠道（同账号兄弟）：sk-shared 应被级联拉黑
	if slices.Contains(got.ChatUpstream[0].APIKeys, "sk-shared") {
		t.Fatal("chat 渠道的同一 key 应被级联拉黑（跨协议）")
	}
	if !slices.ContainsFunc(got.ChatUpstream[0].DisabledAPIKeys, func(d DisabledKeyInfo) bool { return d.Key == "sk-shared" }) {
		t.Fatal("chat 渠道应有 sk-shared 的禁用记录")
	}
}

// TestBlacklistKey_NoCrossAccountLeak 验证不同账号同 key 不被误级联。
func TestBlacklistKey_NoCrossAccountLeak(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")

	cfg := Config{
		ChatUpstream: []UpstreamConfig{
			{ChannelUID: "ch_a", AccountUID: "acct_a", Name: "a", BaseURL: "https://relay-a.example.com", ServiceType: "openai", Status: "active", APIKeys: []string{"sk-x"}},
			{ChannelUID: "ch_b", AccountUID: "acct_b", Name: "b", BaseURL: "https://relay-b.example.com", ServiceType: "openai", Status: "active", APIKeys: []string{"sk-x"}},
		},
	}
	data, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(configPath, data, 0600); err != nil {
		t.Fatal(err)
	}
	cm, err := NewConfigManager(configPath, filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatalf("NewConfigManager: %v", err)
	}

	if err := cm.BlacklistKey("Chat", 0, "sk-x", "auth_error", "bad"); err != nil {
		t.Fatalf("BlacklistKey: %v", err)
	}
	got := cm.GetConfig()
	if slices.Contains(got.ChatUpstream[0].APIKeys, "sk-x") {
		t.Fatal("acct_a 的 sk-x 应被拉黑")
	}
	// acct_b 是不同账号，同名 key 不应被级联
	if !slices.Contains(got.ChatUpstream[1].APIKeys, "sk-x") {
		t.Fatal("不同账号 acct_b 的同名 key 不应被误级联拉黑")
	}
}
