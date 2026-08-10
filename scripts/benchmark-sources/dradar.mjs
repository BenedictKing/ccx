/**
 * dradar (codexradar) 数据抓取器
 *
 * 从 https://api.codexradar.com 抓取 leaderboard 数据。
 *
 * 数据来源：
 * - /api/v1/leaderboard: 各模型+effort 的 pass_rate、graded 数
 * - /api/v1/table: 各 task+model+effort 的 cell 数据（含 cost）
 *
 * 数据结构：
 * - leaderboard: {models: [{model, effort, pass_rate, graded, passed, cells, cells_passed, tasks}]}
 * - table: {cells: {"task|model|effort": {rate, n, p, ran_by: [{actual_cost_usd, duration_sec, ...}]}}}
 */

/**
 * dradar 模型名 -> CCX canonicalModel 映射
 * dradar 使用点号: "gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna", "gpt-5.5"
 */
import { fetchWithTimeout } from './http.mjs'
import { cachedFetch, cacheResponseData, getSimpleCache, setSimpleCache } from './http-cache.mjs'

export const DRADAR_MODEL_MAP = {
  'gpt-5.6-sol': 'gpt-5.6-sol',
  'gpt-5.6-terra': 'gpt-5.6-terra',
  'gpt-5.6-luna': 'gpt-5.6-luna',
  'gpt-5.5': 'gpt-5.5',
  'gpt-5-4': 'gpt-5.4',
  'gpt-5-4-mini': 'gpt-5.4-mini',
  'claude-opus-4-8': 'claude-opus-4-8',
  'claude-opus-5': 'claude-opus-5',
  'claude-sonnet-5': 'claude-sonnet-5',
  'claude-sonnet-4-6': 'claude-sonnet-4-6',
  'claude-haiku-4-5': 'claude-haiku-4.5',
  'glm-5-2': 'glm-5.2',
  'kimi-k2-7-code': 'kimi-k2.7-code',
  'gemini-3-5-flash': 'gemini-3.5-flash',
  'gemini-3-1-pro': 'gemini-3.1-pro',
  'gemini-3-flash': 'gemini-3-flash',
  'deepseek-v4-flash': 'deepseek-v4-flash',
}

const BASE_URL = 'https://api.codexradar.com'
const SITE_URL = 'https://deng.codexradar.com/'
const TABLE_FETCH_TIMEOUT_MS = 45_000

export function extractTableCacheVersion(html) {
  const match = String(html).match(/TABLE_CACHE_VERSION\s*=\s*["']([^"']+)["']/)
  if (!match?.[1]) throw new Error('TABLE_CACHE_VERSION not found on CodexRadar site')
  return match[1]
}

async function fetchTableCacheVersion() {
  console.log(`[dradar] Fetching ${SITE_URL} for table cache version`)
  const resp = await fetchWithTimeout(SITE_URL, {
    headers: {
      'User-Agent': 'ccx-benchmark-updater/1.0',
      Accept: 'text/html',
    },
  })
  if (!resp.ok) {
    throw new Error(`HTTP ${resp.status} ${resp.statusText} for ${SITE_URL}`)
  }
  return extractTableCacheVersion(await resp.text())
}

/**
 * 获取 dradar leaderboard 数据
 * @returns {Promise<Object>}
 */
export async function fetchLeaderboard() {
  const url = `${BASE_URL}/api/v1/leaderboard`

  console.log(`[dradar] Fetching ${url}`)

  const result = await cachedFetch(url, {
    headers: {
      'User-Agent': 'ccx-benchmark-updater/1.0',
      Accept: 'application/json',
    },
  })

  if (result.cached) {
    console.log(`[dradar] ${url} → 304 Not Modified, using cached data`)
    return result.data
  }

  const data = await result.response.json()
  cacheResponseData(url, data)
  return data
}

/**
 * 获取 dradar table 数据（含 cost）
 * @returns {Promise<Object>}
 */
export async function fetchTable() {
  const cacheVersion = await fetchTableCacheVersion()
  const cachedVersion = getSimpleCache('dradar:cacheVersion')
  const url = `${BASE_URL}/api/v1/table?ui=${encodeURIComponent(cacheVersion)}`

  // 如果 cacheVersion 与上次相同，使用条件请求（ETag）
  if (cachedVersion === cacheVersion) {
    console.log(`[dradar] Cache version unchanged (${cacheVersion}), trying conditional request`)
    const result = await cachedFetch(url, {
      headers: {
        'User-Agent': 'ccx-benchmark-updater/1.0',
        Accept: 'application/json',
      },
    }, TABLE_FETCH_TIMEOUT_MS)

    if (result.cached) {
      console.log(`[dradar] ${url} → 304 Not Modified, using cached data`)
      return result.data
    }

    const data = await result.response.json()
    cacheResponseData(url, data)
    return data
  }

  console.log(`[dradar] Cache version changed: ${cachedVersion || '(none)'} → ${cacheVersion}`)
  setSimpleCache('dradar:cacheVersion', cacheVersion)

  console.log(`[dradar] Fetching ${url}`)

  const resp = await fetchWithTimeout(url, {
    headers: {
      'User-Agent': 'ccx-benchmark-updater/1.0',
      Accept: 'application/json',
    },
  }, TABLE_FETCH_TIMEOUT_MS)

  if (!resp.ok) {
    throw new Error(`HTTP ${resp.status} ${resp.statusText} for ${url}`)
  }

  const data = await resp.json()
  cacheResponseData(url, data)
  return data
}

