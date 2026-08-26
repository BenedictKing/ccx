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
  'glm-5-3': 'glm-5.3',
  'glm-5-3-flash': 'glm-5.3-flash',
  'glm-5-2': 'glm-5.2',
  'kimi-k2-7-code': 'kimi-k2.7-code',
  'kimi-k2-7-code-highspeed': 'kimi-k2.7-code',
  'kimi-k3': 'kimi-k3',
  'kimi-3': 'kimi-k3',
  'gemini-3-1-pro': 'gemini-3.1-pro',
  'gemini-3-flash': 'gemini-3-flash',
  'gemini-3-5-flash': 'gemini-3.5-flash',
  'gemini-3-7-flash': 'gemini-3.7-flash',
  'gemini-3-6-flash': 'gemini-3.6-flash',
  'gemini-3-1-pro-preview': 'gemini-3.1-pro',
  'gemini-3-flash-preview': 'gemini-3-flash',
  'claude-haiku-4-5': 'claude-haiku-4.5',
  'gpt-5-4-mini': 'gpt-5.4-mini',
  'gpt-5-4-nano': 'gpt-5.4-nano',
  'gpt-5-4-openai-compact': 'gpt-5.4',
  'grok-4-5': 'grok-4.5',
  'grok-4-6': 'grok-4.6',
  'muse-spark-1-1': 'muse-spark-1.1',
  'muse-spark-1-2': 'muse-spark-1.2',
  'deepseek-v4-flash': 'deepseek-v4-flash',
  'deepseek-v4-pro': 'deepseek-v4-pro',
  'qwen3-8-max': 'qwen3.8-max',
}

/**
 * benchlm.ai 模型 slug -> CCX canonicalModel 映射
 * 注意：benchlm.ai 可能使用简称，如 'claude-fable' 而不是 'claude-fable-5'
 */
