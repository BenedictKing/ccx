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
      <v-tooltip v-if="incomplete" :text="coverageNote" location="bottom">
        <template #activator="{ props: tooltipProps }"
          ><v-btn v-bind="tooltipProps" icon="mdi-information" variant="text" size="x-small" :aria-label="coverageNote"
        /></template>
      </v-tooltip>
    </div>
    <div class="routing-content">
      <v-alert v-if="!supported" type="info" variant="tonal" density="compact">
        {{ t('routing.rangeUnsupported')
        }}<v-btn size="small" variant="text" @click="emit('update:duration', '24h')">{{ t('routing.use24h') }}</v-btn>
      </v-alert>
      <template v-else>
        <div class="routing-toolbar">
          <v-btn-toggle v-model="basis" mandatory density="compact" variant="outlined" divided>
            <v-btn size="x-small" value="successful_requests">{{ t('routing.successBasis') }}</v-btn
            ><v-btn size="x-small" value="all_attempts">{{ t('routing.attemptBasis') }}</v-btn>
          </v-btn-toggle>
        </div>
        <div v-if="Object.keys(filters).length" class="routing-filters">
          <v-chip
            v-for="(value, field) in filters"
            :key="field"
            closable
            size="small"
            @click:close="removeFilter(field)"
            >{{ fieldTitle(field) }}: {{ field === 'channelUid' ? channelLabel(value) : value }}</v-chip
          >
          <v-btn v-if="Object.keys(filters).length" variant="text" size="x-small" @click="clearFilters">{{
            t('routing.clear')
          }}</v-btn>
        </div>
        <v-alert v-if="error" type="error" variant="tonal" density="compact"
          >{{ error }} <v-btn variant="text" size="small" @click="load">{{ t('routing.retry') }}</v-btn></v-alert
        >
        <template v-if="data">
          <p class="routing-summary text-caption">
            <span v-for="metric in summaryMetrics" :key="metric.key"
              >{{ t('routing.' + metric.key) }} <strong>{{ metric.value }}</strong></span
            >
          </p>
          <div v-if="!data.paths.length" class="routing-empty text-medium-emphasis">{{ t('routing.empty') }}</div>
          <ModelRoutingFlow
            v-else
            :key="scopeKey"
            :paths="data.paths"
            :total="data.summary.matchedCount"
            @select="selectNode"
            @path="openPath"
          />
          <div class="routing-toolbar">
            <span v-if="hiddenPathCount" class="text-caption">{{
              t('routing.hiddenPaths', { count: hiddenPathCount })
            }}</span
            ><span v-else></span
            ><v-btn size="small" variant="text" @click="openPath()">{{ t('routing.viewRequests') }}</v-btn>
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
            <h3>{{ t('routing.details') }}</h3>
            <v-btn
              icon="mdi-close"
              variant="text"
              size="small"
              :aria-label="t('routing.close')"
              @click="drawerOpen = false"
            />
          </div>
          <p v-if="selectedPath" class="selected-path">
            {{ pathTitle(selectedPath) }}<br />{{ selectedPath.count }} · {{ share(selectedPath.share) }}
          </p>
          <v-btn v-if="selectedPath" size="small" variant="text" @click="openPath()">{{ t('routing.allPaths') }}</v-btn>
          <p v-if="!selectedPath" class="text-caption">{{ t('routing.selectPath') }}</p>
          <div v-if="!selectedPath" class="drawer-paths">
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
  StatsDuration
} from '../services/routing-types'
import ModelRoutingFlow from './ModelRoutingFlow.vue'
import { routingPathKey, type RoutingField } from '../utils/modelRouting'
const props = defineProps<{
  apiType: ChannelKind
  duration: StatsDuration
  refreshVersion: number
  resetVersion: number
}>()
const emit = defineEmits<{ 'update:duration': [value: StatsDuration] }>()
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
const cursorHistory = ref<string[]>([])
const drawerExpired = ref(false)
const drawerPaths = ref<RoutingPath[]>([])
let generation = 0,
  drawerGeneration = 0,
  currentCursor = ''
let drawerQuery: RoutingQuery | undefined
const supported = computed(() => ['1h', '6h', '24h', 'today'].includes(props.duration))
const hiddenPathCount = computed(() => Math.max(0, (data.value?.paths.length ?? 0) - 12))
const scopeKey = computed(() => JSON.stringify([props.apiType, props.duration, basis.value, filters]))
const incomplete = computed(
  () =>
    data.value &&
    (new Date(data.value.coverage.startedAt) > new Date(data.value.from) ||
      !data.value.coverage.persistent ||
      data.value.coverage.droppedWrites > 0 ||
      data.value.coverage.limited)
)
const coverageNote = computed(() => {
  if (!data.value) return ''
  return [
    t('routing.coverage', { time: time(data.value.coverage.startedAt) }),
    !data.value.coverage.persistent ? t('routing.memoryOnly') : '',
    data.value.coverage.droppedWrites || data.value.coverage.limited ? t('routing.incomplete') : ''
  ]
    .filter(Boolean)
    .join(' ')
})
const summaryMetrics = computed(() =>
  data.value
    ? Object.entries(data.value.summary)
        .filter(
          ([key, value]) =>
            !['matchedCount', 'noAttemptRequests'].includes(key) &&
            (value || ['requests', 'success', 'attempts'].includes(key))
        )
        .map(([key, value]) => ({ key, value }))
    : []
)
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
  drawerQuery = undefined
}
async function loadRequests(cursor = '') {
  const id = ++drawerGeneration
  if (!drawerQuery) return
  drawerLoading.value = true
  drawerError.value = ''
  drawerExpired.value = false
  try {
    const page = await api.getRoutingRequests(props.apiType, { ...drawerQuery, cursor, limit: 10 })
    if (id === drawerGeneration && drawerOpen.value) {
      requestPage.value = page
      currentCursor = cursor
      return true
    }
  } catch (err) {
    if (id === drawerGeneration) {
      drawerExpired.value = err instanceof ApiError && err.status === 410
      drawerError.value = drawerExpired.value
        ? t('routing.snapshotExpired')
        : err instanceof Error
          ? err.message
          : String(err)
    }
  } finally {
    if (id === drawerGeneration) drawerLoading.value = false
  }
}
function openPath(path?: RoutingPath) {
  if (!data.value) return
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
    data.value = null
    load()
  },
  { deep: true }
)
onMounted(load)
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
  if (!value) drawerGeneration++
  else {
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
.routing-filters,
.routing-summary {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: 8px 18px;
}
.routing-toolbar {
  justify-content: space-between;
}
.routing-summary {
  font-variant-numeric: tabular-nums;
}
.routing-summary strong {
  padding-left: 4px;
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
</style>
