/**
 * dradar (codexradar) 数据抓取器 —— 雷达Bench v2
 *
 * 2026-10 站点重构（deployment007/server024/config64）后的新事实源：
 * - 旧 /api/v1/table 深榜已退役（HTTP 410 score_endpoint_retired），全模型混榜不复存在；
 * - 评测收敛为「雷达Bench」统一 64 题池：DeepSWE 精选 15 + 庞贝邻接 16 + TB 14 + Science 19；
 * - 权威分端点 /api/v1/radar-bench-score?model&effort，150 分制
 *   （scoring_policy: radar-bench-v2-last3-per-task-equal-weight-150：
 *    每任务取最近至多 3 次有效判分的平均奖励，覆盖任务等权平均后 ×1.5）；
 * - 模型范围收缩为 GPT 系 6 模型 × 5-6 档（score_selection_catalog:
 *   codex-0160-six-model-score-query-20261005-v1）；其他厂商模型已不在榜，
 *   gpt-5.5 明确退役（unsupported_selection）。
 *
 * 数据流：
 * 1. GET /api/v1/library-catalog → 四库 model_effort_selections 的并集 = 待查 (模型, 档位) 清单；
 *    unified_pool.score_url 绑定校验防端点漂移。
 * 2. GET /api/v1/radar-bench-score?model&effort → 每 (模型, 档位) 一份权威 payload：
 *    score(0-150)/coverage(0-64)/score_status、tasks[].mean_reward 与
 *    statistics.metrics.api_equivalent_usd.mean（官方标准 API 等效成本，单 run 均值）。
 *
 * 证据口径：rawValue = score/score_max（0-1 等权平均任务奖励率）。
 * 庞贝任务奖励是连续 F1 部分分，不再是纯二值通过率，故 metric 标 mean_reward
 * （Go 端 isMeasuredCodingEvidenceMetric 同步接受 pass_at_1/mean_reward）。
 */

import { fetchWithTimeout } from './http.mjs'
import { cachedFetch, cacheResponseData } from './http-cache.mjs'
import { warnNewModelCandidates } from './mapper.mjs'

/**
 * dradar 模型名 -> CCX canonicalModel 映射。
 * 雷达Bench v2 只发布 GPT 系 slug（与 canonical 同名）；其他厂商模型
 * （claude/gemini/glm/kimi/grok/deepseek 等）已不在新榜，映射条目随之退役，
 * 若站点重新收录会经 NEW-MODEL 告警路径补回。
 */
export const DRADAR_MODEL_MAP = {
  'gpt-6.1-sol': 'gpt-6.1-sol',
  'gpt-6-astra': 'gpt-6-astra',
  'gpt-6-sol': 'gpt-6-sol',
  'gpt-6-luna': 'gpt-6-luna',
  'gpt-5.6-sol': 'gpt-5.6-sol',
  'gpt-5.6-luna': 'gpt-5.6-luna',
}

const BASE_URL = 'https://api.codexradar.com'
const SITE_URL = 'https://deng.codexradar.com/'
const CATALOG_URL = `${BASE_URL}/api/v1/library-catalog`
const SCORE_PATH = '/api/v1/radar-bench-score'
const SCORE_FETCH_TIMEOUT_MS = 30_000
// 单批并发：34 个 (模型,档位) 请求分批打，避免对社区站点瞬时压满
const SCORE_CONCURRENCY = 4

/**
 * 官网对未覆盖档位标 ⚠（数据不足）。雷达Bench 沿用同口径：
 * 覆盖任务数低于全池任务数的 60% 时该档证据不产出，避免小样本档污染选型证据。
 */
export const DRADAR_MIN_TASK_COVERAGE = 0.6

function describeError(error) {
  const details = [error?.message]
  if (error?.cause?.code) details.push(error.cause.code)
  if (error?.cause?.message && error.cause.message !== error?.message) details.push(error.cause.message)
  return details.filter(Boolean).join(': ') || String(error)
}