export const BENCHLM_MODEL_MAP = {
  'claude-opus-4-8': 'claude-opus-4-8',
  'claude-opus-4-7': 'claude-opus-4-7',
  'claude-opus-4-7-adaptive': 'claude-opus-4-7',
  'claude-opus-4-6': 'claude-opus-4-6',
  'claude-opus-4-6-thinking': 'claude-opus-4-6',
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
  'glm-5-3': 'glm-5.3',
  'glm-5-2': 'glm-5.2',
  'kimi-k2-7-code': 'kimi-k2.7-code',
  // 榜单 slug 是 kimi-k3（旧键 kimi-3 是 8/20 审计发现的键名错误，保留作别名）
  'kimi-k3': 'kimi-k3',
  'kimi-3': 'kimi-k3',
  'claude-mythos-5': 'claude-mythos-5',
  'qwen3-8-max': 'qwen3.8-max',
  // preview 当前 displayScore 为 null，映射仅为消除 UNMAPPED 误报，null 不会写入
  'qwen3-8-max-preview': 'qwen3.8-max',
  'gemini-3-7-flash': 'gemini-3.7-flash',
  'gemini-3-5-flash': 'gemini-3.5-flash',
  'claude-haiku-4-5': 'claude-haiku-4.5',
  'gpt-5-4-mini': 'gpt-5.4-mini',
  'grok-4-5': 'grok-4.5',
  'grok-4-6': 'grok-4.6',
  'muse-spark-1-1': 'muse-spark-1.1',
  'muse-spark-1-2': 'muse-spark-1.2',
  'mimo-v2-5': 'mimo-v2.5',
  'mimo-v2-5-pro': 'mimo-v2.5-pro',
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
  'deepseek-v4-flash-0731': 'deepseek-v4-flash',
  // gemini-3-1-pro / gemini-3-flash 已在 benchlm 榜单有公开总分（55.96 / 59.6）
  'gemini-3-6-flash': 'gemini-3.6-flash',
  'gemini-3-1-pro': 'gemini-3.1-pro',
  'gemini-3-flash': 'gemini-3-flash',
  'gemini-3-5-flash-lite': 'gemini-3.5-flash-lite',
  'minimax-m2-7': 'minimax-m2.7',
  'minimax-m3': 'minimax-m3',
  'qwen3-7-max': 'qwen3.7-max',
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
 * 命名形态不一：claude-opus-5（版本在末位）、gpt-5.6-sol（版本居中）、muse-spark-1.2（带小数）、
 * grok-4-6（数据源 slug 连字符分段）。
 * 提取首个数字段作为版本（主.次，`.` 与 `-` 分隔的连续数字段等价），并识别预发布后缀
 * （preview/beta/alpha/rc 等）。
 * 返回 { family, nums, suffix }：family 为版本号之前的家族名，nums 为版本数值数组，suffix 为版本后的小写尾部。
 */
function parseModelVersion(model) {
  const s = String(model).toLowerCase()
  const m = s.match(/\d+(?:[.-]\d+)*/)
  if (!m) return { family: s, nums: [], suffix: '' }
  const family = s.slice(0, m.index).replace(/[-/]+$/, '')
  const nums = m[0].split(/[.-]/).map(Number)
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

/**
 * 解析候选名的版本与后缀（新模型检测专用）。
 * 与 parseModelVersion 一致，但额外从版本尾部落掉后跟 'b' 的参数量段：
 * `qwen3-32b` → [3]、`Qwen3.5-397B-A22B` → [3,5]，避免参数量被当作更高版本。
 */
function parseCandidateVersion(variant) {
  // 先剔除 "NpMb" 形态的参数量段（fireworks 风格，如 qwen3-1p7b = 1.7B；glm-5p2 无 b 不受影响）
  const s = String(variant).toLowerCase().replace(/\d+p\d+b/g, '')
  const m = s.match(/\d+(?:[.-]\d+)*/)
  if (!m) return { family: s, nums: [] }
  const family = s.slice(0, m.index).replace(/[-/]+$/, '')
  const parts = m[0].split(/[.-]/)
  // 每段的结束偏移，用于识别后跟 'b' 的参数量段（32b / 235B）
  let offset = m.index
  const ends = []
  for (const part of parts) {
    offset += part.length
    ends.push(offset)
    offset += 1
  }
  const nums = parts.map(Number)
  while (nums.length > 1 && s[ends[nums.length - 1]] === 'b') {
    nums.pop()
  }
  return { family, nums }
}

// 正常模型版本段不会超过 50：更大的数值是日期/年份/参数量（gpt-5-2025-08-07、grok-4-0709、Qwen3-235B）
const MAX_PLAUSIBLE_VERSION_SEGMENT = 50

/**
 * 展开一个数据源模型名的可比对变体：
 * 原名 + 逐段剥掉 provider 前缀（`zai/glm-6` → `glm-6`、`google_seedream-6` → `seedream-6`），
 * 每个形态再补一条下划线归一为连字符的变体（`gpt_image-3` → `gpt-image-3`）。
 * 仅用于新模型检测的输入预处理；裸名不受影响。
 */
function expandNameVariants(name) {
  const variants = new Set()
  let current = name
  while (true) {
    variants.add(current)
    variants.add(current.replace(/_/g, '-'))
    const sepIndex = current.search(/[/_]/)
    if (sepIndex < 0 || sepIndex === current.length - 1) break
    current = current.slice(sepIndex + 1)
  }
  return [...variants]
}

/**
 * 判断数据源模型名是否被 registry 的任一 pattern 认识。
 * registry patterns 用点号（gpt-5\.6），数据源名常用连字符（gpt-5-6），匹配前把
 * 转义点号放宽为 [.-]；名字侧经 expandNameVariants 剥 provider 前缀并归一下划线。
 * @param {string} name - 数据源模型名
 * @param {string[]} patterns - registry.upstreamCapabilities 的全部 patterns
 * @returns {boolean}
 */
export function matchesAnyRegistryPattern(name, patterns) {
  for (const variant of expandNameVariants(String(name).toLowerCase())) {
    for (const p of patterns || []) {
      try {
        if (new RegExp(p.replace(/\\\./g, '[.-]'), 'i').test(variant)) return true
      } catch {
        // 非法 pattern 跳过
      }
    }
  }
  return false
}

/**
 * 反向对照报告：把数据源的未映射模型名与 registry patterns 全量对照，分两类输出：
 * - [UNMAPPED] registry 认识、但该源映射表没收录——已注册模型的分数/定价正在丢，warn 级全列。
 *   注意 litellm 的映射表是每 canonical 精选一个 key，其余托管商 key 命中 registry 属设计使然，
 *   该源应传 reportUnmapped: false 关闭此段；
 * - [UNRECOGNIZED] registry 也不认识——全新家族/新模型（如未来的 claude-mythos-6 之前的状态），
 *   log 级；小清单全量列出，大清单按根家族聚合计数，每个家族展示版本号最高的代表名
 *   （新代际分支会自然浮到代表位）。
 * 与正向的映射表 NEW-MODEL 检测互补：全新家族没有映射基线，只能靠这里暴露。
 * @param {string} source - 日志前缀（如 'benchlm'）
 * @param {string[]} names - 数据源的未映射模型名
 * @param {string[]} patterns - registry patterns
 * @param {Object} [options]
 * @param {string} [options.mapName] - 提示补充映射的表名（如 'BENCHLM_MODEL_MAP'）
 * @param {boolean} [options.reportUnmapped] - 是否输出 UNMAPPED 段（分数源默认 true）
 * @param {number} [options.maxListed] - 全量列出的阈值（默认 20）
 * @param {number} [options.maxFamilies] - 聚合模式展示的家族数上限（默认 15）
 * @returns {{unmapped: string[], unrecognized: string[]}}
 */
export function reportUnmappedAgainstRegistry(source, names, patterns, { mapName = 'MODEL_MAP', reportUnmapped = true, maxListed = 20, maxFamilies = 15 } = {}) {
  const unique = [...new Set(names || [])]
  const unmappedToRegistry = unique.filter(name => matchesAnyRegistryPattern(name, patterns))
  const unrecognized = unique.filter(name => !matchesAnyRegistryPattern(name, patterns))

  if (reportUnmapped && unmappedToRegistry.length > 0) {
    console.warn(
      `[${source}] [UNMAPPED] ${unmappedToRegistry.length} upstream model(s) match registry patterns ` +
      `but are NOT in ${mapName} — registered models are losing data here: ${unmappedToRegistry.sort().join(', ')}`,
    )
  }

  if (unrecognized.length > 0) {
    if (unrecognized.length <= maxListed) {
      console.log(`[${source}] [UNRECOGNIZED] ${unrecognized.length} upstream model(s) match no registry pattern: ${unrecognized.sort().join(', ')}`)
    } else {
      // 根家族（剥掉 provider 前缀后的家族名第一段）聚合；代表名取家族内版本最高者
      // （版本段值超过 50 的日期/参数量形态不参与代表竞争，避免 newest 显示成日期快照）
      const byRoot = new Map()
      for (const name of unrecognized) {
        const { family, nums } = parseCandidateVersion(name)
        const root = family.split('/').pop().split('-')[0] || '(unparsed)'
        const plausible = nums.length > 0 && nums.every(n => n <= MAX_PLAUSIBLE_VERSION_SEGMENT)
        const prev = byRoot.get(root)
        if (!prev) {
          byRoot.set(root, { count: 1, newest: name, nums, plausible })
        } else {
          prev.count += 1
          if (plausible && (!prev.plausible || compareVersionArrays(nums, prev.nums) > 0)) {
            prev.newest = name
            prev.nums = nums
            prev.plausible = true
          }
        }
      }
      const families = [...byRoot.entries()].sort((a, b) => b[1].count - a[1].count)
      const shown = families.slice(0, maxFamilies)
        .map(([root, info]) => `${root} x${info.count} (newest: ${info.newest})`)
        .join(', ')
      const moreFamilies = families.length > maxFamilies ? ` ... and ${families.length - maxFamilies} more families` : ''
      console.log(`[${source}] [UNRECOGNIZED] ${unrecognized.length} upstream models match no registry pattern, by root family: ${shown}${moreFamilies}`)
    }
  }

  return { unmapped: unmappedToRegistry, unrecognized }
}

/**
 * 从数据源的未映射模型名中检测"疑似新模型"：
 * 与某个已映射 canonical 同家族、且版本号高于该家族已映射的最大版本。
 * （如榜单出现 grok-4-6 而映射表最高只有 grok-4.5。）
 * 仅做家族+版本比较，不依赖 pattern，避免点号/连字符命名差异造成的漏报。
 * 输入先经 expandNameVariants 展开，覆盖带 provider 前缀的源命名；
 * 并排除日期/参数量/旧命名的伪高版本（详见各过滤条件）。
 * @param {string[]} names - 数据源中的未映射模型名（slug）
 * @param {Object} modelMap - 该源的 模型名 -> canonicalModel 映射
 * @returns {Array<{name: string, family: string, version: string, mappedBest: string}>}
 */
export function detectNewModelCandidates(names, modelMap) {
  // 家族 -> 已映射的最大版本（与代表性 canonical）
  const familyBest = new Map()
  for (const canonical of Object.values(modelMap)) {
    const { family, nums } = parseModelVersion(canonical)
    const prev = familyBest.get(family)
    if (!prev || compareVersionArrays(nums, prev.nums) > 0) {
      familyBest.set(family, { nums, canonical })
    }
  }

  const seen = new Set()
  const candidates = []
  for (const name of names || []) {
    if (typeof name !== 'string' || name.trim() === '') continue
    for (const variant of expandNameVariants(name)) {
      const { family, nums } = parseCandidateVersion(variant)
      const best = familyBest.get(family)
      if (!best || compareVersionArrays(nums, best.nums) <= 0) continue
      // 主版本须与已映射同代或仅高一代：排除 gpt-35-turbo / claude-opus-41 等旧命名与大跳号
      if (nums[0] !== best.nums[0] && nums[0] !== best.nums[0] + 1) continue
      // 版本段值超过 50 视为日期/参数量（gpt-5-2025-08-07、mimo-v2-0206）
      if (nums.some(n => n > MAX_PLAUSIBLE_VERSION_SEGMENT)) continue
      if (seen.has(name)) continue
      seen.add(name)
      candidates.push({
        name,
        family,
        version: nums.join('.'),
        mappedBest: best.canonical,
      })
      break // 一个名字命中即可，避免同前缀多形态重复计入
    }
  }
  return candidates
}

/**
 * 各数据源共用的 [NEW-MODEL] 告警：检测到同家族更高版本的未映射模型时 console.warn，
 * 避免其分数/定价被静默丢弃。返回检测到的候选（便于测试断言）。
 * @param {string[]} names - 数据源中的未映射模型名
 * @param {Object} modelMap - 该源的 模型名 -> canonicalModel 映射
 * @param {Object} [options]
 * @param {string} [options.source] - 日志前缀（如 'dradar'）
 * @param {string} [options.mapName] - 提示补充映射的表名（如 'DRADAR_MODEL_MAP'）
 * @param {string} [options.dropped] - 丢弃内容描述（如 'its pricing data are dropped'）
 * @param {(candidate: {name: string}) => boolean} [options.ignore] - 已知故意不映射的名字，跳过告警
 */
export function warnNewModelCandidates(names, modelMap, { source = 'unknown', mapName = 'MODEL_MAP', dropped = 'its scores are dropped', ignore } = {}) {
  const candidates = detectNewModelCandidates(names, modelMap).filter(c => !ignore?.(c))
  for (const candidate of candidates) {
    console.warn(
      `[${source}] [NEW-MODEL] "${candidate.name}" (family ${candidate.family}, v${candidate.version}) ` +
      `exceeds mapped ${candidate.mappedBest} but is NOT in ${mapName}; ` +
      `${dropped} — add the mapping (or register the model) to include it.`,
    )
  }
  return candidates
}

/** 逐段版本号比较（缺段视为 0），返回 >0 / 0 / <0 */
function compareVersionArrays(a, b) {
  const len = Math.max(a.length, b.length)
  for (let i = 0; i < len; i++) {
    const x = a[i] ?? 0
    const y = b[i] ?? 0
    if (x !== y) return x - y
  }
  return 0
}
