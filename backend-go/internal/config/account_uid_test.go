package config

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestCredentialUIDStableWithinAccount(t *testing.T) {
	first := GenerateCredentialUID("acct_test", "sk-test")
	second := GenerateCredentialUID("acct_test", "sk-test")
	if first == "" || first != second {
		t.Fatalf("credential uid 不稳定: first=%q second=%q", first, second)
	}
	if other := GenerateCredentialUID("acct_other", "sk-test"); other == first {
		t.Fatalf("不同账号不应共享 credential uid: %q", other)
	}
}

func TestEnsureAccountUIDsGroupsLegacyProviderRoutes(t *testing.T) {
	cm := &ConfigManager{config: Config{
		Upstream:          []UpstreamConfig{{Name: "mimo-main-claude", ProviderID: "mimo", AutoManaged: true, APIKeys: []string{"sk-b", "sk-a"}}},
		ChatUpstream:      []UpstreamConfig{{Name: "mimo-main-chat", ProviderID: "mimo", AutoManaged: true, APIKeys: []string{"sk-a", "sk-b"}}},
		ResponsesUpstream: []UpstreamConfig{{Name: "mimo-main-codex", ProviderID: "mimo", AutoManaged: true, APIKeys: []string{"sk-a", "sk-b"}}},
		GeminiUpstream:    []UpstreamConfig{{Name: "mimo-main-gemini", ProviderID: "mimo", AutoManaged: true, APIKeys: []string{"sk-a", "sk-b"}}},
	}}
	if !cm.ensureAccountUIDs() {
		t.Fatal("旧 provider routes 应触发 accountUid 回填")
	}
	want := cm.config.Upstream[0].AccountUID
	if want == "" || cm.config.ChatUpstream[0].AccountUID != want || cm.config.ResponsesUpstream[0].AccountUID != want || cm.config.GeminiUpstream[0].AccountUID != want {
		t.Fatalf("旧 MiMo 多协议 route 未聚合到同一账号")
	}
}

func TestMergeManagedProviderAccountsCombinesKeysAndRoutes(t *testing.T) {
	cm := &ConfigManager{config: Config{
		ManagedAccounts: []ManagedAccountConfig{
			{AccountUID: "acct-old", ProviderID: "mimo", Name: "mimo-old"},
			{AccountUID: "acct-new", ProviderID: "mimo", Name: "mimo-new"},
		},
		Upstream: []UpstreamConfig{
			{AccountUID: "acct-old", ChannelUID: "ch-msg-old", Name: "mimo-old-claude", ProviderID: "mimo", AutoManaged: true, ServiceType: "claude", APIKeys: []string{"sk-a"}, APIKeyConfigs: []APIKeyConfig{{Key: "sk-a", BaseURL: "https://a.example/anthropic"}}},
			{AccountUID: "acct-new", ChannelUID: "ch-msg-new", Name: "mimo-new-claude", ProviderID: "mimo", AutoManaged: true, ServiceType: "claude", APIKeys: []string{"sk-b"}, APIKeyConfigs: []APIKeyConfig{{Key: "sk-b", BaseURL: "https://b.example/anthropic"}}},
		},
		ChatUpstream: []UpstreamConfig{
			{AccountUID: "acct-old", ChannelUID: "ch-chat-old", Name: "mimo-old-chat", ProviderID: "mimo", AutoManaged: true, ServiceType: "openai", APIKeys: []string{"sk-a"}, APIKeyConfigs: []APIKeyConfig{{Key: "sk-a", BaseURL: "https://a.example/v1"}}},
			{AccountUID: "acct-new", ChannelUID: "ch-chat-new", Name: "mimo-new-chat", ProviderID: "mimo", AutoManaged: true, ServiceType: "openai", APIKeys: []string{"sk-b"}, APIKeyConfigs: []APIKeyConfig{{Key: "sk-b", BaseURL: "https://b.example/v1"}}},
		},
	}}

	if !cm.mergeManagedProviderAccounts() {
		t.Fatal("重复 provider 账号应触发合并")
	}
	if len(cm.config.Upstream) != 1 || len(cm.config.ChatUpstream) != 1 {
		t.Fatalf("每种协议应只保留一条 route: messages=%d chat=%d", len(cm.config.Upstream), len(cm.config.ChatUpstream))
	}
	for _, channel := range []UpstreamConfig{cm.config.Upstream[0], cm.config.ChatUpstream[0]} {
		if channel.AccountUID != "acct-new" || len(channel.APIKeys) != 2 {
			t.Fatalf("route 未归并到最近账号或 Key 未合并: %+v", channel)
		}
	}
	if cm.config.Upstream[0].ChannelUID != "ch-msg-new" || cm.config.ChatUpstream[0].ChannelUID != "ch-chat-new" {
		t.Fatalf("应保留最近账号的 route 身份")
	}
	if len(cm.config.ManagedAccounts) != 1 || len(cm.config.ManagedAccounts[0].Credentials) != 2 {
		t.Fatalf("账号凭证池未合并: %+v", cm.config.ManagedAccounts)
	}
}

