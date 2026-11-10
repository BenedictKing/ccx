import { computed, reactive } from 'vue'
import { describe, expect, it, vi } from 'vitest'
import { ApiService, type Channel } from '../services/api'
import type { APIKeyConfig } from '../services/api-types'
import { useDisabledApiKeys } from './useDisabledApiKeys'

type ChannelType = 'messages' | 'chat' | 'responses' | 'gemini' | 'images' | 'vectors'

function createState(channelType: ChannelType = 'chat') {
  const form = reactive<{ apiKeys: string[]; apiKeyConfigs?: APIKeyConfig[] }>({
    apiKeys: ['sk-test'],
    apiKeyConfigs: [{ key: 'sk-test', groupMultiplier: 0.5 }],
  })
  const apiService = {
    setApiKeySuspended: vi.fn().mockResolvedValue(undefined),
    removeChatApiKey: vi.fn().mockResolvedValue(undefined),
    updateGroupModelRestriction: vi.fn().mockResolvedValue({
      success: true,
      quotaGroup: 'vip',
      model: 'Model-A',
      affectedKeyCount: 2,
    }),
  } as unknown as ApiService
  const state = useDisabledApiKeys({
    apiService,
    channel: computed(() => ({
      index: 7,
      disabledApiKeys: [{ key: 'sk-blocked', reason: 'invalid', message: '', disabledAt: '' }],
    } as Channel)),
    channelType: computed(() => channelType),
    emitError: vi.fn(),
    form,
  })
  return { apiService, form, state }
}

describe('useDisabledApiKeys 暂停与恢复', () => {
  it('暂停调用当前协议端点并保留其他 Key 配置', async () => {
    const { apiService, form, state } = createState()

    await state.suspendKey('sk-test')

    expect(apiService.setApiKeySuspended).toHaveBeenCalledWith('chat', 7, 'sk-test', true)
    expect(form.apiKeys).toEqual(['sk-test'])
    expect(form.apiKeyConfigs?.[0]).toMatchObject({
      key: 'sk-test',
      enabled: false,
      groupMultiplier: 0.5,
    })
    expect(state.suspendingKey.value).toBe('')
  })

  it('恢复清除 enabled 限制', async () => {
    const { apiService, form, state } = createState('messages')
    form.apiKeyConfigs = [{ key: 'sk-test', enabled: false, name: 'primary' }]

    await state.resumeKey('sk-test')

    expect(apiService.setApiKeySuspended).toHaveBeenCalledWith('messages', 7, 'sk-test', false)
    expect(form.apiKeyConfigs?.[0].enabled).toBeUndefined()
    expect(form.apiKeyConfigs?.[0].name).toBe('primary')
  })

  it('没有附加配置的 Key 也能暂停', async () => {
    const { form, state } = createState()
    form.apiKeyConfigs = undefined

    await state.suspendKey('sk-test')

    expect(form.apiKeyConfigs).toEqual([{ key: 'sk-test', enabled: false }])
  })

  it('彻底删除拉黑 Key 后立即从列表隐藏', async () => {
    const { apiService, state } = createState()

    await state.removeDisabledKey('sk-blocked')

    expect(apiService.removeChatApiKey).toHaveBeenCalledWith(7, 'sk-blocked')
    expect(state.visibleDisabledKeys.value).toEqual([])
    expect(state.removingKey.value).toBe('')
  })

  it('分组模型可禁用并按策略标识恢复', async () => {
    const { apiService, state } = createState()

    await state.disableGroupModel('sk-test', 'Model-A')

    expect(apiService.updateGroupModelRestriction).toHaveBeenCalledWith('chat', 7, 'sk-test', '', 'Model-A', true)
    expect(state.visibleDisabledGroupModels.value).toHaveLength(1)
    const record = state.visibleDisabledGroupModels.value[0]
    expect(record).toMatchObject({ quotaGroup: 'vip', model: 'Model-A' })

    await state.restoreDisabledGroupModel(record)

    expect(apiService.updateGroupModelRestriction).toHaveBeenLastCalledWith('chat', 7, '', 'vip', 'Model-A', false)
    expect(state.visibleDisabledGroupModels.value).toEqual([])
  })
})
