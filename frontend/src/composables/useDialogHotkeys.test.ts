import { beforeEach, describe, expect, it, vi } from 'vitest'
import { ref } from 'vue'
import {
  clearDialogHotkeyLayers,
  dispatchDialogHotkeyEvent,
  useDialogHotkeys,
} from './useDialogHotkeys'

interface FakeKeyEvent {
  key: string
  metaKey?: boolean
  ctrlKey?: boolean
  altKey?: boolean
  shiftKey?: boolean
}

function keyEvent(init: FakeKeyEvent): KeyboardEvent {
  return {
    preventDefault: vi.fn(),
    ...init,
  } as unknown as KeyboardEvent
}

function wasPrevented(e: KeyboardEvent): boolean {
  return (e.preventDefault as ReturnType<typeof vi.fn>).mock.calls.length > 0
}

beforeEach(() => {
  clearDialogHotkeyLayers()
})

describe('useDialogHotkeys 全局对话框快捷键栈', () => {
  it('多层同开时 Esc 只作用于栈顶，底层不响应', () => {
    const baseOpen = ref(false)
    const topOpen = ref(false)
    const baseEsc = vi.fn()
    const topEsc = vi.fn()
    useDialogHotkeys(baseOpen, { esc: baseEsc })
    useDialogHotkeys(topOpen, { esc: topEsc })

    baseOpen.value = true
    topOpen.value = true

    const e = keyEvent({ key: 'Escape' })
    dispatchDialogHotkeyEvent(e)

    expect(topEsc).toHaveBeenCalledTimes(1)
    expect(baseEsc).not.toHaveBeenCalled()
    expect(wasPrevented(e)).toBe(true)
  })

  it('上层关闭后（watch 未 flush 前）Esc 作用于底层：惰性清理兜底', () => {
    const baseOpen = ref(false)
    const topOpen = ref(false)
    const baseEsc = vi.fn()
    const topEsc = vi.fn()
    useDialogHotkeys(baseOpen, { esc: baseEsc })
    useDialogHotkeys(topOpen, { esc: topEsc })

    baseOpen.value = true
    topOpen.value = true
    // 同步置 false，popLayer 的 watch 回调尚未执行
    topOpen.value = false

    const e = keyEvent({ key: 'Escape' })
    dispatchDialogHotkeyEvent(e)

    expect(topEsc).not.toHaveBeenCalled()
    expect(baseEsc).toHaveBeenCalledTimes(1)
  })

  it('栈顶 esc 返回 false 时不拦截事件（交还 Vuetify 原生处理）', () => {
    const open = ref(false)
    const esc = vi.fn(() => false)
    useDialogHotkeys(open, { esc })
    open.value = true

    const e = keyEvent({ key: 'Escape' })
    dispatchDialogHotkeyEvent(e)

    expect(esc).toHaveBeenCalledTimes(1)
    expect(wasPrevented(e)).toBe(false)
  })

  it('栈顶层未注册 esc 时不拦截事件，也不穿透到下层（该层关闭由 Vuetify 原生负责）', () => {
    const baseOpen = ref(false)
    const topOpen = ref(false)
    const baseEsc = vi.fn()
    useDialogHotkeys(baseOpen, { esc: baseEsc })
    useDialogHotkeys(topOpen, { confirm: () => {} }) // 顶层只有 confirm
    baseOpen.value = true
    topOpen.value = true

    const e = keyEvent({ key: 'Escape' })
    dispatchDialogHotkeyEvent(e)

    expect(baseEsc).not.toHaveBeenCalled()
    expect(wasPrevented(e)).toBe(false)
  })

  it('⌘/Ctrl+Enter 触发栈顶 confirm，Shift+Enter 不触发', () => {
    const baseOpen = ref(false)
    const topOpen = ref(false)
    const baseConfirm = vi.fn()
    const topConfirm = vi.fn()
    useDialogHotkeys(baseOpen, { confirm: baseConfirm })
    useDialogHotkeys(topOpen, { confirm: topConfirm })
    baseOpen.value = true
    topOpen.value = true

    const cmdEnter = keyEvent({ key: 'Enter', metaKey: true })
    dispatchDialogHotkeyEvent(cmdEnter)
    expect(topConfirm).toHaveBeenCalledTimes(1)
    expect(baseConfirm).not.toHaveBeenCalled()
    expect(wasPrevented(cmdEnter)).toBe(true)

    const ctrlEnter = keyEvent({ key: 'Enter', ctrlKey: true })
    dispatchDialogHotkeyEvent(ctrlEnter)
    expect(topConfirm).toHaveBeenCalledTimes(2)

    const shiftCmdEnter = keyEvent({ key: 'Enter', metaKey: true, shiftKey: true })
    dispatchDialogHotkeyEvent(shiftCmdEnter)
    expect(topConfirm).toHaveBeenCalledTimes(2)
    expect(wasPrevented(shiftCmdEnter)).toBe(false)
  })

  it('裸 Enter 触发 plainEnter 而非 confirm；Alt+Enter 均不触发', () => {
    const open = ref(false)
    const confirm = vi.fn()
    const plainEnter = vi.fn()
    useDialogHotkeys(open, { confirm, plainEnter })
    open.value = true

    dispatchDialogHotkeyEvent(keyEvent({ key: 'Enter' }))
    expect(plainEnter).toHaveBeenCalledTimes(1)
    expect(confirm).not.toHaveBeenCalled()

    dispatchDialogHotkeyEvent(keyEvent({ key: 'Enter', altKey: true }))
    expect(plainEnter).toHaveBeenCalledTimes(1)

    dispatchDialogHotkeyEvent(keyEvent({ key: 'a' }))
    expect(plainEnter).toHaveBeenCalledTimes(1)
    expect(confirm).not.toHaveBeenCalled()
  })

  it('不可见对话框不入栈：仅底层打开时按键不触发未打开层的回调', () => {
    const baseOpen = ref(false)
    const topOpen = ref(false)
    const baseEsc = vi.fn()
    const topEsc = vi.fn()
    useDialogHotkeys(baseOpen, { esc: baseEsc })
    useDialogHotkeys(topOpen, { esc: topEsc })

    baseOpen.value = true
    dispatchDialogHotkeyEvent(keyEvent({ key: 'Escape' }))

    expect(baseEsc).toHaveBeenCalledTimes(1)
    expect(topEsc).not.toHaveBeenCalled()
  })
})
