<template>
  <div class="basic-info-section">
    <v-row>
      <!-- 渠道名称不再占用表单位置：由首个 BaseURL 自动派生，统一在对话框头部展示 -->

      <!-- 渠道备注：与渠道名称解耦，名称派生自 baseURL 不可改，备注允许用户手填（≤10 字符） -->
      <v-col v-if="!hideRemark" cols="12">
        <v-text-field
          :model-value="form.remark"
          :label="t('channelEditor.basic.remark.label')"
          :hint="t('channelEditor.basic.remark.hint')"
          persistent-hint
          prepend-inner-icon="mdi-text"
          variant="outlined"
          density="comfortable"
          maxlength="10"
          counter="10"
          @update:model-value="updateField('remark', $event)"
        />
      </v-col>

      <!-- 服务类型 -->
      <v-col v-if="!hideServiceType" cols="12" sm="4">
        <v-select
          :model-value="form.serviceType"
          :label="t('channelEditor.basic.serviceType.label')"
          :items="serviceTypeOptions"
          prepend-inner-icon="mdi-cog"
          variant="outlined"
          density="comfortable"
          :rules="[rules.required]"
          required
          :error-messages="errors.serviceType"
          eager
          @update:model-value="updateField('serviceType', $event)"
          @update:menu="$emit('menu-update', $event)"
        />
      </v-col>

      <!-- Base URL -->
      <v-col v-if="!hideBaseUrl && form.serviceType !== 'copilot'" cols="12">
        <v-textarea
          :model-value="baseUrlsText"
          :label="t('channelEditor.basic.baseUrl.label')"
          :placeholder="t('channelEditor.basic.baseUrl.placeholder')"
          prepend-inner-icon="mdi-web"
          variant="outlined"
          density="comfortable"
          rows="3"
          no-resize
          :rules="[rules.required, rules.baseUrls]"
          required
          :error-messages="errors.baseUrl"
          hide-details="auto"
          @update:model-value="$emit('update:baseUrlsText', $event)"
        />
        <!-- 预期请求提示 -->
        <div v-show="expectedRequestUrls.length > 0 && !baseUrlHasError" class="base-url-hint">
          <div class="text-caption text-medium-emphasis mb-1">
            {{ t('addChannel.expectedRequest') }}
          </div>
          <div class="expected-request-list">
            <div
              v-for="item in expectedRequestUrls"
              :key="`${item.protocol}:${item.expectedUrl}`"
              class="expected-request-row"
            >
              <span class="text-caption font-weight-medium">{{ expectedProtocolLabel(item.protocol) }}</span>
              <span class="text-caption text-medium-emphasis expected-request-url">{{ item.expectedUrl }}</span>
            </div>
          </div>
        </div>
      </v-col>

      <!-- 官网/控制台 -->
      <v-col v-if="!hideMetadata || managedAccount" cols="12">
        <v-text-field
          :model-value="form.website"
          :label="t('channelEditor.basic.website.label')"
          :placeholder="t('channelEditor.basic.website.placeholder')"
          prepend-inner-icon="mdi-open-in-new"
          variant="outlined"
          density="comfortable"
          type="url"
          :rules="[rules.urlOptional]"
          :error-messages="errors.website"
          @update:model-value="updateField('website', $event)"
        />
        <div v-if="managedAccount && websiteLinks?.length" class="website-links">
          <span class="text-caption text-medium-emphasis">{{ t('channelEditor.basic.website.detectedPlans') }}</span>
          <v-btn
            v-for="link in websiteLinks"
            :key="link.kind"
            :href="link.url"
            target="_blank"
            rel="noopener noreferrer"
            size="small"
            variant="tonal"
            color="primary"
          >
            <v-icon start size="small">{{ websiteLinkIcon(link.kind) }}</v-icon>
            {{ websiteLinkLabel(link.kind) }}
          </v-btn>
        </div>
      </v-col>

      <!-- 描述 -->
      <v-col v-if="!hideMetadata" cols="12">
        <v-textarea
          :model-value="form.description"
          :label="t('addChannel.descriptionLabel')"
          :hint="t('addChannel.descriptionHint')"
          persistent-hint
          prepend-inner-icon="mdi-text"
          variant="outlined"
          density="comfortable"
          rows="3"
          no-resize
          @update:model-value="updateField('description', $event)"
        />
      </v-col>

      <!-- 用户自定义标签 -->
      <v-col v-if="!hideMetadata" cols="12">
        <v-combobox
          :model-value="form.tags ?? []"
          :label="t('channelEditor.basic.tags.label')"
          :hint="t('channelEditor.basic.tags.hint')"
          persistent-hint
          prepend-inner-icon="mdi-tag"
          variant="outlined"
          density="comfortable"
          chips
          closable-chips
          multiple
          hide-selected
          @update:model-value="updateField('tags', $event)"
        >
          <template #chip="{ props: chipProps, item }">
            <v-chip v-bind="chipProps" :text="item.value" color="teal" size="small" variant="tonal" closable />
          </template>
        </v-combobox>
      </v-col>
    </v-row>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from '../../i18n'
