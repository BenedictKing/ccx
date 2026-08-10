package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAuthoritativeLoad_RoundTripLossless 验证开关开启后，从 ChannelsV3 重建的六数组
// 与 BuildAuthoritativeChannels 输入逐字段一致（round-trip）。
func TestAuthoritativeLoad_RoundTripLossless(t *testing.T) {
	defer withEnv(channelAuthoritativeLoadEnv, "true")()

	mk := func(kind, uid, acct, base, svc, key string, priority int) UpstreamConfig {
		u := makeKeyChannel(kind, uid, acct, base, svc, key, "vip", []string{"m1", "m2"})
		u.Priority = priority
		u.ModelMapping = map[string]string{"a": "b"}
		return u
	}
	cfg := &Config{
		Upstream: []UpstreamConfig{
			mk("messages", "ch_c1", "acct_v", "https://ark.example.com/api/coding", "claude", "sk-a", 1),
			mk("messages", "ch_c2", "acct_w", "https://other.example.com", "claude", "sk-b", 2),
		},
		ChatUpstream: []UpstreamConfig{
			mk("chat", "ch_ch1", "acct_v", "https://ark.example.com/api/coding", "openai", "sk-a", 1),
		},
		ResponsesUpstream: []UpstreamConfig{
			mk("responses", "ch_r1", "acct_v", "https://ark.example.com/api/coding", "openai", "sk-a", 1),
		},
		GeminiUpstream:  []UpstreamConfig{},
		ImagesUpstream:  []UpstreamConfig{},
		VectorsUpstream: []UpstreamConfig{},
	}
	cfg.ChannelAuthoritativeVersion = ChannelV3SchemaVersion
	cfg.ChannelsV3 = BuildAuthoritativeChannels(cfg)

	if applied, err := applyAuthoritativeChannelsAsLoadSource(cfg); err != nil {
		t.Fatalf("不应报错: %v", err)
	} else if !applied {
		t.Fatal("应已应用 ChannelsV3 重建")
	}

	assertUpstreamsEqual(t, "messages", cfg.Upstream, []UpstreamConfig{
		mk("messages", "ch_c1", "acct_v", "https://ark.example.com/api/coding", "claude", "sk-a", 1),
		mk("messages", "ch_c2", "acct_w", "https://other.example.com", "claude", "sk-b", 2),
	})
	assertUpstreamsEqual(t, "chat", cfg.ChatUpstream, []UpstreamConfig{
		mk("chat", "ch_ch1", "acct_v", "https://ark.example.com/api/coding", "openai", "sk-a", 1),
	})
	assertUpstreamsEqual(t, "responses", cfg.ResponsesUpstream, []UpstreamConfig{
		mk("responses", "ch_r1", "acct_v", "https://ark.example.com/api/coding", "openai", "sk-a", 1),
	})
}

// TestAuthoritativeLoad_OrderRestored 验证乱序的 ChannelsV3 成员按 Index 恢复原始 failover 顺序。
func TestAuthoritativeLoad_OrderRestored(t *testing.T) {
	defer withEnv(channelAuthoritativeLoadEnv, "true")()

	cfg := &Config{
		Upstream: []UpstreamConfig{
			makeKeyChannel("messages", "ch_a", "", "https://a.example.com", "claude", "sk-a", "g", nil),
			makeKeyChannel("messages", "ch_b", "", "https://b.example.com", "claude", "sk-b", "g", nil),
			makeKeyChannel("messages", "ch_c", "", "https://c.example.com", "claude", "sk-c", "g", nil),
		},
	}
	cfg.ChannelAuthoritativeVersion = ChannelV3SchemaVersion
	cfg.ChannelsV3 = BuildAuthoritativeChannels(cfg)
	// 打乱 messages 协议成员的 Index 顺序，验证回投影仍按 Index 排序。
	for i := range cfg.ChannelsV3[0].Protocols {
		if cfg.ChannelsV3[0].Protocols[i].Kind == "messages" {
			cfg.ChannelsV3[0].Protocols[i].Index = 2 - cfg.ChannelsV3[0].Protocols[i].Index
		}
	}

	if _, err := applyAuthoritativeChannelsAsLoadSource(cfg); err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if len(cfg.Upstream) != 3 {
		t.Fatalf("应恢复 3 个 messages 渠道，实际 %d", len(cfg.Upstream))
	}
	if cfg.Upstream[0].ChannelUID != "ch_a" || cfg.Upstream[1].ChannelUID != "ch_b" || cfg.Upstream[2].ChannelUID != "ch_c" {
		t.Fatalf("数组内顺序未按原 Index 恢复: %s,%s,%s", cfg.Upstream[0].ChannelUID, cfg.Upstream[1].ChannelUID, cfg.Upstream[2].ChannelUID)
	}
}

