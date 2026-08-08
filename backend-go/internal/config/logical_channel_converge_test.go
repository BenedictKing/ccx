package config

import "testing"

// mkPhys 构造一条已托管的物理渠道（带账号与逻辑身份字段）。
func mkPhys(acc, uid, luid, name, serviceType, status string) UpstreamConfig {
	return UpstreamConfig{
		AccountUID:        acc,
		ChannelUID:        uid,
		LogicalChannelUID: luid,
		LogicalName:       name,
		ProviderID:        "",
		Name:              name,
		ServiceType:       serviceType,
		Status:            status,
	}
}

func protocolKinds(l LogicalChannel) []string {
	out := make([]string, 0, len(l.Protocols))
	for _, p := range l.Protocols {
		out = append(out, p.Kind)
	}
	return out
}

func findLogical(cfg *Config, uid string) *LogicalChannel {
	for i := range cfg.LogicalChannels {
		if cfg.LogicalChannels[i].LogicalChannelUID == uid {
			return &cfg.LogicalChannels[i]
		}
	}
	return nil
}

// TestRebuildConvergesSameAccountIntoSingleCard 复现火山方舟式缺陷：
// 同一账号的 messages 与 chat 渠道被历史缺陷分别回填了两个不同的 LogicalChannelUID，
// 归组必须把它们收敛回单一逻辑卡。
func TestRebuildConvergesSameAccountIntoSingleCard(t *testing.T) {
	acc := "acct_volc"
	cfg := &Config{
		LogicalChannelSchemaVersion: LogicalChannelSchemaVersion,
		Upstream: []UpstreamConfig{
			withProvider(mkPhys(acc, "ch_msg", "lc_a", "volc-claude", "claude", "active"), "volc"),
		},
		ChatUpstream: []UpstreamConfig{
			withProvider(mkPhys(acc, "ch_chat", "lc_b", "volc-chat", "openai", "active"), "volc"),
		},
		ResponsesUpstream: []UpstreamConfig{
			withProvider(mkPhys(acc, "ch_resp", "lc_b", "volc-chat", "openai", "active"), "volc"),
		},
		LogicalChannels: []LogicalChannel{
			{LogicalChannelUID: "lc_a", Name: "volc-claude", AccountUID: acc, ProviderID: "volc", Kind: LogicalChannelKindLLM},
			{LogicalChannelUID: "lc_b", Name: "volc-chat", AccountUID: acc, ProviderID: "volc", Kind: LogicalChannelKindLLM},
		},
	}

	RebuildLogicalChannels(cfg)

	if len(cfg.LogicalChannels) != 1 {
		t.Fatalf("同账号应收敛为 1 张逻辑卡，实际 %d 张: %+v", len(cfg.LogicalChannels), cfg.LogicalChannels)
	}
	card := cfg.LogicalChannels[0]
	if card.LogicalChannelUID != "lc_a" {
		t.Fatalf("canonical 应为首个遇到的 lc_a，实际 %q", card.LogicalChannelUID)
	}
	got := map[string]bool{}
	for _, k := range protocolKinds(card) {
		got[k] = true
	}
	for _, want := range []string{"messages", "chat", "responses"} {
		if !got[want] {
			t.Fatalf("收敛后缺少协议 %q，实际 %v", want, protocolKinds(card))
		}
	}
	// 物理渠道的 LogicalChannelUID 必须被强制重指到 canonical
	for _, ch := range cfg.ChatUpstream {
		if ch.LogicalChannelUID != "lc_a" {
			t.Fatalf("chat 渠道 LogicalChannelUID 未重指: %q", ch.LogicalChannelUID)
		}
	}
	for _, ch := range cfg.ResponsesUpstream {
		if ch.LogicalChannelUID != "lc_a" {
			t.Fatalf("responses 渠道 LogicalChannelUID 未重指: %q", ch.LogicalChannelUID)
		}
	}
}

