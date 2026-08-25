import type { Channel } from '@/services/api'

// 品牌名静态镜像：与后端 provider_templates.go 各 ProviderTemplate 的 DisplayName 保持一致。
// providerDisplayName 是同步函数（渠道卡片渲染 / 编辑弹窗 computed 直接调用），
// 不便走异步的 getProviderTemplates()，故在此维护一份静态兜底表。
// 新增 provider 时必须同步注册，否则会退化为按 providerId 拼出的可读名（如 Tokenrhythm）。
const PROVIDER_BRAND_NAMES: Record<string, string> = {
  mimo: 'MiMo',
  openai: 'OpenAI',
  deepseek: 'DeepSeek',
  gemini: 'Gemini',
  anthropic: 'Anthropic',
  kimi: 'Kimi',
  'kimi-code': 'Kimi Coding Plan',
  glm: '智谱 GLM',
  volcengine: '火山方舟',
  'volc-ark': '火山方舟',
  compshare: '优云智算',
  sensenova: 'SenseNova',
  minimax: 'MiniMax',
  tokenrhythm: '基元律动',
  dashscope: '阿里云 DashScope',
  'opencode-zen': 'OpenCode Zen / Go',
  'opencode-go': 'OpenCode Go',
  'tencent-lkeap': '腾讯云 TokenHub',
  qianfan: '百度千帆',
  xfyun: '讯飞星辰',
  openrouter: 'OpenRouter',
  modelscope: 'ModelScope 魔搭',
  originrouter: '极易云 OriginRouter',
  atomgit: 'AtomGit'
}

export const providerDisplayName = (providerId?: string): string => {
  const normalized = providerId?.trim().toLowerCase() ?? ''
  if (!normalized) return ''
  if (PROVIDER_BRAND_NAMES[normalized]) return PROVIDER_BRAND_NAMES[normalized]

  return normalized
    .split(/[-_\s]+/)
    .filter(Boolean)
    .map(part => part.charAt(0).toUpperCase() + part.slice(1))
    .join(' ')
}

export const isManagedProviderChannel = (channel?: Channel | null): boolean => {
  return !!channel?.providerId && !!channel.accountUid
}

export const isOfficialProviderChannel = (channel?: Channel | null): boolean => {
  return channel?.originType === 'official_api' || channel?.originType === 'official_token_plan'
}

export const isAutoManagedAccountChannel = (channel?: Channel | null): boolean => {
  return !!channel?.accountUid && (!!channel.autoManaged || !!channel.providerId)
}

type TranslateFn = (key: string, params?: Record<string, string | number>) => string

// 托管渠道友好展示名（如 "Kimi 官方渠道" / "Official Kimi channel"），
// 渠道卡片与编辑弹窗头部共用；非托管渠道或无品牌名时返回空串。
export const managedProviderChannelName = (channel: Channel | null | undefined, t: TranslateFn): string => {
  if (!isManagedProviderChannel(channel)) return ''
  const provider = providerDisplayName(channel?.providerId)
  if (!provider) return ''
  return t(
    isOfficialProviderChannel(channel)
      ? 'channelEditor.managed.officialChannel'
      : 'channelEditor.managed.providerChannel',
    { provider },
  )
}
