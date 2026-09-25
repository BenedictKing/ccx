<template>
  <v-dialog :model-value="modelValue" max-width="800" @update:model-value="$emit('update:modelValue', $event)">
    <v-card>
      <v-card-title class="d-flex align-center justify-space-between">
        <span class="dialog-title">{{ t('channelLogs.title', { channel: channelName }) }}</span>
        <div class="d-flex align-center ga-2">
          <v-btn-toggle
            v-model="logViewMode"
            mandatory
            density="compact"
            variant="outlined"
            divided
            class="log-view-toggle"
          >
            <v-btn value="all" size="x-small">{{ t('channelLogs.filter.all') }}</v-btn>
            <v-btn value="final" size="x-small">{{ t('channelLogs.filter.final') }}</v-btn>
            <v-btn value="racing" size="x-small">{{ t('channelLogs.filter.racing') }}</v-btn>
          </v-btn-toggle>
          <v-tooltip :text="t('app.actions.close') + ' (Esc)'" location="bottom" content-class="ccx-tooltip">
            <template #activator="{ props: tooltipProps }">
              <v-btn icon size="small" variant="text" v-bind="tooltipProps" @click="$emit('update:modelValue', false)">
                <v-icon>mdi-close</v-icon>
              </v-btn>
            </template>
          </v-tooltip>
        </div>
      </v-card-title>
      <v-divider />
      <v-card-text class="pa-0 channel-logs-scroll">
        <!-- Loading -->
        <div v-if="isLoading && !logs.length" class="d-flex justify-center py-8">
          <v-progress-circular indeterminate color="primary" />
        </div>

        <!-- Empty -->
        <div v-else-if="!logs.length" class="text-center py-8 text-medium-emphasis">
          <v-icon size="40">mdi-format-list-bulleted</v-icon>
          <div class="text-caption mt-2">{{ t('channelLogs.empty') }}</div>

          <!-- 熔断但无日志：交代熔断依据，避免呈现无来由的黑盒 -->
          <v-alert
            v-if="breakerEvidence"
            type="warning"
            variant="tonal"
            density="compact"
            class="text-start mx-auto mt-4 breaker-evidence-alert"
          >
            <div class="text-caption font-weight-medium">
              {{ breakerEvidence.circuitState === 'open'
                ? t('channelLogs.breakerOpenTitle')
                : t('channelLogs.breakerHalfOpenTitle') }}
            </div>
            <div v-if="breakerEvidence.predatesRestart" class="text-caption mt-1">
              {{ t('channelLogs.breakerPredatesRestart') }}
            </div>
            <div v-if="breakerEvidence.lastFailureAt" class="text-caption mt-1">
              {{ t('channelLogs.breakerLastFailure', { time: formatFullTime(breakerEvidence.lastFailureAt) }) }}
            </div>
            <div v-if="breakerEvidence.nextRetryAt" class="text-caption mt-1">
              {{ t('channelLogs.breakerNextRetry', { time: formatFullTime(breakerEvidence.nextRetryAt) }) }}
            </div>
            <div class="text-caption mt-1">
              {{ t('channelLogs.breakerBackoff', {
                level: breakerEvidence.backoffLevel,
                failures: breakerEvidence.consecutiveFailures,
              }) }}
            </div>
          </v-alert>
        </div>

        <!-- Log list -->
        <v-list v-else density="comfortable" class="pa-0">
          <div v-if="!displayRows.length" class="text-center py-8 text-medium-emphasis text-caption">
            {{ t('channelLogs.noMatch') }}
          </div>
          <template v-for="(row, i) in displayRows" :key="row.key">
            <v-list-item
              :class="['log-item', { 'bg-error-subtle': row.log.status === 'failed', 'log-attempt-item': row.isAttempt }]"
              @click="onLogRowClick(row)"
            >
              <template #append>
                <v-tooltip
                  :text="copiedLogKey === row.key ? t('channelLogs.copiedEntry') : t('channelLogs.copyEntry')"
                  location="left"
                  content-class="ccx-tooltip"
                >
                  <template #activator="{ props: tooltipProps }">
                    <v-btn
                      v-bind="tooltipProps"
                      icon
                      size="x-small"
                      variant="flat"
                      class="log-copy-btn"
                      :class="{ 'log-copy-btn--visible': copiedLogKey === row.key }"
                      :aria-label="t('channelLogs.copyEntry')"
                      @click.stop="copyLogEntry(row.log, row.key)"
                    >
                      <v-icon size="16">{{ copiedLogKey === row.key ? 'mdi-check' : 'mdi-content-copy' }}</v-icon>
                    </v-btn>
                  </template>
                </v-tooltip>
              </template>
              <template #prepend>
                <v-chip
                  v-if="row.log.statusCode > 0"
                  :color="statusColor(row.log.statusCode)"
                  size="small"
                  variant="flat"
                  class="mr-2 font-weight-bold log-status-chip"
                  :class="{ 'log-status-chip--in-progress': isInProgress(row.log.status) }"
                >
                  {{ row.log.statusCode }}
                </v-chip>
                <v-chip
                  v-else-if="isInProgress(row.log.status)"
                  size="small"
                  variant="flat"
                  class="mr-2 font-weight-bold log-status-chip log-status-chip--placeholder log-status-chip--in-progress"
                >
                  <span class="log-status-chip__placeholder">000</span>
                </v-chip>
                <v-chip v-else size="small" color="default" variant="flat" class="mr-2 font-weight-bold log-status-chip">
                  -
                </v-chip>
              </template>
              <v-list-item-title class="d-flex align-center ga-2 flex-wrap log-summary">
                <span class="text-medium-emphasis log-meta">{{ formatTime(row.log.timestamp) }}</span>
                <v-chip v-if="row.log.status" size="small" :color="requestStatusColor(row.log.status)" variant="tonal" class="text-uppercase">
                  {{ requestStatusText(row.log.status) }}
                </v-chip>
                <v-chip v-if="!row.isAttempt && row.group.entries.length > 1" size="small" color="primary" variant="flat">
                  {{ t('channelLogs.attempts', { count: row.group.entries.length }) }}
                </v-chip>
                <v-chip v-if="row.log.interfaceType" size="small" :color="interfaceTypeColor(row.log.interfaceType)" variant="tonal" class="text-uppercase">
                  {{ row.log.interfaceType }}
                </v-chip>
                <v-chip v-if="row.log.agentRole === 'subagent'" size="small" color="warning" variant="tonal" class="text-uppercase">
                  SUBAGENT
                  <span v-if="row.log.agentConfidence === 'heuristic'" class="ml-1" style="opacity:.7">?</span>
                </v-chip>
                <v-chip v-else-if="row.log.agentRole === 'main'" size="small" color="success" variant="tonal" class="text-uppercase">
                  MAIN
                </v-chip>
                <v-chip v-if="row.log.operation" size="small" color="info" variant="tonal" class="text-uppercase">
                  {{ row.log.operation }}
                </v-chip>
                <v-chip v-if="row.log.requestSource === 'capability_test'" size="small" color="warning" variant="tonal">
                  {{ t('channelLogs.sourceCapabilityTest') }}
                </v-chip>
                <v-chip v-else-if="row.log.requestSource === 'healthcheck'" size="small" color="default" variant="tonal">
                  {{ t('channelLogs.sourceHealthCheck') }}
                </v-chip>
                <v-chip v-if="row.log.racingStatus === 'won'" size="small" color="success" variant="flat" prepend-icon="mdi-flag-checkered">
                  {{ t('channelLogs.racing.won') }}
                </v-chip>
                <v-chip v-if="row.log.racingStatus === 'lost'" size="small" color="default" variant="outlined" prepend-icon="mdi-flag-outline">
                  {{ t('channelLogs.racing.lost') }}
                </v-chip>
                <span v-if="row.log.originalModel" class="text-medium-emphasis log-meta">{{ row.log.originalModel }} →</span>
                <span class="font-weight-medium log-model">{{ row.log.model }}</span>
                <v-chip
                  v-if="singleReasoningEffort(row.log)"
                  size="small"
                  :color="reasoningEffortColor(singleReasoningEffort(row.log))"
                  variant="tonal"
                  class="log-reasoning-chip"
                  :title="singleReasoningEffort(row.log)"
                >
                  {{ formatReasoningEffort(singleReasoningEffort(row.log)) }}
                </v-chip>
                <template v-else>
                  <v-chip
                    v-if="row.log.originalReasoningEffort"
                    size="small"
                    :color="reasoningEffortColor(row.log.originalReasoningEffort)"
                    variant="tonal"
                    class="log-reasoning-chip"
                    :title="row.log.originalReasoningEffort"
                  >
                    {{ t('channelLogs.reasoning.original') }} {{ formatReasoningEffort(row.log.originalReasoningEffort) }}
                  </v-chip>
                  <v-chip
                    v-if="row.log.actualReasoningEffort"
                    size="small"
                    :color="reasoningEffortColor(row.log.actualReasoningEffort)"
                    variant="flat"
                    class="log-reasoning-chip"
                    :title="row.log.actualReasoningEffort"
                  >
                    {{ t('channelLogs.reasoning.actual') }} {{ formatReasoningEffort(row.log.actualReasoningEffort) }}
                  </v-chip>
                </template>
                <code class="text-caption bg-surface pa-1 rounded log-inline-code log-key-mask">{{ row.log.keyMask }}</code>
                <code v-if="row.log.baseUrl" class="text-caption bg-surface pa-1 rounded log-inline-code log-base-url" :title="row.log.baseUrl">{{ row.log.baseUrl }}</code>
                <v-chip v-if="row.log.isRetry" size="small" color="warning" variant="tonal">{{ t('channelLogs.retry') }}</v-chip>
                <template v-if="calculateDurations(row.log)">
                  <span v-if="calculateDurations(row.log)!.connectMs !== null" class="text-medium-emphasis log-meta">
                    {{ t('channelLogs.duration.connect') }} {{ formatDurationSeconds(calculateDurations(row.log)!.connectMs!) }}
                  </span>
                  <span v-if="calculateDurations(row.log)!.firstByteMs !== null" class="text-medium-emphasis log-meta">
                    {{ t('channelLogs.duration.firstByte') }} {{ formatDurationSeconds(calculateDurations(row.log)!.firstByteMs!) }}
                  </span>
                  <span v-if="calculateDurations(row.log)!.totalMs !== null" class="text-medium-emphasis log-meta">
                    {{ t('channelLogs.duration.total') }} {{ formatDurationSeconds(calculateDurations(row.log)!.totalMs!) }}
                  </span>
                </template>
                <span v-else class="text-medium-emphasis log-meta">{{ formatDurationSeconds(row.log.durationMs) }}</span>
                <v-chip v-if="row.log.selectionReason" size="small" color="secondary" variant="tonal" :title="row.log.selectionReason">
                  {{ t('channelLogs.selectionReason') }} {{ row.log.selectionReason }}
                </v-chip>
                <v-chip
                  v-if="row.log.autopilotTraceUid"
                  size="small"
                  color="info"
                  variant="outlined"
                  prepend-icon="mdi-chart-timeline-variant"
                  :title="t('channelLogs.viewAutopilotTrace')"
                  @click.stop="openAutopilotTrace(row.log.autopilotTraceUid)"
                >
                  {{ t('channelLogs.autopilotTrace') }} {{ row.log.autopilotTraceUid.slice(0, 12) }}...
                </v-chip>
                <span v-if="row.log.firstContentLatencyMs" class="text-medium-emphasis log-meta">
                  {{ t('channelLogs.duration.firstContent') }} {{ formatDurationSeconds(row.log.firstContentLatencyMs) }}
                </span>
                <span v-if="row.log.maxStreamIdleMs" class="text-medium-emphasis log-meta">
                  {{ t('channelLogs.duration.maxStreamIdle') }} {{ formatDurationSeconds(row.log.maxStreamIdleMs) }}
                </span>
                <span v-if="row.log.maxToolCallIdleMs" class="text-medium-emphasis log-meta">
                  {{ t('channelLogs.duration.maxToolCallIdle') }} {{ formatDurationSeconds(row.log.maxToolCallIdleMs) }}
                </span>
                <v-icon v-if="!row.isAttempt && isGroupExpandable(row.group)" size="small" class="log-group-chevron">
                  {{ expandedGroupKey === row.group.key ? 'mdi-chevron-up' : 'mdi-chevron-down' }}
                </v-icon>
              </v-list-item-title>
            </v-list-item>
            <!-- 展开的诊断详情 -->
            <v-expand-transition>
              <div v-if="expandedLogKey === row.key && hasLogDetails(row.log)" class="px-4 py-2 log-detail-info">
                <div v-if="row.log.errorInfo">
                  {{ formatErrorInfo(row.log.errorInfo) }}
                </div>
              </div>
            </v-expand-transition>
            <v-divider v-if="i < displayRows.length - 1" />
          </template>
        </v-list>
      </v-card-text>
    </v-card>
  </v-dialog>

  <!-- Autopilot Trace 详情对话框 -->
  <AutopilotTraceDetailDialog
    v-model="autopilotDetailOpen"
    :trace-uid="autopilotDetailTraceUid"
  />
