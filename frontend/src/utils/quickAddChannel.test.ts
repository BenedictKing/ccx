import { describe, expect, it } from 'vitest'

import {
  buildQuickAddChannelName,
  defaultQuickAddServiceType,
  findExistingQuickAddChannel,
  inferQuickAddProviderId,
  normalizeQuickAddApiKeys,
  normalizeQuickAddBaseUrls,
  normalizeDiscoveredChannelKind,
  recognizeQuickAddBaseUrl,
  supportsQuickAddProtocolDiscovery
} from './quickAddChannel'

const providers = [
  {
    providerId: 'glm',
    candidates: [{ baseUrl: 'https://open.bigmodel.cn/api/anthropic' }],
    routes: [
      { candidates: [{ baseUrl: 'https://open.bigmodel.cn/api/anthropic' }] },
      { candidates: [{ baseUrl: 'https://open.bigmodel.cn/api/paas/v4#' }] }
    ]
  },
  {
    providerId: 'deepseek',
    candidates: [{ baseUrl: 'https://api.deepseek.com/anthropic' }],
    routes: [{ candidates: [{ baseUrl: 'https://api.deepseek.com' }] }]
  },
  {
    providerId: 'opencode-zen',
    aliases: ['opencode-go'],
    candidates: [{ baseUrl: 'https://opencode.ai/zen/go/v1' }],
    routes: [{ candidates: [{ baseUrl: 'https://opencode.ai/zen/v1' }] }]
  }
]

describe('buildQuickAddChannelName', () => {
  it('端口加入渠道名称且不再附加随机后缀', () => {
    expect(buildQuickAddChannelName('http://localhost:8990/', 'abc123')).toBe('localhost-8990')
    expect(buildQuickAddChannelName('https://www.example.com:8443/v1', 'abc123')).toBe('example-com-8443')
  })

  it('明确域名省略前导 www 且不附加随机后缀', () => {
    expect(buildQuickAddChannelName('https://www.fastaitoken.com/v1', 'ivpp0p')).toBe('fastaitoken-com')
  })

  it('不误删主机名中间的 www', () => {
    expect(buildQuickAddChannelName('https://api.www-example.com', 'abc123')).toBe('api-www-example-com')
  })

  it('完整保留非标准多级路径并剥离版本尾段', () => {
    expect(buildQuickAddChannelName('https://load103.diyai.diy/proxy/feishu-glm-46', 'abc123')).toBe(
      'load103-diyai-diy-proxy-feishu-glm-46'
    )
    expect(buildQuickAddChannelName('https://host.com/tenant/a/v1', 'abc123')).toBe('host-com-tenant-a')
    expect(buildQuickAddChannelName('https://host.com/api/v5', 'abc123')).toBe('host-com')
  })

  it('无效地址回退到通用名称', () => {
    expect(buildQuickAddChannelName('not a url', 'abc123')).toBe('channel-abc123')
  })
})

describe('findExistingQuickAddChannel', () => {
  const channel = {
    index: 3,
    name: 'localhost-8990',
    serviceType: 'openai' as const,
    baseUrl: 'http://localhost:8990/v1',
    apiKeys: ['sk-test']
  }

  it('识别带端口地址及默认版本前缀的等效渠道', () => {
    const match = findExistingQuickAddChannel(['http://localhost:8990/'], [channel])
    expect(match?.channel.name).toBe('localhost-8990')
    expect(match?.existingBaseUrl).toBe('http://localhost:8990/v1')
  })

  it('忽略协议和域名大小写但保留不同端口与业务路径', () => {
    expect(
      findExistingQuickAddChannel(['HTTPS://API.EXAMPLE.COM/v1'], [{ ...channel, baseUrl: 'https://api.example.com' }])
        ?.channel.name
    ).toBe('localhost-8990')
    expect(findExistingQuickAddChannel(['http://localhost:8991'], [channel])).toBeNull()
    expect(findExistingQuickAddChannel(['http://localhost:8990/other'], [channel])).toBeNull()
  })

  it('保留禁止自动追加版本前缀的 # 语义', () => {
    const hashChannel = { ...channel, baseUrl: 'https://api.example.com#' }
    expect(findExistingQuickAddChannel(['https://api.example.com'], [hashChannel])).toBeNull()
    expect(findExistingQuickAddChannel(['https://api.example.com/#'], [hashChannel])?.channel.name).toBe(
      'localhost-8990'
    )
  })
})

