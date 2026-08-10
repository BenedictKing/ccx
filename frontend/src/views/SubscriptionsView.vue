<template>
  <div class="subscriptions-view">
    <SubscriptionProviderGrid @select="handleProviderSelect" @add="handleProviderAdd" />

    <v-expand-transition>
      <v-card v-if="addProvider" variant="outlined" class="pa-4 mt-6">
        <v-card-title class="text-h6 d-flex align-center"><v-icon color="secondary" class="mr-2">mdi-domain</v-icon>{{ addProvider.displayName }}</v-card-title>
        <v-card-text>
          <div class="text-body-2 text-medium-emphasis mb-4">{{ addProvider.description }}</div>
          <v-text-field v-model="addApiKey" :label="t('subscription.apiKeyLabel')" type="password" variant="outlined" density="compact" />
          <v-alert v-if="addError" color="error" variant="tonal" density="compact" class="mt-3">{{ addError }}</v-alert>
        </v-card-text>
        <v-card-actions><v-spacer /><v-btn variant="text" @click="cancelProviderAdd">{{ t('app.actions.cancel') }}</v-btn><v-btn color="primary" :loading="addSubmitting" :disabled="!addApiKey.trim()" @click="handleProviderAddSubmit">{{ t('app.actions.add') }}</v-btn></v-card-actions>
      </v-card>
    </v-expand-transition>

    <v-expand-transition>
      <div v-if="selectedProvider" class="mt-6">
        <v-card v-if="selectedProvider === 'github-copilot'" variant="outlined" class="pa-4">
          <v-card-title class="text-h6"><v-icon color="primary" class="mr-2">mdi-github</v-icon>GitHub Copilot</v-card-title>
          <v-card-text><v-alert color="info" variant="tonal">{{ t('subscription.copilotComingSoon') }}</v-alert></v-card-text>
        </v-card>
        <v-card v-if="selectedProvider === 'new-api'" variant="outlined" class="pa-4">
          <v-card-title class="text-h6"><v-icon color="warning" class="mr-2">mdi-server-network</v-icon>{{ t('subscription.newApi.connect') }}</v-card-title>
          <v-card-text><NewApiSubscriptionForm @created="handleNewApiCreated" @error="emit('error', $event)" /></v-card-text>
        </v-card>
      </div>
    </v-expand-transition>

    <v-card variant="outlined" class="mt-6">
      <v-card-title class="d-flex align-center justify-space-between ga-2 flex-wrap">
        <span>{{ t('subscription.managementTitle') }}</span>
        <v-btn size="small" variant="text" :loading="loading" @click="loadSubscriptions">{{ t('app.actions.refresh') }}</v-btn>
      </v-card-title>
      <v-card-text>
        <v-alert v-if="loadError" color="error" variant="tonal" density="compact" class="mb-3">{{ loadError }}</v-alert>
        <SubscriptionPlanTable v-if="!loading && subscriptions.length > 0" :subscriptions="subscriptions" @edit="openBillingEditor" @refresh="refreshItem" @delete="deleteItem" @link="openLinkDialog" />
        <EmptyState
          v-else-if="!loading && subscriptions.length === 0"
          icon="mdi-cash-multiple"
          :title="t('subscription.empty.title')"
          :description="t('subscription.empty.description')"
        />
      </v-card-text>
    </v-card>

    <ExchangeRateManager />

    <v-dialog v-model="billingDialog" max-width="560">
      <v-card>
        <v-card-title>{{ t('subscription.billingTerms.title') }}</v-card-title>
        <v-card-text>
          <div class="text-body-2 mb-3">{{ billingItem?.displayName }}</div>
          <v-text-field v-model.number="billingForm.paymentAmount" type="number" min="0" step="any" :label="t('subscription.billingTerms.paymentAmount')" variant="outlined" />
          <v-text-field v-model="billingForm.paymentUnit" :label="t('subscription.billingTerms.paymentUnit')" variant="outlined" />
          <v-text-field v-model.number="billingForm.creditAmount" type="number" min="0" step="any" :label="t('subscription.billingTerms.creditAmount')" variant="outlined" />
          <v-text-field v-model="billingForm.creditUnit" :label="t('subscription.billingTerms.creditUnit')" variant="outlined" />
          <v-alert v-if="billingError" color="error" variant="tonal" density="compact">{{ billingError }}</v-alert>
        </v-card-text>
        <v-card-actions><v-btn color="error" variant="text" @click="resetBillingTerms">{{ t('subscription.billingTerms.reset') }}</v-btn><v-spacer /><v-btn variant="text" @click="billingDialog = false">{{ t('app.actions.cancel') }}</v-btn><v-btn color="primary" :loading="billingSaving" @click="saveBillingTerms">{{ t('app.actions.save') }}</v-btn></v-card-actions>
      </v-card>
    </v-dialog>

    <v-dialog v-model="linkDialog" max-width="560">
      <v-card>
        <v-card-title>{{ t('subscription.linkChannel.title') }}</v-card-title>
        <v-card-text>
          <div class="text-body-2 mb-3">{{ t('subscription.linkChannel.description', { name: linkItem?.displayName || '' }) }}</div>
          <div v-if="linkableChannels.length === 0" class="text-body-2 text-medium-emphasis">{{ t('subscription.linkChannel.empty') }}</div>
          <v-select
            v-else
            v-model="linkSelectedChannelUid"
            :items="linkableChannels"
            item-title="label"
            item-value="channelUid"
            :label="t('subscription.linkChannel.placeholder')"
            variant="outlined"
            density="compact"
            :loading="linkChannelsLoading"
            :disabled="linkChannelsLoading || linkSubmitting"
          />
          <v-alert v-if="linkError" color="error" variant="tonal" density="compact" class="mt-3">{{ linkError }}</v-alert>
          <v-divider v-if="linkItem?.linkedChannelUids?.length" class="my-4" />
          <div v-if="linkItem?.linkedChannelUids?.length" class="d-flex flex-column ga-2">
            <div class="text-subtitle-2">{{ t('subscription.field.linkedChannels') }}</div>
            <div v-for="ch in linkItem.linkedChannelUids" :key="ch" class="d-flex align-center justify-space-between">
              <v-chip size="small" variant="outlined" color="primary">{{ ch }}</v-chip>
              <v-btn size="small" variant="text" color="error" :loading="unlinkLoading === ch" @click="unlinkChannel(ch)">
                <v-icon size="18" class="mr-1">mdi-link-off</v-icon>{{ t('subscription.unlinkChannel') }}
              </v-btn>
            </div>
          </div>
        </v-card-text>
        <v-card-actions>
          <v-spacer />
          <v-btn variant="text" :disabled="linkSubmitting" @click="linkDialog = false">{{ t('app.actions.cancel') }}</v-btn>
          <v-btn color="primary" :loading="linkSubmitting" :disabled="!linkSelectedChannelUid" @click="linkChannel">{{ t('subscription.linkChannel') }}</v-btn>
        </v-card-actions>
      </v-card>
    </v-dialog>

    <v-dialog v-model="syncDialog" max-width="760"><v-card><v-card-title>{{ t('subscription.sync.title') }}</v-card-title><v-card-text><v-table density="compact"><thead><tr><th>{{ t('subscription.sync.group') }}</th><th>{{ t('subscription.sync.multiplier') }}</th><th>{{ t('subscription.sync.status') }}</th><th>{{ t('subscription.sync.updated') }}</th><th>{{ t('subscription.sync.reason') }}</th></tr></thead><tbody><tr v-for="key in syncResult?.keys || []" :key="key.keyUid || key.sourceRemoteTokenId"><td>{{ key.group }}</td><td>{{ key.groupMultiplier }} / {{ key.maxGroupMultiplier }}</td><td><v-chip size="x-small" :color="key.syncStatus === 'fresh' ? 'success' : 'warning'">{{ multiplierStatusLabel(key.syncStatus) }}</v-chip></td><td>{{ formatSyncTimes(key.updatedAt, key.multiplierExpiresAt) }}</td><td>{{ key.reason || '-' }}</td></tr></tbody></v-table></v-card-text><v-card-actions><v-spacer /><v-btn @click="syncDialog = false">{{ t('app.actions.close') }}</v-btn></v-card-actions></v-card></v-dialog>
  </div>
