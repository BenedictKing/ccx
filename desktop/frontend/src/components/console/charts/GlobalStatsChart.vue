<template>
  <div class="global-stats-chart">
    <!-- Header: Duration selector + View switcher -->
    <div class="flex items-center justify-between mb-3 flex-wrap gap-2">
      <div class="flex items-center gap-2">
        <!-- Duration selector -->
        <div class="inline-flex rounded-md border border-border divide-x divide-border">
          <button
            v-for="opt in durationOptions"
            :key="opt.value"
            type="button"
            class="px-2 py-1 text-[11px] font-semibold transition-colors hover:bg-accent/40 disabled:opacity-50"
            :class="{ 'bg-accent text-accent-foreground': selectedDuration === opt.value }"
            :disabled="isLoading"
            @click="selectedDuration = opt.value"
          >
            {{ opt.label }}
          </button>
        </div>

        <button
          type="button"
          class="p-1 text-muted-foreground hover:text-foreground transition-colors disabled:opacity-50"
          :disabled="isLoading"
          @click="refreshData"
        >
          <svg v-if="!isLoading" class="w-4 h-4" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
          </svg>
          <svg v-else class="w-4 h-4 animate-spin" fill="none" viewBox="0 0 24 24">
            <circle class="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" stroke-width="4" />
            <path class="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z" />
          </svg>
        </button>
      </div>

      <!-- View switcher -->
      <div class="inline-flex rounded-md border border-border divide-x divide-border">
        <button
          v-for="view in viewOptions"
          :key="view.value"
          type="button"
          class="px-2 py-1 text-[11px] font-semibold transition-colors hover:bg-accent/40 disabled:opacity-50 flex items-center gap-1"
          :class="{ 'bg-accent text-accent-foreground': selectedView === view.value }"
          :disabled="isLoading"
          @click="selectedView = view.value"
        >
          <svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path v-if="view.value === 'traffic'" stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M7 12l3-3 3 3 4-4M8 21l4-4 4 4M3 4h18M4 4h16v12a1 1 0 01-1 1H5a1 1 0 01-1-1V4z" />
            <path v-else-if="view.value === 'tokens'" stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M16 8v8m-4-5v5m-4-2v2m-2 4h12a2 2 0 002-2V6a2 2 0 00-2-2H6a2 2 0 00-2 2v12a2 2 0 002 2z" />
            <path v-else stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 1v22M17 5H9.5a3.5 3.5 0 000 7h5a3.5 3.5 0 010 7H6" />
          </svg>
          {{ view.label }}
        </button>
      </div>
    </div>

    <!-- Summary cards -->
    <div v-if="summary && !compact" class="flex flex-wrap gap-2 mb-3">
      <div class="flex-1 min-w-[80px] p-2 rounded-lg text-center bg-secondary/30 dark:bg-secondary/20">
        <div class="text-xs text-muted-foreground font-medium mb-1">{{ t('chart.totalRequests') }}</div>
        <div class="text-sm font-semibold">{{ formatNumber(summary.totalRequests) }}</div>
      </div>
      <div class="flex-1 min-w-[80px] p-2 rounded-lg text-center bg-secondary/30 dark:bg-secondary/20">
        <div class="text-xs text-muted-foreground font-medium mb-1">{{ t('chart.successRate') }}</div>
        <div
          class="text-sm font-semibold"
          :class="{
            'text-accent': summary.avgSuccessRate >= 95,
            'text-warning': summary.avgSuccessRate >= 80 && summary.avgSuccessRate < 95,
            'text-destructive': summary.avgSuccessRate < 80,
          }"
        >
          {{ summary.avgSuccessRate.toFixed(1) }}%
        </div>
      </div>
      <div class="flex-1 min-w-[80px] p-2 rounded-lg text-center bg-secondary/30 dark:bg-secondary/20">
        <div class="text-xs text-muted-foreground font-medium mb-1">{{ t('chart.inputTokens') }}</div>
        <div class="text-sm font-semibold">{{ formatNumber(summary.totalInputTokens) }}</div>
      </div>
      <div class="flex-1 min-w-[80px] p-2 rounded-lg text-center bg-secondary/30 dark:bg-secondary/20">
        <div class="text-xs text-muted-foreground font-medium mb-1">{{ t('chart.outputTokens') }}</div>
        <div class="text-sm font-semibold">{{ formatNumber(summary.totalOutputTokens) }}</div>
      </div>
      <div
        v-if="summary.totalCacheReadTokens > 0 || summary.totalCacheCreationTokens > 0"
        class="flex-1 min-w-[80px] p-2 rounded-lg text-center bg-secondary/30 dark:bg-secondary/20"
      >
        <div class="text-xs text-muted-foreground font-medium mb-1">Cache R/W</div>
        <div class="text-sm font-semibold">
          {{ formatNumber(summary.totalCacheReadTokens) }} / {{ formatNumber(summary.totalCacheCreationTokens) }}
        </div>
      </div>
      <div
        v-if="(summary.totalCostUSD || 0) > 0"
        class="flex-1 min-w-[80px] p-2 rounded-lg text-center bg-secondary/30 dark:bg-secondary/20"
      >
        <div class="text-xs text-muted-foreground font-medium mb-1">{{ t('chart.totalCost') }}</div>
        <div class="text-sm font-semibold">${{ formatCost(summary.totalCostUSD || 0) }}</div>
      </div>
    </div>

    <!-- Compact summary (single line) -->
    <div v-if="summary && compact" class="flex items-center gap-3 mb-2 text-xs">
      <span><strong>{{ formatNumber(summary.totalRequests) }}</strong> {{ t('chart.requestUnit') }}</span>
      <span
        :class="{
          'text-accent': summary.avgSuccessRate >= 95,
          'text-warning': summary.avgSuccessRate >= 80 && summary.avgSuccessRate < 95,
          'text-destructive': summary.avgSuccessRate < 80,
        }"
      >
        <strong>{{ summary.avgSuccessRate.toFixed(1) }}%</strong> {{ t('chart.successRate') }}
      </span>
      <span><strong>{{ formatNumber(summary.totalInputTokens) }}</strong> {{ t('chart.input') }}</span>
      <span><strong>{{ formatNumber(summary.totalOutputTokens) }}</strong> {{ t('chart.output') }}</span>
      <span v-if="summary.totalCacheReadTokens > 0 || summary.totalCacheCreationTokens > 0">
        <strong>{{ formatNumber(summary.totalCacheReadTokens) }}/{{ formatNumber(summary.totalCacheCreationTokens) }}</strong> Cache R/W
      </span>
      <span v-if="(summary.totalCostUSD || 0) > 0">
        <strong>${{ formatCost(summary.totalCostUSD || 0) }}</strong> {{ t('chart.totalCost') }}
      </span>
    </div>

    <!-- Loading state -->
    <div
      v-if="isLoading"
      class="flex items-center justify-center"
      :style="{ height: chartHeight + 'px' }"
    >
      <div class="w-8 h-8 border-2 border-primary border-t-transparent rounded-full animate-spin" />
    </div>

    <!-- Empty state -->
    <div
      v-else-if="!hasData"
      class="flex flex-col items-center justify-center text-muted-foreground"
      :style="{ height: chartHeight + 'px' }"
    >
      <div class="text-2xl mb-2 opacity-40">&#x1F4C8;</div>
      <div class="text-xs">{{ t('chart.noData') }}</div>
    </div>

    <!-- Chart -->
    <div v-else>
      <VueApexCharts
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
import { computed, ref, watch, onMounted } from 'vue'
import VueApexCharts from 'vue3-apexcharts'
import type { ApexOptions } from 'apexcharts'
import { useTheme } from '@/composables/useTheme'
import { useI18n } from '@/i18n'
import type { GlobalHistoryDataPoint, GlobalStatsSummary, ModelHistoryDataPoint } from '@/services/admin-api'
import { effectiveChartIntervalMs } from '@/utils/chart-sampling'

