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

  it('expands add forms under the selected provider card', () => {
    expect(source).toContain('v-model:expanded-provider-id="expandedProviderId"')
    expect(source).toContain('#expand="{ providerId }"')
    expect(source).not.toContain('@select="handleProviderSelect"')
  })
})

describe('SubscriptionProviderGrid card actions', () => {
  const source = readFileSync(new URL('../components/subscriptions/SubscriptionProviderGrid.vue', import.meta.url), 'utf8')

  it('keeps add/site/console actions on every card and drops RunAPI from sponsors', () => {
    expect(source).toContain("t('subscription.addProvider')")
    expect(source).toContain("t('subscription.visitSite')")
    expect(source).toContain("t('subscription.visitConsole')")
    expect(source).toContain("id: 'new-api'")
    expect(source).toContain("id: 'github-copilot'")
    expect(source).not.toContain("providerId: 'runapi'")
    expect(source).not.toContain('runapiLogo')
  })
})
