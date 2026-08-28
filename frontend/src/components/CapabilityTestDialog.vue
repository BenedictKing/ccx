<template>
  <v-dialog
    :model-value="modelValue"
    max-width="960"
    :scrim="true"
    scrollable
    @update:model-value="$emit('update:modelValue', $event)"
  >
    <v-card rounded="xl">
      <v-card-title class="d-flex align-center justify-space-between pa-4">
        <div class="d-flex align-center ga-2 dialog-title-wrapper">
          <v-icon color="success">mdi-test-tube</v-icon>
          <span class="dialog-title">{{ t('capability.title', { channel: channelName }) }}</span>
        </div>
        <v-tooltip :text="t('app.actions.close') + ' (Esc)'" location="bottom" content-class="ccx-tooltip">
          <template #activator="{ props: tooltipProps }">
            <v-btn icon variant="text" v-bind="tooltipProps" @click="$emit('update:modelValue', false)">
              <v-icon>mdi-close</v-icon>
            </v-btn>
          </template>
        </v-tooltip>
      </v-card-title>

      <v-divider />

      <v-card-text class="pa-4">
        <div v-if="state === 'initializing'" class="d-flex flex-column align-center py-8">
          <v-progress-circular indeterminate size="48" color="primary" />
          <p class="text-body-1 mt-4 text-medium-emphasis">{{ t('capability.loadingTitle') }}</p>
        </div>

        <div v-else-if="state === 'error'" class="py-4">
          <v-alert type="error" variant="tonal" rounded="lg">
            {{ errorMessage }}
          </v-alert>
        </div>

        <div v-else-if="job">
          <div class="capability-status-bar mb-4">
            <div class="d-flex align-center flex-wrap ga-2 capability-status-summary">
              <v-chip v-if="runMode !== 'fresh'" color="info" size="small" variant="tonal">
                {{ getRunModeLabel(runMode) }}
              </v-chip>
              <v-chip v-if="displayOutcome === 'partial'" color="warning" size="small" variant="tonal">
                {{ t('capability.partial') }}
              </v-chip>
              <v-chip v-else-if="displayOutcome === 'cancelled'" color="grey" size="small" variant="tonal">
                {{ t('capability.cancelled') }}
              </v-chip>
              <v-chip
                v-for="proto in (job?.compatibleProtocols ?? [])"
                :key="proto"
                :color="getProtocolColor(proto)"
                size="small"
                variant="tonal"
              >
                <v-icon start size="small">{{ getProtocolIcon(proto) }}</v-icon>
                {{ getProtocolDisplayName(proto) }}
              </v-chip>
              <v-chip v-if="hasNoCompatibleProtocolsYet && (state === 'completed' || state === 'cancelled')" color="grey" size="small" variant="tonal">
                {{ t('capability.noCompatibleProtocols') }}
              </v-chip>
              <v-chip v-else-if="hasNoCompatibleProtocolsYet && state !== 'idle'" color="grey" size="small" variant="tonal" class="d-flex align-center ga-2">
                <v-progress-circular v-if="state === 'pending' || state === 'running'" indeterminate size="12" width="2" color="primary" />
                <span>{{ state === 'pending' ? t('capability.modelQueued') : t('capability.protocolRunning') }}</span>
              </v-chip>
              <label class="capability-rpm-inline" :aria-label="t('capability.rpmLabel')">
                <v-icon size="small">mdi-speedometer</v-icon>
                <span class="capability-rpm-label">{{ t('capability.rpmLabel') }}</span>
                <input
                  v-model.number="rpmValue"
                  class="capability-rpm-input"
                  type="number"
                  min="1"
                  max="60"
                  step="1"
                  @blur="handleRpmBlur"
                />
              </label>

              <span v-if="job?.progress?.totalModels && isJobActiveLike" class="text-caption text-medium-emphasis">
                {{ t('capability.progressSummary', { done: job.progress.completedModels, total: job.progress.totalModels }) }}
              </span>
              <span v-if="snapshotUpdatedText" class="text-caption text-medium-emphasis">
                {{ snapshotUpdatedText }}
              </span>
            </div>

            <v-btn
              v-if="state === 'pending' || state === 'running'"
              color="error"
              variant="tonal"
              size="small"
              class="capability-action-btn"
              :loading="cancelling"
              @click="handleCancel"
            >
              <v-icon start size="small">mdi-stop-circle</v-icon>
              {{ cancelling ? t('capability.cancelling') : t('capability.cancel') }}
            </v-btn>
          </div>

          <!-- 移动端卡片布局 -->
          <div class="mobile-layout">
            <div v-for="test in sortedTests" :key="test.protocol" class="protocol-card">
              <div class="protocol-header">
                <v-chip :color="getProtocolColor(test.protocol)" size="small" variant="tonal">
                  {{ getProtocolDisplayName(test.protocol) }}
                </v-chip>
                <div class="d-flex align-center ga-2 flex-wrap justify-end">
                  <v-btn
                    v-if="shouldShowTestProtocolButton(test)"
                    size="x-small"
                    color="secondary"
                    variant="tonal"
                    rounded="lg"
                    :disabled="isTestProtocolButtonDisabled(test)"
                    @click="handleTestProtocol(test.protocol)"
                  >
                    {{ t('capability.startTest') }}
                  </v-btn>
                  <template v-if="!isProtocolFailed(test)">
                    <div class="d-flex align-center ga-1">
                      <v-icon :color="getProtocolStatusIconColor(test)" size="small">{{ getProtocolStatusIcon(test) }}</v-icon>
                      <span :class="['text-body-2', getProtocolStatusTextClass(test)]">{{ getProtocolStatusText(test) }}</span>
                    </div>
                  </template>
                  <v-tooltip v-else :text="getProtocolErrorText(test)" location="top" content-class="error-tooltip">
                    <template #activator="{ props: activatorProps }">
                      <div v-bind="activatorProps" class="d-flex align-center ga-1">
                        <v-icon :color="getProtocolStatusIconColor(test)" size="small">{{ getProtocolStatusIcon(test) }}</v-icon>
                        <span :class="['text-body-2', getProtocolStatusTextClass(test)]">{{ getProtocolStatusText(test) }}</span>
                      </div>
                    </template>
                  </v-tooltip>
                </div>
              </div>

              <CapabilityModelResults
                :test="test"
                :pending-text="getProtocolPendingText(test)"
                :show-label="false"
                :retry-enabled="!isProtocolBusy(test)"
                @retry-model="handleRetryModel"
              />
            </div>
          </div>

          <!-- 桌面端表格布局 -->
          <v-table density="comfortable" class="rounded-lg capability-table desktop-layout">
            <thead>
              <tr>
                <th>{{ t('capability.table.protocol') }}</th>
                <th>{{ t('capability.table.status') }}</th>
                <th>{{ t('capability.table.successCount') }}</th>
                <th>{{ t('capability.table.latency') }}</th>
                <th>{{ t('capability.table.streaming') }}</th>
                <th>{{ t('capability.table.actions') }}</th>
              </tr>
            </thead>
            <tbody>
              <template v-for="test in sortedTests" :key="test.protocol">
                <tr class="protocol-summary-row">
                  <td>
                    <v-chip :color="getProtocolColor(test.protocol)" size="small" variant="tonal">
                      {{ getProtocolDisplayName(test.protocol) }}
                    </v-chip>
                  </td>
                  <td>
                    <template v-if="!isProtocolFailed(test)">
                      <div class="d-flex align-center ga-1">
                        <v-icon :color="getProtocolStatusIconColor(test)" size="small">{{ getProtocolStatusIcon(test) }}</v-icon>
                        <span :class="['text-body-2', getProtocolStatusTextClass(test)]">{{ getProtocolStatusText(test) }}</span>
                      </div>
                    </template>
                    <v-tooltip v-else :text="getProtocolErrorText(test)" location="top" content-class="error-tooltip">
                      <template #activator="{ props: activatorProps }">
                        <div v-bind="activatorProps" class="d-flex align-center ga-1">
                          <v-icon :color="getProtocolStatusIconColor(test)" size="small">{{ getProtocolStatusIcon(test) }}</v-icon>
                          <span :class="['text-body-2', getProtocolStatusTextClass(test)]">{{ getProtocolStatusText(test) }}</span>
                        </div>
                      </template>
                    </v-tooltip>
                  </td>
                  <td>
                    <span :class="['success-ratio-text', getSuccessCount(test) === getAttemptedModels(test) ? 'is-success' : 'is-partial']">
                      {{ formatSuccessRatio(test) }}
                    </span>
                  </td>
                  <td>
                    <span v-if="hasProtocolLatency(test)" class="latency-value">
                      <span class="latency-number">{{ getAverageLatency(test) }}</span>
                      <span class="latency-unit">ms</span>
                    </span>
                    <span v-else class="text-body-2 text-medium-emphasis">-</span>
                  </td>
                  <td>
                    <div v-if="test.success && test.streamingSupported" class="d-flex align-center ga-1">
                      <v-icon color="success" size="small">mdi-check-circle</v-icon>
                      <span class="text-body-2 text-success">{{ t('capability.supported') }}</span>
                    </div>
                    <div v-else-if="test.success" class="d-flex align-center ga-1">
                      <v-icon color="warning" size="small">mdi-minus-circle</v-icon>
                      <span class="text-body-2 text-warning">{{ t('capability.unsupported') }}</span>
                    </div>
                    <span v-else class="text-body-2 text-medium-emphasis">-</span>
                  </td>
                  <td>
                    <div class="d-flex flex-wrap ga-1 align-center justify-end">
                      <v-btn
                        v-if="shouldShowTestProtocolButton(test)"
                        size="x-small"
                        color="secondary"
                        variant="tonal"
                        rounded="lg"
                        :disabled="isTestProtocolButtonDisabled(test)"
                        @click="handleTestProtocol(test.protocol)"
                      >
                        {{ t('capability.startTest') }}
                      </v-btn>
                      <v-btn
                        v-if="test.success && !isCurrentTabProtocol(test.protocol)"
                        size="x-small"
                        color="primary"
                        variant="tonal"
                        rounded="lg"
                        class="copy-tab-btn"
                        @click="$emit('copyToTab', test.protocol, test.protocol)"
                      >
                        {{ t('capability.copyToTab') }}
                      </v-btn>
                      <v-chip v-else-if="isCurrentTabProtocol(test.protocol)" size="x-small" color="grey" variant="tonal">
                        {{ t('capability.currentTab') }}
                      </v-chip>
                      <div v-else-if="!test.success && !isCurrentTabProtocol(test.protocol)" class="d-flex flex-wrap ga-1">
                        <v-btn
                          v-for="successProto in getSuccessfulProtocols()"
                          :key="successProto"
                          size="x-small"
                          :color="getProtocolColor(successProto)"
                          variant="tonal"
                          rounded="lg"
                          class="convert-btn"
                          @click="$emit('copyToTab', test.protocol, successProto)"
                        >
                          {{ t('capability.convert', { protocol: getProtocolDisplayName(successProto) }) }}
                        </v-btn>
                      </div>
                    </div>
                  </td>
                </tr>
                <tr class="protocol-models-row">
                  <td colspan="6" class="model-results-cell">
                    <div class="model-results-wrapper">
                      <CapabilityModelResults
                        :test="test"
                        :pending-text="getProtocolPendingText(test)"
                        :show-label="false"
                        :retry-enabled="!isProtocolBusy(test)"
                        @retry-model="handleRetryModel"
                      />
                    </div>
                  </td>
                </tr>
              </template>
            </tbody>
          </v-table>

          <!-- 模型重定向测试结果（已集成到虚拟协议中，不再单独显示） -->
          <!-- <div v-if="_hasRedirectTests" class="redirect-tests-section mt-4">
            <div class="redirect-tests-header mb-3">
              <v-icon color="info" size="small">mdi-swap-horizontal</v-icon>
              <span class="text-subtitle-2 font-weight-bold ml-2">{{ t('capability.redirectTests') }}</span>
              <span class="text-caption text-medium-emphasis ml-2">
                ({{ t('capability.redirectTestsDesc') }})
              </span>
            </div>
            <div class="redirect-tests-flow">
              <v-tooltip
                v-for="(result, index) in job?.redirectTests"
                :key="`redirect-${index}`"
                location="top"
                :content-class="result.success ? 'success-tooltip' : 'error-tooltip'"
              >
                <template #activator="{ props: tooltipProps }">
                  <div
                    v-bind="tooltipProps"
                    :class="['redirect-test-badge', result.success ? 'success-badge' : 'error-badge']"
                  >
                    <span class="redirect-probe-model">{{ result.probeModel }}</span>
                    <v-icon size="14">mdi-arrow-right</v-icon>
                    <span class="redirect-actual-model">{{ result.actualModel }}</span>
                    <v-icon size="16">
                      {{ result.success ? 'mdi-check-circle' : 'mdi-close-circle' }}
                    </v-icon>
                  </div>
                </template>
                <div class="tooltip-content">
                  <div class="tooltip-title">{{ result.probeModel }} → {{ result.actualModel }}</div>
                  <div class="tooltip-row">
                    <span class="tooltip-label">{{ t('capability.tooltipLatency') }}</span>
                    <span class="tooltip-value">{{ result.latency >= 0 ? `${result.latency}ms` : '-' }}</span>
                  </div>
                  <div v-if="result.success && result.streamingSupported !== undefined" class="tooltip-row">
                    <span class="tooltip-label">{{ t('capability.tooltipStreaming') }}</span>
                    <span class="tooltip-value">{{ result.streamingSupported ? t('capability.supported') : t('capability.unsupported') }}</span>
                  </div>
                  <div class="tooltip-row">
                    <span class="tooltip-label">{{ t('capability.modelStatus') }}</span>
                    <span class="tooltip-value">{{ result.success ? t('capability.modelSuccess') : t('capability.modelFailed') }}</span>
                  </div>
                  <div v-if="!result.success && result.error" class="tooltip-error">{{ result.error }}</div>
                </div>
              </v-tooltip>
            </div>
          </div> -->

        </div>
      </v-card-text>
    </v-card>
  </v-dialog>
