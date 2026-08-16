/**
 * 快速添加渠道 - 输入解析测试
 *
 * 测试 isValidApiKey 和 isValidUrl 工具函数
 */

import { describe, it, expect } from 'vitest'
import { isValidApiKey, isValidUrl, isZhipuApiKey, parseQuickInput } from './quickInputParser'

describe('API Key 识别', () => {
  describe('OpenAI 格式', () => {
    it('应识别 OpenAI Legacy 格式 (sk-xxx)', () => {
      expect(isValidApiKey('sk-7nKxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx1234')).toBe(true)
      expect(isValidApiKey('sk-abcdef1234567890abcdef1234567890abcdef123456')).toBe(true)
    })

    it('应识别 OpenAI Project 格式 (sk-proj-xxx)', () => {
      expect(isValidApiKey('sk-proj-Aw9' + 'x'.repeat(100))).toBe(true)
      expect(isValidApiKey('sk-proj-' + 'abcdef1234567890'.repeat(8))).toBe(true)
    })
  })

  describe('Anthropic Claude 格式', () => {
    it('应识别 Anthropic 格式 (sk-ant-api03-xxx)', () => {
      expect(isValidApiKey('sk-ant-api03-bK9' + 'x'.repeat(80))).toBe(true)
      expect(isValidApiKey('sk-ant-api03-' + 'abcdef1234567890'.repeat(6))).toBe(true)
    })
  })

  describe('Google Gemini 格式', () => {
    it('应识别 AIza 开头的 key', () => {
      // Google API Key 总长度 39 字符: AIza (4) + 35 字符
      expect(isValidApiKey('AIzaSyDxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx')).toBe(true)
      expect(isValidApiKey('AIzaXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXXX')).toBe(true)
    })

    it('不应识别非 AIza 开头的类似格式', () => {
      expect(isValidApiKey('AIzbSyDxxx')).toBe(false)
      expect(isValidApiKey('Aiza1234567')).toBe(false)
    })
  })

  describe('OpenRouter 格式', () => {
    it('应识别 OpenRouter 格式 (sk-or-v1-xxx)', () => {
      // OpenRouter 使用混合大小写字母数字
      expect(isValidApiKey('sk-or-v1-0ndQl1opjKLMNOPqrs' + 'x'.repeat(40))).toBe(true)
      expect(isValidApiKey('sk-or-v1-' + 'AbCdEf123456'.repeat(5))).toBe(true)
    })
  })

  describe('Hugging Face 格式', () => {
    it('应识别 hf_ 前缀', () => {
      expect(isValidApiKey('hf_AVd' + 'x'.repeat(31))).toBe(true)
      expect(isValidApiKey('hf_' + 'abcdef1234567890'.repeat(2) + 'ab')).toBe(true)
    })
  })

  describe('Groq 格式', () => {
    it('应识别 gsk_ 前缀', () => {
      expect(isValidApiKey('gsk_8sX' + 'x'.repeat(49))).toBe(true)
      expect(isValidApiKey('gsk_' + 'abcdef1234567890'.repeat(3) + 'abcd')).toBe(true)
    })
  })

  describe('Perplexity 格式', () => {
    it('应识别 pplx- 前缀', () => {
      expect(isValidApiKey('pplx-f9a' + 'x'.repeat(40))).toBe(true)
      expect(isValidApiKey('pplx-' + 'abcdef1234567890'.repeat(3))).toBe(true)
    })
  })

  describe('Replicate 格式', () => {
    it('应识别 r8_ 前缀', () => {
      expect(isValidApiKey('r8_G7b' + 'x'.repeat(20))).toBe(true)
      expect(isValidApiKey('r8_abcdef1234567890abcdef')).toBe(true)
    })
  })

  describe('智谱 AI 格式 (id.secret)', () => {
    it('应识别智谱 AI 的 id.secret 格式', () => {
      const keys = [
        '269abc123456789012345678.r8abcdef1234',
        'abcdefghij1234567890abcd.secretkey123456'
      ]
      for (const key of keys) {
        expect(isValidApiKey(key)).toBe(true)
        expect(isZhipuApiKey(key)).toBe(true)
      }
    })

    it('不把 JWT、短点号字符串或普通 sk- Key 当成智谱 Key', () => {
      expect(isZhipuApiKey('eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.signature')).toBe(false)
      expect(isZhipuApiKey('abc.def')).toBe(false)
      expect(isZhipuApiKey('sk-abcdefghijklmnopqrstuvwxyz123456')).toBe(false)
    })
  })

  describe('火山引擎格式', () => {
    it('应识别火山引擎 Ark UUID 格式', () => {
      expect(isValidApiKey('550e8400-e29b-41d4-a716-446655440000')).toBe(true)
      expect(isValidApiKey('123e4567-e89b-12d3-a456-426614174000')).toBe(true)
    })

    it('应识别火山引擎 IAM AK 格式', () => {
      expect(isValidApiKey('AKLTNmYyYz' + 'x'.repeat(20))).toBe(true)
      expect(isValidApiKey('AKLTabcdefghij1234567890abcdefgh')).toBe(true)
    })
  })

  describe('通用前缀格式 (xx-xxx / xx_xxx)', () => {
    it('应识别包含数字或混合大小写的后缀', () => {
      expect(isValidApiKey('sk-proj-abc123xyz')).toBe(true)
      expect(isValidApiKey('sk-1234567890abcdef')).toBe(true)
      expect(isValidApiKey('sk-abcDEFghiJKL')).toBe(true)
      expect(isValidApiKey('ut_abc123456789')).toBe(true)
      expect(isValidApiKey('api-key12345678901')).toBe(true)
      expect(isValidApiKey('cr_xxxxxxxxx123')).toBe(true)
    })

    it('不应识别单字母前缀', () => {
      expect(isValidApiKey('s-1234567890123')).toBe(false)
      expect(isValidApiKey('u_1234567890123')).toBe(false)
    })

    it('不应识别无分隔符的字符串', () => {
      expect(isValidApiKey('sk123')).toBe(false)
      expect(isValidApiKey('apikey')).toBe(false)
    })

    it('不应识别分隔符后无内容的字符串', () => {
      expect(isValidApiKey('sk-')).toBe(false)
      expect(isValidApiKey('ut_')).toBe(false)
    })
  })

  describe('宽松兜底格式（常见前缀 + 任意后缀）', () => {
    it('应识别 sk- 前缀的短密钥', () => {
      expect(isValidApiKey('sk-111')).toBe(true)
      expect(isValidApiKey('sk-x')).toBe(true)
      expect(isValidApiKey('sk-abc')).toBe(true)
      expect(isValidApiKey('sk-test')).toBe(true)
    })

    it('应识别其他常见前缀的短密钥', () => {
      expect(isValidApiKey('api-123')).toBe(true)
      expect(isValidApiKey('key-abc')).toBe(true)
      expect(isValidApiKey('ut_test')).toBe(true)
      expect(isValidApiKey('hf_short')).toBe(true)
      expect(isValidApiKey('gsk_x')).toBe(true)
      expect(isValidApiKey('cr_1')).toBe(true)
      expect(isValidApiKey('ms-test')).toBe(true)
      expect(isValidApiKey('r8_abc')).toBe(true)
      expect(isValidApiKey('pplx-x')).toBe(true)
    })

    it('不应识别未知前缀的短字符串', () => {
      expect(isValidApiKey('xx-111')).toBe(false)
      expect(isValidApiKey('foo-bar')).toBe(false)
      expect(isValidApiKey('test_key')).toBe(false)
    })
  })

  describe('配置键名格式（应被排除）', () => {
    it('不应识别全大写下划线分隔的配置键名', () => {
      expect(isValidApiKey('API_TIMEOUT_MS')).toBe(false)
      expect(isValidApiKey('ANTHROPIC_BASE_URL')).toBe(false)
      expect(isValidApiKey('ANTHROPIC_AUTH_TOKEN')).toBe(false)
      expect(isValidApiKey('CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC')).toBe(false)
      expect(isValidApiKey('DATABASE_URL')).toBe(false)
      expect(isValidApiKey('SECRET_KEY')).toBe(false)
    })

    it('不应识别带数字的配置键名', () => {
      expect(isValidApiKey('API_V2_KEY')).toBe(false)
      expect(isValidApiKey('REDIS_DB_0')).toBe(false)
    })
  })

  describe('JWT 格式', () => {
    it('应识别有效的 JWT', () => {
      const validJwt = 'eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U'
      expect(isValidApiKey(validJwt)).toBe(true)
    })

    it('应识别简短但有效的 JWT 格式', () => {
      // 至少 20 字符，有两个点
      expect(isValidApiKey('eyJhbGciOiJIUzI1Ni.eyJzdWIiOiIxMjM0.xxx')).toBe(true)
    })

    it('不应识别只有一个点的 JWT', () => {
      expect(isValidApiKey('eyJhbGciOiJIUzI1NiIs.xxx')).toBe(false)
    })

    it('不应识别过短的 JWT', () => {
      expect(isValidApiKey('eyJ.xxx.yyy')).toBe(false)
    })
  })

  describe('长字符串格式 (≥32 字符，需同时包含字母和数字)', () => {
    it('应识别 32+ 字符的字母数字混合字符串', () => {
      expect(isValidApiKey('abcdefghijklmnopqrstuvwxyz123456')).toBe(true)
      expect(isValidApiKey('ABCDEFGHIJKLMNOPQRSTUVWXYZ123456')).toBe(true)
      expect(isValidApiKey('72f988bf7ab9e0f0a1234567890abcde')).toBe(true) // Azure OpenAI 风格
    })

    it('应识别包含下划线和横线的长字符串', () => {
      expect(isValidApiKey('abcdefghijklmnop_qrstuvwxyz-12345')).toBe(true)
    })

    it('不应识别少于 32 字符的无前缀字符串', () => {
      expect(isValidApiKey('a'.repeat(31))).toBe(false)
      expect(isValidApiKey('shortkey')).toBe(false)
    })

    it('不应识别纯字母的长字符串', () => {
      expect(isValidApiKey('a'.repeat(32))).toBe(false)
      expect(isValidApiKey('abcdefghijklmnopqrstuvwxyzabcdef')).toBe(false)
    })

    it('不应识别包含特殊字符的字符串', () => {
      expect(isValidApiKey('a'.repeat(30) + '!@')).toBe(false)
      expect(isValidApiKey('abcdefghijklmnopqrstuvwxyz12345!')).toBe(false)
    })
  })

  describe('无效输入', () => {
    it('不应识别普通单词', () => {
      expect(isValidApiKey('hello')).toBe(false)
      expect(isValidApiKey('world')).toBe(false)
      expect(isValidApiKey('test')).toBe(false)
    })

    it('不应识别 URL', () => {
      expect(isValidApiKey('http://localhost')).toBe(false)
      expect(isValidApiKey('https://api.example.com')).toBe(false)
    })

    it('不应识别空字符串', () => {
      expect(isValidApiKey('')).toBe(false)
    })

    it('不应识别纯数字', () => {
      expect(isValidApiKey('12345678901234567890123456789012')).toBe(false)
    })
  })
})

