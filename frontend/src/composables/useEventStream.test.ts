// @vitest-environment jsdom
/* eslint-disable vue/one-component-per-file */
import { describe, expect, it, vi, beforeEach, afterEach } from 'vitest'
import { defineComponent, h, onMounted } from 'vue'
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { useEventStream, __resetEventStreamForTests__ } from './useEventStream'
import type { StateEvent } from '@/services/api-types'

// ─── WebSocket Mock ───

type WSHandler = ((ev?: unknown) => void) | null

class MockWebSocket {
  static instances: MockWebSocket[] = []
  static CONNECTING = 0
  static OPEN = 1
  static CLOSING = 2
  static CLOSED = 3

  url: string
  protocols?: string | string[]
  onopen: WSHandler = null
  onmessage: WSHandler = null
  onclose: WSHandler = null
  onerror: WSHandler = null
  readyState = MockWebSocket.CONNECTING
  closed = false

  constructor(url: string, protocols?: string | string[]) {
    this.url = url
    this.protocols = protocols
    MockWebSocket.instances.push(this)
  }

  close(): void {
    this.closed = true
  }

  // 测试辅助：模拟服务器行为
  simulateOpen(): void {
    this.readyState = MockWebSocket.OPEN
    this.onopen?.()
  }
  simulateMessage(data: unknown): void {
    this.onmessage?.({ data: JSON.stringify(data) })
  }
  simulateClose(): void {
    this.readyState = MockWebSocket.CLOSED
    this.onclose?.()
  }
}

function makeEvent(overrides: Partial<StateEvent> = {}): StateEvent {
  return {
    uid: 'ev_test',
    type: 'circuit_breaker_state_changed',
    scope: 'metrics',
    subject: 'ch-1',
    createdAt: '2026-08-09T00:00:00Z',
    ...overrides,
  }
}

describe('useEventStream', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    setActivePinia(createPinia())
    MockWebSocket.instances = []
    vi.stubGlobal('WebSocket', MockWebSocket)
  })

  afterEach(() => {
    __resetEventStreamForTests__()
    vi.unstubAllGlobals()
    vi.restoreAllMocks()
    vi.useRealTimers()
  })

  function mountConsumer(onEvent: (ev: StateEvent) => void, type: Parameters<ReturnType<typeof useEventStream>['on']>[0] = 'event') {
    return mount(
      defineComponent({
        setup() {
          const stream = useEventStream()
          onMounted(() => {
            stream.on(type, onEvent)
          })
          return () => h('div')
        },
      })
    )
  }

  it('首个订阅者建立 WS 连接，连接成功状态变为 open', () => {
    mountConsumer(() => {})
    expect(MockWebSocket.instances).toHaveLength(1)
    const ws = MockWebSocket.instances[0]
    expect(ws.url).toContain('/health-center/state-events/stream')
    ws.simulateOpen()
    expect(ws.readyState).toBe(MockWebSocket.OPEN)
  })

  it('按事件类型分发到对应订阅者，通配订阅者也能收到', () => {
    const wildcard: StateEvent[] = []
    const typed: StateEvent[] = []
    mount(
      defineComponent({
        setup() {
          const stream = useEventStream()
          onMounted(() => {
            stream.on('event', ev => wildcard.push(ev))
            stream.on('circuit_breaker_state_changed', ev => typed.push(ev))
          })
          return () => h('div')
        },
      })
    )
    const ws = MockWebSocket.instances[0]
    ws.simulateOpen()

    const ev = makeEvent()
    ws.simulateMessage(ev)
    expect(wildcard).toHaveLength(1)
    expect(typed).toHaveLength(1)
    expect(typed[0].uid).toBe('ev_test')
  })

  it('不匹配类型的订阅者收不到事件', () => {
    const keyEvents: StateEvent[] = []
    mount(
      defineComponent({
        setup() {
          const stream = useEventStream()
          onMounted(() => {
            stream.on('key_blacklisted', ev => keyEvents.push(ev))
          })
          return () => h('div')
        },
      })
    )
    const ws = MockWebSocket.instances[0]
    ws.simulateOpen()
    ws.simulateMessage(makeEvent({ type: 'circuit_breaker_state_changed' }))
    expect(keyEvents).toHaveLength(0)
    ws.simulateMessage(makeEvent({ type: 'key_blacklisted' }))
    expect(keyEvents).toHaveLength(1)
  })

  it('无法解析的消息被忽略，不影响后续事件', () => {
    const received: StateEvent[] = []
    mountConsumer(ev => received.push(ev))
    const ws = MockWebSocket.instances[0]
    ws.simulateOpen()
    ws.onmessage?.({ data: 'not-json{{' })
    ws.simulateMessage(makeEvent())
    expect(received).toHaveLength(1)
  })

  it('断线后按指数退避自动重连', () => {
    mountConsumer(() => {})
    expect(MockWebSocket.instances).toHaveLength(1)
    MockWebSocket.instances[0].simulateOpen()
    MockWebSocket.instances[0].simulateClose()

    // 第一次重连：1s 后
    vi.advanceTimersByTime(1000)
    expect(MockWebSocket.instances).toHaveLength(2)
    MockWebSocket.instances[1].simulateClose()

    // 第二次重连：2s 后（退避翻倍）
    vi.advanceTimersByTime(1000)
    expect(MockWebSocket.instances).toHaveLength(2)
    vi.advanceTimersByTime(1000)
    expect(MockWebSocket.instances).toHaveLength(3)
  })

  it('最后一个订阅者卸载后关闭连接且不再重连', () => {
    const wrapper = mountConsumer(() => {})
    const ws = MockWebSocket.instances[0]
    ws.simulateOpen()
    wrapper.unmount()
    expect(ws.closed).toBe(true)

    // 卸载后即使触发 close 也不应重连
    ws.simulateClose()
    vi.advanceTimersByTime(5000)
    expect(MockWebSocket.instances).toHaveLength(1)
  })
})
