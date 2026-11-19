// @vitest-environment jsdom
import { createApp, defineComponent, h, nextTick } from 'vue'
import { afterEach, describe, expect, it, vi } from 'vitest'
import SubagentRoutingPanel from './SubagentRoutingPanel.vue'

const { get, put } = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn() }))
vi.mock('@/composables/useLanguage', () => ({ useLanguage: () => ({ t: (key: string) => key }) }))
vi.mock('@/composables/useAdminApi', () => ({
  AdminApiError: class extends Error {},
  useAdminApi: () => ({ get, put })
}))
vi.mock('@/components/ui/button', () => ({
  Button: defineComponent({
    props: ['disabled'],
    setup:
      (props, { slots }) =>
      () =>
        h('button', { disabled: props.disabled }, slots.default?.())
  })
}))

const draft = {
  activated: false,
  revision: 'r1',
  legacyMixed: false,
  policy: { model: 'sol', channels: [] },
  channels: []
}
const cleanups: (() => void)[] = []
afterEach(() => {
  cleanups.splice(0).forEach(cleanup => cleanup())
  vi.clearAllMocks()
})
async function renderPanel() {
  const container = document.createElement('div')
  document.body.append(container)
  const app = createApp({ render: () => h(SubagentRoutingPanel, { type: 'responses' }) })
  app.mount(container)
  cleanups.push(() => {
    app.unmount()
    container.remove()
  })
  await Promise.resolve()
  await nextTick()
  return container
}
describe('SubagentRoutingPanel', () => {
  it('直接保存未修改的迁移草稿，成功后禁止重复保存', async () => {
    get.mockResolvedValue(draft)
    put.mockResolvedValue({ ...draft, activated: true, revision: 'r2' })
    const container = await renderPanel()
    const save = [...container.querySelectorAll('button')].find(button => button.textContent?.includes('common.save'))!
    expect(save.disabled).toBe(false)
    save.click()
    await Promise.resolve()
    await nextTick()
    expect(put).toHaveBeenCalledWith('/api/responses/channels/subagent-routing', {
      revision: 'r1',
      policy: draft.policy
    })
    expect(save.disabled).toBe(true)
  })
  it('加载失败时不允许提交空策略', async () => {
    get.mockRejectedValue(new Error('offline'))
    const container = await renderPanel()
    const save = [...container.querySelectorAll('button')].find(button => button.textContent?.includes('common.save'))!
    expect(save.disabled).toBe(true)
    expect(put).not.toHaveBeenCalled()
  })
})
