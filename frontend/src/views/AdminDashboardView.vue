<template>
  <v-container class="pa-6" max-width="1200">
    <v-row class="mb-4">
      <v-col>
        <div class="text-h4 font-weight-bold">
          <v-icon class="mr-2">mdi-view-dashboard</v-icon>
          管理仪表盘
        </div>
        <div class="text-body-2 text-medium-emphasis mt-1">SaaS 平台运营数据总览</div>
      </v-col>
      <v-col cols="auto" class="d-flex align-center">
        <v-btn variant="tonal" color="primary" to="/admin/users" prepend-icon="mdi-account-group">
          用户管理
        </v-btn>
      </v-col>
    </v-row>

    <!-- KPI 卡片 -->
    <v-row v-if="stats">
      <v-col cols="12" sm="6" md="3">
        <v-card class="pa-4" rounded="lg" color="primary" variant="tonal">
          <v-card-text class="text-center">
            <div class="text-h3 font-weight-bold">{{ stats.totalUsers }}</div>
            <div class="text-body-2">总用户数</div>
          </v-card-text>
        </v-card>
      </v-col>
      <v-col cols="12" sm="6" md="3">
        <v-card class="pa-4" rounded="lg" color="success" variant="tonal">
          <v-card-text class="text-center">
            <div class="text-h3 font-weight-bold">{{ stats.paidUsers }}</div>
            <div class="text-body-2">付费用户</div>
          </v-card-text>
        </v-card>
      </v-col>
      <v-col cols="12" sm="6" md="3">
        <v-card class="pa-4" rounded="lg" color="info" variant="tonal">
          <v-card-text class="text-center">
            <div class="text-h3 font-weight-bold">{{ stats.activeUsers }}</div>
            <div class="text-body-2">月活跃用户</div>
          </v-card-text>
        </v-card>
      </v-col>
      <v-col cols="12" sm="6" md="3">
        <v-card class="pa-4" rounded="lg" color="warning" variant="tonal">
          <v-card-text class="text-center">
            <div class="text-h3 font-weight-bold">¥{{ ((stats?.revenue?.currentMonth ?? 0) / 100).toFixed(2) }}</div>
            <div class="text-body-2">本月收入</div>
          </v-card-text>
        </v-card>
      </v-col>
    </v-row>

    <v-skeleton-loader v-else type="card" />

    <!-- 第二行：用量概览 -->
    <v-row class="mt-2">
      <v-col cols="12" md="4">
        <v-card class="pa-4" rounded="lg">
          <div class="text-h6 mb-3">
            <v-icon class="mr-2">mdi-chart-bar</v-icon>
            本月用量
          </div>
          <v-list>
            <v-list-item>
              <template #prepend><v-icon color="primary">mdi-api</v-icon></template>
              <v-list-item-title class="text-h6">{{ stats?.monthlyApiUsage?.toLocaleString() }}</v-list-item-title>
              <v-list-item-subtitle>API 调用次数</v-list-item-subtitle>
            </v-list-item>
            <v-list-item>
              <template #prepend><v-icon color="info">mdi-counter</v-icon></template>
              <v-list-item-title class="text-h6">{{ ((stats?.monthlyTokens ?? 0) / 10000).toFixed(1) }}万</v-list-item-title>
              <v-list-item-subtitle>Token 用量</v-list-item-subtitle>
            </v-list-item>
            <v-list-item>
              <template #prepend><v-icon color="secondary">mdi-key-variant</v-icon></template>
              <v-list-item-title class="text-h6">{{ stats?.totalApiKeys }}</v-list-item-title>
              <v-list-item-subtitle>API Key 总数</v-list-item-subtitle>
            </v-list-item>
          </v-list>
        </v-card>
      </v-col>

      <v-col cols="12" md="4">
        <v-card class="pa-4" rounded="lg">
          <div class="text-h6 mb-3">
            <v-icon class="mr-2">mdi-pie-chart</v-icon>
            套餐分布
          </div>
          <v-list v-if="stats?.planBreakdown?.length">
            <v-list-item v-for="pb in stats.planBreakdown" :key="pb.plan">
              <template #prepend>
                <v-icon :color="planColor(pb.plan)">mdi-star</v-icon>
              </template>
              <v-list-item-title class="text-h6">{{ pb.count }}</v-list-item-title>
              <v-list-item-subtitle>{{ planLabel(pb.plan) }}</v-list-item-subtitle>
            </v-list-item>
          </v-list>
          <v-card-text v-else class="text-center text-medium-emphasis">
            暂无数据
          </v-card-text>
        </v-card>
      </v-col>

      <v-col cols="12" md="4">
        <v-card class="pa-4" rounded="lg">
          <div class="text-h6 mb-3">
            <v-icon class="mr-2">mdi-currency-usd</v-icon>
            收入详情
          </div>
          <v-list>
            <v-list-item>
              <template #prepend><v-icon color="success">mdi-calendar-month</v-icon></template>
              <v-list-item-title class="text-h6">¥{{ ((stats?.revenue?.currentMonth ?? 0) / 100).toFixed(2) }}</v-list-item-title>
              <v-list-item-subtitle>本月收入</v-list-item-subtitle>
            </v-list-item>
            <v-list-item>
              <template #prepend><v-icon color="primary">mdi-chart-line</v-icon></template>
              <v-list-item-title class="text-h6">¥{{ ((stats?.revenue?.totalRevenue ?? 0) / 100).toFixed(2) }}</v-list-item-title>
              <v-list-item-subtitle>总收入</v-list-item-subtitle>
            </v-list-item>
          </v-list>
        </v-card>
      </v-col>
    </v-row>

    <!-- 用量趋势（简表） -->
    <v-row class="mt-2">
      <v-col cols="12">
        <v-card class="pa-4" rounded="lg">
          <div class="text-h6 mb-3">
            <v-icon class="mr-2">mdi-trending-up</v-icon>
            近 30 天用量趋势
          </div>
          <v-table v-if="trends?.length">
            <thead>
              <tr>
                <th>日期</th>
                <th class="text-right">API 调用</th>
                <th class="text-right">Token 用量</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="t in trends" :key="t.date">
                <td>{{ t.date }}</td>
                <td class="text-right">{{ t.calls?.toLocaleString() }}</td>
                <td class="text-right">{{ (t.tokens / 1000).toFixed(0) }}K</td>
              </tr>
            </tbody>
          </v-table>
          <v-card-text v-else class="text-center text-medium-emphasis">
            暂无用量数据
          </v-card-text>
        </v-card>
      </v-col>
    </v-row>

    <!-- TOP 用户 -->
    <v-row class="mt-2">
      <v-col cols="12">
        <v-card class="pa-4" rounded="lg">
          <div class="text-h6 mb-3">
            <v-icon class="mr-2">mdi-account-star</v-icon>
            用量 Top 用户
          </div>
          <v-table v-if="topUsers?.length">
            <thead>
              <tr>
                <th>用户</th>
                <th>邮箱</th>
                <th>套餐</th>
                <th class="text-right">调用次数</th>
                <th class="text-right">Token 用量</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="u in topUsers" :key="u.id">
                <td>{{ u.name || u.id.slice(0, 8) }}</td>
                <td class="text-body-2">{{ u.email }}</td>
                <td>
                  <v-chip size="small" :color="planColor(u.plan)">{{ planLabel(u.plan) }}</v-chip>
                </td>
                <td class="text-right">{{ u.calls?.toLocaleString() }}</td>
                <td class="text-right">{{ (u.tokens / 1000).toFixed(0) }}K</td>
              </tr>
            </tbody>
          </v-table>
          <v-card-text v-else class="text-center text-medium-emphasis">
            暂无用量数据
          </v-card-text>
        </v-card>
      </v-col>
    </v-row>

    <!-- 加载状态 -->
    <v-overlay :model-value="loading" class="d-flex align-center justify-center">
      <v-progress-circular indeterminate size="48" />
    </v-overlay>
  </v-container>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useUserStore } from '@/stores/user'

