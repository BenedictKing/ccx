const encoder = new globalThis.TextEncoder()

export const codexSourceModelNames = [
  'astra',
  'sol',
  'terra',
  'luna',
  'codex-auto-review',
  'gpt-6.2-astra',
  'gpt-6.2-sol',
  'gpt-6.1-sol',
  'gpt-6.1-terra',
  'gpt-6-astra',
  'gpt-6-sol',
  'gpt-6-luna',
  'gpt-5.6-sol',
  'gpt-5.6-terra',
  'gpt-5.6-luna',
] as const

// 与 Go RedirectModelWithRule 一致：精确匹配优先，其次按 UTF-8 字节长度和字典序匹配。
export function resolveMappedModel(model: string, mapping: Readonly<Record<string, string>> = {}): string {
  if (Object.prototype.hasOwnProperty.call(mapping, model)) return mapping[model]

  const candidates = Object.entries(mapping)
    .filter(([source]) => model.includes(source))
    .map(([source, target]) => ({ target, bytes: encoder.encode(source) }))
    .sort((a, b) => {
      if (a.bytes.length !== b.bytes.length) return b.bytes.length - a.bytes.length
      for (let index = 0; index < a.bytes.length; index++) {
        if (a.bytes[index] !== b.bytes[index]) return a.bytes[index] - b.bytes[index]
      }
      return 0
    })

  return candidates[0]?.target ?? model
}
