import { describe, expect, it } from 'vitest'
import { buildChannelPayload } from './channelPayload'

describe('buildChannelPayload', () => {
  it('应序列化 reasoningMapping 与渠道级 verbosity/fastMode', () => {
    const result = buildChannelPayload({
      name: '  test-channel  ',
      serviceType: 'openai',
      baseUrl: 'https://api.example.com/v1#',
      baseUrls: [],
      website: ' https://platform.openai.com ',
      insecureSkipVerify: false,
      lowQuality: false,
      injectDummyThoughtSignature: false,
      stripThoughtSignature: false,
      description: '  desc  ',
      apiKeys: ['sk-1', '  ', 'sk-2'],
      modelMapping: { 'gpt-5': 'gpt-5.2' },
      reasoningMapping: { 'gpt-5': 'high' },
      textVerbosity: 'medium',
      fastMode: true,
      customHeaders: { 'x-test': '1' },
      proxyUrl: ' http://127.0.0.1:7890 ',
      routePrefix: '',
      supportedModels: ['gpt-5'],
      autoBlacklistBalance: true,
      normalizeMetadataUserId: true,
      streamPassthroughEnabled: true,
      strictRequestPassthroughEnabled: true,
      failoverRules: []
    })

    expect(result.name).toBe('test-channel')
    expect(result.baseUrl).toBe('https://api.example.com/v1#')
    expect(result.website).toBe('https://platform.openai.com')
    expect(result.description).toBe('desc')
    expect(result.apiKeys).toEqual(['sk-1', 'sk-2'])
    expect(result.modelMapping).toEqual({ 'gpt-5': 'gpt-5.2' })
    expect(result.reasoningMapping).toEqual({ 'gpt-5': 'high' })
    expect(result.textVerbosity).toBe('medium')
    expect(result.fastMode).toBe(true)
    expect(result.proxyUrl).toBe('http://127.0.0.1:7890')
  })

  it('应对多个 baseUrls 去重并保留 baseUrls 输出', () => {
    const result = buildChannelPayload({
      name: 'multi',
      serviceType: 'responses',
      baseUrl: '',
      baseUrls: ['https://api.example.com/v1/', 'https://api.example.com/v1#', 'https://backup.example.com/v1'],
      website: '',
      insecureSkipVerify: false,
      lowQuality: false,
      injectDummyThoughtSignature: false,
      stripThoughtSignature: false,
      description: '',
      apiKeys: ['sk-1'],
      modelMapping: {},
      reasoningMapping: {},
      textVerbosity: '',
      fastMode: false,
      customHeaders: {},
      proxyUrl: '',
      routePrefix: '',
      supportedModels: [],
      autoBlacklistBalance: true,
      normalizeMetadataUserId: true,
      streamPassthroughEnabled: true,
      strictRequestPassthroughEnabled: true,
      failoverRules: []
    })

    expect(result.baseUrl).toBe('https://api.example.com/v1#')
    expect(result.baseUrls).toEqual(['https://api.example.com/v1#', 'https://backup.example.com/v1'])
  })

  it('应清空 claude 渠道不支持的高级参数', () => {
    const result = buildChannelPayload({
      name: 'claude-channel',
      serviceType: 'claude',
      baseUrl: 'https://api.anthropic.com/v1',
      baseUrls: [],
      website: '',
      insecureSkipVerify: false,
      lowQuality: false,
      injectDummyThoughtSignature: false,
      stripThoughtSignature: false,
      description: '',
      apiKeys: ['sk-ant'],
      modelMapping: { opus: 'claude-3-7-sonnet' },
      reasoningMapping: { opus: 'high' },
      textVerbosity: 'high',
      fastMode: true,
      customHeaders: {},
      proxyUrl: '',
      routePrefix: '',
      supportedModels: ['opus'],
      autoBlacklistBalance: true,
      normalizeMetadataUserId: true,
      streamPassthroughEnabled: true,
      strictRequestPassthroughEnabled: true,
      failoverRules: []
    })

    expect(result.modelMapping).toEqual({ opus: 'claude-3-7-sonnet' })
    expect(result.reasoningMapping).toEqual({})
    expect(result.textVerbosity).toBe('')
    expect(result.fastMode).toBe(false)
  })

  it('应携带 autoBlacklistBalance 开关', () => {
    const result = buildChannelPayload({
      name: 'balance-guard',
      serviceType: 'responses',
      baseUrl: 'https://api.example.com/v1',
      baseUrls: [],
      website: '',
      insecureSkipVerify: false,
      lowQuality: false,
      injectDummyThoughtSignature: false,
      stripThoughtSignature: false,
      description: '',
      apiKeys: ['sk-1'],
      modelMapping: {},
      reasoningMapping: {},
      textVerbosity: '',
      fastMode: false,
      customHeaders: {},
      proxyUrl: '',
      routePrefix: '',
      supportedModels: [],
      autoBlacklistBalance: false,
      normalizeMetadataUserId: true,
      streamPassthroughEnabled: true,
      strictRequestPassthroughEnabled: true,
      failoverRules: []
    })

    expect(result.autoBlacklistBalance).toBe(false)
  })

  it('应携带 normalizeMetadataUserId 开关', () => {
    const result = buildChannelPayload({
      name: 'metadata-guard',
      serviceType: 'responses',
      baseUrl: 'https://api.example.com/v1',
      baseUrls: [],
      website: '',
      insecureSkipVerify: false,
      lowQuality: false,
      injectDummyThoughtSignature: false,
      stripThoughtSignature: false,
      description: '',
      apiKeys: ['sk-1'],
      modelMapping: {},
      reasoningMapping: {},
      textVerbosity: '',
      fastMode: false,
      customHeaders: {},
      proxyUrl: '',
      routePrefix: '',
      supportedModels: [],
      autoBlacklistBalance: true,
      normalizeMetadataUserId: false,
      streamPassthroughEnabled: true,
      strictRequestPassthroughEnabled: true,
      failoverRules: []
    })

    expect(result.normalizeMetadataUserId).toBe(false)
  })

  it('应携带 strictRequestPassthroughEnabled 开关', () => {
    const result = buildChannelPayload({
      name: 'claude-strict',
      serviceType: 'claude',
      baseUrl: 'https://api.anthropic.com/v1',
      baseUrls: [],
      website: '',
      insecureSkipVerify: false,
      lowQuality: false,
      injectDummyThoughtSignature: false,
      stripThoughtSignature: false,
      description: '',
      apiKeys: ['sk-ant'],
      modelMapping: {},
      reasoningMapping: {},
      textVerbosity: '',
      fastMode: false,
      customHeaders: {},
      proxyUrl: '',
      routePrefix: '',
      supportedModels: [],
      autoBlacklistBalance: true,
      normalizeMetadataUserId: true,
      streamPassthroughEnabled: true,
      strictRequestPassthroughEnabled: false,
      failoverRules: []
    })

    expect(result.strictRequestPassthroughEnabled).toBe(false)
  })
})
