/**
 * CCX Admin API 类型定义
 * 从根 frontend/src/services/api.ts 移植，去除 Vuetify/Pinia 依赖。
 * 调用层由 useAdminApi composable 提供。
 */

// 渠道状态枚举
export type ChannelStatus = 'active' | 'suspended' | 'disabled'

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
export type ChannelAuthHeader = 'auto' | 'bearer' | 'x-api-key'

export const COPILOT_OAUTH_DEVICE_CODE_PATH = '/api/copilot/oauth/device/code'
export const COPILOT_OAUTH_TOKEN_PATH = '/api/copilot/oauth/token'
export const COPILOT_OAUTH_VERIFY_PATH = '/api/copilot/oauth/verify'

export interface CopilotDeviceCodeResponse {
  deviceCode: string
  userCode: string
  verificationUri: string
  expiresIn: number
  interval: number
}

export interface CopilotTokenResponse {
  accessToken?: string
  tokenType?: string
  scope?: string
  error?: string
  errorDescription?: string
}

export interface CopilotVerifyResponse {
  login: string
  id?: number
  avatarUrl?: string
  htmlUrl?: string
}

export interface CopilotDiagnoseResult {
  githubUser?: { login?: string; id?: number }
  githubUserError?: string
  copilotBaseUrl?: string
  tokenError?: string
  tokenErrorKind?: string
  modelsUrl?: string
  modelsStatus?: number
  modelsError?: string
  modelsBodyPrefix?: string
}

export interface ChannelMetrics {
  channelIndex: number
  requestCount: number
  successCount: number
  failureCount: number
  successRate: number
  errorRate: number
  consecutiveFailures: number
  latency: number
  circuitState?: CircuitState
  circuitBrokenAt?: string
  nextRetryAt?: string
  halfOpenSuccesses?: number
  breakerFailureRate?: number
  lastSuccessAt?: string
  lastFailureAt?: string
  timeWindows?: {
    '15m': TimeWindowStats
    '1h': TimeWindowStats
    '6h': TimeWindowStats
    '24h': TimeWindowStats
  }
}

export interface DisabledKeyInfo {
  key: string
  reason: string
  message: string
  disabledAt: string
  recoverAt?: string
  config?: APIKeyConfig
}

export interface APIKeyConfig {
  key: string
  name?: string
  enabled?: boolean
  quotaGroup?: string
  rateLimitRpm?: number
  rateLimitWindowMinutes?: number
  rateLimitMaxConcurrent?: number
  rateLimitAutoFromHeaders?: boolean
  weight?: number
  models?: string[]
}

export interface UpstreamModelCapability {
  contextWindowTokens?: number
  maxOutputTokens?: number
  defaultOutputTokens?: number
  recommendedOutputTokens?: number
  thinkingMode?: string
  reasoningEfforts?: string[]
  provider?: string
  displayName?: string
  description?: string
  capabilities?: Record<string, boolean>
  pricing?: ModelPricing
  sources?: string[]
}

export interface ModelPricing {
  unit?: string
  currency?: string
  inputCacheHitPrice?: number
  inputCacheMissPrice?: number
  outputPrice?: number
  tiers?: ModelPricingTier[]
}

export interface ModelPricingTier {
  label?: string
  inputTokensAbove?: number
  inputTokensUpTo?: number
  inputCacheHitPrice?: number
  inputCacheMissPrice?: number
  outputPrice?: number
}

export interface Channel {
  name: string
  serviceType: 'openai' | 'gemini' | 'claude' | 'responses' | 'copilot'
  authHeader?: ChannelAuthHeader | ''
  baseUrl: string
  baseUrls?: string[]
  apiKeys: string[]
  apiKeyConfigs?: APIKeyConfig[]
  disabledApiKeys?: DisabledKeyInfo[]
  historicalApiKeys?: string[]
  description?: string
  website?: string
  insecureSkipVerify?: boolean
  modelMapping?: Record<string, string>
  modelCapabilities?: Record<string, UpstreamModelCapability>
  defaultCapability?: UpstreamModelCapability
  allowUnknownContext?: boolean
  reasoningMapping?: Record<string, 'none' | 'low' | 'medium' | 'high' | 'xhigh' | 'max'>
  reasoningParamStyle?: 'reasoning' | 'reasoning_effort' | 'thinking'
  textVerbosity?: 'low' | 'medium' | 'high' | ''
  fastMode?: boolean
  customHeaders?: Record<string, string>
  proxyUrl?: string
  requestTimeoutMs?: number
  responseHeaderTimeoutMs?: number
  streamFirstContentTimeoutMs?: number
  streamInactivityTimeoutMs?: number
  streamToolCallIdleTimeoutMs?: number
  routePrefix?: string
  autoBlacklistBalance?: boolean
  normalizeMetadataUserId?: boolean
  stripBillingHeader?: boolean
  stripEmptyTextBlocks?: boolean
  normalizeSystemRoleToTopLevel?: boolean
  codexNativeToolPassthrough?: boolean
  codexToolCompat?: boolean
  normalizeNonstandardChatRoles?: boolean
  stripCodexClientTools?: boolean
  stripImageGenerationTool?: boolean
  latency?: number
  status?: ChannelStatus | 'healthy' | 'error' | 'unknown' | ''
  index: number
  pinned?: boolean
  priority?: number
  metrics?: ChannelMetrics
  suspendReason?: string
  promotionUntil?: string
  latencyTestTime?: number
  lowQuality?: boolean
  injectDummyThoughtSignature?: boolean
  stripThoughtSignature?: boolean
  passbackReasoningContent?: boolean
  passbackThinkingBlocks?: boolean
  supportedModels?: string[]
  noVision?: boolean
  noVisionModels?: string[]
  visionFallbackModel?: string
  historicalImageTurnLimit?: number
  compactModel?: string
  // 主动限速（渠道级生产代理限速）
  rateLimitRpm?: number
  rateLimitWindowMinutes?: number
  rateLimitMaxConcurrent?: number
  rateLimitAutoFromHeaders?: boolean
  rpm?: number
}

