// Capability test 状态管理逻辑，从 App.vue 抽出。
// 原始代码原样搬运，仅加函数包装。
import { ref, watch } from "vue"
import { api, type CapabilityTestJob, type CapabilityProtocolJobResult, type CapabilityModelJobResult, type CapabilitySnapshot, type Channel, type CapabilityTestJobStartResponse, ApiError } from "../services/api"
import { useChannelStore } from '../stores/channel'
import { useDialogStore } from '../stores/dialog'
import capabilityProbeSchema from '../../../shared/capability-probe-schema.json'

const capabilityBaseProtocolOrder = capabilityProbeSchema.baseProtocols as readonly ('messages' | 'chat' | 'responses' | 'gemini')[]
type CapabilityBaseProtocol = typeof capabilityBaseProtocolOrder[number]
type CapabilityChannelKind = CapabilityBaseProtocol
type CapabilityCopyTargetProtocol = CapabilityBaseProtocol | 'images'

export function useCapabilityTestManager(
  channelStore: ReturnType<typeof useChannelStore>,
  dialogStore: ReturnType<typeof useDialogStore>,
  showToast: (message: string, type: "success" | "error" | "warning" | "info") => void,
  t: (key: string, params?: Record<string, string | number>) => string,
  refreshChannels: () => Promise<void>,
) {

const showCapabilityTestDialog = ref(false)
const capabilityTestChannelName = ref('')
const capabilityTestChannelId = ref<number | null>(null)
const capabilityTestChannelType = ref<CapabilityChannelKind>('messages')
const capabilityTestSourceTab = ref<CapabilityChannelKind>('messages')
const capabilityTestDialogRef = ref<{ setError: (message: string) => void } | null>(null)
const capabilityTestJobId = ref('')
const capabilityPollers = ref<Record<string, ReturnType<typeof setInterval>>>({})
const capabilityTestJob = ref<CapabilityTestJob | null>(null)
const capabilityTestRpm = ref(30)
const capabilityTestPreviousJobId = ref('') // 记录上一次的 jobId，用于复用成功结果
const capabilityRetryPendingUntil = ref<Record<string, number>>({})

const isCapabilityChannelKind = (tab: string): tab is CapabilityChannelKind => {
  return capabilityBaseProtocolOrder.includes(tab as CapabilityBaseProtocol)
}

// 前端占位模型：取 schema 中的后端探测模型，并追加前端特有 images 占位列表（后端 capability 不支持 images）
const capabilityPlaceholderModels: Record<string, string[]> = {
  ...capabilityProbeSchema.frontendPlaceholderModels,
  images: ['gpt-image-2', 'gpt-image-1', 'dall-e-3', 'dall-e-2']
}

// 复合协议支持：将 from->to 的 from 映射到对应的占位模型集
const getPlaceholderModelsForProtocol = (protocol: string): string[] => {
  if (protocol.includes('->')) {
    const from = protocol.split('->')[0]
    return capabilityPlaceholderModels[from] ?? []
  }
  return capabilityPlaceholderModels[protocol] ?? []
}

const capabilityNativeServiceTypeByProtocol: Record<CapabilityCopyTargetProtocol, Channel['serviceType']> = {
  messages: 'claude',
  responses: 'responses',
  chat: 'openai',
  gemini: 'gemini',
  images: 'openai'
}

const getCapabilityNativeServiceType = (protocol: string): Channel['serviceType'] | null => {
  return capabilityNativeServiceTypeByProtocol[protocol as CapabilityCopyTargetProtocol] ?? null
}

// 判断协议是否为已知协议（基础协议 或 复合协议 from->to，其中 from 是已知基础协议）
const isCapabilityProtocol = (protocol: string): boolean => {
  if (capabilityBaseProtocolOrder.includes(protocol as CapabilityBaseProtocol)) return true
  if (protocol.includes('->')) {
    const from = protocol.split('->')[0]
    return capabilityBaseProtocolOrder.includes(from as CapabilityBaseProtocol)
  }
  return false
}

const buildCapabilityModels = (
  protocol: string,
  status: CapabilityModelJobResult['status'],
  models?: string[]
): CapabilityModelJobResult[] => {
  const now = new Date().toISOString()
  const targetModels = models?.length ? models : getPlaceholderModelsForProtocol(protocol)
  return targetModels.map(model => ({
    model,
    status,
    lifecycle: status === 'running' ? 'active' : 'pending',
    outcome: 'unknown',
    success: false,
    latency: 0,
    streamingSupported: false,
    testedAt: now
  }))
}

const buildCapabilityProtocolResult = (
  protocol: string,
  status: CapabilityProtocolJobResult['status'],
  models?: string[]
): CapabilityProtocolJobResult => {
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
    testedAt: now
  }
}

