import type { APIKeyConfig, DisabledKeyInfo } from '@/services/admin-api'

export interface ChannelApiKeyRow {
  key: string
  activeIndex: number
  disabled?: DisabledKeyInfo
  config?: APIKeyConfig
}

export function buildChannelApiKeyRows(
  apiKeys: string[] | null | undefined = [],
  disabledKeys: DisabledKeyInfo[] | null | undefined = [],
  apiKeyConfigs: APIKeyConfig[] | null | undefined = [],
): ChannelApiKeyRow[] {
  const activeKeys = apiKeys ?? []
  const disabledItems = disabledKeys ?? []
  const keyConfigs = apiKeyConfigs ?? []
  const disabledByKey = new Map(disabledItems.filter(item => item.key).map(item => [item.key, item]))
  const configByKey = new Map(keyConfigs.filter(item => item.key).map(item => [item.key, item]))
  const seen = new Set<string>()
  const rows: ChannelApiKeyRow[] = []

  activeKeys.forEach((key, activeIndex) => {
    if (!key || seen.has(key)) return
    seen.add(key)
    rows.push({ key, activeIndex, disabled: disabledByKey.get(key), config: configByKey.get(key) })
  })

  for (const config of keyConfigs) {
    if (!config.key || seen.has(config.key)) continue
    seen.add(config.key)
    rows.push({ key: config.key, activeIndex: -1, disabled: disabledByKey.get(config.key), config })
  }

  for (const disabled of disabledItems) {
    if (!disabled.key || seen.has(disabled.key)) continue
    seen.add(disabled.key)
    rows.push({ key: disabled.key, activeIndex: -1, disabled, config: disabled.config })
  }

  return rows
}

type ChannelKeyState = {
  apiKeys?: string[] | null
  disabledApiKeys?: DisabledKeyInfo[] | null
  apiKeyConfigs?: APIKeyConfig[] | null
}

export function availableChannelApiKeyCount(channel: ChannelKeyState): number {
  return buildChannelApiKeyRows(channel.apiKeys, channel.disabledApiKeys, channel.apiKeyConfigs).filter(row => !row.disabled).length
}

export function disabledChannelApiKeyCount(channel: ChannelKeyState): number {
  return buildChannelApiKeyRows(channel.apiKeys, channel.disabledApiKeys, channel.apiKeyConfigs).filter(row => !!row.disabled).length
}
