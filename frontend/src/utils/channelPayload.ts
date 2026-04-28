import type { Channel } from '../services/api'
import { normalizeAdvancedChannelOptions } from './channelAdvancedOptions'
import { deduplicateEquivalentBaseUrls } from './baseUrlSemantics'

export interface ChannelFormLike {
  name: string
  serviceType: 'openai' | 'gemini' | 'claude' | 'responses' | ''
  baseUrl: string
  baseUrls: string[]
  website: string
  insecureSkipVerify: boolean
  lowQuality: boolean
  injectDummyThoughtSignature: boolean
  stripThoughtSignature: boolean
  description: string
  apiKeys: string[]
  modelMapping: Record<string, string>
  reasoningMapping: Record<string, 'none' | 'low' | 'medium' | 'high' | 'xhigh'>
  textVerbosity: 'low' | 'medium' | 'high' | ''
  fastMode: boolean
  customHeaders: Record<string, string>
  proxyUrl: string
  routePrefix: string
  supportedModels: string[]
  modelsResponseMode?: 'upstream' | 'manual'
  manualModels?: string[]
  autoBlacklistBalance: boolean
  normalizeMetadataUserId: boolean
  streamPassthroughEnabled: boolean
  sub2apiPassthroughEnabled: boolean
  keyAffinityEnabled?: boolean
  strictRequestPassthroughEnabled: boolean
  modelsHealthCheckEnabled?: boolean
  modelsHealthCheckIntervalMinutes?: number
  failoverRules: NonNullable<Channel['failoverRules']>
}

export function buildChannelPayload(form: ChannelFormLike): Omit<Channel, 'index' | 'latency' | 'status'> {
  const isClaude = form.serviceType === 'claude'
  const sub2apiPassthroughEnabled = isClaude ? !!form.sub2apiPassthroughEnabled : false
  const streamPassthroughEnabled = isClaude ? (!!form.streamPassthroughEnabled && !sub2apiPassthroughEnabled) : true
  const modelsHealthCheckEnabled = !!form.modelsHealthCheckEnabled
  const modelsHealthCheckIntervalMinutes = form.modelsHealthCheckIntervalMinutes && form.modelsHealthCheckIntervalMinutes > 0
    ? Math.floor(form.modelsHealthCheckIntervalMinutes)
    : 60
  const modelsResponseMode = form.modelsResponseMode === 'manual' ? 'manual' : 'upstream'
  const manualModels = (form.manualModels || []).map(model => model.trim()).filter(Boolean)

  const processedApiKeys = form.apiKeys.filter(key => key.trim())
  const normalizedFailoverRules = (form.serviceType === 'claude' ? (form.failoverRules || []) : [])
    .map(rule => ({
      description: (rule.description || '').trim(),
      action: rule.action,
      statusCodes: (rule.statusCodes || []).filter(code => Number.isInteger(code) && code >= 100 && code <= 599),
      errorCodes: (rule.errorCodes || []).map(code => code.trim()).filter(Boolean),
      keywords: (rule.keywords || []).map(keyword => keyword.trim()).filter(Boolean),
      durationMinutes: rule.durationMinutes && rule.durationMinutes > 0 ? Math.floor(rule.durationMinutes) : undefined
    }))
    .filter(rule => rule.action && (rule.statusCodes.length > 0 || rule.errorCodes.length > 0 || rule.keywords.length > 0))

  const advancedOptions = normalizeAdvancedChannelOptions(form.serviceType, {
    reasoningMapping: form.reasoningMapping,
    textVerbosity: form.textVerbosity,
    fastMode: form.fastMode
  })

  const sourceUrls = form.baseUrls.length > 0 ? form.baseUrls : [form.baseUrl]
  const deduplicatedUrls = deduplicateEquivalentBaseUrls(sourceUrls, form.serviceType)

  const channelData: Omit<Channel, 'index' | 'latency' | 'status'> = {
    name: form.name.trim(),
    serviceType: form.serviceType as 'openai' | 'gemini' | 'claude' | 'responses',
    baseUrl: deduplicatedUrls[0] || '',
    website: form.website.trim(),
    insecureSkipVerify: form.insecureSkipVerify,
    lowQuality: form.lowQuality,
    injectDummyThoughtSignature: form.injectDummyThoughtSignature,
    stripThoughtSignature: form.stripThoughtSignature,
    description: form.description.trim(),
    apiKeys: processedApiKeys,
    modelMapping: form.modelMapping,
    reasoningMapping: advancedOptions.reasoningMapping,
    textVerbosity: advancedOptions.textVerbosity,
    fastMode: advancedOptions.fastMode,
    customHeaders: form.customHeaders,
    proxyUrl: form.proxyUrl.trim(),
    routePrefix: form.routePrefix.trim(),
    supportedModels: form.supportedModels,
    modelsResponseMode,
    manualModels,
    autoBlacklistBalance: form.autoBlacklistBalance,
    normalizeMetadataUserId: form.normalizeMetadataUserId,
    streamPassthroughEnabled,
    sub2apiPassthroughEnabled,
    keyAffinityEnabled: form.serviceType === 'claude' ? (form.keyAffinityEnabled ?? true) : !!form.keyAffinityEnabled,
    strictRequestPassthroughEnabled: form.serviceType === 'claude' ? form.strictRequestPassthroughEnabled : true,
    modelsHealthCheckEnabled,
    modelsHealthCheckIntervalMinutes,
    failoverRules: normalizedFailoverRules
  }

  if (deduplicatedUrls.length > 1) {
    channelData.baseUrls = deduplicatedUrls
  }

  return channelData
}
