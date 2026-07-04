import { ref, reactive, computed, watch, onMounted, onBeforeUnmount, nextTick } from 'vue'
import { useConsoleChannels } from '@/composables/useConsoleChannels'
import { useLanguage } from '@/composables/useLanguage'
import { useAdminApi } from '@/composables/useAdminApi'
import {
  buildChannelPayload,
  modelCapabilitiesToRows,
  modelCapabilityRowsToRecord,
  type ModelCapabilityRow,
} from '@/utils/channel-payload'
import { supportsAdvancedChannelOptions, supportsReasoningMapping } from '@/utils/channel-advanced-options'
import { extractChannelNamePrefix, syncBaseUrlsFormState } from '@/utils/channel-dialog-state'
import type { ManagedChannelType } from '@/utils/channel-type-api'
import { buildExpectedRequestUrls } from '@/utils/expected-request-urls'
import { parseQuickInput } from '@/utils/quick-input-parser'
import { defaultStreamTimeouts } from '@/utils/stream-timeout-presets'
import type { Channel, CompatDiagnoseResult, DisabledKeyInfo } from '@/services/admin-api'
import { useChannelEditSectionNav } from '@/composables/useChannelEditSectionNav'
import { useCopilotOAuth } from '@/composables/useCopilotOAuth'
import { useModelAutocomplete } from '@/composables/useModelAutocomplete'
import { useChannelApiKeys } from '@/composables/useChannelApiKeys'
import { useChannelEditPresets } from '@/composables/useChannelEditPresets'
import { useChannelModelMapping, type ReasoningEffort } from '@/composables/useChannelModelMapping'
import { useChannelTargetModels, type KeyModelsStatus } from '@/composables/useChannelTargetModels'
import { useChannelCustomHeaders } from '@/composables/useChannelCustomHeaders'
import { useChannelEditorOptions } from '@/composables/useChannelEditorOptions'

export interface ChannelEditDialogProps {
  channel?: Channel | null
  channelType: ManagedChannelType
}

export type ChannelEditDialogEmit = {
  (e: 'close'): void
  (e: 'saved'): void
  (e: 'test-capability', channel: Channel): void
}

