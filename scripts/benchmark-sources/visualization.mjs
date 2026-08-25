// 与 Go 端 8 档规范轴一致：off/minimal/low/medium/high/xhigh/max/ultra。
// 图表仅展示实际出现的档位；ultra 为最高投入档，不可省略，否则会被 (?? 99) 沉底而不可见。
const EFFORT_ORDER = new Map([
  ['off', -2],
  ['minimal', -1],
  ['low', 0],
  ['medium', 1],
  ['high', 2],
  ['xhigh', 3],
  ['max', 4],
  ['ultra', 5],
])

const EFFORT_QUALITY_RATIO = new Map([
  ['low', 0.686],
  ['default', 1],
  ['medium', 1],
  ['high', 1.413],
  ['xhigh', 1.627],
  ['max', 1.975],
  ['ultra', 1.975],
])

const DEFAULT_QUALITY_TIERS = {
  scale: '0-100',
  algorithm: 'regular-effort-coding-v1',
  source: 'backend-autopilot',
  premiumMin: 75,
  highMin: 61,
  normalMin: 55,
}

function includesModel(model, models) {
  return !models || models.includes(model)
}

function normalizedScore(value) {
  if (!Number.isFinite(value)) return null
  return value <= 1 ? value * 100 : value
}

function effortQualityScore(effort, passRate) {
  const ratio = EFFORT_QUALITY_RATIO.get(String(effort || 'default').trim().toLowerCase())
  return ratio ? passRate * 100 / ratio : null
}

function regularEffortBaselineScore(evidence = []) {
  const byEffort = new Map()
  for (const item of evidence) {
    if (item?.domain !== 'coding' || item?.metric !== 'pass_at_1') continue
    if (item.benchmark !== 'deepswe' && item.benchmark !== 'codexradar' && item.benchmarkVersion !== 'codexradar') continue
    const effort = String(item.effort || 'default').trim().toLowerCase()
    const ratio = EFFORT_QUALITY_RATIO.get(effort)
    if (!ratio || !Number.isFinite(item.rawValue)) continue
    const score = item.rawValue * 100 / ratio
    if (score > (byEffort.get(effort) ?? -Infinity)) byEffort.set(effort, score)
  }
  if (!byEffort.size) return null
  for (const effort of ['medium', 'default']) {
    if (byEffort.has(effort)) return byEffort.get(effort)
  }
  return Math.min(...byEffort.values())
}

function largestGapMid(scores, floor) {
  let bestSize = 0
  let bestMid = null
  for (let index = 0; index + 1 < scores.length; index++) {
    if (scores[index] < floor || scores[index + 1] < floor) continue
    const size = scores[index + 1] - scores[index]
    if (size > bestSize) {
      bestSize = size
      bestMid = (scores[index] + scores[index + 1]) / 2
    }
  }
  return bestMid
}

function deriveQualityMetadata(benchmarkProfiles = {}) {
  const scores = []
  const qualityScores = new Map()
  const seen = new Set()
  for (const [model, profile] of Object.entries(benchmarkProfiles || {})) {
    const canonical = profile?.canonicalModel || model
    if (seen.has(canonical)) continue
    seen.add(canonical)
    const score = regularEffortBaselineScore(profile?.benchmarkEvidence)
    if (score == null) continue
    qualityScores.set(canonical, score)
    scores.push(score)
  }
  scores.sort((a, b) => a - b)
  let { premiumMin, highMin, normalMin } = DEFAULT_QUALITY_TIERS
  if (scores.length >= 4) {
    const premium = largestGapMid(scores, scores.at(-1) * 0.75)
    if (premium != null) premiumMin = premium
    const belowPremium = scores.filter(score => score < premiumMin)
    const high = largestGapMid(belowPremium, premiumMin * 0.5)
    if (high != null) highMin = high
    const belowHigh = scores.filter(score => score < highMin)
    const normal = largestGapMid(belowHigh, highMin * 0.4)
    if (normal != null) normalMin = normal
  }
  return {
    qualityScores,
    qualityTiers: { ...DEFAULT_QUALITY_TIERS, premiumMin, highMin, normalMin },
  }
}

function sourceName(evidence) {
  if (evidence.benchmark === 'codexradar' || evidence.benchmarkVersion === 'codexradar') {
    return 'CodexRadar'
  }
  if (evidence.benchmark === 'deepswe') {
    return `DeepSWE ${evidence.benchmarkVersion || ''}`.trim()
  }
  if (evidence.benchmark === 'artificial_analysis') {
    return 'Artificial Analysis'
  }
  return [evidence.benchmark, evidence.benchmarkVersion].filter(Boolean).join(' ')
}

