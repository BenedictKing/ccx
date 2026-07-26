export type ServiceType = 'openai' | 'gemini' | 'claude' | 'responses' | 'copilot' | ''

const versionSuffixPattern = /\/v\d+[a-z]*$/
const dashboardPathPrefixes = [
  '/admin',
  '/console',
  '/dashboard',
  '/keys',
  '/api-keys',
  '/apikeys',
  '/panel',
  '/token',
  '/profile',
  '/usage',
  '/wallet',
  '/log',
  '/pricing'
]

export function getDefaultVersionPrefix(serviceType: ServiceType): '/v1' | '/v1beta' | '' {
  if (serviceType === 'copilot') return ''
  return serviceType === 'gemini' ? '/v1beta' : '/v1'
}

export function stripDashboardPathFromBaseUrl(rawUrl: string): string {
  const trimmed = rawUrl.trim()
  if (!trimmed) return ''

  const hasHash = trimmed.endsWith('#')
  const withoutHash = hasHash ? trimmed.slice(0, -1) : trimmed

  try {
    const parsed = new URL(withoutHash)
    const path = parsed.pathname.toLowerCase()
    if (dashboardPathPrefixes.some(prefix => path === prefix || path.startsWith(prefix + '/'))) {
      return parsed.origin + (hasHash ? '#' : '')
    }
  } catch {
    return trimmed
  }

  return trimmed
}

export function normalizeBaseUrl(rawUrl: string): { normalized: string; hasHash: boolean } {
  const trimmed = stripDashboardPathFromBaseUrl(rawUrl)
  if (!trimmed) {
    return { normalized: '', hasHash: false }
  }

  const hasHash = trimmed.endsWith('#')
  const withoutHash = hasHash ? trimmed.slice(0, -1) : trimmed
  return {
    normalized: withoutHash.replace(/\/+$/, ''),
    hasHash
  }
}

export function canonicalBaseUrl(rawUrl: string, serviceType: ServiceType): string {
  const { normalized, hasHash } = normalizeBaseUrl(rawUrl)
  if (!normalized) return ''
  if (hasHash) return normalized + '#'

  const versionPrefix = getDefaultVersionPrefix(serviceType)
  if (versionPrefix && normalized.endsWith(versionPrefix)) {
    return normalized.slice(0, -versionPrefix.length)
  }
  return normalized
}

export function metricsIdentityBaseUrl(rawUrl: string, serviceType: ServiceType): string {
  const { normalized, hasHash } = normalizeBaseUrl(rawUrl)
  if (!normalized) return ''
  if (hasHash) return normalized + '#'
  const versionPrefix = getDefaultVersionPrefix(serviceType)
  if (!versionPrefix) return normalized
  if (versionSuffixPattern.test(normalized)) return normalized
  return normalized + versionPrefix
}

export function deduplicateEquivalentBaseUrls(urls: string[], serviceType: ServiceType): string[] {
  const seen = new Set<string>()
  const result: string[] = []

  urls.forEach(rawUrl => {
    const canonical = canonicalBaseUrl(rawUrl, serviceType)
    if (!canonical || seen.has(canonical)) return
    seen.add(canonical)
    result.push(canonical)
  })

  return result
}

/**
 * 判断 base URL 是否需要 x-anthropic-billing-header。
 * 与后端 config.requiresAnthropicBillingHeader 保持同一规则：
 * 仅精确匹配 api.anthropic.com 主域名，子域名视为第三方，端口号不影响判断。
 */
export function requiresAnthropicBillingHeader(baseUrl: string): boolean {
  let s = baseUrl.trim().toLowerCase()
  const schemeIdx = s.indexOf('://')
  if (schemeIdx >= 0) s = s.slice(schemeIdx + 3)
  s = s.replace(/\/+$/, '')
  const pathIdx = s.indexOf('/')
  let host = pathIdx >= 0 ? s.slice(0, pathIdx) : s
  const portIdx = host.lastIndexOf(':')
  if (portIdx >= 0) host = host.slice(0, portIdx)
  return host === 'api.anthropic.com'
}

/**
 * 计算 stripBillingHeader 的默认值：渠道所有地址均为 Anthropic 官方时保留计费头，
 * 其余情况（第三方中转、自建网关等）默认剔除，避免每次变化的 cch= nonce 打穿上游 prompt 缓存。
 * 地址全为空时返回 false，等待用户填入 baseUrl 后再重算。
 */
export function defaultStripBillingHeader(baseUrls: string[]): boolean {
  const urls = baseUrls.map(url => url.trim()).filter(Boolean)
  if (urls.length === 0) return false
  return urls.some(url => !requiresAnthropicBillingHeader(url))
}

export function buildExpectedRequestUrl(
  serviceType: ServiceType,
  endpoint: string,
  rawBaseUrl: string
): string {
  const { normalized, hasHash } = normalizeBaseUrl(rawBaseUrl)
  if (!normalized) return ''
  if (hasHash || versionSuffixPattern.test(normalized)) {
    return normalized + endpoint
  }
  return normalized + getDefaultVersionPrefix(serviceType) + endpoint
}
