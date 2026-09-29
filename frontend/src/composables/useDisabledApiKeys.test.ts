import { computed, ref } from 'vue'
import { describe, expect, it, vi } from 'vitest'
import type { ApiService, Channel } from '../services/api'
import type { APIKeyConfig } from '../services/api-types'
import { useDisabledApiKeys } from './useDisabledApiKeys'

const disabledKey = 'ark-test-disabled-key'
const activeKey = 'ark-test-active-key'

const createChannel = () => ref<Channel | null>({
  index: 3,
  apiKeys: [],
  disabledApiKeys: [{
    key: disabledKey,
    reason: 'rate_limited',
    message: 'temporary limit',
    disabledAt: new Date().toISOString(),
  }],
} as unknown as Channel)

type TestForm = {
  apiKeys: string[]
  apiKeyConfigs?: APIKeyConfig[]
}

const createOptions = (
  apiService: Partial<ApiService>,
  form: TestForm = { apiKeys: [] },
  onKeysChanged?: () => Promise<void>,
) => {
  const channel = createChannel()
  const emitError = vi.fn()
  const state = useDisabledApiKeys({
    apiService: apiService as ApiService,
    channel: computed(() => channel.value),
    channelType: computed(() => 'messages' as const),
    emitError,
    form,
    onKeysChanged,
  })
  return { channel, form, state, emitError }
}

