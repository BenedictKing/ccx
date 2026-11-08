<template>
  <div class="api-key-management-section">
    <v-card variant="outlined" rounded="lg" :color="hasConfigurableKeys ? undefined : 'error'">
      <v-card-title class="d-flex align-center justify-space-between pa-4 pb-2">
        <div class="d-flex align-center ga-2">
          <v-icon :color="hasConfigurableKeys ? 'primary' : 'error'">mdi-key</v-icon>
          <span class="section-title">
            {{ t('channelCard.apiKeyManagement') }}<span v-if="serviceType !== 'copilot'"> *</span>
          </span>
          <v-chip v-if="!hasConfigurableKeys" size="x-small" color="error" variant="tonal">
            {{ t('channelEditor.auth.apiKeyRequired') }}
          </v-chip>
        </div>
        <v-chip size="small" color="info" variant="tonal">
          {{ t('addChannel.apiKeyLoadBalance') }}
        </v-chip>
      </v-card-title>

      <v-card-text class="pt-2">
        <!-- 现有密钥列表 -->
        <div v-if="apiKeys.length" class="mb-4">
          <v-list density="compact" class="bg-transparent">
            <v-list-item
              v-for="(key, index) in apiKeys"
              :key="index"
              class="mb-2"
              rounded="lg"
              variant="tonal"
              :color="duplicateKeyIndex === index ? 'error' : 'surface-variant'"
              :class="{ 'animate-pulse': duplicateKeyIndex === index }"
            >
              <template #prepend>
                <v-icon size="small" :color="duplicateKeyIndex === index ? 'error' : 'primary'">
                  {{ duplicateKeyIndex === index ? 'mdi-alert' : 'mdi-key' }}
                </v-icon>
              </template>

              <v-list-item-title>
                <div class="d-flex align-center justify-space-between">
                  <code class="text-caption">{{ maskApiKey(key) }}</code>
                  <div class="d-flex align-center ga-1">
                    <!-- Models 状态标签 -->
                    <v-chip
                      v-if="keyModelsStatus.get(key)?.loading"
                      size="x-small"
                      color="info"
                      variant="tonal"
                    >
                      <v-icon start size="12">mdi-loading</v-icon>
                      {{ t('addChannel.checking') }}
                    </v-chip>
                    <v-chip
                      v-else-if="keyModelsStatus.get(key)?.success"
                      size="x-small"
                      color="success"
                      variant="tonal"
                    >
                      {{ t('addChannel.modelsCount', { statusCode: keyModelsStatus.get(key)?.statusCode ?? 'OK', count: keyModelsStatus.get(key)?.modelCount ?? 0 }) }}
                    </v-chip>
                    <v-tooltip
                      v-else-if="keyModelsStatus.get(key)?.error"
                      :text="keyModelsStatus.get(key)?.error"
                      location="top"
                      max-width="300"
                      content-class="key-tooltip"
                    >
                      <template #activator="{ props: tooltipProps }">
                        <v-chip
                          v-bind="tooltipProps"
                          size="x-small"
                          color="error"
                          variant="tonal"
                        >
                          models {{ keyModelsStatus.get(key)?.statusCode || 'ERR' }}
                        </v-chip>
                      </template>
                    </v-tooltip>
                    <!-- 重复密钥标签 -->
                    <v-chip v-if="duplicateKeyIndex === index" size="x-small" color="error" variant="text">
                      {{ t('channelEditor.auth.duplicateKey') }}
                    </v-chip>
                  </div>
                </div>
              </v-list-item-title>

              <v-list-item-subtitle class="d-flex align-center ga-2 mt-1">
                <span class="text-caption">{{ t('channelEditor.auth.groupMultiplier') }}</span>
                <v-text-field
                  :model-value="getGroupMultiplierInput(key)"
                  type="number"
                  min="0"
                  step="any"
                  variant="outlined"
                  density="compact"
                  hide-details
                  :aria-label="t('channelEditor.auth.groupMultiplier')"
                  class="group-multiplier-input"
                  @update:model-value="stageGroupMultiplier(key, $event)"
                  @blur="commitGroupMultiplier(key)"
                  @keydown.enter="commitGroupMultiplier(key)"
                />
              </v-list-item-subtitle>

              <template #append>
                <div class="d-flex align-center ga-1">
                  <!-- 置顶/置底：仅首尾密钥显示 -->
                  <v-tooltip
                    v-if="index === apiKeys.length - 1 && apiKeys.length > 1"
                    :text="t('channelCard.moveTop')"
                    location="top"
                    :open-delay="150"
                    content-class="key-tooltip"
                  >
                    <template #activator="{ props: tooltipProps }">
                      <v-btn
                        v-bind="tooltipProps"
                        size="small"
                        color="warning"
                        icon
                        variant="text"
                        rounded="md"
                        @click="moveToTop(index)"
                      >
                        <v-icon size="small">mdi-arrow-up-bold</v-icon>
                      </v-btn>
                    </template>
                  </v-tooltip>
                  <v-tooltip
                    v-if="index === 0 && apiKeys.length > 1"
                    :text="t('channelCard.moveBottom')"
                    location="top"
                    :open-delay="150"
                    content-class="key-tooltip"
                  >
                    <template #activator="{ props: tooltipProps }">
                      <v-btn
                        v-bind="tooltipProps"
                        size="small"
                        color="warning"
                        icon
                        variant="text"
                        rounded="md"
                        @click="moveToBottom(index)"
                      >
                        <v-icon size="small">mdi-arrow-down-bold</v-icon>
                      </v-btn>
                    </template>
                  </v-tooltip>
                  <v-tooltip
                    :text="copiedKeyIndex === index ? t('channelCard.copied') : t('channelCard.copyKey')"
                    location="top"
                    :open-delay="150"
                    content-class="key-tooltip"
                  >
                    <template #activator="{ props: tooltipProps }">
                      <v-btn
                        v-bind="tooltipProps"
                        size="small"
                        :color="copiedKeyIndex === index ? 'success' : 'primary'"
                        icon
                        variant="text"
                        @click="copyKey(key, index)"
                      >
                        <v-icon size="small">{{
                          copiedKeyIndex === index ? 'mdi-check' : 'mdi-content-copy'
                        }}</v-icon>
                      </v-btn>
                    </template>
                  </v-tooltip>
                  <v-tooltip :text="t('addChannel.deleteKey')" location="top" :open-delay="150" content-class="key-tooltip">
                    <template #activator="{ props: tooltipProps }">
                      <v-btn
                        v-bind="tooltipProps"
                        size="small"
                        color="error"
                        icon
                        variant="text"
                        @click="removeKey(index)"
                      >
                        <v-icon size="small" color="error">mdi-close</v-icon>
                      </v-btn>
                    </template>
                  </v-tooltip>
                </div>
              </template>
            </v-list-item>
          </v-list>
        </div>

        <!-- 添加新密钥 -->
        <div class="d-flex align-start ga-3">
          <v-text-field
            v-model="newApiKey"
            :label="t('addChannel.addNewApiKey')"
            :placeholder="t('channelEditor.auth.addNewApiKey.placeholder')"
            prepend-inner-icon="mdi-plus"
            variant="outlined"
            density="comfortable"
            type="password"
            :error="!!apiKeyError"
            :error-messages="apiKeyError"
            class="flex-grow-1"
            @keyup.enter="handleAddKey"
            @input="handleInput"
          />
          <v-btn
            color="primary"
            variant="elevated"
            size="large"
            height="40"
            :disabled="!newApiKey.trim()"
            class="mt-1"
            @click="handleAddKey"
          >
            {{ t('app.actions.add') }}
          </v-btn>
        </div>

        <v-alert
          v-if="serviceType === 'copilot'"
          class="mt-3"
          color="info"
          variant="tonal"
          density="comfortable"
        >
          <div class="d-flex flex-column ga-2">
            <div class="text-body-2">{{ t('copilotOAuth.description') }}</div>
            <v-text-field
              :model-value="proxyUrl || ''"
              :label="t('channelEditor.transport.proxyUrl.label')"
              :placeholder="t('channelEditor.transport.proxyUrl.placeholder')"
              :hint="t('channelEditor.transport.proxyUrl.hint')"
              prepend-inner-icon="mdi-shield-lock-outline"
              variant="outlined"
              density="compact"
              clearable
              persistent-hint
              @update:model-value="$emit('update:proxyUrl', String($event || ''))"
            />
            <div v-if="copilotUserCode" class="d-flex align-center flex-wrap ga-2">
              <span class="text-body-2">{{ t('copilotOAuth.userCode') }}</span>
              <code class="px-2 py-1 rounded bg-surface">{{ copilotUserCode }}</code>
              <v-btn
                size="small"
                color="primary"
                variant="text"
                :href="copilotVerificationUri"
                target="_blank"
                rel="noopener noreferrer"
              >
                <v-icon start size="small">mdi-open-in-new</v-icon>
                {{ t('copilotOAuth.openAuthorize') }}
              </v-btn>
            </div>
            <v-alert v-if="copilotOAuthSuccess" color="success" variant="tonal" density="compact">
              {{ t('copilotOAuth.success') }}
            </v-alert>
            <v-alert v-if="copilotOAuthError" color="error" variant="tonal" density="compact">
              {{ copilotOAuthError }}
            </v-alert>
            <div class="d-flex align-center ga-2">
              <v-btn
                color="primary"
                variant="tonal"
                :loading="copilotOAuthLoading"
                @click="startCopilotOAuth"
              >
                <v-icon start>mdi-code-braces</v-icon>
                {{ t('copilotOAuth.button') }}
              </v-btn>
              <span v-if="copilotPolling" class="text-caption text-medium-emphasis">
                {{ t('copilotOAuth.waiting') }}
              </span>
            </div>
            <div class="d-flex align-center ga-2 mt-1">
              <v-btn
                v-if="copilotPolling || copilotOAuthLoading"
                size="small"
                color="warning"
                variant="text"
                @click="cancelCopilotOAuth"
              >
                {{ t('copilotOAuth.cancel') }}
              </v-btn>
              <v-btn
                v-if="copilotOAuthError"
                size="small"
                color="primary"
                variant="text"
                @click="retryCopilotOAuth"
              >
                {{ t('copilotOAuth.retry') }}
              </v-btn>
            </div>
            <div v-if="serviceType === 'copilot' && isEditing && channelId !== undefined" class="mt-3 d-flex flex-column ga-2">
              <div class="d-flex align-center ga-2">
                <v-btn
                  color="secondary"
                  variant="tonal"
                  :loading="copilotDiagnoseLoading"
                  @click="diagnoseCopilotChannel"
                >
                  {{ t('copilotDiagnose.button') }}
                </v-btn>
                <span v-if="copilotDiagnoseLoading" class="text-caption text-medium-emphasis">
                  {{ t('copilotDiagnose.loading') }}
                </span>
              </div>
              <v-alert v-if="copilotDiagnoseError" color="error" variant="tonal" density="compact">
                {{ copilotDiagnoseError }}
              </v-alert>
              <v-alert v-if="copilotDiagnoseResult" color="info" variant="tonal" density="compact" class="text-caption">
                <div class="d-flex flex-column ga-2">
                  <div class="d-flex align-center ga-2">
                    <v-chip :color="copilotDiagnoseResult.githubUser ? 'success' : 'warning'" size="small" variant="tonal">
                      GitHub
                    </v-chip>
                    <span v-if="copilotDiagnoseResult.githubUser">{{ copilotDiagnoseResult.githubUser.login }}</span>
                    <span v-else>{{ copilotDiagnoseResult.githubUserError }}</span>
                  </div>
                  <div class="d-flex align-center ga-2">
                    <v-chip :color="copilotDiagnoseResult.tokenError ? 'error' : 'success'" size="small" variant="tonal">
                      Token
                    </v-chip>
                    <span v-if="copilotDiagnoseResult.tokenError">{{ copilotDiagnoseResult.tokenError }}</span>
                    <span v-else>{{ copilotDiagnoseResult.copilotBaseUrl }}</span>
                  </div>
                  <div class="d-flex align-center ga-2">
                    <v-chip :color="copilotDiagnoseResult.modelsError ? 'error' : (copilotDiagnoseResult.modelsStatus && copilotDiagnoseResult.modelsStatus < 400 ? 'success' : 'warning')" size="small" variant="tonal">
                      Models
                    </v-chip>
                    <span v-if="copilotDiagnoseResult.modelsError">{{ copilotDiagnoseResult.modelsError }}</span>
                    <span v-else>{{ copilotDiagnoseResult.modelsStatus }} {{ copilotDiagnoseResult.modelsUrl }}</span>
                  </div>
                </div>
              </v-alert>
            </div>
          </div>
        </v-alert>

        <!-- 被拉黑的密钥（仅编辑模式） -->
        <div v-if="isEditing && visibleDisabledKeys.length" class="mt-4">
          <div class="d-flex align-center ga-2 mb-2">
            <v-icon size="small" color="error">mdi-key-remove</v-icon>
            <span class="text-body-2 font-weight-medium text-error">{{ t('channelCard.disabledKeys') }}</span>
            <v-chip size="x-small" color="error" variant="tonal">{{ visibleDisabledKeys.length }}</v-chip>
          </div>
          <v-list density="compact" class="rounded-lg" style="max-height: 150px; overflow-y: auto;">
            <v-list-item
              v-for="(dk, dkIdx) in visibleDisabledKeys"
              :key="'disabled-' + dkIdx"
              class="px-3"
              style="background: rgba(var(--v-theme-error), 0.04);"
            >
              <template #prepend>
                <v-icon size="small" color="error" class="mr-2">mdi-key-alert</v-icon>
              </template>
              <v-list-item-title class="text-caption font-weight-mono">
                {{ dk.key.length > 20 ? dk.key.slice(0, 8) + '***' + dk.key.slice(-5) : dk.key }}
              </v-list-item-title>
              <v-list-item-subtitle class="d-flex align-center ga-1">
                <v-chip size="x-small" :color="dk.reason === 'insufficient_balance' ? 'warning' : 'error'" variant="tonal">
                  {{ t(getDisabledKeyLabel(dk.reason)) }}
                </v-chip>
                <span class="text-caption">{{ new Date(dk.disabledAt).toLocaleDateString() }}</span>
              </v-list-item-subtitle>
              <template #append>
                <v-btn
                  size="x-small"
                  color="success"
                  variant="tonal"
                  rounded="lg"
                  :loading="restoringKey === dk.key"
                  @click="$emit('restore-key', dk.key)"
                >
                  <v-icon start size="small">mdi-restore</v-icon>
                  {{ t('channelCard.restoreKey') }}
                </v-btn>
              </template>
            </v-list-item>
          </v-list>
        </div>

        <!-- 被限制的 (Key, 模型) 组合（仅编辑模式） -->
        <div v-if="isEditing && visibleDisabledKeyModels.length" class="mt-4">
          <div class="d-flex align-center ga-2 mb-2">
            <v-icon size="small" color="warning">mdi-alert-circle-outline</v-icon>
            <span class="text-body-2 font-weight-medium text-warning">{{ t('channelCard.disabledKeyModels') }}</span>
            <v-chip size="x-small" color="warning" variant="tonal">{{ visibleDisabledKeyModels.length }}</v-chip>
          </div>
          <v-list density="compact" class="rounded-lg" style="max-height: 150px; overflow-y: auto;">
            <v-list-item
              v-for="(dm, dmIdx) in visibleDisabledKeyModels"
              :key="'disabled-model-' + dmIdx"
              class="px-3"
              style="background: rgba(var(--v-theme-warning), 0.04);"
            >
              <template #prepend>
                <v-icon size="small" color="warning" class="mr-2">mdi-key-alert</v-icon>
              </template>
              <v-list-item-title class="text-caption font-weight-mono">
                {{ dm.key.length > 20 ? dm.key.slice(0, 8) + '***' + dm.key.slice(-5) : dm.key }}
                <v-chip size="x-small" color="primary" variant="tonal" class="ml-1">{{ dm.model }}</v-chip>
              </v-list-item-title>
              <v-list-item-subtitle class="d-flex align-center ga-1">
                <v-chip size="x-small" color="warning" variant="tonal">{{ t('channelCard.modelNotFound') }}</v-chip>
                <span class="text-caption">{{ t('channelCard.recoverAt') }}: {{ new Date(dm.recoverAt).toLocaleString() }}</span>
              </v-list-item-subtitle>
              <template #append>
                <v-btn
                  size="x-small"
                  color="success"
                  variant="tonal"
                  rounded="lg"
                  :loading="restoringKeyModel === (dm.key + '|' + dm.model)"
                  @click="$emit('restore-key-model', dm.key, dm.model)"
                >
                  <v-icon start size="small">mdi-restore</v-icon>
                  {{ t('channelCard.restoreKey') }}
                </v-btn>
              </template>
            </v-list-item>
          </v-list>
        </div>
      </v-card-text>
    </v-card>
  </div>
