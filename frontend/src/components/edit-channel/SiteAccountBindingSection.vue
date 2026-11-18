<template>
  <v-dialog :model-value="show" max-width="760" persistent @update:model-value="$emit('update:show', $event)">
    <v-card rounded="lg">
      <v-card-title class="d-flex align-center ga-2">
        <v-icon color="primary">mdi-account-switch</v-icon>
        {{ t('channelEditor.accountBinding.title') }}
      </v-card-title>
      <v-card-subtitle class="text-wrap">{{ t('channelEditor.accountBinding.subtitle') }}</v-card-subtitle>
      <v-card-text>
        <v-alert type="info" variant="tonal" density="compact" class="mb-4">
          {{ t('channelEditor.accountBinding.currentKey') }}: <code>{{ maskApiKey(apiKey) }}</code>
        </v-alert>
        <v-btn-toggle v-model="mode" mandatory color="primary" variant="outlined" class="mb-4">
          <v-btn value="bind">{{ t('channelEditor.accountBinding.bindCurrent') }}</v-btn>
          <v-btn value="copy">{{ t('channelEditor.accountBinding.copyGroup') }}</v-btn>
        </v-btn-toggle>
        <v-alert variant="text" density="compact" class="px-0 mb-2">
          {{ t(mode === 'bind' ? 'channelEditor.accountBinding.bindHint' : 'channelEditor.accountBinding.copyHint') }}
        </v-alert>

        <v-select
          v-model="selectedAccountUid"
          :items="accountOptions"
          :label="t('channelEditor.accountBinding.account')"
          :loading="loadingAccounts"
          variant="outlined"
          density="compact"
        />
        <template v-if="needsCredential">
          <v-row dense>
            <v-col cols="12" md="6">
              <v-select v-model="siteType" :items="['newapi', 'sub2api']" :label="t('siteOnboarding.siteType')" variant="outlined" density="compact" />
            </v-col>
            <v-col cols="12" md="6">
              <v-select v-model="authTokenMode" :items="authModes" :label="t('siteOnboarding.authMode')" variant="outlined" density="compact" />
            </v-col>
          </v-row>
          <v-text-field
            v-model="accessToken"
            :label="t('siteOnboarding.accessToken')"
            type="password"
            variant="outlined"
            density="compact"
            :hint="t('siteOnboarding.accessTokenHint')"
            persistent-hint
          />
          <v-text-field v-model="userId" :label="t('siteOnboarding.userId')" :hint="t('siteOnboarding.userIdHint')" variant="outlined" density="compact" />
        </template>

        <div class="d-flex justify-end mb-3">
          <v-btn color="primary" variant="tonal" :loading="loadingRemote" :disabled="!canLoadRemote" @click="loadRemote">
            {{ t('channelEditor.accountBinding.loadRemote') }}
          </v-btn>
        </div>

        <template v-if="remoteLoaded">
          <v-select
            v-model="selectedGroup"
            :items="groupOptions"
            :label="t('channelEditor.accountBinding.group')"
            variant="outlined"
            density="compact"
          />
          <v-select
            v-model="remoteTokenId"
            :items="remoteKeyOptions"
            :label="t('channelEditor.accountBinding.remoteKey')"
            :disabled="!selectedGroup"
            :hint="t(mode === 'bind' ? 'channelEditor.accountBinding.remoteBindHint' : 'channelEditor.accountBinding.remoteCopyHint')"
            persistent-hint
            variant="outlined"
            density="compact"
          />
        </template>
        <v-alert v-if="message" :type="messageType" variant="tonal" density="compact" class="mt-3">{{ message }}</v-alert>
      </v-card-text>
      <v-card-actions>
        <v-btn v-if="currentBinding" color="warning" variant="text" :loading="saving" @click="unbind">
          {{ t('channelEditor.accountBinding.unbindKey') }}
        </v-btn>
        <v-spacer />
        <v-btn variant="text" @click="$emit('update:show', false)">{{ t('app.actions.cancel') }}</v-btn>
        <v-btn color="primary" :loading="saving" :disabled="!canSubmit" @click="submit">
          {{ t(mode === 'bind' ? 'channelEditor.accountBinding.confirmBind' : 'channelEditor.accountBinding.createCopy') }}
        </v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from '../../i18n'
