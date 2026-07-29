package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/BenedictKing/ccx/internal/errutil"
)

// newTestConfigManager 写入初始配置并返回 ConfigManager
func newTestConfigManager(t *testing.T, initial string) *ConfigManager {
	t.Helper()
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfgFile, []byte(initial), 0600); err != nil {
		t.Fatalf("写入初始配置失败: %v", err)
	}
	cm, err := NewConfigManager(cfgFile, "")
	if err != nil {
		t.Fatalf("NewConfigManager 失败: %v", err)
	}
	t.Cleanup(func() { errutil.IgnoreDeferred(cm.Close) })
	return cm
}

// TestAddUpstream_PriorityPlacement 测试新增渠道按 placement 分配 priority
func TestAddUpstream_PriorityPlacement(t *testing.T) {
	baseChannels := `{"name":"a","baseUrl":"https://a.example.com","apiKeys":["k1"],"priority":1},{"name":"b","baseUrl":"https://b.example.com","apiKeys":["k2"],"priority":2}`

	newChannel := func(name string) UpstreamConfig {
		return UpstreamConfig{Name: name, BaseURL: "https://" + name + ".example.com", APIKeys: []string{"k9"}}
	}

	t.Run("messages back 追加到末尾", func(t *testing.T) {
		cm := newTestConfigManager(t, `{"upstream":[`+baseChannels+`],"responsesUpstream":[],"geminiUpstream":[],"chatUpstream":[],"imagesUpstream":[],"vectorsUpstream":[]}`)
		if err := cm.AddUpstream(newChannel("c")); err != nil {
			t.Fatalf("AddUpstream 失败: %v", err)
		}
		cfg := cm.GetConfig()
		if cfg.Upstream[0].Priority != 3 {
			t.Fatalf("新渠道 priority 期望 3，得到 %d", cfg.Upstream[0].Priority)
		}
		if cfg.Upstream[1].Priority != 1 || cfg.Upstream[2].Priority != 2 {
			t.Fatalf("现有渠道 priority 不应变化: %d, %d", cfg.Upstream[1].Priority, cfg.Upstream[2].Priority)
		}
	})

	t.Run("messages front 插入首位并整体后移", func(t *testing.T) {
		cm := newTestConfigManager(t, `{"upstream":[`+baseChannels+`],"responsesUpstream":[],"geminiUpstream":[],"chatUpstream":[],"imagesUpstream":[],"vectorsUpstream":[]}`)
		if err := cm.AddUpstream(newChannel("c"), ChannelPlacementFront); err != nil {
			t.Fatalf("AddUpstream 失败: %v", err)
		}
		cfg := cm.GetConfig()
		if cfg.Upstream[0].Priority != 1 {
			t.Fatalf("新渠道 priority 期望 1，得到 %d", cfg.Upstream[0].Priority)
		}
		if cfg.Upstream[1].Priority != 2 || cfg.Upstream[2].Priority != 3 {
			t.Fatalf("现有渠道 priority 应整体 +1: %d, %d", cfg.Upstream[1].Priority, cfg.Upstream[2].Priority)
		}
	})

	t.Run("messages front 不改动未显式配置的 0 值渠道", func(t *testing.T) {
		initial := `{"upstream":[{"name":"legacy","baseUrl":"https://l.example.com","apiKeys":["k0"]},` + baseChannels + `],"responsesUpstream":[],"geminiUpstream":[],"chatUpstream":[],"imagesUpstream":[],"vectorsUpstream":[]}`
		cm := newTestConfigManager(t, initial)
		if err := cm.AddUpstream(newChannel("c"), ChannelPlacementFront); err != nil {
			t.Fatalf("AddUpstream 失败: %v", err)
		}
		cfg := cm.GetConfig()
		// 数组顺序: c(新), legacy, a, b
		if cfg.Upstream[0].Priority != 1 {
			t.Fatalf("新渠道 priority 期望 1，得到 %d", cfg.Upstream[0].Priority)
		}
		if cfg.Upstream[1].Priority != 0 {
			t.Fatalf("legacy 0 值渠道不应被后移，得到 %d", cfg.Upstream[1].Priority)
		}
		if cfg.Upstream[2].Priority != 2 || cfg.Upstream[3].Priority != 3 {
			t.Fatalf("现有渠道 priority 应整体 +1: %d, %d", cfg.Upstream[2].Priority, cfg.Upstream[3].Priority)
		}
	})

	t.Run("messages 空配置首个渠道 priority 为 1", func(t *testing.T) {
		cm := newTestConfigManager(t, `{"upstream":[],"responsesUpstream":[],"geminiUpstream":[],"chatUpstream":[],"imagesUpstream":[],"vectorsUpstream":[]}`)
		if err := cm.AddUpstream(newChannel("c"), ChannelPlacementFront); err != nil {
			t.Fatalf("AddUpstream 失败: %v", err)
		}
		if got := cm.GetConfig().Upstream[0].Priority; got != 1 {
			t.Fatalf("首个渠道 priority 期望 1，得到 %d", got)
		}
	})

	t.Run("chat back 追加到末尾", func(t *testing.T) {
		cm := newTestConfigManager(t, `{"upstream":[],"chatUpstream":[`+baseChannels+`],"responsesUpstream":[],"geminiUpstream":[],"imagesUpstream":[],"vectorsUpstream":[]}`)
		if err := cm.AddChatUpstream(newChannel("c")); err != nil {
			t.Fatalf("AddChatUpstream 失败: %v", err)
		}
		cfg := cm.GetConfig()
		if cfg.ChatUpstream[0].Priority != 3 {
			t.Fatalf("新渠道 priority 期望 3，得到 %d", cfg.ChatUpstream[0].Priority)
		}
	})

	t.Run("chat front 插入首位并整体后移", func(t *testing.T) {
		cm := newTestConfigManager(t, `{"upstream":[],"chatUpstream":[`+baseChannels+`],"responsesUpstream":[],"geminiUpstream":[],"imagesUpstream":[],"vectorsUpstream":[]}`)
		if err := cm.AddChatUpstream(newChannel("c"), ChannelPlacementFront); err != nil {
			t.Fatalf("AddChatUpstream 失败: %v", err)
		}
		cfg := cm.GetConfig()
		if cfg.ChatUpstream[0].Priority != 1 {
			t.Fatalf("新渠道 priority 期望 1，得到 %d", cfg.ChatUpstream[0].Priority)
		}
		if cfg.ChatUpstream[1].Priority != 2 || cfg.ChatUpstream[2].Priority != 3 {
			t.Fatalf("现有渠道 priority 应整体 +1: %d, %d", cfg.ChatUpstream[1].Priority, cfg.ChatUpstream[2].Priority)
		}
	})

	t.Run("非法 placement 按 back 处理", func(t *testing.T) {
		cm := newTestConfigManager(t, `{"upstream":[`+baseChannels+`],"responsesUpstream":[],"geminiUpstream":[],"chatUpstream":[],"imagesUpstream":[],"vectorsUpstream":[]}`)
		if err := cm.AddUpstream(newChannel("c"), "middle"); err != nil {
			t.Fatalf("AddUpstream 失败: %v", err)
		}
		if got := cm.GetConfig().Upstream[0].Priority; got != 3 {
			t.Fatalf("非法 placement 应按末尾处理，priority 期望 3，得到 %d", got)
		}
	})

	t.Run("back 保留调用方显式指定的 priority", func(t *testing.T) {
		cm := newTestConfigManager(t, `{"upstream":[`+baseChannels+`],"responsesUpstream":[],"geminiUpstream":[],"chatUpstream":[],"imagesUpstream":[],"vectorsUpstream":[]}`)
		ch := newChannel("c")
		ch.Priority = 1
		if err := cm.AddUpstream(ch); err != nil {
			t.Fatalf("AddUpstream 失败: %v", err)
		}
		if got := cm.GetConfig().Upstream[0].Priority; got != 1 {
			t.Fatalf("显式 priority 应保留为 1，得到 %d", got)
		}
	})
}
