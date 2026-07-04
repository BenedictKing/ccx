<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Alert } from '@/components/ui/alert'
import { Skeleton } from '@/components/ui/skeleton'
import { Plus, Search, Layers, Archive, Loader2, ShieldCheck, ShieldOff, Zap, ChevronDown, BarChart3, X } from 'lucide-vue-next'
import { useConsoleChannels } from '@/composables/useConsoleChannels'
import { useAdminApi } from '@/composables/useAdminApi'
import { useDesktopActivity } from '@/composables/useDesktopActivity'
import { useStatus } from '@/composables/useStatus'
import { useLanguage } from '@/composables/useLanguage'
import ChannelCard from '@/components/console/ChannelCard.vue'
import ChannelEditDialog from '@/components/console/ChannelEditDialog.vue'
import ChannelLogsDialog from '@/components/console/ChannelLogsDialog.vue'
import CapabilityTestDialog from '@/components/console/CapabilityTestDialog.vue'
import CircuitBreakerDialog from '@/components/console/CircuitBreakerDialog.vue'
import GlobalStatsChart from '@/components/console/charts/GlobalStatsChart.vue'
import KeyTrendChart from '@/components/console/charts/KeyTrendChart.vue'
import type { ManagedChannelType } from '@/utils/channel-type-api'
import { selectDenseSamplingInterval } from '@/utils/chart-sampling'
import type { Channel, ChannelMetrics, ChannelRecentActivity, GlobalStatsHistoryResponse, KeyHistoryData, GlobalStatsSummary } from '@/services/admin-api'

interface Props {
  type: ManagedChannelType
}

const props = defineProps<Props>()

const { t } = useLanguage()
const { status } = useStatus()
const { isConsoleChannelsActive } = useDesktopActivity()
const {
  activeTab,
  channelsByType,
  dashboardCache,
  refreshChannels,
  deleteChannel,
  setChannelStatus,
  resumeChannel,
  promoteChannel,
  reorderChannels,
} = useConsoleChannels()

watch(() => props.type, (newType) => {
  activeTab.value = newType
}, { immediate: true })

const channels = computed(() => channelsByType.value[props.type]?.channels || [])
const metrics = computed(() => dashboardCache.value[props.type]?.metrics || [])
const activity = computed(() => dashboardCache.value[props.type]?.recentActivity || [])
const stats = computed(() => dashboardCache.value[props.type]?.stats)
const supportsCapability = computed(() => props.type !== 'images' && props.type !== 'vectors')

const searchQuery = ref('')
const normalizedSearch = computed(() => searchQuery.value.trim().toLowerCase())

const orderedActiveChannels = computed(() => {
  const list = channels.value.filter(ch => ch.status !== 'disabled')
  return sortChannels(list)
})

const activeChannels = computed(() => orderedActiveChannels.value.filter(matchesSearch))

const inactiveChannels = computed(() => {
  const list = channels.value.filter(ch => ch.status === 'disabled')
  return sortChannels(list).filter(matchesSearch)
})

const activeCount = computed(() => channels.value.filter(ch => ch.status === 'active' || ch.status === undefined || ch.status === '').length)
const failoverCount = computed(() => channels.value.filter(ch => ch.status !== 'disabled').length)
const disabledCount = computed(() => channels.value.filter(ch => ch.status === 'disabled').length)

const metricsMap = computed(() => {
  const map = new Map<number, ChannelMetrics>()
  for (const m of metrics.value) {
    map.set(m.channelIndex, m)
  }
  return map
})

const activityMap = computed(() => {
  const map = new Map<number, ChannelRecentActivity>()
  for (const a of activity.value) {
    map.set(a.channelIndex, a)
  }
  return map
})

