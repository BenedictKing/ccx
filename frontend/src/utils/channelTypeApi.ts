import type { ApiService, ChannelMetrics, ChannelStatus, ResumeChannelResponse, SchedulerStatsResponse } from '../services/api'

export type ManagedChannelType = 'messages' | 'chat' | 'responses' | 'gemini' | 'images' | 'vectors'

type ChannelTypeApi = {
  getMetrics: () => Promise<ChannelMetrics[]>
  getSchedulerStats: () => Promise<SchedulerStatsResponse>
  reorder: (_order: number[], _priorities?: number[]) => Promise<void>
  setStatus: (_channelId: number, _status: ChannelStatus) => Promise<void>
  resume: (_channelId: number) => Promise<ResumeChannelResponse>
  promote: (_channelId: number, _durationSeconds: number) => Promise<void>
}

type ChannelApiSubset = Pick<ApiService,
  | 'getChannelMetrics'
  | 'getResponsesChannelMetrics'
  | 'getChatChannelMetrics'
  | 'getGeminiChannelMetrics'
  | 'getImagesChannelMetrics'
  | 'getVectorsChannelMetrics'
  | 'getSchedulerStats'
  | 'reorderChannels'
  | 'reorderResponsesChannels'
  | 'reorderChatChannels'
  | 'reorderGeminiChannels'
  | 'reorderImagesChannels'
  | 'reorderVectorsChannels'
  | 'setChannelStatus'
  | 'setResponsesChannelStatus'
  | 'setChatChannelStatus'
  | 'setGeminiChannelStatus'
  | 'setImagesChannelStatus'
  | 'setVectorsChannelStatus'
  | 'resumeChannel'
  | 'resumeResponsesChannel'
  | 'resumeChatChannel'
  | 'resumeGeminiChannel'
  | 'resumeImagesChannel'
  | 'resumeVectorsChannel'
  | 'setChannelPromotion'
  | 'setResponsesChannelPromotion'
  | 'setChatChannelPromotion'
  | 'setGeminiChannelPromotion'
  | 'setImagesChannelPromotion'
  | 'setVectorsChannelPromotion'
>

export const getChannelTypeApi = (api: ChannelApiSubset, channelType: ManagedChannelType): ChannelTypeApi => {
  switch (channelType) {
    case 'chat':
      return {
        getMetrics: () => api.getChatChannelMetrics(),
        getSchedulerStats: () => api.getSchedulerStats('chat'),
        reorder: (order, priorities) => api.reorderChatChannels(order, priorities),
        setStatus: (channelId, status) => api.setChatChannelStatus(channelId, status),
        resume: (channelId) => api.resumeChatChannel(channelId),
        promote: (channelId, durationSeconds) => api.setChatChannelPromotion(channelId, durationSeconds)
      }
    case 'gemini':
      return {
        getMetrics: () => api.getGeminiChannelMetrics(),
        getSchedulerStats: () => api.getSchedulerStats('gemini'),
        reorder: (order, priorities) => api.reorderGeminiChannels(order, priorities),
        setStatus: (channelId, status) => api.setGeminiChannelStatus(channelId, status),
        resume: (channelId) => api.resumeGeminiChannel(channelId),
        promote: (channelId, durationSeconds) => api.setGeminiChannelPromotion(channelId, durationSeconds)
      }
    case 'images':
      return {
        getMetrics: () => api.getImagesChannelMetrics(),
        getSchedulerStats: () => api.getSchedulerStats('images'),
        reorder: (order, priorities) => api.reorderImagesChannels(order, priorities),
        setStatus: (channelId, status) => api.setImagesChannelStatus(channelId, status),
        resume: (channelId) => api.resumeImagesChannel(channelId),
        promote: (channelId, durationSeconds) => api.setImagesChannelPromotion(channelId, durationSeconds)
      }
    case 'vectors':
      return {
        getMetrics: () => api.getVectorsChannelMetrics(),
        getSchedulerStats: () => api.getSchedulerStats('vectors'),
        reorder: (order, priorities) => api.reorderVectorsChannels(order, priorities),
        setStatus: (channelId, status) => api.setVectorsChannelStatus(channelId, status),
        resume: (channelId) => api.resumeVectorsChannel(channelId),
        promote: (channelId, durationSeconds) => api.setVectorsChannelPromotion(channelId, durationSeconds)
      }
    case 'responses':
      return {
        getMetrics: () => api.getResponsesChannelMetrics(),
        getSchedulerStats: () => api.getSchedulerStats('responses'),
        reorder: (order, priorities) => api.reorderResponsesChannels(order, priorities),
        setStatus: (channelId, status) => api.setResponsesChannelStatus(channelId, status),
        resume: (channelId) => api.resumeResponsesChannel(channelId),
        promote: (channelId, durationSeconds) => api.setResponsesChannelPromotion(channelId, durationSeconds)
      }
    default:
      return {
        getMetrics: () => api.getChannelMetrics(),
        getSchedulerStats: () => api.getSchedulerStats('messages'),
        reorder: (order, priorities) => api.reorderChannels(order, priorities),
        setStatus: (channelId, status) => api.setChannelStatus(channelId, status),
        resume: (channelId) => api.resumeChannel(channelId),
        promote: (channelId, durationSeconds) => api.setChannelPromotion(channelId, durationSeconds)
      }
  }
}
