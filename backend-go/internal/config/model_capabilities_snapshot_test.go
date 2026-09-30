package config

import "testing"

// UpstreamModelCapabilitiesSnapshot 必须与内部状态完全隔离（深拷贝），并且语义与
// GetConfig().UpstreamModelCapabilities 一致——调度热路径每个 attempt 都读它，
// 只克隆这一张表而不整份 GetConfig()。
func TestUpstreamModelCapabilitiesSnapshotIsolation(t *testing.T) {
	cm := newTestConfigManager(t, `{
		"upstreamModelCapabilities": {
			"claude-sonnet-5-5*": {
				"displayName": "Claude Sonnet 5.5",
				"reasoningEfforts": ["low", "max"],
				"capabilities": {"vision": true},
				"pricing": {"unit": "per_1m_tokens_usd", "outputPrice": 10}
			}
		}
	}`)

	snapshot := cm.UpstreamModelCapabilitiesSnapshot()
	capability, ok := snapshot["claude-sonnet-5-5*"]
	if !ok {
		t.Fatalf("快照缺少条目: %+v", snapshot)
	}
	if capability.Pricing == nil || capability.Pricing.OutputPrice == nil || *capability.Pricing.OutputPrice != 10 {
		t.Fatalf("定价未按预期解析: %+v", capability.Pricing)
	}

	// 修改快照（含指针与切片字段）不得回写内部状态
	*capability.Pricing.OutputPrice = 999
	capability.ReasoningEfforts[0] = "mutated"
	capability.Capabilities["vision"] = false
	snapshot["claude-sonnet-5-5*"] = capability

	fresh := cm.UpstreamModelCapabilitiesSnapshot()["claude-sonnet-5-5*"]
	if fresh.Pricing == nil || fresh.Pricing.OutputPrice == nil || *fresh.Pricing.OutputPrice != 10 {
		t.Fatalf("快照改动穿透到内部定价: %+v", fresh.Pricing)
	}
	if len(fresh.ReasoningEfforts) == 0 || fresh.ReasoningEfforts[0] != "low" {
		t.Fatalf("快照改动穿透到内部 effort: %+v", fresh.ReasoningEfforts)
	}
	if fresh.Capabilities["vision"] != true {
		t.Fatalf("快照改动穿透到内部能力表: %+v", fresh.Capabilities)
	}

	// 与 GetConfig() 的口径一致
	viaGetConfig := cm.GetConfig().UpstreamModelCapabilities["claude-sonnet-5-5*"]
	if viaGetConfig.Pricing == nil || *viaGetConfig.Pricing.OutputPrice != *fresh.Pricing.OutputPrice {
		t.Fatalf("两条读取口径不一致: %+v vs %+v", viaGetConfig.Pricing, fresh.Pricing)
	}
}
