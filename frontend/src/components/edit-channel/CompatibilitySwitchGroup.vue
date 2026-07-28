<template>
  <v-card variant="outlined" class="pa-4">
    <div class="d-flex align-center justify-space-between mb-3">
      <div class="text-caption font-weight-bold text-uppercase text-medium-emphasis">
        <v-icon size="small" color="primary" class="mr-1">mdi-format-align-justify</v-icon>
        {{ t('channelEditor.compat.title') }}
      </div>
      <v-btn
        v-if="channelType !== 'images' && channelType !== 'vectors' && !diagnoseResult"
        size="x-small"
        variant="tonal"
        :loading="diagnosing"
        prepend-icon="mdi-stethoscope"
        @click="$emit('diagnose')"
      >{{ t('channelEditor.compat.diagnose') }}</v-btn>
      <v-btn
        v-else-if="channelType !== 'images' && channelType !== 'vectors' && diagnoseResult?.type === 'error'"
        size="x-small"
        variant="tonal"
        color="error"
        prepend-icon="mdi-alert-circle"
        @click="$emit('diagnose')"
      >{{ t('channelEditor.compat.diagnoseFailedShort') }}</v-btn>
      <v-btn
        v-else-if="channelType !== 'images' && channelType !== 'vectors' && diagnoseResult && diagnoseResult.appliedCount > 0"
        size="x-small"
        variant="tonal"
        color="success"
        prepend-icon="mdi-check-circle"
        @click="$emit('diagnose')"
      >{{ t('channelEditor.compat.diagnoseAppliedShort', { count: diagnoseResult.appliedCount }) }}</v-btn>
      <v-btn
        v-else-if="channelType !== 'images' && channelType !== 'vectors' && diagnoseResult"
        size="x-small"
        variant="tonal"
        prepend-icon="mdi-check"
        @click="$emit('diagnose')"
      >{{ t('channelEditor.compat.diagnoseNoChangeShort') }}</v-btn>
    </div>

    <v-expand-transition>
      <v-alert
        v-if="diagnoseResult"
        :type="diagnoseResult.type === 'error' ? 'error' : 'success'"
        variant="tonal"
        density="compact"
        class="mb-3 text-caption"
        :icon="diagnoseResult.type === 'error' ? 'mdi-alert-circle' : 'mdi-check-circle'"
      >
        {{ diagnoseResult.message }}
      </v-alert>
    </v-expand-transition>

    <div class="d-flex flex-column ga-3 compat-options">
      <!-- Codex Tool Compat -->
      <div v-if="channelType === 'responses'" class="d-flex align-center justify-space-between">
        <div class="d-flex align-center ga-2">
          <v-icon color="primary">mdi-cog</v-icon>
          <div>
            <div class="section-title section-title--soft">{{ t('channelEditor.compat.codexCompat.label') }}</div>
            <div class="text-caption text-medium-emphasis">{{ t('channelEditor.compat.codexCompat.hint') }}</div>
          </div>
        </div>
        <v-switch :model-value="form.codexToolCompat" inset color="primary" hide-details @update:model-value="updateField('codexToolCompat', $event)" />
      </div>

      <!-- Convert Images URL to b64_json -->
      <div v-if="channelType === 'images'" class="d-flex align-center justify-space-between">
        <div class="d-flex align-center ga-2">
          <v-icon color="primary">mdi-image-multiple</v-icon>
          <div>
            <div class="section-title section-title--soft">{{ t('channelEditor.compat.convertImageUrlToB64Json.label') }}</div>
            <div class="text-caption text-medium-emphasis">{{ t('channelEditor.compat.convertImageUrlToB64Json.hint') }}</div>
          </div>
        </div>
        <v-switch :model-value="form.convertImageUrlToB64Json" inset color="primary" hide-details @update:model-value="updateField('convertImageUrlToB64Json', $event)" />
      </div>

      <!-- Normalize System Role To TopLevel -->
      <div v-if="channelType === 'messages'" class="d-flex align-center justify-space-between">
        <div class="d-flex align-center ga-2">
          <v-icon color="warning">mdi-arrow-collapse-up</v-icon>
          <div>
            <div class="section-title section-title--soft">{{ t('channelEditor.compat.normalizeSystem.label') }}</div>
            <div class="text-caption text-medium-emphasis">{{ t('channelEditor.compat.normalizeSystem.hint') }}</div>
          </div>
        </div>
        <v-switch :model-value="form.normalizeSystemRoleToTopLevel" inset color="warning" hide-details @update:model-value="updateField('normalizeSystemRoleToTopLevel', $event)" />
      </div>

      <!-- Normalize Metadata UserId -->
      <div v-if="channelType === 'messages'" class="d-flex align-center justify-space-between">
        <div class="d-flex align-center ga-2">
          <v-icon color="primary">mdi-identifier</v-icon>
          <div>
            <div class="section-title section-title--soft">{{ t('channelEditor.compat.normalizeUserId.label') }}</div>
            <div class="text-caption text-medium-emphasis">{{ t('channelEditor.compat.normalizeUserId.hint') }}</div>
          </div>
        </div>
        <v-switch :model-value="form.normalizeMetadataUserId" inset color="primary" hide-details @update:model-value="updateField('normalizeMetadataUserId', $event)" />
      </div>

      <!-- Strip Billing Header -->
      <div v-if="channelType === 'messages'" class="d-flex align-center justify-space-between">
        <div class="d-flex align-center ga-2">
          <v-icon color="warning">mdi-tag-off</v-icon>
          <div>
            <div class="section-title section-title--soft">{{ t('channelEditor.compat.stripBillingHeader.label') }}</div>
            <div class="text-caption text-medium-emphasis">{{ t('channelEditor.compat.stripBillingHeader.hint') }}</div>
          </div>
        </div>
        <v-switch :model-value="form.stripBillingHeader" inset color="warning" hide-details @update:model-value="updateField('stripBillingHeader', $event)" />
      </div>

      <!-- Reasoning Param Style -->
      <div v-if="supportsOpenAIAdvancedOptions" class="d-flex align-center justify-space-between">
        <div class="d-flex align-center ga-2 flex-1">
          <v-icon color="primary">mdi-tune</v-icon>
          <div class="flex-1">
            <div class="section-title section-title--soft">{{ t('channelEditor.compat.reasoningStyle.label') }}</div>
            <div class="text-caption text-medium-emphasis">{{ t('channelEditor.compat.reasoningStyle.hint') }}</div>
          </div>
        </div>
        <v-select
          :model-value="form.reasoningParamStyle"
          :items="reasoningParamStyleOptions"
          variant="outlined"
          density="comfortable"
          hide-details
          class="channel-config-select"
          style="max-width: 200px;"
          eager
          @update:model-value="updateField('reasoningParamStyle', $event)"
          @update:menu="$emit('menu-update', $event)"
        />
      </div>

      <!-- Fast Mode -->
      <div v-if="supportsOpenAIAdvancedOptions" class="d-flex align-center justify-space-between">
        <div class="d-flex align-center ga-2">
          <v-icon color="primary">mdi-fast-forward</v-icon>
          <div>
            <div class="section-title section-title--soft">{{ t('channelEditor.compat.fastMode.label') }}</div>
            <div class="text-caption text-medium-emphasis">{{ t('channelEditor.compat.fastMode.hint') }}</div>
          </div>
        </div>
        <v-switch :model-value="form.fastMode" inset color="primary" hide-details @update:model-value="updateField('fastMode', $event)" />
      </div>

      <!-- Text Verbosity -->
      <div v-if="supportsOpenAIAdvancedOptions" class="d-flex align-center justify-space-between">
        <div class="d-flex align-center ga-2">
          <v-icon color="primary">mdi-text</v-icon>
          <div>
            <div class="section-title section-title--soft">{{ t('addChannel.textVerbosityLabel') }}</div>
          </div>
        </div>
        <v-select
          :model-value="form.textVerbosity || ''"
          :items="textVerbosityOptions"
          variant="outlined"
          density="comfortable"
          hide-details
          clearable
          class="channel-config-select"
          eager
          @update:model-value="updateField('textVerbosity', $event || '')"
          @update:menu="$emit('menu-update', $event)"
        />
      </div>

      <!-- Inject Dummy Thought Signature (Gemini) -->
      <div v-if="(channelType === 'gemini' || channelType === 'messages') && form.serviceType === 'gemini'" class="d-flex align-center justify-space-between">
        <div class="d-flex align-center ga-2">
          <v-icon color="secondary">mdi-signature</v-icon>
          <div>
            <div class="section-title section-title--soft">{{ t('channelEditor.compat.injectDummySignature.label') }}</div>
            <div class="text-caption text-medium-emphasis">{{ t('channelEditor.compat.injectDummySignature.hint') }}</div>
          </div>
        </div>
        <v-switch :model-value="form.injectDummyThoughtSignature" inset color="secondary" hide-details @update:model-value="updateField('injectDummyThoughtSignature', $event)" />
      </div>

      <!-- Strip Thought Signature (Gemini) -->
      <div v-if="form.serviceType === 'gemini' && (channelType === 'gemini' || channelType === 'messages' || channelType === 'chat' || channelType === 'responses')" class="d-flex align-center justify-space-between">
        <div class="d-flex align-center ga-2">
          <v-icon color="error">mdi-close-circle</v-icon>
          <div>
            <div class="section-title section-title--soft">{{ t('channelEditor.compat.stripThoughtSignature.label') }}</div>
            <div class="text-caption text-medium-emphasis">{{ t('channelEditor.compat.stripThoughtSignature.hint') }}</div>
          </div>
        </div>
        <v-switch :model-value="form.stripThoughtSignature" inset color="error" hide-details @update:model-value="updateField('stripThoughtSignature', $event)" />
      </div>

      <!-- Historical Image Turn Limit -->
      <div v-if="channelType !== 'images' && channelType !== 'vectors'" class="d-flex align-center justify-space-between">
        <div class="d-flex align-center ga-2">
          <v-icon color="primary">mdi-image-multiple</v-icon>
          <div>
            <div class="section-title section-title--soft">{{ t('channelEditor.compat.historicalImageLimit.label') }}</div>
            <div class="text-caption text-medium-emphasis">{{ t('channelEditor.compat.historicalImageLimit.hint') }}</div>
          </div>
        </div>
        <v-text-field
          :model-value="form.historicalImageTurnLimit"
          type="number"
          min="0"
          max="10"
          variant="outlined"
          density="comfortable"
          hide-details
          class="historical-image-limit-field"
          style="max-width: 120px;"
          @update:model-value="updateField('historicalImageTurnLimit', Number($event))"
        />
      </div>
    </div>
  </v-card>
