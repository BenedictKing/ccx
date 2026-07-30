import type { EndpointDetailItem, MiniMaxTokenPlanUsage } from '../services/api-types'
import type { UsageQuotaItem } from './usageQuotaItem'

const hasUsableUsage = (endpoint: EndpointDetailItem): boolean =>
  !endpoint.miniMaxTokenPlanUsageError && Boolean(endpoint.miniMaxTokenPlanUsage?.models.length)

const usageTimestamp = (endpoint: EndpointDetailItem): number => {
  const value = endpoint.miniMaxTokenPlanUsage?.fetchedAt
  if (!value) return 0
  const timestamp = Date.parse(value)
  return Number.isNaN(timestamp) ? 0 : timestamp
}

const pickBestEndpoint = (endpoints: EndpointDetailItem[]): EndpointDetailItem | undefined =>
  [...endpoints].sort((left, right) => {
    const usageDifference = Number(hasUsableUsage(right)) - Number(hasUsableUsage(left))
    if (usageDifference !== 0) return usageDifference
    const timestampDifference = usageTimestamp(right) - usageTimestamp(left)
    if (timestampDifference !== 0) return timestampDifference
    return left.endpointUid.localeCompare(right.endpointUid)
  })[0]

export const sha256KeyHash = async (apiKey: string): Promise<string> => {
  if (!apiKey) return ''
  const bytes = new globalThis.TextEncoder().encode(apiKey)
  const digest = await globalThis.crypto.subtle.digest('SHA-256', bytes)
  return Array.from(new Uint8Array(digest), byte => byte.toString(16).padStart(2, '0')).join('').slice(0, 16)
}

const clampPercent = (value: number): number => Math.max(0, Math.min(100, value))

// 与编辑渠道保持一致的配额文本：剩余 x% (已用/总数)。
const formatModelQuota = (remainingPercent: number, used: number, total: number): string => {
  const percent = clampPercent(remainingPercent).toFixed(0)
  return total > 0 ? `剩余 ${percent}% (${used}/${total})` : `剩余 ${percent}%`
}

const formatRemainsTime = (t: (key: string) => string, milliseconds: number): string => {
  if (milliseconds <= 0) return t('healthCenter.detail.resetSoon')
  const minutes = Math.floor(milliseconds / 60000)
  const hours = Math.floor(minutes / 60)
  const remainingMinutes = minutes % 60
  return hours > 0 ? `${hours}h ${remainingMinutes}m` : `${remainingMinutes}m`
}

/** 把 MiniMax 按模型的 Token Plan 用量映射为统一余量行：每模型拆当前窗口/每周窗口两行。 */
export const buildMinimaxQuotaItems = (
  usage: MiniMaxTokenPlanUsage,
  t: (key: string) => string,
): UsageQuotaItem[] => {
  const items: UsageQuotaItem[] = []
  for (const quota of usage.models) {
    items.push({
      key: `${quota.modelName}-current`,
      label: `${quota.modelName} · ${t('healthCenter.detail.currentWindow')}`,
      usedPercent: 100 - clampPercent(quota.currentIntervalRemainingPercent),
      value: formatModelQuota(
        quota.currentIntervalRemainingPercent,
        quota.currentIntervalUsageCount,
        quota.currentIntervalTotalCount,
      ),
      caption: `${t('healthCenter.detail.resetsIn')} ${formatRemainsTime(t, quota.remainsTimeMs)}`,
    })
    items.push({
      key: `${quota.modelName}-weekly`,
      label: `${quota.modelName} · ${t('healthCenter.detail.weeklyWindow')}`,
      usedPercent: 100 - clampPercent(quota.currentWeeklyRemainingPercent),
      value: formatModelQuota(
        quota.currentWeeklyRemainingPercent,
        quota.currentWeeklyUsageCount,
        quota.currentWeeklyTotalCount,
      ),
    })
  }
  return items
}

export const selectMiniMaxTokenPlanEndpoint = (
  endpoints: EndpointDetailItem[],
  keyHash: string,
  keyMask: string,
): EndpointDetailItem | undefined => {
  const supported = endpoints.filter(endpoint => endpoint.tokenPlanUsageSupported)
  const hashMatches = keyHash ? supported.filter(endpoint => endpoint.keyHash === keyHash) : []
  if (hashMatches.length > 0) return pickBestEndpoint(hashMatches)

  // 兼容尚未升级、仍把掩码放在 keyHash 字段中的后端。
  return pickBestEndpoint(supported.filter(endpoint =>
    endpoint.keyMask === keyMask || endpoint.keyHash === keyMask,
  ))
}
