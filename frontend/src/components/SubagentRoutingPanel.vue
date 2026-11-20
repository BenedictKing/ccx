<template>
  <v-card elevation="0" rounded="lg" variant="flat" class="subagent-panel mb-6">
    <v-alert v-if="!activated && legacyModels.length" type="info" variant="tonal" density="comfortable" class="mb-4">
      <div>{{ t('subagentRouting.legacyHint') }}</div>
      <div v-if="legacyMixed" class="mt-2 d-flex flex-wrap ga-2">
        <v-chip v-for="item in legacyModels" :key="item.channelUid" size="small" variant="outlined">
          {{ item.channelName }}: {{ item.model }}
        </v-chip>
      </div>
    </v-alert>
    <v-alert v-if="errorMessage" type="error" variant="tonal" density="comfortable" class="mb-4" closable @click:close="errorMessage = ''">
      {{ errorMessage }}
    </v-alert>

    <v-skeleton-loader v-if="loading && !channels.length" type="list-item-three-line" />
    <template v-else>
      <section class="sa-model-toolbar">
      <v-autocomplete
        v-model="draftModel"
        :items="modelOptions"
        :return-object="false"
        :loading="fetchingModels"
        clearable
        :disabled="saving || loading"
        :label="t('subagentRouting.modelLabel')"
        :hint="t('subagentRouting.modelHint')"
        persistent-hint
        variant="outlined"
        density="comfortable"
        prepend-inner-icon="mdi-robot-outline"
        class="model-field sa-model"
        :placeholder="t('subagentRouting.keepRequestModel')"
        @update:menu="onModelMenu"
      >
        <template #selection="{ internalItem }">
          <span class="v-autocomplete__selection-text">{{ internalItem.title }}</span>
        </template>
      </v-autocomplete>
        <v-text-field
          v-model="searchQuery"
          density="compact"
          variant="outlined"
          :placeholder="t('orchestration.searchPlaceholder')"
          prepend-inner-icon="mdi-magnify"
          clearable
          hide-details
          single-line
          class="sa-search"
        />
      </section>
      <div v-if="modelsUnavailable" class="text-caption text-warning mt-2">{{ t('subagentRouting.modelsUnavailable') }}</div>
      <v-divider />

      <div class="sa-sequence-toolbar">
        <div class="text-subtitle-2 text-medium-emphasis d-flex align-center flex-shrink-0 text-no-wrap">
          <v-icon size="small" class="mr-1" color="success">mdi-play-circle</v-icon>
          {{ t('subagentRouting.channelSequence') }}
          <v-chip size="x-small" class="ml-2">{{ enabledCount }}</v-chip>
        </div>

      </div>
      <div v-if="!enabledCount" class="text-caption text-warning mb-2">{{ t('subagentRouting.noEnabled') }}</div>

      <draggable
        v-model="channels"
        item-key="channelUid"
        handle=".drag-handle"
        ghost-class="ghost"
        :disabled="Boolean(searchQuery?.trim()) || saving"
        class="channel-list"
      >
        <template #item="{ element, index }">
          <div v-show="matchesSearch(element)" class="channel-row" :class="{ 'channel-disabled': !element.enabled }">
            <v-icon class="drag-handle" size="small" color="grey">mdi-drag-vertical</v-icon>
            <span class="order-number">{{ index + 1 }}</span>
            <div class="channel-identity">
              <div class="d-flex align-center flex-wrap ga-2">
                <span class="font-weight-medium">{{ element.channelName }}</span>
                <span class="text-caption text-disabled">{{ element.serviceType }}</span>
                <v-chip v-if="element.status !== 'active'" size="x-small" color="warning" variant="tonal">
                  {{ t(`status.${element.status === 'suspended' ? 'paused' : element.status}`) }}
                </v-chip>
              </div>
              <div class="text-caption text-medium-emphasis model-preview">
                <template v-if="normalizedModel">
                  {{ normalizedModel }} → {{ previewModel(element) }}
                </template>
                <template v-else>{{ t('subagentRouting.keepRequestModel') }}</template>
                <v-icon v-if="normalizedModel && !element.modelSupported" size="14" color="warning" class="ml-1">mdi-alert-circle-outline</v-icon>
              </div>
            </div>
            <div class="move-actions">
              <v-btn icon size="x-small" variant="text" :disabled="index === 0 || saving" :title="t('subagentRouting.moveUp')" @click="move(index, -1)">
                <v-icon size="small">mdi-arrow-up-bold</v-icon>
              </v-btn>
              <v-btn icon size="x-small" variant="text" :disabled="index === channels.length - 1 || saving" :title="t('subagentRouting.moveDown')" @click="move(index, 1)">
                <v-icon size="small">mdi-arrow-down</v-icon>
              </v-btn>
            </div>
            <v-switch v-model="element.enabled" color="primary" density="compact" hide-details inset :disabled="saving" :aria-label="t('subagentRouting.channelEnabled')" />
          </div>
        </template>
      </draggable>

      <div v-if="!channels.length" class="empty-list">{{ t('subagentRouting.noChannels') }}</div>
      <div class="panel-actions">
        <span v-if="dirty" class="text-caption text-medium-emphasis">{{ t('subagentRouting.unsaved') }}</span>
        <v-spacer />
        <v-btn variant="text" :disabled="!dirty || saving" @click="cancel">{{ t('app.actions.cancel') }}</v-btn>
        <v-btn color="primary" :loading="saving" :disabled="!canSave" @click="save">{{ t('app.actions.save') }}</v-btn>
      </div>
    </template>
  </v-card>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate } from 'vue-router'
