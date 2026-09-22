<script setup lang="ts">
// 成本报表 tab：Wave 2 填充完整实现（筛选/汇总卡/分组表/CSV 导出）。
// 移植自 web 端 CostReportView.vue，按桌面端 shadcn 风格重写。
import { computed, onMounted, ref, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { Badge } from '@/components/ui/badge'
import { Skeleton } from '@/components/ui/skeleton'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from '@/components/ui/select'
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from '@/components/ui/table'
import { CircleAlert, Coins, Download, RefreshCw, Server } from 'lucide-vue-next'
import { useLanguage } from '@/composables/useLanguage'
import { useAdminApi } from '@/composables/useAdminApi'
import { COST_REPORT_PATH } from '@/services/admin-api'
import type { CostReportRow, CostReportResponse } from '@/services/admin-api'

const { t } = useLanguage()
const adminApi = useAdminApi()

const loading = ref(false)
const loadError = ref('')
const rows = ref<CostReportRow[]>([])
const groupBy = ref<'user' | 'model' | 'key'>('user')
const duration = ref('7d')
const apiType = ref('messages')

const groupByOptions = computed(() => [
  { label: t('costReport.groupBy.user'), value: 'user' as const },
  { label: t('costReport.groupBy.model'), value: 'model' as const },
  { label: t('costReport.groupBy.key'), value: 'key' as const },
])

const durationOptions = ['24h', '7d', '30d', '90d', '365d'] as const

const apiTypeOptions = computed(() => [
  { label: t('costReport.apiType.messages'), value: 'messages' },
  { label: t('costReport.apiType.responses'), value: 'responses' },
  { label: t('costReport.apiType.chat'), value: 'chat' },
  { label: t('costReport.apiType.gemini'), value: 'gemini' },
  { label: t('costReport.apiType.images'), value: 'images' },
  { label: t('costReport.apiType.vectors'), value: 'vectors' },
])

const groupByLabel = computed(() => {
  return groupByOptions.value.find(o => o.value === groupBy.value)?.label || t('costReport.groupByLabel')
})

const totalRequests = computed(() => rows.value.reduce((s, r) => s + r.totalRequests, 0))
const totalSuccess = computed(() => rows.value.reduce((s, r) => s + r.successCount, 0))
const totalInputTokens = computed(() => rows.value.reduce((s, r) => s + r.inputTokens, 0))
const totalListCostUSD = computed(() => rows.value.reduce((s, r) => s + r.listCostUSD, 0))
const totalCompressionSaved = computed(() =>
  rows.value.reduce((s, r) => s + ((r.originalTokensSaved ?? 0) - (r.compressedTokensAfter ?? 0)), 0))
const pricingComplete = computed(() => rows.value.every(isPricingComplete))
const successRate = computed(() => {
  if (totalRequests.value === 0) return '0.0'
  return ((totalSuccess.value / totalRequests.value) * 100).toFixed(1)
})

function formatNumber(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`
  return `${n}`
}

function formatTokens(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(2)}M`
  if (n >= 1_000) return `${(n / 1_000).toFixed(1)}K`
  return `${n}`
}

function isPricingComplete(row: CostReportRow): boolean {
  return row.pricingComplete !== false
}

function formatCost(costUSD: number, complete: boolean, decimals = 6): string {
  return `$${costUSD.toFixed(decimals)}${complete ? '' : '+'}`
}

function pricingHint(row: CostReportRow): string {
  if (row.unpricedModels?.length) {
    return t('costReport.pricingHintModels', { models: row.unpricedModels.join(t('costReport.modelSeparator')) })
  }
  return t('costReport.pricingHintGeneric')
}

function rowSuccessRate(row: CostReportRow): string {
  if (!row.totalRequests) return '0.0'
  return ((row.successCount / row.totalRequests) * 100).toFixed(1)
}

async function fetchReport() {
  loading.value = true
  loadError.value = ''
  try {
    const params = new URLSearchParams({
      groupBy: groupBy.value,
      duration: duration.value,
      type: apiType.value,
    })
    const resp = await adminApi.get<CostReportResponse>(`${COST_REPORT_PATH}?${params.toString()}`)
    rows.value = resp.rows || []
  } catch (e) {
    loadError.value = e instanceof Error ? e.message : String(e)
    rows.value = []
  } finally {
    loading.value = false
  }
}

// 筛选变化即重新拉取
watch([groupBy, duration, apiType], () => {
  void fetchReport()
})

function exportCSV() {
  if (rows.value.length === 0) return

  const headers = [
    groupByLabel.value,
    t('costReport.csv.requests'),
    t('costReport.csv.successCount'),
    t('costReport.csv.inputTokens'),
    t('costReport.csv.outputTokens'),
    t('costReport.csv.cacheCreationTokens'),
    t('costReport.csv.cacheReadTokens'),
    t('costReport.csv.compressionOriginalTokens'),
    t('costReport.csv.compressionSavedTokens'),
    t('costReport.csv.listCostUsd'),
    t('costReport.csv.pricingStatus'),
    t('costReport.csv.unpricedModels'),
    t('costReport.csv.zeroCostCount'),
    t('costReport.csv.configuredMultiplierCount'),
    t('costReport.csv.subscriptionCostCount'),
    t('costReport.csv.unpricedCostCount'),
  ]
  const csvRows = rows.value.map(r => [
    r.groupKey, r.totalRequests, r.successCount,
    r.inputTokens, r.outputTokens, r.cacheCreationTokens,
    r.cacheReadTokens,
    (r.originalTokensSaved ?? 0),
    ((r.originalTokensSaved ?? 0) - (r.compressedTokensAfter ?? 0)),
    r.listCostUSD.toFixed(6),
    isPricingComplete(r) ? t('costReport.pricingStatus.complete') : t('costReport.pricingStatus.partial'),
    r.unpricedModels?.join(' | ') || '',
    r.zeroCostCount || 0,
    r.configuredMultiplierCount || 0,
    r.subscriptionCostCount || 0,
    r.unpricedCostCount || 0,
  ])

  const csv = [headers.join(','), ...csvRows.map(r => r.join(','))].join('\n')
  // BOM 前缀保证 Excel 正确识别 UTF-8
  const blob = new Blob(['﻿' + csv], { type: 'text/csv;charset=utf-8;' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `cost-report-${groupBy.value}-${apiType.value}-${duration.value}.csv`
  a.click()
  URL.revokeObjectURL(url)
}

onMounted(() => {
  void fetchReport()
})
</script>

<template>
  <div class="mx-auto flex w-full max-w-[1680px] flex-col gap-4 p-1">
    <!-- Header -->
    <div class="flex items-center justify-between gap-3">
      <div class="flex items-center gap-2">
        <Coins class="h-5 w-5 text-primary" />
        <h3 class="text-base font-bold text-foreground">{{ t('costReport.title') }}</h3>
      </div>
      <div class="flex items-center gap-2">
        <Button variant="outline" size="sm" :disabled="loading" @click="fetchReport">
          <RefreshCw class="h-3.5 w-3.5" :class="{ 'animate-spin': loading }" />
          {{ t('costReport.refresh') }}
        </Button>
        <Button variant="outline" size="sm" :disabled="rows.length === 0" @click="exportCSV">
          <Download class="h-3.5 w-3.5" />
          {{ t('costReport.exportCsv') }}
        </Button>
      </div>
    </div>

    <!-- Filter bar -->
    <div class="flex flex-wrap items-center gap-x-5 gap-y-2 border border-border bg-card/60 px-3 py-2.5">
      <div class="flex items-center gap-1.5">
        <span class="text-[10px] font-bold uppercase tracking-[0.18em] text-muted-foreground">{{ t('costReport.groupByLabel') }}</span>
        <div class="flex items-center gap-1">
          <Button
            v-for="opt in groupByOptions"
            :key="opt.value"
            size="sm"
            :variant="groupBy === opt.value ? 'default' : 'outline'"
            class="h-7 px-2.5 text-xs"
            @click="groupBy = opt.value"
          >
            {{ opt.label }}
          </Button>
        </div>
      </div>

      <div class="flex items-center gap-1.5">
        <span class="text-[10px] font-bold uppercase tracking-[0.18em] text-muted-foreground">{{ t('costReport.durationLabel') }}</span>
        <div class="flex items-center gap-1">
          <Button
            v-for="opt in durationOptions"
            :key="opt"
            size="sm"
            :variant="duration === opt ? 'default' : 'outline'"
            class="h-7 px-2.5 font-mono text-xs"
            @click="duration = opt"
          >
            {{ opt }}
          </Button>
        </div>
      </div>

      <div class="flex items-center gap-1.5">
        <span class="text-[10px] font-bold uppercase tracking-[0.18em] text-muted-foreground">API</span>
        <Select v-model="apiType">
          <SelectTrigger class="h-7 w-[150px] text-xs">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem v-for="opt in apiTypeOptions" :key="opt.value" :value="opt.value" class="text-xs">
              {{ opt.label }}
            </SelectItem>
          </SelectContent>
        </Select>
      </div>
    </div>

    <!-- Summary cards -->
    <div class="grid grid-cols-2 gap-3 md:grid-cols-3 xl:grid-cols-5">
      <div class="border border-border bg-card/60 px-3 py-2.5">
        <div class="text-[10px] font-bold uppercase tracking-[0.18em] text-muted-foreground">{{ t('costReport.totalRequests') }}</div>
        <div class="mt-1 font-mono text-xl font-bold text-foreground">{{ formatNumber(totalRequests) }}</div>
      </div>
      <div class="border border-border bg-card/60 px-3 py-2.5">
        <div class="text-[10px] font-bold uppercase tracking-[0.18em] text-muted-foreground">{{ t('costReport.successRate') }}</div>
        <div class="mt-1 font-mono text-xl font-bold" :class="Number(successRate) >= 95 ? 'text-emerald-600 dark:text-emerald-300' : 'text-amber-600 dark:text-amber-300'">
          {{ successRate }}%
        </div>
      </div>
      <div class="border border-border bg-card/60 px-3 py-2.5">
        <div class="text-[10px] font-bold uppercase tracking-[0.18em] text-muted-foreground">{{ t('costReport.totalInputTokens') }}</div>
        <div class="mt-1 font-mono text-xl font-bold text-foreground">{{ formatTokens(totalInputTokens) }}</div>
      </div>
      <div class="border border-border bg-card/60 px-3 py-2.5">
        <div class="text-[10px] font-bold uppercase tracking-[0.18em] text-muted-foreground">{{ t('costReport.totalListCost') }}</div>
        <div class="mt-1 flex items-baseline gap-1 font-mono text-xl font-bold" :class="pricingComplete ? 'text-foreground' : 'text-amber-600 dark:text-amber-300'">
          {{ formatCost(totalListCostUSD, pricingComplete, 4) }}
        </div>
        <div v-if="!pricingComplete" class="mt-0.5 flex items-center gap-1 text-[10px] text-amber-600 dark:text-amber-300">
          <CircleAlert class="h-3 w-3 shrink-0" />
          {{ t('costReport.incompletePricingHint') }}
        </div>
      </div>
      <div class="border border-border bg-card/60 px-3 py-2.5" :title="t('costReport.compressionsSavingHint')">
        <div class="text-[10px] font-bold uppercase tracking-[0.18em] text-muted-foreground">{{ t('costReport.col.compressionSavings') }}</div>
        <div class="mt-1 font-mono text-xl font-bold text-emerald-600 dark:text-emerald-300">{{ formatTokens(totalCompressionSaved) }}</div>
      </div>
    </div>

    <!-- Loading state -->
    <div v-if="loading && rows.length === 0" class="space-y-2">
      <Skeleton v-for="i in 6" :key="i" class="h-10 w-full" />
    </div>

    <!-- Error state -->
    <div v-else-if="loadError" class="flex items-center gap-2 border border-destructive/40 bg-destructive/5 px-3 py-2.5 text-sm text-destructive">
      <CircleAlert class="h-4 w-4 shrink-0" />
      {{ loadError }}
    </div>

    <!-- Empty state -->
    <div v-else-if="rows.length === 0" class="flex flex-col items-center gap-2 border border-dashed border-border bg-card/40 py-14 text-center">
      <Server class="h-10 w-10 text-muted-foreground/50" />
      <div class="text-sm font-semibold text-foreground">{{ t('costReport.emptyTitle') }}</div>
      <div class="text-xs text-muted-foreground">{{ t('costReport.emptyHint') }}</div>
    </div>

    <!-- Data table -->
    <div v-else class="overflow-x-auto border border-border bg-card/60">
      <Table>
        <TableHeader>
          <TableRow class="hover:bg-transparent">
            <TableHead class="min-w-[180px]">{{ groupByLabel }}</TableHead>
            <TableHead class="text-right">{{ t('costReport.col.requests') }}</TableHead>
            <TableHead class="text-right">{{ t('costReport.col.successRate') }}</TableHead>
            <TableHead class="text-right">{{ t('costReport.col.inputTokens') }}</TableHead>
            <TableHead class="text-right">{{ t('costReport.col.outputTokens') }}</TableHead>
            <TableHead class="text-right">{{ t('costReport.col.cacheCreation') }}</TableHead>
            <TableHead class="text-right">{{ t('costReport.col.cacheRead') }}</TableHead>
            <TableHead class="text-right">{{ t('costReport.col.compressionSavings') }}</TableHead>
            <TableHead class="text-right">{{ t('costReport.col.listCost') }}</TableHead>
            <TableHead class="min-w-[220px]">{{ t('costReport.col.costBreakdown') }}</TableHead>
          </TableRow>
        </TableHeader>
        <TableBody>
          <TableRow v-for="row in rows" :key="row.groupKey">
            <TableCell>
              <Badge variant="outline" class="max-w-[220px] truncate border-primary/25 bg-primary/5 font-normal text-primary">
                {{ row.groupKey || t('costReport.emptyGroup') }}
              </Badge>
            </TableCell>
            <TableCell class="text-right font-mono text-xs">{{ formatNumber(row.totalRequests) }}</TableCell>
            <TableCell class="text-right font-mono text-xs">
              <span :class="row.totalRequests && row.successCount / row.totalRequests >= 0.95 ? 'text-emerald-600 dark:text-emerald-300' : 'text-amber-600 dark:text-amber-300'">
                {{ rowSuccessRate(row) }}%
              </span>
            </TableCell>
            <TableCell class="text-right font-mono text-xs">{{ formatTokens(row.inputTokens) }}</TableCell>
            <TableCell class="text-right font-mono text-xs">{{ formatTokens(row.outputTokens) }}</TableCell>
            <TableCell class="text-right font-mono text-xs">{{ formatTokens(row.cacheCreationTokens) }}</TableCell>
            <TableCell class="text-right font-mono text-xs">{{ formatTokens(row.cacheReadTokens) }}</TableCell>
            <TableCell class="text-right font-mono text-xs">
              <span v-if="(row.originalTokensSaved ?? 0) > 0" class="text-emerald-600 dark:text-emerald-300">
                {{ formatTokens((row.originalTokensSaved ?? 0) - (row.compressedTokensAfter ?? 0)) }}
              </span>
              <span v-else class="text-muted-foreground">-</span>
            </TableCell>
            <TableCell class="text-right font-mono text-xs font-bold">
              <Tooltip>
                <TooltipTrigger as-child>
                  <span :class="{ 'cursor-help text-amber-600 dark:text-amber-300': !isPricingComplete(row) }">
                    {{ formatCost(row.listCostUSD, isPricingComplete(row)) }}
                  </span>
                </TooltipTrigger>
                <TooltipContent v-if="!isPricingComplete(row)" side="top" class="max-w-[280px] text-xs">
                  {{ pricingHint(row) }}
                </TooltipContent>
              </Tooltip>
            </TableCell>
            <TableCell>
              <div class="flex flex-wrap gap-1">
                <Badge
                  v-if="(row.zeroCostCount || 0) > 0"
                  class="border-emerald-500/25 bg-emerald-500/10 font-normal text-emerald-700 dark:text-emerald-300"
                  :title="t('costReport.costBreakdown.zeroCost')"
                >
                  {{ t('costReport.costBreakdown.zeroCost') }} {{ formatNumber(row.zeroCostCount || 0) }}
                </Badge>
                <Badge
                  v-if="(row.configuredMultiplierCount || 0) > 0"
                  class="border-blue-500/25 bg-blue-500/10 font-normal text-blue-700 dark:text-blue-300"
                  :title="t('costReport.costBreakdown.configuredMultiplier')"
                >
                  {{ t('costReport.costBreakdown.configuredMultiplier') }} {{ formatNumber(row.configuredMultiplierCount || 0) }}
                </Badge>
                <Badge
                  v-if="(row.subscriptionCostCount || 0) > 0"
                  class="border-sky-500/25 bg-sky-500/10 font-normal text-sky-700 dark:text-sky-300"
                  :title="t('costReport.costBreakdown.subscription')"
                >
                  {{ t('costReport.costBreakdown.subscription') }} {{ formatNumber(row.subscriptionCostCount || 0) }}
                </Badge>
                <Badge
                  v-if="(row.unpricedCostCount || 0) > 0"
                  class="border-amber-500/25 bg-amber-500/10 font-normal text-amber-700 dark:text-amber-300"
                  :title="t('costReport.costBreakdown.unpriced')"
                >
                  {{ t('costReport.costBreakdown.unpriced') }} {{ formatNumber(row.unpricedCostCount || 0) }}
                </Badge>
              </div>
            </TableCell>
          </TableRow>
        </TableBody>
      </Table>
    </div>
  </div>
</template>
