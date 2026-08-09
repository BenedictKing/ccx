/**
 * useEventStream — 跨模块状态事件的统一 WebSocket 订阅 + mitt 分发（Phase B.3）
 *
 * 后端 `GET /api/health-center/state-events/stream` 推送 eventbus.Event
 * （熔断迁移 / Key 拉黑恢复 / 配置与逻辑渠道重建 / preset 置换等）。
 * 本 composable 维护进程级单例 WS 连接，按事件 Type 分发到 mitt 总线；
 * 组件通过 on(type, fn) 订阅，unmount 自动退订；最后一个订阅者退订后关闭连接。
 *
 * 事件是「通知」而非真相源：订阅丢事件不影响正确性，组件仍应保留
 * useGlobalTick 轮询作为兜底（降频而非移除）。
 */

import { onUnmounted } from 'vue'
import mitt from 'mitt'
import { useAuthStore } from '@/stores/auth'
import { API_BASE } from '@/services/api-helpers'
import type { StateEvent, StateEventType } from '@/services/api-types'

// ─── mitt 总线 ───

type EventStreamEvents = {
  /** 任意状态事件（通配） */
  event: StateEvent
} & {
  [K in StateEventType]: StateEvent
}

const emitter = mitt<EventStreamEvents>()

// ─── 单例 WS 连接（引用计数） ───

type ConnectionStatus = 'idle' | 'connecting' | 'open' | 'closed'

let socket: WebSocket | null = null
let reconnectTimer: ReturnType<typeof setTimeout> | null = null
let backoffMs = 1000
const maxBackoffMs = 30000
let refCount = 0
let status: ConnectionStatus = 'idle'
const statusListeners = new Set<(s: ConnectionStatus) => void>()

function buildWsUrl(path: string): string {
  if (/^https?:\/\//i.test(API_BASE)) {
    return API_BASE.replace(/^http/i, 'ws') + path
  }
  const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:'
  return `${protocol}//${window.location.host}${API_BASE}${path}`
}

function setStatus(s: ConnectionStatus): void {
  status = s
  for (const fn of statusListeners) {
    try {
      fn(s)
    } catch (err) {
      console.warn('[useEventStream] status listener error:', err)
    }
  }
}

function connect(): void {
  if (refCount <= 0) return
  const authStore = useAuthStore()
  const apiKey = authStore.apiKey as unknown as string | null
  const url = buildWsUrl('/health-center/state-events/stream')

  setStatus('connecting')
  socket = apiKey ? new WebSocket(url, [apiKey]) : new WebSocket(url)

  socket.onopen = () => {
    backoffMs = 1000
    setStatus('open')
  }

  socket.onmessage = (event: MessageEvent<string>) => {
    let parsed: StateEvent
    try {
      parsed = JSON.parse(event.data) as StateEvent
    } catch {
      return // 忽略无法解析的消息
    }
    // 先按具体类型分发，再发通配
    emitter.emit(parsed.type, parsed)
    emitter.emit('event', parsed)
  }

  socket.onclose = () => {
    setStatus('closed')
    socket = null
    if (refCount <= 0) return
    reconnectTimer = setTimeout(connect, backoffMs)
    backoffMs = Math.min(backoffMs * 2, maxBackoffMs)
  }

  socket.onerror = () => {
    // onclose 会随后触发，重连统一在那里处理
  }
}

function disconnect(): void {
  if (reconnectTimer) {
    clearTimeout(reconnectTimer)
    reconnectTimer = null
  }
  if (socket) {
    socket.onopen = null
    socket.onmessage = null
    socket.onclose = null
    socket.onerror = null
    socket.close()
    socket = null
  }
  backoffMs = 1000
  setStatus('idle')
}

function acquire(): void {
  refCount++
  if (refCount === 1) {
    connect()
  }
}

function release(): void {
  refCount = Math.max(0, refCount - 1)
  if (refCount === 0) {
    disconnect()
  }
}

// ─── 公共 API ───

export interface EventStreamHandle {
  /** 订阅指定事件类型；unmount 自动退订。返回手动退订函数。 */
  on: (type: StateEventType | 'event', fn: (ev: StateEvent) => void) => () => void
  /** 当前连接状态 */
  getStatus: () => ConnectionStatus
  /** 订阅连接状态变化（用于调试徽章等）；unmount 自动退订 */
  onStatusChange: (fn: (s: ConnectionStatus) => void) => void
}

export function useEventStream(): EventStreamHandle {
  // 每个 useEventStream() 调用点计一次引用；unmount 释放。
  acquire()
  try {
    onUnmounted(() => {
      release()
    })
  } catch {
    // 非组件上下文（如 store 顶层）调用时 onUnmounted 告警；
    // 该场景引用常驻，进程生命周期内保持连接，可接受。
  }

  const on = (type: StateEventType | 'event', fn: (ev: StateEvent) => void): (() => void) => {
    emitter.on(type as keyof EventStreamEvents, fn as (ev: StateEvent) => void)
    const off = () => emitter.off(type as keyof EventStreamEvents, fn as (ev: StateEvent) => void)
    try {
      onUnmounted(off)
    } catch {
      // 非组件上下文：调用方需自行用返回的 off 退订
    }
    return off
  }

  const getStatus = (): ConnectionStatus => status

  const onStatusChange = (fn: (s: ConnectionStatus) => void): void => {
    statusListeners.add(fn)
    try {
      onUnmounted(() => statusListeners.delete(fn))
    } catch {
      // 非组件上下文：忽略
    }
  }

  return { on, getStatus, onStatusChange }
}

/**
 * 测试专用：重置模块级状态（关闭连接、清空订阅与引用计数）。
 * @internal
 */
export function __resetEventStreamForTests__(): void {
  refCount = 0
  disconnect()
  emitter.all.clear()
  statusListeners.clear()
}
