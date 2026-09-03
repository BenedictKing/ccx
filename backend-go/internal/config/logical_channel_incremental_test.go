package config

import (
	"strings"
	"testing"
	"time"
)

// TestRebuildIncrementalManualAddSameSite 复现"添加渠道后同站点分裂为两张逻辑卡"：
// 稳态下已有一条手工 chat 渠道（LogicalChannelUID 已回填），此时经 AddUpstream
// 新增一条同站点 messages 渠道（LogicalChannelUID 为空）。若 RebuildLogicalChannels
// 第 5 步只把空 UID 渠道按归组键建新组、而已归组旧渠道 continue 跳过，
// 新渠道将拿到全新 UID，与旧渠道永久分裂为两张卡。
func TestRebuildIncrementalManualAddSameSite(t *testing.T) {
	cm := newLogicalTestCM(t, func(c *Config) {
		c.ChatUpstream = []UpstreamConfig{
			{ChannelUID: "ch-c1", Name: "manual-c", BaseURL: "https://manual.example.com/v1", ServiceType: "openai", APIKeys: []string{"k1"}, Status: "active"},
		}
	})
	logicalLoadAndRebuild(t, cm)

	// 稳态：一张逻辑卡
	if logicals := cm.ListLogicalChannels(); len(logicals) != 1 {
		t.Fatalf("稳态期望 1 张逻辑卡，实际 %d", len(logicals))
	}
	chatUIDBefore := strings.TrimSpace(cm.GetConfig().ChatUpstream[0].LogicalChannelUID)
	if chatUIDBefore == "" {
		t.Fatalf("稳态 chat 渠道应有 LogicalChannelUID")
	}

	// 生产入口：新增同站点 messages 手工渠道
	if err := cm.AddUpstream(UpstreamConfig{
		ChannelUID: "ch-m1", Name: "manual-m", BaseURL: "https://manual.example.com/v1",
		ServiceType: "claude", APIKeys: []string{"k1"}, Status: "active",
	}); err != nil {
		t.Fatalf("AddUpstream 失败: %v", err)
	}

	cfg := cm.GetConfig()
	if len(cfg.Upstream) != 1 || len(cfg.ChatUpstream) != 1 {
		t.Fatalf("渠道数量异常: messages=%d chat=%d", len(cfg.Upstream), len(cfg.ChatUpstream))
	}
	msgUID := strings.TrimSpace(cfg.Upstream[0].LogicalChannelUID)
	chatUID := strings.TrimSpace(cfg.ChatUpstream[0].LogicalChannelUID)
	t.Logf("新增后: messages uid=%q chat uid=%q", msgUID, chatUID)

	if logicals := cm.ListLogicalChannels(); len(logicals) != 1 {
		t.Errorf("期望同站点手工渠道合并为 1 张逻辑卡，实际 %d 张（分裂）", len(logicals))
	}
	if msgUID != chatUID {
		t.Errorf("同站点两协议 LogicalChannelUID 不一致: messages=%s chat=%s", msgUID, chatUID)
	}

	// 再次落盘（模拟后续任意写触发 rebuild），分裂是否自愈
	if err := cm.SaveConfig(); err != nil {
		t.Fatalf("SaveConfig 失败: %v", err)
	}
	cfg = cm.GetConfig()
	msgUIDAfter := strings.TrimSpace(cfg.Upstream[0].LogicalChannelUID)
	chatUIDAfter := strings.TrimSpace(cfg.ChatUpstream[0].LogicalChannelUID)
	if logicals := cm.ListLogicalChannels(); len(logicals) != 1 {
		t.Errorf("二次 rebuild 后仍为 %d 张逻辑卡（分裂持久化）", len(logicals))
	}
	if msgUIDAfter != chatUIDAfter {
		t.Errorf("二次 rebuild 后 UID 仍不一致: messages=%s chat=%s", msgUIDAfter, chatUIDAfter)
	}
}

// TestRebuildIncrementalAccountManagedAddSameAccount 对照组：已有托管账号 chat 路由，
// 经 ApplyAccountChannelChanges 追加同账号 messages 路由（对应 auto-add 补协议路径）。
// 账号归组由第 4.5 步 convergeLogicalByAccount 强制收敛，预期不分裂。
func TestRebuildIncrementalAccountManagedAddSameAccount(t *testing.T) {
	accountUID := "acct-fixed-1"
	cm := newLogicalTestCM(t, func(c *Config) {
		c.ChatUpstream = []UpstreamConfig{
			{ChannelUID: "ch-c1", AccountUID: accountUID, AutoManaged: true, Name: "site-c", BaseURL: "https://acct.example.com/v1", ServiceType: "openai", APIKeys: []string{"k1"}, Status: "active"},
		}
	})
	logicalLoadAndRebuild(t, cm)
	if logicals := cm.ListLogicalChannels(); len(logicals) != 1 {
		t.Fatalf("稳态期望 1 张逻辑卡，实际 %d", len(logicals))
	}

	now := time.Now().UTC()
	additions := []AccountChannelAddition{{
		Kind: "messages",
		Upstream: UpstreamConfig{
			ChannelUID: "ch-m1", AccountUID: accountUID, AutoManaged: true, AutoManagedAt: &now,
			Name: "site-m", BaseURL: "https://acct.example.com/v1", ServiceType: "claude",
			APIKeys: []string{"k1"}, Status: "active",
		},
	}}
	updates := []AccountChannelUpdate{{ChannelUID: "ch-c1", APIKeys: []string{"k1"}}}
	if err := cm.ApplyAccountChannelChanges(accountUID, updates, additions); err != nil {
		t.Fatalf("ApplyAccountChannelChanges 失败: %v", err)
	}

	cfg := cm.GetConfig()
	msgUID := strings.TrimSpace(cfg.Upstream[0].LogicalChannelUID)
	chatUID := strings.TrimSpace(cfg.ChatUpstream[0].LogicalChannelUID)
	if logicals := cm.ListLogicalChannels(); len(logicals) != 1 {
		t.Errorf("账号托管路径期望 1 张逻辑卡，实际 %d", len(logicals))
	}
	if msgUID != chatUID {
		t.Errorf("账号托管路径 UID 不一致: messages=%s chat=%s", msgUID, chatUID)
	}
}

