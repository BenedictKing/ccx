<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { Loader2, RefreshCw } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { useAdminApi } from '@/composables/useAdminApi'
import { useLanguage } from '@/composables/useLanguage'
import {
  AUTOPILOT_TRACES_PATH,
  AUTOPILOT_TRACE_STATS_PATH,
  SMART_ROUTING_CONFIG_PATH,
} from '@/services/admin-api'
import type {
  AutopilotTraceListResponse,
  AutopilotTraceStats as TraceStatsType,
  SmartRoutingConfig,
} from '@/services/admin-api'
import AutopilotDiagnosePanel from './AutopilotDiagnosePanel.vue'
import AutopilotModePanel from './AutopilotModePanel.vue'
import AutopilotTraceDetailDialog from './AutopilotTraceDetailDialog.vue'
import AutopilotTraceStats from './AutopilotTraceStats.vue'
import AutopilotTraceTable from './AutopilotTraceTable.vue'

const { t } = useLanguage()
const api = useAdminApi()

const config = ref<SmartRoutingConfig | null>(null)
const traceStats = ref<TraceStatsType | null>(null)
const traces = ref<AutopilotTraceListResponse['traces']>([])
const loading = ref(true)
const saving = ref(false)
const tracesLoading = ref(false)

// Trace 详情对话框状态
const detailTraceUid = ref<string | null>(null)
const detailOpen = ref(false)

async function fetchAll() {
  loading.value = true
  try {
    const [cfg, stats, traceResp] = await Promise.all([
      api.get<SmartRoutingConfig>(SMART_ROUTING_CONFIG_PATH),
      api.get<TraceStatsType>(AUTOPILOT_TRACE_STATS_PATH),
      api.get<AutopilotTraceListResponse>(`${AUTOPILOT_TRACES_PATH}?limit=50`),
    ])
    config.value = cfg
    traceStats.value = stats
    traces.value = traceResp.traces
  } catch (err) {
    console.error('[Autopilot-Tab] 加载失败:', err)
  } finally {
    loading.value = false
  }
}

async function fetchTraces() {
  tracesLoading.value = true
  try {
    const resp = await api.get<AutopilotTraceListResponse>(`${AUTOPILOT_TRACES_PATH}?limit=50`)
    traces.value = resp.traces
  } catch (err) {
    console.error('[Autopilot-Tab] Trace 刷新失败:', err)
  } finally {
    tracesLoading.value = false
  }
}

async function handleConfigUpdate(updated: SmartRoutingConfig) {
  saving.value = true
  try {
    const resp = await api.put<SmartRoutingConfig>(SMART_ROUTING_CONFIG_PATH, updated)
    config.value = resp
  } catch (err) {
    console.error('[Autopilot-Tab] 配置保存失败:', err)
  } finally {
    saving.value = false
  }
}

onMounted(fetchAll)
</script>

<template>
  <div class="mx-auto flex w-full max-w-[1680px] flex-col gap-4">
    <div class="flex items-center justify-between">
      <h3 class="text-lg font-bold">{{ t('autopilot.title') }}</h3>
      <Button variant="outline" size="sm" :disabled="loading" @click="fetchAll">
        <Loader2 v-if="loading" class="size-4 animate-spin" />
        <RefreshCw v-else class="size-4" />
      </Button>
    </div>

    <div v-if="loading && !config" class="flex items-center justify-center py-16 text-muted-foreground">
      <Loader2 class="size-6 animate-spin" />
    </div>

    <template v-else-if="config">
      <AutopilotModePanel
        :config="config"
        :saving="saving"
        @update:config="handleConfigUpdate"
      />

      <AutopilotDiagnosePanel />

      <AutopilotTraceStats v-if="traceStats" :stats="traceStats" />

      <AutopilotTraceTable
        :traces="traces"
        :loading="tracesLoading"
        @refresh="fetchTraces"
        @select="detailTraceUid = $event; detailOpen = true"
      />
    </template>

    <AutopilotTraceDetailDialog v-model:open="detailOpen" :trace-uid="detailTraceUid" />
  </div>
</template>
