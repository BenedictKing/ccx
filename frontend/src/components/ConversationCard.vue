<template>
  <v-card
    :class="['conversation-card', { 'override-active': hasOverride }]"
    :style="{ '--ccx-kind-color': kindCssColor }"
    elevation="0"
    role="button"
    tabindex="0"
    :aria-expanded="expanded"
    @click="$emit('toggleExpand')"
    @keydown.enter.prevent="$emit('toggleExpand')"
    @keydown.space.prevent="$emit('toggleExpand')"
  >
    <v-card-text class="pa-4">
      <div class="task-card-title-row">
        <v-tooltip :text="statusTooltip" location="top" :open-delay="150" content-class="ccx-tooltip">
          <template #activator="{ props: tooltipProps }">
            <span v-bind="tooltipProps" :class="['status-led', `status-led--${conversation.status}`]"></span>
          </template>
        </v-tooltip>
        <span :class="['status-label', `status-label--${conversation.status}`]">{{ statusLabel }}</span>
        <v-tooltip :text="kindTooltip" location="top" :open-delay="150" content-class="ccx-tooltip">
          <template #activator="{ props: tooltipProps }">
            <span v-bind="tooltipProps" class="kind-chip">{{ kindLabel }}</span>
          </template>
        </v-tooltip>
        <span class="task-card-title" :title="tooltipText">
          <span :class="['display-label-text', { 'display-label-text--expanded': expanded }]">{{ displayLabel }}</span>
        </span>
        <v-tooltip :text="requestCountTooltip" location="top" :open-delay="150" content-class="ccx-tooltip">
          <template #activator="{ props: tooltipProps }">
            <span v-bind="tooltipProps" class="task-meta-item task-title-stat">{{ conversation.requestCount }}x</span>
          </template>
        </v-tooltip>
        <v-tooltip :text="durationTooltip" location="top" :open-delay="150" content-class="ccx-tooltip">
          <template #activator="{ props: tooltipProps }">
            <span v-bind="tooltipProps" class="task-meta-item task-title-stat">{{ duration }}</span>
          </template>
        </v-tooltip>
        <v-tooltip v-if="hasSubagentActivity && (expanded || childConversationCount === 0)" :text="subagentCountTooltip" location="top" :open-delay="150" content-class="ccx-tooltip">
          <template #activator="{ props: tooltipProps }">
            <span v-bind="tooltipProps" class="task-subagent-chip">
              {{ t('cockpit.subagents') }} {{ displaySubagentCount }}
            </span>
          </template>
        </v-tooltip>
      </div>

      <div
        v-if="conversation.parentConversationId || conversation.parentThreadId || (!expanded && childConversationCount > 0)"
        class="relation-row"
        @click.stop
      >
        <v-tooltip
          v-if="conversation.parentConversationId"
          :text="parentConversationTooltip"
          location="top"
          :open-delay="150"
          content-class="ccx-tooltip"
        >
          <template #activator="{ props: tooltipProps }">
            <button
              v-bind="tooltipProps"
              type="button"
              class="relation-chip relation-chip--parent"
              @click="navigateConversation(conversation.parentConversationId)"
            >
              <v-icon size="12">mdi-arrow-left</v-icon>
              <span>{{ t('cockpit.relation.parent') }}</span>
            </button>
          </template>
        </v-tooltip>
        <v-tooltip
          v-else-if="conversation.parentThreadId"
          :text="parentThreadTooltip"
          location="top"
          :open-delay="150"
          content-class="ccx-tooltip"
        >
          <template #activator="{ props: tooltipProps }">
            <span
              v-bind="tooltipProps"
              class="relation-chip relation-chip--thread"
            >
              <v-icon size="12">mdi-arrow-left</v-icon>
              <span>{{ t('cockpit.relation.parentThread', { id: parentThreadLabel }) }}</span>
            </span>
          </template>
        </v-tooltip>

        <v-tooltip
          v-if="!expanded && childConversationCount > 0 && firstChildConversationId"
          :text="childConversationTooltip"
          location="top"
          :open-delay="150"
          content-class="ccx-tooltip"
        >
          <template #activator="{ props: tooltipProps }">
            <button
              v-bind="tooltipProps"
              type="button"
              class="relation-chip relation-chip--children"
              @click="navigateConversation(firstChildConversationId)"
            >
              <v-icon size="12">mdi-source-branch</v-icon>
              <span>{{ t('cockpit.relation.children', { count: String(childConversationCount) }) }}</span>
            </button>
          </template>
        </v-tooltip>
      </div>

      <div class="task-card-notes">
        <span class="task-card-model" :title="conversation.lastModel">{{ conversation.lastModel }}</span>
        <v-icon size="12" class="task-route-arrow">mdi-arrow-right</v-icon>
        <span class="task-card-channel" :title="currentChannelInfo.name">{{ currentChannelInfo.name }}</span>
        <span v-if="hasOverride" class="override-badge">{{ t('cockpit.overrideBadge') }}</span>
      </div>

      <div v-if="expanded" class="main-conversation-detail">
        <div class="conversation-section-head">
          <span>{{ t('cockpit.mainConversation') }}</span>
        </div>
        <div ref="mainConversationTurnsRef" class="main-conversation-turns">
          <div
            v-for="(turn, index) in mainConversationTurns"
            :key="`${index}-${turn.fullText}`"
            :class="[
              'main-conversation-turn',
              {
                'main-conversation-turn--numbered': mainConversationTurns.length > 1,
                'main-conversation-turn--collapsible': turn.truncated,
              },
            ]"
            :role="turn.truncated ? 'button' : undefined"
            :tabindex="turn.truncated ? 0 : undefined"
            :aria-expanded="turn.truncated ? turn.expanded : undefined"
            @click.stop="turn.truncated && toggleMainConversationTurn(index)"
            @keydown.enter.prevent.stop="turn.truncated && toggleMainConversationTurn(index)"
            @keydown.space.prevent.stop="turn.truncated && toggleMainConversationTurn(index)"
          >
            <span v-if="mainConversationTurns.length > 1" class="main-conversation-turn-index">{{ index - mainConversationTurns.length + 1 }}</span>
            <span class="main-conversation-turn-text">
              <template v-if="turn.truncated && !turn.expanded">
                <span>{{ turn.head }}</span>
                <span
                  class="main-conversation-turn-ellipsis"
                  aria-hidden="true"
                >
                  …
                </span>
                <span>{{ turn.tail }}</span>
              </template>
              <template v-else>{{ turn.fullText }}</template>
            </span>
          </div>
        </div>
        <div v-if="conversation.lastRecap" class="main-conversation-recap">
          <div class="main-conversation-recap-head">
            <v-icon size="13">mdi-text-box-check-outline</v-icon>
            <span>{{ t('cockpit.recap') }}</span>
          </div>
          <div class="main-conversation-recap-text">{{ conversation.lastRecap }}</div>
        </div>
        <div class="main-conversation-grid">
          <div v-for="row in mainDetailRows" :key="row.label" class="main-conversation-field">
            <span>{{ row.label }}</span>
            <strong>{{ row.value }}</strong>
          </div>
        </div>
      </div>

      <!-- Expanded: Override alert -->
      <v-alert v-if="expanded && hasOverride" type="warning" density="compact" variant="tonal" class="override-alert mb-2 mt-2">
        <div class="d-flex align-center">
          <span v-if="override?.isPerpetual" class="text-caption">{{ t('cockpit.overrideActivePerpetual') }}</span>
          <span v-else class="text-caption">{{ t('cockpit.overrideActive', { time: remainingTime }) }}</span>
          <v-spacer />
          <v-tooltip :text="t('cockpit.tooltip.restoreDefault')" location="top" :open-delay="150" content-class="ccx-tooltip">
            <template #activator="{ props: tooltipProps }">
              <v-btn v-bind="tooltipProps" size="x-small" variant="text" @click.stop="$emit('removeOverride', conversation.id)">{{ t('cockpit.restoreDefault') }}</v-btn>
            </template>
          </v-tooltip>
        </div>
      </v-alert>

      <div v-if="expanded" class="main-routing-section">
        <div class="main-routing-head" @click.stop>
          <span class="main-routing-title">{{ t('cockpit.mainRouting') }} · {{ conversation.lastModel }}</span>
          <v-select
            :model-value="overrideDuration"
            :items="durationOptions"
            :label="t('cockpit.overrideDuration')"
            density="compact"
            variant="outlined"
            hide-details
            class="override-duration-select"
            @update:model-value="emit('update:overrideDuration', $event)"
          />
        </div>
        <ConversationChannelSequence
          :channels="channelSequence"
          :current-channel="conversation.currentChannel"
          :next-channel="nextChannel"
          :next-channel-circuit-open="nextChannelCircuitOpen"
          :override-active="hasOverride"
          @move-to-top="handleMoveToTop"
          @demote="handleDemote"
        />
      </div>

      <div v-if="expanded && showSubagentSection" class="subagent-expanded-section" @click.stop>
        <div v-if="subagents.length > 0" class="subagent-list">
          <div class="subagent-list-head">
            <span>{{ t('cockpit.subagents') }}</span>
            <span>{{ subagents.length }}</span>
          </div>
          <div v-for="agent in visibleSubagents" :key="agent.id" class="subagent-row">
            <span :class="['subagent-dot', `subagent-dot--${agent.status}`]"></span>
            <div class="subagent-row-main">
              <span class="subagent-row-title">{{ agent.title || agent.userId }}</span>
              <span class="subagent-row-meta">{{ agent.lastModel }} · {{ agent.channelName || `Channel ${agent.currentChannel}` }}</span>
            </div>
            <v-chip size="x-small" variant="tonal" :color="subagentStatusColor(agent.status)">
              {{ agent.status }}
            </v-chip>
          </div>
          <v-tooltip v-if="subagents.length > visibleSubagents.length" :text="moreSubagentsTooltip" location="top" :open-delay="150" content-class="ccx-tooltip">
            <template #activator="{ props: tooltipProps }">
              <button v-bind="tooltipProps" type="button" class="subagent-more" @click.stop="$emit('toggleExpand')">
                +{{ subagents.length - visibleSubagents.length }} more
              </button>
            </template>
          </v-tooltip>
        </div>

        <!-- Subagent Routing：为主对话与 subagent 分别指定渠道 -->
        <div class="subagent-routing mt-3">
          <div class="d-flex align-center mb-1">
            <span class="text-caption text-medium-emphasis">{{ t('cockpit.subagentRouting') }}</span>
            <span v-if="hasSubagentOverride" class="text-caption text-warning ml-2">[{{ t('cockpit.subagentOverride') }}]</span>
            <span v-else class="text-caption text-medium-emphasis ml-2">[{{ t('cockpit.subagentFollowMain') }}]</span>
            <v-spacer />
            <v-tooltip v-if="hasSubagentOverride" :text="t('cockpit.tooltip.clearSubagentOverride')" location="top" :open-delay="150" content-class="ccx-tooltip">
              <template #activator="{ props: tooltipProps }">
                <v-btn v-bind="tooltipProps" size="x-small" variant="text" @click.stop="handleClearSubagentOverride">{{ t('cockpit.subagentClearOverride') }}</v-btn>
              </template>
            </v-tooltip>
          </div>
          <ConversationChannelSequence
            :channels="subagentSequence"
            :current-channel="subagentCurrentChannel"
            :next-channel="subagentNextChannel"
            :next-channel-circuit-open="subagentNextChannelCircuitOpen"
            :override-active="hasSubagentOverride"
            @move-to-top="handleSubagentMoveToTop"
            @demote="handleSubagentDemote"
          />
        </div>
      </div>

      <div v-if="expanded && conversation.rawUserId" class="raw-user-id mt-2 d-flex align-center">
        <v-tooltip :text="t('cockpit.copyRawUserId')" location="top" :open-delay="150" content-class="ccx-tooltip">
          <template #activator="{ props: tooltipProps }">
            <span v-bind="tooltipProps" class="text-caption text-medium-emphasis font-weight-mono raw-user-id-text" @click.stop="copyRawUserId">{{ conversation.rawUserId }}</span>
          </template>
        </v-tooltip>
        <v-tooltip :text="t('cockpit.copyRawUserId')" location="top" :open-delay="150" content-class="ccx-tooltip">
          <template #activator="{ props: tooltipProps }">
            <v-btn v-bind="tooltipProps" icon size="x-small" variant="text" class="copy-btn" :aria-label="t('cockpit.copyRawUserId')" @click.stop="copyRawUserId">
              <v-icon size="12">mdi-content-copy</v-icon>
            </v-btn>
          </template>
        </v-tooltip>
      </div>
    </v-card-text>
  </v-card>
