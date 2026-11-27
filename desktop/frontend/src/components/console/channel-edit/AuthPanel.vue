<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import {
  AlertTriangle,
  ArrowDown,
  ArrowUp,
  CheckCircle2,
  Copy,
  Key,
  Loader2,
  Pause,
  Play,
  Plus,
  RotateCcw,
  Trash2,
} from 'lucide-vue-next'
import { useLanguage } from '@/composables/useLanguage'
import { useAdminApi } from '@/composables/useAdminApi'
import { maskApiKey } from '@/utils/api-key-mask'
import type { APIKeyConfig, DisabledGroupModelInfo, GroupModelMutationResponse } from '@/services/admin-api'
import type { ManagedChannelType } from '@/utils/channel-type-api'
import { groupModelPolicyKey } from '@/utils/group-model-policy'

interface DisabledKeyInfo {
  key: string
  reason?: string
  disabledAt?: string
}

interface KeyModelsStatus {
  loading?: boolean
  success?: boolean
  error?: string
  statusCode?: string | number
  modelCount?: number
}

const props = defineProps<{
  apiKeyConfigs?: APIKeyConfig[]
  groupMultiplier?: number | null
  managedMultiplier?: boolean
  existingApiKeys: string[]
  newApiKeysText: string
  copiedKeyIndex: number | null
  duplicateKeyIndex: number | null
  disabledApiKeys: DisabledKeyInfo[]
  disabledGroupModels: DisabledGroupModelInfo[]
  historicalApiKeys: string[]
  restoringKey: string
  removingKey: string
  localRestoredKeys: Set<string>
  keyModelsStatus: Map<string, KeyModelsStatus>
  serviceType: string
  channelKind: ManagedChannelType
  channelId?: number
  errors: { apiKeys?: string }
}>()

const emit = defineEmits<{
  'update:apiKeyConfigs': [APIKeyConfig[]]
  'update:groupMultiplier': [number | null]
  'update:newApiKeysText': [value: string]
  'addNewApiKeys': []
  'removeExistingApiKey': [index: number]
  'moveApiKeyToTop': [index: number]
  'moveApiKeyToBottom': [index: number]
  'copyApiKey': [key: string, index: number]
  'handleDisabledKeyRestore': [key: string]
  'handleDisabledKeyRemove': [key: string]
}>()

const { t, tf } = useLanguage()
const adminApi = useAdminApi()
const suspendingKey = ref('')
const suspendError = ref('')
const selectedGroupModelKey = ref('')
const groupModelName = ref('')
const groupModelBusy = ref('')
const groupModelError = ref('')
const localDisabledGroupModels = ref<DisabledGroupModelInfo[]>([])
const localRestoredGroupModels = ref(new Set<string>())

watch(() => props.existingApiKeys, keys => {
  if (!keys.includes(selectedGroupModelKey.value)) selectedGroupModelKey.value = keys[0] || ''
}, { immediate: true, deep: true })

const visibleDisabledGroupModels = computed(() => {
  const records = new Map<string, DisabledGroupModelInfo>()
  for (const record of [...props.disabledGroupModels, ...localDisabledGroupModels.value]) {
    records.set(groupModelPolicyKey(record), record)
  }
  return [...records.values()].filter(record => !localRestoredGroupModels.value.has(groupModelPolicyKey(record)))
})

function confirmRemoveDisabledKey(key: string) {
  if (window.confirm(t('channelCard.confirmRemoveDisabledKey', { key: maskApiKey(key) }))) {
    emit('handleDisabledKeyRemove', key)
  }
}

