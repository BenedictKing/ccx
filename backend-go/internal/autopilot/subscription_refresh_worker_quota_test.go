package autopilot

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/BenedictKing/ccx/internal/quota"
)

// fakeBalanceFetcher 返回固定余额的测试 fetcher。
type fakeBalanceFetcher struct {
	balance float64
	err     error
}

func (f *fakeBalanceFetcher) ProviderName() string { return "openai" }

func (f *fakeBalanceFetcher) FetchBalance(_ context.Context, _ string) (float64, string, error) {
	if f.err != nil {
		return 0, "", f.err
	}
	return f.balance, "USD", nil
}

// newQuotaRefreshTestWorker 构建带假 fetcher 的刷新 worker 与关联渠道的订阅画像。
func newQuotaRefreshTestWorker(t *testing.T, fetcher SubscriptionBalanceFetcher, linkedChannels []string) (*SubscriptionRefreshWorker, *quota.Manager, *SubscriptionStore) {
	t.Helper()

	store, err := NewSubscriptionStore(filepath.Join(t.TempDir(), "subs.db"))
	if err != nil {
		t.Fatalf("创建订阅存储失败: %v", err)
	}
	t.Cleanup(func() { _ = store.Close() })

	if err := store.Create(&SubscriptionProfile{
		SubscriptionUID:    "sub-openai-1",
		Provider:           "openai",
		AutoRefreshEnabled: true,
		BillingAPIKey:      "sk-admin-test",
		LinkedChannelUIDs:  linkedChannels,
	}); err != nil {
		t.Fatalf("创建订阅画像失败: %v", err)
	}

	registry := NewBalanceFetcherRegistry()
	registry.Register(fetcher)

	worker := NewSubscriptionRefreshWorker(
		store,
		registry,
		SubscriptionRefreshWorkerConfig{QuietLogs: true},
		func() bool { return true },
	)
	qm := quota.NewManager()
	worker.SetQuotaManager(qm)
	return worker, qm, store
}

// P1 provider_api 接线：余额刷新结果应写入配额管理器，
// 每个关联渠道获得 currency 维度的 provider_api 级数据。
func TestSubscriptionRefreshWorker_FeedsQuotaManager(t *testing.T) {
	worker, qm, _ := newQuotaRefreshTestWorker(t, &fakeBalanceFetcher{balance: 42.5}, []string{"ch-a", "ch-b"})

	worker.refreshAll()

	for _, channelUID := range []string{"ch-a", "ch-b"} {
		state := qm.GetChannelState(channelUID)
		if !state.Supported {
			t.Fatalf("渠道 %s Supported 应为 true", channelUID)
		}
		v, ok := state.Values[quota.DimCurrency]
		if !ok {
			t.Fatalf("渠道 %s 缺少 currency 维度: %+v", channelUID, state.Values)
		}
		if v.Remaining == nil || *v.Remaining != 42.5 {
			t.Fatalf("remaining = %+v, want 42.5", v.Remaining)
		}
		if v.Source != quota.SourceProviderAPI {
			t.Fatalf("source = %v, want provider_api", v.Source)
		}
		if v.Unit != "USD" {
			t.Fatalf("unit = %q, want USD", v.Unit)
		}
		if state.AccountUID != "sub-openai-1" {
			t.Fatalf("accountUID = %q, want sub-openai-1", state.AccountUID)
		}
	}
}

// 刷新失败：错误写入状态但不清空已有数据，不惩罚渠道（unavailable ≠ exhausted 红线）。
func TestSubscriptionRefreshWorker_FetchErrorKeepsFailOpen(t *testing.T) {
	worker, qm, _ := newQuotaRefreshTestWorker(t, &fakeBalanceFetcher{err: context.DeadlineExceeded}, []string{"ch-err"})

	worker.refreshAll()

	state := qm.GetChannelState("ch-err")
	if !state.Supported {
		t.Fatal("Supported 应为 true（provider 支持查询）")
	}
	if state.Error == "" {
		t.Fatal("刷新错误应写入 state.Error")
	}
	if truth := qm.GetChannelTruth("ch-err"); truth == quota.TruthExhausted {
		t.Fatal("查询失败不得判为 exhausted（fail-open 红线）")
	}
	if qm.IsChannelSaturated("ch-err", state.FetchedAtMs) {
		t.Fatal("查询失败不得触发饱和沉底")
	}
}

// 未关联渠道的订阅：刷新正常执行，配额侧静默跳过（fail-open）。
func TestSubscriptionRefreshWorker_NoLinkedChannelsSkipsQuota(t *testing.T) {
	worker, qm, _ := newQuotaRefreshTestWorker(t, &fakeBalanceFetcher{balance: 10}, nil)

	worker.refreshAll()

	if state := qm.GetChannelState("anything"); state.Status != quota.TruthUnknown {
		t.Fatalf("status = %v, want unknown（无关联渠道不应产生数据）", state.Status)
	}
}

// 未注入配额管理器：刷新照常工作，不 panic。
func TestSubscriptionRefreshWorker_WithoutQuotaManager(t *testing.T) {
	worker, _, _ := newQuotaRefreshTestWorker(t, &fakeBalanceFetcher{balance: 10}, []string{"ch-c"})
	worker.SetQuotaManager(nil)

	worker.refreshAll() // 不应 panic
}
