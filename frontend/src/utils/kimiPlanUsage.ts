import type {
  KimiBoosterWallet,
  KimiCodeBalance,
  KimiCodeUsageSnapshot,
} from '../services/api-types'

export type KimiUsageSection =
  | {
      key: string
      kind: 'window'
      labelKey: string
      usedPercent: number
      resetTime?: string
    }
  | {
      key: string
      kind: 'subscription' | 'gift'
      labelKey: string
      usedPercent: number
      balance: KimiCodeBalance
    }
  | {
      key: string
      kind: 'booster'
      labelKey: string
      usedPercent?: number
      wallet: KimiBoosterWallet
    }

const clampPercent = (value: number): number => Math.max(0, Math.min(100, value))

const balanceUsedPercent = (balance: KimiCodeBalance): number =>
  clampPercent(balance.amountUsedRatio * 100)

const boosterUsedPercent = (wallet: KimiBoosterWallet): number | undefined => {
  if (!wallet.allowTopup || wallet.moneyTotal.priceInCents <= 0) return undefined
  const remainingPercent = clampPercent(
    (wallet.moneyLeft.priceInCents / wallet.moneyTotal.priceInCents) * 100,
  )
  return 100 - remainingPercent
}

export const buildKimiUsageSections = (usage: KimiCodeUsageSnapshot): KimiUsageSection[] => {
  const sections: KimiUsageSection[] = []

  if (usage.codeSevenDay?.enabled) {
    sections.push({
      key: 'window-seven-day',
      kind: 'window',
      labelKey: 'kimiConsoleToken.weeklyLimit',
      usedPercent: clampPercent(usage.codeSevenDay.ratio * 100),
      resetTime: usage.codeSevenDay.resetTime,
    })
  }
  if (usage.codeFiveHour?.enabled) {
    sections.push({
      key: 'window-five-hour',
      kind: 'window',
      labelKey: 'kimiConsoleToken.fiveHourLimit',
      usedPercent: clampPercent(usage.codeFiveHour.ratio * 100),
      resetTime: usage.codeFiveHour.resetTime,
    })
  }
  if (usage.subscriptionBalance) {
    sections.push({
      key: 'subscription',
      kind: 'subscription',
      labelKey: 'kimiConsoleToken.subscriptionBalance',
      usedPercent: balanceUsedPercent(usage.subscriptionBalance),
      balance: usage.subscriptionBalance,
    })
  }
  for (const [index, gift] of (usage.giftBalances ?? []).entries()) {
    sections.push({
      key: `gift-${index}`,
      kind: 'gift',
      labelKey: 'kimiConsoleToken.giftBalance',
      usedPercent: balanceUsedPercent(gift),
      balance: gift,
    })
  }
  for (const [index, wallet] of (usage.boosterWallets ?? []).entries()) {
    sections.push({
      key: `booster-${wallet.id || index}`,
      kind: 'booster',
      labelKey: 'kimiConsoleToken.boosterWallet',
      usedPercent: boosterUsedPercent(wallet),
      wallet,
    })
  }

  return sections
}
