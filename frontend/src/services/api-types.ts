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
  firstByteLatencySamples?: number
  firstByteLatencyP50Ms?: number
  firstByteLatencyP95Ms?: number
  firstContentLatencySamples?: number
  firstContentLatencyP50Ms?: number
  firstContentLatencyP95Ms?: number
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

// 被限制的 (Key, 模型) 组合（model_not_found 等场景，仅限制该 Key 对该模型的路由）
export interface DisabledKeyModelInfo {
  key: string
  model: string       // 触发限制的模型
  reason: string      // "model_not_found"
  message: string
  disabledAt: string  // ISO8601 时间戳
  recoverAt: string   // 自动恢复时间（ISO8601）
}

export interface DisabledGroupModelInfo {
  quotaGroup?: string
  key?: string
  model: string
  disabledAt: string
}

export interface GroupModelMutationResponse {
  success: boolean
  quotaGroup: string
  model: string
  affectedKeyCount: number
}

export interface APIKeyConfig {
  key: string
  name?: string
  enabled?: boolean
  quotaGroup?: string
  groupMultiplier?: number | null
  multiplierSource?: string
  multiplierUpdatedAt?: string
  sourceAccountUid?: string
  sourceRemoteTokenId?: number
  remoteStatus?: string
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
  channelUid?: string
  siteAccountUid?: string
  siteGroup?: string
  maxGroupMultiplier?: number | null
  name: string
  serviceType: 'openai' | 'gemini' | 'claude' | 'responses' | 'copilot'
  authHeader?: ChannelAuthHeader | ''
  baseUrl: string
  baseUrls?: string[]                // 多 BaseURL 支持（failover 模式）
  apiKeys: string[]
  apiKeyConfigs?: APIKeyConfig[]
  disabledApiKeys?: DisabledKeyInfo[]  // 被拉黑的 API Key
  disabledKeyModels?: DisabledKeyModelInfo[]  // 被限制的 (Key, 模型) 组合
  disabledGroupModels?: DisabledGroupModelInfo[]  // 人工禁用的 (配额组, 模型) 组合
  historicalApiKeys?: string[]
  description?: string
  website?: string
  insecureSkipVerify?: boolean
  modelMapping?: Record<string, string>
  subagentModel?: string                 // 子代理请求模型大类或具体模型名，为空沿用请求模型
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
  circuitBreakerDisabled?: boolean         // 禁用该渠道自动熔断（默认 false）
  normalizeMetadataUserId?: boolean        // 规范化 metadata.user_id（默认 false）
  stripBillingHeader?: boolean             // Messages 渠道特定：转发前移除 system 中 cch= 计费参数（默认 true）
  stripEmptyTextBlocks?: boolean           // Claude 协议特定：转发前移除裸空 text content block（兼容严格校验的第三方上游）
  normalizeSystemRoleToTopLevel?: boolean  // Claude 协议特定：将 messages 中 system 角色抽取回顶层 system 字段（兼容仅支持 user/assistant 的旧上游）
  codexNativeToolPassthrough?: boolean    // Codex 原生工具透传（默认 true）
  codexToolCompat?: boolean               // Codex 工具兼容（默认 true）
  responsesReasoningFold?: boolean        // Responses 推理续接折叠：截断推理时自动续轮（默认 false）
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

export type ChannelKind = 'messages' | 'chat' | 'responses' | 'gemini' | 'images' | 'vectors'
export type TextChannelKind = 'messages' | 'chat' | 'responses' | 'gemini'

export interface SubagentRoutingChannel {
  channelUid: string
  enabled: boolean
}

export interface SubagentRoutingPolicy {
  model: string
  channels: SubagentRoutingChannel[]
}

export interface SubagentRoutingChannelView {
  channelUid: string
  channelIndex: number
  channelName: string
  serviceType: string
  status: ChannelStatus
  effectiveState: string
  enabled: boolean
  requestModel: string
  resolvedModel: string
  modelSupported: boolean
  modelMapping?: Record<string, string>
}

export interface SubagentRoutingView {
  protocol: TextChannelKind
  activated: boolean
  revision: string
  policy: SubagentRoutingPolicy
  channels: SubagentRoutingChannelView[]
  legacyModels?: Array<{ channelUid: string; channelName: string; model: string }>
  legacyMixed: boolean
}

export interface UsageReportChannel {
  id: string
  name: string
  apiType: ChannelKind
  serviceType: string
}

export interface SchedulerDiagnoseContextRequirement {
  inputTokens?: number
  outputTokens?: number
  requiredTokens?: number
  minimumContextWindowTokens?: number
  explicitOutputMax?: boolean
  skipWindowValidation?: boolean
}

export interface SchedulerDiagnoseRequest {
  userId?: string
  model?: string
  routePrefix?: string
  channelName?: string
  failedChannels?: number[]
  hasImageContent?: boolean
  agentRole?: string
  contextRequirement?: SchedulerDiagnoseContextRequirement
}

export interface SchedulerTraceStage {
  name: string
  count: number
}

export interface SchedulerTraceCandidate {
  channelIndex: number
  channelName: string
  stage: string
  reason: string
  details?: string
}

export interface SchedulerTraceSelection {
  channelIndex: number
  channelName: string
  reason: string
}

export interface SchedulerSelectionTrace {
  kind: ChannelKind
  model?: string
  routePrefix?: string
  channelName?: string
  agentRole?: string
  stages?: SchedulerTraceStage[]
  candidates?: SchedulerTraceCandidate[]
  selected?: SchedulerTraceSelection
}

export interface SchedulerDiagnoseResponse {
  ok: boolean
  kind: ChannelKind
  reason?: string
  summary?: string
  error?: string
  selected?: {
    channelIndex: number
    channelName: string
    serviceType?: string
  }
  trace?: SchedulerSelectionTrace
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
  reasoningEffort?: string
  force?: boolean
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
  reasoningEffort?: string
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
  gatewayRequestId?: string
  requestId: string
  timestamp: string
  model: string
  originalModel?: string
  operation?: string
  originalReasoningEffort?: string
  actualReasoningEffort?: string
  statusCode: number
  durationMs: number
  inputTokens?: number
  outputTokens?: number
  cacheCreationTokens?: number
  cacheReadTokens?: number
  contextTokens?: number
  estimatedContextTokens?: number
  tokensPerSecond?: number
  isStream?: boolean  // 客户端请求是否流式；缺省表示旧日志未记录
  costUSD?: number    // 查询时按当前价格与 Key 倍率估算
  success: boolean
  keyMask: string
  baseUrl: string
  errorInfo: string
  isRetry: boolean
  interfaceType?: string  // 接口类型（Messages/Responses/Gemini）
  requestSource?: string
  selectionReason?: string
  selectionTraceSummary?: string

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
  subagentPolicyApplied?: boolean
  subagentPolicyModel?: string
  sessionId?: string         // 扁平化会话标识（用于请求关联）
}

export interface ChannelLogRetestRequest {
  channelId: number
  protocol: 'messages' | 'chat' | 'responses' | 'gemini'
  model: string
  reasoningEffort?: string
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
  modelDetails?: Array<{ id: string; supported_endpoints?: string[] }>
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

export interface ChannelDiscoveryResponse {
  models: DiscoveryModelsResult
  protocols: DiscoveryProtocolResult[]
  capabilities: DiscoveryCapabilitiesResult
  recommendation: ChannelDiscoveryRecommendation
  evidence?: DiscoveryEvidence[]
}

// ── 站点账号接入（new-api 家族）──────────────────────────────────────────

export interface SiteAccountProvisionedKey {
  name: string
  group?: string
  tokenId?: number
  protocol: string
  keyFingerprint?: string
}

export interface SiteChannelEvidence {
  protocol: string
  group: string
  level: 'configured' | 'info_verified' | 'real_request' | ''
  configuredAt?: string
  infoVerifiedAt?: string
  realRequestAt?: string
  lastDiagnostic?: string
}

export interface SiteAccount {
  plans?: SiteProvisionRequest[]
  credentialMode?: 'management' | 'key'
  lastSyncAt?: string
  lastSyncError?: string
  uid: string
  siteType: string
  baseUrl: string
  userId?: string
  authTokenMode?: string
  maskedAccessToken?: string
  createdAt: string
  updatedAt: string
  provisionedKeys?: SiteAccountProvisionedKey[]
  evidence?: SiteChannelEvidence[]
}

export interface SiteAccountUpsertRequest {
  uid?: string
  credentialMode?: 'management' | 'key'
  siteType: string
  baseUrl: string
  userId?: string
  authTokenMode?: string
  accessToken: string
}

export interface SiteGroupPreview {
  name: string
  displayName?: string
  modelsUnknown?: boolean
  ratio: number
  models?: string[]
  error?: string
}

export interface SiteGroupsPreviewResponse {
  identified: boolean
  groups?: SiteGroupPreview[]
  error?: string
}

export interface SiteVerifyResponse {
  verified: boolean
  error?: string
}

export interface SiteProvisionRequest {
  autoSyncGroups?: boolean
  allowModelFallback?: boolean
  maxAttempts?: number
  maxWaitSeconds?: number
  agent?: string
  existingTokenIds?: Record<string, number>
  protocol: string
  groups?: string[]
  excludeGroups?: string[]
  maxMultiplier?: number
  models?: string[]
  modelMapping?: Record<string, string>
}

export interface SiteProvisionGroupResult {
  group: string
  keyName: string
  channelName: string
  skipped: string
  reused: boolean
}

export interface SiteProvisionResult {
  accountUID: string
  protocol: string
  groups: SiteProvisionGroupResult[]
}

export interface SiteRemoteKey {
  id: number
  name: string
  group: string
  status: number
  model_limits_enabled: boolean
  model_limits: string
}

export interface SiteVerifyRequestResponse {
  success: boolean
  error?: string
}

export interface SiteReconcileAction {
  protocol: string
  group: string
  keyName: string
  channelName: string
  action: string
  detail: string
}

export interface SiteBalanceResponse {
  identified: boolean
  balance?: number
  currency?: string
  error?: string
}

export interface SiteChannelGroupCopyResult {
  channelIndex: number
  channelName: string
  accountUid: string
  group: string
  reused: boolean
}
