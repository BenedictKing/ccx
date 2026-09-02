// 模型名优先级排序：按预定义优先级模式降序排列，同优先级组内按自然降序。
// 规则顺序：先新后旧、先精确后宽松；同家族新版本在前，带 codex/pro/max 等精确后缀优先于通用名。
const modelPriorityPatterns: RegExp[] = [
  // Anthropic Claude
  /fable-5[.-]1/i,
  /fable-5/i,
  /opus-5/i,
  /opus-4[.-]8/i,
  /opus-4-7/i,
  /sonnet-5/i,
  /sonnet-4-7/i,
  /haiku-4-7/i,
  /opus-4-6/i,
  /sonnet-4-6/i,
  /haiku-4-6/i,
  /opus-4-5/i,
  /sonnet-4-5/i,
  /haiku-4-5/i,

  // OpenAI GPT-5 系列
  /gpt-5\.6-sol/i,
  /gpt-5\.6-terra/i,
  /gpt-5\.6-luna/i,
  /gpt-5\.6/i,
  /gpt-5\.5-pro/i,
  /gpt-5\.5/i,
  /gpt-5\.4-pro/i,
  /gpt-5\.4-mini/i,
  /gpt-5\.4-nano/i,
  /gpt-5\.4/i,
  /gpt-5\.3-codex/i,
  /gpt-5\.3/i,
  /gpt-5\.2-codex/i,
  /gpt-5\.2-pro/i,
  /gpt-5\.2/i,
  /gpt-5\.1-codex/i,
  /gpt-5\.1/i,
  /gpt-5-codex/i,
  /gpt-5-pro/i,
  /gpt-5/i,

  // Google Gemini
  /gemini-3\.7-flash/i,
  /gemini-3\.6-flash/i,
  /gemini-3\.5-flash/i,
  /gemini-3\.1-pro/i,
  /gemini-3\.1-flash-lite/i,
  /gemini-3-pro/i,
  /gemini-3-flash/i,
  /gemini-3/i,
  /gemini-2\.5-pro/i,
  /gemini-2\.5-flash-lite/i,
  /gemini-2\.5-flash/i,

  // xAI Grok
  /grok-4\.6/i,
  /grok-4\.5/i,
  /grok-4\.3/i,
  /grok-4-3/i,
  /grok-4\.2/i,
  /grok-4\.1/i,
  /grok-4/i,

  // 智谱 GLM
  /glm-?5\.3/i,
  /glm-?5\.2/i,
  /glm-?5\.1/i,
  /glm-?5/i,
  /glm-?4\.7-flash/i,
  /glm-?4\.7/i,
  /glm-?4\.6/i,

  // 阿里 Qwen
  /qwen-?3\.8-max/i,
  /qwen-?3\.7-max/i,
  /qwen-?3\.7-plus/i,
  /qwen-?3\.6-plus/i,
  /qwen-?3\.6/i,
  /qwen-?3\.5/i,
  /qwen-?3-max/i,
  /qwen-?3-coder/i,
  /qwen-?3/i,

  // DeepSeek
  /deepseek-v4-pro/i,
  /deepseek-v4-flash/i,
  /deepseek-v4/i,
  /deepseek-v3\.2/i,
  /deepseek-reasoner/i,
  /deepseek-chat/i,
  /deepseek-v3/i,

  // Moonshot Kimi / MiniMax
  /^(?:kimi-)?k3(?:\[1m\])?$/i,
  /kimi-for-coding-highspeed/i,
  /kimi-for-coding/i,
  /kimi-?k2\.7/i,
  /kimi-?k2\.6/i,
  /kimi-?k2\.5/i,
  /kimi-?k2-thinking/i,
  /minimax-?m3/i,
  /minimax-?m2\.7/i,
  /minimax-?m2\.5/i,
  /mimo-v2\.5/i,
  /doubao-seed-2-0/i,
  /ernie-4\.5/i,
  /baichuan-m2/i,
  /yi-34b-200k/i,
  /k2\.7/i,
  /k2\.6/i,
  /k2\.5/i,
  /m3/i,
  /m2\.7/i,
  /m2\.5/i,

  // DeepSeek 兜底
  /deepseek-/i,
]

const modelNameCollator = new Intl.Collator('en', { numeric: true, sensitivity: 'base' })

export function getModelPriority(name: string): number {
  for (let i = 0; i < modelPriorityPatterns.length; i++) {
    if (modelPriorityPatterns[i].test(name)) return i
  }
  return modelPriorityPatterns.length
}

export function sortModelNamesDesc(models: string[]): string[] {
  return [...models].sort((a, b) => {
    const priorityA = getModelPriority(a)
    const priorityB = getModelPriority(b)
    if (priorityA !== priorityB) return priorityA - priorityB
    return modelNameCollator.compare(b, a)
  })
}
