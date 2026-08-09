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
              <span class="shortcut-hint ml-2 text-xs opacity-50">Enter</span>
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
        <div class="app-logo d-flex align-center justify-center pa-0 overflow-hidden">
          <!-- 显著放大 Logo 尺寸（手机端 32px，电脑端 44px）让流转动画和发光更清晰夺目 -->
          <Logo :size="$vuetify.display.mobile ? 32 : 44" />
        </div>
      </template>

      <!-- 自定义标题容器 - 替代 v-app-bar-title -->
      <div class="header-title">
        <!-- 移动/平板端：下拉菜单（宽度小于 1000px 时进行折叠，防止菜单过窄挤压） -->
        <v-menu v-if="$vuetify.display.width < 1000">
          <template #activator="{ props: menuProps }">
            <v-btn
              v-bind="menuProps"
              variant="text"
              class="mobile-tab-selector text-body-2 font-weight-bold"
              append-icon="mdi-chevron-down"
            >
              {{ translatedApiTabOptions.find(tab => tab.route === route.path)?.label || translatedApiTabOptions.find(tab => tab.value === channelStore.activeTab)?.label }}
            </v-btn>
          </template>
          <v-list density="compact" nav>
            <v-list-item
              v-for="tab in translatedApiTabOptions"
              :key="tab.value"
              :active="route.path === tab.route"
              :to="tab.route"
            >
              <v-list-item-title>{{ tab.label }}</v-list-item-title>
            </v-list-item>
          </v-list>
        </v-menu>

        <!-- 桌面端：平铺链接 -->
        <div v-else class="text-h6 font-weight-bold d-flex align-center">
          <router-link to="/channels/messages" class="api-type-text" :class="{ active: route.path === '/channels/messages' }">
            {{ t('app.tabs.channels') }}
          </router-link>
          <span class="api-type-text separator">/</span>
          <router-link to="/channels/images" class="api-type-text" :class="{ active: route.path === '/channels/images' }">
            {{ t('app.tabs.images') }}
          </router-link>
          <span class="api-type-text separator">/</span>
          <router-link to="/channels/vectors" class="api-type-text" :class="{ active: route.path === '/channels/vectors' }">
            {{ t('app.tabs.vectors') }}
          </router-link>
          <span class="api-type-text separator">/</span>
          <router-link to="/conversations" class="api-type-text" :class="{ active: route.path === '/conversations' }">
            {{ t('app.tabs.conversations') }}
          </router-link>
          <span class="api-type-text separator">/</span>
          <router-link to="/health" class="api-type-text" :class="{ active: route.path === '/health' }">
            {{ t('app.tabs.healthCenter') }}
          </router-link>
          <span class="api-type-text separator">/</span>
          <router-link to="/subscriptions" class="api-type-text" :class="{ active: route.path === '/subscriptions' }">
            {{ t('app.tabs.subscriptions') }}
          </router-link>
          <span class="api-type-text separator">/</span>
          <router-link to="/cockpit" class="api-type-text" :class="{ active: route.path === '/cockpit' }">
            {{ t('app.tabs.cockpitOverview') }}
          </router-link>
          <span class="api-type-text separator">/</span>
          <router-link to="/autopilot" class="api-type-text" :class="{ active: route.path === '/autopilot' }">
            {{ t('app.tabs.autopilot') }}
          </router-link>
          <span class="api-type-text separator">/</span>
          <router-link to="/cost-report" class="api-type-text" :class="{ active: route.path === '/cost-report' }">
            {{ t('app.tabs.costReport') }}
          </router-link>
          <span class="brand-text d-none d-md-inline">API Proxy - CCX</span>
        </div>
      </div>

      <v-spacer/>

      <!-- 版本信息（< 500px 隐藏，避免在窄屏挤压右侧按钮） -->
      <div
        v-if="!isDesktopWebUI && $vuetify.display.width >= 500 && systemStore.versionInfo.currentVersion"
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

      <!-- 新用户指引 -->
      <v-btn
        icon
        variant="text"
        size="small"
        class="header-btn"
        :title="t('guide.helpButton')"
        @click="openGuide"
      >
        <v-icon size="20">mdi-help-circle</v-icon>
      </v-btn>

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
        <v-card v-if="isAuthenticated && !['/conversations', '/health', '/autopilot', '/cost-report', '/subscriptions', '/cockpit'].includes(route.path)" class="mb-6 global-stats-panel">
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
        <v-row v-if="!['/conversations', '/health', '/autopilot', '/cost-report', '/subscriptions', '/cockpit'].includes(route.path)" class="mb-6 stat-cards-row">
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
        <div v-if="!['/conversations', '/health', '/autopilot', '/cost-report', '/subscriptions', '/cockpit'].includes(route.path)" class="action-bar mb-6">
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

            <v-btn size="large" prepend-icon="mdi-refresh" variant="text" class="action-btn" @click="refreshChannels">
              {{ t('app.actions.refresh') }}
            </v-btn>
          </div>

          <div class="action-bar-right">
            <!-- 熔断器配置按钮 -->
            <v-tooltip location="bottom" content-class="ccx-tooltip">
              <template #activator="{ props }">
                <v-btn
                  v-bind="props"
                  variant="tonal"
                  size="large"
                  color="default"
                  class="action-btn"
                  @click="openCircuitBreakerDialog"
                >
                  <v-icon start size="20">mdi-tune</v-icon>
                  TB
                </v-btn>
              </template>
              <span>{{ t('tooltip.circuitBreakerSettings') }}</span>
            </v-tooltip>

          </div>
        </div>
        <router-view
          @edit="editChannel"
          @delete="deleteChannel"
          @refresh="refreshChannels"
          @error="showErrorToast"
          @success="showSuccessToast"
        />
      </v-container>
    </v-main>

    <!-- 添加渠道模态框 -->
    <AddChannelModal
      v-model:show="dialogStore.showAddChannelModal"
      :channel-type="channelStore.activeTab"
      @save="saveChannel"
      @auto-added="handleAutoAddedChannel"
      @error="showErrorToast"
    />

    <!-- 编辑渠道模态框 -->
    <EditChannelModal
      v-model:show="dialogStore.showEditChannelModal"
      :channel="dialogStore.editingChannel"
      :channel-type="editingChannelType"
      @save="saveChannel"
      @updated="refreshChannels"
      @error="showErrorToast"
    />

    <!-- OTA 更新对话框 -->
    <UpdateDialog v-model="systemStore.updateDialogOpen" />

    <!-- 新用户指引对话框 -->
    <UserGuideDialog v-model="showGuide" />

    <!-- 能力测试对话框 -->
    <CapabilityTestDialog
      v-model:model-value="showCapabilityTestDialog"
      :channel-name="capabilityTestChannelName"
      :current-tab="channelStore.activeTab"
      :capability-job="capabilityTestJob"
      :capability-rpm="capabilityTestRpm"
      @update:capability-rpm="value => capabilityTestRpm = value"
      @copy-to-tab="handleCopyToTab"
      @cancel="handleCancelCapabilityTest"
      @retry-model="handleRetryCapabilityModel"
      @test-protocol="handleTestCapabilityProtocol"
    />

    <!-- 熔断器配置对话框 -->
    <v-dialog v-model="circuitBreakerDialogOpen" max-width="640">
      <v-card class="cb-dialog-card">
        <v-card-title class="cb-dialog-title">
          {{ t('dialog.circuitBreaker.title') }}
        </v-card-title>
        <v-divider />
        <v-card-text class="cb-dialog-body">
          <div class="cb-dialog-desc">
            {{ t('dialog.circuitBreaker.description') }}
          </div>

          <!-- 滑块区域 - 三列并排 -->
          <div class="cb-control-grid">
            <!-- 滑动窗口大小 -->
            <div class="cb-control">
              <div class="cb-control-header">
                <span class="cb-slider-label">{{ t('dialog.circuitBreaker.windowSize') }}</span>
                <span class="cb-slider-value">{{ cbForm.windowSize }}</span>
              </div>
              <input
                type="range"
                :value="cbForm.windowSize"
                :min="3"
                :max="100"
                step="1"
                class="cb-slider w-100"
                @input="onSliderChange('windowSize', $event)"
              />
              <div class="cb-slider-range">
                <span>3</span><span>100</span>
              </div>
            </div>

            <!-- 失败率阈值 -->
            <div class="cb-control">
              <div class="cb-control-header">
                <span class="cb-slider-label">{{ t('dialog.circuitBreaker.failureThreshold') }}</span>
                <span class="cb-slider-value">{{ cbForm.failureThreshold.toFixed(2) }}</span>
              </div>
              <input
                type="range"
                :value="cbForm.failureThreshold"
                :min="0.01"
                :max="1"
                step="0.01"
                class="cb-slider w-100"
                @input="onSliderChange('failureThreshold', $event)"
              />
              <div class="cb-slider-range">
                <span>0.01</span><span>1.00</span>
              </div>
            </div>

            <!-- 连续失败阈值 -->
            <div class="cb-control">
              <div class="cb-control-header">
                <span class="cb-slider-label">{{ t('dialog.circuitBreaker.consecutiveFailuresThreshold') }}</span>
                <span class="cb-slider-value">{{ cbForm.consecutiveFailuresThreshold }}</span>
              </div>
              <input
                type="range"
                :value="cbForm.consecutiveFailuresThreshold"
                :min="1"
                :max="100"
                step="1"
                class="cb-slider w-100"
                @input="onSliderChange('consecutiveFailuresThreshold', $event)"
              />
              <div class="cb-slider-range">
                <span>1</span><span>100</span>
              </div>
            </div>
          </div>

          <!-- 上游请求生命周期超时 -->
          <div class="cb-control-grid cb-control-grid--two">
            <div class="cb-control">
              <div class="cb-control-header">
                <span class="cb-slider-label">{{ t('dialog.circuitBreaker.requestTimeout') }}</span>
                <span class="cb-slider-value">{{ (cbForm.requestTimeoutMs / 1000) + 's' }}</span>
              </div>
              <input
                type="range"
                :value="cbForm.requestTimeoutMs"
                :min="1000"
                :max="300000"
                step="1000"
                class="cb-slider w-100"
                @input="onSliderChange('requestTimeoutMs', $event)"
              />
              <div class="cb-slider-range">
                <span>1s</span><span>300s</span>
              </div>
            </div>

            <div class="cb-control">
              <div class="cb-control-header">
                <span class="cb-slider-label">{{ t('dialog.circuitBreaker.responseHeaderTimeout') }}</span>
                <span class="cb-slider-value">{{ (cbForm.responseHeaderTimeoutMs / 1000) + 's' }}</span>
              </div>
              <input
                type="range"
                :value="cbForm.responseHeaderTimeoutMs"
                :min="1000"
                :max="300000"
                step="1000"
                class="cb-slider w-100"
                @input="onSliderChange('responseHeaderTimeoutMs', $event)"
              />
              <div class="cb-slider-range">
                <span>1s</span><span>300s</span>
              </div>
            </div>
          </div>

          <!-- 流式健康检测超时 -->
          <div class="cb-control-grid">
            <!-- 首字等待超时 -->
            <div class="cb-control">
              <div class="cb-control-header">
                <span class="cb-slider-label">{{ t('dialog.circuitBreaker.streamFirstContentTimeout') }}</span>
                <span class="cb-slider-value">{{ (cbForm.streamFirstContentTimeoutMs / 1000) + 's' }}</span>
              </div>
              <input
                type="range"
                :value="cbForm.streamFirstContentTimeoutMs"
                :min="5000"
                :max="300000"
                step="1000"
                class="cb-slider w-100"
                @input="onSliderChange('streamFirstContentTimeoutMs', $event)"
              />
              <div class="cb-slider-range">
                <span>5s</span><span>300s</span>
              </div>
            </div>

            <!-- 首字后断流超时 -->
            <div class="cb-control">
              <div class="cb-control-header">
                <span class="cb-slider-label">{{ t('dialog.circuitBreaker.streamInactivityTimeout') }}</span>
                <span class="cb-slider-value">{{ (cbForm.streamInactivityTimeoutMs / 1000) + 's' }}</span>
              </div>
              <input
                type="range"
                :value="cbForm.streamInactivityTimeoutMs"
                :min="1000"
                :max="180000"
                step="1000"
                class="cb-slider w-100"
                @input="onSliderChange('streamInactivityTimeoutMs', $event)"
              />
              <div class="cb-slider-range">
                <span>1s</span><span>180s</span>
              </div>
            </div>

            <!-- 工具调用空闲超时 -->
            <div class="cb-control">
              <div class="cb-control-header">
                <span class="cb-slider-label">{{ t('dialog.circuitBreaker.streamToolCallIdleTimeout') }}</span>
                <span class="cb-slider-value">{{ (cbForm.streamToolCallIdleTimeoutMs / 1000) + 's' }}</span>
              </div>
              <input
                type="range"
                :value="cbForm.streamToolCallIdleTimeoutMs"
                :min="30000"
                :max="300000"
                step="1000"
                class="cb-slider w-100"
                @input="onSliderChange('streamToolCallIdleTimeoutMs', $event)"
              />
              <div class="cb-slider-range">
                <span>30s</span><span>300s</span>
              </div>
            </div>
          </div>

          <!-- 预设按钮 -->
          <div class="cb-preset-grid">
            <v-btn
              v-for="preset in cbPresets"
              :key="preset.key"
              variant="flat"
              :color="activePreset === preset.key ? 'primary' : 'default'"
              size="small"
              class="cb-preset-btn"
              :class="{ 'cb-preset-active': activePreset === preset.key }"
              @click="applyPreset(preset)"
            >
              {{ t(preset.labelKey) }}
            </v-btn>
          </div>

        </v-card-text>
        <v-divider />
        <v-card-actions class="cb-dialog-actions">
          <v-spacer />
          <v-btn variant="outlined" class="cb-dialog-btn" @click="circuitBreakerDialogOpen = false">
            {{ t('app.actions.cancel') }}
            <span class="shortcut-hint ml-2 text-xs opacity-50">Esc</span>
          </v-btn>
          <v-btn color="primary" variant="elevated" class="cb-dialog-btn cb-dialog-btn-primary" :loading="cbSaving" @click="saveCircuitBreaker">
            {{ t('app.actions.confirm') }}
            <span class="shortcut-hint ml-2 text-xs opacity-50">{{ isMac ? '⌘Enter' : 'Ctrl+Enter' }}</span>
          </v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

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
          <v-btn variant="outlined" @click="dialogStore.closeAddKeyModal()">{{ t('app.actions.cancel') }} <span class="shortcut-hint ml-2 text-xs opacity-50">Esc</span></v-btn>
          <v-btn :disabled="!dialogStore.newApiKey.trim()" color="primary" variant="elevated" @click="addApiKey">{{ t('app.actions.add') }} <span class="shortcut-hint ml-2 text-xs opacity-50">Enter</span></v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <!-- 通用确认对话框（替代 window.confirm，兼容 Wails 桌面 iframe） -->
    <v-dialog v-model="dialogStore.showConfirmDialog" max-width="420" persistent>
      <v-card rounded="lg">
        <v-card-title class="d-flex align-center pt-4">
          <v-icon class="mr-3" :color="dialogStore.confirmDialogColor">mdi-alert-circle-outline</v-icon>
          {{ t('app.dialog.confirmTitle') }}
        </v-card-title>
        <v-card-text class="text-body-1 pt-2">{{ dialogStore.confirmDialogMessage }}</v-card-text>
        <v-card-actions>
          <v-spacer/>
          <v-btn variant="text" @click="dialogStore.resolveConfirm(false)">
            {{ dialogStore.confirmDialogCancelText || t('app.actions.cancel') }}
            <span class="shortcut-hint ml-2 text-xs opacity-50">Esc</span>
          </v-btn>
          <v-btn :color="dialogStore.confirmDialogColor" variant="elevated" @click="dialogStore.resolveConfirm(true)">
            {{ dialogStore.confirmDialogConfirmText || t('app.actions.confirm') }}
            <span class="shortcut-hint ml-2 text-xs opacity-50">{{ isMac ? '⌘Enter' : 'Ctrl+Enter' }}</span>
          </v-btn>
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
  </v-app>