// TestRebuildDropsOrphanCardWithDuplicatedProtocol 复现一叶知秋式缺陷：
// 同账号出现两张同名卡，且同一 messages 渠道被两张卡同时引用（孤儿卡）。
// 归组必须保留单卡、剔除孤儿卡，并把误挂到 claude 卡的 gemini 协议并入。
func TestRebuildDropsOrphanCardWithDuplicatedProtocol(t *testing.T) {
	acc := "acct_yiye"
	cfg := &Config{
		LogicalChannelSchemaVersion: LogicalChannelSchemaVersion,
		Upstream: []UpstreamConfig{
			mkPhys(acc, "ch_msg", "lc_d1c7", "yiye-claude", "claude", "suspended"),
		},
		ChatUpstream: []UpstreamConfig{
			mkPhys(acc, "ch_chat", "lc_e8a0", "yiye-chat", "openai", "active"),
		},
		GeminiUpstream: []UpstreamConfig{
			mkPhys(acc, "ch_gem", "lc_d1c7", "yiye-claude", "gemini", "disabled"),
		},
		LogicalChannels: []LogicalChannel{
			{LogicalChannelUID: "lc_d1c7", Name: "yiye-claude", AccountUID: acc, Kind: LogicalChannelKindLLM},
			{LogicalChannelUID: "lc_e8a0", Name: "yiye-chat", AccountUID: acc, Kind: LogicalChannelKindImages},
			// 孤儿卡：与 lc_d1c7 同名、同样引用 messages 渠道
			{LogicalChannelUID: "lc_ef40", Name: "yiye-claude", AccountUID: acc, Kind: LogicalChannelKindLLM},
		},
	}

	RebuildLogicalChannels(cfg)

	if len(cfg.LogicalChannels) != 1 {
		t.Fatalf("应收敛为 1 张卡并剔除孤儿卡，实际 %d 张", len(cfg.LogicalChannels))
	}
	if findLogical(cfg, "lc_ef40") != nil {
		t.Fatal("孤儿卡 lc_ef40 应被剔除")
	}
	card := cfg.LogicalChannels[0]
	got := map[string]bool{}
	for _, k := range protocolKinds(card) {
		got[k] = true
	}
	for _, want := range []string{"messages", "chat", "gemini"} {
		if !got[want] {
			t.Fatalf("收敛后缺少协议 %q，实际 %v", want, protocolKinds(card))
		}
	}
}

// TestRebuildConvergenceIsIdempotent 二次 Rebuild 不应再产生变化。
func TestRebuildConvergenceIsIdempotent(t *testing.T) {
	acc := "acct_idem"
	cfg := &Config{
		LogicalChannelSchemaVersion: LogicalChannelSchemaVersion,
		Upstream: []UpstreamConfig{
			mkPhys(acc, "ch_msg", "lc_a", "n-claude", "claude", "active"),
		},
		ChatUpstream: []UpstreamConfig{
			mkPhys(acc, "ch_chat", "lc_b", "n-chat", "openai", "active"),
		},
		LogicalChannels: []LogicalChannel{
			{LogicalChannelUID: "lc_a", Name: "n-claude", AccountUID: acc, Kind: LogicalChannelKindLLM},
			{LogicalChannelUID: "lc_b", Name: "n-chat", AccountUID: acc, Kind: LogicalChannelKindLLM},
		},
	}

	RebuildLogicalChannels(cfg)
	first := len(cfg.LogicalChannels)
	firstUIDs := map[string]int{}
	for _, l := range cfg.LogicalChannels {
		firstUIDs[l.LogicalChannelUID] = len(l.Protocols)
	}

	RebuildLogicalChannels(cfg)
	if len(cfg.LogicalChannels) != first {
		t.Fatalf("二次 Rebuild 卡数变化: %d -> %d", first, len(cfg.LogicalChannels))
	}
	for _, l := range cfg.LogicalChannels {
		if n, ok := firstUIDs[l.LogicalChannelUID]; !ok || n != len(l.Protocols) {
			t.Fatalf("二次 Rebuild 卡 %q 协议数变化", l.LogicalChannelUID)
		}
	}
}

// TestRebuildLeavesDifferentAccountsUntouched 不同账号不得被误合并。
func TestRebuildLeavesDifferentAccountsUntouched(t *testing.T) {
	cfg := &Config{
		LogicalChannelSchemaVersion: LogicalChannelSchemaVersion,
		Upstream: []UpstreamConfig{
			mkPhys("acct_1", "ch_1", "lc_1", "a-claude", "claude", "active"),
			mkPhys("acct_2", "ch_2", "lc_2", "b-claude", "claude", "active"),
		},
		LogicalChannels: []LogicalChannel{
			{LogicalChannelUID: "lc_1", Name: "a-claude", AccountUID: "acct_1", Kind: LogicalChannelKindLLM},
			{LogicalChannelUID: "lc_2", Name: "b-claude", AccountUID: "acct_2", Kind: LogicalChannelKindLLM},
		},
	}

	RebuildLogicalChannels(cfg)

	if len(cfg.LogicalChannels) != 2 {
		t.Fatalf("不同账号不应合并，实际 %d 张", len(cfg.LogicalChannels))
	}
}

func withProvider(ch UpstreamConfig, providerID string) UpstreamConfig {
	ch.ProviderID = providerID
	return ch
}
