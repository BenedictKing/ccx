<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { AlertTriangle, CheckCircle2, Loader2 } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'
import { useAdminApi } from '@/composables/useAdminApi'
import { useLanguage } from '@/composables/useLanguage'
import { NEWAPI_PROVISION_PATH, NEWAPI_VERIFY_PATH } from '@/services/admin-api'
import { stripDashboardPathFromBaseUrl } from '@/utils/base-url-semantics'
import type {
  ChannelKind,
  NewApiProvisionRequest,
  NewApiProvisionResponse,
  NewApiVerifyRequest,
  NewApiVerifyResponse,
} from '@/services/admin-api'

const { t } = useLanguage()
const adminApi = useAdminApi()
const emit = defineEmits<{ created: [result: NewApiProvisionResponse]; error: [message: string] }>()

const verifying = ref(false)
const provisioning = ref(false)
const verified = ref(false)
const verifyResult = ref<NewApiVerifyResponse | null>(null)
const maxGroupMultiplierText = ref('1')

const verifyForm = ref<NewApiVerifyRequest>({
  baseUrl: '', accessToken: '', userId: '', authTokenMode: 'bearer', displayName: '',
})
const provisionForm = ref<NewApiProvisionRequest>({
  subscriptionUid: '', displayName: '', baseUrl: '', accessToken: '', channelKind: 'messages',
  userId: '', authTokenMode: 'bearer', channelName: '', provisionAllEligibleGroups: true, notes: '',
})

const authTokenModeOptions = [
  { label: 'Bearer', value: 'bearer' },
  { label: 'Raw Authorization', value: 'raw_auth' },
]
const channelKindOptions: ChannelKind[] = ['messages', 'chat', 'responses', 'gemini', 'images', 'vectors']
const maxGroupMultiplier = computed(() => Number(maxGroupMultiplierText.value))
const maxGroupMultiplierValid = computed(() => {
  const value = maxGroupMultiplier.value
  return maxGroupMultiplierText.value.trim() !== '' && Number.isFinite(value) && value >= 0
})
const groupItems = computed(() => Object.entries(verifyResult.value?.groups || {})
  .map(([name, ratio]) => ({ name, ratio }))
  .sort((a, b) => a.ratio - b.ratio || a.name.localeCompare(b.name)))
const eligibleGroupItems = computed(() => maxGroupMultiplierValid.value
  ? groupItems.value.filter(group => Number.isFinite(group.ratio) && group.ratio >= 0 && group.ratio <= maxGroupMultiplier.value)
  : [])
const blockedGroupItems = computed(() => groupItems.value.filter(group => !eligibleGroupItems.value.includes(group)))
const canVerify = computed(() => !!verifyForm.value.baseUrl.trim() && !!verifyForm.value.accessToken.trim())
const canProvision = computed(() => !!provisionForm.value.subscriptionUid.trim()
  && !!provisionForm.value.channelKind
  && maxGroupMultiplierValid.value
  && !verifyResult.value?.groupFetchError
  && eligibleGroupItems.value.length > 0)

