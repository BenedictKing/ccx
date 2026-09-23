<script setup lang="ts">
import { computed, ref } from 'vue'
import { Badge } from '@/components/ui/badge'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Coins, Database, Plus, Trash2 } from 'lucide-vue-next'
import { useLanguage } from '@/composables/useLanguage'
import {
  createModelCapabilityRow,
  resolveBuiltinUpstreamModelCapability,
  type ModelCapabilityRow,
} from '@/utils/channel-payload'

const props = defineProps<{
  rows: ModelCapabilityRow[]
  targetModels: string[]
  fetchingModels: boolean
  fetchModelsError: string
  error: string
}>()

const emit = defineEmits<{
  'update:rows': [rows: ModelCapabilityRow[]]
  'sync-upstream-models': []
}>()

const { t } = useLanguage()
const newModel = ref('')
const activeModelInputId = ref('')
const thinkingModeOptions = ['thinking', 'extended', 'adaptive', 'adaptive_only', 'adaptive_always_on']
const reasoningEffortOptions = ['none', 'minimal', 'low', 'medium', 'high', 'xhigh', 'max']
const newModelName = computed(() => newModel.value.trim())
const thinkingDatalistId = `model-capability-thinking-${Math.random().toString(36).slice(2)}`
const reasoningDatalistId = `model-capability-reasoning-${Math.random().toString(36).slice(2)}`
const normalizedTargetModels = computed(() => {
  const seen = new Set<string>()
  return props.targetModels
    .map(model => model.trim())
    .filter(model => {
      if (!model || seen.has(model)) return false
      seen.add(model)
      return true
    })
})
const modelListEmptyHint = computed(() => props.fetchModelsError || t('addChannel.modelListEmptyHint'))

function updateRows(rows: ModelCapabilityRow[]) {
  emit('update:rows', rows)
}

function updateRow(id: number, patch: Partial<ModelCapabilityRow>) {
  updateRows(props.rows.map(row => row.id === id ? { ...row, ...patch } : row))
}

function formatTokens(value?: number) {
  if (!value) return ''
  if (value >= 1000 && value % 1000 === 0) return `${value / 1000}k`
  return value.toLocaleString()
}

function formatTokenMeta(value: ModelCapabilityRow['contextWindowTokens']) {
  const numericValue = Number(value)
  if (!Number.isFinite(numericValue) || numericValue <= 0) return ''
  const formatted = numericValue.toLocaleString()
  if (numericValue >= 1000000 && numericValue % 1000000 === 0) {
    return `${formatted} (${numericValue / 1000000}M)`
  }
  if (numericValue >= 1000 && numericValue % 1000 === 0) {
    return `${formatted} (${numericValue / 1000}K)`
  }
  return formatted
}

function pricingUnitLabel() {
  return t('addChannel.modelPricingUnitHint')
}

function modelInitial(row: ModelCapabilityRow) {
  const source = (row.model || '?').trim()
  return source ? source.slice(0, 1).toUpperCase() : '?'
}

function addRow() {
  const model = newModelName.value
  if (!model) return
  if (props.rows.some(row => row.model.trim() === model)) {
    newModel.value = ''
    return
  }
  const builtin = resolveBuiltinUpstreamModelCapability(model)
  updateRows([
    ...props.rows,
    createModelCapabilityRow(
      Date.now() + Math.floor(Math.random() * 1000),
      model,
      builtin?.capability,
      builtin ? 'builtin' : 'custom',
      builtin?.pattern || '',
    ),
  ])
  newModel.value = ''
  activeModelInputId.value = ''
}

function updateNewModel(model: string) {
  newModel.value = model
  activeModelInputId.value = 'new'
}

function filteredModels(query: string) {
  const normalizedQuery = query.trim().toLowerCase()
  const candidates = normalizedQuery
    ? normalizedTargetModels.value.filter(model => model.toLowerCase().includes(normalizedQuery))
    : normalizedTargetModels.value
  return candidates.slice(0, 80)
}

function focusModelInput(inputId: string) {
  activeModelInputId.value = inputId
  emit('sync-upstream-models')
}

function closeModelDropdownSoon() {
  window.setTimeout(() => {
    activeModelInputId.value = ''
  }, 120)
}

function selectNewModel(model: string) {
  newModel.value = model
  addRow()
}

function reasoningEfforts(row: ModelCapabilityRow) {
  return row.reasoningEffortsText
    .split(',')
    .map(item => item.trim())
    .filter(Boolean)
}

