<template>
  <div class="key-trend-chart">
    <!-- Header: duration and view switcher -->
    <div class="flex flex-wrap items-center justify-between gap-2 mb-3">
      <div class="inline-flex rounded-md border border-border divide-x divide-border">
        <button
          v-for="opt in durationOptions"
          :key="opt.value"
          type="button"
          class="px-2 py-1 text-[11px] font-semibold transition-colors hover:bg-accent/40 disabled:opacity-50"
          :class="{ 'bg-accent text-accent-foreground': selectedDuration === opt.value }"
          :disabled="loading"
          @click="selectedDuration = opt.value"
        >
          {{ opt.label }}
        </button>
      </div>

      <div class="inline-flex rounded-md border border-border divide-x divide-border">
        <button
          v-for="view in viewOptions"
          :key="view.value"
          type="button"
          class="px-2 py-1 text-[11px] font-semibold transition-colors hover:bg-accent/40 flex items-center gap-1"
          :class="{ 'bg-accent text-accent-foreground': selectedView === view.value }"
          @click="selectedView = view.value"
        >
          <svg class="w-3.5 h-3.5" fill="none" stroke="currentColor" viewBox="0 0 24 24">
            <path v-if="view.value === 'traffic'" stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M7 12l3-3 3 3 4-4M8 21l4-4 4 4M3 4h18M4 4h16v12a1 1 0 01-1 1H5a1 1 0 01-1-1V4z" />
            <path v-else-if="view.value === 'tokens'" stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M16 8v8m-4-5v5m-4-2v2m-2 4h12a2 2 0 002-2V6a2 2 0 00-2-2H6a2 2 0 00-2 2v12a2 2 0 002 2z" />
            <path v-else-if="view.value === 'cache'" stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M4 4v5h.582m15.356 2A8.001 8.001 0 004.582 9m0 0H9m11 11v-5h-.581m0 0a8.003 8.003 0 01-15.357-2m15.357 2H15" />
            <path v-else stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M12 1v22M17 5H9.5a3.5 3.5 0 000 7h5a3.5 3.5 0 010 7H6" />
          </svg>
          {{ t(view.label) }}
        </button>
      </div>
    </div>

    <!-- Summary cards -->
    <div v-if="summary && !loading" class="flex flex-wrap gap-2 mb-3">
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

    <!-- Loading state -->
    <div v-if="loading" class="flex items-center justify-center" style="height: 200px">
      <div class="w-6 h-6 border-2 border-primary border-t-transparent rounded-full animate-spin" />
    </div>

    <!-- Empty state -->
    <div
      v-else-if="!hasData"
      class="flex flex-col items-center justify-center text-muted-foreground"
      style="height: 200px"
    >
      <div class="text-2xl mb-2 opacity-40">&#x1F517;</div>
      <div class="text-xs">{{ t('chart.noKeyUsageInRange') }}</div>
    </div>

    <!-- Chart -->
    <div v-else>
      <VueApexCharts
        ref="chartRef"
        :key="`key-trend-${selectedView}-${selectedDuration}`"
        type="area"
        height="280"
        :options="chartOptions"
        :series="chartSeries"
      />
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import VueApexCharts from 'vue3-apexcharts'
import type { ApexOptions } from 'apexcharts'
import { useTheme } from '@/composables/useTheme'
import { useI18n } from '@/i18n'
import type { KeyHistoryData, GlobalStatsSummary } from '@/services/admin-api'
import { effectiveChartIntervalMs } from '@/utils/chart-sampling'

const { t } = useI18n()
const props = withDefaults(
  defineProps<{
    data: KeyHistoryData[]
    channelName: string
    loading?: boolean
    duration?: string
    summary?: GlobalStatsSummary | null
    chartInterval?: string
  }>(),
  {
    loading: false,
    duration: '1h',
    summary: null,
    chartInterval: undefined,
  },
)

const emit = defineEmits<{
  refresh: [duration: string]
}>()

const { theme } = useTheme()
const chartRef = ref<InstanceType<typeof VueApexCharts> | null>(null)

const isDark = computed(() => {
  if (theme.value === 'dark') return true
  if (theme.value === 'auto') return window.matchMedia('(prefers-color-scheme: dark)').matches
  return false
})

