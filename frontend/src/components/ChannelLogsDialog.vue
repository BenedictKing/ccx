<template>
  <v-dialog :model-value="modelValue" max-width="900" @update:model-value="$emit('update:modelValue', $event)">
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
        </div>

        <!-- Log list -->
        <v-list v-else density="comfortable" class="pa-0">
          <template v-for="(log, i) in logs" :key="getLogCopyKey(log, i)">
            <v-list-item
              :class="['log-item', { 'bg-error-subtle': log.status === 'failed' }]"
              :aria-expanded="expandedIndex === i"
              :aria-controls="`channel-log-details-${i}`"
              @click="toggleExpand(i)"
            >
              <template #prepend>
                <v-chip
                  v-if="log.statusCode > 0"
                  :color="statusColor(log.statusCode)"
                  size="small"
                  variant="flat"
                  class="font-weight-bold log-status-chip"
                  :class="{ 'log-status-chip--in-progress': isInProgress(log.status) }"
                >
                  {{ log.statusCode }}
                </v-chip>
                <v-chip
                  v-else-if="isInProgress(log.status)"
                  size="small"
                  variant="flat"
                  class="font-weight-bold log-status-chip log-status-chip--placeholder log-status-chip--in-progress"
                >
                  <span class="log-status-chip__placeholder">000</span>
                </v-chip>
                <v-chip v-else size="small" color="default" variant="flat" class="font-weight-bold log-status-chip">
                  -
                </v-chip>
              </template>

              <div class="log-entry">
                <div class="log-heading">
                  <span v-if="log.originalModel && log.originalModel !== log.model" class="text-medium-emphasis log-original-model">
                    {{ log.originalModel }} →
                  </span>
                  <span class="font-weight-medium log-model">{{ log.model }}</span>
                  <v-chip v-if="log.interfaceType" size="x-small" :color="interfaceTypeColor(log.interfaceType)" variant="tonal" class="text-uppercase">
                    {{ log.interfaceType }}
                  </v-chip>
                  <v-chip
                    v-if="singleReasoningEffort(log)"
                    size="x-small"
                    :color="reasoningEffortColor(singleReasoningEffort(log))"
                    variant="tonal"
                    class="log-reasoning-chip"
                    :title="singleReasoningEffort(log)"
                  >
                    {{ formatReasoningEffort(singleReasoningEffort(log)) }}
                  </v-chip>
                  <v-chip v-if="log.agentRole === 'subagent'" size="x-small" color="warning" variant="tonal" class="text-uppercase">
                    SUBAGENT<span v-if="log.agentConfidence === 'heuristic'" class="ml-1">?</span>
                  </v-chip>
                  <v-chip v-else-if="log.agentRole === 'main'" size="x-small" color="success" variant="tonal" class="text-uppercase">
                    MAIN
                  </v-chip>
                  <v-chip v-if="log.requestSource === 'capability_test'" size="x-small" color="warning" variant="tonal">
                    {{ t('channelLogs.sourceCapabilityTest') }}
                  </v-chip>
                  <v-chip v-if="log.isRetry" size="x-small" color="warning" variant="tonal">{{ t('channelLogs.retry') }}</v-chip>
                </div>

                <div class="log-summary text-medium-emphasis">
                  <span class="log-time">{{ formatTime(log.timestamp) }}</span>
                  <v-chip v-if="log.status" size="x-small" :color="requestStatusColor(log.status)" variant="tonal">
                    {{ requestStatusText(log.status) }}
                  </v-chip>
                  <span class="log-metric">
                    {{ t('channelLogs.duration.total') }} {{ formatDurationSeconds(calculateDurations(log)?.totalMs ?? log.durationMs) }}
                  </span>
                  <template v-if="(log.inputTokens || 0) > 0 || (log.outputTokens || 0) > 0">
                    <span class="log-metric">{{ t('channelLogs.input') }} {{ formatTokenCount(log.inputTokens || 0) }}</span>
                    <span class="log-metric">{{ t('channelLogs.output') }} {{ formatTokenCount(log.outputTokens || 0) }}</span>
                  </template>
                  <span v-if="log.tokensPerSecond" class="log-metric" :title="t('channelLogs.tpsDetail', { output: formatTokenCount(log.outputTokens || 0) })">
                    {{ formatTokensPerSecond(log.tokensPerSecond) }} TPS
                  </span>
                </div>

                <div v-if="log.errorInfo?.trim() && expandedIndex !== i" class="log-error-preview">
                  {{ formatErrorInfo(log.errorInfo) }}
                </div>
              </div>

              <template #append>
                <div class="log-actions">
                  <v-tooltip
                    v-if="canRetestModel(log)"
                    :text="t('channelLogs.retestModel', { model: log.model })"
                    location="left"
                    content-class="ccx-tooltip"
                  >
                    <template #activator="{ props: tooltipProps }">
                      <v-btn
                        v-bind="tooltipProps"
                        icon
                        size="x-small"
                        variant="text"
                        color="secondary"
                        class="log-action-btn"
                        :aria-label="t('channelLogs.retestModel', { model: log.model })"
                        @click.stop="retestModel(log)"
                      >
                        <v-icon size="16">mdi-test-tube</v-icon>
                      </v-btn>
                    </template>
                  </v-tooltip>
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
                        variant="text"
                        class="log-action-btn log-copy-btn"
                        :class="{ 'log-copy-btn--visible': copiedLogKey === getLogCopyKey(log, i) }"
                        :aria-label="t('channelLogs.copyEntry')"
                        @click.stop="copyLogEntry(log, i)"
                      >
                        <v-icon size="16">{{ copiedLogKey === getLogCopyKey(log, i) ? 'mdi-check' : 'mdi-content-copy' }}</v-icon>
                      </v-btn>
                    </template>
                  </v-tooltip>
                  <v-icon size="18" class="log-expand-icon" :class="{ 'log-expand-icon--expanded': expandedIndex === i }">
                    mdi-chevron-down
                  </v-icon>
                </div>
              </template>
            </v-list-item>

            <v-expand-transition>
              <div v-if="expandedIndex === i" :id="`channel-log-details-${i}`" class="log-details">
                <div v-if="log.errorInfo?.trim()" class="log-error-detail">{{ formatErrorInfo(log.errorInfo) }}</div>
                <div class="log-detail-grid">
                  <section class="log-detail-section">
                    <div class="log-detail-title">{{ t('channelLogs.details.request') }}</div>
                    <dl class="log-detail-list">
                      <div v-if="log.requestId" class="log-detail-row">
                        <dt>{{ t('channelLogs.requestId') }}</dt><dd>{{ log.requestId }}</dd>
                      </div>
                      <div v-if="log.operation" class="log-detail-row">
                        <dt>{{ t('channelLogs.operation') }}</dt><dd>{{ log.operation }}</dd>
                      </div>
                      <div v-if="log.originalReasoningEffort && !singleReasoningEffort(log)" class="log-detail-row">
                        <dt>{{ t('channelLogs.reasoning.original') }}</dt><dd>{{ log.originalReasoningEffort }}</dd>
                      </div>
                      <div v-if="log.actualReasoningEffort && !singleReasoningEffort(log)" class="log-detail-row">
                        <dt>{{ t('channelLogs.reasoning.actual') }}</dt><dd>{{ log.actualReasoningEffort }}</dd>
                      </div>
                      <div v-if="log.keyMask" class="log-detail-row">
                        <dt>{{ t('channelLogs.key') }}</dt><dd class="log-detail-code">{{ log.keyMask }}</dd>
                      </div>
                      <div v-if="log.baseUrl" class="log-detail-row">
                        <dt>{{ t('channelLogs.baseUrl') }}</dt><dd class="log-detail-code">{{ log.baseUrl }}</dd>
                      </div>
                      <div v-if="log.selectionReason" class="log-detail-row">
                        <dt>{{ t('channelLogs.selectionReason') }}</dt><dd>{{ log.selectionReason }}</dd>
                      </div>
                      <div v-if="log.selectionTraceSummary" class="log-detail-row">
                        <dt>{{ t('channelLogs.selectionTrace') }}</dt><dd>{{ log.selectionTraceSummary }}</dd>
                      </div>
                    </dl>
                  </section>

                  <section class="log-detail-section">
                    <div class="log-detail-title">{{ t('channelLogs.details.timing') }}</div>
                    <dl class="log-detail-list">
                      <template v-if="calculateDurations(log)">
                        <div v-if="calculateDurations(log)!.connectMs !== null" class="log-detail-row">
                          <dt>{{ t('channelLogs.duration.connect') }}</dt><dd>{{ formatDurationSeconds(calculateDurations(log)!.connectMs!) }}</dd>
                        </div>
                        <div v-if="calculateDurations(log)!.firstByteMs !== null" class="log-detail-row">
                          <dt>{{ t('channelLogs.duration.firstByte') }}</dt><dd>{{ formatDurationSeconds(calculateDurations(log)!.firstByteMs!) }}</dd>
                        </div>
                      </template>
                      <div class="log-detail-row">
                        <dt>{{ t('channelLogs.duration.total') }}</dt><dd>{{ formatDurationSeconds(calculateDurations(log)?.totalMs ?? log.durationMs) }}</dd>
                      </div>
                      <div v-if="log.firstContentLatencyMs" class="log-detail-row">
                        <dt>{{ t('channelLogs.duration.firstContent') }}</dt><dd>{{ formatDurationSeconds(log.firstContentLatencyMs) }}</dd>
                      </div>
                      <div v-if="log.maxStreamIdleMs" class="log-detail-row">
                        <dt>{{ t('channelLogs.duration.maxStreamIdle') }}</dt><dd>{{ formatDurationSeconds(log.maxStreamIdleMs) }}</dd>
                      </div>
                      <div v-if="log.maxToolCallIdleMs" class="log-detail-row">
                        <dt>{{ t('channelLogs.duration.maxToolCallIdle') }}</dt><dd>{{ formatDurationSeconds(log.maxToolCallIdleMs) }}</dd>
                      </div>
                    </dl>
                  </section>

                  <section v-if="(log.inputTokens || 0) > 0 || (log.outputTokens || 0) > 0 || log.contextTokens" class="log-detail-section">
                    <div class="log-detail-title">{{ t('channelLogs.details.tokens') }}</div>
                    <dl class="log-detail-list">
                      <div class="log-detail-row"><dt>{{ t('channelLogs.input') }}</dt><dd>{{ formatTokenCount(log.inputTokens || 0) }}</dd></div>
                      <div class="log-detail-row"><dt>{{ t('channelLogs.output') }}</dt><dd>{{ formatTokenCount(log.outputTokens || 0) }}</dd></div>
                      <div v-if="log.contextTokens && log.contextTokens !== (log.inputTokens || 0)" class="log-detail-row" :title="t('channelLogs.contextTokensDetail', { input: formatTokenCount(log.inputTokens || 0), cacheCreation: formatTokenCount(log.cacheCreationTokens || 0), cacheRead: formatTokenCount(log.cacheReadTokens || 0) })">
                        <dt>{{ t('channelLogs.contextTokens') }}</dt><dd>{{ formatTokenCount(log.contextTokens) }}</dd>
                      </div>
                      <div v-if="(log.cacheReadTokens || 0) > 0" class="log-detail-row">
                        <dt>{{ t('channelLogs.cacheRead') }}</dt><dd>{{ formatTokenCount(log.cacheReadTokens!) }}</dd>
                      </div>
                      <div v-if="(log.cacheCreationTokens || 0) > 0" class="log-detail-row">
                        <dt>{{ t('channelLogs.cacheWrite') }}</dt><dd>{{ formatTokenCount(log.cacheCreationTokens!) }}</dd>
                      </div>
                      <div v-if="log.tokensPerSecond" class="log-detail-row">
                        <dt>TPS</dt><dd>{{ formatTokensPerSecond(log.tokensPerSecond) }}</dd>
                      </div>
                    </dl>
                  </section>
                </div>
              </div>
            </v-expand-transition>
            <v-divider v-if="i < logs.length - 1" />
          </template>
        </v-list>
      </v-card-text>
    </v-card>
  </v-dialog>
