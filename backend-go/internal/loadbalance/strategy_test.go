package loadbalance

import (
	"context"
	"testing"
	"time"
)

// fakeMetricsProvider 是单测桩：嵌入 noop 默认实现，按需覆盖具体方法。
//
// 这样测试只需关心被测 strategy 真正消费的字段，其余 13 方法返回零值。
type fakeMetricsProvider struct {
	noopMetricsProvider // 默认零值实现
	promotionActive     map[int]bool
	traceMap            map[string]int    // traceID → channelID
	consecFailures      map[int]int
	lastFailure         map[int]time.Time
	requestCount        map[int]int
	orderingWeight      map[int]int
	rateLimit           map[int]RateLimitState
	activeConn          map[int]int
	maxConcurrent       map[int]int
	fttlP95             map[string]time.Duration // key = "channelID/model"
	tpsP50              map[string]float64
	e2eP95              map[string]time.Duration
}

func (p *fakeMetricsProvider) IsPromotionActive(channelID int) bool {
	return p.promotionActive[channelID]
}

func (p *fakeMetricsProvider) LastSuccessfulChannelByTrace(_ context.Context, traceID string) (int, bool) {
	id, ok := p.traceMap[traceID]
	return id, ok
}

func (p *fakeMetricsProvider) ConsecutiveFailures(channelID int) int {
	return p.consecFailures[channelID]
}
func (p *fakeMetricsProvider) LastFailureTime(channelID int) time.Time {
	return p.lastFailure[channelID]
}
func (p *fakeMetricsProvider) RequestCount(channelID int, _ time.Duration) int {
	return p.requestCount[channelID]
}
func (p *fakeMetricsProvider) OrderingWeight(channelID int) int {
	return p.orderingWeight[channelID]
}
func (p *fakeMetricsProvider) RateLimitState(channelID int) RateLimitState {
	return p.rateLimit[channelID]
}
func (p *fakeMetricsProvider) ActiveConnections(channelID int) int {
	return p.activeConn[channelID]
}
func (p *fakeMetricsProvider) MaxConcurrent(channelID int) int {
	return p.maxConcurrent[channelID]
}

// ---- PromotionStrategy ----

func TestPromotionStrategy_GivesBoostWhenActive(t *testing.T) {
	p := &fakeMetricsProvider{promotionActive: map[int]bool{2: true}}
	s := NewPromotionStrategy(p, 0) // 0 → 默认 800
	if got := s.Score(context.Background(), &Channel{ID: 2}); got != DefaultPromotionBoost {
		t.Fatalf("active channel: expected %v, got %v", DefaultPromotionBoost, got)
	}
	if got := s.Score(context.Background(), &Channel{ID: 1}); got != 0 {
		t.Fatalf("inactive channel: expected 0, got %v", got)
	}
}

func TestPromotionStrategy_CustomBoostAndNilChannel(t *testing.T) {
	s := NewPromotionStrategy(&fakeMetricsProvider{promotionActive: map[int]bool{1: true}}, 250)
	if got := s.Score(context.Background(), &Channel{ID: 1}); got != 250 {
		t.Fatalf("custom boost: expected 250, got %v", got)
	}
	// nil channel 不 panic 且返回 0。
	if got := s.Score(context.Background(), nil); got != 0 {
		t.Fatalf("nil channel: expected 0, got %v", got)
	}
}

func TestPromotionStrategy_DebugDetails(t *testing.T) {
	p := &fakeMetricsProvider{promotionActive: map[int]bool{5: true}}
	s := NewPromotionStrategy(p, 0)
	score, ss := s.ScoreWithDebug(context.Background(), &Channel{ID: 5})
	if score != DefaultPromotionBoost {
		t.Fatalf("expected boost score, got %v", score)
	}
	if ss.StrategyName != "promotion" {
		t.Fatalf("name mismatch: %q", ss.StrategyName)
	}
	if ss.Details["applied"] != true {
		t.Fatalf("expected applied=true, got %v", ss.Details["applied"])
	}
	if ss.Details["channel_id"] != 5 {
		t.Fatalf("expected channel_id=5, got %v", ss.Details["channel_id"])
	}
	if ss.Details["boost"] != DefaultPromotionBoost {
		t.Fatalf("boost detail mismatch: %v", ss.Details["boost"])
	}
}

func TestPromotionStrategy_NilProviderFallsBackToNoop(t *testing.T) {
	s := NewPromotionStrategy(nil, 0)
	if got := s.Score(context.Background(), &Channel{ID: 1}); got != 0 {
		t.Fatalf("nil provider: expected 0, got %v", got)
	}
}

func TestPromotionStrategy_NegativeBoostFallsBackToDefault(t *testing.T) {
	p := &fakeMetricsProvider{promotionActive: map[int]bool{1: true}}
	s := NewPromotionStrategy(p, -10)
	if got := s.Score(context.Background(), &Channel{ID: 1}); got != DefaultPromotionBoost {
		t.Fatalf("negative boost should fallback to default, got %v", got)
	}
}