</template>

<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import type {
  CapabilityTestJob,
  CapabilityProtocolJobResult
} from '../services/api'
import { useI18n } from '../i18n'
import CapabilityModelResults from './CapabilityModelResults.vue'
import capabilityProbeSchema from '../../../shared/capability-probe-schema.json'

const capabilityBaseProtocolOrder = capabilityProbeSchema.baseProtocols as readonly ('messages' | 'chat' | 'responses' | 'gemini')[]
type CapabilityBaseProtocol = typeof capabilityBaseProtocolOrder[number]

interface Props {
  modelValue: boolean
  channelName: string
  currentTab: string
  capabilityJob: CapabilityTestJob | null
  capabilityRpm: number
}

const props = defineProps<Props>()
const emit = defineEmits<{
  'update:modelValue': [value: boolean]
  'update:capabilityRpm': [value: number]
  'copyToTab': [targetProtocol: string, serviceProtocol?: string]
  'cancel': []
  'retryModel': [protocol: string, model: string]
  'testProtocol': [protocol: string]
}>()

const { t } = useI18n()

const errorMessage = ref('')
const cancelling = ref(false)
const DEFAULT_CAPABILITY_TEST_RPM = 30
const rpmValue = ref(DEFAULT_CAPABILITY_TEST_RPM)

