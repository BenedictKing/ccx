<template>
  <v-dialog v-model="dialogVisible" max-width="560" persistent>
    <v-card class="pa-4">
      <v-card-title class="text-h6 mb-4 d-flex align-center">
        <v-icon color="warning" class="mr-2">mdi-server-network</v-icon>
        {{ t('subscription.newApi.connect') }}
      </v-card-title>

      <v-card-text>
        <v-form @submit.prevent="handleSubmit">
          <v-text-field
            v-model="form.baseUrl"
            :label="t('subscription.newApi.baseUrl')"
            placeholder="https://your-newapi-instance.com"
            variant="outlined"
            density="compact"
            class="mb-3"
            required
          />

          <v-text-field
            v-model="form.accessToken"
            :label="t('subscription.newApi.accessToken')"
            variant="outlined"
            density="compact"
            type="password"
            class="mb-3"
            required
          />

          <v-expansion-panels variant="accordion" class="mb-3">
            <v-expansion-panel>
              <v-expansion-panel-title>{{ t('subscription.newApi.advancedOptions') }}</v-expansion-panel-title>
              <v-expansion-panel-text>
                <v-text-field
                  v-model="form.userId"
                  :label="t('subscription.newApi.userId')"
                  variant="outlined"
                  density="compact"
                  class="mb-2"
                />
                <v-select
                  v-model="form.authTokenMode"
                  :label="t('subscription.newApi.authTokenMode')"
                  :items="authTokenModeOptions"
                  variant="outlined"
                  density="compact"
                  class="mb-2"
                />
                <v-text-field
                  v-model="form.displayName"
                  :label="t('subscription.field.name')"
                  variant="outlined"
                  density="compact"
                />
              </v-expansion-panel-text>
            </v-expansion-panel>
          </v-expansion-panels>

          <v-alert
            v-if="verifyResult"
            color="success"
            variant="tonal"
            density="compact"
            class="mb-3"
          >
            <div class="text-body-2">
              <div>{{ t('subscription.newApi.username') }}: {{ verifyResult.username }}</div>
              <div>{{ t('subscription.newApi.quota') }}: {{ verifyResult.quota }}</div>
              <div v-if="verifyResult.availableModels?.length">
                {{ t('subscription.newApi.availableModels') }}: {{ verifyResult.availableModels.length }}
              </div>
            </div>
          </v-alert>

          <v-alert
            v-if="errorMessage"
            color="error"
            variant="tonal"
            density="compact"
            class="mb-3"
          >
            {{ errorMessage }}
          </v-alert>
        </v-form>
      </v-card-text>

      <v-card-actions>
        <v-spacer />
        <v-btn variant="text" @click="closeDialog">{{ t('app.actions.cancel') }}</v-btn>
        <v-btn
          v-if="!verifyResult"
          color="primary"
          :loading="verifying"
          :disabled="!canVerify"
          @click="handleVerify"
        >
          {{ t('subscription.newApi.verify') }}
        </v-btn>
        <v-btn
          v-else
          color="primary"
          :loading="provisioning"
          @click="handleProvision"
        >
          {{ t('subscription.newApi.provision') }}
        </v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from '@/i18n'
import { api } from '@/services/api'
import type { NewApiVerifyResponse, NewApiProvisionResponse } from '@/services/api-types'

const { t } = useI18n()

const emit = defineEmits<{
  created: [result: NewApiProvisionResponse]
  error: [message: string]
}>()

const props = withDefaults(
  defineProps<{
    /** 接入的目标渠道类型 */
    channelKind?: 'messages' | 'chat' | 'responses' | 'gemini' | 'images' | 'vectors'
  }>(),
  {
    channelKind: 'messages',
  }
)

const dialogVisible = ref(false)
const verifying = ref(false)
const provisioning = ref(false)
const verifyResult = ref<NewApiVerifyResponse | null>(null)
const errorMessage = ref('')

const form = ref({
  baseUrl: '',
  accessToken: '',
  userId: '',
  authTokenMode: 'bearer',
  displayName: '',
})

const authTokenModeOptions = computed(() => [
  { title: 'Bearer', value: 'bearer' },
  { title: 'Raw', value: 'raw' },
])

const canVerify = computed(() => {
  return !!form.value.baseUrl.trim() && !!form.value.accessToken.trim()
})

function openDialog() {
  dialogVisible.value = true
  resetForm()
}

function closeDialog() {
  dialogVisible.value = false
  resetForm()
}

function resetForm() {
  form.value = {
    baseUrl: '',
    accessToken: '',
    userId: '',
    authTokenMode: 'bearer',
    displayName: '',
  }
  verifyResult.value = null
  errorMessage.value = ''
}

async function handleSubmit() {
  if (!verifyResult.value) {
    await handleVerify()
  } else {
    await handleProvision()
  }
}

async function handleVerify() {
  if (!canVerify.value) return
  verifying.value = true
  errorMessage.value = ''
  try {
    const result = await api.verifyNewApiSubscription({
      baseUrl: form.value.baseUrl.trim(),
      accessToken: form.value.accessToken,
      userId: form.value.userId || undefined,
      authTokenMode: form.value.authTokenMode || undefined,
      displayName: form.value.displayName || undefined,
    })
    verifyResult.value = result
  } catch (e) {
    errorMessage.value = e instanceof Error ? e.message : 'Unknown error'
  } finally {
    verifying.value = false
  }
}

async function handleProvision() {
  if (!verifyResult.value) return
  provisioning.value = true
  errorMessage.value = ''
  try {
    const displayName = form.value.displayName || verifyResult.value.username || 'new-api'
    const result = await api.provisionNewApiSubscription({
      subscriptionUid: `newapi-${Date.now()}`,
      displayName,
      baseUrl: form.value.baseUrl.trim(),
      accessToken: form.value.accessToken,
      userId: form.value.userId || undefined,
      authTokenMode: form.value.authTokenMode || undefined,
      channelKind: props.channelKind,
      provisionAllEligibleGroups: true,
      maxGroupMultiplier: 1.0,
    })
    emit('created', result)
    closeDialog()
  } catch (e) {
    errorMessage.value = e instanceof Error ? e.message : 'Unknown error'
    emit('error', errorMessage.value)
  } finally {
    provisioning.value = false
  }
}

defineExpose({ openDialog })
</script>
