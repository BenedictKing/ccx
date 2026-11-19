<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { Alert } from '@/components/ui/alert'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Skeleton } from '@/components/ui/skeleton'
import { Switch } from '@/components/ui/switch'
import { ArrowDown, ArrowUp, Bot, GripVertical, Loader2, RefreshCw } from 'lucide-vue-next'
import { AdminApiError, useAdminApi } from '@/composables/useAdminApi'
import { useLanguage } from '@/composables/useLanguage'
import type { SubagentRoutingChannelView, SubagentRoutingView, TextChannelKind } from '@/services/admin-api'

const props = defineProps<{ type: TextChannelKind }>()
const emit = defineEmits<{ saved: [] }>()
const { t } = useLanguage()
const api = useAdminApi()
const loading = ref(false)
const saving = ref(false)
const activated = ref(false)
const revision = ref('')
const channels = ref<SubagentRoutingChannelView[]>([])
const draftModel = ref('')
const legacyModels = ref<NonNullable<SubagentRoutingView['legacyModels']>>([])
const legacyMixed = ref(false)
const searchQuery = ref('')
const errorMessage = ref('')
const savedMessage = ref('')
const savedState = ref('')
const draggedUID = ref<string | null>(null)
let requestSequence = 0

const normalizedModel = computed(() => draftModel.value.trim())
const enabledCount = computed(() => channels.value.filter(channel => channel.enabled).length)
const visibleChannels = computed(() => {
  const query = searchQuery.value.trim().toLowerCase()
  return channels.value.filter(channel => !query || channel.channelName.toLowerCase().includes(query) || channel.serviceType.toLowerCase().includes(query))
})
const currentState = computed(() => JSON.stringify({ model: normalizedModel.value, channels: channels.value.map(channel => ({ channelUid: channel.channelUid, enabled: channel.enabled })) }))
const dirty = computed(() => Boolean(savedState.value) && currentState.value !== savedState.value)
const canSave = computed(() => Boolean(savedState.value) && !loading.value && !saving.value && (dirty.value || !activated.value))
const modelOptions = computed(() => props.type === 'messages'
  ? ['haiku', 'sonnet', 'opus', 'fable']
  : props.type === 'gemini'
    ? ['gemini-2.5-flash-lite', 'gemini-2.5-flash', 'gemini-3.5-flash', 'gemini-2.5-pro']
    : ['sol', 'luna', 'terra', 'astra'])

function applyView(view: SubagentRoutingView) {
  activated.value = view.activated
  revision.value = view.revision
  legacyModels.value = view.legacyModels || []
  legacyMixed.value = view.legacyMixed
  channels.value = (view.channels || []).map(channel => ({ ...channel, enabled: view.policy.channels.find(entry => entry.channelUid === channel.channelUid)?.enabled ?? channel.enabled }))
  draftModel.value = view.policy.model
  savedState.value = JSON.stringify({ model: view.policy.model.trim(), channels: channels.value.map(channel => ({ channelUid: channel.channelUid, enabled: channel.enabled })) })
  errorMessage.value = ''
}

async function load() {
  const sequence = ++requestSequence
  loading.value = true
  errorMessage.value = ''
  try {
    const view = await api.get<SubagentRoutingView>(`/api/${props.type}/channels/subagent-routing`)
    if (sequence === requestSequence) applyView(view)
  } catch (error) {
    if (sequence === requestSequence) errorMessage.value = error instanceof Error ? error.message : String(error)
  } finally {
    if (sequence === requestSequence) loading.value = false
  }
}

function previewModel(channel: SubagentRoutingChannelView) {
  if (!normalizedModel.value) return t('subagentRouting.keepRequestModel')
  const mapping = channel.modelMapping || {}
  if (mapping[normalizedModel.value]) return mapping[normalizedModel.value]
  const source = Object.keys(mapping).filter(key => normalizedModel.value.includes(key)).sort((a, b) => b.length - a.length)[0]
  return source ? mapping[source] : normalizedModel.value
}