func TestLoadConfigMergesPersistedProviderCredentialsWithoutLoss(t *testing.T) {
	dir := t.TempDir()
	configPath := dir + "/config.json"
	data := `{
  "managedAccounts": [
    {"accountUid":"acct-old","providerId":"mimo","name":"mimo-old","credentials":[{"credentialUid":"cred-old","apiKey":"sk-old"}]},
    {"accountUid":"acct-new","providerId":"mimo","name":"mimo-new","credentials":[{"credentialUid":"cred-new","apiKey":"sk-new"}]}
  ],
  "upstream": [
    {"accountUid":"acct-old","channelUid":"ch-old","providerId":"mimo","name":"mimo-old","serviceType":"claude","autoManaged":true,"status":"active","baseUrl":"https://old.example/anthropic","apiKeyConfigs":[{"credentialUid":"cred-old","baseUrl":"https://old.example/anthropic"}]},
    {"accountUid":"acct-new","channelUid":"ch-new","providerId":"mimo","name":"mimo-new","serviceType":"claude","autoManaged":true,"status":"active","baseUrl":"https://new.example/anthropic","apiKeyConfigs":[{"credentialUid":"cred-new","baseUrl":"https://new.example/anthropic"}]}
  ],
  "chatUpstream": [], "responsesUpstream": [], "geminiUpstream": [], "imagesUpstream": [], "vectorsUpstream": []
}`
	if err := os.WriteFile(configPath, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	cm, err := NewConfigManager(configPath, dir+"/backups")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cm.Close() })
	cfg := cm.GetConfig()
	if len(cfg.ManagedAccounts) != 1 || len(cfg.ManagedAccounts[0].Credentials) != 2 {
		t.Fatalf("持久化凭证迁移丢失: %+v", cfg.ManagedAccounts)
	}
	if len(cfg.Upstream) != 1 || len(cfg.Upstream[0].APIKeys) != 2 {
		t.Fatalf("route 运行时 Key 迁移丢失: %+v", cfg.Upstream)
	}
}

func TestMergeManagedProviderAccountsManualSuspensionWins(t *testing.T) {
	cm := &ConfigManager{config: Config{
		ManagedAccounts: []ManagedAccountConfig{
			{AccountUID: "acct-old", ProviderID: "mimo", Name: "old"},
			{AccountUID: "acct-new", ProviderID: "mimo", Name: "new"},
		},
		Upstream: []UpstreamConfig{
			{AccountUID: "acct-old", ProviderID: "mimo", AutoManaged: true, Status: "active", APIKeys: []string{"sk-old"}},
			{AccountUID: "acct-new", ProviderID: "mimo", AutoManaged: true, Status: "suspended", SuspensionSource: SuspensionSourceManual, APIKeys: []string{"sk-new"}},
		},
	}}
	if !cm.mergeManagedProviderAccounts() {
		t.Fatal("重复 provider 账号应触发合并")
	}
	got := cm.config.Upstream[0]
	if got.Status != "suspended" || got.SuspensionSource != SuspensionSourceManual {
		t.Fatalf("人工暂停被 active 路由覆盖: status=%q source=%q", got.Status, got.SuspensionSource)
	}
}

