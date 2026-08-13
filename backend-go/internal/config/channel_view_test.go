package config

import (
	"testing"
	"time"
)

// makeKeyChannel 构造一个带单 key 的物理渠道。
func makeKeyChannel(kind, channelUID, accountUID, baseURL, serviceType, key, group string, models []string) UpstreamConfig {
	enabled := true
	return UpstreamConfig{
		ChannelUID:  channelUID,
		AccountUID:  accountUID,
		BaseURL:     baseURL,
		ServiceType: serviceType,
		Status:      "active",
		APIKeys:     []string{key},
		APIKeyConfigs: []APIKeyConfig{{
			Key:        key,
			KeyUID:     "ku_" + channelUID,
			QuotaGroup: group,
			Enabled:    &enabled,
		}},
		SupportedModels: models,
	}
}

func findCapForProtocol(view ChannelView, caps map[string]EndpointCapability, keyHash, protocol string) (EndpointCapability, bool) {
	for _, k := range view.Keys {
		if k.KeyHash != keyHash {
			continue
		}
		for _, e := range k.Endpoints {
			if e.Protocol == protocol {
				if c, ok := caps[e.CapabilityUID]; ok {
					return c, true
				}
			}
		}
	}
	return EndpointCapability{}, false
}

// TestBuildChannelViews_MultiProtocolSameAccount 验证同账号多协议收敛为一个 ChannelView。
func TestBuildChannelViews_MultiProtocolSameAccount(t *testing.T) {
	cfg := &Config{
		Upstream: []UpstreamConfig{
			makeKeyChannel("messages", "ch_claude", "acct_volc", "https://ark.example.com/api/coding", "claude", "sk-a", "vip", []string{"claude-x"}),
		},
		ChatUpstream: []UpstreamConfig{
			makeKeyChannel("chat", "ch_chat", "acct_volc", "https://ark.example.com/api/coding", "openai", "sk-a", "vip", []string{"gpt-x"}),
		},
	}

	views, caps := BuildChannelViews(cfg)
	if len(views) != 1 {
		t.Fatalf("同账号多协议应收敛为 1 个 ChannelView，实际 %d", len(views))
	}
	v := views[0]
	if v.AccountUID != "acct_volc" {
		t.Fatalf("AccountUID = %q, 期望 acct_volc", v.AccountUID)
	}
	if len(v.Protocols) != 2 {
		t.Fatalf("应有 2 个协议 facade，实际 %d", len(v.Protocols))
	}
	// 同一明文 key 在两个协议下合并为一个 ChannelKeyView
	if len(v.Keys) != 1 {
		t.Fatalf("同一 key 应合并为 1 个 ChannelKeyView，实际 %d", len(v.Keys))
	}
	if len(v.Keys[0].Endpoints) != 2 {
		t.Fatalf("该 key 应有 2 个 (baseURL,协议) endpoint，实际 %d", len(v.Keys[0].Endpoints))
	}
	if len(caps) != 2 {
		t.Fatalf("应注册 2 份能力（messages/chat 各一），实际 %d", len(caps))
	}
}

// TestBuildChannelViews_CrossAccountSameGroupSharesCapability 验证不同账号同站点同分组共享能力。
func TestBuildChannelViews_CrossAccountSameGroupSharesCapability(t *testing.T) {
	cfg := &Config{
		ChatUpstream: []UpstreamConfig{
			makeKeyChannel("chat", "ch_a", "acct_a", "https://relay.example.com", "openai", "sk-aaa", "vip", []string{"gpt-x"}),
			makeKeyChannel("chat", "ch_b", "acct_b", "https://relay.example.com", "openai", "sk-bbb", "vip", []string{"gpt-x"}),
		},
	}

	views, caps := BuildChannelViews(cfg)
	if len(views) != 2 {
		t.Fatalf("不同账号应产生 2 个 ChannelView，实际 %d", len(views))
	}

	capA, okA := findCapForProtocol(views[0], caps, ChannelKeyHash("sk-aaa"), "chat")
	capB, okB := findCapForProtocol(views[1], caps, ChannelKeyHash("sk-bbb"), "chat")
	if !okA || !okB {
		t.Fatalf("两账号 key 均应绑定能力: A=%v B=%v", okA, okB)
	}
	if capA.CapabilityUID != capB.CapabilityUID {
		t.Fatalf("同站点同分组应共享同一 CapabilityUID，实际 A=%s B=%s", capA.CapabilityUID, capB.CapabilityUID)
	}
}

// TestBuildChannelViews_DifferentGroupIsolatesCapability 验证不同分组不共享能力。
func TestBuildChannelViews_DifferentGroupIsolatesCapability(t *testing.T) {
	cfg := &Config{
		ChatUpstream: []UpstreamConfig{
			makeKeyChannel("chat", "ch_a", "acct_a", "https://relay.example.com", "openai", "sk-aaa", "vip", nil),
			makeKeyChannel("chat", "ch_b", "acct_b", "https://relay.example.com", "openai", "sk-bbb", "default", nil),
		},
	}
	_, caps := BuildChannelViews(cfg)
	if len(caps) != 2 {
		t.Fatalf("不同分组应产生 2 份独立能力，实际 %d", len(caps))
	}
}

