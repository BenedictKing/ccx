<template>
  <div class="model-routing-flow">
    <div class="routing-flow">
      <svg :viewBox="'0 0 ' + graph.width + ' ' + graph.height" :height="graph.height" role="group" :aria-label="t('routing.flowLabel')">
        <rect
          v-for="channel in graph.channels"
          :key="channel.key"
          :x="channel.x"
          :y="channel.y"
          :width="channel.width"
          :height="channel.height"
          rx="6"
          class="channel-background"
        />
        <g
          v-for="link in graph.links"
          :key="link.key"
          class="flow-link"
          role="button"
          tabindex="0"
          :aria-label="pathTitle(link.path)"
          @click="emit('path', link.path)"
          @keydown.enter.prevent="emit('path', link.path)"
          @keydown.space.prevent="emit('path', link.path)"
        >
          <title>{{ pathTitle(link.path) }} · {{ link.path.count }} · {{ share(link.path.count) }}</title>
          <path :d="link.ribbon" :class="'tone-' + link.tone" />
        </g>
        <g
          v-for="source in graph.sources"
          :key="source.key"
          class="flow-node flow-source"
          role="button"
          tabindex="0"
          :aria-label="t('routing.requestedModel') + ': ' + source.name"
          @click="select('requestedModel', source.name)"
          @keydown.enter.prevent="select('requestedModel', source.name)"
          @keydown.space.prevent="select('requestedModel', source.name)"
        >
          <title>{{ source.name || t('routing.unknownModel') }} · {{ source.count }}</title>
          <rect :x="source.x" :y="source.y" :width="source.width" :height="source.height" rx="4" class="source-box" />
          <text :x="source.x + 14" :y="source.y + 28">{{ shortName(source.name, 32) }}</text>
          <text :x="source.x + 14" :y="source.y + 51" class="node-count">
            {{ source.count }} · {{ share(source.count) }}
          </text>
        </g>
        <g
          v-for="channel in graph.channels"
          :key="channel.key"
          class="flow-channel"
          role="button"
          tabindex="0"
          :aria-label="t('routing.channelUid') + ': ' + channel.name"
          @click="select('channelUid', channel.key)"
          @keydown.enter.prevent="select('channelUid', channel.key)"
          @keydown.space.prevent="select('channelUid', channel.key)"
        >
          <title>{{ channel.name }}{{ channel.deleted ? t('routing.deleted') : '' }} · {{ channel.count }}</title>
          <rect
            :x="channel.x"
            :y="channel.y"
            :width="channel.width"
            :height="channel.height"
            rx="6"
            class="channel-outline"
          />
          <text :x="channel.x + 16" :y="channel.y + 30" class="channel-name">{{ shortName(channel.name, 28) }}</text>
          <text :x="channel.x + channel.width - 16" :y="channel.y + 30" text-anchor="end" class="node-count">
            {{ channel.count }} · {{ share(channel.count) }}
          </text>
          <g
            v-for="model in channel.models"
            :key="model.key"
            class="flow-node flow-model"
            role="button"
            tabindex="0"
            :aria-label="channel.name + ': ' + t('routing.sentModel') + ': ' + model.name"
            @click.stop="select('sentModel', model.name, channel.key)"
            @keydown.enter.stop.prevent="select('sentModel', model.name, channel.key)"
            @keydown.space.stop.prevent="select('sentModel', model.name, channel.key)"
          >
            <title>{{ channel.name }} → {{ model.name || t('routing.unknownModel') }} · {{ model.count }}</title>
            <rect :x="model.x" :y="model.y" :width="model.width" :height="model.height" rx="4" class="model-box" />
            <text :x="model.x + 14" :y="model.y + 27">{{ shortName(model.name, 40) }}</text>
            <text :x="model.x + 14" :y="model.y + 49" class="node-count">
              {{ model.count }} · {{ share(model.count) }}
            </text>
          </g>
        </g>
      </svg>
    </div>
    <div class="routing-mobile">
      <section v-for="channel in graph.channels" :key="channel.key" class="mobile-channel">
        <button class="mobile-channel-heading" @click="select('channelUid', channel.key)">
          <strong>{{ channel.name }}</strong
          ><span>{{ channel.count }} · {{ share(channel.count) }}</span>
        </button>
        <div v-for="model in channel.models" :key="model.key" class="mobile-model">
          <button class="mobile-model-heading" @click="select('sentModel', model.name, channel.key)">
            <strong>{{ model.name || t('routing.unknownModel') }}</strong
            ><span>{{ model.count }} · {{ share(model.count) }}</span>
          </button>
          <button
            v-for="link in graph.links.filter(
              link => link.path.channelUid === channel.key && link.path.sentModel === model.name
            )"
            :key="link.key"
            class="mobile-path"
            @click="emit('path', link.path)"
          >
            <span>{{ link.path.requestedModel || t('routing.unknownModel') }}</span
            ><span>{{ link.path.count }} →</span>
          </button>
        </div>
      </section>
    </div>
  </div>
