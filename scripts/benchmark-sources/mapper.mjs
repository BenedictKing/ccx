/**
 * 模型名称映射模块
 *
 * 将 deepswe / benchlm.ai 的模型名映射到 CCX 注册表中的 canonicalModel 和 patterns。
 *
 * 映射规则：
 * - deepswe 使用连字符: "gpt-5-6-sol", "claude-opus-4-8", "claude-fable-5"
 * - benchlm.ai 使用 slug: "claude-opus-4-8", "gpt-5-6-terra"
 * - CCX 使用点号: "gpt-5.6-sol", "claude-opus-4-8"
 */

/**
 * deepswe 模型名 -> CCX canonicalModel 映射
 */
export const DEEPSWE_MODEL_MAP = {
  'gpt-5-6-sol': 'gpt-5.6-sol',
  'gpt-5-6-terra': 'gpt-5.6-terra',
  'gpt-5-6-luna': 'gpt-5.6-luna',
  'gpt-5-5': 'gpt-5.5',
  'gpt-5-4': 'gpt-5.4',
  'claude-opus-4-8': 'claude-opus-4-8',
  'claude-opus-5': 'claude-opus-5',
  'claude-fable-5': 'claude-fable-5',
  'claude-sonnet-5': 'claude-sonnet-5',
  'claude-sonnet-4-6': 'claude-sonnet-4-6',
  'claude-sonnet-4-6-thinking': 'claude-sonnet-4-6',
  'glm-5-2': 'glm-5.2',
  'kimi-k2-7-code': 'kimi-k2.7-code',
  'kimi-k2-7-code-highspeed': 'kimi-k2.7-code',
  'kimi-k3': 'kimi-k3',
  'kimi-3': 'kimi-k3',
  'gemini-3-1-pro': 'gemini-3.1-pro',
  'gemini-3-flash': 'gemini-3-flash',
  'gemini-3-5-flash': 'gemini-3.5-flash',
  'gemini-3-6-flash': 'gemini-3.6-flash',
  'gemini-3-1-pro-preview': 'gemini-3.1-pro',
  'gemini-3-flash-preview': 'gemini-3-flash',
  'claude-haiku-4-5': 'claude-haiku-4.5',
  'gpt-5-4-mini': 'gpt-5.4-mini',
  'gpt-5-4-nano': 'gpt-5.4-nano',
  'gpt-5-4-openai-compact': 'gpt-5.4',
  'grok-4-5': 'grok-4.5',
  'muse-spark-1-1': 'muse-spark-1.1',
}

/**
 * benchlm.ai 模型 slug -> CCX canonicalModel 映射
 * 注意：benchlm.ai 可能使用简称，如 'claude-fable' 而不是 'claude-fable-5'
 */
export const BENCHLM_MODEL_MAP = {
  'claude-opus-4-8': 'claude-opus-4-8',
  'claude-opus-5': 'claude-opus-5',
  'gpt-5-6-terra': 'gpt-5.6-terra',
  'gpt-5-6-sol': 'gpt-5.6-sol',
  'gpt-5-6-luna': 'gpt-5.6-luna',
  'gpt-5-5': 'gpt-5.5',
  'gpt-5-4': 'gpt-5.4',
  'claude-fable': 'claude-fable-5',        // benchlm 使用简称
  'claude-fable-5': 'claude-fable-5',
  'claude-sonnet-5': 'claude-sonnet-5',
  'claude-sonnet-4-6': 'claude-sonnet-4-6',
  'glm-5-2': 'glm-5.2',
  'kimi-k2-7-code': 'kimi-k2.7-code',
  'kimi-3': 'kimi-k3',
  'gemini-3-5-flash': 'gemini-3.5-flash',
  'claude-haiku-4-5': 'claude-haiku-4.5',
  'gpt-5-4-mini': 'gpt-5.4-mini',
  'grok-4-5': 'grok-4.5',
  'muse-spark-1-1': 'muse-spark-1.1',
  'deepseek-v4-flash': 'deepseek-v4-flash',
  'deepseek-v4-flash-base': 'deepseek-v4-flash',
  'deepseek-v4-flash-high': 'deepseek-v4-flash',
  'deepseek-v4-flash-max': 'deepseek-v4-flash',
  'deepseek-v4-pro': 'deepseek-v4-pro',
  'deepseek-v4-pro-base': 'deepseek-v4-pro',
  'deepseek-v4-pro-high': 'deepseek-v4-pro',
  'deepseek-v4-pro-max': 'deepseek-v4-pro',
}

