/**
 * 官方公告评测分数预填充脚本（可重复执行，幂等）
 *
 * 读取 scripts/benchmark-sources/official-releases.mjs 的官方公告分数，
 * 对公告表内同基准、registry 已有 DeepSWE coding 直测（medium 档）的锚点模型
 * 做锚点序数插值折算：
 *
 *   equiv = 分段线性插值(target_official; 锚点按官方分排序, y=锚点 deepswe medium)
 *   - 高于最高锚点 → 截断到最高锚点 deepswe（保守，防适配失真外推）
 *   - 低于最低锚点 → 按最低两锚点斜率外推，clamp [0,1]
 *
 * 产出 DeepSWE 等价分（benchmark=official_release, metric=deepswe_equivalent,
 * effort=medium 口径）供 autopilot calibrateOfficialReleaseEffort 消费
 * （EvidenceCalibrated，封顶 high）；同时写入公告原始分作展示证据。
 *
 * 用法：node scripts/prefill-official-scores.mjs [--dry-run]
 */

import { readFileSync, writeFileSync } from 'node:fs'
import {
  OFFICIAL_RELEASE_ANNOUNCEMENTS,
  OFFICIAL_RELEASE_ANCHOR_MODELS,
} from './benchmark-sources/official-releases.mjs'

const REGISTRY_PATH = 'shared/model-registry/ccx_model_registry.json'
const EQUIV_BENCHMARK = 'official_release'
const EQUIV_METRIC = 'deepswe_equivalent'
const MIN_ANCHORS = 2
const dryRun = process.argv.includes('--dry-run')

const registry = JSON.parse(readFileSync(REGISTRY_PATH, 'utf8'))
const profilesByCanonical = new Map(
  (registry.benchmarkProfiles || []).map(p => [p.canonicalModel, p]),
)
// 公告按 key 索引，供 anchorPoolFrom 跨公告复用锚点池（同 runner 口径已交叉验证时）
const announcementsByKey = OFFICIAL_RELEASE_ANNOUNCEMENTS

function deepsweMediumScore(profile) {
  const hit = (profile.benchmarkEvidence || []).find(
    e => e.benchmark === 'deepswe' && e.domain === 'coding'
      && e.metric === 'pass_at_1' && e.effort === 'medium',
  )
  return hit ? hit.rawValue : null
}

function hasAACodingIndex(profile) {
  return (profile.benchmarkEvidence || []).some(
    e => e.benchmark === 'artificial_analysis' && e.domain === 'coding'
      && e.metric === 'coding_index',
  )
}

function median(values) {
  const sorted = [...values].sort((a, b) => a - b)
  const mid = Math.floor(sorted.length / 2)
  return sorted.length % 2 ? sorted[mid] : (sorted[mid - 1] + sorted[mid]) / 2
}

function makeRawEvidence(entry, sourceUrl, capturedAt) {
  return {
    benchmark: entry.benchmark,
    benchmarkVersion: entry.benchmarkVersion,
    sourceModel: entry.model,
    domain: entry.domain,
    metric: entry.metric,
    rawValue: entry.rawValue,
    uncertainty: 0,
    cohortPercentile: 0,
    taskCount: 1,
    cohortSize: 1,
    effort: entry.effort,
    selectionBasis: 'official_release_announcement',
    sourceUrl,
    capturedAt,
  }
}

function makeEquivEvidence(entry, equiv, anchorSummary, sourceUrl, capturedAt) {
  return {
    benchmark: EQUIV_BENCHMARK,
    benchmarkVersion: entry.benchmarkVersion,
    sourceModel: entry.model,
    domain: 'coding',
    metric: EQUIV_METRIC,
    rawValue: Math.round(equiv * 1000) / 1000,
    uncertainty: 0,
    cohortPercentile: 0,
    taskCount: 1,
    cohortSize: 1,
    effort: 'medium',
    selectionBasis: `anchor_interp raw=${entry.rawValue}@${entry.benchmark}@${entry.effort} ${anchorSummary}`,
    sourceUrl,
    capturedAt,
  }
}

