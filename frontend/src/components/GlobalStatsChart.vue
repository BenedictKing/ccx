<template>
  <div class="global-stats-chart-container">
    <!-- Snackbar for error notification -->
    <v-snackbar v-model="showError" color="error" :timeout="3000" location="top">
      {{ errorMessage }}
      <template #actions>
        <v-btn variant="text" @click="showError = false">{{ t('chart.close') }}</v-btn>
      </template>
    </v-snackbar>

    <!-- Header: Duration selector + View switcher -->
    <div class="chart-header d-flex align-center justify-space-between mb-3 flex-wrap ga-2">
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
          <v-icon size="small" class="mr-1">mdi-chart-areaspline</v-icon>
          {{ t('chart.tokens') }}
        </v-btn>
        <v-btn value="cost" size="x-small" class="chart-control-btn">
          <v-icon size="small" class="mr-1">mdi-cash</v-icon>
          {{ t('chart.cost') }}
        </v-btn>
      </v-btn-toggle>
    </div>

    <!-- Summary cards -->
    <div v-if="summary && !compact" class="summary-cards d-flex flex-wrap ga-2 mb-3">
      <div class="summary-card">
        <div class="summary-label">{{ t('chart.totalRequests') }}</div>
        <div class="summary-value">{{ formatNumber(summary.totalRequests) }}</div>
      </div>
      <div class="summary-card">
        <div class="summary-label">{{ t('chart.successRate') }}</div>
        <div class="summary-value" :class="{ 'text-success': summary.avgSuccessRate >= 95, 'text-warning': summary.avgSuccessRate >= 80 && summary.avgSuccessRate < 95, 'text-error': summary.avgSuccessRate < 80 }">
          {{ summary.avgSuccessRate.toFixed(1) }}%
        </div>
      </div>
      <div class="summary-card">
        <div class="summary-label">{{ t('chart.inputTokens') }}</div>
        <div class="summary-value">{{ formatNumber(summary.totalInputTokens) }}</div>
      </div>
      <div class="summary-card">
        <div class="summary-label">{{ t('chart.outputTokens') }}</div>
        <div class="summary-value">{{ formatNumber(summary.totalOutputTokens) }}</div>
      </div>
      <div v-if="summary.totalCacheReadTokens > 0 || summary.totalCacheCreationTokens > 0" class="summary-card">
        <div class="summary-label">{{ t('chart.cacheRw') }}</div>
        <div class="summary-value">{{ formatNumber(summary.totalCacheReadTokens) }} / {{ formatNumber(summary.totalCacheCreationTokens) }}</div>
      </div>
      <div v-if="(summary.totalCostUSD || 0) > 0" class="summary-card">
        <div class="summary-label">{{ t('chart.totalCost') }}</div>
        <div class="summary-value">${{ formatCost(summary.totalCostUSD || 0) }}</div>
      </div>
    </div>

    <!-- Compact summary (single line) -->
    <div v-if="summary && compact" class="compact-summary d-flex align-center ga-3 mb-2 text-caption">
      <span><strong>{{ formatNumber(summary.totalRequests) }}</strong> {{ t('orchestration.requests') }}</span>
      <span :class="{ 'text-success': summary.avgSuccessRate >= 95, 'text-warning': summary.avgSuccessRate >= 80 && summary.avgSuccessRate < 95, 'text-error': summary.avgSuccessRate < 80 }">
        <strong>{{ summary.avgSuccessRate.toFixed(1) }}%</strong> {{ t('chart.successRate') }}
      </span>
      <span><strong>{{ formatNumber(summary.totalInputTokens) }}</strong> {{ t('chart.input') }}</span>
      <span><strong>{{ formatNumber(summary.totalOutputTokens) }}</strong> {{ t('chart.output') }}</span>
      <span v-if="summary.totalCacheReadTokens > 0 || summary.totalCacheCreationTokens > 0">
        <strong>{{ formatNumber(summary.totalCacheReadTokens) }}/{{ formatNumber(summary.totalCacheCreationTokens) }}</strong> {{ t('chart.cacheRw') }}
      </span>
      <span v-if="(summary.totalCostUSD || 0) > 0">
        <strong>${{ formatCost(summary.totalCostUSD || 0) }}</strong> {{ t('chart.totalCost') }}
      </span>
    </div>

    <!-- Loading state -->
    <div v-if="isLoading" class="d-flex justify-center align-center" :style="{ height: chartHeight + 'px' }">
      <v-progress-circular indeterminate size="32" color="primary" />
    </div>

    <!-- Empty state -->
    <div v-else-if="!hasData" class="d-flex flex-column justify-center align-center text-medium-emphasis" :style="{ height: chartHeight + 'px' }">
      <v-icon size="40" color="grey-lighten-1">mdi-chart-timeline-variant</v-icon>
      <div class="text-caption mt-2">{{ t('chart.noRequestsInRange') }}</div>
    </div>

    <!-- Chart -->
    <div v-else class="chart-area">
      <apexchart
        ref="chartRef"
        :key="`global-chart-${selectedView}`"
        type="area"
        :height="chartHeight"
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
import { api, type GlobalStatsHistoryResponse, type GlobalHistoryDataPoint as _GlobalHistoryDataPoint, type GlobalStatsSummary, type ModelHistoryDataPoint } from '../services/api'
import { useI18n } from '../i18n'
import { useGlobalTick } from '../composables/useGlobalTick'
import { effectiveChartIntervalMs, selectDenseSamplingInterval } from '../utils/chartSampling'

