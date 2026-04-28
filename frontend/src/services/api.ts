// API服务模块
import { normalizeLocale, translate } from '@/i18n/core'
import { useAuthStore } from '@/stores/auth'
import { usePreferencesStore } from '@/stores/preferences'

export class ApiError extends Error {
  readonly status: number
  readonly details?: unknown

  constructor(message: string, status: number, details?: unknown) {
    super(message)
    this.name = 'ApiError'
    this.status = status
    this.details = details
  }
}

// 从环境变量读取配置
const getApiBase = () => {
  // 在生产环境中，API调用会直接请求当前域名
  if (import.meta.env.PROD) {
    return '/api'
  }

  // 在开发环境中，支持从环境变量配置后端地址
  const backendUrl = import.meta.env.VITE_BACKEND_URL
  const apiBasePath = import.meta.env.VITE_API_BASE_PATH || '/api'

  if (backendUrl) {
    return `${backendUrl}${apiBasePath}`
  }

  // fallback到默认配置
  return '/api'
}

const API_BASE = getApiBase()
export const API_REQUEST_TIMEOUT_MS = 10000

function createTimeoutError(timeoutMs: number): Error {
  const error = new Error(`Request timed out after ${timeoutMs}ms`)
  error.name = 'TimeoutError'
  return error
}

function createTimeoutSignal(timeoutMs: number): AbortSignal {
  if (typeof AbortSignal.timeout === 'function') {
    return AbortSignal.timeout(timeoutMs)
  }

  const controller = new AbortController()
  setTimeout(() => controller.abort(createTimeoutError(timeoutMs)), timeoutMs)
  return controller.signal
}

function mergeRequestSignals(signal?: AbortSignal | null, timeoutMs: number = API_REQUEST_TIMEOUT_MS): AbortSignal {
  const timeoutSignal = createTimeoutSignal(timeoutMs)
  if (!signal) {
    return timeoutSignal
  }

  if (typeof AbortSignal.any === 'function') {
    return AbortSignal.any([signal, timeoutSignal])
  }

  const controller = new AbortController()
  const abort = (source: AbortSignal) => {
    controller.abort(source.reason)
    signal.removeEventListener('abort', abortFromSignal)
    timeoutSignal.removeEventListener('abort', abortFromTimeout)
  }
  const abortFromSignal = () => abort(signal)
  const abortFromTimeout = () => abort(timeoutSignal)

  if (signal.aborted) {
    abort(signal)
    return controller.signal
  }

  if (timeoutSignal.aborted) {
    abort(timeoutSignal)
    return controller.signal
  }

  signal.addEventListener('abort', abortFromSignal, { once: true })
  timeoutSignal.addEventListener('abort', abortFromTimeout, { once: true })
  return controller.signal
}

// 打印当前API配置（仅开发环境）
if (import.meta.env.DEV) {
  console.log('🔗 API Configuration:', {
    API_BASE,
    BACKEND_URL: import.meta.env.VITE_BACKEND_URL,
    IS_DEV: import.meta.env.DEV,
    IS_PROD: import.meta.env.PROD
  })
}

// 渠道状态枚举
export type ChannelStatus = 'active' | 'suspended' | 'disabled'

// 渠道指标
// 分时段统计
export interface TimeWindowStats {
  requestCount: number
  successCount: number
  failureCount: number
  successRate: number
  inputTokens?: number
  outputTokens?: number
  cacheCreationTokens?: number
  cacheReadTokens?: number
  cacheHitRate?: number
}

export type CircuitState = 'closed' | 'open' | 'half_open'

export interface ChannelMetrics {
  channelIndex: number
  requestCount: number
  successCount: number
  failureCount: number
  successRate: number       // 0-100
  errorRate: number         // 0-100
  consecutiveFailures: number
  latency: number           // ms
  circuitState?: CircuitState
  circuitBrokenAt?: string
  nextRetryAt?: string
  halfOpenSuccesses?: number
  breakerFailureRate?: number
  lastSuccessAt?: string
  lastFailureAt?: string
  // 分时段统计 (15m, 1h, 6h, 24h)
  timeWindows?: {
    '15m': TimeWindowStats
    '1h': TimeWindowStats
    '6h': TimeWindowStats
    '24h': TimeWindowStats
  }
}

export interface DisabledKeyInfo {
  key: string
  reason: string      // "authentication_error" / "permission_error" / "insufficient_balance"
  message: string
  disabledAt: string  // ISO8601 时间戳
}

export interface CooldownKeyInfo {
  key: string
  failureCount: number
  cooldownUntil: string
  remainingSeconds: number
  fixedDurationSeconds?: number
}

