import { describe, expect, it } from 'vitest'

import type { KimiCodeMoney, KimiCodeUsageSnapshot } from '../services/api-types'
import { buildKimiUsageSections } from './kimiPlanUsage'

const money = (priceInCents: number): KimiCodeMoney => ({ currency: 'CNY', priceInCents })

const usage = (overrides: Partial<KimiCodeUsageSnapshot> = {}): KimiCodeUsageSnapshot => ({
  weeklyUsage: { limit: 0, remaining: 0, used: 0, resetTime: '' },
  totalQuota: { limit: 0, remaining: 0, used: 0, resetTime: '' },
  validatedAt: '2026-07-28T00:00:00Z',
  ...overrides,
})

describe('buildKimiUsageSections', () => {
  it('按时间窗口、订阅、赠送、加油包的固定顺序构建用量分项', () => {
    const sections = buildKimiUsageSections(usage({
      codeSevenDay: { enabled: true, ratio: 0.25 },
      codeFiveHour: { enabled: true, ratio: 0.5 },
      subscriptionBalance: { amountUsedRatio: 0.2, kimiCodeUsedRatio: 0.1 },
      giftBalances: [{ amountUsedRatio: 0.4, kimiCodeUsedRatio: 0.3 }],
      boosterWallets: [{
        id: 'wallet-1',
        allowTopup: true,
        moneyLeft: money(2500),
        moneyTotal: money(10000),
        monthlyChargeLimit: money(10000),
        monthlyUsed: money(7500),
      }],
    }))

    expect(sections.map(section => section.kind)).toEqual([
      'window',
      'window',
      'subscription',
      'gift',
      'booster',
    ])
    expect(sections.map(section => section.usedPercent)).toEqual([25, 50, 20, 40, 75])
    expect(sections[2]).toMatchObject({ kimiUsedPercent: 10, codeUsedPercent: 10 })
    expect(sections[3]).toMatchObject({ kimiUsedPercent: 10, codeUsedPercent: 30 })
  })

  it('跳过禁用时间窗口，但保留余额类分项', () => {
    const sections = buildKimiUsageSections(usage({
      codeSevenDay: { enabled: false, ratio: 0.25 },
      codeFiveHour: { enabled: false, ratio: 0.5 },
      subscriptionBalance: { amountUsedRatio: 0.2, kimiCodeUsedRatio: 0.1 },
      giftBalances: [{ amountUsedRatio: 0.4, kimiCodeUsedRatio: 0.3 }],
    }))

    expect(sections.map(section => section.kind)).toEqual(['subscription', 'gift'])
  })

  it('钳制异常比率，并避免不可充值或零额度加油包产生百分比', () => {
    const sections = buildKimiUsageSections(usage({
      subscriptionBalance: { amountUsedRatio: 1.5, kimiCodeUsedRatio: 0 },
      giftBalances: [{ amountUsedRatio: -0.2, kimiCodeUsedRatio: 0 }],
      boosterWallets: [
        {
          id: 'disabled',
          allowTopup: false,
          moneyLeft: money(100),
          moneyTotal: money(100),
          monthlyChargeLimit: money(0),
          monthlyUsed: money(0),
        },
        {
          id: 'zero-total',
          allowTopup: true,
          moneyLeft: money(0),
          moneyTotal: money(0),
          monthlyChargeLimit: money(0),
          monthlyUsed: money(0),
        },
      ],
    }))

    expect(sections.map(section => section.usedPercent)).toEqual([100, 0, undefined, undefined])
  })
})
