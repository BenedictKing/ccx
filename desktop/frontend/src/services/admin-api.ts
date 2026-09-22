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
  userRequestCount?: number // 用户请求数（双口径：≠requestCount 时显示「X 请求 / Y 次尝试」）
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

export interface DisabledGroupModel {
  quotaGroup: string
  key?: string
  model: string
  note?: string
  disabledAt: string
}

export interface GroupModelDisableRequest {
  apiKey: string
  model: string
  note?: string
}

export interface GroupModelRestoreRequest {
  apiKey?: string
  quotaGroup?: string
  model: string
}

export interface GroupModelMutationResponse {
  success: boolean
  quotaGroup: string
  model: string
  affectedKeyCount: number
}

export interface APIKeyConfig {
  key: string
  keyUid?: string
  credentialUid?: string
  name?: string
  enabled?: boolean
  quotaGroup?: string
  groupMultiplier?: number | null
  consumptionPolicy?: 'normal' | 'opportunistic'
  maxGroupMultiplier?: number | null
  multiplierSource?: 'manual' | 'new_api' | 'provider'
  multiplierUpdatedAt?: string
  multiplierExpiresAt?: string
  multiplierSyncStatus?: 'manual' | 'fresh' | 'stale' | 'over_limit' | 'sync_error' | 'relink_required' | string
  multiplierSyncError?: string
  sourceSubscriptionUid?: string
  sourceRemoteTokenId?: number
  eligible?: boolean
  ineligibleReason?: string
  rateLimitRpm?: number
  rateLimitWindowMinutes?: number
  rateLimitMaxConcurrent?: number
  rateLimitAutoFromHeaders?: boolean
  weight?: number
  models?: string[]
  [key: string]: unknown
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
  /** 厂商文档已知的请求参数硬约束（如 Kimi 固定值采样参数），只读展示用，不参与前端逻辑。 */
  paramConstraints?: ModelParamConstraints
}

export interface ModelParamConstraints {
  fixedParams?: string[]
  toolChoiceRequiredUnsupported?: boolean
  thinkingFixedValue?: Record<string, unknown>
}

export interface ModelBenchmarkProfile {
  canonicalModel: string
  overallScore?: number
  categoryScores?: Record<string, number>
  benchmarkEvidence?: ModelBenchmarkEvidence[]
  sources?: string[]
  verifiedAt?: string
  lane?: 'provisional' | 'verified'
  sharedResults?: number
  comparableCategories?: number
  totalCategories?: number
}

export interface ModelBenchmarkEvidence {
  benchmark: string
  benchmarkVersion: string
  sourceModel: string
  domain: string
  metric: string
  rawValue: number
  uncertainty?: number
  cohortPercentile: number
  taskCount: number
  cohortSize: number
  effort: string
  selectionBasis: string
  sourceUrl: string
  capturedAt: string
}

export interface EmbeddingCapability {
  embeddingSpaceId?: string
  dimensions?: number
  supportedDimensions?: number[]
  normalized?: boolean
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
  disabledGroupModels?: DisabledGroupModel[]
  historicalApiKeys?: string[]
  description?: string
  remark?: string
  website?: string
  insecureSkipVerify?: boolean
  modelMapping?: Record<string, string>
  modelCapabilities?: Record<string, UpstreamModelCapability>
  embeddingCapabilities?: Record<string, EmbeddingCapability>
  defaultCapability?: UpstreamModelCapability
  allowUnknownContext?: boolean
  reasoningMapping?: Record<string, 'none' | 'minimal' | 'low' | 'medium' | 'high' | 'xhigh' | 'max'>
  reasoningParamStyle?: 'reasoning' | 'reasoning_effort' | 'thinking'
  textVerbosity?: 'low' | 'medium' | 'high' | ''
  fastMode?: boolean
  customHeaders?: Record<string, string>
  proxyUrl?: string
  proxyPreferDirect?: boolean           // 直连优先：配代理时先直连，失败（网络错误/451/403）自动回退代理
  racing?: { enabled?: boolean }        // 渠道级竞速参与开关（不参与=不做主触发也不做影子目标）
  costMultiplier?: number               // 渠道级充值倍率（EffectiveCost = ListCost × 倍率，0/空=不参与）
  maxGroupMultiplier?: number           // 渠道级最高分组倍率上限（Key 分组倍率超过则自动退出调度，0/空=不启用闸门）
  channelPaymentCurrency?: string       // 充值币种（如 LDC/CNY/USD）
  channelPaymentAmount?: number         // 充值金额（0/空=不参与）
  channelCreditCurrency?: string        // 渠道显示/计价币种（如 USD）
  channelCreditAmount?: number          // 渠道到账金额（0/空=不参与）
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
  // 托管渠道身份（autopilot 自动托管渠道）
  accountUid?: string
  channelUid?: string
  logicalChannelUid?: string // 逻辑渠道 UID：同站点多协议渠道共享
  providerId?: string
  autoManaged?: boolean
  autoManagedAt?: string
  autoManagedKind?: string
  subscriptionUid?: string
  originType?: string
  originTier?: string
  tags?: string[]
  // 逻辑渠道聚合后的协议路由（后端 /api/logical-channels/dashboard 返回）
  protocolRoutes?: ChannelProtocolRoute[]
  protocolCapsules?: ChannelProtocolCapsule[]
}

