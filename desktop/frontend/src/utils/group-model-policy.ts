import type { Channel, ChannelKind, DisabledGroupModel } from '@/services/admin-api'

// 分组模型限制在逻辑渠道聚合视图里分散在各协议路由上：展示与操作都必须按并集处理，
// 否则只看得到/只清得掉主路由那一份，其它协议残留（恢复后刷新还会"复活"）。
// 这些是纯函数，便于单测；组合式函数只负责传渠道与兜底路由。

export type GroupModelRouteTarget = { kind: ChannelKind; index: number }

/** 策略键：与后端 sameDisabledGroupModel 同口径（有配额组按组，否则按 Key，模型大小写不敏感）。 */
export function groupModelPolicyKey(record: Pick<DisabledGroupModel, 'quotaGroup' | 'key' | 'model'>): string {
  const group = record.quotaGroup?.trim() || ''
  return JSON.stringify([group ? 'group' : 'key', group || record.key?.trim() || '', record.model.trim().toLowerCase()])
}

/** 顶层记录与各协议路由记录的并集（按策略键去重，顶层优先）。 */
export function mergeGroupModelUnion(channel: Channel): DisabledGroupModel[] {
  const merged = new Map<string, DisabledGroupModel>()
  for (const record of channel.disabledGroupModels ?? []) {
    merged.set(groupModelPolicyKey(record), record)
  }
  for (const route of channel.protocolRoutes ?? []) {
    for (const record of route.disabledGroupModels ?? []) {
      const key = groupModelPolicyKey(record)
      if (!merged.has(key)) merged.set(key, record)
    }
  }
  return Array.from(merged.values())
}

/**
 * 恢复目标：只打真正持有该限制的路由；都查不到时回退主路由
 * （兼容非聚合渠道与旧记录）。
 */
export function groupModelRestoreTargets(
  channel: Channel,
  record: DisabledGroupModel,
  fallback: GroupModelRouteTarget,
): GroupModelRouteTarget[] {
  const policyKey = groupModelPolicyKey(record)
  const routes = (channel.protocolRoutes ?? []).filter(route =>
    (route.disabledGroupModels ?? []).some(item => groupModelPolicyKey(item) === policyKey),
  )
  if (routes.length > 0) {
    return routes.map(route => ({ kind: route.kind as ChannelKind, index: route.index }))
  }
  return [fallback]
}

/**
 * 禁用目标：后端按"渠道内该 Key 所属配额组"落库，Key 不在该渠道会直接报
 * "API Key 不属于该渠道"，因此必须打到所有包含该 Key 的协议路由。
 */
export function groupModelDisableTargets(
  channel: Channel,
  apiKey: string,
  fallback: GroupModelRouteTarget,
): GroupModelRouteTarget[] {
  const routes = (channel.protocolRoutes ?? []).filter(route => (route.apiKeys ?? []).includes(apiKey))
  if (routes.length > 0) {
    return routes.map(route => ({ kind: route.kind as ChannelKind, index: route.index }))
  }
  return [fallback]
}
