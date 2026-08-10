/**
 * 模型能力基准自动更新编排脚本
 *
 * 功能：
 * 1. 从 deepswe、benchlm.ai、dradar (codexradar)、Artificial Analysis 抓取最新 benchmark 数据
 * 2. 从 litellm 抓取价格/上下文窗口数据
 * 3. 映射到 CCX 模型注册表
 * 4. 更新 shared/model-registry/ccx_model_registry.json
 * 5. 运行 generate-model-registry.mjs 重新生成代码和运行时预置
 * 6. 生成多来源 benchmark 可视化
 * 7. 输出变更报告
 *
 * 用法：
 *   node scripts/update-benchmark-data.mjs [--dry-run] [--skip-*] [--models <model1,model2>]
 *
 * 选项：
 *   --dry-run                   只预览变更，不写入文件
 *   --skip-deepswe              跳过 deepswe 数据源
 *   --skip-benchlm              跳过 benchlm.ai 数据源
 *   --skip-dradar               跳过 dradar (codexradar) 数据源
 *   --skip-litellm              跳过 litellm 价格/上下文数据源
 *   --skip-artificial-analysis  跳过 Artificial Analysis 数据源（首个需 API key 的源）
 *   --force-litellm             强制重新拉取并处理 litellm（忽略 blob SHA 缓存）
 *   --models                    只更新指定模型 (逗号分隔)
 *
 * 环境变量：
 *   ARTIFICIAL_ANALYSIS_API_KEY  Artificial Analysis API key（缺失时自动跳过 AA，不报错）
 */

import { existsSync, readFileSync, renameSync, unlinkSync, writeFileSync } from 'node:fs'
import { dirname, join, resolve } from 'node:path'
import { fileURLToPath } from 'node:url'
import { execFileSync } from 'node:child_process'

import {
  DEEPSWE_MODEL_MAP,
  BENCHLM_MODEL_MAP,
  BENCHLM_CATEGORY_MAP,
  canonicalModelToPattern,
  compareCanonicalModels,
  deepsweModelToPattern,
} from './benchmark-sources/mapper.mjs'
import { fetchDeepsweDataset } from './benchmark-sources/deepswe.mjs'
import { fetchBenchlmData } from './benchmark-sources/benchlm.mjs'
import { fetchDradarData, DRADAR_MODEL_MAP } from './benchmark-sources/dradar.mjs'
import { fetchLitellmModelInfo, LITELLM_MODEL_MAP } from './benchmark-sources/litellm.mjs'
import {
  fetchArtificialAnalysisData,
  ARTIFICIAL_ANALYSIS_MODEL_MAP,
  ARTIFICIAL_ANALYSIS_IMAGE_MODEL_MAP,
} from './benchmark-sources/artificialanalysis.mjs'
import { buildBenchmarkVisualizationData } from './benchmark-sources/visualization.mjs'
import { presetArtifactPaths } from './generate-preset-manifest.mjs'
import { saveCache } from './benchmark-sources/http-cache.mjs'

const root = dirname(dirname(fileURLToPath(import.meta.url)))
const registryPath = join(root, 'shared/model-registry/ccx_model_registry.json')
const chartDataPath = '/tmp/benchmark-viz-data.json'
const chartOutputPath = '/tmp/benchmark-chart.html'

// 命令行参数
const args = process.argv.slice(2)
const dryRun = args.includes('--dry-run')
const skipDeepswe = args.includes('--skip-deepswe')
const skipBenchlm = args.includes('--skip-benchlm')
const skipDradar = args.includes('--skip-dradar')
const skipLitellm = args.includes('--skip-litellm')
const forceLitellm = args.includes('--force-litellm')
const skipArtificialAnalysis = args.includes('--skip-artificial-analysis')
const artificialAnalysisApiKey = process.env.ARTIFICIAL_ANALYSIS_API_KEY || ''
// 无 key 且未显式 skip 时自动跳过 AA（首个需 key 的源；保持现有工作流零破坏）
const artificialAnalysisEnabled = !skipArtificialAnalysis && !!artificialAnalysisApiKey
const modelsArg = args.find(a => a.startsWith('--models='))
const modelsArgIndex = args.indexOf('--models')
const modelsValue = modelsArg?.split('=', 2)[1] ?? (modelsArgIndex >= 0 ? args[modelsArgIndex + 1] : '')
const targetModels = modelsValue ? modelsValue.split(',').map(model => model.trim()).filter(Boolean) : null
export const generatedArtifactPaths = [...presetArtifactPaths]

/**
 * 加载注册表
 */
function loadRegistry() {
  const content = readFileSync(registryPath, 'utf8')
  return JSON.parse(content)
}

/**
 * 对 registry 做确定性排序，保证写出顺序稳定。
 *
 * benchmarkProfiles 与每条 profile 的 benchmarkEvidence 此前按"各源 merge/push 的先后"
 * 排列，顺序取决于本次跑了哪些源、上游返回顺序，导致同样数据每次写出顺序不同、
 * 产生大段"删除+新增"的无意义 diff。这里统一按键排序，使顺序只由数据本身决定：
 * - benchmarkProfiles 按版本感知模型序（compareCanonicalModels：版本号降序、预发布后缀置后）
 * - benchmarkEvidence 按 (benchmark, domain, effort, metric) 字典序
 * - sources 按字典序（同域名 URL 自然聚类；此前按各源 merge 追加序，随"本次跑了哪些源"漂移）
 */
