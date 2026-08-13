/**
 * HTTP 缓存层 - 支持 ETag/If-None-Match 条件请求 + 内容哈希比对
 *
 * 用途：
 * - 对支持 ETag/Last-Modified 的 API（deepswe、dradar、benchlm 数据文件），发送条件请求，304 时复用缓存
 * - 对不支持条件请求的页面，拉取后比对内容哈希，跳过未变更的处理
 *
 * 缓存文件：<project-root>/.cache/benchmark-http-cache.json
 */

import { createHash } from 'node:crypto'
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'

import './http.mjs'

const root = dirname(dirname(dirname(fileURLToPath(import.meta.url))))
const CACHE_DIR = join(root, '.cache')
export const CACHE_PATH = join(CACHE_DIR, 'benchmark-http-cache.json')

/** @type {Object} 内存中的缓存 */
let _cache = null

/**
 * 加载缓存
 */
export function loadCache() {
  if (_cache) return _cache
  try {
    if (existsSync(CACHE_PATH)) {
      _cache = JSON.parse(readFileSync(CACHE_PATH, 'utf8'))
    }
  } catch {
    // 缓存损坏，忽略
  }
  if (!_cache || !_cache.entries) {
    _cache = { version: 1, entries: {} }
  }
  return _cache
}

/**
 * 保存缓存到磁盘
 */
export function saveCache() {
  if (!_cache) return
  if (!existsSync(CACHE_DIR)) {
    mkdirSync(CACHE_DIR, { recursive: true })
  }
  writeFileSync(CACHE_PATH, JSON.stringify(_cache, null, 2) + '\n', 'utf8')
}

/**
 * 获取缓存条目
 * @param {string} key
 * @returns {Object|null}
 */
export function getCacheEntry(key) {
  const cache = loadCache()
  return cache.entries[key] || null
}

/**
 * 设置缓存条目
 * @param {string} key
 * @param {Object} entry
 */
export function setCacheEntry(key, entry) {
  const cache = loadCache()
  cache.entries[key] = { ...entry, lastFetch: new Date().toISOString() }
}

/**
 * 计算内容的 SHA256 哈希
 * @param {string} content
 * @returns {string}
 */
export function contentHash(content) {
  return createHash('sha256').update(content).digest('hex').slice(0, 16)
}

/**
 * 带条件请求的 fetch
 *
 * 如果缓存中有 ETag 或 Last-Modified，发送对应的条件请求头。
 * 服务器返回 304 时，返回 { status: 304, cached: true } 而非完整响应。
 *
 * @param {string} url
 * @param {Object} options - fetch 选项
 * @param {number} timeoutMs
 * @returns {Promise<{status: number, cached: boolean, response?: Response, data?: any}>}
 */
export async function cachedFetch(url, options = {}, timeoutMs = 15_000) {
  const cacheKey = `etag:${url}`
  const entry = getCacheEntry(cacheKey)
  const headers = { ...(options.headers || {}) }

  if (entry?.etag) {
    headers['If-None-Match'] = entry.etag
  }
  if (entry?.lastModified) {
    headers['If-Modified-Since'] = entry.lastModified
  }

  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), timeoutMs)
  timer.unref?.()

  try {
    const resp = await fetch(url, { ...options, headers, signal: controller.signal })

    if (resp.status === 304) {
      // 未修改，返回缓存数据
      return { status: 304, cached: true, data: entry.data }
    }

    // 更新缓存
    const newEtag = resp.headers.get('etag')
    const newLastModified = resp.headers.get('last-modified')
    const cacheEntry = {}
    if (newEtag) cacheEntry.etag = newEtag
    if (newLastModified) cacheEntry.lastModified = newLastModified

    if (newEtag || newLastModified) {
      setCacheEntry(cacheKey, cacheEntry)
    }

    return { status: resp.status, cached: false, response: resp }
  } catch (error) {
    if (error?.name === 'AbortError') {
      throw new Error(`Request timed out after ${timeoutMs}ms: ${url}`)
    }
    throw error
  } finally {
    clearTimeout(timer)
  }
}

/**
 * 带内容缓存的 fetch（用于不支持 ETag 的端点）
 *
 * 始终拉取完整响应，但比对内容哈希。如果哈希未变，返回缓存数据。
 * 适用于 benchlm.ai 这类 Next.js SSR 页面（ETag 不稳定但数据可能未变）。
 *
 * @param {string} url
 * @param {Object} options
 * @param {number} timeoutMs
 * @returns {Promise<{changed: boolean, response?: Response, cachedData?: any}>}
 */
export async function cachedFetchByContent(url, options = {}, timeoutMs = 15_000) {
  const cacheKey = `content:${url}`
  const entry = getCacheEntry(cacheKey)

  const controller = new AbortController()
  const timer = setTimeout(() => controller.abort(), timeoutMs)
  timer.unref?.()

  try {
    const resp = await fetch(url, { ...options, signal: controller.signal })

    if (!resp.ok) {
      throw new Error(`HTTP ${resp.status} ${resp.statusText} for ${url}`)
    }

    const text = await resp.text()
    const hash = contentHash(text)

    if (entry?.dataHash === hash) {
      return { changed: false, cachedData: entry.data }
    }

    // 内容已变更，更新缓存
    setCacheEntry(cacheKey, { dataHash: hash })
    return { changed: true, response: resp, _text: text }
  } catch (error) {
    if (error?.name === 'AbortError') {
      throw new Error(`Request timed out after ${timeoutMs}ms: ${url}`)
    }
    throw error
  } finally {
    clearTimeout(timer)
  }
}

/**
 * 将响应数据存入缓存（配合 cachedFetch 的 304 场景使用）
 * @param {string} url
 * @param {any} data - 响应解析后的 JSON 数据
 */
export function cacheResponseData(url, data) {
  const cacheKey = `etag:${url}`
  const entry = getCacheEntry(cacheKey) || {}
  entry.data = data
  setCacheEntry(cacheKey, entry)
}

/**
 * 将内容缓存的数据存入缓存（配合 cachedFetchByContent 使用）
 * @param {string} url
 * @param {any} data
 */
export function cacheContentData(url, data) {
  const cacheKey = `content:${url}`
  const entry = getCacheEntry(cacheKey) || {}
  entry.data = data
  setCacheEntry(cacheKey, entry)
}

/**
 * 获取缓存数据的年龄（毫秒）
 * @param {string} key
 * @returns {number} - 毫秒，无缓存返回 Infinity
 */
export function cacheAge(key) {
  const entry = getCacheEntry(key)
  if (!entry?.lastFetch) return Infinity
  return Date.now() - new Date(entry.lastFetch).getTime()
}

/**
 * 简单的键值缓存（用于 dradar cacheVersion、litellm blobSha 等）
 * @param {string} key
 * @returns {any|null}
 */
export function getSimpleCache(key) {
  return getCacheEntry(key)?.value ?? null
}

/**
 * @param {string} key
 * @param {any} value
 */
export function setSimpleCache(key, value) {
  setCacheEntry(key, { value })
}

/**
 * 获取缓存条目是否存在
 * @param {string} key
 * @returns {boolean}
 */
export function hasCacheEntry(key) {
  return getCacheEntry(key) != null
}