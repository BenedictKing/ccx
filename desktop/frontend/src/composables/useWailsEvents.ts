import { onMounted, onBeforeUnmount, type Ref } from 'vue'
import { Events } from '@wailsio/runtime'
import type { TabValue } from '@/types'
import { setDesktopWindowVisible } from '@/composables/useDesktopActivity'

export function useWailsEvents(
  activeTab: Ref<TabValue>,
  actionError: Ref<string>,
  syncStatus: () => Promise<void>,
) {
  let unsubscribeTab: (() => void) | undefined
  let unsubscribeTrayError: (() => void) | undefined
  let unsubscribeWindowVisibility: (() => void) | undefined

  onMounted(() => {
    unsubscribeTab = Events.On('desktop:show-tab', (event: { data: string }) => {
      const tab = event.data === 'cockpit' ? 'channels' : event.data
      const validTabs: TabValue[] = ['status', 'agent', 'channels', 'subscriptions', 'env', 'dashboard']
      if (validTabs.includes(tab as TabValue)) {
        activeTab.value = tab as TabValue
      }
    })
    unsubscribeTrayError = Events.On('desktop:tray-error', (event: { data: string }) => {
      actionError.value = event.data
      void syncStatus()
    })
    unsubscribeWindowVisibility = Events.On('desktop:window-visibility', (event: { data: boolean }) => {
      setDesktopWindowVisible(event.data !== false)
    })
  })

  onBeforeUnmount(() => {
    unsubscribeTab?.()
    unsubscribeTrayError?.()
    unsubscribeWindowVisibility?.()
  })
}
