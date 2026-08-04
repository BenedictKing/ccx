<script setup lang="ts">
import { computed } from 'vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { ScrollArea } from '@/components/ui/scroll-area'
import {
  CheckCircle2,
  Copy,
  Check,
  Loader2,
  Radar,
} from 'lucide-vue-next'
import ChannelEditorHeader from './channel-edit/ChannelEditorHeader.vue'
import QuickCreatePanel from './channel-edit/QuickCreatePanel.vue'
import BasicConfigPanel from './channel-edit/BasicConfigPanel.vue'
import AuthPanel from './channel-edit/AuthPanel.vue'
import ModelMappingPanel from './channel-edit/ModelMappingPanel.vue'
import ModelCapabilityPanel from './channel-edit/ModelCapabilityPanel.vue'
import EmbeddingCompatibilityPanel from './channel-edit/EmbeddingCompatibilityPanel.vue'
import AdvancedPanel from './channel-edit/AdvancedPanel.vue'
import CustomHeadersPanel from './channel-edit/CustomHeadersPanel.vue'
import CustomParamsPanel from './channel-edit/CustomParamsPanel.vue'
import StreamTimeoutPanel from './channel-edit/StreamTimeoutPanel.vue'
import { useChannelEditDialog, type ChannelEditDialogEmit, type ChannelEditDialogProps } from '@/composables/useChannelEditDialog'

const props = defineProps<ChannelEditDialogProps>()
const emit = defineEmits<ChannelEditDialogEmit>()

const {
  isEditMode,
  isMac,
  saving,
  restoringKey,
  error,
  success,
  diagnosingCompat,
  diagnoseResult,
  quickInput,
  existingApiKeys,
  newApiKeysText,
  copiedKeyIndex,
  duplicateKeyIndex,
  localRestoredKeys,
  copilotOAuthLoading,
  copilotPolling,
  copilotOAuthError,
  copilotOAuthSuccess,
  copilotUserCode,
  copilotUserCodeCopied,
  keyModelsStatus,
  activeSection,
  sections,
  modelMappingRows,
  modelCapabilityRows,
  mappedTargetModels,
  newModelMapping,
  headerRows,
  newHeader,
  showTargetSuggestions,
  activeTargetInputId,
  fetchedModelsError,
  filteredTargetModels,
  showSourceSuggestions,
  activeSourceInputId,
  filteredSourceModels,
  reasoningParamStyleOptions,
  textVerbosityOptions,
  DEFAULT_SELECT_VALUE,
  reasoningEffortOptions,
  form,
  applyFormUpdates,
  disabledApiKeys,
  disabledGroupModels,
  historicalApiKeys,
  detectedBaseUrls,
  detectedApiKeys,
  generatedChannelName,
  errors,
  embeddingCapabilitiesError,
  isValid,
  serviceTypeOptions,
  headerServiceTypeItems,
  supportsOpenAIAdvancedOptions,
  supportsReasoningMappingOptions,
  supportsChannelDiscovery,
  modelMappingHint,
  targetModelPlaceholder,
  showModelMappingPresets,
  showMessagesOpenAIChannelPresets,
  showClaudeChannelPresets,
  showCodexResponsesPresets,
  fetchingModels,
  sourceModelOptions,
  targetModelDatalist,
  commonSupportedModelFilters,
  supportedModelsError,
  selectedSupportedModelSet,
  sourceMappingError,
  discoveringChannelConfig,
  channelDiscoveryResult,
  channelDiscoveryError,
  channelDiscoveryModelMappingEntries,
  channelDiscoveryCompatEntries,
  channelDiscoveryReasoningEntries,
  channelDiscoverySuccessfulProtocols,
  channelDiscoveryCapabilityEntries,
  expectedRequestUrls,
  quickExpectedRequestUrls,
  clearCopilotPollTimer,
  scrollToSection,
  setSectionRef,
  showTargetDropdown,
  hideTargetDropdown,
  showSourceDropdown,
  hideSourceDropdown,
  selectSourceModel,
  selectTargetModel,
  removeExistingApiKey,
  handleQuickPaste,
  updateQuickServiceType,
  clearDuplicateKeyHighlight,
  moveApiKeyToTop,
  moveApiKeyToBottom,
  addModelMappingRow,
  removeModelMappingRow,
  toggleSupportedModelFilter,
  handleTargetFocus,
  applyPreset,
  syncUpstreamModels,
  updateMappingRow,
  updateModelCapabilityRows,
  startMappingTargetEdit,
  finishMappingTargetEdit,
  addHeaderRow,
  removeHeaderRow,
  updateHeaderRow,
  copyCopilotUserCode,
  startCopilotOAuth,
  openCopilotAuthorization,
  handleSubmit,
  addNewApiKeys,
  copyApiKey,
  handleDisabledKeyRestore,
  handleGroupModelDisable,
  handleGroupModelRestore,
  handleTestCapability,
  handleDiagnoseCompat,
  handleDiscoverChannelConfig,
  applyChannelDiscoveryRecommendation,
  t,
  managedProviderName,
  isOfficialManagedProvider,
  isAutoManagedChannel,
} = useChannelEditDialog(props, emit)