const hasLoaded = computed(() => Boolean(stats.value) || metrics.value.length > 0 || channels.value.length > 0)
const isInitialLoading = computed(() => !hasLoaded.value && !actionError.value)
const actionLoading = ref(false)
const actionError = ref('')
const isRefreshing = ref(false)
const showChannelEditor = ref(false)
const editingChannel = ref<Channel | null>(null)
const logsChannel = ref<Channel | null>(null)
const capabilityChannel = ref<Channel | null>(null)
const showCapabilityDialog = ref(false)
const draggedIndex = ref<number | null>(null)

// Fuzzy 模式
const fuzzyEnabled = ref(false)
const fuzzyLoading = ref(false)
const fuzzyLoadError = ref(false)
const showCbDialog = ref(false)

// 用量统计
const globalStatsChartRef = ref<InstanceType<typeof GlobalStatsChart> | null>(null)
const statsLoading = ref(false)
const showGlobalStats = ref(false)
const globalStatsDuration = ref('6h')
let isMounted = true
let globalStatsRequestSeq = 0

// 渠道级 Key 趋势图
const expandedChannelId = ref<number | null>(null)
const keyMetricsDuration = ref('1h')
const keyMetricsData = ref<KeyHistoryData[]>([])
const keyMetricsSummary = ref<GlobalStatsSummary | null>(null)
const keyMetricsLoading = ref(false)
const globalStatsAdaptiveInterval = ref<{ key: string; interval: string } | null>(null)
const keyMetricsAdaptiveInterval = ref<{ key: string; interval: string } | null>(null)
const globalStatsChartInterval = computed(() => {
  const interval = globalStatsAdaptiveInterval.value
  return interval?.key === `${props.type}:${globalStatsDuration.value}` ? interval.interval : undefined
})
const keyMetricsChartInterval = computed(() => {
  if (expandedChannelId.value === null) return undefined
  const interval = keyMetricsAdaptiveInterval.value
  return interval?.key === `${props.type}:${expandedChannelId.value}:${keyMetricsDuration.value}` ? interval.interval : undefined
})

watch(() => props.type, () => {
  globalStatsDuration.value = '6h'
  globalStatsAdaptiveInterval.value = null
  keyMetricsAdaptiveInterval.value = null
  keyMetricsData.value = []
  keyMetricsSummary.value = null
  expandedChannelId.value = null
})

type ChartDuration = '1h' | '6h' | '24h' | 'today' | '7d' | '30d' | '90d' | '180d' | '365d' | 'thisyear'

function isChartDuration(duration: string): duration is ChartDuration {
  return ['1h', '6h', '24h', 'today', '7d', '30d', '90d', '180d', '365d', 'thisyear'].includes(duration)
}

function historyQuery(duration: string, interval?: string): string {
  const params = new URLSearchParams({ duration })
  if (interval) {
    params.set('interval', interval)
  }
  return params.toString()
}

function apiPathForType(type: ManagedChannelType): string {
  const typeMap: Record<ManagedChannelType, string> = {
    messages: 'messages',
    chat: 'chat',
    responses: 'responses',
    gemini: 'gemini',
    images: 'images',
    vectors: 'vectors',
  }
  return typeMap[type]
}

function shouldLoadGlobalStats() {
  return status.value.running && isConsoleChannelsActive.value && showGlobalStats.value
}

function canApplyGlobalStatsRequest(seq: number, requestType: ManagedChannelType, duration: string) {
  return (
    isMounted &&
    seq === globalStatsRequestSeq &&
    showGlobalStats.value &&
    props.type === requestType &&
    globalStatsDuration.value === duration
  )
}

