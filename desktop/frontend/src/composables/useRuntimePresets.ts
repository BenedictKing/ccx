import { computed, ref } from 'vue'

import { useAdminApi } from '@/composables/useAdminApi'
import type { UpstreamModelCapability } from '@/services/admin-api'
import { setRuntimeUpstreamModelCapabilities } from '@/utils/channel-payload'

interface RuntimeModelRegistryEntry extends UpstreamModelCapability {
  patterns?: string[]
}

interface RuntimePresetBundle {
  dataVersion?: string
  modelRegistry?: {
    upstreamCapabilities?: RuntimeModelRegistryEntry[]
  }
}

const registry = ref<Record<string, UpstreamModelCapability>>({})
const dataVersion = ref('')
const loaded = ref(false)
const loading = ref(false)
let inflight: Promise<void> | null = null

function normalizeCapabilities(bundle?: RuntimePresetBundle | null) {
  const result: Record<string, UpstreamModelCapability> = {}
  for (const entry of bundle?.modelRegistry?.upstreamCapabilities || []) {
    const capability: UpstreamModelCapability = { ...entry }
    delete (capability as UpstreamModelCapability & { patterns?: string[] }).patterns
    for (const pattern of entry.patterns || []) {
      const trimmed = pattern.trim()
      if (trimmed) result[trimmed] = capability
    }
  }
  return result
}

export async function ensureDesktopRuntimePresetsLoaded(force = false): Promise<void> {
  if (!force && loaded.value) return
  if (!force && inflight) return inflight

  loading.value = true
  const { get } = useAdminApi()
  inflight = get<RuntimePresetBundle>('/api/presets')
    .then((bundle) => {
      const next = normalizeCapabilities(bundle)
      registry.value = next
      dataVersion.value = bundle.dataVersion || ''
      setRuntimeUpstreamModelCapabilities(next)
      loaded.value = true
    })
    .finally(() => {
      loading.value = false
      inflight = null
    })
  return inflight
}

export function resetDesktopRuntimePresets() {
  registry.value = {}
  dataVersion.value = ''
  loaded.value = false
  loading.value = false
  inflight = null
  setRuntimeUpstreamModelCapabilities(null)
}

export function useRuntimePresets() {
  return {
    modelRegistry: computed(() => registry.value),
    dataVersion: computed(() => dataVersion.value),
    loaded: computed(() => loaded.value),
    loading: computed(() => loading.value),
    ensureLoaded: ensureDesktopRuntimePresetsLoaded,
  }
}
