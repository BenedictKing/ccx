<template>
  <v-dialog
    :model-value="modelValue"
    max-width="760"
    scrollable
    @update:model-value="$emit('update:modelValue', $event)"
  >
    <v-card class="guide-card" rounded="lg">
      <!-- 标题栏 -->
      <v-card-title class="guide-header d-flex align-center px-5 py-4">
        <v-icon class="mr-3" color="primary">mdi-help-circle</v-icon>
        <span class="text-h6 font-weight-bold">{{ t('guide.title') }}</span>
        <v-spacer />
        <span class="text-caption text-medium-emphasis mr-3 d-none d-sm-inline">
          {{ t('guide.stepIndicator', { current: step + 1, total: STEP_COUNT }) }}
        </span>
        <v-tooltip :text="t('app.actions.close') + ' (Esc)'" location="bottom" content-class="ccx-tooltip">
          <template #activator="{ props: tooltipProps }">
            <v-btn icon size="small" variant="text" v-bind="tooltipProps" @click="close">
              <v-icon size="20">mdi-close</v-icon>
            </v-btn>
          </template>
        </v-tooltip>
      </v-card-title>

      <v-divider />

      <!-- 步骤进度点 -->
      <div class="guide-dots d-flex align-center justify-center ga-2 py-3">
        <button
          v-for="i in STEP_COUNT"
          :key="i"
          type="button"
          class="guide-dot"
          :class="{ active: i - 1 === step }"
          :aria-label="`${i}`"
          @click="step = i - 1"
        ></button>
      </div>

      <v-divider />

      <v-card-text class="guide-body px-5 py-5">
        <!-- 1. 欢迎总览 -->
        <section v-show="step === 0" class="guide-section">
          <h3 class="guide-section-title">{{ t('guide.welcome.title') }}</h3>
          <p class="guide-section-body">{{ t('guide.welcome.body') }}</p>
          <ol class="guide-steps-list">
            <li>{{ t('guide.welcome.step1') }}</li>
            <li>{{ t('guide.welcome.step2') }}</li>
            <li>{{ t('guide.welcome.step3') }}</li>
          </ol>
        </section>

        <!-- 2. 协议切换 -->
        <section v-show="step === 1" class="guide-section">
          <h3 class="guide-section-title">{{ t('guide.protocol.title') }}</h3>
          <div class="guide-tabs-demo mb-3">
            <span class="demo-tab active">Claude</span>
            <span class="demo-sep">/</span>
            <span class="demo-tab">OpenAI Chat</span>
            <span class="demo-sep">/</span>
            <span class="demo-tab">Images</span>
            <span class="demo-sep">/</span>
            <span class="demo-tab">Codex</span>
            <span class="demo-sep">/</span>
            <span class="demo-tab">Gemini</span>
            <span class="demo-sep">/</span>
            <span class="demo-tab">Cockpit</span>
          </div>
          <p class="guide-section-body">{{ t('guide.protocol.body1') }}</p>
          <p class="guide-section-body">{{ t('guide.protocol.body2') }}</p>
          <p class="guide-section-body">{{ t('guide.protocol.body3') }}</p>
        </section>

        <!-- 3. 添加渠道 -->
        <section v-show="step === 2" class="guide-section">
          <h3 class="guide-section-title">{{ t('guide.addChannel.title') }}</h3>
          <div class="guide-addbtn-demo mb-3">
            <v-btn color="primary" prepend-icon="mdi-plus" size="small" variant="flat" class="demo-add-btn">
              {{ t('app.actions.addChannel') }}
            </v-btn>
          </div>
          <p class="guide-section-body">{{ t('guide.addChannel.body1') }}</p>
          <p class="guide-section-body">{{ t('guide.addChannel.body2') }}</p>
        </section>

        <!-- 4. 看懂渠道列表（自绘两渠道示意） -->
        <section v-show="step === 3" class="guide-section">
          <h3 class="guide-section-title">{{ t('guide.channelList.title') }}</h3>
          <p class="guide-section-body mb-3">{{ t('guide.channelList.intro') }}</p>

          <!-- 示意渠道行 -->
          <div class="demo-channel-list mb-4">
            <div
              v-for="row in demoRows"
              :key="row.priority"
              class="demo-row"
              :class="{ 'is-paused': row.status === 'suspended' }"
            >
              <span class="demo-handle"><v-icon size="16" color="grey">mdi-drag-vertical</v-icon></span>
              <span class="demo-priority">{{ row.priority }}</span>
              <span class="demo-status"><ChannelStatusBadge :status="row.status" :show-label="false" size="small" /></span>
              <span class="demo-name">{{ row.name }}</span>
              <span class="demo-mid text-caption text-medium-emphasis">15m · 99%</span>
              <span class="demo-keys"><v-icon size="14">mdi-key</v-icon> {{ row.keys }}</span>
              <span class="demo-actions">
                <v-icon
                  v-if="row.status === 'suspended'"
                  size="18"
                  color="warning"
                  class="demo-action-icon"
                >mdi-refresh</v-icon>
                <v-icon size="18" class="demo-action-icon">mdi-history</v-icon>
              </span>
            </div>
          </div>

          <ul class="guide-click-list">
            <li><v-icon size="16" color="primary" class="mr-2">mdi-pencil</v-icon>{{ t('guide.channelList.clickName') }}</li>
            <li><v-icon size="16" color="primary" class="mr-2">mdi-chart-areaspline</v-icon>{{ t('guide.channelList.clickRow') }}</li>
            <li><v-icon size="16" color="primary" class="mr-2">mdi-history</v-icon>{{ t('guide.channelList.clickLogs') }}</li>
            <li><v-icon size="16" color="warning" class="mr-2">mdi-refresh</v-icon>{{ t('guide.channelList.clickResume') }}</li>
            <li><v-icon size="16" color="grey" class="mr-2">mdi-drag-vertical</v-icon>{{ t('guide.channelList.drag') }}</li>
          </ul>
        </section>

      </v-card-text>

      <v-divider />

      <!-- 底部导航 -->
      <v-card-actions class="guide-footer px-5 py-3">
        <v-btn v-if="step > 0" variant="outlined" prepend-icon="mdi-arrow-left" @click="prev">
          {{ t('guide.prev') }}
        </v-btn>
        <v-spacer />
        <v-btn v-if="step < STEP_COUNT - 1" color="primary" variant="elevated" append-icon="mdi-arrow-right" @click="next">
          {{ t('guide.next') }}
          <span class="shortcut-hint ml-2 text-xs opacity-50">Enter</span>
        </v-btn>
        <v-btn v-else color="primary" variant="elevated" prepend-icon="mdi-check" @click="close">
          {{ t('guide.gotIt') }}
          <span class="shortcut-hint ml-2 text-xs opacity-50">Enter</span>
        </v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'

