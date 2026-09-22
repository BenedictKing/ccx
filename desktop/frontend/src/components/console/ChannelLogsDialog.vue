<script setup lang="ts">
import { ref, watch, computed, onBeforeUnmount } from 'vue'
import { useDocumentVisibility, useIntervalFn } from '@vueuse/core'
import { Alert } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Skeleton } from '@/components/ui/skeleton'
import { Badge } from '@/components/ui/badge'
import { Check, ChevronDown, ChevronUp, Copy, Flag, Loader2, X, List } from 'lucide-vue-next'
import { useAdminApi } from '@/composables/useAdminApi'
import { useLanguage } from '@/composables/useLanguage'
import type { ChannelBreakerEvidence, ChannelLogEntry, ChannelLogsResponse } from '@/services/admin-api'
import AutopilotTraceDetailDialog from '@/components/autopilot/AutopilotTraceDetailDialog.vue'

interface Props {
  open: boolean
  channelType: string
  channelId: number
  channelName: string
}

const props = defineProps<Props>()
const emit = defineEmits<{ (e: 'close'): void }>()

const { t } = useLanguage()
const api = useAdminApi()
const visibility = useDocumentVisibility()

const logs = ref<ChannelLogEntry[]>([])
const breakerEvidence = ref<ChannelBreakerEvidence | null>(null)
const loading = ref(false)
const refreshing = ref(false)
const error = ref('')
const autoRefresh = ref(true)
const copiedLogKey = ref<string | null>(null)
let fetchPromise: Promise<void> | null = null
let copyLogResetTimer: ReturnType<typeof setTimeout> | null = null

// 展开状态以稳定 key 记录（correlationId/requestId），列表刷新重排后不会错位
const expandedGroupKey = ref<string | null>(null)
const expandedLogKey = ref<string | null>(null)

// 日志视图过滤：全部（组可展开看尝试明细）/ 仅最终交付（折叠组行，不展开明细）/ 含竞速放大（仅竞速相关组）
type LogViewMode = 'all' | 'final' | 'racing'
const logViewMode = ref<LogViewMode>('all')

interface LogGroup {
  key: string
  entries: ChannelLogEntry[]
  representative: ChannelLogEntry
  hasRacing: boolean
}

interface LogRow {
  key: string
  log: ChannelLogEntry
  group: LogGroup
  isAttempt: boolean
}

// Autopilot Trace 详情对话框
const autopilotDetailOpen = ref(false)
const autopilotDetailTraceUid = ref<string | null>(null)
function openAutopilotTrace(traceUid: string) {
  autopilotDetailTraceUid.value = traceUid
  autopilotDetailOpen.value = true
}

const shouldPoll = computed(() => props.open && autoRefresh.value && visibility.value === 'visible')

async function fetchLogs(options: { silent?: boolean } = {}) {
  if (!props.open || props.channelId < 0 || fetchPromise) return fetchPromise

  const silent = options.silent || logs.value.length > 0
  if (silent) refreshing.value = true
  else loading.value = true
  error.value = ''

  fetchPromise = api.get<ChannelLogsResponse>(`/api/${props.channelType}/channels/${props.channelId}/logs`)
    .then(data => {
      logs.value = (data.logs || [])
        .slice()
        .sort((a, b) => new Date(b.timestamp).getTime() - new Date(a.timestamp).getTime())
      // 日志为空但渠道熔断时，后端会给出熔断成因依据
      breakerEvidence.value = data.breakerEvidence ?? null
    })
    .catch(e => {
      error.value = e instanceof Error ? e.message : String(e)
    })
    .finally(() => {
      loading.value = false
      refreshing.value = false
      fetchPromise = null
    })

  return fetchPromise
}

function resetExpanded() {
  expandedGroupKey.value = null
  expandedLogKey.value = null
}

