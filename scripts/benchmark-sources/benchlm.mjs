/**
 * benchlm.ai 数据抓取器
 *
 * 从 https://benchlm.ai 抓取模型对比数据。
 *
 * 数据来源：
 * - 对比页面: /compare/{modelA}-vs-{modelB}
 * - 数据嵌入在 __NEXT_DATA__ 中 (Next.js SSR)
 *
 * 数据结构：
 * - pageData: {modelA, modelB, scoreA, scoreB, diffRows, counts, scoreEvidenceA, scoreEvidenceB}
 * - diffRows: [{key, name, a, b}] - 各分类分数
 * - counts: {sharedBenchmarkCount, comparableCategoryCount, totalCategoryCount}
 */

import { fetchWithTimeout } from './http.mjs'
import {
  getCacheEntry,
  setCacheEntry,
  contentHash,
  cacheContentData,
} from './http-cache.mjs'

const BASE_URL = 'https://benchlm.ai'

/**
 * 获取对比页面数据
 * @param {string} modelASlug
 * @param {string} modelBSlug
 * @returns {Promise<Object>}
 */
export async function fetchComparison(modelASlug, modelBSlug) {
  const url = `${BASE_URL}/compare/${modelASlug}-vs-${modelBSlug}`

  console.log(`[benchlm] Fetching ${url}`)

  const resp = await fetchWithTimeout(url, {
    headers: {
      'User-Agent': 'ccx-benchmark-updater/1.0',
      Accept: 'text/html',
    },
  })

  if (!resp.ok) {
    throw new Error(`HTTP ${resp.status} ${resp.statusText} for ${url}`)
  }

  const html = await resp.text()
  const match = html.match(/<script id="__NEXT_DATA__" type="application\/json">(.*?)<\/script>/s)

  if (!match) {
    throw new Error(`__NEXT_DATA__ not found in ${url}`)
  }

  const data = JSON.parse(match[1])
  return data.props.pageProps.pageData
}

/**
 * 带缓存的对比页面获取：
 * - 提取 __NEXT_DATA__ 后计算内容哈希
 * - 如果哈希与上次相同，返回 { unchanged: true } 跳过处理
 * - 如果哈希不同，返回 pageData 并更新缓存
 *
 * @param {string} modelASlug
 * @param {string} modelBSlug
 * @returns {Promise<{unchanged: boolean, pageData?: Object}>}
 */
export async function fetchComparisonCached(modelASlug, modelBSlug) {
  const url = `${BASE_URL}/compare/${modelASlug}-vs-${modelBSlug}`
  const cacheKey = `benchlm:${modelASlug}-vs-${modelBSlug}`

  const resp = await fetchWithTimeout(url, {
    headers: {
      'User-Agent': 'ccx-benchmark-updater/1.0',
      Accept: 'text/html',
    },
  })

  if (!resp.ok) {
    throw new Error(`HTTP ${resp.status} ${resp.statusText} for ${url}`)
  }

  const html = await resp.text()
  const match = html.match(/<script id="__NEXT_DATA__" type="application\/json">(.*?)<\/script>/s)

  if (!match) {
    throw new Error(`__NEXT_DATA__ not found in ${url}`)
  }

  // 对提取的 JSON 数据做哈希，而非整个 HTML（HTML 含时间戳等动态内容）
  const dataHash = contentHash(match[1])
  const cached = getCacheEntry(cacheKey)

  if (cached?.dataHash === dataHash) {
    return { unchanged: true }
  }

  // 内容变更，更新缓存
  setCacheEntry(cacheKey, { dataHash })
  const data = JSON.parse(match[1])
  return { unchanged: false, pageData: data.props.pageProps.pageData }
}

/**
 * 从对比数据中提取模型分数
 * @param {Object} pageData - fetchComparison 的输出
 * @param {Object} modelMap - benchlm slug -> CCX canonicalModel 映射
 * @param {Object} categoryMap - benchlm 分类名 -> CCX 分类名映射
 * @returns {Object} - {canonicalModel, overallScore, categoryScores, counts, scoreEvidence}
 */
export function extractModelScore(pageData, modelMap, categoryMap) {
  const modelASlug = pageData.modelA.slug
  const modelBSlug = pageData.modelB.slug

  const canonicalA = modelMap[modelASlug]
  const canonicalB = modelMap[modelBSlug]

  const results = {}

  // 提取 modelA 数据
  if (canonicalA) {
    const categoryScores = {}
    for (const row of pageData.diffRows || []) {
      const ccxCategory = categoryMap[row.key]
      if (ccxCategory && row.a !== null && row.a !== undefined) {
        categoryScores[ccxCategory] = row.a
      }
    }

    results[canonicalA] = {
      canonicalModel: canonicalA,
      overallScore: pageData.scoreA,
      categoryScores,
      counts: pageData.counts,
      scoreEvidence: pageData.scoreEvidenceA,
      sourceUrl: `${BASE_URL}/compare/${modelASlug}-vs-${modelBSlug}`,
    }
  }

  // 提取 modelB 数据
  if (canonicalB) {
    const categoryScores = {}
    for (const row of pageData.diffRows || []) {
      const ccxCategory = categoryMap[row.key]
      if (ccxCategory && row.b !== null && row.b !== undefined) {
        categoryScores[ccxCategory] = row.b
      }
    }

    results[canonicalB] = {
      canonicalModel: canonicalB,
      overallScore: pageData.scoreB,
      categoryScores,
      counts: pageData.counts,
      scoreEvidence: pageData.scoreEvidenceB,
      sourceUrl: `${BASE_URL}/compare/${modelASlug}-vs-${modelBSlug}`,
    }
  }

  return results
}

