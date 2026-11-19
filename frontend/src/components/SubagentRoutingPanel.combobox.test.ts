// @vitest-environment jsdom
import { createApp, h, nextTick } from 'vue'
import { afterEach, expect, it, vi } from 'vitest'
import { createVuetify } from 'vuetify'
import * as components from 'vuetify/components'
import SubagentRoutingPanel from './SubagentRoutingPanel.vue'

const { get, update } = vi.hoisted(() => ({ get: vi.fn(), update: vi.fn() }))
vi.mock('../i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('vue-router', () => ({ onBeforeRouteLeave: vi.fn(), onBeforeRouteUpdate: vi.fn() }))
vi.mock('../services/api', () => ({
  ApiError: class extends Error {},
  api: { getSubagentRouting: get, updateSubagentRouting: update }
}))

let cleanup: (() => void) | undefined
afterEach(() => {
  cleanup?.()
  vi.unstubAllGlobals()
})
it('真实模型下拉框选中 sol 后保存字符串模型值', async () => {
  vi.stubGlobal(
    'ResizeObserver',
    class {
      observe() {}
      unobserve() {}
      disconnect() {}
    }
  )
  vi.stubGlobal(
    'IntersectionObserver',
    class {
      observe() {}
      unobserve() {}
      disconnect() {}
    }
  )
  vi.stubGlobal('matchMedia', () => ({ matches: false, addEventListener() {}, removeEventListener() {} }))
  vi.stubGlobal('visualViewport', {
    width: 1280,
    height: 800,
    offsetLeft: 0,
    offsetTop: 0,
    addEventListener() {},
    removeEventListener() {}
  })
  const view = {
    activated: true,
    revision: 'r1',
    legacyMixed: false,
    policy: { model: '', channels: [] },
    channels: []
  }
  get.mockResolvedValue(view)
  update.mockResolvedValue({ ...view, policy: { ...view.policy, model: 'sol' } })
  const container = document.createElement('div')
  document.body.append(container)
  const app = createApp({ render: () => h(SubagentRoutingPanel, { channelType: 'responses' }) })
  app.use(createVuetify({ components })).mount(container)
  cleanup = () => {
    app.unmount()
    container.remove()
  }
  await Promise.resolve()
  await nextTick()
  const input = container.querySelector('input')!
  input.focus()
  input.dispatchEvent(new KeyboardEvent('keydown', { key: 'ArrowDown', bubbles: true }))
  await nextTick()
  const option = [...document.querySelectorAll('.v-list-item')].find(
    item => item.textContent?.trim() === 'sol'
  ) as HTMLElement
  expect(option).toBeTruthy()
  option.click()
  await nextTick()
  const save = [...container.querySelectorAll('button')].find(
    button => button.textContent?.trim() === 'app.actions.save'
  )!
  save.click()
  await Promise.resolve()
  await nextTick()
  expect(update).toHaveBeenCalledWith('responses', 'r1', { model: 'sol', channels: [] })
})
