import { useAuthStore } from '@/stores/auth'
import {
  normalizeDiscoveredChannelKind,
  supportsQuickAddProtocolDiscovery
} from '@/utils/quickAddChannel'
import api from './api'
import { API_BASE } from './api-helpers'
import type { ChannelKind, ChannelPlacement, DiscoveryRateLimitResult } from './api-types'

// ─── 类型定义 ───

/** 自动添加渠道请求 */
export interface AutoAddChannelRequest {
  name?: string
  /** provider 模板模式：带 providerId 时 baseURL 由后端按 key 前缀探测判定，无需填 baseUrls */
  providerId?: string
  baseUrls?: string[]
  apiKeys: string[]
  routes?: AutoAddRouteRequest[]
  rateLimitHint?: DiscoveryRateLimitResult
  subscriptionUid?: string
  /** 渠道级代理（HTTP/HTTPS/SOCKS5）：发现/探活与后续上游请求经代理访问 */
  proxyUrl?: string
  /** 直连优先：配置代理后先直连，失败自动回退代理（仅 proxyUrl 非空时生效） */
  proxyPreferDirect?: boolean
  /** 故障转移位置：front（首位）| back（末尾，默认） */
  placement?: ChannelPlacement
}

export interface AutoAddRouteRequest {
  channelKind: ChannelKind
  supportedModels?: string[]
}

export interface AutoAddRouteDiscovery {
  primaryKind: ChannelKind
  routes: AutoAddRouteRequest[]
  rateLimitHint?: DiscoveryRateLimitResult
}

/** Provider 模板 key 前缀规则 */
export interface ProviderKeyPrefixRule {
  prefix: string
  planTag: string
}

/** Provider 候选 baseURL */
export interface ProviderCandidate {
  baseUrl: string
  planTag?: string
  region?: string
  priority?: number
}

/** Provider 在某个 CCX 协议渠道下的原生上游入口 */
export interface ProviderRoute {
  channelKind: string
  serviceType: string
  description?: string
  candidates?: ProviderCandidate[]
}

/** 已知 provider 模板 */
export interface ProviderTemplate {
  providerId: string
  aliases?: string[]
  displayName: string
  description?: string
  channelKind: string
  serviceType: string
  originType?: string
  originTier?: string
  keyPrefixRules?: ProviderKeyPrefixRule[]
  candidates?: ProviderCandidate[]
  routes?: ProviderRoute[]
}

/** 自动添加创建出的单条渠道 */
export interface AutoAddChannelResult {
  accountUid: string
  channelKind: string
  channelUid: string
  index: number
  name: string
  serviceType: string
  discoveryStarted: boolean
}

/** 自动添加渠道响应 */
export interface AutoAddChannelResponse {
  accountUid: string
  channelUid: string
  index: number
  discoveryStarted: boolean
  channels?: AutoAddChannelResult[]
}

/** Endpoint 发现信息 */
export interface AutoEndpointStatus {
  keyMask: string
  baseUrl: string
  modelsCount: number
  protocolOk: boolean
}

/** 发现状态信息 */
export interface AutoDiscoveryStatus {
  status: 'pending' | 'running' | 'done' | 'failed'
  startedAt?: string
  finishedAt?: string
  error?: string
  endpoints?: AutoEndpointStatus[]
}

/** 自动托管状态响应 */
export interface ChannelAutoStatusResponse {
  autoManaged: boolean
  autoManagedAt?: string
  discovery?: AutoDiscoveryStatus
}

export type SmartRoutingDiagnoseChannelKind = 'messages' | 'chat' | 'responses' | 'gemini' | 'images' | 'vectors'

/** 智能路由 dry-run 请求。 */
export interface SmartRoutingDiagnoseRequest {
  model: string
  channelKind: SmartRoutingDiagnoseChannelKind
  operation?: string
  agentRole?: 'main' | 'subagent' | ''
  agentType?: string
  hasImage?: boolean
  estTokens?: number
  visionNeed?: boolean
  imageGenNeed?: boolean
  embeddingNeed?: boolean
  toolUseNeed?: boolean
  reasoningNeed?: boolean
  contextNeed?: number
}

