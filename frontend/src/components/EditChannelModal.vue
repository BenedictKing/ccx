<template>
  <v-dialog :model-value="show" max-width="1200" persistent scrollable @update:model-value="$emit('update:show', $event)">
    <v-card rounded="lg" class="add-channel-dialog channel-editor-dialog">
      <!-- 头部 -->
      <AddChannelHeader
        :is-editing="isEditing"
        :channel-type="props.channelType"
        :channel-name="isManagedProvider ? '' : form.name"
        :channel-name-hint="''"
        :identity-name="headerIdentityName"
        :identity-label="managedIdentityName ? t('channelEditor.managed.providerLabel') : t('channelEditor.basic.name.label')"
        :identity-icon="managedIdentityName ? 'mdi-domain' : 'mdi-tag'"
        :identity-badge="headerIdentityBadge"
        :hide-capability-actions="true"
        :no-vision="form.noVision"
        :header-classes="headerClasses"
        :avatar-color="avatarColor"
        :header-icon-style="headerIconStyle"
        :subtitle-classes="subtitleClasses"
        :edit-title="t('addChannel.editTitle')"
        :create-title="t('addChannel.createTitle')"
        :edit-subtitle="isManagedProvider
          ? t(isOfficialManagedProvider
            ? 'channelEditor.managed.editSubtitle'
            : 'channelEditor.managed.providerEditSubtitle', { provider: managedProviderName })
          : t('channelEditor.managed.customEditSubtitle')"
        :create-subtitle="t('addChannel.quickSubtitle')"
        :vision-tooltip="form.noVision ? t('channelCard.noVision') : t('channelCard.hasVision')"
        @toggle-no-vision="form.noVision = !form.noVision"
      />

      <!-- 主体内容 -->
      <v-card-text class="pa-0 channel-editor-body">
        <!-- 左侧导航 + 右侧面板 -->
        <div class="content-row">
          <!-- 左侧垂直导航 -->
          <AddChannelSidebarNav
            :title="t('addChannel.outline')"
            :sections="sections"
            :active-section="activeSection"
            @navigate="scrollToSection"
          />

          <!-- 右侧内容面板 -->
          <v-form ref="formRef" class="content-area" @submit.prevent="handleSubmit">
            <!-- 基本信息 -->
            <section :ref="(el: any) => setSectionRef('basic', el)" data-section-id="basic" class="pa-6 scroll-mt-4">
              <!-- hide-base-url 对官方直连与 Provider 模板托管渠道生效：其地址由官方/模板固定；
                   仅自定义手填地址的自动托管账号需要手工维护 CDN 地址池 -->
              <BasicInfoSection
                :form="form"
                :base-urls-text="baseUrlsText"
                :expected-request-urls="expectedRequestUrls"
                :base-url-has-error="baseUrlHasError"
                :service-type-options="serviceTypeOptions"
                :hide-service-type="true"
                :hide-base-url="isManagedProvider || isOfficialManagedProvider"
                :hide-metadata="true"
                :managed-account="true"
                :provider-name="managedProviderName"
                :website-links="managedProviderWebsiteLinks"
                :errors="errors"
                :rules="rules"
                @update:form="updateForm"
                @update:base-urls-text="baseUrlsText = $event"
                @menu-update="onMenuUpdate"
              />
              <ProtocolModelAvailability :routes="protocolModelRoutes" :loading="managedModelsLoading" @refreshed="handleProtocolModelsRefreshed" />
            </section>

            <!-- 身份认证 -->
            <section :ref="(el: any) => setSectionRef('auth', el)" data-section-id="auth" class="pa-6 scroll-mt-4">
              <ApiKeyManagementSection
                :api-keys="form.apiKeys"
                :disabled-keys="visibleDisabledKeys"
                :disabled-key-models="visibleDisabledKeyModels"
                :disabled-group-models="visibleDisabledGroupModels"
                :model-options="targetModelOptions"
                :api-key-configs="form.apiKeyConfigs"
                :key-models-status="keyModelsStatus"
                :is-editing="isEditing"
                :restoring-key="restoringKey"
                :restoring-key-model="restoringKeyModel"
                :changing-group-model="changingGroupModel"
                :removing-key="removingKey"
                :suspending-key="suspendingKey"
                :service-type="form.serviceType"
                :channel-id="props.channel?.index"
                :channel-uid="props.channel?.channelUid"
                :channel-kind="props.channelType"
                :dialog-open="props.show"
                :proxy-url="form.proxyUrl"
                :account-uid="props.channel?.accountUid"
                :provider-id="props.channel?.providerId"
                @update:api-keys="form.apiKeys = $event"
                @update:api-key-configs="form.apiKeyConfigs = $event"
                @update:proxy-url="form.proxyUrl = $event"
                @restore-key="restoreDisabledKey"
                @restore-key-model="restoreDisabledKeyModel"
                @disable-group-model="disableGroupModel"
                @restore-group-model="restoreDisabledGroupModel"
                @remove-key="removeDisabledKey"
                @suspend-key="suspendKey"
                @resume-key="resumeKey"
                @ensure-models-loaded="ensureTargetModelsLoaded"
              />
            </section>

            <!-- new-api 账号管理 -->
            <section
              v-if="isNewApiChannel || isGenericAutoManagedChannel"
              :ref="(el: any) => setSectionRef('accounts', el)"
              data-section-id="accounts"
              class="pa-6 scroll-mt-4"
            >
              <NewApiAccountPanel
                :subscription-uid="props.channel?.subscriptionUid || ''"
                :channel-name="props.channel?.name"
                :base-url="props.channel?.baseUrl"
                :channel-uid="props.channel?.channelUid"
                :channel-kind="props.channelType"
                :is-generic="isGenericAutoManagedChannel"
                :auto-managed-kind="props.channel?.autoManagedKind"
                @updated="handleAccountsUpdated"
              />
            </section>

            <!-- 自定义参数（代理服务器 + 自定义请求头 + 充值倍率/汇率） -->
            <section :ref="(el: any) => setSectionRef('custom', el)" data-section-id="custom" class="pa-6 scroll-mt-4">
              <!-- 代理服务器 -->
              <v-text-field
                :model-value="form.proxyUrl"
                :label="t('channelEditor.transport.proxyUrl.label')"
                :placeholder="t('channelEditor.transport.proxyUrl.placeholder')"
                :hint="t('channelEditor.transport.proxyUrl.hint')"
                persistent-hint
                prepend-inner-icon="mdi-vpn"
                variant="outlined"
                density="comfortable"
                clearable
                @update:model-value="updateForm({ proxyUrl: $event ?? '' })"
              />

              <div class="mt-6">
                <CustomHeadersSection
                  :headers="customHeadersArray"
                  @update:headers="updateCustomHeaders"
                />
              </div>

              <!-- 渠道级计费：充值币种/金额 + 渠道币种/到账金额 -->
              <div class="mt-6">
                <div class="text-subtitle-2 font-weight-medium mb-1">{{ t('channelEditor.billing.title') }}</div>
                <div class="text-caption text-medium-emphasis mb-3">{{ t('channelEditor.billing.hint') }}</div>
                <v-row dense>
                  <v-col cols="12" sm="6">
                    <v-text-field
                      :model-value="form.channelPaymentCurrency"
                      :label="t('channelEditor.billing.paymentCurrency.label')"
                      :hint="t('channelEditor.billing.paymentCurrency.hint')"
                      persistent-hint
                      prepend-inner-icon="mdi-cash"
                      variant="outlined"
                      density="comfortable"
                      placeholder="LDC / CNY / USD"
                      clearable
                      @update:model-value="updateForm({ channelPaymentCurrency: $event ?? '' })"
                    />
                  </v-col>
                  <v-col cols="12" sm="6">
                    <v-text-field
                      :model-value="form.channelPaymentAmount"
                      :label="t('channelEditor.billing.paymentAmount.label')"
                      :hint="t('channelEditor.billing.paymentAmount.hint')"
                      persistent-hint
                      prepend-inner-icon="mdi-cash-multiple"
                      variant="outlined"
                      density="comfortable"
                      type="number"
                      step="0.01"
                      min="0"
                      clearable
                      @update:model-value="updateForm({ channelPaymentAmount: $event })"
                    />
                  </v-col>
                  <v-col cols="12" sm="6">
                    <v-text-field
                      :model-value="form.channelCreditCurrency"
                      :label="t('channelEditor.billing.creditCurrency.label')"
                      :hint="t('channelEditor.billing.creditCurrency.hint')"
                      persistent-hint
                      prepend-inner-icon="mdi-currency-usd"
                      variant="outlined"
                      density="comfortable"
                      placeholder="USD"
                      clearable
                      @update:model-value="updateForm({ channelCreditCurrency: $event ?? '' })"
                    />
                  </v-col>
                  <v-col cols="12" sm="6">
                    <v-text-field
                      :model-value="form.channelCreditAmount"
                      :label="t('channelEditor.billing.creditAmount.label')"
                      :hint="t('channelEditor.billing.creditAmount.hint')"
                      persistent-hint
                      prepend-inner-icon="mdi-cash-check"
                      variant="outlined"
                      density="comfortable"
                      type="number"
                      step="0.01"
                      min="0"
                      clearable
                      @update:model-value="updateForm({ channelCreditAmount: $event })"
                    />
                  </v-col>
                </v-row>
                <div class="text-caption text-medium-emphasis mt-1">{{ t('channelEditor.billing.example') }}</div>
              </div>
            </section>
          </v-form>
        </div>
      </v-card-text>

      <!-- 底部按钮 -->
      <v-card-actions class="pa-6 pt-2">
        <v-spacer />
        <v-btn variant="outlined" :disabled="submitting" @click="handleCancel">
          {{ t('app.actions.cancel') }}<span class="shortcut-hint ml-2 text-xs opacity-50">Esc</span>
        </v-btn>
        <v-btn
          color="primary"
          variant="elevated"
          :disabled="!isFormValid || submitting"
          :loading="submitting"
          @click="handleSubmit"
        >
          {{ t('app.actions.save') }}<span class="shortcut-hint ml-2 text-xs opacity-50">{{ isMac ? '⌘Enter' : 'Ctrl+Enter' }}</span>
        </v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'

