import { ref, reactive, computed, watch, onMounted, onUnmounted, nextTick } from 'vue'
import { useTheme } from 'vuetify'
import type { Channel, ChannelDiscoveryRequest, ChannelDiscoveryResponse, ChannelDiscoveryTargetClient } from '../services/api'
import { ApiService } from '../services/api'
import { supportsAdvancedChannelOptions, supportsReasoningMapping } from '../utils/channelAdvancedOptions'
import {
  buildChannelPayload,
  createEmbeddingCapabilityRow,
  createModelCapabilityRow,
  embeddingCapabilitiesToRows,
  embeddingCapabilityRowsToRecord,
  modelCapabilitiesToRows,
  modelCapabilityRowsToRecord,
  normalizeSelectableString,
  resolveBuiltinUpstreamModelCapability,
  type EmbeddingCapabilityRow,
  type ModelCapabilityRow,
} from '../utils/channelPayload'
import {
  resolveChannelWatcherAction,
  syncBaseUrlsFormState,
  filterValidSupportedModelPatterns,
} from '../utils/add-channel-modal-state'
import { streamTimeoutPresets } from '../utils/streamTimeoutPresets'
import { useI18n } from '../i18n'
import { useChannelEditorFormDerived } from './useChannelEditorFormDerived'
import { useChannelEditorHeaderState } from './useChannelEditorHeaderState'
import { useDialogMenuWorkaround } from './useDialogMenuWorkaround'
import { useDisabledApiKeys } from './useDisabledApiKeys'
import { useEditChannelPresets } from './useEditChannelPresets'
import { useEditChannelSectionNav } from './useEditChannelSectionNav'
import { useTargetModelFetch } from './useTargetModelFetch'
import { useStreamTimeoutStrategy } from './useStreamTimeoutStrategy'
import { useSupportedModelFilters } from './useSupportedModelFilters'
import { useEditChannelOptions } from '../utils/editChannelOptions'
import { defaultStripBillingHeader, isValidUrl, normalizeModelCapabilities } from '../utils/editChannelHelpers'
import { isAutoManagedAccountChannel } from '../utils/providerDisplay'
import { getManagedProviderWebsiteLinks } from '../utils/channelWebsite'

export interface EditChannelModalProps {
  show: boolean
  channel?: Channel | null
  channelType?: 'messages' | 'chat' | 'responses' | 'gemini' | 'images' | 'vectors'
}

export type EditChannelModalEmits = {
  'update:show': [value: boolean]
  save: [
    channel: Omit<Channel, 'index' | 'latency' | 'status'>,
    options?: { isQuickAdd?: boolean },
    onComplete?: () => void,
  ]
  error: [message: string]
  success: [message: string]
}

type EditChannelModalEmit = <K extends keyof EditChannelModalEmits>(event: K, ...args: EditChannelModalEmits[K]) => void
type ResolvedEditChannelModalProps = Readonly<EditChannelModalProps & { channelType: NonNullable<EditChannelModalProps['channelType']> }>

type ChannelDiscoverySessionStatus = 'running' | 'success' | 'error'

interface ChannelDiscoverySession {
  ownerKey: string
  requestKey: string
  status: ChannelDiscoverySessionStatus
  result: ChannelDiscoveryResponse | null
  error: string
  promise?: Promise<ChannelDiscoveryResponse>
}

const channelDiscoverySessions = new Map<string, ChannelDiscoverySession>()

// 已完全收归运行时自动学习的兼容性字段，不再接受前端编辑，
// 诊断（compat-diagnose）返回的建议中这些字段仅供只读参考，不写回表单。
const RUNTIME_MANAGED_COMPAT_FIELDS = new Set([
  'normalizeNonstandardChatRoles',
  'codexNativeToolPassthrough',
  'stripImageGenerationTool',
  'stripEmptyTextBlocks',
  'passbackReasoningContent',
  'passbackThinkingBlocks',
])

function stableStringify(value: unknown): string {
  if (Array.isArray(value)) {
    return `[${value.map(item => stableStringify(item)).join(',')}]`
  }
  if (value && typeof value === 'object') {
    const record = value as Record<string, unknown>
    return `{${Object.keys(record)
      .sort()
      .filter(key => record[key] !== undefined)
      .map(key => `${JSON.stringify(key)}:${stableStringify(record[key])}`)
      .join(',')}}`
  }
  const serialized = JSON.stringify(value)
  return serialized === undefined ? 'null' : serialized
}

