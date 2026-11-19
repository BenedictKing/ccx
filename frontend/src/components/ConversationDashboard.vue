<template>
  <div class="cockpit-board-page">
    <div class="cockpit-toolbar">
      <div class="cockpit-summary">
        <span class="cockpit-summary-item">
          <span class="cockpit-column-dot cockpit-column-dot--working"></span>
          <strong>{{ boardSummary.working }}</strong>
          <span>{{ t('cockpit.column.working') }}</span>
        </span>
        <span class="cockpit-summary-item">
          <span class="cockpit-column-dot cockpit-column-dot--idle"></span>
          <strong>{{ boardSummary.idle }}</strong>
          <span>{{ t('cockpit.column.idle') }}</span>
        </span>
        <span v-if="overrideCount" class="cockpit-summary-item cockpit-summary-item--override">
          {{ t('cockpit.override', { count: overrideCount }) }}
        </span>
      </div>

      <div class="cockpit-controls">
        <v-select
          v-model="kindFilter"
          :items="kindFilterOptions"
          :label="t('cockpit.protocol')"
          density="compact"
          variant="outlined"
          hide-details
          class="kind-filter-select"
        />
        <v-text-field
          v-model="searchQuery"
          density="compact"
          variant="outlined"
          hide-details
          clearable
          prepend-inner-icon="mdi-magnify"
          :placeholder="t('cockpit.searchPlaceholder')"
          :aria-label="t('cockpit.searchPlaceholder')"
          class="conversation-search-field"
        />
      </div>
    </div>

    <div v-if="loading && !conversations.length" class="d-flex justify-center py-12">
      <v-progress-circular indeterminate color="primary" />
    </div>

    <v-card v-else-if="!conversations.length" variant="outlined" class="text-center pa-12 cockpit-empty">
      <v-icon size="48" color="grey">mdi-chat-outline</v-icon>
      <div class="text-body-1 mt-4 text-medium-emphasis">
        {{ t('cockpit.empty') }}
      </div>
    </v-card>

    <template v-else>
      <v-card v-if="!visibleBoardItems.length" variant="outlined" class="text-center pa-8 mb-4 cockpit-empty">
        <div class="text-body-2 text-medium-emphasis">
          {{ t('cockpit.noMatches') }}
        </div>
      </v-card>

      <div v-else class="cockpit-board">
        <section
          v-for="column in boardColumns"
          :key="column.key"
          class="cockpit-column"
        >
          <div class="cockpit-column-head">
            <div class="cockpit-column-title">
              <span class="cockpit-column-dot" :style="{ background: column.color }"></span>
              <span>{{ column.label }}</span>
            </div>
            <v-tooltip :text="t('cockpit.tooltip.columnCount', { column: column.label, count: column.items.length })" location="top" :open-delay="150" content-class="ccx-tooltip">
              <template #activator="{ props: tooltipProps }">
                <span v-bind="tooltipProps" class="cockpit-column-count">{{ column.items.length }}</span>
              </template>
            </v-tooltip>
          </div>

          <div class="cockpit-column-body">
            <div v-if="!column.items.length" class="cockpit-column-empty">--</div>
            <div
              v-for="item in column.items"
              :key="item.conversation.id"
              :ref="el => setConversationCardRef(item.conversation.id, el as Element | null)"
              class="cockpit-board-card"
            >
              <ConversationCard
                v-model:override-duration="overrideDuration"
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
                @success="(msg: string) => emit('success', msg)"
                @error="(msg: string) => emit('error', msg)"
              />
            </div>
          </div>
        </section>
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, nextTick, watch } from 'vue'
import { api, type ConversationInfo, type SequenceOverrideInfo, type ChannelSequenceEntry } from '@/services/api'
import { useGlobalTick } from '@/composables/useGlobalTick'
import { useI18n } from '@/i18n'
import { buildConversationBoardItems, filterConversationBoardItems, type BoardColumnKey, type ConversationBoardItem } from '@/utils/conversationDashboard'
import ConversationCard from './ConversationCard.vue'

const { t } = useI18n()
const emit = defineEmits<{
  success: [message: string]
  error: [message: string]
}>()

type DashboardChannel = { index: number; name: string; priority: number; status: string; circuitOpen?: boolean }

const loading = ref(true)
const conversations = ref<ConversationInfo[]>([])
const overrides = ref<Record<string, SequenceOverrideInfo>>({})
const channelsByKind = ref<Record<string, DashboardChannel[]>>({})
const activatedRoutingKinds = ref(new Set<string>())
const kindFilter = ref('')
const searchQuery = ref('')
const overrideDuration = ref(1800)
const nowMs = ref(Date.now())
const expandedCards = ref(new Set<string>())
const pinnedConversationOrder = ref<string[]>([])
const cardElements = new Map<string, HTMLElement>()

