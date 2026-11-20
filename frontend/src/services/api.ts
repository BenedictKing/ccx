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
  ChannelDiscoveryRequest,
  ChannelDiscoveryResponse,
  ChannelKeyMetricsHistoryResponse,
  ChannelLogsResponse,
  ChannelMetrics,
  ChannelModelsRequest,
  ChannelSequenceEntry,
  ChannelsResponse,
  ChannelStatus,
  CopilotDeviceCodeResponse,
  CopilotTokenResponse,
  CopilotUserResponse,
  ConversationsResponse,
  GlobalStatsHistoryResponse,
  GroupModelMutationResponse,
  MetricsHistoryResponse,
  ModelStatsHistoryResponse,
  ModelsResponse,
  PingResult,
  ResumeChannelResponse,
  SchedulerStatsResponse,
  SchedulerDiagnoseRequest,
  SchedulerDiagnoseResponse,
  SubagentRoutingView,
  TextChannelKind,
  StartCapabilityTestOptions,
  UsageReportChannel,
  CompatDiagnoseResult
} from './api-types'

export * from './api-helpers'
export * from './api-types'

import type { RoutingDistribution, RoutingQuery, RoutingRequestPage } from "./routing-types"

export class ApiService {
  async getModelRouting(type: string, query: RoutingQuery): Promise<RoutingDistribution> {
    return this.request(`/${type}/models/routing?${this.routingQuery(query)}`)
  }

  async getRoutingRequests(type: string, query: RoutingQuery): Promise<RoutingRequestPage> {
    return this.request(`/${type}/models/routing/requests?${this.routingQuery(query)}`)
  }

  private routingQuery(query: RoutingQuery): string {
    return new URLSearchParams(Object.entries(query).filter(([, value]) => value !== undefined && value !== "").map(([key, value]) => [key, String(value)])).toString()
  }

  async getUsageReportChannels(): Promise<UsageReportChannel[]> {
    return this.request('/reports/usage/channels')
  }

  async downloadUsageReport(month: string, channel: string, granularity: 'hour' | 'day'): Promise<Blob> {
    const params = new URLSearchParams({
      month, channel, granularity,
      timezone: Intl.DateTimeFormat().resolvedOptions().timeZone || 'UTC'
    })
    const headers: Record<string, string> = {}
    const apiKey = this.getApiKey()
    if (apiKey) headers['x-api-key'] = apiKey
    const response = await fetch(`${API_BASE}/reports/usage.csv?${params}`, { headers })
    if (!response.ok) {
      const body = await this.parseResponseBody(response)
      if (response.status === 401) {
        useAuthStore().clearAuth()
        throw new ApiError(this.t('service.authFailed'), 401, body)
      }
      const message = typeof body === 'object' && body && 'error' in body && typeof body.error === 'string'
        ? body.error : `Request failed (${response.status})`
      throw new ApiError(message, response.status, body)
    }
    return response.blob()
  }

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

  async getSubagentRouting(type: TextChannelKind): Promise<SubagentRoutingView> {
    return this.request(`/${type}/channels/subagent-routing`)
  }

  async getProtocolChannelModels(type: TextChannelKind, id: number, request: ChannelModelsRequest): Promise<ModelsResponse> {
    return this.request(`/${type}/channels/${id}/models`, {
      method: 'POST',
      body: JSON.stringify(request)
    })
  }