describe('useDisabledApiKeys', () => {
  it('restores the key and updates the visible blacklist immediately', async () => {
    const restoreApiKey = vi.fn().mockResolvedValue(undefined)
    const { form, state } = createOptions({ restoreApiKey })

    const restore = state.restoreDisabledKey(disabledKey)
    expect(state.restoringKey.value).toBe(disabledKey)

    await restore

    expect(restoreApiKey).toHaveBeenCalledWith(3, disabledKey)
    expect(form.apiKeys).toEqual([disabledKey])
    expect(state.visibleDisabledKeys.value).toEqual([])
    expect(state.restoringKey.value).toBe('')
  })

  it('恢复 Key 后等待服务端渠道快照刷新', async () => {
    const onKeysChanged = vi.fn().mockResolvedValue(undefined)
    const { state } = createOptions(
      { restoreApiKey: vi.fn().mockResolvedValue(undefined) },
      { apiKeys: [] },
      onKeysChanged,
    )

    await state.restoreDisabledKey(disabledKey)

    expect(onKeysChanged).toHaveBeenCalledTimes(1)
    expect(state.visibleDisabledKeys.value).toHaveLength(1)
  })

  it('ignores a second key restore while the first request is pending', async () => {
    let resolveRestore!: () => void
    const restoreApiKey = vi.fn(() => new Promise<void>(resolve => {
      resolveRestore = resolve
    }))
    const { state } = createOptions({ restoreApiKey })

    const firstRestore = state.restoreDisabledKey(disabledKey)
    const secondRestore = state.restoreDisabledKey(disabledKey)

    expect(restoreApiKey).toHaveBeenCalledTimes(1)
    resolveRestore()
    await Promise.all([firstRestore, secondRestore])
  })

  it('删除拉黑 Key 后立即从可见列表移除', async () => {
    const removeApiKey = vi.fn().mockResolvedValue(undefined)
    const { state } = createOptions({ removeApiKey })

    const remove = state.removeDisabledKey(disabledKey)
    expect(state.removingKey.value).toBe(disabledKey)

    await remove

    expect(removeApiKey).toHaveBeenCalledWith(3, disabledKey)
    expect(state.visibleDisabledKeys.value).toEqual([])
    expect(state.removingKey.value).toBe('')
  })

  it('删除拉黑 Key 时一并清理表单中残留的同名 Key', async () => {
    const removeApiKey = vi.fn().mockResolvedValue(undefined)
    const form: TestForm = { apiKeys: [disabledKey, activeKey] }
    const { state } = createOptions({ removeApiKey }, form)

    await state.removeDisabledKey(disabledKey)

    expect(form.apiKeys).toEqual([activeKey])
  })

  it('删除拉黑 Key 失败时提示错误且不移出列表', async () => {
    const removeApiKey = vi.fn().mockRejectedValue(new Error('boom'))
    const emitError = vi.fn()
    const channel = createChannel()
    const state = useDisabledApiKeys({
      apiService: { removeApiKey } as unknown as ApiService,
      channel: computed(() => channel.value),
      channelType: computed(() => 'messages' as const),
      emitError,
      form: { apiKeys: [] },
    })

    await state.removeDisabledKey(disabledKey)

    expect(emitError).toHaveBeenCalledWith('boom')
    expect(state.visibleDisabledKeys.value).toHaveLength(1)
  })

  // 统一视图：disabledApiKeys 是各协议路由的并集，删除/恢复必须覆盖所有包含该 key 的路由
  const createUnifiedChannel = () => ref<Channel | null>({
    index: 3,
    routeKind: 'messages',
    routeIndex: 3,
    apiKeys: [activeKey],
    disabledApiKeys: [{ key: disabledKey, reason: 'permission_error', message: '', disabledAt: '' }],
    protocolRoutes: [
      { kind: 'messages', index: 3, name: 'c', serviceType: 'claude', apiKeys: [activeKey], disabledApiKeys: [] },
      { kind: 'chat', index: 19, name: 'c', serviceType: 'openai', apiKeys: [activeKey], disabledApiKeys: [{ key: disabledKey, reason: 'permission_error', message: '', disabledAt: '' }] },
      { kind: 'responses', index: 14, name: 'c', serviceType: 'responses', apiKeys: [activeKey], disabledApiKeys: [{ key: disabledKey, reason: 'permission_error', message: '', disabledAt: '' }] },
    ],
  } as unknown as Channel)

  const createUnifiedModelRestrictionChannel = () => {
    const channel = createUnifiedChannel()
    const keyModel = {
      key: activeKey, model: 'model-x', reason: 'model_not_found', message: '', disabledAt: '', recoverAt: '',
    }
    const groupModel = { quotaGroup: 'coding', model: 'model-x', disabledAt: '' }
    channel.value!.disabledKeyModels = [keyModel]
    channel.value!.disabledGroupModels = [groupModel]
    for (const route of channel.value!.protocolRoutes!) {
      const model = route.kind === 'chat' ? ' MODEL-X ' : 'model-x'
      route.disabledKeyModels = route.kind === 'messages' ? [] : [{ ...keyModel, model }]
      route.disabledGroupModels = route.kind === 'messages' ? [] : [{ ...groupModel, model }]
    }
    return channel
  }

  it('统一视图下删除拉黑 Key 覆盖所有包含它的协议路由', async () => {
    const removeApiKey = vi.fn().mockResolvedValue(undefined)
    const removeChatApiKey = vi.fn().mockResolvedValue(undefined)
    const removeResponsesApiKey = vi.fn().mockResolvedValue(undefined)
    const channel = createUnifiedChannel()
    const state = useDisabledApiKeys({
      apiService: { removeApiKey, removeChatApiKey, removeResponsesApiKey } as unknown as ApiService,
      channel: computed(() => channel.value),
      channelType: computed(() => 'messages' as const),
      emitError: vi.fn(),
      form: { apiKeys: [] },
    })

    await state.removeDisabledKey(disabledKey)

    expect(removeChatApiKey).toHaveBeenCalledWith(19, disabledKey)
    expect(removeResponsesApiKey).toHaveBeenCalledWith(14, disabledKey)
    expect(removeApiKey).not.toHaveBeenCalled()
    expect(state.visibleDisabledKeys.value).toEqual([])
  })

  it('统一视图下部分路由已删除（404）时幂等成功', async () => {
    const removeChatApiKey = vi.fn().mockRejectedValue(new Error('API key not found'))
    const removeResponsesApiKey = vi.fn().mockResolvedValue(undefined)
    const emitError = vi.fn()
    const channel = createUnifiedChannel()
    const state = useDisabledApiKeys({
      apiService: { removeChatApiKey, removeResponsesApiKey } as unknown as ApiService,
      channel: computed(() => channel.value),
      channelType: computed(() => 'messages' as const),
      emitError,
      form: { apiKeys: [] },
    })

    await state.removeDisabledKey(disabledKey)

    expect(emitError).not.toHaveBeenCalled()
    expect(state.visibleDisabledKeys.value).toEqual([])
  })

  it('统一视图下恢复拉黑 Key 覆盖所有包含它的协议路由', async () => {
    const restoreChatApiKey = vi.fn().mockResolvedValue(undefined)
    const restoreResponsesApiKey = vi.fn().mockResolvedValue(undefined)
    const channel = createUnifiedChannel()
    const form: TestForm = { apiKeys: [activeKey] }
    const state = useDisabledApiKeys({
      apiService: { restoreChatApiKey, restoreResponsesApiKey } as unknown as ApiService,
      channel: computed(() => channel.value),
      channelType: computed(() => 'messages' as const),
      emitError: vi.fn(),
      form,
    })

    await state.restoreDisabledKey(disabledKey)

    expect(restoreChatApiKey).toHaveBeenCalledWith(19, disabledKey)
    expect(restoreResponsesApiKey).toHaveBeenCalledWith(14, disabledKey)
    expect(form.apiKeys).toEqual([activeKey, disabledKey])
  })

  it('聚合渠道恢复 Key 模型限制时只请求包含该限制的全部协议路由', async () => {
    const restoreKeyModel = vi.fn().mockResolvedValue(undefined)
    const restoreChatKeyModel = vi.fn().mockResolvedValue(undefined)
    const restoreResponsesKeyModel = vi.fn().mockResolvedValue(undefined)
    const { channel, state, emitError } = createOptions({ restoreKeyModel, restoreChatKeyModel, restoreResponsesKeyModel })
    channel.value = createUnifiedModelRestrictionChannel().value
    const record = channel.value!.disabledKeyModels![0]
    channel.value!.protocolRoutes![0].disabledKeyModels = [
      { ...record, key: 'other-key' },
      { ...record, model: 'other-model' },
    ]

    await state.restoreDisabledKeyModel(activeKey, 'model-x')

    expect(restoreChatKeyModel).toHaveBeenCalledWith(19, activeKey, 'model-x')
    expect(restoreResponsesKeyModel).toHaveBeenCalledWith(14, activeKey, 'model-x')
    expect(restoreKeyModel).not.toHaveBeenCalled()
    expect(emitError).not.toHaveBeenCalled()
    expect(state.visibleDisabledKeyModels.value).toEqual([])
    expect(state.restoringKeyModel.value).toBe('')
  })

  it('聚合渠道恢复 Key 模型部分失败时保留记录，重试兼容已恢复的路由', async () => {
    const restoreChatKeyModel = vi.fn()
      .mockResolvedValueOnce(undefined)
      .mockRejectedValue(new Error('(Key test-key, 模型 model-x) 不在限制列表中'))
    const restoreResponsesKeyModel = vi.fn()
      .mockRejectedValueOnce(new Error('restore failed'))
      .mockResolvedValue(undefined)
    const { channel, state, emitError } = createOptions({ restoreChatKeyModel, restoreResponsesKeyModel })
    channel.value = createUnifiedModelRestrictionChannel().value

    await state.restoreDisabledKeyModel(activeKey, 'model-x')

    expect(restoreChatKeyModel).toHaveBeenCalledTimes(1)
    expect(restoreResponsesKeyModel).toHaveBeenCalledTimes(1)
    expect(emitError).toHaveBeenCalledWith('restore failed')
    expect(state.visibleDisabledKeyModels.value).toHaveLength(1)
    expect(state.restoringKeyModel.value).toBe('')

    await state.restoreDisabledKeyModel(activeKey, 'model-x')

    expect(restoreChatKeyModel).toHaveBeenCalledTimes(2)
    expect(restoreResponsesKeyModel).toHaveBeenCalledTimes(2)
    expect(emitError).toHaveBeenCalledTimes(1)
    expect(state.visibleDisabledKeyModels.value).toEqual([])
  })

  it('聚合渠道恢复分组模型时只请求包含该策略的全部协议路由', async () => {
    const result = { success: true, quotaGroup: 'coding', model: 'model-x', affectedKeyCount: 2 }
    const restoreGroupModel = vi.fn().mockResolvedValue(result)
    const { channel, state, emitError } = createOptions({ restoreGroupModel })
    channel.value = createUnifiedModelRestrictionChannel().value
    channel.value!.protocolRoutes![0].disabledGroupModels = [
      { quotaGroup: 'other-group', model: 'model-x', disabledAt: '' },
      { quotaGroup: 'coding', model: 'other-model', disabledAt: '' },
      { quotaGroup: '', key: 'coding', model: 'model-x', disabledAt: '' },
    ]
    channel.value!.protocolRoutes![1].disabledGroupModels![0].quotaGroup = ' coding '

    expect(await state.restoreDisabledGroupModel(channel.value!.disabledGroupModels![0])).toEqual(result)

    expect(restoreGroupModel).toHaveBeenCalledTimes(2)
    expect(restoreGroupModel).toHaveBeenCalledWith('chat', 19, 'model-x', { quotaGroup: 'coding', apiKey: undefined })
    expect(restoreGroupModel).toHaveBeenCalledWith('responses', 14, 'model-x', { quotaGroup: 'coding', apiKey: undefined })
    expect(emitError).not.toHaveBeenCalled()
    expect(state.visibleDisabledGroupModels.value).toEqual([])
    expect(state.changingGroupModel.value).toBe('')
  })

  it('聚合渠道恢复空组模型按 Key 精确匹配，不影响其他 Key 或同名分组', async () => {
    const restoreGroupModel = vi.fn().mockResolvedValue({ success: true, quotaGroup: '', model: 'model-x', affectedKeyCount: 1 })
    const { channel, state, emitError } = createOptions({ restoreGroupModel })
    channel.value = createUnifiedModelRestrictionChannel().value
    const record = { quotaGroup: '', key: activeKey, model: 'model-x', disabledAt: '' }
    const unrelatedRecords = [
      { ...record, key: 'other-key' },
      { ...record, model: 'other-model' },
      { quotaGroup: activeKey, model: 'model-x', disabledAt: '' },
    ]
    channel.value!.disabledGroupModels = [record, ...unrelatedRecords]
    for (const route of channel.value!.protocolRoutes!) {
      route.disabledGroupModels = route.kind === 'messages' ? unrelatedRecords : [record]
    }

    await state.restoreDisabledGroupModel(record)

    expect(restoreGroupModel).toHaveBeenCalledTimes(2)
    expect(restoreGroupModel).toHaveBeenCalledWith('chat', 19, 'model-x', { quotaGroup: undefined, apiKey: activeKey })
    expect(restoreGroupModel).toHaveBeenCalledWith('responses', 14, 'model-x', { quotaGroup: undefined, apiKey: activeKey })
    expect(emitError).not.toHaveBeenCalled()
    expect(state.visibleDisabledGroupModels.value).toEqual(unrelatedRecords)
  })

  it('聚合渠道恢复分组模型部分失败时保留记录并允许重试', async () => {
    const result = { success: true, quotaGroup: 'coding', model: 'model-x', affectedKeyCount: 2 }
    const restoreGroupModel = vi.fn()
      .mockRejectedValueOnce(new Error('restore failed'))
      .mockResolvedValue(result)
    const { channel, state, emitError } = createOptions({ restoreGroupModel })
    channel.value = createUnifiedModelRestrictionChannel().value
    const record = channel.value!.disabledGroupModels![0]

    await state.restoreDisabledGroupModel(record)

    expect(restoreGroupModel).toHaveBeenCalledTimes(2)
    expect(emitError).toHaveBeenCalledWith('restore failed')
    expect(state.visibleDisabledGroupModels.value).toHaveLength(1)
    expect(state.changingGroupModel.value).toBe('')

    await state.restoreDisabledGroupModel(record)

    expect(restoreGroupModel).toHaveBeenCalledTimes(4)
    expect(emitError).toHaveBeenCalledTimes(1)
    expect(state.visibleDisabledGroupModels.value).toEqual([])
  })

  it.each([
    ['messages', 'restoreKeyModel'],
    ['chat', 'restoreChatKeyModel'],
    ['responses', 'restoreResponsesKeyModel'],
    ['gemini', 'restoreGeminiKeyModel'],
    ['images', 'restoreImagesKeyModel'],
    ['vectors', 'restoreVectorsKeyModel'],
  ] as const)('没有协议明细时恢复模型限制使用 %s 的真实路由和索引', async (kind, method) => {
    const restoreKeyModel = vi.fn().mockResolvedValue(undefined)
    const restoreGroupModel = vi.fn().mockResolvedValue({ success: true, quotaGroup: 'coding', model: 'model-x', affectedKeyCount: 1 })
    const { channel, state, emitError } = createOptions({ [method]: restoreKeyModel, restoreGroupModel })
    channel.value!.routeKind = kind
    channel.value!.routeIndex = 9

    await state.restoreDisabledKeyModel(activeKey, 'model-x')
    await state.restoreDisabledGroupModel({ quotaGroup: 'coding', model: 'model-x' })

    expect(restoreKeyModel).toHaveBeenCalledWith(9, activeKey, 'model-x')
    expect(restoreGroupModel).toHaveBeenCalledWith(kind, 9, 'model-x', { quotaGroup: 'coding', apiKey: undefined })
    expect(emitError).not.toHaveBeenCalled()
  })

  it('统一视图下禁用分组模型覆盖所有持有该 Key 的协议路由', async () => {
    const disableGroupModel = vi.fn().mockResolvedValue({ success: true, quotaGroup: 'coding', model: 'model-x', affectedKeyCount: 3 })
    const { channel, state, emitError } = createOptions({ disableGroupModel })
    channel.value = createUnifiedChannel().value

    await state.disableGroupModel(activeKey, 'model-x')

    expect(disableGroupModel).toHaveBeenCalledTimes(3)
    expect(disableGroupModel).toHaveBeenCalledWith('messages', 3, activeKey, 'model-x')
    expect(disableGroupModel).toHaveBeenCalledWith('chat', 19, activeKey, 'model-x')
    expect(disableGroupModel).toHaveBeenCalledWith('responses', 14, activeKey, 'model-x')
    expect(emitError).not.toHaveBeenCalled()
    expect(state.visibleDisabledGroupModels.value).toEqual([
      { quotaGroup: 'coding', key: activeKey, model: 'model-x', disabledAt: expect.any(String) },
    ])
  })

  it('Key 只属于非主协议路由时，禁用分组模型打在该路由而非主路由', async () => {
    // 旧实现固定打「主路由」：后端按渠道解析 Key 所属配额组，Key 不在该渠道会直接
    // 报「API Key 不属于该渠道」，暂存条目静默丢失；其它协议也不受限制。
    const disableGroupModel = vi.fn().mockResolvedValue({ success: true, quotaGroup: '', model: 'model-x', affectedKeyCount: 1 })
    const { channel, state, emitError } = createOptions({ disableGroupModel })
    const unified = createUnifiedChannel().value!
    unified.protocolRoutes![0].apiKeys = []
    unified.protocolRoutes![2].apiKeys = []
    channel.value = unified

    await state.disableGroupModel(activeKey, 'model-x')

    expect(disableGroupModel).toHaveBeenCalledTimes(1)
    expect(disableGroupModel).toHaveBeenCalledWith('chat', 19, activeKey, 'model-x')
    expect(emitError).not.toHaveBeenCalled()
  })

  it('暂停成功后立即写入 enabled=false，并使用真实路由索引', async () => {
    const suspendApiKey = vi.fn().mockResolvedValue(undefined)
    const form: TestForm = { apiKeys: [activeKey] }
    const { channel, state } = createOptions({ suspendApiKey }, form)
    channel.value!.routeIndex = 9

    await state.suspendKey(activeKey)

    expect(suspendApiKey).toHaveBeenCalledWith(9, activeKey)
    expect(form.apiKeyConfigs).toEqual([{ key: activeKey, enabled: false }])
  })

  it('暂停已有配置的 Key 时保留名称和限流配置', async () => {
    const suspendApiKey = vi.fn().mockResolvedValue(undefined)
    const form: TestForm = {
      apiKeys: [activeKey],
      apiKeyConfigs: [{ key: activeKey, name: '主 Key', rateLimitRpm: 60, enabled: true }],
    }
    const { state } = createOptions({ suspendApiKey }, form)

    await state.suspendKey(activeKey)

    expect(form.apiKeyConfigs).toEqual([
      { key: activeKey, name: '主 Key', rateLimitRpm: 60, enabled: false },
    ])
  })

  it('恢复 Key 时移除 enabled=false 并保留其他配置', async () => {
    const resumeApiKey = vi.fn().mockResolvedValue(undefined)
    const form: TestForm = {
      apiKeys: [activeKey],
      apiKeyConfigs: [{ key: activeKey, name: '备用 Key', weight: 2, enabled: false }],
    }
    const { state } = createOptions({ resumeApiKey }, form)

    await state.resumeKey(activeKey)

    expect(form.apiKeyConfigs).toEqual([{ key: activeKey, name: '备用 Key', weight: 2 }])
  })

  it('恢复仅用于暂停标记的 Key 时清理空配置项', async () => {
    const resumeApiKey = vi.fn().mockResolvedValue(undefined)
    const form: TestForm = {
      apiKeys: [activeKey],
      apiKeyConfigs: [{ key: activeKey, enabled: false }],
    }
    const { state } = createOptions({ resumeApiKey }, form)

    await state.resumeKey(activeKey)

    expect(form.apiKeyConfigs).toBeUndefined()
  })

  it('人工禁用分组模型时调用统一 kind API 并立即显示记录', async () => {
    const disableGroupModel = vi.fn().mockResolvedValue({
      success: true,
      quotaGroup: 'coding',
      model: 'model-x',
      affectedKeyCount: 2,
    })
    const form: TestForm = {
      apiKeys: [activeKey, 'same-group-key'],
      apiKeyConfigs: [
        { key: activeKey, quotaGroup: 'coding' },
        { key: 'same-group-key', quotaGroup: 'coding' },
      ],
    }
    const { state } = createOptions({ disableGroupModel }, form)

    const result = await state.disableGroupModel(activeKey, ' model-x ')

    expect(disableGroupModel).toHaveBeenCalledWith('messages', 3, activeKey, 'model-x')
    expect(result?.affectedKeyCount).toBe(2)
    expect(state.visibleDisabledGroupModels.value).toEqual([
      expect.objectContaining({ quotaGroup: 'coding', key: activeKey, model: 'model-x' }),
    ])
  })

  it('恢复分组模型优先按 quotaGroup 定位', async () => {
    const restoreGroupModel = vi.fn().mockResolvedValue({ success: true, quotaGroup: 'coding', model: 'model-x', affectedKeyCount: 0 })
    const { channel, state } = createOptions({ restoreGroupModel })
    channel.value!.disabledGroupModels = [{ quotaGroup: 'coding', key: activeKey, model: 'model-x', disabledAt: new Date().toISOString() }]

    await state.restoreDisabledGroupModel(channel.value!.disabledGroupModels[0])

    expect(restoreGroupModel).toHaveBeenCalledWith('messages', 3, 'model-x', { quotaGroup: 'coding', apiKey: undefined })
    expect(state.visibleDisabledGroupModels.value).toEqual([])
  })

  it('恢复空组记录时回退使用记录中的 key', async () => {
    const restoreGroupModel = vi.fn().mockResolvedValue({ success: true, quotaGroup: '', model: 'model-x', affectedKeyCount: 1 })
    const { channel, state } = createOptions({ restoreGroupModel })
    channel.value!.disabledGroupModels = [{ quotaGroup: '', key: activeKey, model: 'model-x', disabledAt: new Date().toISOString() }]

    await state.restoreDisabledGroupModel(channel.value!.disabledGroupModels[0])

    expect(restoreGroupModel).toHaveBeenCalledWith('messages', 3, 'model-x', { quotaGroup: undefined, apiKey: activeKey })
  })

  it('不同独立 Key 暂存同模型后保留两条记录，恢复其中一条不影响另一条', async () => {
    const result = { success: true, quotaGroup: '', model: 'model-x', affectedKeyCount: 1 }
    const disableGroupModel = vi.fn().mockResolvedValue(result)
    const restoreGroupModel = vi.fn().mockResolvedValue(result)
    const { state } = createOptions({ disableGroupModel, restoreGroupModel })

    state.stageGroupModelDisable('key-a', 'model-x')
    state.stageGroupModelDisable('key-b', 'model-x')
    await state.flushStagedGroupModelDisables()
    expect(state.visibleDisabledGroupModels.value.map(record => record.key)).toEqual(['key-a', 'key-b'])

    await state.restoreDisabledGroupModel({ quotaGroup: '', key: 'key-a', model: 'model-x' })
    expect(restoreGroupModel).toHaveBeenCalledWith('messages', 3, 'model-x', { quotaGroup: undefined, apiKey: 'key-a' })
    expect(state.visibleDisabledGroupModels.value.map(record => record.key)).toEqual(['key-b'])
  })

  it('恢复服务端独立 Key 记录不会隐藏其他 Key 或同名分组记录', async () => {
    const restoreGroupModel = vi.fn().mockResolvedValue({ success: true, quotaGroup: '', model: 'model-x', affectedKeyCount: 1 })
    const { channel, state } = createOptions({ restoreGroupModel })
    const records = [
      { quotaGroup: '', key: 'key-a', model: 'model-x', disabledAt: '' },
      { quotaGroup: '', key: 'key-b', model: 'model-x', disabledAt: '' },
      { quotaGroup: 'key-a', model: 'model-x', disabledAt: '' },
    ]
    channel.value!.disabledGroupModels = records

    await state.restoreDisabledGroupModel(records[0])
    expect(state.visibleDisabledGroupModels.value).toEqual(records.slice(1))
  })

  it('同一明确分组的不同 Key 排除同模型仍合并为一条记录', async () => {
    const disableGroupModel = vi.fn().mockResolvedValue({ success: true, quotaGroup: 'coding', model: 'model-x', affectedKeyCount: 2 })
    const { state } = createOptions({ disableGroupModel })
    await state.disableGroupModel('key-a', 'model-x')
    await state.disableGroupModel('key-b', 'model-x')
    expect(state.visibleDisabledGroupModels.value).toHaveLength(1)
  })

  it('flush 时渠道已清空仍按快照发出 disable 请求', async () => {
    // 复现保存链路：saveChannel 成功 → closeEditChannelModal 同步置空 editingChannel，
    // watch(show) 才异步 flush——不传快照时请求会被静默吞掉（暂存无声丢失）。
    const disableGroupModel = vi.fn().mockResolvedValue({ success: true, quotaGroup: '', model: 'model-x', affectedKeyCount: 1 })
    const { channel, state } = createOptions({ disableGroupModel })
    const snapshot = {
      ...channel.value!,
      routeKind: 'chat' as const,
      routeIndex: 7,
    }

    state.stageGroupModelDisable('key-a', 'model-x')
    channel.value = null
    await state.flushStagedGroupModelDisables(snapshot)

    expect(disableGroupModel).toHaveBeenCalledTimes(1)
    expect(disableGroupModel).toHaveBeenCalledWith('chat', 7, 'key-a', 'model-x')
    expect(state.pendingGroupModelDisables.value).toEqual([])
  })

  it('聚合渠道恢复 Key 时覆盖拥有该 Key 的全部协议路由', async () => {
    const resumeApiKey = vi.fn().mockResolvedValue(undefined)
    const resumeChatApiKey = vi.fn().mockResolvedValue(undefined)
    const resumeResponsesApiKey = vi.fn().mockResolvedValue(undefined)
    const resumeGeminiApiKey = vi.fn().mockResolvedValue(undefined)
    const form: TestForm = {
      apiKeys: [activeKey],
      apiKeyConfigs: [{ key: activeKey, enabled: false }],
    }
    const { channel, state } = createOptions({
      resumeApiKey,
      resumeChatApiKey,
      resumeResponsesApiKey,
      resumeGeminiApiKey,
    }, form)
    channel.value!.protocolRoutes = [
      { kind: 'messages', index: 3, name: 'provider-claude', serviceType: 'claude', apiKeys: [activeKey] },
      { kind: 'chat', index: 7, name: 'provider-chat', serviceType: 'openai', apiKeys: [activeKey] },
      { kind: 'responses', index: 5, name: 'provider-codex', serviceType: 'responses', apiKeys: ['other-key'] },
      { kind: 'gemini', index: 2, name: 'provider-gemini', serviceType: 'gemini', apiKeys: [activeKey] },
    ]

    await state.resumeKey(activeKey)

    expect(resumeApiKey).toHaveBeenCalledWith(3, activeKey)
    expect(resumeChatApiKey).toHaveBeenCalledWith(7, activeKey)
    expect(resumeGeminiApiKey).toHaveBeenCalledWith(2, activeKey)
    expect(resumeResponsesApiKey).not.toHaveBeenCalled()
    expect(form.apiKeyConfigs).toBeUndefined()
  })

  it('聚合渠道暂停 Key 时覆盖拥有该 Key 的全部协议路由', async () => {
    const suspendApiKey = vi.fn().mockResolvedValue(undefined)
    const suspendChatApiKey = vi.fn().mockResolvedValue(undefined)
    const form: TestForm = { apiKeys: [activeKey] }
    const { channel, state } = createOptions({ suspendApiKey, suspendChatApiKey }, form)
    channel.value!.protocolRoutes = [
      { kind: 'messages', index: 3, name: 'provider-claude', serviceType: 'claude', apiKeys: [activeKey] },
      { kind: 'chat', index: 7, name: 'provider-chat', serviceType: 'openai', apiKeys: [activeKey] },
    ]

    await state.suspendKey(activeKey)

    expect(suspendApiKey).toHaveBeenCalledWith(3, activeKey)
    expect(suspendChatApiKey).toHaveBeenCalledWith(7, activeKey)
    expect(form.apiKeyConfigs).toEqual([{ key: activeKey, enabled: false }])
  })
})
