// @vitest-environment jsdom
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import type { Channel, ChannelProtocolRoute } from '../services/api'
import { useChannelStore } from './channel'

const mocks = vi.hoisted(() => ({ updateChannelV2: vi.fn(), updateManagedAccount: vi.fn() }))
vi.mock('@/services/api', () => ({ api: mocks }))
vi.mock('@/services/autopilot-api', () => ({ autoAddChannel: vi.fn() }))
vi.mock('vue-router', () => ({ useRouter: () => ({ currentRoute: { value: { params: { type: 'responses' } } } }) }))
vi.mock('@/stores/preferences', () => ({ usePreferencesStore: () => ({ uiLanguage: 'zh-CN' }) }))
vi.mock('@/i18n', () => ({ translate: (_locale: string, key: string) => key }))

const routes: ChannelProtocolRoute[] = [
  { kind: 'responses', index: 1, channelUid: 'luna', name: 'luna', serviceType: 'responses', supportedModels: ['*luna'] },
  { kind: 'responses', index: 3, channelUid: 'code', name: 'code', serviceType: 'responses', supportedModels: ['*sol'] },
]
const original = { index: 1, channelUid: 'luna', name: 'aihub', serviceType: 'responses',
  baseUrl: 'https://example.com', apiKeys: [], supportedModels: ['*luna'], protocolRoutes: routes } as Channel

describe('渠道白名单保存', () => {
  beforeEach(() => {
    setActivePinia(createPinia())
    mocks.updateChannelV2.mockReset().mockResolvedValue(undefined)
    mocks.updateManagedAccount.mockReset().mockResolvedValue({})
  })
  it.each([false, true])('普通/托管渠道均按 UID 保存每条路由，autoManaged=%s', async autoManaged => {
    const store = useChannelStore()
    const edited = { ...original, protocolRoutes: [routes[0]!, { ...routes[1]!, supportedModels: [] }] }
    const result = await store.saveChannel(edited, 1, {
      channelType: 'responses', autoManaged, accountUid: 'account', originalChannel: original,
    })
    expect(result.success).toBe(true)
    expect(mocks.updateChannelV2).toHaveBeenLastCalledWith('code', { kind: 'responses', supportedModels: [] })
    expect(mocks.updateChannelV2.mock.calls.filter(([uid]) => uid === 'code')).toHaveLength(1)
  })
  it('保存模型限制失败时不返回成功', async () => {
    const store = useChannelStore()
    mocks.updateChannelV2.mockImplementation((uid: string) => uid === 'code' ? Promise.reject(new Error('save failed')) : Promise.resolve())
    await expect(store.saveChannel({ ...original, protocolRoutes: [routes[0]!, { ...routes[1]!, supportedModels: ['*astra'] }] }, 1,
      { channelType: 'responses', originalChannel: original })).rejects.toThrow('save failed')
  })
})
