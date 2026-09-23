import { computed, type ComputedRef } from 'vue'

type ChannelType = 'messages' | 'chat' | 'responses' | 'gemini' | 'images' | 'vectors'

export function useEditChannelOptions(
  channelType: ComputedRef<ChannelType>,
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
    reasoningParamStyleOptions,
    textVerbosityOptions,
  }
}
