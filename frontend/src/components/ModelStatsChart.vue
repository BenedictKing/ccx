<template>
  <div class="model-stats-chart-container">
    <v-snackbar v-model="showError" color="error" :timeout="3000" location="top">
      {{ errorMessage }}
      <template #actions>
        <v-btn variant="text" @click="showError = false">{{ t('chart.close') }}</v-btn>
      </template>
    </v-snackbar>

    <!-- Header -->
    <div class="chart-header d-flex align-center justify-space-between mb-3 flex-wrap ga-2">
      <div class="d-flex align-center ga-2">
        <v-btn-toggle v-model="selectedDuration" mandatory density="compact" variant="outlined" divided :disabled="isLoading">
          <v-btn value="1h" size="x-small">{{ t('chart.1h') }}</v-btn>
          <v-btn value="6h" size="x-small">{{ t('chart.6h') }}</v-btn>
          <v-btn value="24h" size="x-small">{{ t('chart.24h') }}</v-btn>
          <v-btn value="today" size="x-small">{{ t('chart.today') }}</v-btn>
        </v-btn-toggle>
        <v-btn icon size="x-small" variant="text" :loading="isLoading" :disabled="isLoading" @click="refreshData()">
          <v-icon size="small">mdi-refresh</v-icon>
        </v-btn>
      </div>
      <v-btn-toggle v-model="selectedView" mandatory density="compact" variant="outlined" divided :disabled="isLoading">
        <v-btn value="requests" size="x-small">
          <v-icon size="small" class="mr-1">mdi-chart-line</v-icon>
          {{ t('chart.traffic') }}
        </v-btn>
        <v-btn value="tokens" size="x-small">
          <v-icon size="small" class="mr-1">mdi-chart-areaspline</v-icon>
          Token
        </v-btn>
      </v-btn-toggle>
    </div>

    <!-- Compact summary -->
    <div v-if="topModels.length" class="compact-summary d-flex align-center ga-3 mb-2 text-caption flex-wrap">
      <span v-for="(m, i) in topModels" :key="m.name">
        <span :style="{ color: modelColors[i % modelColors.length] }">●</span>
        <strong>{{ m.name }}</strong> {{ formatNumber(m.count) }} {{ t('chart.requestUnit') }}
      </span>
    </div>

    <!-- Loading -->
    <div v-if="isLoading" class="d-flex justify-center align-center" style="height: 200px">
      <v-progress-circular indeterminate size="32" color="primary" />
    </div>

    <!-- Empty -->
    <div v-else-if="!hasData" class="d-flex flex-column justify-center align-center text-medium-emphasis" style="height: 200px">
      <v-icon size="40" color="grey-lighten-1">mdi-chart-timeline-variant</v-icon>
      <div class="text-caption mt-2">{{ t('chart.noModelRequestsInRange') }}</div>
    </div>

    <!-- Chart -->
    <div v-else>
      <apexchart type="area" :height="200" :options="chartOptions" :series="chartSeries" />
    </div>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import { useTheme } from 'vuetify'
import VueApexCharts from 'vue3-apexcharts'
import type { ApexOptions } from 'apexcharts'
import { api, type ModelStatsHistoryResponse } from '../services/api'
import { useI18n } from '../i18n'

const apexchart = VueApexCharts

const props = defineProps<{
  apiType: 'messages' | 'chat' | 'responses' | 'gemini' | 'images'
}>()

type Duration = '1h' | '6h' | '24h' | 'today'
type ViewMode = 'requests' | 'tokens'

const theme = useTheme()
const isDark = computed(() => theme.global.current.value.dark)
const { t } = useI18n()

// Persisted preferences
const storageKey = (key: string) => `modelStats:${props.apiType}:${key}`
const loadPref = (apiType: string) => ({
  duration: (localStorage.getItem(`modelStats:${apiType}:duration`) as Duration) || '6h',
  view: (localStorage.getItem(`modelStats:${apiType}:view`) as ViewMode) || 'requests'
})

const saved = loadPref(props.apiType)
const selectedDuration = ref<Duration>(saved.duration)
const selectedView = ref<ViewMode>(saved.view)
const isLoading = ref(false)
const historyData = ref<ModelStatsHistoryResponse | null>(null)
const showError = ref(false)
const errorMessage = ref('')

// Model color palette
const modelColors = [
  '#3b82f6', '#10b981', '#f97316', '#8b5cf6', '#ef4444',
  '#06b6d4', '#ec4899', '#84cc16', '#f59e0b', '#6366f1'
]

const formatNumber = (num: number): string => {
  if (num >= 1000000) return (num / 1000000).toFixed(1) + 'M'
  if (num >= 1000) return (num / 1000).toFixed(1) + 'K'
  return num.toFixed(0)
}