function setReasoningEfforts(row: ModelCapabilityRow, efforts: string[]) {
  const seen = new Set<string>()
  const reasoningEffortsText = efforts
    .map(item => item.trim())
    .filter(item => {
      if (!item || seen.has(item)) return false
      seen.add(item)
      return true
    })
    .join(', ')
  updateRow(row.id, { reasoningEffortsText })
}

function addReasoningEffort(row: ModelCapabilityRow, rawValue: string) {
  const nextEfforts = rawValue
    .split(',')
    .map(item => item.trim())
    .filter(Boolean)
  if (!nextEfforts.length) return
  setReasoningEfforts(row, [...reasoningEfforts(row), ...nextEfforts])
}

function removeReasoningEffort(row: ModelCapabilityRow, effort: string) {
  setReasoningEfforts(row, reasoningEfforts(row).filter(item => item !== effort))
}

function handleReasoningInput(row: ModelCapabilityRow, event: Event) {
  const input = event.target as HTMLInputElement
  if (input.value.includes(',')) {
    addReasoningEffort(row, input.value)
    input.value = ''
  }
}

function handleReasoningKeydown(row: ModelCapabilityRow, event: KeyboardEvent) {
  if (event.key !== 'Enter') return
  event.preventDefault()
  const input = event.target as HTMLInputElement
  addReasoningEffort(row, input.value)
  input.value = ''
}

function removeRow(id: number) {
  updateRows(props.rows.filter(row => row.id !== id))
}
</script>

