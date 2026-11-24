<script setup lang="ts">
import { ref, watch, computed, onBeforeUnmount } from 'vue'
import { useDocumentVisibility, useIntervalFn } from '@vueuse/core'
import { Button } from '@/components/ui/button'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Skeleton } from '@/components/ui/skeleton'
import { Badge } from '@/components/ui/badge'
import { Check, Copy, Loader2, X, List } from 'lucide-vue-next'
import { useAdminApi } from '@/composables/useAdminApi'
import { useLanguage } from '@/composables/useLanguage'
import type { ChannelLogEntry, ChannelLogsResponse } from '@/services/admin-api'

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
const loading = ref(false)
const refreshing = ref(false)
const error = ref('')
const expandedKey = ref<string | null>(null)
const autoRefresh = ref(true)
const copiedLogKey = ref<string | null>(null)
let fetchPromise: Promise<void> | null = null
let copyLogResetTimer: ReturnType<typeof setTimeout> | null = null

const shouldPoll = computed(() => props.open && autoRefresh.value && visibility.value === 'visible')

async function fetchLogs(options: { silent?: boolean } = {}) {
  if (!props.open || props.channelId < 0 || fetchPromise) return fetchPromise

  const silent = options.silent || logs.value.length > 0
  if (silent) refreshing.value = true
  else loading.value = true
  error.value = ''

  fetchPromise = api.get<ChannelLogsResponse>(`/api/${props.channelType}/channels/${props.channelId}/logs`)
    .then(data => {
      logs.value = data.logs || []
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

// 展开状态绑定日志的稳定标识：轮询时新日志插入顶部，下标会整体后移
function toggleExpand(log: ChannelLogEntry, index: number) {
  const key = getLogCopyKey(log, index)
  expandedKey.value = expandedKey.value === key ? null : key
}

function getLogCopyKey(log: ChannelLogEntry, index: number) {
  return log.requestId || `${log.timestamp}-${index}`
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

async function copyLogEntry(log: ChannelLogEntry, index: number) {
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

function formatTokenCount(value: number) {
  return value.toLocaleString()
}

function formatTokensPerSecond(value: number) {
  return value >= 100 ? value.toFixed(0) : value.toFixed(1)
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

// 列表标签：一致时显示单值，被重定向时显示 "none → high"
function reasoningEffortLabel(log: ChannelLogEntry) {
  const single = singleReasoningEffort(log)
  if (single) return formatReasoningEffort(single)
  const original = normalizedReasoningEffort(log.originalReasoningEffort)
  const actual = normalizedReasoningEffort(log.actualReasoningEffort)
  if (!original || !actual) return ''
  return `${formatReasoningEffort(original)} → ${formatReasoningEffort(actual)}`
}

// 标签配色以实际发往上游的推理强度为准
function displayedReasoningEffort(log: ChannelLogEntry) {
  return singleReasoningEffort(log) || normalizedReasoningEffort(log.actualReasoningEffort)
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

const { pause, resume } = useIntervalFn(() => {
  if (shouldPoll.value) fetchLogs({ silent: true })
}, 3000, { immediate: false })

watch(() => props.open, (isOpen) => {
  if (isOpen) {
    logs.value = []
    expandedKey.value = null
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
  expandedKey.value = null
  void fetchLogs()
})

watch(shouldPoll, (polling) => {
  if (polling) resume()
  else pause()
})

function onKeyDown(e: KeyboardEvent) {
  if (!props.open || e.key !== 'Escape') return
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
            </div>

            <ScrollArea v-else type="auto" class="h-full min-h-0 w-full">
              <div class="divide-y divide-border">
                <div
                  v-for="(log, index) in logs"
                  :key="getLogCopyKey(log, index)"
                  class="group relative cursor-pointer px-4 py-3 pr-14 transition-colors hover:bg-accent/40"
                  :class="{ 'bg-destructive/5': log.status === 'failed' }"
                  @click="toggleExpand(log, index)"
                >
                  <Button
                    variant="outline"
                    size="icon-sm"
                    class="absolute right-3 top-3 h-7 w-7 bg-card/95 opacity-0 shadow-sm transition-opacity group-hover:opacity-100 focus-visible:opacity-100"
                    :class="{ 'border-emerald-500/40 text-emerald-600 opacity-100 dark:text-emerald-300': copiedLogKey === getLogCopyKey(log, index) }"
                    :title="copiedLogKey === getLogCopyKey(log, index) ? t('channelLogs.copiedEntry') : t('channelLogs.copyEntry')"
                    :aria-label="t('channelLogs.copyEntry')"
                    @click.stop="copyLogEntry(log, index)"
                  >
                    <Check v-if="copiedLogKey === getLogCopyKey(log, index)" class="h-3.5 w-3.5" />
                    <Copy v-else class="h-3.5 w-3.5" />
                  </Button>
                  <div class="flex flex-wrap items-center gap-2 text-xs">
                    <span
                      v-if="log.statusCode > 0"
                      class="inline-flex min-w-10 justify-center border px-2 py-0.5 font-mono font-bold text-white"
                      :class="statusColorClass(log.statusCode)"
                    >
                      {{ log.statusCode }}
                    </span>
                    <span
                      v-else-if="isInProgress(log.status)"
                      class="inline-flex min-w-10 justify-center border border-primary/30 bg-primary/10 px-2 py-0.5 font-mono font-bold text-primary"
                    >
                      000
                    </span>
                    <span v-else class="inline-flex min-w-10 justify-center border border-border bg-muted/30 px-2 py-0.5 font-mono font-bold text-muted-foreground">-</span>

                    <span class="text-muted-foreground">{{ formatTime(log.timestamp) }}</span>
                    <span v-if="log.status" class="inline-flex border px-1.5 py-0.5 text-[10px] font-bold uppercase" :class="requestStatusClass(log.status)">
                      {{ requestStatusText(log.status) }}
                    </span>
                    <span v-if="log.interfaceType" class="inline-flex border px-1.5 py-0.5 text-[10px] font-bold uppercase" :class="interfaceTypeClass(log.interfaceType)">
                      {{ log.interfaceType }}
                    </span>
                    <span v-if="log.agentRole === 'subagent'" class="inline-flex border border-amber-500/30 bg-amber-500/10 px-1.5 py-0.5 text-[10px] font-bold uppercase text-amber-700 dark:text-amber-300">
                      SUBAGENT<span v-if="log.agentConfidence === 'heuristic'">?</span>
                    </span>
                    <span v-else-if="log.agentRole === 'main'" class="inline-flex border border-emerald-500/30 bg-emerald-500/10 px-1.5 py-0.5 text-[10px] font-bold uppercase text-emerald-700 dark:text-emerald-300">
                      MAIN
                    </span>
                    <span v-if="log.operation" class="inline-flex border border-cyan-500/30 bg-cyan-500/10 px-1.5 py-0.5 text-[10px] font-bold uppercase text-cyan-700 dark:text-cyan-300">
                      {{ log.operation }}
                    </span>
                    <span v-if="log.requestSource === 'capability_test'" class="inline-flex border border-amber-500/30 bg-amber-500/10 px-1.5 py-0.5 text-[10px] font-bold text-amber-700 dark:text-amber-300">
                      {{ t('channelLogs.sourceCapabilityTest') }}
                    </span>
                    <span v-if="log.subagentPolicyApplied" class="inline-flex border border-primary/30 bg-primary/10 px-1.5 py-0.5 text-[10px] font-bold text-primary" :title="t('subagentRouting.modelLabel')">
                      {{ log.subagentPolicyModel || t('subagentRouting.keepRequestModel') }}
                    </span>
                    <span v-if="log.originalModel" class="text-muted-foreground">{{ log.originalModel }} →</span>
                    <span class="font-medium">{{ log.model }}</span>
                    <span
                      v-if="reasoningEffortLabel(log)"
                      class="inline-flex border px-1.5 py-0.5 text-[10px] font-bold"
                      :class="reasoningEffortClass(displayedReasoningEffort(log))"
                      :title="reasoningEffortLabel(log)"
                    >
                      {{ reasoningEffortLabel(log) }}
                    </span>
                    <code class="max-w-[130px] truncate bg-secondary px-1 py-0.5 font-mono text-[10px] text-muted-foreground">{{ log.keyMask }}</code>
                    <code v-if="log.baseUrl" class="max-w-[220px] truncate bg-secondary px-1 py-0.5 font-mono text-[10px] text-muted-foreground" :title="log.baseUrl">
                      {{ log.baseUrl }}
                    </code>
                    <span v-if="log.isRetry" class="inline-flex border border-amber-500/30 bg-amber-500/10 px-1.5 py-0.5 text-[10px] font-bold text-amber-700 dark:text-amber-300">
                      {{ t('channelLogs.retry') }}
                    </span>
                    <template v-if="calculateDurations(log)">
                      <span v-if="calculateDurations(log)!.connectMs !== null" class="text-muted-foreground">
                        {{ t('channelLogs.duration.connect') }} {{ formatDurationSeconds(calculateDurations(log)!.connectMs!) }}
                      </span>
                      <span v-if="calculateDurations(log)!.firstByteMs !== null" class="text-muted-foreground">
                        {{ t('channelLogs.duration.firstByte') }} {{ formatDurationSeconds(calculateDurations(log)!.firstByteMs!) }}
                      </span>
                      <span v-if="calculateDurations(log)!.totalMs !== null" class="text-muted-foreground">
                        {{ t('channelLogs.duration.total') }} {{ formatDurationSeconds(calculateDurations(log)!.totalMs!) }}
                      </span>
                    </template>
                    <span v-else class="text-muted-foreground">{{ formatDurationSeconds(log.durationMs) }}</span>
                    <span
                      v-if="log.contextTokens"
                      class="text-muted-foreground"
                      :title="t('channelLogs.contextTokensDetail', { input: formatTokenCount(log.inputTokens || 0), cacheCreation: formatTokenCount(log.cacheCreationTokens || 0), cacheRead: formatTokenCount(log.cacheReadTokens || 0) })"
                    >
                      {{ t('channelLogs.contextTokens') }} {{ formatTokenCount(log.contextTokens) }}
                    </span>
                    <span
                      v-if="log.tokensPerSecond"
                      class="text-muted-foreground"
                      :title="t('channelLogs.tpsDetail', { output: formatTokenCount(log.outputTokens || 0) })"
                    >
                      {{ formatTokensPerSecond(log.tokensPerSecond) }} TPS
                    </span>
                  </div>

                  <div v-if="expandedKey === getLogCopyKey(log, index) && log.errorInfo" class="mt-2 border border-destructive/20 bg-destructive/10 p-2 text-xs text-destructive">
                    {{ log.errorInfo }}
                  </div>
                </div>
              </div>
            </ScrollArea>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
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
