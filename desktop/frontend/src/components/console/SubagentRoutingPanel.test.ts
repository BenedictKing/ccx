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
function channelView() {
  const channels = ['Match A', 'Hidden', 'Match B'].map((channelName, index) => ({
    channelUid: String(index), channelName, serviceType: 'responses', enabled: true,
    status: index === 0 ? 'suspended' : 'active', modelMapping: { sun: 'sun-target', sol: 'sol-target' }
  }))
  return { ...draft, channels, policy: { model: 'sol-sun', channels: channels.map(channel => ({ channelUid: channel.channelUid, enabled: true })) } }
}

async function setInput(input: HTMLInputElement, value: string) {
  input.value = value
  input.dispatchEvent(new Event('input', { bubbles: true }))
  await nextTick()
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

  it('过滤排序交换匹配项并保留隐藏渠道的位置，边界按钮同步更新', async () => {
    const view = channelView()
    get.mockResolvedValue(view)
    put.mockResolvedValue({ ...view, activated: true })
    const container = await renderPanel()
    const search = container.querySelector<HTMLInputElement>('input[placeholder="orchestration.searchPlaceholder"]')!
    await setInput(search, 'Match')
    expect(container.querySelectorAll('[draggable]')).toHaveLength(2)
    const down = container.querySelector<HTMLButtonElement>('button[title="subagentRouting.moveDown"]')!
    expect(down.disabled).toBe(false)
    down.click()
    await nextTick()
    expect([...container.querySelectorAll('[draggable]')].map(row => row.textContent)).toEqual([
      expect.stringContaining('Match B'), expect.stringContaining('Match A')
    ])
    expect(container.querySelector<HTMLButtonElement>('button[title="subagentRouting.moveUp"]')!.disabled).toBe(true)
    expect([...container.querySelectorAll<HTMLButtonElement>('button[title="subagentRouting.moveDown"]')][1].disabled).toBe(true)
    const save = [...container.querySelectorAll('button')].find(button => button.textContent?.includes('common.save'))!
    save.click()
    await Promise.resolve()
    await nextTick()
    expect(put).toHaveBeenCalledWith('/api/responses/channels/subagent-routing', {
      revision: 'r1', policy: { model: 'sol-sun', channels: [
        { channelUid: '2', enabled: true }, { channelUid: '1', enabled: true }, { channelUid: '0', enabled: true }
      ] }
    })
  })

  it('慢速保存期间锁定模型、排序、开关、拖动和重新加载，失败后保留草稿', async () => {
    get.mockResolvedValue(channelView())
    let rejectSave!: (error: Error) => void
    put.mockImplementation(() => new Promise((_, reject) => { rejectSave = reject }))
    const container = await renderPanel()
    const input = container.querySelector<HTMLInputElement>('#subagent-model-responses')!
    await setInput(input, 'luna')
    const save = [...container.querySelectorAll('button')].find(button => button.textContent?.includes('common.save'))!
    save.click()
    await nextTick()
    expect(input.disabled).toBe(true)
    expect([...container.querySelectorAll('button')].every(button => button.disabled)).toBe(true)
    expect([...container.querySelectorAll('[draggable]')].every(row => row.getAttribute('draggable') === 'false')).toBe(true)
    rejectSave(new Error('offline'))
    await Promise.resolve()
    await nextTick()
    expect(input.disabled).toBe(false)
    expect(input.value).toBe('luna')
    expect(container.textContent).toContain('offline')
  })

  it('暂停状态复用现有翻译，模糊映射预览不依赖键顺序', async () => {
    get.mockResolvedValue(channelView())
    const container = await renderPanel()
    expect(container.textContent).toContain('status.paused')
    expect(container.textContent).not.toContain('status.suspended')
    expect(container.querySelector('[draggable]')!.textContent).toContain('sol-target')
  })
})
