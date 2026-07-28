<script setup lang="ts">
import { Button } from '@/components/ui/button'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Check, CheckCircle, KeyRound, Loader2, ShieldCheck, Stethoscope, XCircle, Zap } from 'lucide-vue-next'
import { useLanguage } from '@/composables/useLanguage'

interface FormData {
  fastMode: boolean
  modelCapabilitiesText: string
  historicalImageTurnLimit: number
  insecureSkipVerify: boolean
  lowQuality: boolean
  injectDummyThoughtSignature: boolean
  stripThoughtSignature: boolean
  normalizeSystemRoleToTopLevel: boolean
  normalizeMetadataUserId: boolean
  stripBillingHeader: boolean
  autoBlacklistBalance: boolean
  codexToolCompat: boolean
  reasoningParamStyle: string
  textVerbosity: string
  serviceType: string
  authHeader: 'auto' | 'bearer' | 'x-api-key' | ''
  rateLimitAutoFromHeaders: boolean
}

defineProps<{
  form: FormData
  channelType: string
  supportsOpenAIAdvancedOptions: boolean
  reasoningParamStyleOptions: Array<{ label: string; value: string }>
  textVerbosityOptions: Array<{ label: string; value: string }>
  diagnosing?: boolean
  diagnoseResult?: { type: 'success' | 'error'; message: string; appliedCount: number } | null
}>()

const emit = defineEmits<{
  'update:form': [value: Partial<FormData>]
  'diagnose': []
}>()

const { t } = useLanguage()
const TEXT_VERBOSITY_DEFAULT_VALUE = 'default'
const authHeaderOptions = [
  { label: t('channelEditor.advanced.authHeader.auto'), value: 'auto' },
  { label: 'Authorization: Bearer', value: 'bearer' },
  { label: 'x-api-key', value: 'x-api-key' },
]

function updateField<K extends keyof FormData>(key: K, value: FormData[K]) {
  emit('update:form', { [key]: value } as Partial<FormData>)
}

function updateTextVerbosity(value: string) {
  updateField(
    'textVerbosity',
    (value === TEXT_VERBOSITY_DEFAULT_VALUE ? '' : value) as FormData['textVerbosity'],
  )
}
</script>

