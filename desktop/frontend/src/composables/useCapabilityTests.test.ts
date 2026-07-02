import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { nextTick } from 'vue'
import { AdminApiError } from '@/composables/useAdminApi'
import { useCapabilityTests } from './useCapabilityTests'
import type { CapabilityTestJob } from '@/services/admin-api'

const apiPost = vi.fn()
const apiGet = vi.fn()

vi.mock('@/composables/useAdminApi', async () => {
  const actual = await vi.importActual<typeof import('@/composables/useAdminApi')>('@/composables/useAdminApi')
  return {
    AdminApiError: actual.AdminApiError,
    useAdminApi: () => ({
      post: apiPost,
      get: apiGet,
      del: vi.fn(),
    }),
  }
})

vi.mock('@/composables/useConsoleChannels', () => ({
  useConsoleChannels: () => ({
    refreshChannels: vi.fn(),
  }),
}))

describe('useCapabilityTests', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    useCapabilityTests().reset()
  })

  afterEach(() => {
    useCapabilityTests().reset()
  })

  it('polls job status immediately after starting a protocol test', async () => {
    const capability = useCapabilityTests()
    capability.prepareChannelSession('messages', 1, 'channel')

    apiPost.mockResolvedValueOnce({
      jobId: 'job-1',
      job: buildJob('job-1', [
        {
          model: 'claude-a',
          status: 'queued',
          lifecycle: 'pending',
          outcome: 'unknown',
          success: false,
          latency: 0,
          streamingSupported: false,
        },
      ], 'queued', 'pending'),
    })
    apiGet.mockResolvedValueOnce(buildJob('job-1', [
      {
        model: 'claude-a',
        status: 'running',
        lifecycle: 'active',
        outcome: 'unknown',
        success: false,
        latency: 0,
        streamingSupported: false,
      },
    ], 'running', 'active'))

    await capability.startProtocolTest('messages', 1, 'messages', undefined, 10)

    expect(apiGet).toHaveBeenCalledWith('/api/messages/channels/1/capability-test/job-1')
  })

  it('falls back to a single-model protocol test when retry job does not contain that model', async () => {
    const capability = useCapabilityTests()
    capability.prepareChannelSession('messages', 1, 'channel')

    const completedJob = buildJob('job-1', [
      {
        model: 'claude-a',
        status: 'failed',
        lifecycle: 'done',
        outcome: 'failed',
        success: false,
        latency: 0,
        streamingSupported: false,
      },
      {
        model: 'claude-b',
        status: 'idle',
        lifecycle: 'pending',
        outcome: 'unknown',
        success: false,
        latency: 0,
        streamingSupported: false,
      },
    ])
    capability.activeJob.value = completedJob

    apiPost.mockRejectedValueOnce(new AdminApiError('Model not found in job', 404))
    apiPost.mockResolvedValueOnce({
      jobId: 'job-2',
      job: buildJob('job-2', [
        {
          model: 'claude-b',
          status: 'queued',
          lifecycle: 'pending',
          outcome: 'unknown',
          success: false,
          latency: 0,
          streamingSupported: false,
        },
      ], 'queued', 'pending'),
    })

    await capability.retryModelForProtocol('messages', 1, 'messages', 'claude-b')
    await nextTick()

    expect(apiPost).toHaveBeenNthCalledWith(
      1,
      '/api/messages/channels/1/capability-test/job-1/retry',
      { protocol: 'messages', model: 'claude-b' },
    )
    expect(apiPost).toHaveBeenNthCalledWith(
      2,
      '/api/messages/channels/1/capability-test',
      expect.objectContaining({
        targetProtocols: ['messages'],
        models: ['claude-b'],
        previousJobId: 'job-1',
      }),
    )
    const retriedModel = capability.activeJob.value?.tests
      .find(test => test.protocol === 'messages')
      ?.modelResults?.find(modelResult => modelResult.model === 'claude-b')
    expect(retriedModel?.status).toBe('queued')
  })

  it('falls back with all source models mapped to the same actual model', async () => {
    const capability = useCapabilityTests()
    capability.prepareChannelSession('responses', 1, 'channel')

    capability.activeJob.value = buildJob('job-1', [
      {
        model: 'gpt-5.5',
        status: 'success',
        lifecycle: 'done',
        outcome: 'success',
        success: true,
        latency: 120,
        streamingSupported: true,
      },
      {
        model: 'gpt-5.4-mini',
        actualModel: 'gpt-5.5',
        status: 'failed',
        lifecycle: 'done',
        outcome: 'failed',
        success: false,
        latency: 0,
        streamingSupported: false,
      },
    ], 'completed', 'done', 'responses', 'responses->chat')

    apiPost.mockRejectedValueOnce(new AdminApiError('Model not found in job', 404))
    apiPost.mockResolvedValueOnce({
      jobId: 'job-2',
      job: buildJob('job-2', [
        {
          model: 'gpt-5.5',
          status: 'queued',
          lifecycle: 'pending',
          outcome: 'unknown',
          success: false,
          latency: 0,
          streamingSupported: false,
        },
        {
          model: 'gpt-5.4-mini',
          actualModel: 'gpt-5.5',
          status: 'queued',
          lifecycle: 'pending',
          outcome: 'unknown',
          success: false,
          latency: 0,
          streamingSupported: false,
        },
      ], 'queued', 'pending', 'responses', 'responses->chat'),
    })

    await capability.retryModelForProtocol('responses', 1, 'responses->chat', 'gpt-5.4-mini')
    await nextTick()

    expect(apiPost).toHaveBeenNthCalledWith(
      2,
      '/api/responses/channels/1/capability-test',
      expect.objectContaining({
        targetProtocols: ['responses->chat'],
        models: ['gpt-5.5', 'gpt-5.4-mini'],
        previousJobId: 'job-1',
      }),
    )
    const modelResults = capability.activeJob.value?.tests.find(test => test.protocol === 'responses->chat')?.modelResults ?? []
    expect(modelResults.map(result => [result.model, result.status])).toEqual([
      ['gpt-5.5', 'queued'],
      ['gpt-5.4-mini', 'queued'],
    ])
  })

  it('merges redirect results across all source models sharing an actual model', async () => {
    const capability = useCapabilityTests()
    capability.prepareChannelSession('responses', 1, 'channel')

    const oldTime = '2026-01-01T00:00:00.000Z'
    const newTime = '2026-01-01T00:01:00.000Z'
    capability.activeJob.value = buildJob('job-1', [
      {
        model: 'gpt-5.5',
        status: 'failed',
        lifecycle: 'done',
        outcome: 'failed',
        success: false,
        latency: 0,
        streamingSupported: false,
        testedAt: oldTime,
      },
      {
        model: 'gpt-5.4-mini',
        actualModel: 'gpt-5.5',
        status: 'failed',
        lifecycle: 'done',
        outcome: 'failed',
        success: false,
        latency: 0,
        streamingSupported: false,
        testedAt: oldTime,
      },
    ], 'completed', 'done', 'responses', 'responses->chat')

    apiGet.mockResolvedValueOnce(buildJob('job-1', [
      {
        model: 'gpt-5.4-mini',
        actualModel: 'gpt-5.5',
        status: 'success',
        lifecycle: 'done',
        outcome: 'success',
        success: true,
        latency: 88,
        streamingSupported: true,
        testedAt: newTime,
      },
    ], 'completed', 'done', 'responses', 'responses->chat'))

    await capability.fetchJobStatus('responses', 1, 'job-1')

    const modelResults = capability.activeJob.value?.tests.find(test => test.protocol === 'responses->chat')?.modelResults ?? []
    expect(modelResults).toHaveLength(2)
    expect(modelResults.map(result => [result.model, result.status, result.actualModel, result.latency])).toEqual([
      ['gpt-5.5', 'success', 'gpt-5.5', 88],
      ['gpt-5.4-mini', 'success', 'gpt-5.5', 88],
    ])
  })
})

