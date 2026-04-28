<template>
  <v-card elevation="0" rounded="lg" class="channel-orchestration" variant="flat">
    <!-- Scheduler statistics -->
    <v-card-title class="d-flex align-center justify-space-between py-3 px-0">
      <div class="d-flex align-center" style="flex-shrink: 1; min-width: 0;">
        <v-icon class="mr-2" color="primary">mdi-swap-vertical-bold</v-icon>
        <span class="text-h6" style="white-space: nowrap;">{{ t('orchestration.title') }}</span>
        <v-chip v-if="isMultiChannelMode" size="small" color="success" variant="tonal" class="ml-3 mode-chip">
          {{ t('orchestration.multiChannel') }}
        </v-chip>
        <v-chip v-else size="small" color="warning" variant="tonal" class="ml-3 mode-chip"> {{ t('orchestration.singleChannel') }} </v-chip>
      </div>
      <div class="d-flex align-center ga-2">
        <v-text-field
          v-model="searchQuery"
          density="compact"
          variant="outlined"
          :placeholder="t('orchestration.searchPlaceholder')"
          prepend-inner-icon="mdi-magnify"
          clearable
          hide-details
          single-line
          class="channel-search-field"
        />
        <v-progress-circular v-if="isLoadingMetrics" indeterminate size="16" width="2" color="primary" />
      </div>
    </v-card-title>

    <v-divider />

    <!-- Failover sequence (active + suspended) -->
    <div class="pt-3 pb-2">
      <div class="d-flex align-center justify-space-between mb-2">
        <div class="text-subtitle-2 text-medium-emphasis d-flex align-center">
          <v-icon size="small" class="mr-1" color="success">mdi-play-circle</v-icon>
          {{ t('orchestration.failoverSequence') }}
          <v-chip size="x-small" class="ml-2">{{ activeChannels.length }}</v-chip>
        </div>
        <div class="d-flex align-center ga-2">
          <span class="text-caption text-medium-emphasis">{{ t('orchestration.dragHint') }}</span>
          <v-progress-circular v-if="isSavingOrder" indeterminate size="16" width="2" color="primary" />
        </div>
      </div>

      <!-- Draggable list -->
      <draggable
        v-model="activeChannels"
        item-key="index"
        :handle="isSearchActive ? '.no-drag' : '.drag-handle'"
        ghost-class="ghost"
        class="channel-list"
        :disabled="isSearchActive"
        @change="onDragChange"
      >
        <template #item="{ element, index }">
          <div v-show="matchesSearch(element)" class="channel-item-wrapper">
            <div
              class="channel-row"
              :class="getChannelRowClass(element)"
              @click="toggleChannelChart(element.index)"
            >
              <!-- SVG activity waveform bar chart background -->
              <svg class="activity-chart-bg" preserveAspectRatio="none" viewBox="0 0 150 100">
                <!-- Gradient definitions (define a separate gradient for each bar) -->
                <defs>
                  <linearGradient
                    v-for="(bar, i) in getActivityBars(element.index)"
                    :id="`gradient-${element.index}-${i}`"
                    :key="`gradient-${element.index}-${i}`"
                    x1="0%"
                    y1="0%"
                    x2="0%"
                    y2="100%"
                  >
                    <stop offset="0%" :stop-color="bar.color" stop-opacity="0.8" />
                    <stop offset="100%" :stop-color="bar.color" stop-opacity="0.3" />
                  </linearGradient>
                </defs>
                <!-- Waveform bar chart -->
                <g v-for="(bar, i) in getActivityBars(element.index)" :key="i">
                  <rect
                    :x="bar.x"
                    :y="bar.y"
                    :width="bar.width"
                    :height="bar.height"
                    :fill="`url(#gradient-${element.index}-${i})`"
                    :rx="bar.radius"
                    :ry="bar.radius"
                    class="activity-bar"
                  />
                </g>
              </svg>

              <!-- Grid content container -->
              <div class="channel-row-content">
                <!-- Drag handle -->
                <div class="drag-handle" @click.stop>
                  <v-icon size="small" color="grey">mdi-drag-vertical</v-icon>
                </div>

            <!-- Priority index -->
            <div class="priority-number" @click.stop>
              <span class="text-caption font-weight-bold">{{ index + 1 }}</span>
            </div>

            <!-- Status indicator -->
            <div class="status-badge-wrapper" @click.stop>
              <ChannelStatusBadge :status="element.status || 'active'" :metrics="getChannelMetrics(element.index)" />
            </div>

            <!-- Channel name and description -->
            <div class="channel-name">
              <span
                class="font-weight-medium channel-name-link"
                tabindex="0"
                role="button"
                @click.stop="$emit('edit', element)"
                @keydown.enter.stop="$emit('edit', element)"
                @keydown.space.stop="$emit('edit', element)"
              >{{ element.name }}</span>
              <!-- Promotion period badge -->
              <v-chip
                v-if="isInPromotion(element)"
                size="x-small"
                color="info"
                variant="flat"
                class="ml-2"
              >
                <v-icon start size="12">mdi-rocket-launch</v-icon>
                {{ formatPromotionRemaining(element.promotionUntil) }}
              </v-chip>
              <!-- Official website link button -->
              <v-btn
                :href="getWebsiteUrl(element)"
                target="_blank"
                rel="noopener"
                icon
                size="x-small"
                variant="text"
                color="primary"
                class="ml-1"
                :title="t('orchestration.openWebsite')"
                @click.stop
              >
                <v-icon size="14">mdi-open-in-new</v-icon>
              </v-btn>
              <span class="text-caption text-medium-emphasis ml-2">{{ element.serviceType }}</span>
              <span v-if="element.description" class="text-caption text-disabled ml-3 channel-description">{{ element.description }}</span>
              <!-- Expand icon -->
              <v-icon
                size="x-small"
                class="ml-auto expand-icon"
                :color="expandedChannelIndex === element.index ? 'primary' : 'grey-lighten-1'"
              >{{ expandedChannelIndex === element.index ? 'mdi-chevron-up' : 'mdi-chevron-down' }}</v-icon>
            </div>

            <!-- Metrics display -->
            <div class="channel-metrics" @click.stop>
              <template v-if="getChannelMetrics(element.index)">
                <v-tooltip location="top" :open-delay="200" content-class="ccx-tooltip">
                  <template #activator="{ props: tooltipProps }">
                    <div v-bind="tooltipProps" class="d-flex align-center metrics-display">
                      <!-- Show success rate when there are requests in the last 15 minutes; otherwise show -- -->
                      <template v-if="get15mStats(element.index)?.requestCount">
                        <v-chip
                          size="x-small"
                          :color="getSuccessRateColor(get15mStats(element.index)?.successRate)"
                          variant="tonal"
                          class="metrics-chip success-chip"
                        >
                          {{ get15mStats(element.index)?.successRate?.toFixed(0) }}%
                        </v-chip>
                        <span class="request-summary ml-2 mr-1">
                          {{ get15mStats(element.index)?.requestCount }} {{ t('orchestration.requests') }}
                        </span>
                        <v-chip
                          v-if="shouldShowCacheHitRate(get15mStats(element.index))"
                          size="x-small"
                          :color="getCacheHitRateColor(get15mStats(element.index)?.cacheHitRate)"
                          variant="tonal"
                          class="ml-1 metrics-chip cache-chip"
                        >
                          {{ t('orchestration.cache') }} {{ get15mStats(element.index)?.cacheHitRate?.toFixed(0) }}%
                        </v-chip>
                      </template>
                      <span v-else class="text-caption text-medium-emphasis">--</span>
                    </div>
                  </template>
                  <div class="metrics-tooltip">
                    <div class="text-caption font-weight-bold mb-1">{{ t('orchestration.requestStats') }}</div>
                    <div class="metrics-tooltip-row">
                      <span>{{ t('orchestration.minutes15') }}:</span>
                      <span>{{ formatStats(get15mStats(element.index)) }}</span>
                    </div>
                    <div class="metrics-tooltip-row">
                      <span>{{ t('orchestration.hour1') }}:</span>
                      <span>{{ formatStats(get1hStats(element.index)) }}</span>
                    </div>
                    <div class="metrics-tooltip-row">
                      <span>{{ t('orchestration.hours6') }}:</span>
                      <span>{{ formatStats(get6hStats(element.index)) }}</span>
                    </div>
                    <div class="metrics-tooltip-row">
                      <span>{{ t('orchestration.hours24') }}:</span>
                      <span>{{ formatStats(get24hStats(element.index)) }}</span>
                    </div>

                    <div class="text-caption font-weight-bold mt-2 mb-1">{{ t('orchestration.cacheStats') }}</div>
                    <div class="metrics-tooltip-row">
                      <span>{{ t('orchestration.minutes15') }}:</span>
                      <span>{{ formatCacheStats(get15mStats(element.index)) }}</span>
                    </div>
                    <div class="metrics-tooltip-row">
                      <span>{{ t('orchestration.hour1') }}:</span>
                      <span>{{ formatCacheStats(get1hStats(element.index)) }}</span>
                    </div>
                    <div class="metrics-tooltip-row">
                      <span>{{ t('orchestration.hours6') }}:</span>
                      <span>{{ formatCacheStats(get6hStats(element.index)) }}</span>
                    </div>
                    <div class="metrics-tooltip-row">
                      <span>{{ t('orchestration.hours24') }}:</span>
                      <span>{{ formatCacheStats(get24hStats(element.index)) }}</span>
                    </div>
                  </div>
                </v-tooltip>
              </template>
              <span v-else class="text-caption text-medium-emphasis">--</span>
            </div>

            <!-- RPM/TPM display -->
            <div class="channel-rpm-tpm" @click.stop>
              <div class="rpm-tpm-values">
                <span class="rpm-value" :class="{ 'has-data': hasActivityData(element.index) }">{{ formatRPM(element.index) }}</span>
                <span class="rpm-tpm-separator">/</span>
                <span class="tpm-value" :class="{ 'has-data': hasActivityData(element.index) }">{{ formatTPM(element.index) }}</span>
              </div>
              <div class="rpm-tpm-labels">
                <span>RPM</span>
                <span>/</span>
                <span>TPM</span>
              </div>
            </div>

            <!-- Latency display -->
            <div class="channel-latency" @click.stop>
              <v-chip
                v-if="isLatencyValid(element)"
                size="x-small"
                :color="getLatencyColor(element.latency!)"
                variant="tonal"
              >
                {{ element.latency }}ms
              </v-chip>
            </div>

            <!-- API key count -->
            <div class="channel-keys d-flex align-center ga-1" @click.stop>
              <v-chip size="x-small" variant="outlined" class="keys-chip" @click="$emit('edit', element)">
                <v-icon start size="x-small">mdi-key</v-icon>
                {{ element.apiKeys?.length || 0 }}
              </v-chip>
              <v-tooltip v-if="element.disabledApiKeys?.length" :text="`${element.disabledApiKeys.length} 个密钥已拉黑`" location="top" color="warning" content-class="ccx-tooltip">
                <template #activator="{ props: tip }">
                  <v-chip v-bind="tip" size="x-small" color="warning" variant="tonal" @click="$emit('edit', element)">
                    {{ element.disabledApiKeys.length }}
                  </v-chip>
                </template>
              </v-tooltip>
              <v-tooltip v-if="element.cooldownApiKeys?.length" :text="`${element.cooldownApiKeys.length} 个密钥冷却中`" location="top" color="info" content-class="ccx-tooltip">
                <template #activator="{ props: tip }">
                  <v-chip v-bind="tip" size="x-small" color="info" variant="tonal" @click="$emit('edit', element)">
                    {{ element.cooldownApiKeys.length }}
                  </v-chip>
                </template>
              </v-tooltip>
            </div>

            <!-- Action buttons -->
            <div class="channel-actions" @click.stop>
              <!-- Show resume button for breaker-managed channels -->
              <v-btn
                v-if="isBreakerManagedChannel(element)"
                icon
                size="x-small"
                variant="text"
                color="warning"
                :title="t('orchestration.resume')"
                @click="resumeChannel(element.index)"
              >
                <v-icon size="small">mdi-refresh</v-icon>
              </v-btn>

              <v-btn
                icon
                size="x-small"
                variant="text"
                :title="t('orchestration.logs')"
                @click="openLogsDialog(element)"
              >
                <v-icon size="small">mdi-history</v-icon>
              </v-btn>

              <v-menu>
                <template #activator="{ props: menuProps }">
                  <v-btn
                    icon
                    size="x-small"
                    :variant="copiedChannelIndex === element.index ? 'flat' : 'text'"
                    :color="copiedChannelIndex === element.index ? 'success' : ''"
                    v-bind="menuProps"
                  >
                    <v-icon size="small">
                      {{ copiedChannelIndex === element.index ? 'mdi-check-bold' : 'mdi-dots-vertical' }}
                    </v-icon>
                  </v-btn>
                </template>
                <v-list density="compact">
                  <v-list-item @click="$emit('edit', element)">
                    <template #prepend>
                      <v-icon size="small">mdi-pencil</v-icon>
                    </template>
                    <v-list-item-title>{{ t('orchestration.edit') }}</v-list-item-title>
                  </v-list-item>
                  <v-list-item @click="$emit('ping', element.index)">
                    <template #prepend>
                      <v-icon size="small">mdi-speedometer</v-icon>
                    </template>
                    <v-list-item-title>{{ t('app.actions.ping') }}</v-list-item-title>
                  </v-list-item>
                  <v-list-item v-if="channelType !== 'images'" @click="$emit('testCapability', element.index)">
                    <template #prepend>
                      <v-icon size="small" color="success">mdi-test-tube</v-icon>
                    </template>
                    <v-list-item-title>{{ t('addChannel.testCapability') }}</v-list-item-title>
                  </v-list-item>
                  <v-list-item @click="openLogsDialog(element)">
                    <template #prepend>
                      <v-icon size="small">mdi-history</v-icon>
                    </template>
                    <v-list-item-title>{{ t('orchestration.logs') }}</v-list-item-title>
                  </v-list-item>
                  <v-list-item @click="copyChannelInfo(element)">
                    <template #prepend>
                      <v-icon size="small">mdi-content-copy</v-icon>
                    </template>
                    <v-list-item-title>{{ t('orchestration.copyConfig') }}</v-list-item-title>
                  </v-list-item>
                  <v-list-item @click="setPromotion(element)">
                    <template #prepend>
                      <v-icon size="small" color="info">mdi-rocket-launch</v-icon>
                    </template>
                    <v-list-item-title>{{ t('orchestration.promotion') }}</v-list-item-title>
                  </v-list-item>
                  <v-list-item v-if="index > 0" :disabled="isSavingOrder" @click="moveChannelToTop(element.index)">
                    <template #prepend>
                      <v-icon size="small" color="primary">mdi-arrow-collapse-up</v-icon>
                    </template>
                    <v-list-item-title>{{ t('orchestration.moveTop') }}</v-list-item-title>
                  </v-list-item>
                  <v-list-item v-if="index < activeChannels.length - 1" :disabled="isSavingOrder" @click="moveChannelToBottom(element.index)">
                    <template #prepend>
                      <v-icon size="small" color="primary">mdi-arrow-collapse-down</v-icon>
                    </template>
                    <v-list-item-title>{{ t('orchestration.moveBottom') }}</v-list-item-title>
                  </v-list-item>
                  <v-divider />
                  <v-list-item v-if="isBreakerManagedChannel(element)" @click="resumeChannel(element.index)">
                    <template #prepend>
                      <v-icon size="small" color="success">mdi-play-circle</v-icon>
                    </template>
                    <v-list-item-title>{{ t('orchestration.resumeReset') }}</v-list-item-title>
                  </v-list-item>
                  <v-list-item
                    v-if="element.status !== 'suspended'"
                    @click="setChannelStatus(element.index, 'suspended')"
                  >
                    <template #prepend>
                      <v-icon size="small" color="warning">mdi-pause-circle</v-icon>
                    </template>
                    <v-list-item-title>{{ t('orchestration.pause') }}</v-list-item-title>
                  </v-list-item>
                  <v-list-item @click="setChannelStatus(element.index, 'disabled')">
                    <template #prepend>
                      <v-icon size="small" color="error">mdi-stop-circle</v-icon>
                    </template>
                    <v-list-item-title>{{ t('orchestration.moveToPool') }}</v-list-item-title>
                  </v-list-item>
                  <v-list-item :disabled="!canDeleteChannel(element)" @click="handleDeleteChannel(element)">
                    <template #prepend>
                      <v-icon size="small" :color="canDeleteChannel(element) ? 'error' : 'grey'">mdi-delete</v-icon>
                    </template>
                    <v-list-item-title>
                      {{ t('orchestration.delete') }}
                      <span v-if="!canDeleteChannel(element)" class="text-caption text-disabled ml-1">
                        {{ t('orchestration.keepOne') }}
                      </span>
                    </v-list-item-title>
                  </v-list-item>
                </v-list>
              </v-menu>
            </div>
              </div><!-- .channel-row-content -->
          </div><!-- .channel-row -->

          <!-- Expanded chart area -->
          <v-expand-transition>
            <div v-if="expandedChannelIndex === element.index" class="channel-chart-wrapper">
              <KeyTrendChart
                :key="`chart-${channelType}-${element.index}`"
                :channel-id="element.index"
                :channel-type="channelType"
                @close="expandedChannelIndex = null"
              />
            </div>
          </v-expand-transition>
          </div>
        </template>
      </draggable>

      <!-- Empty state -->
      <div v-if="activeChannels.length === 0" class="text-center py-6 text-medium-emphasis">
        <v-icon size="48" color="grey-lighten-1">mdi-playlist-remove</v-icon>
        <div class="mt-2">{{ t('orchestration.noActiveChannels') }}</div>
        <div class="text-caption">{{ t('orchestration.enableFromPool') }}</div>
      </div>
    </div>

    <v-divider class="my-2" />

    <!-- Standby resource pool (disabled only) -->
    <div class="pt-2 pb-3">
      <div class="inactive-pool-header">
        <div class="text-subtitle-2 text-medium-emphasis d-flex align-center">
          <v-icon size="small" class="mr-1" color="grey">mdi-archive-outline</v-icon>
          {{ t('orchestration.standbyPool') }}
          <v-chip size="x-small" class="ml-2">{{ filteredInactiveChannels.length }}</v-chip>
        </div>
        <span class="text-caption text-medium-emphasis">{{ t('orchestration.appendToFailover') }}</span>
      </div>

      <div v-if="filteredInactiveChannels.length > 0" class="inactive-pool">
        <div v-for="channel in filteredInactiveChannels" :key="channel.index" class="inactive-channel-row">
          <!-- Channel information -->
          <div class="channel-info">
            <div class="channel-info-main">
              <span
                class="font-weight-medium channel-name-link"
                tabindex="0"
                role="button"
                @click="$emit('edit', channel)"
                @keydown.enter="$emit('edit', channel)"
                @keydown.space.prevent="$emit('edit', channel)"
              >{{ channel.name }}</span>
              <span class="text-caption text-disabled ml-2">{{ channel.serviceType }}</span>
            </div>
            <div v-if="channel.description" class="channel-info-desc text-caption text-disabled">
              {{ channel.description }}
            </div>
          </div>

          <!-- API key count -->
          <div class="channel-keys d-flex align-center ga-1">
            <v-chip size="x-small" variant="outlined" color="grey" class="keys-chip" @click="$emit('edit', channel)">
              <v-icon start size="x-small">mdi-key</v-icon>
              {{ channel.apiKeys?.length || 0 }}
            </v-chip>
            <v-tooltip v-if="channel.disabledApiKeys?.length" :text="`${channel.disabledApiKeys.length} 个密钥已拉黑`" location="top" color="warning" content-class="ccx-tooltip">
              <template #activator="{ props: tip }">
                <v-chip v-bind="tip" size="x-small" color="warning" variant="tonal" @click="$emit('edit', channel)">
                  {{ channel.disabledApiKeys.length }}
                </v-chip>
              </template>
            </v-tooltip>
            <v-tooltip v-if="channel.cooldownApiKeys?.length" :text="`${channel.cooldownApiKeys.length} 个密钥冷却中`" location="top" color="info" content-class="ccx-tooltip">
              <template #activator="{ props: tip }">
                <v-chip v-bind="tip" size="x-small" color="info" variant="tonal" @click="$emit('edit', channel)">
                  {{ channel.cooldownApiKeys.length }}
                </v-chip>
              </template>
            </v-tooltip>
          </div>

          <!-- Action buttons -->
          <div class="channel-actions">
            <v-btn size="small" color="success" variant="tonal" @click="enableChannel(channel.index)">
              <v-icon start size="small">mdi-play-circle</v-icon>
              {{ t('orchestration.enable') }}
            </v-btn>

            <v-menu>
              <template #activator="{ props: menuProps }">
                <v-btn
                  icon
                  size="x-small"
                  :variant="copiedChannelIndex === channel.index ? 'flat' : 'text'"
                  :color="copiedChannelIndex === channel.index ? 'success' : ''"
                  v-bind="menuProps"
                >
                  <v-icon size="small">
                    {{ copiedChannelIndex === channel.index ? 'mdi-check-bold' : 'mdi-dots-vertical' }}
                  </v-icon>
                </v-btn>
              </template>
              <v-list density="compact">
                <v-list-item @click="$emit('edit', channel)">
                  <template #prepend>
                    <v-icon size="small">mdi-pencil</v-icon>
                  </template>
                  <v-list-item-title>{{ t('orchestration.edit') }}</v-list-item-title>
                </v-list-item>
                <v-list-item v-if="channelType !== 'images'" @click="$emit('testCapability', channel.index)">
                  <template #prepend>
                    <v-icon size="small" color="success">mdi-test-tube</v-icon>
                  </template>
                  <v-list-item-title>{{ t('addChannel.testCapability') }}</v-list-item-title>
                </v-list-item>
                <v-list-item @click="copyChannelInfo(channel)">
                  <template #prepend>
                    <v-icon size="small">mdi-content-copy</v-icon>
                  </template>
                  <v-list-item-title>{{ t('orchestration.copyConfig') }}</v-list-item-title>
                </v-list-item>
                <v-divider />
                <v-list-item @click="enableChannel(channel.index)">
                  <template #prepend>
                    <v-icon size="small" color="success">mdi-play-circle</v-icon>
                  </template>
                  <v-list-item-title>{{ t('orchestration.enable') }}</v-list-item-title>
                </v-list-item>
                <v-list-item @click="$emit('delete', channel.index)">
                  <template #prepend>
                    <v-icon size="small" color="error">mdi-delete</v-icon>
                  </template>
                  <v-list-item-title>{{ t('orchestration.delete') }}</v-list-item-title>
                </v-list-item>
              </v-list>
            </v-menu>
          </div>
        </div>
      </div>

      <div v-else-if="isSearchActive && inactiveChannels.length > 0" class="text-center py-4 text-medium-emphasis text-caption">{{ t('orchestration.noMatchingStandby') }}</div>
      <div v-else class="text-center py-4 text-medium-emphasis text-caption">{{ t('orchestration.allActive') }}</div>
    </div>
    <!-- Channel logs dialog -->
    <ChannelLogsDialog
      v-model="showLogsDialog"
      :channel-index="logsChannelIndex"
      :channel-name="logsChannelName"
      :channel-type="channelType"
    />
  </v-card>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted, defineAsyncComponent } from 'vue'
