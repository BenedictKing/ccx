<script setup lang="ts">
import { ref, watch, computed, onBeforeUnmount, onMounted } from 'vue'
import Sidebar from '@/components/layout/Sidebar.vue'
import StatusTab from '@/components/status/StatusTab.vue'
import AgentTab from '@/components/agent/AgentTab.vue'
import EnvTab from '@/components/env/EnvTab.vue'
import ConsoleTab from '@/components/console/ConsoleTab.vue'
import ChannelTab from '@/components/channel/ChannelTab.vue'
import SubscriptionTab from '@/components/subscriptions/SubscriptionTab.vue'
import CockpitOverview from '@/components/cockpit/CockpitOverview.vue'
import HealthCenterTab from '@/components/health/HealthCenterTab.vue'
import AutopilotTab from '@/components/autopilot/AutopilotTab.vue'
import SetupLoading from '@/components/setup/SetupLoading.vue'
import SetupView from '@/components/setup/SetupView.vue'
import { TooltipProvider } from '@/components/ui/tooltip'
import { useStatus } from '@/composables/useStatus'
import { useWailsEvents } from '@/composables/useWailsEvents'
import { useSetup } from '@/composables/useSetup'
import { useLanguage } from '@/composables/useLanguage'
import { useTheme } from '@/composables/useTheme'
import { ensureDesktopRuntimePresetsLoaded } from '@/composables/useRuntimePresets'
import {
  setDesktopActiveTab,
  setDesktopConsoleSelection,
  useDesktopActivity,
} from '@/composables/useDesktopActivity'
import { useConsoleChannels } from '@/composables/useConsoleChannels'
import { useConversations } from '@/composables/useConversations'
import {
  useConsoleSelection,
  type ConsoleSelection,
} from '@/composables/useConsoleSelection'
import type { TabValue } from '@/types'

const activeTab = ref<TabValue>('status')
const { status, actionError, syncStatus } = useStatus()
const { t, initializeLanguage } = useLanguage()
const { init: initTheme } = useTheme()
const { consoleSelection, setConsoleSelection } = useConsoleSelection()
const { refreshChannels } = useConsoleChannels()
const { fetchConversations } = useConversations()
const {
  windowVisible,
  isConsoleChannelsActive,
  isConsoleConversationsActive,
} = useDesktopActivity()

const CONSOLE_CHANNEL_REFRESH_INTERVAL_MS = 5000
const CONVERSATION_REFRESH_INTERVAL_MS = 3000
let channelRefreshTimer: ReturnType<typeof setInterval> | undefined
let conversationRefreshTimer: ReturnType<typeof setInterval> | undefined
let channelRefreshPromise: Promise<void> | null = null
let conversationRefreshPromise: Promise<void> | null = null

useWailsEvents(activeTab, actionError, syncStatus)

// Setup 引导流程
const { setupChecked, setupComplete, pendingTab, checkSetup } = useSetup()

onMounted(() => {
  initTheme()
  void initializeLanguage()
  void checkSetup()
  if (status.value.running) {
    void ensureDesktopRuntimePresetsLoaded().catch(() => {})
  }
})

watch(() => status.value.running, (running) => {
  if (running) {
    void ensureDesktopRuntimePresetsLoaded(true).catch(() => {})
  }
})

// Setup 完成后跳转到目标标签页
watch(pendingTab, (tab) => {
  if (tab) {
    activeTab.value = tab
    pendingTab.value = null
  }
})

const switchToDashboard = () => {
  activeTab.value = 'dashboard'
}

const handleConsoleSelectionUpdate = (selection: ConsoleSelection) => {
  setConsoleSelection(selection)
}

async function refreshVisibleChannels() {
  if (!status.value.running || !isConsoleChannelsActive.value || channelRefreshPromise) return
  channelRefreshPromise = refreshChannels()
    .finally(() => {
      channelRefreshPromise = null
    })
  return channelRefreshPromise
}