</template>

<script setup lang="ts">
import { ref, computed, onBeforeUnmount } from 'vue'
import { useI18n } from '../../i18n'
import { ApiService } from '../../services/api'
import type { APIKeyConfig } from '../../services/api-types'
import { maskApiKey } from '../../utils/apiKeyMask'

interface KeyModelsStatus {
  loading?: boolean
  success?: boolean
  error?: string
  statusCode?: string | number
  modelCount?: number
}

interface DisabledKey {
  key: string
  reason: string
  disabledAt: string
}

interface DisabledKeyModel {
  key: string
  model: string
  reason: string
  disabledAt: string
  recoverAt: string
}

interface Props {
  apiKeys: string[]
  disabledKeys: DisabledKey[]
  disabledKeyModels?: DisabledKeyModel[]
  keyModelsStatus: Map<string, KeyModelsStatus>
  isEditing: boolean
  restoringKey: string
  restoringKeyModel?: string
  serviceType?: string
  channelId?: number
  proxyUrl?: string
  apiKeyConfigs?: APIKeyConfig[]
}

const props = defineProps<Props>()

const emit = defineEmits<{
  'update:apiKeys': [string[]]
  'update:apiKeyConfigs': [APIKeyConfig[]]
  'update:proxyUrl': [string]
  'restore-key': [string]
  'restore-key-model': [string, string]
}>()

