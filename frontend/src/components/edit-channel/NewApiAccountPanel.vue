<template>
  <div class="newapi-account-panel">
    <div class="text-subtitle-1 font-weight-bold mb-3 d-flex align-center">
      <v-icon color="warning" class="mr-2">mdi-account-multiple</v-icon>
      {{ t('subscription.newApi.accountManagement') }}
    </div>

    <template v-if="isGeneric && !subscription">
      <v-alert color="info" variant="tonal" density="compact" class="mb-4">
        {{ t('subscription.newApi.genericAutoManagedHint') }}
      </v-alert>

      <v-card variant="outlined" rounded="lg" class="mb-4">
        <v-card-text>
          <v-form @submit.prevent="bindNewApi">
            <v-text-field
              v-model="bindForm.accessToken"
              :label="t('subscription.newApi.accessToken')"
              variant="outlined"
              density="compact"
              type="password"
              autocomplete="new-password"
              required
              class="mb-2"
            />
            <v-row dense>
              <v-col cols="12" md="6">
                <v-text-field
                  v-model="bindForm.userId"
                  :label="t('subscription.newApi.userId')"
                  variant="outlined"
                  density="compact"
                />
              </v-col>
              <v-col cols="12" md="6">
                <v-select
                  v-model="bindForm.authTokenMode"
                  :label="t('subscription.newApi.authTokenMode')"
                  :items="authTokenModeOptions"
                  variant="outlined"
                  density="compact"
                />
              </v-col>
            </v-row>
            <v-alert v-if="bindError" color="error" variant="tonal" density="compact" class="mb-3">
              {{ bindError }}
            </v-alert>
            <div class="d-flex justify-end">
              <v-btn
                color="primary"
                :loading="binding"
                :disabled="!canBindNewApi"
                @click="bindNewApi"
              >
                <v-icon start size="small">mdi-check</v-icon>
                {{ t('subscription.newApi.bindAccount') }}
              </v-btn>
            </div>
          </v-form>
        </v-card-text>
      </v-card>
    </template>

    <template v-else>
    <v-card variant="outlined" rounded="lg" class="mb-4">
      <v-card-title class="d-flex align-center justify-space-between ga-3 pa-4 pb-2">
        <div class="d-flex align-center ga-2">
          <v-icon color="primary">mdi-account-multiple-outline</v-icon>
          <span class="text-subtitle-1 font-weight-bold">{{ t('subscription.newApi.primaryAccount') }}</span>
          <v-chip v-if="subscription?.accessTokenMasked" size="x-small" color="success" variant="tonal">
            {{ subscription.accessTokenMasked }}
          </v-chip>
        </div>
        <v-btn
          icon
          size="small"
          variant="text"
          color="primary"
          :loading="refreshingPrimary"
          :aria-label="t('subscription.newApi.refreshBalance')"
          @click="refreshPrimaryAccount"
        >
          <v-icon size="18">mdi-refresh</v-icon>
          <v-tooltip activator="parent" location="top">{{ t('subscription.newApi.refreshBalance') }}</v-tooltip>
        </v-btn>
      </v-card-title>
      <v-card-text class="pt-2">
        <v-progress-linear v-if="loadingPrimary" indeterminate color="primary" class="mb-3" />
        <v-alert v-if="primaryError" color="error" variant="tonal" density="compact" class="mb-3">
          {{ primaryError }}
        </v-alert>

        <template v-if="subscription">
          <div class="primary-summary mb-4">
            <div class="summary-item">
              <span class="text-caption text-medium-emphasis">{{ t('subscription.newApi.quota') }}</span>
              <strong>{{ formatQuota(subscription.balance) }}</strong>
            </div>
            <div class="summary-item">
              <span class="text-caption text-medium-emphasis">{{ t('subscription.newApi.usedQuota') }}</span>
              <strong>{{ formatQuota(subscription.usedQuota) }}</strong>
            </div>
            <div class="summary-item summary-item--wide">
              <span class="text-caption text-medium-emphasis">{{ t('subscription.newApi.baseUrl') }}</span>
              <code class="text-caption">{{ subscription.baseUrl || '-' }}</code>
            </div>
          </div>

          <div v-if="subscription.lastBalanceRefreshAt" class="text-caption text-medium-emphasis mb-3">
            {{ t('subscription.newApi.lastRefreshedAt') }}: {{ formatTime(subscription.lastBalanceRefreshAt) }}
          </div>
          <v-alert
            v-if="subscription.lastBalanceRefreshError"
            color="warning"
            variant="tonal"
            density="compact"
            class="mb-3"
          >
            {{ subscription.lastBalanceRefreshError }}
          </v-alert>

          <v-form @submit.prevent="savePrimaryCredentials">
            <v-text-field
              v-model="primaryForm.accessToken"
              :label="t('subscription.newApi.accessToken')"
              :placeholder="t('subscription.newApi.accessTokenKeepPlaceholder')"
              variant="outlined"
              density="compact"
              type="password"
              autocomplete="new-password"
              class="mb-2"
            />
            <v-row dense>
              <v-col cols="12" md="6">
                <v-text-field
                  v-model="primaryForm.userId"
                  :label="t('subscription.newApi.userId')"
                  variant="outlined"
                  density="compact"
                />
              </v-col>
              <v-col cols="12" md="6">
                <v-select
                  v-model="primaryForm.authTokenMode"
                  :label="t('subscription.newApi.authTokenMode')"
                  :items="authTokenModeOptions"
                  variant="outlined"
                  density="compact"
                />
              </v-col>
            </v-row>
            <div class="d-flex justify-end">
              <v-btn
                color="primary"
                variant="tonal"
                :loading="savingPrimary"
                :disabled="!primaryCredentialsChanged"
                @click="savePrimaryCredentials"
              >
                <v-icon start size="small">mdi-check</v-icon>
                {{ t('subscription.newApi.saveCredentials') }}
              </v-btn>
            </div>
          </v-form>
        </template>
      </v-card-text>
    </v-card>

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
            <v-alert v-if="addError" color="error" variant="tonal" density="compact" class="mb-2">
              {{ addError }}
            </v-alert>
            <v-btn color="primary" :loading="adding" :disabled="!addForm.accessToken.trim()" @click="handleAddAccount">
              {{ t('app.actions.add') }}
            </v-btn>
          </v-form>
        </v-expansion-panel-text>
      </v-expansion-panel>
    </v-expansion-panels>

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
            <div class="text-body-2 font-weight-medium">{{ account.displayName || account.accountUid }}</div>
            <div class="text-caption text-medium-emphasis">
              {{ t('subscription.newApi.quota') }}: {{ account.balance }}
              <template v-if="account.accessTokenMasked">
                · {{ t('subscription.newApi.accessToken') }}: {{ account.accessTokenMasked }}
              </template>
            </div>
          </div>
        </div>
        <div class="d-flex ga-2">
          <v-btn icon size="small" variant="text" color="primary" :loading="refreshing === account.accountUid" @click="refreshAccount(account.accountUid)">
            <v-icon size="18">mdi-refresh</v-icon>
          </v-btn>
          <v-btn icon size="small" variant="text" color="error" :loading="deleting === account.accountUid" @click="deleteAccount(account.accountUid)">
            <v-icon size="18">mdi-delete</v-icon>
          </v-btn>
        </div>
      </div>
    </div>
    <v-alert v-else color="info" variant="tonal" density="compact">
      {{ t('subscription.newApi.noAccounts') }}
    </v-alert>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from '@/i18n'
