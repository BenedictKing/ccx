import type { Channel } from '../services/api'

export const EDIT_CHANNEL_PAYLOAD_KEYS = [
  'name', 'remark', 'serviceType', 'baseUrl', 'baseUrls', 'website', 'insecureSkipVerify',
  'lowQuality', 'injectDummyThoughtSignature', 'stripThoughtSignature', 'description',
  'apiKeys', 'apiKeyConfigs', 'modelCapabilities', 'embeddingCapabilities', 'defaultCapability', 'allowUnknownContext',
  'reasoningParamStyle', 'textVerbosity',
  'fastMode', 'customHeaders', 'proxyUrl', 'costMultiplier', 'maxGroupMultiplier', 'channelPaymentCurrency', 'channelPaymentAmount', 'channelCreditCurrency', 'channelCreditAmount', 'authHeader', 'requestTimeoutMs', 'responseHeaderTimeoutMs', 'streamFirstContentTimeoutMs', 'streamInactivityTimeoutMs', 'streamToolCallIdleTimeoutMs', 'routePrefix', 'supportedModels',
  'rateLimitRpm', 'rateLimitWindowMinutes', 'rateLimitMaxConcurrent', 'rateLimitAutoFromHeaders',
  'autoBlacklistBalance', 'normalizeMetadataUserId', 'stripBillingHeader', 'normalizeSystemRoleToTopLevel',
  'codexToolCompat', 'stripCodexClientTools', 'convertImageUrlToB64Json', 'tags', 'racing',
] as const

export function extractEditChannelPayloadFields(channel: Channel): Record<string, unknown> {
  const result: Record<string, unknown> = {}
  for (const key of EDIT_CHANNEL_PAYLOAD_KEYS) {
    if (key in channel) {
      result[key] = channel[key as keyof Channel]
    }
  }
  return result
}
