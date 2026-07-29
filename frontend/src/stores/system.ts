import { defineStore } from 'pinia'
import { ref } from 'vue'
import type { VersionInfo } from '@/services/version'

/**
 * 系统状态管理 Store
 *
 * 职责：
 * - 管理系统运行状态（running/error/connecting）
 * - 管理版本信息和版本检查状态
 */
export const useSystemStore = defineStore('system', () => {
  // ===== 状态 =====

  // 系统连接状态
  type SystemStatus = 'running' | 'error' | 'connecting'
  const systemStatus = ref<SystemStatus>('connecting')

  // 版本信息
  const versionInfo = ref<VersionInfo>({
    currentVersion: '',
    latestVersion: null,
    isLatest: false,
    hasUpdate: false,
    releaseUrl: null,
    lastCheckTime: 0,
    status: 'checking',
  })

  // 版本检查加载状态
  const isCheckingVersion = ref(false)

  // 更新对话框
  const updateDialogOpen = ref(false)

  // ===== 计算属性 =====

  // ===== 操作方法 =====

  /**
   * 设置系统状态
   */
  function setSystemStatus(status: SystemStatus) {
    systemStatus.value = status
  }

  /**
   * 设置版本信息
   */
  function setVersionInfo(info: VersionInfo) {
    versionInfo.value = info
  }

  /**
   * 更新当前版本号
   */
  function setCurrentVersion(version: string) {
    versionInfo.value.currentVersion = version
  }

  /**
   * 设置版本检查状态
   */
  function setCheckingVersion(checking: boolean) {
    isCheckingVersion.value = checking
  }

  function setUpdateDialogOpen(open: boolean) {
    updateDialogOpen.value = open
  }

  /**
   * 重置系统状态
   */
  function resetSystemState() {
    systemStatus.value = 'connecting'
    versionInfo.value = {
      currentVersion: '',
      latestVersion: null,
      isLatest: false,
      hasUpdate: false,
      releaseUrl: null,
      lastCheckTime: 0,
      status: 'checking',
    }
    isCheckingVersion.value = false
    updateDialogOpen.value = false
  }

  return {
    // 状态
    systemStatus,
    versionInfo,
    isCheckingVersion,
    updateDialogOpen,

    // 计算属性

    // 方法
    setSystemStatus,
    setVersionInfo,
    setCurrentVersion,
    setCheckingVersion,
    setUpdateDialogOpen,
    resetSystemState,
  }
})
