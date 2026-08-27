package config

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/BenedictKing/ccx/internal/utils"
)

func derivedSeekaiName() string {
	return utils.DeriveChannelNameFromBaseURL("https://seekai.cc")
}

// 回归：名称迁移不得把物理渠道残留的旧备注回填到逻辑渠道。
// 背景：旧迁移曾把派生前的历史名截断写入物理渠道 Remark（如 "seekai-cc-"），
// 用户删除备注后，migrateAllChannelNamesConfig 只要因任一渠道改名而触发（changed==true），
// 就会把空逻辑备注从该残留值复活，表现为“删了保存再打开又出现”。
func TestMigrateNameDoesNotReviveLogicalRemark(t *testing.T) {
	victimDerived := derivedSeekaiName()
	cfg := &Config{
		LogicalChannels: []LogicalChannel{
			{
				LogicalChannelUID: "lc_victim",
				Name:              victimDerived,
				Remark:            "", // 用户已删除的备注，不允许被回填复活
				Kind:              "llm",
				Protocols: []LogicalChannelProtocol{
					{Kind: "messages", ChannelUID: "ch_victim"},
				},
			},
		},
		// victim 物理渠道：名称已收敛为派生值，但残留旧迁移写下的截断旧名。
		Upstream: []UpstreamConfig{
			{
				ChannelUID:        "ch_victim",
				LogicalChannelUID: "lc_victim",
				Name:              victimDerived,
				LogicalName:       victimDerived,
				Remark:            "seekai-cc-",
				BaseURL:           "https://seekai.cc",
				BaseURLs:          []string{"https://seekai.cc"},
				ServiceType:       "claude",
				APIKeys:           []string{"sk-test-victim"},
			},
		},
		// trigger 物理渠道：名称与派生值不一致，保证迁移发生整体写回（changed==true），
		// 从而覆盖原回填分支所在代码路径。
		ChatUpstream: []UpstreamConfig{
			{
				ChannelUID:        "ch_trigger",
				LogicalChannelUID: "lc_trigger",
				Name:              "bad-old-name",
				LogicalName:       "bad-old-name",
				BaseURL:           "https://other.example.com",
				BaseURLs:          []string{"https://other.example.com"},
				ServiceType:       "openai",
				APIKeys:           []string{"sk-test-trigger"},
			},
		},
	}

	if !migrateAllChannelNamesConfig(cfg) {
		t.Fatal("预期 trigger 渠道触发名称迁移写回")
	}

	lc := &cfg.LogicalChannels[0]
	if strings.TrimSpace(lc.Remark) != "" {
		t.Errorf("逻辑渠道备注被物理残留值复活: %q（应为空）", lc.Remark)
	}
	if lc.Name != victimDerived {
		t.Errorf("逻辑渠道名被意外改写: %q", lc.Name)
	}
	if got := cfg.Upstream[0].Remark; got != "seekai-cc-" {
		t.Errorf("用户未触及的物理渠道残留备注不应被迁移改动: %q", got)
	}
	if got := cfg.ChatUpstream[0].Name; got != "other-example-com" {
		t.Errorf("trigger 渠道应完成名称重派生: %q", got)
	}
}

// 端到端补充：落盘→重载后逻辑渠道备注同样保持为空（真实 config 管理器路径）。
func TestMigrateNameDoesNotReviveLogicalRemarkOnReload(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.json")
	cm, err := NewConfigManager(cfgFile, filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	derived := derivedSeekaiName()

	cm.mu.Lock()
	cm.config.LogicalChannels = append(cm.config.LogicalChannels, LogicalChannel{
		LogicalChannelUID: "lc_victim2",
		Name:              derived,
		Remark:            "",
		Kind:              "llm",
		SiteIdentity:      SiteIdentityForBaseURL("https://seekai.cc"),
		BaseURLs:          []string{"https://seekai.cc"},
		Protocols: []LogicalChannelProtocol{
			{Kind: "messages", ChannelUID: "ch_victim2", ServiceType: "claude", Enabled: true, Status: "active", Priority: 1},
		},
	})
	cm.config.Upstream = append(cm.config.Upstream, UpstreamConfig{
		ChannelUID:        "ch_victim2",
		LogicalChannelUID: "lc_victim2",
		Name:              derived,
		LogicalName:       derived,
		Remark:            "seekai-cc-",
		BaseURL:           "https://seekai.cc",
		BaseURLs:          []string{"https://seekai.cc"},
		ServiceType:       "claude",
		APIKeys:           []string{"sk-test-victim"},
		Status:            "active",
		Priority:          1,
	})
	if err := cm.saveConfigLocked(cm.config); err != nil {
		cm.mu.Unlock()
		t.Fatalf("保存失败: %v", err)
	}
	assertNoVictimRemark(t, "保存后(内存)", cm.config.LogicalChannels)
	cm.mu.Unlock()

	cm2, err := NewConfigManager(cfgFile, filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	assertNoVictimRemark(t, "重新加载后", cm2.ListLogicalChannels())
}

func assertNoVictimRemark(t *testing.T, stage string, lcs []LogicalChannel) {
	t.Helper()
	for _, lc := range lcs {
		if lc.Name == derivedSeekaiName() && strings.TrimSpace(lc.Remark) != "" {
			t.Errorf("[%s] 逻辑渠道 %s 备注被复活: %q（应为空）", stage, lc.Name, lc.Remark)
		}
	}
}
