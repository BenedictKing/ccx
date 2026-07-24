<template>
  <div class="subscriptions-view">
    <!-- Provider 卡片网格 -->
    <SubscriptionProviderGrid @select="handleProviderSelect" @add="handleProviderAdd" />

    <!-- 服务商添加面板（key 前缀探测 + 自动建渠道） -->
    <v-expand-transition>
      <v-card v-if="addProvider" variant="outlined" class="pa-4 mt-6">
        <v-card-title class="text-h6 d-flex align-center">
          <v-icon color="secondary" class="mr-2">mdi-domain</v-icon>
          {{ addProvider.displayName }}
        </v-card-title>
        <v-card-text>
          <div class="text-body-2 text-medium-emphasis mb-4">{{ addProvider.description }}</div>
          <v-form @submit.prevent="handleProviderAddSubmit">
            <v-text-field
              v-model="addApiKey"
              :label="t('subscription.apiKeyLabel')"
              variant="outlined"
              density="compact"
              type="password"
              :placeholder="t('subscription.apiKeyPlaceholder')"
              autofocus
            />
          </v-form>
          <v-alert v-if="addError" color="error" variant="tonal" density="compact" class="mt-3">
            {{ addError }}
          </v-alert>
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" @click="cancelProviderAdd">{{ t('app.actions.cancel') }}</v-btn>
          <v-btn
            color="primary"
            :loading="addSubmitting"
            :disabled="!addApiKey.trim()"
            @click="handleProviderAddSubmit"
          >
            {{ t('app.actions.add') }}
          </v-btn>
        </v-card-actions>
      </v-card>
    </v-expand-transition>

    <!-- 右侧详情面板（根据选中 Provider 展示不同表单） -->
    <v-expand-transition>
      <div v-if="selectedProvider" class="mt-6">
        <!-- GitHub Copilot 详情 -->
        <v-card v-if="selectedProvider === 'github-copilot'" variant="outlined" class="pa-4">
          <v-card-title class="text-h6 d-flex align-center">
            <v-icon color="primary" class="mr-2">mdi-github</v-icon>
            GitHub Copilot
          </v-card-title>
          <v-card-text>
            <p class="text-body-2 text-medium-emphasis mb-4">
              {{ t('subscription.copilotDescription') }}
            </p>
            <v-alert color="info" variant="tonal" density="compact" class="mb-4">
              {{ t('subscription.copilotComingSoon') }}
            </v-alert>
          </v-card-text>
        </v-card>

        <!-- new-api 详情 -->
        <v-card v-if="selectedProvider === 'new-api'" variant="outlined" class="pa-4">
          <v-card-title class="text-h6 d-flex align-center">
            <v-icon color="warning" class="mr-2">mdi-server-network</v-icon>
            {{ t('subscription.newApi.connect') }}
          </v-card-title>
          <v-card-text>
            <v-form @submit.prevent="handleNewApiSubmit">
              <div class="d-flex flex-column ga-3">
                <v-text-field
                  v-model="newApiForm.baseUrl"
                  :label="t('subscription.newApi.baseUrl')"
                  placeholder="https://your-newapi-instance.com"
                  variant="outlined"
                  density="compact"
                  required
                />
                <v-text-field
                  v-model="newApiForm.accessToken"
                  :label="t('subscription.newApi.accessToken')"
                  variant="outlined"
                  density="compact"
                  type="password"
                  required
                />
                <v-expansion-panels variant="accordion">
                  <v-expansion-panel>
                    <v-expansion-panel-title>
                      {{ t('subscription.newApi.advancedOptions') }}
                    </v-expansion-panel-title>
                    <v-expansion-panel-text>
                      <v-text-field
                        v-model="newApiForm.userId"
                        :label="t('subscription.newApi.userId')"
                        variant="outlined"
                        density="compact"
                        class="mb-2"
                      />
                      <v-select
                        v-model="newApiForm.authTokenMode"
                        :label="t('subscription.newApi.authTokenMode')"
                        :items="authTokenModeOptions"
                        variant="outlined"
                        density="compact"
                        class="mb-2"
                      />
                      <v-text-field
                        v-model="newApiForm.displayName"
                        :label="t('subscription.field.name')"
                        variant="outlined"
                        density="compact"
                      />
                    </v-expansion-panel-text>
                  </v-expansion-panel>
                </v-expansion-panels>
              </div>

              <v-alert
                v-if="newApiVerifyResult"
                color="success"
                variant="tonal"
                density="compact"
                class="mt-3"
              >
                <div class="text-body-2">
                  <div>{{ t('subscription.newApi.username') }}: {{ newApiVerifyResult.username }}</div>
                  <div>{{ t('subscription.newApi.quota') }}: {{ newApiVerifyResult.quota }}</div>
                  <div v-if="newApiVerifyResult.availableModels?.length">
                    {{ t('subscription.newApi.availableModels') }}: {{ newApiVerifyResult.availableModels.length }}
                  </div>
                </div>
              </v-alert>

              <v-alert
                v-if="newApiError"
                color="error"
                variant="tonal"
                density="compact"
                class="mt-3"
              >
                {{ newApiError }}
              </v-alert>
            </v-form>
          </v-card-text>
          <v-card-actions>
            <v-spacer />
            <v-btn variant="text" @click="selectedProvider = ''">{{ t('app.actions.cancel') }}</v-btn>
            <v-btn
              v-if="!newApiVerifyResult"
              color="primary"
              :loading="newApiVerifying"
              :disabled="!canNewApiVerify"
              @click="handleNewApiVerify"
            >
              {{ t('subscription.newApi.verify') }}
            </v-btn>
            <v-btn
              v-else
              color="primary"
              :loading="newApiProvisioning"
              @click="handleNewApiProvision"
            >
              {{ t('subscription.newApi.provision') }}
            </v-btn>
          </v-card-actions>
        </v-card>
      </div>
    </v-expand-transition>

    <!-- 提示信息 -->
    <v-alert color="info" variant="tonal" density="compact" class="mt-6">
      <v-icon start>mdi-information</v-icon>
      {{ t('subscription.manageInChannels') }}
    </v-alert>

    <!-- Snackbar -->
    <v-snackbar v-model="snackbar.show" :color="snackbar.color" :timeout="3000">
      {{ snackbar.message }}
    </v-snackbar>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from '@/i18n'
