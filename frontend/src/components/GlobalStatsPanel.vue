<template>
  <GlobalStatsChart v-model:duration="duration" :api-type="apiType" @refreshed="refreshed" />
  <ModelRoutingPanel
    :api-type="apiType"
    :duration="duration"
    :refresh-version="refreshVersion"
    :reset-version="resetVersion"
    @update:duration="duration = $event"
  />
</template>
<script setup lang="ts">
import { ref } from 'vue'
import type { ChannelKind } from '../services/api-types'
import type { StatsDuration } from '../services/routing-types'
import GlobalStatsChart from './GlobalStatsChart.vue'
import ModelRoutingPanel from './ModelRoutingPanel.vue'
const props = defineProps<{ apiType: ChannelKind }>()
const saved = localStorage.getItem(`globalStats:${props.apiType}:duration`)
const durations: StatsDuration[] = ['1h', '6h', '24h', 'today', '7d', '30d', '90d', '180d', '365d', 'thisyear']
const duration = ref<StatsDuration>(durations.includes(saved as StatsDuration) ? (saved as StatsDuration) : '6h')
const refreshVersion = ref(0)
const resetVersion = ref(0)
function refreshed(manual: boolean) {
  if (manual) resetVersion.value++
  refreshVersion.value++
}
</script>
