<template>
  <v-card elevation="0" rounded="lg" class="channel-orchestration" variant="flat">
    <!--
      全局共享 gradient 定义（7 档成功率色带）
      所有渠道的 activity bar 通过 url(#ccx-act-g{0..6}) 引用，
      避免每个 bar 独立定义 gradient 导致 SVG 节点爆炸（30+ 渠道 × 150 bar = 4500+ 节点）
    -->
    <svg class="activity-gradient-defs" aria-hidden="true" width="0" height="0" style="position:absolute;">
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
        <v-tooltip :text="t('schedulerDiagnose.title')" location="bottom" content-class="ccx-tooltip">
          <template #activator="{ props: tooltipProps }">
            <v-btn icon size="small" variant="text" v-bind="tooltipProps" @click="showSchedulerDiagnoseDialog = true">
              <v-icon size="small">mdi-routes</v-icon>
            </v-btn>
          </template>
        </v-tooltip>
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
              :class="[
                getChannelRowClass(element),
                { 'has-open-menu': isChannelMenuOpen('active', element.index) },
              ]"
              @click="toggleChannelChart(element.index)"
            >
              <!-- SVG activity waveform bar chart background -->
              <!-- Gradient 定义在组件顶部一次性渲染（见 .activity-gradient-defs），这里只绘制 rect 并引用共享 gradient -->
              <svg class="activity-chart-bg" preserveAspectRatio="none" viewBox="0 0 150 100">
                <template v-for="(bar, i) in getActivityBars(element.index)" :key="i">
                  <rect
                    v-if="bar.v"
                    :x="bar.x"
                    :y="bar.y"
                    :width="bar.width"
                    :height="bar.height"
                    :fill="`url(#ccx-act-g${bar.g})`"
                    :rx="bar.radius"
                    :ry="bar.radius"
                    class="activity-bar"
                  />
                </template>
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
              <v-icon v-if="element.noVision" size="14" color="warning" class="ml-1">mdi-eye-off</v-icon>
              <span v-if="element.description" class="text-caption text-disabled ml-3 channel-description">{{ element.description }}</span>
              <!-- Expand icon -->
              <v-icon
                size="x-small"
                class="ml-auto expand-icon"
                :color="expandedChannelIndex === element.index ? 'primary' : 'grey-lighten-1'"
              >{{ expandedChannelIndex === element.index ? 'mdi-chevron-up' : 'mdi-chevron-down' }}</v-icon>
            </div>

            <!-- Metrics display -->
            <!--
              tooltip 懒挂载：仅 hover/focus 当前渠道时才渲染 <v-tooltip>，避免 100+ 渠道常驻 overlay
            -->
            <div class="channel-metrics" @click.stop>
              <template v-if="getChannelMetrics(element.index)">
                <div
                  class="d-flex align-center metrics-display"
                  tabindex="0"
                  @mouseenter="hoveredMetricsChannel = element.index"
                  @mouseleave="hoveredMetricsChannel === element.index && (hoveredMetricsChannel = null)"
                  @focusin="hoveredMetricsChannel = element.index"
                  @focusout="hoveredMetricsChannel === element.index && (hoveredMetricsChannel = null)"
                >
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
                    <v-chip
                      v-if="shouldShowCacheWriteWarning(get15mStats(element.index))"
                      size="x-small"
                      color="warning"
                      variant="tonal"
                      class="ml-1 metrics-chip cache-chip"
                    >
                      {{ t('orchestration.cacheWriteHigh') }}
                    </v-chip>
                  </template>
                  <span v-else class="text-caption text-medium-emphasis">--</span>
                  <v-tooltip
                    v-if="hoveredMetricsChannel === element.index"
                    :model-value="true"
                    activator="parent"
                    location="top"
                    :open-delay="150"
                    content-class="ccx-tooltip"
                  >
                    <div class="metrics-tooltip">
                      <div class="text-caption font-weight-bold mb-1">{{ t('orchestration.requestStats') }}</div>
                      <div class="metrics-tooltip-row">
                        <span>{{ t('orchestration.minutes15') }}:</span>
                        <span>{{ formatStats(get15mStats(element.index)) }}</span>
                      </div>
                      <div class="metrics-tooltip-row">
                        <span>{{ t('orchestration.firstByteLatency') }} · {{ t('orchestration.minutes15') }}:</span>
                        <span>{{ formatRequestLatency(get15mStats(element.index)) }}</span>
                      </div>
                      <div class="metrics-tooltip-row">
                        <span>{{ t('orchestration.firstContentLatency') }} · {{ t('orchestration.minutes15') }}:</span>
                        <span>{{ formatRequestLatency(get15mStats(element.index), 'firstContent') }}</span>
                      </div>
                      <div class="metrics-tooltip-row">
                        <span>{{ t('orchestration.hour1') }}:</span>
                        <span>{{ formatStats(get1hStats(element.index)) }}</span>
                      </div>
                      <div class="metrics-tooltip-row">
                        <span>{{ t('orchestration.firstByteLatency') }} · {{ t('orchestration.hour1') }}:</span>
                        <span>{{ formatRequestLatency(get1hStats(element.index)) }}</span>
                      </div>
                      <div class="metrics-tooltip-row">
                        <span>{{ t('orchestration.firstContentLatency') }} · {{ t('orchestration.hour1') }}:</span>
                        <span>{{ formatRequestLatency(get1hStats(element.index), 'firstContent') }}</span>
                      </div>
                      <div class="metrics-tooltip-row">
                        <span>{{ t('orchestration.hours6') }}:</span>
                        <span>{{ formatStats(get6hStats(element.index)) }}</span>
                      </div>
                      <div class="metrics-tooltip-row">
                        <span>{{ t('orchestration.firstByteLatency') }} · {{ t('orchestration.hours6') }}:</span>
                        <span>{{ formatRequestLatency(get6hStats(element.index)) }}</span>
                      </div>
                      <div class="metrics-tooltip-row">
                        <span>{{ t('orchestration.firstContentLatency') }} · {{ t('orchestration.hours6') }}:</span>
                        <span>{{ formatRequestLatency(get6hStats(element.index), 'firstContent') }}</span>
                      </div>
                      <div class="metrics-tooltip-row">
                        <span>{{ t('orchestration.hours24') }}:</span>
                        <span>{{ formatStats(get24hStats(element.index)) }}</span>
                      </div>
                      <div class="metrics-tooltip-row">
                        <span>{{ t('orchestration.firstByteLatency') }} · {{ t('orchestration.hours24') }}:</span>
                        <span>{{ formatRequestLatency(get24hStats(element.index)) }}</span>
                      </div>
                      <div class="metrics-tooltip-row">
                        <span>{{ t('orchestration.firstContentLatency') }} · {{ t('orchestration.hours24') }}:</span>
                        <span>{{ formatRequestLatency(get24hStats(element.index), 'firstContent') }}</span>
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
                </div>
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
              <button
                v-for="latency in realLatencySummaries(get15mStats(element.index))"
                :key="latency.kind"
                class="real-latency-summary"
                type="button"
                :disabled="!element.channelUid"
                :title="t(`orchestration.${latency.kind}LatencyTooltip`, { samples: latency.samples })"
                @click="openRequestHistory(element, '15m')"
              >
                <span>{{ t(`orchestration.${latency.kind}P95`) }}</span>
                <strong v-if="latency.samples >= 5">≈{{ Math.round(latency.p95) }}ms</strong>
                <strong v-else>{{ t('orchestration.latencyInsufficient') }}</strong>
              </button>
              <v-chip
                v-if="isLatencyValid(element)"
                size="x-small"
                :color="getLatencyColor(element.latency!)"
                variant="tonal"
                :title="t('orchestration.probeLatency')"
              >
                {{ element.latency }}ms
              </v-chip>
            </div>

            <!-- API key count -->
            <div class="channel-keys d-flex align-center ga-1" @click.stop>
              <v-chip size="x-small" variant="outlined" class="keys-chip" @click="$emit('edit', element)">
                <v-icon start size="x-small">mdi-key</v-icon>
                {{ availableChannelApiKeyCount(element) }}
              </v-chip>
              <v-tooltip v-if="pausedChannelApiKeyCount(element)" :text="t('orchestration.pausedKeys', { count: pausedChannelApiKeyCount(element) })" location="top" color="warning" content-class="ccx-tooltip">
                <template #activator="{ props: tip }">
                  <v-chip v-bind="tip" size="x-small" color="warning" variant="tonal" @click="$emit('edit', element)">
                    <v-icon start size="x-small">mdi-pause</v-icon>
                    {{ pausedChannelApiKeyCount(element) }}
                  </v-chip>
                </template>
              </v-tooltip>
              <v-tooltip v-if="disabledChannelApiKeyCount(element)" :text="t('orchestration.blacklistedKeys', { count: disabledChannelApiKeyCount(element) })" location="top" color="error" content-class="ccx-tooltip">
                <template #activator="{ props: tip }">
                  <v-chip v-bind="tip" size="x-small" color="error" variant="tonal" @click="$emit('edit', element)">
                    {{ disabledChannelApiKeyCount(element) }}
                  </v-chip>
                </template>
              </v-tooltip>
            </div>

            <!-- Action buttons -->
            <div class="channel-actions" @click.stop>
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
                v-else
                icon
                size="x-small"
                variant="text"
                color="warning"
                :title="t('orchestration.pause')"
                @click="setChannelStatus(element.index, 'suspended')"
              >
                <v-icon size="small">mdi-pause-circle</v-icon>
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

              <v-menu
                :model-value="isChannelMenuOpen('active', element.index)"
                location="bottom end"
                origin="top end"
                location-strategy="connected"
                scroll-strategy="reposition"
                :offset="6"
                @update:model-value="open => handleChannelMenuUpdate('active', element.index, open)"
              >
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
                  <v-list-item v-if="channelType !== 'images' && channelType !== 'vectors'" @click="$emit('testCapability', element.index)">
                    <template #prepend>
                      <v-icon size="small" color="success">mdi-test-tube</v-icon>
                    </template>
                    <v-list-item-title>{{ t('addChannel.testCapability') }}</v-list-item-title>
                  </v-list-item>
                  <v-list-item @click="copyChannelInfo(element)">
                    <template #prepend>
                      <v-icon size="small">mdi-content-copy</v-icon>
                    </template>
                    <v-list-item-title>{{ t('orchestration.copyConfig') }}</v-list-item-title>
                  </v-list-item>
                  <v-list-item v-if="!isInPromotion(element)" @click="setPromotion(element)">
                    <template #prepend>
                      <v-icon size="small" color="info">mdi-rocket-launch</v-icon>
                    </template>
                    <v-list-item-title>{{ t('orchestration.promotion') }}</v-list-item-title>
                  </v-list-item>
                  <v-list-item v-if="!isFirstActiveChannel(element)" :disabled="isSavingOrder" @click="moveChannelToTop(element.index)">
                    <template #prepend>
                      <v-icon size="small" color="primary">mdi-arrow-collapse-up</v-icon>
                    </template>
                    <v-list-item-title>{{ t('orchestration.moveTop') }}</v-list-item-title>
                  </v-list-item>
                  <v-list-item v-if="!isLastActiveChannel(element)" :disabled="isSavingOrder" @click="moveChannelToBottom(element.index)">
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
                  <v-list-item @click="setChannelStatus(element.index, 'disabled')">
                    <template #prepend>
                      <v-icon size="small" color="error">mdi-stop-circle</v-icon>
                    </template>
                    <v-list-item-title>{{ t('orchestration.moveToPool') }}</v-list-item-title>
                  </v-list-item>
                  <v-divider />
                  <v-list-item disabled density="compact">
                    <template #prepend>
                      <v-icon size="small">mdi-key</v-icon>
                    </template>
                    <v-list-item-title class="text-caption text-medium-emphasis">
                      {{ availableChannelApiKeyCount(element) }} {{ t('channelCard.configuredKeys') }}
                      <span v-if="pausedChannelApiKeyCount(element)"> · {{ pausedChannelApiKeyCount(element) }} {{ t('status.paused') }}</span>
                      <span v-if="disabledChannelApiKeyCount(element)"> · {{ disabledChannelApiKeyCount(element) }} {{ t('channelCard.disabledKeys') }}</span>
                    </v-list-item-title>
                  </v-list-item>
                  <v-list-item @click="handleDeleteChannel(element)">
                    <template #prepend>
                      <v-icon size="small" color="error">mdi-delete</v-icon>
                    </template>
                    <v-list-item-title>{{ t('orchestration.delete') }}</v-list-item-title>
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
        <div
          v-for="channel in filteredInactiveChannels"
          :key="channel.index"
          class="inactive-channel-row"
          :class="{ 'has-open-menu': isChannelMenuOpen('inactive', channel.index) }"
        >
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
              <v-icon v-if="channel.noVision" size="14" color="warning" class="ml-1">mdi-eye-off</v-icon>
            </div>
            <div v-if="channel.description" class="channel-info-desc text-caption text-disabled">
              {{ channel.description }}
            </div>
          </div>

          <!-- API key count -->
          <div class="channel-keys d-flex align-center ga-1">
            <v-chip size="x-small" variant="outlined" color="grey" class="keys-chip" @click="$emit('edit', channel)">
              <v-icon start size="x-small">mdi-key</v-icon>
              {{ availableChannelApiKeyCount(channel) }}
            </v-chip>
            <v-tooltip v-if="pausedChannelApiKeyCount(channel)" :text="t('orchestration.pausedKeys', { count: pausedChannelApiKeyCount(channel) })" location="top" color="warning" content-class="ccx-tooltip">
              <template #activator="{ props: tip }">
                <v-chip v-bind="tip" size="x-small" color="warning" variant="tonal" @click="$emit('edit', channel)">
                  <v-icon start size="x-small">mdi-pause</v-icon>
                  {{ pausedChannelApiKeyCount(channel) }}
                </v-chip>
              </template>
            </v-tooltip>
            <v-tooltip v-if="disabledChannelApiKeyCount(channel)" :text="t('orchestration.blacklistedKeys', { count: disabledChannelApiKeyCount(channel) })" location="top" color="error" content-class="ccx-tooltip">
              <template #activator="{ props: tip }">
                <v-chip v-bind="tip" size="x-small" color="error" variant="tonal" @click="$emit('edit', channel)">
                  {{ disabledChannelApiKeyCount(channel) }}
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

            <v-menu
              :model-value="isChannelMenuOpen('inactive', channel.index)"
              location="bottom end"
              origin="top end"
              location-strategy="connected"
              scroll-strategy="reposition"
              :offset="6"
              @update:model-value="open => handleChannelMenuUpdate('inactive', channel.index, open)"
            >
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
                <v-list-item v-if="channelType !== 'images' && channelType !== 'vectors'" @click="$emit('testCapability', channel.index)">
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
      :history-available="!!logsChannel?.channelUid"
      @retest-model="handleRetestModel"
      @request-history="openLogsHistory"
    />
    <SchedulerDiagnoseDialog
      v-model="showSchedulerDiagnoseDialog"
      :channel-type="channelType"
    />
  </v-card>
