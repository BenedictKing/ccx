<template>
  <v-app>
    <!-- 自动认证加载提示 - 只在真正进行自动认证时显示 -->
    <v-overlay
      :model-value="authStore.isAutoAuthenticating && !authStore.isInitialized"
      persistent
      class="align-center justify-center"
      scrim="black"
    >
      <v-card class="pa-6 text-center" max-width="400" rounded="lg">
        <v-progress-circular indeterminate :size="64" :width="6" color="primary" class="mb-4" />
        <div class="text-h6 mb-2">{{ t('app.auth.verifyingTitle') }}</div>
        <div class="text-body-2 text-medium-emphasis">{{ t('app.auth.verifyingBody') }}</div>
      </v-card>
    </v-overlay>

    <!-- 认证界面 -->
    <v-dialog v-model="showAuthDialog" persistent max-width="500">
      <v-card class="pa-4">
        <v-card-title class="text-h5 text-center mb-4"> 🔐 API Proxy - CCX </v-card-title>

        <v-card-text>
          <v-alert v-if="authStore.authError" type="error" variant="tonal" class="mb-4">
            {{ authStore.authError }}
          </v-alert>

          <v-form @submit.prevent="handleAuthSubmit">
            <v-text-field
              v-model="authStore.authKeyInput"
              :label="t('app.auth.inputLabel')"
              type="password"
              variant="outlined"
              prepend-inner-icon="mdi-key"
              :rules="[(v: string) => !!v || t('app.auth.inputRequired')]"
              required
              autofocus
              @keyup.enter="handleAuthSubmit"
            />

            <v-btn type="submit" color="primary" block size="large" class="mt-4" :loading="authStore.authLoading">
              {{ t('app.auth.submit') }}
            </v-btn>
          </v-form>

          <v-divider class="my-4" />

          <v-alert type="info" variant="tonal" density="compact" class="mb-0" :icon="false">
            <div class="text-body-2">
              <p class="mb-2"><strong>🔒 {{ t('app.auth.securityTitle') }}</strong></p>
              <ul class="ml-4 mb-0">
                <li>{{ t('app.auth.securityItem1') }}</li>
                <li>{{ t('app.auth.securityItem2') }}</li>
                <li>{{ t('app.auth.securityItem3') }}</li>
                <li>{{ t('app.auth.securityItem4') }}</li>
                <li>{{ t('app.auth.securityItem5', { attempts: MAX_AUTH_ATTEMPTS }) }}</li>
              </ul>
            </div>
          </v-alert>
        </v-card-text>
      </v-card>
    </v-dialog>

    <!-- 应用栏 - 毛玻璃效果 -->
    <v-app-bar elevation="0" :height="$vuetify.display.mobile ? 56 : 72" class="app-header">
      <template #prepend>
        <a href="https://github.com/BenedictKing/ccx" target="_blank" rel="noopener noreferrer" class="app-logo">
          <v-icon :size="$vuetify.display.mobile ? 22 : 32" color="white"> mdi-rocket-launch </v-icon>
        </a>
      </template>

      <!-- 自定义标题容器 - 替代 v-app-bar-title -->
      <div class="header-title">
        <!-- 手机端：下拉菜单（仅 xs 断点，< 600px） -->
        <v-menu v-if="$vuetify.display.xs">
          <template #activator="{ props: menuProps }">
            <v-btn
              v-bind="menuProps"
              variant="text"
              class="mobile-tab-selector text-body-2 font-weight-bold"
              append-icon="mdi-chevron-down"
            >
              {{ translatedApiTabOptions.find(tab => tab.value === channelStore.activeTab)?.label }}
            </v-btn>
          </template>
          <v-list density="compact" nav>
            <v-list-item
              v-for="tab in translatedApiTabOptions"
              :key="tab.value"
              :active="channelStore.activeTab === tab.value"
              :to="tab.route"
            >
              <v-list-item-title>{{ tab.label }}</v-list-item-title>
            </v-list-item>
          </v-list>
        </v-menu>

        <!-- 桌面端：平铺链接 -->
        <div v-else class="text-h6 font-weight-bold d-flex align-center">
          <router-link to="/channels/messages" class="api-type-text" :class="{ active: channelStore.activeTab === 'messages' }">
            {{ t('app.tabs.messages') }}
          </router-link>
          <span class="api-type-text separator">/</span>
          <router-link to="/channels/chat" class="api-type-text" :class="{ active: channelStore.activeTab === 'chat' }">
            {{ t('app.tabs.chat') }}
          </router-link>
          <span class="api-type-text separator">/</span>
          <router-link to="/channels/images" class="api-type-text" :class="{ active: channelStore.activeTab === 'images' }">
            {{ t('app.tabs.images') }}
          </router-link>
          <span class="api-type-text separator">/</span>
          <router-link to="/channels/responses" class="api-type-text" :class="{ active: channelStore.activeTab === 'responses' }">
            {{ t('app.tabs.responses') }}
          </router-link>
          <span class="api-type-text separator">/</span>
          <router-link to="/channels/gemini" class="api-type-text" :class="{ active: channelStore.activeTab === 'gemini' }">
            {{ t('app.tabs.gemini') }}
          </router-link>
          <span class="brand-text d-none d-md-inline">API Proxy - CCX</span>
        </div>
      </div>

      <v-spacer/>

      <!-- 版本信息（手机端隐藏） -->
      <div
        v-if="!$vuetify.display.xs && systemStore.versionInfo.currentVersion"
        class="version-badge"
        :class="{
          'version-clickable': systemStore.versionInfo.status === 'update-available' || systemStore.versionInfo.status === 'latest',
          'version-checking': systemStore.versionInfo.status === 'checking',
          'version-latest': systemStore.versionInfo.status === 'latest',
          'version-update': systemStore.versionInfo.status === 'update-available'
        }"
        @click="handleVersionClick"
      >
        <v-icon
          v-if="systemStore.versionInfo.status === 'checking'"
          size="14"
          class="mr-1"
        >mdi-clock-outline</v-icon>
        <v-icon
          v-else-if="systemStore.versionInfo.status === 'latest'"
          size="14"
          class="mr-1"
          color="success"
        >mdi-check-circle</v-icon>
        <v-icon
          v-else-if="systemStore.versionInfo.status === 'update-available'"
          size="14"
          class="mr-1"
          color="warning"
        >mdi-alert</v-icon>
        <span class="version-text">{{ systemStore.versionInfo.currentVersion }}</span>
        <template v-if="systemStore.versionInfo.status === 'update-available' && systemStore.versionInfo.latestVersion">
          <span class="version-arrow mx-1">→</span>
          <span class="version-latest-text">{{ systemStore.versionInfo.latestVersion }}</span>
        </template>
      </div>

      <!-- 语言切换 -->
      <v-menu location="bottom end">
        <template #activator="{ props: menuProps }">
          <v-btn
            v-bind="menuProps"
            icon
            variant="text"
            size="small"
            class="header-btn language-switch-btn"
          >
            <span class="language-switch-label">{{ currentLanguageShortLabel }}</span>
          </v-btn>
        </template>
        <v-list density="compact" nav>
          <v-list-item
            v-for="option in languageOptions"
            :key="option.value"
            :active="currentLocale === option.value"
            @click="setLocale(option.value)"
          >
            <v-list-item-title>{{ option.label }}</v-list-item-title>
          </v-list-item>
        </v-list>
      </v-menu>

      <!-- 暗色模式切换 -->
      <v-btn icon variant="text" size="small" class="header-btn" @click="toggleDarkMode">
        <v-icon size="20">{{
          theme.global.current.value.dark ? 'mdi-weather-night' : 'mdi-white-balance-sunny'
        }}</v-icon>
      </v-btn>

      <!-- 注销按钮 -->
      <v-btn
        v-if="isAuthenticated"
        icon
        variant="text"
        size="small"
        class="header-btn"
        :title="t('app.header.logout')"
        @click="handleLogout"
      >
        <v-icon size="20">mdi-logout</v-icon>
      </v-btn>
    </v-app-bar>

    <!-- 主要内容 -->
    <v-main>
      <v-container fluid class="pa-4 pa-md-6">
        <!-- 全局统计顶部可折叠卡片（根据当前 Tab 显示对应统计） -->
        <v-card v-if="isAuthenticated" class="mb-4 global-stats-panel">
          <div
            class="global-stats-header d-flex align-center justify-space-between px-4 py-2"
            style="cursor: pointer;"
            @click="preferencesStore.toggleGlobalStats()"
          >
            <div class="d-flex align-center">
              <v-icon size="20" class="mr-2">mdi-chart-areaspline</v-icon>
              <span class="text-subtitle-1 font-weight-bold">{{ activeTrafficTitle }}</span>
            </div>
            <v-btn icon size="small" variant="text">
              <v-icon>{{ preferencesStore.showGlobalStats ? 'mdi-chevron-up' : 'mdi-chevron-down' }}</v-icon>
            </v-btn>
          </div>
          <v-expand-transition>
            <div v-if="preferencesStore.showGlobalStats">
              <v-divider />
              <GlobalStatsChart :api-type="channelStore.activeTab" />
            </div>
          </v-expand-transition>
        </v-card>

        <!-- 统计卡片 - 玻璃拟态风格 -->
        <v-row class="mb-6 stat-cards-row">
          <v-col cols="6" sm="4">
            <div class="stat-card stat-card-info">
              <div class="stat-card-icon">
                <v-icon size="28">mdi-server-network</v-icon>
              </div>
              <div class="stat-card-content">
                <div class="stat-card-value">{{ channelStore.currentChannelsData.channels?.length || 0 }}</div>
                <div class="stat-card-label">{{ t('app.stats.totalChannels') }}</div>
                <div class="stat-card-desc">{{ t('app.stats.totalChannelsDesc') }}</div>
              </div>
              <div class="stat-card-glow"></div>
            </div>
          </v-col>

          <v-col cols="6" sm="4">
            <div class="stat-card stat-card-success">
              <div class="stat-card-icon">
                <v-icon size="28">mdi-check-circle</v-icon>
              </div>
              <div class="stat-card-content">
                <div class="stat-card-value">
                  {{ channelStore.activeChannelCount }}<span class="stat-card-total">/{{ channelStore.failoverChannelCount }}</span>
                </div>
                <div class="stat-card-label">{{ t('app.stats.activeChannels') }}</div>
                <div class="stat-card-desc">{{ t('app.stats.activeChannelsDesc') }}</div>
              </div>
              <div class="stat-card-glow"></div>
            </div>
          </v-col>

          <v-col cols="6" sm="4">
            <div class="stat-card" :class="systemStore.systemStatus === 'running' ? 'stat-card-emerald' : 'stat-card-error'">
              <div class="stat-card-icon" :class="{ 'pulse-animation': systemStore.systemStatus === 'running' }">
                <v-icon size="28">{{ systemStore.systemStatus === 'running' ? 'mdi-heart-pulse' : 'mdi-alert-circle' }}</v-icon>
              </div>
              <div class="stat-card-content">
                <div class="stat-card-value">{{ systemStatusText }}</div>
                <div class="stat-card-label">{{ t('app.stats.systemStatus') }}</div>
                <div class="stat-card-desc">{{ systemStatusDesc }}</div>
              </div>
              <div class="stat-card-glow"></div>
            </div>
          </v-col>
        </v-row>

        <!-- 操作按钮区域 - 现代化设计 -->
        <div class="action-bar mb-6">
          <div class="action-bar-left">
            <v-btn
              color="primary"
              size="large"
              prepend-icon="mdi-plus"
              class="action-btn action-btn-primary"
              @click="openAddChannelModal"
            >
              {{ t('app.actions.addChannel') }}
            </v-btn>

            <v-btn
              color="info"
              size="large"
              prepend-icon="mdi-speedometer"
              variant="tonal"
              :loading="channelStore.isPingingAll"
              class="action-btn"
              @click="pingAllChannels"
            >
              {{ t('app.actions.ping') }}
            </v-btn>

            <v-btn size="large" prepend-icon="mdi-refresh" variant="text" class="action-btn" @click="refreshChannels">
              {{ t('app.actions.refresh') }}
            </v-btn>
          </div>

          <div class="action-bar-right">
            <!-- CCH 计费头移除切换按钮（仅 Claude Messages 渠道相关） -->
            <v-tooltip v-if="channelStore.activeTab === 'messages'" location="bottom" content-class="ccx-tooltip">
              <template #activator="{ props }">
                <v-btn
                  v-bind="props"
                  variant="tonal"
                  size="large"
                  :loading="systemStore.stripBillingHeaderLoading"
                  :disabled="systemStore.stripBillingHeaderLoadError"
                  :color="systemStore.stripBillingHeaderLoadError ? 'error' : (preferencesStore.stripBillingHeader ? 'info' : 'default')"
                  class="action-btn"
                  @click="toggleStripBillingHeader"
                >
                  <v-icon start size="20">
                    {{ systemStore.stripBillingHeaderLoadError ? 'mdi-alert-circle-outline' : (preferencesStore.stripBillingHeader ? 'mdi-tag-off' : 'mdi-tag') }}
                  </v-icon>
                  CCH
                </v-btn>
              </template>
              <span>{{ systemStore.stripBillingHeaderLoadError ? t('tooltip.loadFailedRefresh') : (preferencesStore.stripBillingHeader ? t('tooltip.billingEnabled') : t('tooltip.billingDisabled')) }}</span>
            </v-tooltip>

            <!-- Fuzzy 模式切换按钮 -->
            <v-tooltip location="bottom" content-class="ccx-tooltip">
              <template #activator="{ props }">
                <v-btn
                  v-bind="props"
                  variant="tonal"
                  size="large"
                  :loading="systemStore.fuzzyModeLoading"
                  :disabled="systemStore.fuzzyModeLoadError"
                  :color="systemStore.fuzzyModeLoadError ? 'error' : (preferencesStore.fuzzyModeEnabled ? 'warning' : 'default')"
                  class="action-btn"
                  @click="toggleFuzzyMode"
                >
                  <v-icon start size="20">
                    {{ systemStore.fuzzyModeLoadError ? 'mdi-alert-circle-outline' : (preferencesStore.fuzzyModeEnabled ? 'mdi-shield-refresh' : 'mdi-shield-off-outline') }}
                  </v-icon>
                  Fuzzy
                </v-btn>
              </template>
              <span>{{ systemStore.fuzzyModeLoadError ? t('tooltip.loadFailedRefresh') : (preferencesStore.fuzzyModeEnabled ? t('tooltip.fuzzyEnabled') : t('tooltip.fuzzyDisabled')) }}</span>
            </v-tooltip>
          </div>
        </div>

        <!-- 渠道编排（高密度列表模式） -->
        <router-view
          @edit="editChannel"
          @delete="deleteChannel"
          @ping="pingChannel"
          @test-capability="testChannelCapability"
          @refresh="refreshChannels"
          @error="showErrorToast"
          @success="showSuccessToast"
        />
      </v-container>
    </v-main>

    <!-- 添加渠道模态框 -->
    <AddChannelModal
      v-model:show="dialogStore.showAddChannelModal"
      :channel="dialogStore.editingChannel"
      :channel-type="channelStore.activeTab"
      @save="saveChannel"
      @test-capability="testChannelCapability"
      @error="showErrorToast"
    />

    <!-- 能力测试对话框 -->
    <CapabilityTestDialog
      ref="capabilityTestDialogRef"
      v-model="showCapabilityTestDialog"
      :channel-name="capabilityTestChannelName"
      :current-tab="channelStore.activeTab"
      :capability-job="capabilityTestJob"
      :capability-rpm="capabilityTestRpm"
      @update:capability-rpm="capabilityTestRpm = $event"
      @copy-to-tab="handleCopyToTab"
      @cancel="handleCancelCapabilityTest"
      @retry-model="handleRetryCapabilityModel"
      @test-protocol="handleTestCapabilityProtocol"
    />

    <!-- 添加API密钥对话框 -->
    <v-dialog v-model="dialogStore.showAddKeyModal" max-width="500">
      <v-card rounded="lg">
        <v-card-title class="d-flex align-center">
          <v-icon class="mr-3">mdi-key-plus</v-icon>
          {{ t('app.dialog.addApiKeyTitle') }}
        </v-card-title>
        <v-card-text>
          <v-text-field
            v-model="dialogStore.newApiKey"
            :label="t('app.dialog.apiKeyLabel')"
            type="password"
            variant="outlined"
            density="comfortable"
            :placeholder="t('app.dialog.apiKeyPlaceholder')"
            @keyup.enter="addApiKey"
          />
        </v-card-text>
        <v-card-actions>
          <v-spacer/>
          <v-btn variant="text" @click="dialogStore.closeAddKeyModal()">{{ t('app.actions.cancel') }}</v-btn>
          <v-btn :disabled="!dialogStore.newApiKey.trim()" color="primary" variant="elevated" @click="addApiKey">{{ t('app.actions.add') }}</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <!-- Toast通知 -->
    <v-snackbar
      v-for="toast in toasts"
      :key="toast.id"
      v-model="toast.show"
      :color="getToastColor(toast.type)"
      :timeout="3000"
      location="top right"
      variant="elevated"
    >
      <div class="d-flex align-center">
        <v-icon class="mr-3">{{ getToastIcon(toast.type) }}</v-icon>
        {{ toast.message }}
      </div>
    </v-snackbar>

    <!-- 版本更新对话框 -->
    <VersionUpdateDialog
      v-if="showUpdateDialog"
      :model-value="showUpdateDialog"
      :version-info="updateDialogInfo"
      @update:model-value="showUpdateDialog = $event"
      @update-success="onUpdateSuccess"
    />
  </v-app>
