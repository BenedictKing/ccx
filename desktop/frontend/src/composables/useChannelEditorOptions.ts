import { computed, type ComputedRef, type Ref } from 'vue'
import type { ManagedChannelType } from '@/utils/channel-type-api'

type Translator = (key: string) => string
type ServiceType = 'openai' | 'claude' | 'gemini' | 'responses' | 'copilot' | ''

type FormLike = {
  serviceType: ServiceType
}

type ChannelEditorOptionsOptions = {
  channelType: () => ManagedChannelType
  defaultServiceTypeForChannel: () => Exclude<ServiceType, ''>
  detectedServiceType: ComputedRef<ServiceType | null | undefined>
  form: FormLike
  quickServiceTypeTouched: Ref<boolean>
  t: Translator
}

const reasoningParamStyleOptions = [
  { label: 'reasoning.effort', value: 'reasoning' },
  { label: 'reasoning_effort', value: 'reasoning_effort' },
  { label: 'thinking (JD/GLM)', value: 'thinking' },
]

const textVerbosityOptions = [
  { label: 'Low', value: 'low' },
  { label: 'Medium', value: 'medium' },
  { label: 'High', value: 'high' },
]

export function useChannelEditorOptions(options: ChannelEditorOptionsOptions) {
  const serviceTypeOptions = computed(() => {
    const all = [
      { label: 'OpenAI Chat', value: 'openai' },
      { label: 'Claude', value: 'claude' },
      { label: 'Gemini', value: 'gemini' },
      { label: 'Responses (Codex)', value: 'responses' },
      { label: 'GitHub Copilot', value: 'copilot' },
    ]
    const first = options.channelType() === 'messages' ? 'claude'
      : options.channelType() === 'responses' ? 'responses'
      : options.channelType() === 'gemini' ? 'gemini'
      : 'openai'
    if (options.channelType() === 'images') return [{ label: 'OpenAI Images', value: 'openai' }]
    if (options.channelType() === 'vectors') return [{ label: 'OpenAI Embeddings', value: 'openai' }]
    const primary = all.find(o => o.value === first)
    const rest = all.filter(o => o.value !== first)
    return primary ? [primary, ...rest] : all
  })

  const recommendedServiceType = computed<string | null>(() => {
    if (options.quickServiceTypeTouched.value) return null
    return options.detectedServiceType.value || options.defaultServiceTypeForChannel()
  })

  const headerServiceTypeItems = computed(() => {
    const suffix = options.t('addChannel.serviceTypeRecommendedSuffix')
    return serviceTypeOptions.value.map(option =>
      option.value === recommendedServiceType.value
        ? { ...option, label: `${option.label}${suffix}` }
        : option,
    )
  })

  return {
    reasoningParamStyleOptions,
    textVerbosityOptions,
    serviceTypeOptions,
    headerServiceTypeItems,
  }
}
