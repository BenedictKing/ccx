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
  </div>
</template>

<script setup lang="ts">
import { computed, reactive, watch } from 'vue'
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
  localConfig.killSwitchActive = newCfg.killSwitchActive
  localConfig.costPreference = newCfg.costPreference
  localConfig.l2ProbeEnabled = newCfg.l2ProbeEnabled
}, { deep: true })

// 价格偏好选项
const costPreferenceItems = computed(() => [
  { value: 'quality_first', label: t('autopilot.costPreference.quality_first') },
  { value: 'balanced', label: t('autopilot.costPreference.balanced') },
  { value: 'cost_first', label: t('autopilot.costPreference.cost_first') },
  { value: 'custom', label: t('autopilot.costPreference.custom') },
])

// 检测是否有变更
const hasChanges = computed(() => {
  return localConfig.costPreference !== props.config.costPreference
})

// 保存配置
function saveConfig() {
  emit('update:config', cloneConfig(localConfig))
}

// 重置为父组件传入的值
function resetConfig() {
  localConfig.killSwitchActive = props.config.killSwitchActive
  localConfig.costPreference = props.config.costPreference
}

// 深拷贝配置（只拷贝前端需要的字段）
function cloneConfig(src: SmartRoutingConfig): SmartRoutingConfig {
  return {
    killSwitchActive: src.killSwitchActive,
    costPreference: src.costPreference,
    l2ProbeEnabled: src.l2ProbeEnabled,
  }
}
</script>