export interface FailoverRule {
  description?: string
  action: 'cooldown' | 'blacklist'
  statusCodes?: number[]
  errorCodes?: string[]
  keywords?: string[]
  durationMinutes?: number
}
export interface Channel {
  name: string
  serviceType: 'openai' | 'gemini' | 'claude' | 'responses'
  baseUrl: string
  baseUrls?: string[]                // 多 BaseURL 支持（failover 模式）
  apiKeys: string[]
  disabledApiKeys?: DisabledKeyInfo[]  // 被拉黑的 API Key
  cooldownApiKeys?: CooldownKeyInfo[]  // 处于冷却期的 API Key
  historicalApiKeys?: string[]
  description?: string
  website?: string
  insecureSkipVerify?: boolean
  modelMapping?: Record<string, string>
  reasoningMapping?: Record<string, 'none' | 'low' | 'medium' | 'high' | 'xhigh'>
  textVerbosity?: 'low' | 'medium' | 'high' | ''
  fastMode?: boolean
  customHeaders?: Record<string, string>  // 自定义请求头
  proxyUrl?: string                        // HTTP/HTTPS/SOCKS5 代理 URL
  routePrefix?: string                     // 路由前缀（如 "kimi"，访问 /kimi/v1/messages）
  modelsResponseMode?: 'upstream' | 'manual'
  manualModels?: string[]
  autoBlacklistBalance?: boolean
  normalizeMetadataUserId?: boolean
  streamPassthroughEnabled?: boolean
  sub2apiPassthroughEnabled?: boolean
  keyAffinityEnabled?: boolean
  strictRequestPassthroughEnabled?: boolean
  modelsHealthCheckEnabled?: boolean
  modelsHealthCheckIntervalMinutes?: number
  failoverRules?: FailoverRule[]
  latency?: number
  status?: ChannelStatus | 'healthy' | 'error' | 'unknown' | ''
  index: number
  pinned?: boolean
  // 多渠道调度相关字段
  priority?: number          // 渠道优先级（数字越小优先级越高）
  metrics?: ChannelMetrics   // 实时指标
  suspendReason?: string     // 熔断原因
  promotionUntil?: string    // 促销期截止时间（ISO 格式）
  latencyTestTime?: number   // 延迟测试时间戳（用于 5 分钟后自动清除显示）
  lowQuality?: boolean       // 低质量渠道标记：启用后强制本地估算 token，偏差>5%时使用本地值
  injectDummyThoughtSignature?: boolean  // Gemini 特定：为 functionCall 注入 dummy thought_signature（兼容第三方 API）
  stripThoughtSignature?: boolean        // Gemini 特定：移除 thought_signature 字段（兼容旧版 Gemini API）
  supportedModels?: string[]  // 支持的模型白名单（空=全部），支持通配符如 gpt-4*
  rpm?: number                // 能力测试发送速率（仅影响能力测试）
}

export interface ChannelsResponse {
  channels: Channel[]
  current: number
}

// 渠道仪表盘响应（合并 channels + metrics + stats）
export interface ChannelDashboardResponse {
  channels: Channel[]
  metrics: ChannelMetrics[]
  stats: SchedulerStatsResponse
  recentActivity?: ChannelRecentActivity[]  // 最近 15 分钟分段活跃度
}

export interface SchedulerStatsResponse {
  multiChannelMode: boolean
  activeChannelCount: number
  traceAffinityCount: number
  traceAffinityTTL: string
  failureThreshold: number
  windowSize: number
  circuitRecoveryTime?: string
  consecutiveRetryableFailuresThreshold?: number
  halfOpenSuccessTarget?: number
  circuitBackoffBase?: string
  circuitBackoffMax?: string
}

export interface PingResult {
  success: boolean
  latency: number
  status: string
  error?: string
}

export interface ResumeChannelResponse {
  success: boolean
  message: string
  restoredKeys?: number
}

// ============== 能力测试类型 ==============

export interface CapabilityTestJobStartResponse {
  jobId: string
  resumed?: boolean
  job?: CapabilityTestJob
}

export type CapabilityLifecycle = 'pending' | 'active' | 'done' | 'cancelled'
export type CapabilityOutcome = 'unknown' | 'success' | 'failed' | 'partial' | 'cancelled'
export type CapabilityRunMode = 'fresh' | 'reused_running' | 'resumed_cancelled' | 'cache_hit' | 'reused_previous_results'

export type CapabilityTestJobStatus = 'queued' | 'running' | 'completed' | 'failed' | 'cancelled'
export type CapabilityProtocolJobStatus = 'queued' | 'running' | 'completed' | 'failed'
export type CapabilityModelJobStatus = 'queued' | 'running' | 'success' | 'failed' | 'skipped'

export interface CapabilityJobProgress {
  totalModels: number
  queuedModels: number
  runningModels: number
  successModels: number
  failedModels: number
  skippedModels: number
  completedModels: number
}

export interface CapabilityModelJobResult {
  model: string
  status: CapabilityModelJobStatus
  lifecycle: CapabilityLifecycle
  outcome: CapabilityOutcome
  reason?: string
  success: boolean
  latency: number
  streamingSupported: boolean
  error?: string
  startedAt?: string
  testedAt?: string
}

export interface CapabilityProtocolJobResult {
  protocol: string
  status: CapabilityProtocolJobStatus
  lifecycle: CapabilityLifecycle
  outcome: CapabilityOutcome
  reason?: string
  success: boolean
  latency: number
  streamingSupported: boolean
  testedModel: string
  modelResults?: CapabilityModelJobResult[]
  successCount?: number
  attemptedModels?: number
  error?: string
  testedAt: string
}

export interface CapabilityTestJob {
  jobId: string
  channelId: number
  channelName: string
  channelKind: string
  sourceType: string
  status: CapabilityTestJobStatus
  lifecycle: CapabilityLifecycle
  outcome: CapabilityOutcome
  reason?: string
  runMode?: CapabilityRunMode
  summaryReason?: string
  activeOperations?: number
  isResumed?: boolean
  hasReusedResults?: boolean
  tests: CapabilityProtocolJobResult[]
  compatibleProtocols: string[]
  totalDuration: number
  startedAt?: string
  updatedAt: string
  finishedAt?: string
  progress: CapabilityJobProgress
  error?: string
  cacheHit?: boolean
  timeoutMilliseconds?: number
}

export interface ModelTestResult {
  model: string
  success: boolean
  latency: number
  streamingSupported: boolean
  error?: string
  startedAt?: string
  testedAt: string
}

