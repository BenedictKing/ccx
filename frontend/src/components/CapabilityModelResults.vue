<template>
  <div class="model-results-wrapper">
    <div v-if="shouldShowPendingPlaceholder" class="d-flex align-center ga-2 py-2">
      <v-progress-circular indeterminate size="16" width="2" color="primary" />
      <span class="text-body-2 text-medium-emphasis">{{ pendingText }}</span>
    </div>
    <div v-else-if="shouldShowDetailsUnavailable" class="text-body-2 text-medium-emphasis py-2">
      {{ t('capability.modelDetailsUnavailable') }}
    </div>

    <div v-else>
      <div v-if="showLabel" class="models-label">{{ t('capability.modelsLabel') }}</div>
      <div class="model-results-flow">
        <v-tooltip
          v-for="modelResult in modelResults"
          :key="`${test.protocol}-${modelResult.model}`"
          location="top"
          :content-class="getModelTooltipClasses(modelResult)"
        >
          <template #activator="{ props: tooltipProps }">
            <div
              v-bind="tooltipProps"
              :class="getModelBadgeClasses(modelResult)"
              @click="shouldRetryModel(modelResult) ? emit('retryModel', test.protocol, modelResult.model) : undefined"
            >
              <span class="model-name">{{ modelResult.model }}</span>
              <v-icon v-if="isRedirectedModel(modelResult)" size="14" class="redirect-icon">
                mdi-arrow-right-bold
              </v-icon>
              <v-icon v-if="isUpstreamModelRedirected(modelResult)" size="14" class="upstream-redirect-icon">
                mdi-swap-horizontal-circle
              </v-icon>
              <v-icon size="16">
                {{ getModelStatusIcon(modelResult) }}
              </v-icon>
              <v-icon
                v-if="isModelSuccessful(modelResult)"
                size="14"
                class="mapping-action-icon"
                :title="t('capability.createMapping')"
                @click.stop="emit('createMapping', test.protocol, modelResult.model)"
              >
                mdi-link-plus
              </v-icon>
            </div>
          </template>
          <div v-if="getModelTooltipView(modelResult) === 'success'" class="tooltip-content">
            <div class="tooltip-title">{{ modelResult.model }}</div>
            <div v-if="modelResult.actualModel" class="tooltip-row">
              <span class="tooltip-label">{{ t('capability.actualModel') }}</span>
              <span class="tooltip-value">{{ modelResult.actualModel }}</span>
            </div>
            <div v-if="modelResult.upstreamModel" class="tooltip-row">
              <span class="tooltip-label">{{ t('capability.upstreamModel') }}</span>
              <span :class="['tooltip-value', isUpstreamModelRedirected(modelResult) ? 'tooltip-value-warning' : '']">{{ modelResult.upstreamModel }}</span>
            </div>
            <div class="tooltip-row">
              <span class="tooltip-label">{{ t('capability.tooltipLatency') }}</span>
              <span class="tooltip-value">{{ getModelTooltipLatencyText(modelResult) }}</span>
            </div>
            <div class="tooltip-row">
              <span class="tooltip-label">{{ t('capability.tooltipStreaming') }}</span>
              <span class="tooltip-value">{{ formatStreaming(modelResult) }}</span>
            </div>
            <div class="tooltip-row">
              <span class="tooltip-label">{{ t('capability.modelStatus') }}</span>
              <span class="tooltip-value">{{ getModelStatusText(modelResult) }}</span>
            </div>
            <div v-if="modelResult.codexImageGeneration?.tested" class="tooltip-row">
              <span class="tooltip-label">{{ t('capability.codexImageGeneration') }}</span>
              <span class="tooltip-value">{{ formatCodexImageGeneration(modelResult) }}</span>
            </div>
          </div>
          <div v-else-if="getModelTooltipView(modelResult) === 'pending'" class="tooltip-content">
            <div class="tooltip-title">{{ modelResult.model }}</div>
            <div v-if="modelResult.actualModel" class="tooltip-row">
              <span class="tooltip-label">{{ t('capability.actualModel') }}</span>
              <span class="tooltip-value">{{ modelResult.actualModel }}</span>
            </div>
            <div class="tooltip-row">
              <span class="tooltip-label">{{ t('capability.modelStatus') }}</span>
              <span class="tooltip-value">{{ getModelStatusText(modelResult) }}</span>
            </div>
          </div>
          <div v-else class="tooltip-content">
            <div class="tooltip-title">{{ modelResult.model }}</div>
            <div v-if="modelResult.actualModel" class="tooltip-row">
              <span class="tooltip-label">{{ t('capability.actualModel') }}</span>
              <span class="tooltip-value">{{ modelResult.actualModel }}</span>
            </div>
            <div v-if="modelResult.upstreamModel" class="tooltip-row">
              <span class="tooltip-label">{{ t('capability.upstreamModel') }}</span>
              <span :class="['tooltip-value', isUpstreamModelRedirected(modelResult) ? 'tooltip-value-warning' : '']">{{ modelResult.upstreamModel }}</span>
            </div>
            <div class="tooltip-row">
              <span class="tooltip-label">{{ t('capability.modelStatus') }}</span>
              <span class="tooltip-value">{{ getModelStatusText(modelResult) }}</span>
            </div>
            <div v-if="modelResult.codexImageGeneration?.tested" class="tooltip-row">
              <span class="tooltip-label">{{ t('capability.codexImageGeneration') }}</span>
              <span class="tooltip-value">{{ formatCodexImageGeneration(modelResult) }}</span>
            </div>
            <div v-if="getModelDisplayState(modelResult) !== 'idle'" class="tooltip-error">{{ getModelTooltipError(modelResult) }}</div>
            <div v-if="getModelRetryHintVisible(modelResult)" class="tooltip-retry">{{ getModelDisplayState(modelResult) === 'idle' ? t('capability.testModel') : t('capability.retryModel') }}</div>
          </div>
        </v-tooltip>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed } from 'vue'
