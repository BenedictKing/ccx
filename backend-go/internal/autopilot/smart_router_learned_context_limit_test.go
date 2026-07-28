package autopilot

import (
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/scheduler"
)

// stubLearnedContextLimit 用内存桩替换实测上限查询，避免测试依赖落盘的共享兼容性记忆。
func stubLearnedContextLimit(t *testing.T, limits map[string]int) {
	t.Helper()
	original := learnedContextLimitLookup
	learnedContextLimitLookup = func(channelUID, model string) (int, bool) {
		limit, ok := limits[channelUID+"|"+model]
		return limit, ok
	}
	t.Cleanup(func() { learnedContextLimitLookup = original })
}

func TestLearnedContextLimitTightensRegistryWindow(t *testing.T) {
	// 注册表说 gpt-5.5 有 105 万窗口，但该渠道实测只吃 27.2 万：
	// 这正是现网 400 context_too_large 的成因，硬约束必须按实测值判断。
	stubLearnedContextLimit(t, map[string]int{"ch_relay|gpt-5.5": 272_000})

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

func TestLearnedContextLimitDoesNotWidenRegistryWindow(t *testing.T) {
	// 实测值比注册表更宽时不得放宽：注册表是模型能力上界，
	// 实测上限只用于收紧（学到的是"更短"这一事实，不是"更长"的许可）。
	stubLearnedContextLimit(t, map[string]int{"ch_relay|short-model": 900_000})

	router := NewSmartRouter(nil, nil, nil, nil)
	upstream := &config.UpstreamConfig{
		ChannelUID: "ch_relay",
		ModelCapabilities: map[string]config.UpstreamModelCapability{
			"short-model": {ContextWindowTokens: 32_768},
		},
	}
	entry := router.buildChannelEntry(
		scheduler.ChannelInfo{Index: 0, Name: "relay", Status: "active"},
		upstream,
		"messages",
		"short-model",
		nil,
	)

	if entry.ContextWindowTokens != 32_768 {
		t.Fatalf("ContextWindowTokens = %d, want 32768（不应被更宽的实测值放宽）", entry.ContextWindowTokens)
	}
}

func TestLearnedContextLimitAppliesWhenRegistryUnknown(t *testing.T) {
	// 注册表完全不认识该模型时（窗口 0 = 未知，原本 fail-open 不过滤），
	// 实测上限是唯一可用证据，必须生效，否则这类渠道会反复吃 400。
	stubLearnedContextLimit(t, map[string]int{"ch_unknown|mystery-model": 16_384})

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
		t.Error("超出实测上限的请求应被过滤")
	}
}

func TestNoLearnedContextLimitKeepsFailOpen(t *testing.T) {
	stubLearnedContextLimit(t, nil)

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
