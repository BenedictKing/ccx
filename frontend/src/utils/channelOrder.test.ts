import { describe, expect, it } from 'vitest'

import type { Channel } from '@/services/api'
import { sortChannelsByPriority } from './channelOrder'

const channel = (
  name: string,
  index: number,
  apiKeys: string[],
  overrides: Partial<Channel> = {},
): Channel => ({
  name,
  channelUid: `ch-${index}`,
  index,
  serviceType: 'claude',
  baseUrl: 'https://example.com',
  apiKeys,
  ...overrides,
} as Channel)

const uiKey = (ch: Channel) => ch.displayKey ?? `messages:${ch.routeIndex ?? ch.index}`
const routeIndex = (ch: Channel) => ch.routeIndex ?? ch.index

const sort = (channels: Channel[], fallbackOrder: string[] = [], builtInOrder: string[] = []) =>
  sortChannelsByPriority(channels, fallbackOrder, builtInOrder, uiKey, routeIndex)
    .map(ch => ch.name)

describe('sortChannelsByPriority', () => {
  it('按 priority 升序排列', () => {
    const result = sort([
      channel('third', 2, ['sk-c'], { priority: 3 }),
      channel('first', 0, ['sk-a'], { priority: 1 }),
      channel('second', 1, ['sk-b'], { priority: 2 }),
    ])
    expect(result).toEqual(['first', 'second', 'third'])
  })

  it('首位渠道所有 key 被拉黑后仍保持首位', () => {
    const before = [
      channel('newapi-messages-29380', 0, ['sk-new'], { priority: 1 }),
      channel('volcengine-claude', 1, ['sk-volc'], { priority: 2 }),
      channel('mimo-claude', 2, ['sk-mimo'], { priority: 3 }),
    ]
    const expected = ['newapi-messages-29380', 'volcengine-claude', 'mimo-claude']
    expect(sort(before)).toEqual(expected)

    // 拉黑后端只清空 apiKeys，priority 与 index 均不变
    const after = [
      channel('newapi-messages-29380', 0, [], { priority: 1, status: 'suspended' }),
      channel('volcengine-claude', 1, ['sk-volc'], { priority: 2 }),
      channel('mimo-claude', 2, ['sk-mimo'], { priority: 3 }),
    ]
    expect(sort(after)).toEqual(expected)
  })

  it('priority 相同时按 routeIndex 兜底，不受 key 状态影响', () => {
    const withKeys = [
      channel('tied-b', 1, ['sk-b'], { priority: 2 }),
      channel('tied-a', 0, ['sk-a'], { priority: 2 }),
    ]
    expect(sort(withKeys)).toEqual(['tied-a', 'tied-b'])

    // 靠前的渠道 key 清空后，顺序必须不变
    const aBlacklisted = [
      channel('tied-b', 1, ['sk-b'], { priority: 2 }),
      channel('tied-a', 0, [], { priority: 2 }),
    ]
    expect(sort(aBlacklisted)).toEqual(['tied-a', 'tied-b'])
  })

  it('priority 缺失时依次回退到上次顺序与内置顺序', () => {
    const channels = [
      channel('beta', 1, ['sk-b']),
      channel('alpha', 0, []),
    ]
    // fallbackOrder 指定 beta 在前
    expect(sort(channels, ['messages:1', 'messages:0'])).toEqual(['beta', 'alpha'])
    // 无 fallbackOrder 时用内置顺序
    expect(sort(channels, [], ['messages:1', 'messages:0'])).toEqual(['beta', 'alpha'])
  })

  it('不修改入参数组', () => {
    const channels = [
      channel('second', 1, ['sk-b'], { priority: 2 }),
      channel('first', 0, ['sk-a'], { priority: 1 }),
    ]
    sort(channels)
    expect(channels.map(ch => ch.name)).toEqual(['second', 'first'])
  })

  it('priority 为 0 时视为未配置，落入兜底而不排在显式 priority 之前', () => {
    const channels = [
      channel('legacy-zero', 10, ['sk-z'], { priority: 0 }),
      channel('pinned', 5, ['sk-p'], { priority: 1 }),
      channel('second', 1, ['sk-s'], { priority: 2 }),
    ]
    // 0 不参与数值比较：pinned 仍在首位，legacy-zero 按 routeIndex(10) 兜底排最后
    expect(sort(channels)).toEqual(['pinned', 'second', 'legacy-zero'])
  })
})