</template>

<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from '@/i18n'
import { api, ApiError } from '@/services/api'
import { useDialogStore } from '@/stores/dialog'
import SubscriptionProviderGrid from '@/components/subscriptions/SubscriptionProviderGrid.vue'
import NewApiSubscriptionForm from '@/components/NewApiSubscriptionForm.vue'
import SubscriptionPlanTable from '@/components/SubscriptionPlanTable.vue'
import ExchangeRateManager from '@/components/subscriptions/ExchangeRateManager.vue'
import EmptyState from '@/components/EmptyState.vue'
import { autoAddChannel, extractAutoAddErrorMessage, getProviderTemplates, type ProviderTemplate } from '@/services/autopilot-api'
import type { Channel, NewApiProvisionResponse, NewApiSyncResult, SubscriptionItem } from '@/services/api-types'
import { billingTermsPatch, multiplierStatusLabel } from '@/utils/subscriptionBilling'

const { t } = useI18n()
const dialogStore = useDialogStore()
const emit = defineEmits<{
  success: [message: string]
  error: [message: string]
}>()
const selectedProvider = ref('')
const addProvider = ref<ProviderTemplate | null>(null)
const addApiKey = ref('')
const addSubmitting = ref(false)
const addError = ref('')
const subscriptions = ref<SubscriptionItem[]>([])
const loading = ref(false)
const loadError = ref('')
const billingDialog = ref(false)
const billingSaving = ref(false)
const billingError = ref('')
const billingItem = ref<SubscriptionItem | null>(null)
const billingForm = ref({ paymentAmount: null as number | null, paymentUnit: '', creditAmount: null as number | null, creditUnit: '' })
const syncDialog = ref(false)
const syncResult = ref<NewApiSyncResult | null>(null)
const linkDialog = ref(false)
const linkSubmitting = ref(false)
const linkChannelsLoading = ref(false)
const linkError = ref('')
const linkItem = ref<SubscriptionItem | null>(null)
const linkSelectedChannelUid = ref('')
const linkableChannels = ref<{ channelUid: string; label: string }[]>([])
const unlinkLoading = ref('')