function sortRegistryDeterministic(registry) {
  if (Array.isArray(registry.benchmarkProfiles)) {
    for (const profile of registry.benchmarkProfiles) {
      if (Array.isArray(profile?.benchmarkEvidence)) {
        profile.benchmarkEvidence.sort((a, b) =>
          [a.benchmark, a.domain, a.effort, a.metric].join('').localeCompare(
            [b.benchmark, b.domain, b.effort, b.metric].join(''),
          ),
        )
      }
      if (Array.isArray(profile?.sources)) {
        profile.sources.sort((a, b) => String(a).localeCompare(String(b)))
      }
    }
    registry.benchmarkProfiles.sort((a, b) =>
      compareCanonicalModels(a.canonicalModel, b.canonicalModel),
    )
  }
  return registry
}

/**
 * 保存注册表
 */
function serializeRegistry(registry) {
  return JSON.stringify(sortRegistryDeterministic(registry), null, 2) + '\n'
}

function atomicWrite(path, content) {
  const tempPath = `${path}.tmp-${process.pid}-${Date.now()}`
  try {
    writeFileSync(tempPath, content, 'utf8')
    renameSync(tempPath, path)
  } catch (error) {
    if (existsSync(tempPath)) unlinkSync(tempPath)
    throw error
  }
}

function saveRegistry(registry) {
  atomicWrite(registryPath, serializeRegistry(registry))
}

/**
 * 查找现有的 benchmarkProfile
 * @param {Array} profiles
 * @param {string} canonicalModel
 * @returns {number} - 索引，未找到返回 -1
 */
function findProfileIndex(profiles, canonicalModel) {
  return profiles.findIndex(p => p.canonicalModel === canonicalModel)
}

/**
 * 创建新的 benchmarkProfile
 * @param {string} canonicalModel
 * @param {string} pattern
 * @returns {Object}
 */
export function createProfile(canonicalModel, pattern = canonicalModelToPattern(canonicalModel)) {
  if (!pattern) {
    throw new Error(`cannot generate benchmark pattern for ${canonicalModel}`)
  }
  return {
    patterns: [pattern],
    canonicalModel,
    verifiedAt: new Date().toISOString().split('T')[0],
    lane: 'provisional',
    sources: [],
    sharedResults: 1,
    comparableCategories: 1,
    totalCategories: 1,
  }
}

/**
 * 比较两条证据列表是否等价：忽略 capturedAt（抓取日期），元素顺序无关。
 * 用于判断上游数据是否真的变了，没变就跳过 merge，避免只刷日期产生无效 diff。
 */
function evidenceListsEqual(a, b) {
  if (a.length !== b.length) return false
  const normalize = list =>
    list.map(item => JSON.stringify({ ...item, capturedAt: undefined })).sort()
  const sortedA = normalize(a)
  const sortedB = normalize(b)
  return sortedA.every((item, i) => item === sortedB[i])
}

// 顺序无关比较：sources 在写出前会被 sortRegistryDeterministic 排成字典序，
// 而 merge 时新构造的是追加序；顺序敏感比较会把纯顺序差误判为"有变化"刷无效 diff。
function stringArraysEqual(a, b) {
  const sort = list => [...(list || [])].map(String).sort()
  return JSON.stringify(sort(a)) === JSON.stringify(sort(b))
}

/**
 * 归一化证据的浮点字段精度，消除上游重算产生的尾数 diff。
 *
 * registry 受版本管理，全精度浮点（如 rawValue=0.7053571428571429）会让上游
 * 每次重算都产生亚千分位抖动，制造无意义 diff。统一舍入：
 * - 比率型字段（rawValue 为 0-1 的 pass_at_1、cohortPercentile、uncertainty）→ 3 位小数（0.1% 精度）
 * - 指数分型 rawValue（artificial_analysis 的 0-100 index）→ 1 位小数（0.1 分）
 * - costUsd 已在 dradar 注入处单独舍入到 2 位小数（0.01 美元），此处不重复处理
 *
 * 必须在 evidenceListsEqual 比较与写回 registry 之前调用，否则新数据（全精度）
 * 与已舍入的旧 registry 会因尾差被误判为"有变化"。
 */
function normalizeEvidencePrecision(evidence) {
  if (!Array.isArray(evidence)) return evidence
  const round = (v, digits) => {
    if (typeof v !== 'number' || !Number.isFinite(v)) return v
    const f = 10 ** digits
    return Math.round(v * f) / f
  }
  for (const e of evidence) {
    if (!e || typeof e !== 'object') continue
    // rawValue：比率（<=1）按 3 位；指数分（>1，如 0-100）按 1 位
    if (e.rawValue !== undefined) {
      e.rawValue = e.rawValue <= 1 ? round(e.rawValue, 3) : round(e.rawValue, 1)
    }
    if (e.cohortPercentile !== undefined) e.cohortPercentile = round(e.cohortPercentile, 3)
    if (e.uncertainty !== undefined) e.uncertainty = round(e.uncertainty, 3)
  }
  return evidence
}

