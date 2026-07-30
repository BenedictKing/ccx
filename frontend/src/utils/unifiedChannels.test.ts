import { describe, expect, it } from 'vitest'

import type { Channel, ChannelRecentActivity, ChannelsResponse } from '@/services/api'
import { buildUnifiedChannelsData, buildUnifiedRecentActivity, buildUnifiedReorderPayloads, resolveChannelRecoveryRoutes, type LlmChannelKind } from './unifiedChannels'

const channel = (
  name: string,
  accountUid: string,
  index: number,
  apiKeys: string[],
  overrides: Partial<Channel> = {},
): Channel => ({
  name,
  accountUid,
  channelUid: `ch-${index}`,
  providerId: 'mimo',
  autoManaged: true,
  index,
  serviceType: name.endsWith('-claude') ? 'claude' : 'openai',
  baseUrl: 'https://example.com',
  apiKeys,
  ...overrides,
})

const response = (channels: Channel[]): ChannelsResponse => ({ channels, current: -1 })

describe('buildUnifiedChannelsData account grouping', () => {
  it('优先按 accountUid 聚合多协议渠道，不依赖 Key 指纹', () => {
    const data: Record<LlmChannelKind, ChannelsResponse> = {
      messages: response([channel('mimo-main-claude', 'acct-main', 0, ['sk-a'])]),
      chat: response([channel('mimo-main-chat', 'acct-main', 1, ['sk-a', 'sk-b'])]),
      responses: response([channel('mimo-main-codex', 'acct-main', 2, ['sk-b'])]),
      gemini: response([channel('mimo-main-gemini', 'acct-main', 3, ['sk-a'])]),
    }

    const result = buildUnifiedChannelsData(data)
    expect(result.channels).toHaveLength(1)
    expect(result.channels[0].accountUid).toBe('acct-main')
    expect(result.channels[0].protocolCapsules?.map(item => item.label)).toEqual(['CLAUDE', 'CHAT'])
    expect(result.channels[0].protocolRoutes?.map(item => item.kind)).toEqual(['messages', 'chat', 'responses', 'gemini'])
    expect(result.channels[0].protocolRoutes?.map(item => item.status)).toEqual([undefined, undefined, undefined, undefined])
  })

  it('聚合账号合并各协议凭证并按全部路由计算状态', () => {
    const data: Record<LlmChannelKind, ChannelsResponse> = {
      messages: response([channel('kimi-claude', 'acct-kimi', 0, [], {
        status: 'suspended',
      })]),
      chat: response([channel('kimi-chat', 'acct-kimi', 1, ['sk-kimi', 'sk-disabled'], {
        status: 'active',
        apiKeyConfigs: [{ key: 'sk-kimi', credentialUid: 'cred-kimi' }],
        disabledApiKeys: [{
          key: 'sk-disabled',
          reason: 'authentication_error',
          message: 'invalid key',
          disabledAt: '2026-07-28T00:00:00Z',
        }],
      })]),
      responses: response([]),
      gemini: response([]),
    }

    const [logicalChannel] = buildUnifiedChannelsData(data).channels
    expect(logicalChannel.apiKeys).toEqual(['sk-kimi', 'sk-disabled'])
    expect(logicalChannel.apiKeyConfigs).toEqual([
      { key: 'sk-kimi', credentialUid: 'cred-kimi' },
    ])
    expect(logicalChannel.disabledApiKeys).toEqual([{
      key: 'sk-disabled',
      reason: 'authentication_error',
      message: 'invalid key',
      disabledAt: '2026-07-28T00:00:00Z',
    }])
    expect(logicalChannel.status).toBe('active')
  })

  it('保留各协议路由状态供聚合渠道恢复使用', () => {
    const data: Record<LlmChannelKind, ChannelsResponse> = {
      messages: response([channel('mimo-main-claude', 'acct-main', 0, ['sk-a'], { status: 'suspended' })]),
      chat: response([channel('mimo-main-chat', 'acct-main', 1, ['sk-a'], { status: 'active' })]),
      responses: response([channel('mimo-main-codex', 'acct-main', 2, ['sk-a'], { status: 'disabled' })]),
      gemini: response([]),
    }

    const logicalChannel = buildUnifiedChannelsData(data).channels[0]
    expect(logicalChannel.protocolRoutes?.map(route => route.status)).toEqual([
      'suspended',
      'active',
      'disabled',
    ])
    expect(resolveChannelRecoveryRoutes(logicalChannel)).toEqual([
      { kind: 'messages', index: 0, status: 'suspended' },
      { kind: 'chat', index: 1, status: 'active' },
      { kind: 'responses', index: 2, status: 'disabled' },
    ])
  })


  it('相同 provider 和名称下不同 accountUid 不应合并', () => {
    const data: Record<LlmChannelKind, ChannelsResponse> = {
      messages: response([
        channel('mimo-main-claude', 'acct-a', 0, ['sk-a']),
        channel('mimo-main-claude', 'acct-b', 1, ['sk-b']),
      ]),
      chat: response([]),
      responses: response([]),
      gemini: response([]),
    }

    expect(buildUnifiedChannelsData(data).channels).toHaveLength(2)
  })

  it('按 accountUid 聚合自定义自动托管的全部成功协议', () => {
    const custom = (name: string, index: number, serviceType: Channel['serviceType']): Channel =>
      channel(name, 'acct-fastaitoken', index, ['sk-fastaitoken'], { providerId: '', autoManaged: true, serviceType })
    const data: Record<LlmChannelKind, ChannelsResponse> = {
      messages: response([{
        ...custom('fastaitoken-com-test-claude', 0, 'claude'),
        supportedModels: ['gpt-5.6-sol', 'gpt-5.6-terra'],
      }]),
      chat: response([{
        ...custom('fastaitoken-com-test-chat', 0, 'openai'),
        supportedModels: ['gpt-5.6-sol'],
      }]),
      responses: response([{
        ...custom('fastaitoken-com-test-codex', 0, 'responses'),
        supportedModels: ['codex-auto-review', 'gpt-5.6-sol'],
      }]),
      gemini: response([]),
    }

    const result = buildUnifiedChannelsData(data)
    expect(result.channels).toHaveLength(1)
    expect(result.channels[0].name).toBe('fastaitoken-com-test')
    expect(result.channels[0].protocolCapsules?.map(item => item.label)).toEqual(['CLAUDE', 'CHAT', 'CODEX'])
    expect(result.channels[0].protocolRoutes?.map(item => item.kind)).toEqual(['messages', 'chat', 'responses'])
    expect(Object.fromEntries(
      result.channels[0].protocolRoutes?.map(route => [route.kind, route.supportedModels]) ?? [],
    )).toEqual({
      messages: ['gpt-5.6-sol', 'gpt-5.6-terra'],
      chat: ['gpt-5.6-sol'],
      responses: ['codex-auto-review', 'gpt-5.6-sol'],
    })
  })

  it('协议标签只展示上游实际提供的 serviceType', () => {
    const data: Record<LlmChannelKind, ChannelsResponse> = {
      messages: response([channel('volcengine-claude', 'acct-volcengine', 0, ['ark-key'], {
        providerId: 'volcengine',
        serviceType: 'claude',
      })]),
      chat: response([channel('volcengine-chat', 'acct-volcengine', 0, ['ark-key'], {
        providerId: 'volcengine',
        serviceType: 'openai',
      })]),
      responses: response([channel('volcengine-codex', 'acct-volcengine', 0, ['ark-key'], {
        providerId: 'volcengine',
        serviceType: 'openai',
      })]),
      gemini: response([channel('volcengine-gemini', 'acct-volcengine', 0, ['ark-key'], {
        providerId: 'volcengine',
        serviceType: 'openai',
      })]),
    }

    const [volcengine] = buildUnifiedChannelsData(data).channels
    expect(volcengine.protocolCapsules?.map(item => item.label)).toEqual(['CLAUDE', 'CHAT'])
    expect(volcengine.protocolRoutes).toHaveLength(4)
    expect(volcengine.protocolRoutes?.every(route => route.supportedModels === undefined)).toBe(true)
  })

  it('新增单协议渠道置顶后保持多协议账号的既有相对顺序', () => {
    const data: Record<LlmChannelKind, ChannelsResponse> = {
      messages: response([
        channel('localhost-37zq4d', 'acct-local', 0, ['sk-local'], { providerId: '', priority: 0 }),
        channel('volcengine-claude', 'acct-volcengine', 1, ['sk-volcengine'], { providerId: 'volcengine', priority: 1 }),
        channel('mimo-claude', 'acct-mimo', 2, ['sk-mimo'], { priority: 2 }),
      ]),
      chat: response([
        channel('volcengine-chat', 'acct-volcengine', 0, ['sk-volcengine'], { providerId: 'volcengine', priority: 0 }),
        channel('mimo-chat', 'acct-mimo', 1, ['sk-mimo'], { priority: 1 }),
        channel('desktop-deepseek-chat', 'acct-deepseek', 34, ['sk-deepseek'], {
          autoManaged: false,
          providerId: '',
          priority: 1,
        }),
      ]),
      responses: response([
        channel('volcengine-codex', 'acct-volcengine', 0, ['sk-volcengine'], { providerId: 'volcengine', priority: 0 }),
        channel('mimo-codex', 'acct-mimo', 1, ['sk-mimo'], { priority: 1 }),
        channel('aixoras-xanqfm', 'acct-aixoras', 2, ['sk-aixoras'], {
          autoManaged: false,
          providerId: '',
          priority: 1,
        }),
      ]),
      gemini: response([
        channel('volcengine-gemini', 'acct-volcengine', 0, ['sk-volcengine'], { providerId: 'volcengine', priority: 0 }),
        channel('mimo-gemini', 'acct-mimo', 1, ['sk-mimo'], { priority: 1 }),
      ]),
    }

    const channels = buildUnifiedChannelsData(data).channels
    const sorted = [...channels].sort((a, b) => (a.priority ?? a.index) - (b.priority ?? b.index))

    expect(sorted.slice(0, 5).map(item => item.name)).toEqual([
      'localhost-37zq4d',
      'volcengine',
      'mimo',
      'desktop-deepseek-chat',
      'aixoras-xanqfm',
    ])
    expect(channels.find(item => item.name === 'mimo')?.priority).toBe(1)
  })

  it('在列表头部插入渠道时保持既有逻辑渠道的展示 key 稳定', () => {
    const original: Record<LlmChannelKind, ChannelsResponse> = {
      messages: response([
        channel('volcengine-claude', 'acct-volcengine', 0, ['sk-volcengine'], { providerId: 'volcengine' }),
        channel('mimo-claude', 'acct-mimo', 1, ['sk-mimo']),
      ]),
      chat: response([]),
      responses: response([]),
      gemini: response([]),
    }
    const withNewChannel: Record<LlmChannelKind, ChannelsResponse> = {
      ...original,
      messages: response([
        channel('localhost-37zq4d', 'acct-local', 0, ['sk-local'], { providerId: '' }),
        ...original.messages.channels,
      ]),
    }

    const originalMimo = buildUnifiedChannelsData(original).channels.find(item => item.name === 'mimo')
    const nextMimo = buildUnifiedChannelsData(withNewChannel).channels.find(item => item.name === 'mimo')

    expect(nextMimo?.displayKey).toBe(originalMimo?.displayKey)
  })

  it('聚合逻辑渠道全部协议的最近请求活动', () => {
    const data: Record<LlmChannelKind, ChannelsResponse> = {
      messages: response([channel('mimo-claude', 'acct-mimo', 0, ['sk-mimo'])]),
      chat: response([channel('mimo-chat', 'acct-mimo', 1, ['sk-mimo'])]),
      responses: response([]),
      gemini: response([]),
    }
    const [logicalChannel] = buildUnifiedChannelsData(data).channels
    const activity = (
      channelIndex: number,
      requestCount: number,
      successCount: number,
      failureCount: number,
    ): ChannelRecentActivity => ({
      channelIndex,
      segments: {
        4: { requestCount, successCount, failureCount, inputTokens: requestCount * 10, outputTokens: requestCount * 2 },
      },
      totalSegs: 150,
      rpm: requestCount / 15,
      tpm: requestCount * 2 / 15,
    })

    const [merged] = buildUnifiedRecentActivity([logicalChannel], {
      messages: [activity(0, 2, 2, 0)],
      chat: [activity(1, 3, 2, 1)],
      responses: [],
      gemini: [],
    })

    expect(merged.channelIndex).toBe(0)
    expect(merged.routeKind).toBe('messages')
    expect(merged.segments[4]).toEqual({
      requestCount: 5,
      successCount: 4,
      failureCount: 1,
      inputTokens: 50,
      outputTokens: 10,
    })
    expect(merged.rpm).toBeCloseTo(5 / 15)
    expect(merged.tpm).toBeCloseTo(10 / 15)
  })
})


