package healthcheck

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/utils"
)

// fakeKeyHealthStore 内存版 KeyHealthStore，模拟 SQLite 的 upsert 语义
type fakeKeyHealthStore struct {
	mu      sync.Mutex
	records map[string]metrics.KeyHealthRecord
}

func newFakeKeyHealthStore() *fakeKeyHealthStore {
	return &fakeKeyHealthStore{records: make(map[string]metrics.KeyHealthRecord)}
}

func storeKey(rec metrics.KeyHealthRecord) string {
	return rec.ChannelType + "/" + rec.ChannelID + "/" + rec.KeyMask + "/" + rec.CheckKind
}

func (s *fakeKeyHealthStore) UpsertKeyHealth(rec metrics.KeyHealthRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records[storeKey(rec)] = rec
	return nil
}

func (s *fakeKeyHealthStore) GetKeyHealthForChannel(channelType, channelID string) ([]metrics.KeyHealthRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []metrics.KeyHealthRecord
	for _, rec := range s.records {
		if rec.ChannelType == channelType && rec.ChannelID == channelID {
			out = append(out, rec)
		}
	}
	return out, nil
}

func (s *fakeKeyHealthStore) GetAllKeyHealth() ([]metrics.KeyHealthRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]metrics.KeyHealthRecord, 0, len(s.records))
	for _, rec := range s.records {
		out = append(out, rec)
	}
	return out, nil
}

func (s *fakeKeyHealthStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.records)
}

// testWrappedFetcher 模拟各渠道 GetChannelModels 包装 handler 的行为约定：
// 200 透传模型列表；上游 401 包装为 400+statusCode+details；其他状态透传；网络/超时错误返回 502。
func testWrappedFetcher() L1Fetcher {
	return func(ctx context.Context, req L1Request) (L1Response, error) {
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodGet, req.BaseURL+"/v1/models", nil)
		if err != nil {
			return L1Response{}, err
		}
		resp, err := http.DefaultClient.Do(httpReq)
		if err != nil {
			return L1Response{StatusCode: http.StatusBadGateway, Body: []byte(`{"error":"Failed to fetch models"}`)}, nil
		}
		body, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode == http.StatusUnauthorized {
			wrapped, _ := json.Marshal(map[string]interface{}{
				"error":      "上游 API Key 无效",
				"statusCode": 401,
				"details":    string(body),
			})
			return L1Response{StatusCode: http.StatusBadRequest, Body: wrapped}, nil
		}
		return L1Response{StatusCode: resp.StatusCode, Body: body}, nil
	}
}

func newModelsServer(t *testing.T, statusCode int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func boolPtr(v bool) *bool { return &v }

func TestChannelDue(t *testing.T) {
	now := time.Now()
	recent := now.Add(-1 * time.Minute)
	old := now.Add(-3 * time.Hour)
	keyMask := utils.MaskAPIKey("sk-key-1111111")

	tests := []struct {
		name     string
		keys     []string
		records  []metrics.KeyHealthRecord
		interval time.Duration
		wantDue  bool
	}{
		{
			name:     "从未验证立即到期",
			keys:     []string{"sk-key-1111111"},
			records:  nil,
			interval: time.Hour,
			wantDue:  true,
		},
		{
			name: "最近已验证未到期",
			keys: []string{"sk-key-1111111"},
			records: []metrics.KeyHealthRecord{
				{KeyMask: keyMask, CheckKind: CheckKindL1, LastCheckAt: recent},
			},
			interval: time.Hour,
			wantDue:  false,
		},
		{
			name: "超过间隔+jitter上限到期",
			keys: []string{"sk-key-1111111"},
			records: []metrics.KeyHealthRecord{
				{KeyMask: keyMask, CheckKind: CheckKindL1, LastCheckAt: old},
			},
			interval: time.Hour,
			wantDue:  true,
		},
		{
			name: "新增key无记录立即到期",
			keys: []string{"sk-key-1111111", "sk-key-2222222"},
			records: []metrics.KeyHealthRecord{
				{KeyMask: keyMask, CheckKind: CheckKindL1, LastCheckAt: recent},
			},
			interval: time.Hour,
			wantDue:  true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := channelDue("messages", "0", tt.keys, tt.records, tt.interval, now)
			if got != tt.wantDue {
				t.Fatalf("channelDue = %v, 期望 %v", got, tt.wantDue)
			}
		})
	}
}