import draggable from 'vuedraggable'
import { api, type Channel, type ChannelMetrics, type ChannelStatus, type TimeWindowStats, type ChannelRecentActivity, type SchedulerStatsResponse, expandSparseSegments } from '../services/api'
import { getChannelTypeApi } from '../utils/channelTypeApi'
import { useI18n } from '../i18n'
import ChannelStatusBadge from './ChannelStatusBadge.vue'
// Lazy-load chart components to reduce initial JS bundle size
const KeyTrendChart = defineAsyncComponent(() => import('./KeyTrendChart.vue'))
import ChannelLogsDialog from './ChannelLogsDialog.vue'

const props = defineProps<{
  channels: Channel[]
  currentChannelIndex: number
  channelType: 'messages' | 'chat' | 'responses' | 'gemini' | 'images'
  // Optional: metrics and stats passed from the parent component (when using the dashboard API)
  dashboardMetrics?: ChannelMetrics[]
  dashboardStats?: SchedulerStatsResponse
  // Optional: realtime activity data passed from the parent component
  dashboardRecentActivity?: ChannelRecentActivity[]
}>()

const emit = defineEmits<{
  (_e: 'edit', _channel: Channel): void
  (_e: 'delete', _channelId: number): void
  (_e: 'ping', _channelId: number): void
  (_e: 'testCapability', _channelId: number): void
  (_e: 'refresh'): void
  (_e: 'error', _message: string): void
  (_e: 'success', _message: string): void
}>()
const { t } = useI18n()
const getCurrentChannelTypeApi = () => getChannelTypeApi(api, props.channelType)

