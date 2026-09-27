import { describe, expect, it } from 'vitest'
import type { ChannelKeyMetricsHistoryResponse } from '../services/api'
import { mergeChannelKeyMetricsHistories } from './channelKeyMetrics'

const history = (
  channelIndex: number,
  requestCount: number,
  inputTokens: number,
  keyIdentity = 'kh_shared',
  keyMask = '***7628'
): ChannelKeyMetricsHistoryResponse => ({
  channelIndex,
  channelName: 'routex-best',
  keys: [{
    keyIdentity,
    keyMask,
    model: 'gpt-6-sol',
    color: '#123456',
    dataPoints: [{
      timestamp: '2026-09-27T01:00:00Z',
      requestCount,
      successCount: requestCount,
      failureCount: 0,
      successRate: 100,
      inputTokens,
      outputTokens: 2,
      cacheCreationTokens: 0,
      cacheReadTokens: 1,
      costUSD: 0.01
    }]
  }]
})

describe('mergeChannelKeyMetricsHistories', () => {
  it('合并同一逻辑渠道不同协议的同 Key 时间桶', () => {
    const merged = mergeChannelKeyMetricsHistories([history(1, 2, 10), history(4, 3, 20)], '1h')

    expect(merged.keys).toHaveLength(1)
    expect(merged.keys[0].dataPoints[0]).toMatchObject({
      requestCount: 5,
      successCount: 5,
      inputTokens: 30,
      outputTokens: 4,
      cacheReadTokens: 2,
      costUSD: 0.02
    })
    expect(merged.summary).toMatchObject({
      totalRequests: 5,
      totalInputTokens: 30,
      avgSuccessRate: 100
    })
  })

  it('使用稳定 key 标识避免相同脱敏文本误合并', () => {
    const merged = mergeChannelKeyMetricsHistories([
      history(1, 2, 10, 'kh_a', 'sk-a99***628'),
      history(4, 3, 20, 'kh_b', 'sk-a99***628')
    ], '1h')

    expect(merged.keys).toHaveLength(2)
  })

  it('汇总采用各协议全量 summary，不受 Top 10 曲线截断影响', () => {
    const first = history(1, 2, 10)
    first.summary = {
      totalRequests: 20,
      totalSuccess: 18,
      totalFailure: 2,
      totalInputTokens: 100,
      totalOutputTokens: 30,
      totalCacheCreationTokens: 4,
      totalCacheReadTokens: 5,
      totalCostUSD: 0.3,
      avgSuccessRate: 90,
      duration: '1h',
      intervalSeconds: 60
    }
    const second = history(4, 3, 20)
    second.summary = {
      totalRequests: 30,
      totalSuccess: 24,
      totalFailure: 6,
      totalInputTokens: 200,
      totalOutputTokens: 50,
      totalCacheCreationTokens: 6,
      totalCacheReadTokens: 7,
      totalCostUSD: 0.7,
      avgSuccessRate: 80,
      duration: '1h',
      intervalSeconds: 300
    }

    const merged = mergeChannelKeyMetricsHistories([first, second], '1h')
    expect(merged.summary).toMatchObject({
      totalRequests: 50,
      totalSuccess: 42,
      totalFailure: 8,
      totalInputTokens: 300,
      totalCostUSD: 1,
      avgSuccessRate: 84,
      intervalSeconds: 60
    })
  })
})