function slugifyDisplayName(name: string): string {
  return name.trim().toLowerCase().replace(/[^a-z0-9一-龥]+/g, '-').replace(/^-+|-+$/g, '') || 'newapi'
}
watch(() => verifyForm.value.displayName, name => {
  if (!verified.value && name) provisionForm.value.subscriptionUid = `newapi-${slugifyDisplayName(name)}`
})
function normalizeVerifyBaseUrl() {
  verifyForm.value.baseUrl = stripDashboardPathFromBaseUrl(verifyForm.value.baseUrl)
}
async function handleVerify() {
  if (!canVerify.value) return
  normalizeVerifyBaseUrl()
  verifying.value = true
  try {
    const result = await adminApi.post<NewApiVerifyResponse>(NEWAPI_VERIFY_PATH, {
      baseUrl: verifyForm.value.baseUrl.trim(), accessToken: verifyForm.value.accessToken,
      userId: verifyForm.value.userId || undefined, authTokenMode: verifyForm.value.authTokenMode || undefined,
      displayName: verifyForm.value.displayName || undefined,
    })
    verifyResult.value = result
    verified.value = true
    Object.assign(provisionForm.value, {
      baseUrl: verifyForm.value.baseUrl.trim(), accessToken: verifyForm.value.accessToken,
      userId: verifyForm.value.userId || undefined, authTokenMode: verifyForm.value.authTokenMode || undefined,
      displayName: verifyForm.value.displayName || result.username,
    })
    if (!provisionForm.value.subscriptionUid.trim()) {
      provisionForm.value.subscriptionUid = `newapi-${slugifyDisplayName(verifyForm.value.displayName || result.username)}`
    }
  } catch (error) {
    emit('error', error instanceof Error ? error.message : 'Unknown error')
  } finally { verifying.value = false }
}
function resetVerification() { verified.value = false; verifyResult.value = null }
async function handleProvision() {
  if (!canProvision.value) return
  provisioning.value = true
  try {
    const result = await adminApi.post<NewApiProvisionResponse>(NEWAPI_PROVISION_PATH, {
      ...provisionForm.value,
      subscriptionUid: provisionForm.value.subscriptionUid.trim(),
      displayName: provisionForm.value.displayName || provisionForm.value.subscriptionUid,
      userId: provisionForm.value.userId || undefined,
      authTokenMode: provisionForm.value.authTokenMode || undefined,
      channelName: provisionForm.value.channelName || undefined,
      provisionAllEligibleGroups: true,
      maxGroupMultiplier: maxGroupMultiplier.value,
      notes: provisionForm.value.notes || undefined,
    })
    emit('created', result)
  } catch (error) {
    emit('error', error instanceof Error ? error.message : 'Unknown error')
  } finally { provisioning.value = false }
}
</script>

