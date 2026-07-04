<template>
  <div class="key-trend-chart-container">
    <!-- Snackbar for error notification -->
    <v-snackbar v-model="showError" color="error" :timeout="3000" location="top">
      {{ errorMessage }}
      <template #actions>
        <v-btn variant="text" @click="showError = false">{{ t('chart.close') }}</v-btn>
      </template>
    </v-snackbar>

    <!-- Header: duration selector (left) + view switcher (right) -->
    <div class="chart-header d-flex align-center justify-space-between mb-3">
      <div class="d-flex align-center ga-2">
        <!-- Duration selector -->
        <v-btn-toggle v-model="selectedDuration" mandatory density="compact" variant="outlined" divided :disabled="isLoading" class="chart-control-toggle">
          <v-btn value="1h" size="x-small" class="chart-control-btn">{{ t('chart.1h') }}</v-btn>
          <v-btn value="6h" size="x-small" class="chart-control-btn">{{ t('chart.6h') }}</v-btn>
          <v-btn value="24h" size="x-small" class="chart-control-btn">{{ t('chart.24h') }}</v-btn>
          <v-btn value="today" size="x-small" class="chart-control-btn">{{ t('chart.today') }}</v-btn>
          <v-btn value="7d" size="x-small" class="chart-control-btn">{{ t('chart.7d') }}</v-btn>
          <v-btn value="30d" size="x-small" class="chart-control-btn">{{ t('chart.30d') }}</v-btn>
          <v-btn value="90d" size="x-small" class="chart-control-btn">{{ t('chart.90d') }}</v-btn>
          <v-btn value="180d" size="x-small" class="chart-control-btn">{{ t('chart.180d') }}</v-btn>
          <v-btn value="365d" size="x-small" class="chart-control-btn">{{ t('chart.365d') }}</v-btn>
          <v-btn value="thisyear" size="x-small" class="chart-control-btn">{{ t('chart.thisyear') }}</v-btn>
        </v-btn-toggle>

        <v-btn icon size="x-small" variant="text" :loading="isLoading" :disabled="isLoading" @click="refreshData">
          <v-icon size="small">mdi-refresh</v-icon>
        </v-btn>
      </div>

      <!-- View switcher -->
      <v-btn-toggle v-model="selectedView" mandatory density="compact" variant="outlined" divided :disabled="isLoading" class="chart-control-toggle">
        <v-btn value="traffic" size="x-small" class="chart-control-btn">
          <v-icon size="small" class="mr-1">mdi-chart-line</v-icon>
          {{ t('chart.traffic') }}
        </v-btn>
        <v-btn value="tokens" size="x-small" class="chart-control-btn">
          <v-icon size="small" class="mr-1">mdi-chart-line</v-icon>
          Token I/O
        </v-btn>
        <v-btn value="cache" size="x-small" class="chart-control-btn">
          <v-icon size="small" class="mr-1">mdi-database</v-icon>
          {{ t('chart.cacheRw') }}
        </v-btn>
        <v-btn value="cost" size="x-small" class="chart-control-btn">
          <v-icon size="small" class="mr-1">mdi-cash</v-icon>
          {{ t('chart.cost') }}
        </v-btn>
      </v-btn-toggle>
    </div>

    <!-- Summary cards -->
    <div v-if="summaryData && !isLoading" class="summary-cards d-flex flex-wrap ga-2 mb-3">
      <div class="summary-card">
        <div class="summary-label">{{ t('chart.totalRequests') }}</div>
        <div class="summary-value">{{ formatNumber(summaryData.totalRequests) }}</div>
      </div>
      <div class="summary-card">
        <div class="summary-label">{{ t('chart.successRate') }}</div>
        <div class="summary-value" :class="{ 'text-success': summaryData.avgSuccessRate >= 95, 'text-warning': summaryData.avgSuccessRate >= 80 && summaryData.avgSuccessRate < 95, 'text-error': summaryData.avgSuccessRate < 80 }">
          {{ summaryData.avgSuccessRate.toFixed(1) }}%
        </div>
      </div>
      <div class="summary-card">
        <div class="summary-label">{{ t('chart.inputTokens') }}</div>
        <div class="summary-value">{{ formatNumber(summaryData.totalInputTokens) }}</div>
      </div>
      <div class="summary-card">
        <div class="summary-label">{{ t('chart.outputTokens') }}</div>
        <div class="summary-value">{{ formatNumber(summaryData.totalOutputTokens) }}</div>
      </div>
      <div v-if="summaryData.totalCacheReadTokens > 0 || summaryData.totalCacheCreationTokens > 0" class="summary-card">
        <div class="summary-label">{{ t('chart.cacheRw') }}</div>
        <div class="summary-value">{{ formatNumber(summaryData.totalCacheReadTokens) }} / {{ formatNumber(summaryData.totalCacheCreationTokens) }}</div>
      </div>
      <div v-if="(summaryData.totalCostUSD || 0) > 0" class="summary-card">
        <div class="summary-label">{{ t('chart.totalCost') }}</div>
        <div class="summary-value">${{ formatCost(summaryData.totalCostUSD || 0) }}</div>
      </div>
    </div>

    <!-- Loading state -->
    <div v-if="isLoading" class="d-flex justify-center align-center" style="height: 200px">
      <v-progress-circular indeterminate size="32" color="primary" />
    </div>

    <!-- Empty state -->
    <div v-else-if="!hasData" class="d-flex flex-column justify-center align-center text-medium-emphasis" style="height: 200px">
      <v-icon size="40" color="grey-lighten-1">mdi-chart-timeline-variant</v-icon>
      <div class="text-caption mt-2">{{ t('chart.noKeyUsageInRange') }}</div>
    </div>

    <!-- Chart area -->
    <div v-else class="chart-area">
      <apexchart
        ref="chartRef"
        :key="`key-trend-${selectedView}`"
        type="area"
        height="280"
        :options="chartOptions"
        :series="chartSeries"
      />
    </div>

  </div>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted } from 'vue'
