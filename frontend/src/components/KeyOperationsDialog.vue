<template>
  <v-dialog v-model="open" max-width="760" scrollable>
    <v-card>
      <v-card-title class="d-flex align-center ga-2">
        <span class="key-dialog-title">{{ t('keyOps.title') }} · {{ channel?.name }}</span>
        <v-spacer />
        <v-btn
          icon="mdi-refresh"
          variant="text"
          size="small"
          :aria-label="t('keyOps.refresh')"
          :disabled="loading || operationsLoading"
          @click="refresh"
        />
        <v-btn icon="mdi-close" variant="text" size="small" :aria-label="t('keyOps.close')" @click="open = false" />
      </v-card-title>
      <v-card-text>
        <v-btn-toggle v-model="tab" mandatory density="compact" class="mb-4">
          <v-btn value="keys" size="small">{{ t('keyOps.overview') }}</v-btn>
          <v-btn value="operations" size="small">{{ t('keyOps.operations') }}</v-btn>
        </v-btn-toggle>
        <template v-if="tab === 'keys'">
          <v-progress-linear v-if="loading" indeterminate class="mb-3" />
          <v-alert v-if="overviewError" type="error" variant="tonal" class="mb-3">{{ overviewError }}</v-alert>
          <v-alert
            v-if="overview && (!overview.failureCoverage.persistent || overview.failureCoverage.limited)"
            type="info"
            variant="tonal"
            density="compact"
            class="mb-3"
          >
            {{ t(overview.failureCoverage.persistent ? 'keyOps.failureLimited' : 'keyOps.memoryOnly') }}
          </v-alert>
          <p v-if="overview && !overview.keys.length" class="text-medium-emphasis">{{ t('keyOps.noKeys') }}</p>
          <div v-for="key in overview?.keys" :key="key.keyId" class="key-observation">
            <div class="d-flex align-center flex-wrap ga-2">
              <code>{{ keyLabel(key) }}</code>
              <v-chip size="x-small" variant="tonal">{{ t(`keyOps.state.${key.state}`) }}</v-chip>
              <v-spacer />
              <span class="text-caption text-medium-emphasis">{{
                t('keyOps.stats15m', {
                  requests: key.stats.requestCount ?? 0,
                  rate: key.stats.requestCount ? key.stats.successRate.toFixed(0) : '--'
                })
              }}</span>
              <v-btn size="small" variant="text" @click="openRequests(key)">{{ t('keyOps.requests') }}</v-btn>
            </div>
            <div v-if="key.latestFailure" class="key-failure">
              <v-btn
                size="small"
                color="error"
                variant="text"
                class="px-0"
                @click="openRequests(key, key.latestFailure.attemptId)"
              >
                {{ t('keyOps.latestFailure') }} · HTTP {{ key.latestFailure.statusCode || '--' }}
              </v-btn>
              <span class="text-caption text-medium-emphasis ml-2">{{ date(key.latestFailure.occurredAt) }}</span>
              <p class="text-caption key-error-summary">
                {{ key.latestFailure.errorSummary || t('keyOps.noErrorSummary') }}
              </p>
            </div>
            <p v-else class="text-caption text-medium-emphasis mt-2 mb-0">{{ t('keyOps.noFailure') }}</p>
          </div>
        </template>
        <template v-else>
          <v-select
            :model-value="selectedKey"
            :items="keyItems"
            :label="t('keyOps.filterKey')"
            variant="outlined"
            density="compact"
            hide-details
            class="mb-3"
            @update:model-value="changeKey"
          />
          <v-progress-linear v-if="operationsLoading" indeterminate class="mb-3" />
          <v-alert v-if="operationsError" type="error" variant="tonal" class="mb-3">{{ operationsError }}</v-alert>
          <v-alert
            v-if="
              operations &&
              (!operations.coverage.persistent || operations.coverage.droppedWrites || operations.coverage.limited)
            "
            type="warning"
            variant="tonal"
            density="compact"
            class="mb-3"
          >
            {{
              t('keyOps.operationCoverage', {
                persistent: t(operations.coverage.persistent ? 'keyOps.persistent' : 'keyOps.memoryOnlyShort'),
                dropped: operations.coverage.droppedWrites
              })
            }}
          </v-alert>
          <p v-if="operations" class="text-caption text-medium-emphasis">
            {{ t('keyOps.retention', { days: operations.coverage.retentionDays }) }}
          </p>
          <p v-if="operations && !operations.events.length" class="text-medium-emphasis">
            {{ t('keyOps.noOperations') }}
          </p>
          <div v-for="event in operations?.events" :key="event.sequence" class="key-observation">
            <div class="d-flex align-center flex-wrap ga-2">
              <code>{{ keyLabel(event) }}</code>
              <strong class="text-body-2">{{ t(`keyOps.action.${event.action}`) }}</strong>
              <v-chip size="x-small" :color="event.outcome === 'failed' ? 'error' : undefined" variant="tonal">{{
                t(`keyOps.outcome.${event.outcome}`)
              }}</v-chip>
              <span class="text-caption text-medium-emphasis"
                >{{ t(`keyOps.source.${event.source}`) }} · {{ date(event.occurredAt) }}</span
              >
            </div>
            <p class="text-caption mt-2 mb-0">
              {{ t(`keyOps.state.${event.from}`) }} → {{ t(`keyOps.state.${event.to}`)
              }}<span v-if="event.reason"> · {{ t(`keyOps.reason.${event.reason}`) }}</span>
            </p>
          </div>
          <div class="d-flex justify-end ga-2 mt-3">
            <v-btn size="small" :disabled="operationsLoading || !cursorStack.length" @click="previousPage">{{
              t('routing.previousPage')
            }}</v-btn>
            <v-btn size="small" :disabled="operationsLoading || !operations?.nextCursor" @click="nextPage">{{
              t('routing.nextPage')
            }}</v-btn>
          </div>
        </template>
      </v-card-text>
    </v-card>
  </v-dialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { api, type Channel, type KeyOverview, type KeyOverviewResponse, type KeyOperationsPage } from '../services/api'
