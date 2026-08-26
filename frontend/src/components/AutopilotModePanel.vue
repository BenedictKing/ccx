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

        <!-- 场景模式选择 -->
        <div class="mb-4">
          <div class="text-caption text-medium-emphasis mb-2">
            {{ t('autopilot.modePanel.scenario') }}
          </div>
          <v-select
            v-model="localConfig.scenario"
            :items="scenarioItems"
            item-title="label"
            item-value="value"
            variant="outlined"
            density="compact"
            hide-details
            :disabled="localConfig.killSwitchActive"
            style="max-width: 300px;"
          />
          <div class="text-caption text-medium-emphasis mt-1">
            {{ t(`autopilot.scenarioDesc.${localConfig.scenario ?? 'auto'}`) }}
          </div>
          <!-- 非 auto 场景显示预设参数摘要 -->
          <div
            v-if="localConfig.scenario && localConfig.scenario !== 'auto'"
            class="text-caption text-medium-emphasis mt-2 scenario-summary"
          >
            <v-icon size="14" class="mr-1">mdi-tune-variant</v-icon>
            {{ scenarioSummary }}
          </div>
          <div v-else class="text-caption text-medium-emphasis mt-2">
            {{ t('autopilot.modePanel.scenarioHeaderHint') }}
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
            :disabled="localConfig.killSwitchActive || scenarioActive"
            style="max-width: 300px;"
          />
          <div class="text-caption text-medium-emphasis mt-1">
            {{ scenarioActive
              ? t('autopilot.modePanel.costPreferenceFollowsScenario')
              : t(`autopilot.costPreferenceDesc.${localConfig.costPreference}`) }}
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
  </div>
</template>

<script setup lang="ts">
import { computed, reactive, watch } from 'vue'
import { useI18n } from '@/i18n'
import type { RoutingScenario, ScenarioPresetView, SmartRoutingConfig } from '@/services/api-types'

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
  localConfig.killSwitchActive = newCfg.killSwitchActive
  localConfig.costPreference = newCfg.costPreference
  localConfig.scenario = newCfg.scenario ?? 'auto'
  localConfig.scenarioPresets = newCfg.scenarioPresets
  localConfig.l2ProbeEnabled = newCfg.l2ProbeEnabled
}, { deep: true })

// 场景模式选项
const scenarioItems = computed(() => ([
  { value: 'auto', label: t('autopilot.scenario.auto') },
  { value: 'daily_dev', label: t('autopilot.scenario.daily_dev') },
  { value: 'hard_problem', label: t('autopilot.scenario.hard_problem') },
  { value: 'background', label: t('autopilot.scenario.background') },
  { value: 'batch_cheap', label: t('autopilot.scenario.batch_cheap') },
] as { value: RoutingScenario; label: string }[]))

// 价格偏好选项
const costPreferenceItems = computed(() => [
  { value: 'quality_first', label: t('autopilot.costPreference.quality_first') },
  { value: 'balanced', label: t('autopilot.costPreference.balanced') },
  { value: 'cost_first', label: t('autopilot.costPreference.cost_first') },
])

// 当前是否命中具体场景（非 auto）
const scenarioActive = computed(() => {
  const s = localConfig.scenario ?? 'auto'
  return s !== 'auto'
})

// 当前命中场景的预设参数（来自后端 scenarioPresets）
const activePreset = computed<ScenarioPresetView | undefined>(() => {
  const key = localConfig.scenario
  if (!key || key === 'auto') return undefined
  return (localConfig.scenarioPresets ?? []).find(p => p.key === key)
})

// 场景参数摘要：质量下限 · 价格偏好 · effort 区间
const scenarioSummary = computed(() => {
  const preset = activePreset.value
  if (!preset) return ''
  const parts: string[] = [
    t(`autopilot.qualityTier.${preset.minQualityTier}`),
    t(`autopilot.costPreference.${preset.costPreference}`),
  ]
  if (preset.effortFloor || preset.effortCeil) {
    const floor = preset.effortFloor ?? 'low'
    const ceil = preset.effortCeil ?? 'ultra'
    parts.push(t('autopilot.effortRange', { floor, ceil }))
  }
  return parts.join(' · ')
})

// 检测是否有变更
const hasChanges = computed(() => {
  return localConfig.costPreference !== props.config.costPreference
    || (localConfig.scenario ?? 'auto') !== (props.config.scenario ?? 'auto')
})

// 保存配置
function saveConfig() {
  emit('update:config', cloneConfig(localConfig))
}

// 重置为父组件传入的值
function resetConfig() {
  localConfig.killSwitchActive = props.config.killSwitchActive
  localConfig.costPreference = props.config.costPreference
  localConfig.scenario = props.config.scenario ?? 'auto'
}

// 深拷贝配置（只拷贝前端需要的字段）
function cloneConfig(src: SmartRoutingConfig): SmartRoutingConfig {
  return {
    killSwitchActive: src.killSwitchActive,
    costPreference: src.costPreference,
    scenario: src.scenario ?? 'auto',
    scenarioPresets: src.scenarioPresets ? [...src.scenarioPresets] : undefined,
    l2ProbeEnabled: src.l2ProbeEnabled,
  }
}
</script>

<style scoped>
.scenario-summary {
  display: inline-flex;
  align-items: center;
  line-height: 1.4;
}
</style>
