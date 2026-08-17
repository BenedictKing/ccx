import { describe, expect, it } from 'vitest'
import {
  isAutoManagedAccountChannel,
  isManagedProviderChannel,
  isOfficialProviderChannel,
  providerDisplayName
} from './providerDisplay'

describe('providerDisplayName', () => {
  it('保留 provider 品牌的标准大小写', () => {
    expect(providerDisplayName('mimo')).toBe('MiMo')
    expect(providerDisplayName('volcengine')).toBe('火山方舟')
    expect(providerDisplayName('openai')).toBe('OpenAI')
    expect(providerDisplayName('opencode-zen')).toBe('OpenCode Zen / Go')
    expect(providerDisplayName('dashscope')).toBe('阿里云 DashScope')
    expect(providerDisplayName('tencent-lkeap')).toBe('腾讯云 TokenHub')
    expect(providerDisplayName('atomgit')).toBe('AtomGit')
    expect(providerDisplayName('tokenrhythm')).toBe('基元律动')
  })

  it('为未知 provider 生成可读名称', () => {
    expect(providerDisplayName('example-provider')).toBe('Example Provider')
    expect(providerDisplayName()).toBe('')
  })

  it('通过 provider 和 account 身份识别聚合托管渠道', () => {
    expect(isManagedProviderChannel({ providerId: 'mimo', accountUid: 'acct-1' } as never)).toBe(true)
    expect(isManagedProviderChannel({ autoManaged: true } as never)).toBe(false)
    expect(isManagedProviderChannel({ providerId: 'mimo' } as never)).toBe(false)
  })

  it('仅将官方 API 和官方套餐标记为官方渠道', () => {
    expect(isOfficialProviderChannel({ originType: 'official_api' } as never)).toBe(true)
    expect(isOfficialProviderChannel({ originType: 'official_token_plan' } as never)).toBe(true)
    expect(isOfficialProviderChannel({ originType: 'relay' } as never)).toBe(false)
    expect(isOfficialProviderChannel({ originType: 'unknown' } as never)).toBe(false)
  })

  it('通过 autoManaged 和 accountUid 识别自定义自动托管账号', () => {
    expect(isAutoManagedAccountChannel({ autoManaged: true, accountUid: 'acct-1' } as never)).toBe(true)
    expect(isAutoManagedAccountChannel({ providerId: 'mimo', accountUid: 'acct-1' } as never)).toBe(true)
    expect(isAutoManagedAccountChannel({ autoManaged: true } as never)).toBe(false)
    expect(isAutoManagedAccountChannel({ accountUid: 'acct-1' } as never)).toBe(false)
  })
})
