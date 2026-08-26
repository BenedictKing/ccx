import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import test from 'node:test'

import {
  canonicalModelToPattern,
  deepsweModelToPattern,
  detectNewModelCandidates,
  matchesAnyRegistryPattern,
  reportUnmappedAgainstRegistry,
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
  collectUnmappedTableModels,
  toBenchmarkEvidence as toDradarEvidence,
  DRADAR_MODEL_MAP,
} from './dradar.mjs'
import { extractProfiles as extractBenchlmProfiles } from './benchlm.mjs'
import { extractModelInfo, collectUnmappedLitellmKeys, collectMissingMappedKeys, LITELLM_MODEL_MAP } from './litellm.mjs'
import {
  extractLlmProfiles as extractAaLlm,
  extractImageArenaProfiles as extractAaImage,
  resolveArtificialAnalysisSlug,
  ARTIFICIAL_ANALYSIS_MODEL_MAP,
  ARTIFICIAL_ANALYSIS_IMAGE_MODEL_MAP,
} from './artificialanalysis.mjs'
import { resolveProxyEnv } from './http.mjs'
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

test('benchmark fetch resolves lowercase proxy env variables', () => {
  const proxyEnv = resolveProxyEnv({
    http_proxy: 'http://127.0.0.1:6785',
    https_proxy: 'http://127.0.0.1:6785',
  })

  assert.equal(proxyEnv.http_proxy, 'http://127.0.0.1:6785')
  assert.equal(proxyEnv.https_proxy, 'http://127.0.0.1:6785')
})

test('benchmark fetch uses ALL_PROXY as a fallback', () => {
  const proxyEnv = resolveProxyEnv({ ALL_PROXY: 'socks5://127.0.0.1:6785' })

  assert.equal(proxyEnv.http_proxy, 'socks5://127.0.0.1:6785')
  assert.equal(proxyEnv.https_proxy, 'socks5://127.0.0.1:6785')
})

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

test('new-model detection flags unmapped same-family higher versions only', () => {
  const modelMap = {
    'grok-4-5': 'grok-4.5',
    'grok-4-6': 'grok-4.6',
    'claude-opus-4-8': 'claude-opus-4-8',
    'claude-opus-5': 'claude-opus-5',
    'gpt-5-6-sol': 'gpt-5.6-sol',
  }
  const candidates = detectNewModelCandidates(
    [
      'grok-4-20-beta',        // 同家族更高版本 → 提示
      'grok-4-3',              // 同家族更低版本 → 忽略
      'grok-4-1-fast',         // 更低版本变体 → 忽略
      'grok-code-fast-1',      // 家族不同 → 忽略
      'claude-opus-4-6',       // 低于已映射 4.8 → 忽略
      'claude-opus-5.5',       // 高于 5 → 提示
      'gpt-6',                 // 高于 5.6 → 提示
      'some-unrelated-model',  // 无家族 → 忽略
    ],
    modelMap,
  )
  assert.deepEqual(
    candidates.map(c => c.name),
    ['grok-4-20-beta', 'claude-opus-5.5', 'gpt-6'],
  )
  assert.equal(candidates[0].mappedBest, 'grok-4.6')
  assert.equal(candidates[0].version, '4.20')
})

test('new-model detection expands provider prefixes and underscore variants', () => {
  const candidates = detectNewModelCandidates(
    [
      'zai/glm-6',                    // provider/ 前缀剥离后命中 glm 家族 → 提示
      'openrouter/xiaomi/mimo-v3',    // 多级前缀逐段剥离后命中 → 提示
      'google_grok-4-20',             // provider_ 前缀剥离后命中 → 提示
      'zai/glm-4',                    // 前缀剥离后版本更低 → 忽略
      'foo/grok-4-1',                 // 前缀剥离后版本更低 → 忽略
    ],
    BENCHLM_MODEL_MAP,
  )
  assert.deepEqual(
    candidates.map(c => c.name),
    ['zai/glm-6', 'openrouter/xiaomi/mimo-v3', 'google_grok-4-20'],
  )
  assert.equal(candidates[0].family, 'glm')
  assert.equal(candidates[0].version, '6')
  assert.equal(candidates[0].mappedBest, 'glm-5.3')
})

