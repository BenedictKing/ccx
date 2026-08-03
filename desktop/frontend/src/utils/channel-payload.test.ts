import { describe, expect, it } from 'vitest'
import type { APIKeyConfig } from '@/services/admin-api'
import { buildChannelPayload } from './channel-payload'

describe('buildChannelPayload', () => {
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

  it('保留服务端 key 元数据、未知扩展字段与 null/0，并自动补齐 skeleton key', () => {
    const cfg: APIKeyConfig = {
      key: ' key-skeleton ',
      keyUid: 'ku_1',
      credentialUid: 'cred_1',
      groupMultiplier: 0,
      maxGroupMultiplier: null,
      multiplierSource: 'new_api',
      multiplierUpdatedAt: '2026-08-01T00:00:00Z',
      multiplierExpiresAt: '',
      multiplierSyncStatus: 'sync_error',
      multiplierSyncError: null as unknown as string,
      sourceSubscriptionUid: 'sub_1',
      sourceRemoteTokenId: 0,
      eligible: false,
      ineligibleReason: 'quota',
      extraPayload: { foo: 'bar' },
      models: [' model-a ', '']
    }

    const result = buildChannelPayload({
      name: 'desktop-channel',
      serviceType: 'openai',
      authHeader: 'auto',
      baseUrl: 'https://example.com',
      baseUrls: ['https://example.com'],
      website: '',
      insecureSkipVerify: false,
      lowQuality: false,
      injectDummyThoughtSignature: false,
      stripThoughtSignature: false,
      description: '',
      apiKeys: [],
      apiKeyConfigs: [cfg],
      modelMapping: {},
      modelCapabilitiesText: '',
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

    expect(result.apiKeys).toEqual(['key-skeleton'])
    expect(result.apiKeyConfigs).toEqual([{
      ...cfg,
      key: 'key-skeleton',
      models: ['model-a'],
      multiplierExpiresAt: '',
      multiplierSyncError: null,
    }])
  })

  it('只过滤被删除目标 key，不污染其他 key 配置', () => {
    const keep: APIKeyConfig = {
      key: 'key-keep',
      keyUid: 'ku_keep',
      credentialUid: 'cred_keep',
      extraFlag: 'keep-me',
    }
    const remove: APIKeyConfig = {
      key: 'key-remove',
      keyUid: 'ku_remove',
      credentialUid: 'cred_remove',
      extraFlag: 'drop-me',
    }

    const result = buildChannelPayload({
      name: 'desktop-channel',
      serviceType: 'openai',
      authHeader: 'auto',
      baseUrl: 'https://example.com',
      baseUrls: ['https://example.com'],
      website: '',
      insecureSkipVerify: false,
      lowQuality: false,
      injectDummyThoughtSignature: false,
      stripThoughtSignature: false,
      description: '',
      apiKeys: ['key-keep'],
      apiKeyConfigs: [keep, remove],
      modelMapping: {},
      modelCapabilitiesText: '',
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

    expect(result.apiKeys).toEqual(['key-keep', 'key-remove'])
    expect(result.apiKeyConfigs).toEqual([keep, remove])
  })
})
