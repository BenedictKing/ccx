<template>
  <div class="site-account-binding">
    <div class="d-flex align-center ga-2 mb-2">
      <v-icon color="primary">mdi-account-link</v-icon>
      <div>
        <div class="text-subtitle-2">{{ t('channelEditor.accountBinding.title') }}</div>
        <div class="text-caption text-medium-emphasis">{{ t('channelEditor.accountBinding.hint') }}</div>
      </div>
    </div>

    <v-progress-linear v-if="loading" indeterminate class="mb-3" />
    <v-alert v-if="!loading && accounts.length === 0 && !boundAccountUid" type="info" variant="tonal" density="compact">
      {{ t('channelEditor.accountBinding.empty') }}
    </v-alert>
    <template v-if="accounts.length > 0 || boundAccountUid">
      <v-row dense>
        <v-col cols="12" md="7">
          <v-select
            v-model="selectedAccountUid"
            :items="accountOptions"
            :label="t('channelEditor.accountBinding.account')"
            variant="outlined"
            density="compact"
            hide-details
          />
        </v-col>
        <v-col cols="12" md="5">
          <v-select
            v-model="selectedGroup"
            :items="groupOptions"
            :label="t('channelEditor.accountBinding.group')"
            :disabled="!selectedAccountUid"
            variant="outlined"
            density="compact"
            hide-details
          />
        </v-col>
      </v-row>
      <div v-if="selectedAccountUid && groupOptions.length === 0" class="text-caption text-warning mt-2">
        {{ t('channelEditor.accountBinding.noGroupKey') }}
      </div>
      <v-alert v-if="message" :type="messageType" variant="tonal" density="compact" class="mt-3">{{ message }}</v-alert>
      <div class="d-flex justify-end ga-2 mt-3">
        <v-btn v-if="boundAccountUid" variant="text" color="warning" :loading="saving" @click="unbind">
          {{ t('channelEditor.accountBinding.unbind') }}
        </v-btn>
        <v-btn color="primary" variant="tonal" :loading="saving" :disabled="!selectedAccountUid || !selectedGroup || isCurrentBinding" @click="bind">
          {{ t('channelEditor.accountBinding.bind') }}
        </v-btn>
      </div>
    </template>
  </div>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from '../../i18n'
import api from '../../services/api'
import type { Channel, SiteAccount } from '../../services/api-types'

const props = defineProps<{ channel: Channel; channelType: string }>()
const emit = defineEmits<{ updated: [] }>()
const { t } = useI18n()

const accounts = ref<SiteAccount[]>([])
const loading = ref(false)
const saving = ref(false)
const boundAccountUid = ref(props.channel.siteAccountUid || '')
const boundGroup = ref(props.channel.siteGroup || '')
const selectedAccountUid = ref(boundAccountUid.value)
const selectedGroup = ref(boundGroup.value)
const message = ref('')
const messageType = ref<'success' | 'error'>('success')

const accountOptions = computed(() => accounts.value.map(account => ({
  title: account.userId ? `${account.baseUrl} · ${account.userId}` : account.baseUrl,
  value: account.uid,
})))
const selectedAccount = computed(() => accounts.value.find(account => account.uid === selectedAccountUid.value))
const groupOptions = computed(() => {
  const groups = new Set((selectedAccount.value?.provisionedKeys || [])
    .filter(key => key.protocol === props.channelType && key.group)
    .map(key => key.group!))
  return [...groups].sort()
})
const isCurrentBinding = computed(() => selectedAccountUid.value === boundAccountUid.value && selectedGroup.value === boundGroup.value)

watch(selectedAccountUid, () => {
  if (!groupOptions.value.includes(selectedGroup.value)) selectedGroup.value = ''
})
watch(() => [props.channel.siteAccountUid, props.channel.siteGroup], () => {
  boundAccountUid.value = props.channel.siteAccountUid || ''
  boundGroup.value = props.channel.siteGroup || ''
  selectedAccountUid.value = boundAccountUid.value
  selectedGroup.value = boundGroup.value
})
watch(() => props.channel.index, loadAccounts, { immediate: true })

async function loadAccounts() {
  loading.value = true
  message.value = ''
  try {
    accounts.value = (await api.listSiteAccounts()).accounts || []
  } catch (error) {
    messageType.value = 'error'
    message.value = error instanceof Error ? error.message : String(error)
  } finally {
    loading.value = false
  }
}

async function bind() {
  if (!selectedAccountUid.value || !selectedGroup.value) return
  if (!await saveBinding(selectedAccountUid.value, selectedGroup.value)) return
  boundAccountUid.value = selectedAccountUid.value
  boundGroup.value = selectedGroup.value
}

async function unbind() {
  if (!await saveBinding('', '')) return
  boundAccountUid.value = ''
  boundGroup.value = ''
  selectedGroup.value = ''
}

async function saveBinding(accountUid: string, group: string): Promise<boolean> {
  saving.value = true
  message.value = ''
  try {
    await api.updateSiteChannelBinding(props.channelType, props.channel.index, { accountUid, group })
    messageType.value = 'success'
    message.value = t(accountUid ? 'channelEditor.accountBinding.bound' : 'channelEditor.accountBinding.unbound')
    emit('updated')
    return true
  } catch (error) {
    messageType.value = 'error'
    message.value = error instanceof Error ? error.message : String(error)
    return false
  } finally {
    saving.value = false
  }
}
</script>
