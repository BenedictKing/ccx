// API服务模块
import { normalizeLocale } from '@/i18n/core'
import { translate } from '@/i18n'
import { useAuthStore } from '@/stores/auth'
import { usePreferencesStore } from '@/stores/preferences'
import { API_BASE, ApiError } from './api-helpers'
import type {
  CapabilitySnapshot,
  CapabilityTestJob,
  CapabilityTestJobStartResponse,
  CapabilityTestResult,
  Channel,
  ChannelDashboardResponse,
  LlmChannelDashboardResponse,
  ChannelDiscoveryRequest,
  ChannelDiscoveryResponse,
  ChannelDiscoveryFastRequest,
  ChannelDiscoveryFastResponse,
  ChannelKeyMetricsHistoryResponse,
  ChannelLogsResponse,
  ChannelMetrics,
  ChannelModelsRequest,
  ChannelSequenceEntry,
  ChannelsResponse,
  ChannelStatus,
  CockpitOverviewResponse,
  RecommendationsResponse,
  CopilotDeviceCodeResponse,
  CopilotTokenResponse,
  CopilotUserResponse,
  ConversationsResponse,
  GlobalStatsHistoryResponse,
  MetricsHistoryResponse,
  ModelStatsHistoryResponse,
  ModelsResponse,
  PingResult,
  ResumeChannelResponse,
  SchedulerStatsResponse,
  SchedulerDiagnoseRequest,
  SchedulerDiagnoseResponse,
  StartCapabilityTestOptions,
  SubscriptionCreateRequest,
  SubscriptionItem,
  SubscriptionsListResponse,
  SubscriptionUpdateRequest,
  PresetBundle,
  CreateIntentRequest,
  ManualRoutingIntent,
  IntentListResponse,
  CompatDiagnoseResult,
  HealthCenterOverview,
  HealthCenterChannelsResponse,
  HealthCenterEndpointsResponse,
  TokenPlanUsageRefreshResponse,
  SmartRoutingConfig,
  AutopilotTraceListResponse,
  AutopilotTraceStats,
  AutopilotTraceDetailResponse,
  AutoAddChannelRequest,
  AutoAddChannelResponse,
  UpdateManagedAccountResponse,
  ManagedAccountsResponse,
  DeepSeekAccountBalancesResponse,
  MiMoConsoleCookieResponse,
  CompshareConsoleCookieResponse,
  KimiConsoleTokenResponse,
  VolcengineAccessKeyResponse,
  VolcenginePlanUsageRefreshResponse,
  ChannelAutoStatusResponse,
  ProviderTemplatesResponse,
  CostReportResponse,
  ABTestResultsResponse,
  ABTestEmergencyStopResponse,
  NewApiVerifyRequest,
  NewApiVerifyResponse,
  NewApiProvisionRequest,
  NewApiProvisionResponse,
  NewApiAccountCreateRequest,
  NewApiAccountItem,
  NewApiAccountListResponse,
} from './api-types'

export * from './api-helpers'
export * from './api-types'

export class ApiService {
  private historyQuery(duration: string, interval?: string): string {
    const params = new URLSearchParams({ duration })
    if (interval) {
      params.set('interval', interval)
    }
    return params.toString()
  }

  private t(key: Parameters<typeof translate>[1], params?: Parameters<typeof translate>[2]): string {
    const preferencesStore = usePreferencesStore()
    return translate(normalizeLocale(preferencesStore.uiLanguage as unknown as string), key, params)
  }

