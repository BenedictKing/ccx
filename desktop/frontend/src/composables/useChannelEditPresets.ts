import { computed, type ComputedRef, type Ref } from 'vue'
import { claudeMessagesPresets } from '@/generated/claude-messages-presets'
import { codexResponsesPresets } from '@/generated/codex-responses-presets'
import type { ManagedChannelType } from '@/utils/channel-type-api'
import type { ModelMappingRow } from '@/composables/useChannelModelMapping'

type FormLike = {
  codexNativeToolPassthrough: boolean
  codexToolCompat: boolean
  fastMode: boolean
  noVision: boolean
  normalizeNonstandardChatRoles: boolean
  normalizeMetadataUserId: boolean
  normalizeSystemRoleToTopLevel: boolean
  passbackReasoningContent: boolean
  passbackThinkingBlocks: boolean
  reasoningParamStyle: 'reasoning' | 'reasoning_effort' | 'thinking'
  serviceType: 'openai' | 'claude' | 'gemini' | 'responses' | 'copilot' | ''
  stripBillingHeader: boolean
  stripCodexClientTools: boolean
  stripEmptyTextBlocks: boolean
  stripImageGenerationTool: boolean
  textVerbosity: 'low' | 'medium' | 'high' | ''
  visionFallbackModel: string
  visionFallbackReasoningEffort: ModelMappingRow['reasoning']
  authHeader: 'auto' | 'bearer' | 'x-api-key' | ''
}

type ChannelEditPresetOptions = {
  channelType: () => ManagedChannelType
  form: FormLike
  modelMappingRows: Ref<ModelMappingRow[]>
  nextRowId: () => number
  supportsOpenAIAdvanced: ComputedRef<boolean>
}

export function useChannelEditPresets(options: ChannelEditPresetOptions) {
  const claudeChannelPresets = claudeMessagesPresets

  const showModelMappingPresets = computed(() => (
    options.channelType() === 'messages' && options.supportsOpenAIAdvanced.value
  ))
  const showMessagesOpenAIChannelPresets = computed(() => (
    options.channelType() === 'messages' && options.supportsOpenAIAdvanced.value
  ))
  const showClaudeChannelPresets = computed(() => (
    options.form.serviceType === 'claude'
      && ['messages', 'chat', 'responses'].includes(options.channelType())
  ))
  const showCodexResponsesPresets = computed(() => (
    options.channelType() === 'responses' && options.supportsOpenAIAdvanced.value
  ))

  function applyClaudePreset(name: string) {
    const preset = claudeChannelPresets[name.toLowerCase()]
    if (!preset) return
    const noVisionSet = new Set(preset.noVisionModels)
    options.modelMappingRows.value = Object.entries(preset.modelMapping).map(([source, target]) => ({
      id: options.nextRowId(),
      source,
      target,
      reasoning: preset.reasoningMapping[source] || '',
      noVision: noVisionSet.has(target),
    }))
    options.form.reasoningParamStyle = preset.reasoningParamStyle as typeof options.form.reasoningParamStyle
    if (preset.serviceType) {
      options.form.serviceType = preset.serviceType as typeof options.form.serviceType
    }
    options.form.passbackReasoningContent = preset.passbackReasoningContent
    options.form.passbackThinkingBlocks = preset.passbackThinkingBlocks
    options.form.stripEmptyTextBlocks = preset.stripEmptyTextBlocks
    options.form.normalizeSystemRoleToTopLevel = preset.normalizeSystemRoleToTopLevel
    if (preset.normalizeMetadataUserId) {
      options.form.normalizeMetadataUserId = true
    }
    options.form.stripBillingHeader = !!preset.stripBillingHeader
    options.form.stripImageGenerationTool = preset.stripImageGenerationTool
    options.form.noVision = preset.noVision
    options.form.visionFallbackModel = preset.visionFallbackModel
    options.form.visionFallbackReasoningEffort = ''
    options.form.authHeader = preset.authHeader || 'auto'
  }

  function applyCodexResponsesPreset(name: string) {
    const preset = codexResponsesPresets[name.toLowerCase()]
    if (!preset) return
    const noVisionSet = new Set(preset.noVisionModels)
    options.modelMappingRows.value = Object.entries(preset.modelMapping).map(([source, target]) => ({
      id: options.nextRowId(),
      source,
      target,
      reasoning: preset.reasoningMapping[source] || '',
      noVision: noVisionSet.has(target),
    }))
    options.form.reasoningParamStyle = preset.reasoningParamStyle as typeof options.form.reasoningParamStyle
    if (preset.serviceType) {
      options.form.serviceType = preset.serviceType as typeof options.form.serviceType
    }
    options.form.codexNativeToolPassthrough = preset.codexNativeToolPassthrough
    options.form.codexToolCompat = preset.codexToolCompat
    options.form.stripCodexClientTools = preset.stripCodexClientTools
    options.form.stripImageGenerationTool = preset.stripImageGenerationTool
    options.form.normalizeNonstandardChatRoles = preset.normalizeNonstandardChatRoles
    options.form.noVision = preset.noVision
    options.form.visionFallbackModel = preset.visionFallbackModel
    options.form.visionFallbackReasoningEffort = ''
  }

  function applyPreset(presetName: string) {
    if (options.form.serviceType === 'claude') {
      applyClaudePreset(presetName)
    } else if (options.channelType() === 'responses') {
      applyCodexResponsesPreset(presetName)
    } else if (options.channelType() === 'messages' || options.channelType() === 'chat') {
      applyClaudePreset(presetName)
    }
  }

  return {
    showModelMappingPresets,
    showMessagesOpenAIChannelPresets,
    showClaudeChannelPresets,
    showCodexResponsesPresets,
    applyPreset,
  }
}