const boardColumnMeta = computed<Array<{ key: BoardColumnKey; label: string; color: string }>>(() => [
  { key: 'working', label: t('cockpit.column.working'), color: '#6366f1' },
  { key: 'idle', label: t('cockpit.column.idle'), color: '#10b981' },
])

const kindFilterOptions = computed(() => [
  { title: t('cockpit.allProtocols'), value: '' },
  { title: 'MESSAGES', value: 'messages' },
  { title: 'CHAT', value: 'chat' },
  { title: 'IMAGES', value: 'images' },
  { title: 'VECTORS', value: 'vectors' },
  { title: 'RESPONSES', value: 'responses' },
  { title: 'GEMINI', value: 'gemini' },
])

const sortedConversations = computed(() => {
  if (expandedCards.value.size > 0) {
    return getPinnedOrderedConversations(conversations.value)
  }
  return sortConversationsByLastActive(conversations.value)
})

const boardItems = computed(() => buildConversationBoardItems(sortedConversations.value))

const boardSummary = computed(() => boardItems.value.reduce((summary, item) => {
  summary[item.aggregateStatus] += 1
  return summary
}, { working: 0, idle: 0 }))

const overrideCount = computed(() => Object.values(overrides.value).filter(override =>
  override.hasMainSequence || (override.subagentSequence?.length ?? 0) > 0,
).length)

const visibleBoardItems = computed(() => {
  return filterConversationBoardItems(boardItems.value, kindFilter.value, searchQuery.value)
})

const boardColumns = computed(() => {
  const buckets = bucketBoardItems(visibleBoardItems.value)
  return boardColumnMeta.value.map(column => ({
    ...column,
    items: buckets[column.key],
  }))
})

function bucketBoardItems(items: ConversationBoardItem[]): Record<BoardColumnKey, ConversationBoardItem[]> {
  return items.reduce<Record<BoardColumnKey, ConversationBoardItem[]>>((buckets, item) => {
    buckets[item.aggregateStatus].push(item)
    return buckets
  }, { working: [], idle: [] })
}

function normalizeChannel(ch: any): DashboardChannel {
  const index = ch.index ?? ch.Index ?? 0
  return {
    index,
    name: ch.name ?? ch.Name ?? `Channel ${index}`,
    priority: ch.priority ?? ch.Priority ?? index,
    status: ch.status ?? ch.Status ?? 'active',
    circuitOpen: ch.circuitOpen ?? ch.CircuitOpen ?? false,
  }
}

function normalizeChannelsByKind(value: Record<string, any[]>): Record<string, DashboardChannel[]> {
  return Object.fromEntries(
    Object.entries(value).map(([kind, channels]) => [
      kind,
      (channels || [])
        .map(normalizeChannel)
        .sort((a, b) => (a.priority - b.priority) || (a.index - b.index)),
    ]),
  )
}

function getChannelsForKind(kind: string): DashboardChannel[] {
  return channelsByKind.value[kind] || []
}

