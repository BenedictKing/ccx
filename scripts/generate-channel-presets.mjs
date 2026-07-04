import { readFileSync, writeFileSync } from 'node:fs'
import { dirname, join } from 'node:path'
import { fileURLToPath } from 'node:url'

const root = dirname(dirname(fileURLToPath(import.meta.url)))
const reasoningEfforts = ['none', 'low', 'medium', 'high', 'xhigh', 'max']
const textVerbosityValues = ['low', 'medium', 'high']

const channelPresetDefaults = {
  modelMapping: {},
  reasoningMapping: {},
  reasoningParamStyle: '',
  authHeader: '',
  passbackReasoningContent: false,
  passbackThinkingBlocks: false,
  stripEmptyTextBlocks: false,
  normalizeSystemRoleToTopLevel: false,
  stripImageGenerationTool: false,
  normalizeNonstandardChatRoles: false,
  noVision: false,
  noVisionModels: [],
  visionFallbackModel: '',
}

const codexResponsesDefaults = {
  modelMapping: {},
  reasoningMapping: {},
  reasoningParamStyle: '',
  codexNativeToolPassthrough: false,
  codexToolCompat: true,
  stripCodexClientTools: true,
  stripImageGenerationTool: false,
  normalizeNonstandardChatRoles: false,
  noVision: false,
  noVisionModels: [],
  visionFallbackModel: '',
}

const openaiMessagesDefaults = {
  modelMapping: {},
  reasoningMapping: {},
  fastMode: false,
  textVerbosity: 'medium',
}

const generatedSources = [
  {
    sourceFile: 'codex-responses.json',
    kind: 'codexResponses',
    collectionKey: 'providers',
    defaults: codexResponsesDefaults,
    exportName: 'codexResponsesPresets',
    interfaceName: 'CodexResponsesPreset',
    typePrefix: 'CodexResponses',
    webTsPath: 'frontend/src/generated/codexResponsesPresets.ts',
    desktopTsPath: 'desktop/frontend/src/generated/codex-responses-presets.ts',
    goPath: 'desktop/internal/channelpreset/generated_codex_responses_presets.go',
    goFunctionName: 'generatedCodexResponsesTargetConfigs',
  },
  {
    sourceFile: 'claude-messages.json',
    kind: 'channelTarget',
    collectionKey: 'providers',
    defaults: channelPresetDefaults,
    exportName: 'claudeMessagesPresets',
    interfaceName: 'ClaudeMessagesPreset',
    typePrefix: 'ClaudeMessages',
    webTsPath: 'frontend/src/generated/claudeMessagesPresets.ts',
    desktopTsPath: 'desktop/frontend/src/generated/claude-messages-presets.ts',
    goPath: 'desktop/internal/channelpreset/generated_claude_messages_presets.go',
    goFunctionName: 'generatedClaudeMessagesTargetConfigs',
    compatibilityFields: true,
  },
  {
    sourceFile: 'openai-chat.json',
    kind: 'channelTarget',
    collectionKey: 'providers',
    defaults: channelPresetDefaults,
    exportName: 'openaiChatPresets',
    interfaceName: 'OpenAIChatPreset',
    typePrefix: 'OpenAIChat',
    webTsPath: 'frontend/src/generated/openaiChatPresets.ts',
    desktopTsPath: 'desktop/frontend/src/generated/openai-chat-presets.ts',
    goPath: 'desktop/internal/channelpreset/generated_openai_chat_presets.go',
    goFunctionName: 'generatedOpenAIChatTargetConfigs',
  },
  {
    sourceFile: 'openai-messages.json',
    kind: 'mappingPreset',
    collectionKey: 'presets',
    defaults: openaiMessagesDefaults,
    exportName: 'openaiMessagesPresets',
    interfaceName: 'OpenAIMessagesPreset',
    typePrefix: 'OpenAIMessages',
    webTsPath: 'frontend/src/generated/openaiMessagesPresets.ts',
    desktopTsPath: 'desktop/frontend/src/generated/openai-messages-presets.ts',
  },
]

function readPresetSource(sourceFile) {
  const sourcePath = join(root, 'shared/channel-presets', sourceFile)
  return JSON.parse(readFileSync(sourcePath, 'utf8'))
}

function quote(value) {
  return JSON.stringify(value)
}

function hasOwn(object, key) {
  return Object.prototype.hasOwnProperty.call(object, key)
}

