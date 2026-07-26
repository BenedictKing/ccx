<template>
  <div class="model-chip-list">
    <div
      ref="viewportRef"
      class="model-chip-list__viewport"
      :class="{ 'model-chip-list__viewport--collapsed': !expanded }"
    >
      <div ref="modelsRef" class="model-chip-list__models">
        <v-chip
          v-for="model in models"
          :key="model"
          size="small"
          variant="outlined"
          :color="color"
          class="model-chip-list__model"
        >
          {{ model }}
        </v-chip>
      </div>
    </div>
    <button
      v-if="hasOverflow"
      type="button"
      class="model-chip-list__toggle text-caption"
      @click="expanded = !expanded"
    >
      {{ expanded ? t('channelEditor.protocolModels.collapse') : t('channelEditor.protocolModels.expand') }}
      <v-icon
        class="model-chip-list__toggle-icon"
        :class="{ 'model-chip-list__toggle-icon--expanded': expanded }"
        size="16"
      >
        mdi-chevron-down
      </v-icon>
    </button>
  </div>
</template>

<script setup lang="ts">
import { nextTick, onBeforeUnmount, ref, watch } from 'vue'

import { useI18n } from '../../i18n'

const props = withDefaults(defineProps<{
  models: string[]
  color?: string
}>(), {
  color: undefined,
})

const { t } = useI18n()
const expanded = ref(false)
const hasOverflow = ref(false)
const viewportRef = ref<HTMLElement | null>(null)
const modelsRef = ref<HTMLElement | null>(null)
let observer: ResizeObserver | null = null

const measureOverflow = () => {
  const viewport = viewportRef.value
  const models = modelsRef.value
  if (!viewport || !models || expanded.value) return
  hasOverflow.value = models.scrollHeight > viewport.clientHeight + 1
}

const resetAndMeasure = async () => {
  expanded.value = false
  await nextTick()
  measureOverflow()
}

watch(() => props.models, resetAndMeasure, { deep: true, flush: 'post' })

watch(viewportRef, (viewport) => {
  observer?.disconnect()
  observer = null
  if (!viewport) return
  // 容器宽度变化会改变换行结果，需重新判断是否溢出；无 ResizeObserver 时仅做一次测量。
  if (typeof ResizeObserver !== 'undefined') {
    observer = new ResizeObserver(measureOverflow)
    observer.observe(viewport)
  }
  resetAndMeasure()
}, { immediate: true, flush: 'post' })

onBeforeUnmount(() => observer?.disconnect())
</script>

<style scoped>
.model-chip-list {
  --model-chip-row-height: 24px;
  min-width: 0;
}

/* 两行 chip 高度 + 行间 gap，折叠态最多显示两行 */
.model-chip-list__viewport--collapsed {
  max-height: calc(var(--model-chip-row-height) * 2 + 6px);
  overflow: hidden;
}

.model-chip-list__models {
  display: flex;
  align-items: flex-start;
  align-content: flex-start;
  flex-wrap: wrap;
  gap: 6px;
  min-width: 0;
}

.model-chip-list__model {
  height: auto;
  min-height: 24px;
  max-width: 100%;
}

.model-chip-list__model :deep(.v-chip__content) {
  overflow-wrap: anywhere;
  white-space: normal;
  line-height: 1.35;
}

.model-chip-list__toggle {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  margin-top: 8px;
  padding: 0;
  border: none;
  background: none;
  color: rgb(var(--v-theme-primary));
  cursor: pointer;
}

.model-chip-list__toggle-icon {
  transition: transform 0.16s ease;
}

.model-chip-list__toggle-icon--expanded {
  transform: rotate(180deg);
}
</style>
