import { createRouter, createWebHistory } from 'vue-router'

const routes = [
  {
    path: '/',
    redirect: '/channels/messages'  // 默认跳转到统一渠道列表
  },
  {
    path: '/channels/chat',
    redirect: '/channels/messages'
  },
  {
    path: '/channels/responses',
    redirect: '/channels/messages'
  },
  {
    path: '/channels/gemini',
    redirect: '/channels/messages'
  },
  {
    path: '/channels/:type',  // 动态参数匹配 messages/images/vectors
    component: () => import('@/views/ChannelsView.vue'),  // 懒加载
    props: true,  // 将路由参数作为 props 传递
    meta: { requiresAuth: true }
  },
  {
    path: '/conversations',
    component: () => import('@/views/ConversationsView.vue'),
    meta: { requiresAuth: true }
  },
  {
    path: '/health',
    component: () => import('@/views/HealthCenterView.vue'),
    meta: { requiresAuth: true }
  },
  {
    path: '/subscriptions',
    component: () => import('@/views/SubscriptionsView.vue'),
    meta: { requiresAuth: true }
  },
  {
    path: '/cockpit',
    component: () => import('@/views/CockpitView.vue'),
    meta: { requiresAuth: true }
  },
  {
    path: '/autopilot',
    name: 'autopilot',
    component: () => import('@/views/AutopilotView.vue'),
    meta: { requiresAuth: true }
  },
  {
    path: '/cost-report',
    name: 'cost-report',
    component: () => import('@/views/CostReportView.vue'),
    meta: { requiresAuth: true }
  }
]

const router = createRouter({
  history: createWebHistory(),  // 使用 HTML5 History 模式
  routes
})

// 鉴权说明：路由 meta.requiresAuth 仅作语义标注，鉴权不在路由层强制。
// 实际门控由 App.vue 的持久认证对话框承担（未认证时 showAuthDialog 弹出、
// 阻塞页面交互，直至认证成功）。因此这里不注册 beforeEach 守卫——
// 此前的空转守卫（对所有路由一律 next()）易造成"有路由级鉴权"的误解，已移除（P8）。

export default router
