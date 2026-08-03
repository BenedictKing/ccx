<template>
  <div v-if="normalizedRoutes.length" class="protocol-model-availability">
    <div class="protocol-model-availability__header">
      <v-icon color="primary" size="20">mdi-routes</v-icon>
      <div>
        <div class="text-subtitle-2 font-weight-medium">
          {{ t('channelEditor.protocolModels.title') }}
        </div>
        <div class="text-caption text-medium-emphasis">
          {{ t('channelEditor.protocolModels.hint') }}
        </div>
      </div>
      <div class="protocol-model-availability__actions">
        <div
          v-if="isDetecting && !primaryDiscoveryRoute"
          class="protocol-model-availability__detecting text-caption text-medium-emphasis"
        >
          <v-progress-circular color="primary" indeterminate size="18" width="2" />
          <span>{{ t('channelEditor.protocolModels.detecting') }}</span>
        </div>
        <v-btn
          v-if="primaryDiscoveryRoute"
          class="protocol-model-availability__rediscover-all"
          size="small"
          variant="tonal"
          color="primary"
          :disabled="isDetecting"
          @click="rediscoverAll"
        >
          <v-progress-circular
            v-if="isDetecting"
            class="protocol-model-availability__btn-spinner"
            color="primary"
            indeterminate
            size="16"
            width="2"
          />
          <v-icon v-else start size="16">mdi-refresh</v-icon>
          {{ isDetecting ? t('channelEditor.protocolModels.detecting') : t('channelEditor.protocolModels.rediscoverAll') }}
        </v-btn>
      </div>
    </div>

    <v-alert
      v-if="showIncompleteHint"
      class="mb-3"
      type="warning"
      variant="tonal"
      density="compact"
    >
      {{ t('channelEditor.protocolModels.incompleteHint') }}
    </v-alert>
    <v-alert
      v-if="rediscoverError"
      class="mb-3"
      type="error"
      variant="tonal"
      density="compact"
    >
      {{ rediscoverError }}
    </v-alert>

    <section v-if="sharedModels.length" class="protocol-model-shared">
      <div class="protocol-model-shared__header">
        <v-icon size="18" color="success">mdi-check-all</v-icon>
        <div class="protocol-model-shared__identity">
          <span class="text-body-2 font-weight-medium">
            {{ t('channelEditor.protocolModels.sharedTitle') }}
          </span>
          <span class="text-caption text-medium-emphasis">
            {{ t('channelEditor.protocolModels.sharedHint', { count: sharedProtocolCount }) }}
          </span>
        </div>
        <v-chip size="x-small" variant="tonal" color="success">
          {{ t('channelEditor.protocolModels.sharedCount', { count: sharedModels.length }) }}
        </v-chip>
      </div>
      <ModelChipList :models="sharedModels" color="success" />
    </section>

    <div class="protocol-model-availability__rows">
      <div
        v-for="route in normalizedRoutes"
        :key="`${route.kind}:${route.channelUid || route.index}`"
        class="protocol-model-route"
        :class="{ 'protocol-model-route--unconfigured': !route.configured }"
        :data-kind="route.upstreamKind"
      >
        <div v-if="!route.configured" class="protocol-model-route__unconfigured text-caption text-info">
          <v-icon size="15" color="info">mdi-information-outline</v-icon>
          {{ t('channelEditor.protocolModels.unconfiguredProtocol') }}
        </div>
        <div class="protocol-model-route__identity">
          <v-icon size="18" color="primary">{{ route.icon }}</v-icon>
          <div class="protocol-model-route__label">
            <span class="text-body-2 font-weight-medium">{{ route.label }}</span>
            <code class="protocol-model-route__path">{{ route.path }}</code>
          </div>
          <v-chip v-if="route.hasInventory" size="x-small" variant="tonal" color="primary">
            {{ t('channelEditor.protocolModels.count', { count: route.models.length }) }}
          </v-chip>
        </div>

        <div v-if="route.hasInventory" class="protocol-model-route__discovery-meta">
          <span class="text-caption text-medium-emphasis">
            {{ t('channelEditor.protocolModels.lastDiscovered') }}
            {{ route.discoveryTime || t('channelEditor.protocolModels.discoveryTimeUnknown') }}
          </span>
          <v-chip size="x-small" variant="tonal" color="secondary">
            {{ route.discoverySourceLabel }}
          </v-chip>
          <span v-if="route.modelDiscoveryMessage" class="text-caption text-medium-emphasis">
            {{ route.modelDiscoveryMessage }}
          </span>
        </div>

        <!-- 多 Key 按可用 Key 集合归组，直接展示共同模型与各子集专有模型。 -->
        <div v-if="route.coverageGroups.length" class="protocol-model-route__coverage-groups">
          <div class="protocol-model-route__coverage-groups-header">
            <v-icon :color="route.hasBindingDifferences ? 'warning' : 'success'" size="16">
              {{ route.hasBindingDifferences ? 'mdi-key-alert' : 'mdi-check-all' }}
            </v-icon>
            <span
              class="text-caption font-weight-medium"
              :class="route.hasBindingDifferences ? 'text-warning' : 'text-success'"
            >
              {{ route.hasBindingDifferences
                ? t('channelEditor.protocolModels.diffCount', { count: route.diffModelCount })
                : t('channelEditor.protocolModels.consistent', { count: route.bindings.length }) }}
            </span>
          </div>
          <!-- 各 Key 模型一致时只会归出“全部共同可用”一个分组，其元信息与上方“一致”提示重复，
               直接列出 Key 与模型即可；存在差异时才按可用 Key 集合分组展示。 -->
          <template v-if="!route.hasBindingDifferences">
            <div class="protocol-model-coverage-group__keys">
              <v-chip
                v-for="binding in route.bindings"
                :key="binding.credentialUid || binding.keyMask"
                size="x-small"
                variant="tonal"
                color="success"
                class="protocol-model-coverage-group__key"
              >
                {{ binding.keyMask }}
              </v-chip>
            </div>
            <ModelChipList :models="route.specificModels" />
          </template>
          <template v-else>
            <div class="protocol-model-route__coverage-group-list">
              <section
                v-for="group in route.coverageGroups"
                :key="group.signature"
                class="protocol-model-coverage-group"
              >
                <div class="protocol-model-coverage-group__meta">
                  <span class="text-caption font-weight-medium">
                    {{ group.isSharedByAll
                      ? t('channelEditor.protocolModels.coverageGroupShared', { count: route.bindings.length })
                      : group.availableBindings.length
                        ? t('channelEditor.protocolModels.coverageGroupExclusive', { count: group.availableBindings.length })
                        : t('channelEditor.protocolModels.coverageGroupUnavailable') }}
                  </span>
                  <v-chip
                    size="x-small"
                    variant="tonal"
                    :color="coverageGroupColor(group, route.bindings.length)"
                  >
                    {{ t('channelEditor.protocolModels.coverageGroupModelCount', { count: group.models.length }) }}
                  </v-chip>
                </div>
                <div v-if="group.availableBindings.length" class="protocol-model-coverage-group__keys">
                  <v-chip
                    v-for="binding in group.availableBindings"
                    :key="binding.credentialUid || binding.keyMask"
                    size="x-small"
                    variant="tonal"
                    :color="coverageGroupColor(group, route.bindings.length)"
                    class="protocol-model-coverage-group__key"
                  >
                    {{ binding.keyMask }}
                  </v-chip>
                </div>
                <ModelChipList :models="group.models" />
              </section>
            </div>
            <div class="protocol-model-route__coverage">
              <v-chip
                v-for="binding in route.bindings"
                :key="binding.credentialUid || binding.keyMask"
                size="x-small"
                variant="tonal"
                :color="binding.models.length === route.models.length ? 'success' : 'warning'"
              >
                {{ binding.keyMask }} ·
                {{ t('channelEditor.protocolModels.coverage', { available: binding.models.length, total: route.models.length }) }}
              </v-chip>
            </div>
          </template>
        </div>

        <!-- 已按 Key 归组展示过模型的行不再需要兜底文案，避免与上方清单同时出现。 -->
        <template v-if="!route.coverageGroups.length">
          <ModelChipList v-if="route.specificModels.length" :models="route.specificModels" />
          <div v-else-if="route.hasInventory && sharedModels.length" class="text-caption text-medium-emphasis">
            {{ t('channelEditor.protocolModels.specificEmpty') }}
          </div>
          <div v-else class="text-caption text-medium-emphasis">
            {{ t('channelEditor.protocolModels.empty') }}
          </div>
        </template>
      </div>
    </div>
  </div>
