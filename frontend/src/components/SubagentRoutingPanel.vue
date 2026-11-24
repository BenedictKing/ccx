<template>
  <div class="subagent-panel">
    <div v-if="!activated && legacyModels.length" class="sa-notice sa-notice--info">
      <v-icon size="small" class="sa-notice__icon">mdi-information</v-icon>
      <div>
        <div>{{ t('subagentRouting.legacyHint') }}</div>
        <div v-if="legacyMixed" class="sa-notice__chips">
          <v-chip v-for="item in legacyModels" :key="item.channelUid" size="small" variant="outlined" class="sa-badge">
            {{ item.channelName }}: {{ item.model }}
          </v-chip>
        </div>
      </div>
    </div>
    <v-alert v-if="errorMessage" type="error" variant="tonal" density="comfortable" class="sa-alert" closable @click:close="errorMessage = ''">
      {{ errorMessage }}
    </v-alert>

    <v-skeleton-loader v-if="loading && !channels.length" type="list-item-three-line" class="sa-skeleton" />
    <template v-else>
      <section class="sa-config sa-model-toolbar">
        <div class="sa-config__col">
          <div class="sa-config__label">
            <span class="sa-config__mark"><v-icon size="14">mdi-robot-outline</v-icon></span>
            {{ t('subagentRouting.modelLabel') }}
          </div>
          <v-autocomplete
            v-model="draftModel"
            :items="modelOptions"
            :return-object="false"
            :loading="fetchingModels"
            clearable
            :disabled="saving || loading"
            :placeholder="t('subagentRouting.keepRequestModel')"
            :aria-label="t('subagentRouting.modelLabel')"
            :title="t('subagentRouting.modelHint')"
            variant="outlined"
            density="compact"
            prepend-inner-icon="mdi-robot-outline"
            hide-details
            class="sa-field sa-model"
            @update:menu="onModelMenu"
          >
            <template #selection="{ internalItem }">
              <span class="v-autocomplete__selection-text">{{ internalItem.title }}</span>
            </template>
          </v-autocomplete>
        </div>
        <v-text-field
          v-model="searchQuery"
          density="compact"
          variant="outlined"
          :placeholder="t('orchestration.searchPlaceholder')"
          :aria-label="t('orchestration.searchPlaceholder')"
          prepend-inner-icon="mdi-magnify"
          clearable
          hide-details
          single-line
          :disabled="saving || loading"
          class="sa-search"
        />
      </section>
      <div v-if="modelsUnavailable" class="text-caption text-warning mb-2">{{ t('subagentRouting.modelsUnavailable') }}</div>
      <v-divider />
      <div class="sa-list-wrap">
        <div class="sa-sequence-toolbar">
          <div class="text-subtitle-2 text-medium-emphasis d-flex align-center flex-shrink-0 text-no-wrap">
            <v-icon size="small" class="mr-1" color="success">mdi-play-circle</v-icon>
            {{ t('subagentRouting.channelSequence') }}
            <v-chip size="x-small" class="ml-2">{{ enabledCount }}</v-chip>
          </div>
        </div>
        <div v-if="!enabledCount" class="sa-notice sa-notice--warn">
          <v-icon size="small" class="sa-notice__icon">mdi-alert</v-icon>
          <span>{{ t('subagentRouting.noEnabled') }}</span>
        </div>

        <draggable
          v-model="channels"
          item-key="channelUid"
          handle=".drag-handle"
          ghost-class="ghost"
          :disabled="searchActive || saving"
          class="sa-list"
        >
          <template #item="{ element, index }">
            <div v-show="matchesSearch(element)" class="sa-row" :class="{ 'sa-row--off': !element.enabled, 'sa-row--paused': element.status === 'suspended' }">
              <span class="sa-drag drag-handle" :title="t('subagentRouting.priorityHint')" :aria-label="t('subagentRouting.priorityHint')">
                <v-icon size="small">mdi-drag-vertical</v-icon>
              </span>
              <span class="sa-rank">{{ index + 1 }}</span>
              <div class="sa-row__body">
                <div class="sa-row__title">
                  <span class="sa-name">{{ element.channelName }}</span>
                  <v-chip v-if="element.status !== 'active'" size="x-small" color="warning" variant="tonal" class="sa-badge sa-badge--warn">
                    {{ t(`status.${element.status === 'suspended' ? 'paused' : element.status}`) }}
                  </v-chip>
                </div>
              </div>
              <div class="sa-row__model">
                <div v-if="normalizedModel" class="model-preview sa-preview">
                  <span class="sa-preview__mark">↳</span>
                  <span>{{ previewModel(element) }}</span>
                  <span v-if="previewModel(element) !== normalizedModel" class="sa-preview__from">{{ normalizedModel }} →</span>
                  <v-icon v-if="!element.modelSupported" size="14" color="warning" class="ml-1">mdi-alert-circle-outline</v-icon>
                </div>
              </div>
              <div class="sa-row__tools">
                <div class="sa-toggle" :title="t('subagentRouting.channelEnabled')">
                  <span class="sa-toggle__text">{{ t('subagentRouting.enabledShort') }}</span>
                  <v-switch v-model="element.enabled" color="primary" density="compact" hide-details inset :disabled="saving" :aria-label="t('subagentRouting.channelEnabled')" />
                </div>
              </div>
            </div>
          </template>
        </draggable>

        <div v-if="!channels.length" class="sa-empty">{{ t('subagentRouting.noChannels') }}</div>
      </div>

      <div class="sa-foot">
        <span v-if="dirty" class="sa-foot__dirty"><span class="sa-foot__dot"></span>{{ t('subagentRouting.unsaved') }}</span>
        <span v-else class="sa-foot__hint">{{ enabledCount }}/{{ channels.length }} {{ t('subagentRouting.enabledShort') }}</span>
        <v-spacer />
        <v-btn variant="outlined" class="sa-btn" :disabled="!dirty || saving" @click="cancel">{{ t('app.actions.cancel') }}</v-btn>
        <v-btn color="primary" variant="flat" class="sa-btn sa-btn--primary" :loading="saving" :disabled="!canSave" @click="save">{{ t('app.actions.save') }}</v-btn>
      </div>
    </template>
  </div>
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
const searchActive = computed(() => Boolean(searchQuery.value?.trim()))
const currentState = computed(() => JSON.stringify({ model: normalizedModel.value, channels: channels.value.map(channel => ({ channelUid: channel.channelUid, enabled: channel.enabled })) }))
const dirty = computed(() => Boolean(savedState.value) && currentState.value !== savedState.value)
const canSave = computed(() => Boolean(savedState.value) && !loading.value && !saving.value && (dirty.value || !activated.value))
const { modelOptions, fetchingModels, modelsUnavailable, fetchModelOptions } = useChannelModelOptions(
  computed(() => props.channelType), computed(() => props.availableChannels || [])
)

