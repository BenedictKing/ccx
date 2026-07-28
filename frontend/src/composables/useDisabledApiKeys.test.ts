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
  const state = useDisabledApiKeys({
    apiService: apiService as ApiService,
    channel: computed(() => channel.value),
    channelType: computed(() => 'messages' as const),
    emitError: vi.fn(),
    form,
    onKeysChanged,
  })
  return { channel, form, state }
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
})
