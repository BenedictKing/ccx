<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { Check, Copy, CornerUpLeft, FileText, GitBranch } from 'lucide-vue-next'
import { useLanguage } from '@/composables/useLanguage'
import type {
  ChannelSequenceEntry,
  ConversationChannelInfo,
  ConversationInfo,
  SequenceOverrideInfo,
} from '@/services/admin-api'
import type { SubagentSummary } from '@/utils/conversation-dashboard'
import { buildConversationTurnMiddlePreview, getConversationTurnFont } from '@/utils/conversation-preview'
import ConversationChannelSequence from './ConversationChannelSequence.vue'

interface ChannelInfo {
  index: number
  name: string
  status: string
  priority?: number
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
  availableChannels: ConversationChannelInfo[]
  expanded: boolean
  nowMs: number
  relatedParentTitle?: string
}>()

const emit = defineEmits<{
  toggleExpand: []
  setOverride: [conversationId: string, sequence: ChannelSequenceEntry[], subagentSequence?: ChannelSequenceEntry[], clearSubagentSequence?: boolean]
  removeOverride: [conversationId: string]
  navigateConversation: [conversationId: string]
  success: [message: string]
  error: [message: string]
}>()

const { t } = useLanguage()
const MAX_VISIBLE = 6
const emptySubagentSummary: SubagentSummary = { total: 0, streaming: 0, active: 0, idle: 0 }

const conversation = computed(() => props.conversation)
const subagents = computed(() => props.subagents ?? [])
const subagentSummary = computed(() => props.subagentSummary ?? emptySubagentSummary)
const hasSubagentActivity = computed(() => props.conversation.hasSubagents || subagents.value.length > 0)
const displaySubagentCount = computed(() => subagentSummary.value.total || props.conversation.subagentCount || subagents.value.length || 1)
const visibleSubagents = computed(() => subagents.value.slice(0, props.expanded ? 12 : 4))
const hasOverride = computed(() => props.override?.hasMainSequence === true)
const kindLabel = computed(() => props.conversation.kind.toUpperCase())

