import { computed, ref } from 'vue'
import type { TabValue } from '@/types'
import {
  DEFAULT_CONSOLE_SELECTION,
  type ConsoleSelection,
} from '@/composables/useConsoleSelection'

const windowVisible = ref(true)
const activeTab = ref<TabValue>('status')
const consoleSelection = ref<ConsoleSelection>(DEFAULT_CONSOLE_SELECTION)

export function setDesktopWindowVisible(visible: boolean) {
  windowVisible.value = visible
}

export function setDesktopActiveTab(tab: TabValue) {
  activeTab.value = tab
}

export function setDesktopConsoleSelection(selection: ConsoleSelection) {
  consoleSelection.value = selection
}

export function useDesktopActivity() {
  const isChannelPageActive = computed(() => windowVisible.value && activeTab.value === 'channels')
  const isDashboardActive = computed(() => windowVisible.value && activeTab.value === 'dashboard')
  const isStatusActive = computed(() => windowVisible.value && activeTab.value === 'status')
  const isConsoleChannelsActive = computed(() => isDashboardActive.value)

  return {
    windowVisible,
    activeTab,
    consoleSelection,
    isChannelPageActive,
    isStatusActive,
    isConsoleChannelsActive,
  }
}
