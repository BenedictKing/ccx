import type { RoutingPath } from '../services/routing-types'

export type RoutingField = 'requestedModel' | 'channelUid' | 'sentModel'
export const routingPathKey = (path: RoutingPath) =>
  JSON.stringify([path.requestedModel, path.channelUid, path.sentModel])

interface RoutingBox {
  key: string
  name: string
  x: number
  y: number
  width: number
  height: number
  count: number
}
export interface RoutingModelBox extends RoutingBox {
  channelUid: string
}
export interface RoutingChannelBox extends RoutingBox {
  deleted: boolean
  models: RoutingModelBox[]
}
export interface RoutingGraph {
  width: number
  height: number
  sources: RoutingBox[]
  channels: RoutingChannelBox[]
  links: { key: string; path: RoutingPath; ribbon: string; tone: number }[]
  pathKeys: string[]
  hidden: number
}

const MAX_PATHS = 12
const MODEL_HEIGHT = 68
const MODEL_GAP = 12
const CHANNEL_HEADER = 52
const CHANNEL_PADDING = 16
const CHANNEL_GAP = 20
const TOP = 16

// 同一筛选范围内保留已展示路径，避免计数排名变化导致节点在刷新时重排。
export function routingGraph(paths: RoutingPath[], previous?: RoutingGraph): RoutingGraph {
  const byKey = new Map(paths.filter(path => path.count > 0).map(path => [routingPathKey(path), path]))
  const selected = (previous?.pathKeys ?? []).filter(key => byKey.has(key))
  for (const key of byKey.keys()) {
    if (selected.length >= MAX_PATHS) break
    if (!selected.includes(key)) selected.push(key)
  }
  const drawn = selected.map(key => byKey.get(key)!)
  const sourceNames = [...new Set(drawn.map(path => path.requestedModel))].sort()
  const channelIds = [...new Set(drawn.map(path => path.channelUid))].sort()
  let channelY = TOP
  const channels: RoutingChannelBox[] = channelIds.map(channelUid => {
    const channelPaths = drawn.filter(path => path.channelUid === channelUid)
    const names = [...new Set(channelPaths.map(path => path.sentModel))].sort()
    const height =
      CHANNEL_HEADER + CHANNEL_PADDING + names.length * MODEL_HEIGHT + Math.max(0, names.length - 1) * MODEL_GAP
    const channel: RoutingChannelBox = {
      key: channelUid,
      name: channelPaths[0]!.channelName,
      deleted: !!channelPaths[0]!.channelDeleted,
      x: 560,
      y: channelY,
      width: 376,
      height,
      count: channelPaths.reduce((sum, path) => sum + path.count, 0),
      models: names.map((name, index) => ({
        key: JSON.stringify([channelUid, name]),
        channelUid,
        name,
        x: 576,
        y: channelY + CHANNEL_HEADER + index * (MODEL_HEIGHT + MODEL_GAP),
        width: 344,
        height: MODEL_HEIGHT,
        count: channelPaths.filter(path => path.sentModel === name).reduce((sum, path) => sum + path.count, 0)
      }))
    }
    channelY += height + CHANNEL_GAP
    return channel
  })
  const contentHeight = Math.max(260, channelY - CHANNEL_GAP - TOP, sourceNames.length * 88 - 16)
  const sources = sourceNames.map((name, index) => ({
    key: name,
    name,
    x: 24,
    y: TOP + ((contentHeight - sourceNames.length * 72) / (sourceNames.length + 1)) * (index + 1) + index * 72,
    width: 280,
    height: 72,
    count: drawn.filter(path => path.requestedModel === name).reduce((sum, path) => sum + path.count, 0)
  }))
  interface Slot {
    y: number
    height: number
  }
  const slots = new Map<RoutingBox, Map<string, Slot>>()
  for (const box of [...sources, ...channels.flatMap(channel => channel.models)]) {
    const related = drawn.filter(path =>
      'channelUid' in box
        ? path.channelUid === box.channelUid && path.sentModel === box.name
        : path.requestedModel === box.name
    )
    let y = box.y + 8
    const available = box.height - 16 - related.length * 2
    slots.set(
      box,
      new Map(
        related.map(path => {
          const height = 2 + (path.count / box.count) * available
          const slot = { y, height }
          y += height
          return [routingPathKey(path), slot]
        })
      )
    )
  }
  const links = drawn.map(path => {
    const key = routingPathKey(path)
    const source = sources.find(box => box.name === path.requestedModel)!
    const model = channels
      .find(channel => channel.key === path.channelUid)!
      .models.find(box => box.name === path.sentModel)!
    const from = slots.get(source)!.get(key)!,
      to = slots.get(model)!.get(key)!
    const left = source.x + source.width,
      right = model.x,
      middle = (left + right) / 2
    const ribbon =
      'M' +
      left +
      ',' +
      from.y +
      ' C' +
      middle +
      ',' +
      from.y +
      ' ' +
      middle +
      ',' +
      to.y +
      ' ' +
      right +
      ',' +
      to.y +
      ' L' +
      right +
      ',' +
      (to.y + to.height) +
      ' C' +
      middle +
      ',' +
      (to.y + to.height) +
      ' ' +
      middle +
      ',' +
      (from.y + from.height) +
      ' ' +
      left +
      ',' +
      (from.y + from.height) +
      ' Z'
    let hash = 0
    for (const char of path.requestedModel) hash = (hash * 31 + char.charCodeAt(0)) >>> 0
    return { key, path, ribbon, tone: hash % 6 }
  })
  return {
    width: 960,
    height: contentHeight + TOP * 2,
    sources,
    channels,
    links,
    pathKeys: selected,
    hidden: Math.max(0, paths.length - drawn.length)
  }
}
