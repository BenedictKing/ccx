package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newLogicalTestCM 在临时目录创建配置管理器，先按 setup 写入内存再落盘。
// 加载过程会自动触发旧配置迁移回填逻辑渠道。
func newLogicalTestCM(t *testing.T, setup func(*Config)) *ConfigManager {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	cfg := Config{
		Upstream:          []UpstreamConfig{},
		ChatUpstream:      []UpstreamConfig{},
		ResponsesUpstream: []UpstreamConfig{},
		GeminiUpstream:    []UpstreamConfig{},
		ImagesUpstream:    []UpstreamConfig{},
		VectorsUpstream:   []UpstreamConfig{},
	}
	if setup != nil {
		setup(&cfg)
	}
	// 确保 ChannelUID 已分配
	assignChannelUIDs(&cfg)
	data, err := json.MarshalIndent(&cfg, "", "  ")
	if err != nil {
		t.Fatalf("序列化配置失败: %v", err)
	}
	if err := os.WriteFile(cfgPath, data, 0600); err != nil {
		t.Fatalf("写入配置失败: %v", err)
	}
	cm, err := NewConfigManager(cfgPath, filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatalf("NewConfigManager 失败: %v", err)
	}
	t.Cleanup(func() { cm.Close() })
	return cm
}

func assignChannelUIDs(c *Config) {
	for i := range c.Upstream {
		if c.Upstream[i].ChannelUID == "" {
			c.Upstream[i].ChannelUID = GenerateChannelUID()
		}
	}
	for i := range c.ChatUpstream {
		if c.ChatUpstream[i].ChannelUID == "" {
			c.ChatUpstream[i].ChannelUID = GenerateChannelUID()
		}
	}
	for i := range c.ResponsesUpstream {
		if c.ResponsesUpstream[i].ChannelUID == "" {
			c.ResponsesUpstream[i].ChannelUID = GenerateChannelUID()
		}
	}
	for i := range c.GeminiUpstream {
		if c.GeminiUpstream[i].ChannelUID == "" {
			c.GeminiUpstream[i].ChannelUID = GenerateChannelUID()
		}
	}
	for i := range c.ImagesUpstream {
		if c.ImagesUpstream[i].ChannelUID == "" {
			c.ImagesUpstream[i].ChannelUID = GenerateChannelUID()
		}
	}
	for i := range c.VectorsUpstream {
		if c.VectorsUpstream[i].ChannelUID == "" {
			c.VectorsUpstream[i].ChannelUID = GenerateChannelUID()
		}
	}
}

// logicalLoadAndRebuild 模拟“从盘上加载后首次访问”的回填路径：
// 取出内存，抹掉 LogicalChannelUID/Name，然后重新触发回填。
func logicalLoadAndRebuild(t *testing.T, cm *ConfigManager) {
	t.Helper()
	cfg := cm.GetConfig()
	all := []*[]UpstreamConfig{&cfg.Upstream, &cfg.ChatUpstream, &cfg.ResponsesUpstream, &cfg.GeminiUpstream, &cfg.ImagesUpstream, &cfg.VectorsUpstream}
	for _, slice := range all {
		for i := range *slice {
			(*slice)[i].LogicalChannelUID = ""
			(*slice)[i].LogicalName = ""
		}
	}
	cfg.LogicalChannels = nil
	cfg.LogicalChannelSchemaVersion = 0
	cm.ReloadFromMemory(&cfg)
}