describe('URL 识别', () => {
  describe('有效 URL', () => {
    it('应识别 localhost', () => {
      expect(isValidUrl('http://localhost')).toBe(true)
      expect(isValidUrl('http://localhost/')).toBe(true)
      expect(isValidUrl('http://localhost:3000')).toBe(true)
      expect(isValidUrl('http://localhost:3000/')).toBe(true)
      expect(isValidUrl('http://localhost:5688/v1')).toBe(true)
    })

    it('应识别域名', () => {
      expect(isValidUrl('https://api.openai.com')).toBe(true)
      expect(isValidUrl('https://api.openai.com/')).toBe(true)
      expect(isValidUrl('https://api.openai.com/v1')).toBe(true)
      expect(isValidUrl('https://api.anthropic.com/v1')).toBe(true)
    })

    it('应识别带端口的域名', () => {
      expect(isValidUrl('http://example.com:8080')).toBe(true)
      expect(isValidUrl('https://api.example.com:443/v1')).toBe(true)
    })

    it('应识别 IP 地址', () => {
      expect(isValidUrl('http://127.0.0.1')).toBe(true)
      expect(isValidUrl('http://192.168.1.1:8080')).toBe(true)
    })

    it('应识别子域名', () => {
      expect(isValidUrl('https://api.v2.example.com')).toBe(true)
      expect(isValidUrl('https://a.b.c.d.example.com/path')).toBe(true)
    })
  })

  describe('无效 URL', () => {
    it('不应识别不完整的 URL', () => {
      expect(isValidUrl('http://')).toBe(false)
      expect(isValidUrl('https://')).toBe(false)
      expect(isValidUrl('http:///')).toBe(false)
    })

    it('不应识别无协议的 URL', () => {
      expect(isValidUrl('localhost')).toBe(false)
      expect(isValidUrl('api.openai.com')).toBe(false)
      expect(isValidUrl('//api.openai.com')).toBe(false)
    })

    it('不应识别无效协议', () => {
      expect(isValidUrl('ftp://example.com')).toBe(false)
      expect(isValidUrl('ws://example.com')).toBe(false)
    })

    it('不应识别无效域名格式', () => {
      expect(isValidUrl('http://-example.com')).toBe(false)
      expect(isValidUrl('http://example-.com')).toBe(false)
    })
  })
})

