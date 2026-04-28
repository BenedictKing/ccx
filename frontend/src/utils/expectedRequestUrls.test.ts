import { describe, expect, it } from 'vitest'
import { buildExpectedRequestUrls } from './expectedRequestUrls'

describe('buildExpectedRequestUrls', () => {
  it('builds a Gemini upstream preview URL for a responses channel', () => {
    const result = buildExpectedRequestUrls('responses', 'gemini', 'https://generativelanguage.googleapis.com')

    expect(result).toHaveLength(1)
    expect(result[0].expectedUrl).toBe(
      'https://generativelanguage.googleapis.com/v1beta/models/{model}:generateContent'
    )
  })

  it('does not duplicate version prefixes already present in baseUrl', () => {
    const result = buildExpectedRequestUrls('responses', 'gemini', 'https://generativelanguage.googleapis.com/v1beta')

    expect(result).toHaveLength(1)
    expect(result[0].expectedUrl).toBe(
      'https://generativelanguage.googleapis.com/v1beta/models/{model}:generateContent'
    )
  })

  it('builds a Claude messages upstream preview URL for a responses channel', () => {
    const result = buildExpectedRequestUrls('responses', 'claude', 'https://api.anthropic.com')

    expect(result).toHaveLength(1)
    expect(result[0].expectedUrl).toBe('https://api.anthropic.com/v1/messages')
  })

  it('builds an OpenAI chat upstream preview URL for a responses channel', () => {
    const result = buildExpectedRequestUrls('responses', 'openai', 'https://api.openai.com')

    expect(result).toHaveLength(1)
    expect(result[0].expectedUrl).toBe('https://api.openai.com/v1/chat/completions')
  })

  it('builds a responses upstream preview URL for a messages channel', () => {
    const result = buildExpectedRequestUrls('messages', 'responses', 'https://api.openai.com')

    expect(result).toHaveLength(1)
    expect(result[0].expectedUrl).toBe('https://api.openai.com/v1/responses')
  })

  it('builds a responses upstream preview URL for a chat channel', () => {
    const result = buildExpectedRequestUrls('chat', 'responses', 'https://api.openai.com')

    expect(result).toHaveLength(1)
    expect(result[0].expectedUrl).toBe('https://api.openai.com/v1/responses')
  })

  it('builds a responses upstream preview URL for a gemini channel', () => {
    const result = buildExpectedRequestUrls('gemini', 'responses', 'https://proxy.example.com')

    expect(result).toHaveLength(1)
    expect(result[0].expectedUrl).toBe('https://proxy.example.com/v1/responses')
  })

  it('builds an image generation preview URL for images channels', () => {
    const result = buildExpectedRequestUrls('images', 'openai', 'https://api.openai.com')

    expect(result).toHaveLength(1)
    expect(result[0].expectedUrl).toBe('https://api.openai.com/v1/images/generations')
  })
})
