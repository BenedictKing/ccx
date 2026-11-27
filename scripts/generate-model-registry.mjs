import { readFileSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = dirname(dirname(fileURLToPath(import.meta.url)))
const registryPath = join(root, 'shared/model-registry/ccx_model_registry.json')
const registry = JSON.parse(readFileSync(registryPath, 'utf8'))

const entries = registry.upstreamCapabilities || []
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
        throw new Error(
          `Invalid regex pattern for ${entry.displayName || entry.provider}: ${pattern}\n  ${err.message}`,
          { cause: err }
        )
      }
      result[pattern] = capability
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
  }
}

function generateGo(patternMap) {
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
  lines.push('\t}', '}')
  return `${lines.join('\n')}\n`
}

function generateTs(patternMap) {
  const json = JSON.stringify(patternMap, null, 2)
  return `import type { UpstreamModelCapability } from '../services/api-types'\n\n// Code generated by scripts/generate-model-registry.mjs; DO NOT EDIT.\nexport const builtinUpstreamModelCapabilities: Record<string, UpstreamModelCapability> = ${json}\n`
}

function generateDesktopTs(patternMap) {
  const json = JSON.stringify(patternMap, null, 2)
  return `import type { UpstreamModelCapability } from '@/services/admin-api'\n\n// Code generated by scripts/generate-model-registry.mjs; DO NOT EDIT.\nexport const builtinUpstreamModelCapabilities: Record<string, UpstreamModelCapability> = ${json}\n`
}

const patternMap = expandedPatternMap()

writeFileSync(
  join(root, 'backend-go/internal/config/generated_model_registry.go'),
  generateGo(patternMap),
)
writeFileSync(
  join(root, 'frontend/src/generated/modelRegistry.ts'),
  generateTs(patternMap),
)
writeFileSync(
  join(root, 'desktop/frontend/src/generated/model-registry.ts'),
  generateDesktopTs(patternMap),
)
