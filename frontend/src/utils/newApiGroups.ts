export interface NewApiGroup {
  name: string
  ratio: number
  /** 分组可用模型数；undefined 表示未查询或上游不支持按组查询 */
  modelCount?: number
}

export const DEFAULT_NEWAPI_MAX_GROUP_MULTIPLIER = 1

export function isValidNewApiGroupMultiplier(value: number): boolean {
  return Number.isFinite(value) && value >= 0
}

export function eligibleNewApiGroups(
  groups: Record<string, number>,
  maxMultiplier: number,
  modelCounts?: Record<string, number>
): NewApiGroup[] {
  if (!isValidNewApiGroupMultiplier(maxMultiplier)) return []

  return Object.entries(groups)
    .map(([name, ratio]) => ({ name: name.trim(), ratio, modelCount: modelCounts?.[name] }))
    .filter(group => group.name !== '' && Number.isFinite(group.ratio) && group.ratio >= 0 && group.ratio <= maxMultiplier)
    // 仅剔除明确查到 0 模型的分组；无记录（查询失败/fork 不支持 group 参数）保守保留
    .filter(group => group.modelCount !== 0)
    .sort((left, right) => left.ratio - right.ratio || left.name.localeCompare(right.name))
}