async function fetchJson(url, timeoutMs) {
  const resp = await fetchWithTimeout(url, {
    headers: {
      'User-Agent': 'ccx-benchmark-updater/1.0',
      Accept: 'application/json',
    },
  }, timeoutMs)
  if (!resp.ok) {
    const body = await resp.text().catch(() => '')
    throw new Error(`HTTP ${resp.status} ${resp.statusText} for ${url}${body ? `: ${body.slice(0, 200)}` : ''}`)
  }
  return resp.json()
}

/**
 * 获取雷达Bench 目录（含四库 model_effort_selections 与 unified_pool 绑定信息）
 * @returns {Promise<Object>}
 */
export async function fetchRadarBenchCatalog() {
  console.log(`[dradar] Fetching ${CATALOG_URL}`)
  const result = await cachedFetch(CATALOG_URL, {
    headers: {
      'User-Agent': 'ccx-benchmark-updater/1.0',
      Accept: 'application/json',
    },
  })
  if (result.cached) {
    console.log(`[dradar] ${CATALOG_URL} → 304 Not Modified, using cached data`)
    return result.data
  }
  const data = await result.response.json()
  cacheResponseData(CATALOG_URL, data)
  return data
}

/**
 * 从目录提取待查 (模型, 档位) 清单：四库 model_effort_selections 的并集。
 * 站点退役的组合（如 luna 无 ultra）不在 selections 里，天然不会发起请求。
 *
 * @param {Object} catalog - library-catalog JSON
 * @param {Object} modelMap - dradar 模型名 -> CCX canonicalModel 映射
 * @returns {{pairs: Array<{model: string, effort: string}>, unmappedModels: string[]}}
 */
export function extractScoreSelections(catalog, modelMap) {
  const pool = catalog?.unified_pool
  if (pool?.score_url !== SCORE_PATH) {
    throw new Error(`unified_pool.score_url mismatch: expected ${SCORE_PATH}, got ${pool?.score_url}`)
  }

  const pairKeys = new Set()
  const unmapped = []
  const seenUnmapped = new Set()
  for (const collection of catalog?.collections || []) {
    for (const selection of collection?.model_effort_selections || []) {
      const { model, effort } = selection
      if (!model || !effort) continue
      pairKeys.add(`${model}|${effort}`)
      if (!modelMap[model] && !seenUnmapped.has(model)) {
        seenUnmapped.add(model)
        unmapped.push(model)
      }
    }
  }
  if (pairKeys.size === 0) {
    throw new Error('library-catalog contains no model_effort_selections')
  }

  const pairs = [...pairKeys]
    .map(key => {
      const [model, effort] = key.split('|')
      return { model, effort }
    })
    .sort((a, b) => a.model.localeCompare(b.model) || a.effort.localeCompare(b.effort))
  return { pairs, unmappedModels: unmapped }
}

/**
 * 获取单 (模型, 档位) 的雷达Bench 权威分 payload。
 * 站点会因组合退役/不支持返回 4xx（unsupported_selection），调用方按可跳过处理。
 * @returns {Promise<Object|null>} payload；不可用组合返回 null
 */
export async function fetchRadarBenchScore(model, effort) {
  const url = `${BASE_URL}${SCORE_PATH}?model=${encodeURIComponent(model)}&effort=${encodeURIComponent(effort)}`
  try {
    const result = await cachedFetch(url, {
      headers: {
        'User-Agent': 'ccx-benchmark-updater/1.0',
        Accept: 'application/json',
      },
    }, SCORE_FETCH_TIMEOUT_MS)

    if (result.cached) return result.data

    const data = await result.response.json()
    cacheResponseData(url, data)
    return data
  } catch (error) {
    const message = describeError(error)
    if (/HTTP 4\d\d/.test(message)) {
      console.warn(`[dradar] ${model} ${effort}: unavailable on RadarBench (${message}); skipped`)
      return null
    }
    throw error
  }
}

/**
 * 从 score payload 提取 0-1 等权平均任务奖励率。
 * @returns {number|null} score 为 null（missing_current_result）时返回 null
 */
