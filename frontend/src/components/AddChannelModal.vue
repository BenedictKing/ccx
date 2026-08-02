<template>
  <v-dialog :model-value="show" max-width="800" persistent @update:model-value="$emit('update:show', $event)">
    <v-card rounded="lg" class="add-channel-dialog">
      <v-card-title class="d-flex align-center ga-3 pa-6" :class="headerClasses">
        <v-avatar :color="avatarColor" variant="flat" size="40">
          <v-icon :style="headerIconStyle" size="20">mdi-plus</v-icon>
        </v-avatar>
        <div class="flex-grow-1 modal-header-text">
          <div class="modal-title">
            {{ t('addChannel.createTitle') }}
          </div>
          <div class="modal-subtitle" :class="subtitleClasses">
            {{ t('addChannel.quickSubtitle') }}
          </div>
        </div>
      </v-card-title>

      <v-card-text class="pa-6">
        <!-- 模式切换 -->
        <div class="d-flex justify-center mb-4">
          <v-btn-toggle
            v-model="quickAddMode"
            mandatory
            :disabled="isCreatingChannel"
            density="compact"
            rounded="lg"
            color="primary"
            variant="outlined"
          >
            <v-btn :value="false" size="small">
              <v-icon start size="small">mdi-text</v-icon>
              {{ t('autopilot.quickAdd.modeStandard') }}
            </v-btn>
            <v-btn :value="true" size="small">
              <v-icon start size="small">mdi-auto-fix</v-icon>
              {{ t('autopilot.quickAdd.modeQuick') }}
            </v-btn>
          </v-btn-toggle>
        </div>

        <!-- 快速添加模式 -->
        <QuickAddChannelForm
          v-if="quickAddMode"
          ref="quickAddFormRef"
          :channel-type="channelType"
          :existing-channels="existingCustomChannels"
          v-model:placement="placement"
          @added="onQuickAddSuccess"
        />

        <!-- 标准模式（现有 textarea 解析） -->
        <div v-else class="d-flex flex-column ga-3">
          <v-textarea
            v-model="quickInput"
            :label="t('addChannel.quickInputLabel')"
            :placeholder="t('addChannel.quickInputPlaceholder')"
            variant="outlined"
            rows="10"
            no-resize
            autofocus
            class="quick-input-textarea"
            @input="parseQuickInput"
          />

          <v-card variant="outlined" class="detection-status-card" rounded="lg">
            <v-card-text class="pa-4">
              <div class="d-flex flex-column ga-3">
                <div class="d-flex align-start ga-3">
                  <v-icon :color="detectedBaseUrls.length > 0 ? 'success' : 'error'" size="20" class="mt-1">
                    {{ detectedBaseUrls.length > 0 ? 'mdi-check-circle' : 'mdi-alert-circle' }}
                  </v-icon>
                  <div class="flex-grow-1">
                    <div class="text-body-2 font-weight-medium">{{ t('addChannel.baseUrl') }}</div>
                    <div v-if="detectedBaseUrls.length === 0" class="text-caption text-error">
                      {{ t('addChannel.enterValidUrl') }}
                    </div>
                    <div v-else class="d-flex flex-column ga-2 mt-1">
                      <div v-for="url in detectedBaseUrls" :key="url" class="base-url-item">
                        <div class="text-caption text-success">{{ url }}</div>
                        <div class="text-caption text-medium-emphasis mt-1">
                          {{ t('addChannel.expectedRequest') }}
                        </div>
                        <div class="expected-request-list mt-1">
                          <div
                            v-for="item in getExpectedRequestUrls(url)"
                            :key="`${item.protocol}:${item.expectedUrl}`"
                            class="expected-request-row"
                          >
                            <span class="text-caption font-weight-medium">{{ expectedProtocolLabel(item.protocol) }}</span>
                            <span class="text-caption text-medium-emphasis expected-request-url">{{ item.expectedUrl }}</span>
                          </div>
                        </div>
                      </div>
                    </div>
                  </div>
                  <v-chip v-if="detectedBaseUrls.length > 0" size="x-small" color="success" variant="tonal">
                    {{ t('addChannel.count', { count: detectedBaseUrls.length }) }}
                  </v-chip>
                </div>

                <v-row dense>
                  <v-col cols="12" md="7">
                    <div class="d-flex flex-column ga-3">
                      <div class="d-flex align-center ga-3 pa-3 bg-grey-lighten-4 rounded-lg">
                        <v-icon color="primary" size="20">mdi-tag</v-icon>
                        <div class="flex-grow-1">
                          <div class="text-caption text-medium-emphasis">{{ t('addChannel.channelName') }}</div>
                          <div class="text-body-2 font-weight-bold text-primary">
                            {{ generatedChannelName }}
                          </div>
                        </div>
                        <v-chip size="x-small" color="primary" variant="tonal">
                          {{ t('common.autoGenerated') }}
                        </v-chip>
                      </div>
                    </div>
                  </v-col>

                  <v-col cols="12" md="5">
                    <div class="d-flex flex-column ga-3">
                      <div class="d-flex align-center ga-3 pa-3 rounded-lg apikeys-card">
                        <v-icon :color="apiKeyStatusColor" size="20">
                          {{ apiKeyStatusIcon }}
                        </v-icon>
                        <div class="flex-grow-1">
                          <div class="text-body-2 font-weight-medium">{{ t('addChannel.apiKeys') }}</div>
                          <div class="text-caption" :class="apiKeyStatusTextClass">
                            {{ apiKeyStatusMessage }}
                          </div>
                        </div>
                        <v-chip v-if="detectedApiKeys.length > 0" size="x-small" color="success" variant="tonal">
                          {{ t('addChannel.count', { count: detectedApiKeys.length }) }}
                        </v-chip>
                      </div>

                      <!-- 故障转移位置（与 desktop 一致：API Keys 下方开关） -->
                      <div
                        class="d-flex align-center ga-3 pa-3 rounded-lg placement-card"
                        :class="{ 'placement-card-disabled': isCreatingChannel }"
                        @click="togglePlacement"
                      >
                        <v-icon color="primary" size="20">mdi-arrow-down-to-line</v-icon>
                        <div class="flex-grow-1">
                          <div class="text-body-2 font-weight-medium">{{ t('addChannel.placementBack') }}</div>
                        </div>
                        <v-switch
                          :model-value="placement === 'back'"
                          readonly
                          hide-details
                          density="compact"
                          color="primary"
                          class="flex-grow-0 placement-switch"
                        />
                      </div>
                    </div>
                  </v-col>
                </v-row>

                <v-alert
                  v-if="quickServiceType === 'copilot'"
                  class="mt-3"
                  color="info"
                  variant="tonal"
                  density="comfortable"
                >
                  {{ t('copilotOAuth.quickAddHint') }}
                </v-alert>
              </div>
            </v-card-text>
          </v-card>

          <v-alert
            v-if="duplicateChannel"
            color="info"
            variant="tonal"
            density="comfortable"
            icon="mdi-content-duplicate"
          >
            {{ t('autopilot.quickAdd.alreadyAdded', { name: duplicateChannel.channel.logicalName || duplicateChannel.channel.name }) }}
          </v-alert>

          <v-alert
            v-if="standardSubmitError"
            color="error"
            variant="tonal"
            density="comfortable"
            icon="mdi-alert-circle-outline"
          >
            {{ standardSubmitError }}
          </v-alert>
        </div>
      </v-card-text>

      <v-card-actions class="pa-6 pt-0">
        <v-spacer />
        <v-btn variant="outlined" @click="handleCancel">
          {{ t('app.actions.cancel') }}
          <span class="shortcut-hint ml-2 text-xs opacity-50">Esc</span>
        </v-btn>
        <v-btn
          color="primary"
          variant="elevated"
          :disabled="quickAddMode ? !quickAddFormRef?.isFormValid : !isQuickFormValid || standardSubmitting"
          :loading="quickAddMode ? quickAddFormRef?.submitting : standardSubmitting"
          prepend-icon="mdi-check"
          @click="handleSubmitByMode"
        >
          {{ t('addChannel.createChannel') }}
          <span class="shortcut-hint ml-2 text-xs opacity-50">{{ isMac ? '⌘Enter' : 'Ctrl+Enter' }}</span>
        </v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
