package scheduler

import (
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

// TestFilterChannelsByContextProbeFallback 验证溢出试探层：
// 窗口不足但注册表分段阶梯可覆盖的同模型候选保留为试探候选，
// 已被实测收紧上限判死的组合不得试探，超出试探档上限的组合直接判死。
func TestFilterChannelsByContextProbeFallback(t *testing.T) {
	cfg := config.Config{
		ResponsesUpstream: []config.UpstreamConfig{
			{
				Name: "stale-registry", Status: "active", ChannelUID: "ch_stale",
				BaseURL: "https://stale.example.com",
				APIKeys: []string{"sk-test"},
				ModelCapabilities: map[string]config.UpstreamModelCapability{
					"gpt-5.6-sol": {
						ContextWindowTokens: 272_000,
						ContextWindowTiers:  []int{272_000, 372_000, 1_050_000},
					},
				},
			},
			{
				Name: "capped", Status: "active", ChannelUID: "ch_capped",
				BaseURL: "https://capped.example.com",
				APIKeys: []string{"sk-test"},
				ModelCapabilities: map[string]config.UpstreamModelCapability{
					"gpt-5.6-sol": {ContextWindowTokens: 272_000},
				},
			},
			{
				// 渠道级覆盖为 128K 且无分段阶梯：注册表层面的 1.05M 被渠道覆盖压低，
				// 模拟"该渠道部署的窗口就是小的且不会扩容"。
				Name: "no-tiers", Status: "active", ChannelUID: "ch_notiers",
				BaseURL: "https://notiers.example.com",
				APIKeys: []string{"sk-test"},
				ModelCapabilities: map[string]config.UpstreamModelCapability{
					"gpt-5.6-sol": {ContextWindowTokens: 128_000},
				},
			},
		},
	}
	s, cleanup := createTestScheduler(t, cfg)
	defer cleanup()

	// declared 收紧注入：capped 渠道实测只吃 200K，试探必须排除。
	s.SetContextWindowResolverProvider(func(channelUID string, kind ChannelKind, actualModel string, registryWindow int) (int, int) {
		switch channelUID {
		case "ch_capped":
			return 200_000, 200_000
		default:
			return registryWindow, 0
		}
	})

	channels := []ChannelInfo{
		{Index: 0, Name: "stale-registry", Status: "active"},
		{Index: 1, Name: "capped", Status: "active"},
		{Index: 2, Name: "no-tiers", Status: "active"},
	}

	// 输入 274K：stale-registry（阶梯覆盖 1.05M）→ 试探候选；
	// capped（declared 200K 矛盾）→ 剔除；no-tiers（128K 无阶梯，274K 超试探档）→ 剔除。
	result, err := s.filterChannelsByContext(channels, ChannelKindResponses, "gpt-5.6-sol", &ContextRequirement{
		InputTokens:    274_081,
		OutputTokens:   8_192,
		RequiredTokens: 282_273,
	}, newSelectionTrace(SelectionOptions{Kind: ChannelKindResponses, Model: "gpt-5.6-sol"}))
	if err != nil {
		t.Fatalf("试探候选应避免容量错误: %v", err)
	}
	if len(result) != 1 || result[0].Name != "stale-registry" {
		t.Fatalf("试探候选 = %v, want [stale-registry]", result)
	}

	// 输入 900K：仍被 1.05M 阶梯覆盖 → 试探放行
	if _, err := s.filterChannelsByContext([]ChannelInfo{{Index: 0, Name: "stale-registry", Status: "active"}},
		ChannelKindResponses, "gpt-5.6-sol", &ContextRequirement{InputTokens: 900_000, RequiredTokens: 908_000},
		newSelectionTrace(SelectionOptions{Kind: ChannelKindResponses, Model: "gpt-5.6-sol"})); err != nil {
		t.Fatalf("阶梯覆盖内的请求应保留试探候选: %v", err)
	}

	// 输入 1.1M：超出 1.05M 试探档上限 → 全灭，返回类型化容量错误
	if _, err := s.filterChannelsByContext([]ChannelInfo{{Index: 0, Name: "stale-registry", Status: "active"}},
		ChannelKindResponses, "gpt-5.6-sol", &ContextRequirement{InputTokens: 1_100_000, RequiredTokens: 1_108_000},
		newSelectionTrace(SelectionOptions{Kind: ChannelKindResponses, Model: "gpt-5.6-sol"})); err == nil {
		t.Fatal("超出试探档上限应返回错误")
	} else if _, ok := AsContextCapacityError(err); !ok {
		t.Fatalf("应为 ContextCapacityError, got %T", err)
	}
}

// TestResolveContextWindowFallbackNilResolver 注入器缺省/异常返回时沿用注册表窗口。
func TestResolveContextWindowFallbackNilResolver(t *testing.T) {
	s, cleanup := createTestScheduler(t, config.Config{})
	defer cleanup()

	effective, declared := s.resolveContextWindow("ch_x", ChannelKindResponses, "m", 272_000)
	if effective != 272_000 || declared != 0 {
		t.Fatalf("got (%d, %d), want (272000, 0)", effective, declared)
	}

	s.SetContextWindowResolverProvider(func(string, ChannelKind, string, int) (int, int) { return 0, -5 })
	effective, declared = s.resolveContextWindow("ch_x", ChannelKindResponses, "m", 272_000)
	if effective != 272_000 || declared != 0 {
		t.Fatalf("非正返回应回退注册表并钳平 declared, got (%d, %d)", effective, declared)
	}
}