describe('综合解析场景', () => {
  it('应根据 Claude Messages 协议路径推断上游类型并移除端点路径', () => {
    const result = parseQuickInput('https://api.example.com/v1/messages sk-key1234567890')
    expect(result.detectedServiceType).toBe('claude')
    expect(result.detectedBaseUrl).toBe('https://api.example.com')
  })

  it('应根据 OpenAI Chat 协议路径推断上游类型并移除端点路径', () => {
    const result = parseQuickInput('https://api.example.com/v1/chat/completions sk-key1234567890')
    expect(result.detectedServiceType).toBe('openai')
    expect(result.detectedBaseUrl).toBe('https://api.example.com')
  })

  it('detects OpenAI embeddings endpoints and strips the endpoint path', () => {
    const result = parseQuickInput('https://api.example.com/v1/embeddings sk-key1234567890')
    expect(result.detectedServiceType).toBe('openai')
    expect(result.detectedBaseUrl).toBe('https://api.example.com')
  })

  it('应根据 Responses 协议路径推断上游类型并移除端点路径', () => {
    const result = parseQuickInput('https://api.example.com/v1/responses sk-key1234567890')
    expect(result.detectedServiceType).toBe('responses')
    expect(result.detectedBaseUrl).toBe('https://api.example.com')
  })

  it('应根据 Gemini generateContent 协议路径推断上游类型并移除端点路径', () => {
    const result = parseQuickInput('https://generativelanguage.googleapis.com/v1beta/models/gemini-3-pro:generateContent AIza1234567890abcdefghijklmnopqrstuv')
    expect(result.detectedServiceType).toBe('gemini')
    expect(result.detectedBaseUrl).toBe('https://generativelanguage.googleapis.com')
  })

  it('应根据常见 Anthropic/Claude 路径线索推断 Claude 上游且保留非协议路径', () => {
    const result = parseQuickInput('https://relay.example.com/anthropic sk-key1234567890')
    expect(result.detectedServiceType).toBe('claude')
    expect(result.detectedBaseUrl).toBe('https://relay.example.com/anthropic')
  })

  it('应识别渠道中心内置但不包含协议关键词的常见入口', () => {
    expect(parseQuickInput('https://cp.compshare.cn sk-key1234567890').detectedServiceType).toBe('claude')
    expect(parseQuickInput('https://openrouter.ai/api sk-key1234567890').detectedServiceType).toBe('claude')
    expect(parseQuickInput('https://openrouter.ai/api/v1 sk-key1234567890').detectedServiceType).toBe('openai')
    expect(parseQuickInput('https://api.kimi.com/coding/v1 sk-key1234567890').detectedServiceType).toBe('openai')
    expect(parseQuickInput('https://api.githubcopilot.com sk-key1234567890').detectedServiceType).toBe('copilot')
    expect(parseQuickInput('https://api-ai.gitcode.com/v1 sk-key1234567890').detectedServiceType).toBe('openai')
  })

  it('应正确解析 URL + 多个 API Key', () => {
    const input = `
      https://api.openai.com/v1
      sk-key1abc123456
      sk-key2def789012
      sk-key3ghi345678
    `
    const result = parseQuickInput(input)
    expect(result.detectedBaseUrl).toBe('https://api.openai.com')
    expect(result.detectedApiKeys).toEqual(['sk-key1abc123456', 'sk-key2def789012', 'sk-key3ghi345678'])
  })

  it('应正确解析 localhost URL', () => {
    const input = 'http://localhost:5688 sk-1234567890ab sk-abcdef123456'
    const result = parseQuickInput(input)
    expect(result.detectedBaseUrl).toBe('http://localhost:5688')
    expect(result.detectedApiKeys).toEqual(['sk-1234567890ab', 'sk-abcdef123456'])
  })

  it('应正确解析混合分隔符', () => {
    const input = 'https://api.example.com, sk-key1234567890; ut_key2abc123456, api-key3def789012'
    const result = parseQuickInput(input)
    expect(result.detectedBaseUrl).toBe('https://api.example.com')
    expect(result.detectedApiKeys).toEqual(['sk-key1234567890', 'ut_key2abc123456', 'api-key3def789012'])
  })

  it('应忽略不完整的 URL', () => {
    const input = 'http:// sk-key1234567890'
    const result = parseQuickInput(input)
    expect(result.detectedBaseUrl).toBe('')
    expect(result.detectedApiKeys).toEqual(['sk-key1234567890'])
  })

  it('应只取第一个 URL', () => {
    const input = 'https://first.com https://second.com sk-key1234567890'
    const result = parseQuickInput(input)
    expect(result.detectedBaseUrl).toBe('https://first.com')
    expect(result.detectedApiKeys).toEqual(['sk-key1234567890'])
  })

  it('应去重 API Key', () => {
    const input = 'sk-key1234567890 sk-key1234567890 sk-key2abcdef123'
    const result = parseQuickInput(input)
    expect(result.detectedApiKeys).toEqual(['sk-key1234567890', 'sk-key2abcdef123'])
  })

  it('应保留 # 结尾（跳过版本号）', () => {
    const input = 'https://api.example.com/anthropic# sk-key1234567890'
    const result = parseQuickInput(input)
    expect(result.detectedBaseUrl).toBe('https://api.example.com/anthropic#')
  })

  it('应保留无路径的 # 结尾', () => {
    const input = 'https://api.example.com# sk-key1234567890'
    const result = parseQuickInput(input)
    expect(result.detectedBaseUrl).toBe('https://api.example.com#')
  })

  it('应移除末尾斜杠', () => {
    const input = 'https://api.example.com/ sk-key1234567890'
    const result = parseQuickInput(input)
    expect(result.detectedBaseUrl).toBe('https://api.example.com')
  })

  it('应剔除 new-api 控制台路径', () => {
    const input = 'https://stephecurry.asia/console/token sk-key1234567890'
    const result = parseQuickInput(input)
    expect(result.detectedBaseUrl).toBe('https://stephecurry.asia')
  })

  it('应剔除 console 下的 personal 路径', () => {
    const input = 'https://ai.muapi.cn/console/personal sk-key1234567890'
    const result = parseQuickInput(input)
    expect(result.detectedBaseUrl).toBe('https://ai.muapi.cn')
  })

  it('应剔除 done hub 的 panel token 路径', () => {
    const input = 'https://api.224442.xyz/panel/token sk-key1234567890'
    const result = parseQuickInput(input)
    expect(result.detectedBaseUrl).toBe('https://api.224442.xyz')
  })

  it('应剔除 sub2api 面板路径', () => {
    const input = 'https://ai.qaq.al/dashboard https://ai.qaq.al/keys sk-key1234567890'
    const result = parseQuickInput(input)
    expect(result.detectedBaseUrl).toBe('https://ai.qaq.al')
    expect(result.detectedBaseUrls).toEqual(['https://ai.qaq.al'])
  })

  it('应剔除 pricing 页面路径', () => {
    const input = 'https://pay.kxaug.xyz/pricing sk-key1234567890'
    const result = parseQuickInput(input)
    expect(result.detectedBaseUrl).toBe('https://pay.kxaug.xyz')
  })

  it('应剔除 api-keys 创建页面路径', () => {
    const input = 'https://aihub.top/api-keys/create sk-key1234567890'
    const result = parseQuickInput(input)
    expect(result.detectedBaseUrl).toBe('https://aihub.top')
    expect(result.detectedBaseUrls).toEqual(['https://aihub.top'])
  })

  it('应剔除 usage 页面路径但保留非根级同名 API 路径', () => {
    const dashboard = parseQuickInput('https://www.fastaitoken.com/usage sk-key1234567890')
    const apiPath = parseQuickInput('https://api.example.com/api/usage sk-key1234567890')

    expect(dashboard.detectedBaseUrl).toBe('https://www.fastaitoken.com')
    expect(apiPath.detectedBaseUrl).toBe('https://api.example.com/api/usage')
  })

  it('应剔除常见 admin 管理后台路径', () => {
    const input = 'https://chybenzun.top/admin sk-key1234567890'
    const result = parseQuickInput(input)
    expect(result.detectedBaseUrl).toBe('https://chybenzun.top')
    expect(result.detectedBaseUrls).toEqual(['https://chybenzun.top'])
  })

  it('应剔除 profile 与 wallet 管理后台路径', () => {
    const result = parseQuickInput(`
      https://chybenzun.top/profile
      https://chybenzun.top/wallet
      sk-key1234567890
    `)

    expect(result.detectedBaseUrl).toBe('https://chybenzun.top')
    expect(result.detectedBaseUrls).toEqual(['https://chybenzun.top'])
  })

})