watch(() => props.capabilityRpm, (value) => {
  rpmValue.value = value >= 1 && value <= 60 ? Math.floor(value) : DEFAULT_CAPABILITY_TEST_RPM
}, { immediate: true })

watch(() => props.modelValue, (open) => {
  if (open) {
    errorMessage.value = ''
    cancelling.value = false
  }
})

watch(() => props.capabilityJob?.jobId ?? '', (nextJobId, prevJobId) => {
  if (nextJobId !== prevJobId) {
    errorMessage.value = ''
  }
})

watch(() => props.capabilityJob?.error, (error) => {
  if (!error) return
  if (error === 'no_api_key') {
    errorMessage.value = t('capability.noApiKeyError')
    return
  }
  errorMessage.value = t('capability.genericJobError', { message: error })
})

const state = computed(() => {
  if (errorMessage.value) return 'error'
  if (!props.capabilityJob) return 'initializing'
  if ((props.capabilityJob.status as any) === 'idle') return 'idle'
  if (props.capabilityJob.lifecycle === 'cancelled') return 'cancelled'
  if (props.capabilityJob.lifecycle === 'done') return 'completed'
  if (props.capabilityJob.lifecycle === 'pending') return 'pending'
  return 'running'
})

const displayOutcome = computed(() => props.capabilityJob?.outcome ?? 'unknown')
const runMode = computed(() => props.capabilityJob?.runMode ?? 'fresh')
const isJobActiveLike = computed(() => state.value === 'pending' || state.value === 'running')
const hasNoCompatibleProtocolsYet = computed(() => (job.value?.compatibleProtocols ?? []).length === 0)
const _hasRedirectTests = computed(() => (job.value?.redirectTests ?? []).length > 0)

