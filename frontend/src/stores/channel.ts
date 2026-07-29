import { defineStore } from 'pinia'
import { ref, computed, unref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { usePreferencesStore } from '@/stores/preferences'
import { api, type Channel, type ChannelPlacement, type ChannelsResponse, type ChannelMetrics, type ChannelDashboardResponse } from '@/services/api'
import { normalizeLocale } from '@/i18n/core'
import { translate } from '@/i18n'
import { registerGlobalTick } from '@/composables/useGlobalTick'
import { mergeChannelsWithLocalData } from '@/utils/channelMerge'
import {
  buildUnifiedRecentActivity,
  buildUnifiedChannelsData,
  isLlmChannelKind,
  LLM_CHANNEL_KINDS,
  withRouteKindMetrics,
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

  // Dashboard 数据缓存结构（每个 tab 独立缓存）
  interface DashboardCache {
    metrics: ChannelMetrics[]
    stats: ChannelDashboardResponse['stats'] | undefined
    recentActivity: ChannelDashboardResponse['recentActivity'] | undefined
  }

  const dashboardCache = ref<Record<ApiTab, DashboardCache>>({
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
      return LLM_CHANNEL_KINDS.flatMap(kind => withRouteKindMetrics(kind, dashboardCache.value[kind].metrics))
    }
    return dashboardCache.value[activeTab.value].metrics
  })
  const currentDashboardStats = computed(() => dashboardCache.value[activeTab.value].stats)
  const currentDashboardRecentActivity = computed(() => {
    if (isLlmChannelKind(activeTab.value)) {
      return buildUnifiedRecentActivity(
        unifiedLlmChannelsData.value.channels,
        {
          messages: dashboardCache.value.messages.recentActivity,
          chat: dashboardCache.value.chat.recentActivity,
          responses: dashboardCache.value.responses.recentActivity,
          gemini: dashboardCache.value.gemini.recentActivity,
        },
      )
    }
    return dashboardCache.value[activeTab.value].recentActivity
  })

  // 活跃渠道数（仅 active 状态）
  const activeChannelCount = computed(() => {
    const data = currentChannelsData.value
    if (!data.channels) return 0
    return data.channels.filter(ch => ch.status === 'active' || ch.status === undefined || ch.status === '').length
  })

  // 参与故障转移的渠道数（active + suspended）
  const failoverChannelCount = computed(() => {
    const data = currentChannelsData.value
    if (!data.channels) return 0
    return data.channels.filter(ch => ch.status !== 'disabled').length
  })

  // ===== 辅助方法 =====

  // 合并渠道数据 + 冻结不可变字段的纯函数已抽到 @/utils/channelMerge，便于单元测试

  // ===== 操作方法 =====

  /**
   * 刷新渠道数据
   */
  const applyDashboard = (tab: ApiTab, dashboard: ChannelDashboardResponse) => {
    const nextCache = {
      metrics: dashboard.metrics,
      stats: dashboard.stats,
      recentActivity: dashboard.recentActivity,
    }

    switch (tab) {
      case 'gemini':
        geminiChannelsData.value = {
          channels: mergeChannelsWithLocalData(dashboard.channels, geminiChannelsData.value.channels),
          current: geminiChannelsData.value.current,
        }
        dashboardCache.value.gemini = nextCache
        break
      case 'chat':
        chatChannelsData.value = {
          channels: mergeChannelsWithLocalData(dashboard.channels, chatChannelsData.value.channels),
          current: chatChannelsData.value.current,
        }
        dashboardCache.value.chat = nextCache
        break
      case 'images':
        imagesChannelsData.value = {
          channels: mergeChannelsWithLocalData(dashboard.channels, imagesChannelsData.value.channels),
          current: imagesChannelsData.value.current,
        }
        dashboardCache.value.images = nextCache
        break
      case 'vectors':
        vectorsChannelsData.value = {
          channels: mergeChannelsWithLocalData(dashboard.channels, vectorsChannelsData.value.channels),
          current: vectorsChannelsData.value.current,
        }
        dashboardCache.value.vectors = nextCache
        break
      case 'messages':
        channelsData.value = {
          channels: mergeChannelsWithLocalData(dashboard.channels, channelsData.value.channels),
          current: channelsData.value.current,
        }
        dashboardCache.value.messages = nextCache
        break
      case 'responses':
        responsesChannelsData.value = {
          channels: mergeChannelsWithLocalData(dashboard.channels, responsesChannelsData.value.channels),
          current: responsesChannelsData.value.current,
        }
        dashboardCache.value.responses = nextCache
        break
    }
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
    options?: { isQuickAdd?: boolean; placement?: ChannelPlacement; channelType?: ApiTab; autoManaged?: boolean; accountUid?: string; originalChannel?: Channel }
  ): Promise<{ success: boolean; message: string; quickAddMessage?: string; channelId?: number }> {
    const targetTab = options?.channelType ?? activeTab.value
    const isResponses = targetTab === 'responses'
    const isGemini = targetTab === 'gemini'
    const isChat = targetTab === 'chat'
    const isImages = targetTab === 'images'
    const isVectors = targetTab === 'vectors'

    if (editingChannelIndex !== null) {
      // 更新现有渠道
      if (options?.autoManaged && options.accountUid) {
        const original = options.originalChannel
        if (original && !original.providerId) {
          await api.updateManagedAccount(options.accountUid, { name: channel.name, apiKeys: channel.apiKeys })
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
      } else if (isChat) {
        await api.updateChatChannel(editingChannelIndex, channel)
      } else if (isVectors) {
        await api.updateVectorsChannel(editingChannelIndex, channel)
      } else if (isImages) {
        await api.updateImagesChannel(editingChannelIndex, channel)
      } else if (isGemini) {
        await api.updateGeminiChannel(editingChannelIndex, channel)
      } else if (isResponses) {
        await api.updateResponsesChannel(editingChannelIndex, channel)
      } else {
        await api.updateChannel(editingChannelIndex, channel)
      }
      return { success: true, message: t('store.channel.updated'), channelId: editingChannelIndex }
    } else {
      // 添加新渠道：placement 优先取调用方（新增弹窗选择），缺省跟随全局偏好
      const placement: ChannelPlacement =
        options?.placement ?? (unref(preferencesStore.newChannelPlacement) === 'bottom' ? 'back' : 'front')
      if (isChat) {
        await api.addChatChannel(channel, placement)
      } else if (isVectors) {
        await api.addVectorsChannel(channel, placement)
      } else if (isImages) {
        await api.addImagesChannel(channel, placement)
      } else if (isGemini) {
        await api.addGeminiChannel(channel, placement)
      } else if (isResponses) {
        await api.addResponsesChannel(channel, placement)
      } else {
        await api.addChannel(channel, placement)
      }

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
   * 删除渠道
   */
  async function deleteChannel(channelId: number, channelType: ApiTab = activeTab.value, accountUid?: string) {
    if (accountUid) {
      await api.deleteManagedAccount(accountUid)
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
    dashboardCache.value = {
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
      images: {
        metrics: [],
        stats: undefined,
        recentActivity: undefined
      },
      vectors: {
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
      }
    }

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
  }
})