import api from '../../services/api'
import type { Channel, SiteAccount, SiteGroupPreview, SiteRemoteKey } from '../../services/api-types'
import { maskApiKey } from '../../utils/apiKeyMask'

const NEW_ACCOUNT = '__new__'
const props = defineProps<{ show: boolean; channel: Channel; channelType: string; apiKey: string }>()
const emit = defineEmits<{ 'update:show': [boolean]; updated: [] }>()
const { t } = useI18n()

const accounts = ref<SiteAccount[]>([])
const loadingAccounts = ref(false)
const loadingRemote = ref(false)
const saving = ref(false)
const selectedAccountUid = ref(NEW_ACCOUNT)
const siteType = ref('newapi')
const authTokenMode = ref('bearer')
const accessToken = ref('')
const userId = ref('')
const mode = ref<'bind' | 'copy'>('bind')
const groups = ref<SiteGroupPreview[]>([])
const remoteKeys = ref<SiteRemoteKey[]>([])
const remoteLoaded = ref(false)
const selectedGroup = ref('')
const remoteTokenId = ref(0)
const message = ref('')
const messageType = ref<'success' | 'error'>('success')
const authModes = [{ title: 'Bearer', value: 'bearer' }, { title: 'Raw (fork)', value: 'raw_auth' }]

const currentConfig = computed(() => props.channel.apiKeyConfigs?.find(config => config.key === props.apiKey))
const currentBinding = computed(() => !!currentConfig.value?.sourceAccountUid)
const selectedAccount = computed(() => accounts.value.find(account => account.uid === selectedAccountUid.value))
const needsCredential = computed(() => selectedAccountUid.value === NEW_ACCOUNT || selectedAccount.value?.credentialMode === 'key')
const accountOptions = computed(() => [
  { title: t('channelEditor.accountBinding.newAccount'), value: NEW_ACCOUNT },
  ...accounts.value.map(account => ({ title: account.userId ? `${account.baseUrl} · ${account.userId}` : account.baseUrl, value: account.uid })),
])
const groupOptions = computed(() => groups.value.map(group => ({
  title: `${group.displayName || group.name} · ×${group.ratio}`,
  value: group.name,
})))
const remoteKeyOptions = computed(() => {
  const items = remoteKeys.value
    .filter(key => key.group === selectedGroup.value && (key.status === 0 || key.status === 1))
    .map(key => ({ title: `${key.name} · #${key.id}`, value: key.id }))
  if (mode.value === 'copy') items.unshift({ title: t('channelEditor.accountBinding.createRemoteKey'), value: 0 })
  return items
})
const canLoadRemote = computed(() => !!selectedAccountUid.value && (!needsCredential.value || !!accessToken.value.trim()))
const canSubmit = computed(() => remoteLoaded.value && !!selectedGroup.value && (mode.value === 'copy' || remoteTokenId.value > 0))

watch(() => [props.show, props.apiKey], ([show]) => {
  if (show) void initialize()
}, { immediate: true })
watch(selectedAccountUid, () => resetRemote())
watch(mode, () => {
  if (mode.value === 'bind' && remoteTokenId.value === 0) remoteTokenId.value = -1
  if (mode.value === 'copy' && remoteTokenId.value < 0) remoteTokenId.value = 0
})
watch(selectedGroup, () => {
  const current = currentConfig.value
  remoteTokenId.value = current?.sourceAccountUid === selectedAccountUid.value && current.sourceRemoteTokenId && remoteKeys.value.some(key => key.id === current.sourceRemoteTokenId && key.group === selectedGroup.value)
    ? current.sourceRemoteTokenId
    : (mode.value === 'copy' ? 0 : -1)
})

