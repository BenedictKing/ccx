import { execFileSync } from 'node:child_process'
import { readFileSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

import { generatePresetManifest } from './generate-preset-manifest.mjs'

const root = dirname(dirname(fileURLToPath(import.meta.url)))
const registryPath = join(root, 'shared/model-registry/ccx_model_registry.json')
const registry = JSON.parse(readFileSync(registryPath, 'utf8'))

const entries = registry.upstreamCapabilities || []
const benchmarkEntries = registry.benchmarkProfiles || []
const pricingUnit = registry.pricingUnit || 'per_1m_tokens_usd'

function compactCapability(entry) {
  const capability = {}
  for (const key of [
    'provider',
    'displayName',
    'description',
    'contextWindowTokens',
    'maxOutputTokens',
    'defaultOutputTokens',
    'recommendedOutputTokens',
    'thinkingMode',
    'reasoningEfforts',
    'capabilities',
    'pricing',
    'sources',
    'paramConstraints',
  ]) {
    if (entry[key] !== undefined) {
      capability[key] = entry[key]
    }
  }
  if (capability.pricing) {
    capability.pricing = {
      unit: capability.pricing.unit || pricingUnit,
      currency: capability.pricing.currency || 'USD',
      ...capability.pricing,
    }
  }
  return capability
}

function expandedPatternMap() {
  const result = {}
  for (const entry of entries) {
    const capability = compactCapability(entry)
    for (const pattern of entry.patterns || []) {
      // 构建时校验：每个 pattern 必须是合法正则（加 i flag 校验，pattern 本身不含 (?i)）
      try {
        new RegExp(pattern, 'i')
      } catch (err) {
        throw new Error(`Invalid regex pattern for ${entry.displayName || entry.provider}: ${pattern}\n  ${err.message}`)
      }
      result[pattern] = capability
    }
  }
  return result
}

function compactBenchmarkProfile(entry) {
  const profile = {}
  for (const key of [
    'canonicalModel',
    'overallScore',
    'categoryScores',
    'benchmarkEvidence',
    'sources',
    'verifiedAt',
    'lane',
    'sharedResults',
    'comparableCategories',
    'totalCategories',
  ]) {
    if (entry[key] !== undefined) {
      profile[key] = entry[key]
    }
  }
  return profile
}

function expandedBenchmarkPatternMap() {
  const result = {}
  for (const entry of benchmarkEntries) {
    const profile = compactBenchmarkProfile(entry)
    for (const pattern of entry.patterns || []) {
      try {
        new RegExp(pattern, 'i')
      } catch (err) {
        throw new Error(`Invalid benchmark regex pattern for ${entry.canonicalModel}: ${pattern}\n  ${err.message}`)
      }
      result[pattern] = profile
    }
  }
  return result
}

function quoteGoString(value) {
  return JSON.stringify(value)
}

function formatGoStringSlice(values) {
  return `[]string{${values.map(quoteGoString).join(', ')}}`
}

function formatGoFloatPointer(value) {
  return `floatPointer(${Number(value)})`
}

function formatGoCapabilities(capabilities) {
  const keys = Object.keys(capabilities || {}).sort()
  if (!keys.length) return ''
  const items = keys.map(key => `${quoteGoString(key)}: ${capabilities[key] ? 'true' : 'false'}`).join(', ')
  return `map[string]bool{${items}}`
}

function formatGoFloatMap(values) {
  const keys = Object.keys(values || {}).sort()
  if (!keys.length) return ''
  const items = keys.map(key => `${quoteGoString(key)}: ${Number(values[key])}`).join(', ')
  return `map[string]float64{${items}}`
}

function formatGoPricing(pricing) {
  if (!pricing) return ''
  const fields = []
  if (pricing.unit) fields.push(`Unit: ${quoteGoString(pricing.unit)}`)
  if (pricing.currency) fields.push(`Currency: ${quoteGoString(pricing.currency)}`)
  if (pricing.inputCacheHitPrice !== undefined) fields.push(`InputCacheHitPrice: ${formatGoFloatPointer(pricing.inputCacheHitPrice)}`)
  if (pricing.inputCacheMissPrice !== undefined) fields.push(`InputCacheMissPrice: ${formatGoFloatPointer(pricing.inputCacheMissPrice)}`)
  if (pricing.outputPrice !== undefined) fields.push(`OutputPrice: ${formatGoFloatPointer(pricing.outputPrice)}`)
  if (pricing.tiers?.length) fields.push(`Tiers: ${formatGoPricingTiers(pricing.tiers)}`)
  return `&ModelPricing{${fields.join(', ')}}`
}

function formatGoPricingTiers(tiers) {
  return `[]ModelPricingTier{${tiers.map(formatGoPricingTier).join(', ')}}`
}

function formatGoPricingTier(tier) {
  const fields = []
  if (tier.label) fields.push(`Label: ${quoteGoString(tier.label)}`)
  if (tier.inputTokensAbove) fields.push(`InputTokensAbove: ${Number(tier.inputTokensAbove)}`)
  if (tier.inputTokensUpTo) fields.push(`InputTokensUpTo: ${Number(tier.inputTokensUpTo)}`)
  if (tier.inputCacheHitPrice !== undefined) fields.push(`InputCacheHitPrice: ${formatGoFloatPointer(tier.inputCacheHitPrice)}`)
  if (tier.inputCacheMissPrice !== undefined) fields.push(`InputCacheMissPrice: ${formatGoFloatPointer(tier.inputCacheMissPrice)}`)
  if (tier.outputPrice !== undefined) fields.push(`OutputPrice: ${formatGoFloatPointer(tier.outputPrice)}`)
  return `ModelPricingTier{${fields.join(', ')}}`
}

function formatGoCapability(capability) {
  const fields = []
  if (capability.Provider) fields.push(`Provider: ${quoteGoString(capability.Provider)}`)
  if (capability.DisplayName) fields.push(`DisplayName: ${quoteGoString(capability.DisplayName)}`)
  if (capability.Description) fields.push(`Description: ${quoteGoString(capability.Description)}`)
  if (capability.ContextWindowTokens) fields.push(`ContextWindowTokens: ${capability.ContextWindowTokens}`)
  if (capability.MaxOutputTokens) fields.push(`MaxOutputTokens: ${capability.MaxOutputTokens}`)
  if (capability.DefaultOutputTokens) fields.push(`DefaultOutputTokens: ${capability.DefaultOutputTokens}`)
  if (capability.RecommendedOutputTokens) fields.push(`RecommendedOutputTokens: ${capability.RecommendedOutputTokens}`)
  if (capability.ThinkingMode) fields.push(`ThinkingMode: ${quoteGoString(capability.ThinkingMode)}`)
  if (capability.ReasoningEfforts?.length) fields.push(`ReasoningEfforts: ${formatGoStringSlice(capability.ReasoningEfforts)}`)
  const capabilities = formatGoCapabilities(capability.Capabilities)
  if (capabilities) fields.push(`Capabilities: ${capabilities}`)
  const pricing = formatGoPricing(capability.Pricing)
  if (pricing) fields.push(`Pricing: ${pricing}`)
  if (capability.Sources?.length) fields.push(`Sources: ${formatGoStringSlice(capability.Sources)}`)
  const paramConstraints = formatGoParamConstraints(capability.ParamConstraints)
  if (paramConstraints) fields.push(`ParamConstraints: ${paramConstraints}`)
  return `UpstreamModelCapability{${fields.join(', ')}}`
}

function toGoCapability(capability) {
  return {
    Provider: capability.provider,
    DisplayName: capability.displayName,
    Description: capability.description,
    ContextWindowTokens: capability.contextWindowTokens,
    MaxOutputTokens: capability.maxOutputTokens,
    DefaultOutputTokens: capability.defaultOutputTokens,
    RecommendedOutputTokens: capability.recommendedOutputTokens,
    ThinkingMode: capability.thinkingMode,
    ReasoningEfforts: capability.reasoningEfforts,
    Capabilities: capability.capabilities,
    Pricing: capability.pricing,
    Sources: capability.sources,
    ParamConstraints: capability.paramConstraints,
  }
}

// formatGoParamConstraints 渲染 config.ModelParamConstraints 字面量。
// 对应厂商文档已知的请求参数硬约束（如 Kimi 固定值采样参数），走 model-registry 刷新链路，
// 不需要重新编译发版即可更新；应用逻辑见 internal/handlers/common/compat_param_constraints.go。
function formatGoParamConstraints(constraints) {
  if (!constraints) return ''
  const fields = []
  if (constraints.fixedParams?.length) fields.push(`FixedParams: ${formatGoStringSlice(constraints.fixedParams)}`)
  if (constraints.toolChoiceRequiredUnsupported) fields.push(`ToolChoiceRequiredUnsupported: true`)
  if (constraints.thinkingFixedValue) {
    const entries = Object.entries(constraints.thinkingFixedValue)
      .map(([k, v]) => `${quoteGoString(k)}: ${typeof v === 'string' ? quoteGoString(v) : JSON.stringify(v)}`)
    fields.push(`ThinkingFixedValue: map[string]interface{}{${entries.join(', ')}}`)
  }
  return `&ModelParamConstraints{${fields.join(', ')}}`
}

function formatGoBenchmarkProfile(profile) {
  const fields = [`CanonicalModel: ${quoteGoString(profile.CanonicalModel)}`]
  if (profile.OverallScore) fields.push(`OverallScore: ${Number(profile.OverallScore)}`)
  const categoryScores = formatGoFloatMap(profile.CategoryScores)
  if (categoryScores) fields.push(`CategoryScores: ${categoryScores}`)
  const benchmarkEvidence = formatGoBenchmarkEvidence(profile.BenchmarkEvidence)
  if (benchmarkEvidence) fields.push(`BenchmarkEvidence: ${benchmarkEvidence}`)
  if (profile.Sources?.length) fields.push(`Sources: ${formatGoStringSlice(profile.Sources)}`)
  if (profile.VerifiedAt) fields.push(`VerifiedAt: ${quoteGoString(profile.VerifiedAt)}`)
  if (profile.Lane) fields.push(`Lane: ${quoteGoString(profile.Lane)}`)
  if (profile.SharedResults) fields.push(`SharedResults: ${Number(profile.SharedResults)}`)
  if (profile.ComparableCategories) fields.push(`ComparableCategories: ${Number(profile.ComparableCategories)}`)
  if (profile.TotalCategories) fields.push(`TotalCategories: ${Number(profile.TotalCategories)}`)
  return `ModelBenchmarkProfile{${fields.join(', ')}}`
}

function formatGoBenchmarkEvidence(items) {
  if (!items?.length) return ''
  const values = items.map(item => {
    const fields = [
      'Benchmark: ' + quoteGoString(item.benchmark),
      'BenchmarkVersion: ' + quoteGoString(item.benchmarkVersion),
      'SourceModel: ' + quoteGoString(item.sourceModel),
      'Domain: ' + quoteGoString(item.domain),
      'Metric: ' + quoteGoString(item.metric),
      'RawValue: ' + Number(item.rawValue),
      'Uncertainty: ' + Number(item.uncertainty || 0),
      'CohortPercentile: ' + Number(item.cohortPercentile),
      'TaskCount: ' + Number(item.taskCount),
      'CohortSize: ' + Number(item.cohortSize),
      'Effort: ' + quoteGoString(item.effort),
      'SelectionBasis: ' + quoteGoString(item.selectionBasis),
      'SourceURL: ' + quoteGoString(item.sourceUrl),
      'CapturedAt: ' + quoteGoString(item.capturedAt),
    ]
    return '{' + fields.join(', ') + '}'
  })
  return '[]ModelBenchmarkEvidence{' + values.join(', ') + '}'
}

function toGoBenchmarkProfile(profile) {
  return {
    CanonicalModel: profile.canonicalModel,
    OverallScore: profile.overallScore,
    CategoryScores: profile.categoryScores,
    BenchmarkEvidence: profile.benchmarkEvidence,
    Sources: profile.sources,
    VerifiedAt: profile.verifiedAt,
    Lane: profile.lane,
    SharedResults: profile.sharedResults,
    ComparableCategories: profile.comparableCategories,
    TotalCategories: profile.totalCategories,
  }
}

function generateGo(patternMap, benchmarkPatternMap) {
  const lines = [
    'package config',
    '',
    '// Code generated by scripts/generate-model-registry.mjs; DO NOT EDIT.',
    '',
    `const ModelPricingUnitPer1MTokensUSD = ${quoteGoString(pricingUnit)}`,
    '',
    'func floatPointer(value float64) *float64 {',
    '\treturn &value',
    '}',
    '',
    'func generatedBuiltinUpstreamModelCapabilities() map[string]UpstreamModelCapability {',
    '\treturn map[string]UpstreamModelCapability{',
  ]
  for (const pattern of Object.keys(patternMap)) {
    lines.push(`\t\t${quoteGoString(pattern)}: ${formatGoCapability(toGoCapability(patternMap[pattern]))},`)
  }
  lines.push('\t}', '}', '', 'func generatedBuiltinModelBenchmarkProfiles() map[string]ModelBenchmarkProfile {', '\treturn map[string]ModelBenchmarkProfile{')
  for (const pattern of Object.keys(benchmarkPatternMap)) {
    lines.push(`\t\t${quoteGoString(pattern)}: ${formatGoBenchmarkProfile(toGoBenchmarkProfile(benchmarkPatternMap[pattern]))},`)
  }
  lines.push('\t}', '}')
  return `${lines.join('\n')}\n`
}

function generateTs(patternMap, benchmarkPatternMap) {
  const json = JSON.stringify(patternMap, null, 2)
  const benchmarkJson = JSON.stringify(benchmarkPatternMap, null, 2)
  return `import type { ModelBenchmarkProfile, UpstreamModelCapability } from '../services/api-types'\n\n// Code generated by scripts/generate-model-registry.mjs; DO NOT EDIT.\nexport const builtinUpstreamModelCapabilities: Record<string, UpstreamModelCapability> = ${json}\n\nexport const builtinModelBenchmarkProfiles: Record<string, ModelBenchmarkProfile> = ${benchmarkJson}\n`
}

function generateDesktopTs(patternMap, benchmarkPatternMap) {
  const json = JSON.stringify(patternMap, null, 2)
  const benchmarkJson = JSON.stringify(benchmarkPatternMap, null, 2)
  return `import type { ModelBenchmarkProfile, UpstreamModelCapability } from '@/services/admin-api'\n\n// Code generated by scripts/generate-model-registry.mjs; DO NOT EDIT.\nexport const builtinUpstreamModelCapabilities: Record<string, UpstreamModelCapability> = ${json}\n\nexport const builtinModelBenchmarkProfiles: Record<string, ModelBenchmarkProfile> = ${benchmarkJson}\n`
}

const patternMap = expandedPatternMap()
const benchmarkPatternMap = expandedBenchmarkPatternMap()
const goOutputPath = join(root, 'backend-go/internal/config/generated_model_registry.go')

writeFileSync(
  goOutputPath,
  generateGo(patternMap, benchmarkPatternMap),
)
execFileSync('gofmt', ['-w', goOutputPath])
writeFileSync(
  join(root, 'frontend/src/generated/modelRegistry.ts'),
  generateTs(patternMap, benchmarkPatternMap),
)
writeFileSync(
  join(root, 'desktop/frontend/src/generated/model-registry.ts'),
  generateDesktopTs(patternMap, benchmarkPatternMap),
)

// 运行时优先加载预置注册表；代码生成时必须同步该副本，避免其覆盖最新定价。
generatePresetManifest()
