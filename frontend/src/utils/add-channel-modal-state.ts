import type { Channel } from '@/services/api'
import { deduplicateEquivalentBaseUrls, type ServiceType } from './baseUrlSemantics'

export type ChannelWatcherAction = 'load-edit-channel' | 'reset-new-form' | 'noop'

export function resolveChannelWatcherAction(params: {
  show: boolean
  newChannel: Channel | null | undefined
  oldChannel: Channel | null | undefined
}): ChannelWatcherAction {
  const { show, newChannel, oldChannel } = params

  if (!show) {
    return 'noop'
  }

  if (newChannel) {
    if (oldChannel && newChannel.index === oldChannel.index) {
      return 'noop'
    }
    return 'load-edit-channel'
  }

  if (oldChannel) {
    return 'noop'
  }

  return 'reset-new-form'
}

export function syncBaseUrlsFormState(rawText: string, serviceType: ServiceType): {
  baseUrl: string
  baseUrls: string[]
} {
  const rawUrls = rawText
    .split('\n')
    .map(s => s.trim())
    .filter(Boolean)
  const deduplicated = deduplicateEquivalentBaseUrls(rawUrls, serviceType)

  if (deduplicated.length === 0) {
    return { baseUrl: '', baseUrls: [] }
  }

  if (deduplicated.length === 1) {
    return { baseUrl: deduplicated[0], baseUrls: [] }
  }

  return {
    baseUrl: deduplicated[0],
    baseUrls: deduplicated
  }
}

const MULTI_PART_PUBLIC_SUFFIXES = new Set([
  'ac.cn',
  'com.cn',
  'edu.cn',
  'gov.cn',
  'net.cn',
  'org.cn',
  'co.uk',
  'org.uk',
  'ac.uk',
  'gov.uk',
  'com.au',
  'net.au',
  'org.au',
  'co.jp',
  'ne.jp',
  'or.jp',
  'co.kr',
  'or.kr',
  'com.br',
  'com.mx',
  'com.sg',
  'com.hk',
  'com.tw',
  'com.vn',
  'co.id',
  'co.in',
  'co.nz',
  'github.io',
  'pages.dev',
  'workers.dev',
  'vercel.app',
  'netlify.app',
  'onrender.com',
  'railway.app',
])

const GENERIC_HOST_PREFIXES = new Set(['www', 'api', 'apis', 'openapi', 'gateway', 'proxy'])
const MAX_CHANNEL_NAME_PREFIX_LENGTH = 40

function isIPv4Address(hostname: string): boolean {
  const parts = hostname.split('.')
  return parts.length === 4 && parts.every(part => {
    if (!/^\d+$/.test(part)) return false
    const value = Number(part)
    return value >= 0 && value <= 255
  })
}

function slugifyHostPart(value: string): string {
  return value
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .replace(/-{2,}/g, '-')
}

function appendPort(prefix: string, port: string): string {
  return port ? `${prefix}-${port}` : prefix
}

function publicSuffixLabelCount(labels: string[]): number {
  const maxSuffixLabels = Math.min(3, labels.length - 1)

  for (let count = maxSuffixLabels; count >= 2; count--) {
    const suffix = labels.slice(labels.length - count).join('.')
    if (MULTI_PART_PUBLIC_SUFFIXES.has(suffix)) {
      return count
    }
  }

  return 1
}

function dropGenericLeadingLabels(labels: string[]): string[] {
  const result = [...labels]
  while (result.length > 1 && GENERIC_HOST_PREFIXES.has(result[0])) {
    result.shift()
  }
  return result
}

function fitChannelNamePrefix(labels: string[]): string[] {
  let result = labels
  while (result.length > 1 && result.join('-').length > MAX_CHANNEL_NAME_PREFIX_LENGTH) {
    result = result.slice(1)
  }
  return result
}

export function extractChannelNamePrefix(url: string): string {
  try {
    const parsed = new URL(url.trim())
    const hostname = parsed.hostname.toLowerCase().replace(/^\[|\]$/g, '')

    if (!hostname) {
      return 'channel'
    }

    if (isIPv4Address(hostname)) {
      return appendPort(hostname.replace(/\./g, '-'), parsed.port)
    }

    if (hostname.includes(':')) {
      return slugifyHostPart(appendPort(`ipv6-${hostname}`, parsed.port)) || 'ipv6'
    }

    const labels = hostname.split('.').map(slugifyHostPart).filter(Boolean)
    if (labels.length === 0) {
      return 'channel'
    }

    if (labels.length === 1) {
      return appendPort(labels[0], parsed.port)
    }

    const suffixCount = publicSuffixLabelCount(labels)
    const stemEnd = Math.max(1, labels.length - suffixCount)
    const stemLabels = labels.slice(0, stemEnd)
    const meaningfulLabels = fitChannelNamePrefix(dropGenericLeadingLabels(stemLabels))
    const prefix = meaningfulLabels.join('-')
    if (!prefix) return 'channel'
    return appendPort(prefix, parsed.port)
  } catch {
    return 'channel'
  }
}

// 模型名合法字符：字母、数字、点、下划线、连字符、冒号、斜杠，外加通配符 * 与排除前缀 !
// 显式排除中文顿号（、）、逗号、分号、竖线等分隔符与其他标点，避免被当作单条规则保留
const SUPPORTED_MODEL_TOKEN_CHARS = /^[A-Za-z0-9._:/*!-]+$/

// 模型规则的分隔符：空白、中文顿号、逗号（中英文）、分号（中英文）、竖线、换行
const SUPPORTED_MODEL_SEPARATORS = /[\s、,，;；|]+/

/**
 * 将用户手动输入的原始文本按合法分隔符拆分为独立规则。
 * 例如 `GPT-5*、ada*` -> ['GPT-5*', 'ada*']。
 */
export function parseSupportedModelInput(raw: string): string[] {
  return raw
    .split(SUPPORTED_MODEL_SEPARATORS)
    .map(s => s.trim())
    .filter(Boolean)
}

export function isValidSupportedModelPattern(pattern: string): boolean {
  const trimmed = pattern.trim()
  if (!trimmed) {
    return false
  }

  // 仅允许模型名合法字符集；含顿号等非法字符直接拒绝
  if (!SUPPORTED_MODEL_TOKEN_CHARS.test(trimmed)) {
    return false
  }

  if ((trimmed.match(/!/g) || []).length > 1) {
    return false
  }

  const normalized = trimmed.startsWith('!') ? trimmed.slice(1).trim() : trimmed
  if (!normalized || normalized.startsWith('!')) {
    return false
  }

  const starCount = (normalized.match(/\*/g) || []).length
  if (starCount === 0) {
    return true
  }
  if (normalized === '*') {
    return true
  }
  if (starCount === 1) {
    return normalized.startsWith('*') || normalized.endsWith('*')
  }
  if (starCount === 2) {
    return normalized.startsWith('*') && normalized.endsWith('*') && normalized.replace(/\*/g, '') !== ''
  }
  return false
}

export function filterValidSupportedModelPatterns(patterns: string[]): {
  validPatterns: string[]
  hasInvalidPatterns: boolean
} {
  // 先按分隔符拆分（兼容用户把多个规则粘进同一项的情况），再做合法性校验
  const normalized = patterns.flatMap(parseSupportedModelInput)

  const validPatterns = normalized.filter(isValidSupportedModelPattern)
  return {
    validPatterns,
    hasInvalidPatterns: validPatterns.length !== normalized.length
  }
}