</template>

<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'

import { useI18n } from '../../i18n'
import type { ChannelKind, ChannelProtocolRoute } from '../../services/api'
import { autoDiscoverChannel, getChannelAutoStatus } from '../../services/autopilot-api'
import ModelChipList from './ModelChipList.vue'

interface ProtocolDefinition {
  labelKey: string
  path: string
  icon: string
}

interface NormalizedModelBinding {
  credentialUid?: string
  keyMask: string
  models: string[]
}

interface ModelCoverageGroup {
  signature: string
  models: string[]
  availableBindings: NormalizedModelBinding[]
  isSharedByAll: boolean
}

const protocolDefinitions: Record<ChannelKind, ProtocolDefinition> = {
  messages: {
    labelKey: 'channelEditor.protocolModels.messages',
    path: '/v1/messages',
    icon: 'mdi-message-text-outline',
  },
  chat: {
    labelKey: 'channelEditor.protocolModels.chat',
    path: '/v1/chat/completions',
    icon: 'mdi-forum-outline',
  },
  responses: {
    labelKey: 'channelEditor.protocolModels.responses',
    path: '/v1/responses',
    icon: 'mdi-code-json',
  },
  gemini: {
    labelKey: 'channelEditor.protocolModels.gemini',
    path: '/v1beta/models/{model}:generateContent',
    icon: 'mdi-creation-outline',
  },
  images: {
    labelKey: 'channelEditor.protocolModels.images',
    path: '/v1/images/*',
    icon: 'mdi-image-outline',
  },
  vectors: {
    labelKey: 'channelEditor.protocolModels.vectors',
    path: '/v1/embeddings',
    icon: 'mdi-vector-polyline',
  },
}

