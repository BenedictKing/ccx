<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import {
  AlertCircle,
  CheckCircle2,
  ChevronDown,
  ChevronUp,
  Link2,
  Loader2,
  Plus,
  RefreshCw,
  Trash2,
  Users,
  X,
} from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { AdminApiError, useAdminApi } from '@/composables/useAdminApi'
import { useLanguage } from '@/composables/useLanguage'
import {
  NEWAPI_PROVISION_PATH,
  NEWAPI_VERIFY_PATH,
  subscriptionPath,
  subscriptionPrimaryAccountPath,
  subscriptionRefreshPath,
} from '@/services/admin-api'
import type {
  ChannelKind,
  NewApiAccountItem,
  NewApiProvisionResponse,
  NewApiVerifyResponse,
  SubscriptionItem,
} from '@/services/admin-api'
import { eligibleNewApiGroups } from '@/utils/subscription-management'

// 兼容两种挂载入口：
// 1) 订阅管理列表（SubscriptionTab）：传 subscription 对象；
// 2) 渠道编辑器 accounts 区（ChannelEditDialog）：传 subscriptionUid/channelUid/baseUrl 等，
//    new_api 托管渠道按约定订阅 UID = newapi-{channelUid}（effectiveSubscriptionUid 回退链）。
const props = defineProps<{
  subscription?: SubscriptionItem | null
  subscriptionUid?: string
  isGeneric?: boolean
  autoManagedKind?: string
  channelUid?: string
  channelName?: string
  channelKind?: string
  baseUrl?: string
  /** 渠道"代理通道"设置：绑定/校验/同步统一复用，本面板不再单独配置代理 */
  channelProxyUrl?: string
  channelProxyPreferDirect?: boolean
}>()
const emit = defineEmits<{ updated: [] }>()
const { t } = useLanguage()
const adminApi = useAdminApi()

const MAX_GROUP_MULTIPLIER = 1

const primary = ref<SubscriptionItem | null>(null)
// 绑定成功后本地回填 subscriptionUid：generic 渠道的 props.subscriptionUid 来自 channel.subscriptionUid，
// 需后端回填并重新拉取才非空；绑定后先用 provision 响应里的 uid 让面板立即切到多账号视图并拉取账号。
const localSubscriptionUid = ref('')
const accounts = ref<NewApiAccountItem[]>([])
const loadingPrimary = ref(false)
const refreshingPrimary = ref(false)
const deletingPrimary = ref(false)
const pendingDeletePrimary = ref(false)
const primaryError = ref('')
const loading = ref(false)
const adding = ref(false)
const refreshing = ref('')
const deleting = ref('')
const binding = ref(false)
const error = ref('')
const addError = ref('')
const bindError = ref('')
const showAddForm = ref(false)
const expandedPrimary = ref(false)
const expandedAccountUid = ref('')
const pendingDeleteAccountUid = ref('')

const addForm = ref({ accessToken: '', userId: '', authTokenMode: 'bearer' })
const bindForm = ref({ accessToken: '', userId: '', authTokenMode: 'bearer' })

// new_api 托管渠道按约定订阅 UID=newapi-{channelUid}；key 配置的 sourceSubscriptionUid 丢失时
// （如编辑换 key 切断关联），仍能凭 channelUid 直连订阅，面板不瘫痪。
const isManagedNewApiChannel = computed(() =>
  !props.isGeneric && props.autoManagedKind === 'new_api' && !!props.channelUid?.trim(),
)
const effectiveSubscriptionUid = computed(() =>
  props.subscriptionUid ||
  props.subscription?.subscriptionUid ||
  localSubscriptionUid.value ||
  (isManagedNewApiChannel.value ? `newapi-${props.channelUid!.trim()}` : ''),
)
const channelProxy = computed(() => {
  const proxyUrl = props.channelProxyUrl?.trim()
  return proxyUrl
    ? { proxyUrl, proxyPreferDirect: props.channelProxyPreferDirect || false }
    : undefined
})
const channelProxyHint = computed(() => {
  const proxyUrl = props.channelProxyUrl?.trim()
  const key = proxyUrl ? 'subscription.newApi.proxyInheritedHint' : 'subscription.newApi.proxyDirectHint'
  return proxyUrl ? `${t(key)}（${proxyUrl}）` : t(key)
})
const hasPrimaryCredential = computed(() => !!primary.value?.accessTokenMasked)
const canBindNewApi = computed(() => Boolean(
  props.isGeneric &&
  props.channelName?.trim() &&
  props.baseUrl?.trim() &&
  props.channelUid?.trim() &&
  props.channelKind?.trim() &&
  bindForm.value.accessToken.trim() &&
  bindForm.value.userId.trim(),
))

