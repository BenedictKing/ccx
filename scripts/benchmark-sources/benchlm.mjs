/**
 * benchlm.ai 数据抓取器（官方 JSON 数据源）
 *
 * 从 https://benchlm.ai/data/models.json 抓取模型权威数据。单个 JSON 文件包含全部
 * 模型的顶层 displayScore（总体分）、scores.displayCategoryScores（分类分）和 coverage
 * （可信 benchmark 数），取代原先抓取 35 个 /compare 对比页 HTML 的方式。
 *
 * 相比对比页抓取的优势：
 * - 覆盖完整：一次拉取即覆盖全部已映射模型，不再受限于硬编码对比对
 * - 语义更准：categoryScores 为模型独立权威分类分，而非"两模型共享 benchmark 集合上的均值"
 * - 变更检测：ETag 条件请求（304 快速路径）+ generatedAt 时间戳双层检测
 *
 * 数据结构（每个 canonicalModel）：
 * - overallScore: 顶层 displayScore（= leaderboard score）
 * - categoryScores: {ccxCategory: score}（经 BENCHLM_CATEGORY_MAP 映射，跳过 null）
 * - counts: {sharedBenchmarkCount, comparableCategoryCount, totalCategoryCount}
 * - sources: [模型页 URL, methodology URL]
 *
 * 未变更时返回上次提取的 profiles（供图表保留 BenchLM 行），主脚本据此跳过 merge。
 */

import { fetchWithTimeout } from './http.mjs'
import {
  CACHE_PATH,
  cachedFetch,
  cacheAge,
  getSimpleCache,
  hasCacheEntry,
  setSimpleCache,
} from './http-cache.mjs'
import { detectNewModelCandidates } from './mapper.mjs'

const BASE_URL = 'https://benchlm.ai'
const MODELS_URL = `${BASE_URL}/data/models.json`

/** benchlm 共有 8 个分类 */
const TOTAL_CATEGORIES = 8

const GENERATED_AT_KEY = 'benchlm:modelsGeneratedAt'
const EXTRACTED_PROFILES_KEY = 'benchlm:extractedProfiles'
const RAW_DOC_KEY = 'benchlm:rawModelsDoc'
const RAW_DOC_REFRESH_INTERVAL_MS = 24 * 60 * 60 * 1000

const FETCH_HEADERS = {
  'User-Agent': 'ccx-benchmark-updater/1.0',
  Accept: 'application/json',
}

/**
 * 生成 methodology URL
 * @returns {string}
 */
export function methodologyUrl() {
  return `${BASE_URL}/methodology`
}

/**
 * 对未命中映射的 slug 做新模型检测并告警（同家族、版本高于已映射最大版本）。
 * 提示维护者补 slug 映射或注册新模型，避免新模型分数被静默丢弃。
 */
function warnNewModelSlugs(unmappedSlugs, modelMap) {
  if (!Array.isArray(unmappedSlugs) || unmappedSlugs.length === 0) return
  const candidates = detectNewModelCandidates(unmappedSlugs, modelMap)
  for (const c of candidates) {
    console.warn(
      `[benchlm] [NEW-MODEL] slug "${c.name}" (family ${c.family}, v${c.version}) ` +
      `exceeds mapped ${c.mappedBest} but is NOT in BENCHLM_MODEL_MAP; ` +
      `its scores are dropped — add the mapping (or register the model) to include it.`,
    )
  }
}

function describeCacheAge(ageMs) {
  return Number.isFinite(ageMs) ? String(ageMs) : 'missing'
}

function benchlmCacheSummary() {
  const etagKey = `etag:${MODELS_URL}`
  const keys = [
    etagKey,
    `simple:${GENERATED_AT_KEY}`,
    `simple:${EXTRACTED_PROFILES_KEY}`,
    `simple:${RAW_DOC_KEY}`,
  ]
  return keys.map(key => {
    const present = hasCacheEntry(key)
    const ageMs = describeCacheAge(cacheAge(key))
    return `${key}=${present ? 'present' : 'missing'}@${ageMs}`
  }).join(', ')
}

