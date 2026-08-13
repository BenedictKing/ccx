import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

import {
  canonicalModelToPattern,
  deepsweModelToPattern,
  DEEPSWE_MODEL_MAP,
  BENCHLM_MODEL_MAP,
} from './mapper.mjs'
import {
  extractBestPerModel as extractDeepSWEBest,
  toBenchmarkEvidence as toDeepSWEEvidence,
} from './deepswe.mjs'
import {
  extractTableCacheVersion,
  extractBestPerModel as extractDradarBest,
  extractLeaderboardFromTable,
  extractCostData,
  toBenchmarkEvidence as toDradarEvidence,
  DRADAR_MODEL_MAP,
} from './dradar.mjs'
import { extractProfiles as extractBenchlmProfiles } from './benchlm.mjs'
import { extractModelInfo, LITELLM_MODEL_MAP } from './litellm.mjs'
import {
  extractLlmProfiles as extractAaLlm,
  extractImageArenaProfiles as extractAaImage,
  ARTIFICIAL_ANALYSIS_MODEL_MAP,
  ARTIFICIAL_ANALYSIS_IMAGE_MODEL_MAP,
} from './artificialanalysis.mjs'
import {
  generatedArtifactPaths,
  warnSourceFailures,
  mergeBenchlmData,
  mergeDeepsweData,
  mergeLitellmData,
  mergeArtificialAnalysisLlm,
  mergeArtificialAnalysisImageArena,
  validateRegistry,
} from '../update-benchmark-data.mjs'
import { presetArtifactPaths } from '../generate-preset-manifest.mjs'
import { buildBenchmarkVisualizationData } from './visualization.mjs'
import { renderBenchmarkChart, validateVisualizationData } from '../generate-benchmark-chart.mjs'

