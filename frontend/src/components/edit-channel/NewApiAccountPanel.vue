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
            <div class="text-caption text-medium-emphasis mb-2">
              {{ channelProxyHint }}
            </div>
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
          <v-tooltip activator="parent" location="top" content-class="ccx-tooltip">{{ t('subscription.newApi.refreshBalance') }}</v-tooltip>
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
              <span class="text-caption text-medium-emphasis">{{ t('subscription.newApi.username') }}</span>
              <strong>{{ subscription.username || '-' }}</strong>
            </div>
            <div class="summary-item">
              <span class="text-caption text-medium-emphasis">{{ t('subscription.newApi.userId') }}</span>
              <strong>{{ subscription.userId || '-' }}</strong>
            </div>
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
            <div class="text-caption text-medium-emphasis mb-2">
              {{ channelProxyHint }}
            </div>
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
            <v-row dense>
              <v-col cols="12" md="6">
                <v-text-field
                  v-model="addForm.userId"
                  :label="t('subscription.newApi.userId')"
                  variant="outlined"
                  density="compact"
                />
              </v-col>
              <v-col cols="12" md="6">
                <v-select
                  v-model="addForm.authTokenMode"
                  :label="t('subscription.newApi.authTokenMode')"
                  :items="authTokenModeOptions"
                  variant="outlined"
                  density="compact"
                />
              </v-col>
            </v-row>
            <div class="text-caption text-medium-emphasis mb-2">
              {{ channelProxyHint }}
            </div>
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
        class="account-item mb-2"
      >
        <div
          class="d-flex align-center justify-space-between pa-3 cursor-pointer"
          :aria-expanded="expandedAccountUid === account.accountUid"
          role="button"
          tabindex="0"
          @click="toggleAccount(account.accountUid)"
          @keydown.enter.prevent="toggleAccount(account.accountUid)"
        >
          <div class="d-flex align-center ga-3 min-width-0">
            <v-icon :color="account.status === 'active' ? 'success' : 'error'">
              {{ account.status === 'active' ? 'mdi-check-circle' : 'mdi-alert-circle' }}
            </v-icon>
            <div class="min-width-0">
              <div class="text-body-2 font-weight-medium">{{ account.displayName || account.accountUid }}</div>
              <div class="text-caption text-medium-emphasis text-truncate">
                {{ t('subscription.newApi.quota') }}: {{ account.balance }}
                <template v-if="account.accessTokenMasked">
                  · {{ account.accessTokenMasked }}
                </template>
                <template v-if="account.provisionedKeys?.length">
                  · {{ t('subscription.newApi.provisionedKeysCount', { count: account.provisionedKeys.length }) }}
                </template>
              </div>
            </div>
          </div>
          <div class="d-flex align-center ga-1" @click.stop>
            <v-btn icon size="small" variant="text" color="primary" :loading="refreshing === account.accountUid" @click.stop="refreshAccount(account.accountUid)">
              <v-icon size="18">mdi-refresh</v-icon>
              <v-tooltip activator="parent" location="top" content-class="ccx-tooltip">{{ t('subscription.newApi.refreshBalance') }}</v-tooltip>
            </v-btn>
            <v-btn icon size="small" variant="text" color="error" :loading="deleting === account.accountUid" @click.stop="deleteAccount(account.accountUid)">
              <v-icon size="18">mdi-delete</v-icon>
              <v-tooltip activator="parent" location="top" content-class="ccx-tooltip">{{ t('app.actions.delete') }}</v-tooltip>
            </v-btn>
            <v-icon size="20" class="ml-1">{{ expandedAccountUid === account.accountUid ? 'mdi-chevron-up' : 'mdi-chevron-down' }}</v-icon>
          </div>
        </div>

        <v-expand-transition>
          <div v-if="expandedAccountUid === account.accountUid" class="account-detail pa-3">
            <div class="account-detail-grid mb-3">
              <div class="summary-item">
                <span class="text-caption text-medium-emphasis">{{ t('subscription.newApi.userId') }}</span>
                <strong class="text-body-2">{{ account.userId || '-' }}</strong>
              </div>
              <div class="summary-item">
                <span class="text-caption text-medium-emphasis">{{ t('subscription.newApi.status') }}</span>
                <strong class="text-body-2">{{ account.status || '-' }}</strong>
              </div>
              <div class="summary-item">
                <span class="text-caption text-medium-emphasis">{{ t('subscription.newApi.quota') }}</span>
                <strong class="text-body-2">{{ formatQuota(account.balance) }}</strong>
              </div>
              <div class="summary-item">
                <span class="text-caption text-medium-emphasis">{{ t('subscription.newApi.lastCheckedAt') }}</span>
                <strong class="text-body-2">{{ account.lastCheckedAt ? formatTime(account.lastCheckedAt) : '-' }}</strong>
              </div>
              <div class="summary-item">
                <span class="text-caption text-medium-emphasis">{{ t('subscription.newApi.createdAt') }}</span>
                <strong class="text-body-2">{{ formatTime(account.createdAt) }}</strong>
              </div>
              <div class="summary-item">
                <span class="text-caption text-medium-emphasis">{{ t('subscription.newApi.authTokenMode') }}</span>
                <strong class="text-body-2">{{ authTokenModeLabel(account.authTokenMode) }}</strong>
              </div>
              <div class="summary-item summary-item--wide">
                <span class="text-caption text-medium-emphasis">{{ t('subscription.newApi.accessToken') }}</span>
                <code class="text-caption">{{ account.accessTokenMasked || '-' }}</code>
              </div>
            </div>

            <div v-if="account.provisionedKeys?.length" class="mb-3">
              <div class="text-caption text-medium-emphasis mb-1">{{ t('subscription.newApi.provisionedKeys') }}</div>
              <div class="d-flex flex-wrap ga-1">
                <v-chip
                  v-for="key in account.provisionedKeys"
                  :key="key.tokenId"
                  size="x-small"
                  color="primary"
                  variant="tonal"
                >
                  {{ key.name }} · {{ key.group }} × {{ key.groupMultiplier }}
                </v-chip>
              </div>
            </div>

            <v-alert v-if="account.lastSyncError" color="warning" variant="tonal" density="compact" class="mb-3">
              {{ account.lastSyncError }}
            </v-alert>

            <v-divider class="mb-3" />

            <v-form @submit.prevent="saveAccountCredentials(account)">
              <div class="text-caption text-medium-emphasis mb-2">{{ t('subscription.newApi.updateCredentials') }}</div>
              <v-text-field
                v-model="accountForm(account.accountUid).accessToken"
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
                    v-model="accountForm(account.accountUid).userId"
                    :label="t('subscription.newApi.userId')"
                    variant="outlined"
                    density="compact"
                  />
                </v-col>
                <v-col cols="12" md="6">
                  <v-select
                    v-model="accountForm(account.accountUid).authTokenMode"
                    :label="t('subscription.newApi.authTokenMode')"
                    :items="authTokenModeOptions"
                    variant="outlined"
                    density="compact"
                  />
                </v-col>
              </v-row>
              <v-alert v-if="accountErrors[account.accountUid]" color="error" variant="tonal" density="compact" class="mb-3">
                {{ accountErrors[account.accountUid] }}
              </v-alert>
              <div class="d-flex justify-end">
                <v-btn
                  color="primary"
                  variant="tonal"
                  :loading="savingAccount === account.accountUid"
                  :disabled="!accountCredentialsChanged(account)"
                  @click="saveAccountCredentials(account)"
                >
                  <v-icon start size="small">mdi-check</v-icon>
                  {{ t('subscription.newApi.saveCredentials') }}
                </v-btn>
              </div>
            </v-form>
          </div>
        </v-expand-transition>
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
import type { NewApiAccountItem, NewApiVerifyResponse, SubscriptionItem } from '@/services/api-types'
import { DEFAULT_NEWAPI_MAX_GROUP_MULTIPLIER, eligibleNewApiGroups } from '@/utils/newApiGroups'

