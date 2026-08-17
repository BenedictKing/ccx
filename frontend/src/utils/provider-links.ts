// 服务商官方控制台与推广链接映射。
// 与桌面端 desktop/frontend/src/lib/external-link.ts 保持一致，用于订阅中心展示卡片的外链入口。

/** 官方控制台链接（查看用量 / 余额 / 密钥） */
export const providerConsoleLinks: Record<string, string> = {
  deepseek: 'https://platform.deepseek.com/usage',
  mimo: 'https://platform.xiaomimimo.com/console/balance',
  compshare: 'https://console.compshare.cn/light-gpu/model-subscription',
  runapi: 'https://runapi.co/console',
  tokenrhythm: 'https://tokenrhythm.studio/',
  'tencent-lkeap': 'https://console.cloud.tencent.com/lkeap/token-plan',
  kimi: 'https://platform.moonshot.cn/console/account',
  volcengine:
    'https://console.volcengine.com/ark/region:cn-beijing/subscription/coding-plan?projectName=default',
  qianfan: 'https://console.bce.baidu.com/qianfan/resource/subscribe',
  originrouter: 'https://easytransnote.com/ai/console/#key',
  glm: 'https://open.bigmodel.cn/coding-plan/personal/overview',
  sensenova: 'https://platform.sensenova.cn/console',
  minimax: 'https://platform.minimaxi.com/user-center/payment/balance',
  dashscope: 'https://bailian.console.aliyun.com/cn-beijing?tab=model#/api-key',
  'opencode-zen': 'https://opencode.ai/',
  openrouter: 'https://openrouter.ai/keys',
  modelscope: 'https://modelscope.cn/my/myaccesstoken',
  xfyun: 'https://console.xfyun.cn/',
  atomgit: 'https://ai.atomgit.com/dashboard/coding-plan'
}

/** 推广 / 注册链接（带 aff 溯源，标记赞助商渠道） */
export const providerPromotionLinks: Record<string, string> = {
  compshare: 'https://www.compshare.cn/?ytag=GPU_YY_git_ccx',
  runapi: 'https://runapi.co/register?aff=CqQO',
  volcengine:
    'https://www.volcengine.com/activity/ai618?utm_campaign=hw&utm_content=hw&utm_medium=devrel_tool_web&utm_source=OWO&utm_term=ccx',
}

export function openExternal(url: string) {
  if (typeof window !== 'undefined') window.open(url, '_blank', 'noopener,noreferrer')
}

export function openProviderConsole(providerId: string) {
  const url = providerConsoleLinks[providerId]
  if (url) openExternal(url)
}

export function openProviderPromotion(providerId: string) {
  const url = providerPromotionLinks[providerId]
  if (url) openExternal(url)
}
