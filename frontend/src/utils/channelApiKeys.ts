import type { DisabledKeyInfo, APIKeyConfig } from '../services/api-types'

export interface ChannelApiKeyRow extends Omit<APIKeyConfig, 'key'> {
  key: string
  activeIndex: number
  keyUid?: string
  quotaGroup?: string
  groupMultiplier?: number | null
  maxGroupMultiplier?: number | null
  multiplierSource?: 'manual' | 'new_api' | 'provider'
  consumptionPolicy?: 'normal' | 'opportunistic' | null
  effectiveCostClass?: 'zero' | 'discounted' | 'standard' | 'premium' | 'unknown'
  multiplierUpdatedAt?: string
  multiplierExpiresAt?: string
  multiplierSyncStatus?: string
  multiplierSyncError?: string
  eligible?: boolean
  ineligibleReason?: string
  disabled?: DisabledKeyInfo
  /** undefined = 默认活跃，true = 显式启用，false = 手动暂停 */
  enabled?: boolean
}

export function buildChannelApiKeyRows(
  apiKeys: string[] | null | undefined = [],
  disabledKeys: DisabledKeyInfo[] | null | undefined = [],
  apiKeyConfigs?: APIKeyConfig[] | null,
): ChannelApiKeyRow[] {
  const activeKeys = apiKeys ?? []
  const disabledItems = disabledKeys ?? []
  const disabledByKey = new Map(disabledItems.filter(item => item.key).map(item => [item.key, item]))

  const configByKey = new Map<string, APIKeyConfig>()
  for (const cfg of apiKeyConfigs ?? []) {
    if (cfg.key) configByKey.set(cfg.key, cfg)
  }

  const seen = new Set<string>()
  const rows: ChannelApiKeyRow[] = []

  activeKeys.forEach((key, activeIndex) => {
    if (!key || seen.has(key)) return
    seen.add(key)
    rows.push({
      ...configByKey.get(key),
      key,
      activeIndex,
      disabled: disabledByKey.get(key),
    })
  })

  for (const disabled of disabledItems) {
    if (!disabled.key || seen.has(disabled.key)) continue
    seen.add(disabled.key)
    rows.push({
      ...configByKey.get(disabled.key),
      key: disabled.key,
      activeIndex: -1,
      disabled,
    })
  }

  return rows
}

type ChannelKeyState = {
  apiKeys?: string[] | null
  disabledApiKeys?: DisabledKeyInfo[] | null
  apiKeyConfigs?: APIKeyConfig[] | null
}

export function availableChannelApiKeyCount(channel: ChannelKeyState): number {
  return buildChannelApiKeyRows(channel.apiKeys, channel.disabledApiKeys, channel.apiKeyConfigs)
    .filter(row => !row.disabled && row.enabled !== false)
    .length
}

export function pausedChannelApiKeyCount(channel: ChannelKeyState): number {
  return buildChannelApiKeyRows(channel.apiKeys, channel.disabledApiKeys, channel.apiKeyConfigs)
    .filter(row => !row.disabled && row.enabled === false)
    .length
}

export function disabledChannelApiKeyCount(channel: ChannelKeyState): number {
  return buildChannelApiKeyRows(channel.apiKeys, channel.disabledApiKeys).filter(row => !!row.disabled).length
}

/** 渠道是否仅包含拉黑 Key，可由“恢复渠道”操作完全处理。 */
export function hasOnlyDisabledChannelApiKeys(channel: ChannelKeyState): boolean {
  const rows = buildChannelApiKeyRows(channel.apiKeys, channel.disabledApiKeys, channel.apiKeyConfigs)
  return rows.length > 0 && rows.every(row => !!row.disabled)
}

/** 渠道至少配置过一个 Key，且所有 Key 均处于手动暂停或拉黑状态。 */
export function hasNoUsableChannelApiKeys(channel: ChannelKeyState): boolean {
  const rows = buildChannelApiKeyRows(channel.apiKeys, channel.disabledApiKeys, channel.apiKeyConfigs)
  return rows.length > 0 && rows.every(row => !!row.disabled || row.enabled === false)
}
