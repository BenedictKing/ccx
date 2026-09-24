package config

import (
	"os"
	"path/filepath"
	"testing"
)

// 渠道编辑只补发变更 Key 的 trimmed 配置（托管渠道单卡补发），未提及的 Key 配置必须保留。
func TestMergeAndNormalizeAPIKeyConfigsKeepsUnmentionedKeyConfigs(t *testing.T) {
	existing := []APIKeyConfig{
		{Key: "sk-1", CredentialUID: "cred-1", GroupMultiplier: ptrFloat64(0.08), MultiplierSource: "manual"},
		{Key: "sk-2", CredentialUID: "cred-2"},
	}
	incoming := []APIKeyConfig{{Key: "sk-2", CredentialUID: "cred-2", GroupMultiplier: ptrFloat64(0.5)}}

	got := mergeAndNormalizeAPIKeyConfigs([]string{"sk-1", "sk-2"}, existing, incoming)
	if len(got) != 2 {
		t.Fatalf("expected 2 configs, got %d: %+v", len(got), got)
	}
	if got[0].GroupMultiplier == nil || *got[0].GroupMultiplier != 0.08 {
		t.Fatalf("expected untouched key multiplier preserved, got %+v", got[0])
	}
	if got[0].MultiplierSource != "manual" {
		t.Fatalf("expected untouched key source preserved, got %+v", got[0])
	}
	if got[1].GroupMultiplier == nil || *got[1].GroupMultiplier != 0.5 {
		t.Fatalf("expected patched key multiplier applied, got %+v", got[1])
	}
}

// 统一列表把各协议路由的 Key 配置合并展示，但存储是每张物理卡一份：
// 单卡保存 Key 倍率必须同步到同逻辑渠道的兄弟路由，否则用户改完再打开看到的仍是旧值。
func TestKeyMultiplierUpdateSyncsAcrossLogicalGroupRoutes(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	initial := `{
  "chatUpstream": [{
    "channelUid": "ch_chat",
    "accountUid": "acct_site",
    "name": "site-chat",
    "serviceType": "openai",
    "baseUrl": "https://relay.example.com",
    "apiKeys": ["sk-1", "sk-2"],
    "apiKeyConfigs": [
      {"key": "sk-1", "credentialUid": "cred-1"},
      {"key": "sk-2", "credentialUid": "cred-2"}
    ]
  }],
  "responsesUpstream": [{
    "channelUid": "ch_resp",
    "accountUid": "acct_site",
    "name": "site-codex",
    "serviceType": "responses",
    "baseUrl": "https://relay.example.com",
    "apiKeys": ["sk-1", "sk-2"],
    "apiKeyConfigs": [
      {"key": "sk-1", "credentialUid": "cred-1", "groupMultiplier": 0.08},
      {"key": "sk-2", "credentialUid": "cred-2"}
    ]
  }]
}`
	if err := os.WriteFile(configPath, []byte(initial), 0o644); err != nil {
		t.Fatalf("写入初始配置失败: %v", err)
	}
	cm, err := NewConfigManager(configPath, "")
	if err != nil {
		t.Fatalf("NewConfigManager() error = %v", err)
	}
	t.Cleanup(func() { _ = cm.Close() })

	chatLoc, chat, found := cm.FindUpstreamByUID("ch_chat")
	if !found {
		t.Fatalf("chat 路由缺失: %+v", cm.GetConfig().ChatUpstream)
	}
	_, resp, found := cm.FindUpstreamByUID("ch_resp")
	if !found {
		t.Fatalf("responses 路由缺失: %+v", cm.GetConfig().ResponsesUpstream)
	}
	if chat.LogicalChannelUID == "" || chat.LogicalChannelUID != resp.LogicalChannelUID {
		t.Fatalf("两条路由未归入同一逻辑渠道: chat=%q resp=%q", chat.LogicalChannelUID, resp.LogicalChannelUID)
	}

	if _, err := UpdateUpstreamByKind(cm, chatLoc, UpstreamUpdate{
		APIKeyConfigs: []APIKeyConfig{{Key: "sk-2", CredentialUID: "cred-2", GroupMultiplier: ptrFloat64(0.5)}},
	}); err != nil {
		t.Fatalf("UpdateUpstreamByKind() error = %v", err)
	}

	_, resp, _ = cm.FindUpstreamByUID("ch_resp")
	if len(resp.APIKeyConfigs) != 2 {
		t.Fatalf("expected 2 configs on responses route, got %+v", resp.APIKeyConfigs)
	}
	var siblingSk2 *APIKeyConfig
	for i := range resp.APIKeyConfigs {
		if resp.APIKeyConfigs[i].Key == "sk-2" {
			siblingSk2 = &resp.APIKeyConfigs[i]
		}
	}
	if siblingSk2 == nil {
		t.Fatalf("responses 路由缺少 sk-2 配置: %+v", resp.APIKeyConfigs)
	}
	if siblingSk2.GroupMultiplier == nil || *siblingSk2.GroupMultiplier != 0.5 {
		t.Fatalf("expected sibling route multiplier synced to 0.5, got %+v", siblingSk2)
	}
}

// new_api 倍率由订阅同步服务逐路由写入（含订阅身份字段），渠道编辑不做跨协议扩散。
func TestKeyMultiplierUpdateSkipsNewApiManagedKeys(t *testing.T) {
	cm := &ConfigManager{config: Config{
		ChatUpstream: []UpstreamConfig{
			{
				ChannelUID:        "ch_chat",
				LogicalChannelUID: "lc_site",
				APIKeys:           []string{"sk-1"},
				APIKeyConfigs: []APIKeyConfig{
					{Key: "sk-1", MultiplierSource: "new_api", GroupMultiplier: ptrFloat64(2), SourceSubscriptionUID: "sub-1"},
				},
			},
		},
		ResponsesUpstream: []UpstreamConfig{
			{
				ChannelUID:        "ch_resp",
				LogicalChannelUID: "lc_site",
				APIKeys:           []string{"sk-1"},
				APIKeyConfigs: []APIKeyConfig{
					{Key: "sk-1", MultiplierSource: "new_api", SourceSubscriptionUID: "sub-1"},
				},
			},
		},
	}}
	upstream := &cm.config.ChatUpstream[0]
	cm.syncKeyMultiplierAcrossLogicalGroupLocked(upstream, []APIKeyConfig{{Key: "sk-1"}})

	if got := cm.config.ResponsesUpstream[0].APIKeyConfigs[0].GroupMultiplier; got != nil {
		t.Fatalf("expected new_api key multiplier untouched, got %v", *got)
	}
}