</template>

<script setup lang="ts">
import { ref, watch, onMounted, onUnmounted } from 'vue'
import { api, type ChannelLogEntry, type ChannelLogRetestRequest } from '../services/api'
import { useI18n } from '../i18n'
import { useGlobalTick } from '../composables/useGlobalTick'

const props = defineProps<{
  modelValue: boolean
  channelIndex: number
  channelName: string
  channelType: 'messages' | 'chat' | 'responses' | 'gemini' | 'images' | 'vectors'
}>()

const emit = defineEmits<{
  (_e: 'update:modelValue', _v: boolean): void
  (_e: 'retestModel', _request: ChannelLogRetestRequest): void
}>()
const { t } = useI18n()

const logs = ref<ChannelLogEntry[]>([])
const isLoading = ref(false)
const autoRefresh = ref(true)
const expandedIndex = ref<number | null>(null)
const copiedLogKey = ref<string | null>(null)
let copyLogResetTimer: ReturnType<typeof setTimeout> | null = null

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
    // 复制完整日志，保留页面不展示的调度诊断信息。
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

const getRetestProtocol = (log: ChannelLogEntry): ChannelLogRetestRequest['protocol'] | null => {
  const protocol = (log.interfaceType || props.channelType).toLowerCase()
  if (protocol === 'messages' || protocol === 'chat' || protocol === 'responses' || protocol === 'gemini') {
    return protocol
  }
  return null
}