export interface ProtocolTestResult {
  protocol: string
  success: boolean
  latency: number
  streamingSupported: boolean
  testedModel: string
  modelResults?: ModelTestResult[]
  successCount?: number
  attemptedModels?: number
  error?: string
  testedAt: string
}

export interface CapabilityTestResult {
  channelId: number
  channelName: string
  sourceType: string
  tests: ProtocolTestResult[]
  compatibleProtocols: string[]
  totalDuration: number
}

// 历史数据点（用于时间序列图表）
export interface HistoryDataPoint {
  timestamp: string
  requestCount: number
  successCount: number
  failureCount: number
  successRate: number
}

// 渠道历史指标响应
export interface MetricsHistoryResponse {
  channelIndex: number
  channelName: string
  dataPoints: HistoryDataPoint[]
}

// Key 级别历史数据点（包含 Token 数据）
export interface KeyHistoryDataPoint {
  timestamp: string
  requestCount: number
  successCount: number
  failureCount: number
  successRate: number
  inputTokens: number
  outputTokens: number
  cacheCreationTokens: number
  cacheReadTokens: number
}

// 单个 Key 的历史数据
export interface KeyHistoryData {
  keyMask: string
  model?: string  // 模型名（可选，用于 Key+Model 组合显示）
  color: string
  dataPoints: KeyHistoryDataPoint[]
}

// 渠道 Key 级别历史指标响应
export interface ChannelKeyMetricsHistoryResponse {
  channelIndex: number
  channelName: string
  keys: KeyHistoryData[]
}

// ============== 全局统计类型 ==============

// 全局历史数据点（包含 Token 数据）
export interface GlobalHistoryDataPoint {
  timestamp: string
  requestCount: number
  successCount: number
  failureCount: number
  successRate: number
  inputTokens: number
  outputTokens: number
  cacheCreationTokens: number
  cacheReadTokens: number
}

// 全局统计汇总
export interface GlobalStatsSummary {
  totalRequests: number
  totalSuccess: number
  totalFailure: number
  totalInputTokens: number
  totalOutputTokens: number
  totalCacheCreationTokens: number
  totalCacheReadTokens: number
  avgSuccessRate: number
  duration: string
}

// 全局统计响应
export interface GlobalStatsHistoryResponse {
  dataPoints: GlobalHistoryDataPoint[]
  summary: GlobalStatsSummary
  modelDataPoints?: Record<string, ModelHistoryDataPoint[]>
}
// ============== 模型统计类型 ==============

export interface ModelHistoryDataPoint {
  timestamp: string
  requestCount: number
  successCount: number
  failureCount: number
  inputTokens: number
  outputTokens: number
}

export interface ModelStatsHistoryResponse {
  models: Record<string, ModelHistoryDataPoint[]>
  duration: string
  interval: string
}

// ============== 渠道日志类型 ==============

export interface ChannelLogEntry {
  timestamp: string
  model: string
  originalModel?: string
  statusCode: number
  durationMs: number
  success: boolean
  keyMask: string
  baseUrl: string
  errorInfo: string
  isRetry: boolean
  interfaceType?: string  // 接口类型（Messages/Responses/Gemini）
  requestSource?: string
}

export interface ChannelLogsResponse {
  channelIndex: number
  logs: ChannelLogEntry[]
}

// ============== 渠道实时活跃度类型 ==============

// 活跃度分段数据（每 6 秒一段）
export interface ActivitySegment {
  requestCount: number
  successCount: number
  failureCount: number
  inputTokens: number
  outputTokens: number
}

// 渠道最近活跃度数据（稀疏格式，减少 JSON 体积）
export interface ChannelRecentActivity {
  channelIndex: number
  segments: Record<number, ActivitySegment> | ActivitySegment[]  // 稀疏 Map 或数组格式（兼容旧版）
  totalSegs: number                                               // 总段数（固定 150）
  rpm: number                                                     // 15分钟平均 RPM
  tpm: number                                                     // 15分钟平均 TPM
}

// 辅助函数：将稀疏 segments 展开为完整数组
export function expandSparseSegments(activity: ChannelRecentActivity): ActivitySegment[] {
  const totalSegs = activity.totalSegs || 150
  const result: ActivitySegment[] = new Array(totalSegs).fill(null).map(() => ({
    requestCount: 0,
    successCount: 0,
    failureCount: 0,
    inputTokens: 0,
    outputTokens: 0
  }))

  // 兼容旧版数组格式
  if (Array.isArray(activity.segments)) {
    return activity.segments
  }

  // 稀疏 Map 格式：展开到完整数组
  if (activity.segments && typeof activity.segments === 'object') {
    for (const [indexStr, seg] of Object.entries(activity.segments)) {
      const index = parseInt(indexStr, 10)
      if (index >= 0 && index < totalSegs && seg) {
        result[index] = seg
      }
    }
  }

  return result
}

// ============== 上游模型列表类型 ==============

export interface ModelEntry {
  id: string
  object: string
  created: number
  owned_by: string
}

export interface ModelsResponse {
  object: string
  data: ModelEntry[]
}

/**
 * 构建上游的 /v1/models 端点 URL
 * 参考：backend-go/internal/handlers/messages/models.go:240-257
 */
