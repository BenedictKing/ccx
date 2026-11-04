import { computed, reactive, ref } from 'vue'
import { describe, expect, it } from 'vitest'
import { useEditChannelOptions } from './editChannelOptions'

type ChannelType = 'messages' | 'chat' | 'responses' | 'gemini' | 'images' | 'vectors'

function setup(channelType: ChannelType, modelMapping: Record<string, string> = {}) {
  const type = ref(channelType)
  const form = reactive({ modelMapping })
  return {
    type,
    form,
    ...useEditChannelOptions(computed(() => type.value), form),
  }
}

describe('useEditChannelOptions source models', () => {
  it('provides messages aliases and filters configured sources', () => {
    const { sourceModelOptions } = setup('messages', { opus: 'claude-opus-4-6' })

    expect(sourceModelOptions.value.map(option => option.value)).toEqual(['fable', 'sonnet', 'haiku'])
  })

  it('updates built-in sources when the channel type changes', () => {
    const { type, sourceModelOptions } = setup('chat')

    expect(sourceModelOptions.value.map(option => option.value)).toContain('codex')
    type.value = 'responses'
    expect(sourceModelOptions.value.map(option => option.value)).toContain('codex-auto-review')
    type.value = 'vectors'
    expect(sourceModelOptions.value).toEqual([])
  })

  it('keeps source options reactive to mapping edits', () => {
    const { form, sourceModelOptions } = setup('images')

    expect(sourceModelOptions.value.map(option => option.value)).toContain('gpt-image-2')
    form.modelMapping = { 'gpt-image-2': 'vendor-image-model' }
    expect(sourceModelOptions.value.map(option => option.value)).not.toContain('gpt-image-2')
  })
})
