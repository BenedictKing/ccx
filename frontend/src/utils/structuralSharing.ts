/**
 * 为 dashboard 这类 JSON 数据提供结构共享。
 * 相等条目沿用旧引用，避免 Vue 重复代理化并让下游按引用跳过无效更新。
 */

export function isStructurallyEqual(left: unknown, right: unknown): boolean {
  if (Object.is(left, right)) return true
  if (left === null || right === null) return false
  if (typeof left !== 'object' || typeof right !== 'object') return false

  if (Array.isArray(left) || Array.isArray(right)) {
    if (!Array.isArray(left) || !Array.isArray(right) || left.length !== right.length) return false
    return left.every((item, index) => isStructurallyEqual(item, right[index]))
  }

  const leftPrototype = Object.getPrototypeOf(left)
  const rightPrototype = Object.getPrototypeOf(right)
  const isPlainObject = (prototype: object | null) => prototype === Object.prototype || prototype === null
  if (!isPlainObject(leftPrototype) || !isPlainObject(rightPrototype)) return false

  const leftRecord = left as Record<string, unknown>
  const rightRecord = right as Record<string, unknown>
  const leftKeys = Object.keys(leftRecord)
  const rightKeys = Object.keys(rightRecord)
  if (leftKeys.length !== rightKeys.length) return false

  return leftKeys.every(
    key =>
      Object.prototype.hasOwnProperty.call(rightRecord, key) && isStructurallyEqual(leftRecord[key], rightRecord[key])
  )
}

export function reuseUnchangedItemsByKey<T, K>(
  nextItems: T[],
  previousItems: T[] | undefined,
  getKey: (item: T) => K
): T[] {
  if (!previousItems) return nextItems
  if (nextItems.length === 0) return previousItems.length === 0 ? previousItems : nextItems

  const previousByKey = new Map<K, T>()
  for (const item of previousItems) previousByKey.set(getKey(item), item)

  let sameOrderAndReferences = nextItems.length === previousItems.length
  const result = nextItems.map((item, index) => {
    const key = getKey(item)
    const previous = previousByKey.get(key)
    const shared = previous !== undefined && isStructurallyEqual(item, previous) ? previous : item
    if (sameOrderAndReferences && shared !== previousItems[index]) sameOrderAndReferences = false
    return shared
  })

  return sameOrderAndReferences ? previousItems : result
}