// ---- TraceAwareStrategy ----

func TestTraceAware_GivesHitScoreOnMatch(t *testing.T) {
	p := &fakeMetricsProvider{traceMap: map[string]int{"trace-A": 7}}
	s := NewTraceAwareStrategy(p, 0)
	ctx := ContextWithTraceID(context.Background(), "trace-A")

	if got := s.Score(ctx, &Channel{ID: 7}); got != DefaultTraceAwareHit {
		t.Fatalf("matching channel: expected %v, got %v", DefaultTraceAwareHit, got)
	}
	if got := s.Score(ctx, &Channel{ID: 99}); got != 0 {
		t.Fatalf("non-matching channel: expected 0, got %v", got)
	}
}

func TestTraceAware_NoTraceIDInCtx_ReturnsZero(t *testing.T) {
	p := &fakeMetricsProvider{traceMap: map[string]int{"trace-A": 7}}
	s := NewTraceAwareStrategy(p, 0)
	if got := s.Score(context.Background(), &Channel{ID: 7}); got != 0 {
		t.Fatalf("no trace id: expected 0, got %v", got)
	}
}

func TestTraceAware_TraceIDNotFoundInProvider_ReturnsZero(t *testing.T) {
	p := &fakeMetricsProvider{traceMap: map[string]int{"trace-A": 7}}
	s := NewTraceAwareStrategy(p, 0)
	ctx := ContextWithTraceID(context.Background(), "trace-UNKNOWN")
	if got := s.Score(ctx, &Channel{ID: 7}); got != 0 {
		t.Fatalf("unknown trace: expected 0, got %v", got)
	}
}

func TestTraceAware_NilChannelOrNilProvider(t *testing.T) {
	if got := NewTraceAwareStrategy(nil, 0).Score(context.Background(), &Channel{ID: 1}); got != 0 {
		t.Fatalf("nil provider: expected 0, got %v", got)
	}
	s := NewTraceAwareStrategy(&fakeMetricsProvider{}, 0)
	if got := s.Score(context.Background(), nil); got != 0 {
		t.Fatalf("nil channel: expected 0, got %v", got)
	}
}

func TestTraceAware_DebugIncludesTraceID(t *testing.T) {
	p := &fakeMetricsProvider{traceMap: map[string]int{"trace-A": 3}}
	s := NewTraceAwareStrategy(p, 0)
	ctx := ContextWithTraceID(context.Background(), "trace-A")

	score, ss := s.ScoreWithDebug(ctx, &Channel{ID: 3})
	if score != DefaultTraceAwareHit || ss.StrategyName != "trace_aware" {
		t.Fatalf("score/name mismatch: score=%v name=%q", score, ss.StrategyName)
	}
	if ss.Details["trace_id"] != "trace-A" {
		t.Fatalf("trace_id detail missing: %v", ss.Details["trace_id"])
	}
	if ss.Details["applied"] != true {
		t.Fatalf("expected applied=true, got %v", ss.Details["applied"])
	}
}

// ---- 集成：两个 strategy 共用 LB ----

func TestLoadBalancer_PromotionAndTraceCoexist(t *testing.T) {
	// 场景：3 个 channel
	//   ch=1 普通
	//   ch=2 处于促销期
	//   ch=3 是 trace 亲和命中
	// 期望排序：ch3（1000）> ch2（800）> ch1（0）
	p := &fakeMetricsProvider{
		promotionActive: map[int]bool{2: true},
		traceMap:        map[string]int{"sess-X": 3},
	}
	lb := New(p,
		NewTraceAwareStrategy(p, 0),
		NewPromotionStrategy(p, 0),
	)
	ctx := ContextWithTraceID(context.Background(), "sess-X")

	candidates := []*Channel{
		{ID: 1}, {ID: 2}, {ID: 3},
	}
	sorted := lb.Sort(ctx, candidates, "gpt-4o", false, 0)
	if len(sorted) != 3 {
		t.Fatalf("expected 3 channels, got %d", len(sorted))
	}
	if sorted[0].ID != 3 || sorted[1].ID != 2 || sorted[2].ID != 1 {
		t.Fatalf("expected order [3,2,1], got [%d,%d,%d]", sorted[0].ID, sorted[1].ID, sorted[2].ID)
	}
}

func TestLoadBalancer_TraceMissShowsPromotionWins(t *testing.T) {
	// trace 不命中时，promotion 应胜出。
	p := &fakeMetricsProvider{
		promotionActive: map[int]bool{2: true},
		// traceMap 故意不设 sess-X
	}
	lb := New(p,
		NewTraceAwareStrategy(p, 0),
		NewPromotionStrategy(p, 0),
	)
	ctx := ContextWithTraceID(context.Background(), "sess-X")
	candidates := []*Channel{{ID: 1}, {ID: 2}, {ID: 3}}
	sorted := lb.Sort(ctx, candidates, "", false, 0)
	if sorted[0].ID != 2 {
		t.Fatalf("expected promotion channel first, got id=%d", sorted[0].ID)
	}
}