const textColor = computed(() => (isDark.value ? '#9ca3af' : '#6b7280'))
const gridBorder = computed(() => (isDark.value ? 'rgba(255,255,255,0.1)' : 'rgba(0,0,0,0.1)'))

// Key color palette - supports up to 10 keys
const KEY_COLORS = [
  '#3b82f6', '#f97316', '#10b981', '#8b5cf6', '#ec4899',
  '#eab308', '#06b6d4', '#f43f5e', '#84cc16', '#6366f1',
]

// View and duration mode
type ViewMode = 'traffic' | 'tokens' | 'cache' | 'cost'
type Duration = '1h' | '6h' | '24h' | 'today' | '7d' | '30d' | '90d' | '180d' | '365d' | 'thisyear'
type SeriesDirection = 'in' | 'out'
type ChartSeriesPoint = { x: number; y: number }
type ChartSeriesMeta = {
  name: string
  color: string
  direction?: SeriesDirection
  data: ChartSeriesPoint[]
}
const isDuration = (value?: string): value is Duration => !!value && ['1h', '6h', '24h', 'today', '7d', '30d', '90d', '180d', '365d', 'thisyear'].includes(value)
const selectedView = ref<ViewMode>('traffic')
const selectedDuration = ref<Duration>(isDuration(props.duration) ? props.duration : '1h')

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
  { label: 'chart.traffic', value: 'traffic' as ViewMode },
  { label: 'chart.tokens', value: 'tokens' as ViewMode },
  { label: 'chart.cacheRw', value: 'cache' as ViewMode },
  { label: 'chart.cost', value: 'cost' as ViewMode },
])

watch(selectedDuration, (duration) => {
  emit('refresh', duration)
})

watch(() => props.duration, (duration) => {
  if (isDuration(duration) && duration !== selectedDuration.value) {
    selectedDuration.value = duration
  }
})

const hasData = computed(() => {
  if (!props.data?.length) return false
  const mode = selectedView.value
  return props.data.some(keyData => {
    if (!keyData.dataPoints?.length) return false
    return keyData.dataPoints.some(dp => {
      if (mode === 'traffic') return dp.requestCount > 0
      if (mode === 'cost') return (dp.costUSD || 0) > 0
      if (mode === 'tokens') return dp.inputTokens > 0 || dp.outputTokens > 0
      return dp.cacheReadTokens > 0 || dp.cacheCreationTokens > 0
    })
  })
})

const xLabelFormat = computed(() =>
  ['7d', '30d', '90d', '180d', '365d', 'thisyear'].includes(selectedDuration.value) ? 'MM-dd HH:mm' : 'HH:mm',
)

// Failure rate background bands
const FAILURE_RATE_THRESHOLD = 0.1

const AGGREGATION_INTERVALS: Record<string, number> = {
  '1h': 60000,
  '6h': 300000,
  '24h': 900000,
  'today': 300000,
  '7d': 3600000,
  '30d': 14400000,
  '90d': 43200000,
  '180d': 86400000,
  '365d': 172800000,
  'thisyear': 43200000,
}

const getAggregationInterval = (duration: string): number => {
  const interval = effectiveChartIntervalMs(props.summary?.intervalSeconds, props.chartInterval)
  if (interval) return interval
  return AGGREGATION_INTERVALS[duration] || 60000
}

