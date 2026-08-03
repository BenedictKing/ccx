import { computed, ref } from 'vue'
import { api } from '../services/api'
import type {
  ModelBenchmarkProfile,
  PresetBundle,
  RuntimeModelRegistryBundle,
  SubscriptionPreset,
  UpstreamModelCapability,
  WrappedPresetCollection,
} from '../services/api-types'
import { setRuntimeUpstreamModelCapabilities } from '../utils/channelPayload'
import { claudeMessagesPresets as builtinClaudeMessagesPresets } from '../generated/claudeMessagesPresets'
import { codexResponsesPresets as builtinCodexResponsesPresets } from '../generated/codexResponsesPresets'
import { openaiChatPresets as builtinOpenAIChatPresets } from '../generated/openaiChatPresets'
import { openaiMessagesPresets as builtinOpenAIMessagesPresets } from '../generated/openaiMessagesPresets'

export type RuntimeClaudeMessagesPreset = typeof builtinClaudeMessagesPresets
export type RuntimeOpenAIMessagesPreset = typeof builtinOpenAIMessagesPresets
export type RuntimeOpenAIChatPreset = typeof builtinOpenAIChatPresets
export type RuntimeCodexResponsesPreset = typeof builtinCodexResponsesPresets
export type RuntimeModelRegistry = Record<string, UpstreamModelCapability>
export type RuntimeBenchmarkProfiles = Record<string, ModelBenchmarkProfile>

export interface RuntimeChannelPresetCollections {
  schemaVersion?: number
  claudeMessages: RuntimeClaudeMessagesPreset
  openAIChat: RuntimeOpenAIChatPreset
  codexResponses: RuntimeCodexResponsesPreset
  openAIMessages: RuntimeOpenAIMessagesPreset
}

export interface RuntimePresetState {
  schemaVersion?: number
  dataVersion?: string
  subscription: SubscriptionPreset
  modelRegistry: RuntimeModelRegistry
  benchmarkProfiles: RuntimeBenchmarkProfiles
  channelPresets: RuntimeChannelPresetCollections
  builtinModelsManifests?: PresetBundle['builtinModelsManifests']
}

const fallbackSubscriptionPreset: SubscriptionPreset = {
  originTypes: [
    { value: 'official_api', tier: 'first' },
    { value: 'official_token_plan', tier: 'first' },
    { value: 'relay', tier: 'second' },
    { value: 'community', tier: 'third' },
    { value: 'local_runtime', tier: 'local' },
    { value: 'unknown', tier: 'unknown' },
  ],
  billingModes: ['token_plan', 'pay_as_you_go', 'shared_free', 'unknown'],
  sources: ['manual', 'auto_discovered'],
  autoRefreshProviders: ['openai', 'anthropic', 'google'],
  newApiDefaults: { originType: 'relay', originTier: 'second', billingMode: 'token_plan' },
  originTypeAliases: { public_benefit: 'community' },
}

const fallbackChannelPresets: RuntimeChannelPresetCollections = {
  claudeMessages: builtinClaudeMessagesPresets,
  openAIChat: builtinOpenAIChatPresets,
  codexResponses: builtinCodexResponsesPresets,
  openAIMessages: builtinOpenAIMessagesPresets,
}

const fallbackRuntimePresetState: RuntimePresetState = {
  subscription: fallbackSubscriptionPreset,
  modelRegistry: {},
  benchmarkProfiles: {},
  channelPresets: fallbackChannelPresets,
}

const state = ref<RuntimePresetState>(fallbackRuntimePresetState)
const loaded = ref(false)
const loading = ref(false)
let inflight: Promise<RuntimePresetState> | null = null
let latestRequestToken = 0
let activeRequestCount = 0

function normalizeSubscriptionPreset(subscription?: Partial<SubscriptionPreset> | null): SubscriptionPreset {
  return {
    originTypes: subscription?.originTypes?.length ? subscription.originTypes : fallbackSubscriptionPreset.originTypes,
    billingModes: subscription?.billingModes?.length ? subscription.billingModes : fallbackSubscriptionPreset.billingModes,
    sources: subscription?.sources?.length ? subscription.sources : fallbackSubscriptionPreset.sources,
    autoRefreshProviders: subscription?.autoRefreshProviders?.length
      ? subscription.autoRefreshProviders
      : fallbackSubscriptionPreset.autoRefreshProviders,
    newApiDefaults: subscription?.newApiDefaults || fallbackSubscriptionPreset.newApiDefaults,
    originTypeAliases: subscription?.originTypeAliases || fallbackSubscriptionPreset.originTypeAliases,
  }
}

function isNonEmptyRecord(value: unknown): value is Record<string, unknown> {
  return !!value && typeof value === 'object' && !Array.isArray(value) && Object.keys(value).length > 0
}

function unwrapPresetCollection<T>(
  value: Record<string, T> | WrappedPresetCollection<T> | undefined,
  fallback: Record<string, T>,
): Record<string, T> {
  if (!value) return fallback
  const wrapped = value as WrappedPresetCollection<T>
  if (isNonEmptyRecord(wrapped.providers)) {
    return wrapped.providers as Record<string, T>
  }
  if (isNonEmptyRecord(wrapped.presets)) {
    return wrapped.presets as Record<string, T>
  }
  if (isNonEmptyRecord(value) && !('providers' in value) && !('presets' in value)) {
    return value as Record<string, T>
  }
  return fallback
}

