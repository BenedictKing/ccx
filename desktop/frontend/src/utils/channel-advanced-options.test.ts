import { describe, expect, it } from 'vitest'
import { normalizeAdvancedChannelOptions, supportsAdvancedChannelOptions } from './channel-advanced-options'

describe('channel-advanced-options', () => {
  it('Claude 不保留 OpenAI 专属高级选项', () => {
    expect(supportsAdvancedChannelOptions('claude')).toBe(false)

    const result = normalizeAdvancedChannelOptions('claude', {
      reasoningParamStyle: 'thinking',
      textVerbosity: 'high',
      fastMode: true
    })

    expect(result).toEqual({
      reasoningParamStyle: 'reasoning',
      textVerbosity: '',
      fastMode: false
    })
  })

  it('OpenAI 渠道保留高级选项', () => {
    expect(supportsAdvancedChannelOptions('openai')).toBe(true)

    const result = normalizeAdvancedChannelOptions('openai', {
      reasoningParamStyle: 'reasoning_effort',
      textVerbosity: 'high',
      fastMode: true
    })

    expect(result).toEqual({
      reasoningParamStyle: 'reasoning_effort',
      textVerbosity: 'high',
      fastMode: true
    })
  })
})
