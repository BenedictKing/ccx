import { describe, expect, it } from 'vitest'

import type { Channel } from '@/services/api'
import {
  filterValidSupportedModelPatterns,
  isValidSupportedModelPattern,
  resolveChannelWatcherAction,
  syncBaseUrlsFormState
} from './add-channel-modal-state'

const sampleChannel: Channel = {
  index: 1,
  name: 'existing-channel',
  serviceType: 'openai',
  baseUrl: 'https://example.com/v1',
  apiKeys: ['sk-test']
}

describe('resolveChannelWatcherAction', () => {
  it('resets the draft when opening in create mode', () => {
    expect(resolveChannelWatcherAction({
      show: true,
      newChannel: null,
      oldChannel: null
    })).toBe('reset-new-form')
  })

  it('loads channel data when entering edit mode', () => {
    expect(resolveChannelWatcherAction({
      show: true,
      newChannel: sampleChannel,
      oldChannel: null
    })).toBe('load-edit-channel')
  })

  it('keeps the local draft when the same edited channel is silently refreshed', () => {
    expect(resolveChannelWatcherAction({
      show: true,
      newChannel: {
        ...sampleChannel,
        name: 'existing-channel-updated',
        baseUrl: 'https://example.com/v2'
      },
      oldChannel: sampleChannel
    })).toBe('noop')
  })

  it('keeps noop when edit channel is cleared while dialog is open', () => {
    expect(resolveChannelWatcherAction({
      show: true,
      newChannel: null,
      oldChannel: sampleChannel
    })).toBe('noop')
  })

  it('ignores channel changes while dialog is closed', () => {
    expect(resolveChannelWatcherAction({
      show: false,
      newChannel: sampleChannel,
      oldChannel: null
    })).toBe('noop')
  })
})

describe('syncBaseUrlsFormState', () => {
  it('deduplicates default-version URLs for the active service type', () => {
    expect(syncBaseUrlsFormState('https://host\nhttps://host/v1', 'openai')).toEqual({
      baseUrl: 'https://host',
      baseUrls: []
    })
  })

  it('keeps service-specific default-version semantics separate', () => {
    expect(syncBaseUrlsFormState('https://host/v1\nhttps://host', 'gemini')).toEqual({
      baseUrl: 'https://host/v1',
      baseUrls: ['https://host/v1', 'https://host']
    })
  })
})

describe('isValidSupportedModelPattern', () => {
  it('accepts exact, wildcard, contains, and exclusion rules', () => {
    expect(isValidSupportedModelPattern('gpt-4o')).toBe(true)
    expect(isValidSupportedModelPattern('gpt-4*')).toBe(true)
    expect(isValidSupportedModelPattern('*image')).toBe(true)
    expect(isValidSupportedModelPattern('*image*')).toBe(true)
    expect(isValidSupportedModelPattern('!*image*')).toBe(true)
  })

  it('rejects invalid wildcard and empty rules', () => {
    expect(isValidSupportedModelPattern('foo*bar')).toBe(false)
    expect(isValidSupportedModelPattern('**')).toBe(false)
    expect(isValidSupportedModelPattern('')).toBe(false)
    expect(isValidSupportedModelPattern('   ')).toBe(false)
    expect(isValidSupportedModelPattern('!')).toBe(false)
    expect(isValidSupportedModelPattern('!!gpt-4*')).toBe(false)
  })
})

describe('filterValidSupportedModelPatterns', () => {
  it('filters invalid rules while preserving valid rule order', () => {
    expect(filterValidSupportedModelPatterns([' gpt-4* ', 'foo*bar', '!*image*'])).toEqual({
      validPatterns: ['gpt-4*', '!*image*'],
      hasInvalidPatterns: true
    })
  })

  it('does not mark all-valid rules as invalid', () => {
    expect(filterValidSupportedModelPatterns(['gpt-4*', '*image*'])).toEqual({
      validPatterns: ['gpt-4*', '*image*'],
      hasInvalidPatterns: false
    })
  })
})
