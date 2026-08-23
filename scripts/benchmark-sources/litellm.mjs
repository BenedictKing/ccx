/**
 * litellm 价格/上下文数据抓取器
 *
 * 从 https://github.com/BerriAI/litellm 抓取 model_prices_and_context_window.json
 * 使用 gh CLI 的 git blob API 下载（避免 raw.githubusercontent.com 的网络限制）
 *
 * 数据结构：
 * - {model_name: {max_tokens, max_input_tokens, max_output_tokens, input_cost_per_token, output_cost_per_token, ...}}
 *
 * 注意：litellm 的模型名与 CCX 不完全一致，需要映射
 */

import { execFileSync } from 'node:child_process'
import { getSimpleCache, setSimpleCache } from './http-cache.mjs'
import { warnNewModelCandidates } from './mapper.mjs'

const REPO = 'BerriAI/litellm'
const FILE_PATH = 'model_prices_and_context_window.json'

/**
 * litellm 的价格是 per-token 的极小浮点（如 5e-8），乘 1e6 后会留下
 * 二进制浮点尾巴（0.05 -> 0.049999999999999996），导致每次生成都产生伪 diff。
 * 用 12 位有效数字归一化：既消除尾巴，也不会像定点 round 那样把极小价格截成 0。
 */
function pricePerMillionOrNull(value) {
  if (typeof value !== 'number' || !Number.isFinite(value)) return null
  const perMillion = value * 1_000_000
  return perMillion === 0 ? 0 : Number(perMillion.toPrecision(12))
}

/**
 * 通过 gh CLI 获取文件内容
 * @param {boolean} force - 忽略 blob SHA 缓存，强制重新拉取并处理
 * @returns {Promise<Object|null>}
 */
export async function fetchLitellmData(force = false) {
  console.log(`[litellm] Fetching ${FILE_PATH} via gh CLI${force ? ' (forced)' : ''}...`)

  try {
    // 1. 获取文件元数据（拿到 git blob sha）
    const metaOutput = execFileSync(
      'gh',
      ['api', `repos/${REPO}/contents/${FILE_PATH}`, '--jq', '.sha'],
      { encoding: 'utf8', maxBuffer: 10 * 1024 * 1024, timeout: 20_000 }
    )
    const currentSha = metaOutput.trim()
    const cachedSha = getSimpleCache('litellm:blobSha')

    if (!force && cachedSha === currentSha) {
      console.log(`[litellm] Blob SHA unchanged (${currentSha.slice(0, 8)}), skipping fetch`)
      return null // 表示无变更
    }

    console.log(`[litellm] Blob SHA changed: ${cachedSha?.slice(0, 8) || '(none)'} → ${currentSha.slice(0, 8)}`)
    setSimpleCache('litellm:blobSha', currentSha)

    // 2. 获取 git_url（需要用于 blob API）
    const gitUrlOutput = execFileSync(
      'gh',
      ['api', `repos/${REPO}/contents/${FILE_PATH}`, '--jq', '.git_url'],
      { encoding: 'utf8', maxBuffer: 10 * 1024 * 1024, timeout: 20_000 }
    )
    const gitUrl = gitUrlOutput.trim()

    // 3. 获取 blob 内容（base64 编码）
    const blobOutput = execFileSync(
      'gh',
      ['api', gitUrl.replace('https://api.github.com/', ''), '--jq', '.content'],
      { encoding: 'utf8', maxBuffer: 50 * 1024 * 1024, timeout: 30_000 }
    )
    const base64Content = blobOutput.trim()

    // 4. base64 解码
    const content = Buffer.from(base64Content, 'base64').toString('utf8')
    return JSON.parse(content)
  } catch (err) {
    console.error(`[litellm] Failed to fetch via gh:`, err.message)
    throw err
  }
}

/**
 * litellm 模型名 -> CCX canonicalModel 映射
 *
 * 重要：canonicalModel 的形式必须能匹配 registry.upstreamCapabilities 对应条目的 pattern
 * （mergeLitellmData 用 `new RegExp(pattern).test(canonical)` 查找）。
 * 因此 claude 系列用连字符（pattern 为 claude-haiku-4-5），gpt/glm/mimo 用点号（pattern 为 gpt-5\.2）。
 * litellm slug 可用带日期别名或 provider 前缀，只要 litellm 数据里存在该 key 即可。
 */
