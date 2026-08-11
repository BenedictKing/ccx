package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestBindManagedAccountCompshareConsoleAppliesKeyConcurrencyAcrossRoutes(t *testing.T) {
	const (
		accountUID  = "acct_compshare"
		credentialA = "cred_a"
		credentialB = "cred_b"
		keyA        = "sk-cp-a"
		keyB        = "sk-cp-b"
	)
	newChannel := func(uid string) UpstreamConfig {
		return UpstreamConfig{
			AccountUID: accountUID, ChannelUID: uid, ProviderID: "compshare", AutoManaged: true,
			APIKeys: []string{keyA, keyB}, APIKeyConfigs: []APIKeyConfig{
				{Key: keyA, CredentialUID: credentialA, RateLimitMaxConcurrent: 2},
				{Key: keyB, CredentialUID: credentialB, RateLimitMaxConcurrent: 4},
			},
			RateLimitMaxConcurrent: 3,
		}
	}
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	manager := &ConfigManager{
		configFile: configPath,
		backupDir:  filepath.Join(dir, "backups"),
		config: Config{
			ManagedAccounts: []ManagedAccountConfig{{
				AccountUID: accountUID, ProviderID: "compshare",
				Credentials: []ManagedAccountCredential{{CredentialUID: credentialA, APIKey: keyA}, {CredentialUID: credentialB, APIKey: keyB}},
			}},
			Upstream:          []UpstreamConfig{newChannel("ch_messages")},
			ChatUpstream:      []UpstreamConfig{newChannel("ch_chat")},
			ResponsesUpstream: []UpstreamConfig{newChannel("ch_responses")},
			GeminiUpstream:    []UpstreamConfig{newChannel("ch_gemini")},
			ImagesUpstream:    []UpstreamConfig{newChannel("ch_images")},
			VectorsUpstream:   []UpstreamConfig{newChannel("ch_vectors")},
		},
	}

	if err := manager.BindManagedAccountCompshareConsole(accountUID, credentialA, CompshareConsoleCredential{
		Cookie: "U_USER_EMAIL=test; U_CSRF_TOKEN=csrf", ConcurrencyLimit: 10,
	}); err != nil {
		t.Fatal(err)
	}
	for _, channel := range manager.GetAccountChannels(accountUID) {
		if channel.Upstream.RateLimitMaxConcurrent != 3 {
			t.Fatalf("%s 渠道级限速被意外覆盖: %d", channel.Kind, channel.Upstream.RateLimitMaxConcurrent)
		}
		if got := channel.Upstream.APIKeyConfigs[0].RateLimitMaxConcurrent; got != 10 {
			t.Fatalf("%s 目标 Key 并发=%d, want 10", channel.Kind, got)
		}
		if got := channel.Upstream.APIKeyConfigs[1].RateLimitMaxConcurrent; got != 4 {
			t.Fatalf("%s 其他 Key 并发=%d, want 4", channel.Kind, got)
		}
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var persisted Config
	if err := json.Unmarshal(data, &persisted); err != nil {
		t.Fatal(err)
	}
	found := false
	// 波 3 后落盘只含 ChannelsV3（六数组不再持久化），在权威形态的协议成员上断言。
	for _, channel := range persisted.ChannelsV3 {
		for _, member := range channel.Protocols {
			for _, keyConfig := range member.Upstream.APIKeyConfigs {
				if keyConfig.CredentialUID == credentialA {
					found = true
					if keyConfig.RateLimitMaxConcurrent != 10 {
						t.Fatalf("并发上限未持久化: %+v", keyConfig)
					}
				}
			}
		}
	}
	if !found {
		t.Fatal("持久化配置中缺少目标凭证")
	}
}