/** 后端 RequestProfile 当前使用 Go 字段名序列化。 */
export interface SmartRoutingDiagnoseProfile {
  Model: string
  ChannelKind: string
  Operation: string
  AgentRole: string
  AgentType: string
  HasImage: boolean
  EstTokens: number
  QualityNeed: string
  ContextNeed: number
  VisionNeed: boolean
  ImageGenNeed: boolean
  EmbeddingNeed: boolean
  ToolUseNeed: boolean
  ReasoningNeed: boolean
  TaskClass: string
  TaskDomain: string
}

export interface SmartRoutingDiagnoseCandidate {
  channelUid: string
  score: number
  qualityScore: number
  stabilityScore: number
  speedScore: number
  costScore: number
  savingsScore: number
  selected: boolean
  filterReasons?: string[]
  candidateKey?: string // 五元组标识（v3）：channelUID|protocol|keyIdentity|model|effort；v2 前为二元组
  mappedModel?: string
  mappingSource?: string
  mappingReason?: string
  actualModel?: string // v3：候选行实际发送模型
  keyIdentity?: string // v3：候选行 key 身份（KeyUID 或 kh_ 哈希前缀）
  quotaGroup?: string // v3：key 分组
  effort?: string // v3：思考档位（空 = passthrough）
  baseQualityTier?: string
  effortQualityTier?: string
  effortQualityScore?: number
  effortEvidenceClass?: string
  effortQualityKnown?: boolean
  effortAwareTotalScore?: number
}

export interface SmartRoutingDiagnosePlan {
  requestProfile: SmartRoutingDiagnoseProfile
  candidates: SmartRoutingDiagnoseCandidate[]
  selectedChannelUid?: string
  selectedModel?: string
  fallbackUsed: boolean
  sortReasons?: string[]
  mode: string
  logicalGroups?: SmartRoutingDiagnoseLogicalGroup[]
}

// SmartRoutingDiagnoseLogicalGroup 是候选按 LogicalChannel 聚合的诊断视图（Phase A.3）。
// 仅用于展示，不改变真实调度；后端在身份透传开启且候选带 logical 身份时填充。
export interface SmartRoutingDiagnoseLogicalGroup {
  logicalChannelUid?: string
  logicalChannelName?: string
  channelUids: string[]
  bestChannelUid: string
  bestScore: number
  selectedCount: number
  totalCount: number
}

export interface SmartRoutingDiagnoseResponse {
  plan: SmartRoutingDiagnosePlan | null
  mode: string
  message?: string
}

// ─── 路由预演（Route Preview）──

/** 路由预演请求：原始请求体 + 入站协议。 */
export interface RoutePreviewRequest {
  channelKind: SmartRoutingDiagnoseChannelKind
  model?: string
  operation?: string
  body: Record<string, unknown> | unknown[]
}

/** 预演响应中的 scheduler 层诊断。 */
export interface RoutePreviewSchedulerDiagnose {
  ok: boolean
  kind: string
  reason?: string
  summary?: string
  trace?: RoutePreviewSelectionTrace
  selected?: {
    channelIndex: number
    channelName: string
    serviceType: string
  }
}

export interface RoutePreviewSelectionTrace {
  kind: string
  model?: string
  routePrefix?: string
  stages?: Array<{ name: string; count: number }>
  candidates?: Array<{
    channelIndex: number
    channelName: string
    stage: string
    reason: string
    details?: string
  }>
  selected?: {
    channelIndex: number
    channelName: string
    reason: string
  }
}

/** 路由预演响应：SmartRouter 层 + scheduler 层两面。 */
export interface RoutePreviewResponse {
  plan: SmartRoutingDiagnosePlan | null
  mode: string
  extractedProfile: SmartRoutingDiagnoseProfile | null
  schedulerDiagnose: RoutePreviewSchedulerDiagnose | null
  message?: string
}

// ─── 辅助方法 ───

function getAuthHeaders(): Record<string, string> {
  const authStore = useAuthStore()
  const headers: Record<string, string> = {
    'Content-Type': 'application/json'
  }
  const apiKey = authStore.apiKey as unknown as string | null
  if (apiKey) {
    headers['x-api-key'] = apiKey
  }
  return headers
}

let providerTemplatesRequest: Promise<ProviderTemplate[]> | null = null

// ─── API 方法 ───

