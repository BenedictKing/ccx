import { createRouter, createWebHistory } from 'vue-router'

const routes = [
  {
    path: '/',
    redirect: '/channels/messages'
  },
  {
    path: '/login',
    component: () => import('@/views/LoginView.vue'),
    meta: { guest: true }
  },
  {
    path: '/register',
    component: () => import('@/views/RegisterView.vue'),
    meta: { guest: true }
  },
  {
    path: '/profile',
    component: () => import('@/views/ProfileView.vue'),
    meta: { requiresSaaSAuth: true }
  },
  {
    path: '/pricing',
    component: () => import('@/views/PricingView.vue'),
  },
  {
    path: '/admin/users',
    component: () => import('@/views/AdminUsersView.vue'),
    meta: { requiresSaaSAdmin: true }
  },
  {
    path: '/admin/dashboard',
    component: () => import('@/views/AdminDashboardView.vue'),
    meta: { requiresSaaSAdmin: true }
  },
  {
    path: '/channels/:type',
    component: () => import('@/views/ChannelsView.vue'),
    props: true,
    meta: { requiresAuth: true }
  },
  {
    path: '/conversations',
    component: () => import('@/views/ConversationsView.vue'),
    meta: { requiresAuth: true }
  }
]

const router = createRouter({
  history: createWebHistory(),
  routes
})

// SaaS 认证守卫
router.beforeEach((to, _from, next) => {
  // 从 localStorage 读取 SaaS token
  const token = localStorage.getItem('ccx-saas-token')

  if (to.meta.requiresSaaSAuth && !token) {
    next('/login')
    return
  }

  if (to.meta.requiresSaaSAdmin && !token) {
    next('/login')
    return
  }

  if (to.meta.requiresSaaSAdmin && token) {
    // 检查是否是管理员
    const userStr = localStorage.getItem('ccx-saas-user')
    if (userStr) {
      try {
        const user = JSON.parse(userStr)
        if (!user.isAdmin) {
          next('/')
          return
        }
      } catch {
        next('/login')
        return
      }
    }
  }

  next()
})

export default router
