import { createRouter, createWebHistory } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

const routes = [
  {
    path: '/',
    redirect: '/channels/messages'  // 默认跳转到 Messages
  },
  {
    path: '/channels/:type',  // 动态参数匹配 messages/responses/gemini
    component: () => import('@/views/ChannelsView.vue'),  // 懒加载
    props: true,  // 将路由参数作为 props 传递
    meta: { requiresAuth: true }
  },
  {
    path: '/conversations',
    redirect: '/channels/messages',
    meta: { requiresAuth: true }
  },
  {
    path: '/site-onboarding',
    component: () => import('@/views/SiteOnboardingView.vue'),
    meta: { requiresAuth: true }
  }
]

const router = createRouter({
  history: createWebHistory(),  // 使用 HTML5 History 模式
  routes
})

// 认证守卫（可选，认证逻辑已在 App.vue 中处理）
router.beforeEach((to, from, next) => {
  const authStore = useAuthStore()
  if (to.meta.requiresAuth && !authStore.isAuthenticated) {
    // 认证对话框已在 App.vue 中处理，无需重定向
    next()
  } else {
    next()
  }
})

export default router
