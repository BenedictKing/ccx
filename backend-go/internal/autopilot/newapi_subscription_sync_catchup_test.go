package autopilot

import (
	"context"
	"fmt"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
)

// catchUpFakeAdapter 同时满足 NewApiSyncAdapter 与 newApiGroupCatchUpper，
// 用于驱动 SyncNow 全链路并观察兜底补建行为。未实现 ListTokens/GetTokenKey，
// 自愈路径（healMissingProvisionedKeys）按设计自动跳过。
type catchUpFakeAdapter struct {
	groups         map[string]float64
	models         []string
	groupModels    map[string][]string
	provisionCalls []NewApiProvisionOptions
	nextTokenID    int
}

func (f *catchUpFakeAdapter) VerifyWithFallback(context.Context, string, string, string, string) (*NewApiUserSelf, string, error) {
	return &NewApiUserSelf{ID: 1, Username: "alice", Quota: 100}, "1", nil
}

func (f *catchUpFakeAdapter) FetchGroups(context.Context, string, string, string, string) (map[string]float64, error) {
	return f.groups, nil
}

func (f *catchUpFakeAdapter) FetchModels(context.Context, string, string, string, string) ([]string, error) {
	return f.models, nil
}

func (f *catchUpFakeAdapter) FetchGroupModels(_ context.Context, _ string, _ string, _ string, _ string, group string) ([]string, error) {
	return f.groupModels[group], nil
}

func (f *catchUpFakeAdapter) ProvisionKey(_ context.Context, _ string, _ string, _ string, _ string, opts NewApiProvisionOptions) (int, string, bool, string, error) {
	f.provisionCalls = append(f.provisionCalls, opts)
	f.nextTokenID++
	name := opts.Name
	if name == "" {
		name = DefaultNewApiProvisionKeyName
	}
	return f.nextTokenID, fmt.Sprintf("sk-catchup-%d", f.nextTokenID), false, name, nil
}

func newCatchUpFixture(t *testing.T, adapter *catchUpFakeAdapter, allEligible bool, channelMax *float64) (*NewApiSubscriptionSyncService, *config.ConfigManager) {
	t.Helper()
	store, err := NewSubscriptionStoreWithDB(newTestDB(t))
	if err != nil {
		t.Fatalf("NewSubscriptionStoreWithDB 失败: %v", err)
	}
	channel := config.UpstreamConfig{
		ChannelUID:         "ch-catchup",
		Name:               "catchup-target",
		ServiceType:        "claude",
		BaseURL:            "https://new-api.example.com",
		APIKeys:            []string{"sk-existing-default"},
		APIKeyConfigs:      []config.APIKeyConfig{{Key: "sk-existing-default", SourceSubscriptionUID: "sub-catchup", SourceRemoteTokenID: 1}},
		MaxGroupMultiplier: channelMax,
	}
	cfgManager := newProxyTestConfigManager(t, channel)
	svc := NewNewApiSubscriptionSyncService(NewApiSubscriptionSyncServiceDeps{
		Store:      store,
		CfgManager: cfgManager,
		Adapter:    adapter,
		QuietLogs:  true,
	})
	profile := &SubscriptionProfile{
		SubscriptionUID:      "sub-catchup",
		Provider:             "new_api",
		BaseURL:              "https://new-api.example.com",
		AccessToken:          "access-token",
		UserID:               "1",
		ProvisionAllEligible: allEligible,
		LinkedChannelUIDs:    []string{"ch-catchup"},
		ProvisionedKeys: []NewApiProvisionedKey{
			{Name: "ccx-autopilot-default", Group: "default", GroupMultiplier: 1, TokenID: 1},
		},
	}
	if err := store.Create(profile); err != nil {
		t.Fatalf("创建订阅画像失败: %v", err)
	}
	return svc, cfgManager
}