async function loadGlobalStats(duration?: string) {
  if (!showGlobalStats.value) return
  const requestSeq = ++globalStatsRequestSeq
  statsLoading.value = true
  globalStatsChartRef.value?.setLoading(true)
  try {
    const adminApi = useAdminApi()
    const requestType = props.type
    const apiPath = apiPathForType(requestType)
    const dur = duration || '6h'
    if (globalStatsDuration.value !== dur) {
      globalStatsDuration.value = dur
      globalStatsAdaptiveInterval.value = null
    }
    const samplingKey = `${requestType}:${dur}`
    const interval = globalStatsAdaptiveInterval.value?.key === samplingKey
      ? globalStatsAdaptiveInterval.value.interval
      : undefined
    let data = await adminApi.get<GlobalStatsHistoryResponse>(
      `/api/${apiPath}/global/stats/history?${historyQuery(dur, interval)}`
    )
    if (!interval && isChartDuration(dur)) {
      const denseInterval = selectDenseSamplingInterval(
        dur,
        data.dataPoints || [],
        dp => dp.requestCount > 0 || dp.inputTokens > 0 || dp.outputTokens > 0 || dp.cacheReadTokens > 0 || dp.cacheCreationTokens > 0,
        data.summary?.intervalSeconds
      )
      if (denseInterval) {
        globalStatsAdaptiveInterval.value = { key: samplingKey, interval: denseInterval }
        data = await adminApi.get<GlobalStatsHistoryResponse>(
          `/api/${apiPath}/global/stats/history?${historyQuery(dur, denseInterval)}`
        )
      }
    }
    if (!canApplyGlobalStatsRequest(requestSeq, requestType, dur)) return
    globalStatsChartRef.value?.updateData(data.dataPoints, data.summary, data.modelDataPoints)
  } catch {
    // Silently fail
  } finally {
    if (requestSeq === globalStatsRequestSeq) {
      statsLoading.value = false
      if (isMounted && showGlobalStats.value) {
        globalStatsChartRef.value?.setLoading(false)
      }
    }
  }
}

async function loadKeyMetrics(channelId: number, duration?: string) {
  keyMetricsLoading.value = true
  try {
    const adminApi = useAdminApi()
    const requestType = props.type
    const apiPath = apiPathForType(requestType)
    const dur = duration || keyMetricsDuration.value
    // 只在 duration 真正变化时更新 ref，避免触发子组件 KeyTrendChart 的 props.duration watcher
    // 产生 emit refresh → handleKeyMetricsRefresh → loadKeyMetrics 的 double-fetch 链式反应
    if (keyMetricsDuration.value !== dur) {
      keyMetricsAdaptiveInterval.value = null
      keyMetricsDuration.value = dur
    }
    const samplingKey = `${requestType}:${channelId}:${dur}`
    const interval = keyMetricsAdaptiveInterval.value?.key === samplingKey
      ? keyMetricsAdaptiveInterval.value.interval
      : undefined
    let data = await adminApi.get<{ keys: KeyHistoryData[]; summary?: GlobalStatsSummary }>(
      `/api/${apiPath}/channels/${channelId}/keys/metrics/history?${historyQuery(dur, interval)}`
    )
    if (!interval && isChartDuration(dur)) {
      const points = (data.keys || []).flatMap(keyData => keyData.dataPoints || [])
      const denseInterval = selectDenseSamplingInterval(
        dur,
        points,
        dp => dp.requestCount > 0 || dp.inputTokens > 0 || dp.outputTokens > 0 || dp.cacheReadTokens > 0 || dp.cacheCreationTokens > 0,
        data.summary?.intervalSeconds
      )
      if (denseInterval) {
        keyMetricsAdaptiveInterval.value = { key: samplingKey, interval: denseInterval }
        data = await adminApi.get<{ keys: KeyHistoryData[]; summary?: GlobalStatsSummary }>(
          `/api/${apiPath}/channels/${channelId}/keys/metrics/history?${historyQuery(dur, denseInterval)}`
        )
      }
    }
    if (props.type !== requestType || expandedChannelId.value !== channelId || keyMetricsDuration.value !== dur) return
    keyMetricsData.value = data.keys || []
    keyMetricsSummary.value = data.summary || null
  } catch {
    keyMetricsData.value = []
    keyMetricsSummary.value = null
  } finally {
    keyMetricsLoading.value = false
  }
}