const userStore = useUserStore()
const loading = ref(true)

const stats = ref<{
  totalUsers: number
  paidUsers: number
  activeUsers: number
  totalApiKeys: number
  monthlyApiUsage: number
  monthlyTokens: number
  planBreakdown: { plan: string; count: number }[]
  revenue: { currentMonth: number; totalRevenue: number }
} | null>(null)

const trends = ref<{ date: string; calls: number; tokens: number }[]>([])
const topUsers = ref<{ id: string; email: string; name: string; plan: string; calls: number; tokens: number }[]>([])

function planColor(plan: string) {
  switch (plan) {
    case 'free': return 'grey'
    case 'pro': return 'primary'
    case 'team': return 'success'
    default: return 'grey'
  }
}

function planLabel(plan: string) {
  switch (plan) {
    case 'free': return 'Free'
    case 'pro': return 'Pro'
    case 'team': return 'Team'
    default: return plan.toUpperCase()
  }
}

async function loadData() {
  try {
    // 加载仪表盘统计数据
    const statsRes = await fetch('/api/saas/admin/dashboard', {
      headers: { Authorization: `Bearer ${userStore.token}` }
    })
    if (statsRes.ok) stats.value = await statsRes.json()

    // 加载用量趋势
    const trendsRes = await fetch('/api/saas/admin/dashboard/trends', {
      headers: { Authorization: `Bearer ${userStore.token}` }
    })
    if (trendsRes.ok) {
      const data = await trendsRes.json()
      trends.value = data.trends
    }

    // 加载 Top 用户
    const topRes = await fetch('/api/saas/admin/dashboard/top-users', {
      headers: { Authorization: `Bearer ${userStore.token}` }
    })
    if (topRes.ok) {
      const data = await topRes.json()
      topUsers.value = data.users
    }
  } catch (e) {
    console.error('加载仪表盘数据失败:', e)
  } finally {
    loading.value = false
  }
}

onMounted(() => loadData())
</script>
