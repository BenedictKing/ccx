// @vitest-environment jsdom
import { createApp, nextTick } from 'vue'
import { afterEach, describe, expect, it, vi } from 'vitest'

import ModelCapabilityPanel from './ModelCapabilityPanel.vue'

vi.mock('@/composables/useLanguage', () => ({
  useLanguage: () => ({
    t: (key: string) => key,
  }),
}))

describe('ModelCapabilityPanel', () => {
  let root: HTMLDivElement | undefined
  let app: ReturnType<typeof createApp> | undefined

  afterEach(() => {
    app?.unmount()
    root?.remove()
    app = undefined
    root = undefined
  })

  it('打开新增实际模型下拉时提升整个面板层级', async () => {
    root = document.createElement('div')
    document.body.appendChild(root)
    app = createApp(ModelCapabilityPanel, {
      rows: [],
      targetModels: ['gpt-5.5-openai-compact', 'gpt-5.5'],
      fetchingModels: false,
      fetchModelsError: '',
      error: '',
    })
    app.mount(root)

    const input = root.querySelector('input') as HTMLInputElement | null
    expect(input).toBeTruthy()
    input?.dispatchEvent(new FocusEvent('focus'))
    await nextTick()

    const panel = root.querySelector('section')
    expect(panel?.className).toContain('relative')
    expect(panel?.className).toContain('z-50')
  })
})
