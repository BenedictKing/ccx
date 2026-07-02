<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { KeyRound, Network, Sparkles, CheckCircle2, Loader2, ExternalLink } from 'lucide-vue-next'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { useChannelPresets } from '@/composables/useChannelPresets'
import { useDesktopActivity } from '@/composables/useDesktopActivity'
import { useLanguage } from '@/composables/useLanguage'
import { openProviderPromotion, openProviderConsole, providerConsoleLinks, providerPromotionLinks } from '@/lib/external-link'
import { maskApiKey } from '@/utils/api-key-mask'
import compshareIcon from '@/assets/compshare.png'
import runapiIcon from '@/assets/runapi.svg'
import unity2Icon from '@/assets/unity2.jpg'
import type { ProviderPreset, ProviderPlan, ChannelTarget, ProviderKeyAsset } from '@/types'

const { t, tf } = useLanguage()
const { isChannelPageActive } = useDesktopActivity()
const emit = defineEmits<{
  created: [target: string]
}>()

const {
  presets,
  keyAssets,
  keysByProvider,
  creating,
  error,
  result,
  loadChannelPresets,
  createChannel,
} = useChannelPresets()

const providerIcons: Record<string, string> = {
  compshare: compshareIcon,
  runapi: runapiIcon,
  unity2: unity2Icon,
}

const selectedProvider = ref('')
const selectedTarget = ref('')
const selectedPlan = ref('')
const apiKey = ref('')
const channelName = ref('')
const localError = ref('')
let hasLoadedPresets = false

async function ensurePresetsLoaded() {
  if (!isChannelPageActive.value) return
  await loadChannelPresets()
  if (!selectedProvider.value && orderedPresets.value.length > 0) {
    selectedProvider.value = orderedPresets.value[0].id
  }
  hasLoadedPresets = true
}

onMounted(() => {
  void ensurePresetsLoaded()
})

const presetOrder = [
  'deepseek',
  'mimo',
  'compshare',
  'runapi',
  'unity2',
  'openrouter',
  'kimi',
  'glm',
  'sensenova',
  'minimax',
  'dashscope',
  'modelscope',
  'xfyun',
  'tencent-lkeap',
  'volc-ark',
  'qianfan',
  'opencode-zen',
  'opencode-go',
]
const presetRank = new Map(presetOrder.map((id, index) => [id, index]))

// 暂不在渠道中心展示的 preset（后端仍提供，作为后备或后续开放）。
const hiddenPresetIds = new Set(['originrouter', 'opencode-go'])

// 有 Key 的 provider 组整体提前；组内仍保持固定产品顺序，避免 Key 状态改变组内排序。
const orderedPresets = computed(() =>
  presets.value
    .filter((preset) => !hiddenPresetIds.has(preset.id))
    .sort((a, b) => {
      const keyDiff = Number(!!keysByProvider.value[b.id]) - Number(!!keysByProvider.value[a.id])
      if (keyDiff !== 0) return keyDiff
      return (presetRank.get(a.id) ?? presetOrder.length) - (presetRank.get(b.id) ?? presetOrder.length)
    }),
)

const currentPreset = computed(() => {
  return orderedPresets.value.find((item) => item.id === selectedProvider.value) || null
})

const localizePresetLabel = (preset: ProviderPreset) =>
  tf(`channel.preset.${preset.id}.label`, preset.label)

const localizePresetDescription = (preset: ProviderPreset) =>
  tf(`channel.preset.${preset.id}.description`, preset.description)

const localizePlanLabel = (preset: ProviderPreset, plan: ProviderPlan) => {
  const target = selectedTarget.value || preset.defaultTarget
  const fallback = tf(`channel.preset.${preset.id}.plan.${plan.id}.label`, plan.label)
  return tf(`channel.preset.${preset.id}.target.${target}.plan.${plan.id}.label`, fallback)
}

const localizePlanDescription = (preset: ProviderPreset, plan: ProviderPlan) => {
  const target = selectedTarget.value || preset.defaultTarget
  const fallback = tf(`channel.preset.${preset.id}.plan.${plan.id}.description`, plan.description)
  return tf(`channel.preset.${preset.id}.target.${target}.plan.${plan.id}.description`, fallback)
}