describe('引号内容提取', () => {
  it('应从英文双引号中提取 URL 和 API Key', () => {
    const input = `"ANTHROPIC_AUTH_TOKEN": "sk-lACTyHP69FC46DeD8F67T3BLBkFJ4cE3879908bc4c38a336",
"ANTHROPIC_BASE_URL": "https://apic1.ohmycdn.com/api/v1/ai/openai/cc-omg"`
    const result = parseQuickInput(input)
    expect(result.detectedBaseUrl).toBe('https://apic1.ohmycdn.com/api/v1/ai/openai/cc-omg')
    expect(result.detectedApiKeys).toContain('sk-lACTyHP69FC46DeD8F67T3BLBkFJ4cE3879908bc4c38a336')
    // 不应识别配置键名
    expect(result.detectedApiKeys).not.toContain('ANTHROPIC_AUTH_TOKEN')
    expect(result.detectedApiKeys).not.toContain('ANTHROPIC_BASE_URL')
  })

  it('应从英文单引号中提取内容', () => {
    const input = `'sk-test123456789012' 'https://api.example.com/v1'`
    const result = parseQuickInput(input)
    expect(result.detectedBaseUrl).toBe('https://api.example.com')
    expect(result.detectedApiKeys).toEqual(['sk-test123456789012'])
  })

  it('应从中文双引号中提取内容', () => {
    const input = `"sk-chinese123456789""https://api.example.com"`
    const result = parseQuickInput(input)
    expect(result.detectedBaseUrl).toBe('https://api.example.com')
    expect(result.detectedApiKeys).toEqual(['sk-chinese123456789'])
  })

  it('应从中文单引号中提取内容', () => {
    const input = `'sk-chinese789012345''https://api.test.com'`
    const result = parseQuickInput(input)
    expect(result.detectedBaseUrl).toBe('https://api.test.com')
    expect(result.detectedApiKeys).toEqual(['sk-chinese789012345'])
  })

  it('应正确解析完整的 Claude Code 配置格式', () => {
    const input = `发一个20$的key，用起来还不错，你们试试，好像是官逆
snow里获取不到模型不知道为啥
{
  "env": {
    "ANTHROPIC_AUTH_TOKEN": "sk-lACTyHP69FC46DeD8F67T3BLBkFJ4cE3879908bc4c38a336",
    "ANTHROPIC_BASE_URL": "https://apic1.ohmycdn.com/api/v1/ai/openai/cc-omg",
    "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": 1
  }
}`
    const result = parseQuickInput(input)
    expect(result.detectedBaseUrl).toBe('https://apic1.ohmycdn.com/api/v1/ai/openai/cc-omg')
    expect(result.detectedApiKeys).toContain('sk-lACTyHP69FC46DeD8F67T3BLBkFJ4cE3879908bc4c38a336')
    // 不应识别配置键名
    expect(result.detectedApiKeys).not.toContain('ANTHROPIC_AUTH_TOKEN')
    expect(result.detectedApiKeys).not.toContain('ANTHROPIC_BASE_URL')
    expect(result.detectedApiKeys).not.toContain('CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC')
  })

  it('应正确解析 Claude Code settings.json 格式', () => {
    const input = `{
  "$schema": "https://json.schemastore.org/claude-code-settings.json",
  "env": {
    "API_TIMEOUT_MS": "200000",
    "ANTHROPIC_BASE_URL": "http://localhost:3688/",
    "ANTHROPIC_AUTH_TOKEN": "key",
    "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1"
  },
  "includeCoAuthoredBy": false
}`
    const result = parseQuickInput(input)
    expect(result.detectedBaseUrl).toBe('https://json.schemastore.org/claude-code-settings.json')
    // 不应识别任何配置键名
    expect(result.detectedApiKeys).not.toContain('API_TIMEOUT_MS')
    expect(result.detectedApiKeys).not.toContain('ANTHROPIC_BASE_URL')
    expect(result.detectedApiKeys).not.toContain('ANTHROPIC_AUTH_TOKEN')
    expect(result.detectedApiKeys).not.toContain('CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC')
  })

  it('应忽略引号内的非 URL/Key 内容', () => {
    const input = `"env": { "ANTHROPIC_AUTH_TOKEN": "sk-valid1234567890" }`
    const result = parseQuickInput(input)
    expect(result.detectedBaseUrl).toBe('')
    expect(result.detectedApiKeys).toContain('sk-valid1234567890')
    expect(result.detectedApiKeys).not.toContain('ANTHROPIC_AUTH_TOKEN')
  })

  it('应同时支持引号内容和普通分隔', () => {
    const input = `"sk-quoted123456789" sk-plain4567890123 https://api.example.com`
    const result = parseQuickInput(input)
    expect(result.detectedBaseUrl).toBe('https://api.example.com')
    expect(result.detectedApiKeys).toContain('sk-quoted123456789')
    expect(result.detectedApiKeys).toContain('sk-plain4567890123')
  })

  it('应支持单边引号（只有开头引号）', () => {
    const input = `"http://localhost:5689`
    const result = parseQuickInput(input)
    expect(result.detectedBaseUrl).toBe('http://localhost:5689')
  })

  it('应支持单边引号提取 API Key', () => {
    const input = `"sk-test1234567890`
    const result = parseQuickInput(input)
    expect(result.detectedApiKeys).toContain('sk-test1234567890')
  })

  it('应支持单边单引号', () => {
    const input = `'https://api.example.com/v1`
    const result = parseQuickInput(input)
    expect(result.detectedBaseUrl).toBe('https://api.example.com')
  })

  it('应支持混合完整引号和单边引号', () => {
    const input = `"https://api.example.com" "sk-key12345678901`
    const result = parseQuickInput(input)
    expect(result.detectedBaseUrl).toBe('https://api.example.com')
    expect(result.detectedApiKeys).toContain('sk-key12345678901')
  })
})

