import { ref, computed, type ComputedRef } from 'vue'
import type { AgentPlatform, AgentProvider, AgentConfigStatus, ApplyAgentConfigRequest, ConfigDiffResult, MigrateCodexSessionsResult } from '@/types'
import { useLanguage } from '@/composables/useLanguage'
import {
  GetAgentConfigStatus,
  ApplyAgentConfig,
  RestoreAgentConfig,
  GetSavedProviderKeys,
  PreviewAgentConfigDiff,
  PreviewRestoreConfigDiff,
  MigrateCodexSessions,
} from '@bindings/github.com/BenedictKing/ccx/desktop/desktopservice'

const agentLabels: Record<AgentPlatform, string> = {
  claude: 'Claude Code',
  codex: 'Codex',
  opencode: 'OpenCode',
}

const claudeProviderLabels: Record<AgentProvider | 'custom', string> = {
  ccx: 'CCX',
  deepseek: 'DeepSeek',
  mimo: 'MiMo',
  'tencent-lkeap': '腾讯云 TokenHub',
  'kimi-code': 'Kimi Code',
  'volc-ark': '火山方舟',
  qianfan: '百度千帆',
  originrouter: '极易云',
  kimi: 'Kimi',
  glm: 'GLM',
  sensenova: 'SenseNova',
  minimax: 'MiniMax',
  dashscope: 'DashScope',
  openrouter: 'OpenRouter',
  modelscope: 'ModelScope',
  'opencode-zen': 'OpenCode Zen',
  'opencode-go': 'OpenCode Go',
  openai: 'OpenAI',
  xfyun: '讯飞星辰',
  // custom 需要 i18n，延迟到函数内获取
  custom: 'Custom',
}

// codexProviderLabels 需要响应式 t()，延迟到 useAgentConfig() 内构建
let _codexProviderLabels: ComputedRef<Record<AgentProvider | 'custom', string>> | null = null

function getCodexProviderLabels(): ComputedRef<Record<AgentProvider | 'custom', string>> {
  if (!_codexProviderLabels) {
    const { t } = useLanguage()
    _codexProviderLabels = computed<Record<AgentProvider | 'custom', string>>(() => ({
      ccx: t('agent.localGateway'),
      openai: 'OpenAI',
      deepseek: 'DeepSeek',
      mimo: 'MiMo',
      'tencent-lkeap': '腾讯云 TokenHub',
      'kimi-code': 'Kimi Code',
      'volc-ark': '火山方舟',
      qianfan: '百度千帆',
      originrouter: '极易云',
      kimi: 'Kimi',
      glm: 'GLM',
      sensenova: 'SenseNova',
      minimax: 'MiniMax',
      dashscope: 'DashScope',
      openrouter: 'OpenRouter',
      modelscope: 'ModelScope',
      'opencode-zen': 'OpenCode Zen',
      'opencode-go': 'OpenCode Go',
      xfyun: '讯飞星辰',
      custom: t('agent.custom'),
    }))
  }
  return _codexProviderLabels!
}

// 延迟获取 t 函数，首次 useAgentConfig() 调用时初始化
let _t: ((key: string, params?: Record<string, string>) => string) | null = null

function getT() {
  if (!_t) {
    _t = useLanguage().t
  }
  return _t
}

const agentPlatforms: AgentPlatform[] = ['claude', 'codex', 'opencode']

// Module-level singletons
const agentStatuses = ref<Record<AgentPlatform, AgentConfigStatus | null>>({
  claude: null,
  codex: null,
  opencode: null,
})
const configLoading = ref(false)
const selectedClaudeProvider = ref<AgentProvider>('ccx')
const claudeProviderKeys = ref<Record<AgentProvider, string>>({
  ccx: '',
  deepseek: '',
  mimo: '',
  'tencent-lkeap': '',
  'kimi-code': '',
  'volc-ark': '',
  qianfan: '',
  originrouter: '',
  kimi: '',
  glm: '',
  sensenova: '',
  minimax: '',
  dashscope: '',
  openrouter: '',
  modelscope: '',
  'opencode-zen': '',
  'opencode-go': '',
  openai: '',
  xfyun: '',
})
const savedProviderKeys = ref<Record<string, string>>({})
const codexOpenAIKey = ref('')
// OpenAI 直连：是否勾选「我有自己的 API Key」。勾选则必须输入 key，否则走 OAuth。
const codexOpenAIUseOwnKey = ref(false)
const openCodeOpenAIKey = ref('')
const claudeMimoBaseUrl = ref('https://api.xiaomimimo.com/anthropic')
const selectedMimoPlan = ref('https://api.xiaomimimo.com/anthropic')
const selectedMimoCodexPlan = ref('https://api.xiaomimimo.com/v1')
const selectedMimoOpenCodePlan = ref('https://api.xiaomimimo.com/v1')
const selectedDashScopePlan = ref('https://dashscope.aliyuncs.com/apps/anthropic')
const selectedCodexProvider = ref<AgentProvider>('ccx')
const codexMode = ref<'quick' | 'plugin'>('quick')
const selectedOpenCodeProvider = ref<AgentProvider>('ccx')