</template>

<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import type { ConversationInfo, SequenceOverrideInfo, ChannelSequenceEntry } from '@/services/api'
import type { SubagentSummary } from '@/utils/conversationDashboard'
import { useI18n } from '@/i18n'
import { buildConversationTurnMiddlePreview, getConversationTurnFont, normalizeConversationTurnSources } from '@/utils/conversationPreview'
import ConversationChannelSequence from './ConversationChannelSequence.vue'

const { t } = useI18n()

interface ChannelInfo {
  index: number
  name: string
  status: string
  circuitOpen?: boolean
}

interface MainConversationTurn {
  fullText: string
  head: string
  tail: string
  truncated: boolean
  expanded: boolean
}

const props = defineProps<{
  conversation: ConversationInfo
  subagents?: ConversationInfo[]
  subagentSummary?: SubagentSummary
  override?: SequenceOverrideInfo
  availableChannels: ChannelInfo[]
  expanded: boolean
  nowMs: number
  overrideDuration: number
  relatedParentTitle?: string
}>()

const emit = defineEmits<{
  'update:overrideDuration': [value: number]
  toggleExpand: []
  setOverride: [convId: string, sequence: ChannelSequenceEntry[], subagentSequence?: ChannelSequenceEntry[], clearSubagentSequence?: boolean]
  removeOverride: [convId: string]
  navigateConversation: [conversationId: string]
  success: [message: string]
  error: [message: string]
}>()

