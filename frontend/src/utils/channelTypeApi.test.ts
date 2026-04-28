import { describe, expect, it, vi } from 'vitest'
import type { ChannelStatus } from '../services/api'
import { getChannelTypeApi, type ManagedChannelType } from './channelTypeApi'

const createApiMock = () => ({
  getChannelMetrics: vi.fn().mockResolvedValue([]),
  getResponsesChannelMetrics: vi.fn().mockResolvedValue([]),
  getChatChannelMetrics: vi.fn().mockResolvedValue([]),
  getGeminiChannelMetrics: vi.fn().mockResolvedValue([]),
  getImagesChannelMetrics: vi.fn().mockResolvedValue([]),
  getSchedulerStats: vi.fn().mockResolvedValue({
    multiChannelMode: true,
    activeChannelCount: 2,
    traceAffinityCount: 1,
    traceAffinityTTL: '30m',
    failureThreshold: 50,
    windowSize: 10,
    circuitRecoveryTime: '15m'
  }),
  reorderChannels: vi.fn().mockResolvedValue(undefined),
  reorderResponsesChannels: vi.fn().mockResolvedValue(undefined),
  reorderChatChannels: vi.fn().mockResolvedValue(undefined),
  reorderGeminiChannels: vi.fn().mockResolvedValue(undefined),
  reorderImagesChannels: vi.fn().mockResolvedValue(undefined),
  setChannelStatus: vi.fn().mockResolvedValue(undefined),
  setResponsesChannelStatus: vi.fn().mockResolvedValue(undefined),
  setChatChannelStatus: vi.fn().mockResolvedValue(undefined),
  setGeminiChannelStatus: vi.fn().mockResolvedValue(undefined),
  setImagesChannelStatus: vi.fn().mockResolvedValue(undefined),
  resumeChannel: vi.fn().mockResolvedValue({ success: true, message: 'ok', restoredKeys: 0 }),
  resumeResponsesChannel: vi.fn().mockResolvedValue({ success: true, message: 'ok', restoredKeys: 1 }),
  resumeChatChannel: vi.fn().mockResolvedValue({ success: true, message: 'ok', restoredKeys: 2 }),
  resumeGeminiChannel: vi.fn().mockResolvedValue({ success: true, message: 'ok', restoredKeys: 3 }),
  resumeImagesChannel: vi.fn().mockResolvedValue({ success: true, message: 'ok', restoredKeys: 4 }),
  setChannelPromotion: vi.fn().mockResolvedValue(undefined),
  setResponsesChannelPromotion: vi.fn().mockResolvedValue(undefined),
  setChatChannelPromotion: vi.fn().mockResolvedValue(undefined),
  setGeminiChannelPromotion: vi.fn().mockResolvedValue(undefined),
  setImagesChannelPromotion: vi.fn().mockResolvedValue(undefined)
})

describe('getChannelTypeApi', () => {
  const cases: Array<{
    type: ManagedChannelType
    metricsMethod: string
    reorderMethod: string
    statusMethod: string
    resumeMethod: string
    promoteMethod: string
  }> = [
    {
      type: 'messages',
      metricsMethod: 'getChannelMetrics',
      reorderMethod: 'reorderChannels',
      statusMethod: 'setChannelStatus',
      resumeMethod: 'resumeChannel',
      promoteMethod: 'setChannelPromotion'
    },
    {
      type: 'responses',
      metricsMethod: 'getResponsesChannelMetrics',
      reorderMethod: 'reorderResponsesChannels',
      statusMethod: 'setResponsesChannelStatus',
      resumeMethod: 'resumeResponsesChannel',
      promoteMethod: 'setResponsesChannelPromotion'
    },
    {
      type: 'chat',
      metricsMethod: 'getChatChannelMetrics',
      reorderMethod: 'reorderChatChannels',
      statusMethod: 'setChatChannelStatus',
      resumeMethod: 'resumeChatChannel',
      promoteMethod: 'setChatChannelPromotion'
    },
    {
      type: 'gemini',
      metricsMethod: 'getGeminiChannelMetrics',
      reorderMethod: 'reorderGeminiChannels',
      statusMethod: 'setGeminiChannelStatus',
      resumeMethod: 'resumeGeminiChannel',
      promoteMethod: 'setGeminiChannelPromotion'
    },
    {
      type: 'images',
      metricsMethod: 'getImagesChannelMetrics',
      reorderMethod: 'reorderImagesChannels',
      statusMethod: 'setImagesChannelStatus',
      resumeMethod: 'resumeImagesChannel',
      promoteMethod: 'setImagesChannelPromotion'
    }
  ]

  it.each(cases)('routes calls to the correct API for $type', async ({ type, metricsMethod, reorderMethod, statusMethod, resumeMethod, promoteMethod }) => {
    const api = createApiMock()
    const helper = getChannelTypeApi(api, type)
    const status: ChannelStatus = 'active'

    await helper.getMetrics()
    const stats = await helper.getSchedulerStats()
    await helper.reorder([1, 2, 3])
    await helper.setStatus(7, status)
    const resumeResult = await helper.resume(7)
    await helper.promote(7, 300)

    expect(api[metricsMethod as keyof typeof api]).toHaveBeenCalledTimes(1)
    expect(api.getSchedulerStats).toHaveBeenCalledWith(type)
    expect(stats.multiChannelMode).toBe(true)
    expect(api[reorderMethod as keyof typeof api]).toHaveBeenCalledWith([1, 2, 3])
    expect(api[statusMethod as keyof typeof api]).toHaveBeenCalledWith(7, status)
    expect(api[resumeMethod as keyof typeof api]).toHaveBeenCalledWith(7)
    expect(api[promoteMethod as keyof typeof api]).toHaveBeenCalledWith(7, 300)
    expect(resumeResult).toEqual((api[resumeMethod as keyof typeof api] as ReturnType<typeof vi.fn>).mock.results[0]?.value ? await (api[resumeMethod as keyof typeof api] as ReturnType<typeof vi.fn>).mock.results[0].value : expect.anything())
    expect(resumeResult.restoredKeys).toBeGreaterThanOrEqual(0)
    expect(resumeResult.success).toBe(true)
  })
})
