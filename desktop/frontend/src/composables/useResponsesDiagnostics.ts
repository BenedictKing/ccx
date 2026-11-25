import { computed, ref } from 'vue'
import { useAgentConfig } from '@/composables/useAgentConfig'
import { useLanguage } from '@/composables/useLanguage'
import { useAdminApi } from '@/composables/useAdminApi'
import { useStatus } from '@/composables/useStatus'
import type { ChannelDashboardResponse, Channel, ChannelLogsResponse, ChannelLogEntry } from '@/services/admin-api'

const diagnosticMessageKeyMap: Record<string, Parameters<ReturnType<typeof useLanguage>['t']>[0]> = {
  'codex.missing_api_key': 'agent.codexDiagnosticCodeMissingApiKey',
  'codex.proxy_key_mismatch': 'agent.codexDiagnosticCodeProxyKeyMismatch',
  'codex.auth_mode_mismatch': 'agent.codexDiagnosticCodeAuthModeMismatch',
  'codex.plugin_missing_bearer': 'agent.codexDiagnosticCodePluginMissingBearer',
  'codex.legacy_incomplete': 'agent.codexDiagnosticCodeLegacyIncomplete',
  'codex.config_polluted': 'agent.codexDiagnosticCodeConfigPolluted',
  'codex.auth_unreadable': 'agent.codexDiagnosticCodeAuthUnreadable',
}

// 模块级共享状态：同一页面内多次使用 composable 时复用排障缓存。
const responsesChannels = ref<Channel[]>([])
const responsesMetrics = ref<ChannelDashboardResponse['metrics']>([])
const responsesChannelsLoaded = ref(false)
const responsesChannelError = ref('')
const recentFailedLogs = ref<ChannelLogEntry[]>([])
const recentFailedLogsLoaded = ref(false)
const recentFailedLogsError = ref('')
const codexTroubleshootingRequested = ref(false)
const codexTroubleshootingLoading = ref(false)
let refreshPromise: Promise<void> | null = null