// TestRebuildLogicalChannels_GroupsByAccount 验证同 accountUID 合并。
func TestRebuildLogicalChannels_GroupsByAccount(t *testing.T) {
	accountUID := "acct-1"
	cm := newLogicalTestCM(t, func(c *Config) {
		c.Upstream = []UpstreamConfig{
			{ChannelUID: "ch-m1", AccountUID: accountUID, ProviderID: "mimo", Name: "site", BaseURL: "https://api.example.com/v1", ServiceType: "claude", APIKeys: []string{"k1"}, Status: "active"},
		}
		c.ChatUpstream = []UpstreamConfig{
			{ChannelUID: "ch-c1", AccountUID: accountUID, ProviderID: "mimo", Name: "site", BaseURL: "https://api.example.com/v1", ServiceType: "openai", APIKeys: []string{"k1"}, Status: "active"},
		}
	})
	logicalLoadAndRebuild(t, cm)
	logicals := cm.ListLogicalChannels()
	if len(logicals) != 1 {
		t.Fatalf("期望归为 1 个逻辑渠道，实际 %d 个", len(logicals))
	}
	if len(logicals[0].Protocols) != 2 {
		t.Fatalf("期望 2 个 protocol，实际 %d", len(logicals[0].Protocols))
	}
	if logicals[0].AccountUID != accountUID {
		t.Fatalf("AccountUID 丢失: %s", logicals[0].AccountUID)
	}
}

// TestRebuildLogicalChannels_GroupsByProviderAndSiteIdentity 验证同 provider + 同 site 合并。
// 注意：手工渠道使用同 site 端点（claude/openai 共用 /v1 路径），归组后才合并；
// 跨服务类型不同路径的（如 messages 走 /anthropic，chat 走根）视为不同站，不合并。
func TestRebuildLogicalChannels_GroupsByProviderAndSiteIdentity(t *testing.T) {
	cm := newLogicalTestCM(t, func(c *Config) {
		c.Upstream = []UpstreamConfig{
			{ChannelUID: "ch-m1", ProviderID: "deepseek", Name: "deepseek-m", BaseURL: "https://api.deepseek.com/v1", ServiceType: "claude", APIKeys: []string{"k1"}, Status: "active"},
		}
		c.ChatUpstream = []UpstreamConfig{
			{ChannelUID: "ch-c1", ProviderID: "deepseek", Name: "deepseek-c", BaseURL: "https://api.deepseek.com/v1", ServiceType: "openai", APIKeys: []string{"k1"}, Status: "active"},
		}
	})
	logicalLoadAndRebuild(t, cm)
	logicals := cm.ListLogicalChannels()
	if len(logicals) != 1 {
		t.Fatalf("期望归为 1 个逻辑渠道，实际 %d 个", len(logicals))
	}
	if logicals[0].ProviderID != "deepseek" {
		t.Fatalf("ProviderID 丢失: %s", logicals[0].ProviderID)
	}
}

// TestRebuildLogicalChannels_ManualChannelsSameSiteGrouped 验证手工同站合并。
func TestRebuildLogicalChannels_ManualChannelsSameSiteGrouped(t *testing.T) {
	cm := newLogicalTestCM(t, func(c *Config) {
		c.Upstream = []UpstreamConfig{
			{ChannelUID: "ch-m1", Name: "manual-m", BaseURL: "https://manual.example.com/v1", ServiceType: "claude", APIKeys: []string{"k1"}, Status: "active"},
		}
		c.ChatUpstream = []UpstreamConfig{
			{ChannelUID: "ch-c1", Name: "manual-c", BaseURL: "https://manual.example.com/v1", ServiceType: "openai", APIKeys: []string{"k1"}, Status: "active"},
		}
	})
	logicalLoadAndRebuild(t, cm)
	logicals := cm.ListLogicalChannels()
	if len(logicals) != 1 {
		t.Fatalf("期望归为 1 个逻辑渠道，实际 %d 个", len(logicals))
	}
}

// TestRebuildLogicalChannels_DifferentProvidersNotGrouped 验证不同 provider 不合并。
func TestRebuildLogicalChannels_DifferentProvidersNotGrouped(t *testing.T) {
	cm := newLogicalTestCM(t, func(c *Config) {
		c.Upstream = []UpstreamConfig{
			{ChannelUID: "ch-m1", ProviderID: "deepseek", Name: "ds-m", BaseURL: "https://api.deepseek.com/anthropic", ServiceType: "claude", APIKeys: []string{"k1"}, Status: "active"},
		}
		c.ChatUpstream = []UpstreamConfig{
			{ChannelUID: "ch-c1", ProviderID: "mimo", Name: "mimo-c", BaseURL: "https://api.mimo.example.com", ServiceType: "openai", APIKeys: []string{"k1"}, Status: "active"},
		}
	})
	logicalLoadAndRebuild(t, cm)
	logicals := cm.ListLogicalChannels()
	if len(logicals) != 2 {
		t.Fatalf("不同 provider 应归为 2 个逻辑渠道，实际 %d 个", len(logicals))
	}
}