</template>

<script setup lang="ts">
import { ref, onMounted, onUnmounted, computed, watch, defineAsyncComponent } from 'vue'
import { useTheme } from 'vuetify'
import { api, fetchHealth, ApiError, type Channel, type CapabilityTestJob, type CapabilityTestJobStartResponse, type CapabilityProtocolJobResult, type CapabilityModelJobResult, type CapabilitySnapshot } from './services/api'
import { versionService } from './services/version'
import { useAuthStore } from './stores/auth'
import { useChannelStore } from './stores/channel'
import { usePreferencesStore } from './stores/preferences'
import { useDialogStore } from './stores/dialog'
import { useSystemStore } from './stores/system'
import { useI18n } from './i18n'
import type { SupportedLocale } from './i18n'
import AddChannelModal from './components/AddChannelModal.vue'
import CapabilityTestDialog from './components/CapabilityTestDialog.vue'
import VersionUpdateDialog from './components/VersionUpdateDialog.vue'
import type { VersionInfo } from './services/version'
// 异步加载图表组件，减少首屏 JS 体积
const GlobalStatsChart = defineAsyncComponent(() => import('./components/GlobalStatsChart.vue'))
import { useAppTheme } from './composables/useTheme'

// Vuetify主题
const theme = useTheme()

// 应用主题系统
const { init: initTheme } = useAppTheme()

// 认证 Store
// 注意：as any 是 Pinia 3.x + Vue 3.5 + TS 6.x 兼容补丁——
// Vue 3.5 将 Ref<T> 改为 Ref<T, S>，Pinia 的 UnwrapRef<Ref<infer V, unknown>> 模式失效，
// 导致模板中访问 store 属性时类型未被自动解包。运行时行为正常。
const authStore = useAuthStore() as any

// 渠道 Store
const channelStore = useChannelStore() as any

// 偏好设置 Store
const preferencesStore = usePreferencesStore() as any

// 对话框 Store
const dialogStore = useDialogStore() as any

// 系统状态 Store
const systemStore = useSystemStore() as any
const { locale, t, setLocale } = useI18n()

const languageOptions: Array<{ value: SupportedLocale, label: string, shortLabel: string }> = [
  { value: 'en', label: 'English', shortLabel: 'EN' },
  { value: 'id', label: 'Bahasa Indonesia', shortLabel: 'ID' },
  { value: 'zh-CN', label: '简体中文', shortLabel: 'ZH' },
]

const currentLocale = computed(() => locale.value)
const currentLanguageShortLabel = computed(() => {
  return languageOptions.find(option => option.value === currentLocale.value)?.shortLabel ?? currentLocale.value.slice(0, 2).toUpperCase()
})

// API 类型 Tab 选项（移动端下拉菜单使用）
const apiTabOptions = [
  { value: 'messages', labelKey: 'app.tabs.messages', route: '/channels/messages' },
  { value: 'chat', labelKey: 'app.tabs.chat', route: '/channels/chat' },
  { value: 'images', labelKey: 'app.tabs.images', route: '/channels/images' },
  { value: 'responses', labelKey: 'app.tabs.responses', route: '/channels/responses' },
  { value: 'gemini', labelKey: 'app.tabs.gemini', route: '/channels/gemini' },
] as const

const translatedApiTabOptions = computed(() => {
  return apiTabOptions.map(tab => ({
    ...tab,
    label: t(tab.labelKey),
  }))
})

const currentTabLabel = computed(() => {
  return translatedApiTabOptions.value.find(tab => tab.value === channelStore.activeTab)?.label || channelStore.activeTab
})

const activeTrafficTitle = computed(() => t('app.stats.trafficTitle', { tab: currentTabLabel.value }))

const systemStatusText = computed(() => {
  switch (systemStore.systemStatus) {
    case 'running':
      return t('system.running')
    case 'error':
      return t('system.error')
    case 'connecting':
      return t('system.connecting')
    default:
      return t('system.unknown')
  }
})

const systemStatusDesc = computed(() => {
  switch (systemStore.systemStatus) {
    case 'running':
      return t('system.runningDesc')
    case 'error':
      return t('system.errorDesc')
    case 'connecting':
      return t('system.connectingDesc')
    default:
      return ''
  }
})

// 对话框状态已迁移到 DialogStore

// 主题和偏好设置已迁移到 PreferencesStore

// 系统状态已迁移到 SystemStore

// Toast通知系统
interface Toast {
  id: number
  message: string
  type: 'success' | 'error' | 'warning' | 'info'
  show?: boolean
}
const toasts = ref<Toast[]>([])
let toastId = 0

// Toast工具函数
const getToastColor = (type: string) => {
  const colorMap: Record<string, string> = {
    success: 'success',
    error: 'error',
    warning: 'warning',
    info: 'info'
  }
  return colorMap[type] || 'info'
}

const getToastIcon = (type: string) => {
  const iconMap: Record<string, string> = {
    success: 'mdi-check-circle',
    error: 'mdi-alert-circle',
    warning: 'mdi-alert',
    info: 'mdi-information'
  }
  return iconMap[type] || 'mdi-information'
}

// 工具函数
const showToast = (message: string, type: 'success' | 'error' | 'warning' | 'info' = 'info') => {
  const toast: Toast = { id: ++toastId, message, type, show: true }
  toasts.value.push(toast)
  setTimeout(() => {
    const index = toasts.value.findIndex(t => t.id === toast.id)
    if (index > -1) toasts.value.splice(index, 1)
  }, 3000)
}

const _handleError = (error: unknown, defaultMessage: string) => {
  const message = error instanceof Error ? error.message : defaultMessage
  showToast(message, 'error')
  console.error(error)
}

// 直接显示错误消息（供子组件事件使用）
const showErrorToast = (message: string) => {
  showToast(message, 'error')
}

// 直接显示成功消息（供子组件事件使用）
const showSuccessToast = (message: string) => {
  showToast(message, 'info')
}

// 主要功能函数 - 使用 ChannelStore
const refreshChannels = async () => {
  try {
    await channelStore.refreshChannels()
  } catch (error) {
    handleAuthError(error)
  }
}

