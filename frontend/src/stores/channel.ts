import { defineStore } from 'pinia'
import { ref, computed, watch } from 'vue'
import { useRouter } from 'vue-router'
import { usePreferencesStore } from '@/stores/preferences'
import { api, type Channel, type ChannelsResponse, type ChannelMetrics, type ChannelDashboardResponse } from '@/services/api'
import { normalizeLocale, translate } from '@/i18n/core'

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
    return translate(normalizeLocale(preferencesStore.uiLanguage), key, params)
  }
  // ===== 状态 =====

  // 当前选中的 API 类型
  type ApiTab = 'messages' | 'chat' | 'responses' | 'gemini' | 'images'
  const activeTab = ref<ApiTab>('messages')

  // 路由同步：从路由读取当前类型
  const router = useRouter()
  const currentChannelType = computed(() => {
    const route = router.currentRoute.value
    const type = route.params.type as ApiTab
    return (type === 'messages' || type === 'chat' || type === 'responses' || type === 'gemini' || type === 'images') ? type : 'messages'
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
    }
  })

  // 批量延迟测试加载状态
  const isPingingAll = ref(false)

  // 最后一次刷新状态（用于 systemStatus 更新）
  const lastRefreshSuccess = ref(true)

  // 自动刷新定时器（串行 setTimeout，避免重入）
  let autoRefreshTimer: ReturnType<typeof setTimeout> | null = null
  let autoRefreshRunning = false
  const AUTO_REFRESH_INTERVAL = 5000 // 5秒，降低统计聚合与锁竞争压力

  // 刷新并发控制：同一时间只允许一个 refresh 在跑；期间再次调用会被合并成一次后续刷新
  let refreshLoopPromise: Promise<void> | null = null
  let refreshRequested = false

  // ===== 计算属性 =====

  // 根据当前 Tab 返回对应的渠道数据
  const currentChannelsData = computed(() => {
    switch (activeTab.value) {
      case 'messages': return channelsData.value
      case 'chat': return chatChannelsData.value
      case 'responses': return responsesChannelsData.value
      case 'gemini': return geminiChannelsData.value
      case 'images': return imagesChannelsData.value
      default: return channelsData.value
    }
  })

  // 根据当前 Tab 返回对应的 Dashboard 数据（独立缓存，避免切换闪烁）
  const currentDashboardMetrics = computed(() => dashboardCache.value[activeTab.value].metrics)
  const currentDashboardStats = computed(() => dashboardCache.value[activeTab.value].stats)
  const currentDashboardRecentActivity = computed(() => dashboardCache.value[activeTab.value].recentActivity)

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

  // 合并渠道数据，保留本地的延迟测试结果
  const LATENCY_VALID_DURATION = 5 * 60 * 1000 // 5 分钟有效期

  function mergeChannelsWithLocalData(newChannels: Channel[], existingChannels: Channel[] | undefined): Channel[] {
    if (!existingChannels) return newChannels

    const now = Date.now()
    return newChannels.map(newCh => {
      const existingCh = existingChannels.find(ch => ch.index === newCh.index)
      // 只有在 5 分钟有效期内才保留本地延迟测试结果
      if (existingCh?.latencyTestTime && (now - existingCh.latencyTestTime) < LATENCY_VALID_DURATION) {
        return {
          ...newCh,
          latency: existingCh.latency,
          latencyTestTime: existingCh.latencyTestTime
        }
      }
      return newCh
    })
  }

  // ===== 操作方法 =====

  /**
   * 刷新渠道数据
   */
  async function refreshChannels() {
    refreshRequested = true
    if (refreshLoopPromise) return refreshLoopPromise

    const doRefresh = async (tab: ApiTab) => {
      try {
        // 统一使用 dashboard 接口
        const dashboard = await api.getChannelDashboard(tab)

        // 根据 tab 更新对应的数据和缓存
        switch (tab) {
          case 'gemini':
            geminiChannelsData.value = {
              channels: mergeChannelsWithLocalData(dashboard.channels, geminiChannelsData.value.channels),
              current: geminiChannelsData.value.current
            }
            dashboardCache.value.gemini = {
              metrics: dashboard.metrics,
              stats: dashboard.stats,
              recentActivity: dashboard.recentActivity
            }
            break

          case 'chat':
            chatChannelsData.value = {
              channels: mergeChannelsWithLocalData(dashboard.channels, chatChannelsData.value.channels),
              current: chatChannelsData.value.current
            }
            dashboardCache.value.chat = {
              metrics: dashboard.metrics,
              stats: dashboard.stats,
              recentActivity: dashboard.recentActivity
            }
            break

          case 'images':
            imagesChannelsData.value = {
              channels: mergeChannelsWithLocalData(dashboard.channels, imagesChannelsData.value.channels),
              current: imagesChannelsData.value.current
            }
            dashboardCache.value.images = {
              metrics: dashboard.metrics,
              stats: dashboard.stats,
              recentActivity: dashboard.recentActivity
            }
            break

          case 'messages':
            channelsData.value = {
              channels: mergeChannelsWithLocalData(dashboard.channels, channelsData.value.channels),
              current: channelsData.value.current
            }
            dashboardCache.value.messages = {
              metrics: dashboard.metrics,
              stats: dashboard.stats,
              recentActivity: dashboard.recentActivity
            }
            break

          case 'responses':
            responsesChannelsData.value = {
              channels: mergeChannelsWithLocalData(dashboard.channels, responsesChannelsData.value.channels),
              current: responsesChannelsData.value.current
            }
            dashboardCache.value.responses = {
              metrics: dashboard.metrics,
              stats: dashboard.stats,
              recentActivity: dashboard.recentActivity
            }
            break
        }

        lastRefreshSuccess.value = true
      } catch (error) {
        lastRefreshSuccess.value = false
        throw error
      }
    }

    refreshLoopPromise = (async () => {
      try {
        while (refreshRequested) {
          refreshRequested = false
          const tab = activeTab.value
          await doRefresh(tab)
        }
      } finally {
        refreshLoopPromise = null
      }
    })()

    return refreshLoopPromise
  }

  /**
   * 保存渠道（添加或更新）
   */
  async function saveChannel(
    channel: Omit<Channel, 'index' | 'latency' | 'status'>,
    editingChannelIndex: number | null,
    options?: { isQuickAdd?: boolean }
  ) {
    const isResponses = activeTab.value === 'responses'
    const isGemini = activeTab.value === 'gemini'
    const isChat = activeTab.value === 'chat'
    const isImages = activeTab.value === 'images'

    if (editingChannelIndex !== null) {
      // 更新现有渠道
      if (isChat) {
        await api.updateChatChannel(editingChannelIndex, channel)
      } else if (isImages) {
        await api.updateImagesChannel(editingChannelIndex, channel)
      } else if (isGemini) {
        await api.updateGeminiChannel(editingChannelIndex, channel)
      } else if (isResponses) {
        await api.updateResponsesChannel(editingChannelIndex, channel)
      } else {
        await api.updateChannel(editingChannelIndex, channel)
      }
      return { success: true, message: t('store.channel.updated') }
    } else {
      // 添加新渠道
      if (isChat) {
        await api.addChatChannel(channel)
      } else if (isImages) {
        await api.addImagesChannel(channel)
      } else if (isGemini) {
        await api.addGeminiChannel(channel)
      } else if (isResponses) {
        await api.addResponsesChannel(channel)
      } else {
        await api.addChannel(channel)
      }

      // 快速添加模式：将新渠道设为第一优先级并设置5分钟促销期
      if (options?.isQuickAdd) {
        await refreshChannels() // 先刷新获取新渠道的 index
        const data = isChat
          ? chatChannelsData.value
          : isImages
            ? imagesChannelsData.value
            : isGemini
              ? geminiChannelsData.value
              : (isResponses ? responsesChannelsData.value : channelsData.value)

        // 找到新添加的渠道（应该是列表中 index 最大的 active 状态渠道）
        const activeChannels = data.channels?.filter(ch => ch.status !== 'disabled') || []
        if (activeChannels.length > 0) {
          // 新添加的渠道会分配到最大的 index
          const newChannel = activeChannels.reduce((max, ch) => ch.index > max.index ? ch : max, activeChannels[0])

          try {
            // 1. 重新排序：将新渠道放到第一位
            const otherIndexes = activeChannels
              .filter(ch => ch.index !== newChannel.index)
              .sort((a, b) => (a.priority ?? a.index) - (b.priority ?? b.index))
              .map(ch => ch.index)
            const newOrder = [newChannel.index, ...otherIndexes]

            if (isChat) {
              await api.reorderChatChannels(newOrder)
            } else if (isImages) {
              await api.reorderImagesChannels(newOrder)
            } else if (isGemini) {
              await api.reorderGeminiChannels(newOrder)
            } else if (isResponses) {
              await api.reorderResponsesChannels(newOrder)
            } else {
              await api.reorderChannels(newOrder)
            }

            // 2. 设置5分钟促销期（300秒）
            if (isChat) {
              await api.setChatChannelPromotion(newChannel.index, 300)
            } else if (isImages) {
              await api.setImagesChannelPromotion(newChannel.index, 300)
            } else if (isGemini) {
              await api.setGeminiChannelPromotion(newChannel.index, 300)
            } else if (isResponses) {
              await api.setResponsesChannelPromotion(newChannel.index, 300)
            } else {
              await api.setChannelPromotion(newChannel.index, 300)
            }

            return {
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
  async function deleteChannel(channelId: number) {
    if (activeTab.value === 'chat') {
      await api.deleteChatChannel(channelId)
    } else if (activeTab.value === 'images') {
      await api.deleteImagesChannel(channelId)
    } else if (activeTab.value === 'gemini') {
      await api.deleteGeminiChannel(channelId)
    } else if (activeTab.value === 'responses') {
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
  async function pingChannel(channelId: number) {
    const result = activeTab.value === 'chat'
      ? await api.pingChatChannel(channelId)
      : activeTab.value === 'images'
        ? await api.pingImagesChannel(channelId)
      : activeTab.value === 'gemini'
        ? await api.pingGeminiChannel(channelId)
        : activeTab.value === 'responses'
          ? await api.pingResponsesChannel(channelId)
          : await api.pingChannel(channelId)

    const data = activeTab.value === 'chat'
      ? chatChannelsData.value
      : activeTab.value === 'images'
        ? imagesChannelsData.value
      : activeTab.value === 'gemini'
        ? geminiChannelsData.value
        : (activeTab.value === 'messages' ? channelsData.value : responsesChannelsData.value)

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
      const results = activeTab.value === 'chat'
        ? await api.pingAllChatChannels()
        : activeTab.value === 'images'
          ? await api.pingAllImagesChannels()
        : activeTab.value === 'gemini'
          ? await api.pingAllGeminiChannels()
          : activeTab.value === 'responses'
            ? await api.pingAllResponsesChannels()
            : await api.pingAllChannels()

      const data = activeTab.value === 'chat'
        ? chatChannelsData.value
        : activeTab.value === 'images'
          ? imagesChannelsData.value
        : activeTab.value === 'gemini'
          ? geminiChannelsData.value
          : (activeTab.value === 'messages' ? channelsData.value : responsesChannelsData.value)

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

  /**
   * 启动自动刷新定时器
   */
  function startAutoRefresh() {
    stopAutoRefresh()
    autoRefreshRunning = true

    const tick = async () => {
      if (!autoRefreshRunning) return
      try {
        await refreshChannels()
      } catch (error) {
        console.warn(t('store.channel.autoRefreshFailed'), error)
      } finally {
        if (autoRefreshRunning) {
          autoRefreshTimer = setTimeout(() => {
            void tick()
          }, AUTO_REFRESH_INTERVAL)
        }
      }
    }

    autoRefreshTimer = setTimeout(() => {
      void tick()
    }, AUTO_REFRESH_INTERVAL)
  }

  /**
   * 停止自动刷新定时器
   */
  function stopAutoRefresh() {
    autoRefreshRunning = false
    if (!autoRefreshTimer) return
    clearTimeout(autoRefreshTimer)
    autoRefreshTimer = null
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
    responsesChannelsData,
    geminiChannelsData,
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