// TestRebuildLogicalChannels_DifferentAccountUIDIsolated 验证不同 AccountUID 即使 site 一致也不合并。
// 注意：loadConfig 阶段 `mergeManagedProviderAccounts` 会把同 site 的渠道合并为同一 AccountUID，
// 因此这里直接走 `RebuildLogicalChannels`（绕开 merge），验证归组规则本身。
func TestRebuildLogicalChannels_DifferentAccountUIDIsolated(t *testing.T) {
	cm := newLogicalTestCM(t, nil)
	cfg := Config{
		Upstream: []UpstreamConfig{
			{ChannelUID: "ch-m1", AccountUID: "acct-1", Name: "a1-m", BaseURL: "https://site.example.com/v1", ServiceType: "claude", APIKeys: []string{"k1"}, Status: "active"},
		},
		ChatUpstream: []UpstreamConfig{
			{ChannelUID: "ch-c1", AccountUID: "acct-2", Name: "a2-c", BaseURL: "https://site.example.com/v1", ServiceType: "openai", APIKeys: []string{"k1"}, Status: "active"},
		},
		ResponsesUpstream: []UpstreamConfig{}, GeminiUpstream: []UpstreamConfig{}, ImagesUpstream: []UpstreamConfig{}, VectorsUpstream: []UpstreamConfig{},
	}
	cm.ReloadFromMemory(&cfg)
	logicals := cm.ListLogicalChannels()
	if len(logicals) != 2 {
		t.Fatalf("不同 AccountUID 应保持 2 个 logical，实际 %d 个", len(logicals))
	}
}

// TestRebuildLogicalChannels_TenantPathKeptSeparate 验证不同 tenant path 不合并。
func TestRebuildLogicalChannels_TenantPathKeptSeparate(t *testing.T) {
	cm := newLogicalTestCM(t, func(c *Config) {
		c.Upstream = []UpstreamConfig{
			{ChannelUID: "ch-m1", Name: "tenantA", BaseURL: "https://api.openai.com/v1", ServiceType: "claude", APIKeys: []string{"k1"}, Status: "active"},
		}
		c.ChatUpstream = []UpstreamConfig{
			{ChannelUID: "ch-c1", Name: "tenantB", BaseURL: "https://api.openai.com/tenantB/v1", ServiceType: "openai", APIKeys: []string{"k1"}, Status: "active"},
		}
	})
	logicalLoadAndRebuild(t, cm)
	logicals := cm.ListLogicalChannels()
	if len(logicals) != 2 {
		t.Fatalf("不同 tenant path 应保持 2 个 logical，实际 %d 个", len(logicals))
	}
}

// TestCreateLogicalChannel_AllProtocolsCreatedAtomically 验证一次 POST 创建多个协议物理渠道。
func TestCreateLogicalChannel_AllProtocolsCreatedAtomically(t *testing.T) {
	cm := newLogicalTestCM(t, nil)
	in := CreateLogicalChannelInput{
		Name:     "测试渠道",
		Kind:     LogicalChannelKindLLM,
		BaseURLs: []string{"https://api.test.com/v1"},
		Protocols: []CreateLogicalChannelProtocol{
			{Kind: "messages", ServiceType: "claude", APIKeys: []string{"k1"}},
			{Kind: "chat", ServiceType: "openai", APIKeys: []string{"k1"}},
		},
	}
	logical, err := cm.CreateLogicalChannel(in)
	if err != nil {
		t.Fatalf("CreateLogicalChannel 失败: %v", err)
	}
	if logical == nil {
		t.Fatalf("返回的 logical 为空")
	}
	if len(logical.Protocols) != 2 {
		t.Fatalf("期望 2 个 protocol，实际 %d", len(logical.Protocols))
	}
	cfg := cm.GetConfig()
	if len(cfg.Upstream) != 1 {
		t.Fatalf("messages 数组期望 1 条，实际 %d", len(cfg.Upstream))
	}
	if len(cfg.ChatUpstream) != 1 {
		t.Fatalf("chat 数组期望 1 条，实际 %d", len(cfg.ChatUpstream))
	}
	if cfg.Upstream[0].LogicalChannelUID != logical.LogicalChannelUID {
		t.Fatalf("物理渠道 LogicalChannelUID 未回写")
	}
}