</template>

<script setup lang="ts">
import { useI18n } from '../../i18n'

interface FormData {
  serviceType: string
  codexToolCompat?: boolean
  convertImageUrlToB64Json?: boolean
  normalizeSystemRoleToTopLevel?: boolean
  normalizeMetadataUserId?: boolean
  stripBillingHeader?: boolean
  reasoningParamStyle?: string
  textVerbosity?: string
  fastMode?: boolean
  injectDummyThoughtSignature?: boolean
  stripThoughtSignature?: boolean
  historicalImageTurnLimit?: number
}

interface Props {
  form: FormData
  channelType: string
  supportsOpenAIAdvancedOptions: boolean
  reasoningParamStyleOptions: Array<{ title: string; value: string }>
  textVerbosityOptions: Array<{ title: string; value: string }>
  diagnosing?: boolean
  diagnoseResult?: { type: 'success' | 'error'; message: string; appliedCount: number } | null
}

defineProps<Props>()

const emit = defineEmits<{
  'update:field': [field: keyof FormData, value: unknown]
  'menu-update': [open: boolean]
  'diagnose': []
}>()

const { t } = useI18n()

const updateField = (field: keyof FormData, value: unknown) => {
  emit('update:field', field, value)
}
</script>

<style scoped>
.section-title--soft {
  font-weight: 500;
  font-size: 0.875rem;
}

