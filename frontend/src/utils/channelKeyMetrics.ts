import type {
  ChannelKeyMetricsHistoryResponse,
  GlobalStatsSummary,
  KeyHistoryData,
  KeyHistoryDataPoint
} from '../services/api'

const mergePoint = (target: KeyHistoryDataPoint, source: KeyHistoryDataPoint): void => {
  target.requestCount += source.requestCount
  target.successCount += source.successCount
  target.failureCount += source.failureCount
  target.inputTokens += source.inputTokens
  target.outputTokens += source.outputTokens
  target.cacheCreationTokens += source.cacheCreationTokens
  target.cacheReadTokens += source.cacheReadTokens
  target.costUSD = (target.costUSD || 0) + (source.costUSD || 0)
  target.successRate = target.requestCount > 0 ? target.successCount / target.requestCount * 100 : 0
}

const summarizeKeys = (keys: KeyHistoryData[], duration: string, intervalSeconds?: number): GlobalStatsSummary => {
  const summary: GlobalStatsSummary = {
    totalRequests: 0,
    totalSuccess: 0,
    totalFailure: 0,
    totalInputTokens: 0,
    totalOutputTokens: 0,
    totalCacheCreationTokens: 0,
    totalCacheReadTokens: 0,
    totalCostUSD: 0,
    avgSuccessRate: 0,
    duration,
    intervalSeconds
  }
  for (const key of keys) {
    for (const point of key.dataPoints) {
      summary.totalRequests += point.requestCount
      summary.totalSuccess += point.successCount
      summary.totalFailure += point.failureCount
      summary.totalInputTokens += point.inputTokens
      summary.totalOutputTokens += point.outputTokens
      summary.totalCacheCreationTokens += point.cacheCreationTokens
      summary.totalCacheReadTokens += point.cacheReadTokens
      summary.totalCostUSD = (summary.totalCostUSD || 0) + (point.costUSD || 0)
    }
  }
  summary.avgSuccessRate = summary.totalRequests > 0
    ? summary.totalSuccess / summary.totalRequests * 100
    : 0
  return summary
}

const summarizeHistories = (
  histories: ChannelKeyMetricsHistoryResponse[],
  duration: string
): GlobalStatsSummary => {
  const summaries = histories.map(history => history.summary || summarizeKeys(history.keys || [], duration))
  const intervals = summaries
    .map(summary => summary.intervalSeconds)
    .filter((value): value is number => typeof value === 'number' && value > 0)
  const summary: GlobalStatsSummary = {
    totalRequests: 0,
    totalSuccess: 0,
    totalFailure: 0,
    totalInputTokens: 0,
    totalOutputTokens: 0,
    totalCacheCreationTokens: 0,
    totalCacheReadTokens: 0,
    totalCostUSD: 0,
    avgSuccessRate: 0,
    duration,
    intervalSeconds: intervals.length > 0 ? Math.min(...intervals) : undefined
  }
  for (const item of summaries) {
    summary.totalRequests += item.totalRequests
    summary.totalSuccess += item.totalSuccess
    summary.totalFailure += item.totalFailure
    summary.totalInputTokens += item.totalInputTokens
    summary.totalOutputTokens += item.totalOutputTokens
    summary.totalCacheCreationTokens += item.totalCacheCreationTokens
    summary.totalCacheReadTokens += item.totalCacheReadTokens
    summary.totalCostUSD = (summary.totalCostUSD || 0) + (item.totalCostUSD || 0)
  }
  summary.avgSuccessRate = summary.totalRequests > 0
    ? summary.totalSuccess / summary.totalRequests * 100
    : 0
  return summary
}

// mergeChannelKeyMetricsHistories 合并逻辑渠道各协议路由的 Key 指标。
// 相同 Key+Model 归为同一曲线，时间桶逐项求和，避免统一渠道只展示主协议流量。
export const mergeChannelKeyMetricsHistories = (
  histories: ChannelKeyMetricsHistoryResponse[],
  duration: string
): ChannelKeyMetricsHistoryResponse => {
  const primary = histories[0]
  if (!primary) {
    return { channelIndex: 0, channelName: '', keys: [] }
  }

  const keysByIdentity = new Map<string, KeyHistoryData>()
  for (const history of histories) {
    for (const key of history.keys || []) {
      const identity = `${key.keyIdentity || key.keyMask}\u0000${key.model || ''}`
      let mergedKey = keysByIdentity.get(identity)
      if (!mergedKey) {
        mergedKey = { ...key, dataPoints: [] }
        keysByIdentity.set(identity, mergedKey)
      }
      const pointsByTime = new Map(mergedKey.dataPoints.map(point => [point.timestamp, point]))
      for (const point of key.dataPoints || []) {
        const existing = pointsByTime.get(point.timestamp)
        if (existing) {
          mergePoint(existing, point)
          continue
        }
        const cloned = { ...point }
        mergedKey.dataPoints.push(cloned)
        pointsByTime.set(cloned.timestamp, cloned)
      }
      mergedKey.dataPoints.sort((a, b) => new Date(a.timestamp).getTime() - new Date(b.timestamp).getTime())
    }
  }

  const keys = Array.from(keysByIdentity.values())

  return {
    channelIndex: primary.channelIndex,
    channelName: primary.channelName,
    keys,
    // 各协议接口的 summary 基于该协议全部 key；直接相加可保留 Top 10
    // 曲线之外的请求量，避免图表卡片因展示截断而少算。
    summary: summarizeHistories(histories, duration)
  }
}
