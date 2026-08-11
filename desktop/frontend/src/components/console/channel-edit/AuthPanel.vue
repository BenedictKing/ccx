<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import {
  AlertTriangle,
  ArrowDown,
  ArrowUp,
  Ban,
  CheckCircle2,
  Copy,
  Key,
  Loader2,
  Plus,
  RotateCcw,
  Trash2,
  Zap,
} from 'lucide-vue-next'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { useLanguage } from '@/composables/useLanguage'
import { useAdminApi } from '@/composables/useAdminApi'
import { maskApiKey } from '@/utils/api-key-mask'
import type {
  APIKeyConfig,
  ChannelKind,
  DisabledGroupModel,
  GroupModelMutationResponse,
  KeyMultiplierPatch,
  KeyMultiplierResponse,
} from '@/services/admin-api'

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
  existingApiKeys: string[]
  newApiKeysText: string
  copiedKeyIndex: number | null
  duplicateKeyIndex: number | null
  disabledApiKeys: DisabledKeyInfo[]
  disabledGroupModels: DisabledGroupModel[]
  historicalApiKeys: string[]
  restoringKey: string
  localRestoredKeys: Set<string>
  keyModelsStatus: Map<string, KeyModelsStatus>
  serviceType: string
  channelKind: ChannelKind
  channelUid?: string
  apiKeyConfigs?: APIKeyConfig[]
  onDisableGroupModel: (payload: { apiKey: string; model: string; note?: string }) => Promise<GroupModelMutationResponse>
  onRestoreGroupModel: (record: DisabledGroupModel) => Promise<GroupModelMutationResponse>
  errors: { apiKeys?: string }
}>()

const emit = defineEmits<{
  'update:newApiKeysText': [value: string]
  'addNewApiKeys': []
  'removeExistingApiKey': [index: number]
  'moveApiKeyToTop': [index: number]
  'moveApiKeyToBottom': [index: number]
  'copyApiKey': [key: string, index: number]
  'handleDisabledKeyRestore': [key: string]
}>()

const { t, tf } = useLanguage()
const adminApi = useAdminApi()
const multiplierDrafts = ref<Record<string, { groupMultiplier: string; maxGroupMultiplier: string; consumptionPolicy: 'normal' | 'opportunistic' }>>({})
const multiplierResults = ref<Record<string, KeyMultiplierResponse>>({})
const savingMultiplier = ref('')
const multiplierError = ref('')
const groupModelDrafts = ref<Record<string, { model: string; note: string }>>({})
const groupModelBusy = ref('')
const groupModelError = ref('')
const groupModelNotice = ref('')

watch(() => props.apiKeyConfigs, configs => {
  const next: Record<string, { groupMultiplier: string; maxGroupMultiplier: string; consumptionPolicy: 'normal' | 'opportunistic' }> = {}
  for (const config of configs || []) {
    if (!config.keyUid) continue
    next[config.keyUid] = {
      groupMultiplier: config.groupMultiplier == null ? '' : String(config.groupMultiplier),
      maxGroupMultiplier: config.maxGroupMultiplier == null ? '' : String(config.maxGroupMultiplier),
      consumptionPolicy: config.consumptionPolicy === 'opportunistic' ? 'opportunistic' : 'normal',
    }
  }
  multiplierDrafts.value = next
}, { immediate: true, deep: true })