function handleToggleChart(channelId: number) {
  if (expandedChannelId.value === channelId) {
    expandedChannelId.value = null
    keyMetricsData.value = []
    keyMetricsSummary.value = null
    keyMetricsAdaptiveInterval.value = null
  } else {
    expandedChannelId.value = channelId
    keyMetricsAdaptiveInterval.value = null
    loadKeyMetrics(channelId, keyMetricsDuration.value)
  }
}

function handleKeyMetricsRefresh(duration: string) {
  if (expandedChannelId.value === null) return
  loadKeyMetrics(expandedChannelId.value, duration)
}

async function loadFuzzyMode() {
  fuzzyLoadError.value = false
  try {
    const adminApi = useAdminApi()
    const data = await adminApi.get<{ fuzzyModeEnabled: boolean }>('/api/settings/fuzzy-mode')
    fuzzyEnabled.value = data.fuzzyModeEnabled
  } catch {
    fuzzyLoadError.value = true
  }
}

async function toggleFuzzyMode() {
  if (fuzzyLoadError.value) {
    await loadFuzzyMode()
    return
  }
  fuzzyLoading.value = true
  try {
    const adminApi = useAdminApi()
    await adminApi.put('/api/settings/fuzzy-mode', { enabled: !fuzzyEnabled.value })
    fuzzyEnabled.value = !fuzzyEnabled.value
  } catch (e) {
    actionError.value = e instanceof Error ? e.message : String(e)
  } finally {
    fuzzyLoading.value = false
  }
}

function clearActionError() {
  actionError.value = ''
}

function sortChannels(source: Channel[]) {
  return [...source].sort((a, b) => {
    const priorityDiff = (a.priority ?? a.index) - (b.priority ?? b.index)
    if (priorityDiff !== 0) return priorityDiff
    return a.index - b.index
  })
}

function matchesSearch(channel: Channel) {
  if (!normalizedSearch.value) return true
  const q = normalizedSearch.value
  return (
    channel.name?.toLowerCase().includes(q) ||
    channel.description?.toLowerCase().includes(q) ||
    channel.serviceType?.toLowerCase().includes(q) ||
    channel.baseUrl?.toLowerCase().includes(q) ||
    channel.baseUrls?.some(url => url.toLowerCase().includes(q))
  )
}

function isFirstOrderedActiveChannel(channel: Channel) {
  return orderedActiveChannels.value[0]?.index === channel.index
}

function isLastOrderedActiveChannel(channel: Channel) {
  return orderedActiveChannels.value[orderedActiveChannels.value.length - 1]?.index === channel.index
}

async function refreshCurrentChannels() {
  clearActionError()
  isRefreshing.value = true
  try {
    await refreshChannels(props.type)
  } catch (e) {
    actionError.value = e instanceof Error ? e.message : String(e)
  } finally {
    isRefreshing.value = false
  }
}


function canDeleteChannel(channel: Channel) {
  const activeCount = orderedActiveChannels.value.filter(ch => ch.status === 'active' || ch.status === undefined || ch.status === '').length
  const isActive = channel.status === 'active' || channel.status === undefined || channel.status === ''
  return !(isActive && activeCount <= 1)
}

async function handleDelete(channel: Channel) {
  clearActionError()
  if (!canDeleteChannel(channel)) {
    actionError.value = t('orchestration.deleteActiveGuard')
    return
  }

  actionLoading.value = true
  try {
    await deleteChannel(channel.index, props.type)
  } catch (e) {
    actionError.value = e instanceof Error ? e.message : String(e)
  } finally {
    actionLoading.value = false
  }
}

async function handleStatusToggle(channelId: number, currentStatus: string) {
  clearActionError()
  const newStatus = currentStatus === 'active' || !currentStatus ? 'suspended' : 'active'
  try {
    await setChannelStatus(channelId, newStatus as 'active' | 'suspended', props.type)
  } catch (e) {
    actionError.value = e instanceof Error ? e.message : String(e)
  }
}

