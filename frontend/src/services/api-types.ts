import type { ClaudeMessagesPreset } from '../generated/claudeMessagesPresets'
import type { CodexResponsesPreset } from '../generated/codexResponsesPresets'
import type { OpenAIChatPreset } from '../generated/openaiChatPresets'
import type { OpenAIMessagesPreset } from '../generated/openaiMessagesPresets'

// API 数据结构类型
export type ChannelStatus = 'active' | 'suspended' | 'disabled'
export type ChannelDisplayStatus = ChannelStatus | 'partial' | 'healthy' | 'error' | 'unknown' | ''

// 新增渠道在故障转移序列中的放置位置：front（首位）| back（末尾，默认）
export type ChannelPlacement = 'front' | 'back'

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
  routeKind?: ChannelKind
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
  reason: string      // "authentication_error" / "permission_error" / "insufficient_balance" / "insufficient_quota"
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

// 人工设置的配额分组模型禁用，与 disabledKeyModels 的自动临时限制相互独立。
export interface DisabledGroupModelInfo {
  quotaGroup: string
  key?: string
  model: string
  note?: string
  disabledAt: string
}

export interface GroupModelPolicyResponse {
  success: boolean
  quotaGroup: string
  model: string
  affectedKeyCount: number
}

export interface APIKeyConfig {
  /** 新建配置通常按 key 关联；服务端身份骨架可将 key 留空并仅返回 keyUid。 */
  key: string
  keyUid?: string
  credentialUid?: string
  name?: string
  baseUrl?: string
  enabled?: boolean
  quotaGroup?: string
  groupMultiplier?: number | null
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
  /** 保留服务端未来扩展字段，整对象编辑时不得意外丢失。 */
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
  accountUid?: string                // 自动托管账号稳定身份，同一 provider 的多协议渠道共享
  channelUid?: string                // 渠道稳定身份标识（创建后不因重排/改名/Key 变更而改变）
  logicalChannelUid?: string         // 逻辑渠道 UID（同站点多协议渠道共享；后端 RebuildLogicalChannels 回填）
  providerId?: string                // 已知 provider 模板 ID（如 mimo/deepseek）
  routeKind?: ChannelKind            // 前端统一列表中真实所属的后端渠道类型
  routeIndex?: number                // 前端统一列表中真实后端渠道索引
  logicalName?: string               // 前端统一列表中的逻辑渠道名称
  displayKey?: string                // 前端统一列表中的稳定展示 key
  protocolCapsules?: ChannelProtocolCapsule[]
  protocolRoutes?: ChannelProtocolRoute[]
  serviceType: 'openai' | 'gemini' | 'claude' | 'responses' | 'copilot'
  authHeader?: ChannelAuthHeader | ''
  baseUrl: string
  baseUrls?: string[]                // 多 BaseURL 支持（failover 模式）
  apiKeys: string[]
  apiKeyConfigs?: APIKeyConfig[]
  disabledApiKeys?: DisabledKeyInfo[]  // 被拉黑的 API Key
  disabledKeyModels?: DisabledKeyModelInfo[]  // 自动临时限制的 (Key, 模型) 组合
  disabledGroupModels?: DisabledGroupModelInfo[]  // 人工禁用的 (配额分组, 模型) 组合
  historicalApiKeys?: string[]
  description?: string
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
  stripBillingHeader?: boolean             // Messages 渠道：移除 billing header（默认：非官方 Anthropic 域名 true，官方域名 false）
  stripEmptyTextBlocks?: boolean           // Claude 协议特定：转发前移除裸空 text content block（兼容严格校验的第三方上游）
  normalizeSystemRoleToTopLevel?: boolean  // Claude 协议特定：将 messages 中 system 角色抽取回顶层 system 字段（兼容仅支持 user/assistant 的旧上游）
  codexNativeToolPassthrough?: boolean    // Codex 原生工具透传（默认 true）
  codexToolCompat?: boolean               // Codex 工具兼容（默认 true）
  normalizeNonstandardChatRoles?: boolean  // OpenAI Chat 上游：将非标准 role 改写为 user（默认 true）
  stripCodexClientTools?: boolean          // Responses 上游：透传前剥离 Codex CLI 0.130+ 客户端专属工具条目（默认 true）
  stripImageGenerationTool?: boolean       // Responses/Chat 上游：移除 image_generation 工具（默认 true）
  convertImageUrlToB64Json?: boolean       // Images 上游：将仅返回 URL 的 b64_json 请求响应转换为 base64
  latency?: number
  status?: ChannelDisplayStatus
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
  autoManaged?: boolean                      // 启用自动托管
  autoManagedAt?: string                     // 开始托管时间（ISO 格式）
  autoManagedKind?: string                   // 托管子类型："" | "generic" | "new_api"
  originType?: string                        // 渠道来源类型
  originTier?: string                        // 渠道来源可信层级
  subscriptionUid?: string                   // 关联的订阅 UID（new-api 自动托管场景）
  rpm?: number                // 能力测试发送速率（仅影响能力测试）
  tags?: string[]             // 用户自定义标签（自由文本，与 PoolTag 完全独立）
}