const toRetryingCapabilityModel = (modelResult: CapabilityModelJobResult): CapabilityModelJobResult => ({
  ...modelResult,
  status: 'running',
  lifecycle: 'active',
  outcome: 'unknown',
  success: false,
  error: undefined,
  reason: undefined,
})

const markCapabilityModelRetrying = (job: CapabilityTestJob, protocol: string, model: string): CapabilityTestJob => ({
  ...job,
  tests: job.tests.map(test => {
    if (test.protocol !== protocol) return test
    return {
      ...test,
      modelResults: (test.modelResults ?? []).map(modelResult => {
        if (modelResult.model !== model) return modelResult
        return toRetryingCapabilityModel(modelResult)
      })
    }
  })
})

const applyCapabilityRetryPending = (
  job: CapabilityTestJob,
  pendingMap: Record<string, number>,
  now: number
): CapabilityTestJob => ({
  ...job,
  tests: job.tests.map(test => ({
    ...test,
    modelResults: (test.modelResults ?? []).map(modelResult => {
      const key = `${test.protocol}:${modelResult.model}`
      const pendingUntil = pendingMap[key]
      if (!pendingUntil || now >= pendingUntil) {
        delete pendingMap[key]
        return modelResult
      }
      if (modelResult.lifecycle === 'pending' || modelResult.lifecycle === 'active') {
        return modelResult
      }
      return toRetryingCapabilityModel(modelResult)
    })
  }))
})

const isIdleCapabilityTest = (test: CapabilityProtocolJobResult): boolean => {
  return (test.status as string) === 'idle'
}

const isActiveCapabilityTest = (test: CapabilityProtocolJobResult): boolean => {
  return !isIdleCapabilityTest(test) && test.lifecycle !== 'cancelled' && test.lifecycle !== 'done' && (test.lifecycle === 'active' || test.status === 'running')
}

const isBusyCapabilityTest = (test: CapabilityProtocolJobResult): boolean => {
  return !isIdleCapabilityTest(test) && test.lifecycle !== 'cancelled' && test.lifecycle !== 'done' && (test.lifecycle === 'pending' || test.lifecycle === 'active' || test.status === 'queued' || test.status === 'running')
}

const isPendingCapabilityTest = (test: CapabilityProtocolJobResult): boolean => {
  return !isIdleCapabilityTest(test) && test.lifecycle !== 'cancelled' && test.lifecycle !== 'done' && (test.lifecycle === 'pending' || test.status === 'queued')
}

const isSuccessfulCapabilityTest = (test: CapabilityProtocolJobResult): boolean => {
  return test.success || test.outcome === 'success'
}

const getCapabilityAggregateState = (tests: CapabilityProtocolJobResult[]): {
  status: CapabilityTestJob['status']
  lifecycle: CapabilityTestJob['lifecycle']
  outcome: CapabilityTestJob['outcome']
  activeOperations: number
} => {
  const nonIdleTests = tests.filter(test => !isIdleCapabilityTest(test))
  const activeOperations = tests.filter(isActiveCapabilityTest).length
  if (nonIdleTests.length === 0) {
    return { status: 'idle' as const, lifecycle: 'pending' as const, outcome: 'unknown' as const, activeOperations: 0 }
  }
  if (activeOperations > 0) {
    return { status: 'running' as const, lifecycle: 'active' as const, outcome: 'unknown' as const, activeOperations }
  }
  if (tests.some(isPendingCapabilityTest)) {
    return { status: 'queued' as const, lifecycle: 'pending' as const, outcome: 'unknown' as const, activeOperations: 0 }
  }

  const cancelledCount = nonIdleTests.filter(test => test.lifecycle === 'cancelled' || test.outcome === 'cancelled').length
  if (cancelledCount === nonIdleTests.length) {
    return { status: 'cancelled' as const, lifecycle: 'cancelled' as const, outcome: 'cancelled' as const, activeOperations: 0 }
  }

  const successCount = nonIdleTests.filter(isSuccessfulCapabilityTest).length
  if (successCount === 0) {
    return { status: 'failed' as const, lifecycle: 'done' as const, outcome: 'failed' as const, activeOperations: 0 }
  }

  const outcome = successCount === tests.length ? 'success' : 'partial'
  return { status: 'completed' as const, lifecycle: 'done' as const, outcome, activeOperations: 0 }
}

