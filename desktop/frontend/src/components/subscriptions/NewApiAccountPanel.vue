<script setup lang="ts">
import { onMounted, ref, watch } from 'vue'
import { AlertCircle, Loader2, Plus, RefreshCw, Trash2, Users } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { useAdminApi } from '@/composables/useAdminApi'
import { useLanguage } from '@/composables/useLanguage'
import type { NewApiAccountItem, SubscriptionItem } from '@/services/admin-api'

const props = defineProps<{ subscription: SubscriptionItem | null }>()
const emit = defineEmits<{ updated: [] }>()
const { t } = useLanguage()
const adminApi = useAdminApi()

const accounts = ref<NewApiAccountItem[]>([])
const loading = ref(false)
const adding = ref(false)
const refreshing = ref('')
const deleting = ref('')
const error = ref('')
const addForm = ref({ accessToken: '', userId: '', displayName: '', authTokenMode: 'bearer' })

async function loadAccounts() {
  if (!props.subscription?.subscriptionUid) {
    accounts.value = []
    return
  }
  loading.value = true
  error.value = ''
  try {
    const response = await adminApi.getSubscriptionAccounts(props.subscription.subscriptionUid)
    accounts.value = response.accounts || []
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : String(cause)
  } finally {
    loading.value = false
  }
}

async function addAccount() {
  if (!props.subscription?.subscriptionUid || !addForm.value.accessToken.trim()) return
  adding.value = true
  error.value = ''
  try {
    await adminApi.addSubscriptionAccount(props.subscription.subscriptionUid, {
      accessToken: addForm.value.accessToken.trim(),
      userId: addForm.value.userId.trim() || undefined,
      displayName: addForm.value.displayName.trim() || undefined,
      authTokenMode: addForm.value.authTokenMode,
    })
    addForm.value = { accessToken: '', userId: '', displayName: '', authTokenMode: 'bearer' }
    await loadAccounts()
    emit('updated')
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : String(cause)
  } finally {
    adding.value = false
  }
}

async function refreshAccount(accountUid: string) {
  if (!props.subscription?.subscriptionUid) return
  refreshing.value = accountUid
  error.value = ''
  try {
    await adminApi.refreshSubscriptionAccount(props.subscription.subscriptionUid, accountUid)
    await loadAccounts()
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : String(cause)
  } finally {
    refreshing.value = ''
  }
}

async function deleteAccount(accountUid: string) {
  if (!props.subscription?.subscriptionUid) return
  deleting.value = accountUid
  error.value = ''
  try {
    await adminApi.deleteSubscriptionAccount(props.subscription.subscriptionUid, accountUid)
    await loadAccounts()
    emit('updated')
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : String(cause)
  } finally {
    deleting.value = ''
  }
}

watch(() => props.subscription?.subscriptionUid, () => void loadAccounts())
onMounted(() => void loadAccounts())
</script>

<template>
  <div v-if="subscription" class="space-y-4 rounded-xl border border-border bg-card/40 p-4">
    <div class="flex items-center justify-between gap-3">
      <div class="flex items-center gap-2">
        <Users class="h-4 w-4 text-amber-500" />
        <div>
          <p class="text-sm font-semibold">{{ t('subscription.newApi.accountManagement') }}</p>
          <p class="text-xs text-muted-foreground">{{ subscription.displayName }} · {{ subscription.subscriptionUid }}</p>
        </div>
      </div>
      <Button size="sm" variant="outline" :disabled="loading" @click="loadAccounts">
        <Loader2 v-if="loading" class="h-3.5 w-3.5 animate-spin" />
        <RefreshCw v-else class="h-3.5 w-3.5" />
        {{ t('common.refresh') }}
      </Button>
    </div>

    <form class="grid gap-2 sm:grid-cols-2" @submit.prevent="addAccount">
      <div class="space-y-1.5 sm:col-span-2">
        <Label class="text-xs text-muted-foreground">{{ t('subscription.newApi.accessToken') }}</Label>
        <Input v-model="addForm.accessToken" type="password" autocomplete="off" required />
      </div>
      <div class="space-y-1.5"><Label class="text-xs text-muted-foreground">{{ t('subscription.newApi.userId') }}</Label><Input v-model="addForm.userId" /></div>
      <div class="space-y-1.5"><Label class="text-xs text-muted-foreground">{{ t('subscription.field.name') }}</Label><Input v-model="addForm.displayName" /></div>
      <div class="space-y-1.5 sm:col-span-2">
        <Label class="text-xs text-muted-foreground">{{ t('subscription.newApi.authTokenMode') }}</Label>
        <Select v-model="addForm.authTokenMode"><SelectTrigger class="h-9 w-full"><SelectValue /></SelectTrigger><SelectContent><SelectItem value="bearer">Bearer</SelectItem><SelectItem value="raw_auth">Raw Authorization</SelectItem></SelectContent></Select>
      </div>
      <Button type="submit" class="sm:col-span-2" :disabled="adding || !addForm.accessToken.trim()">
        <Loader2 v-if="adding" class="h-3.5 w-3.5 animate-spin" /><Plus v-else class="h-3.5 w-3.5" />
        {{ t('subscription.newApi.addAccount') }}
      </Button>
    </form>

    <p v-if="error" class="flex items-start gap-2 text-xs text-destructive"><AlertCircle class="mt-0.5 h-3.5 w-3.5 shrink-0" />{{ error }}</p>

    <div v-if="accounts.length" class="space-y-2">
      <div v-for="account in accounts" :key="account.accountUid" class="rounded-lg border border-border bg-background/60 p-3">
        <div class="flex items-start justify-between gap-3">
          <div class="min-w-0">
            <p class="text-sm font-medium">{{ account.displayName || account.accountUid }}</p>
            <p class="text-xs text-muted-foreground">{{ t('subscription.newApi.quota') }}: {{ account.balance ?? 0 }}<span v-if="account.accessTokenMasked"> · {{ account.accessTokenMasked }}</span></p>
            <div v-if="account.provisionedKeys?.length" class="mt-1 flex flex-wrap gap-1">
              <span v-for="key in account.provisionedKeys" :key="key.tokenId" class="rounded-full border border-primary/30 bg-primary/10 px-1.5 py-0.5 text-[10px] text-primary">{{ key.group }} × {{ key.groupMultiplier }}</span>
            </div>
            <p v-if="account.lastSyncError" class="mt-1 text-[11px] text-destructive">{{ account.lastSyncError }}</p>
          </div>
          <div class="flex gap-1">
            <Button size="icon" variant="ghost" :disabled="refreshing === account.accountUid" @click="refreshAccount(account.accountUid)"><Loader2 v-if="refreshing === account.accountUid" class="h-3.5 w-3.5 animate-spin" /><RefreshCw v-else class="h-3.5 w-3.5" /></Button>
            <Button size="icon" variant="ghost" class="text-destructive" :disabled="deleting === account.accountUid" @click="deleteAccount(account.accountUid)"><Loader2 v-if="deleting === account.accountUid" class="h-3.5 w-3.5 animate-spin" /><Trash2 v-else class="h-3.5 w-3.5" /></Button>
          </div>
        </div>
      </div>
    </div>
    <p v-else-if="!loading" class="rounded-lg border border-dashed p-3 text-xs text-muted-foreground">{{ t('subscription.newApi.noAccounts') }}</p>
  </div>
</template>