describe('quick add protocol discovery', () => {
  it('探测前清理并去重 API Key，同时保留首次出现顺序', () => {
    expect(normalizeQuickAddApiKeys([' sk-first ', 'sk-second', 'sk-first', '', '  '])).toEqual([
      'sk-first',
      'sk-second'
    ])
  })

  it('API Key 去重保持大小写敏感', () => {
    expect(normalizeQuickAddApiKeys(['sk-key', 'SK-key'])).toEqual(['sk-key', 'SK-key'])
  })

  it('与标准模式一致地清理后台路径和协议端点', () => {
    expect(recognizeQuickAddBaseUrl('https://www.fastaitoken.com/keys', 'messages')).toBe('https://www.fastaitoken.com')
    expect(recognizeQuickAddBaseUrl('https://www.fastaitoken.com/usage', 'messages')).toBe(
      'https://www.fastaitoken.com'
    )
    expect(recognizeQuickAddBaseUrl('https://relay.example.com/v1/responses', 'messages')).toBe(
      'https://relay.example.com'
    )
  })

  it('规范化并去重多个 Base URL', () => {
    expect(
      normalizeQuickAddBaseUrls(['https://api.example.com/keys', 'https://api.example.com', 'not-a-url'], 'responses')
    ).toEqual(['https://api.example.com'])
  })

  it('仅对四类 LLM 协议执行发现', () => {
    expect(supportsQuickAddProtocolDiscovery('messages')).toBe(true)
    expect(supportsQuickAddProtocolDiscovery('responses')).toBe(true)
    expect(supportsQuickAddProtocolDiscovery('images')).toBe(false)
    expect(supportsQuickAddProtocolDiscovery('vectors')).toBe(false)
  })

  it('按渠道类型提供探测所需的默认 serviceType', () => {
    expect(defaultQuickAddServiceType('messages')).toBe('claude')
    expect(defaultQuickAddServiceType('responses')).toBe('responses')
  })

  it('只接受发现接口支持的协议类型', () => {
    expect(normalizeDiscoveredChannelKind('responses')).toBe('responses')
    expect(normalizeDiscoveredChannelKind('images')).toBeNull()
    expect(normalizeDiscoveredChannelKind('')).toBeNull()
  })
})

describe('inferQuickAddProviderId', () => {
  const zhipuKey = '0123456789abcdef0123456789abcdef.ABCDEFGHIJKLMNO1'

  it('识别智谱两个官方协议根及其完整端点', () => {
    expect(inferQuickAddProviderId(providers, ['https://open.bigmodel.cn/api/anthropic'], ['sk-any'])).toBe('glm')
    expect(
      inferQuickAddProviderId(providers, ['https://open.bigmodel.cn/api/paas/v4/chat/completions'], ['sk-any'])
    ).toBe('glm')
  })

  it('识别 OpenCode Zen/Go 完整端点且不误认相似域名', () => {
    expect(inferQuickAddProviderId(providers, ['https://opencode.ai/zen/go/v1/chat/completions'], ['sk-any'])).toBe(
      'opencode-zen'
    )
    expect(inferQuickAddProviderId(providers, ['https://opencode.ai/zen/v1'], ['sk-any'])).toBe('opencode-zen')
    expect(inferQuickAddProviderId(providers, ['https://opencode.ai.evil.example/zen/v1'], ['sk-any'])).toBe('')
  })

  it('没有 Base URL 时按 id.secret Key 识别智谱', () => {
    expect(inferQuickAddProviderId(providers, [''], [zhipuKey])).toBe('glm')
  })

  it('第三方 URL 优先，不能仅凭智谱样式 Key 标为官方', () => {
    expect(inferQuickAddProviderId(providers, ['https://relay.example/v1'], [zhipuKey])).toBe('')
  })

  it('混合官方和第三方 URL 时保持自定义模式', () => {
    expect(
      inferQuickAddProviderId(
        providers,
        ['https://open.bigmodel.cn/api/paas/v4', 'https://relay.example/v1'],
        [zhipuKey]
      )
    ).toBe('')
  })

  it('不会根据共享的 sk- Key 猜测 provider', () => {
    expect(inferQuickAddProviderId(providers, [''], ['sk-abcdefghijklmnopqrstuvwxyz123456'])).toBe('')
  })
})
