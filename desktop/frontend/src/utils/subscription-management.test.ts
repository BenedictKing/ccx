import { describe, expect, it } from 'vitest'
import {
  billingTermsPreview,
  eligibleNewApiGroups,
  isFiniteNonNegative,
  isValidExchangeRateQuote,
  multiplierStatusI18nKey,
  consumptionPolicyI18nKey,
} from './subscription-management'
import {
  EXCHANGE_RATES_PATH,
  keyMultiplierPath,
  groupModelDisablePath,
  groupModelRestorePath,
  subscriptionBillingTermsPath,
  subscriptionRefreshPath,
} from '@/services/admin-api'

describe('admin management API paths', () => {
  it('matches backend atomic endpoints', () => {
    expect(keyMultiplierPath('images', 'channel/a', 'key/b')).toBe('/api/images/channels/channel%2Fa/keys/key%2Fb/multiplier')
    expect(groupModelDisablePath('messages', 3)).toBe('/api/messages/channels/3/keys/group-model/disable')
    expect(groupModelRestorePath('responses', 'channel/a')).toBe('/api/responses/channels/channel%2Fa/keys/group-model/restore')
    expect(subscriptionBillingTermsPath('sub/a')).toBe('/api/subscriptions/sub%2Fa/billing-terms')
    expect(subscriptionRefreshPath('sub/a')).toBe('/api/subscriptions/sub%2Fa/refresh')
    expect(EXCHANGE_RATES_PATH).toBe('/api/autopilot/cost/exchange-rates')
  })
})

describe('new-api provisioning validation', () => {
  it('accepts finite non-negative maximums and filters eligible groups', () => {
    expect(isFiniteNonNegative(0)).toBe(true)
    expect(isFiniteNonNegative(Number.POSITIVE_INFINITY)).toBe(false)
    expect(eligibleNewApiGroups({ premium: 1.5, default: 1, free: 0 }, 1)).toEqual([
      { name: 'free', ratio: 0 },
      { name: 'default', ratio: 1 },
    ])
  })
})

describe('billing, exchange rate and status helpers', () => {
  it('builds structured billing preview', () => {
    expect(billingTermsPreview(10, 'cny', 1.4, 'usd')).toBe('10 CNY → 1.4 USD')
    expect(billingTermsPreview(0, 'CNY', 1, 'USD')).toBeNull()
  })

  it('validates quote inputs', () => {
    expect(isValidExchangeRateQuote({ sourceAmount: 7.2, sourceUnit: 'CNY', targetAmount: 1, targetUnit: 'USD' })).toBe(true)
    expect(isValidExchangeRateQuote({ sourceAmount: Infinity, sourceUnit: 'CNY', targetAmount: 1, targetUnit: 'USD' })).toBe(false)
  })

  it('maps backend status to i18n keys', () => {
    expect(multiplierStatusI18nKey('over_limit')).toBe('multiplier.status.over_limit')
  })

  it('maps consumption policy to i18n keys for all three states', () => {
    expect(consumptionPolicyI18nKey(undefined)).toBe('multiplier.consumptionPolicy.normal')
    expect(consumptionPolicyI18nKey(null)).toBe('multiplier.consumptionPolicy.normal')
    expect(consumptionPolicyI18nKey('')).toBe('multiplier.consumptionPolicy.normal')
    expect(consumptionPolicyI18nKey('normal')).toBe('multiplier.consumptionPolicy.normal')
    expect(consumptionPolicyI18nKey('opportunistic')).toBe('multiplier.consumptionPolicy.opportunistic')
  })
})
