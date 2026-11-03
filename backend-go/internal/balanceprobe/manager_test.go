package balanceprobe

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/quota"
)

// fakeStore 内存版 KeyHealthStore。
type fakeStore struct {
	mu      sync.Mutex
	records map[string]metrics.KeyHealthRecord
}

func newFakeStore() *fakeStore {
	return &fakeStore{records: make(map[string]metrics.KeyHealthRecord)}
}

func recKey(r metrics.KeyHealthRecord) string {
	return r.ChannelType + "/" + r.ChannelID + "/" + r.KeyMask + "/" + r.CheckKind
}

func (f *fakeStore) UpsertKeyHealth(rec metrics.KeyHealthRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.records[recKey(rec)] = rec
	return nil
}

func (f *fakeStore) GetKeyHealthForChannel(channelType, channelID string) ([]metrics.KeyHealthRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []metrics.KeyHealthRecord
	for _, r := range f.records {
		if r.ChannelType == channelType && r.ChannelID == channelID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *fakeStore) GetAllKeyHealth() ([]metrics.KeyHealthRecord, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]metrics.KeyHealthRecord, 0, len(f.records))
	for _, r := range f.records {
		out = append(out, r)
	}
	return out, nil
}

type quotaUpdateCall struct {
	channelUID string
	accountUID string
	values     []quota.Value
	fetchErr   error
}

// fakeQuota 内存版 QuotaSink。
type fakeQuota struct {
	mu      sync.Mutex
	updates []quotaUpdateCall
}

func (f *fakeQuota) UpdateChannelProviderAPI(channelUID, accountUID string, values []quota.Value, fetchErr error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.updates = append(f.updates, quotaUpdateCall{channelUID, accountUID, values, fetchErr})
}

func (f *fakeQuota) GetChannelState(string) *quota.ChannelState { return nil }

func (f *fakeQuota) snapshot() []quotaUpdateCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]quotaUpdateCall(nil), f.updates...)
}

// newTestManager 构造不启动 goroutine 的 Manager（测试手动驱动 probeChannel/scan）。
func newTestManager(cfg config.Config, store *fakeStore, q *fakeQuota) *Manager {
	return NewManager(func() config.Config { return cfg }, store, q, Options{})
}