// Register apexchart component
const apexchart = VueApexCharts

// Props
const props = withDefaults(defineProps<{
  apiType: 'messages' | 'chat' | 'responses' | 'gemini' | 'images' | 'vectors'
  compact?: boolean
}>(), {
  compact: false
})
const { t } = useI18n()

// Types
type ViewMode = 'traffic' | 'tokens' | 'cost'
type Duration = '1h' | '6h' | '24h' | 'today' | '7d' | '30d' | '90d' | '180d' | '365d' | 'thisyear'

// LocalStorage keys for preferences (per apiType)
const getStorageKey = (apiType: string, key: string) => `globalStats:${apiType}:${key}`

// Load saved preferences from localStorage (per apiType)
const loadSavedPreferences = (apiType: string) => {
  const savedView = localStorage.getItem(getStorageKey(apiType, 'viewMode')) as ViewMode | null
  const savedDuration = localStorage.getItem(getStorageKey(apiType, 'duration')) as Duration | null
  return {
    view: savedView && ['traffic', 'tokens', 'cost'].includes(savedView) ? savedView : 'traffic',
    duration: savedDuration && ['1h', '6h', '24h', 'today', '7d', '30d', '90d', '180d', '365d', 'thisyear'].includes(savedDuration) ? savedDuration : '6h'
  }
}

// Save preference to localStorage
const savePreference = (apiType: string, key: string, value: string) => {
  localStorage.setItem(getStorageKey(apiType, key), value)
}

// Theme
const theme = useTheme()
const isDark = computed(() => theme.global.current.value.dark)

// Load saved preferences for current apiType
const savedPrefs = loadSavedPreferences(props.apiType)

// State (initialized from saved preferences)
const selectedView = ref<ViewMode>(savedPrefs.view)
const selectedDuration = ref<Duration>(savedPrefs.duration)
const isLoading = ref(false)
const historyData = ref<GlobalStatsHistoryResponse | null>(null)
const samplingKey = computed(() => `${props.apiType}:${selectedDuration.value}`)
const adaptiveInterval = ref<{ key: string; interval: string } | null>(null)
const currentAdaptiveInterval = computed(() => (
  adaptiveInterval.value?.key === samplingKey.value ? adaptiveInterval.value.interval : undefined
))
const showError = ref(false)
const errorMessage = ref('')

// Chart ref for updateSeries
const chartRef = ref<InstanceType<typeof VueApexCharts> | null>(null)