async function writeClipboardText(text: string) {
  if (typeof navigator !== 'undefined' && navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(text)
      return
    } catch {
      // 继续使用传统复制路径
    }
  }

  if (typeof document === 'undefined') {
    throw new Error('Clipboard API is unavailable')
  }

  const textarea = document.createElement('textarea')
  textarea.value = text
  textarea.setAttribute('readonly', '')
  textarea.style.position = 'fixed'
  textarea.style.top = '-9999px'
  textarea.style.left = '-9999px'
  document.body.appendChild(textarea)
  textarea.select()

  try {
    if (!document.execCommand('copy')) {
      throw new Error('Copy command failed')
    }
  } finally {
    document.body.removeChild(textarea)
  }
}

async function copyLogEntry(log: ChannelLogEntry, key: string) {
  try {
    await writeClipboardText(JSON.stringify(log, null, 2))
    copiedLogKey.value = key
    if (copyLogResetTimer) clearTimeout(copyLogResetTimer)
    copyLogResetTimer = setTimeout(() => {
      copiedLogKey.value = null
      copyLogResetTimer = null
    }, 1600)
  } catch (e) {
    console.error('Failed to copy channel log:', e)
  }
}

function isRacingLost(log: ChannelLogEntry): boolean {
  return log.racingStatus === 'lost' || log.status === 'racing_lost'
}

function hasRacingInfo(log: ChannelLogEntry): boolean {
  return Boolean(log.racingRole) || Boolean(log.racingStatus) || (log.selectionReason ?? '').toLowerCase().includes('racing')
}

// 组的「最终结局」：成功交付的那条 > 最新的非竞速败出 > 最新一条（entries 已按时间倒序，越靠前越新）
function pickRepresentative(entries: ChannelLogEntry[]): ChannelLogEntry {
  return entries.find(e => e.success && e.status === 'completed')
    ?? entries.find(e => !isRacingLost(e))
    ?? entries[0]
}

// 按用户请求关联 ID 折叠：同一 correlationId 的多条上游尝试归为一组；无 ID 的条目自成一组
function groupLogs(entries: ChannelLogEntry[]): LogGroup[] {
  const grouped = new Map<string, ChannelLogEntry[]>()
  for (const entry of entries) {
    const key = entry.requestCorrelationId
      ? `cid:${entry.requestCorrelationId}`
      : `single:${entry.requestId || entry.timestamp}`
    const list = grouped.get(key)
    if (list) list.push(entry)
    else grouped.set(key, [entry])
  }
  return Array.from(grouped, ([key, list]) => ({
    key,
    entries: list,
    representative: pickRepresentative(list),
    hasRacing: list.some(hasRacingInfo),
  }))
}

// 50 组截断，对应原来的 50 条上限
const logGroups = computed(() => groupLogs(logs.value).slice(0, 50))

const visibleGroups = computed(() =>
  logViewMode.value === 'racing' ? logGroups.value.filter(g => g.hasRacing) : logGroups.value,
)

// 拍平为渲染行：组行（最终结局）+ 展开时的尝试明细行，明细行复用同一渲染
const displayRows = computed<LogRow[]>(() => {
  const rows: LogRow[] = []
  for (const group of visibleGroups.value) {
    rows.push({ key: group.key, log: group.representative, group, isAttempt: false })
    if (group.entries.length > 1 && expandedGroupKey.value === group.key) {
      group.entries.forEach((entry, j) => {
        rows.push({ key: `${group.key}#${entry.requestId || j}`, log: entry, group, isAttempt: true })
      })
    }
  }
  return rows
})

const isGroupExpandable = (group: LogGroup): boolean =>
  group.entries.length > 1 && logViewMode.value !== 'final'

function onRowClick(row: LogRow) {
  if (!row.isAttempt && isGroupExpandable(row.group)) {
    expandedGroupKey.value = expandedGroupKey.value === row.group.key ? null : row.group.key
    return
  }
  if (row.log.errorInfo?.trim()) {
    expandedLogKey.value = expandedLogKey.value === row.key ? null : row.key
  }
}

function statusColorClass(code: number) {
  if (code >= 200 && code < 300) return 'border-emerald-500 bg-emerald-500 text-white'
  if (code >= 400 && code < 500) return 'border-amber-500 bg-amber-500 text-white'
  return 'border-rose-500 bg-rose-500 text-white'
}