function keyConfig(key: string) { return props.apiKeyConfigs?.find(config => config.key === key) }
function finiteNonNegative(value: string) { return value.trim() === '' || (Number.isFinite(Number(value)) && Number(value) >= 0) }
async function saveMultiplier(config: APIKeyConfig) {
  if (!props.channelUid || !config.keyUid) return
  const draft = multiplierDrafts.value[config.keyUid]
  if (!draft || !finiteNonNegative(draft.groupMultiplier) || !finiteNonNegative(draft.maxGroupMultiplier)) {
    multiplierError.value = t('multiplier.invalid')
    return
  }
  savingMultiplier.value = config.keyUid; multiplierError.value = ''
  try {
    const payload: KeyMultiplierPatch = {
      maxGroupMultiplier: draft.maxGroupMultiplier.trim() === '' ? null : Number(draft.maxGroupMultiplier),
      consumptionPolicy: draft.consumptionPolicy,
    }
    if (config.multiplierSource !== 'new_api') payload.groupMultiplier = draft.groupMultiplier.trim() === '' ? null : Number(draft.groupMultiplier)
    multiplierResults.value[config.keyUid] = await adminApi.patchKeyMultiplier(props.channelKind, props.channelUid, config.keyUid, payload)
  } catch (cause) { multiplierError.value = cause instanceof Error ? cause.message : String(cause) }
  finally { savingMultiplier.value = '' }
}
function multiplierStatus(config: APIKeyConfig) {
  const result = config.keyUid ? multiplierResults.value[config.keyUid] : undefined
  return result?.status || config.multiplierSyncStatus || (config.groupMultiplier != null || config.maxGroupMultiplier != null ? 'manual' : '')
}
function multiplierReason(config: APIKeyConfig) {
  const result = config.keyUid ? multiplierResults.value[config.keyUid] : undefined
  return result?.reason || config.ineligibleReason || config.multiplierSyncError || ''
}

function effectiveCostClassLabel(value?: string) {
  if (!value) return ''
  return `multiplier.effectiveCostClass.${value}`
}

function markAsOpportunistic(keyUid: string) {
  const draft = multiplierDrafts.value[keyUid]
  if (!draft) return
  draft.groupMultiplier = '0'
  draft.maxGroupMultiplier = '0'
  draft.consumptionPolicy = 'opportunistic'
}