/**
 * 锚点序数插值：按官方分排序锚点（x=官方分, y=deepswe medium 直测分），
 * 对 target 官方分分段线性插值；高于最高锚点截断（保守），低于最低锚点按
 * 最低两锚点斜率外推并 clamp [0,1]——外推斜率有界（rawValue 为 0-1 尺度，
 * 正常锚点池斜率 0.5~1.7/unit；上界 2.0 仅截断锚点 x 值过近导致的斜率爆炸，
 * 如 stepfun DeepSWE 池两锚点 x 差 0.001 时斜率达 39/unit）。锚点 < 2 返回 null。
 */
const MAX_EXTRAPOLATION_SLOPE = 2.0

function interpolateByAnchors(targetOfficial, anchors) {
  if (anchors.length < MIN_ANCHORS) return null
  const sorted = [...anchors].sort((a, b) => a.official - b.official)
  if (targetOfficial >= sorted[sorted.length - 1].official) {
    return sorted[sorted.length - 1].deepswe
  }
  if (targetOfficial <= sorted[0].official) {
    if (sorted.length < 2) return sorted[0].deepswe
    const rawSlope = (sorted[1].deepswe - sorted[0].deepswe) / (sorted[1].official - sorted[0].official)
    const slope = Math.min(MAX_EXTRAPOLATION_SLOPE, Math.max(-MAX_EXTRAPOLATION_SLOPE, rawSlope))
    return Math.min(1, Math.max(0, sorted[0].deepswe + slope * (targetOfficial - sorted[0].official)))
  }
  for (let i = 1; i < sorted.length; i++) {
    if (targetOfficial <= sorted[i].official) {
      const { official: x0, deepswe: y0 } = sorted[i - 1]
      const { official: x1, deepswe: y1 } = sorted[i]
      return y0 + (y1 - y0) * ((targetOfficial - x0) / (x1 - x0))
    }
  }
  return sorted[sorted.length - 1].deepswe
}

let addedRaw = 0
let addedEquiv = 0
let skippedAnchors = 0