export function useChannelEditDialog(props: ChannelEditDialogProps, emit: ChannelEditDialogEmit) {
const { t } = useLanguage()
  const { saveChannel, restoreApiKey } = useConsoleChannels()
  const adminApi = useAdminApi()

  const isEditMode = computed(() => !!props.channel)
  const isMac = computed(() => typeof navigator !== 'undefined' && /Mac|iPod|iPhone|iPad/.test(navigator.platform))
  const saving = ref(false)
  const error = ref('')
  const success = ref('')
  const diagnosingCompat = ref(false)
  const diagnoseResult = ref<{ type: 'success' | 'error'; message: string; appliedCount: number } | null>(null)
  let diagnoseTimer: ReturnType<typeof setTimeout> | null = null
  const quickInput = ref('')
  const quickServiceTypeTouched = ref(false)
  const copilotDefaultBaseUrl = 'https://api.githubcopilot.com'
  const defaultNormalizeMetadataUserId = () => props.channelType === 'messages'
  const disabledApiKeys = computed<DisabledKeyInfo[]>(() => props.channel?.disabledApiKeys ?? [])
  const historicalApiKeys = computed(() => props.channel?.historicalApiKeys ?? [])
  const {
    restoringKey,
    existingApiKeys,
    newApiKeysText,
    copiedKeyIndex,
    duplicateKeyIndex,
    localRestoredKeys,
    visibleDisabledKeys,
    clearDuplicateKeyHighlight,
    removeExistingApiKey,
    getSubmitApiKeys,
    addNewApiKeys,
    copyApiKey,
    moveApiKeyToTop,
    moveApiKeyToBottom,
    handleDisabledKeyRestore,
  } = useChannelApiKeys({
    channel: computed(() => props.channel),
    channelType: () => props.channelType,
    disabledApiKeys,
    error,
    fallbackApiKeysText: () => form.apiKeysText,
    isEditMode,
    parseLines,
    restoreApiKey,
    t,
  })

  const {
    copilotOAuthLoading,
    copilotPolling,
    copilotOAuthError,
    copilotOAuthSuccess,
    copilotUserCode,
    copilotUserCodeCopied,
    clearCopilotPollTimer,
    clearCopilotCopyTimer,
    copyCopilotUserCode,
    startCopilotOAuth,
    openCopilotAuthorization,
  } = useCopilotOAuth(existingApiKeys, t, () => form.proxyUrl)

  const keyModelsStatus = ref<Map<string, KeyModelsStatus>>(new Map())

  let rowId = 0
  const nextRowId = () => ++rowId
  const dialogRef = ref<HTMLElement | null>(null)
  const {
    activeSection,
    sections,
    scrollToSection,
    setSectionRef,
    bindScrollRoot,
    unbindScrollRoot,
  } = useChannelEditSectionNav(t, dialogRef)
  const {
    headerRows,
    newHeader,
    headerRowsFromChannel,
    addHeaderRow,
    removeHeaderRow,
    updateHeaderRow,
    getHeadersAsObject,
  } = useChannelCustomHeaders({ nextRowId })

  const form = reactive({
    name: '',
    description: '',
    serviceType: '' as 'openai' | 'claude' | 'gemini' | 'responses' | 'copilot' | '',
    authHeader: 'auto' as 'auto' | 'bearer' | 'x-api-key' | '',
    baseUrl: '',
    baseUrlsText: '',
    website: '',
    proxyUrl: '',
    requestTimeoutMs: '' as string | number,
    responseHeaderTimeoutMs: '' as string | number,
    streamFirstContentTimeoutEnabled: false,
    streamFirstContentTimeoutMs: defaultStreamTimeouts.firstContentMs,
    streamInactivityTimeoutEnabled: false,
    streamInactivityTimeoutMs: defaultStreamTimeouts.inactivityMs,
    streamToolCallIdleTimeoutEnabled: false,
    streamToolCallIdleTimeoutMs: defaultStreamTimeouts.toolCallIdleMs,
    rateLimitRpm: '' as string | number,
    rateLimitWindowMinutes: '' as string | number,
    rateLimitMaxConcurrent: '' as string | number,
    rateLimitAutoFromHeaders: true,
    routePrefix: '',
    insecureSkipVerify: false,
    apiKeysText: '',
    customHeadersText: '{}',
    modelMappingText: '{}',
    modelCapabilitiesText: '',
    modelCapabilityRows: [] as ModelCapabilityRow[],
    defaultContextWindowTokens: '' as string | number,
    defaultMaxOutputTokens: '' as string | number,
    allowUnknownContext: false,
    reasoningMappingText: '{}',
    reasoningParamStyle: 'reasoning' as 'reasoning' | 'reasoning_effort' | 'thinking',
    textVerbosity: '' as 'low' | 'medium' | 'high' | '',
    supportedModelsText: '',
    visionFallbackModel: '',
    visionFallbackReasoningEffort: '' as ReasoningEffort | '',
    noVision: false,
    historicalImageTurnLimit: 0,
    passbackReasoningContent: false,
    passbackThinkingBlocks: false,
    fastMode: false,
    lowQuality: false,
    injectDummyThoughtSignature: false,
    stripThoughtSignature: false,
    stripEmptyTextBlocks: false,
    normalizeSystemRoleToTopLevel: false,
    normalizeMetadataUserId: defaultNormalizeMetadataUserId(),
    stripBillingHeader: false,
    normalizeNonstandardChatRoles: false,
    autoBlacklistBalance: true,
    codexNativeToolPassthrough: false,
    codexToolCompat: false,
    stripCodexClientTools: false,
    stripImageGenerationTool: false,
  })

  const supportsOpenAIAdvanced = computed(() => props.channelType !== 'vectors' && supportsAdvancedChannelOptions(form.serviceType))
  const supportsOpenAIAdvancedOptions = computed(() => props.channelType !== 'vectors' && supportsAdvancedChannelOptions(form.serviceType))
  const supportsReasoningMappingOptions = computed(() => props.channelType !== 'vectors' && supportsReasoningMapping(form.serviceType))
  const {
    modelMappingRows,
    modelCapabilityRows,
    mappedTargetModels,
    newModelMapping,
    modelMappingFromChannel,
    addModelMappingRow,
    removeModelMappingRow,
    getModelMappingAsObject,
    getReasoningMappingAsObject,
    applyVisionFallbackReasoning,
    getNoVisionModelsFromRows,
    updateMappingRow,
    updateModelCapabilityRows,
    syncModelCapabilitiesFromMapping,
    startMappingTargetEdit,
    finishMappingTargetEdit,
  } = useChannelModelMapping({
    form,
    getSourceMappingError: () => sourceMappingError.value,
    nextRowId,
    supportsReasoningMappingOptions,
  })
  const {
    showModelMappingPresets,
    showMessagesOpenAIChannelPresets,
    showClaudeChannelPresets,
    showCodexResponsesPresets,
    applyPreset,
  } = useChannelEditPresets({
    channelType: () => props.channelType,
    form,
    modelMappingRows,
    nextRowId,
    supportsOpenAIAdvanced,
  })
  const {
    fetchingModels,
    fetchedModelsError,
    sourceModelOptions,
    targetModelDatalist,
    commonSupportedModelFilters,
    normalizedSupportedModelState,
    supportedModelsError,
    selectedSupportedModelSet,
    sourceMappingError,
    resetTargetModelState,
    toggleSupportedModelFilter,
    fetchTargetModels,
    handleTargetFocus: loadTargetModelsOnFocus,
  } = useChannelTargetModels({
    channel: () => props.channel,
    channelType: () => props.channelType,
    defaultServiceTypeForChannel,
    form,
    getHeadersAsObject,
    getSubmitApiKeys,
    keyModelsStatus,
    modelMappingRows,
    newModelMapping,
    t,
  })

  const quickDetection = computed(() => parseQuickInput(quickInput.value, form.serviceType || undefined))
  const detectedBaseUrls = computed(() => {
    if (form.serviceType === 'copilot' && quickDetection.value.detectedBaseUrls.length === 0) {
      return [copilotDefaultBaseUrl]
    }
    return quickDetection.value.detectedBaseUrls
  })
  const detectedApiKeys = computed(() => quickDetection.value.detectedApiKeys)
  const detectedServiceType = computed(() => quickDetection.value.detectedServiceType)
  const {
    reasoningParamStyleOptions,
    textVerbosityOptions,
    DEFAULT_SELECT_VALUE,
    reasoningEffortOptions,
    serviceTypeOptions,
    headerServiceTypeItems,
    supportsChatRoleNormalization,
    modelMappingHint,
    targetModelPlaceholder,
  } = useChannelEditorOptions({
    channelType: () => props.channelType,
    defaultServiceTypeForChannel,
    detectedServiceType,
    form,
    quickServiceTypeTouched,
    t,
  })

  // 生成随机字符串（用于渠道名称后缀）
  function generateRandomString(length: number): string {
    const chars = 'abcdefghijklmnopqrstuvwxyz0123456789'
    let result = ''
    for (let i = 0; i < length; i++) {
      result += chars.charAt(Math.floor(Math.random() * chars.length))
    }
    return result
  }

  const randomSuffix = ref(generateRandomString(6))

  // 自动生成的渠道名称
  const generatedChannelName = computed(() => {
    const firstUrl = detectedBaseUrls.value[0]
    if (!firstUrl) return `channel-${randomSuffix.value}`
    const prefix = extractChannelNamePrefix(firstUrl)
    return `${prefix}-${randomSuffix.value}`
  })

  watch(detectedServiceType, (serviceType) => {
    if (props.channelType === 'images' || props.channelType === 'vectors') {
      form.serviceType = 'openai'
      return
    }
    if (isEditMode.value || quickServiceTypeTouched.value || !serviceType) return
    form.serviceType = serviceType
  })

  function resetForm() {
    clearDuplicateKeyHighlight()
    randomSuffix.value = generateRandomString(6)
    form.name = ''
    form.description = ''
    form.serviceType = defaultServiceTypeForChannel()
    form.authHeader = 'auto'
    form.baseUrl = ''
    form.baseUrlsText = ''
    form.website = ''
    form.proxyUrl = ''
    form.requestTimeoutMs = ''
    form.responseHeaderTimeoutMs = ''
    form.streamFirstContentTimeoutEnabled = false
    form.streamFirstContentTimeoutMs = defaultStreamTimeouts.firstContentMs
    form.streamInactivityTimeoutEnabled = false
    form.streamInactivityTimeoutMs = defaultStreamTimeouts.inactivityMs
    form.streamToolCallIdleTimeoutEnabled = false
    form.streamToolCallIdleTimeoutMs = defaultStreamTimeouts.toolCallIdleMs
    form.rateLimitRpm = ''
    form.rateLimitWindowMinutes = ''
    form.rateLimitMaxConcurrent = ''
    form.rateLimitAutoFromHeaders = true
    form.routePrefix = ''
    form.insecureSkipVerify = false
    form.apiKeysText = ''
    form.customHeadersText = '{}'
    form.modelMappingText = '{}'
    form.modelCapabilitiesText = ''
    form.modelCapabilityRows = []
    form.defaultContextWindowTokens = ''
    form.defaultMaxOutputTokens = ''
    form.allowUnknownContext = false
    form.reasoningMappingText = '{}'
    form.reasoningParamStyle = 'reasoning'
    form.textVerbosity = ''
    form.supportedModelsText = ''
    form.visionFallbackModel = ''
    form.visionFallbackReasoningEffort = ''
    form.noVision = false
    form.historicalImageTurnLimit = 0
    form.passbackReasoningContent = false
    form.passbackThinkingBlocks = false
    form.fastMode = false
    form.lowQuality = false
    form.injectDummyThoughtSignature = false
    form.stripThoughtSignature = false
    form.stripEmptyTextBlocks = false
    form.normalizeSystemRoleToTopLevel = false
    form.normalizeMetadataUserId = defaultNormalizeMetadataUserId()
    form.stripBillingHeader = false
    form.normalizeNonstandardChatRoles = false
    form.autoBlacklistBalance = true
    form.codexNativeToolPassthrough = false
    form.codexToolCompat = false
    form.stripCodexClientTools = false
    form.stripImageGenerationTool = false
    quickInput.value = ''
    quickServiceTypeTouched.value = false
    existingApiKeys.value = []
    newApiKeysText.value = ''
    copiedKeyIndex.value = null
    keyModelsStatus.value.clear()
    resetTargetModelState()
    localRestoredKeys.value = new Set()
    modelMappingRows.value = []
    modelCapabilityRows.value = []
    headerRows.value = []
    error.value = ''
    success.value = ''
  }

  function defaultServiceTypeForChannel() {
    if (props.channelType === 'gemini') return 'gemini'
    if (props.channelType === 'responses') return 'responses'
    if (props.channelType === 'messages') return 'claude'
    return 'openai'
  }

  function populateFromChannel(ch: Channel) {
    form.name = ch.name || ''
    form.description = ch.description || ''
    form.serviceType = ch.serviceType || defaultServiceTypeForChannel()
    form.authHeader = ch.authHeader || 'auto'
    // baseUrls 多 URL 时已包含主 URL；否则回退单个 baseUrl。form.baseUrl 由 watch 派生
    form.baseUrlsText = (ch.baseUrls?.length ? ch.baseUrls : [ch.baseUrl].filter(Boolean)).join('\n')
    form.website = ch.website || ''
    form.proxyUrl = ch.proxyUrl || ''
    form.requestTimeoutMs = ch.requestTimeoutMs || ''
    form.responseHeaderTimeoutMs = ch.responseHeaderTimeoutMs || ''
    form.streamFirstContentTimeoutEnabled = !!(ch.streamFirstContentTimeoutMs && ch.streamFirstContentTimeoutMs > 0)
    form.streamFirstContentTimeoutMs = ch.streamFirstContentTimeoutMs && ch.streamFirstContentTimeoutMs > 0 ? ch.streamFirstContentTimeoutMs : defaultStreamTimeouts.firstContentMs
    form.streamInactivityTimeoutEnabled = !!(ch.streamInactivityTimeoutMs && ch.streamInactivityTimeoutMs > 0)
    form.streamInactivityTimeoutMs = ch.streamInactivityTimeoutMs && ch.streamInactivityTimeoutMs > 0 ? ch.streamInactivityTimeoutMs : defaultStreamTimeouts.inactivityMs
    form.streamToolCallIdleTimeoutEnabled = !!(ch.streamToolCallIdleTimeoutMs && ch.streamToolCallIdleTimeoutMs > 0)
    form.streamToolCallIdleTimeoutMs = ch.streamToolCallIdleTimeoutMs && ch.streamToolCallIdleTimeoutMs > 0 ? ch.streamToolCallIdleTimeoutMs : defaultStreamTimeouts.toolCallIdleMs
    form.rateLimitRpm = (ch.rateLimitRpm && ch.rateLimitRpm > 0) ? ch.rateLimitRpm : ''
    form.rateLimitWindowMinutes = (ch.rateLimitWindowMinutes && ch.rateLimitWindowMinutes > 0) ? ch.rateLimitWindowMinutes : ''
    form.rateLimitMaxConcurrent = (ch.rateLimitMaxConcurrent && ch.rateLimitMaxConcurrent > 0) ? ch.rateLimitMaxConcurrent : ''
    form.rateLimitAutoFromHeaders = !!ch.rateLimitAutoFromHeaders
    form.routePrefix = ch.routePrefix || ''
    form.insecureSkipVerify = ch.insecureSkipVerify ?? false
    existingApiKeys.value = [...(ch.apiKeys || [])]
    form.apiKeysText = ''
    newApiKeysText.value = ''
    copiedKeyIndex.value = null
    keyModelsStatus.value.clear()
    resetTargetModelState()
    localRestoredKeys.value = new Set()
    modelMappingRows.value = modelMappingFromChannel(ch)
    modelCapabilityRows.value = modelCapabilitiesToRows(ch.modelCapabilities || {}, () => ++rowId)
    form.modelCapabilityRows = modelCapabilityRows.value
    headerRows.value = headerRowsFromChannel(ch)
    form.customHeadersText = stringifyJson(ch.customHeaders)
    form.modelMappingText = stringifyJson(ch.modelMapping)
    form.modelCapabilitiesText = stringifyJson(ch.modelCapabilities)
    form.defaultContextWindowTokens = ch.defaultCapability?.contextWindowTokens ?? ''
    form.defaultMaxOutputTokens = ch.defaultCapability?.maxOutputTokens ?? ''
    form.allowUnknownContext = ch.allowUnknownContext ?? false
    form.reasoningMappingText = stringifyJson(ch.reasoningMapping)
    form.reasoningParamStyle = ch.reasoningParamStyle || 'reasoning'
    form.textVerbosity = ch.textVerbosity || ''
    form.supportedModelsText = (ch.supportedModels || []).join('\n')
    // noVisionModels 中命中映射 target 的由行级 toggle 表示，其余保留在文本框，避免重复展示
    form.visionFallbackModel = ch.visionFallbackModel || ''
    form.visionFallbackReasoningEffort = (ch.reasoningMapping?.[form.visionFallbackModel] || '') as ReasoningEffort | ''
    form.noVision = ch.noVision ?? false
    form.historicalImageTurnLimit = ch.historicalImageTurnLimit ?? 0
    form.passbackReasoningContent = ch.passbackReasoningContent ?? false
    form.passbackThinkingBlocks = ch.passbackThinkingBlocks ?? false
    form.fastMode = ch.fastMode ?? false
    form.lowQuality = ch.lowQuality ?? false
    form.injectDummyThoughtSignature = ch.injectDummyThoughtSignature ?? false
    form.stripThoughtSignature = ch.stripThoughtSignature ?? false
    form.stripEmptyTextBlocks = ch.stripEmptyTextBlocks ?? false
    form.normalizeSystemRoleToTopLevel = ch.normalizeSystemRoleToTopLevel ?? false
    form.normalizeMetadataUserId = ch.normalizeMetadataUserId ?? true
    form.stripBillingHeader = ch.stripBillingHeader ?? false
    form.normalizeNonstandardChatRoles = ch.normalizeNonstandardChatRoles ?? false
    form.autoBlacklistBalance = ch.autoBlacklistBalance ?? true
    form.codexNativeToolPassthrough = ch.codexNativeToolPassthrough ?? false
    form.codexToolCompat = ch.codexToolCompat ?? ch.stripCodexClientTools ?? false
    form.stripCodexClientTools = ch.stripCodexClientTools ?? ch.codexToolCompat ?? false
    form.stripImageGenerationTool = ch.stripImageGenerationTool ?? false
  }

  watch(() => props.channel, (ch) => {
    resetForm()
    if (ch) {
      populateFromChannel(ch)
      syncModelCapabilitiesFromMapping()
      // 如果有模型映射配置，主动触发一次模型列表获取
      // 使用 nextTick 确保表单数据已填充完成
      if (ch.modelMapping && Object.keys(ch.modelMapping).length > 0) {
        nextTick(() => {
          void fetchTargetModelsAndShowDropdown()
        })
      }
    }
  }, { immediate: true })

  // baseUrlsText 是唯一的 Base URL 输入（每行一个，第一行为主），派生 form.baseUrl / form.baseUrls（对齐 WebUI）
  watch([() => form.baseUrlsText, () => form.serviceType], () => {
    const { baseUrl } = syncBaseUrlsFormState(form.baseUrlsText, form.serviceType)
    form.baseUrl = baseUrl
  }, { immediate: true })

  // In create mode, textarea quick input is the submit source; paste handling is only an enhancement.
  const submitBaseUrls = computed(() => {
    const formBaseUrls = parseLines(form.baseUrlsText)
    if (isEditMode.value) return formBaseUrls
    return detectedBaseUrls.value.length > 0 ? detectedBaseUrls.value : formBaseUrls
  })
  const submitBaseUrl = computed(() => submitBaseUrls.value[0] || form.baseUrl)
  const submitApiKeys = computed(() => {
    const apiKeys = getSubmitApiKeys()
    if (isEditMode.value) return apiKeys
    return [...new Set([...apiKeys, ...detectedApiKeys.value])]
  })

  // API Key is required from existing/new keys; create mode also includes quick input, and edit mode counts restorable disabled keys.
  const hasConfigurableKeys = computed(() => {
    if (submitApiKeys.value.length > 0) return true
    if (isEditMode.value && visibleDisabledKeys.value.length > 0) return true
    return false
  })

  const errors = computed(() => {
    const errs: Record<string, string> = {}
    if (isEditMode.value && !form.name.trim()) errs.name = t('channelEditor.basic.name.required')
    if (!isEditMode.value && !generatedChannelName.value.trim()) errs.name = t('channelEditor.basic.name.required')
    if (!form.serviceType) errs.serviceType = t('channelEditor.basic.serviceType.required')
    if (form.serviceType !== 'copilot' && submitBaseUrls.value.length === 0) errs.baseUrl = t('channelEditor.basic.baseUrl.required')
    // copilot 渠道通过 OAuth 登录，apiKeys 由登录流程填充，此处豁免必填校验
    if (!hasConfigurableKeys.value && form.serviceType !== 'copilot') errs.apiKeys = t('channelEditor.auth.apiKeyRequired')
    if (String(form.requestTimeoutMs).trim()) {
      const timeout = Number(form.requestTimeoutMs)
      if (!Number.isInteger(timeout) || timeout < 1000 || timeout > 300000) {
        errs.requestTimeoutMs = t('channelEditor.transport.requestTimeout.invalid')
      }
    }
    if (String(form.responseHeaderTimeoutMs).trim()) {
      const timeout = Number(form.responseHeaderTimeoutMs)
      if (!Number.isInteger(timeout) || timeout < 1000 || timeout > 300000) {
        errs.responseHeaderTimeoutMs = t('channelEditor.transport.responseHeaderTimeout.invalid')
      }
    }
    if (modelCapabilityRowsToRecord(modelCapabilityRows.value) === null) {
      errs.modelCapabilitiesText = t('addChannel.modelCapabilitiesRowsInvalid')
    }
    return errs
  })

  const isValid = computed(() => Object.keys(errors.value).length === 0)

  function stringifyJson(value?: Record<string, unknown>) {
    if (!value || Object.keys(value).length === 0) return '{}'
    return JSON.stringify(value, null, 2)
  }

  function parseJsonObject<T extends Record<string, unknown>>(text: string, label: string): T {
    const trimmed = text.trim()
    if (!trimmed) return {} as T
    const parsed = JSON.parse(trimmed)
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) {
      throw new Error(`${label} must be a JSON object`)
    }
    return parsed as T
  }

  function parseLines(text: string) {
    return text
      .split('\n')
      .map(s => s.trim())
      .filter(Boolean)
  }

  function handleQuickPaste(text: string) {
    const result = parseQuickInput(text, form.serviceType || undefined)
    // 统一写入 baseUrlsText（每行一个，第一行为主），form.baseUrl 由 watch 派生
    const detectedUrls = result.detectedBaseUrls.length ? result.detectedBaseUrls : [result.detectedBaseUrl].filter(Boolean)
    if (detectedUrls.length) form.baseUrlsText = detectedUrls.join('\n')
    if (result.detectedApiKeys.length) {
      existingApiKeys.value = [...new Set([...existingApiKeys.value, ...result.detectedApiKeys])]
    }
    if (result.detectedServiceType && !quickServiceTypeTouched.value) form.serviceType = result.detectedServiceType
    if (!form.serviceType) form.serviceType = defaultServiceTypeForChannel()
    applyQuickCopilotDefaults()
  }

  function updateQuickServiceType(value: string) {
    form.serviceType = value as typeof form.serviceType
    quickServiceTypeTouched.value = true
    applyQuickCopilotDefaults()
  }

  function applyQuickCopilotDefaults() {
    if (isEditMode.value || form.serviceType !== 'copilot' || form.baseUrlsText.trim()) return
    form.baseUrlsText = copilotDefaultBaseUrl
  }

  function buildSubmitPayload() {
    const payload = isEditMode.value
      ? buildCurrentPayload()
      : buildChannelPayload({
          name: generatedChannelName.value,
          serviceType: form.serviceType,
          authHeader: form.authHeader,
          baseUrl: submitBaseUrl.value,
          baseUrls: submitBaseUrls.value,
          website: form.website,
          insecureSkipVerify: form.insecureSkipVerify,
          lowQuality: form.lowQuality,
          injectDummyThoughtSignature: form.injectDummyThoughtSignature,
          stripThoughtSignature: form.stripThoughtSignature,
          passbackReasoningContent: form.passbackReasoningContent,
          passbackThinkingBlocks: form.passbackThinkingBlocks,
          description: form.description,
          apiKeys: submitApiKeys.value,
          modelMapping: parseJsonObject<Record<string, string>>(form.modelMappingText, 'Model mapping'),
          modelCapabilityRows: modelCapabilityRows.value,
          reasoningMapping: parseJsonObject<Record<string, 'none' | 'low' | 'medium' | 'high' | 'xhigh' | 'max'>>(form.reasoningMappingText, 'Reasoning mapping'),
          reasoningParamStyle: form.reasoningParamStyle,
          textVerbosity: form.textVerbosity,
          fastMode: form.fastMode,
          customHeaders: parseJsonObject<Record<string, string>>(form.customHeadersText, 'Custom headers'),
          proxyUrl: form.proxyUrl,
          requestTimeoutMs: form.requestTimeoutMs,
          responseHeaderTimeoutMs: form.responseHeaderTimeoutMs,
          streamFirstContentTimeoutMs: form.streamFirstContentTimeoutEnabled ? form.streamFirstContentTimeoutMs : undefined,
          streamInactivityTimeoutMs: form.streamInactivityTimeoutEnabled ? form.streamInactivityTimeoutMs : undefined,
          streamToolCallIdleTimeoutMs: form.streamToolCallIdleTimeoutEnabled ? form.streamToolCallIdleTimeoutMs : undefined,
          rateLimitWindowMinutes: form.rateLimitWindowMinutes,
          routePrefix: form.routePrefix,
          supportedModels: normalizedSupportedModelState.value.validPatterns,
          autoBlacklistBalance: form.autoBlacklistBalance,
          normalizeMetadataUserId: form.normalizeMetadataUserId,
          stripBillingHeader: form.stripBillingHeader,
          stripEmptyTextBlocks: form.stripEmptyTextBlocks,
          normalizeSystemRoleToTopLevel: form.normalizeSystemRoleToTopLevel,
          codexNativeToolPassthrough: form.codexNativeToolPassthrough,
          codexToolCompat: form.codexToolCompat,
          normalizeNonstandardChatRoles: form.normalizeNonstandardChatRoles,
          stripCodexClientTools: form.stripCodexClientTools,
          stripImageGenerationTool: form.stripImageGenerationTool,
          noVision: form.noVision,
          noVisionModels: getNoVisionModelsFromRows(),
          visionFallbackModel: form.visionFallbackModel,
          historicalImageTurnLimit: form.historicalImageTurnLimit,
        }, { channelType: props.channelType })

    applyVisionFallbackReasoning(payload)

    if (isEditMode.value && props.channel?.requestTimeoutMs && !String(form.requestTimeoutMs ?? '').trim()) {
      payload.requestTimeoutMs = 0
    }
    if (isEditMode.value && props.channel?.responseHeaderTimeoutMs && !String(form.responseHeaderTimeoutMs ?? '').trim()) {
      payload.responseHeaderTimeoutMs = 0
    }
    if (isEditMode.value && props.channel?.streamFirstContentTimeoutMs && !form.streamFirstContentTimeoutEnabled) {
      payload.streamFirstContentTimeoutMs = 0
    }
    if (isEditMode.value && props.channel?.streamInactivityTimeoutMs && !form.streamInactivityTimeoutEnabled) {
      payload.streamInactivityTimeoutMs = 0
    }
    if (isEditMode.value && props.channel?.streamToolCallIdleTimeoutMs && !form.streamToolCallIdleTimeoutEnabled) {
      payload.streamToolCallIdleTimeoutMs = 0
    }
    if (isEditMode.value && props.channel?.rateLimitRpm && !payload.rateLimitRpm) {
      payload.rateLimitRpm = 0
    }
    if (isEditMode.value && props.channel?.rateLimitMaxConcurrent && !payload.rateLimitMaxConcurrent) {
      payload.rateLimitMaxConcurrent = 0
    }

    return payload
  }

  async function persistCurrentDraft(options: { notifyParent?: boolean; close?: boolean } = {}) {
    syncModelCapabilitiesFromMapping()
    applyQuickCopilotDefaults()

    if (!isValid.value) {
      error.value = Object.values(errors.value)[0] || ''
      return false
    }

    saving.value = true
    error.value = ''
    success.value = ''
    try {
      await saveChannel(buildSubmitPayload(), props.channel?.index ?? null, {
        isQuickAdd: !isEditMode.value,
      }, props.channelType)
      if (options.notifyParent) emit('saved')
      if (options.close) emit('close')
      return true
    } catch (e) {
      error.value = e instanceof Error ? e.message : String(e)
      return false
    } finally {
      saving.value = false
    }
  }

  async function handleSubmit() {
    await persistCurrentDraft({ notifyParent: true, close: true })
  }

  // Keyboard shortcuts: Esc 取消，Cmd/Ctrl+Enter 保存（编辑/创建一致，避免多行文本内 Enter 误触发）
  const handleGlobalKeydown = (e: KeyboardEvent) => {
    if (e.key === 'Escape') {
      if (showTargetSuggestions.value) {
        e.preventDefault()
        e.stopPropagation()
        hideTargetDropdown()
        return
      }
      e.preventDefault()
      emit('close')
      return
    }

    if (e.key !== 'Enter') return
    if (saving.value) return

    // 统一 Cmd/Ctrl+Enter 保存（textarea 内也生效），普通 Enter 保留换行/原生行为
    if ((e.metaKey || e.ctrlKey) && !e.shiftKey) {
      e.preventDefault()
      void handleSubmit()
    }
  }

  // 组件挂载即注册快捷键（新建和编辑模式都需要）
  onMounted(() => {
    window.addEventListener('keydown', handleGlobalKeydown)
    window.addEventListener('pointerdown', handlePointerDown)

    // 按滚动位置同步左侧导航高亮；长 section 内滚动也需要实时更新
    // 使用多次 nextTick + setTimeout 确保 Teleport + reka-ui 完全渲染
    nextTick(() => {
      nextTick(() => {
        setTimeout(() => {
          bindScrollRoot()
        }, 200)
      })
    })
  })

  onBeforeUnmount(() => {
    window.removeEventListener('keydown', handleGlobalKeydown)
    window.removeEventListener('pointerdown', handlePointerDown)
    unbindScrollRoot()
    clearDuplicateKeyHighlight()
    clearCopilotPollTimer()
    clearCopilotCopyTimer()
    if (diagnoseTimer) clearTimeout(diagnoseTimer)
  })

  const {
    showTargetSuggestions,
    activeTargetInputId,
    filteredTargetModels,
    showSourceSuggestions,
    activeSourceInputId,
    filteredSourceModels,
    showTargetDropdown,
    hideTargetDropdown,
    handlePointerDown,
    showSourceDropdown,
    hideSourceDropdown,
    selectSourceModel,
    selectTargetModel,
  } = useModelAutocomplete({
    finishMappingTargetEdit,
    modelMappingRows,
    newModelMapping,
    sourceModelOptions,
    targetModelDatalist,
  })

  // ── Base URL 预期请求预览 ──
  const expectedRequestUrls = computed(() => {
    return buildExpectedRequestUrls(
      props.channelType as any,
      form.serviceType as any,
      form.baseUrl,
      parseLines(form.baseUrlsText),
    )
  })

  // 快速添加模式：基于 detectedBaseUrls 计算预期请求预览
  const quickExpectedRequestUrls = computed(() => {
    return buildExpectedRequestUrls(
      props.channelType as any,
      (form.serviceType || detectedServiceType.value) as any,
      detectedBaseUrls.value[0] || '',
      detectedBaseUrls.value,
    )
  })

  async function fetchTargetModelsAndShowDropdown() {
    await fetchTargetModels()
    showTargetSuggestions.value = !!activeTargetInputId.value && targetModelDatalist.value.length > 0
  }

  function handleTargetFocus() {
    loadTargetModelsOnFocus()
  }

  function syncUpstreamModels() {
    void fetchTargetModelsAndShowDropdown()
  }

  // Refresh dropdown visibility after target models load asynchronously while the input is still focused.
  // The first focus may see an empty datalist and hide suggestions; flip it back once data arrives.
  watch(targetModelDatalist, (list) => {
    if (activeTargetInputId.value && list.length > 0 && !showTargetSuggestions.value) {
      showTargetSuggestions.value = true
    }
  })

  // ── 编辑头部动作：noVision toggle + Test Capability ──

  async function handleTestCapability() {
    if (!props.channel) return

    // 父组件收到 test-capability 后负责关闭编辑弹窗并刷新；这里不能先 emit saved，避免组件卸载后丢失事件。
    const saved = await persistCurrentDraft()
    if (!saved) return

    emit('test-capability', {
      ...props.channel,
      name: form.name || props.channel.name,
      index: props.channel.index,
    })
  }

  async function handleDiagnoseCompat() {
    if (!props.channel || props.channelType === 'images' || props.channelType === 'vectors') return
    diagnosingCompat.value = true
    if (diagnoseTimer) { clearTimeout(diagnoseTimer); diagnoseTimer = null }
    diagnoseResult.value = null
    try {
      const result = await adminApi.post<CompatDiagnoseResult>(`/api/${props.channelType}/channels/${props.channel.index}/compat-diagnose`, {})
      const applied: string[] = []
      for (const [key, val] of Object.entries(result.recommendations)) {
        if (val !== undefined && (form as Record<string, unknown>)[key] !== val) {
          Object.assign(form, { [key]: val })
          applied.push(key)
        }
      }
      if (result.urlRecommendations?.recommended) {
        const current = result.urlRecommendations.current
        const recommended = result.urlRecommendations.recommended
        const lines = form.baseUrlsText.split('\n').map(line => line.trim()).filter(Boolean)
        const nextLines = lines.length > 0
          ? lines.map((line, index) => (index === 0 || line === current) ? recommended : line)
          : [recommended]
        form.baseUrlsText = Array.from(new Set(nextLines)).join('\n')
        applied.push('baseUrl')
      }
      const message = applied.length
        ? t('channelEditor.compat.diagnoseApplied', { count: String(applied.length) })
        : t('channelEditor.compat.diagnoseNoChange')
      diagnoseResult.value = { type: 'success', message, appliedCount: applied.length }
    } catch (e) {
      diagnoseResult.value = { type: 'error', message: e instanceof Error ? e.message : t('channelEditor.compat.diagnoseFailed'), appliedCount: 0 }
    } finally {
      diagnosingCompat.value = false
      diagnoseTimer = setTimeout(() => { diagnoseResult.value = null }, 5000)
    }
  }

  function buildCurrentPayload() {
    const modelMapping = getModelMappingAsObject()
    const reasoningMapping = getReasoningMappingAsObject() as Record<string, 'none' | 'low' | 'medium' | 'high' | 'xhigh' | 'max'>

    return buildChannelPayload({
      name: form.name,
      serviceType: form.serviceType,
      authHeader: form.authHeader,
      baseUrl: form.baseUrl,
      baseUrls: parseLines(form.baseUrlsText),
      website: form.website,
      insecureSkipVerify: form.insecureSkipVerify,
      lowQuality: form.lowQuality,
      injectDummyThoughtSignature: form.injectDummyThoughtSignature,
      stripThoughtSignature: form.stripThoughtSignature,
      passbackReasoningContent: form.passbackReasoningContent,
      passbackThinkingBlocks: form.passbackThinkingBlocks,
      description: form.description,
      apiKeys: getSubmitApiKeys(),
      modelMapping,
      modelCapabilityRows: modelCapabilityRows.value,
      reasoningMapping,
      reasoningParamStyle: form.reasoningParamStyle,
      textVerbosity: form.textVerbosity,
      fastMode: form.fastMode,
      customHeaders: getHeadersAsObject(),
      proxyUrl: form.proxyUrl,
      requestTimeoutMs: form.requestTimeoutMs,
      responseHeaderTimeoutMs: form.responseHeaderTimeoutMs,
      streamFirstContentTimeoutMs: form.streamFirstContentTimeoutEnabled ? form.streamFirstContentTimeoutMs : undefined,
      streamInactivityTimeoutMs: form.streamInactivityTimeoutEnabled ? form.streamInactivityTimeoutMs : undefined,
      streamToolCallIdleTimeoutMs: form.streamToolCallIdleTimeoutEnabled ? form.streamToolCallIdleTimeoutMs : undefined,
      rateLimitRpm: form.rateLimitRpm,
      rateLimitWindowMinutes: form.rateLimitWindowMinutes,
      rateLimitMaxConcurrent: form.rateLimitMaxConcurrent,
      rateLimitAutoFromHeaders: form.rateLimitAutoFromHeaders,
      routePrefix: form.routePrefix,
      supportedModels: normalizedSupportedModelState.value.validPatterns,
      autoBlacklistBalance: form.autoBlacklistBalance,
      normalizeMetadataUserId: form.normalizeMetadataUserId,
      stripBillingHeader: form.stripBillingHeader,
      stripEmptyTextBlocks: form.stripEmptyTextBlocks,
      normalizeSystemRoleToTopLevel: form.normalizeSystemRoleToTopLevel,
      codexNativeToolPassthrough: form.codexNativeToolPassthrough,
      codexToolCompat: form.codexToolCompat,
      normalizeNonstandardChatRoles: form.normalizeNonstandardChatRoles,
      stripCodexClientTools: form.stripCodexClientTools,
      stripImageGenerationTool: form.stripImageGenerationTool,
      noVision: form.noVision,
      noVisionModels: getNoVisionModelsFromRows(),
      visionFallbackModel: form.visionFallbackModel,
      historicalImageTurnLimit: form.historicalImageTurnLimit,
    }, { channelType: props.channelType })
  }

  return {
    isEditMode,
    isMac,
    saving,
    restoringKey,
    error,
    success,
    diagnosingCompat,
    diagnoseResult,
    quickInput,
    existingApiKeys,
    newApiKeysText,
    copiedKeyIndex,
    duplicateKeyIndex,
    localRestoredKeys,
    copilotOAuthLoading,
    copilotPolling,
    copilotOAuthError,
    copilotOAuthSuccess,
    copilotUserCode,
    copilotUserCodeCopied,
    keyModelsStatus,
    activeSection,
    dialogRef,
    sections,
    modelMappingRows,
    modelCapabilityRows,
    mappedTargetModels,
    newModelMapping,
    headerRows,
    newHeader,
    showTargetSuggestions,
    activeTargetInputId,
    fetchedModelsError,
    filteredTargetModels,
    showSourceSuggestions,
    activeSourceInputId,
    filteredSourceModels,
    reasoningParamStyleOptions,
    textVerbosityOptions,
    DEFAULT_SELECT_VALUE,
    reasoningEffortOptions,
    form,
    disabledApiKeys,
    historicalApiKeys,
    detectedBaseUrls,
    detectedApiKeys,
    generatedChannelName,
    errors,
    isValid,
    serviceTypeOptions,
    headerServiceTypeItems,
    supportsOpenAIAdvancedOptions,
    supportsReasoningMappingOptions,
    supportsChatRoleNormalization,
    modelMappingHint,
    targetModelPlaceholder,
    showModelMappingPresets,
    showMessagesOpenAIChannelPresets,
    showClaudeChannelPresets,
    showCodexResponsesPresets,
    fetchingModels,
    sourceModelOptions,
    targetModelDatalist,
    commonSupportedModelFilters,
    supportedModelsError,
    selectedSupportedModelSet,
    sourceMappingError,
    expectedRequestUrls,
    quickExpectedRequestUrls,
    clearCopilotPollTimer,
    scrollToSection,
    setSectionRef,
    showTargetDropdown,
    hideTargetDropdown,
    showSourceDropdown,
    hideSourceDropdown,
    selectSourceModel,
    selectTargetModel,
    removeExistingApiKey,
    handleQuickPaste,
    updateQuickServiceType,
    clearDuplicateKeyHighlight,
    moveApiKeyToTop,
    moveApiKeyToBottom,
    addModelMappingRow,
    removeModelMappingRow,
    toggleSupportedModelFilter,
    handleTargetFocus,
    applyPreset,
    syncUpstreamModels,
    updateMappingRow,
    updateModelCapabilityRows,
    startMappingTargetEdit,
    finishMappingTargetEdit,
    addHeaderRow,
    removeHeaderRow,
    updateHeaderRow,
    copyCopilotUserCode,
    startCopilotOAuth,
    openCopilotAuthorization,
    handleSubmit,
    addNewApiKeys,
    copyApiKey,
    handleDisabledKeyRestore,
    handleTestCapability,
    handleDiagnoseCompat,
    t,
  }
}
