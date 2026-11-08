<script setup lang="ts">
import { computed, onMounted, ref, watch, nextTick } from 'vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { useAdminApi } from '@/composables/useAdminApi'
import { useLanguage } from '@/composables/useLanguage'
import { useStatus } from '@/composables/useStatus'
import type { AgentPlatform } from '@/types'
import type { SiteAccount, SiteGroupPreview, SiteProvisionResult, SiteRemoteKey } from '../../../../../frontend/src/services/api-types'

const emit = defineEmits<{ configureAgent: [AgentPlatform] }>()
const api = useAdminApi()
const { t } = useLanguage()
const { status } = useStatus()
const accounts = ref<SiteAccount[]>([])
const uid = ref('')
const siteType = ref('newapi')
const credentialMode = ref<'management' | 'key'>('management')
const baseUrl = ref('')
const accessToken = ref('')
const userId = ref('')
const authTokenMode = ref('bearer')
const agent = ref<AgentPlatform>('codex')
const modelsText = ref('')
const maxMultiplier = ref('1')
const allowModelFallback = ref(false)
const autoSyncGroups = ref(false)
const maxAttempts = ref('6')
const maxWaitSeconds = ref('90')
const groups = ref<SiteGroupPreview[]>([])
const selectedGroups = ref<string[]>([])
const remoteKeys = ref<SiteRemoteKey[]>([])
const existingTokenIds = ref<Record<string, number>>({})
const connected = ref(false)
const busy = ref(false)
const message = ref('')
const result = ref<SiteProvisionResult | null>(null)
const protocol = computed(() => agent.value === 'claude' ? 'messages' : agent.value === 'codex' ? 'responses' : 'chat')
const models = computed(() => [...new Set(modelsText.value.split(/[,\n]/).map(value => value.trim()).filter(Boolean))])
const modelOptions = computed(() => [...new Set(groups.value.flatMap(group => group.models || []))])
const connectionUrl = computed(() => `${status.value.url}${agent.value === 'claude' ? '' : '/v1'}`)
const eligible = (group: SiteGroupPreview) => !group.error && Number.isFinite(group.ratio) && group.ratio <= Number(maxMultiplier.value) && models.value.length > 0 && (group.modelsUnknown || models.value.some(model => group.models?.includes(model)))
watch([models, maxMultiplier, groups], () => {
  selectedGroups.value = credentialMode.value === 'key' ? ['imported'] : groups.value.filter(eligible).map(group => group.name)
})

