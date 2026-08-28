package autopilot

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
)

type healFakeAdapter struct {
	tokens      []NewApiToken
	listCalls   int
	revealByID  map[int]string
	revealErrID map[int]error
}

func (f *healFakeAdapter) VerifyWithFallback(context.Context, string, string, string, string) (*NewApiUserSelf, string, error) {
	return &NewApiUserSelf{ID: 7, Username: "bob", Quota: 50000, UsedQuota: 1000}, "1", nil
}

func (f *healFakeAdapter) FetchGroups(context.Context, string, string, string, string) (map[string]float64, error) {
	return map[string]float64{"default": 1}, nil
}

func (f *healFakeAdapter) FetchModels(context.Context, string, string, string, string) ([]string, error) {
	return nil, nil
}

func (f *healFakeAdapter) ListTokens(context.Context, string, string, string, string, int, int) ([]NewApiToken, error) {
	f.listCalls++
	return f.tokens, nil
}

func (f *healFakeAdapter) GetTokenKey(_ context.Context, _ string, _ string, _ string, _ string, tokenID int) (string, error) {
	if err, exists := f.revealErrID[tokenID]; exists {
		return "", err
	}
	if key, exists := f.revealByID[tokenID]; exists {
		return key, nil
	}
	return "", fmt.Errorf("token %d 无明文", tokenID)
}

func healDesired(tokenID int64, group string) newApiDesiredKey {
	return newApiDesiredKey{
		keyUID:    StableKeyUID("newapi-ch-1", tokenID),
		name:      fmt.Sprintf("ccx-%d", tokenID),
		group:     group,
		status:    newApiSyncStatusFresh,
		tokenID:   tokenID,
		ratio:     1,
		limit:     1,
		updatedAt: time.Now(),
	}
}

func healChannel(uid string, apiKeys []string, configs []config.APIKeyConfig) config.UpstreamConfig {
	return config.UpstreamConfig{
		ChannelUID:    uid,
		Name:          "heal-target",
		ServiceType:   "claude",
		BaseURL:       "https://new-api.example.com",
		APIKeys:       apiKeys,
		APIKeyConfigs: configs,
	}
}

func healService(t *testing.T, channels ...config.UpstreamConfig) *NewApiSubscriptionSyncService {
	t.Helper()
	return &NewApiSubscriptionSyncService{cfgManager: newProxyTestConfigManager(t, channels...)}
}

// 注入的明文 key 必须同时并入渠道 APIKeys：调度与 keypool 候选只遍历 APIKeys，
// 仅写 configs 的 key 不参与调用。
func TestInjectProvisionedKeysMergesAPIKeys(t *testing.T) {
	svc := healService(t, healChannel("ch-1", []string{"sk-existing"}, []config.APIKeyConfig{{Key: "sk-existing"}}))
	profile := &SubscriptionProfile{SubscriptionUID: "newapi-ch-1", LinkedChannelUIDs: []string{"ch-1"}}

	if err := svc.injectProvisionedKeys(profile, []newApiDesiredKey{healDesired(7, "default")}, map[int64]string{7: "sk-new"}); err != nil {
		t.Fatalf("injectProvisionedKeys 失败: %v", err)
	}

	up := svc.cfgManager.GetConfig().Upstream[0]
	if len(up.APIKeys) != 2 || up.APIKeys[0] != "sk-existing" || up.APIKeys[1] != "sk-new" {
		t.Fatalf("期望 APIKeys=[sk-existing sk-new]，实际 %v", up.APIKeys)
	}
	found := false
	for _, cfg := range up.APIKeyConfigs {
		if cfg.SourceRemoteTokenID == 7 {
			found = true
			if cfg.Key != "sk-new" || cfg.SourceSubscriptionUID != "newapi-ch-1" || cfg.QuotaGroup != "default" {
				t.Fatalf("注入 config 字段不完整: %+v", cfg)
			}
		}
	}
	if !found {
		t.Fatalf("期望存在 tokenID=7 的 config，实际 %+v", up.APIKeyConfigs)
	}
}

// 渠道侧被误删的自动接入 key：按 tokenID 从远端列表找回，掩码 key 经揭示端点换回明文。
func TestHealMissingProvisionedKeysRecoversDeletedKeys(t *testing.T) {
	svc := healService(t, healChannel("ch-1", nil, nil))
	profile := &SubscriptionProfile{SubscriptionUID: "newapi-ch-1", LinkedChannelUIDs: []string{"ch-1"}}
	adapter := &healFakeAdapter{
		tokens: []NewApiToken{
			{ID: 11, Key: "sk-plain11", Name: "ccx-11", Group: "Surprise"},
			{ID: 12, Key: "sk-ma***ed12", Name: "ccx-12", Group: "default"},
		},
		revealByID: map[int]string{12: "revealed12"},
	}
	desired := []newApiDesiredKey{healDesired(11, "Surprise"), healDesired(12, "default")}

	svc.healMissingProvisionedKeys(context.Background(), profile, adapter, "https://new-api.example.com", "access", "1", NewApiAuthModeBearer, desired)

	up := svc.cfgManager.GetConfig().Upstream[0]
	if len(up.APIKeys) != 2 || up.APIKeys[0] != "sk-plain11" || up.APIKeys[1] != "sk-revealed12" {
		t.Fatalf("期望找回两个明文 key（掩码揭示+前缀规范），实际 %v", up.APIKeys)
	}
	byToken := map[int64]config.APIKeyConfig{}
	for _, cfg := range up.APIKeyConfigs {
		byToken[cfg.SourceRemoteTokenID] = cfg
	}
	if len(byToken) != 2 {
		t.Fatalf("期望两个 config，实际 %+v", up.APIKeyConfigs)
	}
	for _, id := range []int64{11, 12} {
		cfg, ok := byToken[id]
		if !ok || cfg.SourceSubscriptionUID != "newapi-ch-1" || cfg.KeyUID != StableKeyUID("newapi-ch-1", id) {
			t.Fatalf("tokenID=%d 的 config 不完整: %+v", id, cfg)
		}
	}
}