function emptyReport() {
  return {
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
}

function readJson(relativePath) {
  return JSON.parse(readFileSync(new URL(relativePath, import.meta.url), 'utf8'))
}

test('runtime and published preset registries stay synchronized with the shared source', () => {
  const source = readJson('../../shared/model-registry/ccx_model_registry.json')
  const embedded = readJson('../../backend-go/internal/presetstore/embedded/model-registry.json')
  const published = readJson('../../docs/public/presets/model-registry.json')

  assert.deepEqual(embedded, source)
  assert.deepEqual(published, source)
})

test('benchmark updater rolls preset artifacts into its generated output transaction', () => {
  for (const artifactPath of presetArtifactPaths) {
    assert.ok(generatedArtifactPaths.includes(artifactPath), `${artifactPath} is not tracked`)
  }
})

test('benchmark updater warns but continues when optional sources fail', () => {
  const warnings = []

  assert.doesNotThrow(() => {
    warnSourceFailures([
      { source: 'dradar', error: 'fetch failed' },
      { source: 'artificial-analysis', error: 'HTTP 401 Unauthorized' },
    ], message => warnings.push(message))
  })
  assert.deepEqual(warnings, [
    '[warning] benchmark sources failed (dradar, artificial-analysis); continuing with successful sources',
  ])
})

test('canonical pattern generation accepts canonical and source model names', () => {
  const expected = '(?:^|[-/])gpt-5\\.6-sol(?=$|@)'
  assert.equal(canonicalModelToPattern('gpt-5.6-sol'), expected)
  assert.equal(deepsweModelToPattern('gpt-5-6-sol'), expected)
  assert.equal(deepsweModelToPattern('gpt-5.6-sol'), null)
})

test('DeepSWE percentile and cohort use one best row per model', () => {
  const rows = [
    { model: 'model-a', pass_at_1: 0.8, reasoning_effort: 'high', n_tasks_attempted: 100 },
    { model: 'model-a', pass_at_1: 0.7, reasoning_effort: 'low', n_tasks_attempted: 100 },
    { model: 'model-b', pass_at_1: 0.6, reasoning_effort: 'high', n_tasks_attempted: 100 },
  ]
  const best = extractDeepSWEBest({ rows }, { 'model-a': 'a', 'model-b': 'b' })
  const evidence = toDeepSWEEvidence(best[0], best)

  assert.equal(best.length, 2)
  assert.equal(evidence.cohortSize, 2)
  assert.equal(evidence.cohortPercentile, 1)
  assert.equal(evidence.taskCount, 100)
})

test('benchmark evidence normalizes missing effort to default', () => {
  const deepEvidence = toDeepSWEEvidence({
    deepsweModel: 'model-a',
    score: 0.5,
    nTasks: 100,
    reasoningEffort: null,
  }, [{ score: 0.5 }])
  const radarEvidence = toDradarEvidence({
    deepsweModel: 'model-a',
    passRate: 0.5,
    cells: 100,
    bestEffort: null,
  }, [{ passRate: 0.5 }])

  assert.equal(deepEvidence.effort, 'default')
  assert.equal(radarEvidence.effort, 'default')
})

test('CodexRadar table cache version is read from the live page contract', () => {
  assert.equal(
    extractTableCacheVersion('var TABLE_CACHE_VERSION = "20260718-discrimination-toggle-2";'),
    '20260718-discrimination-toggle-2',
  )
  assert.equal(
    extractTableCacheVersion('<script src="/assets/radar-report.js?v=20260807-goldset-v1"></script>'),
    '20260807-goldset-v1',
  )
  assert.equal(
    extractTableCacheVersion('<script src="/assets/radar-report.js?lang=zh&v=release%2Dchoice%2Dv2"></script>'),
    'release-choice-v2',
  )
  assert.throws(() => extractTableCacheVersion('<html></html>'), /table cache version/)
})

test('dradar cohort size is model count rather than graded run count', () => {
  const best = extractDradarBest({
    models: [
      { model: 'a', effort: 'high', pass_rate: 0.8, graded: 450, cells: 100, cells_passed: 80 },
      { model: 'b', effort: 'high', pass_rate: 0.6, graded: 440, cells: 100, cells_passed: 60 },
    ],
  }, { a: 'a', b: 'b' })
  const evidence = toDradarEvidence(best.a, Object.values(best))

  assert.equal(evidence.cohortSize, 2)
  assert.equal(evidence.cohortPercentile, 1)
  assert.equal(evidence.benchmark, 'codexradar')
  assert.equal(evidence.benchmarkVersion, 'v1')
})

test('CodexRadar leaderboard aggregation uses strict cell majority', () => {
  const leaderboard = extractLeaderboardFromTable({
    cells: {
      'task-a|gpt-5.6-sol|low': { n: 3, p: 2 },
      'task-b|gpt-5.6-sol|low': { n: 2, p: 1 },
      'task-c|gpt-5.6-sol|low': { n: 3, p: 3 },
      'task-d|ignored|low': { n: 3, p: 3 },
    },
  }, { 'gpt-5.6-sol': 'gpt-5.6-sol' })

  assert.deepEqual(leaderboard.models, [{
    model: 'gpt-5.6-sol',
    effort: 'low',
    graded: 8,
    passed: 6,
    cells: 3,
    cells_passed: 2,
    pass_rate: 2 / 3,
  }])
})

test('dradar extractCostData aggregates mean and median cost per model x effort', () => {
  const data = {
    cells: {
      'task-a|dradar-model|high': {
        ran_by: [
          { actual_cost_usd: 1.0, duration_sec: 10 },
          { actual_cost_usd: 3.0, duration_sec: 20 },
        ],
      },
      'task-b|dradar-model|high': {
        ran_by: [{ actual_cost_usd: 2.0, duration_sec: 30 }],
      },
      'task-c|dradar-model|low': {
        ran_by: [{ actual_cost_usd: 0.5, duration_sec: 5 }],
      },
      'task-d|unmapped|high': {
        ran_by: [{ actual_cost_usd: 99, duration_sec: 1 }],
      },
      'task-e|dradar-model|high': { ran_by: [] }, // 无运行记录应被跳过
    },
  }
  const cost = extractCostData(data, { 'dradar-model': 'canonical-model' })

  // high 档聚合 3 次运行：costs [1,2,3] -> mean 2, median 2
  assert.equal(cost['canonical-model'].high.nRuns, 3)
  assert.equal(cost['canonical-model'].high.meanCost, 2)
  assert.equal(cost['canonical-model'].high.medianCost, 2)
  // low 档单次运行
  assert.equal(cost['canonical-model'].low.meanCost, 0.5)
  assert.equal(cost['canonical-model'].low.nRuns, 1)
  // 未映射模型被忽略
  assert.equal(Object.keys(cost).length, 1)
})

test('dradar toBenchmarkEvidence injects meanCost into costUsd when costData present', () => {
  const modelData = {
    deepsweModel: 'dradar-model',
    canonicalModel: 'canonical-model',
    passRate: 0.7,
    cells: 100,
    bestEffort: 'high',
  }
  const costData = { 'canonical-model': { high: { meanCost: 2.5, medianCost: 2.0, nRuns: 3 } } }

  const withCost = toDradarEvidence(modelData, [{ passRate: 0.7 }], costData)
  assert.equal(withCost.costUsd, 2.5)
  assert.equal(withCost.effort, 'high')

  // 缺 costData 时 costUsd 不注入（保持 undefined）
  const withoutCost = toDradarEvidence(modelData, [{ passRate: 0.7 }])
  assert.equal(withoutCost.costUsd, undefined)

  // costData 存在但该 model x effort 无实测时同样不注入
  const noEffortCost = toDradarEvidence(modelData, [{ passRate: 0.7 }], { 'canonical-model': {} })
  assert.equal(noEffortCost.costUsd, undefined)
})

test('LiteLLM keeps missing capabilities unknown and maps function calling to toolCalls', () => {
  const info = extractModelInfo({
    source: {
      max_input_tokens: 100_000,
      supports_function_calling: true,
    },
  }, { source: 'canonical' }).canonical

  assert.equal(info.supports.toolCalls, true)
  assert.equal(info.supports.vision, undefined)
  assert.equal(info.supports.reasoning, undefined)
  assert.equal(Object.hasOwn(info.supports, 'functionCalling'), false)
})

test('LiteLLM preserves explicit zero prices', () => {
  const info = extractModelInfo({
    source: {
      input_cost_per_token: 0,
      output_cost_per_token: 0,
      cache_read_input_token_cost: 0,
    },
  }, { source: 'canonical' }).canonical

  assert.equal(info.pricing.inputCacheMissPrice, 0)
  assert.equal(info.pricing.outputPrice, 0)
  assert.equal(info.pricing.inputCacheHitPrice, 0)
})

test('LiteLLM normalizes per-million prices to avoid float noise', () => {
  const info = extractModelInfo({
    source: {
      input_cost_per_token: 2e-7,
      output_cost_per_token: 6.4e-6,
      cache_read_input_token_cost: 5e-8,
    },
  }, { source: 'canonical' }).canonical

  assert.equal(info.pricing.inputCacheMissPrice, 0.2)
  assert.equal(info.pricing.outputPrice, 6.4)
  assert.equal(info.pricing.inputCacheHitPrice, 0.05)
})

test('benchmark merge creates a complete valid profile', () => {
  const registry = { benchmarkProfiles: [], upstreamCapabilities: [] }
  mergeDeepsweData(registry, {
    'gpt-5.6-sol': {
      deepsweMeta: { deepsweModel: 'gpt-5-6-sol' },
      benchmarkEvidence: [{
        benchmark: 'deepswe',
        benchmarkVersion: 'v1.1',
        sourceModel: 'gpt-5-6-sol',
        domain: 'coding',
        metric: 'pass_at_1',
        rawValue: 0.8,
        uncertainty: 0.01,
        cohortPercentile: 1,
        taskCount: 100,
        cohortSize: 4,
        effort: 'high',
        selectionBasis: 'best_available_effort',
        sourceUrl: 'https://deepswe.example/',
        capturedAt: '2026-07-21',
      }],
    },
  }, emptyReport(), null)

  assert.doesNotThrow(() => validateRegistry(registry))
  assert.deepEqual(registry.benchmarkProfiles[0].sources, ['https://deepswe.example/'])
  assert.equal(registry.benchmarkProfiles[0].sharedResults, 4)
})

test('BenchLM zero comparable categories do not erase valid evidence metadata', () => {
  const registry = {
    benchmarkProfiles: [{
      patterns: ['(?:^|[-/])kimi-k2\\.7-code(?=$|@)'],
      canonicalModel: 'kimi-k2.7-code',
      benchmarkEvidence: [{
        benchmark: 'deepswe',
        benchmarkVersion: 'v1.1',
        domain: 'coding',
        sourceUrl: 'https://deepswe.example/',
        cohortSize: 16,
      }],
      sources: ['https://deepswe.example/'],
      verifiedAt: '2026-07-21',
      lane: 'provisional',
      sharedResults: 16,
      comparableCategories: 1,
      totalCategories: 1,
    }],
  }
  mergeBenchlmData(registry, {
    'kimi-k2.7-code': {
      overallScore: 55,
      categoryScores: {},
      counts: { sharedBenchmarkCount: 18, comparableCategoryCount: 0, totalCategoryCount: 8 },
      sources: ['https://benchlm.example/compare'],
    },
  }, emptyReport(), null)

  const profile = registry.benchmarkProfiles[0]
  assert.equal(profile.sharedResults, 18)
  assert.equal(profile.comparableCategories, 1)
  assert.equal(profile.totalCategories, 8)
  assert.doesNotThrow(() => validateRegistry(registry))
})

test('BenchLM mapper can rebuild fresh profiles from raw models doc for DeepSeek variants', () => {
  const rawDoc = {
    items: [{
      slug: 'deepseek-v4-flash-max',
      url: 'https://benchlm.ai/models/deepseek-v4-flash-max',
      displayScore: 78,
      coverage: { trustedBenchmarkCount: 12 },
      scores: {
        displayCategoryScores: {
          Coding: 81,
          Knowledge: 75,
        },
      },
    }],
  }

  const profiles = extractBenchlmProfiles(rawDoc, {
    'deepseek-v4-flash-max': 'deepseek-v4-flash',
  }, {
    Coding: 'coding',
    Knowledge: 'knowledge',
  })

  assert.equal(profiles['deepseek-v4-flash'].overallScore, 78)
  assert.equal(profiles['deepseek-v4-flash'].categoryScores.coding, 81)
  assert.equal(profiles['deepseek-v4-flash'].categoryScores.knowledge, 75)
  assert.deepEqual(profiles['deepseek-v4-flash'].sources, [
    'https://benchlm.ai/models/deepseek-v4-flash-max',
    'https://benchlm.ai/methodology',
  ])
})

test('visualization combines DeepSWE, BenchLM and CodexRadar sources', () => {
  const evidence = (benchmark, benchmarkVersion, rawValue) => ({
    benchmark,
    benchmarkVersion,
    domain: 'coding',
    metric: 'pass_at_1',
    rawValue,
    effort: 'high',
  })
  const visualization = buildBenchmarkVisualizationData({
    modelMap: { source: 'model' },
    deepsweLeaderboard: { rows: [{
      model: 'source', reasoning_effort: 'high', pass_at_1: 0.7,
      mean_cost_usd: 2, median_cost_usd: 1.5,
    }] },
    deepsweProfiles: { model: { benchmarkEvidence: [evidence('deepswe', 'v1.1', 0.7)] } },
    benchlmProfiles: { model: { overallScore: 80, categoryScores: { coding: 75 } } },
    dradarProfiles: { model: {
      benchmarkEvidence: [evidence('codexradar', 'v1', 0.6)],
      efforts: { high: { passRate: 0.6 } },
      costData: { high: { meanCost: 1, medianCost: 0.8 } },
    } },
  })

  assert.deepEqual([...new Set(visualization.data.map(row => row.source))].sort(), ['CodexRadar', 'DeepSWE v1.1'])
  assert.deepEqual(
    [...new Set(visualization.comparisons.map(row => row.source))].sort(),
    ['BenchLM.ai', 'CodexRadar', 'DeepSWE v1.1'],
  )
  const validated = validateVisualizationData(visualization)
  const html = renderBenchmarkChart(validated.rows, validated.comparisons)
  assert.match(html, /多来源能力比较/)
  assert.match(html, /BenchLM\.ai/)
})

test('LiteLLM fills only unknown capabilities', () => {
  const registry = {
    upstreamCapabilities: [{
      patterns: ['(?:^|[-/])model(?=$|@)'],
      capabilities: { vision: true },
    }],
  }
  mergeLitellmData(registry, {
    model: { supports: { vision: false, toolCalls: true } },
  }, emptyReport(), null)

  assert.equal(registry.upstreamCapabilities[0].capabilities.vision, true)
  assert.equal(registry.upstreamCapabilities[0].capabilities.toolCalls, true)
})

test('opus-5 is mapped across every benchmark source', () => {
  assert.equal(DEEPSWE_MODEL_MAP['claude-opus-5'], 'claude-opus-5')
  assert.equal(BENCHLM_MODEL_MAP['claude-opus-5'], 'claude-opus-5')
  assert.equal(DRADAR_MODEL_MAP['claude-opus-5'], 'claude-opus-5')
  assert.equal(LITELLM_MODEL_MAP['claude-opus-5'], 'claude-opus-5')
  assert.equal(ARTIFICIAL_ANALYSIS_MODEL_MAP['claude-opus-5'], 'claude-opus-5')
})

test('DeepSeek BenchLM variants fold into canonical flash and pro models', () => {
  assert.equal(BENCHLM_MODEL_MAP['deepseek-v4-flash'], 'deepseek-v4-flash')
  assert.equal(BENCHLM_MODEL_MAP['deepseek-v4-flash-base'], 'deepseek-v4-flash')
  assert.equal(BENCHLM_MODEL_MAP['deepseek-v4-flash-high'], 'deepseek-v4-flash')
  assert.equal(BENCHLM_MODEL_MAP['deepseek-v4-flash-max'], 'deepseek-v4-flash')
  assert.equal(BENCHLM_MODEL_MAP['deepseek-v4-pro'], 'deepseek-v4-pro')
  assert.equal(BENCHLM_MODEL_MAP['deepseek-v4-pro-base'], 'deepseek-v4-pro')
  assert.equal(BENCHLM_MODEL_MAP['deepseek-v4-pro-high'], 'deepseek-v4-pro')
  assert.equal(BENCHLM_MODEL_MAP['deepseek-v4-pro-max'], 'deepseek-v4-pro')
  // 日期后缀别名：部分渠道以 -MMDD 形式发布新版本
  assert.equal(BENCHLM_MODEL_MAP['deepseek-v4-pro-0813'], 'deepseek-v4-pro')
})

test('DeepSeek CodexRadar flash and pro models are mapped', () => {
  assert.equal(DRADAR_MODEL_MAP['deepseek-v4-flash'], 'deepseek-v4-flash')
  assert.equal(DRADAR_MODEL_MAP['deepseek-v4-pro'], 'deepseek-v4-pro')
})

test('DeepSeek canonical model pattern supports dated suffixes', () => {
  const pattern = canonicalModelToPattern('deepseek-v4-pro')
  const ok = [
    'deepseek-v4-pro',
    'DeepSeek-V4-Pro-0813',
    'deepseek-v4-pro-2026-08-13',
    'deepseek-v4-pro-260813',
    'deepseek-v4-pro-20260813',
  ]
  for (const model of ok) {
    assert.match(model, new RegExp(pattern, 'i'), `${model} should match ${pattern}`)
  }
})

test('Artificial Analysis LLM extraction yields one evidence per composite index', () => {
  const models = [
    { slug: 'claude-opus-5', evaluations: {
      artificial_analysis_intelligence_index: 92,
      artificial_analysis_coding_index: 88,
      artificial_analysis_agentic_index: 80,
    } },
    { slug: 'claude-opus-4-8', evaluations: { artificial_analysis_intelligence_index: 77 } },
    { slug: 'unmapped-foo', evaluations: { artificial_analysis_intelligence_index: 50 } },
  ]
  const { profiles, unmappedSlugs } = extractAaLlm(models, ARTIFICIAL_ANALYSIS_MODEL_MAP, 4.1)

  assert.deepEqual(unmappedSlugs, ['unmapped-foo'])
  const evidence = profiles['claude-opus-5'].benchmarkEvidence
  assert.equal(evidence.length, 3)
  assert.deepEqual(evidence.map(e => e.domain).sort(), ['agentic', 'coding', 'overall'])
  assert.equal(evidence[0].benchmark, 'artificial_analysis')
  assert.equal(evidence[0].benchmarkVersion, 'v4.1')
  assert.equal(evidence[0].effort, 'default')
  assert.equal(evidence[0].selectionBasis, 'composite_index')
  // composite index 无任务级 raw data，taskCount 用 evaluation 数作代理以满足 schema
  assert.equal(evidence[0].taskCount, 9)
  // cohort 为数据集中所有有 intelligence_index 的模型（含未映射的，百分位相对全市场）
  assert.equal(evidence[0].cohortSize, 3)
  // opus-5 (92) 在 [92, 77, 50] 队列中百分位 = 1.0
  const overall = evidence.find(e => e.domain === 'overall')
  assert.equal(overall.rawValue, 92)
  assert.equal(overall.cohortPercentile, 1)
  assert.match(overall.sourceUrl, /^https:\/\/artificialanalysis\.ai\/models\/claude-opus-5$/)
})

test('Artificial Analysis image arena extraction maps Elo', () => {
  const models = [
    { slug: 'agnes-image-2.1-flash', elo: 1180, ci_95: 12 },
    { slug: 'unmapped-img', elo: 900, ci_95: null },
  ]
  const { profiles, unmappedSlugs } = extractAaImage(models, ARTIFICIAL_ANALYSIS_IMAGE_MODEL_MAP)

  assert.deepEqual(unmappedSlugs, ['unmapped-img'])
  assert.equal(profiles['agnes-image-2.1-flash'].elo, 1180)
  assert.equal(profiles['agnes-image-2.1-flash'].ci95, 12)
  assert.ok(profiles['agnes-image-2.1-flash'].sources.length > 0)
})

test('Artificial Analysis LLM merge replaces evidence without clobbering overallScore', () => {
  const registry = {
    benchmarkProfiles: [{
      patterns: ['(?:^|[-/])claude-opus-5(?=$|@)'],
      canonicalModel: 'claude-opus-5',
      overallScore: 70,
      categoryScores: { coding: 60 },
      benchmarkEvidence: [{
        benchmark: 'deepswe',
        benchmarkVersion: 'v1.1',
        domain: 'coding',
        sourceUrl: 'https://deepswe.example/',
        cohortSize: 4,
      }, {
        benchmark: 'artificial_analysis',
        benchmarkVersion: 'v4.0',
        domain: 'overall',
        sourceUrl: 'https://artificialanalysis.ai/models/claude-opus-5',
      }],
      sources: ['https://deepswe.example/', 'https://artificialanalysis.ai/models/claude-opus-5'],
      verifiedAt: '2026-07-20',
      lane: 'provisional',
      sharedResults: 4,
      comparableCategories: 1,
      totalCategories: 1,
    }],
  }
  mergeArtificialAnalysisLlm(registry, {
    'claude-opus-5': {
      aaMeta: { slug: 'claude-opus-5' },
      benchmarkEvidence: [{
        benchmark: 'artificial_analysis',
        benchmarkVersion: 'v4.1',
        sourceModel: 'claude-opus-5',
        domain: 'overall',
        metric: 'intelligence_index',
        rawValue: 92,
        uncertainty: 0,
        cohortPercentile: 1,
        cohortSize: 2,
        effort: 'default',
        selectionBasis: 'composite_index',
        sourceUrl: 'https://artificialanalysis.ai/models/claude-opus-5',
        capturedAt: '2026-07-25',
      }],
    },
  }, emptyReport(), null)

  const profile = registry.benchmarkProfiles[0]
  // overallScore/categoryScores 保持 benchlm 原值，未被 AA 覆盖
  assert.equal(profile.overallScore, 70)
  assert.equal(profile.categoryScores.coding, 60)
  // 旧 AA 证据被替换，deepswe 证据保留
  const aa = profile.benchmarkEvidence.filter(e => e.benchmark === 'artificial_analysis')
  assert.equal(aa.length, 1)
  assert.equal(aa[0].benchmarkVersion, 'v4.1')
  const deep = profile.benchmarkEvidence.filter(e => e.benchmark === 'deepswe')
  assert.equal(deep.length, 1)
  assert.doesNotThrow(() => validateRegistry(registry))
})

test('AA llm merge skips unchanged data and preserves verifiedAt', () => {
  const makeRegistry = () => ({
    benchmarkProfiles: [{
      patterns: ['(?:^|[-/])claude-opus-5(?=$|@)'],
      canonicalModel: 'claude-opus-5',
      benchmarkEvidence: [{
        benchmark: 'artificial_analysis',
        benchmarkVersion: 'v4.1',
        sourceModel: 'claude-opus-5',
        domain: 'overall',
        metric: 'intelligence_index',
        rawValue: 92,
        cohortSize: 2,
        sourceUrl: 'https://artificialanalysis.ai/models/claude-opus-5',
        capturedAt: '2026-07-25',
      }],
      sources: ['https://artificialanalysis.ai/models/claude-opus-5'],
      verifiedAt: '2026-07-20',
      lane: 'provisional',
      sharedResults: 4,
      comparableCategories: 1,
      totalCategories: 1,
    }],
  })
  const aaData = {
    'claude-opus-5': {
      aaMeta: { slug: 'claude-opus-5' },
      benchmarkEvidence: [{
        benchmark: 'artificial_analysis',
        benchmarkVersion: 'v4.1',
        sourceModel: 'claude-opus-5',
        domain: 'overall',
        metric: 'intelligence_index',
        rawValue: 92,
        cohortSize: 2,
        sourceUrl: 'https://artificialanalysis.ai/models/claude-opus-5',
        capturedAt: '2026-07-29',
      }],
    },
  }

  const registry = makeRegistry()
  const report = emptyReport()
  mergeArtificialAnalysisLlm(registry, aaData, report, null)

  assert.equal(report.unchanged.length, 1)
  assert.equal(report.updated.length, 0)
  const profile = registry.benchmarkProfiles[0]
  assert.equal(profile.verifiedAt, '2026-07-20')
  assert.equal(profile.benchmarkEvidence[0].capturedAt, '2026-07-25')
  assert.doesNotThrow(() => validateRegistry(registry))
})

test('Artificial Analysis image arena merge builds and updates imageArenaProfiles', () => {
  const registry = { benchmarkProfiles: [], imageArenaProfiles: [] }
  mergeArtificialAnalysisImageArena(registry, {
    'agnes-image-2.1-flash': {
      canonicalModel: 'agnes-image-2.1-flash',
      elo: 1180,
      ci95: 12,
      sources: ['https://artificialanalysis.ai/leaderboards/image-generation'],
    },
  }, emptyReport(), null)

  assert.equal(registry.imageArenaProfiles.length, 1)
  const arena = registry.imageArenaProfiles[0]
  assert.equal(arena.canonicalModel, 'agnes-image-2.1-flash')
  assert.equal(arena.elo, 1180)
  assert.ok(arena.patterns.length > 0)
  assert.ok(/^\d{4}-\d{2}-\d{2}$/.test(arena.verifiedAt))
  assert.doesNotThrow(() => validateRegistry(registry))

  // 更新路径：同 canonical 再次 merge 时替换 elo
  mergeArtificialAnalysisImageArena(registry, {
    'agnes-image-2.1-flash': {
      canonicalModel: 'agnes-image-2.1-flash',
      elo: 1195,
      ci95: 10,
      sources: ['https://artificialanalysis.ai/leaderboards/image-generation'],
    },
  }, emptyReport(), null)
  assert.equal(registry.imageArenaProfiles.length, 1)
  assert.equal(registry.imageArenaProfiles[0].elo, 1195)
  assert.doesNotThrow(() => validateRegistry(registry))
})

test('deepswe merge skips unchanged data and preserves capturedAt/verifiedAt', () => {
  const registry = { benchmarkProfiles: [], upstreamCapabilities: [] }
  const evidence = {
    benchmark: 'deepswe',
    benchmarkVersion: 'v1.1',
    sourceModel: 'gpt-5-6-sol',
    domain: 'coding',
    metric: 'pass_at_1',
    rawValue: 0.8,
    uncertainty: 0.01,
    cohortPercentile: 1,
    taskCount: 100,
    cohortSize: 4,
    effort: 'high',
    selectionBasis: 'best_available_effort',
    sourceUrl: 'https://deepswe.example/',
    capturedAt: '2026-07-21',
  }
  mergeDeepsweData(registry, {
    'gpt-5.6-sol': {
      deepsweMeta: { deepsweModel: 'gpt-5-6-sol' },
      benchmarkEvidence: [evidence],
    },
  }, emptyReport(), null)
  registry.benchmarkProfiles[0].verifiedAt = '2026-07-01'

  // 相同数据（仅 capturedAt 变为新日期）再次 merge：应跳过，日期保持原值
  const report = emptyReport()
  mergeDeepsweData(registry, {
    'gpt-5.6-sol': {
      deepsweMeta: { deepsweModel: 'gpt-5-6-sol' },
      benchmarkEvidence: [{ ...evidence, capturedAt: '2026-07-29' }],
    },
  }, report, null)

  assert.equal(report.unchanged.length, 1)
  assert.equal(report.updated.length, 0)
  const profile = registry.benchmarkProfiles[0]
  assert.equal(profile.verifiedAt, '2026-07-01')
  assert.equal(profile.benchmarkEvidence[0].capturedAt, '2026-07-21')
  assert.doesNotThrow(() => validateRegistry(registry))

  // 数据真正变化（rawValue 不同）时正常更新并刷新 verifiedAt
  const report2 = emptyReport()
  mergeDeepsweData(registry, {
    'gpt-5.6-sol': {
      deepsweMeta: { deepsweModel: 'gpt-5-6-sol' },
      benchmarkEvidence: [{ ...evidence, rawValue: 0.85, capturedAt: '2026-07-29' }],
    },
  }, report2, null)

  assert.equal(report2.updated.length, 1)
  assert.equal(registry.benchmarkProfiles[0].benchmarkEvidence[0].rawValue, 0.85)
  assert.notEqual(registry.benchmarkProfiles[0].verifiedAt, '2026-07-01')
})

test('deepswe merge treats reordered evidence as unchanged', () => {
  const makeEvidence = (rawValue) => ({
    benchmark: 'deepswe',
    benchmarkVersion: 'v1.1',
    sourceModel: 'gpt-5-6-sol',
    domain: 'coding',
    metric: 'pass_at_1',
    rawValue,
    sourceUrl: 'https://deepswe.example/',
    capturedAt: '2026-07-21',
  })
  const registry = { benchmarkProfiles: [], upstreamCapabilities: [] }
  mergeDeepsweData(registry, {
    'gpt-5.6-sol': {
      deepsweMeta: { deepsweModel: 'gpt-5-6-sol' },
      benchmarkEvidence: [makeEvidence(0.8), makeEvidence(0.6)],
    },
  }, emptyReport(), null)
  registry.benchmarkProfiles[0].verifiedAt = '2026-07-01'

  // 上游返回顺序颠倒但内容相同：应视为未变更
  const report = emptyReport()
  mergeDeepsweData(registry, {
    'gpt-5.6-sol': {
      deepsweMeta: { deepsweModel: 'gpt-5-6-sol' },
      benchmarkEvidence: [makeEvidence(0.6), makeEvidence(0.8)],
    },
  }, report, null)

  assert.equal(report.unchanged.length, 1)
  assert.equal(registry.benchmarkProfiles[0].verifiedAt, '2026-07-01')
})

test('image arena merge skips unchanged elo and preserves verifiedAt', () => {
  const registry = { benchmarkProfiles: [], imageArenaProfiles: [] }
  const data = {
    canonicalModel: 'agnes-image-2.1-flash',
    elo: 1180,
    ci95: 12,
    sources: ['https://artificialanalysis.ai/leaderboards/image-generation'],
  }
  mergeArtificialAnalysisImageArena(registry, { 'agnes-image-2.1-flash': data }, emptyReport(), null)
  registry.imageArenaProfiles[0].verifiedAt = '2026-07-01'

  const report = emptyReport()
  mergeArtificialAnalysisImageArena(registry, { 'agnes-image-2.1-flash': { ...data } }, report, null)

  assert.equal(report.unchanged.length, 1)
  assert.equal(report.aaImageArenaUpdated.length, 0)
  assert.equal(registry.imageArenaProfiles[0].verifiedAt, '2026-07-01')
  assert.doesNotThrow(() => validateRegistry(registry))
})

test('visualization includes Artificial Analysis and image arena comparisons', () => {
  const evidence = (benchmark, rawValue) => ({
    benchmark,
    benchmarkVersion: 'v4.1',
    domain: 'overall',
    metric: 'intelligence_index',
    rawValue,
    effort: 'default',
  })
  const visualization = buildBenchmarkVisualizationData({
    modelMap: { source: 'model' },
    deepsweProfiles: { model: { benchmarkEvidence: [evidence('deepswe', 0.7)] } },
    benchlmProfiles: { model: { overallScore: 80, categoryScores: { coding: 75 } } },
    dradarProfiles: { model: { benchmarkEvidence: [evidence('codexradar', 0.6)], efforts: {} } },
    artificialAnalysisProfiles: { model: { benchmarkEvidence: [evidence('artificial_analysis', 92)] } },
    artificialAnalysisImageArena: { 'agnes-image-2.1-flash': { elo: 1180 } },
  })

  assert.ok(
    [...new Set(visualization.comparisons.map(row => row.source))].includes('Artificial Analysis'),
    'comparisons should include Artificial Analysis',
  )
  const imgRow = visualization.comparisons.find(r => r.category === 'image_arena')
  assert.ok(imgRow, 'comparisons should include image_arena rows')
  assert.equal(imgRow.score, 1180)
  const validated = validateVisualizationData(visualization)
  const html = renderBenchmarkChart(validated.rows, validated.comparisons)
  assert.match(html, /Artificial Analysis/)
  assert.match(html, /图像 Arena Elo/)
})
