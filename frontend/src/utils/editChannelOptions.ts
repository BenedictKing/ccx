import { computed, type ComputedRef } from 'vue'
import { codexSourceModelNames } from '../../../shared/model-mapping'

type ChannelType = 'messages' | 'chat' | 'responses' | 'gemini' | 'images' | 'vectors'
type Translator = (key: string) => string
type FormLike = {
  modelMapping: Record<string, string>
}

export function useEditChannelOptions(
  channelType: ComputedRef<ChannelType>,
  form: FormLike,
  t: Translator,
) {
  const serviceTypeOptions = computed(() => {
    const allOptions = [
      { title: 'OpenAI Chat', value: 'openai' },
      { title: 'Claude', value: 'claude' },
      { title: 'Gemini', value: 'gemini' },
      { title: 'Responses (Codex)', value: 'responses' },
      { title: 'GitHub Copilot', value: 'copilot' },
    ]

    const reorder = (options: typeof allOptions, first: string) => {
      const firstOption = options.find(o => o.value === first)
      const rest = options.filter(o => o.value !== first)
      return firstOption ? [firstOption, ...rest] : options
    }

    switch (channelType.value) {
      case 'messages':
        return reorder(allOptions, 'claude')
      case 'chat':
        return reorder(allOptions, 'openai')
      case 'responses':
        return reorder(allOptions, 'responses')
      case 'images':
        return [{ title: 'OpenAI Images', value: 'openai' }]
      case 'vectors':
        return [{ title: 'OpenAI Embeddings', value: 'openai' }]
      case 'gemini':
        return reorder(allOptions, 'gemini')
      default:
        return allOptions
    }
  })

  const allSourceModelOptions = computed(() => {
    if (channelType.value === 'chat' || channelType.value === 'responses') {
      return codexSourceModelNames.map(value => ({ title: value, value }))
    }
    if (channelType.value === 'images') {
      return [
        { title: 'gpt-image-2', value: 'gpt-image-2' },
        { title: 'gpt-image-1', value: 'gpt-image-1' },
        { title: 'dall-e-3', value: 'dall-e-3' },
        { title: 'dall-e-2', value: 'dall-e-2' },
      ]
    }
    if (channelType.value === 'vectors') {
      return []
    }
    if (channelType.value === 'gemini') {
      return [
        { title: 'gemini-3.5-flash', value: 'gemini-3.5-flash' },
        { title: 'gemini-3.1-pro-preview', value: 'gemini-3.1-pro-preview' },
        { title: 'gemini-3-pro-preview', value: 'gemini-3-pro-preview' },
        { title: 'gemini-3-flash-preview', value: 'gemini-3-flash-preview' },
        { title: 'gemini-3.1-flash-lite', value: 'gemini-3.1-flash-lite' },
        { title: 'gemini-2.5-pro', value: 'gemini-2.5-pro' },
        { title: 'gemini-2.5-flash', value: 'gemini-2.5-flash' },
        { title: 'gemini-2.5-flash-lite', value: 'gemini-2.5-flash-lite' },
        { title: 'gemini-2', value: 'gemini-2' },
      ]
    }
    return [
      { title: 'fable', value: 'fable' },
      { title: 'opus', value: 'opus' },
      { title: 'sonnet', value: 'sonnet' },
      { title: 'haiku', value: 'haiku' },
    ]
  })

  const sourceModelOptions = computed(() => {
    const configuredModels = Object.keys(form.modelMapping)
    return allSourceModelOptions.value.filter(opt => !configuredModels.includes(opt.value))
  })

  const modelMappingHint = computed(() => {
    if (channelType.value === 'vectors') {
      return t('addChannel.modelMappingHintVectors')
    }
    if (channelType.value === 'chat' || channelType.value === 'images') {
      return t('addChannel.modelMappingHintChat')
    }
    if (channelType.value === 'gemini') {
      return t('addChannel.modelMappingHintGemini')
    }
    if (channelType.value === 'responses') {
      return t('addChannel.modelMappingHintResponses')
    }
    return t('addChannel.modelMappingHintMessages')
  })

  const targetModelPlaceholder = computed(() => {
    if (channelType.value === 'vectors') {
      return t('addChannel.targetModelPlaceholderVectors')
    }
    if (channelType.value === 'chat' || channelType.value === 'images') {
      return t('addChannel.targetModelPlaceholderChat')
    }
    if (channelType.value === 'responses') {
      return t('addChannel.targetModelPlaceholderResponses')
    }
    if (channelType.value === 'gemini') {
      return t('addChannel.targetModelPlaceholderGemini')
    }
    return t('addChannel.targetModelPlaceholderMessages')
  })

  const reasoningEffortOptions = [
    { title: t('addChannel.reasoningDefault'), value: '' },
    { title: 'None', value: 'none' },
    { title: 'Low', value: 'low' },
    { title: 'Medium', value: 'medium' },
    { title: 'High', value: 'high' },
    { title: 'XHigh', value: 'xhigh' },
    { title: 'Max', value: 'max' },
  ]

  const reasoningParamStyleOptions = [
    { title: 'reasoning.effort', value: 'reasoning' },
    { title: 'reasoning_effort', value: 'reasoning_effort' },
    { title: 'thinking (JD/GLM)', value: 'thinking' },
  ]

  const textVerbosityOptions = [
    { title: 'Low', value: 'low' },
    { title: 'Medium', value: 'medium' },
    { title: 'High', value: 'high' },
  ]

  return {
    serviceTypeOptions,
    allSourceModelOptions,
    sourceModelOptions,
    modelMappingHint,
    targetModelPlaceholder,
    reasoningEffortOptions,
    reasoningParamStyleOptions,
    textVerbosityOptions,
  }
}
