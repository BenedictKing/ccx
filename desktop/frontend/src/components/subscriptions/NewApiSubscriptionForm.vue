<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { CheckCircle2, Loader2 } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Textarea } from '@/components/ui/textarea'
import { useAdminApi } from '@/composables/useAdminApi'
import { useLanguage } from '@/composables/useLanguage'
import {
  NEWAPI_PROVISION_PATH,
  NEWAPI_VERIFY_PATH,
} from '@/services/admin-api'
import { stripDashboardPathFromBaseUrl } from '@/utils/base-url-semantics'
import type {
  NewApiProvisionRequest,
  NewApiProvisionResponse,
  NewApiVerifyRequest,
  NewApiVerifyResponse,
} from '@/services/admin-api'

const { t } = useLanguage()
const adminApi = useAdminApi()

const emit = defineEmits<{
  created: [result: NewApiProvisionResponse]
  error: [message: string]
}>()

const verifying = ref(false)
const provisioning = ref(false)
const verified = ref(false)
const verifyResult = ref<NewApiVerifyResponse | null>(null)

const verifyForm = ref<NewApiVerifyRequest>({
  baseUrl: '',
  accessToken: '',
  userId: '',
  authTokenMode: 'bearer',
  displayName: '',
})

const provisionForm = ref<NewApiProvisionRequest>({
  subscriptionUid: '',
  displayName: '',
  baseUrl: '',
  accessToken: '',
  channelKind: 'messages',
  userId: '',
  authTokenMode: 'bearer',
  channelName: '',
  provisionGroup: '',
  notes: '',
})

const authTokenModeOptions = [
  { label: 'Bearer', value: 'bearer' },
  { label: 'Raw', value: 'raw' },
]

const channelKindOptions = [
  { label: 'messages', value: 'messages' },
  { label: 'chat', value: 'chat' },
  { label: 'responses', value: 'responses' },
  { label: 'gemini', value: 'gemini' },
]

const groupItems = computed(() => {
  if (!verifyResult.value) return []
  return Object.entries(verifyResult.value.groups || {}).map(([name, ratio]) => ({ name, ratio }))
})

const canVerify = computed(() => !!verifyForm.value.baseUrl.trim() && !!verifyForm.value.accessToken.trim())
const canProvision = computed(
  () => !!provisionForm.value.subscriptionUid.trim() && !!provisionForm.value.channelKind,
)

function slugifyDisplayName(name: string): string {
  const base = name
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9一-龥]+/g, '-')
    .replace(/^-+|-+$/g, '')
  return base || 'newapi'
}

watch(
  () => verifyForm.value.displayName,
  (name) => {
    if (!verified.value && name) {
      provisionForm.value.subscriptionUid = `newapi-${slugifyDisplayName(name)}`
    }
  },
)

function normalizeVerifyBaseUrl() {
  verifyForm.value.baseUrl = stripDashboardPathFromBaseUrl(verifyForm.value.baseUrl)
}

async function handleVerify() {
  if (!canVerify.value) return
  normalizeVerifyBaseUrl()
  verifying.value = true
  try {
    const result = await adminApi.post<NewApiVerifyResponse>(NEWAPI_VERIFY_PATH, {
      baseUrl: verifyForm.value.baseUrl.trim(),
      accessToken: verifyForm.value.accessToken,
      userId: verifyForm.value.userId || undefined,
      authTokenMode: verifyForm.value.authTokenMode || undefined,
      displayName: verifyForm.value.displayName || undefined,
    })
    verifyResult.value = result
    verified.value = true

    // 预填第 2 步表单
    provisionForm.value.baseUrl = verifyForm.value.baseUrl.trim()
    provisionForm.value.accessToken = verifyForm.value.accessToken
    provisionForm.value.userId = verifyForm.value.userId || undefined
    provisionForm.value.authTokenMode = verifyForm.value.authTokenMode || undefined
    provisionForm.value.displayName = verifyForm.value.displayName || result.username
    if (!provisionForm.value.subscriptionUid.trim()) {
      provisionForm.value.subscriptionUid = `newapi-${slugifyDisplayName(
        verifyForm.value.displayName || result.username,
      )}`
    }
  } catch (e) {
    const message = e instanceof Error ? e.message : 'Unknown error'
    emit('error', message)
  } finally {
    verifying.value = false
  }
}

function resetVerification() {
  verified.value = false
  verifyResult.value = null
}

async function handleProvision() {
  if (!canProvision.value) return
  provisioning.value = true
  try {
    const result = await adminApi.post<NewApiProvisionResponse>(NEWAPI_PROVISION_PATH, {
      subscriptionUid: provisionForm.value.subscriptionUid.trim(),
      displayName: provisionForm.value.displayName || provisionForm.value.subscriptionUid,
      baseUrl: provisionForm.value.baseUrl,
      accessToken: provisionForm.value.accessToken,
      channelKind: provisionForm.value.channelKind,
      userId: provisionForm.value.userId || undefined,
      authTokenMode: provisionForm.value.authTokenMode || undefined,
      channelName: provisionForm.value.channelName || undefined,
      provisionGroup: provisionForm.value.provisionGroup || undefined,
      notes: provisionForm.value.notes || undefined,
    })
    emit('created', result)
  } catch (e) {
    const message = e instanceof Error ? e.message : 'Unknown error'
    emit('error', message)
  } finally {
    provisioning.value = false
  }
}
</script>

