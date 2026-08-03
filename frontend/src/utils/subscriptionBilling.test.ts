import { describe, expect, it } from 'vitest'
import { billingTermsPatch, billingTermsPreview, multiplierStatusLabel } from './subscriptionBilling'

describe('subscription billing helpers', () => {
  it('renders a natural-language billing preview including zero values', () => {
    expect(billingTermsPreview({ paymentAmount: 0, paymentUnit: 'USD', creditAmount: 0, creditUnit: 'LDC' }))
      .toBe('支付 0 USD → 到账 0 LDC')
  })

  it('preserves set and reset payload semantics', () => {
    expect(billingTermsPatch({ paymentAmount: 10, paymentUnit: ' usd ', creditAmount: 500, creditUnit: ' ldc ' }, 3))
      .toEqual({ paymentAmount: 10, paymentUnit: 'USD', creditAmount: 500, creditUnit: 'LDC', expectedVersion: 3 })
    expect(billingTermsPatch({ paymentAmount: null, paymentUnit: 'USD', creditAmount: null, creditUnit: 'LDC' }))
      .toEqual({ paymentAmount: null, paymentUnit: '', creditAmount: null, creditUnit: '', expectedVersion: undefined })
  })

  it('labels all sync statuses', () => {
    expect(['fresh', 'stale', 'over_limit', 'sync_error', 'relink_required'].map(multiplierStatusLabel))
      .toEqual(['Fresh', 'Stale', 'Over limit', 'Sync error', 'Relink required'])
  })
})