function ensureEvidenceProfileMetadata(profile) {
  const evidence = profile.benchmarkEvidence || []
  const sourceURLs = evidence.map(item => item.sourceUrl).filter(Boolean)
  profile.sources = [...new Set([...(profile.sources || []), ...sourceURLs])]

  const cohortSize = Math.max(0, ...evidence.map(item => Number(item.cohortSize) || 0))
  const domainCount = new Set(evidence.map(item => item.domain).filter(Boolean)).size
  profile.sharedResults = Math.max(Number(profile.sharedResults) || 0, cohortSize, 1)
  profile.comparableCategories = Math.max(Number(profile.comparableCategories) || 0, domainCount, 1)
  profile.totalCategories = Math.max(
    Number(profile.totalCategories) || 0,
    profile.comparableCategories,
  )
}

/**
 * 合并 deepswe 数据到注册表
 */
export function mergeDeepsweData(registry, deepsweData, report, models = targetModels) {
  if (!registry.benchmarkProfiles) {
    registry.benchmarkProfiles = []
  }

  for (const [canonical, data] of Object.entries(deepsweData)) {
    if (models && !models.includes(canonical)) {
      continue
    }

    const idx = findProfileIndex(registry.benchmarkProfiles, canonical)
    const profile = idx >= 0
      ? registry.benchmarkProfiles[idx]
      : createProfile(
          canonical,
          deepsweModelToPattern(data.deepsweMeta?.deepsweModel) || canonicalModelToPattern(canonical),
        )

    // 确保 benchmarkEvidence 存在
    if (!profile.benchmarkEvidence) {
      profile.benchmarkEvidence = []
    }

    // 移除旧的 deepswe 证据，与新证据合并
    const nextEvidence = normalizeEvidencePrecision([
      ...profile.benchmarkEvidence.filter(e => e.benchmark !== 'deepswe'),
      ...data.benchmarkEvidence,
    ])

    // 数据未变化（忽略 capturedAt）时跳过，不刷 verifiedAt，避免无效 diff
    if (idx >= 0 && evidenceListsEqual(nextEvidence, profile.benchmarkEvidence)) {
      report.unchanged.push({ canonical, source: 'deepswe' })
      continue
    }

    profile.benchmarkEvidence = nextEvidence
    ensureEvidenceProfileMetadata(profile)

    // 更新 verifiedAt
    profile.verifiedAt = new Date().toISOString().split('T')[0]

    // 更新或插入
    if (idx >= 0) {
      registry.benchmarkProfiles[idx] = profile
      report.updated.push({ canonical, source: 'deepswe' })
    } else {
      registry.benchmarkProfiles.push(profile)
      report.added.push({ canonical, source: 'deepswe' })
    }
  }
}

/**
 * 合并 benchlm.ai 数据到注册表
 */
export function mergeBenchlmData(registry, benchlmData, report, models = targetModels) {
  if (!registry.benchmarkProfiles) {
    registry.benchmarkProfiles = []
  }

  for (const [canonical, data] of Object.entries(benchlmData)) {
    if (models && !models.includes(canonical)) {
      continue
    }

    const categoryCount = Object.keys(data.categoryScores || {}).length
    const idx = findProfileIndex(registry.benchmarkProfiles, canonical)
    if (idx < 0 && categoryCount === 0) {
      continue
    }
    const profile = idx >= 0 ? registry.benchmarkProfiles[idx] : createProfile(canonical)

    // 更新 overallScore
    if (data.overallScore !== null && data.overallScore !== undefined) {
      profile.overallScore = data.overallScore
    }

    // 更新 categoryScores
    if (categoryCount > 0) {
      profile.categoryScores = data.categoryScores
    }

    // 不让缺少可比分组的页面用 0 覆盖其他来源的有效元数据
    if (data.counts) {
      const sharedResults = Number(data.counts.sharedBenchmarkCount) || 0
      const comparableCategories = Math.max(
        Number(data.counts.comparableCategoryCount) || 0,
        categoryCount,
      )
      const totalCategories = Number(data.counts.totalCategoryCount) || 0
      profile.sharedResults = sharedResults > 0 ? sharedResults : Math.max(Number(profile.sharedResults) || 0, 1)
      profile.comparableCategories = comparableCategories > 0
        ? comparableCategories
        : Math.max(Number(profile.comparableCategories) || 0, 1)
      profile.totalCategories = Math.max(
        totalCategories > 0 ? totalCategories : Number(profile.totalCategories) || 0,
        profile.comparableCategories,
      )
    }

    // 更新 sources：benchlm 来源由本次数据全量替换（从对比页迁移到模型页），
    // 保留其他来源（deepswe/dradar）。
    if (data.sources && data.sources.length > 0) {
      const existingSources = profile.sources || []
      const nonBenchlm = existingSources.filter(s => !s.startsWith('https://benchlm.ai/'))
      profile.sources = [...new Set([...nonBenchlm, ...data.sources])]
    }
    ensureEvidenceProfileMetadata(profile)

    // 更新 verifiedAt
    profile.verifiedAt = new Date().toISOString().split('T')[0]

    // 更新或插入
    if (idx >= 0) {
      registry.benchmarkProfiles[idx] = profile
      report.updated.push({ canonical, source: 'benchlm' })
    } else {
      registry.benchmarkProfiles.push(profile)
      report.added.push({ canonical, source: 'benchlm' })
    }
  }
}