export interface ChannelProtocolRoute {
  kind: string
  index: number
  name: string
  serviceType: string
  channelUid: string
  status: string
  apiKeys: string[]
}

export interface ChannelProtocolCapsule {
  kind: string
  label: string
  serviceType: string
  channelUid: string
  index: number
  status: string
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
export type ImageGenerationProbeState = 'supported' | 'unsupported' | 'inconclusive'

export interface CodexImageGenerationKeyProbeResult {
  keyMask: string
  hostedTool: ImageGenerationProbeState
  namespaceTool: ImageGenerationProbeState
  status: ImageGenerationProbeState
}

export interface CodexImageGenerationProbeSummary {
  tested: boolean
  supported: boolean
  compatibleViaStrip?: boolean
  actualModel: string
  supportedKeys: number
  unsupportedKeys: number
  inconclusiveKeys: number
  keyResults?: CodexImageGenerationKeyProbeResult[]
}

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
  codexImageGeneration?: CodexImageGenerationProbeSummary
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
  codexImageGeneration?: CodexImageGenerationProbeSummary
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
  codexImageGeneration?: CodexImageGenerationProbeSummary
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

export type ChannelDiscoveryKind = 'messages' | 'chat' | 'gemini' | 'responses'
export type ChannelDiscoveryTargetClient = 'codex' | 'claude-code' | 'claude'

export interface ChannelDiscoveryRequest {
  channelKind?: ChannelDiscoveryKind
  serviceType: Channel['serviceType'] | ''
  baseUrl?: string
  baseUrls?: string[]
  apiKey: string
  authHeader?: ChannelAuthHeader | ''
  customHeaders?: Record<string, string>
  proxyUrl?: string
  insecureSkipVerify?: boolean
  modelMapping?: Record<string, string>
  reasoningMapping?: Record<string, string>
  targetClients?: ChannelDiscoveryTargetClient[]
}

export interface DiscoverySelectedModels {
  strong?: string
  primary?: string
  fast?: string
}

export interface DiscoveryModelsResult {
  source: string
  url?: string
  statusCode?: number
  items: string[]
  selected: DiscoverySelectedModels
  warnings?: string[]
}

export interface DiscoveryProtocolResult {
  protocol: ChannelDiscoveryKind
  success: boolean
  successModels?: string[]
  failedModels?: string[]
  latencyMs?: number
  error?: string
}

export interface DiscoveryCapabilityProbeResult {
  tested: boolean
  supported: boolean
  required?: boolean
  statusCode?: number
  evidence?: string
  error?: string
  recommendation?: Partial<Record<string, boolean>>
}

export interface DiscoveryCapabilitiesResult {
  toolCalls: DiscoveryCapabilityProbeResult
  vision: DiscoveryCapabilityProbeResult
  imageGeneration: DiscoveryCapabilityProbeResult
  thinkingPassback: DiscoveryCapabilityProbeResult
}

export interface DiscoveryEvidence {
  type: string
  key?: string
  message: string
}

export interface ChannelDiscoveryRecommendation {
  channelKind: ChannelDiscoveryKind | ''
  serviceType: Channel['serviceType'] | ''
  baseUrls?: string[]
  modelMapping: Record<string, string>
  reasoningMapping?: Record<string, string>
  supportedModels?: string[]
  noVisionModels?: string[]
  visionFallbackModel?: string
  compat?: Partial<Record<string, boolean>>
  urlRecommendation?: {
    current: string
    recommended: string
    reason: string
  } | null
  evidence?: DiscoveryEvidence[]
}

export interface DiscoveryRateLimitResult {
  initialRpm: number
  effectiveRpm: number
  rateLimited: boolean
  rateLimitedCount?: number
}

export interface ChannelDiscoveryResponse {
  models: DiscoveryModelsResult
  protocols: DiscoveryProtocolResult[]
  capabilities: DiscoveryCapabilitiesResult
  recommendation: ChannelDiscoveryRecommendation
  rateLimit: DiscoveryRateLimitResult
  evidence?: DiscoveryEvidence[]
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
  selectionReason?: string
  selectionTraceSummary?: string
  racingRole?: string
  racingStatus?: string    // won | lost
  status: string  // pending/connecting/first_byte/streaming/completed/failed/cancelled/racing_lost
  startTime: string
  connectedAt?: string
  firstByteAt?: string
  completedAt?: string
  firstContentLatencyMs?: number
  maxStreamIdleMs?: number
  maxToolCallIdleMs?: number

  // 代理上下文观测（subagent 识别）
  agentRole?: string         // main | subagent
  agentType?: string         // codex_subagent | claude_code_subagent
  parentThreadId?: string    // Codex parent thread id
  agentConfidence?: string   // exact | heuristic
  sessionId?: string         // 扁平化会话标识