import type { RoutingHistorySelection } from '../services/routing-types'
import { useI18n } from '../i18n'

const props = defineProps<{ modelValue: boolean; channel?: Channel; channelType: RoutingHistorySelection['apiType'] }>()
const emit = defineEmits<{ 'update:modelValue': [boolean]; requestHistory: [RoutingHistorySelection] }>()
const { t } = useI18n()
const open = computed({ get: () => props.modelValue, set: value => emit('update:modelValue', value) })
const tab = ref('keys')
const overview = ref<KeyOverviewResponse>()
const operations = ref<KeyOperationsPage>()
const loading = ref(false)
const operationsLoading = ref(false)
const overviewError = ref('')
const operationsError = ref('')
const selectedKey = ref('')
const cursorStack = ref<number[]>([])
const keyLabels = ref(new Map<string, string>())
let generation = 0,
  operationsGeneration = 0,
  cursor = 0

const keyLabel = (key: { keyMask: string; keyId: string }) => `${key.keyMask} · ${key.keyId.slice(0, 8)}`
const keyItems = computed(() => [
  { title: t('keyOps.allKeys'), value: '' },
  ...[...keyLabels.value].map(([value, title]) => ({ title, value }))
])
const date = (value: string) => new Date(value).toLocaleString()
const errorMessage = (error: unknown) => (error instanceof Error ? error.message : t('keyOps.loadFailed'))
const rememberKeys = (keys: { keyId: string; keyMask: string }[]) => {
  const labels = new Map(keyLabels.value)
  for (const key of keys) labels.set(key.keyId, keyLabel(key))
  keyLabels.value = labels
}

async function loadOperations(before = 0) {
  if (!props.channel?.channelUid || !open.value) return false
  const current = generation,
    request = ++operationsGeneration
  operationsLoading.value = true
  operationsError.value = ''
  try {
    const result = await api.getKeyOperations(props.channelType, props.channel.channelUid, selectedKey.value, before)
    if (current !== generation || request !== operationsGeneration) return false
    operations.value = result
    rememberKeys(result.events)
    cursor = before
    return true
  } catch (error) {
    if (current === generation && request === operationsGeneration) operationsError.value = errorMessage(error)
    return false
  } finally {
    if (current === generation && request === operationsGeneration) operationsLoading.value = false
  }
}

async function refresh() {
  if (!props.channel?.channelUid || !open.value) return
  const current = generation,
    uid = props.channel.channelUid
  loading.value = true
  overviewError.value = ''
  cursorStack.value = []
  const operationsTask = loadOperations()
  try {
    const result = await api.getKeyOverview(props.channelType, uid)
    if (current !== generation) return
    if (result.channelUid !== uid) throw new Error(t('keyOps.loadFailed'))
    overview.value = result
    rememberKeys(result.keys)
  } catch (error) {
    if (current === generation) overviewError.value = errorMessage(error)
  } finally {
    if (current === generation) loading.value = false
    await operationsTask
  }
}

function changeKey(value: string) {
  selectedKey.value = value
  operations.value = undefined
  cursorStack.value = []
  void loadOperations()
}
async function nextPage() {
  const previous = cursor
  if (operations.value?.nextCursor && (await loadOperations(operations.value.nextCursor)))
    cursorStack.value.push(previous)
}
async function previousPage() {
  const previous = cursorStack.value[cursorStack.value.length - 1]
  if (previous !== undefined && (await loadOperations(previous))) cursorStack.value.pop()
}
function openRequests(key: KeyOverview, attemptId = '') {
  if (!props.channel?.channelUid) return
  open.value = false
  emit('requestHistory', {
    apiType: props.channelType,
    channelUid: props.channel.channelUid,
    channelName: props.channel.name,
    duration: 'retained',
    keyId: key.keyId,
    q: attemptId
  })
}
watch(
  () => [props.modelValue, props.channel?.channelUid, props.channelType],
  () => {
    generation++
    overview.value = undefined
    operations.value = undefined
    overviewError.value = operationsError.value = ''
    loading.value = operationsLoading.value = false
    selectedKey.value = ''
    keyLabels.value = new Map()
    tab.value = 'keys'
    if (open.value) void refresh()
  },
  { immediate: true }
)
</script>

<style scoped>
.key-dialog-title {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
}
.key-observation {
  padding: 12px 0;
  border-bottom: 1px solid rgba(var(--v-theme-on-surface), 0.12);
}
.key-observation code,
.key-error-summary {
  overflow-wrap: anywhere;
}
.key-error-summary {
  margin: 2px 0 0;
  white-space: pre-wrap;
}
</style>
