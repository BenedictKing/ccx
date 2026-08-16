import { deduplicateEquivalentBaseUrls, stripDashboardPathFromBaseUrl, type ServiceType } from './baseUrlSemantics'

/**
 * 快速添加渠道 - 输入解析工具
 *
 * 用于识别 API Key 和 URL 格式
 */

/**
 * 检测字符串是否看起来像配置键名
 * - 全大写 + 下划线分隔的多段单词：API_TIMEOUT_MS, ANTHROPIC_BASE_URL
 * - 全小写常见配置字段名：api_key, base_url, url, name, token, auth, secret, endpoint, model, type
 *   （来自用户粘贴 yaml/markdown 配置时的字段名残留，而不是真实密钥）
 */
const looksLikeConfigKey = (token: string): boolean => {
  // 1) 全大写字母 + 下划线，且由多个单词组成（至少包含一个下划线分隔的段）
  if (/^[A-Z][A-Z0-9]*(_[A-Z][A-Z0-9]*)+$/.test(token)) {
    return true
  }

  // 2) 全小写、常见配置字段名（精确白名单避免误伤短前缀密钥如 hf_short / r8_abc）
  const LOWERCASE_CONFIG_KEYS = new Set<string>([
    'api_key',
    'apikey',
    'api_secret',
    'access_key',
    'access_token',
    'auth_token',
    'auth_key',
    'secret_key',
    'secret',
    'token',
    'auth',
    'base_url',
    'baseurl',
    'base',
    'url',
    'endpoint',
    'host',
    'name',
    'model',
    'model_name',
    'type',
    'service_type',
    'provider',
    'env',
  ])
  if (LOWERCASE_CONFIG_KEYS.has(token.toLowerCase())) {
    return true
  }

  return false
}

/**
 * 各平台 API Key 格式的专用正则匹配
 *
 * 国际主流:
 * - OpenAI Legacy: sk-[a-zA-Z0-9]{48}
 * - OpenAI Project: sk-proj-[a-zA-Z0-9-]{100,}
 * - Anthropic: sk-ant-api03-[a-zA-Z0-9-]{80,}
 * - Google Gemini: AIza[0-9A-Za-z-_]{35}
 * - Azure OpenAI: 32位十六进制
 *
 * 新兴生态:
 * - Hugging Face: hf_[a-zA-Z0-9]{34}
 * - Groq: gsk_[a-zA-Z0-9]{52}
 * - Perplexity: pplx-[a-zA-Z0-9]{40,}
 * - Replicate: r8_[a-zA-Z0-9]+
 * - OpenRouter: sk-or-v1-[a-zA-Z0-9]{50,}
 *
 * 国内平台:
 * - DeepSeek/Moonshot/01.AI/SiliconFlow: sk-[a-zA-Z0-9]{48} (兼容 OpenAI)
 * - 智谱 AI: [a-z0-9]{32}\.[a-z0-9]+ (id.secret 格式)
 * - 火山引擎 Ark: UUID 格式
 * - 火山引擎 IAM: AK 开头
 */
const ZHIPU_API_KEY_PATTERN = /^[a-zA-Z0-9]{20,}\.[a-zA-Z0-9]{10,}$/

const PLATFORM_KEY_PATTERNS: RegExp[] = [
  // OpenAI Project Key (新格式，最长，优先匹配)
  /^sk-proj-[a-zA-Z0-9_-]{50,}$/,
  // Anthropic Claude
  /^sk-ant-api03-[a-zA-Z0-9_-]{50,}$/,
  // OpenRouter (混合大小写字母数字)
  /^sk-or-v1-[a-zA-Z0-9]{50,}$/,
  // OpenAI Legacy / DeepSeek / Moonshot / 01.AI / SiliconFlow
  /^sk-[a-zA-Z0-9]{20,}$/,
  // Google Gemini/PaLM (通常 39 字符，允许一定范围)
  /^AIza[0-9A-Za-z_-]{30,}$/,
  // Hugging Face
  /^hf_[a-zA-Z0-9]{30,}$/,
  // Groq
  /^gsk_[a-zA-Z0-9]{40,}$/,
  // Perplexity
  /^pplx-[a-zA-Z0-9]{40,}$/,
  // Replicate
  /^r8_[a-zA-Z0-9]{20,}$/,
  // 智谱 AI (id.secret 格式)
  ZHIPU_API_KEY_PATTERN,
  // 火山引擎 Ark (UUID 格式)
  /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/i,
  // 火山引擎 IAM AK
  /^AK[A-Z]{2,4}[a-zA-Z0-9]{20,}$/
]