function getConversationTitle(id?: string) {
  if (!id) return ''
  const conversation = conversations.value.find(item => item.id === id)
  return conversation?.title || conversation?.userId || id
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

watch(expandedCards, expanded => {
  if (expanded.size === 0) {
    conversations.value = applyConversationOrder(conversations.value)
  }
})

async function fetchAllChannels() {
  const textKinds = ['messages', 'chat', 'responses', 'gemini'] as const
  const routingStates = await Promise.all(textKinds.map(async kind => {
    try {
      return [kind, (await api.getSubagentRouting(kind)).activated] as const
    } catch {
      return [kind, false] as const
    }
  }))
  activatedRoutingKinds.value = new Set(routingStates.filter(([, active]) => active).map(([kind]) => kind))
  const kinds = ['messages', 'chat', 'responses', 'gemini', 'images', 'vectors'] as const
  for (const kind of kinds) {
    try {
      const dashboard = await api.getChannelDashboard(kind)
      if (!channelsByKind.value[kind]?.length) {
        channelsByKind.value[kind] = (dashboard.channels || [])
          .map(normalizeChannel)
          .sort((a, b) => (a.priority - b.priority) || (a.index - b.index))
      }
    } catch (e) {
      console.error(`[ConversationDashboard] fetch ${kind} channels error:`, e)
    }
  }
}

async function fetchConversations() {
  try {
    const resp = await api.getConversations(undefined)
    conversations.value = applyConversationOrder(resp.conversations || [])
    overrides.value = resp.overrides || {}
    if (resp.channelsByKind) channelsByKind.value = normalizeChannelsByKind(resp.channelsByKind)
  } catch (e) {
    console.error('[ConversationDashboard] fetch error:', e)
  } finally {
    loading.value = false
  }
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
    emit('error', `未找到关联对话 ${id.slice(0, 8)}`)
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

async function handleSetOverride(
  convId: string,
  sequence: ChannelSequenceEntry[],
  subagentSequence?: ChannelSequenceEntry[],
  clearSubagentSequence = false,
) {
  try {
    await api.setConversationOverride(convId, sequence, overrideDuration.value, subagentSequence, clearSubagentSequence)
    await fetchConversations()
  } catch (e) {
    console.error('[ConversationDashboard] set override error:', e)
    emit('error', e instanceof Error ? e.message : 'Override failed')
  }
}

async function handleRemoveOverride(convId: string) {
  try {
    await api.removeConversationOverride(convId)
    await fetchConversations()
  } catch (e) {
    console.error('[ConversationDashboard] remove override error:', e)
    emit('error', e instanceof Error ? e.message : 'Remove override failed')
  }
}

const tick = useGlobalTick(3000, 'ConversationDashboard')
tick.onTick(() => fetchConversations())
const clockTick = useGlobalTick(1000, 'ConversationDashboardClock')
clockTick.onTick(() => { nowMs.value = Date.now() })
fetchConversations()
fetchAllChannels()
</script>

<style scoped>
.cockpit-board-page {
  max-width: 1680px;
  margin: 0 auto;
}

.cockpit-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 20px;
}

.cockpit-summary,
.cockpit-summary-item,
.cockpit-controls {
  display: flex;
  align-items: center;
}

.cockpit-summary {
  gap: 18px;
  min-width: 0;
  flex-wrap: wrap;
}

.cockpit-summary-item {
  gap: 7px;
  color: rgb(var(--v-theme-on-surface) / 66%);
  font-size: 12px;
  white-space: nowrap;
}

.cockpit-summary-item strong {
  color: rgb(var(--v-theme-on-surface));
  font-size: 18px;
  font-variant-numeric: tabular-nums;
  line-height: 1;
}

.cockpit-summary-item--override {
  padding-left: 18px;
  border-left: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  color: rgb(var(--v-theme-on-surface) / 72%);
  font-weight: 700;
}

.cockpit-controls {
  gap: 10px;
  min-width: 0;
}

.cockpit-column-dot {
  width: 7px;
  height: 7px;
  border-radius: 999px;
  flex: 0 0 auto;
}

.cockpit-column-dot--working {
  background: #6366f1;
}

.cockpit-column-dot--idle {
  background: #10b981;
}

.kind-filter-select {
  width: 170px;
  flex: 0 0 170px;
}

.conversation-search-field {
  width: 300px;
  max-width: 300px;
  flex: 0 1 300px;
}

.conversation-search-field :deep(.v-field__input)::placeholder {
  color: rgb(var(--v-theme-on-surface) / 58%);
  opacity: 1;
}

.cockpit-empty {
  border-radius: 0 !important;
}

.cockpit-board {
  display: grid;
  grid-template-columns: minmax(420px, 1.35fr) minmax(320px, 1fr);
  gap: 20px;
  align-items: start;
}

.cockpit-column {
  min-width: 0;
}

.cockpit-column-head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 10px;
  padding: 0 2px 10px;
  border-bottom: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
}

.cockpit-column-title {
  display: inline-flex;
  align-items: center;
  gap: 8px;
  min-width: 0;
  color: rgb(var(--v-theme-on-surface) / 76%);
  font-size: 13px;
  font-weight: 800;
}

.cockpit-column-count {
  color: rgb(var(--v-theme-on-surface) / 58%);
  font-size: 12px;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
}

.cockpit-column-body {
  display: flex;
  flex-direction: column;
  gap: 10px;
  min-height: 80px;
  padding-top: 10px;
}

.cockpit-column-empty {
  padding: 18px 8px;
  border: 1px dashed rgba(var(--v-border-color), var(--v-border-opacity));
  color: rgb(var(--v-theme-on-surface) / 48%);
  font-size: 12px;
  text-align: center;
}

.conversation-card-target :deep(.conversation-card) {
  border-color: rgb(var(--v-theme-primary));
  box-shadow: 0 0 0 2px rgb(var(--v-theme-primary) / 24%);
}

@media (max-width: 959px) {
  .cockpit-board {
    grid-template-columns: 1fr;
  }
}

@media (max-width: 600px) {
  .cockpit-toolbar {
    align-items: stretch;
    flex-direction: column;
    gap: 12px;
    margin-bottom: 16px;
  }

  .cockpit-summary {
    justify-content: space-between;
    gap: 10px;
  }

  .cockpit-summary-item--override {
    margin-left: auto;
    padding-left: 10px;
  }

  .cockpit-controls {
    width: 100%;
  }

  .kind-filter-select {
    width: 142px;
    flex-basis: 142px;
  }

  .conversation-search-field {
    width: auto;
    min-width: 0;
    max-width: none;
    flex: 1 1 0;
  }
}

@media (max-width: 340px) {
  .cockpit-controls {
    align-items: stretch;
    flex-direction: column;
  }

  .kind-filter-select,
  .conversation-search-field {
    width: 100%;
    flex-basis: auto;
  }
}
</style>
