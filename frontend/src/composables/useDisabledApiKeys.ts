import { computed, ref, type ComputedRef } from 'vue'
import { ApiService, type Channel } from '../services/api'
import type { APIKeyConfig, DisabledGroupModelInfo } from '../services/api-types'
import { groupModelPolicyKey } from '../utils/channelApiKeys'

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
  const removingKey = ref('')
  const localRemovedKeys = ref(new Set<string>())
  const changingGroupModel = ref('')
  const localDisabledGroupModels = ref<DisabledGroupModelInfo[]>([])
  const localRestoredGroupModels = ref(new Set<string>())

  const keyModelKey = (apiKey: string, model: string) => `${apiKey}|${model}`

  const disabledKeys = computed(() => options.channel.value?.disabledApiKeys || [])
  const visibleDisabledKeys = computed(() =>
    (options.channel.value?.disabledApiKeys || []).filter(
      dk => !localRestoredKeys.value.has(dk.key) && !localRemovedKeys.value.has(dk.key)
    )
  )

  const disabledKeyModels = computed(() => options.channel.value?.disabledKeyModels || [])
  const visibleDisabledKeyModels = computed(() =>
    (options.channel.value?.disabledKeyModels || []).filter(
      dm => !localRestoredKeyModels.value.has(keyModelKey(dm.key, dm.model))
    )
  )

  const visibleDisabledGroupModels = computed(() => {
    const records = new Map<string, DisabledGroupModelInfo>()
    for (const record of [...(options.channel.value?.disabledGroupModels || []), ...localDisabledGroupModels.value]) {
      records.set(groupModelPolicyKey(record), record)
    }
    return [...records.values()].filter(record => !localRestoredGroupModels.value.has(groupModelPolicyKey(record)))
  })

  const suspendingKey = ref('')

  const resetRestoredKeys = () => {
    localRestoredKeys.value = new Set<string>()
    restoringKey.value = ''
    localRestoredKeyModels.value = new Set<string>()
    restoringKeyModel.value = ''
    localRemovedKeys.value = new Set<string>()
    removingKey.value = ''
    localDisabledGroupModels.value = []
    localRestoredGroupModels.value = new Set<string>()
    changingGroupModel.value = ''
    suspendingKey.value = ''
  }

  const setKeyEnabled = (apiKey: string, enabled: boolean | undefined) => {
    let found = false
    const configs = (options.form.apiKeyConfigs || []).map(config => {
      if (config.key !== apiKey) return config
      found = true
      return { ...config, enabled }
    })
    if (!found && enabled !== undefined) configs.push({ key: apiKey, enabled })
    options.form.apiKeyConfigs = configs
  }

  const setKeySuspended = async (apiKey: string, suspended: boolean) => {
    const channel = options.channel.value
    if (!channel || suspendingKey.value) return
    suspendingKey.value = apiKey
    try {
      await options.apiService.setApiKeySuspended(options.channelType.value, channel.index, apiKey, suspended)
      setKeyEnabled(apiKey, suspended ? false : undefined)
    } catch (error) {
      options.emitError(error instanceof Error ? error.message : suspended ? 'Suspend failed' : 'Resume failed')
    } finally {
      suspendingKey.value = ''
    }
  }

  const suspendKey = (apiKey: string) => setKeySuspended(apiKey, true)
  const resumeKey = (apiKey: string) => setKeySuspended(apiKey, false)

  const removeDisabledKey = async (apiKey: string) => {
    const channel = options.channel.value
    if (!channel || removingKey.value) return
    removingKey.value = apiKey
    try {
      const channelId = channel.index
      switch (options.channelType.value) {
        case 'chat':
          await options.apiService.removeChatApiKey(channelId, apiKey)
          break
        case 'images':
          await options.apiService.removeImagesApiKey(channelId, apiKey)
          break
        case 'vectors':
          await options.apiService.removeVectorsApiKey(channelId, apiKey)
          break
        case 'gemini':
          await options.apiService.removeGeminiApiKey(channelId, apiKey)
          break
        case 'responses':
          await options.apiService.removeResponsesApiKey(channelId, apiKey)
          break
        default:
          await options.apiService.removeApiKey(channelId, apiKey)
      }
      localRemovedKeys.value = new Set([...localRemovedKeys.value, apiKey])
      options.form.apiKeys = options.form.apiKeys.filter(key => key !== apiKey)
    } catch (error) {
      options.emitError(error instanceof Error ? error.message : 'Remove failed')
    } finally {
      removingKey.value = ''
    }
  }

  const disableGroupModel = async (apiKey: string, model: string) => {
    const channel = options.channel.value
    const normalizedModel = model.trim()
    if (!channel || !normalizedModel || changingGroupModel.value) return
    changingGroupModel.value = `${apiKey}|${normalizedModel}`
    try {
      const result = await options.apiService.updateGroupModelRestriction(
        options.channelType.value, channel.index, apiKey, '', normalizedModel, true
      )
      const record: DisabledGroupModelInfo = {
        quotaGroup: result.quotaGroup,
        key: result.quotaGroup ? undefined : apiKey,
        model: result.model,
        disabledAt: new Date().toISOString(),
      }
      const policyKey = groupModelPolicyKey(record)
      localDisabledGroupModels.value = [
        ...localDisabledGroupModels.value.filter(item => groupModelPolicyKey(item) !== policyKey),
        record,
      ]
      localRestoredGroupModels.value.delete(policyKey)
    } catch (error) {
      options.emitError(error instanceof Error ? error.message : 'Disable failed')
    } finally {
      changingGroupModel.value = ''
    }
  }

  const restoreDisabledGroupModel = async (record: DisabledGroupModelInfo) => {
    const channel = options.channel.value
    const policyKey = groupModelPolicyKey(record)
    if (!channel || changingGroupModel.value) return
    changingGroupModel.value = policyKey
    try {
      await options.apiService.updateGroupModelRestriction(
        options.channelType.value,
        channel.index,
        record.key || '',
        record.quotaGroup || '',
        record.model,
        false
      )
      localRestoredGroupModels.value = new Set([...localRestoredGroupModels.value, policyKey])
      localDisabledGroupModels.value = localDisabledGroupModels.value.filter(item => groupModelPolicyKey(item) !== policyKey)
    } catch (error) {
      options.emitError(error instanceof Error ? error.message : 'Restore failed')
    } finally {
      changingGroupModel.value = ''
    }
  }

  const restoreDisabledKey = async (apiKey: string) => {
    const channel = options.channel.value
    if (!channel) return
    restoringKey.value = apiKey
    try {
      const channelId = channel.index
      switch (options.channelType.value) {
        case 'chat':
          await options.apiService.restoreChatApiKey(channelId, apiKey)
          break
        case 'images':
          await options.apiService.restoreImagesApiKey(channelId, apiKey)
          break
        case 'vectors':
          await options.apiService.restoreVectorsApiKey(channelId, apiKey)
          break
        case 'gemini':
          await options.apiService.restoreGeminiApiKey(channelId, apiKey)
          break
        case 'responses':
          await options.apiService.restoreResponsesApiKey(channelId, apiKey)
          break
        default:
          await options.apiService.restoreApiKey(channelId, apiKey)
      }
      localRestoredKeys.value.add(apiKey)
      options.form.apiKeys.push(apiKey)
    } catch (error) {
      options.emitError(error instanceof Error ? error.message : 'Restore failed')
    } finally {
      restoringKey.value = ''
    }
  }

  const restoreDisabledKeyModel = async (apiKey: string, model: string) => {
    const channel = options.channel.value
    if (!channel) return
    restoringKeyModel.value = keyModelKey(apiKey, model)
    try {
      const channelId = channel.index
      switch (options.channelType.value) {
        case 'chat':
          await options.apiService.restoreChatKeyModel(channelId, apiKey, model)
          break
        case 'images':
          await options.apiService.restoreImagesKeyModel(channelId, apiKey, model)
          break
        case 'vectors':
          await options.apiService.restoreVectorsKeyModel(channelId, apiKey, model)
          break
        case 'gemini':
          await options.apiService.restoreGeminiKeyModel(channelId, apiKey, model)
          break
        case 'responses':
          await options.apiService.restoreResponsesKeyModel(channelId, apiKey, model)
          break
        default:
          await options.apiService.restoreKeyModel(channelId, apiKey, model)
      }
      localRestoredKeyModels.value.add(keyModelKey(apiKey, model))
    } catch (error) {
      options.emitError(error instanceof Error ? error.message : 'Restore failed')
    } finally {
      restoringKeyModel.value = ''
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
    removingKey,
    removeDisabledKey,
    changingGroupModel,
    visibleDisabledGroupModels,
    disableGroupModel,
    restoreDisabledGroupModel,
  }
}