async function handleDisable(channelId: number) {
  clearActionError()
  try {
    await setChannelStatus(channelId, 'disabled', props.type)
  } catch (e) {
    actionError.value = e instanceof Error ? e.message : String(e)
  }
}

async function handleEnable(channelId: number) {
  clearActionError()
  try {
    await setChannelStatus(channelId, 'active', props.type)
  } catch (e) {
    actionError.value = e instanceof Error ? e.message : String(e)
  }
}

async function handleResume(channelId: number) {
  clearActionError()
  try {
    await resumeChannel(channelId, props.type)
  } catch (e) {
    actionError.value = e instanceof Error ? e.message : String(e)
  }
}

function isBreakerManagedChannel(channel: Channel) {
  const channelMetrics = metricsMap.value.get(channel.index)
  return channel.status === 'suspended' || channelMetrics?.circuitState === 'open'
}

async function handlePromote(channel: Channel, duration: number) {
  clearActionError()
  try {
    if (isBreakerManagedChannel(channel)) {
      await resumeChannel(channel.index, props.type)
    }
    await promoteChannel(channel.index, duration, props.type)
  } catch (e) {
    actionError.value = e instanceof Error ? e.message : String(e)
  }
}

async function handleMoveTop(channelId: number) {
  const ordered = orderedActiveChannels.value.map(channel => channel.index)
  const index = ordered.indexOf(channelId)
  if (index <= 0) return
  ordered.splice(index, 1)
  ordered.unshift(channelId)
  await handleReorder(ordered)
}

async function handleMoveBottom(channelId: number) {
  const ordered = orderedActiveChannels.value.map(channel => channel.index)
  const index = ordered.indexOf(channelId)
  if (index < 0 || index >= ordered.length - 1) return
  ordered.splice(index, 1)
  ordered.push(channelId)
  await handleReorder(ordered)
}

function handleEdit(channel: Channel) {
  editingChannel.value = channel
  showChannelEditor.value = true
}

function handleAdd() {
  editingChannel.value = null
  showChannelEditor.value = true
}

function handleSaved() {
  showChannelEditor.value = false
  refreshCurrentChannels()
}

async function handleEditTestCapability(channel: Channel) {
  showChannelEditor.value = false
  await refreshCurrentChannels()
  capabilityChannel.value = channels.value.find(ch => ch.index === channel.index) ?? channel
  showCapabilityDialog.value = true
}

function handleLogs(channel: Channel) {
  logsChannel.value = channel
}

function handleCapability(channel: Channel) {
  capabilityChannel.value = channel
  showCapabilityDialog.value = true
}

function closeCapabilityDialog() {
  showCapabilityDialog.value = false
  capabilityChannel.value = null
}

async function handleReorder(newOrder: number[]) {
  clearActionError()
  try {
    await reorderChannels(newOrder, props.type)
  } catch (e) {
    actionError.value = e instanceof Error ? e.message : String(e)
  }
}

function onDragStart(e: DragEvent, channelIndex: number) {
  draggedIndex.value = channelIndex
  e.dataTransfer?.setData('text/plain', String(channelIndex))
  if (e.dataTransfer) e.dataTransfer.effectAllowed = 'move'
}

function onDragOver(e: DragEvent) {
  e.preventDefault()
  if (e.dataTransfer) e.dataTransfer.dropEffect = 'move'
}

function onDrop(e: DragEvent, targetIndex: number) {
  e.preventDefault()
  const sourceIndex = draggedIndex.value
  if (sourceIndex === null || sourceIndex === targetIndex || normalizedSearch.value) return

  const currentChannels = [...activeChannels.value]
  const sourcePos = currentChannels.findIndex(c => c.index === sourceIndex)
  const targetPos = currentChannels.findIndex(c => c.index === targetIndex)
  if (sourcePos === -1 || targetPos === -1) return

  const [moved] = currentChannels.splice(sourcePos, 1)
  currentChannels.splice(targetPos, 0, moved)
  void handleReorder(currentChannels.map(c => c.index))
  draggedIndex.value = null
}

