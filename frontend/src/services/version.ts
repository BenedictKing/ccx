/**
 * 版本检查服务
 * 参考 gpt-load 项目实现
 */

const CACHE_KEY = 'ccx-version-info'
const CACHE_DURATION = 30 * 60 * 1000 // 30分钟缓存
const ERROR_CACHE_DURATION = 5 * 60 * 1000 // 错误状态缓存5分钟，避免频繁请求
const GITHUB_API_TIMEOUT = 10000 // 10秒超时
const SEMVER_PATTERN = /^v?(?<major>0|[1-9]\d*)(?:\.(?<minor>0|[1-9]\d*))?(?:\.(?<patch>0|[1-9]\d*))?(?:-(?<prerelease>[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?(?:\+(?<build>[0-9A-Za-z-]+(?:\.[0-9A-Za-z-]+)*))?$/

export interface GitHubRelease {
  tag_name: string
  html_url: string
  published_at: string
  name: string
  prerelease?: boolean
}

export interface VersionInfo {
  currentVersion: string
  latestVersion: string | null
  isLatest: boolean
  hasUpdate: boolean
  releaseUrl: string | null
  lastCheckTime: number
  status: 'checking' | 'latest' | 'update-available' | 'error'
}

// 预发布版本标识正则（如 -rc1, -beta, -alpha 等）
const PRERELEASE_PATTERN = /-(alpha|beta|rc|dev|pre|canary|nightly)/i

interface ParsedSemver {
  major: number
  minor: number
  patch: number
  prerelease: string[]
}

function createTimeoutError(timeoutMs: number): Error {
  const error = new Error(`Request timed out after ${timeoutMs}ms`)
  error.name = 'TimeoutError'
  return error
}

function createTimeoutSignal(timeoutMs: number): AbortSignal {
  if (typeof AbortSignal.timeout === 'function') {
    return AbortSignal.timeout(timeoutMs)
  }

  const controller = new AbortController()
  setTimeout(() => controller.abort(createTimeoutError(timeoutMs)), timeoutMs)
  return controller.signal
}

function parseSemver(version: string): ParsedSemver | null {
  const match = version.trim().match(SEMVER_PATTERN)
  if (!match?.groups) {
    return null
  }

  return {
    major: Number(match.groups.major),
    minor: Number(match.groups.minor ?? '0'),
    patch: Number(match.groups.patch ?? '0'),
    prerelease: match.groups.prerelease?.split('.') ?? []
  }
}

function comparePrereleaseIdentifiers(current: string, latest: string): number {
  const currentIsNumeric = /^\d+$/.test(current)
  const latestIsNumeric = /^\d+$/.test(latest)

  if (currentIsNumeric && latestIsNumeric) {
    const currentNumber = Number(current)
    const latestNumber = Number(latest)
    if (currentNumber < latestNumber) return -1
    if (currentNumber > latestNumber) return 1
    return 0
  }

  if (currentIsNumeric) return -1
  if (latestIsNumeric) return 1

  if (current < latest) return -1
  if (current > latest) return 1
  return 0
}

export function compareSemverVersions(current: string, latest: string): number {
  const currentVersion = parseSemver(current)
  const latestVersion = parseSemver(latest)

  if (!currentVersion || !latestVersion) {
    const normalizedCurrent = current.trim()
    const normalizedLatest = latest.trim()
    if (normalizedCurrent < normalizedLatest) return -1
    if (normalizedCurrent > normalizedLatest) return 1
    return 0
  }

  if (currentVersion.major !== latestVersion.major) {
    return currentVersion.major < latestVersion.major ? -1 : 1
  }
  if (currentVersion.minor !== latestVersion.minor) {
    return currentVersion.minor < latestVersion.minor ? -1 : 1
  }
  if (currentVersion.patch !== latestVersion.patch) {
    return currentVersion.patch < latestVersion.patch ? -1 : 1
  }

  const currentPrerelease = currentVersion.prerelease
  const latestPrerelease = latestVersion.prerelease

  if (currentPrerelease.length === 0 && latestPrerelease.length === 0) {
    return 0
  }
  if (currentPrerelease.length === 0) {
    return 1
  }
  if (latestPrerelease.length === 0) {
    return -1
  }

  const maxLength = Math.max(currentPrerelease.length, latestPrerelease.length)
  for (let index = 0; index < maxLength; index++) {
    const currentIdentifier = currentPrerelease[index]
    const latestIdentifier = latestPrerelease[index]

    if (currentIdentifier === undefined) return -1
    if (latestIdentifier === undefined) return 1

    const comparison = comparePrereleaseIdentifiers(currentIdentifier, latestIdentifier)
    if (comparison !== 0) {
      return comparison
    }
  }

  return 0
}

class VersionService {
  private currentVersion: string = ''

  /**
   * 检查是否为预发布版本
   */
  private isPrerelease(version: string): boolean {
    return PRERELEASE_PATTERN.test(version)
  }

  /**
   * 设置当前版本（从 /health 端点获取）
   */
  setCurrentVersion(version: string): void {
    this.currentVersion = version
  }

  /**
   * 获取当前版本
   */
  getCurrentVersion(): string {
    return this.currentVersion
  }

  /**
   * 从缓存获取版本信息
   */
  private getCachedVersionInfo(): VersionInfo | null {
    try {
      const cached = localStorage.getItem(CACHE_KEY)
      if (!cached) {
        return null
      }

      const versionInfo: VersionInfo = JSON.parse(cached)
      const now = Date.now()

      // 根据状态选择不同的缓存时长
      const cacheDuration = versionInfo.status === 'error'
        ? ERROR_CACHE_DURATION
        : CACHE_DURATION

      // 检查缓存是否过期
      if (now - versionInfo.lastCheckTime > cacheDuration) {
        return null
      }

      // 检查缓存中的版本号是否与当前应用版本号一致
      if (versionInfo.currentVersion !== this.currentVersion) {
        this.clearCache()
        return null
      }

      return versionInfo
    } catch (error) {
      console.warn('Failed to parse cached version info:', error)
      localStorage.removeItem(CACHE_KEY)
      return null
    }
  }

  /**
   * 保存版本信息到缓存
   */
  private setCachedVersionInfo(info: VersionInfo): void {
    try {
      localStorage.setItem(CACHE_KEY, JSON.stringify(info))
    } catch (error) {
      console.warn('Failed to cache version info:', error)
    }
  }

  /**
   * 清除缓存
   */
  clearCache(): void {
    localStorage.removeItem(CACHE_KEY)
  }

  /**
   * 版本比较
   * @returns -1: current < latest (有更新), 0: 相等, 1: current > latest
   */
  private compareVersions(current: string, latest: string): number {
    return compareSemverVersions(current, latest)
  }

  /**
   * 从 GitHub API 获取最新正式版本（过滤预发布版本）
   */
  private async fetchLatestVersion(): Promise<GitHubRelease | null> {
    try {
      // 使用 /releases 端点获取最近的发布列表，然后过滤出第一个正式版本
      const response = await fetch(
        'https://api.github.com/repos/BenedictKing/ccx/releases?per_page=10',
        {
          headers: {
            Accept: 'application/vnd.github.v3+json',
          },
          signal: createTimeoutSignal(GITHUB_API_TIMEOUT),
        }
      )

      if (response.status === 200) {
        const releases: GitHubRelease[] = await response.json()
        // 过滤掉预发布版本，返回第一个正式版本
        const stableRelease = releases.find(
          release => !release.prerelease && !this.isPrerelease(release.tag_name)
        )
        return stableRelease || null
      }
      return null
    } catch (error) {
      console.warn('Failed to fetch latest version from GitHub:', error)
      return null
    }
  }

  /**
   * 检查更新
   */
  async checkForUpdates(): Promise<VersionInfo> {
    // 如果没有当前版本，返回错误状态
    if (!this.currentVersion) {
      return {
        currentVersion: '',
        latestVersion: null,
        isLatest: false,
        hasUpdate: false,
        releaseUrl: null,
        lastCheckTime: Date.now(),
        status: 'error',
      }
    }

    // 先检查缓存
    const cached = this.getCachedVersionInfo()
    if (cached) {
      return cached
    }

    // 创建初始状态
    const versionInfo: VersionInfo = {
      currentVersion: this.currentVersion,
      latestVersion: null,
      isLatest: false,
      hasUpdate: false,
      releaseUrl: null,
      lastCheckTime: Date.now(),
      status: 'checking',
    }

    // 获取最新版本
    try {
      const release = await this.fetchLatestVersion()

      if (release) {
        const comparison = this.compareVersions(this.currentVersion, release.tag_name)

        versionInfo.latestVersion = release.tag_name
        versionInfo.releaseUrl = release.html_url
        versionInfo.isLatest = comparison >= 0
        versionInfo.hasUpdate = comparison < 0
        versionInfo.status = comparison < 0 ? 'update-available' : 'latest'

        // 成功时缓存结果（30分钟）
        this.setCachedVersionInfo(versionInfo)
      } else {
        versionInfo.status = 'error'
        // 错误时也缓存（5分钟），避免频繁请求 GitHub API
        this.setCachedVersionInfo(versionInfo)
      }
    } catch (error) {
      console.warn('Version check failed:', error)
      versionInfo.status = 'error'
      // 错误时也缓存（5分钟），避免频繁请求 GitHub API
      this.setCachedVersionInfo(versionInfo)
    }

    return versionInfo
  }
}

// 导出单例
export const versionService = new VersionService()