// Model list sorted by request count
const sortedModels = computed(() => {
  if (!historyData.value?.models) return []
  return Object.entries(historyData.value.models)
    .map(([name, points]) => ({
      name,
      points,
      totalRequests: points.reduce((s, p) => s + p.requestCount, 0),
      totalTokens: points.reduce((s, p) => s + p.inputTokens + p.outputTokens, 0)
    }))
    .sort((a, b) => b.totalRequests - a.totalRequests)
})

const topModels = computed(() =>
  sortedModels.value.slice(0, 5).map(m => ({ name: m.name, count: m.totalRequests }))
)

const hasData = computed(() =>
  sortedModels.value.some(m => m.totalRequests > 0)
)

const chartSeries = computed(() => {
  return sortedModels.value.map(m => ({
    name: m.name,
    data: m.points.map(p => ({
      x: new Date(p.timestamp).getTime(),
      y: selectedView.value === 'requests' ? p.requestCount : p.inputTokens + p.outputTokens
    }))
  }))
})

const chartOptions = computed<ApexOptions>(() => ({
  chart: {
    toolbar: { show: false },
    zoom: { enabled: false },
    background: 'transparent',
    fontFamily: 'inherit',
    animations: { enabled: true, speed: 400 }
  },
  theme: { mode: isDark.value ? 'dark' : 'light' },
  colors: modelColors.slice(0, sortedModels.value.length),
  fill: {
    type: 'gradient',
    gradient: { shadeIntensity: 1, opacityFrom: 0.3, opacityTo: 0.05, stops: [0, 90, 100] }
  },
  dataLabels: { enabled: false },
  stroke: { curve: 'smooth', width: 2 },
  grid: {
    borderColor: isDark.value ? 'rgba(255,255,255,0.1)' : 'rgba(0,0,0,0.1)',
    padding: { left: 10, right: 10 }
  },
  xaxis: {
    type: 'datetime',
    labels: { datetimeUTC: false, format: 'HH:mm', style: { fontSize: '10px' } },
    axisBorder: { show: false },
    axisTicks: { show: false }
  },
  yaxis: {
    labels: {
      formatter: (val: number) => selectedView.value === 'requests' ? Math.round(val).toString() : formatNumber(val),
      style: { fontSize: '11px' }
    },
    min: 0
  },
  tooltip: {
    x: { format: 'MM-dd HH:mm' },
    y: {
      formatter: (val: number) => selectedView.value === 'requests'
        ? `${Math.round(val)} ${t('chart.requestUnit')}`
        : formatNumber(val)
    }
  },
  legend: {
    show: true,
    position: 'top',
    horizontalAlign: 'right',
    fontSize: '11px',
    markers: { size: 4 }
  }
}))

let requestVersion = 0
const refreshData = async (silent = false) => {
  if (!silent) stopAutoRefresh()
  const currentVersion = ++requestVersion
  if (!silent) isLoading.value = true
  try {
    const data = await api.getModelStatsHistory(props.apiType, selectedDuration.value)
    if (currentVersion === requestVersion) {
      historyData.value = data
    }
  } catch (e) {
    if (currentVersion === requestVersion && !silent) {
      console.error('Failed to fetch model stats:', e)
      errorMessage.value = e instanceof Error ? e.message : t('chart.modelStatsLoadFailed')
      showError.value = true
      historyData.value = null
    }
  } finally {
    if (currentVersion === requestVersion && !silent) {
      isLoading.value = false
      startAutoRefresh()
    }
  }
}

// Auto refresh
let timer: ReturnType<typeof setInterval> | null = null
const startAutoRefresh = () => {
  stopAutoRefresh()
  timer = setInterval(() => refreshData(true), 5000) // Poll every 5s to reduce lock contention
}
const stopAutoRefresh = () => { if (timer) { clearInterval(timer); timer = null } }

watch(selectedDuration, (v) => { localStorage.setItem(storageKey('duration'), v); refreshData() })
watch(selectedView, (v) => { localStorage.setItem(storageKey('view'), v) })
watch(() => props.apiType, (t) => {
  const p = loadPref(t)
  selectedDuration.value = p.duration as Duration
  selectedView.value = p.view as ViewMode
  refreshData()
})

onMounted(() => { refreshData() })
onUnmounted(() => { stopAutoRefresh() })
</script>

<style scoped>
.model-stats-chart-container {
  padding: 12px 16px;
}
.compact-summary {
  padding: 4px 8px;
  background: rgba(var(--v-theme-surface-variant), 0.2);
  border-radius: 4px;
}
</style>
