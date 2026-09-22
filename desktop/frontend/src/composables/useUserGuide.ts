import { ref } from 'vue'

/**
 * 用户指引对话框的全局开关（模块级单例）。
 * Sidebar 帮助按钮与 App.vue 的首启自动弹出门槛共用同一状态，
 * 避免 Sidebar 在 defineModel 之外再声明 emits（vue-tsc 3.3.5 的 defineModel+defineEmits 组合存在类型缺陷）。
 */
const showUserGuide = ref(false)

export function useUserGuide() {
  return {
    showUserGuide,
    openUserGuide: () => {
      showUserGuide.value = true
    },
  }
}