describe('buildUnifiedReorderPayloads', () => {
  it('priority 使用统一列表全局位次而非各协议组内名次', () => {
    const data: Record<LlmChannelKind, ChannelsResponse> = {
      messages: response([
        channel('a-claude', 'acct-a', 0, ['sk-a']),
        channel('b-claude', 'acct-b', 1, ['sk-b']),
      ]),
      chat: response([]),
      responses: response([]),
      gemini: response([channel('g-gemini', 'acct-g', 0, ['sk-g'], { serviceType: 'gemini' })]),
    }
    const unified = buildUnifiedChannelsData(data).channels
    const a = unified.find(c => c.protocolRoutes?.some(r => r.kind === 'messages' && r.index === 0))!
    const b = unified.find(c => c.protocolRoutes?.some(r => r.kind === 'messages' && r.index === 1))!
    const g = unified.find(c => c.protocolRoutes?.some(r => r.kind === 'gemini'))!

    // 模拟拖拽后的统一列表顺序：a, g, b
    const payloads = buildUnifiedReorderPayloads([a, g, b])

    // messages：a 位次 1，b 位次 3（中间隔着 gemini 渠道）
    expect(payloads.get('messages')).toEqual({ order: [0, 1], priorities: [1, 3] })
    // gemini：g 位次 2。若按组内名次编号会得到 1，刷新后 min(priority) 会把 g 提到最前
    expect(payloads.get('gemini')).toEqual({ order: [0], priorities: [2] })
    expect(payloads.has('chat')).toBe(false)
  })

  it('无 protocolRoutes 的渠道不产生载荷', () => {
    const payloads = buildUnifiedReorderPayloads([{ name: 'plain', index: 0 } as Channel])
    expect(payloads.size).toBe(0)
  })
})
