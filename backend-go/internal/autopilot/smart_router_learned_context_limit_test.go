package autopilot

import (
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/scheduler"
)

// stubEffectiveContextWindow 用内存桩替换有效窗口查询，避免测试依赖落盘的共享兼容性记忆。
// 桩直接给出"合成后的有效窗口"，公式本身（放宽/收紧合成）由 config 包的
// channel_compat_context_window_test.go 覆盖。
func stubEffectiveContextWindow(t *testing.T, windows map[string]int) {
	t.Helper()
	original := effectiveContextWindowLookup
	effectiveContextWindowLookup = func(channelUID, channelKind, model string, registryWindow int) int {
		if window, ok := windows[channelUID+"|"+channelKind+"|"+model]; ok {
			return window
		}
		return registryWindow
	}
	t.Cleanup(func() { effectiveContextWindowLookup = original })
}

func TestEffectiveContextWindowTightensRegistryWindow(t *testing.T) {
	// 注册表说 gpt-5.5 有 105 万窗口，但该渠道实测只吃 27.2 万：
	// 这正是现网 400 context_too_large 的成因，硬约束必须按实测值判断。
	stubEffectiveContextWindow(t, map[string]int{"ch_relay|messages|gpt-5.5": 272_000})

	router := NewSmartRouter(nil, nil, nil, nil)
	upstream := &config.UpstreamConfig{
		ChannelUID: "ch_relay",
		ModelCapabilities: map[string]config.UpstreamModelCapability{
			"gpt-5.5": {ContextWindowTokens: 1_050_000},
		},
	}
	entry := router.buildChannelEntry(
		scheduler.ChannelInfo{Index: 0, Name: "relay", Status: "active"},
		upstream,
		"messages",
		"gpt-5.5",
		nil,
	)

	if entry.ContextWindowTokens != 272_000 {
		t.Fatalf("ContextWindowTokens = %d, want 272000（应取实测上限）", entry.ContextWindowTokens)
	}

	// 500k 请求应被硬约束挡下，而不是送上去换一个 400
	reasons := routingHardConstraintReasons(&RequestProfile{ContextNeed: 500_000}, &entry)
	if len(reasons) != 1 || reasons[0] != "上下文窗口不满足" {
		t.Fatalf("routingHardConstraintReasons() = %v, want [上下文窗口不满足]", reasons)
	}

	// 实测上限之内的请求仍应放行
	if reasons := routingHardConstraintReasons(&RequestProfile{ContextNeed: 100_000}, &entry); len(reasons) != 0 {
		t.Errorf("上限内请求不应被过滤, got %v", reasons)
	}
}

func TestEffectiveContextWindowWidensStaleRegistry(t *testing.T) {
	// 渐进扩容场景：注册表仍停留在 272K，渠道实际已开放 500K 且被成功实证。
	// 旧的只收紧逻辑会把 274K 请求锁死在发前过滤（gpt-5.6-sol 事故路径）。
	stubEffectiveContextWindow(t, map[string]int{"ch_stale|responses|gpt-5.6-sol": 500_000})

	router := NewSmartRouter(nil, nil, nil, nil)
	upstream := &config.UpstreamConfig{
		ChannelUID: "ch_stale",
		ModelCapabilities: map[string]config.UpstreamModelCapability{
			"gpt-5.6-sol": {ContextWindowTokens: 272_000},
		},
	}
	entry := router.buildChannelEntry(
		scheduler.ChannelInfo{Index: 0, Name: "stale", Status: "active"},
		upstream,
		"responses",
		"gpt-5.6-sol",
		nil,
	)

	if entry.ContextWindowTokens != 500_000 {
		t.Fatalf("ContextWindowTokens = %d, want 500000（实证值应顶开过期注册表）", entry.ContextWindowTokens)
	}
	if reasons := routingHardConstraintReasons(&RequestProfile{ContextNeed: 480_000}, &entry); len(reasons) != 0 {
		t.Errorf("实证窗口内的请求不应被过滤, got %v", reasons)
	}
}

func TestEffectiveContextWindowAppliesWhenRegistryUnknown(t *testing.T) {
	// 注册表完全不认识该模型时（窗口 0 = 未知，原本 fail-open 不过滤），
	// 学习证据是唯一可用事实，必须生效，否则这类渠道会反复吃 400。
	stubEffectiveContextWindow(t, map[string]int{"ch_unknown|messages|mystery-model": 16_384})

	router := NewSmartRouter(nil, nil, nil, nil)
	upstream := &config.UpstreamConfig{ChannelUID: "ch_unknown"}
	entry := router.buildChannelEntry(
		scheduler.ChannelInfo{Index: 0, Name: "unknown", Status: "active"},
		upstream,
		"messages",
		"mystery-model",
		nil,
	)

	if entry.ContextWindowTokens != 16_384 {
		t.Fatalf("ContextWindowTokens = %d, want 16384", entry.ContextWindowTokens)
	}
	if reasons := routingHardConstraintReasons(&RequestProfile{ContextNeed: 50_000}, &entry); len(reasons) == 0 {
		t.Error("超出学习窗口的请求应被过滤")
	}
}

func TestNoEffectiveContextWindowKeepsFailOpen(t *testing.T) {
	stubEffectiveContextWindow(t, nil)

	router := NewSmartRouter(nil, nil, nil, nil)
	upstream := &config.UpstreamConfig{ChannelUID: "ch_fresh"}
	entry := router.buildChannelEntry(
		scheduler.ChannelInfo{Index: 0, Name: "fresh", Status: "active"},
		upstream,
		"messages",
		"brand-new-model",
		nil,
	)

	// 无任何证据的新渠道不应被上下文约束误杀
	if entry.ContextWindowTokens != 0 {
		t.Fatalf("ContextWindowTokens = %d, want 0（fail-open）", entry.ContextWindowTokens)
	}
	if reasons := routingHardConstraintReasons(&RequestProfile{ContextNeed: 500_000}, &entry); len(reasons) != 0 {
		t.Errorf("未知窗口应 fail-open, got %v", reasons)
	}
}