/**
 * 合并 dradar 数据到注册表
 */
export function mergeDradarData(registry, dradarData, report, models = targetModels) {
  if (!registry.benchmarkProfiles) {
    registry.benchmarkProfiles = []
  }

  for (const [canonical, data] of Object.entries(dradarData)) {
    if (models && !models.includes(canonical)) {
      continue
    }

    const idx = findProfileIndex(registry.benchmarkProfiles, canonical)
    const profile = idx >= 0 ? registry.benchmarkProfiles[idx] : createProfile(canonical)

    // 确保 benchmarkEvidence 存在
    if (!profile.benchmarkEvidence) {
      profile.benchmarkEvidence = []
    }

    // 移除当前及旧格式的 codexradar 证据，与新证据合并
    const nextEvidence = normalizeEvidencePrecision([
      ...profile.benchmarkEvidence.filter(
        e => e.benchmark !== 'codexradar' &&
          !(e.benchmark === 'deepswe' && e.benchmarkVersion === 'codexradar')
      ),
      ...data.benchmarkEvidence,
    ])

    // 数据未变化（忽略 capturedAt）时跳过，不刷 verifiedAt，避免无效 diff
    if (idx >= 0 && evidenceListsEqual(nextEvidence, profile.benchmarkEvidence)) {
      report.unchanged.push({ canonical, source: 'dradar' })
      continue
    }

    profile.benchmarkEvidence = nextEvidence
    ensureEvidenceProfileMetadata(profile)

    // costData 保留供可视化使用，实测 cost 已随 benchmarkEvidence 进入注册表
    if (profile.costData) {
      delete profile.costData
    }

    // 更新 verifiedAt
    profile.verifiedAt = new Date().toISOString().split('T')[0]

    // 更新或插入
    if (idx >= 0) {
      registry.benchmarkProfiles[idx] = profile
      report.updated.push({ canonical, source: 'dradar' })
    } else {
      registry.benchmarkProfiles.push(profile)
      report.added.push({ canonical, source: 'dradar' })
    }
  }
}

/**
 * 合并 litellm 数据到注册表（更新 upstreamCapabilities 的 pricing/contextWindow）
 */
export function mergeLitellmData(registry, litellmData, report, models = targetModels) {
  if (!registry.upstreamCapabilities) {
    registry.upstreamCapabilities = []
  }

  for (const [canonical, data] of Object.entries(litellmData)) {
    if (models && !models.includes(canonical)) {
      continue
    }

    // 查找现有的 upstreamCapability
    const capIdx = registry.upstreamCapabilities.findIndex(c => {
      const patterns = c.patterns || []
      return patterns.some(p => {
        // 检查 pattern 是否匹配 canonical 模型名
        try {
          const regex = new RegExp(p, 'i')
          return regex.test(canonical)
        } catch {
          return false
        }
      })
    })

    if (capIdx >= 0) {
      const cap = registry.upstreamCapabilities[capIdx]

      // 更新 contextWindowTokens
      if (data.contextWindowTokens && !cap.contextWindowTokens) {
        cap.contextWindowTokens = data.contextWindowTokens
        report.litellmUpdated.push({ canonical, field: 'contextWindowTokens', value: data.contextWindowTokens })
      }

      // 更新 maxOutputTokens
      if (data.maxOutputTokens && !cap.maxOutputTokens) {
        cap.maxOutputTokens = data.maxOutputTokens
        report.litellmUpdated.push({ canonical, field: 'maxOutputTokens', value: data.maxOutputTokens })
      }

      // 更新 pricing（如果 litellm 有数据）
      if (data.pricing && Object.values(data.pricing).some(v => v !== null)) {
        if (!cap.pricing) {
          cap.pricing = { unit: 'per_1m_tokens_usd', currency: 'USD' }
        }
        if (data.pricing.inputCacheMissPrice !== null) {
          cap.pricing.inputCacheMissPrice = data.pricing.inputCacheMissPrice
        }
        if (data.pricing.outputPrice !== null) {
          cap.pricing.outputPrice = data.pricing.outputPrice
        }
        if (data.pricing.inputCacheHitPrice !== null) {
          cap.pricing.inputCacheHitPrice = data.pricing.inputCacheHitPrice
        }
        report.litellmUpdated.push({ canonical, field: 'pricing', value: 'updated' })
      }

      // 更新 capabilities
      if (data.supports) {
        if (!cap.capabilities) {
          cap.capabilities = {}
        }
        let capabilitiesUpdated = false
        for (const field of ['reasoning', 'vision', 'toolCalls', 'parallelFunctionCalling', 'webSearch']) {
          if (data.supports[field] !== undefined && cap.capabilities[field] === undefined) {
            cap.capabilities[field] = data.supports[field]
            capabilitiesUpdated = true
          }
        }
        if (capabilitiesUpdated) {
          report.litellmUpdated.push({ canonical, field: 'capabilities', value: 'filled missing values' })
        }
      }

      registry.upstreamCapabilities[capIdx] = cap
    } else {
      report.litellmSkipped.push({ canonical, reason: 'not found in upstreamCapabilities' })
    }
  }
}

