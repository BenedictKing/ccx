const EFFORT_ORDER = new Map([
  ['low', 0],
  ['medium', 1],
  ['high', 2],
  ['xhigh', 3],
  ['max', 4],
])

function includesModel(model, models) {
  return !models || models.includes(model)
}

function normalizedScore(value) {
  if (!Number.isFinite(value)) return null
  return value <= 1 ? value * 100 : value
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
} = {}) {
  const data = [
    ...extractDeepsweCostRows(deepsweLeaderboard, modelMap, models),
    ...extractDradarCostRows(dradarProfiles, models),
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
    data,
    comparisons,
  }
}