</template>

<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useTheme } from 'vuetify'
import type { Channel, ChannelPlacement } from '../services/api'
import {
  buildDiscoveryExpectedRequestUrls,
  buildExpectedRequestUrls,
  type DiscoveryProtocol
} from '../utils/expectedRequestUrls'
import { parseQuickInput as parseQuickInputUtil } from '../utils/quickInputParser'
import { buildQuickAddChannelName, findExistingQuickAddChannel } from '../utils/quickAddChannel'
import { useI18n } from '../i18n'
import { useAuthStore } from '../stores/auth'
import { useChannelStore } from '../stores/channel'
import { usePreferencesStore } from '../stores/preferences'
import {
  autoAddChannel,
  discoverFast,
  extractAutoAddErrorMessage,
  preloadProviderTemplates
} from '../services/autopilot-api'
import QuickAddChannelForm from './QuickAddChannelForm.vue'

type ServiceType = 'openai' | 'gemini' | 'claude' | 'responses' | 'copilot'
type ChannelType = 'messages' | 'chat' | 'responses' | 'gemini' | 'images' | 'vectors'

interface Props {
  show: boolean
  channelType?: ChannelType
}

const props = withDefaults(defineProps<Props>(), {
  channelType: 'messages'
})

const emit = defineEmits<{
  'update:show': [value: boolean]
  save: [
    channel: Omit<Channel, 'index' | 'latency' | 'status'>,
    options?: { isQuickAdd?: boolean; placement?: ChannelPlacement }
  ]
  error: [message: string]
  autoAdded: [channelId: number]
}>()

