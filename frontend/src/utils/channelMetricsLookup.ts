import type { ChannelKind, ChannelMetrics } from '@/services/api'

/**
 * 渠道指标查表。
 *
 * 统一列表（messages/chat/responses/gemini 合并展示）里不同协议的渠道
 * 可能拥有相同的 channelIndex，因此必须按 `routeKind:channelIndex` 复合键
 * 精确匹配，与 RPM/TPM 的 activityMap 保持同一套键控策略。
 *
 * 旧数据（后端未返回 routeKind）退化为按 channelIndex 匹配，但仅在该 index
 * 唯一时才生效；出现碰撞时返回 undefined，避免把首条指标复用给其他渠道。
 */
export interface ChannelMetricsLookup {
  get(channelIndex: number, routeKind?: ChannelKind): ChannelMetrics | undefined
}

const strictKey = (channelIndex: number, routeKind: ChannelKind): string =>
  `${routeKind}:${channelIndex}`

export const buildChannelMetricsLookup = (metrics: ChannelMetrics[]): ChannelMetricsLookup => {
  const strict = new Map<string, ChannelMetrics>()
  // null 表示该 channelIndex 在无 routeKind 的数据里出现多次（歧义），不可用于兜底
  const legacy = new Map<number, ChannelMetrics | null>()

  for (const metric of metrics) {
    if (metric.routeKind) {
      strict.set(strictKey(metric.channelIndex, metric.routeKind), metric)
      continue
    }
    legacy.set(metric.channelIndex, legacy.has(metric.channelIndex) ? null : metric)
  }

  return {
    get(channelIndex: number, routeKind?: ChannelKind): ChannelMetrics | undefined {
      if (routeKind) {
        const hit = strict.get(strictKey(channelIndex, routeKind))
        if (hit) return hit
      }
      return legacy.get(channelIndex) ?? undefined
    },
  }
}
