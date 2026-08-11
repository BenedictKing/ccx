package config

import (
	"encoding/json"
	"testing"
)

// TestAuthoritativeChannels_RoundTripLossless 验证六数组 → ChannelV3 → 六数组无损往返。
func TestAuthoritativeChannels_RoundTripLossless(t *testing.T) {
	mk := func(kind, uid, acct, base, svc, key string, priority int, policy KeyConsumptionPolicy) UpstreamConfig {
		u := makeKeyChannel(kind, uid, acct, base, svc, key, "vip", []string{"m1", "m2"})
		u.Priority = priority
		u.ModelMapping = map[string]string{"a": "b"}
		u.APIKeyConfigs = []APIKeyConfig{
			{Key: key, KeyUID: "kid-" + uid, ConsumptionPolicy: policy},
		}
		return u
	}
	cfg := &Config{
		Upstream: []UpstreamConfig{
			mk("messages", "ch_c1", "acct_v", "https://ark.example.com/api/coding", "claude", "sk-a", 1, KeyConsumptionOpportunistic),
			mk("messages", "ch_c2", "acct_w", "https://other.example.com", "claude", "sk-b", 2, KeyConsumptionNormal),
		},
		ChatUpstream: []UpstreamConfig{
			mk("chat", "ch_ch1", "acct_v", "https://ark.example.com/api/coding", "openai", "sk-a", 1, KeyConsumptionOpportunistic),
		},
		ResponsesUpstream: []UpstreamConfig{
			mk("responses", "ch_r1", "acct_v", "https://ark.example.com/api/coding", "openai", "sk-a", 1, KeyConsumptionOpportunistic),
		},
	}

	// 对 opportunistic 配置做一次 clone 模拟加载/落盘中的规范化，确保 round-trip 不丢。
	for i := range cfg.Upstream {
		for j := range cfg.Upstream[i].APIKeyConfigs {
			cfg.Upstream[i].APIKeyConfigs[j].ConsumptionPolicy = NormalizeKeyConsumptionPolicy(cfg.Upstream[i].APIKeyConfigs[j].ConsumptionPolicy)
		}
	}
	for i := range cfg.ChatUpstream {
		for j := range cfg.ChatUpstream[i].APIKeyConfigs {
			cfg.ChatUpstream[i].APIKeyConfigs[j].ConsumptionPolicy = NormalizeKeyConsumptionPolicy(cfg.ChatUpstream[i].APIKeyConfigs[j].ConsumptionPolicy)
		}
	}
	for i := range cfg.ResponsesUpstream {
		for j := range cfg.ResponsesUpstream[i].APIKeyConfigs {
			cfg.ResponsesUpstream[i].APIKeyConfigs[j].ConsumptionPolicy = NormalizeKeyConsumptionPolicy(cfg.ResponsesUpstream[i].APIKeyConfigs[j].ConsumptionPolicy)
		}
	}

	channels := BuildAuthoritativeChannels(cfg)
	// acct_v 的 messages/chat/responses 应聚合为一个渠道（3 协议），acct_w 独立。
	if len(channels) != 2 {
		t.Fatalf("应聚合为 2 个权威渠道，实际 %d", len(channels))
	}

	up, chat, resp, gem, img, vec := ApplyAuthoritativeChannels(channels)

	assertUpstreamsEqual(t, "messages", cfg.Upstream, up)
	assertUpstreamsEqual(t, "chat", cfg.ChatUpstream, chat)
	assertUpstreamsEqual(t, "responses", cfg.ResponsesUpstream, resp)
	if len(gem) != 0 || len(img) != 0 || len(vec) != 0 {
		t.Fatalf("空数组回投影仍应为空: gem=%d img=%d vec=%d", len(gem), len(img), len(vec))
	}

	// 验证 opportunistic 配置在 V3 往返中不丢失。
	v0 := channels[0].Protocols[0].Upstream.APIKeyConfigs[0].ConsumptionPolicy
	if v0 != KeyConsumptionOpportunistic {
		t.Fatalf("V3 成员应保留 opportunistic，实际 %q", v0)
	}
	v1 := channels[1].Protocols[0].Upstream.APIKeyConfigs[0].ConsumptionPolicy
	if v1 != KeyConsumptionNormal {
		t.Fatalf("V3 成员应保留 normal，实际 %q", v1)
	}
}

// assertUpstreamsEqual 通过 JSON 比较两组 UpstreamConfig 顺序与字段完全一致。
func assertUpstreamsEqual(t *testing.T, kind string, want, got []UpstreamConfig) {
	t.Helper()
	if len(want) != len(got) {
		t.Fatalf("%s 数组长度不一致: want %d got %d", kind, len(want), len(got))
	}
	for i := range want {
		wb, _ := json.Marshal(want[i])
		gb, _ := json.Marshal(got[i])
		if string(wb) != string(gb) {
			t.Fatalf("%s[%d] 回投影不一致\nwant: %s\ngot:  %s", kind, i, wb, gb)
		}
	}
}

// TestAuthoritativeChannels_OrderRestored 验证乱序成员按 Index 恢复原始 failover 顺序。
func TestAuthoritativeChannels_OrderRestored(t *testing.T) {
	cfg := &Config{
		Upstream: []UpstreamConfig{
			makeKeyChannel("messages", "ch_a", "", "https://a.example.com", "claude", "sk-a", "g", nil),
			makeKeyChannel("messages", "ch_b", "", "https://b.example.com", "claude", "sk-b", "g", nil),
			makeKeyChannel("messages", "ch_c", "", "https://c.example.com", "claude", "sk-c", "g", nil),
		},
	}
	channels := BuildAuthoritativeChannels(cfg)
	up, _, _, _, _, _ := ApplyAuthoritativeChannels(channels)
	if len(up) != 3 {
		t.Fatalf("应恢复 3 个 messages 渠道，实际 %d", len(up))
	}
	if up[0].ChannelUID != "ch_a" || up[1].ChannelUID != "ch_b" || up[2].ChannelUID != "ch_c" {
		t.Fatalf("数组内顺序未按原 Index 恢复: %s,%s,%s", up[0].ChannelUID, up[1].ChannelUID, up[2].ChannelUID)
	}
}
