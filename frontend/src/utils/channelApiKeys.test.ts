import { describe, expect, it } from 'vitest'
import {
  availableChannelApiKeyCount,
  disabledChannelApiKeyCount,
  groupModelPolicyKey,
  pausedChannelApiKeyCount,
} from './channelApiKeys'

describe('渠道 Key 状态计数', () => {
  it('分别统计可用、暂停和拉黑 Key，并处理重复及重叠记录', () => {
    const channel = {
      apiKeys: ['active', 'paused', 'blocked', 'active'],
      apiKeyConfigs: [{ key: 'paused', enabled: false }],
      disabledApiKeys: [
        { key: 'blocked', reason: 'invalid', message: '', disabledAt: '' },
        { key: 'historical', reason: 'invalid', message: '', disabledAt: '' },
      ],
    }

    expect(availableChannelApiKeyCount(channel)).toBe(1)
    expect(pausedChannelApiKeyCount(channel)).toBe(1)
    expect(disabledChannelApiKeyCount(channel)).toBe(2)
  })

  it('分组模型策略按组或单 Key 生成稳定标识', () => {
    expect(groupModelPolicyKey({ quotaGroup: 'vip', model: 'Model-A' })).toBe('["group","vip","model-a"]')
    expect(groupModelPolicyKey({ key: 'sk-a', model: 'Model-A' })).toBe('["key","sk-a","model-a"]')
  })
})