// State
const metrics = ref<ChannelMetrics[]>([])
const recentActivity = ref<ChannelRecentActivity[]>([])

// Search filtering
const searchQuery = ref('')
const isSearchActive = computed(() => !!searchQuery.value?.trim())
const matchesSearch = (channel: Channel) => {
  if (!isSearchActive.value) return true
  const q = searchQuery.value.trim().toLowerCase()
  return (
    channel.name?.toLowerCase().includes(q) ||
    channel.description?.toLowerCase().includes(q) ||
    channel.serviceType?.toLowerCase().includes(q) ||
    channel.baseUrl?.toLowerCase().includes(q)
  )
}

const schedulerStats = ref<SchedulerStatsResponse | null>(null)
const isLoadingMetrics = ref(false)
const isSavingOrder = ref(false)

// Channel logs dialog state
const showLogsDialog = ref(false)
const logsChannelIndex = ref(0)
const logsChannelName = ref('')
const openLogsDialog = (ch: Channel) => {
  logsChannelIndex.value = ch.index
  logsChannelName.value = ch.name
  showLogsDialog.value = true
}

// Validity period for latency test results (5 minutes)
const LATENCY_VALID_DURATION = 5 * 60 * 1000
// Timestamp used to trigger reactive updates
const currentTime = ref(Date.now())
let latencyCheckTimer: ReturnType<typeof setInterval> | null = null

