package config

import "testing"

func TestCapabilityProbeLedger_ClaimOnce(t *testing.T) {
	l := NewCapabilityProbeLedger()
	if !l.ClaimProbe("cap_a") {
		t.Fatal("首次 claim 应返回 true")
	}
	if l.ClaimProbe("cap_a") {
		t.Fatal("同一能力第二次 claim 应返回 false（复用）")
	}
	if !l.ClaimProbe("cap_b") {
		t.Fatal("不同能力应可独立 claim")
	}
	if l.ProbedCount() != 2 {
		t.Fatalf("已占用能力数 = %d, 期望 2", l.ProbedCount())
	}
	l.Reset()
	if l.ProbedCount() != 0 {
		t.Fatalf("Reset 后应为 0, 实际 %d", l.ProbedCount())
	}
	if !l.ClaimProbe("cap_a") {
		t.Fatal("Reset 后应可重新 claim")
	}
}

func TestCapabilityProbeLedger_NilAndEmpty(t *testing.T) {
	var l *CapabilityProbeLedger
	if !l.ClaimProbe("cap_a") {
		t.Fatal("nil 台账应恒返回 true（不去重）")
	}
	real := NewCapabilityProbeLedger()
	if !real.ClaimProbe("") {
		t.Fatal("空 CapabilityUID 应恒返回 true（不去重）")
	}
}

// TestCapabilityRegistry_CrossAccountShare 验证两账号同分组解析到同一份能力模型。
func TestCapabilityRegistry_CrossAccountShare(t *testing.T) {
	cfg := &Config{
		ChatUpstream: []UpstreamConfig{
			makeKeyChannel("chat", "ch_a", "acct_a", "https://relay.example.com", "openai", "sk-aaa", "vip", []string{"gpt-x"}),
			makeKeyChannel("chat", "ch_b", "acct_b", "https://relay.example.com", "openai", "sk-bbb", "vip", []string{"gpt-x"}),
		},
	}
	views, caps := BuildChannelViews(cfg)
	reg := NewEndpointCapabilityRegistry(caps)

	uidsA := KeyEndpointCapabilityUIDs(views[0], ChannelKeyHash("sk-aaa"))
	uidsB := KeyEndpointCapabilityUIDs(views[1], ChannelKeyHash("sk-bbb"))
	if len(uidsA) != 1 || len(uidsB) != 1 {
		t.Fatalf("每个 key 应绑定 1 个能力: A=%v B=%v", uidsA, uidsB)
	}
	if uidsA[0] != uidsB[0] {
		t.Fatalf("跨账号同分组应绑定同一 CapabilityUID: A=%s B=%s", uidsA[0], uidsB[0])
	}
	models := reg.ModelsForCapability(uidsA[0])
	if len(models) != 1 || models[0] != "gpt-x" {
		t.Fatalf("共享能力模型清单 = %v, 期望 [gpt-x]", models)
	}

	// 去重台账：两账号 key 指向同一能力，只有一个能 claim 到探测权。
	ledger := NewCapabilityProbeLedger()
	claimA := ledger.ClaimProbe(uidsA[0])
	claimB := ledger.ClaimProbe(uidsB[0])
	if !(claimA != claimB) {
		t.Fatalf("同能力两账号应只有一个 claim 到探测权: A=%v B=%v", claimA, claimB)
	}
}
