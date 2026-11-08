package siteaccess

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

func TestCredentialUpdatePreservesOwnershipAndEvidence(t *testing.T) {
	s := NewAccountStore(filepath.Join(t.TempDir(), "accounts.json"))
	a := SiteAccount{SiteType: "newapi", BaseURL: "https://relay.example", UserID: "7", AccessToken: "first"}
	uid, err := s.Upsert(a)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Patch(uid, func(a *SiteAccount) error {
		a.ProvisionedKeys = []ProvisionedKeyMeta{{TokenID: 17, Protocol: "chat"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	RecordRealRequest(s, uid, "chat", "default", true, "")
	a.AccessToken = "rotated"
	got, err := s.Upsert(a)
	if err != nil || got != uid {
		t.Fatalf("换令牌改变身份: %s %v", got, err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) { defer wg.Done(); RecordConfigured(s, uid, "chat", fmt.Sprint(i)) }(i)
	}
	wg.Wait()
	current, _, _ := s.Get(uid)
	if len(current.ProvisionedKeys) != 1 || len(current.Evidence) != 11 {
		t.Fatalf("并发更新丢记录: %+v", current)
	}
	plain, _ := current.plaintextAccessToken()
	if plain != "rotated" {
		t.Fatal("令牌未更新")
	}
}

func TestAccountChannelsSurviveRenameAndRemainIsolated(t *testing.T) {
	site, uid, store, cm, adapter := newProvisionTestDeps(t)
	defer cm.Close()
	req := ProvisionRequest{AccountUID: uid, Protocol: "chat", Groups: []string{"default"}, Models: []string{"gpt-4o"}}
	if _, err := ProvisionProtocol(context.Background(), store, cm, adapter, req); err != nil {
		t.Fatal(err)
	}
	name := "我的自定义名字"
	if _, err := cm.UpdateChatUpstream(0, config.UpstreamUpdate{Name: &name}); err != nil {
		t.Fatal(err)
	}
	a, _, _ := store.Get(uid)
	second, err := store.Upsert(SiteAccount{SiteType: a.SiteType, BaseURL: a.BaseURL, UserID: "2", AccessToken: "second"})
	if err != nil {
		t.Fatal(err)
	}
	req.AccountUID = second
	if _, err := ProvisionProtocol(context.Background(), store, cm, adapter, req); err != nil {
		t.Fatal(err)
	}
	req.AccountUID = uid
	if _, err := ProvisionProtocol(context.Background(), store, cm, adapter, req); err != nil {
		t.Fatal(err)
	}
	channels := cm.GetConfig().ChatUpstream
	if len(channels) != 2 || channels[0].SiteAccountUID == channels[1].SiteAccountUID {
		t.Fatalf("账号串绑: %+v", channels)
	}
	site.deleteTokenByName(a.ProvisionedKeys[0].Name)
	actions, err := ReconcileAccount(context.Background(), store, cm, adapter, uid)
	if err != nil || len(actions) != 1 || actions[0].ChannelName != name || actions[0].Action != "disabled" {
		t.Fatalf("改名后对账失败: %+v %v", actions, err)
	}
}

func TestMultiplierSyncBlocksAndRecoversWithoutOverridingManualDisable(t *testing.T) {
	site, uid, store, cm, adapter := newProvisionTestDeps(t)
	defer cm.Close()
	_, err := ProvisionProtocol(context.Background(), store, cm, adapter, ProvisionRequest{AccountUID: uid, Protocol: "chat", Groups: []string{"default"}, Models: []string{"gpt-4o"}})
	if err != nil {
		t.Fatal(err)
	}
	check := func(want bool) {
		t.Helper()
		ch := cm.GetConfig().ChatUpstream[0]
		if ch.IsAPIKeyGroupMultiplierAllowed(ch.APIKeys[0]) != want {
			t.Fatalf("倍率准入不符: %+v", ch.APIKeyConfigs)
		}
	}
	check(true)
	site.mu.Lock()
	site.groups["default"] = 2
	site.mu.Unlock()
	if _, err := ReconcileAccount(context.Background(), store, cm, adapter, uid); err != nil {
		t.Fatal(err)
	}
	check(false)
	site.mu.Lock()
	site.groups["default"] = 0
	site.mu.Unlock()
	if _, err := ReconcileAccount(context.Background(), store, cm, adapter, uid); err != nil {
		t.Fatal(err)
	}
	check(true)
	ch := cm.GetConfig().ChatUpstream[0]
	no := false
	ch.APIKeyConfigs[0].Enabled = &no
	if _, err := cm.UpdateChatUpstream(0, config.UpstreamUpdate{APIKeyConfigs: ch.APIKeyConfigs}); err != nil {
		t.Fatal(err)
	}
	if _, err := ReconcileAccount(context.Background(), store, cm, adapter, uid); err != nil {
		t.Fatal(err)
	}
	if *cm.GetConfig().ChatUpstream[0].APIKeyConfigs[0].Enabled {
		t.Fatal("同步覆盖人工停用")
	}
}

func TestResponsesVerificationUsesConfiguredEndpoint(t *testing.T) {
	for _, service := range []string{"responses", "openai"} {
		t.Run(service, func(t *testing.T) {
			seen := ""
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				seen = r.URL.Path
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"}}]}`)
			}))
			defer srv.Close()
			err := doMinimalInferenceRequest(context.Background(), config.UpstreamConfig{BaseURL: srv.URL, ServiceType: service, APIKeys: []string{"key"}}, "responses", "test-model")
			want := "/v1/responses"
			if service == "openai" {
				want = "/v1/chat/completions"
			}
			if err != nil || seen != want {
				t.Fatalf("验收路径 %s, want %s: %v", seen, want, err)
			}
		})
	}
}

func TestAutoGroupCatchUpRespectsModelExclusionsAndRemoteDeletion(t *testing.T) {
	site, uid, store, cm, adapter := newProvisionTestDeps(t)
	req := ProvisionRequest{AccountUID: uid, Protocol: "chat", Groups: []string{"default"}, ExcludeGroups: []string{"ignored"}, Models: []string{"gpt-4o"}, AutoSyncGroups: true}
	if _, err := ProvisionProtocol(context.Background(), store, cm, adapter, req); err != nil {
		t.Fatal(err)
	}
	site.mu.Lock()
	for _, name := range []string{"fresh", "ignored", "wrong-model"} {
		site.groups[name] = 0.5
		site.models[name] = []string{"gpt-4o"}
	}
	site.models["wrong-model"] = []string{"unselected"}
	site.mu.Unlock()
	if _, err := ReconcileAccount(context.Background(), store, cm, adapter, uid); err != nil {
		t.Fatal(err)
	}
	if len(cm.GetConfig().ChatUpstream) != 2 {
		t.Fatal("应只补接 fresh，不应接入人工排除组或其他模型")
	}
	account, _, _ := store.Get(uid)
	var name string
	for _, meta := range account.ProvisionedKeys {
		if meta.Group == "fresh" {
			name = meta.Name
		}
	}
	if name == "" {
		t.Fatal("缺失新组元数据")
	}
	site.deleteTokenByName(name)
	for i := 0; i < 2; i++ {
		if _, err := ReconcileAccount(context.Background(), store, cm, adapter, uid); err != nil {
			t.Fatal(err)
		}
	}
	site.mu.Lock()
	defer site.mu.Unlock()
	for _, token := range site.tokens {
		if token.Name == name {
			t.Fatal("不得自动复活用户删除的远端 Key")
		}
	}
}

func TestSyncDoesNotOverwriteConcurrentManualEdit(t *testing.T) {
	_, uid, store, cm, adapter := newProvisionTestDeps(t)
	_, err := ProvisionProtocol(context.Background(), store, cm, adapter, ProvisionRequest{AccountUID: uid, Protocol: "chat", Models: []string{"gpt-4o"}})
	if err != nil {
		t.Fatal(err)
	}
	stale := cm.GetConfig().ChatUpstream[0]
	account, _, _ := store.Get(uid)
	meta := account.ProvisionedKeys[0]
	latest := stale.Clone()
	no := false
	latest.APIKeyConfigs[0].Enabled = &no
	if _, err := cm.UpdateChatUpstream(0, config.UpstreamUpdate{APIKeyConfigs: latest.APIKeyConfigs}); err != nil {
		t.Fatal(err)
	}
	_, err = syncRemoteKey(cm, "Chat", 0, stale, meta, NewApiToken{ID: meta.TokenID, Group: meta.Group, Status: 1}, map[string]float64{meta.Group: 0.5})
	if err == nil || *cm.GetConfig().ChatUpstream[0].APIKeyConfigs[0].Enabled {
		t.Fatal("后台同步覆盖了最新的人工停用设置")
	}
}
