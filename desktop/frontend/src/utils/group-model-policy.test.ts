import { describe, expect, it } from 'vitest'
import { groupModelPolicyKey } from './group-model-policy'

describe('groupModelPolicyKey', () => {
  it('按分组或单 Key 生成稳定标识', () => {
    expect(groupModelPolicyKey({ quotaGroup: 'vip', model: 'Model-A' })).toBe('["group","vip","model-a"]')
    expect(groupModelPolicyKey({ key: 'sk-a', model: 'Model-A' })).toBe('["key","sk-a","model-a"]')
  })
})