import { api } from '@/services/api'
import type { NewApiAccountItem, SubscriptionItem } from '@/services/api-types'

const { t } = useI18n()
const props = defineProps<{
  subscriptionUid: string
  channelName?: string
  baseUrl?: string
  channelUid?: string
  channelKind?: string
  isGeneric?: boolean
  autoManagedKind?: string
}>()
const emit = defineEmits<{ updated: [] }>()

const subscription = ref<SubscriptionItem | null>(null)
const accounts = ref<NewApiAccountItem[]>([])
const loadingPrimary = ref(false)
const refreshingPrimary = ref(false)
const savingPrimary = ref(false)
const primaryError = ref('')
const loading = ref(false)
const binding = ref(false)
const adding = ref(false)
const refreshing = ref('')
const deleting = ref('')
const addError = ref('')
const bindError = ref('')

const bindForm = ref({ accessToken: '', userId: '', authTokenMode: 'bearer' })
const primaryForm = ref({ accessToken: '', userId: '', authTokenMode: 'bearer' })
const addForm = ref({ accessToken: '', userId: '', displayName: '', authTokenMode: 'bearer' })
const authTokenModeOptions = computed(() => [
  { title: 'Bearer', value: 'bearer' },
  { title: 'Raw', value: 'raw' },
])
const canBindNewApi = computed(() => Boolean(
  props.isGeneric &&
  props.channelName?.trim() &&
  props.baseUrl?.trim() &&
  props.channelUid?.trim() &&
  props.channelKind?.trim() &&
  bindForm.value.accessToken.trim(),
))

