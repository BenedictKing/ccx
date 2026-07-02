import { ref, computed } from 'vue'
import { AdminApiError, useAdminApi } from '@/composables/useAdminApi'
import { useConsoleChannels } from '@/composables/useConsoleChannels'
import type {
  CapabilitySnapshot,
  CapabilityTestJob,
  CapabilityTestJobStartResponse,
  CapabilityProtocolJobResult,
  CapabilityModelJobResult,
  CapabilityLifecycle,
  CapabilityOutcome,
  Channel,
} from '@/services/admin-api'
import { getChannelTypeApi } from '@/utils/channel-type-api'
import type { ManagedChannelType } from '@/utils/channel-type-api'

// Module-level singletons
const activeJob = ref<CapabilityTestJob | null>(null)
const snapshot = ref<CapabilitySnapshot | null>(null)
const loading = ref(false)
const polling = ref(false)
const cancelling = ref(false)
const error = ref('')
const pollers = new Map<string, ReturnType<typeof setInterval>>()
const POLL_INTERVAL = 1000
const BASE_PROTOCOL_ORDER = ['messages', 'responses', 'chat', 'gemini'] as const
type CapabilityChannelKind = typeof BASE_PROTOCOL_ORDER[number]
type CopyToTabResult = { ok: true } | { ok: false; message: string }
const MANAGED_CHANNEL_TYPES = ['messages', 'chat', 'responses', 'gemini', 'images', 'vectors'] as const
const PLACEHOLDER_MODELS: Record<string, string[]> = {
  // 修改此处时需要同步后端 backend-go/internal/handlers/capability_probe_models.go
  messages: ['claude-fable-5', 'claude-opus-4-8', 'claude-opus-4-7', 'claude-opus-4-6', 'claude-sonnet-4-6', 'claude-sonnet-4-5-20250929', 'claude-haiku-4-5-20251001'],
  chat: ['gpt-5.5', 'gpt-5.4', 'gpt-5.4-mini', 'codex-auto-review'],
  responses: ['gpt-5.5', 'gpt-5.4', 'gpt-5.4-mini', 'codex-auto-review'],
  gemini: ['gemini-3.5-flash', 'gemini-3.1-pro-preview', 'gemini-3-pro-preview', 'gemini-3-flash-preview', 'gemini-3.1-flash-lite'],
}

function isCapabilityChannelKind(value: string): value is CapabilityChannelKind {
  return BASE_PROTOCOL_ORDER.includes(value as CapabilityChannelKind)
}

function isManagedChannelType(value: string): value is ManagedChannelType {
  return MANAGED_CHANNEL_TYPES.includes(value as ManagedChannelType)
}

function isCapabilityProtocol(protocol: string): boolean {
  if (BASE_PROTOCOL_ORDER.includes(protocol as CapabilityChannelKind)) return true
  if (protocol.includes('->')) {
    const [from] = protocol.split('->')
    return BASE_PROTOCOL_ORDER.includes(from as CapabilityChannelKind)
  }
  return false
}

function buildCapabilityModels(protocol: string, status: CapabilityModelJobResult['status'], models?: string[]): CapabilityModelJobResult[] {
  const now = new Date().toISOString()
  const targetModels = models ?? getPlaceholderModelsForProtocol(protocol)
  return targetModels.map(model => ({
    model,
    status,
    lifecycle: status === 'running' ? 'active' : 'pending',
    outcome: 'unknown',
    success: false,
    latency: 0,
    streamingSupported: false,
    testedAt: now,
  }))
}

function getPlaceholderModelsForProtocol(protocol: string): string[] {
  if (protocol.includes('->')) {
    const [from] = protocol.split('->')
    return PLACEHOLDER_MODELS[from] ?? []
  }
  return PLACEHOLDER_MODELS[protocol] ?? []
}

function buildCapabilityProtocolResult(
  protocol: string,
  status: CapabilityProtocolJobResult['status'],
  models?: string[],
): CapabilityProtocolJobResult {
  const now = new Date().toISOString()
  const modelStatus: CapabilityModelJobResult['status'] = status === 'running' ? 'running' : status === 'queued' ? 'queued' : 'idle'
  const modelResults = buildCapabilityModels(protocol, modelStatus, models)
  return {
    protocol,
    status,
    lifecycle: status === 'running' ? 'active' : 'pending',
    outcome: 'unknown',
    success: false,
    latency: 0,
    streamingSupported: false,
    testedModel: '',
    modelResults,
    successCount: 0,
    attemptedModels: modelResults.length,
    testedAt: now,
  }
}