// newTokenUsageServer 双 key newapi_token 端点（按 Authorization 区分 key）。
func newTokenUsageServer(t *testing.T, balances map[string]float64) (*httptest.Server, map[string]*int) {
	t.Helper()
	hits := map[string]*int{}
	for k := range balances {
		hits[k] = new(int)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/usage/token/", func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		remaining, ok := balances[auth]
		if !ok {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"success":false}`))
			return
		}
		(*hits[auth])++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(tokenUsageJSON(remaining*2, remaining, remaining, false)))
	})
	return httptest.NewServer(mux), hits
}

func tokenUsageJSON(granted, used, available float64, unlimited bool) string {
	return fmt.Sprintf(`{"code":true,"data":{"object":"token_usage","total_granted":%g,"total_used":%g,"total_available":%g,"unlimited_quota":%t}}`, granted, used, available, unlimited)
}

func TestProbeChannelAggregatesMultiKey(t *testing.T) {
	srv, _ := newTokenUsageServer(t, map[string]float64{
		"Bearer sk-test-key-aaaaaa": 4000000, // $8
		"Bearer sk-test-key-bbbbbb": 1000000, // $2
	})
	defer srv.Close()

	cfg := config.Config{
		ChatUpstream: []config.UpstreamConfig{{
			ChannelUID:      "ch-agg",
			Name:            "agg",
			BaseURL:         srv.URL,
			APIKeys:         []string{"sk-test-key-aaaaaa", "sk-test-key-bbbbbb"},
			AutoManagedKind: "new_api",
		}},
	}
	store := newFakeStore()
	q := &fakeQuota{}
	m := newTestManager(cfg, store, q)

	m.probeChannel("chat", 0)

	// key_health：两条 ok 记录
	recs, _ := store.GetKeyHealthForChannel("chat", "ch-agg")
	if len(recs) != 2 {
		t.Fatalf("balance 记录数 = %d, want 2", len(recs))
	}
	for _, r := range recs {
		if r.CheckKind != CheckKindBalance || r.LastStatus != "ok" {
			t.Errorf("记录 %s: kind=%s status=%s", r.KeyMask, r.CheckKind, r.LastStatus)
		}
	}

	// quota 聚合：Σcredits=5000000、ΣUSD=10、accountUID=probe:ch-agg
	ups := q.snapshot()
	if len(ups) != 1 {
		t.Fatalf("quota 更新次数 = %d, want 1", len(ups))
	}
	up := ups[0]
	if up.channelUID != "ch-agg" || up.accountUID != "probe:ch-agg" {
		t.Errorf("uid=%s account=%s, want ch-agg/probe:ch-agg", up.channelUID, up.accountUID)
	}
	if up.fetchErr != nil {
		t.Errorf("fetchErr = %v, want nil", up.fetchErr)
	}
	var currency, credits *quota.Value
	for i, v := range up.values {
		switch v.Dimension {
		case quota.DimCurrency:
			currency = &up.values[i]
		case quota.DimCredits:
			credits = &up.values[i]
		}
	}
	if currency == nil || currency.Remaining == nil || *currency.Remaining != 10 {
		t.Errorf("currency remaining = %v, want 10", currency)
	}
	if credits == nil || credits.Remaining == nil || *credits.Remaining != 5000000 {
		t.Errorf("credits remaining = %v, want 5000000", credits)
	}
}

func TestProbeChannelAllUnlimited(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/usage/token/", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(tokenUsageJSON(0, 0, 0, true)))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := config.Config{
		ChatUpstream: []config.UpstreamConfig{{
			ChannelUID: "ch-unltd", Name: "u", BaseURL: srv.URL,
			APIKeys: []string{"sk-x"}, AutoManagedKind: "new_api",
		}},
	}
	store := newFakeStore()
	q := &fakeQuota{}
	m := newTestManager(cfg, store, q)

	m.probeChannel("chat", 0)

	ups := q.snapshot()
	if len(ups) != 1 {
		t.Fatalf("quota 更新次数 = %d, want 1（仍需刷新 Supported/FetchedAt）", len(ups))
	}
	if len(ups[0].values) != 0 {
		t.Errorf("全 unlimited 不应写数值维度, got %v", ups[0].values)
	}
	if ups[0].fetchErr != nil {
		t.Errorf("fetchErr = %v, want nil", ups[0].fetchErr)
	}
}

func TestProbeChannelAllFailReportsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"404"}`))
	}))
	defer srv.Close()

	cfg := config.Config{
		ChatUpstream: []config.UpstreamConfig{{
			ChannelUID: "ch-fail", Name: "f", BaseURL: srv.URL,
			APIKeys: []string{"sk-x"}, AutoManagedKind: "new_api",
		}},
	}
	store := newFakeStore()
	q := &fakeQuota{}
	m := newTestManager(cfg, store, q)

	m.probeChannel("chat", 0)

	recs, _ := store.GetKeyHealthForChannel("chat", "ch-fail")
	if len(recs) != 1 || recs[0].LastStatus != "error" {
		t.Fatalf("应记录 error 状态, got %+v", recs)
	}
	if recs[0].ConsecutiveFailures != 1 {
		t.Errorf("ConsecutiveFailures = %d, want 1", recs[0].ConsecutiveFailures)
	}
	ups := q.snapshot()
	if len(ups) != 1 || ups[0].fetchErr == nil {
		t.Fatalf("全失败应带 fetchErr, got %+v", ups)
	}
}

