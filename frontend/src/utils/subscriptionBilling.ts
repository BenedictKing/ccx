import type { BillingTermsPatch, SubscriptionItem } from '@/services/api-types'

export function billingTermsPreview(item: Pick<SubscriptionItem, 'paymentAmount' | 'paymentUnit' | 'creditAmount' | 'creditUnit'>): string {
  if (item.paymentAmount == null || item.creditAmount == null || !item.paymentUnit || !item.creditUnit) return '-'
  return `支付 ${item.paymentAmount} ${item.paymentUnit} → 到账 ${item.creditAmount} ${item.creditUnit}`
}

export function billingTermsPatch(values: { paymentAmount: number | null; paymentUnit: string; creditAmount: number | null; creditUnit: string }, expectedVersion?: number): BillingTermsPatch {
  const reset = values.paymentAmount == null && values.creditAmount == null
  return {
    paymentAmount: reset ? null : values.paymentAmount,
    paymentUnit: reset ? '' : values.paymentUnit.trim().toUpperCase(),
    creditAmount: reset ? null : values.creditAmount,
    creditUnit: reset ? '' : values.creditUnit.trim().toUpperCase(),
    expectedVersion,
  }
}

export function multiplierStatusLabel(status: string): string {
  return ({
    fresh: 'Fresh',
    stale: 'Stale',
    over_limit: 'Over limit',
    sync_error: 'Sync error',
    relink_required: 'Relink required',
    remote_group_missing: 'Remote group missing',
    manual: 'Manual',
  } as Record<string, string>)[status] || status || '-'
}