import { useTheme } from 'vuetify'
import VueApexCharts from 'vue3-apexcharts'
import { api, type ChannelKeyMetricsHistoryResponse, type GlobalStatsSummary } from '../services/api'
import { useGlobalTick } from '../composables/useGlobalTick'
import { useI18n } from '../i18n'
import { effectiveChartIntervalMs, selectDenseSamplingInterval } from '../utils/chartSampling'

// Register apexchart component
const apexchart = VueApexCharts

// Props
const props = defineProps<{
  channelId: number
  channelType: 'messages' | 'chat' | 'responses' | 'gemini' | 'images' | 'vectors'
}>()
const { t } = useI18n()

// View mode type
type ViewMode = 'traffic' | 'tokens' | 'cache' | 'cost'
type Duration = '1h' | '6h' | '24h' | 'today' | '7d' | '30d' | '90d' | '180d' | '365d' | 'thisyear'

// LocalStorage keys for preferences (per channelType)
const getStorageKey = (channelType: string, key: string) => `keyTrendChart:${channelType}:${key}`

// Check if localStorage is available (SSR-safe)
const isLocalStorageAvailable = (): boolean => {
  try {
    return typeof window !== 'undefined' && window.localStorage !== undefined
  } catch {
    return false
  }
}

const loadSavedPreferences = (channelType: string): { view: ViewMode; duration: Duration } => {
  if (!isLocalStorageAvailable()) {
    return { view: 'traffic', duration: '1h' }
  }
  try {
    const savedView = window.localStorage.getItem(getStorageKey(channelType, 'viewMode')) as ViewMode | null
    const savedDuration = window.localStorage.getItem(getStorageKey(channelType, 'duration')) as Duration | null
    return {
      view: savedView && ['traffic', 'tokens', 'cache', 'cost'].includes(savedView) ? savedView : 'traffic',
      duration: savedDuration && ['1h', '6h', '24h', 'today', '7d', '30d', '90d', '180d', '365d', 'thisyear'].includes(savedDuration) ? savedDuration : '1h'
    }
  } catch {
    return { view: 'traffic', duration: '1h' }
  }
}

const savePreference = (channelType: string, key: string, value: string) => {
  if (!isLocalStorageAvailable()) return
  try {
    window.localStorage.setItem(getStorageKey(channelType, key), value)
  } catch {
    // Ignore storage errors (quota exceeded, private mode, etc.)
  }
}

// Theme
const theme = useTheme()
const isDark = computed(() => theme.global.current.value.dark)

// Load saved preferences for current channelType
const savedPrefs = loadSavedPreferences(props.channelType)

// State
const selectedView = ref<ViewMode>(savedPrefs.view)
const selectedDuration = ref<Duration>(savedPrefs.duration)
const isLoading = ref(false)
const isRefreshing = ref(false) // includes auto-refresh (silent) requests
const historyData = ref<ChannelKeyMetricsHistoryResponse | null>(null)
const samplingKey = computed(() => `${props.channelType}:${props.channelId}:${selectedDuration.value}`)
const adaptiveInterval = ref<{ key: string; interval: string } | null>(null)
const currentAdaptiveInterval = computed(() => (
  adaptiveInterval.value?.key === samplingKey.value ? adaptiveInterval.value.interval : undefined
))
const showError = ref(false)
const errorMessage = ref('')

// Chart ref for updateSeries
const chartRef = ref<InstanceType<typeof VueApexCharts> | null>(null)

// request id for refreshData
let refreshRequestId = 0

// Auto refresh (使用全局 tick，visibility hidden 时自动暂停)
const AUTO_REFRESH_INTERVAL = 5000
const autoRefreshTick = useGlobalTick(AUTO_REFRESH_INTERVAL, 'KeyTrend')
let autoRefreshActive = false

const startAutoRefresh = () => {
  autoRefreshActive = true
}

const stopAutoRefresh = () => {
  autoRefreshActive = false
}

// Key color palette - supports up to 10 keys
const keyColors = [
  '#3b82f6', // Blue
  '#f97316', // Orange
  '#10b981', // Green
  '#8b5cf6', // Purple
  '#ec4899', // Pink
  '#eab308', // Yellow
  '#06b6d4', // Cyan
  '#f43f5e', // Rose
  '#84cc16', // Lime
  '#6366f1', // Indigo
]

