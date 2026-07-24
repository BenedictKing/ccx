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

const REPO = 'BerriAI/litellm'
const FILE_PATH = 'model_prices_and_context_window.json'

function pricePerMillionOrNull(value) {
  return typeof value === 'number' && Number.isFinite(value)
    ? value * 1_000_000
    : null
}

/**
 * 通过 gh CLI 获取文件内容
 * @returns {Promise<Object>}
 */
export async function fetchLitellmData() {
  console.log(`[litellm] Fetching ${FILE_PATH} via gh CLI...`)

  try {
    // 1. 获取文件元数据（拿到 git blob sha）
    const metaOutput = execFileSync(
      'gh',
      ['api', `repos/${REPO}/contents/${FILE_PATH}`, '--jq', '.sha'],
      { encoding: 'utf8', maxBuffer: 10 * 1024 * 1024, timeout: 20_000 }
    )
    const currentSha = metaOutput.trim()
    const cachedSha = getSimpleCache('litellm:blobSha')

    if (cachedSha === currentSha) {
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
 * litellm 使用带日期的版本名，CCX 使用不带日期的 canonical 名
 */
export const LITELLM_MODEL_MAP = {
  'claude-opus-4-8': 'claude-opus-4-8',
  'claude-fable-5': 'claude-fable-5',
  'claude-sonnet-5': 'claude-sonnet-5',
  'claude-sonnet-4-6': 'claude-sonnet-4-6',
  'claude-haiku-4-5': 'claude-haiku-4.5',
  'gpt-5.6-sol': 'gpt-5.6-sol',
  'gpt-5.6-terra': 'gpt-5.6-terra',
  'gpt-5.6-luna': 'gpt-5.6-luna',
  'gpt-5.6': 'gpt-5.6',
  'gpt-5.5': 'gpt-5.5',
  'gpt-5.4': 'gpt-5.4',
  'gpt-5.4-mini': 'gpt-5.4-mini',
  'fireworks_ai/glm-5p2': 'glm-5.2',
  'kimi-k2.7-code': 'kimi-k2.7-code',
  'gemini-3.5-flash': 'gemini-3.5-flash',
  'gemini-3.1-pro': 'gemini-3.1-pro',
  'gemini-3-flash': 'gemini-3-flash',
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
 * 主函数：抓取并转换 litellm 数据
 * @param {Object} modelMap - litellm 模型名 -> CCX canonicalModel 映射
 * @returns {Promise<Object>} - {canonicalModel: {contextWindowTokens, maxOutputTokens, pricing, supports}}
 */
export async function fetchLitellmModelInfo(modelMap = LITELLM_MODEL_MAP) {
  const data = await fetchLitellmData()
  if (data === null) {
    console.log(`[litellm] No changes detected, skipping processing`)
    return { _unchanged: true }
  }
  const result = extractModelInfo(data, modelMap)
  console.log(`[litellm] Extracted data for ${Object.keys(result).length} models`)
  return result
}