import type { CapabilityProtocolJobResult, CapabilityModelJobResult } from '../services/api'
import { useI18n } from '../i18n'

interface Props {
  test: CapabilityProtocolJobResult
  pendingText: string
  showLabel?: boolean
  retryEnabled?: boolean
}

const props = withDefaults(defineProps<Props>(), {
  showLabel: false,
  retryEnabled: true
})

const emit = defineEmits<{
  'retryModel': [protocol: string, model: string]
  'createMapping': [protocol: string, model: string]
}>()

const { t } = useI18n()

const modelResults = computed(() => Array.isArray(props.test.modelResults) ? props.test.modelResults : [])
const shouldShowPendingPlaceholder = computed(() => modelResults.value.length === 0 && (props.test.lifecycle === 'pending' || props.test.lifecycle === 'active'))
const shouldShowDetailsUnavailable = computed(() => modelResults.value.length === 0 && !shouldShowPendingPlaceholder.value)

const getModelDisplayState = (modelResult: CapabilityModelJobResult): 'idle' | 'pending' | 'running' | 'success' | 'cancelled' | 'skipped' | 'failed' => {
  if ((modelResult.status as any) === 'idle') return 'idle'
  if (modelResult.lifecycle === 'pending') return 'pending'
  if (modelResult.lifecycle === 'active') return 'running'
  if (modelResult.lifecycle === 'cancelled' || modelResult.outcome === 'cancelled') return 'cancelled'
  if (modelResult.status === 'skipped') return 'skipped'
  if (modelResult.outcome === 'success') return 'success'
  return 'failed'
}

const getModelBadgeClass = (modelResult: CapabilityModelJobResult): string => {
  switch (getModelDisplayState(modelResult)) {
    case 'idle': return 'skipped-badge'
    case 'running': return 'running-badge'
    case 'pending': return 'queued-badge'
    case 'success': return 'success-badge'
    case 'cancelled':
    case 'skipped': return 'skipped-badge'
    default: return 'error-badge'
  }
}