function requestStatusClass(status: string) {
  switch (status) {
    case 'completed': return 'border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300'
    case 'failed': return 'border-rose-500/30 bg-rose-500/10 text-rose-700 dark:text-rose-300'
    case 'cancelled':
    case 'canceled': return 'border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300'
    case 'racing_lost': return 'border-slate-500/30 bg-slate-500/10 text-slate-600 dark:text-slate-300'
    case 'streaming': return 'border-cyan-500/30 bg-cyan-500/10 text-cyan-700 dark:text-cyan-300'
    case 'first_byte': return 'border-primary/30 bg-primary/10 text-primary'
    case 'connecting': return 'border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300'
    case 'pending': return 'border-border bg-muted/30 text-muted-foreground'
    default: return 'border-border bg-muted/30 text-muted-foreground'
  }
}

function requestStatusText(status: string) {
  switch (status) {
    case 'pending': return t('channelLogs.status.pending')
    case 'connecting': return t('channelLogs.status.connecting')
    case 'first_byte': return t('channelLogs.status.firstByte')
    case 'streaming': return t('channelLogs.status.streaming')
    case 'completed': return t('channelLogs.status.completed')
    case 'failed': return t('channelLogs.status.failed')
    case 'cancelled':
    case 'canceled': return t('channelLogs.status.cancelled')
    case 'racing_lost': return t('channelLogs.status.racingLost')
    default: return status || '—'
  }
}

function isInProgress(status: string) {
  return ['pending', 'connecting', 'first_byte', 'streaming'].includes(status)
}

function interfaceTypeClass(type: string) {
  switch (type.toLowerCase()) {
    case 'messages': return 'border-orange-500/30 bg-orange-500/10 text-orange-700 dark:text-orange-300'
    case 'chat': return 'border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300'
    case 'responses': return 'border-teal-500/30 bg-teal-500/10 text-teal-700 dark:text-teal-300'
    case 'gemini': return 'border-purple-500/30 bg-purple-500/10 text-purple-700 dark:text-purple-300'
    case 'images': return 'border-pink-500/30 bg-pink-500/10 text-pink-700 dark:text-pink-300'
    case 'vectors': return 'border-sky-500/30 bg-sky-500/10 text-sky-700 dark:text-sky-300'
    default: return 'border-border bg-muted/30 text-muted-foreground'
  }
}

function calculateDurations(log: ChannelLogEntry) {
  if (!log.startTime) return null

  const start = new Date(log.startTime).getTime()
  if (!Number.isFinite(start)) return null
  const connected = log.connectedAt ? new Date(log.connectedAt).getTime() : null
  const firstByte = log.firstByteAt ? new Date(log.firstByteAt).getTime() : null
  const completed = log.completedAt ? new Date(log.completedAt).getTime() : null

  return {
    connectMs: connected && Number.isFinite(connected) ? connected - start : null,
    firstByteMs: firstByte && Number.isFinite(firstByte) ? firstByte - start : null,
    totalMs: completed && Number.isFinite(completed) ? completed - start : null,
  }
}

function formatDurationSeconds(durationMs: number) {
  const seconds = durationMs / 1000
  return `${Number.parseFloat(seconds.toPrecision(3))}s`
}

function formatReasoningEffort(effort: string) {
  const value = effort.trim()
  return value.length > 24 ? `${value.slice(0, 21)}...` : value
}

function normalizedReasoningEffort(effort?: string) {
  return effort?.trim() || ''
}

function singleReasoningEffort(log: ChannelLogEntry) {
  const original = normalizedReasoningEffort(log.originalReasoningEffort)
  const actual = normalizedReasoningEffort(log.actualReasoningEffort)
  if (!original) return actual
  if (!actual) return original
  return original.toLowerCase() === actual.toLowerCase() ? actual : ''
}

