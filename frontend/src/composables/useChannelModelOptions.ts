import { computed, onScopeDispose, ref, watch, type ComputedRef } from 'vue'
import { api, type Channel, type TextChannelKind } from '../services/api'
import { sortModelNamesDesc } from '../utils/modelPriority'

function activeKeys(channel: Channel): string[] {
  const disabled = new Set((channel.disabledApiKeys || []).map(item => item.key))
  return [...new Set(channel.apiKeys || [])].filter(key =>
    !disabled.has(key) && channel.apiKeyConfigs?.find(config => config.key === key)?.enabled !== false
  )
}

function concreteModels(models: string[]): string[] {
  return models.map(model => model.trim()).filter(model => model && !/[!*?^${}()|[\]\\]/.test(model))
}

function supportsModel(channel: Channel, model: string): boolean {
  const rules = (channel.supportedModels || []).flatMap(rule => rule.split(/[,，、\n]/)).map(rule => rule.trim()).filter(Boolean)
  const matches = (pattern: string) => new RegExp(`^${pattern.replace(/[.+?^${}()|[\]\\]/g, '\\$&').replace(/\*/g, '.*')}$`, 'i').test(model)
  if (rules.some(rule => rule.startsWith('!') && matches(rule.slice(1)))) return false
  const includes = rules.filter(rule => !rule.startsWith('!'))
  return !includes.length || includes.some(matches)
}

export function useChannelModelOptions(type: ComputedRef<TextChannelKind>, channels: ComputedRef<Channel[]>) {
  const fetchingModels = ref(false)
  const modelsUnavailable = ref(false)
  const fetched = ref<Record<string, string[]>>({})
  const loaded = new Set<string>()
  let generation = 0
  const sources = computed(() => channels.value.filter(channel => channel.status !== 'disabled'))
  const channelId = (channel: Channel) => channel.channelUid || String(channel.index)
  const discoveryKey = computed(() => JSON.stringify(sources.value.map(channel => [
    channelId(channel), channel.index, channel.baseUrl, channel.baseUrls, channel.serviceType,
    channel.authHeader, channel.proxyUrl, channel.customHeaders, channel.insecureSkipVerify, activeKeys(channel)
  ])))

  const modelOptions = computed(() => {
    const models = new Set<string>()
    for (const channel of sources.value) {
      const keys = new Set(activeKeys(channel))
      const configured = concreteModels([
        ...Object.keys(channel.modelMapping || {}), ...Object.values(channel.modelMapping || {}),
        ...Object.keys(channel.modelCapabilities || {}), ...(channel.supportedModels || []),
        ...(channel.apiKeyConfigs || []).filter(config => keys.has(config.key)).flatMap(config => config.models || [])
      ])
      for (const model of [...configured, ...(fetched.value[channelId(channel)] || [])]) {
        if (supportsModel(channel, model)) models.add(model)
      }
    }
    return sortModelNamesDesc([...models])
  })

  watch([type, discoveryKey], () => {
    generation++
    loaded.clear()
    fetched.value = {}
    fetchingModels.value = false
    modelsUnavailable.value = false
  })
  onScopeDispose(() => { generation++ })

  async function fetchModelOptions() {
    if (fetchingModels.value) return
    const run = generation
    const protocol = type.value
    const requests = sources.value.flatMap(channel => activeKeys(channel).map(key => ({
      channel, key, cacheKey: JSON.stringify([channelId(channel), key])
    }))).filter(request => !loaded.has(request.cacheKey))
    if (!requests.length) return
    fetchingModels.value = true
    modelsUnavailable.value = false
    let successes = 0
    try {
      // 分批查询，避免打开模型菜单时同时请求所有渠道和 Key。
      for (let index = 0; index < requests.length; index += 4) {
        if (run !== generation) return
        await Promise.all(requests.slice(index, index + 4).map(async ({ channel, key, cacheKey }) => {
          try {
            const response = await api.getProtocolChannelModels(protocol, channel.index, { key })
            if (run !== generation) return
            const id = channelId(channel)
            fetched.value[id] = [...new Set([...(fetched.value[id] || []), ...response.data.map(model => model.id.trim()).filter(Boolean)])]
            loaded.add(cacheKey)
            successes++
          } catch {
            // 失败项保留为可重试状态，其他渠道的模型仍可选择。
          }
        }))
      }
      if (run === generation) modelsUnavailable.value = !successes && !modelOptions.value.length
    } finally {
      if (run === generation) fetchingModels.value = false
    }
  }

  return { modelOptions, fetchingModels, modelsUnavailable, fetchModelOptions }
}