.channel-config-select {
  flex: 0 0 200px;
  max-width: 200px;
  min-width: 160px;
}

.compat-options {
  min-width: 0;
}

.compat-options > div {
  align-items: flex-start !important;
  gap: 16px;
  min-width: 0;
}

.compat-options > div > .d-flex:first-child {
  flex: 1 1 auto;
  min-width: 0;
}

.compat-options > div > .d-flex:first-child > .v-icon {
  flex: 0 0 auto;
  margin-top: 2px;
}

.compat-options > div > .d-flex:first-child > div {
  min-width: 0;
}

.compat-options .section-title,
.compat-options :deep(.text-caption) {
  white-space: normal;
  overflow-wrap: anywhere;
}

.compat-options :deep(.v-switch) {
  flex: 0 0 72px;
  width: 72px;
  margin-inline-start: 12px;
}

.compat-options :deep(.v-selection-control) {
  justify-content: flex-end;
}

.historical-image-limit-field {
  flex: 0 0 120px;
  max-width: 120px;
  min-width: 120px;
}

@media (max-width: 600px) {
  .compat-options > div {
    flex-direction: column;
    align-items: stretch !important;
  }

  .compat-options :deep(.v-switch) {
    align-self: flex-end;
  }

  .channel-config-select,
  .historical-image-limit-field {
    flex-basis: auto;
    width: 100%;
    max-width: 100% !important;
  }
}
</style>