/** 智谱传统 API Key 使用唯一的 id.secret 格式，可用于无 URL 时的 provider 推断。 */
export const isZhipuApiKey = (token: string): boolean => ZHIPU_API_KEY_PATTERN.test(token.trim())

/**
 * 检测字符串是否为有效的 API Key
 *
 * 支持的格式：
 * 1. 平台特定格式（优先匹配，准确度最高）
 * 2. 通用前缀格式：xx-xxx 或 xx_xxx（如 sk-xxx, ut_xxx, api-xxx）
 * 3. JWT 格式：eyJ 开头，包含两个点分隔的 base64 段
 * 4. 长随机字符串：≥32 字符的字母数字串（必须包含字母和数字）
 * 5. 宽松兜底：常见前缀 + 任意后缀（当以上都不匹配时）
 *
 * 排除的格式：
 * - 配置键名：全大写 + 下划线分隔（如 API_TIMEOUT_MS）
 */
export const isValidApiKey = (token: string): boolean => {
  // 首先排除配置键名格式
  if (looksLikeConfigKey(token)) {
    return false
  }

  // 1. 平台特定格式匹配（最准确）
  for (const pattern of PLATFORM_KEY_PATTERNS) {
    if (pattern.test(token)) {
      return true
    }
  }

  // 2. 通用前缀格式（前缀 2-6 字母 + 连字符/下划线 + 至少 10 字符后缀）
  // 后缀必须包含数字或混合大小写（随机特征）
  if (/^[a-zA-Z]{2,6}[-_][a-zA-Z0-9_-]{10,}$/.test(token)) {
    const suffix = token.replace(/^[a-zA-Z]{2,6}[-_]/, '')
    const hasDigit = /\d/.test(suffix)
    const hasMixedCase = /[a-z]/.test(suffix) && /[A-Z]/.test(suffix)
    if (hasDigit || hasMixedCase) {
      return true
    }
  }

  // 3. JWT 格式 (eyJ 开头，包含两个点分隔的 base64 段，总长度 >= 20)
  if (/^eyJ[a-zA-Z0-9_-]+\.[a-zA-Z0-9_-]+\./.test(token) && token.length >= 20) {
    return true
  }

  // 4. 长随机字符串（≥32 字符，必须同时包含字母和数字）
  if (token.length >= 32 && /^[a-zA-Z0-9_-]+$/.test(token) && /[a-zA-Z]/.test(token) && /\d/.test(token)) {
    return true
  }

  // 5. 宽松兜底：常见 API Key 前缀 + 任意后缀（至少 1 个字符）
  // 当以上严格规则都不匹配时，放松标准识别常见格式
  // 支持：sk-xxx, api-xxx, key-xxx, ut_xxx, hf_xxx, gsk_xxx 等
  if (/^(sk|api|key|ut|hf|gsk|cr|ms|r8|pplx)[-_].+$/i.test(token)) {
    return true
  }

  return false
}

/**
 * 检测字符串是否为有效的 URL
 *
 * 要求：
 * - 必须以 http:// 或 https:// 开头
 * - 必须包含有效域名（域名段不能以横线开头或结尾）
 * - 支持末尾 # 标记（用于跳过自动添加 /v1）
 */
export const isValidUrl = (token: string): boolean => {
  // 域名段不能以横线开头或结尾，支持末尾 # 或 / 或直接结束
  return /^https?:\/\/[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?)*(:\d+)?(\/|#|$)/i.test(
    token
  )
}

/**
 * 从输入中提取所有 token
 * 按空白/逗号/分号/中英文冒号/换行/引号（中英文）/等号/%20 分割
 * 注意：URL 里的 `://` 冒号必须保留，否则会被误切。
 */
