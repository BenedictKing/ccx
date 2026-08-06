import { describe, expect, it } from 'vitest'
import { buildChannelPayload, embeddingCapabilityRowsToRecord } from './channelPayload'

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
      modelMapping: { 'gpt-5': 'gpt-5.4' },
      reasoningMapping: { 'gpt-5': 'max' },
      reasoningParamStyle: 'reasoning_effort',
      textVerbosity: 'medium',
      fastMode: true,
      customHeaders: { 'x-test': '1' },
      proxyUrl: ' http://127.0.0.1:7890 ',
      requestTimeoutMs: 15000,
      responseHeaderTimeoutMs: 90000,
      routePrefix: '',
      supportedModels: ['gpt-5'],
      autoBlacklistBalance: true,
      normalizeMetadataUserId: true,
      normalizeSystemRoleToTopLevel: false,
      codexToolCompat: true,
      noVision: false,
      noVisionModels: [],
      visionFallbackModel: '',
      historicalImageTurnLimit: 3
    })

    // 渠道名称由首个 baseURL 自动派生（api.example.com → example），不再使用手工传入值
    expect(result.name).toBe('example')
    expect(result.baseUrl).toBe('https://api.example.com/v1#')
    expect(result.website).toBe('https://platform.openai.com')
    expect(result.description).toBe('desc')
    expect(result.apiKeys).toEqual(['sk-1', 'sk-2'])
    expect(result.modelMapping).toEqual({ 'gpt-5': 'gpt-5.4' })
    expect(result.reasoningMapping).toEqual({ 'gpt-5': 'max' })
    expect(result.reasoningParamStyle).toBe('reasoning_effort')
    expect(result.textVerbosity).toBe('medium')
    expect(result.fastMode).toBe(true)
    expect(result.proxyUrl).toBe('http://127.0.0.1:7890')
    expect(result.requestTimeoutMs).toBe(15000)
    expect(result.responseHeaderTimeoutMs).toBe(90000)
    expect(result.historicalImageTurnLimit).toBe(3)
  })

  it('Copilot 渠道省略 Base URL 时应写入默认上游地址', () => {
    const result = buildChannelPayload({
      name: 'copilot-channel',
      serviceType: 'copilot',
      baseUrl: '',
      baseUrls: [],
      website: '',
      insecureSkipVerify: false,
      lowQuality: false,
      injectDummyThoughtSignature: false,
      stripThoughtSignature: false,
      description: '',
      apiKeys: [],
      modelMapping: {},
      reasoningMapping: {},
      reasoningParamStyle: 'reasoning',
      textVerbosity: '',
      fastMode: false,
      customHeaders: {},
      proxyUrl: '',
      routePrefix: '',
      supportedModels: [],
      autoBlacklistBalance: true,
      normalizeMetadataUserId: true,
      normalizeSystemRoleToTopLevel: false,
      codexToolCompat: false,
      noVision: false,
      noVisionModels: [],
      visionFallbackModel: ''
    })

    expect(result.baseUrl).toBe('https://api.githubcopilot.com')
    expect(result.baseUrls).toBeUndefined()
  })

  it('应将模型映射中的 combobox 对象规整为字符串', () => {
    const result = buildChannelPayload({
      name: 'mapping-object',
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
      modelMapping: {
        '{"title":"codex","value":"codex"}': { title: 'MiMo', value: 'mimo-v2.5-pro' }
      },
      reasoningMapping: {},
      reasoningParamStyle: 'reasoning',
      textVerbosity: '',
      fastMode: false,
      customHeaders: {},
      proxyUrl: '',
      routePrefix: '',
      supportedModels: [],
      autoBlacklistBalance: true,
      normalizeMetadataUserId: true,
      normalizeSystemRoleToTopLevel: false,
      codexToolCompat: true,
      noVision: false,
      noVisionModels: [],
      visionFallbackModel: { title: 'MiMo', value: 'mimo-v2.5-pro' }
    })

    expect(result.modelMapping).toEqual({ codex: 'mimo-v2.5-pro' })
    expect(result.visionFallbackModel).toBe('mimo-v2.5-pro')
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
      reasoningParamStyle: 'reasoning',
      textVerbosity: '',
      fastMode: false,
      customHeaders: {},
      proxyUrl: '',
      routePrefix: '',
      supportedModels: [],
      autoBlacklistBalance: true,
      normalizeMetadataUserId: true,
      normalizeSystemRoleToTopLevel: false,
      codexToolCompat: true,
      noVision: false,
      noVisionModels: [],
      visionFallbackModel: ''
    })

    expect(result.baseUrl).toBe('https://api.example.com')
    expect(result.baseUrls).toEqual([
      'https://api.example.com',
      'https://api.example.com/v1#',
      'https://backup.example.com'
    ])
  })

  it('应将根域名与默认版本前缀 URL 去重为最短形式', () => {
    const result = buildChannelPayload({
      name: 'multi',
      serviceType: 'openai',
      baseUrl: '',
      baseUrls: ['https://new.timefiles.online/v1', 'https://new.timefiles.online'],
      website: '',
      insecureSkipVerify: false,
      lowQuality: false,
      injectDummyThoughtSignature: false,
      stripThoughtSignature: false,
      description: '',
      apiKeys: ['sk-1'],
      modelMapping: {},
      reasoningMapping: {},
      reasoningParamStyle: 'reasoning',
      textVerbosity: '',
      fastMode: false,
      customHeaders: {},
      proxyUrl: '',
      routePrefix: '',
      supportedModels: [],
      autoBlacklistBalance: true,
      normalizeMetadataUserId: true,
      normalizeSystemRoleToTopLevel: false,
      codexToolCompat: true,
      noVision: false,
      noVisionModels: [],
      visionFallbackModel: ''
    })

    expect(result.baseUrl).toBe('https://new.timefiles.online')
    expect(result.baseUrls).toBeUndefined()
  })

  it('应保留带 # 的 URL 与普通 URL 分离', () => {
    const result = buildChannelPayload({
      name: 'multi',
      serviceType: 'openai',
      baseUrl: '',
      baseUrls: ['https://new.timefiles.online/v1', 'https://new.timefiles.online#'],
      website: '',
      insecureSkipVerify: false,
      lowQuality: false,
      injectDummyThoughtSignature: false,
      stripThoughtSignature: false,
      description: '',
      apiKeys: ['sk-1'],
      modelMapping: {},
      reasoningMapping: {},
      reasoningParamStyle: 'reasoning',
      textVerbosity: '',
      fastMode: false,
      customHeaders: {},
      proxyUrl: '',
      routePrefix: '',
      supportedModels: [],
      autoBlacklistBalance: true,
      normalizeMetadataUserId: true,
      normalizeSystemRoleToTopLevel: false,
      codexToolCompat: true,
      noVision: false,
      noVisionModels: [],
      visionFallbackModel: ''
    })

    expect(result.baseUrl).toBe('https://new.timefiles.online')
    expect(result.baseUrls).toEqual(['https://new.timefiles.online', 'https://new.timefiles.online#'])
  })

  it('应为 claude 渠道保留模型级思考强度并清空 OpenAI 专属高级参数', () => {
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
      reasoningParamStyle: 'reasoning_effort',
      textVerbosity: 'high',
      fastMode: true,
      customHeaders: {},
      proxyUrl: '',
      routePrefix: '',
      supportedModels: ['opus'],
      autoBlacklistBalance: true,
      normalizeMetadataUserId: true,
      normalizeSystemRoleToTopLevel: false,
      codexToolCompat: true,
      noVision: false,
      noVisionModels: [],
      visionFallbackModel: ''
    })

    expect(result.modelMapping).toEqual({ opus: 'claude-3-7-sonnet' })
    expect(result.reasoningMapping).toEqual({ opus: 'high' })
    expect(result.reasoningParamStyle).toBe('reasoning')
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
      reasoningParamStyle: 'reasoning',
      textVerbosity: '',
      fastMode: false,
      customHeaders: {},
      proxyUrl: '',
      routePrefix: '',
      supportedModels: [],
      autoBlacklistBalance: false,
      normalizeMetadataUserId: true,
      normalizeSystemRoleToTopLevel: false,
      codexToolCompat: true,
      noVision: false,
      noVisionModels: [],
      visionFallbackModel: ''
    })

    expect(result.autoBlacklistBalance).toBe(false)
  })

  it('应仅在 Claude 协议保留 normalizeMetadataUserId 开关', () => {
    const form = {
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
      reasoningParamStyle: 'reasoning',
      textVerbosity: '',
      fastMode: false,
      customHeaders: {},
      proxyUrl: '',
      routePrefix: '',
      supportedModels: [],
      autoBlacklistBalance: true,
      normalizeMetadataUserId: true,
      normalizeSystemRoleToTopLevel: false,
      codexToolCompat: true,
      noVision: false,
      noVisionModels: [],
      visionFallbackModel: ''
    } satisfies Parameters<typeof buildChannelPayload>[0]

    const responsesResult = buildChannelPayload(form, { channelType: 'responses' })
    const messagesResult = buildChannelPayload(
      { ...form, serviceType: 'claude' },
      { channelType: 'messages' }
    )

    expect(responsesResult.normalizeMetadataUserId).toBe(false)
    expect(messagesResult.normalizeMetadataUserId).toBe(true)
  })

  it('应携带 stripBillingHeader 开关', () => {
    const result = buildChannelPayload({
      name: 'claude-cch-strip',
      serviceType: 'claude',
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
      reasoningParamStyle: 'reasoning',
      textVerbosity: '',
      fastMode: false,
      customHeaders: {},
      proxyUrl: '',
      routePrefix: '',
      supportedModels: [],
      autoBlacklistBalance: true,
      normalizeMetadataUserId: true,
      stripBillingHeader: true,
      normalizeSystemRoleToTopLevel: false,
      codexToolCompat: true,
      noVision: false,
      noVisionModels: [],
      visionFallbackModel: ''
    })

    expect(result.stripBillingHeader).toBe(true)
  })

  it('应携带 normalizeSystemRoleToTopLevel 开关', () => {
    const result = buildChannelPayload({
      name: 'claude-system-normalize',
      serviceType: 'claude',
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
      reasoningParamStyle: 'reasoning',
      textVerbosity: '',
      fastMode: false,
      customHeaders: {},
      proxyUrl: '',
      routePrefix: '',
      supportedModels: [],
      autoBlacklistBalance: true,
      normalizeMetadataUserId: true,
      normalizeSystemRoleToTopLevel: true,
      codexToolCompat: true,
      noVision: false,
      noVisionModels: [],
      visionFallbackModel: ''
    })

    expect(result.normalizeSystemRoleToTopLevel).toBe(true)
  })

  it('空请求超时不写入 payload，继承全局配置', () => {
    const result = buildChannelPayload({
      name: 'inherit-timeout',
      serviceType: 'openai',
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
      reasoningParamStyle: 'reasoning',
      textVerbosity: '',
      fastMode: false,
      customHeaders: {},
      proxyUrl: '',
      requestTimeoutMs: null,
      responseHeaderTimeoutMs: null,
      routePrefix: '',
      supportedModels: [],
      autoBlacklistBalance: true,
      normalizeMetadataUserId: true,
      normalizeSystemRoleToTopLevel: false,
      codexToolCompat: true,
      noVision: false,
      noVisionModels: [],
      visionFallbackModel: ''
    })

    expect(result.requestTimeoutMs).toBeUndefined()
    expect(result.responseHeaderTimeoutMs).toBeUndefined()
  })

  it('超出上限的请求生命周期超时不写入 payload', () => {
    const result = buildChannelPayload({
      name: 'invalid-timeout',
      serviceType: 'openai',
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
      reasoningParamStyle: 'reasoning',
      textVerbosity: '',
      fastMode: false,
      customHeaders: {},
      proxyUrl: '',
      requestTimeoutMs: 301000,
      responseHeaderTimeoutMs: 301000,
      routePrefix: '',
      supportedModels: [],
      autoBlacklistBalance: true,
      normalizeMetadataUserId: true,
      normalizeSystemRoleToTopLevel: false,
      codexToolCompat: true,
      noVision: false,
      noVisionModels: [],
      visionFallbackModel: ''
    })

    expect(result.requestTimeoutMs).toBeUndefined()
    expect(result.responseHeaderTimeoutMs).toBeUndefined()
  })

  it('应清洗 modelMapping 中的对象值为字符串', () => {
    const result = buildChannelPayload({
      name: 'test',
      serviceType: 'claude',
      baseUrl: 'https://api.example.com',
      baseUrls: [],
      website: '',
      insecureSkipVerify: false,
      lowQuality: false,
      injectDummyThoughtSignature: false,
      stripThoughtSignature: false,
      description: '',
      apiKeys: ['sk-1'],
      // v-combobox 选中下拉后可能产生对象值
      modelMapping: {
        'fable': 'claude-3-5-sonnet',
        'haiku': { title: 'claude-3-5-haiku', value: 'claude-3-5-haiku' }
      },
      reasoningMapping: {},
      reasoningParamStyle: 'reasoning',
      textVerbosity: '',
      fastMode: false,
      customHeaders: {},
      proxyUrl: '',
      routePrefix: '',
      supportedModels: [],
      autoBlacklistBalance: true,
      normalizeMetadataUserId: true,
      normalizeSystemRoleToTopLevel: false,
      codexToolCompat: false,
      noVision: false,
      noVisionModels: [],
      visionFallbackModel: ''
    })

    // 确保所有 modelMapping 值都是字符串
    expect(result.modelMapping).toEqual({
      'fable': 'claude-3-5-sonnet',
      'haiku': 'claude-3-5-haiku'
    })
    expect(result.modelMapping).toBeDefined()
    expect(typeof result.modelMapping!.fable).toBe('string')
    expect(typeof result.modelMapping!.haiku).toBe('string')
  })

  it('应为 Vectors 渠道序列化 embeddingCapabilities', () => {
    const result = buildChannelPayload({
      name: 'vectors-channel',
      serviceType: 'openai',
      baseUrl: 'https://api.example.com/v1',
      baseUrls: [],
      website: '',
      insecureSkipVerify: false,
      lowQuality: false,
      injectDummyThoughtSignature: false,
      stripThoughtSignature: false,
      description: '',
      apiKeys: ['sk-1'],
      modelMapping: { 'embed-public': 'embedding-model-a' },
      embeddingCapabilityRows: [
        {
          id: 1,
          model: 'embedding-model-a',
          embeddingSpaceId: 'shared-space',
          dimensions: 1536,
          supportedDimensionsText: '1024, 1536, 1024',
          normalized: 'false',
        },
      ],
      reasoningMapping: {},
      reasoningParamStyle: 'reasoning',
      textVerbosity: '',
      fastMode: false,
      customHeaders: {},
      proxyUrl: '',
      routePrefix: '',
      supportedModels: ['embed-public'],
      autoBlacklistBalance: true,
      normalizeMetadataUserId: true,
      normalizeSystemRoleToTopLevel: false,
      codexToolCompat: false,
      noVision: false,
      noVisionModels: [],
      visionFallbackModel: ''
    }, { channelType: 'vectors' })

    expect(result.embeddingCapabilities).toEqual({
      'embedding-model-a': {
        embeddingSpaceId: 'shared-space',
        dimensions: 1536,
        supportedDimensions: [1024, 1536],
        normalized: false,
      },
    })
  })

  it('非 Vectors 渠道不应写出 embeddingCapabilities', () => {
    const result = buildChannelPayload({
      name: 'chat-channel',
      serviceType: 'openai',
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
      embeddingCapabilityRows: [
        {
          id: 1,
          model: 'embedding-model-a',
          embeddingSpaceId: 'shared-space',
          dimensions: 1536,
          supportedDimensionsText: '',
          normalized: 'true',
        },
      ],
      reasoningMapping: {},
      reasoningParamStyle: 'reasoning',
      textVerbosity: '',
      fastMode: false,
      customHeaders: {},
      proxyUrl: '',
      routePrefix: '',
      supportedModels: [],
      autoBlacklistBalance: true,
      normalizeMetadataUserId: true,
      normalizeSystemRoleToTopLevel: false,
      codexToolCompat: false,
      noVision: false,
      noVisionModels: [],
      visionFallbackModel: ''
    }, { channelType: 'chat' })

    expect(result.embeddingCapabilities).toBeUndefined()
  })

  it('应跳过只有模型名的空白 Embedding 兼容性行', () => {
    const result = buildChannelPayload({
      name: 'vectors-channel',
      serviceType: 'openai',
      baseUrl: 'https://api.example.com/v1',
      baseUrls: [],
      website: '',
      insecureSkipVerify: false,
      lowQuality: false,
      injectDummyThoughtSignature: false,
      stripThoughtSignature: false,
      description: '',
      apiKeys: ['sk-1'],
      modelMapping: { 'embed-public': 'embedding-model-a' },
      embeddingCapabilityRows: [
        {
          id: 1,
          model: 'embedding-model-a',
          embeddingSpaceId: '',
          dimensions: null,
          supportedDimensionsText: '',
          normalized: '',
        },
      ],
      reasoningMapping: {},
      reasoningParamStyle: 'reasoning',
      textVerbosity: '',
      fastMode: false,
      customHeaders: {},
      proxyUrl: '',
      routePrefix: '',
      supportedModels: ['embed-public'],
      autoBlacklistBalance: true,
      normalizeMetadataUserId: true,
      normalizeSystemRoleToTopLevel: false,
      codexToolCompat: false,
      noVision: false,
      noVisionModels: [],
      visionFallbackModel: ''
    }, { channelType: 'vectors' })

    expect(result.embeddingCapabilities).toEqual({})
  })

  it('应保留仅填写 normalized=false 的 Embedding 兼容性行', () => {
    expect(embeddingCapabilityRowsToRecord([
      {
        id: 1,
        model: 'embedding-model-a',
        embeddingSpaceId: '',
        dimensions: null,
        supportedDimensionsText: '',
        normalized: 'false',
      },
    ])).toEqual({
      'embedding-model-a': {
        normalized: false,
      },
    })
  })

  it('应拒绝非法 Embedding 兼容性行', () => {
    expect(embeddingCapabilityRowsToRecord([
      {
        id: 1,
        model: 'embedding-model-a',
        embeddingSpaceId: '',
        dimensions: 0,
        supportedDimensionsText: '',
        normalized: '',
      },
    ])).toBeNull()

    expect(embeddingCapabilityRowsToRecord([
      {
        id: 2,
        model: '',
        embeddingSpaceId: 'space',
        dimensions: null,
        supportedDimensionsText: '',
        normalized: '',
      },
    ])).toBeNull()
  })
  it('应完整保留 Key 服务端元数据、扩展字段及 nullable multiplier', () => {
    const metadata = {
      key: '  sk-preserved  ', keyUid: 'key-uid', credentialUid: 'credential-uid',
      groupMultiplier: null, maxGroupMultiplier: 0, multiplierSource: 'new_api' as const,
      multiplierUpdatedAt: '2026-08-01T00:00:00Z', multiplierExpiresAt: '2026-09-01T00:00:00Z',
      multiplierSyncStatus: 'sync_error', multiplierSyncError: 'timeout', sourceSubscriptionUid: 'sub-uid',
      sourceRemoteTokenId: 42, eligible: false, ineligibleReason: 'expired', futureField: { nested: true },
    }
    const result = buildChannelPayload({
      name: 'keys', serviceType: 'openai', baseUrl: 'https://api.example.com', baseUrls: [], website: '',
      insecureSkipVerify: false, lowQuality: false, injectDummyThoughtSignature: false, stripThoughtSignature: false,
      description: '', apiKeys: ['sk-preserved'], apiKeyConfigs: [metadata], modelMapping: {}, reasoningMapping: {},
      reasoningParamStyle: 'reasoning', textVerbosity: '', fastMode: false, customHeaders: {}, proxyUrl: '',
      routePrefix: '', supportedModels: [], autoBlacklistBalance: true, normalizeMetadataUserId: true,
      normalizeSystemRoleToTopLevel: false, codexToolCompat: false, noVision: false, noVisionModels: [], visionFallbackModel: '',
    })

    expect(result.apiKeyConfigs).toEqual([{ ...metadata, key: 'sk-preserved' }])
    expect(result.apiKeyConfigs?.[0]).toHaveProperty('groupMultiplier', null)
    expect(result.apiKeyConfigs?.[0]).toHaveProperty('maxGroupMultiplier', 0)
  })

  it('删除 Key 时仅过滤目标配置，其他 Key 配置保持不变', () => {
    const untouched = { key: 'key-b', keyUid: 'uid-b', groupMultiplier: null, unknownMetadata: 'keep' }
    const result = buildChannelPayload({
      name: 'keys', serviceType: 'openai', baseUrl: 'https://api.example.com', baseUrls: [], website: '',
      insecureSkipVerify: false, lowQuality: false, injectDummyThoughtSignature: false, stripThoughtSignature: false,
      description: '', apiKeys: ['key-b'], apiKeyConfigs: [
        { key: 'key-a', keyUid: 'uid-a', groupMultiplier: 2 }, untouched,
      ], modelMapping: {}, reasoningMapping: {}, reasoningParamStyle: 'reasoning', textVerbosity: '', fastMode: false,
      customHeaders: {}, proxyUrl: '', routePrefix: '', supportedModels: [], autoBlacklistBalance: true,
      normalizeMetadataUserId: true, normalizeSystemRoleToTopLevel: false, codexToolCompat: false,
      noVision: false, noVisionModels: [], visionFallbackModel: '',
    })

    expect(result.apiKeyConfigs).toEqual([untouched])
  })

  it('应区分 multiplier 的 undefined、null 与 0，并保留 KeyUID-only 身份骨架', () => {
    const result = buildChannelPayload({
      name: 'keys', serviceType: 'openai', baseUrl: 'https://api.example.com', baseUrls: [], website: '',
      insecureSkipVerify: false, lowQuality: false, injectDummyThoughtSignature: false, stripThoughtSignature: false,
      description: '', apiKeys: ['key-a', 'key-b'], apiKeyConfigs: [
        { key: 'key-a', groupMultiplier: undefined, maxGroupMultiplier: null },
        { key: 'key-b', groupMultiplier: 0, maxGroupMultiplier: 0 },
        { key: '', keyUid: 'uid-only', credentialUid: 'credential-only' },
      ], modelMapping: {}, reasoningMapping: {}, reasoningParamStyle: 'reasoning', textVerbosity: '', fastMode: false,
      customHeaders: {}, proxyUrl: '', routePrefix: '', supportedModels: [], autoBlacklistBalance: true,
      normalizeMetadataUserId: true, normalizeSystemRoleToTopLevel: false, codexToolCompat: false,
      noVision: false, noVisionModels: [], visionFallbackModel: '',
    })

    expect(result.apiKeyConfigs).toEqual([
      { key: 'key-a', groupMultiplier: undefined, maxGroupMultiplier: null },
      { key: 'key-b', groupMultiplier: 0, maxGroupMultiplier: 0 },
      { key: '', keyUid: 'uid-only', credentialUid: 'credential-only' },
    ])
  })
})

