<script setup lang="ts">
import { ArrowDown } from 'lucide-vue-next'
import { Tooltip, TooltipContent, TooltipTrigger } from '@/components/ui/tooltip'
import { useLanguage } from '@/composables/useLanguage'

interface ChannelInfo {
  index: number
  name: string
  status: string
  priority?: number
  circuitOpen?: boolean
}

const props = withDefaults(defineProps<{
  channels: ChannelInfo[]
  currentChannel?: number
  nextChannel?: number
  nextChannelCircuitOpen?: boolean
  overrideActive?: boolean
}>(), {
  currentChannel: undefined,
  nextChannel: undefined,
  nextChannelCircuitOpen: false,
  overrideActive: false,
})

const emit = defineEmits<{
  moveToTop: [channel: ChannelInfo, index: number]
  demote: [index: number]
}>()

const { t } = useLanguage()

function isDemoted(index: number): boolean {
  return props.overrideActive && index >= props.channels.length - 1
}

function badgeClass(channel: ChannelInfo): string {
  if (channel.index === props.currentChannel) return 'bg-primary text-primary-foreground'
  if (channel.index === props.nextChannel) {
    return props.nextChannelCircuitOpen ? 'bg-red-500 text-white' : 'bg-emerald-500 text-white'
  }
  return 'border border-border bg-muted/30 text-muted-foreground'
}

function getMoveToTopTooltip(channel: ChannelInfo, index: number): string {
  // 当前渠道 + 已固定（override + 在序列首位）→ 点击取消固定
  if (channel.index === props.currentChannel && props.overrideActive && index === 0) {
    return t('cockpit.tooltip.quickPinned')
  }
  // 当前渠道 + 未固定 → 点击固定
  if (channel.index === props.currentChannel && !props.overrideActive) {
    return t('cockpit.tooltip.quickCurrentChannel')
  }
  // 当前渠道 + override 但不在首位 → 移到首位固定
  if (channel.index === props.currentChannel) {
    return t('cockpit.tooltip.moveChannelToTop', { name: channel.name })
  }
  // 已在首位
  if (index === 0 && channel.status !== 'suspended' && !channel.circuitOpen) {
    return t('cockpit.tooltip.channelAlreadyFirst', { name: channel.name })
  }
  return t('cockpit.tooltip.moveChannelToTop', { name: channel.name })
}

function getDemoteTooltip(channel: ChannelInfo, index: number): string {
  if (index === props.channels.length - 1) return t('cockpit.tooltip.demoteChannelDisabled')
  return t('cockpit.tooltip.demoteChannel', { name: channel.name })
}
</script>

