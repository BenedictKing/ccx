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

func TestMultiplierSyncChangesChannelCostWithoutOverridingManualDisable(t *testing.T) {
	site, uid, store, cm, adapter := newProvisionTestDeps(t)
	defer cm.Close()
	_, err := ProvisionProtocol(context.Background(), store, cm, adapter, ProvisionRequest{AccountUID: uid, Protocol: "chat", Groups: []string{"default"}, Models: []string{"gpt-4o"}})
	if err != nil {
		t.Fatal(err)
	}
	check := func(want float64) {
		t.Helper()
		ch := cm.GetConfig().ChatUpstream[0]
		if got := ch.BillingMultiplier(); got != want || !ch.IsAPIKeyRemoteAvailable(ch.APIKeys[0]) {
			t.Fatalf("倍率或调度状态不符: got %v, want %v", got, want)
		}
	}
	check(1)
	site.mu.Lock()
	site.groups["default"] = 2
	site.mu.Unlock()
	if _, err := ReconcileAccount(context.Background(), store, cm, adapter, uid); err != nil {
		t.Fatal(err)
	}
	check(2)
	site.mu.Lock()
	site.groups["default"] = 0
	site.mu.Unlock()
	if _, err := ReconcileAccount(context.Background(), store, cm, adapter, uid); err != nil {
		t.Fatal(err)
	}
	check(0)
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

func TestExistingChannelAccountBindingPreservesKeysAndRefreshesRatio(t *testing.T) {
	site, uid, store, cm, adapter := newProvisionTestDeps(t)
	defer cm.Close()
	req := ProvisionRequest{AccountUID: uid, Protocol: "chat", Groups: []string{"default"}, Models: []string{"gpt-4o"}}
	if _, err := ProvisionProtocol(context.Background(), store, cm, adapter, req); err != nil {
		t.Fatal(err)
	}
	original := cm.GetConfig().ChatUpstream[0]
	originalKeys := append([]string(nil), original.APIKeys...)
	binding := ChannelBindingRequest{AccountUID: uid, Protocol: "chat", ChannelIndex: 0, Group: "default"}

	if err := BindExistingChannel(context.Background(), store, cm, adapter, ChannelBindingRequest{AccountUID: uid, Protocol: "chat", ChannelIndex: 0, Group: "missing"}); err == nil {
		t.Fatal("未知分组不应绑定")
	}
	if err := UnbindExistingChannel(cm, binding); err != nil {
		t.Fatal(err)
	}
	unbound := cm.GetConfig().ChatUpstream[0]
	if unbound.SiteAccountUID != "" || unbound.SiteGroup != "" || unbound.BillingMultiplier() != original.BillingMultiplier() {
		t.Fatalf("解绑状态不符: %+v", unbound)
	}
	if fmt.Sprint(unbound.APIKeys) != fmt.Sprint(originalKeys) || unbound.Name != original.Name {
		t.Fatal("解绑改写了渠道或 Key")
	}

	site.mu.Lock()
	site.groups["default"] = 0.25
	site.mu.Unlock()
	if err := BindExistingChannel(context.Background(), store, cm, adapter, binding); err != nil {
		t.Fatal(err)
	}
	bound := cm.GetConfig().ChatUpstream[0]
	if bound.SiteAccountUID != uid || bound.SiteGroup != "default" || bound.BillingMultiplier() != 0.25 {
		t.Fatalf("绑定或倍率同步失败: %+v", bound)
	}
	if fmt.Sprint(bound.APIKeys) != fmt.Sprint(originalKeys) || bound.Name != original.Name {
		t.Fatal("绑定改写了渠道或 Key")
	}

	otherKey := "sk-not-the-remote-key"
	if _, err := cm.UpdateChatUpstream(0, config.UpstreamUpdate{SiteAccountUID: new(string), SiteGroup: new(string), APIKeys: []string{otherKey}}); err != nil {
		t.Fatal(err)
	}
	if err := BindExistingChannel(context.Background(), store, cm, adapter, binding); err == nil {
		t.Fatal("Key 指纹不匹配时不应绑定")
	}
}

func TestKeyBindingsSupportTwoAccountsInOneChannel(t *testing.T) {
	site, uidA, store, cm, adapter := newProvisionTestDeps(t)
	if _, err := ProvisionProtocol(context.Background(), store, cm, adapter, ProvisionRequest{AccountUID: uidA, Protocol: "chat", Groups: []string{"default"}, Models: []string{"gpt-4o"}}); err != nil {
		t.Fatal(err)
	}
	accountA, _, _ := store.Get(uidA)
	uidB, err := store.Upsert(SiteAccount{SiteType: "newapi", BaseURL: accountA.BaseURL, UserID: "2", AccessToken: "second"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ProvisionProtocol(context.Background(), store, cm, adapter, ProvisionRequest{AccountUID: uidB, Protocol: "chat", Groups: []string{"default"}, Models: []string{"claude-sonnet-4"}}); err != nil {
		t.Fatal(err)
	}
	channels := cm.GetConfig().ChatUpstream
	indexA, indexB := -1, -1
	for i := range channels {
		if channels[i].SiteAccountUID == uidA {
			indexA = i
		}
		if channels[i].SiteAccountUID == uidB {
			indexB = i
		}
	}
	keyA := channels[indexA].APIKeys[0]
	keyB := channels[indexB].APIKeys[0]
	if _, err := cm.UpdateChatUpstream(indexA, config.UpstreamUpdate{APIKeys: []string{keyA, keyB}}); err != nil {
		t.Fatal(err)
	}
	accountB, _, _ := store.Get(uidB)
	metaB := accountB.ProvisionedKeys[0]
	site.mu.Lock()
	site.tokens[metaB.TokenID].ModelLimits = "gpt-4o,claude-sonnet-4"
	site.mu.Unlock()
	if err := BindExistingKey(context.Background(), store, cm, adapter, KeyBindingRequest{AccountUID: uidB, Protocol: "chat", ChannelIndex: indexA, APIKey: keyB, Group: "default", RemoteTokenID: metaB.TokenID}); err != nil {
		t.Fatal(err)
	}
	mixed := cm.GetConfig().ChatUpstream[indexA]
	if mixed.SiteAccountUID != uidA {
		t.Fatal("绑定第二个账号不应改写渠道主账号")
	}
	if mixed.APIKeyConfigs[0].SourceAccountUID != uidA || mixed.APIKeyConfigs[1].SourceAccountUID != uidB {
		t.Fatalf("两个 Key 的账号归属不符: keys=%v configs=%+v", mixed.APIKeys, mixed.APIKeyConfigs)
	}
	if err := BindExistingKey(context.Background(), store, cm, adapter, KeyBindingRequest{AccountUID: uidB, Protocol: "chat", ChannelIndex: indexA, APIKey: keyA, Group: "default", RemoteTokenID: metaB.TokenID}); err == nil {
		t.Fatal("远端与本地明文不一致时不得绑定")
	}
	if err := UnbindExistingKey(store, cm, KeyBindingRequest{Protocol: "chat", ChannelIndex: indexA, APIKey: keyB}); err != nil {
		t.Fatal(err)
	}
	mixed = cm.GetConfig().ChatUpstream[indexA]
	if mixed.SiteAccountUID != uidA || mixed.APIKeyConfigs[0].SourceAccountUID != uidA || mixed.APIKeyConfigs[1].SourceAccountUID != "" {
		t.Fatalf("解绑账号 B 影响了账号 A: %+v", mixed.APIKeyConfigs)
	}
	if err := BindExistingKey(context.Background(), store, cm, adapter, KeyBindingRequest{AccountUID: uidB, Protocol: "chat", ChannelIndex: indexA, APIKey: keyB, Group: "default", RemoteTokenID: metaB.TokenID}); err != nil {
		t.Fatal(err)
	}

	if _, err := cm.RemoveChatUpstream(indexB); err != nil {
		t.Fatal(err)
	}
	for i, channel := range cm.GetConfig().ChatUpstream {
		if channel.SiteAccountUID == uidA {
			indexA = i
		}
	}
	site.deleteTokenByName(metaB.Name)
	if _, err := ReconcileAccount(context.Background(), store, cm, adapter, uidB); err != nil {
		t.Fatal(err)
	}
	mixed = cm.GetConfig().ChatUpstream[indexA]
	if len(mixed.APIKeys) != 1 || mixed.APIKeys[0] != keyA || len(mixed.DisabledAPIKeys) != 1 || mixed.DisabledAPIKeys[0].Key != keyB {
		t.Fatalf("账号 B 对账误伤账号 A 的 Key: %+v", mixed)
	}
}

func TestGroupCopyKeepsOriginalAndUsesIndependentKey(t *testing.T) {
	site, uid, store, cm, adapter := newProvisionTestDeps(t)
	if _, err := ProvisionProtocol(context.Background(), store, cm, adapter, ProvisionRequest{AccountUID: uid, Protocol: "chat", Groups: []string{"default"}, Models: []string{"gpt-4o"}}); err != nil {
		t.Fatal(err)
	}
	original := cm.GetConfig().ChatUpstream[0]
	emptyCopy := ChannelGroupCopyRequest{KeyBindingRequest: KeyBindingRequest{AccountUID: uid, Protocol: "chat", ChannelIndex: 0, APIKey: original.APIKeys[0], Group: "empty"}}
	if _, err := CopyChannelToGroup(context.Background(), store, cm, adapter, emptyCopy); err == nil || len(cm.GetConfig().ChatUpstream) != 1 {
		t.Fatal("空模型分组不应创建渠道副本")
	}
	site.mu.Lock()
	site.groups["vip"] = 2.0
	site.mu.Unlock()
	req := ChannelGroupCopyRequest{KeyBindingRequest: KeyBindingRequest{AccountUID: uid, Protocol: "chat", ChannelIndex: 0, APIKey: original.APIKeys[0], Group: "vip"}}
	result, err := CopyChannelToGroup(context.Background(), store, cm, adapter, req)
	if err != nil {
		t.Fatal(err)
	}
	if result.Reused || result.ChannelIndex != 0 {
		t.Fatalf("副本结果不符: %+v", result)
	}
	channels := cm.GetConfig().ChatUpstream
	if len(channels) != 2 || channels[1].Name != original.Name || fmt.Sprint(channels[1].APIKeys) != fmt.Sprint(original.APIKeys) {
		t.Fatalf("原渠道被改写: %+v", channels)
	}
	copy := channels[0]
	if copy.SiteAccountUID != uid || copy.SiteGroup != "vip" || len(copy.APIKeys) != 1 || copy.APIKeys[0] == original.APIKeys[0] || copy.BillingMultiplier() != 2 {
		t.Fatalf("副本分组或 Key 不符: %+v", copy)
	}
	req.ChannelIndex = 1
	again, err := CopyChannelToGroup(context.Background(), store, cm, adapter, req)
	if err != nil || !again.Reused || len(cm.GetConfig().ChatUpstream) != 2 {
		t.Fatalf("重复复制应复用已有副本: %+v %v", again, err)
	}
	site.mu.Lock()
	site.groups["vip"] = 0.25
	site.mu.Unlock()
	if _, err := ReconcileAccount(context.Background(), store, cm, adapter, uid); err != nil {
		t.Fatal(err)
	}
	channels = cm.GetConfig().ChatUpstream
	if channels[0].BillingMultiplier() != 0.25 || channels[1].BillingMultiplier() != 1 {
		t.Fatalf("分组对账串写: vip=%v default=%v", channels[0].BillingMultiplier(), channels[1].BillingMultiplier())
	}
}