function extractRegistryCostRows(benchmarkProfiles = {}, models = null) {
  const rows = []
  for (const [model, profile] of Object.entries(benchmarkProfiles || {})) {
    const canonical = profile?.canonicalModel || model
    if (!includesModel(canonical, models)) continue
    const bySource = new Map()
    for (const evidence of profile?.benchmarkEvidence || []) {
      if (evidence.domain !== 'coding' || evidence.metric !== 'pass_at_1') continue
      if (evidence.benchmark !== 'deepswe' && evidence.benchmark !== 'codexradar' && evidence.benchmarkVersion !== 'codexradar') continue
      if (!Number.isFinite(evidence.rawValue) || !Number.isFinite(evidence.costUsd)) continue
      const source = sourceName(evidence)
      const effort = String(evidence.effort || 'default').toLowerCase()
      const key = `${source}|${effort}`
      const candidate = {
        model: canonical,
        effort,
        pass_rate: evidence.rawValue,
        mean_cost: evidence.costUsd,
        median_cost: evidence.costUsd,
        source,
        sourceModel: evidence.sourceModel || canonical,
      }
      const current = bySource.get(key)
      if (!current || candidate.pass_rate > current.pass_rate ||
          (candidate.pass_rate === current.pass_rate && candidate.mean_cost < current.mean_cost)) {
        bySource.set(key, candidate)
      }
    }
    rows.push(...bySource.values())
  }
  return rows
}

/**
 * 将 DeepSWE live leaderboard 转为能力-成本散点，并按模型 + effort 去重。
 */
export function extractDeepsweCostRows(data, modelMap, models = null) {
  const bestRows = new Map()
  for (const row of data?.rows || []) {
    const model = modelMap[row.model]
    const passRate = row.pass_at_1 ?? row.pass_rate
    if (!model || !includesModel(model, models) || !Number.isFinite(passRate)) continue
    if (!Number.isFinite(row.mean_cost_usd) && !Number.isFinite(row.median_cost_usd)) continue

    const candidate = {
      model,
      effort: row.reasoning_effort || 'default',
      pass_rate: passRate,
      mean_cost: row.mean_cost_usd,
      median_cost: row.median_cost_usd,
      source: 'DeepSWE v1.1',
      sourceModel: row.model,
    }
    const key = `${model}|${candidate.effort}`
    const current = bestRows.get(key)
    if (!current || candidate.pass_rate > current.pass_rate ||
        (candidate.pass_rate === current.pass_rate && candidate.mean_cost < current.mean_cost)) {
      bestRows.set(key, candidate)
    }
  }
  return [...bestRows.values()]
}

/**
 * 将 CodexRadar 聚合结果转为能力-成本散点。
 */
export function extractDradarCostRows(data, models = null) {
  const rows = []
  for (const [model, profile] of Object.entries(data || {})) {
    if (!includesModel(model, models)) continue
    for (const [effort, result] of Object.entries(profile.efforts || {})) {
      const cost = profile.costData?.[effort]
      if (!Number.isFinite(result.passRate) || !cost) continue
      if (!Number.isFinite(cost.meanCost) && !Number.isFinite(cost.medianCost)) continue
      rows.push({
        model,
        effort,
        pass_rate: result.passRate,
        mean_cost: cost.meanCost,
        median_cost: cost.medianCost,
        source: 'CodexRadar',
        sourceModel: model,
      })
    }
  }
  return rows
}

function evidenceComparisonRows(profiles, models) {
  const rows = []
  for (const [model, profile] of Object.entries(profiles || {})) {
    if (!includesModel(model, models)) continue
    for (const evidence of profile.benchmarkEvidence || []) {
      const score = normalizedScore(evidence.rawValue)
      if (score === null || !evidence.domain) continue
      rows.push({
        model,
        source: sourceName(evidence),
        category: evidence.domain,
        metric: evidence.metric,
        score,
        effort: evidence.effort,
      })
    }
  }
  return rows
}

