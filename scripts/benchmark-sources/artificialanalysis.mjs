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
import { warnNewModelCandidates } from './mapper.mjs'

const BASE_URL = 'https://artificialanalysis.ai/api/v2'
const LANGUAGE_MODELS_FREE_URL = `${BASE_URL}/language/models/free`
const TEXT_TO_IMAGE_FREE_URL = `${BASE_URL}/media/text-to-image/models/free`

const FETCH_HEADERS = {
  'User-Agent': 'ccx-benchmark-updater/1.0',
  Accept: 'application/json',
}

/**
 * Artificial Analysis LLM 基名（连字符形式）-> CCX canonicalModel 映射。
 * 解析逻辑（resolveArtificialAnalysisSlug）先把 slug 的点号归一成连字符、剥离
 * effort/non-reasoning 尾后缀，再查本表，因此点号 canonical（如 claude-haiku-4.5、
 * gpt-5.5、glm-5.2）只需收录基名一条，无需重复维护点号/连字符两个变体。
 */
export const ARTIFICIAL_ANALYSIS_MODEL_MAP = {
  'gpt-6-astra': 'gpt-6-astra',
  'gpt-6': 'gpt-6-astra',
  'astra': 'gpt-6-astra',
  'claude-opus-4-8': 'claude-opus-4-8',
  'claude-opus-4-7': 'claude-opus-4-7',
  'claude-opus-4-6': 'claude-opus-4-6',
  'claude-opus-4-6-adaptive': 'claude-opus-4-6',
  'claude-opus-4-5': 'claude-opus-4-5',
  'claude-opus-5': 'claude-opus-5',
  'claude-sonnet-5': 'claude-sonnet-5',
  'claude-sonnet-4-6': 'claude-sonnet-4-6',
  'claude-haiku-4-5': 'claude-haiku-4.5',
  'claude-fable-5': 'claude-fable-5',
  'claude-fable-5-1': 'claude-fable-5-1',
  'gpt-5-6-sol': 'gpt-5.6-sol',
  'gpt-5-6-terra': 'gpt-5.6-terra',
  'gpt-5-6-luna': 'gpt-5.6-luna',
  'gpt-5-5': 'gpt-5.5',
  'gpt-5-5-pro': 'gpt-5.5-pro',
  'gpt-5-4': 'gpt-5.4',
  'gpt-5-4-mini': 'gpt-5.4-mini',
  'gpt-5-4-nano': 'gpt-5.4-nano',
  'gpt-5-4-pro': 'gpt-5.4-pro',
  'gpt-5-3-codex': 'gpt-5.3-codex',
  'gpt-5-2': 'gpt-5.2',
  'gpt-5-2-codex': 'gpt-5.2-codex',
  'glm-5-3': 'glm-5.3',
  'glm-5-3-flash': 'glm-5.3-flash',
  'glm-5-turbo': 'glm-5-turbo',
  'glm-5-2': 'glm-5.2',
  'glm-5-1': 'glm-5.1',
  'glm-5': 'glm-5',
  'kimi-k2-7-code': 'kimi-k2.7-code',
  'kimi-k2-6': 'kimi-k2.6',
  'kimi-k2-5': 'kimi-k2.5',
  'kimi-k2-thinking': 'kimi-k2-thinking',
  'kimi-k3': 'kimi-k3',
  'gemini-3-1-pro': 'gemini-3.1-pro',
  'gemini-3-flash': 'gemini-3-flash',
  'gemini-3-5-flash': 'gemini-3.5-flash',
  'gemini-3-5-flash-lite': 'gemini-3.5-flash-lite',
  'gemini-3-7-flash': 'gemini-3.7-flash',
  'gemini-3-6-flash': 'gemini-3.6-flash',
  'gemini-3-8-flash': 'gemini-3.8-flash',
  'grok-4-5': 'grok-4.5',
  'grok-4-6': 'grok-4.6',
  // AA v2 GA slug（2026-04-07）；-non-reasoning 变体经 splitEffortSuffix 折进同一 canonical，
  // grok-4-20-0309 旧快照（定价 $2/$6 已过期）故意不映射
  'grok-4-20': 'grok-4.20',
  // deepseek 系：AA composite/coding 证据补充（overallScore 仍归 benchlm 所有）
  'deepseek-v3-2': 'deepseek-v3.2',
  'deepseek-v4-flash': 'deepseek-v4-flash',
  'deepseek-v4-pro': 'deepseek-v4-pro',
  // deepseek 日期快照（-MMDD）归并到对应基模型
  'deepseek-v4-flash-0420': 'deepseek-v4-flash',
  'deepseek-v4-pro-0424': 'deepseek-v4-pro',
  'minimax-m2-1': 'minimax-m2.1',
  'minimax-m2-5': 'minimax-m2.5',
  'minimax-m2-7': 'minimax-m2.7',
  'minimax-m3': 'minimax-m3',
  'qwen3-7-max': 'qwen3.7-max',
  'qwen3-7-plus': 'qwen3.7-plus',
  'qwen3-8-max': 'qwen3.8-max',
  // Model Studio 快照别名 Qwen3.8-Max-0902 归并到基模型
  'qwen3-8-max-0902': 'qwen3.8-max',
  'qwen3-max': 'qwen3-max',
  // AA 快照变体：2.4T 参数量 + commit hash 后缀，属于 qwen3.8-max 同一模型
  'qwen3-8-2-4t-a95b': 'qwen3.8-max',
  'longcat-2-0': 'longcat-2.0',
  'step-3-7-flash': 'step-3.7-flash',
  'doubao-seed-code': 'doubao-seed-code',
  'mimo-v2-5': 'mimo-v2.5',
  'mimo-v2-5-pro': 'mimo-v2.5-pro',
  'muse-spark-1-1': 'muse-spark-1.1',
  'muse-spark-1-2': 'muse-spark-1.2',
  'muse-spark-1-3': 'muse-spark-1.3',
}