export interface ChannelProtocolCapsule {
  kind: ChannelKind
  label: string
  serviceType: string
  channelUid?: string
  index: number
  status?: ChannelDisplayStatus
}

export interface ChannelModelBinding {
  credentialUid?: string
  keyMask: string
  models: string[]
  updatedAt?: string
  modelsDiscoveredAt?: string
  modelDiscoverySource?: string
  modelDiscoveryMessage?: string
}

export interface ChannelProtocolRoute {
  kind: ChannelKind
  upstreamKind?: ChannelKind
  index: number
  name: string
  serviceType: string
  channelUid?: string
  status?: ChannelDisplayStatus
  apiKeys?: string[]
  apiKeyConfigs?: APIKeyConfig[]
  disabledApiKeys?: DisabledKeyInfo[]
  supportedModels?: string[]
  modelInventoryKnown?: boolean
  discoveredModels?: string[]
  modelBindings?: ChannelModelBinding[]
  modelsUpdatedAt?: string
  modelsDiscoveredAt?: string
  modelDiscoverySource?: string
  modelDiscoveryMessage?: string
  configured?: boolean
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

export interface LlmChannelDashboardResponse {
  dashboards: Record<'messages' | 'chat' | 'responses' | 'gemini', ChannelDashboardResponse>
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
  actualModel?: string // 复合协议：经过 ModelMapping 后实际发送给上游的模型名
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

// RedirectModelResult 单个探测模型经 ModelMapping 后的测试结果
export interface RedirectModelResult {
  probeModel: string      // 原生探测模型名
  actualModel: string     // ModelMapping 后实际发给上游的模型名
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
  sessionId?: string         // 扁平化会话标识（用于驾驶舱关联）

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
  routeKind?: ChannelKind
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
  serviceType?: Channel['serviceType']
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
  lastUserMessages?: string[]
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

export type ChannelDiscoveryKind = 'messages' | 'chat' | 'gemini' | 'responses'
export type ChannelDiscoveryTargetClient = 'codex' | 'claude-code' | 'claude'

export interface ChannelDiscoveryRequest {
  channelKind?: ChannelDiscoveryKind
  serviceType?: Channel['serviceType'] | ''
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

export interface DiscoveryRateLimitResult {
  initialRpm: number
  effectiveRpm: number
  rateLimited: boolean
  rateLimitedCount?: number
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
  rateLimit: DiscoveryRateLimitResult
  evidence?: DiscoveryEvidence[]
}

/** 快速探活请求：仅探一个真实模型以定 primaryKind，不做全量协议/能力探测。 */
export interface ChannelDiscoveryFastRequest {
  /** 可选提示，不能限制自动探测结果 */
  channelKind?: ChannelDiscoveryKind
  baseUrl?: string
  baseUrls?: string[]
  /** 兼容单个 key */
  apiKey?: string
  apiKeys?: string[]
  authHeader?: ChannelAuthHeader | ''
  customHeaders?: Record<string, string>
  proxyUrl?: string
  insecureSkipVerify?: boolean
}

/** 快速探活响应：primaryKind 必须来自成功协议；testedModel/streamingSupported 仅作证据，不写入渠道白名单。 */
export interface ChannelDiscoveryFastResponse {
  primaryKind: ChannelDiscoveryKind | ''
  testedModel: string
  streamingSupported: boolean
  testedKeyHash: string
  rateLimit: DiscoveryRateLimitResult
}

// ============== 健康中心类型 ==============

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
  firstByteSampleCount?: number
  p95FirstByteLatencyMs?: number
  // Forward-compat: origin/pool tags for card badge system (§8.2).
  // These fields may not be present in all API versions; consumers must null-check.
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
  keyMask?: string
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
  tokenPlanUsageSupported?: boolean
  miniMaxTokenPlanUsage?: MiniMaxTokenPlanUsage
  miniMaxTokenPlanUsageError?: string
}

export interface MiniMaxTokenPlanModelUsage {
  modelName: string
  currentIntervalUsageCount: number
  currentIntervalTotalCount: number
  currentIntervalRemainingPercent: number
  currentWeeklyUsageCount: number
  currentWeeklyTotalCount: number
  currentWeeklyRemainingPercent: number
  remainsTimeMs: number
  weeklyStartTime?: string
  weeklyEndTime?: string
}

export interface MiniMaxTokenPlanUsage {
  models: MiniMaxTokenPlanModelUsage[]
  fetchedAt: string
  sourceUrl: string
}

export interface TokenPlanUsageRefreshResponse {
  usage: MiniMaxTokenPlanUsage
  cached: boolean
}

export interface HealthCenterEndpointsResponse {
  channelUid: string
  endpoints: EndpointDetailItem[]
}

// ============== 汇率与 NewAPI Key 状态类型 ==============

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
  version?: number
}

export interface ExchangeRatesReplaceRequest {
  quotes: ExchangeRateQuote[]
  expectedSnapshotVersion?: number
}

export interface KeyMultiplierPatch {
  groupMultiplier?: number | null
  maxGroupMultiplier?: number | null
}

export interface KeyMultiplierResponse {
  keyUid: string
  group: string
  remoteMultiplier?: number
  groupMultiplier?: number
  maxMultiplier?: number
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
  paymentAmount?: number
  paymentUnit?: string
  creditAmount?: number
  creditUnit?: string
  version: number
  preview: string
}

export interface NewApiKeyStatus {
  keyUid?: string
  name: string
  group: string
  groupMultiplier: number
  maxGroupMultiplier: number
  sourceRemoteTokenId: number
  syncStatus: string
  reason?: string
  multiplierExpiresAt?: string
  updatedAt?: string
}

export interface NewApiSyncResult {
  subscriptionUid: string
  success: boolean
  balance?: number
  usedQuota?: number
  models?: string[]
  modelsHash?: string
  modelsHashChanged: boolean
  keys: NewApiKeyStatus[]
  discoveryTriggered: boolean
  failedReason?: string
}

export interface BalanceRefreshResult {
  success: boolean
  balance: number
  currency: string
  errorMessage: string
}

export interface SubscriptionRefreshResponse {
  subscription: SubscriptionItem
  refreshResult: NewApiSyncResult | BalanceRefreshResult
}

// ============== 订阅中心类型 ==============

export interface SubscriptionItem {
  subscriptionUid: string
  displayName: string
  provider?: string
  originType?: string
  originTier?: string
  billingMode?: string
  currency?: string
  balance?: number
  usedQuota?: number
  groupMultipliers?: Record<string, number>
  rechargeMultiplier?: number
  paymentAmount?: number | null
  paymentUnit?: string
  creditAmount?: number | null
  creditUnit?: string
  version: number
  authTokenMode?: 'bearer' | 'raw' | 'raw_auth' | string
  baseUrl?: string
  accessTokenMasked?: string
  userId?: string
  linkedChannelUids?: string[]
  source?: string
  confidence?: number
  notes?: string
  createdAt: string
  updatedAt: string
  archivedAt?: string