export const LITELLM_MODEL_MAP = {
  // Claude（pattern 用连字符；旧版 value 曾误用点号导致匹配失败，已修正）
  'claude-opus-4-8': 'claude-opus-4-8',
  'claude-opus-5': 'claude-opus-5',
  'claude-sonnet-5': 'claude-sonnet-5',
  'claude-sonnet-4-6': 'claude-sonnet-4-6',
  'claude-haiku-4-5-20251001': 'claude-haiku-4-5',
  'claude-sonnet-4-5-20250929': 'claude-sonnet-4-5',
  'claude-opus-4-5': 'claude-opus-4-5',
  'claude-opus-4-6': 'claude-opus-4-6',
  'claude-opus-4-7': 'claude-opus-4-7',
  'claude-fable-5': 'claude-fable-5',
  // GPT（pattern 用点号）
  'gpt-5.6-sol': 'gpt-5.6-sol',
  'gpt-5.6-terra': 'gpt-5.6-terra',
  'gpt-5.6-luna': 'gpt-5.6-luna',
  'gpt-5.6': 'gpt-5.6',
  'gpt-5.5': 'gpt-5.5',
  'gpt-5.5-pro': 'gpt-5.5-pro',
  'gpt-5.4': 'gpt-5.4',
  'gpt-5.4-mini': 'gpt-5.4-mini',
  'gpt-5.4-nano': 'gpt-5.4-nano',
  'gpt-5.4-pro': 'gpt-5.4-pro',
  'gpt-5.2': 'gpt-5.2',
  'gpt-5.2-chat-latest': 'gpt-5.2-chat-latest',
  'gpt-5.2-pro': 'gpt-5.2-pro',
  'gpt-5.2-codex': 'gpt-5.2-codex',
  'gpt-5.3-codex': 'gpt-5.3-codex',
  'gpt-5.3-chat-latest': 'gpt-5.3-chat-latest',
  // GLM / Qwen / 其他国产（litellm 多用 provider 前缀 slug）
  'zai/glm-5.3': 'glm-5.3',
  'fireworks_ai/glm-5p2': 'glm-5.2',
  'zai/glm-5.1': 'glm-5.1',
  'dashscope/qwen-coder': 'qwen3-coder',
  'dashscope/qwen-max': 'qwen3-max',
  'moonshot/kimi-k2-thinking': 'kimi-k2-thinking',
  // kimi-k2.7-code 的裸 key 已从上游移除，改用 Cloudflare 托管 key（定价与官方一致 0.95/4/0.19，ctx 256K）
  'cloudflare/@cf/moonshotai/kimi-k2.7-code': 'kimi-k2.7-code',
  // kimi-k3 在 litellm 仅有 Azure AI Foundry 托管价（高于官方直连），故意不映射：
  // 官方价 ¥2/¥20/¥100 手工维护在 registry，mergeLitellmData 会无条件覆盖 pricing。
  'xai/grok-4.5': 'grok-4.5',
  'xai/grok-4.6': 'grok-4.6',
  'meta/muse-spark-1.1': 'muse-spark-1.1',
  'meta/muse-spark-1.2': 'muse-spark-1.2',
  'minimax/MiniMax-M3': 'minimax-m3',
  'openrouter/xiaomi/mimo-v2.5': 'mimo-v2.5',
  'openrouter/xiaomi/mimo-v2.5-pro': 'mimo-v2.5-pro',
  // Gemini（litellm 对未 GA 的型号只提供 -preview 后缀 key，
  // 裸 gemini-3.1-pro / gemini-3-flash 在上游数据中不存在，映射左侧必须用真实 key）
  'gemini-3.5-flash': 'gemini-3.5-flash',
  'gemini-3.7-flash': 'gemini-3.7-flash',
  'gemini-3.6-flash': 'gemini-3.6-flash',
  'gemini-3.1-pro-preview': 'gemini-3.1-pro',
  'gemini-3-flash-preview': 'gemini-3-flash',
}

/**
 * 从 litellm 数据中提取模型信息
 * @param {Object} data - litellm JSON 数据
 * @param {Object} modelMap - litellm 模型名 -> CCX canonicalModel 映射
 * @returns {Object} - {canonicalModel: {contextWindow, maxOutput, pricing, supports}}
 */
