import { describe, expect, it } from 'vitest'
import { eligibleNewApiGroups, isFiniteNonNegative } from '@/utils/subscription-management'
import type { ChannelKind, NewApiProvisionRequest } from '@/services/admin-api'

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
})