const saveChannel = async (channel: Omit<Channel, 'index' | 'latency' | 'status'>, options?: { isQuickAdd?: boolean; triggerCapabilityTest?: boolean }) => {
  try {
    const result = await channelStore.saveChannel(channel, dialogStore.editingChannel?.index ?? null, options)
    showToast(result.message, 'success')
    if (result.quickAddMessage) {
      showToast(result.quickAddMessage, 'info')
    }
    dialogStore.closeAddChannelModal()
    await refreshChannels()

    if (options?.triggerCapabilityTest && result.channelId !== undefined) {
      testChannelCapability(result.channelId)
    }

    return result
  } catch (error) {
    handleAuthError(error)
    return undefined
  }
}

const editChannel = (channel: Channel) => {
  dialogStore.openEditChannelModal(channel)
}

const deleteChannel = async (channelId: number) => {
  if (!confirm(t('toast.confirmDeleteChannel'))) return

  try {
    const result = await channelStore.deleteChannel(channelId)
    showToast(result.message, 'success')
  } catch (error) {
    handleAuthError(error)
  }
}

const openAddChannelModal = () => {
  dialogStore.openAddChannelModal()
}

const _openAddKeyModal = (channelId: number) => {
  dialogStore.openAddKeyModal(channelId)
}

const addApiKey = async () => {
  if (!dialogStore.newApiKey.trim()) return

  try {
    if (channelStore.activeTab === 'chat') {
      await api.addChatApiKey(dialogStore.selectedChannelForKey, dialogStore.newApiKey.trim())
    } else if (channelStore.activeTab === 'images') {
      await api.addImagesApiKey(dialogStore.selectedChannelForKey, dialogStore.newApiKey.trim())
    } else if (channelStore.activeTab === 'gemini') {
      await api.addGeminiApiKey(dialogStore.selectedChannelForKey, dialogStore.newApiKey.trim())
    } else if (channelStore.activeTab === 'responses') {
      await api.addResponsesApiKey(dialogStore.selectedChannelForKey, dialogStore.newApiKey.trim())
    } else {
      await api.addApiKey(dialogStore.selectedChannelForKey, dialogStore.newApiKey.trim())
    }
    showToast(t('toast.apiKeyAdded'), 'success')
    dialogStore.closeAddKeyModal()
    await refreshChannels()
  } catch (error) {
    showToast(t('toast.apiKeyAddFailed', { message: error instanceof Error ? error.message : t('system.unknown') }), 'error')
  }
}

const _removeApiKey = async (channelId: number, apiKey: string) => {
  if (!confirm(t('toast.confirmDeleteApiKey'))) return

  try {
    if (channelStore.activeTab === 'chat') {
      await api.removeChatApiKey(channelId, apiKey)
    } else if (channelStore.activeTab === 'images') {
      await api.removeImagesApiKey(channelId, apiKey)
    } else if (channelStore.activeTab === 'gemini') {
      await api.removeGeminiApiKey(channelId, apiKey)
    } else if (channelStore.activeTab === 'responses') {
      await api.removeResponsesApiKey(channelId, apiKey)
    } else {
      await api.removeApiKey(channelId, apiKey)
    }
    showToast(t('toast.apiKeyDeleted'), 'success')
    await refreshChannels()
  } catch (error) {
    showToast(t('toast.apiKeyDeleteFailed', { message: error instanceof Error ? error.message : t('system.unknown') }), 'error')
  }
}

const pingChannel = async (channelId: number) => {
  try {
    await channelStore.pingChannel(channelId)
    // 不再使用 Toast，延迟结果直接显示在渠道列表中
  } catch (error) {
    showToast(t('toast.latencyFailed', { message: error instanceof Error ? error.message : t('system.unknown') }), 'error')
  }
}

// ============== 能力测试 ==============

const showCapabilityTestDialog = ref(false)
const capabilityTestChannelName = ref('')
const capabilityTestChannelId = ref(0)
const capabilityTestChannelType = ref<CapabilityChannelKind>('messages')
const capabilityTestSourceTab = ref<CapabilityChannelKind>('messages')
const capabilityTestDialogRef = ref<InstanceType<typeof CapabilityTestDialog> | null>(null)
const capabilityTestJobId = ref('')
const capabilityPollers = ref<Record<string, ReturnType<typeof setInterval>>>({})
const capabilityTestJob = ref<CapabilityTestJob | null>(null)
const capabilityTestRpm = ref(10)
const capabilityTestPreviousJobId = ref('') // 记录上一次的 jobId，用于复用成功结果
const capabilityRetryPendingUntil = ref<Record<string, number>>({})

type CapabilityChannelKind = 'messages' | 'chat' | 'responses' | 'gemini'

const isCapabilityChannelKind = (tab: string): tab is CapabilityChannelKind => {
  return tab === 'messages' || tab === 'chat' || tab === 'responses' || tab === 'gemini'
}

const serviceTypeToChannelKind = (serviceType: string): CapabilityChannelKind => {
  switch (serviceType) {
    case 'claude': return 'messages'
    case 'openai': return 'chat'
    case 'responses': return 'responses'
    case 'gemini': return 'gemini'
    default: return 'chat'
  }
}

const capabilityPlaceholderModels: Record<string, string[]> = {
  // ⚠️ 修改此处时必须同步修改后端 backend-go/internal/handlers/capability_probe_models.go
  // 用于开始接口返回前的首屏占位
  messages: ['claude-opus-4-7', 'claude-opus-4-6', 'claude-sonnet-4-6', 'claude-sonnet-4-5-20250929', 'claude-haiku-4-5-20251001'],
  chat: ['gpt-5.5', 'gpt-5.4', 'gpt-5.3-codex', 'gpt-5.2', 'gpt-5.2-codex'],
  responses: ['gpt-5.5', 'gpt-5.4', 'gpt-5.3-codex', 'gpt-5.2', 'gpt-5.2-codex'],
  gemini: ['gemini-3.1-pro-preview', 'gemini-3.1-pro', 'gemini-3-pro-preview', 'gemini-3-pro', 'gemini-3-flash-preview', 'gemini-3-flash'],
  images: ['gpt-image-1', 'dall-e-3', 'dall-e-2']
}

// 复合协议支持：将 from->to 的 from 映射到对应的占位模型集
const getPlaceholderModelsForProtocol = (protocol: string): string[] => {
  if (protocol.includes('->')) {
    const from = protocol.split('->')[0]
    return capabilityPlaceholderModels[from] ?? []
  }
  return capabilityPlaceholderModels[protocol] ?? []
}

const capabilityBaseProtocolOrder = ['messages', 'responses', 'chat', 'gemini'] as const
type CapabilityBaseProtocol = typeof capabilityBaseProtocolOrder[number]

// 判断协议是否为已知协议（基础协议 或 复合协议 from->to，其中 from 是已知基础协议）
const isCapabilityProtocol = (protocol: string): boolean => {
  if (capabilityBaseProtocolOrder.includes(protocol as CapabilityBaseProtocol)) return true
  if (protocol.includes('->')) {
    const from = protocol.split('->')[0]
    return capabilityBaseProtocolOrder.includes(from as CapabilityBaseProtocol)
  }
  return false
}

const buildCapabilityModels = (
  protocol: string,
  status: CapabilityModelJobResult['status'],
  models?: string[]
): CapabilityModelJobResult[] => {
  const now = new Date().toISOString()
  const targetModels = models?.length ? models : getPlaceholderModelsForProtocol(protocol)
  return targetModels.map(model => ({
    model,
    status,
    lifecycle: status === 'running' ? 'active' : 'pending',
    outcome: 'unknown',
    success: false,
    latency: 0,
    streamingSupported: false,
    testedAt: now
  }))
}

const buildCapabilityProtocolResult = (
  protocol: string,
  status: CapabilityProtocolJobResult['status'],
  models?: string[]
): CapabilityProtocolJobResult => {
  const now = new Date().toISOString()
  const modelStatus: CapabilityModelJobResult['status'] = status === 'running' ? 'running' : status === 'queued' ? 'queued' : 'idle'
  const modelResults = buildCapabilityModels(protocol, modelStatus, models)
  return {
    protocol,
    status,
    lifecycle: status === 'running' ? 'active' : 'pending',
    outcome: 'unknown',
    success: false,
    latency: 0,
    streamingSupported: false,
    testedModel: '',
    modelResults,
    successCount: 0,
    attemptedModels: modelResults.length,
    testedAt: now
  }
}

const toRetryingCapabilityModel = (modelResult: CapabilityModelJobResult): CapabilityModelJobResult => ({
  ...modelResult,
  status: 'running',
  lifecycle: 'active',
  outcome: 'unknown',
  success: false,
  error: undefined,
  reason: undefined,
})

const markCapabilityModelRetrying = (job: CapabilityTestJob, protocol: string, model: string): CapabilityTestJob => ({
  ...job,
  tests: job.tests.map(test => {
    if (test.protocol !== protocol) return test
    return {
      ...test,
      modelResults: (test.modelResults ?? []).map(modelResult => {
        if (modelResult.model !== model) return modelResult
        return toRetryingCapabilityModel(modelResult)
      })
    }
  })
})

const applyCapabilityRetryPending = (
  job: CapabilityTestJob,
  pendingMap: Record<string, number>,
  now: number
): CapabilityTestJob => ({
  ...job,
  tests: job.tests.map(test => ({
    ...test,
    modelResults: (test.modelResults ?? []).map(modelResult => {
      const key = `${test.protocol}:${modelResult.model}`
      const pendingUntil = pendingMap[key]
      if (!pendingUntil || now >= pendingUntil) {
        delete pendingMap[key]
        return modelResult
      }
      if (modelResult.lifecycle === 'pending' || modelResult.lifecycle === 'active') {
        return modelResult
      }
      return toRetryingCapabilityModel(modelResult)
    })
  }))
})

const isIdleCapabilityTest = (test: CapabilityProtocolJobResult): boolean => {
  return (test.status as string) === 'idle'
}

const isActiveCapabilityTest = (test: CapabilityProtocolJobResult): boolean => {
  return test.lifecycle === 'active' || test.status === 'running'
}

const isBusyCapabilityTest = (test: CapabilityProtocolJobResult): boolean => {
  return !isIdleCapabilityTest(test) && (test.lifecycle === 'pending' || test.lifecycle === 'active' || test.status === 'queued' || test.status === 'running')
}

const isPendingCapabilityTest = (test: CapabilityProtocolJobResult): boolean => {
  return !isIdleCapabilityTest(test) && test.lifecycle === 'pending'
}

const isSuccessfulCapabilityTest = (test: CapabilityProtocolJobResult): boolean => {
  return test.success || test.outcome === 'success'
}

const getCapabilityAggregateState = (tests: CapabilityProtocolJobResult[]): {
  status: CapabilityTestJob['status']
  lifecycle: CapabilityTestJob['lifecycle']
  outcome: CapabilityTestJob['outcome']
  activeOperations: number
} => {
  const nonIdleTests = tests.filter(test => !isIdleCapabilityTest(test))
  const activeOperations = tests.filter(isActiveCapabilityTest).length
  if (nonIdleTests.length === 0) {
    return { status: 'idle' as const, lifecycle: 'pending' as const, outcome: 'unknown' as const, activeOperations: 0 }
  }
  if (activeOperations > 0) {
    return { status: 'running' as const, lifecycle: 'active' as const, outcome: 'unknown' as const, activeOperations }
  }
  if (tests.some(isPendingCapabilityTest)) {
    return { status: 'queued' as const, lifecycle: 'pending' as const, outcome: 'unknown' as const, activeOperations: 0 }
  }

  const cancelledCount = nonIdleTests.filter(test => test.lifecycle === 'cancelled' || test.outcome === 'cancelled').length
  if (cancelledCount === nonIdleTests.length) {
    return { status: 'cancelled' as const, lifecycle: 'cancelled' as const, outcome: 'cancelled' as const, activeOperations: 0 }
  }

  const successCount = nonIdleTests.filter(isSuccessfulCapabilityTest).length
  if (successCount === 0) {
    return { status: 'failed' as const, lifecycle: 'done' as const, outcome: 'failed' as const, activeOperations: 0 }
  }

  const outcome = successCount === tests.length ? 'success' : 'partial'
  return { status: 'completed' as const, lifecycle: 'done' as const, outcome, activeOperations: 0 }
}