function groupModelDraft(key: string) {
  return groupModelDrafts.value[key] ||= { model: '', note: '' }
}
async function disableGroupModel(key: string) {
  const draft = groupModelDraft(key)
  const model = draft.model.trim()
  if (!model) { groupModelError.value = t('groupModel.error.modelRequired'); return }
  groupModelBusy.value = `disable:${key}`; groupModelError.value = ''; groupModelNotice.value = ''
  try {
    const result = await props.onDisableGroupModel({ apiKey: key, model, note: draft.note.trim() || undefined })
    groupModelNotice.value = t('groupModel.disableSuccess', { group: result.quotaGroup, model: result.model, count: String(result.affectedKeyCount) })
    groupModelDrafts.value[key] = { model: '', note: '' }
  } catch (cause) { groupModelError.value = cause instanceof Error ? cause.message : String(cause) }
  finally { groupModelBusy.value = '' }
}
async function restoreGroupModel(record: DisabledGroupModel) {
  const token = `restore:${record.quotaGroup}:${record.model}`
  groupModelBusy.value = token; groupModelError.value = ''; groupModelNotice.value = ''
  try {
    const result = await props.onRestoreGroupModel(record)
    groupModelNotice.value = t('groupModel.restoreSuccess', { group: result.quotaGroup, model: result.model, count: String(result.affectedKeyCount) })
  } catch (cause) { groupModelError.value = cause instanceof Error ? cause.message : String(cause) }
  finally { groupModelBusy.value = '' }
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
    insufficient_quota: 'channelCard.blacklistReason.insufficient_quota',
    unauthorized: 'channelCard.blacklistReason.authentication_error',
    authentication_error: 'channelCard.blacklistReason.authentication_error',
    permission_error: 'channelCard.blacklistReason.permission_error',
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

    <!-- API Keys 列表 -->
    <div v-if="existingApiKeys.length" class="grid gap-2 max-h-[300px] overflow-y-auto pr-1">
      <div v-for="(key, index) in existingApiKeys" :key="`${index}-${key}`" class="space-y-2">
      <div
        class="flex items-center justify-between gap-4 rounded-lg px-3 py-2 text-xs shadow-2xs transition-all group"
        :class="duplicateKeyIndex === index ? 'border-destructive/70 bg-destructive/10 animate-pulse' : 'border border-border/60 bg-background/60 hover:bg-background'"
      >
        <div class="flex min-w-0 items-center gap-2.5">
          <AlertTriangle v-if="duplicateKeyIndex === index" class="h-3.5 w-3.5 shrink-0 text-destructive" />
          <Key v-else class="h-3.5 w-3.5 shrink-0 text-primary/70" />
          <code class="font-mono text-muted-foreground font-medium select-all">{{ maskApiKey(key) }}</code>
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
      <div v-if="keyConfig(key)?.keyUid" class="grid grid-cols-1 gap-2 rounded-lg border border-border/60 bg-secondary/20 p-2 sm:grid-cols-[1fr_1fr_1fr_auto]">
        <div>
          <label class="text-[10px] text-muted-foreground">{{ t('multiplier.group') }}</label>
          <Input v-model="multiplierDrafts[keyConfig(key)!.keyUid!].groupMultiplier" type="number" min="0" step="any" class="h-8" :disabled="keyConfig(key)?.multiplierSource === 'new_api'" />
        </div>
        <div>
          <label class="text-[10px] text-muted-foreground">{{ t('multiplier.max') }}</label>
          <Input v-model="multiplierDrafts[keyConfig(key)!.keyUid!].maxGroupMultiplier" type="number" min="0" step="any" class="h-8" />
        </div>
        <div>
          <label class="text-[10px] text-muted-foreground">{{ t('multiplier.consumptionPolicy.label') }}</label>
          <Select v-model="multiplierDrafts[keyConfig(key)!.keyUid!].consumptionPolicy">
            <SelectTrigger class="h-8">
              <SelectValue :placeholder="t('multiplier.consumptionPolicy.normal')" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem value="normal">{{ t('multiplier.consumptionPolicy.normal') }}</SelectItem>
              <SelectItem value="opportunistic">{{ t('multiplier.consumptionPolicy.opportunistic') }}</SelectItem>
            </SelectContent>
          </Select>
        </div>
        <div class="flex flex-col gap-1 self-end">
          <Button type="button" size="sm" :disabled="savingMultiplier === keyConfig(key)?.keyUid" @click="saveMultiplier(keyConfig(key)!)">
            <Loader2 v-if="savingMultiplier === keyConfig(key)?.keyUid" class="h-3.5 w-3.5 animate-spin" />
            {{ t('multiplier.save') }}
          </Button>
          <Button type="button" size="sm" variant="outline" :disabled="!keyConfig(key)?.keyUid" @click="markAsOpportunistic(keyConfig(key)!.keyUid!)">
            <Zap class="h-3 w-3 mr-1" />
            {{ t('multiplier.markAsOpportunistic') }}
          </Button>
        </div>
        <div class="text-[10px] text-muted-foreground sm:col-span-4">
          <span v-if="keyConfig(key)?.quotaGroup">{{ t('multiplier.groupName') }}: {{ keyConfig(key)?.quotaGroup }} · </span>
          <span v-if="multiplierStatus(keyConfig(key)!)">{{ t('multiplier.statusLabel') }}: {{ tf(`multiplier.status.${multiplierStatus(keyConfig(key)!)}`, multiplierStatus(keyConfig(key)!)) }}</span>
          <span v-if="keyConfig(key)?.eligible !== undefined"> · {{ keyConfig(key)?.eligible ? t('multiplier.eligible') : t('multiplier.ineligible') }}</span>
          <span v-if="keyConfig(key)?.multiplierExpiresAt"> · {{ t('multiplier.ttl') }}: {{ keyConfig(key)?.multiplierExpiresAt }}</span>
          <span v-if="multiplierResults[keyConfig(key)!.keyUid!]?.consumptionPolicy"> · {{ t('multiplier.consumptionPolicy.label') }}: {{ t(`multiplier.consumptionPolicy.${multiplierResults[keyConfig(key)!.keyUid!].consumptionPolicy}`) }}</span>
          <span v-if="multiplierResults[keyConfig(key)!.keyUid!]?.effectiveCostClass"> · {{ t('multiplier.effectiveCostClass.label') }}: {{ t(effectiveCostClassLabel(multiplierResults[keyConfig(key)!.keyUid!].effectiveCostClass)) }}</span>
          <span v-if="multiplierReason(keyConfig(key)!)" :title="multiplierReason(keyConfig(key)!)"> · {{ t('multiplier.reason') }}: {{ multiplierReason(keyConfig(key)!) }}</span>
        </div>
        <p v-if="multiplierDrafts[keyConfig(key)!.keyUid!].consumptionPolicy === 'opportunistic'" class="text-[10px] text-amber-700 dark:text-amber-300 sm:col-span-4">
          {{ t('multiplier.consumptionPolicy.opportunisticHint') }}
        </p>
      </div>
      <div v-if="keyConfig(key)?.quotaGroup" class="grid gap-2 rounded-lg border border-rose-500/20 bg-rose-500/5 p-2 sm:grid-cols-[1fr_1fr_auto]">
        <div>
          <label class="text-[10px] text-muted-foreground">{{ t('groupModel.model') }}</label>
          <Input v-model="groupModelDraft(key).model" class="h-8 font-mono" :placeholder="t('groupModel.modelPlaceholder')" />
        </div>
        <div>
          <label class="text-[10px] text-muted-foreground">{{ t('groupModel.note') }}</label>
          <Input v-model="groupModelDraft(key).note" class="h-8" :placeholder="t('groupModel.notePlaceholder')" />
        </div>
        <Button type="button" size="sm" variant="outline" class="self-end border-rose-500/30 text-rose-700 dark:text-rose-300" :disabled="groupModelBusy === `disable:${key}`" @click="disableGroupModel(key)">
          <Loader2 v-if="groupModelBusy === `disable:${key}`" class="h-3.5 w-3.5 animate-spin" />
          <Ban v-else class="h-3.5 w-3.5" />
          {{ t('groupModel.disable') }}
        </Button>
        <p class="text-[10px] text-muted-foreground sm:col-span-3">{{ t('groupModel.disableHint', { group: keyConfig(key)?.quotaGroup || '-' }) }}</p>
      </div>
      </div>
    </div>
    <p v-if="multiplierError" class="text-[10px] text-destructive">{{ multiplierError }}</p>
    <p v-if="groupModelError" class="text-[10px] text-destructive">{{ groupModelError }}</p>
    <p v-if="groupModelNotice" class="text-[10px] text-emerald-700 dark:text-emerald-300">{{ groupModelNotice }}</p>

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

    <!-- 人工分组模型禁用，与自动临时 Key 限制分开呈现 -->
    <div v-if="disabledGroupModels.length" class="space-y-2 rounded-lg border border-rose-500/20 bg-rose-500/5 p-3">
      <div>
        <div class="text-[10px] font-bold uppercase tracking-wider text-rose-700 dark:text-rose-300">{{ t('groupModel.disabledTitle') }} ({{ disabledGroupModels.length }})</div>
        <p class="mt-1 text-[10px] text-muted-foreground">{{ t('groupModel.disabledHint') }}</p>
      </div>
      <div v-for="record in disabledGroupModels" :key="`${record.quotaGroup}:${record.model}`" class="flex items-center justify-between gap-3 rounded-md border border-rose-500/15 bg-background/50 p-2 text-xs">
        <div class="min-w-0 space-y-1">
          <div class="flex flex-wrap items-center gap-1.5">
            <span class="rounded bg-rose-500/10 px-1.5 py-0.5 font-medium text-rose-700 dark:text-rose-300">{{ record.quotaGroup || t('groupModel.unknownGroup') }}</span>
            <code class="break-all font-mono text-muted-foreground">{{ record.model }}</code>
          </div>
          <p v-if="record.note" class="text-[10px] text-muted-foreground">{{ record.note }}</p>
          <p class="text-[10px] text-muted-foreground">{{ record.disabledAt }}</p>
        </div>
        <Button type="button" size="sm" variant="outline" :disabled="groupModelBusy === `restore:${record.quotaGroup}:${record.model}`" @click="restoreGroupModel(record)">
          <Loader2 v-if="groupModelBusy === `restore:${record.quotaGroup}:${record.model}`" class="h-3 w-3 animate-spin" />
          <RotateCcw v-else class="h-3 w-3" />
          {{ t('groupModel.restore') }}
        </Button>
      </div>
    </div>

    <!-- 自动临时限制的 Key -->
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
        <Button
          type="button"
          size="sm"
          variant="outline"
          :disabled="restoringKey === item.key"
          @click="emit('handleDisabledKeyRestore', item.key)"
        >
          <Loader2 v-if="restoringKey === item.key" class="h-3 w-3 animate-spin" />
          <RotateCcw v-else class="h-3 w-3" />
          {{ t('channelEditor.auth.restoreKey') }}
        </Button>
      </div>
    </div>
  </section>
</template>