import draggable from 'vuedraggable'
import { api, ApiError, type Channel, type SubagentRoutingChannelView, type SubagentRoutingView, type TextChannelKind } from '../services/api'
import { useI18n } from '../i18n'
import { useChannelModelOptions } from '../composables/useChannelModelOptions'
import { resolveMappedModel } from '../../../shared/model-mapping'

const props = defineProps<{ channelType: TextChannelKind; availableChannels?: Channel[] }>()
const emit = defineEmits<{ success: [message: string]; error: [message: string] }>()
const { t } = useI18n()
const loading = ref(false)
const saving = ref(false)
const activated = ref(false)
const revision = ref('')
const legacyModels = ref<NonNullable<SubagentRoutingView['legacyModels']>>([])
const legacyMixed = ref(false)
const channels = ref<SubagentRoutingChannelView[]>([])
const draftModel = ref<string | null>('')
const searchQuery = ref('')
const errorMessage = ref('')
const savedState = ref('')
let requestSequence = 0
let pendingLoad = 0
let latestView: SubagentRoutingView | undefined

const normalizedModel = computed(() => (typeof draftModel.value === 'string' ? draftModel.value.trim() : ''))
const enabledCount = computed(() => channels.value.filter(channel => channel.enabled).length)
const currentState = computed(() => JSON.stringify({ model: normalizedModel.value, channels: channels.value.map(channel => ({ channelUid: channel.channelUid, enabled: channel.enabled })) }))
const dirty = computed(() => Boolean(savedState.value) && currentState.value !== savedState.value)
const canSave = computed(() => Boolean(savedState.value) && !loading.value && !saving.value && (dirty.value || !activated.value))
const { modelOptions, fetchingModels, modelsUnavailable, fetchModelOptions } = useChannelModelOptions(
  computed(() => props.channelType), computed(() => props.availableChannels || [])
)

function onModelMenu(open: boolean) {
  if (open) void fetchModelOptions()
}

function viewChannels(view: SubagentRoutingView) {
  return (view.channels || []).map(channel => ({
    ...channel, enabled: view.policy.channels.find(entry => entry.channelUid === channel.channelUid)?.enabled ?? channel.enabled
  }))
}

function applyView(view: SubagentRoutingView) {
  latestView = view
  activated.value = view.activated
  revision.value = view.revision
  legacyMixed.value = view.legacyMixed
  const visible = viewChannels(view)
  const visibleUIDs = new Set(visible.map(channel => channel.channelUid))
  legacyModels.value = (view.legacyModels || []).filter(item => visibleUIDs.has(item.channelUid))
  channels.value = visible
  draftModel.value = view.policy.model || null
  savedState.value = JSON.stringify({ model: view.policy.model.trim(), channels: channels.value.map(channel => ({ channelUid: channel.channelUid, enabled: channel.enabled })) })
  errorMessage.value = ''
}

function synchronizeView(view: SubagentRoutingView) {
  if (!dirty.value) return applyView(view)
  latestView = view
  const incoming = viewChannels(view)
  activated.value = view.activated
  legacyMixed.value = view.legacyMixed
  const incomingUIDs = new Set(incoming.map(channel => channel.channelUid))
  legacyModels.value = (view.legacyModels || []).filter(item => incomingUIDs.has(item.channelUid))
  const byUID = new Map(incoming.map(channel => [channel.channelUid, channel]))
  const previousUIDs = new Set(channels.value.map(channel => channel.channelUid))
  // 后台刷新只更新渠道信息；用户的模型、开关、顺序和保存版本仍留在草稿中。
  channels.value = [
    ...channels.value.flatMap(channel => {
      const updated = byUID.get(channel.channelUid)
      return updated ? [{ ...updated, enabled: channel.enabled }] : []
    }),
    ...incoming.filter(channel => !previousUIDs.has(channel.channelUid))
  ]
}

async function load(background = false) {
  if (background && (pendingLoad || saving.value)) return
  const sequence = ++requestSequence
  pendingLoad = sequence
  if (!background) loading.value = true
  try {
    const view = await api.getSubagentRouting(props.channelType)
    if (sequence === requestSequence) synchronizeView(view)
  } catch (error) {
    if (sequence === requestSequence && !background) errorMessage.value = error instanceof Error ? error.message : String(error)
  } finally {
    if (pendingLoad === sequence) {
      pendingLoad = 0
      loading.value = false
    }
  }
}