// Timestamp used to trigger activity view updates (updated every 2 seconds)
const activityUpdateTick = ref(0)
let activityUpdateTimer: ReturnType<typeof setInterval> | null = null

// Chart expansion state
const expandedChannelIndex = ref<number | null>(null)

// Channel config copy state
const copiedChannelIndex = ref<number | null>(null)
let copyTimeoutId: ReturnType<typeof setTimeout> | null = null

// Toggle channel chart expansion/collapse
const toggleChannelChart = (channelIndex: number) => {
  expandedChannelIndex.value = expandedChannelIndex.value === channelIndex ? null : channelIndex
}

// Copy channel configuration to the clipboard (BaseURL + API keys, one per line)
// Note: copied content includes API keys (sensitive information), so share with caution
const copyChannelInfo = async (channel: Channel) => {
  // Clear the previous timeout to avoid race conditions
  if (copyTimeoutId) {
    clearTimeout(copyTimeoutId)
    copyTimeoutId = null
  }

  // Collect all BaseURLs
  const baseUrls: string[] = []
  if (channel.baseUrls && channel.baseUrls.length > 0) {
    baseUrls.push(...channel.baseUrls)
  } else if (channel.baseUrl) {
    baseUrls.push(channel.baseUrl)
  }

  // Build the copied content: BaseURLs and API keys separated by lines, filtering empty values and trimming
  const lines = [...baseUrls, ...(channel.apiKeys ?? [])]
    .map(s => s?.trim())
    .filter(Boolean)

  const content = lines.join('\n')

  // Set success state and start the timeout
  const setSuccessState = () => {
    copiedChannelIndex.value = channel.index
    copyTimeoutId = setTimeout(() => {
      copiedChannelIndex.value = null
      copyTimeoutId = null
    }, 2000)
  }

  try {
    await navigator.clipboard.writeText(content)
    setSuccessState()
  } catch (err) {
    console.error(t('orchestration.copyFailed'), err)
    // Fallback: use the traditional copy approach
    const textArea = document.createElement('textarea')
    textArea.value = content
    textArea.style.position = 'fixed'
    textArea.style.left = '-999999px'
    textArea.style.top = '-999999px'
    document.body.appendChild(textArea)
    textArea.focus()
    textArea.select()

    try {
      document.execCommand('copy')
      setSuccessState()
    } catch (copyErr) {
      console.error(t('orchestration.copyFailed'), copyErr)
    } finally {
      textArea.remove()
    }
  }
}

// Active channels (draggable and sortable) - includes active and suspended statuses
const activeChannels = ref<Channel[]>([])

// Computed: inactive channels - disabled status only
const inactiveChannels = computed(() => {
  return props.channels.filter(ch => ch.status === 'disabled')
})

// Computed: inactive channels after search filtering
const filteredInactiveChannels = computed(() => {
  return inactiveChannels.value.filter(matchesSearch)
})

// Computed: whether multi-channel mode is enabled
// Multi-channel mode detection logic:
// 1. Only one enabled channel 鈫?single-channel mode
// 2. One active channel + several suspended channels 鈫?single-channel mode
// 3. Multiple active channels 鈫?multi-channel mode
const isMultiChannelMode = computed(() => {
  const activeCount = props.channels.filter(
    ch => ch.status === 'active' || ch.status === undefined || ch.status === ''
  ).length
  return activeCount > 1
})

// Initialize the active channel list - both active and suspended channels participate in the failover sequence
// Optimization: update only when the structure changes to avoid frequent rebuilds that destroy child components
const initActiveChannels = () => {
  const newActive = props.channels
    .filter(ch => ch.status !== 'disabled')
    .sort((a, b) => (a.priority ?? a.index) - (b.priority ?? b.index))

  // Check whether an update is needed by comparing index lists
  const currentIndexes = activeChannels.value.map(ch => ch.index).join(',')
  const newIndexes = newActive.map(ch => ch.index).join(',')

  if (currentIndexes !== newIndexes) {
    // Structure changed (added/removed/reordered), array must be rebuilt
    activeChannels.value = [...newActive]
  } else {
    // Structure unchanged, update properties on existing objects only (preserve references)
    activeChannels.value.forEach((ch, i) => {
      Object.assign(ch, newActive[i])
    })
  }
}

// Watch channel changes
watch(() => props.channels, initActiveChannels, { immediate: true, deep: true })

// Watch dashboard prop changes (merged data passed from the parent component)
watch(() => props.dashboardMetrics, (newMetrics) => {
  if (newMetrics) {
    metrics.value = newMetrics
  }
}, { immediate: true })

watch(() => props.dashboardStats, (newStats) => {
  if (newStats) {
    schedulerStats.value = newStats
  }
}, { immediate: true })

// Watch recentActivity prop changes
watch(() => props.dashboardRecentActivity, (newActivity) => {
  recentActivity.value = newActivity ?? []
}, { immediate: true })

// Watch channelType changes - refresh metrics and collapse charts on switch
watch(() => props.channelType, () => {
  searchQuery.value = '' // Clear the search when switching tabs
  expandedChannelIndex.value = null // Collapse the expanded chart
  // Refresh locally if dashboard props are not being used
  if (!props.dashboardMetrics) {
    refreshMetrics()
  }
})

// Fetch channel metrics
const getChannelMetrics = (channelIndex: number): ChannelMetrics | undefined => {
  return metrics.value.find(m => m.channelIndex === channelIndex)
}

// Helper method for fetching time-sliced statistics
const get15mStats = (channelIndex: number) => {
  return getChannelMetrics(channelIndex)?.timeWindows?.['15m']
}

const get1hStats = (channelIndex: number) => {
  return getChannelMetrics(channelIndex)?.timeWindows?.['1h']
}

const get6hStats = (channelIndex: number) => {
  return getChannelMetrics(channelIndex)?.timeWindows?.['6h']
}

const get24hStats = (channelIndex: number) => {
  return getChannelMetrics(channelIndex)?.timeWindows?.['24h']
}

// Get success-rate color
const getSuccessRateColor = (rate?: number): string => {
  if (rate === undefined) return 'grey'
  if (rate >= 90) return 'success'
  if (rate >= 70) return 'warning'
  return 'error'
}

const getCacheHitRateColor = (rate?: number): string => {
  if (rate === undefined) return 'grey'
  if (rate >= 50) return 'success'
  if (rate >= 20) return 'info'
  if (rate >= 5) return 'warning'
  return 'orange'
}

const shouldShowCacheHitRate = (stats?: TimeWindowStats): boolean => {
  if (!stats || !stats.requestCount) return false
  const inputTokens = stats.inputTokens ?? 0
  const cacheReadTokens = stats.cacheReadTokens ?? 0
  return (inputTokens + cacheReadTokens) > 0
}

// Get latency color
const getLatencyColor = (latency: number): string => {
  if (latency < 500) return 'success'
  if (latency < 1000) return 'warning'
  return 'error'
}

// Check whether the latency test result is still valid (within 5 minutes)
const isLatencyValid = (channel: Channel): boolean => {
  // Do not display when there is no latency value
  if (channel.latency === undefined || channel.latency === null) return false
  // Do not display when there is no test timestamp (for compatibility with old data)
  if (!channel.latencyTestTime) return false
  // Check whether it is within the validity period (use currentTime.value to trigger reactive updates)
  return (currentTime.value - channel.latencyTestTime) < LATENCY_VALID_DURATION
}