function toRetryingCapabilityModel(modelResult: CapabilityModelJobResult): CapabilityModelJobResult {
  return {
    ...modelResult,
    status: 'running',
    lifecycle: 'active',
    outcome: 'unknown',
    success: false,
    error: undefined,
    reason: undefined,
  }
}

function getCapabilityModelActual(modelResult: CapabilityModelJobResult): string {
  return modelResult.actualModel || modelResult.model
}

function findCapabilityRedirectActual(test: CapabilityProtocolJobResult, model: string): string {
  const modelResult = (test.modelResults ?? []).find(result => result.model === model)
  return modelResult ? getCapabilityModelActual(modelResult) : model
}

function getCapabilityRedirectGroupModels(test: CapabilityProtocolJobResult, model: string): string[] {
  const actualModel = findCapabilityRedirectActual(test, model)
  const groupModels = (test.modelResults ?? [])
    .filter(modelResult => getCapabilityModelActual(modelResult) === actualModel)
    .map(modelResult => modelResult.model)
  return groupModels.length > 0 ? groupModels : [model]
}

function getCapabilityModelEventTime(modelResult: CapabilityModelJobResult): number {
  const timestamps = [modelResult.startedAt, modelResult.testedAt]
    .map(value => value ? Date.parse(value) : Number.NaN)
    .filter(Number.isFinite)
  return timestamps.length > 0 ? Math.max(...timestamps) : 0
}

function getCapabilityModelStateWeight(modelResult: CapabilityModelJobResult): number {
  if (modelResult.lifecycle === 'active' || modelResult.status === 'running') return 5
  if (modelResult.lifecycle === 'pending' || modelResult.status === 'queued') return 4
  if (modelResult.status === 'success' || modelResult.outcome === 'success') return 3
  if (modelResult.status === 'failed' || modelResult.outcome === 'failed') return 2
  if (modelResult.status === 'skipped' || modelResult.lifecycle === 'cancelled') return 1
  return 0
}

function pickLatestCapabilityGroupResult(modelResults: CapabilityModelJobResult[]): CapabilityModelJobResult {
  return modelResults.reduce((latest, candidate) => {
    const latestTime = getCapabilityModelEventTime(latest)
    const candidateTime = getCapabilityModelEventTime(candidate)
    if (candidateTime !== latestTime) {
      return candidateTime > latestTime ? candidate : latest
    }
    return getCapabilityModelStateWeight(candidate) > getCapabilityModelStateWeight(latest) ? candidate : latest
  })
}

function applyCapabilityRedirectGroupState(test: CapabilityProtocolJobResult): CapabilityProtocolJobResult {
  if (!test.protocol.includes('->') || !test.modelResults?.length) return test

  const groupsByActual = new Map<string, CapabilityModelJobResult[]>()
  for (const modelResult of test.modelResults) {
    const actualModel = getCapabilityModelActual(modelResult)
    groupsByActual.set(actualModel, [...(groupsByActual.get(actualModel) ?? []), modelResult])
  }

  const latestByActual = new Map<string, CapabilityModelJobResult>()
  for (const [actualModel, groupResults] of groupsByActual.entries()) {
    if (groupResults.length < 2 && !groupResults.some(result => result.actualModel && result.actualModel !== result.model)) continue
    latestByActual.set(actualModel, pickLatestCapabilityGroupResult(groupResults))
  }
  if (latestByActual.size === 0) return test

  const modelResults = test.modelResults.map(modelResult => {
    const actualModel = getCapabilityModelActual(modelResult)
    const latestResult = latestByActual.get(actualModel)
    if (!latestResult) return modelResult
    return {
      ...latestResult,
      model: modelResult.model,
      actualModel: modelResult.actualModel || (actualModel !== modelResult.model ? actualModel : latestResult.actualModel),
    }
  })

  return {
    ...test,
    modelResults,
    attemptedModels: modelResults.filter(modelResult => (modelResult.status as string) !== 'idle').length,
    successCount: modelResults.filter(modelResult => modelResult.status === 'success' || modelResult.outcome === 'success').length,
  }
}