async function disableSelectedGroupModel() {
  const apiKey = selectedGroupModelKey.value
  const model = groupModelName.value.trim()
  if (props.channelId === undefined || !apiKey || !model || groupModelBusy.value) return
  groupModelBusy.value = 'disable'
  groupModelError.value = ''
  try {
    const result = await adminApi.post<GroupModelMutationResponse>(
      `/api/${props.channelKind}/channels/${props.channelId}/keys/group-model/disable`,
      { apiKey, model }
    )
    const record: DisabledGroupModelInfo = {
      quotaGroup: result.quotaGroup,
      key: result.quotaGroup ? undefined : apiKey,
      model: result.model,
      disabledAt: new Date().toISOString(),
    }
    const policyKey = groupModelPolicyKey(record)
    localDisabledGroupModels.value = [
      ...localDisabledGroupModels.value.filter(item => groupModelPolicyKey(item) !== policyKey),
      record,
    ]
    localRestoredGroupModels.value.delete(policyKey)
    groupModelName.value = ''
  } catch (cause) {
    groupModelError.value = cause instanceof Error ? cause.message : String(cause)
  } finally {
    groupModelBusy.value = ''
  }
}

async function restoreGroupModel(record: DisabledGroupModelInfo) {
  const policyKey = groupModelPolicyKey(record)
  if (props.channelId === undefined || groupModelBusy.value) return
  groupModelBusy.value = policyKey
  groupModelError.value = ''
  try {
    await adminApi.post(`/api/${props.channelKind}/channels/${props.channelId}/keys/group-model/restore`, {
      apiKey: record.key || undefined,
      quotaGroup: record.quotaGroup || undefined,
      model: record.model,
    })
    localRestoredGroupModels.value = new Set([...localRestoredGroupModels.value, policyKey])
    localDisabledGroupModels.value = localDisabledGroupModels.value.filter(item => groupModelPolicyKey(item) !== policyKey)
  } catch (cause) {
    groupModelError.value = cause instanceof Error ? cause.message : String(cause)
  } finally {
    groupModelBusy.value = ''
  }
}

function isKeyPaused(key: string) {
  return props.apiKeyConfigs?.find(cfg => cfg.key === key)?.enabled === false
}

function getQuotaGroup(key: string) {
  return props.apiKeyConfigs?.find(cfg => cfg.key === key)?.quotaGroup?.trim() || ''
}

function setKeyEnabled(key: string, enabled: boolean | undefined) {
  let found = false
  const configs = (props.apiKeyConfigs || []).map(cfg => {
    if (cfg.key !== key) return cfg
    found = true
    return { ...cfg, enabled }
  })
  if (!found && enabled !== undefined) configs.push({ key, enabled })
  emit('update:apiKeyConfigs', configs)
}

async function setKeySuspended(key: string, suspended: boolean) {
  if (props.channelId === undefined || suspendingKey.value) return
  suspendingKey.value = key
  suspendError.value = ''
  try {
    const action = suspended ? 'suspend' : 'resume'
    await adminApi.post(`/api/${props.channelKind}/channels/${props.channelId}/keys/${action}`, { apiKey: key })
    setKeyEnabled(key, suspended ? false : undefined)
  } catch (cause) {
    suspendError.value = cause instanceof Error ? cause.message : String(cause)
  } finally {
    suspendingKey.value = ''
  }
}

function getKeyStatus(key: string) {
  return props.keyModelsStatus.get(key)
}

function formatModelsCount(statusCode: string | number, count: number) {
  return t('addChannel.modelsCount', {
    statusCode: String(statusCode),
    count: String(count),
  })
}

function getDisabledReasonLabel(reason?: string) {
  const map: Record<string, string> = {
    insufficient_balance: 'channelCard.blacklistReason.insufficient_balance',
    unauthorized: 'channelCard.blacklistReason.authentication_error',
    invalid: 'channelCard.blacklistReason.invalid',
  }
  return reason ? tf(map[reason] || 'channelCard.blacklistReason.unknown', reason) : ''
}

const hasDisabledKeys = computed(() => props.disabledApiKeys.length > 0)

const visibleDisabledKeys = computed(() => {
  return props.disabledApiKeys.filter(item => !props.localRestoredKeys.has(item.key))
})
</script>