// Failure rate threshold: show red background when exceeded
const FAILURE_RATE_THRESHOLD = 0.1 // 10%

// Aggregation interval settings (kept consistent with the backend selectIntervalForDuration)
// Keep each default range under 200 buckets.
const AGGREGATION_INTERVALS: Record<Duration, number> = {
  '1h': 60000,          // 1 minute
  '6h': 300000,         // 5 minutes
  '24h': 900000,        // 15 minutes
  'today': 300000,       // 5 minutes fallback
  '7d': 3600000,        // 1 hour
  '30d': 14400000,      // 4 hours
  '90d': 43200000,      // 12 hours
  '180d': 86400000,     // 24 hours
  '365d': 172800000,    // 48 hours
  'thisyear': 43200000  // 12 hours fallback
}

// Get the aggregation interval based on the selected duration
const getAggregationInterval = (duration: Duration): number => {
  const interval = effectiveChartIntervalMs(summaryData.value?.intervalSeconds, currentAdaptiveInterval.value)
  if (interval) return interval
  return AGGREGATION_INTERVALS[duration] || 60000
}

// Align the timestamp to the aggregation bucket (round down)
const alignToBucket = (timestamp: number, interval: number): number => {
  return Math.floor(timestamp / interval) * interval
}

// Computed: check if has data
const hasData = computed(() => {
  if (!historyData.value?.keys?.length) return false
  const mode = selectedView.value
  return historyData.value.keys.some(keyData => {
    if (!keyData.dataPoints?.length) return false
    return keyData.dataPoints.some(dp => {
      if (mode === 'traffic') return dp.requestCount > 0
      if (mode === 'cost') return (dp.costUSD || 0) > 0
      if (mode === 'tokens') return dp.inputTokens > 0 || dp.outputTokens > 0
      return dp.cacheReadTokens > 0 || dp.cacheCreationTokens > 0
    })
  })
})

// Summary data from server response (or fallback from dataPoints)
const summaryData = computed<GlobalStatsSummary | null>(() => {
  if (!historyData.value) return null
  if (historyData.value.summary) return historyData.value.summary
  // Fallback: aggregate from displayed keys
  if (!historyData.value.keys?.length) return null
  let totalRequests = 0, totalSuccess = 0, totalFailure = 0
  let totalInputTokens = 0, totalOutputTokens = 0
  let totalCacheCreationTokens = 0, totalCacheReadTokens = 0
  for (const key of historyData.value.keys) {
    for (const p of key.dataPoints) {
      totalRequests += p.requestCount
      totalSuccess += p.successCount
      totalFailure += p.failureCount
      totalInputTokens += p.inputTokens || 0
      totalOutputTokens += p.outputTokens || 0
      totalCacheCreationTokens += p.cacheCreationTokens || 0
      totalCacheReadTokens += p.cacheReadTokens || 0
    }
  }
  return {
    totalRequests,
    totalSuccess,
    totalFailure,
    totalInputTokens,
    totalOutputTokens,
    totalCacheCreationTokens,
    totalCacheReadTokens,
    avgSuccessRate: totalRequests > 0 ? (totalSuccess / totalRequests) * 100 : 0,
    duration: selectedDuration.value
  }
})

// Computed: calculate the weighted average failure rate for each time point for background bands
// Return format: { timestamp: number, failureRate: number }[]
const timePointFailureRates = computed(() => {
  if (!historyData.value?.keys?.length) return []

  // Get the current aggregation interval to keep it aligned with the tooltip
  const interval = getAggregationInterval(selectedDuration.value)

  // Aggregate data for all keys by aligned timestamp (consistent with tooltip logic)
  const timeMap = new Map<number, { totalRequests: number; totalFailures: number }>()

  historyData.value.keys.forEach(keyData => {
    keyData.dataPoints?.forEach(dp => {
      const rawTs = new Date(dp.timestamp).getTime()
      // Align timestamps with alignToBucket to keep them consistent with tooltip data
      const alignedTs = alignToBucket(rawTs, interval)
      const existing = timeMap.get(alignedTs) || { totalRequests: 0, totalFailures: 0 }
      existing.totalRequests += dp.requestCount
      existing.totalFailures += dp.failureCount
      timeMap.set(alignedTs, existing)
    })
  })

  // Convert to an array and calculate failure rates
  return Array.from(timeMap.entries())
    .map(([timestamp, data]) => ({
      timestamp,
      failureRate: data.totalRequests > 0 ? data.totalFailures / data.totalRequests : 0
    }))
    .sort((a, b) => a.timestamp - b.timestamp)
})

// Helper: calculate opacity based on failure rate (higher failure rate = darker color)
// 10% -> 0.08, 20% -> 0.15, 30% -> 0.22, 50% -> 0.35, 70% -> 0.48, 100% -> 0.65
const getFailureOpacity = (failureRate: number): number => {
  const minOpacity = 0.08
  const maxOpacity = 0.65
  // Start at the threshold and reach maximum opacity at 100%
  const normalizedRate = Math.min((failureRate - FAILURE_RATE_THRESHOLD) / (1 - FAILURE_RATE_THRESHOLD), 1)
  return minOpacity + normalizedRate * (maxOpacity - minOpacity)
}