test('registry reverse-lookup recognizes known models across naming variants', () => {
  const patterns = [
    canonicalModelToPattern('gpt-5.6-sol'),        // 点号 pattern
    canonicalModelToPattern('claude-opus-5'),      // 含日期快照后缀
    canonicalModelToPattern('deepseek-v4-pro'),    // 含 -MMDD 别名后缀
  ]
  assert.equal(matchesAnyRegistryPattern('gpt-5-6-sol', patterns), true)   // 连字符 slug
  assert.equal(matchesAnyRegistryPattern('gpt-5.6-sol', patterns), true)   // 点号 slug
  assert.equal(matchesAnyRegistryPattern('zai/gpt-5.6-sol', patterns), true) // provider 前缀
  assert.equal(matchesAnyRegistryPattern('claude-opus-5-20260101', patterns), true) // 日期快照
  assert.equal(matchesAnyRegistryPattern('deepseek-v4-pro-0813', patterns), true)  // 日期别名
  assert.equal(matchesAnyRegistryPattern('claude-mythos-5', patterns), false)      // 全新家族
  assert.equal(matchesAnyRegistryPattern('grok-4-20-beta', patterns), false)
})

test('reportUnmappedAgainstRegistry splits registered-but-unmapped from unrecognized models', () => {
  const patterns = [
    canonicalModelToPattern('claude-opus-5'),
    canonicalModelToPattern('gpt-5.5'),
    canonicalModelToPattern('claude-mythos-5'),
  ]
  // 三种状态：已注册但源映射表缺条目（UNMAPPED，丢分告警）、registry 认识的日期快照（同样 UNMAPPED）、
  // 全新家族（UNRECOGNIZED，正向 NEW-MODEL 检测无基线，只有反向对照能暴露）
  const { unmapped, unrecognized } = reportUnmappedAgainstRegistry('test-source', [
    'claude-mythos-5',
    'claude-opus-5-20260101',
    'claude-mythos-6',
    'grok-4-20-beta',
  ], patterns, { mapName: 'TEST_MAP', maxListed: 10 })
  assert.deepEqual(unmapped.sort(), ['claude-mythos-5', 'claude-opus-5-20260101'])
  assert.deepEqual(unrecognized.sort(), ['claude-mythos-6', 'grok-4-20-beta'])

  // 大清单按根家族聚合，claude 家族的 newest 代表应是版本最高的 mythos-6
  const many = ['claude-3-5-haiku', 'claude-3-7-sonnet-thinking', 'claude-mythos-6']
  for (let i = 0; i < 25; i++) many.push(`unknown-vendor-model-${i}`)
  const aggregated = reportUnmappedAgainstRegistry('test-source', many, patterns, { maxListed: 5, maxFamilies: 3 })
  assert.equal(aggregated.unrecognized.length, 28)
})

test('new-model detection rejects date snapshots, parameter sizes and legacy names', () => {  const candidates = detectNewModelCandidates(
    [
      'gpt-5-2025-08-07',      // 日期快照段值 > 50 → 忽略
      'grok-4-0709',           // 日期快照 → 忽略
      'mimo-v2-0206',          // 日期快照 → 忽略
      'azure/gpt-35-turbo',    // 主版本 35 与 gpt-5.x 非同代 → 忽略
      'claude-opus-41',        // 主版本 41 非同代 → 忽略
      'qwen3-32b',             // 参数量段(32b) 落掉后与 mapped qwen3 等版本 → 忽略
      'Qwen3-235B-A22B',       // 参数量段落掉后等版本 → 忽略
      'Qwen3.5-397B-A22B',     // 参数量段落掉后 [3,5] > [3] → 提示
      'gpt-7',                 // 主版本跳两代 → 忽略（安全网只覆盖同代与下一代）
      'grok-4-20-beta',        // 同代更高版本 → 提示
    ],
    LITELLM_MODEL_MAP,
  )
  assert.deepEqual(
    candidates.map(c => c.name),
    ['Qwen3.5-397B-A22B', 'grok-4-20-beta'],
  )
  assert.equal(candidates[0].version, '3.5')
})

