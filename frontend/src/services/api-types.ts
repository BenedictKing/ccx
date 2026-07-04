// API 数据结构类型
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
export type ChannelAuthHeader = 'auto' | 'bearer' | 'x-api-key'

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

export interface CopilotUserResponse {
  login: string
  id: number
  avatarUrl?: string
  htmlUrl?: string
}

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
  recoverAt?: string  // 自动恢复时间（可选，ISO8601）
  config?: APIKeyConfig // 拉黑前的 key 配置快照，restore 时恢复
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
  baseUrls?: string[]                // 多 BaseURL 支持（failover 模式）
  apiKeys: string[]
  apiKeyConfigs?: APIKeyConfig[]
  disabledApiKeys?: DisabledKeyInfo[]  // 被拉黑的 API Key
  historicalApiKeys?: string[]
  description?: string
  website?: string
  insecureSkipVerify?: boolean
  modelMapping?: Record<string, string>
  modelCapabilities?: Record<string, UpstreamModelCapability>
  embeddingCapabilities?: Record<string, EmbeddingCapability>
  defaultCapability?: UpstreamModelCapability
  allowUnknownContext?: boolean
  reasoningMapping?: Record<string, 'none' | 'low' | 'medium' | 'high' | 'xhigh' | 'max'>
  reasoningParamStyle?: 'reasoning' | 'reasoning_effort' | 'thinking'
  textVerbosity?: 'low' | 'medium' | 'high' | ''
  fastMode?: boolean
  customHeaders?: Record<string, string>  // 自定义请求头
  proxyUrl?: string                        // HTTP/HTTPS/SOCKS5 代理 URL
  requestTimeoutMs?: number                // 非流式上游请求超时时间（毫秒，0/空=继承全局）
  responseHeaderTimeoutMs?: number         // 等待上游 HTTP 响应头超时时间（毫秒，0/空=继承全局）
  streamFirstContentTimeoutMs?: number     // 流式首字等待超时（毫秒，0/空=继承全局）
  streamInactivityTimeoutMs?: number       // 流式首字后断流超时（毫秒，0/空=继承全局）
  streamToolCallIdleTimeoutMs?: number     // 工具调用空闲超时（毫秒，0/空=继承全局）
  routePrefix?: string                     // 路由前缀（如 "kimi"，访问 /kimi/v1/messages）
  autoBlacklistBalance?: boolean           // 余额不足自动拉黑（默认 true）
  normalizeMetadataUserId?: boolean        // 规范化 metadata.user_id（默认 true）
  stripBillingHeader?: boolean             // Messages 渠道特定：转发前移除 system 中 cch= 计费参数（默认 true）
  stripEmptyTextBlocks?: boolean           // Claude 协议特定：转发前移除裸空 text content block（兼容严格校验的第三方上游）
  normalizeSystemRoleToTopLevel?: boolean  // Claude 协议特定：将 messages 中 system 角色抽取回顶层 system 字段（兼容仅支持 user/assistant 的旧上游）
  codexNativeToolPassthrough?: boolean    // Codex 原生工具透传（默认 true）
  codexToolCompat?: boolean               // Codex 工具兼容（默认 true）
  normalizeNonstandardChatRoles?: boolean  // OpenAI Chat 上游：将非标准 role 改写为 user（默认 true）
  stripCodexClientTools?: boolean          // Responses 上游：透传前剥离 Codex CLI 0.130+ 客户端专属工具条目（默认 true）
  stripImageGenerationTool?: boolean       // Responses/Chat 上游：移除 image_generation 工具（默认 true）
  convertImageUrlToB64Json?: boolean       // Images 上游：将仅返回 URL 的 b64_json 请求响应转换为 base64
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
  passbackReasoningContent?: boolean     // Claude 协议特定：将 thinking 块转为 reasoning_content 回传（兼容 mimo 等上游）
  passbackThinkingBlocks?: boolean       // Claude 协议特定：将真实 reasoning_content 投影为 content[].thinking（兼容 DeepSeek/GLM 等严格 thinking 上游）
  supportedModels?: string[]  // 支持的模型白名单（空=全部），支持通配符如 gpt-4*
  noVision?: boolean                       // 整个渠道不支持图片输入
  noVisionModels?: string[]                // 不支持图片输入的模型列表（匹配 modelMapping 后的实际模型名）
  visionFallbackModel?: string               // 含图请求命中 noVisionModels 时使用的替代模型
  // 主动限速（渠道级生产代理限速，区别于能力测试的 rpm）
  rateLimitRpm?: number                      // 每分钟请求数上限（0/空=不限）
  rateLimitWindowMinutes?: number            // 滑动窗口时长（秒，0/空=默认60秒）
  rateLimitBurst?: number                    // 已废弃，保留仅为兼容性
  rateLimitMaxConcurrent?: number            // 最大并发上游请求数（0/空=不限）
  rateLimitAutoFromHeaders?: boolean         // 自动从上游响应头解析限流信息并动态调速（默认 true）
  historicalImageTurnLimit?: number          // 历史图片轮次限制（0=不限制，2-10=裁剪历史图片）
  compactModel?: string                      // 本地 compact 时使用的上游模型名（不经过 modelMapping，为空则使用原始请求的模型）
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
  actualModel?: string // 复合协议：经过 ModelMapping 后实际发送给上游的模型名
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

