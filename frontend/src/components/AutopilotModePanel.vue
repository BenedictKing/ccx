<template>
  <div class="autopilot-mode-panel">
    <v-card variant="outlined" rounded="lg">
      <v-card-title class="d-flex align-center text-subtitle-1 font-weight-bold pb-0">
        <v-icon size="20" class="mr-2" color="primary">mdi-steering</v-icon>
        {{ t('autopilot.modePanel.title') }}
      </v-card-title>

      <v-card-text>
        <!-- KillSwitch 警告 -->
        <v-alert
          v-if="localConfig.killSwitchActive"
          type="error"
          variant="tonal"
          density="compact"
          class="mb-4"
          icon="mdi-alert-octagon"
        >
          {{ t('autopilot.modePanel.killSwitchActive') }}
        </v-alert>

        <v-alert
          v-if="localConfig.readiness"
          :type="localConfig.readiness.recoveryPending ? 'warning' : (localConfig.readiness.ready ? 'success' : 'info')"
          variant="tonal"
          density="compact"
          class="mb-4"
          :icon="localConfig.readiness.recoveryPending ? 'mdi-shield-sync' : (localConfig.readiness.ready ? 'mdi-check-decagram' : 'mdi-chart-timeline-variant')"
        >
          <div class="font-weight-medium">
            {{ t(localConfig.readiness.recoveryPending ? 'autopilot.readiness.recovering' : (localConfig.readiness.ready ? 'autopilot.readiness.ready' : 'autopilot.readiness.collecting')) }}
          </div>
          <div class="text-caption mt-1">
            {{ t('autopilot.readiness.progress', {
              samples: localConfig.readiness.safeModeMetrics.requestCount,
              requiredSamples: localConfig.readiness.requiredSamples,
              hours: localConfig.readiness.observationHours.toFixed(1),
              requiredHours: localConfig.readiness.requiredObservationHours,
            }) }}
          </div>
          <v-progress-linear
            :model-value="readinessProgress"
            color="primary"
            height="5"
            rounded
            class="my-2"
          />
          <div class="d-flex flex-wrap ga-2 text-caption">
            <span>{{ t('autopilot.readiness.successRate') }} {{ formatRate(localConfig.readiness.safeModeMetrics.successRate) }}</span>
            <span>{{ t('autopilot.readiness.fallbackRate') }} {{ formatRate(localConfig.readiness.safeModeMetrics.fallbackRate) }}</span>
            <span>{{ t('autopilot.readiness.failOpenRate') }} {{ formatRate(localConfig.readiness.safeModeMetrics.failOpenRate) }}</span>
            <span>{{ t('autopilot.readiness.recentP95') }} {{ localConfig.readiness.recentMetrics.p95LatencyMs || '-' }}ms</span>
            <span>{{ t('autopilot.readiness.baselineP95') }} {{ localConfig.readiness.baselineMetrics.p95LatencyMs || '-' }}ms</span>
          </div>
          <div v-if="!localConfig.readiness.ready" class="text-caption mt-2">
            {{ readinessReasons }}
          </div>
        </v-alert>

        <v-alert
          v-if="lastRollback"
          :type="localConfig.readiness?.recoveryPending ? 'warning' : 'info'"
          variant="tonal"
          density="compact"
          class="mb-4"
          icon="mdi-restore-alert"
        >
          {{ t(localConfig.readiness?.recoveryPending ? 'autopilot.readiness.lastRollbackPending' : 'autopilot.readiness.lastRollback', {
            time: formatRollbackTime(lastRollback.createdAt),
          }) }}
        </v-alert>

        <!-- 路由模式选择 -->
        <div class="mb-4">
          <div class="text-caption text-medium-emphasis mb-2">
            {{ t('autopilot.modePanel.routingMode') }}
          </div>
          <v-btn-toggle
            v-model="localConfig.mode"
            mandatory
            variant="outlined"
            divided
            density="comfortable"
            :disabled="localConfig.killSwitchActive"
            @update:model-value="onModeChange"
          >
            <v-btn value="off" size="small">
              {{ t('autopilot.mode.off') }}
            </v-btn>
            <v-btn value="shadow" size="small">
              {{ t('autopilot.mode.shadow') }}
            </v-btn>
            <v-btn value="assist" size="small">
              {{ t('autopilot.mode.assist') }}
            </v-btn>
            <v-btn value="auto" size="small" :disabled="!localConfig.readiness?.ready">
              {{ t('autopilot.mode.auto') }}
            </v-btn>
          </v-btn-toggle>
          <div class="text-caption text-medium-emphasis mt-1">
            {{ t(`autopilot.modeDesc.${localConfig.mode}`) }}
          </div>
        </div>

        <!-- KillSwitch 开关（只读） -->
        <div class="mb-4">
          <v-switch
            v-model="localConfig.killSwitchActive"
            :label="t('autopilot.modePanel.killSwitch')"
            color="error"
            density="compact"
            hide-details
            disabled
          />
          <div class="text-caption text-medium-emphasis mt-1">
            {{ t('autopilot.modePanel.killSwitchHint') }}
          </div>
        </div>

        <!-- 价格偏好选择 -->
        <div class="mb-4">
          <div class="text-caption text-medium-emphasis mb-2">
            {{ t('autopilot.modePanel.costPreference') }}
          </div>
          <v-select
            v-model="localConfig.costPreference"
            :items="costPreferenceItems"
            item-title="label"
            item-value="value"
            variant="outlined"
            density="compact"
            hide-details
            :disabled="localConfig.killSwitchActive"
            style="max-width: 300px;"
          />
          <div class="text-caption text-medium-emphasis mt-1">
            {{ t(`autopilot.costPreferenceDesc.${localConfig.costPreference}`) }}
          </div>
        </div>

        <!-- 保存按钮 -->
        <div class="d-flex ga-2">
          <v-btn
            color="primary"
            variant="flat"
            :loading="saving"
            :disabled="!hasChanges"
            @click="saveConfig"
          >
            {{ t('autopilot.modePanel.save') }}
          </v-btn>
          <v-btn
            variant="text"
            :disabled="!hasChanges"
            @click="resetConfig"
          >
            {{ t('autopilot.modePanel.reset') }}
          </v-btn>
        </div>
      </v-card-text>
    </v-card>

    <!-- 确认对话框：切到 assist/auto -->
    <v-dialog v-model="confirmDialog" max-width="420">
      <v-card>
        <v-card-title class="text-subtitle-1 font-weight-bold d-flex align-center">
          <v-icon class="mr-2" color="warning">mdi-alert</v-icon>
          {{ t('autopilot.modePanel.confirmTitle') }}
        </v-card-title>
        <v-card-text>
          {{ t('autopilot.modePanel.confirmMessage', { mode: pendingMode }) }}
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" @click="cancelModeChange">
            {{ t('app.actions.cancel') }}
          </v-btn>
          <v-btn color="warning" variant="flat" @click="confirmModeChange">
            {{ t('app.actions.confirm') }}
          </v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, reactive, watch } from 'vue'
