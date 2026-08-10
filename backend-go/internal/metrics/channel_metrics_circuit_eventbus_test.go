package metrics

import (
	"sync"
	"testing"
	"time"

	"github.com/BenedictKing/ccx/internal/eventbus"
)

// TestCircuitStateTransitions_PublishEvents 验证 B.1：
// 三个熔断迁移点同时发布 eventbus.TypeCircuitBreakerStateChanged 与 TypeChannelStatusChanged，from/to/subject 正确。
func TestCircuitStateTransitions_PublishEvents(t *testing.T) {
	bus := eventbus.NewBus()
	circuitCh, unsubCircuit := bus.Subscribe(eventbus.TypeCircuitBreakerStateChanged)
	defer unsubCircuit()
	statusCh, unsubStatus := bus.Subscribe(eventbus.TypeChannelStatusChanged)
	defer unsubStatus()

	mgr := NewMetricsManager()
	mgr.apiType = "messages"
	mgr.SetEventBus(bus)

	km := &KeyMetrics{
		MetricsKey: "mk-1",
		BaseURL:    "https://example.com",
		KeyMask:    "sk-***ab",
	}
	mgr.keyMetrics[km.MetricsKey] = km

	// Closed -> Open
	mgr.mu.Lock()
	km.CircuitState = CircuitStateClosed
	mgr.mu.Unlock()

	mgr.mu.Lock()
	mgr.moveCircuitToOpenLocked(km, time.Now(), false, "breaker_threshold")
	mgr.mu.Unlock()

	// Open -> HalfOpen
	mgr.mu.Lock()
	km.CircuitState = CircuitStateOpen
	km.NextRetryAt = ptrTime(time.Now().Add(-time.Second))
	mgr.advanceCircuitStateIfDueLocked(km, time.Now())
	mgr.mu.Unlock()

	// HalfOpen -> Closed
	mgr.mu.Lock()
	km.CircuitState = CircuitStateHalfOpen
	mgr.resetCircuitStateLocked(km, true)
	mgr.mu.Unlock()

	gotCircuit := drainTimeout(circuitCh, 3, 200*time.Millisecond)
	if len(gotCircuit) != 3 {
		t.Fatalf("期望 3 条熔断迁移事件，实际 %d", len(gotCircuit))
	}
	gotStatus := drainTimeout(statusCh, 3, 200*time.Millisecond)
	if len(gotStatus) != 3 {
		t.Fatalf("期望 3 条渠道状态迁移事件，实际 %d", len(gotStatus))
	}

	expect := []struct {
		from, to string
		reason   string
	}{
		{"closed", "open", "breaker_threshold"},
		{"open", "half_open", "cooldown_expired"},
		{"half_open", "closed", "probe_success"},
	}
	for i, ex := range expect {
		if gotCircuit[i].From != ex.from || gotCircuit[i].To != ex.to {
			t.Errorf("第 %d 条熔断事件：from=%s to=%s，期望 from=%s to=%s", i, gotCircuit[i].From, gotCircuit[i].To, ex.from, ex.to)
		}
		if gotCircuit[i].Subject != "mk-1" {
			t.Errorf("第 %d 条熔断事件：Subject=%s，期望 mk-1", i, gotCircuit[i].Subject)
		}
		if gotCircuit[i].ChannelKind != "messages" {
			t.Errorf("第 %d 条熔断事件：ChannelKind=%s，期望 messages", i, gotCircuit[i].ChannelKind)
		}

		status := gotStatus[i]
		if status.From != ex.from || status.To != ex.to {
			t.Errorf("第 %d 条状态事件：from=%s to=%s，期望 from=%s to=%s", i, status.From, status.To, ex.from, ex.to)
		}
		if status.Subject != "mk-1" {
			t.Errorf("第 %d 条状态事件：Subject=%s，期望 mk-1", i, status.Subject)
		}
		if status.ChannelKind != "messages" {
			t.Errorf("第 %d 条状态事件：ChannelKind=%s，期望 messages", i, status.ChannelKind)
		}
		if status.Cause != ex.reason {
			t.Errorf("第 %d 条状态事件：Cause=%s，期望 %s", i, status.Cause, ex.reason)
		}
		p := status.Payload
		if p["channelUID"] != "mk-1" {
			t.Errorf("第 %d 条状态事件：payload.channelUID=%v，期望 mk-1", i, p["channelUID"])
		}
		if p["channelName"] != "sk-***ab" {
			t.Errorf("第 %d 条状态事件：payload.channelName=%v，期望 sk-***ab", i, p["channelName"])
		}
		if p["kind"] != "messages" {
			t.Errorf("第 %d 条状态事件：payload.kind=%v，期望 messages", i, p["kind"])
		}
		if p["oldStatus"] != ex.from || p["newStatus"] != ex.to {
			t.Errorf("第 %d 条状态事件：payload.old/new=%v/%v，期望 %s/%s", i, p["oldStatus"], p["newStatus"], ex.from, ex.to)
		}
		if p["reason"] != ex.reason {
			t.Errorf("第 %d 条状态事件：payload.reason=%v，期望 %s", i, p["reason"], ex.reason)
		}
		if _, ok := p["timestamp"]; !ok {
			t.Errorf("第 %d 条状态事件：payload 缺少 timestamp", i)
		}
	}
}

