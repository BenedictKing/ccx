import type { ExchangeRateQuote } from '@/services/admin-api'

export function isFiniteNonNegative(value: unknown): boolean {
  return typeof value === 'number' && Number.isFinite(value) && value >= 0
}

export function eligibleNewApiGroups(groups: Record<string, number>, maximum: number) {
  if (!isFiniteNonNegative(maximum)) return []
  return Object.entries(groups)
    .filter(([, ratio]) => isFiniteNonNegative(ratio) && ratio <= maximum)
    .map(([name, ratio]) => ({ name, ratio }))
    .sort((a, b) => a.ratio - b.ratio || a.name.localeCompare(b.name))
}

export function isValidBillingAmount(value: number): boolean {
  return Number.isFinite(value) && value > 0
}

export function billingTermsPreview(
  paymentAmount?: number | null,
  paymentUnit?: string,
  creditAmount?: number | null,
  creditUnit?: string,
): string | null {
  if (!isValidBillingAmount(paymentAmount ?? NaN) || !isValidBillingAmount(creditAmount ?? NaN)) return null
  if (!paymentUnit?.trim() || !creditUnit?.trim()) return null
  return `${paymentAmount} ${paymentUnit.toUpperCase()} → ${creditAmount} ${creditUnit.toUpperCase()}`
}

export function isValidExchangeRateQuote(quote: ExchangeRateQuote): boolean {
  return Number.isFinite(quote.sourceAmount) && quote.sourceAmount > 0
    && Number.isFinite(quote.targetAmount) && quote.targetAmount > 0
    && !!quote.sourceUnit.trim() && !!quote.targetUnit.trim()
}

export const multiplierStatusI18nKey = (status: string) => `multiplier.status.${status}`

export const consumptionPolicyI18nKey = (policy: 'normal' | 'opportunistic' | '' | null | undefined): string => {
  if (!policy) return 'multiplier.consumptionPolicy.normal'
  return `multiplier.consumptionPolicy.${policy}`
}
