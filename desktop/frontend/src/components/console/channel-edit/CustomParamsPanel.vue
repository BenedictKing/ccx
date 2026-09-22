<script setup lang="ts">
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Switch } from '@/components/ui/switch'
import { CircleDollarSign, Clock, Flag, Globe, Network } from 'lucide-vue-next'
import { useLanguage } from '@/composables/useLanguage'

interface FormData {
  proxyUrl: string
  proxyPreferDirect: boolean
  racingEnabled: boolean
  costMultiplier: string | number
  channelPaymentCurrency: string
  channelPaymentAmount: string | number
  channelCreditCurrency: string
  channelCreditAmount: string | number
  routePrefix: string
  rateLimitRpm: string | number
  rateLimitWindowMinutes: string | number
  rateLimitMaxConcurrent: string | number
}

defineProps<{
  form: FormData
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
  <div class="space-y-6">
    <!-- Transport 代理路由网络 -->
    <section class="p-4 rounded-lg border border-border/60 bg-gradient-to-br from-background/60 to-background/40 shadow-sm backdrop-blur-sm space-y-3">
      <div class="flex items-center gap-1.5 text-[10px] font-bold uppercase tracking-wider text-primary border-b border-border/40 pb-2">
        <Globe class="h-3 w-3" />
        {{ t('channelEditor.transport.title') }}
      </div>
      <div class="grid gap-2">
        <div class="space-y-1">
          <Label class="text-[9px] font-bold text-muted-foreground">{{ t('channelEditor.transport.proxyUrl.label') }}</Label>
          <Input
            :model-value="form.proxyUrl"
            class="h-8 w-full font-mono text-xs"
            :placeholder="t('channelEditor.transport.proxyUrl.placeholder')"
            @update:model-value="(val) => updateField('proxyUrl', val as string)"
          />
          <p class="text-[10px] leading-4 text-muted-foreground">{{ t('channelEditor.transport.proxyUrl.hint') }}</p>
        </div>
        <!-- 直连优先：开关行（未配置代理时禁用） -->
        <div
          class="flex items-center justify-between gap-3 rounded-lg border px-3 py-2"
          :class="form.proxyPreferDirect && form.proxyUrl?.trim() ? 'border-primary/40 bg-primary/5' : 'border-border/60'"
        >
          <div class="flex min-w-0 items-center gap-2">
            <Network class="h-3.5 w-3.5 shrink-0" :class="form.proxyPreferDirect && form.proxyUrl?.trim() ? 'text-primary' : 'text-muted-foreground'" />
            <div class="min-w-0">
              <p class="text-xs font-medium">{{ t('channelEditor.transport.proxyPreferDirect.label') }}</p>
              <p class="text-[10px] leading-4 text-muted-foreground">{{ t('channelEditor.transport.proxyPreferDirect.hint') }}</p>
            </div>
          </div>
          <Switch
            :model-value="form.proxyPreferDirect"
            :disabled="!form.proxyUrl?.trim()"
            @update:model-value="(val) => updateField('proxyPreferDirect', val === true)"
          />
        </div>
        <!-- 渠道级竞速参与：显示为开 = racing.enabled !== false；未触碰保存时保持后端缺省 -->
        <div
          class="flex items-center justify-between gap-3 rounded-lg border px-3 py-2"
          :class="form.racingEnabled ? 'border-primary/40 bg-primary/5' : 'border-border/60'"
        >
          <div class="flex min-w-0 items-center gap-2">
            <Flag class="h-3.5 w-3.5 shrink-0" :class="form.racingEnabled ? 'text-primary' : 'text-muted-foreground'" />
            <div class="min-w-0">
              <p class="text-xs font-medium">{{ t('channelEditor.transport.racing.label') }}</p>
              <p class="text-[10px] leading-4 text-muted-foreground">{{ t('channelEditor.transport.racing.hint') }}</p>
            </div>
          </div>
          <Switch
            :model-value="form.racingEnabled"
            @update:model-value="(val) => updateField('racingEnabled', val === true)"
          />
        </div>
        <div class="space-y-1">
          <Label class="text-[9px] font-bold text-muted-foreground">{{ t('channelEditor.transport.routePrefix.label') }}</Label>
          <Input
            :model-value="form.routePrefix"
            class="h-8 w-full font-mono text-xs"
            :placeholder="t('channelEditor.transport.routePrefix.placeholder')"
            @update:model-value="(val) => updateField('routePrefix', val as string)"
          />
          <p class="text-[10px] leading-4 text-muted-foreground">{{ t('channelEditor.transport.routePrefix.hint') }}</p>
        </div>
      </div>
    </section>

    <!-- Rate Limit -->
    <section class="p-4 rounded-lg border border-border/60 bg-gradient-to-br from-background/60 to-background/40 shadow-sm backdrop-blur-sm space-y-3">
      <div class="flex items-center gap-1.5 text-[10px] font-bold uppercase tracking-wider text-primary border-b border-border/40 pb-2">
        <Clock class="h-3 w-3" />
        {{ t('channelEditor.rateLimit.title') }}
      </div>
      <p class="text-[10px] leading-4 text-muted-foreground">{{ t('channelEditor.rateLimit.section.hint') }}</p>
      <div class="grid gap-3 md:grid-cols-3">
        <div class="space-y-1">
          <Label class="text-[10px] font-medium text-muted-foreground/80">{{ t('channelEditor.rateLimit.rpm.label') }}</Label>
          <Input
            :model-value="form.rateLimitRpm"
            type="number"
            class="h-9 text-xs"
            :placeholder="t('channelEditor.rateLimit.rpm.placeholder')"
            @update:model-value="updateField('rateLimitRpm', $event)"
          />
          <p class="text-[10px] leading-4 text-muted-foreground">{{ t('channelEditor.rateLimit.rpm.hint') }}</p>
        </div>
        <div class="space-y-1">
          <Label class="text-[10px] font-medium text-muted-foreground/80">{{ t('channelEditor.rateLimit.window.label') }}</Label>
          <Input
            :model-value="form.rateLimitWindowMinutes"
            type="number"
            class="h-9 text-xs"
            :placeholder="t('channelEditor.rateLimit.window.placeholder')"
            @update:model-value="updateField('rateLimitWindowMinutes', $event)"
          />
          <p class="text-[10px] leading-4 text-muted-foreground">{{ t('channelEditor.rateLimit.window.hint') }}</p>
        </div>
        <div class="space-y-1">
          <Label class="text-[10px] font-medium text-muted-foreground/80">{{ t('channelEditor.rateLimit.maxConcurrent.label') }}</Label>
          <Input
            :model-value="form.rateLimitMaxConcurrent"
            type="number"
            class="h-9 text-xs"
            :placeholder="t('channelEditor.rateLimit.maxConcurrent.placeholder')"
            @update:model-value="updateField('rateLimitMaxConcurrent', $event)"
          />
          <p class="text-[10px] leading-4 text-muted-foreground">{{ t('channelEditor.rateLimit.maxConcurrent.hint') }}</p>
        </div>
      </div>
    </section>

    <!-- 渠道级计费：充值币种/金额 + 渠道币种/到账金额（0/空=不参与成本核算） -->
    <section class="p-4 rounded-lg border border-border/60 bg-gradient-to-br from-background/60 to-background/40 shadow-sm backdrop-blur-sm space-y-3">
      <div class="flex items-center gap-1.5 text-[10px] font-bold uppercase tracking-wider text-primary border-b border-border/40 pb-2">
        <CircleDollarSign class="h-3 w-3" />
        {{ t('channelEditor.billing.title') }}
      </div>
      <p class="text-[10px] leading-4 text-muted-foreground">{{ t('channelEditor.billing.hint') }}</p>
      <p class="text-[9px] font-bold uppercase tracking-wider text-muted-foreground/70">{{ t('channelEditor.billing.paymentGroup') }}</p>
      <div class="grid gap-3 md:grid-cols-2">
        <div class="space-y-1">
          <Label class="text-[10px] font-medium text-muted-foreground/80">{{ t('channelEditor.billing.paymentCurrency.label') }}</Label>
          <Input
            :model-value="form.channelPaymentCurrency"
            class="h-9 text-xs"
            placeholder="LDC / CNY / USD"
            @update:model-value="(val) => updateField('channelPaymentCurrency', val as string)"
          />
          <p class="text-[10px] leading-4 text-muted-foreground">{{ t('channelEditor.billing.paymentCurrency.hint') }}</p>
        </div>
        <div class="space-y-1">
          <Label class="text-[10px] font-medium text-muted-foreground/80">{{ t('channelEditor.billing.paymentAmount.label') }}</Label>
          <Input
            :model-value="form.channelPaymentAmount"
            type="number"
            step="0.01"
            min="0"
            class="h-9 text-xs"
            @update:model-value="updateField('channelPaymentAmount', $event)"
          />
          <p class="text-[10px] leading-4 text-muted-foreground">{{ t('channelEditor.billing.paymentAmount.hint') }}</p>
        </div>
      </div>
      <p class="text-[9px] font-bold uppercase tracking-wider text-muted-foreground/70">{{ t('channelEditor.billing.creditGroup') }}</p>
      <div class="grid gap-3 md:grid-cols-2">
        <div class="space-y-1">
          <Label class="text-[10px] font-medium text-muted-foreground/80">{{ t('channelEditor.billing.creditCurrency.label') }}</Label>
          <Input
            :model-value="form.channelCreditCurrency"
            class="h-9 text-xs"
            placeholder="USD"
            @update:model-value="(val) => updateField('channelCreditCurrency', val as string)"
          />
          <p class="text-[10px] leading-4 text-muted-foreground">{{ t('channelEditor.billing.creditCurrency.hint') }}</p>
        </div>
        <div class="space-y-1">
          <Label class="text-[10px] font-medium text-muted-foreground/80">{{ t('channelEditor.billing.creditAmount.label') }}</Label>
          <Input
            :model-value="form.channelCreditAmount"
            type="number"
            step="0.01"
            min="0"
            class="h-9 text-xs"
            @update:model-value="updateField('channelCreditAmount', $event)"
          />
          <p class="text-[10px] leading-4 text-muted-foreground">{{ t('channelEditor.billing.creditAmount.hint') }}</p>
        </div>
      </div>
      <p class="text-[10px] leading-4 text-muted-foreground">{{ t('channelEditor.billing.example') }}</p>
    </section>
  </div>
</template>
