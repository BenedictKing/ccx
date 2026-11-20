<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { Alert } from '@/components/ui/alert'
import { Input } from '@/components/ui/input'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Skeleton } from '@/components/ui/skeleton'
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from '@/components/ui/tooltip'
import { MessageSquare, Search, X } from 'lucide-vue-next'
import { useAdminApi } from '@/composables/useAdminApi'
import { useConversations } from '@/composables/useConversations'
import { useDesktopActivity } from '@/composables/useDesktopActivity'
import { useLanguage } from '@/composables/useLanguage'
import { useStatus } from '@/composables/useStatus'
import ConversationCard from './ConversationCard.vue'
import type { ChannelSequenceEntry, ConversationInfo } from '@/services/admin-api'
import { getChannelTypeApi, type ManagedChannelType } from '@/utils/channel-type-api'
import { buildConversationBoardItems, filterConversationBoardItems, type BoardColumnKey, type ConversationBoardItem } from '@/utils/conversation-dashboard'

const api = useAdminApi()
const { status } = useStatus()
const { t, tf } = useLanguage()
const {
  conversations,
  channelsByKind,
  overrides,
  loading,
  error,
  fetchConversations,
  setOverride,
  removeOverride,
} = useConversations()
const { isConsoleConversationsActive } = useDesktopActivity()

const kindFilter = ref('')
const searchQuery = ref('')
const overrideDuration = ref('1800')
const nowMs = ref(Date.now())
const expandedCards = ref(new Set<string>())
const pinnedConversationOrder = ref<string[]>([])
const notice = ref<{ variant: 'success' | 'destructive'; message: string } | null>(null)
const activatedRoutingKinds = ref(new Set<string>())
const settingsReady = ref(false)
let noticeTimer: ReturnType<typeof setTimeout> | undefined
let refreshTimer: ReturnType<typeof setInterval> | undefined
let conversationTimer: ReturnType<typeof setInterval> | undefined
const cardElements = new Map<string, HTMLElement>()

const boardMeta = computed<Array<{ key: BoardColumnKey; label: string; hint: string; color: string }>>(() => [
  { key: 'working', label: tf('cockpit.board.working', 'Working'), hint: tf('cockpit.board.workingHint', 'Root conversations with active work'), color: '#6366f1' },
  { key: 'idle', label: tf('cockpit.board.idle', 'Idle'), hint: tf('cockpit.board.idleHint', 'Idle conversations'), color: '#10b981' },
])

const kindFilterOptions = [
  { label: 'ALL', value: '' },
  { label: 'MESSAGES', value: 'messages', class: 'border-purple-500/50 text-purple-500 data-[active=true]:bg-purple-500/10' },
  { label: 'CHAT', value: 'chat', class: 'border-blue-500/50 text-blue-500 data-[active=true]:bg-blue-500/10' },
  { label: 'IMAGES', value: 'images', class: 'border-pink-500/50 text-pink-500 data-[active=true]:bg-pink-500/10' },
  { label: 'VECTORS', value: 'vectors', class: 'border-sky-500/50 text-sky-500 data-[active=true]:bg-sky-500/10' },
  { label: 'RESPONSES', value: 'responses', class: 'border-teal-500/50 text-teal-500 data-[active=true]:bg-teal-500/10' },
  { label: 'GEMINI', value: 'gemini', class: 'border-orange-500/50 text-orange-500 data-[active=true]:bg-orange-500/10' },
]

const durationOptions = computed(() => [
  { label: t('cockpit.durationDefault'), value: '1800' },
  { label: t('cockpit.duration15min'), value: '900' },
  { label: t('cockpit.duration1hour'), value: '3600' },
  { label: t('cockpit.duration2hours'), value: '7200' },
  { label: t('cockpit.duration4hours'), value: '14400' },
  { label: t('cockpit.duration8hours'), value: '28800' },
  { label: t('cockpit.duration12hours'), value: '43200' },
  { label: t('cockpit.duration24hours'), value: '86400' },
  { label: t('cockpit.durationNever'), value: '-1' },
])

const sortedConversations = computed(() => {
  if (expandedCards.value.size > 0) {
    return getPinnedOrderedConversations(conversations.value)
  }
  return sortConversationsByLastActive(conversations.value)
})

const boardItems = computed(() => buildConversationBoardItems(sortedConversations.value))

const visibleBoardItems = computed(() => {
  return filterConversationBoardItems(boardItems.value, kindFilter.value, searchQuery.value)
})

const shouldRefresh = computed(() => status.value.running)

const boardData = computed(() => {
  const buckets = buildColumnBuckets(visibleBoardItems.value)
  return boardMeta.value.map(item => ({
    ...item,
    items: buckets[item.key],
  }))
})