  // 获取当前 API Key（从 AuthStore）
  private getApiKey(): string | null {
    const authStore = useAuthStore()
    return authStore.apiKey as unknown as string | null
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
      headers
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

  async requestCopilotDeviceCode(proxyUrl?: string): Promise<CopilotDeviceCodeResponse> {
    return this.request('/copilot/oauth/device/code', {
      method: 'POST',
      body: JSON.stringify({ proxyUrl: proxyUrl?.trim() || undefined })
    })
  }

  async pollCopilotAccessToken(deviceCode: string, proxyUrl?: string): Promise<CopilotTokenResponse> {
    return this.request('/copilot/oauth/token', {
      method: 'POST',
      body: JSON.stringify({ deviceCode, proxyUrl: proxyUrl?.trim() || undefined })
    })
  }

  async verifyCopilotAccessToken(accessToken: string, proxyUrl?: string): Promise<CopilotUserResponse> {
    return this.request('/copilot/oauth/verify', {
      method: 'POST',
      body: JSON.stringify({ accessToken, proxyUrl: proxyUrl?.trim() || undefined })
    })
  }

  async diagnoseCopilotChannel(channelId: number, accessToken?: string): Promise<Record<string, unknown>> {
    return this.request(`/responses/channels/${channelId}/copilot/diagnose`, {
      method: 'POST',
      body: JSON.stringify({ accessToken })
    })
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

  async suspendApiKey(channelId: number, apiKey: string): Promise<void> {
    await this.request(`/messages/channels/${channelId}/keys/suspend`, {
      method: 'POST',
      body: JSON.stringify({ apiKey })
    })
  }

  async resumeApiKey(channelId: number, apiKey: string): Promise<void> {
    await this.request(`/messages/channels/${channelId}/keys/resume`, {
      method: 'POST',
      body: JSON.stringify({ apiKey })
    })
  }

  async restoreKeyModel(channelId: number, apiKey: string, model: string): Promise<void> {
    await this.request(`/messages/channels/${channelId}/keys/restore-model`, {
      method: 'POST',
      body: JSON.stringify({ apiKey, model })
    })
  }

  async pingChannel(id: number): Promise<PingResult> {
    return this.request(`/messages/ping/${id}`)
  }

  async pingAllChannels(): Promise<Array<{ id: number; name: string; latency: number; status: string }>> {
    return this.request('/messages/ping')
  }

  async getChannelModels(id: number, request: ChannelModelsRequest): Promise<ModelsResponse> {
    return this.request(`/messages/channels/${id}/models`, {
      method: 'POST',
      body: JSON.stringify(request)
    })
  }

  async updateChannelModelMapping(id: number, sourcePattern: string, targetModel: string, reasoning: string): Promise<void> {
    await this.request(`/messages/channels/${id}/mappings`, {
      method: 'PUT',
      body: JSON.stringify({ source_pattern: sourcePattern, target_model: targetModel, reasoning })
    })
  }

  // ============== 兼容性诊断 API ==============

  async diagnoseChannelCompat(
    type: 'messages' | 'chat' | 'gemini' | 'responses',
    id: number
  ): Promise<CompatDiagnoseResult> {
    return this.request(`/${type}/channels/${id}/compat-diagnose`, { method: 'POST', body: '{}' })
  }

  async discoverChannelConfig(request: ChannelDiscoveryRequest): Promise<ChannelDiscoveryResponse> {
    return this.request('/channel-discovery', {
      method: 'POST',
      body: JSON.stringify(request)
    })
  }

  /** 快速探活：仅探一个真实模型以定 primaryKind，不做全量协议/能力探测。 */
  async discoverChannelConfigFast(request: ChannelDiscoveryFastRequest): Promise<ChannelDiscoveryFastResponse> {
    return this.request('/channel-discovery-fast', {
      method: 'POST',
      body: JSON.stringify(request)
    })
  }

  // ============== 能力测试 API ==============

  async startChannelCapabilityTest(
    type: 'messages' | 'chat' | 'gemini' | 'responses',
    id: number,
    options: StartCapabilityTestOptions = {}
  ): Promise<CapabilityTestJobStartResponse> {
    const body: { targetProtocols: string[]; timeout: number; previousJobId?: string; rpm?: number; sourceTab?: string; models?: string[] } = {
      targetProtocols: options.targetProtocols?.length ? options.targetProtocols : ['messages', 'responses', 'chat', 'gemini'],
      timeout: 10000,
      rpm: options.rpm
    }
    if (options.previousJobId) {
      body.previousJobId = options.previousJobId
    }
    if (options.sourceTab) {
      body.sourceTab = options.sourceTab
    }
    if (options.models?.length) {
      body.models = options.models
    }
    return this.request(`/${type}/channels/${id}/capability-test`, {
      method: 'POST',
      body: JSON.stringify(body)
    })
  }

  async getChannelCapabilitySnapshot(type: 'messages' | 'chat' | 'gemini' | 'responses', id: number, sourceTab?: string): Promise<CapabilitySnapshot> {
    const url = sourceTab
      ? `/${type}/channels/${id}/capability-snapshot?sourceTab=${sourceTab}`
      : `/${type}/channels/${id}/capability-snapshot`
    return this.request(url)
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
        targetProtocols: ['messages', 'responses', 'chat', 'gemini'],
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

  async suspendResponsesApiKey(channelId: number, apiKey: string): Promise<void> {
    await this.request(`/responses/channels/${channelId}/keys/suspend`, {
      method: 'POST',
      body: JSON.stringify({ apiKey })
    })
  }

  async resumeResponsesApiKey(channelId: number, apiKey: string): Promise<void> {
    await this.request(`/responses/channels/${channelId}/keys/resume`, {
      method: 'POST',
      body: JSON.stringify({ apiKey })
    })
  }

  async restoreResponsesKeyModel(channelId: number, apiKey: string, model: string): Promise<void> {
    await this.request(`/responses/channels/${channelId}/keys/restore-model`, {
      method: 'POST',
      body: JSON.stringify({ apiKey, model })
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

  async getResponsesChannelModels(id: number, request: ChannelModelsRequest): Promise<ModelsResponse> {
    return this.request(`/responses/channels/${id}/models`, {
      method: 'POST',
      body: JSON.stringify(request)
    })
  }

  async updateResponsesChannelModelMapping(id: number, sourcePattern: string, targetModel: string, reasoning: string): Promise<void> {
    await this.request(`/responses/channels/${id}/mappings`, {
      method: 'PUT',
      body: JSON.stringify({ source_pattern: sourcePattern, target_model: targetModel, reasoning })
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
  async getSchedulerStats(type?: 'messages' | 'responses' | 'gemini' | 'chat' | 'images' | 'vectors'): Promise<SchedulerStatsResponse> {
    // All managed channel groups share the scheduler stats endpoint via ?type=.
    const query = type && type !== 'messages' ? `?type=${type}` : ''
    return this.request(`/messages/channels/scheduler/stats${query}`)
  }

  // 获取渠道仪表盘数据（合并 channels + metrics + stats）
  async getChannelDashboard(type: 'messages' | 'responses' | 'gemini' | 'chat' | 'images' | 'vectors' = 'messages'): Promise<ChannelDashboardResponse> {
    const query = type !== 'messages' ? `?type=${type}` : ''
    return this.request(`/messages/channels/dashboard${query}`)
  }

  // 获取统一 LLM 渠道仪表盘，避免前端分别轮询四种协议。
  async getLlmChannelDashboard(): Promise<LlmChannelDashboardResponse> {
    return this.request('/messages/channels/dashboard?type=llm')
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

  // ============== 熔断器配置 API ==============

  // 获取熔断器运行时配置
  async getCircuitBreaker(): Promise<{ windowSize: number; failureThreshold: number; consecutiveFailuresThreshold: number; requestTimeoutMs: number; responseHeaderTimeoutMs: number; streamFirstContentTimeoutMs: number; streamInactivityTimeoutMs: number; streamToolCallIdleTimeoutMs: number }> {
    return this.request('/settings/circuit-breaker')
  }

  // 更新熔断器运行时配置（partial update）
  async setCircuitBreaker(params: { windowSize?: number; failureThreshold?: number; consecutiveFailuresThreshold?: number; requestTimeoutMs?: number; responseHeaderTimeoutMs?: number; streamFirstContentTimeoutMs?: number; streamInactivityTimeoutMs?: number; streamToolCallIdleTimeoutMs?: number }): Promise<unknown> {
    return this.request('/settings/circuit-breaker', {
      method: 'PUT',
      body: JSON.stringify(params)
    })
  }

  // ============== 历史指标 API ==============

  // 获取 Messages 渠道历史指标（用于时间序列图表）
  async getChannelMetricsHistory(duration: string = '24h', interval?: string): Promise<MetricsHistoryResponse[]> {
    return this.request(`/messages/channels/metrics/history?${this.historyQuery(duration, interval)}`)
  }

  // 获取 Responses 渠道历史指标
  async getResponsesChannelMetricsHistory(duration: string = '24h', interval?: string): Promise<MetricsHistoryResponse[]> {
    return this.request(`/responses/channels/metrics/history?${this.historyQuery(duration, interval)}`)
  }

  // ============== Key 级别历史指标 API ==============

  // 获取 Messages 渠道 Key 级别历史指标（用于 Key 趋势图表）
  async getChannelKeyMetricsHistory(channelId: number, duration: string = '6h', interval?: string): Promise<ChannelKeyMetricsHistoryResponse> {
    return this.request(`/messages/channels/${channelId}/keys/metrics/history?${this.historyQuery(duration, interval)}`)
  }

  // 获取 Responses 渠道 Key 级别历史指标
  async getResponsesChannelKeyMetricsHistory(channelId: number, duration: string = '6h', interval?: string): Promise<ChannelKeyMetricsHistoryResponse> {
    return this.request(`/responses/channels/${channelId}/keys/metrics/history?${this.historyQuery(duration, interval)}`)
  }

  // ============== 全局统计 API ==============

  // 获取 Messages 全局统计历史
  async getMessagesGlobalStats(duration: string = '24h', interval?: string): Promise<GlobalStatsHistoryResponse> {
    return this.request(`/messages/global/stats/history?${this.historyQuery(duration, interval)}`)
  }

  // 获取 Responses 全局统计历史
  async getResponsesGlobalStats(duration: string = '24h', interval?: string): Promise<GlobalStatsHistoryResponse> {
    return this.request(`/responses/global/stats/history?${this.historyQuery(duration, interval)}`)
  }
  // ============== 模型统计 API ==============

  async getModelStatsHistory(type: 'messages' | 'responses' | 'gemini' | 'chat' | 'images' | 'vectors', duration: string = '24h'): Promise<ModelStatsHistoryResponse> {
    return this.request(`/${type}/models/stats/history?duration=${duration}`)
  }

  // ============== 渠道日志 API ==============

  async getChannelLogs(type: 'messages' | 'responses' | 'gemini' | 'chat' | 'images' | 'vectors', channelId: number): Promise<ChannelLogsResponse> {
    return this.request(`/${type}/channels/${channelId}/logs`)
  }

  async diagnoseSchedulerSelection(type: 'messages' | 'responses' | 'gemini' | 'chat' | 'images' | 'vectors', payload: SchedulerDiagnoseRequest): Promise<SchedulerDiagnoseResponse> {
    return this.request(`/${type}/channels/scheduler/diagnose`, {
      method: 'POST',
      body: JSON.stringify(payload)
    })
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

  async suspendChatApiKey(channelId: number, apiKey: string): Promise<void> {
    await this.request(`/chat/channels/${channelId}/keys/suspend`, {
      method: 'POST',
      body: JSON.stringify({ apiKey })
    })
  }

  async resumeChatApiKey(channelId: number, apiKey: string): Promise<void> {
    await this.request(`/chat/channels/${channelId}/keys/resume`, {
      method: 'POST',
      body: JSON.stringify({ apiKey })
    })
  }

  async restoreChatKeyModel(channelId: number, apiKey: string, model: string): Promise<void> {
    await this.request(`/chat/channels/${channelId}/keys/restore-model`, {
      method: 'POST',
      body: JSON.stringify({ apiKey, model })
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

  async getChatChannelMetricsHistory(duration: string = '24h', interval?: string): Promise<MetricsHistoryResponse[]> {
    return this.request(`/chat/channels/metrics/history?${this.historyQuery(duration, interval)}`)
  }

  async getChatChannelKeyMetricsHistory(channelId: number, duration: string = '6h', interval?: string): Promise<ChannelKeyMetricsHistoryResponse> {
    return this.request(`/chat/channels/${channelId}/keys/metrics/history?${this.historyQuery(duration, interval)}`)
  }

  async getChatGlobalStats(duration: string = '24h', interval?: string): Promise<GlobalStatsHistoryResponse> {
    return this.request(`/chat/global/stats/history?${this.historyQuery(duration, interval)}`)
  }

  async pingChatChannel(id: number): Promise<PingResult> {
    return this.request(`/chat/ping/${id}`)
  }

  async pingAllChatChannels(): Promise<Array<{ id: number; name: string; latency: number; status: string }>> {
    const resp = await this.request('/chat/ping')
    return (resp.channels || []).map((ch: { index: number; name: string; latency: number; success: boolean }) => ({
      id: ch.index,
      name: ch.name,
      latency: ch.latency,
      status: ch.success ? 'healthy' : 'error'
    }))
  }

  async getChatChannelModels(id: number, request: ChannelModelsRequest): Promise<ModelsResponse> {
    return this.request(`/chat/channels/${id}/models`, {
      method: 'POST',
      body: JSON.stringify(request)
    })
  }

  async updateChatChannelModelMapping(id: number, sourcePattern: string, targetModel: string, reasoning: string): Promise<void> {
    await this.request(`/chat/channels/${id}/mappings`, {
      method: 'PUT',
      body: JSON.stringify({ source_pattern: sourcePattern, target_model: targetModel, reasoning })
    })
  }

  // ============== Images 渠道管理 API ==============

  async getImagesChannels(): Promise<ChannelsResponse> {
    return this.request('/images/channels')
  }

  async addImagesChannel(channel: Omit<Channel, 'index' | 'latency' | 'status'>): Promise<void> {
    await this.request('/images/channels', {
      method: 'POST',
      body: JSON.stringify(channel)
    })
  }

  async updateImagesChannel(id: number, channel: Partial<Channel>): Promise<void> {
    await this.request(`/images/channels/${id}`, {
      method: 'PUT',
      body: JSON.stringify(channel)
    })
  }

  async deleteImagesChannel(id: number): Promise<void> {
    await this.request(`/images/channels/${id}`, {
      method: 'DELETE'
    })
  }

  async addImagesApiKey(channelId: number, apiKey: string): Promise<void> {
    await this.request(`/images/channels/${channelId}/keys`, {
      method: 'POST',
      body: JSON.stringify({ apiKey })
    })
  }

  async removeImagesApiKey(channelId: number, apiKey: string): Promise<void> {
    await this.request(`/images/channels/${channelId}/keys/${encodeURIComponent(apiKey)}`, {
      method: 'DELETE'
    })
  }

  async restoreImagesApiKey(channelId: number, apiKey: string): Promise<void> {
    await this.request(`/images/channels/${channelId}/keys/restore`, {
      method: 'POST',
      body: JSON.stringify({ apiKey })
    })
  }

  async suspendImagesApiKey(channelId: number, apiKey: string): Promise<void> {
    await this.request(`/images/channels/${channelId}/keys/suspend`, {
      method: 'POST',
      body: JSON.stringify({ apiKey })
    })
  }

  async resumeImagesApiKey(channelId: number, apiKey: string): Promise<void> {
    await this.request(`/images/channels/${channelId}/keys/resume`, {
      method: 'POST',
      body: JSON.stringify({ apiKey })
    })
  }

  async restoreImagesKeyModel(channelId: number, apiKey: string, model: string): Promise<void> {
    await this.request(`/images/channels/${channelId}/keys/restore-model`, {
      method: 'POST',
      body: JSON.stringify({ apiKey, model })
    })
  }

  async moveImagesApiKeyToTop(channelId: number, apiKey: string): Promise<void> {
    await this.request(`/images/channels/${channelId}/keys/${encodeURIComponent(apiKey)}/top`, {
      method: 'POST'
    })
  }

  async moveImagesApiKeyToBottom(channelId: number, apiKey: string): Promise<void> {
    await this.request(`/images/channels/${channelId}/keys/${encodeURIComponent(apiKey)}/bottom`, {
      method: 'POST'
    })
  }

  async reorderImagesChannels(order: number[]): Promise<void> {
    await this.request('/images/channels/reorder', {
      method: 'POST',
      body: JSON.stringify({ order })
    })
  }

  async setImagesChannelStatus(channelId: number, status: ChannelStatus): Promise<void> {
    await this.request(`/images/channels/${channelId}/status`, {
      method: 'PATCH',
      body: JSON.stringify({ status })
    })
  }

  async resumeImagesChannel(channelId: number): Promise<ResumeChannelResponse> {
    return this.request(`/images/channels/${channelId}/resume`, {
      method: 'POST'
    })
  }

  async getImagesChannelMetrics(): Promise<ChannelMetrics[]> {
    return this.request('/images/channels/metrics')
  }

  async setImagesChannelPromotion(channelId: number, durationSeconds: number): Promise<void> {
    await this.request(`/images/channels/${channelId}/promotion`, {
      method: 'POST',
      body: JSON.stringify({ duration: durationSeconds })
    })
  }

  async getImagesChannelMetricsHistory(duration: string = '24h', interval?: string): Promise<MetricsHistoryResponse[]> {
    return this.request(`/images/channels/metrics/history?${this.historyQuery(duration, interval)}`)
  }

  async getImagesChannelKeyMetricsHistory(channelId: number, duration: string = '6h', interval?: string): Promise<ChannelKeyMetricsHistoryResponse> {
    return this.request(`/images/channels/${channelId}/keys/metrics/history?${this.historyQuery(duration, interval)}`)
  }

  async getImagesGlobalStats(duration: string = '24h', interval?: string): Promise<GlobalStatsHistoryResponse> {
    return this.request(`/images/global/stats/history?${this.historyQuery(duration, interval)}`)
  }

  async pingImagesChannel(id: number): Promise<PingResult> {
    return this.request(`/images/ping/${id}`)
  }

  async pingAllImagesChannels(): Promise<Array<{ id: number; name: string; latency: number; status: string }>> {
    const resp = await this.request('/images/ping')
    return (resp.channels || []).map((ch: { index: number; name: string; latency: number; success: boolean }) => ({
      id: ch.index,
      name: ch.name,
      latency: ch.latency,
      status: ch.success ? 'healthy' : 'error'
    }))
  }

  async getImagesChannelModels(id: number, request: ChannelModelsRequest): Promise<ModelsResponse> {
    return this.request(`/images/channels/${id}/models`, {
      method: 'POST',
      body: JSON.stringify(request)
    })
  }

  async updateImagesChannelModelMapping(id: number, sourcePattern: string, targetModel: string, reasoning: string): Promise<void> {
    await this.request(`/images/channels/${id}/mappings`, {
      method: 'PUT',
      body: JSON.stringify({ source_pattern: sourcePattern, target_model: targetModel, reasoning })
    })
  }

  // ============== Vectors 渠道管理 API ==============

  async getVectorsChannels(): Promise<ChannelsResponse> {
    return this.request('/vectors/channels')
  }

  async addVectorsChannel(channel: Omit<Channel, 'index' | 'latency' | 'status'>): Promise<void> {
    await this.request('/vectors/channels', {
      method: 'POST',
      body: JSON.stringify(channel)
    })
  }

  async updateVectorsChannel(id: number, channel: Partial<Channel>): Promise<void> {
    await this.request(`/vectors/channels/${id}`, {
      method: 'PUT',
      body: JSON.stringify(channel)
    })
  }

  async deleteVectorsChannel(id: number): Promise<void> {
    await this.request(`/vectors/channels/${id}`, {
      method: 'DELETE'
    })
  }

  async addVectorsApiKey(channelId: number, apiKey: string): Promise<void> {
    await this.request(`/vectors/channels/${channelId}/keys`, {
      method: 'POST',
      body: JSON.stringify({ apiKey })
    })
  }

  async removeVectorsApiKey(channelId: number, apiKey: string): Promise<void> {
    await this.request(`/vectors/channels/${channelId}/keys/${encodeURIComponent(apiKey)}`, {
      method: 'DELETE'
    })
  }

  async restoreVectorsApiKey(channelId: number, apiKey: string): Promise<void> {
    await this.request(`/vectors/channels/${channelId}/keys/restore`, {
      method: 'POST',
      body: JSON.stringify({ apiKey })
    })
  }

  async suspendVectorsApiKey(channelId: number, apiKey: string): Promise<void> {
    await this.request(`/vectors/channels/${channelId}/keys/suspend`, {
      method: 'POST',
      body: JSON.stringify({ apiKey })
    })
  }

  async resumeVectorsApiKey(channelId: number, apiKey: string): Promise<void> {
    await this.request(`/vectors/channels/${channelId}/keys/resume`, {
      method: 'POST',
      body: JSON.stringify({ apiKey })
    })
  }

  async restoreVectorsKeyModel(channelId: number, apiKey: string, model: string): Promise<void> {
    await this.request(`/vectors/channels/${channelId}/keys/restore-model`, {
      method: 'POST',
      body: JSON.stringify({ apiKey, model })
    })
  }

  async moveVectorsApiKeyToTop(channelId: number, apiKey: string): Promise<void> {
    await this.request(`/vectors/channels/${channelId}/keys/${encodeURIComponent(apiKey)}/top`, {
      method: 'POST'
    })
  }

  async moveVectorsApiKeyToBottom(channelId: number, apiKey: string): Promise<void> {
    await this.request(`/vectors/channels/${channelId}/keys/${encodeURIComponent(apiKey)}/bottom`, {
      method: 'POST'
    })
  }

  async reorderVectorsChannels(order: number[]): Promise<void> {
    await this.request('/vectors/channels/reorder', {
      method: 'POST',
      body: JSON.stringify({ order })
    })
  }

  async setVectorsChannelStatus(channelId: number, status: ChannelStatus): Promise<void> {
    await this.request(`/vectors/channels/${channelId}/status`, {
      method: 'PATCH',
      body: JSON.stringify({ status })
    })
  }

  async resumeVectorsChannel(channelId: number): Promise<ResumeChannelResponse> {
    return this.request(`/vectors/channels/${channelId}/resume`, {
      method: 'POST'
    })
  }

  async getVectorsChannelMetrics(): Promise<ChannelMetrics[]> {
    return this.request('/vectors/channels/metrics')
  }

  async setVectorsChannelPromotion(channelId: number, durationSeconds: number): Promise<void> {
    await this.request(`/vectors/channels/${channelId}/promotion`, {
      method: 'POST',
      body: JSON.stringify({ duration: durationSeconds })
    })
  }

  async getVectorsChannelMetricsHistory(duration: string = '24h', interval?: string): Promise<MetricsHistoryResponse[]> {
    return this.request(`/vectors/channels/metrics/history?${this.historyQuery(duration, interval)}`)
  }

  async getVectorsChannelKeyMetricsHistory(channelId: number, duration: string = '6h', interval?: string): Promise<ChannelKeyMetricsHistoryResponse> {
    return this.request(`/vectors/channels/${channelId}/keys/metrics/history?${this.historyQuery(duration, interval)}`)
  }

  async getVectorsGlobalStats(duration: string = '24h', interval?: string): Promise<GlobalStatsHistoryResponse> {
    return this.request(`/vectors/global/stats/history?${this.historyQuery(duration, interval)}`)
  }

  async pingVectorsChannel(id: number): Promise<PingResult> {
    return this.request(`/vectors/ping/${id}`)
  }

  async pingAllVectorsChannels(): Promise<Array<{ id: number; name: string; latency: number; status: string }>> {
    const resp = await this.request('/vectors/ping')
    return (resp.channels || []).map((ch: { index: number; name: string; latency: number; success: boolean }) => ({
      id: ch.index,
      name: ch.name,
      latency: ch.latency,
      status: ch.success ? 'healthy' : 'error'
    }))
  }

  async getVectorsChannelModels(id: number, request: ChannelModelsRequest): Promise<ModelsResponse> {
    return this.request(`/vectors/channels/${id}/models`, {
      method: 'POST',
      body: JSON.stringify(request)
    })
  }

  async updateVectorsChannelModelMapping(id: number, sourcePattern: string, targetModel: string, reasoning: string): Promise<void> {
    await this.request(`/vectors/channels/${id}/mappings`, {
      method: 'PUT',
      body: JSON.stringify({ source_pattern: sourcePattern, target_model: targetModel, reasoning })
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

  async suspendGeminiApiKey(channelId: number, apiKey: string): Promise<void> {
    await this.request(`/gemini/channels/${channelId}/keys/suspend`, {
      method: 'POST',
      body: JSON.stringify({ apiKey })
    })
  }

  async resumeGeminiApiKey(channelId: number, apiKey: string): Promise<void> {
    await this.request(`/gemini/channels/${channelId}/keys/resume`, {
      method: 'POST',
      body: JSON.stringify({ apiKey })
    })
  }

  async restoreGeminiKeyModel(channelId: number, apiKey: string, model: string): Promise<void> {
    await this.request(`/gemini/channels/${channelId}/keys/restore-model`, {
      method: 'POST',
      body: JSON.stringify({ apiKey, model })
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
  async getGeminiChannelMetricsHistory(duration: string = '24h', interval?: string): Promise<MetricsHistoryResponse[]> {
    return this.request(`/gemini/channels/metrics/history?${this.historyQuery(duration, interval)}`)
  }

  // 获取 Gemini 渠道 Key 级别历史指标
  async getGeminiChannelKeyMetricsHistory(channelId: number, duration: string = '6h', interval?: string): Promise<ChannelKeyMetricsHistoryResponse> {
    return this.request(`/gemini/channels/${channelId}/keys/metrics/history?${this.historyQuery(duration, interval)}`)
  }

  // 获取 Gemini 全局统计历史
  async getGeminiGlobalStats(duration: string = '24h', interval?: string): Promise<GlobalStatsHistoryResponse> {
    return this.request(`/gemini/global/stats/history?${this.historyQuery(duration, interval)}`)
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

  async getGeminiChannelModels(id: number, request: ChannelModelsRequest): Promise<ModelsResponse> {
    return this.request(`/gemini/channels/${id}/models`, {
      method: 'POST',
      body: JSON.stringify(request)
    })
  }

  async updateGeminiChannelModelMapping(id: number, sourcePattern: string, targetModel: string, reasoning: string): Promise<void> {
    await this.request(`/gemini/channels/${id}/mappings`, {
      method: 'PUT',
      body: JSON.stringify({ source_pattern: sourcePattern, target_model: targetModel, reasoning })
    })
  }

  // ============== 会话调度看板 API ==============

  async getConversations(kind?: string): Promise<ConversationsResponse> {
    const params = kind ? `?kind=${kind}` : ''
    return this.request(`/conversations${params}`)
  }

  async setConversationOverride(
    id: string,
    sequence: ChannelSequenceEntry[],
    duration?: number,
    subagentSequence?: ChannelSequenceEntry[],
    clearSubagentSequence = false
  ): Promise<void> {
    const body: Record<string, unknown> = { sequence }
    if (duration !== undefined) {
      body.duration = duration
    }
    if (subagentSequence && subagentSequence.length > 0) {
      body.subagentSequence = subagentSequence
    }
    if (clearSubagentSequence) {
      body.clearSubagentSequence = true
    }
    await this.request(`/conversations/${id}/override`, {
      method: 'POST',
      body: JSON.stringify(body)
    })
  }

  async removeConversationOverride(id: string): Promise<void> {
    await this.request(`/conversations/${id}/override`, {
      method: 'DELETE'
    })
  }

  // ============== 健康中心 API ==============

  async getHealthCenterOverview(): Promise<HealthCenterOverview> {
    return this.request('/health-center/overview')
  }

  async getHealthCenterChannels(): Promise<HealthCenterChannelsResponse> {
    return this.request('/health-center/channels')
  }

  async getHealthCenterChannelEndpoints(channelUid: string): Promise<HealthCenterEndpointsResponse> {
    return this.request(`/health-center/channels/${encodeURIComponent(channelUid)}/endpoints`)
  }

  async refreshEndpointTokenPlanUsage(endpointUid: string): Promise<TokenPlanUsageRefreshResponse> {
    return this.request(`/health-center/endpoints/${encodeURIComponent(endpointUid)}/token-plan-usage/refresh`, {
      method: 'POST'
    })
  }

  // ============== 订阅中心 API ==============

  async getPresets(): Promise<PresetBundle> {
    return this.request('/presets')
  }

  async getSubscriptions(): Promise<SubscriptionsListResponse> {
    return this.request('/subscriptions')
  }

  async getSubscription(uid: string): Promise<SubscriptionItem> {
    return this.request(`/subscriptions/${encodeURIComponent(uid)}`)
  }

  async createSubscription(data: SubscriptionCreateRequest): Promise<SubscriptionItem> {
    return this.request('/subscriptions', {
      method: 'POST',
      body: JSON.stringify(data),
    })
  }

  async updateSubscription(uid: string, data: SubscriptionUpdateRequest): Promise<SubscriptionItem> {
    return this.request(`/subscriptions/${encodeURIComponent(uid)}`, {
      method: 'PUT',
      body: JSON.stringify(data),
    })
  }

  async deleteSubscription(uid: string): Promise<void> {
    await this.request(`/subscriptions/${encodeURIComponent(uid)}`, {
      method: 'DELETE',
    })
  }

  async linkSubscriptionChannel(uid: string, channelUid: string): Promise<SubscriptionItem> {
    return this.request(`/subscriptions/${encodeURIComponent(uid)}/link`, {
      method: 'POST',
      body: JSON.stringify({ channelUid }),
    })
  }

  async unlinkSubscriptionChannel(uid: string, channelUid: string): Promise<SubscriptionItem> {
    return this.request(`/subscriptions/${encodeURIComponent(uid)}/unlink`, {
      method: 'POST',
      body: JSON.stringify({ channelUid }),
    })
  }

  async refreshSubscription(uid: string): Promise<{ subscription: SubscriptionItem; refreshResult: { success: boolean; balance: number; currency: string; errorMessage: string } }> {
    return this.request(`/subscriptions/${encodeURIComponent(uid)}/refresh`, {
      method: 'POST',
    })
  }

  /** 校验 new-api 令牌并预览账户/分组/模型（不落库） */
  async verifyNewApiSubscription(data: NewApiVerifyRequest): Promise<NewApiVerifyResponse> {
    return this.request('/subscriptions/newapi/verify', {
      method: 'POST',
      body: JSON.stringify(data),
    })
  }

  /** 完整接入 new-api：建 profile + 建 key + 建渠道 + 触发 Discovery */
  async provisionNewApiSubscription(data: NewApiProvisionRequest): Promise<NewApiProvisionResponse> {
    return this.request('/subscriptions/newapi/provision', {
      method: 'POST',
      body: JSON.stringify(data),
    })
  }

  /** 获取 new-api 订阅下的账号列表 */
  async getSubscriptionAccounts(uid: string): Promise<NewApiAccountListResponse> {
    return this.request(`/subscriptions/${encodeURIComponent(uid)}/accounts`)
  }

  /** 为 new-api 订阅添加新账号 */
  async addSubscriptionAccount(uid: string, data: NewApiAccountCreateRequest): Promise<NewApiAccountItem> {
    return this.request(`/subscriptions/${encodeURIComponent(uid)}/accounts`, {
      method: 'POST',
      body: JSON.stringify(data),
    })
  }

  /** 删除 new-api 订阅下的账号 */
  async deleteSubscriptionAccount(uid: string, accountUid: string): Promise<void> {
    await this.request(`/subscriptions/${encodeURIComponent(uid)}/accounts/${encodeURIComponent(accountUid)}`, {
      method: 'DELETE',
    })
  }

  /** 刷新 new-api 账号余额 */
  async refreshSubscriptionAccount(uid: string, accountUid: string): Promise<NewApiAccountItem> {
    return this.request(`/subscriptions/${encodeURIComponent(uid)}/accounts/${encodeURIComponent(accountUid)}/refresh`, {
      method: 'POST',
    })
  }

  // ============== 驾驶舱 API ==============

  async getCockpitOverview(): Promise<CockpitOverviewResponse> {
    return this.request('/cockpit/overview')
  }

  // ============== 人工路由意图（试用意图）API ==============

  /** 查询试用意图列表。all=true 返回全部（含 expired/exhausted/disabled），默认只返回 active */
  async getManualIntents(params?: { all?: boolean }): Promise<IntentListResponse> {
    const query = new URLSearchParams()
    if (params?.all) query.set('all', 'true')
    const qs = query.toString()
    return this.request(`/manual-intents${qs ? `?${qs}` : ''}`)
  }

  /** 查询单条试用意图详情 */
  async getManualIntent(uid: string): Promise<ManualRoutingIntent> {
    return this.request(`/manual-intents/${encodeURIComponent(uid)}`)
  }

  /** 创建试用意图 */
  async createManualIntent(data: CreateIntentRequest): Promise<ManualRoutingIntent> {
    return this.request('/manual-intents', {
      method: 'POST',
      body: JSON.stringify(data),
    })
  }

  /** 删除（结束）试用意图 */
  async deleteManualIntent(uid: string): Promise<void> {
    await this.request(`/manual-intents/${encodeURIComponent(uid)}`, {
      method: 'DELETE',
    })
  }

  // ============== 渠道推荐 API（Phase 4 Item 4）==============

  /** 获取渠道推荐列表。proxyKeyMask 缺省时聚合返回全部用户的推荐。 */
  async getRecommendations(params?: { proxyKeyMask?: string; windowDays?: number }): Promise<RecommendationsResponse> {
    const query = new URLSearchParams()
    if (params?.proxyKeyMask) query.set('proxyKeyMask', params.proxyKeyMask)
    if (params?.windowDays) query.set('windowDays', String(params.windowDays))
    const qs = query.toString()
    return this.request(`/autopilot/recommendations${qs ? `?${qs}` : ''}`)
  }

  // ============== Autopilot 智能路由 API ==============

  /** 获取智能路由全局配置 */
  async getSmartRoutingConfig(): Promise<SmartRoutingConfig> {
    return this.request('/smart-routing/config')
  }

  /** 更新智能路由全局配置 */
  async updateSmartRoutingConfig(data: Partial<SmartRoutingConfig>): Promise<SmartRoutingConfig> {
    return this.request('/smart-routing/config', {
      method: 'PUT',
      body: JSON.stringify(data),
    })
  }

  /** 获取路由决策追踪列表 */
  async getAutopilotTraces(params?: {
    limit?: number
    mismatch?: boolean
    release?: string
    cohort?: string
    mode?: string
  }): Promise<AutopilotTraceListResponse> {
    const query = new URLSearchParams()
    if (params?.limit) query.set('limit', String(params.limit))
    if (params?.mismatch) query.set('mismatch', 'true')
    if (params?.release) query.set('release', params.release)
    if (params?.cohort) query.set('cohort', params.cohort)
    if (params?.mode) query.set('mode', params.mode)
    const qs = query.toString()
    return this.request(`/traces${qs ? '?' + qs : ''}`)
  }

  /** 获取单条路由追踪详情 */
  async getAutopilotTraceDetail(traceUid: string): Promise<AutopilotTraceDetailResponse> {
    return this.request(`/traces/${encodeURIComponent(traceUid)}`)
  }

  /** 获取路由追踪统计汇总 */
  async getAutopilotTraceStats(): Promise<AutopilotTraceStats> {
    return this.request('/traces/stats')
  }

  /** 快速添加渠道（自动托管模式） */
  async autoAddChannel(kind: string, data: AutoAddChannelRequest): Promise<AutoAddChannelResponse> {
    return this.request(`/${kind}/channels/auto-add`, {
      method: 'POST',
      body: JSON.stringify(data),
    })
  }

  /** 原子更新自动托管账号名称与凭证集合。 */
  async updateManagedAccount(accountUid: string, data: { name: string; apiKeys: string[] }): Promise<UpdateManagedAccountResponse> {
    return this.request(`/accounts/${encodeURIComponent(accountUid)}`, {
      method: 'PUT',
      body: JSON.stringify(data),
    })
  }

  /** 获取自动托管账号及掩码凭证，不返回明文 Key。 */
  async getManagedAccounts(): Promise<ManagedAccountsResponse> {
    return this.request('/accounts')
  }

  /** 使用托管账号中已保存的 Key 查询 DeepSeek 官方余额，不回传明文 Key。 */
  async getDeepSeekAccountBalances(accountUid: string): Promise<DeepSeekAccountBalancesResponse> {
    return this.request(`/accounts/${encodeURIComponent(accountUid)}/deepseek-balance`)
  }

  /** 向账号增量添加一批凭证。 */
  async addManagedAccountCredentials(accountUid: string, apiKeys: string[]): Promise<UpdateManagedAccountResponse> {
    return this.request(`/accounts/${encodeURIComponent(accountUid)}/credentials`, {
      method: 'POST',
      body: JSON.stringify({ apiKeys }),
    })
  }

  /** 原子增加和删除账号凭证，不回传已有明文 Key。 */
  async patchManagedAccountCredentials(
    accountUid: string,
    data: { addApiKeys: string[]; removeCredentialUids: string[] }
  ): Promise<UpdateManagedAccountResponse> {
    return this.request(`/accounts/${encodeURIComponent(accountUid)}/credentials`, {
      method: 'PATCH',
      body: JSON.stringify(data),
    })
  }

  /** 按稳定凭证 ID 删除账号中的单个 Key。 */
  async deleteManagedAccountCredential(accountUid: string, credentialUid: string): Promise<UpdateManagedAccountResponse> {
    return this.request(`/accounts/${encodeURIComponent(accountUid)}/credentials/${encodeURIComponent(credentialUid)}`, {
      method: 'DELETE',
    })
  }

  /** 为火山套餐推理 Key 绑定管控面 AK/SK，并自动识别 Agent/Coding Plan。 */
  async setVolcengineAccessKey(
    accountUid: string,
    credentialUid: string,
    data: { accessKeyId: string; secretAccessKey: string }
  ): Promise<VolcengineAccessKeyResponse> {
    return this.request(`/accounts/${encodeURIComponent(accountUid)}/credentials/${encodeURIComponent(credentialUid)}/volcengine-access-key`, {
      method: 'PUT',
      body: JSON.stringify(data),
    })
  }

  /** 删除火山套餐推理 Key 绑定的管控面 AK/SK。 */
  async clearVolcengineAccessKey(accountUid: string, credentialUid: string): Promise<void> {
    await this.request(`/accounts/${encodeURIComponent(accountUid)}/credentials/${encodeURIComponent(credentialUid)}/volcengine-access-key`, {
      method: 'DELETE',
    })
  }

  /** 刷新火山套餐用量快照（后端带 TTL 缓存）。 */
  async refreshVolcenginePlanUsage(accountUid: string, credentialUid: string): Promise<VolcenginePlanUsageRefreshResponse> {
    return this.request(`/accounts/${encodeURIComponent(accountUid)}/credentials/${encodeURIComponent(credentialUid)}/volcengine-plan-usage/refresh`, {
      method: 'POST',
    })
  }

  async setMiMoConsoleCookie(
    accountUid: string,
    credentialUid: string,
    data: { cookie: string; adoptCookieKey?: boolean }
  ): Promise<MiMoConsoleCookieResponse> {
    return this.request(`/accounts/${encodeURIComponent(accountUid)}/credentials/${encodeURIComponent(credentialUid)}/mimo-console-cookie`, {
      method: 'PUT',
      body: JSON.stringify(data),
    })
  }

  async refreshMiMoConsoleCookie(accountUid: string, credentialUid: string): Promise<{ tokenPlan: MiMoConsoleCookieResponse['tokenPlan'] }> {
    return this.request(`/accounts/${encodeURIComponent(accountUid)}/credentials/${encodeURIComponent(credentialUid)}/mimo-console-cookie/refresh`, {
      method: 'POST',
    })
  }

  async clearMiMoConsoleCookie(accountUid: string, credentialUid: string): Promise<void> {
    await this.request(`/accounts/${encodeURIComponent(accountUid)}/credentials/${encodeURIComponent(credentialUid)}/mimo-console-cookie`, {
      method: 'DELETE',
    })
  }

  async setCompshareConsoleCookie(
    accountUid: string,
    credentialUid: string,
    cookie: string
  ): Promise<CompshareConsoleCookieResponse> {
    return this.request(`/accounts/${encodeURIComponent(accountUid)}/credentials/${encodeURIComponent(credentialUid)}/compshare-console-cookie`, {
      method: 'PUT',
      body: JSON.stringify({ cookie }),
    })
  }

  async refreshCompshareConsoleCookie(
    accountUid: string,
    credentialUid: string
  ): Promise<{ plan: CompshareConsoleCookieResponse['plan'] }> {
    return this.request(`/accounts/${encodeURIComponent(accountUid)}/credentials/${encodeURIComponent(credentialUid)}/compshare-console-cookie/refresh`, {
      method: 'POST',
    })
  }

  async clearCompshareConsoleCookie(accountUid: string, credentialUid: string): Promise<void> {
    await this.request(`/accounts/${encodeURIComponent(accountUid)}/credentials/${encodeURIComponent(credentialUid)}/compshare-console-cookie`, {
      method: 'DELETE',
    })
  }

  async setKimiConsoleToken(
    accountUid: string,
    credentialUid: string,
    accessToken: string
  ): Promise<KimiConsoleTokenResponse> {
    return this.request(`/accounts/${encodeURIComponent(accountUid)}/credentials/${encodeURIComponent(credentialUid)}/kimi-console-token`, {
      method: 'PUT',
      body: JSON.stringify({ accessToken }),
    })
  }

  async refreshKimiConsoleToken(
    accountUid: string,
    credentialUid: string
  ): Promise<{ usage: KimiConsoleTokenResponse['usage'] }> {
    return this.request(`/accounts/${encodeURIComponent(accountUid)}/credentials/${encodeURIComponent(credentialUid)}/kimi-console-token/refresh`, {
      method: 'POST',
    })
  }

  async clearKimiConsoleToken(accountUid: string, credentialUid: string): Promise<void> {
    await this.request(`/accounts/${encodeURIComponent(accountUid)}/credentials/${encodeURIComponent(credentialUid)}/kimi-console-token`, {
      method: 'DELETE',
    })
  }

  /** 级联删除自动托管账号及其全部协议渠道。 */
  async deleteManagedAccount(accountUid: string): Promise<void> {
    await this.request(`/accounts/${encodeURIComponent(accountUid)}`, { method: 'DELETE' })
  }

  /** 查询渠道自动托管状态 */
  async getChannelAutoStatus(kind: string, channelId: number): Promise<ChannelAutoStatusResponse> {
    return this.request(`/${kind}/channels/${channelId}/auto-status`)
  }

  /** 获取内置 provider 模板（模板化添加：选 provider + 输 key） */
  async getProviderTemplates(): Promise<ProviderTemplatesResponse> {
    return this.request('/channels/provider-templates')
  }

  // ============== 成本报表 API（Phase 4 Item 2） ==============

  /** 获取成本报表（按 user/model/key 分组） */
  async getCostReport(groupBy: string = 'user', duration: string = '7d', apiType: string = 'messages'): Promise<CostReportResponse> {
    return this.request(`/reports/cost?groupBy=${groupBy}&duration=${duration}&type=${apiType}`)
  }

  // ============== A/B 测试 API（Phase 4 Item 8） ==============

  /** 获取 A/B 测试结果统计 */
  async getABTestResults(): Promise<ABTestResultsResponse> {
    return this.request('/autopilot/ab-test-results')
  }

  /** 紧急停止 A/B 测试 */
  async emergencyStopABTest(reason?: string): Promise<ABTestEmergencyStopResponse> {
    return this.request('/autopilot/ab-test/emergency-stop', {
      method: 'POST',
      body: JSON.stringify({ reason }),
    })
  }

}

export const api = new ApiService()
export default api