async function refresh() {
  try { accounts.value = (await api.get<{ accounts: SiteAccount[] }>('/api/siteaccess/accounts')).accounts } catch (error) { message.value = String(error) }
}
onMounted(refresh)
function selectAccount() {
  const account = accounts.value.find(item => item.uid === uid.value)
  if (!account) return
  siteType.value = account.siteType
  credentialMode.value = account.credentialMode || 'management'
  baseUrl.value = account.baseUrl
  userId.value = account.userId || ''
  authTokenMode.value = account.authTokenMode || 'bearer'
  accessToken.value = ''
  connected.value = false
  result.value = null
  restorePlan(account)
}
function restorePlan(account: SiteAccount) {
  const plan = account.plans?.find(item => item.protocol === protocol.value)
  if (!plan) return
  modelsText.value = (plan.models || []).join(', ')
  maxMultiplier.value = String(plan.maxMultiplier ?? 1)
  allowModelFallback.value = !!plan.allowModelFallback
  autoSyncGroups.value = !!plan.autoSyncGroups
  maxAttempts.value = String(plan.maxAttempts || 6)
  maxWaitSeconds.value = String(plan.maxWaitSeconds || 90)
  existingTokenIds.value = { ...(plan.existingTokenIds || {}) }
}
async function connect() {
  busy.value = true; message.value = ''; connected.value = false; result.value = null
  try {
    const response = await api.post<{ account: SiteAccount }>('/api/siteaccess/accounts', { uid: uid.value || undefined, siteType: siteType.value, credentialMode: credentialMode.value, baseUrl: baseUrl.value, userId: userId.value, accessToken: accessToken.value, authTokenMode: authTokenMode.value })
    uid.value = response.account.uid
    restorePlan(response.account)
    const verification = await api.post<{ verified: boolean; error?: string }>(`/api/siteaccess/accounts/${uid.value}/verify`)
    if (verification.error) message.value = verification.error
    const preview = await api.get<{ groups?: SiteGroupPreview[]; error?: string }>(`/api/siteaccess/accounts/${uid.value}/groups`)
    groups.value = preview.groups || []
    const plan = response.account.plans?.find(item => item.protocol === protocol.value)
    if (plan?.groups?.length) { await nextTick(); selectedGroups.value = plan.groups.filter(name => groups.value.some(group => group.name === name && eligible(group))) }
    if (preview.error) message.value = preview.error
    try { remoteKeys.value = (await api.get<{ keys: SiteRemoteKey[] }>(`/api/siteaccess/accounts/${uid.value}/keys`)).keys || [] } catch { remoteKeys.value = [] }
    connected.value = true
    await refresh()
  } catch (error) { message.value = String(error) } finally { busy.value = false }
}
async function provision() {
  busy.value = true; message.value = ''
  try {
    result.value = await api.post<SiteProvisionResult>(`/api/siteaccess/accounts/${uid.value}/provision`, { agent: agent.value, protocol: protocol.value, models: models.value, groups: selectedGroups.value, maxMultiplier: Number(maxMultiplier.value), existingTokenIds: existingTokenIds.value, allowModelFallback: allowModelFallback.value, autoSyncGroups: autoSyncGroups.value, excludeGroups: groups.value.filter(group => eligible(group) && !selectedGroups.value.includes(group.name)).map(group => group.name), maxAttempts: Number(maxAttempts.value), maxWaitSeconds: Number(maxWaitSeconds.value) })
    await refresh()
  } catch (error) { message.value = String(error) } finally { busy.value = false }
}
async function verify(group: string) {
  const available = groups.value.find(item => item.name === group)
  const model = models.value.find(item => credentialMode.value === 'key' || available?.modelsUnknown || available?.models?.includes(item))
  if (!model) return
  busy.value = true
  try { const response = await api.post<{ success: boolean; error?: string }>(`/api/siteaccess/accounts/${uid.value}/verify-request`, { protocol: protocol.value, group, model }); message.value = response.success ? t('siteOnboarding.realVerifySuccess', { group, model }) : response.error || t('siteOnboarding.realVerifyFailed') } catch (error) { message.value = String(error) } finally { busy.value = false }
}
async function reconcile() {
  busy.value = true
  try { await api.post(`/api/siteaccess/accounts/${uid.value}/reconcile`); await refresh(); message.value = accounts.value.find(a => a.uid === uid.value)?.lastSyncError || t('siteOnboarding.reconcileClean') } catch (error) { message.value = String(error) } finally { busy.value = false }
}
</script>

