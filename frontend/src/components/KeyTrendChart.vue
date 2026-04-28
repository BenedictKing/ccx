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
      </v-btn-toggle>
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
import type { ApexOptions } from 'apexcharts'
import { api, type ChannelKeyMetricsHistoryResponse } from '../services/api'
import { useI18n } from '../i18n'

// Register apexchart component
const apexchart = VueApexCharts

// Props
const props = defineProps<{
  channelId: number
  channelType: 'messages' | 'chat' | 'responses' | 'gemini' | 'images'
}>()
const { t } = useI18n()

// View mode type
type ViewMode = 'traffic' | 'tokens' | 'cache'
type Duration = '1h' | '6h' | '24h' | 'today'

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
      view: savedView && ['traffic', 'tokens', 'cache'].includes(savedView) ? savedView : 'traffic',
      duration: savedDuration && ['1h', '6h', '24h', 'today'].includes(savedDuration) ? savedDuration : '1h'
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
const showError = ref(false)
const errorMessage = ref('')

// Chart ref for updateSeries
const chartRef = ref<InstanceType<typeof VueApexCharts> | null>(null)

// request id for refreshData
let refreshRequestId = 0

// Auto refresh timer
const AUTO_REFRESH_INTERVAL = 5000
let autoRefreshTimer: ReturnType<typeof setInterval> | null = null

const startAutoRefresh = () => {
  stopAutoRefresh()
  autoRefreshTimer = setInterval(() => {
    // Skip if already refreshing to prevent concurrent requests / stale overwrites
    if (!isRefreshing.value) {
      refreshData(true) // true = auto refresh, use updateSeries
    }
  }, AUTO_REFRESH_INTERVAL)
}

const stopAutoRefresh = () => {
  if (autoRefreshTimer) {
    clearInterval(autoRefreshTimer)
    autoRefreshTimer = null
  }
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

// Aggregation interval settings (kept consistent with the backend)
// 1h = 1m, 6h = 5m, 24h = 15m, today = dynamically calculated
const AGGREGATION_INTERVALS: Record<Duration, number> = {
  '1h': 60000,    // 1 minute
  '6h': 300000,   // 5 minutes
  '24h': 900000,  // 15 minutes
  'today': 300000 // 5 minutes (today uses 5-minute intervals by default)
}

// Get the aggregation interval based on the selected duration
const getAggregationInterval = (duration: Duration): number => {
  const interval = AGGREGATION_INTERVALS[duration]
  if (interval === undefined) {
    console.warn(`[KeyTrendChart] Unknown duration "${duration}", falling back to 1m interval`)
    return 60000
  }
  return interval
}

// Align the timestamp to the aggregation bucket (round down)
const alignToBucket = (timestamp: number, interval: number): number => {
  return Math.floor(timestamp / interval) * interval
}

// Computed: check if has data
const hasData = computed(() => {
  if (!historyData.value) return false
  return historyData.value.keys &&
    historyData.value.keys.length > 0 &&
    historyData.value.keys.some(k => k.dataPoints && k.dataPoints.length > 0)
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
        label: {
          text: ''
        }
      })
    }
  })

  return annotations
})

// Computed: get all data points flattened
const _allDataPoints = computed(() => {
  if (!historyData.value?.keys) return []
  return historyData.value.keys.flatMap(k => k.dataPoints || [])
})