/**
 * 从 models.json 文档提取每个已映射模型的 benchlm 数据。
 * @param {Object} modelsDoc - models.json 解析结果
 * @param {Object} modelMap - benchlm slug -> CCX canonicalModel
 * @param {Object} categoryMap - benchlm 分类名 -> CCX 分类名
 * @param {string[]} [unmappedSlugs] - 可选出参：收集未命中映射的 slug（供新模型检测）
 * @returns {Object} - {canonicalModel: {overallScore, categoryScores, counts, sources}}
 */
export function extractProfiles(modelsDoc, modelMap, categoryMap, unmappedSlugs) {
  const result = {}

  for (const item of modelsDoc.items || []) {
    const canonical = modelMap[item.slug]
    if (!canonical) {
      if (unmappedSlugs && item.slug) unmappedSlugs.push(item.slug)
      continue
    }

    // overallScore 取顶层 displayScore（= leaderboard 公开分）。
    // 不用 scores.displayScore：那是 verified lane，对 estimated 模型（如 kimi）为 0。
    // displayScore 为 0/null 视为无总体分，置 null 不覆盖已有值。
    const rawScore = item.displayScore
    const overallScore =
      typeof rawScore === 'number' && rawScore > 0 ? rawScore : null

    const categoryScores = {}
    let comparableCategoryCount = 0
    const dcs = item.scores?.displayCategoryScores || {}
    for (const [benchlmCat, value] of Object.entries(dcs)) {
      const ccxCat = categoryMap[benchlmCat]
      if (!ccxCat || value === null || value === undefined) continue
      categoryScores[ccxCat] = value
      comparableCategoryCount += 1
    }

    const trusted = Number(item.coverage?.trustedBenchmarkCount) || 0
    const counts = {
      sharedBenchmarkCount: trusted,
      comparableCategoryCount,
      totalCategoryCount: TOTAL_CATEGORIES,
    }

    const existing = result[canonical]
    if (existing) {
      // 同一 canonical 对应多个 slug 时（如 claude-fable / claude-fable-5），合并取优
      for (const [cat, val] of Object.entries(categoryScores)) {
        if (existing.categoryScores[cat] == null || val > existing.categoryScores[cat]) {
          existing.categoryScores[cat] = val
        }
      }
      existing.counts.sharedBenchmarkCount = Math.max(
        existing.counts.sharedBenchmarkCount,
        counts.sharedBenchmarkCount,
      )
      existing.counts.comparableCategoryCount = Math.max(
        existing.counts.comparableCategoryCount,
        comparableCategoryCount,
      )
      if (overallScore != null && (existing.overallScore == null || overallScore > existing.overallScore)) {
        existing.overallScore = overallScore
      }
      if (item.url && !existing.sources.includes(item.url)) {
        existing.sources.push(item.url)
      }
    } else {
      const sources = []
      if (item.url) sources.push(item.url)
      sources.push(methodologyUrl())
      result[canonical] = {
        overallScore,
        categoryScores,
        counts,
        sources,
      }
    }
  }

  return result
}

/**
 * 拉取并解析 models.json（无条件请求，用于 304 缺缓存 profiles 的回退）
 * @returns {Promise<{doc: Object, generatedAt: string|null}>}
 */
async function fetchModelsDoc() {
  const resp = await fetchWithTimeout(MODELS_URL, { headers: FETCH_HEADERS })
  if (!resp.ok) {
    throw new Error(`HTTP ${resp.status} ${resp.statusText} for ${MODELS_URL}`)
  }
  const doc = JSON.parse(await resp.text())
  return { doc, generatedAt: doc.generatedAt || null }
}

/**
 * 主函数：抓取 benchlm.ai 数据
 * @param {Object} modelMap - benchlm slug -> CCX canonicalModel 映射
 * @param {Object} categoryMap - benchlm 分类名 -> CCX 分类名映射
 * @returns {Promise<{data: Object, unchanged: string[]}>}
 *   - data: {canonicalModel: {overallScore, categoryScores, counts, sources}}，未变更时为缓存 profiles
 *   - unchanged: 非空表示未变更（主脚本据此跳过 merge，但仍用 data 喂图表）
 */