function splitConversationTurns(text: string): string[] {
  const turns = text
    .split(/\s+\/\s+/)
    .map((turn) => turn.trim())
    .filter(Boolean)

  return turns.length > 0 ? turns : [text]
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

const kindStyle = computed(() => {
  switch (props.conversation.kind) {
    case 'messages': return { color: '#a855f7', chip: 'border-purple-500/60 text-purple-500 bg-purple-500/10' }
    case 'chat': return { color: '#3b82f6', chip: 'border-blue-500/60 text-blue-500 bg-blue-500/10' }
    case 'responses': return { color: '#14b8a6', chip: 'border-teal-500/60 text-teal-500 bg-teal-500/10' }
    case 'gemini': return { color: '#f97316', chip: 'border-orange-500/60 text-orange-500 bg-orange-500/10' }
    case 'images': return { color: '#ec4899', chip: 'border-pink-500/60 text-pink-500 bg-pink-500/10' }
    case 'vectors': return { color: '#0ea5e9', chip: 'border-sky-500/60 text-sky-500 bg-sky-500/10' }
    default: return { color: 'var(--color-foreground)', chip: 'border-border text-muted-foreground bg-muted/20' }
  }
})

const displayLabel = computed(() => props.conversation.title || props.conversation.userId)
const mainConversationText = computed(() => props.conversation.lastUserMessage || displayLabel.value)
const mainConversationTurnsRef = ref<HTMLElement | null>(null)
const mainConversationTurnsWidth = ref(0)
const expandedMainConversationTurnIndexes = ref<Set<number>>(new Set())
let mainConversationTurnsObserver: ResizeObserver | null = null
const isSingleMainConversation = computed(() => props.conversation.requestCount <= 1)
const sourceMainConversationTurns = computed(() => {
  if (!isSingleMainConversation.value) return splitConversationTurns(mainConversationText.value)
  return [mainConversationText.value]
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
const tooltipText = computed(() => props.conversation.title || props.conversation.userId)
const childConversationCount = computed(() => props.conversation.childConversationIds?.length ?? 0)
const firstChildConversationId = computed(() => props.conversation.childConversationIds?.[0])
const parentThreadLabel = computed(() => props.conversation.parentThreadId ? shortId(props.conversation.parentThreadId) : '')
const parentConversationTooltip = computed(() => t('cockpit.tooltip.parentConversation', { id: props.relatedParentTitle || props.conversation.parentConversationId || '' }))
const parentThreadTooltip = computed(() => t('cockpit.tooltip.parentThread', { id: props.conversation.parentThreadId || '' }))
const childConversationTooltip = computed(() => t('cockpit.tooltip.childConversation', { id: firstChildConversationId.value || '' }))

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
const requestCountTooltip = computed(() => t('cockpit.tooltip.requests', { count: String(props.conversation.requestCount) }))
const durationTooltip = computed(() => t('cockpit.tooltip.duration', { duration: duration.value }))
const subagentCountTooltip = computed(() => t('cockpit.tooltip.subagents', { count: String(displaySubagentCount.value) }))

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
  if (!props.override?.expiresAt) return '--:--'
  const expires = new Date(props.override.expiresAt).getTime()
  if (!Number.isFinite(expires)) return '--:--'
  const remaining = Math.max(0, expires - props.nowMs)
  const mins = Math.floor(remaining / 60000)
  const secs = Math.floor((remaining % 60000) / 1000)
  return `${mins}:${secs.toString().padStart(2, '0')}`
})

const normalizedAvailableChannels = computed<ChannelInfo[]>(() => {
  return props.availableChannels
    .map(channel => ({
      index: channel.index,
      name: channel.name,
      status: channel.status || 'active',
      priority: channel.priority,
      circuitOpen: channel.circuitOpen,
    }))
    .sort((a, b) => ((a.priority ?? a.index) - (b.priority ?? b.index)) || (a.index - b.index))
})

const fallbackChannels = computed((): ChannelInfo[] => {
  const channels: ChannelInfo[] = []
  const pushUnique = (channel: ChannelInfo) => {
    if (!channels.some(item => item.index === channel.index)) channels.push(channel)
  }

  if (hasOverride.value && props.override?.sequence) {
    for (const entry of props.override.sequence) {
      pushUnique({
        index: entry.channelIndex,
        name: entry.channelName || `Channel ${entry.channelIndex}`,
        status: 'active',
      })
    }
  }

  pushUnique({
    index: props.conversation.currentChannel,
    name: props.conversation.channelName || `Channel ${props.conversation.currentChannel}`,
    status: 'active',
  })

  return channels
})

const channelSequence = computed((): ChannelInfo[] => {
  if (hasOverride.value && props.override?.sequence?.length) {
    return props.override.sequence.map(entry => {
      const channel = normalizedAvailableChannels.value.find(item => item.index === entry.channelIndex)
      return {
        index: entry.channelIndex,
        name: entry.channelName || channel?.name || `Channel ${entry.channelIndex}`,
        status: channel?.status || 'active',
        priority: channel?.priority,
        circuitOpen: channel?.circuitOpen,
      }
    })
  }

  const channels = normalizedAvailableChannels.value.filter(channel => channel.status !== 'disabled')
  return channels.length > 0 ? channels : fallbackChannels.value
})

const currentChannelInfo = computed(() => {
  const existing = channelSequence.value.find(channel => channel.index === props.conversation.currentChannel)
    ?? normalizedAvailableChannels.value.find(channel => channel.index === props.conversation.currentChannel)
  if (existing) return existing
  return {
    index: props.conversation.currentChannel,
    name: props.conversation.channelName || `Channel ${props.conversation.currentChannel}`,
    status: 'active',
  }
})

const nextChannel = computed(() => {
  const candidate = hasOverride.value ? props.override?.sequence?.[0]?.channelIndex : undefined
  return candidate !== undefined && candidate !== props.conversation.currentChannel ? candidate : undefined
})

const nextChannelInfo = computed(() => {
  if (nextChannel.value === undefined) return undefined
  const existing = channelSequence.value.find(channel => channel.index === nextChannel.value)
    ?? normalizedAvailableChannels.value.find(channel => channel.index === nextChannel.value)
  if (existing) return existing
  const entry = hasOverride.value ? props.override?.sequence?.[0] : undefined
  return {
    index: nextChannel.value,
    name: entry?.channelName || `Channel ${nextChannel.value}`,
    status: 'active',
  }
})

const nextChannelCircuitOpen = computed(() => nextChannelInfo.value?.circuitOpen === true)

const visibleChannels = computed(() => {
  const result: ChannelInfo[] = []
  const required = [currentChannelInfo.value, nextChannelInfo.value].filter((channel): channel is ChannelInfo => !!channel)
  const requiredIndexes = new Set(required.map(channel => channel.index))
  const pushUnique = (channel?: ChannelInfo) => {
    if (!channel || result.some(item => item.index === channel.index)) return
    result.push(channel)
  }

  for (const channel of channelSequence.value) {
    if (result.length >= MAX_VISIBLE) break
    pushUnique(channel)
  }

  for (const channel of required) {
    if (result.some(item => item.index === channel.index)) continue
    if (result.length >= MAX_VISIBLE) {
      let removeIndex = result.length - 1
      for (let i = result.length - 1; i >= 0; i--) {
        if (!requiredIndexes.has(result[i].index)) {
          removeIndex = i
          break
        }
      }
      result.splice(removeIndex, 1)
    }
    pushUnique(channel)
  }

  return result
})

const hiddenCount = computed(() => Math.max(0, channelSequence.value.length - visibleChannels.value.length))
const hiddenChannelsTooltip = computed(() => t('cockpit.tooltip.hiddenChannels', { count: String(hiddenCount.value) }))
const moreSubagentsTooltip = computed(() => t('cockpit.tooltip.moreSubagents', { count: String(subagents.value.length - visibleSubagents.value.length) }))

// subagent 渠道序列：优先用 override.subagentSequence，否则 fallback 到主序列
const subagentSequence = computed((): ChannelInfo[] => {
  if (props.override?.subagentSequence?.length) {
    return props.override.subagentSequence.map(entry => {
      const ch = normalizedAvailableChannels.value.find(c => c.index === entry.channelIndex)
      return { index: entry.channelIndex, name: entry.channelName || ch?.name || `Channel ${entry.channelIndex}`, status: ch?.status || 'active', circuitOpen: ch?.circuitOpen }
    })
  }
  return channelSequence.value
})
const hasSubagentOverride = computed(() => !!props.override?.subagentSequence?.length)
const showSubagentSection = computed(() => hasSubagentActivity.value || hasSubagentOverride.value)
const subagentCurrentChannel = computed(() => props.conversation.subagentChannel ?? -1)
const subagentNextChannel = computed(() => {
  const candidate = props.override?.subagentSequence?.[0]?.channelIndex ?? (hasOverride.value ? props.override?.sequence?.[0]?.channelIndex : undefined)
  return candidate !== undefined && candidate !== subagentCurrentChannel.value ? candidate : undefined
})

const subagentNextChannelInfo = computed(() => {
  if (subagentNextChannel.value === undefined) return undefined
  const existing = subagentSequence.value.find(channel => channel.index === subagentNextChannel.value)
    ?? normalizedAvailableChannels.value.find(channel => channel.index === subagentNextChannel.value)
  if (existing) return existing
  const entry = props.override?.subagentSequence?.[0]
  return {
    index: subagentNextChannel.value,
    name: entry?.channelName || `Channel ${subagentNextChannel.value}`,
    status: 'active',
  }
})

const subagentNextChannelCircuitOpen = computed(() => subagentNextChannelInfo.value?.circuitOpen === true)

function subagentStatusClass(status: ConversationInfo['status']): string {
  switch (status) {
    case 'streaming': return 'bg-rose-500'
    case 'active': return 'bg-blue-500'
    case 'idle': return 'bg-muted-foreground'
    default: return 'bg-muted-foreground'
  }
}

function buildSequence(channels: ChannelInfo[]): ChannelSequenceEntry[] {
  return channels.map(channel => ({
    channelIndex: channel.index,
    channelName: channel.name,
  }))
}

function getChannelTooltip(channel: ChannelInfo): string {
  // 该渠道已是 override 首位 → 已固定为此对话优先渠道
  if (hasOverride.value && props.override?.sequence?.[0]?.channelIndex === channel.index) {
    return t('cockpit.tooltip.quickPinned')
  }
  // 暂停或熔断 → 恢复并固定
  if (channel.status === 'suspended' || channel.circuitOpen) {
    return t('cockpit.tooltip.quickResumeAndSetNext')
  }
  // 当前渠道（未固定）→ 点击固定当前渠道
  if (channel.index === props.conversation.currentChannel) {
    return t('cockpit.tooltip.quickCurrentChannel')
  }
  // 其他渠道 → 固定为此对话优先渠道
  return t('cockpit.tooltip.quickSetNext')
}

function channelChipClass(channel: ChannelInfo): string {
  if (channel.index === props.conversation.currentChannel) {
    return 'border-primary bg-primary text-primary-foreground shadow-sm'
  }
  if (channel.index === nextChannel.value) {
    return nextChannelCircuitOpen.value
      ? 'next-channel-chip border-red-500 bg-red-500 text-white'
      : 'next-channel-chip border-emerald-500 bg-emerald-500 text-white'
  }
  return 'border-border bg-background/50 text-foreground hover:border-primary/40 hover:bg-accent/30'
}

function handleQuickOverride(channel: ChannelInfo) {
  // 已固定此渠道（override 首位）→ 再次点击取消固定
  if (hasOverride.value && props.override?.sequence?.[0]?.channelIndex === channel.index) {
    emit('removeOverride', props.conversation.id)
    return
  }
  const rest = channelSequence.value.filter(item => item.index !== channel.index)
  emit('setOverride', props.conversation.id, buildSequence([channel, ...rest]))
}

function handleSubagentMoveToTop(channel: ChannelInfo, currentIndex: number) {
  if (currentIndex === 0) return
  const current = [...subagentSequence.value]
  const [item] = current.splice(currentIndex, 1)
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

function handleMoveToTop(channel: ChannelInfo, currentIndex: number) {
  // 当前渠道：toggle 固定/取消固定（与折叠态逻辑一致）
  if (channel.index === props.conversation.currentChannel) {
    if (hasOverride.value && props.override?.sequence?.[0]?.channelIndex === channel.index) {
      emit('removeOverride', props.conversation.id)
      return
    }
    const rest = channelSequence.value.filter(item => item.index !== channel.index)
    emit('setOverride', props.conversation.id, buildSequence([channel, ...rest]))
    return
  }
  // 非当前渠道：移到首位
  if (currentIndex === 0 && channel.status !== 'suspended' && !channel.circuitOpen) return
  const current = [...channelSequence.value]
  const [item] = current.splice(currentIndex, 1)
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

<template>
  <div
    class="conversation-card border bg-card/85 p-4 text-card-foreground"
    :class="{ 'override-active': hasOverride }"
    :style="{ '--ccx-kind-color': kindStyle.color }"
    role="button"
    tabindex="0"
    :aria-expanded="expanded"
    @click="emit('toggleExpand')"
    @keydown.enter.prevent="emit('toggleExpand')"
    @keydown.space.prevent="emit('toggleExpand')"
  >
    <!-- Row 1: LED + Kind + Title/User + Stats -->
    <div class="mb-3 flex min-w-0 items-center gap-2">
      <Tooltip>
        <TooltipTrigger as-child>
          <span class="status-led" :class="`status-led--${conversation.status}`" />
        </TooltipTrigger>
        <TooltipContent side="top">{{ statusTooltip }}</TooltipContent>
      </Tooltip>
      <Tooltip>
        <TooltipTrigger as-child>
          <span class="kind-chip border px-1 py-0.5 text-[9px] font-bold tracking-[0.08em]" :class="kindStyle.chip">
            {{ kindLabel }}
          </span>
        </TooltipTrigger>
        <TooltipContent side="top">{{ kindTooltip }}</TooltipContent>
      </Tooltip>
      <span class="display-label min-w-0 flex-1 font-mono text-xs text-muted-foreground" :title="tooltipText">
        <span class="display-label-text" :class="{ 'display-label-text--expanded': expanded }">{{ displayLabel }}</span>
      </span>
      <Tooltip>
        <TooltipTrigger as-child>
          <span class="shrink-0 text-xs text-muted-foreground">{{ conversation.requestCount }}x</span>
        </TooltipTrigger>
        <TooltipContent side="top">{{ requestCountTooltip }}</TooltipContent>
      </Tooltip>
      <Tooltip>
        <TooltipTrigger as-child>
          <span class="shrink-0 text-xs text-muted-foreground">{{ duration }}</span>
        </TooltipTrigger>
        <TooltipContent side="top">{{ durationTooltip }}</TooltipContent>
      </Tooltip>
      <Tooltip v-if="hasSubagentActivity">
        <TooltipTrigger as-child>
          <span
            class="inline-flex shrink-0 items-center border border-amber-500/50 bg-amber-500/10 px-1 py-0.5 text-[10px] font-medium text-amber-500"
          >
            SA {{ displaySubagentCount }}
          </span>
        </TooltipTrigger>
        <TooltipContent side="top">{{ subagentCountTooltip }}</TooltipContent>
      </Tooltip>
    </div>

    <div
      v-if="conversation.parentConversationId || conversation.parentThreadId || (!expanded && childConversationCount > 0)"
      class="relation-row mb-3 flex flex-wrap items-center gap-1.5"
      @click.stop
    >
      <Tooltip
        v-if="conversation.parentConversationId"
        >
        <TooltipTrigger as-child>
          <button
            type="button"
            class="relation-chip border border-sky-500/50 bg-sky-500/10 text-sky-600 hover:bg-sky-500/20 dark:text-sky-400"
            @click="navigateConversation(conversation.parentConversationId)"
          >
            <CornerUpLeft class="h-3 w-3" />
            <span>{{ t('cockpit.relation.parent') }}</span>
          </button>
        </TooltipTrigger>
        <TooltipContent side="top">{{ parentConversationTooltip }}</TooltipContent>
      </Tooltip>
      <Tooltip
        v-else-if="conversation.parentThreadId"
      >
        <TooltipTrigger as-child>
          <span
            class="relation-chip border border-border bg-muted/30 text-muted-foreground"
          >
            <CornerUpLeft class="h-3 w-3" />
            <span>{{ t('cockpit.relation.parentThread', { id: parentThreadLabel }) }}</span>
          </span>
        </TooltipTrigger>
        <TooltipContent side="top">{{ parentThreadTooltip }}</TooltipContent>
      </Tooltip>

      <Tooltip
        v-if="!expanded && childConversationCount > 0 && firstChildConversationId"
      >
        <TooltipTrigger as-child>
          <button
            type="button"
            class="relation-chip border border-amber-500/50 bg-amber-500/10 text-amber-600 hover:bg-amber-500/20 dark:text-amber-400"
            @click="navigateConversation(firstChildConversationId)"
          >
            <GitBranch class="h-3 w-3" />
            <span>{{ t('cockpit.relation.children', { count: String(childConversationCount) }) }}</span>
          </button>
        </TooltipTrigger>
        <TooltipContent side="top">{{ childConversationTooltip }}</TooltipContent>
      </Tooltip>
    </div>

    <div v-if="expanded" class="main-conversation-detail mb-3 border border-border bg-background/60 p-2.5">
      <div class="conversation-section-head flex items-center gap-2 text-[10px] font-bold uppercase tracking-[0.04em] text-muted-foreground">
        <span>{{ t('cockpit.mainConversation') }}</span>
      </div>
      <div ref="mainConversationTurnsRef" class="main-conversation-turns mt-1.5 flex flex-col gap-1.5">
        <div
          v-for="(turn, index) in mainConversationTurns"
          :key="`${index}-${turn.fullText}`"
          :class="[
            'main-conversation-turn text-xs font-semibold leading-relaxed text-foreground',
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
      <div v-if="conversation.lastRecap" class="main-conversation-recap mt-2 border-t border-dashed border-border pt-2">
        <div class="inline-flex items-center gap-1.5 text-[10px] font-bold uppercase tracking-[0.04em] text-primary">
          <FileText class="h-3.5 w-3.5" />
          <span>{{ t('cockpit.recap') }}</span>
        </div>
        <div class="mt-1.5 whitespace-pre-wrap break-words text-xs font-semibold leading-relaxed text-foreground/80">
          {{ conversation.lastRecap }}
        </div>
      </div>
      <div class="mt-2 grid grid-cols-2 gap-2">
        <div v-for="row in mainDetailRows" :key="row.label" class="min-w-0 border-t border-dashed border-border pt-1.5">
          <span class="block truncate text-[10px] font-semibold text-muted-foreground">{{ row.label }}</span>
          <strong class="mt-0.5 block truncate text-[11px] font-bold text-foreground/85">{{ row.value }}</strong>
        </div>
      </div>
    </div>

    <!-- Row 2: Model + Channel chips (collapsed) -->
    <div v-if="!expanded" class="flex flex-wrap items-center gap-2">
      <span class="mr-2 min-w-0 max-w-full truncate text-sm font-medium">{{ conversation.lastModel }}</span>
      <Tooltip
        v-for="channel in visibleChannels"
        :key="channel.index"
      >
        <TooltipTrigger as-child>
          <button
            type="button"
            class="channel-chip inline-flex max-w-[160px] items-center gap-1 truncate border px-2 py-0.5 text-[10px] font-medium transition-colors"
            :class="channelChipClass(channel)"
            @click.stop="handleQuickOverride(channel)"
          >
            <span class="truncate">{{ channel.name }}</span>
            <Check v-if="channel.index === conversation.currentChannel" class="h-2.5 w-2.5 shrink-0" />
            <span v-else-if="channel.index === nextChannel" class="next-label shrink-0">
              | {{ nextChannelCircuitOpen ? 'TRIPPED' : 'NEXT' }}
            </span>
          </button>
        </TooltipTrigger>
        <TooltipContent side="top">{{ getChannelTooltip(channel) }}</TooltipContent>
      </Tooltip>
      <Tooltip
        v-if="hiddenCount > 0"
      >
        <TooltipTrigger as-child>
          <button
            type="button"
            class="px-1.5 py-0.5 text-[10px] text-muted-foreground hover:text-foreground"
            @click.stop="emit('toggleExpand')"
          >
            +{{ hiddenCount }}
          </button>
        </TooltipTrigger>
        <TooltipContent side="top">{{ hiddenChannelsTooltip }}</TooltipContent>
      </Tooltip>
    </div>

    <!-- Expanded: Override alert -->
    <div v-if="expanded && hasOverride" class="override-alert mt-3 border border-amber-500/70 bg-amber-500/10 p-2">
      <div class="flex items-center gap-2">
        <span v-if="override?.isPerpetual" class="text-xs text-amber-600 dark:text-amber-400">
          {{ t('cockpit.overrideActivePerpetual') }}
        </span>
        <span v-else class="text-xs text-amber-600 dark:text-amber-400">
          {{ t('cockpit.overrideActive', { time: remainingTime }) }}
        </span>
        <div class="flex-1" />
        <Tooltip>
          <TooltipTrigger as-child>
            <Button variant="ghost" size="sm" class="h-6 px-2 text-xs" @click.stop="emit('removeOverride', conversation.id)">
              {{ t('cockpit.restoreDefault') }}
            </Button>
          </TooltipTrigger>
          <TooltipContent side="top">{{ t('cockpit.tooltip.restoreDefault') }}</TooltipContent>
        </Tooltip>
      </div>
    </div>

    <!-- Expanded: Full channel sequence -->
    <div v-if="expanded" class="main-routing-section mt-3">
      <div class="mb-1 text-xs text-muted-foreground">{{ t('cockpit.mainRouting') }} · {{ conversation.lastModel }}</div>
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

    <div v-if="expanded && showSubagentSection" class="subagent-expanded-section mt-3 border-t border-dashed border-border pt-2" @click.stop>
      <div v-if="subagents.length > 0" class="subagent-list border border-border bg-background/60">
        <div class="flex items-center justify-between gap-2 border-b border-border px-2 py-1.5 text-[10px] font-semibold text-muted-foreground">
          <span>{{ t('cockpit.board.subagents') }}</span>
          <span>{{ subagents.length }}</span>
        </div>
        <div
          v-for="agent in visibleSubagents"
          :key="agent.id"
          class="grid grid-cols-[8px_minmax(0,1fr)_auto] items-center gap-2 border-b border-border/60 px-2 py-1.5 last:border-b-0"
        >
          <span class="h-2 w-2 rounded-full" :class="subagentStatusClass(agent.status)" />
          <div class="min-w-0">
            <div class="truncate text-xs font-semibold text-foreground">{{ agent.title || agent.userId }}</div>
            <div class="truncate text-[10px] text-muted-foreground">{{ agent.lastModel }} · {{ agent.channelName || `Channel ${agent.currentChannel}` }}</div>
          </div>
          <span class="border border-border bg-muted/30 px-1.5 py-0.5 text-[10px] font-semibold text-muted-foreground">{{ agent.status }}</span>
        </div>
        <Tooltip
          v-if="subagents.length > visibleSubagents.length"
        >
          <TooltipTrigger as-child>
            <button
              type="button"
              class="w-full px-2 py-1 text-left text-[10px] text-muted-foreground hover:text-foreground"
              @click.stop="emit('toggleExpand')"
            >
              +{{ subagents.length - visibleSubagents.length }} more
            </button>
          </TooltipTrigger>
          <TooltipContent side="top">{{ moreSubagentsTooltip }}</TooltipContent>
        </Tooltip>
      </div>

      <!-- Subagent Routing：为主对话与 subagent 分别指定渠道 -->
      <div class="subagent-routing mt-3">
        <div class="mb-1 flex items-center">
          <span class="text-xs text-muted-foreground">{{ t('cockpit.subagentRouting') }}</span>
          <span v-if="hasSubagentOverride" class="ml-2 text-xs text-amber-500">[{{ t('cockpit.subagentOverride') }}]</span>
          <span v-else class="ml-2 text-xs text-muted-foreground">[{{ t('cockpit.subagentFollowMain') }}]</span>
          <span class="flex-1" />
          <Tooltip v-if="hasSubagentOverride">
            <TooltipTrigger as-child>
              <Button variant="ghost" size="sm" class="h-6 px-2 text-xs" @click.stop="handleClearSubagentOverride">
                {{ t('cockpit.subagentClearOverride') }}
              </Button>
            </TooltipTrigger>
            <TooltipContent side="top">{{ t('cockpit.tooltip.clearSubagentOverride') }}</TooltipContent>
          </Tooltip>
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

    <!-- Row 3: Raw User ID -->
    <div v-if="conversation.rawUserId" class="raw-user-id mt-2 flex items-center gap-1 border-t border-dashed border-border pt-2">
      <Tooltip>
        <TooltipTrigger as-child>
          <button
            type="button"
            class="raw-user-id-text min-w-0 flex-1 truncate text-left font-mono text-xs text-muted-foreground"
            @click.stop="copyRawUserId"
          >
            {{ conversation.rawUserId }}
          </button>
        </TooltipTrigger>
        <TooltipContent side="top">{{ t('cockpit.copyRawUserId') }}</TooltipContent>
      </Tooltip>
      <Tooltip>
        <TooltipTrigger as-child>
          <Button variant="ghost" size="icon-sm" class="copy-btn h-6 w-6" :aria-label="t('cockpit.copyRawUserId')" @click.stop="copyRawUserId">
            <Copy class="h-3 w-3" />
          </Button>
        </TooltipTrigger>
        <TooltipContent side="top">
          {{ t('cockpit.copyRawUserId') }}
        </TooltipContent>
      </Tooltip>
    </div>
  </div>
</template>

<style scoped>
.conversation-card {
  cursor: pointer;
  position: relative;
  border-width: 2px;
  border-color: color-mix(in srgb, var(--color-foreground) 55%, transparent);
  border-radius: 0;
  box-shadow: 4px 4px 0 0 color-mix(in srgb, var(--color-foreground) 80%, transparent);
  background:
    radial-gradient(circle, color-mix(in srgb, var(--color-foreground) 12%, transparent) 1px, transparent 1px) 0 0 / 16px 16px,
    var(--color-card);
  transition: all 0.1s ease;
}

.conversation-card::before,
.conversation-card::after {
  content: '';
  position: absolute;
  width: 14px;
  height: 14px;
  pointer-events: none;
  opacity: 0.45;
}

.conversation-card::before {
  left: 6px;
  top: 6px;
  border-left: 2px solid var(--ccx-kind-color);
  border-top: 2px solid var(--ccx-kind-color);
}

.conversation-card::after {
  right: 6px;
  bottom: 6px;
  border-bottom: 2px solid var(--ccx-kind-color);
  border-right: 2px solid var(--ccx-kind-color);
}

.conversation-card:hover {
  border-color: var(--ccx-kind-color);
  box-shadow: 5px 5px 0 0 var(--ccx-kind-color);
  transform: translate(-1px, -1px);
}

.conversation-card:active {
  box-shadow: 2px 2px 0 0 color-mix(in srgb, var(--color-foreground) 80%, transparent);
  transform: translate(1px, 1px);
}

.conversation-card.override-active {
  border-color: var(--color-warning);
  box-shadow: 4px 4px 0 0 var(--color-warning);
}

.conversation-card.override-active:hover {
  border-color: var(--color-warning);
  box-shadow: 5px 5px 0 0 var(--color-warning);
}

.status-led {
  display: inline-block;
  width: 8px;
  height: 8px;
  flex-shrink: 0;
  border-radius: 999px;
}

.status-led--streaming {
  background: #22d3ee;
  animation: ccx-led-pulse 1.4s ease-in-out infinite;
}

.status-led--active {
  background: #10b981;
  box-shadow: 0 0 4px 1px rgba(16, 185, 129, 0.45);
}

.status-led--idle {
  background: var(--color-muted-foreground);
}

.kind-chip,
.channel-chip,
.sequence-badge {
  border-radius: 0;
}

.display-label-text {
  display: block;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}


.main-conversation-detail,
.subagent-summary,
.subagent-list {
  border-radius: 0;
}

.main-conversation-turn {
  overflow-wrap: anywhere;
  white-space: pre-wrap;
  word-break: break-word;
}

.main-conversation-turn--collapsible {
  cursor: pointer;
}

.main-conversation-turn--collapsible:focus-visible {
  outline: 2px solid color-mix(in oklab, var(--color-primary) 60%, transparent);
  outline-offset: 2px;
}

.main-conversation-turn--numbered {
  display: grid;
  grid-template-columns: 18px minmax(0, 1fr);
  gap: 6px;
}

.main-conversation-turn-index {
  color: var(--color-muted-foreground);
  font-family: ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace;
  font-size: 11px;
  font-weight: 800;
  line-height: 1.7;
  opacity: 0.7;
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
  color: var(--color-primary);
  cursor: pointer;
  font: inherit;
  font-weight: 900;
  line-height: 1.4;
  text-align: center;
}

.main-conversation-turn-ellipsis:hover {
  background: color-mix(in oklab, var(--color-primary) 8%, transparent);
}

.main-conversation-turns {
  min-width: 0;
}

.next-label {
  display: inline-block;
  margin-left: 4px;
  font-size: 9px;
  font-weight: 800;
  letter-spacing: 0.05em;
}

.next-channel-chip {
  animation: ccx-breathe 2s ease-in-out infinite;
}

.relation-chip {
  display: inline-flex;
  min-height: 22px;
  max-width: 100%;
  align-items: center;
  gap: 4px;
  padding: 2px 6px;
  font-size: 10px;
  font-weight: 700;
  line-height: 1;
  transition: background-color 0.12s ease, border-color 0.12s ease;
}

.override-alert {
  border-radius: 0;
}

.raw-user-id {
  opacity: 0.65;
}

.raw-user-id:hover {
  opacity: 0.95;
}

.copy-btn {
  opacity: 0.55;
}

.raw-user-id:hover .copy-btn {
  opacity: 1;
}

@keyframes ccx-led-pulse {
  0%, 100% { opacity: 1; box-shadow: 0 0 5px rgba(34, 211, 238, 0.55); }
  50% { opacity: 0.4; box-shadow: 0 0 12px rgba(34, 211, 238, 0.9); }
}

@keyframes ccx-breathe {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.55; }
}

</style>