const localizeTargetLabel = (target: ChannelTarget) =>
  tf(`channel.target.${target.type}.label`, target.label)

// target description 优先取 provider 级覆盖（如 MiMo messages 描述差异化），
// 找不到时回退到共用 target description，再回退到 Go preset 原文
const localizeTargetDescription = (preset: ProviderPreset, target: ChannelTarget) => {
  const overrideKey = `channel.preset.${preset.id}.target.${target.type}.description`
  const sharedKey = `channel.target.${target.type}.description`
  const sharedFallback = tf(sharedKey, target.description)
  return tf(overrideKey, sharedFallback)
}

const currentAsset = computed(() => {
  const provider = selectedProvider.value.trim()
  if (!provider) return undefined

  const planId = selectedPlan.value.trim()
  const asset = findKeyAsset(provider, planId)
  if (asset) return asset

  if (provider === 'opencode-zen') {
    return findOpenCodeFallbackAsset(planId)
  }

  return undefined
})

const savedApiKeyMask = computed(() => {
  return currentAsset.value?.apiKey ? maskApiKey(currentAsset.value.apiKey) : ''
})

const currentPlan = computed(() => {
  return currentPreset.value?.plans.find((item) => item.id === selectedPlan.value) || currentPreset.value?.plans[0]
})

const targetOptions = computed<ChannelTarget[]>(() => currentPreset.value?.targets || [])

// 切换左侧 provider 时重置表单；仅响应 selectedProvider 变化，
// 避免 target 变化触发 loadChannelPresets 后因 presets 更新而级联重置 selectedTarget
watch(selectedProvider, (id) => {
  const preset = orderedPresets.value.find((item) => item.id === id)
  if (!preset) return
  selectedTarget.value = preset.defaultTarget
  selectedPlan.value = bestPlanForTarget(preset, preset.defaultTarget)
  apiKey.value = ''
  channelName.value = buildChannelName(preset, preset.defaultTarget, selectedPlan.value)
  result.value = null
  localError.value = ''
})

// target 变化时重新加载后端过滤后的 plans，尽量保留已选 plan；
// 若当前 plan 被协议过滤掉，尝试切换同区域的协议变体（如 token-cn ↔ token-cn-anthropic）
watch(selectedTarget, async (target) => {
  if (!target || !hasLoadedPresets || !isChannelPageActive.value) return
  result.value = null
  const prevPlan = selectedPlan.value
  await loadChannelPresets(target)
  const preset = currentPreset.value
  if (!preset) return
  if (preset.plans.some((p) => p.id === prevPlan)) {
    selectedPlan.value = prevPlan
  } else {
    const counterpart = prevPlan.endsWith('-anthropic')
      ? prevPlan.replace(/-anthropic$/, '')
      : prevPlan + '-anthropic'
    const match = preset.plans.find((p) => p.id === counterpart)
    selectedPlan.value = match ? match.id : bestPlanForTarget(preset, target)
  }
  channelName.value = buildChannelName(preset, target, selectedPlan.value)
})

watch(isChannelPageActive, (active) => {
  if (active && !hasLoadedPresets) void ensurePresetsLoaded()
})

// plan 变化时同步刷新 channel 名，确保不同套餐入口可以共存而非互相覆盖
watch(selectedPlan, (planId) => {
  const preset = currentPreset.value
  if (!preset || !planId) return
  channelName.value = buildChannelName(preset, selectedTarget.value || preset.defaultTarget, planId)
})

// buildChannelName 生成默认渠道名：OpenCode 合并卡片按套餐使用 Zen/Go 前缀，其它 provider 仅在非默认 plan 时追加 plan suffix。
// 例如 MiMo + messages + 默认 anthropic plan → desktop-mimo-messages
//      MiMo + messages + token-sgp-anthropic → desktop-mimo-messages-token-sgp-anthropic
// 这样用户切换非默认套餐时会得到独立渠道名，避免后端同名覆盖。
function buildChannelName(preset: ProviderPreset, target: string, planId: string): string {
  const base = `desktop-${channelProviderIdForPlan(preset, planId)}-${target}`
  if (preset.id === 'opencode-zen' && (planId === 'openai-chat' || isOpenCodeGoPlan(planId))) return base
  const defaultPlan = bestPlanForTarget(preset, target)
  if (!planId || planId === defaultPlan) return base
  return `${base}-${planId}`
}

