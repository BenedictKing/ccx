import type { Channel } from '../services/api'

export const EDIT_CHANNEL_PAYLOAD_KEYS = [
  'name', 'serviceType', 'baseUrl', 'baseUrls', 'website', 'insecureSkipVerify',
  'lowQuality', 'injectDummyThoughtSignature', 'stripThoughtSignature', 'description',
  'apiKeys', 'apiKeyConfigs', 'maxGroupMultiplier', 'modelMapping', 'subagentModel', 'modelCapabilities', 'embeddingCapabilities', 'defaultCapability', 'allowUnknownContext',
  'reasoningMapping', 'reasoningParamStyle', 'textVerbosity',
  'fastMode', 'customHeaders', 'proxyUrl', 'authHeader', 'requestTimeoutMs', 'responseHeaderTimeoutMs', 'streamFirstContentTimeoutMs', 'streamInactivityTimeoutMs', 'streamToolCallIdleTimeoutMs', 'routePrefix', 'supportedModels',
  'rateLimitRpm', 'rateLimitWindowMinutes', 'rateLimitMaxConcurrent', 'rateLimitAutoFromHeaders',
  'autoBlacklistBalance', 'circuitBreakerDisabled', 'normalizeMetadataUserId', 'stripBillingHeader', 'passbackThinkingBlocks', 'stripEmptyTextBlocks', 'normalizeSystemRoleToTopLevel', 'codexNativeToolPassthrough',
  'codexToolCompat', 'normalizeNonstandardChatRoles', 'stripCodexClientTools', 'stripImageGenerationTool', 'convertImageUrlToB64Json',
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