<template>
  <div class="flex flex-col gap-4">
    <!-- Step 1: 验证 -->
    <form class="flex flex-col gap-3" @submit.prevent="handleVerify">
      <div class="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
        {{ t('subscription.newApi.step1Title') }}
      </div>

      <div class="space-y-1.5">
        <Label class="text-xs text-muted-foreground">{{ t('subscription.newApi.baseUrl') }}</Label>
        <Input
          v-model="verifyForm.baseUrl"
          placeholder="https://your-newapi-instance.com"
          :disabled="verified"
          @blur="normalizeVerifyBaseUrl"
        />
      </div>

      <div class="space-y-1.5">
        <Label class="text-xs text-muted-foreground">{{ t('subscription.newApi.accessToken') }}</Label>
        <Input
          v-model="verifyForm.accessToken"
          type="password"
          autocomplete="off"
          :disabled="verified"
        />
      </div>

      <div class="space-y-1.5">
        <Label class="text-xs text-muted-foreground">{{ t('subscription.newApi.userId') }}</Label>
        <Input v-model="verifyForm.userId" :disabled="verified" />
      </div>

      <div class="space-y-1.5">
        <Label class="text-xs text-muted-foreground">{{ t('subscription.newApi.authTokenMode') }}</Label>
        <Select v-model="verifyForm.authTokenMode" :disabled="verified">
          <SelectTrigger class="h-9 w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem v-for="opt in authTokenModeOptions" :key="opt.value" :value="opt.value">
              {{ opt.label }}
            </SelectItem>
          </SelectContent>
        </Select>
      </div>

      <div class="space-y-1.5">
        <Label class="text-xs text-muted-foreground">{{ t('subscription.field.name') }}</Label>
        <Input v-model="verifyForm.displayName" :disabled="verified" />
      </div>

      <Button v-if="!verified" type="submit" :disabled="!canVerify || verifying" class="w-full">
        <Loader2 v-if="verifying" class="h-3.5 w-3.5 animate-spin" />
        {{ t('subscription.newApi.verify') }}
      </Button>
      <Button v-else type="button" variant="outline" class="w-full" @click="resetVerification">
        {{ t('subscription.newApi.reVerify') }}
      </Button>
    </form>

    <!-- 验证结果展示 -->
    <div v-if="verified && verifyResult" class="rounded-lg border border-border bg-card/40 p-3">
      <div class="mb-2 flex items-center gap-1.5 text-xs font-semibold text-foreground">
        <CheckCircle2 class="h-3.5 w-3.5 text-emerald-600 dark:text-emerald-400" />
        {{ t('subscription.newApi.accountPreview') }}
      </div>
      <div class="flex flex-col gap-1 text-xs text-muted-foreground">
        <div>{{ t('subscription.newApi.username') }}: {{ verifyResult.username }}</div>
        <div>{{ t('subscription.newApi.quota') }}: {{ verifyResult.quota }}</div>
        <div>{{ t('subscription.newApi.usedQuota') }}: {{ verifyResult.usedQuota }}</div>
        <div>{{ t('subscription.newApi.availableModels') }}: {{ verifyResult.availableModels.length }}</div>
        <div v-if="groupItems.length" class="flex flex-wrap items-center gap-1">
          <span>{{ t('subscription.newApi.groups') }}:</span>
          <span
            v-for="g in groupItems"
            :key="g.name"
            class="rounded-full border border-border bg-secondary/60 px-1.5 py-0.5 text-[10px] text-foreground"
          >
            {{ g.name }} × {{ g.ratio }}
          </span>
        </div>
      </div>
    </div>

    <!-- Step 2: 接入 -->
    <form v-if="verified" class="flex flex-col gap-3 border-t border-border pt-3" @submit.prevent="handleProvision">
      <div class="text-xs font-semibold uppercase tracking-wide text-muted-foreground">
        {{ t('subscription.newApi.step2Title') }}
      </div>

      <div class="space-y-1.5">
        <Label class="text-xs text-muted-foreground">{{ t('subscription.field.uid') }}</Label>
        <Input v-model="provisionForm.subscriptionUid" />
      </div>

      <div class="space-y-1.5">
        <Label class="text-xs text-muted-foreground">{{ t('subscription.newApi.channelKind') }}</Label>
        <Select v-model="provisionForm.channelKind">
          <SelectTrigger class="h-9 w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem v-for="opt in channelKindOptions" :key="opt.value" :value="opt.value">
              {{ opt.label }}
            </SelectItem>
          </SelectContent>
        </Select>
      </div>

      <div class="space-y-1.5">
        <Label class="text-xs text-muted-foreground">{{ t('subscription.newApi.channelName') }}</Label>
        <Input v-model="provisionForm.channelName" />
      </div>

      <div v-if="groupItems.length" class="space-y-1.5">
        <Label class="text-xs text-muted-foreground">{{ t('subscription.newApi.provisionGroup') }}</Label>
        <Select v-model="provisionForm.provisionGroup">
          <SelectTrigger class="h-9 w-full">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem v-for="g in groupItems" :key="g.name" :value="g.name">
              {{ g.name }} × {{ g.ratio }}
            </SelectItem>
          </SelectContent>
        </Select>
      </div>

      <div class="space-y-1.5">
        <Label class="text-xs text-muted-foreground">{{ t('subscription.field.notes') }}</Label>
        <Textarea v-model="provisionForm.notes" class="min-h-[60px]" />
      </div>

      <Button type="submit" :disabled="!canProvision || provisioning" class="w-full">
        <Loader2 v-if="provisioning" class="h-3.5 w-3.5 animate-spin" />
        {{ t('subscription.newApi.provision') }}
      </Button>
    </form>
  </div>
</template>