</template>

<script setup lang="ts">
import { ref, computed, watch, onUnmounted } from 'vue'
import { api, type ChannelBreakerEvidence, type ChannelKind, type ChannelLogEntry, type ChannelProtocolRoute } from '../services/api'
import { useI18n } from '../i18n'
import { useGlobalTick } from '../composables/useGlobalTick'
import { writeClipboardText } from '../utils/clipboard'
import AutopilotTraceDetailDialog from './AutopilotTraceDetailDialog.vue'

const props = defineProps<{
  modelValue: boolean
  channelIndex: number
  channelName: string
  channelType: ChannelKind
  protocolRoutes?: ChannelProtocolRoute[]
}>()

defineEmits<{
  (_e: 'update:modelValue', _v: boolean): void
}>()
const { t } = useI18n()

const logs = ref<ChannelLogEntry[]>([])
const breakerEvidence = ref<ChannelBreakerEvidence | null>(null)
const isLoading = ref(false)
const autoRefresh = ref(true)
// 展开状态以稳定 key 记录（correlationId/requestId），列表刷新重排后不会错位
const expandedGroupKey = ref<string | null>(null)
const expandedLogKey = ref<string | null>(null)
const copiedLogKey = ref<string | null>(null)
let copyLogResetTimer: ReturnType<typeof setTimeout> | null = null