const { t } = useI18n()
const props = defineProps<{
  subscriptionUid: string
  channelName?: string
  baseUrl?: string
  channelUid?: string
  channelKind?: string
  isGeneric?: boolean
  autoManagedKind?: string
  /** 渠道"代理通道"设置：绑定/校验/同步统一复用，本面板不再单独配置代理 */
  channelProxyUrl?: string
  channelProxyPreferDirect?: boolean
}>()
const emit = defineEmits<{ updated: [] }>()

const subscription = ref<SubscriptionItem | null>(null)
// 绑定成功后本地回填 subscriptionUid：generic 渠道的 props.subscriptionUid 来自 channel.subscriptionUid，
// 需后端回填并重新拉取才非空；绑定后先用 provision 响应里的 uid 让面板立即切到多账号视图并拉取账号。
const localSubscriptionUid = ref('')
const effectiveSubscriptionUid = computed(() => props.subscriptionUid || localSubscriptionUid.value)
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
// 子账号展开详情 + 每账号独立的凭据表单/保存状态
const expandedAccountUid = ref('')
const accountForms = ref<Record<string, { accessToken: string; userId: string; authTokenMode: string }>>({})
const accountErrors = ref<Record<string, string>>({})
const savingAccount = ref('')

const bindForm = ref({ accessToken: '', userId: '', authTokenMode: 'bearer' })
const primaryForm = ref({ accessToken: '', userId: '', authTokenMode: 'bearer' })
const addForm = ref({ accessToken: '', userId: '', authTokenMode: 'bearer' })
const effectiveChannelProxyUrl = computed(() => props.channelProxyUrl?.trim() || '')
// 渠道代理通道是唯一代理设置：绑定/校验/同步均复用，未配置时直连
const channelProxy = computed(() =>
  effectiveChannelProxyUrl.value
    ? { proxyUrl: effectiveChannelProxyUrl.value, proxyPreferDirect: props.channelProxyPreferDirect || false }
    : undefined,
)
const channelProxyHint = computed(() => {
  const key = effectiveChannelProxyUrl.value
    ? 'subscription.newApi.proxyInheritedHint'
    : 'subscription.newApi.proxyDirectHint'
  const base = t(key)
  return effectiveChannelProxyUrl.value ? `${base}（${effectiveChannelProxyUrl.value}）` : base
})
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

