import { describe, expect, it } from 'vitest'

import {
  extractChannelNamePrefix,
  filterValidSupportedModelPatterns,
  isValidSupportedModelPattern,
  parseSupportedModelInput,
  syncBaseUrlsFormState
} from './channel-dialog-state'

describe('extractChannelNamePrefix', () => {
  it('保留常见 API 域名的服务商主体', () => {
    expect(extractChannelNamePrefix('https://api.openai.com/v1')).toBe('api-openai')
    expect(extractChannelNamePrefix('https://www.anthropic.com')).toBe('anthropic')
  })

  it('应为多级子域名保留可区分的前缀', () => {
    expect(extractChannelNamePrefix('https://api.us-east-1.openai.com/v1')).toBe('api-us-east-1-openai')
    expect(extractChannelNamePrefix('https://relay.team.example.com.cn/v1')).toBe('relay-team-example')
    expect(extractChannelNamePrefix('https://worker.demo.pages.dev/v1')).toBe('worker-demo')
  })

  it('应为 IP 和本地域名生成稳定可读的前缀', () => {
    expect(extractChannelNamePrefix('http://192.168.1.8:11434/v1')).toBe('192-168-1-8-11434')
    expect(extractChannelNamePrefix('http://127.0.0.1/v1')).toBe('127-0-0-1')
    expect(extractChannelNamePrefix('http://localhost:11434/v1')).toBe('localhost-11434')
    expect(extractChannelNamePrefix('http://[::1]:11434/v1')).toBe('ipv6-1-11434')
  })

  it('无效 URL 应回退到通用前缀', () => {
    expect(extractChannelNamePrefix('not a url')).toBe('channel')
    expect(extractChannelNamePrefix('')).toBe('channel')
  })

  it('多 label 域名带端口时应保留端口后缀', () => {
    expect(extractChannelNamePrefix('https://api.example.com:8443/v1')).toBe('api-example-8443')
    expect(extractChannelNamePrefix('https://api.us-east-1.openai.com:9443/v1')).toBe('api-us-east-1-openai-9443')
  })
})

describe('syncBaseUrlsFormState', () => {
  it('Copilot Base URL 不应因空版本前缀被清空', () => {
    expect(syncBaseUrlsFormState('https://api.githubcopilot.com', 'copilot')).toEqual({
      baseUrl: 'https://api.githubcopilot.com',
      baseUrls: [],
    })
  })
})

describe('isValidSupportedModelPattern', () => {
  it('支持精确、前缀、后缀、包含和排除规则', () => {
    expect(isValidSupportedModelPattern('gpt-4o')).toBe(true)
    expect(isValidSupportedModelPattern('gpt-4*')).toBe(true)
    expect(isValidSupportedModelPattern('*image')).toBe(true)
    expect(isValidSupportedModelPattern('*image*')).toBe(true)
    expect(isValidSupportedModelPattern('!*image*')).toBe(true)
  })

  it('拒绝非法中间通配和空规则', () => {
    expect(isValidSupportedModelPattern('foo*bar')).toBe(false)
    expect(isValidSupportedModelPattern('**')).toBe(false)
    expect(isValidSupportedModelPattern('')).toBe(false)
    expect(isValidSupportedModelPattern('   ')).toBe(false)
    expect(isValidSupportedModelPattern('!')).toBe(false)
    expect(isValidSupportedModelPattern('!!gpt-4*')).toBe(false)
  })

  it('拒绝包含中文顿号、逗号等非法字符的规则', () => {
    expect(isValidSupportedModelPattern('gpt-5*、ada*')).toBe(false)
    expect(isValidSupportedModelPattern('gpt-5*,ada*')).toBe(false)
    expect(isValidSupportedModelPattern('gpt 5')).toBe(false)
    expect(isValidSupportedModelPattern('模型')).toBe(false)
  })
})

describe('parseSupportedModelInput', () => {
  it('按中文顿号拆分多条规则', () => {
    expect(parseSupportedModelInput('GPT-5*、ada*')).toEqual(['GPT-5*', 'ada*'])
  })

  it('兼容逗号、分号、竖线和多余空白', () => {
    expect(parseSupportedModelInput('a, b ; c | d')).toEqual(['a', 'b', 'c', 'd'])
    expect(parseSupportedModelInput('  gpt-4*  ，  *image*  ')).toEqual(['gpt-4*', '*image*'])
  })

  it('过滤纯空白与空项', () => {
    expect(parseSupportedModelInput('、、 ,, ；')).toEqual([])
    expect(parseSupportedModelInput('')).toEqual([])
  })
})

describe('filterValidSupportedModelPatterns', () => {
  it('过滤非法规则并保留合法规则顺序', () => {
    expect(filterValidSupportedModelPatterns([' gpt-4* ', 'foo*bar', '!*image*'])).toEqual({
      validPatterns: ['gpt-4*', '!*image*'],
      hasInvalidPatterns: true
    })
  })

  it('全部合法时不标记错误', () => {
    expect(filterValidSupportedModelPatterns(['gpt-4*', '*image*'])).toEqual({
      validPatterns: ['gpt-4*', '*image*'],
      hasInvalidPatterns: false
    })
  })

  it('自动拆分含顿号的粘贴输入为多条合法规则', () => {
    expect(filterValidSupportedModelPatterns(['GPT-5*、ada*'])).toEqual({
      validPatterns: ['GPT-5*', 'ada*'],
      hasInvalidPatterns: false
    })
  })
})
