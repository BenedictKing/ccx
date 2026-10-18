// @vitest-environment jsdom
import { createPinia, setActivePinia } from 'pinia'
import { expect, it, vi } from 'vitest'
import type { Channel } from '../services/api'
import { useChannelStore } from './channel'

const mocks = vi.hoisted(() => ({ updateChannelV2: vi.fn(), updateManagedAccount: vi.fn() }))
vi.mock('@/services/api', () => ({ api: mocks }))
vi.mock('@/services/autopilot-api', () => ({ autoAddChannel: vi.fn() }))
vi.mock('vue-router', () => ({ useRouter: () => ({ currentRoute: { value: { params: { type: 'responses' } } } }) }))
vi.mock('@/stores/preferences', () => ({ usePreferencesStore: () => ({ uiLanguage: 'zh-CN' }) }))
vi.mock('@/i18n', () => ({ translate: (_locale: string, key: string) => key }))

it.each([false, true])('多协议渠道按 Responses 路由保存续接设置，原值=%s', async enabled => {
  setActivePinia(createPinia())
  mocks.updateChannelV2.mockReset().mockResolvedValue(undefined)
  const store = useChannelStore()
  store.chatChannelsData.channels = [{ index: 0, channelUid: 'chat' } as Channel]
  const mixed = {
    index: 0,
    name: 'test',
    baseUrl: 'https://example.com',
    apiKeys: [],
    serviceType: 'openai',
    channelUid: 'chat',
    responsesReasoningFold: false,
    protocolRoutes: [
      { kind: 'chat', index: 0, channelUid: 'chat', name: 'test', serviceType: 'openai' },
      {
        kind: 'responses',
        index: 1,
        channelUid: 'response',
        name: 'test',
        serviceType: 'responses',
        responsesReasoningFold: enabled
      }
    ]
  } as Channel
  const result = await store.saveChannel({ ...mixed, responsesReasoningFold: !enabled }, 0, {
    channelType: 'chat',
    originalChannel: mixed
  })
  expect(result.success).toBe(true)
  expect(mocks.updateChannelV2.mock.calls.find(([uid]) => uid === 'chat')?.[1]).not.toHaveProperty(
    'responsesReasoningFold'
  )
  expect(mocks.updateChannelV2).toHaveBeenCalledWith('response', {
    kind: 'responses',
    responsesReasoningFold: !enabled
  })
})