function markCapabilityModelRetrying(job: CapabilityTestJob, protocol: string, model: string): CapabilityTestJob {
  return {
    ...job,
    tests: job.tests.map(test => {
      if (test.protocol !== protocol) return test
      const retryModels = new Set(getCapabilityRedirectGroupModels(test, model))
      const existingModels = new Set((test.modelResults ?? []).map(modelResult => modelResult.model))
      const modelResults = (test.modelResults ?? []).map(modelResult => {
        if (!retryModels.has(modelResult.model)) return modelResult
        return toRetryingCapabilityModel(modelResult)
      })
      if (!existingModels.has(model)) {
        modelResults.push(toRetryingCapabilityModel({
          model,
          status: 'idle',
          lifecycle: 'pending',
          outcome: 'unknown',
          success: false,
          latency: 0,
          streamingSupported: false,
          testedAt: new Date().toISOString(),
        }))
      }
      return applyCapabilityRedirectGroupState({
        ...test,
        status: 'running',
        lifecycle: 'active',
        outcome: 'unknown',
        success: false,
        error: undefined,
        reason: undefined,
        modelResults,
      })
    }),
  }
}

function isIdleCapabilityTest(test: CapabilityProtocolJobResult): boolean {
  return (test.status as string) === 'idle'
}

function isActiveCapabilityTest(test: CapabilityProtocolJobResult): boolean {
  return !isIdleCapabilityTest(test) && (test.lifecycle === 'active' || test.status === 'running')
}

function isPendingCapabilityTest(test: CapabilityProtocolJobResult): boolean {
  return !isIdleCapabilityTest(test) && (test.lifecycle === 'pending' || test.status === 'queued')
}

function isSuccessfulCapabilityTest(test: CapabilityProtocolJobResult): boolean {
  return test.success || test.outcome === 'success'
}

function mergeCapabilityProtocolResult(baseTest: CapabilityProtocolJobResult, incomingTest: CapabilityProtocolJobResult): CapabilityProtocolJobResult {
  const modelResultsByModel = new Map<string, CapabilityModelJobResult>()
  for (const modelResult of baseTest.modelResults ?? []) {
    modelResultsByModel.set(modelResult.model, modelResult)
  }
  for (const modelResult of incomingTest.modelResults ?? []) {
    modelResultsByModel.set(modelResult.model, modelResult)
  }
  const modelResults = Array.from(modelResultsByModel.values())

  return applyCapabilityRedirectGroupState({
    ...baseTest,
    ...incomingTest,
    modelResults,
    attemptedModels: modelResults.filter(modelResult => (modelResult.status as string) !== 'idle').length,
    successCount: modelResults.filter(modelResult => modelResult.status === 'success' || modelResult.outcome === 'success').length,
  })
}

function normalizeCapabilityTests(tests: CapabilityProtocolJobResult[]): CapabilityProtocolJobResult[] {
  const testsByProtocol = new Map<string, CapabilityProtocolJobResult>()

  for (const test of tests) {
    if (!isCapabilityProtocol(test.protocol)) continue
    const existingTest = testsByProtocol.get(test.protocol)
    testsByProtocol.set(test.protocol, existingTest ? mergeCapabilityProtocolResult(existingTest, test) : test)
  }

  const compositeTests = Array.from(testsByProtocol.values()).filter(test => test.protocol.includes('->'))
  const baseTests = BASE_PROTOCOL_ORDER.map(protocol =>
    testsByProtocol.get(protocol) ?? buildCapabilityProtocolResult(protocol, 'idle')
  )

  return [...compositeTests, ...baseTests]
}

function buildCapabilityProgress(tests: CapabilityProtocolJobResult[]) {
  const progress = {
    totalModels: 0,
    queuedModels: 0,
    runningModels: 0,
    successModels: 0,
    failedModels: 0,
    skippedModels: 0,
    completedModels: 0,
  }

  for (const test of tests) {
    for (const modelResult of test.modelResults ?? []) {
      progress.totalModels += 1
      if ((modelResult.status as string) === 'idle') continue
      if (modelResult.lifecycle === 'active' || modelResult.status === 'running') {
        progress.runningModels += 1
        continue
      }
      if (modelResult.lifecycle === 'pending') {
        progress.queuedModels += 1
        continue
      }
      if (modelResult.status === 'success' || modelResult.outcome === 'success') {
        progress.successModels += 1
        progress.completedModels += 1
        continue
      }
      if (modelResult.status === 'skipped' || modelResult.lifecycle === 'cancelled') {
        progress.skippedModels += 1
        progress.completedModels += 1
        continue
      }
      progress.failedModels += 1
      progress.completedModels += 1
    }
  }

  return progress
}

