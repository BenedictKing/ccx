<template>
  <div v-if="modelValue.length" class="mt-4">
    <div class="text-subtitle-2 mb-2">{{ t('channelEditor.modelLimits.title') }}</div>
    <p class="text-caption text-medium-emphasis mb-3">{{ t('channelEditor.modelLimits.hint') }}</p>
    <v-textarea
      v-for="route in modelValue"
      :key="route.channelUid || `${route.kind}:${route.index}`"
      :data-route-uid="route.channelUid"
      :model-value="texts[routeKey(route)] ?? ''"
      :label="`${t(`channelEditor.protocolModels.${route.kind}`)} · ${route.name}`"
      :hint="t('channelEditor.modelLimits.inputHint')"
      :rules="[validatePatterns]"
      :disabled="disabled || !route.channelUid"
      variant="outlined"
      density="compact"
      rows="2"
      auto-grow
      persistent-hint
      class="mb-3"
      @update:model-value="updateModels(route, $event ?? '')"
    />
  </div>
</template>

<script setup lang="ts">
import { reactive, watch } from 'vue'
import type { ChannelProtocolRoute } from '../../services/api'
import { useI18n } from '../../i18n'
import { filterValidSupportedModelPatterns, parseSupportedModelInput } from '../../utils/add-channel-modal-state'

const props = defineProps<{
  modelValue: ChannelProtocolRoute[]
  disabled?: boolean
}>()
const emit = defineEmits<{
  'update:modelValue': [routes: ChannelProtocolRoute[]]
}>()
const { t } = useI18n()
const texts = reactive<Record<string, string>>({})
const routeKey = (route: ChannelProtocolRoute) => route.channelUid || `${route.kind}:${route.index}`
watch(() => props.modelValue, routes => {
  for (const route of routes) {
    const key = routeKey(route)
    const models = route.supportedModels ?? []
    if (texts[key] === undefined || JSON.stringify([...new Set(parseSupportedModelInput(texts[key]))]) !== JSON.stringify(models)) {
      texts[key] = models.join('\n')
    }
  }
}, { immediate: true, deep: true })

const validatePatterns = (value: string) => (
  !filterValidSupportedModelPatterns([value || '']).hasInvalidPatterns || t('channelEditor.modelLimits.invalid')
)

const updateModels = (route: ChannelProtocolRoute, value: string) => {
  texts[routeKey(route)] = value
  emit('update:modelValue', props.modelValue.map(item => (
    item === route ? { ...item, supportedModels: [...new Set(parseSupportedModelInput(value))] } : item
  )))
}
</script>