// Diff preview dialog state
const diffDialogOpen = ref(false)
const diffResult = ref<ConfigDiffResult | null>(null)
const diffMode = ref<'apply' | 'restore'>('apply')
const diffLoading = ref(false)
const diffPendingPlatform = ref<AgentPlatform>('claude')
const diffWarning = ref<string | undefined>(undefined)
const migrateDialogOpen = ref(false)
const migrateLoading = ref(false)
const migrateResult = ref<MigrateCodexSessionsResult | null>(null)
const migrateError = ref('')

const isClaudeProvider = (value?: string): value is AgentProvider => {
  return value === 'ccx' || value === 'deepseek' || value === 'mimo' || value === 'tencent-lkeap' || value === 'kimi' || value === 'glm' || value === 'sensenova' || value === 'minimax' || value === 'dashscope' || value === 'openrouter' || value === 'modelscope' || value === 'opencode-zen' || value === 'opencode-go' || value === 'xfyun' || value === 'volc-ark' || value === 'qianfan'
}

// Codex 支持快捷模式/插件模式切换的第三方 provider
const isCodexThirdPartyWithMode = (provider?: string) => {
  return provider === 'deepseek' || provider === 'mimo' || provider === 'kimi' || provider === 'glm' || provider === 'minimax' || provider === 'dashscope' || provider === 'openrouter' || provider === 'modelscope' || provider === 'opencode-zen' || provider === 'opencode-go' || provider === 'xfyun' || provider === 'tencent-lkeap' || provider === 'volc-ark' || provider === 'qianfan'
}

const claudeProviderLabel = (value?: string) => {
  if (!value) return getT()('agent.statusDetecting')
  return claudeProviderLabels[value as AgentProvider | 'custom'] || value
}

const codexProviderLabel = (value?: string) => {
  if (!value) return getT()('agent.statusDetecting')
  return getCodexProviderLabels().value[value as AgentProvider | 'custom'] || value
}

const openCodeProviderLabel = (value?: string) => {
  if (!value) return getT()('agent.statusDetecting')
  return getCodexProviderLabels().value[value as AgentProvider | 'custom'] || value
}

const claudeTargetBaseUrl = () => {
  switch (selectedClaudeProvider.value) {
    case 'ccx':
      return agentStatuses.value.claude?.targetBaseUrl || getT()('agent.localGateway')
    case 'deepseek':
      return 'https://api.deepseek.com/anthropic'
    case 'mimo':
      return claudeMimoBaseUrl.value || 'https://api.xiaomimimo.com/anthropic'
    case 'kimi':
      return 'https://api.moonshot.cn/anthropic'
    case 'glm':
      return 'https://open.bigmodel.cn/api/anthropic'
    case 'sensenova':
      return 'https://token.sensenova.cn'
    case 'minimax':
      return 'https://api.minimaxi.com/anthropic'
    case 'dashscope':
      return selectedDashScopePlan.value
    case 'openrouter':
      return 'https://openrouter.ai/api'
    case 'modelscope':
      return 'https://api-inference.modelscope.cn'
    case 'opencode-zen':
      return 'https://opencode.ai/zen'
    case 'opencode-go':
      return 'https://opencode.ai/zen/go'
    case 'xfyun':
      return 'https://maas-coding-api.cn-huabei-1.xf-yun.com/anthropic'
    case 'tencent-lkeap':
      return 'https://api.lkeap.cloud.tencent.com/plan/anthropic'
    case 'volc-ark':
      return 'https://ark.cn-beijing.volces.com/api/coding'
    case 'qianfan':
      return 'https://qianfan.baidubce.com/anthropic/coding'
    default:
      return ''
  }
}