/**
 * 合并 Artificial Analysis LLM 复合指数到 benchmarkProfile.benchmarkEvidence。
 * 仿 mergeDradarData：全量替换 artificial_analysis 证据，合并 sources，更新 verifiedAt。
 * 刻意不碰 overallScore/categoryScores（benchlm 保留所有权，避免不同量表互相覆盖）。
 */
export function mergeArtificialAnalysisLlm(registry, aaLlmData, report, models = targetModels) {
  if (!registry.benchmarkProfiles) {
    registry.benchmarkProfiles = []
  }

  for (const [canonical, data] of Object.entries(aaLlmData)) {
    if (models && !models.includes(canonical)) {
      continue
    }

    const idx = findProfileIndex(registry.benchmarkProfiles, canonical)
    const profile = idx >= 0 ? registry.benchmarkProfiles[idx] : createProfile(canonical)

    if (!profile.benchmarkEvidence) {
      profile.benchmarkEvidence = []
    }

    // 移除旧的 AA 证据，与新证据合并
    const nextEvidence = normalizeEvidencePrecision([
      ...profile.benchmarkEvidence.filter(e => e.benchmark !== 'artificial_analysis'),
      ...(data.benchmarkEvidence || []),
    ])

    // 合并 sources（保留其他来源，追加 AA model 页）
    let nextSources = profile.sources || []
    if (data.aaMeta?.slug) {
      const aaUrl = `https://artificialanalysis.ai/models/${data.aaMeta.slug}`
      const nonAA = nextSources.filter(s => !s.startsWith('https://artificialanalysis.ai/'))
      nextSources = [...new Set([...nonAA, aaUrl])]
    }

    // 证据和来源都未变化（忽略 capturedAt）时跳过，不刷 verifiedAt，避免无效 diff
    if (
      idx >= 0 &&
      evidenceListsEqual(nextEvidence, profile.benchmarkEvidence) &&
      stringArraysEqual(nextSources, profile.sources)
    ) {
      report.unchanged.push({ canonical, source: 'artificial-analysis' })
      continue
    }

    profile.benchmarkEvidence = nextEvidence
    ensureEvidenceProfileMetadata(profile)
    profile.sources = nextSources

    profile.verifiedAt = new Date().toISOString().split('T')[0]

    if (idx >= 0) {
      registry.benchmarkProfiles[idx] = profile
      report.updated.push({ canonical, source: 'artificial-analysis' })
    } else {
      registry.benchmarkProfiles.push(profile)
      report.added.push({ canonical, source: 'artificial-analysis' })
    }
  }
}

/**
 * 合并 Artificial Analysis 图像 arena Elo 到顶层 imageArenaProfiles section。
 * 按 canonical 全量替换 elo/ci95/sources/verifiedAt。
 */
export function mergeArtificialAnalysisImageArena(registry, aaImageData, report, models = targetModels) {
  if (!registry.imageArenaProfiles) {
    registry.imageArenaProfiles = []
  }

  for (const [canonical, data] of Object.entries(aaImageData)) {
    if (models && !models.includes(canonical)) {
      continue
    }

    const idx = registry.imageArenaProfiles.findIndex(p => p.canonicalModel === canonical)
    const arena = idx >= 0
      ? registry.imageArenaProfiles[idx]
      : {
          patterns: [canonicalModelToPattern(canonical)],
          canonicalModel: canonical,
          lane: 'provisional',
        }

    const nextCi95 = Object.prototype.hasOwnProperty.call(data, 'ci95') ? data.ci95 : arena.ci95
    const nextSources = data.sources && data.sources.length > 0 ? [...data.sources] : arena.sources

    // elo/ci95/sources 都未变化时跳过，不刷 verifiedAt，避免无效 diff
    if (
      idx >= 0 &&
      arena.elo === data.elo &&
      arena.ci95 === nextCi95 &&
      stringArraysEqual(arena.sources, nextSources)
    ) {
      report.unchanged.push({ canonical, source: 'artificial-analysis-image-arena' })
      continue
    }

    arena.elo = data.elo
    if (Object.prototype.hasOwnProperty.call(data, 'ci95')) {
      arena.ci95 = data.ci95
    }
    if (data.sources && data.sources.length > 0) {
      arena.sources = nextSources
    }
    arena.verifiedAt = new Date().toISOString().split('T')[0]

    if (idx >= 0) {
      registry.imageArenaProfiles[idx] = arena
      report.aaImageArenaUpdated.push({ canonical, elo: data.elo })
    } else {
      registry.imageArenaProfiles.push(arena)
      report.aaImageArenaAdded.push({ canonical, elo: data.elo })
    }
  }
}

