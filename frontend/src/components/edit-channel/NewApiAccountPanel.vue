<template>
  <div class="newapi-account-panel">
    <div class="text-subtitle-1 font-weight-bold mb-3 d-flex align-center">
      <v-icon color="warning" class="mr-2">mdi-account-multiple</v-icon>
      {{ t('subscription.newApi.accountManagement') }}
    </div>

    <!-- 添加新账号 -->
    <v-expansion-panels variant="accordion" class="mb-4">
      <v-expansion-panel>
        <v-expansion-panel-title>
          <v-icon start size="small" class="mr-2">mdi-plus</v-icon>
          {{ t('subscription.newApi.addAccount') }}
        </v-expansion-panel-title>
        <v-expansion-panel-text>
          <v-form @submit.prevent="handleAddAccount">
            <v-text-field
              v-model="addForm.accessToken"
              :label="t('subscription.newApi.accessToken')"
              variant="outlined"
              density="compact"
              type="password"
              class="mb-2"
              required
            />
            <v-text-field
              v-model="addForm.userId"
              :label="t('subscription.newApi.userId')"
              variant="outlined"
              density="compact"
              class="mb-2"
            />
            <v-text-field
              v-model="addForm.displayName"
              :label="t('subscription.field.name')"
              variant="outlined"
              density="compact"
              class="mb-2"
            />
            <v-select
              v-model="addForm.authTokenMode"
              :label="t('subscription.newApi.authTokenMode')"
              :items="authTokenModeOptions"
              variant="outlined"
              density="compact"
              class="mb-2"
            />
            <v-alert
              v-if="addError"
              color="error"
              variant="tonal"
              density="compact"
              class="mb-2"
            >
              {{ addError }}
            </v-alert>
            <v-btn
              color="primary"
              :loading="adding"
              :disabled="!addForm.accessToken.trim()"
              @click="handleAddAccount"
            >
              {{ t('app.actions.add') }}
            </v-btn>
          </v-form>
        </v-expansion-panel-text>
      </v-expansion-panel>
    </v-expansion-panels>

    <!-- 账号列表 -->
    <div v-if="accounts.length > 0" class="account-list">
      <div
        v-for="account in accounts"
        :key="account.accountUid"
        class="account-item d-flex align-center justify-space-between pa-3 mb-2 rounded-lg"
      >
        <div class="d-flex align-center ga-3">
          <v-icon :color="account.status === 'active' ? 'success' : 'error'">
            {{ account.status === 'active' ? 'mdi-check-circle' : 'mdi-alert-circle' }}
          </v-icon>
          <div>
            <div class="text-body-2 font-weight-medium">
              {{ account.displayName || account.accountUid }}
            </div>
            <div class="text-caption text-medium-emphasis">
              {{ t('subscription.newApi.quota') }}: {{ account.balance }}
              <template v-if="account.accessTokenMasked">
                · {{ t('subscription.newApi.accessToken') }}: {{ account.accessTokenMasked }}
              </template>
            </div>
          </div>
        </div>
        <div class="d-flex ga-2">
          <v-btn
            icon
            size="small"
            variant="text"
            color="primary"
            :loading="refreshing === account.accountUid"
            @click="refreshAccount(account.accountUid)"
          >
            <v-icon size="18">mdi-refresh</v-icon>
          </v-btn>
          <v-btn
            icon
            size="small"
            variant="text"
            color="error"
            :loading="deleting === account.accountUid"
            @click="deleteAccount(account.accountUid)"
          >
            <v-icon size="18">mdi-delete</v-icon>
          </v-btn>
        </div>
      </div>
    </div>
    <v-alert
      v-else
      color="info"
      variant="tonal"
      density="compact"
    >
      {{ t('subscription.newApi.noAccounts') }}
    </v-alert>
  </div>
</template>

<script setup lang="ts">
import { ref, computed } from 'vue'
import { useI18n } from '@/i18n'
import { api } from '@/services/api'
import type { NewApiAccountItem } from '@/services/api-types'

const { t } = useI18n()

const props = defineProps<{
  subscriptionUid: string
}>()

const emit = defineEmits<{
  updated: []
}>()

const accounts = ref<NewApiAccountItem[]>([])
const loading = ref(false)
const adding = ref(false)
const refreshing = ref('')
const deleting = ref('')
const addError = ref('')

const addForm = ref({
  accessToken: '',
  userId: '',
  displayName: '',
  authTokenMode: 'bearer',
})

const authTokenModeOptions = computed(() => [
  { title: 'Bearer', value: 'bearer' },
  { title: 'Raw', value: 'raw' },
])

async function fetchAccounts() {
  if (!props.subscriptionUid) return
  loading.value = true
  try {
    const resp = await api.getSubscriptionAccounts(props.subscriptionUid)
    accounts.value = resp.accounts || []
  } catch (e) {
    console.error('Failed to fetch accounts:', e)
  } finally {
    loading.value = false
  }
}

async function handleAddAccount() {
  if (!addForm.value.accessToken.trim()) return
  adding.value = true
  addError.value = ''
  try {
    await api.addSubscriptionAccount(props.subscriptionUid, {
      accessToken: addForm.value.accessToken.trim(),
      userId: addForm.value.userId || undefined,
      displayName: addForm.value.displayName || undefined,
      authTokenMode: addForm.value.authTokenMode || undefined,
    })
    addForm.value = { accessToken: '', userId: '', displayName: '', authTokenMode: 'bearer' }
    await fetchAccounts()
    emit('updated')
  } catch (e) {
    addError.value = e instanceof Error ? e.message : 'Unknown error'
  } finally {
    adding.value = false
  }
}

async function refreshAccount(accountUid: string) {
  refreshing.value = accountUid
  try {
    await api.refreshSubscriptionAccount(props.subscriptionUid, accountUid)
    await fetchAccounts()
  } catch (e) {
    console.error('Failed to refresh account:', e)
  } finally {
    refreshing.value = ''
  }
}

async function deleteAccount(accountUid: string) {
  deleting.value = accountUid
  try {
    await api.deleteSubscriptionAccount(props.subscriptionUid, accountUid)
    await fetchAccounts()
    emit('updated')
  } catch (e) {
    console.error('Failed to delete account:', e)
  } finally {
    deleting.value = ''
  }
}

fetchAccounts()
</script>

<style scoped>
.newapi-account-panel {
  padding: 16px;
}
.account-item {
  background-color: rgba(var(--v-theme-surface-variant), 0.5);
  border: 1px solid rgba(var(--v-theme-outline), 0.2);
}
</style>