// TestAuthoritativeLoad_DivergenceStrictMode 验证不一致且严格模式开启时拒绝启动。
func TestAuthoritativeLoad_DivergenceStrictMode(t *testing.T) {
	defer withEnv(channelAuthoritativeLoadEnv, "true")()
	defer withEnv(channelAuthoritativeStrictEnv, "true")()

	cfg := &Config{
		Upstream: []UpstreamConfig{
			makeKeyChannel("messages", "ch_a", "", "https://a.example.com", "claude", "sk-a", "g", nil),
		},
	}
	cfg.ChannelAuthoritativeVersion = ChannelV3SchemaVersion
	cfg.ChannelsV3 = BuildAuthoritativeChannels(cfg)
	// 篡改磁盘数组，使 ChannelsV3 与六数组不一致。
	cfg.Upstream[0].ChannelUID = "ch_b"

	if _, err := applyAuthoritativeChannelsAsLoadSource(cfg); err == nil {
		t.Fatal("严格模式下应拒绝启动")
	}
}

// TestAuthoritativeLoad_DivergenceNonStrictMode 验证不一致且非严格模式时回退到磁盘六数组。
func TestAuthoritativeLoad_DivergenceNonStrictMode(t *testing.T) {
	defer withEnv(channelAuthoritativeLoadEnv, "true")()
	// 严格模式默认关闭

	original := []UpstreamConfig{
		makeKeyChannel("messages", "ch_a", "", "https://a.example.com", "claude", "sk-a", "g", nil),
	}
	cfg := &Config{Upstream: original}
	cfg.ChannelAuthoritativeVersion = ChannelV3SchemaVersion
	cfg.ChannelsV3 = BuildAuthoritativeChannels(cfg)
	// 重建后把 ChannelsV3 的 ChannelUID 改掉，但保持磁盘数组不变，模拟不一致。
	cfg.ChannelsV3[0].Protocols[0].Upstream.ChannelUID = "ch_b"

	if applied, err := applyAuthoritativeChannelsAsLoadSource(cfg); err != nil {
		t.Fatalf("非严格模式不应报错: %v", err)
	} else if applied {
		t.Fatal("非严格模式不一致时不应应用 ChannelsV3")
	}
	assertUpstreamsEqual(t, "messages", cfg.Upstream, original)
}

// TestAuthoritativeLoad_OldConfigNoChannelsV3 验证不含 ChannelsV3 的旧配置零影响。
func TestAuthoritativeLoad_OldConfigNoChannelsV3(t *testing.T) {
	defer withEnv(channelAuthoritativeLoadEnv, "true")()

	original := []UpstreamConfig{
		makeKeyChannel("messages", "ch_a", "", "https://a.example.com", "claude", "sk-a", "g", nil),
	}
	cfg := &Config{Upstream: original}

	if applied, err := applyAuthoritativeChannelsAsLoadSource(cfg); err != nil {
		t.Fatalf("不应报错: %v", err)
	} else if applied {
		t.Fatal("旧配置不应触发 ChannelsV3 重建")
	}
	assertUpstreamsEqual(t, "messages", cfg.Upstream, original)
}