const props = withDefaults(defineProps<{
  routes?: ChannelProtocolRoute[]
  loading?: boolean
}>(), {
  loading: false,
})

const emit = defineEmits<{
  refreshed: []
}>()

const { t } = useI18n()

const REDISCOVER_POLL_INTERVAL_MS = 1500
const REDISCOVER_POLL_TIMEOUT_MS = 5 * 60 * 1000

const sleep = (ms: number) => new Promise(resolve => setTimeout(resolve, ms))
const rediscovering = ref(false)
const rediscoverError = ref('')
let pollingGeneration = 0

const primaryDiscoveryRoute = computed(() => (
  (props.routes ?? []).find(route => route.configured !== false && route.channelUid && route.index >= 0)
  ?? (props.routes ?? []).find(route => route.configured !== false && route.channelUid)
))

const isDetecting = computed(() => props.loading || rediscovering.value)

const pollDiscovery = async (route: ChannelProtocolRoute, generation: number) => {
  const channelUid = route.channelUid
  if (!channelUid) return
  const deadline = Date.now() + REDISCOVER_POLL_TIMEOUT_MS
  for (;;) {
    const status = await getChannelAutoStatus(route.kind, channelUid)
    if (generation !== pollingGeneration) return
    const discovery = status.discovery
    if (discovery?.status === 'failed') {
      throw new Error(discovery.error || t('channelEditor.protocolModels.rediscoverFailed'))
    }
    if (!discovery || (discovery.status !== 'pending' && discovery.status !== 'running')) {
      emit('refreshed')
      return
    }
    if (Date.now() >= deadline) {
      throw new Error(t('channelEditor.protocolModels.rediscoverTimedOut'))
    }
    await sleep(REDISCOVER_POLL_INTERVAL_MS)
  }
}

const rediscoverAll = async () => {
  const route = primaryDiscoveryRoute.value
  const channelUid = route?.channelUid
  if (!route || !channelUid || rediscovering.value) return
  const generation = ++pollingGeneration
  rediscovering.value = true
  rediscoverError.value = ''

  try {
    try {
      await autoDiscoverChannel(route.kind, channelUid)
    } catch (err) {
      // 409 表示发现任务已在运行，直接进入轮询等待，不算错误。
      const status = (err as { status?: number }).status
      if (status !== 409) throw err
    }
    await pollDiscovery(route, generation)
  } catch (err) {
    if (generation === pollingGeneration) {
      rediscoverError.value = err instanceof Error ? err.message : t('channelEditor.protocolModels.rediscoverFailed')
    }
  } finally {
    if (generation === pollingGeneration) rediscovering.value = false
  }
}

watch(
  () => {
    const route = primaryDiscoveryRoute.value
    return route?.channelUid ? `${route.kind}:${route.channelUid}` : ''
  },
  async (target) => {
    const generation = ++pollingGeneration
    rediscovering.value = false
    rediscoverError.value = ''
    const route = primaryDiscoveryRoute.value
    if (!target || !route?.channelUid) return
    try {
      const status = await getChannelAutoStatus(route.kind, route.channelUid)
      if (generation !== pollingGeneration) return
      if (status.discovery?.status !== 'pending' && status.discovery?.status !== 'running') return
      rediscovering.value = true
      await pollDiscovery(route, generation)
    } catch {
      // 初始状态查询失败不阻断模型清单展示，用户仍可手动重新发现。
    } finally {
      if (generation === pollingGeneration) rediscovering.value = false
    }
  },
  { immediate: true },
)

