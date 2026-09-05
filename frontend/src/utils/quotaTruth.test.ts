import { describe, expect, it } from 'vitest'

import {
  buildQuotaTruth,
  configuredQuotaTruth,
  unknownQuotaTruth,
  unavailableQuotaTruth,
} from './quotaTruth'

describe('buildQuotaTruth', () => {
  it('余量充足 → healthy / provider_api', () => {
    expect(buildQuotaTruth(0)).toEqual({ truthLevel: 'healthy', truthSource: 'provider_api' })
    expect(buildQuotaTruth(50)).toEqual({ truthLevel: 'healthy', truthSource: 'provider_api' })
    expect(buildQuotaTruth(79.9)).toEqual({ truthLevel: 'healthy', truthSource: 'provider_api' })
  })

  it('余量 ≤ 20% → approaching_limit（与后端 IsApproaching 阈值对齐）', () => {
    expect(buildQuotaTruth(80)).toEqual({ truthLevel: 'approaching_limit', truthSource: 'provider_api' })
    expect(buildQuotaTruth(95)).toEqual({ truthLevel: 'approaching_limit', truthSource: 'provider_api' })
    expect(buildQuotaTruth(99.9)).toEqual({ truthLevel: 'approaching_limit', truthSource: 'provider_api' })
  })

  it('余量耗尽 → exhausted', () => {
    expect(buildQuotaTruth(100)).toEqual({ truthLevel: 'exhausted', truthSource: 'provider_api' })
    expect(buildQuotaTruth(120)).toEqual({ truthLevel: 'exhausted', truthSource: 'provider_api' })
  })

  it('无法换算百分比（无余量概念）→ unknown / provider_api（fail-open 不判耗尽）', () => {
    expect(buildQuotaTruth(undefined)).toEqual({ truthLevel: 'unknown', truthSource: 'provider_api' })
    expect(buildQuotaTruth(Number.NaN)).toEqual({ truthLevel: 'unknown', truthSource: 'provider_api' })
  })

  it('负值按 0 处理 → healthy', () => {
    expect(buildQuotaTruth(-5)).toEqual({ truthLevel: 'healthy', truthSource: 'provider_api' })
  })
})

describe('配额真相辅助档位', () => {
  it('已知支持但本次失败 → unavailable / provider_api', () => {
    expect(unavailableQuotaTruth()).toEqual({ truthLevel: 'unavailable', truthSource: 'provider_api' })
  })

  it('只有配置声明 → unknown / configured', () => {
    expect(configuredQuotaTruth()).toEqual({ truthLevel: 'unknown', truthSource: 'configured' })
  })

  it('无任何证据 → unknown / unknown', () => {
    expect(unknownQuotaTruth()).toEqual({ truthLevel: 'unknown', truthSource: 'unknown' })
  })
})