const codexTargetBaseUrl = () => {
  switch (selectedCodexProvider.value) {
    case 'ccx':
      return agentStatuses.value.codex?.targetBaseUrl || getT()('agent.localGateway')
    case 'openai':
      return 'https://api.openai.com/v1'
    case 'deepseek':
      return 'https://api.deepseek.com/v1'
    case 'mimo':
      return selectedMimoCodexPlan.value || 'https://api.xiaomimimo.com/v1'
    case 'kimi':
      return 'https://api.moonshot.cn/v1'
    case 'glm':
      return 'https://open.bigmodel.cn/api/paas/v4'
    case 'minimax':
      return 'https://api.minimax.chat/v1'
    case 'dashscope':
      return 'https://dashscope.aliyuncs.com/compatible-mode/v1'
    case 'openrouter':
      return 'https://openrouter.ai/api/v1'
    case 'modelscope':
      return 'https://api-inference.modelscope.cn/v1'
    case 'opencode-zen':
      return 'https://opencode.ai/zen/v1'
    case 'opencode-go':
      return 'https://opencode.ai/zen/go/v1'
    case 'xfyun':
      return 'https://maas-coding-api.cn-huabei-1.xf-yun.com/v2'
    case 'tencent-lkeap':
      return 'https://api.lkeap.cloud.tencent.com/plan/v3'
    case 'volc-ark':
      return 'https://ark.cn-beijing.volces.com/api/coding/v3'
    case 'qianfan':
      return 'https://qianfan.baidubce.com/v2/coding'
    default:
      return ''
  }
}

const openCodeTargetBaseUrl = () => {
  switch (selectedOpenCodeProvider.value) {
    case 'ccx':
      return agentStatuses.value.opencode?.targetBaseUrl || getT()('agent.localGateway')
    case 'deepseek':
      return 'https://api.deepseek.com/v1'
    case 'mimo':
      return selectedMimoOpenCodePlan.value || 'https://api.xiaomimimo.com/v1'
    case 'kimi':
      return 'https://api.moonshot.cn/v1'
    case 'glm':
      return 'https://open.bigmodel.cn/api/paas/v4'
    case 'sensenova':
      return 'https://token.sensenova.cn/v1'
    case 'minimax':
      return 'https://api.minimax.chat/v1'
    case 'dashscope':
      return 'https://dashscope.aliyuncs.com/compatible-mode/v1'
    case 'xfyun':
      return 'https://maas-coding-api.cn-huabei-1.xf-yun.com/v2'
    case 'openrouter':
      return 'https://openrouter.ai/api/v1'
    case 'modelscope':
      return 'https://api-inference.modelscope.cn/v1'
    case 'opencode-zen':
      return 'https://opencode.ai/zen/v1'
    case 'opencode-go':
      return 'https://opencode.ai/zen/go/v1'
    case 'tencent-lkeap':
      return 'https://api.lkeap.cloud.tencent.com/plan/v3'
    case 'volc-ark':
      return 'https://ark.cn-beijing.volces.com/api/coding/v3'
    case 'qianfan':
      return 'https://qianfan.baidubce.com/v2/coding'
    default:
      return ''
  }
}

const agentStatusText = (item: AgentConfigStatus | null) => {
  const t = getT()
  if (!item) return t('agent.statusDetecting')
  if (item.configured) return t('agent.statusConfigured')
  if (item.needsUpdate) return t('agent.statusPortMismatch')
  return t('agent.statusUnconfigured')
}

const agentStatusClass = (item: AgentConfigStatus | null) => {
  if (!item) return 'starting'
  if (item.configured) return 'running'
  if (item.needsUpdate) return 'starting'
  return 'stopped'
}

const resolveMiMoPlan = (url: string): string => {
  const known = [
    'https://api.xiaomimimo.com/anthropic',
    'https://token-plan-cn.xiaomimimo.com/anthropic',
    'https://token-plan-sgp.xiaomimimo.com/anthropic',
    'https://token-plan-ams.xiaomimimo.com/anthropic',
  ]
  return known.includes(url) ? url : ''
}

const resolveMiMoCodexPlan = (url: string): string => {
  const known = [
    'https://api.xiaomimimo.com/v1',
    'https://token-plan-cn.xiaomimimo.com/v1',
    'https://token-plan-sgp.xiaomimimo.com/v1',
    'https://token-plan-ams.xiaomimimo.com/v1',
  ]
  return known.includes(url) ? url : ''
}

const resolveDashScopePlan = (url: string): string => {
  const known = [
    'https://dashscope.aliyuncs.com/apps/anthropic',
    'https://coding.dashscope.aliyuncs.com/apps/anthropic',
  ]
  return known.includes(url) ? url : ''
}

