export type HealthInfo = {
  status?: string
  timestamp?: string
  uptime?: number
  mode?: string
  version?: {
    version?: string
    buildTime?: string
    gitCommit?: string
  }
  config?: {
    upstreamCount?: number
  }
}

export type DesktopStatus = {
  running: boolean
  starting: boolean
  attached?: boolean
  port: number
  url: string
  pid: number
  binaryPath: string
  dataDir: string
  health?: HealthInfo
  lastError?: string
  logs: string[]
}

export type AgentPlatform = 'claude' | 'codex' | 'opencode'
export type AgentProvider = 'ccx' | 'deepseek' | 'mimo' | 'compshare' | 'runapi' | 'unity2' | 'tencent-lkeap' | 'kimi' | 'kimi-code' | 'volc-ark' | 'qianfan' | 'originrouter' | 'glm' | 'sensenova' | 'minimax' | 'dashscope' | 'openrouter' | 'modelscope' | 'opencode-zen' | 'opencode-go' | 'openai' | 'xfyun'

export type AgentConfigStatus = {
  platform: AgentPlatform
  provider?: string
  mode?: 'quick' | 'plugin'
  targetProvider?: string
  configured: boolean
  matchesCurrentPort: boolean
  needsUpdate: boolean
  currentBaseUrl: string
  targetBaseUrl: string
  configPath: string
  authPath?: string
  hasState: boolean
  lastError?: string
  authMode?: string
  configConsistent?: boolean
  diagnosticCode?: string
  diagnosticMessage?: string
}

export type ApplyAgentConfigRequest = {
  platform: AgentPlatform
  provider?: AgentProvider
  apiKey?: string
  baseUrl?: string
  mode?: 'quick' | 'plugin'
}

export type MigrateCodexSessionsRequest = {
  provider?: AgentProvider
  mode?: 'quick' | 'plugin'
}

export type MigrateCodexSessionsResult = {
  targetProvider: string
  totalFiles: number
  migratedFiles: number
  skippedFiles: number
  failedFiles: number
  sqliteRowsUpdated: number
  sqliteSkipped: boolean
  sqliteError?: string
}

export type TabValue = 'status' | 'agent' | 'env' | 'channels' | 'subscriptions' | 'cockpit' | 'dashboard' | 'health' | 'autopilot' | 'conversations' | 'reports'

export type ProviderPlan = {
  id: string
  label: string
  baseUrl: string
  description: string
  recommended: boolean
  custom: boolean
}

export type ChannelTarget = {
  type: 'messages' | 'chat' | 'responses'
  label: string
  description: string
  recommended: boolean
}

export type ProviderPreset = {
  id: string
  order?: number
  label: string
  description: string
  directAgent: boolean
  nativeMessages: boolean
  chatCompatible: boolean
  responsesCompatible: boolean
  plans: ProviderPlan[]
  targets: ChannelTarget[]
  defaultTarget: 'messages' | 'chat' | 'responses'
}

export type ProviderKeyAsset = {
  provider: string
  apiKey: string
  baseUrl?: string
  planId?: string
  proxyUrl?: string
  usages?: string[]
}

export type CreateChannelRequest = {
  provider: string
  target: string
  planId?: string
  baseUrl?: string
  apiKey?: string
  name?: string
  description?: string
  proxyUrl?: string
}

export type CreateChannelResult = {
  provider: string
  target: string
  name: string
  baseUrl: string
  message: string
}

export type DiffLine = {
  type: 'context' | 'added' | 'removed'
  content: string
}

export type FileDiff = {
  path: string
  action: 'modify' | 'create' | 'delete'
  lines: DiffLine[]
}

export type ConfigDiffResult = {
  files: FileDiff[]
}