import { useI18n } from '@/i18n'
import type { SmartRoutingConfig } from '@/services/api-types'

const props = defineProps<{
  config: SmartRoutingConfig
  saving: boolean
}>()

const emit = defineEmits<{
  'update:config': [config: SmartRoutingConfig]
}>()

const { t } = useI18n()

// 本地可编辑副本（深拷贝，避免直接修改 props）
const localConfig = reactive<SmartRoutingConfig>(cloneConfig(props.config))

// 监听 props 变化（保存后父组件传入新配置时同步）
watch(() => props.config, (newCfg) => {
  localConfig.mode = newCfg.mode
  localConfig.killSwitchActive = newCfg.killSwitchActive
  localConfig.costPreference = newCfg.costPreference
  localConfig.l2ProbeEnabled = newCfg.l2ProbeEnabled
  localConfig.readiness = cloneReadiness(newCfg.readiness)
}, { deep: true })

// 确认对话框状态
const confirmDialog = ref(false)
const pendingMode = ref<string>('')
const pendingCostMode = ref<string | null>(null)

// 价格偏好选项
const costPreferenceItems = computed(() => [
  { value: 'quality_first', label: t('autopilot.costPreference.quality_first') },
  { value: 'balanced', label: t('autopilot.costPreference.balanced') },
  { value: 'cost_first', label: t('autopilot.costPreference.cost_first') },
  { value: 'custom', label: t('autopilot.costPreference.custom') },
])

const readinessProgress = computed(() => {
  const readiness = localConfig.readiness
  if (!readiness) return 0
  const sampleProgress = readiness.requiredSamples > 0
    ? readiness.safeModeMetrics.requestCount / readiness.requiredSamples
    : 0
  const timeProgress = readiness.requiredObservationHours > 0
    ? readiness.observationHours / readiness.requiredObservationHours
    : 0
  return Math.min(100, Math.max(0, Math.min(sampleProgress, timeProgress) * 100))
})

const readinessReasons = computed(() => {
  const reasons = localConfig.readiness?.blockingReasons ?? []
  return reasons.map(reason => t(`autopilot.readiness.reason.${reason}`)).join(' · ')
})

const lastRollback = computed(() => localConfig.readiness?.lastRollback)

// 检测是否有变更
const hasChanges = computed(() => {
  return (
    localConfig.mode !== props.config.mode ||
    localConfig.costPreference !== props.config.costPreference
  )
})

// 模式变更：切到 assist/auto 需确认
function onModeChange(newMode: string | null) {
  if (!newMode) return
  if (newMode === 'assist' || newMode === 'auto') {
    pendingMode.value = newMode
    pendingCostMode.value = null
    confirmDialog.value = true
  }
}

// 确认模式变更
function confirmModeChange() {
  confirmDialog.value = false
  pendingMode.value = ''
}

// 取消模式变更，回退到之前的值
function cancelModeChange() {
  localConfig.mode = props.config.mode
  pendingMode.value = ''
  confirmDialog.value = false
}

// 保存配置
function saveConfig() {
  emit('update:config', cloneConfig(localConfig))
}

// 重置为父组件传入的值
function resetConfig() {
  localConfig.mode = props.config.mode
  localConfig.killSwitchActive = props.config.killSwitchActive
  localConfig.costPreference = props.config.costPreference
}

// 深拷贝配置（只拷贝前端需要的字段）
function cloneConfig(src: SmartRoutingConfig): SmartRoutingConfig {
  return {
    mode: src.mode,
    killSwitchActive: src.killSwitchActive,
    costPreference: src.costPreference,
    l2ProbeEnabled: src.l2ProbeEnabled,
    readiness: cloneReadiness(src.readiness),
  }
}

function cloneReadiness(src: SmartRoutingConfig['readiness']): SmartRoutingConfig['readiness'] {
  return src ? JSON.parse(JSON.stringify(src)) : undefined
}

function formatRate(value: number): string {
  return `${(value * 100).toFixed(1)}%`
}

function formatRollbackTime(value: string): string {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}
</script>
