package loadbalance

import (
	"context"
	"math"
	"testing"
	"time"
)

// approx 用于浮点近似断言；abs(want-got) < eps。
func approx(t *testing.T, name string, want, got, eps float64) {
	t.Helper()
	if math.Abs(want-got) > eps {
		t.Fatalf("%s: want %.4f got %.4f (eps=%.4f)", name, want, got, eps)
	}
}

// ---- WeightRoundRobinStrategy ----

func TestWRR_ZeroRequestsReturnsMaxScore(t *testing.T) {
	p := &fakeMetricsProvider{
		requestCount:   map[int]int{1: 0},
		orderingWeight: map[int]int{1: 100},
	}
	s := NewWeightRoundRobinStrategy(p)
	if got := s.Score(context.Background(), &Channel{ID: 1}); got != DefaultWRRMaxScore {
		t.Fatalf("zero requests: expected %v, got %v", DefaultWRRMaxScore, got)
	}
}

func TestWRR_HigherWeightYieldsHigherScore(t *testing.T) {
	// 同请求数下，权重高的得分更高（更耐受请求）。
	p := &fakeMetricsProvider{
		requestCount:   map[int]int{1: 50, 2: 50},
		orderingWeight: map[int]int{1: 100, 2: 200},
	}
	s := NewWeightRoundRobinStrategy(p)
	a := s.Score(context.Background(), &Channel{ID: 1})
	b := s.Score(context.Background(), &Channel{ID: 2})
	if !(b > a) {
		t.Fatalf("higher weight should yield higher score: id1=%v id2=%v", a, b)
	}
}

func TestWRR_FewerRequestsYieldsHigherScore(t *testing.T) {
	// 同权重下，请求少的得分高。
	p := &fakeMetricsProvider{
		requestCount:   map[int]int{1: 10, 2: 200},
		orderingWeight: map[int]int{1: 100, 2: 100},
	}
	s := NewWeightRoundRobinStrategy(p)
	low := s.Score(context.Background(), &Channel{ID: 1})
	high := s.Score(context.Background(), &Channel{ID: 2})
	if !(low > high) {
		t.Fatalf("fewer requests should yield higher score: 10req=%v 200req=%v", low, high)
	}
}

func TestWRR_ScoreClampedToMinScore(t *testing.T) {
	// 极端高请求数 + 低权重 ⇒ 分数被 clamp 到 minScore。
	p := &fakeMetricsProvider{
		requestCount:   map[int]int{1: 100000},
		orderingWeight: map[int]int{1: 10},
	}
	s := NewWeightRoundRobinStrategy(p)
	got := s.Score(context.Background(), &Channel{ID: 1})
	if got < DefaultWRRMinScore-0.0001 || got > DefaultWRRMinScore+0.0001 {
		t.Fatalf("expected clamped to minScore=%v, got %v", DefaultWRRMinScore, got)
	}
}

func TestWRR_DebugIncludesRequestCountAndWeight(t *testing.T) {
	p := &fakeMetricsProvider{
		requestCount:   map[int]int{7: 25},
		orderingWeight: map[int]int{7: 80},
	}
	s := NewWeightRoundRobinStrategy(p)
	score, ss := s.ScoreWithDebug(context.Background(), &Channel{ID: 7})
	if score <= 0 {
		t.Fatalf("unexpected score %v", score)
	}
	if ss.Details["request_count"] != 25 || ss.Details["weight"] != 80 {
		t.Fatalf("debug details mismatch: %v", ss.Details)
	}
	if ss.StrategyName != "weight_rr" {
		t.Fatalf("name mismatch: %q", ss.StrategyName)
	}
}

func TestWRR_FallsBackToChannelOrderingWeightWhenProviderHasNone(t *testing.T) {
	// provider 不暴露 weight，channel.OrderingWeight 应作为 fallback。
	p := &fakeMetricsProvider{
		requestCount: map[int]int{1: 50, 2: 50},
		// orderingWeight 故意不设
	}
	s := NewWeightRoundRobinStrategy(p)
	a := s.Score(context.Background(), &Channel{ID: 1, OrderingWeight: 50})
	b := s.Score(context.Background(), &Channel{ID: 2, OrderingWeight: 200})
	if !(b > a) {
		t.Fatalf("channel-level weight should be fallback: id1=%v id2=%v", a, b)
	}
}

func TestWRR_NilProviderAndNilChannelSafe(t *testing.T) {
	s := NewWeightRoundRobinStrategy(nil)
	mid := (DefaultWRRMaxScore + DefaultWRRMinScore) / 2
	if got := s.Score(context.Background(), nil); got != mid {
		t.Fatalf("nil channel: expected %v, got %v", mid, got)
	}
}

// ---- ErrorAwareStrategy ----

func TestErrorAware_CleanChannelGetsMaxScore(t *testing.T) {
	p := &fakeMetricsProvider{}
	s := NewErrorAwareStrategy(p)
	if got := s.Score(context.Background(), &Channel{ID: 1}); got != DefaultErrorMaxScore {
		t.Fatalf("clean channel: expected %v, got %v", DefaultErrorMaxScore, got)
	}
}

func TestErrorAware_RecentFailurePenalty(t *testing.T) {
	// 连续 2 次失败、刚刚发生：cooldownRatio≈1，
	// score = 200 - 2×30×1 - 40×1 = 100
	p := &fakeMetricsProvider{
		consecFailures: map[int]int{1: 2},
		lastFailure:    map[int]time.Time{1: time.Now()},
	}
	s := NewErrorAwareStrategy(p)
	got := s.Score(context.Background(), &Channel{ID: 1})
	approx(t, "recent 2-failure score", 100.0, got, 2.0) // 容忍 1-2 秒的衰减
}

