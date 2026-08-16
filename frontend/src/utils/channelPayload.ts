import type { Channel, EmbeddingCapability, UpstreamModelCapability } from '../services/api'
import { normalizeAdvancedChannelOptions } from './channelAdvancedOptions'
import { deduplicateEquivalentBaseUrls } from './baseUrlSemantics'
import { extractChannelNamePrefix } from './add-channel-modal-state'

let runtimeUpstreamModelCapabilities: Record<string, UpstreamModelCapability> = {}

const DEFAULT_COPILOT_BASE_URL = 'https://api.githubcopilot.com'

export interface ModelCapabilityRow {
  id: number
  model: string
  contextWindowTokens: string | number | null
  maxOutputTokens: string | number | null
  thinkingMode: string
  reasoningEffortsText: string
  pricingUnit: string
  pricingCurrency: string
  inputCacheHitPrice: string | number | null
  inputCacheMissPrice: string | number | null
  outputPrice: string | number | null
  pricingTiers?: NonNullable<UpstreamModelCapability['pricing']>['tiers']
  defaultOutputTokens?: number
  recommendedOutputTokens?: number
  displayName?: string
  description?: string
  source?: 'builtin' | 'custom'
  matchedPattern?: string
}

export interface EmbeddingCapabilityRow {
  id: number
  model: string
  embeddingSpaceId: string
  dimensions: string | number | null
  supportedDimensionsText: string
  normalized: '' | 'true' | 'false'
}

type SelectableString = string | { title?: string; value?: unknown } | null | undefined

export interface ChannelFormLike {
  name: string
  remark?: string
  serviceType: 'openai' | 'gemini' | 'claude' | 'responses' | 'copilot' | ''
  authHeader?: 'auto' | 'bearer' | 'x-api-key' | ''
  baseUrl: string
  baseUrls: string[]
  website: string
  insecureSkipVerify: boolean
  lowQuality: boolean
  injectDummyThoughtSignature: boolean
  stripThoughtSignature: boolean
  description: string
  apiKeys: string[]
  apiKeyConfigs?: Channel['apiKeyConfigs']
  modelMapping: Record<string, SelectableString>
  modelCapabilitiesText?: string
  modelCapabilityRows?: ModelCapabilityRow[]
  embeddingCapabilityRows?: EmbeddingCapabilityRow[]
  defaultContextWindowTokens?: string | number | null
  defaultMaxOutputTokens?: string | number | null
  allowUnknownContext?: boolean
  reasoningMapping: Record<string, 'none' | 'minimal' | 'low' | 'medium' | 'high' | 'xhigh' | 'max'>
  reasoningParamStyle: 'reasoning' | 'reasoning_effort' | 'thinking'
  textVerbosity: 'low' | 'medium' | 'high' | ''
  fastMode: boolean
  customHeaders: Record<string, string>
  proxyUrl: string
  costMultiplier?: string | number | null
  exchangeRate?: string | number | null
  requestTimeoutMs?: string | number | null
  responseHeaderTimeoutMs?: string | number | null
  streamFirstContentTimeoutMs?: string | number | null
  streamInactivityTimeoutMs?: string | number | null
  streamToolCallIdleTimeoutMs?: string | number | null
  rateLimitRpm?: string | number | null
  rateLimitWindowMinutes?: string | number | null
  rateLimitBurst?: string | number | null
  rateLimitMaxConcurrent?: string | number | null
  rateLimitAutoFromHeaders?: boolean
  routePrefix: string
  supportedModels: string[]
  autoBlacklistBalance: boolean
  normalizeMetadataUserId: boolean
  stripBillingHeader?: boolean
  normalizeSystemRoleToTopLevel: boolean
  codexToolCompat: boolean
  stripCodexClientTools?: boolean
  convertImageUrlToB64Json?: boolean
  noVision: boolean
  noVisionModels: string[]
  visionFallbackModel: SelectableString
  historicalImageTurnLimit?: string | number | null
  tags?: string[]

}

export type ChannelProtocol = 'messages' | 'chat' | 'responses' | 'gemini' | 'images' | 'vectors'