// TestCircuitState_SameState_NoEvent 验证：from == to 时不发布事件。
func TestCircuitState_SameState_NoEvent(t *testing.T) {
	bus := eventbus.NewBus()
	ch, unsub := bus.Subscribe(eventbus.TypeCircuitBreakerStateChanged)
	defer unsub()
	statusCh, unsubStatus := bus.Subscribe(eventbus.TypeChannelStatusChanged)
	defer unsubStatus()

	mgr := NewMetricsManager()
	mgr.apiType = "messages"
	mgr.SetEventBus(bus)

	km := &KeyMetrics{
		MetricsKey:   "mk-1",
		BaseURL:      "https://example.com",
		KeyMask:      "sk-***ab",
		CircuitState: CircuitStateClosed,
	}
	mgr.keyMetrics[km.MetricsKey] = km

	// 已经 Closed，resetCircuitStateLocked 的 from=closed 写入后状态仍为 closed
	// 走到这里 0 条事件
	mgr.mu.Lock()
	// 实际是 reset 写 closed from closed -> publishCircuitEventLocked 会判断 from==to 并直接 return
	// 走到这里 0 条事件
	mgr.resetCircuitStateLocked(km, true)
	mgr.mu.Unlock()

	// 等 50ms 确保无事件
	got := drainTimeout(ch, 1, 50*time.Millisecond)
	if len(got) != 0 {
		t.Errorf("from==to 时不应发布熔断事件，实际收到 %d 条", len(got))
	}
	gotStatus := drainTimeout(statusCh, 1, 50*time.Millisecond)
	if len(gotStatus) != 0 {
		t.Errorf("from==to 时不应发布状态事件，实际收到 %d 条", len(gotStatus))
	}
}

// TestCircuitState_NilBus_NoPanic 验证：bus 未注入时不 panic。
func TestCircuitState_NilBus_NoPanic(t *testing.T) {
	mgr := NewMetricsManager()
	mgr.apiType = "messages"
	// 不调用 SetEventBus，bus 字段为零值 atomic.Pointer[Bus] = nil

	km := &KeyMetrics{
		MetricsKey:   "mk-1",
		BaseURL:      "https://example.com",
		KeyMask:      "sk-***ab",
		CircuitState: CircuitStateClosed,
	}
	mgr.keyMetrics[km.MetricsKey] = km

	// 不应 panic
	mgr.mu.Lock()
	mgr.moveCircuitToOpenLocked(km, time.Now(), false, "breaker_threshold")
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
