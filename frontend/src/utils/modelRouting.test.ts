import { describe, expect, it } from 'vitest'
import type { RoutingPath } from '../services/routing-types'
import { routingGraph, routingPathKey } from './modelRouting'
const path = (source: string, channel: string, target: string, count: number): RoutingPath => ({
  requestedModel: source,
  channelUid: channel,
  channelName: channel,
  sentModel: target,
  count,
  share: count,
  mappingOutcomes: {},
  statuses: {}
})
const positions = (graph: ReturnType<typeof routingGraph>) => ({
  height: graph.height,
  sources: graph.sources.map(({ key, x, y, width, height }) => ({ key, x, y, width, height })),
  channels: graph.channels.map(({ key, x, y, width, height, models }) => ({
    key,
    x,
    y,
    width,
    height,
    models: models.map(({ key, x, y, width, height }) => ({ key, x, y, width, height }))
  }))
})
describe('model routing flow', () => {
  it('contains each sent model inside its own channel and preserves exact triples', () => {
    const paths = [
      path('source-a', 'shared', 'target-a', 70),
      path('source-b', 'shared', 'target-b', 30),
      path('source-a', 'other', 'target-a', 10)
    ]
    const graph = routingGraph(paths)
    expect(graph.links.map(link => link.key)).toEqual(paths.map(routingPathKey))
    expect(graph.channels.find(channel => channel.key === 'shared')?.count).toBe(100)
    for (const channel of graph.channels)
      for (const model of channel.models) {
        expect(model.channelUid).toBe(channel.key)
        expect(model.x).toBeGreaterThan(channel.x)
        expect(model.y).toBeGreaterThan(channel.y)
        expect(model.x + model.width).toBeLessThan(channel.x + channel.width)
        expect(model.y + model.height).toBeLessThan(channel.y + channel.height)
      }
    const repeated = graph.channels.flatMap(channel => channel.models).filter(model => model.name === 'target-a')
    expect(repeated).toHaveLength(2)
    expect(repeated[0]?.key).not.toBe(repeated[1]?.key)
    for (const link of graph.links) {
      expect(link.ribbon).not.toContain('NaN')
      expect(link.ribbon.endsWith('Z')).toBe(true)
    }
  })
  it('keeps all box positions and path order stable when counts and ranks change', () => {
    const paths = [path('source-a', 'c1', 'm1', 90), path('source-b', 'c2', 'm2', 10)]
    const first = routingGraph(paths)
    const refreshed = routingGraph(
      [
        { ...paths[1]!, count: 900 },
        { ...paths[0]!, count: 1 }
      ],
      first
    )
    expect(positions(refreshed)).toEqual(positions(first))
    expect(refreshed.pathKeys).toEqual(first.pathKeys)
    expect(refreshed.links.map(link => link.tone)).toEqual(first.links.map(link => link.tone))
    expect(refreshed.channels.find(channel => channel.key === 'c2')?.count).toBe(900)
  })
  it('keeps displayed paths when the top 12 rankings change without renormalizing shares', () => {
    const paths = Array.from({ length: 16 }, (_, i) => path('s' + i, 'c' + i, 't' + i, 16 - i))
    const first = routingGraph(paths)
    const refreshed = routingGraph(
      [...paths].reverse().map(path => ({ ...path, count: 100 - path.count })),
      first
    )
    expect(first.links).toHaveLength(12)
    expect(first.hidden).toBe(4)
    expect(refreshed.pathKeys).toEqual(first.pathKeys)
    expect(first.links[0]?.path.share).toBe(16)
    expect(paths).toHaveLength(16)
  })
})
