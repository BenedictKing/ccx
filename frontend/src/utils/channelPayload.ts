import type { Channel } from '../services/api'
import { normalizeAdvancedChannelOptions } from './channelAdvancedOptions'

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
  autoBlacklistBalance: boolean
  normalizeMetadataUserId: boolean
  streamPassthroughEnabled: boolean
  sub2apiPassthroughEnabled: boolean
  keyAffinityEnabled?: boolean
  strictRequestPassthroughEnabled: boolean
  modelsHealthCheckEnabled?: boolean
  modelsHealthCheckIntervalMinutes?: number
  failoverRules: NonNullable<Channel['failoverRules']>
  rpm?: number

}

function normalizeBaseUrlPreservingHash(url: string): string {
  const trimmed = url.trim()
  if (!trimmed) return ''

  const hasHashSuffix = trimmed.endsWith('#')
  const withoutHash = hasHashSuffix ? trimmed.slice(0, -1) : trimmed
  const normalized = withoutHash.replace(/\/+$/, '')

  return hasHashSuffix ? normalized + '#' : normalized
}

function getBaseUrlDeduplicationKey(url: string): string {
  return normalizeBaseUrlPreservingHash(url).replace(/#$/, '')
}

export function buildChannelPayload(form: ChannelFormLike): Omit<Channel, 'index' | 'latency' | 'status'> {
  const isClaude = form.serviceType === 'claude'
  const sub2apiPassthroughEnabled = isClaude ? !!form.sub2apiPassthroughEnabled : false
  const streamPassthroughEnabled = isClaude ? (!!form.streamPassthroughEnabled && !sub2apiPassthroughEnabled) : true
  const modelsHealthCheckEnabled = !!form.modelsHealthCheckEnabled
  const modelsHealthCheckIntervalMinutes = form.modelsHealthCheckIntervalMinutes && form.modelsHealthCheckIntervalMinutes > 0
    ? Math.floor(form.modelsHealthCheckIntervalMinutes)
    : 60

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

  const sourceUrls = (form.baseUrls.length > 0 ? form.baseUrls : [form.baseUrl])
    .map(normalizeBaseUrlPreservingHash)
    .filter(Boolean)

  const urlMap = new Map<string, string>()
  sourceUrls.forEach(url => {
    const key = getBaseUrlDeduplicationKey(url)
    const existing = urlMap.get(key)
    if (!existing || (!existing.endsWith('#') && url.endsWith('#'))) {
      urlMap.set(key, url)
    }
  })
  const deduplicatedUrls = Array.from(urlMap.values())

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
    autoBlacklistBalance: form.autoBlacklistBalance,
    normalizeMetadataUserId: form.normalizeMetadataUserId,
    streamPassthroughEnabled,
    sub2apiPassthroughEnabled,
    keyAffinityEnabled: form.serviceType === 'claude' ? (form.keyAffinityEnabled ?? true) : !!form.keyAffinityEnabled,
    strictRequestPassthroughEnabled: form.serviceType === 'claude' ? form.strictRequestPassthroughEnabled : true,
    modelsHealthCheckEnabled,
    modelsHealthCheckIntervalMinutes,
    failoverRules: normalizedFailoverRules,
    rpm: form.rpm && form.rpm > 0 ? form.rpm : 10
  }

  if (deduplicatedUrls.length > 1) {
    channelData.baseUrls = deduplicatedUrls
  }

  return channelData
}