function matchesSearch(channel: SubagentRoutingChannelView) {
  const query = searchQuery.value?.trim().toLowerCase()
  return !query || channel.channelName.toLowerCase().includes(query) || channel.serviceType.toLowerCase().includes(query)
}

function previewModel(channel: SubagentRoutingChannelView) {
  const model = normalizedModel.value
  if (!model) return t('subagentRouting.keepRequestModel')
  return resolveMappedModel(model, channel.modelMapping)
}

function move(index: number, offset: number) {
  if (saving.value || loading.value) return
  const target = index + offset
  if (target < 0 || target >= channels.value.length) return
  const [channel] = channels.value.splice(index, 1)
  channels.value.splice(target, 0, channel)
}

function cancel() {
  if (latestView) applyView(latestView)
}

async function save() {
  if (!canSave.value) return
  const sequence = ++requestSequence
  saving.value = true
  errorMessage.value = ''
  try {
    const view = await api.updateSubagentRouting(props.channelType, revision.value, {
      model: normalizedModel.value,
      channels: channels.value.map(channel => ({ channelUid: channel.channelUid, enabled: channel.enabled }))
    })
    if (sequence !== requestSequence) return
    applyView(view)
    emit('success', t('subagentRouting.saved'))
  } catch (error) {
    if (sequence !== requestSequence) return
    errorMessage.value = error instanceof Error ? error.message : String(error)
    if (error instanceof ApiError && error.status === 409) {
      try {
        const view = await api.getSubagentRouting(props.channelType)
        if (sequence !== requestSequence) return
        synchronizeView(view)
      } catch {
        // 自动刷新会继续重试，保存失败时不丢弃当前草稿。
      }
      if (sequence !== requestSequence) return
      errorMessage.value = `${errorMessage.value} ${t('subagentRouting.conflictHint')}`
    }
    emit('error', errorMessage.value)
  } finally {
    saving.value = false
  }
}

function confirmDiscard() {
  return !dirty.value || window.confirm(t('subagentRouting.discardConfirm'))
}

watch(() => props.channelType, () => {
  searchQuery.value = ''
  channels.value = []
  savedState.value = ''
  latestView = undefined
  draftModel.value = null
  void load()
}, { immediate: true })
watch(() => props.availableChannels, () => { void load(true) })
onBeforeRouteUpdate(() => confirmDiscard())
onBeforeRouteLeave(() => confirmDiscard())
</script>

<style scoped>
.sa-model-toolbar { display: flex; align-items: center; justify-content: space-between; gap: 16px; padding: 12px 0; }
.sa-model-toolbar > .sa-model { flex: 1; margin-bottom: 0; }
.sa-search { width: clamp(220px, 28vw, 320px); max-width: 100%; flex: 0 1 auto; }
@media (max-width: 700px) {
  .sa-model-toolbar { flex-direction: column; align-items: stretch; gap: 8px; }
  .sa-search { width: 100%; }
}
.sa-model.v-autocomplete--active-menu :deep(.v-autocomplete__selection) { display: none; }
.subagent-panel { margin: 0 0 24px; border: 1px solid rgba(var(--v-border-color), var(--v-border-opacity)); }
.model-field { max-width: 720px; margin-bottom: 20px; }
.sa-sequence-toolbar { display: flex; align-items: center; justify-content: space-between; gap: 16px; margin-bottom: 8px; }

.channel-list { display: flex; flex-direction: column; gap: 6px; }
.channel-row { display: grid; grid-template-columns: 24px 32px minmax(0, 1fr) auto auto; align-items: center; gap: 10px; min-height: 64px; padding: 8px 12px; border: 1px solid rgba(var(--v-border-color), var(--v-border-opacity)); border-radius: 8px; background: rgb(var(--v-theme-surface)); transition: border-color 160ms ease, opacity 160ms ease; }
.channel-row:hover { border-color: rgba(var(--v-theme-primary), 0.45); }
.channel-disabled { opacity: 0.62; }
.drag-handle { cursor: grab; }
.order-number { color: rgba(var(--v-theme-on-surface), 0.55); font-variant-numeric: tabular-nums; }
.channel-identity { min-width: 0; }
.model-preview { margin-top: 3px; overflow-wrap: anywhere; }
.move-actions { display: flex; gap: 2px; }
.ghost { opacity: 0.4; }
.empty-list { padding: 32px; text-align: center; color: rgba(var(--v-theme-on-surface), 0.6); border: 1px dashed rgba(var(--v-border-color), var(--v-border-opacity)); }
.panel-actions { display: flex; align-items: center; gap: 8px; margin-top: 16px; }
@media (max-width: 700px) {
  .sa-sequence-toolbar { align-items: stretch; flex-direction: column; }
  .channel-row { grid-template-columns: 20px 24px minmax(0, 1fr) auto; gap: 6px; padding: 8px; }
  .move-actions { grid-column: 3; }
  .channel-row :deep(.v-switch) { grid-column: 4; grid-row: 1 / 3; }
}
</style>
