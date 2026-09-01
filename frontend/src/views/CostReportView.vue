<template>
  <div class="cost-report-view">
    <!-- Header -->
    <div class="d-flex align-center justify-space-between mb-4">
      <div class="d-flex align-center">
        <v-icon size="28" class="mr-2" color="primary">mdi-cash-multiple</v-icon>
        <span class="text-h5 font-weight-bold">{{ t('costReport.title') }}</span>
      </div>
      <div class="d-flex ga-2">
        <v-btn
          variant="tonal"
          prepend-icon="mdi-refresh"
          :loading="loading"
          @click="fetchReport"
        >
          {{ t('costReport.refresh') }}
        </v-btn>
        <v-btn
          variant="tonal"
          prepend-icon="mdi-download"
          :disabled="rows.length === 0"
          @click="exportCSV"
        >
          {{ t('costReport.exportCsv') }}
        </v-btn>
      </div>
    </div>

    <!-- Filter bar -->
    <v-card class="mb-4" variant="outlined">
      <v-card-text class="d-flex align-center ga-4 flex-wrap">
        <!-- groupBy selector -->
        <div class="d-flex align-center ga-1">
          <span class="text-caption text-medium-emphasis mr-1">{{ t('costReport.groupByLabel') }}</span>
          <v-chip
            v-for="opt in groupByOptions"
            :key="opt.value"
            :color="groupBy === opt.value ? 'primary' : undefined"
            :variant="groupBy === opt.value ? 'flat' : 'outlined'"
            size="small"
            @click="groupBy = opt.value; fetchReport()"
          >
            {{ opt.label }}
          </v-chip>
        </div>

        <!-- duration selector -->
        <div class="d-flex align-center ga-1">
          <span class="text-caption text-medium-emphasis mr-1">{{ t('costReport.durationLabel') }}</span>
          <v-chip
            v-for="opt in durationOptions"
            :key="opt.value"
            :color="duration === opt.value ? 'primary' : undefined"
            :variant="duration === opt.value ? 'flat' : 'outlined'"
            size="small"
            @click="duration = opt.value; fetchReport()"
          >
            {{ opt.label }}
          </v-chip>
        </div>

        <!-- apiType selector -->
        <v-select
          v-model="apiType"
          :items="apiTypeOptions"
          item-title="label"
          item-value="value"
          variant="outlined"
          density="compact"
          hide-details
          style="max-width: 180px"
          @update:model-value="fetchReport"
        />
      </v-card-text>
    </v-card>

    <!-- Summary cards -->
    <v-row class="mb-4">
      <v-col cols="6" sm="3">
        <v-card variant="outlined" class="pa-3 text-center">
          <div class="text-caption text-medium-emphasis">{{ t('costReport.totalRequests') }}</div>
          <div class="text-h6 font-weight-bold">{{ formatNumber(totalRequests) }}</div>
        </v-card>
      </v-col>
      <v-col cols="6" sm="3">
        <v-card variant="outlined" class="pa-3 text-center">
          <div class="text-caption text-medium-emphasis">{{ t('costReport.successRate') }}</div>
          <div class="text-h6 font-weight-bold">{{ successRate }}%</div>
        </v-card>
      </v-col>
      <v-col cols="6" sm="3">
        <v-card variant="outlined" class="pa-3 text-center">
          <div class="text-caption text-medium-emphasis">{{ t('costReport.totalInputTokens') }}</div>
          <div class="text-h6 font-weight-bold">{{ formatTokens(totalInputTokens) }}</div>
        </v-card>
      </v-col>
      <v-col cols="6" sm="3">
        <v-card variant="outlined" class="pa-3 text-center">
          <div class="text-caption text-medium-emphasis">{{ t('costReport.totalListCost') }}</div>
          <div :class="['text-h6 font-weight-bold', { 'text-warning': !pricingComplete }]">
            {{ formatCost(totalListCostUSD, pricingComplete, 4) }}
          </div>
          <div v-if="!pricingComplete" class="text-caption text-warning">{{ t('costReport.incompletePricingHint') }}</div>
        </v-card>
      </v-col>
      <v-col cols="6" sm="3">
        <v-card variant="outlined" class="pa-3 text-center">
          <div class="text-caption text-medium-emphasis" :title="t('costReport.compressionsSavingHint')">
            {{ t('costReport.col.compressionSavings') }}
          </div>
          <div class="text-h6 font-weight-bold text-success">{{ formatTokens(totalCompressionSaved) }}</div>
          <div v-if="totalCompressionSaved > 0" class="text-caption text-medium-emphasis">
            {{ formatTokens(totalCompressionSaved) }}
          </div>
        </v-card>
      </v-col>
    </v-row>

    <!-- Loading state -->
    <div v-if="loading && rows.length === 0" class="text-center py-12">
      <v-progress-circular indeterminate color="primary" size="48" />
    </div>

    <!-- Empty state -->
    <div v-else-if="!loading && rows.length === 0" class="text-center py-12 text-medium-emphasis">
      <v-icon size="64" class="mb-4" color="grey">mdi-cash-multiple</v-icon>
      <div class="text-body-1">{{ t('costReport.emptyTitle') }}</div>
      <div class="text-caption mt-1">{{ t('costReport.emptyHint') }}</div>
    </div>

    <!-- Data table -->
    <v-card v-else variant="outlined">
      <v-table hover>
        <thead>
          <tr>
            <th class="text-left" style="min-width: 200px">{{ groupByLabel }}</th>
            <th class="text-right">{{ t('costReport.col.requests') }}</th>
            <th class="text-right">{{ t('costReport.col.successRate') }}</th>
            <th class="text-right">{{ t('costReport.col.inputTokens') }}</th>
            <th class="text-right">{{ t('costReport.col.outputTokens') }}</th>
            <th class="text-right">{{ t('costReport.col.cacheCreation') }}</th>
            <th class="text-right">{{ t('costReport.col.cacheRead') }}</th>
            <th class="text-right">{{ t('costReport.col.compressionSavings') }}</th>
            <th class="text-right">{{ t('costReport.col.listCost') }}</th>
            <th class="text-left">{{ t('costReport.col.costBreakdown') }}</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="row in rows" :key="row.groupKey">
            <td class="text-left">
              <v-chip size="small" variant="tonal" color="primary">{{ row.groupKey || t('costReport.emptyGroup') }}</v-chip>
            </td>
            <td class="text-right">{{ formatNumber(row.totalRequests) }}</td>
            <td class="text-right">
              <span :class="row.successCount / row.totalRequests >= 0.95 ? 'text-success' : 'text-warning'">
                {{ ((row.successCount / row.totalRequests) * 100).toFixed(1) }}%
              </span>
            </td>
            <td class="text-right">{{ formatTokens(row.inputTokens) }}</td>
            <td class="text-right">{{ formatTokens(row.outputTokens) }}</td>
            <td class="text-right">{{ formatTokens(row.cacheCreationTokens) }}</td>
            <td class="text-right">{{ formatTokens(row.cacheReadTokens) }}</td>
            <td class="text-right">
              <span v-if="(row.originalTokensSaved ?? 0) > 0" class="text-success">
                {{ formatTokens((row.originalTokensSaved ?? 0) - (row.compressedTokensAfter ?? 0)) }}
              </span>
              <span v-else class="text-medium-emphasis">-</span>
            </td>
            <td class="text-right font-weight-bold">
              <span :class="{ 'text-warning': !isPricingComplete(row) }">
                {{ formatCost(row.listCostUSD, isPricingComplete(row)) }}
              </span>
              <v-tooltip v-if="!isPricingComplete(row)" location="top">
                <template #activator="{ props }">
                  <v-icon v-bind="props" class="ml-1" color="warning" size="16">mdi-alert-circle-outline</v-icon>
                </template>
                {{ pricingHint(row) }}
              </v-tooltip>
            </td>
            <td class="text-left">
              <div class="d-flex ga-1 flex-wrap">
                <v-chip
                  v-if="(row.zeroCostCount || 0) > 0"
                  size="x-small"
                  variant="tonal"
                  color="success"
                  :title="t('costReport.costBreakdown.zeroCost')"
                >
                  {{ t('costReport.costBreakdown.zeroCost') }} {{ formatNumber(row.zeroCostCount || 0) }}
                </v-chip>
                <v-chip
                  v-if="(row.configuredMultiplierCount || 0) > 0"
                  size="x-small"
                  variant="tonal"
                  color="primary"
                  :title="t('costReport.costBreakdown.configuredMultiplier')"
                >
                  {{ t('costReport.costBreakdown.configuredMultiplier') }} {{ formatNumber(row.configuredMultiplierCount || 0) }}
                </v-chip>
                <v-chip
                  v-if="(row.subscriptionCostCount || 0) > 0"
                  size="x-small"
                  variant="tonal"
                  color="info"
                  :title="t('costReport.costBreakdown.subscription')"
                >
                  {{ t('costReport.costBreakdown.subscription') }} {{ formatNumber(row.subscriptionCostCount || 0) }}
                </v-chip>
                <v-chip
                  v-if="(row.unpricedCostCount || 0) > 0"
                  size="x-small"
                  variant="tonal"
                  color="warning"
                  :title="t('costReport.costBreakdown.unpriced')"
                >
                  {{ t('costReport.costBreakdown.unpriced') }} {{ formatNumber(row.unpricedCostCount || 0) }}
                </v-chip>
              </div>
            </td>
          </tr>
        </tbody>
      </v-table>
    </v-card>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { api } from '@/services/api'