const firstNonEmptyTimestamp = computed(() => {
  if (!props.data?.length) return undefined
  let earliest = Infinity
  props.data.forEach(keyData => {
    if (!keyData.dataPoints?.length) return
    keyData.dataPoints.forEach(dp => {
      const hasVisibleData = selectedView.value === 'traffic'
        ? dp.requestCount > 0
        : selectedView.value === 'cost'
          ? (dp.costUSD || 0) > 0
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
  return ts - interval
})


const getFailureOpacity = (failureRate: number): number => {
  const minOpacity = 0.03
  const maxOpacity = 0.2
  const normalizedRate = Math.min((failureRate - FAILURE_RATE_THRESHOLD) / (1 - FAILURE_RATE_THRESHOLD), 1)
  return minOpacity + normalizedRate * (maxOpacity - minOpacity)
}

const failureAnnotations = computed(() => {
  if (selectedView.value !== 'traffic') return []
  if (!props.data?.length) return []

  const interval = getAggregationInterval(selectedDuration.value)

  // Aggregate all keys by aligned timestamp
  const timeMap = new Map<number, { totalRequests: number; totalFailures: number }>()
  for (const keyData of props.data) {
    for (const dp of keyData.dataPoints || []) {
      const rawTs = new Date(dp.timestamp).getTime()
      const alignedTs = Math.floor(rawTs / interval) * interval
      const existing = timeMap.get(alignedTs) || { totalRequests: 0, totalFailures: 0 }
      existing.totalRequests += dp.requestCount
      existing.totalFailures += dp.failureCount
      timeMap.set(alignedTs, existing)
    }
  }

  const rates = Array.from(timeMap.entries())
    .map(([timestamp, d]) => ({
      timestamp,
      failureRate: d.totalRequests > 0 ? d.totalFailures / d.totalRequests : 0,
    }))
    .sort((a, b) => a.timestamp - b.timestamp)

  const MAX_INTERVAL = interval * 2

  return rates
    .filter(p => p.failureRate >= FAILURE_RATE_THRESHOLD)
    .map((point, index, arr) => {
      let pointInterval = interval
      if (arr.length > 1) {
        if (index > 0) {
          pointInterval = point.timestamp - arr[index - 1].timestamp
        } else if (index < arr.length - 1) {
          pointInterval = arr[index + 1].timestamp - point.timestamp
        }
      }
      pointInterval = Math.min(pointInterval, MAX_INTERVAL)

      return {
        x: point.timestamp - pointInterval / 2,
        x2: point.timestamp + pointInterval / 2,
        fillColor: '#ef4444',
        opacity: getFailureOpacity(point.failureRate),
        borderColor: 'transparent',
        borderWidth: 0,
        label: { text: '' },
      }
    })
})

// Helper: format number abbreviation
function formatNumber(num: number): string {
  if (num >= 1000000) return (num / 1000000).toFixed(1) + 'M'
  if (num >= 1000) return (num / 1000).toFixed(1) + 'K'
  return num.toFixed(0)
}

// Helper: format cost (USD) for display
function formatCost(val: number): string {
  if (val >= 1000) return (val / 1000).toFixed(2) + 'K'
  if (val >= 1) return val.toFixed(2)
  return val.toFixed(4)
}

// Helper: format axis value based on view mode
function formatAxisValue(val: number, mode: ViewMode): string {
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
function formatTooltipValue(val: number, mode: ViewMode): string {
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

// Helper: get display name for a key
function getDisplayName(keyData: KeyHistoryData): string {
  return keyData.model ? `${keyData.keyMask}/${keyData.model}` : keyData.keyMask
}

function getSeriesColor(keyData: KeyHistoryData, index: number): string {
  return keyData.color || KEY_COLORS[index % KEY_COLORS.length]
}

function buildChartSeries(): ChartSeriesMeta[] {
  if (!props.data?.length) return []
  const mode = selectedView.value

  if (mode === 'traffic' || mode === 'cost') {
    return props.data
      .filter(keyData => keyData.dataPoints.reduce((sum, dp) => sum + (mode === 'cost' ? (dp.costUSD || 0) : dp.requestCount), 0) > 0)
      .map((keyData, index) => {
        const keyName = getDisplayName(keyData)
        return {
          name: keyName,
          color: getSeriesColor(keyData, index),
          data: keyData.dataPoints.map(dp => ({
            x: new Date(dp.timestamp).getTime(),
            y: mode === 'cost' ? (dp.costUSD || 0) : dp.requestCount,
          })),
        }
      })
  }

  const result: ChartSeriesMeta[] = []
  const inLabel = mode === 'tokens' ? t('chart.input') : t('chart.cacheRead')
  const outLabel = mode === 'tokens' ? t('chart.output') : t('chart.cacheWrite')

  props.data.forEach((keyData, index) => {
    const keyName = getDisplayName(keyData)
    const color = getSeriesColor(keyData, index)
    const inTotal = keyData.dataPoints.reduce((sum, dp) => sum + (mode === 'tokens' ? dp.inputTokens : dp.cacheReadTokens), 0)
    const outTotal = keyData.dataPoints.reduce((sum, dp) => sum + (mode === 'tokens' ? dp.outputTokens : dp.cacheCreationTokens), 0)

    if (inTotal > 0) {
      result.push({
        name: `${keyName} ${inLabel}`,
        color,
        direction: 'in',
        data: keyData.dataPoints.map(dp => ({
          x: new Date(dp.timestamp).getTime(),
          y: mode === 'tokens' ? dp.inputTokens : dp.cacheReadTokens,
        })),
      })
    }

    if (outTotal > 0) {
      result.push({
        name: `${keyName} ${outLabel}`,
        color,
        direction: 'out',
        data: keyData.dataPoints.map(dp => ({
          x: new Date(dp.timestamp).getTime(),
          y: mode === 'tokens' ? dp.outputTokens : dp.cacheCreationTokens,
        })),
      })
    }
  })

  return result
}

const chartSeriesData = computed(() => buildChartSeries())
const chartSeries = computed(() => chartSeriesData.value.map(({ name, data }) => ({ name, data })))

// Helper: get dash array (solid for forward, dashed for reverse)
function getDashArray(): number | number[] {
  if (selectedView.value === 'traffic' || selectedView.value === 'cost') return 0
  const dashArray = chartSeriesData.value.map(series => series.direction === 'out' ? 5 : 0)
  return dashArray.length > 0 ? dashArray : 0
}

// Helper: get chart colors (duplicate for bidirectional mode)
function getChartColors(): string[] {
  const series = chartSeriesData.value
  return series.length > 0 ? series.map(item => item.color) : KEY_COLORS
}

// Token/cache mode: determine Y-axis anchor series names
function getYaxisConfig() {
  const mode = selectedView.value
  if (mode === 'traffic' || mode === 'cost') {
    return {
      labels: {
        formatter: (val: number) => formatAxisValue(val, mode),
        style: { fontSize: '11px', colors: textColor.value },
      },
      min: 0,
      forceNiceScale: true,
    }
  }

  const inNames = chartSeriesData.value.filter(item => item.direction === 'in').map(item => item.name)
  const outNames = chartSeriesData.value.filter(item => item.direction === 'out').map(item => item.name)
  const primaryInName = inNames[0] || outNames[0]
  if (!primaryInName) {
    return {
      labels: {
        formatter: (val: number) => formatAxisValue(val, mode),
        style: { fontSize: '11px', colors: textColor.value },
      },
      min: 0,
      forceNiceScale: true,
    }
  }

  const axes: any[] = [
    {
      seriesName: primaryInName,
      show: true,
      labels: {
        formatter: (val: number) => formatAxisValue(val, mode),
        style: { fontSize: '11px', colors: textColor.value },
      },
      min: 0,
      forceNiceScale: true,
    },
  ]

  if (inNames[0] && outNames[0]) {
    axes.push({
      seriesName: outNames[0],
      opposite: true,
      show: true,
      labels: {
        formatter: (val: number) => formatAxisValue(val, mode),
        style: { fontSize: '11px', colors: textColor.value },
      },
      min: 0,
      forceNiceScale: true,
    })
  }

  inNames.slice(1).forEach(name => axes.push({ seriesName: name, show: false, min: 0, forceNiceScale: true }))
  outNames.slice(inNames[0] ? 1 : 0).forEach(name => axes.push({ seriesName: name, show: false, min: 0, forceNiceScale: true }))

  return axes
}

const chartOptions = computed<ApexOptions>(() => {
  const keyCount = props.data?.length || 0

  return {
    chart: {
      toolbar: { show: false },
      zoom: { enabled: false },
      background: 'transparent',
      fontFamily: 'inherit',
      defaultLocale: 'en',
      stacked: selectedView.value === 'traffic' || selectedView.value === 'cost',
      animations: { enabled: false },
    },
    theme: { mode: isDark.value ? 'dark' : 'light' },
    colors: getChartColors(),
    fill: {
      type: 'gradient',
      gradient: { shadeIntensity: 1, opacityFrom: 0.4, opacityTo: 0.08, stops: [0, 90, 100] },
    },
    dataLabels: { enabled: false },
    stroke: { curve: 'smooth', width: 2, dashArray: getDashArray() },
    grid: { borderColor: gridBorder.value, padding: { left: 10, right: 10 } },
    xaxis: {
      type: 'datetime',
      min: xaxisMin.value,
      labels: {
        datetimeUTC: false,
        format: xLabelFormat.value,
        style: { fontSize: '11px', colors: textColor.value },
      },
      axisBorder: { show: false },
      axisTicks: { show: false },
    },
    yaxis: getYaxisConfig(),
    tooltip: {
      x: { format: 'MM-dd HH:mm' },
      y: { formatter: (val: number) => formatTooltipValue(val, selectedView.value) },
      custom: selectedView.value === 'traffic' && keyCount > 1 ? buildTrafficTooltip : undefined,
    },
    annotations: { xaxis: failureAnnotations.value },
    legend: {
      show: selectedView.value !== 'traffic' || keyCount > 1,
      position: 'top',
      horizontalAlign: 'right',
      fontSize: '11px',
      markers: { size: 4 },
      labels: { colors: textColor.value },
    },
  }
})

// Custom tooltip for traffic mode
const buildTrafficTooltip = ({ seriesIndex, dataPointIndex, w }: any): string => {
  if (!props.data?.length) return ''

  const timestamp = w.globals.seriesX[seriesIndex][dataPointIndex]
  const date = new Date(timestamp)
  const timeStr = date.toLocaleString('zh-CN', {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
  })

  const interval = getAggregationInterval(selectedDuration.value)
  const alignedTimestamp = Math.floor(timestamp / interval) * interval

  interface KeyStat {
    displayName: string
    total: number
    failure: number
    color: string
  }

  const keyStats: KeyStat[] = []
  let grandTotal = 0
  let grandFailure = 0

  const escapeHtml = (str: string): string =>
    str.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;').replace(/"/g, '&quot;')

  props.data.forEach((keyData, keyIndex) => {
    const matchingPoints = (keyData.dataPoints || []).filter(p => {
      const dpTs = new Date(p.timestamp).getTime()
      return Math.floor(dpTs / interval) === Math.floor(alignedTimestamp / interval)
    })

    if (matchingPoints.length > 0) {
      const aggregated = matchingPoints.reduce(
        (acc, dp) => ({
          success: acc.success + dp.successCount,
          failure: acc.failure + dp.failureCount,
          total: acc.total + dp.requestCount,
        }),
        { success: 0, failure: 0, total: 0 },
      )

      if (aggregated.total > 0) {
        keyStats.push({
          displayName: escapeHtml(getDisplayName(keyData)),
          total: aggregated.total,
          failure: aggregated.failure,
          color: getSeriesColor(keyData, keyIndex),
        })
        grandTotal += aggregated.total
        grandFailure += aggregated.failure
      }
    }
  })

  if (keyStats.length === 0) return ''

  const hasFailure = grandFailure > 0
  let html = `<div style="padding: 8px 12px; font-size: 13px; line-height: 1.6; font-family: var(--font-sans);">`
  html += `<div style="font-weight: 600; margin-bottom: 6px; color: ${hasFailure ? '#ef4444' : 'inherit'};">${timeStr}</div>`

  keyStats.forEach(stat => {
    const failureRate = stat.total > 0 ? ((stat.failure / stat.total) * 100).toFixed(0) : '0'
    html += `<div style="display: flex; align-items: center; margin: 4px 0;">`
    html += `<span style="width: 10px; height: 10px; border-radius: 50%; background: ${stat.color}; margin-right: 6px;"></span>`
    html += `<span style="flex: 1;">${stat.displayName}</span>`
    html += `<span style="margin-left: 12px; font-weight: 500;">${stat.total} ${t('chart.requestUnit')}</span>`
    if (stat.failure > 0) {
      html += `<span style="margin-left: 6px; color: #ef4444; font-size: 12px;">(${stat.failure} ${t('chart.issueCount')}, ${failureRate}%)</span>`
    }
    html += `</div>`
  })

  if (keyStats.length > 1) {
    const grandFailureRate = grandTotal > 0 ? ((grandFailure / grandTotal) * 100).toFixed(1) : '0'
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

defineExpose({ chartRef })
</script>