// 子组件导入
import AddChannelHeader from './edit-channel/AddChannelHeader.vue'
import AddChannelSidebarNav from './edit-channel/AddChannelSidebarNav.vue'
import BasicInfoSection from './edit-channel/BasicInfoSection.vue'
import ProtocolModelAvailability from './edit-channel/ProtocolModelAvailability.vue'
import ApiKeyManagementSection from './edit-channel/ApiKeyManagementSection.vue'
import CustomHeadersSection from './edit-channel/CustomHeadersSection.vue'
import NewApiAccountPanel from './edit-channel/NewApiAccountPanel.vue'
import { useEditChannelModal, type EditChannelModalEmits, type EditChannelModalProps } from '../composables/useEditChannelModal'
import { ApiService } from '../services/api'
import type { ManagedAccountChannel } from '../services/api-types'
import { buildNativeProtocolModelRoutes, loadLegacyManagedModelAvailability } from '../utils/channelModelAvailability'
import { getManagedProviderWebsiteLinks } from '../utils/channelWebsite'
import { isManagedProviderChannel, isOfficialProviderChannel, managedProviderChannelName, providerDisplayName } from '../utils/providerDisplay'
import { useChannelStore } from '../stores/channel'
import { useDialogStore } from '../stores/dialog'

const props = withDefaults(defineProps<EditChannelModalProps>(), {
  channelType: 'messages',
})