function buildModelsURL(baseURL: string): string {
  // 处理 # 后缀（跳过版本前缀）
  const skipVersionPrefix = baseURL.endsWith('#')
  if (skipVersionPrefix) {
    baseURL = baseURL.slice(0, -1)
  }
  baseURL = baseURL.replace(/\/$/, '')

  // 检查是否已有版本后缀（如 /v1, /v2）
  const versionPattern = /\/v\d+[a-z]*$/
  const hasVersionSuffix = versionPattern.test(baseURL)

  // 构建端点
  let endpoint = '/models'
  if (!hasVersionSuffix && !skipVersionPrefix) {
    endpoint = '/v1' + endpoint
  }

  return baseURL + endpoint
}

/**
 * 直接从上游获取模型列表（前端直连）
 */
export async function fetchUpstreamModels(
  baseUrl: string,
  apiKey: string
): Promise<ModelsResponse> {
  const url = buildModelsURL(baseUrl)

  const response = await fetch(url, {
    method: 'GET',
    headers: {
      'Authorization': `Bearer ${apiKey}`
    },
    signal: createTimeoutSignal(API_REQUEST_TIMEOUT_MS)
  })

  if (!response.ok) {
    let errorMessage = `${response.status} ${response.statusText}`
    let errorDetails: unknown = null

    try {
      const errorText = await response.text()
      if (errorText) {
        const errorJson = JSON.parse(errorText)
        // 解析上游错误格式: { "error": { "code": "", "message": "...", "type": "..." } }
        if (errorJson.error && errorJson.error.message) {
          errorMessage = errorJson.error.message
          errorDetails = errorJson.error
        } else if (errorJson.message) {
          errorMessage = errorJson.message
          errorDetails = errorJson
        }
      }
    } catch {
      // 解析失败,使用默认错误消息
    }

    throw new ApiError(errorMessage, response.status, errorDetails)
  }

  return await response.json()
}

export class ApiService {
  private t(key: Parameters<typeof translate>[1], params?: Parameters<typeof translate>[2]): string {
    const preferencesStore = usePreferencesStore()
    return translate(normalizeLocale(preferencesStore.uiLanguage), key, params)
  }

  // 获取当前 API Key（从 AuthStore）
  private getApiKey(): string | null {
    const authStore = useAuthStore()
    return authStore.apiKey
  }

  private async parseResponseBody(response: Response): Promise<unknown> {
    const text = await response.text()
    if (!text) return null
    try {
      return JSON.parse(text)
    } catch {
      return text
    }
  }

  // eslint-disable-next-line @typescript-eslint/no-explicit-any
  private async request(url: string, options: RequestInit = {}): Promise<any> {
    const headers: Record<string, string> = {
      'Content-Type': 'application/json',
      ...(options.headers as Record<string, string>)
    }

    // 从 AuthStore 获取 API 密钥并添加到请求头
    const apiKey = this.getApiKey()
    if (apiKey) {
      headers['x-api-key'] = apiKey
    }

    const response = await fetch(`${API_BASE}${url}`, {
      ...options,
      headers,
      signal: mergeRequestSignals(options.signal)
    })

    if (!response.ok) {
      const errorBody = await this.parseResponseBody(response)
      const errorMessage =
        (typeof errorBody === 'object' && errorBody && 'error' in errorBody && typeof (errorBody as { error?: unknown }).error === 'string'
          ? (errorBody as { error: string }).error
          : typeof errorBody === 'object' && errorBody && 'message' in errorBody && typeof (errorBody as { message?: unknown }).message === 'string'
            ? (errorBody as { message: string }).message
            : typeof errorBody === 'string'
              ? errorBody
              : null) || `Request failed (${response.status})`

      // 如果是401错误，清除认证信息并提示用户重新登录
      if (response.status === 401) {
        const authStore = useAuthStore()
        authStore.clearAuth()
        // 记录认证失败(前端日志)
        if (import.meta.env.DEV) {
          console.warn('🔒 认证失败 - 时间:', new Date().toISOString())
        }
        throw new ApiError(this.t('service.authFailed'), response.status, errorBody)
      }

      throw new ApiError(errorMessage, response.status, errorBody)
    }

    if (response.status === 204) return null
    return this.parseResponseBody(response)
  }

  async getChannels(): Promise<ChannelsResponse> {
    return this.request('/messages/channels')
  }

  async addChannel(channel: Omit<Channel, 'index' | 'latency' | 'status'>): Promise<void> {
    await this.request('/messages/channels', {
      method: 'POST',
      body: JSON.stringify(channel)
    })
  }

  async updateChannel(id: number, channel: Partial<Channel>): Promise<void> {
    await this.request(`/messages/channels/${id}`, {
      method: 'PUT',
      body: JSON.stringify(channel)
    })
  }

  async deleteChannel(id: number): Promise<void> {
    await this.request(`/messages/channels/${id}`, {
      method: 'DELETE'
    })
  }

  async addApiKey(channelId: number, apiKey: string): Promise<void> {
    await this.request(`/messages/channels/${channelId}/keys`, {
      method: 'POST',
      body: JSON.stringify({ apiKey })
    })
  }

  async removeApiKey(channelId: number, apiKey: string): Promise<void> {
    await this.request(`/messages/channels/${channelId}/keys/${encodeURIComponent(apiKey)}`, {
      method: 'DELETE'
    })
  }

  async restoreApiKey(channelId: number, apiKey: string): Promise<void> {
    await this.request(`/messages/channels/${channelId}/keys/restore`, {
      method: 'POST',
      body: JSON.stringify({ apiKey })
    })
  }

  async pingChannel(id: number): Promise<PingResult> {
    return this.request(`/messages/ping/${id}`)
  }

  async pingAllChannels(): Promise<Array<{ id: number; name: string; latency: number; status: string }>> {
    return this.request('/messages/ping')
  }

