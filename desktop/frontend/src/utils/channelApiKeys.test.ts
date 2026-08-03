import { describe, expect, it } from 'vitest'
import type { APIKeyConfig } from '@/services/admin-api'
import {
  availableChannelApiKeyCount,
  buildChannelApiKeyRows,
  disabledChannelApiKeyCount,
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

  it('保留 keyUid-only skeleton 与未知扩展字段', () => {
    const config: APIKeyConfig = {
      key: 'key-skeleton',
      keyUid: 'ku_1',
      credentialUid: 'cred_1',
      eligible: true,
      extraField: 'keep-me',
    }

    const rows = buildChannelApiKeyRows([], [], [config])

    expect(rows).toEqual([{
      key: 'key-skeleton',
      activeIndex: -1,
      disabled: undefined,
      config,
    }])
  })

  it('删除目标 key 时保留其他 key 的 config 关联', () => {
    const keepConfig: APIKeyConfig = {
      key: 'key-keep',
      keyUid: 'ku_keep',
      credentialUid: 'cred_keep',
      groupMultiplier: 0,
    }
    const removeConfig: APIKeyConfig = {
      key: 'key-remove',
      keyUid: 'ku_remove',
      credentialUid: 'cred_remove',
      extraField: 'drop-me',
    }

    const rows = buildChannelApiKeyRows(['key-keep'], [], [keepConfig, removeConfig])

    expect(rows.map(row => row.key)).toEqual(['key-keep', 'key-remove'])
    expect(rows[0].config).toBe(keepConfig)
    expect(rows[1].config).toBe(removeConfig)
  })
})