const props = withDefaults(
  defineProps<{
    apiType: 'messages' | 'chat' | 'responses' | 'gemini' | 'images' | 'vectors'
    compact?: boolean
    chartInterval?: string
  }>(),
  {
    compact: false,
    chartInterval: undefined,
  },
)

const emit = defineEmits<{
  refresh: [duration: string]
}>()

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

const { theme } = useTheme()
const { t } = useI18n()

const isDark = computed(() => {
  if (theme.value === 'dark') return true
  if (theme.value === 'auto') return window.matchMedia('(prefers-color-scheme: dark)').matches
  return false
})

// Load saved preferences for current apiType
const savedPrefs = loadSavedPreferences(props.apiType)

// State (initialized from saved preferences)
const selectedView = ref<ViewMode>(savedPrefs.view)
const selectedDuration = ref<Duration>(savedPrefs.duration)
const isLoading = ref(false)
const historyData = ref<{ dataPoints: GlobalHistoryDataPoint[], summary: GlobalStatsSummary | null, modelDataPoints?: Record<string, ModelHistoryDataPoint[]> } | null>(null)

const chartHeight = computed(() => (props.compact ? 180 : 260))

const durationOptions = computed(() => [
  { label: t('chart.1h'), value: '1h' as Duration },
  { label: t('chart.6h'), value: '6h' as Duration },
  { label: t('chart.24h'), value: '24h' as Duration },
  { label: t('chart.today'), value: 'today' as Duration },
  { label: t('chart.7d'), value: '7d' as Duration },
  { label: t('chart.30d'), value: '30d' as Duration },
  { label: t('chart.90d'), value: '90d' as Duration },
  { label: t('chart.180d'), value: '180d' as Duration },
  { label: t('chart.365d'), value: '365d' as Duration },
  { label: t('chart.thisyear'), value: 'thisyear' as Duration },
])