// Check whether the channel is in a promotion period
const isInPromotion = (channel: Channel): boolean => {
  if (!channel.promotionUntil) return false
  return new Date(channel.promotionUntil) > new Date()
}

// Format the remaining promotion period
const formatPromotionRemaining = (until?: string): string => {
  if (!until) return ''
  const remaining = Math.max(0, new Date(until).getTime() - Date.now())
  const minutes = Math.ceil(remaining / 60000)
  if (minutes <= 0) return t('orchestration.endingSoon')
  return t('orchestration.minutesRemaining', { count: minutes })
}

// Format stats: show "N requests (X%)" when requests exist, otherwise show "--"
const formatStats = (stats?: TimeWindowStats): string => {
  if (!stats || !stats.requestCount) return '--'
  return `${stats.requestCount} ${t('orchestration.requests')} (${stats.successRate?.toFixed(0)}%)`
}

const formatTokens = (num?: number): string => {
  const value = num ?? 0
  if (value >= 1000000) return `${(value / 1000000).toFixed(1)}M`
  if (value >= 1000) return `${(value / 1000).toFixed(1)}K`
  return Math.round(value).toString()
}

const formatCacheStats = (stats?: TimeWindowStats): string => {
  if (!stats || !stats.requestCount) return '--'

  const inputTokens = stats.inputTokens ?? 0
  const cacheReadTokens = stats.cacheReadTokens ?? 0
  const cacheCreationTokens = stats.cacheCreationTokens ?? 0
  const denom = inputTokens + cacheReadTokens

  if (denom <= 0) return '--'

  const hitRate = stats.cacheHitRate ?? (cacheReadTokens / denom * 100)
  return `${t('orchestration.hitRate')} ${hitRate.toFixed(0)}% 路 ${t('orchestration.read')} ${formatTokens(cacheReadTokens)} 路 ${t('orchestration.write')} ${formatTokens(cacheCreationTokens)}`
}

// Get the official website URL (prefer website; otherwise extract the domain from baseUrl)
const getWebsiteUrl = (channel: Channel): string => {
  if (channel.website) return channel.website
  try {
    const url = new URL(channel.baseUrl)
    return `${url.protocol}//${url.host}`
  } catch {
    return channel.baseUrl
  }
}

// ============== Realtime channel activity helpers ==============

// Activity data Map cache (avoids linear lookup)
const activityMap = computed(() => {
  const map = new Map<number, ChannelRecentActivity>()
  for (const a of recentActivity.value) {
    map.set(a.channelIndex, a)
  }
  return map
})

// Historical maximum request count per channel (used to stabilize bar height scaling)
// Structure: { max: number, updatedAt: number }
const maxRequestsHistory = ref(new Map<number, { max: number; updatedAt: number }>())

// Exponential decay parameters
const DECAY_HALF_LIFE = 5 * 60 * 1000  // Half-life: 5 minutes
const MIN_MAX_REQUESTS = 1  // Minimum baseline value to avoid division by zero

// Calculate the decayed historical maximum
const getDecayedMax = (record: { max: number; updatedAt: number }, now: number): number => {
  const elapsed = now - record.updatedAt
  // Exponential decay: max * 0.5^(elapsed / halfLife)
  const decayFactor = Math.pow(0.5, elapsed / DECAY_HALF_LIFE)
  return Math.max(record.max * decayFactor, MIN_MAX_REQUESTS)
}

// Update the historical maximum (with exponential decay)
watch(activityMap, (newMap) => {
  const now = Date.now()

  for (const [channelIndex, activity] of newMap.entries()) {
    // Expand sparse segments into an array
    const segments = expandSparseSegments(activity)
    if (segments.length === 0) continue

    const currentMax = Math.max(...segments.map(s => s.requestCount), 0)
    const record = maxRequestsHistory.value.get(channelIndex)

    if (!record) {
      // First record
      if (currentMax > 0) {
        maxRequestsHistory.value.set(channelIndex, { max: currentMax, updatedAt: now })
      }
      continue
    }

    // Calculate the decayed historical maximum
    const decayedMax = getDecayedMax(record, now)

    if (currentMax >= decayedMax) {
      // If the current value exceeds the decayed historical value, update it to the current value
      maxRequestsHistory.value.set(channelIndex, { max: currentMax, updatedAt: now })
    } else {
      // If the current value is smaller, keep the decayed value (update the timestamp so decay can continue)
      maxRequestsHistory.value.set(channelIndex, { max: decayedMax, updatedAt: now })
    }
  }
})

// Get channel activity data
const getChannelActivity = (channelIndex: number): ChannelRecentActivity | undefined => {
  return activityMap.value.get(channelIndex)
}

// Cache bar chart data for all channels (avoid repeated computation in the template)
const activityBarsCache = computed(() => {
  const cache = new Map<number, Array<{ x: number; y: number; width: number; height: number; radius: number; color: string }>>()

  // Use activityUpdateTick to trigger reactive updates
  const _ = activityUpdateTick.value

  for (const [channelIndex, activity] of activityMap.value.entries()) {
    if (!activity) {
      cache.set(channelIndex, [])
      continue
    }

    // Expand sparse segments into an array
    const segments = expandSparseSegments(activity)
    const numSegments = segments.length  // 150 (the backend has already aggregated data into 6-second segments)

    if (numSegments === 0) {
      cache.set(channelIndex, [])
      continue
    }

    // One bar per segment
    const barWidth = 150 / numSegments
    const barGap = barWidth * 0.2  // 20% gap
    const actualBarWidth = barWidth - barGap

    // Use the decayed historical maximum as the normalization baseline
    const now = Date.now()
    const record = maxRequestsHistory.value.get(channelIndex)
    const currentMax = Math.max(...segments.map(s => s.requestCount), 1)
    const maxRequests = record ? Math.max(getDecayedMax(record, now), currentMax) : currentMax

    const bars: Array<{ x: number; y: number; width: number; height: number; radius: number; color: string }> = []

    for (let i = 0; i < numSegments; i++) {
      const segment = segments[i]
      const requests = segment.requestCount

      // Calculate bar height (minimum height 2 to avoid disappearing completely)
      const heightPercent = requests / maxRequests
      const height = Math.max(heightPercent * 85, requests > 0 ? 2 : 0)
      const y = 100 - height

      // Calculate color based on the success rate of this 6-second segment (7 levels: extreme bands + integer bands)
      let color = 'rgb(74, 222, 128)'  // Default green (no requests or 100% success)

      if (requests > 0) {
        const successCount = requests - segment.failureCount
        const successRate = (successCount / requests) * 100

        if (successRate < 5) {
          color = 'rgb(220, 38, 38)'       // 0-5%: dark red (extreme failure)
        } else if (successRate < 20) {
          color = 'rgb(239, 68, 68)'       // 5-20%: red (severe failure)
        } else if (successRate < 40) {
          color = 'rgb(249, 115, 22)'      // 20-40%: dark orange (high failure rate)
        } else if (successRate < 60) {
          color = 'rgb(251, 146, 60)'      // 40-60%: orange (moderate failure rate)
        } else if (successRate < 80) {
          color = 'rgb(250, 204, 21)'      // 60-80%: yellow (minor failure)
        } else if (successRate < 95) {
          color = 'rgb(132, 204, 22)'      // 80-95%: yellow-green (good)
        } else {
          color = 'rgb(34, 197, 94)'       // 95-100%: green (excellent)
        }
      }

      bars.push({
        x: i * barWidth + barGap / 2,
        y,
        width: actualBarWidth,
        height,
        radius: Math.min(actualBarWidth / 2, 1.5),  // Corner radius
        color
      })
    }

    cache.set(channelIndex, bars)
  }

  return cache
})

// Generate waveform bar chart data (read from cache)
const getActivityBars = (channelIndex: number): Array<{ x: number; y: number; width: number; height: number; radius: number; color: string }> => {
  return activityBarsCache.value.get(channelIndex) ?? []
}

