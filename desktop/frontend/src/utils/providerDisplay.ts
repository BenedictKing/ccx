import type { Channel } from '@/services/admin-api'

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