function handleProviderSelect(provider: string) { selectedProvider.value = provider; cancelProviderAdd() }
async function handleProviderAdd(providerId: string) { selectedProvider.value = ''; addError.value = ''; const templates = await getProviderTemplates(); addProvider.value = templates.find(item => item.providerId === providerId) || null }
function cancelProviderAdd() { addProvider.value = null; addApiKey.value = ''; addError.value = '' }
async function handleProviderAddSubmit() { const provider = addProvider.value; if (!provider || !addApiKey.value.trim()) return; addSubmitting.value = true; try { const kind = provider.channelKind || provider.routes?.[0]?.channelKind || 'messages'; await autoAddChannel(kind, { providerId: provider.providerId, apiKeys: [addApiKey.value.trim()] }); emit('success', t('subscription.addProviderSuccess', { name: provider.displayName })); cancelProviderAdd() } catch (error) { addError.value = extractAutoAddErrorMessage(error) } finally { addSubmitting.value = false } }
function handleNewApiCreated(_result: NewApiProvisionResponse) { selectedProvider.value = ''; emit('success', t('subscription.newApi.provisionSuccess')); void loadSubscriptions() }
async function loadSubscriptions() { loading.value = true; loadError.value = ''; try { subscriptions.value = (await api.getSubscriptions()).subscriptions } catch (error) { loadError.value = error instanceof Error ? error.message : String(error) } finally { loading.value = false } }
function openBillingEditor(item: SubscriptionItem) { billingItem.value = item; billingForm.value = { paymentAmount: item.paymentAmount ?? null, paymentUnit: item.paymentUnit || '', creditAmount: item.creditAmount ?? null, creditUnit: item.creditUnit || '' }; billingError.value = ''; billingDialog.value = true }
async function saveBillingTerms() { if (!billingItem.value) return; billingSaving.value = true; billingError.value = ''; try { await api.patchSubscriptionBillingTerms(billingItem.value.subscriptionUid, billingTermsPatch(billingForm.value, billingItem.value.version)); billingDialog.value = false; await loadSubscriptions() } catch (error) { if (error instanceof ApiError && error.status === 409) { billingError.value = t('subscription.billingTerms.versionConflict'); await loadSubscriptions() } else billingError.value = error instanceof Error ? error.message : String(error) } finally { billingSaving.value = false } }
function resetBillingTerms() { billingForm.value = { paymentAmount: null, paymentUnit: '', creditAmount: null, creditUnit: '' }; void saveBillingTerms() }
async function refreshItem(item: SubscriptionItem) { try { const response = await api.refreshSubscription(item.subscriptionUid); const result = response.refreshResult as NewApiSyncResult; if (Array.isArray(result.keys)) { syncResult.value = result; syncDialog.value = true } if (result.success) { emit('success', t('subscription.refreshSuccess')) } else { emit('error', result.failedReason || t('subscription.refreshFailed')) } await loadSubscriptions() } catch (error) { emit('error', error instanceof Error ? error.message : String(error)) } }
async function deleteItem(item: SubscriptionItem) { const confirmed = await dialogStore.confirm({ message: t('subscription.deleteConfirm', { name: item.displayName }) }); if (!confirmed) return; try { await api.deleteSubscription(item.subscriptionUid); emit('success', t('subscription.deleteSuccess', { name: item.displayName })); await loadSubscriptions() } catch (error) { emit('error', error instanceof Error ? error.message : String(error)) } }
function formatSyncTimes(updated?: string, expires?: string) { return `${updated ? new Date(updated).toLocaleString() : '-'} / ${expires ? new Date(expires).toLocaleString() : '-'}` }

