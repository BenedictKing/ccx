/**
 * Artificial Analysis 数据抓取器（首个需 API key 的源）
 *
 * 端点（Free tier，文档：https://artificialanalysis.ai/data-api）：
 * - GET /api/v2/language/models/free          分页，返回 evaluations 复合指数（intelligence/coding/agentic）+ 中位性能 + 输入输出定价
 * - GET /api/v2/media/text-to-image/models/free 非分页，返回图像生成 arena 的 Elo + CI
 *
 * 鉴权：`x-api-key: <key>`，key 由环境变量 ARTIFICIAL_ANALYSIS_API_KEY 提供；缺失时由
 * 上层 update-benchmark-data.mjs 短路跳过，本模块不读 env。
 *
 * 落点：
 * - LLM 复合指数 → benchmarkProfile.benchmarkEvidence（benchmark='artificial_analysis'），不碰
 *   overallScore/categoryScores（benchlm 保留所有权，避免不同量表互相覆盖）。
 * - 图像 arena Elo → registry.imageArenaProfiles（顶层新 section，preset manifest 整包拷贝，生成器忽略）。
 *
 * 缓存：每页 URL 走 cachedFetch（ETag/304）；图像 arena 同理。
 */

import { cachedFetch, cacheResponseData } from './http-cache.mjs'

const BASE_URL = 'https://artificialanalysis.ai/api/v2'
const LANGUAGE_MODELS_FREE_URL = `${BASE_URL}/language/models/free`
const TEXT_TO_IMAGE_FREE_URL = `${BASE_URL}/media/text-to-image/models/free`

const FETCH_HEADERS = {
  'User-Agent': 'ccx-benchmark-updater/1.0',
  Accept: 'application/json',
}

/**
 * Artificial Analysis LLM slug -> CCX canonicalModel 映射。
 * AA slug 命名（点号 vs 连字符）文档未完全明示，对含点号的 canonical 同时收录点号/连字符
 * 两种形式以最大化命中率；首跑 unmapped 日志会提示未命中的真实 slug，便于微调。
 */
export const ARTIFICIAL_ANALYSIS_MODEL_MAP = {
  'claude-opus-4-8': 'claude-opus-4-8',
  'claude-opus-5': 'claude-opus-5',
  'claude-sonnet-5': 'claude-sonnet-5',
  'claude-sonnet-4-6': 'claude-sonnet-4-6',
  'claude-haiku-4-5': 'claude-haiku-4.5',
  'claude-fable-5': 'claude-fable-5',
  'gpt-5.6-sol': 'gpt-5.6-sol',
  'gpt-5-6-sol': 'gpt-5.6-sol',
  'gpt-5.6-terra': 'gpt-5.6-terra',
  'gpt-5-6-terra': 'gpt-5.6-terra',
  'gpt-5.6-luna': 'gpt-5.6-luna',
  'gpt-5-6-luna': 'gpt-5.6-luna',
  'gpt-5.5': 'gpt-5.5',
  'gpt-5-5': 'gpt-5.5',
  'gpt-5.4': 'gpt-5.4',
  'gpt-5-4': 'gpt-5.4',
  'gpt-5.4-mini': 'gpt-5.4-mini',
  'gpt-5-4-mini': 'gpt-5.4-mini',
  'glm-5.2': 'glm-5.2',
  'glm-5-2': 'glm-5.2',
  'kimi-k2.7-code': 'kimi-k2.7-code',
  'kimi-k2-7-code': 'kimi-k2.7-code',
  'kimi-k3': 'kimi-k3',
  'kimi-3': 'kimi-k3',
  'gemini-3.1-pro': 'gemini-3.1-pro',
  'gemini-3-1-pro': 'gemini-3.1-pro',
  'gemini-3-flash': 'gemini-3-flash',
  'gemini-3.5-flash': 'gemini-3.5-flash',
  'gemini-3-5-flash': 'gemini-3.5-flash',
  'gemini-3.6-flash': 'gemini-3.6-flash',
  'gemini-3-6-flash': 'gemini-3.6-flash',
  'grok-4.5': 'grok-4.5',
  'grok-4-5': 'grok-4.5',
  'muse-spark-1.1': 'muse-spark-1.1',
  'muse-spark-1-1': 'muse-spark-1.1',
}

/**
 * Artificial Analysis 图像 arena slug -> CCX canonicalModel 映射。
 * CCX 目前仅 agnes 图像模型在 upstreamCapabilities，其余 AA 图像 slug 暂不映射；
 * 首跑 unmapped 日志会列出全部未命中 slug，便于后续扩展。
 */