function bestPlanForTarget(preset: ProviderPreset, target: string): string {
  if (preset.plans.length <= 1) return preset.plans[0]?.id || ''
  const wantOpenCodeChat = target === 'messages' && (preset.id === 'opencode-zen' || preset.id === 'opencode-go')
  if (wantOpenCodeChat) {
    const recommended = preset.plans.find((p) => !p.custom && p.recommended && planProtocol(p) === 'openai')
    if (recommended) return recommended.id
    const openaiPlan = preset.plans.find((p) => !p.custom && planProtocol(p) === 'openai')
    if (openaiPlan) return openaiPlan.id
  }
  const wantedProtocol = target === 'messages' ? 'anthropic' : target === 'responses' ? 'responses' : 'openai'
  for (const plan of preset.plans) {
    if (plan.custom) continue
    const protocol = planProtocol(plan)
    if (protocol === wantedProtocol) return plan.id
    if (target === 'responses' && protocol === 'openai') return plan.id
  }
  const recommended = preset.plans.find((p) => p.recommended)
  return recommended?.id || preset.plans[0]?.id || ''
}

function findKeyAsset(provider: string, planId: string): ProviderKeyAsset | undefined {
  const providerAssets = keyAssets.value.filter((item) => item.provider === provider && item.apiKey)
  if (planId) {
    const exact = providerAssets.find((item) => (item.planId || '') === planId)
    if (exact) return exact

    const hasPlanScopedAsset = providerAssets.some((item) => !!item.planId)
    return hasPlanScopedAsset ? undefined : providerAssets.find((item) => !item.planId)
  }

  return providerAssets.find((item) => !item.planId) || providerAssets[0]
}

function findOpenCodeFallbackAsset(planId: string): ProviderKeyAsset | undefined {
  if (isOpenCodeGoPlan(planId)) {
    const legacyGoAsset = findKeyAsset('opencode-go', legacyOpenCodeGoPlanId(planId))
    if (legacyGoAsset) return legacyGoAsset
  }
  return findKeyAsset('opencode-zen', '') || findKeyAsset('opencode-go', '')
}

function isOpenCodeGoPlan(planId: string): boolean {
  return planId.startsWith('go-')
}

function legacyOpenCodeGoPlanId(planId: string): string {
  return isOpenCodeGoPlan(planId) ? planId.replace(/^go-/, '') : planId
}

function channelProviderIdForPlan(preset: ProviderPreset, planId: string): string {
  if (preset.id === 'opencode-zen' && isOpenCodeGoPlan(planId)) return 'opencode-go'
  return preset.id
}

function planProtocol(plan: ProviderPlan): 'anthropic' | 'responses' | 'openai' {
  const id = plan.id.toLowerCase()
  const label = plan.label.toLowerCase()
  const baseUrl = (plan.baseUrl || '').toLowerCase().replace(/\/+$/, '')
  if (id.includes('responses') || label.includes('responses') || baseUrl.endsWith('/responses')) return 'responses'
  if (id.includes('anthropic') || label.includes('anthropic') || baseUrl.includes('anthropic') || baseUrl.endsWith('/messages')) return 'anthropic'
  return 'openai'
}

const capabilityBadges = computed(() => {
  const preset = currentPreset.value
  if (!preset) return []
  return [
    preset.directAgent && t('channel.badgeDirectAgent'),
    preset.nativeMessages && t('channel.badgeNativeMessages'),
    preset.chatCompatible && 'OpenAI Chat',
    preset.responsesCompatible && 'Codex',
  ].filter(Boolean) as string[]
})

const effectiveBaseUrl = computed(() => {
  return currentPlan.value?.baseUrl || currentAsset.value?.baseUrl || ''
})

const submit = async () => {
  localError.value = ''
  const preset = currentPreset.value
  if (!preset) return
  if (!apiKey.value.trim() && !currentAsset.value?.apiKey) {
    localError.value = t('channel.missingKey')
    return
  }
  const target = selectedTarget.value || preset.defaultTarget
  await createChannel({
    provider: preset.id,
    target,
    planId: selectedPlan.value,
    baseUrl: effectiveBaseUrl.value,
    apiKey: apiKey.value.trim() || currentAsset.value?.apiKey || '',
    name: channelName.value.trim(),
  })
  apiKey.value = ''
  emit('created', target)
}
</script>

