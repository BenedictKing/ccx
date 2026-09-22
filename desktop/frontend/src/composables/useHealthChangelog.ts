import { useAdminApi } from '@/composables/useAdminApi'
import { useStatus } from '@/composables/useStatus'
import { GetAdminAccessKey } from '@bindings/github.com/BenedictKing/ccx/desktop/desktopservice'
import {
  HEALTH_CENTER_CHANGELOG_PATH,
  HEALTH_CENTER_EVENTS_WS_PATH,
  HEALTH_CENTER_STATE_EVENTS_WS_PATH,
} from '@/services/admin-api'
import type { ProfileChangeEvent, ProfileChangelogResponse, StateEvent } from '@/services/admin-api'

/**
 * 渠道健康画像变更事件的历史拉取 + WebSocket 实时推送。
 * 复用后端 /api/health-center/changelog（REST）、/api/health-center/events（WS，画像变更）
 * 与 /api/health-center/state-events/stream（WS，跨模块状态事件，含 drift）。
 */

export type ProfileEventsConnectionStatus = 'connecting' | 'open' | 'closed'

export interface ConnectProfileEventsOptions {
  onEvent: (event: ProfileChangeEvent) => void
  onStatusChange?: (status: ProfileEventsConnectionStatus) => void
}

/** 拉取画像变更历史（REST） */
export async function fetchHealthChangelog(params?: {
  channelUid?: string
  limit?: number
}): Promise<ProfileChangelogResponse> {
  const api = useAdminApi()
  const query = new URLSearchParams()
  if (params?.channelUid) query.set('channelUid', params.channelUid)
  if (params?.limit) query.set('limit', String(params.limit))
  const qs = query.toString()
  return api.get<ProfileChangelogResponse>(`${HEALTH_CENTER_CHANGELOG_PATH}${qs ? `?${qs}` : ''}`)
}

function buildWsUrl(baseUrl: string, path: string): string {
  return baseUrl.replace(/^http/i, 'ws') + path
}

interface EventsSocketOptions<T> {
  path: string
  onMessage: (event: T) => void
  onStatusChange?: (status: ProfileEventsConnectionStatus) => void
}

/**
 * 健康中心事件 WebSocket 公共管线：key 走 Sec-WebSocket-Protocol 子协议鉴权，
 * 断线自动重连（指数退避，1s 起步，封顶 30s）；返回的 close() 用于组件卸载时清理，
 * 调用后不再重连。若网关未运行则等待其启动后再建立连接。
 */
function openEventsSocket<T>(options: EventsSocketOptions<T>): () => void {
  const { status } = useStatus()
  let closedByCaller = false
  let socket: WebSocket | null = null
  let reconnectTimer: ReturnType<typeof setTimeout> | null = null
  let backoffMs = 1000
  const maxBackoffMs = 30000

  const notifyStatus = (s: ProfileEventsConnectionStatus) => {
    options.onStatusChange?.(s)
  }

  const connect = async () => {
    if (closedByCaller) return
    if (!status.value.running || !status.value.url) {
      notifyStatus('closed')
      reconnectTimer = setTimeout(connect, backoffMs)
      backoffMs = Math.min(backoffMs * 2, maxBackoffMs)
      return
    }

    const adminKey = await GetAdminAccessKey()
    const url = buildWsUrl(status.value.url, options.path)

    notifyStatus('connecting')
    socket = adminKey ? new WebSocket(url, [adminKey]) : new WebSocket(url)

    socket.onopen = () => {
      backoffMs = 1000
      notifyStatus('open')
    }

    socket.onmessage = (event: MessageEvent<string>) => {
      try {
        options.onMessage(JSON.parse(event.data) as T)
      } catch {
        // 忽略无法解析的消息
      }
    }

    socket.onclose = () => {
      notifyStatus('closed')
      if (closedByCaller) return
      reconnectTimer = setTimeout(connect, backoffMs)
      backoffMs = Math.min(backoffMs * 2, maxBackoffMs)
    }

    socket.onerror = () => {
      // onclose 会随后触发，重连逻辑统一在那里处理
    }
  }

  connect()

  return () => {
    closedByCaller = true
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
  }
}

/**
 * 建立画像变更事件 WebSocket 连接（实时推送，ProfileChangeEvent）。
 */
export function connectHealthChangelogEvents(options: ConnectProfileEventsOptions): () => void {
  return openEventsSocket<ProfileChangeEvent>({
    path: HEALTH_CENTER_EVENTS_WS_PATH,
    onMessage: options.onEvent,
    onStatusChange: options.onStatusChange,
  })
}

export interface ConnectStateEventsOptions {
  onEvent: (event: StateEvent) => void
  onStatusChange?: (status: ProfileEventsConnectionStatus) => void
}

/**
 * 建立跨模块状态事件 WebSocket 连接（/api/health-center/state-events/stream，
 * 推送 eventbus.Event：circuit_breaker / key 黑名单 / manifest_drift / capability_drift 等）。
 */
export function connectHealthStateEvents(options: ConnectStateEventsOptions): () => void {
  return openEventsSocket<StateEvent>({
    path: HEALTH_CENTER_STATE_EVENTS_WS_PATH,
    onMessage: options.onEvent,
    onStatusChange: options.onStatusChange,
  })
}