function buildJob(
  jobId: string,
  modelResults: CapabilityTestJob['tests'][number]['modelResults'],
  status: CapabilityTestJob['status'] = 'completed',
  lifecycle: CapabilityTestJob['lifecycle'] = 'done',
  channelKind: string = 'messages',
  protocol: string = 'messages',
): CapabilityTestJob {
  const protocolStatus = status === 'queued'
    ? 'queued'
    : status === 'running'
      ? 'running'
      : status === 'completed'
        ? 'completed'
        : 'failed'
  const jobOutcome = lifecycle === 'done'
    ? status === 'completed' ? 'failed' : 'unknown'
    : 'unknown'
  return {
    jobId,
    channelId: 1,
    channelName: 'channel',
    channelKind,
    sourceType: channelKind,
    status,
    lifecycle,
    outcome: jobOutcome,
    runMode: 'fresh',
    tests: [{
      protocol,
      status: protocolStatus,
      lifecycle,
      outcome: status === 'completed' ? 'failed' : 'unknown',
      success: false,
      latency: 0,
      streamingSupported: false,
      testedModel: '',
      modelResults,
      successCount: 0,
      attemptedModels: modelResults?.length ?? 0,
      testedAt: new Date().toISOString(),
    }],
    compatibleProtocols: [],
    totalDuration: 0,
    updatedAt: new Date().toISOString(),
    targetProtocols: [protocol],
    protocolJobIds: { [protocol]: jobId },
    protocolJobRefs: { [protocol]: { jobId, channelKind: channelKind as 'messages', channelId: 1 } },
    progress: {
      totalModels: modelResults?.length ?? 0,
      queuedModels: status === 'queued' ? modelResults?.length ?? 0 : 0,
      runningModels: 0,
      successModels: 0,
      failedModels: status === 'completed' ? modelResults?.length ?? 0 : 0,
      skippedModels: 0,
      completedModels: status === 'completed' ? modelResults?.length ?? 0 : 0,
    },
  }
}