// Generate a smoothed curve path (using moving average + Catmull-Rom spline)
const getActivityPath = (channelIndex: number): string => {
  const activity = getChannelActivity(channelIndex)
  if (!activity) return ''

  // Use activityUpdateTick to trigger reactive updates

  const _ = activityUpdateTick.value

  // Expand sparse segments into an array
  const segments = expandSparseSegments(activity)
  const numSegments = segments.length  // 150 (the backend has already aggregated data into 6-second segments)

  if (numSegments === 0) return ''

  // Find the maximum request count for normalization
  const maxRequests = Math.max(...segments.map(s => s.requestCount), 1)

  // Apply a moving average to smooth data (window size 5 = 10 seconds)
  const windowSize = 5
  const smoothedData: number[] = []

  for (let i = 0; i < numSegments; i++) {
    const start = Math.max(0, i - Math.floor(windowSize / 2))
    const end = Math.min(numSegments, i + Math.ceil(windowSize / 2))
    let sum = 0
    let count = 0

    for (let j = start; j < end; j++) {
      sum += segments[j].requestCount
      count++
    }

    smoothedData.push(count > 0 ? sum / count : 0)
  }

  // Generate the smoothed points
  const points: { x: number; y: number }[] = []
  for (let i = 0; i < numSegments; i++) {
    const x = i
    const y = 100 - (smoothedData[i] / maxRequests * 85)
    points.push({ x, y })
  }

  if (points.length < 2) return ''

  // Generate a smooth curve with a Catmull-Rom spline
  return catmullRomToPath(points)
}

// Convert a Catmull-Rom spline to an SVG B茅zier path
function catmullRomToPath(points: { x: number; y: number }[]): string {
  if (points.length < 2) return ''

  const path: string[] = []
  path.push(`M ${points[0].x} ${points[0].y}`)

  // Tension parameter (0.3 = lower tension, curve stays closer to the original points)
  const tension = 0.3

  for (let i = 0; i < points.length - 1; i++) {
    const p0 = points[Math.max(0, i - 1)]
    const p1 = points[i]
    const p2 = points[i + 1]
    const p3 = points[Math.min(points.length - 1, i + 2)]

    // Calculate control points
    const cp1x = p1.x + (p2.x - p0.x) / 6 * tension
    const cp1y = p1.y + (p2.y - p0.y) / 6 * tension
    const cp2x = p2.x - (p3.x - p1.x) / 6 * tension
    const cp2y = p2.y - (p3.y - p1.y) / 6 * tension

    path.push(`C ${cp1x} ${cp1y}, ${cp2x} ${cp2y}, ${p2.x} ${p2.y}`)
  }

  return path.join(' ')
}

// Generate the filled area path for the smoothed curve
const _getActivityAreaPath = (channelIndex: number): string => {
  const linePath = getActivityPath(channelIndex)
  if (!linePath) return ''

  const activity = getChannelActivity(channelIndex)
  if (!activity) return ''

  // Expand sparse segments into an array
  const segments = expandSparseSegments(activity)
  const numSegments = segments.length

  if (numSegments === 0) return ''

  // Add a closing segment to the bottom after the curve path
  return `${linePath} L ${numSegments - 1} 100 L 0 100 Z`
}

// Get the channel activity gradient background (deprecated; replaced with SVG curves)
const _getActivityGradient = (channelIndex: number): string => {
  const activity = getChannelActivity(channelIndex)
  if (!activity) return 'transparent'

  // Expand sparse segments into an array
  const segments = expandSparseSegments(activity)
  const numSegments = segments.length

  if (numSegments === 0) return 'transparent'

  // Check whether there is any activity
  const hasActivity = segments.some(seg => seg.requestCount > 0)
  if (!hasActivity) return 'transparent'

  // Use activityUpdateTick to trigger reactive updates

  const _ = activityUpdateTick.value

  // The backend returns 150 segments (6 seconds per segment)
  // Use raw data directly without weighted averaging so users see feedback immediately after calling the API

  // Generate a color for each 6-second segment (based on raw request count)
  const segmentColors: string[] = []

  for (let i = 0; i < numSegments; i++) {
    const seg = segments[i]

    // Make it transparent when there are no requests
    if (seg.requestCount === 0) {
      segmentColors.push('transparent')
      continue
    }

    const hasFailure = seg.failureCount > 0

    if (hasFailure) {
      const failureRatio = seg.failureCount / seg.requestCount
      if (failureRatio >= 0.5) {
        // High failure rate: red
        const intensity = Math.min(0.5, 0.2 + seg.requestCount * 0.01)
        segmentColors.push(`rgba(239, 68, 68, ${intensity})`)
      } else {
        // Partial failures: orange
        const intensity = Math.min(0.4, 0.15 + seg.requestCount * 0.008)
        segmentColors.push(`rgba(251, 146, 60, ${intensity})`)
      }
    } else {
      // Pure success: green, with 6 intensity levels based on request count
      if (seg.requestCount >= 20) segmentColors.push('rgba(22, 163, 74, 0.65)')       // Very dark green
      else if (seg.requestCount >= 15) segmentColors.push('rgba(22, 163, 74, 0.55)')  // Dark green
      else if (seg.requestCount >= 10) segmentColors.push('rgba(34, 197, 94, 0.50)')  // Medium-dark green
      else if (seg.requestCount >= 6) segmentColors.push('rgba(34, 197, 94, 0.42)')   // Medium green
      else if (seg.requestCount >= 3) segmentColors.push('rgba(74, 222, 128, 0.38)')  // Light green
      else segmentColors.push('rgba(74, 222, 128, 0.30)')                              // Very light green
    }
  }

  // Generate the gradient: each segment takes 100/150%
  const stops = segmentColors.map((color, i) => {
    const start = (i / numSegments * 100).toFixed(3)
    const end = ((i + 1) / numSegments * 100).toFixed(3)
    return `${color} ${start}%, ${color} ${end}%`
  }).join(', ')

  return `linear-gradient(to right, ${stops})`
}

// Format RPM display
const formatRPM = (channelIndex: number): string => {
  const activity = getChannelActivity(channelIndex)
  if (!activity || !activity.rpm) return '--'
  if (activity.rpm >= 10) return activity.rpm.toFixed(0)
  return activity.rpm.toFixed(1)
}

// Format TPM display
const formatTPM = (channelIndex: number): string => {
  const activity = getChannelActivity(channelIndex)
  if (!activity || !activity.tpm) return '--'
  if (activity.tpm >= 1000000) return `${(activity.tpm / 1000000).toFixed(1)}M`
  if (activity.tpm >= 1000) return `${(activity.tpm / 1000).toFixed(1)}K`
  return activity.tpm.toFixed(0)
}

// Check whether the channel has activity data
const hasActivityData = (channelIndex: number): boolean => {
  const activity = getChannelActivity(channelIndex)
  if (!activity) return false
  return activity.rpm > 0 || activity.tpm > 0
}

// Refresh metrics
const refreshMetrics = async () => {
  isLoadingMetrics.value = true
  try {
    const channelTypeApi = getCurrentChannelTypeApi()
    const [metricsData, statsData] = await Promise.all([
      channelTypeApi.getMetrics(),
      channelTypeApi.getSchedulerStats()
    ])
    metrics.value = metricsData
    schedulerStats.value = statsData
  } catch (error) {
    console.error('Failed to load metrics:', error)
  } finally {
    isLoadingMetrics.value = false
  }
}

// Drag change event - auto-save order
const onDragChange = () => {
  // Auto-save the order to the backend after dragging
  saveOrder()
}

// Save order
const saveOrder = async () => {
  isSavingOrder.value = true
  try {
    const order = activeChannels.value.map(ch => ch.index)
    await getCurrentChannelTypeApi().reorder(order)
    // Do not call emit('refresh') to avoid list flicker caused by parent refresh
  } catch (error) {
    console.error('Failed to save order:', error)
    const errorMessage = error instanceof Error ? error.message : t('addChannel.unknownError')
    emit('error', t('toast.operationFailed', { message: errorMessage }))
    // Reinitialize the list when save fails to restore the original order
    initActiveChannels()
  } finally {
    isSavingOrder.value = false
  }
}

// Move channel to top
const moveChannelToTop = async (channelIndex: number) => {
  if (isSavingOrder.value) return
  const idx = activeChannels.value.findIndex(ch => ch.index === channelIndex)
  if (idx <= 0) return

  const [channel] = activeChannels.value.splice(idx, 1)
  activeChannels.value.unshift(channel)
  await saveOrder()
}

// Move channel to bottom
const moveChannelToBottom = async (channelIndex: number) => {
  if (isSavingOrder.value) return
  const idx = activeChannels.value.findIndex(ch => ch.index === channelIndex)
  if (idx < 0 || idx >= activeChannels.value.length - 1) return

  const [channel] = activeChannels.value.splice(idx, 1)
  activeChannels.value.push(channel)
  await saveOrder()
}

const setChannelStatusInternal = async (
  channelId: number,
  status: ChannelStatus,
  options: { refresh?: boolean } = {}
) => {
  const { refresh = true } = options
  await getCurrentChannelTypeApi().setStatus(channelId, status)
  if (refresh) {
    emit('refresh')
  }
}

