import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

import { compareSemverVersions, versionService } from './version'

interface StorageLike {
  readonly length: number
  clear(): void
  getItem(_key: string): string | null
  key(_index: number): string | null
  removeItem(_key: string): void
  setItem(_key: string, _value: string): void
}

function createLocalStorageMock(): StorageLike {
  const store = new Map<string, string>()

  return {
    get length() {
      return store.size
    },
    clear() {
      store.clear()
    },
    getItem(key: string) {
      return store.get(key) ?? null
    },
    key(index: number) {
      return Array.from(store.keys())[index] ?? null
    },
    removeItem(key: string) {
      store.delete(key)
    },
    setItem(key: string, value: string) {
      store.set(key, value)
    }
  }
}

describe('compareSemverVersions', () => {
  it('treats prerelease builds as older than the matching stable release', () => {
    expect(compareSemverVersions('v1.2.3-beta.1', 'v1.2.3')).toBe(-1)
    expect(compareSemverVersions('v1.2.3-rc1', 'v1.2.3')).toBe(-1)
  })

  it('compares prerelease identifiers in semver order', () => {
    expect(compareSemverVersions('v1.2.3-beta.1', 'v1.2.3-beta.2')).toBe(-1)
    expect(compareSemverVersions('v1.2.3-rc.2', 'v1.2.3-rc.10')).toBe(-1)
  })

  it('ignores build metadata when comparing versions', () => {
    expect(compareSemverVersions('v1.2.3+build.1', 'v1.2.3+build.9')).toBe(0)
    expect(compareSemverVersions('v1.2.3-rc.1+sha.1', 'v1.2.3-rc.1+sha.9')).toBe(0)
  })
})

describe('versionService.checkForUpdates', () => {
  beforeEach(() => {
    vi.stubGlobal('localStorage', createLocalStorageMock())
    versionService.clearCache()
  })

  afterEach(() => {
    versionService.setCurrentVersion('')
    versionService.clearCache()
    vi.restoreAllMocks()
    vi.unstubAllGlobals()
  })

  it('reports updates when the current build is a prerelease of the latest stable tag', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify([
            {
              tag_name: 'v1.2.3',
              html_url: 'https://example.com/releases/v1.2.3',
              published_at: '2026-04-28T00:00:00Z',
              name: 'v1.2.3',
              prerelease: false
            }
          ]),
          {
            status: 200,
            headers: {
              'Content-Type': 'application/json'
            }
          }
        )
      )
    )

    versionService.setCurrentVersion('v1.2.3-beta.1')

    const result = await versionService.checkForUpdates()

    expect(result.latestVersion).toBe('v1.2.3')
    expect(result.isLatest).toBe(false)
    expect(result.hasUpdate).toBe(true)
    expect(result.status).toBe('update-available')
  })
})
