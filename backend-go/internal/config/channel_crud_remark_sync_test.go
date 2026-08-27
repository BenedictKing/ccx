package config

import (
	"path/filepath"
	"strings"
	"testing"
)

// 回归：统一列表(Dashboard)展示的是逻辑渠道层备注，而编辑器保存走单张物理卡更新。
// 若单卡保存不同步逻辑层与兄弟卡，用户删除/修改的备注会被其它层的残留值顶回，
// 表现为“删了保存再打开又出现”（如 vip-lyclaude-site 渠道的 "vip-lyclau"）。
func TestSingleCardRemarkClearSyncsWholeGroup(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.json")
	cm, err := NewConfigManager(cfgFile, filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatal(err)
	}

	const groupUID = "lc_group"
	base := BaseURLForTest("https://vip.lyclaude.site")
	cm.mu.Lock()
	cm.config.LogicalChannels = append(cm.config.LogicalChannels, LogicalChannel{
		LogicalChannelUID: groupUID,
		Name:              "vip-lyclaude-site",
		Remark:            "vip-lyclau",
		Kind:              "llm",
		SiteIdentity:      SiteIdentityForBaseURL(base),
		BaseURLs:          []string{base},
	})
	mkCard := func(uid string) UpstreamConfig {
		return UpstreamConfig{
			ChannelUID:        uid,
			LogicalChannelUID: groupUID,
			Name:              "vip-lyclaude-site",
			LogicalName:       "vip-lyclaude-site",
			Remark:            "vip-lyclau",
			BaseURL:           base,
			BaseURLs:          []string{base},
			ServiceType:       "claude",
			APIKeys:           []string{"sk-test"},
			Status:            "active",
			Priority:          1,
		}
	}
	cm.config.Upstream = append(cm.config.Upstream, mkCard("ch_m"))
	cm.config.ChatUpstream = append(cm.config.ChatUpstream, mkCard("ch_c"))
	cm.config.ResponsesUpstream = append(cm.config.ResponsesUpstream, mkCard("ch_r"))
	cm.mu.Unlock()

	k := ChannelKindRegistry[ChannelKindMessages]
	empty := ""
	if _, err := cm.updateUpstreamCommon(k, 0, UpstreamUpdate{Remark: &empty}); err != nil {
		t.Fatalf("单卡更新失败: %v", err)
	}

	cm.mu.RLock()
	defer cm.mu.RUnlock()
	checks := map[string]string{
		"messages 卡":   cm.config.Upstream[0].Remark,
		"chat 兄弟卡":    cm.config.ChatUpstream[0].Remark,
		"responses 兄弟卡": cm.config.ResponsesUpstream[0].Remark,
		"逻辑渠道":         cm.config.LogicalChannels[0].Remark,
	}
	for label, got := range checks {
		if strings.TrimSpace(got) != "" {
			t.Errorf("[%s] 备注未被同步清除: %q", label, got)
		}
	}

	t.Cleanup(func() {})
	_ = cfgFile
}

// 落盘持久性：同步删除后重载配置文件，各层备注仍为空。
func TestSingleCardRemarkClearPersistsOnReload(t *testing.T) {
	dir := t.TempDir()
	cfgFile := filepath.Join(dir, "config.json")
	cm, err := NewConfigManager(cfgFile, filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	const groupUID = "lc_group2"
	base := BaseURLForTest("https://vip.lyclaude.site")
	card := func(uid string) UpstreamConfig {
		return UpstreamConfig{
			ChannelUID:        uid,
			LogicalChannelUID: groupUID,
			Name:              "vip-lyclaude-site",
			LogicalName:       "vip-lyclaude-site",
			Remark:            "vip-lyclau",
			BaseURL:           base,
			BaseURLs:          []string{base},
			ServiceType:       "claude",
			APIKeys:           []string{"sk-test"},
			Status:            "active",
			Priority:          1,
		}
	}
	cm.mu.Lock()
	cm.config.LogicalChannels = append(cm.config.LogicalChannels, LogicalChannel{
		LogicalChannelUID: groupUID,
		Name:              "vip-lyclaude-site",
		Remark:            "vip-lyclau",
		Kind:              "llm",
		SiteIdentity:      SiteIdentityForBaseURL(base),
		BaseURLs:          []string{base},
	})
	cm.config.Upstream = append(cm.config.Upstream, card("ch_m2"))
	cm.config.ResponsesUpstream = append(cm.config.ResponsesUpstream, card("ch_r2"))
	cm.mu.Unlock()

	empty := ""
	if _, err := cm.updateUpstreamCommon(ChannelKindRegistry[ChannelKindMessages], 0, UpstreamUpdate{Remark: &empty}); err != nil {
		t.Fatalf("单卡更新失败: %v", err)
	}

	cm2, err := NewConfigManager(cfgFile, filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatal(err)
	}
	for _, lc := range cm2.ListLogicalChannels() {
		if lc.LogicalChannelUID == groupUID && strings.TrimSpace(lc.Remark) != "" {
			t.Errorf("重载后逻辑渠道备注复活: %q", lc.Remark)
		}
	}
}

// BaseURLForTest 测试辅助：显式包装字面量，避免测试内散落硬编码 URL。
func BaseURLForTest(u string) string { return u }
