<template>
  <v-dialog
    :model-value="modelValue"
    max-width="900"
    @update:model-value="$emit('update:modelValue', $event)"
  >
    <v-card>
      <v-card-title class="d-flex align-center justify-space-between">
        <span class="dialog-title text-subtitle-1 font-weight-bold">
          <v-icon size="20" class="mr-2" color="info">mdi-chart-timeline-variant</v-icon>
          {{ t('autopilot.traceDetail.title') }}
        </span>
        <div class="d-flex align-center">
          <v-tooltip
            v-if="detail"
            :text="copied ? t('autopilot.traceDetail.copied') : t('autopilot.traceDetail.copy')"
            location="bottom"
            content-class="ccx-tooltip"
          >
            <template #activator="{ props: tooltipProps }">
              <v-btn
                icon
                size="small"
                variant="text"
                v-bind="tooltipProps"
                :aria-label="t('autopilot.traceDetail.copy')"
                @click="copyDetail"
              >
                <v-icon size="18">{{ copied ? 'mdi-check' : 'mdi-content-copy' }}</v-icon>
              </v-btn>
            </template>
          </v-tooltip>
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

      <v-card-text class="pa-0 trace-detail-scroll">
        <!-- 加载中 -->
        <div v-if="loading" class="d-flex justify-center align-center pa-8">
          <v-progress-circular indeterminate color="primary" />
        </div>

        <!-- 404：记录不存在/已过期/未采样 -->
        <div v-else-if="notFound" class="text-center pa-8">
          <v-icon size="48" color="grey-lighten-1" class="mb-3">mdi-file-question-outline</v-icon>
          <div class="text-body-1 text-medium-emphasis mb-2">
            {{ t('autopilot.traceDetail.notFound') }}
          </div>
          <div class="text-caption text-medium-emphasis mb-4">
            <code>{{ traceUid }}</code>
          </div>
          <v-btn variant="tonal" size="small" prepend-icon="mdi-refresh" @click="fetchDetail">
            {{ t('autopilot.traceDetail.retry') }}
          </v-btn>
        </div>

        <!-- 网络错误可重试 -->
        <div v-else-if="fetchError" class="text-center pa-8">
          <v-icon size="48" color="error" class="mb-3">mdi-alert-circle-outline</v-icon>
          <div class="text-body-1 text-medium-emphasis mb-4">
            {{ t('autopilot.traceDetail.fetchError') }}
          </div>
          <v-btn variant="tonal" size="small" prepend-icon="mdi-refresh" @click="fetchDetail">
            {{ t('autopilot.traceDetail.retry') }}
          </v-btn>
        </div>

        <!-- 详情内容 -->
        <div v-else-if="detail" class="pa-4">
          <!-- 历史 schema 提示 -->
          <v-alert v-if="detail.historicalSchema" type="info" variant="tonal" density="compact" class="mb-3">
            {{ t('autopilot.traceDetail.historicalSchema') }}
          </v-alert>

          <!-- 身份与策略快照 -->
          <div class="mb-4">
            <div class="text-caption font-weight-bold mb-2 text-medium-emphasis">
              {{ t('autopilot.traceDetail.identity') }}
            </div>
            <v-row dense>
              <v-col cols="6">
                <div class="text-caption text-medium-emphasis">{{ t('autopilot.traceDetail.traceUid') }}</div>
                <div class="text-body-2"><code>{{ detail.traceUid }}</code></div>
              </v-col>
              <v-col cols="6">
                <div class="text-caption text-medium-emphasis">{{ t('autopilot.traceDetail.createdAt') }}</div>
                <div class="text-body-2">{{ formatTime(detail.createdAt) }}</div>
              </v-col>
              <v-col cols="6">
                <div class="text-caption text-medium-emphasis">{{ t('autopilot.traceDetail.releaseId') }}</div>
                <div class="text-body-2"><code>{{ shortReleaseId(detail.releaseId) }}</code></div>
              </v-col>
              <v-col cols="6">
                <div class="text-caption text-medium-emphasis">{{ t('autopilot.traceDetail.cohort') }}</div>
                <div class="text-body-2">{{ detail.cohort || '-' }}</div>
              </v-col>
              <v-col cols="6">
                <div class="text-caption text-medium-emphasis">{{ t('autopilot.traceDetail.targetMode') }}</div>
                <v-chip size="x-small" variant="tonal" :color="modeColor(detail.targetMode)">
                  {{ t(`autopilot.mode.${detail.targetMode}`) || detail.targetMode }}
                </v-chip>
              </v-col>
              <v-col cols="6">
                <div class="text-caption text-medium-emphasis">{{ t('autopilot.traceDetail.effectiveMode') }}</div>
                <v-chip size="x-small" variant="tonal" :color="modeColor(detail.effectiveMode)">
                  {{ t(`autopilot.mode.${detail.effectiveMode}`) || detail.effectiveMode }}
                </v-chip>
              </v-col>
              <v-col v-if="detail.bypassReason" cols="6">
                <div class="text-caption text-medium-emphasis">{{ t('autopilot.traceDetail.bypassReason') }}</div>
                <div class="text-body-2">{{ detail.bypassReason }}</div>
              </v-col>
              <v-col v-if="detail.requestCorrelationId" cols="6">
                <div class="text-caption text-medium-emphasis">{{ t('autopilot.traceDetail.correlationId') }}</div>
                <div class="text-body-2"><code>{{ detail.requestCorrelationId }}</code></div>
              </v-col>
            </v-row>
          </div>

          <v-divider class="mb-4" />

          <!-- 请求画像 -->
          <div class="mb-4">
            <div class="text-caption font-weight-bold mb-2 text-medium-emphasis">
              {{ t('autopilot.traceDetail.requestProfile') }}
            </div>
            <v-row dense>
              <v-col cols="4">
                <div class="text-caption text-medium-emphasis">{{ t('autopilot.traceTable.col.kind') }}</div>
                <v-chip size="x-small" variant="outlined" color="primary">{{ detail.requestKind }}</v-chip>
              </v-col>
              <v-col cols="4">
                <div class="text-caption text-medium-emphasis">{{ t('autopilot.traceTable.col.taskClass') }}</div>
                <v-chip size="x-small" variant="tonal" color="secondary">{{ detail.taskClass || '-' }}</v-chip>
              </v-col>
              <v-col cols="4">
                <div class="text-caption text-medium-emphasis">{{ t('autopilot.traceTable.col.model') }}</div>
                <div class="text-body-2 text-truncate">{{ detail.requestedModel || '-' }}</div>
              </v-col>
            </v-row>
            <v-row dense class="mt-1">
              <v-col v-if="detail.agentRole" cols="4">
                <div class="text-caption text-medium-emphasis">Agent Role</div>
                <div class="text-body-2">{{ detail.agentRole }}</div>
              </v-col>
              <v-col v-if="detail.manualIntentUid" cols="4">
                <div class="text-caption text-medium-emphasis">Manual Intent</div>
                <div class="text-body-2"><code>{{ detail.manualIntentUid }}</code></div>
              </v-col>
              <v-col v-if="detail.advisorDecisionUid" cols="4">
                <div class="text-caption text-medium-emphasis">Advisor</div>
                <div class="text-body-2"><code>{{ detail.advisorDecisionUid }}</code></div>
              </v-col>
            </v-row>
          </div>

          <v-divider class="mb-4" />

          <!-- 候选与决策 -->
          <div class="mb-4">
            <div class="text-caption font-weight-bold mb-2 text-medium-emphasis">
              {{ t('autopilot.traceDetail.candidates') }}
              <span class="text-medium-emphasis">
                ({{ detail.candidatesAfter }}/{{ detail.candidatesBefore }})
              </span>
            </div>
            <v-table v-if="detail.candidates && detail.candidates.length > 0" density="compact">
              <thead>
                <tr>
                  <th class="text-caption">Channel</th>
                  <th class="text-caption">Model</th>
                  <th class="text-caption">Origin Tier</th>
                  <th class="text-caption">Score</th>
                  <th class="text-caption">Selected</th>
                  <th class="text-caption">Filter Reasons</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="(cand, ci) in detail.candidates" :key="ci">
                  <td class="text-caption">{{ formatChannelDisplay(cand) }}</td>
                  <td class="text-caption">{{ displayCandidateModel(cand) }}</td>
                  <td class="text-caption">{{ cand.originTier || '-' }}</td>
                  <td class="text-caption">{{ cand.totalScore.toFixed(3) }}</td>
                  <td>
                    <v-icon v-if="cand.selected" size="14" color="success">mdi-check</v-icon>
                    <v-icon v-else size="14" color="grey">mdi-minus</v-icon>
                  </td>
                  <td class="text-caption">{{ cand.filterReasons?.join('; ') || '-' }}</td>
                </tr>
              </tbody>
            </v-table>
            <div v-else class="text-caption text-medium-emphasis">{{ t('autopilot.traceDetail.noCandidates') }}</div>

            <!-- 全局过滤原因 -->
            <div v-if="detail.globalFilterReasons && Object.keys(detail.globalFilterReasons).length > 0" class="mt-2">
              <div class="text-caption font-weight-bold mb-1">{{ t('autopilot.traceDetail.globalFilterReasons') }}</div>
              <div v-for="(reasons, stage) in detail.globalFilterReasons" :key="stage" class="text-caption">
                <strong>{{ stage }}:</strong> {{ reasons.join(', ') }}
              </div>
            </div>

            <!-- 排序原因 -->
            <div v-if="detail.sortReasons && detail.sortReasons.length > 0" class="mt-2">
              <div class="text-caption font-weight-bold mb-1">{{ t('autopilot.traceTable.sortReasons') }}</div>
              <ul class="text-caption text-medium-emphasis ml-4">
                <li v-for="(reason, ri) in detail.sortReasons" :key="ri">{{ reason }}</li>
              </ul>
            </div>
          </div>

          <v-divider class="mb-4" />

          <!-- Scheduler 裁决 -->
          <div v-if="detail.schedulerDecision" class="mb-4">
            <div class="text-caption font-weight-bold mb-2 text-medium-emphasis">
              {{ t('autopilot.traceDetail.schedulerDecision') }}
            </div>
            <v-table v-if="detail.schedulerDecision.stages" density="compact">
              <thead>
                <tr>
                  <th class="text-caption">Stage</th>
                  <th class="text-caption">Count</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="(stage, si) in detail.schedulerDecision.stages" :key="si">
                  <td class="text-caption">{{ stage.name }}</td>
                  <td class="text-caption">{{ stage.count }}</td>
                </tr>
              </tbody>
            </v-table>
            <div v-if="detail.schedulerDecision.selectedUid" class="text-caption mt-2">
              <strong>{{ t('autopilot.traceDetail.selected') }}:</strong>
              <code>{{ detail.schedulerDecision.selectedUid }}</code>
              ({{ detail.schedulerDecision.selectionCode || detail.schedulerDecision.selectedName || '-' }})
            </div>
            <div v-if="detail.schedulerDecision.skipReasons && detail.schedulerDecision.skipReasons.length > 0" class="text-caption mt-1">
              <strong>{{ t('autopilot.traceDetail.skipReasons') }}:</strong>
              {{ detail.schedulerDecision.skipReasons.join(', ') }}
            </div>
            <template v-if="detail.schedulerDecision.skippedCandidates && detail.schedulerDecision.skippedCandidates.length > 0">
              <div class="text-caption font-weight-bold mt-2 mb-1 text-medium-emphasis">
                {{ t('autopilot.traceDetail.skippedCandidates') }}
              </div>
              <v-table density="compact">
                <thead>
                  <tr>
                    <th class="text-caption">Stage</th>
                    <th class="text-caption">Channel</th>
                    <th class="text-caption">Reason</th>
                    <th class="text-caption">Details</th>
                  </tr>
                </thead>
                <tbody>
                  <tr v-for="(cand, ci) in detail.schedulerDecision.skippedCandidates" :key="ci">
                    <td class="text-caption">{{ cand.stage }}</td>
                    <td class="text-caption">{{ cand.channelName || cand.channelIndex }}</td>
                    <td class="text-caption">{{ cand.reason }}</td>
                    <td class="text-caption">{{ cand.details || '-' }}</td>
                  </tr>
                </tbody>
              </v-table>
            </template>
          </div>

          <!-- endpoint 尝试 -->
          <div v-if="detail.endpointAttempts && detail.endpointAttempts.length > 0" class="mb-4">
            <div class="text-caption font-weight-bold mb-2 text-medium-emphasis">
              {{ t('autopilot.traceDetail.attempts') }}
              <span v-if="detail.attemptsTruncated" class="text-warning">
                ({{ t('autopilot.traceDetail.truncated') }}: {{ detail.attemptsTotal }})
              </span>
            </div>
            <v-table density="compact">
              <thead>
                <tr>
                  <th class="text-caption">#</th>
                  <th class="text-caption">Channel</th>
                  <th class="text-caption">Endpoint</th>
                  <th class="text-caption">Actual Model</th>
                  <th class="text-caption">Effort</th>
                  <th class="text-caption">Result</th>
                  <th class="text-caption">Status</th>
                  <th class="text-caption">Duration</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="(att, ai) in detail.endpointAttempts" :key="ai">
                  <td class="text-caption">{{ att.attemptSeq }}</td>
                  <td class="text-caption">{{ att.channelUid }}</td>
                  <td class="text-caption">{{ att.endpointLabel }}</td>
                  <td class="text-caption">{{ att.actualModel || '-' }}</td>
                  <td class="text-caption">{{ att.actualEffort || '-' }}</td>
                  <td>
                    <v-chip size="x-small" :color="outcomeColor(att.result)" variant="tonal">{{ att.result }}</v-chip>
                  </td>
                  <td class="text-caption">{{ att.statusCode || '-' }}</td>
                  <td class="text-caption">{{ att.durationMs ? att.durationMs + 'ms' : '-' }}</td>
                </tr>
              </tbody>
            </v-table>
            <div v-if="detail.attemptsByResult && Object.keys(detail.attemptsByResult).length > 0" class="text-caption mt-1">
              <strong>{{ t('autopilot.traceDetail.attemptsByResult') }}:</strong>
              <span v-for="(cnt, res) in detail.attemptsByResult" :key="res" class="mr-2">
                {{ res }}: {{ cnt }}
              </span>
            </div>
          </div>

          <v-divider class="mb-4" />

          <!-- 终态 -->
          <div>
            <div class="text-caption font-weight-bold mb-2 text-medium-emphasis">
              {{ t('autopilot.traceDetail.outcome') }}
            </div>
            <v-row dense>
              <v-col cols="4">
                <div class="text-caption text-medium-emphasis">{{ t('autopilot.traceDetail.comparison') }}</div>
                <v-chip size="x-small" :color="comparisonColor(detail.comparisonStatus)" variant="flat">
                  {{ t(`autopilot.traceTable.comparison.${detail.comparisonStatus}`) }}
                </v-chip>
              </v-col>
              <v-col cols="4">
                <div class="text-caption text-medium-emphasis">{{ t('autopilot.traceTable.col.outcome') }}</div>
                <v-chip v-if="detail.outcome" size="x-small" :color="outcomeColor(detail.outcome)" variant="tonal">
                  {{ detail.outcome }}
                </v-chip>
                <span v-else class="text-caption">-</span>
              </v-col>
              <v-col cols="4">
                <div class="text-caption text-medium-emphasis">Actual Model</div>
                <div class="text-body-2 text-truncate">{{ detail.actualModel || '-' }}</div>
              </v-col>
              <v-col cols="4">
                <div class="text-caption text-medium-emphasis">Effort</div>
                <div class="text-body-2">{{ detail.actualEffort || '-' }}</div>
              </v-col>
              <v-col cols="4">
                <div class="text-caption text-medium-emphasis">{{ t('autopilot.traceDetail.statusCode') }}</div>
                <div class="text-body-2">{{ detail.statusCode || '-' }}</div>
              </v-col>
              <v-col cols="4">
                <div class="text-caption text-medium-emphasis">{{ t('autopilot.traceDetail.duration') }}</div>
                <div class="text-body-2">{{ detail.requestDurationMs ? detail.requestDurationMs + 'ms' : '-' }}</div>
              </v-col>
              <v-col cols="4">
                <div class="text-caption text-medium-emphasis">{{ t('autopilot.traceDetail.firstByte') }}</div>
                <div class="text-body-2">{{ detail.firstByteLatencyMs ? detail.firstByteLatencyMs + 'ms' : '-' }}</div>
              </v-col>
              <v-col cols="4">
                <div class="text-caption text-medium-emphasis">{{ t('autopilot.traceDetail.fallbackUsed') }}</div>
                <v-icon v-if="detail.fallbackUsed" size="14" color="warning">mdi-check</v-icon>
                <span v-else class="text-caption">-</span>
              </v-col>
            </v-row>
          </div>
        </div>
      </v-card-text>
    </v-card>
  </v-dialog>
