import type {
  ActivitySegment,
  Channel,
  ChannelKind,
  ChannelMetrics,
  ChannelProtocolCapsule,
  ChannelProtocolRoute,
  ChannelRecentActivity,
  ChannelsResponse,
} from '@/services/api'

export type LlmChannelKind = 'messages' | 'chat' | 'responses' | 'gemini'

export const LLM_CHANNEL_KINDS: LlmChannelKind[] = ['messages', 'chat', 'responses', 'gemini']

const PROTOCOL_LABELS: Record<LlmChannelKind, string> = {
  messages: 'Claude',
  chat: 'Chat',
  responses: 'Codex',
  gemini: 'Gemini',
}

const PROVIDER_ROUTE_SUFFIXES: Record<LlmChannelKind, RegExp> = {
  messages: /-claude$/i,
  chat: /-chat$/i,
  responses: /-codex$/i,
  gemini: /-gemini$/i,
}

const PRIMARY_KIND_ORDER: LlmChannelKind[] = ['messages', 'chat', 'responses', 'gemini']

const UPSTREAM_PROTOCOLS: Record<Channel['serviceType'], { kind: LlmChannelKind; label: string }> = {
  claude: { kind: 'messages', label: 'CLAUDE' },
  openai: { kind: 'chat', label: 'CHAT' },
  responses: { kind: 'responses', label: 'CODEX' },
  gemini: { kind: 'gemini', label: 'GEMINI' },
  copilot: { kind: 'chat', label: 'COPILOT' },
}

type RoutedChannel = Channel & {
  routeKind: ChannelKind
  routeIndex: number
  displayKey: string
}

type ChannelGroup = {
  key: string
  logicalName: string
  channels: Partial<Record<LlmChannelKind, RoutedChannel>>
}

export const isLlmChannelKind = (kind: string): kind is LlmChannelKind => {
  return kind === 'messages' || kind === 'chat' || kind === 'responses' || kind === 'gemini'
}

export const protocolLabelForKind = (kind: ChannelKind): string => {
  return isLlmChannelKind(kind) ? PROTOCOL_LABELS[kind] : kind
}

export type ChannelRecoveryRoute = {
  kind: ChannelKind
  index: number
  status?: Channel['status']
}

export const resolveChannelRecoveryRoutes = (channel: Channel): ChannelRecoveryRoute[] => {
  if (channel.protocolRoutes?.length) {
    return channel.protocolRoutes.map(route => ({
      kind: route.kind,
      index: route.index,
      status: route.status,
    }))
  }
  return [{
    kind: channel.routeKind ?? 'messages',
    index: channel.routeIndex ?? channel.index,
    status: channel.status,
  }]
}

const stripRouteSuffix = (name: string, kind: LlmChannelKind): string => {
  return name.replace(PROVIDER_ROUTE_SUFFIXES[kind], '')
}

const apiKeyFingerprint = (channel: Channel): string => {
  const keys = channel.apiKeys ?? []
  if (!keys.length) return ''
  return keys.map(key => `${key.slice(0, 8)}:${key.slice(-6)}`).sort().join('|')
}

const logicalGroupKey = (kind: LlmChannelKind, channel: Channel): { key: string; name: string } => {
  const accountManaged = !!channel.accountUid && (!!channel.autoManaged || !!channel.providerId)
  const name = accountManaged || (channel.autoManaged && channel.providerId)
    ? stripRouteSuffix(channel.name, kind)
    : channel.name

  if (accountManaged) {
    return {
      key: `account:${channel.accountUid}`,
      name,
    }
  }

  if (!channel.autoManaged || !channel.providerId) {
    return {
      key: `${kind}:${channel.index}:${channel.channelUid || channel.name}`,
      name,
    }
  }

  return {
    key: ['provider', channel.providerId, name, apiKeyFingerprint(channel)].join(':'),
    name,
  }
}

const annotateChannel = (kind: LlmChannelKind, channel: Channel): RoutedChannel => ({
  ...channel,
  routeKind: kind,
  routeIndex: channel.index,
  displayKey: `${kind}:${channel.index}:${channel.channelUid || channel.name}`,
})

