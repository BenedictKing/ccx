<template>
  <div class="channel-sequence" @click.stop>
    <div
      v-for="(ch, i) in channels"
      :key="ch.index"
      :class="['channel-item d-flex align-center pa-1 channel-item-animated', { demoted: isDemoted(i) }]"
      :style="{ animationDelay: `${Math.min(i, 12) * 35}ms` }"
    >
      <span class="seq-num">{{ String(i + 1).padStart(2, '0') }}</span>
      <span class="seq-arrow">&rarr;</span>
      <v-tooltip v-bind="channelTooltipProps" :text="getMoveToTopTooltip(ch, i)">
        <template #activator="{ props: tooltipProps }">
          <span v-bind="tooltipProps" class="text-caption flex-grow-1 channel-name" @click.stop="emit('moveToTop', ch, i)">{{ ch.name }}</span>
        </template>
      </v-tooltip>
      <v-tooltip v-if="ch.index === currentChannel" v-bind="channelTooltipProps" :text="t('cockpit.tooltip.currentChannel')">
        <template #activator="{ props: tooltipProps }">
          <v-chip v-bind="tooltipProps" size="x-small" color="primary" variant="flat" class="mr-1">CURRENT</v-chip>
        </template>
      </v-tooltip>
      <v-tooltip
        v-else-if="ch.index === nextChannel"
        :text="nextChannelCircuitOpen ? t('cockpit.tooltip.nextChannelTripped') : t('cockpit.tooltip.nextChannel')"
        v-bind="channelTooltipProps"
      >
        <template #activator="{ props: tooltipProps }">
          <v-chip
            v-bind="tooltipProps"
            size="x-small"
            :color="nextChannelCircuitOpen ? 'error' : 'success'"
            variant="flat"
            class="next-channel-chip mr-1"
          >
            {{ nextChannelCircuitOpen ? 'TRIPPED' : 'NEXT' }}
          </v-chip>
        </template>
      </v-tooltip>
      <v-tooltip v-if="ch.status === 'suspended'" v-bind="channelTooltipProps" :text="t('cockpit.tooltip.pausedChannel')">
        <template #activator="{ props: tooltipProps }">
          <v-chip v-bind="tooltipProps" size="x-small" variant="flat" class="fused-chip mr-1">PAUSED</v-chip>
        </template>
      </v-tooltip>
      <v-tooltip v-if="ch.circuitOpen" v-bind="channelTooltipProps" :text="t('cockpit.tooltip.circuitOpen')">
        <template #activator="{ props: tooltipProps }">
          <v-chip v-bind="tooltipProps" size="x-small" color="error" variant="tonal" class="mr-1">TRIPPED</v-chip>
        </template>
      </v-tooltip>
      <v-tooltip v-bind="channelTooltipProps" :text="getDemoteTooltip(ch, i)">
        <template #activator="{ props: tooltipProps }">
          <span v-bind="tooltipProps" class="sequence-action-wrapper">
            <button
              type="button"
              class="sequence-action"
              :disabled="i === channels.length - 1"
              :aria-label="getDemoteTooltip(ch, i)"
              @click.stop="emit('demote', i)"
            >
              <v-icon size="14">mdi-arrow-down</v-icon>
            </button>
          </span>
        </template>
      </v-tooltip>
    </div>
  </div>
</template>

<script setup lang="ts">
import { useI18n } from '@/i18n'

interface ChannelInfo {
  index: number
  name: string
  status: string
  circuitOpen?: boolean
}

const { t } = useI18n()

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

const channelTooltipProps = {
  attach: 'body',
  contentClass: 'ccx-tooltip',
  location: 'top',
  locationStrategy: 'connected',
  openDelay: 150,
  scrollStrategy: 'reposition',
} as const

function isDemoted(index: number): boolean {
  return props.overrideActive && index >= props.channels.length - 1
}

function getMoveToTopTooltip(ch: ChannelInfo, index: number): string {
  if (index === 0) return t('cockpit.tooltip.channelAlreadyFirst', { name: ch.name })
  return t('cockpit.tooltip.moveChannelToTop', { name: ch.name })
}

function getDemoteTooltip(ch: ChannelInfo, index: number): string {
  if (index === props.channels.length - 1) return t('cockpit.tooltip.demoteChannelDisabled')
  return t('cockpit.tooltip.demoteChannel', { name: ch.name })
}
</script>

<style scoped>
.channel-sequence {
  border: 1px solid rgba(var(--v-border-color), var(--v-border-opacity));
  border-radius: 0;
  overflow-x: hidden;
  overflow-y: auto;
  max-height: calc(5 * 36px);
  overscroll-behavior: auto;
}

.channel-item {
  border-bottom: 1px solid rgba(var(--v-border-color), calc(var(--v-border-opacity) * 0.6));
}

.channel-item:last-child {
  border-bottom: none;
}

.channel-item :deep(.v-chip__content) {
  font-size: 11px;
}

.channel-item-animated {
  animation: ccx-slide-in 0.18s ease both;
}

.channel-item.demoted {
  opacity: 0.5;
}

.seq-num {
  font-size: 11px;
  font-weight: 700;
  opacity: 0.65;
  min-width: 2.5ch;
  font-variant-numeric: tabular-nums;
}

.seq-arrow {
  font-size: 11px;
  opacity: 0.5;
  margin: 0 4px;
}

.channel-name {
  cursor: pointer;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.channel-name:hover {
  text-decoration: underline;
  color: rgb(var(--v-theme-primary));
}

.next-channel-chip {
  font-weight: 700;
  animation: ccx-breathe 2s ease-in-out infinite;
}

.next-channel-chip :deep(.v-chip__content),
.next-channel-chip :deep(.v-chip__append) {
  color: #fff !important;
}

.fused-chip {
  background: repeating-linear-gradient(
    -45deg,
    var(--ccx-fused-stripe-b) 0px,
    var(--ccx-fused-stripe-b) 4px,
    var(--ccx-fused-stripe-a) 4px,
    var(--ccx-fused-stripe-a) 8px
  ) !important;
  color: #fff !important;
  border-radius: 0 !important;
  border: none !important;
  font-weight: 700;
  font-size: 9px !important;
  letter-spacing: 0.05em;
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
  color: rgba(var(--v-theme-on-surface), 0.68);
  cursor: pointer;
}

.sequence-action-wrapper {
  display: inline-flex;
  width: 24px;
  height: 24px;
  flex: 0 0 24px;
}

.sequence-action:hover:not(:disabled) {
  background: rgba(var(--v-theme-on-surface), 0.08);
}

.sequence-action:disabled {
  cursor: default;
  opacity: 0.2;
}

@keyframes ccx-breathe {
  0%, 100% { opacity: 1; }
  50% { opacity: 0.55; }
}
</style>