</template>

<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from '@/i18n'
import { api } from '@/services/api'
import type { TraceDetailV2, RoutingCandidate } from '@/services/api-types'
import { writeClipboardText } from '@/utils/clipboard'

const props = defineProps<{
  modelValue: boolean
  traceUid: string
}>()

defineEmits<{
  (_e: 'update:modelValue', _v: boolean): void
}>()

const { t } = useI18n()

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
    await writeClipboardText(JSON.stringify(detail.value, null, 2))
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
  () => props.modelValue,
  (open) => {
    if (open && props.traceUid) {
      fetchDetail()
    }
  },
)

watch(
  () => props.traceUid,
  (uid) => {
    if (props.modelValue && uid) {
      fetchDetail()
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
    const resp = await api.getAutopilotTraceDetail(props.traceUid)
    detail.value = resp.trace
  } catch (err: unknown) {
    const status = (err as { status?: number }).status
    if (status === 404) {
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

// 紧凑展示候选渠道：渠道名 (key掩码)。
// 模型名单独成列（displayCandidateModel），不再拼进渠道名。
function formatChannelDisplay(cand: RoutingCandidate): string {
  const name = cand.channelName || cand.channelUid
  if (cand.keyMask) {
    return `${name} (${cand.keyMask})`
  }
  return name
}

// displayCandidateModel 返回该 (渠道, 模型) 行的承接模型名。
// 显式/自动映射时 mappedModel 直接有值；同名承接时 mappedModel 为空，
// 回退取 candidateKey（channelUid|model）的模型段，保证每行模型名非空。
function displayCandidateModel(cand: RoutingCandidate): string {
  if (cand.mappedModel) {
    return cand.mappedModel
  }
  const key = cand.candidateKey
  if (key) {
    const idx = key.indexOf('|')
    if (idx >= 0 && idx < key.length - 1) {
      return key.slice(idx + 1)
    }
  }
  return '-'
}

function modeColor(mode?: string): string {
  const map: Record<string, string> = {
    off: 'grey',
    shadow: 'info',
    assist: 'warning',
    auto: 'success',
    active: 'primary',
    dry_run: 'info',
  }
  return map[mode ?? ''] ?? 'grey'
}

function comparisonColor(status: string): string {
  if (status === 'matched') return 'success'
  if (status === 'mismatched') return 'error'
  return 'grey'
}

function outcomeColor(outcome?: string): string {
  if (outcome === 'success') return 'success'
  if (outcome === 'cancelled') return 'grey'
  if (outcome === 'attempt_failed') return 'warning'
  return 'error'
}
</script>

<style scoped>
.trace-detail-scroll {
  max-height: 80vh;
  overflow-y: auto;
}
</style>