onBeforeUnmount(() => {
  pollingGeneration++
})

const discoverySourceKey: Record<string, string> = {
  control_plane: 'channelEditor.protocolModels.source.controlPlane',
  models_api: 'channelEditor.protocolModels.source.modelsApi',
  builtin_manifest: 'channelEditor.protocolModels.source.builtinManifest',
  builtin_fallback: 'channelEditor.protocolModels.source.builtinFallback',
  // protocol_probe 为旧数据兼容：历史 profile 可能仍保留单模型代表全部协议的探测来源，
  // 刷新一次发现后会被 protocol_model_probe 取代。
  protocol_probe: 'channelEditor.protocolModels.source.protocolProbe',
  protocol_model_probe: 'channelEditor.protocolModels.source.protocolModelProbe',
  mixed: 'channelEditor.protocolModels.source.mixed',
}

const discoverySourceLabel = (source?: string) => {
  const key = source ? discoverySourceKey[source] : undefined
  return t(key ?? 'channelEditor.protocolModels.source.unknown')
}

const discoveryDateTimeFormat = new Intl.DateTimeFormat(undefined, {
  dateStyle: 'medium',
  timeStyle: 'medium',
})

const formatDiscoveryTime = (value?: string) => {
  if (!value) return ''
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? '' : discoveryDateTimeFormat.format(date)
}

const normalizeModels = (models?: string[]) => Array.from(new Set(
  (models ?? []).map(model => model.trim()).filter(Boolean),
)).sort((left, right) => left.localeCompare(right))

const groupModelsByAvailability = (models: string[], bindings: NormalizedModelBinding[]): ModelCoverageGroup[] => {
  if (bindings.length < 2) return []

  const groups = new Map<string, ModelCoverageGroup>()
  for (const model of models) {
    const availability = bindings.map(binding => binding.models.includes(model))
    const availableBindings = bindings.filter((_, index) => availability[index])
    const signature = availability.map(available => (available ? '1' : '0')).join('')
    const group = groups.get(signature)
    if (group) {
      group.models.push(model)
      continue
    }
    groups.set(signature, {
      signature,
      models: [model],
      availableBindings,
      isSharedByAll: availableBindings.length === bindings.length,
    })
  }

  return Array.from(groups.values()).sort((left, right) => {
    const coverageDifference = right.availableBindings.length - left.availableBindings.length
    return coverageDifference || left.models[0].localeCompare(right.models[0])
  })
}

const coverageGroupColor = (group: ModelCoverageGroup, bindingCount: number) => {
  if (group.availableBindings.length === bindingCount) return 'success'
  return group.availableBindings.length > 0 ? 'warning' : 'error'
}

const baseNormalizedRoutes = computed(() => (props.routes ?? []).map((route) => {
  const upstreamKind = route.upstreamKind ?? route.kind
  const definition = protocolDefinitions[upstreamKind]
  const hasDiscoveredInventory = route.modelInventoryKnown === true || Array.isArray(route.discoveredModels)
  const inventoryModels = hasDiscoveredInventory
    ? normalizeModels(route.discoveredModels)
    : normalizeModels(route.supportedModels)
  const bindings: NormalizedModelBinding[] = (route.modelBindings ?? []).map(binding => ({
    credentialUid: binding.credentialUid,
    keyMask: binding.keyMask,
    models: normalizeModels(binding.models),
  }))
  const models = normalizeModels([
    ...inventoryModels,
    ...bindings.flatMap(binding => binding.models),
  ])
  return {
    ...route,
    upstreamKind,
    configured: route.configured !== false,
    label: t(definition.labelKey),
    path: definition.path,
    icon: definition.icon,
    models,
    bindings,
    hasInventory: hasDiscoveredInventory || models.length > 0,
    discoveryTime: formatDiscoveryTime(route.modelsDiscoveredAt),
    discoverySourceLabel: discoverySourceLabel(route.modelDiscoverySource),
  }
}))

const sharedProtocolRoutes = computed(() => (
  baseNormalizedRoutes.value.filter(route => route.hasInventory)
))

const sharedProtocolCount = computed(() => sharedProtocolRoutes.value.length)

const sharedModels = computed(() => {
  const routes = sharedProtocolRoutes.value
  if (routes.length < 2) return []
  let intersection = routes[0].models
  for (const route of routes.slice(1)) {
    const available = new Set(route.models)
    intersection = intersection.filter(model => available.has(model))
  }
  return intersection
})