export interface BuildChannelPayloadOptions {
  channelType?: ChannelProtocol
}

function normalizePricingValue(value: unknown): number | null | false {
  const trimmed = String(value ?? '').trim()
  if (!trimmed) return null
  const parsed = Number(trimmed)
  if (!Number.isFinite(parsed) || parsed < 0) return false
  return parsed
}

function hasPricingValue(row: ModelCapabilityRow): boolean {
  return !!(
    String(row.inputCacheHitPrice ?? '').trim() ||
    String(row.inputCacheMissPrice ?? '').trim() ||
    String(row.outputPrice ?? '').trim() ||
    row.pricingTiers?.length
  )
}

function createPricingFromRow(row: ModelCapabilityRow): UpstreamModelCapability['pricing'] | null | false {
  if (!hasPricingValue(row)) return null
  const inputCacheHitPrice = normalizePricingValue(row.inputCacheHitPrice)
  const inputCacheMissPrice = normalizePricingValue(row.inputCacheMissPrice)
  const outputPrice = normalizePricingValue(row.outputPrice)
  if (inputCacheHitPrice === false || inputCacheMissPrice === false || outputPrice === false) return false
  const currency = row.pricingCurrency.trim() || 'USD'
  const pricing: NonNullable<UpstreamModelCapability['pricing']> = {
    unit: `per_1m_tokens_${currency.toLowerCase()}`,
    currency,
  }
  if (inputCacheHitPrice !== null) pricing.inputCacheHitPrice = inputCacheHitPrice
  if (inputCacheMissPrice !== null) pricing.inputCacheMissPrice = inputCacheMissPrice
  if (outputPrice !== null) pricing.outputPrice = outputPrice
  if (row.pricingTiers?.length) pricing.tiers = row.pricingTiers
  return pricing
}

export function parseModelCapabilitiesText(text?: string): Record<string, UpstreamModelCapability> | null {
  const trimmed = (text || '').trim()
  if (!trimmed) return {}

  let parsed: unknown
  try {
    parsed = JSON.parse(trimmed)
  } catch {
    return null
  }

  if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
    return null
  }

  const result: Record<string, UpstreamModelCapability> = {}
  for (const [model, rawCapability] of Object.entries(parsed as Record<string, unknown>)) {
    const modelName = model.trim()
    if (!modelName || !rawCapability || typeof rawCapability !== 'object' || Array.isArray(rawCapability)) {
      return null
    }

    const capability = rawCapability as Record<string, unknown>
    const normalized: UpstreamModelCapability = {}
    const contextWindowTokens = capability.contextWindowTokens
    if (contextWindowTokens !== undefined) {
      if (typeof contextWindowTokens !== 'number' || !Number.isInteger(contextWindowTokens) || contextWindowTokens < 0) return null
      normalized.contextWindowTokens = contextWindowTokens
    }
    const maxOutputTokens = capability.maxOutputTokens
    if (maxOutputTokens !== undefined) {
      if (typeof maxOutputTokens !== 'number' || !Number.isInteger(maxOutputTokens) || maxOutputTokens < 0) return null
      normalized.maxOutputTokens = maxOutputTokens
    }
    if (capability.thinkingMode !== undefined) {
      if (typeof capability.thinkingMode !== 'string') return null
      normalized.thinkingMode = capability.thinkingMode
    }
    if (capability.reasoningEfforts !== undefined) {
      if (!Array.isArray(capability.reasoningEfforts) || !capability.reasoningEfforts.every(v => typeof v === 'string')) return null
      normalized.reasoningEfforts = capability.reasoningEfforts
    }
    if (capability.pricing !== undefined) {
      if (!capability.pricing || typeof capability.pricing !== 'object' || Array.isArray(capability.pricing)) return null
      const pricing = capability.pricing as Record<string, unknown>
      const normalizedPricing: NonNullable<UpstreamModelCapability['pricing']> = {}
      if (pricing.unit !== undefined) {
        if (typeof pricing.unit !== 'string') return null
        normalizedPricing.unit = pricing.unit
      }
      if (pricing.currency !== undefined) {
        if (typeof pricing.currency !== 'string') return null
        normalizedPricing.currency = pricing.currency
      }
      for (const key of ['inputCacheHitPrice', 'inputCacheMissPrice', 'outputPrice'] as const) {
        if (pricing[key] !== undefined) {
          if (typeof pricing[key] !== 'number' || !Number.isFinite(pricing[key]) || pricing[key] < 0) return null
          normalizedPricing[key] = pricing[key]
        }
      }
      if (pricing.tiers !== undefined) {
        if (!Array.isArray(pricing.tiers)) return null
        const tiers: NonNullable<UpstreamModelCapability['pricing']>['tiers'] = []
        for (const tier of pricing.tiers) {
          if (!tier || typeof tier !== 'object' || Array.isArray(tier)) return null
          const tierRecord = tier as Record<string, unknown>
          const normalizedTier: NonNullable<NonNullable<UpstreamModelCapability['pricing']>['tiers']>[number] = {}
          if (tierRecord.label !== undefined) {
            if (typeof tierRecord.label !== 'string') return null
            normalizedTier.label = tierRecord.label
          }
          for (const key of ['inputTokensAbove', 'inputTokensUpTo'] as const) {
            if (tierRecord[key] !== undefined) {
              if (typeof tierRecord[key] !== 'number' || !Number.isInteger(tierRecord[key]) || tierRecord[key] < 0) return null
              normalizedTier[key] = tierRecord[key]
            }
          }
          for (const key of ['inputCacheHitPrice', 'inputCacheMissPrice', 'outputPrice'] as const) {
            if (tierRecord[key] !== undefined) {
              if (typeof tierRecord[key] !== 'number' || !Number.isFinite(tierRecord[key]) || tierRecord[key] < 0) return null
              normalizedTier[key] = tierRecord[key]
            }
          }
          tiers.push(normalizedTier)
        }
        normalizedPricing.tiers = tiers
      }
      normalized.pricing = normalizedPricing
    }

    result[modelName] = normalized
  }

  return result
}

