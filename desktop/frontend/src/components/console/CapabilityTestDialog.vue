<script setup lang="ts">
import { ref, computed, watch, onBeforeUnmount } from 'vue'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Input } from '@/components/ui/input'
import { Progress } from '@/components/ui/progress'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Switch } from '@/components/ui/switch'
import { Dialog, DialogContent, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Loader2, X, Play, Square, ArrowRight, CheckCircle2, XCircle, Clock, Gauge, Link2 } from 'lucide-vue-next'
import { useCapabilityTests } from '@/composables/useCapabilityTests'
import { useLanguage } from '@/composables/useLanguage'
import CapabilityModelResultBadge from '@/components/console/CapabilityModelResultBadge.vue'
import type { CapabilityProtocolJobResult } from '@/services/admin-api'
import { cn } from '@/lib/utils'

interface Props {
  open: boolean
  channelType: string
  channelId: number
  channelName: string
}

const props = defineProps<Props>()
const emit = defineEmits<{
  (e: 'close'): void
  (e: 'copyToTab', targetProtocol: string, serviceProtocol: string): void
}>()

const { t } = useLanguage()
const {
  activeJob,
  snapshot,
  cancelling,
  error,
  prepareChannelSession,
  startProtocolTest,
  fetchSnapshot,
  cancelActiveTests,
  retryModelForProtocol,
  copyToTab,
  createModelMapping,
  closeDialog,
  protocolResults,
  compatibleProtocols,
  outcome,
  isActive,
  state,
} = useCapabilityTests()

const isStarting = ref(false)
const DEFAULT_CAPABILITY_TEST_RPM = 30
const rpmValue = ref(DEFAULT_CAPABILITY_TEST_RPM)
const copyingKey = ref('')
const copyMessage = ref<{ type: 'success' | 'error'; text: string } | null>(null)
// 以渠道认可模型列表为探测范围（随下次测试启动生效）
const useChannelModels = ref(false)

// 加载 snapshot
watch(() => props.open, async (isOpen) => {
  if (isOpen) {
    copyingKey.value = ''
    copyMessage.value = null
    window.addEventListener('keydown', onKeyDown)
    prepareChannelSession(props.channelType, props.channelId, props.channelName)
    await fetchSnapshot(props.channelType, props.channelId, props.channelType, props.channelName)
  } else {
    window.removeEventListener('keydown', onKeyDown)
    closeDialog()
  }
}, { immediate: true })

const currentJob = computed(() => activeJob.value)
const progress = computed(() => currentJob.value?.progress)

const progressPercent = computed(() => {
  if (!progress.value || !progress.value.totalModels) return 0
  return Math.round((progress.value.completedModels / progress.value.totalModels) * 100)
})

const runMode = computed(() => currentJob.value?.runMode ?? 'fresh')
const displayOutcome = computed(() => outcome.value)
const hasNoCompatibleProtocolsYet = computed(() => compatibleProtocols.value.length === 0)

// ── 协议排序 ──

const PROTOCOL_ORDER = ['messages', 'responses', 'chat', 'gemini']

function sortProtocolOrder(proto: string): number {
  if (proto.includes('->')) return 0
  const idx = PROTOCOL_ORDER.indexOf(proto)
  return idx >= 0 ? idx + 1 : PROTOCOL_ORDER.length + 1
}

const sortedTests = computed(() => {
  return [...protocolResults.value].sort((a, b) => sortProtocolOrder(a.protocol) - sortProtocolOrder(b.protocol))
})

// ── 协议显示名/颜色 ──

const PROTOCOL_COLORS: Record<string, string> = {
  messages: 'text-orange-600 dark:text-orange-400 bg-orange-500/15 border-orange-500/20',
  chat: 'text-primary bg-primary/15 border-primary/20',
  responses: 'text-teal-600 dark:text-teal-400 bg-teal-500/15 border-teal-500/20',
  gemini: 'text-purple-600 dark:text-purple-400 bg-purple-500/15 border-purple-500/20',
}

function getProtocolColor(proto: string): string {
  if (proto.includes('->')) return 'text-cyan-600 dark:text-cyan-400 bg-cyan-500/15 border-cyan-500/20'
  return PROTOCOL_COLORS[proto] ?? 'text-muted-foreground bg-muted/30 border-border'
}