export async function fetchBenchlmData(modelMap, categoryMap) {
  // 第一层：ETag 条件请求
  const { status, response } = await cachedFetch(MODELS_URL, { headers: FETCH_HEADERS })

  if (status === 304) {
    const rawDoc = getSimpleCache(RAW_DOC_KEY)
    const rawDocAge = cacheAge(`simple:${RAW_DOC_KEY}`)
    if (rawDoc && rawDocAge <= RAW_DOC_REFRESH_INTERVAL_MS) {
      console.log(
        `[benchlm] ${MODELS_URL} → 304 Not Modified, rebuilding profiles from cached raw doc `
        + `(cache=${CACHE_PATH}, key=${RAW_DOC_KEY}, ageMs=${rawDocAge}; keys=${benchlmCacheSummary()})`,
      )
      const unmapped = []
      const profiles = extractProfiles(rawDoc, modelMap, categoryMap, unmapped)
      warnNewModelSlugs(unmapped, modelMap)
      setSimpleCache(EXTRACTED_PROFILES_KEY, profiles)
      return {
        data: { ...profiles },
        unchanged: ['models.json unchanged (304 Not Modified)'],
      }
    }
    // 原始文档缺失或超过 24h：主动全量拉取一次，避免长期被旧 extractedProfiles 锁死
    console.log(
      `[benchlm] 304 Not Modified but raw doc missing/stale, refetching full models.json `
      + `(cache=${CACHE_PATH}, key=${RAW_DOC_KEY}, ageMs=${Number.isFinite(rawDocAge) ? rawDocAge : 'missing'}, maxAgeMs=${RAW_DOC_REFRESH_INTERVAL_MS}; keys=${benchlmCacheSummary()})`,
    )
    const { doc, generatedAt } = await fetchModelsDoc()
    return processFresh(doc, generatedAt, modelMap, categoryMap, true)
  }

  if (!response.ok) {
    throw new Error(`HTTP ${response.status} ${response.statusText} for ${MODELS_URL}`)
  }

  const doc = JSON.parse(await response.text())
  const generatedAt = doc.generatedAt || null
  console.log(`[benchlm] Fetched ${MODELS_URL} (generatedAt: ${generatedAt || 'unknown'})`)

  // 第二层：generatedAt 时间戳比对（应对 ETag 每次重建但数据实际未变）
  const cachedGeneratedAt = getSimpleCache(GENERATED_AT_KEY)
  if (cachedGeneratedAt && generatedAt && cachedGeneratedAt === generatedAt) {
    const extracted = getSimpleCache(EXTRACTED_PROFILES_KEY)
    if (extracted && Object.keys(extracted).length > 0) {
      console.log(`[benchlm] generatedAt unchanged (${generatedAt}), using cached profiles`)
      return {
        data: { ...extracted },
        unchanged: [`models.json unchanged (generatedAt ${generatedAt})`],
      }
    }
    // 缓存 profiles 丢失，按新数据处理并重建缓存
  }

  return processFresh(doc, generatedAt, modelMap, categoryMap, false)
}

/**
 * 处理新拉取的 models.json：提取 profiles、更新缓存、返回结果
 * @param {boolean} from304Refetch - 是否为 304 回退拉取（影响日志）
 */
function processFresh(doc, generatedAt, modelMap, categoryMap, from304Refetch) {
  const unmapped = []
  const profiles = extractProfiles(doc, modelMap, categoryMap, unmapped)
  warnNewModelSlugs(unmapped, modelMap)
  setSimpleCache(GENERATED_AT_KEY, generatedAt)
  setSimpleCache(EXTRACTED_PROFILES_KEY, profiles)
  setSimpleCache(RAW_DOC_KEY, doc)

  const label = from304Refetch ? 'refetched' : 'extracted'
  const models = Object.keys(profiles).sort()
  console.log(`[benchlm] ${label} data for ${models.length} models: ${models.join(', ') || '(none)'}`)
  return { data: profiles, unchanged: [] }
}