// 当状态离开 running 时复位 cancelling（覆盖取消失败、重测恢复等场景）
watch(state, (newState) => {
  if (newState !== 'running') {
    cancelling.value = false
  }
})

const job = computed(() => props.capabilityJob)

// 基础协议列表从 shared/capability-probe-schema.json 读取，保证前后端单一真相源
const knownBaseProtocols = capabilityBaseProtocolOrder

// 复合协议检测：包含 "->" 的协议字符串（如 "messages->responses"）
const isCompositeProtocol = (protocol: string): boolean => protocol.includes('->')

// 基础协议校验：直接匹配已知协议，或从复合协议中提取 from 部分校验
const isKnownProtocol = (protocol: string): boolean => {
  if (knownBaseProtocols.includes(protocol as CapabilityBaseProtocol)) return true
  if (isCompositeProtocol(protocol)) {
    const from = protocol.split('->')[0]
    return knownBaseProtocols.includes(from as CapabilityBaseProtocol)
  }
  return false
}

const getRunModeLabel = (mode: string) => {
  switch (mode) {
    case 'cache_hit': return t('capability.runModeCacheHit')
    case 'reused_running': return t('capability.runModeReusedRunning')
    case 'resumed_cancelled': return t('capability.runModeResumedCancelled')
    case 'reused_previous_results': return t('capability.runModeReusedPreviousResults')
    default: return mode
  }
}