function matchesModelPattern(pattern: string, model: string): boolean {
  const trimmedPattern = pattern.trim()
  const trimmedModel = model.trim()
  if (!trimmedPattern || !trimmedModel) return false
  try {
    return new RegExp(trimmedPattern, 'i').test(trimmedModel)
  } catch {
    return false
  }
}

export function setRuntimeUpstreamModelCapabilities(capabilities: Record<string, UpstreamModelCapability> | null | undefined) {
  runtimeUpstreamModelCapabilities = capabilities || {}
}

function getEffectiveUpstreamModelCapabilities(): Record<string, UpstreamModelCapability> {
  return runtimeUpstreamModelCapabilities
}

export function resolveBuiltinUpstreamModelCapability(model: string): { capability: UpstreamModelCapability; pattern: string } | null {
  const trimmed = model.trim()
  if (!trimmed) return null
  const registry = getEffectiveUpstreamModelCapabilities()
  if (registry[trimmed]) {
    return { capability: registry[trimmed], pattern: trimmed }
  }

  const patterns = Object.keys(registry)
    .filter(pattern => pattern !== trimmed)
    .sort((a, b) => b.length - a.length || a.localeCompare(b))
  for (const pattern of patterns) {
    if (matchesModelPattern(pattern, trimmed)) {
      return { capability: registry[pattern], pattern }
    }
  }
  return null
}

export function createModelCapabilityRow(
  id: number,
  model = '',
  capability?: UpstreamModelCapability,
  source: 'builtin' | 'custom' = 'custom',
  matchedPattern = '',
): ModelCapabilityRow {
  return {
    id,
    model,
    contextWindowTokens: capability?.contextWindowTokens || null,
    maxOutputTokens: capability?.maxOutputTokens || null,
    thinkingMode: capability?.thinkingMode || '',
    reasoningEffortsText: capability?.reasoningEfforts?.join(', ') || '',
    pricingUnit: capability?.pricing?.unit || 'per_1m_tokens_usd',
    pricingCurrency: capability?.pricing?.currency || 'USD',
    inputCacheHitPrice: capability?.pricing?.inputCacheHitPrice ?? null,
    inputCacheMissPrice: capability?.pricing?.inputCacheMissPrice ?? null,
    outputPrice: capability?.pricing?.outputPrice ?? null,
    pricingTiers: capability?.pricing?.tiers,
    defaultOutputTokens: capability?.defaultOutputTokens,
    recommendedOutputTokens: capability?.recommendedOutputTokens,
    displayName: capability?.displayName || '',
    description: capability?.description || '',
    source,
    matchedPattern,
  }
}