const viewOptions = computed(() => [
  { label: t('chart.traffic'), value: 'traffic' as ViewMode },
  { label: t('chart.tokens'), value: 'tokens' as ViewMode },
  { label: t('chart.cost'), value: 'cost' as ViewMode },
])

// Expose data for parent to update
const updateData = (data: GlobalHistoryDataPoint[], summary: GlobalStatsSummary | null, modelDataPoints?: Record<string, ModelHistoryDataPoint[]>) => {
  historyData.value = { dataPoints: data, summary, modelDataPoints }
}

const refreshData = () => {
  emit('refresh', selectedDuration.value)
}

const setLoading = (loading: boolean) => {
  isLoading.value = loading
}

// Watchers
watch(selectedDuration, (newVal) => {
  savePreference(props.apiType, 'duration', newVal)
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
  refreshData()
})

defineExpose({
  updateData,
  setLoading,
  refreshData,
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

const summary = computed<GlobalStatsSummary | null>(() => historyData.value?.summary || null)

const hasData = computed(() => {
  if (!historyData.value?.dataPoints) return false
  return historyData.value.dataPoints.length > 0 &&
    historyData.value.dataPoints.some(dp => dp.requestCount > 0)
})

const hasCacheData = computed(() => {
  if (!historyData.value?.dataPoints) return false
  return historyData.value.dataPoints.some(dp => (dp.cacheReadTokens || 0) > 0 || (dp.cacheCreationTokens || 0) > 0)
})

const sortedModels = computed(() => {
  const models = historyData.value?.modelDataPoints
  if (!models) return []
  return Object.entries(models)
    .map(([name, points]) => ({ name, points, total: points.reduce((s: number, p: ModelHistoryDataPoint) => s + p.requestCount, 0) }))
    .filter(m => m.total > 0)
    .sort((a, b) => b.total - a.total)
})

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
  const interval = effectiveChartIntervalMs(summary.value?.intervalSeconds, props.chartInterval)
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
  const minOpacity = 0.03
  const maxOpacity = 0.2
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
function formatNumber(num: number): string {
  if (num >= 1000000) return (num / 1000000).toFixed(1) + 'M'
  if (num >= 1000) return (num / 1000).toFixed(1) + 'K'
  return num.toFixed(0)
}

// Format cost (USD) for display
function formatCost(val: number): string {
  if (val >= 1000) return (val / 1000).toFixed(2) + 'K'
  if (val >= 1) return val.toFixed(2)
  return val.toFixed(4)
}

// Chart options
const chartOptions = computed<ApexOptions>(() => {
  const mode = selectedView.value
  const isMultiModelView = (mode === 'traffic' || mode === 'cost') && hasMultiModel.value

  // Traffic/cost mode colors: assign by model in multi-model mode, otherwise use a single color
  const multiModelColors = isMultiModelView
    ? sortedModels.value.map((_, i) => modelColors[i % modelColors.length])
    : [chartColors.traffic.primary]

  const textColor = isDark.value ? '#9ca3af' : '#6b7280'
  const gridBorder = isDark.value ? 'rgba(255,255,255,0.1)' : 'rgba(0,0,0,0.1)'
  const xLabelFormat = ['7d', '30d', '90d', '180d', '365d', 'thisyear'].includes(selectedDuration.value) ? 'MM-dd HH:mm' : 'HH:mm'

  return {
    chart: {
      toolbar: { show: false },
      zoom: { enabled: false },
      background: 'transparent',
      fontFamily: 'inherit',
      stacked: isMultiModelView,
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
      borderColor: gridBorder,
      padding: { left: 10, right: 10 }
    },
    xaxis: {
      type: 'datetime',
      min: xaxisMin.value,
      labels: {
        datetimeUTC: false,
        format: xLabelFormat,
        style: { fontSize: '11px', colors: textColor }
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
            style: { fontSize: '11px', colors: textColor }
          },
          min: 0
        },
        {
          seriesName: t('chart.outputTokens'),
          opposite: true,
          labels: {
            formatter: (val: number) => formatNumber(val),
            style: { fontSize: '11px', colors: textColor }
          },
          min: 0
        }
      ]
      if (hasCacheData.value) {
        axes.push(
          { seriesName: 'Cache Read', show: false, min: 0 },
          { seriesName: 'Cache Write', show: false, min: 0 }
        )
      }
      return axes
    })() : {
      labels: {
        formatter: (val: number) => mode === 'cost' ? '$' + formatCost(val) : Math.round(val).toString(),
        style: { fontSize: '11px', colors: textColor }
      },
      min: 0
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
      markers: { size: 4 },
      labels: { colors: textColor }
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
        name: t('chart.totalRequests'),
        data: dataPoints.map(dp => ({
          x: new Date(dp.timestamp).getTime(),
          y: dp.requestCount
        }))
      }
    ]
  } else if (mode === 'cost') {
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

onMounted(() => {
  refreshData()
})
</script>
