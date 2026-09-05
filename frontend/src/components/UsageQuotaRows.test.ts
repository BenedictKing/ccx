// @vitest-environment jsdom
import { describe, expect, it } from 'vitest'
import { mount } from '@vue/test-utils'

import UsageQuotaRows from './UsageQuotaRows.vue'
import type { UsageQuotaItem } from '@/utils/usageQuotaItem'

const item = (overrides: Partial<UsageQuotaItem>): UsageQuotaItem => ({
  key: 'row',
  label: '当前窗口',
  value: '剩余 78.5%',
  ...overrides,
})

describe('UsageQuotaRows 真相徽章', () => {
  it('五种 TruthLevel 均渲染徽章并带正确样式类', () => {
    const levels = ['healthy', 'approaching_limit', 'exhausted', 'unavailable', 'unknown'] as const
    const wrapper = mount(UsageQuotaRows, {
      props: { items: levels.map((level, i) => item({ key: `k${i}`, truthLevel: level })) },
    })
    const badges = wrapper.findAll('.truth-badge')
    expect(badges).toHaveLength(5)
    levels.forEach((level, i) => {
      expect(badges[i].classes()).toContain(`truth-badge--${level}`)
    })
  })

  it('unknown 徽章不使用红色样式（fail-open 原则）', () => {
    const wrapper = mount(UsageQuotaRows, {
      props: { items: [item({ truthLevel: 'unknown', truthSource: 'provider_api' })] },
    })
    const badge = wrapper.find('.truth-badge')
    expect(badge.classes()).toContain('truth-badge--unknown')
    expect(badge.classes()).not.toContain('truth-badge--exhausted')
    expect(badge.classes()).not.toContain('truth-badge--approaching_limit')
  })

  it('tooltip 显示数据来源与真相等级', () => {
    const wrapper = mount(UsageQuotaRows, {
      props: { items: [item({ truthLevel: 'exhausted', truthSource: 'provider_api' })] },
    })
    const title = wrapper.find('.truth-badge').attributes('title')
    expect(title).toContain('官方 API')
    expect(title).toContain('耗尽')
  })

  it('四种 Source 均可正确标注来源', () => {
    const sources = ['provider_api', 'response_headers', 'configured', 'estimated'] as const
    const labels = ['官方 API', '响应头', '配置声明', '估算']
    sources.forEach((source, i) => {
      const wrapper = mount(UsageQuotaRows, {
        props: { items: [item({ key: 'k', truthLevel: 'healthy', truthSource: source })] },
      })
      expect(wrapper.find('.truth-badge').attributes('title')).toContain(labels[i])
    })
  })

  it('无 truthLevel 的行不渲染徽章（向后兼容）', () => {
    const wrapper = mount(UsageQuotaRows, {
      props: { items: [item({})] },
    })
    expect(wrapper.find('.truth-badge').exists()).toBe(false)
  })
})