const selectPrimary = (channels: Partial<Record<LlmChannelKind, RoutedChannel>>): RoutedChannel => {
  for (const kind of PRIMARY_KIND_ORDER) {
    const channel = channels[kind]
    if (channel) return channel
  }
  return Object.values(channels)[0] as RoutedChannel
}

const buildProtocolCapsules = (channels: Partial<Record<LlmChannelKind, RoutedChannel>>): ChannelProtocolCapsule[] => {
  const seenServiceTypes = new Set<string>()
  return PRIMARY_KIND_ORDER.flatMap(kind => {
    const channel = channels[kind]
    if (!channel) return []
    const serviceType = channel.serviceType
    if (seenServiceTypes.has(serviceType)) return []
    seenServiceTypes.add(serviceType)
    const protocol = UPSTREAM_PROTOCOLS[serviceType]
    return [{
      kind: protocol.kind,
      label: protocol.label,
      serviceType,
      channelUid: channel.channelUid,
      index: channel.routeIndex,
      status: channel.status,
    }]
  })
}

const buildProtocolRoutes = (channels: Partial<Record<LlmChannelKind, RoutedChannel>>): ChannelProtocolRoute[] => {
  return PRIMARY_KIND_ORDER.flatMap(kind => {
    const channel = channels[kind]
    if (!channel) return []
    return [{
      kind,
      index: channel.routeIndex,
      name: channel.name,
      serviceType: channel.serviceType,
      channelUid: channel.channelUid,
      status: channel.status,
      apiKeys: [...(channel.apiKeys ?? [])],
      supportedModels: channel.supportedModels == null ? undefined : [...channel.supportedModels],
    }]
  })
}

const getGroupPriority = (channels: Partial<Record<LlmChannelKind, RoutedChannel>>): number => {
  return Math.min(...Object.values(channels).map(channel => channel.priority ?? channel.routeIndex))
}

const mergeAccountCredentials = (channels: Partial<Record<LlmChannelKind, RoutedChannel>>) => {
  const apiKeys = Array.from(new Set(
    Object.values(channels).flatMap(channel => channel.apiKeys ?? []),
  ))
  const configsByKey = new Map(
    Object.values(channels)
      .flatMap(channel => channel.apiKeyConfigs ?? [])
      .map(config => [config.key, config]),
  )
  const disabledByKey = new Map(
    Object.values(channels)
      .flatMap(channel => channel.disabledApiKeys ?? [])
      .map(item => [item.key, item]),
  )
  return {
    apiKeys,
    apiKeyConfigs: configsByKey.size > 0 ? Array.from(configsByKey.values()) : undefined,
    disabledApiKeys: disabledByKey.size > 0 ? Array.from(disabledByKey.values()) : undefined,
  }
}

const resolveGroupStatus = (channels: Partial<Record<LlmChannelKind, RoutedChannel>>): Channel['status'] => {
  const statuses = Object.values(channels).map(channel => channel.status)
  if (statuses.some(status => status === 'active' || status === 'healthy' || !status)) return 'active'
  if (statuses.some(status => status === 'suspended')) return 'suspended'
  return statuses[0]
}

const buildDisplayChannel = (group: ChannelGroup): Channel => {
  const primary = selectPrimary(group.channels)
  const credentials = mergeAccountCredentials(group.channels)

  return {
    ...primary,
    ...credentials,
    index: primary.routeIndex,
    name: group.logicalName,
    logicalName: group.logicalName,
    routeKind: primary.routeKind,
    routeIndex: primary.routeIndex,
    displayKey: `logical:${group.key}`,
    priority: getGroupPriority(group.channels),
    status: resolveGroupStatus(group.channels),
    protocolCapsules: buildProtocolCapsules(group.channels),
    protocolRoutes: buildProtocolRoutes(group.channels),
  }
}

