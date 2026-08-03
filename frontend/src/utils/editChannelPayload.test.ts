import { describe, expect, it } from 'vitest'
import type { Channel } from '../services/api'
import { extractEditChannelPayloadFields } from './editChannelPayload'

describe('extractEditChannelPayloadFields', () => {
  it('保留 apiKeyConfigs 的完整服务端元数据与扩展字段', () => {
    const apiKeyConfigs = [{
      key: 'key-a',
      keyUid: 'uid-a',
      credentialUid: 'credential-a',
      groupMultiplier: null,
      maxGroupMultiplier: 0,
      multiplierSource: 'subscription',
      multiplierUpdatedAt: 'updated',
      multiplierExpiresAt: 'expires',
      multiplierSyncStatus: 'ok',
      multiplierSyncError: '',
      sourceSubscriptionUid: 'subscription-a',
      sourceRemoteTokenId: 'remote-a',
      eligible: false,
      ineligibleReason: 'expired',
      futureField: { preserved: true },
    }]
    const channel = { name: 'channel', apiKeyConfigs } as unknown as Channel

    const result = extractEditChannelPayloadFields(channel)

    expect(result.apiKeyConfigs).toBe(apiKeyConfigs)
    expect(result.apiKeyConfigs).toEqual(apiKeyConfigs)
  })
})