export function useResponsesDiagnostics() {
  const { agentStatuses, codexMode } = useAgentConfig()
  const { status } = useStatus()
  const { t } = useLanguage()
  const api = useAdminApi()

  const codexStatus = computed(() => agentStatuses.value.codex)
  const codexUsesLocalGateway = computed(() => codexStatus.value?.provider === 'ccx')

  async function refreshRecentFailedLogs() {
    const activeChannels = responsesChannels.value.filter(channel => channel.status === 'active' || channel.status === '' || channel.status === undefined)
    if (activeChannels.length === 0) {
      recentFailedLogs.value = []
      recentFailedLogsLoaded.value = true
      recentFailedLogsError.value = ''
      return
    }

    try {
      const logResults = await Promise.all(
        activeChannels.map(async (channel) => {
          const response = await api.get<ChannelLogsResponse>(`/api/responses/channels/${channel.index}/logs`)
          return Array.isArray(response.logs) ? response.logs : []
        }),
      )
      recentFailedLogs.value = logResults
        .flat()
        .filter((log) => {
          const status = String(log.status || '').toLowerCase()
          const errorInfo = String(log.errorInfo || '').toLowerCase().trim()
          const isClientCancelled = status === 'cancelled' || status === 'canceled' || errorInfo === 'client canceled'
          if (isClientCancelled) return false

          const isTerminalFailure = status === 'failed'
          return isTerminalFailure || log.statusCode >= 400 || !!errorInfo
        })
        .sort((a, b) => String(b.timestamp).localeCompare(String(a.timestamp)))
        .slice(0, 10)
      recentFailedLogsLoaded.value = true
      recentFailedLogsError.value = ''
    } catch (error) {
      recentFailedLogs.value = []
      recentFailedLogsLoaded.value = true
      recentFailedLogsError.value = error instanceof Error ? error.message : String(error)
    }
  }

  async function refreshResponsesChannels() {
    if (refreshPromise) return refreshPromise

    refreshPromise = (async () => {
      if (!status.value.running) {
        responsesChannels.value = []
        responsesMetrics.value = []
        responsesChannelsLoaded.value = false
        responsesChannelError.value = ''
        recentFailedLogs.value = []
        recentFailedLogsLoaded.value = false
        recentFailedLogsError.value = ''
        return
      }
      try {
        const dashboard = await api.get<ChannelDashboardResponse>('/api/messages/channels/dashboard?type=responses')
        responsesChannels.value = Array.isArray(dashboard.channels) ? dashboard.channels : []
        responsesMetrics.value = Array.isArray(dashboard.metrics) ? dashboard.metrics : []
        responsesChannelsLoaded.value = true
        responsesChannelError.value = ''
        await refreshRecentFailedLogs()
      } catch (error) {
        responsesChannelError.value = error instanceof Error ? error.message : String(error)
        responsesChannels.value = []
        responsesMetrics.value = []
        responsesChannelsLoaded.value = true
        recentFailedLogs.value = []
        recentFailedLogsLoaded.value = false
        recentFailedLogsError.value = ''
      }
    })()

    try {
      await refreshPromise
    } finally {
      refreshPromise = null
    }
  }

  async function runCodexTroubleshooting() {
    if (codexTroubleshootingLoading.value) return

    codexTroubleshootingRequested.value = true
    codexTroubleshootingLoading.value = true
    try {
      if (codexUsesLocalGateway.value) {
        await refreshResponsesChannels()
      } else {
        responsesChannels.value = []
        responsesMetrics.value = []
        responsesChannelsLoaded.value = false
        responsesChannelError.value = ''
        recentFailedLogs.value = []
        recentFailedLogsLoaded.value = false
        recentFailedLogsError.value = ''
      }
    } finally {
      codexTroubleshootingLoading.value = false
    }
  }

  const codexDiagnosticVisible = computed(() => {
    return codexTroubleshootingRequested.value && !!codexStatus.value
  })

  // 磁盘上实际的模式：plugin 或 quick（新式 openai_base_url 的 status.mode 为空串，视为 quick）
  const currentCodexMode = computed<'quick' | 'plugin'>(() =>
    codexStatus.value?.mode === 'plugin' ? 'plugin' : 'quick',
  )

  // 模式不一致：磁盘配置和 UI 选择的模式不匹配
  const codexModeMismatch = computed(() => {
    const status = codexStatus.value
    if (!status) return false
    // 仅当 provider 是 CCX 或支持模式切换的第三方时检查
    const provider = status.provider
    const hasMode = provider === 'ccx' || provider === 'dashscope'
      || provider === 'opencode-zen' || provider === 'opencode-go' || provider === 'xfyun'
    if (!hasMode) return false
    return currentCodexMode.value !== codexMode.value
  })

  const modeLabel = (mode: 'quick' | 'plugin') => mode === 'plugin' ? t('agent.codexPluginMode') : t('agent.codexQuickMode')

  const codexDiagnosticSeverity = computed<'ok' | 'warn'>(() => {
    const current = codexStatus.value
    if (codexModeMismatch.value) return 'warn'
    if (!current || current.configConsistent !== false) return 'ok'
    return 'warn'
  })

  const codexDiagnosticSummary = computed(() => {
    const current = codexStatus.value
    if (!current) return ''
    // 模式不一致优先于文件自洽性检查
    if (codexModeMismatch.value) {
      return t('agent.codexDiagnosticCodeModeMismatch', {
        currentMode: modeLabel(currentCodexMode.value),
        targetMode: modeLabel(codexMode.value),
      })
    }
    if (current.configConsistent === false) {
      const key = current.diagnosticCode ? diagnosticMessageKeyMap[current.diagnosticCode] : undefined
      if (key) {
        return t(key)
      }
      return current.diagnosticMessage || t('agent.codexDiagnosticMismatch')
    }
    return t('agent.codexDiagnosticHealthy')
  })

  const codexDiagnosticSuggestions = computed(() => {
    const current = codexStatus.value
    // mode mismatch 时给出应用建议
    if (codexModeMismatch.value) {
      return [t('agent.codexDiagnosticSuggestedApply')]
    }
    if (!current || current.configConsistent !== false) return [] as string[]

    const suggestions = [t('agent.codexDiagnosticSuggestedApply')]
    if (current.hasState) {
      suggestions.push(t('agent.codexDiagnosticSuggestedRestore'))
    }
    return suggestions
  })

  const responsesChannelDiagnosticVisible = computed(() => {
    return codexTroubleshootingRequested.value && codexUsesLocalGateway.value && status.value.running && responsesChannelsLoaded.value
  })

  const responsesMetricByIndex = (index: number) => responsesMetrics.value.find(metric => metric.channelIndex === index)

  const responsesChannelDiagnosticSummary = computed(() => {
    if (!responsesChannelsLoaded.value) return ''
    if (responsesChannelError.value) return responsesChannelError.value

    const channels = responsesChannels.value
    if (channels.length === 0) {
      return t('agent.responsesDiagnosticNoChannel')
    }

    const activeChannels = channels.filter(channel => channel.status === 'active' || channel.status === '' || channel.status === undefined)
    if (activeChannels.length === 0) {
      return t('agent.responsesDiagnosticAllDisabled')
    }

    const channelsWithoutKeys = activeChannels.filter(channel => !Array.isArray(channel.apiKeys) || channel.apiKeys.length === 0)
    if (channelsWithoutKeys.length === activeChannels.length) {
      return t('agent.responsesDiagnosticNoApiKeys')
    }

    const circuitOpenChannels = activeChannels.filter(channel => responsesMetricByIndex(channel.index)?.circuitState === 'open')
    if (circuitOpenChannels.length === activeChannels.length && activeChannels.length > 0) {
      return t('agent.responsesDiagnosticCircuitOpen')
    }

    return t('agent.responsesDiagnosticHealthy')
  })

  const responsesChannelDiagnosticSeverity = computed<'ok' | 'warn'>(() => {
    if (!responsesChannelsLoaded.value) return 'ok'
    if (responsesChannelError.value) return 'warn'

    const channels = responsesChannels.value
    if (channels.length === 0) return 'warn'

    const activeChannels = channels.filter(channel => channel.status === 'active' || channel.status === '' || channel.status === undefined)
    if (activeChannels.length === 0) return 'warn'
    if (activeChannels.every(channel => !Array.isArray(channel.apiKeys) || channel.apiKeys.length === 0)) return 'warn'
    if (activeChannels.length > 0 && activeChannels.every(channel => responsesMetricByIndex(channel.index)?.circuitState === 'open')) return 'warn'

    return 'ok'
  })

  const responsesChannelDiagnosticSuggestions = computed(() => {
    if (!responsesChannelsLoaded.value) return [] as string[]
    if (responsesChannelError.value) return [t('agent.responsesDiagnosticSuggestedRefresh')]

    const channels = responsesChannels.value
    if (channels.length === 0) {
      return [t('agent.responsesDiagnosticSuggestedCreate')]
    }

    const activeChannels = channels.filter(channel => channel.status === 'active' || channel.status === '' || channel.status === undefined)
    if (activeChannels.length === 0) {
      return [t('agent.responsesDiagnosticSuggestedEnable')]
    }

    if (activeChannels.every(channel => !Array.isArray(channel.apiKeys) || channel.apiKeys.length === 0)) {
      return [t('agent.responsesDiagnosticSuggestedAddKey')]
    }

    if (activeChannels.length > 0 && activeChannels.every(channel => responsesMetricByIndex(channel.index)?.circuitState === 'open')) {
      return [t('agent.responsesDiagnosticSuggestedResume')]
    }

    return [] as string[]
  })

  const recentFailedLogsDiagnosticVisible = computed(() => {
    return codexTroubleshootingRequested.value
      && codexUsesLocalGateway.value
      && status.value.running
      && recentFailedLogsLoaded.value
  })

  const recentFailedLogsDiagnosticSummary = computed(() => {
    if (!recentFailedLogsLoaded.value) return ''
    if (recentFailedLogsError.value) return recentFailedLogsError.value
    const latest = recentFailedLogs.value[0]
    if (!latest) return t('agent.responsesLogsDiagnosticHealthy')

    if (latest.statusCode === 401 || latest.statusCode === 403) {
      return t('agent.responsesLogsDiagnosticAuth')
    }
    if (latest.statusCode === 429) {
      return t('agent.responsesLogsDiagnosticRateLimit')
    }
    if (latest.statusCode >= 500) {
      return t('agent.responsesLogsDiagnosticUpstream5xx')
    }

    const errorInfo = String(latest.errorInfo || '').toLowerCase()
    if (errorInfo.includes('timeout') || errorInfo.includes('stalled')) {
      return t('agent.responsesLogsDiagnosticTimeout')
    }

    return t('agent.responsesLogsDiagnosticGeneric')
  })

  const recentFailedLogsDiagnosticSeverity = computed<'ok' | 'warn'>(() => {
    if (recentFailedLogsError.value) return 'warn'
    return recentFailedLogs.value.length > 0 ? 'warn' : 'ok'
  })

  const recentFailedLogsDiagnosticSuggestions = computed(() => {
    if (recentFailedLogsError.value) return [t('agent.responsesLogsDiagnosticSuggestedRefresh')]
    if (!recentFailedLogsLoaded.value || recentFailedLogs.value.length === 0) return [] as string[]

    const latest = recentFailedLogs.value[0]
    if (!latest) return [] as string[]

    if (latest.statusCode === 401 || latest.statusCode === 403) {
      return [t('agent.responsesLogsDiagnosticSuggestedAuth')]
    }
    if (latest.statusCode === 429) {
      return [t('agent.responsesLogsDiagnosticSuggestedRateLimit')]
    }
    if (latest.statusCode >= 500) {
      return [t('agent.responsesLogsDiagnosticSuggestedUpstream5xx')]
    }

    const errorInfo = String(latest.errorInfo || '').toLowerCase()
    if (errorInfo.includes('timeout') || errorInfo.includes('stalled')) {
      return [t('agent.responsesLogsDiagnosticSuggestedTimeout')]
    }

    return [t('agent.responsesLogsDiagnosticSuggestedGeneric')]
  })

  return {
    codexStatus,
    codexDiagnosticVisible,
    codexDiagnosticSeverity,
    codexDiagnosticSummary,
    codexDiagnosticSuggestions,
    responsesChannels,
    responsesChannelsLoaded,
    responsesChannelDiagnosticVisible,
    responsesChannelDiagnosticSeverity,
    responsesChannelDiagnosticSummary,
    responsesChannelDiagnosticSuggestions,
    recentFailedLogs,
    recentFailedLogsDiagnosticVisible,
    recentFailedLogsDiagnosticSeverity,
    recentFailedLogsDiagnosticSummary,
    recentFailedLogsDiagnosticSuggestions,
    codexTroubleshootingLoading,
    runCodexTroubleshooting,
  }
}