const extractTokens = (input: string): string[] => {
  // 用占位符保护整段 URL 中的所有冒号（含 `://` 协议冒号与 `host:port` 端口冒号），
  // 避免被英文冒号分隔符切碎。匹配范围：从 `http(s)://` 到下一个空白/引号/中文标点/逗号/分号/换行。
  const COLON_PLACEHOLDER = '__URLCOLON__'
  const protectedInput = input
    .replace(/%20/g, ' ')
    .replace(/https?:\/\/[^\s,;，；：="“”'‘’\n]+/gi, m => m.replace(/:/g, COLON_PLACEHOLDER))

  return protectedInput
    .split(/[\n\s,;，；：:="“”'‘’]+/)
    .map(t => t.split(COLON_PLACEHOLDER).join(':'))
    .filter(t => t.length > 0)
}

/**
 * 根据 URL 路径检测服务类型，并返回清理后的 baseUrl
 * /messages → claude, /chat/completions → openai, /responses → responses
 */
const detectServiceTypeAndCleanUrl = (
  url: string
): { serviceType: 'openai' | 'gemini' | 'claude' | 'responses' | 'copilot' | null; cleanedUrl: string } => {
  try {
    const cleanUrl = url.replace(/#$/, '')
    const parsed = new URL(cleanUrl)
    const path = parsed.pathname.toLowerCase()

    const endpointRules: Array<{ pattern: RegExp; serviceType: 'openai' | 'gemini' | 'claude' | 'responses' | 'copilot' }> = [
      { pattern: /\/v\d+[a-z]*\/messages(?:\/|$)/, serviceType: 'claude' },
      { pattern: /\/messages(?:\/|$)/, serviceType: 'claude' },
      { pattern: /\/v\d+[a-z]*\/embeddings(?:\/|$)/, serviceType: 'openai' },
      { pattern: /\/embeddings(?:\/|$)/, serviceType: 'openai' },
      { pattern: /\/v\d+[a-z]*\/chat\/completions(?:\/|$)/, serviceType: 'openai' },
      { pattern: /\/chat\/completions(?:\/|$)/, serviceType: 'openai' },
      { pattern: /\/v\d+[a-z]*\/responses(?:\/|$)/, serviceType: 'responses' },
      { pattern: /\/responses(?:\/|$)/, serviceType: 'responses' },
      { pattern: /\/v\d+[a-z]*\/models\/[^/]+:generatecontent(?:\/|$)/, serviceType: 'gemini' },
      { pattern: /\/models\/[^/]+:generatecontent(?:\/|$)/, serviceType: 'gemini' },
      { pattern: /\/generatecontent(?:\/|$)/, serviceType: 'gemini' },
    ]
    for (const rule of endpointRules) {
      const match = path.match(rule.pattern)
      if (!match || match.index === undefined) continue
      // 移除协议端点路径，保留 /v1 或 /v1beta 等版本前缀。
      parsed.pathname = path.slice(0, match.index) || '/'
      let result = parsed.toString().replace(/\/$/, '')
      if (url.endsWith('#')) result += '#'
      return { serviceType: rule.serviceType, cleanedUrl: result }
    }

    const urlKey = `${parsed.origin}${path}`.replace(/\/$/, '')
    if (parsed.hostname.toLowerCase() === 'api.githubcopilot.com') {
      return { serviceType: 'copilot', cleanedUrl: parsed.origin }
    }
    const knownClaudeUrls = new Set([
      'https://cp.compshare.cn',
      'https://api.kimi.com/coding',
      'https://ark.cn-beijing.volces.com/api/coding',
      'https://openrouter.ai/api',
      'https://api-inference.modelscope.cn',
      'https://api.easytransnote.com/coding',
    ])
    const knownOpenAIUrls = new Set([
      'https://api.deepseek.com/v1',
      'https://api.xiaomimimo.com/v1',
      'https://token-plan-cn.xiaomimimo.com/v1',
      'https://token-plan-sgp.xiaomimimo.com/v1',
      'https://token-plan-ams.xiaomimimo.com/v1',
      'https://cp.compshare.cn/v1',
      'https://api.moonshot.cn/v1',
      'https://api.kimi.com/coding/v1',
      'https://open.bigmodel.cn/api/coding/paas/v4',
      'https://open.bigmodel.cn/api/paas/v4',
      'https://api.minimax.chat/v1',
      'https://dashscope.aliyuncs.com/compatible-mode/v1',
      'https://coding.dashscope.aliyuncs.com/v1',
      'https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1',
      'https://opencode.ai/zen/v1',
      'https://opencode.ai/zen/go/v1',
      'https://api.lkeap.cloud.tencent.com/plan/v3',
      'https://ark.cn-beijing.volces.com/api/coding/v3',
      'https://qianfan.baidubce.com/v2/coding',
      'https://runapi.co/api/v1',
      'https://unity2.ai/v1',
      'https://openrouter.ai/api/v1',
      'https://api-inference.modelscope.cn/v1',
      'https://api.easytransnote.com/coding/v1',
      'https://api-ai.gitcode.com/v1',
    ])
    if (knownClaudeUrls.has(urlKey)) {
      return { serviceType: 'claude', cleanedUrl: cleanUrl }
    }
    if (knownOpenAIUrls.has(urlKey)) {
      return { serviceType: 'openai', cleanedUrl: cleanUrl }
    }

    const hintText = `${parsed.hostname.toLowerCase()} ${path}`
    if (/\b(anthropic|claude)\b/.test(hintText) || /(^|\/)(anthropic|claude)(?:\/|$)/.test(path)) {
      return { serviceType: 'claude', cleanedUrl: url }
    }
    if (/\bgemini\b/.test(hintText) || /generativelanguage\.googleapis\.com$/i.test(parsed.hostname)) {
      return { serviceType: 'gemini', cleanedUrl: url }
    }
    if (/\bresponses\b/.test(hintText)) {
      return { serviceType: 'responses', cleanedUrl: url }
    }
    if (/\bcopilot\b/.test(hintText)) {
      return { serviceType: 'copilot', cleanedUrl: url }
    }
    if (/\b(openai|chatgpt)\b/.test(hintText)) {
      return { serviceType: 'openai', cleanedUrl: url }
    }

    // 剔除常见第三方面板路径，仅保留 origin 作为 baseUrl。
    const dashboardUrl = stripDashboardPathFromBaseUrl(url)
    if (dashboardUrl !== url) {
      return { serviceType: null, cleanedUrl: dashboardUrl }
    }
  } catch {
    // 忽略解析错误
  }
  return { serviceType: null, cleanedUrl: url }
}

// 保留导出以兼容可能的外部使用
export const detectServiceType = (url: string): 'openai' | 'gemini' | 'claude' | 'responses' | 'copilot' | null => {
  return detectServiceTypeAndCleanUrl(url).serviceType
}

/** Base URL 最大数量限制 */
const MAX_BASE_URLS = 10

/**
 * 对 Base URL 列表去重（等效 URL 仅保留一条；尾部 # 视为不同语义）
 */
export function deduplicateBaseUrls(urls: string[], serviceType: ServiceType = ''): string[] {
  return deduplicateEquivalentBaseUrls(urls, serviceType)
}

/**
 * 解析快速输入内容，提取 URL 和 API Keys
 *
 * 支持的格式：
 * 1. 纯文本：URL 和 API Key 以空白/逗号/分号/等号分隔
 * 2. 引号包裹：从 "xxx" 或 'xxx' 中提取内容（支持 JSON 配置格式）
 * 3. 多 Base URL：所有符合 HTTP 链接格式的都作为 baseUrl（最多 10 个）
 */
export const parseQuickInput = (
  input: string,
  fallbackServiceType: ServiceType = ''
): {
  detectedBaseUrl: string
  detectedBaseUrls: string[]
  rawBaseUrls: string[]
  detectedApiKeys: string[]
  detectedServiceType: 'openai' | 'gemini' | 'claude' | 'responses' | 'copilot' | null
} => {
  const rawUrls: string[] = []
  let detectedServiceType: 'openai' | 'gemini' | 'claude' | 'responses' | 'copilot' | null = null
  const detectedApiKeys: string[] = []

  const tokens = extractTokens(input)

  for (const token of tokens) {
    if (isValidUrl(token)) {
      const endsWithHash = token.endsWith('#')
      let url = endsWithHash ? token.slice(0, -1) : token
      url = url.replace(/\/$/, '')
      const fullUrl = endsWithHash ? url + '#' : url

      // 检测协议并清理 URL（移除端点路径）
      const { serviceType, cleanedUrl } = detectServiceTypeAndCleanUrl(fullUrl)

      rawUrls.push(cleanedUrl)
      // 使用第一个 URL 的服务类型
      if (!detectedServiceType) {
        detectedServiceType = serviceType
      }
      continue
    }

    if (isValidApiKey(token) && !detectedApiKeys.includes(token)) {
      detectedApiKeys.push(token)
    }
  }

  // 先去重，再限制数量，避免误拒绝等效 URL
  const deduplicatedRawBaseUrls = Array.from(new Set(rawUrls)).slice(0, MAX_BASE_URLS)
  const detectedBaseUrls = deduplicateBaseUrls(deduplicatedRawBaseUrls, detectedServiceType || fallbackServiceType).slice(0, MAX_BASE_URLS)

  return {
    detectedBaseUrl: detectedBaseUrls[0] || '',
    detectedBaseUrls,
    rawBaseUrls: deduplicatedRawBaseUrls,
    detectedApiKeys,
    detectedServiceType
  }
}
