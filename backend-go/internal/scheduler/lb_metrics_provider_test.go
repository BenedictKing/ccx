package scheduler

import (
	"context"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/config"
	"github.com/BenedictKing/ccx/internal/metrics"
	"github.com/BenedictKing/ccx/internal/session"
)

// newLBProviderHarness 构造一个独立的测试 harness，包含 cfgManager + metricsManager + traceAffinity。
// 与 createTestScheduler 不耦合，便于单测 13 个方法。
func newLBProviderHarness(t *testing.T, cfg config.Config) (*LBMetricsProvider, *metrics.MetricsManager, *session.TraceAffinityManager, func()) {
	t.Helper()

	cfgManager, cfgCleanup := createTestConfigManager(t, cfg)
	mm := metrics.NewMetricsManager()
	aff := session.NewTraceAffinityManager()
	provider := NewLBMetricsProvider(mm, cfgManager, aff, ChannelKindMessages)

	cleanup := func() {
		mm.Stop()
		aff.Stop()
		cfgCleanup()
	}
	return provider, mm, aff, cleanup
}

// twoChannelTestConfig 构造一个含 2 个 messages 渠道的测试 config。
func twoChannelTestConfig(promotionUntil *time.Time) config.Config {
	return config.Config{
		Upstream: []config.UpstreamConfig{
			{
				Name:        "alpha",
				BaseURL:     "https://alpha.example.com",
				APIKeys:     []string{"key-alpha"},
				ServiceType: "claude",
				Status:      "active",
				Priority:    1,
			},
			{
				Name:           "beta",
				BaseURL:        "https://beta.example.com",
				APIKeys:        []string{"key-beta"},
				ServiceType:    "claude",
				Status:         "active",
				Priority:       2,
				PromotionUntil: promotionUntil,
			},
		},
	}
}

func TestLBMetricsProvider_BuildLBChannelKey(t *testing.T) {
	got := metrics.BuildLBChannelKey(string(ChannelKindMessages), "alpha")
	if got != "messages:alpha" {
		t.Fatalf("意外的 channelKey: %q", got)
	}
}

func TestLBMetricsProvider_ResolveChannel_OutOfRange(t *testing.T) {
	provider, _, _, cleanup := newLBProviderHarness(t, twoChannelTestConfig(nil))
	defer cleanup()

	// 超出索引应回退为 0/false 类零值，不允许 panic
	if got := provider.ConsecutiveFailures(99); got != 0 {
		t.Fatalf("越界 channelID 应返回 0，得到 %d", got)
	}
	if got := provider.LastFailureTime(99); !got.IsZero() {
		t.Fatalf("越界 channelID 应返回 zero time，得到 %v", got)
	}
	if got := provider.IsPromotionActive(99); got {
		t.Fatalf("越界 channelID 应返回 false")
	}
}

func TestLBMetricsProvider_LastSuccessfulChannelByTrace(t *testing.T) {
	provider, _, aff, cleanup := newLBProviderHarness(t, twoChannelTestConfig(nil))
	defer cleanup()

	// 未设置亲和性时返回 false
	if id, ok := provider.LastSuccessfulChannelByTrace(context.Background(), "user-1"); ok || id != 0 {
		t.Fatalf("未设置时应返回 (0,false)，得到 (%d,%v)", id, ok)
	}

	// 设置亲和性（注意 scheduler 用的复合 key 规则）
	aff.SetPreferredChannel("messages:user-1", 1)
	id, ok := provider.LastSuccessfulChannelByTrace(context.Background(), "user-1")
	if !ok || id != 1 {
		t.Fatalf("亲和性应返回 (1,true)，得到 (%d,%v)", id, ok)
	}

	// 空 traceID 直接返回 false
	if _, ok := provider.LastSuccessfulChannelByTrace(context.Background(), ""); ok {
		t.Fatalf("空 traceID 应返回 false")
	}
}

func TestLBMetricsProvider_LastSuccessfulChannelByTrace_NilAffinity(t *testing.T) {
	cfgManager, cfgCleanup := createTestConfigManager(t, twoChannelTestConfig(nil))
	defer cfgCleanup()
	mm := metrics.NewMetricsManager()
	defer mm.Stop()

	provider := NewLBMetricsProvider(mm, cfgManager, nil, ChannelKindMessages)
	if _, ok := provider.LastSuccessfulChannelByTrace(context.Background(), "user-1"); ok {
		t.Fatalf("nil affinity 应返回 false")
	}
}

func TestLBMetricsProvider_ConsecutiveFailures_And_LastFailureTime(t *testing.T) {
	provider, mm, _, cleanup := newLBProviderHarness(t, twoChannelTestConfig(nil))
	defer cleanup()

	before := time.Now().Add(-1 * time.Second)
	// 触发 channel 0 (alpha) 的失败
	mm.RecordFailure("https://alpha.example.com", "key-alpha", "claude")
	mm.RecordFailure("https://alpha.example.com", "key-alpha", "claude")

	if got := provider.ConsecutiveFailures(0); got < 2 {
		t.Fatalf("alpha ConsecutiveFailures 应 >=2，得到 %d", got)
	}
	last := provider.LastFailureTime(0)
	if last.IsZero() || last.Before(before) {
		t.Fatalf("alpha LastFailureTime 应在 %v 之后，得到 %v", before, last)
	}

	// channel 1 (beta) 没有失败，应为零值
	if got := provider.ConsecutiveFailures(1); got != 0 {
		t.Fatalf("beta 未失败应返回 0，得到 %d", got)
	}
	if !provider.LastFailureTime(1).IsZero() {
		t.Fatalf("beta 未失败应返回 zero time")
	}
}

