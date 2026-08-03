import { describe, expect, it } from 'vitest'
import { readFileSync } from 'node:fs'

describe('SubscriptionsView new-api integration', () => {
  const source = readFileSync(new URL('../views/SubscriptionsView.vue', import.meta.url), 'utf8')

  it('uses the complete form and avoids legacy hardcoded provisioning', () => {
    expect(source).toContain('<NewApiSubscriptionForm')
    expect(source).not.toContain("channelKind: 'messages'")
    expect(source).not.toContain('maxGroupMultiplier: 1.0')
    expect(source).not.toContain('newapi-${Date.now()}')
  })
})