const baseProtocolDisplayNames: Record<string, string> = {
  messages: 'Claude',
  chat: 'OpenAI Chat',
  gemini: 'Gemini',
  responses: 'Codex'
}

const getProtocolDisplayName = (protocol: string): string => {
  if (isCompositeProtocol(protocol)) {
    const [from, to] = protocol.split('->')
    const fromName = baseProtocolDisplayNames[from] || from
    const toName = baseProtocolDisplayNames[to] || to
    return `${fromName} → ${toName}`
  }
  return baseProtocolDisplayNames[protocol] || protocol
}

const getProtocolColor = (protocol: string): string => {
  if (isCompositeProtocol(protocol)) return 'cyan-darken-1'
  const map: Record<string, string> = {
    messages: 'orange',
    chat: 'primary',
    gemini: 'deep-purple',
    responses: 'teal'
  }
  return map[protocol] || 'grey'
}

// 检查协议是否与当前 Tab 相关（包括虚拟协议）
const isCurrentTabProtocol = (protocol: string): boolean => {
  if (protocol === props.currentTab) return true
  if (isCompositeProtocol(protocol)) {
    const from = protocol.split('->')[0]
    return from === props.currentTab
  }
  return false
}

const getProtocolIcon = (protocol: string): string => {
  if (isCompositeProtocol(protocol)) return 'mdi-swap-horizontal'
  const map: Record<string, string> = {
    messages: 'mdi-message-processing',
    chat: 'mdi-robot',
    gemini: 'mdi-diamond-stone',
    responses: 'mdi-code-braces'
  }
  return map[protocol] || 'mdi-api'
}

const getSuccessfulProtocols = () => {
  if (!job.value) return []
  return job.value.tests
    .filter(t => t.success && !isCompositeProtocol(t.protocol))
    .map(t => t.protocol)
}

// 协议排序：复合协议始终排在第一位，其余按 schema 基础协议顺序
const baseProtocolOrder = capabilityBaseProtocolOrder as readonly string[]

