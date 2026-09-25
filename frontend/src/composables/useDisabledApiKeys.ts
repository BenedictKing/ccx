import { computed, ref, type ComputedRef } from 'vue'
import { ApiService, type Channel } from '../services/api'
import type { APIKeyConfig } from '../services/api-types'
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
  onKeysChanged?: () => Promise<void>
}

type KeyRoute = {
  kind: ChannelType
  index: number
}

export function useDisabledApiKeys(options: DisabledApiKeyOptions) {
  const restoringKey = ref('')
  const localRestoredKeys = ref(new Set<string>())
  const removingKey = ref('')
  const localRemovedKeys = ref(new Set<string>())
  const restoringKeyModel = ref('')
  const localRestoredKeyModels = ref(new Set<string>())
  const changingGroupModel = ref('')
  const localDisabledGroupModels = ref<Array<{ quotaGroup: string; key?: string; model: string; disabledAt: string }>>([])
  const localRestoredGroupModels = ref(new Set<string>())
  // 分组模型排除暂存：行内面板暂存、渠道主保存成功后统一提交（flushStagedGroupModelDisables）。
  const pendingGroupModelDisables = ref<Array<{ key: string; model: string }>>([])
  const suspendingKey = ref('')
  const localSuspendedKeys = ref(new Set<string>())
  const localResumedKeys = ref(new Set<string>())

  const keyModelKey = (apiKey: string, model: string) => `${apiKey}|${model}`
  const channelId = (channel: Channel) => channel.routeIndex ?? channel.index

  const keyRoutes = (channel: Channel, apiKey: string): KeyRoute[] => {
    const routes = (channel.protocolRoutes ?? []).filter(route => route.apiKeys?.includes(apiKey))
    if (routes.length > 0) {
      return routes.map(route => ({ kind: route.kind, index: route.index }))
    }
    return [{ kind: options.channelType.value, index: channelId(channel) }]
  }

  const suspendKeyAtRoute = (route: KeyRoute, apiKey: string): Promise<void> => {
    switch (route.kind) {
      case 'chat': return options.apiService.suspendChatApiKey(route.index, apiKey)
      case 'images': return options.apiService.suspendImagesApiKey(route.index, apiKey)
      case 'vectors': return options.apiService.suspendVectorsApiKey(route.index, apiKey)
      case 'gemini': return options.apiService.suspendGeminiApiKey(route.index, apiKey)
      case 'responses': return options.apiService.suspendResponsesApiKey(route.index, apiKey)
      default: return options.apiService.suspendApiKey(route.index, apiKey)
    }
  }

  const resumeKeyAtRoute = (route: KeyRoute, apiKey: string): Promise<void> => {
    switch (route.kind) {
      case 'chat': return options.apiService.resumeChatApiKey(route.index, apiKey)
      case 'images': return options.apiService.resumeImagesApiKey(route.index, apiKey)
      case 'vectors': return options.apiService.resumeVectorsApiKey(route.index, apiKey)
      case 'gemini': return options.apiService.resumeGeminiApiKey(route.index, apiKey)
      case 'responses': return options.apiService.resumeResponsesApiKey(route.index, apiKey)
      default: return options.apiService.resumeApiKey(route.index, apiKey)
    }
  }

  const restoreKeyAtRoute = (route: KeyRoute, apiKey: string): Promise<void> => {
    switch (route.kind) {
      case 'chat': return options.apiService.restoreChatApiKey(route.index, apiKey)
      case 'images': return options.apiService.restoreImagesApiKey(route.index, apiKey)
      case 'vectors': return options.apiService.restoreVectorsApiKey(route.index, apiKey)
      case 'gemini': return options.apiService.restoreGeminiApiKey(route.index, apiKey)
      case 'responses': return options.apiService.restoreResponsesApiKey(route.index, apiKey)
      default: return options.apiService.restoreApiKey(route.index, apiKey)
    }
  }

  const removeKeyAtRoute = (route: KeyRoute, apiKey: string): Promise<void> => {
    switch (route.kind) {
      case 'chat': return options.apiService.removeChatApiKey(route.index, apiKey)
      case 'images': return options.apiService.removeImagesApiKey(route.index, apiKey)
      case 'vectors': return options.apiService.removeVectorsApiKey(route.index, apiKey)
      case 'gemini': return options.apiService.removeGeminiApiKey(route.index, apiKey)
      case 'responses': return options.apiService.removeResponsesApiKey(route.index, apiKey)
      default: return options.apiService.removeApiKey(route.index, apiKey)
    }
  }

  // 统一视图下 channel.disabledApiKeys 是各协议路由拉黑记录的并集：
  // 恢复/删除必须覆盖所有包含该 key 的物理路由，否则只清掉主路由副本，
  // 刷新后行会从其他协议的拉黑记录中“复活”，再次删除则 404。
  const disabledKeyRoutes = (channel: Channel, apiKey: string): KeyRoute[] => {
    const routes = (channel.protocolRoutes ?? []).filter(route =>
      route.disabledApiKeys?.some(item => item.key === apiKey),
    )
    if (routes.length > 0) {
      return routes.map(route => ({ kind: route.kind, index: route.index }))
    }
    return [{ kind: options.channelType.value, index: channelId(channel) }]
  }

  // key 在某个路由已不存在（如刚被删除）时视为幂等成功，其他错误仍需透出
  const isKeyNotFoundError = (error: unknown): boolean =>
    error instanceof Error && (error.message.includes('API key not found') || error.message.includes('API密钥不存在'))

  const forEachDisabledKeyRoute = async (channel: Channel, apiKey: string, action: (route: KeyRoute, key: string) => Promise<void>) => {
    const routes = disabledKeyRoutes(channel, apiKey)
    const results = await Promise.allSettled(routes.map(route => action(route, apiKey)))
    const fatal = results.find((r): r is PromiseRejectedResult => r.status === 'rejected' && !isKeyNotFoundError(r.reason))
    if (fatal) throw fatal.reason
  }

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
    (options.channel.value?.disabledApiKeys || []).filter(
      dk => !localRestoredKeys.value.has(dk.key) && !localRemovedKeys.value.has(dk.key),
    )
  )

  const disabledKeyModels = computed(() => options.channel.value?.disabledKeyModels || [])
  const visibleDisabledKeyModels = computed(() =>
    (options.channel.value?.disabledKeyModels || []).filter(
      dm => !localRestoredKeyModels.value.has(keyModelKey(dm.key, dm.model))
    )
  )

  const visibleDisabledGroupModels = computed(() => {
    const serverRecords = options.channel.value?.disabledGroupModels || []
    const records = new Map([...serverRecords, ...localDisabledGroupModels.value].map(record => [groupModelPolicyKey(record), record]))
    return [...records.values()].filter(
      record => !localRestoredGroupModels.value.has(groupModelPolicyKey(record))
    )
  })

  const resetRestoredKeys = () => {
    localRestoredKeys.value = new Set<string>()
    restoringKey.value = ''
    localRemovedKeys.value = new Set<string>()
    removingKey.value = ''
    localRestoredKeyModels.value = new Set<string>()
    restoringKeyModel.value = ''
    localDisabledGroupModels.value = []
    localRestoredGroupModels.value = new Set<string>()
    changingGroupModel.value = ''
    localSuspendedKeys.value = new Set<string>()
    localResumedKeys.value = new Set<string>()
    suspendingKey.value = ''
  }

  const restoreDisabledKey = async (apiKey: string) => {
    const channel = options.channel.value
    if (!channel || restoringKey.value) return
    restoringKey.value = apiKey
    try {
      await forEachDisabledKeyRoute(channel, apiKey, restoreKeyAtRoute)
      if (!options.form.apiKeys.includes(apiKey)) {
        options.form.apiKeys = [...options.form.apiKeys, apiKey]
      }
      if (options.onKeysChanged) {
        await options.onKeysChanged()
      } else {
        localRestoredKeys.value = new Set([...localRestoredKeys.value, apiKey])
      }
    } catch (error) {
      options.emitError(error instanceof Error ? error.message : 'Restore failed')
    } finally {
      restoringKey.value = ''
    }
  }

  const removeDisabledKey = async (apiKey: string) => {
    const channel = options.channel.value
    if (!channel || removingKey.value) return
    removingKey.value = apiKey
    try {
      await forEachDisabledKeyRoute(channel, apiKey, removeKeyAtRoute)
      // 兼容拉黑记录与活跃列表同时存在的历史数据，表单内也一并移除
      if (options.form.apiKeys.includes(apiKey)) {
        options.form.apiKeys = options.form.apiKeys.filter(key => key !== apiKey)
      }
      if (options.onKeysChanged) {
        await options.onKeysChanged()
      } else {
        localRemovedKeys.value = new Set([...localRemovedKeys.value, apiKey])
      }
    } catch (error) {
      options.emitError(error instanceof Error ? error.message : 'Remove failed')
    } finally {
      removingKey.value = ''
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

  const disableGroupModel = async (apiKey: string, model: string) => {
    const channel = options.channel.value
    const normalizedModel = model.trim()
    if (!channel || !normalizedModel || changingGroupModel.value) return
    changingGroupModel.value = keyModelKey(apiKey, normalizedModel)
    try {
      const result = await options.apiService.disableGroupModel(
        options.channelType.value,
        channelId(channel),
        apiKey,
        normalizedModel,
      )
      const record = { quotaGroup: result.quotaGroup, key: apiKey, model: result.model, disabledAt: new Date().toISOString() }
      const policyKey = groupModelPolicyKey(record)
      localDisabledGroupModels.value = [
        ...localDisabledGroupModels.value.filter(item => groupModelPolicyKey(item) !== policyKey),
        record,
      ]
      localRestoredGroupModels.value.delete(policyKey)
      return result
    } catch (error) {
      options.emitError(error instanceof Error ? error.message : 'Disable failed')
    } finally {
      changingGroupModel.value = ''
    }
  }

  // ── 分组模型排除暂存（随渠道主保存提交）──

  const stageGroupModelDisable = (apiKey: string, model: string) => {
    const normalizedModel = model.trim()
    if (!normalizedModel) return
    if (pendingGroupModelDisables.value.some(item => item.key === apiKey && item.model === normalizedModel)) return
    pendingGroupModelDisables.value = [
      ...pendingGroupModelDisables.value,
      { key: apiKey, model: normalizedModel },
    ]
  }

  const unstageGroupModelDisable = (apiKey: string, model: string) => {
    pendingGroupModelDisables.value = pendingGroupModelDisables.value.filter(
      item => !(item.key === apiKey && item.model === model),
    )
  }

  // 渠道主保存成功后调用：逐个提交暂存的排除并清空暂存（单个失败仅报错，不阻断其余）。
  const flushStagedGroupModelDisables = async () => {
    const staged = pendingGroupModelDisables.value
    pendingGroupModelDisables.value = []
    for (const item of staged) {
      await disableGroupModel(item.key, item.model)
    }
  }

  const restoreDisabledGroupModel = async (record: { quotaGroup: string; key?: string; model: string }) => {
    const channel = options.channel.value
    const key = groupModelPolicyKey(record)
    if (!channel || changingGroupModel.value) return
    changingGroupModel.value = key
    try {
      const result = await options.apiService.restoreGroupModel(
        options.channelType.value,
        channelId(channel),
        record.model,
        { quotaGroup: record.quotaGroup?.trim() || undefined, apiKey: record.quotaGroup?.trim() ? undefined : record.key },
      )
      localRestoredGroupModels.value = new Set([...localRestoredGroupModels.value, key])
      localDisabledGroupModels.value = localDisabledGroupModels.value.filter(item => groupModelPolicyKey(item) !== key)
      return result
    } catch (error) {
      options.emitError(error instanceof Error ? error.message : 'Restore failed')
    } finally {
      changingGroupModel.value = ''
    }
  }

  const suspendKey = async (apiKey: string) => {
    const channel = options.channel.value
    if (!channel || suspendingKey.value) return
    suspendingKey.value = apiKey
    try {
      for (const route of keyRoutes(channel, apiKey)) {
        await suspendKeyAtRoute(route, apiKey)
      }
      markKeySuspended(apiKey)
      localSuspendedKeys.value = new Set([...localSuspendedKeys.value, apiKey])
      localResumedKeys.value.delete(apiKey)
      await options.onKeysChanged?.()
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
      for (const route of keyRoutes(channel, apiKey)) {
        await resumeKeyAtRoute(route, apiKey)
      }
      markKeyResumed(apiKey)
      localResumedKeys.value = new Set([...localResumedKeys.value, apiKey])
      localSuspendedKeys.value.delete(apiKey)
      await options.onKeysChanged?.()
    } catch (error) {
      options.emitError(error instanceof Error ? error.message : 'Resume failed')
    } finally {
      suspendingKey.value = ''
    }
  }

  return {
    restoringKey,
    localRestoredKeys,
    removingKey,
    localRemovedKeys,
    disabledKeys,
    visibleDisabledKeys,
    resetRestoredKeys,
    restoreDisabledKey,
    removeDisabledKey,
    restoringKeyModel,
    disabledKeyModels,
    visibleDisabledKeyModels,
    restoreDisabledKeyModel,
    changingGroupModel,
    visibleDisabledGroupModels,
    disableGroupModel,
    restoreDisabledGroupModel,
    pendingGroupModelDisables,
    stageGroupModelDisable,
    unstageGroupModelDisable,
    flushStagedGroupModelDisables,
    suspendingKey,
    suspendKey,
    resumeKey,
    localSuspendedKeys,
    localResumedKeys,
  }
}
