<template>
  <v-container class="pa-6">
    <v-card rounded="lg">
      <v-card-title class="text-h5 pa-6">
        <v-icon class="mr-2">mdi-account-group</v-icon>
        用户管理
        <v-spacer />
        <v-text-field
          v-model="search"
          label="搜索..."
          prepend-inner-icon="mdi-magnify"
          density="compact"
          variant="outlined"
          hide-details
          class="ml-4"
          style="max-width: 250px"
        />
      </v-card-title>

      <v-data-table
        :headers="headers"
        :items="filteredUsers"
        :loading="loading"
        :items-per-page="20"
      >
        <template #item.plan="{ item }">
          <v-chip
            :color="item.plan === 'team' ? 'purple' : item.plan === 'pro' ? 'primary' : 'default'"
            size="small"
          >
            {{ item.plan.toUpperCase() }}
          </v-chip>
        </template>

        <template #item.isAdmin="{ item }">
          <v-icon :color="item.isAdmin ? 'success' : 'grey'">
            {{ item.isAdmin ? 'mdi-check-circle' : 'mdi-close-circle' }}
          </v-icon>
        </template>

        <template #item.createdAt="{ item }">
          {{ new Date(item.createdAt).toLocaleDateString() }}
        </template>

        <template #item.actions="{ item }">
          <v-menu>
            <template #activator="{ props }">
              <v-btn v-bind="props" icon variant="text" size="small">
                <v-icon>mdi-dots-vertical</v-icon>
              </v-btn>
            </template>
            <v-list density="compact">
              <v-list-item @click="changePlan(item, 'free')">
                <v-list-item-title>设为免费版</v-list-item-title>
              </v-list-item>
              <v-list-item @click="changePlan(item, 'pro')">
                <v-list-item-title>设为专业版</v-list-item-title>
              </v-list-item>
              <v-list-item @click="changePlan(item, 'team')">
                <v-list-item-title>设为团队版</v-list-item-title>
              </v-list-item>
              <v-divider />
              <v-list-item @click="deleteUser(item)" class="text-error">
                <v-list-item-title>删除用户</v-list-item-title>
              </v-list-item>
            </v-list>
          </v-menu>
        </template>
      </v-data-table>
    </v-card>
  </v-container>
</template>

<script setup lang="ts">
import { ref, computed, onMounted } from 'vue'
import { useUserStore } from '@/stores/user'

interface User {
  id: string
  email: string
  name: string
  plan: string
  isAdmin: boolean
  createdAt: string
}

const userStore = useUserStore()
const users = ref<User[]>([])
const loading = ref(false)
const search = ref('')

const headers = [
  { title: '用户名', key: 'name' },
  { title: '邮箱', key: 'email' },
  { title: '套餐', key: 'plan' },
  { title: '管理员', key: 'isAdmin' },
  { title: '注册日期', key: 'createdAt' },
  { title: '操作', key: 'actions', sortable: false },
]

const filteredUsers = computed(() => {
  if (!search.value) return users.value
  const q = search.value.toLowerCase()
  return users.value.filter(u =>
    u.name.toLowerCase().includes(q) || u.email.toLowerCase().includes(q)
  )
})

async function loadUsers() {
  loading.value = true
  try {
    const res = await fetch('/api/saas/admin/users', {
      headers: { Authorization: `Bearer ${userStore.token}` }
    })
    if (res.ok) {
      const data = await res.json()
      users.value = data.users || []
    }
  } finally {
    loading.value = false
  }
}

async function changePlan(user: User, plan: string) {
  try {
    const res = await fetch(`/api/saas/admin/users/${user.id}/plan`, {
      method: 'POST',
      headers: {
        'Authorization': `Bearer ${userStore.token}`,
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({ plan })
    })
    if (res.ok) {
      user.plan = plan
    }
  } catch {
    // 静默
  }
}

async function deleteUser(user: User) {
  if (!confirm(`确定要删除用户 ${user.email}？此操作不可撤销。`)) return
  try {
    const res = await fetch(`/api/saas/admin/users/${user.id}`, {
      method: 'DELETE',
      headers: { Authorization: `Bearer ${userStore.token}` }
    })
    if (res.ok) {
      users.value = users.value.filter(u => u.id !== user.id)
    }
  } catch {
    // 静默
  }
}

onMounted(loadUsers)
</script>