const emit = defineEmits<EditChannelModalEmits>()
const channelStore = useChannelStore()
const dialogStore = useDialogStore()
const managedProviderName = computed(() => providerDisplayName(props.channel?.providerId))
const isManagedProvider = computed(() => isManagedProviderChannel(props.channel))
const isOfficialManagedProvider = computed(() => isOfficialProviderChannel(props.channel))
const managedProviderWebsiteLinks = computed(() => props.channel ? getManagedProviderWebsiteLinks(props.channel) : [])
const managedAccountChannels = ref<ManagedAccountChannel[]>([])
const managedModelsLoading = ref(false)
const managedAccountsApi = new ApiService()
let managedModelsRequestId = 0

const protocolModelRoutes = computed(() => {
  const routes = props.channel?.protocolRoutes ?? []
  if (!props.channel?.autoManaged || !props.channel.accountUid) return routes
  return buildNativeProtocolModelRoutes(routes, managedAccountChannels.value)
})

const reloadManagedModels = async (accountUid: string) => {
  const requestId = ++managedModelsRequestId
  managedModelsLoading.value = true
  try {
    let accountChannels: ManagedAccountChannel[] = []
    try {
      const response = await managedAccountsApi.getManagedAccounts()
      accountChannels = response.accounts.find(account => account.accountUid === accountUid)?.channels ?? []
    } catch {
      // 旧后端或账号接口暂时失败时，继续尝试渠道 models API。
    }
    if (requestId !== managedModelsRequestId) return
    accountChannels = await loadLegacyManagedModelAvailability(
      managedAccountsApi,
      props.channel?.protocolRoutes,
      accountChannels,
    )
    if (requestId === managedModelsRequestId) managedAccountChannels.value = accountChannels
  } finally {
    if (requestId === managedModelsRequestId) managedModelsLoading.value = false
  }
}