function move(uid: string, offset: number) {
  const index = channels.value.findIndex(channel => channel.channelUid === uid)
  const target = index + offset
  if (index < 0 || target < 0 || target >= channels.value.length) return
  const [channel] = channels.value.splice(index, 1)
  channels.value.splice(target, 0, channel)
}

function drop(targetUID: string) {
  if (!draggedUID.value || draggedUID.value === targetUID || searchQuery.value.trim()) return
  const sourceIndex = channels.value.findIndex(channel => channel.channelUid === draggedUID.value)
  const targetIndex = channels.value.findIndex(channel => channel.channelUid === targetUID)
  if (sourceIndex < 0 || targetIndex < 0) return
  const [channel] = channels.value.splice(sourceIndex, 1)
  channels.value.splice(targetIndex, 0, channel)
  draggedUID.value = null
}

function cancel() {
  if (!savedState.value) return
  const state = JSON.parse(savedState.value) as SubagentRoutingView['policy']
  draftModel.value = state.model
  channels.value = state.channels.map(entry => ({ ...channels.value.find(channel => channel.channelUid === entry.channelUid)!, enabled: entry.enabled }))
  errorMessage.value = ''
}

async function save() {
  if (!canSave.value) return
  saving.value = true
  errorMessage.value = ''
  savedMessage.value = ''
  try {
    const view = await api.put<SubagentRoutingView>(`/api/${props.type}/channels/subagent-routing`, {
      revision: revision.value,
      policy: { model: normalizedModel.value, channels: channels.value.map(channel => ({ channelUid: channel.channelUid, enabled: channel.enabled })) },
    })
    applyView(view)
    savedMessage.value = t('subagentRouting.saved')
    emit('saved')
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : String(error)
    if (error instanceof AdminApiError && error.status === 409) {
      errorMessage.value += ` ${t('subagentRouting.conflictHint')}`
    }
  } finally {
    saving.value = false
  }
}

function confirmDiscard() {
  return !dirty.value || window.confirm(t('subagentRouting.discardConfirm'))
}
defineExpose({ confirmDiscard })

watch(() => props.type, () => {
  searchQuery.value = ''
  savedMessage.value = ''
  channels.value = []
  savedState.value = ''
  void load()
}, { immediate: true })
</script>

