<template>
  <div class="site-onboarding-wizard">
    <v-alert v-if="verifyError" type="warning" variant="tonal" density="compact" class="mb-3">{{ verifyError }}</v-alert>
    <div class="d-sm-none text-subtitle-2 mb-2">{{ step }} / {{ stepTitles.length }} {{ stepTitles[step - 1] }}</div>
    <v-stepper v-model="step" :items="stepTitles" alt-labels hide-actions class="mb-4">
      <template #[`item.1`]>
        <v-card variant="flat">
          <v-card-text>
            <v-select v-model="existingAccountUid" :items="existingAccounts" item-title="baseUrl" item-value="uid" :label="t('siteOnboarding.existingAccount')" clearable variant="outlined" density="compact" @update:model-value="selectAccount" />
            <v-row dense>
              <v-col cols="6"><v-select v-model="form.siteType" :items="['newapi', 'sub2api']" :label="t('siteOnboarding.siteType')" variant="outlined" density="compact" /></v-col>
              <v-col cols="6"><v-select v-model="form.credentialMode" :items="credentialModes" :label="t('siteOnboarding.credentialMode')" variant="outlined" density="compact" /></v-col>
            </v-row>
            <v-text-field
              v-model="form.baseUrl"
              :label="t('siteOnboarding.baseUrl')"
              placeholder="https://your-newapi-instance.com"
              variant="outlined"
              density="compact"
              class="mb-1"
            />
            <div v-if="recognizedBaseUrl" class="text-caption text-medium-emphasis mb-3">
              <v-icon size="14" color="success">mdi-arrow-right</v-icon>
              {{ t('siteOnboarding.recognizedBaseUrl', { url: recognizedBaseUrl }) }}
            </div>
            <v-text-field
              v-model="form.accessToken"
              :label="t(form.credentialMode === 'key' ? 'siteOnboarding.inferenceKey' : 'siteOnboarding.accessToken')"
              :hint="t(existingAccountUid ? 'siteOnboarding.keepCredential' : 'siteOnboarding.accessTokenHint')"
              variant="outlined"
              density="compact"
              type="password"
              class="mb-2"
            />
            <v-row v-if="form.credentialMode === 'management' && form.siteType === 'newapi'" dense>
              <v-col cols="6">
                <v-text-field
                  v-model="form.userId"
                  :label="t('siteOnboarding.userId')"
                  :hint="t('siteOnboarding.userIdHint')"
                  variant="outlined"
                  density="compact"
                />
              </v-col>
              <v-col cols="6">
                <v-select
                  v-model="form.authTokenMode"
                  :items="authModes"
                  :label="t('siteOnboarding.authMode')"
                  variant="outlined"
                  density="compact"
                />
              </v-col>
            </v-row>
            <div class="d-flex ga-2 mt-4">
              <v-btn color="primary" :loading="verifying" :disabled="!form.baseUrl || (!form.accessToken && !existingAccountUid)" @click="verifyAndContinue">
                {{ t('siteOnboarding.verifyAndContinue') }}
              </v-btn>
            </div>
          </v-card-text>
        </v-card>
      </template>

      <template #[`item.2`]>
        <v-card variant="flat">
          <v-card-text>
            <v-select v-model="agent" :items="agents" :label="t('siteOnboarding.agent')" variant="outlined" density="compact" @update:model-value="selectAgent" />
            <v-select
              v-model="protocol"
              :items="protocols"
              :label="t('siteOnboarding.protocol')"
              variant="outlined"
              density="compact"
              class="mb-3"
              :disabled="agent === 'claude' || agent === 'codex'"
            />
            <v-combobox v-model="selectedModels" :items="availableModels" :label="t('siteOnboarding.targetModels')" :hint="t('siteOnboarding.modelFirstHint')" persistent-hint multiple chips closable-chips variant="outlined" density="compact" class="mb-4" />
            <v-text-field
              v-if="form.credentialMode !== 'key'"
              v-model.number="maxMultiplier"
              :label="t('siteOnboarding.maxMultiplier')"
              type="number"
              variant="outlined"
              density="compact"
              class="mb-3"
            />
            <v-progress-linear v-if="loadingGroups" indeterminate class="mb-3" />
            <v-table v-else-if="form.credentialMode !== 'key'" density="compact">
              <thead>
                <tr>
                  <th></th>
                  <th>{{ t('siteOnboarding.group') }}</th>
                  <th>{{ t('siteOnboarding.ratio') }}</th>
                  <th>{{ t('siteOnboarding.modelCount') }}</th>
                  <th>{{ t('siteOnboarding.existingKey') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="group in groupsPreview" :key="group.name">
                  <td>
                    <v-checkbox-btn v-model="selectedGroups" :value="group.name" :disabled="!groupEligible(group)" />
                  </td>
                  <td>{{ group.displayName || group.name }}</td>
                  <td>{{ group.ratio }}</td>
                  <td>
                    <span v-if="group.error" class="text-warning">{{ t('siteOnboarding.groupQueryFailed') }}</span>
                    <span v-else-if="group.modelsUnknown">{{ t('siteOnboarding.modelsUnknown') }}</span>
                    <span v-else>{{ group.models?.length ?? 0 }}</span>
                  </td>
                  <td><v-select :model-value="existingTokenIds[group.name] || 0" :items="keyOptions(group.name)" variant="outlined" density="compact" hide-details style="min-width: 160px" @update:model-value="existingTokenIds[group.name] = $event" /></td>
                </tr>
              </tbody>
            </v-table>
            <div class="d-flex ga-2 mt-4">
              <v-btn variant="text" @click="step = 1">{{ t('siteOnboarding.back') }}</v-btn>
              <v-btn color="primary" :disabled="selectedGroups.length === 0 || selectedModels.length === 0" @click="step = 3">
                {{ t('siteOnboarding.next') }}
              </v-btn>
            </div>
          </v-card-text>
        </v-card>
      </template>

      <template #[`item.3`]>
        <v-card variant="flat">
          <v-card-text>
            <v-alert variant="tonal" type="info" class="mb-3">{{ t('siteOnboarding.mappingHint', { agent, model: selectedModels[0] || '' }) }}</v-alert>
            <v-switch v-model="allowModelFallback" :disabled="selectedModels.length < 2" :label="t('siteOnboarding.allowFallback')" :hint="t('siteOnboarding.fallbackOrder', { models: selectedModels.join(' → ') })" persistent-hint />
            <v-switch v-if="form.credentialMode === 'management'" v-model="autoSyncGroups" :label="t('siteOnboarding.autoGroups')" :hint="t('siteOnboarding.autoGroupsHint')" persistent-hint />
            <v-row dense class="my-3"><v-col cols="6"><v-text-field v-model.number="maxAttempts" type="number" min="1" max="30" :label="t('siteOnboarding.maxAttempts')" variant="outlined" density="compact" /></v-col><v-col cols="6"><v-text-field v-model.number="maxWaitSeconds" type="number" min="1" max="300" :label="t('siteOnboarding.retryWindow')" variant="outlined" density="compact" /></v-col></v-row>
            <div class="text-subtitle-2 mb-2">{{ t('siteOnboarding.modelMapping') }}</div>
            <div v-for="(row, idx) in mappingRows" :key="idx" class="d-flex ga-2 mb-2">
              <v-text-field v-model="row.from" :placeholder="t('siteOnboarding.mappingFrom')" variant="outlined" density="compact" />
              <v-text-field v-model="row.to" :placeholder="t('siteOnboarding.mappingTo')" variant="outlined" density="compact" />
              <v-btn icon="mdi-close" variant="text" size="small" @click="mappingRows.splice(idx, 1)" />
            </div>
            <v-btn size="small" variant="text" prepend-icon="mdi-plus" @click="mappingRows.push({ from: '', to: '' })">
              {{ t('siteOnboarding.addMapping') }}
            </v-btn>
            <div class="d-flex ga-2 mt-4">
              <v-btn variant="text" @click="step = 2">{{ t('siteOnboarding.back') }}</v-btn>
              <v-btn color="primary" :loading="provisioning" :disabled="selectedModels.length === 0" @click="provision">
                {{ t('siteOnboarding.startProvision') }}
              </v-btn>
            </div>
          </v-card-text>
        </v-card>
      </template>

      <template #[`item.4`]>
        <v-card variant="flat">
          <v-card-text>
            <v-alert v-if="provisionResult?.groups.some(group => !group.skipped)" type="info" variant="tonal" class="mb-3">
              {{ t('siteOnboarding.connectionReady') }}
              <div class="mt-2"><code>{{ connectionUrl }}</code></div>
              <div>{{ t('siteOnboarding.targetModels') }}: {{ selectedModels.join(', ') }}</div>
            </v-alert>
            <v-table v-if="provisionResult" density="compact">
              <thead>
                <tr>
                  <th>{{ t('siteOnboarding.group') }}</th>
                  <th>{{ t('siteOnboarding.result') }}</th>
                  <th>{{ t('siteOnboarding.evidence') }}</th>
                  <th>{{ t('siteOnboarding.actions') }}</th>
                </tr>
              </thead>
              <tbody>
                <tr v-for="group in provisionResult.groups" :key="group.group">
                  <td>{{ group.group }}</td>
                  <td>
                    <v-chip v-if="!group.skipped" size="small" color="success">
                      {{ group.reused ? t('siteOnboarding.reusedKey') : t('siteOnboarding.createdChannel') }}
                    </v-chip>
                    <v-chip v-else size="small" color="warning">{{ group.skipped }}</v-chip>
                  </td>
                  <td>
                    <v-chip v-if="!group.skipped" size="small" variant="outlined">{{ evidenceLabel(group.group) }}</v-chip>
                  </td>
                  <td>
                    <template v-if="!group.skipped">
                      <v-btn size="small" variant="text" :loading="verifyingGroup === group.group" @click="verifyReal(group.group)">
                        {{ t('siteOnboarding.verifyReal') }}
                      </v-btn>
                    </template>
                  </td>
                </tr>
              </tbody>
            </v-table>
            <v-alert v-if="realVerifyMessage" :type="realVerifySuccess ? 'success' : 'warning'" variant="tonal" density="compact" class="mt-3">
              {{ realVerifyMessage }}
              <div v-if="!realVerifySuccess" class="text-caption mt-1">{{ t('siteOnboarding.probeFailureNotFatal') }}</div>
            </v-alert>
            <div class="d-flex ga-2 mt-4">
              <v-btn variant="text" :loading="reconciling" @click="reconcile">{{ t('siteOnboarding.reconcile') }}</v-btn>
              <v-btn color="primary" @click="$emit('provisioned')">{{ t('siteOnboarding.finish') }}</v-btn>
            </div>
          </v-card-text>
        </v-card>
      </template>
    </v-stepper>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch, onMounted, nextTick } from 'vue'
import { useI18n } from '../i18n'
import api from '../services/api'
import type { SiteAccount, SiteGroupPreview, SiteProvisionResult, SiteRemoteKey } from '../services/api-types'
import { parseQuickInput } from '../utils/quickInputParser'

defineEmits<{ provisioned: [] }>()
const { t } = useI18n()

const step = ref(1)
const stepTitles = computed(() => [
  t('siteOnboarding.stepSite'),
  t('siteOnboarding.stepProtocol'),
  t('siteOnboarding.stepModels'),
  t('siteOnboarding.stepAcceptance')
])

const authModes = [
  { title: 'Bearer', value: 'bearer' },
  { title: 'Raw (fork)', value: 'raw_auth' }
]
const protocols = ['messages', 'chat', 'responses', 'gemini']

const agents = [{ title: 'Claude Code', value: 'claude' }, { title: 'Codex', value: 'codex' }, { title: 'OpenCode', value: 'opencode' }, { title: 'API', value: 'custom' }]
const agent = ref('codex')
const credentialModes = computed(() => [{ title: t('siteOnboarding.managementCredential'), value: 'management' }, { title: t('siteOnboarding.inferenceKey'), value: 'key' }])
const existingAccounts = ref<SiteAccount[]>([])
const existingAccountUid = ref<string | null>(null)
const remoteKeys = ref<SiteRemoteKey[]>([])
const existingTokenIds = ref<Record<string, number>>({})
onMounted(async () => { existingAccounts.value = (await api.listSiteAccounts()).accounts })

const form = ref({ siteType: 'newapi', credentialMode: 'management' as 'management' | 'key', baseUrl: '', accessToken: '', userId: '', authTokenMode: 'bearer' })
const account = ref<SiteAccount | null>(null)
const verifying = ref(false)
const verifyError = ref('')

const recognizedBaseUrl = computed(() => parseQuickInput(form.value.baseUrl.trim()).detectedBaseUrl)
const submitBaseUrl = computed(() => recognizedBaseUrl.value || form.value.baseUrl.trim())

const groupsPreview = ref<SiteGroupPreview[]>([])
const loadingGroups = ref(false)
const protocol = ref('responses')
const selectedGroups = ref<string[]>([])
const maxMultiplier = ref(1.0)

const selectedModels = ref<string[]>([])
const allowModelFallback = ref(false)
const autoSyncGroups = ref(false)
const maxAttempts = ref(6)
const maxWaitSeconds = ref(90)
const mappingRows = ref<{ from: string; to: string }[]>([])

const provisioning = ref(false)
const provisionResult = ref<SiteProvisionResult | null>(null)
const verifyingGroup = ref('')
const realVerifyMessage = ref('')
const realVerifySuccess = ref(false)
const reconciling = ref(false)
const connectionUrl = computed(() => `${window.location.origin}${protocol.value === 'gemini' ? '/v1beta' : protocol.value === 'messages' ? '' : '/v1'}`)

function selectAccount(uid: string | null) {
  const selected = existingAccounts.value.find(item => item.uid === uid)
  if (!selected) return
  form.value = { siteType: selected.siteType, credentialMode: selected.credentialMode || 'management', baseUrl: selected.baseUrl, accessToken: '', userId: selected.userId || '', authTokenMode: selected.authTokenMode || 'bearer' }
  restorePlan(selected)
}
function restorePlan(selected: SiteAccount) {
  const plan = selected.plans?.find(item => item.protocol === protocol.value)
  if (!plan) return
  selectedModels.value = [...(plan.models || [])]
  maxMultiplier.value = plan.maxMultiplier ?? 1
  allowModelFallback.value = plan.allowModelFallback ?? false
  autoSyncGroups.value = plan.autoSyncGroups ?? false
  maxAttempts.value = plan.maxAttempts || 6
  maxWaitSeconds.value = plan.maxWaitSeconds || 90
  existingTokenIds.value = { ...(plan.existingTokenIds || {}) }
  mappingRows.value = Object.entries(plan.modelMapping || {}).filter(([from, to]) => from !== to && !(['claude', 'gpt'].includes(from) && to === plan.models?.[0])).map(([from, to]) => ({ from, to }))
}
function selectAgent() {
  if (agent.value === 'claude') protocol.value = 'messages'
  if (agent.value === 'codex') protocol.value = 'responses'
  const selected = existingAccounts.value.find(item => item.uid === existingAccountUid.value)
  if (selected) restorePlan(selected)
}
function groupEligible(group: SiteGroupPreview) {
  return !group.error && Number.isFinite(group.ratio) && group.ratio <= maxMultiplier.value && selectedModels.value.length > 0 && (group.modelsUnknown || selectedModels.value.some(model => group.models?.includes(model)))
}
function keyOptions(group: string) {
  return [{ title: t('siteOnboarding.createKey'), value: 0 }, ...remoteKeys.value.filter(key => key.group === group && (key.status === 0 || key.status === 1)).map(key => ({ title: key.name, value: key.id }))]
}
watch([selectedModels, maxMultiplier, groupsPreview], () => {
  selectedGroups.value = form.value.credentialMode === 'key' ? ['imported'] : groupsPreview.value.filter(groupEligible).map(group => group.name)
}, { deep: true })

const availableModels = computed(() => {
  const models = new Set<string>()
  for (const group of groupsPreview.value) {
    for (const model of group.models ?? []) models.add(model)
  }
  return [...models].sort()
})

async function verifyAndContinue() {
  verifying.value = true
  verifyError.value = ''
  try {
    const { account: created } = await api.upsertSiteAccount({
      uid: existingAccountUid.value || undefined,
      siteType: form.value.siteType,
      credentialMode: form.value.credentialMode,
      baseUrl: submitBaseUrl.value,
      userId: form.value.userId || undefined,
      authTokenMode: form.value.authTokenMode,
      accessToken: form.value.accessToken
    })
    account.value = created
    restorePlan(created)
    existingAccounts.value = [...existingAccounts.value.filter(item => item.uid !== created.uid), created]
    existingAccountUid.value = created.uid
    const verifyResult = await api.verifySiteAccount(created.uid)
    if (!verifyResult.verified) {
      // 信息验证失败不阻断接入：诊断展示，用户可继续（探针失败 ≠ 站点不可用）
      verifyError.value = verifyResult.error || ''
    }
    await loadGroups()
    step.value = 2
  } catch (err) {
    verifyError.value = err instanceof Error ? err.message : String(err)
  } finally {
    verifying.value = false
  }
}

async function loadGroups() {
  if (!account.value) return
  loadingGroups.value = true
  try {
    const response = await api.getSiteAccountGroups(account.value.uid)
    groupsPreview.value = (response.groups ?? []).sort((a, b) => a.ratio - b.ratio || a.name.localeCompare(b.name))
    if (response.error) verifyError.value = response.error
    if (form.value.credentialMode !== 'key') {
      try { remoteKeys.value = (await api.getSiteAccountKeys(account.value.uid)).keys || [] } catch { remoteKeys.value = [] }
    }
    const plan = account.value.plans?.find(item => item.protocol === protocol.value)
    if (plan?.groups?.length) { await nextTick(); selectedGroups.value = plan.groups.filter(name => groupsPreview.value.some(group => group.name === name && groupEligible(group))) }
  } finally {
    loadingGroups.value = false
  }
}

async function provision() {
  if (!account.value) return
  provisioning.value = true
  verifyError.value = ''
  realVerifyMessage.value = ''
  try {
    const mapping: Record<string, string> = {}
    for (const row of mappingRows.value) {
      if (row.from.trim() && row.to.trim()) mapping[row.from.trim()] = row.to.trim()
    }
    provisionResult.value = await api.provisionSiteAccount(account.value.uid, {
      protocol: protocol.value,
      agent: agent.value,
      allowModelFallback: allowModelFallback.value,
      autoSyncGroups: autoSyncGroups.value,
      excludeGroups: groupsPreview.value.filter(group => groupEligible(group) && !selectedGroups.value.includes(group.name)).map(group => group.name),
      maxAttempts: maxAttempts.value,
      maxWaitSeconds: maxWaitSeconds.value,
      existingTokenIds: existingTokenIds.value,
      groups: selectedGroups.value,
      maxMultiplier: maxMultiplier.value,
      models: selectedModels.value,
      modelMapping: Object.keys(mapping).length > 0 ? mapping : undefined
    })
    // 接入后刷新账号，展示最新的验收证据（configured 已落库）
    const { accounts } = await api.listSiteAccounts()
    account.value = accounts.find(a => a.uid === account.value?.uid) ?? account.value
    step.value = 4
  } catch (err) {
    verifyError.value = err instanceof Error ? err.message : String(err)
  } finally {
    provisioning.value = false
  }
}

function evidenceLabel(group: string): string {
  const evidence = account.value?.evidence?.find(e => e.group === group && e.protocol === protocol.value)
  const labels = { configured: 'siteOnboarding.configured', info_verified: 'siteOnboarding.infoVerified', real_request: 'siteOnboarding.realVerified' }
  return t(labels[evidence?.level as keyof typeof labels] || labels.configured)
}

async function verifyReal(group: string) {
  if (!account.value) return
  const groupModels = groupsPreview.value.find(item => item.name === group)?.models ?? []
  const model = selectedModels.value.find(item => form.value.credentialMode === 'key' || groupsPreview.value.find(g => g.name === group)?.modelsUnknown || groupModels.includes(item))
  if (!model) return
  verifyingGroup.value = group
  try {
    const result = await api.verifySiteRealRequest(account.value.uid, { protocol: protocol.value, group, model })
    realVerifySuccess.value = result.success
    realVerifyMessage.value = result.success
      ? t('siteOnboarding.realVerifySuccess', { group, model })
      : (result.error || t('siteOnboarding.realVerifyFailed'))
    const { accounts } = await api.listSiteAccounts()
    account.value = accounts.find(item => item.uid === account.value?.uid) ?? account.value
  } catch (err) {
    realVerifySuccess.value = false
    realVerifyMessage.value = err instanceof Error ? err.message : String(err)
  } finally {
    verifyingGroup.value = ''
  }
}

async function reconcile() {
  if (!account.value) return
  reconciling.value = true
  try {
    const { actions } = await api.reconcileSiteAccount(account.value.uid)
    realVerifySuccess.value = true
    realVerifyMessage.value = actions.length === 0
      ? t('siteOnboarding.reconcileClean')
      : actions.map(a => `${a.protocol}/${a.group}: ${a.action} ${a.detail}`).join('\n')
  } finally {
    reconciling.value = false
  }
}
</script>

<style scoped>
@media (max-width: 599px) {
  .site-onboarding-wizard :deep(.v-stepper-item) {
    flex: 0 0 40px;
    min-width: 0;
    padding: 8px 0;
  }

  .site-onboarding-wizard :deep(.v-stepper-header .v-divider) {
    flex: 1 1 0;
    min-width: 0;
    margin: 0;
  }

  .site-onboarding-wizard :deep(.v-stepper-item__title) {
    display: none;
  }
}
</style>
