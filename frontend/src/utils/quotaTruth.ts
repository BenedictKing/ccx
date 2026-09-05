import type { UsageQuotaItem } from './usageQuotaItem'

/**
 * 配额真相等级推导（配额真相分级调度 §2 前端纯函数）。
 *
 * 阈值与后端 quota.Value.IsApproaching 对齐：余量 ≤ 20%（已用 ≥ 80%）为
 * approaching_limit，余量 ≤ 0（已用 ≥ 100%）为 exhausted。所有 provider 的
 * item builder 统一走本函数，禁止各自复制阈值逻辑。
 *
 * 输入口径是"provider 官方快照"的已用百分比——来源固定为 provider_api，
 * 不凭 UI 数值反推可信度；无余量概念的行（无法换算百分比）传 undefined，
 * 得到 unknown（不显示红色，fail-open）。
 */
export function buildQuotaTruth(
  usedPercent?: number,
): Pick<UsageQuotaItem, 'truthLevel' | 'truthSource'> {
  if (usedPercent == null || !Number.isFinite(usedPercent)) {
    return { truthLevel: 'unknown', truthSource: 'provider_api' }
  }
  const clamped = Math.max(0, Math.min(100, usedPercent))
  if (clamped >= 100) return { truthLevel: 'exhausted', truthSource: 'provider_api' }
  if (clamped >= 80) return { truthLevel: 'approaching_limit', truthSource: 'provider_api' }
  return { truthLevel: 'healthy', truthSource: 'provider_api' }
}

/** 已知 provider 支持配额查询但本次获取失败：unavailable / provider_api。 */
export function unavailableQuotaTruth(): Pick<UsageQuotaItem, 'truthLevel' | 'truthSource'> {
  return { truthLevel: 'unavailable', truthSource: 'provider_api' }
}

/** 只有配置声明（无官方快照）：configured 来源。 */
export function configuredQuotaTruth(): Pick<UsageQuotaItem, 'truthLevel' | 'truthSource'> {
  return { truthLevel: 'unknown', truthSource: 'configured' }
}

/** 无任何证据：unknown / unknown。 */
export function unknownQuotaTruth(): Pick<UsageQuotaItem, 'truthLevel' | 'truthSource'> {
  return { truthLevel: 'unknown', truthSource: 'unknown' }
}