export interface ChannelsResponse {
  channels: Channel[]
  current: number
}

export interface ChannelDashboardResponse {
  channels: Channel[]
  current?: number
  metrics: ChannelMetrics[]
  stats: SchedulerStatsResponse
  recentActivity?: ChannelRecentActivity[]
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

export interface CapabilityProtocolJobRef {
  jobId: string
  channelKind: 'messages' | 'chat' | 'gemini' | 'responses'
  channelId: number
}

export interface CapabilityTestJobStartResponse {
  jobId: string
  resumed?: boolean
  job?: CapabilityTestJob
}

export interface StartCapabilityTestOptions {
  targetProtocols?: string[]
  previousJobId?: string
  rpm?: number
  sourceTab?: string
  models?: string[]
}

export type CapabilityLifecycle = 'pending' | 'active' | 'done' | 'cancelled'
export type CapabilityOutcome = 'unknown' | 'success' | 'failed' | 'partial' | 'cancelled'
export type CapabilityRunMode = 'fresh' | 'reused_running' | 'resumed_cancelled' | 'cache_hit' | 'reused_previous_results'

export type CapabilityTestJobStatus = 'idle' | 'queued' | 'running' | 'completed' | 'failed' | 'cancelled'
export type CapabilityProtocolJobStatus = 'idle' | 'queued' | 'running' | 'completed' | 'failed'
export type CapabilityModelJobStatus = 'idle' | 'queued' | 'running' | 'success' | 'failed' | 'skipped'

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
  actualModel?: string
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
  protocolJobIds?: Record<string, string>
  protocolJobRefs?: Record<string, CapabilityProtocolJobRef>
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
  redirectTests?: RedirectModelResult[]
  compatibleProtocols: string[]
  totalDuration: number
  startedAt?: string
  updatedAt: string
  finishedAt?: string
  progress: CapabilityJobProgress
  error?: string
  cacheHit?: boolean
  targetProtocols?: string[]
  timeoutMilliseconds?: number
  snapshotUpdatedAt?: string
}

export interface RedirectModelResult {
  probeModel: string
  actualModel: string
  success: boolean
  latency: number
  streamingSupported?: boolean
  error?: string
  startedAt?: string
  testedAt: string
}

export interface CapabilitySnapshot {
  identityKey: string
  sourceType: string
  protocolJobIds?: Record<string, string>
  protocolJobRefs?: Record<string, CapabilityProtocolJobRef>
  tests: CapabilityProtocolJobResult[]
  compatibleProtocols: string[]
  totalDuration: number
  progress: CapabilityJobProgress
  lifecycle: CapabilityLifecycle
  outcome: CapabilityOutcome
  updatedAt: string
}

export interface ModelTestResult {
  model: string
  actualModel?: string
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

export interface CompatDiagnoseResult {
  recommendations: Partial<Record<string, boolean>>
  urlRecommendations?: {
    current: string
    recommended: string
    reason: string
  }
  evidence: Partial<Record<string, string>>
  duration: number
  cached: boolean
}

// ============== 历史/图表类型 ==============

export interface HistoryDataPoint {
  timestamp: string
  requestCount: number
  successCount: number
  failureCount: number
  successRate: number
  inputTokens?: number
  outputTokens?: number
  cacheCreationTokens?: number
  cacheReadTokens?: number
}

export interface MetricsHistoryResponse {
  channelIndex: number
  channelName: string
  dataPoints: HistoryDataPoint[]
  summary?: GlobalStatsSummary
}

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
  costUSD?: number
}

export interface KeyHistoryData {
  keyMask: string
  model?: string
  color: string
  dataPoints: KeyHistoryDataPoint[]
}

export interface ChannelKeyMetricsHistoryResponse {
  channelIndex: number
  channelName: string
  keys: KeyHistoryData[]
  summary?: GlobalStatsSummary
}

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
  costUSD?: number
}

export interface GlobalStatsSummary {
  totalRequests: number
  totalSuccess: number
  totalFailure: number
  totalInputTokens: number
  totalOutputTokens: number
  totalCacheCreationTokens: number
  totalCacheReadTokens: number
  totalCostUSD?: number
  avgSuccessRate: number
  duration: string
  intervalSeconds?: number
}

