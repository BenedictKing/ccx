import { defineStore } from 'pinia'
import { ref } from 'vue'
import type { Channel } from '@/services/api'

/**
 * 对话框状态管理 Store
 *
 * 职责：
 * - 管理添加/编辑渠道对话框状态
 * - 管理添加 API 密钥对话框状态
 * - 管理对话框相关的临时数据（编辑中的渠道、新密钥等）
 */
export const useDialogStore = defineStore('dialog', () => {
  // ===== 状态 =====

  // 添加/编辑渠道对话框
  const showAddChannelModal = ref(false)
  const showEditChannelModal = ref(false)
  const editingChannel = ref<Channel | null>(null)

  // 添加 API 密钥对话框
  const showAddKeyModal = ref(false)
  const selectedChannelForKey = ref<number>(-1)
  const newApiKey = ref('')

  // 通用确认对话框（替代 window.confirm，兼容 Wails iframe 环境）
  const showConfirmDialog = ref(false)
  const confirmDialogMessage = ref('')
  const confirmDialogConfirmText = ref('')
  const confirmDialogCancelText = ref('')
  const confirmDialogColor = ref<'primary' | 'error' | 'warning'>('error')
  let confirmResolver: ((_value: boolean) => void) | null = null

  // ===== 操作方法 =====

  /**
   * 打开添加渠道对话框
   */
  function openAddChannelModal() {
    editingChannel.value = null
    showEditChannelModal.value = false
    showAddChannelModal.value = true
  }

  /**
   * 打开编辑渠道对话框
   */
  function openEditChannelModal(channel: Channel) {
    showAddChannelModal.value = false
    editingChannel.value = channel
    showEditChannelModal.value = true
  }

  /**
   * 关闭渠道对话框
   */
  function closeAddChannelModal() {
    showAddChannelModal.value = false
  }

  function closeEditChannelModal() {
    showEditChannelModal.value = false
    editingChannel.value = null
  }

  /**
   * 打开添加密钥对话框
   */
  function openAddKeyModal(channelId: number) {
    selectedChannelForKey.value = channelId
    newApiKey.value = ''
    showAddKeyModal.value = true
  }

  /**
   * 关闭密钥对话框
   */
  function closeAddKeyModal() {
    showAddKeyModal.value = false
    selectedChannelForKey.value = -1
    newApiKey.value = ''
  }

  /**
   * 重置所有对话框状态
   */
  function resetDialogState() {
    showAddChannelModal.value = false
    showEditChannelModal.value = false
    editingChannel.value = null
    showAddKeyModal.value = false
    selectedChannelForKey.value = -1
    newApiKey.value = ''
  }

  /**
   * 打开通用确认对话框，返回 Promise<boolean>
   * 用于替代原生 window.confirm()，避免在 Wails iframe 环境下失效
   */
  function confirm(options: {
    message: string
    confirmText?: string
    cancelText?: string
    color?: 'primary' | 'error' | 'warning'
  }): Promise<boolean> {
    // 若已有未决的确认对话框，先解析为 false 避免泄漏
    if (confirmResolver) {
      confirmResolver(false)
      confirmResolver = null
    }
    confirmDialogMessage.value = options.message
    confirmDialogConfirmText.value = options.confirmText || ''
    confirmDialogCancelText.value = options.cancelText || ''
    confirmDialogColor.value = options.color || 'error'
    showConfirmDialog.value = true
    return new Promise((resolve) => {
      confirmResolver = resolve
    })
  }

  /**
   * 解析当前确认对话框的结果（由对话框按钮调用）
   */
  function resolveConfirm(value: boolean) {
    if (confirmResolver) {
      confirmResolver(value)
      confirmResolver = null
    }
    showConfirmDialog.value = false
  }

  return {
    // 状态
    showAddChannelModal,
    showEditChannelModal,
    editingChannel,
    showAddKeyModal,
    selectedChannelForKey,
    newApiKey,
    showConfirmDialog,
    confirmDialogMessage,
    confirmDialogConfirmText,
    confirmDialogCancelText,
    confirmDialogColor,

    // 方法
    openAddChannelModal,
    openEditChannelModal,
    closeAddChannelModal,
    closeEditChannelModal,
    openAddKeyModal,
    closeAddKeyModal,
    resetDialogState,
    confirm,
    resolveConfirm,
  }
})