// RedirectModelResult 单个探测模型经 ModelMapping 后的测试结果
export interface RedirectModelResult {
  probeModel: string      // 原生探测模型名
  actualModel: string     // ModelMapping 后实际发给上游的模型名
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

// 历史数据点（用于时间序列图表）
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

// 渠道历史指标响应
export interface MetricsHistoryResponse {
  channelIndex: number
  channelName: string
  dataPoints: HistoryDataPoint[]
  summary?: GlobalStatsSummary
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
  costUSD?: number
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
  summary?: GlobalStatsSummary
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
  costUSD?: number
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
  totalCostUSD?: number
  avgSuccessRate: number
  duration: string
  intervalSeconds?: number
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
  cacheCreationTokens: number
  cacheReadTokens: number
  costUSD?: number
}

export interface ModelStatsHistoryResponse {
  models: Record<string, ModelHistoryDataPoint[]>
  duration: string
  interval: string
}

// ============== 渠道日志类型 ==============

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
  interfaceType?: string  // 接口类型（Messages/Responses/Gemini）
  requestSource?: string

  // 请求生命周期状态
  status: string  // pending/connecting/first_byte/streaming/completed/failed/cancelled
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
  sessionId?: string         // 扁平化会话标识（用于驾驶舱关联）
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

export interface ChannelModelsRequest {
  key: string
  baseUrl?: string
  proxyUrl?: string
  insecureSkipVerify?: boolean
  customHeaders?: Record<string, string>
  authHeader?: ChannelAuthHeader | ''
  baseUrls?: string[]
}

export interface ChannelSequenceEntry {
  channelIndex: number
  channelName: string
}

export interface ConversationInfo {
  id: string
  kind: 'messages' | 'responses' | 'chat' | 'gemini' | 'images' | 'vectors'
  userId: string
  rawUserId?: string
  title?: string
  createdAt: string
  lastActiveAt: string
  requestCount: number
  models: string[]
  currentChannel: number
  channelName: string
  status: 'active' | 'streaming' | 'idle'
  lastModel: string
  lastRequestId: string
  lastUserMessage?: string
  lastRecap?: string
  lastRecapAt?: string
  parentThreadId?: string
  parentConversationId?: string
  childConversationIds?: string[]

  // subagent 观测（仅展示，不影响路由）
  hasSubagents?: boolean
  subagentCount?: number
  mainChannel?: number
  subagentChannel?: number
}

export interface SequenceOverrideInfo {
  sequence: ChannelSequenceEntry[]
  hasMainSequence?: boolean
  subagentSequence?: ChannelSequenceEntry[]  // subagent 专用序列（为空时 fallback 到 sequence）
  setAt: string
  expiresAt: string
  isPerpetual?: boolean
}

export interface ConversationsResponse {
  conversations: ConversationInfo[]
  total: number
  overrides: Record<string, SequenceOverrideInfo>
  channelsByKind?: Record<string, { index: number; name: string; priority: number; status: string; circuitOpen?: boolean }[]>
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
