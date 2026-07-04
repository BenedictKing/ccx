import { computed, type ComputedRef } from 'vue'
import type { Channel } from '../services/api'
import { claudeMessagesPresets } from '../generated/claudeMessagesPresets'
import { codexResponsesPresets } from '../generated/codexResponsesPresets'
import { openaiMessagesPresets } from '../generated/openaiMessagesPresets'

type ChannelType = 'messages' | 'chat' | 'responses' | 'gemini' | 'images' | 'vectors'
type FormLike = {
  serviceType: 'openai' | 'gemini' | 'claude' | 'responses' | 'copilot' | ''
  fastMode: boolean
  textVerbosity: 'low' | 'medium' | 'high' | ''
  passbackReasoningContent: boolean
  passbackThinkingBlocks: boolean
  stripEmptyTextBlocks: boolean
  normalizeSystemRoleToTopLevel: boolean
  normalizeMetadataUserId: boolean
  stripBillingHeader: boolean
  stripImageGenerationTool: boolean
  noVision: boolean
  noVisionModels: string[]
  visionFallbackModel: string
  visionFallbackReasoningEffort: 'none' | 'low' | 'medium' | 'high' | 'xhigh' | 'max' | ''
  modelMapping: Record<string, string>
  reasoningMapping: Record<string, 'none' | 'low' | 'medium' | 'high' | 'xhigh' | 'max'>
  reasoningParamStyle: 'reasoning' | 'reasoning_effort' | 'thinking'
  codexNativeToolPassthrough: boolean
  codexToolCompat: boolean
  stripCodexClientTools: boolean
  normalizeNonstandardChatRoles: boolean
  authHeader: 'auto' | 'bearer' | 'x-api-key' | ''
}

type EditChannelPresetOptions = {
  channelType: ComputedRef<ChannelType>
  form: FormLike
  supportsOpenAIAdvancedOptions: ComputedRef<boolean>
  syncModelMappingRowsFromForm: () => void
}

export function useEditChannelPresets(options: EditChannelPresetOptions) {
  const showModelMappingPresets = computed(() => {
    return options.channelType.value === 'messages'
      && (options.form.serviceType === 'openai' || options.form.serviceType === 'responses')
  })

  const showMessagesOpenAIChannelPresets = computed(() => {
    return options.channelType.value === 'messages'
      && (options.form.serviceType === 'openai' || options.form.serviceType === 'responses')
  })

  const modelMappingPresets = openaiMessagesPresets

  const applyModelMappingPreset = (preset: keyof typeof modelMappingPresets) => {
    const presetConfig = modelMappingPresets[preset]
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

  const claudeChannelPresets = claudeMessagesPresets

  const applyClaudeChannelPreset = (preset: keyof typeof claudeChannelPresets) => {
    const presetConfig = claudeChannelPresets[preset]
    options.form.passbackReasoningContent = presetConfig.passbackReasoningContent
    options.form.passbackThinkingBlocks = presetConfig.passbackThinkingBlocks
    options.form.stripEmptyTextBlocks = presetConfig.stripEmptyTextBlocks
    options.form.normalizeSystemRoleToTopLevel = presetConfig.normalizeSystemRoleToTopLevel
    if (presetConfig.normalizeMetadataUserId) {
      options.form.normalizeMetadataUserId = true
    }
    options.form.stripBillingHeader = !!presetConfig.stripBillingHeader
    options.form.stripImageGenerationTool = presetConfig.stripImageGenerationTool
    options.form.noVision = presetConfig.noVision
    options.form.noVisionModels = [...presetConfig.noVisionModels]
    options.form.visionFallbackModel = presetConfig.visionFallbackModel
    options.form.visionFallbackReasoningEffort = ''
    options.form.modelMapping = { ...presetConfig.modelMapping }
    options.form.reasoningMapping = { ...(presetConfig.reasoningMapping || {}) } as FormLike['reasoningMapping']
    options.form.reasoningParamStyle = presetConfig.reasoningParamStyle as FormLike['reasoningParamStyle']
    if (presetConfig.serviceType) {
      options.form.serviceType = presetConfig.serviceType as FormLike['serviceType']
    }
    options.form.authHeader = presetConfig.authHeader || 'auto'
    options.syncModelMappingRowsFromForm()
  }

  const showCodexResponsesChannelPresets = computed(() => {
    return options.channelType.value === 'responses' && options.supportsOpenAIAdvancedOptions.value
  })

  const applyCodexResponsesChannelPreset = (preset: string) => {
    const presetConfig = codexResponsesPresets[preset.toLowerCase()]
    if (!presetConfig) return

    options.form.modelMapping = { ...presetConfig.modelMapping }
    options.form.reasoningMapping = { ...(presetConfig.reasoningMapping || {}) } as FormLike['reasoningMapping']
    options.form.reasoningParamStyle = presetConfig.reasoningParamStyle as FormLike['reasoningParamStyle']
    if (presetConfig.serviceType) {
      options.form.serviceType = presetConfig.serviceType as FormLike['serviceType']
    }
    options.form.codexNativeToolPassthrough = presetConfig.codexNativeToolPassthrough
    options.form.codexToolCompat = presetConfig.codexToolCompat
    options.form.stripCodexClientTools = presetConfig.stripCodexClientTools
    options.form.stripImageGenerationTool = presetConfig.stripImageGenerationTool
    options.form.normalizeNonstandardChatRoles = presetConfig.normalizeNonstandardChatRoles
    options.form.noVision = presetConfig.noVision
    options.form.noVisionModels = [...presetConfig.noVisionModels]
    options.form.visionFallbackModel = presetConfig.visionFallbackModel
    options.form.visionFallbackReasoningEffort = ''

    options.syncModelMappingRowsFromForm()
  }

  const applyPreset = (presetName: string) => {
    if (presetName === 'gpt-5.5' || presetName === 'gpt-5.4') {
      applyModelMappingPreset(presetName)
    } else if (options.form.serviceType === 'claude') {
      applyClaudeChannelPreset(presetName as keyof typeof claudeChannelPresets)
    } else if (options.channelType.value === 'responses') {
      applyCodexResponsesChannelPreset(presetName)
    } else {
      applyClaudeChannelPreset(presetName as keyof typeof claudeChannelPresets)
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
    applyPreset,
  }
}