const buildCapabilityProgress = (tests: CapabilityProtocolJobResult[]) => {
  const progress = {
    totalModels: 0,
    queuedModels: 0,
    runningModels: 0,
    successModels: 0,
    failedModels: 0,
    skippedModels: 0,
    completedModels: 0
  }

  for (const test of tests) {
    for (const modelResult of test.modelResults ?? []) {
      progress.totalModels += 1
      if ((modelResult.status as string) === 'idle') continue
      if (modelResult.lifecycle === 'cancelled' || modelResult.status === 'skipped') {
        progress.skippedModels += 1
        progress.completedModels += 1
        continue
      }
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
      progress.failedModels += 1
      progress.completedModels += 1
    }
  }

  return progress
}

// normalizeCapabilityTests 将测试结果归一化：
// 1. 保留所有已知协议（含复合协议），复合协议排在最前
// 2. 补齐缺失的基础协议（以 idle 状态占位）
const mergeCapabilityProtocolResult = (baseTest: CapabilityProtocolJobResult, incomingTest: CapabilityProtocolJobResult): CapabilityProtocolJobResult => {
  const modelResultsByModel = new Map<string, CapabilityModelJobResult>()
  for (const modelResult of baseTest.modelResults ?? []) {
    modelResultsByModel.set(modelResult.model, modelResult)
  }
  for (const modelResult of incomingTest.modelResults ?? []) {
    modelResultsByModel.set(modelResult.model, modelResult)
  }
  const modelResults = Array.from(modelResultsByModel.values())

  const attemptedModels = modelResults.filter(modelResult => (modelResult.status as string) !== 'idle').length

  return {
    ...baseTest,
    ...incomingTest,
    modelResults,
    attemptedModels,
    successCount: modelResults.filter(modelResult => modelResult.status === 'success' || modelResult.outcome === 'success').length
  }
}

const normalizeCapabilityTests = (tests: CapabilityProtocolJobResult[]): CapabilityProtocolJobResult[] => {
  const testsByProtocol = new Map<string, CapabilityProtocolJobResult>()

  for (const test of tests) {
    if (!isCapabilityProtocol(test.protocol)) continue
    const existingTest = testsByProtocol.get(test.protocol)
    testsByProtocol.set(test.protocol, existingTest ? mergeCapabilityProtocolResult(existingTest, test) : test)
  }

  const compositeTests = Array.from(testsByProtocol.values()).filter(test => test.protocol.includes('->'))
  const baseTests = capabilityBaseProtocolOrder.map(protocol =>
    testsByProtocol.get(protocol) ?? buildCapabilityProtocolResult(protocol, 'idle')
  )

  // 复合协议排在基础协议前面
  return [...compositeTests, ...baseTests]
}

const buildCapabilityIdleJob = (channelId: number, channelName: string, channelKind: CapabilityChannelKind): CapabilityTestJob => {
  const now = new Date().toISOString()
  const tests = capabilityBaseProtocolOrder.map(protocol => buildCapabilityProtocolResult(protocol, 'idle'))
  const progress = buildCapabilityProgress(tests)

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
    targetProtocols: [...capabilityBaseProtocolOrder],
    progress
  }
}

