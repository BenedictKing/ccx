package eventbus

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBus_SubscribeAll_ReceivesEvents(t *testing.T) {
	bus := NewBus()
	ch, unsub := bus.Subscribe()
	defer unsub()

	bus.Publish(Event{Type: TypeKeyBlacklisted, Subject: "k1"})
	bus.Publish(Event{Type: TypeConfigReloaded, Subject: "cfg"})

	got := drainTimeout(ch, 2, 100*time.Millisecond)
	if len(got) != 2 {
		t.Fatalf("期望 2 条事件，实际 %d", len(got))
	}
}

func TestBus_SubscribeTyped_FiltersEvents(t *testing.T) {
	bus := NewBus()
	ch, unsub := bus.Subscribe(TypeKeyBlacklisted, TypeKeyRestored)
	defer unsub()

	bus.Publish(Event{Type: TypeKeyBlacklisted, Subject: "k1"})
	bus.Publish(Event{Type: TypeConfigReloaded, Subject: "cfg"}) // 应被过滤
	bus.Publish(Event{Type: TypeKeyRestored, Subject: "k1"})

	got := drainTimeout(ch, 2, 100*time.Millisecond)
	if len(got) != 2 {
		t.Fatalf("期望 2 条事件，实际 %d", len(got))
	}
	for _, ev := range got {
		if ev.Type != TypeKeyBlacklisted && ev.Type != TypeKeyRestored {
			t.Errorf("收到未订阅类型: %s", ev.Type)
		}
	}
}

func TestBus_NilBusPublish_NoPanic(t *testing.T) {
	var b *Bus
	b.Publish(Event{Type: TypeConfigReloaded}) // 不应 panic
}

func TestBus_SlowSubscriber_DoesNotBlockPublish(t *testing.T) {
	bus := NewBus()
	ch, unsub := bus.Subscribe() // buffer=64
	defer unsub()

	// 填满订阅者缓冲区并确保不消费
	for i := 0; i < 1000; i++ {
		bus.Publish(Event{Type: TypeKeyBlacklisted, Subject: "k"})
	}
	// 订阅者应至少收到 bufferSize 条，不应被全部接收
	received := 0
drain:
	for {
		select {
		case <-ch:
			received++
		default:
			break drain
		}
	}
	if received > busBufferSize {
		t.Errorf("慢订阅者接收过多: %d > buffer %d", received, busBufferSize)
	}
}

func TestBus_Unsubscribe_StopsDelivery(t *testing.T) {
	bus := NewBus()
	ch, unsub := bus.Subscribe()
	bus.Publish(Event{Type: TypeKeyBlacklisted})
	<-ch
	unsub()

	// 取消后再发，channel 已关闭，select 应立即走 default
	bus.Publish(Event{Type: TypeKeyBlacklisted})
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("unsubscribe 后不应收到事件")
		}
	case <-time.After(50 * time.Millisecond):
	}
	if got := bus.SubscriberCount(); got != 0 {
		t.Errorf("unsubscribe 后 SubscriberCount=%d，期望 0", got)
	}
}

func TestBus_Unsubscribe_Idempotent(t *testing.T) {
	bus := NewBus()
	_, unsub := bus.Subscribe()
	unsub()
	unsub() // 二次调用不应 panic
}

func TestBus_ConcurrentSubscribeAndPublish(t *testing.T) {
	bus := NewBus()
	var received atomic.Int64
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ch, unsub := bus.Subscribe()
			defer unsub()
			for j := 0; j < 200; j++ {
				select {
				case <-ch:
					received.Add(1)
				case <-time.After(2 * time.Second):
					return
				}
			}
		}()
	}
	for i := 0; i < 2000; i++ {
		bus.Publish(Event{Type: TypeKeyBlacklisted, Subject: "k"})
	}
	wg.Wait()
	// 至少被多个订阅者接收，具体数受丢弃策略影响，只验证非零
	if received.Load() == 0 {
		t.Error("并发场景下订阅者应至少收到部分事件")
	}
}

func TestEvent_EnsureUID_Stable(t *testing.T) {
	ts := time.Unix(1700000000, 0).UTC()
	e1 := Event{Type: TypeKeyBlacklisted, Subject: "k1", CreatedAt: ts}
	e1.EnsureUID()
	e2 := Event{Type: TypeKeyBlacklisted, Subject: "k1", CreatedAt: ts}
	e2.EnsureUID()
	if e1.UID != e2.UID {
		t.Errorf("同 type/subject/time 应生成相同 UID: %s vs %s", e1.UID, e2.UID)
	}
	if e1.UID == "" {
		t.Error("UID 不应为空")
	}
}

// drainTimeout 在 timeout 内最多取 n 条事件。
func drainTimeout(ch <-chan Event, n int, timeout time.Duration) []Event {
	out := make([]Event, 0, n)
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	for len(out) < n {
		select {
		case ev, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, ev)
		case <-deadline.C:
			return out
		}
	}
	return out
}