function reasoningEffortClass(effort: string) {
  const value = effort.toLowerCase()
  if (value === 'none' || value === 'disabled' || value === 'false') return 'border-border bg-muted/30 text-muted-foreground'
  if (value === 'minimal' || value === 'low') return 'border-cyan-500/30 bg-cyan-500/10 text-cyan-700 dark:text-cyan-300'
  if (value === 'high' || value === 'xhigh' || value === 'max') return 'border-amber-500/30 bg-amber-500/10 text-amber-700 dark:text-amber-300'
  if (value.startsWith('budget=')) return 'border-violet-500/30 bg-violet-500/10 text-violet-700 dark:text-violet-300'
  return 'border-primary/30 bg-primary/10 text-primary'
}

function formatTime(ts: string) {
  try {
    return new Date(ts).toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit' })
  } catch {
    return ts
  }
}

// 熔断依据可能跨天（重启前的历史失败），需要带日期而非仅时刻
function formatFullTime(ts: string) {
  const d = new Date(ts)
  return Number.isNaN(d.getTime()) ? ts : d.toLocaleString()
}

const { pause, resume } = useIntervalFn(() => {
  if (shouldPoll.value) fetchLogs({ silent: true })
}, 3000, { immediate: false })

watch(() => props.open, (isOpen) => {
  if (isOpen) {
    logs.value = []
    breakerEvidence.value = null
    resetExpanded()
    logViewMode.value = 'all'
    resume()
    window.addEventListener('keydown', onKeyDown)
    void fetchLogs()
  } else {
    pause()
    window.removeEventListener('keydown', onKeyDown)
  }
})

watch([() => props.channelId, () => props.channelType], () => {
  if (!props.open) return
  logs.value = []
  breakerEvidence.value = null
  resetExpanded()
  void fetchLogs()
})

// 切换过滤模式时收起展开态，避免「仅最终交付」下残留明细
watch(logViewMode, () => {
  resetExpanded()
})

watch(shouldPoll, (polling) => {
  if (polling) resume()
  else pause()
})

function onKeyDown(e: KeyboardEvent) {
  if (!props.open || e.key !== 'Escape') return
  // Trace 详情对话框打开时由其自行处理 Esc（仅关最上层，不连环关闭）
  if (autopilotDetailOpen.value) return
  e.preventDefault()
  emit('close')
}

onBeforeUnmount(() => {
  if (copyLogResetTimer) clearTimeout(copyLogResetTimer)
  window.removeEventListener('keydown', onKeyDown)
})
</script>

