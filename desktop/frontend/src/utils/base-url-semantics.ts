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