/**
 * 快速添加渠道（自动托管模式）
 * POST /api/{kind}/channels/auto-add
 */
export async function autoAddChannel(kind: string, request: AutoAddChannelRequest): Promise<AutoAddChannelResponse> {
  const url = `${API_BASE}/${kind}/channels/auto-add`
  const response = await fetch(url, {
    method: 'POST',
    headers: getAuthHeaders(),
    body: JSON.stringify(request)
  })

  if (!response.ok) {
    const text = await response.text().catch(() => response.statusText)
    throw new Error(`auto-add failed (${response.status}): ${text}`)
  }

  return response.json()
}

/** 全量探测自定义上游，并按协议保留各自实际成功的模型。 */
export async function discoverAutoAddRoutes(
  kind: ChannelKind,
  baseUrls: string[],
  apiKeys: string[]
): Promise<AutoAddRouteDiscovery | null> {
  if (!supportsQuickAddProtocolDiscovery(kind)) {
    return { primaryKind: kind, routes: [{ channelKind: kind }] }
  }

  const apiKey = apiKeys.find(key => key.trim() !== '')
  if (baseUrls.length === 0 || !apiKey) return null

  // 不传 channelKind 和 serviceType，让后端根据真实探测结果决定协议与上游类型。
  const discovery = await api.discoverChannelConfig({
    baseUrls,
    apiKey
  })
  const routes: AutoAddRouteRequest[] = []
  for (const protocol of discovery.protocols) {
    const channelKind = normalizeDiscoveredChannelKind(protocol.protocol)
    if (!channelKind || !protocol.success) continue
    const supportedModels = Array.from(
      new Set((protocol.successModels ?? []).map(model => model.trim()).filter(Boolean))
    )
    if (supportedModels.length === 0) continue
    routes.push({ channelKind, supportedModels })
  }
  if (routes.length === 0) return null

  const recommendedKind = normalizeDiscoveredChannelKind(discovery.recommendation.channelKind)
  const primaryKind =
    recommendedKind && routes.some(route => route.channelKind === recommendedKind)
      ? recommendedKind
      : routes[0].channelKind
  return { primaryKind, routes, rateLimitHint: discovery.rateLimit }
}

/** 快速探活：仅探一个真实模型以定 primaryKind，不做全量协议/能力探测。
 *  返回单条路由 `{ channelKind: primaryKind }`，不含 supportedModels；
 *  后台 discovery 会接管完整模型清单。images/vectors 沿用直接添加路径。 */
export async function discoverFast(
  kind: ChannelKind,
  baseUrls: string[],
  apiKeys: string[],
  opts?: { proxyUrl?: string; proxyPreferDirect?: boolean }
): Promise<AutoAddRouteDiscovery | null> {
  if (!supportsQuickAddProtocolDiscovery(kind)) {
    return { primaryKind: kind, routes: [{ channelKind: kind }] }
  }
  const nonEmptyKeys = apiKeys.map(key => key.trim()).filter(Boolean)
  if (baseUrls.length === 0 || nonEmptyKeys.length === 0) return null

  // 不传 channelKind，让后端根据真实探测结果决定协议；透传全部 key，后端按 (baseURL,key) 组合择优。
  const proxyUrl = opts?.proxyUrl?.trim() || undefined
  const proxyPreferDirect = proxyUrl ? opts?.proxyPreferDirect || undefined : undefined
  const fast = await api.discoverChannelConfigFast({ baseUrls, apiKeys: nonEmptyKeys, proxyUrl, proxyPreferDirect })
  const primaryKind = normalizeDiscoveredChannelKind(fast.primaryKind)
  if (!primaryKind) return null
  return { primaryKind, routes: [{ channelKind: primaryKind }], rateLimitHint: fast.rateLimit }
}

export function extractAutoAddErrorMessage(err: unknown): string {
  const raw = err instanceof Error ? err.message : String(err)
  const jsonStart = raw.indexOf('{')
  if (jsonStart >= 0) {
    try {
      const parsed = JSON.parse(raw.slice(jsonStart))
      if (parsed?.error) return String(parsed.error)
    } catch {
      // 非 JSON 正文，回退到原始消息。
    }
  }
  return raw
}

/**
 * 查询渠道自动托管状态
 * GET /api/{kind}/channels/{id}/auto-status
 */