func TestProviderMemoReusedOnSecondProbe(t *testing.T) {
	// 仅暴露 sub2api 端点；首轮 auto 全序列后应记忆 provider，第二轮只打 /v1/usage。
	var usageHits, tokenHits int
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/usage", func(w http.ResponseWriter, r *http.Request) {
		usageHits++
		_, _ = w.Write([]byte(`{"mode":"quota_limited","quota":{"limit":10,"used":2,"remaining":8,"unit":"USD"}}`))
	})
	mux.HandleFunc("/api/usage/token/", func(w http.ResponseWriter, r *http.Request) {
		tokenHits++
		w.WriteHeader(http.StatusNotFound)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := config.Config{
		ChatUpstream: []config.UpstreamConfig{{
			ChannelUID: "ch-memo", Name: "m", BaseURL: srv.URL,
			APIKeys: []string{"sk-x"}, AutoManagedKind: "new_api",
		}},
	}
	store := newFakeStore()
	q := &fakeQuota{}
	m := newTestManager(cfg, store, q)

	m.probeChannel("chat", 0)
	if tokenHits != 1 || usageHits != 1 {
		t.Fatalf("首轮应 newapi_token(1)+sub2api(1), got token=%d usage=%d", tokenHits, usageHits)
	}
	if got := m.rememberedProvider("ch-memo"); got != "sub2api" {
		t.Fatalf("memo = %q, want sub2api", got)
	}

	m.probeChannel("chat", 0)
	if usageHits != 2 {
		t.Errorf("第二轮应只打 sub2api, usageHits=%d", usageHits)
	}
}

func TestScanDueAndSkip(t *testing.T) {
	srv, _ := newTokenUsageServer(t, map[string]float64{"Bearer sk-a": 100})
	defer srv.Close()

	cfg := config.Config{
		ChatUpstream: []config.UpstreamConfig{
			{ChannelUID: "ch-due", Name: "d", BaseURL: srv.URL, APIKeys: []string{"sk-a"}, AutoManagedKind: "new_api"},
			{ChannelUID: "ch-off", Name: "o", BaseURL: srv.URL, APIKeys: []string{"sk-a"}, AutoManagedKind: ""},
		},
	}
	store := newFakeStore()
	q := &fakeQuota{}
	m := newTestManager(cfg, store, q)

	if !m.running {
		// scan 的 submit 需要 running 标记；测试手动置位后恢复
		m.running = true
		defer func() { m.running = false }()
	}
	m.scan()

	// 只提交了 ch-due（ch-off 未启用）；驱动队列执行
	if len(m.tasks) != 1 {
		t.Fatalf("scan 应只入队 1 个渠道, got %d", len(m.tasks))
	}
	task := <-m.tasks
	if task.channelIndex != 0 {
		t.Errorf("入队渠道 index = %d, want 0", task.channelIndex)
	}

	// 写入近期 balance 记录后，下一轮 scan 不再入队
	_ = store.UpsertKeyHealth(metrics.KeyHealthRecord{
		ChannelType: "chat", ChannelID: "ch-due", KeyMask: "sk-t***aaa",
		CheckKind: CheckKindBalance, LastCheckAt: m.now(), LastStatus: "ok",
	})
	for len(m.tasks) > 0 {
		<-m.tasks
	}
	m.scan()
	if got := len(m.tasks); got != 0 {
		t.Errorf("近期已探测渠道不应重复入队, got %d", got)
	}
}

func TestDisabledPolicyDefault(t *testing.T) {
	// 非托管渠道默认关闭：scan 不入队
	cfg := config.Config{
		ChatUpstream: []config.UpstreamConfig{
			{ChannelUID: "ch-plain", Name: "p", BaseURL: "http://localhost:1", APIKeys: []string{"sk-a"}},
		},
	}
	m := newTestManager(cfg, newFakeStore(), &fakeQuota{})
	m.running = true
	defer func() { m.running = false }()
	m.scan()
	if got := len(m.tasks); got != 0 {
		t.Errorf("默认关闭渠道不应入队, got %d", got)
	}
}
