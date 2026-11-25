import { Browser } from '@wailsio/runtime'

export const providerConsoleLinks: Record<string, string> = {
  deepseek: 'https://platform.deepseek.com/usage',
  mimo: 'https://platform.xiaomimimo.com/console/balance',
  'tencent-lkeap': 'https://console.cloud.tencent.com/lkeap/token-plan',
  kimi: 'https://platform.moonshot.cn/console/account',
  'volc-ark': 'https://console.volcengine.com/ark/region:cn-beijing/subscription/coding-plan?projectName=default',
  qianfan: 'https://console.bce.baidu.com/qianfan/resource/subscribe',
  originrouter: 'https://easytransnote.com/ai/console/#key',
  glm: 'https://open.bigmodel.cn/coding-plan/personal/overview',
  sensenova: 'https://platform.sensenova.cn/console',
  minimax: 'https://platform.minimaxi.com/user-center/payment/balance',
  dashscope: 'https://bailian.console.aliyun.com/cn-beijing?tab=model#/api-key',
  'opencode-zen': 'https://opencode.ai/',
  'opencode-go': 'https://opencode.ai/',
  openrouter: 'https://openrouter.ai/keys',
  modelscope: 'https://modelscope.cn/my/myaccesstoken',
  openai: 'https://platform.openai.com',
  xfyun: 'https://console.xfyun.cn/',
}

export function openExternalLink(url: string) {
  return Browser.OpenURL(url)
}

export function openProviderConsole(provider: string) {
  const url = providerConsoleLinks[provider]
  if (!url) return Promise.resolve()
  return openExternalLink(url)
}