export function radarScoreRatio(payload) {
  const score = payload?.score
  const scoreMax = Number(payload?.score_max)
  if (typeof score !== 'number' || !Number.isFinite(score) || !Number.isFinite(scoreMax) || scoreMax <= 0) {
    return null
  }
  return score / scoreMax
}

/**
 * 档位覆盖门槛：score 缺失或覆盖任务数低于全池的 DRADAR_MIN_TASK_COVERAGE 时证据不产出。
 * @returns {boolean}
 */
export function scoreCoverageSufficient(payload, minCoverage = DRADAR_MIN_TASK_COVERAGE) {
  const ratio = radarScoreRatio(payload)
  if (ratio === null) return false
  const coverage = Number(payload?.coverage)
  const required = Number(payload?.required_tasks)
  if (!Number.isFinite(coverage) || !Number.isFinite(required) || required <= 0) return false
  return coverage / required >= minCoverage
}

/**
 * 从 score payload 的 statistics 提取成本信息（官方标准 API 等效成本）。
 * api_equivalent_usd.mean 是已知样本的单 run 均值，与旧深榜 ran_by 逐 run
 * 聚合口径一致；中位数与时长中位数新端点不提供，置 null。
 * @returns {{meanCost: number|null, medianCost: null, meanDuration: number|null, medianDuration: null, nRuns: number}}
 */
export function extractCostFromStatistics(payload) {
  const metric = payload?.statistics?.metrics?.api_equivalent_usd
  const elapsed = payload?.statistics?.metrics?.elapsed_ms?.mean
  // mean 为 JSON null 时 Number(null)===0 会把「缺成本证据」误当 0 美元，必须先验类型
  const meanCost = typeof metric?.mean === 'number' && Number.isFinite(metric.mean) ? metric.mean : null
  const meanDuration = typeof elapsed === 'number' && Number.isFinite(elapsed) && elapsed > 0 ? elapsed / 1000 : null
  return {
    meanCost,
    medianCost: null,
    meanDuration,
    medianDuration: null,
    nRuns: Number(metric?.known_samples) || 0,
  }
}

/**
 * 生成 benchmarkEvidence 对象
 *
 * @param {Object} entry - {canonicalModel, sourceModel, effort, payload, ratio, cost}
 * @param {Array<{canonicalModel: string, bestRatio: number}>} cohort - 参与百分位计算的模型队列
 * @returns {Object} - benchmarkEvidence 条目
 */
export function toBenchmarkEvidence(entry, cohort) {
  const allRatios = cohort.map(m => m.bestRatio).filter(rate => rate > 0)
  const atOrBelow = allRatios.filter(rate => rate <= entry.ratio).length
  const percentile = allRatios.length > 0 ? atOrBelow / allRatios.length : 0

  const evidence = {
    benchmark: 'codexradar',
    benchmarkVersion: entry.payload?.score_version || 'radar-bench-v2-150',
    sourceModel: entry.sourceModel,
    domain: 'coding',
    metric: 'mean_reward',
    rawValue: entry.ratio,
    uncertainty: 0, // dradar 不提供 CI
    cohortPercentile: percentile,
    taskCount: Number(entry.payload?.coverage) || 0,
    cohortSize: cohort.length,
    effort: entry.effort || 'default',
    selectionBasis: 'per_effort',
    sourceUrl: SITE_URL,
    capturedAt: new Date().toISOString().split('T')[0],
  }

  // 若该模型 × effort 有实测等效成本，则以单 run 均值注入 evidence，供 frontier 成本轴校准。
  // 舍入到 2 位小数（0.01 美元）：costUsd 进入 registry 受版本管理，
  // 全精度浮点会让上游每次重算 mean 都产生亚美分抖动，制造无意义 diff。
  if (entry.cost?.meanCost !== null && entry.cost?.meanCost !== undefined && Number.isFinite(entry.cost.meanCost)) {
    evidence.costUsd = Math.round(entry.cost.meanCost * 1e2) / 1e2
  }

  return evidence
}

