<template>
  <div class="usage-quota-rows">
    <div v-for="item in items" :key="item.key" class="usage-quota-rows__row">
      <span class="text-body-2 text-medium-emphasis">{{ item.label }}</span>

      <!-- 有百分比的行统一展示进度条；无百分比的行留白占位，保持网格对齐 -->
      <div v-if="item.usedPercent !== undefined" class="usage-quota-rows__bar">
        <span
          class="usage-quota-rows__fill"
          :style="{
            width: `${clampPercent(item.usedPercent)}%`,
            background: quotaRemainingColorHex(100 - clampPercent(item.usedPercent)),
          }"
        ></span>
      </div>
      <span v-else></span>

      <span class="text-body-2 font-weight-medium text-no-wrap">{{ item.value }}</span>
      <span class="text-caption text-disabled text-no-wrap" :title="item.captionTitle">{{ item.caption || '' }}</span>

      <!-- 配额真相等级徽章（配额真相分级调度 §2） -->
      <span
        v-if="item.truthLevel"
        class="truth-badge"
        :class="`truth-badge--${item.truthLevel}`"
        :title="truthTooltip(item)"
      >
        <span class="truth-badge__dot"></span>
        <span class="truth-badge__label">{{ truthLabel(item.truthLevel) }}</span>
      </span>
    </div>
  </div>
</template>

<script setup lang="ts">
import type { UsageQuotaItem } from '@/utils/usageQuotaItem'
import { quotaRemainingColorHex } from '@/utils/quotaColor'

defineProps<{ items: UsageQuotaItem[] }>()

const clampPercent = (value: number): number => Math.max(0, Math.min(100, value))

const truthLabel = (level: string): string => {
  switch (level) {
    case 'healthy': return '充足'
    case 'approaching_limit': return '趋紧'
    case 'exhausted': return '耗尽'
    case 'unavailable': return '获取失败'
    case 'unknown': return '未知'
    default: return level
  }
}

const sourceLabel = (source?: string): string => {
  switch (source) {
    case 'provider_api': return '官方 API'
    case 'response_headers': return '响应头'
    case 'configured': return '配置声明'
    case 'estimated': return '估算'
    default: return '未知来源'
  }
}

const truthTooltip = (item: UsageQuotaItem): string => {
  const level = truthLabel(item.truthLevel || 'unknown')
  if (item.truthSource) {
    return `数据来源：${sourceLabel(item.truthSource)}\n真相等级：${level}`
  }
  return `真相等级：${level}`
}
</script>

<style scoped>
.usage-quota-rows {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.usage-quota-rows__row {
  display: grid;
  grid-template-columns: minmax(110px, max-content) minmax(120px, 1fr) auto auto auto;
  align-items: center;
  gap: 12px;
}

.usage-quota-rows__bar {
  height: 6px;
  overflow: hidden;
  border-radius: 999px;
  background: rgba(var(--v-theme-on-surface), 0.08);
}

.usage-quota-rows__fill {
  display: block;
  height: 100%;
  border-radius: 999px;
}

/* 真相等级徽章 */
.truth-badge {
  display: inline-flex;
  align-items: center;
  gap: 6px;
  padding: 2px 8px;
  border-radius: 999px;
  font-size: 11px;
  line-height: 1.4;
  white-space: nowrap;
  background: rgba(var(--v-theme-on-surface), 0.06);
  color: rgba(var(--v-theme-on-surface), 0.6);
}

.truth-badge__dot {
  width: 6px;
  height: 6px;
  border-radius: 50%;
  background: currentColor;
}

.truth-badge--healthy {
  background: rgba(16, 185, 129, 0.12);
  color: #10b981;
}

.truth-badge--approaching_limit {
  background: rgba(245, 158, 11, 0.12);
  color: #f59e0b;
}

.truth-badge--exhausted {
  background: rgba(220, 38, 38, 0.12);
  color: #dc2626;
}

.truth-badge--unavailable {
  background: rgba(107, 114, 128, 0.12);
  color: #6b7280;
}

.truth-badge--unknown {
  background: rgba(156, 163, 175, 0.1);
  color: #9ca3af;
  opacity: 0.7;
}

.truth-badge--unknown .truth-badge__dot {
  opacity: 0.5;
}

@media (max-width: 700px) {
  .usage-quota-rows__row {
    grid-template-columns: 90px minmax(0, 1fr) auto;
  }

  .usage-quota-rows__row > :nth-child(4) {
    grid-column: 1 / -1;
  }

  .truth-badge {
    grid-column: 1 / -1;
    justify-self: start;
  }
}
</style>
