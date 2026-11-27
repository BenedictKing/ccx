import { computed } from 'vue'
import { describe, expect, it } from 'vitest'

import { codexSourceModelNames } from '../../../shared/model-mapping'
import { useEditChannelOptions } from './editChannelOptions'

describe('useEditChannelOptions', () => {
  it('共享列表包含当前 GPT-5.6 和 GPT-6 Codex 源模型 ID', () => {
    expect(codexSourceModelNames).toEqual(expect.arrayContaining([
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
    ]))
  })

  it('为 Responses 和 Chat 提供共享的 Codex 源模型候选', () => {
    for (const channelType of ['responses', 'chat'] as const) {
      const options = useEditChannelOptions(computed(() => channelType), { modelMapping: {} }, key => key)

      expect(options.allSourceModelOptions.value.map(option => option.value)).toEqual(codexSourceModelNames)
    }
  })
})