import ChannelStatusBadge from './ChannelStatusBadge.vue'
import { useDialogHotkeys } from '@/composables/useDialogHotkeys'
import { useI18n } from '../i18n'
import type { ChannelStatus } from '../services/api'

const props = defineProps<{
  modelValue: boolean
}>()

const emit = defineEmits<{
  'update:modelValue': [value: boolean]
}>()

const { t } = useI18n()

const STEP_COUNT = 4
const step = ref(0)

// 每次打开指引时回到第一步
watch(
  () => props.modelValue,
  (open) => {
    if (open) step.value = 0
  },
)

// 渠道列表示意行：一个正常、一个暂停
const demoRows = computed<Array<{ priority: number; status: ChannelStatus; name: string; keys: number }>>(() => [
  { priority: 1, status: 'active', name: t('guide.channelList.demoNormalName'), keys: 3 },
  { priority: 2, status: 'suspended', name: t('guide.channelList.demoPausedName'), keys: 2 },
])

function prev() {
  if (step.value > 0) step.value -= 1
}

function next() {
  if (step.value < STEP_COUNT - 1) step.value += 1
}

function close() {
  emit('update:modelValue', false)
}

// 键盘快捷键：Enter 下一步/完成（经全局对话框快捷键栈，仅栈顶时生效；Esc 关闭由 Vuetify 原生处理）
useDialogHotkeys(
  () => props.modelValue,
  {
    plainEnter: () => {
      if (step.value < STEP_COUNT - 1) next()
      else close()
    },
  },
)
</script>