const conversation = computed(() => props.conversation)
const emptySubagentSummary: SubagentSummary = { total: 0, streaming: 0, active: 0, idle: 0 }
const subagents = computed(() => props.subagents ?? [])
const subagentSummary = computed(() => props.subagentSummary ?? emptySubagentSummary)
const hasSubagentActivity = computed(() => props.conversation.hasSubagents || subagents.value.length > 0)
const displaySubagentCount = computed(() => subagentSummary.value.total || props.conversation.subagentCount || subagents.value.length || 1)
const visibleSubagents = computed(() => subagents.value.slice(0, props.expanded ? 12 : 4))
const hasOverride = computed(() => props.override?.hasMainSequence === true)
const kindLabel = computed(() => props.conversation.kind.toUpperCase())
const statusLabel = computed(() => t(`cockpit.column.${props.conversation.status === 'active' ? 'working' : props.conversation.status}`))
const durationOptions = computed(() => [
  { title: t('cockpit.durationDefault'), value: 1800 },
  { title: t('cockpit.duration15min'), value: 900 },
  { title: t('cockpit.duration1hour'), value: 3600 },
  { title: t('cockpit.duration2hours'), value: 7200 },
  { title: t('cockpit.duration4hours'), value: 14400 },
  { title: t('cockpit.duration8hours'), value: 28800 },
  { title: t('cockpit.duration12hours'), value: 43200 },
  { title: t('cockpit.duration24hours'), value: 86400 },
  { title: t('cockpit.durationNever'), value: -1 },
])