  // Autopilot trace 关联
  autopilotTraceUid?: string
  requestCorrelationId?: string
}

// 熔断成因依据：渠道日志是后端内存态，重启即清空，而熔断状态持久化恢复。
// 日志为空且渠道非 closed 时后端返回该字段，用于解释熔断来源而非留下黑盒。
export interface ChannelBreakerEvidence {
  circuitState: 'open' | 'half_open' | 'closed'
  lastFailureAt?: string
  circuitBrokenAt?: string
  nextRetryAt?: string
  backoffLevel: number
  consecutiveFailures: number
  // true 表示熔断依据的最近失败早于本次进程启动，即日志已随重启清空
  predatesRestart: boolean
  processStartedAt: string
}

export interface ChannelLogsResponse {
  channelIndex: number
  logs: ChannelLogEntry[]
  breakerEvidence?: ChannelBreakerEvidence
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

// ============== 健康中心（Health Center）==============

export type HealthState = 'unknown' | 'healthy' | 'degraded' | 'limited' | 'misconfigured' | 'dead'

export interface HealthCenterOverview {
  totalChannels: number
  totalEndpoints: number
  stateCounts: Record<HealthState, number>
}

export interface ChannelHealthItem {
  channelUid: string
  channelId: number
  channelKind: string
  channelName?: string
  aggState: HealthState
  endpointCount: number
  healthyCount: number
  degradedCount: number
  limitedCount: number
  deadCount: number
  unknownCount: number
  avgSuccessRate?: number
  speedTier?: 'fast' | 'normal' | 'slow'
  connectSampleCount?: number
  p95ConnectLatencyMs?: number
  originTier?: 'first' | 'second' | 'third' | 'unknown'
  poolTag?: 'free' | 'temp' | ''
}

export interface HealthCenterChannelsResponse {
  channels: ChannelHealthItem[]
}

export interface EndpointDetailItem {
  endpointUid: string
  channelUid: string
  channelKind: string
  baseUrl: string
  keyHash: string
  healthState: HealthState
  healthConfidence: number
  healthEvidence?: string
  suggestedAction?: string
  qualityTier?: string
  stabilityTier?: string
  speedTier?: string
  successRate15m?: number
  successRate1h?: number
  p95LatencyMs?: number
  connectSampleCount?: number
  p95ConnectLatencyMs?: number
  firstByteSampleCount?: number
  p95FirstByteLatencyMs?: number
  consecutiveFail: number
  lastSuccessAt?: string
  updatedAt?: string
}

export interface HealthCenterEndpointsResponse {
  channelUid: string
  endpoints: EndpointDetailItem[]
}

export type ProfileChangeEventType =
  | 'profile_updated'
  | 'health_changed'
  | 'discovery_completed'
  | 'auto_mapping_applied'

export interface ProfileChangeEvent {
  eventUid: string
  channelUid: string
  channelKind: string
  endpointUid?: string
  metricsKey?: string
  eventType: ProfileChangeEventType
  summary: string
  oldValue?: string
  newValue?: string
  createdAt: string
}

export interface ProfileChangelogResponse {
  events: ProfileChangeEvent[]
  total: number
}

// ============== 汇率、倍率与 NewAPI Key 状态类型 ==============

export type ChannelKind = 'messages' | 'chat' | 'responses' | 'gemini' | 'images' | 'vectors'

export interface KeyMultiplierPatch {
  groupMultiplier?: number | null
  maxGroupMultiplier?: number | null
  consumptionPolicy?: 'normal' | 'opportunistic' | null
}

export interface KeyMultiplierResponse {
  keyUid: string
  group: string
  remoteMultiplier?: number | null
  groupMultiplier?: number | null
  maxMultiplier?: number | null
  consumptionPolicy?: 'normal' | 'opportunistic'
  effectiveCostClass?: 'zero' | 'discounted' | 'standard' | 'premium' | 'unknown'
  status: string
  reason: string
  eligible: boolean
  updatedAt?: string
  expiresAt?: string
}

export interface BillingTermsPatch {
  paymentAmount: number | null
  paymentUnit: string
  creditAmount: number | null
  creditUnit: string
  expectedVersion?: number
}

export interface BillingTermsResponse {
  paymentAmount?: number | null
  paymentUnit?: string
  creditAmount?: number | null
  creditUnit?: string
  version: number
  preview: string
}

export interface ExchangeRateQuote {
  sourceAmount: number
  sourceUnit: string
  targetAmount: number
  targetUnit: string
  updatedAt?: string
  note?: string
}

export interface ExchangeRateSnapshot {
  version: number
  usdUnitPrices: Record<string, number>
  builtAt: string
}

export interface ExchangeRatesResponse {
  quotes: ExchangeRateQuote[]
  snapshot?: ExchangeRateSnapshot
  source?: string
}

export interface ExchangeRatesReplaceRequest {
  quotes: ExchangeRateQuote[]
  expectedSnapshotVersion?: number
}

export interface ExchangeRatesReplaceResponse {
  quotes: ExchangeRateQuote[]
  snapshot: ExchangeRateSnapshot
  source?: string
  version: number
}

export interface NewApiKeyStatus {
  keyUid?: string
  name: string
  group: string
  groupMultiplier: number
  maxGroupMultiplier: number
  sourceRemoteTokenId: number
  syncStatus: string
  multiplierExpiresAt?: string
  updatedAt?: string
  reason?: string
}

export interface NewApiSyncResult {
  subscriptionUid: string
  success: boolean
  balance?: number
  models?: string[]
  modelsHash?: string
  modelsHashChanged: boolean
  keys: NewApiKeyStatus[]
  discoveryTriggered: boolean
  failedReason?: string
}

export interface SubscriptionRefreshResponse {
  subscription: SubscriptionItem
  refreshResult: NewApiSyncResult | {
    success: boolean
    balance?: number
    currency?: string
    errorMessage?: string
  }
}

// ============== 订阅中心（Subscription Center）==============

export interface SubscriptionItem {
  subscriptionUid: string
  displayName: string
  provider?: string
  originType?: string
  originTier?: string
  billingMode?: string
  currency?: string
  balance?: number
  paymentAmount?: number | null
  paymentUnit?: string
  creditAmount?: number | null
  creditUnit?: string
  version: number
  groupMultipliers?: Record<string, number>
  linkedChannelUids?: string[]
  source?: string
  confidence?: number
  notes?: string
  createdAt: string
  updatedAt: string
  archivedAt?: string
  billingApiKey?: string
  autoRefreshEnabled?: boolean
  autoRefreshSupported?: boolean
  lastBalanceRefreshAt?: string
  lastBalanceRefreshError?: string
  baseUrl?: string
  proxyUrl?: string
  proxyPreferDirect?: boolean
  accessTokenMasked?: string
  userId?: string
  authTokenMode?: 'bearer' | 'raw_auth' | string
  provisionKeyName?: string
  provisionGroup?: string
  provisionGroupRatio?: number | null
  maxGroupMultiplier?: number | null
  provisionModels?: string[]
  provisionedTokenId?: number
  provisionedKeys?: NewApiProvisionedKey[]
  availableModels?: string[]
}

export interface SubscriptionsListResponse {
  subscriptions: SubscriptionItem[]
  total: number
}

export interface SubscriptionCreateRequest {
  subscriptionUid: string
  displayName: string
  provider?: string
  originType?: string
  originTier?: string
  billingMode?: string
  currency?: string
  balance?: number
  paymentAmount?: number | null
  paymentUnit?: string
  creditAmount?: number | null
  creditUnit?: string
  groupMultipliers?: Record<string, number>
  notes?: string
  source?: string
  billingApiKey?: string
  autoRefreshEnabled?: boolean
}

export interface SubscriptionUpdateRequest {
  displayName?: string
  provider?: string
  originType?: string
  originTier?: string
  billingMode?: string
  currency?: string
  balance?: number
  paymentAmount?: number | null
  paymentUnit?: string
  creditAmount?: number | null
  creditUnit?: string
  groupMultipliers?: Record<string, number>
  notes?: string
  source?: string
  confidence?: number
  billingApiKey?: string
  autoRefreshEnabled?: boolean
  expectedVersion?: number
}

export interface NewApiVerifyRequest {
  baseUrl: string
  accessToken: string
  userId?: string
  authTokenMode?: string
  displayName?: string
  subscriptionUid?: string
  proxyUrl?: string
  proxyPreferDirect?: boolean
}

export interface NewApiVerifyResponse {
  username: string
  userId: number
  quota: number
  usedQuota: number
  groups: Record<string, number>
  groupFetchError?: string
  availableModels: string[]
  suggestedOriginType: string
  suggestedOriginTier: string
  accessTokenMasked: string
}

export interface NewApiProvisionedKey {
  name: string
  group: string
  groupMultiplier: number
  tokenId: number
  keyUid?: string
  reused?: boolean
}

export interface NewApiProvisionRequest {
  subscriptionUid: string
  displayName: string
  baseUrl: string
  accessToken: string
  channelKind: ChannelKind
  userId?: string
  authTokenMode?: 'bearer' | 'raw_auth' | string
  proxyUrl?: string
  proxyPreferDirect?: boolean
  channelName?: string
  provisionKeyName?: string
  provisionGroup?: string
  provisionAllEligibleGroups?: boolean
  maxGroupMultiplier?: number
  provisionModels?: string[]
  notes?: string
}

export interface NewApiProvisionResponse {
  subscription: SubscriptionItem
  channelUid: string
  channelIndex: number
  channelName?: string
  mergedChannel?: boolean
  provisionedKey: string
  provisionedTokenId: number
  provisionedKeys?: NewApiProvisionedKey[]
  reused: boolean
  discoveryStarted: boolean
}

export interface NewApiAccountCreateRequest {
  accessToken: string
  userId?: string
  displayName?: string
  authTokenMode?: string
  provisionModels?: string[]
  maxGroupMultiplier?: number
  provisionAllEligibleGroups?: boolean
}

export interface NewApiCredentialsUpdateRequest {
  accessToken?: string
  userId?: string
  authTokenMode?: string
  expectedVersion?: number
}

export interface NewApiAccountItem {
  accountUid: string
  userId?: string
  displayName?: string
  balance?: number
  status?: string
  accessTokenMasked?: string
  provisionedKeys?: NewApiProvisionedKey[]
  lastSyncError?: string
  lastCheckedAt?: string
  createdAt: string
}

export interface NewApiAccountListResponse {
  accounts: NewApiAccountItem[]
}

// ============== 驾驶舱（Cockpit）类型 ==============

export interface CockpitHealthSummary {
  totalChannels: number
  totalEndpoints: number
  stateCounts: Record<string, number>
}

export interface CockpitSubscriptionSummary {
  total: number
  balanceByCode: Record<string, number>
  countByMode: Record<string, number>
  countByTier: Record<string, number>
}

export interface CockpitLocalRuntimeSummary {
  total: number
  statusCounts: Record<string, number>
  totalModels: number
}

export interface CockpitManualIntentSummary {
  activeCount: number
  totalCount: number
}

export interface CockpitTodoItem {
  endpointUid: string
  channelUid: string
  channelKind: string
  baseUrl: string
  healthState: string
  suggestedAction: string
}

export interface CockpitOverviewResponse {
  health: CockpitHealthSummary
  subscriptions: CockpitSubscriptionSummary
  localRuntimes: CockpitLocalRuntimeSummary
  manualIntents: CockpitManualIntentSummary
  todoItems: CockpitTodoItem[]
}

// ============== 渠道推荐类型 ==============

export interface ChannelRecommendation {
  proxyKeyMask: string
  domain: string
  domainUsageCount: number
  currentChannelUid: string
  currentScore: number
  recommendedChannelUid: string
  recommendedScore: number
  scoreDelta: number
  reason: string
}

export interface RecommendationsResponse {
  proxyKeyMask?: string
  recommendations: ChannelRecommendation[]
}

// ============== 人工路由意图（试用意图）类型 ==============

/** 人工路由意图的类型 */
export type ManualIntentType = 'model_trial' | 'channel_trial' | 'endpoint_trial' | 'session_pin'

/** 人工路由意图的生命周期状态 */
export type ManualIntentStatus = 'active' | 'expired' | 'exhausted' | 'disabled'

/** 意图作用范围的任务类别 */
export type ManualIntentTaskClass =
  | 'supervisor'
  | 'worker'
  | 'lightweight'
  | 'vision'
  | 'long_context'
  | 'image_generation'
  | 'embedding'

/** 试用结果统计（Phase 1 shadow：仅记录统计，不影响真实调度） */
export interface TrialResult {
  hitCount: number
  successCount: number
  failureCount: number
  totalLatencyMs?: number
  avgLatencyMs: number
  fallbackCount?: number
  estimatedCost?: number
}

/** POST /api/manual-intents 请求体 */
export interface CreateIntentRequest {
  name?: string
  intentType: ManualIntentType
  channelKind: string
  channelUid?: string
  metricsKey?: string
  model?: string
  mappedModel?: string
  agentRoles?: string[]
  taskClasses?: ManualIntentTaskClass[]
  sessionId?: string
  trafficPercent?: number
  expiresAt?: string
  ttlMinutes?: number
  maxRequests?: number
  maxEstimatedCost?: number
  fallbackOnFailure?: boolean
  requireHardConstraints: boolean
  createdBy?: string
  reason?: string
}

/** 人工路由意图（试用意图）完整记录 */
export interface ManualRoutingIntent {
  intentUid: string
  name?: string
  intentType: ManualIntentType
  channelKind: string
  channelUid?: string
  metricsKey?: string
  model?: string
  mappedModel?: string
  agentRoles?: string[]
  taskClasses?: ManualIntentTaskClass[]
  sessionId?: string
  trafficPercent?: number
  expiresAt: string
  maxRequests?: number
  maxEstimatedCost?: number
  fallbackOnFailure?: boolean
  requireHardConstraints: boolean
  createdBy?: string
  createdAt: string
  reason?: string
  status: ManualIntentStatus
  trialResult: TrialResult
}

/** GET /api/manual-intents 列表响应 */
export interface IntentListResponse {
  intents: ManualRoutingIntent[]
  total: number
}

// ============== Autopilot 智能路由类型 ==============

export type AutopilotMode = 'off' | 'shadow' | 'assist' | 'auto'

export interface SmartRoutingConfig {
  mode: AutopilotMode
  killSwitchActive: boolean
  costPreference: string
  racingEnabled?: boolean   // 竞速模式总开关（随整卡 PUT /smart-routing/config 保存）
  l2ProbeEnabled?: boolean
  readiness?: AutoReadinessReport
}

export interface RoutingWindowSummary {
  requestCount: number
  successRate: number
  fallbackRate: number
  failOpenRate: number
  p95LatencyMs: number
  p95FirstByteLatencyMs: number
}

export interface AutoSafetyEvent {
  eventUid: string
  fromMode: string
  toMode: string
  reasons: string[]
  createdAt: string
}

export interface AutoReadinessReport {
  ready: boolean
  requiredSamples: number
  requiredObservationHours: number
  observationHours: number
  blockingReasons: string[]
  safeModeMetrics: RoutingWindowSummary
  recentMetrics: RoutingWindowSummary
  baselineMetrics: RoutingWindowSummary
  lastRollback?: AutoSafetyEvent
}

export interface CandidateScore {
  dimension: string
  score: number
  weight: number
}

export interface DomainStrengthEvidence {
  source: 'endpoint_override' | 'canonical_benchmark' | 'family_seed' | 'neutral'
  score: number
  canonicalCeiling?: number
  providerQualityFactor?: number
  canonicalModel?: string
  benchmarkCategory?: string
  benchmarkSources?: string[]
  benchmarkVerifiedAt?: string
  benchmarkLane?: string
  evidenceConfidence?: number
}

export interface RoutingCandidate {
  channelUid: string
  channelName?: string
  candidateKey?: string    // 五元组标识（v3）：channelUID|protocol|keyIdentity|model|effort；v2 前为二元组 channelUID|model
  metricsKey?: string
  keyMask?: string
  originTier?: string
  channelKind?: string
  executionKind?: string
  protocolFidelity?: string
  conversionPenalty?: number
  healthState?: string
  mappedModel?: string
  mappingSource?: string
  mappingReason?: string
  actualModel?: string     // v3：候选行实际发送模型（五元组模型维）
  keyIdentity?: string     // v3：候选行 key 身份（KeyUID 或 kh_ 哈希前缀）
  quotaGroup?: string      // v3：key 分组
  effort?: string          // v3：思考档位（空 = passthrough）
  baseQualityTier?: string
  effortQualityTier?: string
  effortQualityScore?: number
  effortEvidenceClass?: string
  effortQualityKnown?: boolean
  effortAwareTotalScore?: number
  qualityConfidence?: number
  qualityDiscount?: number
  qualityDiscountReason?: string
  totalScore: number
  scores?: CandidateScore[]
  domainEvidence?: DomainStrengthEvidence
  selected: boolean
  filterReasons?: string[]
}

export interface RoutingDecisionTrace {
  traceUid: string
  requestKind: string
  taskClass: string
  taskDomain?: string
  requestedModel?: string
  agentRole?: string
  candidates: RoutingCandidate[]
  candidatesBefore: number
  candidatesAfter: number
  globalFilterReasons?: Record<string, string[]>
  sortReasons?: string[]
  selectedChannelUid?: string
  selectedMetricsKey?: string
  selectedOriginTier?: string
  estimatedCost?: number
  costConfidence?: number
  fallbackUsed: boolean
  shadowChannelUid?: string
  actualChannelUid?: string
  match: boolean
  outcomeRecorded?: boolean
  outcome?: 'success' | 'upstream_error' | 'exhausted' | 'cancelled' | 'attempt_failed'
  success?: boolean
  channelFallback?: boolean
  statusCode?: number
  requestDurationMs?: number
  firstByteLatencyMs?: number
  completedAt?: string
  mode: 'off' | 'shadow' | 'assist' | 'auto' | 'active' | 'dry_run'
  durationMs: number
  createdAt: string
}

export interface AutopilotTraceListResponse {
  traces: TraceSummary[]
  total: number
  partial?: boolean
  hasMore: boolean
}

export interface AutopilotTraceStats {
  totalCount: number
  comparedCount: number
  matchedCount?: number
  mismatchCount: number
  uncomparedCount?: number
  successCount?: number
  failOpenCount?: number
  mismatchRate: number
  taskClassDist: Record<string, number>
  modeDist: Record<string, number>
}

// ============== Trace v2 类型 ==============

export type ComparisonStatus = 'matched' | 'mismatched' | 'uncompared'
export type Cohort = 'treatment' | 'control' | 'bypass'

/** 列表摘要（v2） */
export interface TraceSummary {
  traceUid: string
  schemaVersion: number
  createdAt: string
  releaseId?: string
  cohort?: Cohort
  mode: string
  requestKind: string
  taskClass: string
  taskDomain?: string
  requestedModel?: string
  actualModel?: string
  actualEffort?: string
  comparisonStatus: ComparisonStatus
  recommendedChannelUid?: string
  actualChannelUid?: string
  outcome?: string
  success?: boolean
  requestDurationMs?: number
  historicalSchema?: boolean
}

/** Scheduler 裁决摘要 */
export interface SchedulerDecisionSummary {
  stages?: { name: string; count: number }[]
  skipReasons?: string[]
  skippedCandidates?: SkippedCandidateSummary[]
  selectedUid?: string
  selectedName?: string
  selectionCode?: string
}

/** scheduler 阶段被过滤渠道的明细 */
export interface SkippedCandidateSummary {
  channelIndex: number
  channelName: string
  stage: string
  reason: string
  details?: string
}

/** endpoint 尝试摘要 */
export interface EndpointAttemptSummary {
  attemptUid: string
  attemptSeq: number
  status: string
  channelUid: string
  endpointLabel: string
  actualModel?: string
  actualEffort?: string
  result: string
  statusCode?: number
  durationMs?: number
}

/** trace 详情（v2） */
export interface TraceDetailV2 {
  traceUid: string
  schemaVersion: number
  traceRevision?: number
  createdAt: string
  requestCorrelationId?: string
  source?: string
  releaseId?: string
  policyFingerprint?: string
  targetMode?: string
  effectiveMode?: string
  cohort?: Cohort
  bypassReason?: string
  persistenceClass?: string
  comparisonStatus: ComparisonStatus
  requestKind: string
  taskClass: string
  taskDomain?: string
  requestedModel?: string
  actualModel?: string
  actualEffort?: string
  agentRole?: string
  manualIntentUid?: string
  advisorDecisionUid?: string
  candidates?: RoutingCandidate[]
  candidatesBefore: number
  candidatesAfter: number
  globalFilterReasons?: Record<string, string[]>
  sortReasons?: string[]
  recommendedChannelUid?: string
  selectedChannelUid?: string
  estimatedCost?: number
  costConfidence?: number
  fallbackUsed: boolean
  schedulerDecision?: SchedulerDecisionSummary
  endpointAttempts?: EndpointAttemptSummary[]
  attemptsTruncated?: boolean
  attemptsTotal?: number
  attemptsByResult?: Record<string, number>
  outcome?: string
  success?: boolean
  channelFallback?: boolean
  statusCode?: number
  requestDurationMs?: number
  firstByteLatencyMs?: number
  completedAt?: string
  durationMs: number
  historicalSchema?: boolean
}

export interface AutopilotTraceDetailResponse {
  trace: TraceDetailV2
}

// ============== 成本报表（Phase 4 Item 2） ==============

export interface CostReportRow {
  groupKey: string
  totalRequests: number
  successCount: number
  inputTokens: number
  outputTokens: number
  cacheCreationTokens: number
  cacheReadTokens: number
  listCostUSD: number
  effectiveCostUSD: number
  pricingComplete?: boolean
  unpricedModels?: string[]
  zeroCostCount?: number
  configuredMultiplierCount?: number
  subscriptionCostCount?: number
  unpricedCostCount?: number
  // 请求侧压缩统计（RTK 模式）
  compressedRequests?: number
  originalTokensSaved?: number
  compressedTokensAfter?: number
  compressionFallbackCount?: number
  compressionSavingsPct?: number
}

export interface CostReportResponse {
  groupBy: 'user' | 'model' | 'key'
  apiType: string
  duration: string
  rows: CostReportRow[]
}

// ============== 智能路由诊断 / 路由预演 ==============

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
  candidateKey?: string
  mappedModel?: string
  mappingSource?: string
  mappingReason?: string
  actualModel?: string
  keyIdentity?: string
  quotaGroup?: string
  effort?: string
  baseQualityTier?: string
  effortQualityTier?: string
  effortQualityScore?: number
  effortEvidenceClass?: string
  effortQualityKnown?: boolean
  effortAwareTotalScore?: number
  qualityConfidence?: number
  qualityDiscount?: number
  qualityDiscountReason?: string
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

// 候选按 LogicalChannel 聚合的诊断视图；仅用于展示，不改变真实调度。
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

// ============== Admin API 端点路径常量 ==============

export const HEALTH_CENTER_OVERVIEW_PATH = '/api/health-center/overview'
export const HEALTH_CENTER_CHANNELS_PATH = '/api/health-center/channels'
export const healthCenterChannelEndpointsPath = (channelUid: string) =>
  `/api/health-center/channels/${encodeURIComponent(channelUid)}/endpoints`
export const HEALTH_CENTER_CHANGELOG_PATH = '/api/health-center/changelog'
export const HEALTH_CENTER_EVENTS_WS_PATH = '/api/health-center/events'

export const SUBSCRIPTIONS_PATH = '/api/subscriptions'
export const subscriptionPath = (uid: string) => `/api/subscriptions/${encodeURIComponent(uid)}`
export const subscriptionBillingTermsPath = (uid: string) => `${subscriptionPath(uid)}/billing-terms`
export const subscriptionRefreshPath = (uid: string) => `${subscriptionPath(uid)}/refresh`
export const groupModelDisablePath = (kind: ChannelKind, channelId: string | number) =>
  `/api/${kind}/channels/${encodeURIComponent(String(channelId))}/keys/group-model/disable`
export const groupModelRestorePath = (kind: ChannelKind, channelId: string | number) =>
  `/api/${kind}/channels/${encodeURIComponent(String(channelId))}/keys/group-model/restore`
export const keyMultiplierPath = (kind: ChannelKind, channelUid: string, keyUid: string) =>
  `/api/${kind}/channels/${encodeURIComponent(channelUid)}/keys/${encodeURIComponent(keyUid)}/multiplier`
export const EXCHANGE_RATES_PATH = '/api/autopilot/cost/exchange-rates'
export const NEWAPI_VERIFY_PATH = '/api/subscriptions/newapi/verify'
export const NEWAPI_PROVISION_PATH = '/api/subscriptions/newapi/provision'
export const subscriptionNewApiCredentialsPath = (uid: string) => `${subscriptionPath(uid)}/newapi-credentials`
export const subscriptionAccountsPath = (uid: string) => `${subscriptionPath(uid)}/accounts`
export const subscriptionAccountPath = (uid: string, accountUid: string) =>
  `${subscriptionAccountsPath(uid)}/${encodeURIComponent(accountUid)}`
export const subscriptionAccountRefreshPath = (uid: string, accountUid: string) =>
  `${subscriptionAccountPath(uid, accountUid)}/refresh`

export const COCKPIT_OVERVIEW_PATH = '/api/cockpit/overview'

export const MANUAL_INTENTS_PATH = '/api/manual-intents'
export const manualIntentPath = (uid: string) => `/api/manual-intents/${encodeURIComponent(uid)}`

export const AUTOPILOT_RECOMMENDATIONS_PATH = '/api/autopilot/recommendations'
export const SMART_ROUTING_CONFIG_PATH = '/api/smart-routing/config'
export const AUTOPILOT_TRACES_PATH = '/api/traces'
export const AUTOPILOT_TRACE_STATS_PATH = '/api/traces/stats'

// ============== 逻辑渠道（Logical Channel）类型 ==============

export interface LogicalChannelProtocol {
  kind: string // messages / chat / responses / gemini / images / vectors
  channelUid: string
  serviceType: string // claude / openai / responses / gemini
  enabled: boolean
  status: string
  priority: number
  routePrefix: string
}

export type LogicalChannelKind = 'llm' | 'embeddings' | 'images'

export interface LogicalChannel {
  logicalChannelUid: string
  accountUid?: string
  providerId?: string
  name: string
  remark?: string
  description?: string
  website?: string
  kind: LogicalChannelKind
  baseUrls: string[]
  siteIdentity: string
  protocols: LogicalChannelProtocol[]
  tags?: string[]
  createdAt: string
  updatedAt: string
}

export interface LogicalChannelsResponse {
  logicalChannels: LogicalChannel[]
}

export interface CreateLogicalChannelProtocol {
  kind: string
  serviceType: string
  apiKeys?: string[]
  apiKeyConfigs?: APIKeyConfig[]
  baseUrls?: string[]
  baseUrl?: string
  modelMapping?: Record<string, string>
  reasoningMapping?: Record<string, string>
  priority?: number
  enabled?: boolean
  status?: string
  routePrefix?: string
  supportedModels?: string[]
  customHeaders?: Record<string, string>
  proxyUrl?: string
}

export interface CreateLogicalChannelRequest {
  name: string
  remark?: string
  description?: string
  website?: string
  providerId?: string
  accountUid?: string
  kind?: LogicalChannelKind
  baseUrls?: string[]
  apiKeys?: string[]
  tags?: string[]
  protocols: CreateLogicalChannelProtocol[]
  placement?: string
}

export interface UpdateLogicalChannelCommon {
  name?: string
  remark?: string
  description?: string
  website?: string
  tags?: string[]
  baseUrls?: string[]
}

export interface UpdateLogicalChannelProtocol {
  kind: string
  serviceType?: string
  apiKeys?: string[]
  apiKeyConfigs?: APIKeyConfig[]
  baseUrls?: string[]
  baseUrl?: string
  modelMapping?: Record<string, string>
  reasoningMapping?: Record<string, string>
  priority?: number
  enabled?: boolean
  status?: string
  routePrefix?: string
  supportedModels?: string[]
  customHeaders?: Record<string, string>
  proxyUrl?: string
}

export interface UpdateLogicalChannelRequest {
  common?: UpdateLogicalChannelCommon
  protocols?: UpdateLogicalChannelProtocol[]
  removals?: string[]
  placement?: string
}

export interface DeleteLogicalChannelResponse {
  message: string
  logicalChannelUid: string
  removedChannels: string[]
}

export const LOGICAL_CHANNELS_PATH = '/api/logical-channels'
export const logicalChannelPath = (uid: string) => `/api/logical-channels/${encodeURIComponent(uid)}`
export const LOGICAL_CHANNELS_DASHBOARD_PATH = '/api/logical-channels/dashboard'

// ============== 新增端点路径常量（对齐 web 端能力） ==============

/** 成本报表（Phase 4 Item 2） */
export const COST_REPORT_PATH = '/api/reports/cost'

/** 智能路由 dry-run 诊断 */
export const SMART_ROUTING_DIAGNOSE_PATH = '/api/smart-routing/diagnose'

/** 路由预演（请求体直喂 + 两层对齐） */
export const ROUTE_PREVIEW_PATH = '/api/autopilot/route-preview'

/** 单条路由追踪详情 */
export const autopilotTracePath = (traceUid: string) => `/api/traces/${encodeURIComponent(traceUid)}`

/** 渠道级竞速独立端点（脚本直调用；UI 随 /smart-routing/config 整卡保存） */
export const RACING_CONFIG_PATH = '/api/racing/config'

/** 调度器选路 dry-run（按协议注册于 /api/{kind}/channels/scheduler/diagnose） */
export const schedulerDiagnosePath = (kind: ChannelKind) =>
  `/api/${kind}/channels/scheduler/diagnose`

/** 每 Key 暂停（调度跳过该 key；恢复复用既有 restoreApiKey） */
export const suspendKeyPath = (kind: ChannelKind, channelId: number | string) =>
  `/api/${kind}/channels/${encodeURIComponent(String(channelId))}/keys/suspend`

/** 订阅生命周期补齐（subscriptionPath 已在上方定义） */
export const subscriptionLinkPath = (uid: string) => `${subscriptionPath(uid)}/link`
export const subscriptionUnlinkPath = (uid: string) => `${subscriptionPath(uid)}/unlink`
export const subscriptionPrimaryAccountPath = (uid: string) => `${subscriptionPath(uid)}/accounts/primary`
