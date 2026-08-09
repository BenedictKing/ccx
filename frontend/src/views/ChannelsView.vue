<template>
  <!-- 渠道编排（高密度列表模式） -->
  <ChannelOrchestration
    v-if="(channelStore.currentChannelsData as any).channels?.length"
    :channels="(channelStore.currentChannelsData as any).channels"
    :current-channel-index="(channelStore.currentChannelsData as any).current ?? 0"
    :channel-type="channelType"
    :dashboard-metrics="channelStore.currentDashboardMetrics as any"
    :dashboard-stats="channelStore.currentDashboardStats as any"
    :dashboard-recent-activity="channelStore.currentDashboardRecentActivity as any"
    :health-map="healthMap"
    class="mb-6"
    v-bind="$attrs"
  />

  <!-- 空状态 -->
  <v-card v-if="!(channelStore.currentChannelsData as any).channels?.length" elevation="2" class="text-center pa-12" rounded="lg">
    <v-avatar size="120" color="primary" class="mb-6">
      <v-icon size="60" color="white">mdi-rocket-launch</v-icon>
    </v-avatar>
    <div class="text-h4 mb-4 font-weight-bold">{{ t('channels.empty.title') }}</div>
    <div class="text-subtitle-1 text-medium-emphasis mb-8">
      {{ t('channels.empty.description') }}
    </div>
    <v-btn color="primary" size="x-large" prepend-icon="mdi-plus" variant="elevated" @click="emitAddChannel">
      {{ t('channels.empty.button') }}
    </v-btn>
  </v-card>
</template>

<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useChannelStore } from '@/stores/channel'
import { useDialogStore } from '@/stores/dialog'
import { api } from '@/services/api'
import type { ChannelHealthItem } from '@/services/api-types'
import ChannelOrchestration from '@/components/ChannelOrchestration.vue'
import { useI18n } from '@/i18n'
import { useGlobalTick } from '@/composables/useGlobalTick'
import { useEventStream } from '@/composables/useEventStream'

// 接收路由参数
const props = defineProps<{ type: string }>()

// 转换为类型安全的 channelType
const channelType = computed(() =>
  props.type as 'messages' | 'chat' | 'responses' | 'gemini' | 'images' | 'vectors'
)

const channelStore = useChannelStore()
const dialogStore = useDialogStore()
const { t } = useI18n()

// Health center data uses channelUid as the stable identity. The kind/index
// fallback keeps older backend responses compatible without cross-protocol collisions.
const healthMap = ref<Map<string, ChannelHealthItem>>(new Map())

const healthKey = (item: ChannelHealthItem) =>
  item.channelUid || `${item.channelKind}:${item.channelId}`

const loadHealthData = async () => {
  try {
    const resp = await api.getHealthCenterChannels()
    const map = new Map<string, ChannelHealthItem>()
    for (const item of resp.channels) {
      map.set(healthKey(item), item)
      map.set(`${item.channelKind}:${item.channelId}`, item)
    }
    healthMap.value = map
  } catch {
    // Silently ignore: badge rendering is optional; no health data = no badge shown.
  }
}

const healthTick = useGlobalTick(30_000, 'ChannelsView-health')
const eventStream = useEventStream()

// 事件去抖：熔断/Key 状态可能短时高频迁移，合并多次触发为一次刷新
let healthRefreshTimer: ReturnType<typeof setTimeout> | null = null
const scheduleHealthRefresh = () => {
  if (healthRefreshTimer) return
  healthRefreshTimer = setTimeout(() => {
    healthRefreshTimer = null
    void loadHealthData()
  }, 400)
}

onMounted(() => {
  void loadHealthData()
  // 事件驱动即时刷新；30s 轮询降级为兜底（事件丢/断连时仍能对齐）
  healthTick.onTick(loadHealthData)
  eventStream.on('circuit_breaker_state_changed', scheduleHealthRefresh)
  eventStream.on('key_blacklisted', scheduleHealthRefresh)
  eventStream.on('key_restored', scheduleHealthRefresh)
  eventStream.on('channel_status_changed', scheduleHealthRefresh)
})

const emitAddChannel = () => {
  // 打开添加渠道对话框
  dialogStore.openAddChannelModal()
}
</script>