test('extractProfiles collects unmapped slugs for new-model detection', () => {
  const doc = {
    items: [
      { slug: 'grok-4-5', displayScore: 75, scores: { displayCategoryScores: { coding: 50 } } },
      { slug: 'grok-4-7', displayScore: 90, scores: { displayCategoryScores: {} } },
    ],
  }
  const unmapped = []
  const profiles = extractBenchlmProfiles(doc, BENCHLM_MODEL_MAP, { coding: 'coding' }, unmapped)
  assert.ok(profiles['grok-4.5'])
  assert.equal(profiles['grok-4.5'].overallScore, 75)
  assert.deepEqual(unmapped, ['grok-4-7'])
  assert.equal(detectNewModelCandidates(unmapped, BENCHLM_MODEL_MAP).length, 1)
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

test('dradar collectUnmappedTableModels dedupes unmapped models and feeds new-model detection', () => {
  const table = {
    cells: {
      'task-a|gpt-5.6-sol|low': { n: 3, p: 2 },
      'task-b|gpt-5.6-sol|high': { n: 3, p: 3 },
      'task-c|gpt-5.7|high': { n: 3, p: 1 },
      'task-d|gpt-5.7|low': { n: 3, p: 2 },
      'task-e|unrelated-model|high': { n: 3, p: 1 },
    },
  }
  const unmapped = collectUnmappedTableModels(table, DRADAR_MODEL_MAP)
  assert.deepEqual(unmapped, ['gpt-5.7', 'unrelated-model'])
  assert.deepEqual(detectNewModelCandidates(unmapped, DRADAR_MODEL_MAP).map(c => c.name), ['gpt-5.7'])
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

test('LiteLLM unmapped/missing key collectors feed new-model and stale-mapping detection', () => {
  const data = {
    'gpt-5.5': { max_input_tokens: 1 },
    'zai/gpt-5.7': { max_input_tokens: 1 },
    'some-unrelated-vendor/model-x': { max_input_tokens: 1 },
  }
  const modelMap = { 'gpt-5.5': 'gpt-5.5', 'claude-opus-5': 'claude-opus-5' }

  assert.deepEqual(collectUnmappedLitellmKeys(data, modelMap), ['zai/gpt-5.7', 'some-unrelated-vendor/model-x'])
  assert.deepEqual(
    detectNewModelCandidates(collectUnmappedLitellmKeys(data, modelMap), modelMap).map(c => c.name),
    ['zai/gpt-5.7'],
  )
  // 映射表里的 claude-opus-5 在上游数据中不存在 → 失效告警名单
  assert.deepEqual(collectMissingMappedKeys(data, modelMap), ['claude-opus-5'])
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

test('BenchLM extractProfiles falls back to verified displayScore only when public score is null', () => {
  const rawDoc = {
    items: [
      // 有 verified 实测但未排公开总榜（如 deepseek-v4-flash-0731）：回退 verified 分
      {
        slug: 'deepseek-v4-flash-0731',
        displayScore: null,
        scores: { displayScore: 59, displayCategoryScores: { coding: 53.4 } },
      },
      // estimated 模型：顶层有估计分、verified 为 0，不得回退
      {
        slug: 'kimi-k3',
        displayScore: 80.16,
        scores: { displayScore: 0, displayCategoryScores: {} },
      },
      // 顶层与 verified 均无效：保持无总体分
      {
        slug: 'muse-spark-1-1',
        displayScore: null,
        scores: { displayScore: 0, displayCategoryScores: {} },
      },
    ],
  }

  const profiles = extractBenchlmProfiles(rawDoc, {
    'deepseek-v4-flash-0731': 'deepseek-v4-flash',
    'kimi-k3': 'kimi-k3',
    'muse-spark-1-1': 'muse-spark-1.1',
  }, { coding: 'coding' })

  assert.equal(profiles['deepseek-v4-flash'].overallScore, 59)
  assert.equal(profiles['deepseek-v4-flash'].categoryScores.coding, 53.4)
  assert.equal(profiles['kimi-k3'].overallScore, 80.16)
  assert.equal(profiles['muse-spark-1.1'].overallScore, null)
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
    benchmarkProfiles: { model: { canonicalModel: 'model', benchmarkEvidence: [
      { benchmark: 'deepswe', benchmarkVersion: 'v1.1', domain: 'coding', metric: 'pass_at_1', rawValue: 0.7, effort: 'high' },
    ] } },
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
  const html = renderBenchmarkChart(validated.rows, validated.comparisons, validated.qualityTiers)
  const deepsweHighRow = visualization.data.find(row => row.source === 'DeepSWE v1.1' && row.effort === 'high')
  // 组内仅有 high 单点：一侧回退取该档原始分，不再按全局比率折算
  assert.equal(deepsweHighRow.quality_score, 70)
  const registryVisualization = buildBenchmarkVisualizationData({
    benchmarkProfiles: { model: { canonicalModel: 'model', benchmarkEvidence: [
      { benchmark: 'codexradar', benchmarkVersion: 'codexradar', domain: 'coding', metric: 'pass_at_1', rawValue: 0.7, effort: 'ultra', costUsd: 1.25 },
    ] } },
  })
  assert.equal(registryVisualization.data.length, 1)
  assert.equal(registryVisualization.data[0].effort, 'ultra')
  assert.equal(registryVisualization.data[0].quality_score, 70)
  assert.ok(visualization.qualityTiers.premiumMin >= visualization.qualityTiers.highMin)
  assert.ok(visualization.qualityTiers.highMin >= visualization.qualityTiers.normalMin)
  assert.match(html, /多来源能力比较/)
  assert.match(html, /quality-bands/)
  assert.match(html, /BenchLM\.ai/)
})

test('quality score calibration keeps low-gain models below stronger ones', () => {
  const evidence = (rawValue, effort, costUsd) => ({
    benchmark: 'deepswe',
    benchmarkVersion: 'v1.1',
    domain: 'coding',
    metric: 'pass_at_1',
    rawValue,
    effort,
    costUsd,
  })
  const visualization = buildBenchmarkVisualizationData({
    benchmarkProfiles: {
      // 低增益模型：low→medium 实测仅 +9.7%，全局比率法曾把 low 点虚抬到 86.9
      'low-gain': { canonicalModel: 'low-gain', benchmarkEvidence: [
        evidence(0.596, 'low', 3.7),
        evidence(0.654, 'medium', 6),
      ] },
      // 高能力模型：仅有 medium 实测
      strong: { canonicalModel: 'strong', benchmarkEvidence: [
        evidence(0.689, 'medium', 3.2),
      ] },
      // 无 medium：low 与 high 跨越常规档，组内轨迹线性插值
      interpolated: { canonicalModel: 'interpolated', benchmarkEvidence: [
        evidence(0.545, 'low', 1),
        evidence(0.665, 'high', 3),
      ] },
      // 无 medium：全部在常规档一侧，取最接近档位（high）原始分
      'one-side': { canonicalModel: 'one-side', benchmarkEvidence: [
        evidence(0.607, 'high', 5),
        evidence(0.67, 'xhigh', 7),
      ] },
    },
  })
  const scoreOf = (model, effort) => visualization.data
    .find(row => row.model === model && row.effort === effort)?.quality_score

  // 同组共享 medium 实测等效分：low 点不再超过本组 medium 实测
  assert.equal(scoreOf('low-gain', 'low'), 65.4)
  assert.equal(scoreOf('low-gain', 'medium'), 65.4)
  assert.equal(scoreOf('strong', 'medium'), 68.9)
  assert.equal(scoreOf('interpolated', 'low'), 60.5)
  assert.equal(scoreOf('one-side', 'high'), 60.7)

  // 回归：低增益低能力模型的任意点不得高于高能力模型的等效分
  const lowGainMax = Math.max(
    ...visualization.data.filter(row => row.model === 'low-gain').map(row => row.quality_score),
  )
  assert.ok(lowGainMax < scoreOf('strong', 'medium'))

  // 校准分恒在组内实测原始分范围内（百分制）
  const rangeOf = (model) => {
    const rates = visualization.data.filter(row => row.model === model).map(row => row.pass_rate * 100)
    return [Math.min(...rates), Math.max(...rates)]
  }
  for (const model of ['low-gain', 'strong', 'interpolated', 'one-side']) {
    const [min, max] = rangeOf(model)
    for (const row of visualization.data.filter(r => r.model === model)) {
      assert.ok(row.quality_score >= min - 1e-9 && row.quality_score <= max + 1e-9)
    }
  }
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

test('grok-4.6 / kimi-k3 / glm-5.3 stay mapped in dradar and litellm (2026-08-20 audit regressions)', () => {
  // dradar 榜用点号 glm-5.3 与短名 k3；曾因连字符键/缺别名静默丢分
  assert.equal(DRADAR_MODEL_MAP['glm-5.3'], 'glm-5.3')
  assert.equal(DRADAR_MODEL_MAP['k3'], 'kimi-k3')
  assert.equal(DRADAR_MODEL_MAP['grok-4.6'], 'grok-4.6')
  assert.equal(DRADAR_MODEL_MAP['dsh-deepseek-v4-pro'], 'deepseek-v4-pro')
  // litellm：kimi-k2.7-code 裸 key 已从上游移除，grok-4.6 用 xai 官方 key
  assert.equal(LITELLM_MODEL_MAP['cloudflare/@cf/moonshotai/kimi-k2.7-code'], 'kimi-k2.7-code')
  assert.equal(LITELLM_MODEL_MAP['xai/grok-4.6'], 'grok-4.6')
})

test('deepswe glm-5.3-flash stays mapped (2026-08-26 release-day regression)', () => {
  // deepswe UI 渲染成点号 glm-5.3-flash，API 数据 slug 是连字符 glm-5-3-flash；
  // 映射键必须以 API 原始 slug 为准
  assert.equal(DEEPSWE_MODEL_MAP['glm-5-3-flash'], 'glm-5.3-flash')
})

test('benchlm dated deepseek variants and gemini-3-6-flash stay mapped (2026-08-23 regressions)', () => {
  assert.equal(BENCHLM_MODEL_MAP['deepseek-v4-flash-0731'], 'deepseek-v4-flash')
  assert.equal(BENCHLM_MODEL_MAP['gemini-3-6-flash'], 'gemini-3.6-flash')
  assert.equal(ARTIFICIAL_ANALYSIS_MODEL_MAP['deepseek-v4-flash'], 'deepseek-v4-flash')
  assert.equal(ARTIFICIAL_ANALYSIS_MODEL_MAP['deepseek-v4-pro'], 'deepseek-v4-pro')
})

test('five registered models map in benchlm and AA (2026-08-24 additions)', () => {
  // registry 能力表已有条目，补分数映射让它们进入候选池
  assert.equal(BENCHLM_MODEL_MAP['claude-opus-4-6'], 'claude-opus-4-6')
  assert.equal(BENCHLM_MODEL_MAP['claude-opus-4-7'], 'claude-opus-4-7')
  assert.equal(BENCHLM_MODEL_MAP['minimax-m2-7'], 'minimax-m2.7')
  assert.equal(BENCHLM_MODEL_MAP['minimax-m3'], 'minimax-m3')
  assert.equal(BENCHLM_MODEL_MAP['qwen3-7-max'], 'qwen3.7-max')
  assert.equal(ARTIFICIAL_ANALYSIS_MODEL_MAP['claude-opus-4-6'], 'claude-opus-4-6')
  assert.equal(ARTIFICIAL_ANALYSIS_MODEL_MAP['claude-opus-4-7'], 'claude-opus-4-7')
  assert.equal(ARTIFICIAL_ANALYSIS_MODEL_MAP['minimax-m2-7'], 'minimax-m2.7')
  assert.equal(ARTIFICIAL_ANALYSIS_MODEL_MAP['minimax-m3'], 'minimax-m3')
  assert.equal(ARTIFICIAL_ANALYSIS_MODEL_MAP['qwen3-7-max'], 'qwen3.7-max')
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
  const { profiles, unmappedSlugs } = extractAaLlm(models, 4.1)

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

test('Artificial Analysis slug resolver folds effort and non-reasoning variants into canonical', () => {
  // 裸 slug：default 复合指数
  assert.deepEqual(resolveArtificialAnalysisSlug('claude-opus-5'),
    { canonical: 'claude-opus-5', effort: 'default', selectionBasis: 'composite_index' })
  // effort 档位：xhigh/high/medium/low/minimal → best_available_effort
  assert.deepEqual(resolveArtificialAnalysisSlug('claude-opus-5-xhigh'),
    { canonical: 'claude-opus-5', effort: 'xhigh', selectionBasis: 'best_available_effort' })
  assert.deepEqual(resolveArtificialAnalysisSlug('claude-sonnet-5-high'),
    { canonical: 'claude-sonnet-5', effort: 'high', selectionBasis: 'best_available_effort' })
  assert.deepEqual(resolveArtificialAnalysisSlug('gpt-5.6-luna-low'),
    { canonical: 'gpt-5.6-luna', effort: 'low', selectionBasis: 'best_available_effort' })
  // 点号基名归一（gpt-5.6-luna → gpt-5-6-luna 表项）
  assert.deepEqual(resolveArtificialAnalysisSlug('gpt-5.6-sol-medium'),
    { canonical: 'gpt-5.6-sol', effort: 'medium', selectionBasis: 'best_available_effort' })
  // non-reasoning 变体：default 档 + non_reasoning，不污染 default 复合指数
  assert.deepEqual(resolveArtificialAnalysisSlug('claude-sonnet-5-non-reasoning'),
    { canonical: 'claude-sonnet-5', effort: 'default', selectionBasis: 'non_reasoning' })
  // non-reasoning + low-effort 组合后缀
  assert.deepEqual(resolveArtificialAnalysisSlug('claude-sonnet-4-6-non-reasoning-low-effort'),
    { canonical: 'claude-sonnet-4-6', effort: 'default', selectionBasis: 'non_reasoning' })
  // 剥离后基名仍不在映射表 → null（继续计入 unmappedSlugs）
  assert.equal(resolveArtificialAnalysisSlug('gpt-5-2-medium'), null)
  assert.equal(resolveArtificialAnalysisSlug('totally-unknown-xhigh'), null)
})

test('Artificial Analysis effort variants attach to canonical without clobbering default evidence', () => {
  const models = [
    { slug: 'claude-opus-5', evaluations: {
      artificial_analysis_intelligence_index: 63,
      artificial_analysis_coding_index: 78,
      artificial_analysis_agentic_index: 59,
    } },
    { slug: 'claude-opus-5-xhigh', evaluations: {
      artificial_analysis_intelligence_index: 71,
      artificial_analysis_coding_index: 84,
      artificial_analysis_agentic_index: 66,
    } },
  ]
  const { profiles, unmappedSlugs } = extractAaLlm(models, 4.1)

  assert.deepEqual(unmappedSlugs, [])
  const evidence = profiles['claude-opus-5'].benchmarkEvidence
  // 同一 canonical 下 default 与 xhigh 两档证据并存
  assert.equal(evidence.length, 6)
  const byKey = Object.fromEntries(evidence.map(e => [`${e.domain}:${e.effort}`, e]))
  assert.equal(byKey['overall:default'].rawValue, 63)
  assert.equal(byKey['overall:default'].selectionBasis, 'composite_index')
  assert.equal(byKey['overall:xhigh'].rawValue, 71)
  assert.equal(byKey['overall:xhigh'].selectionBasis, 'best_available_effort')
  // xhigh 分更高 → cohortPercentile 不低于 default 档（对齐各自 rawValue，而非一律取 default 分）
  assert.ok(byKey['overall:xhigh'].cohortPercentile >= byKey['overall:default'].cohortPercentile)
  assert.equal(byKey['coding:xhigh'].rawValue, 84)
  assert.equal(byKey['agentic:xhigh'].rawValue, 66)
  // sourceModel/sourceUrl 保留各自真实 slug，便于回溯
  assert.equal(byKey['overall:xhigh'].sourceModel, 'claude-opus-5-xhigh')
  assert.match(byKey['overall:xhigh'].sourceUrl, /claude-opus-5-xhigh$/)
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
  const html = renderBenchmarkChart(validated.rows, validated.comparisons, validated.qualityTiers)
  assert.match(html, /Artificial Analysis/)
  assert.match(html, /图像 Arena Elo/)
})
