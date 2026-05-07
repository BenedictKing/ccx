package loadbalance

import (
	"context"
	"testing"
	"time"
)

// fakeMetricsProvider 在 strategy_test.go 已声明；本文件追加 latency / ratelimit
// 相关字段的填充帮手。

func (p *fakeMetricsProvider) FTTLP95(channelID int, model string) time.Duration {
	return p.fttlP95[keyOf(channelID, model)]
}
func (p *fakeMetricsProvider) TPSP50(channelID int, model string) float64 {
	return p.tpsP50[keyOf(channelID, model)]
}
func (p *fakeMetricsProvider) E2ELatencyP95(channelID int, model string) time.Duration {
	return p.e2eP95[keyOf(channelID, model)]
}

func keyOf(channelID int, model string) string {
	return model + ":" + itoa(channelID)
}

// itoa 避免引入 strconv，让本测试文件保持极简依赖。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

// ---- LatencyAwareStrategy ----

func TestLatency_StreamingFastChannelGetsHighScore(t *testing.T) {
	// FTTL=300ms（target=3000ms 的 10%）⇒ firstTokenScore=0.9
	// TPS=80（min=5,max=100）⇒ throughputScore=(80-5)/95≈0.789
	// score = 80 × (0.7×0.9 + 0.3×0.789) ≈ 80 × 0.867 ≈ 69.4
	p := &fakeMetricsProvider{
		fttlP95: map[string]time.Duration{"gpt-4o:1": 300 * time.Millisecond},
		tpsP50:  map[string]float64{"gpt-4o:1": 80},
	}
	s := NewLatencyAwareStrategy(p)
	ctx := ContextWithRequestStream(ContextWithRequestedModel(context.Background(), "gpt-4o"), true)
	got := s.Score(ctx, &Channel{ID: 1})
	approx(t, "fast streaming", 69.4, got, 1.0)
}

func TestLatency_StreamingSlowChannelGetsLowScore(t *testing.T) {
	// FTTL=2.7s（target 90%）⇒ firstTokenScore=0.1
	// TPS=10（接近底线）⇒ throughputScore=(10-5)/95≈0.0526
	// score = 80 × (0.7×0.1 + 0.3×0.0526) ≈ 80 × 0.086 ≈ 6.86
	p := &fakeMetricsProvider{
		fttlP95: map[string]time.Duration{"gpt-4o:1": 2700 * time.Millisecond},
		tpsP50:  map[string]float64{"gpt-4o:1": 10},
	}
	s := NewLatencyAwareStrategy(p)
	ctx := ContextWithRequestStream(ContextWithRequestedModel(context.Background(), "gpt-4o"), true)
	got := s.Score(ctx, &Channel{ID: 1})
	approx(t, "slow streaming", 6.86, got, 1.0)
}

func TestLatency_StreamingNoSignalReturnsNeutral(t *testing.T) {
	// FTTL=0 且 TPS=0 ⇒ 中位 = 80/2 = 40
	p := &fakeMetricsProvider{}
	s := NewLatencyAwareStrategy(p)
	ctx := ContextWithRequestStream(context.Background(), true)
	if got := s.Score(ctx, &Channel{ID: 1}); got != DefaultLatencyMaxScore/2 {
		t.Fatalf("expected neutral %v, got %v", DefaultLatencyMaxScore/2, got)
	}
}

func TestLatency_NonStreamingUsesE2E(t *testing.T) {
	// E2E=600ms（target 20%）⇒ score = 80 × (1 - 600/3000) = 80 × 0.8 = 64
	p := &fakeMetricsProvider{
		e2eP95: map[string]time.Duration{"claude:1": 600 * time.Millisecond},
	}
	s := NewLatencyAwareStrategy(p)
	ctx := ContextWithRequestStream(ContextWithRequestedModel(context.Background(), "claude"), false)
	got := s.Score(ctx, &Channel{ID: 1})
	approx(t, "non-streaming e2e", 64.0, got, 0.5)
}

