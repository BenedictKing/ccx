import { defineStore } from 'pinia'
import { ref } from 'vue'

export interface UserInfo {
  id: string
  email: string
  name: string
  apiKey: string
  plan: string
  isAdmin: boolean
  createdAt: string
  updatedAt: string
}

export interface AuthResponse {
  token: string
  user: UserInfo
}

/**
 * SaaS 用户认证 Store
 *
 * 职责：
 * - 管理 JWT Token 和用户信息
 * - 提供注册/登录/登出 API
 * - 自动持久化到 localStorage
 */
export const useUserStore = defineStore('user', () => {
  // ===== 状态 =====
  const token = ref<string | null>(null)
  const user = ref<UserInfo | null>(null)
  const loading = ref(false)
  const error = ref('')

  // 从 localStorage 恢复
  const savedToken = localStorage.getItem('ccx-saas-token')
  const savedUser = localStorage.getItem('ccx-saas-user')
  if (savedToken) token.value = savedToken
  if (savedUser) {
    try {
      user.value = JSON.parse(savedUser)
    } catch {
      localStorage.removeItem('ccx-saas-user')
    }
  }

  // ===== 计算属性 =====
  const isLoggedIn = (): boolean => !!token.value
  const isAdmin = (): boolean => user.value?.isAdmin ?? false

  // ===== 方法 =====

  /**
   * 获取 API 基础路径
   */
  function getApiBase(): string {
    const baseUrl = document.querySelector('meta[name="api-base-url"]')?.getAttribute('content')
    return baseUrl || window.location.origin
  }

  /**
   * 发送认证请求
   */
  async function request(path: string, body: unknown): Promise<AuthResponse> {
    const response = await fetch(`${getApiBase()}/api/saas${path}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(body),
    })

    const data = await response.json()
    if (!response.ok) {
      throw new Error(data.error || '请求失败')
    }
    return data as AuthResponse
  }

  /**
   * 注册
   */
  async function register(email: string, password: string, name: string): Promise<void> {
    loading.value = true
    error.value = ''
    try {
      const res = await request('/register', { email, password, name })
      token.value = res.token
      user.value = res.user
      persistState()
    } catch (e: unknown) {
      error.value = e instanceof Error ? e.message : '注册失败'
      throw e
    } finally {
      loading.value = false
    }
  }

  /**
   * 登录
   */
  async function login(email: string, password: string): Promise<void> {
    loading.value = true
    error.value = ''
    try {
      const res = await request('/login', { email, password })
      token.value = res.token
      user.value = res.user
      persistState()
    } catch (e: unknown) {
      error.value = e instanceof Error ? e.message : '登录失败'
      throw e
    } finally {
      loading.value = false
    }
  }

  /**
   * 获取当前用户信息
   */
  async function fetchMe(): Promise<void> {
    if (!token.value) return
    try {
      const response = await fetch(`${getApiBase()}/api/saas/me`, {
        headers: {
          'Authorization': `Bearer ${token.value}`,
          'Content-Type': 'application/json',
        },
      })
      if (response.ok) {
        user.value = await response.json()
        localStorage.setItem('ccx-saas-user', JSON.stringify(user.value))
      }
    } catch {
      // 静默失败
    }
  }

  /**
   * 登出
   */
  function logout() {
    token.value = null
    user.value = null
    localStorage.removeItem('ccx-saas-token')
    localStorage.removeItem('ccx-saas-user')
  }

  /**
   * 持久化状态
   */
  function persistState() {
    if (token.value) {
      localStorage.setItem('ccx-saas-token', token.value)
    }
    if (user.value) {
      localStorage.setItem('ccx-saas-user', JSON.stringify(user.value))
    }
  }

  return {
    token,
    user,
    loading,
    error,
    isLoggedIn,
    isAdmin,
    register,
    login,
    fetchMe,
    logout,
  }
})
