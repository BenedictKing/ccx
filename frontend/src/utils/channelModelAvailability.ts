import type {
  ApiService,
  Channel,
  ChannelKind,
  ChannelModelsRequest,
  ChannelProtocolRoute,
  ChannelsResponse,
  ManagedAccountChannel,
  ModelsResponse,
} from '@/services/api'
import { maskApiKey } from './apiKeyMask'

const NATIVE_KIND_BY_SERVICE_TYPE: Record<string, ChannelKind> = {
  claude: 'messages',
  openai: 'chat',
  chat: 'chat',
  responses: 'responses',
  gemini: 'gemini',
  copilot: 'responses',
}

const DISPLAY_ORDER: ChannelKind[] = ['messages', 'chat', 'responses', 'gemini', 'images', 'vectors']

const SERVICE_TYPE_BY_KIND: Record<ChannelKind, string> = {
  messages: 'claude',
  chat: 'openai',
  responses: 'responses',
  gemini: 'gemini',
  images: 'openai',
  vectors: 'openai',
}

type ManagedModelsApi = Pick<ApiService,
  | 'getChannels'
  | 'getChatChannels'
  | 'getResponsesChannels'
  | 'getGeminiChannels'
  | 'getImagesChannels'
  | 'getVectorsChannels'
  | 'getChannelModels'
  | 'getChatChannelModels'
  | 'getResponsesChannelModels'
  | 'getGeminiChannelModels'
  | 'getImagesChannelModels'
  | 'getVectorsChannelModels'
>

const nativeKindForRoute = (route: ChannelProtocolRoute): ChannelKind => {
  if (route.kind === 'images' || route.kind === 'vectors') return route.kind
  return NATIVE_KIND_BY_SERVICE_TYPE[route.serviceType.trim().toLowerCase()] ?? route.kind
}

const getChannelsForKind = (api: ManagedModelsApi, kind: ChannelKind): Promise<ChannelsResponse> => {
  switch (kind) {
    case 'messages': return api.getChannels()
    case 'chat': return api.getChatChannels()
    case 'responses': return api.getResponsesChannels()
    case 'gemini': return api.getGeminiChannels()
    case 'images': return api.getImagesChannels()
    case 'vectors': return api.getVectorsChannels()
  }
}

const getModelsForKind = (
  api: ManagedModelsApi,
  kind: ChannelKind,
  channelId: number,
  request: ChannelModelsRequest,
): Promise<ModelsResponse> => {
  switch (kind) {
    case 'messages': return api.getChannelModels(channelId, request)
    case 'chat': return api.getChatChannelModels(channelId, request)
    case 'responses': return api.getResponsesChannelModels(channelId, request)
    case 'gemini': return api.getGeminiChannelModels(channelId, request)
    case 'images': return api.getImagesChannelModels(channelId, request)
    case 'vectors': return api.getVectorsChannelModels(channelId, request)
  }
}

const hasModelInventory = (route: ChannelProtocolRoute): boolean => (
  route.modelInventoryKnown === true
  || Array.isArray(route.discoveredModels)
  || Array.isArray(route.modelBindings)
)

const normalizeModels = (models: string[]): string[] => Array.from(new Set(
  models.map(model => model.trim()).filter(Boolean),
)).sort((left, right) => left.localeCompare(right))

const channelForRoute = (response: ChannelsResponse, route: ChannelProtocolRoute): Channel | undefined => {
  if (route.channelUid) {
    const matched = response.channels.find(channel => channel.channelUid === route.channelUid)
    if (matched) return matched
  }
  return response.channels[route.index]
}

const modelRequestForKey = (channel: Channel, key: string): ChannelModelsRequest => {
  const keyConfig = channel.apiKeyConfigs?.find(config => config.key === key)
  return {
    key,
    baseUrl: keyConfig?.baseUrl || undefined,
    serviceType: channel.serviceType,
    proxyUrl: channel.proxyUrl || undefined,
    insecureSkipVerify: channel.insecureSkipVerify || undefined,
    customHeaders: channel.customHeaders,
    authHeader: channel.authHeader,
  }
}

/**
 * 兼容尚未在 /api/accounts 返回模型画像的旧后端：按原生协议和 Key 调用既有 models API。
 * 新后端已有画像时直接跳过，避免重复请求上游。
 */