export const buildUnifiedChannelsData = (
  dataByKind: Record<LlmChannelKind, ChannelsResponse>
): ChannelsResponse => {
  const groups = new Map<string, ChannelGroup>()

  for (const kind of LLM_CHANNEL_KINDS) {
    for (const channel of dataByKind[kind].channels ?? []) {
      const routed = annotateChannel(kind, channel)
      const { key, name } = logicalGroupKey(kind, channel)
      const group = groups.get(key) ?? { key, logicalName: name, channels: {} }
      group.channels[kind] = routed
      groups.set(key, group)
    }
  }

  const channels = Array.from(groups.values()).map(buildDisplayChannel)
  return {
    channels,
    current: channels[0]?.index ?? -1,
  }
}

export const withRouteKindMetrics = (
  kind: LlmChannelKind,
  metrics: ChannelMetrics[]
): ChannelMetrics[] => metrics.map(metric => ({ ...metric, routeKind: kind }))

export const buildUnifiedRecentActivity = (
  channels: Channel[],
  activityByKind: Record<LlmChannelKind, ChannelRecentActivity[] | undefined>,
): ChannelRecentActivity[] => {
  const activityLookup = new Map<string, ChannelRecentActivity>()
  for (const kind of LLM_CHANNEL_KINDS) {
    for (const activity of activityByKind[kind] ?? []) {
      activityLookup.set(`${kind}:${activity.channelIndex}`, activity)
    }
  }

  return channels.map(channel => {
    const segments: Record<number, ActivitySegment> = {}
    const seenRoutes = new Set<string>()
    let totalSegs = 0
    let rpm = 0
    let tpm = 0

    for (const route of channel.protocolRoutes ?? []) {
      if (!isLlmChannelKind(route.kind)) continue
      const routeKey = `${route.kind}:${route.index}`
      if (seenRoutes.has(routeKey)) continue
      seenRoutes.add(routeKey)

      const activity = activityLookup.get(routeKey)
      if (!activity) continue
      totalSegs = Math.max(totalSegs, activity.totalSegs ?? 0)
      rpm += activity.rpm ?? 0
      tpm += activity.tpm ?? 0

      for (const [rawIndex, source] of Object.entries(activity.segments ?? {})) {
        if (!source) continue
        const index = Number(rawIndex)
        const target = segments[index] ?? {
          requestCount: 0,
          successCount: 0,
          failureCount: 0,
          inputTokens: 0,
          outputTokens: 0,
        }
        target.requestCount += source.requestCount ?? 0
        target.successCount += source.successCount ?? 0
        target.failureCount += source.failureCount ?? 0
        target.inputTokens += source.inputTokens ?? 0
        target.outputTokens += source.outputTokens ?? 0
        segments[index] = target
      }
    }

    return {
      channelIndex: channel.routeIndex ?? channel.index,
      routeKind: isLlmChannelKind(channel.routeKind ?? '') ? channel.routeKind : 'messages',
      segments,
      totalSegs,
      rpm,
      tpm,
    }
  })
}

export interface UnifiedReorderPayload {
  order: number[]
  priorities: number[]
}

/**
 * 统一 LLM 视图的排序提交载荷：按协议类型拆分 order，
 * priorities 使用渠道在统一列表中的全局位次（而非各协议数组内名次）。
 * 跨协议分组按 min(priority) 还原展示顺序，若各数组只按组内名次编号，
 * 尺度不一（如 gemini 仅十几个渠道）会导致刷新后顺序被打乱、拖拽弹回。
 */
export const buildUnifiedReorderPayloads = (
  channels: Channel[],
): Map<LlmChannelKind, UnifiedReorderPayload> => {
  const payloads = new Map<LlmChannelKind, UnifiedReorderPayload>()
  channels.forEach((channel, position) => {
    for (const route of channel.protocolRoutes ?? []) {
      if (!isLlmChannelKind(route.kind)) continue
      const payload = payloads.get(route.kind) ?? { order: [], priorities: [] }
      payload.order.push(route.index)
      payload.priorities.push(position + 1)
      payloads.set(route.kind, payload)
    }
  })
  return payloads
}