const buildCapabilityProgress = (tests: CapabilityProtocolJobResult[]) => {
  const progress = {
    totalModels: 0,
    queuedModels: 0,
    runningModels: 0,
    successModels: 0,
    failedModels: 0,
    skippedModels: 0,
    completedModels: 0
  }

  for (const test of tests) {
    for (const modelResult of test.modelResults ?? []) {
      progress.totalModels += 1
      if ((modelResult.status as string) === 'idle') continue
      if (modelResult.lifecycle === 'active' || modelResult.status === 'running') {
        progress.runningModels += 1
        continue
      }
      if (modelResult.lifecycle === 'pending') {
        progress.queuedModels += 1
        continue
      }
      if (modelResult.status === 'success' || modelResult.outcome === 'success') {
        progress.successModels += 1
        progress.completedModels += 1
        continue
      }
      if (modelResult.status === 'skipped' || modelResult.lifecycle === 'cancelled') {
        progress.skippedModels += 1
        progress.completedModels += 1
        continue
      }
      progress.failedModels += 1
      progress.completedModels += 1
    }
  }

  return progress
}

// normalizeCapabilityTests 将测试结果归一化：
// 1. 保留所有已知协议（含复合协议），复合协议排在最前
// 2. 补齐缺失的基础协议（以 idle 状态占位）
const mergeCapabilityProtocolResult = (baseTest: CapabilityProtocolJobResult, incomingTest: CapabilityProtocolJobResult): CapabilityProtocolJobResult => {
  const modelResultsByModel = new Map<string, CapabilityModelJobResult>()
  for (const modelResult of baseTest.modelResults ?? []) {
    modelResultsByModel.set(modelResult.model, modelResult)
  }
  for (const modelResult of incomingTest.modelResults ?? []) {
    modelResultsByModel.set(modelResult.model, modelResult)
  }
  const modelResults = Array.from(modelResultsByModel.values())

  const attemptedModels = modelResults.filter(modelResult => (modelResult.status as string) !== 'idle').length

  return {
    ...baseTest,
    ...incomingTest,
    modelResults,
    attemptedModels,
    successCount: modelResults.filter(modelResult => modelResult.status === 'success' || modelResult.outcome === 'success').length
  }
}

const normalizeCapabilityTests = (tests: CapabilityProtocolJobResult[]): CapabilityProtocolJobResult[] => {
  const testsByProtocol = new Map<string, CapabilityProtocolJobResult>()

  for (const test of tests) {
    if (!isCapabilityProtocol(test.protocol)) continue
    const existingTest = testsByProtocol.get(test.protocol)
    testsByProtocol.set(test.protocol, existingTest ? mergeCapabilityProtocolResult(existingTest, test) : test)
  }

  const compositeTests = Array.from(testsByProtocol.values()).filter(test => test.protocol.includes('->'))
  const baseTests = capabilityBaseProtocolOrder.map(protocol =>
    testsByProtocol.get(protocol) ?? buildCapabilityProtocolResult(protocol, 'idle')
  )

  // 复合协议排在基础协议前面
  return [...compositeTests, ...baseTests]
}

const buildCapabilityIdleJob = (channelId: number, channelName: string, channelKind: CapabilityChannelKind): CapabilityTestJob => {
  const now = new Date().toISOString()
  const tests = capabilityBaseProtocolOrder.map(protocol => buildCapabilityProtocolResult(protocol, 'idle'))
  const progress = buildCapabilityProgress(tests)

  return {
    jobId: '',
    channelId,
    channelName,
    channelKind,
    sourceType: '',
    status: 'idle',
    lifecycle: 'pending',
    outcome: 'unknown',
    runMode: 'fresh',
    tests,
    compatibleProtocols: [],
    totalDuration: 0,
    updatedAt: now,
    targetProtocols: [...capabilityBaseProtocolOrder],
    progress
  }
}

const mergeCapabilityJob = (baseJob: CapabilityTestJob, incomingJob: CapabilityTestJob): CapabilityTestJob => {
  const tests = normalizeCapabilityTests([
    ...baseJob.tests,
    ...incomingJob.tests
  ])
  const aggregate = getCapabilityAggregateState(tests)
  const protocolsInIncoming = incomingJob.tests
    .map(test => test.protocol)
    .filter(isCapabilityProtocol)
  const protocolJobIds = { ...(baseJob.protocolJobIds ?? {}), ...(incomingJob.protocolJobIds ?? {}) }
  const protocolJobRefs = { ...(baseJob.protocolJobRefs ?? {}), ...(incomingJob.protocolJobRefs ?? {}) }

  if (incomingJob.jobId) {
    for (const protocol of protocolsInIncoming) {
      const incomingProtocolJobId = incomingJob.protocolJobRefs?.[protocol]?.jobId || incomingJob.protocolJobIds?.[protocol] || incomingJob.jobId
      protocolJobIds[protocol] = incomingProtocolJobId
      protocolJobRefs[protocol] = incomingJob.protocolJobRefs?.[protocol] ?? {
        jobId: incomingProtocolJobId,
        channelKind: incomingJob.channelKind as CapabilityChannelKind,
        channelId: incomingJob.channelId
      }
    }
  }

  return {
    ...baseJob,
    ...incomingJob,
    protocolJobIds,
    protocolJobRefs,
    status: aggregate.status,
    lifecycle: aggregate.lifecycle,
    outcome: aggregate.outcome,
    activeOperations: aggregate.activeOperations,
    tests,
    compatibleProtocols: tests.filter(isSuccessfulCapabilityTest).map(test => test.protocol),
    progress: buildCapabilityProgress(tests),
    targetProtocols: [...capabilityBaseProtocolOrder],
    updatedAt: incomingJob.updatedAt || baseJob.updatedAt || new Date().toISOString()
  }
}

const getCapabilitySnapshotJobId = (snapshot: CapabilitySnapshot): string => {
  const activeProtocol = snapshot.tests.find(test => test.lifecycle === 'active' || test.lifecycle === 'pending')?.protocol
  if (activeProtocol) {
    return snapshot.protocolJobRefs?.[activeProtocol]?.jobId || snapshot.protocolJobIds?.[activeProtocol] || ''
  }
  return Object.values(snapshot.protocolJobIds ?? {})[0] ?? ''
}

const buildCapabilityJobFromSnapshot = (
  snapshot: CapabilitySnapshot,
  channelId: number,
  channelName: string,
  channelKind: CapabilityChannelKind
): CapabilityTestJob => {
  const baseJob = buildCapabilityIdleJob(channelId, channelName, channelKind)
  const snapshotJobId = getCapabilitySnapshotJobId(snapshot)
  const snapshotJob: CapabilityTestJob = {
    ...baseJob,
    jobId: snapshotJobId,
    protocolJobIds: snapshot.protocolJobIds,
    protocolJobRefs: snapshot.protocolJobRefs,
    sourceType: snapshot.sourceType,
    tests: snapshot.tests,
    compatibleProtocols: snapshot.compatibleProtocols,
    totalDuration: snapshot.totalDuration,
    progress: snapshot.progress,
    lifecycle: snapshot.lifecycle,
    outcome: snapshot.outcome,
    status: snapshot.lifecycle === 'active' ? 'running' : snapshot.lifecycle === 'cancelled' ? 'cancelled' : snapshot.lifecycle === 'done' ? 'completed' : 'queued',
    updatedAt: snapshot.updatedAt,
    snapshotUpdatedAt: snapshot.updatedAt
  }
  return {
    ...mergeCapabilityJob(baseJob, snapshotJob),
    snapshotUpdatedAt: snapshot.updatedAt
  }
}

watch(showCapabilityTestDialog, (open) => {
  if (!open) {
    stopAllCapabilityPolling()
    capabilityRetryPendingUntil.value = {}
  }
})

const collectActiveJobIds = (job: CapabilityTestJob | null): string[] => {
  if (!job) return []
  const seen = new Set<string>()
  for (const test of job.tests) {
    if (test.lifecycle === 'active' || test.lifecycle === 'pending') {
      const jId = job.protocolJobRefs?.[test.protocol]?.jobId || job.protocolJobIds?.[test.protocol]
      if (jId && !seen.has(jId)) seen.add(jId)
    }
  }
  return Array.from(seen)
}

const isCapabilityJobTerminal = (job: CapabilityTestJob | null | undefined) => {
  if (!job) return false
  return job.lifecycle === 'done' || job.lifecycle === 'cancelled'
}
const stopCapabilityPolling = (jobId: string) => {
  if (!jobId || !capabilityPollers.value[jobId]) return
  clearInterval(capabilityPollers.value[jobId])
  delete capabilityPollers.value[jobId]
}

const stopAllCapabilityPolling = () => {
  for (const jobId of Object.keys(capabilityPollers.value)) {
    clearInterval(capabilityPollers.value[jobId])
  }
  capabilityPollers.value = {}
}

const startCapabilityPolling = (channelType: CapabilityChannelKind, channelId: number, jobId: string) => {
  if (!jobId || capabilityPollers.value[jobId]) return
  capabilityPollers.value[jobId] = setInterval(async () => {
    if (!jobId) return
    try {
      const latest = await api.getChannelCapabilityTestStatus(channelType, channelId, jobId)
      updateCapabilityJob(latest)
    } catch (error) {
      console.error('Failed to poll capability test job:', error)
    }
  }, 1000)
}

const updateCapabilityJob = (job: CapabilityTestJob) => {
  const incomingJob = applyCapabilityRetryPending(job, capabilityRetryPendingUntil.value, Date.now())
  const currentJob = capabilityTestJob.value
  const channelKind = isCapabilityChannelKind(job.channelKind)
    ? job.channelKind
    : isCapabilityChannelKind(channelStore.activeTab)
      ? channelStore.activeTab
      : 'messages'
  const baseJob = currentJob && currentJob.channelId === job.channelId && currentJob.channelKind === job.channelKind
    ? currentJob
    : buildCapabilityIdleJob(job.channelId, job.channelName, channelKind)
  const mergedJob = mergeCapabilityJob(baseJob, incomingJob)

  capabilityTestJob.value = mergedJob
  capabilityTestJobId.value = job.jobId
  if (isCapabilityJobTerminal(job)) {
    stopCapabilityPolling(job.jobId)
  }
}

const getCapabilityPreviousJobId = (protocol: string): string | undefined => {
  const currentJob = capabilityTestJob.value
  return currentJob?.protocolJobRefs?.[protocol]?.jobId ||
    currentJob?.protocolJobIds?.[protocol] ||
    capabilityTestPreviousJobId.value ||
    undefined
}

