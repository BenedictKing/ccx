// eslint-disable-next-line @typescript-eslint/ban-ts-comment
// @ts-nocheck
import { readFileSync } from 'node:fs'
import { resolve } from 'node:path'
import { describe, expect, it } from 'vitest'

import { normalizeLocale, resolveInitialLocale } from './index'
import en from '@/locales/en.json'
import zhCN from '@/locales/zh-CN.json'
import id from '@/locales/id.json'

describe('normalizeLocale', () => {
  it('normalizes supported locales', () => {
    expect(normalizeLocale('en')).toBe('en')
    expect(normalizeLocale('id')).toBe('id')
    expect(normalizeLocale('zh')).toBe('zh-CN')
    expect(normalizeLocale('zh-CN')).toBe('zh-CN')
  })

  it('falls back to DEFAULT_LOCALE for invalid locales', () => {
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

describe('JSON locale files', () => {
  it('all locales have identical key sets', () => {
    const enKeys = Object.keys(en).sort()
    const zhKeys = Object.keys(zhCN).sort()
    const idKeys = Object.keys(id).sort()
    expect(enKeys).toEqual(zhKeys)
    expect(enKeys).toEqual(idKeys)
  })

  it('includes known critical keys', () => {
    const requiredKeys = [
      'orchestration.title',
      'orchestration.searchPlaceholder',
      'addChannel.editTitle',
      'addChannel.createTitle',
      'addChannel.modelMappingHintVectors',
      'addChannel.targetModelPlaceholderVectors',
      'chart.traffic',
      'chart.tokens',
      'app.auth.submit'
    ]
    for (const key of requiredKeys) {
      expect((en as Record<string, string>)[key]).toBeTruthy()
      expect((zhCN as Record<string, string>)[key]).toBeTruthy()
      expect((id as Record<string, string>)[key]).toBeTruthy()
    }
  })

  it('Indonesian has correct translations', () => {
    expect((id as Record<string, string>)['app.auth.submit']).toBe('Buka console admin')
    expect((id as Record<string, string>)['channels.empty.title']).toBe('Belum ada channel')
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
        forbidden: [
          "seriesName: '输入 Token'",
          "seriesName: '输出 Token'",
          '获取全局统计数据失败',
          '合计: ${dp.requestCount} 请求'
        ]
      },
      {
        file: 'components/KeyTrendChart.vue',
        forbidden: [
          '获取 Key 历史数据失败',
          '合计: ${grandTotal} 请求',
          '失败 (${grandFailureRate}%)',
          '${Math.round(val)} 请求'
        ]
      },
      {
        file: 'components/AddChannelModal.vue',
        forbidden: [
          'label="源模型名"',
          'label="目标模型名"',
          '跳过 TLS 证书验证</div>',
          '>          创建渠道',
          "'此字段为必填项'",
          'v-model="quickServiceType"',
          'headerServiceTypeItems'
        ]
      },
      {
        file: 'components/QuickAddChannelForm.vue',
        forbidden: ['v-model="serviceType"', 'serviceTypeItems']
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

describe('channel naming constraints', () => {
  it('所有渠道名称均自动派生，表单不再提供名称输入，名称统一在编辑弹窗头部展示', () => {
    const quickAdd = readFileSync(resolve(__dirname, '../components/QuickAddChannelForm.vue'), 'utf8')
    const addModal = readFileSync(resolve(__dirname, '../components/AddChannelModal.vue'), 'utf8')
    const basicInfo = readFileSync(resolve(__dirname, '../components/edit-channel/BasicInfoSection.vue'), 'utf8')
    const header = readFileSync(resolve(__dirname, '../components/edit-channel/AddChannelHeader.vue'), 'utf8')
    const editModal = readFileSync(resolve(__dirname, '../components/EditChannelModal.vue'), 'utf8')

    expect(quickAdd).not.toContain('v-model="channelName"')
    expect(addModal).not.toContain('v-model="channelName"')
    expect(basicInfo).not.toContain('channelEditor.basic.name.label')
    expect(basicInfo).not.toContain('channelEditor.basic.accountName')
    expect(header).toContain('channelName')
    // 托管 provider 隐藏自动派生名称，普通渠道仍消费 form.name
    expect(editModal).toContain(':channel-name="isManagedProvider ? \'\' : form.name"')
  })
})

describe('mandatory automatic channel discovery', () => {
  it('标准与结构化添加均走自动探测，且不再提供自动托管开关', () => {
    const addModal = readFileSync(resolve(__dirname, '../components/AddChannelModal.vue'), 'utf8')
    const quickAdd = readFileSync(resolve(__dirname, '../components/QuickAddChannelForm.vue'), 'utf8')

    expect(addModal).toContain('await discoverFast(')
    expect(addModal).toContain('await autoAddChannel(targetChannelType')
    expect(quickAdd).toContain('recognizedBaseUrls[idx]')
    expect(quickAdd).not.toContain('v-model="autoManaged"')
    expect(quickAdd).not.toContain("t('autopilot.quickAdd.autoManagedHint')")
  })
})