function requireEligibleGroups(result: NewApiVerifyResponse) {
  if (result.groupFetchError) {
    throw new Error(`无法获取上游分组: ${result.groupFetchError}`)
  }
  const groups = eligibleNewApiGroups(result.groups, DEFAULT_NEWAPI_MAX_GROUP_MULTIPLIER)
  if (groups.length === 0) {
    throw new Error(`没有倍率不高于 ${DEFAULT_NEWAPI_MAX_GROUP_MULTIPLIER} 的可用分组`)
  }
  return groups
}

async function bindNewApi() {
  if (!canBindNewApi.value) return
  binding.value = true
  bindError.value = ''
  try {
    const baseUrl = props.baseUrl!.trim()
    const accessToken = bindForm.value.accessToken.trim()
    const userId = bindForm.value.userId.trim() || undefined
    const authTokenMode = bindForm.value.authTokenMode
    // 代理沿用渠道"代理通道"设置，不在绑定表单单独配置
    const proxyUrl = channelProxy.value?.proxyUrl || undefined
    const proxyPreferDirect = channelProxy.value?.proxyPreferDirect || undefined
    const verified = await api.verifyNewApiSubscription({
      baseUrl,
      accessToken,
      userId,
      authTokenMode,
      displayName: props.channelName!.trim(),
      subscriptionUid: `newapi-${props.channelUid}`,
      proxyUrl,
      proxyPreferDirect,
    })
    requireEligibleGroups(verified)
    const response = await api.provisionNewApiSubscription({
      subscriptionUid: `newapi-${props.channelUid}`,
      displayName: props.channelName!.trim(),
      baseUrl,
      accessToken,
      userId: String(verified.userId),
      authTokenMode,
      channelKind: props.channelKind!,
      channelName: props.channelName!.trim(),
      provisionAllEligibleGroups: true,
      maxGroupMultiplier: DEFAULT_NEWAPI_MAX_GROUP_MULTIPLIER,
      provisionModels: verified.availableModels,
      proxyUrl,
      proxyPreferDirect,
    })
    subscription.value = response.subscription
    syncPrimaryForm(response.subscription)
    localSubscriptionUid.value = response.subscription.subscriptionUid
    bindForm.value = { accessToken: '', userId: '', authTokenMode: 'bearer' }
    await fetchAccounts()
    emit('updated')
  } catch (e) {
    bindError.value = e instanceof Error ? e.message : 'Unknown error'
  } finally {
    binding.value = false
  }
}

