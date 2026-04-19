<template>
  <v-dialog :model-value="modelValue" max-width="800" @update:model-value="$emit('update:modelValue', $event)">
    <v-card>
      <v-card-title class="d-flex align-center justify-space-between">
        <span class="dialog-title">{{ t('channelLogs.title', { channel: channelName }) }}</span>
        <div class="d-flex align-center ga-2">
          <v-btn
            class="auto-refresh-btn"
            size="small"
            density="comfortable"
            rounded="pill"
            :variant="autoRefresh ? 'flat' : 'outlined'"
            color="primary"
            :prepend-icon="autoRefresh ? 'mdi-autorenew' : 'mdi-refresh'"
            @click="autoRefresh = !autoRefresh"
          >
            {{ autoRefresh ? t('channelLogs.autoRefreshing') : t('channelLogs.autoRefresh') }}
          </v-btn>
          <v-btn icon size="small" variant="text" @click="$emit('update:modelValue', false)">
            <v-icon>mdi-close</v-icon>
          </v-btn>
        </div>
      </v-card-title>
      <v-divider />
      <v-card-text class="pa-0 channel-logs-scroll">
        <!-- Loading -->
        <div v-if="isLoading && !logs.length" class="d-flex justify-center py-8">
          <v-progress-circular indeterminate color="primary" />
        </div>

        <!-- Empty -->
        <div v-else-if="!logs.length" class="text-center py-8 text-medium-emphasis">
          <v-icon size="40">mdi-format-list-bulleted</v-icon>
          <div class="text-caption mt-2">{{ t('channelLogs.empty') }}</div>
        </div>

        <!-- Log list -->
        <v-list v-else density="comfortable" class="pa-0">
          <template v-for="(log, i) in logs" :key="i">
            <v-list-item :class="['log-item', { 'bg-error-subtle': !log.success }]" @click="toggleExpand(i)">
              <template #prepend>
                <v-chip :color="statusColor(log.statusCode)" size="small" variant="flat" class="mr-2 font-weight-bold log-status-chip">
                  {{ log.statusCode || 'ERR' }}
                </v-chip>
              </template>
              <v-list-item-title class="d-flex align-center ga-2 flex-wrap log-summary">
                <span class="text-medium-emphasis log-meta">{{ formatTime(log.timestamp) }}</span>
                <v-chip v-if="log.interfaceType" size="small" :color="interfaceTypeColor(log.interfaceType)" variant="tonal" class="text-uppercase">
                  {{ log.interfaceType }}
                </v-chip>
                <v-chip v-if="log.requestSource === 'capability_test'" size="small" color="warning" variant="tonal">
                  {{ t('channelLogs.sourceCapabilityTest') }}
                </v-chip>
                <span v-if="log.originalModel" class="text-medium-emphasis log-meta">{{ log.originalModel }} →</span>
                <span class="font-weight-medium log-model">{{ log.model }}</span>
                <span class="text-medium-emphasis log-meta">{{ log.durationMs }}ms</span>
                <span class="text-medium-emphasis log-meta">{{ log.keyMask }}</span>
                <v-chip v-if="log.isRetry" size="small" color="warning" variant="tonal">{{ t('channelLogs.retry') }}</v-chip>
              </v-list-item-title>
            </v-list-item>
            <!-- 展开的错误详情 -->
            <v-expand-transition>
              <div v-if="expandedIndex === i && log.errorInfo" class="px-4 py-2 log-error-info">
                {{ log.errorInfo }}
              </div>
            </v-expand-transition>
            <v-divider v-if="i < logs.length - 1" />
          </template>
        </v-list>
      </v-card-text>
    </v-card>
  </v-dialog>
</template>

<script setup lang="ts">
import { ref, watch, onUnmounted } from 'vue'
import { api, type ChannelLogEntry } from '../services/api'
import { useI18n } from '../i18n'

const props = defineProps<{
  modelValue: boolean
  channelIndex: number
  channelName: string
  channelType: 'messages' | 'chat' | 'responses' | 'gemini'
}>()

defineEmits<{
  (_e: 'update:modelValue', _v: boolean): void
}>()
const { t } = useI18n()

const logs = ref<ChannelLogEntry[]>([])
const isLoading = ref(false)
const autoRefresh = ref(false)
const expandedIndex = ref<number | null>(null)
let timer: ReturnType<typeof setInterval> | null = null

const toggleExpand = (i: number) => {
  expandedIndex.value = expandedIndex.value === i ? null : i
}

const statusColor = (code: number): string => {
  if (code >= 200 && code < 300) return 'success'
  if (code >= 400 && code < 500) return 'warning'
  return 'error'
}

const interfaceTypeColor = (type: string): string => {
  switch (type.toLowerCase()) {
    case 'messages': return 'primary'
    case 'chat': return 'success'
    case 'responses': return 'secondary'
    case 'gemini': return 'info'
    default: return 'default'
  }
}

const formatTime = (ts: string): string => {
  const d = new Date(ts)
  return d.toLocaleTimeString('zh-CN', { hour: '2-digit', minute: '2-digit', second: '2-digit' })
}

const fetchLogs = async () => {
  isLoading.value = true
  try {
    const res = await api.getChannelLogs(props.channelType, props.channelIndex)
    logs.value = res.logs || []
  } catch (e) {
    console.error('Failed to fetch channel logs:', e)
  } finally {
    isLoading.value = false
  }
}

const startPolling = () => {
  stopPolling()
  timer = setInterval(fetchLogs, 3000)
}
const stopPolling = () => { if (timer) { clearInterval(timer); timer = null } }

// 打开时加载，关闭时停止
watch(() => props.modelValue, (open) => {
  if (open) {
    logs.value = []
    expandedIndex.value = null
    fetchLogs()
    if (autoRefresh.value) startPolling()
  } else {
    stopPolling()
  }
})

// 对话框打开状态下切换渠道时重新加载
watch([() => props.channelIndex, () => props.channelType], () => {
  if (props.modelValue) {
    logs.value = []
    expandedIndex.value = null
    fetchLogs()
  }
})

watch(autoRefresh, (v) => {
  if (v && props.modelValue) startPolling()
  else stopPolling()
})

onUnmounted(() => stopPolling())
</script>

<style scoped>
.auto-refresh-btn :deep(.v-btn__content) {
  font-size: 0.8125rem;
  letter-spacing: 0;
  line-height: 1.5;
}

.auto-refresh-btn {
  text-transform: none;
}

.channel-logs-scroll {
  max-height: 500px;
  overflow-y: auto;
}

.log-item {
  padding-top: 10px;
  padding-bottom: 10px;
}

.log-status-chip {
  min-width: 44px;
  justify-content: center;
}

.log-summary {
  font-size: 0.875rem;
  line-height: 1.6;
}

.log-meta {
  font-size: 0.875rem;
}

.log-model {
  font-size: 0.875rem;
}

.log-error-info {
  background: rgba(var(--v-theme-surface-variant), 0.3);
  white-space: pre-wrap;
  word-break: break-all;
  font-size: 0.875rem;
  line-height: 1.6;
}

.bg-error-subtle {
  background: rgba(var(--v-theme-error), 0.05);
}
</style>
