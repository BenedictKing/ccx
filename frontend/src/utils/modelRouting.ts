import type { RoutingPath } from '../services/routing-types'
export type RoutingField = 'requestedModel' | 'channelUid' | 'sentModel'
export const routingPathKey = (path: RoutingPath) =>
  JSON.stringify([path.requestedModel, path.channelUid, path.sentModel])
export function routingGraph(paths: RoutingPath[]) {
  const drawn = paths.slice(0, 12)
  const fields: RoutingField[] = ['requestedModel', 'channelUid', 'sentModel']
  const columns = fields.map(field => [...new Set(drawn.map(path => path[field]))])
  const nodeCount = Math.max(1, ...columns.map(values => values.length))
  const height = Math.max(280, nodeCount * 64)
  const total = drawn.reduce((sum, path) => sum + path.count, 0)
  const scale = (height - nodeCount * 38) / Math.max(total, 1)
  const widths = drawn.map(path => Math.max(3, path.count * scale))
  const nodes = columns.flatMap((values, column) => {
    const sizes = values.map(value =>
      drawn.reduce((sum, path, index) => sum + (path[fields[column]!] === value ? widths[index]! : 0), 0)
    )
    let y = 35 + (height - sizes.reduce((sum, size) => sum + size, 0) - (values.length - 1) * 38) / 2
    return values.map((value, index) => {
      const name = column === 1 ? drawn.find(path => path.channelUid === value)!.channelName : value
      const node = {
        field: fields[column]!,
        value,
        name,
        x: 20 + column * 330,
        y,
        height: sizes[index]!,
        width: 245,
        count: drawn.filter(path => path[fields[column]!] === value).reduce((sum, path) => sum + path.count, 0),
        offset: 0
      }
      y += sizes[index]! + 38
      return node
    })
  })
  const links = drawn.map((path, index) => {
    const positions = fields.map(field => {
      const node = nodes.find(node => node.field === field && node.value === path[field])!
      const position = { x: node.x, y: node.y + node.offset, width: node.width }
      node.offset += widths[index]!
      return position
    })
    const ribbon = (left: (typeof positions)[number], right: (typeof positions)[number]) => {
      const x1 = left.x + left.width,
        x2 = right.x,
        mid = (x1 + x2) / 2,
        thickness = widths[index]!
      return `M${x1},${left.y} C${mid},${left.y} ${mid},${right.y} ${x2},${right.y} L${x2},${right.y + thickness} C${mid},${right.y + thickness} ${mid},${left.y + thickness} ${x1},${left.y + thickness} Z`
    }
    return {
      key: routingPathKey(path),
      path,
      ribbons: [ribbon(positions[0]!, positions[1]!), ribbon(positions[1]!, positions[2]!)],
      tone: index % 6
    }
  })
  return { nodes, links, height: height + 90, hidden: Math.max(0, paths.length - drawn.length) }
}
