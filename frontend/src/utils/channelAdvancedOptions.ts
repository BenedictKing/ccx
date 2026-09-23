export type ChannelServiceType = 'openai' | 'gemini' | 'claude' | 'responses' | 'copilot' | ''
export type ReasoningEffort = 'none' | 'minimal' | 'low' | 'medium' | 'high' | 'xhigh' | 'max'
export type ReasoningParamStyle = 'reasoning' | 'reasoning_effort' | 'thinking'
export type TextVerbosity = 'low' | 'medium' | 'high' | ''

export interface AdvancedChannelOptions {
  reasoningParamStyle: ReasoningParamStyle
  textVerbosity: TextVerbosity
  fastMode: boolean
}

export const supportsAdvancedChannelOptions = (serviceType: ChannelServiceType): boolean => {
  return serviceType === 'openai' || serviceType === 'responses' || serviceType === 'copilot'
}

export const normalizeAdvancedChannelOptions = (
  serviceType: ChannelServiceType,
  options: AdvancedChannelOptions
): AdvancedChannelOptions => {
  if (supportsAdvancedChannelOptions(serviceType)) {
    return options
  }

  return {
    reasoningParamStyle: 'reasoning',
    textVerbosity: '',
    fastMode: false
  }
}