async function initialize() {
  message.value = ''
  mode.value = 'bind'
  accessToken.value = ''
  resetRemote()
  loadingAccounts.value = true
  try {
    accounts.value = (await api.listSiteAccounts()).accounts || []
    selectedAccountUid.value = currentConfig.value?.sourceAccountUid || props.channel.siteAccountUid || NEW_ACCOUNT
    const account = selectedAccount.value
    siteType.value = account?.siteType || 'newapi'
    userId.value = account?.userId || ''
    authTokenMode.value = account?.authTokenMode || 'bearer'
  } catch (error) {
    showError(error)
  } finally {
    loadingAccounts.value = false
  }
}

function resetRemote() {
  groups.value = []
  remoteKeys.value = []
  remoteLoaded.value = false
  selectedGroup.value = ''
  remoteTokenId.value = -1
}

async function ensureAccount(): Promise<SiteAccount> {
  if (!needsCredential.value && selectedAccount.value) return selectedAccount.value
  const token = accessToken.value.trim()
  if (!token) throw new Error(t('channelEditor.accountBinding.tokenRequired'))
  const { account } = await api.upsertSiteAccount({
    uid: selectedAccountUid.value === NEW_ACCOUNT ? undefined : selectedAccountUid.value,
    siteType: siteType.value,
    credentialMode: 'management',
    baseUrl: props.channel.baseUrl,
    userId: userId.value.trim() || undefined,
    authTokenMode: authTokenMode.value,
    accessToken: token,
  })
  accounts.value = [...accounts.value.filter(item => item.uid !== account.uid), account]
  selectedAccountUid.value = account.uid
  accessToken.value = ''
  return account
}

async function loadRemote() {
  loadingRemote.value = true
  message.value = ''
  try {
    const account = await ensureAccount()
    const [groupResponse, keyResponse] = await Promise.all([api.getSiteAccountGroups(account.uid), api.getSiteAccountKeys(account.uid)])
    if (!groupResponse.identified) throw new Error(groupResponse.error || t('channelEditor.accountBinding.remoteLoadFailed'))
    groups.value = (groupResponse.groups || []).sort((a, b) => a.ratio - b.ratio || a.name.localeCompare(b.name))
    remoteKeys.value = keyResponse.keys || []
    const currentGroup = currentConfig.value?.quotaGroup?.startsWith(account.uid + '/')
      ? currentConfig.value.quotaGroup.slice(account.uid.length + 1)
      : props.channel.siteGroup
    selectedGroup.value = groups.value.some(group => group.name === currentGroup) ? currentGroup! : (groups.value[0]?.name || '')
    remoteLoaded.value = true
  } catch (error) {
    showError(error)
  } finally {
    loadingRemote.value = false
  }
}

async function submit() {
  saving.value = true
  message.value = ''
  try {
    const account = await ensureAccount()
    const request = { accountUid: account.uid, apiKey: props.apiKey, group: selectedGroup.value, remoteTokenId: remoteTokenId.value }
    if (mode.value === 'bind') {
      await api.bindSiteKey(props.channelType, props.channel.index, request)
    } else {
      await api.copySiteChannelGroup(props.channelType, props.channel.index, request)
    }
    messageType.value = 'success'
    message.value = t(mode.value === 'bind' ? 'channelEditor.accountBinding.bound' : 'channelEditor.accountBinding.copied')
    emit('updated')
    emit('update:show', false)
  } catch (error) {
    showError(error)
  } finally {
    saving.value = false
  }
}

async function unbind() {
  saving.value = true
  message.value = ''
  try {
    await api.unbindSiteKey(props.channelType, props.channel.index, props.apiKey)
    emit('updated')
    emit('update:show', false)
  } catch (error) {
    showError(error)
  } finally {
    saving.value = false
  }
}

function showError(error: unknown) {
  messageType.value = 'error'
  message.value = error instanceof Error ? error.message : String(error)
}
</script>