function subagentStatusColor(status: ConversationInfo['status']): string {
  switch (status) {
    case 'streaming': return 'error'
    case 'active': return 'primary'
    case 'idle': return 'success'
    default: return 'grey'
  }
}

async function syncMainConversationTurnsWidth() {
  await nextTick()
  const element = mainConversationTurnsRef.value
  if (!element) {
    mainConversationTurnsWidth.value = 0
    return
  }

  mainConversationTurnsWidth.value = element.clientWidth
  mainConversationTurnsObserver?.disconnect()
  mainConversationTurnsObserver = null

  if (typeof window === 'undefined' || !('ResizeObserver' in window)) return

  mainConversationTurnsObserver = new ResizeObserver(() => {
    mainConversationTurnsWidth.value = mainConversationTurnsRef.value?.clientWidth ?? 0
  })
  mainConversationTurnsObserver.observe(element)
}

const kindCssColor = computed(() => {
  const map: Record<string, string> = {
    messages: 'var(--ccx-kind-messages)',
    chat: 'var(--ccx-kind-chat)',
    responses: 'var(--ccx-kind-responses)',
    gemini: 'var(--ccx-kind-gemini)',
    images: 'var(--ccx-kind-images)',
    vectors: 'var(--ccx-kind-vectors)',
  }
  return map[props.conversation.kind] ?? 'rgb(var(--v-theme-on-surface))'
})

const displayLabel = computed(() => props.conversation.title || props.conversation.userId)
const mainConversationText = computed(() => props.conversation.lastUserMessage || displayLabel.value)
const mainConversationTurnsRef = ref<HTMLElement | null>(null)
const mainConversationTurnsWidth = ref(0)
const expandedMainConversationTurnIndexes = ref<Set<number>>(new Set())
let mainConversationTurnsObserver: ResizeObserver | null = null
const sourceMainConversationTurns = computed(() => {
  return normalizeConversationTurnSources(mainConversationText.value, props.conversation.lastUserMessages)
})
const mainConversationTurns = computed<MainConversationTurn[]>(() => {
  const element = mainConversationTurnsRef.value
  const width = mainConversationTurnsWidth.value
  const font = element && width > 0 ? getConversationTurnFont(element) : ''

  return sourceMainConversationTurns.value.map((turn, index) => {
    const preview = buildConversationTurnMiddlePreview(turn, {
      width,
      font,
      edgeLines: 2,
    })

    return {
      fullText: turn,
      head: preview.head,
      tail: preview.tail,
      truncated: preview.truncated,
      expanded: expandedMainConversationTurnIndexes.value.has(index),
    }
  })
})
const childConversationCount = computed(() => props.conversation.childConversationIds?.length ?? 0)
const firstChildConversationId = computed(() => props.conversation.childConversationIds?.[0])
const parentThreadLabel = computed(() => props.conversation.parentThreadId ? shortId(props.conversation.parentThreadId) : '')
const parentConversationTooltip = computed(() => t('cockpit.tooltip.parentConversation', { id: props.relatedParentTitle || props.conversation.parentConversationId || '' }))
const parentThreadTooltip = computed(() => t('cockpit.tooltip.parentThread', { id: props.conversation.parentThreadId || '' }))
const childConversationTooltip = computed(() => t('cockpit.tooltip.childConversation', { id: firstChildConversationId.value || '' }))

const tooltipText = computed(() => {
  if (props.conversation.title) return props.conversation.title
  return props.conversation.userId
})

const duration = computed(() => {
  const start = new Date(props.conversation.createdAt).getTime()
  if (!Number.isFinite(start)) return '<1m'
  const mins = Math.floor((props.nowMs - start) / 60000)
  if (mins < 1) return '<1m'
  if (mins < 60) return `${mins}m`
  return `${Math.floor(mins / 60)}h${mins % 60}m`
})

const statusTooltip = computed(() => {
  switch (props.conversation.status) {
    case 'streaming': return t('cockpit.tooltip.statusStreaming')
    case 'active': return t('cockpit.tooltip.statusActive')
    case 'idle': return t('cockpit.tooltip.statusIdle')
    default: return t('cockpit.tooltip.statusUnknown')
  }
})
const kindTooltip = computed(() => t('cockpit.tooltip.kind', { kind: kindLabel.value }))
const requestCountTooltip = computed(() => t('cockpit.tooltip.requests', { count: props.conversation.requestCount }))
const durationTooltip = computed(() => t('cockpit.tooltip.duration', { duration: duration.value }))
const subagentCountTooltip = computed(() => t('cockpit.tooltip.subagents', { count: displaySubagentCount.value }))

const mainDetailRows = computed(() => [
  { label: t('cockpit.detail.requests'), value: `${props.conversation.requestCount}x` },
  { label: t('cockpit.detail.duration'), value: duration.value },
])