const sortedTests = computed(() => {
  if (!job.value) return []
  return [...job.value.tests]
    .filter(test => isKnownProtocol(test.protocol))
    .sort((a, b) => {
      const aIsComposite = isCompositeProtocol(a.protocol)
      const bIsComposite = isCompositeProtocol(b.protocol)
      // 复合协议始终排在最前面
      if (aIsComposite && !bIsComposite) return -1
      if (!aIsComposite && bIsComposite) return 1
      // 同类型内按基础协议顺序排序
      const getBase = (p: string) => isCompositeProtocol(p) ? p.split('->')[0] : p
      const indexA = baseProtocolOrder.indexOf(getBase(a.protocol))
      const indexB = baseProtocolOrder.indexOf(getBase(b.protocol))
      return (indexA === -1 ? 999 : indexA) - (indexB === -1 ? 999 : indexB)
    })
})

const getProtocolDisplayState = (test: CapabilityProtocolJobResult): 'idle' | 'pending' | 'running' | 'success' | 'partial' | 'cancelled' | 'failed' => {
  if ((test.status as any) === 'idle') return 'idle'
  if (test.lifecycle === 'active') return 'running'
  if (test.lifecycle === 'pending') return 'pending'
  if (test.outcome === 'partial') return 'partial'
  if (test.outcome === 'cancelled') return 'cancelled'
  if (test.outcome === 'success') return 'success'
  return 'failed'
}

const isProtocolFailed = (test: CapabilityProtocolJobResult): boolean => {
  return getProtocolDisplayState(test) === 'failed'
}

const isProtocolBusy = (test: CapabilityProtocolJobResult): boolean => {
  const displayState = getProtocolDisplayState(test)
  return displayState === 'pending' || displayState === 'running'
}

const getProtocolStatusIcon = (test: CapabilityProtocolJobResult): string => {
  switch (getProtocolDisplayState(test)) {
    case 'idle': return 'mdi-clock-outline'
    case 'pending': return 'mdi-timer-sand'
    case 'running': return 'mdi-progress-clock'
    case 'success': return 'mdi-check-circle'
    case 'partial': return 'mdi-alert-circle'
    case 'cancelled': return 'mdi-stop-circle-outline'
    default: return 'mdi-close-circle'
  }
}

const getProtocolStatusIconColor = (test: CapabilityProtocolJobResult): string => {
  switch (getProtocolDisplayState(test)) {
    case 'success': return 'success'
    case 'partial': return 'warning'
    case 'failed': return 'error'
    case 'cancelled': return 'grey'
    default: return 'primary'
  }
}

const getProtocolStatusText = (test: CapabilityProtocolJobResult): string => {
  switch (getProtocolDisplayState(test)) {
    case 'idle': return t('capability.notStarted')
    case 'pending': return t('capability.modelQueued')
    case 'running': return t('capability.protocolRunning')
    case 'success': return t('capability.success')
    case 'partial': return t('capability.partial')
    case 'cancelled': return t('capability.cancelled')
    default: return t('capability.failed')
  }
}

const getProtocolStatusTextClass = (test: CapabilityProtocolJobResult): string => {
  switch (getProtocolDisplayState(test)) {
    case 'success': return 'text-success'
    case 'partial': return 'text-warning'
    case 'failed': return 'text-error'
    default: return 'text-medium-emphasis'
  }
}

const getProtocolErrorText = (test: CapabilityProtocolJobResult): string => {
  if (test.reason === 'not_run') return t('capability.reasonNotRun')
  if (test.reason === 'cancelled') return t('capability.reasonCancelled')
  if (test.error === 'timeout') return t('capability.reasonTimeout')
  return test.error || t('capability.failedTooltip')
}

const getProtocolPendingText = (test: CapabilityProtocolJobResult): string => {
  const displayState = getProtocolDisplayState(test)
  if (displayState === 'idle') return t('capability.notStarted')
  if (displayState === 'pending') return t('capability.modelQueued')
  if (displayState === 'running') return t('capability.protocolRunning')
  return t('capability.modelDetailsUnavailable')
}

