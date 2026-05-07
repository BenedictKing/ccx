package loadbalance

import (
	"context"
	"testing"
	"time"
)

// fixedScoreStrategy 是测试桩：对所有 channel 返回同一个固定值。
//
// 用于验证 LoadBalancer 容器是否正确求和所有 strategy 的分数。
type fixedScoreStrategy struct {
	name  string
	score float64
}

func (s fixedScoreStrategy) Name() string { return s.name }

func (s fixedScoreStrategy) Score(_ context.Context, _ *Channel) float64 {
	return s.score
}

func (s fixedScoreStrategy) ScoreWithDebug(ctx context.Context, ch *Channel) (float64, StrategyScore) {
	return s.score, StrategyScore{
		StrategyName: s.name,
		Score:        s.score,
		Details:      map[string]any{"fixed": s.score, "channel": ch.ID},
		Duration:     time.Microsecond,
	}
}

// scoreByIDStrategy 按 channel.ID 打分（ID 越大分越高），方便构造确定性排序场景。
type scoreByIDStrategy struct{}

func (scoreByIDStrategy) Name() string { return "by-id" }

func (scoreByIDStrategy) Score(_ context.Context, ch *Channel) float64 {
	return float64(ch.ID)
}

func (s scoreByIDStrategy) ScoreWithDebug(_ context.Context, ch *Channel) (float64, StrategyScore) {
	return float64(ch.ID), StrategyScore{StrategyName: s.Name(), Score: float64(ch.ID)}
}

// ---- 容器行为 ----

func TestLoadBalancer_Sort_NoStrategies_StableByOrderingWeight(t *testing.T) {
	lb := New(nil)
	candidates := []*Channel{
		{ID: 1, OrderingWeight: 1},
		{ID: 2, OrderingWeight: 9},
		{ID: 3, OrderingWeight: 5},
	}
	out := lb.Sort(context.Background(), candidates, "", false, 0)
	if len(out) != 3 {
		t.Fatalf("expected 3 channels, got %d", len(out))
	}
	// 全部 0 分，secondary key OrderingWeight 大者前。
	if out[0].ID != 2 || out[1].ID != 3 || out[2].ID != 1 {
		t.Fatalf("unexpected order: %d, %d, %d", out[0].ID, out[1].ID, out[2].ID)
	}
}

func TestLoadBalancer_Sort_FixedStrategies_Sum(t *testing.T) {
	lb := New(nil,
		fixedScoreStrategy{name: "a", score: 100},
		fixedScoreStrategy{name: "b", score: 50},
	)
	candidates := []*Channel{
		{ID: 1, OrderingWeight: 1},
		{ID: 2, OrderingWeight: 5},
	}
	_, log := lb.SortWithDebug(context.Background(), candidates, "gpt-4o", false, 0)
	for _, d := range log.Channels {
		if d.TotalScore != 150 {
			t.Fatalf("expected total=150, got %v", d.TotalScore)
		}
		if len(d.StrategyScores) != 2 {
			t.Fatalf("expected 2 strategy scores, got %d", len(d.StrategyScores))
		}
	}
}

func TestLoadBalancer_Sort_TopKLimitsOutput(t *testing.T) {
	lb := New(nil, scoreByIDStrategy{})
	candidates := []*Channel{
		{ID: 1}, {ID: 2}, {ID: 3}, {ID: 4}, {ID: 5},
	}
	top2 := lb.Sort(context.Background(), candidates, "", false, 2)
	if len(top2) != 2 {
		t.Fatalf("expected top-2, got %d", len(top2))
	}
	// ID 越大分越高，前 2 应为 5、4。
	if top2[0].ID != 5 || top2[1].ID != 4 {
		t.Fatalf("expected [5,4], got [%d,%d]", top2[0].ID, top2[1].ID)
	}
}

func TestLoadBalancer_Sort_NegativeOrZeroTopKReturnsAll(t *testing.T) {
	lb := New(nil, scoreByIDStrategy{})
	candidates := []*Channel{{ID: 1}, {ID: 2}, {ID: 3}}
	for _, k := range []int{0, -1, 100} {
		out := lb.Sort(context.Background(), candidates, "", false, k)
		if len(out) != len(candidates) {
			t.Fatalf("topK=%d: expected %d, got %d", k, len(candidates), len(out))
		}
	}
}

func TestLoadBalancer_Sort_EmptyInput(t *testing.T) {
	lb := New(nil, scoreByIDStrategy{})
	if got := lb.Sort(context.Background(), nil, "", false, 0); got != nil {
		t.Fatalf("expected nil for empty input, got %v", got)
	}
	out, log := lb.SortWithDebug(context.Background(), nil, "", false, 0)
	if out != nil || log.ChannelCount != 0 {
		t.Fatalf("debug path: expected zero log, got %+v", log)
	}
}

func TestLoadBalancer_Sort_DoesNotMutateInputOrder(t *testing.T) {
	candidates := []*Channel{{ID: 1}, {ID: 2}, {ID: 3}}
	original := []*Channel{candidates[0], candidates[1], candidates[2]}

	lb := New(nil, scoreByIDStrategy{})
	_ = lb.Sort(context.Background(), candidates, "", false, 2)

	for i, c := range candidates {
		if c.ID != original[i].ID {
			t.Fatalf("input order mutated at %d: got %d want %d", i, c.ID, original[i].ID)
		}
	}
}

