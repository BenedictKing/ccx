import { describe, expect, it } from 'vitest'
import type { Channel } from '../services/api'
import {
  LATENCY_VALID_DURATION,
  freezeImmutableFields,
  mergeChannelsWithLocalData,
} from './channelMerge'

function makeChannel(overrides: Partial<Channel> = {}): Channel {
  return {
    name: 'test',
    serviceType: 'openai',
    baseUrl: 'https://example.com',
    apiKeys: ['key1'],
    index: 0,
    ...overrides,
  }
}

describe('freezeImmutableFields', () => {
  it('冻结 apiKeys 数组', () => {
    const ch = makeChannel({ apiKeys: ['k1', 'k2'] })
    freezeImmutableFields(ch)
    expect(Object.isFrozen(ch.apiKeys)).toBe(true)
  })

  it('冻结 disabledApiKeys 数组（存在时）', () => {
    const ch = makeChannel({ disabledApiKeys: [{ key: 'k1', reason: 'expired', blacklistedAt: 0 } as any] }) // eslint-disable-line @typescript-eslint/no-explicit-any
    freezeImmutableFields(ch)
    expect(Object.isFrozen(ch.disabledApiKeys)).toBe(true)
  })

  it('冻结 modelMapping 对象', () => {
    const ch = makeChannel({ modelMapping: { 'gpt-4': 'gpt-4o' } })
    freezeImmutableFields(ch)
    expect(Object.isFrozen(ch.modelMapping)).toBe(true)
  })

  it('冻结大型配置集合，避免被 Vue 深度代理化', () => {
    const ch = makeChannel({
      supportedModels: ['model-a'],
      tags: ['fast'],
      customHeaders: { 'x-test': '1' },
      protocolRoutes: [{ kind: 'messages', index: 0, name: 'test', serviceType: 'claude' }],
    })

    freezeImmutableFields(ch)

    expect(Object.isFrozen(ch.supportedModels)).toBe(true)
    expect(Object.isFrozen(ch.tags)).toBe(true)
    expect(Object.isFrozen(ch.customHeaders)).toBe(true)
    expect(Object.isFrozen(ch.protocolRoutes)).toBe(true)
  })

  it('未定义字段不抛错', () => {
    const ch = makeChannel()
    expect(() => freezeImmutableFields(ch)).not.toThrow()
  })

  it('已冻结字段不重复冻结（幂等）', () => {
    const keys = Object.freeze(['k1'])
    const ch = makeChannel({ apiKeys: keys as unknown as string[] })
    expect(() => freezeImmutableFields(ch)).not.toThrow()
    expect(Object.isFrozen(ch.apiKeys)).toBe(true)
  })
})

