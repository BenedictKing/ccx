package autopilot

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/quota"
)

// ── configured 级配额生产接线测试（Q3）──

type quotaSyncFakeAdapter struct{}

func (f *quotaSyncFakeAdapter) VerifyWithFallback(context.Context, string, string, string, string) (*NewApiUserSelf, string, error) {
	return &NewApiUserSelf{ID: 7, Username: "alice", Quota: 15000, UsedQuota: 2500}, "1", nil
}

func (f *quotaSyncFakeAdapter) FetchGroups(context.Context, string, string, string, string) (map[string]float64, error) {
	return map[string]float64{"default": 1}, nil
}

func (f *quotaSyncFakeAdapter) FetchModels(context.Context, string, string, string, string) ([]string, error) {
	return nil, nil
}

func newQuotaSyncSubscription(uid string, linkedChannels ...string) *SubscriptionProfile {
	now := time.Now()
	return &SubscriptionProfile{
		SubscriptionUID:      uid,
		Provider:             "new_api",
		DisplayName:          "quota-sync-test",
		BaseURL:              "https://new-api.example.com",
		AccessToken:          "token-x",
		LinkedChannelUIDs:    linkedChannels,
		LastBalanceRefreshAt: &now,
	}
}

// TestQuotaConfiguredSync_NewApiSyncSuccess new-api 同步成功出口接入：
// SyncNow 余额落盘后，关联渠道的配额状态携带 configured 级 DimCurrency 值。
func TestQuotaConfiguredSync_NewApiSyncSuccess(t *testing.T) {
	store := newTestSubscriptionStore(t)
	profile := newQuotaSyncSubscription("newapi-ch-quota", "ch-quota-live")
	profile.Balance = 0 // 同步前的旧值，SyncNow 会用 fake 的 15000 覆盖
	seedSubscription(t, store, profile)

	cfgManager := newProxyTestConfigManager(t, config.UpstreamConfig{
		ChannelUID:  "ch-quota-live",
		Name:        "quota-live",
		ServiceType: "claude",
		BaseURL:     "https://new-api.example.com",
		APIKeys:     []string{"sk-quota"},
	})

	svc := NewNewApiSubscriptionSyncService(NewApiSubscriptionSyncServiceDeps{
		Store:      store,
		CfgManager: cfgManager,
		Adapter:    &quotaSyncFakeAdapter{},
		Now:        time.Now,
	})
	qm := quota.NewManager()
	svc.SetQuotaManager(qm)

	if _, err := svc.SyncNow(context.Background(), "newapi-ch-quota"); err != nil {
		t.Fatalf("SyncNow: %v", err)
	}

	state := qm.GetChannelState("ch-quota-live")
	v, ok := state.Values[quota.DimCurrency]
	if !ok {
		t.Fatalf("同步成功后应写入 configured 级 DimCurrency, values=%+v", state.Values)
	}
	if v.Source != quota.SourceConfigured {
		t.Fatalf("source = %v, want configured", v.Source)
	}
	if v.Remaining == nil || *v.Remaining != 15000 {
		t.Fatalf("remaining = %v, want 15000（user/self quota）", v.Remaining)
	}
	if state.AccountUID != "newapi-ch-quota" {
		t.Fatalf("accountUID = %q, want 订阅 UID（不能用数组 index）", state.AccountUID)
	}
}

