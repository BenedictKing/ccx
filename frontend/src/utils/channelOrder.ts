import type { Channel } from '../services/api'

/**
 * 渠道列表稳定排序。
 *
 * 排序依据只允许使用与运行时状态无关的字段：
 * 后端拉黑 key 时只会清空 `apiKeys`，既不会改动 `priority`，也不会调整渠道在配置数组中的位置。
 * 因此这里禁止引入任何 `apiKeys` / 熔断 / 指标相关的比较，否则渠道会在 key 被拉黑后
 * 自行改变列表位置（曾出现「首位渠道 key 全黑后掉到第三位」的问题）。
 *
 * @param source 待排序渠道
 * @param fallbackOrder 上一次已知顺序的 UI key 列表，用于 priority 缺失时兜底
 * @param builtInOrder 首次渲染记录的内置顺序，作为二级兜底
 * @param getUiKey 渠道 UI 唯一键
 * @param getRouteIndex 渠道路由索引，最终稳定兜底
 */
export const sortChannelsByPriority = (
  source: Channel[],
  fallbackOrder: string[],
  builtInOrder: string[],
  getUiKey: (channel: Channel) => string,
  getRouteIndex: (channel: Channel) => number,
): Channel[] => {
  const fallbackRank = new Map<string, number>()
  fallbackOrder.forEach((key, rank) => fallbackRank.set(key, rank))

  const originalRank = new Map<string, number>()
  builtInOrder.forEach((key, rank) => originalRank.set(key, rank))

  const getRank = (ch: Channel): number =>
    // priority 0 与缺失等价（后端语义：0 = 未显式配置，调度层回退为索引），
    // 不能让 0 参与数值比较，否则未排序渠道会排到所有显式 priority 之前
    (ch.priority && ch.priority > 0 ? ch.priority : undefined) ??
    fallbackRank.get(getUiKey(ch)) ??
    originalRank.get(getUiKey(ch)) ??
    getRouteIndex(ch)

  return [...source].sort((a, b) => {
    const rankDiff = getRank(a) - getRank(b)
    if (rankDiff !== 0) return rankDiff

    // priority 相同时用 routeIndex 兜底，保证顺序稳定且与运行时状态无关
    return getRouteIndex(a) - getRouteIndex(b)
  })
}