function onDragEnd() {
  draggedIndex.value = null
}

// 服务状态变化时自动加载 Fuzzy 模式和统计数据
watch([() => status.value.running, isConsoleChannelsActive], ([running, active]) => {
  if (running && active) {
    loadFuzzyMode()
    if (showGlobalStats.value) {
      loadGlobalStats()
    }
  }
}, { immediate: true })

// 类型切换时重新加载统计
watch(() => props.type, () => {
  if (shouldLoadGlobalStats()) {
    loadGlobalStats()
  }
})

watch(showGlobalStats, (visible) => {
  if (!visible) {
    globalStatsRequestSeq += 1
    statsLoading.value = false
    return
  }
  if (shouldLoadGlobalStats()) {
    loadGlobalStats(globalStatsDuration.value)
  }
})

onBeforeUnmount(() => {
  isMounted = false
  globalStatsRequestSeq += 1
})
</script>

<template>
  <div class="flex min-h-full flex-col gap-4">
    <Alert v-if="actionError" variant="destructive" class="shrink-0">
      <p class="text-sm">{{ actionError }}</p>
      <template #action>
        <Button variant="ghost" size="sm" @click="clearActionError">
          {{ t('common.cancel') }}
        </Button>
      </template>
    </Alert>

    <div class="border border-border bg-card/75 p-3 shadow-sm dark:bg-card/55">
      <div class="flex flex-wrap items-center gap-3">
        <div class="relative w-full min-w-[160px] sm:w-60 lg:w-64">
          <Search class="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
          <Input
            v-model="searchQuery"
            :placeholder="t('orchestration.searchPlaceholder')"
            class="pl-9 pr-8"
          />
          <button
            v-if="searchQuery"
            type="button"
            class="absolute right-2 top-1/2 inline-flex h-5 w-5 -translate-y-1/2 items-center justify-center rounded text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
            :aria-label="t('common.clearSearch')"
            :title="t('common.clearSearch')"
            @click="searchQuery = ''"
          >
            <X class="h-3.5 w-3.5" />
          </button>
        </div>
        <!-- 批量测速按钮：桌面端暂不展示，放不下 -->
        <Button size="sm" @click="handleAdd">
          <Plus class="h-3.5 w-3.5" />
          {{ t('app.actions.addChannel') }}
        </Button>
        <div class="flex-1" />
        <Button
          size="sm"
          variant="outline"
          class="h-7 text-xs"
          :class="{ 'border-amber-500/40 text-amber-600 dark:text-amber-400': fuzzyEnabled }"
          :disabled="fuzzyLoading"
          :title="fuzzyLoadError ? t('toast.loadFuzzyFailed') : (fuzzyEnabled ? t('tooltip.fuzzyEnabled') : t('tooltip.fuzzyDisabled'))"
          @click="toggleFuzzyMode"
        >
          <ShieldCheck v-if="fuzzyEnabled" class="h-3 w-3 mr-1" />
          <ShieldOff v-else class="h-3 w-3 mr-1" />
          Fuzzy
          <Loader2 v-if="fuzzyLoading" class="h-3 w-3 ml-1 animate-spin" />
        </Button>
        <Button size="sm" variant="outline" class="h-7 text-xs" @click="showCbDialog = true">
          <Zap class="h-3 w-3 mr-1" />
          TB
        </Button>
      </div>

      <div class="mt-3 grid grid-cols-2 gap-2 md:grid-cols-4">
        <div class="border border-border bg-background/60 px-3 py-2">
          <div class="text-[10px] font-bold uppercase tracking-[0.18em] text-muted-foreground">Active</div>
          <div class="font-mono text-lg font-bold text-emerald-600 dark:text-emerald-300">{{ activeCount }}</div>
        </div>
        <div class="border border-border bg-background/60 px-3 py-2">
          <div class="text-[10px] font-bold uppercase tracking-[0.18em] text-muted-foreground">Failover</div>
          <div class="font-mono text-lg font-bold text-blue-600 dark:text-blue-300">{{ failoverCount }}</div>
        </div>
        <div class="border border-border bg-background/60 px-3 py-2">
          <div class="text-[10px] font-bold uppercase tracking-[0.18em] text-muted-foreground">Disabled</div>
          <div class="font-mono text-lg font-bold text-rose-600 dark:text-rose-300">{{ disabledCount }}</div>
        </div>
        <div class="border border-border bg-background/60 px-3 py-2">
          <div class="text-[10px] font-bold uppercase tracking-[0.18em] text-muted-foreground">Mode</div>
          <div class="truncate text-sm font-semibold text-foreground">
            {{ stats?.multiChannelMode ? t('orchestration.multiChannel') : t('orchestration.singleChannel') }}
          </div>
        </div>
      </div>

      <!-- 用量统计图表 -->
      <div class="mt-3 border border-border bg-background/60">
        <button
          type="button"
          class="flex w-full items-center justify-between px-3 py-2 text-xs font-bold uppercase tracking-[0.18em] text-muted-foreground transition-colors hover:text-foreground"
          @click="showGlobalStats = !showGlobalStats"
        >
          <div class="flex items-center gap-2">
            <BarChart3 class="h-4 w-4" />
            <span>{{ t('chart.globalStats') }}</span>
          </div>
          <ChevronDown class="h-4 w-4 transition-transform" :class="{ '-rotate-180': showGlobalStats }" />
        </button>
        <div v-show="showGlobalStats">
          <GlobalStatsChart
            ref="globalStatsChartRef"
            :api-type="props.type"
            :chart-interval="globalStatsChartInterval"
            compact
            @refresh="loadGlobalStats"
          />
        </div>
      </div>
    </div>

    <div v-if="isInitialLoading" class="space-y-2">
      <Skeleton v-for="i in 5" :key="i" class="h-16 w-full rounded-none" />
    </div>

    <div v-else-if="activeChannels.length === 0 && inactiveChannels.length === 0" class="border border-dashed border-border bg-card/50 py-12 text-center">
      <p class="text-sm text-muted-foreground">
        {{ searchQuery
          ? t('orchestration.noMatchingChannels')
          : t('orchestration.noActiveChannels')
        }}
      </p>
    </div>

    <div v-else class="relative space-y-4" :class="{ 'pointer-events-none select-none': actionLoading }">
      <div v-if="actionLoading" class="absolute inset-0 z-10 flex items-center justify-center bg-background/60 backdrop-blur-[1px]">
        <div class="flex items-center gap-2 border border-border bg-card px-3 py-1.5 text-xs text-muted-foreground shadow-sm">
          <Loader2 class="h-3.5 w-3.5 animate-spin" />
          {{ t('console.actions.deleting') }}
        </div>
      </div>
      <section class="border border-border bg-card/50">
        <div class="flex items-center gap-2 border-b border-border bg-secondary/40 px-3 py-2">
          <div class="flex items-center gap-2">
            <Layers class="h-4 w-4 text-primary" />
            <span class="text-xs font-bold uppercase tracking-[0.18em] text-foreground">
              {{ t('orchestration.failoverSequence') }}
            </span>
          </div>
        </div>
        <div class="divide-y divide-border">
          <div
            v-for="(channel, index) in activeChannels"
            :key="`${type}-${channel.index}`"
            :draggable="!normalizedSearch"
            :class="{ 'opacity-50': draggedIndex === channel.index }"
            @dragstart="onDragStart($event, channel.index)"
            @dragover="onDragOver"
            @drop="onDrop($event, channel.index)"
            @dragend="onDragEnd"
          >
            <ChannelCard
              :channel="channel"
              :metrics="metricsMap.get(channel.index)"
              :activity="activityMap.get(channel.index)"
              :priority="index + 1"
              :supports-capability="supportsCapability"
              :can-delete="canDeleteChannel(channel)"
              :can-move-top="!isFirstOrderedActiveChannel(channel)"
              :can-move-bottom="!isLastOrderedActiveChannel(channel)"
              :expanded="expandedChannelId === channel.index"
              @edit="handleEdit(channel)"
              @delete="handleDelete(channel)"
              @status="handleStatusToggle(channel.index, channel.status || 'active')"
              @disable="handleDisable(channel.index)"
              @enable="handleEnable(channel.index)"
              @resume="handleResume(channel.index)"
              @promote="handlePromote(channel, 300)"
              @move-top="handleMoveTop(channel.index)"
              @move-bottom="handleMoveBottom(channel.index)"
              @logs="handleLogs(channel)"
              @capability="handleCapability(channel)"
              @toggle="handleToggleChart(channel.index)"
            />
            <!-- Expanded key trend chart area -->
            <div v-if="expandedChannelId === channel.index" class="border-x border-b border-border bg-background/40 px-3 py-2">
              <KeyTrendChart
                :data="keyMetricsData"
                :channel-name="channel.name"
                :loading="keyMetricsLoading"
                :duration="keyMetricsDuration"
                :summary="keyMetricsSummary"
                :chart-interval="keyMetricsChartInterval"
                @refresh="handleKeyMetricsRefresh"
              />
            </div>
          </div>
        </div>
      </section>

      <section v-if="inactiveChannels.length" class="border border-dashed border-border bg-muted/20">
        <div class="flex items-center gap-2 border-b border-border px-3 py-2">
          <Archive class="h-4 w-4 text-muted-foreground" />
          <span class="text-xs font-bold uppercase tracking-[0.18em] text-muted-foreground">
            {{ t('orchestration.standbyPool') }}
          </span>
        </div>
        <div class="divide-y divide-border">
          <ChannelCard
            v-for="(channel, index) in inactiveChannels"
            :key="`${type}-${channel.index}`"
            :channel="channel"
            :metrics="metricsMap.get(channel.index)"
            :activity="activityMap.get(channel.index)"
            :priority="index + 1"
            :supports-capability="supportsCapability"
            inactive
            :can-delete="canDeleteChannel(channel)"
            @edit="handleEdit(channel)"
            @delete="handleDelete(channel)"
            @status="handleStatusToggle(channel.index, channel.status || 'disabled')"
            @disable="handleDisable(channel.index)"
            @enable="handleEnable(channel.index)"
            @resume="handleResume(channel.index)"
            @promote="handlePromote(channel, 300)"
            @logs="handleLogs(channel)"
            @capability="handleCapability(channel)"
          />
        </div>
      </section>
    </div>

    <ChannelEditDialog
      v-if="showChannelEditor"
      :key="`${type}-${editingChannel?.index ?? 'new'}`"
      :channel="editingChannel"
      :channel-type="type"
      @close="showChannelEditor = false"
      @saved="handleSaved"
      @test-capability="handleEditTestCapability"
    />

    <ChannelLogsDialog
      :open="!!logsChannel"
      :channel-type="type"
      :channel-id="logsChannel?.index ?? -1"
      :channel-name="logsChannel?.name ?? ''"
      @close="logsChannel = null"
    />

    <CapabilityTestDialog
      v-if="capabilityChannel"
      :key="`${type}-${capabilityChannel.index}`"
      :open="showCapabilityDialog"
      :channel-type="type"
      :channel-id="capabilityChannel.index"
      :channel-name="capabilityChannel.name ?? ''"
      @close="closeCapabilityDialog"
    />

    <CircuitBreakerDialog
      :open="showCbDialog"
      @close="showCbDialog = false"
    />
  </div>
</template>