function normalizeModelRegistry(modelRegistry?: PresetBundle['modelRegistry'] | null): RuntimeModelRegistry {
  if (!modelRegistry) return {}

  const registryBundle = modelRegistry as RuntimeModelRegistryBundle
  if (Array.isArray(registryBundle.upstreamCapabilities) && registryBundle.upstreamCapabilities.length > 0) {
    const normalized: RuntimeModelRegistry = {}
    for (const entry of registryBundle.upstreamCapabilities) {
      const patterns = Array.isArray(entry.patterns) ? entry.patterns : []
      const capability: UpstreamModelCapability = {
        ...entry,
      }
      delete (capability as UpstreamModelCapability & { patterns?: string[] }).patterns
      for (const pattern of patterns) {
        const trimmedPattern = pattern.trim()
        if (trimmedPattern) {
          normalized[trimmedPattern] = capability
        }
      }
    }
    return normalized
  }

  if (isNonEmptyRecord(modelRegistry)) {
    return modelRegistry as Record<string, UpstreamModelCapability>
  }

  return {}
}

function normalizeBenchmarkProfiles(modelRegistry?: PresetBundle['modelRegistry'] | null): RuntimeBenchmarkProfiles {
  const registryBundle = modelRegistry as RuntimeModelRegistryBundle | null | undefined
  const profiles = registryBundle?.benchmarkProfiles
  if (!Array.isArray(profiles)) return {}

  const normalized: RuntimeBenchmarkProfiles = {}
  for (const entry of profiles) {
    const patterns = Array.isArray(entry.patterns) ? entry.patterns : []
    const profile: ModelBenchmarkProfile = { ...entry }
    delete (profile as ModelBenchmarkProfile & { patterns?: string[] }).patterns
    for (const pattern of patterns) {
      const trimmedPattern = pattern.trim()
      if (trimmedPattern) normalized[trimmedPattern] = profile
    }
  }
  return normalized
}

function normalizeBundle(bundle?: Partial<PresetBundle> | null): RuntimePresetState {
  const runtimeChannelPresets = bundle?.channelPresets
  return {
    schemaVersion: bundle?.schemaVersion,
    dataVersion: bundle?.dataVersion,
    subscription: normalizeSubscriptionPreset(bundle?.subscription),
    modelRegistry: normalizeModelRegistry(bundle?.modelRegistry),
    benchmarkProfiles: normalizeBenchmarkProfiles(bundle?.modelRegistry),
    channelPresets: {
      schemaVersion: runtimeChannelPresets?.schemaVersion ?? bundle?.schemaVersion,
      claudeMessages: unwrapPresetCollection(runtimeChannelPresets?.claudeMessages, builtinClaudeMessagesPresets),
      openAIChat: unwrapPresetCollection(runtimeChannelPresets?.openAIChat, builtinOpenAIChatPresets),
      codexResponses: unwrapPresetCollection(runtimeChannelPresets?.codexResponses, builtinCodexResponsesPresets),
      openAIMessages: unwrapPresetCollection(runtimeChannelPresets?.openAIMessages, builtinOpenAIMessagesPresets),
    },
    builtinModelsManifests: bundle?.builtinModelsManifests,
  }
}

function applyRuntimePresetState(nextState: RuntimePresetState) {
  state.value = nextState
  setRuntimeUpstreamModelCapabilities(nextState.modelRegistry)
  loaded.value = true
}

export function setRuntimePresetState(nextState: RuntimePresetState) {
  applyRuntimePresetState(nextState)
}

export function resetRuntimePresetState() {
  state.value = fallbackRuntimePresetState
  setRuntimeUpstreamModelCapabilities(null)
  loaded.value = false
  loading.value = false
  inflight = null
  latestRequestToken = 0
  activeRequestCount = 0
}

export async function ensureRuntimePresetsLoaded(force = false): Promise<RuntimePresetState> {
  if (!force && loaded.value) {
    return state.value
  }
  if (!force && inflight) {
    return inflight
  }

  const requestToken = ++latestRequestToken
  activeRequestCount += 1
  loading.value = true

  const request = api.getPresets()
    .then((bundle) => {
      const nextState = normalizeBundle(bundle)
      if (requestToken === latestRequestToken) {
        applyRuntimePresetState(nextState)
      }
      return requestToken === latestRequestToken ? nextState : state.value
    })
    .catch(() => {
      const nextState = normalizeBundle(null)
      if (requestToken === latestRequestToken) {
        applyRuntimePresetState(nextState)
      }
      return requestToken === latestRequestToken ? nextState : state.value
    })
    .finally(() => {
      activeRequestCount = Math.max(0, activeRequestCount - 1)
      if (requestToken === latestRequestToken) {
        inflight = null
      }
      loading.value = activeRequestCount > 0
    })

  inflight = request
  return request
}

export function useRuntimePresets() {
  return {
    runtimePresets: computed(() => state.value),
    subscriptionPreset: computed(() => state.value.subscription),
    effectiveModelRegistry: computed(() => state.value.modelRegistry),
    effectiveBenchmarkProfiles: computed(() => state.value.benchmarkProfiles),
    effectiveChannelPresets: computed(() => state.value.channelPresets),
    loaded: computed(() => loaded.value),
    loading: computed(() => loading.value),
    ensureLoaded: ensureRuntimePresetsLoaded,
  }
}
