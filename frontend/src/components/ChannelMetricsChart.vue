<template>
  <div class="channel-chart-container">
    <!-- Snackbar for error notification -->
    <v-snackbar v-model="showError" color="error" :timeout="3000" location="top">
      {{ errorMessage }}
      <template #actions>
        <v-btn variant="text" @click="showError = false">{{ t('chart.close') }}</v-btn>
      </template>
    </v-snackbar>

    <!-- Duration selector -->
    <div class="chart-header d-flex align-center justify-space-between mb-3">
      <div class="d-flex align-center ga-2">
        <v-btn-toggle v-model="selectedDuration" mandatory density="compact" variant="outlined" divided :disabled="isLoading">
          <v-btn value="1h" size="x-small">{{ t('chart.1h') }}</v-btn>
          <v-btn value="6h" size="x-small">{{ t('chart.6h') }}</v-btn>
          <v-btn value="24h" size="x-small">{{ t('chart.24h') }}</v-btn>
          <v-btn value="7d" size="x-small">{{ t('chart.7d') }}</v-btn>
          <v-btn value="30d" size="x-small">{{ t('chart.30d') }}</v-btn>
          <v-btn value="90d" size="x-small">{{ t('chart.90d') }}</v-btn>
          <v-btn value="180d" size="x-small">{{ t('chart.180d') }}</v-btn>
          <v-btn value="365d" size="x-small">{{ t('chart.365d') }}</v-btn>
          <v-btn value="thisyear" size="x-small">{{ t('chart.thisyear') }}</v-btn>
        </v-btn-toggle>
        <v-btn icon size="x-small" variant="text" :loading="isLoading" :disabled="isLoading" @click="refreshData">
          <v-icon size="small">mdi-refresh</v-icon>
        </v-btn>
      </div>
      <v-btn icon size="x-small" variant="text" :title="t('chart.collapse')" @click="$emit('close')">
        <v-icon size="small">mdi-chevron-up</v-icon>
      </v-btn>
    </div>

    <!-- Loading state -->
    <div v-if="isLoading" class="d-flex justify-center align-center" style="height: 150px">
      <v-progress-circular indeterminate size="24" color="primary" />
    </div>

    <!-- Empty state -->
    <div v-else-if="!hasData" class="d-flex flex-column justify-center align-center text-medium-emphasis" style="height: 150px">
      <v-icon size="32" color="grey-lighten-1">mdi-chart-line-variant</v-icon>
      <div class="text-caption mt-1">{{ t('chart.noRequestsInRange') }}</div>
    </div>

    <!-- Charts -->
    <div v-else class="charts-wrapper">
      <div class="chart-row">
        <!-- Request count chart -->
        <div class="chart-item">
          <div class="text-caption text-medium-emphasis mb-1">{{ t('chart.totalRequests') }}</div>
          <apexchart
            type="area"
            height="120"
            :options="requestCountOptions"
            :series="requestCountSeries"
          />
        </div>

        <!-- Availability chart -->
        <div class="chart-item">
          <div class="text-caption text-medium-emphasis mb-1">{{ t('chart.successRate') }}</div>
          <apexchart
            type="line"
            height="120"
            :options="successRateOptions"
            :series="successRateSeries"
          />
        </div>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue'
import { useTheme } from 'vuetify'
import VueApexCharts from 'vue3-apexcharts'
import type { ApexOptions } from 'apexcharts'
import { api, type MetricsHistoryResponse } from '../services/api'
import { useI18n } from '../i18n'

// Register apexchart component
const apexchart = VueApexCharts

const props = defineProps<{
  channelType: 'messages' | 'responses'
  channelIndex: number  // Single-channel mode: specified channel index
  channelName: string   // Channel name (used for the legend)
}>()

const _emit = defineEmits<{
  (_e: 'close'): void
}>()

const theme = useTheme()
const { t } = useI18n()

// State
const selectedDuration = ref<'1h' | '6h' | '24h' | '7d' | '30d' | '90d' | '180d' | '365d' | 'thisyear'>('6h')
const isLoading = ref(false)
const historyData = ref<MetricsHistoryResponse | null>(null)
const showError = ref(false)
const errorMessage = ref('')

// Computed: check if has data
const hasData = computed(() => {
  if (!historyData.value) return false
  return historyData.value.dataPoints &&
    historyData.value.dataPoints.length > 0 &&
    historyData.value.dataPoints.some(dp => dp.requestCount > 0)
})

// Computed: is dark mode
const isDark = computed(() => theme.global.current.value.dark)

// Chart color - single channel uses primary color
const chartColor = '#2196F3'