async function refreshVisibleConversations() {
  if (!status.value.running || !isConsoleConversationsActive.value || conversationRefreshPromise) return
  conversationRefreshPromise = fetchConversations()
    .finally(() => {
      conversationRefreshPromise = null
    })
  return conversationRefreshPromise
}

function updateDashboardPolling() {
  if (channelRefreshTimer) {
    clearInterval(channelRefreshTimer)
    channelRefreshTimer = undefined
  }
  if (conversationRefreshTimer) {
    clearInterval(conversationRefreshTimer)
    conversationRefreshTimer = undefined
  }

  if (isConsoleChannelsActive.value && status.value.running) {
    void refreshVisibleChannels()
    channelRefreshTimer = setInterval(() => {
      void refreshVisibleChannels()
    }, CONSOLE_CHANNEL_REFRESH_INTERVAL_MS)
  }

  if (isConsoleConversationsActive.value && status.value.running) {
    void refreshVisibleConversations()
    conversationRefreshTimer = setInterval(() => {
      void refreshVisibleConversations()
    }, CONVERSATION_REFRESH_INTERVAL_MS)
  }
}

// 选项卡标题映射
const tabTitles = computed<Record<TabValue, string>>(() => ({
  status: t('tab.statusTitle'),
  agent: t('tab.agentTitle'),
  channels: t('tab.channelsTitle'),
  subscriptions: t('tab.subscriptionsTitle'),
  cockpit: t('tab.cockpitTitle'),
  env: t('tab.envTitle'),
  dashboard: t('tab.dashboardTitle'),
  health: t('tab.healthTitle'),
  autopilot: t('tab.autopilotTitle'),
}))

watch(activeTab, (tab) => {
  setDesktopActiveTab(tab)
}, { immediate: true })

watch(consoleSelection, (selection) => {
  setDesktopConsoleSelection(selection)
}, { immediate: true })

watch(
  [() => status.value.running, windowVisible, activeTab, consoleSelection],
  updateDashboardPolling,
  { immediate: true },
)

onBeforeUnmount(() => {
  if (channelRefreshTimer) clearInterval(channelRefreshTimer)
  if (conversationRefreshTimer) clearInterval(conversationRefreshTimer)
})
</script>