// TestAuthoritativeLoad_SwitchDisabled 验证开关关闭时行为不变。
func TestAuthoritativeLoad_SwitchDisabled(t *testing.T) {
	defer withEnv(channelAuthoritativeLoadEnv, "false")()

	original := []UpstreamConfig{
		makeKeyChannel("messages", "ch_a", "", "https://a.example.com", "claude", "sk-a", "g", nil),
	}
	cfg := &Config{Upstream: original}
	cfg.ChannelAuthoritativeVersion = ChannelV3SchemaVersion
	cfg.ChannelsV3 = BuildAuthoritativeChannels(cfg)

	if applied, err := applyAuthoritativeChannelsAsLoadSource(cfg); err != nil {
		t.Fatalf("不应报错: %v", err)
	} else if applied {
		t.Fatal("开关关闭时不应应用 ChannelsV3")
	}
	assertUpstreamsEqual(t, "messages", cfg.Upstream, original)
}

// TestAuthoritativeLoad_IntegrationAppliedWhenConsistent 验证当 ChannelsV3 与磁盘六数组
// 完全一致时，加载翻转会成功应用 ChannelsV3。
func TestAuthoritativeLoad_IntegrationAppliedWhenConsistent(t *testing.T) {
	defer withEnv(channelAuthoritativeLoadEnv, "true")()

	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	trueVal := true
	cm, err := NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	cm.config.Upstream = []UpstreamConfig{
		{
			ChannelUID:      "ch_a",
			AccountUID:      "acct_1",
			BaseURL:         "https://a.example.com",
			ServiceType:     "claude",
			Status:          "active",
			APIKeys:         []string{"sk-a"},
			APIKeyConfigs:   []APIKeyConfig{{Key: "sk-a", KeyUID: "ku_ch_a", Enabled: &trueVal}},
			SupportedModels: []string{"m1"},
			Priority:        1,
			OriginType:      "unknown",
			OriginTier:      "unknown",
			AutoManaged:     true,
			AutoManagedKind: "generic",
		},
	}
	if err := cm.SaveConfig(); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}
	cm.CloseWatcher()

	cm2, err := NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	defer cm2.CloseWatcher()

	loaded := cm2.GetConfig()
	if loaded.Upstream[0].BaseURL != "https://a.example.com" {
		t.Fatalf("加载翻转应保留 ChannelsV3 中的 BaseURL，实际 %s", loaded.Upstream[0].BaseURL)
	}
	if loaded.Upstream[0].Priority != 1 {
		t.Fatalf("加载翻转应保留 ChannelsV3 中的 Priority，实际 %d", loaded.Upstream[0].Priority)
	}
}

// TestAuthoritativeLoad_IntegrationViaConfigManager 验证通过 ConfigManager 加载时，
// 开关开启且 ChannelsV3 与磁盘六数组不一致时，非严格模式回退到磁盘六数组。
func TestAuthoritativeLoad_IntegrationViaConfigManager(t *testing.T) {
	defer withEnv(channelAuthoritativeLoadEnv, "true")()

	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	mk := makeKeyChannel
	cm, err := NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	cm.config.Upstream = []UpstreamConfig{
		mk("messages", "ch_a", "acct_1", "https://a.example.com", "claude", "sk-a", "g", []string{"m1"}),
	}
	if err := cm.SaveConfig(); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}
	cm.CloseWatcher()

	// 模拟外部手改：只改磁盘六数组，未同步 ChannelsV3。
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("读取配置失败: %v", err)
	}
	modified := strings.Replace(string(data), "https://a.example.com", "https://modified.example.com", -1)
	if err := os.WriteFile(configPath, []byte(modified), 0644); err != nil {
		t.Fatalf("写入修改后配置失败: %v", err)
	}

	cm2, err := NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	defer cm2.CloseWatcher()

	loaded := cm2.GetConfig()
	// 非严格模式下，不一致时回退到磁盘六数组。
	if loaded.Upstream[0].BaseURL != "https://modified.example.com" {
		t.Fatalf("非严格模式应回退到磁盘六数组，实际 BaseURL=%s", loaded.Upstream[0].BaseURL)
	}
}

// withEnv 临时设置环境变量并在返回的函数中恢复。
func withEnv(key, value string) func() {
	old, had := os.LookupEnv(key)
	os.Setenv(key, value)
	return func() {
		if had {
			os.Setenv(key, old)
		} else {
			os.Unsetenv(key)
		}
	}
}