function toggleMainConversationTurn(index: number) {
  const next = new Set(expandedMainConversationTurnIndexes.value)
  if (next.has(index)) {
    next.delete(index)
  } else {
    next.add(index)
  }
  expandedMainConversationTurnIndexes.value = next
}

watch(
  () => props.expanded,
  (expanded) => {
    mainConversationTurnsObserver?.disconnect()
    mainConversationTurnsObserver = null
    if (!expanded) {
      mainConversationTurnsWidth.value = 0
      expandedMainConversationTurnIndexes.value = new Set()
      return
    }
    void syncMainConversationTurnsWidth()
  },
  { immediate: true },
)

watch(
  () => mainConversationText.value,
  () => {
    expandedMainConversationTurnIndexes.value = new Set()
  },
)

onBeforeUnmount(() => {
  mainConversationTurnsObserver?.disconnect()
  mainConversationTurnsObserver = null
})

const remainingTime = computed(() => {
  if (!props.override) return ''
  if (props.override.isPerpetual) return t('cockpit.durationNever')
  const expires = new Date(props.override.expiresAt).getTime()
  const remaining = Math.max(0, expires - props.nowMs)
  const mins = Math.floor(remaining / 60000)
  const secs = Math.floor((remaining % 60000) / 1000)
  return `${mins}:${secs.toString().padStart(2, '0')}`
})

const fallbackChannels = computed((): ChannelInfo[] => {
  const channels: ChannelInfo[] = []
  const pushUnique = (channel: ChannelInfo) => {
    if (!channels.some(ch => ch.index === channel.index)) channels.push(channel)
  }
  if (hasOverride.value && props.override?.sequence) {
    for (const entry of props.override.sequence) {
      pushUnique({ index: entry.channelIndex, name: entry.channelName || `Channel ${entry.channelIndex}`, status: 'active' })
    }
  }
  pushUnique({ index: props.conversation.currentChannel, name: props.conversation.channelName || `Channel ${props.conversation.currentChannel}`, status: 'active' })
  return channels
})

const channelSequence = computed((): ChannelInfo[] => {
  if (hasOverride.value && props.override?.sequence) {
    return props.override.sequence.map(entry => {
      const ch = props.availableChannels.find(c => c.index === entry.channelIndex)
      return { index: entry.channelIndex, name: entry.channelName || ch?.name || `Channel ${entry.channelIndex}`, status: ch?.status || 'active', circuitOpen: ch?.circuitOpen }
    })
  }
  const channels = props.availableChannels.filter(ch => ch.status !== 'disabled')
  return channels.length > 0 ? channels : fallbackChannels.value
})

// subagent 渠道序列：优先用 override.subagentSequence，否则 fallback 到主序列
const subagentSequence = computed((): ChannelInfo[] => {
  if (props.override?.subagentSequence && props.override.subagentSequence.length > 0) {
    return props.override.subagentSequence.map(entry => {
      const ch = props.availableChannels.find(c => c.index === entry.channelIndex)
      return { index: entry.channelIndex, name: entry.channelName || ch?.name || `Channel ${entry.channelIndex}`, status: ch?.status || 'active', circuitOpen: ch?.circuitOpen }
    })
  }
  return channelSequence.value
})

const hasSubagentOverride = computed(() => !!props.override?.subagentSequence && props.override.subagentSequence.length > 0)
const showSubagentSection = computed(() => hasSubagentActivity.value || hasSubagentOverride.value)
const subagentCurrentChannel = computed(() => props.conversation.subagentChannel ?? -1)

const currentChannelInfo = computed(() => {
  const existing = channelSequence.value.find(ch => ch.index === props.conversation.currentChannel)
    ?? props.availableChannels.find(ch => ch.index === props.conversation.currentChannel)
  if (existing) return existing
  return { index: props.conversation.currentChannel, name: props.conversation.channelName || `Channel ${props.conversation.currentChannel}`, status: 'active' }
})

const nextChannel = computed(() => {
  const candidate = hasOverride.value ? props.override?.sequence?.[0]?.channelIndex : undefined
  return candidate !== undefined && candidate !== props.conversation.currentChannel ? candidate : undefined
})

const nextChannelInfo = computed(() => {
  if (nextChannel.value === undefined) return undefined
  const existing = channelSequence.value.find(ch => ch.index === nextChannel.value)
    ?? props.availableChannels.find(ch => ch.index === nextChannel.value)
  if (existing) return existing
  const entry = hasOverride.value ? props.override?.sequence?.[0] : undefined
  return { index: nextChannel.value!, name: entry?.channelName || `Channel ${nextChannel.value}`, status: 'active' }
})

const nextChannelCircuitOpen = computed(() => {
  if (!nextChannelInfo.value) return false
  return nextChannelInfo.value.circuitOpen === true
})

const subagentNextChannel = computed(() => {
  const candidate = props.override?.subagentSequence?.[0]?.channelIndex ?? (hasOverride.value ? props.override?.sequence?.[0]?.channelIndex : undefined)
  return candidate !== undefined && candidate !== subagentCurrentChannel.value ? candidate : undefined
})

