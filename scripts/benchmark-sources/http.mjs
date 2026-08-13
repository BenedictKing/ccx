import http from 'node:http'

export const DEFAULT_FETCH_TIMEOUT_MS = 15_000

let proxyConfigured = false

export function resolveProxyEnv(env = process.env) {
  const proxyEnv = { ...env }
  const allProxy = env.all_proxy || env.ALL_PROXY

  if (allProxy) {
    if (!env.http_proxy && !env.HTTP_PROXY) proxyEnv.http_proxy = allProxy
    if (!env.https_proxy && !env.HTTPS_PROXY) proxyEnv.https_proxy = allProxy
  }

  return proxyEnv
}

export function configureEnvProxy(env = process.env) {
  if (proxyConfigured || typeof http.setGlobalProxyFromEnv !== 'function') return false

  const proxyEnv = resolveProxyEnv(env)
  const hasProxy = ['http_proxy', 'HTTP_PROXY', 'https_proxy', 'HTTPS_PROXY'].some(
    (key) => proxyEnv[key],
  )
  if (!hasProxy) return false

  http.setGlobalProxyFromEnv(proxyEnv)
  proxyConfigured = true
  return true
}

configureEnvProxy()

export async function fetchWithTimeout(url, options = {}, timeoutMs = DEFAULT_FETCH_TIMEOUT_MS) {
  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), timeoutMs)
  timer.unref?.()

  try {
    return await fetch(url, { ...options, signal: controller.signal })
  } catch (error) {
    if (error?.name === 'AbortError') {
      throw new Error(`Request timed out after ${timeoutMs}ms: ${url}`)
    }
    throw error
  } finally {
    clearTimeout(timer)
  }
}