function benchlmComparisonRows(profiles, models) {
  const rows = []
  for (const [model, profile] of Object.entries(profiles || {})) {
    if (!includesModel(model, models)) continue
    const overallScore = normalizedScore(profile.overallScore)
    if (overallScore !== null) {
      rows.push({ model, source: 'BenchLM.ai', category: 'overall', metric: 'score', score: overallScore })
    }
    for (const [category, value] of Object.entries(profile.categoryScores || {})) {
      const score = normalizedScore(value)
      if (score !== null) {
        rows.push({ model, source: 'BenchLM.ai', category, metric: 'category_score', score })
      }
    }
  }
  return rows
}

/**
 * Artificial Analysis 图像 arena Elo → 比较行。
 * Elo 量表 ~1000-1300，按 category 独立显示，不走 normalizedScore（避免被当 0-1 放大）。
 */
function imageArenaComparisonRows(profiles, models) {
  const rows = []
  for (const [model, profile] of Object.entries(profiles || {})) {
    if (!includesModel(model, models)) continue
    const elo = Number(profile.elo)
    if (!Number.isFinite(elo)) continue
    rows.push({
      model,
      source: 'Artificial Analysis Image Arena',
      category: 'image_arena',
      metric: 'elo',
      score: elo,
    })
  }
  return rows
}

function deduplicateComparisonRows(rows) {
  const unique = new Map()
  for (const row of rows) {
    const key = [row.model, row.source, row.category, row.effort || 'default'].join('|')
    const current = unique.get(key)
    if (!current || row.score > current.score) unique.set(key, row)
  }
  return [...unique.values()]
}

/**
 * 生成图表输入：能力-成本散点展示有成本的来源，多源比较图展示所有 benchmark 来源。
 * 统一将数值舍入到合理精度，避免全精度浮点造成 JSON 输出噪声。
 */
export function buildBenchmarkVisualizationData({
  deepsweProfiles = {},
  deepsweLeaderboard = null,
  benchlmProfiles = {},
  dradarProfiles = {},
  artificialAnalysisProfiles = {},
  artificialAnalysisImageArena = {},
  modelMap = {},
  models = null,
  benchmarkProfiles = {},
} = {}) {
  const { qualityScores, qualityTiers } = deriveQualityMetadata(benchmarkProfiles)
  const data = [
    ...extractDeepsweCostRows(deepsweLeaderboard, modelMap, models),
    ...extractDradarCostRows(dradarProfiles, models),
    ...extractRegistryCostRows(benchmarkProfiles, models),
  ].sort((a, b) => (
    a.source.localeCompare(b.source) ||
    a.model.localeCompare(b.model) ||
    (EFFORT_ORDER.get(a.effort) ?? 99) - (EFFORT_ORDER.get(b.effort) ?? 99)
  ))

  const comparisons = deduplicateComparisonRows([
    ...evidenceComparisonRows(deepsweProfiles, models),
    ...benchlmComparisonRows(benchlmProfiles, models),
    ...evidenceComparisonRows(dradarProfiles, models),
    ...evidenceComparisonRows(artificialAnalysisProfiles, models),
    ...imageArenaComparisonRows(artificialAnalysisImageArena, models),
  ]).sort((a, b) => (
    a.category.localeCompare(b.category) ||
    a.model.localeCompare(b.model) ||
    a.source.localeCompare(b.source)
  ))

  return {
    generatedAt: new Date().toISOString(),
    qualityTiers,
    data: data.map(row => normalizeCostRow({
      ...row,
      quality_score: effortQualityScore(row.effort, row.pass_rate),
      model_quality_score: qualityScores.get(row.model) ?? null,
    })),
    comparisons: comparisons.map(normalizeComparisonRow),
  }
}

/**
 * 舍入成本行数值：
 * - pass_rate: 3 位小数（0.1% 精度）
 * - mean_cost/median_cost: 4 位小数（0.0001 美元精度，足够表达单任务成本差异）
 */
function normalizeCostRow(row) {
  return {
    ...row,
    pass_rate: Math.round(row.pass_rate * 1000) / 1000,
    mean_cost: row.mean_cost != null ? Math.round(row.mean_cost * 10000) / 10000 : null,
    median_cost: row.median_cost != null ? Math.round(row.median_cost * 10000) / 10000 : null,
    quality_score: row.quality_score != null ? Math.round(row.quality_score * 10) / 10 : null,
    model_quality_score: row.model_quality_score != null ? Math.round(row.model_quality_score * 10) / 10 : null,
  }
}

/**
 * 舍入比较行数值：
 * - score: 1 位小数（0.1 分精度）
 */
function normalizeComparisonRow(row) {
  return {
    ...row,
    score: Math.round(row.score * 10) / 10,
  }
}
