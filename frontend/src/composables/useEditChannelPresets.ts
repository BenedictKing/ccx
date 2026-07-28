import { computed, type ComputedRef } from 'vue'
import { ensureRuntimePresetsLoaded, useRuntimePresets } from './useRuntimePresets'

type ChannelType = 'messages' | 'chat' | 'responses' | 'gemini' | 'images' | 'vectors'
type FormLike = {
  serviceType: 'openai' | 'gemini' | 'claude' | 'responses' | 'copilot' | ''
  fastMode: boolean
  textVerbosity: 'low' | 'medium' | 'high' | ''
  normalizeSystemRoleToTopLevel: boolean
  normalizeMetadataUserId: boolean
  stripBillingHeader: boolean
  noVision: boolean
  noVisionModels: string[]
  visionFallbackModel: string
  visionFallbackReasoningEffort: 'none' | 'minimal' | 'low' | 'medium' | 'high' | 'xhigh' | 'max' | ''
  modelMapping: Record<string, string>
  reasoningMapping: Record<string, 'none' | 'minimal' | 'low' | 'medium' | 'high' | 'xhigh' | 'max'>
  reasoningParamStyle: 'reasoning' | 'reasoning_effort' | 'thinking'
  codexToolCompat: boolean
  stripCodexClientTools: boolean
  authHeader: 'auto' | 'bearer' | 'x-api-key' | ''
}

type EditChannelPresetOptions = {
  channelType: ComputedRef<ChannelType>
  form: FormLike
  supportsOpenAIAdvancedOptions: ComputedRef<boolean>
  syncModelMappingRowsFromForm: () => void
}

export function useEditChannelPresets(options: EditChannelPresetOptions) {
  void ensureRuntimePresetsLoaded()
  const { effectiveChannelPresets } = useRuntimePresets()

  const showModelMappingPresets = computed(() => {
    return options.channelType.value === 'messages'
      && (options.form.serviceType === 'openai' || options.form.serviceType === 'responses')
  })

  const showMessagesOpenAIChannelPresets = computed(() => {
    return options.channelType.value === 'messages'
      && (options.form.serviceType === 'openai' || options.form.serviceType === 'responses')
  })

  const modelMappingPresets = computed(() => effectiveChannelPresets.value.openAIMessages)

  const applyModelMappingPreset = (preset: string) => {
    const presetConfig = modelMappingPresets.value[preset]
    if (!presetConfig) return
    options.form.modelMapping = { ...presetConfig.modelMapping }
    options.form.fastMode = presetConfig.fastMode
    options.form.textVerbosity = presetConfig.textVerbosity

    if (options.supportsOpenAIAdvancedOptions.value) {
      options.form.reasoningMapping = { ...(presetConfig.reasoningMapping || {}) } as FormLike['reasoningMapping']
    } else {
      options.form.reasoningMapping = {}
    }

    options.syncModelMappingRowsFromForm()
  }

  const showClaudeChannelPresets = computed(() => {
    return options.form.serviceType === 'claude'
      && (options.channelType.value === 'messages' || options.channelType.value === 'chat' || options.channelType.value === 'responses')
  })

  const claudeChannelPresets = computed(() => effectiveChannelPresets.value.claudeMessages)

  const applyClaudeChannelPreset = (preset: string) => {
    const presetConfig = claudeChannelPresets.value[preset]
    if (!presetConfig) return
    options.form.normalizeSystemRoleToTopLevel = !!presetConfig.normalizeSystemRoleToTopLevel
    if (presetConfig.normalizeMetadataUserId) {
      options.form.normalizeMetadataUserId = true
    }
    options.form.stripBillingHeader = !!presetConfig.stripBillingHeader
    options.form.noVision = !!presetConfig.noVision
    options.form.noVisionModels = [...(presetConfig.noVisionModels || [])]
    options.form.visionFallbackModel = presetConfig.visionFallbackModel || ''
    options.form.visionFallbackReasoningEffort = ''
    options.form.modelMapping = { ...presetConfig.modelMapping }
    options.form.reasoningMapping = { ...(presetConfig.reasoningMapping || {}) } as FormLike['reasoningMapping']
    options.form.reasoningParamStyle = presetConfig.reasoningParamStyle || 'thinking'
    if (presetConfig.serviceType) {
      options.form.serviceType = presetConfig.serviceType as FormLike['serviceType']
    }
    options.form.authHeader = presetConfig.authHeader || 'auto'
    options.syncModelMappingRowsFromForm()
  }

  const showCodexResponsesChannelPresets = computed(() => {
    return options.channelType.value === 'responses'
      && options.form.serviceType !== 'claude'
      && options.supportsOpenAIAdvancedOptions.value
  })

  const codexResponsesChannelPresets = computed(() => effectiveChannelPresets.value.codexResponses)

  const applyCodexResponsesChannelPreset = (preset: string) => {
    const presetConfig = codexResponsesChannelPresets.value[preset.toLowerCase()]
    if (!presetConfig) return

    options.form.modelMapping = { ...presetConfig.modelMapping }
    options.form.reasoningMapping = { ...(presetConfig.reasoningMapping || {}) } as FormLike['reasoningMapping']
    options.form.reasoningParamStyle = presetConfig.reasoningParamStyle || 'reasoning'
    if (presetConfig.serviceType) {
      options.form.serviceType = presetConfig.serviceType as FormLike['serviceType']
    }
    options.form.codexToolCompat = !!presetConfig.codexToolCompat
    options.form.stripCodexClientTools = !!presetConfig.stripCodexClientTools
    options.form.noVision = !!presetConfig.noVision
    options.form.noVisionModels = [...(presetConfig.noVisionModels || [])]
    options.form.visionFallbackModel = presetConfig.visionFallbackModel || ''
    options.form.visionFallbackReasoningEffort = ''

    options.syncModelMappingRowsFromForm()
  }

  const applyPreset = (presetName: string) => {
    if (presetName === 'gpt-5.5' || presetName === 'gpt-5.4') {
      applyModelMappingPreset(presetName)
    } else if (options.form.serviceType === 'claude') {
      applyClaudeChannelPreset(presetName)
    } else if (options.channelType.value === 'responses') {
      applyCodexResponsesChannelPreset(presetName)
    } else {
      applyClaudeChannelPreset(presetName)
    }
  }

  return {
    showModelMappingPresets,
    showMessagesOpenAIChannelPresets,
    modelMappingPresets,
    applyModelMappingPreset,
    showClaudeChannelPresets,
    claudeChannelPresets,
    applyClaudeChannelPreset,
    showCodexResponsesChannelPresets,
    applyCodexResponsesChannelPreset,
    codexResponsesChannelPresets,
    applyPreset,
  }
}