// TestCreateLogicalChannel_RollsBackOnFailure 验证中间失败时回滚。
func TestCreateLogicalChannel_RollsBackOnFailure(t *testing.T) {
	cm := newLogicalTestCM(t, nil)
	// 第二个 protocol 是非法 kind，会触发错误并回滚第一个
	in := CreateLogicalChannelInput{
		Name:     "回滚测试",
		Kind:     LogicalChannelKindLLM,
		BaseURLs: []string{"https://api.test.com/v1"},
		Protocols: []CreateLogicalChannelProtocol{
			{Kind: "messages", ServiceType: "claude", APIKeys: []string{"k1"}},
			{Kind: "bogus", ServiceType: "claude", APIKeys: []string{"k1"}},
		},
	}
	if _, err := cm.CreateLogicalChannel(in); err == nil {
		t.Fatalf("期望错误，但成功了")
	}
	cfg := cm.GetConfig()
	if len(cfg.Upstream) != 0 {
		t.Fatalf("期望 messages 已回滚到 0，实际 %d", len(cfg.Upstream))
	}
	if len(cm.ListLogicalChannels()) != 0 {
		t.Fatalf("期望 logical 列表为空")
	}
}

// TestDeleteLogicalChannel_RemovesAllPhysicalChannels 验证 DELETE 一次删除所有物理渠道。
func TestDeleteLogicalChannel_RemovesAllPhysicalChannels(t *testing.T) {
	cm := newLogicalTestCM(t, nil)
	logical, err := cm.CreateLogicalChannel(CreateLogicalChannelInput{
		Name:     "待删除",
		Kind:     LogicalChannelKindLLM,
		BaseURLs: []string{"https://api.test.com/v1"},
		Protocols: []CreateLogicalChannelProtocol{
			{Kind: "messages", ServiceType: "claude", APIKeys: []string{"k1"}},
			{Kind: "chat", ServiceType: "openai", APIKeys: []string{"k1"}},
			{Kind: "responses", ServiceType: "responses", APIKeys: []string{"k1"}},
		},
	})
	if err != nil {
		t.Fatalf("CreateLogicalChannel 失败: %v", err)
	}
	removed, err := cm.DeleteLogicalChannel(logical.LogicalChannelUID)
	if err != nil {
		t.Fatalf("DeleteLogicalChannel 失败: %v", err)
	}
	if len(removed) != 3 {
		t.Fatalf("期望返回 3 条已删除物理渠道，实际 %d", len(removed))
	}
	cfg := cm.GetConfig()
	if len(cfg.Upstream) != 0 || len(cfg.ChatUpstream) != 0 || len(cfg.ResponsesUpstream) != 0 {
		t.Fatalf("DELETE 后物理数组应为空，up=%d chat=%d res=%d", len(cfg.Upstream), len(cfg.ChatUpstream), len(cfg.ResponsesUpstream))
	}
	if len(cm.ListLogicalChannels()) != 0 {
		t.Fatalf("DELETE 后 logical 列表应为空")
	}
}