// TestQuotaConfiguredSync_HotReload 配置热更新出口接入：
// RegisterOnConfigChange 全量重放（模拟 main.go 接线），配置变更后写入。
func TestQuotaConfiguredSync_HotReload(t *testing.T) {
	store := newTestSubscriptionStore(t)
	profile := newQuotaSyncSubscription("sub-quota-hot", "ch-hot-1")
	profile.Balance = 42.5
	profile.Currency = "USD"
	seedSubscription(t, store, profile)

	// 自管配置文件路径：热重载测试需要直接改写文件触发 fsnotify → loadConfig → 异步回调。
	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(configPath, []byte(`{"upstream":[{"channelUid":"ch-hot-1","name":"hot-1","serviceType":"claude","baseUrl":"https://new-api.example.com","apiKeys":["sk-hot"]}]}`), 0600); err != nil {
		t.Fatalf("写测试配置失败: %v", err)
	}
	cfgManager, err := config.NewConfigManager(configPath, filepath.Join(dir, "backups"))
	if err != nil {
		t.Fatalf("NewConfigManager 失败: %v", err)
	}
	t.Cleanup(func() { cfgManager.CloseWatcher() })

	qm := quota.NewManager()
	cfgManager.RegisterOnConfigChange(func(cfg config.Config) {
		SyncAllSubscriptionsQuotaAsConfigured(qm, store, cfg)
	})

	// 触发文件热重载（改渠道名制造合法变更；RegisterOnConfigChange 仅由
	// loadConfig / Set 类成功出口触发，渠道更新方法不在其列——渠道数据
	// 变更由 new-api 同步出口的 writeConfiguredQuota 增量覆盖）。
	current, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("读配置文件: %v", err)
	}
	if err := os.WriteFile(configPath, []byte(strings.Replace(string(current), `"hot-1"`, `"hot-1-reloaded"`, 1)), 0600); err != nil {
		t.Fatalf("写配置文件: %v", err)
	}

	// 回调异步执行（watcher debounce + goroutine），轮询等待重放完成
	deadline := time.Now().Add(5 * time.Second)
	var state *quota.ChannelState
	for {
		state = qm.GetChannelState("ch-hot-1")
		if _, ok := state.Values[quota.DimCurrency]; ok {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("热更新回调未在时限内写入 configured 配额, values=%+v", state.Values)
		}
		time.Sleep(20 * time.Millisecond)
	}
	v := state.Values[quota.DimCurrency]
	if v.Source != quota.SourceConfigured {
		t.Fatalf("热更新后应写入 configured 级配额, source=%v", v.Source)
	}
	if v.Remaining == nil || *v.Remaining != 42.5 {
		t.Fatalf("remaining = %v, want 42.5", v.Remaining)
	}
	if state.AccountUID != "sub-quota-hot" {
		t.Fatalf("accountUID = %q, want 订阅 UID", state.AccountUID)
	}
}

// TestQuotaConfiguredSync_Guards 手工配置边界：未同步过不写、负值不写、
// 已删除渠道不写、configured 不降级覆盖 provider_api。
func TestQuotaConfiguredSync_Guards(t *testing.T) {
	live := map[string]bool{"ch-live": true}
	qm := quota.NewManager()

	// 未同步过（LastBalanceRefreshAt nil）：Balance 零值不代表真相，不写
	neverSynced := newQuotaSyncSubscription("sub-never", "ch-live")
	neverSynced.LastBalanceRefreshAt = nil
	if n := SyncSubscriptionQuotaAsConfigured(qm, neverSynced, live); n != 0 {
		t.Fatalf("未同步过的订阅不应写入, wrote=%d", n)
	}
	if _, ok := qm.GetChannelState("ch-live").Values[quota.DimCurrency]; ok {
		t.Fatal("未同步过的订阅不应产生配额值")
	}

	// 负值/NaN：不写
	negative := newQuotaSyncSubscription("sub-neg", "ch-live")
	negative.Balance = -5
	if n := SyncSubscriptionQuotaAsConfigured(qm, negative, live); n != 0 {
		t.Fatalf("负余额不应写入, wrote=%d", n)
	}

	// 配置中已删除的渠道：不写
	deleted := newQuotaSyncSubscription("sub-deleted", "ch-gone")
	deleted.Balance = 10
	if n := SyncSubscriptionQuotaAsConfigured(qm, deleted, live); n != 0 {
		t.Fatalf("已删除渠道不应写入, wrote=%d", n)
	}

	// configured 不降级覆盖 provider_api
	providerVal := 99.0
	qm.UpdateChannelProviderAPI("ch-live", "acc-api", []quota.Value{
		{Dimension: quota.DimCurrency, Remaining: &providerVal},
	}, nil)
	configuredProfile := newQuotaSyncSubscription("sub-cfg", "ch-live")
	configuredProfile.Balance = 3
	if n := SyncSubscriptionQuotaAsConfigured(qm, configuredProfile, live); n != 1 {
		t.Fatalf("合法订阅应写入 1 个渠道, wrote=%d", n)
	}
	v := qm.GetChannelState("ch-live").Values[quota.DimCurrency]
	if v.Source != quota.SourceProviderAPI || v.Remaining == nil || *v.Remaining != 99 {
		t.Fatalf("provider_api 数据被 configured 降级覆盖: %+v", v)
	}
}
