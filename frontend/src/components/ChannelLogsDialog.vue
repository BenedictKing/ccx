<template>
  <v-dialog :model-value="modelValue" max-width="800" @update:model-value="$emit('update:modelValue', $event)">
    <v-card>
      <v-card-title class="d-flex align-center justify-space-between">
        <span class="dialog-title">{{ t('channelLogs.title', { channel: channelName }) }}</span>
        <v-tooltip :text="t('app.actions.close') + ' (Esc)'" location="bottom" content-class="ccx-tooltip">
          <template #activator="{ props: tooltipProps }">
            <v-btn icon size="small" variant="text" v-bind="tooltipProps" @click="$emit('update:modelValue', false)">
              <v-icon>mdi-close</v-icon>
            </v-btn>
          </template>
        </v-tooltip>
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
          <template v-for="(log, i) in logs" :key="i">
            <v-list-item :class="['log-item', { 'bg-error-subtle': log.status === 'failed' }]" @click="toggleExpand(i)">
              <template #append>
                <v-tooltip
                  :text="copiedLogKey === getLogCopyKey(log, i) ? t('channelLogs.copiedEntry') : t('channelLogs.copyEntry')"
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
                      :class="{ 'log-copy-btn--visible': copiedLogKey === getLogCopyKey(log, i) }"
                      :aria-label="t('channelLogs.copyEntry')"
                      @click.stop="copyLogEntry(log, i)"
                    >
                      <v-icon size="16">{{ copiedLogKey === getLogCopyKey(log, i) ? 'mdi-check' : 'mdi-content-copy' }}</v-icon>
                    </v-btn>
                  </template>
                </v-tooltip>
              </template>
              <template #prepend>
                <v-chip
                  v-if="log.statusCode > 0"
                  :color="statusColor(log.statusCode)"
                  size="small"
                  variant="flat"
                  class="mr-2 font-weight-bold log-status-chip"
                  :class="{ 'log-status-chip--in-progress': isInProgress(log.status) }"
                >
                  {{ log.statusCode }}
                </v-chip>
                <v-chip
                  v-else-if="isInProgress(log.status)"
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
                <span class="text-medium-emphasis log-meta">{{ formatTime(log.timestamp) }}</span>
                <v-chip v-if="log.status" size="small" :color="requestStatusColor(log.status)" variant="tonal" class="text-uppercase">
                  {{ requestStatusText(log.status) }}
                </v-chip>
                <v-chip v-if="log.interfaceType" size="small" :color="interfaceTypeColor(log.interfaceType)" variant="tonal" class="text-uppercase">
                  {{ log.interfaceType }}
                </v-chip>
                <v-chip v-if="log.agentRole === 'subagent'" size="small" color="warning" variant="tonal" class="text-uppercase">
                  SUBAGENT
                  <span v-if="log.agentConfidence === 'heuristic'" class="ml-1" style="opacity:.7">?</span>
                </v-chip>
                <v-chip v-else-if="log.agentRole === 'main'" size="small" color="success" variant="tonal" class="text-uppercase">
                  MAIN
                </v-chip>
                <v-chip v-if="log.operation" size="small" color="info" variant="tonal" class="text-uppercase">
                  {{ log.operation }}
                </v-chip>
                <v-chip v-if="log.requestSource === 'capability_test'" size="small" color="warning" variant="tonal">
                  {{ t('channelLogs.sourceCapabilityTest') }}
                </v-chip>
                <v-chip v-else-if="log.requestSource === 'healthcheck'" size="small" color="default" variant="tonal">
                  {{ t('channelLogs.sourceHealthCheck') }}
                </v-chip>
                <span v-if="log.originalModel" class="text-medium-emphasis log-meta">{{ log.originalModel }} →</span>
                <span class="font-weight-medium log-model">{{ log.model }}</span>
                <v-chip
                  v-if="singleReasoningEffort(log)"
                  size="small"
                  :color="reasoningEffortColor(singleReasoningEffort(log))"
                  variant="tonal"
                  class="log-reasoning-chip"
                  :title="singleReasoningEffort(log)"
                >
                  {{ formatReasoningEffort(singleReasoningEffort(log)) }}
                </v-chip>
                <template v-else>
                  <v-chip
                    v-if="log.originalReasoningEffort"
                    size="small"
                    :color="reasoningEffortColor(log.originalReasoningEffort)"
                    variant="tonal"
                    class="log-reasoning-chip"
                    :title="log.originalReasoningEffort"
                  >
                    {{ t('channelLogs.reasoning.original') }} {{ formatReasoningEffort(log.originalReasoningEffort) }}
                  </v-chip>
                  <v-chip
                    v-if="log.actualReasoningEffort"
                    size="small"
                    :color="reasoningEffortColor(log.actualReasoningEffort)"
                    variant="flat"
                    class="log-reasoning-chip"
                    :title="log.actualReasoningEffort"
                  >
                    {{ t('channelLogs.reasoning.actual') }} {{ formatReasoningEffort(log.actualReasoningEffort) }}
                  </v-chip>
                </template>
                <code class="text-caption bg-surface pa-1 rounded log-inline-code log-key-mask">{{ log.keyMask }}</code>
                <code v-if="log.baseUrl" class="text-caption bg-surface pa-1 rounded log-inline-code log-base-url" :title="log.baseUrl">{{ log.baseUrl }}</code>
                <v-chip v-if="log.isRetry" size="small" color="warning" variant="tonal">{{ t('channelLogs.retry') }}</v-chip>
                <template v-if="calculateDurations(log)">
                  <span v-if="calculateDurations(log)!.connectMs !== null" class="text-medium-emphasis log-meta">
                    {{ t('channelLogs.duration.connect') }} {{ formatDurationSeconds(calculateDurations(log)!.connectMs!) }}
                  </span>
                  <span v-if="calculateDurations(log)!.firstByteMs !== null" class="text-medium-emphasis log-meta">
                    {{ t('channelLogs.duration.firstByte') }} {{ formatDurationSeconds(calculateDurations(log)!.firstByteMs!) }}
                  </span>
                  <span v-if="calculateDurations(log)!.totalMs !== null" class="text-medium-emphasis log-meta">
                    {{ t('channelLogs.duration.total') }} {{ formatDurationSeconds(calculateDurations(log)!.totalMs!) }}
                  </span>
                </template>
                <span v-else class="text-medium-emphasis log-meta">{{ formatDurationSeconds(log.durationMs) }}</span>
                <v-chip v-if="log.selectionReason" size="small" color="secondary" variant="tonal" :title="log.selectionReason">
                  {{ t('channelLogs.selectionReason') }} {{ log.selectionReason }}
                </v-chip>
                <v-chip
                  v-if="log.autopilotTraceUid"
                  size="small"
                  color="info"
                  variant="outlined"
                  prepend-icon="mdi-chart-timeline-variant"
                  :title="t('channelLogs.viewAutopilotTrace')"
                  @click.stop="openAutopilotTrace(log.autopilotTraceUid)"
                >
                  {{ t('channelLogs.autopilotTrace') }} {{ log.autopilotTraceUid.slice(0, 12) }}...
                </v-chip>
                <span v-if="log.firstContentLatencyMs" class="text-medium-emphasis log-meta">
                  {{ t('channelLogs.duration.firstContent') }} {{ formatDurationSeconds(log.firstContentLatencyMs) }}
                </span>
                <span v-if="log.maxStreamIdleMs" class="text-medium-emphasis log-meta">
                  {{ t('channelLogs.duration.maxStreamIdle') }} {{ formatDurationSeconds(log.maxStreamIdleMs) }}
                </span>
                <span v-if="log.maxToolCallIdleMs" class="text-medium-emphasis log-meta">
                  {{ t('channelLogs.duration.maxToolCallIdle') }} {{ formatDurationSeconds(log.maxToolCallIdleMs) }}
                </span>
              </v-list-item-title>
            </v-list-item>
            <!-- 展开的诊断详情 -->
            <v-expand-transition>
              <div v-if="expandedIndex === i && hasLogDetails(log)" class="px-4 py-2 log-detail-info">
                <div v-if="log.errorInfo">
                  {{ formatErrorInfo(log.errorInfo) }}
                </div>
              </div>
            </v-expand-transition>
            <v-divider v-if="i < logs.length - 1" />
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
import { ref, watch, onMounted, onUnmounted } from 'vue'
import { api, type ChannelBreakerEvidence, type ChannelKind, type ChannelLogEntry, type ChannelProtocolRoute } from '../services/api'
import { useI18n } from '../i18n'
import { useGlobalTick } from '../composables/useGlobalTick'
import AutopilotTraceDetailDialog from './AutopilotTraceDetailDialog.vue'