  async getChannelModels(id: number, key: string, baseUrl?: string): Promise<ModelsResponse> {
    const body: { key: string; baseUrl?: string } = { key }
    if (baseUrl) body.baseUrl = baseUrl
    return this.request(`/messages/channels/${id}/models`, {
      method: 'POST',
      body: JSON.stringify(body)
    })
  }

  // ============== 能力测试 API ==============

  async startChannelCapabilityTest(
    type: 'messages' | 'chat' | 'gemini' | 'responses',
    id: number,
    previousJobId?: string
  ): Promise<CapabilityTestJobStartResponse> {
    const body: { targetProtocols: string[]; timeout: number; previousJobId?: string } = {
      targetProtocols: ['messages', 'chat', 'gemini', 'responses'],
      timeout: 10000
    }
    if (previousJobId) {
      body.previousJobId = previousJobId
    }
    return this.request(`/${type}/channels/${id}/capability-test`, {
      method: 'POST',
      body: JSON.stringify(body)
    })
  }

  async getChannelCapabilityTestStatus(type: 'messages' | 'chat' | 'gemini' | 'responses', id: number, jobId: string): Promise<CapabilityTestJob> {
    return this.request(`/${type}/channels/${id}/capability-test/${jobId}`)
  }

  async cancelCapabilityTest(type: 'messages' | 'chat' | 'gemini' | 'responses', id: number, jobId: string): Promise<void> {
    await this.request(`/${type}/channels/${id}/capability-test/${jobId}`, {
      method: 'DELETE'
    })
  }

  async retryCapabilityTestModel(type: 'messages' | 'chat' | 'gemini' | 'responses', id: number, jobId: string, protocol: string, model: string): Promise<void> {
    await this.request(`/${type}/channels/${id}/capability-test/${jobId}/retry`, {
      method: 'POST',
      body: JSON.stringify({ protocol, model })
    })
  }

  async testChannelCapability(type: 'messages' | 'chat' | 'gemini' | 'responses', id: number): Promise<CapabilityTestResult> {
    return this.request(`/${type}/channels/${id}/capability-test`, {
      method: 'POST',
      body: JSON.stringify({
        targetProtocols: ['messages', 'chat', 'gemini', 'responses'],
        timeout: 10000
      })
    })
  }

  // ============== Responses 渠道管理 API ==============

  async getResponsesChannels(): Promise<ChannelsResponse> {
    return this.request('/responses/channels')
  }

  async addResponsesChannel(channel: Omit<Channel, 'index' | 'latency' | 'status'>): Promise<void> {
    await this.request('/responses/channels', {
      method: 'POST',
      body: JSON.stringify(channel)
    })
  }

  async pingResponsesChannel(id: number): Promise<PingResult> {
    return this.request(`/responses/ping/${id}`)
  }

  async pingAllResponsesChannels(): Promise<Array<{ id: number; name: string; latency: number; status: string }>> {
    return this.request('/responses/ping')
  }

  async updateResponsesChannel(id: number, channel: Partial<Channel>): Promise<void> {
    await this.request(`/responses/channels/${id}`, {
      method: 'PUT',
      body: JSON.stringify(channel)
    })
  }

  async deleteResponsesChannel(id: number): Promise<void> {
    await this.request(`/responses/channels/${id}`, {
      method: 'DELETE'
    })
  }

  async addResponsesApiKey(channelId: number, apiKey: string): Promise<void> {
    await this.request(`/responses/channels/${channelId}/keys`, {
      method: 'POST',
      body: JSON.stringify({ apiKey })
    })
  }

  async removeResponsesApiKey(channelId: number, apiKey: string): Promise<void> {
    await this.request(`/responses/channels/${channelId}/keys/${encodeURIComponent(apiKey)}`, {
      method: 'DELETE'
    })
  }

  async restoreResponsesApiKey(channelId: number, apiKey: string): Promise<void> {
    await this.request(`/responses/channels/${channelId}/keys/restore`, {
      method: 'POST',
      body: JSON.stringify({ apiKey })
    })
  }

  async moveApiKeyToTop(channelId: number, apiKey: string): Promise<void> {
    await this.request(`/messages/channels/${channelId}/keys/${encodeURIComponent(apiKey)}/top`, {
      method: 'POST'
    })
  }

  async moveApiKeyToBottom(channelId: number, apiKey: string): Promise<void> {
    await this.request(`/messages/channels/${channelId}/keys/${encodeURIComponent(apiKey)}/bottom`, {
      method: 'POST'
    })
  }

  async getResponsesChannelModels(id: number, key: string, baseUrl?: string): Promise<ModelsResponse> {
    const body: { key: string; baseUrl?: string } = { key }
    if (baseUrl) body.baseUrl = baseUrl
    return this.request(`/responses/channels/${id}/models`, {
      method: 'POST',
      body: JSON.stringify(body)
    })
  }

  async moveResponsesApiKeyToTop(channelId: number, apiKey: string): Promise<void> {
    await this.request(`/responses/channels/${channelId}/keys/${encodeURIComponent(apiKey)}/top`, {
      method: 'POST'
    })
  }

  async moveResponsesApiKeyToBottom(channelId: number, apiKey: string): Promise<void> {
    await this.request(`/responses/channels/${channelId}/keys/${encodeURIComponent(apiKey)}/bottom`, {
      method: 'POST'
    })
  }

  // ============== 多渠道调度 API ==============

  // 重新排序渠道优先级
  async reorderChannels(order: number[]): Promise<void> {
    await this.request('/messages/channels/reorder', {
      method: 'POST',
      body: JSON.stringify({ order })
    })
  }