</template>
<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from '../i18n'
import type { RoutingPath } from '../services/routing-types'
import { routingGraph, type RoutingField, type RoutingGraph } from '../utils/modelRouting'
const props = defineProps<{ paths: RoutingPath[]; total: number }>()
const emit = defineEmits<{
  path: [path: RoutingPath]
  select: [selection: { field: RoutingField; value: string; channelUid?: string }]
}>()
const { t } = useI18n()
const graph = computed<RoutingGraph>(previous => routingGraph(props.paths, previous))
const share = (count: number) => (props.total ? (count / props.total) * 100 : 0).toFixed(1) + '%'
function shortName(value: string, limit: number) {
  if (!value) return t('routing.unknownModel')
  let length = 0,
    visible = ''
  for (const char of value) {
    length += char.charCodeAt(0) > 255 ? 2 : 1
    if (length > limit) return visible + '…'
    visible += char
  }
  return visible
}
const pathTitle = (path: RoutingPath) => [path.requestedModel, path.channelName, path.sentModel].join(' → ')
function select(field: RoutingField, value: string, channelUid?: string) {
  emit('select', { field, value, channelUid })
}
</script>
<style scoped>
.routing-flow {
  max-height: 530px;
  overflow: auto;
  scrollbar-gutter: stable;
}
.routing-flow svg {
  display: block;
  width: 100%;
  min-width: 700px;
}
.routing-flow text {
  fill: rgb(var(--v-theme-on-surface));
  font-size: 12px;
  pointer-events: none;
  font-variant-numeric: tabular-nums;
}
.node-count {
  opacity: 0.65;
  font-size: 11px !important;
}
.channel-name {
  font-weight: 600;
}
.channel-background {
  fill: rgb(var(--v-theme-surface-variant));
  fill-opacity: 0.22;
  pointer-events: none;
}
.channel-outline {
  fill: transparent;
  stroke: rgba(var(--v-border-color), 0.4);
}
.source-box,
.model-box {
  fill: rgb(var(--v-theme-surface));
  stroke: rgba(var(--v-border-color), 0.5);
}
.flow-link,
.flow-node,
.flow-channel {
  cursor: pointer;
}
.flow-link path {
  fill: rgb(var(--v-theme-primary));
  fill-opacity: 0.45;
  stroke: rgb(var(--v-theme-surface));
  stroke-width: 1;
}
.flow-link .tone-1,
.flow-link .tone-4 {
  fill: rgb(var(--v-theme-secondary));
}
.flow-link .tone-2,
.flow-link .tone-5 {
  fill: rgb(var(--v-theme-info));
}
.flow-link:hover path,
.flow-link:focus path {
  fill-opacity: 0.75;
}
.flow-node:hover > rect {
  stroke: rgb(var(--v-theme-primary));
}
.flow-channel:hover > .channel-outline {
  stroke: rgba(var(--v-theme-primary), 0.6);
}
.flow-link:focus-visible,
.flow-node:focus-visible,
.flow-channel:focus-visible,
button:focus-visible {
  outline: 2px solid rgb(var(--v-theme-primary));
  outline-offset: 3px;
}
.routing-mobile {
  display: none;
}
.mobile-channel {
  display: grid;
  gap: 10px;
  padding: 12px;
  border: 1px solid rgba(var(--v-border-color), 0.4);
  border-radius: 6px;
  background: rgba(var(--v-theme-surface-variant), 0.15);
}
.mobile-channel-heading,
.mobile-model-heading,
.mobile-path {
  display: flex;
  justify-content: space-between;
  align-items: center;
  gap: 12px;
  width: 100%;
  text-align: left;
  font-size: 12px;
  overflow-wrap: anywhere;
}
.mobile-channel-heading strong,
.mobile-model-heading strong {
  min-width: 0;
}
.mobile-channel-heading span,
.mobile-model-heading span,
.mobile-path > span:last-child {
  flex-shrink: 0;
  font-variant-numeric: tabular-nums;
}
.mobile-model {
  display: grid;
  gap: 8px;
  padding: 10px;
  border: 1px solid rgba(var(--v-border-color), 0.3);
  border-radius: 4px;
  background: rgb(var(--v-theme-surface));
}
.mobile-path {
  color: rgb(var(--v-theme-on-surface-variant));
}
@media (max-width: 700px) {
  .routing-flow {
    display: none;
  }
  .routing-mobile {
    display: grid;
    gap: 12px;
  }
}
</style>
