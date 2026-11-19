<template>
  <ConversationDashboard @success="(msg: string) => emit('success', msg)" @error="(msg: string) => emit('error', msg)" />
</template>

<script setup lang="ts">
import { onMounted } from 'vue'
import { useRouter } from 'vue-router'
import ConversationDashboard from '@/components/ConversationDashboard.vue'
import { api } from '@/services/api'

const emit = defineEmits<{
  success: [message: string]
  error: [message: string]
}>()
const router = useRouter()
onMounted(async () => {
  const kinds = ['messages', 'chat', 'responses', 'gemini'] as const
  const states = await Promise.all(kinds.map(async kind => {
    try {
      return (await api.getSubagentRouting(kind)).activated
    } catch {
      return false
    }
  }))
  if (states.every(Boolean)) {
    await router.replace('/channels/responses?tab=subagent')
  }
})
</script>