/**
 * 生成 comparison URL
 * @param {string} slugA
 * @param {string} slugB
 * @returns {string}
 */
export function comparisonUrl(slugA, slugB) {
  return `${BASE_URL}/compare/${slugA}-vs-${slugB}`
}

/**
 * 生成 methodology URL
 * @returns {string}
 */
export function methodologyUrl() {
  return `${BASE_URL}/methodology`
}

function mergeComparisonCounts(current = {}, candidate = {}) {
  const merged = { ...current, ...candidate }
  for (const field of [
    'sharedBenchmarkCount',
    'sharedCategoryCount',
    'aOnlyCount',
    'bOnlyCount',
    'comparableCategoryCount',
    'totalCategoryCount',
  ]) {
    merged[field] = Math.max(
      Number.isFinite(current[field]) ? current[field] : 0,
      Number.isFinite(candidate[field]) ? candidate[field] : 0,
    )
  }
  merged.isCoverageLimited = Boolean(current.isCoverageLimited || candidate.isCoverageLimited)
  return merged
}

/**
 * 预定义的对比组合
 * 根据 CCX 注册表中的 benchmarkProfiles，选择有意义的对比
 * 目标：确保每个模型至少出现在一个对比中，优先覆盖注册表已有模型
 */
export const COMPARISON_PAIRS = [
  // Claude Opus 4.8 vs 所有其他模型
  ['claude-opus-4-8', 'gpt-5-6-terra'],
  ['claude-opus-4-8', 'gpt-5-6-sol'],
  ['claude-opus-4-8', 'gpt-5-6-luna'],
  ['claude-opus-4-8', 'gpt-5-5'],
  ['claude-opus-4-8', 'claude-fable-5'],
  ['claude-opus-4-8', 'claude-sonnet-5'],
  ['claude-opus-4-8', 'claude-sonnet-4-6'],
  ['claude-opus-4-8', 'glm-5-2'],
  ['claude-opus-4-8', 'kimi-k2-7-code'],
  ['claude-opus-4-8', 'gpt-5-4'],
  // GPT-5.6 系列内部对比
  ['gpt-5-6-terra', 'gpt-5-6-sol'],
  ['gpt-5-6-terra', 'gpt-5-6-luna'],
  ['gpt-5-6-sol', 'gpt-5-6-luna'],
  ['gpt-5-6-terra', 'gpt-5-5'],
  ['gpt-5-6-sol', 'gpt-5-5'],
  ['gpt-5-6-terra', 'gpt-5-4'],
  ['gpt-5-6-sol', 'gpt-5-4'],
  ['gpt-5-6-luna', 'gpt-5-4'],
  // Claude 系列内部对比
  ['claude-fable-5', 'claude-sonnet-5'],
  ['claude-sonnet-5', 'claude-sonnet-4-6'],
  ['claude-fable-5', 'claude-sonnet-4-6'],
  ['claude-fable-5', 'gpt-5-5'],
  ['claude-fable-5', 'gpt-5-4'],
  ['claude-fable-5', 'gpt-5-6-terra'],
  ['claude-fable-5', 'gpt-5-6-sol'],
  ['claude-fable-5', 'gpt-5-6-luna'],
  // 其他模型对比
  ['gpt-5-5', 'gpt-5-4'],
  ['glm-5-2', 'kimi-k2-7-code'],
  ['glm-5-2', 'gpt-5-4'],
  ['kimi-k2-7-code', 'gpt-5-4'],
  ['glm-5-2', 'gpt-5-5'],
  ['kimi-k2-7-code', 'gpt-5-5'],
  ['gemini-3-5-flash', 'claude-haiku-4-5'],
  ['gemini-3-5-flash', 'gpt-5-4'],
  ['claude-haiku-4-5', 'gpt-5-4'],
]

/**
 * 主函数：抓取 benchlm.ai 数据
 * @param {Object} modelMap - benchlm slug -> CCX canonicalModel 映射
 * @param {Object} categoryMap - benchlm 分类名 -> CCX 分类名映射
 * @returns {Promise<Object>} - {canonicalModel: {overallScore, categoryScores, counts, scoreEvidence, sources}}
 */
