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
  'muse-spark-1-2': 'muse-spark-1.2',
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
  'muse-spark-1-2': 'muse-spark-1.2',
  'deepseek-v4-flash': 'deepseek-v4-flash',
  'deepseek-v4-flash-base': 'deepseek-v4-flash',
  'deepseek-v4-flash-high': 'deepseek-v4-flash',
  'deepseek-v4-flash-max': 'deepseek-v4-flash',
  'deepseek-v4-pro': 'deepseek-v4-pro',
  'deepseek-v4-pro-base': 'deepseek-v4-pro',
  'deepseek-v4-pro-high': 'deepseek-v4-pro',
  'deepseek-v4-pro-max': 'deepseek-v4-pro',
  // DeepSeek 新版本发布后，部分榜单可能使用 -MMDD 日期后缀 slug
  'deepseek-v4-pro-0813': 'deepseek-v4-pro',
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
  if (canonical.startsWith('deepseek-')) {
    // deepseek 渠道常以 -MMDD 发布版本别名，注册表已放宽到 -\d{4,8}
    return `(?:^|[-/])${canonical}(?:-\\d{4}-\\d{2}-\\d{2}|-\\d{4,8})?(?=$|@)`
  }
  // 默认
  return `(?:^|[-/])${canonical}(?=$|@)`
}

/**
 * 解析模型名的版本与后缀，用于跨家族一致的排序。
 * 命名形态不一：claude-opus-5（版本在末位）、gpt-5.6-sol（版本居中）、muse-spark-1.2（带小数）。
 * 提取首个数字段作为版本（主.次），并识别预发布后缀（preview/beta/alpha/rc 等）。
 * 返回 { family, nums, suffix }：family 为版本号之前的家族名，nums 为版本数值数组，suffix 为版本后的小写尾部。
 */
function parseModelVersion(model) {
  const s = String(model).toLowerCase()
  const m = s.match(/\d+(?:\.\d+)*/)
  if (!m) return { family: s, nums: [], suffix: '' }
  const family = s.slice(0, m.index).replace(/[-/]+$/, '')
  const nums = m[0].split('.').map(Number)
  const suffix = s.slice(m.index + m[0].length).replace(/^[-/]+/, '')
  return { family, nums, suffix }
}

// 预发布后缀权重：release(空) 最高，preview/beta/alpha/rc 等排在正式版之后。
const PRERELEASE_TAGS = ['preview', 'beta', 'alpha', 'rc', 'pre', 'dev', 'nightly']

function suffixRank(suffix) {
  if (!suffix) return -1 // 无后缀（正式版）排最前
  const idx = PRERELEASE_TAGS.findIndex(tag => suffix === tag || suffix.startsWith(tag + '-') || suffix.startsWith(tag + '.'))
  return idx >= 0 ? idx : PRERELEASE_TAGS.length + 1 // 其他非版本后缀(如 sol/flash/code)不视为预发布
}

/**
 * 版本感知的模型比较器：家族字典序 → 版本号降序 → 预发布后缀置后 → 其余后缀字典序。
 * 用于 registry benchmarkProfiles 与图表图例，保证 opus-5 排在 opus-4.8 前、preview 排在正式版后。
 */
export function compareCanonicalModels(a, b) {
  const pa = parseModelVersion(a)
  const pb = parseModelVersion(b)
  if (pa.family !== pb.family) return pa.family.localeCompare(pb.family)
  // 版本号降序：逐段比较，缺段视为 0
  const len = Math.max(pa.nums.length, pb.nums.length)
  for (let i = 0; i < len; i++) {
    const x = pa.nums[i] ?? 0
    const y = pb.nums[i] ?? 0
    if (x !== y) return y - x
  }
  // 同版本：正式版（无后缀/非预发布后缀）优先，预发布后缀排后
  const ra = suffixRank(pa.suffix)
  const rb = suffixRank(pb.suffix)
  const aPre = ra >= 0 && ra < PRERELEASE_TAGS.length
  const bPre = rb >= 0 && rb < PRERELEASE_TAGS.length
  if (aPre !== bPre) return aPre ? 1 : -1 // preview 排后
  if (aPre && bPre && ra !== rb) return ra - rb
  return pa.suffix.localeCompare(pb.suffix)
}