describe('mergeChannelsWithLocalData', () => {
  const NOW = 1_700_000_000_000

  it('existingChannels 为空时，直接返回新数组并冻结字段', () => {
    const newChannels = [
      makeChannel({ index: 0, apiKeys: ['a'], modelMapping: { x: 'y' } }),
      makeChannel({ index: 1, apiKeys: ['b'] }),
    ]
    const result = mergeChannelsWithLocalData(newChannels, undefined, NOW)
    expect(result).toBe(newChannels)
    expect(Object.isFrozen(result[0].apiKeys)).toBe(true)
    expect(Object.isFrozen(result[0].modelMapping)).toBe(true)
    expect(Object.isFrozen(result[1].apiKeys)).toBe(true)
  })

  it('latencyTestTime 在 5 分钟内，保留本地 latency', () => {
    const existing = [
      makeChannel({ index: 0, latency: 42, latencyTestTime: NOW - 60_000 }),
    ]
    const fresh = [makeChannel({ index: 0, latency: undefined })]
    const result = mergeChannelsWithLocalData(fresh, existing, NOW)
    expect(result[0].latency).toBe(42)
    expect(result[0].latencyTestTime).toBe(NOW - 60_000)
  })

  it('latencyTestTime 超过 5 分钟，丢弃本地 latency', () => {
    const existing = [
      makeChannel({ index: 0, latency: 42, latencyTestTime: NOW - LATENCY_VALID_DURATION - 1 }),
    ]
    const fresh = [makeChannel({ index: 0, latency: undefined })]
    const result = mergeChannelsWithLocalData(fresh, existing, NOW)
    expect(result[0].latency).toBeUndefined()
  })

  it('同时保留多个渠道的 latency（按 index 匹配，不受顺序影响）', () => {
    const existing = [
      makeChannel({ index: 5, latency: 100, latencyTestTime: NOW - 1_000 }),
      makeChannel({ index: 2, latency: 50, latencyTestTime: NOW - 1_000 }),
    ]
    const fresh = [
      makeChannel({ index: 2 }),
      makeChannel({ index: 5 }),
      makeChannel({ index: 8 }),
    ]
    const result = mergeChannelsWithLocalData(fresh, existing, NOW)
    expect(result[0].latency).toBe(50)
    expect(result[1].latency).toBe(100)
    expect(result[2].latency).toBeUndefined()
  })

  it('所有返回的 channel 都冻结了 apiKeys / modelMapping', () => {
    const fresh = [
      makeChannel({ index: 0, apiKeys: ['a'], modelMapping: { x: 'y' } }),
      makeChannel({ index: 1, apiKeys: ['b'] }),
    ]
    const result = mergeChannelsWithLocalData(fresh, [], NOW)
    for (const ch of result) {
      expect(Object.isFrozen(ch.apiKeys)).toBe(true)
      if (ch.modelMapping) expect(Object.isFrozen(ch.modelMapping)).toBe(true)
    }
  })

  it('过滤非法 index 和重复 index，避免列表 key 冲突', () => {
    const fresh = [
      makeChannel({ index: 0, name: 'first' }),
      makeChannel({ index: -1, name: 'invalid' }),
      makeChannel({ index: 0, name: 'duplicate' }),
      makeChannel({ index: 2, name: 'second' }),
    ]

    const result = mergeChannelsWithLocalData(fresh, undefined, NOW)

    expect(result.map(ch => ch.name)).toEqual(['first', 'second'])
  })

  it('预索引不漏项：1000 个渠道找最后一个也能命中 O(1)', () => {
    const existing: Channel[] = []
    for (let i = 0; i < 1000; i++) {
      existing.push(makeChannel({ index: i, latencyTestTime: NOW - 1_000, latency: i }))
    }
    const fresh = [makeChannel({ index: 999 })]
    const result = mergeChannelsWithLocalData(fresh, existing, NOW)
    expect(result[0].latency).toBe(999)
  })

  it('freshChannel 没有对应 existing 时，返回原引用', () => {
    const fresh = [makeChannel({ index: 99 })]
    const result = mergeChannelsWithLocalData(fresh, [], NOW)
    expect(result[0]).toBe(fresh[0])
  })

  it('服务端数据未变化时复用已有渠道和数组引用', () => {
    const existing = [
      freezeImmutableFields(makeChannel({ index: 0, supportedModels: ['model-a'] })),
      freezeImmutableFields(makeChannel({ index: 1, name: 'second' })),
    ]
    const fresh = [
      makeChannel({ index: 0, supportedModels: ['model-a'] }),
      makeChannel({ index: 1, name: 'second' }),
    ]

    const result = mergeChannelsWithLocalData(fresh, existing, NOW)

    expect(result).toBe(existing)
    expect(result[0]).toBe(existing[0])
  })

  it('仅替换配置实际变化的渠道', () => {
    const existing = [
      freezeImmutableFields(makeChannel({ index: 0, name: 'first' })),
      freezeImmutableFields(makeChannel({ index: 1, name: 'second' })),
    ]
    const fresh = [
      makeChannel({ index: 0, name: 'first' }),
      makeChannel({ index: 1, name: 'changed' }),
    ]

    const result = mergeChannelsWithLocalData(fresh, existing, NOW)

    expect(result).not.toBe(existing)
    expect(result[0]).toBe(existing[0])
    expect(result[1]).toBe(fresh[1])
  })
})