const loadAgentStatuses = async () => {
  configLoading.value = true
  try {
    const [claude, codex, opencode, keys] = await Promise.all([
      GetAgentConfigStatus('claude') as Promise<AgentConfigStatus>,
      GetAgentConfigStatus('codex') as Promise<AgentConfigStatus>,
      GetAgentConfigStatus('opencode') as Promise<AgentConfigStatus>,
      GetSavedProviderKeys(),
    ])
    agentStatuses.value = { claude, codex, opencode }
    savedProviderKeys.value = Object.fromEntries(
      Object.entries(keys).filter((entry): entry is [string, string] => typeof entry[1] === 'string')
    )
    if (isClaudeProvider(claude.provider)) {
      selectedClaudeProvider.value = claude.provider
    }
    if (claude.provider === 'mimo' && claude.currentBaseUrl) {
      claudeMimoBaseUrl.value = claude.currentBaseUrl
      selectedMimoPlan.value = resolveMiMoPlan(claude.currentBaseUrl)
    }
    if (claude.provider === 'dashscope' && claude.currentBaseUrl) {
      selectedDashScopePlan.value = resolveDashScopePlan(claude.currentBaseUrl)
    }
    if (codex.provider && codex.provider !== 'ccx' && codex.provider !== '') {
      selectedCodexProvider.value = codex.provider as AgentProvider
    } else {
      selectedCodexProvider.value = 'ccx'
    }
    if (codex.provider === 'mimo' && codex.currentBaseUrl) {
      selectedMimoCodexPlan.value = resolveMiMoCodexPlan(codex.currentBaseUrl) || 'https://api.xiaomimimo.com/v1'
    }
    // 恢复所有支持 mode 切换的 provider 的 mode 状态
    if (codex.provider === 'ccx' || codex.provider === 'dashscope' || codex.provider === 'opencode-zen' || codex.provider === 'opencode-go') {
      codexMode.value = codex.mode === 'plugin' ? 'plugin' : 'quick'
    }
    if (opencode.provider && opencode.provider !== 'ccx' && opencode.provider !== '') {
      selectedOpenCodeProvider.value = opencode.provider as AgentProvider
    } else {
      selectedOpenCodeProvider.value = 'ccx'
    }
    if (opencode.provider === 'mimo' && opencode.currentBaseUrl) {
      selectedMimoOpenCodePlan.value = resolveMiMoCodexPlan(opencode.currentBaseUrl) || 'https://api.xiaomimimo.com/v1'
    }
  } catch (error) {
    // error is handled by caller
  } finally {
    configLoading.value = false
  }
}

const findSavedKey = (provider: string, planID?: string): string => {
  if (planID) {
    const planKey = savedProviderKeys.value[`claude:${provider}:${planID}`]
    if (planKey) return planKey
  }
  return savedProviderKeys.value[`claude:${provider}`] || ''
}

const canApplyAgent = (platform: AgentPlatform) => {
  if (configLoading.value) return false
  if (platform === 'codex') {
    // CCX 用 proxy key，无需验证
    if (selectedCodexProvider.value === 'ccx') {
      return true
    }
    // OpenAI 直连：不勾选走 OAuth；勾选「我有自己的 API Key」则必须输入
    if (selectedCodexProvider.value === 'openai') {
      return !codexOpenAIUseOwnKey.value || codexOpenAIKey.value.trim() !== ''
    }
    // 第三方 provider 必须有输入的 key 或已保存的 key
    const inputKey = codexOpenAIKey.value.trim()
    const hasSaved = !!savedProviderKeys.value[`codex:${selectedCodexProvider.value}`]
    return inputKey !== '' || hasSaved
  }
  if (platform === 'opencode') {
    if (selectedOpenCodeProvider.value === 'ccx') {
      return true
    }
    const inputKey = openCodeOpenAIKey.value.trim()
    const hasSaved = !!savedProviderKeys.value[`codex:${selectedOpenCodeProvider.value}`]
    return inputKey !== '' || hasSaved
  }
  if (selectedClaudeProvider.value === 'ccx') return true
  const provider = selectedClaudeProvider.value
  const inputKey = claudeProviderKeys.value[provider].trim()
  const planID = provider === 'mimo' ? selectedMimoPlan.value
    : provider === 'dashscope' ? selectedDashScopePlan.value
    : undefined
  const hasSaved = !!findSavedKey(provider, planID)
  return inputKey !== '' || hasSaved
}