<template>
  <section class="space-y-4 rounded-xl border bg-card/40 p-5 shadow-xs" :class="errors.apiKeys ? 'border-destructive/40' : 'border-border/60'">
    <div class="flex items-center justify-between gap-3 border-b border-border/40 pb-2">
      <h4 class="text-xs font-bold uppercase tracking-wider text-primary">
        {{ t('channelCard.apiKeyManagement') }}<span v-if="serviceType !== 'copilot'"> *</span>
      </h4>
      <span class="text-[10px] bg-primary/10 border border-primary/20 text-primary font-semibold px-2 py-0.5 rounded-full">
        {{ t('addChannel.apiKeyLoadBalance') }}
      </span>
    </div>

    <p v-if="errors.apiKeys" class="text-[10px] text-destructive">{{ errors.apiKeys }}</p>
    <p v-if="suspendError" class="text-[10px] text-destructive">{{ suspendError }}</p>
    <div class="space-y-1">
      <label class="text-xs">{{ t('channelEditor.auth.groupMultiplier') }}<Input :model-value="groupMultiplier ?? ''" :readonly="managedMultiplier" type="number" min="0" step="any" class="mt-1 max-w-40" @update:model-value="emit('update:groupMultiplier', $event == null || $event === '' ? null : Number($event))" /></label>
      <p class="text-xs text-muted-foreground">{{ t('channelEditor.auth.groupMultiplierHint') }}</p>
    </div>

    <!-- API Keys 列表 -->
    <div v-if="existingApiKeys.length" class="grid gap-2 max-h-[160px] overflow-y-auto pr-1">
      <div
        v-for="(key, index) in existingApiKeys"
        :key="`${index}-${key}`"
        class="flex items-center justify-between gap-4 rounded-lg px-3 py-2 text-xs shadow-2xs transition-all group"
        :class="duplicateKeyIndex === index ? 'border-destructive/70 bg-destructive/10 animate-pulse' : 'border border-border/60 bg-background/60 hover:bg-background'"
      >
        <div class="flex min-w-0 items-center gap-2.5">
          <AlertTriangle v-if="duplicateKeyIndex === index" class="h-3.5 w-3.5 shrink-0 text-destructive" />
          <Key v-else class="h-3.5 w-3.5 shrink-0 text-primary/70" />
          <code class="font-mono text-muted-foreground font-medium select-all">{{ maskApiKey(key) }}</code>
          <span v-if="isKeyPaused(key)" class="shrink-0 rounded bg-amber-500/10 px-1.5 py-0.5 text-[9px] font-medium text-amber-600">
            {{ t('channelCard.keyPaused') }}
          </span>
          <span
            v-if="getKeyStatus(key)?.loading"
            class="rounded bg-sky-500/10 px-1.5 py-0.5 text-[9px] font-medium text-sky-600"
          >
            {{ t('addChannel.checking') }}
          </span>
          <span
            v-else-if="getKeyStatus(key)?.success"
            class="rounded bg-emerald-500/10 px-1.5 py-0.5 text-[9px] font-medium text-emerald-600"
          >
            {{ formatModelsCount(getKeyStatus(key)?.statusCode ?? 'OK', getKeyStatus(key)?.modelCount ?? 0) }}
          </span>
          <Tooltip v-else-if="getKeyStatus(key)?.error">
            <TooltipTrigger as-child>
              <span
                class="cursor-help rounded bg-destructive/10 px-1.5 py-0.5 text-[9px] font-medium text-destructive"
                :title="getKeyStatus(key)?.error"
              >
                models {{ getKeyStatus(key)?.statusCode || 'ERR' }}
              </span>
            </TooltipTrigger>
            <TooltipContent side="top" class="max-w-[300px] whitespace-pre-wrap break-words text-xs">
              {{ getKeyStatus(key)?.error }}
            </TooltipContent>
          </Tooltip>
          <span
            v-if="duplicateKeyIndex === index"
            class="text-[10px] text-destructive shrink-0"
          >
            {{ t('channelEditor.auth.duplicateKey') }}
          </span>
        </div>
        <div class="flex shrink-0 items-center gap-1 opacity-60 group-hover:opacity-100 transition-opacity">
          <Button
            v-if="channelId !== undefined"
            type="button"
            size="icon-sm"
            variant="ghost"
            class="h-7 w-7 rounded-md"
            :class="isKeyPaused(key) ? 'text-emerald-600 hover:bg-emerald-500/10' : 'text-amber-600 hover:bg-amber-500/10'"
            :title="isKeyPaused(key) ? t('channelCard.resumeKey') : t('channelCard.suspendKey')"
            :disabled="!!suspendingKey"
            @click="setKeySuspended(key, !isKeyPaused(key))"
          >
            <Loader2 v-if="suspendingKey === key" class="h-3.5 w-3.5 animate-spin" />
            <Play v-else-if="isKeyPaused(key)" class="h-3.5 w-3.5" />
            <Pause v-else class="h-3.5 w-3.5" />
          </Button>
          <Button
            type="button"
            size="icon-sm"
            variant="ghost"
            class="h-7 w-7 rounded-md text-muted-foreground hover:bg-accent hover:text-foreground"
            :class="copiedKeyIndex === index ? 'text-emerald-500' : ''"
            :title="t('channelEditor.auth.copyKey')"
            @click="emit('copyApiKey', key, index)"
          >
            <CheckCircle2 v-if="copiedKeyIndex === index" class="h-3.5 w-3.5" />
            <Copy v-else class="h-3.5 w-3.5" />
          </Button>
          <Button
            v-if="index > 0"
            type="button"
            size="icon-sm"
            variant="ghost"
            class="h-7 w-7 rounded-md text-muted-foreground hover:bg-accent hover:text-foreground"
            @click="emit('moveApiKeyToTop', index)"
          >
            <ArrowUp class="h-3.5 w-3.5" />
          </Button>
          <Button
            v-if="index < existingApiKeys.length - 1"
            type="button"
            size="icon-sm"
            variant="ghost"
            class="h-7 w-7 rounded-md text-muted-foreground hover:bg-accent hover:text-foreground"
            @click="emit('moveApiKeyToBottom', index)"
          >
            <ArrowDown class="h-3.5 w-3.5" />
          </Button>
          <Button
            type="button"
            size="icon-sm"
            variant="ghost"
            class="h-7 w-7 rounded-md text-destructive hover:bg-destructive/10"
            :title="t('channelEditor.auth.deleteKey')"
            @click="emit('removeExistingApiKey', index)"
          >
            <Trash2 class="h-3.5 w-3.5" />
          </Button>
        </div>
      </div>
    </div>

    <div v-if="channelId !== undefined && existingApiKeys.length" class="space-y-3 rounded-lg border border-border/60 bg-background/40 p-3">
      <div class="text-[10px] font-bold uppercase tracking-wider text-muted-foreground">
        {{ t('channelCard.groupModelPolicy') }}
      </div>
      <div class="grid gap-2 sm:grid-cols-[1fr_1fr_auto]">
        <select v-model="selectedGroupModelKey" class="h-9 rounded-md border border-input bg-background px-2 text-xs">
          <option v-for="key in existingApiKeys" :key="key" :value="key">
            {{ maskApiKey(key) }}{{ getQuotaGroup(key) ? ' · ' + getQuotaGroup(key) : '' }}
          </option>
        </select>
        <Input v-model="groupModelName" :placeholder="t('channelCard.groupModelPlaceholder')" class="h-9 text-xs" @keydown.enter.prevent="disableSelectedGroupModel" />
        <Button type="button" size="sm" variant="outline" :disabled="!groupModelName.trim() || !!groupModelBusy" @click="disableSelectedGroupModel">
          <Loader2 v-if="groupModelBusy === 'disable'" class="h-3.5 w-3.5 animate-spin" />
          {{ t('channelCard.disableModel') }}
        </Button>
      </div>
      <p v-if="groupModelError" class="text-[10px] text-destructive">{{ groupModelError }}</p>
      <div v-if="visibleDisabledGroupModels.length" class="space-y-2 border-t border-border/50 pt-2">
        <div class="text-[10px] font-medium text-amber-700 dark:text-amber-300">
          {{ t('channelCard.disabledGroupModels') }} ({{ visibleDisabledGroupModels.length }})
        </div>
        <div v-for="record in visibleDisabledGroupModels" :key="groupModelPolicyKey(record)" class="flex items-center justify-between gap-2 text-xs">
          <div class="flex min-w-0 items-center gap-1.5">
            <span class="rounded bg-secondary px-1.5 py-0.5">{{ record.quotaGroup || t('channelCard.independentKey') }}</span>
            <code v-if="!record.quotaGroup && record.key" class="text-muted-foreground">{{ maskApiKey(record.key) }}</code>
            <span class="font-medium">{{ record.model }}</span>
          </div>
          <Button type="button" size="sm" variant="outline" :disabled="!!groupModelBusy" @click="restoreGroupModel(record)">
            <Loader2 v-if="groupModelBusy === groupModelPolicyKey(record)" class="h-3 w-3 animate-spin" />
            <RotateCcw v-else class="h-3 w-3" />
            {{ t('channelCard.restoreKey') }}
          </Button>
        </div>
      </div>
    </div>

    <!-- 添加新 API Key -->
    <div class="flex gap-2">
      <Input
        :model-value="newApiKeysText"
        class="h-9 flex-1 rounded-lg border border-input bg-background/40 px-3 font-mono text-xs placeholder:text-muted-foreground/60 outline-none transition-all focus:border-primary focus:ring-2 focus:ring-primary/20"
        :placeholder="t('channelEditor.auth.addNewApiKey.placeholder')"
        :aria-invalid="duplicateKeyIndex !== null"
        @update:model-value="(val) => emit('update:newApiKeysText', val as string)"
        @keydown.enter.prevent="emit('addNewApiKeys')"
      />
      <Button
        type="button"
        variant="outline"
        size="sm"
        class="h-9 rounded-lg border border-input bg-background/80 hover:bg-accent px-4 text-xs font-semibold shadow-2xs"
        :disabled="!newApiKeysText.trim()"
        @click="emit('addNewApiKeys')"
      >
        <Plus class="h-4 w-4 mr-1" />
        {{ t('channelEditor.auth.addKey') }}
      </Button>
    </div>

    <!-- Disabled Keys -->
    <div v-if="hasDisabledKeys && visibleDisabledKeys.length" class="space-y-2 border border-amber-500/20 bg-amber-500/10 p-3 rounded-lg">
      <div class="text-[10px] font-bold uppercase tracking-wider text-amber-700 dark:text-amber-300">
        {{ t('channelEditor.auth.disabledKeys.label') }} ({{ visibleDisabledKeys.length }})
      </div>
      <div v-for="item in visibleDisabledKeys" :key="item.key" class="flex items-center justify-between gap-2 text-xs">
        <div class="min-w-0 space-y-0.5">
          <div class="flex min-w-0 items-center gap-1.5">
            <span class="truncate font-mono text-muted-foreground">{{ maskApiKey(item.key) }}</span>
            <span
              v-if="item.reason"
              class="shrink-0 rounded bg-amber-500/15 px-1 text-[9px] text-amber-700 dark:text-amber-300"
            >
              {{ getDisabledReasonLabel(item.reason) }}
            </span>
          </div>
          <div v-if="item.disabledAt" class="text-[10px] text-muted-foreground">{{ item.disabledAt }}</div>
        </div>
        <div class="flex items-center gap-1">
          <Button
            type="button"
            size="sm"
            variant="outline"
            :disabled="restoringKey === item.key || !!removingKey"
            @click="emit('handleDisabledKeyRestore', item.key)"
          >
            <Loader2 v-if="restoringKey === item.key" class="h-3 w-3 animate-spin" />
            <RotateCcw v-else class="h-3 w-3" />
            {{ t('channelEditor.auth.restoreKey') }}
          </Button>
          <Button
            type="button"
            size="icon-sm"
            variant="ghost"
            class="text-destructive"
            :disabled="!!removingKey"
            @click="confirmRemoveDisabledKey(item.key)"
          >
            <Loader2 v-if="removingKey === item.key" class="h-3.5 w-3.5 animate-spin" />
            <Trash2 v-else class="h-3.5 w-3.5" />
          </Button>
        </div>
      </div>
    </div>
  </section>
</template>