export function capabilityRowDefaultsFromBuiltin(capability: UpstreamModelCapability) {
  return {
    contextWindowTokens: capability.contextWindowTokens || null,
    maxOutputTokens: capability.maxOutputTokens || null,
    thinkingMode: capability.thinkingMode || '',
    reasoningEffortsText: capability.reasoningEfforts?.join(', ') || '',
    pricingUnit: capability.pricing?.unit || 'per_1m_tokens_usd',
    pricingCurrency: capability.pricing?.currency || 'USD',
    inputCacheHitPrice: capability.pricing?.inputCacheHitPrice ?? null,
    inputCacheMissPrice: capability.pricing?.inputCacheMissPrice ?? null,
    outputPrice: capability.pricing?.outputPrice ?? null,
    pricingTiers: capability.pricing?.tiers,
    defaultOutputTokens: capability.defaultOutputTokens,
    recommendedOutputTokens: capability.recommendedOutputTokens,
    displayName: capability.displayName || '',
    description: capability.description || '',
  }
}

export function modelCapabilitiesToRows(record: Record<string, UpstreamModelCapability> | undefined, nextId: () => number): ModelCapabilityRow[] {
  return Object.entries(record || {})
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([model, capability]) => createModelCapabilityRow(nextId(), model, capability, 'custom'))
}

export function modelCapabilityRowsToRecord(rows: ModelCapabilityRow[] = []): Record<string, UpstreamModelCapability> | null {
  const result: Record<string, UpstreamModelCapability> = {}
  for (const row of rows) {
    const model = row.model.trim()
    const hasAnyValue =
      String(row.contextWindowTokens ?? '').trim() ||
      String(row.maxOutputTokens ?? '').trim() ||
      row.thinkingMode.trim() ||
      row.reasoningEffortsText.trim() ||
      hasPricingValue(row)
    if (!model) {
      if (hasAnyValue) return null
      continue
    }

    const contextWindowTokens = Number(row.contextWindowTokens)
    const maxOutputTokens = Number(row.maxOutputTokens)
    const capability: UpstreamModelCapability = {}

    if (String(row.contextWindowTokens ?? '').trim()) {
      if (!Number.isInteger(contextWindowTokens) || contextWindowTokens < 0) return null
      capability.contextWindowTokens = contextWindowTokens
    }
    if (String(row.maxOutputTokens ?? '').trim()) {
      if (!Number.isInteger(maxOutputTokens) || maxOutputTokens < 0) return null
      capability.maxOutputTokens = maxOutputTokens
    }
    const thinkingMode = row.thinkingMode.trim()
    if (thinkingMode) capability.thinkingMode = thinkingMode

    const reasoningEfforts = row.reasoningEffortsText
      .split(',')
      .map(value => value.trim())
      .filter(Boolean)
    if (reasoningEfforts.length) capability.reasoningEfforts = reasoningEfforts

    const pricing = createPricingFromRow(row)
    if (pricing === false) return null
    if (pricing) capability.pricing = pricing

    result[model] = capability
  }
  return result
}

export function createEmbeddingCapabilityRow(
  id: number,
  model = '',
  capability?: EmbeddingCapability,
): EmbeddingCapabilityRow {
  return {
    id,
    model,
    embeddingSpaceId: capability?.embeddingSpaceId || '',
    dimensions: capability?.dimensions ?? null,
    supportedDimensionsText: capability?.supportedDimensions?.join(', ') || '',
    normalized: capability?.normalized === undefined ? '' : capability.normalized ? 'true' : 'false',
  }
}

export function embeddingCapabilitiesToRows(record: Record<string, EmbeddingCapability> | undefined, nextId: () => number): EmbeddingCapabilityRow[] {
  return Object.entries(record || {})
    .sort(([a], [b]) => a.localeCompare(b))
    .map(([model, capability]) => createEmbeddingCapabilityRow(nextId(), model, capability))
}