// TestUpdateLogicalChannel_AddAndRemoveProtocols 验证更新时增删 protocol。
func TestUpdateLogicalChannel_AddAndRemoveProtocols(t *testing.T) {
	cm := newLogicalTestCM(t, nil)
	logical, err := cm.CreateLogicalChannel(CreateLogicalChannelInput{
		Name:     "更新测试",
		Kind:     LogicalChannelKindLLM,
		BaseURLs: []string{"https://api.test.com/v1"},
		Protocols: []CreateLogicalChannelProtocol{
			{Kind: "messages", ServiceType: "claude", APIKeys: []string{"k1"}},
		},
	})
	if err != nil {
		t.Fatalf("CreateLogicalChannel 失败: %v", err)
	}
	// 增 chat + 删 messages
	updated, err := cm.UpdateLogicalChannel(UpdateLogicalChannelInput{
		LogicalChannelUID: logical.LogicalChannelUID,
		Removals:          []string{"messages"},
		Protocols: []UpdateLogicalChannelProtocol{
			{Kind: "chat", ServiceType: "openai", APIKeys: []string{"k1"}},
		},
	})
	if err != nil {
		t.Fatalf("UpdateLogicalChannel 失败: %v", err)
	}
	if len(updated.Protocols) != 1 || updated.Protocols[0].Kind != "chat" {
		t.Fatalf("更新后协议列表不正确: %+v", updated.Protocols)
	}
	cfg := cm.GetConfig()
	if len(cfg.Upstream) != 0 {
		t.Fatalf("messages 应已被删除")
	}
	if len(cfg.ChatUpstream) != 1 {
		t.Fatalf("chat 应被新增")
	}
}

// TestOldConfigMigration_BackfillsLogicalChannelUID 验证旧配置加载时回填 LogicalChannelUID。
func TestOldConfigMigration_BackfillsLogicalChannelUID(t *testing.T) {
	cm := newLogicalTestCM(t, func(c *Config) {
		c.Upstream = []UpstreamConfig{
			{ChannelUID: "ch-m1", ProviderID: "mimo", Name: "mimo-m", BaseURL: "https://api.mimo.example.com/v1", ServiceType: "claude", APIKeys: []string{"k1"}, Status: "active"},
		}
		c.ChatUpstream = []UpstreamConfig{
			{ChannelUID: "ch-c1", ProviderID: "mimo", Name: "mimo-c", BaseURL: "https://api.mimo.example.com/v1", ServiceType: "openai", APIKeys: []string{"k1"}, Status: "active"},
		}
	})
	logicalLoadAndRebuild(t, cm)
	cfg := cm.GetConfig()
	if cfg.Upstream[0].LogicalChannelUID == "" {
		t.Fatalf("messages 物理渠道未回填 LogicalChannelUID")
	}
	if cfg.ChatUpstream[0].LogicalChannelUID == "" {
		t.Fatalf("chat 物理渠道未回填 LogicalChannelUID")
	}
	if cfg.Upstream[0].LogicalChannelUID != cfg.ChatUpstream[0].LogicalChannelUID {
		t.Fatalf("同 logical 的两个物理渠道 UID 不一致")
	}
	if cfg.LogicalChannelSchemaVersion != LogicalChannelSchemaVersion {
		t.Fatalf("schema version 未更新: %d", cfg.LogicalChannelSchemaVersion)
	}
}