function onModelMenu(open: boolean) {
  if (open) void fetchModelOptions()
}

// 普通请求 disabled 的渠道在子代理侧仍可启用：必须留在列表并随保存整表写回，否则会被移出策略。
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
/* Neo-brutalism, the same system as the regular channel orchestration tab. */
.subagent-panel {
  --sa-ink: rgb(var(--v-theme-on-surface));
  --sa-edge: 2px solid var(--sa-ink);
  --sa-shadow: 4px 4px 0 0 var(--sa-ink);
  position: relative;
  margin: 0 0 24px;
  background: transparent;
  border: 0;
  box-shadow: none;
  overflow: visible;
}
.v-theme--dark .subagent-panel { --sa-ink: rgba(255, 255, 255, 0.78); }
.v-theme--dark .sa-foot__hint,
.v-theme--dark .sa-toggle__text { color: rgba(var(--v-theme-on-surface), 0.82); }

/* Shared retro badge */
.sa-badge.v-chip {
  border: 1px solid var(--sa-ink);
  border-radius: 0 !important;
  height: 22px;
  font-size: 0.75rem;
  letter-spacing: 0;
}
.sa-badge--warn.v-chip {
  color: rgb(var(--v-theme-warning));
  border-color: rgb(var(--v-theme-warning));
  background: rgba(var(--v-theme-warning), 0.12) !important;
}

