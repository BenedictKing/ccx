<template>
  <v-card variant="outlined" class="pa-4">
    <div class="text-caption font-weight-bold text-uppercase text-medium-emphasis mb-3">
      <v-icon size="small" color="primary" class="mr-1">mdi-cog-outline</v-icon>
      {{ t('channelEditor.runtime.title') }}
    </div>

    <!-- 余额耗尽自动拉黑 -->
    <div class="d-flex align-center justify-space-between mb-3">
      <div class="d-flex align-center ga-2">
        <v-icon color="warning">mdi-cash-remove</v-icon>
        <div>
          <div class="section-title section-title--soft">{{ t('channelEditor.runtime.autoBlacklist.label') }}</div>
          <div class="text-caption text-medium-emphasis">{{ t('channelEditor.runtime.autoBlacklist.hint') }}</div>
        </div>
      </div>
      <v-switch :model-value="form.autoBlacklistBalance" inset color="warning" hide-details @update:model-value="updateField('autoBlacklistBalance', $event)" />
    </div>

    <div class="d-flex align-center justify-space-between mb-3">
      <div class="d-flex align-center ga-2">
        <v-icon color="warning">mdi-shield-off-outline</v-icon>
        <div>
          <div class="section-title section-title--soft">{{ t('channelEditor.runtime.disableCircuitBreaker.label') }}</div>
          <div class="text-caption text-medium-emphasis">{{ t('channelEditor.runtime.disableCircuitBreaker.hint') }}</div>
        </div>
      </div>
      <v-switch :model-value="form.circuitBreakerDisabled" inset color="warning" hide-details @update:model-value="updateField('circuitBreakerDisabled', $event)" />
    </div>

    <!-- 自动学习429限速 -->
    <div class="d-flex align-center justify-space-between">
      <div class="d-flex align-center ga-2">
        <v-icon color="secondary">mdi-robot</v-icon>
        <div>
          <div class="section-title section-title--soft">{{ t('channelEditor.runtime.autoLearnRateLimits.label') }}</div>
          <div class="text-caption text-medium-emphasis">{{ t('channelEditor.runtime.autoLearnRateLimits.hint') }}</div>
        </div>
      </div>
      <v-switch :model-value="form.rateLimitAutoFromHeaders" inset color="secondary" hide-details @update:model-value="updateField('rateLimitAutoFromHeaders', $event)" />
    </div>
  </v-card>
</template>

<script setup lang="ts">
import { useI18n } from '../../i18n'

interface FormData {
  autoBlacklistBalance: boolean
  circuitBreakerDisabled: boolean
  rateLimitAutoFromHeaders: boolean
}

interface Props {
  form: FormData
}

defineProps<Props>()

const emit = defineEmits<{
  'update:field': [field: keyof FormData, value: unknown]
}>()

const { t } = useI18n()

const updateField = (field: keyof FormData, value: unknown) => {
  emit('update:field', field, value)
}
</script>

<style scoped>
.section-title--soft {
  font-weight: 500;
  font-size: 0.875rem;
}
</style>
