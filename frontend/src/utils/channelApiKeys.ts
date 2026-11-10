import type { APIKeyConfig, DisabledGroupModelInfo, DisabledKeyInfo } from '../services/api-types'

export function groupModelPolicyKey(record: Pick<DisabledGroupModelInfo, 'quotaGroup' | 'key' | 'model'>): string {
  const group = record.quotaGroup?.trim() || ''
  return JSON.stringify([
    group ? 'group' : 'key',
    group || record.key?.trim() || '',
    record.model.trim().toLowerCase(),
  ])
}

type ChannelKeyState = {
  apiKeys?: string[] | null
  disabledApiKeys?: DisabledKeyInfo[] | null
  apiKeyConfigs?: APIKeyConfig[] | null
}

function keyConfig(channel: ChannelKeyState, key: string) {
  return channel.apiKeyConfigs?.find(config => config.key === key)
}

export function availableChannelApiKeyCount(channel: ChannelKeyState): number {
  const disabled = new Set((channel.disabledApiKeys || []).map(item => item.key))
  return new Set(channel.apiKeys || []).size - [...new Set(channel.apiKeys || [])]
    .filter(key => disabled.has(key) || keyConfig(channel, key)?.enabled === false)
    .length
}

export function pausedChannelApiKeyCount(channel: ChannelKeyState): number {
  const disabled = new Set((channel.disabledApiKeys || []).map(item => item.key))
  return [...new Set(channel.apiKeys || [])]
    .filter(key => !disabled.has(key) && keyConfig(channel, key)?.enabled === false)
    .length
}

export function disabledChannelApiKeyCount(channel: ChannelKeyState): number {
  return new Set((channel.disabledApiKeys || []).map(item => item.key)).size
}