  // 设置渠道状态
  async setChannelStatus(channelId: number, status: ChannelStatus): Promise<void> {
    await this.request(`/messages/channels/${channelId}/status`, {
      method: 'PATCH',
      body: JSON.stringify({ status })
    })
  }

  // 恢复熔断渠道（重置错误计数）
  async resumeChannel(channelId: number): Promise<ResumeChannelResponse> {
    return this.request(`/messages/channels/${channelId}/resume`, {
      method: 'POST'
    })
  }

  // 获取渠道指标
  async getChannelMetrics(): Promise<ChannelMetrics[]> {
    return this.request('/messages/channels/metrics')
  }

  // 获取调度器统计信息
  async getSchedulerStats(type?: 'messages' | 'responses' | 'gemini' | 'chat'): Promise<SchedulerStatsResponse> {
    // Gemini 暂无调度器统计，返回默认值
    if (type === 'gemini') {
      return {
        multiChannelMode: false,
        activeChannelCount: 0,
        traceAffinityCount: 0,
        traceAffinityTTL: '0s',
        failureThreshold: 0,
        windowSize: 0
      }
    }
    const query = type === 'responses' ? '?type=responses' : type === 'chat' ? '?type=chat' : ''
    return this.request(`/messages/channels/scheduler/stats${query}`)
  }

  // 获取渠道仪表盘数据（合并 channels + metrics + stats）
  async getChannelDashboard(type: 'messages' | 'responses' | 'gemini' | 'chat' = 'messages'): Promise<ChannelDashboardResponse> {
    const query = type !== 'messages' ? `?type=${type}` : ''
    return this.request(`/messages/channels/dashboard${query}`)
  }

  // ============== Responses 多渠道调度 API ==============

  // 重新排序 Responses 渠道优先级
  async reorderResponsesChannels(order: number[]): Promise<void> {
    await this.request('/responses/channels/reorder', {
      method: 'POST',
      body: JSON.stringify({ order })
    })
  }

  // 设置 Responses 渠道状态
  async setResponsesChannelStatus(channelId: number, status: ChannelStatus): Promise<void> {
    await this.request(`/responses/channels/${channelId}/status`, {
      method: 'PATCH',
      body: JSON.stringify({ status })
    })
  }

  // 恢复 Responses 熔断渠道
  async resumeResponsesChannel(channelId: number): Promise<ResumeChannelResponse> {
    return this.request(`/responses/channels/${channelId}/resume`, {
      method: 'POST'
    })
  }

  // 获取 Responses 渠道指标
  async getResponsesChannelMetrics(): Promise<ChannelMetrics[]> {
    return this.request('/responses/channels/metrics')
  }

  // ============== 促销期管理 API ==============

  // 设置 Messages 渠道促销期
  async setChannelPromotion(channelId: number, durationSeconds: number): Promise<void> {
    await this.request(`/messages/channels/${channelId}/promotion`, {
      method: 'POST',
      body: JSON.stringify({ duration: durationSeconds })
    })
  }

  // 设置 Responses 渠道促销期
  async setResponsesChannelPromotion(channelId: number, durationSeconds: number): Promise<void> {
    await this.request(`/responses/channels/${channelId}/promotion`, {
      method: 'POST',
      body: JSON.stringify({ duration: durationSeconds })
    })
  }

  // ============== Fuzzy 模式 API ==============

  // 获取 Fuzzy 模式状态
  async getFuzzyMode(): Promise<{ fuzzyModeEnabled: boolean }> {
    return this.request('/settings/fuzzy-mode')
  }

  // 设置 Fuzzy 模式状态
  async setFuzzyMode(enabled: boolean): Promise<void> {
    await this.request('/settings/fuzzy-mode', {
      method: 'PUT',
      body: JSON.stringify({ enabled })
    })
  }

  // ============== 移除计费头 API ==============

  // 获取移除计费头状态
  async getStripBillingHeader(): Promise<{ stripBillingHeader: boolean }> {
    return this.request('/settings/strip-billing-header')
  }

  // 设置移除计费头状态
  async setStripBillingHeader(enabled: boolean): Promise<void> {
    await this.request('/settings/strip-billing-header', {
      method: 'PUT',
      body: JSON.stringify({ enabled })
    })
  }

  // ============== 历史指标 API ==============

  // 获取 Messages 渠道历史指标（用于时间序列图表）
  async getChannelMetricsHistory(duration: string = '24h'): Promise<MetricsHistoryResponse[]> {
    return this.request(`/messages/channels/metrics/history?duration=${duration}`)
  }

  // 获取 Responses 渠道历史指标
  async getResponsesChannelMetricsHistory(duration: string = '24h'): Promise<MetricsHistoryResponse[]> {
    return this.request(`/responses/channels/metrics/history?duration=${duration}`)
  }

  // ============== Key 级别历史指标 API ==============

  // 获取 Messages 渠道 Key 级别历史指标（用于 Key 趋势图表）
  async getChannelKeyMetricsHistory(channelId: number, duration: string = '6h'): Promise<ChannelKeyMetricsHistoryResponse> {
    return this.request(`/messages/channels/${channelId}/keys/metrics/history?duration=${duration}`)
  }

  // 获取 Responses 渠道 Key 级别历史指标
  async getResponsesChannelKeyMetricsHistory(channelId: number, duration: string = '6h'): Promise<ChannelKeyMetricsHistoryResponse> {
    return this.request(`/responses/channels/${channelId}/keys/metrics/history?duration=${duration}`)
  }

