import { describe, expect, it } from 'vitest'
import { applyDocumentLanguage, normalizeLocale, resolveInitialLocale } from './core'
import en from '@/locales/en.json'
import zhCN from '@/locales/zh-CN.json'

describe('normalizeLocale', () => {
  it('returns zh-CN for Chinese variants', () => {
    expect(normalizeLocale('zh')).toBe('zh-CN')
    expect(normalizeLocale('zh-CN')).toBe('zh-CN')
    expect(normalizeLocale('zh_CN')).toBe('zh-CN')
    expect(normalizeLocale('zh-Hans')).toBe('zh-CN')
    expect(normalizeLocale('zh-Hans-CN')).toBe('zh-CN')
    expect(normalizeLocale('zh_CN.UTF-8')).toBe('zh-CN')
  })

  it('returns en for English and unknown locales', () => {
    expect(normalizeLocale('en')).toBe('en')
    expect(normalizeLocale('en-US')).toBe('en')
    expect(normalizeLocale('fr-FR')).toBe('en')
    expect(normalizeLocale(undefined)).toBe('en')
    expect(normalizeLocale(null)).toBe('en')
  })
})

describe('resolveInitialLocale', () => {
  it('prefers supported persisted locale', () => {
    expect(resolveInitialLocale('zh-CN', 'en-US')).toBe('zh-CN')
    expect(resolveInitialLocale('en', 'zh_CN')).toBe('en')
  })

  it('falls back to normalized system locale when persisted unsupported', () => {
    expect(resolveInitialLocale(null, 'zh_CN.UTF-8')).toBe('zh-CN')
    expect(resolveInitialLocale(undefined, 'fr-FR')).toBe('en')
  })

  it('defaults to en when no data', () => {
    expect(resolveInitialLocale(null, null)).toBe('en')
  })
})

describe('JSON locale files', () => {
  it('en and zh-CN have identical key sets', () => {
    const enKeys = [...Object.keys(en)].sort()
    const zhKeys = [...Object.keys(zhCN)].sort()
    expect(zhKeys).toEqual(enKeys)
  })

  it('includes known critical keys', () => {
    const requiredKeys = [
      'common.gatewayLabel',
      'nav.status',
      'env.fieldMin',
    ]
    for (const key of requiredKeys) {
      expect((en as Record<string, string>)[key]).toBeTruthy()
      expect((zhCN as Record<string, string>)[key]).toBeTruthy()
    }
  })

  it('zh-CN has correct translations', () => {
    expect((zhCN as Record<string, string>)['nav.status']).toBe('网关监控')
  })

  it('en has correct translations', () => {
    expect((en as Record<string, string>)['nav.status']).toBe('Status')
  })

  it('en has preset keys with English content', () => {
    expect((en as Record<string, string>)['channel.target.messages.label']).toBe('Messages native')
  })
})

describe('applyDocumentLanguage', () => {
  it('sets document.documentElement.lang', () => {
    document.documentElement.lang = ''
    applyDocumentLanguage('zh-CN')
    expect(document.documentElement.lang).toBe('zh-CN')
  })
})