for (const [announcementKey, announcement] of Object.entries(OFFICIAL_RELEASE_ANNOUNCEMENTS)) {
  const { sourceUrl, capturedAt, scores } = announcement

  // 按基准分组
  const byBenchmark = new Map()
  for (const entry of scores) {
    if (!byBenchmark.has(entry.benchmark)) byBenchmark.set(entry.benchmark, [])
    byBenchmark.get(entry.benchmark).push(entry)
  }

  // 跨基准折算目标收集：coding 基准且该模型自身无 deepswe 直测（锚点不折算，保留直测精度）
  // anchorPoolFrom：本公告 registry 有直测的锚点不足时，复用指定公告的同基准锚点
  // （仅限已交叉验证同 runner 口径的组合，如 xAI 表与 Anthropic 表的 Sol 数字一致）。
  const anchorPool = announcement.anchorPoolFrom
    ? announcementsByKey[announcement.anchorPoolFrom]
    : null
  if (announcement.anchorPoolFrom && !anchorPool) {
    throw new Error(`anchorPoolFrom 指向不存在的公告: ${announcement.anchorPoolFrom}`)
  }
  const targets = new Map()
  for (const [benchmark, entries] of byBenchmark) {
    // 锚点对：锚点模型在该基准有官方分且 registry 有 deepswe medium 直测
    const anchorEntries = (anchorPool || announcement).scores.filter(e => e.benchmark === benchmark)
    const anchors = []
    for (const entry of anchorEntries) {
      if (!OFFICIAL_RELEASE_ANCHOR_MODELS.includes(entry.model)) continue
      const profile = profilesByCanonical.get(entry.model)
      const deepswe = profile ? deepsweMediumScore(profile) : null
      if (deepswe != null && deepswe > 0) {
        anchors.push({ model: entry.model, official: entry.rawValue, deepswe })
      }
    }

    const canCalibrate = anchors.length >= MIN_ANCHORS
    if (!canCalibrate) skippedAnchors += entries.filter(e => e.domain === 'coding').length

    for (const entry of entries) {
      const profile = profilesByCanonical.get(entry.model)
      if (!profile) {
        console.warn(`[prefill] registry 无 ${entry.model} profile，跳过 ${entry.benchmark}`)
        continue
      }
      if (!profile.benchmarkEvidence) profile.benchmarkEvidence = []

      // 幂等：清掉本 announcement 产出的旧条目再写。
      // 已有 AA coding_index 的模型跳过等价分维护（其质量档由更高优先级的 AA 链
      // 覆盖，official 等价分纯冗余）——不清不写，保留其他公告产的条目；
      // 原始展示分仍照常写入。
      const skipEquiv = hasAACodingIndex(profile)
      const isOurRaw = e => e.benchmark === entry.benchmark
        && e.selectionBasis === 'official_release_announcement' && e.sourceUrl === sourceUrl
      let kept = profile.benchmarkEvidence.filter(e => !isOurRaw(e))
      if (!skipEquiv) {
        kept = kept.filter(e => !(e.benchmark === EQUIV_BENCHMARK
          && e.sourceModel === entry.model && e.sourceUrl === sourceUrl))
      }
      profile.benchmarkEvidence = kept

      profile.benchmarkEvidence.push(makeRawEvidence(entry, sourceUrl, capturedAt))
      addedRaw += 1

      // sources 合并官方公告 URL
      profile.sources = [...new Set([...(profile.sources || []), sourceUrl])]
    }

    // 折算目标：coding 基准且该模型自身无 deepswe 直测（锚点不折算，保留直测精度）
    if (!canCalibrate) continue
    for (const entry of entries) {
      if (entry.domain !== 'coding') continue
      const profile = profilesByCanonical.get(entry.model)
      if (!profile || deepsweMediumScore(profile) != null || hasAACodingIndex(profile)) continue
      const equiv = interpolateByAnchors(entry.rawValue, anchors)
      if (equiv == null) continue
      if (!targets.has(entry.model)) targets.set(entry.model, [])
      targets.get(entry.model).push({
        benchmark: entry.benchmark, benchmarkVersion: entry.benchmarkVersion,
        effort: entry.effort, raw: entry.rawValue, equiv,
        anchorSnapshot: anchors.map(a => `${a.model}:${a.deepswe}`).join(','),
      })
    }
  }

  // 跨基准中位数聚合成单一 medium 口径等价分（一条 evidence，抗单基准失真）
  for (const [model, perBenchmark] of targets) {
    const profile = profilesByCanonical.get(model)
    if (!profile) continue
    const equivs = perBenchmark.map(p => p.equiv)
    const aggregate = median(equivs)
    const detail = perBenchmark.map(p => `${p.raw}@${p.benchmark}@${p.effort}→${p.equiv.toFixed(3)}`).join(' ')
    profile.benchmarkEvidence.push(makeEquivEvidence(
      { model, benchmarkVersion: perBenchmark[0].benchmarkVersion },
      aggregate,
      `anchors=${perBenchmark[0].anchorSnapshot} per-benchmark: ${detail}`,
      sourceUrl,
      capturedAt,
    ))
    addedEquiv += 1
    console.log(`[prefill] ${model} → deepswe_equiv(medium)=${aggregate.toFixed(3)} (${detail})`)
  }
}

if (dryRun) {
  console.log(`[prefill] dry-run: raw=${addedRaw}, equiv=${addedEquiv}, 无锚点基准跳过 coding 条目=${skippedAnchors}（未写入）`)
  process.exit(0)
}

writeFileSync(REGISTRY_PATH, JSON.stringify(registry, null, 2) + '\n')
console.log(`[prefill] 写入 ${REGISTRY_PATH}: raw=${addedRaw}, equiv=${addedEquiv}, 无锚点基准跳过 coding 条目=${skippedAnchors}`)
console.log('[prefill] 提示：请运行 node scripts/generate-model-registry.mjs 同步 presetstore 产物')