const { t } = useI18n()
const theme = useTheme()
const authStore = useAuthStore()
const channelStore = useChannelStore()
const preferencesStore = usePreferencesStore()

const quickInput = ref('')
const detectedBaseUrl = ref('')
const detectedBaseUrls = ref<string[]>([])
const detectedApiKeys = ref<string[]>([])
// 标准模式先做本地预览识别，提交时仍以真实协议探测结果为准。
const quickServiceType = ref<ServiceType>(getDefaultServiceTypeValue())
const randomSuffix = ref(generateRandomString(6))
const standardSubmitting = ref(false)
const standardSubmitError = ref('')

// 新渠道故障转移位置：默认跟随全局偏好（top=front / bottom=back）
const defaultPlacement = (): ChannelPlacement =>
  preferencesStore.newChannelPlacement === 'bottom' ? 'back' : 'front'
const placement = ref<ChannelPlacement>(defaultPlacement())

// 快速添加模式
const quickAddMode = ref(false)
const quickAddFormRef = ref<InstanceType<typeof QuickAddChannelForm> | null>(null)

const isMac = computed(() => typeof navigator !== 'undefined' && /Mac|iPod|iPhone|iPad/.test(navigator.platform))
const isCreatingChannel = computed(() =>
  quickAddMode.value ? Boolean(quickAddFormRef.value?.submitting) : standardSubmitting.value
)

// 切换故障转移位置（back=追加到末尾 / front=置顶优先），创建中禁止切换
function togglePlacement() {
  if (isCreatingChannel.value) return
  placement.value = placement.value === 'back' ? 'front' : 'back'
}

const headerClasses = computed(() => {
  const isDark = theme.global.current.value.dark
  return isDark ? 'bg-surface text-high-emphasis' : 'bg-primary text-white'
})

const avatarColor = computed(() => 'primary')

const headerIconStyle = computed(() => ({
  color: 'rgb(var(--v-theme-on-primary))'
}))

const subtitleClasses = computed(() => {
  const isDark = theme.global.current.value.dark
  return isDark ? 'text-medium-emphasis' : 'text-white-subtitle'
})

const isCopilotQuickAdd = computed(() => quickServiceType.value === 'copilot')

const apiKeyStatusColor = computed(() => {
  if (detectedApiKeys.value.length > 0) return 'success'
  return isCopilotQuickAdd.value ? 'info' : 'error'
})

const apiKeyStatusIcon = computed(() => {
  if (detectedApiKeys.value.length > 0) return 'mdi-check-circle'
  return isCopilotQuickAdd.value ? 'mdi-information' : 'mdi-alert-circle'
})

const apiKeyStatusTextClass = computed(() => {
  if (detectedApiKeys.value.length > 0) return 'text-success'
  return isCopilotQuickAdd.value ? 'text-info' : 'text-error'
})

const apiKeyStatusMessage = computed(() => {
  if (detectedApiKeys.value.length > 0) {
    return t('addChannel.detectedKeys', { count: detectedApiKeys.value.length })
  }
  return isCopilotQuickAdd.value ? t('copilotOAuth.quickAddKeyHint') : t('addChannel.enterApiKey')
})

const generatedChannelName = computed(() => {
  return buildQuickAddChannelName(detectedBaseUrl.value, randomSuffix.value)
})

