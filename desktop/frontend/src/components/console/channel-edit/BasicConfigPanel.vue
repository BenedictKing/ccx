<script setup lang="ts">
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Badge } from '@/components/ui/badge'
import { Building, BadgeCheck, Cog } from 'lucide-vue-next'
import { useLanguage } from '@/composables/useLanguage'

interface FormData {
  name: string
  remark: string
  serviceType: 'openai' | 'claude' | 'gemini' | 'responses' | 'copilot' | ''
  baseUrl: string
  baseUrlsText: string
  website: string
  description: string
}

interface Errors {
  name?: string
  serviceType?: string
  baseUrl?: string
}

const props = defineProps<{
  form: FormData
  errors: Errors
  serviceTypeOptions: Array<{ label: string; value: string }>
  expectedRequestUrls: any[]
  managedAccount?: boolean
  providerName?: string
  officialProvider?: boolean
}>()

const emit = defineEmits<{
  'update:form': [value: Partial<FormData>]
}>()

const { t } = useLanguage()

function updateField<K extends keyof FormData>(key: K, value: FormData[K]) {
  emit('update:form', { [key]: value } as Partial<FormData>)
}
</script>

<template>
  <section class="space-y-4 rounded-xl border border-border/60 bg-card/40 p-5 shadow-xs">
    <h4 class="text-xs font-bold uppercase tracking-wider text-primary">{{ t('channelEditor.nav.basic') }}</h4>

    <div v-if="managedAccount && providerName" class="mb-2 flex items-center gap-3 rounded-lg border border-primary/30 bg-primary/5 p-3">
      <Building class="h-5 w-5 shrink-0 text-primary" />
      <div class="min-w-0 flex-1">
        <div class="text-[10px] font-semibold uppercase tracking-wider text-muted-foreground">
          {{ t('channelEditor.managed.providerLabel') }}
        </div>
        <div class="text-sm font-bold text-foreground">
          {{ t(officialProvider ? 'channelEditor.managed.officialChannel' : 'channelEditor.managed.providerChannel', { provider: providerName }) }}
        </div>
      </div>
      <Badge
        :class="officialProvider
          ? 'border-emerald-500/30 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300'
          : 'border-primary/30 bg-primary/10 text-primary'"
        class="shrink-0 gap-1 text-[10px]"
      >
        <component :is="officialProvider ? BadgeCheck : Cog" class="h-3 w-3" />
        {{ t(officialProvider ? 'channelEditor.managed.officialBadge' : 'channelEditor.managed.managedBadge') }}
      </Badge>
    </div>

    <div class="grid gap-3 md:grid-cols-[minmax(0,8fr)_minmax(0,4fr)]">
      <div v-if="!managedAccount || !providerName" class="space-y-1.5">
        <Label class="text-xs font-semibold text-muted-foreground">
          {{ t('channelEditor.basic.name.label') }}
        </Label>
        <Input
          :model-value="form.name"
          class="h-9"
          readonly
        />
        <p class="text-[10px] text-muted-foreground">{{ t('channelEditor.basic.name.autoDerivedHint') }}</p>
      </div>
      <div class="space-y-1.5">
        <Label class="text-xs font-semibold text-muted-foreground">
          {{ t('channelEditor.basic.serviceType.label') }} <span class="text-destructive">*</span>
        </Label>
        <Select :model-value="form.serviceType" @update:model-value="updateField('serviceType', $event as any)">
          <SelectTrigger class="h-9 w-full" :class="{ 'border-destructive': errors.serviceType }">
            <SelectValue :placeholder="t('channelEditor.basic.serviceType.placeholder')" />
          </SelectTrigger>
          <SelectContent>
            <SelectItem v-for="opt in serviceTypeOptions" :key="opt.value" :value="opt.value">
              {{ opt.label }}
            </SelectItem>
          </SelectContent>
        </Select>
        <p v-if="errors.serviceType" class="text-[10px] text-destructive">{{ errors.serviceType }}</p>
      </div>
    </div>

    <div class="space-y-1.5">
      <Label class="text-xs font-semibold text-muted-foreground">{{ t('channelEditor.basic.remark.label') }}</Label>
      <Input
        :model-value="form.remark"
        class="h-9"
        maxlength="10"
        :placeholder="t('channelEditor.basic.remark.hint')"
        @update:model-value="(val) => updateField('remark', val as string)"
      />
      <p class="text-[10px] leading-4 text-muted-foreground">{{ t('channelEditor.basic.remark.hint') }}</p>
    </div>

    <div v-if="form.serviceType !== 'copilot'" class="space-y-1.5">
      <div class="flex items-center justify-between gap-3">
        <Label class="text-xs font-semibold text-muted-foreground">
          {{ t('channelEditor.basic.baseUrl.label') }} <span class="text-destructive">*</span>
        </Label>
        <span class="origin-right scale-95 text-[10px] text-muted-foreground/80">{{ t('channelEditor.basic.multiLineFailover') }}</span>
      </div>
      <Textarea
        :model-value="form.baseUrlsText"
        :placeholder="t('channelEditor.basic.baseUrl.placeholder')"
        class="min-h-[74px] w-full rounded-lg border border-input bg-background/50 px-3 py-2 font-mono text-xs leading-relaxed shadow-inner outline-none transition-all focus:border-primary focus:ring-2 focus:ring-primary/20"
        :class="{ 'border-destructive': errors.baseUrl }"
        @update:model-value="(val) => updateField('baseUrlsText', val as string)"
      />
      <div v-if="expectedRequestUrls.length && !errors.baseUrl" class="space-y-1 rounded-md border border-border/30 bg-accent/40 px-2 py-1">
        <div
          v-for="item in expectedRequestUrls"
          :key="`${item.baseUrl || ''}-${item.expectedUrl}`"
          class="flex items-start gap-1.5 text-[10px] text-muted-foreground/70"
        >
          <span class="mt-1.5 inline-block size-1.5 shrink-0 animate-pulse rounded-full bg-emerald-500"></span>
          <span class="block min-w-0 break-all font-mono">
            {{ t('addChannel.expectedRequest') }} {{ item.expectedUrl }}
          </span>
        </div>
      </div>
      <p v-if="errors.baseUrl" class="text-[10px] text-destructive">{{ errors.baseUrl }}</p>
    </div>

    <div class="space-y-1.5">
      <Label class="text-xs font-semibold text-muted-foreground">{{ t('channelEditor.basic.website.label') }}</Label>
      <Input
        :model-value="form.website"
        class="h-9"
        :placeholder="t('channelEditor.basic.website.placeholder')"
        @update:model-value="(val) => updateField('website', val as string)"
      />
    </div>

    <div class="space-y-1.5">
      <Label class="text-xs font-semibold text-muted-foreground">{{ t('addChannel.descriptionLabel') }}</Label>
      <Textarea
        :model-value="form.description"
        rows="3"
        class="min-h-[84px] resize-none"
        :placeholder="t('addChannel.descriptionHint')"
        @update:model-value="(val) => updateField('description', val as string)"
      />
      <p class="text-[10px] leading-4 text-muted-foreground">{{ t('addChannel.descriptionHint') }}</p>
    </div>
  </section>
</template>
