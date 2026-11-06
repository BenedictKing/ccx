import { describe, it, expect } from 'vitest'
import { buildExpectedRequestUrls } from './expectedRequestUrls'
import { hasExactBaseUrlPaths, setExactBaseUrlPaths } from './baseUrlSemantics'

describe('buildExpectedRequestUrls', () => {
  it('保留自定义 OpenAI Chat 路径并准确预览请求', () => {
    const baseUrl = 'https://relay.example.com/custom/chat'
    const exact = setExactBaseUrlPaths(baseUrl, true)
    expect(exact).toBe(`${baseUrl}#`)
    expect(hasExactBaseUrlPaths(exact)).toBe(true)
    expect(buildExpectedRequestUrls('chat', 'openai', exact)[0].expectedUrl).toBe(
      'https://relay.example.com/custom/chat/chat/completions'
    )
    expect(setExactBaseUrlPaths(exact, false)).toBe(baseUrl)
  })

  it('多地址开关对每条地址使用相同路径语义', () => {
    const exact = setExactBaseUrlPaths('https://a.example/custom\nhttps://b.example/custom', true)
    expect(hasExactBaseUrlPaths(exact)).toBe(true)
    expect(exact).toBe('https://a.example/custom#\nhttps://b.example/custom#')
  })
  it('应为 responses 渠道上的 gemini 上游生成正确预览 URL', () => {
    const result = buildExpectedRequestUrls('responses', 'gemini', 'https://generativelanguage.googleapis.com')

    expect(result).toHaveLength(1)
    expect(result[0].expectedUrl).toBe(
      'https://generativelanguage.googleapis.com/v1beta/models/{model}:generateContent'
    )
  })

  it('应在 baseUrl 已含版本前缀时避免重复追加版本', () => {
    const result = buildExpectedRequestUrls('responses', 'gemini', 'https://generativelanguage.googleapis.com/v1beta')

    expect(result).toHaveLength(1)
    expect(result[0].expectedUrl).toBe(
      'https://generativelanguage.googleapis.com/v1beta/models/{model}:generateContent'
    )
  })

  it('应为 responses 渠道上的 claude 上游生成 messages 端点', () => {
    const result = buildExpectedRequestUrls('responses', 'claude', 'https://api.anthropic.com')

    expect(result).toHaveLength(1)
    expect(result[0].expectedUrl).toBe('https://api.anthropic.com/v1/messages')
  })

  it('应为 responses 渠道上的 claude 上游避免重复版本前缀', () => {
    const result = buildExpectedRequestUrls('responses', 'claude', 'https://token-plan-cn.xiaomimimo.com/v1')

    expect(result).toHaveLength(1)
    expect(result[0].expectedUrl).toBe('https://token-plan-cn.xiaomimimo.com/v1/messages')
  })

  it('应为 responses 渠道上的 openai 上游生成 chat completions 端点', () => {
    const result = buildExpectedRequestUrls('responses', 'openai', 'https://api.openai.com')

    expect(result).toHaveLength(1)
    expect(result[0].expectedUrl).toBe('https://api.openai.com/v1/chat/completions')
  })

  it('应为 messages 渠道上的 responses 上游生成 responses 端点', () => {
    const result = buildExpectedRequestUrls('messages', 'responses', 'https://api.openai.com')

    expect(result).toHaveLength(1)
    expect(result[0].expectedUrl).toBe('https://api.openai.com/v1/responses')
  })

  it('应为 chat 渠道上的 responses 上游生成 responses 端点', () => {
    const result = buildExpectedRequestUrls('chat', 'responses', 'https://api.openai.com')

    expect(result).toHaveLength(1)
    expect(result[0].expectedUrl).toBe('https://api.openai.com/v1/responses')
  })

  it('应让根域名与默认版本前缀预览到同一请求地址', () => {
    const root = buildExpectedRequestUrls('chat', 'openai', 'https://new.timefiles.online')
    const versioned = buildExpectedRequestUrls('chat', 'openai', 'https://new.timefiles.online/v1')

    expect(root[0].expectedUrl).toBe('https://new.timefiles.online/v1/chat/completions')
    expect(versioned[0].expectedUrl).toBe(root[0].expectedUrl)
  })

  it('应为 images 渠道生成 OpenAI Images 端点', () => {
    const result = buildExpectedRequestUrls('images', 'openai', 'https://api.openai.com')

    expect(result).toEqual([
      {
        baseUrl: 'https://api.openai.com',
        expectedUrl: 'https://api.openai.com/v1/images/generations'
      }
    ])
  })

  it('应为带 # 的 images 渠道保留无版本前缀语义', () => {
    const result = buildExpectedRequestUrls('images', 'openai', 'https://api.openai.com#')

    expect(result).toEqual([
      {
        baseUrl: 'https://api.openai.com#',
        expectedUrl: 'https://api.openai.com/images/generations'
      }
    ])
  })

  it('builds the OpenAI embeddings endpoint for vectors channels', () => {
    const result = buildExpectedRequestUrls('vectors', 'openai', 'https://api.openai.com')

    expect(result).toEqual([
      {
        baseUrl: 'https://api.openai.com',
        expectedUrl: 'https://api.openai.com/v1/embeddings'
      }
    ])
  })

  it('keeps no-version semantics for vectors channels with #', () => {
    const result = buildExpectedRequestUrls('vectors', 'openai', 'https://api.openai.com#')

    expect(result).toEqual([
      {
        baseUrl: 'https://api.openai.com#',
        expectedUrl: 'https://api.openai.com/embeddings'
      }
    ])
  })
})
