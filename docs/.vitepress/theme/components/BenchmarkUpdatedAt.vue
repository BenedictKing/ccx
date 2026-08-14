<script setup lang="ts">
import { ref, onMounted } from 'vue'

const props = defineProps<{
  src: string
  locale?: 'zh' | 'en'
}>()

const generatedAt = ref<string | null>(null)
const error = ref<string | null>(null)

const labels = {
  zh: {
    updatedAt: '数据更新时间',
    unknown: '未知',
    error: '读取失败',
  },
  en: {
    updatedAt: 'Data updated at',
    unknown: 'unknown',
    error: 'Failed to load',
  },
}

const t = (key: keyof typeof labels['zh']) => labels[props.locale === 'en' ? 'en' : 'zh'][key]

function formatDateTime(iso: string): string {
  try {
    const d = new Date(iso)
    if (Number.isNaN(d.getTime())) return iso
    return d.toLocaleString(props.locale === 'en' ? 'en-US' : 'zh-CN', {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
      timeZoneName: 'short',
    })
  } catch {
    return iso
  }
}

onMounted(async () => {
  try {
    const response = await fetch(props.src)
    if (!response.ok) {
      throw new Error(`HTTP ${response.status}`)
    }
    const data = await response.json()
    if (data && typeof data.generatedAt === 'string') {
      generatedAt.value = formatDateTime(data.generatedAt)
    } else {
      generatedAt.value = t('unknown')
    }
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  }
})
</script>

<template>
  <div class="benchmark-updated-at">
    <span class="label">{{ t('updatedAt') }}：</span>
    <span v-if="error" class="value error">{{ t('error') }} ({{ error }})</span>
    <span v-else-if="generatedAt" class="value">{{ generatedAt }}</span>
    <span v-else class="value">{{ t('unknown') }}</span>
  </div>
</template>

<style scoped>
.benchmark-updated-at {
  margin: 8px 0 16px;
  color: var(--vp-c-text-2);
  font-size: 14px;
}
.benchmark-updated-at .label {
  color: var(--vp-c-text-1);
  font-weight: 500;
}
.benchmark-updated-at .value {
  font-variant-numeric: tabular-nums;
}
.benchmark-updated-at .error {
  color: var(--vp-c-danger-1);
}
</style>