<template>
  <div class="flex flex-col gap-4">
    <form class="flex flex-col gap-3" @submit.prevent="handleVerify">
      <div class="text-xs font-semibold uppercase tracking-wide text-muted-foreground">{{ t('subscription.newApi.step1Title') }}</div>
      <div class="space-y-1.5"><Label class="text-xs text-muted-foreground">{{ t('subscription.newApi.baseUrl') }}</Label><Input v-model="verifyForm.baseUrl" placeholder="https://your-newapi-instance.com" :disabled="verified" @blur="normalizeVerifyBaseUrl" /></div>
      <div class="space-y-1.5"><Label class="text-xs text-muted-foreground">{{ t('subscription.newApi.accessToken') }}</Label><Input v-model="verifyForm.accessToken" type="password" autocomplete="off" :disabled="verified" /></div>
      <div class="space-y-1.5"><Label class="text-xs text-muted-foreground">{{ t('subscription.newApi.userId') }}</Label><Input v-model="verifyForm.userId" :disabled="verified" /></div>
      <div class="space-y-1.5">
        <Label class="text-xs text-muted-foreground">{{ t('subscription.newApi.authTokenMode') }}</Label>
        <Select v-model="verifyForm.authTokenMode" :disabled="verified"><SelectTrigger class="h-9 w-full"><SelectValue /></SelectTrigger><SelectContent><SelectItem v-for="opt in authTokenModeOptions" :key="opt.value" :value="opt.value">{{ opt.label }}</SelectItem></SelectContent></Select>
      </div>
      <div class="space-y-1.5"><Label class="text-xs text-muted-foreground">{{ t('subscription.field.name') }}</Label><Input v-model="verifyForm.displayName" :disabled="verified" /></div>
      <Button v-if="!verified" type="submit" :disabled="!canVerify || verifying" class="w-full"><Loader2 v-if="verifying" class="h-3.5 w-3.5 animate-spin" />{{ t('subscription.newApi.verify') }}</Button>
      <Button v-else type="button" variant="outline" class="w-full" @click="resetVerification">{{ t('subscription.newApi.reVerify') }}</Button>
    </form>

    <div v-if="verified && verifyResult" class="rounded-lg border border-border bg-card/40 p-3">
      <div class="mb-2 flex items-center gap-1.5 text-xs font-semibold"><CheckCircle2 class="h-3.5 w-3.5 text-emerald-500" />{{ t('subscription.newApi.accountPreview') }}</div>
      <div class="space-y-1 text-xs text-muted-foreground">
        <div>{{ t('subscription.newApi.username') }}: {{ verifyResult.username }}</div><div>{{ t('subscription.newApi.quota') }}: {{ verifyResult.quota }}</div><div>{{ t('subscription.newApi.usedQuota') }}: {{ verifyResult.usedQuota }}</div><div>{{ t('subscription.newApi.availableModels') }}: {{ verifyResult.availableModels.length }}</div>
        <div v-if="groupItems.length" class="flex flex-wrap gap-1"><span v-for="g in groupItems" :key="g.name" class="rounded-full border px-1.5 py-0.5 text-[10px]">{{ g.name }} × {{ g.ratio }}</span></div>
      </div>
    </div>

    <form v-if="verified" class="flex flex-col gap-3 border-t border-border pt-3" @submit.prevent="handleProvision">
      <div class="text-xs font-semibold uppercase tracking-wide text-muted-foreground">{{ t('subscription.newApi.step2Title') }}</div>
      <div class="space-y-1.5"><Label class="text-xs text-muted-foreground">{{ t('subscription.field.uid') }}</Label><Input v-model="provisionForm.subscriptionUid" /></div>
      <div class="space-y-1.5"><Label class="text-xs text-muted-foreground">{{ t('subscription.newApi.channelKind') }}</Label><Select v-model="provisionForm.channelKind"><SelectTrigger class="h-9 w-full"><SelectValue /></SelectTrigger><SelectContent><SelectItem v-for="kind in channelKindOptions" :key="kind" :value="kind">{{ kind }}</SelectItem></SelectContent></Select></div>
      <div class="space-y-1.5"><Label class="text-xs text-muted-foreground">{{ t('subscription.newApi.channelName') }}</Label><Input v-model="provisionForm.channelName" /></div>
      <div class="space-y-1.5"><Label class="text-xs text-muted-foreground">{{ t('subscription.newApi.maxGroupMultiplier') }}</Label><Input v-model="maxGroupMultiplierText" type="number" min="0" step="any" /><p class="text-[11px] text-muted-foreground">{{ t('subscription.newApi.maxGroupMultiplierHint', { limit: maxGroupMultiplierText }) }}</p></div>
      <div v-if="!maxGroupMultiplierValid" class="flex gap-2 rounded-lg border border-destructive/30 bg-destructive/10 p-2 text-xs text-destructive"><AlertTriangle class="h-4 w-4 shrink-0" />{{ t('subscription.newApi.invalidMaxGroupMultiplier') }}</div>
      <div v-if="verifyResult?.groupFetchError" class="rounded-lg border border-destructive/30 bg-destructive/10 p-2 text-xs text-destructive">{{ t('subscription.newApi.groupFetchError') }} {{ verifyResult.groupFetchError }}</div>
      <div v-if="eligibleGroupItems.length" class="rounded-lg border border-emerald-500/20 bg-emerald-500/10 p-2 text-xs text-emerald-700 dark:text-emerald-300">{{ t('subscription.newApi.eligibleGroups', { count: eligibleGroupItems.length }) }}<span v-for="g in eligibleGroupItems" :key="g.name" class="ml-1">{{ g.name }} × {{ g.ratio }}</span></div>
      <div v-if="blockedGroupItems.length" class="rounded-lg border border-amber-500/20 bg-amber-500/10 p-2 text-xs text-amber-700 dark:text-amber-300">{{ t('subscription.newApi.excludedGroups', { count: blockedGroupItems.length, limit: maxGroupMultiplierText }) }}</div>
      <div v-if="maxGroupMultiplierValid && !verifyResult?.groupFetchError && eligibleGroupItems.length === 0" class="rounded-lg border border-destructive/30 bg-destructive/10 p-2 text-xs text-destructive">{{ t('subscription.newApi.noEligibleGroups', { limit: maxGroupMultiplierText }) }}</div>
      <div class="space-y-1.5"><Label class="text-xs text-muted-foreground">{{ t('subscription.field.notes') }}</Label><Textarea v-model="provisionForm.notes" class="min-h-[60px]" /></div>
      <Button type="submit" :disabled="!canProvision || provisioning" class="w-full"><Loader2 v-if="provisioning" class="h-3.5 w-3.5 animate-spin" />{{ t('subscription.newApi.provision') }}</Button>
    </form>
  </div>
</template>