function parsePositiveIntegerList(text: string): number[] | null {
  const trimmed = text.trim()
  if (!trimmed) return []
  const parts = trimmed
    .split(/[\s,，;；|]+/)
    .map(part => part.trim())
    .filter(Boolean)
  const values: number[] = []
  const seen = new Set<number>()
  for (const part of parts) {
    const value = Number(part)
    if (!Number.isInteger(value) || value <= 0) return null
    if (!seen.has(value)) {
      seen.add(value)
      values.push(value)
    }
  }
  return values
}

export function embeddingCapabilityRowsToRecord(rows: EmbeddingCapabilityRow[] = []): Record<string, EmbeddingCapability> | null {
  const result: Record<string, EmbeddingCapability> = {}
  for (const row of rows) {
    const model = normalizeSelectableString(row.model).trim()
    const embeddingSpaceId = row.embeddingSpaceId.trim()
    const dimensionsText = String(row.dimensions ?? '').trim()
    const supportedDimensionsText = row.supportedDimensionsText.trim()
    const hasAnyValue = !!(embeddingSpaceId || dimensionsText || supportedDimensionsText || row.normalized)

    if (!model) {
      if (hasAnyValue) return null
      continue
    }
    if (!hasAnyValue) {
      continue
    }

    const capability: EmbeddingCapability = {}
    if (embeddingSpaceId) {
      capability.embeddingSpaceId = embeddingSpaceId
    }
    if (dimensionsText) {
      const dimensions = Number(dimensionsText)
      if (!Number.isInteger(dimensions) || dimensions <= 0) return null
      capability.dimensions = dimensions
    }
    const supportedDimensions = parsePositiveIntegerList(supportedDimensionsText)
    if (supportedDimensions === null) return null
    if (supportedDimensions.length) {
      capability.supportedDimensions = supportedDimensions
    }
    if (row.normalized === 'true') {
      capability.normalized = true
    } else if (row.normalized === 'false') {
      capability.normalized = false
    } else if (row.normalized !== '') {
      return null
    }

    result[model] = capability
  }
  return result
}

export function normalizeSelectableString(value: SelectableString): string {
  if (!value) return ''
  if (typeof value === 'string') {
    const trimmed = value.trim()
    if (!trimmed.startsWith('{')) return value
    try {
      const parsed = JSON.parse(trimmed)
      if (parsed && typeof parsed === 'object' && 'value' in parsed) {
        return normalizeSelectableString(parsed as SelectableString)
      }
    } catch {
      return value
    }
    return value
  }
  if (typeof value.value === 'string') return value.value
  if (value.value != null) return String(value.value)
  return ''
}

