<script setup lang="ts">
import { computed, ref } from 'vue'
import { Button } from '@/components/ui/button'
import { Input } from '@/components/ui/input'
import { Label } from '@/components/ui/label'
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from '@/components/ui/select'
import { Plus, Trash2, Waves } from 'lucide-vue-next'
import { useLanguage } from '@/composables/useLanguage'
import {
  createEmbeddingCapabilityRow,
  normalizeSelectableString,
  type EmbeddingCapabilityRow,
} from '@/utils/channel-payload'

const props = defineProps<{
  rows: EmbeddingCapabilityRow[]
  targetModels: Array<{ title: string; value: string }>
  fetchingModels: boolean
  fetchModelsError: string
  error: string
}>()

const emit = defineEmits<{
  'update:rows': [rows: EmbeddingCapabilityRow[]]
  'sync-upstream-models': []
}>()

const { t } = useLanguage()
const newModel = ref('')
const activeModelInputId = ref('')
const normalizedTargetModels = computed(() => {
  const seen = new Set<string>()
  return props.targetModels
    .map(m => m.value.trim())
    .filter(m => {
      if (!m || seen.has(m)) return false
      seen.add(m)
      return true
    })
})
const modelListEmptyHint = computed(() => props.fetchModelsError || t('addChannel.modelListEmptyHint'))
const newModelName = computed(() => normalizeSelectableString(newModel.value).trim())

const normalizedUnsetValue = '__ccx_embedding_normalized_unset__'
const normalizedOptions = computed(() => [
  { label: t('addChannel.embeddingNormalizedUnknown'), value: normalizedUnsetValue },
  { label: t('addChannel.embeddingNormalizedTrue'), value: 'true' },
  { label: t('addChannel.embeddingNormalizedFalse'), value: 'false' },
])

function normalizedSelectValue(value: EmbeddingCapabilityRow['normalized']) {
  return value || normalizedUnsetValue
}

function normalizeSelectedValue(value: unknown): EmbeddingCapabilityRow['normalized'] {
  const selected = String(value)
  return selected === normalizedUnsetValue ? '' : selected as EmbeddingCapabilityRow['normalized']
}

function updateRows(rows: EmbeddingCapabilityRow[]) {
  emit('update:rows', rows)
}

function updateRow(index: number, patch: Partial<EmbeddingCapabilityRow>) {
  updateRows(props.rows.map((row, i) => (i === index ? { ...row, ...patch } : row)))
}

function addRow() {
  const model = newModelName.value
  if (!model) return
  if (props.rows.some(row => normalizeSelectableString(row.model).trim().toLowerCase() === model.toLowerCase())) {
    newModel.value = ''
    return
  }
  updateRows([...props.rows, createEmbeddingCapabilityRow(Date.now() + Math.floor(Math.random() * 1000), model)])
  newModel.value = ''
  activeModelInputId.value = ''
}

function removeRow(index: number) {
  updateRows(props.rows.filter((_, i) => i !== index))
}

function modelInitial(model: string) {
  const source = (model || '?').trim()
  return source ? source.slice(0, 1).toUpperCase() : '?'
}