function formatQuota(value?: number) {
  return new Intl.NumberFormat().format(value ?? 0)
}

function formatTime(value?: string) {
  if (!value) return '-'
  const date = new Date(value)
  return Number.isNaN(date.getTime()) ? value : date.toLocaleString()
}

function authTokenModeLabel(mode?: string) {
  return mode === 'raw_auth' ? 'Raw' : 'Bearer'
}

function requireEligibleGroups(result: NewApiVerifyResponse) {
  if (result.groupFetchError) {
    throw new Error(`${t('subscription.newApi.groupFetchError')} ${result.groupFetchError}`)
  }
  const groups = eligibleNewApiGroups(result.groups, MAX_GROUP_MULTIPLIER)
  if (groups.length === 0) {
    throw new Error(t('subscription.newApi.noEligibleGroups', { limit: String(MAX_GROUP_MULTIPLIER) }))
  }
  return groups
}

async function fetchPrimary() {
  if (!effectiveSubscriptionUid.value) return
  loadingPrimary.value = true
  primaryError.value = ''
  try {
    primary.value = await adminApi.get<SubscriptionItem>(subscriptionPath(effectiveSubscriptionUid.value))
  } catch (cause) {
    primaryError.value = cause instanceof AdminApiError && cause.status === 404
      ? t('subscription.newApi.subscriptionNotFound')
      : cause instanceof Error ? cause.message : String(cause)
  } finally {
    loadingPrimary.value = false
  }
}

async function refreshPrimary() {
  if (!effectiveSubscriptionUid.value) return
  refreshingPrimary.value = true
  primaryError.value = ''
  try {
    const response = await adminApi.post<{ subscription: SubscriptionItem }>(subscriptionRefreshPath(effectiveSubscriptionUid.value))
    primary.value = response.subscription
    emit('updated')
  } catch (cause) {
    primaryError.value = cause instanceof Error ? cause.message : String(cause)
  } finally {
    refreshingPrimary.value = false
  }
}

// 账号平权：订阅凭证行同样可删除（清空订阅级凭证并剔除其自动接入 key）；
// 换凭证 = 删除后经「添加账号」重新提供，首个添加的账号凭证将承担账号同步。
async function deletePrimary() {
  if (!effectiveSubscriptionUid.value || !hasPrimaryCredential.value) return
  deletingPrimary.value = true
  primaryError.value = ''
  pendingDeletePrimary.value = false
  try {
    await adminApi.del(subscriptionPrimaryAccountPath(effectiveSubscriptionUid.value))
    expandedPrimary.value = false
    await fetchPrimary()
    await fetchAccounts()
    emit('updated')
  } catch (cause) {
    primaryError.value = cause instanceof Error ? cause.message : String(cause)
  } finally {
    deletingPrimary.value = false
  }
}

async function fetchAccounts() {
  if (!effectiveSubscriptionUid.value) return
  loading.value = true
  try {
    const response = await adminApi.getSubscriptionAccounts(effectiveSubscriptionUid.value)
    accounts.value = response.accounts || []
  } catch (cause) {
    console.error('Failed to fetch accounts:', cause)
  } finally {
    loading.value = false
  }
}

