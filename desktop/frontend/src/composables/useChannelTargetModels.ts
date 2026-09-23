import { computed, ref, watch, type Ref } from 'vue'
import { AdminApiError } from '@/composables/useAdminApi'
import type { Channel } from '@/services/admin-api'
import { filterValidSupportedModelPatterns, parseSupportedModelInput } from '@/utils/channel-dialog-state'
import { getChannelTypeApi, type ManagedChannelType } from '@/utils/channel-type-api'
import { sortModelNamesDesc } from '@/utils/model-priority'

type Translator = (key: string) => string
type ServiceType = 'openai' | 'claude' | 'gemini' | 'responses' | 'copilot' | ''

export type KeyModelsStatus = {
  loading?: boolean
  success?: boolean
  error?: string
  statusCode?: string | number
  modelCount?: number
}

type FormLike = {
  authHeader: 'auto' | 'bearer' | 'x-api-key' | ''
  baseUrl: string
  proxyUrl: string
  insecureSkipVerify: boolean
  serviceType: ServiceType
  supportedModelsText: string
}

type ChannelTargetModelsOptions = {
  channel: () => Channel | null | undefined
  channelType: () => ManagedChannelType
  defaultServiceTypeForChannel: () => Exclude<ServiceType, ''>
  form: FormLike
  getHeadersAsObject: () => Record<string, string>
  getSubmitApiKeys: () => string[]
  keyModelsStatus: Ref<Map<string, KeyModelsStatus>>
  t: Translator
}

