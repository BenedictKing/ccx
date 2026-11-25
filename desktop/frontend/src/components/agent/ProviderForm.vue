<script setup lang="ts">
import { ExternalLink } from 'lucide-vue-next'
import { Label } from '@/components/ui/label'
import { Input } from '@/components/ui/input'
import { openProviderConsole, providerConsoleLinks } from '@/lib/external-link'
import type { AgentProvider } from '@/types'
import { useLanguage } from '@/composables/useLanguage'

const props = defineProps<{
  selectedProvider: AgentProvider
  providerKeys: Record<AgentProvider, string>
  savedProviderKeys: Record<string, string>
  mimoBaseUrl: string
  selectedMimoPlan: string
  selectedDashScopePlan: string
}>()

const emit = defineEmits<{
  'update:selectedProvider': [value: AgentProvider]
  'update:providerKeys': [value: Record<AgentProvider, string>]
  'update:mimoBaseUrl': [value: string]
  'update:selectedMimoPlan': [value: string]
  'update:selectedDashScopePlan': [value: string]
}>()

const { t } = useLanguage()

const mimoPlanOptions = [
  { label: t('agent.planPayAsYouGo'), value: 'https://api.xiaomimimo.com/anthropic' },
  { label: t('agent.planChina'), value: 'https://token-plan-cn.xiaomimimo.com/anthropic' },
  { label: t('agent.planSingapore'), value: 'https://token-plan-sgp.xiaomimimo.com/anthropic' },
  { label: t('agent.planEurope'), value: 'https://token-plan-ams.xiaomimimo.com/anthropic' },
]

const dashScopePlanOptions = [
  { label: t('agent.planPayAsYouGo'), value: 'https://dashscope.aliyuncs.com/apps/anthropic' },
  { label: t('agent.planSubscription'), value: 'https://coding.dashscope.aliyuncs.com/apps/anthropic' },
]

const onProviderChange = (e: Event) => {
  emit('update:selectedProvider', (e.target as HTMLSelectElement).value as AgentProvider)
}

const onKeyChange = (value: string | number) => {
  emit('update:providerKeys', {
    ...props.providerKeys,
    [props.selectedProvider]: String(value),
  })
}

const onMimoPlanChange = (e: Event) => {
  const planValue = (e.target as HTMLSelectElement).value
  emit('update:selectedMimoPlan', planValue)
  emit('update:mimoBaseUrl', planValue)
}

const onDashScopePlanChange = (e: Event) => {
  const planValue = (e.target as HTMLSelectElement).value
  emit('update:selectedDashScopePlan', planValue)
}

const keyPlaceholder = (provider: AgentProvider) => {
  if (provider === 'mimo' && props.selectedMimoPlan && props.savedProviderKeys[`claude:${provider}:${props.selectedMimoPlan}`]) {
    return t('agent.placeholderSaved')
  }
  if (provider === 'dashscope' && props.selectedDashScopePlan && props.savedProviderKeys[`claude:${provider}:${props.selectedDashScopePlan}`]) {
    return t('agent.placeholderSaved')
  }
  if (props.savedProviderKeys[`claude:${provider}`]) {
    return t('agent.placeholderSaved')
  }
  if (provider === 'mimo') return t('agent.placeholderMimo')
  if (provider === 'dashscope') return t('agent.placeholderDashScope')
  return t('agent.placeholderRequired')
}
</script>

<template>
  <div class="space-y-3">
    <div class="space-y-1.5">
      <Label class="text-xs text-muted-foreground">Provider</Label>
      <select
        :value="selectedProvider"
        class="w-full h-9 rounded-md border border-input bg-background px-3 py-1 text-sm shadow-sm transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
        @change="onProviderChange"
      >
        <option value="ccx">{{ t('agent.provider.localGateway') }}</option>
        <option value="deepseek">{{ t('agent.provider.deepseekDirect') }}</option>
        <option value="mimo">{{ t('agent.provider.mimoDirect') }}</option>
        <option value="kimi">{{ t('agent.provider.kimiDirect') }}</option>
        <option value="glm">{{ t('agent.provider.glmDirect') }}</option>
        <option value="sensenova">{{ t('agent.provider.sensenovaDirect') }}</option>
        <option value="minimax">{{ t('agent.provider.minimaxDirect') }}</option>
        <option value="dashscope">{{ t('agent.provider.dashscopeDirect') }}</option>
        <option value="xfyun">{{ t('agent.provider.xfyunDirect') }}</option>
        <option value="tencent-lkeap">{{ t('agent.provider.tencentLkeapDirect') }}</option>
        <option value="volc-ark">{{ t('agent.provider.volcArkDirect') }}</option>
        <option value="qianfan">{{ t('agent.provider.qianfanDirect') }}</option>
        <option value="openrouter">{{ t('agent.provider.openrouterDirect') }}</option>
        <option value="modelscope">{{ t('agent.provider.modelscopeDirect') }}</option>
        <option value="opencode-zen">{{ t('agent.provider.opencodeZenDirect') }}</option>
        <option value="opencode-go">{{ t('agent.provider.opencodeGoDirect') }}</option>
      </select>
    </div>

    <div v-if="selectedProvider !== 'ccx'" class="inline-flex items-center gap-3 flex-wrap">
      <button
        v-if="providerConsoleLinks[selectedProvider]"
        type="button"
        class="inline-flex items-center gap-1.5 text-xs font-medium text-muted-foreground hover:text-foreground"
        @click="openProviderConsole(selectedProvider)"
      >
        {{ t('agent.openConsole') }}
        <ExternalLink class="h-3 w-3" />
      </button>
    </div>

    <div v-if="selectedProvider === 'mimo'" class="space-y-1.5">
      <Label class="text-xs text-muted-foreground">{{ t('agent.billingModeMiMo') }}</Label>
      <select
        :value="selectedMimoPlan"
        class="w-full h-9 rounded-md border border-input bg-background px-3 py-1 text-sm shadow-sm transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
        @change="onMimoPlanChange"
      >
        <option
          v-for="opt in mimoPlanOptions"
          :key="opt.value || '__custom__'"
          :value="opt.value"
        >
          {{ opt.label }}
        </option>
      </select>
    </div>

    <div v-if="selectedProvider === 'dashscope'" class="space-y-1.5">
      <Label class="text-xs text-muted-foreground">{{ t('agent.billingModeDashScope') }}</Label>
      <select
        :value="selectedDashScopePlan"
        class="w-full h-9 rounded-md border border-input bg-background px-3 py-1 text-sm shadow-sm transition-colors focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-ring"
        @change="onDashScopePlanChange"
      >
        <option
          v-for="opt in dashScopePlanOptions"
          :key="opt.value"
          :value="opt.value"
        >
          {{ opt.label }}
        </option>
      </select>
    </div>

    <div v-if="selectedProvider !== 'ccx'" class="space-y-1.5">
      <Label class="text-xs text-muted-foreground">API Key <span class="text-destructive">*</span></Label>
      <Input
        type="password"
        autocomplete="off"
        :placeholder="keyPlaceholder(selectedProvider)"
        :model-value="providerKeys[selectedProvider]"
        @update:model-value="onKeyChange"
      />
    </div>

  </div>
</template>