const existingCustomChannels = computed(() => [
  ...(channelStore.channelsData.channels ?? []),
  ...(channelStore.chatChannelsData.channels ?? []),
  ...(channelStore.responsesChannelsData.channels ?? []),
  ...(channelStore.geminiChannelsData.channels ?? []),
  ...(channelStore.imagesChannelsData.channels ?? []),
  ...(channelStore.vectorsChannelsData.channels ?? [])
].filter(channel => !channel.providerId))

const duplicateChannel = computed(() =>
  findExistingQuickAddChannel(detectedBaseUrls.value, existingCustomChannels.value)
)

const isQuickFormValid = computed(() => {
  if (isCopilotQuickAdd.value) {
    return detectedBaseUrls.value.length > 0
  }
  return detectedBaseUrls.value.length > 0 && detectedApiKeys.value.length > 0
})

function getDefaultServiceTypeValue(): ServiceType {
  if (props.channelType === 'chat') return 'openai'
  if (props.channelType === 'gemini') return 'gemini'
  if (props.channelType === 'responses') return 'responses'
  if (props.channelType === 'images' || props.channelType === 'vectors') return 'openai'
  return 'claude'
}

function generateRandomString(length: number): string {
  const chars = 'abcdefghijklmnopqrstuvwxyz0123456789'
  let result = ''
  for (let i = 0; i < length; i++) {
    result += chars.charAt(Math.floor(Math.random() * chars.length))
  }
  return result
}

function parseQuickInput() {
  standardSubmitError.value = ''
  const fallbackServiceType = getDefaultServiceTypeValue()
  const result = parseQuickInputUtil(quickInput.value, fallbackServiceType)
  detectedBaseUrl.value = result.detectedBaseUrl
  detectedBaseUrls.value = result.detectedBaseUrls
  detectedApiKeys.value = result.detectedApiKeys
  const detectedServiceType =
    props.channelType === 'images' || props.channelType === 'vectors' ? 'openai' : result.detectedServiceType
  quickServiceType.value = detectedServiceType || fallbackServiceType
}

function getExpectedRequestUrls(inputBaseUrl: string) {
  if (!inputBaseUrl) return []
  if (
    props.channelType !== 'images' &&
    props.channelType !== 'vectors' &&
    quickServiceType.value !== 'copilot'
  ) {
    return buildDiscoveryExpectedRequestUrls(inputBaseUrl)
  }
  const serviceType =
    props.channelType === 'images' || props.channelType === 'vectors' ? 'openai' : quickServiceType.value
  return buildExpectedRequestUrls(props.channelType, serviceType, inputBaseUrl).map(item => ({
    ...item,
    protocol: props.channelType
  }))
}

function expectedProtocolLabel(protocol: DiscoveryProtocol | ChannelType): string {
  const labels: Record<DiscoveryProtocol | ChannelType, string> = {
    messages: 'Messages',
    chat: 'Chat',
    responses: 'Responses',
    gemini: 'Gemini',
    images: 'Images',
    vectors: 'Vectors'
  }
  return labels[protocol]
}

function resetQuickState() {
  quickInput.value = ''
  detectedBaseUrl.value = ''
  detectedBaseUrls.value = []
  detectedApiKeys.value = []
  quickServiceType.value = getDefaultServiceTypeValue()
  randomSuffix.value = generateRandomString(6)
  standardSubmitting.value = false
  standardSubmitError.value = ''
  placement.value = defaultPlacement()
}

async function handleQuickSubmit() {
  parseQuickInput()
  if (!isQuickFormValid.value || standardSubmitting.value) return

  // Copilot 在 OAuth 前没有可用于协议探测的凭据，保留原有创建后授权流程。
  if (isCopilotQuickAdd.value) {
    emit(
      'save',
      {
        name: generatedChannelName.value,
        serviceType: 'copilot',
        baseUrl: detectedBaseUrl.value,
        baseUrls: detectedBaseUrls.value,
        apiKeys: detectedApiKeys.value,
        modelMapping: {},
        normalizeMetadataUserId: false
      },
      { isQuickAdd: true, placement: placement.value }
    )
    return
  }

  standardSubmitting.value = true
  standardSubmitError.value = ''
  try {
    const routeDiscovery = await discoverFast(props.channelType, detectedBaseUrls.value, detectedApiKeys.value)
    if (!routeDiscovery) {
      throw new Error(t('autopilot.quickAdd.discoveryFailed'))
    }
    const targetChannelType = routeDiscovery.primaryKind
    const result = await autoAddChannel(targetChannelType, {
      name: generatedChannelName.value,
      baseUrls: detectedBaseUrls.value,
      apiKeys: detectedApiKeys.value,
      routes: routeDiscovery.routes,
      rateLimitHint: routeDiscovery.rateLimitHint,
      placement: placement.value
    })
    const currentChannel = result.channels?.find(channel => channel.channelKind === targetChannelType)
    onQuickAddSuccess(currentChannel?.index ?? result.index)
  } catch (err) {
    standardSubmitError.value = extractAutoAddErrorMessage(err)
    console.error('[StandardAdd-Submit] 自动添加渠道失败:', err)
  } finally {
    standardSubmitting.value = false
  }
}