// generic 未绑定分支：绑定 = verify + provision（代理沿用渠道"代理通道"设置，不在表单单独配置）
async function bindNewApi() {
  if (!canBindNewApi.value) return
  binding.value = true
  bindError.value = ''
  try {
    const baseUrl = props.baseUrl!.trim()
    const accessToken = bindForm.value.accessToken.trim()
    const userId = bindForm.value.userId.trim()
    const authTokenMode = bindForm.value.authTokenMode
    const subscriptionUid = `newapi-${props.channelUid!.trim()}`
    const proxyUrl = channelProxy.value?.proxyUrl
    const proxyPreferDirect = channelProxy.value?.proxyPreferDirect || undefined
    const verified = await adminApi.post<NewApiVerifyResponse>(NEWAPI_VERIFY_PATH, {
      baseUrl,
      accessToken,
      userId,
      authTokenMode,
      displayName: props.channelName!.trim(),
      subscriptionUid,
      proxyUrl,
      proxyPreferDirect,
    })
    requireEligibleGroups(verified)
    const response = await adminApi.post<NewApiProvisionResponse>(NEWAPI_PROVISION_PATH, {
      subscriptionUid,
      displayName: props.channelName!.trim(),
      baseUrl,
      accessToken,
      userId: String(verified.userId),
      authTokenMode,
      channelKind: props.channelKind as ChannelKind,
      channelName: props.channelName!.trim(),
      provisionAllEligibleGroups: true,
      maxGroupMultiplier: MAX_GROUP_MULTIPLIER,
      provisionModels: verified.availableModels,
      proxyUrl,
      proxyPreferDirect,
    })
    localSubscriptionUid.value = response.subscription.subscriptionUid
    bindForm.value = { accessToken: '', userId: '', authTokenMode: 'bearer' }
    await Promise.all([fetchPrimary(), fetchAccounts()])
    emit('updated')
  } catch (cause) {
    bindError.value = cause instanceof Error ? cause.message : String(cause)
  } finally {
    binding.value = false
  }
}

async function addAccount() {
  if (!primary.value) {
    // 订阅信息未就绪（未关联或加载失败）时给出反馈，而不是静默无响应
    addError.value = primaryError.value || t('subscription.newApi.subscriptionUnavailable')
    return
  }
  if (!effectiveSubscriptionUid.value || !addForm.value.accessToken.trim() || !addForm.value.userId.trim()) return
  adding.value = true
  addError.value = ''
  try {
    const accessToken = addForm.value.accessToken.trim()
    const userId = addForm.value.userId.trim()
    const authTokenMode = addForm.value.authTokenMode
    const proxyUrl = channelProxy.value?.proxyUrl
    const proxyPreferDirect = channelProxy.value?.proxyPreferDirect || undefined
    const verified = await adminApi.post<NewApiVerifyResponse>(NEWAPI_VERIFY_PATH, {
      baseUrl: primary.value.baseUrl || props.baseUrl || '',
      accessToken,
      userId,
      authTokenMode,
      subscriptionUid: effectiveSubscriptionUid.value,
      proxyUrl,
      proxyPreferDirect,
    })
    requireEligibleGroups(verified)
    await adminApi.addSubscriptionAccount(effectiveSubscriptionUid.value, {
      accessToken,
      userId: String(verified.userId),
      // 名称不可自定义：固定采用站点用户名
      displayName: verified.username || undefined,
      authTokenMode,
      provisionAllEligibleGroups: true,
      maxGroupMultiplier: MAX_GROUP_MULTIPLIER,
      provisionModels: verified.availableModels,
    })
    addForm.value = { accessToken: '', userId: '', authTokenMode: 'bearer' }
    // 添加账号会自动接入新 Key，主账号行与账号列表都要重拉，避免 Key 统计停留在旧值
    await Promise.all([fetchPrimary(), fetchAccounts()])
    emit('updated')
  } catch (cause) {
    addError.value = cause instanceof Error ? cause.message : String(cause)
  } finally {
    adding.value = false
  }
}

