import { ref, reactive, computed, watch, onMounted, onUnmounted, nextTick } from 'vue'
import { useTheme } from 'vuetify'
import type { Channel } from '../services/api'
import { ApiService } from '../services/api'
import {
  buildChannelPayload,
  embeddingCapabilitiesToRows,
  modelCapabilitiesToRows,
  normalizeSelectableString,
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
import { useEditChannelSectionNav } from './useEditChannelSectionNav'
import { useTargetModelFetch } from './useTargetModelFetch'
import { useEditChannelOptions } from '../utils/editChannelOptions'
import { defaultStripBillingHeader, isValidUrl, normalizeModelCapabilities } from '../utils/editChannelHelpers'
import { isOfficialProviderChannel } from '../utils/providerDisplay'
import { getManagedProviderWebsiteLinks } from '../utils/channelWebsite'
import { useChannelStore } from '../stores/channel'
import { useDialogStore } from '../stores/dialog'

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
  updated: []
}

type EditChannelModalEmit = <K extends keyof EditChannelModalEmits>(event: K, ...args: EditChannelModalEmits[K]) => void
type ResolvedEditChannelModalProps = Readonly<EditChannelModalProps & { channelType: NonNullable<EditChannelModalProps['channelType']> }>


export function useEditChannelModal(props: ResolvedEditChannelModalProps, emit: EditChannelModalEmit) {
  const { t } = useI18n()
  const apiService = new ApiService()
  const channelStore = useChannelStore()
  const dialogStore = useDialogStore()

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
  // 所有渠道均为自动托管；仅自定义手填地址（无 providerId 且非官方直连）可编辑地址池。
  const isEditableBaseUrlsChannel = computed(() => !props.channel?.providerId && !isOfficialProviderChannel(props.channel))
  const sections = computed(() => allSections.filter(section => section.id === 'basic' || section.id === 'auth' || section.id === 'custom'))

  // 流式超时默认值（form 初始化用）
  const defaultStreamTimeouts = { ...streamTimeoutPresets.balanced }

  const form = reactive({
    name: '',
    remark: '',
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
    costMultiplier: null as string | number | null,
    channelPaymentCurrency: '',
    channelPaymentAmount: null as string | number | null,
    channelCreditCurrency: '',
    channelCreditAmount: null as string | number | null,
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


  function resetTransientUiState() {
    resetRestoredKeys()
    errors.name = ''
    errors.serviceType = ''
    errors.baseUrl = ''
    errors.website = ''
    formBaseUrlPreview.value = ''
  }

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

  const draftBaseUrls = () => {
    return baseUrlsText.value
      .split('\n')
      .map(line => line.trim())
      .filter(Boolean)
  }

  // 托管账号提交用的地址池：复用 baseUrlsText -> form 的规范化去重结果，
  // form.baseUrls 仅在多地址时填充，单地址场景回退到 form.baseUrl。
  const managedAccountBaseUrls = () => {
    if (form.baseUrls.length > 0) return [...form.baseUrls]
    const single = form.baseUrl.trim()
    return single ? [single] : []
  }



  const { headerClasses, avatarColor, headerIconStyle, subtitleClasses } = useChannelEditorHeaderState(theme)

  const isFormValid = computed(() => {
    const draftUrls = draftBaseUrls()
    const hasValidBaseUrl = form.serviceType === 'copilot'
      || (isEditableBaseUrlsChannel.value && draftUrls.length > 0 && draftUrls.every(isValidUrl))
      || !isEditableBaseUrlsChannel.value
    const hasValidApiKeys = form.serviceType === 'copilot' || hasConfigurableKeys.value
    return (
      !!form.serviceType && hasValidBaseUrl && hasValidApiKeys
    )
  })

  const buildSubmitPayload = () => {
    const payload = buildChannelPayload(form, { channelType: props.channelType })
    if (!props.channel) {
      // 清理未启用的流式超时字段
      if (!form.streamFirstContentTimeoutEnabled) {
        delete payload.streamFirstContentTimeoutMs
      }
      if (!form.streamInactivityTimeoutEnabled) {
        delete payload.streamInactivityTimeoutMs
      }
      if (!form.streamToolCallIdleTimeoutEnabled) {
        delete payload.streamToolCallIdleTimeoutMs
      }
      return payload
    }

    // 所有渠道均为自动托管：大量元数据字段沿用渠道当前值，避免前端误覆盖模板/学习结果。
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
      // 代理/自定义请求头/备注：取表单值（用户可编辑），而非沿用渠道旧值——
      // 此前沿用旧值导致编辑保存后不生效（proxyUrl/customHeaders 保存丢失的根因）。
      customHeaders: { ...form.customHeaders },
      proxyUrl: form.proxyUrl.trim(),
      remark: form.remark.trim(),
      costMultiplier: form.costMultiplier,
      channelPaymentCurrency: form.channelPaymentCurrency,
      channelPaymentAmount: form.channelPaymentAmount,
      channelCreditCurrency: form.channelCreditCurrency,
      channelCreditAmount: form.channelCreditAmount,
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
    if (isEditableBaseUrlsChannel.value) {
      // 可编辑地址池的托管渠道：沿用表单规范化结果，baseUrl 取首个地址保持旧字段兼容
      const managedUrls = managedAccountBaseUrls()
      payload.baseUrl = managedUrls[0] || props.channel.baseUrl
      if (managedUrls.length > 1) {
        payload.baseUrls = managedUrls
      } else {
        delete payload.baseUrls
      }
    } else {
      payload.baseUrl = props.channel.baseUrl
      if (props.channel.baseUrls?.length) {
        payload.baseUrls = [...props.channel.baseUrls]
      } else {
        delete payload.baseUrls
      }
    }
    payload.insecureSkipVerify = !!props.channel.insecureSkipVerify
    if (props.channel.authHeader) {
      payload.authHeader = props.channel.authHeader
    } else {
      delete payload.authHeader
    }

    // 清理未启用的流式超时字段；编辑场景下若原有值且当前未启用，则显式置 0 清空。
    if (!form.streamFirstContentTimeoutEnabled) {
      delete payload.streamFirstContentTimeoutMs
      if (props.channel.streamFirstContentTimeoutMs) {
        payload.streamFirstContentTimeoutMs = 0
      }
    }
    if (!form.streamInactivityTimeoutEnabled) {
      delete payload.streamInactivityTimeoutMs
      if (props.channel.streamInactivityTimeoutMs) {
        payload.streamInactivityTimeoutMs = 0
      }
    }
    if (!form.streamToolCallIdleTimeoutEnabled) {
      delete payload.streamToolCallIdleTimeoutMs
      if (props.channel.streamToolCallIdleTimeoutMs) {
        payload.streamToolCallIdleTimeoutMs = 0
      }
    }
    if (props.channel.requestTimeoutMs && !payload.requestTimeoutMs) {
      payload.requestTimeoutMs = 0
    }
    if (props.channel.responseHeaderTimeoutMs && !payload.responseHeaderTimeoutMs) {
      payload.responseHeaderTimeoutMs = 0
    }
    if (props.channel.rateLimitRpm && !payload.rateLimitRpm) {
      payload.rateLimitRpm = 0
    }
    if (props.channel.rateLimitWindowMinutes && !payload.rateLimitWindowMinutes) {
      payload.rateLimitWindowMinutes = 0
    }
    if (props.channel.rateLimitMaxConcurrent && !payload.rateLimitMaxConcurrent) {
      payload.rateLimitMaxConcurrent = 0
    }

    return payload
  }


  // 表单操作
  const resetForm = () => {
    resetTransientUiState()
    form.name = ''
    form.remark = ''
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

    form.reasoningParamStyle = 'reasoning'
    form.textVerbosity = ''
    form.fastMode = false
    form.customHeaders = {}
    form.proxyUrl = ''
    form.costMultiplier = null
    form.channelPaymentCurrency = ''
    form.channelPaymentAmount = null
    form.channelCreditCurrency = ''
    form.channelCreditAmount = null
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
    form.remark = channel.remark || ''
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
    let capabilityRowId = 0
    let embeddingCapabilityRowId = 0
    form.modelCapabilityRows = modelCapabilitiesToRows(channel.modelCapabilities || {}, () => ++capabilityRowId)
    form.embeddingCapabilityRows = embeddingCapabilitiesToRows(channel.embeddingCapabilities || {}, () => ++embeddingCapabilityRowId)
    form.defaultContextWindowTokens = channel.defaultCapability?.contextWindowTokens || null
    form.defaultMaxOutputTokens = channel.defaultCapability?.maxOutputTokens || null
    form.allowUnknownContext = !!channel.allowUnknownContext
    form.reasoningMapping = { ...(channel.reasoningMapping || {}) }

    form.reasoningParamStyle = channel.reasoningParamStyle || 'reasoning'
    form.textVerbosity = channel.textVerbosity || ''
    form.fastMode = !!channel.fastMode
    form.customHeaders = { ...(channel.customHeaders || {}) }
    form.proxyUrl = channel.proxyUrl || ''
    form.costMultiplier = channel.costMultiplier ?? null
    form.channelPaymentCurrency = channel.channelPaymentCurrency ?? ''
    form.channelPaymentAmount = channel.channelPaymentAmount ?? null
    form.channelCreditCurrency = channel.channelCreditCurrency ?? ''
    form.channelCreditAmount = channel.channelCreditAmount ?? null
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

    // 打开编辑即预加载各 Key 的上游模型列表，Key 行需展示每个 Key 的模型数
    if (form.apiKeys.length > 0 || visibleDisabledKeys.value.length > 0) {
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
    removingKey,
    removeDisabledKey,
    restoringKeyModel,
    visibleDisabledKeyModels,
    restoreDisabledKeyModel,
    changingGroupModel,
    visibleDisabledGroupModels,
    disableGroupModel,
    restoreDisabledGroupModel,
    suspendingKey,
    suspendKey,
    resumeKey,
  } = useDisabledApiKeys({
    apiService,
    channel: computed(() => props.channel),
    channelType: channelTypeRef,
    emitError: message => emit('error', message),
    form,
    onKeysChanged: async () => {
      const routeKind = props.channel?.routeKind ?? props.channelType
      const routeIndex = props.channel?.routeIndex ?? props.channel?.index
      await channelStore.refreshChannels()
      if (routeIndex == null) return
      const latest = channelStore.currentChannelsData.channels.find(channel =>
        (channel.routeKind === routeKind && (channel.routeIndex ?? channel.index) === routeIndex)
        || channel.protocolRoutes?.some(route => route.kind === routeKind && route.index === routeIndex),
      )
      if (latest) dialogStore.editingChannel = latest
    },
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

  // 从渠道数据初始化 modelMappingRows


  // 辅助函数：更新表单字段
  const updateForm = (partial: Record<string, unknown>) => {
    if ('stripBillingHeader' in partial) {
      stripBillingHeaderTouched.value = true
    }
    Object.assign(form, partial)
  }


  const handleSubmit = async () => {
    if (submitting.value || !formRef.value) return

    submitting.value = true
    let saveStarted = false

    try {
      const { valid } = await formRef.value.validate()
      if (!valid) return

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

  // 监听props变化
  watch(
    () => props.show,
    newShow => {
      if (newShow) {
        dialogMode.value = props.channel ? 'edit' : 'create'
        resetRestoredKeys()

        if (dialogMode.value === 'edit' && props.channel) {
          // 编辑模式：使用完整表单
          loadChannelData(props.channel)
        } else {
          // 添加模式：固定使用快速添加
          resetForm()
        }

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
        return
      }

      if (action === 'reset-new-form') {
        dialogMode.value = 'create'
        resetForm()
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
  })

  return {
    formRef,
    activeSection,
    sections,
    baseUrlHasError,
    onMenuUpdate,
    serviceTypeOptions,
    form,
    baseUrlsText,
    targetModelOptions,
    keyModelsStatus,
    errors,
    rules,
    isEditing,
    isMac,
    headerClasses,
    avatarColor,
    headerIconStyle,
    subtitleClasses,
    isFormValid,
    restoringKey,
    submitting,
    visibleDisabledKeys,
    expectedRequestUrls,
    customHeadersArray,
    updateCustomHeaders,
    restoreDisabledKey,
    removingKey,
    removeDisabledKey,
    restoringKeyModel,
    visibleDisabledKeyModels,
    restoreDisabledKeyModel,
    changingGroupModel,
    visibleDisabledGroupModels,
    disableGroupModel,
    restoreDisabledGroupModel,
    suspendingKey,
    suspendKey,
    resumeKey,
    ensureTargetModelsLoaded,
    updateForm,
    handleSubmit,
    handleCancel,
    scrollToSection,
    setSectionRef,
    t,
  }
}
