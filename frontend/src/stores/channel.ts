import { defineStore } from 'pinia'
import { ref, shallowReactive, computed, unref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { usePreferencesStore } from '@/stores/preferences'
import { api, type Channel, type ChannelPlacement, type ChannelsResponse, type ChannelMetrics, type ChannelDashboardResponse } from '@/services/api'
import { autoAddChannel, extractAutoAddErrorMessage } from '@/services/autopilot-api'
import { normalizeLocale } from '@/i18n/core'
import { translate } from '@/i18n'
import { registerGlobalTick } from '@/composables/useGlobalTick'
import { mergeChannelsWithLocalData } from '@/utils/channelMerge'
import { isStructurallyEqual, reuseUnchangedItemsByKey } from '@/utils/structuralSharing'
import {
  buildUnifiedRecentActivity,
  buildUnifiedChannelsData,
  isLlmChannelKind,
  LLM_CHANNEL_KINDS,
  withRouteKindMetrics,
  normalizeChannelStatus,
  type LlmChannelKind,
} from '@/utils/unifiedChannels'

/**
 * 渠道数据管理 Store
 *
 * 职责：
 * - 管理三种 API 类型的渠道数据（Messages/Responses/Gemini）
 * - 管理渠道指标和统计数据
 * - 提供渠道操作方法（添加、编辑、删除、测试延迟等）
 * - 管理自动刷新定时器
 */
export const useChannelStore = defineStore('channel', () => {
  const preferencesStore = usePreferencesStore()
  const t = (key: Parameters<typeof translate>[1], params?: Parameters<typeof translate>[2]) => {
    return translate(normalizeLocale(preferencesStore.uiLanguage as unknown as string), key, params)
  }
  // ===== 状态 =====

  // 当前选中的 API 类型
  type ApiTab = 'messages' | 'chat' | 'responses' | 'gemini' | 'images' | 'vectors'
  const activeTab = ref<ApiTab>('messages')

  // 路由同步：从路由读取当前类型
  const router = useRouter()
  const currentChannelType = computed(() => {
    const route = router.currentRoute.value
    const type = route.params.type as ApiTab
    return (type === 'messages' || type === 'chat' || type === 'responses' || type === 'gemini' || type === 'images' || type === 'vectors') ? type : 'messages'
  })

  // 监听路由变化，同步 activeTab（确保兼容性）
  watch(currentChannelType, (newType) => {
    activeTab.value = newType
  }, { immediate: true })

  // 三种 API 类型的渠道数据
  const channelsData = ref<ChannelsResponse>({
    channels: [],
    current: -1
  })

  const responsesChannelsData = ref<ChannelsResponse>({
    channels: [],
    current: -1
  })

  const geminiChannelsData = ref<ChannelsResponse>({
    channels: [],
    current: -1
  })

  const chatChannelsData = ref<ChannelsResponse>({
    channels: [],
    current: -1
  })

  const imagesChannelsData = ref<ChannelsResponse>({
    channels: [],
    current: -1
  })

  const vectorsChannelsData = ref<ChannelsResponse>({
    channels: [],
    current: -1
  })

  /** 按照 kind 返回对应的 channels ref */
  function getChannelsForType(kind: ApiTab) {
    switch (kind) {
      case 'chat': return chatChannelsData
      case 'responses': return responsesChannelsData
      case 'gemini': return geminiChannelsData
      case 'images': return imagesChannelsData
      case 'vectors': return vectorsChannelsData
      default: return channelsData
    }
  }

  // Dashboard 数据缓存结构（每个 tab 独立缓存）
  interface DashboardCache {
    metrics: ChannelMetrics[]
    stats: ChannelDashboardResponse['stats'] | undefined
    recentActivity: ChannelDashboardResponse['recentActivity'] | undefined
  }

  const createEmptyDashboardCache = (): Record<ApiTab, DashboardCache> => ({
    messages: {
      metrics: [],
      stats: undefined,
      recentActivity: undefined
    },
    chat: {
      metrics: [],
      stats: undefined,
      recentActivity: undefined
    },
    responses: {
      metrics: [],
      stats: undefined,
      recentActivity: undefined
    },
    gemini: {
      metrics: [],
      stats: undefined,
      recentActivity: undefined
    },
    images: {
      metrics: [],
      stats: undefined,
      recentActivity: undefined
    },
    vectors: {
      metrics: [],
      stats: undefined,
      recentActivity: undefined
    }
  })

  // Dashboard 仅按 tab 整体替换，内部大数组无需深度代理。
  const dashboardCache = shallowReactive<Record<ApiTab, DashboardCache>>(createEmptyDashboardCache())
  let unifiedMetricsCache: ChannelMetrics[] = []
  let unifiedRecentActivityCache: NonNullable<ChannelDashboardResponse['recentActivity']> = []

  // 批量延迟测试加载状态
  const isPingingAll = ref(false)

  // 最后一次刷新状态（用于 systemStatus 更新）
  const lastRefreshSuccess = ref(true)

  // 全局 tick 订阅（5s），与图表等组件共用同一个 setInterval，visibility hidden 时自动暂停
  const AUTO_REFRESH_INTERVAL = 5000 // 5秒，降低统计聚合与锁竞争压力
  let autoRefreshRunning = false
  let autoRefreshUnsubscribe: (() => void) | null = null

  // 刷新并发控制：同一时间只允许一个 refresh 在跑；期间再次调用会被合并成一次后续刷新
  let refreshLoopPromise: Promise<void> | null = null
  let refreshRequested = false

  // ===== 计算属性 =====

  // 根据当前 Tab 返回对应的渠道数据
  const unifiedLlmChannelsData = computed(() => buildUnifiedChannelsData({
    messages: channelsData.value,
    chat: chatChannelsData.value,
    responses: responsesChannelsData.value,
    gemini: geminiChannelsData.value,
  }))

  const currentChannelsData = computed(() => {
    if (isLlmChannelKind(activeTab.value)) return unifiedLlmChannelsData.value
    switch (activeTab.value) {
      case 'images': return imagesChannelsData.value
      case 'vectors': return vectorsChannelsData.value
      default: return channelsData.value
    }
  })

  // 根据当前 Tab 返回对应的 Dashboard 数据（独立缓存，避免切换闪烁）
  const currentDashboardMetrics = computed(() => {
    if (isLlmChannelKind(activeTab.value)) {
      const nextMetrics = LLM_CHANNEL_KINDS.flatMap(kind => withRouteKindMetrics(kind, dashboardCache[kind].metrics))
      unifiedMetricsCache = reuseUnchangedItemsByKey(
        nextMetrics,
        unifiedMetricsCache,
        metric => `${metric.routeKind}:${metric.channelIndex}`,
      )
      return unifiedMetricsCache
    }
    return dashboardCache[activeTab.value].metrics
  })
  const currentDashboardStats = computed(() => dashboardCache[activeTab.value].stats)
  const currentDashboardRecentActivity = computed(() => {
    if (isLlmChannelKind(activeTab.value)) {
      const nextActivity = buildUnifiedRecentActivity(
        unifiedLlmChannelsData.value.channels,
        {
          messages: dashboardCache.messages.recentActivity,
          chat: dashboardCache.chat.recentActivity,
          responses: dashboardCache.responses.recentActivity,
          gemini: dashboardCache.gemini.recentActivity,
        },
      )
      unifiedRecentActivityCache = reuseUnchangedItemsByKey(
        nextActivity,
        unifiedRecentActivityCache,
        activity => `${activity.routeKind}:${activity.channelIndex}`,
      )
      return unifiedRecentActivityCache
    }
    return dashboardCache[activeTab.value].recentActivity
  })

  // 活跃渠道数（任一物理协议路由 active 即视为逻辑渠道活跃）
  const activeChannelCount = computed(() => {
    const data = currentChannelsData.value
    if (!data.channels) return 0
    return data.channels.filter(channel => {
      const statuses = channel.protocolRoutes?.map(route => normalizeChannelStatus(route.status))
      if (statuses?.length) return statuses.some(status => status === 'active')
      return normalizeChannelStatus(channel.status) === 'active'
    }).length
  })

  // 参与故障转移的渠道数（active + suspended）
  const failoverChannelCount = computed(() => {
    const data = currentChannelsData.value
    if (!data.channels) return 0
    return data.channels.filter(channel => {
      const statuses = channel.protocolRoutes?.map(route => normalizeChannelStatus(route.status))
      if (statuses?.length) return statuses.some(status => status !== 'disabled')
      return normalizeChannelStatus(channel.status) !== 'disabled'
    }).length
  })

  // ===== 辅助方法 =====

  // 合并渠道数据 + 冻结不可变字段的纯函数已抽到 @/utils/channelMerge，便于单元测试

  // ===== 操作方法 =====

  const mergeDashboardChannels = (
    current: ChannelsResponse,
    incomingChannels: Channel[],
  ): ChannelsResponse => {
    const channels = mergeChannelsWithLocalData(incomingChannels, current.channels)
    if (channels === current.channels) return current
    return { channels, current: current.current }
  }

  /**
   * 刷新渠道数据
   */
  const applyDashboard = (tab: ApiTab, dashboard: ChannelDashboardResponse) => {
    const previousCache = dashboardCache[tab]
    const routedMetrics = dashboard.metrics.map(metric => (
      metric.routeKind === tab ? metric : { ...metric, routeKind: tab }
    ))
    const metrics = reuseUnchangedItemsByKey(
      routedMetrics,
      previousCache.metrics,
      metric => `${metric.routeKind}:${metric.channelIndex}`,
    )
    const routedActivity = dashboard.recentActivity?.map(activity => (
      activity.routeKind === tab ? activity : { ...activity, routeKind: tab }
    ))
    const recentActivity = routedActivity
      ? reuseUnchangedItemsByKey(
          routedActivity,
          previousCache.recentActivity,
          activity => `${activity.routeKind}:${activity.channelIndex}`,
        )
      : undefined
    const stats = isStructurallyEqual(dashboard.stats, previousCache.stats)
      ? previousCache.stats
      : dashboard.stats
    const nextCache = (
      metrics === previousCache.metrics
      && stats === previousCache.stats
      && recentActivity === previousCache.recentActivity
    ) ? previousCache : { metrics, stats, recentActivity }

    switch (tab) {
      case 'gemini':
        geminiChannelsData.value = mergeDashboardChannels(geminiChannelsData.value, dashboard.channels)
        break
      case 'chat':
        chatChannelsData.value = mergeDashboardChannels(chatChannelsData.value, dashboard.channels)
        break
      case 'images':
        imagesChannelsData.value = mergeDashboardChannels(imagesChannelsData.value, dashboard.channels)
        break
      case 'vectors':
        vectorsChannelsData.value = mergeDashboardChannels(vectorsChannelsData.value, dashboard.channels)
        break
      case 'messages':
        channelsData.value = mergeDashboardChannels(channelsData.value, dashboard.channels)
        break
      case 'responses':
        responsesChannelsData.value = mergeDashboardChannels(responsesChannelsData.value, dashboard.channels)
        break
    }

    if (nextCache !== previousCache) dashboardCache[tab] = nextCache
  }

  async function refreshChannels() {
    refreshRequested = true
    if (refreshLoopPromise) return refreshLoopPromise

    const doRefresh = async (tab: ApiTab) => {
      applyDashboard(tab, await api.getChannelDashboard(tab))
    }

    refreshLoopPromise = (async () => {
      try {
        while (refreshRequested) {
          refreshRequested = false
          if (isLlmChannelKind(activeTab.value)) {
            const response = await api.getLlmChannelDashboard()
            for (const kind of LLM_CHANNEL_KINDS) {
              applyDashboard(kind, response.dashboards[kind])
            }
          } else {
            await doRefresh(activeTab.value)
          }
        }
        lastRefreshSuccess.value = true
      } catch (error) {
        lastRefreshSuccess.value = false
        throw error
      } finally {
        refreshLoopPromise = null
      }
    })()

    return refreshLoopPromise
  }

  /**
   * 保存渠道（添加或更新）
   */
  async function updateChannelByType(
    channelType: ApiTab,
    channelId: number,
    patch: Partial<Channel>,
  ): Promise<void> {
    // 优先使用统一 API（按 ChannelUID 寻址）
    const channelsForType = getChannelsForType(channelType)
    const channel = channelsForType.value.channels[channelId]
    if (channel?.channelUid) {
      await api.updateChannelV2(channel.channelUid, patch as Record<string, unknown>)
      return
    }
    // 兜底：使用旧 API
    switch (channelType) {
      case 'chat':
        await api.updateChatChannel(channelId, patch)
        break
      case 'vectors':
        await api.updateVectorsChannel(channelId, patch)
        break
      case 'images':
        await api.updateImagesChannel(channelId, patch)
        break
      case 'gemini':
        await api.updateGeminiChannel(channelId, patch)
        break
      case 'responses':
        await api.updateResponsesChannel(channelId, patch)
        break
      default:
        await api.updateChannel(channelId, patch)
    }
  }

  async function saveChannel(
    channel: Omit<Channel, 'index' | 'latency' | 'status'>,
    editingChannelIndex: number | null,
    options?: { isQuickAdd?: boolean; placement?: ChannelPlacement; channelType?: ApiTab; autoManaged?: boolean; accountUid?: string; originalChannel?: Channel; skipVerify?: boolean }
  ): Promise<{ success: boolean; message: string; quickAddMessage?: string; channelId?: number; warnings?: string[] }> {
    const targetTab = options?.channelType ?? activeTab.value
    const isResponses = targetTab === 'responses'
    const isGemini = targetTab === 'gemini'
    const isChat = targetTab === 'chat'
    const isImages = targetTab === 'images'
    const isVectors = targetTab === 'vectors'

    if (editingChannelIndex !== null) {
      // 更新现有渠道
      let accountWarnings: string[] | undefined
      if (options?.autoManaged && options.accountUid) {
        const original = options.originalChannel
        if (original && !original.providerId) {
          // 自定义托管账号：地址池按账号统一维护，随凭证一起提交；
          // Provider 模板托管账号不发送 baseUrls，避免空数组或手工地址覆盖模板地址。
          const baseUrls = channel.baseUrls?.length
            ? [...channel.baseUrls]
            : (channel.baseUrl ? [channel.baseUrl] : [])
          const accountResp = await api.updateManagedAccount(options.accountUid, {
            name: channel.name,
            apiKeys: channel.apiKeys,
            ...(baseUrls.length > 0 ? { baseUrls } : {}),
            ...(options.skipVerify ? { skipVerify: true } : {}),
          })
          accountWarnings = accountResp.warnings
        } else if (original) {
          const originalKeys = new Set(original.apiKeys)
          const nextKeys = new Set(channel.apiKeys)
          const addApiKeys = channel.apiKeys.filter(key => !originalKeys.has(key))
          const removeCredentialUids = (original.apiKeyConfigs ?? [])
            .filter(keyConfig => !nextKeys.has(keyConfig.key))
            .map(keyConfig => keyConfig.credentialUid)
            .filter((uid): uid is string => !!uid)
          if (addApiKeys.length > 0 || removeCredentialUids.length > 0) {
            await api.patchManagedAccountCredentials(options.accountUid, { addApiKeys, removeCredentialUids })
          }
        } else {
          await api.updateManagedAccount(options.accountUid, { name: channel.name, apiKeys: channel.apiKeys })
        }

        // 账号接口只负责凭证池；官网地址属于单条协议渠道，需要单独持久化。
        if (original && (channel.website ?? '').trim() !== (original.website ?? '').trim()) {
          await updateChannelByType(targetTab, editingChannelIndex, {
            website: (channel.website ?? '').trim(),
          })
        }
        // 备注同样是跨协议共享字段，账号接口（PUT /accounts）不承载 remark；
        // 变化时经单卡更新下发，由后端整组同步到逻辑渠道与兄弟卡。
        if (original && (channel.remark ?? '').trim() !== (original.remark ?? '').trim()) {
          await updateChannelByType(targetTab, editingChannelIndex, {
            remark: (channel.remark ?? '').trim(),
          })
        }
      } else if (isChat) {
        await updateChannelByType('chat', editingChannelIndex, channel)
      } else if (isVectors) {
        await updateChannelByType('vectors', editingChannelIndex, channel)
      } else if (isImages) {
        await updateChannelByType('images', editingChannelIndex, channel)
      } else if (isGemini) {
        await updateChannelByType('gemini', editingChannelIndex, channel)
      } else if (isResponses) {
        await updateChannelByType('responses', editingChannelIndex, channel)
      } else {
        await updateChannelByType('messages', editingChannelIndex, channel)
      }
      return { success: true, message: t('store.channel.updated'), channelId: editingChannelIndex, ...(accountWarnings?.length ? { warnings: accountWarnings } : {}) }
    } else {
      // 添加新渠道：使用统一 API（按 kind 区分协议）
      const placement: ChannelPlacement =
        options?.placement ?? (unref(preferencesStore.newChannelPlacement) === 'bottom' ? 'back' : 'front')
      await api.addChannelV2({ ...channel, kind: targetTab, placement })

      // 快速添加模式：根据用户偏好将新渠道放到队列顶部（含 5 分钟促销期）或末尾
      if (options?.isQuickAdd) {
        await refreshChannels() // 先刷新获取新渠道的 index
        const data = isChat
          ? chatChannelsData.value
          : isVectors
            ? vectorsChannelsData.value
          : isImages
            ? imagesChannelsData.value
            : isGemini
              ? geminiChannelsData.value
              : (isResponses ? responsesChannelsData.value : channelsData.value)

        // 后端 AddUpstream 把新渠道 prepend 到首位，因此通过 name 精确匹配定位
        // （不能用 "index 最大" 启发——后端是 unshift，新渠道 index = 0；其他渠道 index 全部 +1）
        const allChannels = data.channels || []
        const newChannel = allChannels.find(ch => ch.name === channel.name && ch.status !== 'disabled')
        if (newChannel) {
          try {
            const placeAtBottom = placement === 'back'

            // 1. 重新排序：根据偏好决定新渠道放首位还是末尾（其余渠道按既有 priority/index 升序）
            const otherIndexes = allChannels
              .filter(ch => ch.index !== newChannel.index && ch.status !== 'disabled')
              .sort((a, b) => (a.priority ?? a.index) - (b.priority ?? b.index))
              .map(ch => ch.index)
            const newOrder = placeAtBottom
              ? [...otherIndexes, newChannel.index]
              : [newChannel.index, ...otherIndexes]

            if (isChat) {
              await api.reorderChatChannels(newOrder)
            } else if (isVectors) {
              await api.reorderVectorsChannels(newOrder)
            } else if (isImages) {
              await api.reorderImagesChannels(newOrder)
            } else if (isGemini) {
              await api.reorderGeminiChannels(newOrder)
            } else if (isResponses) {
              await api.reorderResponsesChannels(newOrder)
            } else {
              await api.reorderChannels(newOrder)
            }

            // 2. 仅 top 模式设置 5 分钟促销期（300 秒）；bottom 模式不设促销期
            if (!placeAtBottom) {
              if (isChat) {
                await api.setChatChannelPromotion(newChannel.index, 300)
              } else if (isVectors) {
                await api.setVectorsChannelPromotion(newChannel.index, 300)
              } else if (isImages) {
                await api.setImagesChannelPromotion(newChannel.index, 300)
              } else if (isGemini) {
                await api.setGeminiChannelPromotion(newChannel.index, 300)
              } else if (isResponses) {
                await api.setResponsesChannelPromotion(newChannel.index, 300)
              } else {
                await api.setChannelPromotion(newChannel.index, 300)
              }
            }

            return placeAtBottom
              ? {
                  success: true,
                  message: t('store.channel.added')
                }
              : {
                  success: true,
                  message: t('store.channel.added'),
                  quickAddMessage: t('store.channel.quickAddPrioritized', { name: channel.name })
                }
          } catch (err) {
            console.warn('设置快速添加优先级失败:', err)
            // 不影响主流程
          }
        }
      }

      return { success: true, message: t('store.channel.added') }
    }
  }

  /**
   * 通过 provider 模板快速添加渠道（订阅中心 / 渠道列表快速添加的共享入口）。
   *
   * 行为对齐渠道列表的快速添加：显式 `placement: 'front'`（默认置顶），autoAddChannel
   * 返回后逐条按 kind 调 reorder + 5 分钟促销期，并 refreshChannels 让渠道列表实时更新。
   * 失败抛错由调用方捕获并展示。
   */
  async function quickAddFromTemplate(
    providerId: string,
    apiKeys: string[],
    options: { kind: ApiTab; placement?: ChannelPlacement; displayName?: string } = { kind: 'messages' }
  ): Promise<{ success: boolean; message: string; quickAddMessage?: string }> {
    const placement = options.placement ?? 'front'
    const response = await autoAddChannel(options.kind, { providerId, apiKeys, placement })
    await refreshChannels()
    const created = response.channels ?? []
    for (const ch of created) {
      const tab = ch.channelKind as ApiTab
      const data = getChannelsForType(tab).value
      const others = (data.channels || [])
        .filter(item => item.index !== ch.index && item.status !== 'disabled')
        .sort((a, b) => (a.priority ?? a.index) - (b.priority ?? b.index))
        .map(item => item.index)
      const newOrder = [ch.index, ...others]
      try {
        if (tab === 'chat') await api.reorderChatChannels(newOrder)
        else if (tab === 'vectors') await api.reorderVectorsChannels(newOrder)
        else if (tab === 'images') await api.reorderImagesChannels(newOrder)
        else if (tab === 'gemini') await api.reorderGeminiChannels(newOrder)
        else if (tab === 'responses') await api.reorderResponsesChannels(newOrder)
        else await api.reorderChannels(newOrder)
        if (tab === 'chat') await api.setChatChannelPromotion(ch.index, 300)
        else if (tab === 'vectors') await api.setVectorsChannelPromotion(ch.index, 300)
        else if (tab === 'images') await api.setImagesChannelPromotion(ch.index, 300)
        else if (tab === 'gemini') await api.setGeminiChannelPromotion(ch.index, 300)
        else if (tab === 'responses') await api.setResponsesChannelPromotion(ch.index, 300)
        else await api.setChannelPromotion(ch.index, 300)
      } catch (err) {
        // 单条 reorder/promotion 失败不影响其他渠道
        console.warn('[Channel-QuickAdd] reorder/promotion 失败:', tab, ch.index, err)
      }
    }
    const addedName = options.displayName || providerId
    return created.length > 1
      ? {
          success: true,
          message: t('store.channel.added'),
          quickAddMessage: t('store.channel.quickAddPrioritized', { name: addedName })
        }
      : { success: true, message: t('store.channel.added') }
  }

  /**
   * 删除渠道
   */
  async function deleteChannel(channelId: number, channelType: ApiTab = activeTab.value, accountUid?: string, logicalChannelUid?: string) {
    if (accountUid) {
      await api.deleteManagedAccount(accountUid)
    } else if (logicalChannelUid) {
      await api.deleteLogicalChannel(logicalChannelUid)
    } else {
      // 优先使用统一 API（按 ChannelUID 寻址）
      const channelsForType = getChannelsForType(channelType)
      const channel = channelsForType.value.channels[channelId]
      if (channel?.channelUid) {
        await api.deleteChannelV2(channel.channelUid)
      } else if (channelType === 'chat') {
        await api.deleteChatChannel(channelId)
      } else if (channelType === 'vectors') {
        await api.deleteVectorsChannel(channelId)
      } else if (channelType === 'images') {
        await api.deleteImagesChannel(channelId)
      } else if (channelType === 'gemini') {
        await api.deleteGeminiChannel(channelId)
      } else if (channelType === 'responses') {
        await api.deleteResponsesChannel(channelId)
      } else {
        await api.deleteChannel(channelId)
      }
    }
    await refreshChannels()
    return { success: true, message: t('store.channel.deleted') }
  }

  /**
   * 测试单个渠道延迟
   */
  async function pingChannel(channelId: number, channelType: ApiTab = activeTab.value) {
    const result = channelType === 'chat'
      ? await api.pingChatChannel(channelId)
      : channelType === 'vectors'
        ? await api.pingVectorsChannel(channelId)
      : channelType === 'images'
        ? await api.pingImagesChannel(channelId)
        : channelType === 'gemini'
          ? await api.pingGeminiChannel(channelId)
          : channelType === 'responses'
            ? await api.pingResponsesChannel(channelId)
            : await api.pingChannel(channelId)

    const data = channelType === 'chat'
      ? chatChannelsData.value
      : channelType === 'vectors'
        ? vectorsChannelsData.value
      : channelType === 'images'
        ? imagesChannelsData.value
        : channelType === 'gemini'
          ? geminiChannelsData.value
          : (channelType === 'messages' ? channelsData.value : responsesChannelsData.value)

    const channel = data.channels?.find(c => c.index === channelId)
    if (channel) {
      channel.latency = result.latency
      channel.latencyTestTime = Date.now()  // 记录测试时间，用于 5 分钟后清除
    }

    return { success: true }
  }

  /**
   * 批量测试所有渠道延迟
   */
  async function pingAllChannels() {
    if (isPingingAll.value) return { success: false, message: t('store.channel.pinging') }

    isPingingAll.value = true
    try {
      if (isLlmChannelKind(activeTab.value)) {
        const resultsByKind = await Promise.all(
          LLM_CHANNEL_KINDS.map(async kind => ({
            kind,
            results: await pingAllChannelsForKind(kind),
          }))
        )
        for (const item of resultsByKind) {
          applyPingResults(item.kind, item.results)
        }
        return { success: true }
      }

      const results = activeTab.value === 'vectors'
          ? await api.pingAllVectorsChannels()
          : await api.pingAllImagesChannels()

      const data = activeTab.value === 'vectors'
        ? vectorsChannelsData.value
        : imagesChannelsData.value

      const now = Date.now()
      results.forEach(result => {
        const channel = data.channels?.find(c => c.index === result.id)
        if (channel) {
          channel.latency = result.latency
          channel.latencyTestTime = now  // 记录测试时间，用于 5 分钟后清除
        }
      })

      return { success: true }
    } finally {
      isPingingAll.value = false
    }
  }

  async function pingAllChannelsForKind(kind: LlmChannelKind) {
    switch (kind) {
      case 'chat':
        return api.pingAllChatChannels()
      case 'responses':
        return api.pingAllResponsesChannels()
      case 'gemini':
        return api.pingAllGeminiChannels()
      default:
        return api.pingAllChannels()
    }
  }

  function applyPingResults(kind: LlmChannelKind, results: Array<{ id: number; latency: number }>) {
    const data = kind === 'chat'
      ? chatChannelsData.value
      : kind === 'responses'
        ? responsesChannelsData.value
        : kind === 'gemini'
          ? geminiChannelsData.value
          : channelsData.value
    const now = Date.now()
    results.forEach(result => {
      const channel = data.channels?.find(c => c.index === result.id)
      if (channel) {
        channel.latency = result.latency
        channel.latencyTestTime = now
      }
    })
  }

  /**
   * 启动自动刷新定时器（使用全局 tick，visibility hidden 时自动暂停）
   */
  function startAutoRefresh() {
    stopAutoRefresh()
    autoRefreshRunning = true

    // 退订旧订阅（如果 `stopAutoRefresh` 未被调用过）
    if (autoRefreshUnsubscribe) { autoRefreshUnsubscribe(); autoRefreshUnsubscribe = null }

    autoRefreshUnsubscribe = registerGlobalTick(AUTO_REFRESH_INTERVAL, () => {
      if (!autoRefreshRunning) return
      void refreshChannels().catch((error) => {
        console.warn(t('store.channel.autoRefreshFailed'), error)
      })
    })
  }

  /**
   * 停止自动刷新定时器
   */
  function stopAutoRefresh() {
    autoRefreshRunning = false
    if (autoRefreshUnsubscribe) {
      autoRefreshUnsubscribe()
      autoRefreshUnsubscribe = null
    }
  }

  /**
   * 清空所有渠道数据（用于注销）
   */
  function clearChannels() {
    channelsData.value = {
      channels: [],
      current: -1
    }
    chatChannelsData.value = {
      channels: [],
      current: -1
    }
    imagesChannelsData.value = {
      channels: [],
      current: -1
    }
    vectorsChannelsData.value = {
      channels: [],
      current: -1
    }
    responsesChannelsData.value = {
      channels: [],
      current: -1
    }
    geminiChannelsData.value = {
      channels: [],
      current: -1
    }

    // 清空所有 tab 的独立缓存
    Object.assign(dashboardCache, createEmptyDashboardCache())
    unifiedMetricsCache = []
    unifiedRecentActivityCache = []

    // 重置状态标志，避免注销后状态残留
    lastRefreshSuccess.value = true
    isPingingAll.value = false
  }

  // ===== 返回公开接口 =====
  return {
    // 状态
    activeTab,
    channelsData,
    chatChannelsData,
    imagesChannelsData,
    vectorsChannelsData,
    responsesChannelsData,
    geminiChannelsData,
    unifiedLlmChannelsData,
    isPingingAll,
    lastRefreshSuccess,

    // 计算属性
    currentChannelsData,
    currentDashboardMetrics,
    currentDashboardStats,
    currentDashboardRecentActivity,
    activeChannelCount,
    failoverChannelCount,

    // 方法
    refreshChannels,
    saveChannel,
    deleteChannel,
    pingChannel,
    pingAllChannels,
    startAutoRefresh,
    stopAutoRefresh,
    clearChannels,
    quickAddFromTemplate,
  }
})