const { t } = useI18n()
const apiService = new ApiService()

const newApiKey = ref('')
const apiKeyError = ref('')
const duplicateKeyIndex = ref<number | null>(null)
const copiedKeyIndex = ref<number | null>(null)
interface CopilotDiagnoseResponse {
  githubUser?: {
    login?: string
    id?: number
  }
  githubUserError?: string
  copilotBaseUrl?: string
  tokenError?: string
  tokenErrorKind?: string
  modelsUrl?: string
  modelsStatus?: number
  modelsError?: string
  modelsBodyPrefix?: string
}

const copilotOAuthLoading = ref(false)
const copilotPolling = ref(false)
const copilotOAuthError = ref('')
const copilotOAuthSuccess = ref(false)
const copilotDeviceCode = ref('')
const copilotUserCode = ref('')
const copilotVerificationUri = ref('')
const copilotDiagnoseLoading = ref(false)
const copilotDiagnoseResult = ref<CopilotDiagnoseResponse | null>(null)
const copilotDiagnoseError = ref('')
let copilotPollTimer: number | null = null

const hasConfigurableKeys = computed(() => props.serviceType === 'copilot' || props.apiKeys.length > 0)
const groupMultiplierDrafts = ref(new Map<string, string>())

const getGroupMultiplier = (key: string): number | null => {
  return props.apiKeyConfigs?.find(config => config.key === key)?.groupMultiplier ?? null
}