const testChannelCapability = async (channelId: number) => {
  if (!isCapabilityChannelKind(channelStore.activeTab)) {
    showToast(t('toast.unsupportedProtocol', { protocol: channelStore.activeTab }), 'warning')
    return
  }

  const channel = channelStore.currentChannelsData.channels?.find((ch: Channel) => ch.index === channelId)
  if (!channel) {
    console.error('Channel not found:', channelId)
    return
  }

  // 从渠道的实际 serviceType 推导 channelKind，而不是从 activeTab
  const channelType = channelStore.activeTab  // API 路径由渠道配置位置决定
  const sourceTab = channelStore.activeTab  // 当前查看的 Tab 协议类型
  capabilityTestChannelName.value = channel.name || t('capability.channelFallback', { id: channelId })
  capabilityTestChannelId.value = channelId
  capabilityTestChannelType.value = channelType
  capabilityTestSourceTab.value = sourceTab

  if (dialogStore.showAddChannelModal) {
    dialogStore.closeAddChannelModal()
  }

  showCapabilityTestDialog.value = true
  stopAllCapabilityPolling()
  capabilityTestPreviousJobId.value = capabilityTestJobId.value
  capabilityTestJobId.value = ''
  capabilityTestJob.value = buildCapabilityIdleJob(channelId, capabilityTestChannelName.value, channelType)

  try {
    // sourceTab 是渠道的实际协议类型，channelType 是 API 路径
    const snapshot = await api.getChannelCapabilitySnapshot(channelType, channelId, sourceTab)
    if (capabilityTestChannelId.value !== channelId || capabilityTestChannelType.value !== channelType) return
    const snapshotJob = buildCapabilityJobFromSnapshot(snapshot, channelId, capabilityTestChannelName.value, channelType)
    capabilityTestJob.value = snapshotJob
    capabilityTestJobId.value = snapshotJob.jobId
    if (!isCapabilityJobTerminal(snapshotJob)) {
      const activeIds = collectActiveJobIds(snapshotJob)
      for (const jId of activeIds) {
        startCapabilityPolling(channelType, channelId, jId)
      }
    }
  } catch (error) {
    if (error instanceof ApiError && error.status === 404) return
    const message = error instanceof Error ? error.message : t('system.unknown')
    capabilityTestDialogRef.value?.setError(t('toast.capabilityFailed', { message }))
  }
}

const handleTestCapabilityProtocol = async (protocol: string, models?: string[]) => {
  if (!isCapabilityChannelKind(channelStore.activeTab) || !isCapabilityProtocol(protocol)) {
    return
  }
  if (!capabilityTestChannelId.value) return

  const channelType = capabilityTestChannelType.value
  const channelId = capabilityTestChannelId.value
  const previousJobId = getCapabilityPreviousJobId(protocol)
  const currentJob = capabilityTestJob.value ?? buildCapabilityIdleJob(channelId, capabilityTestChannelName.value, channelType)
  capabilityTestJob.value = mergeCapabilityJob(currentJob, {
    ...currentJob,
    jobId: '',
    status: 'queued',
    lifecycle: 'pending',
    outcome: 'unknown',
    tests: [buildCapabilityProtocolResult(protocol, 'queued', models)],
    targetProtocols: [protocol],
    updatedAt: new Date().toISOString()
  })
  try {
    console.log('[CapabilityTest] Starting test with sourceTab:', capabilityTestSourceTab.value, 'channelType:', channelType)
    const startResp: CapabilityTestJobStartResponse = await api.startChannelCapabilityTest(
      channelType,
      channelId,
      {
        targetProtocols: [protocol],
        previousJobId,
        rpm: capabilityTestRpm.value,
        sourceTab: capabilityTestSourceTab.value,
        models
      }
    )
    capabilityTestJobId.value = startResp.jobId

    if (startResp.job) {
      updateCapabilityJob(startResp.job)
    }

    if (isCapabilityJobTerminal(startResp.job) && !(startResp.job?.activeOperations && startResp.job.activeOperations > 0)) {
      return
    }

    startCapabilityPolling(channelType, channelId, startResp.jobId)
  } catch (error) {
    const message = error instanceof Error ? error.message : t('system.unknown')
    capabilityTestDialogRef.value?.setError(t('toast.capabilityFailed', { message }))
  }
}

const handleTestCapabilityProtocolWithModels = handleTestCapabilityProtocol

const handleCancelCapabilityTest = async () => {
  if (!capabilityTestJob.value) return
  if (!capabilityTestChannelType.value) return
  try {
    const activeIds = collectActiveJobIds(capabilityTestJob.value)
    const channelType = capabilityTestChannelType.value
    const channelId = capabilityTestChannelId.value
    for (const jId of activeIds) {
      await api.cancelCapabilityTest(channelType, channelId, jId).catch(err =>
        console.error('Failed to cancel capability test job:', jId, err)
      )
    }
    stopAllCapabilityPolling()
    const snapshot = await api.getChannelCapabilitySnapshot(channelType, channelId, channelStore.activeTab)
    const snapshotJob = buildCapabilityJobFromSnapshot(snapshot, channelId, capabilityTestChannelName.value, channelType)
    capabilityTestJob.value = snapshotJob
    capabilityTestJobId.value = snapshotJob.jobId
    if (!isCapabilityJobTerminal(snapshotJob)) {
      const refreshedActiveIds = collectActiveJobIds(snapshotJob)
      for (const jId of refreshedActiveIds) {
        startCapabilityPolling(channelType, channelId, jId)
      }
    }
  } catch (error) {
    console.error('Failed to cancel capability test:', error)
  }
}

const handleRetryCapabilityModel = async (protocol: string, model: string) => {
  if (!capabilityTestJob.value) return
  if (!capabilityTestChannelType.value) return
  const job = capabilityTestJob.value
  const protocolTest = job.tests.find(t => t.protocol === protocol)
  if (!protocolTest) return
  if (isBusyCapabilityTest(protocolTest)) return
  const retryJobId = job.protocolJobRefs?.[protocol]?.jobId || job.protocolJobIds?.[protocol]
  if (!retryJobId) {
    // 没有 jobId（虚拟协议未测试过），启动单模型测试
    handleTestCapabilityProtocolWithModels(protocol, [model])
    return
  }
  try {
    const pendingKey = `${protocol}:${model}`
    capabilityRetryPendingUntil.value[pendingKey] = Date.now() + 1000

    capabilityTestJob.value = markCapabilityModelRetrying(capabilityTestJob.value, protocol, model)

    await api.retryCapabilityTestModel(capabilityTestChannelType.value, capabilityTestChannelId.value, retryJobId, protocol, model)
    startCapabilityPolling(capabilityTestChannelType.value, capabilityTestChannelId.value, retryJobId)
  } catch (error) {
    if (error instanceof ApiError && error.status === 404) {
      await handleTestCapabilityProtocolWithModels(protocol, [model])
      return
    }
    console.error('Failed to retry capability test model:', error)
  }
}

// 复制渠道到目标协议 Tab
const handleCopyToTab = async (targetProtocol: string) => {
  const sourceChannel = channelStore.currentChannelsData.channels?.find((ch: Channel) => ch.index === capabilityTestChannelId.value)
  if (!sourceChannel) {
    showToast(t('toast.sourceChannelMissing'), 'error')
    return
  }

  // 构造渠道配置（仅复制核心连接信息）
  const channelConfig: Omit<Channel, 'index' | 'latency' | 'status'> = {
    name: sourceChannel.name,
    serviceType: targetProtocol === 'images' ? 'openai' : sourceChannel.serviceType,
    baseUrl: sourceChannel.baseUrl,
    baseUrls: sourceChannel.baseUrls,
    apiKeys: [...sourceChannel.apiKeys],
    description: sourceChannel.description,
    website: sourceChannel.website,
    proxyUrl: sourceChannel.proxyUrl,
    insecureSkipVerify: sourceChannel.insecureSkipVerify,
    modelMapping: sourceChannel.modelMapping,
    reasoningMapping: sourceChannel.reasoningMapping,
    reasoningParamStyle: sourceChannel.reasoningParamStyle,
    textVerbosity: sourceChannel.textVerbosity,
    fastMode: sourceChannel.fastMode,
    customHeaders: sourceChannel.customHeaders,
    pinned: sourceChannel.pinned,
    priority: sourceChannel.priority,
    lowQuality: sourceChannel.lowQuality,
    injectDummyThoughtSignature: sourceChannel.injectDummyThoughtSignature,
    stripThoughtSignature: sourceChannel.stripThoughtSignature,
    passbackReasoningContent: sourceChannel.passbackReasoningContent,
    supportedModels: sourceChannel.supportedModels,
    normalizeNonstandardChatRoles: sourceChannel.normalizeNonstandardChatRoles,
    rpm: sourceChannel.rpm ?? 10,
  }

  try {
    switch (targetProtocol) {
      case 'messages':
        await api.addChannel(channelConfig)
        break
      case 'chat':
        await api.addChatChannel(channelConfig)
        break
      case 'gemini':
        await api.addGeminiChannel(channelConfig)
        break
      case 'responses':
        await api.addResponsesChannel(channelConfig)
        break
      case 'images':
        await api.addImagesChannel(channelConfig)
        break
      default:
        showToast(t('toast.unsupportedProtocol', { protocol: targetProtocol }), 'error')
        return
    }

    showToast(t('toast.channelCopied', { protocol: targetProtocol }), 'success')
    await refreshChannels()
  } catch (error) {
    showToast(t('toast.copyFailed', { message: error instanceof Error ? error.message : t('system.unknown') }), 'error')
  }
}

const pingAllChannels = async () => {
  try {
    await channelStore.pingAllChannels()
    // 不再使用 Toast，延迟结果直接显示在渠道列表中
  } catch (error) {
    showToast(t('toast.batchLatencyFailed', { message: error instanceof Error ? error.message : t('system.unknown') }), 'error')
  }
}

// Fuzzy 模式管理
const loadFuzzyModeStatus = async () => {
  systemStore.setFuzzyModeLoadError(false)
  try {
    const { fuzzyModeEnabled: enabled } = await api.getFuzzyMode()
    preferencesStore.setFuzzyMode(enabled)
  } catch (e) {
    console.error('Failed to load fuzzy mode status:', e)
    systemStore.setFuzzyModeLoadError(true)
    // 加载失败时不使用默认值，保持 UI 显示未知状态
    showToast(t('toast.loadFuzzyFailed'), 'warning')
  }
}

const toggleFuzzyMode = async () => {
  if (systemStore.fuzzyModeLoadError) {
    showToast(t('toast.fuzzyUnknown'), 'warning')
    return
  }
  systemStore.setFuzzyModeLoading(true)
  try {
    await api.setFuzzyMode(!preferencesStore.fuzzyModeEnabled)
    preferencesStore.toggleFuzzyMode()
    showToast(t('toast.fuzzyToggled', { state: preferencesStore.fuzzyModeEnabled ? t('common.enabled') : t('common.disabled') }), 'success')
  } catch (e) {
    showToast(t('toast.fuzzyToggleFailed', { message: e instanceof Error ? e.message : t('system.unknown') }), 'error')
  } finally {
    systemStore.setFuzzyModeLoading(false)
  }
}

// 移除计费头管理
const loadStripBillingHeaderStatus = async () => {
  systemStore.setStripBillingHeaderLoadError(false)
  try {
    const { stripBillingHeader: enabled } = await api.getStripBillingHeader()
    preferencesStore.setStripBillingHeader(enabled)
  } catch (e) {
    console.error('Failed to load strip billing header status:', e)
    systemStore.setStripBillingHeaderLoadError(true)
    showToast(t('toast.loadBillingFailed'), 'warning')
  }
}

const toggleStripBillingHeader = async () => {
  if (systemStore.stripBillingHeaderLoadError) {
    showToast(t('toast.billingUnknown'), 'warning')
    return
  }
  systemStore.setStripBillingHeaderLoading(true)
  try {
    await api.setStripBillingHeader(!preferencesStore.stripBillingHeader)
    preferencesStore.toggleStripBillingHeader()
    showToast(t('toast.billingToggled', { state: preferencesStore.stripBillingHeader ? t('common.enabled') : t('common.disabled') }), 'success')
  } catch (e) {
    showToast(t('toast.billingToggleFailed', { message: e instanceof Error ? e.message : t('system.unknown') }), 'error')
  } finally {
    systemStore.setStripBillingHeaderLoading(false)
  }
}

// 主题管理
const toggleDarkMode = () => {
  const newMode = preferencesStore.darkModePreference === 'dark' ? 'light' : 'dark'
  setDarkMode(newMode)
}