<template>
  <section class="space-y-5 rounded-xl border border-border bg-card/40 p-5">
    <h3 class="text-base font-semibold">{{ t('siteOnboarding.title') }}</h3>
    <p v-if="message" role="status" class="rounded-lg border border-border bg-secondary/40 p-3 text-sm whitespace-pre-wrap">{{ message }}</p>
    <form class="grid gap-4 md:grid-cols-2" @submit.prevent="connect">
      <label class="space-y-1 text-sm"><span>{{ t('siteOnboarding.existingAccount') }}</span><select v-model="uid" class="h-9 w-full rounded border border-input bg-background px-2" @change="selectAccount"><option value="">{{ t('siteOnboarding.stepSite') }}</option><option v-for="account in accounts" :key="account.uid" :value="account.uid">{{ account.baseUrl }} · {{ account.userId || account.maskedAccessToken }}</option></select></label>
      <label class="space-y-1 text-sm"><span>{{ t('siteOnboarding.siteType') }}</span><select v-model="siteType" class="h-9 w-full rounded border border-input bg-background px-2"><option>newapi</option><option>sub2api</option></select></label>
      <div class="space-y-1"><Label>{{ t('siteOnboarding.baseUrl') }}</Label><Input v-model="baseUrl" placeholder="https://relay.example.com" required /></div>
      <label class="space-y-1 text-sm"><span>{{ t('siteOnboarding.credentialMode') }}</span><select v-model="credentialMode" class="h-9 w-full rounded border border-input bg-background px-2"><option value="management">{{ t('siteOnboarding.managementCredential') }}</option><option value="key">{{ t('siteOnboarding.inferenceKey') }}</option></select></label>
      <div class="space-y-1"><Label>{{ t(credentialMode === 'key' ? 'siteOnboarding.inferenceKey' : 'siteOnboarding.accessToken') }}</Label><Input v-model="accessToken" type="password" :required="!uid" :placeholder="uid ? t('siteOnboarding.keepCredential') : ''" /></div>
      <div v-if="credentialMode === 'management' && siteType === 'newapi'" class="space-y-1"><Label>{{ t('siteOnboarding.userId') }}</Label><Input v-model="userId" /></div>
      <label v-if="credentialMode === 'management' && siteType === 'newapi'" class="space-y-1 text-sm"><span>{{ t('siteOnboarding.authMode') }}</span><select v-model="authTokenMode" class="h-9 w-full rounded border border-input bg-background px-2"><option value="bearer">Bearer</option><option value="raw_auth">Raw (fork)</option></select></label>
      <Button type="submit" :disabled="busy">{{ t('siteOnboarding.verifyAndContinue') }}</Button>
    </form>

    <form v-if="connected" class="space-y-4 border-t border-border pt-4" @submit.prevent="provision">
      <div class="grid gap-4 md:grid-cols-2">
        <label class="space-y-1 text-sm"><span>{{ t('siteOnboarding.agent') }}</span><select v-model="agent" class="h-9 w-full rounded border border-input bg-background px-2"><option value="claude">Claude Code</option><option value="codex">Codex</option><option value="opencode">OpenCode</option></select></label>
        <div class="space-y-1"><Label>{{ t('siteOnboarding.targetModels') }}</Label><Input v-model="modelsText" list="site-models" required /><datalist id="site-models"><option v-for="model in modelOptions" :key="model" :value="model" /></datalist></div>
      </div>
      <p class="text-xs text-muted-foreground">{{ t('siteOnboarding.modelFirstHint') }}</p>
      <label class="flex items-center gap-2 text-sm"><input v-model="allowModelFallback" type="checkbox" :disabled="models.length < 2">{{ t('siteOnboarding.allowFallback') }}</label>
      <label v-if="credentialMode === 'management'" class="flex items-center gap-2 text-sm"><input v-model="autoSyncGroups" type="checkbox">{{ t('siteOnboarding.autoGroups') }}</label>
      <p v-if="credentialMode === 'management'" class="text-xs text-muted-foreground">{{ t('siteOnboarding.autoGroupsHint') }}</p>
      <p class="text-xs text-muted-foreground">{{ t('siteOnboarding.fallbackOrder', { models: models.join(' → ') }) }}</p>
      <div class="grid grid-cols-2 gap-3"><label class="text-xs">{{ t('siteOnboarding.maxAttempts') }}<Input v-model="maxAttempts" type="number" min="1" max="30" required /></label><label class="text-xs">{{ t('siteOnboarding.retryWindow') }}<Input v-model="maxWaitSeconds" type="number" min="1" max="300" required /></label></div>
      <div v-if="credentialMode !== 'key'" class="space-y-3">
        <Label>{{ t('siteOnboarding.maxMultiplier') }}<Input v-model="maxMultiplier" type="number" min="0" step="any" required class="mt-1 max-w-40" /></Label>
        <div v-for="group in groups" :key="group.name" class="flex flex-wrap items-center gap-3 rounded border border-border p-3 text-sm">
          <label class="flex flex-1 items-center gap-2"><input v-model="selectedGroups" type="checkbox" :value="group.name" :disabled="!eligible(group)"> {{ group.displayName || group.name }} · ×{{ group.ratio }} <span v-if="group.modelsUnknown" class="text-xs text-muted-foreground">{{ t('siteOnboarding.modelsUnknown') }}</span></label>
          <select v-model="existingTokenIds[group.name]" class="h-8 rounded border border-input bg-background px-2" :aria-label="t('siteOnboarding.existingKey')"><option :value="undefined">{{ t('siteOnboarding.createKey') }}</option><option v-for="key in remoteKeys.filter(key => key.group === group.name && (key.status === 0 || key.status === 1))" :key="key.id" :value="key.id">{{ key.name }}</option></select>
        </div>
      </div>
      <Button type="submit" :disabled="busy || !models.length || !selectedGroups.length">{{ t('siteOnboarding.startProvision') }}</Button>
    </form>

    <div v-if="result" class="space-y-3 border-t border-border pt-4">
      <div v-for="group in result.groups" :key="group.group" class="flex flex-wrap items-center justify-between gap-3 text-sm"><span>{{ group.channelName }} · {{ group.skipped || t('siteOnboarding.createdChannel') }}</span><Button v-if="!group.skipped" variant="outline" :disabled="busy" @click="verify(group.group)">{{ t('siteOnboarding.verifyReal') }}</Button></div>
      <div v-if="result.groups.some(group => !group.skipped)" class="space-y-3"><code class="text-sm">{{ connectionUrl }}</code><div class="flex flex-wrap gap-2"><Button @click="emit('configureAgent', agent)">{{ t('siteOnboarding.applyAgent') }}</Button><Button variant="outline" :disabled="busy" @click="reconcile">{{ t('siteOnboarding.reconcile') }}</Button></div></div>
    </div>
  </section>
</template>