async function refreshAccount(accountUid: string) {
  if (!effectiveSubscriptionUid.value) return
  refreshing.value = accountUid
  error.value = ''
  try {
    await adminApi.refreshSubscriptionAccount(effectiveSubscriptionUid.value, accountUid)
    // 刷新账号可能重新接入 Key 并更新余额，主账号统计同步重拉
    await Promise.all([fetchPrimary(), fetchAccounts()])
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : String(cause)
  } finally {
    refreshing.value = ''
  }
}

async function deleteAccount(accountUid: string) {
  if (!effectiveSubscriptionUid.value) return
  deleting.value = accountUid
  error.value = ''
  pendingDeleteAccountUid.value = ''
  try {
    await adminApi.deleteSubscriptionAccount(effectiveSubscriptionUid.value, accountUid)
    if (expandedAccountUid.value === accountUid) expandedAccountUid.value = ''
    // 删除账号会剔除其自动接入 Key，主账号统计同步重拉
    await Promise.all([fetchPrimary(), fetchAccounts()])
    emit('updated')
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : String(cause)
  } finally {
    deleting.value = ''
  }
}

watch(effectiveSubscriptionUid, () => {
  primary.value = null
  accounts.value = []
  localSubscriptionUid.value = ''
  expandedPrimary.value = false
  expandedAccountUid.value = ''
  pendingDeletePrimary.value = false
  pendingDeleteAccountUid.value = ''
  void Promise.all([fetchPrimary(), fetchAccounts()])
}, { immediate: true })

onMounted(() => {
  if (!effectiveSubscriptionUid.value) void fetchPrimary()
})
</script>