async function fetchPrimaryAccount() {
  if (!effectiveSubscriptionUid.value) return
  loadingPrimary.value = true
  primaryError.value = ''
  try {
    const item = await api.getSubscription(effectiveSubscriptionUid.value)
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
    // 代理设置不在凭证表单维护：渠道"代理通道"是唯一事实源，管理面已自动跟随
    const item = await api.updateNewApiCredentials(effectiveSubscriptionUid.value, payload)
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
  if (!effectiveSubscriptionUid.value) return
  refreshingPrimary.value = true
  primaryError.value = ''
  try {
    const response = await api.refreshSubscription(effectiveSubscriptionUid.value)
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
  if (!effectiveSubscriptionUid.value) return
  loading.value = true
  try {
    const resp = await api.getSubscriptionAccounts(effectiveSubscriptionUid.value)
    accounts.value = resp.accounts || []
  } catch (e) {
    console.error('Failed to fetch accounts:', e)
  } finally {
    loading.value = false
  }
}

async function handleAddAccount() {
  if (!addForm.value.accessToken.trim() || !subscription.value) return
  adding.value = true
  addError.value = ''
  try {
    const accessToken = addForm.value.accessToken.trim()
    const userId = addForm.value.userId || undefined
    const authTokenMode = addForm.value.authTokenMode || undefined
    // 代理沿用渠道"代理通道"设置（未配置时直连），账号级不再单独覆盖
    const proxyUrl = channelProxy.value?.proxyUrl || undefined
    const proxyPreferDirect = channelProxy.value?.proxyPreferDirect || undefined
    const verified = await api.verifyNewApiSubscription({
      baseUrl: subscription.value.baseUrl || props.baseUrl || '',
      accessToken,
      userId,
      authTokenMode,
      subscriptionUid: effectiveSubscriptionUid.value,
      proxyUrl,
      proxyPreferDirect,
    })
    requireEligibleGroups(verified)
    await api.addSubscriptionAccount(effectiveSubscriptionUid.value, {
      accessToken,
      userId: String(verified.userId),
      // 名称不可自定义：固定采用站点用户名
      displayName: verified.username || undefined,
      authTokenMode,
      provisionAllEligibleGroups: true,
      maxGroupMultiplier: DEFAULT_NEWAPI_MAX_GROUP_MULTIPLIER,
      provisionModels: verified.availableModels,
    })
    addForm.value = { accessToken: '', userId: '', authTokenMode: 'bearer' }
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
    await api.refreshSubscriptionAccount(effectiveSubscriptionUid.value, accountUid)
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
    await api.deleteSubscriptionAccount(effectiveSubscriptionUid.value, accountUid)
    if (expandedAccountUid.value === accountUid) expandedAccountUid.value = ''
    delete accountForms.value[accountUid]
    await fetchAccounts()
    emit('updated')
  } catch (e) {
    console.error('Failed to delete account:', e)
  } finally {
    deleting.value = ''
  }
}

function toggleAccount(accountUid: string) {
  if (expandedAccountUid.value === accountUid) {
    expandedAccountUid.value = ''
    return
  }
  expandedAccountUid.value = accountUid
  accountErrors.value[accountUid] = ''
  // 展开时以账号当前值初始化表单基线（accessToken 永远留空=保持不变）
  const account = accounts.value.find(a => a.accountUid === accountUid)
  accountForms.value[accountUid] = {
    accessToken: '',
    userId: account?.userId || '',
    authTokenMode: normalizeAuthTokenMode(account?.authTokenMode),
  }
}

function accountForm(accountUid: string) {
  if (!accountForms.value[accountUid]) {
    const account = accounts.value.find(a => a.accountUid === accountUid)
    accountForms.value[accountUid] = {
      accessToken: '',
      userId: account?.userId || '',
      authTokenMode: normalizeAuthTokenMode(account?.authTokenMode),
    }
  }
  return accountForms.value[accountUid]
}

function accountCredentialsChanged(account: NewApiAccountItem) {
  const form = accountForms.value[account.accountUid]
  if (!form) return false
  return Boolean(form.accessToken.trim()) ||
    form.userId.trim() !== (account.userId || '') ||
    form.authTokenMode !== normalizeAuthTokenMode(account.authTokenMode)
}

function authTokenModeLabel(mode?: string) {
  const normalized = normalizeAuthTokenMode(mode)
  return normalized === 'raw' ? 'Raw' : 'Bearer'
}

async function saveAccountCredentials(account: NewApiAccountItem) {
  if (!accountCredentialsChanged(account)) return
  const form = accountForms.value[account.accountUid]
  savingAccount.value = account.accountUid
  accountErrors.value[account.accountUid] = ''
  try {
    const payload: { accessToken?: string; userId?: string; authTokenMode?: string } = {
      userId: form.userId.trim(),
      authTokenMode: form.authTokenMode,
    }
    if (form.accessToken.trim()) payload.accessToken = form.accessToken.trim()
    const updated = await api.updateSubscriptionAccountCredentials(
      effectiveSubscriptionUid.value,
      account.accountUid,
      payload,
    )
    const index = accounts.value.findIndex(a => a.accountUid === account.accountUid)
    if (index >= 0) accounts.value[index] = updated
    form.accessToken = ''
    form.userId = updated.userId || ''
    form.authTokenMode = normalizeAuthTokenMode(updated.authTokenMode)
    emit('updated')
  } catch (e) {
    accountErrors.value[account.accountUid] = e instanceof Error ? e.message : 'Unknown error'
  } finally {
    savingAccount.value = ''
  }
}

watch(
  () => props.subscriptionUid,
  () => {
    subscription.value = null
    accounts.value = []
    localSubscriptionUid.value = ''
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
  overflow: hidden;
}
.cursor-pointer {
  cursor: pointer;
}
.min-width-0 {
  min-width: 0;
}
.account-detail {
  border-top: 1px dashed rgba(var(--v-theme-outline), 0.35);
  background-color: rgba(var(--v-theme-surface), 0.4);
}
.account-detail-grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 12px;
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
