import { describe, expect, it } from 'vitest'

import type { ChannelKind, ChannelMetrics } from '@/services/api'
import { buildChannelMetricsLookup } from './channelMetricsLookup'

const metric = (
  channelIndex: number,
  requestCount: number,
  routeKind?: ChannelKind,
): ChannelMetrics => ({
  channelIndex,
  routeKind,
  requestCount,
  successCount: requestCount,
  failureCount: 0,
  successRate: 100,
  errorRate: 0,
  consecutiveFailures: 0,
  latency: 0,
  timeWindows: {
    '15m': { requestCount, successCount: requestCount, failureCount: 0, successRate: 100, cacheHitRate: 94 },
    '1h': { requestCount, successCount: requestCount, failureCount: 0, successRate: 100 },
    '6h': { requestCount, successCount: requestCount, failureCount: 0, successRate: 100 },
    '24h': { requestCount, successCount: requestCount, failureCount: 0, successRate: 100 },
  },
})

describe('buildChannelMetricsLookup', () => {
  it('按 routeKind:channelIndex 复合键精确匹配', () => {
    const lookup = buildChannelMetricsLookup([
      metric(0, 153, 'messages'),
      metric(0, 7, 'responses'),
    ])

    expect(lookup.get(0, 'messages')?.requestCount).toBe(153)
    expect(lookup.get(0, 'responses')?.requestCount).toBe(7)
  })

  it('同一 channelIndex 下缺失的协议不会复用其他协议的指标', () => {
    const lookup = buildChannelMetricsLookup([metric(0, 153, 'messages')])

    // 回归用例：第二个渠道本身没有请求，不应显示第一个渠道的请求数/缓存率
    expect(lookup.get(0, 'gemini')).toBeUndefined()
    expect(lookup.get(1, 'messages')).toBeUndefined()
  })

  it('旧数据缺少 routeKind 时按 channelIndex 兜底', () => {
    const lookup = buildChannelMetricsLookup([metric(3, 42)])

    expect(lookup.get(3, 'messages')?.requestCount).toBe(42)
    expect(lookup.get(3)?.requestCount).toBe(42)
    expect(lookup.get(4, 'messages')).toBeUndefined()
  })

  it('无 routeKind 且 channelIndex 碰撞时视为歧义，不返回首条', () => {
    const lookup = buildChannelMetricsLookup([metric(0, 153), metric(0, 0)])

    expect(lookup.get(0, 'messages')).toBeUndefined()
    expect(lookup.get(0)).toBeUndefined()
  })

  it('严格键命中优先于无 routeKind 的兜底项', () => {
    const lookup = buildChannelMetricsLookup([metric(0, 11), metric(0, 153, 'messages')])

    expect(lookup.get(0, 'messages')?.requestCount).toBe(153)
    expect(lookup.get(0, 'chat')?.requestCount).toBe(11)
  })
})
