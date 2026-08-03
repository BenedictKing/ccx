import type { ExchangeRateQuote } from '@/services/api-types'

export function defaultExchangeRateQuotes(): ExchangeRateQuote[] {
  return [
    { sourceAmount: 1, sourceUnit: 'USD', targetAmount: 7, targetUnit: 'CNY' },
    { sourceAmount: 500, sourceUnit: 'LDC', targetAmount: 10, targetUnit: 'CNY' },
  ]
}

export function normalizeExchangeRateQuotes(quotes: ExchangeRateQuote[]): ExchangeRateQuote[] {
  return quotes.map(quote => ({
    ...quote,
    sourceUnit: quote.sourceUnit.trim().toUpperCase(),
    targetUnit: quote.targetUnit.trim().toUpperCase(),
    note: quote.note?.trim() || undefined,
  }))
}