</template>

<script setup lang="ts">
import { defineAsyncComponent } from 'vue'
import Logo from './components/Logo.vue'
import AddChannelModal from './components/AddChannelModal.vue'
import EditChannelModal from './components/EditChannelModal.vue'
import UpdateDialog from './components/UpdateDialog.vue'
import UserGuideDialog from './components/UserGuideDialog.vue'
import CapabilityTestDialog from './components/CapabilityTestDialog.vue'
import { useAppController } from './composables/useAppController'
import { useCapabilityTestManager } from './composables/useCapabilityTestManager'

// 异步加载图表组件，减少首屏 JS 体积
const GlobalStatsChart = defineAsyncComponent(() => import('./components/GlobalStatsChart.vue'))

const appController = useAppController()

const {
  route,
  theme,
  authStore,
  channelStore,
  preferencesStore,
  dialogStore,
  systemStore,
  t,
  setLocale,
  languageOptions,
  currentLocale,
  currentLanguageShortLabel,
  translatedApiTabOptions,
  editingChannelType,
  isDesktopWebUI,
  activeTrafficTitle,
  systemStatusText,
  systemStatusDesc,
  toasts,
  getToastColor,
  getToastIcon,
  showErrorToast,
  showSuccessToast,
  showToast,
  refreshChannels,
  saveChannel,
  handleAutoAddedChannel,
  editChannel,
  deleteChannel,
  openAddChannelModal,
  addApiKey,
  showGuide,
  openGuide,
  circuitBreakerDialogOpen,
  cbSaving,
  activePreset,
  cbForm,
  cbPresets,
  applyPreset,
  onSliderChange,
  openCircuitBreakerDialog,
  saveCircuitBreaker,
  isMac,
  toggleDarkMode,
  isAuthenticated,
  MAX_AUTH_ATTEMPTS,
  showAuthDialog,
  handleAuthSubmit,
  handleLogout,
  handleVersionClick,
} = appController

const {
  showCapabilityTestDialog,
  capabilityTestChannelName,
  capabilityTestJob,
  capabilityTestRpm,
  testChannelCapability,
  handleCopyToTab,
  handleCancelCapabilityTest,
  handleRetryCapabilityModel,
  handleTestCapabilityProtocol,
} = useCapabilityTestManager(channelStore, dialogStore, showToast, t, refreshChannels)
</script>

<style scoped src="./styles/app-retro.css"></style>

<!-- 全局样式 - 复古像素主题 -->
<style src="./styles/app-retro-global.css"></style>
