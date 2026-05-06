import { describe, expect, it } from 'vitest'
import { buildChannelPayload } from './channelPayload'

const baseForm = {
  name: 'test-channel',
  serviceType: 'openai' as const,
  baseUrl: 'https://api.example.com/v1',
  baseUrls: [] as string[],
  website: '',
  insecureSkipVerify: false,
  lowQuality: false,
  injectDummyThoughtSignature: false,
  stripThoughtSignature: false,
  description: '',
  apiKeys: ['sk-1'],
  modelMapping: {},
  reasoningMapping: {},
  textVerbosity: '' as const,
  fastMode: false,
  customHeaders: {},
  proxyUrl: '',
  routePrefix: '',
  supportedModels: [] as string[],
  autoBlacklistBalance: true,
  failoverRules: []
}

describe('buildChannelPayload', () => {
  it('serializes reasoning mapping and channel advanced options', () => {
    const result = buildChannelPayload({
      ...baseForm,
      name: '  test-channel  ',
      baseUrl: 'https://api.example.com/v1#',
      website: ' https://platform.openai.com ',
      description: '  desc  ',
      apiKeys: ['sk-1', '  ', 'sk-2'],
      modelMapping: { 'gpt-5': 'gpt-5.2' },
      reasoningMapping: { 'gpt-5': 'high' },
      textVerbosity: 'medium',
      fastMode: true,
      customHeaders: { 'x-test': '1' },
      proxyUrl: ' http://127.0.0.1:7890 ',
      supportedModels: ['gpt-5']
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
    expect(result).not.toHaveProperty('normalizeMetadataUserId')
    expect(result).not.toHaveProperty('streamPassthroughEnabled')
    expect(result).not.toHaveProperty('sub2apiPassthroughEnabled')
    expect(result).not.toHaveProperty('strictRequestPassthroughEnabled')
  })

  it('deduplicates default version baseUrls and keeps hash variants separate', () => {
    const result = buildChannelPayload({
      ...baseForm,
      serviceType: 'responses',
      baseUrl: '',
      baseUrls: ['https://api.example.com/v1/', 'https://api.example.com/v1#', 'https://backup.example.com/v1']
    })

    expect(result.baseUrl).toBe('https://api.example.com')
    expect(result.baseUrls).toEqual([
      'https://api.example.com',
      'https://api.example.com/v1#',
      'https://backup.example.com'
    ])
  })

  it('keeps images-compatible fields without capability-test payload leakage', () => {
    const result = buildChannelPayload({
      ...baseForm,
      name: 'images-channel',
      serviceType: 'openai',
      baseUrl: 'https://api.openai.com/v1',
      insecureSkipVerify: true,
      apiKeys: ['sk-image'],
      modelMapping: { 'gpt-image-1': 'gpt-image-1' },
      customHeaders: { 'x-image': '1' },
      proxyUrl: 'http://127.0.0.1:7890',
      routePrefix: 'images',
      supportedModels: ['gpt-image-1', 'dall-e-*']
    })

    expect(result.serviceType).toBe('openai')
    expect(result.baseUrl).toBe('https://api.openai.com')
    expect(result.supportedModels).toEqual(['gpt-image-1', 'dall-e-*'])
    expect(result.customHeaders).toEqual({ 'x-image': '1' })
    expect(result.proxyUrl).toBe('http://127.0.0.1:7890')
    expect(result.routePrefix).toBe('images')
    expect(result.failoverRules).toEqual([])
    expect(result).not.toHaveProperty('rpm')
  })

  it('removes unsupported advanced options for claude channel', () => {
    const result = buildChannelPayload({
      ...baseForm,
      name: 'claude-channel',
      serviceType: 'claude',
      baseUrl: 'https://api.anthropic.com/v1',
      apiKeys: ['sk-ant'],
      modelMapping: { opus: 'claude-3-7-sonnet' },
      reasoningMapping: { opus: 'high' },
      textVerbosity: 'high',
      fastMode: true,
      supportedModels: ['opus']
    })

    expect(result.modelMapping).toEqual({ opus: 'claude-3-7-sonnet' })
    expect(result.reasoningMapping).toEqual({})
    expect(result.textVerbosity).toBe('')
    expect(result.fastMode).toBe(false)
  })

  it('keeps autoBlacklistBalance switch', () => {
    const result = buildChannelPayload({
      ...baseForm,
      serviceType: 'responses',
      autoBlacklistBalance: false
    })

    expect(result.autoBlacklistBalance).toBe(false)
  })

  it('normalizes claude failover rules and removes invalid rules', () => {
    const result = buildChannelPayload({
      ...baseForm,
      name: 'claude-rules',
      serviceType: 'claude',
      baseUrl: 'https://api.anthropic.com/v1',
      apiKeys: ['sk-ant'],
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
      ...baseForm,
      name: 'models-health',
      serviceType: 'claude',
      baseUrl: 'https://api.anthropic.com/v1',
      apiKeys: ['sk-ant'],
      modelsHealthCheckEnabled: true,
      modelsHealthCheckIntervalMinutes: 0
    })

    expect(result.modelsHealthCheckEnabled).toBe(true)
    expect(result.modelsHealthCheckIntervalMinutes).toBe(60)
  })
})
