<template>
  <v-tabs
    v-if="supportsSubagentRouting"
    :model-value="isSubagentTab ? 'subagent' : 'regular'"
    color="primary"
    density="comfortable"
    class="mb-4"
    @update:model-value="changeMode"
  >
    <v-tab value="regular" prepend-icon="mdi-swap-vertical-bold">{{ t('subagentRouting.regularTab') }}</v-tab>
    <v-tab value="subagent" prepend-icon="mdi-account-multiple-outline">{{ t('subagentRouting.subagentTab') }}</v-tab>
  </v-tabs>

  <SubagentRoutingPanel
    v-if="isSubagentTab"
    :channel-type="channelType as TextChannelKind"
    :available-channels="channelStore.currentChannelsData.channels"
    v-bind="$attrs"
  />

  <ChannelOrchestration
    v-else-if="(channelStore.currentChannelsData as any).channels?.length"
    :channels="(channelStore.currentChannelsData as any).channels"
    :current-channel-index="(channelStore.currentChannelsData as any).current ?? 0"
    :channel-type="channelType"
    :dashboard-metrics="channelStore.currentDashboardMetrics as any"
    :dashboard-stats="channelStore.currentDashboardStats as any"
    :dashboard-recent-activity="channelStore.currentDashboardRecentActivity as any"
    class="mb-6"
    v-bind="$attrs"
  />

  <v-card v-if="!isSubagentTab && !(channelStore.currentChannelsData as any).channels?.length" elevation="2" class="text-center pa-12" rounded="lg">
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
import { useRoute, useRouter } from 'vue-router'
import { useChannelStore } from '@/stores/channel'
import { useDialogStore } from '@/stores/dialog'
import ChannelOrchestration from '@/components/ChannelOrchestration.vue'
import SubagentRoutingPanel from '@/components/SubagentRoutingPanel.vue'
import type { TextChannelKind } from '@/services/api'
import { useI18n } from '@/i18n'

const props = defineProps<{ type: string }>()
const channelType = computed(() =>
  props.type as 'messages' | 'chat' | 'responses' | 'gemini' | 'images' | 'vectors'
)
const supportsSubagentRouting = computed(() => ['messages', 'chat', 'responses', 'gemini'].includes(props.type))
const route = useRoute()
const router = useRouter()
const isSubagentTab = computed(() => supportsSubagentRouting.value && route.query.tab === 'subagent')
const changeMode = (value: unknown) => {
  const query = { ...route.query }
  if (value === 'subagent') query.tab = 'subagent'
  else delete query.tab
  void router.replace({ query })
}

const channelStore = useChannelStore()
const dialogStore = useDialogStore()
const { t } = useI18n()
const emitAddChannel = () => dialogStore.openAddChannelModal()
</script>