// Auto refresh (使用全局 tick，visibility hidden 时自动暂停)
const AUTO_REFRESH_INTERVAL = 5000
const autoRefreshTick = useGlobalTick(AUTO_REFRESH_INTERVAL, 'GlobalStats')
let autoRefreshActive = false
const isRefreshing = ref(false)
let refreshRequestId = 0

const startAutoRefresh = () => {
  autoRefreshActive = true
}

const stopAutoRefresh = () => {
  autoRefreshActive = false
}

// Chart height based on compact mode
const chartHeight = computed(() => props.compact ? 180 : 260)

// Summary data
const summary = computed<GlobalStatsSummary | null>(() => historyData.value?.summary || null)

// Check if has data
const hasData = computed(() => {
  if (!historyData.value?.dataPoints) return false
  return historyData.value.dataPoints.length > 0 &&
    historyData.value.dataPoints.some(dp => dp.requestCount > 0)
})

// Check if cache data exists
const hasCacheData = computed(() => {
  if (!historyData.value?.dataPoints) return false
  return historyData.value.dataPoints.some(dp => (dp.cacheReadTokens || 0) > 0 || (dp.cacheCreationTokens || 0) > 0)
})

// Chart colors
const chartColors = {
  traffic: {
    primary: '#3b82f6',    // Blue for requests
  },
  tokens: {
    input: '#8b5cf6',      // Purple for input
    output: '#f97316',     // Orange for output
    cacheRead: '#10b981',  // Green for cache read
    cacheWrite: '#06b6d4'  // Cyan for cache write
  }
}

// Model color palette
const modelColors = [
  '#3b82f6', '#10b981', '#f97316', '#8b5cf6', '#ef4444',
  '#06b6d4', '#ec4899', '#84cc16', '#f59e0b', '#6366f1'
]

// Model list sorted by total request count
const sortedModels = computed(() => {
  const models = historyData.value?.modelDataPoints
  if (!models) return []
  return Object.entries(models)
    .map(([name, points]) => ({ name, points, total: points.reduce((s: number, p: ModelHistoryDataPoint) => s + p.requestCount, 0) }))
    .filter(m => m.total > 0)
    .sort((a, b) => b.total - a.total)
})

// Whether multi-model data exists (used to determine stacked mode)
const hasMultiModel = computed(() => sortedModels.value.length > 0)

// Failure rate threshold: show red background when exceeded
const FAILURE_RATE_THRESHOLD = 0.1 // 10%

// Aggregation interval settings (kept consistent with the backend)
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

const getAggregationInterval = (duration: Duration): number => {
  const interval = effectiveChartIntervalMs(summary.value?.intervalSeconds, currentAdaptiveInterval.value)
  if (interval) return interval
  return AGGREGATION_INTERVALS[duration] || 60000
}

// Calculate the failure rate for each time point
const timePointFailureRates = computed(() => {
  if (!historyData.value?.dataPoints?.length) return []
  return historyData.value.dataPoints
    .filter(dp => dp.requestCount > 0)
    .map(dp => ({
      timestamp: new Date(dp.timestamp).getTime(),
      failureRate: dp.requestCount > 0 ? dp.failureCount / dp.requestCount : 0
    }))
})

// Calculate opacity based on failure rate
const getFailureOpacity = (failureRate: number): number => {
  const minOpacity = 0.08
  const maxOpacity = 0.65
  const normalizedRate = Math.min((failureRate - FAILURE_RATE_THRESHOLD) / (1 - FAILURE_RATE_THRESHOLD), 1)
  return minOpacity + normalizedRate * (maxOpacity - minOpacity)
}

