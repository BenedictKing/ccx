<script setup lang="ts">
import { ref, computed, onMounted, type Component } from 'vue'
import { useStatus } from '@/composables/useStatus'
import { useLanguage } from '@/composables/useLanguage'
import { useReleaseCheck } from '@/composables/useReleaseCheck'
import { useTheme } from '@/composables/useTheme'
import { openExternalLink } from '@/lib/external-link'
import { useUserGuide } from '@/composables/useUserGuide'
import { GetVersion } from '@bindings/github.com/BenedictKing/ccx/desktop/desktopservice'
import type { VersionInfo } from '@bindings/github.com/BenedictKing/ccx/desktop/models'
import Logo from '@/components/layout/Logo.vue'
import {
  Activity,
  Settings,
  Sliders,
  LayoutDashboard,
  Kanban,
  Monitor,
  Play,
  Square,
  Power,
  Network,
  CreditCard,
  Sparkles,
  Sun,
  Moon,
  Loader2,
  HeartPulse,
  Gauge,
  Radar,
  CircleDollarSign,
  CircleHelp
} from 'lucide-vue-next'
import type { TabValue } from '@/types'

const modelValue = defineModel<TabValue>({ required: true })

const { openUserGuide } = useUserGuide()

const { status, loading, autostartEnabled, startService, stopService, setAutostart } = useStatus()
const { locale, languageOptions, setLanguage, t } = useLanguage()
const { releaseInfo, isChecking, isStartingUpdate, manualCheck, startInAppUpdate } = useReleaseCheck()
const { theme, setTheme } = useTheme()

const themeOptions = computed(() => [
  { value: 'auto' as const, icon: Monitor, label: t('sidebar.themeAuto') },
  { value: 'dark' as const, icon: Moon, label: t('sidebar.themeDark') },
  { value: 'light' as const, icon: Sun, label: t('sidebar.themeLight') },
])

const versionInfo = ref<VersionInfo | null>(null)
const isStoreDistribution = computed(() => versionInfo.value?.distribution === 'store')
const canInstallInAppUpdate = computed(() => !!versionInfo.value?.supportsInAppUpdate)

// Store 分发由 Store 接管更新通道，桌面端不应再展示 GitHub release 胶囊
const showUpdateBadge = computed(
  () => !isStoreDistribution.value && releaseInfo.value?.status === 'update-available' && !!releaseInfo.value?.releaseUrl
)

const handleOpenRelease = () => {
  const url = releaseInfo.value?.releaseUrl
  if (!url) return
  openExternalLink(url)
}

const handleUpdateClick = async () => {
  if (canInstallInAppUpdate.value) {
    try {
      await startInAppUpdate()
      return
    } catch {
      // 回退到发布页，避免更新入口静默失败
    }
  }
  handleOpenRelease()
}

/** 点击版本号：无更新提示时触发手动检查，计入冷却时间 */
const handleVersionClick = () => {
  if (showUpdateBadge.value || isStoreDistribution.value) return
  manualCheck()
}

onMounted(async () => {
  try {
    versionInfo.value = await GetVersion()
  } catch {
    // 版本信息获取失败不影响侧栏渲染
  }
})

type MenuItem = {
  id: TabValue
  label: string
  icon: Component
  desc: string
}

const menuItems = computed<MenuItem[]>(() => [
  { id: 'status', label: t('nav.status'), icon: Activity, desc: t('nav.statusDesc') },
  { id: 'agent', label: t('nav.agent'), icon: Settings, desc: t('nav.agentDesc') },
  { id: 'channels', label: t('nav.channels'), icon: Network, desc: t('nav.channelsDesc') },
  { id: 'subscriptions', label: t('nav.subscriptions'), icon: CreditCard, desc: t('nav.subscriptionsDesc') },
  { id: 'env', label: t('nav.env'), icon: Sliders, desc: t('nav.envDesc') },
  { id: 'dashboard', label: t('nav.dashboard'), icon: LayoutDashboard, desc: t('nav.dashboardDesc') },
  { id: 'cockpit', label: t('nav.cockpit'), icon: Kanban, desc: t('nav.cockpitDesc') },
  { id: 'conversations', label: t('nav.conversations'), icon: Radar, desc: t('nav.conversationsDesc') },
  { id: 'health', label: t('nav.health'), icon: HeartPulse, desc: t('nav.healthDesc') },
  { id: 'autopilot', label: t('nav.autopilot'), icon: Gauge, desc: t('nav.autopilotDesc') },
  { id: 'reports', label: t('nav.reports'), icon: CircleDollarSign, desc: t('nav.reportsDesc') },
])

const primaryMenuItems = computed(() => menuItems.value.slice(0, 5))
const managementMenuItems = computed(() => menuItems.value.slice(5))

const statusLabel = computed(() => {
  if (status.value.running) return t('common.serviceHealthy')
  if (status.value.starting) return t('common.serviceStarting')
  return t('common.serviceDisconnected')
})

const statusGlowClass = computed(() => {
  if (status.value.running) return 'shadow-glow-green bg-emerald-500'
  if (status.value.starting) return 'shadow-glow-orange bg-amber-500'
  return 'shadow-glow-red bg-rose-500 animate-pulse'
})