<template>
  <div class="flex h-full min-h-0 flex-col gap-5">
    <div class="bg-glass dark:bg-glass-dark border border-border rounded-2xl p-5 shrink-0">
      <div class="flex items-start justify-between gap-4">
        <div>
          <div class="flex items-center gap-2 text-primary mb-2">
            <Network class="w-4 h-4" />
            <span class="text-xs font-bold uppercase tracking-[0.2em]">{{ t('channel.headerEyebrow') }}</span>
          </div>
          <h3 class="text-xl font-bold text-foreground">{{ t('channel.title') }}</h3>
          <p class="text-sm text-muted-foreground mt-1 max-w-2xl">
            {{ t('channel.description') }}
          </p>
        </div>
      </div>
    </div>

    <div class="grid grid-cols-1 gap-4 md:min-h-0 md:flex-1 md:overflow-hidden md:grid-cols-[280px_1fr]">
      <div class="space-y-1.5 md:min-h-0 md:overflow-y-auto md:overscroll-contain md:pr-1">
        <button
          v-for="preset in orderedPresets"
          :key="preset.id"
          :class="[
            'w-full p-3 rounded-xl border text-left transition-colors duration-200',
            selectedProvider === preset.id
              ? 'border-border bg-secondary/60 dark:border-white/10 dark:bg-white/[0.04]'
              : 'border-border bg-card/40 hover:bg-card/70 dark:hover:bg-white/[0.03]'
          ]"
          @click="selectedProvider = preset.id"
        >
          <div class="flex items-start gap-3">
            <img
              v-if="providerIcons[preset.id]"
              :src="providerIcons[preset.id]"
              :alt="`${preset.label} icon`"
              :class="[
                'mt-0.5 shrink-0 bg-secondary object-cover ring-1 ring-border',
                preset.id === 'runapi' ? 'h-9 w-9 rounded-xl' : 'h-8 w-8 rounded-lg',
              ]"
            >
            <div class="min-w-0 flex-1">
              <div class="flex items-center justify-between gap-2">
                <span class="font-semibold text-foreground">{{ localizePresetLabel(preset) }}</span>
                <span v-if="keysByProvider[preset.id]" class="text-[10px] text-emerald-700 dark:text-emerald-400 bg-emerald-500/10 px-1.5 py-0.5 rounded border border-emerald-500/20">
                  {{ t('channel.hasKey') }}
                </span>
              </div>
              <p class="text-xs text-muted-foreground mt-1 truncate">{{ localizePresetDescription(preset) }}</p>
            </div>
          </div>
        </button>

      </div>

      <div v-if="currentPreset" class="bg-glass dark:bg-glass-dark border border-border rounded-2xl p-5 space-y-5 md:min-h-0 md:overflow-y-auto md:overscroll-contain">
        <div class="space-y-3">
          <div class="flex flex-wrap items-center gap-2">
            <h3 class="text-lg font-semibold text-foreground">{{ localizePresetLabel(currentPreset) }}</h3>
            <span
              v-for="badge in capabilityBadges"
              :key="badge"
              class="text-[10px] text-blue-700 dark:text-blue-300 bg-blue-500/10 border border-blue-500/20 rounded-full px-2 py-0.5"
            >
              {{ badge }}
            </span>
          </div>
          <p class="text-sm text-muted-foreground">{{ localizePresetDescription(currentPreset) }}</p>
          <div class="flex items-center gap-4">
            <button
              v-if="providerPromotionLinks[currentPreset.id]"
              type="button"
              class="inline-flex items-center gap-1.5 text-xs font-medium text-primary hover:text-primary/80"
              @click="openProviderPromotion(currentPreset.id)"
            >
              {{ t('channel.promo') }}
              <ExternalLink class="h-3 w-3" />
            </button>
            <button
              v-if="providerConsoleLinks[currentPreset.id]"
              type="button"
              class="inline-flex items-center gap-1.5 text-xs font-medium text-muted-foreground hover:text-foreground"
              @click="openProviderConsole(currentPreset.id)"
            >
              {{ t('channel.console') }}
              <ExternalLink class="h-3 w-3" />
            </button>
          </div>
        </div>

        <div class="grid grid-cols-1 lg:grid-cols-2 gap-4">
          <div class="space-y-2">
            <Label class="text-xs text-muted-foreground">{{ t('channel.target') }}</Label>
            <select
              v-model="selectedTarget"
              class="w-full h-9 rounded-md border border-border bg-background px-3 text-sm text-foreground focus:outline-none focus:ring-1 focus:ring-ring"
            >
              <option v-for="target in targetOptions" :key="target.type" :value="target.type">
                {{ localizeTargetLabel(target) }}
              </option>
            </select>
            <p class="text-xs text-muted-foreground">
              {{
                currentPreset && targetOptions.find((item) => item.type === selectedTarget)
                  ? localizeTargetDescription(currentPreset, targetOptions.find((item) => item.type === selectedTarget)!)
                  : ''
              }}
            </p>
          </div>

          <div class="space-y-2">
            <Label class="text-xs text-muted-foreground">{{ t('channel.planLabel') }}</Label>
            <select
              v-model="selectedPlan"
              class="w-full h-9 rounded-md border border-border bg-background px-3 text-sm text-foreground focus:outline-none focus:ring-1 focus:ring-ring"
            >
              <option v-for="plan in currentPreset.plans" :key="plan.id" :value="plan.id">
                {{ localizePlanLabel(currentPreset, plan) }}
              </option>
            </select>
            <p class="text-xs text-muted-foreground">{{ currentPlan ? localizePlanDescription(currentPreset, currentPlan) : '' }}</p>
            <p v-if="currentPlan?.baseUrl" class="text-xs text-foreground font-mono break-all">{{ currentPlan.baseUrl }}</p>
          </div>
        </div>

        <div class="grid grid-cols-1 lg:grid-cols-2 gap-4">
          <div class="space-y-2">
            <Label class="text-xs text-muted-foreground">{{ t('channel.apiKeyLabel') }}</Label>
            <Input v-model="apiKey" type="password" autocomplete="off" :placeholder="currentAsset?.apiKey ? t('channel.keySavedPlaceholder') : t('channel.keyInputPlaceholder')" />
            <div v-if="currentAsset?.apiKey" class="flex items-center gap-1.5 text-xs text-emerald-700 dark:text-emerald-400">
              <KeyRound class="w-3 h-3" />
              {{ t('channel.reuseKey', { provider: localizePresetLabel(currentPreset), key: savedApiKeyMask }) }}
            </div>
          </div>

          <div class="space-y-2">
            <Label class="text-xs text-muted-foreground">{{ t('channel.name') }}</Label>
            <Input v-model="channelName" placeholder="desktop-provider-type" />
            <p class="text-xs text-muted-foreground">{{ t('channel.nameHint') }}</p>
          </div>
        </div>

        <div class="rounded-xl bg-secondary/50 border border-border p-3 text-xs text-muted-foreground space-y-1.5">
          <div class="flex items-center gap-1.5 text-foreground">
            <Sparkles class="w-3.5 h-3.5 text-blue-700 dark:text-blue-400" />
            <span class="font-semibold">{{ t('channel.presetWrites') }}</span>
          </div>
          <p class="break-all">{{ t('channel.baseUrlPrefix') }}: <code class="text-foreground">{{ effectiveBaseUrl || '—' }}</code></p>
          <p>{{ t('channel.capabilityHint') }}</p>
        </div>

        <p v-if="localError || error" class="text-sm text-rose-700 dark:text-rose-400">{{ localError || error }}</p>
        <div v-if="result" class="flex items-center gap-2 rounded-xl border border-emerald-500/20 bg-emerald-500/10 px-3 py-2 text-sm text-emerald-700 dark:text-emerald-300">
          <CheckCircle2 class="w-4 h-4 shrink-0" />
          {{ result.message }}：{{ result.name }} → {{ result.baseUrl }}
        </div>

        <div class="flex justify-end">
          <Button :disabled="creating" @click="submit">
            <Loader2 v-if="creating" class="w-3.5 h-3.5 mr-1.5 animate-spin" />
            {{ t('channel.addToCCX') }}
          </Button>
        </div>
      </div>
    </div>
  </div>
</template>
