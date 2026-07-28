import { describe, expect, it } from 'vitest'
import {
  availableChannelApiKeyCount,
  buildChannelApiKeyRows,
  disabledChannelApiKeyCount,
  hasNoUsableChannelApiKeys,
  pausedChannelApiKeyCount,
  hasOnlyDisabledChannelApiKeys,
} from './channelApiKeys'

describe('channel API key state', () => {
  it('拉黑记录优先于自动托管渠道仍注入的同名 Key', () => {
    const channel = {
      apiKeys: ['key-active', 'key-disabled'],
      disabledApiKeys: [{
        key: 'key-disabled',
        reason: 'insufficient_quota',
        message: 'quota exhausted',
        disabledAt: '2026-07-17T17:44:34+08:00',
      }],
    }

    expect(availableChannelApiKeyCount(channel)).toBe(1)
    expect(disabledChannelApiKeyCount(channel)).toBe(1)
  })

  it('保留仅存在于拉黑列表中的 Key，并按 Key 去重', () => {
    const disabled = {
      key: 'key-disabled',
      reason: 'authentication_error',
      message: 'invalid key',
      disabledAt: '2026-07-17T17:44:34+08:00',
    }
    const rows = buildChannelApiKeyRows(['key-active', 'key-active'], [disabled, disabled])

    expect(rows.map(row => row.key)).toEqual(['key-active', 'key-disabled'])
    expect(rows[1].activeIndex).toBe(-1)
    expect(rows[1].disabled).toBe(disabled)
  })

  it('兼容后端 JSON 中的 null 列表', () => {
    expect(buildChannelApiKeyRows(null, null)).toEqual([])
    expect(availableChannelApiKeyCount({ apiKeys: ['key-active'], disabledApiKeys: null })).toBe(1)
  })

  it('可用 Key 数量排除手动暂停项', () => {
    const channel = {
      apiKeys: ['key-active', 'key-suspended'],
      disabledApiKeys: [],
      apiKeyConfigs: [{ key: 'key-suspended', enabled: false }],
    }
    expect(availableChannelApiKeyCount(channel)).toBe(1)
    expect(pausedChannelApiKeyCount(channel)).toBe(1)
  })
})

describe('hasOnlyDisabledChannelApiKeys', () => {
  const disabledKey = (key: string) => ({
    key,
    reason: 'authentication_error',
    message: 'invalid key',
    disabledAt: '2026-07-17T17:44:34+08:00',
  })

  it('可用数为 0 且禁用数 > 0 时返回 true', () => {
    const channel = {
      apiKeys: ['key-a', 'key-b'],
      disabledApiKeys: [disabledKey('key-a'), disabledKey('key-b')],
    }
    expect(hasOnlyDisabledChannelApiKeys(channel)).toBe(true)
  })

  it('部分 Key 仍可用时返回 false', () => {
    const channel = {
      apiKeys: ['key-ok', 'key-down'],
      disabledApiKeys: [disabledKey('key-down')],
    }
    expect(hasOnlyDisabledChannelApiKeys(channel)).toBe(false)
  })

  it('无任何 Key（空配置）时返回 false', () => {
    expect(hasOnlyDisabledChannelApiKeys({ apiKeys: [], disabledApiKeys: [] })).toBe(false)
    expect(hasOnlyDisabledChannelApiKeys({ apiKeys: null, disabledApiKeys: null })).toBe(false)
  })

  it('拉黑记录优先于同名活跃 Key：同名 Key 全部以 disabled 计入', () => {
    const channel = {
      apiKeys: ['key-x'],
      disabledApiKeys: [disabledKey('key-x')],
    }
    expect(hasOnlyDisabledChannelApiKeys(channel)).toBe(true)
  })

  it('仅存在活跃 Key 无禁用记录时返回 false', () => {
    const channel = { apiKeys: ['key-a'], disabledApiKeys: [] }
    expect(hasOnlyDisabledChannelApiKeys(channel)).toBe(false)
  })

  it('暂停与拉黑混合时不视为可由恢复拉黑操作完全处理', () => {
    const channel = {
      apiKeys: ['key-paused', 'key-disabled'],
      disabledApiKeys: [disabledKey('key-disabled')],
      apiKeyConfigs: [{ key: 'key-paused', enabled: false }],
    }
    expect(hasOnlyDisabledChannelApiKeys(channel)).toBe(false)
  })
})

describe('hasNoUsableChannelApiKeys', () => {
  const disabledKey = (key: string) => ({
    key,
    reason: 'authentication_error',
    message: 'invalid key',
    disabledAt: '2026-07-17T17:44:34+08:00',
  })

  it('全部拉黑时返回 true', () => {
    expect(hasNoUsableChannelApiKeys({
      apiKeys: ['key-a', 'key-b'],
      disabledApiKeys: [disabledKey('key-a'), disabledKey('key-b')],
    })).toBe(true)
  })

  it('全部暂停时返回 true', () => {
    expect(hasNoUsableChannelApiKeys({
      apiKeys: ['key-a', 'key-b'],
      apiKeyConfigs: [
        { key: 'key-a', enabled: false },
        { key: 'key-b', enabled: false },
      ],
    })).toBe(true)
  })

  it('暂停与拉黑混合且无可用 Key 时返回 true', () => {
    expect(hasNoUsableChannelApiKeys({
      apiKeys: ['key-paused', 'key-disabled'],
      disabledApiKeys: [disabledKey('key-disabled')],
      apiKeyConfigs: [{ key: 'key-paused', enabled: false }],
    })).toBe(true)
  })

  it('至少存在一个正常 Key 时返回 false', () => {
    expect(hasNoUsableChannelApiKeys({
      apiKeys: ['key-active', 'key-paused'],
      apiKeyConfigs: [{ key: 'key-paused', enabled: false }],
    })).toBe(false)
  })

  it('从未配置 Key 时返回 false', () => {
    expect(hasNoUsableChannelApiKeys({ apiKeys: [], disabledApiKeys: [] })).toBe(false)
    expect(hasNoUsableChannelApiKeys({ apiKeys: null, disabledApiKeys: null })).toBe(false)
  })
})