const handleProtocolModelsRefreshed = () => {
  const accountUid = props.channel?.accountUid
  if (accountUid) void reloadManagedModels(accountUid)
  // 重新发现可能已为该账号补建了缺失协议的渠道（如 chat/gemini），
  // 但 props.channel 是打开弹窗时的静态快照，protocolRoutes 不会自动更新。
  // 这里重新拉取渠道列表，并用刷新后的最新渠道对象替换 editingChannel 快照，
  // 让 protocolRoutes 反映新落地的路由，"未配置路由" 提示才能正确消失。
  void refreshEditingChannelAfterRediscovery()
}

const refreshEditingChannelAfterRediscovery = async () => {
  const accountUid = props.channel?.accountUid
  if (!accountUid) return
  try {
    await channelStore.refreshChannels()
  } catch {
    // 刷新失败时保留旧快照，不阻断后续操作。
    return
  }
  if (!props.show) return
  const latest = channelStore.unifiedLlmChannelsData.channels.find(
    channel => channel.accountUid === accountUid,
  )
  if (latest) dialogStore.editingChannel = latest
}

watch(
  [() => props.show, () => props.channel?.accountUid],
  async ([show, accountUid]) => {
    managedModelsRequestId++
    managedAccountChannels.value = []
    managedModelsLoading.value = false
    if (!show || !accountUid) return
    await reloadManagedModels(accountUid)
  },
  { immediate: true },
)

const {
  formRef,
  activeSection,
  sections,
  baseUrlHasError,
  onMenuUpdate,
  serviceTypeOptions,
  form,
  baseUrlsText,
  keyModelsStatus,
  errors,
  rules,
  isEditing,
  isMac,
  targetModelOptions,
  headerClasses,
  avatarColor,
  headerIconStyle,
  subtitleClasses,
  isFormValid,
  restoringKey,
  submitting,
  visibleDisabledKeys,
  expectedRequestUrls,
  customHeadersArray,
  updateCustomHeaders,
  restoreDisabledKey,
  removingKey,
  removeDisabledKey,
  restoringKeyModel,
  visibleDisabledKeyModels,
  restoreDisabledKeyModel,
  changingGroupModel,
  visibleDisabledGroupModels,
  disableGroupModel,
  restoreDisabledGroupModel,
  suspendingKey,
  suspendKey,
  resumeKey,
  ensureTargetModelsLoaded,
  updateForm,
  handleSubmit,
  handleCancel,
  scrollToSection,
  setSectionRef,
  t,
} = useEditChannelModal(props, emit)

// 托管渠道的头部身份名：友好名（如"Kimi 官方渠道"），非托管渠道为空串
const managedIdentityName = computed(() => managedProviderChannelName(props.channel, t))

// 头部身份块：编辑态下所有渠道统一使用——托管渠道用友好名+徽章，自定义渠道用渠道名
const headerIdentityName = computed(() => (props.channel ? managedIdentityName.value || form.name : ''))
const headerIdentityBadge = computed((): 'official' | 'managed' | '' => {
  if (!managedIdentityName.value) return ''
  return isOfficialManagedProvider.value ? 'official' : 'managed'
})

// generic 自动托管渠道：autoManaged=true、无 providerId，但尚未绑定 new-api
const isGenericAutoManagedChannel = computed(() =>
  !!props.channel?.autoManaged && !props.channel?.providerId && props.channel?.autoManagedKind !== 'new_api'
)

// new-api 绑定渠道：可通过 autoManagedKind 显式标记，或沿用 originType=relay 向后兼容
const isNewApiChannel = computed(() =>
  props.channel?.autoManagedKind === 'new_api' ||
  (props.channel?.originType === 'relay' && props.channel?.autoManaged && !props.channel?.providerId)
)
const handleAccountsUpdated = () => {
  emit('updated')
}
</script>

<style scoped src="./edit-channel/edit-channel-modal.css"></style>
