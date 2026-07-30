import { computed, watch, markRaw, type Ref } from 'vue'
import type { ChannelRecentActivity, ActivitySegment } from '../services/api'
import { expandSparseSegments } from '../services/api-helpers'

/**
 * Activity 可视化相关的状态和计算逻辑。
 * 从 ChannelOrchestration.vue 抽出，降低单文件行数。
 */
export function useChannelActivity(recentActivity: Ref<ChannelRecentActivity[]>) {
  const activityKey = (channelIndex: number, routeKind?: string): string =>
    routeKind ? `${routeKind}:${channelIndex}` : String(channelIndex)

  const activityMap = computed(() => {
    const map = new Map<string, ChannelRecentActivity>()
    for (const a of recentActivity.value) {
      map.set(activityKey(a.channelIndex, a.routeKind), a)
    }
    return map
  })

  type IndexedActivitySegment = { index: number; segment: ActivitySegment }
  type ActivityBar = { index: number; x: number; y: number; width: number; height: number; radius: number; g: number }

  const getPopulatedSegments = (activity: ChannelRecentActivity): IndexedActivitySegment[] => {
    const totalSegs = activity.totalSegs || 150
    const result: IndexedActivitySegment[] = []

    if (Array.isArray(activity.segments)) {
      for (let index = 0; index < Math.min(activity.segments.length, totalSegs); index++) {
        const segment = activity.segments[index]
        if (segment?.requestCount > 0) result.push({ index, segment })
      }
      return result
    }

    for (const [rawIndex, segment] of Object.entries(activity.segments ?? {})) {
      const index = Number(rawIndex)
      if (Number.isInteger(index) && index >= 0 && index < totalSegs && segment?.requestCount > 0) {
        result.push({ index, segment })
      }
    }
    result.sort((a, b) => a.index - b.index)
    return result
  }

  // 该 Map 只在 activity 数据更新时读写，无需交给 Vue 深度代理。
  const maxRequestsHistory = new Map<string, { max: number; updatedAt: number }>()
  const DECAY_HALF_LIFE = 5 * 60 * 1000  // Half-life: 5 minutes
  const MIN_MAX_REQUESTS = 1  // Minimum baseline value to avoid division by zero

  const getDecayedMax = (record: { max: number; updatedAt: number }, now: number): number => {
    const elapsed = now - record.updatedAt
    const decayFactor = Math.pow(0.5, elapsed / DECAY_HALF_LIFE)
    return Math.max(MIN_MAX_REQUESTS, record.max * decayFactor)
  }

  watch(activityMap, (newMap) => {
    const now = Date.now()
    for (const [channelIndex, activity] of newMap.entries()) {
      const populatedSegments = getPopulatedSegments(activity)
      if (populatedSegments.length === 0) continue

      const currentMax = Math.max(...populatedSegments.map(({ segment }) => segment.requestCount), 0)

      const record = maxRequestsHistory.get(channelIndex)
      if (!record) {
        if (currentMax > 0) {
          maxRequestsHistory.set(channelIndex, { max: currentMax, updatedAt: now })
        }
        continue
      }

      const decayedMax = getDecayedMax(record, now)
      if (currentMax >= decayedMax) {
        maxRequestsHistory.set(channelIndex, { max: currentMax, updatedAt: now })
      } else {
        maxRequestsHistory.set(channelIndex, { max: decayedMax, updatedAt: now })
      }
    }
    // Clean up stale entries
    for (const key of maxRequestsHistory.keys()) {
      if (!newMap.has(key)) {
        maxRequestsHistory.delete(key)
      }
    }
  }, { immediate: true })

  const getChannelActivity = (channelIndex: number, routeKind?: string): ChannelRecentActivity | undefined => {
    return activityMap.value.get(activityKey(channelIndex, routeKind))
  }

  const emptyActivityBars = markRaw<ActivityBar[]>([])

  const activityBarsCache = computed(() => {
    const cache = new Map<string, ActivityBar[]>()

    for (const [channelIndex, activity] of activityMap.value.entries()) {
      const numSegments = activity.totalSegs || 150
      const populatedSegments = getPopulatedSegments(activity)
      if (populatedSegments.length === 0) continue

      const barWidth = 150 / numSegments
      const barGap = barWidth * 0.2
      const actualBarWidth = barWidth - barGap

      const now = Date.now()
      const record = maxRequestsHistory.get(channelIndex)
      const currentMax = Math.max(...populatedSegments.map(({ segment }) => segment.requestCount), 1)
      const maxRequests = record ? Math.max(getDecayedMax(record, now), currentMax) : currentMax

      const bars = populatedSegments.map(({ index, segment }) => {
        const requests = segment.requestCount
        const successCount = requests - segment.failureCount
        const successRate = (successCount / requests) * 100
        let g: number
        if (successRate < 5) g = 6
        else if (successRate < 20) g = 5
        else if (successRate < 40) g = 4
        else if (successRate < 60) g = 3
        else if (successRate < 80) g = 2
        else if (successRate < 95) g = 1
        else g = 0

        const heightPercent = requests / maxRequests
        const height = Math.max(heightPercent * 85, 2)

        return {
          index,
          x: index * barWidth + barGap / 2,
          y: 100 - height,
          width: actualBarWidth,
          height,
          radius: Math.min(actualBarWidth / 2, 1.5),
          g,
        }
      })

      cache.set(channelIndex, markRaw(bars))
    }

    return cache
  })

  const getActivityBars = (channelIndex: number, routeKind?: string): ActivityBar[] => {
    return activityBarsCache.value.get(activityKey(channelIndex, routeKind)) || emptyActivityBars
  }

  const getActivityPath = (channelIndex: number, routeKind?: string): string => {
    const activity = getChannelActivity(channelIndex, routeKind)
    if (!activity) return ''
    const segments = expandSparseSegments(activity)
    const numSegments = segments.length
    if (numSegments === 0) return ''

    const maxRequests = Math.max(...segments.map(s => s.requestCount), 1)
    const windowSize = 5
    const smoothedData: number[] = []

    for (let i = 0; i < numSegments; i++) {
      const start = Math.max(0, i - Math.floor(windowSize / 2))
      const end = Math.min(numSegments, i + Math.ceil(windowSize / 2))
      let sum = 0
      let count = 0

      for (let j = start; j < end; j++) {
        sum += segments[j].requestCount
        count++
      }

      smoothedData.push(count > 0 ? sum / count : 0)
    }

    const points: { x: number; y: number }[] = []
    for (let i = 0; i < numSegments; i++) {
      points.push({ x: i, y: 100 - (smoothedData[i] / maxRequests * 85) })
    }
    if (points.length < 2) return ''

    return catmullRomToPath(points)
  }

  function catmullRomToPath(points: { x: number; y: number }[]): string {
    if (points.length < 2) return ''
    const parts: string[] = [`M ${points[0].x} ${points[0].y}`]
    const tension = 0.3
    for (let i = 0; i < points.length - 1; i++) {
      const p0 = points[Math.max(0, i - 1)]
      const p1 = points[i]
      const p2 = points[i + 1]
      const p3 = points[Math.min(points.length - 1, i + 2)]
      const cp1x = p1.x + (p2.x - p0.x) * tension / 6
      const cp1y = p1.y + (p2.y - p0.y) * tension / 6
      const cp2x = p2.x - (p3.x - p1.x) * tension / 6
      const cp2y = p2.y - (p3.y - p1.y) * tension / 6
      parts.push(`C ${cp1x} ${cp1y}, ${cp2x} ${cp2y}, ${p2.x} ${p2.y}`)
    }
    return parts.join(' ')
  }

  const _getActivityAreaPath = (channelIndex: number, routeKind?: string): string => {
    const linePath = getActivityPath(channelIndex, routeKind)
    if (!linePath) return ''

    const activity = getChannelActivity(channelIndex, routeKind)
    if (!activity) return ''

    const segments = expandSparseSegments(activity)
    const numSegments = segments.length
    if (numSegments === 0) return ''

    return `${linePath} L ${numSegments - 1} 100 L 0 100 Z`
  }

  const _getActivityGradient = (channelIndex: number, routeKind?: string): string => {
    const activity = getChannelActivity(channelIndex, routeKind)
    if (!activity) return 'transparent'
    const segments = expandSparseSegments(activity)
    const numSegments = segments.length
    if (numSegments === 0) return 'transparent'

    if (!segments.some(seg => seg.requestCount > 0)) return 'transparent'

    const segmentColors: string[] = []
    for (let i = 0; i < numSegments; i++) {
      const seg = segments[i]

      if (seg.requestCount === 0) {
        segmentColors.push('transparent')
        continue
      }

      if (seg.failureCount > 0) {
        const failureRatio = seg.failureCount / seg.requestCount
        if (failureRatio >= 0.5) {
          const intensity = Math.min(0.5, 0.2 + seg.requestCount * 0.01)
          segmentColors.push(`rgba(239, 68, 68, ${intensity})`)
        } else {
          const intensity = Math.min(0.4, 0.15 + seg.requestCount * 0.008)
          segmentColors.push(`rgba(251, 146, 60, ${intensity})`)
        }
        continue
      }

      if (seg.requestCount >= 20) segmentColors.push('rgba(22, 163, 74, 0.65)')
      else if (seg.requestCount >= 15) segmentColors.push('rgba(22, 163, 74, 0.55)')
      else if (seg.requestCount >= 10) segmentColors.push('rgba(34, 197, 94, 0.50)')
      else if (seg.requestCount >= 6) segmentColors.push('rgba(34, 197, 94, 0.42)')
      else if (seg.requestCount >= 3) segmentColors.push('rgba(74, 222, 128, 0.38)')
      else segmentColors.push('rgba(74, 222, 128, 0.30)')
    }

    const stops = segmentColors.map((color, i) => {
      const start = (i / numSegments * 100).toFixed(3)
      const end = ((i + 1) / numSegments * 100).toFixed(3)
      return `${color} ${start}%, ${color} ${end}%`
    }).join(', ')

    return `linear-gradient(to right, ${stops})`
  }

  const formatRPM = (channelIndex: number, routeKind?: string): string => {
    const activity = getChannelActivity(channelIndex, routeKind)
    if (!activity || !activity.rpm) return '--'
    if (activity.rpm >= 10) return activity.rpm.toFixed(0)
    return activity.rpm.toFixed(1)
  }

  const formatTPM = (channelIndex: number, routeKind?: string): string => {
    const activity = getChannelActivity(channelIndex, routeKind)
    if (!activity || !activity.tpm) return '--'
    if (activity.tpm >= 1000000) return `${(activity.tpm / 1000000).toFixed(1)}M`
    if (activity.tpm >= 1000) return `${(activity.tpm / 1000).toFixed(1)}K`
    return activity.tpm.toFixed(0)
  }

  const hasActivityData = (channelIndex: number, routeKind?: string): boolean => {
    const activity = getChannelActivity(channelIndex, routeKind)
    if (!activity) return false
    return activity.rpm > 0 || activity.tpm > 0
  }

  return {
    activityMap,
    getChannelActivity,
    getActivityBars,
    getActivityPath,
    _getActivityAreaPath,
    _getActivityGradient,
    formatRPM,
    formatTPM,
    hasActivityData,
  }
}