import { api } from '@/services/api'
import SubscriptionProviderGrid from '@/components/subscriptions/SubscriptionProviderGrid.vue'
import {
  autoAddChannel,
  extractAutoAddErrorMessage,
  getProviderTemplates,
  type ProviderTemplate,
} from '@/services/autopilot-api'
import type { NewApiVerifyResponse, NewApiProvisionResponse } from '@/services/api-types'

const { t } = useI18n()

const selectedProvider = ref('')
const snackbar = ref({ show: false, message: '', color: 'success' })

// 服务商添加（key 前缀探测 + 自动建渠道）
const addProvider = ref<ProviderTemplate | null>(null)
const addApiKey = ref('')
const addSubmitting = ref(false)
const addError = ref('')

// new-api 表单
const newApiForm = ref({
  baseUrl: '',
  accessToken: '',
  userId: '',
  authTokenMode: 'bearer',
  displayName: '',
})
const newApiVerifying = ref(false)
const newApiProvisioning = ref(false)
const newApiVerifyResult = ref<NewApiVerifyResponse | null>(null)
const newApiError = ref('')

const authTokenModeOptions = computed(() => [
  { title: 'Bearer', value: 'bearer' },
  { title: 'Raw', value: 'raw' },
])

const canNewApiVerify = computed(() => {
  return !!newApiForm.value.baseUrl.trim() && !!newApiForm.value.accessToken.trim()
})