  // ============== 全局统计 API ==============

  // 获取 Messages 全局统计历史
  async getMessagesGlobalStats(duration: string = '24h'): Promise<GlobalStatsHistoryResponse> {
    return this.request(`/messages/global/stats/history?duration=${duration}`)
  }

  // 获取 Responses 全局统计历史
  async getResponsesGlobalStats(duration: string = '24h'): Promise<GlobalStatsHistoryResponse> {
    return this.request(`/responses/global/stats/history?duration=${duration}`)
  }
  // ============== 模型统计 API ==============

  async getModelStatsHistory(type: 'messages' | 'responses' | 'gemini' | 'chat', duration: string = '24h'): Promise<ModelStatsHistoryResponse> {
    return this.request(`/${type}/models/stats/history?duration=${duration}`)
  }

  // ============== 渠道日志 API ==============

  async getChannelLogs(type: 'messages' | 'responses' | 'gemini' | 'chat', channelId: number): Promise<ChannelLogsResponse> {
    return this.request(`/${type}/channels/${channelId}/logs`)
  }

  // ============== Chat 渠道管理 API ==============

  async getChatChannels(): Promise<ChannelsResponse> {
    return this.request('/chat/channels')
  }

  async addChatChannel(channel: Omit<Channel, 'index' | 'latency' | 'status'>): Promise<void> {
    await this.request('/chat/channels', {
      method: 'POST',
      body: JSON.stringify(channel)
    })
  }

  async updateChatChannel(id: number, channel: Partial<Channel>): Promise<void> {
    await this.request(`/chat/channels/${id}`, {
      method: 'PUT',
      body: JSON.stringify(channel)
    })
  }

  async deleteChatChannel(id: number): Promise<void> {
    await this.request(`/chat/channels/${id}`, {
      method: 'DELETE'
    })
  }

  async addChatApiKey(channelId: number, apiKey: string): Promise<void> {
    await this.request(`/chat/channels/${channelId}/keys`, {
      method: 'POST',
      body: JSON.stringify({ apiKey })
    })
  }

  async removeChatApiKey(channelId: number, apiKey: string): Promise<void> {
    await this.request(`/chat/channels/${channelId}/keys/${encodeURIComponent(apiKey)}`, {
      method: 'DELETE'
    })
  }

  async restoreChatApiKey(channelId: number, apiKey: string): Promise<void> {
    await this.request(`/chat/channels/${channelId}/keys/restore`, {
      method: 'POST',
      body: JSON.stringify({ apiKey })
    })
  }

  async moveChatApiKeyToTop(channelId: number, apiKey: string): Promise<void> {
    await this.request(`/chat/channels/${channelId}/keys/${encodeURIComponent(apiKey)}/top`, {
      method: 'POST'
    })
  }

  async moveChatApiKeyToBottom(channelId: number, apiKey: string): Promise<void> {
    await this.request(`/chat/channels/${channelId}/keys/${encodeURIComponent(apiKey)}/bottom`, {
      method: 'POST'
    })
  }

  // ============== Chat 多渠道调度 API ==============

  async reorderChatChannels(order: number[]): Promise<void> {
    await this.request('/chat/channels/reorder', {
      method: 'POST',
      body: JSON.stringify({ order })
    })
  }

  async setChatChannelStatus(channelId: number, status: ChannelStatus): Promise<void> {
    await this.request(`/chat/channels/${channelId}/status`, {
      method: 'PATCH',
      body: JSON.stringify({ status })
    })
  }

  async resumeChatChannel(channelId: number): Promise<ResumeChannelResponse> {
    return this.request(`/chat/channels/${channelId}/resume`, {
      method: 'POST'
    })
  }

  async getChatChannelMetrics(): Promise<ChannelMetrics[]> {
    return this.request('/chat/channels/metrics')
  }

  async setChatChannelPromotion(channelId: number, durationSeconds: number): Promise<void> {
    await this.request(`/chat/channels/${channelId}/promotion`, {
      method: 'POST',
      body: JSON.stringify({ duration: durationSeconds })
    })
  }

  // ============== Chat 历史指标 API ==============

  async getChatChannelMetricsHistory(duration: string = '24h'): Promise<MetricsHistoryResponse[]> {
    return this.request(`/chat/channels/metrics/history?duration=${duration}`)
  }

  async getChatChannelKeyMetricsHistory(channelId: number, duration: string = '6h'): Promise<ChannelKeyMetricsHistoryResponse> {
    return this.request(`/chat/channels/${channelId}/keys/metrics/history?duration=${duration}`)
  }

  async getChatGlobalStats(duration: string = '24h'): Promise<GlobalStatsHistoryResponse> {
    return this.request(`/chat/global/stats/history?duration=${duration}`)
  }

  async pingChatChannel(id: number): Promise<PingResult> {
    return this.request(`/chat/ping/${id}`)
  }

  async pingAllChatChannels(): Promise<Array<{ id: number; name: string; latency: number; status: string }>> {
    return this.request('/chat/ping')
  }

  async getChatChannelModels(id: number, key: string, baseUrl?: string): Promise<ModelsResponse> {
    const body: { key: string; baseUrl?: string } = { key }
    if (baseUrl) body.baseUrl = baseUrl
    return this.request(`/chat/channels/${id}/models`, {
      method: 'POST',
      body: JSON.stringify(body)
    })
  }

  // ============== Gemini 渠道管理 API ==============

  async getGeminiChannels(): Promise<ChannelsResponse> {
    return this.request('/gemini/channels')
  }

