import { describe, expect, it } from 'vitest'
import { buildManagedChannelPatch, buildStagedKeyMultiplierConfigs, listDroppedManagedChannelFields } from './managedChannelPatch'
import type { Channel } from '../services/api'

const baseChannel = {
  name: 'managed-ch',
  apiKeys: ['sk-1'],
  costMultiplier: 2,
  maxGroupMultiplier: 1.5,
  channelPaymentCurrency: 'CNY',
  channelPaymentAmount: 100,
  channelCreditCurrency: 'USD',
  channelCreditAmount: 15,
  proxyUrl: 'http://127.0.0.1:7890',
  customHeaders: { 'x-test': '1' },
  modelMapping: {},
  fastMode: false,
  tags: ['a'],
} as unknown as Channel

describe('buildManagedChannelPatch', () => {
  it('无变化时返回空对象', () => {
    expect(buildManagedChannelPatch(baseChannel, { ...baseChannel })).toEqual({})
  })

  it('表单字符串与视图数字同口径不误判，真变化才进 patch', () => {
    const patch = buildManagedChannelPatch(baseChannel, {
      ...baseChannel,
      maxGroupMultiplier: '1.5',
      costMultiplier: '3',
    })
    expect(patch).toEqual({ costMultiplier: 3 })
  })

  it('清空表单值归一化为清除口径（0/空串/空对象）', () => {
    const patch = buildManagedChannelPatch(baseChannel, {
      ...baseChannel,
      costMultiplier: '',
      maxGroupMultiplier: null,
      channelPaymentCurrency: '  ',
      channelPaymentAmount: 0,
      proxyUrl: '',
      customHeaders: undefined,
    })
    expect(patch).toEqual({
      costMultiplier: 0,
      maxGroupMultiplier: 0,
      channelPaymentCurrency: '',
      channelPaymentAmount: 0,
      proxyUrl: '',
      customHeaders: {},
    })
  })

  it('customHeaders 增删键都会产生 patch（结构比较）', () => {
    expect(buildManagedChannelPatch(baseChannel, { ...baseChannel, customHeaders: { 'x-test': '1', 'x-new': '2' } }))
      .toEqual({ customHeaders: { 'x-test': '1', 'x-new': '2' } })
    expect(buildManagedChannelPatch(baseChannel, { ...baseChannel, customHeaders: {} }))
      .toEqual({ customHeaders: {} })
  })

  it('协议成员映射新增和清空均通过单卡 patch 保存', () => {
    const mapped = { 'ccx-acceptance-source': 'ccx-acceptance-target' }
    expect(buildManagedChannelPatch(baseChannel, { ...baseChannel, modelMapping: mapped }))
      .toEqual({ modelMapping: mapped })
    expect(buildManagedChannelPatch({ ...baseChannel, modelMapping: mapped }, { ...baseChannel, modelMapping: {} }))
      .toEqual({ modelMapping: {} })
  })

  it('original 为空时不产生 patch', () => {
    expect(buildManagedChannelPatch(null, { costMultiplier: 3 })).toEqual({})
    expect(buildManagedChannelPatch(undefined, { costMultiplier: 3 })).toEqual({})
  })
})

describe('listDroppedManagedChannelFields', () => {
  it('账号接口与白名单字段承载的变化不报丢弃', () => {
    const dropped = listDroppedManagedChannelFields(baseChannel, {
      ...baseChannel,
      name: 'renamed',
      apiKeys: ['sk-2'],
      proxyUrl: 'http://127.0.0.2:7890',
      costMultiplier: 5,
    })
    expect(dropped).toEqual([])
  })

  it('未承载字段变化时报丢弃（debug 兜底信号）', () => {
    const dropped = listDroppedManagedChannelFields(baseChannel, {
      ...baseChannel,
      fastMode: true,
      tags: ['a', 'b'],
    })
    expect(dropped).toEqual(['fastMode', 'tags'])
  })

  it('数字与数字字符串宽松相等不误报', () => {
    const dropped = listDroppedManagedChannelFields(baseChannel, {
      ...baseChannel,
      channelPaymentAmount: '100',
    })
    expect(dropped).toEqual([])
  })
})

describe('buildStagedKeyMultiplierConfigs', () => {
  const original = {
    ...baseChannel,
    apiKeyConfigs: [
      { key: 'sk-1', keyUid: 'uid-1', groupMultiplier: 1, consumptionPolicy: 'normal' },
      { key: 'sk-2', keyUid: 'uid-2', groupMultiplier: 2 },
    ],
  } as unknown as Channel

  it('倍率/策略无差异时返回 null', () => {
    expect(buildStagedKeyMultiplierConfigs(original, {
      ...original,
      apiKeyConfigs: [{ key: 'sk-1', keyUid: 'uid-1', groupMultiplier: '1', consumptionPolicy: 'normal' }],
    })).toBeNull()
  })

  it('倍率变化返回定位+倍率字段的 trimmed 配置', () => {
    const staged = buildStagedKeyMultiplierConfigs(original, {
      ...original,
      apiKeyConfigs: [
        { key: 'sk-1', keyUid: 'uid-1', groupMultiplier: 1, consumptionPolicy: 'normal' },
        { key: 'sk-2', keyUid: 'uid-2', groupMultiplier: 0.5, consumptionPolicy: 'opportunistic' },
      ],
    })
    expect(staged).toEqual([
      { key: 'sk-2', keyUid: 'uid-2', credentialUid: undefined, groupMultiplier: 0.5, consumptionPolicy: 'opportunistic' },
    ])
  })

  it('清空倍率（null）也是有效差异', () => {
    const staged = buildStagedKeyMultiplierConfigs(original, {
      ...original,
      apiKeyConfigs: [{ key: 'sk-1', keyUid: 'uid-1', groupMultiplier: null, consumptionPolicy: 'normal' }],
    })
    expect(staged).toEqual([
      { key: 'sk-1', keyUid: 'uid-1', credentialUid: undefined, groupMultiplier: null, consumptionPolicy: 'normal' },
    ])
  })

  it('original 为空或无 configs 时返回 null', () => {
    expect(buildStagedKeyMultiplierConfigs(null, { apiKeyConfigs: [{ key: 'k' }] })).toBeNull()
    expect(buildStagedKeyMultiplierConfigs(original, {})).toBeNull()
  })
})