function getCapabilityAggregateState(tests: CapabilityProtocolJobResult[]): {
  status: CapabilityTestJob['status']
  lifecycle: CapabilityTestJob['lifecycle']
  outcome: CapabilityTestJob['outcome']
  activeOperations: number
} {
  const nonIdleTests = tests.filter(test => !isIdleCapabilityTest(test))
  const activeOperations = tests.filter(isActiveCapabilityTest).length
  if (nonIdleTests.length === 0) {
    return { status: 'idle', lifecycle: 'pending', outcome: 'unknown', activeOperations: 0 }
  }
  if (activeOperations > 0) {
    return { status: 'running', lifecycle: 'active', outcome: 'unknown', activeOperations }
  }
  if (tests.some(isPendingCapabilityTest)) {
    return { status: 'queued', lifecycle: 'pending', outcome: 'unknown', activeOperations: 0 }
  }

  const cancelledCount = nonIdleTests.filter(test => test.lifecycle === 'cancelled' || test.outcome === 'cancelled').length
  if (cancelledCount === nonIdleTests.length) {
    return { status: 'cancelled', lifecycle: 'cancelled', outcome: 'cancelled', activeOperations: 0 }
  }

  const successCount = nonIdleTests.filter(isSuccessfulCapabilityTest).length
  if (successCount === 0) {
    return { status: 'failed', lifecycle: 'done', outcome: 'failed', activeOperations: 0 }
  }

  return {
    status: 'completed',
    lifecycle: 'done',
    outcome: successCount === nonIdleTests.length ? 'success' : 'partial',
    activeOperations: 0,
  }
}

function buildCapabilityIdleJob(channelType: string, channelId: number, channelName: string): CapabilityTestJob {
  const now = new Date().toISOString()
  const channelKind = isCapabilityChannelKind(channelType) ? channelType : 'messages'
  const tests = BASE_PROTOCOL_ORDER.map(protocol => buildCapabilityProtocolResult(protocol, 'idle'))
  return {
    jobId: '',
    channelId,
    channelName,
    channelKind,
    sourceType: '',
    status: 'idle',
    lifecycle: 'pending',
    outcome: 'unknown',
    runMode: 'fresh',
    tests,
    compatibleProtocols: [],
    totalDuration: 0,
    updatedAt: now,
    targetProtocols: [...BASE_PROTOCOL_ORDER],
    progress: buildCapabilityProgress(tests),
  }
}

function mergeCapabilityJob(baseJob: CapabilityTestJob, incomingJob: CapabilityTestJob): CapabilityTestJob {
  const tests = normalizeCapabilityTests([
    ...baseJob.tests,
    ...incomingJob.tests,
  ])
  const aggregate = getCapabilityAggregateState(tests)
  const protocolsInIncoming = incomingJob.tests
    .map(test => test.protocol)
    .filter(isCapabilityProtocol)
  const protocolJobIds = { ...(baseJob.protocolJobIds ?? {}), ...(incomingJob.protocolJobIds ?? {}) }
  const protocolJobRefs = { ...(baseJob.protocolJobRefs ?? {}), ...(incomingJob.protocolJobRefs ?? {}) }

  if (incomingJob.jobId) {
    for (const protocol of protocolsInIncoming) {
      const incomingProtocolJobId = incomingJob.protocolJobRefs?.[protocol]?.jobId || incomingJob.protocolJobIds?.[protocol] || incomingJob.jobId
      protocolJobIds[protocol] = incomingProtocolJobId
      protocolJobRefs[protocol] = incomingJob.protocolJobRefs?.[protocol] ?? {
        jobId: incomingProtocolJobId,
        channelKind: incomingJob.channelKind as CapabilityChannelKind,
        channelId: incomingJob.channelId,
      }
    }
  }

  return {
    ...baseJob,
    ...incomingJob,
    protocolJobIds,
    protocolJobRefs,
    status: aggregate.status,
    lifecycle: aggregate.lifecycle,
    outcome: aggregate.outcome,
    activeOperations: aggregate.activeOperations,
    tests,
    compatibleProtocols: tests.filter(isSuccessfulCapabilityTest).map(test => test.protocol),
    progress: buildCapabilityProgress(tests),
    targetProtocols: [...BASE_PROTOCOL_ORDER],
    updatedAt: incomingJob.updatedAt || baseJob.updatedAt || new Date().toISOString(),
  }
}

