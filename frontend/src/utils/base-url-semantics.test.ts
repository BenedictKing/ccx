import { describe, expect, it } from 'vitest'

import {
  buildExpectedRequestUrl,
  deduplicateEquivalentBaseUrls,
  stripDashboardPathFromBaseUrl
} from './baseUrlSemantics'

describe('base URL 路径语义', () => {
  it('显式 # 应保留与控制台同名的 API 路径并跳过版本前缀补全', () => {
    const baseUrl = 'https://example.com/token/v1#'

    expect(deduplicateEquivalentBaseUrls([baseUrl], 'openai')).toEqual([baseUrl])
    expect(buildExpectedRequestUrl('openai', '/chat/completions', baseUrl)).toBe(
      'https://example.com/token/v1/chat/completions'
    )
  })

  it('未使用 # 时仍应清理明确的控制台路径', () => {
    expect(stripDashboardPathFromBaseUrl('https://example.com/console/token')).toBe('https://example.com')
  })

  it('带 # 的完整路径 URL 不应与域名根 URL 去重合并', () => {
    expect(
      deduplicateEquivalentBaseUrls(['https://example.com#', 'https://example.com/token/v1#'], 'openai')
    ).toEqual(['https://example.com#', 'https://example.com/token/v1#'])
  })
})
