import { describe, expect, it } from 'vitest'
import { buildChannelPayload } from './channelPayload'

describe('buildChannelPayload', () => {
  it('serializes reasoning mapping and channel advanced options', () => {
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
      sub2apiPassthroughEnabled: false,
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

  it('deduplicates baseUrls and keeps hash variant as canonical', () => {
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
      sub2apiPassthroughEnabled: false,
      strictRequestPassthroughEnabled: true,
      failoverRules: []
    })

    expect(result.baseUrl).toBe('https://api.example.com/v1#')
    expect(result.baseUrls).toEqual(['https://api.example.com/v1#', 'https://backup.example.com/v1'])
  })

  it('removes unsupported advanced options for claude channel', () => {
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
      sub2apiPassthroughEnabled: false,
      strictRequestPassthroughEnabled: true,
      failoverRules: []
    })

    expect(result.modelMapping).toEqual({ opus: 'claude-3-7-sonnet' })
    expect(result.reasoningMapping).toEqual({})
    expect(result.textVerbosity).toBe('')
    expect(result.fastMode).toBe(false)
  })

  it('keeps autoBlacklistBalance switch', () => {
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
      sub2apiPassthroughEnabled: false,
      strictRequestPassthroughEnabled: true,
      failoverRules: []
    })

    expect(result.autoBlacklistBalance).toBe(false)
  })

  it('keeps normalizeMetadataUserId switch', () => {
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
      sub2apiPassthroughEnabled: false,
      strictRequestPassthroughEnabled: true,
      failoverRules: []
    })

    expect(result.normalizeMetadataUserId).toBe(false)
  })

  it('keeps strictRequestPassthroughEnabled for claude only', () => {
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
      sub2apiPassthroughEnabled: false,
      strictRequestPassthroughEnabled: false,
      failoverRules: []
    })

    expect(result.strictRequestPassthroughEnabled).toBe(false)
  })

  it('enforces mutual exclusion: sub2api passthrough wins', () => {
    const result = buildChannelPayload({
      name: 'claude-exclusive',
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
      sub2apiPassthroughEnabled: true,
      strictRequestPassthroughEnabled: true,
      failoverRules: []
    })

    expect(result.sub2apiPassthroughEnabled).toBe(true)
    expect(result.streamPassthroughEnabled).toBe(false)
  })

  it('forces non-claude passthrough options to safe defaults', () => {
    const result = buildChannelPayload({
      name: 'responses-channel',
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
      normalizeMetadataUserId: true,
      streamPassthroughEnabled: false,
      sub2apiPassthroughEnabled: true,
      strictRequestPassthroughEnabled: false,
      failoverRules: [
        {
          action: 'blacklist',
          description: 'ignored',
          statusCodes: [401],
          errorCodes: [],
          keywords: []
        }
      ],
      rpm: 0
    })

    expect(result.streamPassthroughEnabled).toBe(true)
    expect(result.sub2apiPassthroughEnabled).toBe(false)
    expect(result.strictRequestPassthroughEnabled).toBe(true)
    expect(result.failoverRules).toEqual([])
    expect(result.rpm).toBe(10)
  })

  it('normalizes claude failover rules and removes invalid rules', () => {
    const result = buildChannelPayload({
      name: 'claude-rules',
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
      sub2apiPassthroughEnabled: false,
      strictRequestPassthroughEnabled: true,
      failoverRules: [
        {
          action: 'cooldown',
          description: '  429 cooldown  ',
          statusCodes: [429, 99, 600],
          errorCodes: ['  invalid_request_error  ', ''],
          keywords: ['  usage limits ', ''],
          durationMinutes: 61.8
        },
        {
          action: 'blacklist',
          description: 'invalid: no condition',
          statusCodes: [],
          errorCodes: [],
          keywords: []
        }
      ]
    })

    expect(result.failoverRules).toEqual([
      {
        action: 'cooldown',
        description: '429 cooldown',
        statusCodes: [429],
        errorCodes: ['invalid_request_error'],
        keywords: ['usage limits'],
        durationMinutes: 61
      }
    ])
  })

  it('keeps models health check options and falls back to default interval', () => {
    const result = buildChannelPayload({
      name: 'models-health',
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
      sub2apiPassthroughEnabled: false,
      strictRequestPassthroughEnabled: true,
      modelsHealthCheckEnabled: true,
      modelsHealthCheckIntervalMinutes: 0,
      failoverRules: []
    })

    expect(result.modelsHealthCheckEnabled).toBe(true)
    expect(result.modelsHealthCheckIntervalMinutes).toBe(60)
  })
})