function getCapabilitySnapshotJobId(snapshot: CapabilitySnapshot): string {
  const activeProtocol = snapshot.tests.find(test => test.lifecycle === 'active' || test.lifecycle === 'pending')?.protocol
  if (activeProtocol) {
    return snapshot.protocolJobRefs?.[activeProtocol]?.jobId || snapshot.protocolJobIds?.[activeProtocol] || ''
  }
  return Object.values(snapshot.protocolJobIds ?? {})[0] ?? ''
}

function buildCapabilityJobFromSnapshot(
  snapshotValue: CapabilitySnapshot,
  channelType: string,
  channelId: number,
  channelName: string,
): CapabilityTestJob {
  const baseJob = buildCapabilityIdleJob(channelType, channelId, channelName)
  const snapshotJobId = getCapabilitySnapshotJobId(snapshotValue)
  const snapshotJob: CapabilityTestJob = {
    ...baseJob,
    jobId: snapshotJobId,
    protocolJobIds: snapshotValue.protocolJobIds,
    protocolJobRefs: snapshotValue.protocolJobRefs,
    sourceType: snapshotValue.sourceType,
    tests: snapshotValue.tests,
    compatibleProtocols: snapshotValue.compatibleProtocols,
    totalDuration: snapshotValue.totalDuration,
    progress: snapshotValue.progress,
    lifecycle: snapshotValue.lifecycle,
    outcome: snapshotValue.outcome,
    status: snapshotValue.lifecycle === 'active' ? 'running' : snapshotValue.lifecycle === 'cancelled' ? 'cancelled' : snapshotValue.lifecycle === 'done' ? 'completed' : 'queued',
    updatedAt: snapshotValue.updatedAt,
    snapshotUpdatedAt: snapshotValue.updatedAt,
  }
  return {
    ...mergeCapabilityJob(baseJob, snapshotJob),
    snapshotUpdatedAt: snapshotValue.updatedAt,
  }
}

function isCapabilityJobTerminal(job: CapabilityTestJob | null | undefined): boolean {
  return !!job && (job.lifecycle === 'done' || job.lifecycle === 'cancelled')
}

function collectActiveJobIds(job: CapabilityTestJob | null): string[] {
  if (!job) return []
  const seen = new Set<string>()
  for (const test of job.tests) {
    if (test.lifecycle === 'active' || test.lifecycle === 'pending') {
      const jobId = job.protocolJobRefs?.[test.protocol]?.jobId || job.protocolJobIds?.[test.protocol]
      if (jobId) seen.add(jobId)
    }
  }
  if (seen.size === 0 && job.jobId && job.tests.some(test => test.lifecycle === 'active' || test.lifecycle === 'pending')) {
    seen.add(job.jobId)
  }
  return Array.from(seen)
}