const canRetestModel = (log: ChannelLogEntry): boolean => {
  return !log.success && Boolean(log.model.trim()) && getRetestProtocol(log) !== null
}

const retestModel = (log: ChannelLogEntry) => {
  const protocol = getRetestProtocol(log)
  if (!protocol) return
  emit('retestModel', {
    channelId: props.channelIndex,
    protocol,
    model: log.model,
    reasoningEffort: normalizedReasoningEffort(log.actualReasoningEffort) || normalizedReasoningEffort(log.originalReasoningEffort) || undefined
  })
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

const formatTokenCount = (value: number): string => value.toLocaleString()

const formatTokensPerSecond = (value: number): string => {
  return value >= 100 ? value.toFixed(0) : value.toFixed(1)
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

const fetchLogs = async () => {
  isLoading.value = true
  try {
    const res = await api.getChannelLogs(props.channelType, props.channelIndex)
    logs.value = res.logs || []
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
watch([() => props.channelIndex, () => props.channelType], () => {
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
.channel-logs-scroll {
  max-height: 560px;
  overflow-y: auto;
}

.log-item {
  padding-top: 12px;
  padding-bottom: 12px;
}

.log-item :deep(.v-list-item__content) {
  min-width: 0;
  overflow: visible;
}

.log-item :deep(.v-list-item__prepend),
.log-item :deep(.v-list-item__append) {
  align-self: flex-start;
}

.log-item :deep(.v-list-item__append) {
  margin-inline-start: 8px;
}

.log-item :deep(.v-chip__content) {
  font-size: 11px;
}

.log-entry {
  display: grid;
  min-width: 0;
  gap: 6px;
}

.log-heading,
.log-summary {
  display: flex;
  min-width: 0;
  flex-wrap: wrap;
  align-items: center;
  gap: 4px 8px;
}

.log-heading {
  line-height: 1.5;
}

.log-model,
.log-original-model {
  overflow-wrap: anywhere;
}

.log-model {
  font-size: 0.875rem;
}

.log-original-model {
  font-size: 0.8125rem;
}

.log-summary {
  column-gap: 12px;
  font-size: 0.8125rem;
  line-height: 1.5;
}

.log-time {
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace;
  font-variant-numeric: tabular-nums;
}

.log-metric {
  white-space: nowrap;
  font-variant-numeric: tabular-nums;
}

.log-error-preview {
  overflow: hidden;
  color: rgb(var(--v-theme-error));
  font-size: 0.8125rem;
  line-height: 1.5;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.log-actions {
  display: flex;
  align-items: center;
  gap: 2px;
}

.log-action-btn {
  opacity: 0;
  transition: opacity 0.15s ease, color 0.15s ease;
}

.log-item:hover .log-action-btn,
.log-action-btn:focus-visible,
.log-copy-btn--visible {
  opacity: 1;
}

.log-copy-btn--visible {
  color: rgb(var(--v-theme-success)) !important;
}

.log-expand-icon {
  color: rgba(var(--v-theme-on-surface), 0.56);
  transition: transform 0.18s ease;
}

.log-expand-icon--expanded {
  transform: rotate(180deg);
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
  background: radial-gradient(circle at center, rgba(var(--v-theme-primary), 0.34) 0%, rgba(var(--v-theme-primary), 0.2) 45%, rgba(var(--v-theme-primary), 0.06) 100%);
  opacity: 0.88;
  z-index: -1;
}

@keyframes log-chip-neon-pulse {
  0%, 100% {
    box-shadow: 0 0 0 1px rgba(var(--v-theme-primary), 0.28), 0 0 10px rgba(var(--v-theme-primary), 0.22), 0 0 18px rgba(var(--v-theme-primary), 0.12);
    filter: saturate(1);
  }
  50% {
    box-shadow: 0 0 0 1px rgba(var(--v-theme-primary), 0.48), 0 0 14px rgba(var(--v-theme-primary), 0.36), 0 0 28px rgba(var(--v-theme-primary), 0.22);
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

.log-details {
  padding: 16px 20px 18px 80px;
  background: rgba(var(--v-theme-surface-variant), 0.24);
}

.log-error-detail {
  margin-bottom: 14px;
  padding: 10px 12px;
  border-left: 2px solid rgb(var(--v-theme-error));
  background: rgba(var(--v-theme-error), 0.06);
  color: rgb(var(--v-theme-error));
  font-size: 0.8125rem;
  line-height: 1.6;
  overflow-wrap: anywhere;
  white-space: pre-wrap;
}

.log-detail-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 16px 28px;
}

.log-detail-section:first-child {
  grid-column: 1 / -1;
}

.log-detail-section:first-child .log-detail-list {
  grid-template-columns: repeat(2, minmax(0, 1fr));
  column-gap: 28px;
}

.log-detail-title {
  margin-bottom: 6px;
  font-size: 0.75rem;
  font-weight: 600;
  letter-spacing: 0.04em;
}

.log-detail-list {
  display: grid;
  margin: 0;
  gap: 5px;
}

.log-detail-row {
  display: flex;
  min-width: 0;
  align-items: baseline;
  gap: 12px;
  font-size: 0.75rem;
  line-height: 1.55;
}

.log-detail-row dt {
  min-width: 64px;
  flex-shrink: 0;
  color: rgba(var(--v-theme-on-surface), 0.6);
}

.log-detail-row dd {
  min-width: 0;
  margin: 0;
  overflow-wrap: anywhere;
}

.log-detail-code {
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, "Liberation Mono", "Courier New", monospace;
}

.bg-error-subtle {
  background: rgba(var(--v-theme-error), 0.035);
}

@media (hover: none) {
  .log-action-btn {
    opacity: 1;
  }
}

@media (max-width: 600px) {
  .log-item {
    padding-inline: 12px;
  }

  .log-item :deep(.v-list-item__prepend) {
    margin-inline-end: 8px;
  }

  .log-status-chip {
    min-width: 44px;
  }

  .log-details {
    padding: 14px 16px;
  }

  .log-detail-grid,
  .log-detail-section:first-child .log-detail-list {
    grid-template-columns: minmax(0, 1fr);
  }

  .log-detail-section:first-child {
    grid-column: auto;
  }
}

@media (prefers-reduced-motion: reduce) {
  .log-status-chip--in-progress {
    animation: none;
  }

  .log-expand-icon,
  .log-action-btn {
    transition: none;
  }
}
</style>