func TestMergeManagedProviderAccountsPreservesUIDOnlyBindingWithRuntimeKey(t *testing.T) {
	cm := &ConfigManager{config: Config{
		ManagedAccounts: []ManagedAccountConfig{
			{AccountUID: "acct-old", ProviderID: "mimo", Credentials: []ManagedAccountCredential{{CredentialUID: "cred-old", APIKey: "sk-old"}}},
			{AccountUID: "acct-new", ProviderID: "mimo", Credentials: []ManagedAccountCredential{{CredentialUID: "cred-new", APIKey: "sk-new"}}},
		},
		Upstream: []UpstreamConfig{
			{AccountUID: "acct-old", ProviderID: "mimo", AutoManaged: true, APIKeyConfigs: []APIKeyConfig{{CredentialUID: "cred-old"}}},
			{AccountUID: "acct-new", ProviderID: "mimo", AutoManaged: true, APIKeys: []string{"sk-new"}, APIKeyConfigs: []APIKeyConfig{{Key: "sk-new", CredentialUID: "cred-new"}}},
		},
	}}
	if !cm.mergeManagedProviderAccounts() {
		t.Fatal("重复 provider 账号应触发合并")
	}
	foundOld := false
	for _, keyConfig := range cm.config.Upstream[0].APIKeyConfigs {
		if keyConfig.CredentialUID == "cred-old" {
			foundOld = true
		}
	}
	if !foundOld {
		t.Fatalf("merge 丢失 UID-only legacy 绑定: %+v", cm.config.Upstream[0].APIKeyConfigs)
	}
	seen := map[string]string{}
	for _, credential := range cm.config.ManagedAccounts[0].Credentials {
		seen[credential.CredentialUID] = credential.APIKey
	}
	if seen["cred-old"] != "sk-old" {
		t.Fatalf("merge/sync 丢失 cred-old: %+v", seen)
	}
}

func TestSyncManagedAccountsPreservesUIDOnlyCredentialWithRuntimeKey(t *testing.T) {
	cfg := Config{
		ManagedAccounts: []ManagedAccountConfig{{
			AccountUID: "acct", ProviderID: "mimo", Credentials: []ManagedAccountCredential{
				{CredentialUID: "cred-old", APIKey: "sk-old"},
				{CredentialUID: "cred-new", APIKey: "sk-new"},
			},
		}},
		Upstream: []UpstreamConfig{{
			AccountUID: "acct", ProviderID: "mimo", AutoManaged: true,
			APIKeys: []string{"sk-new"},
			APIKeyConfigs: []APIKeyConfig{
				{Key: "sk-new", CredentialUID: "cred-new"},
				{CredentialUID: "cred-old"},
			},
		}},
	}
	cfg.syncManagedAccountsFromChannels()
	if len(cfg.ManagedAccounts) != 1 || len(cfg.ManagedAccounts[0].Credentials) != 2 {
		t.Fatalf("UID-only legacy 凭证丢失: %+v", cfg.ManagedAccounts)
	}
	seen := map[string]string{}
	for _, credential := range cfg.ManagedAccounts[0].Credentials {
		seen[credential.CredentialUID] = credential.APIKey
	}
	if seen["cred-old"] != "sk-old" || seen["cred-new"] != "sk-new" {
		t.Fatalf("凭证绑定异常: %+v", seen)
	}
}

func TestHydrateManagedCredentialsRuntimeOnlyDoesNotReportModified(t *testing.T) {
	cfg := Config{
		ManagedAccounts: []ManagedAccountConfig{{
			AccountUID: "acct", ProviderID: "mimo",
			Credentials: []ManagedAccountCredential{{CredentialUID: "cred", APIKey: "sk-runtime"}},
		}},
		Upstream: []UpstreamConfig{{
			AccountUID: "acct", ProviderID: "mimo", AutoManaged: true,
			APIKeyConfigs: []APIKeyConfig{{CredentialUID: "cred"}},
		}},
	}
	if cfg.hydrateManagedAccountCredentials() {
		t.Fatal("纯运行时 Key 补水不应标记持久化结构已修改")
	}
	if len(cfg.Upstream[0].APIKeys) != 1 || cfg.Upstream[0].APIKeys[0] != "sk-runtime" || cfg.Upstream[0].APIKeyConfigs[0].Key != "sk-runtime" {
		t.Fatalf("运行时补水失败: %+v", cfg.Upstream[0])
	}
}