<template>
  <div class="space-y-4 rounded-xl border border-border bg-card/40 p-4">
    <div class="flex items-center justify-between gap-3">
      <div class="flex items-center gap-2">
        <Users class="h-4 w-4 text-amber-500" />
        <p class="text-sm font-semibold">{{ t('subscription.newApi.accountManagement') }}</p>
      </div>
      <Button size="sm" variant="outline" :disabled="loading" @click="fetchAccounts">
        <Loader2 v-if="loading" class="h-3.5 w-3.5 animate-spin" />
        <RefreshCw v-else class="h-3.5 w-3.5" />
        {{ t('subscription.management.refresh') }}
      </Button>
    </div>

    <!-- generic 未绑定分支：先绑定 new-api 订阅，再进入多账号视图 -->
    <form v-if="isGeneric && !effectiveSubscriptionUid" class="space-y-3" @submit.prevent="bindNewApi">
      <p class="flex items-start gap-2 text-xs text-muted-foreground">
        <Link2 class="mt-0.5 h-3.5 w-3.5 shrink-0" />
        {{ t('subscription.newApi.genericAutoManagedHint') }}
      </p>
      <div class="space-y-1.5">
        <Label class="text-xs text-muted-foreground">{{ t('subscription.newApi.accessToken') }}</Label>
        <Input v-model="bindForm.accessToken" type="password" autocomplete="off" required />
      </div>
      <div class="grid gap-2 sm:grid-cols-2">
        <div class="space-y-1.5">
          <Label class="text-xs text-muted-foreground">{{ t('subscription.newApi.userId') }}</Label>
          <Input v-model="bindForm.userId" required />
        </div>
        <div class="space-y-1.5">
          <Label class="text-xs text-muted-foreground">{{ t('subscription.newApi.authTokenMode') }}</Label>
          <Select v-model="bindForm.authTokenMode">
            <SelectTrigger class="h-9 w-full"><SelectValue /></SelectTrigger>
            <SelectContent>
              <SelectItem value="bearer">Bearer</SelectItem>
              <SelectItem value="raw_auth">Raw Authorization</SelectItem>
            </SelectContent>
          </Select>
        </div>
      </div>
      <p class="text-xs text-muted-foreground">{{ channelProxyHint }}</p>
      <p v-if="bindError" class="flex items-start gap-2 text-xs text-destructive">
        <AlertCircle class="mt-0.5 h-3.5 w-3.5 shrink-0" />{{ bindError }}
      </p>
      <Button type="submit" :disabled="!canBindNewApi || binding">
        <Loader2 v-if="binding" class="h-3.5 w-3.5 animate-spin" />
        {{ t('subscription.newApi.bindAccount') }}
      </Button>
    </form>

    <template v-else>
      <p v-if="loadingPrimary" class="flex items-center gap-2 text-xs text-muted-foreground">
        <Loader2 class="h-3.5 w-3.5 animate-spin" />{{ t('subscription.newApi.accountManagement') }}...
      </p>
      <p v-else-if="primaryError" class="flex items-start gap-2 text-xs text-destructive">
        <AlertCircle class="mt-0.5 h-3.5 w-3.5 shrink-0" />{{ primaryError }}
      </p>
      <p v-else-if="!primary" class="rounded-lg border border-dashed p-3 text-xs text-muted-foreground">
        {{ t('subscription.newApi.primaryAccountUnavailable') }}
      </p>
      <p v-else-if="!hasPrimaryCredential && !accounts.length" class="rounded-lg border border-dashed p-3 text-xs text-muted-foreground">
        {{ t('subscription.newApi.primaryAccountRemoved') }}
      </p>

      <!-- 订阅凭证行与账号列表平权展示：同样可删除（清空订阅凭证并剔除其自动接入 key），
           换凭证 = 删除后经「添加账号」重新提供，首个添加的账号凭证将承担账号同步。 -->
      <div v-if="primary && hasPrimaryCredential" class="rounded-lg border border-border bg-background/60">
        <div
          class="flex cursor-pointer items-center justify-between gap-3 p-3"
          role="button"
          tabindex="0"
          :aria-expanded="expandedPrimary"
          @click="expandedPrimary = !expandedPrimary"
          @keydown.enter.prevent="expandedPrimary = !expandedPrimary"
        >
          <div class="flex min-w-0 items-center gap-3">
            <CheckCircle2 v-if="!primary.lastBalanceRefreshError" class="h-4 w-4 shrink-0 text-emerald-500" />
            <AlertCircle v-else class="h-4 w-4 shrink-0 text-amber-500" />
            <div class="min-w-0">
              <p class="truncate text-sm font-medium">{{ primary.displayName || primary.subscriptionUid }}</p>
              <p class="truncate text-xs text-muted-foreground">
                {{ t('subscription.newApi.quota') }}: {{ formatQuota(primary.balance) }}
                <template v-if="primary.accessTokenMasked"> · {{ primary.accessTokenMasked }}</template>
                <template v-if="primary.provisionedKeys?.length">
                  · {{ t('subscription.newApi.provisionedKeysCount', { count: primary.provisionedKeys.length }) }}
                </template>
              </p>
            </div>
          </div>
          <div class="flex shrink-0 items-center gap-1" @click.stop>
            <Button size="icon" variant="ghost" :disabled="refreshingPrimary" @click="refreshPrimary">
              <Loader2 v-if="refreshingPrimary" class="h-3.5 w-3.5 animate-spin" />
              <RefreshCw v-else class="h-3.5 w-3.5" />
            </Button>
            <template v-if="pendingDeletePrimary">
              <Button size="sm" variant="outline" class="h-7 border-destructive/40 px-2 text-xs text-destructive" :disabled="deletingPrimary" @click="deletePrimary">
                <Loader2 v-if="deletingPrimary" class="h-3.5 w-3.5 animate-spin" />
                <CheckCircle2 v-else class="h-3.5 w-3.5" />
                {{ t('subscription.accountRemoveConfirm') }}
              </Button>
              <Button size="icon" variant="ghost" :aria-label="t('common.cancel')" @click="pendingDeletePrimary = false">
                <X class="h-3.5 w-3.5" />
              </Button>
            </template>
            <Button v-else size="icon" variant="ghost" class="text-destructive" :title="t('common.delete')" @click="pendingDeletePrimary = true">
              <Trash2 class="h-3.5 w-3.5" />
            </Button>
            <ChevronUp v-if="expandedPrimary" class="h-4 w-4 text-muted-foreground" />
            <ChevronDown v-else class="h-4 w-4 text-muted-foreground" />
          </div>
        </div>

        <div v-if="expandedPrimary" class="space-y-3 border-t border-dashed border-border/60 bg-background/40 p-3">
          <div class="grid grid-cols-2 gap-3 sm:grid-cols-3">
            <div class="min-w-0 space-y-0.5">
              <p class="text-xs text-muted-foreground">{{ t('subscription.newApi.userId') }}</p>
              <p class="truncate text-sm">{{ primary.userId || '-' }}</p>
            </div>
            <div class="min-w-0 space-y-0.5">
              <p class="text-xs text-muted-foreground">{{ t('subscription.newApi.quota') }}</p>
              <p class="truncate text-sm">{{ formatQuota(primary.balance) }}</p>
            </div>
            <div class="min-w-0 space-y-0.5">
              <p class="text-xs text-muted-foreground">{{ t('subscription.newApi.lastRefreshedAt') }}</p>
              <p class="truncate text-sm">{{ formatTime(primary.lastBalanceRefreshAt) }}</p>
            </div>
            <div class="min-w-0 space-y-0.5">
              <p class="text-xs text-muted-foreground">{{ t('subscription.newApi.authTokenMode') }}</p>
              <p class="truncate text-sm">{{ authTokenModeLabel(primary.authTokenMode) }}</p>
            </div>
            <div class="col-span-2 min-w-0 space-y-0.5 sm:col-span-3">
              <p class="text-xs text-muted-foreground">{{ t('subscription.newApi.baseUrl') }}</p>
              <code class="block truncate text-xs">{{ primary.baseUrl || '-' }}</code>
            </div>
            <div class="col-span-2 min-w-0 space-y-0.5 sm:col-span-3">
              <p class="text-xs text-muted-foreground">{{ t('subscription.newApi.accessToken') }}</p>
              <code class="block break-all text-xs">{{ primary.accessTokenMasked || '-' }}</code>
            </div>
          </div>

          <div v-if="primary.provisionedKeys?.length" class="space-y-1">
            <p class="text-xs text-muted-foreground">{{ t('subscription.newApi.provisionedKeys') }}</p>
            <div class="flex flex-wrap gap-1">
              <span v-for="key in primary.provisionedKeys" :key="key.tokenId" class="rounded-full border border-primary/30 bg-primary/10 px-1.5 py-0.5 text-[10px] text-primary">
                {{ key.name }} · {{ key.group }} × {{ key.groupMultiplier }}
              </span>
            </div>
          </div>

          <p v-if="primary.lastBalanceRefreshError" class="flex items-start gap-2 text-xs text-amber-600">
            <AlertCircle class="mt-0.5 h-3.5 w-3.5 shrink-0" />{{ primary.lastBalanceRefreshError }}
          </p>
        </div>
      </div>

      <div class="rounded-lg border border-border bg-background/60">
        <button
          type="button"
          class="flex w-full items-center gap-2 p-3 text-left text-sm font-medium transition-colors hover:bg-secondary/40"
          @click="showAddForm = !showAddForm"
        >
          <Plus class="h-4 w-4" />
          {{ t('subscription.newApi.addAccount') }}
          <ChevronUp v-if="showAddForm" class="ml-auto h-4 w-4 text-muted-foreground" />
          <ChevronDown v-else class="ml-auto h-4 w-4 text-muted-foreground" />
        </button>
        <form v-if="showAddForm" class="grid gap-2 border-t border-border/60 p-3 sm:grid-cols-2" @submit.prevent="addAccount">
          <div class="space-y-1.5 sm:col-span-2">
            <Label class="text-xs text-muted-foreground">{{ t('subscription.newApi.accessToken') }}</Label>
            <Input v-model="addForm.accessToken" type="password" autocomplete="off" required />
          </div>
          <div class="space-y-1.5">
            <Label class="text-xs text-muted-foreground">{{ t('subscription.newApi.userId') }}</Label>
            <Input v-model="addForm.userId" required />
          </div>
          <div class="space-y-1.5">
            <Label class="text-xs text-muted-foreground">{{ t('subscription.newApi.authTokenMode') }}</Label>
            <Select v-model="addForm.authTokenMode">
              <SelectTrigger class="h-9 w-full"><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="bearer">Bearer</SelectItem>
                <SelectItem value="raw_auth">Raw Authorization</SelectItem>
              </SelectContent>
            </Select>
          </div>
          <p class="text-xs text-muted-foreground sm:col-span-2">{{ channelProxyHint }}</p>
          <p v-if="addError" class="flex items-start gap-2 text-xs text-destructive sm:col-span-2">
            <AlertCircle class="mt-0.5 h-3.5 w-3.5 shrink-0" />{{ addError }}
          </p>
          <Button type="submit" class="sm:col-span-2" :disabled="adding || !addForm.accessToken.trim() || !addForm.userId.trim()">
            <Loader2 v-if="adding" class="h-3.5 w-3.5 animate-spin" />
            <Plus v-else class="h-3.5 w-3.5" />
            {{ t('common.add') }}
          </Button>
        </form>
      </div>

      <p v-if="error" class="flex items-start gap-2 text-xs text-destructive">
        <AlertCircle class="mt-0.5 h-3.5 w-3.5 shrink-0" />{{ error }}
      </p>

      <div v-if="accounts.length" class="space-y-2">
        <div v-for="account in accounts" :key="account.accountUid" class="rounded-lg border border-border bg-background/60">
          <div
            class="flex cursor-pointer items-center justify-between gap-3 p-3"
            role="button"
            tabindex="0"
            :aria-expanded="expandedAccountUid === account.accountUid"
            @click="expandedAccountUid = expandedAccountUid === account.accountUid ? '' : account.accountUid"
            @keydown.enter.prevent="expandedAccountUid = expandedAccountUid === account.accountUid ? '' : account.accountUid"
          >
            <div class="flex min-w-0 items-center gap-3">
              <CheckCircle2 v-if="account.status === 'active'" class="h-4 w-4 shrink-0 text-emerald-500" />
              <AlertCircle v-else class="h-4 w-4 shrink-0 text-amber-500" />
              <div class="min-w-0">
                <p class="truncate text-sm font-medium">{{ account.displayName || account.accountUid }}</p>
                <p class="truncate text-xs text-muted-foreground">
                  {{ t('subscription.newApi.quota') }}: {{ formatQuota(account.balance) }}
                  <template v-if="account.accessTokenMasked"> · {{ account.accessTokenMasked }}</template>
                  <template v-if="account.provisionedKeys?.length">
                    · {{ t('subscription.newApi.provisionedKeysCount', { count: account.provisionedKeys.length }) }}
                  </template>
                </p>
              </div>
            </div>
            <div class="flex shrink-0 items-center gap-1" @click.stop>
              <Button size="icon" variant="ghost" :disabled="refreshing === account.accountUid" @click="refreshAccount(account.accountUid)">
                <Loader2 v-if="refreshing === account.accountUid" class="h-3.5 w-3.5 animate-spin" />
                <RefreshCw v-else class="h-3.5 w-3.5" />
              </Button>
              <template v-if="pendingDeleteAccountUid === account.accountUid">
                <Button size="sm" variant="outline" class="h-7 border-destructive/40 px-2 text-xs text-destructive" :disabled="deleting === account.accountUid" @click="deleteAccount(account.accountUid)">
                  <Loader2 v-if="deleting === account.accountUid" class="h-3.5 w-3.5 animate-spin" />
                  <CheckCircle2 v-else class="h-3.5 w-3.5" />
                  {{ t('subscription.accountRemoveConfirm') }}
                </Button>
                <Button size="icon" variant="ghost" :aria-label="t('common.cancel')" @click="pendingDeleteAccountUid = ''">
                  <X class="h-3.5 w-3.5" />
                </Button>
              </template>
              <Button v-else size="icon" variant="ghost" class="text-destructive" :title="t('common.delete')" @click="pendingDeleteAccountUid = account.accountUid">
                <Trash2 class="h-3.5 w-3.5" />
              </Button>
              <ChevronUp v-if="expandedAccountUid === account.accountUid" class="h-4 w-4 text-muted-foreground" />
              <ChevronDown v-else class="h-4 w-4 text-muted-foreground" />
            </div>
          </div>

          <div v-if="expandedAccountUid === account.accountUid" class="space-y-3 border-t border-dashed border-border/60 bg-background/40 p-3">
            <div class="grid grid-cols-2 gap-3 sm:grid-cols-3">
              <div class="min-w-0 space-y-0.5">
                <p class="text-xs text-muted-foreground">{{ t('subscription.newApi.userId') }}</p>
                <p class="truncate text-sm">{{ account.userId || '-' }}</p>
              </div>
              <div class="min-w-0 space-y-0.5">
                <p class="text-xs text-muted-foreground">{{ t('subscription.newApi.status') }}</p>
                <p class="truncate text-sm">{{ account.status || '-' }}</p>
              </div>
              <div class="min-w-0 space-y-0.5">
                <p class="text-xs text-muted-foreground">{{ t('subscription.newApi.quota') }}</p>
                <p class="truncate text-sm">{{ formatQuota(account.balance) }}</p>
              </div>
              <div class="min-w-0 space-y-0.5">
                <p class="text-xs text-muted-foreground">{{ t('subscription.newApi.lastCheckedAt') }}</p>
                <p class="truncate text-sm">{{ formatTime(account.lastCheckedAt) }}</p>
              </div>
              <div class="min-w-0 space-y-0.5">
                <p class="text-xs text-muted-foreground">{{ t('subscription.newApi.createdAt') }}</p>
                <p class="truncate text-sm">{{ formatTime(account.createdAt) }}</p>
              </div>
              <div class="col-span-2 min-w-0 space-y-0.5 sm:col-span-3">
                <p class="text-xs text-muted-foreground">{{ t('subscription.newApi.accessToken') }}</p>
                <code class="block break-all text-xs">{{ account.accessTokenMasked || '-' }}</code>
              </div>
            </div>

            <div v-if="account.provisionedKeys?.length" class="space-y-1">
              <p class="text-xs text-muted-foreground">{{ t('subscription.newApi.provisionedKeys') }}</p>
              <div class="flex flex-wrap gap-1">
                <span v-for="key in account.provisionedKeys" :key="key.tokenId" class="rounded-full border border-primary/30 bg-primary/10 px-1.5 py-0.5 text-[10px] text-primary">
                  {{ key.name }} · {{ key.group }} × {{ key.groupMultiplier }}
                </span>
              </div>
            </div>

            <p v-if="account.lastSyncError" class="flex items-start gap-2 text-xs text-amber-600">
              <AlertCircle class="mt-0.5 h-3.5 w-3.5 shrink-0" />{{ account.lastSyncError }}
            </p>
          </div>
        </div>
      </div>
      <!-- 空账号提示仅在完全无账号时出现：已有主账号凭证行（或任一子账号）时不重复提醒 -->
      <p v-else-if="!loading && !hasPrimaryCredential" class="rounded-lg border border-dashed p-3 text-xs text-muted-foreground">
        {{ t('subscription.newApi.noAccounts') }}
      </p>
    </template>
  </div>
</template>
