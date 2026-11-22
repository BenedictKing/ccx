<template>
  <section class="routing-panel">
    <div class="routing-title">
      <h3 class="routing-heading">{{ t('routing.title') }}</h3>
      <span class="routing-loading" :aria-busy="loading"
        ><v-progress-circular
          :indeterminate="loading"
          :size="16"
          :width="2"
          :style="{ visibility: loading ? 'visible' : 'hidden' }"
      /></span>
      <v-btn-toggle
        v-if="supported"
        v-model="basis"
        class="routing-basis"
        mandatory
        density="compact"
        variant="outlined"
        divided
      >
        <v-btn size="x-small" value="successful_requests">{{ t('routing.successBasis') }}</v-btn
        ><v-btn size="x-small" value="all_attempts">{{ t('routing.attemptBasis') }}</v-btn>
      </v-btn-toggle>
      <v-btn size="small" variant="text" prepend-icon="mdi-history" @click="openHistory()">{{
        t('routing.history')
      }}</v-btn>
    </div>
    <div class="routing-content">
      <v-alert v-if="!supported" type="info" variant="tonal" density="compact">
        {{ t('routing.rangeUnsupported')
        }}<v-btn size="small" variant="text" @click="emit('update:duration', '24h')">{{ t('routing.use24h') }}</v-btn>
      </v-alert>
      <template v-else>
        <v-alert v-if="error" type="error" variant="tonal" density="compact"
          >{{ error }} <v-btn variant="text" size="small" @click="load">{{ t('routing.retry') }}</v-btn></v-alert
        >
        <template v-if="data">
          <div v-if="!data.paths.length" class="routing-empty text-medium-emphasis">{{ t('routing.empty') }}</div>
          <ModelRoutingFlow
            v-else
            :paths="data.paths"
            :total="data.summary.matchedCount"
            @select="selectNode"
            @path="openPath"
          />
          <div class="routing-toolbar">
            <div class="routing-actions">
              <div v-if="Object.keys(filters).length" class="routing-filters">
                <v-chip
                  v-for="(value, field) in filters"
                  :key="field"
                  closable
                  size="small"
                  @click:close="removeFilter(field)"
                  >{{ fieldTitle(field) }}: {{ field === 'channelUid' ? channelLabel(value) : value }}</v-chip
                >
                <v-btn variant="text" size="x-small" @click="clearFilters">{{ t('routing.clear') }}</v-btn>
              </div>
              <span v-if="hiddenPathCount" class="text-caption">{{
                t('routing.hiddenPaths', { count: hiddenPathCount })
              }}</span>
            </div>
            <v-btn size="small" variant="text" @click="openPath()">{{ t('routing.viewRequests') }}</v-btn>
          </div>
        </template>
      </template>
    </div>
    <Teleport to="body">
      <v-navigation-drawer
        v-model="drawerOpen"
        location="right"
        temporary
        :width="smAndDown ? width : 520"
        class="routing-drawer"
        @keydown.esc="closeDrawer"
      >
        <div class="drawer-body">
          <div class="routing-toolbar">
            <h3>{{ historyMode ? t('routing.historyTitle') : t('routing.details') }}</h3>
            <v-btn
              icon="mdi-close"
              variant="text"
              size="small"
              :aria-label="t('routing.close')"
              @click="drawerOpen = false"
            />
          </div>
          <v-chip v-if="historyMode && historyChannel" closable size="small" @click:close="clearHistoryChannel">
            {{ historyChannel.channelName }}
          </v-chip>
          <div v-if="historyMode" class="routing-history-filters">
            <v-select
              v-model="historyDuration"
              :items="historyDurationItems"
              :label="t('routing.historyRange')"
              item-title="title"
              item-value="value"
              density="compact"
              variant="outlined"
              hide-details
            />
            <v-select
              v-model="historyStatus"
              :items="historyStatusItems"
              :label="t('routing.historyStatus')"
              item-title="title"
              item-value="value"
              density="compact"
              variant="outlined"
              hide-details
            />
            <v-select
              v-model="historyKeyId"
              :items="historyKeyItems"
              :label="t('routing.historyKey')"
              class="routing-history-key"
              item-title="title"
              item-value="value"
              density="compact"
              variant="outlined"
              hide-details
            />
            <v-text-field
              v-model="historySearch"
              class="routing-history-search"
              :label="t('routing.historySearch')"
              density="compact"
              variant="outlined"
              clearable
              hide-details
              @keydown.enter.prevent="runHistorySearch"
            />
            <v-btn color="primary" variant="tonal" :disabled="drawerLoading" @click="runHistorySearch">{{
              t('routing.search')
            }}</v-btn>
          </div>
          <p v-if="historyMode && requestPage?.coverage.retentionDays" class="text-caption text-medium-emphasis">
            {{ t('routing.historyRetention', { days: requestPage.coverage.retentionDays }) }}
          </p>
          <v-alert
            v-if="historyMode && requestPage && !requestPage.coverage.persistent"
            type="info"
            variant="tonal"
            density="compact"
          >
            {{ t('routing.memoryOnly') }} {{ t('routing.coverage', { time: time(requestPage.coverage.startedAt) }) }}
          </v-alert>
          <v-alert
            v-else-if="
              historyMode && requestPage && (requestPage.coverage.droppedWrites > 0 || requestPage.coverage.limited)
            "
            type="warning"
            variant="tonal"
            density="compact"
          >
            {{ t('routing.incomplete') }}
          </v-alert>
          <p v-if="selectedPath" class="selected-path">
            {{ pathTitle(selectedPath) }}<br />{{ selectedPath.count }} · {{ share(selectedPath.share) }}
          </p>
          <v-btn v-if="selectedPath" size="small" variant="text" @click="openPath()">{{ t('routing.allPaths') }}</v-btn>
          <p v-if="!historyMode && !selectedPath" class="text-caption">{{ t('routing.selectPath') }}</p>
          <div v-if="!historyMode && !selectedPath" class="drawer-paths">
            <button v-for="path in drawerPaths" :key="routingPathKey(path)" class="mobile-path" @click="openPath(path)">
              {{ pathTitle(path) }}<br />{{ path.count }} · {{ share(path.share) }}
            </button>
          </div>
          <v-alert v-if="drawerError" type="error" variant="tonal" density="compact">
            {{ drawerError }}
            <v-btn variant="text" size="small" :disabled="drawerLoading" @click="retryRequests">
              {{ t(drawerExpired ? 'routing.refreshRequests' : 'routing.retry') }}
            </v-btn> </v-alert
          ><v-progress-linear v-if="drawerLoading" indeterminate />
          <v-expansion-panels variant="accordion">
            <v-expansion-panel v-for="request in requestPage?.requests" :key="request.gatewayRequestId">
              <v-expansion-panel-title
                ><span class="request-label"
                  ><strong>{{ request.requestedModel || t('routing.unknownModel') }}</strong
                  ><span
                    >{{ t('routing.status.' + request.status) }} ·
                    {{ t('routing.attemptCount', { count: request.attempts.length }) }}</span
                  ><small>{{ time(request.receivedAt) }} · {{ request.gatewayRequestId }}</small></span
                ></v-expansion-panel-title
              >
              <v-expansion-panel-text>
                <p v-if="!request.attempts.length">{{ t('routing.beforeUpstream') }}</p>
                <article v-for="attempt in request.attempts" :key="attempt.attemptId" class="attempt-card">
                  <p v-if="attempt.auxiliary" class="text-caption">{{ t('routing.auxiliary') }}</p>
                  <strong
                    >{{ attempt.attemptSequence }}. {{ attempt.channelName
                    }}{{ attempt.channelDeleted ? t('routing.deleted') : '' }} ·
                    {{ t('routing.status.' + attempt.status) }}</strong
                  >
                  <p>{{ attempt.effectiveModel }} → {{ attempt.sentModel || t('routing.unknownModel') }}</p>
                  <button v-if="attempt.keyId" type="button" class="attempt-key" @click="filterHistoryKey(attempt)">
                    Key {{ attempt.keyMask || attempt.keyId.slice(0, 8) }} · {{ attempt.keyId.slice(0, 8) }}
                  </button>
                  <template v-if="attempt.details">
                    <p v-if="attempt.details.errorSummary" class="attempt-error">{{ attempt.details.errorSummary }}</p>
                    <div v-if="attempt.details.usageObserved" class="attempt-metrics">
                      <span>{{ t('channelLogs.input') }} {{ attempt.details.inputTokens }}</span>
                      <span>{{ t('channelLogs.output') }} {{ attempt.details.outputTokens }}</span>
                      <span>{{ t('channelLogs.cacheRead') }} {{ attempt.details.cacheReadTokens }}</span>
                      <span>{{ t('channelLogs.cacheWrite') }} {{ attempt.details.cacheCreationTokens }}</span>
                    </div>
                    <p v-else class="text-caption text-medium-emphasis">{{ t('routing.usageUnavailable') }}</p>
                    <div class="attempt-metrics">
                      <span
                        >{{ t('orchestration.firstByteLatency') }}
                        {{ latency(attempt.details.firstByteLatencyMs) }}</span
                      >
                      <span
                        >{{ t('channelLogs.duration.firstContent') }}
                        {{ latency(attempt.details.firstContentLatencyMs) }}</span
                      >
                      <span
                        >{{ t('channelLogs.duration.total') }}
                        {{ latency(attempt.completedAt ? attempt.details.durationMs : undefined) }}</span
                      >
                      <span v-if="attempt.details.maxStreamIdleMs"
                        >{{ t('channelLogs.duration.maxStreamIdle') }}
                        {{ latency(attempt.details.maxStreamIdleMs) }}</span
                      >
                      <span v-if="attempt.details.maxToolCallIdleMs"
                        >{{ t('channelLogs.duration.maxToolCallIdle') }}
                        {{ latency(attempt.details.maxToolCallIdleMs) }}</span
                      >
                    </div>
                  </template>
                  <p v-else class="text-caption text-medium-emphasis">{{ t('routing.detailsUnavailable') }}</p>
                  <p>
                    {{ t('routing.mapping.' + attempt.mappingOutcome)
                    }}<span v-if="attempt.matchType">
                      · {{ t('routing.match.' + attempt.matchType) }}: {{ attempt.mappingSource }} →
                      {{ attempt.mappingTarget }}</span
                    >
                  </p>
                  <p v-if="attempt.transformReason">
                    {{ t('routing.transform') }}:
                    {{
                      attempt.transformReason
                        .split(',')
                        .map(reason => t('routing.transform.' + reason))
                        .join(' · ')
                    }}
                  </p>
                  <p v-if="attempt.selectionReason">
                    {{ t('routing.selection') }}: {{ t('routing.reason.' + attempt.selectionReason) }}
                  </p>
                  <p v-for="(step, index) in attempt.transformSteps" :key="index" class="text-caption">
                    {{ t('routing.transform.' + step.kind) }}: {{ step.from }} → {{ step.to }}
                  </p>
                  <small
                    >{{ attempt.serviceType }} · {{ attempt.transport === 'websocket' ? 'WebSocket' : 'HTTP' }}
                    {{ attempt.statusCode || '—' }} · {{ elapsed(attempt.startedAt, attempt.completedAt) }} ·
                    {{ attempt.configRevision }}</small
                  >
                  <small class="text-medium-emphasis">ID: {{ attempt.attemptId }}</small>
                </article>
              </v-expansion-panel-text>
            </v-expansion-panel>
          </v-expansion-panels>
          <p v-if="requestPage && !requestPage.requests.length" class="text-caption">{{ t('routing.noRequests') }}</p>
          <div class="routing-toolbar">
            <v-btn size="small" :disabled="!cursorHistory.length || drawerLoading" @click="previousPage">{{
              t('routing.previous')
            }}</v-btn
            ><v-btn size="small" :disabled="!requestPage?.nextCursor || drawerLoading" @click="nextPage">{{
              t('routing.next')
            }}</v-btn>
          </div>
        </div>
      </v-navigation-drawer>
    </Teleport>
  </section>