<template>
  <section class="border border-border bg-card/60">
    <div class="flex flex-wrap items-start gap-3 border-b border-border px-4 py-3">
      <div class="min-w-0 flex-1">
        <div class="flex items-center gap-2">
          <Bot class="h-4 w-4 text-primary" />
          <h2 class="text-sm font-bold">{{ t('subagentRouting.title') }}</h2>
          <span class="border border-border px-1.5 py-0.5 text-[10px] uppercase text-muted-foreground">
            {{ t(activated ? 'subagentRouting.active' : 'subagentRouting.draft') }}
          </span>
        </div>
        <p class="mt-1 text-xs text-muted-foreground">{{ t('subagentRouting.description') }}</p>
      </div>
      <Button variant="outline" size="sm" :disabled="loading" @click="load">
        <RefreshCw class="h-3.5 w-3.5" />{{ t('subagentRouting.reload') }}
      </Button>
    </div>

    <div class="space-y-4 p-4">
      <Alert v-if="!activated && legacyModels.length">
        <p class="text-xs">{{ t('subagentRouting.legacyHint') }}</p>
        <div v-if="legacyMixed" class="mt-2 flex flex-wrap gap-2">
          <span v-for="item in legacyModels" :key="item.channelUid" class="border border-border px-2 py-1 font-mono text-[10px]">
            {{ item.channelName }}: {{ item.model }}
          </span>
        </div>
      </Alert>
      <Alert v-if="errorMessage" variant="destructive"><p class="text-xs">{{ errorMessage }}</p></Alert>
      <Alert v-if="savedMessage"><p class="text-xs">{{ savedMessage }}</p></Alert>

      <Skeleton v-if="loading && !channels.length" class="h-40 w-full rounded-none" />
      <template v-else>
        <div>
          <label class="mb-1.5 block text-xs font-semibold" :for="`subagent-model-${type}`">{{ t('subagentRouting.modelLabel') }}</label>
          <Input :id="`subagent-model-${type}`" v-model="draftModel" :list="`subagent-models-${type}`" :placeholder="t('subagentRouting.keepRequestModel')" />
          <datalist :id="`subagent-models-${type}`">
            <option value="">{{ t('subagentRouting.keepRequestModel') }}</option>
            <option v-for="model in modelOptions" :key="model" :value="model" />
          </datalist>
          <p class="mt-1.5 text-[11px] text-muted-foreground">{{ t('subagentRouting.modelHint') }}</p>
        </div>

        <div class="flex flex-wrap items-center gap-3 border-t border-border pt-3">
          <span class="text-xs font-bold">{{ t('subagentRouting.channelSequence') }}</span>
          <span class="font-mono text-xs text-muted-foreground">{{ enabledCount }}/{{ channels.length }}</span>
          <div class="flex-1" />
          <Input v-model="searchQuery" class="w-full sm:w-56" :placeholder="t('orchestration.searchPlaceholder')" />
        </div>
        <p v-if="!enabledCount" class="text-xs text-amber-600 dark:text-amber-400">{{ t('subagentRouting.noEnabled') }}</p>

        <div class="divide-y divide-border border border-border">
          <div
            v-for="(channel, index) in visibleChannels"
            :key="channel.channelUid"
            :draggable="!searchQuery.trim() && !saving"
            class="grid grid-cols-[20px_28px_minmax(0,1fr)_auto] items-center gap-2 bg-background/50 px-3 py-2.5 transition-opacity"
            :class="{ 'opacity-50': !channel.enabled || draggedUID === channel.channelUid }"
            @dragstart="draggedUID = channel.channelUid"
            @dragover.prevent
            @drop="drop(channel.channelUid)"
            @dragend="draggedUID = null"
          >
            <GripVertical class="h-4 w-4 cursor-grab text-muted-foreground" />
            <span class="font-mono text-xs text-muted-foreground">{{ index + 1 }}</span>
            <div class="min-w-0">
              <div class="flex flex-wrap items-center gap-2">
                <span class="truncate text-xs font-semibold">{{ channel.channelName }}</span>
                <span class="text-[10px] uppercase text-muted-foreground">{{ channel.serviceType }}</span>
                <span v-if="channel.status !== 'active'" class="text-[10px] text-amber-600">{{ t(`status.${channel.status}`) }}</span>
              </div>
              <div class="mt-1 break-all font-mono text-[10px] text-muted-foreground">
                {{ normalizedModel ? `${normalizedModel} → ${previewModel(channel)}` : t('subagentRouting.keepRequestModel') }}
              </div>
            </div>
            <div class="flex items-center gap-1">
              <Button variant="ghost" size="icon" class="h-7 w-7" :disabled="index === 0" :title="t('subagentRouting.moveUp')" @click="move(channel.channelUid, -1)"><ArrowUp class="h-3.5 w-3.5" /></Button>
              <Button variant="ghost" size="icon" class="h-7 w-7" :disabled="index === visibleChannels.length - 1" :title="t('subagentRouting.moveDown')" @click="move(channel.channelUid, 1)"><ArrowDown class="h-3.5 w-3.5" /></Button>
              <Switch v-model="channel.enabled" :disabled="saving" :aria-label="t('subagentRouting.channelEnabled')" />
            </div>
          </div>
          <div v-if="!channels.length" class="py-8 text-center text-xs text-muted-foreground">{{ t('subagentRouting.noChannels') }}</div>
        </div>

        <div class="flex items-center justify-end gap-2">
          <span v-if="dirty" class="mr-auto text-[11px] text-muted-foreground">{{ t('subagentRouting.unsaved') }}</span>
          <Button variant="outline" :disabled="!dirty || saving" @click="cancel">{{ t('common.cancel') }}</Button>
          <Button :disabled="!canSave" @click="save"><Loader2 v-if="saving" class="h-3.5 w-3.5 animate-spin" />{{ t('common.save') }}</Button>
        </div>
      </template>
    </div>
  </section>
</template>
