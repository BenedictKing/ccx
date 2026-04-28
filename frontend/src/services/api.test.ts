import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const clearAuth = vi.fn()

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    apiKey: 'test-access-key',
    clearAuth
  })
}))

vi.mock('@/stores/preferences', () => ({
  usePreferencesStore: () => ({
    uiLanguage: 'en'
  })
}))

import { API_REQUEST_TIMEOUT_MS, ApiService } from './api'

type FetchLike = typeof fetch

describe('ApiService request timeout', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    clearAuth.mockReset()
  })

  afterEach(() => {
    vi.useRealTimers()
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
  })

  it('aborts hanging API requests through the shared request entrypoint', async () => {
    vi.spyOn(AbortSignal, 'timeout').mockImplementation((timeout: number) => {
      const controller = new AbortController()
      const reason = new Error(`Request timed out after ${timeout}ms`)
      reason.name = 'TimeoutError'
      setTimeout(() => controller.abort(reason), timeout)
      return controller.signal
    })

    const fetchMock: FetchLike = vi.fn((_input: unknown, init?: RequestInit) => {
      return new Promise<Response>((_resolve, reject) => {
        const signal = init?.signal
        if (!(signal instanceof AbortSignal)) {
          reject(new Error('missing abort signal'))
          return
        }

        if (signal.aborted) {
          reject(signal.reason)
          return
        }

        signal.addEventListener(
          'abort',
          () => {
            reject(signal.reason)
          },
          { once: true }
        )
      })
    })

    vi.stubGlobal('fetch', fetchMock)

    const service = new ApiService()
    const pendingRequest = service.getChannels()
    const rejection = expect(pendingRequest).rejects.toMatchObject({ name: 'TimeoutError' })

    await vi.advanceTimersByTimeAsync(API_REQUEST_TIMEOUT_MS)

    await rejection
    expect(fetchMock).toHaveBeenCalledWith(
      '/api/messages/channels',
      expect.objectContaining({
        headers: expect.objectContaining({
          'Content-Type': 'application/json',
          'x-api-key': 'test-access-key'
        }),
        signal: expect.any(Object)
      })
    )
  })
})
