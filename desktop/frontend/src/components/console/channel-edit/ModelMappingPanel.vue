<script setup lang="ts">
import { computed, ref, watch, nextTick } from 'vue'
import type { ComponentPublicInstance } from 'vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Textarea } from '@/components/ui/textarea'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { ArrowLeftRight, ArrowRight, Eye, EyeOff, Plus, Trash2, Zap } from 'lucide-vue-next'
import { useLanguage } from '@/composables/useLanguage'

type ReasoningEffort = 'none' | 'low' | 'medium' | 'high' | 'xhigh' | 'max'

interface ModelMappingRow {
  id: number
  source: string
  target: string
  reasoning: ReasoningEffort | ''
  noVision: boolean
}

const props = defineProps<{
  modelMappingRows: ModelMappingRow[]
  newModelMapping: ModelMappingRow
  sourceModelOptions: string[]
  filteredSourceModels: string[]
  reasoningEffortOptions: Array<{ label: string; value: string }>
  filteredTargetModels: string[]
  channelType: string
  showTargetSuggestions: boolean
  activeTargetInputId: string | null
  showSourceSuggestions: boolean
  activeSourceInputId: string | null
  DEFAULT_SELECT_VALUE: string
  visionFallbackModel: string
  visionFallbackReasoningEffort: ReasoningEffort | ''
  supportedModelsText: string
  showModelMappingPresets: boolean
  showMessagesOpenAIChannelPresets: boolean
  showClaudeChannelPresets: boolean
  showCodexResponsesPresets: boolean
  supportsReasoningMappingOptions: boolean
  modelMappingHint: string
  targetModelPlaceholder: string
  commonSupportedModelFilters: string[]
  selectedSupportedModelSet: Set<string>
  sourceMappingError: string
  fetchModelsError: string
  supportedModelsError: string
}>()

const emit = defineEmits<{
  'update:newModelMapping': [value: Partial<ModelMappingRow>]
  'update:visionFallbackModel': [value: string]
  'update:visionFallbackReasoningEffort': [value: ReasoningEffort | '']
  'update:supportedModelsText': [value: string]
  'addModelMappingRow': []
  'removeModelMappingRow': [id: number]
  'updateMappingRow': [id: number, field: keyof ModelMappingRow, value: any]
  'syncUpstreamModels': []
  'applyPreset': [name: string]
  'showTargetDropdown': [inputId: string, currentValue: string]
  'hideTargetDropdown': []
  'selectTargetModel': [inputId: string, model: string]
  'handleTargetFocus': []
  'target-edit-start': []
  'target-edit-end': []
  'appendSupportedModelFilter': [filter: string]
  'showSourceDropdown': [inputId: string, currentValue: string]
  'hideSourceDropdown': []
  'selectSourceModel': [inputId: string, model: string]
}>()

const { t } = useLanguage()

const hasNoVisionRows = computed(() => props.modelMappingRows.some(row => row.noVision && row.target.trim()))
const isSupportedModelSelected = (filter: string) => props.selectedSupportedModelSet.has(filter)
const stableInputFocusClass = 'focus-visible:ring-0 focus-visible:ring-offset-0 focus-visible:border-primary/60 focus-visible:shadow-[inset_0_0_0_1px_hsl(var(--primary)/0.2)]'
const isSameModel = (a: string, b: string) => a.trim().toLowerCase() === b.trim().toLowerCase()

const sourceDropdownRef = ref<HTMLDivElement | null>(null)
const targetDropdownRefs = ref<Record<string, HTMLDivElement | null>>({})

// 当源模型下拉列表打开时，自动滚动到选中项
watch(() => props.showSourceSuggestions && props.activeSourceInputId === 'new-source', (isOpen) => {
  if (isOpen && props.newModelMapping.source) {
    nextTick(() => {
      const container = sourceDropdownRef.value
      if (!container) return
      const selectedButton = container.querySelector('[data-selected="true"]') as HTMLElement
      if (selectedButton) {
        selectedButton.scrollIntoView({ block: 'center', behavior: 'smooth' })
      }
    })
  }
})

