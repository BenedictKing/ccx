import { describe, expect, it } from 'vitest'
import { resolveMappedModel } from '../../../shared/model-mapping'

describe('resolveMappedModel', () => {
  it.each([
    ['sol-sun', { sun: 'sun-target', sol: 'sol-target' }, 'sol-target'],
    ['SOL-sol', { sol: 'lower', SOL: 'upper' }, 'upper'],
    ['gpt-sol', { sol: 'short', 'gpt-sol': 'exact' }, 'exact'],
    ['模型-sol', { sol: 'ascii', 模型: 'unicode' }, 'unicode'],
    ['𐀀-\ue000a', { '𐀀': 'astral', '\ue000a': 'bmp' }, 'bmp'],
    ['sol', { sol: '' }, ''],
    ['astra', { sol: 'other' }, 'astra']
  ])('解析 %s 时与后端规则一致且不依赖录入顺序', (model, mapping, expected) => {
    expect(resolveMappedModel(model, mapping)).toBe(expected)
    expect(resolveMappedModel(model, Object.fromEntries(Object.entries(mapping).reverse()))).toBe(expected)
  })
})
