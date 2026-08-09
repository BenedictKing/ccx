import { ref, reactive, onMounted, onUnmounted, computed, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useTheme } from 'vuetify'
import { api, fetchHealth, ApiError, type Channel, type ChannelKind, type ChannelPlacement } from '../services/api'
import { versionService } from '../services/version'
import { useAuthStore } from '../stores/auth'
import { useChannelStore } from '../stores/channel'
import { usePreferencesStore } from '../stores/preferences'
import { useDialogStore } from '../stores/dialog'
import { useSystemStore } from '../stores/system'
import { useI18n } from '../i18n'
import type { SupportedLocale } from '../i18n'
import { useAppTheme } from './useTheme'
import { useToasts } from './useToasts'
import { streamTimeoutPresets as sharedStreamPresets } from '../utils/streamTimeoutPresets'
import { isAutoManagedAccountChannel } from '../utils/providerDisplay'

export function useAppController() {
// 路由
  const route = useRoute()

  // Vuetify主题
  const theme = useTheme()

  // 应用主题系统
  const { init: initTheme } = useAppTheme()

  // 认证 Store
  // 注意：as any 是 Pinia 3.x + Vue 3.5 + TS 6.x 兼容补丁——
  // Vue 3.5 将 Ref<T> 改为 Ref<T, S>，Pinia 的 UnwrapRef<Ref<infer V, unknown>> 模式失效，
  // 导致模板中访问 store 属性时类型未被自动解包。运行时行为正常。
  const authStore = useAuthStore()

  // 渠道 Store
  const channelStore = useChannelStore()

  // 偏好设置 Store
  const preferencesStore = usePreferencesStore()

  // 对话框 Store
  const dialogStore = useDialogStore()

  // 系统状态 Store
  const systemStore = useSystemStore()
  const { locale, t, setLocale } = useI18n()

  const languageOptions: Array<{ value: SupportedLocale, label: string, shortLabel: string }> = [
    { value: 'en', label: 'English', shortLabel: 'EN' },
    { value: 'id', label: 'Bahasa Indonesia', shortLabel: 'ID' },
    { value: 'zh-CN', label: '简体中文', shortLabel: '中' },
  ]

  const currentLocale = computed(() => locale.value)
  const currentLanguageShortLabel = computed(() => {
    return languageOptions.find(option => option.value === currentLocale.value)?.shortLabel ?? currentLocale.value.slice(0, 2).toUpperCase()
  })

  // API 类型 Tab 选项（移动端下拉菜单使用）
  const apiTabOptions = [
    { value: 'messages', labelKey: 'app.tabs.channels', route: '/channels/messages', icon: 'mdi-server-network' },
    { value: 'images', labelKey: 'app.tabs.images', route: '/channels/images', icon: 'mdi-image-outline' },
    { value: 'vectors', labelKey: 'app.tabs.vectors', route: '/channels/vectors', icon: 'mdi-vector-point' },
    { value: 'conversations', labelKey: 'app.tabs.conversations', route: '/conversations', icon: 'mdi-radar' },
    { value: 'health', labelKey: 'app.tabs.healthCenter', route: '/health', icon: 'mdi-stethoscope' },
    { value: 'subscriptions', labelKey: 'app.tabs.subscriptions', route: '/subscriptions', icon: 'mdi-cash-multiple' },
    { value: 'cockpit', labelKey: 'app.tabs.cockpitOverview', route: '/cockpit', icon: 'mdi-view-dashboard-outline' },
    { value: 'autopilot', labelKey: 'app.tabs.autopilot', route: '/autopilot', icon: 'mdi-steering' },
    { value: 'cost-report', labelKey: 'app.tabs.costReport', route: '/cost-report', icon: 'mdi-finance' },
  ] as const

  const translatedApiTabOptions = computed(() => {
    return apiTabOptions.map(tab => ({
      ...tab,
      label: t(tab.labelKey),
    }))
  })
  const isDesktopWebUI = new URLSearchParams(window.location.search).get('ccx_desktop') === '1'

  const currentTabLabel = computed(() => {
    return translatedApiTabOptions.value.find(tab => tab.value === channelStore.activeTab)?.label || channelStore.activeTab
  })

  const activeTrafficTitle = computed(() => t('app.stats.trafficTitle', { tab: currentTabLabel.value }))
  const editingChannelType = computed<ChannelKind>(() => {
    return dialogStore.editingChannel?.routeKind ?? channelStore.activeTab
  })

  const getChannelRouteKind = (channel?: Channel | null): ChannelKind => {
    return channel?.routeKind ?? channelStore.activeTab
  }

  const getChannelRouteIndex = (channel: Channel): number => {
    return channel.routeIndex ?? channel.index
  }

  const systemStatusText = computed(() => {
    switch (systemStore.systemStatus) {
      case 'running':
        return t('system.running')
      case 'error':
        return t('system.error')
      case 'connecting':
        return t('system.connecting')
      default:
        return t('system.unknown')
    }
  })

  const systemStatusDesc = computed(() => {
    switch (systemStore.systemStatus) {
      case 'running':
        return t('system.runningDesc')
      case 'error':
        return t('system.errorDesc')
      case 'connecting':
        return t('system.connectingDesc')
      default:
        return ''
    }
  })

  // 对话框状态已迁移到 DialogStore

  // 主题和偏好设置已迁移到 PreferencesStore

  // 系统状态已迁移到 SystemStore

  const { toasts, getToastColor, getToastIcon, showToast, showErrorToast, showSuccessToast } = useToasts()

  // 主要功能函数 - 使用 ChannelStore
  const refreshChannels = async () => {
    try {
      await channelStore.refreshChannels()
    } catch (error) {
      handleAuthError(error)
    }
  }

  const saveChannel = async (
    channel: Omit<Channel, 'index' | 'latency' | 'status'>,
    options?: { isQuickAdd?: boolean; placement?: ChannelPlacement },
    onComplete?: () => void,
  ) => {
    try {
      const editingChannel = dialogStore.editingChannel as Channel | null
      const result = await channelStore.saveChannel(
        channel,
        editingChannel ? getChannelRouteIndex(editingChannel) : null,
        {
          ...options,
          channelType: getChannelRouteKind(editingChannel),
          autoManaged: isAutoManagedAccountChannel(editingChannel),
          accountUid: editingChannel?.accountUid,
          originalChannel: editingChannel ?? undefined,
        },
      )
      showToast(result.message, 'success')
      if (result.quickAddMessage) {
        showToast(result.quickAddMessage, 'info')
      }
      dialogStore.closeAddChannelModal()
      dialogStore.closeEditChannelModal()
      await refreshChannels()

      return result
    } catch (error) {
      handleAuthError(error)
      return undefined
    } finally {
      onComplete?.()
    }
  }

  const handleAutoAddedChannel = async (_channelId: number) => {
    dialogStore.closeAddChannelModal()
    showToast(t('store.channel.added'), 'success')
    await refreshChannels()
  }

  const editChannel = (channel: Channel) => {
    dialogStore.openEditChannelModal(channel)
  }

  const deleteChannel = async (target: number | Channel) => {
    const ok = await dialogStore.confirm({
      message: t('toast.confirmDeleteChannel'),
      confirmText: t('app.actions.delete'),
    })
    if (!ok) return

    try {
      const channelId = typeof target === 'number' ? target : getChannelRouteIndex(target)
      const channelType = typeof target === 'number' ? channelStore.activeTab : getChannelRouteKind(target)
      const accountUid = typeof target === 'number' || !isAutoManagedAccountChannel(target) ? undefined : target.accountUid
      const logicalChannelUid = typeof target !== 'number' ? target.logicalChannelUid : undefined
      const result = await channelStore.deleteChannel(channelId, channelType, accountUid, logicalChannelUid)
      showToast(result.message, 'success')
    } catch (error) {
      handleAuthError(error)
    }
  }

  const openAddChannelModal = () => {
    dialogStore.openAddChannelModal()
  }

  const _openAddKeyModal = (channelId: number) => {
    dialogStore.openAddKeyModal(channelId)
  }

  const addApiKey = async () => {
    if (!dialogStore.newApiKey.trim()) return

    try {
      if (channelStore.activeTab === 'chat') {
        await api.addChatApiKey(dialogStore.selectedChannelForKey, dialogStore.newApiKey.trim())
      } else if (channelStore.activeTab === 'vectors') {
        await api.addVectorsApiKey(dialogStore.selectedChannelForKey, dialogStore.newApiKey.trim())
      } else if (channelStore.activeTab === 'images') {
        await api.addImagesApiKey(dialogStore.selectedChannelForKey, dialogStore.newApiKey.trim())
      } else if (channelStore.activeTab === 'gemini') {
        await api.addGeminiApiKey(dialogStore.selectedChannelForKey, dialogStore.newApiKey.trim())
      } else if (channelStore.activeTab === 'responses') {
        await api.addResponsesApiKey(dialogStore.selectedChannelForKey, dialogStore.newApiKey.trim())
      } else {
        await api.addApiKey(dialogStore.selectedChannelForKey, dialogStore.newApiKey.trim())
      }
      showToast(t('toast.apiKeyAdded'), 'success')
      dialogStore.closeAddKeyModal()
      await refreshChannels()
    } catch (error) {
      showToast(t('toast.apiKeyAddFailed', { message: error instanceof Error ? error.message : t('system.unknown') }), 'error')
    }
  }

  const _removeApiKey = async (channelId: number, apiKey: string) => {
    const ok = await dialogStore.confirm({
      message: t('toast.confirmDeleteApiKey'),
      confirmText: t('app.actions.delete'),
    })
    if (!ok) return

    try {
      if (channelStore.activeTab === 'chat') {
        await api.removeChatApiKey(channelId, apiKey)
      } else if (channelStore.activeTab === 'vectors') {
        await api.removeVectorsApiKey(channelId, apiKey)
      } else if (channelStore.activeTab === 'images') {
        await api.removeImagesApiKey(channelId, apiKey)
      } else if (channelStore.activeTab === 'gemini') {
        await api.removeGeminiApiKey(channelId, apiKey)
      } else if (channelStore.activeTab === 'responses') {
        await api.removeResponsesApiKey(channelId, apiKey)
      } else {
        await api.removeApiKey(channelId, apiKey)
      }
      showToast(t('toast.apiKeyDeleted'), 'success')
      await refreshChannels()
    } catch (error) {
      showToast(t('toast.apiKeyDeleteFailed', { message: error instanceof Error ? error.message : t('system.unknown') }), 'error')
    }
  }

  // 新用户指引
  const showGuide = ref(false)

  function openGuide() {
    showGuide.value = true
  }

  // 指引关闭后标记已看过，避免下次自动弹出
  watch(showGuide, (open) => {
    if (!open) preferencesStore.markGuideSeen()
  })

  // 首次认证成功后自动弹出一次指引（仅独立 WebUI，桌面端内嵌不打扰）
  // 直接 watch authStore（在前文已定义），避免引用尚未声明的 isAuthenticated computed
  watch(() => authStore.isAuthenticated, (authed) => {
    const isEmbedded = typeof window !== 'undefined' && window.self !== window.top
    if (authed && !preferencesStore.hasSeenGuide && !isEmbedded) {
      showGuide.value = true
    }
  })

  // 熔断器配置
  const circuitBreakerDialogOpen = ref(false)
  const cbSaving = ref(false)
  const activePreset = ref('balanced')
  const cbForm = reactive({
    windowSize: 20,
    failureThreshold: 0.70,
    consecutiveFailuresThreshold: 5,
    requestTimeoutMs: 300000,
    responseHeaderTimeoutMs: 120000,
    streamFirstContentTimeoutMs: 90000,
    streamInactivityTimeoutMs: 90000,
    streamToolCallIdleTimeoutMs: 300000,
  })

  // 预设向更宽松方向平移一档：原温和→均衡、原均衡→激进、原激进移除（需手动自定义）
  // 新增更宽松的温和策略，降低新用户误熔断概率
  const cbPresets = [
    { key: 'gentle', labelKey: 'dialog.circuitBreaker.presetGentle' as const, windowSize: 50, failureThreshold: 0.85, consecutiveFailuresThreshold: 10, requestTimeoutMs: 300000, responseHeaderTimeoutMs: 300000, streamFirstContentTimeoutMs: sharedStreamPresets.gentle.firstContentMs, streamInactivityTimeoutMs: sharedStreamPresets.gentle.inactivityMs, streamToolCallIdleTimeoutMs: sharedStreamPresets.gentle.toolCallIdleMs },
    { key: 'balanced', labelKey: 'dialog.circuitBreaker.presetBalanced' as const, windowSize: 20, failureThreshold: 0.70, consecutiveFailuresThreshold: 5, requestTimeoutMs: 300000, responseHeaderTimeoutMs: 120000, streamFirstContentTimeoutMs: sharedStreamPresets.balanced.firstContentMs, streamInactivityTimeoutMs: sharedStreamPresets.balanced.inactivityMs, streamToolCallIdleTimeoutMs: sharedStreamPresets.balanced.toolCallIdleMs },
    { key: 'aggressive', labelKey: 'dialog.circuitBreaker.presetAggressive' as const, windowSize: 10, failureThreshold: 0.50, consecutiveFailuresThreshold: 3, requestTimeoutMs: 120000, responseHeaderTimeoutMs: 60000, streamFirstContentTimeoutMs: sharedStreamPresets.aggressive.firstContentMs, streamInactivityTimeoutMs: sharedStreamPresets.aggressive.inactivityMs, streamToolCallIdleTimeoutMs: sharedStreamPresets.aggressive.toolCallIdleMs },
    { key: 'custom', labelKey: 'dialog.circuitBreaker.presetCustom' as const, windowSize: 20, failureThreshold: 0.70, consecutiveFailuresThreshold: 5, requestTimeoutMs: 300000, responseHeaderTimeoutMs: 120000, streamFirstContentTimeoutMs: sharedStreamPresets.balanced.firstContentMs, streamInactivityTimeoutMs: sharedStreamPresets.balanced.inactivityMs, streamToolCallIdleTimeoutMs: sharedStreamPresets.balanced.toolCallIdleMs },
  ]

  const matchPreset = () => {
    for (const p of cbPresets) {
      if (p.key === 'custom') continue
      if (cbForm.windowSize === p.windowSize && cbForm.failureThreshold === p.failureThreshold && cbForm.consecutiveFailuresThreshold === p.consecutiveFailuresThreshold && cbForm.requestTimeoutMs === p.requestTimeoutMs && cbForm.responseHeaderTimeoutMs === p.responseHeaderTimeoutMs && cbForm.streamFirstContentTimeoutMs === p.streamFirstContentTimeoutMs && cbForm.streamInactivityTimeoutMs === p.streamInactivityTimeoutMs && cbForm.streamToolCallIdleTimeoutMs === p.streamToolCallIdleTimeoutMs) {
        activePreset.value = p.key
        return
      }
    }
    activePreset.value = 'custom'
  }

  const applyPreset = (preset: typeof cbPresets[number]) => {
    if (preset.key === 'custom') return
    cbForm.windowSize = preset.windowSize
    cbForm.failureThreshold = preset.failureThreshold
    cbForm.consecutiveFailuresThreshold = preset.consecutiveFailuresThreshold
    cbForm.requestTimeoutMs = preset.requestTimeoutMs
    cbForm.responseHeaderTimeoutMs = preset.responseHeaderTimeoutMs
    cbForm.streamFirstContentTimeoutMs = preset.streamFirstContentTimeoutMs
    cbForm.streamInactivityTimeoutMs = preset.streamInactivityTimeoutMs
    cbForm.streamToolCallIdleTimeoutMs = preset.streamToolCallIdleTimeoutMs
    activePreset.value = preset.key
  }

  const onSliderChange = (field: string, event: Event) => {
    const target = event.target
    if (!(target instanceof window.HTMLInputElement)) return
    const val = Number(target.value)
    if (field === 'failureThreshold') {
      cbForm.failureThreshold = Math.round(val * 100) / 100
    } else if (field === 'windowSize') {
      cbForm.windowSize = val
    } else if (field === 'consecutiveFailuresThreshold') {
      cbForm.consecutiveFailuresThreshold = val
    } else if (field === 'requestTimeoutMs') {
      cbForm.requestTimeoutMs = val
    } else if (field === 'responseHeaderTimeoutMs') {
      cbForm.responseHeaderTimeoutMs = val
    } else if (field === 'streamFirstContentTimeoutMs') {
      cbForm.streamFirstContentTimeoutMs = val
    } else if (field === 'streamInactivityTimeoutMs') {
      cbForm.streamInactivityTimeoutMs = val
    } else if (field === 'streamToolCallIdleTimeoutMs') {
      cbForm.streamToolCallIdleTimeoutMs = val
    }
    matchPreset()
  }

  const openCircuitBreakerDialog = async () => {
    try {
      const params = await api.getCircuitBreaker()
      cbForm.windowSize = params.windowSize
      cbForm.failureThreshold = params.failureThreshold
      cbForm.consecutiveFailuresThreshold = params.consecutiveFailuresThreshold
      cbForm.requestTimeoutMs = params.requestTimeoutMs && params.requestTimeoutMs >= 1000 ? params.requestTimeoutMs : 300000
      cbForm.responseHeaderTimeoutMs = params.responseHeaderTimeoutMs && params.responseHeaderTimeoutMs >= 1000 ? params.responseHeaderTimeoutMs : 120000
      cbForm.streamFirstContentTimeoutMs = params.streamFirstContentTimeoutMs && params.streamFirstContentTimeoutMs >= 5000 ? params.streamFirstContentTimeoutMs : 90000
      cbForm.streamInactivityTimeoutMs = params.streamInactivityTimeoutMs && params.streamInactivityTimeoutMs >= 1000 ? params.streamInactivityTimeoutMs : 90000
      cbForm.streamToolCallIdleTimeoutMs = params.streamToolCallIdleTimeoutMs && params.streamToolCallIdleTimeoutMs >= 30000 ? params.streamToolCallIdleTimeoutMs : 300000
      matchPreset()
    } catch (e) {
      console.error('Failed to load circuit breaker config:', e)
    }
    circuitBreakerDialogOpen.value = true
  }

  const saveCircuitBreaker = async () => {
    cbSaving.value = true
    try {
      await api.setCircuitBreaker({
        windowSize: cbForm.windowSize,
        failureThreshold: cbForm.failureThreshold,
        consecutiveFailuresThreshold: cbForm.consecutiveFailuresThreshold,
        requestTimeoutMs: cbForm.requestTimeoutMs,
        responseHeaderTimeoutMs: cbForm.responseHeaderTimeoutMs,
        streamFirstContentTimeoutMs: cbForm.streamFirstContentTimeoutMs,
        streamInactivityTimeoutMs: cbForm.streamInactivityTimeoutMs,
        streamToolCallIdleTimeoutMs: cbForm.streamToolCallIdleTimeoutMs,
      })
      circuitBreakerDialogOpen.value = false
      showToast(t('toast.circuitBreakerSaved'), 'success')
    } catch (e) {
      showToast(t('toast.circuitBreakerFailed', { message: e instanceof Error ? e.message : t('system.unknown') }), 'error')
    } finally {
      cbSaving.value = false
    }
  }

  // 平台检测
  const isMac = computed(() => typeof navigator !== 'undefined' && /Mac|iPod|iPhone|iPad/.test(navigator.platform))

  // 调校台弹窗键盘快捷键
  const handleCircuitBreakerKeydown = (event: KeyboardEvent) => {
    if (!circuitBreakerDialogOpen.value) return

    if (event.key === 'Escape') {
      event.preventDefault()
      circuitBreakerDialogOpen.value = false
      return
    }

    // Cmd/Ctrl+Enter 确认提交
    if (event.key === 'Enter' && (event.metaKey || event.ctrlKey) && !event.shiftKey) {
      event.preventDefault()
      saveCircuitBreaker()
    }
  }

  // 添加API密钥弹窗键盘快捷键
  const handleAddKeyKeydown = (event: KeyboardEvent) => {
    if (!dialogStore.showAddKeyModal) return

    if (event.key === 'Escape') {
      event.preventDefault()
      dialogStore.closeAddKeyModal()
      return
    }

    // Enter 确认添加
    if (event.key === 'Enter' && !event.metaKey && !event.ctrlKey && !event.shiftKey) {
      event.preventDefault()
      addApiKey()
    }
  }

  // 通用确认弹窗键盘快捷键
  const handleConfirmKeydown = (event: KeyboardEvent) => {
    if (!dialogStore.showConfirmDialog) return

    if (event.key === 'Escape') {
      event.preventDefault()
      dialogStore.resolveConfirm(false)
      return
    }

    // Cmd/Ctrl+Enter 确认
    if (event.key === 'Enter' && (event.metaKey || event.ctrlKey) && !event.shiftKey) {
      event.preventDefault()
      dialogStore.resolveConfirm(true)
    }
  }

  // 主题管理
  const toggleDarkMode = () => {
    const newMode = preferencesStore.darkModePreference === 'dark' ? 'light' : 'dark'
    setDarkMode(newMode)
  }

  const setDarkMode = (themeName: 'light' | 'dark' | 'auto') => {
    preferencesStore.setDarkMode(themeName)
    const apply = (isDark: boolean) => {
      // 使用 Vuetify 3.9+ 推荐的 theme.change() API
      theme.change(isDark ? 'dark' : 'light')
    }

    if (themeName === 'auto') {
      const prefersDark = window.matchMedia('(prefers-color-scheme: dark)').matches
      apply(prefersDark)
    } else {
      apply(themeName === 'dark')
    }
    // PreferencesStore 已通过 pinia-plugin-persistedstate 自动持久化，无需手动写入 localStorage
  }

  // 认证状态管理（使用 AuthStore）
  const isAuthenticated = computed(() => authStore.isAuthenticated)
  // 认证相关状态已迁移到 AuthStore

  // 认证尝试限制
  const MAX_AUTH_ATTEMPTS = 5

  const getAuthLockoutRemainingSeconds = () => {
    const lockoutTime = authStore.authLockoutTime
    if (!lockoutTime) return 0

    const remainingSeconds = Math.ceil((lockoutTime - Date.now()) / 1000)
    if (remainingSeconds <= 0) {
      authStore.setAuthLockout(null)
      authStore.resetAuthAttempts()
      return 0
    }
    return remainingSeconds
  }

  // 控制认证对话框显示
  const showAuthDialog = computed({
    get: () => {
      // 只有在初始化完成后，且未认证，且不在自动认证中时，才显示对话框
      return authStore.isInitialized && !isAuthenticated.value && !authStore.isAutoAuthenticating
    },
    set: () => {} // 防止外部修改，认证状态只能通过内部逻辑控制
  })

  // 自动验证保存的密钥
  const autoAuthenticate = async () => {
    // 检查 AuthStore 中是否有保存的密钥
    if (!authStore.apiKey) {
      // 没有保存的密钥，显示登录对话框
      authStore.setAuthError(t('toast.enterAccessKeyContinue'))
      authStore.setAutoAuthenticating(false)
      authStore.setInitialized(true)
      return false
    }

    // 有保存的密钥，尝试自动认证
    try {
      // 尝试调用API验证密钥是否有效
      await api.getChannels()

      // 密钥有效，认证成功
      authStore.setAuthError('')
      return true
    } catch (error) {
      // 仅在明确 401 时视为密钥无效；其他错误（网络/5xx）不应清除密钥
      if (error instanceof ApiError && error.status === 401) {
        console.warn('自动认证失败: 认证失败(401)')
        authStore.clearAuth()
        authStore.setAuthError(t('toast.savedKeyInvalid'))
        return false
      }

      console.warn('自动认证暂时失败:', error)
      showToast(t('toast.cannotVerifyAccessKey', { message: error instanceof Error ? error.message : t('system.unknown') }), 'warning')
      // 非 401：保留密钥，继续尝试连接后端（后续刷新会更新系统状态）
      return true
    } finally {
      authStore.setAutoAuthenticating(false)
      authStore.setInitialized(true)
    }
  }

  // 手动设置密钥（用于重新认证）
  const setAuthKey = (key: string) => {
    authStore.setApiKey(key)
    authStore.setAuthError('')
  }

  // 处理认证提交
  const submitAuth = async (options: { countFailures?: boolean; ignoreLockout?: boolean } = {}) => {
    const countFailures = options.countFailures ?? true
    const ignoreLockout = options.ignoreLockout ?? false

    if (!authStore.authKeyInput.trim()) {
      authStore.setAuthError(t('toast.enterAccessKey'))
      return
    }

    // 检查是否被锁定；过期锁定会自动清理，避免显示负数倒计时
    // 桌面端 iframe 自动注入密钥时忽略前端本地 lockout，但仍会验证 key 是否有效
    const remainingSeconds = ignoreLockout ? 0 : getAuthLockoutRemainingSeconds()
    if (remainingSeconds > 0) {
      authStore.setAuthError(t('toast.tooManyAttemptsSeconds', { seconds: remainingSeconds }))
      return
    }

    authStore.setAuthLoading(true)
    authStore.setAuthError('')

    try {
      // 设置密钥
      setAuthKey(authStore.authKeyInput.trim())

      // 测试API调用以验证密钥
      await api.getChannels()

      // 认证成功，重置计数器
      authStore.resetAuthAttempts()
      authStore.setAuthLockout(null)

      // 如果成功，加载数据
      await refreshChannels()
      // 手动登录成功后同步系统状态，避免状态卡停留在 Connecting
      systemStore.setSystemStatus(channelStore.lastRefreshSuccess ? 'running' : 'error')

      authStore.setAuthKeyInput('')

      // 记录认证成功(前端日志)
      if (import.meta.env.DEV) {
        console.info('✅ 认证成功 - 时间:', new Date().toISOString())
      }
    } catch (error) {
      // 仅在明确 401 时计入认证失败；网络/5xx 不计入失败次数，也不清除已保存密钥
      if (error instanceof ApiError && error.status === 401) {
        if (countFailures) {
          authStore.incrementAuthAttempts()

          // 记录认证失败(前端日志)
          console.warn('🔒 认证失败 - 尝试次数:', authStore.authAttempts, '时间:', new Date().toISOString())

          // 如果尝试次数过多，锁定5分钟
          if (authStore.authAttempts >= MAX_AUTH_ATTEMPTS) {
            authStore.setAuthLockout(new Date(Date.now() + 5 * 60 * 1000))
            authStore.setAuthError(t('toast.tooManyAttempts'))
          } else {
            authStore.setAuthError(t('toast.accessKeyInvalidRemaining', { remaining: MAX_AUTH_ATTEMPTS - authStore.authAttempts }))
          }
        } else {
          // 桌面端 iframe 自动注入密钥时不消耗手动登录尝试次数，避免重复 postMessage 触发本地锁定
          authStore.setAuthError(t('toast.authInvalid'))
        }

        authStore.clearAuth()
        return
      }

      showToast(t('toast.cannotVerifyAccessKey', { message: error instanceof Error ? error.message : t('system.unknown') }), 'error')
    } finally {
      authStore.setAuthLoading(false)
    }
  }

  const handleAuthSubmit = async () => {
    await submitAuth()
  }

  // 处理注销
  const handleLogout = () => {
    authStore.clearAuth()
    channelStore.clearChannels()
    authStore.setAuthError(t('toast.enterAccessKeyContinue'))
    showToast(t('toast.loggedOut'), 'info')
  }

  // 处理认证失败
  const handleAuthError = (error: unknown) => {
    if (error instanceof ApiError && error.status === 401) {
      authStore.setAuthError(t('toast.authInvalid'))
    } else {
      showToast(t('toast.operationFailed', { message: error instanceof Error ? error.message : t('system.unknown') }), 'error')
    }
  }

  // 版本检查
  const checkVersion = async () => {
    if (isDesktopWebUI || systemStore.isCheckingVersion) return

    systemStore.setCheckingVersion(true)
    try {
      // 直接通过 health 接口获取当前版本，再从 GitHub 检查是否有新版本
      const health = await fetchHealth()
      const currentVersion = health.version?.version || ''

      if (currentVersion) {
        versionService.setCurrentVersion(currentVersion)
        systemStore.setCurrentVersion(currentVersion)

        const result = await versionService.checkForUpdates()
        systemStore.setVersionInfo(result)
      } else {
        systemStore.setVersionInfo({
          currentVersion: systemStore.versionInfo.currentVersion,
          latestVersion: null,
          isLatest: false,
          hasUpdate: false,
          releaseUrl: null,
          lastCheckTime: 0,
          status: 'error',
        })
      }
    } catch (error) {
      console.warn('Version check failed:', error)
      systemStore.setVersionInfo({
        currentVersion: systemStore.versionInfo.currentVersion,
        latestVersion: null,
        isLatest: false,
        hasUpdate: false,
        releaseUrl: null,
        lastCheckTime: 0,
        status: 'error',
      })
    } finally {
      systemStore.setCheckingVersion(false)
    }
  }

  // 版本点击处理
  const handleVersionClick = () => {
    systemStore.setUpdateDialogOpen(true)
  }

  // 监听系统主题变化（setup 阶段注册，onUnmounted 清理，避免泄漏）
  // 守卫非浏览器环境（SSR / vitest 非 jsdom）：避免 ReferenceError: window is not defined
  const mediaQuery = typeof window !== 'undefined' && typeof window.matchMedia === 'function'
    ? window.matchMedia('(prefers-color-scheme: dark)')
    : null
  const handlePref = () => {
    if (preferencesStore.darkModePreference === 'auto') setDarkMode('auto')
  }
  mediaQuery?.addEventListener('change', handlePref)

  // 注册弹窗键盘快捷键（setup 阶段注册，onUnmounted 清理）
  if (typeof window !== 'undefined') {
    window.addEventListener('keydown', handleCircuitBreakerKeydown)
    window.addEventListener('keydown', handleAddKeyKeydown)
    window.addEventListener('keydown', handleConfirmKeydown)
  }

  // 初始化
  onMounted(async () => {
    // 初始化复古像素主题
    document.documentElement.dataset.theme = 'retro'
    initTheme()

    // 加载保存的暗色模式偏好（从 PreferencesStore 读取，已自动从 localStorage 恢复）
    setDarkMode(preferencesStore.darkModePreference)

    if (!isDesktopWebUI) {
      // 版本检查（独立于认证，静默执行）
      checkVersion()

      // 监听 UpdateDialog 手动触发的版本检查
      window.addEventListener('ccx-check-version', () => { checkVersion() })
    }

    const desktopAutoLogin = window.self !== window.top && isDesktopWebUI

    if (desktopAutoLogin) {
      authStore.clearAuth()
      authStore.setAutoAuthenticating(false)
      authStore.setInitialized(true)
      authStore.setAuthError(t('toast.enterAccessKeyContinue'))

      // 桌面端通过 postMessage 发送密钥，监听直到认证成功
      const handleDesktopAuth = async (event: MessageEvent) => {
        const data = event.data as { type?: string; accessKey?: string }
        if (data?.type !== 'ccx-desktop-auth' || !data.accessKey) return

        authStore.setAuthKeyInput(data.accessKey)
        await submitAuth({ countFailures: false, ignoreLockout: true })
        // 认证成功后移除监听器；失败时保留以便桌面端重试
        if (authStore.apiKey) {
          window.removeEventListener('message', handleDesktopAuth)
        }
      }

      window.addEventListener('message', handleDesktopAuth)
      return
    }

    // 桌面端嵌入但 ccx_desktop 参数缺失时，也注册 postMessage 监听器作为后备
    if (window.self !== window.top) {
      const handleDesktopAuthFallback = async (event: MessageEvent) => {
        const data = event.data as { type?: string; accessKey?: string }
        if (data?.type !== 'ccx-desktop-auth' || !data.accessKey) return

        window.removeEventListener('message', handleDesktopAuthFallback)
        authStore.setAuthKeyInput(data.accessKey)
        await submitAuth({ countFailures: false, ignoreLockout: true })
      }
      window.addEventListener('message', handleDesktopAuthFallback)
    }

    // 检查 AuthStore 中是否有保存的密钥
    if (authStore.apiKey) {
      // 有保存的密钥，开始自动认证
      authStore.setAutoAuthenticating(true)
      authStore.setInitialized(false)
    } else {
      // 没有保存的密钥，直接显示登录对话框
      authStore.setAutoAuthenticating(false)
      authStore.setInitialized(true)
    }

    // 尝试自动认证
    const authenticated = await autoAuthenticate()

    if (authenticated) {
      // 加载渠道数据
      await refreshChannels()
      // 启动自动刷新
      startAutoRefresh()
      // 初始化完成后根据最新刷新结果设置系统状态
      systemStore.setSystemStatus(channelStore.lastRefreshSuccess ? 'running' : 'error')
    }
  })

  // 启动自动刷新定时器
  const startAutoRefresh = () => {
    channelStore.startAutoRefresh()
  }

  // 停止自动刷新定时器
  const stopAutoRefresh = () => {
    channelStore.stopAutoRefresh()
  }

  // 监听 Tab 切换，刷新对应数据
  watch(() => channelStore.activeTab, async () => {
    if (isAuthenticated.value) {
      try {
        await channelStore.refreshChannels()
      } catch (error) {
        console.error('切换 Tab 刷新失败:', error)
      }
    }
  })

  // 监听认证状态变化
  watch(isAuthenticated, newValue => {
    if (newValue) {
      startAutoRefresh()
    } else {
      stopAutoRefresh()
    }
  })

  // 监听自动刷新状态，更新 systemStatus
  watch(() => channelStore.lastRefreshSuccess, (success) => {
    if (isAuthenticated.value) {
      systemStore.setSystemStatus(success ? 'running' : 'error')
    }
  })

  // 在组件卸载时清除定时器和事件监听器
  onUnmounted(() => {
    channelStore.stopAutoRefresh()
    mediaQuery?.removeEventListener('change', handlePref)
    if (typeof window !== 'undefined') {
      window.removeEventListener('keydown', handleCircuitBreakerKeydown)
      window.removeEventListener('keydown', handleAddKeyKeydown)
      window.removeEventListener('keydown', handleConfirmKeydown)
    }
  })

  return {
    route,
    theme,
    authStore,
    channelStore,
    preferencesStore,
    dialogStore,
    systemStore,
    t,
    setLocale,
    languageOptions,
    currentLocale,
    currentLanguageShortLabel,
    translatedApiTabOptions,
    editingChannelType,
    isDesktopWebUI,
    activeTrafficTitle,
    systemStatusText,
    systemStatusDesc,
    toasts,
    getToastColor,
    getToastIcon,
    showToast,
    showErrorToast,
    showSuccessToast,
    refreshChannels,
    saveChannel,
    handleAutoAddedChannel,
    editChannel,
    deleteChannel,
    openAddChannelModal,
    addApiKey,
    showGuide,
    openGuide,
    circuitBreakerDialogOpen,
    cbSaving,
    activePreset,
    cbForm,
    cbPresets,
    applyPreset,
    onSliderChange,
    openCircuitBreakerDialog,
    saveCircuitBreaker,
    isMac,
    toggleDarkMode,
    isAuthenticated,
    MAX_AUTH_ATTEMPTS,
    showAuthDialog,
    handleAuthSubmit,
    handleLogout,
    handleVersionClick,
  }
}