export async function fetchBenchlmData(modelMap, categoryMap) {
  const result = {}
  const errors = []
  const unchanged = []

  // 去重对比对（避免重复抓取）
  const uniquePairs = []
  const seen = new Set()
  for (const [a, b] of COMPARISON_PAIRS) {
    const key = [a, b].sort().join('-vs-')
    if (!seen.has(key)) {
      seen.add(key)
      uniquePairs.push([a, b])
    }
  }

  // 金丝雀检测：先拉第一个对比页，若未变更则跳过其余
  if (uniquePairs.length > 1) {
    const [canaryA, canaryB] = uniquePairs[0]
    console.log(`[benchlm] Canary check: ${canaryA}-vs-${canaryB}`)
    try {
      const canaryResult = await fetchComparisonCached(canaryA, canaryB)
      if (canaryResult.unchanged) {
        console.log(`[benchlm] Canary unchanged, skipping remaining ${uniquePairs.length - 1} comparisons`)
        unchanged.push(...uniquePairs.map(([a, b]) => `${a}-vs-${b}`))
        // 全部未变更，直接返回空结果
        console.log(`[benchlm] All ${uniquePairs.length} comparisons unchanged, no data to update`)
        return { data: result, unchanged }
      }
      // 金丝雀变更了，处理它的数据
      processPageData(canaryResult.pageData, canaryA, canaryB, modelMap, categoryMap, result)
    } catch (err) {
      errors.push({ pair: `${canaryA}-vs-${canaryB}`, error: err.message })
      console.warn(`[benchlm] Canary failed: ${canaryA}-vs-${canaryB}:`, err.message)
    }
  }

  // 并行拉取剩余对比页（每批最多 6 个并发，避免压垮服务器）
  const remaining = uniquePairs.length > 1 ? uniquePairs.slice(1) : uniquePairs
  const BATCH_SIZE = 6

  for (let i = 0; i < remaining.length; i += BATCH_SIZE) {
    const batch = remaining.slice(i, i + BATCH_SIZE)
    const batchResults = await Promise.allSettled(
      batch.map(async ([slugA, slugB]) => {
        const pairLabel = `${slugA}-vs-${slugB}`
        try {
          const cached = await fetchComparisonCached(slugA, slugB)
          if (cached.unchanged) {
            unchanged.push(pairLabel)
            return null
          }
          processPageData(cached.pageData, slugA, slugB, modelMap, categoryMap, result)
          return pairLabel
        } catch (err) {
          errors.push({ pair: pairLabel, error: err.message })
          console.warn(`[benchlm] Failed to fetch ${pairLabel}:`, err.message)
          return null
        }
      })
    )

    const changed = batchResults.filter(r => r.status === 'fulfilled' && r.value !== null)
    if (changed.length > 0) {
      console.log(`[benchlm] Batch ${Math.floor(i / BATCH_SIZE) + 1}: ${changed.length} changed, ${batch.length - changed.length} unchanged`)
    }
  }

  // 添加 methodology URL 到所有结果
  const methUrl = methodologyUrl()
  for (const canonical of Object.keys(result)) {
    if (!result[canonical].sources.includes(methUrl)) {
      result[canonical].sources.push(methUrl)
    }
  }

  const changedCount = uniquePairs.length - unchanged.length - errors.length
  console.log(`[benchlm] Extracted data for ${Object.keys(result).length} models (${changedCount} changed, ${unchanged.length} unchanged, ${errors.length} failed)`)

  if (Object.keys(result).length === 0 && errors.length > 0) {
    const detail = errors[0]?.error || 'no mapped benchmark results'
    throw new Error(`all ${uniquePairs.length} comparisons failed: ${detail}`)
  }

  // 返回变更统计，供上游报告
  result._unchanged = unchanged
  return { data: result, unchanged }
}

/**
 * 处理单个对比页的数据，合并到 result 中
 */
function processPageData(pageData, slugA, slugB, modelMap, categoryMap, result) {
  const scores = extractModelScore(pageData, modelMap, categoryMap)

  for (const [canonical, data] of Object.entries(scores)) {
    if (!result[canonical]) {
      result[canonical] = {
        overallScore: data.overallScore,
        categoryScores: {},
        counts: data.counts,
        scoreEvidence: data.scoreEvidence,
        sources: [],
      }
    } else {
      result[canonical].counts = mergeComparisonCounts(result[canonical].counts, data.counts)
    }

    // 合并 categoryScores (取最大值，因为不同对比可能覆盖不同分类)
    for (const [cat, score] of Object.entries(data.categoryScores)) {
      if (!result[canonical].categoryScores[cat] || score > result[canonical].categoryScores[cat]) {
        result[canonical].categoryScores[cat] = score
      }
    }

    // 添加 source URL
    const sourceUrl = `${BASE_URL}/compare/${slugA}-vs-${slugB}`
    if (!result[canonical].sources.includes(sourceUrl)) {
      result[canonical].sources.push(sourceUrl)
    }
  }
}