func TestLatency_NonStreamingNoSignalReturnsNeutral(t *testing.T) {
	p := &fakeMetricsProvider{}
	s := NewLatencyAwareStrategy(p)
	ctx := ContextWithRequestStream(context.Background(), false)
	if got := s.Score(ctx, &Channel{ID: 1}); got != DefaultLatencyMaxScore/2 {
		t.Fatalf("expected neutral %v, got %v", DefaultLatencyMaxScore/2, got)
	}
}

func TestLatency_DebugDetails(t *testing.T) {
	p := &fakeMetricsProvider{
		fttlP95: map[string]time.Duration{"gpt-4o:5": 500 * time.Millisecond},
		tpsP50:  map[string]float64{"gpt-4o:5": 50},
	}
	s := NewLatencyAwareStrategy(p)
	ctx := ContextWithRequestStream(ContextWithRequestedModel(context.Background(), "gpt-4o"), true)
	_, ss := s.ScoreWithDebug(ctx, &Channel{ID: 5})
	if ss.StrategyName != "latency_aware" {
		t.Fatalf("name mismatch: %q", ss.StrategyName)
	}
	if ss.Details["fttl_p95_ms"] != int64(500) || ss.Details["tps_p50"] != 50.0 {
		t.Fatalf("details mismatch: %v", ss.Details)
	}
	if ss.Details["stream"] != true {
		t.Fatalf("stream flag missing")
	}
}

// ---- RateLimitAwareStrategy ----

func TestRateLimit_NoSignalReturnsMaxScore(t *testing.T) {
	p := &fakeMetricsProvider{}
	s := NewRateLimitAwareStrategy(p)
	if got := s.Score(context.Background(), &Channel{ID: 1}); got != DefaultRateLimitMaxScore {
		t.Fatalf("no signal: expected %v, got %v", DefaultRateLimitMaxScore, got)
	}
}

func TestRateLimit_LimitedInCooldownGetsHardPenalty(t *testing.T) {
	p := &fakeMetricsProvider{
		rateLimit: map[int]RateLimitState{
			1: {Limited: true, CooldownUntil: time.Now().Add(30 * time.Second)},
		},
	}
	s := NewRateLimitAwareStrategy(p)
	if got := s.Score(context.Background(), &Channel{ID: 1}); got != DefaultRateLimitHardPenalty {
		t.Fatalf("expected hard penalty %v, got %v", DefaultRateLimitHardPenalty, got)
	}
}

func TestRateLimit_LimitedExpiredCooldownNotPenalized(t *testing.T) {
	// Limited=true 但 CooldownUntil 已过 → 不视为硬惩罚（cooldownRemaining == 0）
	p := &fakeMetricsProvider{
		rateLimit: map[int]RateLimitState{
			1: {Limited: true, CooldownUntil: time.Now().Add(-1 * time.Minute)},
		},
	}
	s := NewRateLimitAwareStrategy(p)
	if got := s.Score(context.Background(), &Channel{ID: 1}); got != DefaultRateLimitMaxScore {
		t.Fatalf("expired cooldown: expected %v, got %v", DefaultRateLimitMaxScore, got)
	}
}

func TestRateLimit_ConcurrentFullGetsHardPenalty(t *testing.T) {
	p := &fakeMetricsProvider{
		activeConn:    map[int]int{1: 5},
		maxConcurrent: map[int]int{1: 5},
	}
	s := NewRateLimitAwareStrategy(p)
	if got := s.Score(context.Background(), &Channel{ID: 1}); got != DefaultRateLimitHardPenalty {
		t.Fatalf("expected hard penalty for full concurrent, got %v", got)
	}
}

func TestRateLimit_QuotaPartiallyUsedGivesPartialScore(t *testing.T) {
	// quotaRemaining=300 / quotaTotal=1000 ⇒ ratio=0.3
	// connRatio=0 ⇒ score = 100 × 0.3 × 1 = 30
	p := &fakeMetricsProvider{
		rateLimit: map[int]RateLimitState{
			1: {QuotaRemaining: 300, QuotaTotal: 1000},
		},
	}
	s := NewRateLimitAwareStrategy(p)
	got := s.Score(context.Background(), &Channel{ID: 1})
	approx(t, "30% quota remaining", 30.0, got, 0.0001)
}

