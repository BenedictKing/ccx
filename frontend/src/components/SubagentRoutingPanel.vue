<template>
  <v-card elevation="0" rounded="lg" variant="flat" class="subagent-panel mb-6">
    <div class="panel-heading">
      <div>
        <div class="d-flex align-center ga-2">
          <v-icon color="primary">mdi-account-multiple-outline</v-icon>
          <span class="text-h6">{{ t('subagentRouting.title') }}</span>
          <v-chip size="x-small" :color="activated ? 'success' : 'info'" variant="tonal">
            {{ t(activated ? 'subagentRouting.active' : 'subagentRouting.draft') }}
          </v-chip>
        </div>
        <div class="text-body-2 text-medium-emphasis mt-1">{{ t('subagentRouting.description') }}</div>
      </div>
      <v-btn variant="text" size="small" prepend-icon="mdi-refresh" :loading="loading" @click="load">
        {{ t('subagentRouting.reload') }}
      </v-btn>
    </div>

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
      <v-combobox
        v-model="draftModel"
        :items="modelOptions"
        item-title="title"
        item-value="value"
        :label="t('subagentRouting.modelLabel')"
        :hint="t('subagentRouting.modelHint')"
        persistent-hint
        variant="outlined"
        density="comfortable"
        prepend-inner-icon="mdi-robot-outline"
        class="model-field"
      />

      <div class="list-toolbar">
        <div class="d-flex align-center ga-2">
          <span class="text-subtitle-2">{{ t('subagentRouting.channelSequence') }}</span>
          <v-chip size="x-small" variant="tonal">{{ enabledCount }}/{{ channels.length }}</v-chip>
        </div>
        <v-text-field
          v-model="searchQuery"
          density="compact"
          variant="outlined"
          :placeholder="t('orchestration.searchPlaceholder')"
          prepend-inner-icon="mdi-magnify"
          clearable
          hide-details
          single-line
          class="channel-search"
        />
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
                  {{ t(`status.${element.status}`) }}
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
        <v-btn color="primary" :loading="saving" :disabled="!dirty || saving" @click="save">{{ t('app.actions.save') }}</v-btn>
      </div>
    </template>
  </v-card>
</template>

<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { onBeforeRouteLeave, onBeforeRouteUpdate } from 'vue-router'
import draggable from 'vuedraggable'
import { api, ApiError, type SubagentRoutingChannelView, type SubagentRoutingView, type TextChannelKind } from '../services/api'
import { useI18n } from '../i18n'

const props = defineProps<{ channelType: TextChannelKind }>()
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

const normalizedModel = computed(() => (typeof draftModel.value === 'string' ? draftModel.value.trim() : ''))
const enabledCount = computed(() => channels.value.filter(channel => channel.enabled).length)
const currentState = computed(() => JSON.stringify({ model: normalizedModel.value, channels: channels.value.map(channel => ({ channelUid: channel.channelUid, enabled: channel.enabled })) }))
const dirty = computed(() => Boolean(savedState.value) && currentState.value !== savedState.value)
const modelOptions = computed(() => {
  const models = props.channelType === 'messages'
    ? ['haiku', 'sonnet', 'opus', 'fable']
    : props.channelType === 'gemini'
      ? ['gemini-2.5-flash-lite', 'gemini-2.5-flash', 'gemini-3.5-flash', 'gemini-2.5-pro']
      : ['sol', 'luna', 'terra', 'astra']
  return [{ title: t('subagentRouting.keepRequestModel'), value: '' }, ...models.map(model => ({ title: model, value: model }))]
})

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
  try {
    const view = await api.getSubagentRouting(props.channelType)
    if (sequence === requestSequence) applyView(view)
  } catch (error) {
    if (sequence === requestSequence) errorMessage.value = error instanceof Error ? error.message : String(error)
  } finally {
    if (sequence === requestSequence) loading.value = false
  }
}

function matchesSearch(channel: SubagentRoutingChannelView) {
  const query = searchQuery.value?.trim().toLowerCase()
  return !query || channel.channelName.toLowerCase().includes(query) || channel.serviceType.toLowerCase().includes(query)
}

function previewModel(channel: SubagentRoutingChannelView) {
  const model = normalizedModel.value
  if (!model) return t('subagentRouting.keepRequestModel')
  const mapping = channel.modelMapping || {}
  if (mapping[model]) return mapping[model]
  const source = Object.keys(mapping).filter(key => model.includes(key)).sort((a, b) => b.length - a.length)[0]
  return source ? mapping[source] : model
}

function move(index: number, offset: number) {
  const target = index + offset
  if (target < 0 || target >= channels.value.length) return
  const [channel] = channels.value.splice(index, 1)
  channels.value.splice(target, 0, channel)
}

function cancel() {
  if (!savedState.value) return
  const state = JSON.parse(savedState.value) as SubagentRoutingView['policy']
  draftModel.value = state.model
  const byUID = new Map(state.channels.map(channel => [channel.channelUid, channel]))
  channels.value = state.channels.map(entry => ({ ...channels.value.find(channel => channel.channelUid === entry.channelUid)!, enabled: byUID.get(entry.channelUid)!.enabled }))
  errorMessage.value = ''
}

async function save() {
  saving.value = true
  errorMessage.value = ''
  try {
    const view = await api.updateSubagentRouting(props.channelType, revision.value, {
      model: normalizedModel.value,
      channels: channels.value.map(channel => ({ channelUid: channel.channelUid, enabled: channel.enabled }))
    })
    applyView(view)
    emit('success', t('subagentRouting.saved'))
  } catch (error) {
    errorMessage.value = error instanceof Error ? error.message : String(error)
    if (error instanceof ApiError && error.status === 409) {
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
  void load()
}, { immediate: true })
onBeforeRouteUpdate(() => confirmDiscard())
onBeforeRouteLeave(() => confirmDiscard())
</script>

<style scoped>
.subagent-panel { border: 1px solid rgba(var(--v-border-color), var(--v-border-opacity)); }
.panel-heading { display: flex; align-items: flex-start; justify-content: space-between; gap: 16px; margin-bottom: 20px; }
.model-field { max-width: 720px; margin-bottom: 20px; }
.list-toolbar { display: flex; align-items: center; justify-content: space-between; gap: 16px; padding: 12px 0; border-top: 1px solid rgba(var(--v-border-color), var(--v-border-opacity)); }
.channel-search { max-width: 320px; }
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
  .panel-heading, .list-toolbar { align-items: stretch; flex-direction: column; }
  .channel-search { max-width: none; }
  .channel-row { grid-template-columns: 20px 24px minmax(0, 1fr) auto; gap: 6px; padding: 8px; }
  .move-actions { grid-column: 3; }
  .channel-row :deep(.v-switch) { grid-column: 4; grid-row: 1 / 3; }
}
</style>