function getProtocolDisplayName(proto: string): string {
  const map: Record<string, string> = { messages: 'Claude', chat: 'OpenAI Chat', responses: 'Codex', gemini: 'Gemini' }
  if (proto.includes('->')) {
    const parts = proto.split('->')
    return `${map[parts[0]] ?? parts[0]} → ${map[parts[1]] ?? parts[1]}`
  }
  return map[proto] ?? proto
}

type ProtocolDisplayState = 'idle' | 'pending' | 'running' | 'success' | 'partial' | 'cancelled' | 'failed'

// ── 协议状态判定 ──

function getProtocolDisplayState(test: CapabilityProtocolJobResult): ProtocolDisplayState {
  if ((test.status as string) === 'idle') return 'idle'
  if (test.lifecycle === 'cancelled' || test.outcome === 'cancelled') return 'cancelled'
  if (test.lifecycle === 'active' || test.status === 'running') return 'running'
  if (test.lifecycle === 'pending' || test.status === 'queued') return 'pending'
  if (test.outcome === 'partial') return 'partial'
  if (test.success || test.outcome === 'success') return 'success'
  return 'failed'
}

function isProtocolBusy(test: CapabilityProtocolJobResult): boolean {
  const displayState = getProtocolDisplayState(test)
  return displayState === 'pending' || displayState === 'running'
}

function isProtocolFailed(test: CapabilityProtocolJobResult): boolean {
  return getProtocolDisplayState(test) === 'failed'
}

function getProtocolStatusLabel(test: CapabilityProtocolJobResult): string {
  switch (getProtocolDisplayState(test)) {
    case 'idle': return t('capability.notStarted')
    case 'pending': return t('capability.modelQueued')
    case 'running': return t('capability.protocolRunning')
    case 'success': return t('capability.success')
    case 'partial': return t('capability.partial')
    case 'cancelled': return t('capability.cancelled')
    default: return test.error || t('capability.failed')
  }
}

function shouldShowTestProtocolButton(test: CapabilityProtocolJobResult): boolean {
  return !isProtocolBusy(test)
}

function isCurrentTabProtocol(proto: string): boolean {
  const currentProtocol = props.channelType
  if (proto === currentProtocol) return true
  if (proto.includes('->')) {
    const [from] = proto.split('->')
    return from === currentProtocol
  }
  return false
}

function getSuccessfulProtocols(): string[] {
  return sortedTests.value.filter(t => t.success && !t.protocol.includes('->')).map(t => t.protocol)
}

// ── 表格指标 ──

function getSuccessCount(test: CapabilityProtocolJobResult): number {
  return (test.modelResults ?? []).filter(m => m.status === 'success').length
}

function getAttemptedModels(test: CapabilityProtocolJobResult): number {
  return (test.modelResults ?? []).filter(m => m.status !== 'idle' && m.status !== 'skipped').length
}

function formatSuccessRatio(test: CapabilityProtocolJobResult): string {
  const s = getSuccessCount(test)
  const a = getAttemptedModels(test)
  if (a === 0) return '—'
  return `${s}/${a}`
}

function getAverageLatency(test: CapabilityProtocolJobResult): string {
  const results = (test.modelResults ?? []).filter(m => m.status === 'success' && m.latency >= 0)
  if (!results.length) return '—'
  const avg = Math.round(results.reduce((sum, m) => sum + m.latency, 0) / results.length)
  return `${avg}ms`
}

function hasProtocolLatency(test: CapabilityProtocolJobResult): boolean {
  return (test.modelResults ?? []).some(m => m.latency >= 0)
}

// ── Actions ──

async function handleTestProtocol(protocol: string) {
  isStarting.value = true
  try {
    await startProtocolTest(props.channelType, props.channelId, protocol, undefined, rpmValue.value, useChannelModels.value)
  } finally {
    isStarting.value = false
  }
}

async function handleCancel() {
  await cancelActiveTests(props.channelType, props.channelId, props.channelType)
}

async function handleRetryModel(protocol: string, model: string) {
  await retryModelForProtocol(props.channelType, props.channelId, protocol, model)
}

async function handleCopyToTab(targetProtocol: string, serviceProtocol: string) {
  const key = `${targetProtocol}:${serviceProtocol}`
  copyingKey.value = key
  copyMessage.value = null
  try {
    const result = await copyToTab(props.channelType, props.channelId, targetProtocol, serviceProtocol)
    if (result.ok) {
      copyMessage.value = {
        type: 'success',
        text: t('toast.channelCopied', { protocol: getProtocolDisplayName(targetProtocol) }),
      }
      return
    }
    copyMessage.value = {
      type: 'error',
      text: t('toast.copyFailed', { message: result.message }),
    }
  } finally {
    copyingKey.value = ''
  }
}

