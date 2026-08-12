import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { ApiService } from './api'

const fetchMock = vi.fn()
vi.stubGlobal('fetch', fetchMock)

function ok(body: unknown = {}) {
  return Promise.resolve(new Response(JSON.stringify(body), { status: 200, headers: { 'Content-Type': 'application/json' } }))
}

describe('management api paths and payloads', () => {
  beforeEach(() => { setActivePinia(createPinia()); fetchMock.mockReset(); fetchMock.mockImplementation(() => ok()) })

  it('patches key multiplier using stable identities and preserves null/zero', async () => {
    await new ApiService().patchKeyMultiplier('messages', 'channel/1', 'key 1', { groupMultiplier: 0, maxGroupMultiplier: null })
    expect(fetchMock).toHaveBeenCalledWith('/api/messages/channels/channel%2F1/keys/key%201/multiplier', expect.objectContaining({ method: 'PATCH', body: '{"groupMultiplier":0,"maxGroupMultiplier":null}' }))
  })

  it('patches key consumption policy as a tri-state field', async () => {
    await new ApiService().patchKeyMultiplier('messages', 'ch/1', 'k/1', { consumptionPolicy: 'opportunistic' })
    expect(fetchMock).toHaveBeenCalledWith('/api/messages/channels/ch%2F1/keys/k%2F1/multiplier', expect.objectContaining({
      method: 'PATCH',
      body: '{"consumptionPolicy":"opportunistic"}',
    }))
  })

  it('uses billing and exchange-rate endpoints with exact payloads', async () => {
    const api = new ApiService()
    await api.patchSubscriptionBillingTerms('sub/1', { paymentAmount: null, paymentUnit: '', creditAmount: null, creditUnit: '', expectedVersion: 2 })
    expect(fetchMock.mock.calls[0][0]).toBe('/api/subscriptions/sub%2F1/billing-terms')
    expect((fetchMock.mock.calls[0][1] as RequestInit).body).toBe('{"paymentAmount":null,"paymentUnit":"","creditAmount":null,"creditUnit":"","expectedVersion":2}')
    await api.getExchangeRates()
    expect(fetchMock.mock.calls[1][0]).toBe('/api/autopilot/cost/exchange-rates')
    await api.replaceExchangeRates({ quotes: [], expectedSnapshotVersion: 4 })
    expect(fetchMock.mock.calls[2][0]).toBe('/api/autopilot/cost/exchange-rates')
    expect((fetchMock.mock.calls[2][1] as RequestInit).body).toBe('{"quotes":[],"expectedSnapshotVersion":4}')
  })
})