/**
 * benchlm.ai 分类名 -> CCX categoryScores key 映射
 */
export const BENCHLM_CATEGORY_MAP = {
  knowledge: 'knowledge',
  math: 'math',
  coding: 'coding',
  agentic: 'agentic',
  multimodalGrounded: 'multimodal',
}

/**
 * 将 deepswe 模型名转换为 CCX canonicalModel
 * @param {string} deepsweModel
 * @returns {string|null}
 */
export function deepsweToCanonical(deepsweModel) {
  return DEEPSWE_MODEL_MAP[deepsweModel] || null
}

/**
 * 将 benchlm.ai slug 转换为 CCX canonicalModel
 * @param {string} benchlmSlug
 * @returns {string|null}
 */
export function benchlmToCanonical(benchlmSlug) {
  return BENCHLM_MODEL_MAP[benchlmSlug] || null
}

/**
 * 将 benchlm.ai 分类名转换为 CCX categoryScores key
 * @param {string} benchlmCategory
 * @returns {string|null}
 */
export function benchlmCategoryToCcx(benchlmCategory) {
  return BENCHLM_CATEGORY_MAP[benchlmCategory] || null
}

/**
 * 生成 deepswe 模型名对应的 pattern
 * @param {string} deepsweModel
 * @returns {string|null}
 */
export function deepsweModelToPattern(deepsweModel) {
  const canonical = deepsweToCanonical(deepsweModel)
  if (!canonical) return null

  return canonicalModelToPattern(canonical)
}

/**
 * 生成 CCX canonicalModel 对应的 pattern。
 * @param {string} canonical
 * @returns {string|null}
 */
export function canonicalModelToPattern(canonical) {
  if (typeof canonical !== 'string' || canonical.trim() === '') return null

  // 根据模型类型生成 pattern
  if (canonical.startsWith('claude-')) {
    // claude-opus-4-8 -> (?:^|[-/])claude-opus-4-8(?:-\d{4}-\d{2}-\d{2}|-\d{6,8})?(?=$|@)
    return `(?:^|[-/])${canonical}(?:-\\d{4}-\\d{2}-\\d{2}|-\\d{6,8})?(?=$|@)`
  }
  if (canonical.startsWith('gpt-')) {
    // gpt-5.6-sol -> (?:^|[-/])gpt-5\.6-sol(?=$|@)
    const escaped = canonical.replace(/\./g, '\\.')
    return `(?:^|[-/])${escaped}(?=$|@)`
  }
  if (canonical.startsWith('glm-')) {
    // glm-5.2 -> (?:^|[-/])glm-5\.2(?:-\d{4}-\d{2}-\d{2}|-\d{6,8})?(?=$|@)
    const escaped = canonical.replace(/\./g, '\\.')
    return `(?:^|[-/])${escaped}(?:-\\d{4}-\\d{2}-\\d{2}|-\\d{6,8})?(?=$|@)`
  }
  if (canonical.startsWith('kimi-')) {
    // kimi-k2.7-code -> (?:^|[-/])kimi-k2\.7-code(?:-\d{4}-\d{2}-\d{2}|-\d{6,8})?(?=$|@)
    // 注意：base 名 kimi-k2.7 与套餐名 kimi-for-coding 是别名，由注册表手工维护的多 patterns 合并，
    // 不在此生成器退化（这里只转义点号 + 追加日期快照后缀）。
    const escaped = canonical.replace(/\./g, '\\.')
    return `(?:^|[-/])${escaped}(?:-\\d{4}-\\d{2}-\\d{2}|-\\d{6,8})?(?=$|@)`
  }
  // 默认
  return `(?:^|[-/])${canonical}(?=$|@)`
}