const normalizedRoutes = computed(() => {
  const shared = new Set(sharedModels.value)
  return baseNormalizedRoutes.value.map((route) => {
    const specificModels = route.models.filter(model => !shared.has(model))
    const coverageGroups = groupModelsByAvailability(specificModels, route.bindings)
    const diffModelCount = coverageGroups.reduce(
      (count, group) => count + (group.isSharedByAll ? 0 : group.models.length),
      0,
    )
    return {
      ...route,
      specificModels,
      coverageGroups,
      diffModelCount,
      hasBindingDifferences: diffModelCount > 0,
    }
  })
})

const showIncompleteHint = computed(() => (
  !isDetecting.value
  && normalizedRoutes.value.some(route => route.configured && !route.hasInventory)
))
</script>

<style scoped>
.protocol-model-availability {
  margin-top: 8px;
  border-top: 1px solid rgba(var(--v-theme-on-surface), 0.12);
}

.protocol-model-availability__header {
  display: flex;
  align-items: flex-start;
  flex-wrap: wrap;
  gap: 10px;
  padding: 18px 0 12px;
}

.protocol-model-availability__actions {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  justify-content: flex-end;
  gap: 10px;
  margin-left: auto;
}

.protocol-model-availability__detecting {
  display: flex;
  align-items: center;
  gap: 6px;
  white-space: nowrap;
}

.protocol-model-availability__btn-spinner {
  margin-inline-end: 8px;
}

@media (max-width: 600px) {
  .protocol-model-availability__actions {
    width: 100%;
    margin-left: 0;
    justify-content: space-between;
  }
}

.protocol-model-availability__rows {
  border: 1px solid rgba(var(--v-theme-on-surface), 0.12);
  border-radius: 6px;
  overflow: hidden;
}

.protocol-model-shared {
  display: flex;
  flex-direction: column;
  gap: 10px;
  margin-bottom: 12px;
  padding: 14px 16px;
  border: 1px solid rgba(var(--v-theme-success), 0.28);
  border-radius: 6px;
  background: rgba(var(--v-theme-success), 0.045);
}

.protocol-model-shared__header {
  display: flex;
  align-items: center;
  gap: 8px;
}

.protocol-model-shared__identity {
  display: flex;
  flex: 1;
  min-width: 0;
  flex-direction: column;
  gap: 2px;
}

.protocol-model-route {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 14px 16px;
}

.protocol-model-route + .protocol-model-route {
  border-top: 1px solid rgba(var(--v-theme-on-surface), 0.1);
}

.protocol-model-route--unconfigured {
  border-left: 2px dashed rgb(var(--v-theme-info));
  background: rgba(var(--v-theme-info), 0.035);
}

.protocol-model-route__unconfigured {
  display: flex;
  align-items: center;
  gap: 5px;
}

.protocol-model-route__identity {
  display: flex;
  align-items: flex-start;
  gap: 8px;
  min-width: 0;
}

.protocol-model-route__label {
  display: flex;
  flex: 1;
  min-width: 0;
  flex-direction: column;
  gap: 2px;
}

.protocol-model-route__discovery-meta {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
}

.protocol-model-route__path {
  overflow-wrap: anywhere;
  color: rgba(var(--v-theme-on-surface), 0.62);
  font-size: 0.72rem;
  line-height: 1.35;
}

.protocol-model-route__coverage-groups {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 10px 12px;
  border: 1px dashed rgba(var(--v-theme-on-surface), 0.22);
  border-radius: 6px;
  background: rgba(var(--v-theme-on-surface), 0.02);
}

.protocol-model-route__coverage-groups-header {
  display: flex;
  align-items: center;
  gap: 6px;
}

.protocol-model-route__coverage-group-list {
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.protocol-model-coverage-group {
  display: flex;
  flex-direction: column;
  flex-wrap: wrap;
  gap: 6px;
}

.protocol-model-coverage-group + .protocol-model-coverage-group {
  padding-top: 10px;
  border-top: 1px dashed rgba(var(--v-theme-on-surface), 0.16);
}

.protocol-model-coverage-group__meta,
.protocol-model-coverage-group__keys,
.protocol-model-coverage-group__models {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
}

.protocol-model-coverage-group__meta {
  gap: 8px;
}

.protocol-model-coverage-group__key {
  font-family: var(--v-font-family-mono, monospace);
}

.protocol-model-route__coverage {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  padding-top: 6px;
  border-top: 1px dashed rgba(var(--v-theme-warning), 0.25);
}

</style>
