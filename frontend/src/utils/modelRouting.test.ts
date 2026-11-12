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
describe('model routing flow', () => {
  it('preserves exact triples through shared channels', () => {
    const paths = [path('source-a', 'shared', 'target-a', 70), path('source-b', 'shared', 'target-b', 30)]
    const graph = routingGraph(paths)
    expect(graph.links.map(link => link.key)).toEqual(paths.map(routingPathKey))
    expect(graph.nodes.find(node => node.field === 'channelUid')?.count).toBe(100)
    for (const node of graph.nodes) expect(node.offset).toBeCloseTo(node.height)
    for (const link of graph.links)
      for (const ribbon of link.ribbons) {
        expect(ribbon).not.toContain('NaN')
        expect(ribbon.endsWith('Z')).toBe(true)
      }
  })
  it('limits drawn paths without renormalizing shares or changing inputs', () => {
    const paths = Array.from({ length: 16 }, (_, i) => path('s' + i, 'c' + i, 't' + i, 16 - i))
    const graph = routingGraph(paths)
    expect(graph.links).toHaveLength(12)
    expect(graph.hidden).toBe(4)
    expect(graph.links[0]?.path.share).toBe(16)
    expect(paths).toHaveLength(16)
  })
})
