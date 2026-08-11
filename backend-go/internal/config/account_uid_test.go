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

func TestMergeManagedProviderAccountsCombinesSameSiteKeysAndRoutes(t *testing.T) {
	cm := &ConfigManager{config: Config{
		ManagedAccounts: []ManagedAccountConfig{
			{AccountUID: "acct-old", ProviderID: "legacy", Name: "old"},
			{AccountUID: "acct-new", ProviderID: "mimo", Name: "new"},
		},
		Upstream: []UpstreamConfig{
			{AccountUID: "acct-old", ChannelUID: "ch-msg-old", Name: "old", ProviderID: "legacy", AutoManaged: true, ServiceType: "claude", BaseURL: "HTTPS://API.EXAMPLE/v1/", APIKeys: []string{"sk-a"}},
			{AccountUID: "acct-new", ChannelUID: "ch-msg-new", Name: "new", ProviderID: "mimo", AutoManaged: true, ServiceType: "claude", BaseURL: "https://api.example", APIKeys: []string{"sk-b"}},
		},
		GeminiUpstream: []UpstreamConfig{
			{AccountUID: "acct-old", ChannelUID: "ch-gemini-old", Name: "old", ProviderID: "legacy", AutoManaged: true, ServiceType: "gemini", BaseURL: "https://api.example/v1beta/", APIKeys: []string{"sk-a"}},
			{AccountUID: "acct-new", ChannelUID: "ch-gemini-new", Name: "new", ProviderID: "mimo", AutoManaged: true, ServiceType: "gemini", BaseURL: "https://api.example", APIKeys: []string{"sk-b"}},
		},
	}}

	if !cm.mergeManagedProviderAccounts() {
		t.Fatal("同 BaseURL 站点账号应触发合并")
	}
	if len(cm.config.Upstream) != 1 || len(cm.config.GeminiUpstream) != 1 {
		t.Fatalf("每种协议应只保留一条 route: messages=%d gemini=%d", len(cm.config.Upstream), len(cm.config.GeminiUpstream))
	}
	for _, channel := range []UpstreamConfig{cm.config.Upstream[0], cm.config.GeminiUpstream[0]} {
		if channel.AccountUID != "acct-new" || channel.ProviderID != "mimo" || len(channel.APIKeys) != 2 {
			t.Fatalf("route 未归并到最近账号: %+v", channel)
		}
	}
	if cm.config.Upstream[0].ChannelUID != "ch-msg-new" || cm.config.GeminiUpstream[0].ChannelUID != "ch-gemini-new" {
		t.Fatal("应保留最近账号的 route 身份")
	}
	if len(cm.config.ManagedAccounts) != 1 || len(cm.config.ManagedAccounts[0].Credentials) != 2 {
		t.Fatalf("账号凭证池未合并: %+v", cm.config.ManagedAccounts)
	}
}

func TestMergeChannelsByBaseURLRegardlessOfManagementMode(t *testing.T) {
	cm := &ConfigManager{config: Config{
		Upstream: []UpstreamConfig{{
			AccountUID: "acct-messages", ChannelUID: "ch-messages", Name: "ai-prism-messages",
			ServiceType: "responses", BaseURL: "https://ai.prism.uno/v1", APIKeys: []string{"sk-messages"},
		}},
		ResponsesUpstream: []UpstreamConfig{{
			AccountUID: "acct-responses", ChannelUID: "ch-responses", Name: "ai-prism-responses",
			ServiceType: "responses", BaseURL: "https://ai.prism.uno", APIKeys: []string{"sk-responses"},
		}},
		GeminiUpstream: []UpstreamConfig{{
			AccountUID: "acct-gemini", ChannelUID: "ch-gemini", Name: "prism-gemini",
			ServiceType: "gemini", BaseURL: "https://ai.prism.uno/v1beta", APIKeys: []string{"sk-gemini"},
		}},
	}}
	if !cm.mergeManagedProviderAccounts() {
		t.Fatal("普通渠道的同 BaseURL 站点应触发账号身份合并")
	}
	want := cm.config.GeminiUpstream[0].AccountUID
	if want == "" || cm.config.Upstream[0].AccountUID != want || cm.config.ResponsesUpstream[0].AccountUID != want {
		t.Fatalf("跨协议渠道未归并到同一 AccountUID: messages=%q responses=%q gemini=%q",
			cm.config.Upstream[0].AccountUID, cm.config.ResponsesUpstream[0].AccountUID, cm.config.GeminiUpstream[0].AccountUID)
	}
	if len(cm.config.Upstream) != 1 || len(cm.config.ResponsesUpstream) != 1 || len(cm.config.GeminiUpstream) != 1 {
		t.Fatal("不同协议渠道不应互相删除")
	}
}

