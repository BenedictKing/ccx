<script setup lang="ts">
import { ref, watch } from 'vue'
import {
  Check,
  CircleAlert,
  Copy,
  FileQuestion,
  Loader2,
  Minus,
  RefreshCw,
} from 'lucide-vue-next'
import { Alert } from '@/components/ui/alert'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  Dialog,
  DialogContent,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { ScrollArea } from '@/components/ui/scroll-area'
import { Separator } from '@/components/ui/separator'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { useAdminApi, AdminApiError } from '@/composables/useAdminApi'
import { useLanguage } from '@/composables/useLanguage'
import { autopilotTracePath } from '@/services/admin-api'
import type {
  RoutingCandidate,
  TraceDetailV2,
} from '@/services/admin-api'

const props = defineProps<{
  open: boolean
  traceUid: string | null
}>()

const emit = defineEmits<{
  'update:open': [value: boolean]
}>()

const { t } = useLanguage()
const api = useAdminApi()

const loading = ref(false)
const notFound = ref(false)
const fetchError = ref(false)
const detail = ref<TraceDetailV2 | null>(null)
const copied = ref(false)
let copyResetTimer: ReturnType<typeof setTimeout> | null = null

// 复制整个决策详情为格式化 JSON，便于直接粘贴给 AI 分析定位问题
async function copyDetail() {
  if (!detail.value) return
  try {
    await navigator.clipboard.writeText(JSON.stringify(detail.value, null, 2))
    copied.value = true
    if (copyResetTimer) clearTimeout(copyResetTimer)
    copyResetTimer = setTimeout(() => {
      copied.value = false
      copyResetTimer = null
    }, 1600)
  } catch (e) {
    console.error('Failed to copy trace detail:', e)
  }
}

watch(
  () => props.open,
  (open) => {
    if (open && props.traceUid) {
      void fetchDetail()
    }
  },
)

watch(
  () => props.traceUid,
  (uid) => {
    if (props.open && uid) {
      void fetchDetail()
    }
  },
)

async function fetchDetail() {
  if (!props.traceUid) return
  loading.value = true
  notFound.value = false
  fetchError.value = false
  detail.value = null
  try {
    const resp = await api.get<{ trace: TraceDetailV2 }>(autopilotTracePath(props.traceUid))
    detail.value = resp.trace
  } catch (err: unknown) {
    if (err instanceof AdminApiError && err.status === 404) {
      notFound.value = true
    } else {
      fetchError.value = true
    }
  } finally {
    loading.value = false
  }
}

function formatTime(iso?: string): string {
  if (!iso) return '-'
  try {
    return new Date(iso).toLocaleString()
  } catch {
    return iso
  }
}

function shortReleaseId(id?: string): string {
  if (!id) return '-'
  return id.length > 12 ? id.slice(0, 12) + '...' : id
}

// 紧凑展示候选渠道：v3 有 keyIdentity 时渠道名单独成列，旧 trace 拼 keyMask 兜底
function formatChannelDisplay(cand: RoutingCandidate): string {
  const name = cand.channelName || cand.channelUid
  if (cand.keyMask && !cand.keyIdentity) {
    return `${name} (${cand.keyMask})`
  }
  return name
}

// 候选行承接模型名：优先 mappedModel/actualModel，旧 trace 回退解析 candidateKey
function displayCandidateModel(cand: RoutingCandidate): string {
  if (cand.mappedModel) return cand.mappedModel
  if (cand.actualModel) return cand.actualModel
  const key = cand.candidateKey
  if (key) {
    const parts = key.split('|')
    if (parts.length >= 5) return parts[3] || '-'
    const idx = key.indexOf('|')
    if (idx >= 0 && idx < key.length - 1) return key.slice(idx + 1)
  }
  return '-'
}

