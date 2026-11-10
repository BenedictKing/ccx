import { computed, ref } from 'vue'
import { describe, expect, it, vi } from 'vitest'
import type { Channel } from '@/services/admin-api'
import { useChannelApiKeys } from './useChannelApiKeys'

describe('useChannelApiKeys 删除拉黑 Key', () => {
  it('删除成功后隐藏记录并移除同名活跃 Key', async () => {
    const removeApiKey = vi.fn().mockResolvedValue(undefined)
    const state = useChannelApiKeys({
      channel: computed(() => ({ index: 7 } as Channel)),
      channelType: () => 'chat',
      disabledApiKeys: computed(() => [{ key: 'sk-blocked', reason: 'invalid', message: '', disabledAt: '' }]),
      error: ref(''),
      fallbackApiKeysText: () => '',
      isEditMode: computed(() => true),
      parseLines: (text: string) => text.split('\n').filter(Boolean),
      restoreApiKey: vi.fn(),
      removeApiKey,
      t: (key: string) => key,
    })
    state.existingApiKeys.value = ['sk-blocked', 'sk-active']

    await state.handleDisabledKeyRemove('sk-blocked')

    expect(removeApiKey).toHaveBeenCalledWith(7, 'sk-blocked', 'chat')
    expect(state.visibleDisabledKeys.value).toEqual([])
    expect(state.existingApiKeys.value).toEqual(['sk-active'])
    expect(state.removingKey.value).toBe('')
  })
})
