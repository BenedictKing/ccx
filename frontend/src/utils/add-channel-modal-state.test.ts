import { describe, expect, it } from 'vitest'

import type { Channel } from '@/services/api'
import {
  filterValidSupportedModelPatterns,
  extractChannelNamePrefix,
  isValidSupportedModelPattern,
  parseSupportedModelInput,
  resolveChannelWatcherAction,
  syncBaseUrlsFormState
} from './add-channel-modal-state'

const sampleChannel: Channel = {
  index: 1,
  name: 'existing-channel',
  serviceType: 'openai',
  baseUrl: 'https://example.com/v1',
  apiKeys: ['sk-test'],
}

describe('resolveChannelWatcherAction', () => {
  it('新增模式打开时返回重置表单动作', () => {
    expect(resolveChannelWatcherAction({
      show: true,
      newChannel: null,
      oldChannel: null,
    })).toBe('reset-new-form')
  })

  it('编辑模式切入时返回回填动作', () => {
    expect(resolveChannelWatcherAction({
      show: true,
      newChannel: sampleChannel,
      oldChannel: null,
    })).toBe('load-edit-channel')
  })

  it('同一渠道静默保存后仅更新基线，不重置本地草稿', () => {
    expect(resolveChannelWatcherAction({
      show: true,
      newChannel: {
        ...sampleChannel,
        name: 'existing-channel-updated',
        baseUrl: 'https://example.com/v2'
      },
      oldChannel: sampleChannel,
    })).toBe('noop')
  })

  it('编辑态 channel 被清空时保持 noop，避免误切快速添加', () => {
    expect(resolveChannelWatcherAction({
      show: true,
      newChannel: null,
      oldChannel: sampleChannel,
    })).toBe('noop')
  })

  it('弹窗关闭时始终忽略 channel 变化', () => {
    expect(resolveChannelWatcherAction({
      show: false,
      newChannel: sampleChannel,
      oldChannel: null,
    })).toBe('noop')
  })
})

describe('syncBaseUrlsFormState', () => {
  it('应在当前 serviceType 语义下去重，但不要求回写原始文本', () => {
    expect(syncBaseUrlsFormState('https://host\nhttps://host/v1', 'openai')).toEqual({
      baseUrl: 'https://host',
      baseUrls: []
    })
  })

  it('应保留原始文本，便于后续按最终 serviceType 重算', () => {
    expect(syncBaseUrlsFormState('https://host/v1\nhttps://host', 'openai')).toEqual({
      baseUrl: 'https://host',
      baseUrls: []
    })

    expect(syncBaseUrlsFormState('https://host/v1\nhttps://host', 'gemini')).toEqual({
      baseUrl: 'https://host/v1',
      baseUrls: ['https://host/v1', 'https://host']
    })
  })

  it('应剔除粘贴 URL 中的 admin 管理后台路径', () => {
    expect(syncBaseUrlsFormState('https://chybenzun.top/admin', 'openai')).toEqual({
      baseUrl: 'https://chybenzun.top',
      baseUrls: []
    })
  })

  it('应剔除粘贴 URL 中的 profile 与 wallet 管理后台路径', () => {
    expect(syncBaseUrlsFormState('https://chybenzun.top/profile\nhttps://chybenzun.top/wallet', 'openai')).toEqual({
      baseUrl: 'https://chybenzun.top',
      baseUrls: []
    })
  })
})

describe('extractChannelNamePrefix', () => {
  it('保留常见 API 域名的服务商主体', () => {
    expect(extractChannelNamePrefix('https://api.openai.com/v1')).toBe('api-openai-com')
    expect(extractChannelNamePrefix('https://www.anthropic.com')).toBe('anthropic-com')
  })

  it('应为多级子域名保留可区分的前缀', () => {
    expect(extractChannelNamePrefix('https://api.us-east-1.openai.com/v1')).toBe('api-us-east-1-openai-com')
    expect(extractChannelNamePrefix('https://relay.team.example.com.cn/v1')).toBe('relay-team-example-com-cn')
    expect(extractChannelNamePrefix('https://worker.demo.pages.dev/v1')).toBe('worker-demo-pages-dev')
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

  it('应将完整非标准路径纳入名称并剥离版本尾段', () => {
    expect(extractChannelNamePrefix('https://load103.diyai.diy/proxy/feishu-glm-46')).toBe(
      'load103-diyai-diy-proxy-feishu-glm-46'
    )
    expect(extractChannelNamePrefix('https://host.com/tenant/a/v1')).toBe('host-com-tenant-a')
    expect(extractChannelNamePrefix('https://host.com/api/v5')).toBe('host-com')
    expect(extractChannelNamePrefix('https://host.com/api/tenant/v5')).toBe('host-com-api-tenant')
  })

  it('多 label 域名带端口时应保留端口后缀', () => {
    expect(extractChannelNamePrefix('https://api.example.com:8443/v1')).toBe('api-example-com-8443')
    expect(extractChannelNamePrefix('https://api.us-east-1.openai.com:9443/v1')).toBe('api-us-east-1-openai-com-9443')
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