function buildColumnBuckets(items: ConversationBoardItem[]): Record<BoardColumnKey, ConversationBoardItem[]> {
  return items.reduce<Record<BoardColumnKey, ConversationBoardItem[]>>((buckets, item) => {
    buckets[item.aggregateStatus].push(item)
    return buckets
  }, { working: [], idle: [] })
}

function getConversationTime(conversation: ConversationInfo) {
  return new Date(conversation.lastActiveAt).getTime()
}

function sortConversationsByLastActive(items: ConversationInfo[]) {
  return [...items].sort((a, b) => getConversationTime(b) - getConversationTime(a))
}

function getPinnedOrderedConversations(items: ConversationInfo[]) {
  const byID = new Map(items.map(item => [item.id, item]))
  const pinned = [...expandedCards.value]
    .filter(id => byID.has(id))
    .map(id => byID.get(id)!)
  const seen = new Set(pinned.map(item => item.id))
  const fresh = sortConversationsByLastActive(items.filter(item => !seen.has(item.id)))
  return [...pinned, ...fresh]
}

function applyConversationOrder(items: ConversationInfo[]) {
  const itemIDs = new Set(items.map(item => item.id))
  const expanded = [...expandedCards.value].filter(id => itemIDs.has(id))
  if (expanded.length !== expandedCards.value.size) {
    expandedCards.value = new Set(expanded)
  }

  if (expanded.length === 0) {
    const sorted = sortConversationsByLastActive(items)
    pinnedConversationOrder.value = sorted.map(item => item.id)
    return sorted
  }

  const ordered = getPinnedOrderedConversations(items)
  pinnedConversationOrder.value = ordered.map(item => item.id)
  return ordered
}

function getChannelsForKind(kind: string) {
  return channelsByKind.value[kind] || []
}

function getConversationTitle(id?: string) {
  if (!id) return ''
  const conversation = conversations.value.find(item => item.id === id)
  return conversation?.title || conversation?.userId || id
}

function overrideDurationAsNumber(): number {
  return Number(overrideDuration.value)
}

watch(expandedCards, expanded => {
  if (expanded.size === 0) {
    conversations.value = applyConversationOrder(conversations.value)
  }
})

async function loadSettings() {
  try {
    const data = await api.get<{ overrideTtlMinutes: number }>('/api/conversations/settings')
    if (data.overrideTtlMinutes !== 0) {
      overrideDuration.value = data.overrideTtlMinutes === -1 ? '-1' : String(data.overrideTtlMinutes * 60)
    }
  } catch (e) {
    console.error('[ConversationDashboard] 加载设置失败:', e)
  }
}

function showNotice(variant: 'success' | 'destructive', message: string) {
  notice.value = { variant, message }
  if (noticeTimer) window.clearTimeout(noticeTimer)
  noticeTimer = window.setTimeout(() => {
    if (notice.value?.message === message) notice.value = null
  }, 2400)
}

async function refreshConversations() {
  if (!shouldRefresh.value || !isConsoleConversationsActive.value) return
  await fetchConversations()
  conversations.value = applyConversationOrder(conversations.value)
}

async function handleSetOverride(
  conversationId: string,
  sequence: ChannelSequenceEntry[],
) {
  try {
    const conversation = conversations.value.find(item => item.id === conversationId)
    const target = sequence[0]
    if (conversation && target) {
      const channelType = conversation.kind as ManagedChannelType
      const channel = channelsByKind.value[channelType]?.find(item => item.index === target.channelIndex)
      if (channel?.status === 'suspended' || channel?.circuitOpen) {
        const typeApi = getChannelTypeApi(channelType)
        try {
          await typeApi.resume(target.channelIndex)
        } catch {
          // 幂等
        }
        await typeApi.setStatus(target.channelIndex, 'active')
      }
    }
    await setOverride(conversationId, sequence, overrideDurationAsNumber())
    conversations.value = applyConversationOrder(conversations.value)
  } catch (e) {
    showNotice('destructive', e instanceof Error ? e.message : String(e))
  }
}

async function handleRemoveOverride(conversationId: string) {
  try {
    await removeOverride(conversationId)
    conversations.value = applyConversationOrder(conversations.value)
  } catch (e) {
    showNotice('destructive', e instanceof Error ? e.message : String(e))
  }
}

function handleSuccess(message: string) {
  showNotice('success', message)
}

function handleError(message: string) {
  showNotice('destructive', message)
}

function toggleExpand(id: string) {
  const next = new Set(expandedCards.value)
  if (next.has(id)) {
    next.delete(id)
  } else {
    if (expandedCards.value.size === 0) {
      conversations.value = applyConversationOrder(conversations.value)
    }
    next.add(id)
  }
  expandedCards.value = next
}

function setConversationCardRef(id: string, el: Element | null) {
  if (el instanceof HTMLElement) {
    cardElements.set(id, el)
    return
  }
  cardElements.delete(id)
}

