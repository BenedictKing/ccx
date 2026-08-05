<script setup lang="ts">
import { computed } from 'vue'
import type {
  Channel,
  ChannelMetrics,
  ChannelRecentActivity,
  TimeWindowStats,
} from '@/services/admin-api'
import { useLanguage } from '@/composables/useLanguage'
import { availableChannelApiKeyCount, disabledChannelApiKeyCount } from '@/utils/channelApiKeys'
import { openExternalLink } from '@/lib/external-link'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from '@/components/ui/dropdown-menu'
import {
  AlertTriangle,
  ArrowDown,
  ArrowUp,
  Ban,
  Copy,
  Database,
  Edit3,
  ExternalLink,
  GripVertical,
  History,
  Key,
  MoreVertical,
  Pause,
  Play,
  RotateCcw,
  Sparkles,
  Trash2,
  Zap,
} from 'lucide-vue-next'
import ActivityChart from './ActivityChart.vue'

const props = withDefaults(defineProps<{
  channel: Channel
  metrics?: ChannelMetrics
  activity?: ChannelRecentActivity
  priority?: number
  inactive?: boolean
  expanded?: boolean
  supportsCapability?: boolean
  canDelete?: boolean
  canMoveTop?: boolean
  canMoveBottom?: boolean
}>(), {
  priority: 0,
  inactive: false,
  expanded: false,
  supportsCapability: true,
  canDelete: true,
  canMoveTop: false,
  canMoveBottom: false,
})

const emit = defineEmits<{
  edit: []
  delete: []
  logs: []
  capability: []
  status: []
  resume: []
  promote: []
  moveTop: []
  moveBottom: []
  disable: []
  enable: []
  toggle: []
}>()

const { t } = useLanguage()

const isSuspended = computed(() => props.channel.status === 'suspended')
const isDisabled = computed(() => props.channel.status === 'disabled')
const isCircuitOpen = computed(() => props.metrics?.circuitState === 'open')
const isTripped = computed(() => isSuspended.value || isCircuitOpen.value)

const serviceTypeClass = computed(() => {
  return protocolClass(props.channel.serviceType)
})

function protocolClass(serviceType: string) {
  const map: Record<string, string> = {
    openai: 'border-blue-500/25 bg-blue-500/10 text-blue-700 dark:text-blue-300',
    claude: 'border-orange-500/25 bg-orange-500/10 text-orange-700 dark:text-orange-300',
    gemini: 'border-purple-500/25 bg-purple-500/10 text-purple-700 dark:text-purple-300',
    responses: 'border-emerald-500/25 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300',
  }
  return map[serviceType] || map.openai
}

const statusConfig = computed(() => {
  if (isDisabled.value) {
    return {
      label: t('status.disabled'),
      dot: 'bg-muted-foreground',
    }
  }
  if (isCircuitOpen.value) {
    return {
      label: t('status.tripped'),
      dot: 'bg-rose-500 animate-pulse',
    }
  }
  if (isSuspended.value) {
    return {
      label: t('status.paused'),
      dot: 'bg-amber-500',
    }
  }
  return {
    label: t('status.normal'),
    dot: 'bg-emerald-500',
  }
})

const circuitDisplay = computed(() => {
  const state = props.metrics?.circuitState
  if (state === 'open') {
    return {
      label: t('status.tripped'),
      class: 'border-rose-500/25 bg-rose-500/10 text-rose-700 dark:text-rose-300',
    }
  }
  return null
})

const isPromoted = computed(() => {
  if (!props.channel.promotionUntil) return false
  return new Date(props.channel.promotionUntil).getTime() > Date.now()
})

const keyCount = computed(() => availableChannelApiKeyCount(props.channel))
const disabledKeyCount = computed(() => disabledChannelApiKeyCount(props.channel))

const successRateDisplay = computed(() => {
  const raw = props.metrics?.successRate
  if (raw === undefined || raw === null) return '—'
  const value = raw <= 1 ? raw * 100 : raw
  return `${value.toFixed(0)}%`
})