func TestUpdateAccountChannelsUpdatesAllRoutes(t *testing.T) {
	cm := &ConfigManager{config: Config{
		Upstream:     []UpstreamConfig{{AccountUID: "acct_test", ChannelUID: "ch_messages", ServiceType: "claude", ProviderID: "mimo", AutoManaged: true}},
		ChatUpstream: []UpstreamConfig{{AccountUID: "acct_test", ChannelUID: "ch_chat", ServiceType: "openai", ProviderID: "mimo", AutoManaged: true}},
	}}
	updates := []AccountChannelUpdate{
		{ChannelUID: "ch_messages", Name: "mimo-claude", APIKeys: []string{"sk-a", "sk-b"}, APIKeyConfig: []APIKeyConfig{{Key: "sk-a", BaseURL: "https://m.example/anthropic"}, {Key: "sk-b", BaseURL: "https://m.example/anthropic"}}, BaseURLs: []string{"https://m.example/anthropic"}},
		{ChannelUID: "ch_chat", Name: "mimo-chat", APIKeys: []string{"sk-a", "sk-b"}, APIKeyConfig: []APIKeyConfig{{Key: "sk-a", BaseURL: "https://m.example/v1"}, {Key: "sk-b", BaseURL: "https://m.example/v1"}}, BaseURLs: []string{"https://m.example/v1"}},
	}
	// 测试不落盘，只验证更新主体；临时配置文件让 saveConfigLocked 可正常写入。
	dir := t.TempDir()
	cm.configFile = dir + "/config.json"
	cm.backupDir = dir + "/backups"
	if err := cm.UpdateAccountChannels("acct_test", updates); err != nil {
		t.Fatalf("UpdateAccountChannels 失败: %v", err)
	}
	if len(cm.config.Upstream[0].APIKeys) != 2 || len(cm.config.ChatUpstream[0].APIKeys) != 2 {
		t.Fatalf("账号 Key 未同步到全部 route")
	}
	messageCred := cm.config.Upstream[0].APIKeyConfigs[0].CredentialUID
	chatCred := cm.config.ChatUpstream[0].APIKeyConfigs[0].CredentialUID
	if messageCred == "" || messageCred != chatCred {
		t.Fatalf("同账号同 Key 应共享 credential uid: messages=%q chat=%q", messageCred, chatCred)
	}
	data, err := os.ReadFile(cm.configFile)
	if err != nil {
		t.Fatalf("读取持久化配置失败: %v", err)
	}
	if count := strings.Count(string(data), "sk-a"); count != 1 {
		t.Fatalf("账号级 Key 应只持久化一次，sk-a 出现 %d 次", count)
	}
	var persisted Config
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatalf("解析持久化配置失败: %v", err)
	}
	persisted.hydrateManagedAccountCredentials()
	if len(persisted.Upstream[0].APIKeys) != 2 || len(persisted.ChatUpstream[0].APIKeys) != 2 {
		t.Fatalf("加载时未从账号凭证恢复 route 运行时 Key")
	}
	if err := cm.RenameManagedAccount("acct_test", "mimo-renamed"); err != nil {
		t.Fatalf("RenameManagedAccount 失败: %v", err)
	}
	if cm.config.Upstream[0].Name != "mimo-renamed-claude" || cm.config.ChatUpstream[0].Name != "mimo-renamed-chat" {
		t.Fatalf("账号重命名未同步全部协议 route")
	}
	removed, err := cm.DeleteAccountChannels("acct_test")
	if err != nil || len(removed) != 2 {
		t.Fatalf("DeleteAccountChannels removed=%v err=%v", removed, err)
	}
	if len(cm.config.Upstream) != 0 || len(cm.config.ChatUpstream) != 0 || len(cm.config.ManagedAccounts) != 0 {
		t.Fatalf("账号级删除未清理全部 route 或凭证源")
	}
}

