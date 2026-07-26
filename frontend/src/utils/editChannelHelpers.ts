import type { Channel } from '../services/api'

export function isValidUrl(url: string): boolean {
  try {
    new URL(url)
    return true
  } catch {
    return false
  }
}

export function normalizeModelCapabilities(record: Channel['modelCapabilities'] = {}): Channel['modelCapabilities'] {
  return Object.fromEntries(Object.entries(record).sort(([a], [b]) => a.localeCompare(b)))
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
