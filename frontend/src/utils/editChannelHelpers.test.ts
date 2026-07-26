import { describe, expect, it } from 'vitest'
import { defaultStripBillingHeader, requiresAnthropicBillingHeader } from './editChannelHelpers'

describe('requiresAnthropicBillingHeader', () => {
  it('识别官方域名（含 scheme、路径、端口、尾部斜杠）', () => {
    expect(requiresAnthropicBillingHeader('api.anthropic.com')).toBe(true)
    expect(requiresAnthropicBillingHeader('https://api.anthropic.com')).toBe(true)
    expect(requiresAnthropicBillingHeader('http://api.anthropic.com/v1')).toBe(true)
    expect(requiresAnthropicBillingHeader('https://api.anthropic.com/')).toBe(true)
    expect(requiresAnthropicBillingHeader('https://api.anthropic.com:443/v1')).toBe(true)
    expect(requiresAnthropicBillingHeader('  HTTPS://API.ANTHROPIC.COM  ')).toBe(true)
  })

  it('子域名与第三方域名不视为官方', () => {
    expect(requiresAnthropicBillingHeader('https://proxy.api.anthropic.com')).toBe(false)
    expect(requiresAnthropicBillingHeader('https://proxy.api.anthropic.com:443')).toBe(false)
    expect(requiresAnthropicBillingHeader('https://api.deepseek.com/anthropic')).toBe(false)
    expect(requiresAnthropicBillingHeader('')).toBe(false)
  })
})

describe('defaultStripBillingHeader', () => {
  it('地址全为空时返回 false，等待用户填入后重算', () => {
    expect(defaultStripBillingHeader([])).toBe(false)
    expect(defaultStripBillingHeader(['', '   '])).toBe(false)
  })

  it('全部地址为官方时保留计费头', () => {
    expect(defaultStripBillingHeader(['https://api.anthropic.com'])).toBe(false)
    expect(defaultStripBillingHeader(['https://api.anthropic.com', 'https://api.anthropic.com/v1'])).toBe(false)
  })

  it('任一地址为第三方时默认剔除', () => {
    expect(defaultStripBillingHeader(['https://api.deepseek.com/anthropic'])).toBe(true)
    expect(defaultStripBillingHeader(['https://api.anthropic.com', 'https://api.deepseek.com/anthropic'])).toBe(true)
  })
})