// Generate failure-rate background band annotations
const failureAnnotations = computed(() => {
  if (selectedView.value !== 'traffic') return []
  const rates = timePointFailureRates.value
  if (rates.length === 0) return []

  const interval = getAggregationInterval(selectedDuration.value)
  const annotations: any[] = []

  rates.forEach((point, index) => {
    if (point.failureRate >= FAILURE_RATE_THRESHOLD) {
      let pointInterval = interval
      if (rates.length > 1) {
        if (index > 0) {
          pointInterval = point.timestamp - rates[index - 1].timestamp
        } else if (index < rates.length - 1) {
          pointInterval = rates[index + 1].timestamp - point.timestamp
        }
      }
      pointInterval = Math.min(pointInterval, interval * 2)

      annotations.push({
        x: point.timestamp - pointInterval / 2,
        x2: point.timestamp + pointInterval / 2,
        fillColor: '#ef4444',
        opacity: getFailureOpacity(point.failureRate),
        borderColor: 'transparent',
        borderWidth: 0,
        label: { text: '' }
      })
    }
  })

  return annotations
})

const firstNonEmptyTimestamp = computed(() => {
  if (!historyData.value?.dataPoints?.length) return undefined
  let earliest = Infinity
  historyData.value.dataPoints.forEach(dp => {
    const hasVisibleData = selectedView.value === 'traffic'
      ? dp.requestCount > 0
      : selectedView.value === 'cost'
        ? (dp.costUSD || 0) > 0
        : dp.inputTokens > 0 || dp.outputTokens > 0 || dp.cacheReadTokens > 0 || dp.cacheCreationTokens > 0
    if (hasVisibleData) {
      const ts = new Date(dp.timestamp).getTime()
      if (ts < earliest) earliest = ts
    }
  })
  return earliest === Infinity ? undefined : earliest
})

const xaxisMin = computed(() => {
  if (!['7d', '30d', '90d', '180d', '365d', 'thisyear'].includes(selectedDuration.value)) return undefined
  const ts = firstNonEmptyTimestamp.value
  if (ts === undefined) return undefined
  const interval = getAggregationInterval(selectedDuration.value)
  return ts - interval
})

// Format number for display
const formatNumber = (num: number): string => {
  if (num >= 1000000) return (num / 1000000).toFixed(1) + 'M'
  if (num >= 1000) return (num / 1000).toFixed(1) + 'K'
  return num.toFixed(0)
}

// Format cost (USD) for display
const formatCost = (val: number): string => {
  if (val >= 1000) return (val / 1000).toFixed(2) + 'K'
  if (val >= 1) return val.toFixed(2)
  return val.toFixed(4)
}

// Chart options
const chartOptions = computed<ApexCharts.ApexOptions>(() => {
  const mode = selectedView.value
  const isMultiModelView = (mode === 'traffic' || mode === 'cost') && hasMultiModel.value

  // Traffic/cost mode colors: assign by model in multi-model mode, otherwise use a single color
  const multiModelColors = isMultiModelView
    ? sortedModels.value.map((_, i) => modelColors[i % modelColors.length])
    : [chartColors.traffic.primary]

  return {
    chart: {
      toolbar: { show: false },
      zoom: { enabled: false },
      background: 'transparent',
      fontFamily: 'inherit',
      stacked: isMultiModelView,
      defaultLocale: 'en',
      animations: {
        enabled: false
      }
    },
    theme: {
      mode: isDark.value ? 'dark' : 'light'
    },
    colors: mode === 'traffic' || mode === 'cost'
      ? multiModelColors
      : hasCacheData.value
        ? [chartColors.tokens.input, chartColors.tokens.output, chartColors.tokens.cacheRead, chartColors.tokens.cacheWrite]
        : [chartColors.tokens.input, chartColors.tokens.output],
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
      dashArray: mode === 'tokens' ? (hasCacheData.value ? [0, 5, 0, 5] : [0, 5]) : 0
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
    yaxis: mode === 'tokens' ? (() => {
      const axes: any[] = [
        {
          seriesName: t('chart.inputTokens'),
          labels: {
            formatter: (val: number) => formatNumber(val),
            style: { fontSize: '11px' }
          },
          min: 0,
          forceNiceScale: true
        },
        {
          seriesName: t('chart.outputTokens'),
          opposite: true,
          labels: {
            formatter: (val: number) => formatNumber(val),
            style: { fontSize: '11px' }
          },
          min: 0,
          forceNiceScale: true
        }
      ]
      if (hasCacheData.value) {
        axes.push(
          { seriesName: 'Cache Read', show: false, min: 0, forceNiceScale: true },
          { seriesName: 'Cache Write', show: false, min: 0, forceNiceScale: true }
        )
      }
      return axes
    })() : {
      labels: {
        formatter: (val: number) => mode === 'cost' ? '$' + formatCost(val) : Math.round(val).toString(),
        style: { fontSize: '11px' }
      },
      min: 0,
      forceNiceScale: true
    },
    tooltip: {
      x: {
        format: 'MM-dd HH:mm'
      },
      y: {
        formatter: (val: number) => mode === 'traffic'
          ? `${Math.round(val)} ${t('chart.requestUnit')}`
          : mode === 'cost'
            ? `$${formatCost(val)}`
            : `${formatNumber(val)} ${t('chart.tokenUnit')}`
      },
      custom: mode === 'traffic' ? buildTrafficTooltip : undefined
    },
    annotations: {
      xaxis: failureAnnotations.value
    },
    legend: {
      show: mode === 'tokens' || isMultiModelView,
      position: 'top' as const,
      horizontalAlign: 'right' as const,
      fontSize: '11px',
      markers: { size: 4 }
    }
  }
})