describe('Base URL 去重', () => {
  it('应去重等效 URL（忽略尾部斜杠）', () => {
    const input = `https://api.example.com/v1
https://api.example.com/v1/
sk-key1234567890`
    const result = parseQuickInput(input)
    expect(result.detectedBaseUrls).toEqual(['https://api.example.com'])
  })

  it('应保留带 # 的独立语义 URL', () => {
    const input = `https://api.example.com/v1
https://api.example.com/v1#
sk-key1234567890`
    const result = parseQuickInput(input)
    expect(result.detectedBaseUrls).toEqual([
      'https://api.example.com',
      'https://api.example.com/v1#'
    ])
  })

  it('应保留原始 URL 列表，供后续按最终 serviceType 重算', () => {
    const input = `https://host/v1
https://host
sk-key1234567890`
    const result = parseQuickInput(input, 'openai')
    expect(result.detectedBaseUrls).toEqual(['https://host'])
    expect(result.rawBaseUrls).toEqual(['https://host/v1', 'https://host'])
  })

  it('fallback serviceType 为 gemini 时不应提前合并 /v1 与根域名', () => {
    const input = `https://host/v1
https://host
sk-key1234567890`
    const result = parseQuickInput(input, 'gemini')
    expect(result.detectedBaseUrls).toEqual(['https://host/v1', 'https://host'])
    expect(result.rawBaseUrls).toEqual(['https://host/v1', 'https://host'])
  })

  it('应将根域名与默认版本前缀 URL 视为等效', () => {
    const input = `https://new.timefiles.online/v1
https://new.timefiles.online
sk-key1234567890`
    const result = parseQuickInput(input, 'openai')
    expect(result.detectedBaseUrls).toEqual(['https://new.timefiles.online'])
  })

  it('应将 Gemini 根域名与 /v1beta 视为等效', () => {
    const input = `https://generativelanguage.googleapis.com/v1beta
https://generativelanguage.googleapis.com
AIzaSyDxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx`
    const result = parseQuickInput(input, 'gemini')
    expect(result.detectedBaseUrls).toEqual(['https://generativelanguage.googleapis.com'])
  })

  it('应仅去重完全等效的带 # URL', () => {
    const input = `https://api.example.com/v1#
https://api.example.com/v1/#
https://api.example.com/v1
sk-key1234567890`
    const result = parseQuickInput(input)
    expect(result.detectedBaseUrls).toEqual([
      'https://api.example.com/v1#',
      'https://api.example.com'
    ])
  })

  it('应保留不同的 URL', () => {
    const input = `https://api.example.com/v1
https://backup.example.com/v1
sk-key1234567890`
    const result = parseQuickInput(input)
    expect(result.detectedBaseUrls).toEqual([
      'https://api.example.com',
      'https://backup.example.com'
    ])
  })
})