// Computed: chart options
const chartOptions = computed<ApexOptions>(() => {
  const mode = selectedView.value

  // Token/cache mode uses dual Y-axes (left for Input/Read, right for Output/Write)
  // All Input series share the left Y-axis, and all Output series share the right Y-axis
  let yaxisConfig: any
  if (mode === 'tokens' || mode === 'cache') {
    const keyCount = historyData.value?.keys?.length || 1
    const inLabel = mode === 'tokens' ? 'Input' : 'Cache Read'
    const outLabel = mode === 'tokens' ? 'Output' : 'Cache Write'

    // Use the first key's series name as the Y-axis anchor
    const firstKey = historyData.value?.keys?.[0]
    const firstDisplayName = firstKey?.model ? `${firstKey.keyMask}/${firstKey.model}` : firstKey?.keyMask
    const anchorInName = firstDisplayName ? `${firstDisplayName} ${inLabel}` : undefined
    const anchorOutName = firstDisplayName ? `${firstDisplayName} ${outLabel}` : undefined

    yaxisConfig = [
      // Left Y-axis (Input/Read)
      {
        seriesName: anchorInName,
        show: true,
        labels: {
          formatter: (val: number) => formatAxisValue(val, mode),
          style: { fontSize: '11px' }
        },
        min: 0
      },
      // Right Y-axis (Output/Write)
      {
        seriesName: anchorOutName,
        opposite: true,
        show: true,
        labels: {
          formatter: (val: number) => formatAxisValue(val, mode),
          style: { fontSize: '11px' }
        },
        min: 0
      }
    ]

    // Bind later key series to the same Y-axis pair (seriesName points to the anchor)
    for (let i = 1; i < keyCount; i++) {
      yaxisConfig.push({
        seriesName: anchorInName,
        show: false,
        min: 0
      })
      yaxisConfig.push({
        seriesName: anchorOutName,
        show: false,
        min: 0
      })
    }
  } else {
    yaxisConfig = {
      labels: {
        formatter: (val: number) => formatAxisValue(val, mode),
        style: { fontSize: '11px' }
      },
      min: 0
    }
  }

  return {
    chart: {
      toolbar: { show: false },
      zoom: { enabled: false },
      background: 'transparent',
      fontFamily: 'inherit',
      sparkline: { enabled: false },
      stacked: mode === 'traffic',
      animations: {
        enabled: true,
        speed: 400,
        animateGradually: { enabled: true, delay: 150 },
        dynamicAnimation: { enabled: true, speed: 350 }
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
      labels: {
        datetimeUTC: false,
        format: 'HH:mm',
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
      show: mode === 'traffic',
      position: 'top' as const,
      horizontalAlign: 'right' as const,
      fontSize: '11px',
      markers: { size: 4 }
    }
  }
})

// Build chart series from data
const buildChartSeries = (data: ChannelKeyMetricsHistoryResponse | null) => {
  if (!data?.keys) return []

  const mode = selectedView.value
  const result: { name: string; data: { x: number; y: number }[] }[] = []

  data.keys.forEach((keyData, _keyIndex) => {
    // Display name: show "keyMask/model" when a model exists; otherwise show only keyMask
    const displayName = keyData.model ? `${keyData.keyMask}/${keyData.model}` : keyData.keyMask

    if (mode === 'traffic') {
      // One-way mode: show request count only
      result.push({
        name: displayName,
        data: keyData.dataPoints.map(dp => ({
          x: new Date(dp.timestamp).getTime(),
          y: dp.requestCount
        }))
      })
    } else {
      // Bidirectional mode: create two series per key (Input/Output or Read/Write)
      const inLabel = mode === 'tokens' ? 'Input' : 'Cache Read'
      const outLabel = mode === 'tokens' ? 'Output' : 'Cache Write'

      // Forward direction (Input/Read)
      result.push({
        name: `${displayName} ${inLabel}`,
        data: keyData.dataPoints.map(dp => ({
          x: new Date(dp.timestamp).getTime(),
          y: mode === 'tokens' ? dp.inputTokens : dp.cacheReadTokens
        }))
      })

      // Output/Write - distinguish with a dashed line
      result.push({
        name: `${displayName} ${outLabel}`,
        data: keyData.dataPoints.map(dp => ({
          x: new Date(dp.timestamp).getTime(),
          y: mode === 'tokens' ? dp.outputTokens : dp.cacheCreationTokens
        }))
      })
    }
  })

  return result
}

// Computed: chart series data
const chartSeries = computed(() => buildChartSeries(historyData.value))

// Helper: format number for display
const formatNumber = (num: number): string => {
  if (num >= 1000000) return (num / 1000000).toFixed(1) + 'M'
  if (num >= 1000) return (num / 1000).toFixed(1) + 'K'
  return num.toFixed(0)
}

// Helper: format axis value based on view mode
const formatAxisValue = (val: number, mode: ViewMode): string => {
  switch (mode) {
    case 'traffic':
      return Math.round(val).toString()
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
    case 'tokens':
    case 'cache':
      return formatNumber(Math.abs(val))
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
    html += `<span style="margin-left: 12px; font-weight: 500;">${stat.total}</span>`
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
// tokens/cache mode: each key has two series (solid forward line, dashed reverse line)
const getDashArray = (): number | number[] => {
  if (selectedView.value === 'traffic') {
    return 0 // All solid lines
  }
  // Bidirectional mode: each key produces 2 series [solid forward line, dashed reverse line]
  const keyCount = historyData.value?.keys?.length || 0
  const dashArray: number[] = []
  for (let i = 0; i < keyCount; i++) {
    dashArray.push(0)  // Forward direction (Input/Read) - solid
    dashArray.push(5)  // Reverse direction (Output/Write) - dashed
  }
  return dashArray.length > 0 ? dashArray : 0
}

// Helper: get chart colors aligned with series count
// traffic mode: one series and one color per key
// tokens/cache mode: each key has two series (Input/Output) using the same color
const getChartColors = (): string[] => {
  const keyCount = historyData.value?.keys?.length || 0
  if (keyCount === 0) return keyColors

  if (selectedView.value === 'traffic') {
    // Traffic mode: one color per key
    return historyData.value!.keys.map((_, i) => keyColors[i % keyColors.length])
  }
  // Bidirectional mode: duplicate the color for each key (same color for Input and Output)
  const colors: string[] = []
  for (let i = 0; i < keyCount; i++) {
    const color = keyColors[i % keyColors.length]
    colors.push(color)  // Forward direction
    colors.push(color)  // Reverse direction (same color)
  }
  return colors
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
    let newData: ChannelKeyMetricsHistoryResponse
    if (props.channelType === 'chat') {
      newData = await api.getChatChannelKeyMetricsHistory(props.channelId, selectedDuration.value)
    } else if (props.channelType === 'responses') {
      newData = await api.getResponsesChannelKeyMetricsHistory(props.channelId, selectedDuration.value)
    } else if (props.channelType === 'gemini') {
      newData = await api.getGeminiChannelKeyMetricsHistory(props.channelId, selectedDuration.value)
    } else {
      newData = await api.getChannelKeyMetricsHistory(props.channelId, selectedDuration.value)
    }

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
      const newSeries = buildChartSeries(newData)
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
  // Only explicitly refresh if duration didn't change (otherwise duration watcher handles it)
  if (oldDuration === prefs.duration) {
    refreshData()
  }
})

// Initial load and start auto refresh
onMounted(() => {
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
</style>
