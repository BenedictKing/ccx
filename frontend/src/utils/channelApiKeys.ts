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
}

export function availableChannelApiKeyCount(channel: ChannelKeyState): number {
  return buildChannelApiKeyRows(channel.apiKeys, channel.disabledApiKeys).filter(row => !row.disabled).length
}

export function disabledChannelApiKeyCount(channel: ChannelKeyState): number {
  return buildChannelApiKeyRows(channel.apiKeys, channel.disabledApiKeys).filter(row => !!row.disabled).length
}

/**
 * 渠道是否因全部 Key 被拉黑/耗尽而可恢复（且至少存在一个禁用 Key）。
 *
 * 条件必须同时满足「可用数为 0」与「禁用数 > 0」：
 *   - 可用数 > 0：仍有可用 Key，不应误导为可恢复（保留暂停主操作）。
 *   - 禁用数 = 0：尚未配置任何 Key 或仅有手动暂停（enabled=false）的 Key，
 *     渠道恢复接口不会改变这两类状态，因此不能算作该按钮可处理的状态。
 */
export function hasOnlyDisabledChannelApiKeys(channel: ChannelKeyState): boolean {
  return availableChannelApiKeyCount(channel) === 0 && disabledChannelApiKeyCount(channel) > 0
}