function modeClass(mode?: string): string {
  const map: Record<string, string> = {
    off: 'border-muted-foreground/40 text-muted-foreground',
    shadow: 'border-cyan-500/40 text-cyan-700 dark:text-cyan-300',
    assist: 'border-amber-500/40 text-amber-700 dark:text-amber-300',
    auto: 'border-emerald-500/40 text-emerald-700 dark:text-emerald-300',
    active: 'border-primary/40 text-primary',
    dry_run: 'border-cyan-500/40 text-cyan-700 dark:text-cyan-300',
  }
  return map[mode ?? ''] ?? 'border-muted-foreground/40 text-muted-foreground'
}

function comparisonClass(status: string): string {
  if (status === 'matched') return 'border-emerald-500/40 text-emerald-700 dark:text-emerald-300'
  if (status === 'mismatched') return 'border-rose-500/40 text-rose-700 dark:text-rose-300'
  return 'border-muted-foreground/40 text-muted-foreground'
}

function outcomeClass(outcome?: string): string {
  if (outcome === 'success') return 'border-emerald-500/40 text-emerald-700 dark:text-emerald-300'
  if (outcome === 'cancelled') return 'border-muted-foreground/40 text-muted-foreground'
  if (outcome === 'attempt_failed') return 'border-amber-500/40 text-amber-700 dark:text-amber-300'
  return 'border-rose-500/40 text-rose-700 dark:text-rose-300'
}

function attemptResultClass(result: string): string {
  return outcomeClass(result)
}
</script>

