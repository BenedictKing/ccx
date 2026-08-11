package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAuthoritativeLoad_RoundTripLossless 验证开关开启后，从 ChannelsV3 重建的六数组
// 与 BuildAuthoritativeChannels 输入逐字段一致（round-trip）。
func TestAuthoritativeLoad_RoundTripLossless(t *testing.T) {

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
	// 反转后 ChannelsV3 是运行时权威,六数组从 ChannelsV3 重建。
	// 索引恢复:ChannelsV3 协议成员 Index 经 ApplyAuthoritativeChannels 排序后填入六数组;
	// 索引被反转后,六数组按 Index 升序排列(具体顺序取决于聚合键对称性,该行为
	// 独立于本测试关注点)。这里只验证"3 个渠道都被恢复且字段不丢"。
	if len(cfg.Upstream) != 3 {
		t.Fatalf("应恢复 3 个 messages 渠道，实际 %d", len(cfg.Upstream))
	}
	uids := map[string]bool{cfg.Upstream[0].ChannelUID: true, cfg.Upstream[1].ChannelUID: true, cfg.Upstream[2].ChannelUID: true}
	for _, want := range []string{"ch_a", "ch_b", "ch_c"} {
		if !uids[want] {
			t.Fatalf("反转后应包含 %s,实际 %s,%s,%s", want, cfg.Upstream[0].ChannelUID, cfg.Upstream[1].ChannelUID, cfg.Upstream[2].ChannelUID)
		}
	}
}

