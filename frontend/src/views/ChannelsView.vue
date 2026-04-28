<template>
  <!-- 渠道编排（高密度列表模式） -->
  <ChannelOrchestration
    v-if="channelStore.currentChannelsData.channels?.length"
    :channels="channelStore.currentChannelsData.channels"
    :current-channel-index="channelStore.currentChannelsData.current ?? 0"
    :channel-type="channelType"
    :dashboard-metrics="channelStore.currentDashboardMetrics"
    :dashboard-stats="channelStore.currentDashboardStats"
    :dashboard-recent-activity="channelStore.currentDashboardRecentActivity"
    class="mb-6"
    v-bind="$attrs"
  />

  <!-- 空状态 -->
  <v-card v-if="!channelStore.currentChannelsData.channels?.length" elevation="2" class="text-center pa-12" rounded="lg">
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
import { computed } from 'vue'
import { useChannelStore } from '@/stores/channel'
import { useDialogStore } from '@/stores/dialog'
import ChannelOrchestration from '@/components/ChannelOrchestration.vue'
import { useI18n } from '@/i18n'

// 接收路由参数
const props = defineProps<{ type: string }>()

// 转换为类型安全的 channelType
const channelType = computed(() =>
  props.type as 'messages' | 'chat' | 'responses' | 'gemini' | 'images'
)

const channelStore = useChannelStore()
const dialogStore = useDialogStore()
const { t } = useI18n()

const emitAddChannel = () => {
  // 打开添加渠道对话框
  dialogStore.openAddChannelModal()
}
</script>