// Computed: generate ApexCharts annotations (red background bands with depth based on failure rate)
const failureAnnotations = computed(() => {
  if (selectedView.value !== 'traffic') return [] // Show only in traffic mode

  const rates = timePointFailureRates.value
  if (rates.length === 0) return []

  const annotations: any[] = []

  // Get the aggregation interval for the current duration (consistent with the backend)
  const DEFAULT_INTERVAL = getAggregationInterval(selectedDuration.value)
  // Maximum interval limit: 2x the default interval to avoid oversized bands for sparse data
  const MAX_INTERVAL = DEFAULT_INTERVAL * 2

  // Create a separate annotation for each point above the threshold
  rates.forEach((point, index) => {
    if (point.failureRate >= FAILURE_RATE_THRESHOLD) {
      // Dynamically determine the interval for this point, preferring the actual gap to adjacent points
      let interval = DEFAULT_INTERVAL
      if (rates.length > 1) {
        if (index > 0) {
          // Use the interval from the previous point
          interval = point.timestamp - rates[index - 1].timestamp
        } else if (index < rates.length - 1) {
          // First point: use the interval to the next point
          interval = rates[index + 1].timestamp - point.timestamp
        }
      }
      // Cap the interval to avoid oversized bands when data is sparse
      interval = Math.min(interval, MAX_INTERVAL)

      annotations.push({
        x: point.timestamp - interval / 2,
        x2: point.timestamp + interval / 2,
        fillColor: '#ef4444',
        opacity: getFailureOpacity(point.failureRate),
        borderColor: 'transparent',
        borderWidth: 0,
        label: {
          text: ''
        }
      })
    }
  })

  return annotations
})

// Computed: trim leading empty buckets for long-duration charts
// When data only exists for a few days and duration is 365d, avoid compressing
// the real data into a narrow strip by starting the x-axis at the first
// non-empty timestamp (minus one interval of context).
const firstNonEmptyTimestamp = computed(() => {
  if (!historyData.value?.keys?.length) return undefined
  let earliest = Infinity
  historyData.value.keys.forEach(keyData => {
    if (!keyData.dataPoints?.length) return
    keyData.dataPoints.forEach(dp => {
      const hasVisibleData = selectedView.value === 'traffic' || selectedView.value === 'cost'
        ? dp.requestCount > 0
        : selectedView.value === 'tokens'
          ? dp.inputTokens > 0 || dp.outputTokens > 0
          : dp.cacheReadTokens > 0 || dp.cacheCreationTokens > 0
      if (hasVisibleData) {
        const ts = new Date(dp.timestamp).getTime()
        if (ts < earliest) earliest = ts
      }
    })
  })
  return earliest === Infinity ? undefined : earliest
})

const xaxisMin = computed(() => {
  if (!['7d', '30d', '90d', '180d', '365d', 'thisyear'].includes(selectedDuration.value)) return undefined
  const ts = firstNonEmptyTimestamp.value
  if (ts === undefined) return undefined
  const interval = getAggregationInterval(selectedDuration.value)
  // Leave one interval of context before the first data point
  return ts - interval
})
const _allDataPoints = computed(() => {
  if (!historyData.value?.keys) return []
  return historyData.value.keys.flatMap(k => k.dataPoints || [])
})