import type { CostReportRow } from '@/services/api-types'
import { useI18n } from '@/i18n'

const { t } = useI18n()

const loading = ref(false)
const rows = ref<CostReportRow[]>([])
const groupBy = ref('user')
const duration = ref('7d')
const apiType = ref('messages')

const groupByOptions = [
  { label: t('costReport.groupBy.user'), value: 'user' },
  { label: t('costReport.groupBy.model'), value: 'model' },
  { label: t('costReport.groupBy.key'), value: 'key' },
]

const durationOptions = [
  { label: '24h', value: '24h' },
  { label: '7d', value: '7d' },
  { label: '30d', value: '30d' },
  { label: '90d', value: '90d' },
  { label: '365d', value: '365d' },
]

const apiTypeOptions = [
  { label: t('costReport.apiType.messages'), value: 'messages' },
  { label: t('costReport.apiType.responses'), value: 'responses' },
  { label: t('costReport.apiType.chat'), value: 'chat' },
  { label: t('costReport.apiType.gemini'), value: 'gemini' },
  { label: t('costReport.apiType.images'), value: 'images' },
  { label: t('costReport.apiType.vectors'), value: 'vectors' },
]

const groupByLabel = computed(() => {
  return groupByOptions.find(o => o.value === groupBy.value)?.label || t('costReport.groupByLabel')
})

