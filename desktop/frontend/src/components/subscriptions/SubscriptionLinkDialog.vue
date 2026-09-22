<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { AlertCircle, Link2Off, Loader2 } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from '@/components/ui/dialog'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { useAdminApi } from '@/composables/useAdminApi'
import { useLanguage } from '@/composables/useLanguage'
import { subscriptionLinkPath, subscriptionUnlinkPath } from '@/services/admin-api'
import type { Channel, ChannelsResponse, SubscriptionItem } from '@/services/admin-api'

const props = defineProps<{
  open: boolean
  subscription: SubscriptionItem | null
}>()
const emit = defineEmits<{ 'update:open': [value: boolean]; updated: [] }>()
const { t } = useLanguage()
const adminApi = useAdminApi()

const channelsLoading = ref(false)
const submitting = ref(false)
const unlinkLoading = ref('')
const error = ref('')
const selectedChannelUid = ref('')
const linkableChannels = ref<{ channelUid: string; label: string }[]>([])

const linkedChannelUids = computed(() => props.subscription?.linkedChannelUids || [])

// 绑定对话框打开时按需拉取全量渠道（/api/messages/channels 返回所有协议渠道），
// 过滤出有 channelUid 且未绑定的渠道作为候选。
async function loadLinkableChannels() {
  if (!props.subscription) return
  channelsLoading.value = true
  linkableChannels.value = []
  try {
    const response = await adminApi.get<ChannelsResponse>('/api/messages/channels')
    const linked = new Set(props.subscription.linkedChannelUids || [])
    linkableChannels.value = response.channels
      .filter((channel: Channel) => !!channel.channelUid && !linked.has(channel.channelUid))
      .map(channel => ({
        channelUid: channel.channelUid as string,
        label: `${channel.name} (${channel.channelUid})`,
      }))
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : String(cause)
  } finally {
    channelsLoading.value = false
  }
}

watch(() => props.open, (open) => {
  if (open) {
    selectedChannelUid.value = ''
    error.value = ''
    void loadLinkableChannels()
  }
})

async function linkChannel() {
  if (!props.subscription || !selectedChannelUid.value || submitting.value) return
  submitting.value = true
  error.value = ''
  try {
    await adminApi.post(subscriptionLinkPath(props.subscription.subscriptionUid), { channelUid: selectedChannelUid.value })
    emit('updated')
    emit('update:open', false)
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : String(cause)
  } finally {
    submitting.value = false
  }
}

async function unlinkChannel(channelUid: string) {
  if (!props.subscription || unlinkLoading.value) return
  unlinkLoading.value = channelUid
  error.value = ''
  try {
    await adminApi.post(subscriptionUnlinkPath(props.subscription.subscriptionUid), { channelUid })
    emit('updated')
  } catch (cause) {
    error.value = cause instanceof Error ? cause.message : String(cause)
  } finally {
    unlinkLoading.value = ''
  }
}
</script>

<template>
  <Dialog :open="open" @update:open="emit('update:open', $event)">
    <DialogContent class="sm:max-w-[520px]">
      <DialogHeader>
        <DialogTitle>{{ t('subscription.linkChannel.title') }}</DialogTitle>
        <DialogDescription>
          {{ t('subscription.linkChannel.description', { name: subscription?.displayName || '' }) }}
        </DialogDescription>
      </DialogHeader>

      <div class="space-y-4">
        <div v-if="channelsLoading" class="flex items-center gap-2 text-xs text-muted-foreground">
          <Loader2 class="h-3.5 w-3.5 animate-spin" />{{ t('subscription.checkingChannel') }}
        </div>
        <div v-else-if="linkableChannels.length === 0" class="text-sm text-muted-foreground">
          {{ t('subscription.linkChannel.empty') }}
        </div>
        <div v-else class="space-y-1.5">
          <Label class="text-xs text-muted-foreground">{{ t('subscription.linkChannel.placeholder') }}</Label>
          <Select v-model="selectedChannelUid" :disabled="channelsLoading || submitting">
            <SelectTrigger class="h-9 w-full"><SelectValue :placeholder="t('subscription.linkChannel.placeholder')" /></SelectTrigger>
            <SelectContent>
              <SelectItem v-for="channel in linkableChannels" :key="channel.channelUid" :value="channel.channelUid">
                {{ channel.label }}
              </SelectItem>
            </SelectContent>
          </Select>
        </div>

        <p v-if="error" class="flex items-start gap-2 text-xs text-destructive">
          <AlertCircle class="mt-0.5 h-3.5 w-3.5 shrink-0" />{{ error }}
        </p>

        <div v-if="linkedChannelUids.length" class="space-y-2 border-t border-border pt-3">
          <p class="text-xs font-semibold text-muted-foreground">{{ t('subscription.field.linkedChannels') }}</p>
          <div v-for="channelUid in linkedChannelUids" :key="channelUid" class="flex items-center justify-between gap-2">
            <code class="truncate text-xs">{{ channelUid }}</code>
            <Button size="sm" variant="ghost" class="h-7 text-xs text-destructive" :disabled="unlinkLoading === channelUid" @click="unlinkChannel(channelUid)">
              <Loader2 v-if="unlinkLoading === channelUid" class="h-3.5 w-3.5 animate-spin" />
              <Link2Off v-else class="h-3.5 w-3.5" />
              {{ t('subscription.unlinkChannel') }}
            </Button>
          </div>
        </div>
      </div>

      <DialogFooter>
        <Button variant="ghost" :disabled="submitting" @click="emit('update:open', false)">{{ t('common.cancel') }}</Button>
        <Button :disabled="!selectedChannelUid || submitting" @click="linkChannel">
          <Loader2 v-if="submitting" class="mr-1.5 h-3.5 w-3.5 animate-spin" />
          {{ t('subscription.linkChannel') }}
        </Button>
      </DialogFooter>
    </DialogContent>
  </Dialog>
</template>
