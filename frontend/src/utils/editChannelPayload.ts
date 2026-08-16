import type { ComputedRef } from 'vue'
import type { Channel } from '../services/api'

export const EDIT_CHANNEL_PAYLOAD_KEYS = [
  'name', 'remark', 'serviceType', 'baseUrl', 'baseUrls', 'website', 'insecureSkipVerify',
  'lowQuality', 'injectDummyThoughtSignature', 'stripThoughtSignature', 'description',
  'apiKeys', 'apiKeyConfigs', 'modelMapping', 'modelCapabilities', 'embeddingCapabilities', 'defaultCapability', 'allowUnknownContext',
  'reasoningMapping', 'reasoningParamStyle', 'textVerbosity',
  'fastMode', 'customHeaders', 'proxyUrl', 'costMultiplier', 'channelPaymentCurrency', 'channelPaymentAmount', 'channelCreditCurrency', 'channelCreditAmount', 'authHeader', 'requestTimeoutMs', 'responseHeaderTimeoutMs', 'streamFirstContentTimeoutMs', 'streamInactivityTimeoutMs', 'streamToolCallIdleTimeoutMs', 'routePrefix', 'supportedModels',
  'rateLimitRpm', 'rateLimitWindowMinutes', 'rateLimitMaxConcurrent', 'rateLimitAutoFromHeaders',
  'autoBlacklistBalance', 'normalizeMetadataUserId', 'stripBillingHeader', 'normalizeSystemRoleToTopLevel',
  'codexToolCompat', 'stripCodexClientTools', 'convertImageUrlToB64Json', 'tags',
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

type HandleTestCapabilityOptions = {
  buildSubmitPayload: () => Omit<Channel, 'index' | 'latency' | 'status'>
  channel: ComputedRef<Channel | null | undefined>
  emitSave: (channel: Omit<Channel, 'index' | 'latency' | 'status'>, options?: { triggerCapabilityTest?: boolean }) => void
  emitTestCapability: (channelId: number) => void
  formRef: { value: { validate: () => Promise<{ valid: boolean }> } | undefined }
  modelCapabilitiesError: ComputedRef<string>
  syncModelCapabilitiesFromMapping: () => void
  syncModelMappingToForm: () => void
}

export function createHandleTestCapability(options: HandleTestCapabilityOptions) {
  return async () => {
    const channel = options.channel.value
    if (channel?.index === undefined || channel?.index === null) {
      return
    }

    if (!options.formRef.value) return
    options.syncModelCapabilitiesFromMapping()
    const { valid } = await options.formRef.value.validate()
    if (!valid) return
    if (options.modelCapabilitiesError.value) return

    options.syncModelMappingToForm()

    const channelData = options.buildSubmitPayload()
    const original = extractEditChannelPayloadFields(channel)
    const hasChanges = JSON.stringify(channelData) !== JSON.stringify(original)

    if (hasChanges) {
      options.emitSave(channelData, { triggerCapabilityTest: true })
    } else {
      options.emitTestCapability(channel.index)
    }
  }
}