export function useChannelTargetModels(options: ChannelTargetModelsOptions) {
  const fetchingModels = ref(false)
  const targetModelOptions = ref<string[]>([])
  const fetchedModelsError = ref('')
  const hasTriedFetchModels = ref(false)
  const commonSupportedModelFilters = ['claude-*', 'gpt-5*', 'gpt-image-2', 'grok-4*', 'gemini-3*', '!*image*']

  function resetTargetModelState(clearError = false) {
    targetModelOptions.value = []
    hasTriedFetchModels.value = false
    if (clearError) fetchedModelsError.value = ''
  }

  watch(() => options.channel()?.index, () => {
    resetTargetModelState(true)
  })

  const targetModelDatalist = computed(() => {
    const byLowercaseModel = new Map<string, string>()
    for (const model of targetModelOptions.value) {
      const trimmed = String(model || '').trim()
      if (!trimmed) continue
      const key = trimmed.toLowerCase()
      const existing = byLowercaseModel.get(key)
      if (!existing || trimmed === key) {
        byLowercaseModel.set(key, trimmed)
      }
    }
    return sortModelNamesDesc(Array.from(byLowercaseModel.values()))
  })

  const normalizedSupportedModelState = computed(() => {
    const parsedPatterns = parseSupportedModelInput(options.form.supportedModelsText)
    return filterValidSupportedModelPatterns(parsedPatterns)
  })

  const supportedModelsError = computed(() => (
    normalizedSupportedModelState.value.hasInvalidPatterns
      ? options.t('addChannel.supportedModelsInvalidPattern')
      : ''
  ))

  const selectedSupportedModelSet = computed(() => new Set(normalizedSupportedModelState.value.validPatterns))

  function toggleSupportedModelFilter(filter: string) {
    const current = [...normalizedSupportedModelState.value.validPatterns]
    const idx = current.indexOf(filter)
    if (idx !== -1) {
      current.splice(idx, 1)
    } else {
      current.push(filter)
    }
    options.form.supportedModelsText = current.join('\n')
  }

  async function fetchTargetModels() {
    const channel = options.channel()
    if (!channel) {
      return false
    }
    if (!options.form.baseUrl.trim() || options.getSubmitApiKeys().length === 0) {
      fetchedModelsError.value = options.t('addChannel.fillBaseUrlAndApiKey')
      return false
    }

    const keys = options.getSubmitApiKeys()
    const uncheckedKeys = keys.filter(key => !options.keyModelsStatus.value.has(key))
    if (uncheckedKeys.length === 0) return true

    fetchingModels.value = true
    fetchedModelsError.value = ''
    try {
      const effectiveServiceType = options.channelType() === 'images' || options.channelType() === 'vectors'
        ? 'openai'
        : (options.form.serviceType || options.defaultServiceTypeForChannel())
      let modelsApiType: ManagedChannelType
      if (options.channelType() === 'vectors') {
        modelsApiType = 'vectors'
      } else if (options.channelType() === 'images') {
        modelsApiType = 'images'
      } else if (effectiveServiceType === 'copilot') {
        modelsApiType = 'responses'
      } else if (effectiveServiceType === 'gemini') {
        modelsApiType = 'gemini'
      } else if (effectiveServiceType === 'responses') {
        modelsApiType = 'responses'
      } else if (effectiveServiceType === 'openai') {
        modelsApiType = 'chat'
      } else {
        modelsApiType = 'messages'
      }

      const typeApi = getChannelTypeApi(modelsApiType)
      const customHeaders = options.getHeadersAsObject()
      const results = await Promise.all(uncheckedKeys.map(async (key) => {
        options.keyModelsStatus.value.set(key, { loading: true, success: false })
        try {
          const resp = await typeApi.getChannelModels(channel.index, {
            key,
            baseUrl: options.form.baseUrl,
            serviceType: effectiveServiceType,
            proxyUrl: options.form.proxyUrl,
            insecureSkipVerify: options.form.insecureSkipVerify,
            customHeaders: Object.keys(customHeaders).length ? customHeaders : undefined,
            authHeader: options.form.authHeader && options.form.authHeader !== 'auto' ? options.form.authHeader : undefined,
          })
          const list: any[] = Array.isArray(resp) ? resp : (resp?.data ?? [])
          options.keyModelsStatus.value.set(key, {
            loading: false,
            success: true,
            // 优先使用后端透传的上游真实状态码（旧后端无此字段时回退 200）
            statusCode: Array.isArray(resp) ? 200 : (resp?.statusCode ?? 200),
            modelCount: list.length,
          })
          return list
        } catch (e) {
          options.keyModelsStatus.value.set(key, {
            loading: false,
            success: false,
            statusCode: e instanceof AdminApiError ? e.status : 'ERR',
            error: e instanceof Error ? e.message : String(e),
          })
          return []
        }
      }))
      const byLowercaseModel = new Map<string, string>()
      results
        .flat()
        .map((m: any) => m.id || m.name || String(m))
        .filter(Boolean)
        .forEach(model => {
          const trimmed = String(model).trim()
          if (!trimmed) return
          const key = trimmed.toLowerCase()
          const existing = byLowercaseModel.get(key)
          if (!existing || trimmed === key) {
            byLowercaseModel.set(key, trimmed)
          }
        })
      targetModelOptions.value = sortModelNamesDesc(Array.from(byLowercaseModel.values()))

      const allFailed = keys.every(key => {
        const status = options.keyModelsStatus.value.get(key)
        return status && !status.success
      })
      if (allFailed) {
        fetchedModelsError.value = options.t('addChannel.allApiKeysModelsFailed')
      }
      return true
    } catch (e) {
      // 意外编排错误（非 per-key API 失败）保留 stack trace 便于诊断；per-key 失败已在上方 catch 单独记录到 keyModelsStatus
      console.error('[fetchTargetModels-请求失败]', e)
      fetchedModelsError.value = e instanceof Error
        ? e.message
        : typeof e === 'object' && e !== null
          ? JSON.stringify(e, null, 2)
          : String(e)
      return false
    } finally {
      fetchingModels.value = false
    }
  }

  function handleTargetFocus() {
    if (hasTriedFetchModels.value || fetchingModels.value) return
    if (!options.form.baseUrl.trim() || options.getSubmitApiKeys().length === 0) return
    hasTriedFetchModels.value = true
    void fetchTargetModels()
  }

  return {
    fetchingModels,
    targetModelOptions,
    fetchedModelsError,
    hasTriedFetchModels,
    targetModelDatalist,
    commonSupportedModelFilters,
    normalizedSupportedModelState,
    supportedModelsError,
    selectedSupportedModelSet,
    resetTargetModelState,
    toggleSupportedModelFilter,
    fetchTargetModels,
    handleTargetFocus,
  }
}
