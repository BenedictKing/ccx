import { describe, expect, it, vi } from 'vitest'

import type {
  ApiService,
  Channel,
  ChannelModelsRequest,
  ChannelProtocolRoute,
  ManagedAccountChannel,
  ModelsResponse,
} from '@/services/api'
import { buildNativeProtocolModelRoutes, loadLegacyManagedModelAvailability } from './channelModelAvailability'

const modelsResponse = (models: string[]): ModelsResponse => ({
  object: 'list',
  data: models.map(id => ({ id, object: 'model', created: 0, owned_by: 'upstream' })),
})

const channel = (value: Partial<Channel> & Pick<Channel, 'name' | 'serviceType' | 'baseUrl' | 'apiKeys' | 'index'>): Channel => ({
  ...value,
}) as Channel

describe('buildNativeProtocolModelRoutes', () => {
  it('火山只展示原生 Messages 与 Chat，并读取各渠道发现模型', () => {
    const routes: ChannelProtocolRoute[] = [
      { kind: 'messages', index: 7, channelUid: 'ch-messages', name: 'volcengine-claude', serviceType: 'claude' },
      { kind: 'chat', index: 6, channelUid: 'ch-chat', name: 'volcengine-chat', serviceType: 'openai' },
      { kind: 'responses', index: 4, channelUid: 'ch-responses', name: 'volcengine-codex', serviceType: 'openai' },
      { kind: 'gemini', index: 0, channelUid: 'ch-gemini', name: 'volcengine-gemini', serviceType: 'openai' },
    ]
    const channels: ManagedAccountChannel[] = [
      {
        kind: 'messages', channelUid: 'ch-messages', name: 'volcengine-claude', serviceType: 'claude', status: 'active',
        modelInventoryKnown: true,
        discoveredModels: ['glm-5.2', 'deepseek-v4-pro'],
        modelsDiscoveredAt: '2026-07-22T00:42:12Z',
        modelDiscoverySource: 'control_plane',
        modelDiscoveryMessage: '火山管控面 Coding Plan 模型清单',
      },
      {
        kind: 'chat', channelUid: 'ch-chat', name: 'volcengine-chat', serviceType: 'openai', status: 'active',
        modelInventoryKnown: true,
        discoveredModels: ['glm-5.2'],
      },
    ]

    const result = buildNativeProtocolModelRoutes(routes, channels)

    expect(result.map(route => route.kind)).toEqual(['messages', 'chat'])
    expect(result.map(route => route.upstreamKind)).toEqual(['messages', 'chat'])
    expect(result[0].discoveredModels).toEqual(['glm-5.2', 'deepseek-v4-pro'])
    expect(result[0].modelInventoryKnown).toBe(true)
    expect(result[0].modelsDiscoveredAt).toBe('2026-07-22T00:42:12Z')
    expect(result[0].modelDiscoverySource).toBe('control_plane')
    expect(result[1].discoveredModels).toEqual(['glm-5.2'])
  })

  it('缺少同名客户端路由时仍按 serviceType 显示真实上游协议', () => {
    const result = buildNativeProtocolModelRoutes([
      { kind: 'responses', index: 1, channelUid: 'ch-only', name: 'chat-through-responses', serviceType: 'openai' },
    ], [])

    expect(result).toHaveLength(1)
    expect(result[0].kind).toBe('responses')
    expect(result[0].upstreamKind).toBe('chat')
  })

  it('兼容旧 chat 别名，并将 Copilot 归为 Responses 上游', () => {
    const result = buildNativeProtocolModelRoutes([
      { kind: 'responses', index: 1, channelUid: 'ch-chat', name: 'legacy-chat', serviceType: ' CHAT ' },
      { kind: 'gemini', index: 2, channelUid: 'ch-copilot', name: 'copilot-through-gemini', serviceType: 'copilot' },
    ], [])

    expect(result.map(route => route.kind)).toEqual(['responses', 'gemini'])
    expect(result.map(route => route.upstreamKind)).toEqual(['chat', 'responses'])
  })

  it('展开后台发现但尚未配置的原生协议', () => {
    const result = buildNativeProtocolModelRoutes([
      { kind: 'responses', index: 2, channelUid: 'ch-responses', name: 'multi-protocol', serviceType: 'responses' },
    ], [{
      kind: 'responses',
      channelUid: 'ch-responses',
      name: 'multi-protocol',
      serviceType: 'responses',
      status: 'active',
      protocolAvailability: [
        { protocol: 'responses', modelInventoryKnown: true, discoveredModels: ['gpt-5.4'] },
        { protocol: 'messages', modelInventoryKnown: true, discoveredModels: ['claude-sonnet-4-6'] },
        { protocol: 'chat', modelInventoryKnown: true, discoveredModels: ['gpt-5.4'] },
      ],
    }])

    expect(result.map(route => route.upstreamKind)).toEqual(['messages', 'chat', 'responses'])
    expect(result.find(route => route.upstreamKind === 'responses')?.configured).toBe(true)
    expect(result.find(route => route.upstreamKind === 'messages')).toMatchObject({
      configured: false,
      index: -1,
      channelUid: 'ch-responses',
      discoveredModels: ['claude-sonnet-4-6'],
    })
  })

  it('旧后端缺少画像字段时按协议和 Key 回退查询模型', async () => {
    const routes: ChannelProtocolRoute[] = [
      { kind: 'messages', index: 0, channelUid: 'ch-messages', name: 'compshare-claude', serviceType: 'claude' },
      { kind: 'chat', index: 0, channelUid: 'ch-chat', name: 'compshare-chat', serviceType: 'openai' },
      { kind: 'responses', index: 0, channelUid: 'ch-responses', name: 'compshare-codex', serviceType: 'openai' },
    ]
    const accountChannels: ManagedAccountChannel[] = routes.map(route => ({
      kind: route.kind,
      channelUid: route.channelUid!,
      name: route.name,
      serviceType: route.serviceType,
      status: 'active',
    }))
    const messagesKeyA = 'sk-messages-alpha'
    const messagesKeyB = 'sk-messages-bravo'
    const chatKey = 'sk-chat-charlie'
    const getResponsesChannels = vi.fn()
    const api = {
      getChannels: vi.fn().mockResolvedValue({
        current: 0,
        channels: [channel({
          index: 0,
          name: 'compshare-claude',
          channelUid: 'ch-messages',
          serviceType: 'claude',
          baseUrl: 'https://cp.compshare.cn',
          apiKeys: [messagesKeyA, messagesKeyB],
          apiKeyConfigs: [
            { key: messagesKeyA, credentialUid: 'cred-a' },
            { key: messagesKeyB, credentialUid: 'cred-b' },
          ],
        })],
      }),
      getChatChannels: vi.fn().mockResolvedValue({
        current: 0,
        channels: [channel({
          index: 0,
          name: 'compshare-chat',
          channelUid: 'ch-chat',
          serviceType: 'openai',
          baseUrl: 'https://cp.compshare.cn/v1',
          apiKeys: [chatKey],
          apiKeyConfigs: [{ key: chatKey, credentialUid: 'cred-c' }],
        })],
      }),
      getResponsesChannels,
      getGeminiChannels: vi.fn(),
      getImagesChannels: vi.fn(),
      getVectorsChannels: vi.fn(),
      getChannelModels: vi.fn().mockImplementation((_id: number, request: ChannelModelsRequest) => Promise.resolve(
        request.key === messagesKeyA
          ? modelsResponse(['glm-5.2', 'deepseek-v4-flash'])
          : modelsResponse(['glm-5.2', 'MiniMax-M2.7']),
      )),
      getChatChannelModels: vi.fn().mockResolvedValue(modelsResponse(['glm-5.2'])),
      getResponsesChannelModels: vi.fn(),
      getGeminiChannelModels: vi.fn(),
      getImagesChannelModels: vi.fn(),
      getVectorsChannelModels: vi.fn(),
    } as unknown as ApiService

    const result = await loadLegacyManagedModelAvailability(api, routes, accountChannels)
    const messages = result.find(item => item.channelUid === 'ch-messages')
    const chat = result.find(item => item.channelUid === 'ch-chat')

    expect(messages?.modelInventoryKnown).toBe(true)
    expect(messages?.discoveredModels).toHaveLength(3)
    expect(messages?.discoveredModels).toEqual(expect.arrayContaining([
      'glm-5.2',
      'deepseek-v4-flash',
      'MiniMax-M2.7',
    ]))
    expect(messages?.modelBindings?.map(binding => binding.credentialUid)).toEqual(['cred-a', 'cred-b'])
    expect(chat?.discoveredModels).toEqual(['glm-5.2'])
    expect(getResponsesChannels).not.toHaveBeenCalled()
  })

  it('全部 Key 被暂停时仍回退查询模型清单', async () => {
    const pausedKey = 'sk-kimi-paused'
    const routes: ChannelProtocolRoute[] = [
      { kind: 'chat', index: 0, channelUid: 'ch-chat', name: 'kimi-chat', serviceType: 'openai' },
    ]
    const api = {
      getChatChannels: vi.fn().mockResolvedValue({
        current: 0,
        channels: [channel({
          index: 0,
          name: 'kimi-chat',
          channelUid: 'ch-chat',
          serviceType: 'openai',
          baseUrl: 'https://api.moonshot.cn/v1',
          apiKeys: [pausedKey],
          apiKeyConfigs: [{ key: pausedKey, credentialUid: 'cred-kimi', enabled: false }],
        })],
      }),
      getChatChannelModels: vi.fn().mockResolvedValue(modelsResponse(['kimi-k2.5'])),
    } as unknown as ApiService

    const result = await loadLegacyManagedModelAvailability(api, routes, [{
      kind: 'chat', channelUid: 'ch-chat', name: 'kimi-chat', serviceType: 'openai', status: 'suspended',
    }])

    expect(result.find(item => item.channelUid === 'ch-chat')).toMatchObject({
      modelInventoryKnown: true,
      discoveredModels: ['kimi-k2.5'],
    })
  })

  it('新版后端已有画像时不重复查询上游', async () => {
    const route: ChannelProtocolRoute = {
      kind: 'messages', index: 0, channelUid: 'ch-messages', name: 'managed-claude', serviceType: 'claude',
    }
    const accountChannels: ManagedAccountChannel[] = [{
      kind: 'messages', channelUid: 'ch-messages', name: 'managed-claude', serviceType: 'claude', status: 'active',
      modelInventoryKnown: true,
      discoveredModels: ['glm-5.2'],
    }]
    const getChannels = vi.fn()

    const result = await loadLegacyManagedModelAvailability(
      { getChannels } as unknown as ApiService,
      [route],
      accountChannels,
    )

    expect(result).toBe(accountChannels)
    expect(getChannels).not.toHaveBeenCalled()
  })
})