func TestGroupL1Records只保留L1(t *testing.T) {
	records := []metrics.KeyHealthRecord{
		{ChannelType: "messages", ChannelID: "0", KeyMask: "k1", CheckKind: CheckKindL1},
		{ChannelType: "messages", ChannelID: "0", KeyMask: "k1", CheckKind: CheckKindL2},
		{ChannelType: "chat", ChannelID: "1", KeyMask: "k2", CheckKind: CheckKindL1},
	}
	grouped := groupL1Records(records)
	if len(grouped["messages/0"]) != 1 {
		t.Fatalf("messages/0 的 L1 记录数 = %d, 期望 1", len(grouped["messages/0"]))
	}
	if len(grouped["chat/1"]) != 1 {
		t.Fatalf("chat/1 的 L1 记录数 = %d, 期望 1", len(grouped["chat/1"]))
	}
}

func TestJitteredInterval(t *testing.T) {
	interval := 30 * time.Minute
	first := jitteredInterval("messages", "0", interval)
	// 确定性：相同输入相同输出
	if got := jitteredInterval("messages", "0", interval); got != first {
		t.Fatalf("jitter 不确定: %v != %v", got, first)
	}
	// 全部落在 [0.9, 1.1) 区间内
	for _, channelType := range ChannelTypes {
		for i := 0; i < 20; i++ {
			got := jitteredInterval(channelType, string(rune('a'+i)), interval)
			lo := time.Duration(float64(interval) * 0.9)
			hi := time.Duration(float64(interval) * 1.1)
			if got < lo || got >= hi {
				t.Fatalf("jitteredInterval(%s, %d) = %v, 超出 [%v, %v)", channelType, i, got, lo, hi)
			}
		}
	}
}