// 接入时「画图」分组 0 可用模型被跳过；站点补上模型后，下一轮同步自动补建该分组的 key。
func TestSyncNowCatchUpProvisionsGroupAfterModelsArrive(t *testing.T) {
	adapter := &catchUpFakeAdapter{
		groups:      map[string]float64{"default": 1, "画图": 1},
		models:      []string{"gpt-4o"},
		groupModels: map[string][]string{"default": {"gpt-4o"}, "画图": {"dall-e-3"}},
		nextTokenID: 1, // tokenID=1 已被 default 分组占用
	}
	svc, cfgManager := newCatchUpFixture(t, adapter, true, nil)

	result, err := svc.SyncNow(context.Background(), "sub-catchup")
	if err != nil {
		t.Fatalf("SyncNow 失败: %v", err)
	}
	if !result.Success {
		t.Fatalf("SyncNow 结果未成功: %+v", result)
	}

	if len(adapter.provisionCalls) != 1 || adapter.provisionCalls[0].Group != "画图" {
		t.Fatalf("期望仅为「画图」补建一次 key，实际调用 %+v", adapter.provisionCalls)
	}

	got := svc.store.Get("sub-catchup")
	if len(got.ProvisionedKeys) != 2 {
		t.Fatalf("期望补建后共 2 条 ProvisionedKeys，实际 %+v", got.ProvisionedKeys)
	}
	var caught *NewApiProvisionedKey
	for i := range got.ProvisionedKeys {
		if got.ProvisionedKeys[i].Group == "画图" {
			caught = &got.ProvisionedKeys[i]
		}
	}
	if caught == nil || caught.TokenID != 2 || caught.GroupMultiplier != 1 {
		t.Fatalf("补建的 key 元数据不完整: %+v", got.ProvisionedKeys)
	}

	up := cfgManager.GetConfig().Upstream[0]
	wantKeys := map[string]bool{"sk-existing-default": false, "sk-catchup-2": false}
	for _, k := range up.APIKeys {
		if _, ok := wantKeys[k]; ok {
			wantKeys[k] = true
		}
	}
	for k, found := range wantKeys {
		if !found {
			t.Fatalf("渠道 APIKeys 缺少 %s，实际 %v", k, up.APIKeys)
		}
	}
}

// 显式单分组接入（ProvisionAllEligible=false）与存量订阅不扩组，避免后台静默建 key。
func TestSyncNowCatchUpSkippedWhenNotAllEligible(t *testing.T) {
	adapter := &catchUpFakeAdapter{
		groups:      map[string]float64{"default": 1, "画图": 1},
		models:      []string{"gpt-4o"},
		groupModels: map[string][]string{"default": {"gpt-4o"}, "画图": {"dall-e-3"}},
		nextTokenID: 1,
	}
	svc, _ := newCatchUpFixture(t, adapter, false, nil)

	if _, err := svc.SyncNow(context.Background(), "sub-catchup"); err != nil {
		t.Fatalf("SyncNow 失败: %v", err)
	}
	if len(adapter.provisionCalls) != 0 {
		t.Fatalf("未开启「接入全部合格分组」时不应补建 key，实际调用 %+v", adapter.provisionCalls)
	}
	if got := svc.store.Get("sub-catchup"); len(got.ProvisionedKeys) != 1 {
		t.Fatalf("ProvisionedKeys 不应变化，实际 %+v", got.ProvisionedKeys)
	}
}

// 超过倍率上限的分组不补建；空分组（依旧 0 模型）也不补建。
func TestSyncNowCatchUpSkipsOverLimitAndEmptyGroups(t *testing.T) {
	limit := 1.0
	adapter := &catchUpFakeAdapter{
		groups: map[string]float64{"default": 1, "premium": 2, "画图": 1},
		models: []string{"gpt-4o"},
		groupModels: map[string][]string{
			"default": {"gpt-4o"},
			"premium": {"gpt-4o-premium"},
			"画图":      {}, // 依旧没有可用模型
		},
		nextTokenID: 1,
	}
	svc, cfgManager := newCatchUpFixture(t, adapter, true, &limit)

	if _, err := svc.SyncNow(context.Background(), "sub-catchup"); err != nil {
		t.Fatalf("SyncNow 失败: %v", err)
	}
	if len(adapter.provisionCalls) != 0 {
		t.Fatalf("超限分组与空分组均不应补建，实际调用 %+v", adapter.provisionCalls)
	}
	if got := svc.store.Get("sub-catchup"); len(got.ProvisionedKeys) != 1 {
		t.Fatalf("ProvisionedKeys 不应变化，实际 %+v", got.ProvisionedKeys)
	}
	up := cfgManager.GetConfig().Upstream[0]
	if len(up.APIKeys) != 1 || up.APIKeys[0] != "sk-existing-default" {
		t.Fatalf("渠道 APIKeys 不应变化，实际 %v", up.APIKeys)
	}
}
