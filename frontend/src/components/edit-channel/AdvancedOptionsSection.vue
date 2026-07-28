<template>
  <div class="advanced-options-section">
    <v-row dense>
      <!-- 跳过 TLS 证书验证 -->
      <v-col v-if="!isAutoManaged" cols="12">
        <div class="d-flex align-center justify-space-between">
          <div class="d-flex align-center ga-2">
            <v-icon color="warning">mdi-shield-alert</v-icon>
            <div>
              <div class="section-title section-title--soft">{{ t('addChannel.skipTlsLabel') }}</div>
              <div class="text-caption text-medium-emphasis">{{ t('addChannel.skipTlsHint') }}</div>
            </div>
          </div>
          <v-switch :model-value="form.insecureSkipVerify" inset color="warning" hide-details @update:model-value="updateField('insecureSkipVerify', $event)" />
        </div>
      </v-col>

      <!-- 低质量渠道标记 -->
      <v-col cols="12">
        <div class="d-flex align-center justify-space-between">
          <div class="d-flex align-center ga-2">
            <v-icon color="info">mdi-speedometer-slow</v-icon>
            <div>
              <div class="section-title section-title--soft">{{ t('addChannel.lowQualityLabel') }}</div>
              <div class="text-caption text-medium-emphasis">{{ t('addChannel.lowQualityHint') }}</div>
            </div>
          </div>
          <v-switch :model-value="form.lowQuality" inset color="info" hide-details @update:model-value="updateField('lowQuality', $event)" />
        </div>
      </v-col>

      <!-- 认证头覆盖 -->
      <v-col v-if="!isAutoManaged" cols="12">
        <v-row dense align="center">
          <v-col cols="12" md="7">
            <div class="d-flex align-center ga-2">
              <v-icon color="primary">mdi-key</v-icon>
              <div>
                <div class="section-title section-title--soft">{{ t('channelEditor.advanced.authHeader.label') }}</div>
                <div class="text-caption text-medium-emphasis">{{ t('channelEditor.advanced.authHeader.hint') }}</div>
              </div>
            </div>
          </v-col>
          <v-col cols="12" md="5" class="auth-header-control">
            <v-select
              :model-value="form.authHeader || 'auto'"
              :items="authHeaderOptions"
              variant="outlined"
              class="auth-header-select"
              density="compact"
              hide-details
              eager
              @update:model-value="updateField('authHeader', $event)"
              @update:menu="$emit('menu-update', $event)"
            />
          </v-col>
        </v-row>
      </v-col>

      <!-- Runtime 运行期策略 -->
      <v-col cols="12">
        <RuntimeSwitchGroup :form="form" @update:field="updateField" />
      </v-col>

      <!-- Compatibility 协议规范化 -->
      <v-col v-if="!isAutoManaged" cols="12">
        <CompatibilitySwitchGroup
          v-if="channelType !== 'vectors'"
          :form="form"
          :channel-type="channelType"
          :supports-open-a-i-advanced-options="supportsOpenAIAdvancedOptions"
          :reasoning-param-style-options="reasoningParamStyleOptions"
          :text-verbosity-options="textVerbosityOptions"
          :diagnosing="diagnosing"
          :diagnose-result="diagnoseResult"
          @update:field="updateField"
          @menu-update="$emit('menu-update', $event)"
          @diagnose="$emit('diagnose')"
        />
      </v-col>

    </v-row>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from '../../i18n'
import CompatibilitySwitchGroup from './CompatibilitySwitchGroup.vue'
import RuntimeSwitchGroup from './RuntimeSwitchGroup.vue'

interface FormData {
  insecureSkipVerify: boolean
  lowQuality: boolean
  autoBlacklistBalance: boolean
  codexToolCompat?: boolean
  convertImageUrlToB64Json?: boolean
  normalizeMetadataUserId?: boolean
  stripBillingHeader?: boolean
  reasoningParamStyle?: string
  textVerbosity?: string
  fastMode?: boolean
  injectDummyThoughtSignature?: boolean
  stripThoughtSignature?: boolean
  normalizeSystemRoleToTopLevel?: boolean
  historicalImageTurnLimit?: number
  authHeader?: 'auto' | 'bearer' | 'x-api-key' | ''
  rateLimitAutoFromHeaders: boolean
  serviceType: string
}

interface Props {
  form: FormData
  channelType: string
  supportsOpenAIAdvancedOptions: boolean
  reasoningParamStyleOptions: Array<{ title: string; value: string }>
  textVerbosityOptions: Array<{ title: string; value: string }>
  isAutoManaged?: boolean
  diagnosing?: boolean
  diagnoseResult?: { type: 'success' | 'error'; message: string; appliedCount: number } | null
}

defineProps<Props>()

const emit = defineEmits<{
  'update:form': [Partial<FormData>]
  'menu-update': [boolean]
  'diagnose': []
}>()

const { t } = useI18n()

const authHeaderOptions = [
  { title: t('channelEditor.advanced.authHeader.auto'), value: 'auto' },
  { title: 'Authorization: Bearer', value: 'bearer' },
  { title: 'x-api-key', value: 'x-api-key' },
]

const updateField = (field: keyof FormData, value: unknown) => {
  emit('update:form', { [field]: value })
}
</script>

<style scoped>
.section-title--soft {
  font-weight: 500;
  font-size: 0.875rem;
}

.channel-config-select {
  max-width: 200px;
}

.auth-header-control {
  display: flex;
  justify-content: flex-end;
}

.auth-header-select {
  width: 160px;
  max-width: 160px;
}

.rate-limit-card {
  background: rgba(var(--v-theme-surface-variant), 0.3);
}

@media (max-width: 959px) {
  .auth-header-control {
    justify-content: stretch;
  }

  .auth-header-select {
    width: 100%;
    max-width: 100%;
  }
}
</style>