function filteredModels(query: string) {
  const normalizedQuery = query.trim().toLowerCase()
  const candidates = normalizedQuery
    ? normalizedTargetModels.value.filter(m => m.toLowerCase().includes(normalizedQuery))
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
</script>

<template>
  <section class="space-y-4 rounded-xl border border-border/60 bg-background/60 p-4 shadow-sm backdrop-blur-sm">
    <div class="border-b border-border/40 pb-2">
      <div class="min-w-0 space-y-1">
        <div class="flex items-center gap-1.5 text-xs font-bold uppercase tracking-wider text-primary">
          <Waves class="h-3 w-3" />
          {{ t('addChannel.embeddingCompatibilityTitle') }}
        </div>
        <p class="text-[10px] leading-4 text-muted-foreground">
          {{ t('addChannel.embeddingCompatibilityHint') }}
        </p>
      </div>
    </div>

    <!-- Existing rows -->
    <div v-if="rows.length" class="space-y-2.5">
      <div class="flex items-center justify-between px-1 text-[10px] font-bold uppercase tracking-wider text-muted-foreground/60">
        <span>{{ t('addChannel.embeddingCompatibilityConfigured') }}</span>
        <span class="rounded-full border border-primary/20 bg-primary/10 px-2 py-0.5 font-mono text-primary">{{ rows.length }}</span>
      </div>

      <div
        v-for="(row, index) in rows"
        :key="row.id"
        class="overflow-hidden rounded-lg border border-border/60 bg-background/70 shadow-2xs"
      >
        <!-- Row header -->
        <div class="flex items-center justify-between gap-3 border-b border-border/50 bg-muted/30 px-3 py-2">
          <div class="flex min-w-0 items-center gap-2">
            <span class="flex h-7 w-7 shrink-0 items-center justify-center rounded-full border border-primary/20 bg-primary/10 text-xs font-bold text-primary">
              {{ modelInitial(row.model) }}
            </span>
            <div class="shrink-0 truncate font-mono text-xs font-bold text-foreground">
              {{ row.model || t('addChannel.embeddingCapabilityModelPlaceholder') }}
            </div>
          </div>
          <Button
            type="button"
            variant="ghost"
            size="icon-sm"
            class="h-8 w-8 shrink-0 text-destructive hover:bg-destructive/10"
            @click="removeRow(index)"
          >
            <Trash2 class="h-3.5 w-3.5" />
          </Button>
        </div>

        <!-- Row fields -->
        <div class="grid gap-3 p-3">
          <div class="grid gap-2 sm:grid-cols-2">
            <div class="space-y-1">
              <Label class="text-[10px] font-semibold uppercase text-muted-foreground/70">{{ t('addChannel.embeddingCapabilityModelLabel') }}</Label>
              <Input
                :model-value="row.model"
                class="h-8 font-mono text-xs"
                :placeholder="t('addChannel.embeddingCapabilityModelPlaceholder')"
                @update:model-value="(val) => updateRow(index, { model: String(val || '') })"
              />
            </div>
            <div class="space-y-1">
              <Label class="text-[10px] font-semibold uppercase text-muted-foreground/70">{{ t('addChannel.embeddingSpaceIdLabel') }}</Label>
              <Input
                :model-value="row.embeddingSpaceId"
                class="h-8 text-xs"
                :placeholder="t('addChannel.embeddingSpaceIdPlaceholder')"
                @update:model-value="(val) => updateRow(index, { embeddingSpaceId: String(val || '') })"
              />
            </div>
          </div>

          <div class="grid gap-2 sm:grid-cols-3">
            <div class="space-y-1">
              <Label class="text-[10px] font-semibold uppercase text-muted-foreground/70">{{ t('addChannel.embeddingDimensionsLabel') }}</Label>
              <Input
                :model-value="row.dimensions ?? ''"
                type="number"
                min="1"
                step="1"
                class="h-8 text-xs"
                @update:model-value="(val) => updateRow(index, { dimensions: val as string | number })"
              />
            </div>
            <div class="space-y-1">
              <Label class="text-[10px] font-semibold uppercase text-muted-foreground/70">{{ t('addChannel.embeddingNormalizedLabel') }}</Label>
              <Select
                :model-value="normalizedSelectValue(row.normalized)"
                @update:model-value="(val) => updateRow(index, { normalized: normalizeSelectedValue(val) })"
              >
                <SelectTrigger class="h-8 w-full text-xs">
                  <SelectValue />
                </SelectTrigger>
                <SelectContent>
                  <SelectItem v-for="opt in normalizedOptions" :key="opt.value" :value="opt.value">
                    {{ opt.label }}
                  </SelectItem>
                </SelectContent>
              </Select>
            </div>
            <div class="space-y-1 sm:col-span-1">
              <Label class="text-[10px] font-semibold uppercase text-muted-foreground/70">{{ t('addChannel.embeddingSupportedDimensionsLabel') }}</Label>
              <Input
                :model-value="row.supportedDimensionsText"
                class="h-8 text-xs"
                :placeholder="t('addChannel.embeddingSupportedDimensionsPlaceholder')"
                @update:model-value="(val) => updateRow(index, { supportedDimensionsText: String(val || '') })"
              />
            </div>
          </div>
        </div>
      </div>
    </div>

    <!-- Add new model row -->
    <div class="grid gap-2 rounded-lg border border-dashed border-primary/30 bg-primary/[0.03] p-3 md:grid-cols-[1fr_auto] md:items-end">
      <div class="relative min-w-0 space-y-1">
        <Label class="text-xs font-semibold text-muted-foreground">
          {{ t('addChannel.embeddingCapabilityModelLabel') }}
        </Label>
        <Input
          :model-value="newModel"
          class="h-9 font-mono text-xs"
          :placeholder="t('addChannel.embeddingCapabilityModelPlaceholder')"
          @focus="focusModelInput('new')"
          @blur="closeModelDropdownSoon"
          @update:model-value="(val) => { newModel = String(val || ''); activeModelInputId = 'new' }"
          @keydown.enter.prevent="addRow"
        />
        <div
          v-if="activeModelInputId === 'new' && filteredModels(newModel).length"
          class="absolute left-0 right-0 top-full z-30 mt-1 max-h-56 overflow-y-auto rounded-lg border border-border bg-popover p-1 shadow-lg"
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
          class="absolute left-0 right-0 top-full z-30 mt-1 rounded-lg border border-border bg-popover px-3 py-2 text-[10px] leading-4 text-muted-foreground shadow-lg"
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

    <!-- Errors -->
    <p v-if="error" class="text-[10px] leading-4 text-destructive">
      {{ error }}
    </p>
    <p v-if="fetchModelsError" class="text-[10px] leading-4 text-destructive">
      {{ fetchModelsError }}
    </p>
  </section>
</template>
