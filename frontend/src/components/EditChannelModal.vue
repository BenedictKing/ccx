<template>
  <v-dialog :model-value="show" max-width="1200" persistent scrollable @update:model-value="$emit('update:show', $event)">
    <v-card rounded="lg" class="add-channel-dialog channel-editor-dialog">
      <!-- 头部 -->
      <AddChannelHeader
        :is-editing="isEditing"
        :channel-type="props.channelType"
        :channel-name="form.name"
        :channel-name-hint="isAutoManagedChannel ? '' : t('channelEditor.basic.name.autoDerivedHint')"
        :hide-capability-actions="isAutoManagedChannel"
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
          : isAutoManagedChannel
            ? t('channelEditor.managed.customEditSubtitle')
            : t('addChannel.editSubtitle')"
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
              <!-- hide-base-url 仅对 Provider 模板托管渠道生效：其地址由模板与自动发现维护；
                   自定义自动托管账号需要手工维护 CDN 地址池，因此保留多行 Base URL 输入 -->
              <BasicInfoSection
                :form="form"
                :base-urls-text="baseUrlsText"
                :expected-request-urls="expectedRequestUrls"
                :base-url-has-error="baseUrlHasError"
                :service-type-options="serviceTypeOptions"
                :hide-service-type="isAutoManagedChannel"
                :hide-base-url="isManagedProvider"
                :hide-metadata="isAutoManagedChannel"
                :managed-account="isAutoManagedChannel"
                :provider-name="managedProviderName"
                :official-provider="isOfficialManagedProvider"
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
                :is-auto-managed="isAutoManagedChannel"
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

            <!-- 模型重定向（模型映射 + Vision 回退 + 模型过滤） -->
            <section v-if="!isAutoManagedChannel" :ref="(el: any) => setSectionRef('redirect', el)" data-section-id="redirect" class="pa-6 scroll-mt-4">
              <v-alert
                v-if="supportsChannelDiscovery"
                class="mb-4"
                variant="tonal"
                color="info"
                density="comfortable"
                rounded="lg"
              >
                <div class="d-flex align-center justify-space-between ga-3 flex-wrap">
                  <div class="d-flex align-center ga-2">
                    <v-icon color="info">mdi-auto-fix</v-icon>
                    <div>
                      <div class="text-subtitle-2 font-weight-medium">{{ t('channelDiscovery.title') }}</div>
                      <div class="text-caption text-medium-emphasis">{{ t('channelDiscovery.hint') }}</div>
                    </div>
                  </div>
                  <v-btn
                    color="info"
                    variant="tonal"
                    size="small"
                    :loading="discoveringChannelConfig"
                    @click="handleDiscoverChannelConfig"
                  >
                    <v-icon start size="small">mdi-radar</v-icon>
                    {{ t('channelDiscovery.button') }}
                  </v-btn>
                </div>

                <v-alert v-if="channelDiscoveryError" class="mt-3" type="error" variant="tonal" density="compact">
                  {{ channelDiscoveryError }}
                </v-alert>

                <v-alert
                  v-if="discoveringChannelConfig && !channelDiscoveryResult"
                  class="mt-3"
                  type="info"
                  variant="tonal"
                  density="compact"
                >
                  {{ t('channelDiscovery.running') }}
                </v-alert>

                <div v-if="channelDiscoveryResult" class="mt-3 d-flex flex-column ga-3">
                  <div class="d-flex align-center ga-2 flex-wrap">
                    <v-chip size="small" color="primary" variant="tonal">
                      {{ t('channelDiscovery.recommendedKind', { kind: channelDiscoveryResult.recommendation.channelKind || '-' }) }}
                    </v-chip>
                    <v-chip size="small" color="success" variant="tonal">
                      {{ t('channelDiscovery.modelsFound', { count: channelDiscoveryResult.models.items.length }) }}
                    </v-chip>
                    <v-chip
                      v-for="protocol in channelDiscoverySuccessfulProtocols"
                      :key="protocol.protocol"
                      size="small"
                      color="success"
                      variant="tonal"
                    >
                      {{ protocol.protocol }} {{ protocol.successModels?.length || 0 }}
                    </v-chip>
                  </div>

                  <div v-if="channelDiscoveryModelMappingEntries.length" class="d-flex flex-column ga-1">
                    <div class="text-caption font-weight-medium">{{ t('channelDiscovery.mapping') }}</div>
                    <div class="d-flex align-center ga-1 flex-wrap">
                      <v-chip
                        v-for="[source, target] in channelDiscoveryModelMappingEntries"
                        :key="source"
                        size="small"
                        color="primary"
                        variant="tonal"
                      >
                        {{ source }} → {{ target }}
                      </v-chip>
                    </div>
                  </div>

                  <div v-if="channelDiscoveryReasoningEntries.length" class="d-flex flex-column ga-1">
                    <div class="text-caption font-weight-medium">{{ t('channelDiscovery.reasoning') }}</div>
                    <div class="d-flex align-center ga-1 flex-wrap">
                      <v-chip
                        v-for="[source, effort] in channelDiscoveryReasoningEntries"
                        :key="source"
                        size="small"
                        color="secondary"
                        variant="outlined"
                      >
                        {{ source }}={{ effort }}
                      </v-chip>
                    </div>
                    <div class="text-caption text-medium-emphasis">{{ t('channelDiscovery.reasoningNote') }}</div>
                  </div>

                  <div v-if="channelDiscoveryCompatEntries.length" class="d-flex flex-column ga-1">
                    <div class="text-caption font-weight-medium">{{ t('channelDiscovery.compat') }}</div>
                    <div class="d-flex align-center ga-1 flex-wrap">
                      <v-chip
                        v-for="[key, value] in channelDiscoveryCompatEntries"
                        :key="key"
                        size="small"
                        :color="value ? 'warning' : 'grey'"
                        variant="tonal"
                      >
                        {{ key }}={{ value }}
                      </v-chip>
                    </div>
                  </div>

                  <div v-if="channelDiscoveryResult.models.warnings?.length" class="text-caption text-warning">
                    {{ channelDiscoveryResult.models.warnings.join(' / ') }}
                  </div>

                  <div v-if="channelDiscoveryCapabilityEntries.length" class="d-flex flex-column ga-1">
                    <div class="text-caption font-weight-medium">{{ t('channelDiscovery.capabilities') }}</div>
                    <div class="d-flex align-center ga-1 flex-wrap">
                      <v-tooltip
                        v-for="capability in channelDiscoveryCapabilityEntries"
                        :key="capability.key"
                        :text="capability.detail"
                        location="top"
                        :open-delay="150"
                      >
                        <template #activator="{ props: tooltipProps }">
                          <v-chip
                            v-bind="tooltipProps"
                            size="small"
                            :color="capability.color"
                            variant="tonal"
                          >
                            {{ capability.label }}={{ capability.text }}
                          </v-chip>
                        </template>
                      </v-tooltip>
                    </div>
                  </div>

                  <div class="d-flex justify-end">
                    <v-btn
                      color="primary"
                      variant="elevated"
                      size="small"
                      :disabled="!channelDiscoveryResult"
                      @click="applyChannelDiscoveryRecommendation"
                    >
                      <v-icon start size="small">mdi-check</v-icon>
                      {{ t('channelDiscovery.apply') }}
                    </v-btn>
                  </div>
                </div>
              </v-alert>

              <ModelMappingSection
                v-if="form.serviceType"
                :mapping-rows="modelMappingRows"
                :source-model-options="sourceModelOptions"
                :target-model-options="targetModelOptions"
                :fetching-models="fetchingModels"
                :source-mapping-error="sourceMappingError"
                :fetch-models-error="fetchModelsError"
                :model-mapping-hint="modelMappingHint"
                :target-model-placeholder="targetModelPlaceholder"
                :show-model-mapping-presets="showModelMappingPresets"
                :show-messages-open-a-i-channel-presets="showMessagesOpenAIChannelPresets"
                :show-claude-channel-presets="showClaudeChannelPresets"
                :show-codex-responses-channel-presets="showCodexResponsesChannelPresets"
                :supports-reasoning-mapping-options="supportsReasoningMappingOptions"
                :reasoning-effort-options="reasoningEffortOptions"
                @update:mapping-rows="modelMappingRows = ($event as any)"
                @sync-upstream="syncUpstreamModels"
                @apply-preset="applyPreset"
                @menu-update="onMenuUpdate"
                @target-edit-start="startMappingTargetEdit"
                @target-edit-end="finishMappingTargetEdit"
              >
                <template #vision-fallback>
                  <div v-if="hasNoVisionRows" class="mt-6">
                    <v-row dense>
                      <v-col cols="12" :md="supportsReasoningMappingOptions ? 8 : 12">
                        <v-combobox
                          v-model="form.visionFallbackModel"
                          :label="t('addChannel.visionFallbackLabel')"
                          :placeholder="t('addChannel.visionFallbackPlaceholder')"
                          :hint="t('addChannel.visionFallbackHint')"
                          :items="targetModelOptions"
                          prepend-inner-icon="mdi-eye"
                          persistent-hint
                          clearable
                          variant="outlined"
                          density="comfortable"
                          eager
                          @focus="startMappingTargetEdit(); ensureTargetModelsLoaded()"
                          @blur="finishMappingTargetEdit"
                          @update:menu="onMenuUpdate"
                        />
                      </v-col>
                      <v-col v-if="supportsReasoningMappingOptions" cols="12" md="4">
                        <v-select
                          v-model="form.visionFallbackReasoningEffort"
                          :label="t('addChannel.visionFallbackReasoningLabel')"
                          :items="reasoningEffortOptions"
                          variant="outlined"
                          density="comfortable"
                          clearable
                          persistent-hint
                          :hint="t('addChannel.visionFallbackReasoningHint')"
                          eager
                          @update:menu="onMenuUpdate"
                        />
                      </v-col>
                    </v-row>
                  </div>
                </template>
              </ModelMappingSection>

              <!-- 模型过滤 -->
              <div class="mt-4">
                <SupportedModelsFilter
                  :model-value="form.supportedModels"
                  :error="supportedModelsError"
                  :common-filters="commonSupportedModelFilters"
                  :selected-filters="Array.from(selectedSupportedModelSet)"
                  @update:model-value="handleSupportedModelsChange($event as any)"
                  @append-filter="appendSupportedModelFilter"
                  @menu-update="onMenuUpdate"
                />
              </div>

              <div v-if="props.channelType !== 'images' && props.channelType !== 'vectors'" class="mt-6">
                <ModelCapabilitySection
                  v-model:rows="form.modelCapabilityRows"
                  :target-model-options="targetModelOptions"
                  :mapped-target-models="mappedTargetModels"
                  :fetching-models="fetchingModels"
                  :fetch-models-error="fetchModelsError"
                  :error="modelCapabilitiesError"
                  @sync-upstream="syncUpstreamModels"
                  @menu-update="onMenuUpdate"
                />
              </div>

              <div v-if="props.channelType === 'vectors'" class="mt-6">
                <EmbeddingCompatibilitySection
                  v-model:rows="form.embeddingCapabilityRows"
                  :target-model-options="targetModelOptions"
                  :mapped-target-models="mappedTargetModels"
                  :fetching-models="fetchingModels"
                  :fetch-models-error="fetchModelsError"
                  :error="embeddingCapabilitiesError"
                  @sync-upstream="syncUpstreamModels"
                  @menu-update="onMenuUpdate"
                />
              </div>
            </section>

            <!-- 高级选项 -->
            <section v-if="!isAutoManagedChannel" :ref="(el: any) => setSectionRef('advanced', el)" data-section-id="advanced" class="pa-6 scroll-mt-4">
              <AdvancedOptionsSection
                :form="form"
                :channel-type="props.channelType"
                :supports-open-a-i-advanced-options="supportsOpenAIAdvancedOptions"
                :reasoning-param-style-options="reasoningParamStyleOptions"
                :text-verbosity-options="textVerbosityOptions"
                :is-auto-managed="isAutoManagedChannel"
                :diagnosing="diagnosingCompat"
                :diagnose-result="diagnoseResult"
                @update:form="updateForm"
                @menu-update="onMenuUpdate"
                @diagnose="handleDiagnoseCompat"
              />
            </section>

            <!-- 自定义参数（自定义请求头 + 流式超时） -->
            <section v-if="!isAutoManagedChannel" :ref="(el: any) => setSectionRef('custom', el)" data-section-id="custom" class="pa-6 scroll-mt-4">
              <CustomHeadersSection
                :headers="customHeadersArray"
                @update:headers="updateCustomHeaders"
              />

              <div class="mt-6">
                <TransportConfigGroup :form="form" @update:field="(field, value) => updateForm({ [field]: value })" />
              </div>

              <div class="mt-6">
                <StreamTimeoutSection
                  :request-timeout-ms="form.requestTimeoutMs"
                  :response-header-timeout-ms="form.responseHeaderTimeoutMs"
                  :selected-strategy="selectedStreamTimeoutStrategy"
                  :first-content-enabled="form.streamFirstContentTimeoutEnabled"
                  :first-content-ms="form.streamFirstContentTimeoutMs"
                  :inactivity-enabled="form.streamInactivityTimeoutEnabled"
                  :inactivity-ms="form.streamInactivityTimeoutMs"
                  :tool-call-idle-enabled="form.streamToolCallIdleTimeoutEnabled"
                  :tool-call-idle-ms="form.streamToolCallIdleTimeoutMs"
                  @update:request-timeout-ms="form.requestTimeoutMs = $event"
                  @update:response-header-timeout-ms="form.responseHeaderTimeoutMs = $event"
                  @apply-strategy="applyStreamTimeoutStrategy"
                  @update:first-content-ms="form.streamFirstContentTimeoutMs = $event"
                  @update:inactivity-ms="form.streamInactivityTimeoutMs = $event"
                  @update:tool-call-idle-ms="form.streamToolCallIdleTimeoutMs = $event"
                />
              </div>

              <div class="mt-6">
                <RateLimitGroup :form="form" @update:field="(field, value) => updateForm({ [field]: value })" />
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
import ModelMappingSection from './edit-channel/ModelMappingSection.vue'
import ModelCapabilitySection from './edit-channel/ModelCapabilitySection.vue'
import EmbeddingCompatibilitySection from './edit-channel/EmbeddingCompatibilitySection.vue'
import SupportedModelsFilter from './edit-channel/SupportedModelsFilter.vue'
import CustomHeadersSection from './edit-channel/CustomHeadersSection.vue'
import StreamTimeoutSection from './edit-channel/StreamTimeoutSection.vue'
import AdvancedOptionsSection from './edit-channel/AdvancedOptionsSection.vue'
import TransportConfigGroup from './edit-channel/TransportConfigGroup.vue'
import RateLimitGroup from './edit-channel/RateLimitGroup.vue'
import NewApiAccountPanel from './edit-channel/NewApiAccountPanel.vue'
import { useEditChannelModal, type EditChannelModalEmits, type EditChannelModalProps } from '../composables/useEditChannelModal'
import { ApiService } from '../services/api'
import type { ManagedAccountChannel } from '../services/api-types'
import { buildNativeProtocolModelRoutes, loadLegacyManagedModelAvailability } from '../utils/channelModelAvailability'
import { getManagedProviderWebsiteLinks } from '../utils/channelWebsite'
import { isManagedProviderChannel, isOfficialProviderChannel, providerDisplayName } from '../utils/providerDisplay'
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
  sourceModelOptions,
  modelMappingHint,
  targetModelPlaceholder,
  reasoningEffortOptions,
  reasoningParamStyleOptions,
  textVerbosityOptions,
  supportsOpenAIAdvancedOptions,
  supportsReasoningMappingOptions,
  supportsChannelDiscovery,
  isAutoManagedChannel,
  showModelMappingPresets,
  showMessagesOpenAIChannelPresets,
  showClaudeChannelPresets,
  showCodexResponsesChannelPresets,
  form,
  baseUrlsText,
  modelMappingRows,
  hasNoVisionRows,
  mappedTargetModels,
  sourceMappingError,
  targetModelOptions,
  fetchingModels,
  fetchModelsError,
  keyModelsStatus,
  errors,
  rules,
  isEditing,
  isMac,
  selectedStreamTimeoutStrategy,
  applyStreamTimeoutStrategy,
  commonSupportedModelFilters,
  selectedSupportedModelSet,
  supportedModelsError,
  modelCapabilitiesError,
  embeddingCapabilitiesError,
  startMappingTargetEdit,
  finishMappingTargetEdit,
  headerClasses,
  avatarColor,
  headerIconStyle,
  subtitleClasses,
  isFormValid,
  handleSupportedModelsChange,
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
  appendSupportedModelFilter,
  ensureTargetModelsLoaded,
  updateForm,
  syncUpstreamModels,
  discoveringChannelConfig,
  channelDiscoveryResult,
  channelDiscoveryError,
  channelDiscoveryModelMappingEntries,
  channelDiscoveryCompatEntries,
  channelDiscoveryReasoningEntries,
  channelDiscoverySuccessfulProtocols,
  channelDiscoveryCapabilityEntries,
  handleDiscoverChannelConfig,
  applyChannelDiscoveryRecommendation,
  applyPreset,
  handleSubmit,
  handleCancel,
  diagnosingCompat,
  diagnoseResult,
  handleDiagnoseCompat,
  scrollToSection,
  setSectionRef,
  t,
} = useEditChannelModal(props, emit)

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
