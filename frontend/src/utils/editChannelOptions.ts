import { computed, type ComputedRef } from 'vue'

type ChannelType = 'messages' | 'chat' | 'responses' | 'gemini' | 'images' | 'vectors'
type FormLike = {
  modelMapping: Record<string, string>
}

export function useEditChannelOptions(
  channelType: ComputedRef<ChannelType>,
  form: FormLike,
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
    if (channelType.value === 'chat') {
      return ['codex', 'gpt', 'mini', 'gpt-5', 'gpt-5.5', 'gpt-5.4', 'gpt-5.6-sol', 'gpt-5.6-terra', 'gpt-5.6-luna', 'gpt-5.4-mini']
    }
    if (channelType.value === 'images') {
      return ['gpt-image-2', 'gpt-image-1', 'dall-e-3', 'dall-e-2']
    }
    if (channelType.value === 'vectors') return []
    if (channelType.value === 'gemini') {
      return ['gemini-3.5-flash', 'gemini-3.1-pro-preview', 'gemini-3-pro-preview', 'gemini-3-flash-preview', 'gemini-3.1-flash-lite', 'gemini-2.5-pro', 'gemini-2.5-flash', 'gemini-2.5-flash-lite', 'gemini-2']
    }
    if (channelType.value === 'responses') {
      return ['codex', 'codex-auto-review', 'gpt-5', 'gpt', 'mini', 'gpt-5.5', 'gpt-5.4', 'gpt-5.6-sol', 'gpt-5.6-terra', 'gpt-5.6-luna', 'gpt-5.4-mini']
    }
    return ['fable', 'opus', 'sonnet', 'haiku']
  })

  const sourceModelOptions = computed(() => {
    const configuredModels = Object.keys(form.modelMapping)
    return allSourceModelOptions.value
      .filter(model => !configuredModels.includes(model))
      .map(model => ({ title: model, value: model }))
  })

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
    sourceModelOptions,
    reasoningParamStyleOptions,
    textVerbosityOptions,
  }
}