// Computed: chart options
const chartOptions = computed<ApexCharts.ApexOptions>(() => {
  const mode = selectedView.value

  // Token/cache mode uses dual Y-axes (left for Input/Read, right for Output/Write)
  // All Input series share the left Y-axis, and all Output series share the right Y-axis
  let yaxisConfig: any
  if (mode === 'tokens' || mode === 'cache') {
    const inLabel = mode === 'tokens' ? 'Input' : 'Cache Read'
    const outLabel = mode === 'tokens' ? 'Output' : 'Cache Write'
    const actualSeries = chartSeriesData.value.series
    const hasOutput = chartSeriesData.value.hasOutput

    // Collect actual series names for left/right axes
    const inNames = actualSeries.filter(s => s.name.endsWith(` ${inLabel}`)).map(s => s.name)
    const outNames = actualSeries.filter(s => s.name.endsWith(` ${outLabel}`)).map(s => s.name)

    yaxisConfig = [
      // Left Y-axis (Input/Read)
      {
        seriesName: inNames[0],
        show: true,
        labels: {
          formatter: (val: number) => formatAxisValue(val, mode),
          style: { fontSize: '11px' }
        },
        min: 0,
        forceNiceScale: true
      }
    ]

    // Right Y-axis (Output/Write) — only show if output data exists
    if (hasOutput) {
      yaxisConfig.push({
        seriesName: outNames[0],
        opposite: true,
        show: true,
        labels: {
          formatter: (val: number) => formatAxisValue(val, mode),
          style: { fontSize: '11px' }
        },
        min: 0,
        forceNiceScale: true
      })
    }

    // Bind remaining Input/Read series to the left Y-axis
    for (let i = 1; i < inNames.length; i++) {
      yaxisConfig.push({
        seriesName: inNames[i],
        show: false,
        min: 0,
        forceNiceScale: true
      })
    }
    // Bind remaining Output/Write series to the right Y-axis
    for (let i = 1; i < outNames.length; i++) {
      yaxisConfig.push({
        seriesName: outNames[i],
        show: false,
        min: 0,
        forceNiceScale: true
      })
    }
  } else {
    yaxisConfig = {
      labels: {
        formatter: (val: number) => formatAxisValue(val, mode),
        style: { fontSize: '11px' }
      },
      min: 0,
      forceNiceScale: true
    }
  }

  return {
    chart: {
      toolbar: { show: false },
      zoom: { enabled: false },
      background: 'transparent',
      fontFamily: 'inherit',
      defaultLocale: 'en',
      sparkline: { enabled: false },
      stacked: mode === 'traffic' || mode === 'cost',
      animations: {
        enabled: false
      }
    },
    theme: {
      mode: isDark.value ? 'dark' : 'light'
    },
    colors: getChartColors(),
    fill: {
      type: 'gradient' as const,
      gradient: {
        shadeIntensity: 1,
        opacityFrom: 0.4,
        opacityTo: 0.08,
        stops: [0, 90, 100]
      }
    },
    dataLabels: {
      enabled: false
    },
    stroke: {
      curve: 'smooth' as const,
      width: 2,
      // Use a solid line for traffic mode; use solid for Input/Read and dashed for Output/Write in tokens/cache mode
      dashArray: getDashArray()
    },
    grid: {
      borderColor: isDark.value ? 'rgba(255,255,255,0.1)' : 'rgba(0,0,0,0.1)',
      padding: { left: 10, right: 10 }
    },
    xaxis: {
      type: 'datetime',
      min: xaxisMin.value,
      labels: {
        datetimeUTC: false,
        format: ['7d', '30d', '90d', '180d', '365d', 'thisyear'].includes(selectedDuration.value) ? 'MM-dd HH:mm' : 'HH:mm',
        style: { fontSize: '11px', colors: theme.global.current.value.dark ? '#9ca3af' : '#6b7280' }
      },
      axisBorder: { show: false },
      axisTicks: { show: false }
    },
    yaxis: yaxisConfig,
    annotations: {
      xaxis: failureAnnotations.value
    },
    tooltip: {
      x: {
        format: 'MM-dd HH:mm'
      },
      y: {
        formatter: (val: number) => formatTooltipValue(val, mode)
      },
      custom: mode === 'traffic' ? buildTrafficTooltip : undefined
    },
    legend: {
      show: true,
      position: 'top' as const,
      horizontalAlign: 'right' as const,
      fontSize: '11px',
      markers: { size: 4 }
    }
  }
})

// Build chart series from data (returns series + yaxis mapping info)
const buildChartSeries = (data: ChannelKeyMetricsHistoryResponse | null) => {
  if (!data?.keys) return { series: [] as { name: string; data: { x: number; y: number }[] }[], hasOutput: false }

  const mode = selectedView.value
  const result: { name: string; data: { x: number; y: number }[] }[] = []
  let hasOutput = false

  data.keys.forEach((keyData, _keyIndex) => {
    // Display name: show "keyMask/model" when a model exists; otherwise show only keyMask
    const displayName = keyData.model ? `${keyData.keyMask}/${keyData.model}` : keyData.keyMask

    if (mode === 'traffic' || mode === 'cost') {
      if (mode === 'cost') {
        const totalCost = keyData.dataPoints.reduce((sum, dp) => sum + (dp.costUSD || 0), 0)
        if (totalCost <= 0) return
        result.push({
          name: displayName,
          data: keyData.dataPoints.map(dp => ({
            x: new Date(dp.timestamp).getTime(),
            y: dp.costUSD || 0
          }))
        })
      } else {
        const totalRequests = keyData.dataPoints.reduce((sum, dp) => sum + dp.requestCount, 0)
        if (totalRequests === 0) return

        // One-way mode: show request count only
        result.push({
          name: displayName,
          data: keyData.dataPoints.map(dp => ({
            x: new Date(dp.timestamp).getTime(),
            y: dp.requestCount
          }))
        })
      }
    } else {
      // Bidirectional mode: create two series per key (Input/Output or Read/Write)
      const inLabel = mode === 'tokens' ? 'Input' : 'Cache Read'
      const outLabel = mode === 'tokens' ? 'Output' : 'Cache Write'
      const inTotal = keyData.dataPoints.reduce((sum, dp) => sum + (mode === 'tokens' ? dp.inputTokens : dp.cacheReadTokens), 0)
      const outTotal = keyData.dataPoints.reduce((sum, dp) => sum + (mode === 'tokens' ? dp.outputTokens : dp.cacheCreationTokens), 0)

      // Forward direction (Input/Read)
      if (inTotal > 0) {
        result.push({
          name: `${displayName} ${inLabel}`,
          data: keyData.dataPoints.map(dp => ({
            x: new Date(dp.timestamp).getTime(),
            y: mode === 'tokens' ? dp.inputTokens : dp.cacheReadTokens
          }))
        })
      }

      // Output/Write - distinguish with a dashed line
      if (outTotal > 0) {
        hasOutput = true
        result.push({
          name: `${displayName} ${outLabel}`,
          data: keyData.dataPoints.map(dp => ({
            x: new Date(dp.timestamp).getTime(),
            y: mode === 'tokens' ? dp.outputTokens : dp.cacheCreationTokens
          }))
        })
      }
    }
  })

  return { series: result, hasOutput }
}