/**
 * 从 leaderboard 数据中提取每个模型的最佳表现
 *
 * @param {Object} data - leaderboard JSON 数据
 * @param {Object} modelMap - dradar 模型名 -> CCX canonicalModel 映射
 * @returns {Object} - {canonicalModel: {bestEffort, passRate, graded, cells, cellsPassed, efforts}}
 */
export function extractBestPerModel(data, modelMap) {
  const models = data.models || []
  const result = {}

  for (const m of models) {
    const dradarModel = m.model
    const canonical = modelMap[dradarModel]

    if (!canonical) {
      continue
    }

    if (!result[canonical]) {
      result[canonical] = {
        canonicalModel: canonical,
        deepsweModel: dradarModel,
        bestEffort: null,
        passRate: 0,
        graded: 0,
        cells: 0,
        cellsPassed: 0,
        efforts: {},
      }
    }

    result[canonical].efforts[m.effort] = {
      passRate: m.pass_rate,
      graded: m.graded,
      cells: m.cells,
      cellsPassed: m.cells_passed,
    }

    // 更新最佳 effort
    if (m.pass_rate > result[canonical].passRate) {
      result[canonical].bestEffort = m.effort
      result[canonical].passRate = m.pass_rate
      result[canonical].graded = m.graded
      result[canonical].cells = m.cells
      result[canonical].cellsPassed = m.cells_passed
    }
  }

  return result
}

/**
 * 从版本化 table 直接聚合 leaderboard，避免额外请求冷启动的 /leaderboard。
 * table 的每个 cell 代表一个任务投票，严格多数通过才算 cells_passed。
 */
export function extractLeaderboardFromTable(data, modelMap) {
  const aggregates = new Map()
  for (const [key, cell] of Object.entries(data?.cells || {})) {
    const [taskId, dradarModel, effort] = key.split('|')
    const canonical = modelMap[dradarModel]
    const graded = Number(cell?.n)
    const passed = Number(cell?.p)
    if (!taskId || !canonical || !effort || !Number.isFinite(graded) || graded <= 0) continue

    const aggregateKey = `${canonical}|${effort}`
    if (!aggregates.has(aggregateKey)) {
      aggregates.set(aggregateKey, {
        model: dradarModel,
        effort,
        graded: 0,
        passed: 0,
        cells: 0,
        cells_passed: 0,
      })
    }
    const aggregate = aggregates.get(aggregateKey)
    aggregate.graded += graded
    aggregate.passed += Number.isFinite(passed) ? passed : 0
    aggregate.cells += 1
    if (Number.isFinite(passed) && passed * 2 > graded) aggregate.cells_passed += 1
  }

  return {
    models: [...aggregates.values()].map(aggregate => ({
      ...aggregate,
      pass_rate: aggregate.cells > 0 ? aggregate.cells_passed / aggregate.cells : 0,
    })),
  }
}

/**
 * 从 table 数据中提取 cost 信息
 *
 * @param {Object} data - table JSON 数据
 * @param {Object} modelMap - dradar 模型名 -> CCX canonicalModel 映射
 * @returns {Object} - {canonicalModel: {effort: {meanCost, medianCost, nRuns}}}
 */
