/**
 * 统一的套餐余量/用量明细行：标签 + 可选进度条 + 数值 + 说明。
 * 各渠道把自家数据映射为该结构后交给 UsageQuotaRows 组件渲染。
 */
export interface UsageQuotaItem {
  key: string
  /** 已翻译的行标签，如「近5小时」「MiniMax-M2 · 当前窗口」。 */
  label: string
  /** 0-100 已用百分比；undefined 表示该行没有进度概念（如额度未启用），进度条位置留白。 */
  usedPercent?: number
  /** 已格式化的数值文本，如「已用 22%」「剩余 78.5% · 1,234/5,000」。 */
  value: string
  /** 已格式化的说明文本，如「4天后重置」「有效期至 2026/08/21」。 */
  caption?: string
  /** 说明文本的悬浮提示（如完整重置时间）。 */
  captionTitle?: string
}