// Computed: chart series data
const chartSeriesData = computed(() => buildChartSeries(historyData.value))
const chartSeries = computed(() => chartSeriesData.value.series)

// Helper: format number for display
const formatNumber = (num: number): string => {
  if (num >= 1000000) return (num / 1000000).toFixed(1) + 'M'
  if (num >= 1000) return (num / 1000).toFixed(1) + 'K'
  return num.toFixed(0)
}

// Helper: format cost value (USD)
const formatCost = (val: number): string => {
  if (val >= 1000) return (val / 1000).toFixed(2) + 'K'
  if (val >= 1) return val.toFixed(2)
  return val.toFixed(4)
}

// Helper: format axis value based on view mode
const formatAxisValue = (val: number, mode: ViewMode): string => {
  switch (mode) {
    case 'traffic':
      return Math.round(val).toString()
    case 'cost':
      return '$' + formatCost(Math.abs(val))
    case 'tokens':
    case 'cache':
      return formatNumber(Math.abs(val))
    default:
      return val.toString()
  }
}

// Helper: format tooltip value
const formatTooltipValue = (val: number, mode: ViewMode): string => {
  switch (mode) {
    case 'traffic':
      return `${Math.round(val)} ${t('chart.requestUnit')}`
    case 'cost':
      return `$${formatCost(Math.abs(val))}`
    case 'tokens':
    case 'cache':
      return `${formatNumber(Math.abs(val))} ${t('chart.tokenUnit')}`
    default:
      return val.toString()
  }
}