export function extractCostData(data, modelMap) {
  const cells = data.cells || {}
  const costByModelEffort = {}

  for (const [key, cell] of Object.entries(cells)) {
    const [taskId, model, effort] = key.split('|')
    const canonical = modelMap[model]

    if (!canonical || !cell.ran_by || cell.ran_by.length === 0) {
      continue
    }

    if (!costByModelEffort[canonical]) {
      costByModelEffort[canonical] = {}
    }
    if (!costByModelEffort[canonical][effort]) {
      costByModelEffort[canonical][effort] = {
        costs: [],
        durations: [],
      }
    }

    for (const run of cell.ran_by) {
      if (run.actual_cost_usd !== null && run.actual_cost_usd !== undefined) {
        costByModelEffort[canonical][effort].costs.push(run.actual_cost_usd)
      }
      if (run.duration_sec !== null && run.duration_sec !== undefined) {
        costByModelEffort[canonical][effort].durations.push(run.duration_sec)
      }
    }
  }

  // 计算均值和中位数
  const result = {}
  for (const [canonical, efforts] of Object.entries(costByModelEffort)) {
    result[canonical] = {}
    for (const [effort, data] of Object.entries(efforts)) {
      if (data.costs.length === 0) continue

      const sortedCosts = [...data.costs].sort((a, b) => a - b)
      const sortedDurations = [...data.durations].sort((a, b) => a - b)

      result[canonical][effort] = {
        meanCost: data.costs.reduce((a, b) => a + b, 0) / data.costs.length,
        medianCost: sortedCosts[Math.floor(sortedCosts.length / 2)],
        meanDuration: data.durations.length > 0 ? data.durations.reduce((a, b) => a + b, 0) / data.durations.length : null,
        medianDuration: data.durations.length > 0 ? sortedDurations[Math.floor(sortedDurations.length / 2)] : null,
        nRuns: data.costs.length,
      }
    }
  }

  return result
}

/**
 * 生成 benchmarkEvidence 对象
 * @param {Object} modelData - extractBestPerModel 的输出
 * @param {Array} allModels - 所有模型列表 (用于计算 percentile)
 * @param {Object} costData - extractCostData 的输出 {canonicalModel: {effort: {meanCost, medianCost, nRuns}}}
 * @returns {Object} - benchmarkEvidence 条目
 */
export function toBenchmarkEvidence(modelData, allModels, costData) {
  // 计算 percentile
  const allRates = allModels.map(m => m.passRate).filter(rate => rate > 0)
  const atOrBelow = allRates.filter(rate => rate <= modelData.passRate).length
  const percentile = allRates.length > 0 ? atOrBelow / allRates.length : 0

  const effort = modelData.bestEffort || 'default'
  const canonicalCost = costData?.[modelData.canonicalModel] || costData?.[modelData.deepsweModel]
  const effortCost = canonicalCost?.[effort]

  const evidence = {
    benchmark: 'codexradar',
    benchmarkVersion: 'v1',
    sourceModel: modelData.deepsweModel,
    domain: 'coding',
    metric: 'pass_at_1',
    rawValue: modelData.passRate,
    uncertainty: 0, // dradar 不提供 CI
    cohortPercentile: percentile,
    taskCount: modelData.cells,
    cohortSize: allModels.length,
    effort,
    selectionBasis: 'best_available_effort',
    sourceUrl: 'https://deng.codexradar.com/',
    capturedAt: new Date().toISOString().split('T')[0],
  }

  // 若该模型 x effort 有实测 cost，则以均值注入 evidence，供 frontier 成本轴校准。
  // 舍入到 2 位小数（0.01 美元）：costUsd 进入 registry 受版本管理，
  // 全精度浮点会让上游每次重算 mean 都产生亚美分抖动，制造无意义 diff。
  // 0.01 美元已低于任何实际路由决策关心的成本差异。可视化用的原始 meanCost 不在此舍入。
  if (effortCost?.meanCost !== undefined && effortCost?.meanCost !== null && Number.isFinite(effortCost.meanCost)) {
    evidence.costUsd = Math.round(effortCost.meanCost * 1e2) / 1e2
  }

  return evidence
}

/**
 * 主函数：抓取并转换 dradar 数据
 * @param {Object} modelMap - dradar 模型名 -> CCX canonicalModel 映射
 * @returns {Promise<Object>} - {canonicalModel: {benchmarkEvidence, costData, efforts}}
 */
export async function fetchDradarData(modelMap) {
  try {
    const table = await fetchTable()
    const leaderboard = extractLeaderboardFromTable(table, modelMap)

    const bestPerModel = extractBestPerModel(leaderboard, modelMap)
    const costData = extractCostData(table, modelMap)
    if (Object.keys(costData).length === 0) {
      throw new Error('table response contains no mapped cost data')
    }

    const result = {}

    for (const [canonical, modelData] of Object.entries(bestPerModel)) {
      const evidence = toBenchmarkEvidence(modelData, Object.values(bestPerModel), costData)

      if (!result[canonical]) {
        result[canonical] = {
          benchmarkEvidence: [],
          costData: {},
          efforts: {},
        }
      }

      result[canonical].benchmarkEvidence.push(evidence)
      result[canonical].costData = costData[canonical] || {}
      result[canonical].efforts = modelData.efforts
    }

    console.log(`[dradar] Extracted data for ${Object.keys(result).length} models`)
    return result
  } catch (err) {
    console.error(`[dradar] Failed to fetch data:`, err.message)
    throw err
  }
}