<template>
  <div class="channel-sequence" @click.stop>
    <div
      v-for="(channel, index) in channels"
      :key="channel.index"
      class="channel-item channel-item-animated flex items-center gap-1 border-b border-border/60 px-2 py-1.5 last:border-b-0"
      :class="{ demoted: isDemoted(index) }"
      :style="{ animationDelay: `${Math.min(index, 12) * 35}ms` }"
    >
      <span class="seq-num">{{ String(index + 1).padStart(2, '0') }}</span>
      <span class="seq-arrow">→</span>
      <Tooltip>
        <TooltipTrigger as-child>
          <button
            type="button"
            class="channel-name min-w-0 flex-1 truncate text-left text-xs"
            @click.stop="emit('moveToTop', channel, index)"
          >
            {{ channel.name }}
          </button>
        </TooltipTrigger>
        <TooltipContent side="top">{{ getMoveToTopTooltip(channel, index) }}</TooltipContent>
      </Tooltip>
      <Tooltip
        v-if="channel.index === currentChannel"
      >
        <TooltipTrigger as-child>
          <span
            class="sequence-badge"
            :class="badgeClass(channel)"
          >
            CURRENT
          </span>
        </TooltipTrigger>
        <TooltipContent side="top">{{ t('cockpit.tooltip.currentChannel') }}</TooltipContent>
      </Tooltip>
      <Tooltip
        v-else-if="channel.index === nextChannel"
      >
        <TooltipTrigger as-child>
          <span
            class="sequence-badge next-channel-badge"
            :class="badgeClass(channel)"
          >
            {{ nextChannelCircuitOpen ? 'TRIPPED' : 'NEXT' }}
          </span>
        </TooltipTrigger>
        <TooltipContent side="top">
          {{ nextChannelCircuitOpen ? t('cockpit.tooltip.nextChannelTripped') : t('cockpit.tooltip.nextChannel') }}
        </TooltipContent>
      </Tooltip>
      <Tooltip
        v-if="channel.status === 'suspended'"
      >
        <TooltipTrigger as-child>
          <span
            class="sequence-badge fused-chip text-white"
          >
            PAUSED
          </span>
        </TooltipTrigger>
        <TooltipContent side="top">{{ t('cockpit.tooltip.pausedChannel') }}</TooltipContent>
      </Tooltip>
      <Tooltip
        v-if="channel.circuitOpen"
      >
        <TooltipTrigger as-child>
          <span
            class="sequence-badge bg-red-500 text-white"
          >
            TRIPPED
          </span>
        </TooltipTrigger>
        <TooltipContent side="top">{{ t('cockpit.tooltip.circuitOpen') }}</TooltipContent>
      </Tooltip>
      <Tooltip>
        <TooltipTrigger as-child>
          <span class="sequence-action-wrapper">
            <button
              type="button"
              class="sequence-action"
              :disabled="index === channels.length - 1"
              :aria-label="getDemoteTooltip(channel, index)"
              @click.stop="emit('demote', index)"
            >
              <ArrowDown class="h-3.5 w-3.5" />
            </button>
          </span>
        </TooltipTrigger>
        <TooltipContent side="top">{{ getDemoteTooltip(channel, index) }}</TooltipContent>
      </Tooltip>
    </div>
  </div>
</template>

<style scoped>
.channel-sequence {
  max-height: calc(5 * 36px);
  overflow-x: hidden;
  overflow-y: auto;
  border: 1px solid var(--color-border);
  border-radius: 0;
  overscroll-behavior: auto;
}

.channel-item-animated {
  animation: ccx-slide-in 0.18s ease both;
}

.channel-item.demoted {
  opacity: 0.5;
}

.seq-num {
  min-width: 2.5ch;
  font-variant-numeric: tabular-nums;
  font-size: 10px;
  font-weight: 700;
  opacity: 0.5;
}

.seq-arrow {
  font-size: 10px;
  opacity: 0.35;
}

.channel-name:hover {
  color: var(--color-primary);
  text-decoration: underline;
}

.sequence-badge {
  display: inline-flex;
  align-items: center;
  padding: 1px 6px;
  border-radius: 0;
  font-size: 9px;
  font-weight: 800;
  letter-spacing: 0.05em;
}

.next-channel-badge {
  animation: ccx-breathe 2s ease-in-out infinite;
}

.fused-chip {
  border: none;
  background: repeating-linear-gradient(
    -45deg,
    rgba(239, 68, 68, 0.9) 0,
    rgba(239, 68, 68, 0.9) 4px,
    rgba(127, 29, 29, 0.9) 4px,
    rgba(127, 29, 29, 0.9) 8px
  );
}

.sequence-action {
  display: inline-flex;
  width: 24px;
  height: 24px;
  flex: 0 0 24px;
  align-items: center;
  justify-content: center;
  border: 0;
  background: transparent;
  color: var(--color-foreground);
  cursor: pointer;
  opacity: 0.55;
}

.sequence-action-wrapper {
  display: inline-flex;
  width: 24px;
  height: 24px;
  flex: 0 0 24px;
}

.sequence-action:hover:not(:disabled) {
  background: var(--color-accent);
  opacity: 1;
}

.sequence-action:disabled {
  cursor: default;
  opacity: 0.2;
}

@keyframes ccx-breathe {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.55; }
}

@keyframes ccx-slide-in {
  from { opacity: 0; transform: translateX(-6px); }
  to { opacity: 1; transform: translateX(0); }
}
</style>