export const ARTIFICIAL_ANALYSIS_IMAGE_MODEL_MAP = {
  'agnes-image-2.0-flash': 'agnes-image-2.0-flash',
  'agnes-image-2-0-flash': 'agnes-image-2.0-flash',
  'agnes-image-2.1-flash': 'agnes-image-2.1-flash',
  'agnes-image-2-1-flash': 'agnes-image-2.1-flash',
}

/**
 * 将 AA LLM slug 转换为 CCX canonicalModel
 * @param {string} slug
 * @returns {string|null}
 */
export function artificialAnalysisToCanonical(slug) {
  return ARTIFICIAL_ANALYSIS_MODEL_MAP[slug] || null
}

function authHeaders(apiKey) {
  return { ...FETCH_HEADERS, 'x-api-key': apiKey }
}

function today() {
  return new Date().toISOString().split('T')[0]
}

// AA intelligence_index v4.1 由 9 项 evaluation 复合而成（见 AA 方法文档）。
// composite index 无任务级 raw data，taskCount 用 evaluation 数作代理，满足
// ModelBenchmarkEvidence.taskCount>0 的 schema 约束与 presetstore 校验。
const INTELLIGENCE_INDEX_EVALUATION_COUNT = 9

/**
 * 分页拉取 /language/models/free，合并所有页的 data[]。
 * 每页独立走 cachedFetch（304 复用缓存页）。
 * @returns {Promise<{tier:string, version:number|null, models:Array, pages:number}>}
 */
export async function fetchLanguageModelsFree(apiKey) {
  const headers = authHeaders(apiKey)
  const allModels = []
  let tier = 'free'
  let version = null
  let page = 1
  let pages = 0

  // 上限保护：AA free 通常一两页；超过 50 页视为异常
  while (page <= 50) {
    const url = `${LANGUAGE_MODELS_FREE_URL}?page=${page}`
    console.log(`[artificial-analysis] Fetching ${url}`)
    const result = await cachedFetch(url, { headers })

    let doc
    if (result.cached) {
      console.log(`[artificial-analysis] ${url} → 304 Not Modified, using cached data`)
      doc = result.data
    } else {
      if (!result.response.ok) {
        const body = await result.response.text().catch(() => '')
        throw new Error(`HTTP ${result.response.status} ${result.response.statusText} for ${url}${body ? `: ${body.slice(0, 200)}` : ''}`)
      }
      doc = await result.response.json()
      cacheResponseData(url, doc)
    }

    tier = doc.tier || tier
    version = typeof doc.intelligence_index_version === 'number' ? doc.intelligence_index_version : version
    const data = Array.isArray(doc.data) ? doc.data : []
    allModels.push(...data)
    pages += 1

    const hasMore = doc.pagination?.has_more === true
    if (!hasMore) break
    page += 1
  }

  return { tier, version, models: allModels, pages }
}

/**
 * 从 intelligence_index 队列计算某分数的 cohort 百分位。
 * @param {number} score
 * @param {number[]} allScores 同批次全部模型的 intelligence_index（已剔除 null/非有限数）
 */
function cohortPercentile(score, allScores) {
  if (!Number.isFinite(score) || allScores.length === 0) return 0
  const atOrBelow = allScores.filter(s => s <= score).length
  return atOrBelow / allScores.length
}

/**
 * 从 free 响应的 data[] 提取每个已映射模型的 benchmarkEvidence。
 * @param {Array} models 合并后的 data[]
 * @param {Object} modelMap AA slug -> CCX canonicalModel
 * @param {number|null} version intelligence_index_version（如 4.1）
 * @returns {{profiles:Object, unmappedSlugs:string[]}}
 *   profiles: {canonical: {benchmarkEvidence:[...], aaMeta:{slug, tier}}}
 */