function handleRpmBlur() {
  const parsedValue = Number.isFinite(rpmValue.value) ? Math.floor(rpmValue.value) : DEFAULT_CAPABILITY_TEST_RPM
  rpmValue.value = Math.min(60, Math.max(1, parsedValue || DEFAULT_CAPABILITY_TEST_RPM))
}

// ── 一键建映射（源模型名 → 实测真实模型） ──

interface MappableModel {
  model: string
  actualModel: string
}

function getMappableModels(test: CapabilityProtocolJobResult): MappableModel[] {
  return (test.modelResults ?? [])
    .filter(m => !!m.actualModel && m.actualModel !== m.model)
    .map(m => ({ model: m.model, actualModel: m.actualModel as string }))
}

const mappingDialogOpen = ref(false)
const mappingCandidates = ref<MappableModel[]>([])
const mappingSource = ref('')
const mappingTarget = ref('')
const creatingMapping = ref(false)

function applyMappingCandidate(candidate: MappableModel) {
  mappingSource.value = candidate.model
  mappingTarget.value = candidate.actualModel
}

function openMappingDialog(test: CapabilityProtocolJobResult) {
  const candidates = getMappableModels(test)
  if (candidates.length === 0) return
  mappingCandidates.value = candidates
  applyMappingCandidate(candidates[0])
  mappingDialogOpen.value = true
}

async function submitCreateMapping() {
  const source = mappingSource.value.trim()
  if (!source || creatingMapping.value) return
  creatingMapping.value = true
  try {
    await createModelMapping(props.channelType, props.channelId, source, mappingTarget.value)
    mappingDialogOpen.value = false
    copyMessage.value = {
      type: 'success',
      text: t('capability.mappingCreated', { source, target: mappingTarget.value }),
    }
  } catch (e) {
    copyMessage.value = {
      type: 'error',
      text: e instanceof Error ? e.message : String(e),
    }
  } finally {
    creatingMapping.value = false
  }
}

function getRunModeLabel(mode: string): string {
  const map: Record<string, string> = {
    fresh: '',
    reused_running: t('capability.runModeReusedRunning'),
    resumed_cancelled: t('capability.runModeResumedCancelled'),
    cache_hit: t('capability.runModeCacheHit'),
    reused_previous_results: t('capability.runModeReusedPreviousResults'),
  }
  return map[mode] ?? mode
}

function onKeyDown(e: KeyboardEvent) {
  if (!props.open || e.key !== 'Escape') return
  e.preventDefault()
  emit('close')
}

onBeforeUnmount(() => {
  window.removeEventListener('keydown', onKeyDown)
  closeDialog()
})
</script>