// TestBuildChannelViews_KeyEnabledReflectsDisabled 验证禁用 key 状态。
func TestBuildChannelViews_KeyEnabledReflectsDisabled(t *testing.T) {
	disabled := false
	ch := makeKeyChannel("chat", "ch_a", "acct_a", "https://relay.example.com", "openai", "sk-aaa", "vip", nil)
	ch.APIKeyConfigs[0].Enabled = &disabled
	cfg := &Config{ChatUpstream: []UpstreamConfig{ch}}

	views, _ := BuildChannelViews(cfg)
	if len(views) != 1 || len(views[0].Keys) != 1 {
		t.Fatalf("应有 1 view 1 key")
	}
	if views[0].Keys[0].Enabled {
		t.Fatalf("Enabled=false 的 key 应标记为不可用")
	}
	_ = time.Now
}

// TestMigrateStaleChannelViewNames_ResetsOldDerivedName 验证旧派生规则写下的
// vip-lyclaude-site-claude 在镜像里被改写为新规则派生值 vip-lyclaude，
// 且旧名进 Remark，ChannelsV3 同步。
func TestMigrateStaleChannelViewNames_ResetsOldDerivedName(t *testing.T) {
	cfg := &Config{
		ChannelsV3: []ChannelV3{{
			ChannelUID:   "lc_test",
			SiteIdentity: "https://vip.lyclaude.site",
			Name:         "vip-lyclaude-site-claude",
		}},
		Channels: []ChannelView{{
			ChannelUID:   "lc_test",
			SiteIdentity: "https://vip.lyclaude.site",
			BaseURLs:     []string{"https://vip.lyclaude.site"},
			Name:         "vip-lyclaude-site-claude",
		}},
	}

	if !migrateStaleChannelViewNames(cfg) {
		t.Fatalf("期望发生改名，实际未触发")
	}

	if got := cfg.Channels[0].Name; got != "vip-lyclaude" {
		t.Errorf("Channels[0].Name = %q, want %q", got, "vip-lyclaude")
	}
	if got := cfg.Channels[0].Remark; got != "vip-lyclau" {
		t.Errorf("Channels[0].Remark = %q, want %q (10 字符截断)", got, "vip-lyclau")
	}
	if got := cfg.ChannelsV3[0].Name; got != "vip-lyclaude" {
		t.Errorf("ChannelsV3[0].Name = %q, want %q", got, "vip-lyclaude")
	}
}

// TestMigrateStaleChannelViewNames_KeepsAlreadyCorrect 验证已经是新规则
// 派生名时不做改动、不写 Remark、ChannelsV3 也不动。
func TestMigrateStaleChannelViewNames_KeepsAlreadyCorrect(t *testing.T) {
	cfg := &Config{
		ChannelsV3: []ChannelV3{{
			ChannelUID: "lc_ok",
			Name:       "api-openai",
		}},
		Channels: []ChannelView{{
			ChannelUID: "lc_ok",
			BaseURLs:   []string{"https://api.openai.com/v1"},
			Name:       "api-openai",
		}},
	}

	if migrateStaleChannelViewNames(cfg) {
		t.Fatalf("不应发生改名")
	}
	if cfg.Channels[0].Remark != "" {
		t.Errorf("Remark 仍应为空, got %q", cfg.Channels[0].Remark)
	}
	if cfg.ChannelsV3[0].Name != "api-openai" {
		t.Errorf("ChannelsV3[0].Name 仍应为 api-openai, got %q", cfg.ChannelsV3[0].Name)
	}
}

// TestMigrateStaleChannelViewNames_PreservesExistingRemark 验证已有 Remark 时不被覆盖。
func TestMigrateStaleChannelViewNames_PreservesExistingRemark(t *testing.T) {
	cfg := &Config{
		ChannelsV3: []ChannelV3{{
			ChannelUID: "lc_remark",
			Name:       "vip-lyclaude-site-claude",
		}},
		Channels: []ChannelView{{
			ChannelUID: "lc_remark",
			BaseURLs:   []string{"https://vip.lyclaude.site"},
			Name:       "vip-lyclaude-site-claude",
			Remark:     "人工备注",
		}},
	}

	if !migrateStaleChannelViewNames(cfg) {
		t.Fatalf("期望发生改名")
	}
	if cfg.Channels[0].Remark != "人工备注" {
		t.Errorf("Remark 不应被覆盖, got %q", cfg.Channels[0].Remark)
	}
	if cfg.Channels[0].Name != "vip-lyclaude" {
		t.Errorf("Name 应改为 vip-lyclaude, got %q", cfg.Channels[0].Name)
	}
}

// TestMigrateStaleChannelViewNames_EmptyChannelsIsNoop 验证 Channels 为空时安全 no-op。
func TestMigrateStaleChannelViewNames_EmptyChannelsIsNoop(t *testing.T) {
	cfg := &Config{
		ChannelsV3: []ChannelV3{{ChannelUID: "lc_x", Name: "vip-lyclaude-site-claude"}},
	}
	if migrateStaleChannelViewNames(cfg) {
		t.Fatalf("Channels 为空应直接 return false")
	}
	if cfg.ChannelsV3[0].Name != "vip-lyclaude-site-claude" {
		t.Errorf("ChannelsV3 不应被改动, got %q", cfg.ChannelsV3[0].Name)
	}
}