// ---- DecisionLog ----

func TestLoadBalancer_SortWithDebug_FillsDecisionLog(t *testing.T) {
	lb := New(nil,
		fixedScoreStrategy{name: "promotion", score: 800},
		scoreByIDStrategy{},
	)
	candidates := []*Channel{
		{ID: 1, Name: "ch1", OrderingWeight: 5},
		{ID: 7, Name: "ch7", OrderingWeight: 1},
	}
	_, log := lb.SortWithDebug(context.Background(), candidates, "claude", true, 0)

	if log.ChannelCount != 2 {
		t.Fatalf("ChannelCount=%d", log.ChannelCount)
	}
	if log.TotalDuration < 0 {
		t.Fatalf("TotalDuration must not be negative, got %v", log.TotalDuration)
	}
	// 排序后 FinalRank=1 应是 ID=7（800+7=807 > 800+1=801）。
	if log.Channels[0].FinalRank != 1 || log.Channels[0].Channel.ID != 7 {
		t.Fatalf("expected rank-1 = ch7, got %+v", log.Channels[0])
	}
	if log.Channels[1].FinalRank != 2 {
		t.Fatalf("expected rank-2, got %d", log.Channels[1].FinalRank)
	}
	for _, d := range log.Channels {
		if len(d.StrategyScores) != 2 {
			t.Fatalf("expected 2 strategy scores per channel, got %d", len(d.StrategyScores))
		}
	}
}

// ---- partial sort 单测 ----

func TestPartialSortTopK_KEqualsLen(t *testing.T) {
	items := []int{3, 1, 4, 1, 5, 9, 2, 6}
	partialSortTopK(items, len(items), func(a, b int) bool { return a < b })
	for i := 1; i < len(items); i++ {
		if items[i-1] > items[i] {
			t.Fatalf("not sorted at %d: %v", i, items)
		}
	}
}

func TestPartialSortTopK_KSmallerThanLen(t *testing.T) {
	items := []int{3, 1, 4, 1, 5, 9, 2, 6}
	partialSortTopK(items, 3, func(a, b int) bool { return a < b })
	want := []int{1, 1, 2}
	for i, v := range want {
		if items[i] != v {
			t.Fatalf("top-3 mismatch at %d: got %v", i, items[:3])
		}
	}
}

func TestPartialSortTopK_NoOpEdges(t *testing.T) {
	partialSortTopK(([]int)(nil), 3, func(a, b int) bool { return a < b })  // empty
	partialSortTopK([]int{42}, 3, func(a, b int) bool { return a < b })     // single
	partialSortTopK([]int{2, 1}, 0, func(a, b int) bool { return a < b })   // k=0
	partialSortTopK([]int{2, 1}, -1, func(a, b int) bool { return a < b })  // k<0
}

// ---- ctx helpers ----

func TestContextHelpers_RoundTrip(t *testing.T) {
	ctx := context.Background()
	if got := RequestedModelFromContext(ctx); got != "" {
		t.Fatalf("expected empty model, got %q", got)
	}
	ctx = ContextWithRequestedModel(ctx, "gpt-5")
	if got := RequestedModelFromContext(ctx); got != "gpt-5" {
		t.Fatalf("model mismatch: %q", got)
	}
	ctx = ContextWithRequestStream(ctx, true)
	if !RequestStreamFromContext(ctx) {
		t.Fatal("stream flag lost")
	}
	ctx = ContextWithTraceID(ctx, "trace-abc")
	if got := TraceIDFromContext(ctx); got != "trace-abc" {
		t.Fatalf("trace mismatch: %q", got)
	}
	// 空字符串注入应返回原 ctx（不污染）。
	ctx2 := ContextWithRequestedModel(ctx, "")
	if got := RequestedModelFromContext(ctx2); got != "gpt-5" {
		t.Fatalf("empty injection should keep prior value, got %q", got)
	}
}

// ---- noop provider ----

func TestNoopMetricsProvider_AllZero(t *testing.T) {
	p := NewNoopMetricsProvider()
	if id, ok := p.LastSuccessfulChannelByTrace(context.Background(), "x"); id != 0 || ok {
		t.Fatal("expected zero/false")
	}
	if p.ConsecutiveFailures(1) != 0 {
		t.Fatal("expected 0")
	}
	if !p.LastFailureTime(1).IsZero() {
		t.Fatal("expected zero time")
	}
	if p.RequestCount(1, time.Minute) != 0 {
		t.Fatal("expected 0")
	}
	if p.OrderingWeight(1) != 0 {
		t.Fatal("expected 0")
	}
	if p.FTTLP95(1, "m") != 0 || p.TPSP50(1, "m") != 0 || p.E2ELatencyP95(1, "m") != 0 {
		t.Fatal("expected zeros for latency metrics")
	}
	rs := p.RateLimitState(1)
	if rs.Limited || !rs.CooldownUntil.IsZero() {
		t.Fatalf("expected zero RateLimitState, got %+v", rs)
	}
	if p.ActiveConnections(1) != 0 || p.MaxConcurrent(1) != 0 {
		t.Fatal("expected 0")
	}
	if p.IsPromotionActive(1) {
		t.Fatal("expected false")
	}
}
