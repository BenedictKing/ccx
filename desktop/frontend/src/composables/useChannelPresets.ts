import { ref, computed } from 'vue'
import type {
  ProviderPreset,
  ProviderKeyAsset,
  CreateChannelRequest,
  CreateChannelResult,
} from '@/types'
import {
  GetProviderPresets,
  GetProviderKeyAssets,
  CreateCCXChannelFromPreset,
} from '@bindings/github.com/BenedictKing/ccx/desktop/desktopservice'

const presets = ref<ProviderPreset[]>([])
const keyAssets = ref<ProviderKeyAsset[]>([])
const loading = ref(false)
const creating = ref(false)
const error = ref('')
const result = ref<CreateChannelResult | null>(null)

const keysByProvider = computed(() => {
  const result = keyAssets.value.reduce<Record<string, ProviderKeyAsset>>((acc, item) => {
    if (item.provider && !acc[item.provider]) acc[item.provider] = item
    return acc
  }, {})
  if (result['opencode-go'] && !result['opencode-zen']) {
    result['opencode-zen'] = result['opencode-go']
  }
  return result
})

const loadChannelPresets = async (target?: string) => {
  loading.value = true
  error.value = ''
  try {
    const [nextPresets, nextAssets] = await Promise.all([
      GetProviderPresets(typeof target === 'string' ? target : '') as Promise<ProviderPreset[]>,
      GetProviderKeyAssets() as Promise<ProviderKeyAsset[]>,
    ])
    presets.value = nextPresets
    keyAssets.value = nextAssets
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
  } finally {
    loading.value = false
  }
}

const createChannel = async (request: CreateChannelRequest, options: { reloadPresets?: boolean } = {}) => {
  creating.value = true
  error.value = ''
  result.value = null
  try {
    result.value = await CreateCCXChannelFromPreset({
      provider: request.provider,
      target: request.target || '',
      planId: request.planId || '',
      baseUrl: request.baseUrl || '',
      apiKey: request.apiKey || '',
      name: request.name || '',
      description: request.description || '',
      proxyUrl: request.proxyUrl || '',
    }) as CreateChannelResult
    if (options.reloadPresets !== false) {
      await loadChannelPresets(request.target)
    }
  } catch (err) {
    error.value = err instanceof Error ? err.message : String(err)
    throw err
  } finally {
    creating.value = false
  }
}

export function useChannelPresets() {
  return {
    presets,
    keyAssets,
    keysByProvider,
    loading,
    creating,
    error,
    result,
    loadChannelPresets,
    createChannel,
  }
}