</template>
<script setup lang="ts">
import { computed, reactive, ref, watch, onMounted, onBeforeUnmount, nextTick } from 'vue'
import { useDisplay } from 'vuetify'
import { VNavigationDrawer } from 'vuetify/components/VNavigationDrawer'
import { useI18n } from '../i18n'
import { api, type ChannelKind } from '../services/api'
import { ApiError } from '../services/api-helpers'
import type {
  RoutingBasis,
  RoutingDistribution,
  RoutingPath,
  RoutingQuery,
  RoutingRequestPage,
  RoutingStatus,
  RoutingHistoryDuration,
  RoutingHistorySelection,
  RoutingAttempt,
  StatsDuration
} from '../services/routing-types'
import ModelRoutingFlow from './ModelRoutingFlow.vue'
import { routingPathKey, type RoutingField } from '../utils/modelRouting'
const props = defineProps<{
  apiType: ChannelKind
  duration: StatsDuration
  refreshVersion: number
  resetVersion: number
  historySelection?: RoutingHistorySelection
}>()
const emit = defineEmits<{ 'update:duration': [value: StatsDuration]; historyClosed: [] }>()
const { t } = useI18n()
const { smAndDown, width } = useDisplay()
const basis = ref<RoutingBasis>('successful_requests')
const filters = reactive<Partial<Record<RoutingField, string>>>({})
const data = ref<RoutingDistribution | null>(null)
const loading = ref(false),
  error = ref('')