function normalizedCollection(source, collectionKey, defaults) {
  const collection = source[collectionKey] || {}
  return Object.fromEntries(
    Object.entries(collection).map(([name, preset]) => {
      const allowedKeys = new Set([...Object.keys(defaults), 'rateLimitRpm', 'serviceType', 'normalizeMetadataUserId', 'stripBillingHeader'])
      const normalized = { ...defaults }
      for (const [key, value] of Object.entries(preset)) {
        if (allowedKeys.has(key)) {
          normalized[key] = value
        }
      }
      if (hasOwn(defaults, 'modelMapping') || hasOwn(preset, 'modelMapping')) {
        normalized.modelMapping = preset.modelMapping || {}
      }
      if (hasOwn(defaults, 'reasoningMapping') || hasOwn(preset, 'reasoningMapping')) {
        normalized.reasoningMapping = preset.reasoningMapping || {}
      }
      if (hasOwn(defaults, 'noVisionModels') || hasOwn(preset, 'noVisionModels')) {
        normalized.noVisionModels = preset.noVisionModels || []
      }
      return [name, normalized]
    }),
  )
}

function formatTs(config, source) {
  const normalized = normalizedCollection(source, config.collectionKey, config.defaults)
  const json = JSON.stringify(normalized, null, 2)
  const compatibilityInterfaceFields = config.compatibilityFields
    ? '  normalizeMetadataUserId?: boolean\n  stripBillingHeader?: boolean\n'
    : ''
  if (config.kind === 'mappingPreset') {
    return `// Code generated by scripts/generate-channel-presets.mjs; DO NOT EDIT.

export type ${config.typePrefix}ReasoningEffort = ${reasoningEfforts.map(quote).join(' | ')}
export type ${config.typePrefix}TextVerbosity = ${textVerbosityValues.map(quote).join(' | ')}

export interface ${config.interfaceName} {
  modelMapping: Record<string, string>
  reasoningMapping: Partial<Record<string, ${config.typePrefix}ReasoningEffort>>
  fastMode: boolean
  textVerbosity: ${config.typePrefix}TextVerbosity
}

export const ${config.exportName}: Record<string, ${config.interfaceName}> = ${json}
`
  }

  if (config.kind === 'codexResponses') {
    return `// Code generated by scripts/generate-channel-presets.mjs; DO NOT EDIT.

export type ${config.typePrefix}ReasoningEffort = ${reasoningEfforts.map(quote).join(' | ')}
export type ${config.typePrefix}ReasoningParamStyle = '' | 'reasoning' | 'reasoning_effort' | 'thinking'

export interface ${config.interfaceName} {
  modelMapping: Record<string, string>
  reasoningMapping: Partial<Record<string, ${config.typePrefix}ReasoningEffort>>
  reasoningParamStyle: ${config.typePrefix}ReasoningParamStyle
  serviceType?: 'openai' | 'gemini' | 'claude' | 'responses' | 'copilot'
  codexNativeToolPassthrough: boolean
  codexToolCompat: boolean
  stripCodexClientTools: boolean
  stripImageGenerationTool: boolean
  normalizeNonstandardChatRoles: boolean
  noVision: boolean
  noVisionModels: string[]
  visionFallbackModel: string
  rateLimitRpm?: number
}

export const ${config.exportName}: Record<string, ${config.interfaceName}> = ${json}
`
  }

  return `// Code generated by scripts/generate-channel-presets.mjs; DO NOT EDIT.

export type ${config.typePrefix}ReasoningEffort = ${reasoningEfforts.map(quote).join(' | ')}
export type ${config.typePrefix}ReasoningParamStyle = '' | 'reasoning' | 'reasoning_effort' | 'thinking'

export interface ${config.interfaceName} {
  modelMapping: Record<string, string>
  reasoningMapping: Partial<Record<string, ${config.typePrefix}ReasoningEffort>>
  reasoningParamStyle: ${config.typePrefix}ReasoningParamStyle
  serviceType?: 'openai' | 'gemini' | 'claude' | 'responses' | 'copilot'
  authHeader: '' | 'auto' | 'bearer' | 'x-api-key'
  passbackReasoningContent: boolean
  passbackThinkingBlocks: boolean
  stripEmptyTextBlocks: boolean
  normalizeSystemRoleToTopLevel: boolean
${compatibilityInterfaceFields}  stripImageGenerationTool: boolean
  normalizeNonstandardChatRoles: boolean
  noVision: boolean
  noVisionModels: string[]
  visionFallbackModel: string
  rateLimitRpm?: number
}

export const ${config.exportName}: Record<string, ${config.interfaceName}> = ${json}
`
}