export function validateRegistry(registry) {
  for (const [index, profile] of (registry.benchmarkProfiles || []).entries()) {
    const prefix = `benchmarkProfiles[${index}]`
    if (!profile.canonicalModel || !Array.isArray(profile.patterns) || profile.patterns.length === 0) {
      throw new Error(`${prefix} is missing canonicalModel or patterns`)
    }
    if (profile.patterns.some(pattern => typeof pattern !== 'string' || pattern.trim() === '')) {
      throw new Error(`${prefix} contains an empty pattern`)
    }
    // provisional lane 允许作为纯占位条目存在（已发布但尚未被任何榜单收录），
    // 此时可以没有 categoryScores、benchmarkEvidence 和 sources。
    if (profile.lane !== 'provisional') {
      if ((!profile.categoryScores || Object.keys(profile.categoryScores).length === 0) &&
          (!profile.benchmarkEvidence || profile.benchmarkEvidence.length === 0)) {
        throw new Error(`${prefix} requires categoryScores or benchmarkEvidence`)
      }
      if (!Array.isArray(profile.sources) || profile.sources.length === 0) {
        throw new Error(`${prefix} requires at least one source`)
      }
    }
    if (!/^\d{4}-\d{2}-\d{2}$/.test(profile.verifiedAt || '')) {
      throw new Error(`${prefix}.verifiedAt must use YYYY-MM-DD`)
    }
    if (!['provisional', 'verified'].includes(profile.lane)) {
      throw new Error(`${prefix}.lane is invalid`)
    }
    for (const field of ['sharedResults', 'comparableCategories', 'totalCategories']) {
      if (!Number.isFinite(profile[field]) || profile[field] <= 0) {
        throw new Error(`${prefix}.${field} must be positive`)
      }
    }
    if (profile.comparableCategories > profile.totalCategories) {
      throw new Error(`${prefix}.comparableCategories exceeds totalCategories`)
    }
  }

  // 图像 arena profile 校验（顶层 imageArenaProfiles section）
  for (const [index, arena] of (registry.imageArenaProfiles || []).entries()) {
    const prefix = `imageArenaProfiles[${index}]`
    if (!arena.canonicalModel || !Array.isArray(arena.patterns) || arena.patterns.length === 0) {
      throw new Error(`${prefix} is missing canonicalModel or patterns`)
    }
    if (!Number.isFinite(arena.elo)) {
      throw new Error(`${prefix}.elo must be a finite number`)
    }
    if (!Array.isArray(arena.sources) || arena.sources.length === 0) {
      throw new Error(`${prefix} requires at least one source`)
    }
    if (!/^\d{4}-\d{2}-\d{2}$/.test(arena.verifiedAt || '')) {
      throw new Error(`${prefix}.verifiedAt must use YYYY-MM-DD`)
    }
    if (!['provisional', 'verified'].includes(arena.lane)) {
      throw new Error(`${prefix}.lane is invalid`)
    }
  }
}

/**
 * 运行代码生成
 */
function runCodeGeneration() {
  console.log('\n[generate] Running generate-model-registry.mjs (code and presets)...')
  try {
    execFileSync('node', [join(root, 'scripts/generate-model-registry.mjs')], {
      stdio: 'inherit',
      cwd: root,
    })
    console.log('[generate] Done')
  } catch (err) {
    console.error('[generate] Failed:', err.message)
    throw err
  }
}

function generateBenchmarkChart(data) {
  atomicWrite(chartDataPath, JSON.stringify(data, null, 2) + '\n')
  console.log('\n[chart] Generating multi-source benchmark chart...')
  execFileSync('node', [
    join(root, 'scripts/generate-benchmark-chart.mjs'),
    '--input', chartDataPath,
    '--output', chartOutputPath,
  ], {
    stdio: 'inherit',
    cwd: root,
  })
}

function saveAndGenerateAtomically(registry) {
  const trackedPaths = [registryPath, ...generatedArtifactPaths]
  const snapshots = new Map(
    trackedPaths
      .filter(path => existsSync(path))
      .map(path => [path, readFileSync(path, 'utf8')]),
  )

  try {
    saveRegistry(registry)
    runCodeGeneration()
  } catch (error) {
    for (const [path, content] of snapshots) {
      atomicWrite(path, content)
    }
    for (const path of trackedPaths) {
      if (!snapshots.has(path) && existsSync(path)) {
        unlinkSync(path)
      }
    }
    throw error
  }
}

/**
 * 主函数
 */