const primaryCredentialsChanged = computed(() => {
  if (!subscription.value) return false
  return Boolean(primaryForm.value.accessToken.trim()) ||
    primaryForm.value.userId.trim() !== (subscription.value.userId || '') ||
    primaryForm.value.authTokenMode !== normalizeAuthTokenMode(subscription.value.authTokenMode)
})

function normalizeAuthTokenMode(mode?: string) {
  return mode === 'raw_auth' ? 'raw' : mode || 'bearer'
}

function syncPrimaryForm(item: SubscriptionItem) {
  primaryForm.value = {
    accessToken: '',
    userId: item.userId || '',
    authTokenMode: normalizeAuthTokenMode(item.authTokenMode),
  }
}

function formatQuota(value?: number) {
  return new Intl.NumberFormat().format(value ?? 0)
}

function formatTime(value: string) {
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

async function bindNewApi() {
  if (!canBindNewApi.value) return
  binding.value = true
  bindError.value = ''
  try {
    const response = await api.provisionNewApiSubscription({
      subscriptionUid: `newapi-${props.channelUid}`,
      displayName: props.channelName!.trim(),
      baseUrl: props.baseUrl!.trim(),
      accessToken: bindForm.value.accessToken.trim(),
      userId: bindForm.value.userId.trim() || undefined,
      authTokenMode: bindForm.value.authTokenMode,
      channelKind: props.channelKind!,
      channelName: props.channelName!.trim(),
    })
    subscription.value = response.subscription
    syncPrimaryForm(response.subscription)
    bindForm.value = { accessToken: '', userId: '', authTokenMode: 'bearer' }
    emit('updated')
  } catch (e) {
    bindError.value = e instanceof Error ? e.message : 'Unknown error'
  } finally {
    binding.value = false
  }
}

async function fetchPrimaryAccount() {
  if (!props.subscriptionUid) return
  loadingPrimary.value = true
  primaryError.value = ''
  try {
    const item = await api.getSubscription(props.subscriptionUid)
    subscription.value = item
    syncPrimaryForm(item)
  } catch (e) {
    primaryError.value = e instanceof Error ? e.message : 'Unknown error'
  } finally {
    loadingPrimary.value = false
  }
}

async function savePrimaryCredentials() {
  if (!subscription.value || !primaryCredentialsChanged.value) return
  savingPrimary.value = true
  primaryError.value = ''
  try {
    const payload: { accessToken?: string; userId?: string; authTokenMode?: string; expectedVersion?: number } = {
      userId: primaryForm.value.userId.trim(),
      authTokenMode: primaryForm.value.authTokenMode,
      expectedVersion: subscription.value.version,
    }
    if (primaryForm.value.accessToken.trim()) payload.accessToken = primaryForm.value.accessToken.trim()
    const item = await api.updateNewApiCredentials(props.subscriptionUid, payload)
    subscription.value = item
    syncPrimaryForm(item)
    emit('updated')
  } catch (e) {
    primaryError.value = e instanceof Error ? e.message : 'Unknown error'
  } finally {
    savingPrimary.value = false
  }
}

async function refreshPrimaryAccount() {
  if (!props.subscriptionUid) return
  refreshingPrimary.value = true
  primaryError.value = ''
  try {
    const response = await api.refreshSubscription(props.subscriptionUid)
    subscription.value = response.subscription
    syncPrimaryForm(response.subscription)
    emit('updated')
  } catch (e) {
    primaryError.value = e instanceof Error ? e.message : 'Unknown error'
  } finally {
    refreshingPrimary.value = false
  }
}

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

watch(
  () => props.subscriptionUid,
  () => {
    subscription.value = null
    accounts.value = []
    void Promise.all([fetchPrimaryAccount(), fetchAccounts()])
  },
  { immediate: true },
)
</script>

<style scoped>
.newapi-account-panel {
  padding: 16px;
}
.primary-summary {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px;
}
.summary-item {
  display: flex;
  min-width: 0;
  flex-direction: column;
  gap: 2px;
}
.summary-item--wide {
  grid-column: 1 / -1;
}
.summary-item code {
  overflow-wrap: anywhere;
}
.account-item {
  background-color: rgba(var(--v-theme-surface-variant), 0.5);
  border: 1px solid rgba(var(--v-theme-outline), 0.2);
}
@media (max-width: 600px) {
  .primary-summary {
    grid-template-columns: minmax(0, 1fr);
  }
  .summary-item--wide {
    grid-column: auto;
  }
}
</style>
