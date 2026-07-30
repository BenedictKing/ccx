<script setup lang="ts">
import { computed, reactive, ref, watch } from 'vue'
import { Activity, AlertOctagon, BadgeCheck, Undo2 } from 'lucide-vue-next'
import { Alert } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Progress } from '@/components/ui/progress'
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from '@/components/ui/dialog'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Switch } from '@/components/ui/switch'
import { useLanguage } from '@/composables/useLanguage'
import type { AutopilotMode, SmartRoutingConfig } from '@/services/admin-api'

const props = defineProps<{
  config: SmartRoutingConfig
  saving: boolean
}>()

const emit = defineEmits<{
  'update:config': [config: SmartRoutingConfig]
}>()

const { t } = useLanguage()

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

const localConfig = reactive<SmartRoutingConfig>(cloneConfig(props.config))

watch(
  () => props.config,
  (newCfg) => {
    localConfig.mode = newCfg.mode
    localConfig.killSwitchActive = newCfg.killSwitchActive
    localConfig.costPreference = newCfg.costPreference
    localConfig.l2ProbeEnabled = newCfg.l2ProbeEnabled
    localConfig.readiness = cloneReadiness(newCfg.readiness)
  },
  { deep: true },
)

const modeOptions: AutopilotMode[] = ['off', 'shadow', 'assist', 'auto']

const costPreferenceItems = computed(() => [
  { value: 'quality_first', label: t('autopilot.costPreference.quality_first') },
  { value: 'balanced', label: t('autopilot.costPreference.balanced') },
  { value: 'cost_first', label: t('autopilot.costPreference.cost_first') },
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
  return reasons.map((reason) => t(`autopilot.readiness.reason.${reason}`)).join(' · ')
})

const lastRollback = computed(() => localConfig.readiness?.lastRollback)

const hasChanges = computed(() => {
  return (
    localConfig.mode !== props.config.mode ||
    localConfig.costPreference !== props.config.costPreference
  )
})

const confirmDialog = ref(false)
const pendingMode = ref<AutopilotMode | ''>('')

function onModeSelect(mode: AutopilotMode) {
  if (localConfig.killSwitchActive) return
  if (mode === localConfig.mode) return
  if (mode === 'assist' || mode === 'auto') {
    pendingMode.value = mode
    confirmDialog.value = true
    return
  }
  localConfig.mode = mode
}

function confirmModeChange() {
  if (pendingMode.value) {
    localConfig.mode = pendingMode.value
  }
  pendingMode.value = ''
  confirmDialog.value = false
}

function cancelModeChange() {
  pendingMode.value = ''
  confirmDialog.value = false
}

function saveConfig() {
  emit('update:config', cloneConfig(localConfig))
}

function resetConfig() {
  localConfig.mode = props.config.mode
  localConfig.killSwitchActive = props.config.killSwitchActive
  localConfig.costPreference = props.config.costPreference
}
</script>

