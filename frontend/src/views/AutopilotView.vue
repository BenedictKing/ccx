<template>
  <div class="autopilot-view">
    <!-- 标题栏 -->
    <div class="d-flex align-center justify-space-between mb-4">
      <div class="d-flex align-center">
        <v-icon size="28" class="mr-2" color="primary">mdi-steering</v-icon>
        <span class="text-h5 font-weight-bold">{{ t('autopilot.title') }}</span>
      </div>
      <v-btn
        variant="tonal"
        prepend-icon="mdi-refresh"
        :loading="loading"
        @click="fetchAll"
      >
        {{ t('app.actions.refresh') }}
      </v-btn>
    </div>

    <!-- 加载态 -->
    <div v-if="loading && !config" class="text-center py-12">
      <v-progress-circular indeterminate color="primary" size="48" />
    </div>

    <!-- 内容 -->
    <template v-else-if="config">
      <!-- 全局策略面板 -->
      <AutopilotModePanel
        :config="config"
        :saving="saving"
        @update:config="handleConfigUpdate"
      />

      <!-- 路由诊断（dry-run，不发送真实上游请求） -->
      <AutopilotDiagnosePanel class="mt-6" />

      <!-- Trace 统计 -->
      <AutopilotTraceStats
        v-if="traceStats"
        :stats="traceStats"
        class="mt-6"
      />

      <!-- Trace 列表 -->
      <AutopilotTraceTable
        :traces="traces"
        :loading="tracesLoading"
        :partial="tracesPartial"
        class="mt-4"
        @refresh="fetchTraces"
        @select="openTraceDetail"
      />
    </template>

    <!-- 空态 -->
    <EmptyState
      v-else
      icon="mdi-steering"
      :title="t('autopilot.empty.title')"
      :description="t('autopilot.empty.description')"
      :action-label="t('app.actions.refresh')"
      @action="fetchAll"
    />

    <!-- Trace 详情对话框 -->
    <AutopilotTraceDetailDialog
      v-model="detailDialogOpen"
      :trace-uid="selectedTraceUid"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useI18n } from '@/i18n'
import { api } from '@/services/api'
import AutopilotModePanel from '@/components/AutopilotModePanel.vue'
import AutopilotDiagnosePanel from '@/components/AutopilotDiagnosePanel.vue'
import AutopilotTraceStats from '@/components/AutopilotTraceStats.vue'
import AutopilotTraceTable from '@/components/AutopilotTraceTable.vue'
import AutopilotTraceDetailDialog from '@/components/AutopilotTraceDetailDialog.vue'
import EmptyState from '@/components/EmptyState.vue'
import type {
  SmartRoutingConfig,
  AutopilotTraceStats as TraceStatsType,
  TraceSummary,
} from '@/services/api-types'

const { t } = useI18n()

const config = ref<SmartRoutingConfig | null>(null)
const traceStats = ref<TraceStatsType | null>(null)
const traces = ref<TraceSummary[]>([])
const tracesPartial = ref(false)
const loading = ref(true)
const saving = ref(false)
const tracesLoading = ref(false)

// 详情对话框状态
const detailDialogOpen = ref(false)
const selectedTraceUid = ref('')

async function fetchAll() {
  loading.value = true
  try {
    const [cfg, stats, traceResp] = await Promise.all([
      api.getSmartRoutingConfig(),
      api.getAutopilotTraceStats(),
      api.getAutopilotTraces({ limit: 50 }),
    ])
    config.value = cfg
    traceStats.value = stats
    traces.value = traceResp.traces
    tracesPartial.value = traceResp.partial ?? false
  } catch (e) {
    console.error('[Autopilot-View] 加载失败:', e)
  } finally {
    loading.value = false
  }
}

async function fetchTraces() {
  tracesLoading.value = true
  try {
    const resp = await api.getAutopilotTraces({ limit: 50 })
    traces.value = resp.traces
    tracesPartial.value = resp.partial ?? false
  } catch (e) {
    console.error('[Autopilot-View] Trace 刷新失败:', e)
  } finally {
    tracesLoading.value = false
  }
}

function openTraceDetail(traceUid: string) {
  selectedTraceUid.value = traceUid
  detailDialogOpen.value = true
}

async function handleConfigUpdate(updated: SmartRoutingConfig) {
  saving.value = true
  try {
    const resp = await api.updateSmartRoutingConfig(updated)
    config.value = resp
  } catch (e) {
    console.error('[Autopilot-View] 配置保存失败:', e)
  } finally {
    saving.value = false
  }
}

onMounted(fetchAll)
</script>