func TestRateLimit_HighActiveConnectionsLowersScore(t *testing.T) {
	// active=8, max=10 ⇒ connRatio=0.8 ⇒ score = 100 × 1 × 0.2 = 20
	p := &fakeMetricsProvider{
		activeConn:    map[int]int{1: 8},
		maxConcurrent: map[int]int{1: 10},
	}
	s := NewRateLimitAwareStrategy(p)
	got := s.Score(context.Background(), &Channel{ID: 1})
	approx(t, "80% conn used", 20.0, got, 0.0001)
}

func TestRateLimit_DebugDetails(t *testing.T) {
	p := &fakeMetricsProvider{
		rateLimit:     map[int]RateLimitState{3: {QuotaRemaining: 50, QuotaTotal: 100}},
		activeConn:    map[int]int{3: 2},
		maxConcurrent: map[int]int{3: 4},
	}
	s := NewRateLimitAwareStrategy(p)
	score, ss := s.ScoreWithDebug(context.Background(), &Channel{ID: 3})
	// quotaRatio=0.5, connRatio=0.5 ⇒ score = 100 × 0.5 × 0.5 = 25
	approx(t, "combined", 25.0, score, 0.0001)
	if ss.Details["active_connections"] != 2 || ss.Details["max_concurrent"] != 4 {
		t.Fatalf("conn details mismatch: %v", ss.Details)
	}
	if ss.Details["quota_remaining"].(int64) != 50 || ss.Details["quota_total"].(int64) != 100 {
		t.Fatalf("quota details mismatch: %v", ss.Details)
	}
	if ss.StrategyName != "rate_limit_aware" {
		t.Fatalf("name mismatch: %q", ss.StrategyName)
	}
}

// ---- 6 strategy 联动 ----

func TestLoadBalancer_AllSixStrategiesOrdering(t *testing.T) {
	// 5 channels:
	//   ch1: 普通
	//   ch2: trace 命中（+1000）
	//   ch3: 促销（+800）
	//   ch4: 限流 cooldown 中（-10000，应排最后）
	//   ch5: 健康 + 低延迟 + 高 TPS
	// 期望首位仍是 trace 命中的 ch2，最尾是 ch4
	p := &fakeMetricsProvider{
		promotionActive: map[int]bool{3: true},
		traceMap:        map[string]int{"sess": 2},
		rateLimit: map[int]RateLimitState{
			4: {Limited: true, CooldownUntil: time.Now().Add(time.Minute)},
		},
		fttlP95: map[string]time.Duration{"gpt-4o:5": 200 * time.Millisecond},
		tpsP50:  map[string]float64{"gpt-4o:5": 80},
		requestCount:   map[int]int{1: 0, 2: 0, 3: 0, 4: 0, 5: 0},
		orderingWeight: map[int]int{1: 100, 2: 100, 3: 100, 4: 100, 5: 100},
	}
	lb := New(p,
		NewTraceAwareStrategy(p, 0),
		NewPromotionStrategy(p, 0),
		NewWeightRoundRobinStrategy(p),
		NewErrorAwareStrategy(p),
		NewLatencyAwareStrategy(p),
		NewRateLimitAwareStrategy(p),
	)
	ctx := ContextWithTraceID(
		ContextWithRequestStream(ContextWithRequestedModel(context.Background(), "gpt-4o"), true),
		"sess",
	)
	candidates := []*Channel{{ID: 1}, {ID: 2}, {ID: 3}, {ID: 4}, {ID: 5}}
	out := lb.Sort(ctx, candidates, "gpt-4o", true, 0)
	if out[0].ID != 2 {
		t.Fatalf("expected trace channel first, got id=%d", out[0].ID)
	}
	if out[len(out)-1].ID != 4 {
		t.Fatalf("expected rate-limited channel last, got id=%d", out[len(out)-1].ID)
	}
}