import type { ChannelWebsiteKind, ChannelWebsiteLink } from '../../utils/channelWebsite'

type ChannelType = 'messages' | 'chat' | 'responses' | 'gemini' | 'images' | 'vectors'

interface FormData {
  name: string
  remark: string
  serviceType: string
  website: string
  description: string
  tags?: string[]
}

interface Props {
  form: FormData
  baseUrlsText: string
  expectedRequestUrls: Array<{ protocol: ChannelType; expectedUrl: string }>
  baseUrlHasError: boolean
  serviceTypeOptions: Array<{ title: string; value: string }>
  hideServiceType?: boolean
  hideBaseUrl?: boolean
  hideMetadata?: boolean
  hideRemark?: boolean
  managedAccount?: boolean
  providerName?: string
  websiteLinks?: ChannelWebsiteLink[]
  errors: Record<string, string>
  rules: {
    required: (_value: string) => boolean | string
    baseUrls: (_value: string) => boolean | string
    urlOptional: (_value: string) => boolean | string
  }
}

const props = defineProps<Props>()

const emit = defineEmits<{
  'update:form': [Partial<FormData>]
  'update:baseUrlsText': [string]
  'menu-update': [boolean]
}>()

const { t } = useI18n()

function expectedProtocolLabel(protocol: ChannelType): string {
  const labels: Record<ChannelType, string> = {
    messages: 'Messages',
    chat: 'Chat',
    responses: 'Responses',
    gemini: 'Gemini',
    images: 'Images',
    vectors: 'Vectors',
  }
  return labels[protocol]
}

const updateField = (field: keyof FormData, value: unknown) => {
  emit('update:form', { [field]: value })
}

const websiteLinkLabel = (kind: ChannelWebsiteKind): string => {
  if (kind === 'agent_plan') return t('volcengineAccessKey.agentPlanConsole')
  if (kind === 'coding_plan') return t('volcengineAccessKey.codingPlanConsole')
  if (kind === 'provider_console') {
    return t('channelEditor.basic.website.providerConsole', { provider: props.providerName || '' })
  }
  return t('channelCard.openWebsite')
}

const websiteLinkIcon = (kind: ChannelWebsiteKind): string => (
  kind === 'coding_plan' ? 'mdi-code-braces' : kind === 'agent_plan' ? 'mdi-robot-outline' : 'mdi-open-in-new'
)
</script>

<style scoped>
.base-url-hint {
  margin-top: 8px;
  padding: 8px 12px;
  background: rgba(var(--v-theme-surface-variant), 0.3);
  border-radius: 4px;
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
  min-width: 0;
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
}

.website-links {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  margin-top: -8px;
}
</style>