func TestMergeManagedProviderAccountsKeepsDifferentSitesSeparate(t *testing.T) {
	cm := &ConfigManager{config: Config{
		ManagedAccounts: []ManagedAccountConfig{
			{AccountUID: "acct-a", ProviderID: "mimo"},
			{AccountUID: "acct-b", ProviderID: "mimo"},
		},
		Upstream: []UpstreamConfig{
			{AccountUID: "acct-a", ProviderID: "mimo", AutoManaged: true, BaseURL: "https://a.example/v1"},
			{AccountUID: "acct-b", ProviderID: "mimo", AutoManaged: true, BaseURL: "https://b.example/v1"},
		},
	}}
	if cm.mergeManagedProviderAccounts() {
		t.Fatal("相同 provider 的不同站点不应合并")
	}
	if len(cm.config.Upstream) != 2 || len(cm.config.ManagedAccounts) != 2 {
		t.Fatalf("不同站点被误合并: channels=%d accounts=%d", len(cm.config.Upstream), len(cm.config.ManagedAccounts))
	}
}

func TestMergeManagedProviderAccountsSiteBoundaries(t *testing.T) {
	tests := []struct {
		name  string
		left  string
		right string
	}{
		{name: "different port", left: "https://api.example:8443/v1", right: "https://api.example/v1"},
		{name: "different tenant path", left: "https://api.example/tenant-a/v1", right: "https://api.example/tenant-b/v1"},
		{name: "different query", left: "https://api.example/v1?tenant=a", right: "https://api.example/v1?tenant=b"},
		{name: "hash semantics", left: "https://api.example/v1#", right: "https://api.example/v1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cm := &ConfigManager{config: Config{Upstream: []UpstreamConfig{
				{AccountUID: "acct-a", AutoManaged: true, BaseURL: tt.left},
				{AccountUID: "acct-b", AutoManaged: true, BaseURL: tt.right},
			}}}
			if cm.mergeManagedProviderAccounts() {
				t.Fatalf("站点边界被误合并: %q / %q", tt.left, tt.right)
			}
		})
	}
}

func TestMergeManagedProviderAccountsRequiresURL(t *testing.T) {
	cm := &ConfigManager{config: Config{Upstream: []UpstreamConfig{
		{AccountUID: "acct-a", ProviderID: "mimo", AutoManaged: true},
		{AccountUID: "acct-b", ProviderID: "mimo", AutoManaged: true},
	}}}
	if cm.mergeManagedProviderAccounts() {
		t.Fatal("无 BaseURL 的渠道不应自动合并")
	}
}