const setDarkMode = (themeName: 'light' | 'dark' | 'auto') => {
  preferencesStore.setDarkMode(themeName)
  const apply = (isDark: boolean) => {
    // 使用 Vuetify 3.9+ 推荐的 theme.change() API
    theme.change(isDark ? 'dark' : 'light')
  }

  if (themeName === 'auto') {
    const prefersDark = window.matchMedia('(prefers-color-scheme: dark)').matches
    apply(prefersDark)
  } else {
    apply(themeName === 'dark')
  }
  // PreferencesStore 已通过 pinia-plugin-persistedstate 自动持久化，无需手动写入 localStorage
}

// 认证状态管理（使用 AuthStore）
const isAuthenticated = computed(() => authStore.isAuthenticated)
// 认证相关状态已迁移到 AuthStore

// 认证尝试限制
const MAX_AUTH_ATTEMPTS = 5

// 控制认证对话框显示
const showAuthDialog = computed({
  get: () => {
    // 只有在初始化完成后，且未认证，且不在自动认证中时，才显示对话框
    return authStore.isInitialized && !isAuthenticated.value && !authStore.isAutoAuthenticating
  },
  set: () => {} // 防止外部修改，认证状态只能通过内部逻辑控制
})

// 自动验证保存的密钥
const autoAuthenticate = async () => {
  // 检查 AuthStore 中是否有保存的密钥
  if (!authStore.apiKey) {
    // 没有保存的密钥，显示登录对话框
    authStore.setAuthError(t('toast.enterAccessKeyContinue'))
    authStore.setAutoAuthenticating(false)
    authStore.setInitialized(true)
    return false
  }

  // 有保存的密钥，尝试自动认证
  try {
    // 尝试调用API验证密钥是否有效
    await api.getChannels()

    // 密钥有效，认证成功
    authStore.setAuthError('')
    return true
  } catch (error) {
    // 仅在明确 401 时视为密钥无效；其他错误（网络/5xx）不应清除密钥
    if (error instanceof ApiError && error.status === 401) {
      console.warn('自动认证失败: 认证失败(401)')
      authStore.clearAuth()
      authStore.setAuthError(t('toast.savedKeyInvalid'))
      return false
    }

    console.warn('自动认证暂时失败:', error)
    showToast(t('toast.cannotVerifyAccessKey', { message: error instanceof Error ? error.message : t('system.unknown') }), 'warning')
    // 非 401：保留密钥，继续尝试连接后端（后续刷新会更新系统状态）
    return true
  } finally {
    authStore.setAutoAuthenticating(false)
    authStore.setInitialized(true)
  }
}

// 手动设置密钥（用于重新认证）
const setAuthKey = (key: string) => {
  authStore.setApiKey(key)
  authStore.setAuthError('')
}

// 处理认证提交
const handleAuthSubmit = async () => {
  if (!authStore.authKeyInput.trim()) {
    authStore.setAuthError(t('toast.enterAccessKey'))
    return
  }

  // 检查是否被锁定
  if (authStore.isAuthLocked) {
    const remainingSeconds = Math.ceil((authStore.authLockoutTime! - Date.now()) / 1000)
    authStore.setAuthError(t('toast.tooManyAttemptsSeconds', { seconds: remainingSeconds }))
    return
  }

  authStore.setAuthLoading(true)
  authStore.setAuthError('')

  try {
    // 设置密钥
    setAuthKey(authStore.authKeyInput.trim())

    // 测试API调用以验证密钥
    await api.getChannels()

    // 认证成功，重置计数器
    authStore.resetAuthAttempts()
    authStore.setAuthLockout(null)

    // 如果成功，加载数据
    await refreshChannels()
    // 手动登录成功后同步系统状态，避免状态卡停留在 Connecting
    systemStore.setSystemStatus(channelStore.lastRefreshSuccess ? 'running' : 'error')

    authStore.setAuthKeyInput('')

    // 记录认证成功(前端日志)
    if (import.meta.env.DEV) {
      console.info('✅ 认证成功 - 时间:', new Date().toISOString())
    }
  } catch (error) {
    // 仅在明确 401 时计入认证失败；网络/5xx 不计入失败次数，也不清除已保存密钥
    if (error instanceof ApiError && error.status === 401) {
      authStore.incrementAuthAttempts()

      // 记录认证失败(前端日志)
      console.warn('🔒 认证失败 - 尝试次数:', authStore.authAttempts, '时间:', new Date().toISOString())

      // 如果尝试次数过多，锁定5分钟
      if (authStore.authAttempts >= MAX_AUTH_ATTEMPTS) {
        authStore.setAuthLockout(new Date(Date.now() + 5 * 60 * 1000))
        authStore.setAuthError(t('toast.tooManyAttempts'))
      } else {
        authStore.setAuthError(t('toast.accessKeyInvalidRemaining', { remaining: MAX_AUTH_ATTEMPTS - authStore.authAttempts }))
      }

      authStore.clearAuth()
      return
    }

    showToast(t('toast.cannotVerifyAccessKey', { message: error instanceof Error ? error.message : t('system.unknown') }), 'error')
  } finally {
    authStore.setAuthLoading(false)
  }
}

// 处理注销
const handleLogout = () => {
  authStore.clearAuth()
  channelStore.clearChannels()
  authStore.setAuthError(t('toast.enterAccessKeyContinue'))
  showToast(t('toast.loggedOut'), 'info')
}

// 处理认证失败
const handleAuthError = (error: unknown) => {
  if (error instanceof ApiError && error.status === 401) {
    authStore.setAuthError(t('toast.authInvalid'))
  } else {
    showToast(t('toast.operationFailed', { message: error instanceof Error ? error.message : t('system.unknown') }), 'error')
  }
}

// 版本检查
const checkVersion = async () => {
  if (systemStore.isCheckingVersion) return

  systemStore.setCheckingVersion(true)
  try {
    // 先获取当前版本
    const health = await fetchHealth()
    const currentVersion = health.version?.version || ''

    if (currentVersion) {
      versionService.setCurrentVersion(currentVersion)
      systemStore.setCurrentVersion(currentVersion)

      // 通过后端 API 检查最新版本
      const result = await versionService.checkViaBackend()
      systemStore.setVersionInfo(result)
    } else {
      systemStore.setVersionInfo({
        ...systemStore.versionInfo,
        status: 'error',
      })
    }
  } catch (error) {
    console.warn('Version check failed:', error)
    systemStore.setVersionInfo({
      ...systemStore.versionInfo,
      status: 'error',
    })
  } finally {
    systemStore.setCheckingVersion(false)
  }
}

// 版本点击处理

// 更新对话框相关状态
const showUpdateDialog = ref(false)
const updateDialogInfo = ref<VersionInfo>(systemStore.versionInfo)

const onUpdateSuccess = () => {
  showUpdateDialog.value = false
}

const handleVersionClick = () => {
  if (systemStore.versionInfo.status === 'update-available') {
    updateDialogInfo.value = { ...systemStore.versionInfo }
    showUpdateDialog.value = true
  } else if (systemStore.versionInfo.status === 'latest' && systemStore.versionInfo.releaseUrl) {
    window.open(systemStore.versionInfo.releaseUrl, '_blank', 'noopener,noreferrer')
  }
}

// 监听系统主题变化（setup 阶段注册，onUnmounted 清理，避免泄漏）
// 守卫非浏览器环境（SSR / vitest 非 jsdom）：避免 ReferenceError: window is not defined
const mediaQuery = typeof window !== 'undefined' && typeof window.matchMedia === 'function'
  ? window.matchMedia('(prefers-color-scheme: dark)')
  : null
const handlePref = () => {
  if (preferencesStore.darkModePreference === 'auto') setDarkMode('auto')
}
mediaQuery?.addEventListener('change', handlePref)

// 初始化
onMounted(async () => {
  // 初始化复古像素主题
  document.documentElement.dataset.theme = 'retro'
  initTheme()

  // 加载保存的暗色模式偏好（从 PreferencesStore 读取，已自动从 localStorage 恢复）
  setDarkMode(preferencesStore.darkModePreference)

  // 版本检查（独立于认证，静默执行）
  checkVersion()

  // 检查 AuthStore 中是否有保存的密钥
  if (authStore.apiKey) {
    // 有保存的密钥，开始自动认证
    authStore.setAutoAuthenticating(true)
    authStore.setInitialized(false)
  } else {
    // 没有保存的密钥，直接显示登录对话框
    authStore.setAutoAuthenticating(false)
    authStore.setInitialized(true)
  }

  // 尝试自动认证
  const authenticated = await autoAuthenticate()

  if (authenticated) {
    // 加载渠道数据
    await refreshChannels()
    // 加载 Fuzzy 模式状态
    await loadFuzzyModeStatus()
    // 加载移除计费头状态
    await loadStripBillingHeaderStatus()
    // 启动自动刷新
    startAutoRefresh()
    // 初始化完成后根据最新刷新结果设置系统状态
    systemStore.setSystemStatus(channelStore.lastRefreshSuccess ? 'running' : 'error')
  }
})

// 启动自动刷新定时器
const startAutoRefresh = () => {
  channelStore.startAutoRefresh()
}

// 停止自动刷新定时器
const stopAutoRefresh = () => {
  channelStore.stopAutoRefresh()
}

// 监听 Tab 切换，刷新对应数据
watch(() => channelStore.activeTab, async () => {
  if (isAuthenticated.value) {
    try {
      await channelStore.refreshChannels()
    } catch (error) {
      console.error('切换 Tab 刷新失败:', error)
    }
  }
})

// 监听认证状态变化
watch(isAuthenticated, newValue => {
  if (newValue) {
    startAutoRefresh()
  } else {
    stopAutoRefresh()
  }
})

// 监听自动刷新状态，更新 systemStatus
watch(() => channelStore.lastRefreshSuccess, (success) => {
  if (isAuthenticated.value) {
    systemStore.setSystemStatus(success ? 'running' : 'error')
  }
})

// 在组件卸载时清除定时器和事件监听器
onUnmounted(() => {
  channelStore.stopAutoRefresh()
  stopAllCapabilityPolling()
  mediaQuery?.removeEventListener('change', handlePref)
})
</script>

<style scoped>
/* =====================================================
   🎮 复古像素 (Retro Pixel) 主题样式系统
   Neo-Brutalism: 直角、粗黑边框、硬阴影、等宽字体
   ===================================================== */

/* ----- 应用栏 - 复古像素风格 ----- */
.app-header {
  background: rgb(var(--v-theme-surface)) !important;
  border-bottom: 2px solid rgb(var(--v-theme-on-surface));
  transition: none;
  padding: 0 16px !important;
}

.v-theme--dark .app-header {
  background: rgb(var(--v-theme-surface)) !important;
  border-bottom: 2px solid rgba(255, 255, 255, 0.8);
}

/* 修复 Header 布局 */
.app-header :deep(.v-toolbar__prepend) {
  margin-inline-end: 4px !important;
}

.app-header .v-toolbar-title {
  overflow: hidden !important;
  min-width: 0 !important;
  flex: 1 !important;
}

.app-header :deep(.v-toolbar__content) {
  overflow: visible !important;
}

.app-header :deep(.v-toolbar__content > .v-toolbar-title) {
  min-width: 0 !important;
  margin-inline-start: 0 !important;
  margin-inline-end: auto !important;
}

.app-header :deep(.v-toolbar-title__placeholder) {
  width: 100%;
  overflow: hidden;
  white-space: nowrap;
  text-overflow: ellipsis;
}

.app-logo {
  width: 42px;
  height: 42px;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgb(var(--v-theme-primary));
  border: 2px solid rgb(var(--v-theme-on-surface));
  box-shadow: 3px 3px 0 0 rgb(var(--v-theme-on-surface));
  margin-right: 8px;
}

.v-theme--dark .app-logo {
  border-color: rgba(255, 255, 255, 0.8);
  box-shadow: 3px 3px 0 0 rgba(255, 255, 255, 0.8);
}

/* 自定义标题容器 */
.header-title {
  display: flex;
  align-items: center;
  flex-shrink: 0;
}