// 远端 token 也被删除的项跳过，绝不注入空 key；已有全部 config 时不发起远端请求。
func TestHealMissingProvisionedKeysSkipsRemoteDeleted(t *testing.T) {
	svc := healService(t, healChannel("ch-1", nil, nil))
	profile := &SubscriptionProfile{SubscriptionUID: "newapi-ch-1", LinkedChannelUIDs: []string{"ch-1"}}
	adapter := &healFakeAdapter{
		tokens: []NewApiToken{{ID: 11, Key: "sk-plain11", Name: "ccx-11", Group: "Surprise"}},
	}
	desired := []newApiDesiredKey{healDesired(11, "Surprise"), healDesired(99, "default")}

	svc.healMissingProvisionedKeys(context.Background(), profile, adapter, "https://new-api.example.com", "access", "1", NewApiAuthModeBearer, desired)

	up := svc.cfgManager.GetConfig().Upstream[0]
	if len(up.APIKeys) != 1 || up.APIKeys[0] != "sk-plain11" {
		t.Fatalf("期望仅找回远端存在的 key，实际 %v", up.APIKeys)
	}
	for _, cfg := range up.APIKeyConfigs {
		if cfg.Key == "" {
			t.Fatalf("不应注入空 key config: %+v", cfg)
		}
	}
}

// 主账号已删除（无订阅级凭证）：SyncNow 不报错，仅同步各子账号。
func TestSyncNowWithoutPrimaryCredentialStillSyncsAccounts(t *testing.T) {
	store, err := NewSubscriptionStoreWithDB(newTestDB(t))
	if err != nil {
		t.Fatalf("创建 store 失败: %v", err)
	}
	profile := &SubscriptionProfile{
		SubscriptionUID: "newapi-ch-noprimary",
		Provider:        "new_api",
		BaseURL:         "https://new-api.example.com",
		Accounts: []NewApiAccount{{
			AccountUID:  "acct_sub_1",
			AccessToken: "sub-token",
			Status:      "active",
			ProvisionedKeys: []NewApiProvisionedKey{
				{Name: "ccx-sub", Group: "default", GroupMultiplier: 1, TokenID: 21},
			},
		}},
	}
	if err := store.Create(profile); err != nil {
		t.Fatalf("创建订阅失败: %v", err)
	}
	svc := &NewApiSubscriptionSyncService{store: store, adapter: &healFakeAdapter{}, now: time.Now}

	result, err := svc.SyncNow(context.Background(), "newapi-ch-noprimary")
	if err != nil {
		t.Fatalf("无主凭证不应报错: %v", err)
	}
	if len(result.Keys) != 1 || result.Keys[0].KeyUID != StableKeyUID("newapi-ch-noprimary", 21) {
		t.Fatalf("子账号 key 状态应被同步: %+v", result.Keys)
	}
	updated := store.Get("newapi-ch-noprimary")
	if len(updated.Accounts) != 1 || updated.Accounts[0].Status != "active" || updated.Accounts[0].Balance != 50000 {
		t.Fatalf("子账号余额/状态未更新: %+v", updated.Accounts)
	}
}

func TestHealMissingProvisionedKeysNoRequestWhenComplete(t *testing.T) {
	existing := []config.APIKeyConfig{
		{KeyUID: StableKeyUID("newapi-ch-1", 11), SourceSubscriptionUID: "newapi-ch-1", SourceRemoteTokenID: 11},
		{KeyUID: StableKeyUID("newapi-ch-1", 12), SourceSubscriptionUID: "newapi-ch-1", SourceRemoteTokenID: 12},
	}
	svc := healService(t, healChannel("ch-1", []string{"sk-a"}, existing))
	profile := &SubscriptionProfile{SubscriptionUID: "newapi-ch-1", LinkedChannelUIDs: []string{"ch-1"}}
	adapter := &healFakeAdapter{}

	svc.healMissingProvisionedKeys(context.Background(), profile, adapter, "https://new-api.example.com", "access", "1", NewApiAuthModeBearer,
		[]newApiDesiredKey{healDesired(11, "Surprise"), healDesired(12, "default")})

	if adapter.listCalls != 0 {
		t.Fatalf("渠道 key 完整时不应拉远端 token 列表，实际调用 %d 次", adapter.listCalls)
	}
}
