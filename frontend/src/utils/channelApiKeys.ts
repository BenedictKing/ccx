import type { DisabledKeyInfo, APIKeyConfig } from '../services/api-types'

export interface ChannelApiKeyRow {
  key: string
  activeIndex: number
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

  const enabledByKey = new Map<string, boolean | undefined>()
  for (const cfg of apiKeyConfigs ?? []) {
    enabledByKey.set(cfg.key, cfg.enabled)
  }

  const seen = new Set<string>()
  const rows: ChannelApiKeyRow[] = []

  activeKeys.forEach((key, activeIndex) => {
    if (!key || seen.has(key)) return
    seen.add(key)
    rows.push({
      key,
      activeIndex,
      disabled: disabledByKey.get(key),
      enabled: enabledByKey.get(key),
    })
  })

  for (const disabled of disabledItems) {
    if (!disabled.key || seen.has(disabled.key)) continue
    seen.add(disabled.key)
    rows.push({
      key: disabled.key,
      activeIndex: -1,
      disabled,
      enabled: enabledByKey.get(disabled.key),
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