.api-type-text {
  cursor: pointer;
  opacity: 0.5;
  transition: all 0.1s ease;
  padding: 4px 8px;
  position: relative;
  text-decoration: none;
  color: inherit;
}

a.api-type-text {
  display: inline-block;
}

.api-type-text:not(.separator):hover {
  opacity: 0.8;
  background: rgba(var(--v-theme-primary), 0.15);
}

.api-type-text.active {
  opacity: 1;
  font-weight: 700;
  color: rgb(var(--v-theme-primary));
  background: rgba(var(--v-theme-primary), 0.1);
  border: 1px solid rgb(var(--v-theme-on-surface));
}

.v-theme--dark .api-type-text.active {
  border-color: rgba(255, 255, 255, 0.6);
}

.separator {
  opacity: 0.25;
  margin: 0 2px;
  cursor: default;
  padding: 0;
}

.brand-text {
  margin-left: 10px;
  color: rgb(var(--v-theme-primary));
  font-weight: 700;
}

.header-btn {
  border: 2px solid rgb(var(--v-theme-on-surface)) !important;
  box-shadow: 2px 2px 0 0 rgb(var(--v-theme-on-surface)) !important;
  margin-left: 4px;
  transition: all 0.1s ease !important;
}

.language-switch-btn {
  border-radius: 999px !important;
}

.language-switch-label {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  min-width: 2ch;
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0;
  line-height: 1;
}

.v-theme--dark .header-btn {
  border-color: rgba(255, 255, 255, 0.6) !important;
  box-shadow: 2px 2px 0 0 rgba(255, 255, 255, 0.6) !important;
}

.header-btn:hover {
  background: rgba(var(--v-theme-primary), 0.1);
  transform: translate(-1px, -1px);
  box-shadow: 3px 3px 0 0 rgb(var(--v-theme-on-surface)) !important;
}

.header-btn:active {
  transform: translate(2px, 2px) !important;
  box-shadow: none !important;
}

/* ----- 版本信息徽章 ----- */
.version-badge {
  display: flex;
  align-items: center;
  padding: 4px 10px;
  margin-right: 8px;
  font-family: 'JetBrains Mono', 'Fira Code', monospace;
  font-size: 12px;
  border: 2px solid rgb(var(--v-theme-on-surface));
  background: rgb(var(--v-theme-surface));
  transition: all 0.15s ease;
}

.version-badge.version-clickable {
  cursor: pointer;
}

.version-badge.version-clickable:hover {
  transform: translateY(-1px);
  box-shadow: 3px 3px 0 0 rgb(var(--v-theme-on-surface));
}

.version-badge.version-checking {
  opacity: 0.7;
}

.version-badge.version-latest {
  border-color: rgb(var(--v-theme-success));
}

.version-badge.version-update {
  border-color: rgb(var(--v-theme-warning));
  background: rgba(var(--v-theme-warning), 0.1);
}

.version-text {
  color: rgb(var(--v-theme-on-surface));
}

.version-arrow {
  color: rgb(var(--v-theme-warning));
  font-weight: bold;
}

.version-latest-text {
  color: rgb(var(--v-theme-warning));
  font-weight: bold;
}

.v-theme--dark .version-badge {
  border-color: rgba(255, 255, 255, 0.6);
}

.v-theme--dark .version-badge.version-latest {
  border-color: rgb(var(--v-theme-success));
}

.v-theme--dark .version-badge.version-update {
  border-color: rgb(var(--v-theme-warning));
}

/* ----- 统计卡片 - 复古像素风格 ----- */
.stat-cards-row {
  margin-top: -8px;
}

.stat-card {
  position: relative;
  display: flex;
  align-items: center;
  gap: 16px;
  padding: 20px;
  margin: 2px;
  background: rgb(var(--v-theme-surface));
  border: 2px solid rgb(var(--v-theme-on-surface));
  box-shadow: 6px 6px 0 0 rgb(var(--v-theme-on-surface));
  transition: all 0.1s ease;
  overflow: hidden;
  min-height: 100px;
}
.stat-card:hover {
  transform: translate(-2px, -2px);
  box-shadow: 8px 8px 0 0 rgb(var(--v-theme-on-surface));
  border: 2px solid rgb(var(--v-theme-on-surface));
}

.stat-card:active {
  transform: translate(2px, 2px);
  box-shadow: 2px 2px 0 0 rgb(var(--v-theme-on-surface));
}

.v-theme--dark .stat-card {
  background: rgb(var(--v-theme-surface));
  border-color: rgba(255, 255, 255, 0.8);
  box-shadow: 6px 6px 0 0 rgba(255, 255, 255, 0.8);
}
.v-theme--dark .stat-card:hover {
  box-shadow: 8px 8px 0 0 rgba(255, 255, 255, 0.8);
  border-color: rgba(255, 255, 255, 0.8);
}

.v-theme--dark .stat-card:active {
  box-shadow: 2px 2px 0 0 rgba(255, 255, 255, 0.8);
}

.stat-card-icon {
  width: 56px;
  height: 56px;
  display: flex;
  align-items: center;
  justify-content: center;
  flex-shrink: 0;
  border: 2px solid rgb(var(--v-theme-on-surface));
  background: rgba(var(--v-theme-primary), 0.15);
  transition: transform 0.1s ease;
}

.v-theme--dark .stat-card-icon {
  border-color: rgba(255, 255, 255, 0.6);
}

.stat-card:hover .stat-card-icon {
  transform: scale(1.05);
}

.stat-card-content {
  flex: 1;
  min-width: 0;
}

.stat-card-value {
  font-size: 1.75rem;
  font-weight: 700;
  line-height: 1.2;
  letter-spacing: 0;
}

.stat-card-total {
  font-size: 1rem;
  font-weight: 500;
  opacity: 0.6;
}

.stat-card-label {
  font-size: 0.875rem;
  font-weight: 600;
  margin-top: 4px;
  line-height: 1.4;
  opacity: 0.92;
  text-transform: uppercase;
  letter-spacing: 0;
}

.stat-card-desc {
  font-size: 0.8125rem;
  opacity: 0.72;
  margin-top: 4px;
  line-height: 1.5;
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
}

/* 隐藏光晕效果 */
.stat-card-glow {
  display: none;
}

/* 统计卡片颜色变体 */
.stat-card-info .stat-card-icon {
  background: #3b82f6;
  color: white;
}
.stat-card-info .stat-card-value {
  color: #3b82f6;
}
.v-theme--dark .stat-card-info .stat-card-value {
  color: #60a5fa;
}

.stat-card-success .stat-card-icon {
  background: #10b981;
  color: white;
}
.stat-card-success .stat-card-value {
  color: #10b981;
}
.v-theme--dark .stat-card-success .stat-card-value {
  color: #34d399;
}

.stat-card-primary .stat-card-icon {
  background: #6366f1;
  color: white;
}
.stat-card-primary .stat-card-value {
  color: #6366f1;
}
.v-theme--dark .stat-card-primary .stat-card-value {
  color: #818cf8;
}

.stat-card-emerald .stat-card-icon {
  background: #059669;
  color: white;
}
.stat-card-emerald .stat-card-value {
  color: #059669;
}
.v-theme--dark .stat-card-emerald .stat-card-value {
  color: #34d399;
}

.stat-card-error .stat-card-icon {
  background: #dc2626;
  color: white;
}
.stat-card-error .stat-card-value {
  color: #dc2626;
}
.v-theme--dark .stat-card-error .stat-card-value {
  color: #f87171;
}

/* =========================================
   复古像素主题 - 全局样式覆盖
   ========================================= */

/* 全局背景 */
.v-application {
  background-color: #fffbeb !important;
  font-family: 'Courier New', Consolas, monospace !important;
}

.v-theme--dark .v-application,
.v-theme--dark.v-application {
  background-color: rgb(var(--v-theme-background)) !important;
}

.v-main {
  background-color: #fffbeb !important;
}

.v-theme--dark .v-main {
  background-color: rgb(var(--v-theme-background)) !important;
}

/* 统计卡片图标配色 */
.stat-card-icon .v-icon {
  color: white !important;
}

.stat-card-emerald .stat-card-icon .v-icon {
  color: white !important;
}

/* 主按钮 - 复古像素风格 */
.action-btn-primary {
  background: rgb(var(--v-theme-primary)) !important;
  border: 2px solid rgb(var(--v-theme-on-surface)) !important;
  box-shadow: 4px 4px 0 0 rgb(var(--v-theme-on-surface)) !important;
  color: white !important;
}

.action-btn-primary:hover {
  transform: translate(-1px, -1px);
  box-shadow: 5px 5px 0 0 rgb(var(--v-theme-on-surface)) !important;
}

.action-btn-primary:active {
  transform: translate(2px, 2px) !important;
  box-shadow: none !important;
}

.v-theme--dark .action-btn-primary {
  border-color: rgba(129, 140, 248, 0.5) !important;
  box-shadow: 4px 4px 0 0 rgba(129, 140, 248, 0.25) !important;
}
.v-theme--dark .action-btn-primary:hover {
  background: rgb(129, 140, 248) !important;
  border-color: rgba(129, 140, 248, 0.7) !important;
  box-shadow: 5px 5px 0 0 rgba(129, 140, 248, 0.3) !important;
}

/* 渠道编排容器 */
.channel-orchestration {
  background: transparent !important;
  box-shadow: none !important;
  border: none !important;
}

/* 渠道列表卡片样式 */
.channel-list .channel-row {
  background: rgb(var(--v-theme-surface)) !important;
  margin-bottom: 0;
  padding: 14px 12px 14px 28px !important;
  border: 2px solid rgb(var(--v-theme-on-surface)) !important;
  box-shadow: 4px 4px 0 0 rgb(var(--v-theme-on-surface)) !important;
  min-height: 48px !important;
  position: relative;
}

.v-theme--dark .channel-list .channel-row {
  border-color: rgba(255, 255, 255, 0.7) !important;
  box-shadow: 4px 4px 0 0 rgba(255, 255, 255, 0.7) !important;
}

.channel-list .channel-row:active {
  transform: translate(2px, 2px);
  box-shadow: none !important;
  transition: transform 0.1s;
}

/* 序号角标 */
.channel-row .priority-number {
  position: absolute !important;
  top: -1px !important;
  left: -1px !important;
  background: rgb(var(--v-theme-surface)) !important;
  color: rgb(var(--v-theme-on-surface)) !important;
  font-size: 11px !important;
  font-weight: 700 !important;
  padding: 3px 8px !important;
  border: 1px solid rgb(var(--v-theme-on-surface)) !important;
  border-top: none !important;
  border-left: none !important;
  width: auto !important;
  height: auto !important;
  margin: 0 !important;
  box-shadow: none !important;
  text-transform: uppercase;
}

.v-theme--dark .channel-row .priority-number {
  border-color: rgba(255, 255, 255, 0.5) !important;
}

/* 拖拽手柄 */
.drag-handle {
  opacity: 0.3;
  padding: 8px;
  margin-left: -8px;
}

/* 渠道名称 */
.channel-name {
  font-size: 14px !important;
  font-weight: 700 !important;
  color: rgb(var(--v-theme-on-surface));
}

.channel-name .text-caption.text-medium-emphasis {
  background: rgb(var(--v-theme-surface-variant));
  padding: 2px 6px;
  font-size: 11px !important;
  font-weight: 600;
  color: rgb(var(--v-theme-on-surface)) !important;
  border: 1px solid rgb(var(--v-theme-on-surface));
  text-transform: uppercase;
}

.v-theme--dark .channel-name .text-caption.text-medium-emphasis {
  border-color: rgba(255, 255, 255, 0.5);
}

/* 隐藏描述文字 */
.channel-name .text-disabled {
  display: none !important;
}

/* 隐藏指标和密钥数 */
.channel-metrics,
.channel-keys {
  display: none !important;
}

/* --- 备用资源池 --- */
.inactive-pool {
  background: rgb(var(--v-theme-surface)) !important;
  border: 2px dashed rgb(var(--v-theme-on-surface)) !important;
  padding: 8px !important;
  margin-top: 12px;
}