<template>
  <Teleport to="body">
    <Transition name="fade">
      <div
        v-if="open"
        class="fixed inset-0 z-50 flex items-center justify-center"
      >
        <div class="absolute inset-0 bg-black/60 backdrop-blur-sm" @click="emit('close')" />

        <div class="relative z-10 flex h-[85vh] max-h-[85vh] w-[90vw] max-w-4xl flex-col overflow-hidden rounded-2xl border border-border bg-card shadow-2xl">
          <div class="flex shrink-0 items-center justify-between border-b border-border p-4">
            <div class="flex min-w-0 items-center gap-2">
              <h3 class="truncate text-sm font-semibold">
                {{ t('channelLogs.title') }}: {{ channelName }}
              </h3>
              <Badge variant="secondary" class="text-[10px]">
                {{ logs.length }} {{ t('console.logs.entries') }}
              </Badge>
              <Badge v-if="refreshing" variant="outline" class="text-[10px]">
                <Loader2 class="h-3 w-3 animate-spin" />
                {{ t('common.refreshing') }}
              </Badge>
            </div>
            <div class="flex items-center gap-2">
              <div class="flex items-center gap-1">
                <Button
                  v-for="mode in (['all', 'final', 'racing'] as LogViewMode[])"
                  :key="mode"
                  size="sm"
                  class="h-7 px-2 text-[11px] font-semibold"
                  :variant="logViewMode === mode ? 'default' : 'outline'"
                  @click="logViewMode = mode"
                >
                  {{ t(`channelLogs.filter.${mode}`) }}
                </Button>
              </div>
              <Button variant="ghost" size="icon-sm" @click="emit('close')" class="relative group">
                <X class="h-4 w-4" />
                <span class="absolute -bottom-6 right-0 text-[10px] text-muted-foreground opacity-0 group-hover:opacity-100 transition-opacity pointer-events-none whitespace-nowrap">Esc</span>
              </Button>
            </div>
          </div>

          <div class="min-h-0 flex-1 overflow-hidden">
            <div v-if="loading && logs.length === 0" class="space-y-2 p-4">
              <Skeleton v-for="i in 5" :key="i" class="h-14 w-full" />
            </div>

            <div v-else-if="error" class="p-4 text-sm text-destructive">
              {{ error }}
            </div>

            <div v-else-if="logs.length === 0" class="flex flex-col items-center gap-2 p-8 text-center text-sm text-muted-foreground">
              <List class="h-10 w-10" />
              {{ t('channelLogs.empty') }}

              <!-- 熔断但无日志：交代熔断依据，避免呈现无来由的黑盒 -->
              <Alert
                v-if="breakerEvidence"
                class="mt-3 max-w-md border-amber-500/50 bg-amber-500/10 text-left text-amber-700 dark:text-amber-300"
              >
                <div class="space-y-1 text-xs">
                  <div class="font-medium">
                    {{ breakerEvidence.circuitState === 'open'
                      ? t('channelLogs.breakerOpenTitle')
                      : t('channelLogs.breakerHalfOpenTitle') }}
                  </div>
                  <div v-if="breakerEvidence.predatesRestart">
                    {{ t('channelLogs.breakerPredatesRestart') }}
                  </div>
                  <div v-if="breakerEvidence.lastFailureAt">
                    {{ t('channelLogs.breakerLastFailure', { time: formatFullTime(breakerEvidence.lastFailureAt) }) }}
                  </div>
                  <div v-if="breakerEvidence.nextRetryAt">
                    {{ t('channelLogs.breakerNextRetry', { time: formatFullTime(breakerEvidence.nextRetryAt) }) }}
                  </div>
                  <div>
                    {{ t('channelLogs.breakerBackoff', {
                      level: breakerEvidence.backoffLevel,
                      failures: breakerEvidence.consecutiveFailures,
                    }) }}
                  </div>
                </div>
              </Alert>
            </div>

            <ScrollArea v-else type="auto" class="h-full min-h-0 w-full">
              <div v-if="displayRows.length === 0" class="py-8 text-center text-sm text-muted-foreground">
                {{ t('channelLogs.noMatch') }}
              </div>
              <div v-else class="divide-y divide-border">
                <div
                  v-for="row in displayRows"
                  :key="row.key"
                  class="group relative cursor-pointer px-4 py-3 pr-14 transition-colors hover:bg-accent/40"
                  :class="{
                    'bg-destructive/5': row.log.status === 'failed',
                    'bg-slate-500/5': row.log.status === 'racing_lost',
                    'pl-10': row.isAttempt,
                  }"
                  @click="onRowClick(row)"
                >
                  <Button
                    variant="outline"
                    size="icon-sm"
                    class="absolute right-3 top-3 h-7 w-7 bg-card/95 opacity-0 shadow-sm transition-opacity group-hover:opacity-100 focus-visible:opacity-100"
                    :class="{ 'border-emerald-500/40 text-emerald-600 opacity-100 dark:text-emerald-300': copiedLogKey === row.key }"
                    :title="copiedLogKey === row.key ? t('channelLogs.copiedEntry') : t('channelLogs.copyEntry')"
                    :aria-label="t('channelLogs.copyEntry')"
                    @click.stop="copyLogEntry(row.log, row.key)"
                  >
                    <Check v-if="copiedLogKey === row.key" class="h-3.5 w-3.5" />
                    <Copy v-else class="h-3.5 w-3.5" />
                  </Button>
                  <div class="flex flex-wrap items-center gap-2 text-xs">
                    <span
                      v-if="row.log.statusCode > 0"
                      class="inline-flex min-w-10 justify-center border px-2 py-0.5 font-mono font-bold text-white"
                      :class="statusColorClass(row.log.statusCode)"
                    >
                      {{ row.log.statusCode }}
                    </span>
                    <span
                      v-else-if="isInProgress(row.log.status)"
                      class="inline-flex min-w-10 justify-center border border-primary/30 bg-primary/10 px-2 py-0.5 font-mono font-bold text-primary"
                    >
                      000
                    </span>
                    <span v-else class="inline-flex min-w-10 justify-center border border-border bg-muted/30 px-2 py-0.5 font-mono font-bold text-muted-foreground">-</span>

                    <span class="text-muted-foreground">{{ formatTime(row.log.timestamp) }}</span>
                    <span v-if="row.log.status" class="inline-flex border px-1.5 py-0.5 text-[10px] font-bold uppercase" :class="requestStatusClass(row.log.status)">
                      {{ requestStatusText(row.log.status) }}
                    </span>
                    <Badge v-if="!row.isAttempt && row.group.entries.length > 1" variant="secondary" class="text-[10px]">
                      {{ t('channelLogs.attempts', { count: row.group.entries.length }) }}
                    </Badge>
                    <span v-if="row.log.interfaceType" class="inline-flex border px-1.5 py-0.5 text-[10px] font-bold uppercase" :class="interfaceTypeClass(row.log.interfaceType)">
                      {{ row.log.interfaceType }}
                    </span>
                    <span v-if="row.log.agentRole === 'subagent'" class="inline-flex border border-amber-500/30 bg-amber-500/10 px-1.5 py-0.5 text-[10px] font-bold uppercase text-amber-700 dark:text-amber-300">
                      SUBAGENT<span v-if="row.log.agentConfidence === 'heuristic'">?</span>
                    </span>
                    <span v-else-if="row.log.agentRole === 'main'" class="inline-flex border border-emerald-500/30 bg-emerald-500/10 px-1.5 py-0.5 text-[10px] font-bold uppercase text-emerald-700 dark:text-emerald-300">
                      MAIN
                    </span>
                    <span v-if="row.log.operation" class="inline-flex border border-cyan-500/30 bg-cyan-500/10 px-1.5 py-0.5 text-[10px] font-bold uppercase text-cyan-700 dark:text-cyan-300">
                      {{ row.log.operation }}
                    </span>
                    <span v-if="row.log.requestSource === 'capability_test'" class="inline-flex border border-amber-500/30 bg-amber-500/10 px-1.5 py-0.5 text-[10px] font-bold text-amber-700 dark:text-amber-300">
                      {{ t('channelLogs.sourceCapabilityTest') }}
                    </span>
                    <span v-if="row.log.requestSource === 'healthcheck'" class="inline-flex border border-border bg-muted/30 px-1.5 py-0.5 text-[10px] font-bold text-muted-foreground">
                      {{ t('channelLogs.sourceHealthCheck') }}
                    </span>
                    <Badge v-if="row.log.racingStatus === 'won'" variant="outline" class="border-emerald-500/40 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300">
                      <Flag class="h-3 w-3" />
                      {{ t('channelLogs.racing.won') }}
                    </Badge>
                    <Badge v-else-if="row.log.racingStatus === 'lost'" variant="outline" class="border-slate-500/40 text-slate-600 dark:text-slate-300">
                      <Flag class="h-3 w-3" />
                      {{ t('channelLogs.racing.lost') }}
                    </Badge>
                    <span v-if="row.log.originalModel" class="text-muted-foreground">{{ row.log.originalModel }} →</span>
                    <span class="font-medium">{{ row.log.model }}</span>
                    <span
                      v-if="singleReasoningEffort(row.log)"
                      class="inline-flex border px-1.5 py-0.5 text-[10px] font-bold"
                      :class="reasoningEffortClass(singleReasoningEffort(row.log))"
                      :title="singleReasoningEffort(row.log)"
                    >
                      {{ formatReasoningEffort(singleReasoningEffort(row.log)) }}
                    </span>
                    <template v-else>
                      <span
                        v-if="row.log.originalReasoningEffort"
                        class="inline-flex border px-1.5 py-0.5 text-[10px] font-bold"
                        :class="reasoningEffortClass(row.log.originalReasoningEffort)"
                        :title="row.log.originalReasoningEffort"
                      >
                        {{ t('channelLogs.reasoning.original') }} {{ formatReasoningEffort(row.log.originalReasoningEffort) }}
                      </span>
                      <span
                        v-if="row.log.actualReasoningEffort"
                        class="inline-flex border px-1.5 py-0.5 text-[10px] font-bold"
                        :class="reasoningEffortClass(row.log.actualReasoningEffort)"
                        :title="row.log.actualReasoningEffort"
                      >
                        {{ t('channelLogs.reasoning.actual') }} {{ formatReasoningEffort(row.log.actualReasoningEffort) }}
                      </span>
                    </template>
                    <code class="max-w-[130px] truncate bg-secondary px-1 py-0.5 font-mono text-[10px] text-muted-foreground">{{ row.log.keyMask }}</code>
                    <code v-if="row.log.baseUrl" class="max-w-[220px] truncate bg-secondary px-1 py-0.5 font-mono text-[10px] text-muted-foreground" :title="row.log.baseUrl">
                      {{ row.log.baseUrl }}
                    </code>
                    <span v-if="row.log.isRetry" class="inline-flex border border-amber-500/30 bg-amber-500/10 px-1.5 py-0.5 text-[10px] font-bold text-amber-700 dark:text-amber-300">
                      {{ t('channelLogs.retry') }}
                    </span>
                    <template v-if="calculateDurations(row.log)">
                      <span v-if="calculateDurations(row.log)!.connectMs !== null" class="text-muted-foreground">
                        {{ t('channelLogs.duration.connect') }} {{ formatDurationSeconds(calculateDurations(row.log)!.connectMs!) }}
                      </span>
                      <span v-if="calculateDurations(row.log)!.firstByteMs !== null" class="text-muted-foreground">
                        {{ t('channelLogs.duration.firstByte') }} {{ formatDurationSeconds(calculateDurations(row.log)!.firstByteMs!) }}
                      </span>
                      <span v-if="calculateDurations(row.log)!.totalMs !== null" class="text-muted-foreground">
                        {{ t('channelLogs.duration.total') }} {{ formatDurationSeconds(calculateDurations(row.log)!.totalMs!) }}
                      </span>
                    </template>
                    <span v-else class="text-muted-foreground">{{ formatDurationSeconds(row.log.durationMs) }}</span>
                    <button
                      v-if="row.log.autopilotTraceUid"
                      class="inline-flex items-center border border-cyan-500/40 bg-cyan-500/10 px-1.5 py-0.5 text-[10px] font-bold text-cyan-700 transition-colors hover:bg-cyan-500/20 dark:text-cyan-300"
                      :title="t('channelLogs.viewAutopilotTrace')"
                      @click.stop="openAutopilotTrace(row.log.autopilotTraceUid)"
                    >
                      {{ t('channelLogs.autopilotTrace') }} {{ row.log.autopilotTraceUid.slice(0, 12) }}...
                    </button>
                    <component
                      :is="expandedGroupKey === row.group.key ? ChevronUp : ChevronDown"
                      v-if="!row.isAttempt && isGroupExpandable(row.group)"
                      class="h-3.5 w-3.5 text-muted-foreground"
                    />
                  </div>

                  <div v-if="expandedLogKey === row.key && row.log.errorInfo" class="mt-2 border border-destructive/20 bg-destructive/10 p-2 text-xs text-destructive">
                    {{ row.log.errorInfo }}
                  </div>
                </div>
              </div>
            </ScrollArea>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>

  <!-- Autopilot Trace 详情对话框 -->
  <AutopilotTraceDetailDialog
    v-model:open="autopilotDetailOpen"
    :trace-uid="autopilotDetailTraceUid"
  />
</template>
<style scoped>
.fade-enter-active,
.fade-leave-active {
  transition: opacity 0.15s ease;
}
.fade-enter-from,
.fade-leave-to {
  opacity: 0;
}
</style>