const getGroupMultiplierInput = (key: string): string | number => {
  return groupMultiplierDrafts.value.get(key) ?? getGroupMultiplier(key) ?? ''
}

const stageGroupMultiplier = (key: string, value: unknown) => {
  const drafts = new Map(groupMultiplierDrafts.value)
  const raw = value == null ? '' : String(value)
  drafts.set(key, raw)
  groupMultiplierDrafts.value = drafts

  const groupMultiplier = raw.trim() === '' ? null : Number(raw.trim())
  if (groupMultiplier === null || (Number.isFinite(groupMultiplier) && groupMultiplier >= 0)) {
    saveGroupMultiplier(key, groupMultiplier)
  }
}

const commitGroupMultiplier = (key: string) => {
  const drafts = new Map(groupMultiplierDrafts.value)
  const raw = drafts.get(key)
  if (raw === undefined) return

  const groupMultiplier = raw.trim() === '' ? null : Number(raw.trim())
  drafts.delete(key)
  groupMultiplierDrafts.value = drafts
  if (groupMultiplier !== null && (!Number.isFinite(groupMultiplier) || groupMultiplier < 0)) return
  saveGroupMultiplier(key, groupMultiplier)
}

const saveGroupMultiplier = (key: string, groupMultiplier: number | null) => {
  let found = false
  const configs = (props.apiKeyConfigs ?? []).map(config => {
    if (config.key !== key) return config
    found = true
    return { ...config, groupMultiplier }
  })
  if (!found && groupMultiplier !== null) configs.push({ key, groupMultiplier })
  emit('update:apiKeyConfigs', configs)
}

