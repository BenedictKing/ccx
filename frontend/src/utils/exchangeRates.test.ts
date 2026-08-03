import { describe, expect, it } from 'vitest'
import { defaultExchangeRateQuotes, normalizeExchangeRateQuotes } from './exchangeRates'

describe('exchange rates', () => {
  it('provides the documented defaults', () => {
    expect(defaultExchangeRateQuotes()).toEqual([
      { sourceAmount: 1, sourceUnit: 'USD', targetAmount: 7, targetUnit: 'CNY' },
      { sourceAmount: 500, sourceUnit: 'LDC', targetAmount: 10, targetUnit: 'CNY' },
    ])
  })

  it('normalizes units without changing amounts', () => {
    expect(normalizeExchangeRateQuotes([{ sourceAmount: 0, sourceUnit: ' usd ', targetAmount: 7, targetUnit: ' cny ' }]))
      .toEqual([{ sourceAmount: 0, sourceUnit: 'USD', targetAmount: 7, targetUnit: 'CNY', note: undefined }])
  })
})