// Common chart options
const baseChartOptions = computed<ApexOptions>(() => ({
  chart: {
    toolbar: { show: false },
    zoom: { enabled: false },
    background: 'transparent',
    fontFamily: 'inherit',
    defaultLocale: 'en',
    sparkline: { enabled: false }
  },
  theme: {
    mode: isDark.value ? 'dark' : 'light'
  },
  grid: {
    borderColor: isDark.value ? 'rgba(255,255,255,0.1)' : 'rgba(0,0,0,0.1)',
    strokeDashArray: 3,
    padding: { left: 10, right: 10 }
  },
  xaxis: {
    type: 'datetime',
    labels: {
      datetimeUTC: false,
      format: ['7d', '30d', '90d', '180d', '365d', 'thisyear'].includes(selectedDuration.value) ? 'MM-dd HH:mm' : 'HH:mm',
      style: { fontSize: '10px' }
    },
    axisBorder: { show: false },
    axisTicks: { show: false }
  },
  yaxis: {
    labels: {
      style: { fontSize: '10px' }
    }
  },
  tooltip: {
    x: {
      format: 'MM-dd HH:mm'
    }
  },
  legend: {
    show: false
  },
  stroke: {
    curve: 'smooth' as const,
    width: 2
  }
}))

// Request count chart options
const requestCountOptions = computed<ApexOptions>(() => ({
  ...baseChartOptions.value,
  colors: [chartColor],
  fill: {
    type: 'gradient' as const,
    gradient: {
      shadeIntensity: 1,
      opacityFrom: 0.4,
      opacityTo: 0.1,
      stops: [0, 90, 100]
    }
  },
  yaxis: {
    min: 0,
    labels: {
      formatter: (val: number) => Math.round(val).toString(),
      style: { fontSize: '10px' }
    }
  },
  dataLabels: {
    enabled: false
  }
}))

// Availability chart options
const successRateOptions = computed<ApexOptions>(() => ({
  ...baseChartOptions.value,
  colors: ['#4CAF50'],
  yaxis: {
    min: 0,
    max: 100,
    labels: {
      formatter: (val: number) => `${val.toFixed(0)}%`,
      style: { fontSize: '10px' }
    }
  },
  dataLabels: {
    enabled: false
  },
  markers: {
    size: 2,
    hover: {
      size: 4
    }
  }
}))

// Request count series data
const requestCountSeries = computed(() => {
  if (!historyData.value || !historyData.value.dataPoints) return []
  return [{
    name: t('status.metrics.requests'),
    data: historyData.value.dataPoints.map(dp => ({
      x: new Date(dp.timestamp).getTime(),
      y: dp.requestCount
    }))
  }]
})

// Availability series data
const successRateSeries = computed(() => {
  if (!historyData.value || !historyData.value.dataPoints) return []
  return [{
    name: t('chart.successRate'),
    data: historyData.value.dataPoints
      .filter(dp => dp.requestCount > 0)
      .map(dp => ({
        x: new Date(dp.timestamp).getTime(),
        y: dp.successRate
      }))
  }]
})

// Fetch data for single channel
const refreshData = async () => {
  isLoading.value = true
  errorMessage.value = ''
  try {
    let allData: MetricsHistoryResponse[]
    if (props.channelType === 'messages') {
      allData = await api.getChannelMetricsHistory(selectedDuration.value)
    } else {
      allData = await api.getResponsesChannelMetricsHistory(selectedDuration.value)
    }
    // Find the matching channel data
    historyData.value = allData.find(ch => ch.channelIndex === props.channelIndex) || null
  } catch (error) {
    console.error('Failed to fetch metrics history:', error)
    errorMessage.value = error instanceof Error ? error.message : t('chart.historyLoadFailed')
    showError.value = true
    historyData.value = null
  } finally {
    isLoading.value = false
  }
}

// Watch duration change
watch(selectedDuration, () => {
  refreshData()
})

// Watch channel change
watch(() => props.channelIndex, () => {
  refreshData()
})

// Watch channelType change
watch(() => props.channelType, () => {
  refreshData()
})

// Initial load
onMounted(() => {
  refreshData()
})

// Expose refresh method
defineExpose({
  refreshData
})
</script>

<style scoped>
.channel-chart-container {
  padding: 12px 16px;
  background: rgba(var(--v-theme-primary), 0.03);
  border-top: 1px dashed rgba(var(--v-theme-on-surface), 0.2);
}

.v-theme--dark .channel-chart-container {
  background: rgba(var(--v-theme-primary), 0.05);
  border-top-color: rgba(255, 255, 255, 0.15);
}

.charts-wrapper {
  margin-top: 8px;
}

.chart-row {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 16px;
}

.chart-item {
  min-width: 0;
}

@media (max-width: 800px) {
  .chart-row {
    grid-template-columns: 1fr;
    gap: 12px;
  }
}
</style>