// TestRebuildFreshAccountMultiRouteAdd 复现快速添加主路径：全新托管账号一次创建
// chat+messages 两条路由（对应 auto-add → ApplyAccountChannelChanges 单次原子写）。
func TestRebuildFreshAccountMultiRouteAdd(t *testing.T) {
	cm := newLogicalTestCM(t, nil)
	logicalLoadAndRebuild(t, cm)

	accountUID := "acct-fresh-1"
	now := time.Now().UTC()
	additions := []AccountChannelAddition{
		{Kind: "messages", Upstream: UpstreamConfig{
			ChannelUID: "ch-m1", AccountUID: accountUID, AutoManaged: true, AutoManagedAt: &now,
			Name: "site-m", BaseURL: "https://fresh.example.com/v1", ServiceType: "claude",
			APIKeys: []string{"k1"}, Status: "active",
		}},
		{Kind: "chat", Upstream: UpstreamConfig{
			ChannelUID: "ch-c1", AccountUID: accountUID, AutoManaged: true, AutoManagedAt: &now,
			Name: "site-c", BaseURL: "https://fresh.example.com/v1", ServiceType: "openai",
			APIKeys: []string{"k1"}, Status: "active",
		}},
	}
	if err := cm.ApplyAccountChannelChanges(accountUID, nil, additions); err != nil {
		t.Fatalf("ApplyAccountChannelChanges 失败: %v", err)
	}

	cfg := cm.GetConfig()
	if len(cfg.Upstream) != 1 || len(cfg.ChatUpstream) != 1 {
		t.Fatalf("渠道数量异常: messages=%d chat=%d", len(cfg.Upstream), len(cfg.ChatUpstream))
	}
	msgUID := strings.TrimSpace(cfg.Upstream[0].LogicalChannelUID)
	chatUID := strings.TrimSpace(cfg.ChatUpstream[0].LogicalChannelUID)
	t.Logf("新建后: messages uid=%q chat uid=%q", msgUID, chatUID)
	if logicals := cm.ListLogicalChannels(); len(logicals) != 1 {
		t.Errorf("全新账号双路由期望 1 张逻辑卡，实际 %d", len(logicals))
	}
	if msgUID != chatUID {
		t.Errorf("全新账号双路由 UID 不一致: messages=%s chat=%s", msgUID, chatUID)
	}
}

// TestRebuildAbsorbGuards 验证吸收护栏：带真实 accountUID 的新渠道不得并入
// 同站点的其他账号/托管卡（对齐"不同 account 不合并"）；裸手工渠道不得并入
// provider 卡（对齐冷启动规则 2/3 的 provider 互斥）。
func TestRebuildAbsorbGuards(t *testing.T) {
	cm := newLogicalTestCM(t, func(c *Config) {
		c.ChatUpstream = []UpstreamConfig{
			// 已有账号 A 的托管渠道（冷启动后成卡，accountUID=A）
			{ChannelUID: "ch-c1", AccountUID: "acct-A", AutoManaged: true, Name: "site-c", BaseURL: "https://guard.example.com/v1", ServiceType: "openai", APIKeys: []string{"k1"}, Status: "active"},
		}
		c.GeminiUpstream = []UpstreamConfig{
			// 已有 provider 卡
			{ChannelUID: "ch-g1", ProviderID: "someprov", Name: "prov-g", BaseURL: "https://prov.example.com/v1", ServiceType: "gemini", APIKeys: []string{"k1"}, Status: "active"},
		}
	})
	logicalLoadAndRebuild(t, cm)

	// 账号 B 的新渠道，站点与账号 A 相同：不得并入 A 卡
	if err := cm.AddUpstream(UpstreamConfig{
		ChannelUID: "ch-m1", AccountUID: "acct-B", AutoManaged: true, Name: "site-m",
		BaseURL: "https://guard.example.com/v1", ServiceType: "claude",
		APIKeys: []string{"k2"}, Status: "active",
	}); err != nil {
		t.Fatalf("AddUpstream(acct-B) 失败: %v", err)
	}
	cfg := cm.GetConfig()
	bUID := strings.TrimSpace(cfg.Upstream[0].LogicalChannelUID)
	aUID := strings.TrimSpace(cfg.ChatUpstream[0].LogicalChannelUID)
	if bUID == aUID {
		t.Errorf("不同账号渠道被错误并入同一逻辑卡: %s", aUID)
	}

	// 裸手工渠道，站点与 provider 卡相同：不得并入 provider 卡
	if err := cm.AddResponsesUpstream(UpstreamConfig{
		ChannelUID: "ch-r1", Name: "prov-r", BaseURL: "https://prov.example.com/v1",
		ServiceType: "responses", APIKeys: []string{"k3"}, Status: "active",
	}); err != nil {
		t.Fatalf("AddResponsesUpstream 失败: %v", err)
	}
	cfg = cm.GetConfig()
	rUID := strings.TrimSpace(cfg.ResponsesUpstream[0].LogicalChannelUID)
	gUID := strings.TrimSpace(cfg.GeminiUpstream[0].LogicalChannelUID)
	if rUID == gUID {
		t.Errorf("裸手工渠道被错误并入 provider 卡: %s", gUID)
	}
}