async function handleNavigateConversation(id: string) {
  if (!id) return

  const target = conversations.value.find(item => item.id === id)
  if (!target) {
    showNotice('destructive', `未找到关联对话 ${id.slice(0, 8)}`)
    return
  }
  pinnedConversationOrder.value = [
    id,
    ...pinnedConversationOrder.value.filter(itemID => itemID !== id),
  ]
  conversations.value = applyConversationOrder(conversations.value)

  kindFilter.value = ''
  searchQuery.value = ''
  const next = new Set(expandedCards.value)
  next.add(id)
  expandedCards.value = next

  await nextTick()
  const el = cardElements.get(id)
  if (!el) return

  el.scrollIntoView({ behavior: 'smooth', block: 'center' })
  el.classList.add('conversation-card-target')
  window.setTimeout(() => {
    el.classList.remove('conversation-card-target')
  }, 1800)
}

function syncClockTimer() {
  if (refreshTimer) {
    window.clearInterval(refreshTimer)
    refreshTimer = undefined
  }
  if (!isConsoleConversationsActive.value) return
  nowMs.value = Date.now()
  refreshTimer = window.setInterval(() => {
    nowMs.value = Date.now()
  }, 1000)
}

function syncConversationTimer() {
  if (conversationTimer) {
    window.clearInterval(conversationTimer)
    conversationTimer = undefined
  }
  if (!shouldRefresh.value || !isConsoleConversationsActive.value) return
  conversationTimer = window.setInterval(() => {
    void refreshConversations()
  }, 3000)
}

async function loadRoutingStates() {
  const kinds = ['messages', 'chat', 'responses', 'gemini'] as const
  const states = await Promise.all(kinds.map(async kind => {
    try {
      const view = await api.get<{ activated: boolean }>(`/api/${kind}/channels/subagent-routing`)
      return [kind, view.activated] as const
    } catch {
      return [kind, false] as const
    }
  }))
  activatedRoutingKinds.value = new Set(states.filter(([, active]) => active).map(([kind]) => kind))
}

onMounted(async () => {
  await Promise.all([loadSettings(), loadRoutingStates()])
  settingsReady.value = true
  syncClockTimer()
  syncConversationTimer()
  void refreshConversations()
})

watch([shouldRefresh, isConsoleConversationsActive], () => {
  syncConversationTimer()
  syncClockTimer()
  if (shouldRefresh.value && isConsoleConversationsActive.value) {
    void refreshConversations()
  }
})

watch(overrideDuration, async value => {
  if (!settingsReady.value) return
  const seconds = Number(value)
  if (Number.isNaN(seconds) || seconds === 0) return
  try {
    const minutes = seconds === -1 ? -1 : Math.round(seconds / 60)
    await api.put('/api/conversations/settings', { overrideTtlMinutes: minutes })
  } catch (e) {
    showNotice('destructive', e instanceof Error ? e.message : String(e))
  }
})

onBeforeUnmount(() => {
  if (refreshTimer) window.clearInterval(refreshTimer)
  if (conversationTimer) window.clearInterval(conversationTimer)
  if (noticeTimer) window.clearTimeout(noticeTimer)
})
</script>

