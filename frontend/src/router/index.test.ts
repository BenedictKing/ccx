// @vitest-environment jsdom
import { describe, expect, it, vi } from 'vitest'
import router from './index'

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({ isAuthenticated: true }),
}))

vi.mock('@/views/ChannelsView.vue', () => ({
  default: { template: '<div>channels</div>' },
}))

describe('旧页面导航兼容', () => {
  it('旧会话地址跳转到渠道页并保留查询参数', async () => {
    await router.push('/conversations?legacy=1')

    expect(router.currentRoute.value.path).toBe('/channels/messages')
    expect(router.currentRoute.value.query).toEqual({ legacy: '1' })
    expect(router.currentRoute.value.meta.requiresAuth).toBe(true)
  })

  it('仍可直接进入其他协议的渠道页', async () => {
    await router.push('/channels/chat')

    expect(router.currentRoute.value.params.type).toBe('chat')
  })
})
