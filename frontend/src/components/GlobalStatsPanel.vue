<template>
  <GlobalStatsChart v-model:duration="duration" :api-type="apiType" @refreshed="refreshed" />
  <ModelRoutingPanel
    :api-type="apiType"
    :duration="duration"
    :refresh-version="refreshVersion"
    :reset-version="resetVersion"
    :history-selection="historySelection"
    @update:duration="duration = $event"
    @history-closed="emit('historyClosed')"
  />
</template>
<script setup lang="ts">
import { ref } from 'vue'
import type { ChannelKind } from '../services/api-types'
import type { RoutingHistorySelection, StatsDuration } from '../services/routing-types'
import GlobalStatsChart from './GlobalStatsChart.vue'
import ModelRoutingPanel from './ModelRoutingPanel.vue'
const props = defineProps<{ apiType: ChannelKind; historySelection?: RoutingHistorySelection }>()
const emit = defineEmits<{ historyClosed: [] }>()
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