export function useEditChannelModal(props: ResolvedEditChannelModalProps, emit: EditChannelModalEmit) {
  const { t } = useI18n()
  const apiService = new ApiService()

  // 主题
  const theme = useTheme()

  // 表单引用
  const formRef = ref()

  const defaultServiceTypeValueFallback = (): 'openai' | 'gemini' | 'claude' | 'responses' | 'copilot' => {
    if (props.channelType === 'chat') return 'openai'
    if (props.channelType === 'vectors') return 'openai'
    if (props.channelType === 'gemini') return 'gemini'
    if (props.channelType === 'responses') return 'responses'
    return 'claude'
  }

  const defaultNormalizeMetadataUserId = () => props.channelType === 'messages'

  // 新建渠道时 stripBillingHeader 跟随 baseUrl 自动推断；用户手动切换过则不再覆盖
  const stripBillingHeaderTouched = ref(false)

  // 详细表单预期请求 URL 预览（防止输入时抖动）
  const formBaseUrlPreview = ref('')
  let formBaseUrlPreviewTimer: number | null = null

  const {
    activeSection,
    sections: allSections,
    scrollToSection,
    setSectionRef,
    attachScrollListener,
    detachScrollListener,
  } = useEditChannelSectionNav(t)

  const { isAnySelectMenuOpen, suppressDialogEscapeUntil, onMenuUpdate } = useDialogMenuWorkaround()
  const isAutoManagedChannel = computed(() => isAutoManagedAccountChannel(props.channel))
  const sections = computed(() => {
    if (!isAutoManagedChannel.value) return allSections
    return allSections.filter(section => section.id === 'basic' || section.id === 'auth')
  })

  const supportsOpenAIAdvancedOptions = computed(() => props.channelType !== 'vectors' && supportsAdvancedChannelOptions(form.serviceType))
  const supportsReasoningMappingOptions = computed(() => props.channelType !== 'vectors' && supportsReasoningMapping(form.serviceType))
  const supportsChannelDiscovery = computed(() => {
    return !isAutoManagedChannel.value && props.channelType !== 'images' && props.channelType !== 'vectors'
  })

  // 模型优先级排序规则（索引越小优先级越高）
  // 表单数据：balanced 预设值作为渠道级默认回退值
  const defaultStreamTimeouts = { ...streamTimeoutPresets.balanced }

  const form = reactive({
    name: '',
    serviceType: '' as 'openai' | 'gemini' | 'claude' | 'responses' | 'copilot' | '',
    authHeader: 'auto' as 'auto' | 'bearer' | 'x-api-key' | '',
    baseUrl: '',
    baseUrls: [] as string[],
    website: '',
    insecureSkipVerify: false,
    lowQuality: false,
    injectDummyThoughtSignature: false,
    stripThoughtSignature: false,
    normalizeSystemRoleToTopLevel: false,
    description: '',
    tags: [] as string[],
    apiKeys: [] as string[],
    apiKeyConfigs: undefined as Channel['apiKeyConfigs'],
    modelMapping: {} as Record<string, string>,
    modelCapabilitiesText: '',
    modelCapabilityRows: [] as ModelCapabilityRow[],
    embeddingCapabilityRows: [] as EmbeddingCapabilityRow[],
    defaultContextWindowTokens: null as string | number | null,
    defaultMaxOutputTokens: null as string | number | null,
    allowUnknownContext: false,
    reasoningMapping: {} as Record<string, 'none' | 'minimal' | 'low' | 'medium' | 'high' | 'xhigh' | 'max'>,
    reasoningParamStyle: 'reasoning' as 'reasoning' | 'reasoning_effort' | 'thinking',
    textVerbosity: '' as 'low' | 'medium' | 'high' | '',
    fastMode: false,
    customHeaders: {} as Record<string, string>,
    proxyUrl: '',
    requestTimeoutMs: null as string | number | null,
    responseHeaderTimeoutMs: null as string | number | null,
    streamFirstContentTimeoutEnabled: false,
    streamFirstContentTimeoutMs: defaultStreamTimeouts.firstContentMs as number,
    streamInactivityTimeoutEnabled: false,
    streamInactivityTimeoutMs: defaultStreamTimeouts.inactivityMs as number,
    streamToolCallIdleTimeoutEnabled: false,
    streamToolCallIdleTimeoutMs: defaultStreamTimeouts.toolCallIdleMs as number,
    rateLimitRpm: null as string | number | null,
    rateLimitWindowMinutes: null as string | number | null,
    rateLimitMaxConcurrent: null as string | number | null,
    rateLimitAutoFromHeaders: true,
    routePrefix: '',
    supportedModels: [] as string[],
    autoBlacklistBalance: true,
    normalizeMetadataUserId: defaultNormalizeMetadataUserId(),
    stripBillingHeader: false,
    codexToolCompat: false,
    stripCodexClientTools: false,
    convertImageUrlToB64Json: false,
    noVision: false,
    noVisionModels: [] as string[],
    visionFallbackModel: '',
    visionFallbackReasoningEffort: '' as 'none' | 'minimal' | 'low' | 'medium' | 'high' | 'xhigh' | 'max' | '',
    historicalImageTurnLimit: 0,
  })

  const channelTypeRef = computed(() => props.channelType)
  const {
    serviceTypeOptions,
    sourceModelOptions,
    modelMappingHint,
    targetModelPlaceholder,
    reasoningEffortOptions,
    reasoningParamStyleOptions,
    textVerbosityOptions,
  } = useEditChannelOptions(channelTypeRef, form, t)

  // 多 BaseURL 文本输入（独立变量，保留用户输入的换行）
  const baseUrlsText = ref('')

  // 监听 baseUrlsText 变化，同步到 form（去重等效 URL）
  watch(baseUrlsText, val => {
    const { baseUrl, baseUrls } = syncBaseUrlsFormState(val, form.serviceType)
    form.baseUrl = baseUrl
    form.baseUrls = baseUrls
  })

  watch(() => form.serviceType, () => {
    const { baseUrl, baseUrls } = syncBaseUrlsFormState(baseUrlsText.value, form.serviceType)
    form.baseUrl = baseUrl
    form.baseUrls = baseUrls
  })

  // 新建 Messages 渠道时按 baseUrl 推断 stripBillingHeader 默认值：
  // 非官方 Anthropic 域名默认剔除，避免每次变化的 cch= nonce 打穿上游 prompt 缓存。
  // 编辑既有渠道或用户已手动切换开关时不覆盖。
  watch(
    () => [form.baseUrl, form.baseUrls.join(',')],
    () => {
      if (props.channel || stripBillingHeaderTouched.value) return
      if (props.channelType !== 'messages') return
      form.stripBillingHeader = defaultStripBillingHeader([form.baseUrl, ...form.baseUrls])
    },
  )

  // 模型映射行数据结构（改用数组存储，支持直接编辑）
  interface ModelMappingRow {
    id: number
    source: string
    target: string
    reasoning: '' | 'none' | 'minimal' | 'low' | 'medium' | 'high' | 'xhigh' | 'max'
    noVision: boolean
  }

  let rowIdCounter = 0
  const modelMappingRows = ref<ModelMappingRow[]>([])
  let capabilityRowIdCounter = 0
  const nextCapabilityRowId = () => ++capabilityRowIdCounter
  let embeddingCapabilityRowIdCounter = 0
  const nextEmbeddingCapabilityRowId = () => ++embeddingCapabilityRowIdCounter

  const incompleteMappedTargetSuffix = /[._:/-]$/
  const isCompleteMappedTargetModel = (model: string) => !!model && !incompleteMappedTargetSuffix.test(model)
  const hasNoVisionRows = computed(() => modelMappingRows.value.some(row => row.noVision && row.target.trim()))
  const mappedTargetModels = computed(() => {
    const seen = new Set<string>()
    const models = [
      ...modelMappingRows.value.map(row => normalizeSelectableString(row.target).trim()),
      normalizeSelectableString(form.visionFallbackModel).trim(),
    ]

    return models.filter(model => {
      const key = model.toLowerCase()
      if (!isCompleteMappedTargetModel(model) || seen.has(key)) return false
      seen.add(key)
      return true
    })
  })
  const isMappingTargetEditing = ref(false)
  const hasPendingModelCapabilitySync = ref(false)

  function resetTransientUiState() {
    sourceMappingError.value = ''
    resetRestoredKeys()
    errors.name = ''
    errors.serviceType = ''
    errors.baseUrl = ''
    errors.website = ''
    formBaseUrlPreview.value = ''
  }

  // 源模型名验证错误
  const sourceMappingError = ref('')

  // 表单验证错误
  const errors = reactive({
    name: '',
    serviceType: '',
    baseUrl: '',
    website: ''
  })

  // 验证规则
  const rules = {
    required: (value: string) => !!value || t('addChannel.fieldRequired'),
    url: (value: string) => {
      try {
        new URL(value)
        return true
      } catch {
        return t('addChannel.invalidUrl')
      }
    },
    urlOptional: (value: string) => {
      if (!value) return true
      try {
        new URL(value)
        return true
      } catch {
        return t('addChannel.invalidUrl')
      }
    },
    baseUrls: (value: string) => {
      if (!value) return t('addChannel.fieldRequired')
      const urls = value
        .split('\n')
        .map(s => s.trim())
        .filter(Boolean)
      if (urls.length === 0) return t('addChannel.atLeastOneUrl')
      for (const url of urls) {
        try {
          new URL(url)
        } catch {
          return t('addChannel.invalidUrlValue', { url })
        }
      }
      return true
    },
    requestTimeoutMs: (value: string | number | null) => {
      if (value === null || value === undefined || value === '') return true
      const timeout = Number(value)
      return (Number.isInteger(timeout) && timeout >= 1000 && timeout <= 300000) || t('addChannel.requestTimeoutMsInvalid')
    },
    responseHeaderTimeoutMs: (value: string | number | null) => {
      if (value === null || value === undefined || value === '') return true
      const timeout = Number(value)
      return (Number.isInteger(timeout) && timeout >= 1000 && timeout <= 300000) || t('addChannel.responseHeaderTimeoutMsInvalid')
    }
  }

  // 计算属性
  const dialogMode = ref<'create' | 'edit'>('create')
  const isEditing = computed(() => dialogMode.value === 'edit')
  const isMac = computed(() => typeof navigator !== 'undefined' && /Mac|iPod|iPhone|iPad/.test(navigator.platform))
  const hasDisabledKeysAvailable = computed(() => visibleDisabledKeys.value.length > 0)
  const hasConfigurableKeys = computed(() => form.apiKeys.length > 0 || (isEditing.value && hasDisabledKeysAvailable.value))

  const { selectedStreamTimeoutStrategy, applyStreamTimeoutStrategy } = useStreamTimeoutStrategy(form)

  const {
    commonSupportedModelFilters,
    selectedSupportedModelSet,
    supportedModelsError,
    handleSupportedModelsChange,
    appendSupportedModelFilter,
  } = useSupportedModelFilters(form, t)

  const modelCapabilitiesError = computed(() => {
    return modelCapabilityRowsToRecord(form.modelCapabilityRows) === null
      ? t('addChannel.modelCapabilitiesRowsInvalid')
      : ''
  })

  const discoveringChannelConfig = ref(false)
  const channelDiscoveryResult = ref<ChannelDiscoveryResponse | null>(null)
  const channelDiscoveryError = ref('')
  const channelDiscoveryModelMappingEntries = computed(() => {
    const mapping = channelDiscoveryResult.value?.recommendation?.modelMapping ?? {}
    return Object.entries(mapping)
  })
  const channelDiscoveryCompatEntries = computed(() => {
    const compat = channelDiscoveryResult.value?.recommendation?.compat ?? {}
    return Object.entries(compat).filter(([, value]) => value !== undefined)
  })
  const channelDiscoveryReasoningEntries = computed(() => {
    const reasoning = channelDiscoveryResult.value?.recommendation?.reasoningMapping ?? {}
    return Object.entries(reasoning)
  })
  const channelDiscoverySuccessfulProtocols = computed(() => {
    return channelDiscoveryResult.value?.protocols.filter(protocol => protocol.success) ?? []
  })
  const channelDiscoveryCapabilityEntries = computed(() => {
    const capabilities = channelDiscoveryResult.value?.capabilities
    if (!capabilities) return []

    const entries: Array<{ key: string; label: string; text: string; color: string; detail: string }> = []
    if (capabilities.toolCalls?.tested) {
      entries.push({
        key: 'toolCalls',
        label: t('channelDiscovery.capabilityToolCalls'),
        text: capabilities.toolCalls.supported
          ? t('channelDiscovery.capabilitySupported')
          : t('channelDiscovery.capabilityUnsupported'),
        color: capabilities.toolCalls.supported ? 'success' : 'warning',
        detail: capabilities.toolCalls.evidence || capabilities.toolCalls.error || '',
      })
    }
    if (capabilities.vision?.tested) {
      entries.push({
        key: 'vision',
        label: t('channelDiscovery.capabilityVision'),
        text: capabilities.vision.supported
          ? t('channelDiscovery.capabilitySupported')
          : t('channelDiscovery.capabilityUnsupported'),
        color: capabilities.vision.supported ? 'success' : 'warning',
        detail: capabilities.vision.evidence || capabilities.vision.error || '',
      })
    }
    if (capabilities.imageGeneration?.tested) {
      entries.push({
        key: 'imageGeneration',
        label: t('channelDiscovery.capabilityImageGeneration'),
        text: capabilities.imageGeneration.supported
          ? t('channelDiscovery.capabilitySupported')
          : t('channelDiscovery.capabilityUnsupported'),
        color: capabilities.imageGeneration.supported ? 'success' : 'warning',
        detail: capabilities.imageGeneration.evidence || capabilities.imageGeneration.error || '',
      })
    }
    if (capabilities.thinkingPassback?.tested) {
      entries.push({
        key: 'thinkingPassback',
        label: t('channelDiscovery.capabilityThinkingPassback'),
        text: capabilities.thinkingPassback.required
          ? t('channelDiscovery.capabilityRequired')
          : t('channelDiscovery.capabilityNotRequired'),
        color: capabilities.thinkingPassback.required ? 'secondary' : 'success',
        detail: capabilities.thinkingPassback.evidence || capabilities.thinkingPassback.error || '',
      })
    }
    return entries
  })

  const discoveryTargetClients = (): ChannelDiscoveryTargetClient[] => {
    if (props.channelType === 'responses') return ['codex']
    if (props.channelType === 'messages') return ['claude-code']
    return []
  }

  const firstDraftApiKey = () => {
    return form.apiKeys.map(key => key.trim()).find(Boolean) || ''
  }

  const draftBaseUrls = () => {
    return baseUrlsText.value
      .split('\n')
      .map(line => line.trim())
      .filter(Boolean)
  }

  const draftModelMapping = () => {
    const mapping: Record<string, string> = {}
    modelMappingRows.value.forEach(row => {
      if (row.source && row.target) {
        mapping[row.source] = row.target
      }
    })
    return mapping
  }

  const draftReasoningMapping = () => {
    const reasoning: Record<string, string> = {}
    modelMappingRows.value.forEach(row => {
      if (row.source && row.target && row.reasoning) {
        reasoning[row.source] = row.reasoning
      }
    })
    return reasoning
  }

  const buildChannelDiscoveryRequest = (): ChannelDiscoveryRequest | null => {
    if (!supportsChannelDiscovery.value) return null
    const baseUrls = draftBaseUrls()
    const apiKey = firstDraftApiKey()
    if (baseUrls.length === 0 || !apiKey || !form.serviceType) return null

    return {
      channelKind: props.channelType as 'messages' | 'chat' | 'responses' | 'gemini',
      serviceType: form.serviceType,
      baseUrls,
      apiKey,
      authHeader: form.authHeader,
      customHeaders: { ...form.customHeaders },
      proxyUrl: form.proxyUrl,
      insecureSkipVerify: form.insecureSkipVerify,
      modelMapping: draftModelMapping(),
      reasoningMapping: draftReasoningMapping(),
      targetClients: discoveryTargetClients(),
    }
  }

  const channelDiscoveryOwnerKey = () => `${props.channelType}:${props.channel?.index ?? 'new'}`
  const channelDiscoveryRequestKey = (request: ChannelDiscoveryRequest) => stableStringify(request)

  const isCurrentDiscoverySession = (session: ChannelDiscoverySession) => {
    const request = buildChannelDiscoveryRequest()
    return !!request
      && session.ownerKey === channelDiscoveryOwnerKey()
      && session.requestKey === channelDiscoveryRequestKey(request)
  }

  const syncChannelDiscoverySessionToState = (session: ChannelDiscoverySession) => {
    if (!isCurrentDiscoverySession(session)) return
    discoveringChannelConfig.value = session.status === 'running'
    channelDiscoveryResult.value = session.result
    channelDiscoveryError.value = session.error
  }

  const clearChannelDiscoveryState = () => {
    discoveringChannelConfig.value = false
    channelDiscoveryResult.value = null
    channelDiscoveryError.value = ''
  }

  const restoreChannelDiscoverySession = () => {
    const request = buildChannelDiscoveryRequest()
    const session = request ? channelDiscoverySessions.get(channelDiscoveryOwnerKey()) : undefined
    if (!request || !session || session.requestKey !== channelDiscoveryRequestKey(request)) {
      clearChannelDiscoveryState()
      return
    }
    syncChannelDiscoverySessionToState(session)
    if (session.status === 'running' && session.promise) {
      void session.promise
        .then(() => syncChannelDiscoverySessionToState(session))
        .catch(() => syncChannelDiscoverySessionToState(session))
    }
  }

  const handleDiscoverChannelConfig = async () => {
    if (!supportsChannelDiscovery.value) return
    const baseUrls = draftBaseUrls()
    if (baseUrls.length === 0) {
      channelDiscoveryError.value = t('channelDiscovery.missingBaseUrl')
      return
    }
    const apiKey = firstDraftApiKey()
    if (!apiKey) {
      channelDiscoveryError.value = t('channelDiscovery.missingApiKey')
      return
    }
    if (!form.serviceType) {
      channelDiscoveryError.value = t('channelDiscovery.missingServiceType')
      return
    }

    syncModelMappingToForm()
    const request = buildChannelDiscoveryRequest()
    if (!request) return

    const ownerKey = channelDiscoveryOwnerKey()
    const requestKey = channelDiscoveryRequestKey(request)
    const existing = channelDiscoverySessions.get(ownerKey)
    if (existing?.status === 'running' && existing.requestKey === requestKey) {
      syncChannelDiscoverySessionToState(existing)
      return
    }

    const session: ChannelDiscoverySession = {
      ownerKey,
      requestKey,
      status: 'running',
      result: null,
      error: '',
    }
    session.promise = apiService.discoverChannelConfig(request)
      .then(result => {
        session.status = 'success'
        session.result = result
        session.error = ''
        return result
      })
      .catch(e => {
        session.status = 'error'
        session.result = null
        session.error = e instanceof Error ? e.message : t('channelDiscovery.failed')
        throw e
      })
      .finally(() => {
        syncChannelDiscoverySessionToState(session)
      })

    channelDiscoverySessions.set(ownerKey, session)
    syncChannelDiscoverySessionToState(session)
    try {
      await session.promise
    } catch {
      // 错误已写入 session，保持弹窗内联展示。
    }
  }

  const applyChannelDiscoveryRecommendation = () => {
    const recommendation = channelDiscoveryResult.value?.recommendation
    if (!recommendation) return

    if (recommendation.serviceType) {
      updateForm({ serviceType: recommendation.serviceType })
    }
    if (recommendation.baseUrls?.length) {
      baseUrlsText.value = recommendation.baseUrls.join('\n')
    }
    if (recommendation.urlRecommendation?.recommended) {
      const current = recommendation.urlRecommendation.current
      const recommended = recommendation.urlRecommendation.recommended
      const nextLines = draftBaseUrls().map(line => (line === current ? recommended : line))
      baseUrlsText.value = Array.from(new Set(nextLines.length ? nextLines : [recommended])).join('\n')
    }

    const mapping = recommendation.modelMapping ?? {}
    const noVisionSet = new Set(recommendation.noVisionModels ?? [])
    modelMappingRows.value = Object.entries(mapping).map(([source, target]) => ({
      id: ++rowIdCounter,
      source,
      target,
      reasoning: (recommendation.reasoningMapping?.[source] || '') as ModelMappingRow['reasoning'],
      noVision: noVisionSet.has(target),
    }))
    form.modelMapping = { ...mapping }
    form.reasoningMapping = { ...(recommendation.reasoningMapping || {}) } as typeof form.reasoningMapping
    form.noVisionModels = [...noVisionSet]
    form.visionFallbackModel = recommendation.visionFallbackModel || ''
    form.visionFallbackReasoningEffort = ''
    // discovery 不再生成 supportedModels 模式；apply 时始终清空，
    // 避免历史错误配置残留（例如把 responses 渠道的源模型过滤留在 messages 渠道里）。
    form.supportedModels = recommendation.supportedModels ? [...recommendation.supportedModels] : []
    syncModelCapabilitiesFromMapping()
    for (const [key, value] of Object.entries(recommendation.compat || {})) {
      if (typeof value === 'boolean' && key in form) {
        ;(form as Record<string, unknown>)[key] = value
      }
    }
    emit('success', t('channelDiscovery.applied'))
  }
  const embeddingCapabilitiesError = computed(() => {
    return props.channelType === 'vectors' && embeddingCapabilityRowsToRecord(form.embeddingCapabilityRows) === null
      ? t('addChannel.embeddingCapabilitiesRowsInvalid')
      : ''
  })

  const syncModelCapabilitiesFromMapping = () => {
    if (props.channelType === 'vectors') return
    const existingModels = new Set(
      form.modelCapabilityRows
        .map(row => normalizeSelectableString(row.model).trim().toLowerCase())
        .filter(Boolean)
    )
    const rowsToAdd = mappedTargetModels.value
      .filter(isCompleteMappedTargetModel)
      .filter(model => !existingModels.has(model.toLowerCase()))
      .map(model => {
        const builtin = resolveBuiltinUpstreamModelCapability(model)
        return createModelCapabilityRow(
          nextCapabilityRowId(),
          model,
          builtin?.capability,
          builtin ? 'builtin' : 'custom',
          builtin?.pattern || '',
        )
      })
    if (!rowsToAdd.length) return
    form.modelCapabilityRows = [...form.modelCapabilityRows, ...rowsToAdd]
  }

  const syncEmbeddingCapabilitiesFromMapping = () => {
    if (props.channelType !== 'vectors') return
    const existingModels = new Set(
      form.embeddingCapabilityRows
        .map(row => normalizeSelectableString(row.model).trim().toLowerCase())
        .filter(Boolean)
    )
    const rowsToAdd = mappedTargetModels.value
      .filter(isCompleteMappedTargetModel)
      .filter(model => !existingModels.has(model.toLowerCase()))
      .map(model => createEmbeddingCapabilityRow(nextEmbeddingCapabilityRowId(), model))
    if (!rowsToAdd.length) return
    form.embeddingCapabilityRows = [...form.embeddingCapabilityRows, ...rowsToAdd]
  }

  const syncModelCapabilitiesFromMappingWhenIdle = () => {
    if (isMappingTargetEditing.value) {
      hasPendingModelCapabilitySync.value = true
      return
    }
    hasPendingModelCapabilitySync.value = false
    if (props.channelType === 'vectors') {
      syncEmbeddingCapabilitiesFromMapping()
    } else {
      syncModelCapabilitiesFromMapping()
    }
  }

  const startMappingTargetEdit = () => {
    isMappingTargetEditing.value = true
  }

  const finishMappingTargetEdit = () => {
    if (!isMappingTargetEditing.value) return
    isMappingTargetEditing.value = false
    if (!hasPendingModelCapabilitySync.value) return
    hasPendingModelCapabilitySync.value = false
    nextTick(() => {
      if (props.channelType === 'vectors') {
        syncEmbeddingCapabilitiesFromMapping()
      } else {
        syncModelCapabilitiesFromMapping()
      }
    })
  }

  const { headerClasses, avatarColor, headerIconStyle, subtitleClasses } = useChannelEditorHeaderState(theme)

  const isFormValid = computed(() => {
    const hasValidName = isAutoManagedChannel.value || !!form.name.trim()
    const hasValidBaseUrl = isAutoManagedChannel.value || form.serviceType === 'copilot' || (!!form.baseUrl.trim() && isValidUrl(form.baseUrl))
    const hasValidApiKeys = form.serviceType === 'copilot' || hasConfigurableKeys.value
    const hasValidModelConfig = isAutoManagedChannel.value || (!modelCapabilitiesError.value && !embeddingCapabilitiesError.value)
    return (
      hasValidName && !!form.serviceType && hasValidBaseUrl && hasValidApiKeys && hasValidModelConfig
    )
  })

  const buildSubmitPayload = () => {
    const payload = buildChannelPayload(form, { channelType: props.channelType })
    if (isAutoManagedChannel.value && props.channel) {
      Object.assign(payload, {
        // 官网地址允许 Provider 托管渠道编辑，其余元数据字段仍沿用模板值
        website: form.website ?? '',
        description: props.channel.description || '',
        tags: [...(props.channel.tags || [])],
        modelMapping: { ...(props.channel.modelMapping || {}) },
        modelCapabilities: { ...(props.channel.modelCapabilities || {}) },
        embeddingCapabilities: { ...(props.channel.embeddingCapabilities || {}) },
        defaultCapability: { ...(props.channel.defaultCapability || {}) },
        allowUnknownContext: !!props.channel.allowUnknownContext,
        reasoningMapping: { ...(props.channel.reasoningMapping || {}) },
        reasoningParamStyle: props.channel.reasoningParamStyle,
        textVerbosity: props.channel.textVerbosity,
        fastMode: !!props.channel.fastMode,
        supportedModels: [...(props.channel.supportedModels || [])],
        noVision: !!props.channel.noVision,
        noVisionModels: [...(props.channel.noVisionModels || [])],
        visionFallbackModel: props.channel.visionFallbackModel || '',
        lowQuality: !!props.channel.lowQuality,
        injectDummyThoughtSignature: !!props.channel.injectDummyThoughtSignature,
        stripThoughtSignature: !!props.channel.stripThoughtSignature,
        autoBlacklistBalance: props.channel.autoBlacklistBalance,
        normalizeMetadataUserId: props.channel.normalizeMetadataUserId,
        stripBillingHeader: props.channel.stripBillingHeader,
        normalizeSystemRoleToTopLevel: props.channel.normalizeSystemRoleToTopLevel,
        codexToolCompat: props.channel.codexToolCompat,
        stripCodexClientTools: props.channel.stripCodexClientTools,
        convertImageUrlToB64Json: props.channel.convertImageUrlToB64Json,
        historicalImageTurnLimit: props.channel.historicalImageTurnLimit,
        customHeaders: { ...(props.channel.customHeaders || {}) },
        proxyUrl: props.channel.proxyUrl || '',
        routePrefix: props.channel.routePrefix || '',
        requestTimeoutMs: props.channel.requestTimeoutMs,
        responseHeaderTimeoutMs: props.channel.responseHeaderTimeoutMs,
        streamFirstContentTimeoutMs: props.channel.streamFirstContentTimeoutMs,
        streamInactivityTimeoutMs: props.channel.streamInactivityTimeoutMs,
        streamToolCallIdleTimeoutMs: props.channel.streamToolCallIdleTimeoutMs,
        rateLimitRpm: props.channel.rateLimitRpm,
        rateLimitWindowMinutes: props.channel.rateLimitWindowMinutes,
        rateLimitBurst: props.channel.rateLimitBurst,
        rateLimitMaxConcurrent: props.channel.rateLimitMaxConcurrent,
        rateLimitAutoFromHeaders: props.channel.rateLimitAutoFromHeaders,
      })
      payload.serviceType = props.channel.serviceType
      payload.baseUrl = props.channel.baseUrl
      if (props.channel.baseUrls?.length) {
        payload.baseUrls = [...props.channel.baseUrls]
      } else {
        delete payload.baseUrls
      }
      payload.insecureSkipVerify = !!props.channel.insecureSkipVerify
      if (props.channel.authHeader) {
        payload.authHeader = props.channel.authHeader
      } else {
        delete payload.authHeader
      }
      return payload
    }
    applyVisionFallbackReasoning(payload)
    if (!form.streamFirstContentTimeoutEnabled) {
      delete payload.streamFirstContentTimeoutMs
      if (isEditing.value && props.channel?.streamFirstContentTimeoutMs) {
        payload.streamFirstContentTimeoutMs = 0
      }
    }
    if (!form.streamInactivityTimeoutEnabled) {
      delete payload.streamInactivityTimeoutMs
      if (isEditing.value && props.channel?.streamInactivityTimeoutMs) {
        payload.streamInactivityTimeoutMs = 0
      }
    }
    if (!form.streamToolCallIdleTimeoutEnabled) {
      delete payload.streamToolCallIdleTimeoutMs
      if (isEditing.value && props.channel?.streamToolCallIdleTimeoutMs) {
        payload.streamToolCallIdleTimeoutMs = 0
      }
    }
    if (isEditing.value && props.channel?.requestTimeoutMs && !payload.requestTimeoutMs) {
      payload.requestTimeoutMs = 0
    }
    if (isEditing.value && props.channel?.responseHeaderTimeoutMs && !payload.responseHeaderTimeoutMs) {
      payload.responseHeaderTimeoutMs = 0
    }
    if (isEditing.value && props.channel?.rateLimitRpm && !payload.rateLimitRpm) {
      payload.rateLimitRpm = 0
    }
    if (isEditing.value && props.channel?.rateLimitWindowMinutes && !payload.rateLimitWindowMinutes) {
      payload.rateLimitWindowMinutes = 0
    }
    if (isEditing.value && props.channel?.rateLimitMaxConcurrent && !payload.rateLimitMaxConcurrent) {
      payload.rateLimitMaxConcurrent = 0
    }
    return payload
  }

  const applyVisionFallbackReasoning = (payload: Partial<Channel>) => {
    const fallbackModel = normalizeSelectableString(form.visionFallbackModel).trim()
    if (!supportsReasoningMappingOptions.value || !fallbackModel) {
      return
    }

    const reasoningMapping = { ...(payload.reasoningMapping || {}) }
    if (form.visionFallbackReasoningEffort) {
      reasoningMapping[fallbackModel] = form.visionFallbackReasoningEffort
    } else if (!modelMappingRows.value.some(row => row.source === fallbackModel && row.reasoning)) {
      delete reasoningMapping[fallbackModel]
    }
    payload.reasoningMapping = reasoningMapping
  }

  // 表单操作
  const resetForm = () => {
    resetTransientUiState()
    form.name = ''
    form.serviceType = props.channelType === 'images' || props.channelType === 'vectors' ? 'openai' : ''
    form.authHeader = 'auto'
    form.baseUrl = ''
    form.baseUrls = []
    form.website = ''
    form.insecureSkipVerify = false
    form.lowQuality = false
    form.injectDummyThoughtSignature = false
    form.stripThoughtSignature = false
    form.normalizeSystemRoleToTopLevel = false
    form.description = ''
    form.apiKeys = []
    form.apiKeyConfigs = undefined
    form.modelMapping = {}
    form.modelCapabilitiesText = ''
    form.modelCapabilityRows = []
    form.embeddingCapabilityRows = []
    form.defaultContextWindowTokens = null
    form.defaultMaxOutputTokens = null
    form.allowUnknownContext = false
    form.reasoningMapping = {}

    // 清空模型映射行
    modelMappingRows.value = []

    form.reasoningParamStyle = 'reasoning'
    form.textVerbosity = ''
    form.fastMode = false
    form.customHeaders = {}
    form.proxyUrl = ''
    form.requestTimeoutMs = null
    form.responseHeaderTimeoutMs = null
    form.streamFirstContentTimeoutEnabled = false
    form.streamFirstContentTimeoutMs = defaultStreamTimeouts.firstContentMs
    form.streamInactivityTimeoutEnabled = false
    form.streamInactivityTimeoutMs = defaultStreamTimeouts.inactivityMs
    form.streamToolCallIdleTimeoutEnabled = false
    form.streamToolCallIdleTimeoutMs = defaultStreamTimeouts.toolCallIdleMs
    form.rateLimitRpm = null
    form.rateLimitWindowMinutes = null
    form.rateLimitMaxConcurrent = null
    form.rateLimitAutoFromHeaders = true
    form.routePrefix = ''
    form.supportedModels = []
    supportedModelsError.value = ''
    form.autoBlacklistBalance = true
    form.normalizeMetadataUserId = defaultNormalizeMetadataUserId()
    form.stripBillingHeader = false
    stripBillingHeaderTouched.value = false
    form.codexToolCompat = false
    form.stripCodexClientTools = false
    form.convertImageUrlToB64Json = false
    form.noVision = false
    form.noVisionModels = []
    form.visionFallbackModel = ''
    form.visionFallbackReasoningEffort = ''
    form.historicalImageTurnLimit = 0

    // 重置 baseUrlsText
    baseUrlsText.value = ''

    // 清空模型缓存和状态
    resetTargetModelOptions()
    fetchingModels.value = false
    fetchModelsError.value = ''
    keyModelsStatus.value.clear()

    }

  const loadChannelData = (channel: Channel) => {
    resetTransientUiState()
    form.name = channel.name
    form.serviceType = props.channelType === 'images' || props.channelType === 'vectors' ? 'openai' : channel.serviceType
    form.authHeader = channel.authHeader || 'auto'
    form.baseUrl = channel.baseUrl
    form.baseUrls = channel.baseUrls || []
    const providerWebsiteLinks = getManagedProviderWebsiteLinks(channel)
    form.website = channel.website || (providerWebsiteLinks.length === 1 ? providerWebsiteLinks[0].url : '')
    form.insecureSkipVerify = !!channel.insecureSkipVerify
    form.lowQuality = !!channel.lowQuality
    form.injectDummyThoughtSignature = !!channel.injectDummyThoughtSignature
    form.stripThoughtSignature = !!channel.stripThoughtSignature
    form.normalizeSystemRoleToTopLevel = !!channel.normalizeSystemRoleToTopLevel
    form.description = channel.description || ''
    form.tags = [...(channel.tags || [])]

    // 同步 baseUrlsText（优先使用 baseUrls，否则使用 baseUrl），保留用户显式配置的原始 URL 形式
    const rawUrls = channel.baseUrls && channel.baseUrls.length > 0
      ? channel.baseUrls
      : (channel.baseUrl ? [channel.baseUrl] : [])
    baseUrlsText.value = rawUrls.join('\n')

    // 直接存储原始密钥，不需要映射关系
    form.apiKeys = [...channel.apiKeys]
    form.apiKeyConfigs = channel.apiKeyConfigs
      ? channel.apiKeyConfigs.map(cfg => ({
          ...cfg,
          models: cfg.models ? [...cfg.models] : undefined,
        }))
      : undefined

    form.modelMapping = { ...(channel.modelMapping || {}) }
    form.modelCapabilitiesText = Object.keys(channel.modelCapabilities || {}).length > 0
      ? JSON.stringify(normalizeModelCapabilities(channel.modelCapabilities), null, 2)
      : ''
    form.modelCapabilityRows = modelCapabilitiesToRows(channel.modelCapabilities || {}, nextCapabilityRowId)
    form.embeddingCapabilityRows = embeddingCapabilitiesToRows(channel.embeddingCapabilities || {}, nextEmbeddingCapabilityRowId)
    form.defaultContextWindowTokens = channel.defaultCapability?.contextWindowTokens || null
    form.defaultMaxOutputTokens = channel.defaultCapability?.maxOutputTokens || null
    form.allowUnknownContext = !!channel.allowUnknownContext
    form.reasoningMapping = { ...(channel.reasoningMapping || {}) }

    // 加载模型映射行
    loadModelMappingRows(channel)

    form.reasoningParamStyle = channel.reasoningParamStyle || 'reasoning'
    form.textVerbosity = channel.textVerbosity || ''
    form.fastMode = !!channel.fastMode
    form.customHeaders = { ...(channel.customHeaders || {}) }
    form.proxyUrl = channel.proxyUrl || ''
    form.requestTimeoutMs = channel.requestTimeoutMs || null
    form.responseHeaderTimeoutMs = channel.responseHeaderTimeoutMs || null
    form.streamFirstContentTimeoutEnabled = !!(channel.streamFirstContentTimeoutMs && channel.streamFirstContentTimeoutMs > 0)
    form.streamFirstContentTimeoutMs = channel.streamFirstContentTimeoutMs && channel.streamFirstContentTimeoutMs > 0 ? channel.streamFirstContentTimeoutMs : defaultStreamTimeouts.firstContentMs
    form.streamInactivityTimeoutEnabled = !!(channel.streamInactivityTimeoutMs && channel.streamInactivityTimeoutMs > 0)
    form.streamInactivityTimeoutMs = channel.streamInactivityTimeoutMs && channel.streamInactivityTimeoutMs > 0 ? channel.streamInactivityTimeoutMs : defaultStreamTimeouts.inactivityMs
    form.streamToolCallIdleTimeoutEnabled = !!(channel.streamToolCallIdleTimeoutMs && channel.streamToolCallIdleTimeoutMs >= 30000)
    form.streamToolCallIdleTimeoutMs = channel.streamToolCallIdleTimeoutMs && channel.streamToolCallIdleTimeoutMs >= 30000 ? channel.streamToolCallIdleTimeoutMs : defaultStreamTimeouts.toolCallIdleMs
    form.rateLimitRpm = (channel.rateLimitRpm && channel.rateLimitRpm > 0) ? channel.rateLimitRpm : null
    form.rateLimitWindowMinutes = (channel.rateLimitWindowMinutes && channel.rateLimitWindowMinutes > 0) ? channel.rateLimitWindowMinutes : null
    form.rateLimitMaxConcurrent = (channel.rateLimitMaxConcurrent && channel.rateLimitMaxConcurrent > 0) ? channel.rateLimitMaxConcurrent : null
    form.rateLimitAutoFromHeaders = channel.rateLimitAutoFromHeaders !== false
    form.routePrefix = channel.routePrefix || ''
    const { validPatterns, hasInvalidPatterns } = filterValidSupportedModelPatterns(channel.supportedModels || [])
    form.supportedModels = validPatterns
    supportedModelsError.value = hasInvalidPatterns ? t('addChannel.supportedModelsInvalidPattern') : ''
    form.autoBlacklistBalance = channel.autoBlacklistBalance ?? true
    form.normalizeMetadataUserId = channel.normalizeMetadataUserId ?? true
    form.stripBillingHeader = channel.stripBillingHeader ?? false
    // 载入既有渠道的值即视为显式配置，避免 baseUrl watcher 覆盖用户已保存的选择
    stripBillingHeaderTouched.value = true
    form.codexToolCompat = channel.codexToolCompat ?? channel.stripCodexClientTools ?? false
    form.stripCodexClientTools = channel.codexToolCompat ?? channel.stripCodexClientTools ?? false
    form.convertImageUrlToB64Json = !!channel.convertImageUrlToB64Json
    form.noVision = !!channel.noVision
    form.noVisionModels = [...(channel.noVisionModels || [])]
    form.visionFallbackModel = channel.visionFallbackModel || ''
    form.visionFallbackReasoningEffort = (channel.reasoningMapping?.[form.visionFallbackModel] || '') as 'none' | 'minimal' | 'low' | 'medium' | 'high' | 'xhigh' | 'max' | ''
    form.historicalImageTurnLimit = channel.historicalImageTurnLimit ?? 0

    // 立即同步 baseUrl 到预览变量，避免等待 debounce
    formBaseUrlPreview.value = channel.baseUrl

    // 清空模型缓存和状态（切换渠道时重置）
    resetTargetModelOptions()
    fetchingModels.value = false
    fetchModelsError.value = ''
    keyModelsStatus.value.clear()

    // 如果有模型映射配置，主动预加载模型列表
    if (channel.modelMapping && Object.keys(channel.modelMapping).length > 0) {
      nextTick(() => {
        fetchTargetModels()
      })
    }
  }

  const {
    restoringKey,
    visibleDisabledKeys,
    resetRestoredKeys,
    restoreDisabledKey,
    restoringKeyModel,
    visibleDisabledKeyModels,
    restoreDisabledKeyModel,
    suspendingKey,
    suspendKey,
    resumeKey,
  } = useDisabledApiKeys({
    apiService,
    channel: computed(() => props.channel),
    channelType: channelTypeRef,
    emitError: message => emit('error', message),
    form,
  })

  // 提交状态
  const submitting = ref(false)

  const {
    targetModelOptions,
    resetTargetModelOptions,
    fetchingModels,
    fetchModelsError,
    keyModelsStatus,
    ensureTargetModelsLoaded,
    fetchTargetModels,
  } = useTargetModelFetch({
    apiService,
    channel: computed(() => props.channel),
    channelType: channelTypeRef,
    defaultServiceType: defaultServiceTypeValueFallback,
    form,
    isEditing,
    t,
    visibleDisabledKeys,
  })

  const {
    baseUrlHasError,
    expectedRequestUrls,
    customHeadersArray,
    updateCustomHeaders,
  } = useChannelEditorFormDerived(channelTypeRef, form, baseUrlsText)

  // 将 modelMappingRows 转换为 form.modelMapping 对象（保存时使用）
  const syncModelMappingToForm = () => {
    form.modelMapping = {}
    form.reasoningMapping = {}
    form.noVisionModels = []
    const noVisionModels = new Set<string>()

    modelMappingRows.value.forEach(row => {
      if (row.source && row.target) {
        form.modelMapping[row.source] = row.target
        if (row.reasoning) {
          form.reasoningMapping[row.source] = row.reasoning
        }
        if (row.noVision) {
          noVisionModels.add(row.target)
        }
      }
    })

    form.noVisionModels = [...noVisionModels]
  }

  // 从渠道数据初始化 modelMappingRows
  const loadModelMappingRows = (channel: Channel) => {
    const mapping = channel.modelMapping || {}
    const reasoning = channel.reasoningMapping || {}
    const noVisionSet = new Set(channel.noVisionModels || [])

    modelMappingRows.value = Object.entries(mapping).map(([source, target]) => ({
      id: ++rowIdCounter,
      source,
      target,
      reasoning: (reasoning[source] || '') as ModelMappingRow['reasoning'],
      noVision: noVisionSet.has(target)
    }))
  }

  const syncModelMappingRowsFromForm = () => {
    const noVisionSet = new Set(form.noVisionModels || [])

    modelMappingRows.value = Object.entries(form.modelMapping || {}).map(([source, target]) => ({
      id: ++rowIdCounter,
      source,
      target,
      reasoning: (form.reasoningMapping[source] || '') as ModelMappingRow['reasoning'],
      noVision: noVisionSet.has(target)
    }))
  }

  const {
    showModelMappingPresets,
    showMessagesOpenAIChannelPresets,
    showClaudeChannelPresets,
    showCodexResponsesChannelPresets,
    applyPreset,
  } = useEditChannelPresets({
    channelType: channelTypeRef,
    form,
    supportsOpenAIAdvancedOptions,
    syncModelMappingRowsFromForm,
  })

  // 辅助函数：更新表单字段
  const updateForm = (partial: Record<string, unknown>) => {
    if ('stripBillingHeader' in partial) {
      stripBillingHeaderTouched.value = true
    }
    Object.assign(form, partial)
  }

  // 辅助函数：同步上游模型
  const syncUpstreamModels = () => {
    fetchTargetModels()
  }

  const handleSubmit = async () => {
    if (submitting.value || !formRef.value) return

    submitting.value = true
    let saveStarted = false

    try {
      if (!isAutoManagedChannel.value) {
        if (props.channelType === 'vectors') {
          syncEmbeddingCapabilitiesFromMapping()
        } else {
          syncModelCapabilitiesFromMapping()
        }
      }

      const { valid } = await formRef.value.validate()
      if (!valid) return
      if (!isAutoManagedChannel.value && (modelCapabilitiesError.value || embeddingCapabilitiesError.value)) return

      if (!isAutoManagedChannel.value) {
        syncModelMappingToForm()
      }

      const channelData = buildSubmitPayload()

      emit('save', channelData, undefined, () => {
        submitting.value = false
      })
      saveStarted = true
    } finally {
      if (!saveStarted) {
        submitting.value = false
      }
    }
  }

  const handleCancel = () => {
    if (submitting.value) return
    emit('update:show', false)
    resetForm()
  }

  const diagnosingCompat = ref(false)
  const diagnoseResult = ref<{ type: 'success' | 'error'; message: string; appliedCount: number } | null>(null)
  let diagnoseTimer: ReturnType<typeof setTimeout> | null = null

  const handleDiagnoseCompat = async () => {
    if (props.channel?.index === undefined || props.channel?.index === null) return
    if (props.channelType === 'images' || props.channelType === 'vectors') return

    diagnosingCompat.value = true
    if (diagnoseTimer) { clearTimeout(diagnoseTimer); diagnoseTimer = null }
    diagnoseResult.value = null
    try {
      const type = props.channelType as 'messages' | 'chat' | 'responses' | 'gemini'
      const result = await apiService.diagnoseChannelCompat(type, props.channel.index)
      const applied: string[] = []
      for (const [key, val] of Object.entries(result.recommendations)) {
        // 以下六项已完全收归运行时自动学习，不再是可编辑字段，诊断建议不写回表单
        if (RUNTIME_MANAGED_COMPAT_FIELDS.has(key)) continue
        if (val !== undefined && (form as Record<string, unknown>)[key] !== val) {
          updateForm({ [key]: val })
          applied.push(key)
        }
      }
      if (result.urlRecommendations?.recommended) {
        const current = result.urlRecommendations.current
        const recommended = result.urlRecommendations.recommended
        const lines = baseUrlsText.value.split('\n').map(line => line.trim()).filter(Boolean)
        const nextLines = lines.length > 0
          ? lines.map((line, index) => (index === 0 || line === current) ? recommended : line)
          : [recommended]
        baseUrlsText.value = Array.from(new Set(nextLines)).join('\n')
        applied.push('baseUrl')
      }
      const message = applied.length
        ? t('channelEditor.compat.diagnoseApplied', { count: applied.length })
        : t('channelEditor.compat.diagnoseNoChange')
      diagnoseResult.value = { type: 'success', message, appliedCount: applied.length }
    } catch (e) {
      diagnoseResult.value = { type: 'error', message: e instanceof Error ? e.message : t('channelEditor.compat.diagnoseFailed'), appliedCount: 0 }
    } finally {
      diagnosingCompat.value = false
      diagnoseTimer = setTimeout(() => { diagnoseResult.value = null }, 5000)
    }
  }

  // 监听props变化
  watch(
    () => props.show,
    newShow => {
      if (newShow) {
        dialogMode.value = props.channel ? 'edit' : 'create'
        resetRestoredKeys()
        if (diagnoseTimer) { clearTimeout(diagnoseTimer); diagnoseTimer = null }
        diagnoseResult.value = null

        if (dialogMode.value === 'edit' && props.channel) {
          // 编辑模式：使用完整表单
          loadChannelData(props.channel)
        } else {
          // 添加模式：固定使用快速添加
          resetForm()
        }
        restoreChannelDiscoverySession()

        // dialog 渲染完成后绑定滚动监听，同步左侧导航高亮
        nextTick(() => attachScrollListener())
      } else {
        detachScrollListener()
      }
    }
  )

  watch(
    () => props.channel,
    (newChannel, oldChannel) => {
      const action = resolveChannelWatcherAction({
        show: props.show,
        newChannel,
        oldChannel,
      })

      if (action === 'load-edit-channel' && newChannel) {
        dialogMode.value = 'edit'
        loadChannelData(newChannel)
        restoreChannelDiscoverySession()
        return
      }

      if (action === 'reset-new-form') {
        dialogMode.value = 'create'
        resetForm()
        restoreChannelDiscoverySession()
      }
    }
  )

  watch(
    () => props.show ? stableStringify(buildChannelDiscoveryRequest()) : '',
    () => {
      if (props.show) {
        restoreChannelDiscoverySession()
      }
    }
  )

  watch(
    () => form.baseUrl,
    value => {
      if (formBaseUrlPreviewTimer !== null) {
        window.clearTimeout(formBaseUrlPreviewTimer)
      }
      formBaseUrlPreviewTimer = window.setTimeout(() => {
        formBaseUrlPreview.value = value
      }, 200)
    },
    { immediate: true }
  )

  watch(
    mappedTargetModels,
    () => {
      syncModelCapabilitiesFromMappingWhenIdle()
    }
  )

  watch(
    () => JSON.stringify({
      baseUrl: form.baseUrl,
      baseUrls: form.baseUrls,
      apiKeys: form.apiKeys,
      proxyUrl: form.proxyUrl,
      insecureSkipVerify: form.insecureSkipVerify,
      customHeaders: form.customHeaders,
      authHeader: form.authHeader,
      serviceType: form.serviceType,
      routePrefix: form.routePrefix,
    }),
    () => {
      resetTargetModelOptions()
      keyModelsStatus.value.clear()
      fetchModelsError.value = ''
    }
  )

  // ESC键监听 & Cmd/Ctrl+Enter 确认
  const handleKeydown = (event: Event) => {
    const keyboardEvent = event as KeyboardEvent
    if (!props.show) return

    if (keyboardEvent.key === 'Escape') {
      if (submitting.value) {
        keyboardEvent.preventDefault()
        return
      }
      if (isAnySelectMenuOpen.value || Date.now() < suppressDialogEscapeUntil.value) {
        keyboardEvent.preventDefault()
        keyboardEvent.stopPropagation()
        return
      }
      keyboardEvent.preventDefault()
      handleCancel()
      return
    }

    // Cmd/Ctrl+Enter 确认提交
    if (keyboardEvent.key === 'Enter' && (keyboardEvent.metaKey || keyboardEvent.ctrlKey) && !keyboardEvent.shiftKey) {
      keyboardEvent.preventDefault()
      handleSubmit()
    }
  }

  onMounted(() => {
    document.addEventListener('keydown', handleKeydown)
  })

  onUnmounted(() => {
    document.removeEventListener('keydown', handleKeydown)
    detachScrollListener()
    if (formBaseUrlPreviewTimer !== null) {
      window.clearTimeout(formBaseUrlPreviewTimer)
    }
    if (diagnoseTimer !== null) {
      window.clearTimeout(diagnoseTimer)
    }
  })

  return {
    formRef,
    activeSection,
    sections,
    baseUrlHasError,
    onMenuUpdate,
    serviceTypeOptions,
    sourceModelOptions,
    modelMappingHint,
    targetModelPlaceholder,
    reasoningEffortOptions,
    reasoningParamStyleOptions,
    textVerbosityOptions,
    supportsOpenAIAdvancedOptions,
    supportsReasoningMappingOptions,
    supportsChannelDiscovery,
    isAutoManagedChannel,
    showModelMappingPresets,
    showMessagesOpenAIChannelPresets,
    showClaudeChannelPresets,
    showCodexResponsesChannelPresets,
    form,
    baseUrlsText,
    modelMappingRows,
    hasNoVisionRows,
    mappedTargetModels,
    sourceMappingError,
    targetModelOptions,
    fetchingModels,
    fetchModelsError,
    keyModelsStatus,
    errors,
    rules,
    isEditing,
    isMac,
    selectedStreamTimeoutStrategy,
    applyStreamTimeoutStrategy,
    commonSupportedModelFilters,
    selectedSupportedModelSet,
    supportedModelsError,
    modelCapabilitiesError,
    embeddingCapabilitiesError,
    startMappingTargetEdit,
    finishMappingTargetEdit,
    headerClasses,
    avatarColor,
    headerIconStyle,
    subtitleClasses,
    isFormValid,
    handleSupportedModelsChange,
    restoringKey,
    submitting,
    visibleDisabledKeys,
    expectedRequestUrls,
    customHeadersArray,
    updateCustomHeaders,
    restoreDisabledKey,
    restoringKeyModel,
    visibleDisabledKeyModels,
    restoreDisabledKeyModel,
    suspendingKey,
    suspendKey,
    resumeKey,
    appendSupportedModelFilter,
    ensureTargetModelsLoaded,
    updateForm,
    syncUpstreamModels,
    discoveringChannelConfig,
    channelDiscoveryResult,
    channelDiscoveryError,
    channelDiscoveryModelMappingEntries,
    channelDiscoveryCompatEntries,
    channelDiscoveryReasoningEntries,
    channelDiscoverySuccessfulProtocols,
    channelDiscoveryCapabilityEntries,
    handleDiscoverChannelConfig,
    applyChannelDiscoveryRecommendation,
    applyPreset,
    handleSubmit,
    handleCancel,
    diagnosingCompat,
    diagnoseResult,
    handleDiagnoseCompat,
    scrollToSection,
    setSectionRef,
    t,
  }
}
