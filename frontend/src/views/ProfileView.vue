<template>
  <v-container class="pa-6" max-width="800">
    <v-card class="pa-6" rounded="lg">
      <v-card-title class="text-h5 mb-4">
        <v-icon class="mr-2">mdi-account-circle</v-icon>
        个人中心
      </v-card-title>

      <v-card-text>
        <!-- 用户信息 -->
        <v-row>
          <v-col cols="12" md="6">
            <v-text-field
              :model-value="userStore.user?.name"
              label="用户名"
              readonly
              variant="outlined"
              prepend-icon="mdi-account"
            />
          </v-col>
          <v-col cols="12" md="6">
            <v-text-field
              :model-value="userStore.user?.email"
              label="邮箱"
              readonly
              variant="outlined"
              prepend-icon="mdi-email"
            />
          </v-col>
        </v-row>

        <v-row>
          <v-col cols="12" md="6">
            <v-text-field
              :model-value="userStore.user?.plan?.toUpperCase()"
              label="当前套餐"
              readonly
              variant="outlined"
              prepend-icon="mdi-star"
            >
              <template #append>
                <v-btn variant="text" color="primary" size="small" to="/pricing">
                  升级
                </v-btn>
              </template>
            </v-text-field>
          </v-col>
        </v-row>

        <v-divider class="my-6" />

        <!-- API Key 管理 -->
        <div class="text-h6 mb-4">
          <v-icon class="mr-2">mdi-key-variant</v-icon>
          API Key
        </div>

        <v-alert v-if="copied" type="success" variant="tonal" class="mb-4" density="compact">
          API Key 已复制到剪贴板
        </v-alert>

        <v-text-field
          :model-value="displayedApiKey"
          label="您的 API Key"
          readonly
          variant="outlined"
          :type="showKey ? 'text' : 'password'"
          :append-inner-icon="showKey ? 'mdi-eye-off' : 'mdi-eye'"
          @click:append-inner="showKey = !showKey"
        >
          <template #append>
            <v-btn variant="text" color="primary" size="small" @click="copyKey">
              <v-icon>mdi-content-copy</v-icon>
            </v-btn>
          </template>
        </v-text-field>

        <v-btn
          color="warning"
          variant="tonal"
          class="mt-2"
          :loading="regenerating"
          @click="regenerateKey"
        >
          <v-icon class="mr-2">mdi-refresh</v-icon>
          重新生成
        </v-btn>

        <v-divider class="my-6" />

        <!-- 用量信息 -->
        <div class="text-h6 mb-4">
          <v-icon class="mr-2">mdi-chart-bar</v-icon>
          本月用量
        </div>

        <v-row v-if="usage">
          <v-col cols="12" md="4">
            <v-card variant="outlined" class="pa-4 text-center">
              <div class="text-h4 font-weight-bold">{{ usage.apiCalls }}</div>
              <div class="text-caption text-medium-emphasis">API 调用次数</div>
              <div class="text-caption" v-if="limits">
                {{ limits.maxRequests > 0 ? `上限 ${limits.maxRequests.toLocaleString()}` : '无限制' }}
              </div>
            </v-card>
          </v-col>
          <v-col cols="12" md="4">
            <v-card variant="outlined" class="pa-4 text-center">
              <div class="text-h4 font-weight-bold">{{ (usage.tokensIn / 1000).toFixed(0) }}K</div>
              <div class="text-caption text-medium-emphasis">输入 Tokens</div>
            </v-card>
          </v-col>
          <v-col cols="12" md="4">
            <v-card variant="outlined" class="pa-4 text-center">
              <div class="text-h4 font-weight-bold">{{ (usage.tokensOut / 1000).toFixed(0) }}K</div>
              <div class="text-caption text-medium-emphasis">输出 Tokens</div>
            </v-card>
          </v-col>
        </v-row>
        <v-skeleton-loader v-else type="card" />
      </v-card-text>
    </v-card>
  </v-container>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useUserStore } from '@/stores/user'
import { useRouter } from 'vue-router'

const userStore = useUserStore()
const router = useRouter()
const showKey = ref(false)
const copied = ref(false)
const regenerating = ref(false)
const displayedApiKey = ref('正在加载...')
const usage = ref<{ apiCalls: number; tokensIn: number; tokensOut: number } | null>(null)
const limits = ref<{ maxRequests: number; maxTokens: number; maxChannels: number; maxApiKeys: number } | null>(null)

async function loadData() {
  try {
    // 加载 API Key
    const keyRes = await fetch('/api/saas/me/api-key', {
      headers: { Authorization: `Bearer ${userStore.token}` }
    })
    if (keyRes.ok) {
      const data = await keyRes.json()
      displayedApiKey.value = data.api_key
    }

    // 加载用量
    const usageRes = await fetch('/api/saas/me/usage', {
      headers: { Authorization: `Bearer ${userStore.token}` }
    })
    if (usageRes.ok) {
      const data = await usageRes.json()
      usage.value = data.usage
      limits.value = data.planLimits
    }
  } catch {
    // 静默
  }
}

async function copyKey() {
  try {
    await navigator.clipboard.writeText(displayedApiKey.value)
    copied.value = true
    setTimeout(() => { copied.value = false }, 2000)
  } catch {
    // fallback
    const input = document.createElement('input')
    input.value = displayedApiKey.value
    document.body.appendChild(input)
    input.select()
    document.execCommand('copy')
    document.body.removeChild(input)
    copied.value = true
    setTimeout(() => { copied.value = false }, 2000)
  }
}

async function regenerateKey() {
  if (!confirm('确定要重新生成 API Key 吗？旧的 Key 将立即失效。')) return
  regenerating.value = true
  try {
    const res = await fetch('/api/saas/me/api-key/regenerate', {
      method: 'POST',
      headers: { Authorization: `Bearer ${userStore.token}` }
    })
    if (res.ok) {
      const data = await res.json()
      displayedApiKey.value = data.api_key
    }
  } finally {
    regenerating.value = false
  }
}

onMounted(loadData)
</script>
