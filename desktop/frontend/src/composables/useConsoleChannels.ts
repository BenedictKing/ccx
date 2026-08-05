import { ref, computed } from 'vue'
import { useAdminApi } from '@/composables/useAdminApi'
import { useChannelPlacementPreference } from '@/composables/useChannelPlacementPreference'
import { mergeChannelsWithLocalData } from '@/utils/channel-merge'
import { getChannelTypeApi, type ManagedChannelType } from '@/utils/channel-type-api'
import type {
  Channel,
  ChannelsResponse,
  ChannelMetrics,
  ChannelDashboardResponse,
  ChannelRecentActivity,
  SchedulerStatsResponse,
  PingResult,
  DeleteLogicalChannelResponse,
} from '@/services/admin-api'

/**
 * 管理控制台频道状态层
 *
 * 使用 composable 单例模式（模块级 state），与 Desktop 现有架构一致。
 * 替代根 frontend 的 Pinia channel store，通过 HTTP 直接调后端 /api/*。
 */

type ChannelType = ManagedChannelType
type DashboardCache = {
  metrics: ChannelMetrics[]
  stats: SchedulerStatsResponse | undefined
  recentActivity: ChannelRecentActivity[] | undefined
}

const EMPTY_CACHE: DashboardCache = { metrics: [], stats: undefined, recentActivity: undefined }

// ===== 模块级单例状态 =====

const activeTab = ref<ChannelType>('messages')

// 六种协议的频道数据（独立缓存，切换不闪烁）
const channelsByType = ref<Record<ChannelType, ChannelsResponse>>({
  messages: { channels: [], current: -1 },
  chat: { channels: [], current: -1 },
  responses: { channels: [], current: -1 },
  gemini: { channels: [], current: -1 },
  images: { channels: [], current: -1 },
  vectors: { channels: [], current: -1 },
})

// 六种协议的 dashboard 缓存
const dashboardCache = ref<Record<ChannelType, DashboardCache>>({
  messages: { ...EMPTY_CACHE },
  chat: { ...EMPTY_CACHE },
  responses: { ...EMPTY_CACHE },
  gemini: { ...EMPTY_CACHE },
  images: { ...EMPTY_CACHE },
  vectors: { ...EMPTY_CACHE },
})

const isPingingAll = ref(false)
const refreshError = ref('')
let refreshLoopPromise: Promise<void> | null = null
let refreshRequested = false

function translate(key: string, fallback: string): string {
  const i18n = (globalThis as any).__CCX_I18N__
  const translated = i18n?.global?.t?.(key)
  return translated && translated !== key ? translated : fallback
}

// ===== 计算属性 =====

const currentChannelsData = computed(() => channelsByType.value[activeTab.value])
const currentDashboardMetrics = computed(() => dashboardCache.value[activeTab.value].metrics)
const currentDashboardStats = computed(() => dashboardCache.value[activeTab.value].stats)
const currentDashboardRecentActivity = computed(() => dashboardCache.value[activeTab.value].recentActivity)

const activeChannelCount = computed(() => {
  const chs = currentChannelsData.value.channels
  return chs.filter(ch => ch.status === 'active' || ch.status === undefined || ch.status === '').length
})

const failoverChannelCount = computed(() => {
  const chs = currentChannelsData.value.channels
  return chs.filter(ch => ch.status !== 'disabled').length
})

// ===== 刷新逻辑 =====

async function doRefresh(tab: ChannelType) {
  const api = useAdminApi()
  try {
    // 统一 dashboard 接口：GET /api/logical-channels/dashboard?kind=llm
    // 后端已按 logical channel 聚合，一个站点多协议只返回一张卡片
    const dashboard = await api.get<ChannelDashboardResponse>(
      `/api/logical-channels/dashboard?kind=llm`
    )
    const existing = channelsByType.value[tab].channels
    channelsByType.value[tab] = {
      channels: mergeChannelsWithLocalData(dashboard.channels, existing),
      current: typeof dashboard.current === 'number' ? dashboard.current : channelsByType.value[tab].current,
    }
    dashboardCache.value[tab] = {
      metrics: dashboard.metrics,
      stats: dashboard.stats,
      recentActivity: dashboard.recentActivity,
    }
    refreshError.value = ''
  } catch (e) {
    const msg = e instanceof Error ? e.message : String(e)
    // 网络层 TypeError 包装为友好提示
    refreshError.value = msg.includes('Failed to fetch')
      ? translate('adminApi.error.networkUnavailable', '服务未运行或网络不可达，请检查后端是否已启动')
      : msg
  }
}