function handleProviderSelect(provider: string) {
  selectedProvider.value = provider
  // 与服务商添加面板互斥：选中卡片时收起添加面板
  cancelProviderAdd()
  // 重置表单
  newApiForm.value = { baseUrl: '', accessToken: '', userId: '', authTokenMode: 'bearer', displayName: '' }
  newApiVerifyResult.value = null
  newApiError.value = ''
}

// 打开某服务商的添加面板
async function handleProviderAdd(providerId: string) {
  addError.value = ''
  addApiKey.value = ''
  selectedProvider.value = ''
  try {
    const templates = await getProviderTemplates()
    addProvider.value = templates.find(p => p.providerId === providerId) ?? null
  } catch (err) {
    addError.value = extractAutoAddErrorMessage(err)
  }
}

function cancelProviderAdd() {
  addProvider.value = null
  addApiKey.value = ''
  addError.value = ''
}

async function handleProviderAddSubmit() {
  const provider = addProvider.value
  const apiKey = addApiKey.value.trim()
  if (!provider || !apiKey) return
  addSubmitting.value = true
  addError.value = ''
  try {
    // provider 模式：channelKind 取模板默认 route（通常 messages），baseURL/协议由后端按 key 前缀探测判定
    const kind = provider.channelKind || provider.routes?.[0]?.channelKind || 'messages'
    const result = await autoAddChannel(kind, { providerId: provider.providerId, apiKeys: [apiKey] })
    const created = result.channels?.find(c => c.channelKind === kind) ?? result.channels?.[0]
    showSnackbar(t('subscription.addProviderSuccess', { name: created?.name || provider.displayName }), 'success')
    cancelProviderAdd()
  } catch (err) {
    addError.value = extractAutoAddErrorMessage(err)
  } finally {
    addSubmitting.value = false
  }
}

async function handleNewApiSubmit() {
  if (!newApiVerifyResult.value) {
    await handleNewApiVerify()
  } else {
    await handleNewApiProvision()
  }
}

async function handleNewApiVerify() {
  if (!canNewApiVerify.value) return
  newApiVerifying.value = true
  newApiError.value = ''
  try {
    const result = await api.verifyNewApiSubscription({
      baseUrl: newApiForm.value.baseUrl.trim(),
      accessToken: newApiForm.value.accessToken,
      userId: newApiForm.value.userId || undefined,
      authTokenMode: newApiForm.value.authTokenMode || undefined,
      displayName: newApiForm.value.displayName || undefined,
    })
    newApiVerifyResult.value = result
  } catch (e) {
    newApiError.value = e instanceof Error ? e.message : 'Unknown error'
  } finally {
    newApiVerifying.value = false
  }
}

async function handleNewApiProvision() {
  if (!newApiVerifyResult.value) return
  newApiProvisioning.value = true
  newApiError.value = ''
  try {
    const displayName = newApiForm.value.displayName || newApiVerifyResult.value.username || 'new-api'
    const result = await api.provisionNewApiSubscription({
      subscriptionUid: `newapi-${Date.now()}`,
      displayName,
      baseUrl: newApiForm.value.baseUrl.trim(),
      accessToken: newApiForm.value.accessToken,
      userId: newApiForm.value.userId || undefined,
      authTokenMode: newApiForm.value.authTokenMode || undefined,
      channelKind: 'messages',
      provisionAllEligibleGroups: true,
      maxGroupMultiplier: 1.0,
    })
    showSnackbar(t('subscription.newApi.provisionSuccess'), 'success')
    selectedProvider.value = ''
  } catch (e) {
    newApiError.value = e instanceof Error ? e.message : 'Unknown error'
    showSnackbar(newApiError.value, 'error')
  } finally {
    newApiProvisioning.value = false
  }
}

function showSnackbar(message: string, color: string) {
  snackbar.value = { show: true, message, color }
}
</script>

<style scoped>
.subscriptions-view {
  padding: 16px;
}
</style>
