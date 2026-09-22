import { describe, expect, it } from 'vitest'
import { eligibleNewApiGroups, isFiniteNonNegative } from '@/utils/subscription-management'
import type { ChannelKind, NewApiProvisionRequest, NewApiVerifyRequest } from '@/services/admin-api'

describe('NewApiSubscriptionForm contract', () => {
  it('uses raw_auth and supports all six channel kinds', () => {
    const authTokenMode: NewApiProvisionRequest['authTokenMode'] = 'raw_auth'
    const kinds: ChannelKind[] = ['messages', 'chat', 'responses', 'gemini', 'images', 'vectors']
    expect(authTokenMode).toBe('raw_auth')
    expect(kinds).toHaveLength(6)
  })

  it('validates user-defined finite maximum and filters eligible groups', () => {
    expect(isFiniteNonNegative(0)).toBe(true)
    expect(isFiniteNonNegative(Infinity)).toBe(false)
    expect(eligibleNewApiGroups({ default: 1, premium: 2 }, 1)).toEqual([{ name: 'default', ratio: 1 }])
  })

  it('models the all-eligible-groups payload', () => {
    const payload: Pick<NewApiProvisionRequest, 'provisionAllEligibleGroups' | 'maxGroupMultiplier'> = {
      provisionAllEligibleGroups: true,
      maxGroupMultiplier: 1.25,
    }
    expect(payload).toEqual({ provisionAllEligibleGroups: true, maxGroupMultiplier: 1.25 })
  })

  it('carries proxy settings on verify and provision payloads', () => {
    // verify/provision 均透传 proxyUrl + proxyPreferDirect；未配置代理时直连优先开关不生效
    const verify: NewApiVerifyRequest = {
      baseUrl: 'https://example.com', accessToken: 'sk-test',
      proxyUrl: 'socks5://127.0.0.1:7890', proxyPreferDirect: true,
    }
    const provision: Pick<NewApiProvisionRequest, 'proxyUrl' | 'proxyPreferDirect'> = {
      proxyUrl: verify.proxyUrl, proxyPreferDirect: verify.proxyPreferDirect,
    }
    expect(verify.proxyUrl).toBe('socks5://127.0.0.1:7890')
    expect(provision).toEqual({ proxyUrl: 'socks5://127.0.0.1:7890', proxyPreferDirect: true })
    // 空代理直连：proxyUrl 缺省、直连优先不透传
    const direct: NewApiVerifyRequest = { baseUrl: 'https://example.com', accessToken: 'sk-test' }
    expect(direct.proxyUrl ?? '').toBe('')
    expect(direct.proxyPreferDirect).toBeUndefined()
  })
})
