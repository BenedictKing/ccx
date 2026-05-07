/**
 * ChannelDashboardCard - 格式化与渲染测试
 *
 * 表驱动覆盖：
 * - formatNumber: < 1000 原样；1K-1M 显示 1.2K；>= 1M 显示 1.2M；缺值 em dash
 * - formatPercent: 保留 1 位小数；缺值 em dash
 * - formatCost: decimal string -> $0.12；缺值或解析失败 em dash
 * - formatCacheReadWrite: 全缺 em dash；其余 "读 X 写 Y"
 */

import { describe, it, expect } from 'vitest'
import {
  formatNumber,
  formatPercent,
  formatCost,
  formatCacheReadWrite
} from './ChannelDashboardCard.vue'

describe('formatNumber', () => {
  const cases: Array<[unknown, string]> = [
    [0, '0'],
    [18, '18'],
    [999, '999'],
    [1000, '1.0K'],
    [1234, '1.2K'],
    [9999, '10.0K'],
    [999999, '1000.0K'],
    [1000000, '1.0M'],
    [1234567, '1.2M'],
    [8000000, '8.0M'],
    [undefined, '—'],
    [null, '—'],
    [NaN, '—']
  ]

  it.each(cases)('formatNumber(%p) -> %p', (input, expected) => {
    expect(formatNumber(input as number | undefined | null)).toBe(expected)
  })
})

describe('formatPercent', () => {
  const cases: Array<[unknown, string]> = [
    [100, '100.0%'],
    [99.94, '99.9%'],
    [0, '0.0%'],
    [42.5, '42.5%'],
    [undefined, '—'],
    [null, '—'],
    [NaN, '—']
  ]

  it.each(cases)('formatPercent(%p) -> %p', (input, expected) => {
    expect(formatPercent(input as number | undefined | null)).toBe(expected)
  })
})

describe('formatCost', () => {
  const cases: Array<[unknown, string]> = [
    ['0.12345', '$0.12'],
    ['0.00', '$0.00'],
    ['1.999', '$2.00'],
    ['100', '$100.00'],
    [undefined, '—'],
    [null, '—'],
    ['', '—'],
    ['abc', '—']
  ]

  it.each(cases)('formatCost(%p) -> %p', (input, expected) => {
    expect(formatCost(input as string | undefined | null)).toBe(expected)
  })
})

describe('formatCacheReadWrite', () => {
  it('两侧都缺值 -> em dash', () => {
    expect(formatCacheReadWrite(undefined, undefined)).toBe('—')
    expect(formatCacheReadWrite(null, null)).toBe('—')
  })

  it('两侧都有值 -> 读 X 写 Y', () => {
    expect(formatCacheReadWrite(689900, 589400)).toBe('读 689.9K 写 589.4K')
  })

  it('小数值不缩写', () => {
    expect(formatCacheReadWrite(123, 456)).toBe('读 123 写 456')
  })

  it('单侧缺值时该侧显示 em dash', () => {
    expect(formatCacheReadWrite(1234, undefined)).toBe('读 1.2K 写 —')
    expect(formatCacheReadWrite(undefined, 1234)).toBe('读 — 写 1.2K')
  })
})