/* Buttons */
.sa-btn.v-btn {
  border: var(--sa-edge);
  border-radius: 0;
  box-shadow: 3px 3px 0 0 var(--sa-ink);
  letter-spacing: 0;
}
.sa-btn.v-btn:hover { transform: translate(-1px, -1px); box-shadow: 4px 4px 0 0 var(--sa-ink); }
.sa-btn.v-btn:active { transform: translate(2px, 2px); box-shadow: none; }
.sa-btn--primary.v-btn { color: #fff; }

/* Notices */
.sa-notice {
  display: flex;
  align-items: flex-start;
  gap: 10px;
  margin: 14px 20px 0;
  padding: 10px 12px;
  border: var(--sa-edge);
  box-shadow: var(--sa-shadow);
  font-size: 0.8125rem;
  line-height: 1.5;
  background: rgba(var(--v-theme-info), 0.08);
}
.sa-notice--warn { background: rgba(var(--v-theme-warning), 0.12); }
.sa-notice__icon { flex: 0 0 auto; margin-top: 2px; }
.sa-notice__chips { display: flex; flex-wrap: wrap; gap: 6px; margin-top: 8px; }
.sa-alert.v-alert {
  margin: 14px 20px 0;
  border: var(--sa-edge);
  border-radius: 0;
  box-shadow: var(--sa-shadow);
}
.sa-skeleton { padding: 20px; }

/* Default model configuration */
.sa-model-toolbar { display: flex; align-items: center; justify-content: space-between; gap: 16px; padding: 12px 0; }
.sa-model-toolbar > .sa-model { flex: 1; margin-bottom: 0; }
.sa-config__col { display: flex; align-items: center; flex: 1; gap: 10px; min-width: 0; }
.sa-config__col .sa-field { flex: 1 1 auto; min-width: 0; }
.sa-config__label {
  display: flex;
  align-items: center;
  flex-wrap: nowrap;
  gap: 6px;
  flex: 0 0 auto;
  white-space: nowrap;
  font-size: 0.8125rem;
  font-weight: 700;
  line-height: 1.3;
  color: rgb(var(--v-theme-on-surface));
}
.sa-config__mark {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 20px;
  height: 20px;
  border: 1px solid var(--sa-ink);
  background: rgba(var(--v-theme-primary), 0.14);
}
.sa-sequence-toolbar {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 16px;
  margin-bottom: 8px;
}
.sa-model { width: 100%; max-width: 360px; }
.sa-model.v-autocomplete--active-menu :deep(.v-autocomplete__selection) { display: none; }
.sa-search { width: clamp(220px, 28vw, 320px); max-width: 100%; flex: 0 1 auto; }
/* Retro form fields: hard 2px outline, square corners */
.sa-field :deep(.v-field__outline) {
  --v-field-border-width: 2px;
  --v-field-border-opacity: 1;
  color: var(--sa-ink);
}
.sa-field :deep(.v-field) { border-radius: 0 !important; box-shadow: 3px 3px 0 0 var(--sa-ink); }

/* Channel list */
.sa-list-wrap { padding: 12px 0 8px; }
.sa-list { display: flex; flex-direction: column; gap: 8px; }
.sa-row {
  display: grid;
  grid-template-columns: 30px 28px minmax(0, 1fr) minmax(140px, 240px) auto;
  align-items: center;
  gap: 10px;
  min-height: 60px;
  margin: 2px;
  padding: 10px 12px;
  background: rgb(var(--v-theme-surface));
  border: var(--sa-edge);
  box-shadow: var(--sa-shadow);
  transition: transform 0.1s ease, box-shadow 0.1s ease, background-color 0.1s ease, opacity 0.1s ease;
}
.sa-row:hover { transform: translate(-2px, -2px); box-shadow: 6px 6px 0 0 var(--sa-ink); background: rgba(var(--v-theme-primary), 0.06); }
/* 与普通请求页签的暂停行共用同一组状态色变量，不另立一套配色 */
.sa-row--paused {
  background: var(--ccx-channel-row-suspended-bg);
  border-color: var(--ccx-channel-row-suspended-border);
  box-shadow: var(--ccx-channel-row-shadow);
}
.sa-row--paused:hover {
  background: var(--ccx-channel-row-suspended-hover-bg);
  box-shadow: var(--ccx-channel-row-shadow-hover);
}
.sa-row--off { opacity: 0.58; }
.ghost { opacity: 0.5; background: rgba(var(--v-theme-primary), 0.18); border: 2px dashed rgb(var(--v-theme-primary)); box-shadow: none; }

.sa-drag {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 32px;
  cursor: grab;
  color: rgba(var(--v-theme-on-surface), 0.82);
}
.sa-drag:hover { background: rgba(var(--v-theme-primary), 0.18); }
.sa-drag:active { cursor: grabbing; }
.sa-rank {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border: var(--sa-edge);
  background: rgb(var(--v-theme-primary));
  color: #fff;
  font-size: 0.75rem;
  font-weight: 700;
  font-variant-numeric: tabular-nums;
}
.sa-row__body { min-width: 0; min-height: 38px; display: flex; flex-direction: column; justify-content: center; }
.sa-row__title { display: flex; align-items: center; flex-wrap: wrap; gap: 8px; min-width: 0; }
.sa-name { font-size: 0.875rem; font-weight: 700; color: rgb(var(--v-theme-on-surface)); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.sa-row__model {
  display: flex;
  align-items: center;
  min-width: 0;
  min-height: 38px;
}
.sa-preview {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 6px;
  font-size: 0.75rem;
  color: rgba(var(--v-theme-on-surface), 0.78);
  overflow-wrap: anywhere;
}
.sa-preview__mark { color: rgba(var(--v-theme-on-surface), 0.62); font-weight: 700; }
.sa-preview__from { color: rgba(var(--v-theme-on-surface), 0.55); }

.sa-row__tools { display: flex; align-items: center; }
.sa-toggle { display: flex; align-items: center; gap: 8px; }
.sa-toggle__text { font-size: 0.75rem; font-weight: 700; color: rgba(var(--v-theme-on-surface), 0.72); white-space: nowrap; }
.sa-toggle :deep(.v-switch__track) { border-radius: 0 !important; border: var(--sa-edge); }
.sa-toggle :deep(.v-switch__thumb) { border-radius: 0 !important; }

.sa-empty {
  padding: 34px 16px;
  text-align: center;
  font-size: 0.8125rem;
  color: rgba(var(--v-theme-on-surface), 0.62);
  border: 2px dashed var(--sa-ink);
}

/* Save bar stays reachable on long channel lists */
.sa-foot {
  position: sticky;
  bottom: 0;
  z-index: 3;
  display: flex;
  align-items: center;
  gap: 10px;
  padding: 12px 20px;
  border-top: var(--sa-edge);
  background: rgb(var(--v-theme-surface));
}
.sa-foot__hint { font-size: 0.8125rem; color: rgba(var(--v-theme-on-surface), 0.72); }
.sa-foot__dirty {
  display: inline-flex;
  align-items: center;
  gap: 7px;
  font-size: 0.75rem;
  font-weight: 700;
  color: rgb(var(--v-theme-warning));
}
.sa-foot__dot { width: 8px; height: 8px; background: rgb(var(--v-theme-warning)); border: 1px solid var(--sa-ink); }

@media (max-width: 1100px) {
  .sa-row { grid-template-columns: 30px 28px minmax(0, 1fr) auto; }
  .sa-row__body { grid-column: 3; grid-row: 1; }
  .sa-row__model { grid-column: 3; grid-row: 2; min-height: 0; padding-left: 0; border-left: 0; }
  .sa-row__tools { grid-column: 4; grid-row: 1 / 3; }
}
@media (max-width: 900px) {
  .sa-config__col { flex-direction: column; align-items: stretch; gap: 4px; }
  .sa-config__col .sa-field { width: 100%; }
  .sa-model { max-width: 100%; }
}
@media (max-width: 700px) {
  .sa-model-toolbar { flex-direction: column; align-items: stretch; gap: 8px; }
  .sa-search { width: 100%; }
  .sa-notice, .sa-alert.v-alert { margin: 12px 14px 0; }
  .sa-foot { padding: 12px 14px; }
  .sa-row { grid-template-columns: 26px 28px minmax(0, 1fr) auto; gap: 8px; padding: 9px; }
  .sa-row__body { grid-column: 3; grid-row: 1; min-height: 0; }
  .sa-row__model { grid-column: 3; grid-row: 2; }
  .sa-row__tools { grid-column: 4; grid-row: 1 / 3; justify-content: flex-end; }
  .sa-toggle__text { display: none; }
  .sa-row:hover { transform: none; }
}
@media (prefers-reduced-motion: reduce) {
  .sa-row, .sa-btn.v-btn { transition: none; }
  .sa-row:hover, .sa-btn.v-btn:hover { transform: none; }
}
</style>
