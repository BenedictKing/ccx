package metrics

import (
	"sync"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/eventbus"
)

// TestCircuitStateTransitions_PublishEvents 验证 B.1：
// 三个熔断迁移点发布 eventbus.TypeCircuitBreakerStateChanged，from/to/subject 正确。
func TestCircuitStateTransitions_PublishEvents(t *testing.T) {
	bus := eventbus.NewBus()
	ch, unsub := bus.Subscribe(eventbus.TypeCircuitBreakerStateChanged)
	defer unsub()

	mgr := NewMetricsManager()
	mgr.apiType = "messages"
	mgr.SetEventBus(bus)

	km := &KeyMetrics{
		MetricsKey: "mk-1",
		BaseURL:    "https://example.com",
		KeyMask:    "sk-***ab",
	}
	mgr.keyMetrics[km.MetricsKey] = km

	// Closed → Open
	mgr.mu.Lock()
	km.CircuitState = CircuitStateClosed
	mgr.mu.Unlock()

	mgr.mu.Lock()
	mgr.moveCircuitToOpenLocked(km, time.Now(), false)
	mgr.mu.Unlock()

	// Open → HalfOpen
	mgr.mu.Lock()
	km.CircuitState = CircuitStateOpen
	km.NextRetryAt = ptrTime(time.Now().Add(-time.Second))
	mgr.advanceCircuitStateIfDueLocked(km, time.Now())
	mgr.mu.Unlock()

	// HalfOpen → Closed
	mgr.mu.Lock()
	km.CircuitState = CircuitStateHalfOpen
	mgr.resetCircuitStateLocked(km, true)
	mgr.mu.Unlock()

	got := drainTimeout(ch, 3, 200*time.Millisecond)
	if len(got) != 3 {
		t.Fatalf("期望 3 条迁移事件，实际 %d", len(got))
	}
	expect := []struct{ from, to string }{
		{"closed", "open"},
		{"open", "half_open"},
		{"half_open", "closed"},
	}
	for i, ex := range expect {
		if got[i].From != ex.from || got[i].To != ex.to {
			t.Errorf("第 %d 条：from=%s to=%s，期望 from=%s to=%s", i, got[i].From, got[i].To, ex.from, ex.to)
		}
		if got[i].Subject != "mk-1" {
			t.Errorf("第 %d 条：Subject=%s，期望 mk-1", i, got[i].Subject)
		}
		if got[i].ChannelKind != "messages" {
			t.Errorf("第 %d 条：ChannelKind=%s，期望 messages", i, got[i].ChannelKind)
		}
	}
}

// TestCircuitState_SameState_NoEvent 验证：from == to 时不发布事件。
func TestCircuitState_SameState_NoEvent(t *testing.T) {
	bus := eventbus.NewBus()
	ch, unsub := bus.Subscribe(eventbus.TypeCircuitBreakerStateChanged)
	defer unsub()

	mgr := NewMetricsManager()
	mgr.apiType = "messages"
	mgr.SetEventBus(bus)

	km := &KeyMetrics{
		MetricsKey: "mk-1",
		BaseURL:    "https://example.com",
		KeyMask:    "sk-***ab",
		CircuitState: CircuitStateClosed,
	}
	mgr.keyMetrics[km.MetricsKey] = km

	// 已经 Closed，resetCircuitStateLocked 的 from=closed 写入后状态仍为 closed
	mgr.mu.Lock()
	// 实际是 reset 写 closed from closed → publishCircuitEventLocked 会判断 from==to 并直接 return
	// 走到这里 0 条事件
	mgr.resetCircuitStateLocked(km, true)
	mgr.mu.Unlock()

	// 等 50ms 确保无事件
	got := drainTimeout(ch, 1, 50*time.Millisecond)
	if len(got) != 0 {
		t.Errorf("from==to 时不应发布事件，实际收到 %d 条", len(got))
	}
}

// TestCircuitState_NilBus_NoPanic 验证：bus 未注入时不 panic。
func TestCircuitState_NilBus_NoPanic(t *testing.T) {
	mgr := NewMetricsManager()
	mgr.apiType = "messages"
	// 不调用 SetEventBus，bus 字段为零值 atomic.Pointer[Bus] = nil

	km := &KeyMetrics{
		MetricsKey:  "mk-1",
		BaseURL:     "https://example.com",
		KeyMask:     "sk-***ab",
		CircuitState: CircuitStateClosed,
	}
	mgr.keyMetrics[km.MetricsKey] = km

	// 不应 panic
	mgr.mu.Lock()
	mgr.moveCircuitToOpenLocked(km, time.Now(), false)
	mgr.moveCircuitToHalfOpenLocked(km, time.Now())
	mgr.resetCircuitStateLocked(km, true)
	mgr.mu.Unlock()
}

func ptrTime(t time.Time) *time.Time { return &t }

func drainTimeout(ch <-chan eventbus.Event, n int, timeout time.Duration) []eventbus.Event {
	out := make([]eventbus.Event, 0, n)
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	for len(out) < n {
		select {
		case ev, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, ev)
		case <-timer.C:
			return out
		}
	}
	return out
}

var _ = sync.Mutex{} // 引入 sync 仅为与既有文件风格一致（必要时）