const visibleDisabledKeys = computed(() => {
  return props.disabledKeys.filter(dk => !props.apiKeys.includes(dk.key))
})

const visibleDisabledKeyModels = computed(() => props.disabledKeyModels || [])
const handleInput = () => {
  apiKeyError.value = ''
  duplicateKeyIndex.value = null
}

const handleAddKey = () => {
  const trimmed = newApiKey.value.trim()
  if (!trimmed) return

  // 检查重复
  const existingIndex = props.apiKeys.indexOf(trimmed)
  if (existingIndex !== -1) {
    apiKeyError.value = t('addChannel.duplicateKey')
    duplicateKeyIndex.value = existingIndex

    // 3秒后清除高亮
    setTimeout(() => {
      duplicateKeyIndex.value = null
    }, 3000)
    return
  }

  emit('update:apiKeys', [...props.apiKeys, trimmed])
  newApiKey.value = ''
  apiKeyError.value = ''
}

const removeKey = (index: number) => {
  const updated = props.apiKeys.filter((_, i) => i !== index)
  emit('update:apiKeys', updated)
}

const moveToTop = (index: number) => {
  const updated = [...props.apiKeys]
  const [key] = updated.splice(index, 1)
  updated.unshift(key)
  emit('update:apiKeys', updated)
}