// Set channel status
const setChannelStatus = async (channelId: number, status: ChannelStatus) => {
  try {
    await setChannelStatusInternal(channelId, status)
  } catch (error) {
    console.error('Failed to set channel status:', error)
    const errorMessage = error instanceof Error ? error.message : t('addChannel.unknownError')
    emit('error', t('toast.operationFailed', { message: errorMessage }))
  }
}

// Enable channel (move it from the standby pool to the active sequence)
const enableChannel = async (channelId: number) => {
  await setChannelStatus(channelId, 'active')
}

const resumeChannelInternal = async (
  channelId: number,
  options: { refresh?: boolean, notify?: boolean } = {}
) => {
  const { refresh = true, notify = true } = options

  const result = await getCurrentChannelTypeApi().resume(channelId)
  await setChannelStatusInternal(channelId, 'active', { refresh })

  if (notify) {
    if ((result?.restoredKeys || 0) > 0) {
      emit('success', t('orchestration.resumeSuccessWithKeys', { count: result?.restoredKeys || 0 }))
    } else {
      emit('success', t('orchestration.resumeSuccess'))
    }
  }

  return result
}

const isTrippedChannel = (channel: Channel): boolean => {
  const channelMetrics = getChannelMetrics(channel.index)
  return channel.status === 'suspended' || channelMetrics?.circuitState === 'open'
}

const isBreakerManagedChannel = (channel: Channel): boolean => {
  const channelMetrics = getChannelMetrics(channel.index)
  return channel.status === 'suspended' || channelMetrics?.circuitState === 'open' || channelMetrics?.circuitState === 'half_open'
}

const getChannelRowClass = (channel: Channel) => {
  return {
    'is-tripped': isTrippedChannel(channel)
  }
}

// Resume channel (reset metrics and set it to active)
const resumeChannel = async (channelId: number) => {
  try {
    await resumeChannelInternal(channelId)
  } catch (error) {
    console.error('Failed to resume channel:', error)
    const errorMessage = error instanceof Error ? error.message : t('addChannel.unknownError')
    emit('error', t('toast.operationFailed', { message: errorMessage }))
  }
}

// Set channel promotion via the correct API for the current channel type
const setChannelPromotionInternal = async (channelId: number, durationSeconds: number) => {
  await getCurrentChannelTypeApi().promote(channelId, durationSeconds)
}

// Set the channel promotion period (boost priority)
const setPromotion = async (channel: Channel) => {
  try {
    const PROMOTION_DURATION = 300 // 5 minutes

    // If the channel is in a breaker-managed state, resume it first
    if (isBreakerManagedChannel(channel)) {
      await resumeChannelInternal(channel.index, { refresh: false, notify: false })
    }

    await setChannelPromotionInternal(channel.index, PROMOTION_DURATION)
    emit('refresh')
    // Notify the user
    emit('success', t('orchestration.promotionSuccess', { name: channel.name }))
  } catch (error) {
    emit('refresh')
    console.error('Failed to set promotion:', error)
    const errorMessage = error instanceof Error ? error.message : t('addChannel.unknownError')
    emit('error', t('toast.operationFailed', { message: errorMessage }))
  }
}

// Check whether the channel can be deleted
// Rule: keep at least one active channel in the failover sequence
const canDeleteChannel = (channel: Channel): boolean => {
  // Count the number of currently active channels
  const activeCount = activeChannels.value.filter(
    ch => ch.status === 'active' || ch.status === undefined || ch.status === ''
  ).length

  // Do not allow deletion if the target is an active channel and it is the last active one
  const isActive = channel.status === 'active' || channel.status === undefined || channel.status === ''
  if (isActive && activeCount <= 1) {
    return false
  }

  return true
}

// Handle channel deletion
const handleDeleteChannel = (channel: Channel) => {
  if (!canDeleteChannel(channel)) {
    emit('error', t('orchestration.deleteActiveGuard'))
    return
  }
  emit('delete', channel.index)
}

// Load metrics and start the latency expiry check timer when the component mounts
onMounted(() => {
  refreshMetrics()
  // Update currentTime every 30 seconds to trigger reactive latency display updates
  latencyCheckTimer = setInterval(() => {
    currentTime.value = Date.now()
  }, 30000)
  // Update activityUpdateTick every 2 seconds to trigger activity view updates
  activityUpdateTimer = setInterval(() => {
    activityUpdateTick.value++
  }, 2000)
})

// Clear timers when the component unmounts
onUnmounted(() => {
  if (latencyCheckTimer) {
    clearInterval(latencyCheckTimer)
    latencyCheckTimer = null
  }
  if (activityUpdateTimer) {
    clearInterval(activityUpdateTimer)
    activityUpdateTimer = null
  }
  if (copyTimeoutId) {
    clearTimeout(copyTimeoutId)
    copyTimeoutId = null
  }
})

// Expose methods to the parent component
defineExpose({
  refreshMetrics
})
</script>

<style scoped>
/* =====================================================
   Channel orchestration - retro pixel theme styles
   Neo-Brutalism: sharp corners, bold black borders, hard shadows
   ===================================================== */

.channel-orchestration {
  overflow: hidden;
  background: transparent;
  border: none;
}

.channel-search-field {
  width: 280px;
  max-width: 40vw;
  flex-shrink: 1;
}