function formatGoStringMap(values) {
  const entries = Object.entries(values || {})
  if (!entries.length) return ''
  return `map[string]string{${entries.map(([key, value]) => `${quote(key)}: ${quote(value)}`).join(', ')}}`
}

function formatGoStringSlice(values) {
  if (!values?.length) return ''
  return `[]string{${values.map(quote).join(', ')}}`
}

function formatGoConfig(preset) {
  const fields = []
  const modelMapping = formatGoStringMap(preset.modelMapping)
  const reasoningMapping = formatGoStringMap(preset.reasoningMapping)
  const noVisionModels = formatGoStringSlice(preset.noVisionModels)

  if (modelMapping) fields.push(`ModelMapping: ${modelMapping}`)
  if (reasoningMapping) fields.push(`ReasoningMapping: ${reasoningMapping}`)
  if (preset.serviceType) fields.push(`ServiceType: ${quote(preset.serviceType)}`)
  if (preset.reasoningParamStyle) fields.push(`ReasoningParamStyle: ${quote(preset.reasoningParamStyle)}`)
  if (preset.authHeader) fields.push(`AuthHeader: ${quote(preset.authHeader)}`)
  if (preset.passbackReasoningContent) fields.push('PassbackReasoningContent: true')
  if (preset.passbackThinkingBlocks) fields.push('PassbackThinkingBlocks: true')
  if (preset.normalizeSystemRoleToTopLevel) fields.push('NormalizeSystemRoleToTopLevel: true')
  if (hasOwn(preset, 'normalizeMetadataUserId')) fields.push(`NormalizeMetadataUserId: boolRef(${Boolean(preset.normalizeMetadataUserId)})`)
  if (preset.stripBillingHeader) fields.push('StripBillingHeader: true')
  if (preset.noVision) fields.push('NoVision: true')
  if (noVisionModels) fields.push(`NoVisionModels: ${noVisionModels}`)
  if (preset.visionFallbackModel) fields.push(`VisionFallbackModel: ${quote(preset.visionFallbackModel)}`)
  if (preset.stripEmptyTextBlocks) fields.push('StripEmptyTextBlocks: true')
  if (preset.codexNativeToolPassthrough) fields.push('CodexNativeToolPassthrough: true')
  if (hasOwn(preset, 'codexToolCompat')) fields.push(`CodexToolCompat: boolRef(${Boolean(preset.codexToolCompat)})`)
  if (hasOwn(preset, 'stripCodexClientTools')) fields.push(`StripCodexClientTools: boolRef(${Boolean(preset.stripCodexClientTools)})`)
  if (preset.stripImageGenerationTool) fields.push('StripImageGenerationTool: true')
  if (preset.normalizeNonstandardChatRoles) fields.push('NormalizeNonstandardChatRoles: true')
  if (preset.rateLimitRpm) fields.push(`RateLimitRPM: ${Number(preset.rateLimitRpm)}`)

  return `channelTargetConfig{${fields.join(', ')}}`
}

function formatGo(config, source) {
  const providers = source[config.collectionKey] || {}
  const providerNames = Object.keys(providers)
  const providerKeyWidth = providerNames.reduce((width, provider) => Math.max(width, quote(provider).length), 0)
  const lines = [
    'package channelpreset',
    '',
    '// Code generated by scripts/generate-channel-presets.mjs; DO NOT EDIT.',
    '',
    `func ${config.goFunctionName}() map[string]channelTargetConfig {`,
    '\treturn map[string]channelTargetConfig{',
  ]

  for (const [provider, preset] of Object.entries(providers)) {
    const providerKey = quote(provider)
    const providerPadding = ' '.repeat(providerKeyWidth - providerKey.length + 1)
    lines.push(`\t\t${providerKey}:${providerPadding}${formatGoConfig(preset)},`)
  }

  lines.push('\t}', '}')
  return `${lines.join('\n')}\n`
}

for (const config of generatedSources) {
  const source = readPresetSource(config.sourceFile)
  const ts = formatTs(config, source)
  writeFileSync(join(root, config.webTsPath), ts)
  writeFileSync(join(root, config.desktopTsPath), ts)
  if (config.goPath) {
    writeFileSync(join(root, config.goPath), formatGo(config, source))
  }
}
