<template>
  <div class="health-center">
    <!-- Header -->
    <div class="d-flex align-center justify-space-between mb-4">
      <div class="d-flex align-center">
        <v-icon size="28" class="mr-2" color="primary">mdi-stethoscope</v-icon>
        <span class="text-h5 font-weight-bold">{{ t('healthCenter.title') }}</span>
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

    <!-- Overview stats -->
    <HealthCenterStats v-if="overview" :overview="overview" class="mb-2" />

    <!-- Summary line -->
    <div v-if="overview" class="d-flex ga-4 text-caption text-medium-emphasis mb-4">
      <span>{{ t('healthCenter.totalChannels') }}: {{ overview.totalChannels }}</span>
      <span>{{ t('healthCenter.totalEndpoints') }}: {{ overview.totalEndpoints }}</span>
    </div>

    <!-- Drift alerts (manifest / capability) -->
    <v-alert
      v-for="alert in driftAlerts"
      :key="alert.uid"
      type="warning"
      variant="tonal"
      density="compact"
      class="mb-2"
      icon="mdi-alert"
      closable
      @click:close="dismissDriftAlert(alert.uid)"
    >
      <div class="d-flex align-center justify-space-between">
        <span class="font-weight-medium">{{ alert.title }}</span>
        <span class="text-caption text-medium-emphasis">{{ relativeTime(alert.createdAt) }}</span>
      </div>
      <div class="text-body-2">{{ alert.message }}</div>
    </v-alert>

    <!-- Recent changes timeline (Phase 3A) -->
    <ProfileChangelogTimeline class="mb-4" />

    <!-- Loading state -->
    <div v-if="loading && !overview" class="text-center py-12">
      <v-progress-circular indeterminate color="primary" size="48" />
    </div>

    <!-- Channel table -->
    <HealthChannelTable v-else-if="overview" :channels="channels" />

    <!-- Empty state -->
    <EmptyState
      v-else-if="!loading && !overview"
      icon="mdi-stethoscope"
      :title="t('healthCenter.empty.title')"
      :description="t('healthCenter.empty.description')"
      :action-label="t('app.actions.refresh')"
      @action="fetchAll"
    />
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useI18n } from '@/i18n'
import { api } from '@/services/api'
import HealthCenterStats from '@/components/HealthCenterStats.vue'
import HealthChannelTable from '@/components/HealthChannelTable.vue'
import ProfileChangelogTimeline from '@/components/ProfileChangelogTimeline.vue'
import EmptyState from '@/components/EmptyState.vue'
import type { HealthCenterOverview, ChannelHealthItem, StateEvent, ManifestDriftPayload, CapabilityDriftPayload } from '@/services/api-types'
import { useEventStream } from '@/composables/useEventStream'

const { t } = useI18n()

const overview = ref<HealthCenterOverview | null>(null)
const channels = ref<ChannelHealthItem[]>([])
const loading = ref(true)

async function fetchAll() {
  loading.value = true
  try {
    const [ov, ch] = await Promise.all([
      api.getHealthCenterOverview(),
      api.getHealthCenterChannels(),
    ])
    overview.value = ov
    channels.value = ch.channels
  } finally {
    loading.value = false
  }
}

const eventStream = useEventStream()

// 事件去抖：熔断/Key 状态可能短时高频迁移，合并多次触发为一次刷新
let refreshTimer: ReturnType<typeof setTimeout> | null = null
const scheduleRefresh = () => {
  if (refreshTimer) return
  refreshTimer = setTimeout(() => {
    refreshTimer = null
    void fetchAll()
  }, 400)
}

interface DriftAlert {
  uid: string
  title: string
  message: string
  createdAt: string
}

const driftAlerts = ref<DriftAlert[]>([])
const maxDriftAlerts = 5

function dismissDriftAlert(uid: string) {
  driftAlerts.value = driftAlerts.value.filter(a => a.uid !== uid)
}

function pushDriftAlert(alert: DriftAlert) {
  // 相同 uid 的 drift 事件只保留最新一条
  const existingIndex = driftAlerts.value.findIndex(a => a.uid === alert.uid)
  if (existingIndex >= 0) {
    driftAlerts.value[existingIndex] = alert
    return
  }
  driftAlerts.value.unshift(alert)
  if (driftAlerts.value.length > maxDriftAlerts) {
    driftAlerts.value = driftAlerts.value.slice(0, maxDriftAlerts)
  }
}

function buildManifestDriftAlert(ev: StateEvent, payload: ManifestDriftPayload): DriftAlert {
  const added = payload.added ?? []
  const removed = payload.removed ?? []
  const parts: string[] = []
  if (added.length > 0) {
    parts.push(t('healthCenter.drift.addedModels', { count: added.length, models: added.join(', ') }))
  }
  if (removed.length > 0) {
    parts.push(t('healthCenter.drift.removedModels', { count: removed.length, models: removed.join(', ') }))
  }
  return {
    uid: ev.uid,
    title: t('healthCenter.drift.manifestTitle', { subject: ev.subject ?? '' }),
    message: parts.join('；') || t('healthCenter.drift.manifestFallback'),
    createdAt: ev.createdAt,
  }
}

function buildCapabilityDriftAlert(ev: StateEvent, payload: CapabilityDriftPayload): DriftAlert {
  const model = payload.model ?? ''
  const protocol = payload.protocol ?? ''
  const channelName = payload.channelName ?? ev.subject ?? ''
  const fields = payload.driftFields ?? []
  return {
    uid: ev.uid,
    title: t('healthCenter.drift.capabilityTitle', { model, protocol }),
    message: t('healthCenter.drift.capabilityMessage', {
      channel: channelName,
      model,
      protocol,
      fields: fields.join(', ') || 'probe_success',
    }),
    createdAt: ev.createdAt,
  }
}

function handleDriftEvent(ev: StateEvent) {
  if (ev.type !== 'manifest_drift' && ev.type !== 'capability_drift') return
  if (!ev.payload) return

  if (ev.type === 'manifest_drift') {
    pushDriftAlert(buildManifestDriftAlert(ev, ev.payload as ManifestDriftPayload))
  } else {
    pushDriftAlert(buildCapabilityDriftAlert(ev, ev.payload as CapabilityDriftPayload))
  }
}

function relativeTime(iso: string): string {
  const then = new Date(iso).getTime()
  if (Number.isNaN(then)) return ''
  const diffSec = Math.max(0, Math.floor((Date.now() - then) / 1000))
  if (diffSec < 60) return `${diffSec}s`
  const diffMin = Math.floor(diffSec / 60)
  if (diffMin < 60) return `${diffMin}m`
  const diffHour = Math.floor(diffMin / 60)
  if (diffHour < 24) return `${diffHour}h`
  return `${Math.floor(diffHour / 24)}d`
}

onMounted(() => {
  void fetchAll()
  // 事件驱动即时刷新（本视图原本无轮询，事件为纯增量；失败时仍可手动刷新）
  eventStream.on('circuit_breaker_state_changed', scheduleRefresh)
  eventStream.on('key_blacklisted', scheduleRefresh)
  eventStream.on('key_restored', scheduleRefresh)
  eventStream.on('channel_status_changed', scheduleRefresh)
  eventStream.on('upstream_changed', scheduleRefresh)
  eventStream.on('manifest_drift', handleDriftEvent)
  eventStream.on('capability_drift', handleDriftEvent)
})
</script>
