/**
 * 额度余量分档着色。
 *
 * 按"剩余百分比"划分 5 档，阈值 5/10/20/30/50，从深到浅表示余量递增：
 * - < 5%  quota-critical（深红，加粗）— 即将耗尽
 * - < 10% quota-danger（红）— 余量极低
 * - < 20% quota-warning（橙）— 余量偏低
 * - < 30% quota-caution（琥珀）— 需关注
 * - < 50% quota-low（蓝）— 偏低
 * - >= 50% 空字符串 — 默认色，无需强调
 *
 * 对应的 CSS 类定义在各消费组件的 scoped style 中（class 名统一，样式各自维护）。
 * 各渠道用量语义不同（有的是剩余%，有的是已用%），调用前请自行换算为"剩余百分比"。
 */
export const quotaRemainingColorClass = (remainingPercent: number): string => {
  if (remainingPercent < 5) return 'quota-critical'
  if (remainingPercent < 10) return 'quota-danger'
  if (remainingPercent < 20) return 'quota-warning'
  if (remainingPercent < 30) return 'quota-caution'
  if (remainingPercent < 50) return 'quota-low'
  return ''
}