<template>
  <SetupLoading v-if="!setupChecked" />
  <SetupView v-else-if="!setupComplete" />
  <TooltipProvider v-else>
    <div class="flex h-screen w-screen bg-background text-foreground overflow-hidden font-sans">
    <!-- 全局 SVG Gradient 定义（活动图表共享） -->
    <svg aria-hidden="true" width="0" height="0" class="absolute">
      <defs>
        <linearGradient id="ccx-act-g0" x1="0%" y1="0%" x2="0%" y2="100%">
          <stop offset="0%" stop-color="rgb(34, 197, 94)" stop-opacity="0.8" />
          <stop offset="100%" stop-color="rgb(34, 197, 94)" stop-opacity="0.3" />
        </linearGradient>
        <linearGradient id="ccx-act-g1" x1="0%" y1="0%" x2="0%" y2="100%">
          <stop offset="0%" stop-color="rgb(132, 204, 22)" stop-opacity="0.8" />
          <stop offset="100%" stop-color="rgb(132, 204, 22)" stop-opacity="0.3" />
        </linearGradient>
        <linearGradient id="ccx-act-g2" x1="0%" y1="0%" x2="0%" y2="100%">
          <stop offset="0%" stop-color="rgb(250, 204, 21)" stop-opacity="0.8" />
          <stop offset="100%" stop-color="rgb(250, 204, 21)" stop-opacity="0.3" />
        </linearGradient>
        <linearGradient id="ccx-act-g3" x1="0%" y1="0%" x2="0%" y2="100%">
          <stop offset="0%" stop-color="rgb(251, 146, 60)" stop-opacity="0.8" />
          <stop offset="100%" stop-color="rgb(251, 146, 60)" stop-opacity="0.3" />
        </linearGradient>
        <linearGradient id="ccx-act-g4" x1="0%" y1="0%" x2="0%" y2="100%">
          <stop offset="0%" stop-color="rgb(249, 115, 22)" stop-opacity="0.8" />
          <stop offset="100%" stop-color="rgb(249, 115, 22)" stop-opacity="0.3" />
        </linearGradient>
        <linearGradient id="ccx-act-g5" x1="0%" y1="0%" x2="0%" y2="100%">
          <stop offset="0%" stop-color="rgb(239, 68, 68)" stop-opacity="0.8" />
          <stop offset="100%" stop-color="rgb(239, 68, 68)" stop-opacity="0.3" />
        </linearGradient>
        <linearGradient id="ccx-act-g6" x1="0%" y1="0%" x2="0%" y2="100%">
          <stop offset="0%" stop-color="rgb(220, 38, 38)" stop-opacity="0.8" />
          <stop offset="100%" stop-color="rgb(220, 38, 38)" stop-opacity="0.3" />
        </linearGradient>
      </defs>
    </svg>

    <!-- 常驻左侧高级磨砂侧边栏 -->
    <Sidebar v-model="activeTab" />

    <!-- 右侧内容主展区 -->
    <main class="flex-1 flex flex-col min-w-0 h-full relative">
      <!-- 右侧顶部精细页眉 -->
      <header class="h-14 border-b border-border bg-background/60 backdrop-blur-md flex items-center justify-between px-8 shrink-0" data-wails-drag>
        <div class="flex items-center gap-3">
          <span class="text-xs bg-blue-500/10 text-blue-700 dark:text-blue-400 font-semibold px-2 py-0.5 rounded border border-blue-500/15">
            {{ t('common.gatewayLabel') }}
          </span>
          <h2 class="text-sm font-bold text-foreground tracking-wide uppercase">
            {{ tabTitles[activeTab] }}
          </h2>
        </div>

        <div class="flex items-center gap-2">
          <!-- 实时网关状态指示微标 -->
          <span
            v-if="status.running"
            class="text-[10px] bg-emerald-500/10 text-emerald-700 dark:text-emerald-400 border border-emerald-500/20 font-bold px-2 py-0.5 rounded-full"
          >
            {{ t('common.online') }}
          </span>
          <span
            v-else-if="status.starting"
            class="text-[10px] bg-amber-500/10 text-amber-700 dark:text-amber-400 border border-amber-500/20 font-bold px-2 py-0.5 rounded-full animate-pulse"
          >
            {{ t('common.connecting') }}
          </span>
          <span
            v-else
            class="text-[10px] bg-rose-500/10 text-rose-700 dark:text-rose-400 border border-rose-500/20 font-bold px-2 py-0.5 rounded-full"
          >
            {{ t('common.offline') }}
          </span>
        </div>
      </header>

      <!-- 独立内容滚动区域 -->
      <div
        class="flex-1 overflow-y-auto"
        :class="'px-8 py-7'"
      >
        <div class="h-full">
          <!-- v-show 常驻缓存各 Tab，切换时保留内部状态与滚动位置 -->
          <div v-show="activeTab === 'status'" class="h-full">
            <StatusTab @switch-to-dashboard="switchToDashboard" />
          </div>
          <div v-show="activeTab === 'agent'" class="h-full">
            <AgentTab />
          </div>
          <div v-show="activeTab === 'channels'" class="h-full">
            <ChannelTab />
          </div>
          <div v-show="activeTab === 'subscriptions'" class="h-full">
            <SubscriptionTab />
          </div>
          <div v-show="activeTab === 'cockpit'" class="h-full">
            <CockpitOverview />
          </div>
          <div v-show="activeTab === 'dashboard'" class="h-full">
            <ConsoleTab
              :selection="consoleSelection"
              @update:selection="handleConsoleSelectionUpdate"
            />
          </div>
          <div v-show="activeTab === 'env'" class="h-full">
            <EnvTab />
          </div>
          <div v-show="activeTab === 'health'" class="h-full">
            <HealthCenterTab />
          </div>
          <div v-show="activeTab === 'autopilot'" class="h-full">
            <AutopilotTab />
          </div>
        </div>
      </div>
    </main>
    </div>
  </TooltipProvider>
</template>