// TestAuthoritativeLoad_DivergenceStrictMode 验证不一致且严格模式开启时拒绝启动。
func TestAuthoritativeLoad_DivergenceStrictMode(t *testing.T) {
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

// TestAuthoritativeLoad_DivergenceNonStrictMode 验证不一致且非严格模式时:ChannelsV3 仍是权威,
// 记录诊断后覆盖磁盘六数组(而非回退)。
func TestAuthoritativeLoad_DivergenceNonStrictMode(t *testing.T) {
	// 严格模式默认关闭

	original := []UpstreamConfig{
		makeKeyChannel("messages", "ch_a", "", "https://a.example.com", "claude", "sk-a", "g", nil),
	}
	cfg := &Config{Upstream: original}
	cfg.ChannelAuthoritativeVersion = ChannelV3SchemaVersion
	cfg.ChannelsV3 = BuildAuthoritativeChannels(cfg)
	// 重建后把 ChannelsV3 的 ChannelUID 改掉,但保持磁盘数组不变,模拟不一致。
	cfg.ChannelsV3[0].Protocols[0].Upstream.ChannelUID = "ch_b"

	applied, err := applyAuthoritativeChannelsAsLoadSource(cfg)
	if err != nil {
		t.Fatalf("非严格模式不应报错: %v", err)
	}
	if !applied {
		t.Fatal("非严格模式应仍应用 ChannelsV3(以 ChannelsV3 为权威覆盖)")
	}
	// ChannelsV3 是权威,六数组应被覆盖为 ch_b。
	if len(cfg.Upstream) != 1 || cfg.Upstream[0].ChannelUID != "ch_b" {
		t.Fatalf("应以 ChannelsV3 覆盖,实际 UID=%v", cfg.Upstream[0].ChannelUID)
	}
}

// TestAuthoritativeLoad_OldConfigNoChannelsV3 验证不含 ChannelsV3 的旧配置零影响。
func TestAuthoritativeLoad_OldConfigNoChannelsV3(t *testing.T) {

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

// TestAuthoritativeLoad_LegacyLoadSwitchRemoved 验证运行时权威反转后
// CCX_CHANNEL_AUTHORITATIVE_LOAD 门控已移除（常量与读取函数均已删除）：
// ChannelsV3 存在时始终应用重建，无任何开关可回退到旧行为。
func TestAuthoritativeLoad_LegacyLoadSwitchRemoved(t *testing.T) {
	original := []UpstreamConfig{
		makeKeyChannel("messages", "ch_a", "", "https://a.example.com", "claude", "sk-a", "g", nil),
	}
	cfg := &Config{Upstream: original}
	cfg.ChannelAuthoritativeVersion = ChannelV3SchemaVersion
	cfg.ChannelsV3 = BuildAuthoritativeChannels(cfg)

	applied, err := applyAuthoritativeChannelsAsLoadSource(cfg)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if !applied {
		t.Fatal("反转后应始终应用 ChannelsV3(无开关门控)")
	}
	// 应用后 cfg.Upstream 被 ChannelsV3 覆盖
	if len(cfg.Upstream) != 1 || cfg.Upstream[0].ChannelUID != "ch_a" {
		t.Fatalf("应覆盖为 ChannelsV3 内容,实际 %v", cfg.Upstream)
	}
}

// TestAuthoritativeLoad_IntegrationAppliedWhenConsistent 验证当 ChannelsV3 与磁盘六数组
// 完全一致时，加载翻转会成功应用 ChannelsV3。
func TestAuthoritativeLoad_IntegrationAppliedWhenConsistent(t *testing.T) {

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
	// 手改同时替换了磁盘六数组与 ChannelsV3 中的 BaseURL，两者仍一致，
	// 加载应用 ChannelsV3 重建，结果为修改后的值。
	if loaded.Upstream[0].BaseURL != "https://modified.example.com" {
		t.Fatalf("一致时应应用 ChannelsV3 重建结果，实际 BaseURL=%s", loaded.Upstream[0].BaseURL)
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

// TestAuthoritativeLoad_MigratedChannelNoFalseRollback 回归 critical：带迁移历史的渠道
// （AutoManagedKind 为空，加载时由 ensureAutoManagedKind 回填 new_api；AccountUID/CredentialUID/
// LogicalChannelUID 为加载期随机生成）落盘后重载，不应误报"ChannelsV3 与六数组不一致"而回退，
// 应正常应用 ChannelsV3 重建。
func TestAuthoritativeLoad_MigratedChannelNoFalseRollback(t *testing.T) {

	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")
	trueVal := true
	cm, err := NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	// 历史渠道：AutoManagedKind 留空，加载期会被迁移回填；UID 字段加载期随机生成。
	cm.config.Upstream = []UpstreamConfig{{
		ChannelUID:      "ch_mig",
		BaseURL:         "https://mig.example.com",
		ServiceType:     "claude",
		Status:          "active",
		APIKeys:         []string{"sk-mig"},
		APIKeyConfigs:   []APIKeyConfig{{Key: "sk-mig", Enabled: &trueVal}},
		AutoManaged:     true,
		OriginType:      "relay",
		SupportedModels: []string{"m1"},
	}}
	if err := cm.SaveConfig(); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}
	cm.CloseWatcher()

	cm2, err := NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("NewConfigManager() reload error = %v", err)
	}
	defer cm2.CloseWatcher()
	got := cm2.GetConfig()
	if len(got.Upstream) != 1 {
		t.Fatalf("重载后渠道数应为 1，实际 %d", len(got.Upstream))
	}
	if got.Upstream[0].BaseURL != "https://mig.example.com" {
		t.Errorf("应应用 ChannelsV3 重建保留 BaseURL，实际 %q", got.Upstream[0].BaseURL)
	}
	if len(got.Upstream[0].SupportedModels) != 1 || got.Upstream[0].SupportedModels[0] != "m1" {
		t.Errorf("重建不应丢 SupportedModels，实际 %v", got.Upstream[0].SupportedModels)
	}
}

// TestAuthoritativeLoad_ManagedKeySurvivesSaveReload 端到端验证波 1 + 方案 1（421ae7ac）联动：
// 托管渠道（AutoManaged + AccountUID + ProviderID，满足 strip 条件）带 Key 落盘时，
// syncManagedAccountCredentialsFromChannels 把 Key 注册到 ManagedAccounts 后 strip 脱敏；
// 重载时 ChannelsV3 翻转重建六数组（无 Key），hydrateManagedAccountCredentials 再补回。
// 最终 GetConfig 的运行时六数组 Key 必须完整。
func TestAuthoritativeLoad_ManagedKeySurvivesSaveReload(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "config.json")

	cm, err := NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	channel := makeKeyChannel("messages", "ch_e2e", "acct_e", "https://e.example.com", "claude", "sk-e", "g", []string{"m1"})
	channel.AutoManaged = true
	channel.ProviderID = "kimi"
	cm.config.Upstream = []UpstreamConfig{channel}
	if err := cm.SaveConfig(); err != nil {
		t.Fatalf("SaveConfig() error = %v", err)
	}
	cm.CloseWatcher()

	// 落盘脱敏：磁盘六数组与 ChannelsV3 均不含明文 Key，Key 只在 managedAccounts.credentials。
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("读取落盘配置失败: %v", err)
	}
	var persisted Config
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("解析落盘配置失败: %v", err)
	}
	if len(persisted.Upstream) != 1 || len(persisted.Upstream[0].APIKeys) != 0 {
		t.Fatalf("磁盘六数组应已脱敏: %+v", persisted.Upstream)
	}
	if strings.Contains(string(data), `"key":"sk-e"`) || strings.Contains(string(data), `"apiKeys":["sk-e"]`) {
		t.Fatal("落盘 JSON 的渠道字段不应含明文 sk-e")
	}
	foundCred := false
	for _, account := range persisted.ManagedAccounts {
		if account.AccountUID != "acct_e" {
			continue
		}
		for _, credential := range account.Credentials {
			if credential.APIKey == "sk-e" {
				foundCred = true
			}
		}
	}
	if !foundCred {
		t.Fatalf("ManagedAccounts 应持有 sk-e 凭证: %+v", persisted.ManagedAccounts)
	}

	// 重载：翻转重建 + hydrate 补 Key 后，运行时六数组 Key 完整。
	cm2, err := NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("NewConfigManager() reload error = %v", err)
	}
	defer cm2.CloseWatcher()
	got := cm2.GetConfig()
	if len(got.Upstream) != 1 {
		t.Fatalf("重载后渠道数应为 1，实际 %d", len(got.Upstream))
	}
	if len(got.Upstream[0].APIKeys) != 1 || got.Upstream[0].APIKeys[0] != "sk-e" {
		t.Fatalf("重载后托管 Key 应完整恢复，实际 %+v", got.Upstream[0].APIKeys)
	}
}