const applyAgent = async (platform: AgentPlatform) => {
  configLoading.value = true
  try {
    const request: ApplyAgentConfigRequest = { platform }
    if (platform === 'claude') {
      request.provider = selectedClaudeProvider.value
      if (selectedClaudeProvider.value !== 'ccx') {
        const inputKey = claudeProviderKeys.value[selectedClaudeProvider.value].trim()
        const planID = selectedClaudeProvider.value === 'mimo' ? selectedMimoPlan.value
          : selectedClaudeProvider.value === 'dashscope' ? selectedDashScopePlan.value
          : undefined
        request.apiKey = inputKey || findSavedKey(selectedClaudeProvider.value, planID)
      }
      if (selectedClaudeProvider.value === 'mimo') {
        request.baseUrl = claudeMimoBaseUrl.value.trim()
      }
      if (selectedClaudeProvider.value === 'dashscope') {
        request.baseUrl = selectedDashScopePlan.value.trim()
      }
    }
    if (platform === 'codex') {
      request.provider = selectedCodexProvider.value
      if (selectedCodexProvider.value === 'ccx' || isCodexThirdPartyWithMode(selectedCodexProvider.value)) {
        request.mode = codexMode.value
      }
      if (selectedCodexProvider.value === 'openai') {
        // OpenAI 直连只用用户当前输入的 key，不 fallback 到 saved key
        // 无 key → 后端走 OAuth 登录模式 (auth_mode="chatgpt")
        request.apiKey = codexOpenAIKey.value.trim()
      } else if (selectedCodexProvider.value !== 'ccx') {
        const inputKey = codexOpenAIKey.value.trim()
        request.apiKey = inputKey || savedProviderKeys.value[`codex:${selectedCodexProvider.value}`] || ''
      }
      if (selectedCodexProvider.value === 'mimo') {
        request.baseUrl = selectedMimoCodexPlan.value.trim()
      }
    }
    if (platform === 'opencode') {
      request.provider = selectedOpenCodeProvider.value
      if (selectedOpenCodeProvider.value !== 'ccx') {
        const inputKey = openCodeOpenAIKey.value.trim()
        request.apiKey = inputKey || savedProviderKeys.value[`codex:${selectedOpenCodeProvider.value}`] || ''
      }
      if (selectedOpenCodeProvider.value === 'mimo') {
        request.baseUrl = selectedMimoOpenCodePlan.value.trim()
      }
    }
    await ApplyAgentConfig(request)
    await loadAgentStatuses()
  } finally {
    configLoading.value = false
  }
}

const showApplyPreview = async (platform: AgentPlatform) => {
  const request: ApplyAgentConfigRequest = { platform }
  if (platform === 'claude') {
    request.provider = selectedClaudeProvider.value
    if (selectedClaudeProvider.value !== 'ccx') {
      const inputKey = claudeProviderKeys.value[selectedClaudeProvider.value].trim()
      request.apiKey = inputKey || findSavedKey(selectedClaudeProvider.value, selectedMimoPlan.value)
    }
    if (selectedClaudeProvider.value === 'mimo') {
      request.baseUrl = claudeMimoBaseUrl.value.trim()
    }
    if (selectedClaudeProvider.value === 'dashscope') {
      request.baseUrl = selectedDashScopePlan.value.trim()
    }
  }
  if (platform === 'codex') {
    request.provider = selectedCodexProvider.value
    if (selectedCodexProvider.value === 'ccx' || isCodexThirdPartyWithMode(selectedCodexProvider.value)) {
      request.mode = codexMode.value
    }
    if (selectedCodexProvider.value === 'openai') {
      // OpenAI 直连只用用户当前输入的 key，不 fallback 到 saved key
      request.apiKey = codexOpenAIKey.value.trim()
    } else if (selectedCodexProvider.value !== 'ccx') {
      const inputKey = codexOpenAIKey.value.trim()
      request.apiKey = inputKey || savedProviderKeys.value[`codex:${selectedCodexProvider.value}`] || ''
    }
    if (selectedCodexProvider.value === 'mimo') {
      request.baseUrl = selectedMimoCodexPlan.value.trim()
    }
  }
  if (platform === 'opencode') {
    request.provider = selectedOpenCodeProvider.value
    if (selectedOpenCodeProvider.value !== 'ccx') {
      const inputKey = openCodeOpenAIKey.value.trim()
      request.apiKey = inputKey || savedProviderKeys.value[`codex:${selectedOpenCodeProvider.value}`] || ''
    }
    if (selectedOpenCodeProvider.value === 'mimo') {
      request.baseUrl = selectedMimoOpenCodePlan.value.trim()
    }
  }
  // 检测模式切换，显示会话迁移警告
  diffWarning.value = undefined
  if (platform === 'codex' && (selectedCodexProvider.value === 'ccx' || isCodexThirdPartyWithMode(selectedCodexProvider.value))) {
    const currentMode = agentStatuses.value.codex?.mode === 'plugin' ? 'plugin' : 'quick'
    if (currentMode !== codexMode.value) {
      diffWarning.value = getT()('agent.sessionMigrationWarning')
    }
  }
  diffPendingPlatform.value = platform
  diffMode.value = 'apply'
  diffDialogOpen.value = true
  diffLoading.value = true
  diffResult.value = null
  try {
    diffResult.value = await PreviewAgentConfigDiff(request) as ConfigDiffResult
  } catch {
    diffResult.value = null
  } finally {
    diffLoading.value = false
  }
}