<template>
  <Teleport to="body">
    <Transition name="fade">
      <div v-if="open" class="fixed inset-0 z-50 flex items-center justify-center">
        <div class="absolute inset-0 bg-black/60 backdrop-blur-sm" @click="emit('close')" />

        <div class="relative z-10 w-[94vw] max-w-4xl max-h-[90vh] border border-border bg-card shadow-2xl flex flex-col">
          <!-- Header -->
          <div class="flex items-center justify-between border-b border-border px-4 py-3 shrink-0">
            <div class="flex items-center gap-2">
              <Play class="h-4 w-4 text-primary" />
              <h3 class="text-sm font-semibold">
                {{ t('capability.title', { channel: channelName }) }}
              </h3>
            </div>
            <div class="flex items-center gap-2">
              <Badge v-if="runMode !== 'fresh'" variant="secondary" class="text-[10px]">{{ getRunModeLabel(runMode) }}</Badge>
              <Badge v-if="displayOutcome === 'partial'" variant="outline" class="text-[10px] border-amber-500/30 text-amber-700 dark:text-amber-400">{{ t('capability.partial') }}</Badge>
              <Badge v-else-if="displayOutcome === 'cancelled'" variant="outline" class="text-[10px]">{{ t('capability.cancelled') }}</Badge>
              <Button variant="ghost" size="icon-sm" @click="emit('close')" class="relative group">
                <X class="h-4 w-4" />
                <span class="absolute -bottom-6 right-0 text-[10px] text-muted-foreground opacity-0 group-hover:opacity-100 transition-opacity pointer-events-none whitespace-nowrap">Esc</span>
              </Button>
            </div>
          </div>

          <!-- Body -->
          <ScrollArea class="flex-1 min-h-0">
            <div class="p-4 space-y-4">
              <!-- Error -->
              <div v-if="error" class="text-sm text-destructive bg-destructive/10 p-3">{{ error }}</div>
              <div
                v-if="copyMessage"
                :class="cn(
                  'text-sm p-3 border',
                  copyMessage.type === 'success'
                    ? 'text-emerald-700 dark:text-emerald-300 bg-emerald-500/10 border-emerald-500/20'
                    : 'text-destructive bg-destructive/10 border-destructive/20',
                )"
              >
                {{ copyMessage.text }}
              </div>

              <!-- Initializing -->
              <div v-if="state === 'initializing'" class="flex flex-col items-center py-8 gap-3">
                <Loader2 class="h-8 w-8 animate-spin text-primary" />
                <p class="text-sm text-muted-foreground">{{ t('capability.loadingTitle') }}</p>
              </div>

              <!-- 状态栏 -->
              <div v-if="state !== 'initializing' && state !== 'error'" class="flex items-center gap-2 flex-wrap border border-border bg-secondary/30 px-3 py-2">
                <Badge v-for="proto in compatibleProtocols" :key="proto" variant="outline" :class="['text-[10px]', getProtocolColor(proto)]">{{ getProtocolDisplayName(proto) }}</Badge>
                <Badge v-if="hasNoCompatibleProtocolsYet && (state === 'completed' || state === 'cancelled')" variant="outline" class="text-[10px] text-muted-foreground">{{ t('capability.noCompatibleProtocols') }}</Badge>
                <div v-else-if="hasNoCompatibleProtocolsYet && state !== 'idle'" class="flex items-center gap-1.5 text-[10px] text-muted-foreground">
                  <Loader2 v-if="state === 'pending' || state === 'running'" class="h-3 w-3 animate-spin text-primary" />
                  <span>{{ state === 'pending' ? t('capability.modelQueued') : t('capability.protocolRunning') }}</span>
                </div>

                <div class="flex items-center gap-1.5 text-[10px] text-muted-foreground ml-auto">
                  <Gauge class="h-3 w-3" />
                  <span>{{ t('capability.rpmLabel') }}</span>
                  <Input v-model.number="rpmValue" type="number" min="1" max="60" step="1" class="h-6 w-14 text-[11px] font-mono px-1.5" @blur="handleRpmBlur" />
                </div>

                <div class="flex items-center gap-1.5 text-[10px] text-muted-foreground" :title="t('capability.useChannelModelsHint')">
                  <Switch v-model="useChannelModels" />
                  <span class="cursor-pointer select-none" @click="useChannelModels = !useChannelModels">{{ t('capability.useChannelModels') }}</span>
                </div>

                <span v-if="progress?.totalModels && isActive" class="text-[10px] text-muted-foreground">
                  {{ progress?.completedModels || 0 }}/{{ progress?.totalModels || 0 }} {{ t('capability.models') }}
                </span>

                <span v-if="currentJob?.snapshotUpdatedAt" class="text-[10px] text-muted-foreground">
                  {{ t('capability.snapshotUpdated', { time: currentJob.snapshotUpdatedAt }) }}
                </span>

                <Button v-if="state === 'pending' || state === 'running'" variant="destructive" size="sm" :disabled="cancelling" @click="handleCancel">
                  <Square class="h-3 w-3 mr-1" />
                  {{ cancelling ? t('capability.cancelling') : t('capability.cancel') }}
                </Button>
              </div>

              <!-- 进度条 -->
              <div v-if="isActive && currentJob">
                <Progress :model-value="progressPercent" />
              </div>

              <!-- 无任务 -->
              <div v-if="state === 'idle' && !isActive && sortedTests.length === 0" class="flex flex-col items-center py-6 gap-3">
                <p v-if="protocolResults.length > 0" class="text-sm text-muted-foreground">{{ t('capability.lastResults') }}</p>
                <p v-else class="text-sm text-muted-foreground">{{ t('capability.noResults') }}</p>
              </div>

              <!-- 协议表格 -->
              <div v-if="sortedTests.length > 0" class="border border-border overflow-hidden">
                <table class="w-full text-xs">
                  <thead class="bg-secondary/40 border-b border-border">
                    <tr>
                      <th class="px-3 py-2 text-left font-semibold uppercase tracking-wider text-muted-foreground">{{ t('capability.table.protocol') }}</th>
                      <th class="px-3 py-2 text-left font-semibold uppercase tracking-wider text-muted-foreground">{{ t('capability.table.status') }}</th>
                      <th class="px-3 py-2 text-center font-semibold uppercase tracking-wider text-muted-foreground">{{ t('capability.table.successCount') }}</th>
                      <th class="px-3 py-2 text-right font-semibold uppercase tracking-wider text-muted-foreground">{{ t('capability.table.latency') }}</th>
                      <th class="px-3 py-2 text-center font-semibold uppercase tracking-wider text-muted-foreground">{{ t('capability.table.streaming') }}</th>
                      <th class="px-3 py-2 text-right font-semibold uppercase tracking-wider text-muted-foreground">{{ t('capability.table.actions') }}</th>
                    </tr>
                  </thead>
                  <tbody>
                    <template v-for="test in sortedTests" :key="test.protocol">
                      <tr class="border-b border-border hover:bg-secondary/20">
                        <td class="px-3 py-2">
                          <Badge variant="outline" :class="['text-[10px]', getProtocolColor(test.protocol)]">{{ getProtocolDisplayName(test.protocol) }}</Badge>
                        </td>
                        <td class="px-3 py-2">
                          <div v-if="!isProtocolFailed(test)" class="flex items-center gap-1.5">
                            <CheckCircle2 v-if="test.success" class="h-3.5 w-3.5 text-emerald-500" />
                            <XCircle v-else-if="test.status === 'failed'" class="h-3.5 w-3.5 text-rose-500" />
                            <Loader2 v-else-if="isProtocolBusy(test)" class="h-3.5 w-3.5 animate-spin text-primary" />
                            <Clock v-else class="h-3.5 w-3.5 text-muted-foreground" />
                            <span class="text-xs">{{ getProtocolStatusLabel(test) }}</span>
                          </div>
                          <div v-else class="flex items-center gap-1.5 text-rose-600 dark:text-rose-400" :title="test.error">
                            <XCircle class="h-3.5 w-3.5" />
                            <span class="text-xs truncate max-w-[180px]">{{ test.error || test.status }}</span>
                          </div>
                        </td>
                        <td class="px-3 py-2 text-center">
                          <span :class="getSuccessCount(test) === getAttemptedModels(test) ? 'text-emerald-600 dark:text-emerald-400' : 'text-amber-600 dark:text-amber-400'">{{ formatSuccessRatio(test) }}</span>
                        </td>
                        <td class="px-3 py-2 text-right font-mono">
                          <span v-if="hasProtocolLatency(test)">{{ getAverageLatency(test) }}</span>
                          <span v-else class="text-muted-foreground">—</span>
                        </td>
                        <td class="px-3 py-2 text-center">
                          <div v-if="test.success && test.streamingSupported" class="flex items-center justify-center gap-1">
                            <CheckCircle2 class="h-3.5 w-3.5 text-emerald-500" /><span class="text-emerald-600 dark:text-emerald-400">{{ t('capability.supported') }}</span>
                          </div>
                          <div v-else-if="test.success" class="flex items-center justify-center gap-1">
                            <XCircle class="h-3.5 w-3.5 text-amber-500" /><span class="text-amber-600 dark:text-amber-400">{{ t('capability.unsupported') }}</span>
                          </div>
                          <span v-else class="text-muted-foreground">—</span>
                        </td>
                        <td class="px-3 py-2 text-right">
                          <div class="flex items-center justify-end gap-1 flex-wrap">
                            <Button v-if="shouldShowTestProtocolButton(test)" variant="outline" size="sm" class="h-5 text-[10px]" :disabled="isStarting || isProtocolBusy(test)" @click="handleTestProtocol(test.protocol)">
                              <Play class="h-3 w-3" />{{ t('capability.startTest') }}
                            </Button>
                            <Button
                              v-if="test.success && !isCurrentTabProtocol(test.protocol)"
                              variant="outline"
                              size="sm"
                              class="h-5 text-[10px] cursor-pointer hover:ring-1 hover:ring-current/30"
                              :disabled="copyingKey !== ''"
                              @click="handleCopyToTab(test.protocol, test.protocol)"
                            >
                              <Loader2 v-if="copyingKey === `${test.protocol}:${test.protocol}`" class="h-3 w-3 animate-spin" />
                              <ArrowRight v-else class="h-3 w-3" />{{ t('capability.copyToTab') }}
                            </Button>
                            <Badge v-else-if="isCurrentTabProtocol(test.protocol)" variant="secondary" class="text-[10px]">{{ t('capability.currentTab') }}</Badge>
                            <template v-else-if="!test.success && !isCurrentTabProtocol(test.protocol)">
                              <Button
                                v-for="successProto in getSuccessfulProtocols()"
                                :key="successProto"
                                variant="outline"
                                size="sm"
                                :class="['h-5 text-[10px] cursor-pointer hover:ring-1 hover:ring-current/30', getProtocolColor(successProto)]"
                                :disabled="copyingKey !== ''"
                                @click="handleCopyToTab(test.protocol, successProto)"
                              >
                                <Loader2 v-if="copyingKey === `${test.protocol}:${successProto}`" class="h-3 w-3 animate-spin" />
                                {{ t('capability.convert', { protocol: getProtocolDisplayName(successProto) }) }}
                              </Button>
                            </template>
                          </div>
                        </td>
                      </tr>
                      <tr class="border-b border-border/50 bg-background/30">
                        <td colspan="6" class="px-3 py-2">
                          <div class="flex items-center gap-2 flex-wrap">
                            <CapabilityModelResultBadge :test="test" :pending-text="t('capability.modelQueued')" :retry-enabled="!isProtocolBusy(test)" @retry-model="handleRetryModel" />
                            <Button
                              v-if="getMappableModels(test).length > 0 && !isProtocolBusy(test)"
                              variant="outline"
                              size="sm"
                              class="h-5 text-[10px] shrink-0"
                              :title="t('capability.createMapping')"
                              @click="openMappingDialog(test)"
                            >
                              <Link2 class="h-3 w-3" />{{ t('capability.createMapping') }}
                            </Button>
                          </div>
                        </td>
                      </tr>
                    </template>
                  </tbody>
                </table>
              </div>

              <!-- 总耗时 -->
              <div v-if="currentJob?.totalDuration || snapshot?.totalDuration" class="text-xs text-muted-foreground text-right">
                {{ t('capability.duration') }}: {{ ((currentJob?.totalDuration || snapshot?.totalDuration || 0) / 1000).toFixed(1) }}s
              </div>
            </div>
          </ScrollArea>
        </div>
      </div>
    </Transition>
  </Teleport>

  <!-- 创建模型映射对话框：把源模型名显式重定向到实测真实模型 -->
  <Dialog v-model:open="mappingDialogOpen">
    <DialogContent class="sm:max-w-[460px]">
      <DialogHeader>
        <DialogTitle>{{ t('capability.createMappingTitle') }}</DialogTitle>
      </DialogHeader>
      <div class="space-y-3">
        <p class="text-sm text-muted-foreground">{{ t('capability.createMappingDesc', { target: mappingTarget }) }}</p>
        <div v-if="mappingCandidates.length > 1" class="flex flex-wrap gap-1.5">
          <button
            v-for="candidate in mappingCandidates"
            :key="candidate.model"
            type="button"
            :class="mappingSource.trim() === candidate.model ? 'border-primary/40 bg-primary/10 text-primary' : 'border-border text-muted-foreground hover:text-foreground'"
            class="cursor-pointer rounded border px-2 py-1 font-mono text-[11px] transition-colors"
            @click="applyMappingCandidate(candidate)"
          >
            {{ candidate.model }} → {{ candidate.actualModel }}
          </button>
        </div>
        <div class="space-y-1.5">
          <label class="text-xs font-medium">{{ t('capability.mappingSourceLabel') }}</label>
          <Input v-model="mappingSource" :placeholder="t('capability.mappingSourcePlaceholder')" />
        </div>
        <p class="text-xs text-muted-foreground">{{ t('capability.mappingSupportedModelsHint') }}</p>
      </div>
      <DialogFooter>
        <Button variant="outline" size="sm" @click="mappingDialogOpen = false">{{ t('common.cancel') }}</Button>
        <Button size="sm" :disabled="!mappingSource.trim() || creatingMapping" @click="submitCreateMapping">
          <Loader2 v-if="creatingMapping" class="h-3.5 w-3.5 animate-spin" />
          {{ t('capability.createMappingConfirm') }}
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
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