export function extractModelInfo(data, modelMap) {
  const result = {}

  const knownBoolean = (info, field) => {
    if (!Object.prototype.hasOwnProperty.call(info, field) || info[field] == null) {
      return undefined
    }
    return Boolean(info[field])
  }

  for (const [litellmName, canonical] of Object.entries(modelMap)) {
    const info = data[litellmName]
    if (!info) continue

    result[canonical] = {
      contextWindowTokens: info.max_input_tokens,
      maxOutputTokens: info.max_output_tokens,
      pricing: {
        unit: 'per_1m_tokens_usd',
        currency: 'USD',
        inputCacheHitPrice: pricePerMillionOrNull(info.cache_read_input_token_cost),
        inputCacheMissPrice: pricePerMillionOrNull(info.input_cost_per_token),
        outputPrice: pricePerMillionOrNull(info.output_cost_per_token),
      },
      supports: {
        reasoning: knownBoolean(info, 'supports_reasoning'),
        vision: knownBoolean(info, 'supports_vision'),
        toolCalls: knownBoolean(info, 'supports_function_calling'),
        parallelFunctionCalling: knownBoolean(info, 'supports_parallel_function_calling'),
        webSearch: knownBoolean(info, 'supports_web_search'),
        promptCaching: knownBoolean(info, 'supports_prompt_caching'),
        nativeStreaming: knownBoolean(info, 'supports_native_streaming'),
      },
      litellmProvider: info.litellm_provider,
      mode: info.mode,
    }
  }

  return result
}

/**
 * 收集 litellm 数据中未被映射收录的模型 key（用于新模型检测）。
 * @param {Object} data - litellm JSON 数据
 * @param {Object} modelMap - litellm 模型名 -> CCX canonicalModel 映射
 * @returns {string[]}
 */
export function collectUnmappedLitellmKeys(data, modelMap) {
  return Object.keys(data || {}).filter(key => !modelMap[key])
}

/**
 * 收集映射表中上游已不存在的 key（改名/下线会导致定价与上下文静默丢失）。
 * @param {Object} data - litellm JSON 数据
 * @param {Object} modelMap - litellm 模型名 -> CCX canonicalModel 映射
 * @returns {string[]}
 */
export function collectMissingMappedKeys(data, modelMap) {
  return Object.keys(modelMap).filter(key => !data?.[key])
}

/**
 * 主函数：抓取并转换 litellm 数据
 * @param {Object} modelMap - litellm 模型名 -> CCX canonicalModel 映射
 * @param {boolean} force - 忽略 SHA 缓存强制重新处理
 * @returns {Promise<{profiles: Object, unmappedKeys: string[], unchanged: boolean}|{unchanged: true}>}
 *   profiles: {canonicalModel: {contextWindowTokens, maxOutputTokens, pricing, supports}}
 */
export async function fetchLitellmModelInfo(modelMap = LITELLM_MODEL_MAP, force = false) {
  const data = await fetchLitellmData(force)
  if (data === null) {
    console.log(`[litellm] No changes detected, skipping processing`)
    return { unchanged: true }
  }
  const result = extractModelInfo(data, modelMap)
  const models = Object.keys(result).sort()
  console.log(`[litellm] Extracted data for ${models.length} models: ${models.join(', ') || '(none)'}`)

  // 新模型检测：上游出现同家族更高版本但未映射的 key 时告警，避免定价/上下文被静默丢弃
  const unmappedKeys = collectUnmappedLitellmKeys(data, modelMap)
  warnNewModelCandidates(unmappedKeys, modelMap, {
    source: 'litellm',
    mapName: 'LITELLM_MODEL_MAP',
    dropped: 'its pricing/context data are dropped',
    // kimi-k3 故意不映射（见 LITELLM_MODEL_MAP 上方注释：官方价手工维护在 registry）
    ignore: candidate => /(?:^|[-/])kimi-k3(?:[-/]|$)/.test(candidate.name.toLowerCase()),
  })
  // 映射 key 失效检测：上游改名/下线会让对应 canonical 的 litellm 数据静默丢失
  for (const key of collectMissingMappedKeys(data, modelMap)) {
    console.warn(`[litellm] [MAPPED-KEY-MISSING] mapped key "${key}" no longer exists upstream; update LITELLM_MODEL_MAP`)
  }
  return { profiles: result, unmappedKeys }
}