export async function loadLegacyManagedModelAvailability(
  api: ManagedModelsApi,
  routes: ChannelProtocolRoute[] | undefined,
  accountChannels: ManagedAccountChannel[] | undefined,
): Promise<ManagedAccountChannel[]> {
  const existingChannels = accountChannels ?? []
  const nativeRoutes = buildNativeProtocolModelRoutes(routes, existingChannels)
  const missingRoutes = nativeRoutes.filter(route => route.channelUid && !hasModelInventory(route))
  if (missingRoutes.length === 0) return existingChannels

  const kinds = Array.from(new Set(missingRoutes.map(route => route.kind)))
  const channelResponses = new Map<ChannelKind, ChannelsResponse>()
  await Promise.all(kinds.map(async (kind) => {
    try {
      channelResponses.set(kind, await getChannelsForKind(api, kind))
    } catch {
      // 单个协议列表失败不影响其他协议的模型展示。
    }
  }))

  const fallbackChannels = await Promise.all(missingRoutes.map(async (route) => {
    const response = channelResponses.get(route.kind)
    const channel = response ? channelForRoute(response, route) : undefined
    if (!channel || !route.channelUid) return null

    const enabledKeys = channel.apiKeys.filter((key) => {
      const config = channel.apiKeyConfigs?.find(candidate => candidate.key === key)
      return config?.enabled !== false
    })
    // 暂停 Key 只退出调度，不代表凭证失效；全部 Key 暂停时仍用它们探测模型，
    // 否则渠道会退化成"尚无自动发现结果"，无法恢复。
    const probeKeys = enabledKeys.length > 0 ? enabledKeys : channel.apiKeys
    const bindings = (await Promise.all(probeKeys.map(async (key) => {
      try {
        const modelsResponse = await getModelsForKind(
          api,
          route.kind,
          route.index,
          modelRequestForKey(channel, key),
        )
        const keyConfig = channel.apiKeyConfigs?.find(config => config.key === key)
        return {
          credentialUid: keyConfig?.credentialUid,
          keyMask: maskApiKey(key),
          models: normalizeModels((modelsResponse.data ?? []).map(model => model.id)),
        }
      } catch {
        return null
      }
    }))).filter(binding => binding !== null)

    if (bindings.length === 0) return null
    return {
      kind: route.kind,
      channelUid: route.channelUid,
      name: route.name,
      serviceType: route.serviceType,
      status: channel.status || 'active',
      modelInventoryKnown: true,
      discoveredModels: normalizeModels(bindings.flatMap(binding => binding.models)),
      modelBindings: bindings,
    } satisfies ManagedAccountChannel
  }))

  const fallbackByUID = new Map(fallbackChannels
    .filter(channel => channel !== null)
    .map(channel => [channel.channelUid, channel]))
  const merged = existingChannels.map(channel => ({
    ...channel,
    ...(fallbackByUID.get(channel.channelUid) ?? {}),
  }))
  const existingUIDs = new Set(existingChannels.map(channel => channel.channelUid))
  for (const channel of fallbackByUID.values()) {
    if (!existingUIDs.has(channel.channelUid)) merged.push(channel)
  }
  return merged
}

/**
 * 将客户端入站路由折叠为上游原生协议，并附加 endpoint profile 的实际模型清单。
 * 保留 kind 作为 CCX 实际入站路由，upstreamKind 只供上游协议展示使用；
 * 同一上游协议存在多条转换路由时，优先保留 kind 与原生协议一致的渠道。
 */
export function buildNativeProtocolModelRoutes(
  routes: ChannelProtocolRoute[] | undefined,
  accountChannels: ManagedAccountChannel[] | undefined,
): ChannelProtocolRoute[] {
  const availabilityByChannel = new Map(
    (accountChannels ?? []).map(channel => [channel.channelUid, channel]),
  )
  const selected = new Map<ChannelKind, { route: ChannelProtocolRoute; native: boolean }>()

  for (const route of routes ?? []) {
    const upstreamKind = nativeKindForRoute(route)
    const native = route.kind === upstreamKind
    const existing = selected.get(upstreamKind)
    if (existing && (existing.native || !native)) continue

    const channelAvailability = route.channelUid ? availabilityByChannel.get(route.channelUid) : undefined
    const protocolAvailability = channelAvailability?.protocolAvailability?.find(
      availability => availability.protocol === upstreamKind,
    )
    const availability = protocolAvailability ?? channelAvailability
    selected.set(upstreamKind, {
      native,
      route: {
        ...route,
        upstreamKind,
        configured: true,
        modelInventoryKnown: availability?.modelInventoryKnown,
        discoveredModels: availability?.discoveredModels,
        modelBindings: availability?.modelBindings,
        modelsUpdatedAt: protocolAvailability?.modelsDiscoveredAt ?? channelAvailability?.modelsUpdatedAt,
        modelsDiscoveredAt: availability?.modelsDiscoveredAt,
        modelDiscoverySource: availability?.modelDiscoverySource,
        modelDiscoveryMessage: availability?.modelDiscoveryMessage,
      },
    })
  }

  for (const channel of accountChannels ?? []) {
    for (const availability of channel.protocolAvailability ?? []) {
      if (selected.has(availability.protocol)) continue
      selected.set(availability.protocol, {
        native: false,
        route: {
          kind: availability.protocol,
          upstreamKind: availability.protocol,
          index: -1,
          name: channel.name,
          serviceType: SERVICE_TYPE_BY_KIND[availability.protocol],
          channelUid: channel.channelUid,
          configured: false,
          modelInventoryKnown: availability.modelInventoryKnown,
          discoveredModels: availability.discoveredModels,
          modelBindings: availability.modelBindings,
          modelsDiscoveredAt: availability.modelsDiscoveredAt,
          modelDiscoverySource: availability.modelDiscoverySource,
          modelDiscoveryMessage: availability.modelDiscoveryMessage,
        },
      })
    }
  }

  return DISPLAY_ORDER.flatMap(kind => {
    const item = selected.get(kind)
    return item ? [item.route] : []
  })
}