export interface GlobalStatsHistoryResponse {
  dataPoints: GlobalHistoryDataPoint[]
  summary: GlobalStatsSummary
  modelDataPoints?: Record<string, ModelHistoryDataPoint[]>
}

export interface ModelHistoryDataPoint {
  timestamp: string
  requestCount: number
  successCount: number
  failureCount: number
  inputTokens: number
  outputTokens: number
  cacheCreationTokens: number
  cacheReadTokens: number
  costUSD?: number
}

export interface ModelStatsHistoryResponse {
  models: Record<string, ModelHistoryDataPoint[]>
  duration: string
  interval: string
}

// ============== 日志类型 ==============

export interface ChannelLogEntry {
  requestId: string
  timestamp: string
  model: string
  originalModel?: string
  operation?: string
  originalReasoningEffort?: string
  actualReasoningEffort?: string
  statusCode: number
  durationMs: number
  success: boolean
  keyMask: string
  baseUrl: string
  errorInfo: string
  isRetry: boolean
  interfaceType?: string
  requestSource?: string
  status: string
  startTime: string
  connectedAt?: string
  firstByteAt?: string
  completedAt?: string

  // 代理上下文观测（subagent 识别）
  agentRole?: string         // main | subagent
  agentType?: string         // codex_subagent | claude_code_subagent
  parentThreadId?: string    // Codex parent thread id
  agentConfidence?: string   // exact | heuristic
  sessionId?: string         // 扁平化会话标识
}

export interface ChannelLogsResponse {
  channelIndex: number
  logs: ChannelLogEntry[]
}

// ============== 活跃度类型 ==============

export interface ActivitySegment {
  requestCount: number
  successCount: number
  failureCount: number
  inputTokens: number
  outputTokens: number
}

export interface ChannelRecentActivity {
  channelIndex: number
  segments: Record<number, ActivitySegment> | ActivitySegment[]
  totalSegs: number
  rpm: number
  tpm: number
}

/** 将稀疏 segments 展开为完整数组 */
export function expandSparseSegments(activity: ChannelRecentActivity, reuse?: ActivitySegment[]): ActivitySegment[] {
  const totalSegs = activity.totalSegs || 150

  if (Array.isArray(activity.segments)) {
    return activity.segments
  }

  let result: ActivitySegment[]
  if (reuse && reuse.length === totalSegs) {
    result = reuse
  } else {
    result = new Array(totalSegs)
    for (let i = 0; i < totalSegs; i++) {
      result[i] = { requestCount: 0, successCount: 0, failureCount: 0, inputTokens: 0, outputTokens: 0 }
    }
  }

  for (let i = 0; i < totalSegs; i++) {
    result[i].requestCount = 0
    result[i].successCount = 0
    result[i].failureCount = 0
    result[i].inputTokens = 0
    result[i].outputTokens = 0
  }

  if (activity.segments && typeof activity.segments === 'object') {
    for (const [indexStr, seg] of Object.entries(activity.segments)) {
      const index = parseInt(indexStr, 10)
      if (index >= 0 && index < totalSegs && seg) {
        result[index].requestCount = seg.requestCount
        result[index].successCount = seg.successCount
        result[index].failureCount = seg.failureCount
        result[index].inputTokens = seg.inputTokens
        result[index].outputTokens = seg.outputTokens
      }
    }
  }

  return result
}

// ============== 上游模型类型 ==============

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

// ============== 对话类型 ==============

export interface ChannelSequenceEntry {
  channelIndex: number
  channelName: string
}

export interface ConversationChannelInfo {
  index: number
  name: string
  priority: number
  status: string
  circuitOpen?: boolean
}

export interface SequenceOverrideInfo {
  sequence: ChannelSequenceEntry[]
  hasMainSequence?: boolean
  subagentSequence?: ChannelSequenceEntry[]  // subagent 专用序列（为空时 fallback 到 sequence）
  setAt: string
  expiresAt: string
  isPerpetual?: boolean
}

export interface ConversationInfo {
  id: string
  kind: 'messages' | 'responses' | 'chat' | 'gemini' | 'images' | 'vectors'
  userId: string
  rawUserId?: string
  title?: string
  currentChannel: number
  status: 'active' | 'streaming' | 'idle'
  models: string[]
  lastModel: string
  requestCount: number
  channelName: string
  lastRequestId: string
  lastUserMessage?: string
  lastRecap?: string
  lastRecapAt?: string
  createdAt: string
  lastActiveAt: string
  parentThreadId?: string
  parentConversationId?: string
  childConversationIds?: string[]

  // subagent 观测（仅展示）
  hasSubagents?: boolean
  subagentCount?: number
  mainChannel?: number
  subagentChannel?: number
}

export interface ConversationsResponse {
  conversations: ConversationInfo[]
  total: number
  channelsByKind?: Record<string, ConversationChannelInfo[]>
  overrides: Record<string, SequenceOverrideInfo>
}

// ============== 健康检查 ==============

export interface HealthResponse {
  version: string
  uptime: number
  mode: string
  timestamp: string
}
