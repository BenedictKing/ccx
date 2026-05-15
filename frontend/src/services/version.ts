/**
 * 版本检查服务
 * 参考 gpt-load 项目实现
 */

const CACHE_KEY = 'ccx-version-info'
const CACHE_DURATION = 30 * 60 * 1000 // 30分钟缓存
const ERROR_CACHE_DURATION = 5 * 60 * 1000 // 错误状态缓存5分钟，避免频繁请求
const GITHUB_API_TIMEOUT = 10000 // 10秒超时

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
  publishedAt?: string | null
}

// 预发布版本标识正则（如 -rc1, -beta, -alpha 等）
const PRERELEASE_PATTERN = /-(alpha|beta|rc|dev|pre|canary|nightly)/i

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
    // 移除 'v' 前缀，按 '.' 分割成数组
    const currentParts = current.replace(/^v/, '').split('.').map(Number)
    const latestParts = latest.replace(/^v/, '').split('.').map(Number)

    // 遍历每一位版本号
    for (let i = 0; i < Math.max(currentParts.length, latestParts.length); i++) {
      const currentPart = currentParts[i] || 0
      const latestPart = latestParts[i] || 0

      if (currentPart < latestPart) {
        return -1 // 当前版本更低
      }
      if (currentPart > latestPart) {
        return 1 // 当前版本更高
      }
    }
    return 0 // 版本相同
  }

  /**
   * 从 GitHub API 获取最新正式版本（过滤预发布版本）
   */
  private async fetchLatestVersion(): Promise<GitHubRelease | null> {
    try {
      const controller = new AbortController()
      const timeoutId = setTimeout(() => controller.abort(), GITHUB_API_TIMEOUT)

      // 使用 /releases 端点获取最近的发布列表，然后过滤出第一个正式版本
      const response = await fetch(
        'https://api.github.com/repos/BenedictKing/ccx/releases?per_page=10',
        {
          headers: {
            Accept: 'application/vnd.github.v3+json',
          },
          signal: controller.signal,
        }
      )

      clearTimeout(timeoutId)

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

  /**
   * 通过后端 API 检查版本更新（替代直接调用 GitHub API）
   */
  async checkViaBackend(): Promise<VersionInfo> {
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

    const cached = this.getCachedVersionInfo()
    if (cached) {
      return cached
    }

    const versionInfo: VersionInfo = {
      currentVersion: this.currentVersion,
      latestVersion: null,
      isLatest: false,
      hasUpdate: false,
      releaseUrl: null,
      lastCheckTime: Date.now(),
      status: 'checking',
    }

    try {
      const baseUrl = import.meta.env.PROD ? '' : (import.meta.env.VITE_BACKEND_URL || '')
      const response = await fetch(`${baseUrl}/api/version/check`)
      if (!response.ok) {
        throw new Error(`API error: ${response.status}`)
      }
      const data = await response.json()
      if (data.error) {
        throw new Error(data.error)
      }
      versionInfo.latestVersion = data.latest?.version || null
      versionInfo.releaseUrl = data.latest?.url || null
      versionInfo.publishedAt = data.latest?.publishedAt || null
      versionInfo.hasUpdate = !!data.hasUpdate
      versionInfo.isLatest = !data.hasUpdate
      versionInfo.status = data.hasUpdate ? 'update-available' : 'latest'
      this.setCachedVersionInfo(versionInfo)
    } catch (error) {
      console.warn('Backend version check failed:', error)
      versionInfo.status = 'error'
      this.setCachedVersionInfo(versionInfo)
    }

    return versionInfo
  }

  /**
   * 触发更新（POST /api/version/update）
   * 需要管理权限（x-api-key header）
   */
  async triggerUpdate(): Promise<{ status: string; message: string }> {
    // Get API key from auth store
    const { useAuthStore } = await import('../stores/auth')
    const authStore = useAuthStore()
    const apiKey = authStore.apiKey
    if (!apiKey) {
      return { status: 'error', message: '未认证：请先登录管理控制台' }
    }

    const baseUrl = import.meta.env.PROD ? '' : (import.meta.env.VITE_BACKEND_URL || '')
    try {
      const response = await fetch(`${baseUrl}/api/version/update`, {
        method: 'POST',
        headers: {
          'x-api-key': apiKey as string,
        },
      })
      const data = await response.json()
    return { status: data.status, message: data.message }
    } catch (error) {
      return { status: 'error', message: `请求失败: ${error}` }
    }
  }

  /**
   * 查询更新进度状态（GET /api/version/status）
   */
  async checkUpdateStatus(): Promise<{ status: string; error: string; progress: number }> {
    const baseUrl = import.meta.env.PROD ? '' : (import.meta.env.VITE_BACKEND_URL || '')
    try {
      const response = await fetch(`${baseUrl}/api/version/status`)
      if (!response.ok) return { status: 'error', error: `HTTP ${response.status}`, progress: 0 }
      const data = await response.json()
      return { status: data.status, error: data.error || '', progress: data.progress || 0 }
    } catch (e) {
      return { status: 'error', error: `请求失败: ${e}`, progress: 0 }
    }
  }
}

// 导出单例
export const versionService = new VersionService()