const drawerOpen = ref(false),
  drawerLoading = ref(false),
  drawerError = ref('')
const selectedPath = ref<RoutingPath>(),
  requestPage = ref<RoutingRequestPage>()
const historyMode = ref(false)
const historyDuration = ref<RoutingHistoryDuration>('24h')
const historyChannel = ref<RoutingHistorySelection>()
const historyStatus = ref<RoutingStatus | ''>('')
const historySearch = ref('')
const historyKeyId = ref('')
const cursorHistory = ref<string[]>([])
const drawerExpired = ref(false)
const drawerPaths = ref<RoutingPath[]>([])
let generation = 0,
  drawerGeneration = 0,
  currentCursor = ''
let drawerQuery: RoutingQuery | undefined
const supported = computed(() => ['1h', '6h', '24h', 'today'].includes(props.duration))
const hiddenPathCount = computed(() => Math.max(0, (data.value?.paths.length ?? 0) - 12))
const historyDurationItems = computed(() =>
  [
    { title: t('orchestration.minutes15'), value: '15m' },
    { title: t('chart.1h'), value: '1h' },
    { title: t('chart.6h'), value: '6h' },
    { title: t('chart.24h'), value: '24h' },
    { title: t('chart.7d'), value: '7d' },
    { title: t('chart.30d'), value: '30d' }
  ].map(item => ({
    ...item,
    props: {
      disabled:
        !!requestPage.value?.coverage.retentionDays &&
        (item.value === '7d' ? 7 : item.value === '30d' ? 30 : 1) > requestPage.value.coverage.retentionDays
    }
  }))
)
const historyStatusItems = computed(() => [
  { title: t('routing.status.all'), value: '' },
  { title: t('routing.status.success'), value: 'success' },
  { title: t('routing.status.failed'), value: 'failed' },
  { title: t('routing.status.cancelled'), value: 'cancelled' },
  { title: t('routing.status.in_progress'), value: 'in_progress' },
  { title: t('routing.status.unknown'), value: 'unknown' }
])
const historyKeyItems = computed(() => {
  const keys = new Map((requestPage.value?.keyOptions ?? []).map(key => [key.keyId, key.keyMask]))
  for (const request of requestPage.value?.requests ?? []) {
    for (const attempt of request.attempts) {
      if (attempt.keyId && (!historyChannel.value || attempt.channelUid === historyChannel.value.channelUid))
        keys.set(attempt.keyId, attempt.keyMask ?? '')
    }
  }
  if (historyKeyId.value && !keys.has(historyKeyId.value)) keys.set(historyKeyId.value, '')
  return [
    { title: t('routing.allKeys'), value: '' },
    ...[...keys].map(([id, mask]) => ({ title: `${mask || 'Key'} · ${id.slice(0, 8)}`, value: id }))
  ]
})
const latency = (value?: number) => (value === undefined ? '--' : `${Math.round(value)}ms`)
const time = (value: string) => new Date(value).toLocaleString()
const share = (value: number) => value.toFixed(1) + '%'
const fieldTitle = (field: RoutingField) => t('routing.' + field)
const channelLabel = (value?: string) =>
  data.value?.paths.find(path => path.channelUid === value)?.channelName ?? selectedPath.value?.channelName ?? value