<template>
  <TooltipProvider>
  <div class="mx-auto flex w-full max-w-[1680px] flex-col gap-4">
    <Alert v-if="error" variant="destructive">
      <p class="text-sm">{{ error }}</p>
    </Alert>

    <Alert v-if="notice" :variant="notice.variant">
      <p class="text-sm">{{ notice.message }}</p>
    </Alert>

    <div class="cockpit-toolbar flex flex-wrap items-center gap-2">
      <div class="cockpit-filter flex flex-wrap items-center gap-1.5">
        <button
          v-for="option in kindFilterOptions"
          :key="option.value"
          type="button"
          class="cockpit-filter-button border px-2.5 py-1 text-[10px] font-bold tracking-[0.08em] text-muted-foreground transition-colors hover:bg-accent/40"
          :class="[option.class, { 'border-primary bg-primary/10 text-primary': kindFilter === option.value }]"
          :data-active="kindFilter === option.value"
          @click="kindFilter = option.value"
        >
          {{ option.label }}
        </button>
      </div>

      <div class="cockpit-toolbar-spacer min-w-4 flex-1" />

      <div class="cockpit-search relative w-full min-w-[160px] sm:w-60 lg:w-64">
        <Search class="absolute left-3 top-1/2 h-4 w-4 -translate-y-1/2 text-muted-foreground" />
        <Input
          v-model="searchQuery"
          :placeholder="t('cockpit.searchPlaceholder')"
          class="h-9 pl-9 pr-8"
        />
        <button
          v-if="searchQuery"
          type="button"
          class="absolute right-2 top-1/2 inline-flex h-5 w-5 -translate-y-1/2 items-center justify-center rounded text-muted-foreground transition-colors hover:bg-accent hover:text-foreground"
          :aria-label="t('common.clearSearch')"
          :title="t('common.clearSearch')"
          @click="searchQuery = ''"
        >
          <X class="h-3.5 w-3.5" />
        </button>
      </div>

      <Select v-model="overrideDuration">
        <SelectTrigger class="cockpit-duration-select h-9 w-[160px] shrink-0">
          <SelectValue :placeholder="t('cockpit.overrideDuration')" />
        </SelectTrigger>
        <SelectContent>
          <SelectItem v-for="opt in durationOptions" :key="opt.value" :value="opt.value">
            {{ opt.label }}
          </SelectItem>
        </SelectContent>
      </Select>

    </div>

    <div v-if="loading && !conversations.length" class="grid gap-3 md:grid-cols-2 xl:grid-cols-3">
      <Skeleton v-for="i in 8" :key="i" class="h-32 w-full rounded-none" />
    </div>

    <div v-else-if="!conversations.length" class="border border-border bg-card/60 px-6 py-12 text-center">
      <MessageSquare class="mx-auto h-12 w-12 text-muted-foreground/70" />
      <div class="mt-4 text-sm text-muted-foreground">
        {{ t('cockpit.empty') }}
      </div>
    </div>

    <template v-else>
      <div v-if="!visibleBoardItems.length" class="border border-border bg-card/60 px-6 py-8 text-center text-sm text-muted-foreground">
        {{ t('cockpit.noMatches') }}
      </div>

      <div v-else class="grid gap-3 xl:grid-cols-[minmax(0,1.35fr)_minmax(320px,1fr)]">
        <section
          v-for="column in boardData"
          :key="column.key"
          :data-testid="`cockpit-column-${column.key}`"
          class="min-w-0 border border-border bg-card/40"
        >
          <div class="flex items-center justify-between gap-3 border-b border-border px-3 py-2.5">
            <div class="flex items-center gap-2 min-w-0">
              <span class="h-2.5 w-2.5 rounded-full" :style="{ background: column.color }" />
              <div class="min-w-0">
                <div class="text-xs font-semibold uppercase tracking-wide text-foreground">{{ column.label }}</div>
                <div class="text-[10px] text-muted-foreground">{{ column.hint }}</div>
              </div>
            </div>
            <Tooltip>
              <TooltipTrigger as-child>
                <span class="text-xs font-semibold text-muted-foreground">{{ column.items.length }}</span>
              </TooltipTrigger>
              <TooltipContent side="top">
                {{ t('cockpit.tooltip.columnCount', { column: column.label, count: String(column.items.length) }) }}
              </TooltipContent>
            </Tooltip>
          </div>

          <div class="space-y-3 p-3">
            <div v-if="!column.items.length" class="border border-dashed border-border px-4 py-10 text-center text-xs text-muted-foreground">
              --
            </div>

            <div
              v-for="item in column.items"
              :key="item.conversation.id"
              :ref="el => setConversationCardRef(item.conversation.id, el as Element | null)"
            >
              <ConversationCard
                :conversation="item.conversation"
                :subagents="item.subagents"
                :subagent-summary="item.subagentSummary"
                :override="overrides[item.conversation.id]"
                :routing-active="activatedRoutingKinds.has(item.conversation.kind)"
                :available-channels="getChannelsForKind(item.conversation.kind)"
                :expanded="expandedCards.has(item.conversation.id)"
                :now-ms="nowMs"
                :related-parent-title="getConversationTitle(item.conversation.parentConversationId)"
                @toggle-expand="toggleExpand(item.conversation.id)"
                @set-override="handleSetOverride"
                @remove-override="handleRemoveOverride"
                @navigate-conversation="handleNavigateConversation"
                @success="handleSuccess"
                @error="handleError"
              />
            </div>
          </div>
        </section>
      </div>
    </template>
  </div>
  </TooltipProvider>
</template>

<style scoped>
.cockpit-search {
  flex-shrink: 1;
}

@media (max-width: 1024px) {
  .cockpit-toolbar {
    gap: 6px;
  }

  .cockpit-filter {
    gap: 4px;
  }

  .cockpit-filter-button {
    padding-inline: 0.5rem;
    letter-spacing: 0.04em;
  }

  .cockpit-toolbar-spacer {
    min-width: 0;
  }

  .cockpit-search {
    width: 13rem;
    min-width: 9rem;
  }

  .cockpit-duration-select {
    width: 9.25rem;
  }
}

:deep(.conversation-card-target .conversation-card) {
  border-color: var(--color-primary);
  box-shadow: 6px 6px 0 0 var(--color-primary);
}
</style>
