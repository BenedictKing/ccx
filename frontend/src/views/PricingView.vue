<template>
  <v-container class="pa-6" max-width="900">
    <div class="text-center mb-6">
      <div class="text-h4 font-weight-bold mb-2">选择适合您的套餐</div>
      <div class="text-body-1 text-medium-emphasis">灵活的定价方案，满足不同规模的需求</div>
    </div>

    <v-row>
      <v-col v-for="plan in plans" :key="plan.id" cols="12" md="4">
        <v-card
          :color="plan.popular ? 'primary' : undefined"
          variant="outlined"
          rounded="lg"
          class="pa-4 text-center h-100 d-flex flex-column"
          :class="{ 'border-primary': plan.popular }"
        >
          <v-card-item>
            <v-card-title class="text-h5 mb-2">{{ plan.name }}</v-card-title>
            <v-card-subtitle>{{ plan.description }}</v-card-subtitle>
          </v-card-item>

          <v-card-text class="flex-grow-1">
            <div class="text-h3 font-weight-bold my-4">
              {{ plan.price === 0 ? '免费' : `$${(plan.price / 100).toFixed(2)}` }}
              <span v-if="plan.price > 0" class="text-body-2 text-medium-emphasis">/月</span>
            </div>

            <v-divider class="my-4" />

            <div class="text-left">
              <div v-for="feature in getFeatures(plan)" :key="feature" class="mb-2">
                <v-icon color="success" size="small" class="mr-2">mdi-check</v-icon>
                {{ feature }}
              </div>
            </div>
          </v-card-text>

          <v-card-actions class="justify-center pb-4">
            <v-btn
              v-if="plan.id === userStore.user?.plan"
              variant="tonal"
              color="success"
              block
              disabled
            >
              当前套餐
            </v-btn>
            <v-btn
              v-else-if="plan.id === 'free'"
              variant="tonal"
              color="secondary"
              block
              :to="userStore.isLoggedIn() ? '/profile' : '/register'"
            >
              {{ userStore.isLoggedIn() ? '个人中心' : '注册使用' }}
            </v-btn>
            <v-btn
              v-else
              variant="flat"
              :color="plan.popular ? 'primary' : 'secondary'"
              block
              :loading="upgrading"
              @click="upgradePlan(plan.id)"
            >
              {{ userStore.isLoggedIn() ? '升级到此套餐' : '注册使用' }}
            </v-btn>
          </v-card-actions>
        </v-card>
      </v-col>
    </v-row>
  </v-container>
</template>

<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useUserStore } from '@/stores/user'
import { useRouter } from 'vue-router'

interface PricingPlan {
  id: string
  name: string
  description: string
  price: number
  maxRequests: number
  maxTokens: number
  maxChannels: number
  maxApiKeys: number
  popular: boolean
}

const userStore = useUserStore()
const router = useRouter()
const plans = ref<PricingPlan[]>([])
const upgrading = ref(false)

function getFeatures(plan: PricingPlan): string[] {
  const f = [
    `每月 ${plan.maxRequests.toLocaleString()} 次 API 调用`,
    `每月 ${(plan.maxTokens / 1000000).toFixed(0)}M Tokens`,
    `最多 ${plan.maxChannels} 个渠道`,
    `最多 ${plan.maxApiKeys} 个 API Key`,
  ]
  if (plan.price > 0) {
    f.push('优先技术支持')
  }
  return f
}

async function upgradePlan(planId: string) {
  if (!userStore.isLoggedIn()) {
    window.location.href = '/register'
    return
  }

  // 当前已在该套餐
  if (userStore.user?.plan === planId) {
    return
  }

  // Free 套餐不需要支付
  if (planId === 'free') {
    router.push('/profile')
    return
  }

  upgrading.value = true
  try {
    const res = await fetch('/api/saas/create-checkout-session', {
      method: 'POST',
      headers: {
        'Authorization': `Bearer ${userStore.token}`,
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({ plan: planId }),
    })

    const data = await res.json()
    if (!res.ok) {
      throw new Error(data.error || '创建支付会话失败')
    }

    // 跳转到支付页面（Stripe Checkout 或 Mock 页面）
    window.location.href = data.url
  } catch (e) {
    const msg = e instanceof Error ? e.message : '升级失败'
    alert(`支付失败: ${msg}`)
  } finally {
    upgrading.value = false
  }
}

async function loadPricing() {
  try {
    const res = await fetch('/api/saas/pricing')
    if (res.ok) {
      plans.value = await res.json()
    }
  } catch {
    // 静默
  }
}

onMounted(loadPricing)
</script>
