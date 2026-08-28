package autopilot

import (
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/scheduler"
)

// stubLearnedToolCallUnsupported 用内存桩替换实测工具调用不支持查询，避免测试依赖落盘的共享兼容性记忆。
func stubLearnedToolCallUnsupported(t *testing.T, unsupported map[string]bool) {
	t.Helper()
	original := learnedToolCallUnsupportedLookup
	learnedToolCallUnsupportedLookup = func(channelUID, model string) bool {
		return unsupported[channelUID+"|"+model]
	}
	t.Cleanup(func() { learnedToolCallUnsupportedLookup = original })
}

// 注册表支持 + 实测不能执行工具调用 → 收紧为不支持（seekai 类假渠道正是此场景）。
func TestLearnedToolCallUnsupportedOverridesRegistrySupport(t *testing.T) {
	stubLearnedToolCallUnsupported(t, map[string]bool{"ch_relay|fake-model": true})

	router := NewSmartRouter(nil, nil, nil, nil)
	upstream := &config.UpstreamConfig{
		ChannelUID: "ch_relay",
		ModelCapabilities: map[string]config.UpstreamModelCapability{
			"fake-model": {Capabilities: map[string]bool{"toolCalls": true}},
		},
	}
	entry := router.buildChannelEntry(
		scheduler.ChannelInfo{Index: 0, Name: "relay", Status: "active"},
		upstream,
		"messages",
		"fake-model",
		nil,
	)

	if entry.SupportsToolCalls {
		t.Fatal("SupportsToolCalls = true, want false（实测不能执行工具调用应覆盖注册表支持）")
	}
	reasons := routingHardConstraintReasons(&RequestProfile{ToolUseNeed: true}, &entry)
	if len(reasons) == 0 {
		t.Fatal("带工具请求应被硬约束过滤")
	}

	// 无工具需求的请求不受学习结论影响
	if reasons := routingHardConstraintReasons(&RequestProfile{}, &entry); len(reasons) != 0 {
		t.Errorf("无工具需求不应被过滤, got %v", reasons)
	}
}

// 无学习记录 → 注册表结论不变（fail-open）。
func TestNoLearnedToolCallUnsupportedKeepsRegistry(t *testing.T) {
	stubLearnedToolCallUnsupported(t, nil)

	router := NewSmartRouter(nil, nil, nil, nil)
	upstream := &config.UpstreamConfig{
		ChannelUID: "ch_fresh",
		ModelCapabilities: map[string]config.UpstreamModelCapability{
			"fresh-model": {Capabilities: map[string]bool{"toolCalls": true}},
		},
	}
	entry := router.buildChannelEntry(
		scheduler.ChannelInfo{Index: 0, Name: "fresh", Status: "active"},
		upstream,
		"messages",
		"fresh-model",
		nil,
	)

	if !entry.SupportsToolCalls {
		t.Fatal("SupportsToolCalls = false, want true（无学习记录时注册表结论不应被改变）")
	}
	if reasons := routingHardConstraintReasons(&RequestProfile{ToolUseNeed: true}, &entry); len(reasons) != 0 {
		t.Errorf("fail-open: got %v", reasons)
	}
}