func TestErrorAware_HalfwayCooldownHalfPenalty(t *testing.T) {
	// 失败发生在 cooldown 一半（2.5 min）前：cooldownRatio≈0.5
	// score = 200 - 2×30×0.5 - 40×0.5 = 200 - 30 - 20 = 150
	half := DefaultErrorCooldown / 2
	p := &fakeMetricsProvider{
		consecFailures: map[int]int{1: 2},
		lastFailure:    map[int]time.Time{1: time.Now().Add(-half)},
	}
	s := NewErrorAwareStrategy(p)
	got := s.Score(context.Background(), &Channel{ID: 1})
	approx(t, "halfway score", 150.0, got, 2.0)
}

func TestErrorAware_OldFailureFullyRecovered(t *testing.T) {
	// 失败超过 cooldown：cooldownRatio = 0，得满分。
	p := &fakeMetricsProvider{
		consecFailures: map[int]int{1: 5},
		lastFailure:    map[int]time.Time{1: time.Now().Add(-2 * DefaultErrorCooldown)},
	}
	s := NewErrorAwareStrategy(p)
	if got := s.Score(context.Background(), &Channel{ID: 1}); got != DefaultErrorMaxScore {
		t.Fatalf("recovered channel: expected %v, got %v", DefaultErrorMaxScore, got)
	}
}

func TestErrorAware_FailuresWithoutTimestampTreatedAsJustNow(t *testing.T) {
	// 没有 LastFailureTime 但 ConsecutiveFailures > 0：cooldownRatio = 1.0
	// score = 200 - 1×30 - 40 = 130
	p := &fakeMetricsProvider{
		consecFailures: map[int]int{1: 1},
	}
	s := NewErrorAwareStrategy(p)
	got := s.Score(context.Background(), &Channel{ID: 1})
	approx(t, "no-timestamp 1-failure score", 130.0, got, 0.0001)
}

func TestErrorAware_MassiveFailuresClampedToZero(t *testing.T) {
	// 100 次连续失败 + 刚发生：penalty 远超 maxScore，score 应被 clamp 到 0。
	p := &fakeMetricsProvider{
		consecFailures: map[int]int{1: 100},
		lastFailure:    map[int]time.Time{1: time.Now()},
	}
	s := NewErrorAwareStrategy(p)
	if got := s.Score(context.Background(), &Channel{ID: 1}); got != 0 {
		t.Fatalf("massive failures: expected 0, got %v", got)
	}
}

func TestErrorAware_DebugDetails(t *testing.T) {
	p := &fakeMetricsProvider{
		consecFailures: map[int]int{3: 4},
		lastFailure:    map[int]time.Time{3: time.Now()},
	}
	s := NewErrorAwareStrategy(p)
	_, ss := s.ScoreWithDebug(context.Background(), &Channel{ID: 3})
	if ss.StrategyName != "error_aware" {
		t.Fatalf("name mismatch: %q", ss.StrategyName)
	}
	if ss.Details["consecutive_failures"] != 4 {
		t.Fatalf("details mismatch: %v", ss.Details)
	}
	ratio, ok := ss.Details["cooldown_ratio"].(float64)
	if !ok || ratio < 0.95 {
		t.Fatalf("expected cooldown_ratio≈1.0, got %v", ss.Details["cooldown_ratio"])
	}
}

func TestErrorAware_NilSafe(t *testing.T) {
	s := NewErrorAwareStrategy(nil)
	if got := s.Score(context.Background(), nil); got != DefaultErrorMaxScore/2 {
		t.Fatalf("nil channel: expected %v, got %v", DefaultErrorMaxScore/2, got)
	}
}

// ---- 4 个 strategy 共用 LB 集成测试 ----

func TestLoadBalancer_AllFourStrategiesIntegration(t *testing.T) {
	// 场景：4 channel
	//   ch1 普通
	//   ch2 trace 命中（+1000）
	//   ch3 促销期（+800）
	//   ch4 最近失败 2 次（ErrorAware 扣分）
	// 期望：ch2 > ch3 > ch1 > ch4
	p := &fakeMetricsProvider{
		promotionActive: map[int]bool{3: true},
		traceMap:        map[string]int{"sess": 2},
		consecFailures:  map[int]int{4: 2},
		lastFailure:     map[int]time.Time{4: time.Now()},
		requestCount:    map[int]int{1: 0, 2: 0, 3: 0, 4: 0},
		orderingWeight:  map[int]int{1: 100, 2: 100, 3: 100, 4: 100},
	}
	lb := New(p,
		NewTraceAwareStrategy(p, 0),
		NewPromotionStrategy(p, 0),
		NewWeightRoundRobinStrategy(p),
		NewErrorAwareStrategy(p),
	)
	ctx := ContextWithTraceID(context.Background(), "sess")
	candidates := []*Channel{{ID: 1}, {ID: 2}, {ID: 3}, {ID: 4}}
	out := lb.Sort(ctx, candidates, "gpt-4o", false, 0)
	if out[0].ID != 2 || out[1].ID != 3 {
		t.Fatalf("expected [2,3,...], got [%d,%d,%d,%d]", out[0].ID, out[1].ID, out[2].ID, out[3].ID)
	}
	if out[3].ID != 4 {
		t.Fatalf("error-penalty channel should be last, got id=%d", out[3].ID)
	}
}
