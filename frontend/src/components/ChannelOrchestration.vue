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
        :item-key="getChannelUiKey"
        :handle="isSearchActive || !canReorderList ? '.no-drag' : '.drag-handle'"
        ghost-class="ghost"
        class="channel-list"
        :disabled="isSearchActive || !canReorderList"
        @change="onDragChange"
      >
        <template #item="{ element, index }">
          <div v-show="matchesSearch(element)" class="channel-item-wrapper">
            <div
              class="channel-row"
              :class="[
                getChannelRowClass(element),
                { 'has-open-menu': isChannelMenuOpen('active', element) },
              ]"
              @click="toggleChannelChart(element)"
            >
              <!-- SVG activity waveform bar chart background -->
              <!-- Gradient 定义在组件顶部一次性渲染（见 .activity-gradient-defs），这里只绘制 rect 并引用共享 gradient -->
              <svg class="activity-chart-bg" preserveAspectRatio="none" viewBox="0 0 150 100">
                <template v-for="(bar, i) in getActivityBars(getRouteIndex(element), getRouteKind(element))" :key="i">
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
              <ChannelStatusBadge
                :status="element.status || 'active'"
                :metrics="getChannelMetrics(element)"
                :tripped="hasNoUsableChannelApiKeys(element)"
              />
              <!-- Health badge (§8.2) -->
              <ChannelHealthBadge :health="getChannelHealth(element) ?? null" />
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
              >{{ getChannelDisplayName(element) }}</span>
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
              <!-- Official website links; plan channels may expose more than one console. -->
              <v-tooltip
                v-for="link in getChannelWebsiteLinks(element)"
                :key="`${link.kind}:${link.url}`"
                :text="websiteLinkLabel(link.kind)"
                location="top"
                :open-delay="150"
                content-class="ccx-tooltip"
              >
                <template #activator="{ props: tooltipProps }">
                  <v-btn
                    v-bind="tooltipProps"
                    :href="link.url"
                    target="_blank"
                    rel="noopener noreferrer"
                    icon
                    size="x-small"
                    variant="text"
                    color="primary"
                    class="ml-1"
                    :aria-label="websiteLinkLabel(link.kind)"
                    @click.stop
                  >
                    <v-icon size="14">{{ websiteLinkIcon(link.kind) }}</v-icon>
                  </v-btn>
                </template>
              </v-tooltip>
              <v-chip
                v-for="capsule in getProtocolCapsules(element)"
                :key="`${capsule.kind}-${capsule.index}`"
                size="x-small"
                :color="getProtocolCapsuleColor(capsule.kind)"
                variant="tonal"
                density="comfortable"
                rounded="pill"
                class="ml-1 protocol-capsule"
              >
                {{ capsule.label }}
              </v-chip>
              <v-icon v-if="element.noVision" size="14" color="warning" class="ml-1">mdi-eye-off</v-icon>
              <!-- Origin / pool tags (§8.2 标签系统) - only rendered when health data includes originTier/poolTag -->
              <v-chip
                v-for="tag in getOriginTags(element)"
                :key="tag.label"
                :color="tag.color"
                size="x-small"
                variant="tonal"
                density="comfortable"
                rounded="pill"
                class="ml-1 origin-tag-chip-orch"
              >
                <v-icon start size="11">{{ tag.icon }}</v-icon>
                {{ tag.label }}
              </v-chip>
              <!-- 用户自定义标签 -->
              <v-chip
                v-for="utag in element.tags"
                :key="'user-' + utag"
                color="teal"
                size="x-small"
                variant="outlined"
                density="comfortable"
                rounded="pill"
                class="ml-1 user-tag-chip-orch"
              >
                <v-icon start size="11">mdi-tag</v-icon>
                {{ utag }}
              </v-chip>
              <span v-if="element.description" class="text-caption text-disabled ml-3 channel-description">{{ element.description }}</span>
              <!-- Expand icon -->
              <v-icon
                size="x-small"
                class="ml-auto expand-icon"
                :color="expandedChannelKey === getChannelUiKey(element) ? 'primary' : 'grey-lighten-1'"
              >{{ expandedChannelKey === getChannelUiKey(element) ? 'mdi-chevron-up' : 'mdi-chevron-down' }}</v-icon>
            </div>

            <!-- Metrics display -->
            <!--
              tooltip 懒挂载：仅 hover/focus 当前渠道时才渲染 <v-tooltip>，避免 100+ 渠道常驻 overlay
            -->
            <div class="channel-metrics" @click.stop>
              <template v-if="getChannelMetrics(element)">
                <div
                  class="d-flex align-center metrics-display"
                  tabindex="0"
                  @mouseenter="hoveredMetricsChannel = getChannelUiKey(element)"
                  @mouseleave="hoveredMetricsChannel === getChannelUiKey(element) && (hoveredMetricsChannel = null)"
                  @focusin="hoveredMetricsChannel = getChannelUiKey(element)"
                  @focusout="hoveredMetricsChannel === getChannelUiKey(element) && (hoveredMetricsChannel = null)"
                >
                  <!-- Show success rate when there are requests in the last 15 minutes; otherwise show -- -->
                  <template v-if="get15mStats(element)?.requestCount">
                    <v-chip
                      size="x-small"
                      :color="getSuccessRateColor(get15mStats(element)?.successRate)"
                      variant="tonal"
                      class="metrics-chip success-chip"
                    >
                      {{ get15mStats(element)?.successRate?.toFixed(0) }}%
                    </v-chip>
                    <span class="request-summary ml-2 mr-1">
                      {{ get15mStats(element)?.requestCount }} {{ t('orchestration.requests') }}
                    </span>
                    <v-chip
                      v-if="shouldShowCacheHitRate(get15mStats(element))"
                      size="x-small"
                      :color="getCacheHitRateColor(get15mStats(element)?.cacheHitRate)"
                      variant="tonal"
                      class="ml-1 metrics-chip cache-chip"
                    >
                      {{ t('orchestration.cache') }} {{ get15mStats(element)?.cacheHitRate?.toFixed(0) }}%
                    </v-chip>
                    <v-chip
                      v-if="shouldShowCacheWriteWarning(get15mStats(element))"
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
                    v-if="hoveredMetricsChannel === getChannelUiKey(element)"
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
                        <span>{{ formatStats(get15mStats(element)) }}</span>
                      </div>
                      <div class="metrics-tooltip-row">
                        <span>{{ t('orchestration.hour1') }}:</span>
                        <span>{{ formatStats(get1hStats(element)) }}</span>
                      </div>
                      <div class="metrics-tooltip-row">
                        <span>{{ t('orchestration.hours6') }}:</span>
                        <span>{{ formatStats(get6hStats(element)) }}</span>
                      </div>
                      <div class="metrics-tooltip-row">
                        <span>{{ t('orchestration.hours24') }}:</span>
                        <span>{{ formatStats(get24hStats(element)) }}</span>
                      </div>

                      <div class="text-caption font-weight-bold mt-2 mb-1">{{ t('orchestration.cacheStats') }}</div>
                      <div class="metrics-tooltip-row">
                        <span>{{ t('orchestration.minutes15') }}:</span>
                        <span>{{ formatCacheStats(get15mStats(element)) }}</span>
                      </div>
                      <div class="metrics-tooltip-row">
                        <span>{{ t('orchestration.hour1') }}:</span>
                        <span>{{ formatCacheStats(get1hStats(element)) }}</span>
                      </div>
                      <div class="metrics-tooltip-row">
                        <span>{{ t('orchestration.hours6') }}:</span>
                        <span>{{ formatCacheStats(get6hStats(element)) }}</span>
                      </div>
                      <div class="metrics-tooltip-row">
                        <span>{{ t('orchestration.hours24') }}:</span>
                        <span>{{ formatCacheStats(get24hStats(element)) }}</span>
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
                <span class="rpm-value" :class="{ 'has-data': hasActivityData(getRouteIndex(element), getRouteKind(element)) }">{{ formatRPM(getRouteIndex(element), getRouteKind(element)) }}</span>
                <span class="rpm-tpm-separator">/</span>
                <span class="tpm-value" :class="{ 'has-data': hasActivityData(getRouteIndex(element), getRouteKind(element)) }">{{ formatTPM(getRouteIndex(element), getRouteKind(element)) }}</span>
              </div>
              <div class="rpm-tpm-labels">
                <span>RPM</span>
                <span>/</span>
                <span>TPM</span>
              </div>
            </div>

            <!-- Latency display -->
            <div class="channel-latency" @click.stop>
              <v-tooltip
                v-if="isLongLatencyChannel(element)"
                location="top"
                content-class="ccx-tooltip"
              >
                <template #activator="{ props: tooltipProps }">
                  <v-chip
                    v-bind="tooltipProps"
                    size="x-small"
                    color="error"
                    variant="tonal"
                  >
                    <v-icon start size="12">mdi-timer-alert-outline</v-icon>
                    {{ formatObservedLatency(getChannelHealth(element)?.p95ConnectLatencyMs) }}
                  </v-chip>
                </template>
                {{ t('healthCenter.detail.connectP95') }}:
                {{ formatObservedLatency(getChannelHealth(element)?.p95ConnectLatencyMs) }}
                (n={{ getChannelHealth(element)?.connectSampleCount ?? 0 }})
              </v-tooltip>
            </div>

            <!-- API key count -->
            <div class="channel-keys d-flex align-center ga-1" @click.stop>
              <v-chip size="x-small" variant="outlined" class="keys-chip" @click="$emit('edit', element)">
                <v-icon start size="x-small">mdi-key</v-icon>
                {{ availableChannelApiKeyCount(element) }}
              </v-chip>
              <v-tooltip v-if="disabledChannelApiKeyCount(element)" :text="t('orchestration.blacklistedKeys', { count: disabledChannelApiKeyCount(element) })" location="top" color="warning" content-class="ccx-tooltip">
                <template #activator="{ props: tip }">
                  <v-chip v-bind="tip" size="x-small" color="warning" variant="tonal" @click="$emit('edit', element)">
                    {{ disabledChannelApiKeyCount(element) }}
                  </v-chip>
                </template>
              </v-tooltip>
            </div>

            <!-- Action buttons -->
            <div class="channel-actions" @click.stop>
              <v-btn
                v-if="isRecoverableChannel(element)"
                icon
                size="x-small"
                variant="text"
                color="warning"
                :title="t('orchestration.resume')"
                @click="resumeChannel(element)"
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
                @click="setChannelStatus(element, 'suspended')"
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
                :model-value="isChannelMenuOpen('active', element)"
                location="bottom end"
                origin="top end"
                location-strategy="connected"
                scroll-strategy="reposition"
                :offset="6"
                @update:model-value="open => handleChannelMenuUpdate('active', element, open)"
              >
                <template #activator="{ props: menuProps }">
                  <v-btn
                    icon
                    size="x-small"
                    :variant="copiedChannelKey === getChannelUiKey(element) ? 'flat' : 'text'"
                    :color="copiedChannelKey === getChannelUiKey(element) ? 'success' : ''"
                    v-bind="menuProps"
                  >
                    <v-icon size="small">
                      {{ copiedChannelKey === getChannelUiKey(element) ? 'mdi-check-bold' : 'mdi-dots-vertical' }}
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
                  <v-list-item @click="$emit('trial', element)">
                    <template #prepend>
                      <v-icon size="small" color="deep-purple">mdi-flask-outline</v-icon>
                    </template>
                    <v-list-item-title>{{ t('orchestration.trial') }}</v-list-item-title>
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
                  <v-list-item v-if="!isFirstActiveChannel(element)" :disabled="isSavingOrder || !canReorderList" @click="moveChannelToTop(element)">
                    <template #prepend>
                      <v-icon size="small" color="primary">mdi-arrow-collapse-up</v-icon>
                    </template>
                    <v-list-item-title>{{ t('orchestration.moveTop') }}</v-list-item-title>
                  </v-list-item>
                  <v-list-item v-if="!isLastActiveChannel(element)" :disabled="isSavingOrder || !canReorderList" @click="moveChannelToBottom(element)">
                    <template #prepend>
                      <v-icon size="small" color="primary">mdi-arrow-collapse-down</v-icon>
                    </template>
                    <v-list-item-title>{{ t('orchestration.moveBottom') }}</v-list-item-title>
                  </v-list-item>
                  <v-divider />
                  <v-list-item v-if="isRecoverableChannel(element)" @click="resumeChannel(element)">
                    <template #prepend>
                      <v-icon size="small" color="success">mdi-play-circle</v-icon>
                    </template>
                    <v-list-item-title>{{ t('orchestration.resumeReset') }}</v-list-item-title>
                  </v-list-item>
                  <v-list-item @click="setChannelStatus(element, 'disabled')">
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
                      <span v-if="disabledChannelApiKeyCount(element)"> · {{ disabledChannelApiKeyCount(element) }} {{ t('channelCard.disabledKeys') }}</span>
                    </v-list-item-title>
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
            <div v-if="expandedChannelKey === getChannelUiKey(element)" class="channel-chart-wrapper">
              <KeyTrendChart
                :key="`chart-${getRouteKind(element)}-${getRouteIndex(element)}`"
                :channel-id="getRouteIndex(element)"
                :channel-type="getRouteKind(element)"
                @close="expandedChannelKey = null"
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
          :key="getChannelUiKey(channel)"
          class="inactive-channel-row"
          :class="{ 'has-open-menu': isChannelMenuOpen('inactive', channel) }"
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
              >{{ getChannelDisplayName(channel) }}</span>
              <v-chip
                v-for="capsule in getProtocolCapsules(channel)"
                :key="`${capsule.kind}-${capsule.index}`"
                size="x-small"
                :color="getProtocolCapsuleColor(capsule.kind)"
                variant="tonal"
                density="comfortable"
                rounded="pill"
                class="ml-1 protocol-capsule"
              >
                {{ capsule.label }}
              </v-chip>
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
            <v-tooltip v-if="disabledChannelApiKeyCount(channel)" :text="t('orchestration.blacklistedKeys', { count: disabledChannelApiKeyCount(channel) })" location="top" color="warning" content-class="ccx-tooltip">
              <template #activator="{ props: tip }">
                <v-chip v-bind="tip" size="x-small" color="warning" variant="tonal" @click="$emit('edit', channel)">
                  {{ disabledChannelApiKeyCount(channel) }}
                </v-chip>
              </template>
            </v-tooltip>
          </div>

          <!-- Action buttons -->
          <div class="channel-actions">
            <v-btn size="small" color="success" variant="tonal" @click="enableChannel(channel)">
              <v-icon start size="small">mdi-play-circle</v-icon>
              {{ t('orchestration.enable') }}
            </v-btn>

            <v-menu
              :model-value="isChannelMenuOpen('inactive', channel)"
              location="bottom end"
              origin="top end"
              location-strategy="connected"
              scroll-strategy="reposition"
              :offset="6"
              @update:model-value="open => handleChannelMenuUpdate('inactive', channel, open)"
            >
              <template #activator="{ props: menuProps }">
                <v-btn
                  icon
                  size="x-small"
                  :variant="copiedChannelKey === getChannelUiKey(channel) ? 'flat' : 'text'"
                  :color="copiedChannelKey === getChannelUiKey(channel) ? 'success' : ''"
                  v-bind="menuProps"
                >
                  <v-icon size="small">
                    {{ copiedChannelKey === getChannelUiKey(channel) ? 'mdi-check-bold' : 'mdi-dots-vertical' }}
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
                <v-list-item @click="$emit('trial', channel)">
                  <template #prepend>
                    <v-icon size="small" color="deep-purple">mdi-flask-outline</v-icon>
                  </template>
                  <v-list-item-title>{{ t('orchestration.trial') }}</v-list-item-title>
                </v-list-item>
                <v-list-item @click="copyChannelInfo(channel)">
                  <template #prepend>
                    <v-icon size="small">mdi-content-copy</v-icon>
                  </template>
                  <v-list-item-title>{{ t('orchestration.copyConfig') }}</v-list-item-title>
                </v-list-item>
                <v-divider />
                <v-list-item @click="enableChannel(channel)">
                  <template #prepend>
                    <v-icon size="small" color="success">mdi-play-circle</v-icon>
                  </template>
                  <v-list-item-title>{{ t('orchestration.enable') }}</v-list-item-title>
                </v-list-item>
                <v-list-item @click="$emit('delete', channel)">
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
      :channel-type="logsChannelType"
      :protocol-routes="logsProtocolRoutes"
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
import { api, type Channel, type ChannelKind, type ChannelMetrics, type ChannelProtocolRoute, type ChannelStatus, type TimeWindowStats, type ChannelRecentActivity, type SchedulerStatsResponse } from '../services/api'
import { getChannelTypeApi } from '../utils/channelTypeApi'
import { isLlmChannelKind } from '../utils/unifiedChannels'
import { useI18n } from '../i18n'
import { useGlobalTick } from '../composables/useGlobalTick'
import { useChannelActivity } from '../composables/useChannelActivity'
import ChannelStatusBadge from './ChannelStatusBadge.vue'
import ChannelHealthBadge from './ChannelHealthBadge.vue'
import { isManagedProviderChannel, isOfficialProviderChannel, providerDisplayName } from '../utils/providerDisplay'
import { availableChannelApiKeyCount, disabledChannelApiKeyCount, hasNoUsableChannelApiKeys, hasOnlyDisabledChannelApiKeys } from '../utils/channelApiKeys'
import { getChannelWebsiteLinks, type ChannelWebsiteKind } from '../utils/channelWebsite'
import type { ChannelHealthItem } from '../services/api-types'
// Lazy-load chart components to reduce initial JS bundle size
const KeyTrendChart = defineAsyncComponent(() => import('./KeyTrendChart.vue'))
import ChannelLogsDialog from './ChannelLogsDialog.vue'
import SchedulerDiagnoseDialog from './SchedulerDiagnoseDialog.vue'