  async updateSubagentRouting(type: TextChannelKind, revision: string, policy: SubagentRoutingView['policy']): Promise<SubagentRoutingView> {
    return this.request(`/${type}/channels/subagent-routing`, {
      method: 'PUT',
      body: JSON.stringify({ revision, policy })
    })
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

  async setApiKeySuspended(
    channelType: 'messages' | 'chat' | 'responses' | 'gemini' | 'images' | 'vectors',
    channelId: number,
    apiKey: string,
    suspended: boolean
  ): Promise<void> {
    const action = suspended ? 'suspend' : 'resume'
    await this.request(`/${channelType}/channels/${channelId}/keys/${action}`, {
      method: 'POST',
      body: JSON.stringify({ apiKey })
    })
  }

  async updateGroupModelRestriction(
    channelType: 'messages' | 'chat' | 'responses' | 'gemini' | 'images' | 'vectors',
    channelId: number,
    apiKey: string,
    quotaGroup: string,
    model: string,
    disabled: boolean
  ): Promise<GroupModelMutationResponse> {
    const action = disabled ? 'disable' : 'restore'
    return this.request(`/${channelType}/channels/${channelId}/keys/group-model/${action}`, {
      method: 'POST',
      body: JSON.stringify({ apiKey, quotaGroup, model })
    })
  }

  async restoreApiKey(channelId: number, apiKey: string): Promise<void> {
    await this.request(`/messages/channels/${channelId}/keys/restore`, {
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

  // ============== 能力测试 API ==============

  async startChannelCapabilityTest(
    type: 'messages' | 'chat' | 'gemini' | 'responses',
    id: number,
    options: StartCapabilityTestOptions = {}
  ): Promise<CapabilityTestJobStartResponse> {
    const body: { targetProtocols: string[]; timeout: number; previousJobId?: string; rpm?: number; sourceTab?: string; models?: string[]; reasoningEffort?: string; force?: boolean } = {
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
    if (options.reasoningEffort) {
      body.reasoningEffort = options.reasoningEffort
    }
    if (options.force) {
      body.force = true
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

  // ── 站点账号接入（new-api 家族）──────────────────────────────────────────

  async listSiteAccounts(): Promise<{ accounts: import('./api-types').SiteAccount[] }> {
    return this.request('/siteaccess/accounts')
  }

  async upsertSiteAccount(req: import('./api-types').SiteAccountUpsertRequest): Promise<{ account: import('./api-types').SiteAccount }> {
    return this.request('/siteaccess/accounts', { method: 'POST', body: JSON.stringify(req) })
  }

  async deleteSiteAccount(uid: string): Promise<void> {
    await this.request(`/siteaccess/accounts/${uid}`, { method: 'DELETE' })
  }

  async verifySiteAccount(uid: string): Promise<import('./api-types').SiteVerifyResponse> {
    return this.request(`/siteaccess/accounts/${uid}/verify`, { method: 'POST' })
  }

  async getSiteAccountGroups(uid: string): Promise<import('./api-types').SiteGroupsPreviewResponse> {
    return this.request(`/siteaccess/accounts/${uid}/groups`)
  }

  async getSiteAccountKeys(uid: string): Promise<{ keys: import('./api-types').SiteRemoteKey[] }> {
    return this.request(`/siteaccess/accounts/${uid}/keys`)
  }

  async provisionSiteAccount(uid: string, req: import('./api-types').SiteProvisionRequest): Promise<import('./api-types').SiteProvisionResult> {
    return this.request(`/siteaccess/accounts/${uid}/provision`, { method: 'POST', body: JSON.stringify(req) })
  }

  async verifySiteRealRequest(uid: string, req: { protocol: string; group: string; model: string }): Promise<import('./api-types').SiteVerifyRequestResponse> {
    return this.request(`/siteaccess/accounts/${uid}/verify-request`, { method: 'POST', body: JSON.stringify(req) })
  }

  async reconcileSiteAccount(uid: string): Promise<{ actions: import('./api-types').SiteReconcileAction[] }> {
    return this.request(`/siteaccess/accounts/${uid}/reconcile`, { method: 'POST' })
  }

  async getSiteAccountBalance(uid: string): Promise<import('./api-types').SiteBalanceResponse> {
    return this.request(`/siteaccess/accounts/${uid}/balance`)
  }

  async bindSiteKey(protocol: string, index: number, req: { accountUid: string; apiKey: string; group: string; remoteTokenId: number }): Promise<void> {
    await this.request(`/siteaccess/channels/${protocol}/${index}/key-binding`, { method: 'PUT', body: JSON.stringify(req) })
  }

  async unbindSiteKey(protocol: string, index: number, apiKey: string): Promise<void> {
    await this.request(`/siteaccess/channels/${protocol}/${index}/key-unbinding`, { method: 'POST', body: JSON.stringify({ apiKey }) })
  }

  async copySiteChannelGroup(protocol: string, index: number, req: { accountUid: string; apiKey: string; group: string; remoteTokenId: number }): Promise<import('./api-types').SiteChannelGroupCopyResult> {
    return this.request(`/siteaccess/channels/${protocol}/${index}/group-copy`, { method: 'POST', body: JSON.stringify(req) })
  }

}

export const api = new ApiService()
export default api
