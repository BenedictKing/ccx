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
        <v-btn icon variant="text" @click="$emit('update:modelValue', false)">
          <v-icon>mdi-close</v-icon>
        </v-btn>
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
                >
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
                :retry-enabled="!isProtocolBusy(test) && Boolean(job?.protocolJobIds?.[test.protocol] || job?.jobId)"
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
                        v-if="test.success && test.protocol !== currentTab"
                        size="x-small"
                        color="primary"
                        variant="tonal"
                        rounded="lg"
                        class="copy-tab-btn"
                        @click="$emit('copyToTab', test.protocol)"
                      >
                        {{ t('capability.copyToTab') }}
                      </v-btn>
                      <v-chip v-else-if="test.protocol === currentTab" size="x-small" color="grey" variant="tonal">
                        {{ t('capability.currentTab') }}
                      </v-chip>
                      <div v-else-if="!test.success && test.protocol !== currentTab" class="d-flex flex-wrap ga-1">
                        <v-btn
                          v-for="successProto in getSuccessfulProtocols()"
                          :key="successProto"
                          size="x-small"
                          :color="getProtocolColor(successProto)"
                          variant="tonal"
                          rounded="lg"
                          class="convert-btn"
                          @click="$emit('copyToTab', test.protocol)"
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
                        :retry-enabled="!isProtocolBusy(test) && Boolean(job?.protocolJobIds?.[test.protocol] || job?.jobId)"
                        @retry-model="handleRetryModel"
                      />
                    </div>
                  </td>
                </tr>
              </template>
            </tbody>
          </v-table>

          <div v-if="state === 'completed'" class="text-caption text-medium-emphasis mt-3 text-right">
            {{ t('capability.totalDuration', { duration: job?.totalDuration }) }}
          </div>
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
  'copyToTab': [protocol: string]
  'cancel': []
  'retryModel': [protocol: string, model: string]
  'testProtocol': [protocol: string]
}>()

const { t } = useI18n()

const errorMessage = ref('')
const cancelling = ref(false)
const rpmValue = ref(10)

watch(() => props.capabilityRpm, (value) => {
  rpmValue.value = value >= 1 && value <= 60 ? Math.floor(value) : 10
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
  if ((props.capabilityJob.status as string) === 'idle') return 'idle'
  if (props.capabilityJob.lifecycle === 'cancelled') return 'cancelled'
  if (props.capabilityJob.lifecycle === 'done') return 'completed'
  if (props.capabilityJob.lifecycle === 'pending') return 'pending'
  return 'running'
})

const displayOutcome = computed(() => props.capabilityJob?.outcome ?? 'unknown')
const runMode = computed(() => props.capabilityJob?.runMode ?? 'fresh')
const isJobActiveLike = computed(() => state.value === 'pending' || state.value === 'running')
const hasNoCompatibleProtocolsYet = computed(() => (job.value?.compatibleProtocols ?? []).length === 0)

// 当状态离开 running 时复位 cancelling（覆盖取消失败、重测恢复等场景）
watch(state, (newState) => {
  if (newState !== 'running') {
    cancelling.value = false
  }
})

const job = computed(() => props.capabilityJob)

const knownProtocols = ['messages', 'chat', 'responses', 'gemini'] as const

const isKnownProtocol = (protocol: string) => knownProtocols.includes(protocol as typeof knownProtocols[number])

const getRunModeLabel = (mode: string) => {
  switch (mode) {
    case 'cache_hit': return t('capability.runModeCacheHit')
    case 'reused_running': return t('capability.runModeReusedRunning')
    case 'resumed_cancelled': return t('capability.runModeResumedCancelled')
    case 'reused_previous_results': return t('capability.runModeReusedPreviousResults')
    default: return mode
  }
}

const getProtocolDisplayName = (protocol: string) => {
  const map: Record<string, string> = {
    messages: 'Claude',
    chat: 'OpenAI Chat',
    gemini: 'Gemini',
    responses: 'Codex'
  }
  return map[protocol] || protocol
}

const getProtocolColor = (protocol: string) => {
  const map: Record<string, string> = {
    messages: 'orange',
    chat: 'primary',
    gemini: 'deep-purple',
    responses: 'teal'
  }
  return map[protocol] || 'grey'
}

const getProtocolIcon = (protocol: string) => {
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
    .filter(t => t.success)
    .map(t => t.protocol)
}

const protocolOrder = ['messages', 'responses', 'chat', 'gemini']

const sortedTests = computed(() => {
  if (!job.value) return []
  return [...job.value.tests]
    .filter(test => isKnownProtocol(test.protocol))
    .sort((a, b) => {
      const indexA = protocolOrder.indexOf(a.protocol)
      const indexB = protocolOrder.indexOf(b.protocol)
      return (indexA === -1 ? 999 : indexA) - (indexB === -1 ? 999 : indexB)
    })
})

const getProtocolDisplayState = (test: CapabilityProtocolJobResult): 'idle' | 'pending' | 'running' | 'success' | 'partial' | 'cancelled' | 'failed' => {
  if ((test.status as string) === 'idle') return 'idle'
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
    case 'idle': return t('capability.notStarted')
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
  const parsedValue = Number.isFinite(rpmValue.value) ? Math.floor(rpmValue.value) : 10
  const nextValue = Math.min(60, Math.max(1, parsedValue || 10))
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

@media (max-width: 720px) {
  .mobile-layout {
    display: block;
  }

  .desktop-layout {
    display: none;
  }
}
</style>