func TestMergeManagedProviderAccountsIsIdempotent(t *testing.T) {
	cm := &ConfigManager{config: Config{
		ManagedAccounts: []ManagedAccountConfig{{AccountUID: "acct-old"}, {AccountUID: "acct-new"}},
		Upstream: []UpstreamConfig{
			{AccountUID: "acct-old", AutoManaged: true, BaseURL: "https://api.example/v1/", APIKeys: []string{"sk-old"}},
			{AccountUID: "acct-new", AutoManaged: true, BaseURL: "https://api.example", APIKeys: []string{"sk-new"}},
		},
	}}
	if !cm.mergeManagedProviderAccounts() {
		t.Fatal("首次迁移应发生合并")
	}
	first, err := json.Marshal(cm.config)
	if err != nil {
		t.Fatal(err)
	}
	if cm.mergeManagedProviderAccounts() {
		t.Fatal("第二次迁移不应报告变更")
	}
	second, err := json.Marshal(cm.config)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("站点合并迁移不幂等")
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
    {"accountUid":"acct-old","channelUid":"ch-old","providerId":"mimo","name":"mimo-old","serviceType":"claude","autoManaged":true,"status":"active","baseUrl":"https://api.example/v1","apiKeyConfigs":[{"credentialUid":"cred-old","baseUrl":"https://api.example/v1"}]},
    {"accountUid":"acct-new","channelUid":"ch-new","providerId":"mimo","name":"mimo-new","serviceType":"claude","autoManaged":true,"status":"active","baseUrl":"https://api.example","apiKeyConfigs":[{"credentialUid":"cred-new","baseUrl":"https://api.example"}]}
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

func TestLoadConfigBaseURLMergeIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	configPath := dir + "/config.json"
	data := `{
  "managedAccounts": [
    {"accountUid":"acct-old","providerId":"legacy","name":"old","credentials":[{"credentialUid":"cred-old","apiKey":"sk-old"}]},
    {"accountUid":"acct-new","providerId":"mimo","name":"new","credentials":[{"credentialUid":"cred-new","apiKey":"sk-new"}]}
  ],
  "upstream": [
    {"accountUid":"acct-old","channelUid":"ch-old","providerId":"legacy","name":"old","serviceType":"claude","autoManaged":true,"status":"active","baseUrl":"HTTPS://API.EXAMPLE/v1/","apiKeyConfigs":[{"credentialUid":"cred-old","baseUrl":"HTTPS://API.EXAMPLE/v1/"}]},
    {"accountUid":"acct-new","channelUid":"ch-new","providerId":"mimo","name":"new","serviceType":"claude","autoManaged":true,"status":"active","baseUrl":"https://api.example","apiKeyConfigs":[{"credentialUid":"cred-new","baseUrl":"https://api.example"}]}
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
	if err := cm.Close(); err != nil {
		t.Fatal(err)
	}
	first, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var migrated Config
	if err := json.Unmarshal(first, &migrated); err != nil {
		t.Fatal(err)
	}
	// 波 3 后落盘只含 ChannelsV3（六数组不再持久化），合并结果在权威形态上断言。
	if len(migrated.ChannelsV3) != 1 || len(migrated.ManagedAccounts) != 1 {
		t.Fatalf("升级未按 BaseURL 合并: channels=%d accounts=%d", len(migrated.ChannelsV3), len(migrated.ManagedAccounts))
	}
	if migrated.ChannelsV3[0].AccountUID != "acct-new" || migrated.ChannelsV3[0].ProviderID != "mimo" {
		t.Fatalf("升级未保留最近账号身份: %+v", migrated.ChannelsV3[0])
	}

	cm, err = NewConfigManager(configPath, dir+"/backups-second")
	if err != nil {
		t.Fatal(err)
	}
	if err := cm.Close(); err != nil {
		t.Fatal(err)
	}
	second, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(first) != string(second) {
		t.Fatal("第二次加载再次改写了已迁移配置")
	}
}

func TestMergeManagedProviderAccountsManualSuspensionWins(t *testing.T) {
	cm := &ConfigManager{config: Config{
		ManagedAccounts: []ManagedAccountConfig{
			{AccountUID: "acct-old", ProviderID: "mimo", Name: "old"},
			{AccountUID: "acct-new", ProviderID: "mimo", Name: "new"},
		},
		Upstream: []UpstreamConfig{
			{AccountUID: "acct-old", ProviderID: "mimo", AutoManaged: true, BaseURL: "https://api.example/v1", Status: "active", APIKeys: []string{"sk-old"}},
			{AccountUID: "acct-new", ProviderID: "mimo", AutoManaged: true, BaseURL: "https://api.example/v1", Status: "suspended", SuspensionSource: SuspensionSourceManual, APIKeys: []string{"sk-new"}},
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
			{AccountUID: "acct-old", ProviderID: "mimo", AutoManaged: true, BaseURL: "https://api.example/v1", APIKeyConfigs: []APIKeyConfig{{CredentialUID: "cred-old"}}},
			{AccountUID: "acct-new", ProviderID: "mimo", AutoManaged: true, BaseURL: "https://api.example", APIKeys: []string{"sk-new"}, APIKeyConfigs: []APIKeyConfig{{Key: "sk-new", CredentialUID: "cred-new"}}},
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
	// 波 3 后落盘只含 ChannelsV3：模拟加载路径——先从 V3 重建六数组，再从账号凭证补水。
	if _, err := applyAuthoritativeChannelsAsLoadSource(&persisted); err != nil {
		t.Fatalf("从 ChannelsV3 重建六数组失败: %v", err)
	}
	persisted.hydrateManagedAccountCredentials()
	if len(persisted.Upstream) != 1 || len(persisted.ChatUpstream) != 1 ||
		len(persisted.Upstream[0].APIKeys) != 2 || len(persisted.ChatUpstream[0].APIKeys) != 2 {
		t.Fatalf("加载时未从账号凭证恢复 route 运行时 Key: %+v", persisted)
	}
	if err := cm.RenameManagedAccount("acct_test", "mimo-renamed"); err != nil {
		t.Fatalf("RenameManagedAccount 失败: %v", err)
	}
	if cm.config.Upstream[0].Name != "mimo-renamed-claude" || cm.config.ChatUpstream[0].Name != "mimo-renamed-chat" {
		t.Fatalf("账号重命名未同步全部协议 route")
	}
	removed, skipped, err := cm.DeleteAccountChannels("acct_test")
	if err != nil || len(removed) != 2 || len(skipped) != 0 {
		t.Fatalf("DeleteAccountChannels removed=%v skipped=%v err=%v", removed, skipped, err)
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

// TestDeleteAccountChannelsSkipsNonManaged 验证级联删除只删除自动托管渠道，
// 非托管渠道保留并解除账号关联。
func TestDeleteAccountChannelsSkipsNonManaged(t *testing.T) {
	cm := &ConfigManager{
		config: Config{
			Upstream: []UpstreamConfig{
				{AccountUID: "acct_test", ChannelUID: "ch_managed", Name: "x-claude", AutoManaged: true, APIKeys: []string{"sk-a"}},
				{AccountUID: "acct_test", ChannelUID: "ch_manual", Name: "x-manual", AutoManaged: false, APIKeys: []string{"sk-b"}},
			},
			ResponsesUpstream: []UpstreamConfig{
				{AccountUID: "acct_test", ChannelUID: "ch_codex", Name: "x-codex", AutoManaged: false, APIKeys: []string{"sk-c"}},
			},
			ManagedAccounts: []ManagedAccountConfig{{AccountUID: "acct_test", Name: "x"}},
		},
		configFile: t.TempDir() + "/config.json",
	}

	removed, skipped, err := cm.DeleteAccountChannels("acct_test")
	if err != nil {
		t.Fatalf("DeleteAccountChannels 失败: %v", err)
	}
	if len(removed) != 1 || removed[0] != "ch_managed" {
		t.Fatalf("removed=%v，期望仅删除 ch_managed", removed)
	}
	if len(skipped) != 2 {
		t.Fatalf("skipped=%v，期望跳过 2 个非托管渠道", skipped)
	}

	cfg := cm.GetConfig()
	if len(cfg.Upstream) != 1 || cfg.Upstream[0].ChannelUID != "ch_manual" {
		t.Fatalf("非托管 messages 渠道应保留: %+v", cfg.Upstream)
	}
	if cfg.Upstream[0].AccountUID != "" {
		t.Fatalf("保留的非托管渠道应解除账号关联，got accountUid=%q", cfg.Upstream[0].AccountUID)
	}
	if len(cfg.ResponsesUpstream) != 1 || cfg.ResponsesUpstream[0].AccountUID != "" {
		t.Fatalf("非托管 responses 渠道应保留并解除关联: %+v", cfg.ResponsesUpstream)
	}
	if len(cfg.ManagedAccounts) != 0 {
		t.Fatal("账号凭证源应被清理")
	}
}