const subagentNextChannelInfo = computed(() => {
  if (subagentNextChannel.value === undefined) return undefined
  const existing = subagentSequence.value.find(ch => ch.index === subagentNextChannel.value)
    ?? props.availableChannels.find(ch => ch.index === subagentNextChannel.value)
  if (existing) return existing
  const entry = props.override?.subagentSequence?.[0]
  return { index: subagentNextChannel.value!, name: entry?.channelName || `Channel ${subagentNextChannel.value}`, status: 'active' }
})

const subagentNextChannelCircuitOpen = computed(() => {
  if (!subagentNextChannelInfo.value) return false
  return subagentNextChannelInfo.value.circuitOpen === true
})

const moreSubagentsTooltip = computed(() => t('cockpit.tooltip.moreSubagents', { count: subagents.value.length - visibleSubagents.value.length }))

function buildSequence(channels: ChannelInfo[]): ChannelSequenceEntry[] {
  return channels.map(ch => ({ channelIndex: ch.index, channelName: ch.name }))
}

// subagent 渠道：移到最前（等同主对话的 moveToTop）
function handleSubagentMoveToTop(ch: ChannelInfo, currentIdx: number) {
  if (currentIdx === 0) return
  const current = [...subagentSequence.value]
  const [item] = current.splice(currentIdx, 1)
  current.unshift(item)
  emit('setOverride', props.conversation.id, [], buildSequence(current))
}

function handleSubagentDemote(index: number) {
  const current = [...subagentSequence.value]
  if (index >= current.length - 1) return
  const [item] = current.splice(index, 1)
  current.push(item)
  emit('setOverride', props.conversation.id, [], buildSequence(current))
}

function handleClearSubagentOverride() {
  emit('setOverride', props.conversation.id, [], [], true)
}

function handleMoveToTop(ch: ChannelInfo, currentIdx: number) {
  if (currentIdx === 0) return
  const current = [...channelSequence.value]
  const [item] = current.splice(currentIdx, 1)
  current.unshift(item)
  emit('setOverride', props.conversation.id, buildSequence(current))
}

function handleDemote(index: number) {
  const current = [...channelSequence.value]
  if (index >= current.length - 1) return
  const [item] = current.splice(index, 1)
  current.push(item)
  emit('setOverride', props.conversation.id, buildSequence(current))
}

async function copyRawUserId() {
  if (!props.conversation.rawUserId) return
  try {
    await navigator.clipboard.writeText(props.conversation.rawUserId)
    emit('success', t('cockpit.rawUserIdCopied'))
  } catch {
    emit('error', t('cockpit.rawUserIdCopyFailed'))
  }
}

function navigateConversation(id?: string) {
  if (!id) return
  emit('navigateConversation', id)
}

function shortId(value: string): string {
  if (value.length <= 12) return value
  return `${value.slice(0, 8)}...${value.slice(-4)}`
}

</script>

<style scoped>
.conversation-card {
  cursor: pointer;
  position: relative;
  transition: border-color 0.15s ease, box-shadow 0.15s ease;
  border: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  box-shadow: none;
  background: rgb(var(--v-theme-surface));
  border-radius: 0;
}
.conversation-card::before {
  content: '';
  position: absolute;
  left: 0;
  top: 8px;
  bottom: 8px;
  width: 3px;
  background: var(--ccx-kind-color);
  border: 0;
  pointer-events: none;
  z-index: 1;
}
.conversation-card:hover {
  border-color: var(--ccx-kind-color);
  box-shadow: 0 4px 14px rgba(0, 0, 0, 0.08);
}
.conversation-card:active {
  transform: translateY(1px);
}
.conversation-card.override-active {
  border-color: rgb(var(--v-theme-warning));
}
.conversation-card.override-active:hover {
  border-color: rgb(var(--v-theme-warning));
}
.v-theme--dark .conversation-card {
  border-color: rgba(255, 255, 255, 0.16);
  box-shadow: none;
}
.v-theme--dark .conversation-card:hover {
  border-color: var(--ccx-kind-color);
  box-shadow: 0 6px 18px rgba(0, 0, 0, 0.34);
}

.task-card-title-row {
  display: flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
}

.task-card-title {
  flex: 1 1 auto;
  min-width: 0;
  color: rgb(var(--v-theme-on-surface));
  font-size: 14px;
  font-weight: 800;
  line-height: 1.4;
}

.status-label {
  flex-shrink: 0;
  color: rgb(var(--v-theme-on-surface) / 62%);
  font-size: 11px;
  font-weight: 700;
}

.status-label--streaming {
  color: rgb(var(--v-theme-primary));
}

.task-card-meta {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
  margin-top: 8px;
}

.task-meta-item {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  color: rgb(var(--v-theme-on-surface) / 64%);
  font-size: 11px;
  font-weight: 700;
}

.task-title-stat,
.task-subagent-chip {
  flex-shrink: 0;
}

.task-subagent-chip {
  display: inline-flex;
  align-items: center;
  height: 20px;
  padding: 0 5px;
  border: 1px solid rgb(var(--v-theme-warning));
  color: rgb(var(--v-theme-on-surface) / 76%);
  background: rgba(var(--v-theme-warning), 0.1);
  font-size: 11px;
  font-weight: 700;
  line-height: 1;
}