</template>

<script setup lang="ts">
import { ref, computed, watch, onMounted, onUnmounted, defineAsyncComponent, nextTick } from 'vue'
import draggable from 'vuedraggable'
import { api, type Channel, type ChannelMetrics, type ChannelStatus, type TimeWindowStats, type ChannelRecentActivity, type ChannelLogRetestRequest, type SchedulerStatsResponse } from '../services/api'
import { getChannelTypeApi } from '../utils/channelTypeApi'
import type { RoutingHistoryDuration, RoutingHistorySelection } from '../services/routing-types'
import { useI18n } from '../i18n'
import { useGlobalTick } from '../composables/useGlobalTick'
import { useChannelActivity } from '../composables/useChannelActivity'
import ChannelStatusBadge from './ChannelStatusBadge.vue'
// Lazy-load chart components to reduce initial JS bundle size
const KeyTrendChart = defineAsyncComponent(() => import('./KeyTrendChart.vue'))
import ChannelLogsDialog from './ChannelLogsDialog.vue'
import SchedulerDiagnoseDialog from './SchedulerDiagnoseDialog.vue'
import { availableChannelApiKeyCount, disabledChannelApiKeyCount, pausedChannelApiKeyCount } from '../utils/channelApiKeys'

const props = defineProps<{
  channels: Channel[]
  currentChannelIndex: number
  channelType: 'messages' | 'chat' | 'responses' | 'gemini' | 'images' | 'vectors'
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
  (_e: 'retestModel', _request: ChannelLogRetestRequest): void
  (_e: 'refresh'): void
  (_e: 'error', _message: string): void
  (_e: 'success', _message: string): void
  (_e: 'requestHistory', _selection: RoutingHistorySelection): void
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
const showSchedulerDiagnoseDialog = ref(false)

// Channel logs dialog state
const showLogsDialog = ref(false)
const logsChannelIndex = ref(0)
const logsChannelName = ref('')
const logsChannel = computed(() => props.channels.find(ch => ch.index === logsChannelIndex.value))
const openLogsDialog = (ch: Channel) => {
  logsChannelIndex.value = ch.index
  logsChannelName.value = ch.name
  showLogsDialog.value = true
}
const openRequestHistory = (ch: Channel, duration: RoutingHistoryDuration) => {
  if (!ch.channelUid) return
  emit('requestHistory', { apiType: props.channelType, channelUid: ch.channelUid, channelName: ch.name, duration })
}
const openLogsHistory = () => {
  if (!logsChannel.value?.channelUid) return
  showLogsDialog.value = false
  openRequestHistory(logsChannel.value, '24h')
}
const handleRetestModel = (request: ChannelLogRetestRequest) => {
  showLogsDialog.value = false
  emit('retestModel', request)
}

// Validity period for latency test results (5 minutes)
const LATENCY_VALID_DURATION = 5 * 60 * 1000
// Timestamp used to trigger reactive updates
const currentTime = ref(Date.now())

// Timestamp used to trigger activity view updates (updated every 2 seconds)
const activityUpdateTick = ref(0)

// Chart expansion state
const expandedChannelIndex = ref<number | null>(null)

// tooltip 懒挂载：记录当前 hover/focus 的渠道，避免 100+ 渠道每行常驻 <v-tooltip> overlay 实例
const hoveredMetricsChannel = ref<number | null>(null)

// Channel config copy state
const copiedChannelIndex = ref<number | null>(null)
let copyTimeoutId: ReturnType<typeof setTimeout> | null = null

type ChannelMenuScope = 'active' | 'inactive'
type ChannelMenuKey = `${ChannelMenuScope}:${number}`
const openChannelMenuKey = ref<ChannelMenuKey | null>(null)
let channelMenuRepositionTimer: ReturnType<typeof setTimeout> | null = null

const getChannelMenuKey = (scope: ChannelMenuScope, channelIndex: number): ChannelMenuKey =>
  `${scope}:${channelIndex}`

const isChannelMenuOpen = (scope: ChannelMenuScope, channelIndex: number): boolean =>
  openChannelMenuKey.value === getChannelMenuKey(scope, channelIndex)

const dispatchOverlayResize = () => {
  window.dispatchEvent(new Event('resize'))
}

const scheduleChannelMenuReposition = () => {
  nextTick(() => {
    window.requestAnimationFrame(dispatchOverlayResize)

    if (channelMenuRepositionTimer) {
      clearTimeout(channelMenuRepositionTimer)
    }

    channelMenuRepositionTimer = setTimeout(() => {
      dispatchOverlayResize()
      channelMenuRepositionTimer = null
    }, 50)
  })
}

const handleChannelMenuUpdate = (
  scope: ChannelMenuScope,
  channelIndex: number,
  open: boolean,
) => {
  const key = getChannelMenuKey(scope, channelIndex)

  if (open) {
    openChannelMenuKey.value = key
    scheduleChannelMenuReposition()
    return
  }

  if (openChannelMenuKey.value === key) {
    openChannelMenuKey.value = null
  }
}

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

// 首次渲染时记录内置顺序，用作缺省优先级兜底
const initialBuiltInOrder = computed(() => props.channels.map(ch => ch.index))
const lastKnownActiveOrder = ref<number[]>([])
const lastKnownInactiveOrder = ref<number[]>([])

// 按用户排序/后端 priority 稳定排序；有无 key 只作为缺省顺序的兜底，不覆盖用户排序
const buildChannelOrder = (
  source: Channel[],
  fallbackOrder: number[]
): Channel[] => {
  const fallbackRank = new Map<number, number>()
  fallbackOrder.forEach((idx, rank) => fallbackRank.set(idx, rank))

  const originalRank = new Map<number, number>()
  initialBuiltInOrder.value.forEach((idx, rank) => originalRank.set(idx, rank))

  const hasKey = (ch: Channel) =>
    Array.isArray(ch.apiKeys) && ch.apiKeys.length > 0

  const getRank = (ch: Channel): number =>
    ch.priority ?? fallbackRank.get(ch.index) ?? originalRank.get(ch.index) ?? ch.index

  return [...source].sort((a, b) => {
    const rankDiff = getRank(a) - getRank(b)
    if (rankDiff !== 0) return rankDiff

    // 只有在优先级完全相同时，才把已配置 key 的渠道排前，避免覆盖用户拖拽/置顶排序
    const keyDiff = Number(hasKey(b)) - Number(hasKey(a))
    if (keyDiff !== 0) return keyDiff

    return a.index - b.index
  })
}

const isSameIndexOrder = (current: number[], next: number[]) => (
  current.length === next.length && current.every((index, position) => index === next[position])
)

// Computed: inactive channels - disabled status only
const inactiveChannels = computed(() => {
  const inactive = props.channels.filter(ch => ch.status === 'disabled')
  return buildChannelOrder(inactive, lastKnownInactiveOrder.value)
})

// Computed: inactive channels after search filtering
const filteredInactiveChannels = computed(() => {
  return inactiveChannels.value.filter(matchesSearch)
})

watch(inactiveChannels, (channels) => {
  const nextOrder = channels.map(ch => ch.index)
  if (!isSameIndexOrder(lastKnownInactiveOrder.value, nextOrder)) {
    lastKnownInactiveOrder.value = nextOrder
  }
}, { immediate: true })

// Computed: whether multi-channel mode is enabled
// Multi-channel mode detection logic:
// 1. Only one enabled channel → single-channel mode
// 2. One active channel + several suspended channels → single-channel mode
// 3. Multiple active channels → multi-channel mode
const isMultiChannelMode = computed(() => {
  const activeCount = props.channels.filter(
    ch => ch.status === 'active' || ch.status === undefined || ch.status === ''
  ).length
  return activeCount > 1
})

// 初始化渠道编排列表 - 活跃与挂起渠道共同参与 failover 序列
// 优化策略：仅在结构变化时重建数组，避免频繁重构导致子组件被销毁重建
const initActiveChannels = () => {
  const filteredActive = props.channels.filter(ch => ch.status !== 'disabled')
  const newActive = buildChannelOrder(filteredActive, lastKnownActiveOrder.value)
  lastKnownActiveOrder.value = newActive.map(ch => ch.index)

  // 通过索引列表比较，判断是否需要整体重建
  const currentIndexes = activeChannels.value.map(ch => ch.index).join(',')
  const newIndexes = newActive.map(ch => ch.index).join(',')

  if (currentIndexes !== newIndexes) {
    // 结构发生变更（新增/删除/重新排序），需要重建数组
    activeChannels.value = [...newActive]
  } else {
    // 结构未变，仅更新已有对象的属性（保持引用稳定）
    activeChannels.value.forEach((ch, i) => {
      Object.assign(ch, newActive[i])
    })
  }
}

// Watch channel changes - 监听引用变化即可（store refresh 时 channels 是全新数组引用）
// 去掉 deep: true，避免深度遍历 apiKeys/modelMapping 等嵌套结构的性能开销
watch(() => props.channels, initActiveChannels, { immediate: true })

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

const CACHE_WRITE_WARNING_MIN_REQUESTS = 5
const CACHE_WRITE_WARNING_MIN_TOKENS = 100000
const CACHE_WRITE_WARNING_RATIO = 0.2

const shouldShowCacheWriteWarning = (stats?: TimeWindowStats): boolean => {
  if (!stats || (stats.requestCount ?? 0) < CACHE_WRITE_WARNING_MIN_REQUESTS) return false
  const inputTokens = stats.inputTokens ?? 0
  const cacheReadTokens = stats.cacheReadTokens ?? 0
  const cacheCreationTokens = stats.cacheCreationTokens ?? 0
  const denom = inputTokens + cacheReadTokens
  if (denom <= 0 || cacheCreationTokens < CACHE_WRITE_WARNING_MIN_TOKENS) return false
  return (cacheCreationTokens / denom) >= CACHE_WRITE_WARNING_RATIO
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

type RequestLatencyKind = 'firstByte' | 'firstContent'

const requestLatencyValues = (stats: TimeWindowStats | undefined, kind: RequestLatencyKind) => ({
  kind,
  samples: stats?.[`${kind}LatencySamples`] ?? 0,
  p50: stats?.[`${kind}LatencyP50Ms`] ?? 0,
  p95: stats?.[`${kind}LatencyP95Ms`] ?? 0
})

const realLatencySummaries = (stats?: TimeWindowStats) =>
  (['firstByte', 'firstContent'] as const)
    .map(kind => requestLatencyValues(stats, kind))
    .filter(latency => latency.samples > 0)

const formatRequestLatency = (stats?: TimeWindowStats, kind: RequestLatencyKind = 'firstByte'): string => {
  const { samples, p50, p95 } = requestLatencyValues(stats, kind)
  if (!samples) return '--'
  if (samples < 5) return t('orchestration.latencyInsufficientStats', { samples })
  return t('orchestration.firstByteLatencyStats', {
    p50: Math.round(p50),
    p95: Math.round(p95),
    samples
  })
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
  return `${t('orchestration.hitRate')} ${hitRate.toFixed(0)}% · ${t('orchestration.read')} ${formatTokens(cacheReadTokens)} · ${t('orchestration.write')} ${formatTokens(cacheCreationTokens)}`
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

const {
  activityMap,
  getChannelActivity,
  getActivityBars,
  getActivityPath,
  _getActivityAreaPath,
  _getActivityGradient,
  formatRPM,
  formatTPM,
  hasActivityData,
} = useChannelActivity(recentActivity, activityUpdateTick)

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

// 同步 lastKnownActiveOrder 为当前 activeChannels 的顺序
// 用于在用户主动排序（置顶/置底/拖拽）后，防止自动刷新用旧顺序覆盖
const syncActiveOrder = () => {
  lastKnownActiveOrder.value = activeChannels.value.map(ch => ch.index)
}

// Drag change event - auto-save order
const onDragChange = () => {
  syncActiveOrder()
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

// Whether the channel is the first/last in the full active sequence.
// 基于完整活跃列表的 index 值判断，避免搜索过滤（v-show）后位置变量 index 失真。
const isFirstActiveChannel = (channel: Channel): boolean =>
  activeChannels.value[0]?.index === channel.index
const isLastActiveChannel = (channel: Channel): boolean =>
  activeChannels.value[activeChannels.value.length - 1]?.index === channel.index

// Move channel to top
const moveChannelToTop = async (channelIndex: number) => {
  if (isSavingOrder.value) return
  const idx = activeChannels.value.findIndex(ch => ch.index === channelIndex)
  if (idx <= 0) return

  const [channel] = activeChannels.value.splice(idx, 1)
  activeChannels.value.unshift(channel)
  syncActiveOrder()
  await saveOrder()
}

// Move channel to bottom
const moveChannelToBottom = async (channelIndex: number) => {
  if (isSavingOrder.value) return
  const idx = activeChannels.value.findIndex(ch => ch.index === channelIndex)
  if (idx < 0 || idx >= activeChannels.value.length - 1) return

  const [channel] = activeChannels.value.splice(idx, 1)
  activeChannels.value.push(channel)
  syncActiveOrder()
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
  return channel.status === 'suspended' || channelMetrics?.circuitState === 'open'
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

// Handle channel deletion
const handleDeleteChannel = (channel: Channel) => {
  emit('delete', channel.index)
}

// Load metrics and start the latency expiry check timer when the component mounts
// 全局 tick 订阅（visibility hidden 时自动暂停）
const latencyTick = useGlobalTick(30000, 'ChannelOrch-latency')
const activityTick = useGlobalTick(2000, 'ChannelOrch-activity')

onMounted(() => {
  refreshMetrics()
  latencyTick.onTick(() => { currentTime.value = Date.now() })
  activityTick.onTick(() => { activityUpdateTick.value++ })
})

onUnmounted(() => {
  if (copyTimeoutId) {
    clearTimeout(copyTimeoutId)
    copyTimeoutId = null
  }
  if (channelMenuRepositionTimer) {
    clearTimeout(channelMenuRepositionTimer)
    channelMenuRepositionTimer = null
  }
})

// Expose methods to the parent component
defineExpose({
  refreshMetrics
})
</script>

<style scoped src="./channel-orchestration/channel-orchestration.css"></style>
