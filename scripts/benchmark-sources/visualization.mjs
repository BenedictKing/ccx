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

const DEFAULT_QUALITY_TIERS = {
  scale: '0-100',
  algorithm: 'medium-aligned-coding-v1',
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

// 小样本档位阈值：直测任务格数低于该值的 effort 档视为未完成测量，
// 不参与等效分校准与质量档评定（CodexRadar 新模型先跑 1 个任务且恰好
// 通过时 pass@1=100% 纯属噪声，2026-08 hy4-preview low 档 1/1 曾把
// 插值等效分虚抬进 premium）。taskCount 缺失/为 0 表示来源未提供任务数，
// 视为可信以兼容旧数据与无任务数口径的来源。
const MIN_RELIABLE_TASKS = 3

function hasReliableSample(taskCount) {
  return !(Number.isFinite(taskCount) && taskCount > 0 && taskCount < MIN_RELIABLE_TASKS)
}

const REGULAR_EFFORT_RANK = 1

function effortRankOf(effort) {
  return EFFORT_ORDER.get(String(effort || 'default').trim().toLowerCase()) ?? REGULAR_EFFORT_RANK
}

/**
 * 组内校准的常规 effort 等效分：只用同组（模型×来源）的实测 effort 轨迹，
 * 不跨模型借用增益比率——不同模型的 effort 增益差异巨大（实测 low→medium
 * 从 1.1x 到 2.0x 不等），全局比率会把低增益模型的 low 档点虚抬到超过
 * 高能力模型。规则（输出恒在组内实测分数范围内）：
 *   1. 有 medium/default 实测 → 直接用（证据最充分）；
 *   2. 组内档位跨越 medium（如仅 low+high）→ 相邻两点线性插值；
 *   3. 全部在一侧（如仅 high/xhigh 或单点）→ 取最接近 medium 的档位
 *      原始分，距离并列时取低分（保守）。
 */
function mediumAlignedScore(points) {
  const valid = points
    .filter(point => Number.isFinite(point.passRate) && hasReliableSample(point.taskCount))
    .map(point => ({ rank: effortRankOf(point.effort), score: point.passRate * 100 }))
  if (!valid.length) return null
  const regular = valid.filter(point => point.rank === REGULAR_EFFORT_RANK)
  if (regular.length) return Math.max(...regular.map(point => point.score))
  valid.sort((a, b) => a.rank - b.rank)
  for (let index = 0; index + 1 < valid.length; index++) {
    const lower = valid[index]
    const upper = valid[index + 1]
    if (lower.rank < REGULAR_EFFORT_RANK && upper.rank > REGULAR_EFFORT_RANK) {
      const t = (REGULAR_EFFORT_RANK - lower.rank) / (upper.rank - lower.rank)
      return lower.score + (upper.score - lower.score) * t
    }
  }
  let best = null
  for (const point of valid) {
    const distance = Math.abs(point.rank - REGULAR_EFFORT_RANK)
    const bestDistance = Math.abs(best?.rank - REGULAR_EFFORT_RANK)
    if (best == null || distance < bestDistance
        || (distance === bestDistance && point.score < best.score)) {
      best = point
    }
  }
  return best.score
}

function regularEffortBaselineScore(evidence = []) {
  const bySource = new Map()
  for (const item of evidence) {
    if (item?.domain !== 'coding' || item?.metric !== 'pass_at_1') continue
    if (item.benchmark !== 'deepswe' && item.benchmark !== 'codexradar' && item.benchmarkVersion !== 'codexradar') continue
    if (!Number.isFinite(item.rawValue)) continue
    if (!hasReliableSample(item.taskCount)) continue
    const source = sourceName(item)
    if (!bySource.has(source)) bySource.set(source, [])
    bySource.get(source).push({ effort: item.effort, passRate: item.rawValue, taskCount: item.taskCount })
  }
  // 各来源分别校准后取最小值（保守，量纲仍为原始分百分制）。
  const scores = [...bySource.values()]
    .map(points => mediumAlignedScore(points))
    .filter(score => score != null)
  return scores.length ? Math.min(...scores) : null
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
        taskCount: evidence.taskCount ?? null,
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
        taskCount: result.cells ?? null,
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
        taskCount: evidence.taskCount ?? null,
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
/**
 * 成本行按 (model|effort|source) 去重：live 源（deepswe 榜单 / dradar 聚合）优先，
 * registry 证据行只是 live 源缺席时的回填（成本已舍入到 2 位小数），两者并存会让
 * 同一点出现成对行——轨迹折线来回抖、表格重复、Pareto 前沿吃到重复点。
 */
function preferLiveCostRows(liveRows, registryRows) {
  const liveKeys = new Set(liveRows.map(row => `${row.model}|${row.effort}|${row.source}`))
  return [
    ...liveRows,
    ...registryRows.filter(row => !liveKeys.has(`${row.model}|${row.effort}|${row.source}`)),
  ]
}

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
  const data = preferLiveCostRows([
    ...extractDeepsweCostRows(deepsweLeaderboard, modelMap, models),
    ...extractDradarCostRows(dradarProfiles, models),
  ], extractRegistryCostRows(benchmarkProfiles, models)).sort((a, b) => (
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

  // 等效分按（模型×来源）组校准：同组各 effort 档共享同一常规等效能力分，
  // 图表轨迹即"同一能力在不同 effort 档的成本"。
  const groupScores = new Map()
  for (const row of data) {
    const key = `${row.model}|${row.source}`
    if (!groupScores.has(key)) groupScores.set(key, [])
    groupScores.get(key).push({ effort: row.effort, passRate: row.pass_rate, taskCount: row.taskCount })
  }
  const qualityScoreByGroup = new Map([...groupScores]
    .map(([key, points]) => [key, mediumAlignedScore(points)]))

  return {
    generatedAt: new Date().toISOString(),
    qualityTiers,
    data: data.map(row => normalizeCostRow({
      ...row,
      quality_score: qualityScoreByGroup.get(`${row.model}|${row.source}`) ?? null,
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
    taskCount: row.taskCount ?? null,
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
    taskCount: row.taskCount ?? null,
  }
}