const pathTitle = (path: RoutingPath) =>
  (path.requestedModel || t('routing.unknownModel')) +
  ' → ' +
  (path.channelName + (path.channelDeleted ? t('routing.deleted') : '')) +
  ' → ' +
  (path.sentModel || t('routing.unknownModel'))
const elapsed = (start: string, end?: string) =>
  end ? Math.max(0, new Date(end).getTime() - new Date(start).getTime()) + ' ms' : '—'
function clearFilters() {
  for (const key of Object.keys(filters) as RoutingField[]) delete filters[key]
}
function removeFilter(field: RoutingField) {
  delete filters[field]
}
function selectNode(selection: { field: RoutingField; value: string; channelUid?: string }) {
  const { field, value, channelUid } = selection
  if (field === 'sentModel' && channelUid) {
    if (filters.sentModel === value && filters.channelUid === channelUid) delete filters.sentModel
    else {
      filters.channelUid = channelUid
      filters.sentModel = value
    }
  } else {
    if (filters[field] === value) delete filters[field]
    else filters[field] = value
    if (field === 'channelUid') delete filters.sentModel
  }
}
async function load() {
  const id = ++generation
  if (!supported.value) {
    loading.value = false
    return
  }
  loading.value = true
  error.value = ''
  try {
    const result = await api.getModelRouting(props.apiType, {
      duration: props.duration,
      basis: basis.value,
      ...filters
    })
    if (id === generation) data.value = result
  } catch (err) {
    if (id === generation) {
      error.value = err instanceof Error ? err.message : String(err)
    }
  } finally {
    if (id === generation) loading.value = false
  }
}
function closeDrawer() {
  drawerGeneration++
  drawerOpen.value = false
  requestPage.value = undefined
  selectedPath.value = undefined
  historyMode.value = false
  drawerQuery = undefined
}
async function loadRequests(cursor = '') {
  const id = ++drawerGeneration
  if (!drawerQuery) return
  drawerLoading.value = true
  drawerError.value = ''
  drawerExpired.value = false
  try {
    const query = { ...drawerQuery, cursor, limit: 10 }
    const page = historyMode.value
      ? await api.getRoutingHistory(props.apiType, query)
      : await api.getRoutingRequests(props.apiType, query)
    if (id === drawerGeneration && drawerOpen.value) {
      requestPage.value = page
      currentCursor = cursor
      return true
    }
  } catch (err) {
    if (id === drawerGeneration) {
      drawerExpired.value = err instanceof ApiError && err.status === 410
      const errorCode =
        err instanceof ApiError && typeof err.details === 'object' && err.details !== null && 'code' in err.details
          ? err.details.code
          : ''
      drawerError.value = drawerExpired.value
        ? t('routing.snapshotExpired')
        : errorCode === 'routing_history_retention_exceeded'
          ? t('routing.historyRetentionExceeded')
          : err instanceof Error
            ? err.message
            : String(err)
    }
  } finally {
    if (id === drawerGeneration) drawerLoading.value = false
  }
}
function runHistorySearch() {
  historyMode.value = true
  selectedPath.value = undefined
  cursorHistory.value = []
  currentCursor = ''
  requestPage.value = undefined
  drawerQuery = {
    duration: historyDuration.value,
    basis: 'all_attempts',
    status: historyStatus.value,
    channelUid: historyChannel.value?.channelUid,
    keyId: historyKeyId.value,
    q: (historySearch.value || '').trim()
  }
  loadRequests()
}
function openHistory(selection?: RoutingHistorySelection, keyId = '') {
  historyKeyId.value = keyId
  historyChannel.value = selection
  if (selection) {
    historyDuration.value = selection.duration
    historyStatus.value = ''
    historySearch.value = ''
  }
  historyMode.value = true
  selectedPath.value = undefined
  drawerPaths.value = []
  drawerOpen.value = true
  runHistorySearch()
}
function clearHistoryChannel() {
  historyChannel.value = undefined
  runHistorySearch()
}
function filterHistoryKey(attempt: RoutingAttempt) {
  if (!attempt.keyId) return
  openHistory(
    {
      apiType: props.apiType,
      channelUid: attempt.channelUid,
      channelName: attempt.channelName,
      duration: historyMode.value ? historyDuration.value : '24h'
    },
    attempt.keyId
  )
}
function openPath(path?: RoutingPath) {
  if (!data.value) return
  historyMode.value = false
  selectedPath.value = path
  drawerPaths.value = [...data.value.paths]
  cursorHistory.value = []
  requestPage.value = undefined
  drawerOpen.value = true
  drawerQuery = {
    from: data.value.from,
    to: data.value.to,
    basis: basis.value,
    ...filters,
    ...(path ? { requestedModel: path.requestedModel, channelUid: path.channelUid, sentModel: path.sentModel } : {})
  }
  loadRequests()
}
async function nextPage() {
  if (requestPage.value?.nextCursor) {
    const previous = currentCursor
    if (await loadRequests(requestPage.value.nextCursor)) cursorHistory.value.push(previous)
  }
}
async function previousPage() {
  const cursor = cursorHistory.value[cursorHistory.value.length - 1]
  if (cursor !== undefined && (await loadRequests(cursor))) cursorHistory.value.pop()
}
async function retryRequests() {
  if (drawerExpired.value && historyMode.value) {
    runHistorySearch()
    return
  }
  if (!drawerExpired.value) {
    await loadRequests(currentCursor)
    return
  }
  const id = drawerGeneration
  const key = selectedPath.value && routingPathKey(selectedPath.value)
  await load()
  if (id === drawerGeneration && drawerOpen.value && data.value) {
    openPath(key ? data.value.paths.find(path => routingPathKey(path) === key) : undefined)
  }
}
watch([() => props.apiType, () => props.duration], () => {
  closeDrawer()
  clearFilters()
  data.value = null
  load()
})
watch(
  [basis, filters],
  () => {
    closeDrawer()
    load()
  },
  { deep: true }
)
onMounted(load)
watch(
  () => props.historySelection,
  selection => {
    if (selection && selection.apiType === props.apiType) openHistory(selection)
  },
  { immediate: true }
)
watch(
  () => props.refreshVersion,
  () => load()
)
watch(
  () => props.resetVersion,
  () => {
    closeDrawer()
    clearFilters()
  }
)
watch(drawerOpen, async value => {
  if (!value) {
    drawerGeneration++
    emit('historyClosed')
  } else {
    await nextTick()
    document.querySelector<HTMLElement>('.routing-drawer button[aria-label]')?.focus()
  }
})
onBeforeUnmount(() => {
  generation++
  drawerGeneration++
})
</script>
<style scoped>
.routing-panel {
  border-top: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  padding: 8px 16px 14px;
}
.routing-heading {
  display: flex;
  align-items: center;
  flex-shrink: 0;
  white-space: nowrap;
  gap: 7px;
  width: auto;
  margin: 0;
  padding: 7px 0;
  text-align: left;
}
.routing-title {
  display: flex;
  align-items: center;
  gap: 8px;
}
.routing-basis {
  margin-left: auto;
  flex-shrink: 0;
}
.routing-loading {
  display: inline-flex;
  align-items: center;
  width: 16px;
  height: 16px;
  flex-shrink: 0;
}
.routing-content > p,
.routing-toolbar > p,
.drawer-body > p {
  margin: 0;
}
.routing-content,
.drawer-body {
  display: grid;
  gap: 10px;
  min-width: 0;
}
.routing-toolbar,
.routing-actions,
.routing-filters {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px 18px;
}
.routing-toolbar {
  justify-content: space-between;
}
.mobile-path {
  display: grid;
  gap: 4px;
  width: 100%;
  text-align: left;
  border: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  border-radius: 5px;
  padding: 10px;
  overflow-wrap: anywhere;
  font-size: 12px;
}
.routing-empty {
  text-align: center;
  padding: 24px;
  font-size: 13px;
}
.drawer-body {
  padding: 16px;
}
.routing-history-filters {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  align-items: center;
  gap: 8px;
}
.routing-history-search {
  grid-column: 1 / 3;
}
.attempt-metrics {
  display: flex;
  flex-wrap: wrap;
  gap: 4px 12px;
  font-size: 12px;
}
.attempt-error {
  color: rgb(var(--v-theme-error));
  overflow-wrap: anywhere;
}
.attempt-key {
  width: fit-content;
  border: 0;
  background: transparent;
  padding: 0;
  color: rgb(var(--v-theme-primary));
  font: inherit;
  cursor: pointer;
}
.drawer-paths {
  display: grid;
  gap: 8px;
  max-height: 260px;
  overflow: auto;
}
.selected-path,
.request-label,
.attempt-card {
  overflow-wrap: anywhere;
}
.request-label {
  display: grid;
  gap: 4px;
  font-size: 12px;
  min-width: 0;
}
.request-label small {
  opacity: 0.65;
}
.attempt-card {
  border-bottom: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  padding: 10px 0;
  display: grid;
  gap: 5px;
  font-size: 12px;
}
@media (max-width: 600px) {
  .routing-title {
    gap: 4px;
  }
  .routing-heading {
    font-size: 14px;
  }
  .routing-basis :deep(.v-btn) {
    min-width: 0;
    padding: 0 6px;
    font-size: 11px;
  }
  .routing-history-filters {
    grid-template-columns: repeat(2, minmax(0, 1fr));
  }
  .routing-history-search,
  .routing-history-key {
    grid-column: 1 / -1;
  }
}
</style>