const getModelStatusIcon = (modelResult: CapabilityModelJobResult): string => {
  switch (getModelDisplayState(modelResult)) {
    case 'idle': return 'mdi-clock-outline'
    case 'pending': return 'mdi-timer-sand'
    case 'running': return 'mdi-progress-clock'
    case 'cancelled': return 'mdi-stop-circle-outline'
    case 'skipped': return 'mdi-skip-next'
    case 'success': return 'mdi-check-circle'
    default: return 'mdi-close-circle'
  }
}

const getModelStatusLabel = (status: string, modelResult?: CapabilityModelJobResult) => {
  if (modelResult?.lifecycle === 'cancelled' || modelResult?.outcome === 'cancelled') return t('capability.cancelled')
  switch (status) {
    case 'idle': return t('capability.notStarted')
    case 'queued': return t('capability.modelQueued')
    case 'running': return t('capability.modelRunning')
    case 'success': return t('capability.modelSuccess')
    case 'failed': return t('capability.modelFailed')
    case 'skipped': return t('capability.modelSkipped')
    default: return status
  }
}

const isModelRetryable = (modelResult: CapabilityModelJobResult): boolean => {
  const displayState = getModelDisplayState(modelResult)
  return displayState === 'failed' || displayState === 'cancelled' || displayState === 'skipped' || displayState === 'idle'
}

const isModelPending = (modelResult: CapabilityModelJobResult): boolean => {
  const displayState = getModelDisplayState(modelResult)
  return displayState === 'idle' || displayState === 'pending' || displayState === 'running'
}

const getTooltipClass = (modelResult: CapabilityModelJobResult): string => {
  if (modelResult.outcome === 'success') return 'success-tooltip'
  if (isModelPending(modelResult)) return 'pending-tooltip'
  return 'error-tooltip'
}

const getModelTooltipError = (modelResult: CapabilityModelJobResult): string => {
  if (modelResult.reason === 'not_run') return t('capability.reasonNotRun')
  if (modelResult.reason === 'cancelled') return t('capability.reasonCancelled')
  if (modelResult.error === 'timeout') return t('capability.reasonTimeout')
  return modelResult.error || t('capability.failedTooltip')
}

const formatLatency = (latency: number): string => latency >= 0 ? `${latency}ms` : '-'

const formatStreaming = (modelResult: CapabilityModelJobResult): string => {
  if (!modelResult.success) return '-'
  return modelResult.streamingSupported ? t('capability.supported') : t('capability.unsupported')
}

const formatCodexImageGeneration = (modelResult: CapabilityModelJobResult): string => {
  const probe = modelResult.codexImageGeneration
  if (!probe) return '-'
  const total = probe.supportedKeys + probe.unsupportedKeys + probe.inconclusiveKeys
  const summary = t('capability.codexImageGenerationSummary', {
    supported: probe.supportedKeys,
    total,
  })
  return probe.compatibleViaStrip
    ? `${summary} · ${t('capability.codexImageGenerationStripped')}`
    : summary
}

const isModelSuccessful = (modelResult: CapabilityModelJobResult): boolean => getModelDisplayState(modelResult) === 'success'
const getModelTooltipView = (modelResult: CapabilityModelJobResult): 'success' | 'pending' | 'failed' => {
  if (isModelSuccessful(modelResult)) return 'success'
  if (getModelDisplayState(modelResult) === 'idle') return 'failed'
  if (isModelPending(modelResult)) return 'pending'
  return 'failed'
}
const getModelStatusText = (modelResult: CapabilityModelJobResult): string => getModelStatusLabel(modelResult.status, modelResult)
const canRetryModel = (modelResult: CapabilityModelJobResult): boolean => Boolean(props.retryEnabled) && isModelRetryable(modelResult)
const getModelBadgeClasses = (modelResult: CapabilityModelJobResult): string[] => ['model-result-badge', getModelBadgeClass(modelResult), canRetryModel(modelResult) ? 'retryable-badge' : '']
const getModelTooltipClasses = (modelResult: CapabilityModelJobResult): string => getTooltipClass(modelResult)
const getModelRetryHintVisible = (modelResult: CapabilityModelJobResult): boolean => canRetryModel(modelResult)
const shouldRetryModel = (modelResult: CapabilityModelJobResult): boolean => canRetryModel(modelResult)
const getModelTooltipLatencyText = (modelResult: CapabilityModelJobResult): string => formatLatency(modelResult.latency)