<style scoped>
.guide-card {
  border: 1px solid rgb(var(--v-theme-on-surface), 0.12);
}

.guide-header {
  background: rgba(var(--v-theme-primary), 0.04);
}

/* 步骤进度点 */
.guide-dot {
  width: 9px;
  height: 9px;
  border-radius: 50%;
  background: rgba(var(--v-theme-on-surface), 0.2);
  border: none;
  cursor: pointer;
  transition: all 0.15s ease;
  padding: 0;
}

.guide-dot:hover {
  background: rgba(var(--v-theme-on-surface), 0.4);
}

.guide-dot.active {
  width: 22px;
  border-radius: 5px;
  background: rgb(var(--v-theme-primary));
}

/* 内容区 */
.guide-body {
  min-height: 280px;
}

.guide-section-title {
  font-size: 1.05rem;
  font-weight: 700;
  margin-bottom: 12px;
}

.guide-section-body {
  font-size: 0.92rem;
  line-height: 1.7;
  color: rgba(var(--v-theme-on-surface), 0.78);
  margin-bottom: 8px;
}

.guide-steps-list {
  margin: 12px 0 0;
  padding-left: 20px;
  line-height: 2;
  font-size: 0.92rem;
}

/* 协议标签示意 */
.guide-tabs-demo {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 6px;
  padding: 12px 14px;
  background: rgba(var(--v-theme-on-surface), 0.04);
  border-radius: 8px;
  font-weight: 600;
}

.demo-tab {
  color: rgba(var(--v-theme-on-surface), 0.55);
}

.demo-tab.active {
  color: rgb(var(--v-theme-primary));
}

.demo-sep {
  color: rgba(var(--v-theme-on-surface), 0.25);
}

/* 添加渠道按钮示意 */
.guide-addbtn-demo {
  padding: 14px;
  background: rgba(var(--v-theme-on-surface), 0.04);
  border-radius: 8px;
}

.demo-add-btn {
  pointer-events: none;
}

/* 渠道列表示意行 */
.demo-channel-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.demo-row {
  display: grid;
  grid-template-columns: 24px 24px auto 1fr auto auto auto;
  align-items: center;
  gap: 10px;
  padding: 10px 12px;
  background: rgb(var(--v-theme-surface));
  border: 1px solid rgba(var(--v-theme-on-surface), 0.12);
  border-radius: 8px;
}

.demo-row.is-paused {
  border-color: var(--ccx-status-suspended-fg);
  background: rgba(var(--v-theme-warning), 0.08);
}

.demo-handle {
  display: inline-flex;
}

.demo-priority {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 22px;
  height: 22px;
  font-size: 0.75rem;
  font-weight: 700;
  background: rgba(var(--v-theme-on-surface), 0.06);
  border-radius: 4px;
}

.demo-name {
  font-weight: 600;
  font-size: 0.9rem;
}

.demo-mid {
  text-align: center;
  white-space: nowrap;
}

.demo-keys {
  display: inline-flex;
  align-items: center;
  gap: 3px;
  font-size: 0.8rem;
  padding: 2px 8px;
  border: 1px solid rgba(var(--v-theme-on-surface), 0.3);
  border-radius: 12px;
}

.demo-actions {
  display: inline-flex;
  align-items: center;
  gap: 6px;
}

.demo-action-icon {
  opacity: 0.7;
}

/* 点击点说明列表 */
.guide-click-list {
  list-style: none;
  padding: 0;
  margin: 0;
}

.guide-click-list li {
  display: flex;
  align-items: flex-start;
  font-size: 0.9rem;
  line-height: 1.6;
  padding: 7px 0;
  border-bottom: 1px dashed rgba(var(--v-theme-on-surface), 0.08);
}

.guide-click-list li:last-child {
  border-bottom: none;
}

.guide-click-list li :deep(.v-icon) {
  margin-top: 2px;
  flex-shrink: 0;
}

@media (max-width: 600px) {
  .demo-row {
    grid-template-columns: 20px 20px auto 1fr auto auto;
    gap: 6px;
  }

  .demo-mid {
    display: none;
  }
}
</style>