/**
 * 主函数：抓取并转换雷达Bench数据
 *
 * @param {Object} modelMap - dradar 模型名 -> CCX canonicalModel 映射
 * @returns {Promise<{profiles: Object, unmappedModels: string[]}>}
 *   profiles: {canonicalModel: {benchmarkEvidence, costData, efforts}}
 */
export async function fetchDradarData(modelMap) {
  try {
    const catalog = await fetchRadarBenchCatalog()
    const { pairs, unmappedModels } = extractScoreSelections(catalog, modelMap)
    console.log(`[dradar] RadarBench score selections: ${pairs.length} (model, effort) pairs`)

    // 分批并发拉取各 (模型, 档位) 权威分
    const payloads = []
    for (let i = 0; i < pairs.length; i += SCORE_CONCURRENCY) {
      const batch = pairs.slice(i, i + SCORE_CONCURRENCY)
      const results = await Promise.all(
        batch.map(({ model, effort }) => fetchRadarBenchScore(model, effort)),
      )
      batch.forEach(({ model, effort }, idx) => {
        if (results[idx]) payloads.push({ model, effort, payload: results[idx] })
      })
    }
    if (payloads.length === 0) {
      throw new Error('all radar-bench-score requests returned no usable data')
    }

    // 第一遍：按 canonical 收集通过覆盖门槛的档位条目
    const entriesByCanonical = {}
    for (const { model, effort, payload } of payloads) {
      const canonical = modelMap[model]
      if (!canonical) continue
      if (!scoreCoverageSufficient(payload)) {
        const coverage = Number(payload?.coverage) || 0
        const required = Number(payload?.required_tasks) || 0
        console.warn(
          `[dradar] [INSUFFICIENT] ${canonical} ${effort}: 任务覆盖 ${coverage}/${required} ` +
            `< ${DRADAR_MIN_TASK_COVERAGE * 100}% 或无有效分，证据丢弃`,
        )
        continue
      }
      const ratio = radarScoreRatio(payload)
      if (ratio === null) continue
      ;(entriesByCanonical[canonical] ||= []).push({
        canonicalModel: canonical,
        sourceModel: model,
        effort,
        payload,
        ratio,
        cost: extractCostFromStatistics(payload),
      })
    }

    // 百分位队列：只含拿到有效档位的模型，bestRatio 取各模型最高档
    const cohort = Object.entries(entriesByCanonical).map(([canonicalModel, entries]) => ({
      canonicalModel,
      bestRatio: Math.max(...entries.map(entry => entry.ratio)),
    }))

    // 第二遍：每个 (模型, 档位) 一条证据 + costData + efforts 明细
    const result = {}
    for (const [canonical, entries] of Object.entries(entriesByCanonical)) {
      result[canonical] = {
        benchmarkEvidence: entries.map(entry => toBenchmarkEvidence(entry, cohort)),
        costData: {},
        efforts: {},
      }
      for (const entry of entries) {
        result[canonical].efforts[entry.effort] = {
          passRate: entry.ratio,
          graded: Number(entry.payload?.valid_grade_events) || 0,
          cells: Number(entry.payload?.coverage) || 0,
          cellsPassed: Math.round(entry.ratio * (Number(entry.payload?.coverage) || 0)),
          taskIds: (entry.payload?.tasks || []).filter(t => (t?.sample_count || 0) > 0).map(t => t.task_id),
        }
        if (entry.cost.meanCost !== null || entry.cost.nRuns > 0) {
          result[canonical].costData[entry.effort] = entry.cost
        }
      }
    }

    const models = Object.keys(result).sort()
    console.log(`[dradar] Extracted data for ${models.length} models: ${models.join(', ') || '(none)'}`)

    // 新模型检测：站点收录了映射表没有的模型名时告警，避免分数被静默丢弃
    warnNewModelCandidates(unmappedModels, modelMap, {
      source: 'dradar',
      mapName: 'DRADAR_MODEL_MAP',
    })
    return { profiles: result, unmappedModels }
  } catch (err) {
    console.error('[dradar] Failed to fetch data:', describeError(err))
    throw err
  }
}