.channel-list {
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.channel-item-wrapper {
  display: flex;
  flex-direction: column;
}

.channel-row {
  position: relative;
  padding: 10px 12px;
  margin: 2px;
  background: rgb(var(--v-theme-surface));
  border: 2px solid rgb(var(--v-theme-on-surface));
  box-shadow: 4px 4px 0 0 rgb(var(--v-theme-on-surface));
  min-height: 52px;
  transition: all 0.1s ease;
  cursor: pointer;
  overflow: hidden;
}

/* Grid content container */
.channel-row-content {
  display: grid;
  grid-template-columns:
    28px
    28px
    minmax(72px, auto)
    minmax(0, 1fr)
    minmax(168px, auto)
    minmax(96px, auto)
    minmax(64px, auto)
    minmax(68px, auto)
    auto;
  align-items: center;
  gap: 6px;
  position: relative;
  z-index: 1;
}

/* SVG activity waveform bar chart background */
.activity-chart-bg {
  position: absolute;
  top: 0;
  left: 0;
  width: 100%;
  height: 100%;
  pointer-events: none;
  z-index: 0;
}

/* Disable bar chart animation to avoid shrink-grow jitter during data updates */
.activity-bar {
  transition: none;
}

/* Expanded chart area */
.channel-chart-wrapper {
  margin: 0 2px 8px 2px;
}

.channel-row:hover {
  background: rgba(var(--v-theme-primary), 0.08);
  transform: translate(-2px, -2px);
  box-shadow: 6px 6px 0 0 rgb(var(--v-theme-on-surface));
  border: 2px solid rgb(var(--v-theme-on-surface));
}

.channel-row:active {
  transform: translate(2px, 2px);
  box-shadow: none;
}

.v-theme--dark .channel-row {
  background: rgb(var(--v-theme-surface));
  border-color: rgba(255, 255, 255, 0.7);
  box-shadow: 4px 4px 0 0 rgba(255, 255, 255, 0.7);
}
.v-theme--dark .channel-row:hover {
  background: rgba(var(--v-theme-primary), 0.12);
  box-shadow: 6px 6px 0 0 rgba(255, 255, 255, 0.7);
  border-color: rgba(255, 255, 255, 0.7);
}

/* Visual distinction for unavailable channels */
.channel-row.is-tripped {
  background: var(--ccx-channel-row-suspended-bg);
  border-color: var(--ccx-channel-row-suspended-border);
  box-shadow: var(--ccx-channel-row-shadow);
}
.channel-row.is-tripped:hover {
  background: var(--ccx-channel-row-suspended-hover-bg);
  box-shadow: var(--ccx-channel-row-shadow-hover);
}

.v-theme--dark .channel-row.is-tripped {
  background: var(--ccx-channel-row-suspended-bg);
  border-color: var(--ccx-channel-row-suspended-border);
  box-shadow: var(--ccx-channel-row-shadow);
}

.v-theme--dark .channel-row.is-tripped:hover {
  background: var(--ccx-channel-row-suspended-hover-bg);
  box-shadow: var(--ccx-channel-row-shadow-hover);
}

.channel-row.ghost {
  opacity: 0.6;
  background: rgba(var(--v-theme-primary), 0.15);
  border: 2px dashed rgb(var(--v-theme-primary));
  box-shadow: none;
}

.drag-handle {
  cursor: grab;
  display: flex;
  align-items: center;
  justify-content: center;
  width: 32px;
  height: 32px;
  transition: all 0.1s ease;
}

.drag-handle:hover {
  background: rgba(var(--v-theme-on-surface), 0.1);
}

.drag-handle:active {
  cursor: grabbing;
  background: rgba(var(--v-theme-primary), 0.2);
}

.priority-number {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  background: rgb(var(--v-theme-primary));
  color: white;
  font-size: 12px;
  font-weight: 700;
  border: 2px solid rgb(var(--v-theme-on-surface));
  text-transform: uppercase;
}

.status-badge-wrapper {
  display: flex;
  align-items: center;
  align-self: center;
}

.v-theme--dark .priority-number {
  border-color: rgba(255, 255, 255, 0.6);
}

.channel-name {
  display: flex;
  align-items: center;
  overflow: hidden;
  min-width: 0;
  gap: 4px;
  flex-wrap: wrap;
}

.channel-name > * {
  min-width: 0;
}

.channel-name .expand-icon {
  flex-shrink: 0;
}

.channel-name .font-weight-medium {
  font-size: 0.875rem;
  flex-shrink: 0;
}

/* Limit description text to at most two lines */
.channel-description {
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
  text-overflow: ellipsis;
  line-height: 1.4;
  max-height: calc(1.4em * 2);
  word-break: break-word;
}

.channel-name-link {
  cursor: pointer;
  transition: all 0.15s ease;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.channel-name-link:hover,
.channel-name-link:focus {
  color: rgb(var(--v-theme-primary));
  text-decoration: underline;
  outline: none;
}

.channel-name-link:focus-visible {
  outline: 2px solid rgb(var(--v-theme-primary));
  outline-offset: 2px;
  border-radius: 2px;
}

.channel-metrics {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: nowrap;
  white-space: nowrap;
  justify-content: flex-end;
  min-width: 168px;
}

.channel-latency {
  display: flex;
  align-items: center;
  min-width: 60px;
}

/* RPM/TPM display styles */
.channel-rpm-tpm {
  display: flex;
  flex-direction: column;
  align-items: flex-end;
  justify-content: center;
  min-width: 92px;
  margin-left: 12px;
  padding: 2px 6px;
  border-radius: 6px;
  background: rgba(var(--v-theme-surface-variant), 0.28);
}

.rpm-tpm-values {
  display: flex;
  align-items: baseline;
  justify-content: flex-end;
  gap: 4px;
  width: 100%;
  font-size: 15px;
  line-height: 1.1;
  font-weight: 800;
  color: rgba(var(--v-theme-on-surface), 0.82);
  font-variant-numeric: tabular-nums;
}

.rpm-tpm-values .rpm-value.has-data,
.rpm-tpm-values .tpm-value.has-data {
  color: rgb(var(--v-theme-primary));
}

.rpm-tpm-separator {
  color: rgba(var(--v-theme-on-surface), 0.55);
  font-weight: 700;
}

.rpm-tpm-labels {
  display: flex;
  align-items: center;
  justify-content: flex-end;
  gap: 3px;
  width: 100%;
  margin-top: 3px;
  font-size: 11px;
  font-weight: 600;
  color: rgba(var(--v-theme-on-surface), 0.68);
  text-transform: uppercase;
  letter-spacing: 0;
}

.channel-keys {
  display: flex;
  align-items: center;
}

.channel-keys .keys-chip {
  cursor: pointer;
  transition: all 0.1s ease;
}

.channel-keys .keys-chip:hover {
  background: rgba(var(--v-theme-primary), 0.1);
  border-color: rgb(var(--v-theme-primary));
  color: rgb(var(--v-theme-primary));
}

.channel-actions {
  display: flex;
  align-items: center;
  gap: 2px;
  justify-content: flex-end;
  min-width: 50px;
}

/* Standby resource pool styles */
.inactive-pool-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-bottom: 8px;
}

.inactive-pool {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(280px, 1fr));
  gap: 10px;
  background: rgb(var(--v-theme-surface));
  padding: 16px;
  border: 2px dashed rgb(var(--v-theme-on-surface));
}

.v-theme--dark .inactive-pool {
  background: rgb(var(--v-theme-surface));
  border-color: rgba(255, 255, 255, 0.5);
}

.inactive-channel-row {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
  padding: 10px 14px;
  background: rgb(var(--v-theme-surface));
  border: 2px solid rgb(var(--v-theme-on-surface));
  box-shadow: 3px 3px 0 0 rgb(var(--v-theme-on-surface));
  transition: all 0.1s ease;
}

.inactive-channel-row:hover {
  background: rgba(var(--v-theme-primary), 0.08);
  transform: translate(-1px, -1px);
  box-shadow: 4px 4px 0 0 rgb(var(--v-theme-on-surface));
}

.inactive-channel-row:active {
  transform: translate(2px, 2px);
  box-shadow: none;
}

.v-theme--dark .inactive-channel-row {
  background: rgb(var(--v-theme-surface));
  border-color: rgba(255, 255, 255, 0.6);
  box-shadow: 3px 3px 0 0 rgba(255, 255, 255, 0.6);
}

.v-theme--dark .inactive-channel-row:hover {
  background: rgba(var(--v-theme-primary), 0.12);
  box-shadow: 4px 4px 0 0 rgba(255, 255, 255, 0.6);
}

.inactive-channel-row .channel-info {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.inactive-channel-row .channel-info-main {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.inactive-channel-row .channel-info-desc {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  line-height: 1.3;
  max-width: 100%;
}

.inactive-channel-row .channel-actions {
  display: flex;
  align-items: center;
  gap: 4px;
}

/* Responsive adjustments */
@media (max-width: 1400px) {
  .channel-row-content {
    grid-template-columns:
      28px
      28px
      minmax(64px, auto)
      minmax(0, 1fr)
      minmax(152px, auto)
      minmax(88px, auto)
      minmax(60px, auto)
      minmax(64px, auto)
      auto;
    gap: 5px;
  }
  .channel-row {
    padding: 10px 10px;
  }
}

@media (max-width: 1200px) {
  .channel-row-content {
    grid-template-columns:
      26px
      26px
      minmax(60px, auto)
      minmax(0, 1fr)
      minmax(136px, auto)
      minmax(78px, auto)
      minmax(56px, auto)
      minmax(60px, auto)
      auto;
    gap: 4px;
  }
  .channel-row {
    padding: 8px 8px;
  }

  .rpm-tpm-values {
    font-size: 12px;
  }

  .rpm-tpm-labels {
    font-size: 10px;
    letter-spacing: 0;
  }
}

@media (max-width: 960px) {
  .channel-row-content {
    grid-template-columns:
      26px
      26px
      minmax(56px, auto)
      minmax(0, 1fr)
      minmax(124px, auto)
      minmax(72px, auto)
      minmax(52px, auto)
      minmax(56px, auto)
      auto;
    gap: 4px;
  }
  .channel-row {
    padding: 8px 6px;
  }
}

@media (max-width: 600px) {
  .mode-chip {
    display: none;
  }

  .channel-row-content {
    grid-template-columns: 28px 1fr 60px;
    gap: 8px;
  }
  .channel-row {
    padding: 10px;
    box-shadow: 3px 3px 0 0 rgb(var(--v-theme-on-surface));
  }

  .channel-metrics,
  .channel-latency,
  .channel-keys,
  .channel-rpm-tpm {
    display: none;
  }

  .v-theme--dark .channel-row {
    box-shadow: 3px 3px 0 0 rgba(255, 255, 255, 0.6);
  }

  .priority-number,
  .drag-handle {
    display: none;
  }

  .channel-name-link {
    max-width: 12ch;
    display: inline-block;
  }
}

/* Metrics display styles */
.metrics-display {
  cursor: help;
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
}

.metrics-chip {
  font-weight: 700;
  letter-spacing: 0;
}

.request-summary {
  font-size: 12px;
  font-weight: 700;
  color: rgba(var(--v-theme-on-surface), 0.88);
  white-space: nowrap;
}

/* Metrics tooltip styles */
.metrics-tooltip {
  font-size: 12px;
  line-height: 1.5;
  color: rgb(var(--v-theme-on-surface));
}

.metrics-tooltip-row {
  display: flex;
  justify-content: space-between;
  gap: 16px;
  padding: 2px 0;
}

.metrics-tooltip-row span:first-child {
  color: rgba(var(--v-theme-on-surface), 0.7);
}

.metrics-tooltip-row span:last-child {
  font-weight: 500;
  color: rgb(var(--v-theme-on-surface));
}
</style>