const props = defineProps<{
  channels: Channel[]
  currentChannelIndex: number
  channelType: ChannelKind
  // Optional: metrics and stats passed from the parent component (when using the dashboard API)
  dashboardMetrics?: ChannelMetrics[]
  dashboardStats?: SchedulerStatsResponse
  // Optional: realtime activity data passed from the parent component
  dashboardRecentActivity?: ChannelRecentActivity[]
  // Optional: stable channel identity → health data mapping (§8.2 badge integration)
  healthMap?: Map<string, ChannelHealthItem>
}>()

const emit = defineEmits<{
  (_e: 'edit', _channel: Channel): void
  (_e: 'delete', _channel: Channel): void
  (_e: 'trial', _channel: Channel): void
  (_e: 'refresh'): void
  (_e: 'error', _message: string): void
  (_e: 'success', _message: string): void
}>()
const { t } = useI18n()
const getRouteKind = (channel?: Channel): ChannelKind => channel?.routeKind ?? props.channelType
const getRouteIndex = (channel: Channel): number => channel.routeIndex ?? channel.index
const getChannelUiKey = (channel: Channel): string => channel.displayKey ?? `${getRouteKind(channel)}:${getRouteIndex(channel)}`
const getCurrentChannelTypeApi = (channel?: Channel) => getChannelTypeApi(api, getRouteKind(channel))

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
    channel.baseUrl?.toLowerCase().includes(q) ||
    channel.tags?.some(t => t.toLowerCase().includes(q)) ||
    false
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
const logsChannelType = ref<ChannelKind>(props.channelType)
const logsProtocolRoutes = ref<ChannelProtocolRoute[]>([])
const openLogsDialog = (ch: Channel) => {
  logsChannelIndex.value = getRouteIndex(ch)
  logsChannelName.value = ch.name
  logsChannelType.value = getRouteKind(ch)
  logsProtocolRoutes.value = ch.protocolRoutes ?? []
  showLogsDialog.value = true
}