const handleDaemonAction = async () => {
  if (loading.value) return
  if (status.value.running) {
    await stopService()
  } else {
    await startService()
  }
}
</script>

<template>
  <aside class="w-68 flex flex-col h-full bg-card/60 border-r border-border backdrop-blur-3xl shrink-0 select-none">
    <!-- macOS 交通灯避让区 & 标题栏拖拽区域 -->
    <div class="h-14 w-full flex items-center justify-end px-5 shrink-0" data-wails-drag>
      <!-- 将标题完美靠右边对齐，为左侧 macOS 交通灯腾出完全开阔、无阻挡的绝佳操作空间 -->
      <div class="flex items-center gap-2.5 mt-2.5">
        <!-- 引入全新设计的高能自旋转 AI 路由发光核心 Logo，上调尺寸到 32px 凸显精美细节 -->
        <Logo :size="32" />
        <span class="text-sm font-bold tracking-wider bg-clip-text text-transparent bg-gradient-to-r from-foreground to-muted-foreground">
          CCX CONTROL
        </span>
      </div>
    </div>

    <!-- 导航菜单 -->
    <nav class="flex-1 px-3 py-6 overflow-y-auto">
      <div class="space-y-1.5">
        <button
          v-for="item in primaryMenuItems"
          :key="item.id"
          @click="modelValue = item.id"
          :class="[
            'w-full flex items-center gap-3.5 px-4 py-3 rounded-lg text-left transition-all duration-300 relative group overflow-hidden',
            modelValue === item.id
              ? 'bg-primary/10 text-primary border border-primary/15'
              : 'text-muted-foreground hover:text-foreground hover:bg-secondary border border-transparent'
          ]"
        >
          <div
            v-if="modelValue === item.id"
            class="absolute left-0 top-3 bottom-3 w-1 rounded-r-full bg-primary shadow-[0_0_10px_rgba(59,130,246,0.6)]"
          />

          <component
            :is="item.icon"
            :class="[
              'w-4.5 h-4.5 shrink-0 transition-transform duration-300 group-hover:scale-110',
              modelValue === item.id ? 'text-primary' : 'text-muted-foreground group-hover:text-foreground'
            ]"
          />
          <div class="flex flex-col min-w-0">
            <span class="text-sm font-medium leading-tight">{{ item.label }}</span>
            <span class="text-[10px] text-muted-foreground mt-0.5 truncate group-hover:text-foreground transition-colors">
              {{ item.desc }}
            </span>
          </div>
        </button>
      </div>

      <div class="my-0.5 border-t border-border/60" />

      <div class="space-y-1.5">
        <button
          v-for="item in managementMenuItems"
          :key="item.id"
          @click="modelValue = item.id"
          :class="[
            'w-full flex items-center gap-3.5 px-4 py-3 rounded-lg text-left transition-all duration-300 relative group overflow-hidden',
            modelValue === item.id
              ? 'bg-primary/10 text-primary border border-primary/15'
              : 'text-muted-foreground hover:text-foreground hover:bg-secondary border border-transparent'
          ]"
        >
          <div
            v-if="modelValue === item.id"
            class="absolute left-0 top-3 bottom-3 w-1 rounded-r-full bg-primary shadow-[0_0_10px_rgba(59,130,246,0.6)]"
          />

          <component
            :is="item.icon"
            :class="[
              'w-4.5 h-4.5 shrink-0 transition-transform duration-300 group-hover:scale-110',
              modelValue === item.id ? 'text-primary' : 'text-muted-foreground group-hover:text-foreground'
            ]"
          />
          <div class="flex flex-col min-w-0">
            <span class="text-sm font-medium leading-tight">{{ item.label }}</span>
            <span class="text-[10px] text-muted-foreground mt-0.5 truncate group-hover:text-foreground transition-colors">
              {{ item.desc }}
            </span>
          </div>
        </button>
      </div>
    </nav>

    <!-- 底部常驻迷你服务守护面板 -->
    <div class="p-4 border-t border-border bg-background/30 shrink-0">
      <div class="p-3.5 rounded-xl border border-border bg-card/40 hover:bg-card/60 transition-colors">
        <div class="flex items-center justify-between mb-3.5">
          <div class="flex items-center gap-2 min-w-0">
            <!-- 霓虹呼吸指示灯 -->
            <div :class="['w-2 h-2 rounded-full transition-all duration-500 shrink-0', statusGlowClass]" />
            <span class="text-xs font-semibold text-foreground truncate">{{ statusLabel }}</span>
          </div>
          <div class="flex items-center gap-1.5 shrink-0">
            <!-- 使用指引入口 -->
            <button
              @click="openUserGuide"
              :title="t('guide.helpButton')"
              class="p-1.5 rounded-lg border border-border bg-secondary/60 text-muted-foreground hover:text-primary hover:border-primary/30 hover:bg-primary/10 transition-all duration-200 hover:scale-105 active:scale-95 cursor-pointer"
            >
              <CircleHelp class="w-3 h-3" />
            </button>
            <!-- 迷你开关控制，可快速启停 -->
            <button
              @click="handleDaemonAction"
              :disabled="loading"
              :class="[
                'p-1.5 rounded-lg border text-xs transition-all duration-200 hover:scale-105 active:scale-95 cursor-pointer',
                status.running
                  ? 'bg-rose-500/10 text-rose-700 dark:text-rose-400 border-rose-500/20 hover:bg-rose-500/20'
                  : 'bg-emerald-500/10 text-emerald-700 dark:text-emerald-400 border-emerald-500/20 hover:bg-emerald-500/20'
              ]"
            >
              <component :is="status.running ? Square : Play" class="w-3 h-3" />
            </button>
          </div>
        </div>

        <!-- 详细物理信息 -->
        <div class="space-y-1.5 text-[10px] font-mono text-muted-foreground">
          <div class="flex justify-between items-center">
            <span>{{ t('common.gatewayPort') }}</span>
            <span class="text-foreground bg-secondary px-1.5 py-0.5 rounded border border-border">
              {{ status.port || '——' }}
            </span>
          </div>
          <div class="flex justify-between items-center" v-if="status.pid">
            <span>{{ t('common.daemonPid') }}</span>
            <span class="text-foreground bg-secondary px-1.5 py-0.5 rounded border border-border">
              {{ status.pid }}
            </span>
          </div>
          <div class="flex justify-between items-center">
            <span>{{ t('common.autoStart') }}</span>
            <button
              @click="setAutostart(!autostartEnabled)"
              :class="[
                'flex items-center gap-1 px-1.5 py-0.5 rounded border transition-all duration-200 cursor-pointer',
                autostartEnabled
                  ? 'bg-primary/10 text-primary border-primary/15'
                  : 'bg-secondary text-muted-foreground border-border hover:text-foreground'
              ]"
            >
              <Power class="w-2.5 h-2.5" />
              <span>{{ autostartEnabled ? t('common.autoStartOn') : t('common.autoStartOff') }}</span>
            </button>
          </div>
          <div class="flex justify-between items-center">
            <span>{{ t('sidebar.language') }}</span>
            <div class="flex items-center gap-0.5 bg-secondary rounded border border-border p-0.5">
              <button
                v-for="option in languageOptions"
                :key="option.locale"
                @click="setLanguage(option.locale)"
                :class="[
                  'px-1.5 py-0.5 rounded transition-all duration-200 cursor-pointer',
                  locale === option.locale
                    ? 'bg-primary/15 text-primary'
                    : 'text-muted-foreground hover:text-foreground'
                ]"
              >
                {{ option.label }}
              </button>
            </div>
          </div>
          <div class="flex justify-between items-center">
            <span>{{ t('sidebar.theme') }}</span>
            <div class="flex items-center gap-0.5 bg-secondary rounded border border-border p-0.5">
              <button
                v-for="opt in themeOptions"
                :key="opt.value"
                @click="setTheme(opt.value)"
                :title="opt.label"
                :class="[
                  'px-1.5 py-0.5 rounded transition-all duration-200 cursor-pointer',
                  theme === opt.value
                    ? 'bg-primary/15 text-primary'
                    : 'text-muted-foreground hover:text-foreground'
                ]"
              >
                <component :is="opt.icon" class="w-2.5 h-2.5" />
              </button>
            </div>
          </div>
          <div class="flex justify-between items-center">
            <span>{{ t('common.version') }}</span>
            <div class="flex items-center gap-1">
              <button
                v-if="showUpdateBadge"
                type="button"
                @click="handleUpdateClick"
                class="flex items-center gap-1 px-1.5 py-0.5 rounded border border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300 hover:bg-emerald-500/20 hover:text-emerald-800 dark:hover:text-emerald-200 transition-all duration-200 cursor-pointer animate-pulse"
                :title="canInstallInAppUpdate ? t('sidebar.updateInstallHint') : t('sidebar.updateOpenReleaseHint')"
              >
                <Loader2 v-if="isStartingUpdate" class="w-2.5 h-2.5 animate-spin" />
                <Sparkles v-else class="w-2.5 h-2.5" />
                <span>{{ t('sidebar.updateAvailable', { version: releaseInfo?.latestVersion || '' }) }}</span>
              </button>
              <span
                :class="[
                  'flex items-center gap-1 px-1.5 py-0.5 rounded border transition-all duration-200',
                  showUpdateBadge || isStoreDistribution
                    ? 'bg-secondary text-muted-foreground border-border'
                    : 'bg-secondary text-foreground border-border cursor-pointer hover:bg-card hover:border-primary/30 hover:text-primary'
                ]"
                :title="isStoreDistribution ? t('sidebar.versionHintStore') : showUpdateBadge ? '' : t('sidebar.versionClickCheck')"
                @click="handleVersionClick"
              >
                <Loader2 v-if="isChecking" class="w-2.5 h-2.5 animate-spin" />
                <span>{{ versionInfo?.version || '—' }}</span>
              </span>
            </div>
          </div>
        </div>
      </div>
    </div>
  </aside>
</template>