const isRedirectedModel = (modelResult: CapabilityModelJobResult): boolean => {
  return Boolean(modelResult.actualModel && modelResult.actualModel !== modelResult.model)
}

// isUpstreamModelRedirected 上游自报模型与预期（CCX 映射后模型或请求模型）不一致——厂商侧隐式重定向
const isUpstreamModelRedirected = (modelResult: CapabilityModelJobResult): boolean => {
  if (!modelResult.upstreamModel) return false
  const expected = modelResult.actualModel || modelResult.model
  return modelResult.upstreamModel !== expected
}

</script>

<style scoped>
.models-label {
  font-size: 0.75rem;
  font-weight: 600;
  color: rgba(107, 114, 128, 0.9);
  text-transform: uppercase;
  letter-spacing: 0.08em;
  margin-bottom: 8px;
}

.model-results-wrapper {
  width: 100%;
  box-sizing: border-box;
  padding: 10px 16px;
}

@media (max-width: 720px) {
  .model-results-wrapper {
    padding: 10px 12px;
  }
}

.model-results-flow {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.model-result-badge {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 6px 10px;
  border-radius: 999px;
  font-size: 0.75rem;
  font-weight: 600;
  line-height: 1;
  border: 1px solid transparent;
  cursor: default;
}

.model-name {
  max-width: 220px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.queued-badge {
  background: rgba(107, 114, 128, 0.12);
  color: rgba(75, 85, 99, 0.95);
  border-color: rgba(107, 114, 128, 0.2);
}

.running-badge {
  background: rgba(59, 130, 246, 0.12);
  color: rgba(30, 64, 175, 0.95);
  border-color: rgba(59, 130, 246, 0.24);
}

.success-badge {
  background: rgba(34, 197, 94, 0.12);
  color: rgba(21, 128, 61, 0.95);
  border-color: rgba(34, 197, 94, 0.24);
}

.error-badge {
  background: rgba(239, 68, 68, 0.12);
  color: rgba(185, 28, 28, 0.95);
  border-color: rgba(239, 68, 68, 0.24);
}

.skipped-badge {
  background: rgba(148, 163, 184, 0.14);
  color: rgba(71, 85, 105, 0.95);
  border-color: rgba(148, 163, 184, 0.25);
}

/* 暗色模式提升模型徽标可读性，避免文字过暗 */
:global(.v-theme--dark) .queued-badge {
  color: rgba(226, 232, 240, 0.92);
}

:global(.v-theme--dark) .running-badge {
  color: rgba(191, 219, 254, 0.96);
}

:global(.v-theme--dark) .success-badge {
  color: rgba(134, 239, 172, 0.96);
}

:global(.v-theme--dark) .error-badge {
  color: rgba(252, 165, 165, 0.96);
}

:global(.v-theme--dark) .skipped-badge {
  color: rgba(203, 213, 225, 0.9);
}

.retryable-badge {
  cursor: pointer;
}

.retryable-badge:hover {
  box-shadow: 0 0 0 1px rgba(59, 130, 246, 0.28);
}

.tooltip-content {
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.tooltip-title {
  font-size: 0.82rem;
  font-weight: 700;
}

.tooltip-row {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  font-size: 0.75rem;
}

.tooltip-label {
  opacity: 0.72;
}

.tooltip-value {
  font-weight: 600;
}

.tooltip-error {
  font-size: 0.75rem;
  line-height: 1.45;
}

.tooltip-retry {
  font-size: 0.75rem;
  font-weight: 700;
}

.redirect-icon {
  opacity: 0.7;
  margin-left: 2px;
  margin-right: -2px;
}

.upstream-redirect-icon {
  opacity: 0.9;
  color: #d97706;
  margin-left: 2px;
  margin-right: -2px;
}

.mapping-action-icon {
  opacity: 0.55;
  cursor: pointer;
  margin-left: 2px;
}

.mapping-action-icon:hover {
  opacity: 1;
}

.tooltip-value-warning {
  color: #d97706;
}
</style>