// Timestamp used to trigger activity view updates (updated every 2 seconds)
const activityUpdateTick = ref(0)

// Chart expansion state
const expandedChannelKey = ref<string | null>(null)

// tooltip 懒挂载：记录当前 hover/focus 的渠道，避免 100+ 渠道每行常驻 <v-tooltip> overlay 实例
const hoveredMetricsChannel = ref<string | null>(null)

// Channel config copy state
const copiedChannelKey = ref<string | null>(null)
let copyTimeoutId: ReturnType<typeof setTimeout> | null = null

type ChannelMenuScope = 'active' | 'inactive'
type ChannelMenuKey = `${ChannelMenuScope}:${string}`
const openChannelMenuKey = ref<ChannelMenuKey | null>(null)
let channelMenuRepositionTimer: ReturnType<typeof setTimeout> | null = null

const getChannelMenuKey = (scope: ChannelMenuScope, channel: Channel): ChannelMenuKey =>
  `${scope}:${getChannelUiKey(channel)}`

const isChannelMenuOpen = (scope: ChannelMenuScope, channel: Channel): boolean =>
  openChannelMenuKey.value === getChannelMenuKey(scope, channel)

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
  channel: Channel,
  open: boolean,
) => {
  const key = getChannelMenuKey(scope, channel)

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
const toggleChannelChart = (channel: Channel) => {
  const key = getChannelUiKey(channel)
  expandedChannelKey.value = expandedChannelKey.value === key ? null : key
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
    copiedChannelKey.value = getChannelUiKey(channel)
    copyTimeoutId = setTimeout(() => {
      copiedChannelKey.value = null
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
const initialBuiltInOrder = computed(() => props.channels.map(getChannelUiKey))
const lastKnownActiveOrder = ref<string[]>([])
const lastKnownInactiveOrder = ref<string[]>([])

// 按用户排序/后端 priority 稳定排序；有无 key 只作为缺省顺序的兜底，不覆盖用户排序
const buildChannelOrder = (
  source: Channel[],
  fallbackOrder: string[]
): Channel[] => {
  const fallbackRank = new Map<string, number>()
  fallbackOrder.forEach((key, rank) => fallbackRank.set(key, rank))

  const originalRank = new Map<string, number>()
  initialBuiltInOrder.value.forEach((key, rank) => originalRank.set(key, rank))

  const hasKey = (ch: Channel) =>
    Array.isArray(ch.apiKeys) && ch.apiKeys.length > 0

  const getRank = (ch: Channel): number =>
    ch.priority ?? fallbackRank.get(getChannelUiKey(ch)) ?? originalRank.get(getChannelUiKey(ch)) ?? getRouteIndex(ch)

  return [...source].sort((a, b) => {
    const rankDiff = getRank(a) - getRank(b)
    if (rankDiff !== 0) return rankDiff

    // 只有在优先级完全相同时，才把已配置 key 的渠道排前，避免覆盖用户拖拽/置顶排序
    const keyDiff = Number(hasKey(b)) - Number(hasKey(a))
    if (keyDiff !== 0) return keyDiff

    return getRouteIndex(a) - getRouteIndex(b)
  })
}

const isSameKeyOrder = (current: string[], next: string[]) => (
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
  const nextOrder = channels.map(getChannelUiKey)
  if (!isSameKeyOrder(lastKnownInactiveOrder.value, nextOrder)) {
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
  lastKnownActiveOrder.value = newActive.map(getChannelUiKey)

  // 通过索引列表比较，判断是否需要整体重建
  const currentIndexes = activeChannels.value.map(getChannelUiKey).join(',')
  const newIndexes = newActive.map(getChannelUiKey).join(',')

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
  expandedChannelKey.value = null // Collapse the expanded chart
  // Refresh locally if dashboard props are not being used
  if (!props.dashboardMetrics) {
    refreshMetrics()
  }
})

// Fetch channel metrics
const getChannelMetrics = (channel: Channel): ChannelMetrics | undefined => {
  const routeKind = getRouteKind(channel)
  const routeIndex = getRouteIndex(channel)
  return metrics.value.find(m => {
    if (m.channelIndex !== routeIndex) return false
    return !m.routeKind || m.routeKind === routeKind
  })
}

// Fetch channel health (§8.2) by stable UID, with a kind/index fallback for older data.
const getChannelHealth = (channel: Channel): ChannelHealthItem | undefined => {
  const channelUid = channel.channelUid?.trim()
  if (channelUid) {
    const health = props.healthMap?.get(channelUid)
    if (health) return health
  }
  return props.healthMap?.get(`${getRouteKind(channel)}:${getRouteIndex(channel)}`)
}

const isLongLatencyChannel = (channel: Channel): boolean => {
  const health = getChannelHealth(channel)
  return health?.speedTier === 'slow'
    && (health.connectSampleCount ?? 0) > 0
    && (health.p95ConnectLatencyMs ?? 0) > 0
}

const formatObservedLatency = (latencyMs?: number): string => {
  if (!latencyMs || latencyMs <= 0) return '--'
  if (latencyMs >= 1_000) return `${(latencyMs / 1_000).toFixed(1)}s`
  return `${Math.round(latencyMs)}ms`
}

// Origin / pool tags for a channel (§8.2 标签系统)
interface OriginTag { label: string; color: string; icon: string }
const getOriginTags = (channel: Channel): OriginTag[] => {
  const h = getChannelHealth(channel)
  if (!h) return []
  const tags: OriginTag[] = []
  if (h.originTier && h.originTier !== 'unknown') {
    const tierMap: Record<string, { key: string; color: string; icon: string }> = {
      first:  { key: 'channelHealth.originOfficial', color: 'blue',   icon: 'mdi-shield-check' },
      second: { key: 'channelHealth.originRelay',    color: 'indigo', icon: 'mdi-account-group' },
      third:  { key: 'channelHealth.originCommunity', color: 'green',  icon: 'mdi-hand-heart' },
    }
    const cfg = tierMap[h.originTier]
    if (cfg) tags.push({ label: t(cfg.key), color: cfg.color, icon: cfg.icon })
  }
  if (h.poolTag) {
    const poolMap: Record<string, { key: string; color: string; icon: string }> = {
      free: { key: 'channelHealth.poolFree', color: 'green',  icon: 'mdi-gift' },
      temp: { key: 'channelHealth.poolTemp', color: 'orange', icon: 'mdi-clock-alert' },
    }
    const cfg = poolMap[h.poolTag]
    if (cfg) tags.push({ label: t(cfg.key), color: cfg.color, icon: cfg.icon })
  }
  return tags
}

// Helper method for fetching time-sliced statistics
const get15mStats = (channel: Channel) => {
  return getChannelMetrics(channel)?.timeWindows?.['15m']
}

const get1hStats = (channel: Channel) => {
  return getChannelMetrics(channel)?.timeWindows?.['1h']
}

const get6hStats = (channel: Channel) => {
  return getChannelMetrics(channel)?.timeWindows?.['6h']
}

const get24hStats = (channel: Channel) => {
  return getChannelMetrics(channel)?.timeWindows?.['24h']
}

// Provider 托管渠道展示品牌友好名，后台仍保留原始保留名。
const getChannelDisplayName = (channel: Channel): string => {
  if (isManagedProviderChannel(channel)) {
    const provider = providerDisplayName(channel.providerId)
    if (provider) {
      const key = isOfficialProviderChannel(channel)
        ? 'channelEditor.managed.officialChannel'
        : 'channelEditor.managed.providerChannel'
      return t(key, { provider })
    }
  }
  return channel.name
}

const getProtocolCapsules = (channel: Channel) => {
  if (channel.protocolCapsules?.length) return channel.protocolCapsules
  return [{
    kind: getRouteKind(channel),
    label: getRouteKind(channel),
    serviceType: channel.serviceType,
    channelUid: channel.channelUid,
    index: getRouteIndex(channel),
    status: channel.status,
  }]
}

const getProtocolCapsuleColor = (kind: ChannelKind): string => {
  switch (kind) {
    case 'messages': return 'deep-orange'
    case 'chat': return 'blue'
    case 'responses': return 'green'
    case 'gemini': return 'purple'
    default: return 'grey'
  }
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
  return `${t('orchestration.hitRate')} ${hitRate.toFixed(0)}% · ${t('orchestration.read')} ${formatTokens(cacheReadTokens)} · ${t('orchestration.write')} ${formatTokens(cacheCreationTokens)}`
}

const websiteLinkLabel = (kind: ChannelWebsiteKind): string => {
  if (kind === 'agent_plan') return t('volcengineAccessKey.agentPlanConsole')
  if (kind === 'coding_plan') return t('volcengineAccessKey.codingPlanConsole')
  return t('orchestration.openWebsite')
}

const websiteLinkIcon = (kind: ChannelWebsiteKind): string => (
  kind === 'coding_plan' ? 'mdi-code-braces' : kind === 'agent_plan' ? 'mdi-robot-outline' : 'mdi-open-in-new'
)

// ============== Realtime channel activity helpers ==============

// Activity data Map cache (avoids linear lookup)

const {
  getActivityBars,
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
  lastKnownActiveOrder.value = activeChannels.value.map(getChannelUiKey)
}

const canReorderList = computed(() => {
  if (activeChannels.value.length === 0) return true
  if (isLlmChannelKind(props.channelType)) {
    return activeChannels.value.every(ch => isLlmChannelKind(getRouteKind(ch)))
  }
  return activeChannels.value.every(ch => getRouteKind(ch) === props.channelType)
})

// Drag change event - auto-save order
const onDragChange = () => {
  syncActiveOrder()
  saveOrder()
}

// Save order
const saveOrder = async () => {
  if (!canReorderList.value) return
  isSavingOrder.value = true
  try {
    if (isLlmChannelKind(props.channelType)) {
      // 统一 LLM 视图：按协议类型分别提交排序，保证每个协议数组内的顺序与展示一致
      const orderByKind = new Map<ChannelKind, number[]>()
      for (const ch of activeChannels.value) {
        for (const route of ch.protocolRoutes ?? []) {
          if (!isLlmChannelKind(route.kind)) continue
          const order = orderByKind.get(route.kind) ?? []
          order.push(route.index)
          orderByKind.set(route.kind, order)
        }
      }
      for (const [kind, order] of orderByKind) {
        if (order.length === 0) continue
        await getChannelTypeApi(api, kind).reorder(order)
      }
    } else {
      const order = activeChannels.value.map(getRouteIndex)
      await getCurrentChannelTypeApi().reorder(order)
    }
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
  activeChannels.value[0] ? getChannelUiKey(activeChannels.value[0]) === getChannelUiKey(channel) : false
const isLastActiveChannel = (channel: Channel): boolean =>
  activeChannels.value[activeChannels.value.length - 1]
    ? getChannelUiKey(activeChannels.value[activeChannels.value.length - 1]) === getChannelUiKey(channel)
    : false

// Move channel to top
const moveChannelToTop = async (target: Channel) => {
  if (isSavingOrder.value || !canReorderList.value) return
  const targetKey = getChannelUiKey(target)
  const idx = activeChannels.value.findIndex(ch => getChannelUiKey(ch) === targetKey)
  if (idx <= 0) return

  const [channel] = activeChannels.value.splice(idx, 1)
  activeChannels.value.unshift(channel)
  syncActiveOrder()
  await saveOrder()
}

// Move channel to bottom
const moveChannelToBottom = async (target: Channel) => {
  if (isSavingOrder.value || !canReorderList.value) return
  const targetKey = getChannelUiKey(target)
  const idx = activeChannels.value.findIndex(ch => getChannelUiKey(ch) === targetKey)
  if (idx < 0 || idx >= activeChannels.value.length - 1) return

  const [channel] = activeChannels.value.splice(idx, 1)
  activeChannels.value.push(channel)
  syncActiveOrder()
  await saveOrder()
}

const setChannelStatusInternal = async (
  channel: Channel,
  status: ChannelStatus,
  options: { refresh?: boolean } = {}
) => {
  const { refresh = true } = options
  await getCurrentChannelTypeApi(channel).setStatus(getRouteIndex(channel), status)
  if (refresh) {
    emit('refresh')
  }
}

// Set channel status
const setChannelStatus = async (channel: Channel, status: ChannelStatus) => {
  try {
    await setChannelStatusInternal(channel, status)
  } catch (error) {
    console.error('Failed to set channel status:', error)
    const errorMessage = error instanceof Error ? error.message : t('addChannel.unknownError')
    emit('error', t('toast.operationFailed', { message: errorMessage }))
  }
}

// Enable channel (move it from the standby pool to the active sequence)
const enableChannel = async (channel: Channel) => {
  await setChannelStatus(channel, 'active')
}

const resumeChannelInternal = async (
  channel: Channel,
  options: { refresh?: boolean, notify?: boolean } = {}
) => {
  const { refresh = true, notify = true } = options

  const result = await getCurrentChannelTypeApi(channel).resume(getRouteIndex(channel))
  // 仅当渠道当前不是 active 时才更新状态：已是 active 但仅因全部 Key 被拉黑/耗尽而显示恢复的渠道，
  // 跳过冗余的 setStatus(active) 请求，直接刷新数据即可。
  if (channel.status !== 'active') {
    await setChannelStatusInternal(channel, 'active', { refresh })
  } else if (refresh) {
    // active 渠道恢复全部禁用 Key 后，刷新以反映最新 disabledApiKeys 与计数
    emit('refresh')
  }

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
  const channelMetrics = getChannelMetrics(channel)
  return channel.status === 'suspended'
    || channelMetrics?.circuitState === 'open'
    || hasNoUsableChannelApiKeys(channel)
}

// isRecoverableChannel 判定渠道是否应显示「恢复」主操作（一次点击恢复全部禁用 Key + 重置熔断）：
//   - suspended 或熔断 open：原有恢复语义
//   - active 但全部 Key 被拉黑/耗尽（可用数为 0 且禁用数 > 0）：无需先暂停即可直接恢复
const isRecoverableChannel = (channel: Channel): boolean => {
  const channelMetrics = getChannelMetrics(channel)
  return channel.status === 'suspended'
    || channelMetrics?.circuitState === 'open'
    || hasOnlyDisabledChannelApiKeys(channel)
}

const getChannelRowClass = (channel: Channel) => {
  return {
    'is-tripped': isTrippedChannel(channel)
  }
}

// Resume channel (reset metrics and set it to active)
const resumeChannel = async (channel: Channel) => {
  try {
    await resumeChannelInternal(channel)
  } catch (error) {
    console.error('Failed to resume channel:', error)
    const errorMessage = error instanceof Error ? error.message : t('addChannel.unknownError')
    emit('error', t('toast.operationFailed', { message: errorMessage }))
  }
}

// Set channel promotion via the correct API for the current channel type
const setChannelPromotionInternal = async (channel: Channel, durationSeconds: number) => {
  await getCurrentChannelTypeApi(channel).promote(getRouteIndex(channel), durationSeconds)
}

// Set the channel promotion period (boost priority)
const setPromotion = async (channel: Channel) => {
  try {
    const PROMOTION_DURATION = 300 // 5 minutes

    // If the channel is recoverable (suspended / breaker open / 全部 Key 被拉黑), resume it first
    if (isRecoverableChannel(channel)) {
      await resumeChannelInternal(channel, { refresh: false, notify: false })
    }

    await setChannelPromotionInternal(channel, PROMOTION_DURATION)
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
  emit('delete', channel)
}

// 全局 tick 订阅（visibility hidden 时自动暂停）
const activityTick = useGlobalTick(2000, 'ChannelOrch-activity')

onMounted(() => {
  refreshMetrics()
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