<template>
  <Dialog :open="open" @update:open="emit('update:open', $event)">
    <DialogContent class="flex max-h-[85vh] flex-col gap-0 overflow-hidden p-0 sm:max-w-3xl">
      <DialogHeader class="flex flex-row items-center justify-between border-b border-border px-4 py-3">
        <DialogTitle class="flex items-center gap-2 text-sm font-bold">
          {{ t('autopilot.traceDetail.title') }}
        </DialogTitle>
        <Button
          v-if="detail"
          variant="ghost"
          size="icon-sm"
          :title="copied ? t('autopilot.traceDetail.copied') : t('autopilot.traceDetail.copy')"
          :aria-label="t('autopilot.traceDetail.copy')"
          @click="copyDetail"
        >
          <Check v-if="copied" class="size-4 text-emerald-500" />
          <Copy v-else class="size-4" />
        </Button>
      </DialogHeader>

      <ScrollArea type="auto" class="max-h-[calc(85vh-56px)] min-h-0 flex-1">
        <div class="p-4">
          <!-- 加载中 -->
          <div v-if="loading" class="flex items-center justify-center py-10 text-muted-foreground">
            <Loader2 class="size-6 animate-spin" />
          </div>

          <!-- 404：记录不存在/已过期/未采样 -->
          <div v-else-if="notFound" class="flex flex-col items-center gap-3 py-10 text-center">
            <FileQuestion class="size-10 text-muted-foreground/60" />
            <div class="text-sm text-muted-foreground">{{ t('autopilot.traceDetail.notFound') }}</div>
            <code class="text-xs text-muted-foreground">{{ traceUid }}</code>
            <Button variant="outline" size="sm" @click="fetchDetail">
              <RefreshCw class="size-3.5" />
              {{ t('autopilot.traceDetail.retry') }}
            </Button>
          </div>

          <!-- 网络错误可重试 -->
          <div v-else-if="fetchError" class="flex flex-col items-center gap-3 py-10 text-center">
            <CircleAlert class="size-10 text-destructive/70" />
            <div class="text-sm text-muted-foreground">{{ t('autopilot.traceDetail.fetchError') }}</div>
            <Button variant="outline" size="sm" @click="fetchDetail">
              <RefreshCw class="size-3.5" />
              {{ t('autopilot.traceDetail.retry') }}
            </Button>
          </div>

          <!-- 详情内容 -->
          <template v-else-if="detail">
            <!-- 历史 schema 提示 -->
            <Alert v-if="detail.historicalSchema" class="mb-3">
              <div class="text-xs text-muted-foreground">
                {{ t('autopilot.traceDetail.historicalSchema') }}
              </div>
            </Alert>

            <!-- 身份与发布快照 -->
            <div class="mb-4">
              <div class="mb-2 text-xs font-bold text-muted-foreground">{{ t('autopilot.traceDetail.identity') }}</div>
              <div class="grid grid-cols-2 gap-x-4 gap-y-2">
                <div>
                  <div class="text-xs text-muted-foreground">{{ t('autopilot.traceDetail.traceUid') }}</div>
                  <code class="text-xs break-all">{{ detail.traceUid }}</code>
                </div>
                <div>
                  <div class="text-xs text-muted-foreground">{{ t('autopilot.traceDetail.createdAt') }}</div>
                  <div class="text-xs">{{ formatTime(detail.createdAt) }}</div>
                </div>
                <div>
                  <div class="text-xs text-muted-foreground">{{ t('autopilot.traceDetail.releaseId') }}</div>
                  <code class="text-xs">{{ shortReleaseId(detail.releaseId) }}</code>
                </div>
                <div v-if="detail.policyFingerprint">
                  <div class="text-xs text-muted-foreground">Policy</div>
                  <code class="text-xs">{{ shortReleaseId(detail.policyFingerprint) }}</code>
                </div>
                <div>
                  <div class="text-xs text-muted-foreground">{{ t('autopilot.traceDetail.cohort') }}</div>
                  <div class="text-xs">{{ detail.cohort || '-' }}</div>
                </div>
                <div>
                  <div class="text-xs text-muted-foreground">{{ t('autopilot.traceTable.col.comparison') }}</div>
                  <Badge variant="outline" :class="comparisonClass(detail.comparisonStatus)">
                    {{ t(`autopilot.traceTable.comparison.${detail.comparisonStatus}`) }}
                  </Badge>
                </div>
                <div v-if="detail.targetMode">
                  <div class="text-xs text-muted-foreground">{{ t('autopilot.traceDetail.targetMode') }}</div>
                  <Badge variant="outline" :class="modeClass(detail.targetMode)">{{ detail.targetMode }}</Badge>
                </div>
                <div v-if="detail.effectiveMode">
                  <div class="text-xs text-muted-foreground">{{ t('autopilot.traceDetail.effectiveMode') }}</div>
                  <Badge variant="outline" :class="modeClass(detail.effectiveMode)">{{ detail.effectiveMode }}</Badge>
                </div>
                <div v-if="detail.bypassReason">
                  <div class="text-xs text-muted-foreground">{{ t('autopilot.traceDetail.bypassReason') }}</div>
                  <div class="text-xs">{{ detail.bypassReason }}</div>
                </div>
                <div v-if="detail.requestCorrelationId">
                  <div class="text-xs text-muted-foreground">{{ t('autopilot.traceDetail.correlationId') }}</div>
                  <code class="text-xs break-all">{{ detail.requestCorrelationId }}</code>
                </div>
                <div>
                  <div class="text-xs text-muted-foreground">Schema</div>
                  <div class="text-xs">v{{ detail.schemaVersion }}</div>
                </div>
              </div>
            </div>

            <Separator class="mb-4" />

            <!-- 请求画像 -->
            <div class="mb-4">
              <div class="mb-2 text-xs font-bold text-muted-foreground">{{ t('autopilot.traceDetail.requestProfile') }}</div>
              <div class="grid grid-cols-2 gap-x-4 gap-y-2 md:grid-cols-3">
                <div>
                  <div class="text-xs text-muted-foreground">{{ t('autopilot.traceTable.col.kind') }}</div>
                  <Badge variant="outline">{{ detail.requestKind }}</Badge>
                </div>
                <div>
                  <div class="text-xs text-muted-foreground">{{ t('autopilot.traceTable.col.taskClass') }}</div>
                  <Badge variant="secondary">{{ detail.taskClass || '-' }}</Badge>
                </div>
                <div v-if="detail.taskDomain">
                  <div class="text-xs text-muted-foreground">Domain</div>
                  <div class="text-xs">{{ detail.taskDomain }}</div>
                </div>
                <div>
                  <div class="text-xs text-muted-foreground">{{ t('autopilot.traceTable.col.model') }}</div>
                  <div class="truncate text-xs">{{ detail.requestedModel || '-' }}</div>
                </div>
                <div>
                  <div class="text-xs text-muted-foreground">Actual Model</div>
                  <div class="truncate text-xs">{{ detail.actualModel || '-' }}</div>
                </div>
                <div>
                  <div class="text-xs text-muted-foreground">Effort</div>
                  <div class="text-xs">{{ detail.actualEffort || '-' }}</div>
                </div>
                <div v-if="detail.agentRole">
                  <div class="text-xs text-muted-foreground">Agent Role</div>
                  <div class="text-xs">{{ detail.agentRole }}</div>
                </div>
                <div v-if="detail.manualIntentUid">
                  <div class="text-xs text-muted-foreground">Manual Intent</div>
                  <code class="text-xs">{{ detail.manualIntentUid }}</code>
                </div>
                <div v-if="detail.advisorDecisionUid">
                  <div class="text-xs text-muted-foreground">Advisor</div>
                  <code class="text-xs">{{ detail.advisorDecisionUid }}</code>
                </div>
              </div>
            </div>

            <Separator class="mb-4" />

            <!-- 候选与决策 -->
            <div class="mb-4">
              <div class="mb-2 text-xs font-bold text-muted-foreground">
                {{ t('autopilot.traceDetail.candidates') }}
                <span class="font-normal">({{ detail.candidatesAfter }}/{{ detail.candidatesBefore }})</span>
              </div>
              <div
                v-if="detail.candidates && detail.candidates.length > 0"
                class="overflow-hidden rounded-lg border border-border/50"
              >
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead>Channel</TableHead>
                      <TableHead>Model</TableHead>
                      <TableHead>Key</TableHead>
                      <TableHead>Effort</TableHead>
                      <TableHead>Quota</TableHead>
                      <TableHead class="text-right">Score</TableHead>
                      <TableHead class="w-[60px]">Sel</TableHead>
                      <TableHead>Filter Reasons</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    <TableRow v-for="(cand, ci) in detail.candidates" :key="ci" :class="cand.selected ? 'bg-primary/5' : 'text-muted-foreground'">
                      <TableCell class="text-xs">{{ formatChannelDisplay(cand) }}</TableCell>
                      <TableCell class="text-xs">{{ displayCandidateModel(cand) }}</TableCell>
                      <TableCell class="text-xs">{{ cand.keyIdentity || cand.keyMask || '-' }}</TableCell>
                      <TableCell class="text-xs">{{ cand.effort || '-' }}</TableCell>
                      <TableCell class="text-xs">{{ cand.quotaGroup || '-' }}</TableCell>
                      <TableCell class="text-right font-mono text-xs">{{ cand.totalScore.toFixed(3) }}</TableCell>
                      <TableCell>
                        <Check v-if="cand.selected" class="size-3.5 text-emerald-500" />
                        <Minus v-else class="size-3 text-muted-foreground/50" />
                      </TableCell>
                      <TableCell class="text-xs">{{ cand.filterReasons?.join('; ') || '-' }}</TableCell>
                    </TableRow>
                  </TableBody>
                </Table>
              </div>
              <div v-else class="text-xs text-muted-foreground">{{ t('autopilot.traceDetail.noCandidates') }}</div>

              <!-- 全局过滤原因 -->
              <div v-if="detail.globalFilterReasons && Object.keys(detail.globalFilterReasons).length > 0" class="mt-2">
                <div class="text-xs font-bold">{{ t('autopilot.traceDetail.globalFilterReasons') }}</div>
                <div v-for="(reasons, stage) in detail.globalFilterReasons" :key="stage" class="text-xs">
                  <span class="font-medium">{{ stage }}:</span>
                  <span class="text-muted-foreground"> {{ reasons.join(', ') }}</span>
                </div>
              </div>

              <!-- 排序原因 -->
              <div v-if="detail.sortReasons && detail.sortReasons.length > 0" class="mt-2">
                <div class="text-xs font-bold">{{ t('autopilot.traceTable.sortReasons') }}</div>
                <ul class="ml-4 list-disc text-xs text-muted-foreground">
                  <li v-for="(reason, ri) in detail.sortReasons" :key="ri">{{ reason }}</li>
                </ul>
              </div>
            </div>

            <!-- Scheduler 裁决 -->
            <div v-if="detail.schedulerDecision" class="mb-4">
              <div class="mb-2 text-xs font-bold text-muted-foreground">{{ t('autopilot.traceDetail.schedulerDecision') }}</div>
              <div v-if="detail.schedulerDecision.stages?.length" class="mb-2 flex flex-wrap items-center gap-1">
                <Badge v-for="stage in detail.schedulerDecision.stages" :key="stage.name" variant="secondary">
                  {{ stage.name }}: {{ stage.count }}
                </Badge>
              </div>
              <div v-if="detail.schedulerDecision.selectedUid" class="text-xs">
                <span class="font-medium">{{ t('autopilot.traceDetail.selected') }}:</span>
                <code class="ml-1">{{ detail.schedulerDecision.selectedUid }}</code>
                <span class="text-muted-foreground">({{ detail.schedulerDecision.selectionCode || detail.schedulerDecision.selectedName || '-' }})</span>
              </div>
              <div v-if="detail.schedulerDecision.skipReasons?.length" class="mt-1 text-xs">
                <span class="font-medium">{{ t('autopilot.traceDetail.skipReasons') }}:</span>
                <span class="text-muted-foreground"> {{ detail.schedulerDecision.skipReasons.join(', ') }}</span>
              </div>
              <template v-if="detail.schedulerDecision.skippedCandidates && detail.schedulerDecision.skippedCandidates.length > 0">
                <div class="mt-2 text-xs font-bold text-muted-foreground">{{ t('autopilot.traceDetail.skippedCandidates') }}</div>
                <div class="mt-1 overflow-hidden rounded-lg border border-border/50">
                  <Table>
                    <TableHeader>
                      <TableRow>
                        <TableHead>Stage</TableHead>
                        <TableHead>Channel</TableHead>
                        <TableHead>Reason</TableHead>
                        <TableHead>Details</TableHead>
                      </TableRow>
                    </TableHeader>
                    <TableBody>
                      <TableRow v-for="(cand, ci) in detail.schedulerDecision.skippedCandidates" :key="ci" class="text-xs">
                        <TableCell><Badge variant="outline">{{ cand.stage }}</Badge></TableCell>
                        <TableCell>{{ cand.channelName || cand.channelIndex }}</TableCell>
                        <TableCell>{{ cand.reason }}</TableCell>
                        <TableCell class="text-muted-foreground">{{ cand.details || '-' }}</TableCell>
                      </TableRow>
                    </TableBody>
                  </Table>
                </div>
              </template>
            </div>

            <!-- endpoint 尝试 -->
            <div v-if="detail.endpointAttempts && detail.endpointAttempts.length > 0" class="mb-4">
              <div class="mb-2 text-xs font-bold text-muted-foreground">
                {{ t('autopilot.traceDetail.attempts') }}
                <span v-if="detail.attemptsTruncated" class="font-normal text-amber-600 dark:text-amber-300">
                  ({{ t('autopilot.traceDetail.truncated') }}: {{ detail.attemptsTotal }})
                </span>
              </div>
              <div class="overflow-hidden rounded-lg border border-border/50">
                <Table>
                  <TableHeader>
                    <TableRow>
                      <TableHead class="w-[40px]">#</TableHead>
                      <TableHead>Endpoint</TableHead>
                      <TableHead>Actual Model</TableHead>
                      <TableHead>Result</TableHead>
                      <TableHead>Status</TableHead>
                      <TableHead class="text-right">Duration</TableHead>
                    </TableRow>
                  </TableHeader>
                  <TableBody>
                    <TableRow v-for="(att, ai) in detail.endpointAttempts" :key="ai" class="text-xs">
                      <TableCell>{{ att.attemptSeq }}</TableCell>
                      <TableCell>
                        <span :title="att.channelUid">{{ att.endpointLabel || att.channelUid }}</span>
                      </TableCell>
                      <TableCell>{{ att.actualModel || '-' }}</TableCell>
                      <TableCell>
                        <Badge variant="outline" :class="attemptResultClass(att.result)">{{ att.result }}</Badge>
                      </TableCell>
                      <TableCell>{{ att.statusCode || '-' }}</TableCell>
                      <TableCell class="text-right font-mono">{{ att.durationMs ? att.durationMs + 'ms' : '-' }}</TableCell>
                    </TableRow>
                  </TableBody>
                </Table>
              </div>
              <div v-if="detail.attemptsByResult && Object.keys(detail.attemptsByResult).length > 0" class="mt-1 text-xs">
                <span class="font-medium">{{ t('autopilot.traceDetail.attemptsByResult') }}:</span>
                <span v-for="(cnt, res) in detail.attemptsByResult" :key="res" class="mr-2 text-muted-foreground">
                  {{ res }}: {{ cnt }}
                </span>
              </div>
            </div>

            <Separator class="mb-4" />

            <!-- 终态 -->
            <div>
              <div class="mb-2 text-xs font-bold text-muted-foreground">{{ t('autopilot.traceDetail.outcome') }}</div>
              <div class="grid grid-cols-2 gap-x-4 gap-y-2 md:grid-cols-3">
                <div>
                  <div class="text-xs text-muted-foreground">{{ t('autopilot.traceTable.col.outcome') }}</div>
                  <Badge v-if="detail.outcome" variant="outline" :class="outcomeClass(detail.outcome)">
                    {{ detail.outcome }}
                  </Badge>
                  <span v-else class="text-xs">-</span>
                </div>
                <div>
                  <div class="text-xs text-muted-foreground">{{ t('autopilot.traceTable.col.match') }}</div>
                  <Badge variant="outline" :class="comparisonClass(detail.comparisonStatus)">
                    {{ t(`autopilot.traceTable.comparison.${detail.comparisonStatus}`) }}
                  </Badge>
                </div>
                <div>
                  <div class="text-xs text-muted-foreground">{{ t('autopilot.traceDetail.statusCode') }}</div>
                  <div class="text-xs">{{ detail.statusCode || '-' }}</div>
                </div>
                <div>
                  <div class="text-xs text-muted-foreground">{{ t('autopilot.traceDetail.duration') }}</div>
                  <div class="text-xs">{{ detail.requestDurationMs ? detail.requestDurationMs + 'ms' : '-' }}</div>
                </div>
                <div>
                  <div class="text-xs text-muted-foreground">{{ t('autopilot.traceDetail.firstByte') }}</div>
                  <div class="text-xs">{{ detail.firstByteLatencyMs ? detail.firstByteLatencyMs + 'ms' : '-' }}</div>
                </div>
                <div>
                  <div class="text-xs text-muted-foreground">{{ t('autopilot.traceDetail.fallbackUsed') }}</div>
                  <Check v-if="detail.fallbackUsed" class="size-3.5 text-amber-500" />
                  <span v-else class="text-xs">-</span>
                </div>
                <div v-if="detail.channelFallback">
                  <div class="text-xs text-muted-foreground">Channel Fallback</div>
                  <Check class="size-3.5 text-amber-500" />
                </div>
              </div>
            </div>
          </template>
        </div>
      </ScrollArea>
    </DialogContent>
  </Dialog>
</template>