// 点号 canonical（claude-haiku-4.5 / gpt-5.5 / glm-5.2 …）的 AA slug 一律用连字符。
// 解析前统一把点号归一成连字符再查表，省的为每个点号 canonical 维护两条表项。
const normalizeSlug = slug => slug.toLowerCase().replace(/\./g, '-')

// 归一化后的基名 -> canonical 查找表（惰性构建，key 已是连字符形式）。
let _normalizedMap = null
function normalizedModelMap() {
  if (!_normalizedMap) {
    _normalizedMap = {}
    for (const [key, canonical] of Object.entries(ARTIFICIAL_ANALYSIS_MODEL_MAP)) {
      _normalizedMap[normalizeSlug(key)] = canonical
    }
  }
  return _normalizedMap
}

// AA 对可调 reasoning 的模型按 effort 档位与 non-reasoning 变体单独跑分（如
// claude-opus-5-xhigh / gpt-5.6-luna-non-reasoning），统一在基名后用连字符后缀表达。
const EFFORT_SUFFIX_RE = /-(xhigh|high|medium|low|minimal)$/
const NON_REASONING_SUFFIX_RE = /-non-reasoning(?:-[a-z]+-effort)?$/

/**
 * 解析 AA slug 的 effort / non-reasoning 尾后缀，返回剥离后的基名与推导出的 evidence 标记。
 * - 纯 effort 档位（-xhigh…）→ effort=该档位，selectionBasis='best_available_effort'
 * - non-reasoning（含 -low-effort 组合）→ effort='default'，selectionBasis='non_reasoning'
 * - 无后缀 → effort='default'，selectionBasis='composite_index'
 * @param {string} normalizedSlug 已归一（连字符、小写）
 * @returns {{base:string, effort:string, selectionBasis:string}}
 */
function splitEffortSuffix(normalizedSlug) {
  let base = normalizedSlug
  let effort = 'default'
  let selectionBasis = 'composite_index'

  if (NON_REASONING_SUFFIX_RE.test(base)) {
    base = base.replace(NON_REASONING_SUFFIX_RE, '')
    selectionBasis = 'non_reasoning'
  } else {
    const m = base.match(EFFORT_SUFFIX_RE)
    if (m) {
      base = base.slice(0, -m[0].length)
      effort = m[1]
      selectionBasis = 'best_available_effort'
    }
  }
  return { base, effort, selectionBasis }
}

/**
 * 将 AA LLM slug 解析为 canonical + effort 档位。
 * 先精确命中（含点/连字符归一），未命中再剥离 effort/non-reasoning 尾后缀查基名。
 * @param {string} slug AA 原始 slug
 * @returns {{canonical:string, effort:string, selectionBasis:string}|null}
 */
export function resolveArtificialAnalysisSlug(slug) {
  if (!slug) return null
  const normalized = normalizeSlug(slug)
  const map = normalizedModelMap()

  if (map[normalized]) {
    return { canonical: map[normalized], effort: 'default', selectionBasis: 'composite_index' }
  }

  const { base, effort, selectionBasis } = splitEffortSuffix(normalized)
  if (base !== normalized && map[base]) {
    return { canonical: map[base], effort, selectionBasis }
  }
  return null
}

/**
 * 将 AA LLM slug 转换为 CCX canonicalModel（不区分 effort 档位，仅返回基名 canonical）。
 * @param {string} slug
 * @returns {string|null}
 */
