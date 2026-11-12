<script setup lang="ts">
import { Button } from '@/components/ui/button'
import { Eye, EyeOff } from 'lucide-vue-next'
import { useLanguage } from '@/composables/useLanguage'

defineProps<{
  channelType: string
  isEditMode: boolean
  noVision: boolean
  saving: boolean
}>()

const emit = defineEmits<{
  (e: 'toggle-no-vision'): void
}>()

const { t } = useLanguage()
</script>

<template>
  <div class="flex shrink-0 items-start justify-between gap-3 border-b border-border/60 bg-card/50 p-5 backdrop-blur-sm">
    <div class="min-w-0 space-y-1">
      <div class="text-[10px] font-bold uppercase tracking-[0.2em] text-primary/80">
        {{ channelType }} CHANNEL
      </div>
      <h3 class="text-xl font-bold tracking-tight">
        {{ isEditMode
          ? t('channelEditor.title.edit')
          : t('channelEditor.title.create')
        }}
      </h3>
    </div>

    <!-- 编辑模式：显示操作按钮 -->
    <div v-if="isEditMode" class="flex shrink-0 items-center gap-1.5">
      <Button
        v-if="channelType !== 'images' && channelType !== 'vectors'"
        variant="ghost"
        size="icon-sm"
        class="h-8 w-8 rounded-full text-muted-foreground transition-all hover:bg-primary/10 hover:text-primary"
        :title="noVision ? t('channelEditor.compat.visionDisabled') : t('channelEditor.compat.visionEnabled')"
        @click="emit('toggle-no-vision')"
      >
        <EyeOff v-if="noVision" class="h-3.5 w-3.5 text-amber-500" />
        <Eye v-else class="h-3.5 w-3.5" />
      </Button>
    </div>
  </div>
</template>