// 日志视图过滤：全部（组可展开看尝试明细）/ 仅最终交付（折叠组行，不展开明细）/ 含竞速放大（仅竞速相关组）
type LogViewMode = 'all' | 'final' | 'racing'
const logViewMode = ref<LogViewMode>('all')

interface LogGroup {
  key: string
  entries: ChannelLogEntry[]
  representative: ChannelLogEntry
  hasRacing: boolean
}

interface LogDisplayRow {
  key: string
  log: ChannelLogEntry
  group: LogGroup
  isAttempt: boolean
}

// Autopilot Trace 详情对话框
const autopilotDetailOpen = ref(false)
const autopilotDetailTraceUid = ref('')
function openAutopilotTrace(traceUid: string) {
  autopilotDetailTraceUid.value = traceUid
  autopilotDetailOpen.value = true
}

// 全局 tick（3s），visibility hidden 时自动暂停
const logsTick = useGlobalTick(3000, 'ChannelLogs')
let pollingActive = false
const startPolling = () => { pollingActive = true }
const stopPolling = () => { pollingActive = false }

const copyLogEntry = async (log: ChannelLogEntry, key: string) => {
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

const isRacingLost = (log: ChannelLogEntry): boolean =>
  log.racingStatus === 'lost' || log.status === 'racing_lost'

// 组的「最终结局」：成功交付的那条 > 最新的非竞速败出 > 最新一条（entries 已按时间倒序，越靠前越新）
const pickRepresentative = (entries: ChannelLogEntry[]): ChannelLogEntry => {
  return entries.find(e => e.success && e.status === 'completed')
    ?? entries.find(e => !isRacingLost(e))
    ?? entries[0]
}

const hasRacingInfo = (log: ChannelLogEntry): boolean =>
  Boolean(log.racingRole) || Boolean(log.racingStatus) || (log.selectionReason ?? '').toLowerCase().includes('racing')

// 按用户请求关联 ID 折叠：同一 correlationId 的多条上游尝试归为一组；无 ID 的条目自成一组
const groupLogs = (entries: ChannelLogEntry[]): LogGroup[] => {
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

// 50 条截断作用于折叠后的组（即 50 组），对应原来的 50 条上限
const logGroups = computed(() => groupLogs(logs.value).slice(0, 50))

const visibleGroups = computed(() =>
  logViewMode.value === 'racing' ? logGroups.value.filter(g => g.hasRacing) : logGroups.value
)

// 拍平为渲染行：组行（最终结局）+ 展开时的尝试明细行，明细行复用同一渲染
const displayRows = computed<LogDisplayRow[]>(() => {
  const rows: LogDisplayRow[] = []
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

const onLogRowClick = (row: LogDisplayRow) => {
  if (!row.isAttempt && isGroupExpandable(row.group)) {
    expandedGroupKey.value = expandedGroupKey.value === row.group.key ? null : row.group.key
    return
  }
  expandedLogKey.value = expandedLogKey.value === row.key ? null : row.key
}

const hasLogDetails = (log: ChannelLogEntry): boolean => {
  return Boolean(log.errorInfo?.trim())
}

const statusColor = (code: number): string => {
  if (code >= 200 && code < 300) return 'success'
  if (code >= 400 && code < 500) return 'warning'
  return 'error'
}

const requestStatusColor = (status: string): string => {
  switch (status) {
    case 'completed': return 'success'
    case 'failed': return 'error'
    case 'cancelled':
    case 'canceled': return 'warning'
    case 'racing_lost': return 'secondary'
    case 'streaming': return 'info'
    case 'first_byte': return 'primary'
    case 'connecting': return 'warning'
    case 'pending': return 'default'
    default: return 'default'
  }
}

const requestStatusText = (status: string): string => {
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
    default: return status
  }
}

const isInProgress = (status: string): boolean => {
  return ['pending', 'connecting', 'first_byte', 'streaming'].includes(status)
}

const calculateDurations = (log: ChannelLogEntry) => {
  if (!log.startTime) return null

  const start = new Date(log.startTime).getTime()
  const connected = log.connectedAt ? new Date(log.connectedAt).getTime() : null
  const firstByte = log.firstByteAt ? new Date(log.firstByteAt).getTime() : null
  const completed = log.completedAt ? new Date(log.completedAt).getTime() : null

  return {
    connectMs: connected ? connected - start : null,
    firstByteMs: firstByte ? firstByte - start : null,
    totalMs: completed ? completed - start : null
  }
}

const formatDurationSeconds = (durationMs: number): string => {
  const seconds = durationMs / 1000
  return `${Number.parseFloat(seconds.toPrecision(3))}s`
}

const formatReasoningEffort = (effort: string): string => {
  const value = effort.trim()
  return value.length > 24 ? `${value.slice(0, 21)}...` : value
}

const normalizedReasoningEffort = (effort?: string): string => effort?.trim() || ''

const singleReasoningEffort = (log: ChannelLogEntry): string => {
  const original = normalizedReasoningEffort(log.originalReasoningEffort)
  const actual = normalizedReasoningEffort(log.actualReasoningEffort)
  if (!original) return actual
  if (!actual) return original
  return original.toLowerCase() === actual.toLowerCase() ? actual : ''
}

const reasoningEffortColor = (effort: string): string => {
  const value = effort.toLowerCase()
  if (value === 'none' || value === 'disabled' || value === 'false') return 'default'
  if (value === 'minimal' || value === 'low') return 'info'
  if (value === 'high' || value === 'xhigh' || value === 'max') return 'warning'
  if (value.startsWith('budget=')) return 'secondary'
  return 'primary'
}

const formatErrorInfo = (errorInfo: string): string => {
  const text = errorInfo.trim()
  if (text.startsWith('upstream returned empty stream response')) {
    const diagnostic = text.replace(/^upstream returned empty stream response:?\s*/, '').trim()
    return diagnostic
      ? `空流响应：上游 HTTP 200 返回 SSE 流后结束，但未检测到文本或语义内容（${diagnostic}）`
      : '空流响应：上游 HTTP 200 返回 SSE 流后结束，但未检测到文本或语义内容'
  }
  if (text.startsWith('upstream returned empty non-stream response')) {
    return '空响应：上游 HTTP 200 返回非流式响应，但未检测到文本或语义内容'
  }
  if (text.startsWith('stream first content timeout')) {
    return '流式首内容超时：上游 HTTP 200 后未在配置窗口内返回有效内容'
  }
  if (text.startsWith('stream stalled after first content')) {
    return '流式断流：首个有效内容后未在配置窗口内继续返回上游活动'
  }
  return errorInfo
}

const interfaceTypeColor = (type: string): string => {
  switch (type.toLowerCase()) {
    case 'messages': return 'primary'
    case 'chat': return 'success'
    case 'responses': return 'secondary'
    case 'gemini': return 'info'
    case 'images': return 'success'
    case 'vectors': return 'primary'
    default: return 'default'
  }
}

const formatTime = (ts: string): string => {
  const d = new Date(ts)
  return d.toLocaleTimeString(undefined, { hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

// 熔断依据可能跨天（重启前的历史失败），需要带日期而非仅时刻
const formatFullTime = (ts: string): string => {
  const d = new Date(ts)
  if (Number.isNaN(d.getTime())) return ts
  return d.toLocaleString()
}

const fetchLogs = async () => {
  isLoading.value = true
  try {
    const fallbackRoute: ChannelProtocolRoute = {
      kind: props.channelType,
      index: props.channelIndex,
      name: props.channelName,
      serviceType: '',
    }
    const routes = props.protocolRoutes?.length ? props.protocolRoutes : [fallbackRoute]
    const uniqueRoutes = Array.from(
      new Map(routes.map(route => [`${route.kind}:${route.index}`, route])).values()
    )
    const results = await Promise.allSettled(
      uniqueRoutes.map(route => api.getChannelLogs(route.kind, route.index))
    )
    logs.value = results
      .flatMap(result => result.status === 'fulfilled' ? (result.value.logs || []) : [])
      .sort((a, b) => new Date(b.timestamp).getTime() - new Date(a.timestamp).getTime())
    // 日志为空但渠道熔断时，后端会给出熔断成因依据；取最严重的一条展示
    breakerEvidence.value = results
      .flatMap(result => (result.status === 'fulfilled' && result.value.breakerEvidence)
        ? [result.value.breakerEvidence]
        : [])
      .sort((a, b) => (a.circuitState === 'open' ? 0 : 1) - (b.circuitState === 'open' ? 0 : 1))[0] ?? null
    for (const result of results) {
      if (result.status === 'rejected') console.error('Failed to fetch channel route logs:', result.reason)
    }
  } catch (e) {
    console.error('Failed to fetch channel logs:', e)
  } finally {
    isLoading.value = false
  }
}

// 注册 tick 回调（global tick，与其他 3s 组件共用 setInterval）
logsTick.onTick(() => {
  if (pollingActive) fetchLogs()
})

// 打开时加载，关闭时停止
watch(() => props.modelValue, (open) => {
  if (open) {
    logs.value = []
    expandedGroupKey.value = null
    expandedLogKey.value = null
    fetchLogs()
    if (autoRefresh.value) startPolling()
  } else {
    stopPolling()
  }
})

// 对话框打开状态下切换渠道时重新加载
watch([() => props.channelIndex, () => props.channelType, () => props.protocolRoutes], () => {
  if (props.modelValue) {
    logs.value = []
    expandedGroupKey.value = null
    expandedLogKey.value = null
    fetchLogs()
  }
})

// 切换过滤模式时收起展开态，避免「仅最终交付」下残留明细
watch(logViewMode, () => {
  expandedGroupKey.value = null
  expandedLogKey.value = null
})

watch(autoRefresh, (v) => {
  if (v && props.modelValue) startPolling()
  else stopPolling()
})

// 对话框打开时自动开始轮询
watch(() => props.modelValue, (open) => {
  if (open && autoRefresh.value) {
    startPolling()
  }
}, { immediate: true })

// Esc 关闭由 Vuetify 原生按 overlay 栈处理（仅关最上层，内嵌 Trace 详情对话框同开时不连环关闭）

onUnmounted(() => {
  stopPolling()
  if (copyLogResetTimer) clearTimeout(copyLogResetTimer)
})
</script>

<style scoped>
.auto-refresh-btn :deep(.v-btn__content) {
  font-size: 0.8125rem;
  letter-spacing: 0;
  line-height: 1.5;
}

.channel-logs-scroll {
  max-height: 500px;
  overflow-y: auto;
}

.log-item {
  --log-copy-gutter: 52px;
  position: relative;
  padding-top: 10px;
  padding-inline-end: var(--log-copy-gutter) !important;
  padding-bottom: 10px;
}

.log-attempt-item {
  background: rgba(var(--v-theme-on-surface), 0.02);
  padding-inline-start: 32px;
}

.log-group-chevron {
  color: rgba(var(--v-theme-on-surface), 0.54);
}

.log-item :deep(.v-list-item__append) {
  position: absolute;
  top: 8px;
  inset-inline-end: calc(8px - var(--log-copy-gutter));
  z-index: 1;
  margin-inline-start: 0;
}

.log-copy-btn {
  background: rgba(var(--v-theme-surface), 0.94) !important;
  box-shadow: 0 6px 16px rgba(15, 23, 42, 0.14);
  opacity: 0;
  transition: opacity 0.15s ease, transform 0.15s ease, color 0.15s ease;
  transform: translateY(-2px);
}

.log-item:hover .log-copy-btn,
.log-copy-btn:focus-visible,
.log-copy-btn--visible {
  opacity: 1;
  transform: translateY(0);
}

.log-copy-btn--visible {
  color: rgb(var(--v-theme-success)) !important;
}

.log-status-chip {
  min-width: 52px;
  justify-content: center;
}

.log-status-chip--in-progress {
  position: relative;
  overflow: hidden;
  isolation: isolate;
  animation: log-chip-neon-pulse 1.8s ease-in-out infinite;
}

.log-status-chip--in-progress::before {
  content: '';
  position: absolute;
  inset: 0;
  border-radius: inherit;
  background:
    radial-gradient(circle at center, rgba(var(--v-theme-primary), 0.34) 0%, rgba(var(--v-theme-primary), 0.2) 45%, rgba(var(--v-theme-primary), 0.06) 100%);
  opacity: 0.88;
  z-index: -1;
}

@keyframes log-chip-neon-pulse {
  0%, 100% {
    box-shadow:
      0 0 0 1px rgba(var(--v-theme-primary), 0.28),
      0 0 10px rgba(var(--v-theme-primary), 0.22),
      0 0 18px rgba(var(--v-theme-primary), 0.12);
    filter: saturate(1);
  }
  50% {
    box-shadow:
      0 0 0 1px rgba(var(--v-theme-primary), 0.48),
      0 0 14px rgba(var(--v-theme-primary), 0.36),
      0 0 28px rgba(var(--v-theme-primary), 0.22);
    filter: saturate(1.12);
  }
}

.log-status-chip--placeholder {
  background: transparent !important;
  box-shadow: inset 0 0 0 1px rgba(var(--v-theme-on-surface), 0.12);
}

.log-status-chip__placeholder {
  opacity: 0;
  user-select: none;
}

.log-summary {
  font-size: 0.875rem;
  line-height: 1.6;
}

/* 右上过滤切换：标签禁换行防纵向堆叠，标题过长优先收缩标题（text-truncate 省略号） */
.dialog-title {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

/* Vuetify reset 层让按钮字号继承容器（card-title 22px），须显式钉住，
   口径对齐图表控件 chart-control-btn（11px/600） */
.log-view-toggle :deep(.v-btn) {
  min-width: auto;
  padding-inline: 8px;
  font-size: 11px !important;
  font-weight: 600 !important;
  letter-spacing: 0 !important;
}

.log-view-toggle :deep(.v-btn__content) {
  white-space: nowrap;
}

.log-meta {
  font-size: 0.875rem;
}

.log-inline-code {
  display: inline-block;
  font-family: ui-monospace, SFMono-Regular, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace;
  line-height: 1.3;
  vertical-align: middle;
}

.log-key-mask {
  white-space: nowrap;
}

.breaker-evidence-alert {
  max-width: 420px;
}

.log-base-url {
  white-space: nowrap;
}

.log-model {
  font-size: 0.875rem;
}

.log-detail-info {
  background: rgba(var(--v-theme-surface-variant), 0.3);
  white-space: pre-wrap;
  word-break: break-all;
  font-size: 0.875rem;
  line-height: 1.6;
}

.bg-error-subtle {
  background: rgba(var(--v-theme-error), 0.05);
}
</style>
