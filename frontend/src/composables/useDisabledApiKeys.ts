import { computed, ref, type ComputedRef } from 'vue'
import { ApiService, type Channel } from '../services/api'
import type { APIKeyConfig } from '../services/api-types'

type ChannelType = 'messages' | 'chat' | 'responses' | 'gemini' | 'images' | 'vectors'
type FormLike = {
  apiKeys: string[]
  apiKeyConfigs?: APIKeyConfig[]
}

type DisabledApiKeyOptions = {
  apiService: ApiService
  channel: ComputedRef<Channel | null | undefined>
  channelType: ComputedRef<ChannelType>
  emitError: (message: string) => void
  form: FormLike
}

export function useDisabledApiKeys(options: DisabledApiKeyOptions) {
  const restoringKey = ref('')
  const localRestoredKeys = ref(new Set<string>())
  const restoringKeyModel = ref('')
  const localRestoredKeyModels = ref(new Set<string>())
  const suspendingKey = ref('')
  const localSuspendedKeys = ref(new Set<string>())
  const localResumedKeys = ref(new Set<string>())

  const keyModelKey = (apiKey: string, model: string) => `${apiKey}|${model}`
  const channelId = (channel: Channel) => channel.routeIndex ?? channel.index

  const markKeySuspended = (apiKey: string) => {
    const configs = options.form.apiKeyConfigs ?? []
    const existingIndex = configs.findIndex(config => config.key === apiKey)
    if (existingIndex < 0) {
      options.form.apiKeyConfigs = [...configs, { key: apiKey, enabled: false }]
      return
    }
    options.form.apiKeyConfigs = configs.map((config, index) => (
      index === existingIndex ? { ...config, enabled: false } : config
    ))
  }

  const markKeyResumed = (apiKey: string) => {
    const configs = options.form.apiKeyConfigs ?? []
    const existingIndex = configs.findIndex(config => config.key === apiKey)
    if (existingIndex < 0) return

    const resumedConfig = { ...configs[existingIndex] }
    delete resumedConfig.enabled
    const hasOtherConfig = Object.keys(resumedConfig).some(key => key !== 'key')
    const nextConfigs = hasOtherConfig
      ? configs.map((config, index) => index === existingIndex ? resumedConfig : config)
      : configs.filter((_, index) => index !== existingIndex)
    options.form.apiKeyConfigs = nextConfigs.length > 0 ? nextConfigs : undefined
  }

  const disabledKeys = computed(() => options.channel.value?.disabledApiKeys || [])
  const visibleDisabledKeys = computed(() =>
    (options.channel.value?.disabledApiKeys || []).filter(dk => !localRestoredKeys.value.has(dk.key))
  )

  const disabledKeyModels = computed(() => options.channel.value?.disabledKeyModels || [])
  const visibleDisabledKeyModels = computed(() =>
    (options.channel.value?.disabledKeyModels || []).filter(
      dm => !localRestoredKeyModels.value.has(keyModelKey(dm.key, dm.model))
    )
  )

  const resetRestoredKeys = () => {
    localRestoredKeys.value = new Set<string>()
    restoringKey.value = ''
    localRestoredKeyModels.value = new Set<string>()
    restoringKeyModel.value = ''
    localSuspendedKeys.value = new Set<string>()
    localResumedKeys.value = new Set<string>()
    suspendingKey.value = ''
  }

  const restoreDisabledKey = async (apiKey: string) => {
    const channel = options.channel.value
    if (!channel || restoringKey.value) return
    restoringKey.value = apiKey
    try {
      const id = channelId(channel)
      switch (options.channelType.value) {
        case 'chat':
          await options.apiService.restoreChatApiKey(id, apiKey)
          break
        case 'images':
          await options.apiService.restoreImagesApiKey(id, apiKey)
          break
        case 'vectors':
          await options.apiService.restoreVectorsApiKey(id, apiKey)
          break
        case 'gemini':
          await options.apiService.restoreGeminiApiKey(id, apiKey)
          break
        case 'responses':
          await options.apiService.restoreResponsesApiKey(id, apiKey)
          break
        default:
          await options.apiService.restoreApiKey(id, apiKey)
      }
      localRestoredKeys.value = new Set([...localRestoredKeys.value, apiKey])
      if (!options.form.apiKeys.includes(apiKey)) {
        options.form.apiKeys = [...options.form.apiKeys, apiKey]
      }
    } catch (error) {
      options.emitError(error instanceof Error ? error.message : 'Restore failed')
    } finally {
      restoringKey.value = ''
    }
  }

  const restoreDisabledKeyModel = async (apiKey: string, model: string) => {
    const channel = options.channel.value
    const key = keyModelKey(apiKey, model)
    if (!channel || restoringKeyModel.value) return
    restoringKeyModel.value = key
    try {
      const id = channelId(channel)
      switch (options.channelType.value) {
        case 'chat':
          await options.apiService.restoreChatKeyModel(id, apiKey, model)
          break
        case 'images':
          await options.apiService.restoreImagesKeyModel(id, apiKey, model)
          break
        case 'vectors':
          await options.apiService.restoreVectorsKeyModel(id, apiKey, model)
          break
        case 'gemini':
          await options.apiService.restoreGeminiKeyModel(id, apiKey, model)
          break
        case 'responses':
          await options.apiService.restoreResponsesKeyModel(id, apiKey, model)
          break
        default:
          await options.apiService.restoreKeyModel(id, apiKey, model)
      }
      localRestoredKeyModels.value = new Set([...localRestoredKeyModels.value, key])
    } catch (error) {
      options.emitError(error instanceof Error ? error.message : 'Restore failed')
    } finally {
      restoringKeyModel.value = ''
    }
  }

  const suspendKey = async (apiKey: string) => {
    const channel = options.channel.value
    if (!channel || suspendingKey.value) return
    suspendingKey.value = apiKey
    try {
      const id = channelId(channel)
      switch (options.channelType.value) {
        case 'chat':
          await options.apiService.suspendChatApiKey(id, apiKey)
          break
        case 'images':
          await options.apiService.suspendImagesApiKey(id, apiKey)
          break
        case 'vectors':
          await options.apiService.suspendVectorsApiKey(id, apiKey)
          break
        case 'gemini':
          await options.apiService.suspendGeminiApiKey(id, apiKey)
          break
        case 'responses':
          await options.apiService.suspendResponsesApiKey(id, apiKey)
          break
        default:
          await options.apiService.suspendApiKey(id, apiKey)
      }
      markKeySuspended(apiKey)
      localSuspendedKeys.value = new Set([...localSuspendedKeys.value, apiKey])
      localResumedKeys.value.delete(apiKey)
    } catch (error) {
      options.emitError(error instanceof Error ? error.message : 'Suspend failed')
    } finally {
      suspendingKey.value = ''
    }
  }

  const resumeKey = async (apiKey: string) => {
    const channel = options.channel.value
    if (!channel || suspendingKey.value) return
    suspendingKey.value = apiKey
    try {
      const id = channelId(channel)
      switch (options.channelType.value) {
        case 'chat':
          await options.apiService.resumeChatApiKey(id, apiKey)
          break
        case 'images':
          await options.apiService.resumeImagesApiKey(id, apiKey)
          break
        case 'vectors':
          await options.apiService.resumeVectorsApiKey(id, apiKey)
          break
        case 'gemini':
          await options.apiService.resumeGeminiApiKey(id, apiKey)
          break
        case 'responses':
          await options.apiService.resumeResponsesApiKey(id, apiKey)
          break
        default:
          await options.apiService.resumeApiKey(id, apiKey)
      }
      markKeyResumed(apiKey)
      localResumedKeys.value = new Set([...localResumedKeys.value, apiKey])
      localSuspendedKeys.value.delete(apiKey)
    } catch (error) {
      options.emitError(error instanceof Error ? error.message : 'Resume failed')
    } finally {
      suspendingKey.value = ''
    }
  }

  return {
    restoringKey,
    localRestoredKeys,
    disabledKeys,
    visibleDisabledKeys,
    resetRestoredKeys,
    restoreDisabledKey,
    restoringKeyModel,
    disabledKeyModels,
    visibleDisabledKeyModels,
    restoreDisabledKeyModel,
    suspendingKey,
    suspendKey,
    resumeKey,
    localSuspendedKeys,
    localResumedKeys,
  }
}