// Build a custom tooltip for traffic mode
const buildTrafficTooltip = ({ dataPointIndex }: any): string => {
  if (!historyData.value?.dataPoints) return ''
  const dp = historyData.value.dataPoints[dataPointIndex]
  if (!dp) return ''

  const date = new Date(dp.timestamp)
  const timeStr = date.toLocaleString('zh-CN', { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit' })
  const hasFailure = dp.failureCount > 0

  // HTML escaping helper
  const escapeHtml = (str: string): string => {
    return str.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;')
  }

  let html = `<div style="padding: 8px 12px; font-size: 13px; line-height: 1.6;">`
  html += `<div style="font-weight: 600; margin-bottom: 6px; color: ${hasFailure ? '#ef4444' : 'inherit'};">${timeStr}</div>`

  const models = sortedModels.value
  if (models.length > 0) {
    // Multi-model mode: show request and failure counts for each model
    models.forEach((model, idx) => {
      const mdp = model.points[dataPointIndex]
      if (!mdp || mdp.requestCount === 0) return
      const color = modelColors[idx % modelColors.length]
      const hasModelFailure = mdp.failureCount > 0
      const failRate = mdp.requestCount > 0 ? (mdp.failureCount / mdp.requestCount * 100).toFixed(0) : '0'
      html += `<div style="display: flex; align-items: center; margin: 4px 0;">`
      html += `<span style="width: 10px; height: 10px; border-radius: 50%; background: ${color}; margin-right: 6px;"></span>`
      html += `<span style="flex: 1;">${escapeHtml(model.name)}</span>`
      html += `<span style="margin-left: 12px; font-weight: 500;">${mdp.requestCount} ${t('chart.requestUnit')}</span>`
      if (hasModelFailure) {
        html += `<span style="margin-left: 6px; color: #ef4444; font-size: 12px;">(${mdp.failureCount} ${t('chart.issueCount')}, ${failRate}%)</span>`
      }
      html += `</div>`
    })
    // Totals row
    const grandFailureRate = dp.requestCount > 0 ? (dp.failureCount / dp.requestCount * 100).toFixed(1) : '0'
    html += `<div style="border-top: 1px solid rgba(128,128,128,0.3); margin-top: 6px; padding-top: 6px; font-weight: 600;">`
    html += `<span>${t('chart.total')}: ${dp.requestCount} ${t('chart.requestUnit')}</span>`
    if (hasFailure) {
      html += `<span style="color: #ef4444; margin-left: 8px;">${dp.failureCount} ${t('chart.issueCount')} (${grandFailureRate}%)</span>`
    }
    html += `</div>`
  } else {
    // Single-series fallback mode
    const failureRate = dp.requestCount > 0 ? (dp.failureCount / dp.requestCount * 100).toFixed(1) : '0'
    html += `<div style="display: flex; align-items: center; margin: 4px 0;">`
    html += `<span style="width: 10px; height: 10px; border-radius: 50%; background: #3b82f6; margin-right: 6px;"></span>`
    html += `<span style="flex: 1;">${t('chart.totalRequests')}</span>`
    html += `<span style="margin-left: 12px; font-weight: 500;">${dp.requestCount} ${t('chart.requestUnit')}</span>`
    html += `</div>`
    if (hasFailure) {
      html += `<div style="color: #ef4444; font-size: 12px; margin-top: 4px;">${dp.failureCount} ${t('chart.issueCount')} (${failureRate}%)</div>`
    }
  }

  html += `</div>`
  return html
}

// Build chart series
const chartSeries = computed(() => {
  if (!historyData.value?.dataPoints) return []

  const dataPoints = historyData.value.dataPoints
  const mode = selectedView.value

  if (mode === 'traffic') {
    // Generate multiple series by model when model data exists; otherwise fall back to a single series
    const models = sortedModels.value
    if (models.length > 0) {
      return models.map(model => ({
        name: model.name,
        data: model.points.map((p: ModelHistoryDataPoint) => ({
          x: new Date(p.timestamp).getTime(),
          y: p.requestCount
        }))
      }))
    }
    return [
      {
        name: t('status.metrics.requests'),
        data: dataPoints.map(dp => ({
          x: new Date(dp.timestamp).getTime(),
          y: dp.requestCount
        }))
      }
    ]
  } else if (mode === 'cost') {
    // 按模型画花费曲线；无模型维度时回退到全局点成本
    const models = sortedModels.value
    if (models.length > 0) {
      return models.map(model => ({
        name: model.name,
        data: model.points.map((p: ModelHistoryDataPoint) => ({
          x: new Date(p.timestamp).getTime(),
          y: p.costUSD || 0
        }))
      }))
    }
    return [
      {
        name: t('chart.cost'),
        data: dataPoints.map(dp => ({
          x: new Date(dp.timestamp).getTime(),
          y: dp.costUSD || 0
        }))
      }
    ]
  } else {
    const series = [
      {
        name: t('chart.inputTokens'),
        data: dataPoints.map(dp => ({
          x: new Date(dp.timestamp).getTime(),
          y: dp.inputTokens
        }))
      },
      {
        name: t('chart.outputTokens'),
        data: dataPoints.map(dp => ({
          x: new Date(dp.timestamp).getTime(),
          y: dp.outputTokens
        }))
      }
    ]
    const hasCacheData = dataPoints.some(dp => (dp.cacheReadTokens || 0) > 0 || (dp.cacheCreationTokens || 0) > 0)
    if (hasCacheData) {
      series.push(
        {
          name: 'Cache Read',
          data: dataPoints.map(dp => ({
            x: new Date(dp.timestamp).getTime(),
            y: dp.cacheReadTokens || 0
          }))
        },
        {
          name: 'Cache Write',
          data: dataPoints.map(dp => ({
            x: new Date(dp.timestamp).getTime(),
            y: dp.cacheCreationTokens || 0
          }))
        }
      )
    }
    return series
  }
})

const fetchGlobalStats = async (duration: Duration, interval?: string): Promise<GlobalStatsHistoryResponse> => {
  if (props.apiType === 'messages') {
    return api.getMessagesGlobalStats(duration, interval)
  }
  if (props.apiType === 'chat') {
    return api.getChatGlobalStats(duration, interval)
  }
  if (props.apiType === 'gemini') {
    return api.getGeminiGlobalStats(duration, interval)
  }
  if (props.apiType === 'images') {
    return api.getImagesGlobalStats(duration, interval)
  }
  if (props.apiType === 'vectors') {
    return api.getVectorsGlobalStats(duration, interval)
  }
  return api.getResponsesGlobalStats(duration, interval)
}

const withAdaptiveInterval = async (duration: Duration): Promise<GlobalStatsHistoryResponse> => {
  const requestKey = samplingKey.value
  const interval = adaptiveInterval.value?.key === requestKey ? adaptiveInterval.value.interval : undefined
  let newData = await fetchGlobalStats(duration, interval)
  if (interval) {
    return newData
  }

  const denseInterval = selectDenseSamplingInterval(
    duration,
    newData.dataPoints || [],
    dp => dp.requestCount > 0 || dp.inputTokens > 0 || dp.outputTokens > 0 || dp.cacheReadTokens > 0 || dp.cacheCreationTokens > 0,
    newData.summary?.intervalSeconds
  )
  if (!denseInterval) {
    return newData
  }

  adaptiveInterval.value = { key: requestKey, interval: denseInterval }
  newData = await fetchGlobalStats(duration, denseInterval)
  return newData
}

// Fetch data
const refreshData = async (isAutoRefresh = false) => {
  const requestId = ++refreshRequestId
  isRefreshing.value = true
  if (!isAutoRefresh) {
    isLoading.value = true
  }
  errorMessage.value = ''

  try {
    const newData = await withAdaptiveInterval(selectedDuration.value)

    // Check whether updateSeries can be used for a smooth update
    const oldModels = historyData.value?.modelDataPoints ? Object.keys(historyData.value.modelDataPoints).sort().join(',') : ''
    const newModels = newData.modelDataPoints ? Object.keys(newData.modelDataPoints).sort().join(',') : ''
    const canUpdateInPlace = isAutoRefresh &&
      chartRef.value &&
      historyData.value?.dataPoints?.length === newData.dataPoints?.length &&
      oldModels === newModels

    if (requestId !== refreshRequestId) {
      return
    }

    if (canUpdateInPlace) {
      historyData.value = newData
      const series = chartSeries.value
      chartRef.value?.updateSeries(series, false)
    } else {
      historyData.value = newData
    }
  } catch (error) {
    if (requestId !== refreshRequestId) {
      return
    }
    console.error('Failed to fetch global stats:', error)
    errorMessage.value = error instanceof Error ? error.message : t('chart.globalStatsLoadFailed')
    showError.value = true
    historyData.value = null
  } finally {
    if (requestId === refreshRequestId && !isAutoRefresh) {
      isLoading.value = false
    }
    if (requestId === refreshRequestId) {
      isRefreshing.value = false
    }
  }
}

// Watchers
watch(selectedDuration, (newVal) => {
  savePreference(props.apiType, 'duration', newVal)
  adaptiveInterval.value = null
  refreshData()
})

watch(selectedView, (newVal) => {
  savePreference(props.apiType, 'viewMode', newVal)
})

watch(() => props.apiType, (newApiType) => {
  // Load preferences for the new apiType
  const prefs = loadSavedPreferences(newApiType)
  selectedView.value = prefs.view
  selectedDuration.value = prefs.duration
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

// Cleanup on unmount
onUnmounted(() => {
  stopAutoRefresh()
})

// Expose refresh method
defineExpose({
  refreshData,
  startAutoRefresh,
  stopAutoRefresh
})
</script>

<style scoped>
.global-stats-chart-container {
  padding: 12px 16px;
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
  font-size: 13px;
  color: rgba(var(--v-theme-on-surface), 0.72);
  margin-bottom: 4px;
  line-height: 1.4;
  font-weight: 500;
}

.summary-value {
  font-size: 16px;
  font-weight: 600;
  line-height: 1.3;
}

.compact-summary {
  padding: 4px 8px;
  background: rgba(var(--v-theme-surface-variant), 0.2);
  border-radius: 4px;
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

/* Responsive adjustments */
@media (max-width: 600px) {
  .summary-card {
    min-width: 70px;
    padding: 6px 8px;
  }

  .summary-value {
    font-size: 14px;
  }
}
</style>