  // Phase 4 Item 6: 余额自动刷新
  billingApiKey?: string
  autoRefreshEnabled?: boolean
  autoRefreshSupported?: boolean
  lastBalanceRefreshAt?: string
  lastBalanceRefreshError?: string

  // NewAPI 自动接入的分组安全阈值与已绑定分组快照
  provisionGroup?: string
  provisionGroupRatio?: number
  maxGroupMultiplier?: number
  provisionedKeys?: NewApiProvisionedKeyInfo[]
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
  groupMultipliers?: Record<string, number>
  rechargeMultiplier?: number
  notes?: string
  source?: string

  // Phase 4 Item 6: 余额自动刷新
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
  groupMultipliers?: Record<string, number>
  rechargeMultiplier?: number
  notes?: string
  source?: string
  confidence?: number

  // Phase 4 Item 6: 余额自动刷新
  billingApiKey?: string
  autoRefreshEnabled?: boolean
  accessToken?: string
  userId?: string
  authTokenMode?: string
  expectedVersion?: number
}

// ============== new-api 订阅集成类型（§8.5.1） ==============

export interface NewApiVerifyRequest {
  baseUrl: string
  accessToken: string
  userId?: string
  authTokenMode?: string
  displayName?: string
  subscriptionUid?: string
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

export interface NewApiProvisionRequest {
  subscriptionUid: string
  displayName: string
  baseUrl: string
  accessToken: string
  channelKind: string
  userId?: string
  authTokenMode?: string
  channelName?: string
  provisionKeyName?: string
  provisionGroup?: string
  provisionAllEligibleGroups?: boolean
  provisionModels?: string[]
  maxGroupMultiplier?: number
  notes?: string
}

export interface NewApiProvisionedKeyInfo {
  name: string
  group: string
  groupMultiplier: number
  tokenId: number
}

export interface NewApiProvisionedKey extends NewApiProvisionedKeyInfo {
  reused: boolean
}

export interface NewApiProvisionResponse {
  subscription: SubscriptionItem
  channelUid: string
  channelIndex: number
  channelName?: string
  /** true 表示同站点已有渠道，key 已并入而非新建 */
  mergedChannel?: boolean
  provisionedKey: string
  provisionedTokenId: number
  reused: boolean
  provisionedKeys?: NewApiProvisionedKey[]
  discoveryStarted: boolean
}

// ============== new-api 多账号类型 ==============

export interface NewApiAccountCreateRequest {
  accessToken: string
  userId?: string
  displayName?: string
  authTokenMode?: string
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
  lastCheckedAt?: string
  usedQuota?: number
  authTokenMode?: string
  createdAt: string
}

export interface NewApiAccountListResponse {
  accounts: NewApiAccountItem[]
}

// ============== 驾驶舱类型 ==============

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

// ============== 渠道推荐类型（Phase 4 Item 4）==============

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

/** POST /manual-intents 请求体 */
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

/** GET /manual-intents 列表响应 */
export interface IntentListResponse {
  intents: ManualRoutingIntent[]
  total: number
}

// ============== Autopilot 智能路由类型 ==============

export interface SmartRoutingCostPreference {
  mode: 'quality_first' | 'balanced' | 'cost_first' | 'custom'
}

export interface SmartRoutingConfig {
  killSwitchActive: boolean
  costPreference: string
  l2ProbeEnabled?: boolean
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
  metricsKey?: string
  keyMask?: string
  originTier?: string
  channelKind?: string
  healthState?: string
  mappedModel?: string
  mappingSource?: string
  mappingReason?: string
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

// ── Trace v2 类型 ──

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
  selectedUid?: string
  selectedName?: string
  selectionCode?: string
}

/** endpoint 尝试摘要 */
export interface EndpointAttemptSummary {
  attemptUid: string
  attemptSeq: number
  status: string
  channelUid: string
  endpointLabel: string
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

// 自动添加渠道请求
export interface AutoAddChannelRequest {
  name?: string
  // provider 模板模式：带 providerId + apiKeys，baseURL 由后端按 key 前缀探测判定，baseUrls 可省略
  providerId?: string
  baseUrls?: string[]
  apiKeys: string[]
  rateLimitHint?: DiscoveryRateLimitResult
  subscriptionUid?: string
}

// Provider 模板 key 前缀规则
export interface ProviderKeyPrefixRule {
  prefix: string
  planTag: string
}

// Provider 候选 baseURL
export interface ProviderCandidate {
  baseUrl: string
  planTag?: string
  region?: string
  priority?: number
}

// Provider 在某个 CCX 协议渠道下的原生上游入口
export interface ProviderRoute {
  channelKind: string
  serviceType: string
  description?: string
  candidates?: ProviderCandidate[]
}

// 已知 provider 模板（模板化添加：选 provider + 输 key，系统自动判别 plan/baseURL）
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

// GET /channels/provider-templates 响应
export interface ProviderTemplatesResponse {
  providers: ProviderTemplate[]
}

// 自动添加渠道响应
export interface AutoAddChannelResponse {
  accountUid: string
  channelUid: string
  index: number
  discoveryStarted: boolean
  channels?: AutoAddChannelResult[]
}

export interface AutoAddChannelResult {
  accountUid: string
  channelKind: string
  channelUid: string
  index: number
  name: string
  serviceType: string
  discoveryStarted: boolean
}

export interface UpdateManagedAccountResponse {
  accountUid: string
  keyCount: number
  channelCount: number
  discoveryStarted: number
}

export interface ManagedAccountCredential {
  credentialUid: string
  keyMask: string
  hasVolcengineAccessKey?: boolean
  volcengineAccessKeyIdMask?: string
  volcenginePlan?: 'agent_plan' | 'coding_plan'
  volcenginePlanTier?: string
  volcenginePlanStatus?: string
  volcenginePlanUsage?: VolcenginePlanUsage
  hasMiMoConsoleCookie?: boolean
  mimoTokenPlan?: MiMoTokenPlanSnapshot
  hasCompshareConsoleCookie?: boolean
  compsharePlan?: CompsharePlanSnapshot
  hasKimiConsoleToken?: boolean
  kimiCodeUsage?: KimiCodeUsageSnapshot
}

export interface DeepSeekBalanceInfo {
  currency: 'CNY' | 'USD' | string
  totalBalance: string
  grantedBalance: string
  toppedUpBalance: string
}

export interface DeepSeekCredentialBalance {
  credentialUid: string
  keyMask: string
  isAvailable: boolean
  balanceInfos?: DeepSeekBalanceInfo[]
  fetchedAt: string
  error?: string
}

export interface DeepSeekAccountBalancesResponse {
  accountUid: string
  balances: DeepSeekCredentialBalance[]
}

/** 火山套餐单个时间窗口用量。Agent Plan 含 quota/used，Coding Plan 含 usedPercent。 */
export interface VolcenginePlanUsageWindow {
  quota?: number
  used: number
  usedPercent?: number
  resetTime?: number
}

/**
 * 火山套餐用量快照。
 * Agent Plan 填充 fiveHour/daily/weekly/monthly（含 quota）；
 * Coding Plan 填充 fiveHour/weekly/monthly（仅 usedPercent）。
 */
export interface VolcenginePlanUsage {
  fiveHour?: VolcenginePlanUsageWindow
  daily?: VolcenginePlanUsageWindow
  weekly?: VolcenginePlanUsageWindow
  monthly?: VolcenginePlanUsageWindow
  fetchedAt: string
  error?: string
}

export interface VolcenginePlanUsageRefreshResponse {
  usage: VolcenginePlanUsage
  cached: boolean
}

export interface MiMoTokenPlanQuota {
  used: number
  limit: number
  usedPercent: number
}

export interface MiMoTokenPlanSnapshot {
  planCode: string
  planName: string
  currentPeriodEnd: string
  expired: boolean
  monthUsage: MiMoTokenPlanQuota
  currentUsage: MiMoTokenPlanQuota
  validatedAt: string
}

export interface MiMoConsoleCookieResponse {
  accountUid: string
  credentialUid: string
  keyAdopted: boolean
  keyMask: string
  adoptedApiKey?: string
  tokenPlan: MiMoTokenPlanSnapshot
  discoveryStarted: number
}

export interface CompsharePlanUsageWindow {
  used: number
  limit: number
  updatedAt?: number
  nextResetAt?: number
}

export interface CompsharePlanSnapshot {
  planCode: string
  planName: string
  displayName: string
  status: number
  concurrencyLimit: number
  isTeam: boolean
  expireAt: number
  fiveHourUsage: CompsharePlanUsageWindow
  weeklyUsage: CompsharePlanUsageWindow
  monthlyUsage: CompsharePlanUsageWindow
  validatedAt: string
}

export interface CompshareConsoleCookieResponse {
  accountUid: string
  credentialUid: string
  plan: CompsharePlanSnapshot
}

export interface KimiCodeQuotaWindow {
  used: number
  limit: number
  remaining: number
  resetTime?: string
}

export interface KimiCodeRateLimit {
  windowSeconds: number
  usage: KimiCodeQuotaWindow
}

export interface KimiCodeRatioWindow {
  ratio: number
  enabled: boolean
  resetTime?: string
}

export interface KimiCodeMoney {
  currency?: string
  priceInCents: number
}

export interface KimiBoosterWallet {
  id?: string
  status?: string
  allowTopup: boolean
  moneyLeft: KimiCodeMoney
  moneyTotal: KimiCodeMoney
  monthlyChargeLimit: KimiCodeMoney
  monthlyUsed: KimiCodeMoney
}

export interface KimiCodeBalance {
  feature?: string
  type?: string
  unit?: string
  amountUsedRatio: number
  kimiCodeUsedRatio: number
  expireTime?: string
}

export interface KimiCodeUsageSnapshot {
  weeklyUsage: KimiCodeQuotaWindow
  totalQuota: KimiCodeQuotaWindow
  rateLimits?: KimiCodeRateLimit[]
  codeFiveHour?: KimiCodeRatioWindow
  codeSevenDay?: KimiCodeRatioWindow
  subscriptionBalance?: KimiCodeBalance
  giftBalances?: KimiCodeBalance[]
  boosterWallets?: KimiBoosterWallet[]
  validatedAt: string
}

export interface KimiConsoleTokenResponse {
  accountUid: string
  credentialUid: string
  usage: KimiCodeUsageSnapshot
}

export interface VolcengineAccessKeyResponse {
  accountUid: string
  credentialUid: string
  accessKeyIdMask: string
  plan: 'agent_plan' | 'coding_plan'
  planTier: string
  planStatus: string
  usage?: VolcenginePlanUsage
  discoveryStarted: number
}

export interface ManagedAccountChannel {
  kind: ChannelKind
  channelUid: string
  name: string
  serviceType: string
  status: string
  modelInventoryKnown?: boolean
  discoveredModels?: string[]
  modelBindings?: ChannelModelBinding[]
  modelsUpdatedAt?: string
  modelsDiscoveredAt?: string
  modelDiscoverySource?: string
  modelDiscoveryMessage?: string
  protocolAvailability?: ProtocolAvailability[]
}

export interface ProtocolAvailability {
  protocol: ChannelKind
  modelInventoryKnown?: boolean
  discoveredModels?: string[]
  modelBindings?: ChannelModelBinding[]
  modelsDiscoveredAt?: string
  modelDiscoverySource?: string
  modelDiscoveryMessage?: string
}

export interface ManagedAccount {
  accountUid: string
  providerId: string
  name: string
  credentials: ManagedAccountCredential[]
  channels: ManagedAccountChannel[]
  endpointCount: number
}

export interface ManagedAccountsResponse {
  accounts: ManagedAccount[]
}

// Endpoint 发现信息
export interface EndpointDiscoveryInfo {
  keyMask: string
  baseUrl: string
  modelsCount: number
  protocolOk: boolean
  modelDiscoverySource?: string
  modelDiscoveryMessage?: string
  modelsDiscoveredAt?: string
  protocolModels?: Partial<Record<ChannelKind, string[]>>
  protocolDiscoveredAt?: Partial<Record<ChannelKind, string>>
  protocolDiscoverySource?: Partial<Record<ChannelKind, string>>
  protocolDiscoveryMessage?: Partial<Record<ChannelKind, string>>
  protocolDiscoveryError?: Partial<Record<ChannelKind, string>>
}

// 发现状态信息
export interface DiscoveryStatusInfo {
  status: 'idle' | 'pending' | 'running' | 'done' | 'failed'
  startedAt?: string
  finishedAt?: string
  error?: string
  endpoints?: EndpointDiscoveryInfo[]
}

// 自动托管状态响应
export interface ChannelAutoStatusResponse {
  autoManaged: boolean
  autoManagedAt?: string
  discovery?: DiscoveryStatusInfo
}

// ============== 画像变更事件（Phase 3A） ==============

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
}

export interface CostReportResponse {
  groupBy: 'user' | 'model' | 'key'
  apiType: string
  duration: string
  rows: CostReportRow[]
}

// ── A/B 测试（Phase 4 Item 8） ──

export interface ABTestRecord {
  recordUid: string
  model: string
  channelKind: string
  primaryChannelUid: string
  primarySuccess: boolean
  primaryStatusCode: number
  primaryLatencyMs: number
  shadowChannelUid: string
  shadowSuccess: boolean
  shadowStatusCode: number
  shadowLatencyMs: number
  shadowError?: string
  shadowCostUsd: number
  traceUid?: string
  createdAt: string
}

export interface ABTestChannelStats {
  channelUid: string
  count: number
  successCount: number
  successRate: number
  avgLatencyMs: number
  totalCostUsd: number
}

export interface ABTestStats {
  totalRecords: number
  shadowSuccessCount: number
  shadowFailCount: number
  shadowSuccessRate: number
  avgShadowLatencyMs: number
  totalShadowCostUsd: number
  byChannel?: Record<string, ABTestChannelStats>
}

export interface ABTestResultsResponse {
  enabled: boolean
  sampleRatio: number
  shadowCandidateCount: number
  budgetUsed: number
  budgetRemaining: number
  maxBudgetPerHour: number
  killSwitchActive: boolean
  stats: ABTestStats
  recentRecords: ABTestRecord[]
  totalShadowCostUsd: number
}

export interface ABTestEmergencyStopResponse {
  ok: boolean
  action: string
  reason: string
  note: string
}

// ─── 预置数据（GET /api/presets）───────────────────────────────────────────
// 与后端 presetstore.PresetBundle 对齐；前端订阅表单选项由此派生，取代硬编码副本。

export interface PresetOriginTypeEntry {
  value: string
  tier: string
}

export interface PresetNewApiDefaults {
  originType: string
  originTier: string
  billingMode: string
}

export interface SubscriptionPreset {
  originTypes: PresetOriginTypeEntry[]
  billingModes: string[]
  sources: string[]
  autoRefreshProviders: string[]
  newApiDefaults: PresetNewApiDefaults
  originTypeAliases: Record<string, string>
}

export interface WrappedPresetCollection<T> {
  schemaVersion?: number
  providers?: Record<string, T>
  presets?: Record<string, T>
}

export interface RuntimeModelRegistryEntry extends UpstreamModelCapability {
  patterns?: string[]
}

export interface RuntimeModelBenchmarkProfile extends ModelBenchmarkProfile {
  patterns?: string[]
}

export interface RuntimeModelRegistryBundle {
  schemaVersion?: number
  pricingUnit?: string
  upstreamCapabilities?: RuntimeModelRegistryEntry[]
  benchmarkProfiles?: RuntimeModelBenchmarkProfile[]
}

export interface ChannelPresetBundle {
  schemaVersion?: number
  claudeMessages?: Record<string, ClaudeMessagesPreset> | WrappedPresetCollection<ClaudeMessagesPreset>
  openAIChat?: Record<string, OpenAIChatPreset> | WrappedPresetCollection<OpenAIChatPreset>
  codexResponses?: Record<string, CodexResponsesPreset> | WrappedPresetCollection<CodexResponsesPreset>
  openAIMessages?: Record<string, OpenAIMessagesPreset> | WrappedPresetCollection<OpenAIMessagesPreset>
}

export interface PresetBundle {
  schemaVersion: number
  dataVersion: string
  subscription: SubscriptionPreset
  modelRegistry?: Record<string, UpstreamModelCapability> | RuntimeModelRegistryBundle
  channelPresets?: ChannelPresetBundle
  builtinModelsManifests?: Record<string, unknown>
}

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

export interface DeleteLogicalChannelResponse {
  message: string
  logicalChannelUid: string
  removedChannels: string[]
}