const props = defineProps<{
  modelValue: boolean
  channelIndex: number
  channelName: string
  channelType: ChannelKind
  protocolRoutes?: ChannelProtocolRoute[]
}>()

const emit = defineEmits<{
  (_e: 'update:modelValue', _v: boolean): void
}>()
const { t } = useI18n()

const logs = ref<ChannelLogEntry[]>([])
const breakerEvidence = ref<ChannelBreakerEvidence | null>(null)
const isLoading = ref(false)
const autoRefresh = ref(true)
const expandedIndex = ref<number | null>(null)
const copiedLogKey = ref<string | null>(null)
let copyLogResetTimer: ReturnType<typeof setTimeout> | null = null

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

const getLogCopyKey = (log: ChannelLogEntry, index: number): string => {
  return log.requestId || `${log.timestamp}-${index}`
}

const writeClipboardText = async (text: string) => {
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

const copyLogEntry = async (log: ChannelLogEntry, index: number) => {
  try {
    await writeClipboardText(JSON.stringify(log, null, 2))
    copiedLogKey.value = getLogCopyKey(log, index)
    if (copyLogResetTimer) clearTimeout(copyLogResetTimer)
    copyLogResetTimer = setTimeout(() => {
      copiedLogKey.value = null
      copyLogResetTimer = null
    }, 1600)
  } catch (e) {
    console.error('Failed to copy channel log:', e)
  }
}

const toggleExpand = (i: number) => {
  expandedIndex.value = expandedIndex.value === i ? null : i
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
      .slice(0, 50)
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
    expandedIndex.value = null
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
    expandedIndex.value = null
    fetchLogs()
  }
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

// 键盘监听
const handleKeydown = (e: KeyboardEvent) => {
  if (!props.modelValue) return
  if (e.key === 'Escape') {
    emit('update:modelValue', false)
  }
}

onMounted(() => {
  window.addEventListener('keydown', handleKeydown)
})

onUnmounted(() => {
  stopPolling()
  if (copyLogResetTimer) clearTimeout(copyLogResetTimer)
  window.removeEventListener('keydown', handleKeydown)
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