<template>
  <div class="rounded-xl border border-border/60 bg-card/40 p-4">
    <h4 class="mb-3 text-sm font-bold">{{ t('autopilot.modePanel.title') }}</h4>

    <Alert v-if="localConfig.killSwitchActive" variant="destructive" class="mb-4">
      <AlertOctagon class="mr-2 inline size-4" />
      <p class="inline text-sm">{{ t('autopilot.modePanel.killSwitchActive') }}</p>
    </Alert>

    <Alert
      v-if="localConfig.readiness"
      variant="default"
      :class="localConfig.readiness.ready
        ? 'mb-4 border-emerald-500/50 bg-emerald-500/10 text-emerald-700 dark:text-emerald-400'
        : 'mb-4 border-blue-500/50 bg-blue-500/10 text-blue-700 dark:text-blue-400'"
    >
      <component :is="localConfig.readiness.ready ? BadgeCheck : Activity" class="mr-2 inline size-4" />
      <div class="inline">
        <p class="text-sm font-medium">
          {{ t(localConfig.readiness.ready ? 'autopilot.readiness.ready' : 'autopilot.readiness.collecting') }}
        </p>
        <p class="mt-1 text-xs">
          {{ t('autopilot.readiness.progress', {
            samples: localConfig.readiness.safeModeMetrics.requestCount,
            requiredSamples: localConfig.readiness.requiredSamples,
            hours: localConfig.readiness.observationHours.toFixed(1),
            requiredHours: localConfig.readiness.requiredObservationHours,
          }) }}
        </p>
        <Progress :model-value="readinessProgress" class="my-2" />
        <div class="flex flex-wrap gap-2 text-xs">
          <span>{{ t('autopilot.readiness.successRate') }} {{ formatRate(localConfig.readiness.safeModeMetrics.successRate) }}</span>
          <span>{{ t('autopilot.readiness.fallbackRate') }} {{ formatRate(localConfig.readiness.safeModeMetrics.fallbackRate) }}</span>
          <span>{{ t('autopilot.readiness.failOpenRate') }} {{ formatRate(localConfig.readiness.safeModeMetrics.failOpenRate) }}</span>
          <span>p95 {{ localConfig.readiness.safeModeMetrics.p95LatencyMs || '-' }}ms</span>
        </div>
        <p v-if="!localConfig.readiness.ready" class="mt-2 text-xs">{{ readinessReasons }}</p>
      </div>
    </Alert>

    <Alert
      v-if="lastRollback"
      variant="default"
      class="mb-4 border-amber-500/50 bg-amber-500/10 text-amber-700 dark:text-amber-400"
    >
      <Undo2 class="mr-2 inline size-4" />
      <p class="inline text-sm">
        {{ t('autopilot.readiness.lastRollback', { time: formatRollbackTime(lastRollback.createdAt) }) }}
      </p>
    </Alert>

    <div class="mb-4">
      <div class="mb-2 text-xs text-muted-foreground">{{ t('autopilot.modePanel.routingMode') }}</div>
      <div class="flex flex-wrap gap-2">
        <Button
          v-for="mode in modeOptions"
          :key="mode"
          size="sm"
          :variant="localConfig.mode === mode ? 'default' : 'outline'"
          :disabled="localConfig.killSwitchActive || (mode === 'auto' && !localConfig.readiness?.ready)"
          @click="onModeSelect(mode)"
        >
          {{ t(`autopilot.mode.${mode}`) }}
        </Button>
      </div>
      <div class="mt-1 text-xs text-muted-foreground">
        {{ t(`autopilot.modeDesc.${localConfig.mode}`) }}
      </div>
    </div>

    <div class="mb-4">
      <div class="flex items-center gap-2">
        <Switch :model-value="localConfig.killSwitchActive" disabled />
        <span class="text-sm">{{ t('autopilot.modePanel.killSwitch') }}</span>
      </div>
      <div class="mt-1 text-xs text-muted-foreground">{{ t('autopilot.modePanel.killSwitchHint') }}</div>
    </div>

    <div class="mb-4">
      <div class="mb-2 text-xs text-muted-foreground">{{ t('autopilot.modePanel.costPreference') }}</div>
      <Select
        :model-value="localConfig.costPreference"
        :disabled="localConfig.killSwitchActive"
        @update:model-value="(v) => (localConfig.costPreference = v as string)"
      >
        <SelectTrigger class="h-9 w-full max-w-[280px]">
          <SelectValue />
        </SelectTrigger>
        <SelectContent>
          <SelectItem v-for="opt in costPreferenceItems" :key="opt.value" :value="opt.value">
            {{ opt.label }}
          </SelectItem>
        </SelectContent>
      </Select>
      <div class="mt-1 text-xs text-muted-foreground">
        {{ t(`autopilot.costPreferenceDesc.${localConfig.costPreference}`) }}
      </div>
    </div>

    <div class="flex gap-2">
      <Button :disabled="!hasChanges || saving" @click="saveConfig">
        {{ t('autopilot.modePanel.save') }}
      </Button>
      <Button variant="ghost" :disabled="!hasChanges || saving" @click="resetConfig">
        {{ t('autopilot.modePanel.reset') }}
      </Button>
    </div>

    <Dialog v-model:open="confirmDialog">
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{{ t('autopilot.modePanel.confirmTitle') }}</DialogTitle>
        </DialogHeader>
        <p class="text-sm text-muted-foreground">
          {{ t('autopilot.modePanel.confirmMessage', { mode: pendingMode }) }}
        </p>
        <DialogFooter>
          <Button variant="ghost" @click="cancelModeChange">{{ t('app.actions.cancel') }}</Button>
          <Button variant="destructive" @click="confirmModeChange">{{ t('app.actions.confirm') }}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  </div>
</template>