export async function main() {
  console.log('='.repeat(60))

  if (skipDeepswe && skipBenchlm && skipDradar && skipLitellm && !artificialAnalysisEnabled) {
    throw new Error('all benchmark sources are skipped')
  }
  console.log('CCX Benchmark Data Auto-Updater')
  console.log('='.repeat(60))
  console.log(`Mode: ${dryRun ? 'DRY RUN' : 'UPDATE'}`)
  console.log(`Registry: ${registryPath}`)
  console.log(`Skip deepswe: ${skipDeepswe}`)
  console.log(`Skip benchlm: ${skipBenchlm}`)
  console.log(`Skip dradar: ${skipDradar}`)
  console.log(`Skip litellm: ${skipLitellm}`)
  console.log(`Skip artificial-analysis: ${skipArtificialAnalysis || !artificialAnalysisEnabled}`)
  if (targetModels) {
    console.log(`Target models: ${targetModels.join(', ')}`)
  }
  console.log('='.repeat(60))

  const registry = loadRegistry()
  const report = {
    updated: [],
    added: [],
    unchanged: [],
    errors: [],
    litellmUpdated: [],
    litellmSkipped: [],
    aaUpdated: [],
    aaAdded: [],
    aaImageArenaUpdated: [],
    aaImageArenaAdded: [],
    aaSkipped: false,
  }
  const visualizationSources = {
    deepsweProfiles: {},
    deepsweLeaderboard: null,
    benchlmProfiles: {},
    dradarProfiles: {},
    artificialAnalysisProfiles: {},
    artificialAnalysisImageArena: {},
  }

  // 抓取 deepswe 数据
  if (!skipDeepswe) {
    try {
      console.log('\n--- Fetching deepswe data ---')
      const deepsweDataset = await fetchDeepsweDataset(DEEPSWE_MODEL_MAP)
      visualizationSources.deepsweProfiles = deepsweDataset.profiles
      visualizationSources.deepsweLeaderboard = deepsweDataset.liveLeaderboard
      mergeDeepsweData(registry, deepsweDataset.profiles, report)
    } catch (err) {
      report.errors.push({ source: 'deepswe', error: err.message })
      console.error('[deepswe] Failed:', err.message)
    }
  }

  // 抓取 benchlm.ai 数据
  if (!skipBenchlm) {
    try {
      console.log('\n--- Fetching benchlm.ai data ---')
      const benchlmResult = await fetchBenchlmData(BENCHLM_MODEL_MAP, BENCHLM_CATEGORY_MAP)
      // data 已是干净的 profiles（无内部统计字段），直接喂可视化和合并逻辑
      const cleanData = { ...benchlmResult.data }
      visualizationSources.benchlmProfiles = cleanData
      const isUnchanged = (benchlmResult.unchanged?.length ?? 0) > 0
      // 仅在数据变更时 merge（避免每次刷 verifiedAt）；未变更时仍用缓存 profiles 喂图表
      if (!isUnchanged && Object.keys(cleanData).length > 0) {
        mergeBenchlmData(registry, cleanData, report)
      }
      if (isUnchanged) {
        console.log(`[benchlm] ${benchlmResult.unchanged[0]}, skipping merge`)
        report.benchlmUnchanged = benchlmResult.unchanged[0]
      }
    } catch (err) {
      report.errors.push({ source: 'benchlm', error: err.message })
      console.error('[benchlm] Failed:', err.message)
    }
  }

  // 抓取 dradar (codexradar) 数据
  if (!skipDradar) {
    try {
      console.log('\n--- Fetching dradar (codexradar) data ---')
      const dradarData = await fetchDradarData(DRADAR_MODEL_MAP)
      visualizationSources.dradarProfiles = dradarData
      mergeDradarData(registry, dradarData, report)
    } catch (err) {
      report.errors.push({ source: 'dradar', error: err.message })
      console.error('[dradar] Failed:', err.message)
    }
  }

  // 抓取 litellm 数据
  if (!skipLitellm) {
    try {
      console.log('\n--- Fetching litellm pricing/context data ---')
      const litellmData = await fetchLitellmModelInfo(LITELLM_MODEL_MAP, forceLitellm)
      if (!litellmData._unchanged) {
        mergeLitellmData(registry, litellmData, report)
      } else {
        console.log('[litellm] No changes, skipping merge')
        report.litellmUnchanged = true
      }
    } catch (err) {
      report.errors.push({ source: 'litellm', error: err.message })
      console.error('[litellm] Failed:', err.message)
    }
  }

  // 抓取 Artificial Analysis 数据（首个需 API key 的源；无 key 自动跳过，不计错误）
  if (skipArtificialAnalysis) {
    console.log('\n--- Skipping Artificial Analysis (--skip-artificial-analysis) ---')
    report.aaSkipped = true
  } else if (!artificialAnalysisApiKey) {
    console.log('\n--- Skipping Artificial Analysis (ARTIFICIAL_ANALYSIS_API_KEY not set) ---')
    console.log('[artificial-analysis] Set ARTIFICIAL_ANALYSIS_API_KEY to enable, or pass --skip-artificial-analysis to silence this message.')
    report.aaSkipped = true
  } else {
    try {
      console.log('\n--- Fetching Artificial Analysis data ---')
      const aaResult = await fetchArtificialAnalysisData(
        artificialAnalysisApiKey,
        ARTIFICIAL_ANALYSIS_MODEL_MAP,
        ARTIFICIAL_ANALYSIS_IMAGE_MODEL_MAP,
      )
      visualizationSources.artificialAnalysisProfiles = aaResult.llm
      visualizationSources.artificialAnalysisImageArena = aaResult.imageArena
      mergeArtificialAnalysisLlm(registry, aaResult.llm, report)
      mergeArtificialAnalysisImageArena(registry, aaResult.imageArena, report)

      // 首跑调参提示：列出未命中的 AA slug（前 20 个）
      const logUnmapped = (label, slugs) => {
        if (!slugs || slugs.length === 0) return
        const preview = slugs.slice(0, 20).join(', ')
        const more = slugs.length > 20 ? ` ... and ${slugs.length - 20} more` : ''
        console.log(`[artificial-analysis] ${label} unmapped slugs (${slugs.length}): ${preview}${more}`)
      }
      logUnmapped('LLM', aaResult.unmappedLlmSlugs)
      logUnmapped('Image arena', aaResult.unmappedImageSlugs)
    } catch (err) {
      report.errors.push({ source: 'artificial-analysis', error: err.message })
      console.error('[artificial-analysis] Failed:', err.message)
    }
  }

  if (report.errors.length > 0) {
    const failedSources = report.errors.map(item => item.source).join(', ')
    throw new Error(`enabled sources failed (${failedSources}); registry was not changed`)
  }

  validateRegistry(registry)

  // 保存注册表
  if (!dryRun) {
    console.log('\n--- Saving registry ---')
    saveAndGenerateAtomically(registry)
    console.log(`[save] Registry and generated artifacts updated atomically`)
  } else {
    console.log('\n--- DRY RUN: No changes saved ---')
  }

  const visualizationData = buildBenchmarkVisualizationData({
    ...visualizationSources,
    modelMap: DEEPSWE_MODEL_MAP,
    models: targetModels,
  })
  if (visualizationData.data.length > 0 || visualizationData.comparisons.length > 0) {
    generateBenchmarkChart(visualizationData)
  } else {
    console.log('\n[chart] No benchmark data available; chart generation skipped')
  }

  // 输出报告
  console.log('\n' + '='.repeat(60))
  console.log('UPDATE REPORT')
  console.log('='.repeat(60))
  console.log(`Updated profiles: ${report.updated.length}`)
  for (const u of report.updated) {
    console.log(`  - ${u.canonical} (${u.source})`)
  }
  console.log(`Added profiles: ${report.added.length}`)
  for (const a of report.added) {
    console.log(`  + ${a.canonical} (${a.source})`)
  }
  if (report.unchanged.length > 0) {
    console.log(`Unchanged profiles (skipped, dates preserved): ${report.unchanged.length}`)
  }
  if (report.errors.length > 0) {
    console.log(`Errors: ${report.errors.length}`)
    for (const e of report.errors) {
      console.log(`  ! ${e.source}: ${e.error}`)
    }
  }
  if (report.litellmUpdated.length > 0) {
    console.log(`\nlitellm updates: ${report.litellmUpdated.length}`)
    for (const u of report.litellmUpdated.slice(0, 10)) {
      console.log(`  - ${u.canonical}.${u.field}: ${u.value}`)
    }
    if (report.litellmUpdated.length > 10) {
      console.log(`  ... and ${report.litellmUpdated.length - 10} more`)
    }
  }
  if (report.litellmUnchanged) {
    console.log(`\nlitellm: unchanged, skipped`)
  }
  if (report.benchlmUnchanged) {
    console.log(`\nbenchlm: ${report.benchlmUnchanged}`)
  }
  if (report.litellmSkipped.length > 0) {
    console.log(`\nlitellm skipped: ${report.litellmSkipped.length}`)
    for (const s of report.litellmSkipped.slice(0, 5)) {
      console.log(`  - ${s.canonical}: ${s.reason}`)
    }
  }
  if (report.aaSkipped) {
    console.log(`\nartificial-analysis: skipped (no API key or --skip-artificial-analysis)`)
  }
  if (report.aaUpdated.length > 0 || report.aaAdded.length > 0 || report.aaImageArenaUpdated.length > 0 || report.aaImageArenaAdded.length > 0) {
    console.log(`\nartificial-analysis LLM: ${report.aaUpdated.length} updated, ${report.aaAdded.length} added`)
    for (const u of report.aaUpdated.slice(0, 10)) console.log(`  - ${u.canonical}`)
    for (const a of report.aaAdded.slice(0, 10)) console.log(`  + ${a.canonical}`)
    console.log(`artificial-analysis image arena: ${report.aaImageArenaUpdated.length} updated, ${report.aaImageArenaAdded.length} added`)
    for (const u of report.aaImageArenaUpdated.slice(0, 10)) console.log(`  - ${u.canonical} (Elo ${u.elo})`)
    for (const a of report.aaImageArenaAdded.slice(0, 10)) console.log(`  + ${a.canonical} (Elo ${a.elo})`)
  }
  console.log('='.repeat(60))

  // 持久化 HTTP 缓存
  saveCache()

  if (dryRun) {
    console.log('\nTo apply changes, run without --dry-run')
  }
}

const invokedPath = process.argv[1] ? resolve(process.argv[1]) : ''
if (invokedPath === fileURLToPath(import.meta.url)) {
  main().catch(err => {
    console.error('Fatal error:', err)
    process.exit(1)
  })
}