const moveToBottom = (index: number) => {
  const updated = [...props.apiKeys]
  const [key] = updated.splice(index, 1)
  updated.push(key)
  emit('update:apiKeys', updated)
}

const clearCopilotPollTimer = () => {
  if (copilotPollTimer !== null) {
    window.clearTimeout(copilotPollTimer)
    copilotPollTimer = null
  }
}

const appendOAuthKey = (accessToken: string) => {
  if (!props.apiKeys.includes(accessToken)) {
    emit('update:apiKeys', [...props.apiKeys, accessToken])
  }
}

const oauthProxyUrl = () => props.proxyUrl?.trim() || undefined

const pollCopilotAccessToken = async (intervalSeconds: number) => {
  if (!copilotDeviceCode.value) return
  copilotPolling.value = true
  try {
    const token = await apiService.pollCopilotAccessToken(copilotDeviceCode.value, oauthProxyUrl())
    if (token.accessToken) {
      appendOAuthKey(token.accessToken)
      copilotOAuthError.value = ''
      copilotOAuthSuccess.value = true
      copilotPolling.value = false
      copilotOAuthLoading.value = false
      clearCopilotPollTimer()
      return
    }
    if (token.error === 'expired_token') {
      copilotOAuthError.value = t('copilotOAuth.expired')
      copilotOAuthSuccess.value = false
      copilotPolling.value = false
      copilotOAuthLoading.value = false
      clearCopilotPollTimer()
      return
    }
    if (token.error && token.error !== 'authorization_pending') {
      copilotOAuthError.value = token.errorDescription || token.error
      copilotOAuthSuccess.value = false
      copilotPolling.value = false
      copilotOAuthLoading.value = false
      clearCopilotPollTimer()
      return
    }
  } catch (err) {
    copilotOAuthError.value = err instanceof Error ? err.message : String(err)
    copilotOAuthSuccess.value = false
    copilotPolling.value = false
    copilotOAuthLoading.value = false
    clearCopilotPollTimer()
    return
  }

  copilotPollTimer = window.setTimeout(() => {
    void pollCopilotAccessToken(intervalSeconds)
  }, Math.max(intervalSeconds, 5) * 1000)
}

