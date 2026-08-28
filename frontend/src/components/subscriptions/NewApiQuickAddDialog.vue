<template>
  <v-dialog v-model="dialogVisible" max-width="680" persistent>
    <v-card class="pa-4">
      <v-card-title class="text-h6 mb-4 d-flex align-center">
        <v-icon color="warning" class="mr-2">mdi-server-network</v-icon>
        {{ t('subscription.newApi.connect') }}
      </v-card-title>
      <v-card-text>
        <NewApiSubscriptionForm ref="subscriptionFormRef" @created="handleCreated" @error="emit('error', $event)" />
      </v-card-text>
      <v-card-actions>
        <v-spacer />
        <v-btn variant="text" @click="closeDialog">{{ t('app.actions.cancel') }}</v-btn>
      </v-card-actions>
    </v-card>
  </v-dialog>
</template>

<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from '@/i18n'
import { useDialogHotkeys } from '@/composables/useDialogHotkeys'
import NewApiSubscriptionForm from '@/components/NewApiSubscriptionForm.vue'
import type { NewApiProvisionResponse } from '@/services/api-types'

const { t } = useI18n()
const emit = defineEmits<{
  created: [result: NewApiProvisionResponse]
  error: [message: string]
}>()

const dialogVisible = ref(false)
const subscriptionFormRef = ref<InstanceType<typeof NewApiSubscriptionForm> | null>(null)

// persistent 对话框默认快捷键：Esc 取消关闭；⌘/Ctrl+Enter 触发表单当前步骤（验证/接入）
useDialogHotkeys(dialogVisible, {
  esc: () => closeDialog(),
  confirm: () => subscriptionFormRef.value?.requestPrimaryAction(),
})

function openDialog() {
  dialogVisible.value = true
}

function closeDialog() {
  dialogVisible.value = false
}

function handleCreated(result: NewApiProvisionResponse) {
  emit('created', result)
  closeDialog()
}

defineExpose({ openDialog })
</script>