describe('buildChannelPayload tags', () => {
  const baseForm = {
    name: 'test',
    serviceType: 'openai' as const,
    baseUrl: 'https://api.example.com',
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
    reasoningParamStyle: 'reasoning' as const,
    textVerbosity: '' as const,
    fastMode: false,
    customHeaders: {},
    proxyUrl: '',
    routePrefix: '',
    supportedModels: [] as string[],
    autoBlacklistBalance: true,
    normalizeMetadataUserId: true,
    normalizeSystemRoleToTopLevel: false,
    codexToolCompat: false,
    noVision: false,
    noVisionModels: [] as string[],
    visionFallbackModel: '',
  }

  it('应包含非空 tags', () => {
    const result = buildChannelPayload({ ...baseForm, tags: ['prod', 'primary'] })
    expect(result.tags).toEqual(['prod', 'primary'])
  })

  it('空 tags 应返回空数组', () => {
    const result = buildChannelPayload({ ...baseForm, tags: [] })
    expect(result.tags).toEqual([])
  })

  it('undefined tags 应返回空数组', () => {
    const result = buildChannelPayload({ ...baseForm })
    expect(result.tags).toEqual([])
  })

  it('tags 应裁剪空白', () => {
    const result = buildChannelPayload({ ...baseForm, tags: ['  prod  ', ' primary '] })
    expect(result.tags).toEqual(['prod', 'primary'])
  })

  it('空字符串 tag 应被过滤', () => {
    const result = buildChannelPayload({ ...baseForm, tags: ['valid', '', '  ', 'also'] })
    expect(result.tags).toEqual(['valid', 'also'])
  })
})
