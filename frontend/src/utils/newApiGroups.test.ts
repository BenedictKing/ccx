import { describe, expect, it } from 'vitest'

import { eligibleNewApiGroups, isValidNewApiGroupMultiplier } from './newApiGroups'

describe('new-api group multiplier guard', () => {
  it('只保留不超过上限的有效分组，并按倍率稳定排序', () => {
    expect(
      eligibleNewApiGroups(
        { expensive: 3, default: 1, discounted: 0.5, sameRatio: 1, invalid: Number.NaN },
        1
      )
    ).toEqual([
      { name: 'discounted', ratio: 0.5 },
      { name: 'default', ratio: 1 },
      { name: 'sameRatio', ratio: 1 }
    ])
  })

  it('允许 0 倍率上限，但拒绝负数和非有限值', () => {
    expect(isValidNewApiGroupMultiplier(0)).toBe(true)
    expect(isValidNewApiGroupMultiplier(-0.1)).toBe(false)
    expect(isValidNewApiGroupMultiplier(Number.POSITIVE_INFINITY)).toBe(false)
  })

  it('剔除明确 0 模型的分组；无计数记录的分组保守保留', () => {
    expect(
      eligibleNewApiGroups({ default: 1, drawing: 1, unknown: 1 }, 1, { default: 3, drawing: 0 })
    ).toEqual([
      { name: 'default', ratio: 1, modelCount: 3 },
      { name: 'unknown', ratio: 1, modelCount: undefined }
    ])
  })
})
