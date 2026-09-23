/**
 * 官方发布公告评测分数（手工维护）
 *
 * 用途：新模型发布当天第三方评测站（deepswe/benchlm/AA coding）尚未收录时，
 * 以官方公告引用的基准分数作为初始质量证据。scripts/prefill-official-scores.mjs
 * 读取本表，按公告内锚点模型（registry 已有 DeepSWE 直测）做中位比值折算，
 * 产出 DeepSWE 等价分（benchmark=official_release, metric=deepswe_equivalent）
 * 供 autopilot qualityTier 消费（EvidenceCalibrated，封顶 high）。
 *
 * 口径约定：
 * - rawValue 统一 0-1（与 deepswe pass_at_1 同刻度）；公告百分数录入时除以 100
 * - effort 记录公告实际评测档位（Claude 系默认 max，Terminal-Bench 4.0 的 Opus 5.5 为 xhigh）
 * - domain 仅 coding 家族基准（terminal_bench_4 / frontier_code_1_1 / cursor_bench_4）
 *   会产出折算等价分；其余基准（知识/工作流类）只作展示证据
 * - 官方未测的格子（公告表中的 —）不录入
 */

const ANTHROPIC_OPUS_55_URL = 'https://www.anthropic.com/claude-opus-5-5'

export const OFFICIAL_RELEASE_ANNOUNCEMENTS = {
  claude_opus_55: {
    label: 'Anthropic Claude Opus 5.5 发布公告',
    sourceUrl: ANTHROPIC_OPUS_55_URL,
    capturedAt: '2026-09-23',
    scores: [
      // Agentic coding
      { model: 'claude-opus-5-5', benchmark: 'terminal_bench_4', benchmarkVersion: '4.0', domain: 'coding', metric: 'pass_rate', effort: 'xhigh', rawValue: 0.664 },
      { model: 'claude-fable-5-1', benchmark: 'terminal_bench_4', benchmarkVersion: '4.0', domain: 'coding', metric: 'pass_rate', effort: 'max', rawValue: 0.558 },
      { model: 'claude-opus-5', benchmark: 'terminal_bench_4', benchmarkVersion: '4.0', domain: 'coding', metric: 'pass_rate', effort: 'max', rawValue: 0.523 },
      { model: 'gpt-6-astra', benchmark: 'terminal_bench_4', benchmarkVersion: '4.0', domain: 'coding', metric: 'pass_rate', effort: 'high', rawValue: 0.579 },
      { model: 'gpt-5.6-sol', benchmark: 'terminal_bench_4', benchmarkVersion: '4.0', domain: 'coding', metric: 'pass_rate', effort: 'max', rawValue: 0.373 },

      { model: 'claude-opus-5-5', benchmark: 'frontier_code_1_1', benchmarkVersion: '1.1-main', domain: 'coding', metric: 'pass_rate', effort: 'max', rawValue: 0.544 },
      { model: 'claude-fable-5-1', benchmark: 'frontier_code_1_1', benchmarkVersion: '1.1-main', domain: 'coding', metric: 'pass_rate', effort: 'max', rawValue: 0.503 },
      { model: 'claude-opus-5', benchmark: 'frontier_code_1_1', benchmarkVersion: '1.1-main', domain: 'coding', metric: 'pass_rate', effort: 'max', rawValue: 0.480 },
      { model: 'gpt-6-astra', benchmark: 'frontier_code_1_1', benchmarkVersion: '1.1-main', domain: 'coding', metric: 'pass_rate', effort: 'max', rawValue: 0.533 },
      { model: 'gpt-5.6-sol', benchmark: 'frontier_code_1_1', benchmarkVersion: '1.1-main', domain: 'coding', metric: 'pass_rate', effort: 'max', rawValue: 0.475 },

      { model: 'claude-opus-5-5', benchmark: 'cursor_bench_4', benchmarkVersion: '4.0', domain: 'coding', metric: 'pass_rate', effort: 'max', rawValue: 0.578 },
      { model: 'claude-fable-5-1', benchmark: 'cursor_bench_4', benchmarkVersion: '4.0', domain: 'coding', metric: 'pass_rate', effort: 'max', rawValue: 0.518 },
      { model: 'claude-opus-5', benchmark: 'cursor_bench_4', benchmarkVersion: '4.0', domain: 'coding', metric: 'pass_rate', effort: 'max', rawValue: 0.466 },
      { model: 'gpt-5.6-sol', benchmark: 'cursor_bench_4', benchmarkVersion: '4.0', domain: 'coding', metric: 'pass_rate', effort: 'max', rawValue: 0.417 },

      // 业务工作流（Zapier 公开榜口径，展示用）
      { model: 'claude-opus-5-5', benchmark: 'automation_bench', benchmarkVersion: 'zapier', domain: 'agentic', metric: 'pass_rate', effort: 'max', rawValue: 0.400 },
      { model: 'claude-fable-5-1', benchmark: 'automation_bench', benchmarkVersion: 'zapier', domain: 'agentic', metric: 'pass_rate', effort: 'max', rawValue: 0.314 },
      { model: 'claude-opus-5', benchmark: 'automation_bench', benchmarkVersion: 'zapier', domain: 'agentic', metric: 'pass_rate', effort: 'max', rawValue: 0.269 },
      { model: 'gpt-6-astra', benchmark: 'automation_bench', benchmarkVersion: 'zapier', domain: 'agentic', metric: 'pass_rate', effort: 'max', rawValue: 0.414 },
      { model: 'gpt-5.6-sol', benchmark: 'automation_bench', benchmarkVersion: 'zapier', domain: 'agentic', metric: 'pass_rate', effort: 'max', rawValue: 0.288 },

      // 知识工作（展示用）
      { model: 'claude-opus-5-5', benchmark: 'gdpval_aa_v2_1', benchmarkVersion: '2.1', domain: 'knowledge', metric: 'elo', effort: 'max', rawValue: 1846 },
      { model: 'claude-fable-5-1', benchmark: 'gdpval_aa_v2_1', benchmarkVersion: '2.1', domain: 'knowledge', metric: 'elo', effort: 'max', rawValue: 1735 },
      { model: 'claude-opus-5', benchmark: 'gdpval_aa_v2_1', benchmarkVersion: '2.1', domain: 'knowledge', metric: 'elo', effort: 'max', rawValue: 1708 },
      { model: 'gpt-6-astra', benchmark: 'gdpval_aa_v2_1', benchmarkVersion: '2.1', domain: 'knowledge', metric: 'elo', effort: 'max', rawValue: 1542 },
      { model: 'gpt-5.6-sol', benchmark: 'gdpval_aa_v2_1', benchmarkVersion: '2.1', domain: 'knowledge', metric: 'elo', effort: 'max', rawValue: 1588 },

      { model: 'claude-opus-5-5', benchmark: 'hle', benchmarkVersion: 'with-tools', domain: 'knowledge', metric: 'pass_rate', effort: 'max', rawValue: 0.677 },
      { model: 'claude-fable-5-1', benchmark: 'hle', benchmarkVersion: 'with-tools', domain: 'knowledge', metric: 'pass_rate', effort: 'max', rawValue: 0.656 },
      { model: 'claude-opus-5', benchmark: 'hle', benchmarkVersion: 'with-tools', domain: 'knowledge', metric: 'pass_rate', effort: 'max', rawValue: 0.636 },
      { model: 'gpt-6-astra', benchmark: 'hle', benchmarkVersion: 'with-tools', domain: 'knowledge', metric: 'pass_rate', effort: 'max', rawValue: 0.572 },

      // Agentic scientific research（展示用）
      { model: 'claude-opus-5-5', benchmark: 'terminal_bench_science_0_1', benchmarkVersion: '0.1', domain: 'agentic', metric: 'pass_rate', effort: 'max', rawValue: 0.587 },
      { model: 'claude-fable-5-1', benchmark: 'terminal_bench_science_0_1', benchmarkVersion: '0.1', domain: 'agentic', metric: 'pass_rate', effort: 'max', rawValue: 0.526 },
      { model: 'claude-opus-5', benchmark: 'terminal_bench_science_0_1', benchmarkVersion: '0.1', domain: 'agentic', metric: 'pass_rate', effort: 'max', rawValue: 0.290 },
      { model: 'gpt-6-astra', benchmark: 'terminal_bench_science_0_1', benchmarkVersion: '0.1', domain: 'agentic', metric: 'pass_rate', effort: 'max', rawValue: 0.646 },
      { model: 'gpt-5.6-sol', benchmark: 'terminal_bench_science_0_1', benchmarkVersion: '0.1', domain: 'agentic', metric: 'pass_rate', effort: 'max', rawValue: 0.224 },
    ],
  },

  // 小米 MiMo-V2.6 发布公告（2026-09-22，一手：mimo.mi.com + HF 模型卡 XiaomiMiMo/MiMo-V2.6-Flash-RL）。
  // 表内自带 DeepSWE v1.1 / Terminal-Bench 对照列（Opus 5 74.0 ≈ registry max 档 73.6，max 口径），
  // 锚点序数插值可消除 harness 口径差。
  mimo_v2_6: {
    label: '小米 MiMo-V2.6 发布公告',
    sourceUrl: 'https://mimo.mi.com/docs/zh-CN/news/latest/v2-6',
    capturedAt: '2026-09-23',
    scores: [
      // DeepSWE v1.1（Xiaomi harness，锚点对照列齐全）
      { model: 'mimo-v2.6-pro', benchmark: 'deepswe_official', benchmarkVersion: 'v1.1-xiaomi', domain: 'coding', metric: 'pass_rate', effort: 'max', rawValue: 0.719 },
      { model: 'mimo-v2.6-flash', benchmark: 'deepswe_official', benchmarkVersion: 'v1.1-xiaomi', domain: 'coding', metric: 'pass_rate', effort: 'max', rawValue: 0.679 },
      { model: 'claude-opus-5', benchmark: 'deepswe_official', benchmarkVersion: 'v1.1-xiaomi', domain: 'coding', metric: 'pass_rate', effort: 'max', rawValue: 0.740 },
      { model: 'gpt-5.6-sol', benchmark: 'deepswe_official', benchmarkVersion: 'v1.1-xiaomi', domain: 'coding', metric: 'pass_rate', effort: 'max', rawValue: 0.730 },
      { model: 'claude-fable-5', benchmark: 'deepswe_official', benchmarkVersion: 'v1.1-xiaomi', domain: 'coding', metric: 'pass_rate', effort: 'max', rawValue: 0.700 },

      // Terminal-Bench 4.0
      { model: 'mimo-v2.6-pro', benchmark: 'terminal_bench_4', benchmarkVersion: '4.0', domain: 'coding', metric: 'pass_rate', effort: 'max', rawValue: 0.349 },
      { model: 'mimo-v2.6-flash', benchmark: 'terminal_bench_4', benchmarkVersion: '4.0', domain: 'coding', metric: 'pass_rate', effort: 'max', rawValue: 0.288 },
      { model: 'claude-opus-5', benchmark: 'terminal_bench_4', benchmarkVersion: '4.0', domain: 'coding', metric: 'pass_rate', effort: 'max', rawValue: 0.490 },
      { model: 'gpt-5.6-sol', benchmark: 'terminal_bench_4', benchmarkVersion: '4.0', domain: 'coding', metric: 'pass_rate', effort: 'max', rawValue: 0.399 },
      { model: 'claude-fable-5', benchmark: 'terminal_bench_4', benchmarkVersion: '4.0', domain: 'coding', metric: 'pass_rate', effort: 'max', rawValue: 0.424 },

      // AutomationBench v1.0.6（agentic 展示用）
      { model: 'mimo-v2.6-pro', benchmark: 'automation_bench', benchmarkVersion: 'v1.0.6', domain: 'agentic', metric: 'pass_rate', effort: 'max', rawValue: 0.531 },
      { model: 'mimo-v2.6-flash', benchmark: 'automation_bench', benchmarkVersion: 'v1.0.6', domain: 'agentic', metric: 'pass_rate', effort: 'max', rawValue: 0.523 },
      { model: 'claude-opus-5', benchmark: 'automation_bench', benchmarkVersion: 'v1.0.6', domain: 'agentic', metric: 'pass_rate', effort: 'max', rawValue: 0.503 },
      { model: 'gpt-5.6-sol', benchmark: 'automation_bench', benchmarkVersion: 'v1.0.6', domain: 'agentic', metric: 'pass_rate', effort: 'max', rawValue: 0.458 },
    ],
  },

  // 阶跃 Step 5 Preview 公告（一手：stepfun.com/step-5-preview）。
  // 表内 Terminal-Bench v4 数字与 Anthropic Opus 5.5 公告完全一致（同 runner 口径）。
  step_5_preview: {
    label: '阶跃 Step 5 Preview 公告',
    sourceUrl: 'https://www.stepfun.com/step-5-preview',
    capturedAt: '2026-09-23',
    scores: [
      // DeepSWE v1.1（锚点：Astra/Opus 5 有 registry deepswe medium 直测）
      { model: 'step-5', benchmark: 'deepswe_official', benchmarkVersion: 'v1.1-stepfun', domain: 'coding', metric: 'pass_rate', effort: 'high', rawValue: 0.677 },
      { model: 'gpt-6-astra', benchmark: 'deepswe_official', benchmarkVersion: 'v1.1-stepfun', domain: 'coding', metric: 'pass_rate', effort: 'max', rawValue: 0.741 },
      { model: 'claude-opus-5', benchmark: 'deepswe_official', benchmarkVersion: 'v1.1-stepfun', domain: 'coding', metric: 'pass_rate', effort: 'max', rawValue: 0.740 },
      { model: 'claude-fable-5-1', benchmark: 'deepswe_official', benchmarkVersion: 'v1.1-stepfun', domain: 'coding', metric: 'pass_rate', effort: 'max', rawValue: 0.674 },

      // Terminal-Bench v4（与 Anthropic 公告同 runner，锚点交叉验证一致）
      { model: 'step-5', benchmark: 'terminal_bench_4', benchmarkVersion: '4.0', domain: 'coding', metric: 'pass_rate', effort: 'high', rawValue: 0.333 },
      { model: 'gpt-6-astra', benchmark: 'terminal_bench_4', benchmarkVersion: '4.0', domain: 'coding', metric: 'pass_rate', effort: 'max', rawValue: 0.579 },
      { model: 'claude-opus-5', benchmark: 'terminal_bench_4', benchmarkVersion: '4.0', domain: 'coding', metric: 'pass_rate', effort: 'max', rawValue: 0.523 },
      { model: 'claude-fable-5-1', benchmark: 'terminal_bench_4', benchmarkVersion: '4.0', domain: 'coding', metric: 'pass_rate', effort: 'max', rawValue: 0.558 },
    ],
  },

  // xAI Grok 4.7 发布公告（2026-09-21；x.ai launch post，数字经 unite.ai/coursiv 转录，
  // 其中 GPT-5.6 Sol 的 CursorBench 41.7 / Terminal-Bench 4.0 37.3 与 Anthropic 公告完全一致，
  // 证实同 runner 口径）。表内 registry 有 deepswe 直测的锚点只有 Sol 一个（<2），
  // 故锚点池复用 claude_opus_55 公告（anchorPoolFrom）。
  grok_4_7: {
    label: 'xAI Grok 4.7 发布公告',
    sourceUrl: 'https://www.unite.ai/spacexai-releases-grok-4-7-for-coding-and-knowledge-work',
    capturedAt: '2026-09-23',
    anchorPoolFrom: 'claude_opus_55',
    scores: [
      { model: 'grok-4.7', benchmark: 'cursor_bench_4', benchmarkVersion: '4.0', domain: 'coding', metric: 'pass_rate', effort: 'xhigh', rawValue: 0.463 },
      { model: 'gpt-5.6-sol', benchmark: 'cursor_bench_4', benchmarkVersion: '4.0', domain: 'coding', metric: 'pass_rate', effort: 'max', rawValue: 0.417 },
      { model: 'claude-fable-5-1', benchmark: 'cursor_bench_4', benchmarkVersion: '4.0', domain: 'coding', metric: 'pass_rate', effort: 'max', rawValue: 0.518 },

      { model: 'grok-4.7', benchmark: 'terminal_bench_4', benchmarkVersion: '4.0', domain: 'coding', metric: 'pass_rate', effort: 'xhigh', rawValue: 0.380 },
      { model: 'gpt-5.6-sol', benchmark: 'terminal_bench_4', benchmarkVersion: '4.0', domain: 'coding', metric: 'pass_rate', effort: 'max', rawValue: 0.373 },
      { model: 'claude-fable-5-1', benchmark: 'terminal_bench_4', benchmarkVersion: '4.0', domain: 'coding', metric: 'pass_rate', effort: 'max', rawValue: 0.579 },

      // DeepSWE 自报 71.0 为 high 档且表内锚点（Sol 72.7 为 max 档）跨 effort 混杂，
      // 口径不可靠：仅录展示，折算依赖 TB4.0/CursorBench（经 anchorPoolFrom）。
      { model: 'grok-4.7', benchmark: 'deepswe_official', benchmarkVersion: 'v1.1-xai', domain: 'coding', metric: 'pass_rate', effort: 'high', rawValue: 0.710 },
    ],
  },
}

/**
 * 折算锚点：公告表内同基准有分、且 registry 已有 DeepSWE coding 直测的模型。
 * prefill 脚本动态从 registry 读取其 medium 档直测分；此处仅声明锚点资格，
 * 某基准该锚点无官方分时自动不参与该基准的折算。
 */
export const OFFICIAL_RELEASE_ANCHOR_MODELS = [
  'claude-opus-5',
  'claude-fable-5',
  'gpt-6-astra',
  'gpt-5.6-sol',
]
