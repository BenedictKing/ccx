<template>
  <v-card class="channel-dashboard-card" rounded="lg" elevation="0">
    <v-card-text class="pa-3">
      <v-list density="compact" class="pa-0 bg-transparent">
        <v-list-item class="px-0 metric-row">
          <div class="d-flex align-center justify-space-between w-100">
            <span class="text-body-2 text-medium-emphasis metric-label">总请求</span>
            <span class="text-body-2 font-weight-bold metric-value">{{ formatNumber(props.metrics?.requestCount) }}</span>
          </div>
        </v-list-item>
        <v-list-item class="px-0 metric-row">
          <div class="d-flex align-center justify-space-between w-100">
            <span class="text-body-2 text-medium-emphasis metric-label">可用率</span>
            <span class="text-body-2 font-weight-bold metric-value">{{ formatPercent(props.metrics?.successRate) }}</span>
          </div>
        </v-list-item>
        <v-list-item class="px-0 metric-row">
          <div class="d-flex align-center justify-space-between w-100">
            <span class="text-body-2 text-medium-emphasis metric-label">输入 Token</span>
            <span class="text-body-2 font-weight-bold metric-value">{{ formatNumber(props.metrics?.inputTokens) }}</span>
          </div>
        </v-list-item>
        <v-list-item class="px-0 metric-row">
          <div class="d-flex align-center justify-space-between w-100">
            <span class="text-body-2 text-medium-emphasis metric-label">输出 Token</span>
            <span class="text-body-2 font-weight-bold metric-value">{{ formatNumber(props.metrics?.outputTokens) }}</span>
          </div>
        </v-list-item>
        <v-list-item class="px-0 metric-row">
          <div class="d-flex align-center justify-space-between w-100">
            <span class="text-body-2 text-medium-emphasis metric-label">总 Token</span>
            <span class="text-body-2 font-weight-bold metric-value">{{ formatNumber(props.metrics?.totalTokens) }}</span>
          </div>
        </v-list-item>
        <v-list-item class="px-0 metric-row">
          <div class="d-flex align-center justify-space-between w-100">
            <span class="text-body-2 text-medium-emphasis metric-label">缓存 R/W</span>
            <span class="text-body-2 font-weight-bold metric-value">{{ cacheReadWriteText }}</span>
          </div>
        </v-list-item>
        <v-list-item class="px-0 metric-row">
          <div class="d-flex align-center justify-space-between w-100">
            <span class="text-body-2 text-medium-emphasis metric-label">成本</span>
            <span class="text-body-2 font-weight-bold metric-value">{{ formatCost(props.metrics?.totalCost) }}</span>
          </div>
        </v-list-item>
      </v-list>
    </v-card-text>
  </v-card>
</template>

<script lang="ts">
// 格式化数字：< 1000 原样；1K-1M 显示 1.2K；>= 1M 显示 1.2M。
// 缺值（undefined/null）显示 em dash。
export function formatNumber(n: number | undefined | null): string {
  if (n === undefined || n === null || Number.isNaN(n)) return '—'
  if (n < 1000) return String(n)
  if (n < 1_000_000) return (n / 1000).toFixed(1) + 'K'
  return (n / 1_000_000).toFixed(1) + 'M'
}

// 格式化百分比：保留 1 位小数 + %。
export function formatPercent(rate: number | undefined | null): string {
  if (rate === undefined || rate === null || Number.isNaN(rate)) return '—'
  return rate.toFixed(1) + '%'
}

// 格式化成本：decimal string -> $0.12。缺值或解析失败显示 em dash。
export function formatCost(cost: string | undefined | null): string {
  if (cost === undefined || cost === null || cost === '') return '—'
  const num = parseFloat(cost)
  if (Number.isNaN(num)) return '—'
  return '$' + num.toFixed(2)
}

// 缓存 R/W：两侧都缺值时显示 em dash；否则显示 "读 X 写 Y"，
// 单侧缺值时单侧也显示 em dash。
export function formatCacheReadWrite(
  read: number | undefined | null,
  write: number | undefined | null
): string {
  const hasRead = read !== undefined && read !== null
  const hasWrite = write !== undefined && write !== null
  if (!hasRead && !hasWrite) return '—'
  return `读 ${formatNumber(read)} 写 ${formatNumber(write)}`
}
</script>

<script setup lang="ts">
import { computed } from 'vue'
import type { ChannelMetrics } from '../services/api'

interface Props {
  metrics?: ChannelMetrics
}

const props = defineProps<Props>()

const cacheReadWriteText = computed(() =>
  formatCacheReadWrite(
    props.metrics?.cacheReadInputTokens,
    props.metrics?.cacheCreationInputTokens
  )
)
</script>

<style scoped>
.channel-dashboard-card {
  background: rgb(var(--v-theme-surface));
  border: 1px solid rgba(var(--v-theme-on-surface), 0.08);
}

.metric-row {
  min-height: 28px;
  padding-top: 2px;
  padding-bottom: 2px;
}

.metric-label {
  flex: 0 0 auto;
}

.metric-value {
  font-variant-numeric: tabular-nums;
  text-align: right;
}

.v-theme--dark .channel-dashboard-card {
  border-color: rgba(255, 255, 255, 0.08);
}
</style>
