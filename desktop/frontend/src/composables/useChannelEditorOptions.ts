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
  const DEFAULT_SELECT_VALUE = 'default'
  const reasoningEffortOptions = computed(() => [
    { label: options.t('channelEditor.compat.selectDefault'), value: DEFAULT_SELECT_VALUE },
    { label: 'None', value: 'none' },
    { label: 'Minimal', value: 'minimal' },
    { label: 'Low', value: 'low' },
    { label: 'Medium', value: 'medium' },
    { label: 'High', value: 'high' },
    { label: 'XHigh', value: 'xhigh' },
    { label: 'Max', value: 'max' },
  ])

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

  const modelMappingHint = computed(() => {
    if (options.channelType() === 'vectors') {
      return options.t('addChannel.modelMappingHintVectors')
    }
    if (options.channelType() === 'chat' || options.channelType() === 'images') {
      return options.t('addChannel.modelMappingHintChat')
    }
    if (options.channelType() === 'gemini') return options.t('addChannel.modelMappingHintGemini')
    if (options.channelType() === 'responses') return options.t('addChannel.modelMappingHintResponses')
    return options.t('addChannel.modelMappingHintMessages')
  })

  const targetModelPlaceholder = computed(() => {
    if (options.channelType() === 'vectors') {
      return options.t('addChannel.targetModelPlaceholderVectors')
    }
    if (options.channelType() === 'chat' || options.channelType() === 'images') {
      return options.t('addChannel.targetModelPlaceholderChat')
    }
    if (options.channelType() === 'responses') return options.t('addChannel.targetModelPlaceholderResponses')
    if (options.channelType() === 'gemini') return options.t('addChannel.targetModelPlaceholderGemini')
    return options.t('addChannel.targetModelPlaceholderMessages')
  })

  return {
    reasoningParamStyleOptions,
    textVerbosityOptions,
    DEFAULT_SELECT_VALUE,
    reasoningEffortOptions,
    serviceTypeOptions,
    headerServiceTypeItems,
    modelMappingHint,
    targetModelPlaceholder,
  }
}