const mergeCapabilityJob = (baseJob: CapabilityTestJob, incomingJob: CapabilityTestJob): CapabilityTestJob => {
  const tests = normalizeCapabilityTests([
    ...baseJob.tests,
    ...incomingJob.tests
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
        channelId: incomingJob.channelId
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
    targetProtocols: [...capabilityBaseProtocolOrder],
    updatedAt: incomingJob.updatedAt || baseJob.updatedAt || new Date().toISOString()
  }
}

const getCapabilitySnapshotJobId = (snapshot: CapabilitySnapshot): string => {
  const activeProtocol = snapshot.tests.find(test => test.lifecycle === 'active' || test.lifecycle === 'pending')?.protocol
  if (activeProtocol) {
    return snapshot.protocolJobRefs?.[activeProtocol]?.jobId || snapshot.protocolJobIds?.[activeProtocol] || ''
  }
  return Object.values(snapshot.protocolJobIds ?? {})[0] ?? ''
}

const buildCapabilityJobFromSnapshot = (
  snapshot: CapabilitySnapshot,
  channelId: number,
  channelName: string,
  channelKind: CapabilityChannelKind
): CapabilityTestJob => {
  const baseJob = buildCapabilityIdleJob(channelId, channelName, channelKind)
  const snapshotJobId = getCapabilitySnapshotJobId(snapshot)
  const snapshotJob: CapabilityTestJob = {
    ...baseJob,
    jobId: snapshotJobId,
    protocolJobIds: snapshot.protocolJobIds,
    protocolJobRefs: snapshot.protocolJobRefs,
    sourceType: snapshot.sourceType,
    tests: snapshot.tests,
    compatibleProtocols: snapshot.compatibleProtocols,
    totalDuration: snapshot.totalDuration,
    progress: snapshot.progress,
    lifecycle: snapshot.lifecycle,
    outcome: snapshot.outcome,
    status: snapshot.lifecycle === 'active' ? 'running' : snapshot.lifecycle === 'cancelled' ? 'cancelled' : snapshot.lifecycle === 'done' ? 'completed' : 'queued',
    updatedAt: snapshot.updatedAt,
    snapshotUpdatedAt: snapshot.updatedAt
  }
  return {
    ...mergeCapabilityJob(baseJob, snapshotJob),
    snapshotUpdatedAt: snapshot.updatedAt
  }
}

watch(showCapabilityTestDialog, (open) => {
  if (!open) {
    stopAllCapabilityPolling()
    capabilityRetryPendingUntil.value = {}
  }
})

const collectActiveJobIds = (job: CapabilityTestJob | null): string[] => {
  if (!job) return []
  const seen = new Set<string>()
  for (const test of job.tests) {
    if (test.lifecycle === 'active' || test.lifecycle === 'pending') {
      const jId = job.protocolJobRefs?.[test.protocol]?.jobId || job.protocolJobIds?.[test.protocol]
      if (jId && !seen.has(jId)) seen.add(jId)
    }
  }
  return Array.from(seen)
}

const isCapabilityJobTerminal = (job: CapabilityTestJob | null | undefined) => {
  if (!job) return false
  return job.lifecycle === 'done' || job.lifecycle === 'cancelled'
}
const stopCapabilityPolling = (jobId: string) => {
  if (!jobId || !capabilityPollers.value[jobId]) return
  clearInterval(capabilityPollers.value[jobId])
  delete capabilityPollers.value[jobId]
}

const stopAllCapabilityPolling = () => {
  for (const jobId of Object.keys(capabilityPollers.value)) {
    clearInterval(capabilityPollers.value[jobId])
  }
  capabilityPollers.value = {}
}

const startCapabilityPolling = (channelType: CapabilityChannelKind, channelId: number, jobId: string) => {
  if (!jobId || capabilityPollers.value[jobId]) return
  capabilityPollers.value[jobId] = setInterval(async () => {
    if (!jobId) return
    try {
      const latest = await api.getChannelCapabilityTestStatus(channelType, channelId, jobId)
      updateCapabilityJob(latest)
    } catch (error) {
      console.error('Failed to poll capability test job:', error)
    }
  }, 1000)
}

const updateCapabilityJob = (job: CapabilityTestJob) => {
  const incomingJob = applyCapabilityRetryPending(job, capabilityRetryPendingUntil.value, Date.now())
  const currentJob = capabilityTestJob.value
  const channelKind = isCapabilityChannelKind(job.channelKind)
    ? job.channelKind
    : isCapabilityChannelKind(channelStore.activeTab)
      ? channelStore.activeTab
      : 'messages'
  const baseJob = currentJob && currentJob.channelId === job.channelId && currentJob.channelKind === job.channelKind
    ? currentJob
    : buildCapabilityIdleJob(job.channelId, job.channelName, channelKind)
  const mergedJob = mergeCapabilityJob(baseJob, incomingJob)

  capabilityTestJob.value = mergedJob
  capabilityTestJobId.value = job.jobId
  if (isCapabilityJobTerminal(job)) {
    stopCapabilityPolling(job.jobId)
  }
}

const getCapabilityPreviousJobId = (protocol: string): string | undefined => {
  const currentJob = capabilityTestJob.value
  return currentJob?.protocolJobRefs?.[protocol]?.jobId ||
    currentJob?.protocolJobIds?.[protocol] ||
    capabilityTestPreviousJobId.value ||
    undefined
}

const testChannelCapability = async (target: number | Channel) => {
  const channelType = typeof target === 'number'
    ? channelStore.activeTab
    : (target.routeKind ?? channelStore.activeTab)
  const channelId = typeof target === 'number'
    ? target
    : (target.routeIndex ?? target.index)

  if (!isCapabilityChannelKind(channelType)) {
    showToast(t('toast.unsupportedProtocol', { protocol: channelType }), 'warning')
    return
  }

  const channel = typeof target === 'number'
    ? channelStore.currentChannelsData.channels?.find((ch: Channel) => ch.index === channelId)
    : target
  if (!channel) {
    console.error('Channel not found:', channelId)
    return
  }

  // 从渠道的实际 serviceType 推导 channelKind，而不是从 activeTab
  const sourceTab = channelType  // 当前查看的 Tab 协议类型
  capabilityTestChannelName.value = channel.name || t('capability.channelFallback', { id: channelId })
  capabilityTestChannelId.value = channelId
  capabilityTestChannelType.value = channelType
  capabilityTestSourceTab.value = sourceTab

  if (dialogStore.showAddChannelModal) {
    dialogStore.closeAddChannelModal()
  }
  if (dialogStore.showEditChannelModal) {
    dialogStore.closeEditChannelModal()
  }

  showCapabilityTestDialog.value = true
  stopAllCapabilityPolling()
  capabilityTestPreviousJobId.value = capabilityTestJobId.value
  capabilityTestJobId.value = ''
  capabilityTestJob.value = buildCapabilityIdleJob(channelId, capabilityTestChannelName.value, channelType)

  try {
    // sourceTab 是渠道的实际协议类型，channelType 是 API 路径
    const snapshot = await api.getChannelCapabilitySnapshot(channelType, channelId, sourceTab)
    if (capabilityTestChannelId.value !== channelId || capabilityTestChannelType.value !== channelType) return
    const snapshotJob = buildCapabilityJobFromSnapshot(snapshot, channelId, capabilityTestChannelName.value, channelType)
    capabilityTestJob.value = snapshotJob
    capabilityTestJobId.value = snapshotJob.jobId
    if (!isCapabilityJobTerminal(snapshotJob)) {
      const activeIds = collectActiveJobIds(snapshotJob)
      for (const jId of activeIds) {
        startCapabilityPolling(channelType, channelId, jId)
      }
    }
  } catch (error) {
    if (error instanceof ApiError && error.status === 404) return
    if (error instanceof ApiError && error.status === 401) {
      // 401 已由 ApiService 清除认证，关闭能力测试对话框
      showCapabilityTestDialog.value = false
      return
    }
    const message = error instanceof Error ? error.message : t('system.unknown')
    capabilityTestDialogRef.value?.setError(t('toast.capabilityFailed', { message }))
  }
}

const handleTestCapabilityProtocol = async (protocol: string, models?: string[]) => {
  if (!isCapabilityChannelKind(channelStore.activeTab) || !isCapabilityProtocol(protocol)) {
    return
  }
  if (capabilityTestChannelId.value === null) return

  const channelType = capabilityTestChannelType.value
  const channelId = capabilityTestChannelId.value
  const previousJobId = getCapabilityPreviousJobId(protocol)
  const currentJob = capabilityTestJob.value ?? buildCapabilityIdleJob(channelId, capabilityTestChannelName.value, channelType)
  capabilityTestJob.value = mergeCapabilityJob(currentJob, {
    ...currentJob,
    jobId: '',
    status: 'queued',
    lifecycle: 'pending',
    outcome: 'unknown',
    tests: [buildCapabilityProtocolResult(protocol, 'queued', models)],
    targetProtocols: [protocol],
    updatedAt: new Date().toISOString()
  })
  try {
    const startResp: CapabilityTestJobStartResponse = await api.startChannelCapabilityTest(
      channelType,
      channelId,
      {
        targetProtocols: [protocol],
        previousJobId,
        rpm: capabilityTestRpm.value,
        sourceTab: capabilityTestSourceTab.value,
        models
      }
    )
    capabilityTestJobId.value = startResp.jobId

    if (startResp.job) {
      updateCapabilityJob(startResp.job)
    }

    if (isCapabilityJobTerminal(startResp.job) && !(startResp.job?.activeOperations && startResp.job.activeOperations > 0)) {
      return
    }

    startCapabilityPolling(channelType, channelId, startResp.jobId)
  } catch (error) {
    const message = error instanceof Error ? error.message : t('system.unknown')
    capabilityTestDialogRef.value?.setError(t('toast.capabilityFailed', { message }))
  }
}

const handleTestCapabilityProtocolWithModels = handleTestCapabilityProtocol

const handleCancelCapabilityTest = async () => {
  if (!capabilityTestJob.value) return
  if (!capabilityTestChannelType.value) return
  if (capabilityTestChannelId.value === null) return
  try {
    const activeIds = collectActiveJobIds(capabilityTestJob.value)
    const channelType = capabilityTestChannelType.value
    const channelId = capabilityTestChannelId.value
    for (const jId of activeIds) {
      await api.cancelCapabilityTest(channelType, channelId, jId).catch(err =>
        console.error('Failed to cancel capability test job:', jId, err)
      )
    }
    stopAllCapabilityPolling()
    const snapshot = await api.getChannelCapabilitySnapshot(channelType, channelId, channelStore.activeTab)
    const snapshotJob = buildCapabilityJobFromSnapshot(snapshot, channelId, capabilityTestChannelName.value, channelType)
    capabilityTestJob.value = snapshotJob
    capabilityTestJobId.value = snapshotJob.jobId
    if (!isCapabilityJobTerminal(snapshotJob)) {
      const refreshedActiveIds = collectActiveJobIds(snapshotJob)
      for (const jId of refreshedActiveIds) {
        startCapabilityPolling(channelType, channelId, jId)
      }
    }
  } catch (error) {
    console.error('Failed to cancel capability test:', error)
  }
}

const handleRetryCapabilityModel = async (protocol: string, model: string) => {
  if (!capabilityTestJob.value) return
  if (!capabilityTestChannelType.value) return
  if (capabilityTestChannelId.value === null) return
  const channelId = capabilityTestChannelId.value
  const job = capabilityTestJob.value
  const protocolTest = job.tests.find(t => t.protocol === protocol)
  if (!protocolTest) return
  if (isBusyCapabilityTest(protocolTest)) return
  const retryJobId = job.protocolJobRefs?.[protocol]?.jobId || job.protocolJobIds?.[protocol]
  if (!retryJobId) {
    // 没有 jobId（虚拟协议未测试过），启动单模型测试
    handleTestCapabilityProtocolWithModels(protocol, [model])
    return
  }
  try {
    const pendingKey = `${protocol}:${model}`
    capabilityRetryPendingUntil.value[pendingKey] = Date.now() + 1000

    capabilityTestJob.value = markCapabilityModelRetrying(capabilityTestJob.value, protocol, model)

    await api.retryCapabilityTestModel(capabilityTestChannelType.value, channelId, retryJobId, protocol, model)
    startCapabilityPolling(capabilityTestChannelType.value, channelId, retryJobId)
  } catch (error) {
    if (error instanceof ApiError && error.status === 404) {
      await handleTestCapabilityProtocolWithModels(protocol, [model])
      return
    }
    console.error('Failed to retry capability test model:', error)
  }
}

// 复制渠道到目标协议 Tab
const handleCopyToTab = async (targetProtocol: string, serviceProtocol = targetProtocol) => {
  const sourceChannel = channelStore.currentChannelsData.channels?.find((ch: Channel) => ch.index === capabilityTestChannelId.value)
  if (!sourceChannel) {
    showToast(t('toast.sourceChannelMissing'), 'error')
    return
  }

  const targetServiceType = getCapabilityNativeServiceType(serviceProtocol)
  if (!targetServiceType) {
    showToast(t('toast.unsupportedProtocol', { protocol: serviceProtocol }), 'error')
    return
  }

  // 构造渠道配置（仅复制核心连接信息）
  const channelConfig: Omit<Channel, 'index' | 'latency' | 'status'> = {
    name: sourceChannel.name,
    serviceType: targetServiceType,
    baseUrl: sourceChannel.baseUrl,
    baseUrls: sourceChannel.baseUrls,
    apiKeys: [...sourceChannel.apiKeys],
    description: sourceChannel.description,
    website: sourceChannel.website,
    proxyUrl: sourceChannel.proxyUrl,
    requestTimeoutMs: sourceChannel.requestTimeoutMs,
    streamFirstContentTimeoutMs: sourceChannel.streamFirstContentTimeoutMs,
    streamInactivityTimeoutMs: sourceChannel.streamInactivityTimeoutMs,
    insecureSkipVerify: sourceChannel.insecureSkipVerify,
    modelMapping: sourceChannel.modelMapping,
    reasoningMapping: sourceChannel.reasoningMapping,
    reasoningParamStyle: sourceChannel.reasoningParamStyle,
    textVerbosity: sourceChannel.textVerbosity,
    fastMode: sourceChannel.fastMode,
    customHeaders: sourceChannel.customHeaders,
    pinned: sourceChannel.pinned,
    priority: sourceChannel.priority,
    lowQuality: sourceChannel.lowQuality,
    injectDummyThoughtSignature: sourceChannel.injectDummyThoughtSignature,
    stripThoughtSignature: sourceChannel.stripThoughtSignature,
    supportedModels: sourceChannel.supportedModels,
    normalizeSystemRoleToTopLevel: sourceChannel.normalizeSystemRoleToTopLevel,
    rateLimitRpm: sourceChannel.rateLimitRpm,
    rateLimitBurst: sourceChannel.rateLimitBurst,
    rateLimitMaxConcurrent: sourceChannel.rateLimitMaxConcurrent,
    rateLimitAutoFromHeaders: sourceChannel.rateLimitAutoFromHeaders,
    rpm: sourceChannel.rpm ?? 30,
  }

  try {
    switch (targetProtocol) {
      case 'messages':
        await api.addChannel(channelConfig)
        break
      case 'chat':
        await api.addChatChannel(channelConfig)
        break
      case 'gemini':
        await api.addGeminiChannel(channelConfig)
        break
      case 'responses':
        await api.addResponsesChannel(channelConfig)
        break
      case 'images':
        await api.addImagesChannel(channelConfig)
        break
      default:
        showToast(t('toast.unsupportedProtocol', { protocol: targetProtocol }), 'error')
        return
    }

    showToast(t('toast.channelCopied', { protocol: targetProtocol }), 'success')
    await refreshChannels()
  } catch (error) {
    showToast(t('toast.copyFailed', { message: error instanceof Error ? error.message : t('system.unknown') }), 'error')
  }
}

  return {
    showCapabilityTestDialog, capabilityTestChannelName, capabilityTestChannelId,
    capabilityTestChannelType, capabilityTestSourceTab, capabilityTestDialogRef,
    capabilityTestJobId, capabilityPollers, capabilityTestJob, capabilityTestRpm,
    capabilityTestPreviousJobId, capabilityRetryPendingUntil,
    isCapabilityChannelKind, capabilityPlaceholderModels, getPlaceholderModelsForProtocol,
    capabilityBaseProtocolOrder, capabilityNativeServiceTypeByProtocol,
    getCapabilityNativeServiceType, isCapabilityProtocol, buildCapabilityModels,
    buildCapabilityProtocolResult, toRetryingCapabilityModel, markCapabilityModelRetrying,
    applyCapabilityRetryPending, isIdleCapabilityTest, isActiveCapabilityTest,
    isBusyCapabilityTest, isPendingCapabilityTest, isSuccessfulCapabilityTest,
    getCapabilityAggregateState, buildCapabilityProgress, mergeCapabilityProtocolResult,
    normalizeCapabilityTests, buildCapabilityIdleJob, mergeCapabilityJob,
    getCapabilitySnapshotJobId, buildCapabilityJobFromSnapshot,
    collectActiveJobIds, isCapabilityJobTerminal, stopCapabilityPolling,
    stopAllCapabilityPolling, startCapabilityPolling, updateCapabilityJob,
    getCapabilityPreviousJobId, testChannelCapability, handleTestCapabilityProtocol,
    handleTestCapabilityProtocolWithModels, handleCancelCapabilityTest,
    handleRetryCapabilityModel, handleCopyToTab,
  }
}

export type { CapabilityChannelKind }
