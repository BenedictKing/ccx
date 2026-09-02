package common

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/quota"
	"github.com/BenedictKing/ccx/internal/ratelimit"
	"github.com/BenedictKing/ccx/internal/scheduler"
	"github.com/BenedictKing/ccx/internal/types"
	"github.com/BenedictKing/ccx/internal/warmup"
	"github.com/gin-gonic/gin"
)

// runQuotaSignalFailover 在真实转发链路上执行一次请求，上游返回带配额响应头的 200，
// 并以与 main.go 相同的接线方式（ratelimit 共享回调 → quota.Manager）注册回调。
func runQuotaSignalFailover(t *testing.T, setQuotaHeaders func(w http.ResponseWriter)) *quota.Manager {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		setQuotaHeaders(w)
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(server.Close)

	upstream := config.UpstreamConfig{
		Name:       "anthropic-relay",
		ChannelUID: "channel-quota-signal",
		APIKeys:    []string{"sk-quota-1"},
		APIKeyConfigs: []config.APIKeyConfig{
			{Key: "sk-quota-1", Name: "quota-1"},
		},
		Status:      "active",
		ServiceType: "openai",
	}
	upstream.BaseURL = server.URL

	cfgManager, channelScheduler, messagesMetrics, cleanup := newTestFailoverDependencies(t, upstream)
	t.Cleanup(cleanup)

	quotaManager := quota.NewManager()
	original := ratelimit.UpstreamSignalCallback
	ratelimit.SetUpstreamSignalCallback(func(channelUID, endpointUID, _, serviceType, _ string, _ bool, _ int64, headers http.Header, _ int, _ string) {
		// 与 main.go 共享回调中的配额接线保持一致
		quotaManager.UpdateFromUpstreamSignal(channelUID, endpointUID, serviceType, headers)
	})
	t.Cleanup(func() { ratelimit.SetUpstreamSignalCallback(original) })

	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", strings.NewReader(`{"model":"test-model"}`))
	cfg := cfgManager.GetConfig()
	runtimeUpstream := &cfg.Upstream[0]
	handled, successKey, _, failoverErr, _, lastErr := TryUpstreamWithAllKeys(
		c,
		config.NewEnvConfig(),
		cfgManager,
		channelScheduler,
		scheduler.ChannelKindMessages,
		"Messages",
		messagesMetrics,
		runtimeUpstream,
		[]warmup.URLLatencyResult{{URL: server.URL, OriginalIdx: 0}},
		[]byte(`{"model":"test-model","messages":[]}`),
		nil,
		false,
		func(upstream *config.UpstreamConfig, failedKeys map[string]bool) (string, error) {
			return cfgManager.GetNextAPIKey(upstream, failedKeys, "Messages")
		},
		func(c *gin.Context, upstreamCopy *config.UpstreamConfig, apiKey string) (*http.Request, error) {
			req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodPost, upstreamCopy.BaseURL, strings.NewReader(`{}`))
			if err == nil {
				req.Header.Set("X-Test-Key", apiKey)
			}
			return req, err
		},
		func(string) {},
		nil,
		nil,
		func(_ *gin.Context, resp *http.Response, _ *config.UpstreamConfig, _ string, _ []byte) (*types.Usage, error) {
			_ = resp.Body.Close()
			return nil, nil
		},
		"test-model",
		"",
		0,
		channelScheduler.GetChannelLogStore(scheduler.ChannelKindMessages),
	)

	if !handled || successKey != "sk-quota-1" || failoverErr != nil || lastErr != nil {
		t.Fatalf("请求应成功: handled=%v key=%q failoverErr=%v lastErr=%v", handled, successKey, failoverErr, lastErr)
	}
	return quotaManager
}

// P1 配额接线集成测试：真实请求链路上，上游 anthropic-ratelimit-* 响应头
// 经共享回调进入 quota.Manager，余量趋紧的渠道被判为 approaching_limit 并参与饱和沉底。
func TestTryUpstreamWithAllKeys_ResponseHeadersFeedQuotaManager(t *testing.T) {
	const channelUID = "channel-quota-signal"
	qm := runQuotaSignalFailover(t, func(w http.ResponseWriter) {
		w.Header().Set("anthropic-ratelimit-requests-limit", "100")
		w.Header().Set("anthropic-ratelimit-requests-remaining", "2")
		w.Header().Set("anthropic-ratelimit-requests-reset", time.Now().Add(time.Minute).Format(time.RFC3339))
	})

	state := qm.GetChannelState(channelUID)
	if state.Status != quota.TruthApproachingLimit {
		t.Fatalf("truth = %v, want approaching_limit（remaining 2/100 ≤ 20%%）", state.Status)
	}
	v, ok := state.Values[quota.DimRequests]
	if !ok {
		t.Fatalf("requests 维度缺失: %+v", state.Values)
	}
	if v.Limit == nil || *v.Limit != 100 || v.Remaining == nil || *v.Remaining != 2 {
		t.Fatalf("requests value = %+v, want limit=100 remaining=2", v)
	}
	if v.Source != quota.SourceResponseHeaders {
		t.Fatalf("source = %v, want response_headers", v.Source)
	}
	if headroom := qm.GetChannelHeadroom(channelUID); headroom <= 0 || headroom > 0.2 {
		t.Fatalf("headroom = %v, want (0, 0.2]", headroom)
	}
	if !qm.IsChannelSaturated(channelUID, time.Now().UnixMilli()) {
		t.Fatal("approaching_limit 渠道应判定为饱和（沉底排序用）")
	}
}

// exhausted 路径：remaining=0 → TruthExhausted，饱和桶随 reset 时间懒重置恢复。
func TestTryUpstreamWithAllKeys_ResponseHeadersExhaustedLazyReset(t *testing.T) {
	const channelUID = "channel-quota-signal"
	resetAt := time.Now().Add(time.Minute)
	qm := runQuotaSignalFailover(t, func(w http.ResponseWriter) {
		w.Header().Set("anthropic-ratelimit-requests-limit", "100")
		w.Header().Set("anthropic-ratelimit-requests-remaining", "0")
		w.Header().Set("anthropic-ratelimit-requests-reset", resetAt.Format(time.RFC3339))
	})

	if truth := qm.GetChannelTruth(channelUID); truth != quota.TruthExhausted {
		t.Fatalf("truth = %v, want exhausted", truth)
	}
	if !qm.IsChannelSaturated(channelUID, time.Now().UnixMilli()) {
		t.Fatal("exhausted 渠道在重置窗口内应判定为饱和")
	}
	// 窗口翻过去后，桶应懒重置，渠道恢复 eligibility（fail-open 红线）
	if qm.IsChannelSaturated(channelUID, resetAt.Add(time.Second).UnixMilli()) {
		t.Fatal("重置时间已过，饱和桶应懒重置恢复")
	}
}