.task-meta-dot {
  width: 7px;
  height: 7px;
  border-radius: 999px;
  background: var(--ccx-kind-color);
}

.task-status-chip {
  margin-left: auto;
  font-weight: 800;
}

.task-card-notes {
  display: flex;
  align-items: center;
  gap: 7px;
  flex-wrap: wrap;
  margin-top: 8px;
  color: rgb(var(--v-theme-on-surface) / 68%);
  font-size: 12px;
  line-height: 1.45;
}

.task-card-model,
.task-card-channel {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.task-card-channel {
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  color: rgb(var(--v-theme-on-surface) / 58%);
}

.task-route-arrow {
  flex: 0 0 auto;
  color: rgb(var(--v-theme-on-surface) / 42%);
}

.override-badge {
  margin-left: auto;
  padding: 2px 5px;
  border: 1px solid rgb(var(--v-theme-warning) / 55%);
  color: rgb(var(--v-theme-on-surface) / 76%);
  font-size: 11px;
  font-weight: 700;
  white-space: nowrap;
}

/* Status LED */
.status-led {
  display: inline-block;
  width: 8px; height: 8px;
  border-radius: 50%;
  flex-shrink: 0;
}
.status-led--streaming {
  background: var(--ccx-led-streaming-color);
  animation: ccx-led-pulse 1.4s ease-in-out infinite;
}
.status-led--active {
  background: var(--ccx-status-breaker-half-open-dot-bg);
  box-shadow: 0 0 4px 1px var(--ccx-status-breaker-half-open-dot-glow);
}
.status-led--idle {
  background: var(--ccx-status-disabled-dot-bg);
}

/* Kind chip */
.kind-chip {
  display: inline-flex;
  align-items: center;
  flex-shrink: 0;
  height: 20px;
  padding: 0 5px;
  border: 1px solid var(--ccx-kind-color);
  background: color-mix(in srgb, var(--ccx-kind-color) 6%, transparent);
  color: rgb(var(--v-theme-on-surface) / 78%);
  font-size: 11px;
  font-weight: 700;
  letter-spacing: 0.02em;
  line-height: 1;
}

/* Display label (title/userId) */
.display-label {
  min-width: 0;
  flex: 1;
}
.display-label-text {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.main-conversation-detail {
  margin-top: 10px;
  padding: 9px 10px;
  border: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  background: rgb(var(--v-theme-surface) / 72%);
}

.conversation-section-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  color: rgb(var(--v-theme-on-surface) / 64%);
  font-size: 11px;
  font-weight: 800;
  letter-spacing: 0.04em;
  text-transform: uppercase;
}

.main-conversation-turns {
  display: flex;
  flex-direction: column;
  gap: 6px;
  margin-top: 6px;
  min-width: 0;
}

.main-conversation-turn {
  color: rgb(var(--v-theme-on-surface) / 86%);
  font-size: 12px;
  font-weight: 700;
  line-height: 1.55;
  min-width: 0;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  word-break: break-word;
}

.main-conversation-turn--collapsible {
  cursor: pointer;
}

.main-conversation-turn--collapsible:focus-visible {
  outline: 2px solid rgb(var(--v-theme-primary) / 60%);
  outline-offset: 2px;
}

.main-conversation-turn--numbered {
  display: grid;
  grid-template-columns: 18px minmax(0, 1fr);
  gap: 6px;
}

.main-conversation-turn-index {
  color: rgb(var(--v-theme-on-surface) / 42%);
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 11px;
  font-weight: 800;
  line-height: 1.7;
  text-align: right;
}

.main-conversation-turn-text {
  min-width: 0;
}

.main-conversation-turn-ellipsis {
  display: block;
  width: 100%;
  margin: 2px 0;
  padding: 0 6px;
  border: 0;
  background: transparent;
  color: rgb(var(--v-theme-primary));
  cursor: pointer;
  font: inherit;
  font-weight: 900;
  line-height: 1.4;
  text-align: center;
}

.main-conversation-turn-ellipsis:hover {
  background: rgb(var(--v-theme-primary) / 8%);
}

.main-conversation-recap {
  margin-top: 8px;
  padding-top: 8px;
  border-top: 1px dashed rgba(var(--v-border-color), var(--v-border-opacity));
}

.main-conversation-recap-head {
  display: inline-flex;
  align-items: center;
  gap: 5px;
  color: rgb(var(--v-theme-primary));
  font-size: 11px;
  font-weight: 800;
  letter-spacing: 0.04em;
  text-transform: uppercase;
}

.main-conversation-recap-text {
  margin-top: 5px;
  color: rgb(var(--v-theme-on-surface) / 78%);
  font-size: 12px;
  font-weight: 650;
  line-height: 1.5;
  white-space: pre-wrap;
  overflow-wrap: anywhere;
  word-break: break-word;
}

.main-conversation-grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 8px;
  margin-top: 10px;
}