const getAttemptedModels = (test: CapabilityProtocolJobResult): number => {
  if (typeof test.attemptedModels === 'number') return test.attemptedModels
  return Array.isArray(test.modelResults) ? test.modelResults.length : 0
}

const getSuccessCount = (test: CapabilityProtocolJobResult): number => {
  if (typeof test.successCount === 'number') return test.successCount
  return (test.modelResults ?? []).filter(modelResult => modelResult.success).length
}

const formatSuccessRatio = (test: CapabilityProtocolJobResult): string => {
  const attemptedModels = getAttemptedModels(test)
  if (attemptedModels <= 0) return '-'
  return `${getSuccessCount(test)}/${attemptedModels}`
}

const getAverageLatency = (test: CapabilityProtocolJobResult): number => {
  const successModels = (test.modelResults ?? []).filter(m => m.success && typeof m.latency === 'number' && m.latency >= 0)
  if (successModels.length === 0) return -1
  const total = successModels.reduce((sum, m) => sum + m.latency, 0)
  return Math.round(total / successModels.length)
}

const hasProtocolLatency = (test: CapabilityProtocolJobResult): boolean => {
  return getAverageLatency(test) >= 0
}

const snapshotUpdatedText = computed(() => {
  const updatedAt = props.capabilityJob?.snapshotUpdatedAt
  if (!updatedAt) return ''
  return t('capability.snapshotUpdated', { time: updatedAt })
})

const shouldShowTestProtocolButton = (test: CapabilityProtocolJobResult): boolean => {
  const displayState = getProtocolDisplayState(test)
  return displayState !== 'pending' && displayState !== 'running'
}

const isTestProtocolButtonDisabled = (test: CapabilityProtocolJobResult): boolean => {
  const displayState = getProtocolDisplayState(test)
  return displayState === 'pending' || displayState === 'running'
}

const handleTestProtocol = (protocol: string) => {
  emit('testProtocol', protocol)
}

const handleRpmBlur = () => {
  const parsedValue = Number.isFinite(rpmValue.value) ? Math.floor(rpmValue.value) : DEFAULT_CAPABILITY_TEST_RPM
  const nextValue = Math.min(60, Math.max(1, parsedValue || DEFAULT_CAPABILITY_TEST_RPM))
  rpmValue.value = nextValue
  emit('update:capabilityRpm', nextValue)
}

const setError = (error: string) => {
  errorMessage.value = error
}

const handleCancel = () => {
  cancelling.value = true
  emit('cancel')
}

const handleRetryModel = (protocol: string, model: string) => {
  emit('retryModel', protocol, model)
}

// Esc 关闭由 Vuetify 原生按 overlay 栈处理（仅关最上层）

defineExpose({ setError })
</script>

<style scoped>
.dialog-title-wrapper {
  flex: 1;
  min-width: 0;
}

.capability-status-bar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 8px;
  flex-wrap: wrap;
}

.capability-status-summary {
  flex: 1;
  min-width: 0;
}

.capability-rpm-inline {
  display: inline-flex;
  align-items: center;
  flex: 0 0 auto;
  gap: 6px;
  min-height: 26px;
  padding: 0 8px;
  border: 1px solid rgba(var(--v-theme-outline), 0.38);
  border-radius: 6px;
  color: rgba(var(--v-theme-on-surface), 0.78);
  background: rgb(var(--v-theme-surface));
  font-size: 0.75rem;
  line-height: 1;
}

.capability-rpm-inline:focus-within {
  border-color: rgb(var(--v-theme-primary));
  box-shadow: 0 0 0 1px rgb(var(--v-theme-primary));
}

.capability-rpm-label {
  white-space: nowrap;
}

.capability-rpm-input {
  box-sizing: border-box;
  width: 24px;
  min-width: 24px;
  border: 0;
  outline: 0;
  padding: 0;
  -moz-appearance: textfield;
  -webkit-appearance: none;
  appearance: textfield;
  background: transparent;
  color: rgb(var(--v-theme-on-surface));
  font: inherit;
  font-weight: 600;
  line-height: 1.35;
  text-align: right;
}

.capability-rpm-input::-webkit-outer-spin-button,
.capability-rpm-input::-webkit-inner-spin-button {
  -webkit-appearance: none;
  appearance: none;
  margin: 0;
}