  async addGeminiChannel(channel: Omit<Channel, 'index' | 'latency' | 'status'>): Promise<void> {
    await this.request('/gemini/channels', {
      method: 'POST',
      body: JSON.stringify(channel)
    })
  }

  async updateGeminiChannel(id: number, channel: Partial<Channel>): Promise<void> {
    await this.request(`/gemini/channels/${id}`, {
      method: 'PUT',
      body: JSON.stringify(channel)
    })
  }

  async deleteGeminiChannel(id: number): Promise<void> {
    await this.request(`/gemini/channels/${id}`, {
      method: 'DELETE'
    })
  }

  async addGeminiApiKey(channelId: number, apiKey: string): Promise<void> {
    await this.request(`/gemini/channels/${channelId}/keys`, {
      method: 'POST',
      body: JSON.stringify({ apiKey })
    })
  }

  async removeGeminiApiKey(channelId: number, apiKey: string): Promise<void> {
    await this.request(`/gemini/channels/${channelId}/keys/${encodeURIComponent(apiKey)}`, {
      method: 'DELETE'
    })
  }

  async restoreGeminiApiKey(channelId: number, apiKey: string): Promise<void> {
    await this.request(`/gemini/channels/${channelId}/keys/restore`, {
      method: 'POST',
      body: JSON.stringify({ apiKey })
    })
  }

  async moveGeminiApiKeyToTop(channelId: number, apiKey: string): Promise<void> {
    await this.request(`/gemini/channels/${channelId}/keys/${encodeURIComponent(apiKey)}/top`, {
      method: 'POST'
    })
  }

  async moveGeminiApiKeyToBottom(channelId: number, apiKey: string): Promise<void> {
    await this.request(`/gemini/channels/${channelId}/keys/${encodeURIComponent(apiKey)}/bottom`, {
      method: 'POST'
    })
  }

  // ============== Gemini 多渠道调度 API ==============

  async reorderGeminiChannels(order: number[]): Promise<void> {
    await this.request('/gemini/channels/reorder', {
      method: 'POST',
      body: JSON.stringify({ order })
    })
  }

  async setGeminiChannelStatus(channelId: number, status: ChannelStatus): Promise<void> {
    await this.request(`/gemini/channels/${channelId}/status`, {
      method: 'PATCH',
      body: JSON.stringify({ status })
    })
  }

  // Gemini 恢复渠道（重置熔断并恢复被拉黑的 Key）
  async resumeGeminiChannel(channelId: number): Promise<ResumeChannelResponse> {
    return this.request(`/gemini/channels/${channelId}/resume`, {
      method: 'POST'
    })
  }

  async getGeminiChannelMetrics(): Promise<ChannelMetrics[]> {
    return this.request('/gemini/channels/metrics')
  }

  async setGeminiChannelPromotion(channelId: number, durationSeconds: number): Promise<void> {
    await this.request(`/gemini/channels/${channelId}/promotion`, {
      method: 'POST',
      body: JSON.stringify({ duration: durationSeconds })
    })
  }

  // ============== Gemini 历史指标 API ==============

  // 获取 Gemini 渠道历史指标
  async getGeminiChannelMetricsHistory(duration: string = '24h'): Promise<MetricsHistoryResponse[]> {
    return this.request(`/gemini/channels/metrics/history?duration=${duration}`)
  }

  // 获取 Gemini 渠道 Key 级别历史指标
  async getGeminiChannelKeyMetricsHistory(channelId: number, duration: string = '6h'): Promise<ChannelKeyMetricsHistoryResponse> {
    return this.request(`/gemini/channels/${channelId}/keys/metrics/history?duration=${duration}`)
  }

  // 获取 Gemini 全局统计历史
  async getGeminiGlobalStats(duration: string = '24h'): Promise<GlobalStatsHistoryResponse> {
    return this.request(`/gemini/global/stats/history?duration=${duration}`)
  }

  async pingGeminiChannel(id: number): Promise<PingResult> {
    return this.request(`/gemini/ping/${id}`)
  }

  async pingAllGeminiChannels(): Promise<Array<{ id: number; name: string; latency: number; status: string }>> {
    const resp = await this.request('/gemini/ping')
    // 后端返回 { channels: [...] }，需要提取并转换字段名
    return (resp.channels || []).map((ch: { index: number; name: string; latency: number; success: boolean }) => ({
      id: ch.index,
      name: ch.name,
      latency: ch.latency,
      status: ch.success ? 'healthy' : 'error'
    }))
  }

  async getGeminiChannelModels(id: number, key: string, baseUrl?: string): Promise<ModelsResponse> {
    const body: { key: string; baseUrl?: string } = { key }
    if (baseUrl) body.baseUrl = baseUrl
    return this.request(`/gemini/channels/${id}/models`, {
      method: 'POST',
      body: JSON.stringify(body)
    })
  }
}

// 健康检查响应类型
export interface HealthResponse {
  version?: {
    version: string
    buildTime: string
    gitCommit: string
  }
  timestamp: string
  uptime: number
  mode: string
}

/**
 * 获取健康检查信息（包含版本号）
 * 注意：/health 端点不需要认证，直接请求根路径
 */
export const fetchHealth = async (): Promise<HealthResponse> => {
  const baseUrl = import.meta.env.PROD ? '' : (import.meta.env.VITE_BACKEND_URL || '')
  const response = await fetch(`${baseUrl}/health`)
  if (!response.ok) {
    throw new Error(`Health check failed: ${response.status}`)
  }
  return response.json()
}

export const api = new ApiService()
export default api