export function extractLlmProfiles(models, modelMap, version) {
  const profiles = {}
  const unmappedSlugs = []

  // 先收齐所有 intelligence_index 用于 cohort 计算
  const allScores = models
    .map(m => Number(m.evaluations?.artificial_analysis_intelligence_index))
    .filter(s => Number.isFinite(s))
  const cohortSize = allScores.length
  const capturedAt = today()
  const benchmarkVersion = version != null ? `v${version}` : null

  for (const item of models) {
    const slug = item.slug
    const canonical = modelMap[slug]
    if (!canonical) {
      if (slug) unmappedSlugs.push(slug)
      continue
    }

    const ev = item.evaluations || {}
    const indices = [
      ['overall', 'intelligence_index', 'artificial_analysis_intelligence_index'],
      ['coding', 'coding_index', 'artificial_analysis_coding_index'],
      ['agentic', 'agentic_index', 'artificial_analysis_agentic_index'],
    ]

    const evidence = []
    for (const [domain, metric, key] of indices) {
      const raw = Number(ev[key])
      if (!Number.isFinite(raw)) continue
      evidence.push({
        benchmark: 'artificial_analysis',
        benchmarkVersion,
        sourceModel: slug,
        domain,
        metric,
        rawValue: raw,
        uncertainty: 0, // 复合指数无 CI
        cohortPercentile: cohortPercentile(raw, allScores),
        cohortSize,
        taskCount: INTELLIGENCE_INDEX_EVALUATION_COUNT,
        effort: 'default',
        selectionBasis: 'composite_index',
        sourceUrl: `https://artificialanalysis.ai/models/${slug}`,
        capturedAt,
      })
    }

    if (evidence.length === 0) continue

    if (!profiles[canonical]) {
      profiles[canonical] = { benchmarkEvidence: [], aaMeta: { slug } }
    }
    profiles[canonical].benchmarkEvidence.push(...evidence)
  }

  return { profiles, unmappedSlugs }
}

/**
 * 拉取文本生成图像 arena（Free tier，非分页）。
 * @returns {Promise<{tier:string, models:Array}>}
 */
export async function fetchTextToImageArenaFree(apiKey) {
  const headers = authHeaders(apiKey)
  console.log(`[artificial-analysis] Fetching ${TEXT_TO_IMAGE_FREE_URL}`)
  const result = await cachedFetch(TEXT_TO_IMAGE_FREE_URL, { headers })

  let doc
  if (result.cached) {
    console.log(`[artificial-analysis] ${TEXT_TO_IMAGE_FREE_URL} → 304 Not Modified, using cached data`)
    doc = result.data
  } else {
    if (!result.response.ok) {
      const body = await result.response.text().catch(() => '')
      throw new Error(`HTTP ${result.response.status} ${result.response.statusText} for ${TEXT_TO_IMAGE_FREE_URL}${body ? `: ${body.slice(0, 200)}` : ''}`)
    }
    doc = await result.response.json()
    cacheResponseData(TEXT_TO_IMAGE_FREE_URL, doc)
  }

  return { tier: doc.tier || 'free', models: Array.isArray(doc.data) ? doc.data : [] }
}

/**
 * 从图像 arena 响应提取每个已映射模型的 Elo profile。
 * @param {Array} models doc.data[]
 * @param {Object} imageMap AA image slug -> CCX canonicalModel
 * @returns {{profiles:Object, unmappedSlugs:string[]}}
 *   profiles: {canonical: {canonicalModel, elo, ci95, sources}}
 */
export function extractImageArenaProfiles(models, imageMap) {
  const profiles = {}
  const unmappedSlugs = []
  for (const item of models) {
    const slug = item.slug
    const canonical = imageMap[slug]
    if (!canonical) {
      if (slug) unmappedSlugs.push(slug)
      continue
    }
    const elo = Number(item.elo)
    if (!Number.isFinite(elo)) continue
    profiles[canonical] = {
      canonicalModel: canonical,
      elo,
      ci95: Number.isFinite(item.ci_95) ? item.ci_95 : null,
      sources: ['https://artificialanalysis.ai/leaderboards/image-generation'],
    }
  }
  return { profiles, unmappedSlugs }
}

/**
 * 主函数：抓取 LLM + 图像 arena 数据。
 * @param {string} apiKey
 * @param {Object} modelMap AA LLM slug -> canonical
 * @param {Object} imageMap AA image slug -> canonical
 * @returns {Promise<{llm:Object, imageArena:Object, tier:string, version:number|null, unmappedLlmSlugs:string[], unmappedImageSlugs:string[]}>}
 */
export async function fetchArtificialAnalysisData(apiKey, modelMap, imageMap) {
  const { tier, version, models: llmModels, pages } = await fetchLanguageModelsFree(apiKey)
  const { profiles: llm, unmappedSlugs: unmappedLlmSlugs } = extractLlmProfiles(llmModels, modelMap, version)
  console.log(`[artificial-analysis] Extracted LLM data for ${Object.keys(llm).length} models across ${pages} page(s)`)

  const { models: imageModels } = await fetchTextToImageArenaFree(apiKey)
  const { profiles: imageArena, unmappedSlugs: unmappedImageSlugs } = extractImageArenaProfiles(imageModels, imageMap)
  console.log(`[artificial-analysis] Extracted image arena data for ${Object.keys(imageArena).length} models`)

  return {
    llm,
    imageArena,
    tier,
    version,
    unmappedLlmSlugs,
    unmappedImageSlugs,
  }
}