func TestApplyAccountChannelChangesDoesNotPartiallyCommit(t *testing.T) {
	cm := &ConfigManager{config: Config{
		Upstream: []UpstreamConfig{{
			AccountUID: "acct_test", ChannelUID: "ch_messages", Name: "mimo", ServiceType: "claude",
			ProviderID: "mimo", AutoManaged: true, APIKeys: []string{"sk-old"},
		}},
		ResponsesUpstream: []UpstreamConfig{{
			AccountUID: "acct_other", ChannelUID: "ch_taken", Name: "mimo-codex", ServiceType: "openai",
		}},
	}}
	updates := []AccountChannelUpdate{{
		ChannelUID: "ch_messages", Name: "mimo-claude", APIKeys: []string{"sk-new"},
		APIKeyConfig: []APIKeyConfig{{Key: "sk-new", BaseURL: "https://m.example/anthropic"}},
		BaseURLs:     []string{"https://m.example/anthropic"},
	}}
	additions := []AccountChannelAddition{
		{Kind: "chat", Upstream: UpstreamConfig{
			AccountUID: "acct_test", ChannelUID: "ch_chat", Name: "mimo-chat", ServiceType: "openai",
			ProviderID: "mimo", AutoManaged: true, BaseURL: "https://m.example/v1",
			APIKeys: []string{"sk-new"}, APIKeyConfigs: []APIKeyConfig{{Key: "sk-new", BaseURL: "https://m.example/v1"}},
		}},
		{Kind: "responses", Upstream: UpstreamConfig{
			AccountUID: "acct_test", ChannelUID: "ch_responses", Name: "mimo-codex", ServiceType: "openai",
			ProviderID: "mimo", AutoManaged: true, BaseURL: "https://m.example/v1",
			APIKeys: []string{"sk-new"}, APIKeyConfigs: []APIKeyConfig{{Key: "sk-new", BaseURL: "https://m.example/v1"}},
		}},
	}

	if err := cm.ApplyAccountChannelChanges("acct_test", updates, additions); err == nil {
		t.Fatal("第二条新增渠道名称冲突时应拒绝整个事务")
	}
	cfg := cm.GetConfig()
	if len(cfg.Upstream) != 1 || len(cfg.Upstream[0].APIKeys) != 1 || cfg.Upstream[0].APIKeys[0] != "sk-old" {
		t.Fatalf("失败事务修改了原渠道: %+v", cfg.Upstream)
	}
	if len(cfg.ChatUpstream) != 0 || len(cfg.ResponsesUpstream) != 1 {
		t.Fatalf("失败事务留下部分新增渠道: chat=%+v responses=%+v", cfg.ChatUpstream, cfg.ResponsesUpstream)
	}
}

func TestApplyAccountChannelChangesKeepsMemoryOnSaveFailure(t *testing.T) {
	cm := &ConfigManager{
		config: Config{Upstream: []UpstreamConfig{{
			AccountUID: "acct_test", ChannelUID: "ch_messages", Name: "mimo", ServiceType: "claude",
			ProviderID: "mimo", AutoManaged: true, APIKeys: []string{"sk-old"},
		}}},
		configFile: t.TempDir() + "/missing/config.json",
	}
	updates := []AccountChannelUpdate{{
		ChannelUID: "ch_messages", Name: "mimo", APIKeys: []string{"sk-new"},
		APIKeyConfig: []APIKeyConfig{{Key: "sk-new", BaseURL: "https://m.example/anthropic"}},
		BaseURLs:     []string{"https://m.example/anthropic"},
	}}
	if err := cm.UpdateAccountChannels("acct_test", updates); err == nil {
		t.Fatal("配置目录不存在时应保存失败")
	}
	if got := cm.GetConfig().Upstream[0].APIKeys; len(got) != 1 || got[0] != "sk-old" {
		t.Fatalf("保存失败后内存配置被替换: %v", got)
	}
}
