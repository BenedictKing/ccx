<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { Loader2, RefreshCw, TriangleAlert, X } from 'lucide-vue-next'
import { Alert } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { useAdminApi } from '@/composables/useAdminApi'
import { useLanguage } from '@/composables/useLanguage'
import { connectHealthStateEvents } from '@/composables/useHealthChangelog'
import {
  HEALTH_CENTER_CHANNELS_PATH,
  HEALTH_CENTER_OVERVIEW_PATH,
} from '@/services/admin-api'
import type {
  CapabilityDriftPayload,
  ChannelHealthItem,
  HealthCenterOverview,
  ManifestDriftPayload,
  StateEvent,
} from '@/services/admin-api'
import HealthCenterStats from './HealthCenterStats.vue'
import HealthChangelogTimeline from './HealthChangelogTimeline.vue'
import HealthChannelTable from './HealthChannelTable.vue'

const { t } = useLanguage()
const api = useAdminApi()

const overview = ref<HealthCenterOverview | null>(null)
const channels = ref<ChannelHealthItem[]>([])
const loading = ref(true)
const errorMessage = ref('')
let refreshTimer: ReturnType<typeof setInterval> | undefined

// ── 漂移告警栈（manifest_drift / capability_drift） ──

interface DriftAlert {
  uid: string
  title: string
  message: string
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

let disconnectStateEvents: (() => void) | null = null

async function loadData() {
  errorMessage.value = ''
  try {
    const [overviewResp, channelsResp] = await Promise.all([
      api.get<HealthCenterOverview>(HEALTH_CENTER_OVERVIEW_PATH),
      api.get<{ channels: ChannelHealthItem[] }>(HEALTH_CENTER_CHANNELS_PATH),
    ])
    overview.value = overviewResp
    channels.value = channelsResp.channels || []
  } catch (err) {
    errorMessage.value = err instanceof Error ? err.message : String(err)
  } finally {
    loading.value = false
  }
}

onMounted(() => {
  loadData()
  refreshTimer = setInterval(loadData, 30000)
  disconnectStateEvents = connectHealthStateEvents({ onEvent: handleDriftEvent })
})

onBeforeUnmount(() => {
  if (refreshTimer) clearInterval(refreshTimer)
  disconnectStateEvents?.()
  disconnectStateEvents = null
})
</script>

<template>
  <div class="mx-auto flex w-full max-w-[1680px] flex-col gap-4">
    <div class="flex items-center justify-between">
      <h3 class="text-lg font-bold">{{ t('healthCenter.title') }}</h3>
      <Button variant="outline" size="sm" :disabled="loading" @click="loadData">
        <Loader2 v-if="loading" class="size-4 animate-spin" />
        <RefreshCw v-else class="size-4" />
      </Button>
    </div>

    <Alert v-if="errorMessage" variant="destructive">
      <p class="text-sm">{{ errorMessage }}</p>
    </Alert>

    <!-- 漂移告警栈：manifest_drift / capability_drift（WS 实时推送，同 uid 去重，最多 5 条） -->
    <div v-if="driftAlerts.length > 0" class="flex flex-col gap-2">
      <Alert
        v-for="alert in driftAlerts"
        :key="alert.uid"
        class="relative pr-10 [&>svg]:top-3.5"
      >
        <TriangleAlert class="size-4 text-amber-500" />
        <div class="min-w-0">
          <p class="text-sm font-semibold leading-tight">{{ alert.title }}</p>
          <p class="mt-1 text-xs text-muted-foreground">{{ alert.message }}</p>
        </div>
        <button
          type="button"
          class="absolute right-2.5 top-2.5 rounded p-1 text-muted-foreground transition-colors hover:bg-secondary hover:text-foreground cursor-pointer"
          :aria-label="t('common.close')"
          @click="dismissDriftAlert(alert.uid)"
        >
          <X class="size-3.5" />
        </button>
      </Alert>
    </div>

    <div v-if="loading && !overview" class="flex items-center justify-center py-16 text-muted-foreground">
      <Loader2 class="size-6 animate-spin" />
    </div>

    <template v-else>
      <div class="grid grid-cols-2 gap-3 sm:grid-cols-4">
        <div class="rounded-lg border border-border/60 bg-card/40 px-4 py-3">
          <div class="text-2xl font-bold">{{ overview?.totalChannels ?? 0 }}</div>
          <div class="text-xs text-muted-foreground">{{ t('healthCenter.totalChannels') }}</div>
        </div>
        <div class="rounded-lg border border-border/60 bg-card/40 px-4 py-3">
          <div class="text-2xl font-bold">{{ overview?.totalEndpoints ?? 0 }}</div>
          <div class="text-xs text-muted-foreground">{{ t('healthCenter.totalEndpoints') }}</div>
        </div>
      </div>

      <HealthCenterStats v-if="overview" :overview="overview" />

      <div class="grid grid-cols-1 gap-4 lg:grid-cols-[2fr_1fr]">
        <HealthChannelTable :channels="channels" />
        <HealthChangelogTimeline />
      </div>
    </template>
  </div>
</template>
