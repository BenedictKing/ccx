import { createApp, defineComponent, h, ref, type App } from 'vue'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import type { TabValue } from '@/types'
import { useWailsEvents } from './useWailsEvents'

const listeners = vi.hoisted(() => new Map<string, (event: { data: string }) => void>())
const unsubscribe = vi.hoisted(() => vi.fn())

vi.mock('@wailsio/runtime', () => ({
  Events: {
    On: (name: string, listener: (event: { data: string }) => void) => {
      listeners.set(name, listener)
      return unsubscribe
    },
  },
}))

describe('桌面导航事件', () => {
  const activeTab = ref<TabValue>('status')
  let app: App
  let root: HTMLDivElement

  beforeEach(() => {
    activeTab.value = 'status'
    listeners.clear()
    unsubscribe.mockClear()
    root = document.createElement('div')
    document.body.append(root)
    app = createApp(defineComponent({
      setup() {
        useWailsEvents(activeTab, ref(''), vi.fn().mockResolvedValue(undefined))
        return () => h('div')
      },
    }))
    app.mount(root)
  })

  afterEach(() => {
    app.unmount()
    root.remove()
    expect(unsubscribe).toHaveBeenCalledTimes(3)
  })

  it('旧 cockpit 事件转到渠道页', () => {
    listeners.get('desktop:show-tab')?.({ data: 'cockpit' })
    expect(activeTab.value).toBe('channels')
  })

  it.each<TabValue>(['channels', 'dashboard', 'agent'])('保留 %s 导航', tab => {
    listeners.get('desktop:show-tab')?.({ data: tab })
    expect(activeTab.value).toBe(tab)
  })

  it('忽略未知导航', () => {
    listeners.get('desktop:show-tab')?.({ data: 'unknown' })
    expect(activeTab.value).toBe('status')
  })
})