const embeddingTargetModels = computed(() =>
  targetModelDatalist.value.map(m => ({ title: m, value: m }))
)
</script>

<template>
  <Teleport to="body">
    <Transition name="fade">
      <div
        v-if="true"
        class="fixed inset-0 z-50 flex items-center justify-center"
      >
        <div class="absolute inset-0 bg-black/60 backdrop-blur-sm" @click="emit('close')" />

        <div
          ref="dialogRef"
          class="relative z-10 flex max-h-[90vh] w-[94vw] flex-col overflow-hidden rounded-xl border border-border/80 bg-background shadow-2xl backdrop-blur-md"
          :class="isEditMode ? 'max-w-6xl' : 'max-w-3xl'"
        >
          <ChannelEditorHeader
            :channel-type="channelType"
            :is-edit-mode="isEditMode"
            :no-vision="form.noVision"
            :saving="saving"
            @close="emit('close')"
            @toggle-no-vision="form.noVision = !form.noVision"
            @test-capability="handleTestCapability"
          />

          <!-- 创建模式：独立快速添加，不展示编辑器大纲和高级配置 -->
          <div v-if="!isEditMode" class="min-h-0 flex-1 overflow-hidden">
            <ScrollArea class="h-full">
              <form @submit.prevent="handleSubmit">
                <div v-if="error" class="mx-6 mt-6 rounded-lg border border-destructive/30 bg-destructive/10 p-3 text-sm text-destructive">
                  {{ error }}
                </div>

                <QuickCreatePanel
                  :quick-input="quickInput"
                  :service-type="form.serviceType"
                  :service-type-options="headerServiceTypeItems"
                  :detected-base-urls="detectedBaseUrls"
                  :detected-api-keys="detectedApiKeys"
                  :existing-channels="existingChannels"
                  :expected-request-urls="quickExpectedRequestUrls"
                  :generated-channel-name="generatedChannelName"
                  @update:quick-input="quickInput = $event"
                  @update:service-type="updateQuickServiceType"
                  @quick-paste="handleQuickPaste"
                />
              </form>
            </ScrollArea>
          </div>

          <!-- 编辑模式：完整渠道编辑器 -->
          <div v-else class="min-h-0 flex-1 flex">
            <!-- 左侧导航 -->
            <nav class="flex w-[180px] shrink-0 flex-col items-stretch gap-1 rounded-none border-r border-border/50 bg-card/20 p-4">
              <div class="text-[10px] font-bold uppercase tracking-wider text-muted-foreground/60 px-3 mb-2">{{ t('channelEditor.nav.outline') }}</div>
              <button
                v-for="s in sections"
                :key="s.id"
                class="flex items-center justify-start rounded-md border px-3 py-1.5 text-xs font-medium whitespace-nowrap transition-[color,box-shadow] focus-visible:ring-[3px] focus-visible:outline-1 disabled:pointer-events-none disabled:opacity-50"
                :class="activeSection === s.id
                  ? 'bg-background text-foreground shadow-sm border-input'
                  : 'text-muted-foreground border-transparent hover:text-foreground hover:bg-accent/50'"
                @click="scrollToSection(s.id)"
              >{{ s.label }}</button>
            </nav>

              <!-- 右侧内容面板 -->
              <div class="min-w-0 flex-1 overflow-hidden">
                <ScrollArea class="h-full">
                  <form class="p-6 space-y-6" @submit.prevent="handleSubmit">
                    <!-- 错误提示 -->
                    <div v-if="error" class="border border-destructive/30 bg-destructive/10 p-3 text-sm text-destructive rounded-lg">
                      {{ error }}
                    </div>
                    <div v-if="success" class="rounded-lg border border-emerald-500/30 bg-emerald-500/10 p-3 text-sm text-emerald-700 dark:text-emerald-300">
                      {{ success }}
                    </div>

                    <!-- Section: 基础配置 -->
                    <section :ref="(el: any) => setSectionRef('basic', el)" data-section-id="basic" class="scroll-mt-4">
                      <BasicConfigPanel
                        :form="form"
                        :errors="errors"
                        :service-type-options="serviceTypeOptions"
                        :expected-request-urls="expectedRequestUrls"
                        :managed-account="isAutoManagedChannel"
                        :provider-name="managedProviderName"
                        :official-provider="isOfficialManagedProvider"
                        @update:form="(updates) => Object.assign(form, updates)"
                      />
                    </section>

                    <!-- Section: 认证管理 -->
                    <section :ref="(el: any) => setSectionRef('auth', el)" data-section-id="auth" class="scroll-mt-4">
                      <AuthPanel
                        :existing-api-keys="existingApiKeys"
                        :new-api-keys-text="newApiKeysText"
                        :copied-key-index="copiedKeyIndex"
                        :duplicate-key-index="duplicateKeyIndex"
                        :disabled-api-keys="disabledApiKeys"
                        :disabled-group-models="disabledGroupModels"
                        :historical-api-keys="historicalApiKeys"
                        :restoring-key="restoringKey"
                        :local-restored-keys="localRestoredKeys"
                        :key-models-status="keyModelsStatus"
                        :service-type="form.serviceType"
                        :channel-kind="channelType"
                        :channel-uid="channel?.channelUid"
                        :api-key-configs="channel?.apiKeyConfigs"
                        :on-disable-group-model="handleGroupModelDisable"
                        :on-restore-group-model="handleGroupModelRestore"
                        :errors="errors"
                        @update:new-api-keys-text="newApiKeysText = $event; clearDuplicateKeyHighlight()"
                        @add-new-api-keys="addNewApiKeys"
                        @remove-existing-api-key="removeExistingApiKey"
                        @move-api-key-to-top="moveApiKeyToTop"
                        @move-api-key-to-bottom="moveApiKeyToBottom"
                        @copy-api-key="copyApiKey"
                        @handle-disabled-key-restore="handleDisabledKeyRestore"
                      />

                      <!-- GitHub Copilot OAuth 登录（仅 copilot 渠道显示） -->
                      <div v-if="form.serviceType === 'copilot'" class="mt-4 rounded-xl border border-border/60 bg-card/40 p-5 space-y-3">
                        <h4 class="text-xs font-bold uppercase tracking-wider text-primary">GitHub Copilot</h4>
                        <div class="space-y-1.5">
                          <label class="text-[10px] font-semibold text-muted-foreground">
                            {{ t('channelEditor.transport.proxyUrl.label') }}
                          </label>
                          <Input
                            :model-value="form.proxyUrl"
                            class="h-9 font-mono text-xs"
                            :placeholder="t('channelEditor.transport.proxyUrl.placeholder')"
                            @update:model-value="(val) => form.proxyUrl = val as string"
                          />
                          <p class="text-[10px] leading-4 text-muted-foreground">
                            {{ t('channelEditor.transport.proxyUrl.hint') }}
                          </p>
                        </div>
                        <div v-if="copilotUserCode" class="flex items-center gap-2 text-sm">
                          <span class="text-muted-foreground">{{ t('copilotOAuth.userCode') }}</span>
                          <code class="px-2 py-0.5 rounded bg-muted font-mono text-xs">{{ copilotUserCode }}</code>
                          <button
                            type="button"
                            class="inline-flex h-6 w-6 items-center justify-center rounded border border-border text-muted-foreground transition-colors hover:text-foreground"
                            :title="copilotUserCodeCopied ? t('common.copied') : t('common.copy')"
                            :aria-label="copilotUserCodeCopied ? t('common.copied') : t('common.copy')"
                            @click="copyCopilotUserCode"
                          >
                            <CheckCircle2 v-if="copilotUserCodeCopied" class="h-3.5 w-3.5 text-emerald-700 dark:text-emerald-400" />
                            <Copy v-else class="h-3.5 w-3.5" />
                          </button>
                          <button type="button" class="text-primary text-xs underline" @click="openCopilotAuthorization">{{ t('copilotOAuth.openAuthorize') }}</button>
                        </div>
                        <p v-if="copilotOAuthSuccess" class="text-xs text-emerald-600">{{ t('copilotOAuth.success') }}</p>
                        <p v-if="copilotOAuthError" class="text-xs text-destructive">{{ copilotOAuthError }}</p>
                        <div class="flex items-center gap-2">
                          <button
                            type="button"
                            class="inline-flex items-center gap-1.5 rounded-md border border-primary/40 bg-primary/10 px-3 py-1.5 text-xs font-medium text-primary hover:bg-primary/20 disabled:opacity-50"
                            :disabled="copilotOAuthLoading || copilotPolling"
                            @click="startCopilotOAuth"
                          >
                            <Loader2 v-if="copilotOAuthLoading || copilotPolling" class="h-3.5 w-3.5 animate-spin" />
                            {{ t('copilotOAuth.button') }}
                          </button>
                          <span v-if="copilotPolling" class="text-xs text-muted-foreground">{{ t('copilotOAuth.waiting') }}</span>
                          <button v-if="copilotPolling || copilotOAuthLoading" type="button" class="text-xs text-muted-foreground underline" @click="clearCopilotPollTimer(); copilotPolling = false; copilotOAuthLoading = false">{{ t('copilotOAuth.cancel') }}</button>
                        </div>
                      </div>
                    </section>

                    <!-- Section: 模型重定向 -->
                    <section :ref="(el: any) => setSectionRef('redirect', el)" data-section-id="redirect" class="scroll-mt-4">
                      <div
                        v-if="supportsChannelDiscovery"
                        class="mb-6 space-y-4 rounded-xl border border-primary/20 bg-primary/[0.04] p-5 shadow-sm"
                      >
                        <div class="flex flex-wrap items-center justify-between gap-3">
                          <div class="flex min-w-0 items-center gap-3">
                            <span class="flex size-9 shrink-0 items-center justify-center rounded-lg border border-primary/20 bg-primary/10 text-primary">
                              <Radar class="h-4 w-4" />
                            </span>
                            <div class="min-w-0">
                              <h4 class="text-xs font-bold uppercase tracking-wider text-foreground">
                                {{ t('channelDiscovery.title') }}
                              </h4>
                              <p class="mt-1 text-xs leading-5 text-muted-foreground">
                                {{ t('channelDiscovery.hint') }}
                              </p>
                            </div>
                          </div>
                          <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            class="h-8 border-primary/30 bg-background/70 text-primary hover:bg-primary/10"
                            :disabled="discoveringChannelConfig"
                            @click="handleDiscoverChannelConfig"
                          >
                            <Loader2 v-if="discoveringChannelConfig" class="mr-2 h-3.5 w-3.5 animate-spin" />
                            <Radar v-else class="mr-2 h-3.5 w-3.5" />
                            {{ t('channelDiscovery.button') }}
                          </Button>
                        </div>

                        <div v-if="channelDiscoveryError" class="rounded-lg border border-destructive/30 bg-destructive/10 p-3 text-xs text-destructive">
                          {{ channelDiscoveryError }}
                        </div>

                        <div v-if="channelDiscoveryResult" class="space-y-3">
                          <div class="flex flex-wrap items-center gap-2">
                            <span class="rounded-full border border-primary/20 bg-primary/10 px-2.5 py-1 text-[10px] font-medium text-primary">
                              {{ t('channelDiscovery.recommendedKind', { kind: channelDiscoveryResult.recommendation.channelKind || '-' }) }}
                            </span>
                            <span class="rounded-full border border-emerald-500/20 bg-emerald-500/10 px-2.5 py-1 text-[10px] font-medium text-emerald-700 dark:text-emerald-300">
                              {{ t('channelDiscovery.modelsFound', { count: String(channelDiscoveryResult.models.items.length) }) }}
                            </span>
                            <span
                              v-for="protocol in channelDiscoverySuccessfulProtocols"
                              :key="protocol.protocol"
                              class="rounded-full border border-emerald-500/20 bg-emerald-500/10 px-2.5 py-1 text-[10px] font-medium text-emerald-700 dark:text-emerald-300"
                            >
                              {{ protocol.protocol }} {{ protocol.successModels?.length || 0 }}
                            </span>
                          </div>

                          <div v-if="channelDiscoveryModelMappingEntries.length" class="space-y-1.5">
                            <div class="text-[10px] font-bold uppercase tracking-wider text-muted-foreground/70">
                              {{ t('channelDiscovery.mapping') }}
                            </div>
                            <div class="flex flex-wrap items-center gap-1.5">
                              <span
                                v-for="[source, target] in channelDiscoveryModelMappingEntries"
                                :key="source"
                                class="rounded-md border border-primary/20 bg-primary/10 px-2 py-1 font-mono text-[10px] text-primary"
                              >
                                {{ source }} -> {{ target }}
                              </span>
                            </div>
                          </div>

                          <div v-if="channelDiscoveryReasoningEntries.length" class="space-y-1.5">
                            <div class="text-[10px] font-bold uppercase tracking-wider text-muted-foreground/70">
                              {{ t('channelDiscovery.reasoning') }}
                            </div>
                            <div class="flex flex-wrap items-center gap-1.5">
                              <span
                                v-for="[source, effort] in channelDiscoveryReasoningEntries"
                                :key="source"
                                class="rounded-md border border-secondary/30 bg-secondary/10 px-2 py-1 font-mono text-[10px] text-secondary-foreground"
                              >
                                {{ source }}={{ effort }}
                              </span>
                            </div>
                            <p class="text-xs leading-5 text-muted-foreground">
                              {{ t('channelDiscovery.reasoningNote') }}
                            </p>
                          </div>

                          <div v-if="channelDiscoveryCompatEntries.length" class="space-y-1.5">
                            <div class="text-[10px] font-bold uppercase tracking-wider text-muted-foreground/70">
                              {{ t('channelDiscovery.compat') }}
                            </div>
                            <div class="flex flex-wrap items-center gap-1.5">
                              <span
                                v-for="[key, value] in channelDiscoveryCompatEntries"
                                :key="key"
                                class="rounded-md border border-amber-500/20 bg-amber-500/10 px-2 py-1 font-mono text-[10px] text-amber-700 dark:text-amber-300"
                              >
                                {{ key }}={{ value }}
                              </span>
                            </div>
                          </div>

                          <div v-if="channelDiscoveryCapabilityEntries.length" class="space-y-1.5">
                            <div class="text-[10px] font-bold uppercase tracking-wider text-muted-foreground/70">
                              {{ t('channelDiscovery.capabilities') }}
                            </div>
                            <div class="flex flex-wrap items-center gap-1.5">
                              <span
                                v-for="capability in channelDiscoveryCapabilityEntries"
                                :key="capability.key"
                                :title="capability.detail"
                                class="rounded-md border px-2 py-1 font-mono text-[10px]"
                                :class="capability.tone === 'success'
                                  ? 'border-emerald-500/20 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300'
                                  : capability.tone === 'secondary'
                                    ? 'border-secondary/30 bg-secondary/10 text-secondary-foreground'
                                    : 'border-amber-500/20 bg-amber-500/10 text-amber-700 dark:text-amber-300'"
                              >
                                {{ capability.label }}={{ capability.text }}
                              </span>
                            </div>
                          </div>

                          <p v-if="channelDiscoveryResult.models.warnings?.length" class="text-xs leading-5 text-amber-700 dark:text-amber-300">
                            {{ channelDiscoveryResult.models.warnings.join(' / ') }}
                          </p>

                          <div class="flex justify-end">
                            <Button
                              type="button"
                              size="sm"
                              class="h-8"
                              :disabled="!channelDiscoveryResult"
                              @click="applyChannelDiscoveryRecommendation"
                            >
                              <Check class="mr-2 h-3.5 w-3.5" />
                              {{ t('channelDiscovery.apply') }}
                            </Button>
                          </div>
                        </div>
                      </div>

                      <ModelMappingPanel
                        :model-mapping-rows="modelMappingRows"
                        :new-model-mapping="newModelMapping"
                        :source-model-options="sourceModelOptions"
                        :filtered-source-models="filteredSourceModels"
                        :reasoning-effort-options="reasoningEffortOptions"
                        :filtered-target-models="filteredTargetModels"
                        :channel-type="channelType"
                        :show-target-suggestions="showTargetSuggestions"
                        :active-target-input-id="activeTargetInputId"
                        :show-source-suggestions="showSourceSuggestions"
                        :active-source-input-id="activeSourceInputId"
                        :DEFAULT_SELECT_VALUE="DEFAULT_SELECT_VALUE"
                        :vision-fallback-model="form.visionFallbackModel"
                        :vision-fallback-reasoning-effort="form.visionFallbackReasoningEffort"
                        :supported-models-text="form.supportedModelsText"
                        :model-mapping-hint="modelMappingHint"
                        :target-model-placeholder="targetModelPlaceholder"
                        :show-model-mapping-presets="showModelMappingPresets"
                        :show-messages-open-a-i-channel-presets="showMessagesOpenAIChannelPresets"
                        :show-claude-channel-presets="showClaudeChannelPresets"
                        :show-codex-responses-presets="showCodexResponsesPresets"
                        :supports-reasoning-mapping-options="supportsReasoningMappingOptions"
                        :common-supported-model-filters="commonSupportedModelFilters"
                        :selected-supported-model-set="selectedSupportedModelSet"
                        :source-mapping-error="sourceMappingError"
                        :fetch-models-error="fetchedModelsError"
                        :supported-models-error="supportedModelsError"
                        @update:new-model-mapping="(updates) => Object.assign(newModelMapping, updates)"
                        @update:vision-fallback-model="form.visionFallbackModel = $event"
                        @update:vision-fallback-reasoning-effort="form.visionFallbackReasoningEffort = $event"
                        @update:supported-models-text="form.supportedModelsText = $event"
                        @add-model-mapping-row="addModelMappingRow"
                        @remove-model-mapping-row="removeModelMappingRow"
                        @update-mapping-row="updateMappingRow"
                        @sync-upstream-models="syncUpstreamModels"
                        @apply-preset="applyPreset"
                        @show-target-dropdown="showTargetDropdown"
                        @hide-target-dropdown="hideTargetDropdown"
                        @select-target-model="selectTargetModel"
                        @handle-target-focus="handleTargetFocus"
                        @target-edit-start="startMappingTargetEdit"
                        @target-edit-end="finishMappingTargetEdit"
                        @show-source-dropdown="showSourceDropdown"
                        @hide-source-dropdown="hideSourceDropdown"
                        @select-source-model="selectSourceModel"
                        @append-supported-model-filter="toggleSupportedModelFilter"
                      />
                      <ModelCapabilityPanel
                        v-if="channelType !== 'images' && channelType !== 'vectors'"
                        class="mt-6"
                        :rows="modelCapabilityRows"
                        :target-models="targetModelDatalist"
                        :mapped-target-models="mappedTargetModels"
                        :fetching-models="fetchingModels"
                        :fetch-models-error="fetchedModelsError"
                        :error="errors.modelCapabilitiesText"
                        @update:rows="updateModelCapabilityRows"
                        @sync-upstream-models="syncUpstreamModels"
                      />
                      <EmbeddingCompatibilityPanel
                        v-if="channelType === 'vectors'"
                        class="mt-6"
                        :rows="form.embeddingCapabilityRows"
                        :target-models="embeddingTargetModels"
                        :mapped-target-models="mappedTargetModels"
                        :fetching-models="fetchingModels"
                        :fetch-models-error="fetchedModelsError"
                        :error="embeddingCapabilitiesError"
                        @update:rows="(rows) => form.embeddingCapabilityRows = rows"
                        @sync-upstream-models="syncUpstreamModels"
                      />
                    </section>

                    <!-- Section: 高级选项 -->
                    <section :ref="(el: any) => setSectionRef('advanced', el)" data-section-id="advanced" class="scroll-mt-4">
                      <AdvancedPanel
                        :form="form"
                        :channel-type="channelType"
                        :supports-open-a-i-advanced-options="supportsOpenAIAdvancedOptions"
                        :reasoning-param-style-options="reasoningParamStyleOptions"
                        :text-verbosity-options="textVerbosityOptions"
                        :diagnosing="diagnosingCompat"
                        :diagnose-result="diagnoseResult"
                        @update:form="applyFormUpdates"
                        @diagnose="handleDiagnoseCompat"
                      />
                    </section>

                    <!-- Section: 自定义参数 -->
                    <section :ref="(el: any) => setSectionRef('custom', el)" data-section-id="custom" class="scroll-mt-4">
                      <CustomHeadersPanel
                        :header-rows="headerRows"
                        :new-header="newHeader"
                        @update:new-header="(updates) => Object.assign(newHeader, updates)"
                        @add-header-row="addHeaderRow"
                        @remove-header-row="removeHeaderRow"
                        @update-header-row="updateHeaderRow"
                      />
                      <div class="mt-6">
                        <StreamTimeoutPanel
                          :form="form"
                          @update:form="(updates) => Object.assign(form, updates)"
                        />
                      </div>
                      <div class="mt-6">
                        <CustomParamsPanel
                          :form="form"
                          @update:form="(updates) => Object.assign(form, updates)"
                        />
                      </div>
                    </section>
                  </form>
                </ScrollArea>
              </div>
          </div>

          <!-- 底部按钮栏 -->
          <div class="flex shrink-0 flex-wrap items-center justify-end gap-2 border-t border-border bg-card/80 p-4 backdrop-blur-md">
            <Button variant="outline" class="hover:bg-muted hover:text-foreground dark:hover:bg-muted/50 hover:scale-[1.02] active:scale-[0.98]" @click="emit('close')">
              {{ t('common.cancel') }}
              <span class="ml-1 hidden sm:inline-flex h-4 select-none items-center gap-1 rounded border bg-transparent px-1.5 font-mono text-[9px] font-medium text-muted-foreground/80">Esc</span>
            </Button>
            <Button type="button" class="hover:shadow-lg hover:scale-[1.02] active:scale-[0.98]" :disabled="!isValid || saving" @click="handleSubmit">
              <Loader2 v-if="saving" class="mr-2 h-4 w-4 animate-spin" />
              {{ isEditMode
                ? t('channelEditor.actions.save')
                : t('channelEditor.actions.create')
              }}
              <span class="ml-1 hidden sm:inline-flex h-4 select-none items-center gap-1 rounded border border-primary-foreground/30 bg-primary-foreground/10 px-1.5 font-mono text-[9px] font-medium text-primary-foreground/90">{{ isMac ? '⌘ Enter' : 'Ctrl+Enter' }}</span>
            </Button>
          </div>
        </div>
      </div>
    </Transition>
  </Teleport>
</template>

<style scoped>
.fade-enter-active,
.fade-leave-active {
  transition: opacity 0.15s ease;
}
.fade-enter-from,
.fade-leave-to {
  opacity: 0;
}

/* Range Slider 美化 */
input[type="range"].accent-primary::-webkit-slider-runnable-track {
  background: hsl(var(--primary) / 0.1);
  height: 4px;
  border-radius: 9999px;
}

input[type="range"].accent-primary::-webkit-slider-thumb {
  margin-top: -5px;
  background: hsl(var(--primary));
  border: 2px solid hsl(var(--background));
  box-shadow: 0 2px 6px rgba(0, 0, 0, 0.2);
  width: 14px;
  height: 14px;
  border-radius: 9999px;
  transition: transform 0.1s;
}

input[type="range"].accent-primary::-webkit-slider-thumb:hover {
  transform: scale(1.2);
}
</style>
