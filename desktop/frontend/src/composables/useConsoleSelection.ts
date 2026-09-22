import { ref } from 'vue'
import type { ManagedChannelType } from '@/utils/channel-type-api'

export type ConsoleSelection = `/channels/${ManagedChannelType}`

const STORAGE_KEY = 'ccx-desktop-console-selection'
// 与 WebUI 顶部导航保持一致：Messages / Chat / Images / Vectors / Responses / Gemini
const CHANNEL_TYPES: ManagedChannelType[] = ['messages', 'chat', 'images', 'vectors', 'responses', 'gemini']

export const DEFAULT_CONSOLE_SELECTION: ConsoleSelection = '/channels/messages'

const selection = ref<ConsoleSelection>(loadConsoleSelection())

export function isManagedChannelType(value: string): value is ManagedChannelType {
  return CHANNEL_TYPES.includes(value as ManagedChannelType)
}

export function channelSelectionPath(type: ManagedChannelType): ConsoleSelection {
  return `/channels/${type}`
}

export function normalizeConsoleSelection(value: unknown): ConsoleSelection {
  if (typeof value !== 'string') return DEFAULT_CONSOLE_SELECTION

  const channelMatch = value.match(/^\/channels\/([^/?#]+)$/)
  if (!channelMatch) return DEFAULT_CONSOLE_SELECTION

  const channelType = channelMatch[1]
  if (!isManagedChannelType(channelType)) return DEFAULT_CONSOLE_SELECTION
  // IA 合并：旧协议子 tab（chat/responses/gemini）已并入统一 LLM 列表
  if (channelType === 'chat' || channelType === 'responses' || channelType === 'gemini') {
    return channelSelectionPath('messages')
  }
  return channelSelectionPath(channelType)
}

export function consoleSelectionChannelType(value: ConsoleSelection): ManagedChannelType {
  const channelType = value.replace('/channels/', '')
  if (!isManagedChannelType(channelType)) return 'messages'
  // IA 合并：旧协议子 tab 归一到统一 LLM 列表
  if (channelType === 'chat' || channelType === 'responses' || channelType === 'gemini') {
    return 'messages'
  }
  return channelType
}

function loadConsoleSelection(): ConsoleSelection {
  try {
    return normalizeConsoleSelection(localStorage.getItem(STORAGE_KEY))
  } catch {
    return DEFAULT_CONSOLE_SELECTION
  }
}

function persistConsoleSelection(value: ConsoleSelection) {
  try {
    localStorage.setItem(STORAGE_KEY, value)
  } catch {
    // localStorage 不可用时仅保留当前进程内状态
  }
}

export function useConsoleSelection() {
  const setConsoleSelection = (value: unknown) => {
    const normalized = normalizeConsoleSelection(value)
    selection.value = normalized
    persistConsoleSelection(normalized)
  }

  return {
    consoleSelection: selection,
    setConsoleSelection,
  }
}
