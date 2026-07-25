<template>
  <v-card-title class="d-flex align-center ga-3 pa-6" :class="headerClasses">
    <v-avatar :color="avatarColor" variant="flat" size="40">
      <v-icon :style="headerIconStyle" size="20">{{ isEditing ? 'mdi-pencil' : 'mdi-plus' }}</v-icon>
    </v-avatar>

    <div class="flex-grow-1 modal-header-text">
      <div class="modal-title">
        {{ isEditing ? editTitle : createTitle }}
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

interface Props {
  isEditing: boolean
  hideCapabilityActions?: boolean
  channelType?: 'messages' | 'chat' | 'responses' | 'gemini' | 'images' | 'vectors'
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
  noVision: false,
  avatarColor: 'primary',
})

defineEmits<{
  'toggle-no-vision': []
}>()
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