.main-conversation-field {
  min-width: 0;
  padding-top: 6px;
  border-top: 1px dashed rgba(var(--v-border-color), var(--v-border-opacity));
}

.main-conversation-field span,
.main-conversation-field strong {
  display: block;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.main-conversation-field span {
  color: rgb(var(--v-theme-on-surface) / 62%);
  font-size: 11px;
  font-weight: 700;
}

.main-conversation-field strong {
  margin-top: 3px;
  color: rgb(var(--v-theme-on-surface) / 82%);
  font-size: 11px;
  font-weight: 800;
}

.main-routing-section {
  margin-top: 12px;
}

.main-routing-head {
  display: grid;
  grid-template-columns: minmax(0, 1fr) 190px;
  gap: 12px;
  align-items: center;
  margin-bottom: 8px;
}

.main-routing-title {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  color: rgb(var(--v-theme-on-surface) / 62%);
  font-size: 12px;
  white-space: nowrap;
}

.override-duration-select {
  width: 190px;
}

.subagent-expanded-section {
  margin-top: 12px;
  padding-top: 10px;
  border-top: 1px dashed rgba(var(--v-border-color), var(--v-border-opacity));
}

.subagent-list {
  margin-top: 8px;
  border: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  background: rgb(var(--v-theme-surface) / 72%);
}

.subagent-list-head {
  display: flex;
  justify-content: space-between;
  gap: 10px;
  padding: 7px 10px;
  border-bottom: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  color: rgb(var(--v-theme-on-surface) / 62%);
  font-size: 11px;
  font-weight: 700;
}

.subagent-row {
  display: grid;
  grid-template-columns: 8px minmax(0, 1fr) auto;
  gap: 8px;
  align-items: center;
  padding: 7px 10px;
  border-bottom: 1px solid rgba(var(--v-border-color), 0.5);
}

.subagent-row:last-of-type {
  border-bottom: 0;
}

.subagent-dot {
  width: 7px;
  height: 7px;
  border-radius: 999px;
}

.subagent-dot--streaming {
  background: var(--ccx-led-streaming-color);
  animation: ccx-led-pulse 1.4s ease-in-out infinite;
}

.subagent-dot--active {
  background: var(--ccx-status-breaker-half-open-dot-bg);
}

.subagent-dot--idle {
  background: var(--ccx-status-disabled-dot-bg);
}

.subagent-row-main {
  min-width: 0;
}

.subagent-row-title,
.subagent-row-meta {
  display: block;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.subagent-row-title {
  color: rgb(var(--v-theme-on-surface) / 86%);
  font-size: 12px;
  font-weight: 700;
}

.subagent-row-meta {
  color: rgb(var(--v-theme-on-surface) / 48%);
  font-size: 11px;
}

.subagent-more {
  width: 100%;
  padding: 6px 10px;
  color: rgb(var(--v-theme-on-surface) / 58%);
  font-size: 11px;
  text-align: left;
}

.relation-row {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin-top: 8px;
}

.relation-chip {
  display: inline-flex;
  min-height: 22px;
  max-width: 100%;
  align-items: center;
  gap: 4px;
  padding: 2px 6px;
  border: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  background: rgb(var(--v-theme-surface) / 68%);
  color: rgb(var(--v-theme-on-surface) / 68%);
  font-size: 11px;
  font-weight: 700;
  line-height: 1;
  transition: background-color 0.12s ease, border-color 0.12s ease;
}

button.relation-chip {
  cursor: pointer;
}

.relation-chip span {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.relation-chip--parent {
  border-color: rgb(var(--v-theme-info) / 48%);
  background: rgb(var(--v-theme-info) / 10%);
}

.relation-chip--children {
  border-color: rgb(var(--v-theme-warning) / 48%);
  background: rgb(var(--v-theme-warning) / 10%);
}

.relation-chip--parent:hover,
.relation-chip--children:hover {
  background: color-mix(in srgb, var(--ccx-kind-color) 14%, transparent);
  border-color: var(--ccx-kind-color);
}

/* Override alert */
.override-alert {
  border: 2px solid rgb(var(--v-theme-warning)) !important;
  border-radius: 0 !important;
}

.override-alert :deep(.v-alert__content),
.override-alert :deep(.v-btn) {
  color: rgb(var(--v-theme-on-surface) / 82%);
}

.subagent-row :deep(.v-chip__content) {
  font-size: 11px;
}

.font-weight-mono {
  font-family: monospace;
}

/* Raw User ID */
.raw-user-id {
  border-top: 1px dashed rgba(var(--v-border-color), var(--v-border-opacity));
  padding-top: 6px;
  cursor: pointer;
}
.raw-user-id-text {
  overflow: hidden;
  color: rgb(var(--v-theme-on-surface) / 62%);
  text-overflow: ellipsis;
  white-space: nowrap;
  min-width: 0;
}
.raw-user-id .copy-btn {
  flex-shrink: 0;
  opacity: 0.5;
}
.raw-user-id:hover .copy-btn {
  opacity: 1;
}

@media (max-width: 480px) {
  .main-routing-head {
    grid-template-columns: 1fr;
    gap: 8px;
  }

  .override-duration-select {
    width: 100%;
  }
}

</style>