export async function getChannelAutoStatus(kind: string, channelId: number | string): Promise<ChannelAutoStatusResponse> {
  const url = `${API_BASE}/${kind}/channels/${channelId}/auto-status`
  const response = await fetch(url, {
    method: 'GET',
    headers: getAuthHeaders()
  })

  if (!response.ok) {
    const text = await response.text().catch(() => response.statusText)
    throw new Error(`auto-status failed (${response.status}): ${text}`)
  }

  return response.json()
}

/** auto-discover 触发结果：本次覆盖的单个协议上游 */
export interface AutoDiscoverTriggeredChannel {
  kind: string
  channelUid: string
}

/** auto-discover 响应；triggered 为新契约字段（主渠道 + 兄弟协议上游），旧后端无此字段 */
export interface AutoDiscoverChannelResponse {
  channelUid: string
  discoveryStarted: boolean
  triggered?: AutoDiscoverTriggeredChannel[]
}

/**
 * 重新触发渠道自动发现（托管渠道）
 * POST /api/{kind}/channels/{id}/auto-discover
 * channelId 可为 channelUid（后端按 UID 匹配）或整数下标。
 */
export async function autoDiscoverChannel(
  kind: string,
  channelId: number | string,
): Promise<AutoDiscoverChannelResponse> {
  const url = `${API_BASE}/${kind}/channels/${channelId}/auto-discover`
  const response = await fetch(url, {
    method: 'POST',
    headers: getAuthHeaders()
  })

  if (!response.ok) {
    const text = await response.text().catch(() => response.statusText)
    const err = new Error(`auto-discover failed (${response.status}): ${text}`) as Error & { status?: number }
    err.status = response.status
    throw err
  }

  return response.json()
}

/**
 * 获取内置 provider 模板（模板化添加：选 provider + 输 key）
 * GET /api/channels/provider-templates
 */
async function fetchProviderTemplates(): Promise<ProviderTemplate[]> {
  const url = `${API_BASE}/channels/provider-templates`
  const response = await fetch(url, {
    method: 'GET',
    headers: getAuthHeaders()
  })

  if (!response.ok) {
    const text = await response.text().catch(() => response.statusText)
    throw new Error(`provider-templates failed (${response.status}): ${text}`)
  }

  const data = await response.json()
  return data.providers ?? []
}

export function getProviderTemplates(): Promise<ProviderTemplate[]> {
  if (!providerTemplatesRequest) {
    providerTemplatesRequest = fetchProviderTemplates().catch(error => {
      providerTemplatesRequest = null
      throw error
    })
  }
  return providerTemplatesRequest
}

/** 提前加载静态 provider 模板；预取失败不打断调用方。 */
export function preloadProviderTemplates(): Promise<void> {
  return getProviderTemplates().then(
    () => undefined,
    () => undefined
  )
}

/**
 * 智能路由诊断，不发送真实上游请求，也不改变调度结果。
 * POST /api/smart-routing/diagnose
 */
export async function diagnoseSmartRouting(
  request: SmartRoutingDiagnoseRequest
): Promise<SmartRoutingDiagnoseResponse> {
  const response = await fetch(`${API_BASE}/smart-routing/diagnose`, {
    method: 'POST',
    headers: getAuthHeaders(),
    body: JSON.stringify(request)
  })

  if (!response.ok) {
    const text = await response.text().catch(() => response.statusText)
    throw new Error(`smart-routing diagnose failed (${response.status}): ${text}`)
  }

  return response.json()
}

/**
 * 路由预演：粘贴原始请求体，自动提取特征后执行 SmartRouter + scheduler 两层预演。
 * 零上游请求，请求体仅内存态用于特征提取。
 * POST /api/autopilot/route-preview
 */
export async function previewRoute(
  request: RoutePreviewRequest
): Promise<RoutePreviewResponse> {
  const response = await fetch(`${API_BASE}/autopilot/route-preview`, {
    method: 'POST',
    headers: getAuthHeaders(),
    body: JSON.stringify(request)
  })

  if (!response.ok) {
    const text = await response.text().catch(() => response.statusText)
    throw new Error(`route preview failed (${response.status}): ${text}`)
  }

  return response.json()
}
