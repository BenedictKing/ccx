<template>
  <!-- 编辑渠道：头部直接以身份块呈现（图标 + 标签 + 名称 + 可选徽章），取代蓝底标题栏 -->
  <v-card-title v-if="identityName" class="d-flex align-center ga-3 pa-6 border-b">
    <v-icon color="primary" size="22">{{ identityIcon }}</v-icon>
    <div class="flex-grow-1">
      <div class="text-caption text-medium-emphasis">{{ identityLabel || t('channelEditor.managed.providerLabel') }}</div>
      <div class="provider-header-name">{{ identityName }}</div>
    </div>
    <v-chip
      v-if="identityBadge"
      :color="identityBadge === 'official' ? 'success' : 'primary'"
      variant="tonal"
      size="small"
      :prepend-icon="identityBadge === 'official' ? 'mdi-check-decagram' : 'mdi-cog-sync'"
    >
      {{ t(identityBadge === 'official' ? 'channelEditor.managed.officialBadge' : 'channelEditor.managed.managedBadge') }}
    </v-chip>
  </v-card-title>
  <v-card-title v-else class="d-flex align-center ga-3 pa-6" :class="headerClasses">
    <v-avatar :color="avatarColor" variant="flat" size="40">
      <v-icon :style="headerIconStyle" size="20">{{ isEditing ? 'mdi-pencil' : 'mdi-plus' }}</v-icon>
    </v-avatar>

    <div class="flex-grow-1 modal-header-text">
      <div class="modal-title d-flex align-center ga-2 flex-wrap">
        {{ isEditing ? editTitle : createTitle }}
        <v-tooltip
          v-if="channelName"
          location="bottom"
          :text="channelNameHint || t('channelEditor.basic.name.label')"
          :open-delay="150"
          content-class="key-tooltip"
        >
          <template #activator="{ props: tip }">
            <span v-bind="tip" class="channel-name-text">{{ channelName }}</span>
          </template>
        </v-tooltip>
      </div>
      <div class="modal-subtitle" :class="subtitleClasses">
        {{ isEditing ? editSubtitle : createSubtitle }}
      </div>
    </div>

    <div v-if="isEditing && !hideCapabilityActions && channelType !== 'images' && channelType !== 'vectors'" class="header-capability-actions">
      <v-tooltip location="bottom" :text="visionTooltip" :open-delay="150" content-class="key-tooltip">
        <template #activator="{ props: tip }">
          <v-btn
            v-bind="tip"
            :color="noVision ? 'warning' : undefined"
            :variant="noVision ? 'tonal' : 'text'"
            size="small"
            icon
            rounded="lg"
            class="mr-2"
            @click="$emit('toggle-no-vision')"
          >
            <v-icon size="18">{{ noVision ? 'mdi-eye-off' : 'mdi-eye' }}</v-icon>
          </v-btn>
        </template>
      </v-tooltip>
    </div>
  </v-card-title>
</template>

<script setup lang="ts">
import { useI18n } from '../../i18n'

interface Props {
  isEditing: boolean
  hideCapabilityActions?: boolean
  channelType?: 'messages' | 'chat' | 'responses' | 'gemini' | 'images' | 'vectors'
  channelName?: string
  channelNameHint?: string
  identityName?: string
  identityLabel?: string
  identityIcon?: string
  identityBadge?: 'official' | 'managed' | ''
  noVision?: boolean
  headerClasses?: string | Record<string, boolean> | Array<string | Record<string, boolean>>
  avatarColor?: string
  headerIconStyle?: Record<string, string>
  subtitleClasses?: string | Record<string, boolean> | Array<string | Record<string, boolean>>
  editTitle?: string
  createTitle?: string
  editSubtitle?: string
  createSubtitle?: string
  visionTooltip?: string
}

withDefaults(defineProps<Props>(), {
  channelType: 'messages',
  hideCapabilityActions: false,
  channelName: '',
  channelNameHint: '',
  identityName: '',
  identityLabel: '',
  identityIcon: 'mdi-domain',
  identityBadge: '',
  noVision: false,
  avatarColor: 'primary',
})

defineEmits<{
  'toggle-no-vision': []
}>()

const { t } = useI18n()
</script>

<style scoped>
.modal-header-text {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.modal-title {
  font-size: 1.125rem;
  line-height: 1.3;
  font-weight: 600;
  letter-spacing: 0;
}

.modal-subtitle {
  font-size: 0.8125rem;
  line-height: 1.5;
}

.channel-name-text {
  max-width: 320px;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 0.875rem;
  font-weight: 400;
  /* 颜色继承头部（浅色主题 bg-primary 上为 text-white，深色主题 bg-surface 上为
     text-high-emphasis），仅降低不透明度作次级展示，两种主题下均可读 */
  opacity: 0.85;
}

/* 头部身份块名称（编辑态取代蓝底标题栏） */
.provider-header-name {
  font-size: 1rem;
  font-weight: 700;
}

.header-capability-actions {
  display: flex;
  align-items: center;
  gap: 12px;
  flex-wrap: wrap;
}

.text-white-subtitle {
  color: rgba(255, 255, 255, 0.78) !important;
}

.animate-pulse {
  animation: pulse 1.5s ease-in-out infinite;
}

@keyframes pulse {
  0%,
  100% {
    opacity: 1;
  }
  50% {
    opacity: 0.7;
  }
}
</style>