export function artificialAnalysisToCanonical(slug) {
  return resolveArtificialAnalysisSlug(slug)?.canonical || null
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
  'gpt-image-2': 'gpt-image-2',
  'nano-banana-2': 'nano-banana-2',
  'seedream-5.0-pro': 'seedream-5.0-pro',
  'seedream-5-0-pro': 'seedream-5.0-pro',
  'seedream-5.0-lite': 'seedream-5.0-lite',
  'seedream-5-0-lite': 'seedream-5.0-lite',
  'seedream-4.0': 'seedream-4.0',
  'seedream-4-0': 'seedream-4.0',
  'bytedance-seed_seedream-4-0': 'seedream-4.0',
}

function authHeaders(apiKey) {
  return { ...FETCH_HEADERS, 'x-api-key': apiKey }
}

function today() {
  return new Date().toISOString().split('T')[0]
}

// AA intelligence_index 当前版本 v4.1.1（2026-08-06 patch），由 9 项 evaluation 复合而成（见 AA 方法文档）。
// 主要变更：τ³-Banking v1.0.1；HLE/AA-LCR/AA-Omniscience 评分模型统一为 GPT-5.6 Luna (medium)。
// coding_index 与 agentic_index 为上述 evaluation 子集派生指标，不单独 version。
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
    const result = await cachedFetch(url, { headers }, 60_000)

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
 * slug 经 resolveArtificialAnalysisSlug 解析出 canonical + effort 档位：裸 slug 记
 * default/composite_index，-xhigh 等档位记对应 effort/best_available_effort，
 * -non-reasoning 变体记 default/non_reasoning，同一 canonical 下各档证据互不污染。
 * @param {Array} models 合并后的 data[]
 * @param {number|null} version intelligence_index_version（如 4.1）
 * @returns {{profiles:Object, unmappedSlugs:string[]}}
 *   profiles: {canonical: {benchmarkEvidence:[...], aaMeta:{slug}}}
 */
export function extractLlmProfiles(models, version) {
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
    const resolved = resolveArtificialAnalysisSlug(slug)
    if (!resolved) {
      if (slug) unmappedSlugs.push(slug)
      continue
    }
    const { canonical, effort, selectionBasis } = resolved

    const ev = item.evaluations || {}
    const indices = [
      ['overall', 'intelligence_index', 'artificial_analysis_intelligence_index'],
      ['coding', 'coding_index', 'artificial_analysis_coding_index'],
      ['agentic', 'agentic_index', 'artificial_analysis_agentic_index'],
    ]

    const evidence = []
    for (const [domain, metric, key] of indices) {
      const raw = Number(ev[key])
      if (!Number.isFinite(raw) || raw <= 0) continue
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
        effort,
        selectionBasis,
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
  const result = await cachedFetch(TEXT_TO_IMAGE_FREE_URL, { headers }, 60_000)

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
 * LLM slug 走 resolveArtificialAnalysisSlug（基名 + effort 剥离），不再依赖外部传映射表。
 * @param {string} apiKey
 * @param {Object} [_modelMap] 已废弃：LLM 映射改为内部解析，此参数仅为兼容旧调用签名而保留
 * @param {Object} imageMap AA image slug -> canonical
 * @returns {Promise<{llm:Object, imageArena:Object, tier:string, version:number|null, unmappedLlmSlugs:string[], unmappedImageSlugs:string[]}>}
 */
export async function fetchArtificialAnalysisData(apiKey, _modelMap, imageMap) {
  const { tier, version, models: llmModels, pages } = await fetchLanguageModelsFree(apiKey)
  const { profiles: llm, unmappedSlugs: unmappedLlmSlugs } = extractLlmProfiles(llmModels, version)
  const llmModelsMapped = Object.keys(llm).sort()
  console.log(`[artificial-analysis] Extracted LLM data for ${llmModelsMapped.length} models across ${pages} page(s): ${llmModelsMapped.join(', ') || '(none)'}`)
  // 新模型检测：队列出现同家族更高版本但未映射的 slug 时告警，避免分数被静默丢弃
  warnNewModelCandidates(unmappedLlmSlugs, ARTIFICIAL_ANALYSIS_MODEL_MAP, {
    source: 'artificial-analysis',
    mapName: 'ARTIFICIAL_ANALYSIS_MODEL_MAP',
  })

  const { models: imageModels } = await fetchTextToImageArenaFree(apiKey)
  const { profiles: imageArena, unmappedSlugs: unmappedImageSlugs } = extractImageArenaProfiles(imageModels, imageMap)
  const imageModelsMapped = Object.keys(imageArena).sort()
  console.log(`[artificial-analysis] Extracted image arena data for ${imageModelsMapped.length} models: ${imageModelsMapped.join(', ') || '(none)'}`)
  warnNewModelCandidates(unmappedImageSlugs, imageMap, {
    source: 'artificial-analysis',
    mapName: 'ARTIFICIAL_ANALYSIS_IMAGE_MODEL_MAP',
  })

  return {
    llm,
    imageArena,
    tier,
    version,
    unmappedLlmSlugs,
    unmappedImageSlugs,
  }
}
