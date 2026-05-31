<template>
  <v-container class="fill-height d-flex align-center justify-center">
    <v-card class="pa-6" max-width="440" width="100%" rounded="lg" elevation="8">
      <v-card-title class="text-h4 text-center mb-2">
        🔐 CCX SaaS
      </v-card-title>
      <v-card-subtitle class="text-center mb-4">
        登录到您的账户
      </v-card-subtitle>

      <v-card-text>
        <v-alert v-if="userStore.error" type="error" variant="tonal" closable class="mb-4" @click:close="userStore.error = ''">
          {{ userStore.error }}
        </v-alert>

        <v-form @submit.prevent="handleLogin" ref="formRef">
          <v-text-field
            v-model="email"
            label="邮箱"
            type="email"
            variant="outlined"
            prepend-inner-icon="mdi-email"
            :rules="emailRules"
            class="mb-3"
            autocomplete="email"
          />

          <v-text-field
            v-model="password"
            label="密码"
            type="password"
            variant="outlined"
            prepend-inner-icon="mdi-lock"
            :rules="passwordRules"
            class="mb-4"
            autocomplete="current-password"
          />

          <v-btn
            type="submit"
            color="primary"
            block
            size="large"
            :loading="userStore.loading"
            class="mb-3"
          >
            登录
          </v-btn>
        </v-form>

        <v-divider class="my-4" />

        <div class="text-center">
          <span class="text-body-2 text-medium-emphasis">还没有账户？</span>
          <v-btn variant="text" color="primary" class="ml-1" to="/register">
            注册
          </v-btn>
        </div>
      </v-card-text>
    </v-card>
  </v-container>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useRouter } from 'vue-router'
import { useUserStore } from '@/stores/user'

const router = useRouter()
const userStore = useUserStore()

const formRef = ref<HTMLFormElement | null>(null)
const email = ref('')
const password = ref('')

const emailRules = [
  (v: string) => !!v || '请输入邮箱',
  (v: string) => /.+@.+\..+/.test(v) || '请输入有效的邮箱地址',
]

const passwordRules = [
  (v: string) => !!v || '请输入密码',
  (v: string) => v.length >= 6 || '密码至少 6 位',
]

async function handleLogin() {
  if (!formRef.value) return
  const { valid } = await formRef.value.validate()
  if (!valid) return

  try {
    await userStore.login(email.value, password.value)
    router.push('/channels/messages')
  } catch {
    // 错误已经在 userStore.error 中
  }
}
</script>

<style scoped>
.fill-height {
  min-height: 100vh;
}
</style>
