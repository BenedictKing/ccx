import { defineStore } from 'pinia'
import { ref } from 'vue'

import { getRuntimeLocale } from '@/i18n/core'
import type { SupportedLocale } from '@/i18n'

/**
 * 用户偏好设置 Store
 *
 * 职责：
 * - 管理暗色模式偏好（light/dark/auto）
 * - 管理全局统计面板展开状态
 * - 自动持久化到 localStorage
 */
export const usePreferencesStore = defineStore('preferences', () => {
  // ===== 状态 =====

  // 暗色模式偏好
  const darkModePreference = ref<'light' | 'dark' | 'auto'>('auto')

  // UI 语言（默认取运行时配置，persistedstate 有值时会自动覆盖）
  const uiLanguage = ref<SupportedLocale>(getRuntimeLocale())

  // 全局统计面板展开状态
  const showGlobalStats = ref(false)

  // 是否已看过新用户指引（首次登录自动弹出一次）
  const hasSeenGuide = ref(false)

  // 新渠道插入位置偏好：'top' = 队列顶部（默认，带 5 分钟促销期），'bottom' = 队列末尾（无促销期）
  const newChannelPlacement = ref<'top' | 'bottom'>('top')

  // ===== 操作方法 =====

  /**
   * 设置暗色模式
   */
  function setDarkMode(mode: 'light' | 'dark' | 'auto') {
    darkModePreference.value = mode
  }

  /**
   * 切换暗色模式（循环切换）
   */
  function toggleDarkMode() {
    const modes: Array<'light' | 'dark' | 'auto'> = ['light', 'dark', 'auto']
    const currentIndex = modes.indexOf(darkModePreference.value)
    const nextIndex = (currentIndex + 1) % modes.length
    darkModePreference.value = modes[nextIndex]
  }

  /**
   * 设置 UI 语言
   */
  function setUILanguage(language: SupportedLocale) {
    uiLanguage.value = language
  }

  /**
   * 初始化 UI 语言
   * 初始值已通过 getRuntimeLocale() 设置，persistedstate 插件会自动覆盖为已持久化值
   * 此方法仅用于应用启动时同步 document.lang 等副作用
   */
  function initializeUILanguage(_runtimeLanguage?: string) {
    // uiLanguage 此时已是正确值（持久化值 > 运行时默认值），无需额外处理
  }

  /**
   * 切换全局统计面板
   */
  function toggleGlobalStats() {
    showGlobalStats.value = !showGlobalStats.value
  }

  /**
   * 标记新用户指引已看过
   */
  function markGuideSeen() {
    hasSeenGuide.value = true
  }

  /**
   * 设置新渠道插入位置偏好
   */
  function setNewChannelPlacement(placement: 'top' | 'bottom') {
    newChannelPlacement.value = placement
  }

  return {
    // 状态
    darkModePreference,
    uiLanguage,
    showGlobalStats,
    hasSeenGuide,
    newChannelPlacement,

    // 方法
    setDarkMode,
    toggleDarkMode,
    setUILanguage,
    initializeUILanguage,
    toggleGlobalStats,
    markGuideSeen,
    setNewChannelPlacement,
  }
}, {
  // 持久化配置
  persist: {
    key: 'ccx-preferences',
    // 使用条件判断避免在非浏览器环境（SSR、Node 测试）中崩溃
    storage: typeof window !== 'undefined' ? localStorage : undefined,
  },
})