<template>
  <section class="space-y-6 rounded-xl border border-border/60 bg-gradient-to-br from-card/60 to-card/40 p-5 shadow-sm backdrop-blur-sm">
    <h4 class="flex items-center gap-2 text-xs font-bold uppercase tracking-wider text-primary border-b border-border/40 pb-2.5">
      <span class="flex h-5 w-5 items-center justify-center rounded-md bg-primary/10 text-primary">
        <ShieldCheck class="h-3 w-3" />
      </span>
      {{ t('channelEditor.nav.advanced') }}
    </h4>

    <div class="grid gap-3">
      <div class="flex items-center justify-between gap-3 rounded-lg border border-border/60 bg-gradient-to-r from-background/60 to-background/40 p-4 shadow-sm backdrop-blur-sm transition-all hover:shadow-md">
        <div class="min-w-0 space-y-0.5">
          <Label class="text-xs font-medium">{{ t('addChannel.skipTlsLabel') }}</Label>
          <p class="text-[10px] leading-4 text-muted-foreground">{{ t('addChannel.skipTlsHint') }}</p>
        </div>
        <Switch :model-value="form.insecureSkipVerify" @update:model-value="updateField('insecureSkipVerify', $event)" />
      </div>
      <div class="flex items-center justify-between gap-3 rounded-lg border border-border/60 bg-gradient-to-r from-background/60 to-background/40 p-4 shadow-sm backdrop-blur-sm transition-all hover:shadow-md">
        <div class="min-w-0 space-y-0.5">
          <Label class="text-xs font-medium">{{ t('addChannel.lowQualityLabel') }}</Label>
          <p class="text-[10px] leading-4 text-muted-foreground">{{ t('addChannel.lowQualityHint') }}</p>
        </div>
        <Switch :model-value="form.lowQuality" @update:model-value="updateField('lowQuality', $event)" />
      </div>
      <div class="grid gap-3 rounded-lg border border-border/60 bg-gradient-to-r from-background/60 to-background/40 p-4 shadow-sm backdrop-blur-sm md:grid-cols-[minmax(0,1fr)_160px]">
        <div class="min-w-0 space-y-0.5">
          <Label class="flex items-center gap-1.5 text-xs font-medium">
            <KeyRound class="h-3 w-3 text-primary" />
            {{ t('channelEditor.advanced.authHeader.label') }}
          </Label>
          <p class="text-[10px] leading-4 text-muted-foreground">{{ t('channelEditor.advanced.authHeader.hint') }}</p>
        </div>
        <Select
          :model-value="form.authHeader || 'auto'"
          @update:model-value="(val) => updateField('authHeader', String(val) as FormData['authHeader'])"
        >
          <SelectTrigger class="h-8 w-full text-xs">
            <SelectValue />
          </SelectTrigger>
          <SelectContent>
            <SelectItem v-for="opt in authHeaderOptions" :key="opt.value" :value="opt.value">
              {{ opt.label }}
            </SelectItem>
          </SelectContent>
        </Select>
      </div>
    </div>

    <div class="space-y-2.5">
      <!-- Runtime 运行期策略 -->
      <div class="p-4 rounded-lg border border-border/60 bg-gradient-to-br from-background/60 to-background/40 shadow-sm backdrop-blur-sm space-y-2.5">
        <div class="flex items-center gap-1.5 text-[10px] font-bold uppercase tracking-wider text-primary border-b border-border/40 pb-2">
          <Zap class="h-3 w-3" />
          {{ t('channelEditor.runtime.title') }}
        </div>
        <div class="space-y-2">
          <div class="flex items-center justify-between gap-3">
            <div class="min-w-0 space-y-0.5">
              <Label class="text-xs font-medium">{{ t('channelEditor.runtime.autoBlacklist.label') }}</Label>
              <p class="text-[10px] leading-4 text-muted-foreground">
                {{ t('channelEditor.runtime.autoBlacklist.hint') }}
              </p>
            </div>
            <Switch :model-value="form.autoBlacklistBalance" @update:model-value="updateField('autoBlacklistBalance', $event)" />
          </div>
          <div class="flex items-center justify-between gap-3">
            <div class="min-w-0 space-y-0.5">
              <Label class="text-xs font-medium">{{ t('channelEditor.runtime.autoLearnRateLimits.label') }}</Label>
              <p class="text-[10px] leading-4 text-muted-foreground">
                {{ t('channelEditor.runtime.autoLearnRateLimits.hint') }}
              </p>
            </div>
            <Switch :model-value="form.rateLimitAutoFromHeaders" @update:model-value="updateField('rateLimitAutoFromHeaders', $event)" />
          </div>
        </div>
      </div>

      <!-- 协议规范化 -->
      <div class="p-4 rounded-lg border border-border/60 bg-gradient-to-br from-background/60 to-background/40 shadow-sm backdrop-blur-sm space-y-2.5">
        <div class="flex items-center justify-between gap-2 border-b border-border/40 pb-2">
          <div class="flex items-center gap-1.5 text-[10px] font-bold uppercase tracking-wider text-primary">
            <ShieldCheck class="h-3 w-3" />
            {{ t('channelEditor.compat.title') }}
          </div>
          <Button
            v-if="channelType !== 'images' && channelType !== 'vectors' && !diagnoseResult"
            type="button"
            variant="secondary"
            size="sm"
            class="h-6 gap-1 px-2 text-[10px]"
            :disabled="diagnosing"
            @click="$emit('diagnose')"
          >
            <Loader2 v-if="diagnosing" class="h-3 w-3 animate-spin" />
            <Stethoscope v-else class="h-3 w-3" />
            {{ t('channelEditor.compat.diagnose') }}
          </Button>
          <Button
            v-else-if="channelType !== 'images' && channelType !== 'vectors' && diagnoseResult?.type === 'error'"
            type="button" variant="error" size="sm" class="h-6 gap-1 px-2 text-[10px]"
            @click="$emit('diagnose')"
          >
            <XCircle class="h-3 w-3" />
            {{ t('channelEditor.compat.diagnoseFailedShort') }}
          </Button>
          <Button
            v-else-if="channelType !== 'images' && channelType !== 'vectors' && diagnoseResult && diagnoseResult.appliedCount > 0"
            type="button" variant="success" size="sm" class="h-6 gap-1 px-2 text-[10px]"
            @click="$emit('diagnose')"
          >
            <CheckCircle class="h-3 w-3" />
            {{ t('channelEditor.compat.diagnoseAppliedShort', { count: String(diagnoseResult.appliedCount) }) }}
          </Button>
          <Button
            v-else-if="channelType !== 'images' && channelType !== 'vectors' && diagnoseResult"
            type="button" variant="secondary" size="sm" class="h-6 gap-1 px-2 text-[10px]"
            @click="$emit('diagnose')"
          >
            <Check class="h-3 w-3" />
            {{ t('channelEditor.compat.diagnoseNoChangeShort') }}
          </Button>
        </div>
        <Transition name="diagnose-fade">
          <p
            v-if="diagnoseResult"
            :class="[
              'rounded-md px-2.5 py-1.5 text-[11px] leading-4',
              diagnoseResult.type === 'error'
                ? 'bg-destructive/10 text-destructive'
                : 'bg-emerald-500/10 text-emerald-700 dark:text-emerald-300',
            ]"
          >
            {{ diagnoseResult.message }}
          </p>
        </Transition>
        <div class="compat-option-list space-y-2">
          <div v-if="channelType === 'responses'" class="flex items-center justify-between gap-3">
            <div class="min-w-0 space-y-0.5">
              <Label class="text-xs font-medium">{{ t('channelEditor.compat.codexCompat.label') }}</Label>
              <p class="text-[10px] leading-4 text-muted-foreground">{{ t('channelEditor.compat.codexCompat.hint') }}</p>
            </div>
            <Switch :model-value="form.codexToolCompat" @update:model-value="updateField('codexToolCompat', $event)" />
          </div>
          <div v-if="channelType === 'messages'" class="flex items-center justify-between gap-3">
            <div class="min-w-0 space-y-0.5">
              <Label class="text-xs font-medium">{{ t('channelEditor.compat.normalizeSystem.label') }}</Label>
              <p class="text-[10px] leading-4 text-muted-foreground">{{ t('channelEditor.compat.normalizeSystem.hint') }}</p>
            </div>
            <Switch :model-value="form.normalizeSystemRoleToTopLevel" @update:model-value="updateField('normalizeSystemRoleToTopLevel', $event)" />
          </div>
          <div v-if="channelType === 'messages'" class="flex items-center justify-between gap-3">
            <div class="min-w-0 space-y-0.5">
              <Label class="text-xs font-medium">{{ t('channelEditor.compat.normalizeUserId.label') }}</Label>
              <p class="text-[10px] leading-4 text-muted-foreground">{{ t('channelEditor.compat.normalizeUserId.hint') }}</p>
            </div>
            <Switch :model-value="form.normalizeMetadataUserId" @update:model-value="updateField('normalizeMetadataUserId', $event)" />
          </div>
          <div v-if="channelType === 'messages'" class="flex items-center justify-between gap-3">
            <div class="min-w-0 space-y-0.5">
              <Label class="text-xs font-medium">{{ t('channelEditor.compat.stripBillingHeader.label') }}</Label>
              <p class="text-[10px] leading-4 text-muted-foreground">{{ t('channelEditor.compat.stripBillingHeader.hint') }}</p>
            </div>
            <Switch :model-value="form.stripBillingHeader" @update:model-value="updateField('stripBillingHeader', $event)" />
          </div>
          <div v-if="supportsOpenAIAdvancedOptions" class="flex items-center justify-between gap-3">
            <div class="min-w-0 flex-1 space-y-0.5">
              <Label class="text-xs font-medium">{{ t('channelEditor.compat.reasoningStyle.label') }}</Label>
              <p class="text-[10px] leading-4 text-muted-foreground">{{ t('channelEditor.compat.reasoningStyle.hint') }}</p>
            </div>
            <Select
              :model-value="form.reasoningParamStyle || 'reasoning'"
              @update:model-value="(val) => updateField('reasoningParamStyle', String(val))"
            >
              <SelectTrigger class="h-8 w-[200px] text-xs">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem v-for="opt in reasoningParamStyleOptions" :key="opt.value" :value="opt.value">
                  {{ opt.label }}
                </SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div v-if="supportsOpenAIAdvancedOptions" class="flex items-center justify-between gap-3">
            <div class="min-w-0 space-y-0.5">
              <Label class="text-xs font-medium">{{ t('channelEditor.compat.fastMode.label') }}</Label>
              <p class="text-[10px] leading-4 text-muted-foreground">{{ t('channelEditor.compat.fastMode.hint') }}</p>
            </div>
            <Switch :model-value="form.fastMode" @update:model-value="updateField('fastMode', $event)" />
          </div>
          <div v-if="supportsOpenAIAdvancedOptions" class="flex items-center justify-between gap-3">
            <div class="min-w-0 space-y-0.5">
              <Label class="text-xs font-medium">{{ t('channelEditor.compat.textVerbosity.label') }}</Label>
            </div>
            <Select
              :model-value="form.textVerbosity || TEXT_VERBOSITY_DEFAULT_VALUE"
              @update:model-value="(val) => updateTextVerbosity(String(val))"
            >
              <SelectTrigger class="h-8 w-[200px] text-xs">
                <SelectValue :placeholder="t('channelEditor.compat.textVerbosity.placeholder')" />
              </SelectTrigger>
              <SelectContent>
                <SelectItem :value="TEXT_VERBOSITY_DEFAULT_VALUE">
                  {{ t('channelEditor.compat.textVerbosity.placeholder') }}
                </SelectItem>
                <SelectItem v-for="opt in textVerbosityOptions" :key="opt.value" :value="opt.value">
                  {{ opt.label }}
                </SelectItem>
              </SelectContent>
            </Select>
          </div>
          <div v-if="(channelType === 'gemini' || channelType === 'messages') && form.serviceType === 'gemini'" class="flex items-center justify-between gap-3">
            <div class="min-w-0 space-y-0.5">
              <Label class="text-xs font-medium">{{ t('channelEditor.compat.injectDummySignature.label') }}</Label>
              <p class="text-[10px] leading-4 text-muted-foreground">{{ t('channelEditor.compat.injectDummySignature.hint') }}</p>
            </div>
            <Switch :model-value="form.injectDummyThoughtSignature" @update:model-value="updateField('injectDummyThoughtSignature', $event)" />
          </div>
          <div v-if="form.serviceType === 'gemini' && (channelType === 'gemini' || channelType === 'messages' || channelType === 'chat' || channelType === 'responses')" class="flex items-center justify-between gap-3">
            <div class="min-w-0 space-y-0.5">
              <Label class="text-xs font-medium">{{ t('channelEditor.compat.stripThoughtSignature.label') }}</Label>
              <p class="text-[10px] leading-4 text-muted-foreground">{{ t('channelEditor.compat.stripThoughtSignature.hint') }}</p>
            </div>
            <Switch :model-value="form.stripThoughtSignature" @update:model-value="updateField('stripThoughtSignature', $event)" />
          </div>
          <div v-if="channelType !== 'images' && channelType !== 'vectors'" class="flex items-center justify-between gap-3">
            <div class="min-w-0 flex-1 space-y-0.5">
              <Label class="text-xs font-medium">{{ t('channelEditor.compat.historicalImageLimit.label') }}</Label>
              <p class="text-[10px] leading-4 text-muted-foreground">{{ t('channelEditor.compat.historicalImageLimit.hint') }}</p>
            </div>
            <Input
              :model-value="form.historicalImageTurnLimit"
              type="number"
              min="0"
              max="10"
              class="historical-image-limit-field h-8 w-[120px] text-xs"
              @update:model-value="updateField('historicalImageTurnLimit', Number($event))"
            />
          </div>
        </div>
      </div>
    </div>
  </section>
