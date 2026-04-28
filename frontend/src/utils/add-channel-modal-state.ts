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

export function isValidSupportedModelPattern(pattern: string): boolean {
  const trimmed = pattern.trim()
  if (!trimmed) {
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
  const normalized = patterns
    .map(pattern => pattern.trim())
    .filter(Boolean)

  const validPatterns = normalized.filter(isValidSupportedModelPattern)
  return {
    validPatterns,
    hasInvalidPatterns: validPatterns.length !== normalized.length
  }
}
