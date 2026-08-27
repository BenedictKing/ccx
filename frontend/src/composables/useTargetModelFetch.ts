import { ref, type ComputedRef } from 'vue'
import { ApiError, ApiService, type Channel } from '../services/api'
import { sortModelNamesDesc } from '../utils/modelPriority'

type ChannelType = 'messages' | 'chat' | 'responses' | 'gemini' | 'images' | 'vectors'
type ServiceType = 'openai' | 'gemini' | 'claude' | 'responses' | 'copilot' | ''
type Translator = (key: string) => string
type DisabledKeyInfo = { key: string; config?: { baseUrl?: string } }
type FormLike = {
  apiKeys: string[]
  apiKeyConfigs?: Array<{ key: string; baseUrl?: string }>
  authHeader: 'auto' | 'bearer' | 'x-api-key' | ''
  baseUrl: string
  customHeaders: Record<string, string>
  insecureSkipVerify: boolean
  proxyUrl: string
  serviceType: ServiceType
}

export interface KeyModelsStatus {
  loading: boolean
  success: boolean
  statusCode?: number
  error?: string
  modelCount?: number
}

type TargetModelFetchOptions = {
  apiService: ApiService
  channel: ComputedRef<Channel | null | undefined>
  channelType: ComputedRef<ChannelType>
  defaultServiceType: () => Exclude<ServiceType, ''>
  form: FormLike
  isEditing: ComputedRef<boolean>
  t: Translator
  visibleDisabledKeys: ComputedRef<DisabledKeyInfo[]>
}