func TestLBMetricsProvider_RequestCount(t *testing.T) {
	provider, mm, _, cleanup := newLBProviderHarness(t, twoChannelTestConfig(nil))
	defer cleanup()

	// since <=0 直接返回 0
	if got := provider.RequestCount(0, 0); got != 0 {
		t.Fatalf("since=0 应返回 0，得到 %d", got)
	}

	// 没有任何请求时返回 0
	if got := provider.RequestCount(0, time.Hour); got != 0 {
		t.Fatalf("无请求应返回 0，得到 %d", got)
	}

	// 触发 channel 0 的成功请求（GetTimeWindowStatsForKey 通过 requestHistory 计数，
	// requestHistory 由 RecordRequestConnected/RecordRequestFinalize* 流程写入）
	rid := mm.RecordRequestConnected("https://alpha.example.com", "key-alpha", "claude", "claude-3")
	mm.RecordRequestFinalizeSuccess("https://alpha.example.com", "key-alpha", "claude", rid, nil)

	if got := provider.RequestCount(0, time.Hour); got < 1 {
		t.Fatalf("alpha 一次请求后 RequestCount 应 >=1，得到 %d", got)
	}
}

func TestLBMetricsProvider_LBMetrics_FTTL_TPS_ActiveConn(t *testing.T) {
	provider, mm, _, cleanup := newLBProviderHarness(t, twoChannelTestConfig(nil))
	defer cleanup()

	key := metrics.BuildLBChannelKey(string(ChannelKindMessages), "alpha")

	// 初始无样本：返回 0
	if got := provider.FTTLP95(0, "claude-3"); got != 0 {
		t.Fatalf("无样本应 0，得到 %v", got)
	}
	if got := provider.TPSP50(0, "claude-3"); got != 0 {
		t.Fatalf("无样本应 0，得到 %v", got)
	}
	if got := provider.ActiveConnections(0); got != 0 {
		t.Fatalf("无样本应 0，得到 %d", got)
	}

	// 写入样本（key 与 read 端一致）
	mm.RecordFirstToken(key, 250)
	mm.RecordFirstToken(key, 350)
	mm.RecordTPS(key, 1000, 500)
	mm.RecordActiveConnDelta(key, 3)

	got := provider.FTTLP95(0, "claude-3")
	if got != 300*time.Millisecond {
		t.Fatalf("FTTLP95 应 300ms，得到 %v", got)
	}
	if got := provider.TPSP50(0, "claude-3"); got != 2000 {
		t.Fatalf("TPSP50 应 2000，得到 %v", got)
	}
	if got := provider.ActiveConnections(0); got != 3 {
		t.Fatalf("ActiveConnections 应 3，得到 %d", got)
	}

	// channel 1 (beta) 未写入：仍为 0
	if got := provider.FTTLP95(1, "claude-3"); got != 0 {
		t.Fatalf("beta 无样本应 0，得到 %v", got)
	}
}

func TestLBMetricsProvider_OrderingWeight_MaxConcurrent_E2E_RateLimit(t *testing.T) {
	provider, _, _, cleanup := newLBProviderHarness(t, twoChannelTestConfig(nil))
	defer cleanup()

	// 这些方法在当前 ccx 没有数据源，统一返回 fallback 值
	if got := provider.OrderingWeight(0); got != 0 {
		t.Fatalf("OrderingWeight 应 fallback=0，得到 %d", got)
	}
	if got := provider.MaxConcurrent(0); got != 0 {
		t.Fatalf("MaxConcurrent 应 fallback=0，得到 %d", got)
	}
	if got := provider.E2ELatencyP95(0, "claude-3"); got != 0 {
		t.Fatalf("E2ELatencyP95 应 fallback=0，得到 %v", got)
	}
	st := provider.RateLimitState(0)
	if st.Limited {
		t.Fatalf("RateLimitState.Limited 应 false，得到 %v", st)
	}
	if st.QuotaRemaining != -1 || st.QuotaTotal != -1 {
		t.Fatalf("RateLimitState.Quota* 应 -1（未知），得到 %+v", st)
	}
}

func TestLBMetricsProvider_IsPromotionActive(t *testing.T) {
	until := time.Now().Add(10 * time.Minute)
	provider, _, _, cleanup := newLBProviderHarness(t, twoChannelTestConfig(&until))
	defer cleanup()

	if provider.IsPromotionActive(0) {
		t.Fatalf("alpha 没有 promotion，应返回 false")
	}
	if !provider.IsPromotionActive(1) {
		t.Fatalf("beta 在促销期内，应返回 true")
	}

	// 过期的 promotion 应返回 false
	expired := time.Now().Add(-1 * time.Minute)
	provider2, _, _, cleanup2 := newLBProviderHarness(t, twoChannelTestConfig(&expired))
	defer cleanup2()
	if provider2.IsPromotionActive(1) {
		t.Fatalf("过期的 promotion 应返回 false")
	}
}
