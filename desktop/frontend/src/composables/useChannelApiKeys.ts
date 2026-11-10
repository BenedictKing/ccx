import { computed, ref, type ComputedRef, type Ref } from 'vue'
import type { Channel, DisabledKeyInfo } from '@/services/admin-api'
import type { ManagedChannelType } from '@/utils/channel-type-api'

type Translator = (key: string) => string

type ChannelApiKeyOptions = {
  channel: ComputedRef<Channel | null | undefined>
  channelType: () => ManagedChannelType
  disabledApiKeys: ComputedRef<DisabledKeyInfo[]>
  error: Ref<string>
  fallbackApiKeysText: () => string
  isEditMode: ComputedRef<boolean>
  parseLines: (text: string) => string[]
  restoreApiKey: (channelId: number, key: string, channelType: ManagedChannelType) => Promise<void>
  removeApiKey: (channelId: number, key: string, channelType: ManagedChannelType) => Promise<void>
  t: Translator
}

export function useChannelApiKeys(options: ChannelApiKeyOptions) {
  const restoringKey = ref('')
  const existingApiKeys = ref<string[]>([])
  const newApiKeysText = ref('')
  const copiedKeyIndex = ref<number | null>(null)
  const duplicateKeyIndex = ref<number | null>(null)
  const localRestoredKeys = ref<Set<string>>(new Set())
  const removingKey = ref('')
  const localRemovedKeys = ref<Set<string>>(new Set())
  let duplicateKeyTimer: ReturnType<typeof setTimeout> | null = null

  const visibleDisabledKeys = computed(() => {
    if (!options.isEditMode.value) return []
    return options.disabledApiKeys.value.filter(
      dk => !localRestoredKeys.value.has(dk.key) && !localRemovedKeys.value.has(dk.key)
    )
  })

  const hasDisabledKeys = computed(() => visibleDisabledKeys.value.length > 0)

  function findDuplicateKeyIndex(newKey: string): number {
    return existingApiKeys.value.findIndex(k => k === newKey)
  }

  function clearDuplicateKeyHighlight() {
    if (duplicateKeyTimer) {
      clearTimeout(duplicateKeyTimer)
      duplicateKeyTimer = null
    }
    duplicateKeyIndex.value = null
  }

  function setDuplicateKeyHighlight(index: number) {
    clearDuplicateKeyHighlight()
    duplicateKeyIndex.value = index
    duplicateKeyTimer = setTimeout(() => {
      duplicateKeyIndex.value = null
      duplicateKeyTimer = null
    }, 3000)
  }

  function removeExistingApiKey(index: number) {
    existingApiKeys.value.splice(index, 1)
  }

  function getSubmitApiKeys() {
    return [
      ...existingApiKeys.value,
      ...options.parseLines(newApiKeysText.value || options.fallbackApiKeysText()),
    ]
  }

  async function addNewApiKeys() {
    const lines = options.parseLines(newApiKeysText.value)
    if (lines.length === 0) return

    const uniqueLines = [...new Set(lines)]
    for (const key of uniqueLines) {
      const duplicateIndex = findDuplicateKeyIndex(key)
      if (duplicateIndex !== -1) {
        options.error.value = options.t('addChannel.duplicateKey')
        setDuplicateKeyHighlight(duplicateIndex)
        return
      }
    }

    for (const key of uniqueLines) {
      existingApiKeys.value.push(key)
    }
    clearDuplicateKeyHighlight()
    options.error.value = ''
    newApiKeysText.value = ''
  }

  async function copyApiKey(key: string, index: number) {
    try {
      await navigator.clipboard.writeText(key)
      copiedKeyIndex.value = index
      setTimeout(() => { copiedKeyIndex.value = null }, 1200)
    } catch {
      // clipboard 不可用时静默
    }
  }

  function moveApiKeyToTop(index: number) {
    if (index <= 0 || index >= existingApiKeys.value.length) return
    const [key] = existingApiKeys.value.splice(index, 1)
    existingApiKeys.value.unshift(key)
  }

  function moveApiKeyToBottom(index: number) {
    if (index < 0 || index >= existingApiKeys.value.length - 1) return
    const [key] = existingApiKeys.value.splice(index, 1)
    existingApiKeys.value.push(key)
  }

  async function handleDisabledKeyRestore(key: string) {
    const channel = options.channel.value
    if (!channel) return
    restoringKey.value = key
    options.error.value = ''
    try {
      await options.restoreApiKey(channel.index, key, options.channelType())
      localRestoredKeys.value.add(key)
      existingApiKeys.value.push(key)
    } catch (e) {
      options.error.value = e instanceof Error ? e.message : String(e)
    } finally {
      restoringKey.value = ''
    }
  }

  async function handleDisabledKeyRemove(key: string) {
    const channel = options.channel.value
    if (!channel || removingKey.value) return
    removingKey.value = key
    options.error.value = ''
    try {
      await options.removeApiKey(channel.index, key, options.channelType())
      localRemovedKeys.value = new Set([...localRemovedKeys.value, key])
      existingApiKeys.value = existingApiKeys.value.filter(item => item !== key)
    } catch (e) {
      options.error.value = e instanceof Error ? e.message : String(e)
    } finally {
      removingKey.value = ''
    }
  }

  return {
    restoringKey,
    removingKey,
    localRemovedKeys,
    existingApiKeys,
    newApiKeysText,
    copiedKeyIndex,
    duplicateKeyIndex,
    localRestoredKeys,
    visibleDisabledKeys,
    hasDisabledKeys,
    clearDuplicateKeyHighlight,
    removeExistingApiKey,
    getSubmitApiKeys,
    addNewApiKeys,
    copyApiKey,
    moveApiKeyToTop,
    moveApiKeyToBottom,
    handleDisabledKeyRestore,
    handleDisabledKeyRemove,
  }
}