export function useTargetModelFetch(options: TargetModelFetchOptions) {
  const targetModelOptions = ref<Array<{ title: string; value: string }>>([])
  const upstreamTargetModels = ref<string[]>([])
  const fetchingModels = ref(false)
  const fetchModelsError = ref('')
  const keyModelsStatus = ref<Map<string, KeyModelsStatus>>(new Map())

  const normalizeTargetModelNames = (models: string[]) => {
    const byLowercaseModel = new Map<string, string>()
    for (const model of models) {
      const trimmed = String(model || '').trim()
      if (!trimmed) continue
      const key = trimmed.toLowerCase()
      const existing = byLowercaseModel.get(key)
      if (!existing || trimmed === key) {
        byLowercaseModel.set(key, trimmed)
      }
    }
    return sortModelNamesDesc(Array.from(byLowercaseModel.values()))
  }

  const toTargetModelOptions = (models: string[]) => {
    return normalizeTargetModelNames(models).map((id: string) => ({ title: id, value: id }))
  }

  const resetTargetModelOptions = () => {
    upstreamTargetModels.value = []
    targetModelOptions.value = []
  }

  const mergeUpstreamTargetModelOptions = (models: string[]) => {
    upstreamTargetModels.value = normalizeTargetModelNames([...upstreamTargetModels.value, ...models])
    targetModelOptions.value = toTargetModelOptions(upstreamTargetModels.value)
  }

  const ensureTargetModelsLoaded = () => {
    if (upstreamTargetModels.value.length === 0) {
      fetchTargetModels()
    }
  }

  const resolveModelsApiType = () => {
    const effectiveServiceType = options.channelType.value === 'images' || options.channelType.value === 'vectors'
      ? 'openai'
      : (options.form.serviceType || options.defaultServiceType())
    if (options.channelType.value === 'vectors') return 'vectors'
    if (options.channelType.value === 'images') return 'images'
    if (effectiveServiceType === 'gemini') return 'gemini'
    if (effectiveServiceType === 'responses') return 'responses'
    if (effectiveServiceType === 'openai') return 'chat'
    return 'messages'
  }

  const fetchTargetModels = async () => {
    const candidateKeys = options.form.apiKeys.length > 0
      ? options.form.apiKeys
      : (options.isEditing.value ? options.visibleDisabledKeys.value.map(dk => dk.key) : [])

    if (!options.form.baseUrl || candidateKeys.length === 0) {
      fetchModelsError.value = options.t('addChannel.fillBaseUrlAndApiKey')
      return
    }

    const uncheckedKeys = candidateKeys.filter(key => !keyModelsStatus.value.has(key))
    if (uncheckedKeys.length === 0) return

    fetchingModels.value = true
    fetchModelsError.value = ''

    const modelsApiType = resolveModelsApiType()
    // 带 baseUrl 的 override 会让后端走「新增模式」，丢失渠道的 serviceType/
    // learnedClientFingerprint → 探测请求缺 Claude Code 伪装头，会被有客户端
    // 指纹校验的上游 401 拒绝，UI 误报「上游 API Key 无效」。这里显式带上。
    const requestOverrides = {
      baseUrl: options.form.baseUrl || undefined,
      serviceType: options.form.serviceType || undefined,
      learnedClientFingerprint: options.channel.value?.learnedClientFingerprint || undefined,
      proxyUrl: options.form.proxyUrl || undefined,
      insecureSkipVerify: options.form.insecureSkipVerify || undefined,
      customHeaders: Object.keys(options.form.customHeaders).length > 0 ? { ...options.form.customHeaders } : undefined,
      authHeader: options.form.authHeader && options.form.authHeader !== 'auto' ? options.form.authHeader : undefined,
    }

    // 多入口渠道（如火山 Agent Plan / Coding Plan）每个 key 可能绑定了
    // 自己的 baseUrl，拉模型时必须用 key 级地址，否则会被其他入口拒绝鉴权
    const boundBaseUrlForKey = (apiKey: string) => {
      const hit = options.form.apiKeyConfigs?.find(cfg => cfg.key === apiKey && cfg.baseUrl?.trim())
      if (hit?.baseUrl?.trim()) return hit.baseUrl.trim()
      // 禁用 key 已从 apiKeyConfigs 移除，回退到拉黑前的配置快照
      const snapshot = options.visibleDisabledKeys.value.find(dk => dk.key === apiKey)?.config?.baseUrl?.trim()
      return snapshot || undefined
    }

    const keyPromises = uncheckedKeys.map(async (apiKey) => {
      keyModelsStatus.value.set(apiKey, { loading: true, success: false })

      try {
        const id = options.channel.value?.index ?? 0
        const request = { key: apiKey, ...requestOverrides, baseUrl: boundBaseUrlForKey(apiKey) || requestOverrides.baseUrl }
        let response: { data: { id: string }[] }

        switch (modelsApiType) {
          case 'messages':
            response = await options.apiService.getChannelModels(id, request)
            break
          case 'responses':
            response = await options.apiService.getResponsesChannelModels(id, request)
            break
          case 'chat':
            response = await options.apiService.getChatChannelModels(id, request)
            break
          case 'images':
            response = await options.apiService.getImagesChannelModels(id, request)
            break
          case 'vectors':
            response = await options.apiService.getVectorsChannelModels(id, request)
            break
          case 'gemini':
            response = await options.apiService.getGeminiChannelModels(id, request)
            break
        }

        keyModelsStatus.value.set(apiKey, {
          loading: false,
          success: true,
          statusCode: 200,
          modelCount: response.data.length,
        })
        return response.data
      } catch (error) {
        let errorMsg = options.t('addChannel.unknownError')
        let statusCode = 0
        if (error instanceof ApiError) {
          errorMsg = error.message
          statusCode = error.status
        } else if (error instanceof Error) {
          errorMsg = error.message
        }
        keyModelsStatus.value.set(apiKey, {
          loading: false,
          success: false,
          statusCode,
          error: errorMsg,
        })
        return [] as { id: string }[]
      }
    })

    try {
      const results = await Promise.all(keyPromises)
      mergeUpstreamTargetModelOptions(results.flatMap(models => models.map(m => m.id)))

      const allFailed = candidateKeys.every(key => {
        const status = keyModelsStatus.value.get(key)
        return status && !status.success
      })
      if (allFailed) {
        fetchModelsError.value = options.t('addChannel.allApiKeysModelsFailed')
      }
    } finally {
      fetchingModels.value = false
    }
  }

  resetTargetModelOptions()

  return {
    targetModelOptions,
    upstreamTargetModels,
    normalizeTargetModelNames,
    toTargetModelOptions,
    resetTargetModelOptions,
    mergeUpstreamTargetModelOptions,
    fetchingModels,
    fetchModelsError,
    keyModelsStatus,
    ensureTargetModelsLoaded,
    fetchTargetModels,
  }
}