func TestNormalizeWrappedResponse(t *testing.T) {
	tests := []struct {
		name       string
		code       int
		body       string
		wantStatus int
		wantBody   string
	}{
		{name: "200透传", code: 200, body: `{"data":[{"id":"m1"}]}`, wantStatus: 200, wantBody: `{"data":[{"id":"m1"}]}`},
		{name: "400包装的上游401", code: 400, body: `{"error":"上游 API Key 无效","statusCode":401,"details":"{\"error\":{\"type\":\"authentication_error\"}}"}`, wantStatus: 401, wantBody: `{"error":{"type":"authentication_error"}}`},
		{name: "400无statusCode原样返回", code: 400, body: `{"error":"Invalid request body"}`, wantStatus: 400, wantBody: `{"error":"Invalid request body"}`},
		{name: "502网络错误归0", code: 502, body: `{"error":"Failed to fetch models"}`, wantStatus: 0, wantBody: `{"error":"Failed to fetch models"}`},
		{name: "504归0", code: 504, body: ``, wantStatus: 0, wantBody: ``},
		{name: "上游500透传", code: 500, body: `{"error":"internal"}`, wantStatus: 500, wantBody: `{"error":"internal"}`},
		{name: "上游403透传", code: 403, body: `{"error":"forbidden"}`, wantStatus: 403, wantBody: `{"error":"forbidden"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			status, body := normalizeWrappedResponse(tt.code, []byte(tt.body))
			if status != tt.wantStatus {
				t.Fatalf("status = %d, 期望 %d", status, tt.wantStatus)
			}
			if string(body) != tt.wantBody {
				t.Fatalf("body = %q, 期望 %q", string(body), tt.wantBody)
			}
		})
	}
}

func TestCountModels(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{name: "OpenAI data 数组", body: `{"object":"list","data":[{"id":"a"},{"id":"b"}]}`, want: 2},
		{name: "Gemini models 数组", body: `{"models":[{"name":"models/g1"}]}`, want: 1},
		{name: "非法 JSON", body: `not-json`, want: 0},
		{name: "空对象", body: `{}`, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := countModels([]byte(tt.body)); got != tt.want {
				t.Fatalf("countModels = %d, 期望 %d", got, tt.want)
			}
		})
	}
}

func TestEligibleKeys(t *testing.T) {
	now := time.Now()
	u := &config.UpstreamConfig{
		APIKeys: []string{"sk-ok", "sk-disabled", "sk-off", "  "},
		DisabledAPIKeys: []config.DisabledKeyInfo{
			{Key: "sk-disabled", Reason: "authentication_error"},
		},
		APIKeyConfigs: []config.APIKeyConfig{
			{Key: "sk-off", Enabled: boolPtr(false)},
			{Key: "sk-ok", Enabled: boolPtr(true)},
		},
	}
	got := eligibleKeys(u, now)
	if len(got) != 1 || got[0] != "sk-ok" {
		t.Fatalf("eligibleKeys = %v, 期望 [sk-ok]", got)
	}

	// 已到期的自动恢复记录不再阻断
	u2 := &config.UpstreamConfig{
		APIKeys: []string{"sk-recovered"},
		DisabledAPIKeys: []config.DisabledKeyInfo{
			{Key: "sk-recovered", Reason: "insufficient_quota", RecoverAt: now.Add(-time.Hour).Format(time.RFC3339)},
		},
	}
	if got := eligibleKeys(u2, now); len(got) != 1 {
		t.Fatalf("过期禁用记录应恢复候选, eligibleKeys = %v", got)
	}
}

func TestChannelStatus(t *testing.T) {
	if got := channelStatus(&config.UpstreamConfig{}); got != "active" {
		t.Fatalf("空状态应视为 active, got %q", got)
	}
	if got := channelStatus(&config.UpstreamConfig{Status: "suspended"}); got != "suspended" {
		t.Fatalf("got %q", got)
	}
}

// checkKeyL1 测试夹具
type checkKeyFixture struct {
	manager            *Manager
	store              *fakeKeyHealthStore
	blacklistCalls     []blacklistCall
	recordFailureCalls []recordFailureCall
}

type blacklistCall struct {
	channelType  string
	channelIndex int
	apiKey       string
	reason       string
	message      string
	recoverAt    string
}

type recordFailureCall struct {
	channelType  string
	channelIndex int
	baseURL      string
	apiKey       string
	serviceType  string
	detail       string
}

func newCheckKeyFixture() *checkKeyFixture {
	f := &checkKeyFixture{store: newFakeKeyHealthStore()}
	f.manager = NewManager(
		func() config.Config { return config.Config{} },
		f.store,
		func(channelType string, channelIndex int, apiKey, reason, message, recoverAt string) {
			f.blacklistCalls = append(f.blacklistCalls, blacklistCall{channelType, channelIndex, apiKey, reason, message, recoverAt})
		},
		func(channelType string, channelIndex int, baseURL, apiKey, serviceType, detail string) {
			f.recordFailureCalls = append(f.recordFailureCalls, recordFailureCall{channelType, channelIndex, baseURL, apiKey, serviceType, detail})
		},
		Options{},
	)
	return f
}

func (f *checkKeyFixture) run(u *config.UpstreamConfig, baseURLs []string, apiKey string, policy config.ResolvedHealthCheckPolicy, prev map[string]metrics.KeyHealthRecord) metrics.KeyHealthRecord {
	f.manager.checkKeyL1("messages", 0, "0", u, baseURLs, apiKey, policy, prev, testWrappedFetcher())
	recs, _ := f.store.GetKeyHealthForChannel("messages", "0")
	if len(recs) == 0 {
		return metrics.KeyHealthRecord{}
	}
	return recs[len(recs)-1]
}

func defaultTestPolicy(timeout time.Duration) config.ResolvedHealthCheckPolicy {
	return config.ResolvedHealthCheckPolicy{
		Enabled:        true,
		Interval:       time.Hour,
		MaxConcurrency: 4,
		Timeout:        timeout,
	}
}

func TestCheckKeyL1成功(t *testing.T) {
	srv := newModelsServer(t, 200, `{"object":"list","data":[{"id":"m1"},{"id":"m2"},{"id":"m3"}]}`)
	f := newCheckKeyFixture()
	u := &config.UpstreamConfig{ServiceType: "claude"}

	rec := f.run(u, []string{srv.URL}, "sk-key-1", defaultTestPolicy(2*time.Second), nil)

	if rec.LastStatus != StatusOK {
		t.Fatalf("LastStatus = %q, 期望 ok", rec.LastStatus)
	}
	if rec.ModelCount != 3 {
		t.Fatalf("ModelCount = %d, 期望 3", rec.ModelCount)
	}
	if rec.ConsecutiveFailures != 0 {
		t.Fatalf("ConsecutiveFailures = %d, 期望 0", rec.ConsecutiveFailures)
	}
	if rec.CheckKind != CheckKindL1 || rec.ChannelType != "messages" || rec.ChannelID != "0" {
		t.Fatalf("记录标识错误: %+v", rec)
	}
	if rec.KeyMask != utils.MaskAPIKey("sk-key-1") {
		t.Fatalf("KeyMask = %q", rec.KeyMask)
	}
	if len(f.blacklistCalls) != 0 || len(f.recordFailureCalls) != 0 {
		t.Fatalf("成功不应触发回调: blacklist=%d, recordFailure=%d", len(f.blacklistCalls), len(f.recordFailureCalls))
	}
}

func TestCheckKeyL1鉴权失败拉黑(t *testing.T) {
	body := `{"error":{"type":"authentication_error","message":"invalid api key"}}`
	srv := newModelsServer(t, 401, body)
	f := newCheckKeyFixture()
	u := &config.UpstreamConfig{ServiceType: "claude"}
	keyMask := utils.MaskAPIKey("sk-bad")
	prev := map[string]metrics.KeyHealthRecord{
		keyMask: {KeyMask: keyMask, CheckKind: CheckKindL1, ConsecutiveFailures: 2},
	}

	rec := f.run(u, []string{srv.URL}, "sk-bad", defaultTestPolicy(2*time.Second), prev)

	if rec.LastStatus != StatusAuthFailed {
		t.Fatalf("LastStatus = %q, 期望 auth_failed", rec.LastStatus)
	}
	if rec.ConsecutiveFailures != 3 {
		t.Fatalf("ConsecutiveFailures = %d, 期望 3（基于上次递增）", rec.ConsecutiveFailures)
	}
	if rec.Detail == "" {
		t.Fatal("Detail 应包含失败摘要")
	}
	if len(f.blacklistCalls) != 1 {
		t.Fatalf("blacklist 调用次数 = %d, 期望 1", len(f.blacklistCalls))
	}
	call := f.blacklistCalls[0]
	if call.apiKey != "sk-bad" || call.reason != "authentication_error" || call.channelType != "messages" || call.channelIndex != 0 {
		t.Fatalf("blacklist 回调参数错误: %+v", call)
	}
	if len(f.recordFailureCalls) != 0 {
		t.Fatalf("auth_failed 不应喂熔断: %d 次", len(f.recordFailureCalls))
	}
}

func TestCheckKeyL1鉴权失败但不可识别不拉黑(t *testing.T) {
	srv := newModelsServer(t, 401, `{"foo":"bar"}`)
	f := newCheckKeyFixture()
	u := &config.UpstreamConfig{ServiceType: "claude"}

	rec := f.run(u, []string{srv.URL}, "sk-bad", defaultTestPolicy(2*time.Second), nil)

	if rec.LastStatus != StatusAuthFailed {
		t.Fatalf("LastStatus = %q, 期望 auth_failed", rec.LastStatus)
	}
	if len(f.blacklistCalls) != 0 {
		t.Fatalf("不可识别的 401 不应拉黑: %d 次", len(f.blacklistCalls))
	}
}

func TestCheckKeyL1服务器错误喂熔断(t *testing.T) {
	srv := newModelsServer(t, 500, `{"error":"internal"}`)
	f := newCheckKeyFixture()
	u := &config.UpstreamConfig{ServiceType: "claude"}

	rec := f.run(u, []string{srv.URL}, "sk-key-1", defaultTestPolicy(2*time.Second), nil)

	if rec.LastStatus != StatusError {
		t.Fatalf("LastStatus = %q, 期望 error", rec.LastStatus)
	}
	if rec.ConsecutiveFailures != 1 {
		t.Fatalf("ConsecutiveFailures = %d, 期望 1", rec.ConsecutiveFailures)
	}
	if len(f.recordFailureCalls) != 1 {
		t.Fatalf("recordFailure 调用次数 = %d, 期望 1", len(f.recordFailureCalls))
	}
	call := f.recordFailureCalls[0]
	if call.baseURL != srv.URL || call.apiKey != "sk-key-1" {
		t.Fatalf("recordFailure 回调参数错误: %+v", call)
	}
	if len(f.blacklistCalls) != 0 {
		t.Fatalf("500 不应拉黑: %d 次", len(f.blacklistCalls))
	}
}

func TestCheckKeyL1超时归类error(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(500 * time.Millisecond)
		w.WriteHeader(200)
	}))
	t.Cleanup(srv.Close)
	f := newCheckKeyFixture()
	u := &config.UpstreamConfig{ServiceType: "claude"}

	rec := f.run(u, []string{srv.URL}, "sk-key-1", defaultTestPolicy(50*time.Millisecond), nil)

	if rec.LastStatus != StatusError {
		t.Fatalf("LastStatus = %q, 期望 error", rec.LastStatus)
	}
	if len(f.recordFailureCalls) != 1 {
		t.Fatalf("超时应喂熔断: %d 次", len(f.recordFailureCalls))
	}
}

func TestCheckKeyL1多BaseURL逐个尝试(t *testing.T) {
	bad := newModelsServer(t, 500, `{"error":"internal"}`)
	good := newModelsServer(t, 200, `{"data":[{"id":"m1"}]}`)
	f := newCheckKeyFixture()
	u := &config.UpstreamConfig{ServiceType: "claude"}

	rec := f.run(u, []string{bad.URL, good.URL}, "sk-key-1", defaultTestPolicy(2*time.Second), nil)

	if rec.LastStatus != StatusOK {
		t.Fatalf("LastStatus = %q, 期望 ok（第二个 BaseURL 成功）", rec.LastStatus)
	}
	if rec.ModelCount != 1 {
		t.Fatalf("ModelCount = %d, 期望 1", rec.ModelCount)
	}
	if len(f.recordFailureCalls) != 0 {
		t.Fatalf("任一 BaseURL 成功即 ok，不应喂熔断: %d 次", len(f.recordFailureCalls))
	}
}

func TestCheckKeyL1成功后失败计数清零(t *testing.T) {
	srv := newModelsServer(t, 200, `{"data":[{"id":"m1"}]}`)
	f := newCheckKeyFixture()
	u := &config.UpstreamConfig{ServiceType: "claude"}
	keyMask := utils.MaskAPIKey("sk-key-1")
	prev := map[string]metrics.KeyHealthRecord{
		keyMask: {KeyMask: keyMask, CheckKind: CheckKindL1, ConsecutiveFailures: 5, LastStatus: StatusError},
	}

	rec := f.run(u, []string{srv.URL}, "sk-key-1", defaultTestPolicy(2*time.Second), prev)

	if rec.ConsecutiveFailures != 0 {
		t.Fatalf("成功后 ConsecutiveFailures = %d, 期望清零", rec.ConsecutiveFailures)
	}
}

func TestCheckChannel跳过(t *testing.T) {
	srv := newModelsServer(t, 200, `{"data":[{"id":"m1"}]}`)
	fetcherCalled := false
	wrapFetcher := func(ctx context.Context, req L1Request) (L1Response, error) {
		fetcherCalled = true
		return testWrappedFetcher()(ctx, req)
	}

	tests := []struct {
		name    string
		channel config.UpstreamConfig
	}{
		{
			name:    "suspended渠道整渠道跳过",
			channel: config.UpstreamConfig{BaseURL: srv.URL, APIKeys: []string{"sk-1"}, Status: "suspended"},
		},
		{
			name:    "disabled渠道整渠道跳过",
			channel: config.UpstreamConfig{BaseURL: srv.URL, APIKeys: []string{"sk-1"}, Status: "disabled"},
		},
		{
			name:    "策略禁用跳过",
			channel: config.UpstreamConfig{BaseURL: srv.URL, APIKeys: []string{"sk-1"}, Status: "active", HealthCheck: &config.ChannelHealthCheckConfig{Enabled: boolPtr(false)}},
		},
		{
			name:    "无可用key跳过",
			channel: config.UpstreamConfig{BaseURL: srv.URL, APIKeys: []string{"sk-1"}, Status: "active", DisabledAPIKeys: []config.DisabledKeyInfo{{Key: "sk-1"}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fetcherCalled = false
			cfg := config.Config{Upstream: []config.UpstreamConfig{tt.channel}}
			f := newCheckKeyFixture()
			f.manager.getConfig = func() config.Config { return cfg }
			f.manager.RegisterL1Fetcher("messages", wrapFetcher)

			f.manager.checkChannel("messages", 0)

			if fetcherCalled {
				t.Fatal("跳过的渠道不应调用 fetcher")
			}
			if f.store.count() != 0 {
				t.Fatalf("跳过的渠道不应写入记录, got %d", f.store.count())
			}
		})
	}
}
