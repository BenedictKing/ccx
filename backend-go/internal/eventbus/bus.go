package eventbus

import "sync"

// busBufferSize 每个订阅者的缓冲区容量。事件是低频状态信号，
// 缓冲区只需吸收短暂抖动；满即丢弃，绝不阻塞发布方。
const busBufferSize = 64

// subscription 内部订阅记录：投递 channel + 类型过滤集合（nil = 全订阅）。
type subscription struct {
	ch    chan Event
	types map[string]struct{}
}

// Bus 是进程内非阻塞事件总线。发布 O(订阅者数)，非阻塞；
// 慢订阅者缓冲区满时丢弃该条，不影响其他订阅者与发布方。
type Bus struct {
	mu   sync.RWMutex
	subs map[*subscription]struct{}
}

// NewBus 创建一个空总线。
func NewBus() *Bus {
	return &Bus{subs: make(map[*subscription]struct{})}
}

// Subscribe 注册订阅者。types 为空表示订阅全部类型，否则只接收列出的类型。
// 返回接收 channel 与幂等的取消函数；取消后 channel 被关闭。
func (b *Bus) Subscribe(types ...string) (<-chan Event, func()) {
	sub := &subscription{ch: make(chan Event, busBufferSize)}
	if len(types) > 0 {
		sub.types = make(map[string]struct{}, len(types))
		for _, t := range types {
			if t != "" {
				sub.types[t] = struct{}{}
			}
		}
	}

	b.mu.Lock()
	b.subs[sub] = struct{}{}
	b.mu.Unlock()

	var once sync.Once
	unsubscribe := func() {
		once.Do(func() {
			b.mu.Lock()
			if _, ok := b.subs[sub]; ok {
				delete(b.subs, sub)
				close(sub.ch)
			}
			b.mu.Unlock()
		})
	}
	return sub.ch, unsubscribe
}

// Publish 向匹配类型的订阅者广播事件。非阻塞：订阅者缓冲区满则丢弃该条。
// b 为 nil 时是安全的空操作（未注入总线的场景）。
func (b *Bus) Publish(ev Event) {
	if b == nil {
		return
	}
	ev.EnsureUID()

	b.mu.RLock()
	defer b.mu.RUnlock()
	for sub := range b.subs {
		if sub.types != nil {
			if _, ok := sub.types[ev.Type]; !ok {
				continue
			}
		}
		select {
		case sub.ch <- ev:
		default:
			// 订阅者消费过慢，丢弃本条，不阻塞发布方。
		}
	}
}

// SubscriberCount 返回当前订阅者数量，主要用于测试与诊断。
func (b *Bus) SubscriberCount() int {
	if b == nil {
		return 0
	}
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subs)
}
