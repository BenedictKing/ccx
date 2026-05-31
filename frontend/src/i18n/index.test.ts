// eslint-disable-next-line @typescript-eslint/ban-ts-comment
// @ts-nocheck
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

import { createTranslator, normalizeLocale, resolveInitialLocale } from './index'
import { messages } from './messages'

describe('normalizeLocale', () => {
  it('normalizes supported locales', () => {
    expect(normalizeLocale('en')).toBe('en')
    expect(normalizeLocale('id')).toBe('id')
    expect(normalizeLocale('zh')).toBe('zh-CN')
    expect(normalizeLocale('zh-CN')).toBe('zh-CN')
  })

  it('falls back to default locale for invalid locales', () => {
    expect(normalizeLocale('fr')).toBe('zh-CN')
    expect(normalizeLocale('')).toBe('zh-CN')
    expect(normalizeLocale(undefined)).toBe('zh-CN')
  })
})

describe('resolveInitialLocale', () => {
  it('prefers persisted locale over runtime locale', () => {
    expect(resolveInitialLocale('id', 'en')).toBe('id')
  })

  it('uses runtime locale when persisted locale is invalid', () => {
    expect(resolveInitialLocale('fr', 'zh')).toBe('zh-CN')
  })
})

describe('createTranslator', () => {
  it('returns localized messages', () => {
    const t = createTranslator('id')
    expect(t('app.auth.submit')).toBe('Buka console admin')
    expect(t('channels.empty.title')).toBe('Belum ada channel')
  })

  it('falls back to english for missing locale entries', () => {
    const t = createTranslator('id')
    expect(t('app.tabs.chat')).toBe('OpenAI Chat')
  })

  it('returns the key when the message is unknown', () => {
    const t = createTranslator('en')
    expect((t as unknown as (_key: string) => string)('missing.key')).toBe('missing.key')
  })
})

describe('messages', () => {
  it('includes orchestration and add-channel keys for all locales', () => {
    const requiredKeys = [
      'orchestration.title',
      'orchestration.multiChannel',
      'orchestration.singleChannel',
      'orchestration.searchPlaceholder',
      'orchestration.failoverSequence',
      'orchestration.dragHint',
      'orchestration.logs',
      'orchestration.edit',
      'orchestration.copyConfig',
      'orchestration.enable',
      'orchestration.delete',
      'addChannel.editTitle',
      'addChannel.createTitle',
      'addChannel.editSubtitle',
      'addChannel.quickSubtitle',
      'addChannel.fullSubtitle',
      'addChannel.testCapability',
      'addChannel.detailedMode',
      'addChannel.quickMode',
      'chart.close',
      'chart.1h',
      'chart.6h',
      'chart.24h',
      'chart.today',
      'chart.traffic',
      'chart.tokens',
      'chart.cacheRw',
      'chart.noRequestsInRange',
      'chart.noKeyUsageInRange',
    ] as const

    for (const locale of Object.keys(messages) as Array<keyof typeof messages>) {
      for (const requiredKey of requiredKeys) {
        expect(messages[locale][requiredKey as keyof (typeof messages)[typeof locale]]).toBeTruthy()
      }
    }
  })
})

describe('localized components', () => {
  it('does not leave known hardcoded Chinese UI strings in migrated components', () => {
    const checks = [
      {
        file: 'components/ChannelMetricsChart.vue',
        forbidden: ['关闭', '请求数量', '获取历史数据失败', '收起']
      },
      {
        file: 'components/ModelStatsChart.vue',
        forbidden: ['>关闭<', '>今日<', '>请求<', '获取模型统计数据失败']
      },
      {
        file: 'components/GlobalStatsChart.vue',
        forbidden: ["seriesName: '输入 Token'", "seriesName: '输出 Token'", '获取全局统计数据失败', '合计: ${dp.requestCount} 请求']
      },
      {
        file: 'components/KeyTrendChart.vue',
        forbidden: ['获取 Key 历史数据失败', '合计: ${grandTotal} 请求', '失败 (${grandFailureRate}%)', '${Math.round(val)} 请求']
      },
      {
        file: 'components/AddChannelModal.vue',
        forbidden: ['label="源模型名"', 'label="目标模型名"', '跳过 TLS 证书验证</div>', '>          创建渠道', "'此字段为必填项'"]
      }
    ] as const

    for (const check of checks) {
      const content = readFileSync(resolve(__dirname, '..', check.file), 'utf8')
      for (const text of check.forbidden) {
        expect(content).not.toContain(text)
      }
    }
  })
})
