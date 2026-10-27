import type { ChannelProtocolRoute } from '../services/api'

/** 按物理渠道 UID 比较，避免同协议多路由或列表重排时保存到其他渠道。 */
export function buildProtocolModelLimitUpdates(original: ChannelProtocolRoute[], edited: ChannelProtocolRoute[]) {
  return edited.flatMap(route => {
    if (!route.channelUid) return []
    const before = original.find(item => item.channelUid === route.channelUid && item.kind === route.kind)
    if (!before) return []
    const supportedModels = route.supportedModels ?? []
    if (JSON.stringify(before.supportedModels ?? []) === JSON.stringify(supportedModels)) return []
    return [{ channelUid: route.channelUid, kind: route.upstreamKind ?? route.kind, supportedModels }]
  })
}
