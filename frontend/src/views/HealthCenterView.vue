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

    <!-- Recent changes timeline (Phase 3A) -->
    <ProfileChangelogTimeline class="mb-4" />

    <!-- Loading state -->
    <div v-if="loading && !overview" class="text-center py-12">
      <v-progress-circular indeterminate color="primary" size="48" />
    </div>

    <!-- Channel table -->
    <HealthChannelTable v-else-if="overview" :channels="channels" />
  </div>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useI18n } from '@/i18n'
import { api } from '@/services/api'
import HealthCenterStats from '@/components/HealthCenterStats.vue'
import HealthChannelTable from '@/components/HealthChannelTable.vue'
import ProfileChangelogTimeline from '@/components/ProfileChangelogTimeline.vue'
import type { HealthCenterOverview, ChannelHealthItem } from '@/services/api-types'
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

onMounted(() => {
  void fetchAll()
  // 事件驱动即时刷新（本视图原本无轮询，事件为纯增量；失败时仍可手动刷新）
  eventStream.on('circuit_breaker_state_changed', scheduleRefresh)
  eventStream.on('key_blacklisted', scheduleRefresh)
  eventStream.on('key_restored', scheduleRefresh)
  eventStream.on('channel_status_changed', scheduleRefresh)
  eventStream.on('upstream_changed', scheduleRefresh)
})
</script>
