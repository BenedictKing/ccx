import { describe, expect, it } from 'vitest'

import { isStructurallyEqual, reuseUnchangedItemsByKey } from './structuralSharing'

describe('isStructurallyEqual', () => {
  it('比较嵌套 JSON 数据且不依赖对象键顺序', () => {
    expect(
      isStructurallyEqual({ id: 1, values: [{ ok: true, count: 2 }] }, { values: [{ count: 2, ok: true }], id: 1 })
    ).toBe(true)
  })

  it('识别数组顺序和嵌套值变化', () => {
    expect(isStructurallyEqual([1, 2], [2, 1])).toBe(false)
    expect(isStructurallyEqual({ nested: { value: 1 } }, { nested: { value: 2 } })).toBe(false)
  })

  it('不把 Map 等非 JSON 对象误判为相等', () => {
    expect(isStructurallyEqual(new Map([['a', 1]]), new Map([['b', 2]]))).toBe(false)
  })
})

describe('reuseUnchangedItemsByKey', () => {
  it('整个数组未变化时复用数组和条目引用', () => {
    const previous = [
      { id: 1, nested: { value: 1 } },
      { id: 2, nested: { value: 2 } }
    ]
    const next = [
      { id: 1, nested: { value: 1 } },
      { id: 2, nested: { value: 2 } }
    ]

    const result = reuseUnchangedItemsByKey(next, previous, item => item.id)

    expect(result).toBe(previous)
    expect(result[0]).toBe(previous[0])
  })

  it('仅替换实际变化的条目', () => {
    const previous = [
      { id: 1, value: 1 },
      { id: 2, value: 2 }
    ]
    const next = [
      { id: 1, value: 1 },
      { id: 2, value: 3 }
    ]

    const result = reuseUnchangedItemsByKey(next, previous, item => item.id)

    expect(result).not.toBe(previous)
    expect(result[0]).toBe(previous[0])
    expect(result[1]).toBe(next[1])
  })

  it('排序变化时复用条目但返回新数组', () => {
    const previous = [
      { id: 1, value: 1 },
      { id: 2, value: 2 }
    ]
    const next = [
      { id: 2, value: 2 },
      { id: 1, value: 1 }
    ]

    const result = reuseUnchangedItemsByKey(next, previous, item => item.id)

    expect(result).not.toBe(previous)
    expect(result).toEqual([previous[1], previous[0]])
  })
})
