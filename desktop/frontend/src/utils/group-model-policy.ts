import type { DisabledGroupModelInfo } from '@/services/admin-api'

export function groupModelPolicyKey(record: Pick<DisabledGroupModelInfo, 'quotaGroup' | 'key' | 'model'>): string {
  const group = record.quotaGroup?.trim() || ''
  return JSON.stringify([
    group ? 'group' : 'key',
    group || record.key?.trim() || '',
    record.model.trim().toLowerCase(),
  ])
}