<template>
  <section
    class="relative space-y-4 rounded-xl border border-border/60 bg-background/60 p-4 shadow-sm backdrop-blur-sm"
    :class="activeModelInputId ? 'z-50' : 'z-0'"
  >
    <div class="border-b border-border/40 pb-2">
      <div class="min-w-0 space-y-1">
        <div class="flex items-center gap-1.5 text-xs font-bold uppercase tracking-wider text-primary">
          <Database class="h-3 w-3" />
          {{ t('addChannel.contextCapabilityTitle') }}
        </div>
        <p class="text-[10px] leading-4 text-muted-foreground">
          {{ t('addChannel.modelCapabilitiesRowsHint') }}
        </p>
      </div>
    </div>

    <datalist :id="thinkingDatalistId">
      <option v-for="mode in thinkingModeOptions" :key="mode" :value="mode" />
    </datalist>
    <datalist :id="reasoningDatalistId">
      <option v-for="effort in reasoningEffortOptions" :key="effort" :value="effort" />
    </datalist>

    <div v-if="rows.length" class="space-y-2.5">
      <div class="flex items-center justify-between px-1 text-[10px] font-bold uppercase tracking-wider text-muted-foreground/60">
        <span>{{ t('addChannel.modelCapabilitiesConfigured') }}</span>
        <span class="rounded-full border border-primary/20 bg-primary/10 px-2 py-0.5 font-mono text-primary">{{ rows.length }}</span>
      </div>
      <div
        v-for="row in rows"
        :key="row.id"
        class="overflow-hidden rounded-lg border border-border/60 bg-background/70 shadow-2xs"
      >
        <div class="flex items-center justify-between gap-3 border-b border-border/50 bg-muted/30 px-3 py-2">
          <div class="flex min-w-0 items-center gap-2">
            <span class="flex h-7 w-7 shrink-0 items-center justify-center rounded-full border border-primary/20 bg-primary/10 text-xs font-bold text-primary">
              {{ modelInitial(row) }}
            </span>
            <div class="min-w-0 flex-1">
              <div class="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-1">
                <div class="shrink-0 truncate font-mono text-xs font-bold text-foreground">
                  {{ row.model || t('addChannel.modelCapabilityModelPlaceholder') }}
                </div>
                <Badge
                  v-if="row.source === 'builtin' && row.matchedPattern"
                  variant="outline"
                  class="min-w-0 max-w-full justify-start truncate rounded-md px-1.5 py-0 font-mono text-[10px] text-primary"
                >
                  <span class="truncate">
                    {{ t('addChannel.modelCapabilityBuiltinMatched', { pattern: row.matchedPattern }) }}
                  </span>
                </Badge>
              </div>
            </div>
          </div>
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            class="h-8 w-8 shrink-0 text-destructive hover:bg-destructive/10"
            @click="removeRow(row.id)"
          >
            <Trash2 class="h-3.5 w-3.5" />
          </Button>
        </div>

        <div class="grid gap-3 p-3">
          <div class="space-y-3">
            <div class="grid gap-2 sm:grid-cols-2">
              <div class="space-y-1">
                <Label class="text-[10px] font-semibold uppercase text-muted-foreground/70">{{ t('addChannel.contextTokensShort') }}</Label>
                <Input
                  :model-value="row.contextWindowTokens ?? ''"
                  type="number"
                  min="0"
                  class="h-8 text-xs"
                  @update:model-value="(val) => updateRow(row.id, { contextWindowTokens: val as string | number })"
                />
                <p v-if="formatTokenMeta(row.contextWindowTokens)" class="text-[10px] leading-4 text-muted-foreground">
                  {{ formatTokenMeta(row.contextWindowTokens) }}
                </p>
              </div>
              <div class="space-y-1">
                <Label class="text-[10px] font-semibold uppercase text-muted-foreground/70">{{ t('addChannel.outputTokensShort') }}</Label>
                <Input
                  :model-value="row.maxOutputTokens ?? ''"
                  type="number"
                  min="0"
                  class="h-8 text-xs"
                  @update:model-value="(val) => updateRow(row.id, { maxOutputTokens: val as string | number })"
                />
                <p v-if="formatTokenMeta(row.maxOutputTokens)" class="text-[10px] leading-4 text-muted-foreground">
                  {{ formatTokenMeta(row.maxOutputTokens) }}
                </p>
              </div>
            </div>

            <div class="grid gap-2 sm:grid-cols-2">
              <div class="space-y-1">
                <Label class="text-[10px] font-semibold uppercase text-muted-foreground/70">{{ t('addChannel.thinkingModeLabel') }}</Label>
                <Input
                  :model-value="row.thinkingMode"
                  :list="thinkingDatalistId"
                  class="h-8 font-mono text-xs"
                  @update:model-value="(val) => updateRow(row.id, { thinkingMode: String(val || '') })"
                />
              </div>
              <div class="space-y-1">
                <Label class="text-[10px] font-semibold uppercase text-muted-foreground/70">{{ t('addChannel.reasoningEffortsLabel') }}</Label>
                <div class="min-h-8 rounded-md border border-input bg-background px-2 py-1">
                  <div class="flex min-h-6 flex-wrap items-center gap-1">
                    <Badge
                      v-for="effort in reasoningEfforts(row)"
                      :key="effort"
                      variant="secondary"
                      class="gap-1 rounded-md px-1.5 py-0.5 font-mono text-[10px]"
                    >
                      {{ effort }}
                      <button
                        type="button"
                        class="text-muted-foreground hover:text-foreground"
                        @click="removeReasoningEffort(row, effort)"
                      >
                        ×
                      </button>
                    </Badge>
                    <input
                      :list="reasoningDatalistId"
                      class="min-w-20 flex-1 border-0 bg-transparent font-mono text-xs outline-none placeholder:text-muted-foreground"
                      :placeholder="reasoningEfforts(row).length ? '' : 'high, max'"
                      @input="handleReasoningInput(row, $event)"
                      @keydown="handleReasoningKeydown(row, $event)"
                      @blur="(event) => { addReasoningEffort(row, (event.target as HTMLInputElement).value); (event.target as HTMLInputElement).value = '' }"
                    />
                  </div>
                </div>
              </div>
            </div>

            <p v-if="row.description" class="text-[10px] leading-4 text-muted-foreground">
              {{ row.description }}
            </p>
            <p
              v-if="row.defaultOutputTokens || row.recommendedOutputTokens"
              class="text-[10px] leading-4 text-muted-foreground"
            >
              <span v-if="row.defaultOutputTokens">
                {{ t('addChannel.defaultOutputTokensMeta', { tokens: formatTokens(row.defaultOutputTokens) }) }}
              </span>
              <span v-if="row.defaultOutputTokens && row.recommendedOutputTokens"> · </span>
              <span v-if="row.recommendedOutputTokens">
                {{ t('addChannel.recommendedOutputTokensMeta', { tokens: formatTokens(row.recommendedOutputTokens) }) }}
              </span>
            </p>
          </div>

          <div class="space-y-2 rounded-lg border border-primary/10 bg-primary/[0.035] p-3">
            <div class="flex items-center justify-between gap-2">
              <div class="flex items-center gap-1 text-[10px] font-bold uppercase tracking-wider text-foreground">
                <Coins class="h-3.5 w-3.5 text-primary" />
                {{ t('addChannel.pricingTitle') }}
              </div>
              <span class="text-[10px] text-muted-foreground">{{ pricingUnitLabel() }}</span>
            </div>
            <div class="grid gap-2 sm:grid-cols-2 xl:grid-cols-[0.7fr_repeat(3,minmax(0,1fr))]">
              <div class="space-y-1">
                <Label class="text-[10px] font-semibold uppercase text-muted-foreground/70">{{ t('addChannel.pricingCurrencyLabel') }}</Label>
                <Input
                  :model-value="row.pricingCurrency"
                  class="h-8 text-xs"
                  @update:model-value="(val) => updateRow(row.id, { pricingCurrency: String(val || '') })"
                />
              </div>
              <div class="space-y-1">
                <Label class="text-[10px] font-semibold uppercase text-muted-foreground/70">{{ t('addChannel.inputCacheHitPriceLabel') }}</Label>
                <Input
                  :model-value="row.inputCacheHitPrice ?? ''"
                  type="number"
                  min="0"
                  step="0.000001"
                  class="h-8 text-xs"
                  @update:model-value="(val) => updateRow(row.id, { inputCacheHitPrice: val as string | number })"
                />
              </div>
              <div class="space-y-1">
                <Label class="text-[10px] font-semibold uppercase text-muted-foreground/70">{{ t('addChannel.inputCacheMissPriceLabel') }}</Label>
                <Input
                  :model-value="row.inputCacheMissPrice ?? ''"
                  type="number"
                  min="0"
                  step="0.000001"
                  class="h-8 text-xs"
                  @update:model-value="(val) => updateRow(row.id, { inputCacheMissPrice: val as string | number })"
                />
              </div>
              <div class="space-y-1">
                <Label class="text-[10px] font-semibold uppercase text-muted-foreground/70">{{ t('addChannel.outputPriceLabel') }}</Label>
                <Input
                  :model-value="row.outputPrice ?? ''"
                  type="number"
                  min="0"
                  step="0.000001"
                  class="h-8 text-xs"
                  @update:model-value="(val) => updateRow(row.id, { outputPrice: val as string | number })"
                />
              </div>
            </div>
          </div>
        </div>
      </div>
    </div>

    <div class="grid gap-2 rounded-lg border border-dashed border-primary/30 bg-primary/[0.03] p-3 md:grid-cols-[1fr_auto] md:items-end">
      <div class="relative min-w-0 space-y-1">
        <Label class="text-xs font-semibold text-muted-foreground">
          {{ t('addChannel.modelCapabilityModelLabel') }}
        </Label>
        <Input
          :model-value="newModel"
          class="h-9 font-mono text-xs"
          :placeholder="t('addChannel.modelCapabilityModelPlaceholder')"
          @focus="focusModelInput('new')"
          @blur="closeModelDropdownSoon"
          @update:model-value="(val) => updateNewModel(String(val || ''))"
          @keydown.enter.prevent="addRow"
        />
        <div
          v-if="activeModelInputId === 'new' && filteredModels(newModel).length"
          class="absolute left-0 right-0 top-full z-50 mt-1 max-h-56 overflow-y-auto rounded-lg border border-border bg-popover p-1 shadow-lg"
        >
          <button
            v-for="model in filteredModels(newModel)"
            :key="model"
            type="button"
            class="flex w-full items-center rounded-md px-2 py-1.5 text-left font-mono text-xs text-popover-foreground hover:bg-accent hover:text-accent-foreground"
            :class="model === newModel ? 'bg-primary/10 text-primary' : ''"
            @mousedown.prevent="selectNewModel(model)"
          >
            {{ model }}
          </button>
        </div>
        <div
          v-else-if="activeModelInputId === 'new'"
          class="absolute left-0 right-0 top-full z-50 mt-1 rounded-lg border border-border bg-popover px-3 py-2 text-[10px] leading-4 text-muted-foreground shadow-lg"
        >
          {{ modelListEmptyHint }}
        </div>
      </div>
      <Button
        type="button"
        variant="outline"
        size="sm"
        class="h-9 justify-self-start px-3.5 md:justify-self-auto"
        :disabled="!newModelName"
        @click="addRow"
      >
        <Plus class="h-4 w-4" />
      </Button>
    </div>

    <p v-if="error" class="text-[10px] leading-4 text-destructive">
      {{ error }}
    </p>
    <p v-if="fetchModelsError" class="text-[10px] leading-4 text-destructive">
      {{ fetchModelsError }}
    </p>

  </section>
</template>
