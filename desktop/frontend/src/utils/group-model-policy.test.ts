import { describe, expect, it } from 'vitest'
import type { Channel, DisabledGroupModel } from '@/services/admin-api'
import {
  groupModelDisableTargets,
  groupModelPolicyKey,
  groupModelRestoreTargets,
  mergeGroupModelUnion,
} from './group-model-policy'

const record = (over: Partial<DisabledGroupModel> = {}): DisabledGroupModel => ({
  quotaGroup: 'coding',
  model: 'model-x',
  disabledAt: '2026-09-30T00:00:00Z',
  ...over,
})

const channelWith = (over: Partial<Channel> = {}): Channel => ({
  index: 3,
  apiKeys: [],
  ...over,
} as unknown as Channel)

describe('groupModelPolicyKey', () => {
  it('有配额组按组、无配额组按 Key，模型大小写与空白不敏感', () => {
    const group = groupModelPolicyKey(record({ quotaGroup: ' coding ', model: ' MODEL-X ' }))
    const groupCanonical = groupModelPolicyKey(record({ quotaGroup: 'coding', model: 'model-x' }))
    expect(group).toBe(groupCanonical)

    const byKey = groupModelPolicyKey(record({ quotaGroup: '', key: ' sk-a ', model: 'model-x' }))
    const byKeyCanonical = groupModelPolicyKey(record({ quotaGroup: '', key: 'sk-a', model: 'model-x' }))
    expect(byKey).toBe(byKeyCanonical)
    expect(byKey).not.toBe(group)
  })
})

describe('mergeGroupModelUnion', () => {
  it('合并顶层与各协议路由的记录并按策略键去重', () => {
    const shared = record()
    const chatOnly = record({ quotaGroup: 'chat-group' })
    const channel = channelWith({
      disabledGroupModels: [shared],
      protocolRoutes: [
        { kind: 'messages', index: 3, apiKeys: ['k1'], disabledGroupModels: [shared] },
        { kind: 'chat', index: 19, apiKeys: ['k1'], disabledGroupModels: [shared, chatOnly] },
      ] as never,
    })

    const merged = mergeGroupModelUnion(channel)
    expect(merged).toHaveLength(2)
    expect(merged.map(item => item.quotaGroup).sort()).toEqual(['chat-group', 'coding'])
  })

  it('没有记录时返回空数组', () => {
    expect(mergeGroupModelUnion(channelWith())).toEqual([])
  })
})

describe('groupModelRestoreTargets', () => {
  const target = record()

  it('只打真正持有该限制的路由', () => {
    const channel = channelWith({
      protocolRoutes: [
        { kind: 'messages', index: 3, apiKeys: ['k1'], disabledGroupModels: [] },
        { kind: 'chat', index: 19, apiKeys: ['k1'], disabledGroupModels: [target] },
        { kind: 'responses', index: 14, apiKeys: ['k1'], disabledGroupModels: [{ ...target, model: 'other-model' }] },
      ] as never,
    })

    expect(groupModelRestoreTargets(channel, target, { kind: 'messages', index: 3 })).toEqual([
      { kind: 'chat', index: 19 },
    ])
  })

  it('路由上都没有时回退主路由（兼容非聚合渠道与旧记录）', () => {
    const channel = channelWith({ protocolRoutes: [] as never })
    expect(groupModelRestoreTargets(channel, target, { kind: 'messages', index: 3 })).toEqual([
      { kind: 'messages', index: 3 },
    ])
  })
})

describe('groupModelDisableTargets', () => {
  it('覆盖所有持有该 Key 的协议路由', () => {
    const channel = channelWith({
      protocolRoutes: [
        { kind: 'messages', index: 3, apiKeys: ['k1'], disabledGroupModels: [] },
        { kind: 'chat', index: 19, apiKeys: ['k1'], disabledGroupModels: [] },
        { kind: 'responses', index: 14, apiKeys: ['other'], disabledGroupModels: [] },
      ] as never,
    })

    expect(groupModelDisableTargets(channel, 'k1', { kind: 'messages', index: 3 })).toEqual([
      { kind: 'messages', index: 3 },
      { kind: 'chat', index: 19 },
    ])
  })

  it('Key 只属于非主路由时打在该路由而不是主路由', () => {
    const channel = channelWith({
      protocolRoutes: [
        { kind: 'messages', index: 3, apiKeys: [], disabledGroupModels: [] },
        { kind: 'chat', index: 19, apiKeys: ['k1'], disabledGroupModels: [] },
      ] as never,
    })

    expect(groupModelDisableTargets(channel, 'k1', { kind: 'messages', index: 3 })).toEqual([
      { kind: 'chat', index: 19 },
    ])
  })

  it('没有任何路由包含该 Key 时回退主路由', () => {
    const channel = channelWith({ protocolRoutes: [] as never })
    expect(groupModelDisableTargets(channel, 'k1', { kind: 'messages', index: 3 })).toEqual([
      { kind: 'messages', index: 3 },
    ])
  })
})