function setTargetDropdownRef(inputId: string, el: Element | ComponentPublicInstance | null) {
  const element = el instanceof Element ? el : el?.$el
  targetDropdownRefs.value[inputId] = element instanceof HTMLDivElement ? element : null
}

// 当目标模型下拉列表打开时，按当前输入框自动滚动到选中项。
watch(() => props.showTargetSuggestions && props.activeTargetInputId, (inputId) => {
  if (!inputId) return
  nextTick(() => {
    const container = targetDropdownRefs.value[inputId]
    if (!container) return
    const selectedButton = container.querySelector('[data-selected="true"]') as HTMLElement | null
    selectedButton?.scrollIntoView({ block: 'center', behavior: 'smooth' })
  })
})

function toSelectValue(effort: ReasoningEffort | ''): string {
  return effort === '' ? props.DEFAULT_SELECT_VALUE : effort
}

function fromSelectValue(value: string): ReasoningEffort | '' {
  return value === props.DEFAULT_SELECT_VALUE ? '' : (value as ReasoningEffort)
}
</script>

<template>
  <section class="space-y-4 rounded-xl border border-primary/20 bg-gradient-to-b from-primary/[0.02] to-transparent p-5 shadow-sm">
    <div class="border-b border-border/40 pb-2">
      <div class="space-y-2">
        <div class="flex items-center justify-between gap-3">
          <h4 class="min-w-0 flex items-center gap-2 text-xs font-bold uppercase tracking-wider text-foreground">
            <ArrowLeftRight class="h-4 w-4 text-primary" />
            {{ t('channelEditor.mapping.redirect.title') }}
          </h4>
          <span class="shrink-0 rounded-full border border-primary/20 bg-primary/10 px-2.5 py-1 text-[10px] font-medium text-primary">
            {{ t('addChannel.autoConvertModelNames') }}
          </span>
        </div>
        <div class="space-y-1 text-xs leading-relaxed text-muted-foreground">
          <p>{{ modelMappingHint }}</p>
          <p class="text-primary">
            {{ t('addChannel.modelHintTip') }}
          </p>
        </div>
      </div>
    </div>

    <div
      v-if="showModelMappingPresets || showMessagesOpenAIChannelPresets || showClaudeChannelPresets || showCodexResponsesPresets"
      class="flex flex-wrap items-center gap-1.5"
    >
      <div class="flex items-center gap-1.5 text-[10px] text-muted-foreground">
        <Zap class="h-3 w-3 text-primary" />
        {{ t('addChannel.oneClickSetup') }}
      </div>
      <template v-if="showModelMappingPresets">
        <Button
          v-for="tag in ['gpt-5.5', 'gpt-5.4']"
          :key="'openai-' + tag"
          type="button"
          variant="outline"
          size="sm"
          class="h-6 rounded-md border border-border/70 bg-background px-2.5 text-[10px] font-medium text-muted-foreground hover:border-primary/40 hover:text-primary hover:bg-primary/5 shadow-3xs"
          @click="emit('applyPreset', tag)"
        >
          {{ tag }}
        </Button>
      </template>
      <template v-if="showMessagesOpenAIChannelPresets">
        <Button
          v-for="tag in ['MiMo', 'DeepSeek']"
          :key="'messages-openai-' + tag"
          type="button"
          variant="outline"
          size="sm"
          class="h-6 rounded-md border border-border/70 bg-background px-2.5 text-[10px] font-medium text-muted-foreground hover:border-primary/40 hover:text-primary hover:bg-primary/5 shadow-3xs"
          @click="emit('applyPreset', tag)"
        >
          {{ tag }}
        </Button>
      </template>
      <template v-if="showClaudeChannelPresets">
        <Button
          v-for="tag in ['MiMo', 'DeepSeek', 'MiniMax']"
          :key="'claude-' + tag"
          type="button"
          variant="outline"
          size="sm"
          class="h-6 rounded-md border border-border/70 bg-background px-2.5 text-[10px] font-medium text-muted-foreground hover:border-primary/40 hover:text-primary hover:bg-primary/5 shadow-3xs"
          @click="emit('applyPreset', tag)"
        >
          {{ tag }}
        </Button>
      </template>
      <template v-if="showCodexResponsesPresets">
        <Button
          v-for="tag in ['MiMo', 'DeepSeek', 'MiniMax']"
          :key="'codex-' + tag"
          type="button"
          variant="outline"
          size="sm"
          class="h-6 rounded-md border border-border/70 bg-background px-2.5 text-[10px] font-medium text-muted-foreground hover:border-primary/40 hover:text-primary hover:bg-primary/5 shadow-3xs"
          @click="emit('applyPreset', tag)"
        >
          {{ tag }}
        </Button>
      </template>
    </div>

    <!-- 已有映射列表 -->
    <div v-if="modelMappingRows.length" class="space-y-2.5">
      <div class="flex items-center justify-between text-[10px] font-bold uppercase tracking-wider text-muted-foreground/60 px-1">
        <span>{{ t('channelEditor.mapping.configured.label') }}</span>
        <span class="rounded-full bg-primary/10 border border-primary/20 px-2 py-0.5 font-mono text-primary">
          {{ modelMappingRows.length }}
        </span>
      </div>

      <div class="grid gap-2">
        <div
          v-for="(row, index) in modelMappingRows"
          :key="row.id"
          class="group grid grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)_auto_auto] items-center gap-3 rounded-xl border border-border/60 bg-background/50 p-2 shadow-2xs transition-all hover:border-primary/30 hover:bg-background/80"
        >
          <!-- SOURCE -->
          <div class="min-w-0 rounded-lg border border-border/50 bg-muted/40 px-3 py-1.5 h-10 flex flex-col justify-center">
            <span class="text-[8px] font-bold tracking-wider text-muted-foreground/50 leading-none">SOURCE</span>
            <span class="truncate font-mono text-xs font-semibold text-foreground mt-0.5" :title="row.source">
              {{ row.source }}
            </span>
          </div>

          <!-- Arrow -->
          <div class="flex justify-center">
            <span class="flex size-7 items-center justify-center rounded-full border border-primary/10 bg-primary/5 text-primary/70 transition-colors group-hover:border-primary/30 group-hover:bg-primary/10">
              <ArrowRight class="h-3.5 w-3.5" />
            </span>
          </div>

          <!-- TARGET -->
          <div class="relative min-w-0 space-y-0.5" data-target-model-picker>
            <span class="text-[8px] font-bold tracking-wider text-muted-foreground/50 block pl-1">TARGET</span>
            <Input
              :model-value="row.target"
              :class="[
                'h-8 w-full rounded-lg border border-border/70 bg-background px-2.5 font-mono text-xs outline-none transition-colors',
                stableInputFocusClass,
              ]"
              placeholder="target-model"
              @update:model-value="(val) => { emit('updateMappingRow', row.id, 'target', val as string); emit('showTargetDropdown', `row-${index}`, val as string) }"
              @focus="emit('target-edit-start'); emit('handleTargetFocus'); emit('showTargetDropdown', `row-${index}`, row.target)"
              @blur="emit('hideTargetDropdown')"
            />
            <div
              v-if="showTargetSuggestions && activeTargetInputId === `row-${index}` && filteredTargetModels.length"
              :ref="el => setTargetDropdownRef(`row-${index}`, el)"
              class="absolute left-0 right-0 top-full z-30 mt-1 max-h-52 overflow-y-auto rounded-lg border border-border bg-popover p-1 shadow-lg"
            >
              <button
                v-for="model in filteredTargetModels"
                :key="model"
                type="button"
                :data-selected="isSameModel(model, row.target)"
                class="flex w-full items-center rounded-md px-2 py-1.5 text-left font-mono text-xs text-popover-foreground hover:bg-accent hover:text-accent-foreground"
                :class="isSameModel(model, row.target) ? 'bg-primary/10 text-primary' : ''"
                @mousedown.prevent="emit('selectTargetModel', `row-${index}`, model)"
              >
                {{ model }}
              </button>
            </div>
          </div>

          <!-- VERBOSITY (Reasoning) -->
          <div v-if="supportsReasoningMappingOptions" class="space-y-0.5">
            <span class="text-[8px] font-bold tracking-wider text-muted-foreground/50 block pl-1">{{ t('channelEditor.mapping.reasoningEffort.label') }}</span>
            <Select
              :model-value="toSelectValue(row.reasoning)"
              @update:model-value="(val) => emit('updateMappingRow', row.id, 'reasoning', fromSelectValue(val as string))"
            >
              <SelectTrigger class="h-8 rounded-lg border border-border/70 bg-background/60 px-2.5 text-xs text-muted-foreground hover:text-foreground">
                <SelectValue />
              </SelectTrigger>
              <SelectContent>
                <SelectItem v-for="opt in reasoningEffortOptions" :key="opt.value" :value="opt.value">
                  {{ opt.label }}
                </SelectItem>
              </SelectContent>
            </Select>
          </div>

          <!-- Actions -->
          <div class="flex items-center gap-1 pl-1">
            <Button
              type="button"
              size="icon-sm"
              variant="ghost"
              class="h-7 w-7 rounded-full border border-border/50 bg-background/40 text-muted-foreground hover:border-amber-500/40 hover:bg-amber-500/5 hover:text-amber-500"
              :title="t('console.form.toggleVision')"
              @click="emit('updateMappingRow', row.id, 'noVision', !row.noVision)"
            >
              <EyeOff v-if="row.noVision" class="h-3.5 w-3.5" />
              <Eye v-else class="h-3.5 w-3.5" />
            </Button>
            <Button
              type="button"
              size="icon-sm"
              variant="ghost"
              class="h-7 w-7 rounded-full border border-border/50 bg-background/40 text-muted-foreground hover:border-destructive/30 hover:bg-destructive/5 hover:text-destructive"
              @click="emit('removeModelMappingRow', row.id)"
            >
              <Trash2 class="h-3.5 w-3.5" />
            </Button>
          </div>
        </div>
      </div>
    </div>

    <div v-if="hasNoVisionRows" class="space-y-1.5 rounded-xl border border-border/60 bg-card/30 p-4">
      <div class="grid gap-3 md:grid-cols-[minmax(0,1fr)_180px]">
        <div class="space-y-1.5">
          <Label class="text-xs font-semibold text-muted-foreground">
            {{ t('addChannel.visionFallbackLabel') }}
          </Label>
          <div class="relative" data-target-model-picker>
            <Input
              :model-value="visionFallbackModel"
              :class="['h-9 w-full font-mono text-xs', stableInputFocusClass]"
              :placeholder="t('addChannel.visionFallbackPlaceholder')"
              @update:model-value="(val) => { emit('update:visionFallbackModel', val as string); emit('showTargetDropdown', 'vision-fallback', val as string) }"
              @focus="emit('target-edit-start'); emit('handleTargetFocus'); emit('showTargetDropdown', 'vision-fallback', visionFallbackModel)"
              @blur="emit('hideTargetDropdown')"
            />
            <div
              v-if="showTargetSuggestions && activeTargetInputId === 'vision-fallback' && filteredTargetModels.length"
              :ref="el => setTargetDropdownRef('vision-fallback', el)"
              class="absolute left-0 right-0 top-full z-30 mt-1 max-h-52 overflow-y-auto rounded-lg border border-border bg-popover p-1 shadow-lg"
            >
              <button
                v-for="model in filteredTargetModels"
                :key="model"
                type="button"
                :data-selected="isSameModel(model, visionFallbackModel)"
                class="flex w-full items-center rounded-md px-2 py-1.5 text-left font-mono text-xs text-popover-foreground hover:bg-accent hover:text-accent-foreground"
                :class="isSameModel(model, visionFallbackModel) ? 'bg-primary/10 text-primary' : ''"
                @mousedown.prevent="emit('update:visionFallbackModel', model); emit('hideTargetDropdown')"
              >
                {{ model }}
              </button>
            </div>
          </div>
        </div>
        <div v-if="supportsReasoningMappingOptions" class="space-y-1.5">
          <Label class="text-xs font-semibold text-muted-foreground">
            {{ t('addChannel.visionFallbackReasoningLabel') }}
          </Label>
          <Select
            :model-value="toSelectValue(visionFallbackReasoningEffort)"
            @update:model-value="(val) => emit('update:visionFallbackReasoningEffort', fromSelectValue(val as string))"
          >
            <SelectTrigger class="h-9 rounded-lg border border-border/70 bg-background/60 px-3 text-xs text-muted-foreground">
              <SelectValue :placeholder="t('channelEditor.compat.selectDefault')" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem v-for="opt in reasoningEffortOptions" :key="opt.value" :value="opt.value">
                {{ opt.label }}
              </SelectItem>
            </SelectContent>
          </Select>
        </div>
      </div>
      <p class="text-[10px] leading-4 text-muted-foreground">
        {{ t('addChannel.visionFallbackHint') }}
      </p>
    </div>

    <div class="space-y-3 rounded-xl border border-primary/20 bg-primary/[0.02] p-4">
      <div class="grid grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)_auto_auto] items-end gap-3">
        <div class="relative min-w-0 space-y-1" data-source-model-picker>
          <Label class="text-xs font-semibold text-muted-foreground">
            {{ t('channelEditor.mapping.source.label') }}
          </Label>
          <Input
            :model-value="newModelMapping.source"
            :class="[
              'h-9 w-full rounded-lg border border-primary/20 bg-background px-3 font-mono text-xs outline-none transition-colors',
              stableInputFocusClass,
            ]"
            :placeholder="t('channelEditor.mapping.source.placeholder')"
            @update:model-value="(val) => { emit('update:newModelMapping', { source: val as string }); emit('showSourceDropdown', 'new-source', val as string) }"
            @focus="emit('syncUpstreamModels'); emit('showSourceDropdown', 'new-source', newModelMapping.source)"
            @blur="emit('hideSourceDropdown')"
          />
          <div
            v-if="showSourceSuggestions && activeSourceInputId === 'new-source' && filteredSourceModels.length"
            ref="sourceDropdownRef"
            class="absolute left-0 right-0 top-full z-30 mt-1 max-h-52 overflow-y-auto rounded-lg border border-border bg-popover p-1 shadow-lg"
          >
            <button
              v-for="model in filteredSourceModels"
              :key="model"
              type="button"
              :data-selected="isSameModel(model, newModelMapping.source)"
              class="flex w-full items-center rounded-md px-2 py-1.5 text-left font-mono text-xs text-popover-foreground hover:bg-accent hover:text-accent-foreground"
              :class="isSameModel(model, newModelMapping.source) ? 'bg-primary/10 text-primary' : ''"
              @mousedown.prevent="emit('selectSourceModel', 'new-source', model)"
            >
              {{ model }}
            </button>
          </div>
        </div>
        <div class="flex h-9 items-center text-primary/60">
          <ArrowRight class="h-3.5 w-3.5" />
        </div>
        <div class="relative min-w-0 space-y-1">
          <Label class="text-xs font-semibold text-muted-foreground">
            {{ t('channelEditor.mapping.target.label') }}
          </Label>
          <Input
            :model-value="newModelMapping.target"
            :class="[
              'h-9 w-full rounded-lg border border-primary/20 bg-background px-3 font-mono text-xs outline-none transition-colors',
              stableInputFocusClass,
            ]"
            :placeholder="targetModelPlaceholder"
            @update:model-value="(val) => { emit('update:newModelMapping', { target: val as string }); emit('showTargetDropdown', 'new', val as string) }"
            @focus="emit('target-edit-start'); emit('handleTargetFocus'); emit('showTargetDropdown', 'new', newModelMapping.target)"
            @blur="emit('hideTargetDropdown')"
          />
          <div
            v-if="showTargetSuggestions && activeTargetInputId === 'new' && filteredTargetModels.length"
            :ref="el => setTargetDropdownRef('new', el)"
            class="absolute left-0 right-0 top-full z-30 mt-1 max-h-52 overflow-y-auto rounded-lg border border-border bg-popover p-1 shadow-lg"
          >
            <button
              v-for="model in filteredTargetModels"
              :key="model"
              type="button"
              :data-selected="isSameModel(model, newModelMapping.target)"
              class="flex w-full items-center rounded-md px-2 py-1.5 text-left font-mono text-xs text-popover-foreground hover:bg-accent hover:text-accent-foreground"
              :class="isSameModel(model, newModelMapping.target) ? 'bg-primary/10 text-primary' : ''"
              @mousedown.prevent="emit('selectTargetModel', 'new', model)"
            >
              {{ model }}
            </button>
          </div>
        </div>
        <div v-if="supportsReasoningMappingOptions" class="space-y-1">
          <Label class="text-xs font-semibold text-muted-foreground">
            {{ t('channelEditor.mapping.reasoningEffort.label') }}
          </Label>
          <Select
            :model-value="toSelectValue(newModelMapping.reasoning)"
            @update:model-value="(val) => emit('update:newModelMapping', { reasoning: fromSelectValue(val as string) as any })"
          >
            <SelectTrigger class="h-9 rounded-lg border border-primary/20 bg-background px-3 text-xs text-muted-foreground">
              <SelectValue :placeholder="t('channelEditor.compat.selectDefault')" />
            </SelectTrigger>
            <SelectContent>
              <SelectItem v-for="opt in reasoningEffortOptions" :key="opt.value" :value="opt.value">
                {{ opt.label }}
              </SelectItem>
            </SelectContent>
          </Select>
        </div>
        <Button
          type="button"
          variant="default"
          size="sm"
          class="h-9 rounded-lg bg-primary hover:bg-primary/90 px-4 text-xs font-semibold text-primary-foreground shadow-sm"
          :disabled="!newModelMapping.source.trim() || !newModelMapping.target.trim() || !!sourceMappingError"
          @click="emit('addModelMappingRow')"
        >
          <Plus class="h-3.5 w-3.5 mr-1" />
          {{ t('common.add') }}
        </Button>
      </div>
      <div v-if="sourceMappingError" class="text-[10px] text-destructive">
        {{ sourceMappingError }}
      </div>
      <div v-if="fetchModelsError" class="text-[10px] text-destructive">
        {{ fetchModelsError }}
      </div>
    </div>

    <div class="space-y-2">
      <Label class="text-xs font-semibold text-muted-foreground">
        {{ t('addChannel.supportedModelsLabel') }}
      </Label>
      <Textarea
        :model-value="supportedModelsText"
        :placeholder="t('addChannel.supportedModelsPlaceholder')"
        class="w-full min-h-[64px] font-mono text-xs"
        :class="supportedModelsError ? 'border-destructive/40 focus-visible:ring-destructive/20' : ''"
        @update:model-value="(val) => emit('update:supportedModelsText', val as string)"
      />
      <p class="text-[10px] leading-4" :class="supportedModelsError ? 'text-destructive' : 'text-muted-foreground'">
        {{ supportedModelsError || t('addChannel.supportedModelsHint') }}
      </p>
      <div class="flex items-center flex-wrap gap-2">
        <span class="text-[10px] font-bold uppercase tracking-wider text-primary">
          {{ t('addChannel.commonFilters') }}
        </span>
        <Button
          v-for="filter in commonSupportedModelFilters"
          :key="filter"
          type="button"
          variant="outline"
          size="sm"
          class="h-6 rounded-md border border-border/70 bg-background px-2.5 text-[10px] font-medium text-muted-foreground hover:border-primary/40 hover:text-primary hover:bg-primary/5 shadow-3xs"
          :class="isSupportedModelSelected(filter) ? 'border-primary/40 text-primary' : ''"
          @click="emit('appendSupportedModelFilter', filter)"
        >
          {{ filter }}
        </Button>
      </div>
    </div>
  </section>
</template>