const startCopilotOAuth = async () => {
  clearCopilotPollTimer()
  copilotOAuthLoading.value = true
  copilotOAuthError.value = ''
  copilotOAuthSuccess.value = false
  try {
    const device = await apiService.requestCopilotDeviceCode(oauthProxyUrl())
    copilotDeviceCode.value = device.deviceCode
    copilotUserCode.value = device.userCode
    copilotVerificationUri.value = device.verificationUri
    window.open(device.verificationUri, '_blank', 'noopener,noreferrer')
    await pollCopilotAccessToken(device.interval || 5)
  } catch (err) {
    copilotOAuthError.value = err instanceof Error ? err.message : String(err)
    copilotOAuthLoading.value = false
    copilotPolling.value = false
  }
}

onBeforeUnmount(clearCopilotPollTimer)

const cancelCopilotOAuth = () => {
  clearCopilotPollTimer()
  copilotPolling.value = false
  copilotOAuthLoading.value = false
}

const retryCopilotOAuth = () => {
  void startCopilotOAuth()
}

const diagnoseCopilotChannel = async () => {
  if (!props.channelId) return
  copilotDiagnoseLoading.value = true
  copilotDiagnoseError.value = ''
  copilotDiagnoseResult.value = null
  try {
    const latestKey = props.apiKeys[0]
    copilotDiagnoseResult.value = await apiService.diagnoseCopilotChannel(props.channelId, latestKey) as unknown as CopilotDiagnoseResponse
  } catch (err) {
    copilotDiagnoseError.value = err instanceof Error ? err.message : String(err)
  } finally {
    copilotDiagnoseLoading.value = false
  }
}

const copyKey = (key: string, index: number) => {
  navigator.clipboard.writeText(key)
  copiedKeyIndex.value = index
  setTimeout(() => {
    copiedKeyIndex.value = null
  }, 2000)
}

const getDisabledKeyLabel = (reason: string) => {
  const map: Record<string, string> = {
    'insufficient_balance': 'channelCard.blacklistReason.insufficient_balance',
    'unauthorized': 'channelCard.blacklistReason.authentication_error',
    'invalid': 'channelCard.blacklistReason.invalid',
  }
  return (map[reason] || 'channelCard.blacklistReason.unknown') as any
}
</script>

<style scoped>
.group-multiplier-input {
  max-width: 140px;
}

.section-title {
  font-size: 1.125rem;
  font-weight: 600;
}

.animate-pulse {
  animation: pulse 1s ease-in-out 3;
}

@keyframes pulse {
  0%, 100% {
    opacity: 1;
  }
  50% {
    opacity: 0.6;
  }
}

.font-weight-mono {
  font-family: 'Monaco', 'Menlo', 'Ubuntu Mono', 'Courier New', monospace;
}

</style>
