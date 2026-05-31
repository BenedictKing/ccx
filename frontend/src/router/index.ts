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

export default router