// TestOldConfigMigration_AllChannelKindsBackfilled 验证六类数组均被回填。
func TestOldConfigMigration_AllChannelKindsBackfilled(t *testing.T) {
	cm := newLogicalTestCM(t, func(c *Config) {
		c.Upstream = []UpstreamConfig{{ChannelUID: "u1", BaseURL: "https://a/", ServiceType: "claude", Name: "m", ProviderID: "p", APIKeys: []string{"k"}, Status: "active"}}
		c.ChatUpstream = []UpstreamConfig{{ChannelUID: "u2", BaseURL: "https://a/", ServiceType: "openai", Name: "c", ProviderID: "p", APIKeys: []string{"k"}, Status: "active"}}
		c.ResponsesUpstream = []UpstreamConfig{{ChannelUID: "u3", BaseURL: "https://a/", ServiceType: "responses", Name: "r", ProviderID: "p", APIKeys: []string{"k"}, Status: "active"}}
		c.GeminiUpstream = []UpstreamConfig{{ChannelUID: "u4", BaseURL: "https://a/", ServiceType: "gemini", Name: "g", ProviderID: "p", APIKeys: []string{"k"}, Status: "active"}}
		c.ImagesUpstream = []UpstreamConfig{{ChannelUID: "u5", BaseURL: "https://a/", ServiceType: "openai", Name: "i", ProviderID: "p", APIKeys: []string{"k"}, Status: "active"}}
		c.VectorsUpstream = []UpstreamConfig{{ChannelUID: "u6", BaseURL: "https://a/", ServiceType: "openai", Name: "v", ProviderID: "p", APIKeys: []string{"k"}, Status: "active"}}
	})
	logicalLoadAndRebuild(t, cm)
	cfg := cm.GetConfig()
	all := []UpstreamConfig{}
	all = append(all, cfg.Upstream...)
	all = append(all, cfg.ChatUpstream...)
	all = append(all, cfg.ResponsesUpstream...)
	all = append(all, cfg.GeminiUpstream...)
	all = append(all, cfg.ImagesUpstream...)
	all = append(all, cfg.VectorsUpstream...)
	uids := map[string]int{}
	for _, ch := range all {
		if ch.LogicalChannelUID == "" {
			t.Fatalf("物理渠道 %s 未回填", ch.ChannelUID)
		}
		uids[ch.LogicalChannelUID]++
	}
	// images/vectors 与 llm 的 SiteIdentity 不同（无版本前缀），但单一 provider + 单一 baseURL 仍归 1
	if len(uids) != 1 {
		t.Fatalf("期望归为 1 个 logical，实际 %d 个 UID", len(uids))
	}
}

// TestCreateLogicalChannel_DuplicateSiteRejected 验证同站点冲突拒绝。
func TestCreateLogicalChannel_DuplicateSiteRejected(t *testing.T) {
	cm := newLogicalTestCM(t, nil)
	if _, err := cm.CreateLogicalChannel(CreateLogicalChannelInput{
		Name:      "渠道A",
		BaseURLs:  []string{"https://a.example.com/v1"},
		Protocols: []CreateLogicalChannelProtocol{{Kind: "messages", ServiceType: "claude", APIKeys: []string{"k1"}}},
	}); err != nil {
		t.Fatalf("首次创建失败: %v", err)
	}
	if _, err := cm.CreateLogicalChannel(CreateLogicalChannelInput{
		Name:      "渠道A2",
		BaseURLs:  []string{"https://a.example.com/v1"},
		Protocols: []CreateLogicalChannelProtocol{{Kind: "chat", ServiceType: "openai", APIKeys: []string{"k1"}}},
	}); err == nil || !strings.Contains(err.Error(), "站点") {
		t.Fatalf("期望站点冲突错误，实际: %v", err)
	}
}

// TestUpdateLogicalChannel_RemoveAllProtocolsRejected 验证不能删到 0 个 protocol。
func TestUpdateLogicalChannel_RemoveAllProtocolsRejected(t *testing.T) {
	cm := newLogicalTestCM(t, nil)
	logical, err := cm.CreateLogicalChannel(CreateLogicalChannelInput{
		Name:      "至少一个",
		BaseURLs:  []string{"https://a.example.com/v1"},
		Protocols: []CreateLogicalChannelProtocol{{Kind: "messages", ServiceType: "claude", APIKeys: []string{"k1"}}},
	})
	if err != nil {
		t.Fatalf("CreateLogicalChannel 失败: %v", err)
	}
	_, err = cm.UpdateLogicalChannel(UpdateLogicalChannelInput{
		LogicalChannelUID: logical.LogicalChannelUID,
		Removals:          []string{"messages"},
	})
	if err == nil || !strings.Contains(err.Error(), "至少需要保留一个协议") {
		t.Fatalf("期望拒绝删空，实际: %v", err)
	}
}
