<template>
  <v-card
    variant="outlined"
    class="empty-state text-center pa-12"
    rounded="lg"
  >
    <v-icon
      v-if="icon"
      size="64"
      class="mb-4"
      color="grey"
    >
      {{ icon }}
    </v-icon>
    <div class="text-h5 font-weight-bold mb-2">{{ title }}</div>
    <div class="text-body-1 text-medium-emphasis mb-6">
      {{ description }}
    </div>
    <slot name="action">
      <v-btn
        v-if="actionLabel"
        color="primary"
        variant="elevated"
        @click="emit('action')"
      >
        {{ actionLabel }}
      </v-btn>
    </slot>
  </v-card>
</template>

<script setup lang="ts">
interface Props {
  icon?: string
  title: string
  description: string
  actionLabel?: string
}

defineProps<Props>()

const emit = defineEmits<{
  action: []
}>()
</script>
