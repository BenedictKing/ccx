import type { Channel } from '@/services/api'
import { reuseUnchangedItemsByKey } from './structuralSharing'

/**
 * 渠道数据合并工具
 *
 * 职责：
 * 1. 将新拉取的 channels 与本地缓存的 latency 测试结果合并（5 分钟有效期内保留本地 latency）
 * 2. 冻结不可变配置集合，避免 Vue 深度 Proxy 化
 *
 * 抽离为独立模块便于单元测试，原闭包版本在 stores/channel.ts。
 */

export const LATENCY_VALID_DURATION = 5 * 60 * 1000 // 5 分钟有效期

const IMMUTABLE_CHANNEL_FIELDS = [
  'baseUrls',
  'apiKeys',
  'apiKeyConfigs',
  'disabledApiKeys',
  'disabledKeyModels',
  'historicalApiKeys',
  'modelMapping',
  'modelCapabilities',
  'embeddingCapabilities',
  'defaultCapability',
  'reasoningMapping',
  'customHeaders',
  'supportedModels',
  'noVisionModels',
  'tags',
  'protocolCapsules',
  'protocolRoutes',
] as const satisfies ReadonlyArray<keyof Channel>

/**
 * 冻结单个 channel 的不可变字段。
 *
 * 风险控制：编辑对话框提交时发送的是全新对象，不会修改这些冻结字段，安全。
 */
export function freezeImmutableFields(ch: Channel): Channel {
  for (const field of IMMUTABLE_CHANNEL_FIELDS) {
    const value = ch[field]
    if (value && typeof value === 'object' && !Object.isFrozen(value)) {
      Object.freeze(value)
    }
  }
  return ch
}

/**
 * 合并新拉取的 channels 与本地缓存，保留本地 latency 测试结果。
 *
 * 优化：existingChannels 用 Map 预索引，避免每次 .find 的 O(n) 遍历，
 *       N 个 channel 合并从 O(N²) → O(N)。
 *
 * @param now 当前时间戳（可注入便于测试）
 */
export function mergeChannelsWithLocalData(
  newChannels: Channel[],
  existingChannels: Channel[] | undefined,
  now: number = Date.now()
): Channel[] {
  const channels = deduplicateChannelsByIndex(newChannels)

  if (!existingChannels) {
    // 首次加载，直接冻结每个 channel 的不可变字段
    for (let i = 0; i < channels.length; i++) freezeImmutableFields(channels[i])
    return channels
  }

  // 预索引 existingChannels，避免每次 .find 的 O(n) 遍历
  const existingByIndex = new Map<number, Channel>()
  for (const ch of existingChannels) existingByIndex.set(ch.index, ch)

  const validSince = now - LATENCY_VALID_DURATION

  const mergedChannels = channels.map(newCh => {
    const existingCh = existingByIndex.get(newCh.index)
    // 只有在 5 分钟有效期内才保留本地延迟测试结果
    if (existingCh?.latencyTestTime && existingCh.latencyTestTime > validSince) {
      const merged = {
        ...newCh,
        latency: existingCh.latency,
        latencyTestTime: existingCh.latencyTestTime
      }
      return freezeImmutableFields(merged)
    }
    return freezeImmutableFields(newCh)
  })

  return reuseUnchangedItemsByKey(mergedChannels, existingChannels, channel => channel.index)
}

function deduplicateChannelsByIndex(channels: Channel[]): Channel[] {
  const seen = new Set<number>()
  let result: Channel[] | undefined

  for (let i = 0; i < channels.length; i++) {
    const channel = channels[i]
    if (!Number.isInteger(channel.index) || channel.index < 0 || seen.has(channel.index)) {
      result ??= channels.slice(0, i)
      continue
    }
    seen.add(channel.index)
    result?.push(channel)
  }

  return result ?? channels
}