</template>

<style scoped>
.compat-option-list > div {
  align-items: flex-start;
  min-width: 0;
}

.compat-option-list > div > div:first-child {
  flex: 1 1 auto;
  min-width: 0;
  overflow-wrap: anywhere;
}

.compat-option-list :deep([data-slot='switch']) {
  flex: 0 0 2rem;
  margin-top: 0.125rem;
}

.compat-option-list :deep([data-slot='select-trigger']) {
  flex: 0 0 200px;
  max-width: 200px;
  min-width: 160px;
}

.historical-image-limit-field {
  flex: 0 0 120px;
  max-width: 120px;
  min-width: 120px;
}

@media (max-width: 640px) {
  .compat-option-list > div {
    flex-direction: column;
    align-items: stretch;
  }

  .compat-option-list :deep([data-slot='switch']) {
    align-self: flex-end;
  }

  .compat-option-list :deep([data-slot='select-trigger']),
  .historical-image-limit-field {
    flex-basis: auto;
    width: 100%;
    max-width: 100%;
  }
}

.diagnose-fade-enter-active { transition: opacity 0.2s ease-out, transform 0.2s ease-out; }
.diagnose-fade-leave-active { transition: opacity 0.3s ease-in, transform 0.3s ease-in; }
.diagnose-fade-enter-from { opacity: 0; transform: translateY(-4px); }
.diagnose-fade-leave-to { opacity: 0; transform: translateY(-4px); }
</style>