const confirmApply = async () => {
  diffDialogOpen.value = false
  await applyAgent(diffPendingPlatform.value)
}

const showRestorePreview = async (platform: AgentPlatform) => {
  diffPendingPlatform.value = platform
  diffMode.value = 'restore'
  diffDialogOpen.value = true
  diffLoading.value = true
  diffResult.value = null
  try {
    diffResult.value = await PreviewRestoreConfigDiff(platform) as ConfigDiffResult
  } catch {
    diffResult.value = null
  } finally {
    diffLoading.value = false
  }
}

const confirmRestore = async () => {
  diffDialogOpen.value = false
  await restoreAgent(diffPendingPlatform.value)
}

const closeDiffDialog = () => {
  diffDialogOpen.value = false
}

const codexSessionTargetProvider = computed(() => {
  if (selectedCodexProvider.value === 'ccx') {
    return codexMode.value === 'plugin' ? 'ccx' : 'openai'
  }
  if (isCodexThirdPartyWithMode(selectedCodexProvider.value)) {
    return codexMode.value === 'plugin' ? selectedCodexProvider.value : 'openai'
  }
  return 'openai'
})

const showMigrateDialog = () => {
  migrateResult.value = null
  migrateError.value = ''
  migrateDialogOpen.value = true
}

const confirmMigrate = async () => {
  migrateLoading.value = true
  migrateError.value = ''
  migrateResult.value = null
  try {
    migrateResult.value = await MigrateCodexSessions({
      provider: selectedCodexProvider.value,
      mode: codexMode.value,
    }) as MigrateCodexSessionsResult
  } catch (error) {
    migrateError.value = error instanceof Error ? error.message : String(error)
  } finally {
    migrateLoading.value = false
  }
}

const closeMigrateDialog = () => {
  if (migrateLoading.value) return
  migrateDialogOpen.value = false
}

const restoreAgent = async (platform: AgentPlatform) => {
  configLoading.value = true
  try {
    await RestoreAgentConfig(platform)
  } finally {
    configLoading.value = false
  }
}

export function useAgentConfig() {
  return {
    agentStatuses,
    configLoading,
    selectedClaudeProvider,
    claudeProviderKeys,
    savedProviderKeys,
    codexOpenAIKey,
    codexOpenAIUseOwnKey,
    codexMode,
    openCodeOpenAIKey,
    claudeMimoBaseUrl,
    selectedMimoPlan,
    selectedMimoCodexPlan,
    selectedMimoOpenCodePlan,
    selectedDashScopePlan,
    agentLabels,
    claudeProviderLabels,
    codexProviderLabels: getCodexProviderLabels(),
    agentPlatforms,
    isClaudeProvider,
    claudeProviderLabel,
    claudeTargetBaseUrl,
    agentStatusText,
    agentStatusClass,
    loadAgentStatuses,
    canApplyAgent,
    applyAgent,
    restoreAgent,
    selectedCodexProvider,
    selectedOpenCodeProvider,
    codexProviderLabel,
    codexTargetBaseUrl,
    openCodeProviderLabel,
    openCodeTargetBaseUrl,
    // Diff preview
    diffDialogOpen,
    diffResult,
    diffMode,
    diffLoading,
    diffPendingPlatform,
    diffWarning,
    showApplyPreview,
    showRestorePreview,
    confirmApply,
    confirmRestore,
    closeDiffDialog,
    // Codex session migration
    migrateDialogOpen,
    migrateLoading,
    migrateResult,
    migrateError,
    codexSessionTargetProvider,
    showMigrateDialog,
    confirmMigrate,
    closeMigrateDialog,
  }
}