async function refreshChannels(tab: ChannelType = activeTab.value) {
  activeTab.value = tab
  refreshRequested = true
  if (refreshLoopPromise) return refreshLoopPromise

  refreshLoopPromise = (async () => {
    try {
      while (refreshRequested) {
        refreshRequested = false
        await doRefresh(activeTab.value)
      }
    } finally {
      refreshLoopPromise = null
    }
  })()

  return refreshLoopPromise
}

// ===== 频道 CRUD 操作 =====

async function saveChannel(
  payload: Omit<Channel, 'index' | 'latency' | 'status'>,
  editingIndex: number | null,
  options?: { isQuickAdd?: boolean },
  channelType: ChannelType = activeTab.value,
) {
  activeTab.value = channelType
  const api = useAdminApi()

  // 若编辑的是逻辑渠道（有 logicalChannelUid），通过 logical-channels API 同步更新所有协议
  if (editingIndex !== null) {
    const existing = channelsByType.value[channelType].channels.find(ch => ch.index === editingIndex)
    if (existing?.logicalChannelUid) {
      await api.put(`/api/logical-channels/${encodeURIComponent(existing.logicalChannelUid)}`, {
        common: {
          name: payload.name,
          description: payload.description,
          website: payload.website,
          baseUrls: payload.baseUrls ?? (payload.baseUrl ? [payload.baseUrl] : undefined),
          tags: payload.tags,
        },
      })
      await refreshChannels(channelType)
      return { success: true, messageKey: 'channelEditor.toast.updated' }
    }
  }

  const typeApi = getChannelTypeApi(channelType)
  if (editingIndex !== null) {
    await typeApi.updateChannel(editingIndex, payload)
    await refreshChannels(channelType)
    return { success: true, messageKey: 'channelEditor.toast.updated' }
  }
  await typeApi.addChannel(payload)
  await refreshChannels(channelType)

  // 快速添加模式：根据用户偏好将新渠道放到队列顶部（含 5 分钟促销期）或末尾
  if (options?.isQuickAdd) {
    const { newChannelPlacement } = useChannelPlacementPreference()
    const allChannels = channelsByType.value[channelType].channels || []
    // 后端 AddUpstream 把新渠道 prepend 到首位；通过 name 精确匹配定位
    const newChannel = allChannels.find(ch => ch.name === payload.name && ch.status !== 'disabled')
    if (newChannel) {
      try {
        const placeAtBottom = newChannelPlacement.value === 'bottom'
        const otherIndexes = allChannels
          .filter(ch => ch.index !== newChannel.index && ch.status !== 'disabled')
          .sort((a, b) => (a.priority ?? a.index) - (b.priority ?? b.index))
          .map(ch => ch.index)
        const newOrder = placeAtBottom
          ? [...otherIndexes, newChannel.index]
          : [newChannel.index, ...otherIndexes]
        await typeApi.reorder(newOrder)
        if (!placeAtBottom) {
          await typeApi.promote(newChannel.index, 300)
        }
        await refreshChannels(channelType)
      } catch (err) {
        console.warn('设置快速添加优先级失败:', err)
        // 不影响主流程
      }
    }
  }

  return { success: true, messageKey: 'channelEditor.toast.added' }
}

async function deleteChannel(channelId: number, channelType: ChannelType = activeTab.value, logicalChannelUid?: string) {
  activeTab.value = channelType
  const api = useAdminApi()
  if (logicalChannelUid) {
    await api.del<DeleteLogicalChannelResponse>(`/api/logical-channels/${encodeURIComponent(logicalChannelUid)}`)
  } else {
    const typeApi = getChannelTypeApi(channelType)
    await typeApi.deleteChannel(channelId)
  }
  await refreshChannels(channelType)
}