.v-theme--dark .inactive-pool {
  border-color: rgba(255, 255, 255, 0.5) !important;
}

.inactive-channel-row {
  background: rgb(var(--v-theme-surface)) !important;
  margin: 6px !important;
  padding: 12px !important;
  border: 2px solid rgb(var(--v-theme-on-surface)) !important;
  box-shadow: 3px 3px 0 0 rgb(var(--v-theme-on-surface)) !important;
}

.v-theme--dark .inactive-channel-row {
  border-color: rgba(255, 255, 255, 0.6) !important;
  box-shadow: 3px 3px 0 0 rgba(255, 255, 255, 0.6) !important;
}

.inactive-channel-row .channel-info-main {
  color: rgb(var(--v-theme-on-surface)) !important;
  font-weight: 600;
}

/* ----- 操作按钮区域 ----- */
.action-bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 16px 20px;
  background: rgb(var(--v-theme-surface));
  border: 2px solid rgb(var(--v-theme-on-surface));
  box-shadow: 6px 6px 0 0 rgb(var(--v-theme-on-surface));
}

.v-theme--dark .action-bar {
  background: rgb(var(--v-theme-surface));
  border-color: rgba(255, 255, 255, 0.8);
  box-shadow: 6px 6px 0 0 rgba(255, 255, 255, 0.8);
}

.action-bar-left {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 12px;
}

.action-bar-right {
  display: flex;
  align-items: center;
  gap: 12px;
}

.action-btn {
  font-weight: 600;
  letter-spacing: 0;
  text-transform: uppercase;
  transition: all 0.1s ease;
  border: 2px solid rgb(var(--v-theme-on-surface)) !important;
  box-shadow: 4px 4px 0 0 rgb(var(--v-theme-on-surface)) !important;
}

.v-theme--dark .action-btn {
  border-color: rgba(226, 232, 240, 0.2) !important;
  box-shadow: 4px 4px 0 0 rgba(0, 0, 0, 0.35) !important;
}
.v-theme--dark .action-btn:hover {
  border-color: rgba(226, 232, 240, 0.35) !important;
  box-shadow: 5px 5px 0 0 rgba(0, 0, 0, 0.4) !important;
  background: rgba(226, 232, 240, 0.06) !important;
}

.action-btn:hover {
  transform: translate(-1px, -1px);
  box-shadow: 5px 5px 0 0 rgb(var(--v-theme-on-surface)) !important;
}

.action-btn:active {
  transform: translate(2px, 2px) !important;
  box-shadow: none !important;
}

.load-balance-btn {
  text-transform: uppercase;
}

.load-balance-menu {
  min-width: 300px;
  padding: 8px;
  border: 2px solid rgb(var(--v-theme-on-surface)) !important;
  box-shadow: 4px 4px 0 0 rgb(var(--v-theme-on-surface)) !important;
}

.v-theme--dark .load-balance-menu {
  border-color: rgba(226, 232, 240, 0.2) !important;
  box-shadow: 4px 4px 0 0 rgba(0, 0, 0, 0.35) !important;
}

.load-balance-menu .v-list-item {
  margin-bottom: 4px;
  padding: 12px 16px;
}

.load-balance-menu .v-list-item:last-child {
  margin-bottom: 0;
}

/* =========================================
   手机端专属样式 (≤600px)
   ========================================= */
@media (max-width: 600px) {
  /* --- 主容器内边距缩小 --- */
  .v-main .v-container {
    padding-left: 8px !important;
    padding-right: 8px !important;
  }

  /* --- 顶部导航栏 --- */
  .app-header {
    padding: 0 12px !important;
    background: rgb(var(--v-theme-surface)) !important;
    border-bottom: 2px solid rgb(var(--v-theme-on-surface)) !important;
    box-shadow: none !important;
  }

  .v-theme--dark .app-header {
    border-bottom-color: rgba(255, 255, 255, 0.7) !important;
  }

  .app-logo {
    width: 32px;
    height: 32px;
    margin-right: 8px;
    box-shadow: 2px 2px 0 0 rgb(var(--v-theme-on-surface));
  }

  .v-theme--dark .app-logo {
    box-shadow: 2px 2px 0 0 rgba(255, 255, 255, 0.7);
  }

  .mobile-tab-selector {
    color: rgb(var(--v-theme-primary)) !important;
    letter-spacing: 0;
    text-transform: none;
    padding: 0 4px !important;
    min-width: auto !important;
  }

  /* --- 统计卡片优化 --- */
  .stat-card {
    padding: 14px 12px;
    gap: 10px;
    min-height: auto;
    background: rgb(var(--v-theme-surface)) !important;
    box-shadow: 4px 4px 0 0 rgb(var(--v-theme-on-surface)) !important;
    border: 2px solid rgb(var(--v-theme-on-surface)) !important;
  }

  .v-theme--dark .stat-card {
    box-shadow: 4px 4px 0 0 rgba(255, 255, 255, 0.7) !important;
    border-color: rgba(255, 255, 255, 0.7) !important;
  }

  .stat-card-icon {
    width: 36px;
    height: 36px;
  }

  .stat-card-icon .v-icon {
    font-size: 18px !important;
  }

  .stat-card-value {
    font-size: 1.35rem;
    font-weight: 800 !important;
    line-height: 1.2;
    color: rgb(var(--v-theme-on-surface));
    letter-spacing: 0;
  }

  .stat-card-label {
    font-size: 0.7rem;
    color: rgba(var(--v-theme-on-surface), 0.6);
    font-weight: 500;
    text-transform: uppercase;
  }

  .stat-card-desc {
    display: none;
  }

  .stat-cards-row {
    margin-bottom: 12px !important;
    margin-left: -4px !important;
    margin-right: -4px !important;
  }

  .stat-cards-row .v-col {
    padding: 4px !important;
  }

  /* --- 操作按钮区域 --- */
  .action-bar {
    flex-direction: column;
    gap: 10px;
    padding: 12px !important;
    box-shadow: 4px 4px 0 0 rgb(var(--v-theme-on-surface)) !important;
  }

  .v-theme--dark .action-bar {
    box-shadow: 4px 4px 0 0 rgba(255, 255, 255, 0.7) !important;
  }

  .action-bar-left {
    width: 100%;
    display: grid;
    grid-template-columns: 1fr 1fr;
    gap: 8px;
  }

  .action-bar-left .action-btn {
    width: 100%;
    justify-content: center;
  }

  /* 刷新按钮独占一行 */
  .action-bar-left .action-btn:nth-child(3) {
    grid-column: 1 / -1;
  }

  .action-bar-right {
    width: 100%;
    display: grid;
    grid-template-columns: auto 1fr;
    gap: 8px;
  }

  .action-bar-right .action-btn {
    min-width: 0;
    flex-shrink: 1;
  }

  .action-bar-right .load-balance-btn {
    width: 100%;
    justify-content: center;
    min-width: 0;
    overflow: hidden;
  }

  .action-bar-right .load-balance-btn :deep(.v-btn__content) {
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  /* --- 渠道编排容器 --- */
  .channel-orchestration .v-card-title {
    display: none !important;
  }

  .channel-orchestration > .v-divider {
    display: none !important;
  }

  /* 隐藏"故障转移序列"标题区域 */
  .channel-orchestration .px-4.pt-3.pb-2 > .d-flex.mb-2 {
    display: none !important;
  }

  /* --- 渠道列表卡片化 --- */
  .channel-list .channel-row:active {
    transform: translate(2px, 2px);
    box-shadow: none !important;
    transition: transform 0.1s;
  }

  /* --- 通用优化 --- */
  .v-chip {
    font-weight: 600;
    border: 1px solid rgb(var(--v-theme-on-surface));
    text-transform: uppercase;
  }

  .v-theme--dark .v-chip {
    border-color: rgba(255, 255, 255, 0.5);
  }

  /* 隐藏分割线 */
  .channel-orchestration .v-divider {
    display: none !important;
  }
}

/* 心跳动画 - 简化为简单闪烁 */
.pulse-animation {
  animation: pixel-blink 1s step-end infinite;
}

@keyframes pixel-blink {
  0%,
  100% {
    opacity: 1;
  }
  50% {
    opacity: 0.7;
  }
}

/* ----- 响应式调整 ----- */
@media (min-width: 768px) {
  .app-header {
    padding: 0 24px !important;
  }
}

@media (min-width: 1024px) {
  .app-header {
    padding: 0 32px !important;
  }
}

/* ----- 渠道列表动画 ----- */
.d-contents {
  display: contents;
}

.channel-col {
  transition: all 0.2s ease;
  max-width: 640px;
}

.channel-list-enter-active,
.channel-list-leave-active {
  transition: all 0.2s ease;
}

.channel-list-enter-from {
  opacity: 0;
  transform: translateY(10px);
}

.channel-list-leave-to {
  opacity: 0;
  transform: translateY(-10px);
}

.channel-list-move {
  transition: transform 0.2s ease;
}

/* ----- 全局统计面板样式 ----- */

/* 方案 B: 顶部可折叠卡片 */
.global-stats-panel {
  background: rgb(var(--v-theme-surface)) !important;
  border: 2px solid rgb(var(--v-theme-on-surface)) !important;
  box-shadow: 4px 4px 0 0 rgb(var(--v-theme-on-surface)) !important;
}

.v-theme--dark .global-stats-panel {
  border-color: rgba(255, 255, 255, 0.7) !important;
  box-shadow: 4px 4px 0 0 rgba(255, 255, 255, 0.7) !important;
}

.global-stats-header {
  transition: background 0.15s ease;
}

.global-stats-header:hover {
  background: rgba(var(--v-theme-primary), 0.05);
}
</style>

<!-- 全局样式 - 复古像素主题 -->
<style>
/* 复古像素主题 - 全局样式 */
.v-application {
  font-family: 'Courier New', Consolas, 'Liberation Mono', monospace !important;
}

.text-body-1,
.text-body-2 {
  line-height: 1.6 !important;
}

.text-caption {
  font-size: 0.8125rem !important;
  line-height: 1.5 !important;
}

/* 所有按钮复古像素风格 */
.v-btn:not(.v-btn--icon) {
  border-radius: 0 !important;
  text-transform: uppercase !important;
  font-weight: 500 !important;
  letter-spacing: 0 !important;
}

/* 所有卡片复古像素风格 */
.v-card {
  border-radius: 0 !important;
}

/* 所有 Chip 复古像素风格 */
.v-chip {
  border-radius: 0 !important;
  font-weight: 600;
  text-transform: uppercase;
}

/* 输入框复古像素风格 */
.v-text-field .v-field {
  border-radius: 0 !important;
}

/* 对话框复古像素风格 */
.v-dialog .v-card {
  border: 2px solid currentColor !important;
  box-shadow: 6px 6px 0 0 currentColor !important;
}

/* 菜单复古像素风格 */
.v-menu > .v-overlay__content > .v-list {
  border-radius: 0 !important;
  border: 2px solid rgb(var(--v-theme-on-surface)) !important;
  box-shadow: 4px 4px 0 0 rgb(var(--v-theme-on-surface)) !important;
}

.v-theme--dark .v-menu > .v-overlay__content > .v-list {
  border-color: rgba(255, 255, 255, 0.7) !important;
  box-shadow: 4px 4px 0 0 rgba(255, 255, 255, 0.7) !important;
}

/* Snackbar 复古像素风格 */
.v-snackbar__wrapper {
  border-radius: 0 !important;
  border: 2px solid currentColor !important;
  box-shadow: 4px 4px 0 0 currentColor !important;
}

/* 状态徽章复古像素风格 */
.status-badge .badge-content {
  border-radius: 0 !important;
  border: 1px solid rgb(var(--v-theme-on-surface));
}

.v-theme--dark .status-badge .badge-content {
  border-color: rgba(255, 255, 255, 0.6);
}
</style>
