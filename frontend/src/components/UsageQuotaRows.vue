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
    </div>
  </div>
</template>

<script setup lang="ts">
import type { UsageQuotaItem } from '@/utils/usageQuotaItem'
import { quotaRemainingColorHex } from '@/utils/quotaColor'

defineProps<{ items: UsageQuotaItem[] }>()

const clampPercent = (value: number): number => Math.max(0, Math.min(100, value))
</script>

<style scoped>
.usage-quota-rows {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.usage-quota-rows__row {
  display: grid;
  grid-template-columns: minmax(110px, max-content) minmax(120px, 1fr) auto auto;
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

@media (max-width: 700px) {
  .usage-quota-rows__row {
    grid-template-columns: 90px minmax(0, 1fr) auto;
  }

  .usage-quota-rows__row > :last-child {
    grid-column: 1 / -1;
  }
}
</style>