.capability-table :deep(th) {
  white-space: nowrap;
}

/* 协议摘要行：顶部有分割线，底线去掉（和下面模型行视觉合并） */
.capability-table :deep(.protocol-summary-row > td) {
  border-top: thin solid rgba(var(--v-border-color), var(--v-border-opacity)) !important;
  border-bottom: none !important;
}

/* 第一个协议摘要行：去掉顶部线，避免和表头底线重叠 */
.capability-table :deep(tbody > .protocol-summary-row:first-child > td) {
  border-top: none !important;
}

/* 模型行：去掉表格默认边框，组间分割线统一由下一组摘要行顶部负责 */
.capability-table :deep(.protocol-models-row > td) {
  border-top: none !important;
  border-bottom: none !important;
}

.mobile-layout {
  display: none;
}

.desktop-layout {
  display: table;
}

.protocol-card {
  padding: 16px;
  margin-bottom: 12px;
  border-radius: 12px;
  background: rgba(var(--v-theme-surface-variant), 0.12);
  border: 1px solid rgba(var(--v-theme-outline), 0.16);
  box-shadow: inset 3px 0 0 0 rgba(var(--v-theme-outline), 0.18);
}

.protocol-header {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}

.model-results-cell {
  padding: 0 !important;
  background: rgba(var(--v-theme-surface-variant), 0.12);
  border-bottom: 1px solid rgba(var(--v-theme-outline), 0.16) !important;
  box-shadow: inset 3px 0 0 0 rgba(var(--v-theme-outline), 0.18);
}

.latency-value {
  display: inline-flex;
  align-items: baseline;
  gap: 2px;
}

.success-ratio-text {
  min-width: 2.5rem;
  font-size: 0.8125rem;
  font-weight: 600;
}

.success-ratio-text.is-success {
  color: rgb(var(--v-theme-success));
}

.success-ratio-text.is-partial {
  color: rgba(var(--v-theme-on-surface), 0.82);
}

.latency-number {
  font-size: 0.875rem;
  font-weight: 600;
  color: rgba(var(--v-theme-on-surface), 0.92);
}

.latency-unit {
  font-size: 0.75rem;
  color: rgba(var(--v-theme-on-surface), 0.56);
}

.capability-action-btn,
.copy-tab-btn,
.convert-btn {
  text-transform: none;
  font-size: 0.75rem;
  font-weight: 600;
  letter-spacing: 0;
}

.capability-action-btn :deep(.v-btn__content),
.copy-tab-btn :deep(.v-btn__content),
.convert-btn :deep(.v-btn__content) {
  line-height: 1.4;
}

.redirect-tests-section {
  padding: 16px;
  border-radius: 12px;
  background: rgba(var(--v-theme-surface-variant), 0.08);
  border: 1px solid rgba(var(--v-theme-outline), 0.12);
}

.redirect-tests-header {
  display: flex;
  align-items: center;
}

.redirect-tests-flow {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.redirect-test-badge {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 6px 12px;
  border-radius: 999px;
  font-size: 0.75rem;
  font-weight: 600;
  line-height: 1;
  border: 1px solid transparent;
  cursor: default;
}

.redirect-test-badge.success-badge {
  background: rgba(34, 197, 94, 0.12);
  color: rgba(21, 128, 61, 0.95);
  border-color: rgba(34, 197, 94, 0.24);
}

.redirect-test-badge.error-badge {
  background: rgba(239, 68, 68, 0.12);
  color: rgba(185, 28, 28, 0.95);
  border-color: rgba(239, 68, 68, 0.24);
}

:global(.v-theme--dark) .redirect-test-badge.success-badge {
  color: rgba(134, 239, 172, 0.96);
}

:global(.v-theme--dark) .redirect-test-badge.error-badge {
  color: rgba(252, 165, 165, 0.96);
}

.redirect-probe-model {
  font-weight: 700;
}

.redirect-actual-model {
  font-weight: 500;
  opacity: 0.85;
}

@media (max-width: 720px) {
  .mobile-layout {
    display: block;
  }

  .desktop-layout {
    display: none;
  }
}
</style>
