import { createApp, defineComponent, h } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import ChannelEditorHeader from './ChannelEditorHeader.vue'

vi.mock('@/components/ui/button', () => ({
  Button: defineComponent({
    setup(_props, { attrs, slots }) {
      return () => h('button', { ...attrs }, slots.default?.())
    },
  }),
}))

vi.mock('@/composables/useLanguage', () => ({
  useLanguage: () => ({ t: (key: string) => key }),
}))

describe('ChannelEditorHeader', () => {
  let root: HTMLDivElement
  let app: ReturnType<typeof createApp> | undefined

  beforeEach(() => {
    root = document.createElement('div')
    document.body.append(root)
  })

  afterEach(() => {
    app?.unmount()
    app = undefined
    document.body.innerHTML = ''
    vi.clearAllMocks()
  })

  function mountHeader() {
    app = createApp(defineComponent({
      setup: () => () => h(ChannelEditorHeader, {
        channelType: 'messages',
        isEditMode: true,
        noVision: false,
        saving: false,
      }),
    }))
    app.mount(root)
  }

  it('does not render the capability test entry in edit mode', () => {
    mountHeader()

    expect(root.textContent).not.toContain('capability.startTest')
    const buttons = [...root.querySelectorAll('button')]
    expect(buttons).toHaveLength(1) // 仅剩 noVision 切换按钮
    expect(buttons[0].getAttribute('title')).toContain('visionEnabled')
  })
})