export function buildChannelPayload(
  form: ChannelFormLike,
  options: BuildChannelPayloadOptions = {}
): Omit<Channel, 'index' | 'latency' | 'status'> {
  const processedApiKeys = form.apiKeys.map(key => key.trim()).filter(Boolean)
  const processedApiKeySet = new Set(processedApiKeys)
  const processedApiKeyConfigs = (form.apiKeyConfigs || [])
    .filter(cfg => {
      const trimmedKey = typeof cfg?.key === 'string' ? cfg.key.trim() : ''
      return trimmedKey ? processedApiKeySet.has(trimmedKey) : !!cfg?.keyUid
    })
    .map(cfg => {
      const normalized = { ...cfg }
      if (typeof cfg.key === 'string') normalized.key = cfg.key.trim()
      if (typeof cfg.name === 'string') normalized.name = cfg.name.trim() || undefined
      if (typeof cfg.quotaGroup === 'string') normalized.quotaGroup = cfg.quotaGroup.trim() || undefined
      if (Array.isArray(cfg.models)) normalized.models = cfg.models.map(model => model.trim()).filter(Boolean)
      return normalized
    })
  const advancedOptions = normalizeAdvancedChannelOptions(form.serviceType, {
    reasoningMapping: form.reasoningMapping,
    reasoningParamStyle: form.reasoningParamStyle,
    textVerbosity: form.textVerbosity,
    fastMode: form.fastMode
  })

  let sourceUrls = form.baseUrls.length > 0 ? form.baseUrls : [form.baseUrl]
  if (form.serviceType === 'copilot' && sourceUrls.every(url => !url.trim())) {
    sourceUrls = [DEFAULT_COPILOT_BASE_URL]
  }
  const deduplicatedUrls = deduplicateEquivalentBaseUrls(sourceUrls, form.serviceType)

  // 清洗 modelMapping：v-combobox 选中下拉后 key/value 都可能是 { title, value } 对象。
  const cleanModelMapping: Record<string, string> = {}
  for (const [source, target] of Object.entries(form.modelMapping)) {
    const cleanSource = normalizeSelectableString(source).trim()
    const cleanTarget = normalizeSelectableString(target as SelectableString).trim()
    if (cleanSource && cleanTarget) {
      cleanModelMapping[cleanSource] = cleanTarget
    }
  }

  const modelCapabilities = form.modelCapabilityRows
    ? modelCapabilityRowsToRecord(form.modelCapabilityRows)
    : parseModelCapabilitiesText(form.modelCapabilitiesText)
  const embeddingCapabilities = form.embeddingCapabilityRows
    ? embeddingCapabilityRowsToRecord(form.embeddingCapabilityRows)
    : {}

  const normalizeMetadataUserId = options.channelType === undefined
    ? form.normalizeMetadataUserId
    : options.channelType === 'messages' && form.normalizeMetadataUserId

  const isManagedProviderChannel = form.baseUrl.trim() === '' && form.baseUrls.length === 0
  const shouldStripExplicitMappingFields = isManagedProviderChannel && form.serviceType !== 'copilot'

  // 渠道名称不再接受手工修改：非托管渠道按首个 baseURL 自动派生；托管渠道沿用原名称。
  const derivedChannelName = isManagedProviderChannel
    ? form.name.trim()
    : extractChannelNamePrefix(deduplicatedUrls[0] || form.baseUrl)

  const channelData: Omit<Channel, 'index' | 'latency' | 'status'> = {
    name: derivedChannelName,
    remark: form.remark?.trim() ?? '',
    serviceType: form.serviceType as 'openai' | 'gemini' | 'claude' | 'responses' | 'copilot',
    baseUrl: deduplicatedUrls[0] || '',
    website: form.website.trim(),
    insecureSkipVerify: form.insecureSkipVerify,
    lowQuality: form.lowQuality,
    injectDummyThoughtSignature: form.injectDummyThoughtSignature,
    stripThoughtSignature: form.stripThoughtSignature,
    description: form.description.trim(),
    apiKeys: processedApiKeys,
    modelMapping: shouldStripExplicitMappingFields ? {} : cleanModelMapping,
    modelCapabilities: modelCapabilities || {},
    defaultCapability: {},
    allowUnknownContext: false,
    reasoningMapping: shouldStripExplicitMappingFields ? {} : advancedOptions.reasoningMapping,
    reasoningParamStyle: advancedOptions.reasoningParamStyle,
    textVerbosity: advancedOptions.textVerbosity,
    fastMode: advancedOptions.fastMode,
    customHeaders: form.customHeaders,
    proxyUrl: form.proxyUrl.trim(),
    routePrefix: form.routePrefix.trim(),
    supportedModels: form.supportedModels,
    autoBlacklistBalance: form.autoBlacklistBalance,
    normalizeMetadataUserId,
    stripBillingHeader: !!form.stripBillingHeader,
    normalizeSystemRoleToTopLevel: form.normalizeSystemRoleToTopLevel,
    codexToolCompat: form.codexToolCompat,
    stripCodexClientTools: form.codexToolCompat,
    convertImageUrlToB64Json: !!form.convertImageUrlToB64Json,
    noVision: form.noVision,
    noVisionModels: form.noVisionModels,
    visionFallbackModel: normalizeSelectableString(form.visionFallbackModel),
  }

  if (options.channelType === 'vectors') {
    channelData.embeddingCapabilities = embeddingCapabilities || {}
  }

  // 历史图片轮次限制：始终发送（含 0），使编辑场景能把渠道级限制清回不裁剪。
  // 0=不限制；后端会对 >0 的值应用 2-10 约束。空/非整数/负数归一为 0。
  const historicalImageTurnLimit = Number(form.historicalImageTurnLimit)
  channelData.historicalImageTurnLimit = Number.isInteger(historicalImageTurnLimit) && historicalImageTurnLimit > 0
    ? historicalImageTurnLimit
    : 0

  if (form.apiKeyConfigs !== undefined) {
    channelData.apiKeyConfigs = processedApiKeyConfigs
  }

  // 用户自定义标签（始终发送，空数组用于清空标签）
  channelData.tags = (form.tags || []).map(t => t.trim()).filter(Boolean)

  if (deduplicatedUrls.length > 1) {
    channelData.baseUrls = deduplicatedUrls
  }

  if (form.authHeader) {
    channelData.authHeader = form.authHeader
  }

  const requestTimeoutMs = Number(form.requestTimeoutMs)
  if (Number.isInteger(requestTimeoutMs) && requestTimeoutMs >= 1000 && requestTimeoutMs <= 300000) {
    channelData.requestTimeoutMs = requestTimeoutMs
  }

  const responseHeaderTimeoutMs = Number(form.responseHeaderTimeoutMs)
  if (Number.isInteger(responseHeaderTimeoutMs) && responseHeaderTimeoutMs >= 1000 && responseHeaderTimeoutMs <= 300000) {
    channelData.responseHeaderTimeoutMs = responseHeaderTimeoutMs
  }

  // 渠道级计费覆盖：>0 生效；留空/0 发 0 让后端清零（nil=不参与有效成本核算）
  const costMultiplier = Number(form.costMultiplier)
  channelData.costMultiplier = Number.isFinite(costMultiplier) && costMultiplier > 0 ? costMultiplier : 0
  const exchangeRate = Number(form.exchangeRate)
  channelData.exchangeRate = Number.isFinite(exchangeRate) && exchangeRate > 0 ? exchangeRate : 0

  const streamFirstContentTimeoutMs = Number(form.streamFirstContentTimeoutMs)
  if (Number.isInteger(streamFirstContentTimeoutMs) && streamFirstContentTimeoutMs > 0) {
    channelData.streamFirstContentTimeoutMs = streamFirstContentTimeoutMs
  }

  const streamInactivityTimeoutMs = Number(form.streamInactivityTimeoutMs)
  if (Number.isInteger(streamInactivityTimeoutMs) && streamInactivityTimeoutMs > 0) {
    channelData.streamInactivityTimeoutMs = streamInactivityTimeoutMs
  }

  const streamToolCallIdleTimeoutMs = Number(form.streamToolCallIdleTimeoutMs)
  if (Number.isInteger(streamToolCallIdleTimeoutMs) && streamToolCallIdleTimeoutMs >= 30000) {
    channelData.streamToolCallIdleTimeoutMs = streamToolCallIdleTimeoutMs
  }

  const rateLimitRpm = Number(form.rateLimitRpm)
  if (Number.isInteger(rateLimitRpm) && rateLimitRpm > 0) {
    channelData.rateLimitRpm = rateLimitRpm
  }

  const rateLimitWindowMinutes = Number(form.rateLimitWindowMinutes)
  if (Number.isInteger(rateLimitWindowMinutes) && rateLimitWindowMinutes > 0) {
    channelData.rateLimitWindowMinutes = rateLimitWindowMinutes
  }

  const rateLimitBurst = Number(form.rateLimitBurst)
  if (Number.isInteger(rateLimitBurst) && rateLimitBurst > 0) {
    channelData.rateLimitBurst = rateLimitBurst
  }

  const rateLimitMaxConcurrent = Number(form.rateLimitMaxConcurrent)
  if (Number.isInteger(rateLimitMaxConcurrent) && rateLimitMaxConcurrent > 0) {
    channelData.rateLimitMaxConcurrent = rateLimitMaxConcurrent
  }

  channelData.rateLimitAutoFromHeaders = !!form.rateLimitAutoFromHeaders

  return channelData
}