export function useCapabilityTests() {
  const api = useAdminApi()
  const { refreshChannels } = useConsoleChannels()

  function clearError() {
    error.value = ''
  }

  // ── 基础 CRUD ──

  function prepareChannelSession(channelType: string, channelId: number, channelName: string) {
    stopAllPolling()
    snapshot.value = null
    activeJob.value = buildCapabilityIdleJob(channelType, channelId, channelName)
    loading.value = false
    cancelling.value = false
    clearError()
  }

  async function startTest(
    channelType: string,
    channelId: number,
    options?: { targetProtocols?: string[]; models?: string[]; rpm?: number; previousJobId?: string; sourceTab?: string },
  ) {
    loading.value = true
    clearError()
    try {
      const resp = await api.post<CapabilityTestJobStartResponse>(
        `/api/${channelType}/channels/${channelId}/capability-test`,
        options,
      )
      if (resp.job) {
        const baseJob = activeJob.value && activeJob.value.channelId === channelId
          ? activeJob.value
          : buildCapabilityIdleJob(channelType, channelId, resp.job.channelName || '')
        activeJob.value = mergeCapabilityJob(baseJob, resp.job)
      }
      if (resp.jobId) {
        startPolling(channelType, channelId, resp.jobId)
      }
      if (activeJob.value && !isCapabilityJobTerminal(activeJob.value)) {
        for (const activeJobId of collectActiveJobIds(activeJob.value)) {
          startPolling(channelType, channelId, activeJobId)
        }
      }
      return resp
    } catch (e) {
      error.value = e instanceof Error ? e.message : String(e)
      throw e
    } finally {
      loading.value = false
    }
  }

  /** 按协议启动单独测试（WebUI 的 testProtocol） */
  async function startProtocolTest(
    channelType: string,
    channelId: number,
    protocol: string,
    models?: string[],
    rpm?: number,
  ) {
    const currentJob = activeJob.value ?? buildCapabilityIdleJob(channelType, channelId, '')
    activeJob.value = mergeCapabilityJob(currentJob, {
      ...currentJob,
      jobId: '',
      status: 'queued',
      lifecycle: 'pending',
      outcome: 'unknown',
      tests: [buildCapabilityProtocolResult(protocol, 'queued', models)],
      targetProtocols: [protocol],
      updatedAt: new Date().toISOString(),
    })
    return startTest(channelType, channelId, {
      targetProtocols: [protocol],
      models,
      rpm,
      sourceTab: channelType,
      previousJobId: getPreviousJobId(protocol),
    })
  }

  async function fetchSnapshot(channelType: string, channelId: number, sourceTab?: string, channelName = '') {
    try {
      const url = sourceTab
        ? `/api/${channelType}/channels/${channelId}/capability-snapshot?sourceTab=${sourceTab}`
        : `/api/${channelType}/channels/${channelId}/capability-snapshot`
      snapshot.value = await api.get<CapabilitySnapshot>(url)
      const snapshotJob = buildCapabilityJobFromSnapshot(snapshot.value, channelType, channelId, channelName || activeJob.value?.channelName || '')
      activeJob.value = snapshotJob
      if (!isCapabilityJobTerminal(snapshotJob)) {
        for (const jobId of collectActiveJobIds(snapshotJob)) {
          startPolling(channelType, channelId, jobId)
        }
      }
    } catch (e) {
      if (e instanceof AdminApiError && e.status === 404) return
      error.value = e instanceof Error ? e.message : String(e)
    }
  }

  async function fetchJobStatus(channelType: string, channelId: number, jobId: string) {
    const job = await api.get<CapabilityTestJob>(
      `/api/${channelType}/channels/${channelId}/capability-test/${jobId}`,
    )
    const baseJob = activeJob.value && activeJob.value.channelId === channelId && activeJob.value.channelKind === job.channelKind
      ? activeJob.value
      : buildCapabilityIdleJob(channelType, channelId, job.channelName || '')
    activeJob.value = mergeCapabilityJob(baseJob, job)
    if (activeJob.value && !isCapabilityJobTerminal(activeJob.value)) {
      for (const activeJobId of collectActiveJobIds(activeJob.value)) {
        startPolling(channelType, channelId, activeJobId)
      }
    }
    if (job.status === 'completed' || job.status === 'failed' || job.status === 'cancelled') {
      stopPoller(jobId)
    }
    return job
  }

  // ── 轮询（支持多 job 并发） ──

  function startPolling(channelType: string, channelId: number, jobId: string) {
    if (pollers.has(jobId)) return
    polling.value = true
    let inFlight = false
    const pollOnce = async () => {
      if (inFlight || !pollers.has(jobId)) return
      inFlight = true
      try {
        await fetchJobStatus(channelType, channelId, jobId)
      } catch (e) {
        stopPoller(jobId)
        error.value = e instanceof Error ? e.message : String(e)
      } finally {
        inFlight = false
      }
    }
    const timer = setInterval(pollOnce, POLL_INTERVAL)
    pollers.set(jobId, timer)
    void pollOnce()
  }

  function stopPoller(jobId: string) {
    const timer = pollers.get(jobId)
    if (timer) {
      clearInterval(timer)
      pollers.delete(jobId)
    }
    if (pollers.size === 0) polling.value = false
  }

  function stopAllPolling() {
    for (const timer of pollers.values()) clearInterval(timer)
    pollers.clear()
    polling.value = false
  }

  function closeDialog() {
    stopAllPolling()
    loading.value = false
    cancelling.value = false
    clearError()
  }

  function getPreviousJobId(protocol: string): string | undefined {
    return activeJob.value?.protocolJobRefs?.[protocol]?.jobId ||
      activeJob.value?.protocolJobIds?.[protocol] ||
      undefined
  }

  // ── 取消 ──

  async function cancelTest(channelType: string, channelId: number, jobId: string) {
    cancelling.value = true
    try {
      await api.del(`/api/${channelType}/channels/${channelId}/capability-test/${jobId}`)
      stopAllPolling()
      // 重取快照
      await fetchSnapshot(channelType, channelId, channelType)
      // 检查是否有其他活跃 job 需要继续轮询
      if (activeJob.value) {
        for (const activeJobId of collectActiveJobIds(activeJob.value)) {
          if (activeJobId !== jobId) {
            startPolling(channelType, channelId, activeJobId)
          }
        }
      }
    } finally {
      cancelling.value = false
    }
  }

  // ── Retry ──

  async function retryModel(channelType: string, channelId: number, jobId: string) {
    await api.post(`/api/${channelType}/channels/${channelId}/capability-test/${jobId}/retry`)
    startPolling(channelType, channelId, jobId)
  }

  /** 指定协议+模型 retry（WebUI 的 retryCapabilityModel） */
  async function retryModelForProtocol(
    channelType: string,
    channelId: number,
    protocol: string,
    model: string,
  ) {
    const modelGroup = getRetryModelGroup(protocol, model)
    const jobId = activeJob.value?.protocolJobRefs?.[protocol]?.jobId ||
      activeJob.value?.protocolJobIds?.[protocol]
    if (!jobId) {
      // 没有 jobId 则启动一个只测该模型的协议测试
      return startProtocolTest(channelType, channelId, protocol, modelGroup)
    }
    if (activeJob.value) {
      activeJob.value = markCapabilityModelRetrying(activeJob.value, protocol, model)
    }
    try {
      await api.post(
        `/api/${channelType}/channels/${channelId}/capability-test/${jobId}/retry`,
        { protocol, model },
      )
    } catch (e) {
      if (e instanceof AdminApiError && e.status === 404) {
        return startProtocolTest(channelType, channelId, protocol, modelGroup)
      }
      throw e
    }
    try {
      await fetchJobStatus(channelType, channelId, jobId)
    } catch (e) {
      console.error('Failed to refresh capability test job after retry:', e)
    }
    startPolling(channelType, channelId, jobId)
    // 也为子 job 启动轮询
    if (activeJob.value?.protocolJobRefs?.[protocol]?.jobId) {
      const subJobId = activeJob.value.protocolJobRefs[protocol].jobId
      if (subJobId !== jobId) startPolling(channelType, channelId, subJobId)
    }
  }

  function getRetryModelGroup(protocol: string, model: string): string[] {
    const test = activeJob.value?.tests.find(test => test.protocol === protocol)
    if (!test) return [model]
    return getCapabilityRedirectGroupModels(test, model)
  }

  // ── Copy to Tab ──

  /** 复制渠道配置到目标协议 tab（WebUI 的 copyToTab） */
  async function copyToTab(
    sourceChannelType: string,
    channelId: number,
    targetProtocol: string,
    serviceProtocol = targetProtocol,
  ): Promise<CopyToTabResult> {
    if (!isManagedChannelType(sourceChannelType)) {
      return { ok: false, message: `不支持的源协议: ${sourceChannelType}` }
    }
    if (!isManagedChannelType(targetProtocol)) {
      return { ok: false, message: `不支持的目标协议: ${targetProtocol}` }
    }
    const targetServiceType = getNativeServiceType(serviceProtocol)
    if (!targetServiceType) {
      return { ok: false, message: `不支持的上游协议: ${serviceProtocol}` }
    }

    // 先获取当前渠道完整数据
    try {
      const typeApi = getChannelTypeApi(sourceChannelType)
      const channelsResponse = await typeApi.getChannels()
      const sourceChannel = channelsResponse.channels.find(ch => ch.index === channelId)
      if (!sourceChannel) {
        return { ok: false, message: '找不到源渠道数据' }
      }

      // 构建 payload，去掉 index/status/latency 等 runtime 字段，保留连接与兼容配置。
      const payload: Partial<Channel> = {}
      const skipKeys = new Set([
        'index',
        'latency',
        'latencyTestTime',
        'status',
        'metrics',
        'suspendReason',
        'promotionUntil',
        'disabledApiKeys',
        'historicalApiKeys',
      ])
      const payloadRecord = payload as Record<string, unknown>
      for (const [k, v] of Object.entries(sourceChannel)) {
        if (!skipKeys.has(k) && v !== undefined && v !== null) {
          payloadRecord[k] = v
        }
      }
      // targetProtocol 决定复制到哪个 Tab，serviceProtocol 决定该副本实际使用哪种上游协议。
      payload.serviceType = targetServiceType

      const targetApi = getChannelTypeApi(targetProtocol)
      await targetApi.addChannel(payload as any)
      await refreshChannels(targetProtocol)
      await refreshChannels(sourceChannelType)
      return { ok: true }
    } catch (e) {
      return { ok: false, message: e instanceof Error ? e.message : String(e) }
    }
  }

  // ── Reset ──

  function reset() {
    stopAllPolling()
    activeJob.value = null
    snapshot.value = null
    error.value = ''
    loading.value = false
    cancelling.value = false
  }

  // ── Computed helpers ──

  /** 从 snapshot 或 activeJob 中获取协议结果 */
  const protocolResults = computed<CapabilityProtocolJobResult[]>(() => {
    if (activeJob.value?.tests?.length) return activeJob.value.tests
    if (snapshot.value?.tests?.length) return snapshot.value.tests
    return []
  })

  /** 从 job/snapshot 中获取兼容协议 */
  const compatibleProtocols = computed<string[]>(() => {
    return activeJob.value?.compatibleProtocols ?? snapshot.value?.compatibleProtocols ?? []
  })

  /** 聚合 job/snapshot 的 lifecycle */
  const lifecycle = computed<CapabilityLifecycle>(() => {
    return activeJob.value?.lifecycle ?? snapshot.value?.lifecycle ?? 'pending'
  })

  /** 聚合 job/snapshot 的 outcome */
  const outcome = computed<CapabilityOutcome>(() => {
    return activeJob.value?.outcome ?? snapshot.value?.outcome ?? 'unknown'
  })

  /** 是否处于活动状态 */
  const isActive = computed(() => {
    const s = activeJob.value?.status
    return s === 'running' || s === 'queued'
  })

  /** 对话框整体状态 */
  const state = computed<'initializing' | 'error' | 'idle' | 'pending' | 'running' | 'completed' | 'cancelled'>(() => {
    if (error.value) return 'error'
    if (loading.value && !activeJob.value && !snapshot.value) return 'initializing'
    if (activeJob.value?.status === 'idle') return 'idle'
    const l = lifecycle.value
    if (l === 'pending') return 'pending'
    if (l === 'active') return 'running'
    if (l === 'cancelled') return 'cancelled'
    if (l === 'done') {
      const o = outcome.value
      if (o === 'success') return 'completed'
      if (o === 'partial') return 'completed'
      if (o === 'failed') return 'completed'
      return 'completed'
    }
    return 'idle'
  })

  return {
    activeJob,
    snapshot,
    loading,
    polling,
    cancelling,
    error,
    clearError,
    prepareChannelSession,
    startTest,
    startProtocolTest,
    fetchSnapshot,
    fetchJobStatus,
    cancelTest,
    retryModel,
    retryModelForProtocol,
    copyToTab,
    closeDialog,
    reset,
    // computed helpers
    protocolResults,
    compatibleProtocols,
    lifecycle,
    outcome,
    isActive,
    state,
  }
}

/** 协议对应的原生 service type */
function getNativeServiceType(protocol: string): Channel['serviceType'] | null {
  if (protocol === 'messages') return 'claude'
  if (protocol === 'chat') return 'openai'
  if (protocol === 'responses') return 'responses'
  if (protocol === 'gemini') return 'gemini'
  if (protocol === 'images') return 'openai'
  if (protocol === 'vectors') return 'openai'
  return null
}