describe('裸字段名（应被排除）', () => {
  it('不应将裸 api_key 字面量识别为 key', () => {
    expect(isValidApiKey('api_key')).toBe(false)
    expect(isValidApiKey('API_KEY')).toBe(false)
    expect(isValidApiKey('apiKey')).toBe(false)
    expect(isValidApiKey('apikey')).toBe(false)
  })

  it('不应将裸 base_url / url / name 等字面量识别为 key', () => {
    expect(isValidApiKey('base_url')).toBe(false)
    expect(isValidApiKey('url')).toBe(false)
    expect(isValidApiKey('name')).toBe(false)
    expect(isValidApiKey('token')).toBe(false)
    expect(isValidApiKey('auth')).toBe(false)
    expect(isValidApiKey('secret')).toBe(false)
    expect(isValidApiKey('endpoint')).toBe(false)
  })

  it('应在英文冒号分隔的"字段名: 值"中只提取值', () => {
    const input = `url: https://codeapi.icu/
api_key:
sk-11111
sk-22222
sk-33333`
    const result = parseQuickInput(input)
    expect(result.detectedBaseUrls).toEqual(['https://codeapi.icu'])
    expect(result.detectedApiKeys).toEqual(['sk-11111', 'sk-22222', 'sk-33333'])
    // 关键：不能把 api_key 当作 key
    expect(result.detectedApiKeys).not.toContain('api_key')
    expect(result.detectedApiKeys).not.toContain('api_key:')
  })

  it('应在英文冒号紧跟值时正确切分', () => {
    const input = 'api_key:sk-abc123 url:https://api.example.com'
    const result = parseQuickInput(input)
    expect(result.detectedApiKeys).toEqual(['sk-abc123'])
    expect(result.detectedBaseUrls).toEqual(['https://api.example.com'])
  })

  it('应支持大小写协议的 URL 不被冒号分隔破坏', () => {
    const input = 'url: HTTPS://codeapi.icu/ api_key:sk-11111'
    const result = parseQuickInput(input)
    expect(result.detectedBaseUrls).toEqual(['HTTPS://codeapi.icu'])
    expect(result.detectedApiKeys).toEqual(['sk-11111'])
  })
})