// Helper: build custom tooltip for traffic mode (shows success/failure breakdown)
const buildTrafficTooltip = ({ seriesIndex, dataPointIndex, w }: any): string => {
  if (!historyData.value?.keys) return ''

  const timestamp = w.globals.seriesX[seriesIndex][dataPointIndex]
  const date = new Date(timestamp)
  const timeStr = date.toLocaleString('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit'
  })

  // Collect data for all keys at this time point
  const keyStats: { keyMask: string; success: number; failure: number; total: number; color: string }[] = []
  let grandTotal = 0
  let grandFailure = 0

  // HTML escaping helper to prevent XSS
  const escapeHtml = (str: string): string => {
    return str
      .replace(/&/g, '&amp;')
      .replace(/</g, '&lt;')
      .replace(/>/g, '&gt;')
      .replace(/"/g, '&quot;')
      .replace(/'/g, '&#39;')
  }

  // Get the current aggregation interval for aligned timestamp matching
  const interval = getAggregationInterval(selectedDuration.value)
  const alignedTimestamp = alignToBucket(timestamp, interval)

  historyData.value.keys.forEach((keyData, keyIndex) => {
    // Use filter to accumulate all data points in the same time bucket (defensive programming)
    const matchingPoints = keyData.dataPoints?.filter(p => {
      const dpTimestamp = new Date(p.timestamp).getTime()
      return alignToBucket(dpTimestamp, interval) === alignedTimestamp
    }) || []

    if (matchingPoints.length > 0) {
      // Aggregate statistics from all matching points
      const aggregated = matchingPoints.reduce(
        (acc, dp) => ({
          success: acc.success + dp.successCount,
          failure: acc.failure + dp.failureCount,
          total: acc.total + dp.requestCount
        }),
        { success: 0, failure: 0, total: 0 }
      )

      if (aggregated.total > 0) {
        // Display name: show "keyMask/model" when a model exists
        const displayName = keyData.model ? `${keyData.keyMask}/${keyData.model}` : keyData.keyMask
        keyStats.push({
          keyMask: escapeHtml(displayName),
          success: aggregated.success,
          failure: aggregated.failure,
          total: aggregated.total,
          color: keyColors[keyIndex % keyColors.length]
        })
        grandTotal += aggregated.total
        grandFailure += aggregated.failure
      }
    }
  })

  if (keyStats.length === 0) return ''

  const grandFailureRate = grandTotal > 0 ? (grandFailure / grandTotal * 100).toFixed(1) : '0'
  const hasFailure = grandFailure > 0

  // Build HTML
  let html = `<div style="padding: 8px 12px; font-size: 13px; line-height: 1.6;">`
  html += `<div style="font-weight: 600; margin-bottom: 6px; color: ${hasFailure ? '#ef4444' : 'inherit'};">${timeStr}</div>`

  // Details for each key
  keyStats.forEach(stat => {
    const failureRate = stat.total > 0 ? (stat.failure / stat.total * 100).toFixed(0) : '0'
    const hasKeyFailure = stat.failure > 0
    html += `<div style="display: flex; align-items: center; margin: 4px 0;">`
    html += `<span style="width: 10px; height: 10px; border-radius: 50%; background: ${stat.color}; margin-right: 6px;"></span>`
    html += `<span style="flex: 1;">${stat.keyMask}</span>`
    html += `<span style="margin-left: 12px; font-weight: 500;">${stat.total} ${t('chart.requestUnit')}</span>`
    if (hasKeyFailure) {
      html += `<span style="margin-left: 6px; color: #ef4444; font-size: 12px;">(${stat.failure} ${t('chart.issueCount')}, ${failureRate}%)</span>`
    }
    html += `</div>`
  })

  // Summary row (when multiple keys exist)
  if (keyStats.length > 1) {
    html += `<div style="border-top: 1px solid rgba(128,128,128,0.3); margin-top: 6px; padding-top: 6px; font-weight: 600;">`
    html += `<span>${t('chart.total')}: ${grandTotal} ${t('chart.requestUnit')}</span>`
    if (hasFailure) {
      html += `<span style="color: #ef4444; margin-left: 8px;">${grandFailure} ${t('chart.issueCount')} (${grandFailureRate}%)</span>`
    }
    html += `</div>`
  }

  html += `</div>`
  return html
}

// Helper: get duration in milliseconds
const _getDurationMs = (duration: Duration): number => {
  switch (duration) {
    case '1h': return 60 * 60 * 1000
    case '6h': return 6 * 60 * 60 * 1000
    case '24h': return 24 * 60 * 60 * 1000
    case '7d': return 7 * 24 * 60 * 60 * 1000
    case '30d': return 30 * 24 * 60 * 60 * 1000
    case 'today': {
      // Calculate milliseconds from the start of today to now
      const now = new Date()
      const startOfDay = new Date(now.getFullYear(), now.getMonth(), now.getDate())
      return now.getTime() - startOfDay.getTime()
    }
    default: return 6 * 60 * 60 * 1000
  }
}

// Helper: get dash array for stroke style
// traffic mode: all solid lines
// tokens/cache mode: Input/Read solid, Output/Write dashed
const getDashArray = (): number | number[] => {
  if (selectedView.value === 'traffic' || selectedView.value === 'cost') {
    return 0 // All solid lines
  }
  // Use actual series names to determine dash pattern
  const series = chartSeriesData.value.series
  const inLabel = selectedView.value === 'tokens' ? 'Input' : 'Cache Read'
  const dashArray: number[] = series.map(s => s.name.endsWith(` ${inLabel}`) ? 0 : 5)
  return dashArray.length > 0 ? dashArray : 0
}

// Helper: get chart colors aligned with series count
// traffic mode: one series and one color per key
// tokens/cache mode: each key's Input and Output share the same color
const getChartColors = (): string[] => {
  const series = chartSeriesData.value.series
  if (series.length === 0) return keyColors

  if (selectedView.value === 'traffic' || selectedView.value === 'cost') {
    return series.map((_, i) => keyColors[i % keyColors.length])
  }
  // Bidirectional mode: assign same color to Input/Read and Output/Write of each key
  const colors: string[] = []
  const colorMap = new Map<string, string>()
  for (const s of series) {
    // Strip " Input"/" Output"/" Cache Read"/" Cache Write" suffix to get key identity
    const keyName = s.name.replace(/ (Input|Output|Cache Read|Cache Write)$/, '')
    if (!colorMap.has(keyName)) {
      colorMap.set(keyName, keyColors[colorMap.size % keyColors.length])
    }
    colors.push(colorMap.get(keyName)!)
  }
  return colors
}

const fetchKeyMetrics = async (duration: Duration, interval?: string): Promise<ChannelKeyMetricsHistoryResponse> => {
  if (props.channelType === 'chat') {
    return api.getChatChannelKeyMetricsHistory(props.channelId, duration, interval)
  }
  if (props.channelType === 'images') {
    return api.getImagesChannelKeyMetricsHistory(props.channelId, duration, interval)
  }
  if (props.channelType === 'vectors') {
    return api.getVectorsChannelKeyMetricsHistory(props.channelId, duration, interval)
  }
  if (props.channelType === 'responses') {
    return api.getResponsesChannelKeyMetricsHistory(props.channelId, duration, interval)
  }
  if (props.channelType === 'gemini') {
    return api.getGeminiChannelKeyMetricsHistory(props.channelId, duration, interval)
  }
  return api.getChannelKeyMetricsHistory(props.channelId, duration, interval)
}

const withAdaptiveInterval = async (duration: Duration): Promise<ChannelKeyMetricsHistoryResponse> => {
  const requestKey = samplingKey.value
  const interval = adaptiveInterval.value?.key === requestKey ? adaptiveInterval.value.interval : undefined
  let newData = await fetchKeyMetrics(duration, interval)
  if (interval) {
    return newData
  }

  const points = (newData.keys || []).flatMap(keyData => keyData.dataPoints || [])
  const denseInterval = selectDenseSamplingInterval(
    duration,
    points,
    dp => dp.requestCount > 0 || dp.inputTokens > 0 || dp.outputTokens > 0 || dp.cacheReadTokens > 0 || dp.cacheCreationTokens > 0,
    newData.summary?.intervalSeconds
  )
  if (!denseInterval) {
    return newData
  }

  adaptiveInterval.value = { key: requestKey, interval: denseInterval }
  newData = await fetchKeyMetrics(duration, denseInterval)
  return newData
}

// Fetch data
const refreshData = async (isAutoRefresh = false) => {
  // Prevent out-of-order responses from overwriting newer state
  const requestId = ++refreshRequestId
  isRefreshing.value = true

  // Auto refresh uses silent update without loading state
  if (!isAutoRefresh) {
    isLoading.value = true
  }
  errorMessage.value = ''
  try {
    const newData = await withAdaptiveInterval(selectedDuration.value)

    // Ignore stale response
    if (requestId !== refreshRequestId) return

    // Check if we can use updateSeries (same keys structure)
    const canUpdateInPlace = isAutoRefresh &&
      chartRef.value &&
      historyData.value?.keys?.length === newData.keys?.length &&
      historyData.value?.keys?.every((k, i) => k.keyMask === newData.keys[i].keyMask)

    if (canUpdateInPlace) {
      // Update data in place and use updateSeries for smooth update
      historyData.value = newData
      const { series: newSeries } = buildChartSeries(newData)
      chartRef.value?.updateSeries(newSeries, false) // false = no animation reset
    } else {
      // Full update (initial load or structure changed)
      historyData.value = newData
    }
  } catch (error) {
    // Ignore stale error
    if (requestId !== refreshRequestId) return

    console.error('Failed to fetch key metrics history:', error)
    errorMessage.value = error instanceof Error ? error.message : t('chart.keyHistoryLoadFailed')
    showError.value = true
    historyData.value = null
  } finally {
    // Only let the latest request update flags
    if (requestId === refreshRequestId) {
      isRefreshing.value = false
      if (!isAutoRefresh) {
        isLoading.value = false
      }
    }
  }
}

// Watchers
watch(selectedDuration, () => {
  savePreference(props.channelType, 'duration', selectedDuration.value)
  adaptiveInterval.value = null
  refreshData()
}, { flush: 'sync' })

watch(selectedView, () => {
  savePreference(props.channelType, 'viewMode', selectedView.value)
  // View change doesn't need to refetch, just re-render chart
}, { flush: 'sync' })

// Watch channelType changes to reload preferences and refresh data
watch(() => props.channelType, (newChannelType) => {
  const prefs = loadSavedPreferences(newChannelType)
  const oldDuration = selectedDuration.value
  selectedView.value = prefs.view
  selectedDuration.value = prefs.duration
  historyData.value = null
  adaptiveInterval.value = null
  // Only explicitly refresh if duration didn't change (otherwise duration watcher handles it)
  if (oldDuration === prefs.duration) {
    refreshData()
  }
})

watch(() => props.channelId, () => {
  historyData.value = null
  adaptiveInterval.value = null
  refreshData()
})

// Initial load and start auto refresh
onMounted(() => {
  // 注册 tick 回调（global tick，与其他 5s 组件共用 setInterval）
  autoRefreshTick.onTick(() => {
    if (autoRefreshActive && !isRefreshing.value) refreshData(true)
  })
  refreshData()
  startAutoRefresh()
})

// Cleanup timer on unmount
onUnmounted(() => {
  stopAutoRefresh()
})

// Expose refresh method
defineExpose({
  refreshData
})
</script>

<style scoped>
.key-trend-chart-container {
  padding: 12px 16px;
  background: rgba(var(--v-theme-surface-variant), 0.3);
  border-top: 1px dashed rgba(var(--v-theme-on-surface), 0.2);
}

.v-theme--dark .key-trend-chart-container {
  background: rgba(var(--v-theme-surface-variant), 0.2);
  border-top-color: rgba(255, 255, 255, 0.15);
}

.chart-header {
  flex-wrap: wrap;
  gap: 8px;
}

.chart-control-toggle :deep(.v-btn.chart-control-btn) {
  font-size: 11px !important;
  font-weight: 600 !important;
  letter-spacing: 0 !important;
  padding-inline: 8px !important;
  min-width: 36px !important;
}

.chart-area {
  margin-top: 8px;
}

.summary-cards {
  display: flex;
  flex-wrap: wrap;
}

.summary-card {
  flex: 1 1 auto;
  min-width: 80px;
  padding: 8px 12px;
  background: rgba(var(--v-theme-surface-variant), 0.3);
  border-radius: 6px;
  text-align: center;
}

.v-theme--dark .summary-card {
  background: rgba(var(--v-theme-surface-variant), 0.2);
}

.summary-label {
  font-size: 12px;
  color: rgba(var(--v-theme-on-surface), 0.72);
  margin-bottom: 2px;
  line-height: 1.4;
  font-weight: 500;
}

.summary-value {
  font-size: 14px;
  font-weight: 600;
  line-height: 1.3;
}
</style>
