import { onScopeDispose, watch, type Ref } from 'vue'

/**
 * 全局对话框快捷键栈（docs/specs/web-ui-dialogs.md §15 多级对话框约定）
 *
 * 背景：Vuetify v-dialog 原生 Esc 已按 overlay 栈只关最上层（VOverlay globalTop），
 * 但应用层各对话框自建的 window/document keydown 监听只判断自身可见性，
 * 多层对话框同开时一次按键会连环作用于多层（如 EditChannelModal → Key 倍率
 * 对话框按 Esc 会把两层一起关掉）。
 *
 * 约定：
 * - 对话框注册快捷键回调，可见时入栈、关闭时出栈（栈序 = 打开顺序）；
 * - 全局监听只分发给栈顶对话框，一次按键只作用于最上层；
 * - 多级对话框展开后，新对话框必须自带默认 Esc 取消 / ⌘(Ctrl)+Enter 确认。
 *
 * 与 Vuetify 原生 Esc 的分工：非 persistent 对话框的关闭交给 Vuetify 原生
 * （不注册 esc 回调即可）；persistent 对话框或需要自定义关闭语义时才注册 esc。
 */
export interface DialogHotkeyHandlers {
  /**
   * Esc：取消/关闭。返回 false 表示本次忽略（如下拉菜单展开中、提交进行中），
   * 此时事件不拦截，交由 Vuetify 原生处理。
   */
  esc?: (e: KeyboardEvent) => boolean | void
  /** ⌘/Ctrl+Enter（无 Shift）：确认/提交 */
  confirm?: (e: KeyboardEvent) => void
  /** 裸 Enter（无修饰键）：单输入框表单确认 / 引导类对话框前进下一步 */
  plainEnter?: (e: KeyboardEvent) => void
}

interface DialogHotkeyLayer {
  id: object
  isActive: () => boolean
  handlers: DialogHotkeyHandlers
}

/** 栈序 = 对话框打开顺序，栈顶即最上层 */
const layers: DialogHotkeyLayer[] = []
let listenerAttached = false

/**
 * 分发一次键盘事件到快捷键栈顶对话框（全局 window keydown 监听的目标，
 * 单测直接构造事件调用）。
 */
export function dispatchDialogHotkeyEvent(e: KeyboardEvent) {
  // 自栈顶向下找第一个仍可见的层；顺手清理已关闭但尚未出栈的层（时序兜底）
  for (let i = layers.length - 1; i >= 0; i--) {
    const layer = layers[i]
    if (!layer.isActive()) {
      layers.splice(i, 1)
      continue
    }
    dispatchToLayer(layer, e)
    return
  }
}

function dispatchToLayer(layer: DialogHotkeyLayer, e: KeyboardEvent) {
  const { esc, confirm, plainEnter } = layer.handlers
  if (e.key === 'Escape') {
    if (!esc) return
    if (esc(e) === false) return
    e.preventDefault()
    return
  }
  if (e.key !== 'Enter') return
  const plain = !e.metaKey && !e.ctrlKey && !e.altKey && !e.shiftKey
  if (plain && plainEnter) {
    plainEnter(e)
    e.preventDefault()
    return
  }
  if ((e.metaKey || e.ctrlKey) && !e.shiftKey && confirm) {
    confirm(e)
    e.preventDefault()
  }
}

function ensureListener() {
  if (listenerAttached || typeof window === 'undefined') return
  window.addEventListener('keydown', dispatchDialogHotkeyEvent)
  listenerAttached = true
}

function pushLayer(layer: DialogHotkeyLayer) {
  const existing = layers.findIndex((l) => l.id === layer.id)
  if (existing >= 0) return
  layers.push(layer)
  ensureListener()
}

function popLayer(layer: DialogHotkeyLayer) {
  const idx = layers.findIndex((l) => l.id === layer.id)
  if (idx >= 0) layers.splice(idx, 1)
}

/** 仅供单测重置模块级栈状态 */
export function clearDialogHotkeyLayers() {
  layers.splice(0, layers.length)
}

/**
 * 注册对话框快捷键：`active` 变 true 时入栈、变 false 时出栈。
 * @param active 对话框可见性（ref 或 getter）
 * @param handlers 快捷键回调；未注册的键位不拦截，交由 Vuetify 原生处理
 */
export function useDialogHotkeys(
  active: Ref<boolean> | (() => boolean),
  handlers: DialogHotkeyHandlers,
) {
  const isActive = typeof active === 'function' ? active : () => active.value
  const layer: DialogHotkeyLayer = { id: {}, isActive, handlers }

  watch(
    isActive,
    visible => {
      if (visible) pushLayer(layer)
      else popLayer(layer)
    },
    // sync：打开即入栈（push/pop 幂等），避免 watch flush 时序下「已打开但按键无效」的窗口
    { immediate: true, flush: 'sync' },
  )

  // 组件卸载 / effectScope 结束时兜底出栈
  onScopeDispose(() => popLayer(layer))
}