const requestDisplay = computed(() => {
  const value = props.metrics?.requestCount ?? 0
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M`
  if (value >= 1_000) return `${(value / 1_000).toFixed(1)}K`
  return `${value}`
})

const rpmDisplay = computed(() => {
  const rpm = props.activity?.rpm ?? props.channel.rpm ?? 0
  if (!rpm) return '—'
  return rpm >= 10 ? rpm.toFixed(0) : rpm.toFixed(1)
})

const tpmDisplay = computed(() => {
  const tpm = props.activity?.tpm ?? 0
  if (!tpm) return '—'
  if (tpm >= 1_000_000) return `${(tpm / 1_000_000).toFixed(1)}M`
  if (tpm >= 1_000) return `${(tpm / 1_000).toFixed(1)}K`
  return tpm.toFixed(0)
})

const currentStats = computed(() => props.metrics?.timeWindows?.['15m'])

const cacheHitRate = computed(() => {
  const stats = currentStats.value
  if (!shouldShowCacheHitRate(stats)) return null
  const inputTokens = stats.inputTokens ?? 0
  const cacheReadTokens = stats.cacheReadTokens ?? 0
  return stats.cacheHitRate ?? (cacheReadTokens / (inputTokens + cacheReadTokens) * 100)
})

const cacheHitRateClass = computed(() => {
  const rate = cacheHitRate.value
  if (rate === null) return 'border-muted bg-muted/50 text-muted-foreground'
  if (rate >= 50) return 'border-emerald-500/25 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300'
  if (rate >= 20) return 'border-sky-500/25 bg-sky-500/10 text-sky-700 dark:text-sky-300'
  if (rate >= 5) return 'border-amber-500/25 bg-amber-500/10 text-amber-700 dark:text-amber-300'
  return 'border-orange-500/25 bg-orange-500/10 text-orange-700 dark:text-orange-300'
})

const cacheHitRateDisplay = computed(() => {
  const rate = cacheHitRate.value
  return rate === null ? '' : `${rate.toFixed(0)}%`
})

const cacheHitRateTitle = computed(() => {
  const stats = currentStats.value
  if (!stats) return ''
  return `${t('orchestration.cache')} ${cacheHitRateDisplay.value} · R ${formatTokenCount(stats.cacheReadTokens ?? 0)} · W ${formatTokenCount(stats.cacheCreationTokens ?? 0)}`
})

const CACHE_WRITE_WARNING_MIN_REQUESTS = 5
const CACHE_WRITE_WARNING_MIN_TOKENS = 100000
const CACHE_WRITE_WARNING_RATIO = 0.2

const cacheWriteWarning = computed(() => {
  const stats = currentStats.value
  if (!stats || (stats.requestCount ?? 0) < CACHE_WRITE_WARNING_MIN_REQUESTS) return false
  const inputTokens = stats.inputTokens ?? 0
  const cacheReadTokens = stats.cacheReadTokens ?? 0
  const cacheCreationTokens = stats.cacheCreationTokens ?? 0
  const denom = inputTokens + cacheReadTokens
  if (denom <= 0 || cacheCreationTokens < CACHE_WRITE_WARNING_MIN_TOKENS) return false
  return (cacheCreationTokens / denom) >= CACHE_WRITE_WARNING_RATIO
})

function shouldShowCacheHitRate(stats?: TimeWindowStats): stats is TimeWindowStats {
  if (!stats) return false
  const inputTokens = stats.inputTokens ?? 0
  const cacheReadTokens = stats.cacheReadTokens ?? 0
  return inputTokens + cacheReadTokens > 0
}

function formatTokenCount(value: number) {
  if (value >= 1_000_000) return `${(value / 1_000_000).toFixed(1)}M`
  if (value >= 1_000) return `${(value / 1_000).toFixed(1)}K`
  return value.toFixed(0)
}

const websiteUrl = computed(() => {
  if (props.channel.website) return props.channel.website
  try {
    const url = new URL(props.channel.baseUrl)
    return `${url.protocol}//${url.host}`
  } catch {
    return ''
  }
})