async function pingChannel(channelId: number) {
  const typeApi = getChannelTypeApi(activeTab.value)
  const result = await typeApi.pingChannel(channelId) as PingResult
  const channel = channelsByType.value[activeTab.value].channels.find(c => c.index === channelId)
  if (channel) {
    channel.latency = result.latency
    channel.latencyTestTime = Date.now()
  }
  return result
}

async function pingAllChannels() {
  if (isPingingAll.value) return
  isPingingAll.value = true
  try {
    const typeApi = getChannelTypeApi(activeTab.value)
    const results = await typeApi.pingAll() as PingResult[]
    const now = Date.now()
    const channels = channelsByType.value[activeTab.value].channels
    for (const result of results) {
      const ch = channels.find(c => c.index === (result as any).id)
      if (ch) {
        ch.latency = result.latency
        ch.latencyTestTime = now
      }
    }
  } finally {
    isPingingAll.value = false
  }
}

async function reorderChannels(order: number[], channelType: ChannelType = activeTab.value) {
  activeTab.value = channelType
  const typeApi = getChannelTypeApi(channelType)
  await typeApi.reorder(order)
  await refreshChannels(channelType)
}

async function setChannelStatus(channelId: number, status: 'active' | 'suspended' | 'disabled', channelType: ChannelType = activeTab.value) {
  activeTab.value = channelType
  const typeApi = getChannelTypeApi(channelType)
  await typeApi.setStatus(channelId, status)
  await refreshChannels(channelType)
}

async function resumeChannel(channelId: number, channelType: ChannelType = activeTab.value) {
  activeTab.value = channelType
  const typeApi = getChannelTypeApi(channelType)
  const result = await typeApi.resume(channelId)
  await typeApi.setStatus(channelId, 'active')
  await refreshChannels(channelType)
  return result
}

async function promoteChannel(channelId: number, durationSeconds: number, channelType: ChannelType = activeTab.value) {
  activeTab.value = channelType
  const typeApi = getChannelTypeApi(channelType)
  await typeApi.promote(channelId, durationSeconds)
  await refreshChannels(channelType)
}

// ===== Key 管理 =====

async function addApiKey(channelId: number, key: string) {
  const typeApi = getChannelTypeApi(activeTab.value)
  await typeApi.addApiKey(channelId, key)
  await refreshChannels()
}

async function removeApiKey(channelId: number, key: string) {
  const typeApi = getChannelTypeApi(activeTab.value)
  await typeApi.removeApiKey(channelId, key)
  await refreshChannels()
}

async function restoreApiKey(channelId: number, key: string, channelType: ChannelType = activeTab.value) {
  activeTab.value = channelType
  const typeApi = getChannelTypeApi(channelType)
  await typeApi.restoreApiKey(channelId, key)
  await refreshChannels(channelType)
}

async function moveApiKeyToTop(channelId: number, key: string) {
  const typeApi = getChannelTypeApi(activeTab.value)
  await typeApi.moveApiKeyToTop(channelId, key)
  await refreshChannels()
}

async function moveApiKeyToBottom(channelId: number, key: string) {
  const typeApi = getChannelTypeApi(activeTab.value)
  await typeApi.moveApiKeyToBottom(channelId, key)
  await refreshChannels()
}

export function useConsoleChannels() {
  return {
    // 状态
    activeTab,
    channelsByType,
    dashboardCache,
    isPingingAll,
    refreshError,

    // 当前 tab 的计算属性
    currentChannelsData,
    currentDashboardMetrics,
    currentDashboardStats,
    currentDashboardRecentActivity,
    activeChannelCount,
    failoverChannelCount,

    // 操作
    refreshChannels,
    saveChannel,
    deleteChannel,
    pingChannel,
    pingAllChannels,
    reorderChannels,
    setChannelStatus,
    resumeChannel,
    promoteChannel,

    // Key 管理
    addApiKey,
    removeApiKey,
    restoreApiKey,
    moveApiKeyToTop,
    moveApiKeyToBottom,

  }
}
