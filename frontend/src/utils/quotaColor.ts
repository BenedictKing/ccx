/**
 * 额度余量分档着色。
 *
 * 按"剩余百分比"划分 5 档，阈值 5/10/20/30/50，从深到浅表示余量递增。
 * 各渠道用量语义不同（有的是剩余%，有的是已用%），调用前请自行换算为"剩余百分比"。
 */
export const QUOTA_THRESHOLDS = [5, 10, 20, 30, 50] as const

/** 按剩余百分比分档，返回 scoped CSS 类名（供 :class 绑定文字颜色）。 */
export const quotaRemainingColorClass = (remainingPercent: number): string => {
  if (remainingPercent < 5) return 'quota-critical'
  if (remainingPercent < 10) return 'quota-danger'
  if (remainingPercent < 20) return 'quota-warning'
  if (remainingPercent < 30) return 'quota-caution'
  if (remainingPercent < 50) return 'quota-low'
  return ''
}

/** 按剩余百分比分档，返回 hex 色值（供 v-progress-linear :color 等需要色值的场景）。 */
export const quotaRemainingColorHex = (remainingPercent: number): string => {
  if (remainingPercent < 5) return '#DC2626'
  if (remainingPercent < 10) return '#EF4444'
  if (remainingPercent < 20) return '#EA580C'
  if (remainingPercent < 30) return '#F59E0B'
  if (remainingPercent < 50) return '#3B82F6'
  return '#10B981'
}