function handleSubmitByMode() {
  if (quickAddMode.value) {
    quickAddFormRef.value?.handleSubmit()
  } else {
    void handleQuickSubmit()
  }
}

function onQuickAddSuccess(channelId: number) {
  emit('autoAdded', channelId)
  quickAddFormRef.value?.resetForm()
  resetQuickState()
  quickAddMode.value = false
}

function handleCancel() {
  emit('update:show', false)
  resetQuickState()
  quickAddMode.value = false
  quickAddFormRef.value?.resetForm()
}

function handleKeydown(event: KeyboardEvent) {
  if (!props.show) return

  if (event.key === 'Escape') {
    event.preventDefault()
    handleCancel()
    return
  }

  if (event.key === 'Enter' && (event.metaKey || event.ctrlKey) && !event.shiftKey) {
    event.preventDefault()
    handleSubmitByMode()
  }
}

watch(
  () => props.show,
  show => {
    if (show) {
      if (authStore.isAuthenticated) void preloadProviderTemplates()
      resetQuickState()
    }
  },
  { immediate: true }
)

watch(
  () => authStore.apiKey,
  apiKey => {
    if (apiKey) void preloadProviderTemplates()
  },
  { immediate: true }
)

onMounted(() => {
  document.addEventListener('keydown', handleKeydown)
})

onUnmounted(() => {
  document.removeEventListener('keydown', handleKeydown)
})
</script>

<style scoped>
.add-channel-dialog {
  display: flex;
  flex-direction: column;
  max-height: calc(100dvh - 48px);
  overflow: hidden;
}

.add-channel-dialog > .v-card-text {
  min-height: 0;
  overflow-y: auto;
}

.modal-header-text {
  min-width: 0;
}

.modal-title {
  font-size: 1.125rem;
  font-weight: 700;
  line-height: 1.2;
}

.modal-subtitle {
  margin-top: 2px;
  font-size: 0.875rem;
  line-height: 1.35;
}

.text-white-subtitle {
  color: rgba(255, 255, 255, 0.86);
}

.quick-input-textarea {
  width: 100%;
}

.detection-status-card {
  border-color: rgba(var(--v-theme-outline), 0.32);
}

.base-url-item {
  padding: 2px 0;
}

.expected-request-list {
  display: flex;
  flex-direction: column;
  gap: 2px;
}

.expected-request-row {
  display: grid;
  grid-template-columns: 92px minmax(0, 1fr);
  gap: 8px;
  align-items: start;
}

.expected-request-url {
  overflow-wrap: anywhere;
}

@media (max-width: 600px) {
  .expected-request-list {
    gap: 6px;
  }

  .expected-request-row {
    grid-template-columns: minmax(0, 1fr);
    gap: 0;
  }

  .expected-request-url {
    padding-left: 12px;
  }
}

.apikeys-card {
  border: 1px solid rgba(var(--v-theme-outline), 0.32);
}

.placement-card {
  border: 1px solid rgba(var(--v-theme-outline), 0.32);
  cursor: pointer;
  transition:
    border-color 0.15s ease,
    background-color 0.15s ease;
}

.placement-card:hover {
  border-color: rgba(var(--v-theme-primary), 0.4);
  background-color: rgba(var(--v-theme-primary), 0.05);
}

.placement-card-disabled {
  cursor: default;
  opacity: 0.6;
  pointer-events: none;
}

.placement-switch {
  pointer-events: none;
}

.shortcut-hint {
  font-size: 0.75rem;
  opacity: 0.55;
}

.key-tooltip {
  max-width: 320px;
}
</style>