const baseUrlText = computed(() => {
  if (props.channel.baseUrls?.length) return props.channel.baseUrls.join(' · ')
  return props.channel.baseUrl || '—'
})

async function copyChannelInfo() {
  const lines = [
    ...(props.channel.baseUrls?.length ? props.channel.baseUrls : [props.channel.baseUrl]),
    ...(props.channel.apiKeys ?? []),
  ].map(item => item?.trim()).filter(Boolean)
  await navigator.clipboard?.writeText(lines.join('\n'))
}

function openWebsite() {
  if (!websiteUrl.value) return
  void openExternalLink(websiteUrl.value)
}

function handleCardClick(event: MouseEvent) {
  if (props.inactive) return
  const target = event.target as HTMLElement | null
  if (!target) return
  if (target.closest('button, a, input, textarea, select, [role="button"], [role="menuitem"], [data-no-card-toggle]')) return
  emit('toggle')
}
</script>

<template>
  <div
    :class="[
      'group relative grid cursor-pointer grid-cols-[24px_minmax(150px,1fr)_28px_84px_104px] items-center gap-1 border px-2 py-2 transition-all duration-200 lg:grid-cols-[36px_minmax(170px,1fr)_32px_92px_56px_104px] lg:gap-1.5 xl:grid-cols-[42px_minmax(220px,1fr)_32px_108px_64px_104px] xl:gap-3 xl:px-3 xl:py-2.5',
      'bg-card/75 hover:bg-card dark:bg-card/55 dark:hover:bg-card/80',
      'overflow-hidden',
      inactive ? 'border-dashed border-border/80 opacity-80' : 'border-border',
      circuitDisplay ? 'ring-1 ring-rose-500/20' : '',
    ]"
    @click="handleCardClick"
  >
    <!-- SVG activity chart background -->
    <ActivityChart :activity="activity" class="opacity-60 dark:opacity-70" />

    <!-- Content overlay (relative z-index to appear above chart) -->
    <div class="relative z-10 flex items-center gap-2 text-muted-foreground">
      <GripVertical class="hidden h-4 w-4 cursor-grab opacity-60 group-hover:opacity-100 lg:block" />
      <span class="min-w-5 text-right font-mono text-[11px] font-bold text-foreground/70">
        {{ priority || '—' }}
      </span>
    </div>

    <div class="relative z-10 min-w-0 space-y-1">
      <div class="flex min-w-0 items-center gap-2">
        <span
          class="h-2 w-2 shrink-0 rounded-full shadow-[0_0_8px_currentColor]"
          :class="statusConfig.dot"
          :title="statusConfig.label"
        />
        <button
          type="button"
          class="min-w-0 truncate text-left text-sm font-semibold text-foreground underline-offset-4 transition-colors hover:text-primary hover:underline focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-primary"
          @click.stop="emit('edit')"
          @keydown.enter.stop="emit('edit')"
          @keydown.space.prevent.stop="emit('edit')"
        >
          {{ channel.name }}
        </button>
        <template v-if="channel.protocolCapsules?.length">
          <Badge
            v-for="capsule in channel.protocolCapsules"
            :key="capsule.kind"
            class="shrink-0 border text-[10px] uppercase"
            :class="protocolClass(capsule.serviceType)"
          >
            {{ capsule.kind }}
          </Badge>
        </template>
        <Badge v-else class="shrink-0 border text-[10px] uppercase" :class="serviceTypeClass">
          {{ channel.serviceType }}
        </Badge>
        <button
          v-if="websiteUrl"
          type="button"
          class="inline-flex h-5 w-5 shrink-0 items-center justify-center rounded border border-border bg-secondary/50 text-muted-foreground transition-colors hover:border-primary/40 hover:text-primary"
          :title="t('orchestration.openWebsite')"
          @click.stop="openWebsite"
        >
          <ExternalLink class="h-3 w-3" />
        </button>
        <button
          type="button"
          class="inline-flex shrink-0 items-center gap-1 border border-border bg-secondary/50 px-1.5 py-0.5 text-[10px] font-semibold text-muted-foreground transition-colors hover:border-primary/30 hover:text-primary"
          @click.stop="emit('edit')"
        >
          <Key class="h-3 w-3" />
          {{ keyCount }} keys
        </button>
        <span
          v-if="isPromoted"
          class="inline-flex shrink-0 items-center gap-1 border border-purple-500/25 bg-purple-500/10 px-1.5 py-0.5 text-[10px] font-bold text-purple-700 dark:text-purple-300"
        >
          <Sparkles class="h-3 w-3" />
          PROMO
        </span>
        <span
          v-if="circuitDisplay"
          class="inline-flex shrink-0 items-center gap-1 border px-1.5 py-0.5 text-[10px] font-bold"
          :class="circuitDisplay.class"
        >
          <AlertTriangle class="h-3 w-3" />
          {{ circuitDisplay.label }}
        </span>
        <span
          v-if="cacheHitRate !== null"
          class="inline-flex shrink-0 items-center gap-1 border px-1.5 py-0.5 text-[10px] font-bold"
          :class="cacheHitRateClass"
          :title="cacheHitRateTitle"
        >
          <Database class="h-3 w-3" />
          {{ t('orchestration.cache') }} {{ cacheHitRateDisplay }}
        </span>
        <span
          v-if="cacheWriteWarning"
          class="inline-flex shrink-0 items-center gap-1 border border-amber-500/25 bg-amber-500/10 px-1.5 py-0.5 text-[10px] font-bold text-amber-700 dark:text-amber-300"
          :title="t('orchestration.cacheWriteHighHint')"
        >
          <AlertTriangle class="h-3 w-3" />
          {{ t('orchestration.cacheWriteHigh') }}
        </span>
      </div>
      <div class="truncate font-mono text-[11px] text-muted-foreground" :title="baseUrlText">
        {{ baseUrlText }}
      </div>
      <div v-if="channel.description" class="truncate text-[11px] text-muted-foreground" :title="channel.description">
        {{ channel.description }}
      </div>
    </div>

    <!-- Expand/collapse toggle + Status badge -->
    <div class="relative z-10 flex items-center justify-end gap-1">
      <button
        type="button"
        class="flex h-6 w-6 shrink-0 items-center justify-center rounded border border-border bg-secondary/50 text-muted-foreground transition-colors hover:border-primary/40 hover:text-foreground"
        :title="expanded ? t('chart.collapse') : t('chart.expandChart')"
        @click.stop="emit('toggle')"
      >
        <svg
          class="w-3 h-3 transition-transform"
          :class="{ 'rotate-180': expanded }"
          fill="none" stroke="currentColor" viewBox="0 0 24 24"
        >
          <path stroke-linecap="round" stroke-linejoin="round" stroke-width="2" d="M19 9l-7 7-7-7" />
        </svg>
      </button>
    </div>

    <div class="relative z-10 space-y-0.5 text-right font-mono">
      <div class="text-[11px] font-bold text-foreground xl:text-xs">{{ rpmDisplay }} / {{ tpmDisplay }}</div>
      <div class="text-[10px] text-muted-foreground">RPM / TPM</div>
    </div>

    <div class="relative z-10 hidden space-y-0.5 text-right font-mono lg:block">
      <div class="text-[11px] font-bold text-foreground xl:text-xs">{{ requestDisplay }}</div>
      <div class="text-[10px] text-muted-foreground">{{ successRateDisplay }}</div>
    </div>

    <div class="relative z-10 flex items-center justify-end gap-0.5">
      <Button
        v-if="isDisabled"
        variant="outline"
        size="icon-sm"
        class="border-0 bg-transparent shadow-none dark:bg-transparent"
        :title="t('orchestration.enable')"
        @click="emit('enable')"
      >
        <Play class="h-3.5 w-3.5" />
      </Button>
      <Button
        v-else-if="isTripped"
        variant="outline"
        size="icon-sm"
        class="border-0 bg-transparent shadow-none dark:bg-transparent"
        :title="t('orchestration.resume')"
        @click="emit('resume')"
      >
        <RotateCcw class="h-3.5 w-3.5" />
      </Button>
      <Button
        v-else
        variant="outline"
        size="icon-sm"
        class="border-0 bg-transparent shadow-none dark:bg-transparent"
        :title="t('orchestration.pause')"
        @click="emit('status')"
      >
        <Pause class="h-3.5 w-3.5" />
      </Button>

      <Button
        variant="outline"
        size="icon-sm"
        class="border-0 bg-transparent shadow-none dark:bg-transparent"
        :title="t('orchestration.logs')"
        @click="emit('logs')"
      >
        <History class="h-3.5 w-3.5" />
      </Button>

      <DropdownMenu>
        <DropdownMenuTrigger as-child>
          <Button variant="ghost" size="icon-sm" class="shrink-0">
            <MoreVertical class="h-4 w-4" />
          </Button>
        </DropdownMenuTrigger>
        <DropdownMenuContent align="end" class="w-52">
          <DropdownMenuLabel>{{ t('channelCard.actions') }}</DropdownMenuLabel>
          <DropdownMenuSeparator />
          <DropdownMenuGroup>
            <DropdownMenuItem @click="emit('edit')">
              <Edit3 class="h-4 w-4" />
              {{ t('orchestration.edit') }}
            </DropdownMenuItem>
            <DropdownMenuItem v-if="supportsCapability" @click="emit('capability')">
              <Zap class="h-4 w-4" />
              {{ t('addChannel.testCapability') }}
            </DropdownMenuItem>
            <DropdownMenuItem @click="copyChannelInfo">
              <Copy class="h-4 w-4" />
              {{ t('orchestration.copyConfig') }}
            </DropdownMenuItem>
          </DropdownMenuGroup>

          <DropdownMenuSeparator />
          <DropdownMenuGroup>
            <DropdownMenuItem v-if="circuitDisplay" @click="emit('resume')">
              <RotateCcw class="h-4 w-4" />
              {{ t('orchestration.resumeReset') }}
            </DropdownMenuItem>
            <DropdownMenuItem v-if="!isPromoted && !isDisabled" @click="emit('promote')">
              <Sparkles class="h-4 w-4" />
              {{ t('orchestration.promotion') }}
            </DropdownMenuItem>
            <DropdownMenuItem v-if="canMoveTop" @click="emit('moveTop')">
              <ArrowUp class="h-4 w-4" />
              {{ t('orchestration.moveTop') }}
            </DropdownMenuItem>
            <DropdownMenuItem v-if="canMoveBottom" @click="emit('moveBottom')">
              <ArrowDown class="h-4 w-4" />
              {{ t('orchestration.moveBottom') }}
            </DropdownMenuItem>
            <DropdownMenuItem v-if="!isDisabled" @click="emit('disable')">
              <Ban class="h-4 w-4" />
              {{ t('orchestration.moveToPool') }}
            </DropdownMenuItem>
          </DropdownMenuGroup>

          <DropdownMenuSeparator />
          <div class="px-2 py-1 text-[10px] text-muted-foreground">
            <Key class="mr-1 inline h-3 w-3" />
            {{ keyCount }} {{ t('channelCard.configuredKeys') }}
            <span v-if="disabledKeyCount"> · {{ disabledKeyCount }} {{ t('channelCard.disabledKeys') }}</span>
          </div>
          <DropdownMenuItem variant="destructive" :disabled="!canDelete" @click="canDelete && emit('delete')">
            <Trash2 class="h-4 w-4" />
            {{ t('orchestration.delete') }}
            <span v-if="!canDelete" class="ml-1 text-[10px] opacity-70">{{ t('orchestration.keepOne') }}</span>
          </DropdownMenuItem>
        </DropdownMenuContent>
      </DropdownMenu>
    </div>
  </div>
</template>
