import { describe, expect, it } from 'vitest'
import type { Channel } from '@/services/admin-api'
import {
  LATENCY_VALID_DURATION,
  freezeImmutableFields,
  mergeChannelsWithLocalData,
} from './channel-merge'

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
  it('冻结不可变字段', () => {
    const channel = makeChannel({
      apiKeys: ['k1'],
      apiKeyConfigs: [{ key: 'k1' }],
      disabledApiKeys: [{ key: 'k2', reason: 'expired', message: 'expired', disabledAt: '2026-01-01T00:00:00Z' }],
    })

    freezeImmutableFields(channel)

    expect(Object.isFrozen(channel.apiKeys)).toBe(true)
    expect(Object.isFrozen(channel.apiKeyConfigs)).toBe(true)
    expect(Object.isFrozen(channel.disabledApiKeys)).toBe(true)
  })
})

describe('mergeChannelsWithLocalData', () => {
  const NOW = 1_700_000_000_000

  it('保留 5 分钟内的本地 latency', () => {
    const existing = [
      makeChannel({ index: 0, latency: 42, latencyTestTime: NOW - 60_000 }),
    ]
    const fresh = [makeChannel({ index: 0 })]

    const result = mergeChannelsWithLocalData(fresh, existing, NOW)

    expect(result[0].latency).toBe(42)
    expect(result[0].latencyTestTime).toBe(NOW - 60_000)
  })

  it('丢弃过期 latency', () => {
    const existing = [
      makeChannel({ index: 0, latency: 42, latencyTestTime: NOW - LATENCY_VALID_DURATION - 1 }),
    ]
    const fresh = [makeChannel({ index: 0 })]

    const result = mergeChannelsWithLocalData(fresh, existing, NOW)

    expect(result[0].latency).toBeUndefined()
  })

  it('过滤非法 index 和重复 index，避免列表 key 冲突', () => {
    const fresh = [
      makeChannel({ index: 0, name: 'first' }),
      makeChannel({ index: -1, name: 'invalid' }),
      makeChannel({ index: 0, name: 'duplicate' }),
      makeChannel({ index: 2, name: 'second' }),
    ]

    const result = mergeChannelsWithLocalData(fresh, undefined, NOW)

    expect(result.map(channel => channel.name)).toEqual(['first', 'second'])
  })
})
