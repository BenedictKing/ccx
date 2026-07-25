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
    @trial="openTrialDialog"
  />

  <!-- 试用意图创建对话框 -->
  <v-dialog v-model="showTrialDialog" max-width="720" scrollable>
    <v-card rounded="lg">
      <v-card-title class="d-flex align-center ga-2">
        <v-icon color="deep-purple">mdi-flask-outline</v-icon>
        {{ t('manualIntent.dialogTitle') }}
      </v-card-title>
      <v-divider />
      <v-card-text class="pt-4">
        <ManualIntentForm
          :key="trialFormKey"
          :prefill-channel-kind="trialChannelKind"
          :prefill-channel-uid="trialChannelUid"
          @created="onTrialCreated"
          @close="showTrialDialog = false"
        />
      </v-card-text>
    </v-card>
  </v-dialog>

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
import type { Channel, ChannelHealthItem } from '@/services/api-types'
import ChannelOrchestration from '@/components/ChannelOrchestration.vue'
import ManualIntentForm from '@/components/ManualIntentForm.vue'
import type { ManualRoutingIntent } from '@/services/api-types'
import { useI18n } from '@/i18n'
import { useGlobalTick } from '@/composables/useGlobalTick'

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

onMounted(() => {
  void loadHealthData()
  healthTick.onTick(loadHealthData)
})

const emitAddChannel = () => {
  // 打开添加渠道对话框
  dialogStore.openAddChannelModal()
}

// 试用意图对话框
const showTrialDialog = ref(false)
const trialChannelUid = ref<string>('')
const trialChannelKind = ref(channelType.value)
const trialFormKey = ref(0)

const openTrialDialog = (channel: Channel) => {
  trialChannelUid.value = channel.channelUid ?? ''
  trialChannelKind.value = channel.routeKind ?? channelType.value
  trialFormKey.value += 1 // 强制重建表单以刷新预填
  showTrialDialog.value = true
}

const onTrialCreated = (_intent: ManualRoutingIntent) => {
  showTrialDialog.value = false
}
</script>
