import { describe, expect, it } from 'vitest'
import { parseQuickInput } from './quick-input-parser'

describe('quick input service type detection', () => {
  it('detects Claude Messages endpoints and strips the protocol path', () => {
    const result = parseQuickInput('https://api.example.com/v1/messages sk-key1234567890')

    expect(result.detectedServiceType).toBe('claude')
    expect(result.detectedBaseUrl).toBe('https://api.example.com')
  })

  it('detects OpenAI Chat endpoints and strips the protocol path', () => {
    const result = parseQuickInput('https://api.example.com/v1/chat/completions sk-key1234567890')

    expect(result.detectedServiceType).toBe('openai')
    expect(result.detectedBaseUrl).toBe('https://api.example.com')
  })

  it('detects OpenAI Embeddings endpoints and strips the protocol path', () => {
    const result = parseQuickInput('https://api.example.com/v1/embeddings sk-key1234567890')

    expect(result.detectedServiceType).toBe('openai')
    expect(result.detectedBaseUrl).toBe('https://api.example.com')
  })

  it('detects Responses endpoints and strips the protocol path', () => {
    const result = parseQuickInput('https://api.example.com/v1/responses sk-key1234567890')

    expect(result.detectedServiceType).toBe('responses')
    expect(result.detectedBaseUrl).toBe('https://api.example.com')
  })

  it('detects Gemini generateContent endpoints and strips the protocol path', () => {
    const result = parseQuickInput('https://generativelanguage.googleapis.com/v1beta/models/gemini-3-pro:generateContent AIza1234567890abcdefghijklmnopqrstuv')

    expect(result.detectedServiceType).toBe('gemini')
    expect(result.detectedBaseUrl).toBe('https://generativelanguage.googleapis.com')
  })

  it('detects common Anthropic and Claude URL hints without stripping non-protocol paths', () => {
    const result = parseQuickInput('https://relay.example.com/anthropic sk-key1234567890')

    expect(result.detectedServiceType).toBe('claude')
    expect(result.detectedBaseUrl).toBe('https://relay.example.com/anthropic')
  })

  it('strips third-party dashboard paths such as /api-keys/create back to origin', () => {
    const result = parseQuickInput('https://aihub.top/api-keys/create sk-key1234567890')

    expect(result.detectedServiceType).toBeNull()
    expect(result.detectedBaseUrl).toBe('https://aihub.top')
  })

  it('detects built-in preset URLs that do not include protocol keywords', () => {
    expect(parseQuickInput('https://cp.compshare.cn sk-key1234567890').detectedServiceType).toBe('claude')
    expect(parseQuickInput('https://openrouter.ai/api sk-key1234567890').detectedServiceType).toBe('claude')
    expect(parseQuickInput('https://openrouter.ai/api/v1 sk-key1234567890').detectedServiceType).toBe('openai')
    expect(parseQuickInput('https://api.kimi.com/coding/v1 sk-key1234567890').detectedServiceType).toBe('openai')
    expect(parseQuickInput('https://maas-coding-api.cn-huabei-1.xf-yun.com/anthropic sk-key1234567890').detectedServiceType).toBe('claude')
    expect(parseQuickInput('https://maas-coding-api.cn-huabei-1.xf-yun.com/v2 sk-key1234567890').detectedServiceType).toBe('openai')
    expect(parseQuickInput('https://maas-coding-api.cn-huabei-1.xf-yun.com/v1/responses sk-key1234567890').detectedServiceType).toBe('responses')
    expect(parseQuickInput('https://maas-api.cn-huabei-1.xf-yun.com/anthropic sk-key1234567890').detectedServiceType).toBe('claude')
    expect(parseQuickInput('https://maas-api.cn-huabei-1.xf-yun.com/v2 sk-key1234567890').detectedServiceType).toBe('openai')
    expect(parseQuickInput('https://api-ai.gitcode.com/v1 sk-key1234567890').detectedServiceType).toBe('openai')
  })
})