function openLinkDialog(item: SubscriptionItem) {
  linkItem.value = item
  linkSelectedChannelUid.value = ''
  linkError.value = ''
  linkDialog.value = true
  void loadLinkableChannels(item)
}

async function loadLinkableChannels(item: SubscriptionItem) {
  linkChannelsLoading.value = true
  linkableChannels.value = []
  try {
    const response = await api.getChannels()
    const linked = new Set(item.linkedChannelUids || [])
    linkableChannels.value = response.channels
      .filter((ch: Channel) => ch.channelUid && !linked.has(ch.channelUid))
      .map((ch: Channel) => ({
        channelUid: ch.channelUid as string,
        label: `${ch.name} (${ch.channelUid})`,
      }))
  } catch (error) {
    linkError.value = error instanceof Error ? error.message : String(error)
  } finally {
    linkChannelsLoading.value = false
  }
}

async function linkChannel() {
  if (!linkItem.value || !linkSelectedChannelUid.value) return
  linkSubmitting.value = true
  linkError.value = ''
  try {
    await api.linkSubscriptionChannel(linkItem.value.subscriptionUid, linkSelectedChannelUid.value)
    emit('success', t('subscription.linkSuccess', { channel: linkSelectedChannelUid.value, name: linkItem.value.displayName }))
    linkDialog.value = false
    await loadSubscriptions()
  } catch (error) {
    linkError.value = error instanceof Error ? error.message : String(error)
  } finally {
    linkSubmitting.value = false
  }
}

async function unlinkChannel(channelUid: string) {
  if (!linkItem.value) return
  const confirmed = await dialogStore.confirm({ message: t('subscription.unlinkConfirm', { channel: channelUid }) })
  if (!confirmed) return
  unlinkLoading.value = channelUid
  try {
    await api.unlinkSubscriptionChannel(linkItem.value.subscriptionUid, channelUid)
    emit('success', t('subscription.unlinkSuccess', { channel: channelUid, name: linkItem.value.displayName }))
    await loadSubscriptions()
    if (linkItem.value) {
      const updated = subscriptions.value.find(s => s.subscriptionUid === linkItem.value?.subscriptionUid)
      if (updated) {
        linkItem.value = updated
        void loadLinkableChannels(updated)
      }
    }
  } catch (error) {
    emit('error', error instanceof Error ? error.message : String(error))
  } finally {
    unlinkLoading.value = ''
  }
}

onMounted(loadSubscriptions)
</script>

<style scoped>.subscriptions-view { padding: 16px; }</style>
