import { createApp, defineComponent, h, nextTick, ref } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import ConsoleTab from './ConsoleTab.vue'

const status = ref({ running: true })
const activeTab = ref('messages')
const refreshError = ref('')

vi.mock('@/composables/useStatus', () => ({
  useStatus: () => ({ status }),
}))

vi.mock('@/composables/useLanguage', () => ({
  useLanguage: () => ({ t: (key: string) => key }),
}))

vi.mock('@/composables/useConsoleChannels', () => ({
  useConsoleChannels: () => ({ activeTab, refreshError }),
}))

vi.mock('@bindings/github.com/BenedictKing/ccx/desktop/desktopservice', () => ({
  OpenWebUIInBrowser: vi.fn().mockResolvedValue(undefined),
}))

vi.mock('@/components/console/ChannelManager.vue', () => ({
  default: defineComponent({
    props: { type: { type: String, required: true } },
    setup(props) {
      return () => h('button', { type: 'button', 'data-testid': 'channel-manager' }, `channel:${props.type}`)
    },
  }),
}))

describe('ConsoleTab', () => {
  let root: HTMLDivElement
  let errors: unknown[]

  beforeEach(() => {
    status.value = { running: true }
    activeTab.value = 'messages'
    refreshError.value = ''
    root = document.createElement('div')
    document.body.append(root)
    errors = []
    window.addEventListener('unhandledrejection', captureUnhandledRejection)
  })

  afterEach(() => {
    window.removeEventListener('unhandledrejection', captureUnhandledRejection)
    document.body.innerHTML = ''
    vi.clearAllMocks()
  })

  function captureUnhandledRejection(event: PromiseRejectionEvent) {
    errors.push(event.reason)
  }

  it('keeps management dashboard scoped to merged view tabs', async () => {
    const updates: string[] = []
    const app = createApp(ConsoleTab, {
      selection: '/channels/messages',
      'onUpdate:selection': (selection: string) => updates.push(selection),
    })
    const vueErrors: unknown[] = []
    app.config.errorHandler = error => vueErrors.push(error)

    app.mount(root)
    await nextTick()

    expect(root.querySelector('[data-testid="channel-manager"]')?.textContent).toBe('channel:messages')
    // IA 合并：旧协议子 tab 已收敛为统一 LLM 视图
    expect(findButton('OpenAI Chat', false)).toBeNull()
    expect(findButton('Codex', false)).toBeNull()

    const imagesButton = findButton('console.view.images')
    imagesButton.click()
    await nextTick()

    expect(activeTab.value).toBe('images')
    expect(updates.at(-1)).toBe('/channels/images')
    expect(root.querySelector('[data-testid="channel-manager"]')?.textContent).toBe('channel:images')
    expect(vueErrors).toEqual([])
    expect(errors).toEqual([])

    const vectorsButton = findButton('console.view.vectors')
    vectorsButton.click()
    await nextTick()

    expect(activeTab.value).toBe('vectors')
    expect(updates.at(-1)).toBe('/channels/vectors')
    expect(root.querySelector('[data-testid="channel-manager"]')?.textContent).toBe('channel:vectors')

    app.unmount()
  })

  it('normalizes legacy protocol selections into the merged LLM view', async () => {
    const updates: string[] = []
    const app = createApp(ConsoleTab, {
      selection: '/channels/chat',
      'onUpdate:selection': (selection: string) => updates.push(selection),
    })
    app.mount(root)
    await nextTick()

    // 旧 chat 子 tab 选择自动落在统一 LLM 列表
    expect(root.querySelector('[data-testid="channel-manager"]')?.textContent).toBe('channel:messages')
    app.unmount()
  })

  function findButton(text: string): HTMLButtonElement
  function findButton(text: string, required: false): HTMLButtonElement | null
  function findButton(text: string, required = true): HTMLButtonElement | null {
    const button = [...root.querySelectorAll('button')]
      .find(item => item.textContent?.trim() === text)
    if (required) expect(button).toBeTruthy()
    return (button ?? null) as HTMLButtonElement | null
  }
})