const totalRequests = computed(() => rows.value.reduce((s, r) => s + r.totalRequests, 0))
const totalSuccess = computed(() => rows.value.reduce((s, r) => s + r.successCount, 0))
const totalInputTokens = computed(() => rows.value.reduce((s, r) => s + r.inputTokens, 0))
const totalListCostUSD = computed(() => rows.value.reduce((s, r) => s + r.listCostUSD, 0))
const totalCompressionSaved = computed(() => rows.value.reduce((s, r) => s + ((r.originalTokensSaved ?? 0) - (r.compressedTokensAfter ?? 0)), 0))
const pricingComplete = computed(() => rows.value.every(isPricingComplete))
const successRate = computed(() => {
  if (totalRequests.value === 0) return '0.0'
  return ((totalSuccess.value / totalRequests.value) * 100).toFixed(1)
})

function formatNumber(n: number): string {
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(1) + 'M'
  if (n >= 1_000) return (n / 1_000).toFixed(1) + 'K'
  return n.toString()
}

function formatTokens(n: number): string {
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(2) + 'M'
  if (n >= 1_000) return (n / 1_000).toFixed(1) + 'K'
  return n.toString()
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

async function fetchReport() {
  loading.value = true
  try {
    const resp = await api.getCostReport(groupBy.value, duration.value, apiType.value)
    rows.value = resp.rows || []
  } catch (e) {
    console.error('[CostReport] failed to fetch report:', e)
    rows.value = []
  } finally {
    loading.value = false
  }
}

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
  const blob = new Blob(['﻿' + csv], { type: 'text/csv;charset=utf-8;' })
  const url = URL.createObjectURL(blob)
  const a = document.createElement('a')
  a.href = url
  a.download = `cost-report-${groupBy.value}-${apiType.value}-${duration.value}.csv`
  a.click()
  URL.revokeObjectURL(url)
}

onMounted(() => {
  fetchReport()
})
</script>

<style scoped>
.cost-report-view {
  padding: 16px;
}
</style>
